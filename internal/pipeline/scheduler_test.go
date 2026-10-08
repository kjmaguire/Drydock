package pipeline_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// dropHarnessTask cancels the task newTpl created, so the scheduler only sees the tasks a test creates.
func (e *tpl) dropHarnessTask() {
	e.t.Helper()
	if _, err := e.tasks.Transition(context.Background(), e.task.ID, tasks.Cancelled, tasks.TransitionOpts{By: "human:test"}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *tpl) newTask(title string, prio int, deps ...string) tasks.Task {
	e.t.Helper()
	tk, err := e.tasks.Create(context.Background(), tasks.CreateReq{ProjectID: "p1", Title: title, Description: title, Priority: prio, DependsOn: deps,
		Acceptance: []string{"Given x, when y, then z"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return tk
}

func (e *tpl) assign(tk tasks.Task) tasks.Task {
	w, r := aWork, aReview
	out, err := e.tasks.Patch(context.Background(), tk.ID, tasks.Update{Assignee: &w, Reviewer: &r})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// sleepyGraph is a one-node workflow that holds an agent slot for a while, so concurrency can be observed.
func sleepyGraph(ts *tasks.Service, running *atomic.Int32, peak *atomic.Int32, d time.Duration) *workflow.Graph {
	return workflow.NewGraph("sched-sleepy").Add(workflow.Route("nap", func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		// like the real prepare node: the task leaves READY when its run begins
		for _, to := range []tasks.Status{tasks.Claimed, tasks.InProgress} {
			if _, err := ts.Transition(ctx, in.TaskID, to, tasks.TransitionOpts{By: "system"}); err != nil {
				return workflow.NodeOutput{}, err
			}
		}
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(d)
		running.Add(-1)
		return workflow.NodeOutput{Value: []byte(`"done"`)}, nil
	})).Start("nap")
}

func schedFor(e *tpl, max int, start func(context.Context, tasks.Task) (workflow.RunID, error)) *pipeline.Scheduler {
	return pipeline.NewScheduler(pipeline.SchedulerDeps{Tasks: e.tasks, DB: e.db, Bus: e.bus,
		Dev: func(context.Context, string) pipeline.DevSettings {
			return pipeline.DevSettings{MaxParallel: max, Respond: true}
		},
		Start: start})
}

// The scheduler starts READY tasks within the project's parallel limit, highest priority first, only when the task has a
// worker and a reviewer, and only when its dependencies are COMPLETED.
func TestSchedulerStartsWithinTheLimitInPriorityOrderAndRespectsDependencies(t *testing.T) {
	e := newTpl(t, map[string]fakeagent.Script{aWork: shipReviewer(), aReview: shipReviewer()}, tplOpts{title: "Harness task"})
	e.dropHarnessTask()
	var running, peak atomic.Int32
	e.eng.Register(sleepyGraph(e.tasks, &running, &peak, 700*time.Millisecond))
	var started []string
	var mu sync.Mutex
	start := func(ctx context.Context, tk tasks.Task) (workflow.RunID, error) {
		id, err := e.eng.Start(ctx, "sched-sleepy", pipeline.Input{TaskID: tk.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: tk.ID})
		if err == nil {
			rid := string(id)
			e.tasks.Patch(ctx, tk.ID, tasks.Update{RunID: &rid})
			mu.Lock()
			started = append(started, tk.Title)
			mu.Unlock()
		}
		return id, err
	}
	s := schedFor(e, 2, start)
	low := e.assign(e.newTask("low", 0))
	high := e.assign(e.newTask("high", 9))
	mid := e.assign(e.newTask("mid", 5))
	unassigned := e.newTask("unassigned", 9)
	child := e.assign(e.newTask("child", 9, low.ID))
	_ = high
	_ = mid
	_ = unassigned

	res, err := s.Tick(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := strings.Join(started, ",")
	mu.Unlock()
	if got != "high,mid" || res.Capacity != 2 || res.Max != 2 {
		t.Fatalf("started %s result %+v", got, res)
	}
	reasons := map[string]string{}
	for _, sk := range res.Skipped {
		reasons[sk.TaskID] = sk.Reason
	}
	if !strings.Contains(reasons[unassigned.ID], "no worker or reviewer") || !strings.Contains(reasons[low.ID], "limit of 2 parallel agents") {
		t.Fatalf("skip reasons %+v", reasons)
	}
	if _, ok := reasons[child.ID]; ok {
		t.Fatal("a task whose dependency is not COMPLETED is not READY, so it is not even considered")
	}
	// a second pass while both run: no capacity, nothing new starts
	res2, _ := s.Tick(context.Background(), "p1")
	if len(res2.Started) != 0 || res2.Active != 2 || res2.Capacity != 0 {
		t.Fatalf("%+v", res2)
	}
	// they finish; the next pass starts the waiting task, never more than two at once
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := countActive(e); n == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	res3, _ := s.Tick(context.Background(), "p1")
	if len(res3.Started) != 1 || res3.Started[0] != low.ID {
		t.Fatalf("%+v", res3)
	}
	if peak.Load() > 2 {
		t.Fatalf("peak concurrent runs %d, limit is 2", peak.Load())
	}
	// the dependent starts only after its dependency is COMPLETED (set directly once low's own run has finished, so the two
	// writers cannot race)
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if n, _ := countActive(e); n == 0 {
			break
		}
	}
	e.db.Writer().Exec(`UPDATE tasks SET status='COMPLETED', version=version+1 WHERE id=?`, low.ID)
	res4, _ := s.Tick(context.Background(), "p1")
	if len(res4.Started) != 1 || res4.Started[0] != child.ID || len(res4.Promoted) == 0 {
		t.Fatalf("%+v", res4)
	}
}

func countActive(e *tpl) (int, error) {
	var n int
	err := e.db.Reader().QueryRow(`SELECT COUNT(*) FROM workflow_runs WHERE status IN ('PENDING','RUNNING','RECOVERING')`).Scan(&n)
	return n, err
}

// A provider with max_concurrent 1 admits one task per pass even when the project allows more.
func TestSchedulerHonoursProviderMaxConcurrent(t *testing.T) {
	e := newTpl(t, map[string]fakeagent.Script{aWork: shipReviewer(), aReview: shipReviewer()}, tplOpts{title: "Harness task"})
	e.dropHarnessTask()
	var running, peak atomic.Int32
	e.eng.Register(sleepyGraph(e.tasks, &running, &peak, 400*time.Millisecond))
	start := func(ctx context.Context, tk tasks.Task) (workflow.RunID, error) {
		return e.eng.Start(ctx, "sched-sleepy", pipeline.Input{TaskID: tk.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: tk.ID})
	}
	s := pipeline.NewScheduler(pipeline.SchedulerDeps{Tasks: e.tasks, DB: e.db, Bus: e.bus,
		Dev:           func(context.Context, string) pipeline.DevSettings { return pipeline.DevSettings{MaxParallel: 5} },
		Start:         start,
		Provider:      func(context.Context, string) string { return "openrouter" },
		ProviderSlots: func(context.Context, string) (int, bool, error) { return 1, true, nil }})
	for i := 0; i < 3; i++ {
		e.assign(e.newTask(fmt.Sprintf("t%d", i), 0))
	}
	res, err := s.Tick(context.Background(), "p1")
	if err != nil || len(res.Started) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	found := false
	for _, sk := range res.Skipped {
		found = found || strings.Contains(sk.Reason, "max_concurrent")
	}
	if !found {
		t.Fatalf("%+v", res.Skipped)
	}
}

func TestLimiterBoundsConcurrencyAndHonoursCancellation(t *testing.T) {
	var provIn, provPeak atomic.Int32
	prov := func(ctx context.Context, id string) (func(), error) {
		if id == "bad" {
			return nil, fmt.Errorf("provider disabled")
		}
		n := provIn.Add(1)
		for {
			p := provPeak.Load()
			if n <= p || provPeak.CompareAndSwap(p, n) {
				break
			}
		}
		return func() { provIn.Add(-1) }, nil
	}
	l := pipeline.NewLimiter(func(context.Context, string) int { return 2 }, prov)
	var wg sync.WaitGroup
	var in, peak atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := l.Acquire(context.Background(), "p1", "x")
			if err != nil {
				t.Error(err)
				return
			}
			n := in.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(60 * time.Millisecond)
			in.Add(-1)
			rel()
			rel() // idempotent
		}()
	}
	wg.Wait()
	if peak.Load() != 2 || l.Peak("p1") != 2 || l.InUse("p1") != 0 || provIn.Load() != 0 {
		t.Fatalf("peak=%d limiterPeak=%d inUse=%d provider=%d", peak.Load(), l.Peak("p1"), l.InUse("p1"), provIn.Load())
	}
	// projects are independent
	r1, _ := l.Acquire(context.Background(), "a", "")
	r2, _ := l.Acquire(context.Background(), "a", "")
	other, err := l.Acquire(context.Background(), "b", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx, "a", ""); err == nil {
		t.Fatal("a third waiter in a full project must wait until its context ends")
	}
	// a provider failure gives the project slot back
	if _, err := l.Acquire(context.Background(), "b", "bad"); err == nil {
		t.Fatal("provider error must surface")
	}
	if l.InUse("b") != 1 {
		t.Fatalf("project slot leaked after a provider error: %d", l.InUse("b"))
	}
	r1()
	r2()
	other()
}

// Three real tasks and one that conflicts, started by the scheduler with a limit of 2, each in its own worktree. Agent turns
// overlap but never exceed the limit; every task stops at a human approval; the merges are human actions, and the
// conflicting one is parked as a question instead of being merged.
func TestParallelTasksUseSeparateWorktreesRespectTheLimitAndNeedHumanMerges(t *testing.T) {
	nap := fakeagent.Step{SleepMS: 500}
	file := func(path, body string) []fakeagent.Step {
		return []fakeagent.Step{nap, writeStep(path, body), say("done")}
	}
	addA := greenCalc + "\n// variant A\nfunc AddA(a, b int) int { return a + b }\n"
	addB := greenCalc + "\n// variant B\nfunc AddB(a, b int) int { return b + a }\n"
	e := newTpl(t, map[string]fakeagent.Script{
		aWork: {Rules: []fakeagent.Rule{
			{Match: "Task: Add Multiply", Steps: file("multiply.go", "package gocalc\n\nfunc Multiply(a, b int) int { return a * b }\n")},
			{Match: "Task: Add Divide", Steps: file("divide.go", "package gocalc\n\nfunc Divide(a, b int) int { return a / b }\n")},
			{Match: "Task: Variant A", Steps: file("calc.go", addA)},
			{Match: "Task: Variant B", Steps: file("calc.go", addB)},
		}},
		aReview: shipReviewer(),
	}, tplOpts{title: "Harness task"})
	e.dropHarnessTask()
	if err := e.eng.Register(pipeline.IterateUntilVerified(e.td.Deps, projectInfo, 3)); err != nil {
		t.Fatal(err)
	}
	lim := pipeline.NewLimiter(func(context.Context, string) int { return 2 }, nil)
	e.eng.Use(workflow.KindAgentTask, lim.Middleware(nil))
	start := func(ctx context.Context, tk tasks.Task) (workflow.RunID, error) {
		id, err := e.eng.Start(ctx, pipeline.WorkflowIUV, pipeline.Input{TaskID: tk.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: tk.ID})
		if err == nil {
			rid := string(id)
			e.tasks.Patch(ctx, tk.ID, tasks.Update{RunID: &rid})
		}
		return id, err
	}
	s := schedFor(e, 2, start)
	var all []tasks.Task
	for _, title := range []string{"Add Multiply", "Add Divide", "Variant A", "Variant B"} {
		all = append(all, e.assign(e.newTask(title, 0)))
	}

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := s.Tick(context.Background(), "p1"); err != nil {
			t.Fatal(err)
		}
		parked := 0
		for _, tk := range all {
			cur, _ := e.tasks.Get(context.Background(), tk.ID)
			if cur.RunID == "" {
				continue
			}
			if p, _ := e.eng.Pending(context.Background(), workflow.RunID(cur.RunID)); len(p) == 1 && p[0].NodeID == "approve" {
				parked++
			}
		}
		if parked == len(all) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	// separate worktrees, one branch each
	paths, branches := map[string]bool{}, map[string]bool{}
	for _, tk := range all {
		cur, _ := e.tasks.Get(context.Background(), tk.ID)
		if cur.Worktree == "" || cur.Status != tasks.Verification {
			t.Fatalf("task %s: %s worktree %q", cur.Title, cur.Status, cur.Worktree)
		}
		paths[cur.Worktree], branches[cur.Branch] = true, true
	}
	if len(paths) != 4 || len(branches) != 4 {
		t.Fatalf("worktrees %v branches %v", paths, branches)
	}
	// the limit held, and real parallelism happened
	if lim.Peak("p1") != 2 {
		t.Fatalf("limiter peak %d, want exactly 2 (parallel but never more)", lim.Peak("p1"))
	}
	if maxOverlap(e) > 2 {
		t.Fatalf("agent processes overlapped %d at once with a limit of 2", maxOverlap(e))
	}
	if maxOverlap(e) < 2 {
		t.Fatalf("expected two agents to run at the same time, saw %d", maxOverlap(e))
	}
	// main is untouched until a human merges
	if _, err := os.Stat(filepath.Join(e.repo.Root, "multiply.go")); err == nil {
		t.Fatal("nothing may reach main before the human merges")
	}
	// the human merges them one by one; the second change to calc.go conflicts and is parked, not forced
	merged := 0
	var conflicted workflow.RunID
	for _, tk := range all {
		cur, _ := e.tasks.Get(context.Background(), tk.ID)
		rid := workflow.RunID(cur.RunID)
		p, _ := e.eng.Pending(context.Background(), rid)
		if err := e.eng.Resume(context.Background(), rid, workflow.Response{RequestID: p[0].ID, Decision: "approved", By: "human:kyle"}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		info, err := e.eng.Await(ctx, rid, func(r workflow.RunInfo) bool {
			return r.Status == workflow.StatusCompleted || r.Status == workflow.StatusWaitingHuman || r.Status.Terminal()
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if info.Status == workflow.StatusCompleted {
			merged++
		} else if info.Status == workflow.StatusWaitingHuman {
			conflicted = rid
		}
	}
	if merged != 3 || conflicted == "" {
		t.Fatalf("merged %d conflicted %q", merged, conflicted)
	}
	time.Sleep(60 * time.Millisecond)
	p, _ := e.eng.Pending(context.Background(), conflicted)
	if len(p) != 1 || !strings.Contains(p[0].Spec.Title, "Merge conflict") {
		t.Fatalf("pending %+v", p)
	}
	for _, f := range []string{"multiply.go", "divide.go"} {
		if _, err := os.Stat(filepath.Join(e.repo.Root, f)); err != nil {
			t.Fatalf("%s should be merged: %v", f, err)
		}
	}
}

// maxOverlap is the highest number of agent processes alive at once, from the agent_runs table.
func maxOverlap(e *tpl) int {
	rows, err := e.db.Reader().Query(`SELECT started_at, ended_at FROM agent_runs`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	type ev struct {
		at    int64
		delta int
	}
	var evs []ev
	for rows.Next() {
		var s, en int64
		rows.Scan(&s, &en)
		if en == 0 {
			en = time.Now().UnixMilli()
		}
		evs = append(evs, ev{s, 1}, ev{en, -1})
	}
	// ends sort before starts at the same instant
	for i := range evs {
		for j := i + 1; j < len(evs); j++ {
			if evs[j].at < evs[i].at || (evs[j].at == evs[i].at && evs[j].delta < evs[i].delta) {
				evs[i], evs[j] = evs[j], evs[i]
			}
		}
	}
	cur, max := 0, 0
	for _, x := range evs {
		cur += x.delta
		if cur > max {
			max = cur
		}
	}
	return max
}
