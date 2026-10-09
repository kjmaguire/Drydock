package core_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// A project can be created without a repository: the team, room and tasks exist, nothing that changes code starts, and the
// repository is attached later. Once attached it cannot be swapped.
func TestProjectWithoutRepositoryThenAttach(t *testing.T) {
	a := open(t, "")
	ctx := context.Background()
	if _, err := a.c.Call(ctx, "project.create", mustJSON(map[string]any{"name": "  ", "repoPath": ""})); err == nil || !strings.Contains(err.Error(), "needs a name") {
		t.Fatalf("a project with neither a name nor a repository must be refused: %v", err)
	}
	var p core.Project
	a.call("project.create", map[string]any{"name": "Idea board"}, &p)
	if p.HasRepo || p.RepoPath != "" || p.DefaultBranch != "" {
		t.Fatalf("a project without a repository: %+v", p)
	}
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
	if len(rooms) == 0 {
		t.Fatal("default channels must exist without a repository")
	}
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Wren", Adapter: "claude", Role: "backend"}, nil)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Ivy", Adapter: "claude", Role: "reviewer"}, nil)
	var agents []core.Agent
	a.call("agent.list", map[string]any{"projectId": p.ID}, &agents)
	var tk tasks.Task
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Add Subtract"}, &tk)
	a.call("task.assign", map[string]any{"id": tk.ID, "assignee": agents[0].ID, "reviewer": agents[1].ID}, nil)
	if _, err := a.c.Call(ctx, "task.start", mustJSON(map[string]any{"id": tk.ID})); err == nil || !strings.Contains(err.Error(), "no repository yet") {
		t.Fatalf("starting a task without a repository must say to attach one: %v", err)
	}
	if _, err := a.c.Call(ctx, "task.startTemplate", mustJSON(map[string]any{"taskId": tk.ID, "template": "bug"})); err == nil || !strings.Contains(err.Error(), "no repository yet") {
		t.Fatalf("starting a template without a repository must say to attach one: %v", err)
	}
	if _, err := a.c.Call(ctx, "project.update", mustJSON(map[string]any{"id": p.ID, "mode": "pr_flow"})); err == nil {
		t.Fatal("pr_flow needs a repository")
	}
	var scan map[string]any
	a.call("skill.scan", map[string]any{"projectId": p.ID}, &scan)

	// the terminal and room replies use a private folder in the data directory, never the app's working directory
	var term struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	}
	if res, err := a.c.Call(ctx, "terminal.open", mustJSON(map[string]any{"projectId": p.ID})); err == nil {
		_ = json.Unmarshal(res, &term)
		if !strings.Contains(term.Cwd, filepath.Join("projects", p.ID, "workspace")) {
			t.Fatalf("terminal cwd for a project without a repository: %q", term.Cwd)
		}
		a.c.Call(ctx, "terminal.close", mustJSON(map[string]any{"id": term.ID}))
	}

	if _, err := a.c.Call(ctx, "project.attachRepo", mustJSON(map[string]any{"id": p.ID, "repoPath": t.TempDir()})); err == nil || !strings.Contains(err.Error(), "not a usable Git repository") {
		t.Fatalf("a folder that is not a repository must be refused: %v", err)
	}
	repo := newRepo(t)
	a.call("project.attachRepo", map[string]any{"id": p.ID, "repoPath": repo}, &p)
	if !p.HasRepo || p.DefaultBranch != "main" || p.Name != "Idea board" {
		t.Fatalf("after attaching: %+v", p)
	}
	if _, err := a.c.Call(ctx, "project.attachRepo", mustJSON(map[string]any{"id": p.ID, "repoPath": newRepo(t)})); err == nil || !strings.Contains(err.Error(), "cannot be swapped") {
		t.Fatalf("a second repository must be refused: %v", err)
	}
	var run string
	a.call("task.start", map[string]any{"id": tk.ID}, &run)
	if run == "" {
		t.Fatal("the task starts once the repository is attached")
	}
	a.waitFor("the task to reach the human approval", func() bool {
		var got core.TaskDetail
		a.call("task.get", map[string]any{"id": tk.ID}, &got)
		return len(got.Pending) == 1
	})
}

// The brief is stored redacted and bounded, and survives in project.get.
func TestProjectBrief(t *testing.T) {
	a := open(t, "")
	ctx := context.Background()
	var p core.Project
	a.call("project.create", map[string]any{"name": "Shop"}, &p)
	secret := "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "aB3dE5"
	a.call("project.setBrief", map[string]any{"id": p.ID, "brief": "  A storefront on Workers. Deploy with " + secret + "  "}, &p)
	if strings.Contains(p.Brief, secret) || !strings.HasPrefix(p.Brief, "A storefront on Workers.") {
		t.Fatalf("brief: %q", p.Brief)
	}
	if _, err := a.c.Call(ctx, "project.setBrief", mustJSON(map[string]any{"id": p.ID, "brief": strings.Repeat("x", core.MaxBriefLen+1)})); err == nil {
		t.Fatal("an over-long brief must be refused")
	}
	var got core.Project
	a.call("project.get", map[string]any{"id": p.ID}, &got)
	if got.Brief != p.Brief {
		t.Fatalf("project.get brief %q", got.Brief)
	}
	if a.c.ProjectBrief(ctx, p.ID) != p.Brief || a.c.ProjectBrief(ctx, "prj_nope") != "" {
		t.Fatal("ProjectBrief")
	}
}

// A project without a repository can still be planned: the planner reads in the project's private folder, and the accepted
// tasks wait in the backlog until a repository is attached.
func TestPlanningWorksWithoutARepository(t *testing.T) {
	a, _ := openDev(t, devScript())
	var p core.Project
	a.call("project.create", map[string]any{"name": "Someday"}, &p)
	a.agent(p.ID, "Priya", "pm")
	a.agent(p.ID, "Dana", "backend")
	a.agent(p.ID, "Rex", "reviewer")
	a.call("project.setBrief", map[string]any{"id": p.ID, "brief": "A calculator library in Go."}, nil)
	var prop map[string]any
	a.call("plan.propose", map[string]any{"projectId": p.ID, "goal": "Add Multiply and then Divide to gocalc"}, &prop)
	planID := prop["planId"].(string)
	var plan pipeline.Plan
	a.waitFor("the proposal", func() bool {
		a.call("plan.get", map[string]string{"planId": planID}, &plan)
		return plan.Status == "PROPOSED" || plan.Status == "FAILED"
	})
	if plan.Status != "PROPOSED" {
		t.Fatalf("planning without a repository: %+v", plan)
	}
	a.call("plan.accept", map[string]any{"planId": planID}, &plan)
	if len(plan.Created) != 2 {
		t.Fatalf("plan %+v", plan)
	}
}
