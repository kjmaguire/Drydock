package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// A project's own routing level (decision P118): it falls back to the workspace's, the router routes the project's turns at
// it, and each decision's explanation says which level it ran at and where that came from. Through the methods the pages
// call, the real workflow and the labelled fake agent.
func TestAProjectsOwnLevelRoutesItsTurnsAndTheExplanationSaysSo(t *testing.T) {
	g := newGuardRig(t, fakeagent.Script{}, "haiku", "opus")
	var rl core.RouteLevel
	g.a.call("route.level.get", map[string]string{"projectId": g.p.ID}, &rl)
	if rl.Workspace != "balanced" || rl.Project != "" || rl.Effective != "balanced" || rl.Source != "workspace" || len(rl.Levels) != 6 {
		t.Fatalf("defaults %+v", rl)
	}
	for name, bad := range map[string]map[string]string{
		"not a level":           {"projectId": g.p.ID, "level": "turbo"},
		"no workspace level":    {"projectId": "", "level": ""},
		"no such project":       {"projectId": "prj_nope", "level": "quality"},
		"workspace not a level": {"level": "max"},
	} {
		if err := g.a.callErr("route.level.set", bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// the workspace moves to Cheapest; the project follows it until it sets its own
	g.a.call("route.level.set", map[string]string{"level": "cheapest"}, &rl)
	g.a.call("route.level.get", map[string]string{"projectId": g.p.ID}, &rl)
	if rl.Workspace != "cheapest" || rl.Effective != "cheapest" || rl.Source != "workspace" {
		t.Fatalf("following the workspace %+v", rl)
	}
	var rs core.RouteSettings
	g.a.call("route.settings.get", nil, &rs)
	if rs.Level != "cheapest" {
		t.Fatalf("the Routing page shows the workspace's level: %q", rs.Level)
	}
	g.a.call("route.level.set", map[string]string{"projectId": g.p.ID, "level": "quality"}, &rl)
	if rl.Project != "quality" || rl.Effective != "quality" || rl.Source != "project" || rl.Workspace != "cheapest" {
		t.Fatalf("the project's own %+v", rl)
	}
	if evs := g.a.eventsOf("routing.level_changed"); len(evs) != 2 {
		t.Fatalf("events %v", evs)
	}

	// at Quality the worker's first turn runs on its premium model (at Balanced it starts cheap)
	task := g.task("Implement Subtract", g.worker)
	g.a.call("task.start", map[string]string{"id": task.ID}, new(string))
	var id string
	g.a.waitFor("the work turn's decision", func() bool {
		return g.a.c.DB.Reader().QueryRow(`SELECT id FROM routing_decisions WHERE task_id=? AND run_kind='agent_task' ORDER BY ts LIMIT 1`, task.ID).Scan(&id) == nil
	})
	var level, model string
	if err := g.a.c.DB.Reader().QueryRow(`SELECT level, model FROM routing_decisions WHERE id=?`, id).Scan(&level, &model); err != nil {
		t.Fatal(err)
	}
	if level != "quality" || model != "opus" {
		t.Fatalf("the work turn was decided at %q on %q", level, model)
	}
	var ex struct {
		Text string `json:"text"`
	}
	g.a.call("route.explain", map[string]string{"decisionId": id}, &ex)
	if !strings.Contains(ex.Text, "- Level: Quality, the project's own level (its settings).") {
		t.Fatalf("Why this model? does not name the level:\n%s", ex.Text)
	}
	// the reviewer has no model list: its static decision names the level too
	var rid string
	g.a.waitFor("the review turn's decision", func() bool {
		return g.a.c.DB.Reader().QueryRow(`SELECT id FROM routing_decisions WHERE task_id=? AND run_kind='review' ORDER BY ts LIMIT 1`, task.ID).Scan(&rid) == nil
	})
	g.a.call("route.explain", map[string]string{"decisionId": rid}, &ex)
	if !strings.Contains(ex.Text, "- Level: Quality, the project's own level") || !strings.Contains(ex.Text, "static assignment") {
		t.Fatalf("the static decision:\n%s", ex.Text)
	}

	// the project gives its level back: the workspace's applies again, and a decision would say so
	g.a.call("route.level.set", map[string]string{"projectId": g.p.ID, "level": ""}, &rl)
	if rl.Project != "" || rl.Effective != "cheapest" || rl.Source != "workspace" {
		t.Fatalf("back to the workspace %+v", rl)
	}
}

// A task's own routing level and its template's (decision P127, docs/model-routing.md s3): the most specific wins, the task's
// own over the level its template declares over the project's over the workspace's. A person sets a task's level on the task
// page (route.level.set with taskId, an event); the router routes the task's turns at it and "Why this model?" says where it
// came from. Through the methods the pages call, the real workflow and the labelled fake agent.
func TestATasksOwnLevelAndItsTemplatesRouteItsTurns(t *testing.T) {
	g := newGuardRig(t, fakeagent.Script{}, "haiku", "opus")
	var rl core.RouteLevel
	g.a.call("route.level.set", map[string]string{"projectId": g.p.ID, "level": "cheapest"}, &rl)

	// a task of the Security template runs at the level the template declares, over the project's
	var sec tasks.Task
	g.a.call("task.create", tasks.CreateReq{ProjectID: g.p.ID, Title: "Fix the token leak", Template: "security"}, &sec)
	g.a.call("route.level.get", map[string]string{"taskId": sec.ID}, &rl)
	if rl.TaskID != sec.ID || rl.ProjectID != g.p.ID || rl.Task != "" || rl.Template != "Security" || rl.TemplateLevel != "quality" ||
		rl.Project != "cheapest" || rl.Effective != "quality" || rl.Source != "template" {
		t.Fatalf("the template's level %+v", rl)
	}
	// the task's own level is over the template's; "" follows the template again
	g.a.call("route.level.set", map[string]string{"taskId": sec.ID, "level": "fast"}, &rl)
	if rl.Task != "fast" || rl.Effective != "fast" || rl.Source != "task" || rl.TemplateLevel != "quality" {
		t.Fatalf("the task's own %+v", rl)
	}
	rl = core.RouteLevel{} // an empty task level is left out of the answer
	g.a.call("route.level.set", map[string]string{"taskId": sec.ID, "level": ""}, &rl)
	if rl.Task != "" || rl.Effective != "quality" || rl.Source != "template" {
		t.Fatalf("back to the template %+v", rl)
	}
	for name, bad := range map[string]map[string]string{
		"not a level":  {"taskId": sec.ID, "level": "turbo"},
		"no such task": {"taskId": "tsk_nope", "level": "quality"},
	} {
		if err := g.a.callErr("route.level.set", bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var withTask int
	for _, ev := range g.a.eventsOf("routing.level_changed") {
		if ev["taskId"] == sec.ID && ev["_task"] == sec.ID && ev["by"] != "" {
			withTask++
		}
	}
	if withTask != 2 {
		t.Fatalf("%d task level events", withTask)
	}

	// a plain task with its own level Quality: its worker's first turn runs on the premium model although the project is at
	// Cheapest, and the explanation says the level is the task's own
	task := g.task("Implement Subtract", g.worker)
	rl = core.RouteLevel{}
	g.a.call("route.level.set", map[string]string{"taskId": task.ID, "level": "quality"}, &rl)
	if rl.Source != "task" || rl.Template != "" {
		t.Fatalf("%+v", rl)
	}
	g.a.call("task.start", map[string]string{"id": task.ID}, new(string))
	var id string
	g.a.waitFor("the work turn's decision", func() bool {
		return g.a.c.DB.Reader().QueryRow(`SELECT id FROM routing_decisions WHERE task_id=? AND run_kind='agent_task' ORDER BY ts LIMIT 1`, task.ID).Scan(&id) == nil
	})
	var level, model string
	if err := g.a.c.DB.Reader().QueryRow(`SELECT level, model FROM routing_decisions WHERE id=?`, id).Scan(&level, &model); err != nil {
		t.Fatal(err)
	}
	if level != "quality" || model != "opus" {
		t.Fatalf("the work turn was decided at %q on %q", level, model)
	}
	var ex struct {
		Text string `json:"text"`
	}
	g.a.call("route.explain", map[string]string{"decisionId": id}, &ex)
	if !strings.Contains(ex.Text, "- Level: Quality, this task's own level (set on its page).") {
		t.Fatalf("Why this model? does not name the task's level:\n%s", ex.Text)
	}
}
