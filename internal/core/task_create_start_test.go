package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// C1: task.create with a worker and start makes, assigns and starts the task in one call; the reviewer is chosen as task.assign
// chooses one when none is named.
func TestTaskCreateAssignsAndStartsInOneCall(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Ada", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Rex", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)

	var task tasks.Task
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Implement Subtract", "description": "make it return a - b", "assignee": worker.ID, "start": true}, &task)
	if task.Assignee != worker.ID || task.Reviewer != reviewer.ID {
		t.Fatalf("assignee %q reviewer %q, want %s and %s", task.Assignee, task.Reviewer, worker.ID, reviewer.ID)
	}
	if task.RunID == "" {
		t.Fatal("the task was not started")
	}
	a.waitFor("the review request", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		return len(d.Pending) == 1
	})

	// Create only with a worker: assigned, not started
	var only tasks.Task
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Implement Multiply", "assignee": worker.ID, "reviewer": reviewer.ID}, &only)
	if only.Assignee != worker.ID || only.Reviewer != reviewer.ID || only.RunID != "" {
		t.Fatalf("create only = %+v", only)
	}
	// a start without a worker is refused before anything is made
	if _, err := a.c.Call(context.Background(), "task.create", mustJSON(map[string]any{"projectId": p.ID, "title": "No worker", "start": true})); err == nil || !strings.Contains(err.Error(), "choose a worker") {
		t.Fatalf("start without a worker: %v", err)
	}
	var list []tasks.Task
	a.call("task.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 2 {
		t.Fatalf("%d tasks, want 2 (a refused start makes none)", len(list))
	}
}

// C1: a start the project cannot take (no repository) is refused before the task is made.
func TestTaskCreateAndStartWithoutARepositoryMakesNothing(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "plans"}, &p)
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Ada", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Rex", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	_, err := a.c.Call(context.Background(), "task.create", mustJSON(map[string]any{"projectId": p.ID, "title": "Implement Subtract", "assignee": worker.ID, "start": true}))
	if err == nil || !strings.Contains(err.Error(), "no repository") {
		t.Fatalf("start without a repository: %v", err)
	}
	var list []tasks.Task
	a.call("task.list", map[string]any{"projectId": p.ID}, &list)
	if len(list) != 0 {
		t.Fatalf("%d tasks were made", len(list))
	}
}
