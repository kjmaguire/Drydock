package workflow

import (
	"context"
	"database/sql"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// SuspendedPrefix starts the failed_reason text of a run that Suspend parked, so ResumeSuspended resumes only those.
const SuspendedPrefix = "suspended: "

// Suspend stops the driver of an executing run cleanly and parks the run as PAUSED. Nothing is lost: the run keeps its
// latest checkpoint and a step that was in flight is redone from the checkpoint when ResumeSuspended re-launches the run
// (node effects are idempotent per run/node/step/attempt, workflow-engine.md). Unlike Cancel the run is not terminal.
// Runs that are not executing (waiting for a human, terminal, already paused) are left alone: it reports whether the
// run was parked. reason is shown to the human (for example "the license expired").
func (e *Engine) Suspend(ctx context.Context, run RunID, reason string) (bool, error) {
	info, err := e.st.getRun(ctx, run)
	if err != nil {
		return false, err
	}
	switch info.Status {
	case StatusRunning, StatusRecovering, StatusPending:
	default:
		return false, nil
	}
	e.mu.Lock()
	c, active := e.active[run]
	e.mu.Unlock()
	if active {
		c()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			e.mu.Lock()
			_, still := e.active[run]
			e.mu.Unlock()
			if !still {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	// The driver may have finished the run, or parked it for a human, between the status read and the cancel.
	var parked bool
	err = e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status='PAUSED', failed_reason=?, updated_at=?, version=version+1
			WHERE id=? AND status IN ('RUNNING','RECOVERING','PENDING')`, SuspendedPrefix+reason, storage.Now(), string(run))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		parked = n > 0
		return nil
	})
	if err != nil || !parked {
		return false, err
	}
	e.emit(ctx, "workflow.paused", info, map[string]any{"reason": "suspended", "detail": reason})
	return true, nil
}

// ResumeSuspended re-launches every run Suspend parked, from its latest checkpoint, and returns how many it resumed. A run
// whose checkpoint no longer matches the registered graph is failed with a clear reason instead (as Recover does).
func (e *Engine) ResumeSuspended(ctx context.Context) (int, error) {
	rows, err := e.st.db.Reader().QueryContext(ctx, `SELECT id FROM workflow_runs WHERE status='PAUSED' AND failed_reason LIKE ? ORDER BY created_at`, SuspendedPrefix+"%")
	if err != nil {
		return 0, err
	}
	var runs []RunID
	for rows.Next() {
		var id RunID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		runs = append(runs, id)
	}
	rows.Close()
	n := 0
	for _, id := range runs {
		ok, err := e.st.acquireLease(ctx, id, e.cfg.Owner, e.cfg.LeaseTTL)
		if err != nil || !ok {
			continue
		}
		info, err := e.st.getRun(ctx, id)
		if err != nil {
			e.st.releaseLease(ctx, id, e.cfg.Owner)
			continue
		}
		g, err := e.graph(info.Workflow)
		if err != nil {
			e.st.releaseLease(ctx, id, e.cfg.Owner)
			continue
		}
		cp, err := e.st.getCheckpoint(ctx, info.LatestCP)
		if err != nil || cp.State.Topology != g.TopologyHash() {
			e.st.releaseLease(ctx, id, e.cfg.Owner)
			_ = e.st.setStatus(ctx, id, StatusFailed, "resume after suspension: topology mismatch or missing checkpoint")
			continue
		}
		err = e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status='RECOVERING', failed_reason='', updated_at=?, version=version+1 WHERE id=?`, storage.Now(), string(id))
			return err
		})
		if err != nil {
			e.st.releaseLease(ctx, id, e.cfg.Owner)
			continue
		}
		e.emit(ctx, "workflow.recovering", info, map[string]any{"fromCheckpoint": cp.ID, "after": "suspension"})
		e.launch(info, g, cp)
		n++
	}
	return n, nil
}
