package core

import (
	"context"
	"errors"

	"github.com/kjmaguire/drydock/internal/agui"
	"github.com/kjmaguire/drydock/internal/events"
)

// A task's live activity (decision P87): what its agents did, turn by turn, in the typed vocabulary of internal/agui
// (modelled on AG-UI 1.0), read from the agent.* events the bus stored for the task. Read-only; the UI reloads it as the
// task's agent.output events arrive.

// ActivityEventCap is how many of a task's newest agent events the activity is read from.
var ActivityEventCap = 20000

var activityTypes = []string{"agent.started", "agent.waiting", "agent.output", "agent.completed", "agent.failed", "agent.stopped", "approval.requested", "permission.decided"}

func init() {
	RegisterExtension(Extension{Name: "activity", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"task.activity": bind(func(ctx context.Context, p struct {
				TaskID string `json:"taskId"`
			}) (any, error) {
				return c.TaskActivity(ctx, p.TaskID)
			}),
		}, nil, nil
	}})
}

// TaskActivity returns a task's agent activity, one thread per agent process. Past ActivityEventCap events only the newest
// are read and Truncated says so.
func (c *Core) TaskActivity(ctx context.Context, taskID string) (agui.Activity, error) {
	if taskID == "" {
		return agui.Activity{Threads: []agui.Thread{}}, errors.New("activity: name a task")
	}
	if _, err := c.Tasks.Get(ctx, taskID); err != nil {
		return agui.Activity{Threads: []agui.Thread{}}, err
	}
	f := events.Filter{TaskID: taskID, Types: activityTypes}
	var evs []events.Event
	truncated := false
	var after uint64
	for {
		page, err := c.Bus.Replay(ctx, after, f, 1000)
		if err != nil {
			return agui.Activity{Threads: []agui.Thread{}}, err
		}
		evs = append(evs, page...)
		if over := len(evs) - ActivityEventCap; over > 0 { // keep the newest
			evs, truncated = append(evs[:0:0], evs[over:]...), true
		}
		if len(page) < 1000 {
			break
		}
		after = page[len(page)-1].Seq
	}
	a := agui.Project(evs)
	if a.Threads == nil {
		a.Threads = []agui.Thread{}
	}
	for i := range a.Threads {
		th := &a.Threads[i]
		if th.Events == nil {
			th.Events = []agui.Event{}
		}
		// a thread whose agent.started was not read (cut off by the cap, or older than the event) says what its agent is set to
		if th.Adapter == "" && th.AgentID != "" {
			if ag, err := c.agent(ctx, th.AgentID); err == nil {
				th.Adapter, th.Model = ag.Adapter, ag.Model
			}
		}
	}
	a.Truncated = truncated
	return a, nil
}
