package core_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

type appInfoStart struct {
	StartReport core.StartReport `json:"startReport"`
}

// After a stop that did not close the core (its marker left behind, as a crash or a kill leaves it), app.info says so once, with
// the runs the start resumed and the ones it could not resume, their tasks named; after a clean close it says nothing (X511).
func TestTheStartReportSaysWhatTheLastStopLeftAndWhatWasResumed(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a, script := openWithScript(t, dataDir)
	var first appInfoStart
	a.call("app.info", struct{}{}, &first)
	if first.StartReport.Unexpected || len(first.StartReport.Resumed) != 0 || first.StartReport.StartedAt == 0 {
		t.Fatalf("a first start: %+v", first.StartReport)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "drydock.running")); err != nil {
		t.Fatalf("no marker while the core is open: %v", err)
	}
	p := gitProject(t, a, "gocalc")
	var w1, w2, r core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W1", Adapter: "claude", Role: "backend"}, &w1)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "W2", Adapter: "claude", Role: "backend"}, &w2)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "R", Adapter: "claude", Role: "reviewer"}, &r)
	writeScript(t, script, blockingScript)
	ta := a.newTask(p.ID, "Resume me", w1.ID, r.ID)
	tb := a.newTask(p.ID, "Cannot resume me", w2.ID, r.ID)
	var runA, runB string
	a.call("task.start", map[string]string{"id": ta.ID}, &runA)
	a.call("task.start", map[string]string{"id": tb.ID}, &runB)
	a.waitFor("two live agents", func() bool { _, agents := a.activity(); return agents >= 2 })
	a.c.Close() // the runs stay RUNNING in the database
	if _, err := os.Stat(filepath.Join(dataDir, "drydock.running")); !os.IsNotExist(err) {
		t.Fatalf("Close left the marker: %v", err)
	}
	// as a crash leaves it: the marker is still there; and run B's checkpoint is gone, so it cannot be resumed
	if err := os.WriteFile(filepath.Join(dataDir, "drydock.running"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", rawDSN(dataDir, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workflow_runs SET latest_checkpoint_id='cp_gone' WHERE id=?`, runB); err != nil {
		t.Fatal(err)
	}
	db.Close()
	writeScript(t, script, normalScript)

	b := openAt(t, dataDir)
	var info appInfoStart
	b.call("app.info", struct{}{}, &info)
	rep := info.StartReport
	if !rep.Unexpected {
		t.Fatalf("the left marker is not said: %+v", rep)
	}
	if len(rep.Resumed) != 1 || rep.Resumed[0].RunID != runA || rep.Resumed[0].TaskID != ta.ID || rep.Resumed[0].Title != "Resume me" || rep.Resumed[0].ProjectID != p.ID {
		t.Fatalf("resumed: %+v", rep.Resumed)
	}
	if len(rep.NotResumed) != 1 || rep.NotResumed[0].RunID != runB || rep.NotResumed[0].Title != "Cannot resume me" {
		t.Fatalf("not resumed: %+v", rep.NotResumed)
	}
	_ = b.callErr("task.cancel", map[string]string{"id": ta.ID})
	b.c.Close()

	// closed as it should: the next start has nothing to say
	c := openAt(t, dataDir)
	var again appInfoStart
	c.call("app.info", struct{}{}, &again)
	if again.StartReport.Unexpected || len(again.StartReport.NotResumed) != 0 || again.StartReport.Crashes != 0 {
		t.Fatalf("after a clean close: %+v", again.StartReport)
	}
}
