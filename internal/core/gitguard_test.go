package core_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// A task's agent changes the repository's shared git configuration while it works (here: a filter driver, the kind of plant
// that would run in Drydock's own commit). The merge does not run git under it: the run asks the human, names the changed
// key and never its value, and only after the human approves does the merge go through. The trusted state is kept across a
// restart, and project.gitConfig / project.trustGitConfig show and trust it outside a run.
func TestARepositoryConfigChangeDuringATaskWaitsForTheHuman(t *testing.T) {
	dir := t.TempDir()
	a := open(t, dir)
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the review request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1 && detail.Pending[0].Spec.Kind == "review"
	})

	plant := exec.Command("git", "config", "filter.planted.clean", "echo secret-value-xyz; cat")
	plant.Dir = detail.Task.Worktree
	if out, err := plant.CombinedOutput(); err != nil {
		t.Fatalf("plant: %v %s", err, out)
	}
	var gc core.GitConfig
	a.call("project.gitConfig", map[string]string{"id": p.ID}, &gc)
	if gc.Trusted || !slices.Contains(gc.Changed, "filter.planted.clean") {
		t.Fatalf("gitConfig after the plant: %+v", gc)
	}

	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("the configuration approval", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1 && detail.Pending[0].Spec.Kind == "approval"
	})
	req := detail.Pending[0]
	var payload struct {
		Gate    string   `json:"gate"`
		Changed []string `json:"changed"`
	}
	_ = json.Unmarshal(req.Spec.Payload, &payload)
	if payload.Gate != "repo_config_changed" || !slices.Contains(payload.Changed, "filter.planted.clean") {
		t.Fatalf("approval payload %s", req.Spec.Payload)
	}
	if strings.Contains(string(req.Spec.Payload)+req.Spec.Title, "secret-value-xyz") {
		t.Fatal("the approval shows the configured value")
	}
	if detail.Task.Status == tasks.Completed {
		t.Fatal("the merge ran under an untrusted configuration")
	}

	// a restart keeps the trusted state: the change is still untrusted after the core opens the repository again
	a.c.Close()
	a = open(t, dir)
	a.call("project.gitConfig", map[string]string{"id": p.ID}, &gc)
	if gc.Trusted {
		t.Fatal("a restart trusted the planted configuration")
	}

	a.call("run.respond", map[string]any{"runId": runID, "requestId": req.ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion after the human trusted the change", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})
	a.call("project.gitConfig", map[string]string{"id": p.ID}, &gc)
	if !gc.Trusted {
		t.Fatalf("after the approval: %+v", gc)
	}

	// outside a run: a further change is shown and trusted by fingerprint; a stale fingerprint is refused
	more := exec.Command("git", "config", "core.sshCommand", "ssh -i elsewhere")
	more.Dir = repo
	if out, err := more.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	a.call("project.gitConfig", map[string]string{"id": p.ID}, &gc)
	if _, err := a.c.Call(context.Background(), "project.trustGitConfig", mustJSON(map[string]string{"id": p.ID, "fingerprint": "stale"})); err == nil {
		t.Fatal("a stale fingerprint was trusted")
	}
	a.call("project.trustGitConfig", map[string]string{"id": p.ID, "fingerprint": gc.Fingerprint}, &gc)
	if !gc.Trusted {
		t.Fatalf("after trustGitConfig: %+v", gc)
	}
}

// Two starts of one task at the same moment (a double click, the scheduler racing the person) start one run, not two runs
// on one worktree.
func TestTwoStartsOfOneTaskStartOneRun(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make it return a - b"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	const n = 6
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := a.c.Call(context.Background(), "task.start", mustJSON(map[string]string{"id": task.ID}))
			errs <- err
		}()
	}
	started := 0
	for i := 0; i < n; i++ {
		if err := <-errs; err == nil {
			started++
		} else if !strings.Contains(err.Error(), "already has an active run") {
			t.Fatalf("unexpected refusal: %v", err)
		}
	}
	var runs int
	if err := a.c.DB.Reader().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM workflow_runs WHERE task_id=?`, task.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if started != 1 || runs != 1 {
		t.Fatalf("%d starts succeeded and %d runs exist for one task", started, runs)
	}
}

// The trusted configuration is the one the repository had when the person gave it to the project (created with it or
// attached it), not the one it has when Drydock first uses it: a setting planted in between is shown, never taken as trusted.
func TestASettingPlantedBeforeTheFirstUseIsNotTrusted(t *testing.T) {
	a := open(t, "")
	plant := func(repo string) {
		t.Helper()
		if out, err := exec.Command("git", "-C", repo, "config", "filter.evil.clean", "cat").CombinedOutput(); err != nil {
			t.Fatalf("plant: %v %s", err, out)
		}
	}
	created, attached := newRepo(t), newRepo(t)
	var p, q core.Project
	a.call("project.create", map[string]any{"name": "created", "repoPath": created}, &p)
	a.call("project.create", map[string]any{"name": "attached"}, &q)
	a.call("project.attachRepo", map[string]any{"id": q.ID, "repoPath": attached}, &q)
	plant(created)
	plant(attached)
	for _, id := range []string{p.ID, q.ID} {
		var gc core.GitConfig
		a.call("project.gitConfig", map[string]string{"id": id}, &gc)
		if gc.Trusted || !slices.Contains(gc.Changed, "filter.evil.clean") {
			t.Fatalf("%s: a setting planted before the first use was trusted: %+v", id, gc)
		}
	}
}
