package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// TestMain turns the test binary into the scripted fake agent when asked to: a real separate process speaking Claude
// Code's stream-json dialect.
func TestMain(m *testing.M) {
	if os.Getenv("DRYDOCK_FAKE_AGENT") == "1" {
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

type harness struct {
	dir string
	db  *storage.DB
	bus *events.SQLBus
	rt  *rt.Runtime
	ad  *claude.Adapter
}

func newHarness(t *testing.T, script fakeagent.Script) *harness {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 4096)
	ad := &claude.Adapter{Exe: os.Args[0]}
	r := &rt.Runtime{PM: proc.New(), Bus: bus, DB: db, Adapters: map[string]agents.Adapter{"claude": ad}, NudgeWait: 5 * time.Second}
	t.Cleanup(func() {
		// the exit watchers record the runs' ends after the processes are gone: close the database after them (on Windows
		// a write still in flight keeps drydock.db open and the temporary folder cannot be removed)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := r.Wait(ctx); err != nil {
			t.Errorf("an agent session was still running when the test ended: %v", err)
		}
		bus.Close()
		db.Close()
	})
	b, _ := json.Marshal(script)
	sp := filepath.Join(dir, "script.json")
	os.WriteFile(sp, b, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	t.Setenv("DRYDOCK_FAKE_PROMPT_LOG", filepath.Join(dir, "prompts.log"))
	seed(t, db)
	return &harness{dir: dir, db: db, bus: bus, rt: r, ad: ad}
}

func seed(t *testing.T, db *storage.DB) {
	t.Helper()
	w := db.Writer()
	for _, q := range []string{
		`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','demo','/x',1)`,
		`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('a1','p1','Backend #1','claude','backend',1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func prompts(t *testing.T, dir string) []string {
	b, _ := os.ReadFile(filepath.Join(dir, "prompts.log"))
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var s string
		if json.Unmarshal([]byte(l), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// settle waits until the end of every session the test started is recorded (Runtime.Wait).
func (h *harness) settle(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.rt.Wait(ctx); err != nil {
		t.Fatalf("the ends of the agent runs were not recorded: %v", err)
	}
}

func (h *harness) config() rt.Config {
	return rt.Config{AgentID: "a1", ProjectID: "p1", Adapter: "claude", Provider: "fake", Tier: "premium", Spec: agents.RunSpec{AgentID: "a1", Cwd: h.dir, Model: "fake-1"}}
}

func TestDetectAgainstFakeBinary(t *testing.T) {
	h := newHarness(t, fakeagent.Script{})
	d, err := h.ad.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Installed || d.Version == "" || d.Authenticated == nil || !*d.Authenticated || d.Status != agents.StatusReady {
		t.Fatalf("detection %+v", d)
	}
}

func TestDetectMissingCLI(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	d, _ := (&claude.Adapter{}).Detect(context.Background())
	if d.Installed || d.Status != agents.StatusNotInstalled || d.InstallHint == "" || len(d.Problems) == 0 {
		t.Fatalf("detection %+v", d)
	}
}

func TestTurnStreamsRecordsRunUsageAndEvents(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "hello from the fake agent, streaming word by word"}, {Tool: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`), Result: "package a"}, {Say: "all done"}}}})
	ctx := context.Background()
	m, err := h.rt.Spawn(ctx, h.config())
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	res, err := m.RunTurn(ctx, "please do the thing", nil, func(c workflow.Chunk) { seen = append(seen, c.Kind) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "all done" || res.StopReason != "end_turn" || res.ToolCalls != 1 || res.SessionID == "" {
		t.Fatalf("result %+v", res)
	}
	joined := strings.Join(seen, ",")
	for _, k := range []string{"session", "text", "message_end", "tool_call", "tool_result", "usage", "done"} {
		if !strings.Contains(joined, k) {
			t.Fatalf("missing chunk kind %s in %s", k, joined)
		}
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// the exit watcher writes the run's row, then publishes agent.stopped, after the process is gone
	h.settle(t)

	// usage recorded under the tier, as reported
	var n int
	var tier, source string
	var in, out int64
	if err := h.db.Reader().QueryRow(`SELECT COUNT(*),MAX(tier),MAX(source),SUM(input_tokens),SUM(output_tokens) FROM model_usage WHERE agent_id='a1'`).Scan(&n, &tier, &source, &in, &out); err != nil {
		t.Fatal(err)
	}
	if n != 1 || tier != "premium" || source != "reported" || in != 10 || out != 5 {
		t.Fatalf("usage rows n=%d tier=%s source=%s in=%d out=%d", n, tier, source, in, out)
	}
	// agent run row and events
	var status string
	h.db.Reader().QueryRow(`SELECT status FROM agent_runs WHERE id=?`, m.RunID).Scan(&status)
	if status != "STOPPED" {
		t.Fatalf("agent_run status %q", status)
	}
	evs, _ := h.bus.Replay(ctx, 0, events.Filter{AgentID: "a1"}, 0)
	types := map[string]int{}
	for _, e := range evs {
		types[e.Type]++
	}
	for _, want := range []string{"agent.started", "agent.output", "agent.completed", "agent.stopped"} {
		if types[want] == 0 {
			t.Fatalf("no %s event: %v", want, types)
		}
	}
}

func TestInterruptKeepsSessionAlive(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "starting"}, {Block: true}}, {{Say: "alive"}}}})
	ctx := context.Background()
	m, err := h.rt.Spawn(ctx, h.config())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(ctx)
	ctl := newTestController(m)
	done := make(chan struct{})
	var res rt.TurnResult
	var terr error
	go func() {
		res, terr = m.RunTurn(ctx, "go", ctl.c, nil)
		close(done)
	}()
	time.Sleep(500 * time.Millisecond)
	if err := ctl.c.CancelTurn(); err != nil {
		t.Fatal(err)
	}
	ctl.c.Send(ctx, "carry on")
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("turn did not finish after interrupt + nudge")
	}
	if terr != nil || res.Text != "alive" || res.StopReason != "end_turn" {
		t.Fatalf("res=%+v err=%v", res, terr)
	}
	if p := prompts(t, h.dir); len(p) != 2 || p[1] != "carry on" {
		t.Fatalf("prompts %v", p)
	}
}

func TestAgentCrashIsReported(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "about to crash"}, {Exit: 3}}}})
	ctx := context.Background()
	m, err := h.rt.Spawn(ctx, h.config())
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.RunTurn(ctx, "go", nil, nil)
	if err == nil {
		t.Fatal("a crash mid-turn must surface as an error")
	}
	h.settle(t)
	var status string
	var code int
	h.db.Reader().QueryRow(`SELECT status, exit_code FROM agent_runs WHERE id=?`, m.RunID).Scan(&status, &code)
	if status != "FAILED" || code != 3 {
		t.Fatalf("run status=%s code=%d", status, code)
	}
	evs, _ := h.bus.Replay(ctx, 0, events.Filter{Types: []string{"agent.failed"}}, 0)
	if len(evs) == 0 {
		t.Fatal("no agent.failed event")
	}
}

// Once Wait returns, the end of every stopped session is in the database and announced: no polling needed, and the
// database can be closed.
func TestWaitReturnsOnceTheEndsAreRecorded(t *testing.T) {
	h := newHarness(t, fakeagent.Script{})
	ctx := context.Background()
	var ms []*rt.Managed
	for i := 0; i < 2; i++ {
		m, err := h.rt.Spawn(ctx, h.config())
		if err != nil {
			t.Fatal(err)
		}
		defer m.Stop(ctx)
		ms = append(ms, m)
	}
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if err := h.rt.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait returned %v while both sessions were still running", err)
	}
	for _, m := range ms {
		var st string
		h.db.Reader().QueryRow(`SELECT status FROM agent_runs WHERE id=?`, m.RunID).Scan(&st)
		if st != "WORKING" {
			t.Fatalf("run %s is %q before it was stopped", m.RunID, st)
		}
		if err := m.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
	h.settle(t)
	stopped := map[string]bool{}
	evs, _ := h.bus.Replay(ctx, 0, events.Filter{Types: []string{"agent.stopped"}}, 0)
	for _, e := range evs {
		var d struct{ AgentRun string }
		json.Unmarshal(e.Data, &d)
		stopped[d.AgentRun] = true
	}
	for _, m := range ms {
		var st string
		var ended int64
		h.db.Reader().QueryRow(`SELECT status, ended_at FROM agent_runs WHERE id=?`, m.RunID).Scan(&st, &ended)
		if st != "STOPPED" || ended == 0 || !stopped[m.RunID] {
			t.Fatalf("run %s right after Wait: status %q, ended_at %d, agent.stopped %v", m.RunID, st, ended, stopped[m.RunID])
		}
	}
}

func TestMissingExecutableFailsSpawn(t *testing.T) {
	h := newHarness(t, fakeagent.Script{})
	h.ad.Exe = filepath.Join(h.dir, "does-not-exist")
	if _, err := h.rt.Spawn(context.Background(), h.config()); err == nil {
		t.Fatal("expected spawn error")
	}
	evs, _ := h.bus.Replay(context.Background(), 0, events.Filter{Types: []string{"agent.failed"}}, 0)
	if len(evs) != 1 {
		t.Fatalf("want one agent.failed, got %d", len(evs))
	}
}

type testCtl struct{ c *rt.Controller }

func newTestController(m *rt.Managed) testCtl { return testCtl{c: rt.NewControllerForTest(m)} }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Runs a crash left open are closed when the app starts: FAILED with the reason, announced for presence, and an agent that
// is not running any more can be removed. Runs that ended stay as they were.
func TestCloseStaleEndsTheRunsACrashLeftOpen(t *testing.T) {
	h := newHarness(t, fakeagent.Script{})
	for _, q := range []string{
		`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at,ended_at) VALUES('arun_open','a1','','WORKING',1,0)`,
		`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at,ended_at) VALUES('arun_done','a1','','STOPPED',1,2)`,
	} {
		if _, err := h.db.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ch, cancel := h.bus.Subscribe(events.Filter{Types: []string{"agent.failed"}})
	defer cancel()
	n, err := h.rt.CloseStale(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("closed %d %v", n, err)
	}
	var status, msg string
	var ended int64
	h.db.Reader().QueryRow(`SELECT status, error, ended_at FROM agent_runs WHERE id='arun_open'`).Scan(&status, &msg, &ended)
	if status != "FAILED" || ended == 0 || !strings.Contains(msg, "the app stopped") {
		t.Fatalf("open run: %s %q %d", status, msg, ended)
	}
	h.db.Reader().QueryRow(`SELECT status FROM agent_runs WHERE id='arun_done'`).Scan(&status)
	if status != "STOPPED" {
		t.Fatalf("an ended run was changed to %s", status)
	}
	select {
	case e := <-ch:
		if e.AgentID != "a1" || e.ProjectID != "p1" || !strings.Contains(string(e.Data), "the app stopped") {
			t.Fatalf("event %+v %s", e, e.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no agent.failed event for presence")
	}
	if n, _ := h.rt.CloseStale(context.Background()); n != 0 {
		t.Fatalf("a second start closed %d more", n)
	}
}

// The budgets read the usage table. A turn whose usage could not be written used to leave no trace at all, and the money it
// spent was missing from every budget (audit 2026-10-16, M4): it is published, with what the turn used.
func TestUsageThatCannotBeRecordedIsReportedInsteadOfLost(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Turns: [][]fakeagent.Step{{{Say: "done"}}}})
	ctx := context.Background()
	if _, err := h.db.Writer().Exec(`ALTER TABLE model_usage RENAME TO model_usage_gone`); err != nil {
		t.Fatal(err)
	}
	m, err := h.rt.Spawn(ctx, h.config())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RunTurn(ctx, "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	h.settle(t)
	evs, err := h.bus.Replay(ctx, 0, events.Filter{Types: []string{"agent.usage_unrecorded"}}, 0)
	if err != nil || len(evs) != 1 {
		t.Fatalf("%v: %d events", err, len(evs))
	}
	var d struct {
		Error        string
		InputTokens  int
		OutputTokens int
	}
	if err := json.Unmarshal(evs[0].Data, &d); err != nil || d.Error == "" || d.InputTokens != 10 || d.OutputTokens != 5 {
		t.Fatalf("%s", evs[0].Data)
	}
}

// P90: a turn can carry pictures beside its prompt. Claude Code's stream-json input takes them as image blocks after the text
// (what the scripted stand-in, a labelled test double, reads back); a session that cannot take pictures refuses the turn
// before anything is sent.
func TestATurnCarriesPicturesToASessionThatTakesThem(t *testing.T) {
	h := newHarness(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "seen"}}})
	ctx := context.Background()
	m, err := h.rt.Spawn(ctx, h.config())
	if err != nil {
		t.Fatal(err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...)
	if _, err := m.RunTurnImages(ctx, "what is this?", []agents.Image{{MIME: "image/png", Data: png}, {MIME: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if pr := prompts(t, h.dir); len(pr) != 1 || pr[0] != "what is this?\n[image image/png, 48 bytes]\n[image image/jpeg, 3 bytes]" {
		t.Fatalf("what the CLI read: %q", pr)
	}
	// a session that is not an agents.ImageSender is refused the pictures, and nothing reaches it
	m.Sess = textOnly{m.Sess}
	if _, err := m.RunTurnImages(ctx, "again", []agents.Image{{MIME: "image/png", Data: png}}, nil, nil); err == nil || !strings.Contains(err.Error(), "does not take pictures") || !errors.Is(err, rt.ErrNotStarted) {
		t.Fatalf("err %v", err)
	}
	if pr := prompts(t, h.dir); len(pr) != 1 {
		t.Fatalf("a refused turn sent something: %q", pr)
	}
	_ = m.Stop(ctx)
}

// textOnly hides a session's ImageSender (a labelled test double of a CLI that takes text only).
type textOnly struct{ agents.Session }
