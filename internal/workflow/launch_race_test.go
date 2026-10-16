package workflow_test

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// stallBus is the event bus of a busy machine. When an event of a chosen type is published, the database's single writer
// stalls for a while first (a long WAL checkpoint, a virus scanner holding the file: Windows CI does this at times), so that
// publish and every other write queue behind it, and all of them go on together once it ends.
type stallBus struct {
	events.Bus
	db    *storage.DB
	mu    sync.Mutex
	stall map[string]time.Duration
}

func (b *stallBus) stallOn(typ string, d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stall == nil {
		b.stall = map[string]time.Duration{}
	}
	b.stall[typ] = d
}

func (b *stallBus) Publish(ctx context.Context, e events.Event) (uint64, error) {
	b.mu.Lock()
	d := b.stall[e.Type]
	delete(b.stall, e.Type) // once
	b.mu.Unlock()
	if d > 0 {
		began := make(chan struct{})
		go func() {
			_ = b.db.WriteTx(context.Background(), func(*sql.Tx) error {
				close(began)
				time.Sleep(d)
				return nil
			})
		}()
		<-began
	}
	return b.Bus.Publish(ctx, e)
}

// raceEnv is an engine with a short lease whose recovery watch runs all the time, as core.Open runs it (every third of the
// lease), so a run the watch can see as abandoned is taken over at once.
func raceEnv(t *testing.T) (*env, *stallBus) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	sqlBus := events.NewSQLBus(db, 4096)
	bus := &stallBus{Bus: sqlBus, db: db}
	eng := wf.New(wf.Config{DB: db, Bus: bus, DataDir: dir, LeaseTTL: leaseTTL})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); eng.WatchRecover(ctx, 10*time.Millisecond) }()
	t.Cleanup(func() { cancel(); <-done; eng.Close(); sqlBus.Close(); db.Close() })
	return &env{dir: dir, db: db, bus: sqlBus, eng: eng}, bus
}

// counted is a node that counts its executions and how many run at once.
type counted struct {
	runs, now, most atomic.Int32
}

func (c *counted) handler(name string, hold time.Duration) wf.NodeHandler {
	return func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		c.runs.Add(1)
		n := c.now.Add(1)
		for {
			m := c.most.Load()
			if n <= m || c.most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(hold) // an agent turn takes a moment
		c.now.Add(-1)
		return wf.NodeOutput{Value: val(name)}, nil
	}
}

// A run has exactly one driver from the moment Start creates it, however long the writes before its driver starts take.
// Before, Start registered the driver only after publishing workflow.started and the entry checkpoint's event: when the
// database's writer stalled past the lease (3 s in the e2e harness; Windows CI stalls that long at times), the recovery watch
// saw a RUNNING run with an expired lease and no local driver, took it over and started a second driver, and once the writer
// was free both ran the run's first agent turn at the same time. A run driven twice pays for every turn twice and can fail on
// the duplicate (both drivers recording the same attempt, or adding the same task worktree); the UI then waits for a plan or a
// review that never comes, as round3 PlanTeam and round2 did on Windows CI.
func TestStartDrivesTheRunOnceWhenTheDatabaseStallsPastTheLease(t *testing.T) {
	e, bus := raceEnv(t)
	var a, b counted
	g := wf.NewGraph("slowstart").Add(wf.AgentTask("A", a.handler("A", 4*leaseTTL)), wf.AgentTask("B", b.handler("B", 0))).Edge("A", "B").Start("A")
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	bus.stallOn("workflow.started", 3*leaseTTL) // Start publishes it once it holds the lease
	id, err := e.eng.Start(context.Background(), "slowstart", val("go"), wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	info := waitStatus(t, e.eng, id, wf.StatusCompleted)
	if info.Status != wf.StatusCompleted {
		t.Fatalf("run %s: %s", info.Status, info.FailedReason)
	}
	if a.runs.Load() != 1 || a.most.Load() != 1 || b.runs.Load() != 1 {
		t.Fatalf("A ran %d times (%d at once), B %d times: the run had more than one driver", a.runs.Load(), a.most.Load(), b.runs.Load())
	}
}

// The same holds when a human's answer resumes a run: Resume takes the lease, records the answer (the run is RUNNING again)
// and publishes four events before the driver starts; a writer stall meanwhile must not let the recovery watch start a
// second driver.
func TestResumeDrivesTheRunOnceWhenTheDatabaseStallsPastTheLease(t *testing.T) {
	e, bus := raceEnv(t)
	var a, b counted
	g := wf.NewGraph("slowresume").
		Add(wf.AgentTask("A", a.handler("A", 0)), wf.Request("R", wf.RequestSpec{Kind: "approval", Title: "go on?"}), wf.AgentTask("B", b.handler("B", 4*leaseTTL))).
		Edge("A", "R").EdgeWhen("R", "B", "approved").Start("A")
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	id, err := e.eng.Start(context.Background(), "slowresume", val("go"), wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, id, wf.StatusWaitingHuman)
	pend, err := e.eng.Pending(context.Background(), id)
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending %v %v", pend, err)
	}
	bus.stallOn("workflow.request_answered", 3*leaseTTL) // Resume publishes it once the answer is recorded
	if err := e.eng.Resume(context.Background(), id, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	info := waitStatus(t, e.eng, id, wf.StatusCompleted)
	if info.Status != wf.StatusCompleted {
		t.Fatalf("run %s: %s", info.Status, info.FailedReason)
	}
	if a.runs.Load() != 1 || b.runs.Load() != 1 || b.most.Load() != 1 {
		t.Fatalf("A ran %d times, B %d times (%d at once): the run had more than one driver", a.runs.Load(), b.runs.Load(), b.most.Load())
	}
}
