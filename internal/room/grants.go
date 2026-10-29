package room

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
)

// Turn grants (room-protocol s6 rule 1, token-control rule 2, decision P118). The scheduler in this package decides who may
// speak in a room (NextSpeakers); a grant is its written permission for one agent turn: this room, this thread, this agent,
// this round, until it expires, once. The Agent Runtime redeems the grant before it starts a room turn and refuses a room
// turn without one, so no path can start an agent in a room that the scheduler did not choose. Grants are stored
// (room_turn_grants) so a redemption is checked against what was issued, never against what a caller says it was issued.
//
// Which runs need one: an agent's turn in a room (a reply to a message, a turn of a group discussion). Task work, review and
// the template roles are started by the workflow engine, the team lead's and a recommendation's turns by their own services,
// and a person's chat and a connection test by the person: none of them is a turn in a room, and none needs a grant.

// DefaultGrantTTL is how long a grant can be redeemed. It covers the wait for a free agent slot (a queued agent shows Waiting);
// a design value.
const DefaultGrantTTL = 30 * time.Minute

// TurnGrant is the scheduler's permission for one agent turn in a room.
type TurnGrant struct {
	ID        string `json:"id"`
	RoomID    string `json:"roomId"`
	ProjectID string `json:"projectId"`
	ThreadID  string `json:"threadId,omitempty"` // "" for the room's top level
	TriggerID string `json:"triggerId"`
	AgentID   string `json:"agentId"`
	Round     int    `json:"round"`
	Reason    string `json:"reason"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// Grant errors. ErrNoGrant is a room turn started without a grant; ErrGrantRefused a grant that cannot be redeemed (never
// issued, for another room or agent, used, expired) or one the scheduler would not issue.
var (
	ErrNoGrant      = errors.New("room: an agent turn in a room needs a turn grant from the room's scheduler")
	ErrGrantRefused = errors.New("room: the turn grant is not valid")
)

// GrantTurns plans the turns after a message (PlanTurns) for agents replying in the room and issues a grant to every agent
// that may speak, returned by agent id. Two rules of replies are applied on top of the plan: in an auto room an agent's message
// wakes only the members it explicitly mentions (the owner and role fallbacks are for a person's messages; chaining them
// agent to agent would spend turns nobody asked for), and in a direct message a person wrote, the DM's agent answers when
// nobody else was addressed (a DM is one person and one agent).
func (s *Service) GrantTurns(ctx context.Context, messageID string) (TurnDecision, map[string]TurnGrant, error) {
	dec, err := s.PlanTurns(ctx, messageID)
	if err != nil {
		return dec, nil, err
	}
	trig, err := s.GetMessage(ctx, messageID)
	if err != nil {
		return dec, nil, err
	}
	rm, err := s.GetRoom(ctx, trig.RoomID)
	if err != nil {
		return dec, nil, err
	}
	if trig.Author.Kind == AuthorAgent && rm.Mode == ModeAuto {
		var keep []Speaker
		for _, sp := range dec.Speakers {
			if sp.Reason == ReasonMention {
				keep = append(keep, sp)
			}
		}
		dec.Speakers = keep
	}
	if len(dec.Speakers) == 0 && !dec.Bounded && !rm.Archived && rm.Kind == KindDM && trig.Author.Kind == AuthorHuman {
		if ms, err := s.Members(ctx, rm.ID); err == nil {
			for _, m := range ms {
				if m.Author.Kind == AuthorAgent && (m.State == StateActive || m.State == "") {
					dec.Speakers = []Speaker{{Member: m.Author.Key(), Reason: "dm"}}
					break
				}
			}
		}
	}
	grants := map[string]TurnGrant{}
	for _, sp := range dec.Speakers {
		g, err := s.issue(ctx, rm, trig, sp.Member, dec.Round, string(sp.Reason), "")
		if err != nil {
			return dec, nil, err
		}
		grants[sp.Member] = g
	}
	return dec, grants, nil
}

// GroupTurn is a bounded discussion between named agents in one thread (a group-collaboration workflow node).
type GroupTurn struct {
	RoomID string
	Thread string   // the discussion's root message
	Order  []string // agent ids, in speaking order
	Rounds int      // the bound, default DefaultMaxRounds
}

// GrantGroupTurn plans the next turn of a group discussion in sequential mode with the discussion's own order and bound (the
// same scheduler as a sequential room, room-protocol s6 rule 7) and grants it. With the bound reached, or no one left to
// speak, the decision says so and no grant is issued.
func (s *Service) GrantGroupTurn(ctx context.Context, gt GroupTurn) (TurnDecision, *TurnGrant, error) {
	rm, err := s.GetRoom(ctx, gt.RoomID)
	if err != nil {
		return TurnDecision{}, nil, err
	}
	if rm.Archived {
		return TurnDecision{Why: "room archived"}, nil, nil
	}
	ms, err := queryMessages(ctx, s.db.Reader(), `room_id=? AND (id=? OR thread_id=?) ORDER BY seq`, gt.RoomID, gt.Thread, gt.Thread)
	if err != nil {
		return TurnDecision{}, nil, err
	}
	if len(ms) == 0 {
		return TurnDecision{}, nil, fmt.Errorf("%w: thread %s", ErrNotFound, gt.Thread)
	}
	members, err := s.Members(ctx, gt.RoomID)
	if err != nil {
		return TurnDecision{}, nil, err
	}
	tm := make([]TurnMember, len(members))
	for i, m := range members {
		tm[i] = TurnMember{Key: m.Author.Key(), Kind: m.Author.Kind, Role: m.Role, State: m.State}
	}
	rounds := gt.Rounds
	if rounds <= 0 {
		rounds = DefaultMaxRounds
	}
	last := ms[len(ms)-1]
	in := TurnInput{Mode: ModeSequential, Params: ModeParams{Order: gt.Order, MaxRounds: rounds}, Members: tm,
		Trigger: TurnTrigger{MessageID: last.ID, Author: last.Author, Type: last.Type}}
	in.Spoken, in.RoundsCompleted = SequentialProgress(gt.Order, roundMsgs(ms))
	// the discussion is started by the workflow, not by a person: a human message in the thread resets the progress (rule 3),
	// and the progress decides, whoever wrote the last message
	dec := nextSequential(in, TurnDecision{}, rounds, false)
	if len(dec.Speakers) == 0 {
		return dec, nil, nil
	}
	g, err := s.issue(ctx, rm, last, dec.Speakers[0].Member, dec.Round, string(dec.Speakers[0].Reason), "")
	if err != nil {
		return dec, nil, err
	}
	return dec, &g, nil
}

// RenewGrant issues a fresh grant for the same turn as a used one: a fallback hop runs the turn again on another model, as a
// new agent session (the model router's chain, P63), and each session start redeems a grant of its own. Only a grant that was
// used can be renewed, and only once per hop.
func (s *Service) RenewGrant(ctx context.Context, used TurnGrant) (TurnGrant, error) {
	var usedAt int64
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT used_at FROM room_turn_grants WHERE id=? AND room_id=? AND agent_id=?`, used.ID, used.RoomID, used.AgentID).Scan(&usedAt); err != nil {
		return TurnGrant{}, fmt.Errorf("%w: %s was not issued by the scheduler", ErrGrantRefused, used.ID)
	}
	if usedAt == 0 {
		return TurnGrant{}, fmt.Errorf("%w: %s has not been used, so there is nothing to renew", ErrGrantRefused, used.ID)
	}
	rm, err := s.GetRoom(ctx, used.RoomID)
	if err != nil {
		return TurnGrant{}, err
	}
	trig, err := s.GetMessage(ctx, used.TriggerID)
	if err != nil {
		return TurnGrant{}, err
	}
	return s.issue(ctx, rm, trig, used.AgentID, used.Round, used.Reason, used.ID)
}

// issue stores a grant for agentID after trig: the agent must be a member of the room that may speak (not muted).
func (s *Service) issue(ctx context.Context, rm Room, trig Message, agentID string, round int, reason, renews string) (TurnGrant, error) {
	if rm.Archived {
		return TurnGrant{}, fmt.Errorf("%w: %s is archived", ErrGrantRefused, rm.Name)
	}
	ok := false
	if ms, err := s.Members(ctx, rm.ID); err == nil {
		for _, m := range ms {
			if m.Author.Kind == AuthorAgent && m.Author.ID == agentID && m.State != StateMuted {
				ok = true
			}
		}
	}
	if !ok {
		return TurnGrant{}, fmt.Errorf("%w: %s is not a member of %s that may speak", ErrGrantRefused, agentID, rm.Name)
	}
	thread := trig.ThreadRootID
	if thread == "" {
		thread = trig.ID // a reply to a top-level message starts its thread
	}
	now := s.nowMs()
	g := TurnGrant{ID: ids.New("grant"), RoomID: rm.ID, ProjectID: rm.ProjectID, ThreadID: thread, TriggerID: trig.ID, AgentID: agentID, Round: round, Reason: reason,
		IssuedAt: now, ExpiresAt: now + DefaultGrantTTL.Milliseconds()}
	if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO room_turn_grants(id,room_id,project_id,thread_id,trigger_id,agent_id,round,reason,renews,issued_at,expires_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, g.ID, g.RoomID, g.ProjectID, g.ThreadID, g.TriggerID, g.AgentID, g.Round, g.Reason, renews, g.IssuedAt, g.ExpiresAt)
		return err
	}); err != nil {
		return TurnGrant{}, err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.turn_granted", scope: events.Event{ProjectID: rm.ProjectID, TaskID: rm.TaskID, AgentID: agentID},
		data: map[string]any{"room": rm.ID, "grant": g.ID, "member": agentID, "round": round, "reason": reason, "thread": thread}}})
	return g, nil
}

// RedeemGrant uses a grant to start one agent run (the Agent Runtime calls it before the agent's process starts). The grant
// must be one the scheduler issued, unchanged, for this room and this agent, unused and unexpired; it is then used, by runID.
func (s *Service) RedeemGrant(ctx context.Context, g TurnGrant, roomID, agentID, runID string) error {
	if g.ID == "" {
		return ErrNoGrant
	}
	if g.RoomID != roomID || g.AgentID != agentID {
		return fmt.Errorf("%w: the grant is for %s in room %s, not %s in room %s", ErrGrantRefused, g.AgentID, g.RoomID, agentID, roomID)
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var st TurnGrant
		var usedAt int64
		err := tx.QueryRowContext(ctx, `SELECT room_id,agent_id,thread_id,trigger_id,round,expires_at,used_at FROM room_turn_grants WHERE id=?`, g.ID).
			Scan(&st.RoomID, &st.AgentID, &st.ThreadID, &st.TriggerID, &st.Round, &st.ExpiresAt, &usedAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("%w: %s was not issued by the scheduler", ErrGrantRefused, g.ID)
		case err != nil:
			return err
		case st.RoomID != g.RoomID || st.AgentID != g.AgentID || st.ThreadID != g.ThreadID || st.TriggerID != g.TriggerID || st.Round != g.Round:
			return fmt.Errorf("%w: %s does not match what the scheduler issued", ErrGrantRefused, g.ID)
		case usedAt != 0:
			return fmt.Errorf("%w: %s was already used", ErrGrantRefused, g.ID)
		case st.ExpiresAt <= s.nowMs():
			return fmt.Errorf("%w: %s expired", ErrGrantRefused, g.ID)
		}
		res, err := tx.ExecContext(ctx, `UPDATE room_turn_grants SET used_at=?, agent_run_id=? WHERE id=? AND used_at=0`, s.nowMs(), runID, g.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("%w: %s was already used", ErrGrantRefused, g.ID)
		}
		return nil
	})
}
