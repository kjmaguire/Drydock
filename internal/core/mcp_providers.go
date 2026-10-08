package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The Workspace providers that sit on the core's own services: the task tool over tasks.Service, the room tool over
// room.Service and the handoff tool over the handoffs table (migration 00002). They are only reachable by a run whose policy
// lists the tool, and the default role policy withholds all three (ext_mcp.go).

// maxHandoffBytes bounds one stored handoff artifact (token-control s3: about 1,500 tokens, enforced at write time).
const maxHandoffBytes = 8 << 10

// ---- task ----

type mcpTasks struct{ c *Core }

// task loads a task of the run's project; a task of another project is reported as not found, never as forbidden.
func (p mcpTasks) task(ctx context.Context, run mcp.RunInfo, id string) (tasks.Task, error) {
	t, err := p.c.Tasks.Get(ctx, id)
	if errors.Is(err, tasks.ErrNotFound) || (err == nil && run.ProjectID != "" && t.ProjectID != run.ProjectID) {
		return tasks.Task{}, mcp.ErrNotFound
	}
	return t, err
}

func (p mcpTasks) Get(ctx context.Context, run mcp.RunInfo, taskID string) (mcp.TaskView, error) {
	t, err := p.task(ctx, run, taskID)
	if err != nil {
		return mcp.TaskView{}, err
	}
	v := mcp.TaskView{ID: t.ID, Title: t.Title, Description: t.Description, Status: string(t.Status), Acceptance: t.Acceptance,
		Extra: map[string]any{"iteration": t.Iteration, "maxIterations": t.MaxIterations, "branch": t.Branch, "priority": t.Priority}}
	if t.ReviewFeedback != "" {
		v.Feedback = []string{t.ReviewFeedback}
	}
	if ev, err := p.c.Tasks.Evidence(ctx, t.ID); err == nil {
		if len(ev) > 20 {
			ev = ev[len(ev)-20:]
		}
		for _, e := range ev {
			verdict := "n/a"
			if e.Passed != nil {
				verdict = map[bool]string{true: "passed", false: "failed"}[*e.Passed]
			}
			v.Evidence = append(v.Evidence, fmt.Sprintf("%s (%s): %s", e.Kind, verdict, clipRunes(e.Summary, 300)))
		}
	}
	return v, nil
}

// AddNote records the note as a STATUS message of the agent in the task's room. There is no separate notes table, and the
// room is where the human reads what an agent says about its task.
func (p mcpTasks) AddNote(ctx context.Context, run mcp.RunInfo, taskID, note string, evidenceRefs []string) error {
	t, err := p.task(ctx, run, taskID)
	if err != nil {
		return err
	}
	rm, err := p.c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return fmt.Errorf("the task has no room to hold the note: %w", err)
	}
	body := note
	if len(evidenceRefs) > 0 {
		body += "\n\nevidence: " + strings.Join(evidenceRefs, ", ")
	}
	_, err = p.c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.TypeStatus, Author: room.Agent(run.AgentID), Body: body, RunID: run.RunID, TaskID: t.ID,
		Payload: map[string]any{"via": "mcp", "kind": "task_note", "evidence_refs": evidenceRefs}})
	return err
}

// ProposeStatus does not move the task: the pipeline and the human own the state machine (acceptance is human, brief section
// 12). A proposal to stop (BLOCKED, WAITING_HUMAN) becomes a blocking QUESTION so it reaches "Needs you"; a proposal for REVIEW
// is a STATUS. Either way the evidence refs travel with it and task.status_proposed is published.
func (p mcpTasks) ProposeStatus(ctx context.Context, run mcp.RunInfo, taskID, status string, evidenceRefs []string) error {
	t, err := p.task(ctx, run, taskID)
	if err != nil {
		return err
	}
	rm, err := p.c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return fmt.Errorf("the task has no room to hold the proposal: %w", err)
	}
	typ := room.TypeStatus
	body := fmt.Sprintf("I propose moving this task to %s (it is %s now).", status, t.Status)
	payload := map[string]any{"via": "mcp", "kind": "status_proposal", "proposed_status": status, "evidence_refs": evidenceRefs}
	if status != "REVIEW" {
		typ = room.TypeQuestion
		payload["blocking"] = true
		body += " A human needs to look."
	}
	if len(evidenceRefs) > 0 {
		body += "\n\nevidence: " + strings.Join(evidenceRefs, ", ")
	}
	if _, err := p.c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: typ, Author: room.Agent(run.AgentID), Body: body, RunID: run.RunID, TaskID: t.ID, Payload: payload}); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]any{"proposed": status, "from": t.Status, "evidence_refs": evidenceRefs})
	_, _ = p.c.Bus.Publish(ctx, events.Event{Type: "task.status_proposed", ProjectID: t.ProjectID, TaskID: t.ID, AgentID: run.AgentID, RunID: run.RunID, Data: b})
	return nil
}

// ---- room ----

type mcpRoom struct{ c *Core }

// resolve finds the channel or task room a run may use: a channel or task room of its own project that the agent is a member
// of. DMs and group chats are the human's conversations and are never reachable from a tool.
func (p mcpRoom) resolve(ctx context.Context, run mcp.RunInfo, channel string) (room.Room, error) {
	var rm room.Room
	channel = strings.TrimPrefix(strings.TrimSpace(channel), "#")
	if channel == "" {
		if run.TaskID == "" {
			return rm, errors.New("channel is required: this run has no task room")
		}
		r, err := p.c.Room.RoomByTask(ctx, run.TaskID)
		if err != nil {
			return rm, err
		}
		rm = r
	} else {
		rooms, err := p.c.Room.ListRooms(ctx, run.ProjectID, false)
		if err != nil {
			return rm, err
		}
		found := false
		for _, r := range rooms {
			if r.Name == channel && (r.Kind == room.KindChannel || r.Kind == room.KindTask) {
				rm, found = r, true
				break
			}
		}
		if !found {
			return rm, fmt.Errorf("no channel %q in this project", channel)
		}
	}
	if rm.ProjectID != run.ProjectID {
		return room.Room{}, mcp.ErrNotFound
	}
	if ok, err := p.c.Room.IsMember(ctx, rm.ID, room.Agent(run.AgentID)); err != nil || !ok {
		return room.Room{}, fmt.Errorf("this agent is not a member of #%s (a human adds members)", rm.Name)
	}
	return rm, nil
}

func (p mcpRoom) Read(ctx context.Context, run mcp.RunInfo, q mcp.RoomQuery) ([]mcp.RoomMessage, error) {
	rm, err := p.resolve(ctx, run, q.Channel)
	if err != nil {
		return nil, err
	}
	var msgs []room.Message
	if q.ThreadID != "" {
		if msgs, err = p.c.Room.Thread(ctx, q.ThreadID); err != nil {
			return nil, err
		}
		if len(msgs) > 0 && msgs[0].RoomID != rm.ID {
			return nil, mcp.ErrNotFound
		}
		if len(msgs) > q.Limit && q.Limit > 0 {
			msgs = msgs[len(msgs)-q.Limit:]
		}
	} else {
		var last int64
		if err := p.c.DB.Reader().QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM messages WHERE room_id=?`, rm.ID).Scan(&last); err != nil {
			return nil, err
		}
		after := last - int64(q.Limit)
		if after < 0 {
			after = 0
		}
		if msgs, err = p.c.Room.Messages(ctx, rm.ID, after, q.Limit); err != nil {
			return nil, err
		}
	}
	out := make([]mcp.RoomMessage, 0, len(msgs))
	for _, m := range msgs {
		rmsg := mcp.RoomMessage{ID: m.ID, ThreadID: m.ThreadRootID, Author: m.Author.Key(), Kind: string(m.Type), Body: m.Body, Time: m.CreatedAt}
		for _, mn := range m.Mentions {
			rmsg.Mentions = append(rmsg.Mentions, mn.ID)
		}
		out = append(out, rmsg)
	}
	return out, nil
}

func (p mcpRoom) Post(ctx context.Context, run mcp.RunInfo, in mcp.RoomPost) (mcp.RoomMessage, error) {
	rm, err := p.resolve(ctx, run, in.Channel)
	if err != nil {
		return mcp.RoomMessage{}, err
	}
	body := in.Body
	var missing []string
	for _, m := range in.Mentions {
		m = strings.TrimPrefix(strings.TrimSpace(m), "@")
		if m != "" && !strings.Contains(body, "@"+m) {
			missing = append(missing, "@"+m)
		}
	}
	if len(missing) > 0 {
		body += "\n\ncc " + strings.Join(missing, " ")
	}
	res, err := p.c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.MsgType(in.Kind), Author: room.Agent(run.AgentID), Body: body,
		ReplyToID: in.ThreadID, RunID: run.RunID, TaskID: rm.TaskID, Payload: map[string]any{"via": "mcp"}})
	if err != nil {
		return mcp.RoomMessage{}, err
	}
	m := res.Message
	return mcp.RoomMessage{ID: m.ID, ThreadID: m.ThreadRootID, Author: m.Author.Key(), Kind: string(m.Type), Body: m.Body, Time: m.CreatedAt}, nil
}

// ---- handoff ----

type mcpHandoffs struct{ c *Core }

func (p mcpHandoffs) Create(ctx context.Context, run mcp.RunInfo, h mcp.Handoff) (string, error) {
	h.ID = ""
	b, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	if len(b) > maxHandoffBytes {
		return "", fmt.Errorf("the handoff is %d bytes, over the %d byte cap: shorten the summary and lists", len(b), maxHandoffBytes)
	}
	id := ids.New("hnd")
	err = p.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO handoffs(id,task_id,run_id,agent_id,artifact,reason,created_at) VALUES(?,?,?,?,?,?,?)`,
			id, run.TaskID, run.RunID, run.AgentID, string(b), "agent", storage.Now())
		return err
	})
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(map[string]any{"handoff": id, "bytes": len(b)})
	_, _ = p.c.Bus.Publish(ctx, events.Event{Type: "handoff.created", ProjectID: run.ProjectID, TaskID: run.TaskID, AgentID: run.AgentID, RunID: run.RunID, Data: data})
	return id, nil
}

// Get returns a handoff of the run's own project (through its task) or, for a run without a task, one this agent wrote.
func (p mcpHandoffs) Get(ctx context.Context, run mcp.RunInfo, id string) (mcp.Handoff, error) {
	var taskID, agentID, artifact string
	err := p.c.DB.Reader().QueryRowContext(ctx, `SELECT task_id,agent_id,artifact FROM handoffs WHERE id=?`, id).Scan(&taskID, &agentID, &artifact)
	if errors.Is(err, sql.ErrNoRows) {
		return mcp.Handoff{}, mcp.ErrNotFound
	}
	if err != nil {
		return mcp.Handoff{}, err
	}
	if taskID != "" {
		if t, err := p.c.Tasks.Get(ctx, taskID); err != nil || t.ProjectID != run.ProjectID {
			return mcp.Handoff{}, mcp.ErrNotFound
		}
	} else if agentID != run.AgentID {
		return mcp.Handoff{}, mcp.ErrNotFound
	}
	var h mcp.Handoff
	if err := json.Unmarshal([]byte(artifact), &h); err != nil {
		// the watchdog and the router write their own artifact shapes into this table; show them as the summary
		return mcp.Handoff{ID: id, Summary: clipRunes(artifact, 6000)}, nil
	}
	h.ID = id
	return h, nil
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
