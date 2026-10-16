package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Parallel execution (brief section 24, Slice 4). Two parts with different jobs:
//
//   - the Scheduler decides which READY tasks start, within the project's max parallel agents and the providers'
//     max_concurrent, each in its own worktree (the template's prepare node creates it). It starts runs; it never merges:
//     every template ends at a human approval, and a merge conflict is parked as a question by the Merge node.
//   - the Limiter enforces the same limits at the moment an agent turn would run, for every workflow (resumed runs, room
//     replies), by making the turn wait for a free slot. Admission is best effort; the Limiter is the invariant.

// Skip is a READY task the scheduler did not start, and why.
type Skip struct {
	TaskID string `json:"taskId"`
	Reason string `json:"reason"`
}

// TickResult is the outcome of one scheduling pass.
type TickResult struct {
	Promoted []string `json:"promoted"`
	Started  []string `json:"started"`
	Skipped  []Skip   `json:"skipped"`
	Active   int      `json:"active"`   // runs executing now (not counting runs parked for a human)
	Capacity int      `json:"capacity"` // free slots before this pass
	Max      int      `json:"max"`
}

// SchedulerDeps are the services the scheduler needs.
type SchedulerDeps struct {
	Tasks *tasks.Service
	DB    *storage.DB
	Bus   events.Bus
	Dev   func(ctx context.Context, projectID string) DevSettings
	// Start starts the task's workflow (its template, or iterate-until-verified).
	Start func(ctx context.Context, t tasks.Task) (workflow.RunID, error)
	// Provider returns the provider an agent uses ("" = a subscription CLI without a registered provider) for the capacity peek.
	Provider func(ctx context.Context, agentID string) (providerID string)
	// ProviderSlots reports how many slots the provider has free right now; limited is false when it has no max_concurrent.
	ProviderSlots func(ctx context.Context, providerID string) (free int, limited bool, err error)
}

// Scheduler starts READY tasks.
type Scheduler struct {
	d  SchedulerDeps
	mu sync.Mutex // one pass at a time
	// skipped is, per project and task, the reason last published as scheduler.skipped: a task that waits for the same reason pass
	// after pass is said once, not every five seconds for as long as it waits (audit 2026-10-16, L16)
	skipped map[string]map[string]string
}

// NewScheduler returns a Scheduler.
func NewScheduler(d SchedulerDeps) *Scheduler {
	return &Scheduler{d: d, skipped: map[string]map[string]string{}}
}

// activeRuns counts the project's runs that are executing: they hold an agent (or are about to). A run parked for a human
// holds no agent process, so it does not use a slot.
func (s *Scheduler) activeRuns(ctx context.Context, projectID string) (int, error) {
	var n int
	err := s.d.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs WHERE project_id=? AND task_id<>'' AND status IN ('PENDING','RUNNING','RECOVERING')`, projectID).Scan(&n)
	return n, err
}

// Tick promotes BACKLOG tasks whose dependencies are done, then starts as many READY tasks as the limits allow, highest
// priority first, oldest first. Starting a task needs a worker and a reviewer; a task without them is skipped with the
// reason, never started half-configured.
func (s *Scheduler) Tick(ctx context.Context, projectID string) (TickResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := TickResult{Promoted: []string{}, Started: []string{}, Skipped: []Skip{}}
	dev := DefaultDevSettings()
	if s.d.Dev != nil {
		dev = s.d.Dev(ctx, projectID)
	}
	res.Max = dev.MaxParallel
	var err error
	if res.Promoted, err = s.d.Tasks.PromoteReady(ctx, projectID); err != nil {
		return res, err
	}
	if res.Promoted == nil {
		res.Promoted = []string{}
	}
	if res.Active, err = s.activeRuns(ctx, projectID); err != nil {
		return res, err
	}
	res.Capacity = dev.MaxParallel - res.Active
	ready, err := s.d.Tasks.List(ctx, tasks.Filter{ProjectID: projectID, Statuses: []tasks.Status{tasks.Ready}})
	if err != nil {
		return res, err
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].Seq < ready[j].Seq
	})
	free := res.Capacity
	usedProvider := map[string]int{}
	var underway []tasks.Task
	if dev.GuardOn() {
		underway, _ = s.d.Tasks.List(ctx, tasks.Filter{ProjectID: projectID, Statuses: activeStatuses})
		// a task whose run has just been started is still READY until its prepare step has run: it is under way too
		for _, t := range ready {
			if _, ok := s.runActive(ctx, t.RunID); ok && t.RunID != "" {
				t.Started = 1
				underway = append(underway, t)
			}
		}
	}
	for _, t := range ready {
		skip := func(why string) {
			res.Skipped = append(res.Skipped, Skip{t.ID, why})
		}
		switch {
		case t.Assignee == "" || t.Reviewer == "":
			skip("no worker or reviewer assigned")
			continue
		case t.RunID != "":
			if info, ok := s.runActive(ctx, t.RunID); ok {
				skip("already has an active run (" + string(info) + ")")
				continue
			}
		}
		if c := declaredCollision(t, underway); c != nil {
			skip(fmt.Sprintf("waits for %q, which changes the same code (%s)", c.Task.Title, strings.Join(c.Files, ", ")))
			continue
		}
		if free <= 0 {
			skip(fmt.Sprintf("the project is at its limit of %d parallel agents", dev.MaxParallel))
			continue
		}
		if s.d.Provider != nil && s.d.ProviderSlots != nil {
			if pid := s.d.Provider(ctx, t.Assignee); pid != "" {
				slots, limited, err := s.d.ProviderSlots(ctx, pid)
				if err != nil {
					skip("provider " + pid + ": " + err.Error())
					continue
				}
				if limited && slots-usedProvider[pid] <= 0 {
					skip("provider " + pid + " is at its max_concurrent")
					continue
				}
				usedProvider[pid]++
			}
		}
		run, err := s.d.Start(ctx, t)
		if err != nil {
			skip("could not start: " + err.Error())
			continue
		}
		free--
		t.Started = 1
		underway = append(underway, t)
		res.Started = append(res.Started, t.ID)
		s.emit(ctx, "scheduler.started", t, map[string]any{"taskId": t.ID, "runId": run, "active": res.Active + len(res.Started), "max": dev.MaxParallel})
	}
	// scheduler.skipped is published when a task's reason is new or changed; a task that is no longer skipped is forgotten, so a
	// later skip for the same reason is said again
	last := s.skipped[projectID]
	if last == nil {
		last = map[string]string{}
		s.skipped[projectID] = last
	}
	still := map[string]bool{}
	for _, sk := range res.Skipped {
		still[sk.TaskID] = true
		if last[sk.TaskID] == sk.Reason {
			continue
		}
		last[sk.TaskID] = sk.Reason
		if t, err := s.d.Tasks.Get(ctx, sk.TaskID); err == nil {
			s.emit(ctx, "scheduler.skipped", t, map[string]any{"taskId": sk.TaskID, "reason": sk.Reason})
		}
	}
	for id := range last {
		if !still[id] {
			delete(last, id)
		}
	}
	return res, nil
}

func (s *Scheduler) runActive(ctx context.Context, runID string) (string, bool) {
	var st string
	if err := s.d.DB.Reader().QueryRowContext(ctx, `SELECT status FROM workflow_runs WHERE id=?`, runID).Scan(&st); err != nil {
		return "", false
	}
	switch workflow.RunStatus(st) {
	case workflow.StatusCompleted, workflow.StatusFailed, workflow.StatusCancelled:
		return "", false
	}
	return st, true
}

func (s *Scheduler) emit(ctx context.Context, typ string, t tasks.Task, data map[string]any) {
	if s.d.Bus == nil {
		return
	}
	b, _ := json.Marshal(data)
	_, _ = s.d.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: t.ProjectID, TaskID: t.ID, Data: b})
}

// Loop runs Tick for projects that opted into automatic scheduling (DevSettings.AutoSchedule), when a task completes,
// fails or is cancelled and on a timer. It stops when ctx ends. Scheduling is never on by default.
func (s *Scheduler) Loop(ctx context.Context, every time.Duration, projects func(ctx context.Context) []string) {
	if every <= 0 {
		every = 5 * time.Second
	}
	f := events.Filter{Types: []string{"task.completed", "task.failed", "task.cancelled", "task.ready", "task.created", "workflow.paused", "workflow.completed", "workflow.failed"}}
	ch, cancel := s.d.Bus.Subscribe(f)
	defer func() { cancel() }() // the subscription taken last, after a re-subscribe
	t := time.NewTicker(every)
	defer t.Stop()
	pass := func() {
		for _, p := range projects(ctx) {
			dev := DefaultDevSettings()
			if s.d.Dev != nil {
				dev = s.d.Dev(ctx, p)
			}
			if !dev.AutoSchedule {
				continue
			}
			_, _ = s.Tick(ctx, p)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				// the bus dropped this subscriber (it fell behind): take the same subscription again, not the firehose
				cancel()
				ch, cancel = s.d.Bus.Subscribe(f)
				continue
			}
			// coalesce a burst of events into one pass
			time.Sleep(150 * time.Millisecond)
			for drained := false; !drained; {
				select {
				case <-ch:
				default:
					drained = true
				}
			}
			pass()
		case <-t.C:
			pass()
		}
	}
}

// Limiter makes agent turns wait for a free slot: at most DevSettings.MaxParallel turns run at once per project, and each
// provider's max_concurrent is honoured through its own Acquire.
type Limiter struct {
	// Max returns the project's parallel-agent limit.
	Max func(ctx context.Context, projectID string) int
	// Provider acquires a slot on a registered provider and returns its release (providers.Monitor.Acquire).
	Provider func(ctx context.Context, providerID string) (release func(), err error)

	mu    sync.Mutex
	inUse map[string]int
	freed map[string]chan struct{}
	peak  map[string]int
}

// NewLimiter returns a Limiter.
func NewLimiter(max func(ctx context.Context, projectID string) int, provider func(ctx context.Context, providerID string) (func(), error)) *Limiter {
	return &Limiter{Max: max, Provider: provider, inUse: map[string]int{}, freed: map[string]chan struct{}{}, peak: map[string]int{}}
}

// Acquire waits for a project slot (and the provider's slot when providerID is set) and returns the release function.
func (l *Limiter) Acquire(ctx context.Context, projectID, providerID string) (func(), error) {
	for {
		max := 0
		if l.Max != nil {
			max = l.Max(ctx, projectID)
		}
		l.mu.Lock()
		if max <= 0 || l.inUse[projectID] < max {
			l.inUse[projectID]++
			if l.inUse[projectID] > l.peak[projectID] {
				l.peak[projectID] = l.inUse[projectID]
			}
			l.mu.Unlock()
			break
		}
		ch := l.freed[projectID]
		if ch == nil {
			ch = make(chan struct{})
			l.freed[projectID] = ch
		}
		l.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second): // the limit may have been raised in the settings
		}
	}
	releaseProject := func() {
		l.mu.Lock()
		l.inUse[projectID]--
		if ch := l.freed[projectID]; ch != nil {
			close(ch)
			delete(l.freed, projectID)
		}
		l.mu.Unlock()
	}
	if providerID == "" || l.Provider == nil {
		var once sync.Once
		return func() { once.Do(releaseProject) }, nil
	}
	relProv, err := l.Provider(ctx, providerID)
	if err != nil {
		releaseProject()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { relProv(); releaseProject() }) }, nil
}

// Peak reports the highest number of turns that held a slot at once in a project (for tests and the status line).
func (l *Limiter) Peak(projectID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.peak[projectID]
}

// InUse reports the slots held now.
func (l *Limiter) InUse(projectID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inUse[projectID]
}

// Middleware wraps agent_task nodes so each turn holds a slot while it runs. agent resolves the provider of the agent the
// node runs (from the task and the node's metadata); an empty provider means no provider limit applies.
func (l *Limiter) Middleware(provider func(ctx context.Context, in workflow.NodeInput) string) workflow.Middleware {
	return func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			pid := ""
			if provider != nil {
				pid = provider(ctx, in)
			}
			release, err := l.Acquire(ctx, in.ProjectID, pid)
			if err != nil {
				return workflow.NodeOutput{}, fmt.Errorf("waiting for an agent slot: %w", err)
			}
			defer release()
			return next(ctx, in)
		}
	}
}
