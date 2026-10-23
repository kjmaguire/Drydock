package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The task data the next-generation screens read (decision P97): project keys, task labels, the task list's filters,
// task.summaries, what an agent is doing, and each acceptance criterion's state. Every test goes through the methods the UI calls.

func (a *app) project(name string) core.Project {
	a.t.Helper()
	var p core.Project
	a.call("project.create", map[string]any{"name": name}, &p)
	return p
}

func TestAProjectGetsAKeyFromItsNameThatAPersonCanChange(t *testing.T) {
	a := open(t, "")
	p1, p2, p3 := a.project("gocalc"), a.project("gocalc"), a.project("my web app")
	if p1.Key != "GOC" || p2.Key != "GOC2" || p3.Key != "MWA" {
		t.Fatalf("keys %q %q %q", p1.Key, p2.Key, p3.Key)
	}
	var list []core.Project
	a.call("project.list", nil, &list)
	for _, p := range list {
		if p.Key == "" {
			t.Fatalf("project.list without a key: %+v", p)
		}
	}
	var got core.Project
	a.call("project.update", map[string]any{"id": p1.ID, "key": " gx "}, &got)
	if got.Key != "GX" || got.Mode != "local_merge" {
		t.Fatalf("after the change: %+v", got)
	}
	var seen []events.Event
	a.call("events.replay", map[string]any{"types": []string{"project.updated"}}, &seen)
	if len(seen) == 0 || !strings.Contains(string(seen[len(seen)-1].Data), `"key":"GX"`) {
		t.Fatalf("project.updated %+v", seen)
	}
	// refused: another project's key, and anything that is not 2 to 5 letters or digits; the key stays as it was
	for _, k := range []string{"GOC2", "mwa", "G", "TOOLONG", "G-1"} {
		if err := a.callErr("project.update", map[string]any{"id": p1.ID, "key": k}); err == nil {
			t.Errorf("the key %q was accepted", k)
		}
	}
	// a bad key is refused before the mode changes
	if err := a.callErr("project.update", map[string]any{"id": p1.ID, "key": "x", "mergeStrategy": "squash"}); err == nil {
		t.Fatal("a bad key with a strategy was accepted")
	}
	a.call("project.get", map[string]any{"id": p1.ID}, &got)
	if got.Key != "GX" || got.MergeStrategy != "merge" {
		t.Fatalf("after refusals: %+v", got)
	}
	// the strategy alone keeps the key; the key and the strategy together change both
	a.call("project.update", map[string]any{"id": p1.ID, "mergeStrategy": "squash"}, &got)
	if got.Key != "GX" || got.MergeStrategy != "squash" {
		t.Fatalf("strategy only: %+v", got)
	}
	a.call("project.update", map[string]any{"id": p1.ID, "mergeStrategy": "rebase", "key": "GC"}, &got)
	if got.Key != "GC" || got.MergeStrategy != "rebase" {
		t.Fatalf("both: %+v", got)
	}
	// GOC is free again: a new gocalc takes it
	if p4 := a.project("gocalc"); p4.Key != "GOC" {
		t.Fatalf("a freed key: %q", p4.Key)
	}
}

// Projects made before migration 00129 have no key; the next start gives each one, the oldest project first.
func TestProjectsWithoutAKeyGetOneAtStart(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	older, newer, other := a.project("gocalc"), a.project("gocalc"), a.project("Payments API")
	a.sql(`UPDATE projects SET key='' WHERE id IN (?,?)`, older.ID, newer.ID)
	a.sql(`UPDATE projects SET key='GOC' WHERE id=?`, other.ID) // a key a person chose stays and is taken into account
	a.sql(`UPDATE projects SET created_at=1 WHERE id=?`, older.ID)
	a.sql(`UPDATE projects SET created_at=2 WHERE id=?`, newer.ID)
	a.c.Close()
	b := open(t, dataDir)
	keys := map[string]string{}
	var list []core.Project
	b.call("project.list", nil, &list)
	for _, p := range list {
		keys[p.ID] = p.Key
	}
	if keys[other.ID] != "GOC" || keys[older.ID] != "GOC2" || keys[newer.ID] != "GOC3" {
		t.Fatalf("backfilled %v", keys)
	}
}

func TestTaskLabelsAreSetListedAndFiltered(t *testing.T) {
	a := open(t, "")
	p := a.project("gocalc")
	var t1, t2, t3 tasks.Task
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Rate limiter", "labels": []string{"Backend", " auth "}}, &t1)
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Login page", "labels": []string{"frontend", "auth"}}, &t2)
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Docs"}, &t3)
	if !reflect.DeepEqual(t1.Labels, []string{"backend", "auth"}) || len(t3.Labels) != 0 {
		t.Fatalf("labels on create: %q %q", t1.Labels, t3.Labels)
	}
	if err := a.callErr("task.create", map[string]any{"projectId": p.ID, "title": "x", "labels": []string{strings.Repeat("y", 25)}}); err == nil {
		t.Fatal("a label of 25 characters was accepted")
	}
	var got tasks.Task
	a.call("task.labels.set", map[string]any{"id": t3.ID, "labels": []string{"docs", "AUTH", "docs"}}, &got)
	if !reflect.DeepEqual(got.Labels, []string{"docs", "auth"}) {
		t.Fatalf("set: %q", got.Labels)
	}
	if err := a.callErr("task.labels.set", map[string]any{"id": t3.ID, "labels": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}}); err == nil {
		t.Fatal("nine labels were accepted")
	}
	if err := a.callErr("task.labels.set", map[string]any{"id": "task_nope", "labels": []string{"a"}}); err == nil {
		t.Fatal("an unknown task got labels")
	}
	var d core.TaskDetail
	a.call("task.get", map[string]any{"id": t3.ID}, &d)
	if !reflect.DeepEqual(d.Task.Labels, []string{"docs", "auth"}) {
		t.Fatalf("task.get: %q", d.Task.Labels)
	}
	var list []tasks.Task
	a.call("task.list", map[string]any{"projectId": p.ID, "label": "auth"}, &list)
	if len(list) != 3 {
		t.Fatalf("label auth: %d", len(list))
	}
	a.call("task.list", map[string]any{"projectId": p.ID, "label": "Backend"}, &list)
	if len(list) != 1 || list[0].ID != t1.ID {
		t.Fatalf("label backend: %+v", list)
	}
	var n struct{ Total int }
	a.call("task.count", map[string]any{"projectId": p.ID, "label": "frontend"}, &n)
	if n.Total != 1 {
		t.Fatalf("count frontend: %d", n.Total)
	}
	// a label change is published like any other change of a task
	var evs []events.Event
	a.call("events.replay", map[string]any{"types": []string{"task.updated"}, "taskId": t3.ID}, &evs)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"labels":["docs","auth"]`) {
		t.Fatalf("events %+v", evs)
	}
}

func TestTheTaskListFiltersByAssigneeAndWhenATaskFinished(t *testing.T) {
	a := open(t, "")
	p, q := a.project("gocalc"), a.project("billing")
	mk := func(proj core.Project, title string) tasks.Task {
		var tk tasks.Task
		a.call("task.create", map[string]any{"projectId": proj.ID, "title": title}, &tk)
		return tk
	}
	t1, t2, t3, t4 := mk(p, "one"), mk(p, "two"), mk(q, "three"), mk(p, "four")
	a.sql(`UPDATE tasks SET assignee='agent_w' WHERE id IN (?,?)`, t1.ID, t3.ID)
	a.sql(`UPDATE tasks SET status='COMPLETED', completed_at=5000 WHERE id=?`, t1.ID)
	a.sql(`UPDATE tasks SET status='CANCELLED', completed_at=9000 WHERE id=?`, t2.ID)
	titles := func(params map[string]any) string {
		t.Helper()
		var list []tasks.Task
		a.call("task.list", params, &list)
		var n struct{ Total int }
		a.call("task.count", params, &n)
		if n.Total != len(list) {
			t.Fatalf("%v: count %d, listed %d", params, n.Total, len(list))
		}
		var out []string
		for _, x := range list {
			out = append(out, x.Title)
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"projectId": p.ID}, "four,two,one"}, // the params of before still work
		{map[string]any{"projectId": p.ID, "statuses": []string{"BACKLOG"}}, "four"},
		{map[string]any{"projectId": "", "assignee": "agent_w"}, "three,one"},
		{map[string]any{"projectId": p.ID, "assignee": "agent_w"}, "one"},
		{map[string]any{"projectId": p.ID, "completedSince": 6000}, "two"},
		{map[string]any{"projectId": p.ID, "completedSince": 1, "statuses": []string{"COMPLETED"}}, "one"},
		{map[string]any{"projectId": p.ID, "completedSince": 99999}, ""},
	} {
		if got := titles(c.params); got != c.want {
			t.Errorf("%v: %q, want %q", c.params, got, c.want)
		}
	}
	_ = t4
}

// A real task through the core, with the scripted agent: task.summaries says at each point what the board, the inbox and the
// crew show, and answering its request through run.respond with the ids it gives merges the task.
func TestTaskSummariesFollowATaskToTheMerge(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	worker, reviewer := a.agent(p.ID, "Backend #1", "backend"), a.agent(p.ID, "Reviewer #1", "reviewer")
	var task tasks.Task
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Implement Subtract", "description": "Subtract returns 0; make it return a - b.",
		"acceptance": []string{"AC1: Given 5 and 3, when Subtract is called, then it returns 2"}, "labels": []string{"math"}}, &task)
	sum := func(params map[string]any) core.TaskSummaries {
		t.Helper()
		var s core.TaskSummaries
		a.call("task.summaries", params, &s)
		return s
	}
	s := sum(map[string]any{"projectId": p.ID})
	if s.Total != 1 || s.Partial || len(s.Summaries) != 1 {
		t.Fatalf("one task: %+v", s)
	}
	first := s.Summaries[0]
	if first.Step != "spec" || first.StepIndex != 0 || first.StepCount != 5 || first.ProjectKey != "GOC" || first.Seq != 1 || first.Attempt != 1 ||
		first.MaxAttempts != 5 || len(first.Checks) != 0 || first.Diff != nil || first.Pending != nil || first.Criteria != 1 || !reflect.DeepEqual(first.Labels, []string{"math"}) {
		t.Fatalf("a new task: %+v", first)
	}
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var row core.TaskSummary
	a.waitFor("the review and merge request", func() bool {
		s = sum(map[string]any{"projectId": ""})
		for _, r := range s.Summaries {
			if r.TaskID == task.ID && r.Pending != nil {
				row = r
				return true
			}
		}
		return false
	})
	d := a.detail(task.ID)
	if row.Step != "you" || row.StepIndex != 4 || row.Status != tasks.Verification || row.Verdict != "SHIP" || row.StartedAt == 0 || row.CompletedAt != 0 {
		t.Fatalf("waiting for the merge: %+v", row)
	}
	if row.Pending.Phase != "merge" || row.Pending.Kind != "review" || row.Pending.RunID != runID || row.Pending.RequestID != string(d.Pending[0].ID) || row.PendingCount != 1 ||
		!strings.HasPrefix(row.Pending.Title, pipeline.MergeRequestTitlePrefix) {
		t.Fatalf("the request: %+v (task.get has %+v)", row.Pending, d.Pending)
	}
	if row.Diff == nil || row.Diff.Files != 1 || row.Diff.Insertions == 0 {
		t.Fatalf("the diff: %+v", row.Diff)
	}
	var kinds []string
	for _, c := range row.Checks {
		kinds = append(kinds, c.Kind+"="+c.Status)
		if c.Reason == "" {
			t.Errorf("a check without its reason: %+v", c)
		}
	}
	if strings.Join(kinds, " ") != "build=passed test=passed lint=passed secrets=passed" {
		t.Fatalf("checks %q", kinds)
	}
	// the criterion's test is not known by name (the plain template has no test-first step), so its state is unknown
	if len(d.AcceptanceState) != 1 || d.AcceptanceState[0].ID != "AC1" || d.AcceptanceState[0].State != pipeline.CriterionUnknown {
		t.Fatalf("acceptance state %+v", d.AcceptanceState)
	}

	// the worker is at a tool call on this task: the card says what it is doing, with the argument and without a secret
	ctx := context.Background()
	publish := func(typ string, v any) {
		data, _ := json.Marshal(v)
		if _, err := a.c.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: p.ID, TaskID: task.ID, AgentID: worker.ID, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	call := func(name string, args map[string]any) {
		publish("agent.output", map[string]any{"kind": "tool_call", "data": map[string]any{"name": name, "args": args}})
	}
	// its process ended with its turn: the events of a new turn on the task, as the runtime publishes them
	publish("agent.started", map[string]any{"pid": 1})
	publish("agent.waiting", map[string]any{"state": "working"})
	call("Edit", map[string]any{"file_path": filepath.Join(repo, "calc.go")})
	a.waitFor("the doing line", func() bool {
		s = sum(map[string]any{"ids": []string{task.ID}})
		return len(s.Summaries) == 1 && s.Summaries[0].Doing == "Editing calc.go"
	})
	if s.Summaries[0].Presence != string(room.PresenceWorking) {
		t.Fatalf("presence %+v", s.Summaries[0])
	}
	call("Bash", map[string]any{"command": "curl -H 'Authorization: token " + plantedToken + "' https://api.example.com"})
	var pres []room.PresenceState
	a.waitFor("the command", func() bool {
		a.call("presence.list", nil, &pres)
		for _, ps := range pres {
			if ps.AgentID == worker.ID && strings.HasPrefix(ps.Doing, "Running curl") {
				return true
			}
		}
		return false
	})
	for _, ps := range pres {
		if strings.Contains(ps.Doing, plantedToken[:12]) || (ps.AgentID == worker.ID && ps.Activity != "tool: Bash") {
			t.Fatalf("presence %+v", ps)
		}
	}

	a.call("run.respond", map[string]any{"runId": row.Pending.RunID, "requestId": row.Pending.RequestID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("the merge", func() bool {
		s = sum(map[string]any{"projectId": p.ID})
		return len(s.Summaries) == 1 && s.Summaries[0].Status == tasks.Completed
	})
	done := s.Summaries[0]
	if done.Step != "done" || done.StepIndex != 5 || done.Pending != nil || done.CompletedAt == 0 || done.Doing != "" {
		t.Fatalf("merged: %+v", done)
	}
	// a finished task leaves the summaries once it ended before finishedSince, but is still described by id
	if s := sum(map[string]any{"projectId": p.ID, "finishedSince": time.Now().Add(time.Hour).UnixMilli()}); s.Total != 0 || len(s.Summaries) != 0 {
		t.Fatalf("finished before: %+v", s)
	}
	if s := sum(map[string]any{"ids": []string{task.ID, "task_nope"}, "finishedSince": time.Now().Add(time.Hour).UnixMilli()}); len(s.Summaries) != 1 {
		t.Fatalf("by id: %+v", s)
	}
}

// Many tasks across projects in one read: the cap and the total like task.list, newest first, and each row's checks, diff,
// verdict and request read correctly from rows written as the pipeline writes them.
func TestTaskSummariesOfManyTasks(t *testing.T) {
	a := open(t, "")
	p, q := a.project("gocalc"), a.project("billing")
	const n = 620
	now := time.Now().UnixMilli()
	tx, err := a.c.DB.Writer().Begin()
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		if _, err := tx.Exec(query, args...); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
	}
	id := func(i int) string { return fmt.Sprintf("task_many_%04d", i) }
	for i := range n {
		proj := p.ID
		if i%2 == 1 {
			proj = q.ID
		}
		status := []string{"IN_PROGRESS", "REVIEW", "WAITING_HUMAN", "BACKLOG"}[i%4]
		exec(`INSERT INTO tasks(id,project_id,seq,title,status,assignee,reviewer,acceptance,labels,iteration,started_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id(i), proj, i/2+1, fmt.Sprintf("task %d", i), status, "agent_w", "agent_r", `["AC1: Given a, when b, then c"]`, `["bulk"]`, i%3, now-int64(n-i), now-int64(n-i)*10, now)
		r1, r2 := fmt.Sprintf(`{"round":"chk_%d_1"}`, i), fmt.Sprintf(`{"round":"chk_%d_2","failing":["TestAC1_limits"]}`, i)
		exec(`INSERT INTO task_evidence(task_id,kind,passed,summary,data,created_at) VALUES(?,?,?,?,?,?),(?,?,?,?,?,?),(?,?,?,?,?,?),(?,?,?,?,?,?),(?,?,?,?,?,?)`,
			id(i), "build", 1, "build passed", r1, now,
			id(i), "test", 1, "test passed", r1, now,
			id(i), "diff", nil, "2 file(s), +10 -3", `{"Files":2,"Insertions":10,"Deletions":3}`, now,
			id(i), "build", 1, "build passed", r2, now,
			id(i), "test", 0, "test failed (exit 1): TestAC1_limits", r2, now)
		exec(`INSERT INTO task_reviews(task_id,reviewer,verdict,created_at) VALUES(?,?,?,?),(?,?,?,?)`, id(i), "agent_r", "SHIP", now, id(i), "agent_r", "REVISE", now)
	}
	// requests: a spec gate on task 619, a merge on task 618, an open permission request and an expired one on task 617, a
	// request of a run that ended on task 616
	exec(`INSERT INTO workflow_runs(id,workflow,status,task_id,topology_hash,created_at,updated_at) VALUES('run_live','feature','RUNNING',?, 'h',1,1),('run_gone','feature','FAILED',?,'h',1,1),('run_617','x','RUNNING',?,'h',1,1)`,
		id(619), id(616), id(617))
	exec(`UPDATE tasks SET run_id='run_617' WHERE id=?`, id(617))
	exec(`INSERT INTO approvals(id,run_id,task_id,project_id,kind,title,payload,status,created_at,expires_at) VALUES
		('apr_spec','run_live',?,'','review','Accept the spec: x',CAST('{"phase":"spec"}' AS BLOB),'REQUESTED',10,0),
		('apr_merge','run_live',?,'','review','Review and merge: y',CAST('{}' AS BLOB),'REQUESTED',11,0),
		('apr_perm','',?,'','approval','Permission request: Bash',CAST('{}' AS BLOB),'REQUESTED',12,?),
		('apr_old','',?,'','approval','Permission request: Read',CAST('{}' AS BLOB),'REQUESTED',9,?),
		('apr_gone','run_gone',?,'','review','Review and merge: z',CAST('{}' AS BLOB),'REQUESTED',13,0)`,
		id(619), id(618), id(617), now+60_000, id(617), now-1, id(616))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	var s core.TaskSummaries
	a.call("task.summaries", map[string]any{"projectId": ""}, &s)
	took := time.Since(start)
	if s.Total != n || !s.Partial || len(s.Summaries) != core.TaskListDefault {
		t.Fatalf("total %d partial %v rows %d", s.Total, s.Partial, len(s.Summaries))
	}
	if took > 10*time.Second {
		t.Fatalf("%d summaries took %v", len(s.Summaries), took)
	}
	t.Logf("%d of %d summaries in %v", len(s.Summaries), s.Total, took)
	if s.Summaries[0].TaskID != id(n-1) || s.Summaries[1].TaskID != id(n-2) {
		t.Fatalf("not newest first: %s %s", s.Summaries[0].TaskID, s.Summaries[1].TaskID)
	}
	a.call("task.summaries", map[string]any{"projectId": "", "limit": 5000}, &s)
	if len(s.Summaries) != n || s.Partial {
		t.Fatalf("with a limit: %d partial %v", len(s.Summaries), s.Partial)
	}
	a.call("task.summaries", map[string]any{"projectId": p.ID}, &s)
	if s.Total != n/2 || s.Partial || len(s.Summaries) != n/2 || s.Summaries[0].ProjectKey != "GOC" || s.Summaries[0].Seq != n/2 {
		t.Fatalf("one project: total %d rows %d first %+v", s.Total, len(s.Summaries), s.Summaries[0])
	}
	byID := map[string]core.TaskSummary{}
	a.call("task.summaries", map[string]any{"ids": []string{id(619), id(618), id(617), id(616), id(1)}}, &s)
	for _, r := range s.Summaries {
		byID[r.TaskID] = r
	}
	r := byID[id(1)]
	var cs []string
	for _, c := range r.Checks {
		cs = append(cs, c.Kind+"="+c.Status)
	}
	if strings.Join(cs, " ") != "build=passed test=failed" || !reflect.DeepEqual(r.Checks[1].Failing, []string{"TestAC1_limits"}) || r.Checks[1].Reason != "test failed (exit 1): TestAC1_limits" {
		t.Fatalf("checks %+v", r.Checks)
	}
	if r.Diff == nil || *r.Diff != (core.SummaryDiff{Files: 2, Insertions: 10, Deletions: 3}) || r.Verdict != "REVISE" || r.Step != "review" || r.Attempt != 2 || r.ProjectKey != "BIL" {
		t.Fatalf("row 1 %+v", r)
	}
	if g := byID[id(619)]; g.Pending == nil || g.Pending.Phase != "spec" || g.Pending.RunID != "run_live" || g.Step != "spec" || g.StepIndex != 0 {
		t.Fatalf("spec gate %+v %+v", g, g.Pending)
	}
	if m := byID[id(618)]; m.Pending == nil || m.Pending.Phase != "merge" || m.Pending.RequestID != "apr_merge" {
		t.Fatalf("merge %+v", m.Pending)
	}
	if pr := byID[id(617)]; pr.Pending == nil || pr.Pending.Phase != "approval" || pr.Pending.RequestID != "apr_perm" || pr.Pending.RunID != "run_617" || pr.PendingCount != 1 {
		t.Fatalf("permission %+v %+v", pr, pr.Pending)
	}
	if g := byID[id(616)]; g.Pending != nil || g.PendingCount != 0 {
		t.Fatalf("a request of a run that ended: %+v", g.Pending)
	}

	// a pass that has started on task 1 and runs its first check
	data, _ := json.Marshal(map[string]any{"kind": "build", "round": "chk_new"})
	if _, err := a.c.Bus.Publish(context.Background(), events.Event{Type: pipeline.CheckStartedEvent, TaskID: id(1), ProjectID: q.ID, Data: data}); err != nil {
		t.Fatal(err)
	}
	a.waitFor("the running check", func() bool {
		a.call("task.summaries", map[string]any{"ids": []string{id(1)}}, &s)
		return len(s.Summaries) == 1 && len(s.Summaries[0].Checks) == 2 && s.Summaries[0].Checks[0].Status == pipeline.CheckRunning
	})
	if s.Summaries[0].Checks[1].Status != pipeline.CheckNotRun {
		t.Fatalf("the rest of a new pass: %+v", s.Summaries[0].Checks)
	}
	// its result ends it
	if _, err := a.c.Tasks.AddEvidence(context.Background(), id(1), tasks.Evidence{Kind: "build", Passed: ptrBool(true), Summary: "build passed", Data: json.RawMessage(`{"round":"chk_new"}`)}); err != nil {
		t.Fatal(err)
	}
	a.waitFor("the recorded result", func() bool {
		a.call("task.summaries", map[string]any{"ids": []string{id(1)}}, &s)
		return s.Summaries[0].Checks[0].Status == pipeline.CheckPassed && s.Summaries[0].Checks[1].Status == pipeline.CheckNotRun
	})
}

func ptrBool(b bool) *bool { return &b }

// The acceptance criteria's state on task.get, from evidence written as a spec-gated feature writes it.
func TestTaskGetSaysWhichAcceptanceCriteriaPass(t *testing.T) {
	a := open(t, "")
	p := a.project("gocalc")
	var tk tasks.Task
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Limiter",
		"acceptance": []string{"AC1: Given a, when b, then c", "AC2: Given d, when e, then f", "Given no id, when x, then y"}}, &tk)
	d := a.detail(tk.ID)
	if len(d.AcceptanceState) != 3 || d.AcceptanceState[0].State != "unknown" || d.AcceptanceState[2].ID != "" {
		t.Fatalf("before any test: %+v", d.AcceptanceState)
	}
	ctx := context.Background()
	add := func(kind string, passed *bool, data string) {
		t.Helper()
		if _, err := a.c.Tasks.AddEvidence(ctx, tk.ID, tasks.Evidence{Kind: kind, Passed: passed, Data: json.RawMessage(data)}); err != nil {
			t.Fatal(err)
		}
	}
	add("spec_accept", ptrBool(true), `{}`)
	add("red_test", nil, `{"failing":["TestLimiter/AC1_blocks","TestLimiter/AC2_resets"]}`)
	add("test", ptrBool(false), `{"failing":["TestLimiter/AC2_resets"]}`)
	d = a.detail(tk.ID)
	states := func() string {
		var out []string
		for _, s := range d.AcceptanceState {
			out = append(out, fmt.Sprintf("%d:%s=%s", s.Index, s.ID, s.State))
		}
		return strings.Join(out, " ")
	}
	if got := states(); got != "0:AC1=unknown 1:AC2=failing 2:=unknown" {
		t.Fatalf("one failing: %s", got)
	}
	add("test", ptrBool(true), `{}`)
	d = a.detail(tk.ID)
	if got := states(); got != "0:AC1=passed 1:AC2=passed 2:=unknown" {
		t.Fatalf("passing: %s", got)
	}
	var raw map[string]json.RawMessage
	a.call("task.get", map[string]any{"id": tk.ID}, &raw)
	if !strings.Contains(string(raw["acceptanceState"]), `{"index":0,"id":"AC1","state":"passed"}`) {
		t.Fatalf("the JSON: %s", raw["acceptanceState"])
	}
}
