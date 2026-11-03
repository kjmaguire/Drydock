package core

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// The start report (X511): what this start found the last one left behind, so the window that connects later can say it in one
// line instead of opening as if nothing happened. The core does all of this before any window connects (the crash reports it
// kept, the agent runs it closed, the workflow runs it resumed or could not resume) and only the log knew. app.info returns it.
//
// "Closed unexpectedly" is only said when it is known: a marker file is written into the data folder when the core opens and
// removed by Close; a marker found at the next start means the last process ended without Close (a crash, a kill, a power cut).
// A folder without a marker (the first start, or one made by a version without it) is a clean start.

// runningMarker is the file in the data folder that says a core has it open.
const runningMarker = "drydock.running"

// StartRun is one workflow run the start resumed or could not resume.
type StartRun struct {
	RunID     string `json:"runId"`
	TaskID    string `json:"taskId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	Title     string `json:"title,omitempty"`
}

// StartReport is what this start found.
type StartReport struct {
	// StartedAt (ms since the epoch) names this start, so a window can remember it was dismissed.
	StartedAt int64 `json:"startedAt"`
	// Unexpected says the last process ended without closing: its marker was still there, or it left a crash.
	Unexpected bool `json:"unexpected"`
	// Crashes is how many crash reports this start made from what the last process left (none while crash reports are off).
	Crashes int `json:"crashes,omitempty"`
	// AgentRunsClosed is how many agent runs the last stop left open and this start marked as stopped.
	AgentRunsClosed int        `json:"agentRunsClosed,omitempty"`
	Resumed         []StartRun `json:"resumed"`
	// NotResumed are the runs that could not be resumed (their workflow changed, or their checkpoint is missing): they failed.
	NotResumed []StartRun `json:"notResumed"`
	// Resuming are the runs the last process still held a valid lease on at this start (it was killed and started again within
	// the lease TTL): they are resumed as soon as that lease expires, and then move to Resumed (or NotResumed).
	Resuming []StartRun `json:"resuming"`
}

// startState holds the report while Open fills it in.
type startState struct {
	mu sync.Mutex
	r  StartReport
}

func (s *startState) update(f func(r *StartReport)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.r)
}

func (s *startState) get() StartReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.r
	r.Resumed = append([]StartRun{}, r.Resumed...)
	r.NotResumed = append([]StartRun{}, r.NotResumed...)
	r.Resuming = append([]StartRun{}, r.Resuming...)
	return r
}

// markRunning reports whether the last process on dir ended without Close (its marker is still there) and leaves this one's.
func markRunning(dir string) bool {
	p := filepath.Join(dir, runningMarker)
	_, err := os.Stat(p)
	_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o600)
	return err == nil
}

// clearRunning removes the marker: the process closes as it should.
func clearRunning(dir string) { _ = os.Remove(filepath.Join(dir, runningMarker)) }

// StartReport returns what this start found (app.info's startReport).
func (c *Core) StartReport() StartReport { return c.start.get() }

// heldByOthers returns the unfinished runs whose lease another owner holds and has not let expire (the process before this one,
// killed within the lease TTL), with their tasks' titles, and when the last of those leases expires (ms).
func (c *Core) heldByOthers(ctx context.Context) ([]StartRun, int64) {
	out := []StartRun{}
	var until int64
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT r.id, r.task_id, r.project_id, COALESCE(t.title,''), l.expires_at
		FROM workflow_runs r JOIN run_leases l ON l.run_id = r.id LEFT JOIN tasks t ON t.id = r.task_id
		WHERE r.status IN ('RUNNING','RECOVERING') AND l.owner <> ? AND l.expires_at >= ? ORDER BY r.created_at`, c.Engine.Owner(), storage.Now())
	if err != nil {
		c.logf("start report: %v", err)
		return out, 0
	}
	defer rows.Close()
	for rows.Next() {
		var r StartRun
		var exp int64
		if err := rows.Scan(&r.RunID, &r.TaskID, &r.ProjectID, &r.Title, &exp); err != nil {
			c.logf("start report: %v", err)
			return out, until
		}
		out = append(out, r)
		until = max(until, exp)
	}
	return out, until
}

// noteTakenOver waits until the leases held at start have expired and WatchRecover has had time to take their runs over, then
// records them in the start report as resumed or not, and says the report changed (app.start_report) so a window re-reads it.
func (c *Core) noteTakenOver(ctx context.Context, head uint64, since int64, held []StartRun, until int64) {
	ttl := c.cfg.LeaseTTL
	// WatchRecover looks every third of the TTL: two of its passes after the last lease ran out
	wait := time.Until(time.UnixMilli(until)) + 2*ttl/3 + 500*time.Millisecond
	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
	c.noteRecovered(ctx, head, since, held)
	c.publish(ctx, "app.start_report", "", map[string]any{"startedAt": c.start.get().StartedAt})
}

// noteRecovered records the runs Recover (or WatchRecover) resumed (a workflow.recovering event after seq `head`) and those it
// gave up on (failed with a "recover:" reason since `since`, ms), with their tasks' titles; the runs of `held` that are in
// neither are still being resumed.
func (c *Core) noteRecovered(ctx context.Context, head uint64, since int64, held []StartRun) {
	read := func(q string, args ...any) []StartRun {
		out := []StartRun{}
		rows, err := c.DB.Reader().QueryContext(ctx, q, args...)
		if err != nil {
			c.logf("start report: %v", err)
			return out
		}
		defer rows.Close()
		seen := map[string]bool{}
		for rows.Next() {
			var r StartRun
			if err := rows.Scan(&r.RunID, &r.TaskID, &r.ProjectID, &r.Title); err != nil {
				c.logf("start report: %v", err)
				return out
			}
			if !seen[r.RunID] {
				seen[r.RunID] = true
				out = append(out, r)
			}
		}
		return out
	}
	resumed := read(`SELECT e.run_id, e.task_id, e.project_id, COALESCE(t.title,'') FROM events e LEFT JOIN tasks t ON t.id=e.task_id
		WHERE e.seq > ? AND e.type='workflow.recovering' AND e.run_id<>'' ORDER BY e.seq`, head)
	failed := read(`SELECT r.id, r.task_id, r.project_id, COALESCE(t.title,'') FROM workflow_runs r LEFT JOIN tasks t ON t.id=r.task_id
		WHERE r.status='FAILED' AND r.failed_reason LIKE 'recover:%' AND r.updated_at >= ? ORDER BY r.updated_at`, since)
	done := map[string]bool{}
	for _, r := range append(append([]StartRun{}, resumed...), failed...) {
		done[r.RunID] = true
	}
	resuming := []StartRun{}
	for _, r := range held {
		if !done[r.RunID] {
			resuming = append(resuming, r)
		}
	}
	c.start.update(func(r *StartReport) {
		r.Resumed = resumed
		r.NotResumed = failed
		r.Resuming = resuming
	})
}
