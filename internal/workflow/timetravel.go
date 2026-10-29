package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// The three calls a person makes on a run that is not waiting for them (decision P123, docs/workflow-engine.md s6):
//
//   - Continue resumes a run that stopped without finishing, in place, from its latest checkpoint;
//   - TimeTravel forks a new run from any checkpoint, with the state as of that checkpoint;
//   - Replay runs a finished run's nodes again in a new run from one of its checkpoints, never running a node that changes
//     something outside the run (MetaEffect) without a fresh approval.
//
// None of them rewrites a checkpoint, a node execution or an approval of another run: a fork and a replay are new runs whose
// first checkpoint's parent is the checkpoint they started from, and Continue only adds a checkpoint to its own run.

// Continue drives again a run that stopped without finishing: one that FAILED, or one left RUNNING or RECOVERING by a driver
// that is gone (the app was killed and the run's lease has run out; the recovery watch would take it over too, this does it on
// a person's word). It goes on from the run's latest checkpoint, which is the last step that committed: the step that failed
// or was cut off runs again, a node of it that finished is reused (its done row), a node that failed or was cut off runs
// with the next attempt number and its Previous, exactly as after a crash (s5). The resume writes an entry checkpoint whose
// parent is the latest one of the same run, so the timeline shows where the run went on from. Nothing about the run is reset: the same run
// id, so the same budgets, the same approvals (an approval or a rejection already given is read again by the gate that asked
// for it, so a step a person refused fails again), the same loop counters and the same safe points.
//
// A run waiting for a person, paused by a person (ResumePaused), still driven, or one that completed or was cancelled is not
// continued: ErrNotStopped, ErrLeaseHeld or ErrRunTerminal. by (a person) is recorded in the event.
func (e *Engine) Continue(ctx context.Context, run RunID, by string) error {
	info, err := e.st.getRun(ctx, run)
	if err != nil {
		return err
	}
	switch info.Status {
	case StatusFailed, StatusRunning, StatusRecovering:
	case StatusCompleted, StatusCancelled:
		return ErrRunTerminal
	default:
		return fmt.Errorf("%w: it is %s", ErrNotStopped, info.Status)
	}
	g, err := e.graph(info.Workflow)
	if err != nil {
		return err
	}
	e.mu.Lock()
	_, driving := e.active[run]
	e.mu.Unlock()
	if driving && info.Status != StatusFailed {
		return fmt.Errorf("%w: this Drydock is driving it", ErrNotStopped)
	}
	if err := e.awaitDriver(ctx, run, info.Status); err != nil {
		return err // the driver that failed the run is still winding down
	}
	d, claimed := e.claim(run)
	if !claimed {
		return ErrLeaseHeld
	}
	launched := false
	defer func() {
		if !launched {
			e.giveUp(run, d)
		}
	}()
	ok, err := e.st.acquireLease(ctx, run, e.cfg.Owner, e.cfg.LeaseTTL)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: another Drydock is driving the run", ErrLeaseHeld)
	}
	release := true
	defer func() {
		if release {
			e.st.releaseLease(context.Background(), run, e.cfg.Owner)
		}
	}()
	cp, err := e.st.getCheckpoint(ctx, info.LatestCP)
	if err != nil {
		return err
	}
	if cp.State.Topology != g.TopologyHash() {
		return ErrTopologyMismatch
	}
	st := cp.State.clone()
	st.Status = StatusRunning
	st.Result, st.FailedReason = nil, ""
	newCP := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: run, Workflow: g.Name, Step: st.Step, Kind: CheckpointEntry, Parent: cp.ID, State: st, Created: storage.Now()}
	state, err := e.st.stateJSON(newCP)
	if err != nil {
		return err
	}
	err = e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, run, e.cfg.Owner, e.cfg.LeaseTTL); err != nil {
			return err
		}
		// only from the status read above: a run cancelled (or resumed by someone else) meanwhile is left as it is. This is the one
		// write that takes a run out of FAILED, and only on a person's word.
		res, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status='RUNNING', failed_reason='', latest_checkpoint_id=?, updated_at=?, version=version+1
			WHERE id=? AND status=? AND latest_checkpoint_id=?`, string(newCP.ID), storage.Now(), string(run), string(info.Status), string(info.LatestCP))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: the run changed while it was being resumed", ErrNotStopped)
		}
		return insertCheckpoint(ctx, tx, newCP, state)
	})
	if err != nil {
		return err
	}
	e.emit(ctx, "workflow.run_resumed", info, map[string]any{"by": by, "from": info.Status, "reason": info.FailedReason, "checkpoint": newCP.ID, "step": newCP.Step})
	e.emit(ctx, "workflow.checkpoint_written", info, map[string]any{"checkpoint": newCP.ID, "kind": newCP.Kind, "step": newCP.Step})
	release, launched = false, true
	info.Status = StatusRunning
	e.launch(d, info, g, newCP)
	return nil
}

// TimeTravel starts a new run (a fork) from checkpoint cpID of any run, with the state as of that checkpoint: its inboxes, loop
// counters, shared outputs and open requests (which get new approval rows in the new run, so a person answers them afresh).
// The new run's first checkpoint has cpID as its parent, so Lineage draws the branch; the run it came from, its checkpoints, node
// executions and approvals are not touched, whatever state it is in (stopping it is the caller's business). The nodes that
// run after the checkpoint run again as new executions of the new run: they are live, a node that changes something outside
// the run behaves as it would in any run. What lies outside the engine (a task's worktree, its files and status) is not
// rewound. by (a person) is recorded in the new run's Origin.
func (e *Engine) TimeTravel(ctx context.Context, cpID CheckpointID, by string) (RunID, error) {
	src, err := e.st.getCheckpoint(ctx, cpID)
	if err != nil {
		return "", err
	}
	return e.branch(ctx, src, OriginFork, by)
}

// ResumeFrom is TimeTravel without a person's name.
func (e *Engine) ResumeFrom(ctx context.Context, cpID CheckpointID) (RunID, error) {
	return e.TimeTravel(ctx, cpID, "")
}

// Replay runs the nodes of a finished run (completed, failed or cancelled) again in a new run, from checkpoint from of the
// run's lineage (empty: the lineage's first, the entry, with the recorded input), with the recorded inboxes as the nodes'
// inputs. It is a branch like TimeTravel, marked as a replay (Origin), and in it a node that changes something outside the run
// (MetaEffect: a merge, a push, tasks created) never runs without a fresh approval of this run: either a person's approval that
// is the node's own input (the answer to a request just before it, given in this run) or the approval request the node
// raises first (replayGate). A rejection ends the replay there. Requests (and gates) ask afresh as they do in any new run.
func (e *Engine) Replay(ctx context.Context, run RunID, from CheckpointID, by string) (RunID, error) {
	info, err := e.st.getRun(ctx, run)
	if err != nil {
		return "", err
	}
	if !info.Status.Terminal() {
		return "", fmt.Errorf("%w: it is %s", ErrNotFinished, info.Status)
	}
	lin, err := e.st.lineage(ctx, run)
	if err != nil {
		return "", err
	}
	var src *Checkpoint
	for i := range lin {
		if from == "" || lin[i].ID == from {
			src = &lin[i]
			break
		}
	}
	if src == nil {
		return "", fmt.Errorf("workflow: checkpoint %s is not in the history of run %s", from, run)
	}
	return e.branch(ctx, *src, OriginReplay, by)
}

// branch creates and launches a new run from src (TimeTravel, Replay).
func (e *Engine) branch(ctx context.Context, src Checkpoint, kind, by string) (RunID, error) {
	g, err := e.graph(src.Workflow)
	if err != nil {
		return "", err
	}
	if src.State.Topology != g.TopologyHash() {
		return "", ErrTopologyMismatch
	}
	srcRun, err := e.st.getRun(ctx, src.RunID)
	if err != nil {
		return "", err
	}
	id := RunID(ids.New("run"))
	d, claimed := e.claim(id)
	if !claimed {
		return "", ErrEngineClosed
	}
	launched := false
	defer func() {
		if !launched {
			e.giveUp(id, d)
		}
	}()
	st := src.State.clone()
	st.Status = StatusRunning
	st.Result, st.FailedReason = nil, ""
	st.Origin = &Origin{Kind: kind, Run: src.RunID, Checkpoint: src.ID, Step: src.Step, By: by}
	// the open requests of the copied state are asked afresh: new approval rows of the new run, the parked node's inbox kept
	var reqs []approvalInsert
	pending := map[RequestID]PendingRequest{}
	waiting := map[NodeID]RequestID{}
	for _, nid := range g.order {
		old, ok := st.Waiting[nid]
		if !ok {
			continue
		}
		pr, ok := st.Pending[old]
		if !ok {
			continue
		}
		pr.ID = RequestID(ids.New("req"))
		pending[pr.ID], waiting[nid] = pr, pr.ID
		reqs = append(reqs, approvalInsert{id: pr.ID, nodeID: nid, spec: pr.Spec, projectID: srcRun.ProjectID, taskID: srcRun.TaskID})
	}
	st.Pending, st.Waiting = pending, waiting
	cp := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: id, Workflow: g.Name, Step: st.Step, Kind: CheckpointEntry, Parent: src.ID, State: st, Created: storage.Now()}
	info := RunInfo{ID: id, Workflow: g.Name, Status: StatusRunning, ProjectID: srcRun.ProjectID, TaskID: srcRun.TaskID, Input: st.Input, Topology: st.Topology, ParentCP: src.ID}
	if err := e.st.createRun(ctx, info, cp, reqs...); err != nil {
		return "", err
	}
	ok, err := e.st.acquireLease(ctx, id, e.cfg.Owner, e.cfg.LeaseTTL)
	if err != nil || !ok {
		return id, ErrLeaseHeld
	}
	e.emit(ctx, "workflow.started", info, map[string]any{"workflow": g.Name, "branchedFrom": src.ID, "origin": st.Origin})
	e.emit(ctx, "workflow.checkpoint_written", info, map[string]any{"checkpoint": cp.ID, "kind": cp.Kind, "step": cp.Step})
	for _, r := range reqs {
		e.emit(ctx, "workflow.request_raised", info, map[string]any{"request": r.id, "node": r.nodeID, "kind": r.spec.Kind, "title": r.spec.Title})
		e.emit(ctx, "approval.requested", info, map[string]any{"request": r.id, "kind": r.spec.Kind, "title": r.spec.Title})
	}
	launched = true
	e.launch(d, info, g, cp)
	return id, nil
}

// ReplayGate is the "gate" of the approval request a node that changes something outside the run raises in a replay.
const ReplayGate = "replay"

// replayHash is the action hash of the replay approvals of one node of one run: the gate finds them by it.
func replayHash(run RunID, node NodeID) string { return "replay/" + string(run) + "/" + string(node) }

// replayGate decides, before a node that changes something outside the run runs in a replay, whether it may (gated false).
// It may when its input carries a person's approval given in this run (the answer to the request just before it), or when the
// latest replay approval of this node in this run is APPROVED, which is consumed here: one approval lets one execution through,
// so a node reached again asks again. The latest one REJECTED ends the run; otherwise the node raises the approval request
// (Retry: it runs again once a person has answered, and reads the answer here).
func (e *Engine) replayGate(ctx context.Context, info RunInfo, n *Node, step int, inbox []Message) (NodeOutput, bool, error) {
	if e.freshApproval(ctx, info.ID, inbox) {
		return NodeOutput{}, false, nil
	}
	hash := replayHash(info.ID, n.ID)
	var id, status string
	err := e.st.db.Reader().QueryRowContext(ctx, `SELECT id, status FROM approvals WHERE run_id=? AND node_id=? AND action_hash=?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, string(info.ID), string(n.ID), hash).Scan(&id, &status)
	switch {
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return NodeOutput{}, true, err
	case err == nil && status == "REJECTED":
		return NodeOutput{}, true, fmt.Errorf("replay: a person chose not to run %s (%s) again", n.ID, n.Meta[MetaEffect])
	case err == nil && status == "APPROVED":
		if cerr := e.MarkConsumed(ctx, RequestID(id)); cerr == nil {
			return NodeOutput{}, false, nil
		} else if !errors.Is(cerr, ErrRequestConsumed) {
			return NodeOutput{}, true, cerr
		}
	}
	what := n.Meta[MetaEffect]
	payload, _ := json.Marshal(map[string]any{"gate": ReplayGate, "node": n.ID, "step": step, "effect": what, "replayOf": e.originRun(ctx, info.ID)})
	e.emit(ctx, "workflow.gated", info, map[string]any{"node": n.ID, "step": step, "gate": ReplayGate})
	return NodeOutput{Request: &RequestSpec{Kind: "approval", Retry: true, ActionHash: hash, Payload: payload,
		Title: fmt.Sprintf("This replay reached %s, which %s. Approve to run it again, or reject to stop the replay here.", n.ID, what)}}, true, nil
}

// freshApproval reports whether one of the messages is a person's approval of a request of this run (Resume's answer body).
func (e *Engine) freshApproval(ctx context.Context, run RunID, inbox []Message) bool {
	for _, m := range inbox {
		var a struct {
			Decision string    `json:"decision"`
			By       string    `json:"by"`
			Request  RequestID `json:"request"`
		}
		if json.Unmarshal(m.Value, &a) != nil || a.Request == "" || a.Decision != "approved" {
			continue
		}
		var n int
		if e.st.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals WHERE id=? AND run_id=? AND status IN ('APPROVED','CONSUMED')`,
			string(a.Request), string(run)).Scan(&n) == nil && n > 0 {
			return true
		}
	}
	return false
}

// originRun is the run a replay came from ("" when unknown).
func (e *Engine) originRun(ctx context.Context, run RunID) RunID {
	cp, err := e.st.latest(ctx, run)
	if err != nil || cp.State.Origin == nil {
		return ""
	}
	return cp.State.Origin.Run
}

// LeaseInfo says who drives a run.
type LeaseInfo struct {
	// Held: a lease that has not expired exists (some Drydock is driving the run).
	Held bool `json:"held"`
	// Mine: the lease is this engine's.
	Mine bool `json:"mine"`
	// Driving: this engine has a driver for the run right now.
	Driving bool  `json:"driving"`
	Expires int64 `json:"expires,omitempty"`
}

// Lease reports the run's lease and whether this engine drives it.
func (e *Engine) Lease(ctx context.Context, run RunID) (LeaseInfo, error) {
	var li LeaseInfo
	e.mu.Lock()
	_, li.Driving = e.active[run]
	e.mu.Unlock()
	var owner string
	err := e.st.db.Reader().QueryRowContext(ctx, `SELECT owner, expires_at FROM run_leases WHERE run_id=?`, string(run)).Scan(&owner, &li.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return li, nil
	}
	if err != nil {
		return li, err
	}
	li.Held = li.Expires >= storage.Now()
	li.Mine = owner == e.cfg.Owner
	return li, nil
}
