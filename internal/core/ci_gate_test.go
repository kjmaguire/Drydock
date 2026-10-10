package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The CI gate's project setting (docs/github.md section 5): required unless a human declares that the repository has no
// CI; only a human: identity can change it; the declaration records who made it and when; an import puts it back to
// required.
func TestCIGateSettingIsRequiredByDefaultHumanOnlyAndResetByAnImport(t *testing.T) {
	a := open(t, "")
	ctx := context.Background()
	p := gitProject(t, a, "gocalc")
	var s pipeline.DevSettings
	a.call("projectdev.get", map[string]string{"projectId": p.ID}, &s)
	if s.CIMode != pipeline.CIRequired || s.CINoneBy != "" || s.CINoneAt != 0 {
		t.Fatalf("default %+v", s)
	}

	// only a human identity can declare "no CI"
	none := s
	none.CIMode = pipeline.CINone
	if err := pipeline.SaveDevSettings(ctx, a.c.DB, p.ID, none, "agent:a1"); err == nil {
		t.Fatal("an agent identity changed the CI gate")
	}
	if got := pipeline.LoadDevSettings(ctx, a.c.DB, p.ID); got.CIMode != pipeline.CIRequired {
		t.Fatalf("after the refused change: %+v", got)
	}
	if err := a.callErr("projectdev.set", map[string]any{"projectId": p.ID, "settings": map[string]any{"maxParallel": 2, "respond": true, "ciMode": "sometimes", "e2e": map[string]any{}, "preview": map[string]any{}}}); err == nil || !strings.Contains(err.Error(), "ciMode") {
		t.Fatalf("an unknown CI mode must be refused: %v", err)
	}

	// the declaration records the human who made it and when, never what the caller sent
	before := time.Now().UnixMilli()
	none.CINoneBy, none.CINoneAt = "human:someone-else", 1
	a.call("projectdev.set", map[string]any{"projectId": p.ID, "settings": none}, &s)
	if s.CIMode != pipeline.CINone || s.CINoneBy != "human:kyle" || s.CINoneAt < before || s.CINoneAt > time.Now().UnixMilli() {
		t.Fatalf("declared %+v", s)
	}
	declared := s
	// saving other settings later keeps the original record
	s.MaxParallel, s.CINoneBy, s.CINoneAt = 3, "", 0
	a.call("projectdev.set", map[string]any{"projectId": p.ID, "settings": s}, &s)
	if s.MaxParallel != 3 || s.CINoneBy != declared.CINoneBy || s.CINoneAt != declared.CINoneAt {
		t.Fatalf("a later save changed the declaration: %+v (was %+v)", s, declared)
	}

	// an import puts the gate back to required: a file cannot declare that a repository has no CI
	dest := filepath.Join(t.TempDir(), "gocalc.drydock-project.zip")
	a.call("project.export", map[string]any{"projectId": p.ID, "dest": dest}, nil)
	var res struct {
		ProjectID string `json:"projectId"`
	}
	a.call("project.import", map[string]any{"path": dest, "repoPath": newRepo(t), "name": "gocalc (copy)"}, &res)
	var imported pipeline.DevSettings
	a.call("projectdev.get", map[string]string{"projectId": res.ProjectID}, &imported)
	if imported.CIMode != pipeline.CIRequired || imported.CINoneBy != "" || imported.CINoneAt != 0 || imported.MaxParallel != 3 {
		t.Fatalf("imported %+v", imported)
	}
	if got := pipeline.LoadDevSettings(ctx, a.c.DB, p.ID); got.CIMode != pipeline.CINone {
		t.Fatalf("the original project was changed by the import: %+v", got)
	}

	// back to required: the declaration is gone
	s.CIMode = pipeline.CIRequired
	var back pipeline.DevSettings
	a.call("projectdev.set", map[string]any{"projectId": p.ID, "settings": s}, &back)
	if back.CIMode != pipeline.CIRequired || back.CINoneBy != "" || back.CINoneAt != 0 {
		t.Fatalf("required again %+v", back)
	}
}

// gateGitHub is a LABELED PROTOCOL FIXTURE of GitHub's side of one pull request on which no CI ever reports. The core under
// test is real (engine, the watchdog's middleware, settings, the pull-request nodes); the HTTP peer is the fixture because
// this sandbox cannot create real pull requests. git is real: a bare repository is the remote.
type gateGitHub struct {
	mu     sync.Mutex
	bare   string
	branch string
	merges int
}

func (g *gateGitHub) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		p := strings.TrimPrefix(r.URL.Path, "/repos/o/r")
		pr := func() map[string]any {
			out, _ := exec.Command("git", "-C", g.bare, "rev-parse", "refs/heads/"+g.branch).Output()
			return map[string]any{"number": 7, "html_url": "https://github.test/o/r/pull/7", "state": "open", "merged": g.merges > 0, "mergeable": true, "mergeable_state": "clean",
				"head": map[string]any{"ref": g.branch, "sha": strings.TrimSpace(string(out))}, "base": map[string]any{"ref": "main"}}
		}
		switch {
		case r.Method == "POST" && p == "/pulls":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			g.branch, _ = b["head"].(string)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(pr())
		case r.Method == "GET" && p == "/pulls/7":
			json.NewEncoder(w).Encode(pr())
		case strings.HasSuffix(p, "/check-runs"):
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		case strings.HasSuffix(p, "/status"):
			fmt.Fprint(w, `{"state":"pending","statuses":[]}`)
		case p == "/branches/main":
			fmt.Fprint(w, `{"name":"main","protected":false}`)
		case p == "/rules/branches/main", p == "/pulls/7/reviews", p == "/pulls/7/comments", p == "/issues/7/comments":
			fmt.Fprint(w, `[]`)
		case r.Method == "PUT" && p == "/pulls/7/merge":
			g.merges++
			fmt.Fprint(w, `{"sha":"mergedsha","merged":true}`)
		case r.Method == "DELETE" && strings.HasPrefix(p, "/git/refs/heads/"):
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected GitHub call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// In the core the CI node is a check node behind the watchdog's middleware, which takes answers delivered into a check
// node's inbox as answers to its own pauses. The CI question is therefore answered through its approvals row: a human's
// waiver sent with run.respond, as the task page sends it, reaches the CI node and passes it; the merge follows.
func TestCIGateQuestionIsAnsweredThroughTheCore(t *testing.T) {
	dir := t.TempDir()
	script, _ := json.Marshal(fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	})
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, script, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)

	gh := &gateGitHub{bare: filepath.Join(dir, "origin.git")}
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", gh.bare).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	srv := gh.serve(t)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		GitHub: func(ctx context.Context, p core.Project) (pipeline.PRConfig, error) {
			return pipeline.PRConfig{GitHub: host.GitHub{API: host.HTTPAPI{BaseURL: srv.URL}, Owner: "o", Repo: "r"}, BaseBranch: p.DefaultBranch,
				MergeStrategy: host.MergeSquash, DeleteBranch: true, CIPoll: 20 * time.Millisecond, CIGrace: 200 * time.Millisecond, MergeTries: 2, MergePoll: 10 * time.Millisecond}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	repo := newRepo(t)
	gitIn(t, repo, "remote", "add", "origin", gh.bare)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	a.call("project.update", map[string]any{"id": p.ID, "mode": "pr_flow", "mergeStrategy": "squash"}, &p)
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)

	pending := func(node string) core.TaskDetail {
		var d core.TaskDetail
		a.waitFor("a request at "+node, func() bool {
			a.call("task.get", map[string]string{"id": task.ID}, &d)
			return len(d.Pending) == 1 && string(d.Pending[0].NodeID) == node
		})
		return d
	}
	d := pending("ci")
	if q := d.Pending[0]; q.Spec.Kind != "question" || !strings.HasPrefix(q.Spec.Title, "No CI ran on pull request #7 within") {
		t.Fatalf("question %+v", q)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]bool{"waive_ci": true}}, nil)
	d = pending("approve")
	waived := false
	for _, e := range d.Evidence {
		waived = waived || e.Kind == "ci" && e.Passed != nil && *e.Passed && e.Summary == "CI waived by human:kyle for this commit: no CI reported on it"
	}
	if !waived {
		t.Fatalf("the waiver did not reach the CI node: %+v", d.Evidence)
	}
	a.call("run.respond", map[string]any{"runId": runID, "requestId": d.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("the merge", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return d.Task.Status == tasks.Completed
	})
	gh.mu.Lock()
	defer gh.mu.Unlock()
	if gh.merges != 1 {
		t.Fatalf("merge calls: %d", gh.merges)
	}
}
