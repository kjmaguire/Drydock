package workflow_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// A loop that exhausts into a human; the human's "continue" goes through a node that resets the loop counters, so the loop
// gets its whole budget again instead of exhausting on the next failure.
func TestResetLoopsGivesTheLoopItsBudgetAgain(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("reset").
		Add(wf.AgentTask("work", eff(dir, "work", "")), wf.Check("check", eff(dir, "check", "fail")),
			wf.Request("limit", wf.RequestSpec{Kind: "question", Title: "limit"}),
			wf.Route("resume", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
				effect(dir, "resume")
				return wf.NodeOutput{Value: val("again"), ResetLoops: true}, nil
			})).
		Edge("work", "check").Loop("check", "work", "fail", 2, "limit").
		EdgeWhen("limit", "resume", "approved").Edge("resume", "work").Start("work")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "reset", "in", wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	if got := effects(dir); got["work"] != 3 {
		t.Fatalf("first round: work ran %d times, want 3 (initial + 2 loops)", got["work"])
	}
	time.Sleep(50 * time.Millisecond)
	pend, _ := e.eng.Pending(context.Background(), run)
	if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:t"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	// without the reset the very next failure would exhaust again (work 4); with it the loop runs its full 2 extra rounds
	if got := effects(dir); got["work"] != 6 || got["resume"] != 1 {
		t.Fatalf("after the reset: work=%d resume=%d, want 6 and 1", got["work"], got["resume"])
	}
}

// Halt records a node's output and routes it nowhere, even past an unconditional edge: the run completes.
func TestHaltEndsTheBranchEvenWithAnUnconditionalEdge(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	g := wf.NewGraph("halt").
		Add(wf.Route("stop", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			return wf.NodeOutput{Value: val("stopped"), Halt: true}, nil
		}), wf.Route("after", eff(dir, "after", ""))).
		Edge("stop", "after").Start("stop")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "halt", nil, wf.StartOptions{})
	info := waitStatus(t, e.eng, run, wf.StatusCompleted)
	if effects(dir)["after"] != 0 {
		t.Fatal("a halted node must not route to its edges")
	}
	var res map[string]json.RawMessage
	_ = json.Unmarshal(info.Result, &res)
	if string(res["stop"]) != `"stopped"` {
		t.Fatalf("the halted node's output is the result: %s", info.Result)
	}
}

// A Retry request with DeliverAnswer runs the node again with its original inbox plus the human's answer.
func TestDeliverAnswerAppendsTheHumanAnswerToTheRestoredInbox(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	var calls atomic.Int32
	var seen atomic.Value
	turn := func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		if calls.Add(1) == 1 {
			return wf.NodeOutput{Request: &wf.RequestSpec{Kind: "question", Title: "paused", Retry: true, DeliverAnswer: true}}, nil
		}
		seen.Store(in.Inbox)
		return wf.NodeOutput{Value: val("done")}, nil
	}
	g := wf.NewGraph("deliver").
		Add(wf.Route("start", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			return wf.NodeOutput{Value: val("original")}, nil
		}), wf.AgentTask("turn", turn)).
		Edge("start", "turn").Start("start")
	e.eng.Register(g)
	run, _ := e.eng.Start(context.Background(), "deliver", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	time.Sleep(50 * time.Millisecond)
	pend, _ := e.eng.Pending(context.Background(), run)
	if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "answered", By: "human:t", Data: json.RawMessage(`{"text":"try the cache"}`)}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	inbox, _ := seen.Load().([]wf.Message)
	if len(inbox) != 2 || string(inbox[0].Value) != `"original"` {
		t.Fatalf("the rerun must see the original inbox first: %+v", inbox)
	}
	var ans struct {
		Decision string          `json:"decision"`
		By       string          `json:"by"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(inbox[1].Value, &ans); err != nil || ans.Decision != "answered" || ans.By != "human:t" || string(ans.Data) != `{"text":"try the cache"}` {
		t.Fatalf("the answer must be the last message: %s", inbox[1].Value)
	}
}

// statusAtBus reads the run's status at the moment a given event is published (the driver publishes synchronously).
type statusAtBus struct {
	events.Bus
	db   *storage.DB
	typ  string
	seen chan string
}

func (b *statusAtBus) Publish(ctx context.Context, e events.Event) (uint64, error) {
	if e.Type == b.typ {
		var st string
		_ = b.db.Reader().QueryRowContext(ctx, `SELECT status FROM workflow_runs WHERE id=?`, e.RunID).Scan(&st)
		select {
		case b.seen <- st:
		default:
		}
	}
	return b.Bus.Publish(ctx, e)
}

// A step that raises a request and leaves nothing else to run parks the run in the same commit: by the time the request is
// announced the run is already WAITING_HUMAN. Parking with a second write after it left a window where that write could fail
// and leave the run RUNNING with nobody driving it (seen once on a loaded Windows runner).
func TestAStepThatRaisesTheOnlyWorkLeftParksTheRunInItsCommit(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 4096)
	spy := &statusAtBus{Bus: bus, db: db, typ: "workflow.request_raised", seen: make(chan string, 1)}
	eng := wf.New(wf.Config{DB: db, Bus: spy, DataDir: dir, LeaseTTL: leaseTTL})
	t.Cleanup(func() { eng.Close(); bus.Close(); db.Close() })
	g := wf.NewGraph("park").
		Add(wf.AgentTask("work", eff(dir, "work", "")), wf.Request("ask", wf.RequestSpec{Kind: "question", Title: "ask"})).
		Edge("work", "ask").Start("work")
	eng.Register(g)
	run, _ := eng.Start(context.Background(), "park", "in", wf.StartOptions{})
	waitStatus(t, eng, run, wf.StatusWaitingHuman)
	select {
	case st := <-spy.seen:
		if st != string(wf.StatusWaitingHuman) {
			t.Fatalf("status when the request was announced: %s, want WAITING_HUMAN", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no workflow.request_raised")
	}
}
