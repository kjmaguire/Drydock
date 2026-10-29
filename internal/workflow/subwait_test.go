package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// A subworkflow that waits for a person (decision P124): the parent parks with its nested run, shows the nested run's question
// as its own, goes on when the person answers in the nested run, and stops it when it is cancelled; both hold across a
// restart. The side effects are lines in effects.log, as in the acceptance tests.

// asksGraph is outer: A, then the subworkflow S (x, then the question q, then z), then B.
func asksGraph(dir string) *wf.Graph {
	inner := wf.Sequential("asks", wf.AgentTask("x", eff(dir, "x", "")), wf.Request("q", wf.RequestSpec{Kind: "question", Title: "which one?"}), wf.Check("z", eff(dir, "z", "")))
	return wf.Sequential("outer", wf.AgentTask("A", eff(dir, "A", "")), &wf.Node{ID: "S", Kind: wf.KindSubworkflow, Sub: inner}, wf.Check("B", eff(dir, "B", "")))
}

// parked waits for parent to wait on its nested run's question and returns the parent's request and the nested run.
func parked(t *testing.T, eng *wf.Engine, parent wf.RunID) (wf.PendingRequest, wf.RunID) {
	t.Helper()
	waitStatus(t, eng, parent, wf.StatusWaitingHuman)
	child := wf.SubRunID(parent, "S", 2, 1)
	waitStatus(t, eng, child, wf.StatusWaitingHuman)
	ps, err := eng.Pending(context.Background(), parent)
	if err != nil || len(ps) != 1 {
		t.Fatalf("pending %+v %v", ps, err)
	}
	p := ps[0]
	if p.Spec.Child != child || p.Spec.Kind != "question" || p.Spec.Title != "Waiting on asks's question: which one?" || p.Spec.ChildRequest == "" {
		t.Fatalf("the parent's request %+v", p.Spec)
	}
	return p, child
}

func TestAParentWaitsOnItsSubworkflowsQuestionAndGoesOnWithTheAnswer(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	if err := e.eng.Register(asksGraph(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('prj1','p','/x',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Writer().Exec(`INSERT INTO tasks(id,project_id,seq,title,status,created_at,updated_at) VALUES('t1','prj1',1,'t','IN_PROGRESS',1,1)`); err != nil {
		t.Fatal(err)
	}
	run, err := e.eng.Start(ctx, "outer", "in", wf.StartOptions{ProjectID: "prj1", TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	p, child := parked(t, e.eng, run)
	// only the nested run's request is a row (the Inbox's item), in the parent's project and task; the nested run has no task
	open, _ := e.eng.Approvals(ctx, "REQUESTED")
	if len(open) != 1 || open[0].RunID != child || open[0].ID != p.Spec.ChildRequest || open[0].TaskID != "t1" {
		t.Fatalf("open requests %+v", open)
	}
	ci, _ := e.eng.Status(ctx, child)
	if ci.ParentRun != run || ci.TaskID != "" || ci.ProjectID != "prj1" {
		t.Fatalf("the nested run %+v", ci)
	}
	if got := effects(dir); got["B"] != 0 || got["z"] != 0 {
		t.Fatalf("nothing after the question ran: %v", got)
	}
	// the person answers through the parent's request: the answer is the nested run's, and both go on
	if err := e.eng.Resume(ctx, run, wf.Response{RequestID: p.ID, Decision: "answered", Data: []byte(`{"text":"the second"}`), By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if got := effects(dir); got["A"] != 1 || got["x"] != 1 || got["z"] != 1 || got["B"] != 1 {
		t.Fatalf("effects %v", got)
	}
	var by string
	e.db.Reader().QueryRow(`SELECT decided_by FROM approvals WHERE id=?`, string(p.Spec.ChildRequest)).Scan(&by)
	if by != "human:test" {
		t.Fatalf("the answer is recorded on the nested run's request: %q", by)
	}
	if ci, _ := e.eng.Status(ctx, child); ci.Status != wf.StatusCompleted {
		t.Fatalf("nested %s", ci.Status)
	}
	if kinds(t, e.eng, run)[len(kinds(t, e.eng, run))-1] != "step" {
		t.Fatalf("checkpoints %v", kinds(t, e.eng, run))
	}

	// answered in the nested run directly, the parent goes on just the same
	run2, _ := e.eng.Start(ctx, "outer", "in", wf.StartOptions{ProjectID: "prj1"})
	p2, child2 := parked(t, e.eng, run2)
	if err := e.eng.Resume(ctx, child2, wf.Response{RequestID: p2.Spec.ChildRequest, Decision: "answered", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run2, wf.StatusCompleted)
}

func TestCancellingTheParentCancelsTheSubworkflowItWaitsOn(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	if err := e.eng.Register(asksGraph(dir)); err != nil {
		t.Fatal(err)
	}
	run, _ := e.eng.Start(ctx, "outer", "in", wf.StartOptions{})
	_, child := parked(t, e.eng, run)
	if err := e.eng.Cancel(ctx, run); err != nil {
		t.Fatal(err)
	}
	if ci, _ := e.eng.Status(ctx, child); ci.Status != wf.StatusCancelled {
		t.Fatalf("the nested run is %s", ci.Status)
	}
	if open, _ := e.eng.Approvals(ctx, "REQUESTED"); len(open) != 0 {
		t.Fatalf("no request is left open: %+v", open)
	}

	// the nested run cancelled on its own fails the parent's step; resumed, the step runs again with a new nested run
	run2, _ := e.eng.Start(ctx, "outer", "in", wf.StartOptions{})
	_, child2 := parked(t, e.eng, run2)
	if err := e.eng.Cancel(ctx, child2); err != nil {
		t.Fatal(err)
	}
	failed := waitStatus(t, e.eng, run2, wf.StatusFailed)
	if !strings.Contains(failed.FailedReason, "subworkflow asks ended CANCELLED") {
		t.Fatalf("reason %q", failed.FailedReason)
	}
	if err := e.eng.Continue(ctx, run2, "human:test"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run2, wf.StatusWaitingHuman)
	ps, _ := e.eng.Pending(ctx, run2)
	if len(ps) != 1 || ps[0].Spec.Child == child2 || ps[0].Spec.Child == "" {
		t.Fatalf("a new nested run: %+v", ps)
	}
}

// restart is a Drydock that opens the same data again.
func restart(t *testing.T, dir string, g *wf.Graph) (*wf.Engine, *storage.DB, func()) {
	t.Helper()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 4096)
	eng := wf.New(wf.Config{DB: db, Bus: bus, DataDir: dir, LeaseTTL: leaseTTL})
	if g != nil {
		if err := eng.Register(g); err != nil {
			t.Fatal(err)
		}
	}
	return eng, db, func() { eng.Close(); bus.Close(); db.Close() }
}

// Both runs wait across a restart; the answer given to the next Drydock takes both on. A parent whose nested run ended while
// no Drydock could wake it (the nested run went on in a Drydock that does not know the parent's workflow) is taken up by
// recovery.
func TestASubworkflowWaitHoldsAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	g := asksGraph(dir)
	eng, _, stop := restart(t, dir, g)
	run, err := eng.Start(context.Background(), "outer", "in", wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := parked(t, eng, run)
	stop()

	eng2, _, stop2 := restart(t, dir, g)
	if _, err := eng2.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := eng2.Resume(context.Background(), run, wf.Response{RequestID: p.ID, Decision: "answered", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, eng2, run, wf.StatusCompleted)
	if got := effects(dir); got["A"] != 1 || got["x"] != 1 || got["B"] != 1 {
		t.Fatalf("effects %v", got)
	}
	run2, _ := eng2.Start(context.Background(), "outer", "in", wf.StartOptions{})
	p2, child2 := parked(t, eng2, run2)
	stop2()

	// a Drydock that knows only the nested workflow answers it: the nested run ends, nobody can take the parent on
	inner := g.Nodes["S"].Sub
	eng3, _, stop3 := restart(t, dir, inner)
	if err := eng3.Resume(context.Background(), child2, wf.Response{RequestID: p2.Spec.ChildRequest, Decision: "answered", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, eng3, child2, wf.StatusCompleted)
	time.Sleep(100 * time.Millisecond)
	if info, _ := eng3.Status(context.Background(), run2); info.Status != wf.StatusWaitingHuman {
		t.Fatalf("parent %s", info.Status)
	}
	stop3()

	eng4, _, stop4 := restart(t, dir, g)
	defer stop4()
	if _, err := eng4.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, eng4, run2, wf.StatusCompleted)
	if got := effects(dir); got["B"] != 2 {
		t.Fatalf("effects %v", got)
	}
}

// A parent whose driver stopped while its nested run worked takes that nested run up again when it is recovered, instead of
// starting a second one.
func TestARecoveredParentTakesUpItsRunningSubworkflow(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(dir, "go")
	inner := wf.Sequential("slow", wf.AgentTask("w", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		effect(dir, "w_start")
		for {
			if _, err := os.Stat(gate); err == nil {
				effect(dir, "w")
				return wf.NodeOutput{Value: val("w")}, nil
			}
			select {
			case <-ctx.Done():
				return wf.NodeOutput{}, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	g := wf.Sequential("outerslow", &wf.Node{ID: "S", Kind: wf.KindSubworkflow, Sub: inner}, wf.Check("B", eff(dir, "B", "")))
	eng, db, stop := restart(t, dir, g)
	run, _ := eng.Start(context.Background(), "outerslow", "in", wf.StartOptions{})
	deadline := time.Now().Add(10 * time.Second)
	for effects(dir)["w_start"] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the nested turn did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = db
	stop() // the app closes while the nested run works

	eng2, db2, stop2 := restart(t, dir, g)
	defer stop2()
	if _, err := eng2.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(gate, nil, 0o644)
	waitStatus(t, eng2, run, wf.StatusCompleted)
	var nested int
	db2.Reader().QueryRow(`SELECT COUNT(*) FROM workflow_runs WHERE parent_run_id=?`, string(run)).Scan(&nested)
	if nested != 1 {
		t.Fatalf("%d nested runs, want the one it started", nested)
	}
	if got := effects(dir); got["B"] != 1 || got["w"] != 1 {
		t.Fatalf("effects %v", got)
	}
}
