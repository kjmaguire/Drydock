package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The collision guard (docs/parallel-work.md). Every task works in its own worktree, so two agents can never overwrite each
// other's files while they work. What can still go wrong is that two tasks change the same code and one of them lands on a base
// the other already changed. The guard makes the later task wait for the earlier one instead of racing it.
//
// A task's footprint is the paths it is planned to touch (Task.Files, which the planner or the human may set and which is later
// replaced by what the task really changed) plus what its worktree has changed so far. A task waits while an EARLIER task that
// is still active (started before it, not yet merged, failed or cancelled) has a footprint that overlaps its own. Earlier means
// started first, so the order is total and two tasks can never wait on each other. A waiting task is BLOCKED with the reason;
// the human can release it. The wait costs nothing: no agent runs while it waits. After the earlier task merges, the sync step
// brings its work into the waiting task's branch and runs the checks again.

// GuardEvidence is the evidence kind a human release leaves.
const guardOverride = "guard_override"

// Collision is an earlier active task whose footprint overlaps another task's.
type Collision struct {
	Task  tasks.Task `json:"task"`
	Files []string   `json:"files"`
}

// covers reports whether a footprint entry p covers path q: the same file, a directory (with or without a trailing slash or
// "/**"), or a glob.
func covers(p, q string) bool {
	p, q = strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, "\\", "/")), "./"), strings.TrimPrefix(path.Clean(strings.ReplaceAll(q, "\\", "/")), "./")
	if p == q || p == "." {
		return p == q
	}
	if strings.HasSuffix(p, "/**") {
		return strings.HasPrefix(q, strings.TrimSuffix(p, "**"))
	}
	if strings.ContainsAny(p, "*?[") {
		ok, _ := path.Match(p, q)
		return ok
	}
	return strings.HasPrefix(q, p+"/")
}

// overlap lists the entries of a that collide with b (either side may be a directory or a glob).
func overlap(a, b []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range a {
		for _, y := range b {
			if covers(x, y) || covers(y, x) {
				if !seen[x] {
					seen[x] = true
					out = append(out, x)
				}
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func unionPaths(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, p := range l {
			if p = strings.TrimSpace(p); p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

var activeStatuses = []tasks.Status{tasks.Claimed, tasks.InProgress, tasks.Review, tasks.Verification, tasks.WaitingHuman, tasks.Blocked}

// earlier reports whether a started before b.
func earlier(a, b tasks.Task) bool {
	if a.Started == 0 {
		return false
	}
	if b.Started == 0 {
		return true
	}
	if a.Started != b.Started {
		return a.Started < b.Started
	}
	return a.Seq < b.Seq
}

// footprint is what the task is expected to or does change: the planned paths plus the worktree's changes against the base.
func (d Deps) footprint(ctx context.Context, t tasks.Task, target string) []string {
	paths := append([]string(nil), t.Files...)
	if t.Worktree == "" || d.Repo == nil {
		return unionPaths(paths)
	}
	repo, err := d.Repo(ctx, t.ProjectID)
	if err != nil {
		return unionPaths(paths)
	}
	if observed, err := repo.ChangedPaths(ctx, worktreeOf(t), target); err == nil {
		paths = append(paths, observed...)
	}
	return unionPaths(paths)
}

// Collisions returns the earlier active tasks of the project whose footprint overlaps t's, with the overlapping paths.
func (d Deps) Collisions(ctx context.Context, t tasks.Task, target string) ([]Collision, error) {
	mine := d.footprint(ctx, t, target)
	if len(mine) == 0 {
		return nil, nil
	}
	others, err := d.Tasks.List(ctx, tasks.Filter{ProjectID: t.ProjectID, Statuses: activeStatuses})
	if err != nil {
		return nil, err
	}
	var out []Collision
	for _, o := range others {
		if o.ID == t.ID || !earlier(o, t) {
			continue
		}
		if files := overlap(mine, d.footprint(ctx, o, target)); len(files) > 0 {
			out = append(out, Collision{Task: o, Files: files})
		}
	}
	sort.Slice(out, func(i, j int) bool { return earlier(out[i].Task, out[j].Task) })
	return out, nil
}

func waitReason(cs []Collision) string {
	var parts []string
	for _, c := range cs {
		files := c.Files
		more := ""
		if len(files) > 3 {
			files, more = files[:3], fmt.Sprintf(" and %d more", len(c.Files)-3)
		}
		parts = append(parts, fmt.Sprintf("%q (both change %s%s)", c.Task.Title, strings.Join(files, ", "), more))
	}
	return "waiting for " + strings.Join(parts, "; ") + " to finish"
}

// Guard is the node that waits while an earlier task is changing the same code. It runs after the work turn, before the checks.
func Guard(d Deps, project func(ctx context.Context, projectID string) (ProjectInfo, error)) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if !LoadDevSettings(ctx, d.Tasks.DB, t.ProjectID).GuardOn() {
			return workflow.NodeOutput{}, nil
		}
		pi, err := project(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		target := baseTarget(t, pi)
		poll := d.GuardPoll
		if poll <= 0 {
			poll = 2 * time.Second
		}
		baseline := d.lastOverride(ctx, t.ID)
		waited := false
		var lastReason string
		for {
			cs, err := d.Collisions(ctx, t, target)
			if err != nil {
				return workflow.NodeOutput{}, err
			}
			released := d.lastOverride(ctx, t.ID) > baseline
			if len(cs) == 0 || released {
				if waited {
					if _, err := d.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: "system", Reason: "no earlier task is changing the same code any more"}); err != nil {
						return workflow.NodeOutput{}, err
					}
					d.emit(ctx, "task.guard_released", t, map[string]any{"released": released})
				}
				b, _ := json.Marshal(map[string]any{"waited": waited, "released": released})
				return workflow.NodeOutput{Value: b}, nil
			}
			if reason := waitReason(cs); reason != lastReason {
				lastReason = reason
				if cur, err := d.Tasks.Get(ctx, t.ID); err == nil && cur.Status == tasks.InProgress {
					if _, err := d.Tasks.Transition(ctx, t.ID, tasks.Blocked, tasks.TransitionOpts{By: "system", Reason: reason}); err != nil {
						return workflow.NodeOutput{}, err
					}
				}
				waited = true
				var on []map[string]any
				for _, c := range cs {
					on = append(on, map[string]any{"taskId": c.Task.ID, "title": c.Task.Title, "files": c.Files})
				}
				d.emit(ctx, "task.waiting_on", t, map[string]any{"waitingOn": on, "reason": reason})
				_, _ = d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: "guard", Summary: reason})
			}
			select {
			case <-ctx.Done():
				return workflow.NodeOutput{}, ctx.Err()
			case <-time.After(poll):
			}
		}
	}
}

// lastOverride is the id of the newest human release of the task's wait (0 when none).
func (d Deps) lastOverride(ctx context.Context, taskID string) int64 {
	ev, err := d.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return 0
	}
	var last int64
	for _, e := range ev {
		if e.Kind == guardOverride && e.ID > last {
			last = e.ID
		}
	}
	return last
}

// ReleaseGuard lets a waiting task go on although an earlier task touches the same code. Only a human may do it; the merge is
// still checked against the latest base and still needs the human's approval.
func ReleaseGuard(ctx context.Context, d Deps, taskID, by string) error {
	if !strings.HasPrefix(by, "human:") || by == "human:" {
		return fmt.Errorf("only a human can release a task that is waiting for another task")
	}
	t, err := d.Tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if t.Status != tasks.Blocked {
		return fmt.Errorf("the task is %s, not waiting for another task", t.Status)
	}
	ok := true
	_, err = d.Tasks.AddEvidence(ctx, taskID, tasks.Evidence{Kind: guardOverride, Passed: &ok, Summary: "released by " + by + " while it was waiting: " + t.BlockedReason})
	return err
}

// declaredCollision is the scheduler's cheap check before a READY task is started: the paths it is planned to touch against
// those of the tasks already under way (planned or observed at their last finalize). No repository access, so it can run in
// every pass.
func declaredCollision(t tasks.Task, active []tasks.Task) *Collision {
	if len(t.Files) == 0 {
		return nil
	}
	for _, o := range active {
		if o.ID == t.ID || o.Started == 0 {
			continue
		}
		if files := overlap(t.Files, o.Files); len(files) > 0 {
			return &Collision{Task: o, Files: files}
		}
	}
	return nil
}
