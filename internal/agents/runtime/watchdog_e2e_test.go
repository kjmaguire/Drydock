package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

type wdEnv struct {
	*harness
	eng      *workflow.Engine
	wd       *watchdog.Watchdog
	mu       sync.Mutex
	notes    []string
	handoffs []string
}

func newWDEnv(t *testing.T, script fakeagent.Script, cfg watchdog.Config) *wdEnv {
	h := newHarness(t, script)
	e := &wdEnv{harness: h}
	e.eng = workflow.New(workflow.Config{DB: h.db, Bus: h.bus, DataDir: h.dir, LeaseTTL: 2 * time.Second})
	t.Cleanup(e.eng.Close)
	e.wd = watchdog.New(watchdog.SQLStore{DB: h.db}, watchdog.Hooks{
		Notify: func(ctx context.Context, typ string, tr watchdog.TripRecord, msg string) {
			e.mu.Lock()
			e.notes = append(e.notes, typ+": "+msg)
			e.mu.Unlock()
		},
		Handoff: func(ctx context.Context, n workflow.NodeInfo, tr watchdog.TripRecord) (string, error) {
			e.mu.Lock()
			e.handoffs = append(e.handoffs, tr.Signal+":"+tr.Evidence)
			e.mu.Unlock()
			return "handoff_test", nil
		},
	}, func(workflow.NodeInfo) watchdog.Config { return cfg })
	e.eng.Use(workflow.KindAgentTask, e.wd.Middleware())
	e.eng.Observe(e.wd.Observer())
	e.eng.Use(workflow.KindCheck, e.wd.CheckMiddleware(nil))
	return e
}

func (e *wdEnv) agentNode(id workflow.NodeID) *workflow.Node {
	return workflow.AgentTask(id, e.rt.AgentTask(rt.TaskSpec{Config: e.config()})).With("agent", "a1")
}

func (e *wdEnv) trips(t *testing.T, task string) (rows []string) {
	r, err := e.db.Reader().Query(`SELECT signal||'/'||stage FROM watchdog_trips WHERE task_id=? ORDER BY id`, task)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for r.Next() {
		var s string
		r.Scan(&s)
		rows = append(rows, s)
	}
	return rows
}

func (e *wdEnv) await(t *testing.T, run workflow.RunID, want ...workflow.RunStatus) workflow.RunInfo {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	info, err := e.eng.Await(ctx, run, func(r workflow.RunInfo) bool {
		for _, w := range want {
			if r.Status == w {
				return true
			}
		}
		return r.Status.Terminal()
	})
	if err != nil {
		t.Fatalf("await: %v (status %s %s)", err, info.Status, info.FailedReason)
	}
	for _, w := range want {
		if info.Status == w {
			return info
		}
	}
	t.Fatalf("run ended %s (%s), wanted %v; notes=%v", info.Status, info.FailedReason, want, e.notes)
	return info
}

func readStep(n int) []fakeagent.Step {
	return []fakeagent.Step{{Tool: "Read", Input: json.RawMessage(`{"file_path":"src/auth.go"}`), Result: "package auth // identical every time", Repeat: n}}
}

// (a) Same read, same result, three times: stage 1 nudge; the agent repeats; stage 2 pauses with a handoff; a human
// resumes with guidance and the task completes.
func TestWatchdogSpinNudgeThenPauseThenResume(t *testing.T) {
	loop := append(readStep(3), fakeagent.Step{Block: true})
	e := newWDEnv(t, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "GUIDANCE", Steps: []fakeagent.Step{{Say: "done after guidance"}}}},
		Turns: [][]fakeagent.Step{loop, loop},
	}, watchdog.Defaults())
	g := workflow.NewGraph("wd-spin").Add(e.agentNode("work")).Loop("work", "work", "answered", 2, "").Start("work")
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	run, err := e.eng.Start(context.Background(), "wd-spin", "fix the auth bug", workflow.StartOptions{ProjectID: "p1", TaskID: "task_wd1"})
	if err != nil {
		t.Fatal(err)
	}
	e.await(t, run, workflow.StatusWaitingHuman)

	if got := strings.Join(e.trips(t, "task_wd1"), ","); got != "tool_spin/1,tool_spin/2" {
		t.Fatalf("trips %s", got)
	}
	p := prompts(t, e.dir)
	if len(p) != 2 || !strings.Contains(p[1], "Watchdog notice") || !strings.Contains(p[1], "src/auth.go") || !strings.Contains(p[1], "different approach") {
		t.Fatalf("the stage-1 nudge must reach the same session with the evidence: %q", p)
	}
	e.mu.Lock()
	if len(e.handoffs) != 1 || !strings.Contains(e.handoffs[0], "tool_spin") {
		t.Fatalf("stage 2 must write a handoff with evidence: %v", e.handoffs)
	}
	var kinds []string
	for _, n := range e.notes {
		kinds = append(kinds, strings.SplitN(n, ":", 2)[0])
	}
	e.mu.Unlock()
	if strings.Join(kinds, ",") != "watchdog.tripped,watchdog.paused" {
		t.Fatalf("notifications %v", kinds)
	}
	pend, _ := e.eng.Pending(context.Background(), run)
	if len(pend) != 1 || pend[0].Spec.Kind != "question" || !strings.Contains(pend[0].Spec.Title, "Watchdog") {
		t.Fatalf("pending %+v", pend)
	}
	// the human resumes with guidance, which resets the trip count and allows a fresh attempt
	if err := e.wd.Reset(context.Background(), "task_wd1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := e.eng.Resume(context.Background(), run, workflow.Response{RequestID: pend[0].ID, Decision: "answered", Data: json.RawMessage(`{"text":"GUIDANCE: look at middleware"}`), By: "human:k"}); err != nil {
		t.Fatal(err)
	}
	info := e.await(t, run, workflow.StatusCompleted)
	if !strings.Contains(string(info.Result), "done after guidance") {
		t.Fatalf("result %s", info.Result)
	}
	if n, _ := watchdogCount(e, "task_wd1"); n != 0 {
		t.Fatalf("trip count after human guidance should be reset, got %d", n)
	}
}

func watchdogCount(e *wdEnv, task string) (int, error) {
	return watchdog.SQLStore{DB: e.db}.Count(context.Background(), task)
}

// (b) A check fails with the same signature while the worktree diff is unchanged: stage 1 queues a nudge for the next
// turn; the next identical failure pauses the task.
func TestWatchdogErrorEcho(t *testing.T) {
	e := newWDEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "I believe it is fixed now"}}}, watchdog.Defaults())
	var checks int32
	check := workflow.Check("test", func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		atomic.AddInt32(&checks, 1)
		res := workflow.CheckResult{Name: "test", Passed: false, ExitCode: 1, Output: fmt.Sprintf("--- FAIL: TestLogin (0.%02ds)\nexpected 200 got 500", 10+atomic.LoadInt32(&checks)), FailingTests: []string{"TestLogin"}, DiffHash: "unchanged-diff"}
		b, _ := json.Marshal(res)
		return workflow.NodeOutput{Value: b, Route: "fail"}, nil
	})
	g := workflow.NewGraph("wd-echo").Add(e.agentNode("work"), check, workflow.Request("human", workflow.RequestSpec{Kind: "question", Title: "limit"})).
		Edge("work", "test").Loop("test", "work", "fail", 6, "human").Start("work")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "wd-echo", "make tests pass", workflow.StartOptions{ProjectID: "p1", TaskID: "task_wd2"})
	e.await(t, run, workflow.StatusWaitingHuman)

	if got := strings.Join(e.trips(t, "task_wd2"), ","); got != "error_echo/1,error_echo/2" {
		t.Fatalf("trips %s", got)
	}
	if n := atomic.LoadInt32(&checks); n != 3 {
		t.Fatalf("expected 3 check runs (fail, echo stage 1, echo stage 2), got %d", n)
	}
	p := prompts(t, e.dir)
	if len(p) != 3 || strings.Contains(p[1], "Watchdog notice") || !strings.Contains(p[2], "Watchdog notice") || !strings.Contains(p[2], "same signature") {
		t.Fatalf("the nudge from the stage-1 echo must be delivered with the third turn only: %q", p)
	}
	pend, _ := e.eng.Pending(context.Background(), run)
	if len(pend) != 1 || !strings.Contains(pend[0].Spec.Title, "error_echo") {
		t.Fatalf("pending %+v", pend)
	}
}

// (c) The per-turn tool-call cap trips even when every call differs (so tool spin cannot catch it).
func TestWatchdogToolCallCap(t *testing.T) {
	cap61 := []fakeagent.Step{{Tool: "Grep", Input: json.RawMessage(`{"q":"x"}`), Result: "match", Repeat: 70, Vary: true}, {Block: true}}
	e := newWDEnv(t, fakeagent.Script{Turns: [][]fakeagent.Step{cap61, {{Say: "stopping and summarising"}}}}, watchdog.Defaults())
	g := workflow.NewGraph("wd-cap").Add(e.agentNode("work")).Start("work")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "wd-cap", "explore", workflow.StartOptions{ProjectID: "p1", TaskID: "task_wd3"})
	info := e.await(t, run, workflow.StatusCompleted)
	if got := strings.Join(e.trips(t, "task_wd3"), ","); got != "tool_call_cap/1" {
		t.Fatalf("trips %s", got)
	}
	p := prompts(t, e.dir)
	if len(p) != 2 || !strings.Contains(p[1], "tool calls in one turn") {
		t.Fatalf("prompts %q", p)
	}
	if !strings.Contains(string(info.Result), "stopping and summarising") {
		t.Fatalf("result %s", info.Result)
	}
}

// (d) False-positive control: in the Iterate Until Verified shape, tests are re-run after each of several real edits
// and the loop converges. The watchdog must never trip.
func TestWatchdogFalsePositiveControlIterateUntilVerified(t *testing.T) {
	var iter int32
	script := fakeagent.Script{Default: []fakeagent.Step{
		{Tool: "Read", Input: json.RawMessage(`{"file_path":"handler.go"}`), Result: "func Handler() {}"},
		{Tool: "Edit", Input: json.RawMessage(`{"file_path":"handler.go"}`), Result: "edited", Repeat: 3, Vary: true},
		{Tool: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`), Result: "ran tests"},
		{Say: "tests were re-run after my edits"},
	}}
	e := newWDEnv(t, script, watchdog.IterateUntilVerified())
	check := workflow.Check("test", func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		i := int(atomic.AddInt32(&iter, 1))
		res := workflow.CheckResult{Name: "test", Passed: i >= 5, DiffHash: fmt.Sprintf("diff-%d", i)}
		route := "pass"
		if !res.Passed {
			res.Output = fmt.Sprintf("--- FAIL: TestN%d (0.%02ds)\n%d tests failed", i, 10+i, 6-i)
			res.FailingTests = []string{fmt.Sprintf("TestN%d", i)}
			route = "fail"
		}
		b, _ := json.Marshal(res)
		return workflow.NodeOutput{Value: b, Route: route}, nil
	})
	g := workflow.NewGraph("wd-iuv").Add(e.agentNode("work"), check).Edge("work", "test").Loop("test", "work", "fail", 8, "").Start("work")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "wd-iuv", "implement the handler", workflow.StartOptions{ProjectID: "p1", TaskID: "task_wd4"})
	e.await(t, run, workflow.StatusCompleted)
	if trips := e.trips(t, "task_wd4"); len(trips) != 0 {
		t.Fatalf("false trip(s) on healthy iteration: %v", trips)
	}
	if got := atomic.LoadInt32(&iter); got != 5 {
		t.Fatalf("expected 5 iterations, got %d", got)
	}
}
