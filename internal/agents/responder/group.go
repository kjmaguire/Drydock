package responder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The group-collaboration workflow node (brief s4, token-control rule 3, decision P118): a bounded discussion between named
// agents in a room. The topic is posted as the root of a thread; the room's scheduler then grants the turns in sequential
// order with the discussion's own bound (the round counter of a sequential room, default 3 rounds, room-protocol s6 rule
// 7), and each turn is a real agent turn through the Agent Runtime, read-only, over the context the room selects, posted in
// the thread, exactly as a reply. The discussion ends at the bound, or earlier when every agent in turn had nothing to add;
// then the scheduler's bound resolution decides how it ends: a DECISION an agent proposed stands as the candidate, or a
// blocking QUESTION asks a person. The node's output (DiscussionResult) is the next node's input, routed by its outcome.

// Discussion is one bounded discussion.
type Discussion struct {
	RoomID string
	Topic  string
	Agents []string // agent ids, in speaking order; each must already be a member of the room (agents never join on their own)
	Rounds int      // the bound; default room.DefaultMaxRounds
	// Key makes the topic idempotent (GroupNode uses the run, node and step), so an attempt run again after a crash continues
	// the same discussion instead of starting another.
	Key    string
	TaskID string
}

// Outcomes of a discussion (the node's routes).
const (
	OutcomeDecision = "decision"
	OutcomeQuestion = "question"
)

// DiscussionResult is how a discussion ended.
type DiscussionResult struct {
	RoomID     string     `json:"roomId"`
	ThreadID   string     `json:"threadId"`
	Rounds     int        `json:"rounds"`    // rounds held
	Bound      int        `json:"bound"`     // the bound
	Converged  bool       `json:"converged"` // every agent in turn passed before the bound
	Outcome    string     `json:"outcome"`   // OutcomeDecision or OutcomeQuestion
	DecisionID string     `json:"decisionId,omitempty"`
	Decision   string     `json:"decision,omitempty"` // the candidate's text
	QuestionID string     `json:"questionId,omitempty"`
	Turns      []TurnInfo `json:"turns"`
}

// ErrDiscussion is a discussion that could not be held (a turn failed, an agent is not in the room).
var ErrDiscussion = errors.New("group discussion")

// Discuss holds a discussion to its end.
func (r *Responder) Discuss(ctx context.Context, d Discussion) (DiscussionResult, error) {
	out := DiscussionResult{RoomID: d.RoomID, Bound: d.Rounds, Turns: []TurnInfo{}}
	if out.Bound <= 0 {
		out.Bound = room.DefaultMaxRounds
	}
	if len(d.Agents) < 2 {
		return out, fmt.Errorf("%w: a discussion needs at least two agents", ErrDiscussion)
	}
	if strings.TrimSpace(d.Topic) == "" {
		return out, fmt.Errorf("%w: no topic", ErrDiscussion)
	}
	rm, err := r.d.Room.GetRoom(ctx, d.RoomID)
	if err != nil {
		return out, err
	}
	names := make([]string, len(d.Agents))
	for i, id := range d.Agents {
		ok, err := r.d.Room.IsMember(ctx, rm.ID, room.Agent(id))
		if err != nil {
			return out, err
		}
		if !ok {
			return out, fmt.Errorf("%w: agent %s is not a member of %s; a person adds it first", ErrDiscussion, id, rm.Name)
		}
		names[i] = id
		if a, err := r.d.LookupAgent(ctx, id); err == nil && a.Name != "" {
			names[i] = a.Name
		}
	}
	topic, err := r.d.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.TypeSystem, Author: room.System, TaskID: d.TaskID, IdempotencyKey: d.Key,
		Body:    fmt.Sprintf("Discussion between %s, at most %d rounds:\n\n%s", strings.Join(names, ", "), out.Bound, strings.TrimSpace(d.Topic)),
		Payload: map[string]any{"event": "group_discussion", "params": map[string]any{"agents": d.Agents, "rounds": out.Bound}}})
	if err != nil {
		return out, err
	}
	out.ThreadID = topic.Message.ID
	passes := 0
	// the bound ends the discussion; the cap is a second stop should the scheduler keep granting (it does not)
	for n := 0; n <= out.Bound*len(d.Agents); n++ {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		dec, g, err := r.d.Room.GrantGroupTurn(ctx, room.GroupTurn{RoomID: rm.ID, Thread: out.ThreadID, Order: d.Agents, Rounds: out.Bound})
		if err != nil {
			return out, err
		}
		out.Rounds = dec.Round
		if g == nil {
			break // the bound, or nobody left to speak
		}
		trig, err := r.d.Room.GetMessage(ctx, g.TriggerID)
		if err != nil {
			return out, err
		}
		info, reply := r.turn(ctx, rm, trig, dec.Speakers[0], dec, turnOpts{grant: *g, group: &GroupPrompt{Round: dec.Round, Rounds: out.Bound, Agents: names}})
		switch info.Status {
		case "":
			return out, fmt.Errorf("%w: %s already had a turn after message %s", ErrDiscussion, g.AgentID, trig.ID)
		case StatusDone, StatusPassed:
		default:
			out.Turns = append(out.Turns, info)
			return out, fmt.Errorf("%w: %s's turn ended %s: %s", ErrDiscussion, g.AgentID, info.Status, info.Error)
		}
		out.Turns = append(out.Turns, info)
		if info.Status == StatusPassed {
			passes++
		} else {
			passes = 0
		}
		if reply != nil && passes >= len(d.Agents) {
			out.Converged = true // everyone in turn had nothing to add
			break
		}
	}
	res, _, err := r.d.Room.ApplyDebateBound(ctx, rm.ID, out.ThreadID)
	if err != nil {
		return out, err
	}
	thread, err := r.d.Room.Thread(ctx, out.ThreadID)
	if err != nil {
		return out, err
	}
	switch res.Action {
	case room.BoundAdoptDecision:
		out.Outcome, out.DecisionID = OutcomeDecision, res.DecisionID
		for _, m := range thread {
			if m.ID == res.DecisionID {
				out.Decision = strings.TrimSpace(m.Body)
				if t := m.PayloadString("title"); t != "" && out.Decision == "" {
					out.Decision = t
				}
			}
		}
	default:
		out.Outcome = OutcomeQuestion
		for _, m := range thread {
			if m.Type == room.TypeQuestion && m.Author.Kind == room.AuthorSystem {
				out.QuestionID = m.ID
			}
		}
	}
	return out, nil
}

// GroupNode is a group-collaboration node: spec reads the discussion from the node's input (the run input and what earlier
// nodes delivered); the node's value is the DiscussionResult and its route the outcome ("decision" or "question"), so the
// next node gets the decision or the question to put to a person.
func GroupNode(id workflow.NodeID, r *Responder, spec func(in workflow.NodeInput) (Discussion, error)) *workflow.Node {
	return workflow.Group(id, func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		d, err := spec(in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if d.Key == "" {
			d.Key = fmt.Sprintf("group:%s:%s:%d", in.RunID, in.NodeID, in.Step)
		}
		if d.TaskID == "" {
			d.TaskID = in.TaskID
		}
		res, err := r.Discuss(ctx, d)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		v, err := json.Marshal(res)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		return workflow.NodeOutput{Value: v, Route: res.Outcome}, nil
	})
}
