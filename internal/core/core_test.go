package core_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func TestMain(m *testing.M) {
	if os.Getenv("DRYDOCK_FAKE_AGENT") == "1" {
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

const impl = "package gocalc\n\nfunc Add(a, b int) int { return a + b }\n\n// Subtract returns a minus b.\nfunc Subtract(a, b int) int { return a - b }\n"

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

type app struct {
	t    *testing.T
	c    *core.Core
	repo string
}

// open starts a Core whose "claude" adapter is the scripted real-process agent (the same stand-in the pipeline tests use).
func open(t *testing.T, dataDir string) *app {
	t.Helper()
	dir := t.TempDir()
	script, _ := json.Marshal(fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	})
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, script, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	if dataDir == "" {
		dataDir = filepath.Join(dir, "data")
	}
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

func (a *app) call(method string, params any, out any) {
	a.t.Helper()
	b, _ := json.Marshal(params)
	res, err := a.c.Call(context.Background(), method, b)
	if err != nil {
		a.t.Fatalf("%s: %v", method, err)
	}
	if out != nil {
		if err := json.Unmarshal(res, out); err != nil {
			a.t.Fatalf("%s decode: %v (%s)", method, err, res)
		}
	}
}

func (a *app) waitFor(what string, cond func() bool) {
	a.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.t.Fatalf("timed out waiting for %s", what)
}

func newRepo(t *testing.T) string {
	repo := filepath.Join(t.TempDir(), "gocalc")
	os.MkdirAll(repo, 0o755)
	copyTree(t, filepath.Join("..", "..", "testdata", "fixtures", "gocalc"), repo)
	if _, err := git.Init(context.Background(), repo, "main"); err != nil {
		t.Fatal(err)
	}
	return repo
}

// The whole product loop through the same methods the UI calls: project, team, task, start, the room fills in as the work
// happens, the human is asked, approves, and the task completes with the channel archived.
func TestUIMethodsDriveATaskFromCreationToHumanMerge(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)

	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	if p.DefaultBranch != "main" {
		t.Fatalf("project %+v", p)
	}
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
	if len(rooms) == 0 {
		t.Fatal("a new project must have its default channel")
	}
	var worker, reviewer core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "fake-1"}, &worker)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Reviewer #1", Adapter: "claude", Role: "reviewer", Model: "fake-1"}, &reviewer)
	if worker.PermissionMode != "acceptEdits" {
		t.Fatalf("headless workers must default to acceptEdits, got %q", worker.PermissionMode)
	}
	if _, err := a.c.Call(context.Background(), "agent.create", mustJSON(core.AgentSpec{ProjectID: p.ID, Name: "x", Adapter: "nonesuch", Role: "r"})); err == nil {
		t.Fatal("an unknown adapter must be refused")
	}

	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "Subtract returns 0; make it return a - b.", Acceptance: []string{"Given 5 and 3 When Subtract is called Then 2"}}, &task)
	// the task channel appears by itself
	var taskRoom room.Room
	a.waitFor("task channel", func() bool {
		var d core.TaskDetail
		a.call("task.get", map[string]string{"id": task.ID}, &d)
		if d.RoomID != "" {
			r, _ := a.c.Room.GetRoom(context.Background(), d.RoomID)
			taskRoom = r
			return true
		}
		return false
	})
	// assignment rules
	if _, err := a.c.Call(context.Background(), "task.assign", mustJSON(map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": worker.ID})); err == nil {
		t.Fatal("the reviewer must differ from the worker")
	}
	if _, err := a.c.Call(context.Background(), "task.start", mustJSON(map[string]string{"id": task.ID})); err == nil {
		t.Fatal("a task without a team cannot start")
	}
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": worker.ID, "reviewer": reviewer.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)

	var detail core.TaskDetail
	a.waitFor("the human request", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return len(detail.Pending) == 1
	})
	if detail.Pending[0].Spec.Kind != "review" || detail.Task.Status != tasks.Verification {
		t.Fatalf("pending %+v task %s", detail.Pending, detail.Task.Status)
	}
	var diff string
	a.call("task.diff", map[string]string{"id": task.ID}, &diff)
	if !strings.Contains(diff, "return a - b") {
		t.Fatalf("diff:\n%s", diff)
	}

	// the room shows what happened: tool members' check results, the worker's report, the reviewer's verdict
	var msgs []room.Message
	a.call("room.messages", map[string]any{"roomId": taskRoom.ID, "limit": 100}, &msgs)
	var authors []string
	for _, m := range msgs {
		authors = append(authors, m.Author.Key()+":"+string(m.Type))
	}
	joined := strings.Join(authors, " ")
	for _, want := range []string{"tool:tests:RESULT", worker.ID + ":CHAT", reviewer.ID + ":RESULT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("task channel is missing %s; has %v", want, authors)
		}
	}
	var needs []room.NeedsYouItem
	a.call("needsyou.list", map[string]string{"projectId": p.ID}, &needs)
	if len(needs) == 0 {
		t.Fatal("the pending approval must appear in Needs you")
	}

	a.call("run.respond", map[string]any{"runId": runID, "requestId": detail.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	a.waitFor("completion", func() bool {
		a.call("task.get", map[string]string{"id": task.ID}, &detail)
		return detail.Task.Status == tasks.Completed
	})
	if detail.Task.AcceptedBy != "human:kyle" {
		t.Fatalf("accepted by %q", detail.Task.AcceptedBy)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "calc.go")); !strings.Contains(string(b), "return a - b") {
		t.Fatalf("the merge did not land:\n%s", b)
	}
	a.waitFor("task channel archived", func() bool {
		r, _ := a.c.Room.GetRoom(context.Background(), taskRoom.ID)
		return r.Archived
	})
	// the UI can catch up from the event log
	var evs []struct {
		Seq  uint64 `json:"Seq"`
		Type string `json:"Type"`
	}
	a.call("events.replay", map[string]any{"after": 0, "taskId": task.ID}, &evs)
	have := map[string]bool{}
	for _, e := range evs {
		have[e.Type] = true
	}
	for _, w := range []string{"task.created", "task.completed", "git.commit_created"} {
		if !have[w] {
			t.Errorf("event %s missing from replay", w)
		}
	}
}

// Closing the app while a run waits for the human and opening it again resumes exactly there.
func TestRestartKeepsTheRunWaitingForTheHuman(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	repo := newRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer"}, &r)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	a.waitFor("the human request", func() bool { a.call("task.get", map[string]string{"id": task.ID}, &d); return len(d.Pending) == 1 })
	a.c.Close()

	b := &app{t: t}
	c2, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	b.c = c2
	t.Cleanup(c2.Close)
	var d2 core.TaskDetail
	b.call("task.get", map[string]string{"id": task.ID}, &d2)
	if len(d2.Pending) != 1 || d2.Pending[0].ID != d.Pending[0].ID {
		t.Fatalf("after restart: %+v", d2.Pending)
	}
	b.call("run.respond", map[string]any{"runId": runID, "requestId": d2.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	b.waitFor("completion after restart", func() bool {
		b.call("task.get", map[string]string{"id": task.ID}, &d2)
		return d2.Task.Status == tasks.Completed
	})
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
