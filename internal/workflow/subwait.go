package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/storage"
)

// A subworkflow that waits for a person (decision P124, docs/workflow-engine.md s6). The subworkflow node's nested run is a
// run of its own (SubRunID) whose parent_run_id names the run that started it. When the nested run parks at a request, the
// node's output is a request that names it (RequestSpec.Child), with no approvals row: the parent parks too, and shows the
// nested run's request as its own (Pending). A person answers the nested run's request, there or through the parent's
// (Resume passes it on); the nested run goes on, and when it ends the parent goes on from the step that waited (resumeChild):
// with the nested run's result as the node's output when it completed, or failed with its reason (the step's inbox kept, so
// a Continue runs the step again with a new nested run) when it failed or was cancelled. Cancelling the parent cancels the
// nested run. A parent whose nested run ended while nobody drove it (the app stopped in between) is found by recovery.

// approvalTask is the task the requests of a run belong to: its own, or for a nested run (which has no task, so that it never
// splits a task's budget window), the task of the run that started it, so the Inbox and the task page find them.
func (e *Engine) approvalTask(ctx context.Context, info RunInfo) string {
	task, parent := info.TaskID, info.ParentRun
	for i := 0; task == "" && parent != "" && i < 8; i++ {
		pi, err := e.st.getRun(ctx, parent)
		if err != nil {
			break
		}
		task, parent = pi.TaskID, pi.ParentRun
	}
	return task
}

// waitsOn reports the request of st that waits on the nested run child.
func waitsOn(st RunState, child RunID) (PendingRequest, bool) {
	for _, p := range st.Pending {
		if p.Spec.Child == child {
			return p, true
		}
	}
	return PendingRequest{}, false
}

// watchChildren takes up, once run's driver has parked it, each nested run it waits on that has already ended: the nested
// run may have ended while the step that waits on it was being recorded, and its end found nothing to wake then.
func (e *Engine) watchChildren(run RunID, st RunState) {
	var kids []RunID
	for _, p := range st.Pending {
		if p.Spec.Child != "" {
			kids = append(kids, p.Spec.Child)
		}
	}
	if len(kids) == 0 {
		return
	}
	safe.Go("workflow.Engine.watchChildren", func() {
		for _, k := range kids {
			_ = e.resumeChild(context.Background(), run, k)
		}
	})
}

// childEnded wakes the run that started info's run, when it has one: info's run has just ended.
func (e *Engine) childEnded(info RunInfo) {
	if info.ParentRun == "" {
		return
	}
	safe.Go("workflow.Engine.childEnded", func() { _ = e.resumeChild(context.Background(), info.ParentRun, info.ID) })
}

// cancelChildren cancels the nested runs of run that have not ended.
func (e *Engine) cancelChildren(ctx context.Context, run RunID) {
	rows, err := e.st.db.Reader().QueryContext(ctx, `SELECT id FROM workflow_runs WHERE parent_run_id=? AND status NOT IN `+terminalStatuses, string(run))
	if err != nil {
		return
	}
	var kids []RunID
	for rows.Next() {
		var id RunID
		if rows.Scan(&id) == nil {
			kids = append(kids, id)
		}
	}
	rows.Close()
	for _, k := range kids {
		if err := e.Cancel(ctx, k); err != nil && !errors.Is(err, ErrRunTerminal) {
			e.emit(ctx, "workflow.cancel_failed", RunInfo{ID: k}, map[string]any{"parent": run, "error": err.Error()})
		}
	}
}

// resumeChild goes on with parent, parked on a step that waits on the nested run child, once child has ended. Anything else
// (the parent is not parked, it waits on another run, the nested run has not ended) is left as it is.
func (e *Engine) resumeChild(ctx context.Context, parent, child RunID) error {
	ci, err := e.st.getRun(ctx, child)
	if err != nil || !ci.Status.Terminal() {
		return err
	}
	info, err := e.st.getRun(ctx, parent)
	if err != nil {
		return err
	}
	if info.Status != StatusWaitingHuman {
		return nil // a parent that is working meets the nested run's end in its own wait; an ended one is done
	}
	if cp0, err := e.st.getCheckpoint(ctx, info.LatestCP); err != nil {
		return err
	} else if _, ok := waitsOn(cp0.State, child); !ok {
		return nil // an earlier attempt's nested run: the parent waits on another one, or on nothing
	}
	g, err := e.graph(info.Workflow)
	if err != nil {
		return err
	}
	if err := e.awaitDriver(ctx, parent, info.Status); err != nil {
		return err
	}
	d, claimed := e.claim(parent)
	if !claimed {
		return ErrLeaseHeld
	}
	launched := false
	defer func() {
		if !launched {
			e.giveUp(parent, d)
		}
	}()
	ok, err := e.st.acquireLease(ctx, parent, e.cfg.Owner, e.cfg.LeaseTTL)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLeaseHeld
	}
	release := true
	defer func() {
		if release {
			e.st.releaseLease(context.Background(), parent, e.cfg.Owner)
		}
	}()
	// read again under the lease: another wake (the nested run's own end and the recovery watch) may have gone first
	if info, err = e.st.getRun(ctx, parent); err != nil {
		return err
	}
	if info.Status != StatusWaitingHuman {
		return nil
	}
	cp, err := e.st.getCheckpoint(ctx, info.LatestCP)
	if err != nil {
		return err
	}
	if cp.State.Topology != g.TopologyHash() {
		return ErrTopologyMismatch
	}
	st := cp.State.clone()
	pr, ok := waitsOn(st, child)
	if !ok {
		return nil
	}
	delete(st.Pending, pr.ID)
	delete(st.Waiting, pr.NodeID)
	status, reason := StatusRunning, ""
	if ci.Status == StatusCompleted {
		out := NodeOutput{Value: ci.Result}
		if len(out.Value) > 0 {
			st.Shared["out/"+string(pr.NodeID)] = e.externalize(parent, pr.NodeID, pr.Step, out.Value)
		}
		if _, err := route(g, &st, pr.NodeID, out); err != nil {
			status, reason = StatusFailed, err.Error()
		}
	} else {
		// the step is still owed: its inbox back, so a Continue runs it again with a nested run of its own
		st.Inboxes[pr.NodeID] = append([]Message(nil), pr.Inbox...)
		if n := g.Nodes[pr.NodeID]; n != nil && len(n.FanIn) > 0 {
			st.Barriers[pr.NodeID] = append([]NodeID(nil), n.FanIn...)
		}
		status, reason = StatusFailed, fmt.Sprintf("node %s: subworkflow %s ended %s", pr.NodeID, ci.Workflow, ci.Status)
		if ci.FailedReason != "" {
			reason += ": " + ci.FailedReason
		}
	}
	st.Status, st.FailedReason = status, reason
	newCP := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: parent, Workflow: g.Name, Step: st.Step, Kind: CheckpointResponseEntry, Parent: cp.ID, State: st, Created: storage.Now()}
	if err := e.st.commit(ctx, stepCommit{cp: newCP, status: status, failed: reason, owner: e.cfg.Owner, ttl: e.cfg.LeaseTTL}); err != nil {
		return err
	}
	e.emit(ctx, "workflow.child_ended", info, map[string]any{"node": pr.NodeID, "child": child, "status": ci.Status, "request": pr.ID})
	e.emit(ctx, "workflow.checkpoint_written", info, map[string]any{"checkpoint": newCP.ID, "kind": newCP.Kind, "step": newCP.Step})
	if status == StatusFailed {
		info.Status = StatusFailed
		e.emit(context.Background(), "workflow.failed", info, map[string]any{"reason": reason})
		e.childEnded(info)
		return nil
	}
	e.emit(ctx, "workflow.resumed", info, map[string]any{})
	release, launched = false, true // the driver releases the lease when it stops
	info.Status = StatusRunning
	e.launch(d, info, g, newCP)
	return nil
}

// answerChild records an answer given to a step that waits on a nested run on the request that run waits on now.
func (e *Engine) answerChild(ctx context.Context, pr PendingRequest, resp Response) error {
	ps, err := e.Pending(ctx, pr.Spec.Child)
	if err != nil {
		return err
	}
	if len(ps) != 1 {
		return fmt.Errorf("%w (%d open)", ErrNoChildRequest, len(ps))
	}
	resp.RequestID = ps[0].ID
	return e.Resume(ctx, pr.Spec.Child, resp)
}

// resolveChildWait fills a request that waits on a nested run with what that run waits for now: its one open request's kind,
// payload and title ("Waiting on <subworkflow>'s question: ..."), ChildRequest naming it.
func (e *Engine) resolveChildWait(ctx context.Context, p PendingRequest, depth int) PendingRequest {
	ci, err := e.st.getRun(ctx, p.Spec.Child)
	if err != nil {
		return p
	}
	ps, err := e.pending(ctx, p.Spec.Child, depth+1)
	if err != nil {
		return p
	}
	switch len(ps) {
	case 0:
		p.Spec.Title = fmt.Sprintf("Waiting on %s, which is %s", ci.Workflow, statusText(ci.Status))
	case 1:
		c := ps[0].Spec
		p.Spec.Kind, p.Spec.Phase, p.Spec.Payload, p.Spec.ChildRequest = c.Kind, c.Phase, c.Payload, ps[0].ID
		p.Spec.Title = fmt.Sprintf("Waiting on %s's question: %s", ci.Workflow, c.Title)
	default:
		p.Spec.Title = fmt.Sprintf("Waiting on %s's %d questions: answer them in its run", ci.Workflow, len(ps))
	}
	return p
}

func statusText(s RunStatus) string {
	switch s {
	case StatusWaitingHuman:
		return "waiting for a person"
	case StatusRunning, StatusRecovering, StatusPending:
		return "working"
	}
	return "ended " + string(s)
}

// recoverChildWaits takes up the parents parked on a nested run that ended while no driver could wake them.
func (e *Engine) recoverChildWaits(ctx context.Context) {
	rows, err := e.st.db.Reader().QueryContext(ctx, `SELECT p.id, c.id FROM workflow_runs c JOIN workflow_runs p ON p.id = c.parent_run_id
		WHERE c.parent_run_id <> '' AND p.status = 'WAITING_HUMAN' AND c.status IN `+terminalStatuses)
	if err != nil {
		return
	}
	type pair struct{ parent, child RunID }
	var ps []pair
	for rows.Next() {
		var p pair
		if rows.Scan(&p.parent, &p.child) == nil {
			ps = append(ps, p)
		}
	}
	rows.Close()
	for _, p := range ps {
		_ = e.resumeChild(ctx, p.parent, p.child)
	}
}
