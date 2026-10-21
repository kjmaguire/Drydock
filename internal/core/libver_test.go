package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// shopManifests is a repository that pins the Stripe and Linear SDKs through its lockfile and uses a library that is not on
// the curated list.
func shopManifests(t *testing.T, repo string) {
	t.Helper()
	files := map[string]string{
		"package.json": `{"name":"shop","dependencies":{"stripe":"^15.0.0","@linear/sdk":"^58.0.0","left-pad":"^1.3.0"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"name":"shop"},"node_modules/stripe":{"version":"15.4.0"},` +
			`"node_modules/@linear/sdk":{"version":"58.1.0"},"node_modules/left-pad":{"version":"1.3.0"}}}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "dependencies")
}

// Decision P54: a worker's AGENTS.md names the SDK versions the worktree's lockfile pins and tells it to ask documentation for
// them; the instruction writer's manifest records the section and where it came from; the generated file stays out of the diff.
func TestAWorkersInstructionsNameTheSDKVersionsTheRepositoryPins(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	shopManifests(t, repo)
	var p core.Project
	a.call("project.create", map[string]any{"name": "shop", "repoPath": repo}, &p)
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b."}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	agents, err := os.ReadFile(filepath.Join(detail.Task.Worktree, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(agents)
	for _, want := range []string{"## Library versions", "- `package.json` (exact versions from `package-lock.json`): @linear/sdk 58.1.0, stripe 15.4.0",
		"ask for this version, not the latest"} {
		if !strings.Contains(body, want) {
			t.Fatalf("AGENTS.md lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "left-pad") {
		t.Fatalf("a library off the curated list was listed:\n%s", body)
	}
	// the instruction writer records the section and the files it came from
	_, m, err := a.c.Context.Write(context.Background(), p.ID, detail.Task.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range m.Items {
		if it.Source == "libver:package.json,package-lock.json" && it.Included && it.Bytes > 0 && strings.Contains(it.Reason, "2 SDK and framework versions") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the context manifest does not record the section: %+v", m.Items)
	}
	var diff string
	a.call("task.diff", map[string]string{"id": task.ID}, &diff)
	if strings.Contains(diff, "AGENTS.md") {
		t.Fatalf("the generated file reached the diff:\n%s", diff)
	}
}

// A repository with no manifest gets no section, and nothing is recorded for it.
func TestARepositoryWithoutManifestsGetsNoLibrarySection(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "plain", "repoPath": newRepo(t)}, &p)
	wt := t.TempDir()
	_, m, err := a.c.Context.Write(context.Background(), p.ID, wt)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(wt, "AGENTS.md")); err != nil || strings.Contains(string(b), "Library versions") {
		t.Fatalf("%v\n%s", err, b)
	}
	for _, it := range m.Items {
		if strings.HasPrefix(it.Source, "libver:") {
			t.Fatalf("%+v", it)
		}
	}
}

// The lead's recommendation turn is told which SDKs the repository uses, so it can recommend the matching documentation
// server or vendor skill.
func TestTheRecommendationPromptNamesTheRepositorysSDKs(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	shopManifests(t, repo)
	var p core.Project
	a.call("project.create", map[string]any{"name": "shop", "repoPath": repo}, &p)
	var lead core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "fake-1"}, &lead)
	prompt, err := a.c.TestRecommendPrompt(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Libraries the repository uses: package.json: @linear/sdk 58.1.0, stripe 15.4.0") ||
		!strings.Contains(prompt, "documentation server or skills are available below") {
		t.Fatalf("%s", prompt)
	}
}
