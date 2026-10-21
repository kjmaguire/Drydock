package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Recorder persists decisions in routing_decisions and publishes the routing.* events (payloads carry ids and
// counts, not prompts, docs/model-routing.md s13).
type Recorder struct {
	DB  *storage.DB
	Bus events.Bus // optional
}

// Save stores the request and the decision; a stored decision can be reloaded by Next.
func (r *Recorder) Save(ctx context.Context, req RouteRequest, d RoutingDecision) error {
	if r == nil || r.DB == nil {
		return nil
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return err
	}
	decJSON, err := json.Marshal(d)
	if err != nil {
		return err
	}
	from := ""
	switch {
	case d.EscalatedFrom != nil:
		from = d.EscalatedFrom.DecisionID
	case d.DeescalatedFrom != nil:
		from = d.DeescalatedFrom.DecisionID
	}
	return r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO routing_decisions(id,ts,run_id,task_id,node_id,attempt,run_kind,level,agent,provider_id,model,effort,decided_by,
			est_cost_usd,escalated_from,request,decision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, d.Created.UnixMilli(), req.RunID, req.TaskID, req.NodeID, req.Attempt, req.RunKind, req.Level, d.Agent.Adapter+"/"+d.Agent.Instance,
			d.Provider, d.Model, d.Effort, d.DecidedBy, d.EstCostUSD, from, reqJSON, decJSON)
		return err
	})
}

// Load returns the stored request and decision.
func (r *Recorder) Load(ctx context.Context, id string) (RouteRequest, RoutingDecision, error) {
	var req RouteRequest
	var d RoutingDecision
	if r == nil || r.DB == nil {
		return req, d, fmt.Errorf("%w: decision %s (no recorder)", ErrNotFound, id)
	}
	var rb, db []byte
	err := r.DB.Reader().QueryRowContext(ctx, `SELECT request,decision FROM routing_decisions WHERE id=?`, id).Scan(&rb, &db)
	if errors.Is(err, sql.ErrNoRows) {
		return req, d, fmt.Errorf("%w: decision %s", ErrNotFound, id)
	}
	if err != nil {
		return req, d, err
	}
	if err := json.Unmarshal(rb, &req); err != nil {
		return req, d, err
	}
	if err := json.Unmarshal(db, &d); err != nil {
		return req, d, err
	}
	return req, d, nil
}

// emit publishes one routing event scoped to the request.
func (r *Recorder) emit(ctx context.Context, typ string, req RouteRequest, d RoutingDecision, extra map[string]any) {
	if r == nil || r.Bus == nil {
		return
	}
	data := map[string]any{"decisionId": d.ID, "provider": d.Provider, "model": d.Model, "effort": d.Effort, "decidedBy": d.DecidedBy,
		"alternatives": len(d.Alternatives), "policies": d.PoliciesApplied}
	for k, v := range extra {
		data[k] = v
	}
	_, _ = events.Emit(ctx, r.Bus, typ, events.Event{ProjectID: req.ProjectID, RunID: req.RunID, TaskID: req.TaskID, AgentID: req.AgentID}, data)
}

func (r *Recorder) emitBlocked(ctx context.Context, req RouteRequest, e *BlockedError) {
	if r == nil || r.Bus == nil {
		return
	}
	_, _ = events.Emit(ctx, r.Bus, "routing.blocked", events.Event{ProjectID: req.ProjectID, RunID: req.RunID, TaskID: req.TaskID, AgentID: req.AgentID},
		map[string]any{"reason": e.Reason, "rejected": len(e.Alternatives)})
}
