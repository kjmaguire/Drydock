package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The collision guard is on unless the human turns it off, the switch is a project setting, and only a task that is really
// waiting can be released.
func TestCollisionGuardSettingAndRelease(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)

	var s pipeline.DevSettings
	a.call("projectdev.get", map[string]string{"projectId": p.ID}, &s)
	if !s.GuardOn() {
		t.Fatalf("the guard must be on by default: %+v", s)
	}
	off := false
	s.CollisionGuard = &off
	a.call("projectdev.set", map[string]any{"projectId": p.ID, "settings": s}, &s)
	if s.GuardOn() {
		t.Fatal("the guard stayed on after the human turned it off")
	}
	a.call("projectdev.get", map[string]string{"projectId": p.ID}, &s)
	if s.GuardOn() {
		t.Fatal("the switch was not stored")
	}

	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Anything", Description: "d"}, &task)
	if err := a.callErr("task.guard.release", map[string]string{"id": task.ID}); err == nil || !strings.Contains(err.Error(), "not waiting") {
		t.Fatalf("releasing a task that is not waiting: %v", err)
	}
}
