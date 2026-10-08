package core

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// postTurn posts an agent's final text for a turn in the task channel, authored by that agent.
func (c *Core) postTurn(ctx context.Context, t tasks.Task, agentID, role, text string) {
	rm, err := c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return
	}
	typ := room.TypeChat
	if role == "review" {
		typ = room.TypeResult
	}
	if _, err := c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: typ, Author: room.Agent(agentID), Body: text, TaskID: t.ID}); err != nil {
		c.logf("post turn: %v", err)
	}
}

// CreateTask adds a BACKLOG task.
func (c *Core) CreateTask(ctx context.Context, req tasks.CreateReq) (tasks.Task, error) {
	if _, err := c.GetProject(ctx, req.ProjectID); err != nil {
		return tasks.Task{}, err
	}
	return c.Tasks.Create(ctx, req)
}

// AssignTask sets a task's worker and independent reviewer. They must be different agents of the project.
func (c *Core) AssignTask(ctx context.Context, taskID, assignee, reviewer string) (tasks.Task, error) {
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return t, err
	}
	if assignee == "" || reviewer == "" || assignee == reviewer {
		return t, errors.New("a task needs a worker and a different agent as reviewer")
	}
	for _, id := range []string{assignee, reviewer} {
		a, err := c.agent(ctx, id)
		if err != nil || a.ProjectID != t.ProjectID {
			return t, fmt.Errorf("agent %s is not on this project's team", id)
		}
	}
	nt, err := c.Tasks.Patch(ctx, taskID, tasks.Update{Assignee: &assignee, Reviewer: &reviewer})
	if err != nil {
		return nt, err
	}
	if rm, err := c.Room.RoomByTask(ctx, taskID); err == nil {
		for id, role := range map[string]string{assignee: "assignee", reviewer: "reviewer"} {
			if err := c.Room.AddMember(ctx, rm.ID, room.Agent(id), role, room.StateActive, room.Human(c.cfg.User)); err != nil {
				c.logf("add %s to task channel: %v", id, err)
			}
		}
	}
	return nt, nil
}

// StartTask starts the task's workflow: local_merge projects use iterate-until-verified, pr_flow projects the PR variant.
func (c *Core) StartTask(ctx context.Context, taskID string) (workflow.RunID, error) {
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return "", err
	}
	if t.Assignee == "" || t.Reviewer == "" {
		return "", errors.New("assign a worker and a reviewer before starting the task")
	}
	if t.RunID != "" {
		if info, err := c.Engine.Status(ctx, workflow.RunID(t.RunID)); err == nil && !info.Status.Terminal() {
			return "", fmt.Errorf("task already has an active run (%s)", info.Status)
		}
	}
	p, err := c.GetProject(ctx, t.ProjectID)
	if err != nil {
		return "", err
	}
	name := "iterate-until-verified"
	if p.Mode == "pr_flow" {
		name = "iterate-until-verified-pr"
	}
	id, err := c.Engine.Start(ctx, name, pipeline.Input{TaskID: t.ID}, workflow.StartOptions{ProjectID: t.ProjectID, TaskID: t.ID})
	if err != nil {
		return "", err
	}
	rid := string(id)
	_, _ = c.Tasks.Patch(ctx, t.ID, tasks.Update{RunID: &rid})
	return id, nil
}

// TaskDetail is everything the task view shows.
type TaskDetail struct {
	Task     tasks.Task                `json:"task"`
	Evidence []tasks.Evidence          `json:"evidence"`
	Reviews  []tasks.ReviewRecord      `json:"reviews"`
	Run      *workflow.RunInfo         `json:"run,omitempty"`
	Pending  []workflow.PendingRequest `json:"pending"`
	RoomID   string                    `json:"roomId,omitempty"`
}

// GetTask returns the task with its evidence, reviews, run and pending human requests.
func (c *Core) GetTask(ctx context.Context, id string) (TaskDetail, error) {
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	d := TaskDetail{Task: t, Pending: []workflow.PendingRequest{}}
	d.Evidence, _ = c.Tasks.Evidence(ctx, id)
	d.Reviews, _ = c.Tasks.Reviews(ctx, id)
	if t.RunID != "" {
		if info, err := c.Engine.Status(ctx, workflow.RunID(t.RunID)); err == nil {
			d.Run = &info
			if p, err := c.Engine.Pending(ctx, info.ID); err == nil && p != nil {
				d.Pending = p
			}
		}
	}
	if r, err := c.Room.RoomByTask(ctx, id); err == nil {
		d.RoomID = r.ID
	}
	return d, nil
}

// TaskDiff returns the diff evidence of the task (the change reviewers and the human look at).
func (c *Core) TaskDiff(ctx context.Context, id string) (string, error) {
	ev, err := c.Tasks.Evidence(ctx, id)
	if err != nil {
		return "", err
	}
	for i := len(ev) - 1; i >= 0; i-- {
		if ev[i].Kind == "diff" {
			b, err := os.ReadFile(ev[i].Ref)
			return string(b), err
		}
	}
	return "", nil
}

// Respond answers a pending request of a run as the human. Only the human identity is ever attached here: an agent has no
// path to this method.
func (c *Core) Respond(ctx context.Context, runID, requestID, decision string, data []byte) error {
	var d []byte = data
	return c.Engine.Resume(ctx, workflow.RunID(runID), workflow.Response{RequestID: workflow.RequestID(requestID), Decision: decision, By: c.Human(), Data: d})
}

// CancelTask stops the task's run and cancels the task.
func (c *Core) CancelTask(ctx context.Context, id string) error {
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return err
	}
	if t.RunID != "" {
		_ = c.Engine.Cancel(ctx, workflow.RunID(t.RunID))
	}
	_, err = c.Tasks.Transition(ctx, id, tasks.Cancelled, tasks.TransitionOpts{By: c.Human(), Reason: "cancelled by the human"})
	return err
}

// PostMessage posts a human message in a room.
func (c *Core) PostMessage(ctx context.Context, roomID, body, threadRoot string) (room.Posted, error) {
	return c.Room.Post(ctx, room.PostParams{RoomID: roomID, Type: room.TypeChat, Author: room.Human(c.cfg.User), Body: body, ThreadRootID: threadRoot})
}

// Replay returns persisted events after a sequence so the UI can catch up after a reconnect.
func (c *Core) Replay(ctx context.Context, after uint64, f events.Filter, limit int) ([]events.Event, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	return c.Bus.Replay(ctx, after, f, limit)
}

// DescribeAdapters reports every adapter's detection and verified capabilities.
func (c *Core) DescribeAdapters(ctx context.Context) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for id, a := range c.adapters {
		det, err := a.Detect(ctx)
		m := map[string]any{"id": id, "detection": det, "capabilities": a.Capabilities()}
		if err != nil {
			m["error"] = err.Error()
		}
		out = append(out, m)
	}
	return out
}
