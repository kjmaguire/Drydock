package workflow_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

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
