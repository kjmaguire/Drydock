package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The team lead's tests. Every agent, the lead included, is the scripted stand-in (fakeagent: a labelled test double, a
// real process speaking Claude's dialect) with its own script and prompt log; git, go test, SQLite, the engine and the
// watchdog are real.

const (
	agWork   = "a1" // Backend #1, the task's worker
	agReview = "a2" // Reviewer #1
	agOther  = "a3" // Backend #2
	agLead   = "l1" // Lead
)

type leadEnv struct {
	*env
	lead *pipeline.Lead
	// leadDeps are what lead was built from: a test makes a new service from them to play a restart of the app
	leadDeps pipeline.LeadDeps
	wd       *watchdog.Watchdog
	logs     map[string]string
	mu       sync.Mutex
	posts    []string
	devs     pipeline.DevSettings
	nowFunc  func() time.Time
}

// newLeadEnv builds the gocalc repository, the database, the runtime and the lead service, with one scripted agent per entry
// in scripts (by agent id). withLead=false leaves the lead off the team. The watchdog is not in the engine (a worker that
// writes the same wrong file every time would trip its error echo first): see newWatchedLeadEnv.
func newLeadEnv(t *testing.T, scripts map[string]fakeagent.Script, withLead bool) *leadEnv {
	return buildLeadEnv(t, scripts, withLead, false)
}

// newWatchedLeadEnv is newLeadEnv with the watchdog in the engine, wired to the lead the way the core wires it.
func newWatchedLeadEnv(t *testing.T, scripts map[string]fakeagent.Script) *leadEnv {
	return buildLeadEnv(t, scripts, true, true)
}

func buildLeadEnv(t *testing.T, scripts map[string]fakeagent.Script, withLead, watched bool) *leadEnv {
	t.Helper()
	ctx := context.Background()
	e := &leadEnv{env: &env{dir: t.TempDir()}, logs: map[string]string{}, devs: pipeline.DefaultDevSettings()}
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
	members := [][3]string{{agWork, "Backend #1", "backend"}, {agReview, "Reviewer #1", "reviewer"}, {agOther, "Backend #2", "backend"}}
	if withLead {
		members = append(members, [3]string{agLead, "Lead", "lead"})
	}
	for _, m := range members {
		db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES(?,?,?,?,?,1)`, m[0], "p1", m[1], "claude", m[2])
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	scriptPath := map[string]string{}
	for id, sc := range scripts {
		b, _ := json.Marshal(sc)
		sp := filepath.Join(e.dir, id+".json")
		os.WriteFile(sp, b, 0o644)
		scriptPath[id] = sp
		e.logs[id] = filepath.Join(e.dir, id+".prompts")
	}
	cfgFor := func(id string) rt.Config {
		return rt.Config{AgentID: id, Adapter: "claude", Provider: "fake", Tier: "premium",
			Spec: agents.RunSpec{AgentID: id, Model: "fake-1", PermissionMode: "acceptEdits", Env: []string{"DRYDOCK_FAKE_SCRIPT=" + scriptPath[id], "DRYDOCK_FAKE_PROMPT_LOG=" + e.logs[id]}}}
	}
	e.tasks = &tasks.Service{DB: db, Bus: e.bus}
	runtime := &rt.Runtime{PM: proc.New(), Bus: e.bus, DB: db, Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}, NudgeWait: 3 * time.Second}
	e.deps = pipeline.Deps{
		Tasks: e.tasks, Runtime: runtime, PM: proc.New(), Bus: e.bus,
		Context:      &ctxeng.Engine{DB: db, Memory: memory.Store{DB: db}},
		Repo:         func(context.Context, string) (*git.Repo, error) { return repo, nil },
		WorktreeRoot: filepath.Join(e.dir, "worktrees"), DataDir: filepath.Join(e.dir, "data"),
		AgentConfig: func(tk tasks.Task, role string) rt.Config {
			if role == "review" {
				return cfgFor(tk.Reviewer)
			}
			return cfgFor(tk.Assignee)
		},
	}
	e.leadDeps = pipeline.LeadDeps{
		Tasks: e.tasks, Runtime: runtime, Bus: e.bus,
		Team: func(ctx context.Context, projectID string) ([]pipeline.LeadMember, error) {
			rows, err := db.Reader().QueryContext(ctx, `SELECT id,name,role,adapter FROM agents WHERE project_id=? ORDER BY created_at,id`, projectID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var out []pipeline.LeadMember
			for rows.Next() {
				var m pipeline.LeadMember
				rows.Scan(&m.ID, &m.Name, &m.Role, &m.Adapter)
				out = append(out, m)
			}
			return out, nil
		},
		Config: func(ctx context.Context, id string) (rt.Config, error) { return cfgFor(id), nil },
		Cwd:    func(context.Context, string, string) string { return repoDir },
		Assign: func(ctx context.Context, taskID, worker, reviewer string) error {
			if worker == reviewer {
				return errors.New("same agent")
			}
			_, err := e.tasks.Patch(ctx, taskID, tasks.Update{Assignee: &worker, Reviewer: &reviewer})
			return err
		},
		Post: func(ctx context.Context, projectID, taskID, agentID, body string) (string, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.posts = append(e.posts, agentID+"|"+taskID+"|"+body)
			return "msg_" + agentID, nil
		},
		Dev: func(context.Context, string) pipeline.DevSettings { return e.devs },
		Steer: func(ctx context.Context, taskID, msg string, escalate bool) bool {
			return e.wd.Steer(ctx, taskID, msg, escalate)
		},
		Now: func() time.Time {
			if e.nowFunc != nil {
				return e.nowFunc()
			}
			return time.Now()
		},
	}
	e.lead = pipeline.NewLead(e.leadDeps)
	e.deps.Lead = e.lead
	e.eng = workflow.New(workflow.Config{DB: db, Bus: e.bus, DataDir: filepath.Join(e.dir, "data"), LeaseTTL: 3 * time.Second, SafePoint: pipeline.SafePointHook(e.deps)})
	// the watchdog, wired to the lead the way the core wires it
	e.wd = watchdog.New(watchdog.SQLStore{DB: db}, watchdog.Hooks{
		Lead: func(ctx context.Context, n workflow.NodeInfo) bool {
			return e.lead.HasLead(ctx, n.TaskID, string(n.RunID), n.Meta["agent"])
		},
		Supervise: func(ctx context.Context, n workflow.NodeInfo, tr watchdog.TripRecord) (watchdog.Steer, bool) {
			o := e.lead.Supervise(ctx, pipeline.LeadTrigger{Kind: pipeline.TriggerWatchdog, TaskID: n.TaskID, RunID: string(n.RunID), NodeAgent: n.Meta["agent"], Evidence: tr.Evidence})
			return watchdog.Steer{Action: o.Decision.Action, Message: o.Decision.Message}, o.Intervened
		},
	}, watchdog.ForNode)
	if watched {
		e.eng.Use(workflow.KindAgentTask, e.wd.Middleware())
		e.eng.Observe(e.wd.Observer())
		e.eng.Use(workflow.KindCheck, e.wd.CheckMiddleware(nil))
	}
	task, err := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Implement Subtract",
		Description: "calc.go has a stub Subtract that returns 0. Implement it so a - b is returned and all tests pass.",
		Acceptance:  []string{"Given 5 and 3 When Subtract is called Then it returns 2"}})
	if err != nil {
		t.Fatal(err)
	}
	w, r := agWork, agReview
	e.task, _ = e.tasks.Patch(ctx, task.ID, tasks.Update{Assignee: &w, Reviewer: &r})
	return e
}

func (e *leadEnv) prompts(agent string) []string { return prompts(e.logs[agent]) }

func (e *leadEnv) interventions(t *testing.T) []pipeline.Intervention {
	t.Helper()
	iv, err := pipeline.Interventions(context.Background(), e.db, "p1", e.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return iv
}

func leadSays(js string) fakeagent.Script {
	return fakeagent.Script{Default: []fakeagent.Step{{Say: js}}}
}

func fixesOn(keyword string) fakeagent.Script {
	return wrongWorker(fakeagent.Rule{Match: keyword, Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "fixed"}}})
}

var shipper = fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}

// Trigger (b) + action nudge: at the iteration limit the lead is asked first; its nudge reaches the worker as guidance and
// the run continues with a fresh loop budget, without a human. The intervention is stored, posted by the lead and published.
func TestLeadNudgesAtTheIterationLimit(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{
		agWork: fixesOn("LEADFIX"), agReview: shipper,
		agLead: leadSays(`{"action":"nudge","message":"LEADFIX: Subtract must return a - b; you keep returning a + b.","reason":"the same test failed twice"}`),
	}, true)
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	if p := e.pendingOne(t, id); p.NodeID != "approve" {
		t.Fatalf("the lead's nudge should carry the run to the approval, pending %s", p.NodeID)
	}
	wp := e.prompts(agWork)
	if len(wp) != 3 || !strings.Contains(wp[2], "Guidance from the team lead:\nLEADFIX: Subtract must return a - b") {
		t.Fatalf("worker prompts %q", wp)
	}
	lp := e.prompts(agLead)
	if len(lp) != 1 {
		t.Fatalf("one supervision turn, got %d", len(lp))
	}
	for _, want := range []string{"Drydock phase: supervise", "Task #1: Implement Subtract", "Trigger: iteration_limit", "TestSubtract", "Backend #2: backend, claude", "[works on this task]", `"action":"nudge|reassign|split|escalate|continue"`} {
		if !strings.Contains(lp[0], want) {
			t.Fatalf("the digest lacks %q:\n%s", want, lp[0])
		}
	}
	iv := e.interventions(t)
	if len(iv) != 1 || iv[0].Trigger != pipeline.TriggerIterationLimit || iv[0].Action != "nudge" || iv[0].LeadAgentID != agLead || iv[0].MessageID != "msg_"+agLead {
		t.Fatalf("interventions %+v", iv)
	}
	e.mu.Lock()
	if len(e.posts) != 1 || !strings.HasPrefix(e.posts[0], agLead+"|"+e.task.ID+"|Team lead: nudge") || !strings.Contains(e.posts[0], "Reason: the same test failed twice") {
		t.Fatalf("posts %q", e.posts)
	}
	e.mu.Unlock()
	if ev := e.eventTypes(t); ev["lead.intervened"] != 1 || ev["task.continued"] != 1 {
		t.Fatalf("events %v", ev)
	}
}

// Action reassign: the task goes to the named agent (never the reviewer) and the work continues with it.
func TestLeadReassignsTheTask(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{
		agWork: wrongWorker(), agReview: shipper,
		agOther: fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "took over and fixed it"}}},
		agLead:  leadSays(`{"action":"reassign","agent":"Backend #2","message":"Backend #2: take over; the sign in Subtract is wrong.","reason":"Backend #1 repeats the same mistake"}`),
	}, true)
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	tk, _ := e.tasks.Get(context.Background(), e.task.ID)
	if tk.Assignee != agOther || tk.Reviewer != agReview {
		t.Fatalf("assignee %s reviewer %s", tk.Assignee, tk.Reviewer)
	}
	op := e.prompts(agOther)
	if len(op) != 1 || !strings.Contains(op[0], "Guidance from the team lead:\nBackend #2: take over") {
		t.Fatalf("the new worker's prompt %q", op)
	}
	if p := e.pendingOne(t, id); p.NodeID != "approve" {
		t.Fatalf("pending %s", p.NodeID)
	}
	if iv := e.interventions(t); len(iv) != 1 || iv[0].Action != "reassign" || iv[0].TargetAgent != agOther {
		t.Fatalf("interventions %+v", iv)
	}
}

// Action split: BACKLOG follow-ups are created with the lead's text quoted as agent-authored; the current task continues.
func TestLeadSplitsOffFollowUps(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{
		agWork: fixesOn("NARROW"), agReview: shipper,
		agLead: leadSays(`{"action":"split","message":"NARROW: only make Subtract return a - b here.","tasks":[{"title":"Document Subtract","description":"Add a doc comment.","acceptanceCriteria":["Given calc.go, when a reader opens it, then Subtract has a doc comment"]}],"reason":"the documentation is separate work"}`),
	}, true)
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	all, _ := e.tasks.List(context.Background(), tasks.Filter{ProjectID: "p1"})
	var follow *tasks.Task
	for i := range all {
		if all[i].Title == "Document Subtract" {
			follow = &all[i]
		}
	}
	if follow == nil || follow.Status != tasks.Backlog || len(follow.Acceptance) != 1 || !strings.Contains(follow.Description, "written by an agent") || !strings.Contains(follow.Description, "<<<DATA") {
		t.Fatalf("follow-up %+v", follow)
	}
	if wp := e.prompts(agWork); len(wp) != 3 || !strings.Contains(wp[2], "NARROW: only make Subtract") {
		t.Fatalf("the current task continues with the narrowed scope: %q", wp)
	}
	if iv := e.interventions(t); len(iv) != 1 || len(iv[0].CreatedTasks) != 1 || iv[0].CreatedTasks[0] != follow.ID {
		t.Fatalf("interventions %+v", iv)
	}
}

// Validation: an unusable answer gets one retry with the validator's problems, then the decision is an escalation to the
// human, who sees the lead's failure in the question.
func TestLeadInvalidAnswerRetriesOnceThenEscalates(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{
		agWork: wrongWorker(), agReview: shipper,
		agLead: leadSays(`I would nudge the worker. {"action":"nudge","message":"x","reason":"y"}`),
	}, true)
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	lp := e.prompts(agLead)
	if len(lp) != 2 || !strings.Contains(lp[1], "rejected by the validator") || !strings.Contains(lp[1], "not a JSON object") {
		t.Fatalf("lead prompts %d: %q", len(lp), lp)
	}
	p := e.pendingOne(t, id)
	pl := limitPayload(t, p)
	if p.NodeID != pipeline.NodeLimit || pl.Lead == nil || !pl.Lead.Invalid || pl.Lead.Action != "escalate" || !strings.Contains(pl.Lead.Message, "could not be used") {
		t.Fatalf("pending %+v payload %+v", p, pl.Lead)
	}
	if iv := e.interventions(t); len(iv) != 1 || !iv[0].Invalid || iv[0].Action != "escalate" {
		t.Fatalf("interventions %+v", iv)
	}
}

// Action escalate: the human question carries the lead's message.
func TestLeadEscalationReachesTheHuman(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{
		agWork: wrongWorker(), agReview: shipper,
		agLead: leadSays(`{"action":"escalate","message":"The tests and the description disagree about negative numbers; which is right?","reason":"a product decision"}`),
	}, true)
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	pl := limitPayload(t, e.pendingOne(t, id))
	if pl.Lead == nil || pl.Lead.Name != "Lead" || !strings.Contains(pl.Lead.Message, "which is right?") || pl.Lead.Invalid {
		t.Fatalf("payload lead %+v", pl.Lead)
	}
}

// Bounds: at most 3 interventions per task and run, then the human decides (and is told the lead is out of moves).
func TestLeadInterventionsAreBoundedPerRun(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{
		agWork: wrongWorker(), agReview: shipper,
		agLead: leadSays(`{"action":"nudge","message":"Try once more, carefully.","reason":"maybe a typo"}`),
	}, true)
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	if n := len(e.prompts(agLead)); n != 3 {
		t.Fatalf("supervision turns %d, want 3", n)
	}
	pl := limitPayload(t, e.pendingOne(t, id))
	if pl.Lead == nil || !pl.Lead.Exhausted {
		t.Fatalf("the human must be told the lead's bound is used up: %+v", pl.Lead)
	}
	if iv := e.interventions(t); len(iv) != 3 {
		t.Fatalf("interventions %d", len(iv))
	}
	// 1 + 1 loop, then 3 lead continues of 2 turns each
	if n := len(e.prompts(agWork)); n != 8 {
		t.Fatalf("work turns %d", n)
	}
}

// Trigger (c): repeated identical REVISE feedback goes to the lead.
func TestLeadHandlesRepeatedReviewFeedback(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{
		agWork: fakeagent.Script{
			Rules:   []fakeagent.Rule{{Match: "DROPHACK", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "removed it"}}}},
			Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImplHack}}, {Say: "implemented"}},
		},
		agReview: fakeagent.Script{Rules: []fakeagent.Rule{{Match: "HACK", Steps: []fakeagent.Step{{Say: `{"verdict":"REVISE","feedback":"Remove the HACK comment."}`}}}},
			Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}},
		agLead: leadSays(`{"action":"nudge","message":"DROPHACK: delete the line that says HACK; the reviewer wants nothing else.","reason":"the worker ignores the feedback"}`),
	}, true)
	id := e.runIUV(t, 5)
	e.await(t, id, workflow.StatusWaitingHuman)
	if p := e.pendingOne(t, id); p.NodeID != "approve" {
		t.Fatalf("pending %s", p.NodeID)
	}
	iv := e.interventions(t)
	if len(iv) != 1 || iv[0].Trigger != pipeline.TriggerRepeatedRevise || !strings.Contains(iv[0].Evidence, "Remove the HACK comment.") {
		t.Fatalf("interventions %+v", iv)
	}
	if lp := e.prompts(agLead); len(lp) != 1 || !strings.Contains(lp[0], "Review feedback 1") {
		t.Fatalf("the digest shows the repeated feedback: %q", lp)
	}
}

// Trigger (a): a stage-1 watchdog trip on the worker's turn goes to the lead, whose message restarts the work.
func TestLeadHandlesAWatchdogTrip(t *testing.T) {
	spin := []fakeagent.Step{{Tool: "Read", Input: json.RawMessage(`{"file_path":"calc.go"}`), Result: "package gocalc // same every time", Repeat: 3}, {Block: true}}
	e := newWatchedLeadEnv(t, map[string]fakeagent.Script{
		agWork: fakeagent.Script{
			Rules: []fakeagent.Rule{{Match: "Message from the team lead", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "edited instead of re-reading"}}}},
			Turns: [][]fakeagent.Step{spin},
		},
		agReview: shipper,
		agLead:   leadSays(`{"action":"nudge","message":"Stop re-reading calc.go; change Subtract to return a - b.","reason":"tool spin on one read"}`),
	})
	id := e.runIUV(t, 3)
	e.await(t, id, workflow.StatusWaitingHuman)
	if p := e.pendingOne(t, id); p.NodeID != "approve" {
		t.Fatalf("pending %s", p.NodeID)
	}
	iv := e.interventions(t)
	if len(iv) != 1 || iv[0].Trigger != pipeline.TriggerWatchdog || !strings.Contains(iv[0].Evidence, "tool_spin") {
		t.Fatalf("interventions %+v", iv)
	}
	wp := e.prompts(agWork)
	if len(wp) != 2 || !strings.Contains(wp[1], "Message from the team lead") || !strings.Contains(wp[1], "change Subtract to return a - b") {
		t.Fatalf("worker prompts %q", wp)
	}
}

// Trigger (a), the error echo: a check failing the same way on an unchanged worktree goes to the lead at the check; its
// message is the next turn's notice.
func TestLeadHandlesAnErrorEcho(t *testing.T) {
	e := newWatchedLeadEnv(t, map[string]fakeagent.Script{
		agWork: fixesOn("ECHOFIX"), agReview: shipper,
		agLead: leadSays(`{"action":"nudge","message":"ECHOFIX: your edit does not change anything; return a - b.","reason":"error echo"}`),
	})
	id := e.runIUV(t, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if p := e.pendingOne(t, id); p.NodeID != "approve" {
		t.Fatalf("pending %s", p.NodeID)
	}
	iv := e.interventions(t)
	if len(iv) != 1 || iv[0].Trigger != pipeline.TriggerWatchdog || !strings.Contains(iv[0].Evidence, "error_echo") {
		t.Fatalf("interventions %+v", iv)
	}
	if wp := e.prompts(agWork); len(wp) != 3 || !strings.Contains(wp[2], "Message from the team lead") || !strings.Contains(wp[2], "ECHOFIX") {
		t.Fatalf("worker prompts %q", wp)
	}
}

// Trigger (d): a RUNNING task without progress for the stall time (injected clock) is put to the lead once; its escalation
// parks the running turn on a human question.
func TestLeadHandlesAStalledTask(t *testing.T) {
	e := newWatchedLeadEnv(t, map[string]fakeagent.Script{
		agWork:   fakeagent.Script{Default: []fakeagent.Step{{Say: "thinking"}, {Block: true}}},
		agReview: shipper,
		agLead:   leadSays(`{"action":"escalate","message":"The worker has been silent for a long time; please look.","reason":"stalled"}`),
	})
	e.devs.LeadStallMinutes = 15
	id := e.runIUV(t, 3)
	// wait until the worker's turn is under way
	deadline := time.Now().Add(60 * time.Second)
	for len(e.prompts(agWork)) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	tk, _ := e.tasks.Get(context.Background(), e.task.ID)
	if tk.Status != tasks.InProgress || tk.RunID == "" {
		// runIUV does not record the run on the task (the core does): do it here
		rid := string(id)
		e.tasks.Patch(context.Background(), e.task.ID, tasks.Update{RunID: &rid})
	}
	// not stalled yet
	if res, _ := e.lead.Tick(context.Background(), "p1"); len(res.Stalled) != 0 {
		t.Fatalf("not stalled yet: %+v", res)
	}
	e.nowFunc = func() time.Time { return time.Now().Add(20 * time.Minute) }
	res, err := e.lead.Tick(context.Background(), "p1")
	if err != nil || len(res.Stalled) != 1 {
		t.Fatalf("tick %+v %v", res, err)
	}
	iv := e.interventions(t)
	if len(iv) != 1 || iv[0].Trigger != pipeline.TriggerStalled || iv[0].Action != "escalate" || !strings.Contains(iv[0].Evidence, "No progress events for") {
		t.Fatalf("interventions %+v", iv)
	}
	// the escalation went to the running turn through the watchdog (LeadDeps.Steer): the run parks on a human question
	e.await(t, id, workflow.StatusWaitingHuman)
	p := e.pendingOne(t, id)
	if !strings.Contains(p.Spec.Title, "team lead") || !strings.Contains(string(p.Spec.Payload), "silent for a long time") {
		t.Fatalf("pending %+v", p)
	}
	// the same silence is not asked about twice
	if res, _ := e.lead.Tick(context.Background(), "p1"); len(res.Stalled) != 0 {
		t.Fatalf("asked twice: %+v", res)
	}
}

// Trigger (e): with automatic scheduling on, a READY task without a worker is put to the lead, whose reassignment staffs it
// (a reviewer-role agent becomes the reviewer).
func TestLeadStaffsAnUnassignedTask(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{
		agLead: leadSays(`{"action":"reassign","agent":"Backend #2","message":"Backend #2, please take this.","reason":"Backend #2 has nothing open"}`),
	}, true)
	ctx := context.Background()
	nt, _ := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Add Multiply", Description: "Add Multiply."})
	if _, err := e.tasks.Transition(ctx, nt.ID, tasks.Ready, tasks.TransitionOpts{By: "system"}); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.lead.Tick(ctx, "p1"); len(res.Unassigned) != 0 {
		t.Fatalf("autoSchedule is off: %+v", res)
	}
	e.devs.AutoSchedule = true
	res, err := e.lead.Tick(ctx, "p1")
	if err != nil || len(res.Unassigned) != 1 || res.Unassigned[0] != nt.ID {
		t.Fatalf("tick %+v %v", res, err)
	}
	got, _ := e.tasks.Get(ctx, nt.ID)
	if got.Assignee != agOther || got.Reviewer != agReview {
		t.Fatalf("assignee %q reviewer %q", got.Assignee, got.Reviewer)
	}
	if res, _ := e.lead.Tick(ctx, "p1"); len(res.Unassigned) != 0 {
		t.Fatalf("asked twice: %+v", res)
	}
}

// A supervision turn that fails (here the lead's process exits mid-turn) is stored as action failed and counts toward the
// bound: a lead whose turns keep failing is not asked without end. After the bound the trigger site hears it is used up and
// no further turn starts; the trigger site behaves as without a lead every time, and nothing is posted in the room.
func TestAFailedLeadTurnCountsTowardTheBound(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{agLead: {Default: []fakeagent.Step{{Say: "starting"}, {Exit: 3}}}}, true)
	ctx := context.Background()
	tr := pipeline.LeadTrigger{Kind: pipeline.TriggerStalled, TaskID: e.task.ID, RunID: "run_x", Evidence: "No progress events for 20 minutes."}
	for i := 1; i <= pipeline.DefaultLeadMaxPerRun; i++ {
		if o := e.lead.Supervise(ctx, tr); o.Intervened || o.Exhausted {
			t.Fatalf("attempt %d: a failed turn must leave the trigger site as without a lead: %+v", i, o)
		}
	}
	if o := e.lead.Supervise(ctx, tr); !o.Exhausted || o.Intervened {
		t.Fatalf("after %d failed turns the bound must be used up: %+v", pipeline.DefaultLeadMaxPerRun, o)
	}
	if n := len(e.prompts(agLead)); n != pipeline.DefaultLeadMaxPerRun {
		t.Fatalf("lead turns %d, want %d", n, pipeline.DefaultLeadMaxPerRun)
	}
	iv := e.interventions(t)
	if len(iv) != pipeline.DefaultLeadMaxPerRun {
		t.Fatalf("interventions %+v", iv)
	}
	for _, x := range iv {
		if x.Action != pipeline.LeadFailed || x.Trigger != pipeline.TriggerStalled || !strings.Contains(x.Message, "turn failed") || x.Evidence == "" {
			t.Fatalf("a failed turn is stored as %+v", x)
		}
	}
	if e.lead.HasLead(ctx, e.task.ID, "run_x", "work") {
		t.Fatal("HasLead after the bound was used up by failed turns")
	}
	if len(e.posts) != 0 {
		t.Fatalf("a failed turn posted in the room: %v", e.posts)
	}
}

// What the monitor asked survives a restart: a new service (the app started again) asks neither about the same silence
// again (here the lead's turn failed, so the run is still RUNNING and silent) nor about the same unassigned task.
func TestTheMonitorDoesNotAskAgainAfterARestart(t *testing.T) {
	e := newWatchedLeadEnv(t, map[string]fakeagent.Script{
		agWork:   fakeagent.Script{Default: []fakeagent.Step{{Say: "thinking"}, {Block: true}}},
		agReview: shipper,
		agLead:   fakeagent.Script{Default: []fakeagent.Step{{Exit: 3}}},
	})
	ctx := context.Background()
	e.devs.LeadStallMinutes = 15
	id := e.runIUV(t, 3)
	deadline := time.Now().Add(60 * time.Second)
	for len(e.prompts(agWork)) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	rid := string(id)
	e.tasks.Patch(ctx, e.task.ID, tasks.Update{RunID: &rid})
	e.nowFunc = func() time.Time { return time.Now().Add(20 * time.Minute) }
	if res, err := e.lead.Tick(ctx, "p1"); err != nil || len(res.Stalled) != 1 {
		t.Fatalf("tick %+v %v", res, err)
	}
	if n := len(e.prompts(agLead)); n != 1 {
		t.Fatalf("lead turns %d", n)
	}
	restarted := pipeline.NewLead(e.leadDeps)
	if res, _ := restarted.Tick(ctx, "p1"); len(res.Stalled) != 0 {
		t.Fatalf("the same silence was asked about again after a restart: %+v", res)
	}
	if n := len(e.prompts(agLead)); n != 1 {
		t.Fatalf("lead turns after the restart %d", n)
	}
}

// An unassigned task the lead was asked about is not asked about again after a restart, until the task changes.
func TestTheLeadIsAskedOnceAboutAnUnassignedTaskAcrossRestarts(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{agLead: leadSays(`{"action":"continue","message":"","reason":"a human will staff it"}`)}, true)
	ctx := context.Background()
	e.devs.AutoSchedule = true
	nt, _ := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Add Multiply", Description: "Add Multiply."})
	if _, err := e.tasks.Transition(ctx, nt.ID, tasks.Ready, tasks.TransitionOpts{By: "system"}); err != nil {
		t.Fatal(err)
	}
	if res, err := e.lead.Tick(ctx, "p1"); err != nil || len(res.Unassigned) != 1 || res.Unassigned[0] != nt.ID {
		t.Fatalf("tick %+v %v", res, err)
	}
	asked := len(e.prompts(agLead))
	if asked != 1 {
		t.Fatalf("lead turns %d", asked)
	}
	if res, _ := pipeline.NewLead(e.leadDeps).Tick(ctx, "p1"); len(res.Unassigned) != 0 {
		t.Fatalf("asked again after a restart: %+v", res)
	}
	if n := len(e.prompts(agLead)); n != asked {
		t.Fatalf("lead turns %d, want %d", n, asked)
	}
	// a change to the task is a new question
	time.Sleep(5 * time.Millisecond)
	acc := []string{"Given 2 and 3 When Multiply is called Then it returns 6"}
	if _, err := e.tasks.Patch(ctx, nt.ID, tasks.Update{Acceptance: &acc}); err != nil {
		t.Fatal(err)
	}
	if res, _ := pipeline.NewLead(e.leadDeps).Tick(ctx, "p1"); len(res.Unassigned) != 1 || res.Unassigned[0] != nt.ID {
		t.Fatalf("a changed task: %+v", res)
	}
}

// An imported task in BACKLOG waits for a human to move it on: the lead is not asked about it (that is a paid turn for every
// issue an import rule brings in); once a human moves it to READY it is an ordinary unassigned task.
func TestTheLeadIsNotAskedAboutImportedWorkBeforeAHumanMovesItOn(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{agLead: leadSays(`{"action":"continue","message":"","reason":"a human will staff it"}`)}, true)
	ctx := context.Background()
	e.devs.AutoSchedule = true
	imp, _ := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "ENG-7: from Linear", Description: "quoted", IssueRef: "ENG-7"})
	if res, err := e.lead.Tick(ctx, "p1"); err != nil || len(res.Unassigned) != 0 || len(e.prompts(agLead)) != 0 {
		t.Fatalf("tick %+v %v, lead turns %d", res, err, len(e.prompts(agLead)))
	}
	if _, err := e.tasks.Transition(ctx, imp.ID, tasks.Ready, tasks.TransitionOpts{By: "human:k"}); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.lead.Tick(ctx, "p1"); len(res.Unassigned) != 1 || res.Unassigned[0] != imp.ID {
		t.Fatalf("after a human moved it on: %+v", res)
	}
}

// No lead on the team: nothing changes (no supervision turn, the human is asked as before, the monitor does nothing).
func TestWithoutALeadNothingChanges(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{agWork: wrongWorker(), agReview: shipper}, false)
	e.devs.AutoSchedule = true
	id := e.runIUV(t, 1)
	e.await(t, id, workflow.StatusWaitingHuman)
	if pl := limitPayload(t, e.pendingOne(t, id)); pl.Lead != nil {
		t.Fatalf("no lead, no lead view: %+v", pl.Lead)
	}
	if o := e.lead.Supervise(context.Background(), pipeline.LeadTrigger{Kind: pipeline.TriggerIterationLimit, TaskID: e.task.ID}); o.Intervened || o.Exhausted {
		t.Fatalf("outcome %+v", o)
	}
	e.nowFunc = func() time.Time { return time.Now().Add(time.Hour) }
	if res, _ := e.lead.Tick(context.Background(), "p1"); len(res.Stalled)+len(res.Unassigned) != 0 {
		t.Fatalf("tick %+v", res)
	}
	if len(e.interventions(t)) != 0 {
		t.Fatal("no interventions without a lead")
	}
}

// The lead's own turns are never triggers: a task the lead works on is not supervised by it.
func TestTheLeadsOwnTurnsAreExcluded(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{}, true)
	ctx := context.Background()
	if !e.lead.HasLead(ctx, e.task.ID, "run1", "work") {
		t.Fatal("the lead supervises the worker's turn")
	}
	l := agLead
	e.tasks.Patch(ctx, e.task.ID, tasks.Update{Assignee: &l})
	if e.lead.HasLead(ctx, e.task.ID, "run1", "work") {
		t.Fatal("the lead's own turn must not trigger it")
	}
	if o := e.lead.Supervise(ctx, pipeline.LeadTrigger{Kind: pipeline.TriggerWatchdog, TaskID: e.task.ID, NodeAgent: "work"}); o.Intervened {
		t.Fatal("no supervision of the lead's own turn")
	}
}
