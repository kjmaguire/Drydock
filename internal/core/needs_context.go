package core

import (
	"context"
	"encoding/json"

	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// addNeedsContext gives each inbox item of a task its context line (room.NeedsContext): the assignee, the newest diff's size,
// the newest attempt's checks and the newest reviewer verdict. A task that cannot be read is left without one.
func (c *Core) addNeedsContext(ctx context.Context, items []room.NeedsYouItem) {
	if c.Tasks == nil {
		return
	}
	seen := map[string]*room.NeedsContext{}
	for i := range items {
		id := items[i].TaskID
		if id == "" {
			continue
		}
		nc, ok := seen[id]
		if !ok {
			nc = c.needsContext(ctx, id)
			seen[id] = nc
		}
		items[i].Context = nc
	}
}

func (c *Core) needsContext(ctx context.Context, taskID string) *room.NeedsContext {
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return nil
	}
	ev, _ := c.Tasks.Evidence(ctx, taskID)
	rv, _ := c.Tasks.Reviews(ctx, taskID)
	return needsContextOf(t, ev, rv)
}

// needsContextOf is the context line of a task from its evidence and reviews (both oldest first).
func needsContextOf(t tasks.Task, ev []tasks.Evidence, rv []tasks.ReviewRecord) *room.NeedsContext {
	nc := &room.NeedsContext{Agent: t.Assignee}
	attempt := -1
	for _, e := range ev {
		if e.Passed != nil && e.Attempt > attempt {
			attempt = e.Attempt
		}
	}
	latest := map[string]bool{}
	var kinds []string
	for _, e := range ev {
		switch {
		case e.Kind == "diff":
			var st struct{ Files, Insertions, Deletions int }
			if json.Unmarshal(e.Data, &st) == nil {
				nc.HasDiff, nc.Files, nc.Insertions, nc.Deletions = true, st.Files, st.Insertions, st.Deletions
			}
		case e.Passed != nil && e.Attempt == attempt:
			if _, ok := latest[e.Kind]; !ok {
				kinds = append(kinds, e.Kind)
			}
			latest[e.Kind] = *e.Passed
		}
	}
	for _, k := range kinds {
		nc.ChecksTotal++
		if latest[k] {
			nc.ChecksPassed++
		}
	}
	if len(rv) > 0 {
		nc.Verdict = rv[len(rv)-1].Verdict
	}
	return nc
}
