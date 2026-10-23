package workflow

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// SuspendedPrefix starts the failed_reason text of a run that Suspend parked, so ResumeSuspended resumes only those.
const SuspendedPrefix = "suspended: "

// PausedPrefix starts the failed_reason text of a run a person paused (Pause), and the error of the attempt that pause cut off:
// ResumeSuspended leaves such a run alone (only ResumePaused brings it back), and that attempt is not counted as one Drydock
// cut off by stopping (AutoRerunsAfterInterruption), since a person chose to stop it.
const PausedPrefix = "paused: "

// Suspend stops the driver of an executing run cleanly and parks the run as PAUSED. Nothing is lost: the run keeps its
// latest checkpoint and a step that was in flight is redone from the checkpoint when ResumeSuspended re-launches the run
// (node effects are idempotent per run/node/step/attempt, workflow-engine.md). Unlike Cancel the run is not terminal.
// Runs that are not executing (waiting for a human, terminal, already paused) are left alone: it reports whether the
// run was parked. reason is shown to the human (for example "the license expired").
func (e *Engine) Suspend(ctx context.Context, run RunID, reason string) (bool, error) {
	parked, _, err := e.stopAndPark(ctx, run, SuspendedPrefix+reason, "suspended", reason)
	return parked, err
}

// Pause is Suspend on a person's word: it stops the driver of an executing run at once (an agent turn in flight is stopped,
// a step in flight is redone from the latest checkpoint) and parks the run as PAUSED until ResumePaused, which only a person's
// resume calls; a restart and the license's resume leave it parked. The attempt it cut off is marked, so it does not count
// toward the turns Drydock re-runs on its own after a stop. It reports whether the run was parked: a run that is not
// executing (waiting for a human, terminal, already paused) is left alone.
func (e *Engine) Pause(ctx context.Context, run RunID, reason string) (bool, error) {
	parked, since, err := e.stopAndPark(ctx, run, PausedPrefix+reason, "paused", reason)
	if err != nil || !parked {
		return parked, err
	}
	err = e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE node_executions SET error=? WHERE run_id=? AND (status='started' OR (status='abandoned' AND finished_at>=?))`,
			PausedPrefix+reason, string(run), since)
		return err
	})
	return true, err
}

// stopAndPark stops the run's driver and sets PAUSED with failed_reason why, announcing workflow.paused with reason kind; since
// is when the stop began (Unix ms).
func (e *Engine) stopAndPark(ctx context.Context, run RunID, why, kind, detail string) (parked bool, since int64, err error) {
	info, err := e.st.getRun(ctx, run)
	if err != nil {
		return false, 0, err
	}
	switch info.Status {
	case StatusRunning, StatusRecovering, StatusPending:
	default:
		return false, 0, nil
	}
	since = storage.Now()
	e.mu.Lock()
	d, active := e.active[run]
	e.mu.Unlock()
	if active {
		d.cancel()
		select {
		case <-d.done:
		case <-time.After(10 * time.Second):
		}
	}
	// The driver may have finished the run, or parked it for a human, between the status read and the cancel.
	err = e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status='PAUSED', failed_reason=?, updated_at=?, version=version+1
			WHERE id=? AND status IN ('RUNNING','RECOVERING','PENDING')`, why, storage.Now(), string(run))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		parked = n > 0
		return nil
	})
	if err != nil || !parked {
		return false, since, err
	}
	e.emit(ctx, "workflow.paused", info, map[string]any{"reason": kind, "detail": detail})
	return true, since, nil
}

// ResumePaused re-launches one run a person paused (Pause), from its latest checkpoint. It reports whether it resumed it: a
// run that is not paused by a person (running, waiting, ended, suspended for the license) is left alone. A run whose
// checkpoint no longer matches the registered graph is failed with a clear reason instead (as Recover does).
func (e *Engine) ResumePaused(ctx context.Context, run RunID) (bool, error) {
	info, err := e.st.getRun(ctx, run)
	if err != nil {
		return false, err
	}
	if info.Status != StatusPaused || !strings.HasPrefix(info.FailedReason, PausedPrefix) {
		return false, nil
	}
	d, ok := e.claim(run)
	if !ok {
		return false, nil
	}
	if !e.resumeSuspended(ctx, run, d) {
		e.giveUp(run, d)
		if now, err := e.st.getRun(ctx, run); err == nil && now.Status == StatusFailed {
			return false, fmt.Errorf("the run cannot go on: %s", now.FailedReason)
		}
		return false, nil
	}
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
		d, ok := e.claim(id) // before the lease, as recover does: the recovery watch must not take a run being resumed here
		if !ok {
			continue
		}
		if e.resumeSuspended(ctx, id, d) {
			n++
		} else {
			e.giveUp(id, d)
		}
	}
	return n, nil
}

// resumeSuspended re-launches one parked run with the claimed driver d and reports whether it launched it.
func (e *Engine) resumeSuspended(ctx context.Context, id RunID, d *driver) bool {
	ok, err := e.st.acquireLease(ctx, id, e.cfg.Owner, e.cfg.LeaseTTL)
	if err != nil || !ok {
		return false
	}
	info, err := e.st.getRun(ctx, id)
	if err != nil {
		e.st.releaseLease(ctx, id, e.cfg.Owner)
		return false
	}
	g, err := e.graph(info.Workflow)
	if err != nil {
		e.st.releaseLease(ctx, id, e.cfg.Owner)
		return false
	}
	cp, err := e.st.getCheckpoint(ctx, info.LatestCP)
	if err != nil || cp.State.Topology != g.TopologyHash() {
		e.st.releaseLease(ctx, id, e.cfg.Owner)
		_ = e.st.setStatus(ctx, id, StatusFailed, "resume after suspension: topology mismatch or missing checkpoint")
		return false
	}
	// only a run that is still paused comes back: one cancelled or ended since it was read is not brought to life again (audit
	// 2026-10-16, L14)
	var resumed bool
	err = e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status='RECOVERING', failed_reason='', updated_at=?, version=version+1 WHERE id=? AND status='PAUSED'`, storage.Now(), string(id))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		resumed = n > 0
		return nil
	})
	if err != nil || !resumed {
		e.st.releaseLease(ctx, id, e.cfg.Owner)
		return false
	}
	e.emit(ctx, "workflow.recovering", info, map[string]any{"fromCheckpoint": cp.ID, "after": "suspension"})
	e.launch(d, info, g, cp)
	return true
}
