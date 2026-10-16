package workflow_test

import (
	"context"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// leaseEnv is an engine whose lease is long enough that its heartbeat never renews during a test, so a step can make its
// own lease run out (as a stalled machine or a laptop asleep would) and nothing renews it before the step commits.
func leaseEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 4096)
	eng := wf.New(wf.Config{DB: db, Bus: bus, DataDir: dir, LeaseTTL: time.Minute})
	t.Cleanup(func() { eng.Close(); bus.Close(); db.Close() })
	return &env{dir: dir, db: db, bus: bus, eng: eng}
}

// setLease rewrites the run's lease from inside a step.
func setLease(e *env, name, owner string, expires int64) wf.NodeHandler {
	return func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		effect(e.dir, name)
		if _, err := e.db.Writer().ExecContext(ctx, `UPDATE run_leases SET owner=?, expires_at=? WHERE run_id=?`, owner, expires, string(in.RunID)); err != nil {
			return wf.NodeOutput{}, err
		}
		return wf.NodeOutput{Value: val(name)}, nil
	}
}

// A step that outlived its lease (the driver's heartbeat was late: a busy machine, a laptop asleep) while nobody else took
// the lease still commits, and the run goes on. Before, the commit was refused as if another driver had taken over, and
// the driver stopped without a word, leaving the run RUNNING with nobody driving it.
func TestAStepThatOutlivedItsLeaseCommitsWhenNobodyTookIt(t *testing.T) {
	e := leaseEnv(t)
	g := wf.NewGraph("late").
		Add(wf.AgentTask("A", setLease(e, "A", e.eng.Owner(), 1)), wf.AgentTask("B", eff(e.dir, "B", ""))).
		Edge("A", "B").Start("A")
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	id, err := e.eng.Start(context.Background(), "late", val("go"), wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, id, wf.StatusCompleted)
	if got := effects(e.dir); got["A"] != 1 || got["B"] != 1 {
		t.Fatalf("effects %v", got)
	}
}

// A driver whose lease another owner took (it was stalled past its lease and another engine took the run over) is fenced
// out: its step is not committed and it drives no further.
func TestAStepWhoseLeaseAnotherOwnerTookIsNotCommitted(t *testing.T) {
	e := leaseEnv(t)
	later := time.Now().Add(time.Hour).UnixMilli()
	g := wf.NewGraph("taken").
		Add(wf.AgentTask("A", setLease(e, "A", "another-engine", later)), wf.AgentTask("B", eff(e.dir, "B", ""))).
		Edge("A", "B").Start("A")
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	id, err := e.eng.Start(context.Background(), "taken", val("go"), wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if effects(e.dir)["B"] != 0 {
			t.Fatal("the fenced-out driver went on to B")
		}
		time.Sleep(20 * time.Millisecond)
	}
	info, err := e.eng.Status(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	cps, err := e.eng.RunCheckpoints(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != wf.StatusRunning || len(cps) != 1 || effects(e.dir)["A"] != 1 {
		t.Fatalf("status %s, %d checkpoints, effects %v: the step must not be committed", info.Status, len(cps), effects(e.dir))
	}
	var owner string
	e.db.Reader().QueryRow(`SELECT owner FROM run_leases WHERE run_id=?`, string(id)).Scan(&owner)
	if owner != "another-engine" {
		t.Fatalf("the lease now belongs to %q", owner)
	}
}
