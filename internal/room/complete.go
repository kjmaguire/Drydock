package room

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kjmaguire/drydock/internal/events"
)

// The end of a granted turn (room-protocol s6 TurnScheduler.Complete, decision P130). The scheduler issued the grant; the
// Agent Runtime used it; Complete is how the scheduler hears that the turn ended and decides what follows, in one place: the
// grant is closed with the turn's outcome and its reply (a grant is completed once, and one that was never used can no longer
// start a turn), room.turn_completed says so, and the reply of a turn that posted one is the message the next speakers are
// planned from (GrantTurns, GrantGroupTurn). The responder's room_turns row keeps what the turn cost; the grant keeps what
// the scheduler decided and how it ended.

// Turn outcomes a completion records. They are the responder's turn statuses, plus OutcomeRenewed for the used grant of a
// fallback hop (the same turn goes on, on the renewed grant).
const (
	OutcomeDone        = "done"
	OutcomePassed      = "passed"
	OutcomeEmpty       = "empty"
	OutcomeFailed      = "failed"
	OutcomeStopped     = "stopped" // a person stopped the agent that was answering
	OutcomeInterrupted = "interrupted"
	OutcomeRenewed     = "renewed"
)

var turnOutcomes = map[string]bool{OutcomeDone: true, OutcomePassed: true, OutcomeEmpty: true, OutcomeFailed: true, OutcomeStopped: true, OutcomeInterrupted: true}

// TurnResult is how a granted turn ended.
type TurnResult struct {
	Status    string // one of the outcomes above (not OutcomeRenewed)
	MessageID string // the reply the turn posted, "" when it posted none
	RunID     string // the agent run of the turn's last attempt
	Error     string
}

// TurnCompletion is what the scheduler decided at the end of a turn.
type TurnCompletion struct {
	GrantID   string `json:"grantId"`
	Round     int    `json:"round"`
	MaxRounds int    `json:"maxRounds"`
	Status    string `json:"status"`
	// Next is the message the next speakers are planned from: the turn's reply when it posted one (done or passed). "" when
	// the end wakes nobody: an empty, failed, stopped or interrupted turn ends its chain.
	Next string `json:"next,omitempty"`
}

// Complete records the end of the turn a grant permitted. The grant must be one the scheduler issued, for the room and agent
// it names, not completed before; a reply it names must be the agent's own message in that room. A grant that was never used
// (the turn failed before its agent started: a budget, no slot, the context) is closed by its completion.
func (s *Service) Complete(ctx context.Context, g TurnGrant, res TurnResult) (TurnCompletion, error) {
	if g.ID == "" {
		return TurnCompletion{}, ErrNoGrant
	}
	if !turnOutcomes[res.Status] {
		return TurnCompletion{}, fmt.Errorf("%w: turn outcome %q", ErrInvalid, res.Status)
	}
	out := TurnCompletion{GrantID: g.ID, Status: res.Status}
	var projectID string
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		st, err := loadIssued(ctx, tx, g.ID)
		if err != nil {
			return err
		}
		if st.RoomID != g.RoomID || st.AgentID != g.AgentID {
			return fmt.Errorf("%w: %s is not the grant of %s in room %s", ErrGrantRefused, g.ID, g.AgentID, g.RoomID)
		}
		var completed int64
		if err := tx.QueryRowContext(ctx, `SELECT completed_at, project_id FROM room_turn_grants WHERE id=?`, g.ID).Scan(&completed, &projectID); err != nil {
			return err
		}
		if completed != 0 {
			return fmt.Errorf("%w: the turn of %s was already completed", ErrGrantRefused, g.ID)
		}
		reply := ""
		if res.MessageID != "" {
			var author string
			err := tx.QueryRowContext(ctx, `SELECT author FROM messages WHERE id=? AND room_id=?`, res.MessageID, g.RoomID).Scan(&author)
			if err == sql.ErrNoRows || (err == nil && author != Agent(g.AgentID).Key()) {
				return fmt.Errorf("%w: %s is not %s's message in this room", ErrInvalid, res.MessageID, g.AgentID)
			}
			if err != nil {
				return err
			}
			reply = res.MessageID
		}
		now := s.nowMs()
		if _, err := tx.ExecContext(ctx, `UPDATE room_turn_grants SET completed_at=?, outcome=?, reply_id=?,
			used_at=CASE WHEN used_at=0 THEN ? ELSE used_at END, agent_run_id=CASE WHEN agent_run_id='' THEN ? ELSE agent_run_id END WHERE id=?`,
			now, res.Status, reply, now, res.RunID, g.ID); err != nil {
			return err
		}
		out.Round, out.MaxRounds = st.Round, st.MaxRounds
		if reply != "" && (res.Status == OutcomeDone || res.Status == OutcomePassed) {
			out.Next = reply
		}
		return nil
	})
	if err != nil {
		return TurnCompletion{}, err
	}
	data := map[string]any{"room": g.RoomID, "grant": g.ID, "member": g.AgentID, "round": out.Round, "maxRounds": out.MaxRounds, "status": res.Status}
	if res.MessageID != "" {
		data["message"] = res.MessageID
	}
	s.flush(ctx, []pendingEvent{{typ: "room.turn_completed", scope: events.Event{ProjectID: projectID, AgentID: g.AgentID}, data: data}})
	return out, nil
}

// closeRenewed records that a used grant's turn goes on on a renewed grant (a fallback hop): its own part ended.
func (s *Service) closeRenewed(ctx context.Context, usedID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE room_turn_grants SET completed_at=?, outcome=? WHERE id=? AND completed_at=0`, s.nowMs(), OutcomeRenewed, usedID)
		return err
	})
}

// GrantOutcome is how a grant ended, as Complete recorded it ("" while its turn runs or before it started).
func (s *Service) GrantOutcome(ctx context.Context, grantID string) (outcome, replyID string, err error) {
	err = s.db.Reader().QueryRowContext(ctx, `SELECT outcome, reply_id FROM room_turn_grants WHERE id=?`, grantID).Scan(&outcome, &replyID)
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("%w: grant %s", ErrNotFound, grantID)
	}
	return outcome, replyID, err
}

// InterruptOpenGrants completes, as interrupted, every grant whose turn started and never ended: no agent process survives
// the app, so at start such a turn is over (the responder marks its turn row interrupted the same way). It returns how many.
func (s *Service) InterruptOpenGrants(ctx context.Context) (int, error) {
	var n int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE room_turn_grants SET completed_at=?, outcome=? WHERE used_at<>0 AND completed_at=0`, s.nowMs(), OutcomeInterrupted)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}
