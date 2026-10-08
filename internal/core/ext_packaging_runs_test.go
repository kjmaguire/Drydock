package core_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/license"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

func writeScript(t *testing.T, path string, s fakeagent.Script) {
	t.Helper()
	b, _ := json.Marshal(s)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

var (
	normalScript = fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "implemented Subtract"}},
	}
	// the worker blocks until it is interrupted: a run that is in the middle of an agent turn
	blockingScript = fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "independent reviewer", Steps: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}}},
		Default: []fakeagent.Step{{Block: true}},
	}
)

func openWithScript(t *testing.T, dataDir string) (*app, string) {
	t.Helper()
	sp := filepath.Join(t.TempDir(), "script.json")
	writeScript(t, sp, normalScript)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	return openAt(t, dataDir), sp
}

func (a *app) taskDetail(id string) core.TaskDetail {
	var d core.TaskDetail
	a.call("task.get", map[string]string{"id": id}, &d)
	return d
}

func (a *app) newTask(projectID, title, worker, reviewer string) tasks.Task {
	var tk tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: projectID, Title: title, Description: "make Subtract return a - b"}, &tk)
	a.call("task.assign", map[string]string{"id": tk.ID, "assignee": worker, "reviewer": reviewer}, &tk)
	return tk
}

func (a *app) activity() (runs, agents int) {
	r, ag, err := a.c.Activity(context.Background())
	if err != nil {
		a.t.Fatal(err)
	}
	return r, ag
}

// When the license expires: the run that is in the middle of an agent turn is stopped cleanly and parked, a run waiting for
// the human survives, new work and approvals are refused, and after renewal everything resumes from its checkpoint.
func TestExpiryStopsRunningWorkflowsAndTheyResumeAfterRenewal(t *testing.T) {
	kit := newKit(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	a, script := openWithScript(t, dataDir)
	kit.attach(a.c)
	t0 := kit.clk.now()
	exp := t0.Add(24 * time.Hour)
	if _, err := a.importLicense(kit.token("paid", "lic-1", t0.Add(-time.Hour), exp, 0)); err != nil {
		t.Fatal(err)
	}

	p := gitProject(t, a, "gocalc")
	// Both tasks make the same change to the same file. This test is about the license, not about collisions, so the guard that
	// would make A wait for B (docs/parallel-work.md) is switched off.
	off := false
	a.call("projectdev.set", map[string]any{"projectId": p.ID, "settings": pipeline.DevSettings{MaxParallel: 2, Respond: true, CollisionGuard: &off}}, nil)
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer"}, &r)

	// run B reaches the human request and waits there
	tb := a.newTask(p.ID, "B: waiting for the human", w.ID, r.ID)
	var runB string
	a.call("task.start", map[string]string{"id": tb.ID}, &runB)
	var dB core.TaskDetail
	a.waitFor("run B waiting for the human", func() bool { dB = a.taskDetail(tb.ID); return len(dB.Pending) == 1 })

	// run A is in the middle of an agent turn: the worker blocks
	writeScript(t, script, blockingScript)
	ta := a.newTask(p.ID, "A: mid-turn", w.ID, r.ID)
	var runA string
	a.call("task.start", map[string]string{"id": ta.ID}, &runA)
	a.waitFor("a live agent process", func() bool { runs, agents := a.activity(); return runs >= 1 && agents >= 1 })
	cpBefore, err := a.c.Engine.Checkpoints(context.Background(), workflow.RunID(runA))
	if err != nil || len(cpBefore) == 0 {
		t.Fatalf("checkpoints before expiry: %v %v", cpBefore, err)
	}
	tc := a.newTask(p.ID, "C: never starts", w.ID, r.ID) // creating and assigning is planning, not work

	// ---- the license expires ----
	kit.clk.set(exp.Add(2 * 24 * time.Hour))
	a.c.TestReconcileLicense(context.Background())
	if v := a.status(); v.State != license.StateExpired || !v.ReadOnly {
		t.Fatalf("status %+v", v)
	}
	a.waitFor("the mid-turn run to be parked and its agent stopped", func() bool {
		d := a.taskDetail(ta.ID)
		_, agents := a.activity()
		return d.Run != nil && d.Run.Status == workflow.StatusPaused && agents == 0
	})
	if runs, agents := a.activity(); runs != 0 || agents != 0 {
		t.Fatalf("work is still running while read-only: %d runs, %d agents", runs, agents)
	}
	dA := a.taskDetail(ta.ID)
	if dA.Task.RunID != runA || dA.Run.ID != workflow.RunID(runA) {
		t.Fatalf("the run changed: %+v", dA.Run)
	}
	cpAfter, _ := a.c.Engine.Checkpoints(context.Background(), workflow.RunID(runA))
	if len(cpAfter) < len(cpBefore) {
		t.Fatalf("checkpoints were lost: %d -> %d", len(cpBefore), len(cpAfter))
	}
	evs, _ := a.c.Replay(context.Background(), 0, events.Filter{Types: []string{"license.runs_suspended", "workflow.paused"}}, 50)
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.Type] = true
	}
	a.waitFor("suspension events", func() bool {
		evs, _ := a.c.Replay(context.Background(), 0, events.Filter{Types: []string{"license.runs_suspended", "workflow.paused"}}, 50)
		seen = map[string]bool{}
		for _, e := range evs {
			seen[e.Type] = true
		}
		return seen["license.runs_suspended"] && seen["workflow.paused"]
	})
	// the run waiting for the human is untouched, its request still pending
	dB = a.taskDetail(tb.ID)
	if dB.Run == nil || dB.Run.Status != workflow.StatusWaitingHuman || len(dB.Pending) != 1 || dB.Task.Status != tasks.Verification {
		t.Fatalf("run B after expiry: %+v pending=%d task=%s", dB.Run, len(dB.Pending), dB.Task.Status)
	}
	// refused: starting more work and approving the waiting request
	a.mustBeReadOnlyError("starting task C", a.callErr("task.start", map[string]string{"id": tc.ID}))
	a.mustBeReadOnlyError("approving the merge of B", a.callErr("run.respond", map[string]any{"runId": runB, "requestId": dB.Pending[0].ID, "decision": "approved", "data": map[string]string{}}))
	if d := a.taskDetail(tb.ID); len(d.Pending) != 1 || d.Task.Status != tasks.Verification {
		t.Fatalf("the refused approval changed run B: %+v", d.Task.Status)
	}
	// reading, cancelling and exporting still work
	var diff string
	a.call("task.diff", map[string]string{"id": tb.ID}, &diff)
	if !strings.Contains(diff, "return a - b") {
		t.Fatalf("diff of B is not readable while read-only:\n%s", diff)
	}

	// ---- restart while expired: nothing resumes by itself ----
	a.c.Close()
	core.LicensePublicKeys = "k1=" + base64.StdEncoding.EncodeToString(kit.keys["k1"])
	t.Cleanup(func() { core.LicensePublicKeys = "" })
	b := openAt(t, dataDir) // real clock, but the latest time Drydock has seen is persisted
	if v := b.status(); v.State != license.StateExpired || !v.Configured {
		t.Fatalf("after restart: %+v", v)
	}
	if d := b.taskDetail(ta.ID); d.Run == nil || d.Run.Status != workflow.StatusPaused {
		t.Fatalf("run A after restart: %+v", d.Run)
	}
	if runs, agents := b.activity(); runs != 0 || agents != 0 {
		t.Fatalf("a restart while expired resumed work: %d runs %d agents", runs, agents)
	}
	if d := b.taskDetail(tb.ID); d.Run == nil || d.Run.Status != workflow.StatusWaitingHuman || len(d.Pending) != 1 {
		t.Fatalf("run B after restart: %+v", d.Run)
	}
	b.mustBeReadOnlyError("task.start after restart", b.callErr("task.start", map[string]string{"id": tc.ID}))

	// ---- renewal ----
	writeScript(t, script, normalScript) // the resumed worker now finishes its turn
	kit.attach(b.c)
	kit.clk.set(time.Now().Add(3 * 24 * time.Hour)) // past the persisted time, inside the new license
	renewed := kit.token("paid", "lic-2", t0.Add(time.Hour), kit.clk.now().Add(90*24*time.Hour), 7)
	if v, err := b.importLicense(renewed); err != nil || v.State != license.StateActive {
		t.Fatalf("renewal: %+v %v", v, err)
	}
	// A resumes from its checkpoint (same run id) and reaches the human request
	b.waitFor("run A to resume and reach the human", func() bool {
		d := b.taskDetail(ta.ID)
		return d.Run != nil && d.Run.ID == workflow.RunID(runA) && len(d.Pending) == 1
	})
	// B's approval, refused while expired, now goes through and the task completes
	dB = b.taskDetail(tb.ID)
	b.call("run.respond", map[string]any{"runId": runB, "requestId": dB.Pending[0].ID, "decision": "approved", "data": map[string]string{}}, nil)
	b.waitFor("run B to complete", func() bool { return b.taskDetail(tb.ID).Task.Status == tasks.Completed })
	if got := b.taskDetail(tb.ID).Task.AcceptedBy; got != "human:kyle" {
		t.Fatalf("accepted by %q", got)
	}
	// C can start now
	var runC string
	b.call("task.start", map[string]string{"id": tc.ID}, &runC)
	if runC == "" {
		t.Fatal("task C did not start after renewal")
	}
	_ = b.callErr("task.cancel", map[string]string{"id": ta.ID})
}

// A run that is executing when the app starts with a read-only license is parked at start, before Recover could restart it.
func TestStartWhileExpiredParksOrphanedRunsInsteadOfRecoveringThem(t *testing.T) {
	kit := newKit(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	a, script := openWithScript(t, dataDir)
	p := gitProject(t, a, "gocalc")
	var w, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W", Adapter: "claude", Role: "backend"}, &w)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer"}, &r)
	writeScript(t, script, blockingScript)
	ta := a.newTask(p.ID, "A", w.ID, r.ID)
	var runA string
	a.call("task.start", map[string]string{"id": ta.ID}, &runA)
	a.waitFor("a live agent", func() bool { _, agents := a.activity(); return agents >= 1 })
	a.c.Close() // the run stays RUNNING in the database, as after a crash
	if st := mustRunStatus(t, dataDir, runA); st != "RUNNING" {
		t.Fatalf("expected an orphaned RUNNING run, have %s", st)
	}
	// the license has been expired for a long time as far as this installation has seen
	setSetting(t, dataDir, "license.maxseen", "4102444800") // year 2100
	core.LicensePublicKeys = "k1=" + base64.StdEncoding.EncodeToString(kit.keys["k1"])
	t.Cleanup(func() { core.LicensePublicKeys = "" })
	writeScript(t, script, normalScript)
	b := openAt(t, dataDir)
	time.Sleep(1500 * time.Millisecond) // give a wrongly started Recover time to show itself
	d := b.taskDetail(ta.ID)
	if d.Run == nil || d.Run.Status != workflow.StatusPaused {
		t.Fatalf("the orphaned run was not parked: %+v", d.Run)
	}
	if runs, agents := b.activity(); runs != 0 || agents != 0 {
		t.Fatalf("work restarted while read-only: %d %d", runs, agents)
	}
}

func mustRunStatus(t *testing.T, dataDir, runID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dataDir, "drydock.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var st string
	if err := db.QueryRow(`SELECT status FROM workflow_runs WHERE id=?`, runID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func setSetting(t *testing.T, dataDir, key, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dataDir, "drydock.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		t.Fatal(err)
	}
}

var _ = agents.Adapter(nil)
var _ = claude.Adapter{}

// Agent replies in the room are work started on the human's behalf: they stop while the license is read-only, posting does not.
func TestExpiredLicenseStopsAgentRepliesInTheRoomButNotPosting(t *testing.T) {
	kit := newKit(t)
	a, _ := openDev(t, devScript())
	kit.attach(a.c)
	t0 := kit.clk.now()
	exp := t0.Add(24 * time.Hour)
	if _, err := a.importLicense(kit.token("paid", "lic-1", t0.Add(-time.Hour), exp, 0)); err != nil {
		t.Fatal(err)
	}
	repo := newGreenRepo(t)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	alice := a.agent(p.ID, "Alice", "backend")
	var rooms []room.Room
	a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
	general := ""
	for _, r := range rooms {
		if r.Name == "general" {
			general = r.ID
		}
	}
	chatFrom := func(author string) int {
		var ms []room.Message
		a.call("room.messages", map[string]any{"roomId": general, "limit": 200}, &ms)
		n := 0
		for _, m := range ms {
			if m.Author.Key() == author && m.Type == room.TypeChat {
				n++
			}
		}
		return n
	}
	// licensed: the mention is answered
	a.call("room.post", map[string]any{"roomId": general, "body": "@" + alice.ID + " first question"}, nil)
	a.waitFor("a reply while licensed", func() bool { return chatFrom(alice.ID) == 1 })

	// expired: the human can still post, but nobody answers
	kit.clk.set(exp.Add(2 * 24 * time.Hour))
	a.c.TestReconcileLicense(context.Background())
	var posted room.Posted
	a.call("room.post", map[string]any{"roomId": general, "body": "@" + alice.ID + " second question"}, &posted)
	if posted.Message.ID == "" {
		t.Fatal("posting must stay allowed")
	}
	time.Sleep(2 * time.Second)
	if n := chatFrom(alice.ID); n != 1 {
		t.Fatalf("an agent answered while the license is read-only (%d replies)", n)
	}
	a.mustBeReadOnlyError("room.reply", a.callErr("room.reply", map[string]string{"messageId": posted.Message.ID}))
}
