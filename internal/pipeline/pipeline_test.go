package pipeline_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

func TestMain(m *testing.M) {
	if os.Getenv("DRYDOCK_FAKE_AGENT") == "1" {
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

type env struct {
	dir   string
	repo  *git.Repo
	db    *storage.DB
	bus   *events.SQLBus
	tasks *tasks.Service
	eng   *workflow.Engine
	deps  pipeline.Deps
	task  tasks.Task
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// setup builds a real git repo from the gocalc fixture, a real database, and the pipeline deps. mk configures the agent.
func setup(t *testing.T, mkCfg func(e *env, role string) rt.Config, adapter agents.Adapter) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{dir: t.TempDir()}
	repoDir := filepath.Join(e.dir, "repo")
	os.MkdirAll(repoDir, 0o755)
	copyDir(t, filepath.Join("..", "..", "testdata", "fixtures", "gocalc"), repoDir)
	repo, err := git.Init(ctx, repoDir, "main")
	if err != nil {
		t.Fatal(err)
	}
	e.repo = repo
	db, err := storage.OpenTemp(ctx, filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	e.db = db
	e.bus = events.NewSQLBus(db, 4096)
	t.Cleanup(func() { e.eng.Close(); e.bus.Close(); db.Close() })
	db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','gocalc',?,1)`, repoDir)
	db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('a1','p1','Backend #1','claude','backend',1),('a2','p1','Reviewer #1','claude','reviewer',1)`)
	e.tasks = &tasks.Service{DB: db, Bus: e.bus}
	ms := memory.Store{DB: db}
	ms.Add(ctx, memory.Entry{ProjectID: "p1", Kind: memory.Convention, Title: "Small diffs", Body: "Prefer the smallest change that satisfies the task."}, true)
	ce := &ctxeng.Engine{DB: db, Memory: ms}
	runtime := &rt.Runtime{PM: proc.New(), Bus: e.bus, DB: db, Adapters: map[string]agents.Adapter{"claude": adapter}, NudgeWait: 3 * time.Second}
	e.deps = pipeline.Deps{
		Tasks: e.tasks, Runtime: runtime, PM: proc.New(), Bus: e.bus,
		Context:      ce,
		Repo:         func(context.Context, string) (*git.Repo, error) { return repo, nil },
		WorktreeRoot: filepath.Join(e.dir, "worktrees"), DataDir: filepath.Join(e.dir, "data"),
		AgentConfig: func(tk tasks.Task, role string) rt.Config { return mkCfg(e, role) },
	}
	e.eng = workflow.New(workflow.Config{DB: db, Bus: e.bus, DataDir: filepath.Join(e.dir, "data"), LeaseTTL: 3 * time.Second, SafePoint: pipeline.SafePointHook(e.deps)})
	task, err := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Implement Subtract",
		Description: "calc.go has a stub Subtract that returns 0. Implement it so a - b is returned and all tests pass.",
		Acceptance:  []string{"Given 5 and 3 When Subtract is called Then it returns 2"}})
	if err != nil {
		t.Fatal(err)
	}
	a1, a2 := "a1", "a2"
	e.task, _ = e.tasks.Patch(ctx, task.ID, tasks.Update{Assignee: &a1, Reviewer: &a2})
	return e
}

func (e *env) run(t *testing.T, maxLoops int, want ...workflow.RunStatus) (workflow.RunID, workflow.RunInfo) {
	t.Helper()
	g := pipeline.TaskPipeline(e.deps, maxLoops)
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	id, err := e.eng.Start(context.Background(), g.Name, pipeline.Input{TaskID: e.task.ID}, workflow.StartOptions{ProjectID: "p1", TaskID: e.task.ID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	info, err := e.eng.Await(ctx, id, func(r workflow.RunInfo) bool {
		for _, w := range want {
			if r.Status == w {
				return true
			}
		}
		return r.Status.Terminal()
	})
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	ok := false
	for _, w := range want {
		ok = ok || info.Status == w
	}
	if !ok {
		t.Fatalf("run ended %s (%s); wanted %v", info.Status, info.FailedReason, want)
	}
	return id, info
}

func fakeEnv(t *testing.T, s fakeagent.Script) (agents.Adapter, func(e *env, role string) rt.Config) {
	t.Helper()
	b, _ := json.Marshal(s)
	sp := filepath.Join(t.TempDir(), "script.json")
	os.WriteFile(sp, b, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	pl := filepath.Join(t.TempDir(), "prompts.log")
	t.Setenv("DRYDOCK_FAKE_PROMPT_LOG", pl)
	t.Setenv("DRYDOCK_FAKE_PROMPT_LOG_PATH", pl)
	return &claude.Adapter{Exe: os.Args[0]}, func(e *env, role string) rt.Config {
		return rt.Config{AgentID: "a1", Adapter: "claude", Provider: "fake", Tier: "premium", Spec: agents.RunSpec{AgentID: "a1", Model: "fake-1"}}
	}
}

func promptLog() []string {
	b, _ := os.ReadFile(os.Getenv("DRYDOCK_FAKE_PROMPT_LOG_PATH"))
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var s string
		if json.Unmarshal([]byte(l), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

const wrongImpl = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Subtract(a, b int) int { return a + b }\n"
const rightImpl = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Subtract(a, b int) int { return a - b }\n"

// A wrong first attempt fails the test check; the second pass gets ONLY the failure (not the first attempt's history),
// fixes it, and the pipeline finalizes with evidence and moves the task to REVIEW.
func TestPipelineLoopsOnFailingChecksWithMinimalContext(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{
		Rules: []fakeagent.Rule{{Match: "previous attempt needs changes", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "fixed"}}}},
		Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: wrongImpl}}, {Say: "implemented"}}},
	})
	e := setup(t, cfg, ad)
	runID, info := e.run(t, 3, workflow.StatusCompleted)

	p := promptLog()
	if len(p) != 2 {
		t.Fatalf("expected 2 agent turns, got %d: %q", len(p), p)
	}
	if !strings.Contains(p[0], "Implement Subtract") || !strings.Contains(p[0], "Acceptance criteria") {
		t.Fatalf("first prompt should carry the task spec: %q", p[0])
	}
	if !strings.Contains(p[1], "TestSubtract") || !strings.Contains(p[1], "check failed") {
		t.Fatalf("second prompt must carry the failing tests: %q", p[1])
	}
	// a later pass keeps the task (title, description, criteria) so the agent never loses what it is building, but carries
	// no history of the previous session: only the failure
	if !strings.Contains(p[1], "Task: Implement Subtract") || !strings.Contains(p[1], "Acceptance criteria") || !strings.Contains(p[1], "stub Subtract") {
		t.Fatalf("a rework pass must still carry the task: %q", p[1])
	}
	if strings.Contains(p[1], "implemented") || strings.Contains(p[1], "Work in the current directory") {
		t.Fatalf("a rework pass carries no previous-session history: %q", p[1])
	}

	task, _ := e.tasks.Get(context.Background(), e.task.ID)
	if task.Status != tasks.Review || task.Branch == "" || task.Worktree == "" || len(task.Files) != 1 || task.Files[0] != "calc.go" {
		t.Fatalf("task %+v", task)
	}
	ev, _ := e.tasks.Evidence(context.Background(), task.ID)
	kinds := map[string]int{}
	var lastTestPassed bool
	for _, x := range ev {
		kinds[x.Kind]++
		if x.Kind == "test" {
			lastTestPassed = x.Passed != nil && *x.Passed
		}
	}
	if kinds["build"] != 2 || kinds["test"] != 2 || kinds["diff"] != 1 || kinds["commit"] != 1 || !lastTestPassed {
		t.Fatalf("evidence kinds %v lastTestPassed=%v", kinds, lastTestPassed)
	}
	// the first test run failed, the second passed
	var fails int
	for _, x := range ev {
		if x.Kind == "test" && x.Passed != nil && !*x.Passed {
			fails++
		}
	}
	if fails != 1 {
		t.Fatalf("expected exactly one failing test evidence, got %d", fails)
	}
	// each pass of the checks has a round of its own, announced check by check; the newest pass is what LatestChecks reads
	var rounds []string
	for _, x := range ev {
		if r := pipeline.CheckRound(x.Data); pipeline.CheckKinds[x.Kind] && (len(rounds) == 0 || rounds[len(rounds)-1] != r) {
			rounds = append(rounds, r)
		}
	}
	if len(rounds) != 2 || rounds[0] == "" || rounds[1] == "" {
		t.Fatalf("two passes with a round each, got %q", rounds)
	}
	started, _ := e.bus.Replay(context.Background(), 0, events.Filter{TaskID: task.ID, Types: []string{pipeline.CheckStartedEvent}}, 100)
	var announced []string
	for _, x := range started {
		var d struct{ Kind, Round string }
		_ = json.Unmarshal(x.Data, &d)
		announced = append(announced, d.Kind+"@"+map[string]string{rounds[0]: "1", rounds[1]: "2"}[d.Round])
	}
	if strings.Join(announced, " ") != "build@1 test@1 build@2 test@2 lint@2 secrets@2" {
		t.Fatalf("announced checks %q", announced)
	}
	if got := pipeline.LatestChecks(ev, nil); len(got) != 4 || got[0].Status != pipeline.CheckPassed || got[1].Status != pipeline.CheckPassed || got[2].Kind != "lint" || got[3].Kind != "secrets" {
		t.Fatalf("latest checks %+v", got)
	}
	// the work is committed on the task branch, the main branch is untouched
	main, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go"))
	if strings.Contains(string(main), "return a - b") {
		t.Fatal("main working tree must not receive task changes")
	}
	wt, _ := os.ReadFile(filepath.Join(task.Worktree, "calc.go"))
	if !strings.Contains(string(wt), "return a - b") {
		t.Fatalf("worktree content: %s", wt)
	}
	st, _ := e.repo.Status(context.Background(), git.Worktree{Path: task.Worktree})
	if st.Dirty() {
		t.Fatalf("finalize must leave a clean committed worktree: %+v", st)
	}
	// safe points were recorded for the agent attempts and are restorable
	r1, _ := e.eng.SafePointRef(context.Background(), runID, "work", 2, 1)
	if r1 == "" {
		t.Fatal("no safe point recorded before the first agent attempt")
	}
	// the usage of both turns was recorded and rolled into task cost
	var n int
	e.db.Reader().QueryRow(`SELECT COUNT(*) FROM model_usage WHERE task_id=?`, task.ID).Scan(&n)
	if n != 2 || task.CostUSD <= 0 {
		t.Fatalf("usage rows=%d cost=%f", n, task.CostUSD)
	}
	_ = info
}

// An agent that never fixes the problem exhausts the loop: the run parks at a human request, the work stays uncommitted
// in the worktree, and the dirty worktree cannot be deleted.
func TestPipelineIterationLimitParksForHumanAndKeepsWork(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: wrongImpl}}, {Say: "done"}}})
	e := setup(t, cfg, ad)
	runID, _ := e.run(t, 2, workflow.StatusWaitingHuman)
	pend, _ := e.eng.Pending(context.Background(), runID)
	if len(pend) != 1 || !strings.Contains(pend[0].Spec.Title, "Iteration limit") {
		t.Fatalf("pending %+v", pend)
	}
	task, _ := e.tasks.Get(context.Background(), e.task.ID)
	if task.Status != tasks.WaitingHuman {
		t.Fatalf("the task waits for the human at the limit, got %s", task.Status)
	}
	var lp pipeline.LimitPayload
	if err := json.Unmarshal(pend[0].Spec.Payload, &lp); err != nil || lp.Trigger != pipeline.TriggerIterationLimit || lp.Origin != "verify" || !strings.Contains(lp.Evidence, "TestSubtract") || lp.Note == "" {
		t.Fatalf("the limit question must say why the run stopped and how to answer: %+v (%v)", lp, err)
	}
	w := git.Worktree{Path: task.Worktree, Branch: task.Branch, Base: task.BaseBranch, TaskID: task.ID}
	if err := e.repo.DeleteWorktree(context.Background(), w, false); err == nil {
		t.Fatal("a dirty worktree must never be deleted without approval")
	}
	// work attempts: initial + 2 loops = 3 agent turns
	if p := promptLog(); len(p) != 3 {
		t.Fatalf("turns %d", len(p))
	}
	// each attempt has a safe point; the first one holds the pristine pre-agent state
	refs := 0
	rows, _ := e.eng.Executions(context.Background(), runID)
	for _, r := range rows {
		if r.NodeID == "work" {
			if ref, _ := e.eng.SafePointRef(context.Background(), runID, "work", stepOf(rows, r), r.Attempt); ref != "" {
				refs++
			}
		}
	}
	if refs != 3 {
		t.Fatalf("safe points recorded for %d of 3 attempts", refs)
	}
}

func stepOf(rows []workflow.ExecRow, r workflow.ExecRow) int { return r.Step }

// Real Claude Code implements the fixture task end to end (skipped when claude is not installed/authenticated).
func TestRealClaudeImplementsTheFixtureTask(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	ad := claude.New()
	d, _ := ad.Detect(context.Background())
	if d.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: %s %v", d.Status, d.Problems)
	}
	e := setup(t, func(e *env, role string) rt.Config {
		return rt.Config{AgentID: "a1", Adapter: "claude", Provider: "anthropic", Tier: "premium",
			Spec: agents.RunSpec{AgentID: "a1", Model: "haiku", PermissionMode: "acceptEdits",
				AllowedTools: []string{"Read", "Edit", "Write", "Glob", "Grep"}, ExtraArgs: []string{"--no-session-persistence"}}}
	}, ad)
	_, _ = e.run(t, 3, workflow.StatusCompleted)
	task, _ := e.tasks.Get(context.Background(), e.task.ID)
	if task.Status != tasks.Review {
		t.Fatalf("task %s", task.Status)
	}
	wt, _ := os.ReadFile(filepath.Join(task.Worktree, "calc.go"))
	if !strings.Contains(string(wt), "a - b") && !strings.Contains(string(wt), "a-b") {
		t.Fatalf("Subtract not implemented:\n%s", wt)
	}
	ev, _ := e.tasks.Evidence(context.Background(), task.ID)
	passed := false
	for _, x := range ev {
		if x.Kind == "test" && x.Passed != nil && *x.Passed {
			passed = true
		}
	}
	if !passed {
		t.Fatal("no passing test evidence")
	}
	diff, _ := os.ReadFile(func() string {
		for _, x := range ev {
			if x.Kind == "diff" {
				return x.Ref
			}
		}
		return ""
	}())
	if !strings.Contains(string(diff), "Subtract") {
		t.Fatalf("diff evidence:\n%s", diff)
	}
	t.Logf("real claude finished the task: cost $%.4f, %d evidence items", task.CostUSD, len(ev))
}

func eventsFilter(taskID string) events.Filter { return events.Filter{TaskID: taskID} }
