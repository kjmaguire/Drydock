package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kjmaguire/drydock/internal/redact"
	"github.com/kjmaguire/drydock/internal/storage"
)

// RunInfo is the row view of a run.
type RunInfo struct {
	ID           RunID           `json:"id"`
	Workflow     string          `json:"workflow"`
	Status       RunStatus       `json:"status"`
	ProjectID    string          `json:"projectId,omitempty"`
	TaskID       string          `json:"taskId,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Topology     string          `json:"topology"`
	ParentCP     CheckpointID    `json:"parentCheckpoint,omitempty"`
	LatestCP     CheckpointID    `json:"latestCheckpoint,omitempty"`
	FailedReason string          `json:"failedReason,omitempty"`
	Created      int64           `json:"created"`
	Updated      int64           `json:"updated"`
	Version      int64           `json:"version"`
}

// ApprovalRow is the row view of a request.
type ApprovalRow struct {
	ID         RequestID       `json:"id"`
	RunID      RunID           `json:"runId"`
	NodeID     NodeID          `json:"nodeId"`
	TaskID     string          `json:"taskId,omitempty"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	ActionHash string          `json:"actionHash,omitempty"`
	Status     string          `json:"status"`
	Decision   string          `json:"decision,omitempty"`
	Created    int64           `json:"created"`
	Resolved   int64           `json:"resolved,omitempty"`
}

// ExecRow is a node_executions row.
type ExecRow struct {
	NodeID     NodeID
	Step       int
	Attempt    int
	Status     string
	Output     json.RawMessage
	Error      string
	PartialRef string
	SafePoint  string
	Started    int64
	Finished   int64
}

type store struct {
	db *storage.DB
	// redact (optional) removes secrets from the output, error, result and checkpoint state of a run before they are stored
	// (Config.Redact; audit 2026-10-16, H7).
	redact redact.Func
}

// text and jsonb apply the redactor to what a run records.
func (s *store) text(v string) string {
	if s.redact == nil {
		return v
	}
	return redact.Lines(v, s.redact)
}

func (s *store) jsonb(b json.RawMessage) json.RawMessage {
	if s.redact == nil || len(b) == 0 {
		return b
	}
	return redact.JSON(b, s.redact)
}

func (s *store) createRun(ctx context.Context, info RunInfo, cp Checkpoint) error {
	st, err := s.stateJSON(cp)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		now := storage.Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_runs
			(id,workflow,status,project_id,task_id,input,topology_hash,parent_checkpoint_id,latest_checkpoint_id,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			string(info.ID), info.Workflow, string(info.Status), info.ProjectID, info.TaskID, nz(info.Input), info.Topology,
			string(info.ParentCP), string(cp.ID), now, now); err != nil {
			return err
		}
		return insertCheckpoint(ctx, tx, cp, st)
	})
}

func nz(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// stateJSON is a checkpoint's state as it is stored: scanned for secrets, which happens before the write transaction opens (a scan
// of a large state is not a time to hold the database's one writer).
func (s *store) stateJSON(cp Checkpoint) ([]byte, error) {
	st, err := json.Marshal(cp.State)
	if err != nil {
		return nil, err
	}
	if s.redact != nil {
		st = redact.JSON(st, s.redact)
	}
	return st, nil
}

func insertCheckpoint(ctx context.Context, tx *sql.Tx, cp Checkpoint, st []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO workflow_checkpoints(id,run_id,workflow,step,kind,parent_id,state,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, string(cp.ID), string(cp.RunID), cp.Workflow, cp.Step, string(cp.Kind), string(cp.Parent), st, cp.Created)
	return err
}

func (s *store) getRun(ctx context.Context, id RunID) (RunInfo, error) {
	var r RunInfo
	var input, result []byte
	var status string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT id,workflow,status,project_id,task_id,input,result,topology_hash,
		parent_checkpoint_id,latest_checkpoint_id,failed_reason,created_at,updated_at,version FROM workflow_runs WHERE id=?`, string(id)).
		Scan(&r.ID, &r.Workflow, &status, &r.ProjectID, &r.TaskID, &input, &result, &r.Topology, &r.ParentCP, &r.LatestCP, &r.FailedReason, &r.Created, &r.Updated, &r.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("workflow: run %s not found", id)
	}
	r.Status = RunStatus(status)
	r.Input = input
	r.Result = result
	return r, err
}

func (s *store) getCheckpoint(ctx context.Context, id CheckpointID) (Checkpoint, error) {
	var cp Checkpoint
	var kind string
	var st []byte
	err := s.db.Reader().QueryRowContext(ctx, `SELECT id,run_id,workflow,step,kind,parent_id,state,created_at FROM workflow_checkpoints WHERE id=?`, string(id)).
		Scan(&cp.ID, &cp.RunID, &cp.Workflow, &cp.Step, &kind, &cp.Parent, &st, &cp.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return cp, fmt.Errorf("workflow: checkpoint %s not found", id)
	}
	if err != nil {
		return cp, err
	}
	cp.Kind = CheckpointKind(kind)
	if err := json.Unmarshal(st, &cp.State); err != nil {
		return cp, err
	}
	return cp, nil
}

// Latest returns the latest checkpoint of a run.
func (s *store) latest(ctx context.Context, run RunID) (Checkpoint, error) {
	info, err := s.getRun(ctx, run)
	if err != nil {
		return Checkpoint{}, err
	}
	return s.getCheckpoint(ctx, info.LatestCP)
}

// lineage returns checkpoints from the root of the lineage to the latest checkpoint of run, following parent links
// across runs (so a time-travel branch shows the history it forked from).
func (s *store) lineage(ctx context.Context, run RunID) ([]Checkpoint, error) {
	cp, err := s.latest(ctx, run)
	if err != nil {
		return nil, err
	}
	var rev []Checkpoint
	for {
		rev = append(rev, cp)
		if cp.Parent == "" {
			break
		}
		cp, err = s.getCheckpoint(ctx, cp.Parent)
		if err != nil {
			return nil, err
		}
	}
	out := make([]Checkpoint, len(rev))
	for i := range rev {
		out[len(rev)-1-i] = rev[i]
	}
	return out, nil
}

// runCheckpoints lists every checkpoint stored under run (not following parents), oldest first.
func (s *store) runCheckpoints(ctx context.Context, run RunID) ([]Checkpoint, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id FROM workflow_checkpoints WHERE run_id=? ORDER BY created_at, rowid`, string(run))
	if err != nil {
		return nil, err
	}
	var ids []CheckpointID
	for rows.Next() {
		var id CheckpointID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []Checkpoint
	for _, id := range ids {
		cp, err := s.getCheckpoint(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil
}

// --- leases ---

func (s *store) acquireLease(ctx context.Context, run RunID, owner string, ttl time.Duration) (bool, error) {
	var ok bool
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		now := storage.Now()
		exp := time.Now().Add(ttl).UnixMilli()
		res, err := tx.ExecContext(ctx, `INSERT INTO run_leases(run_id,owner,expires_at) VALUES(?,?,?)
			ON CONFLICT(run_id) DO UPDATE SET owner=excluded.owner, expires_at=excluded.expires_at
			WHERE run_leases.expires_at < ?`, string(run), owner, exp, now)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		ok = n > 0
		return nil
	})
	return ok, err
}

func (s *store) renewLease(ctx context.Context, run RunID, owner string, ttl time.Duration) (bool, error) {
	var ok bool
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE run_leases SET expires_at=? WHERE run_id=? AND owner=?`,
			time.Now().Add(ttl).UnixMilli(), string(run), owner)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		ok = n > 0
		return nil
	})
	return ok, err
}

func (s *store) releaseLease(ctx context.Context, run RunID, owner string) {
	_ = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM run_leases WHERE run_id=? AND owner=?`, string(run), owner)
		return err
	})
}

// fence verifies, inside tx, that owner still holds the run's lease, and extends it by ttl: a zombie driver (one whose
// lease another owner took) can never commit. A lease that ran out while its driver was stalled (a busy machine, a laptop
// asleep) and that nobody took is still the owner's, so it is extended rather than refused; refusing it stopped the driver
// and left the run RUNNING with nobody driving it. Taking a lease over needs it expired (acquireLease), and both happen in
// write transactions, so a run never has two owners.
func fence(ctx context.Context, tx *sql.Tx, run RunID, owner string, ttl time.Duration) error {
	res, err := tx.ExecContext(ctx, `UPDATE run_leases SET expires_at=MAX(expires_at, ?) WHERE run_id=? AND owner=?`,
		time.Now().Add(ttl).UnixMilli(), string(run), owner)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseHeld
	}
	return nil
}

// --- commits ---

type stepCommit struct {
	cp          Checkpoint
	status      RunStatus
	result      json.RawMessage
	failed      string
	newRequests []approvalInsert
	owner       string        // when set, the commit is fenced by this owner's lease
	ttl         time.Duration // how far the fence extends the lease
}

type approvalInsert struct {
	id        RequestID
	nodeID    NodeID
	spec      RequestSpec
	projectID string
	taskID    string
}

func (s *store) commit(ctx context.Context, c stepCommit) error {
	state, err := s.stateJSON(c.cp)
	if err != nil {
		return err
	}
	result, failed := s.jsonb(c.result), s.text(c.failed)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if c.owner != "" {
			if err := fence(ctx, tx, c.cp.RunID, c.owner, c.ttl); err != nil {
				return err
			}
		}
		// A run that reached an end meanwhile (a person cancelled it while this step ran) stays ended: the step is not
		// recorded and the driver stops (ErrRunTerminal), instead of writing RUNNING over CANCELLED.
		now := storage.Now()
		res, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?, latest_checkpoint_id=?, result=COALESCE(?,result),
			failed_reason=?, updated_at=?, version=version+1 WHERE id=? AND status NOT IN `+terminalStatuses,
			string(c.status), string(c.cp.ID), nullBytes(result), failed, now, string(c.cp.RunID))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrRunTerminal
		}
		if err := insertCheckpoint(ctx, tx, c.cp, state); err != nil {
			return err
		}
		for _, a := range c.newRequests {
			if _, err := tx.ExecContext(ctx, `INSERT INTO approvals(id,run_id,node_id,task_id,project_id,kind,title,payload,action_hash,status,created_at)
				VALUES(?,?,?,?,?,?,?,?,?,'REQUESTED',?)`, string(a.id), string(c.cp.RunID), string(a.nodeID), a.taskID, a.projectID,
				a.spec.Kind, a.spec.Title, nz(a.spec.Payload), a.spec.ActionHash, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func nullBytes(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

// terminalStatuses is the SQL list of the statuses a run never leaves.
const terminalStatuses = `('COMPLETED','FAILED','CANCELLED')`

// setStatus moves a run that has not ended to status; a run that has ended (completed, failed or cancelled) is left as it is
// and ErrRunTerminal returned, so a late write from a driver never revives a cancelled run.
func (s *store) setStatus(ctx context.Context, run RunID, status RunStatus, reason string) error {
	reason = s.text(reason)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?, failed_reason=CASE WHEN ?<>'' THEN ? ELSE failed_reason END,
			updated_at=?, version=version+1 WHERE id=? AND status NOT IN `+terminalStatuses, string(status), reason, reason, storage.Now(), string(run))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrRunTerminal
		}
		return nil
	})
}

// --- node executions ---

func (s *store) execRows(ctx context.Context, run RunID, node NodeID, step int) ([]ExecRow, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT node_id,step,attempt,status,output,error,partial_ref,safe_point_ref,started_at,finished_at FROM node_executions
		WHERE run_id=? AND node_id=? AND step=? ORDER BY attempt`, string(run), string(node), step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExecRow
	for rows.Next() {
		var r ExecRow
		var o []byte
		if err := rows.Scan(&r.NodeID, &r.Step, &r.Attempt, &r.Status, &o, &r.Error, &r.PartialRef, &r.SafePoint, &r.Started, &r.Finished); err != nil {
			return nil, err
		}
		r.Output = o
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *store) execStart(ctx context.Context, run RunID, node NodeID, step, attempt int, partialRef string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO node_executions(run_id,node_id,step,attempt,status,partial_ref,started_at)
			VALUES(?,?,?,?, 'started', ?, ?)`, string(run), string(node), step, attempt, partialRef, storage.Now())
		return err
	})
}

func (s *store) execFinish(ctx context.Context, run RunID, node NodeID, step, attempt int, status string, out json.RawMessage, errMsg string) error {
	out, errMsg = s.jsonb(out), s.text(errMsg)
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE node_executions SET status=?, output=?, error=?, finished_at=?
			WHERE run_id=? AND node_id=? AND step=? AND attempt=?`, status, nullBytes(out), errMsg, storage.Now(), string(run), string(node), step, attempt)
		return err
	})
}

func (s *store) execAbandon(ctx context.Context, run RunID, node NodeID, step, attempt int) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE node_executions SET status='abandoned', finished_at=? WHERE run_id=? AND node_id=? AND step=? AND attempt=? AND status='started'`,
			storage.Now(), string(run), string(node), step, attempt)
		return err
	})
}

// ExecRows lists all node executions of a run (for the Workflow view and tests).
func (s *store) allExecs(ctx context.Context, run RunID) ([]ExecRow, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT node_id,step,attempt,status,output,error,partial_ref,safe_point_ref,started_at,finished_at
		FROM node_executions WHERE run_id=? ORDER BY step, node_id, attempt`, string(run))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExecRow
	for rows.Next() {
		var r ExecRow
		var o []byte
		if err := rows.Scan(&r.NodeID, &r.Step, &r.Attempt, &r.Status, &o, &r.Error, &r.PartialRef, &r.SafePoint, &r.Started, &r.Finished); err != nil {
			return nil, err
		}
		r.Output = o
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- approvals ---

const approvalCols = `id,run_id,node_id,task_id,kind,title,payload,action_hash,status,decision,created_at,resolved_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanApproval(r rowScanner) (ApprovalRow, error) {
	var a ApprovalRow
	var payload []byte
	var resolved sql.NullInt64
	err := r.Scan(&a.ID, &a.RunID, &a.NodeID, &a.TaskID, &a.Kind, &a.Title, &payload, &a.ActionHash, &a.Status, &a.Decision, &a.Created, &resolved)
	a.Payload = payload
	a.Resolved = resolved.Int64
	return a, err
}

// listApprovals reads the approvals with status (all when empty) in one query, oldest first.
func (s *store) listApprovals(ctx context.Context, status string) ([]ApprovalRow, error) {
	q := `SELECT ` + approvalCols + ` FROM approvals`
	var args []any
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at, rowid`
	rows, err := s.db.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApprovalRow
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) setSafePoint(ctx context.Context, run RunID, node NodeID, step, attempt int, ref string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE node_executions SET safe_point_ref=? WHERE run_id=? AND node_id=? AND step=? AND attempt=?`, ref, string(run), string(node), step, attempt)
		return err
	})
}

// SafePointRef returns the safe point recorded for an attempt ("" if none).
func (e *Engine) SafePointRef(ctx context.Context, run RunID, node NodeID, step, attempt int) (string, error) {
	var ref string
	err := e.st.db.Reader().QueryRowContext(ctx, `SELECT safe_point_ref FROM node_executions WHERE run_id=? AND node_id=? AND step=? AND attempt=?`, string(run), string(node), step, attempt).Scan(&ref)
	return ref, err
}
