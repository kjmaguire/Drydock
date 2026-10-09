package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Template tests use scripted stand-in agents (the labeled fakeagent: a real process speaking Claude's dialect) so each
// role in a template gets its own deterministic behaviour. Everything else is real: git, go test, SQLite, the engine.

const greenCalc = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// Subtract returns a minus b.\nfunc Subtract(a, b int) int { return a - b }\n"
const calcWithMul = greenCalc + "\n// Multiply returns a times b.\nfunc Multiply(a, b int) int { return a * b }\n"

const mulTests = `package gocalc

import "testing"

// AC1: Given 3 and 4, when Multiply is called, then it returns 12
func TestAC1_Multiply(t *testing.T) {
	if got := Multiply(3, 4); got != 12 {
		t.Fatalf("Multiply(3,4) = %d, want 12", got)
	}
}

// AC2: Given 0 and 9, when Multiply is called, then it returns 0
func TestAC2_MultiplyByZero(t *testing.T) {
	if got := Multiply(0, 9); got != 0 {
		t.Fatalf("Multiply(0,9) = %d, want 0", got)
	}
}
`

const mulSpec = `# Multiply
## Problem
Callers of the gocalc package cannot multiply two integers.
## Scope and non-goals
Integers only; no overflow handling.
## Acceptance criteria
- Given 3 and 4, when Multiply is called, then it returns 12
- Given 0 and 9, when Multiply is called, then it returns 0
## Open questions
none`

type agentLog struct{ script, prompts string }

type tpl struct {
	t     *testing.T
	dir   string
	repo  *git.Repo
	db    *storage.DB
	bus   *events.SQLBus
	tasks *tasks.Service
	eng   *workflow.Engine
	td    pipeline.TemplateDeps
	task  tasks.Task
	logs  map[string]agentLog // by agent id
	roles map[string]string   // template role -> agent id
	dev   pipeline.DevSettings
	// previews are the running previews started through the template's preview node; they are stopped at cleanup
	previews []*checks.PreviewHandle
}

// tplOpts configures a template environment.
type tplOpts struct {
	files    map[string]string // extra files committed into the base repository
	title    string
	desc     string
	accept   []string
	template string
	preview  bool // install the real preview runner (checks.StartPreview) and keep the handles
	// pr registers the templates' pull request variants against this GitHub protocol fixture and a local bare remote
	pr *githubFixture
}

const (
	aWork   = "w1"
	aReview = "r1"
	aPM     = "pm1"
	aQA     = "qa1"
	aArch   = "arch1"
	aSec    = "sec1"
)

// newTpl builds the repo (the gocalc fixture with Subtract implemented, so the baseline is green), the database, the engine
// and the template dependencies, with one scripted agent per entry in scripts (keyed by agent id).
func newTpl(t *testing.T, scripts map[string]fakeagent.Script, o tplOpts) *tpl {
	t.Helper()
	ctx := context.Background()
	e := &tpl{t: t, dir: t.TempDir(), logs: map[string]agentLog{}, roles: map[string]string{}, dev: pipeline.DefaultDevSettings()}
	repoDir := filepath.Join(e.dir, "repo")
	os.MkdirAll(repoDir, 0o755)
	copyDir(t, filepath.Join("..", "..", "testdata", "fixtures", "gocalc"), repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "calc.go"), []byte(greenCalc), 0o644); err != nil {
		t.Fatal(err)
	}
	for p, body := range o.files {
		full := filepath.Join(repoDir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	for id, sc := range scripts {
		b, _ := json.Marshal(sc)
		sp := filepath.Join(e.dir, id+".json")
		os.WriteFile(sp, b, 0o644)
		e.logs[id] = agentLog{sp, filepath.Join(e.dir, id+".prompts")}
		db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES(?,?,?,?,?,1)`, id, "p1", id, "claude", map[string]string{aWork: "backend", aReview: "reviewer", aPM: "pm", aQA: "qa", aArch: "architect", aSec: "security"}[id])
	}
	e.tasks = &tasks.Service{DB: db, Bus: e.bus}
	ce := &ctxeng.Engine{DB: db, Memory: memory.Store{DB: db}}
	ad := &claude.Adapter{Exe: os.Args[0]}
	runtime := &rt.Runtime{PM: proc.New(), Bus: e.bus, DB: db, Adapters: map[string]agents.Adapter{"claude": ad}, NudgeWait: 3 * time.Second}
	cfgFor := func(id string) rt.Config {
		l := e.logs[id]
		return rt.Config{AgentID: id, Adapter: "claude", Provider: "fake", Tier: "premium",
			Spec: agents.RunSpec{AgentID: id, Model: "fake-1", PermissionMode: "acceptEdits", Env: []string{"DRYDOCK_FAKE_SCRIPT=" + l.script, "DRYDOCK_FAKE_PROMPT_LOG=" + l.prompts}}}
	}
	deps := pipeline.Deps{
		Tasks: e.tasks, Runtime: runtime, PM: proc.New(), Bus: e.bus, Context: ce,
		Repo:         func(context.Context, string) (*git.Repo, error) { return repo, nil },
		WorktreeRoot: filepath.Join(e.dir, "worktrees"), DataDir: filepath.Join(e.dir, "data"),
		AgentConfig: func(tk tasks.Task, role string) rt.Config {
			if role == "review" {
				return cfgFor(tk.Reviewer)
			}
			return cfgFor(tk.Assignee)
		},
	}
	e.td = pipeline.TemplateDeps{
		Deps:    deps,
		Project: projectInfo,
		RoleConfig: func(ctx context.Context, tk tasks.Task, role, agentID string) (rt.Config, error) {
			if agentID == "" {
				return rt.Config{}, fmt.Errorf("no agent pinned for role %s", role)
			}
			return cfgFor(agentID), nil
		},
		Dev:      func(context.Context, string) pipeline.DevSettings { return e.dev },
		MaxLoops: 3,
	}
	if o.preview {
		e.td.Preview = func(ctx context.Context, tk tasks.Task, dev pipeline.DevSettings) (checks.PreviewResult, error) {
			h, res, err := checks.StartPreview(ctx, proc.New(), tk.Worktree, checks.PreviewOptions{Enabled: dev.Preview.Enabled, Command: dev.Preview.Command,
				HealthURL: dev.Preview.URL, Timeout: 30 * time.Second, LogDir: filepath.Join(e.dir, "data", "previews")}, nil)
			if h != nil {
				e.previews = append(e.previews, h)
			}
			return res, err
		}
		t.Cleanup(func() {
			for _, h := range e.previews {
				h.Stop()
			}
		})
	}
	if o.pr != nil {
		pe := &env{dir: e.dir, repo: repo}
		e.td.PR, _ = pe.prSetup(t, o.pr)
	}
	e.eng = workflow.New(workflow.Config{DB: db, Bus: e.bus, DataDir: filepath.Join(e.dir, "data"), LeaseTTL: 3 * time.Second, SafePoint: pipeline.SafePointHook(deps)})
	if err := pipeline.RegisterTemplates(e.eng, e.td); err != nil {
		t.Fatal(err)
	}
	title := o.title
	if title == "" {
		title = "Add Multiply"
	}
	task, err := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: title, Description: orDefault(o.desc, "Add a Multiply function to the gocalc package."), Acceptance: o.accept, Template: o.template})
	if err != nil {
		t.Fatal(err)
	}
	w, rv := aWork, aReview
	e.task, _ = e.tasks.Patch(ctx, task.ID, tasks.Update{Assignee: &w, Reviewer: &rv})
	return e
}

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}

func (e *tpl) prompts(agent string) []string { return prompts(e.logs[agent].prompts) }

func (e *tpl) start(name string, roles map[string]string, scans ...string) workflow.RunID {
	e.t.Helper()
	id, err := e.eng.Start(context.Background(), name, pipeline.TemplateInput{TaskID: e.task.ID, Roles: roles, Scans: scans}, workflow.StartOptions{ProjectID: "p1", TaskID: e.task.ID})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *tpl) await(id workflow.RunID, want workflow.RunStatus) workflow.RunInfo {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	info, err := e.eng.Await(ctx, id, func(r workflow.RunInfo) bool { return r.Status == want || r.Status.Terminal() })
	if err != nil {
		e.t.Fatalf("await %s: %v", want, err)
	}
	if info.Status != want {
		e.t.Fatalf("run ended %s (%s), wanted %s", info.Status, info.FailedReason, want)
	}
	return info
}

func (e *tpl) pending(id workflow.RunID) workflow.PendingRequest {
	e.t.Helper()
	time.Sleep(60 * time.Millisecond) // the parked driver releases its lease
	p, err := e.eng.Pending(context.Background(), id)
	if err != nil || len(p) != 1 {
		e.t.Fatalf("expected one pending request, got %+v (%v)", p, err)
	}
	return p[0]
}

func (e *tpl) respond(id workflow.RunID, decision, by, data string) error {
	e.t.Helper()
	p := e.pending(id)
	var raw json.RawMessage
	if data != "" {
		raw = json.RawMessage(data)
	}
	return e.eng.Resume(context.Background(), id, workflow.Response{RequestID: p.ID, Decision: decision, By: by, Data: raw})
}

func (e *tpl) evidence() map[string][]tasks.Evidence {
	e.t.Helper()
	ev, err := e.tasks.Evidence(context.Background(), e.task.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string][]tasks.Evidence{}
	for _, x := range ev {
		out[x.Kind] = append(out[x.Kind], x)
	}
	return out
}

func (e *tpl) get() tasks.Task {
	e.t.Helper()
	tk, err := e.tasks.Get(context.Background(), e.task.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return tk
}

func writeStep(path, content string) fakeagent.Step {
	return fakeagent.Step{Write: &fakeagent.WriteSpec{Path: path, Content: content}}
}

func say(s string) fakeagent.Step { return fakeagent.Step{Say: s} }

func shipReviewer() fakeagent.Script {
	return fakeagent.Script{Default: []fakeagent.Step{say(`{"verdict":"SHIP","feedback":""}`)}}
}

var _ = checks.Detect
var _ = strings.Contains

func gitWT(t tasks.Task) git.Worktree {
	return git.Worktree{Path: t.Worktree, Branch: t.Branch, Base: t.BaseBranch, TaskID: t.ID}
}
