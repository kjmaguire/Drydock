package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// Resume of a stopped run, time travel and replay (decision P123, docs/workflow-engine.md s6). The side effects are lines in
// effects.log, as in the acceptance tests.

// flakyOnce fails the first time it runs (a marker file says it has) and succeeds after.
func flakyOnce(dir, name string) wf.NodeHandler {
	return func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		effect(dir, name+"_try")
		mark := filepath.Join(dir, name+".failed")
		if _, err := os.Stat(mark); err != nil {
			_ = os.WriteFile(mark, nil, 0o644)
			return wf.NodeOutput{}, errors.New("the provider was overloaded")
		}
		effect(dir, name)
		return wf.NodeOutput{Value: val(name)}, nil
	}
}

func attempts(t *testing.T, e *wf.Engine, run wf.RunID, node wf.NodeID) []string {
	t.Helper()
	rows, err := e.Executions(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if r.NodeID == node {
			out = append(out, fmt.Sprintf("%d:%s", r.Attempt, r.Status))
		}
	}
	return out
}

func kinds(t *testing.T, e *wf.Engine, run wf.RunID) []string {
	t.Helper()
	cps, err := e.RunCheckpoints(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cps {
		out = append(out, string(c.Kind))
	}
	return out
}

// A failed run goes on from its last good checkpoint: the node that failed runs again with the next attempt, the nodes before
// it are not run again, and a "resumed" checkpoint records where it went on from.
func TestContinueResumesAFailedRunFromItsLastGoodCheckpoint(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	g := wf.Sequential("flaky", wf.AgentTask("A", eff(dir, "A", "")), wf.AgentTask("B", flakyOnce(dir, "B")), wf.AgentTask("C", eff(dir, "C", "")))
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	run, err := e.eng.Start(ctx, "flaky", "in", wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitStatus(t, e.eng, run, wf.StatusFailed)
	if !strings.Contains(failed.FailedReason, "overloaded") {
		t.Fatalf("reason %q", failed.FailedReason)
	}
	if err := e.eng.Continue(ctx, run, "human:test"); err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, e.eng, run, wf.StatusCompleted)
	if done.FailedReason != "" {
		t.Fatalf("a resumed run that completed keeps no failure reason: %q", done.FailedReason)
	}
	if got := effects(dir); got["A"] != 1 || got["B_try"] != 2 || got["B"] != 1 || got["C"] != 1 {
		t.Fatalf("effects %v: A must not run again, B runs a second attempt, C once", got)
	}
	if a := attempts(t, e.eng, run, "B"); strings.Join(a, ",") != "1:failed,2:done" {
		t.Fatalf("B attempts %v", a)
	}
	k := kinds(t, e.eng, run)
	if len(k) < 3 || k[0] != "entry" || k[2] != "entry" {
		t.Fatalf("checkpoint kinds %v: the resume is an entry on the timeline after step 1", k)
	}
	cps, _ := e.eng.RunCheckpoints(ctx, run)
	if c := cps[2]; c.Parent != cps[1].ID || c.Step != 1 {
		t.Fatalf("the resume's entry follows the last good checkpoint (step 1): %+v", c)
	}
	if err := e.eng.Continue(ctx, run, "human:test"); !errors.Is(err, wf.ErrRunTerminal) {
		t.Fatalf("a completed run is not resumed: %v", err)
	}
	evs, _ := e.bus.Replay(ctx, 0, eventsOfRun(run, "workflow.run_resumed"), 10)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"by":"human:test"`) || !strings.Contains(string(evs[0].Data), "overloaded") {
		t.Fatalf("the resume is announced with who and why the run had stopped: %+v", evs)
	}
}

// A run whose app was killed mid-turn is taken over on a person's word once the dead owner's lease has run out (and not before):
// the turn that was cut off runs again, once.
func TestContinueAfterAKillTakesTheRunOverOnce(t *testing.T) {
	dir := t.TempDir()
	child, run := startChild(t, "interrupt", dir)
	probe := openEnv(t, dir)
	if err := probe.eng.Register(interruptGraph(dir, false)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for effects(dir)["W_start"] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the child's turn did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := probe.eng.Continue(ctx, run, "human:test"); !errors.Is(err, wf.ErrLeaseHeld) {
		t.Fatalf("a run its owner still drives is not taken: %v", err)
	}
	kill(t, child)
	if err := probe.eng.Continue(ctx, run, "human:test"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, probe.eng, run, wf.StatusCompleted)
	if got := effects(dir); got["W_start"] != 2 || got["W_done"] != 1 {
		t.Fatalf("effects %v: the cut-off turn runs again exactly once", got)
	}
	if a := attempts(t, probe.eng, run, "W"); strings.Join(a, ",") != "1:abandoned,2:done" {
		t.Fatalf("W attempts %v", a)
	}
}

// Only a run that stopped is resumed: one waiting for a person is answered instead, a cancelled one stays cancelled.
func TestContinueRefusesARunThatHasNotStopped(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	if err := e.eng.Register(approvalGraph(dir)); err != nil {
		t.Fatal(err)
	}
	run, _ := e.eng.Start(ctx, "approval", "go", wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	if err := e.eng.Continue(ctx, run, "human:test"); !errors.Is(err, wf.ErrNotStopped) {
		t.Fatalf("a waiting run: %v", err)
	}
	if err := e.eng.Cancel(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := e.eng.Continue(ctx, run, "human:test"); !errors.Is(err, wf.ErrRunTerminal) {
		t.Fatalf("a cancelled run: %v", err)
	}
}

// gateErr is a labelled test double of a node error that waits for a person (the git configuration gate is the real one).
type gateErr struct{}

func (gateErr) Error() string { return "the configuration changed" }
func (gateErr) Gate() (string, string, map[string]any) {
	return "cfg", "The configuration changed: go on?", map[string]any{}
}

// Resuming keeps the run's approvals: a step a person refused fails again, and nothing asks them a second time.
func TestContinueKeepsARejectionARejection(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	g := wf.Sequential("gated", wf.AgentTask("A", eff(dir, "A", "")), wf.Check("G", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		effect(dir, "G_try")
		return wf.NodeOutput{}, gateErr{}
	}))
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	run, _ := e.eng.Start(ctx, "gated", "in", wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	pend, _ := e.eng.Pending(ctx, run)
	if err := e.eng.Resume(ctx, run, wf.Response{RequestID: pend[0].ID, Decision: "rejected", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	first := waitStatus(t, e.eng, run, wf.StatusFailed)
	if err := e.eng.Continue(ctx, run, "human:test"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	again := waitStatus(t, e.eng, run, wf.StatusFailed)
	if again.FailedReason != first.FailedReason {
		t.Fatalf("the same refusal ends it again: %q vs %q", again.FailedReason, first.FailedReason)
	}
	all, _ := e.eng.Approvals(ctx, "")
	if len(all) != 1 || all[0].Status != "REJECTED" {
		t.Fatalf("no new question, the rejection stands: %+v", all)
	}
	if got := effects(dir); got["A"] != 1 {
		t.Fatalf("effects %v", got)
	}
}

// snapshot is everything the engine stores about a run, for "not touched" comparisons.
func snapshot(t *testing.T, e *env, run wf.RunID) string {
	t.Helper()
	ctx := context.Background()
	cps, err := e.eng.RunCheckpoints(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := e.eng.Executions(ctx, run)
	var appr []string
	r, err := e.db.Reader().Query(`SELECT id, status, decision FROM approvals WHERE run_id=? ORDER BY id`, string(run))
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var id, st, dec string
		_ = r.Scan(&id, &st, &dec)
		appr = append(appr, id+":"+st+":"+dec)
	}
	r.Close()
	info, _ := e.eng.Status(ctx, run)
	b, _ := json.Marshal(map[string]any{"cps": cps, "rows": rows, "approvals": appr, "status": info.Status, "latest": info.LatestCP, "version": info.Version})
	return string(b)
}

// A fork from a checkpoint is a new run with the state as of that checkpoint: its open request is asked afresh in the new run
// (the parked node keeps its inbox), the nodes after the checkpoint run in the fork, and the original run (still waiting) is
// unchanged, byte for byte: checkpoints, node executions, approvals and status.
func TestTimeTravelForksAndKeepsTheOriginalRunUnchanged(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	// W asks (Retry + DeliverAnswer) until it is approved, and says how many messages it was given when it runs
	w := func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		for _, m := range in.Inbox {
			if strings.Contains(string(m.Value), `"decision":"approved"`) {
				effect(dir, fmt.Sprintf("W_with_%d", len(in.Inbox)))
				return wf.NodeOutput{Value: val("W")}, nil
			}
		}
		return wf.NodeOutput{Request: &wf.RequestSpec{Kind: "approval", Title: "go on?", Retry: true, DeliverAnswer: true}}, nil
	}
	g := wf.Sequential("fork", wf.AgentTask("A", eff(dir, "A", "")), wf.AgentTask("W", w), wf.AgentTask("B", eff(dir, "B", "")))
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	run, _ := e.eng.Start(ctx, "fork", "in", wf.StartOptions{ProjectID: "p1", TaskID: "t1"})
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
	time.Sleep(50 * time.Millisecond) // the driver releases its lease
	before := snapshot(t, e, run)
	cps, _ := e.eng.RunCheckpoints(ctx, run)
	waitingCP := cps[len(cps)-1] // after W asked
	fork, err := e.eng.TimeTravel(ctx, waitingCP.ID, "human:test")
	if err != nil {
		t.Fatal(err)
	}
	if fork == run {
		t.Fatal("time travel makes a new run")
	}
	waitStatus(t, e.eng, fork, wf.StatusWaitingHuman)
	fp, _ := e.eng.Pending(ctx, fork)
	op, _ := e.eng.Pending(ctx, run)
	if len(fp) != 1 || len(op) != 1 || fp[0].ID == op[0].ID || fp[0].NodeID != "W" {
		t.Fatalf("the fork asks afresh: fork %+v, original %+v", fp, op)
	}
	if len(fp[0].Inbox) != 1 {
		t.Fatalf("the parked node keeps its inbox in the fork: %+v", fp[0])
	}
	if err := e.eng.Resume(ctx, fork, wf.Response{RequestID: fp[0].ID, Decision: "approved", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, fork, wf.StatusCompleted)
	if got := effects(dir); got["A"] != 1 || got["W_with_2"] != 1 || got["B"] != 1 {
		t.Fatalf("effects %v: A ran once (before the checkpoint), W saw its inbox and the answer, B ran in the fork", got)
	}
	if after := snapshot(t, e, run); after != before {
		t.Fatalf("the original run changed:\n%s\n%s", before, after)
	}
	info, _ := e.eng.Status(ctx, fork)
	if info.ParentCP != waitingCP.ID || info.TaskID != "t1" || info.ProjectID != "p1" {
		t.Fatalf("the fork names where it came from and keeps the task: %+v", info)
	}
	fcps, _ := e.eng.RunCheckpoints(ctx, fork)
	o := fcps[0].State.Origin
	if o == nil || o.Kind != wf.OriginFork || o.Run != run || o.Checkpoint != waitingCP.ID || o.By != "human:test" {
		t.Fatalf("origin %+v", o)
	}
	if last := fcps[len(fcps)-1].State.Origin; last == nil || last.Kind != wf.OriginFork {
		t.Fatalf("the origin is carried to the fork's later checkpoints: %+v", last)
	}
	lin, _ := e.eng.Checkpoints(ctx, fork)
	if lin[0].RunID != run {
		t.Fatalf("the fork's lineage starts in the original run: %+v", lin[0])
	}
}

// A replay never runs a step that changes something outside the run without a fresh approval: the step asks first, a rejection
// ends the replay with the step not run, an approval lets it run once.
func TestReplayRefusesSideEffectsWithoutAFreshApproval(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	g := wf.Sequential("push", wf.AgentTask("A", eff(dir, "A", "")), wf.Route("P", eff(dir, "P", "")).Effect("pushes the branch"))
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	run, _ := e.eng.Start(ctx, "push", "in", wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if got := effects(dir); got["A"] != 1 || got["P"] != 1 {
		t.Fatalf("a live run pushes without asking: %v", got)
	}

	replay, err := e.eng.Replay(ctx, run, "", "human:test")
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, replay, wf.StatusWaitingHuman)
	pend, _ := e.eng.Pending(ctx, replay)
	if len(pend) != 1 || pend[0].NodeID != "P" || !strings.Contains(pend[0].Spec.Title, "replay") || !strings.Contains(pend[0].Spec.Title, "pushes the branch") {
		t.Fatalf("the push asks first: %+v", pend)
	}
	if got := effects(dir); got["A"] != 2 || got["P"] != 1 {
		t.Fatalf("A ran again, P did not: %v", got)
	}
	if err := e.eng.Resume(ctx, replay, wf.Response{RequestID: pend[0].ID, Decision: "rejected", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	info := waitStatus(t, e.eng, replay, wf.StatusFailed)
	if !strings.Contains(info.FailedReason, "chose not to run P") {
		t.Fatalf("reason %q", info.FailedReason)
	}
	if got := effects(dir); got["P"] != 1 {
		t.Fatalf("a rejected push does not run: %v", got)
	}

	second, err := e.eng.Replay(ctx, run, "", "human:test")
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, second, wf.StatusWaitingHuman)
	pend, _ = e.eng.Pending(ctx, second)
	if err := e.eng.Resume(ctx, second, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, second, wf.StatusCompleted)
	if got := effects(dir); got["P"] != 2 {
		t.Fatalf("an approved push runs once: %v", got)
	}
	all, _ := e.eng.Approvals(ctx, "CONSUMED")
	if len(all) != 1 || all[0].RunID != second {
		t.Fatalf("the approval is consumed by the push it let through: %+v", all)
	}
	cps, _ := e.eng.RunCheckpoints(ctx, second)
	if o := cps[0].State.Origin; o == nil || o.Kind != wf.OriginReplay || o.Run != run || cps[0].Parent != o.Checkpoint {
		t.Fatalf("origin %+v parent %s", o, cps[0].Parent)
	}

	// a run that has not finished is not replayed; a checkpoint from elsewhere is refused
	if err := e.eng.Register(approvalGraph(dir)); err != nil {
		t.Fatal(err)
	}
	waiting, _ := e.eng.Start(ctx, "approval", "go", wf.StartOptions{})
	waitStatus(t, e.eng, waiting, wf.StatusWaitingHuman)
	if _, err := e.eng.Replay(ctx, waiting, "", "human:test"); !errors.Is(err, wf.ErrNotFinished) {
		t.Fatalf("replay of a waiting run: %v", err)
	}
	wcps, _ := e.eng.RunCheckpoints(ctx, waiting)
	if _, err := e.eng.Replay(ctx, run, wcps[0].ID, "human:test"); err == nil || !strings.Contains(err.Error(), "not in the history") {
		t.Fatalf("a checkpoint of another run: %v", err)
	}
}

// The approval a person gives in the replay just before the step is the fresh approval: a merge after its own approve request
// runs without asking twice.
func TestReplayCountsTheApprovalJustBeforeTheStep(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	g := wf.NewGraph("ship").
		Add(wf.AgentTask("A", eff(dir, "A", "")), wf.Request("R", wf.RequestSpec{Kind: "approval", Title: "merge?"}), wf.Route("M", eff(dir, "M", "")).Effect("merges into main")).
		Edge("A", "R").EdgeWhen("R", "M", "approved").Start("A")
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	approve := func(run wf.RunID) {
		t.Helper()
		waitStatus(t, e.eng, run, wf.StatusWaitingHuman)
		pend, _ := e.eng.Pending(ctx, run)
		if len(pend) != 1 || pend[0].NodeID != "R" {
			t.Fatalf("pending %+v", pend)
		}
		if err := e.eng.Resume(ctx, run, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:test"}); err != nil {
			t.Fatal(err)
		}
		waitStatus(t, e.eng, run, wf.StatusCompleted)
	}
	run, _ := e.eng.Start(ctx, "ship", "in", wf.StartOptions{})
	approve(run)
	replay, err := e.eng.Replay(ctx, run, "", "human:test")
	if err != nil {
		t.Fatal(err)
	}
	approve(replay)
	if got := effects(dir); got["M"] != 2 {
		t.Fatalf("effects %v", got)
	}
	var n int
	_ = e.db.Reader().QueryRow(`SELECT COUNT(*) FROM approvals WHERE run_id=?`, string(replay)).Scan(&n)
	if n != 1 {
		t.Fatalf("the replay asked %d times, want once (its own approve request)", n)
	}
}

// A subworkflow's run has an id derived from the parent's node execution, so its nodes can be shown inside the parent's graph;
// in a replay a step inside it that changes something outside the run asks first, in the nested run, the parent waiting.
func TestSubworkflowRunsAreFoundFromTheParentsExecution(t *testing.T) {
	dir := t.TempDir()
	e := openEnv(t, dir)
	ctx := context.Background()
	inner := wf.Sequential("inner", wf.AgentTask("x", eff(dir, "x", "")), wf.Check("y", eff(dir, "y", "")))
	g := wf.Sequential("outer", wf.AgentTask("A", eff(dir, "A", "")), &wf.Node{ID: "S", Kind: wf.KindSubworkflow, Sub: inner})
	if err := e.eng.Register(g); err != nil {
		t.Fatal(err)
	}
	run, _ := e.eng.Start(ctx, "outer", "in", wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	sub := wf.SubRunID(run, "S", 2, 1)
	info, err := e.eng.Status(ctx, sub)
	if err != nil || info.Status != wf.StatusCompleted || info.Workflow != "inner" {
		t.Fatalf("the nested run %s: %+v %v", sub, info, err)
	}
	if a := attempts(t, e.eng, sub, "y"); strings.Join(a, ",") != "1:done" {
		t.Fatalf("nested executions %v", a)
	}

	// in a replay, a step inside a subworkflow that changes something outside the run asks for its fresh approval in the nested
	// run, and the replay waits with it (P124; before, the nested run was stopped and the replay failed)
	pushes := wf.Sequential("pushes", wf.Route("p", eff(dir, "p", "")).Effect("pushes"))
	g3 := wf.Sequential("outer3", &wf.Node{ID: "S", Kind: wf.KindSubworkflow, Sub: pushes})
	if err := e.eng.Register(g3); err != nil {
		t.Fatal(err)
	}
	run3, _ := e.eng.Start(ctx, "outer3", "in", wf.StartOptions{})
	waitStatus(t, e.eng, run3, wf.StatusCompleted)
	replay, err := e.eng.Replay(ctx, run3, "", "human:test")
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, replay, wf.StatusWaitingHuman)
	if got := effects(dir); got["p"] != 1 {
		t.Fatalf("the nested push ran in the replay: %v", got)
	}
	ps, _ := e.eng.Pending(ctx, replay)
	if len(ps) != 1 || !strings.Contains(ps[0].Spec.Title, "Waiting on pushes's question: This replay reached p") || ps[0].Spec.Child != wf.SubRunID(replay, "S", 1, 1) {
		t.Fatalf("pending %+v", ps)
	}
	if err := e.eng.Resume(ctx, replay, wf.Response{RequestID: ps[0].ID, Decision: "rejected", By: "human:test"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, replay, wf.StatusFailed)
	if got := effects(dir); got["p"] != 1 {
		t.Fatalf("the nested push ran after a rejection: %v", got)
	}
}

func eventsOfRun(run wf.RunID, typ string) events.Filter {
	return events.Filter{RunID: string(run), Types: []string{typ}}
}
