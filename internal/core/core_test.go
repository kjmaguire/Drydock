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
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

func TestMain(m *testing.M) {
	if scantest.IsStandIn(os.Args) { // the LABELLED stand-in osv-scanner and opengrep of the security scan tests (ext_security_test.go)
		os.Exit(scantest.Main(os.Args))
	}
	if llamatest.IsStandIn(os.Args) { // the LABELLED stand-in llama-server of the local models tests (ext_localllm_test.go)
		os.Exit(llamatest.Main(os.Args))
	}
	if wsltest.IsStandIn() { // the LABELLED stand-in wsl.exe of the WSL run mode tests (wsl_test.go)
		os.Exit(wsltest.StandIn(os.Args[1:]))
	}
	if wsltest.IsInteropCheck() { // started as the MCP server command by the stand-in claude inside a distro
		os.Exit(wsltest.InteropCheckMain())
	}
	// a real agent CLI starts the gateway's stdio shim as <this binary> mcp-shim; it must never fall through to running the tests
	if code, ok := mcp.MaybeRunShim(os.Args); ok {
		os.Exit(code)
	}
	serveTestMCPIfAsked() // a stdio MCP server for the user-added server tests (ext_mcpservers_test.go)
	// the scripted ACP agent (a Codex stand-in) is told apart by the variables its adapter adds, so it is checked first
	if os.Getenv("DRYDOCK_FAKE_ACP") == "1" {
		os.Exit(fakeacp.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
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
	t *testing.T
	c *core.Core
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
	deadline := waitDeadline(a.t)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.t.Fatalf("timed out waiting for %s", what)
}

// waitDeadline is how long a test waits for something the core does in the background. These waits pass in about a second
// on an idle machine; a fixed limit failed under heavy load (load average 25, audit section 3.4) although nothing was wrong,
// so a wait lasts until shortly before the test binary's own -timeout instead, with 90 seconds when there is none. A real
// hang still fails here, with what was awaited, before the binary's timeout kills every test without saying which one.
func waitDeadline(t testing.TB) time.Time {
	if d, ok := t.(interface{ Deadline() (time.Time, bool) }); ok {
		if end, ok := d.Deadline(); ok {
			if until := time.Until(end); until > 2*time.Minute {
				return end.Add(-time.Minute)
			}
		}
	}
	return time.Now().Add(90 * time.Second)
}

// newRepo makes a Git repository of the gocalc fixture and returns its canonical path: the form git reports and Drydock
// stores (on Windows the temporary folder can be a short 8.3 name such as RUNNER~1, which git and Drydock expand).
func newRepo(t *testing.T) string {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "gocalc")
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
	// the item says what is being decided: the worker, the change's size, its checks and the reviewer's verdict (UI review P1-1)
	var nc *room.NeedsContext
	for _, n := range needs {
		if n.TaskID == task.ID && n.Kind == room.NeedsReview {
			nc = n.Context
		}
	}
	if nc == nil || nc.Agent != worker.ID || !nc.HasDiff || nc.Files != 1 || nc.Insertions == 0 || nc.ChecksTotal == 0 || nc.ChecksPassed != nc.ChecksTotal || nc.Verdict != "SHIP" {
		t.Fatalf("review item context = %+v", nc)
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

func TestGatedConnectionsNeedHumanAcknowledgementAndItSurvivesRestart(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := open(t, dataDir)
	var conns []struct {
		ID    string `json:"adapter"`
		Gates []struct {
			ID    string `json:"id"`
			Acked bool   `json:"acked"`
		} `json:"gates"`
	}
	a.call("connection.list", nil, &conns)
	gate := ""
	ids := map[string]bool{}
	for _, c := range conns {
		ids[c.ID] = true
		if c.ID == "antigravity" && len(c.Gates) == 1 && !c.Gates[0].Acked {
			gate = c.Gates[0].ID
		}
	}
	for _, want := range []string{"claude", "codex", "gemini", "opencode", "goose", "antigravity"} {
		if !ids[want] {
			t.Errorf("connection %s missing: %v", want, ids)
		}
	}
	if gate == "" {
		t.Fatalf("antigravity must be gated: %+v", conns)
	}
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	spec := core.AgentSpec{ProjectID: p.ID, Name: "Antigravity", Adapter: "antigravity", Role: "frontend"}
	if _, err := a.c.Call(context.Background(), "agent.create", mustJSON(spec)); err == nil || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatalf("an unacknowledged gated connection must not be usable: %v", err)
	}
	a.call("connection.acknowledge", map[string]string{"gateId": gate}, nil)
	a.call("agent.create", spec, nil)
	a.c.Close()

	b := open(t, dataDir)
	b.call("connection.list", nil, &conns)
	for _, c := range conns {
		if c.ID == "antigravity" && !c.Gates[0].Acked {
			t.Fatal("the acknowledgement was not persisted")
		}
	}
}

// A secret pasted into the room is redacted before it is stored, so it never reaches the database or the event log.
func TestSecretsPastedIntoTheRoomAreRedactedBeforeStorage(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
	secret := "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "aB3dE5"
	a.call("room.post", map[string]any{"roomId": rooms[0].ID, "body": "use this token " + secret + " for the deploy"}, nil)
	var msgs []room.Message
	a.call("room.messages", map[string]any{"roomId": rooms[0].ID, "limit": 50}, &msgs)
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Body, secret) {
			t.Fatalf("the secret was stored in the message body: %q", m.Body)
		}
		if strings.Contains(m.Body, "for the deploy") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the (redacted) message was not stored: %+v", msgs)
	}
	var n int
	a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM events WHERE data LIKE '%' || ? || '%'`, secret).Scan(&n)
	if n != 0 {
		t.Fatalf("the secret reached %d event(s)", n)
	}
}

func TestProviderRoomAndUsageMethodsForThePages(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 3 * time.Second,
		Credentials: providers.NewMemoryCredentialStoreForTests(),
		Adapters:    map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &app{t: t, c: c}

	// Providers page: create (key goes to the credential store, never into the row), list, disable, delete
	var prov providers.Provider
	a.call("provider.create", map[string]any{"Name": "OpenRouter", "Kind": "aggregator", "BaseURL": "https://openrouter.ai/api/v1", "APIKey": "sk-or-test-key-1234567890",
		"DataClass": "private", "Tier": "cheap"}, &prov)
	if prov.ID == "" || prov.KeyRef == "" || prov.DataClass != providers.DataPrivate {
		t.Fatalf("provider %+v", prov)
	}
	raw, _ := json.Marshal(prov)
	if strings.Contains(string(raw), "sk-or-test-key") {
		t.Fatal("the API key must never be part of a provider row or API answer")
	}
	var stored string
	c.DB.Reader().QueryRow(`SELECT COALESCE(group_concat(key_ref),'') FROM providers`).Scan(&stored)
	if strings.Contains(stored, "sk-or-test") {
		t.Fatal("the API key was stored in the providers table")
	}
	var list []providers.Provider
	a.call("provider.list", nil, &list)
	if len(list) != 1 || list[0].ID != prov.ID {
		t.Fatalf("list %+v", list)
	}
	a.call("provider.setEnabled", map[string]any{"id": prov.ID, "enabled": false}, nil)
	a.call("provider.list", nil, &list)
	if list[0].Enabled {
		t.Fatal("provider still enabled")
	}
	// a private-class provider on plain http to a public host is refused at registration
	if _, err := c.Call(context.Background(), "provider.create", mustJSON(map[string]any{"Name": "bad", "Kind": "direct", "BaseURL": "http://example.com/v1", "DataClass": "private", "Tier": "cheap"})); err == nil {
		t.Fatal("plain http to a public host must be refused")
	}
	a.call("provider.delete", map[string]any{"id": prov.ID}, nil)

	// room pages: archived channels stay reachable, DMs can be created, usage and the agent inspector read real rows
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var w core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &w)
	var dm room.Room
	a.call("room.createDM", map[string]any{"projectId": p.ID, "agentId": w.ID}, &dm)
	if dm.Kind != room.KindDM {
		t.Fatalf("dm %+v", dm)
	}
	var got room.Room
	a.call("room.get", map[string]any{"id": dm.ID}, &got)
	if got.ID != dm.ID {
		t.Fatal("room.get")
	}
	var head uint64
	a.call("events.head", nil, &head)
	if head == 0 {
		t.Fatal("events.head must report the newest sequence")
	}
	var det core.AgentDetail
	a.call("agent.get", map[string]any{"id": w.ID}, &det)
	if det.Agent.ID != w.ID || det.Runs == nil || det.Usage == nil {
		t.Fatalf("agent detail %+v", det)
	}
	var usage []core.UsageRow
	a.call("usage.list", map[string]any{"agentId": w.ID}, &usage)
	if usage == nil {
		t.Fatal("usage.list must return a list")
	}
	var adapters []map[string]any
	a.call("adapter.list", nil, &adapters)
	for i := 1; i < len(adapters); i++ {
		if adapters[i-1]["id"].(string) > adapters[i]["id"].(string) {
			t.Fatal("adapter.list must be sorted")
		}
	}
}

// A task created with a template starts through it, so task.start cannot skip the spec gate.
func TestTaskStartHonoursTheTasksTemplate(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer"}, &r)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "PM", Adapter: "claude", Role: "pm"}, nil)
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Add Multiply", Description: "multiply two ints", Template: "feature"}, &task)
	a.call("task.assign", map[string]string{"id": task.ID, "assignee": w.ID, "reviewer": r.ID}, &task)
	var runID string
	a.call("task.start", map[string]string{"id": task.ID}, &runID)
	var d core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &d)
	if d.Run == nil || d.Run.Workflow != "feature" {
		t.Fatalf("a feature task must run the feature workflow (spec gate), got %+v", d.Run)
	}
}

type noDeadline struct{ testing.TB }

// A background wait lasts until a minute before the test binary's timeout, and 90 seconds without one.
func TestWaitDeadlineFollowsTheTestTimeout(t *testing.T) {
	end, ok := t.Deadline()
	got := waitDeadline(t)
	if ok && time.Until(end) > 2*time.Minute && !got.Equal(end.Add(-time.Minute)) {
		t.Fatalf("wait until %s, test deadline %s", got, end)
	}
	if left := time.Until(waitDeadline(noDeadline{t})); left < 85*time.Second || left > 90*time.Second {
		t.Fatalf("without a deadline: %s", left)
	}
}
