package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/runfiles"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The run explanation (docs/ui.md S16, brief 24: "the run is fully explained by manifest, usage, checkpoints and events"):
// for one workflow run of a task, every node execution with what its turn was given (the context manifest and the exact
// prompt), what it cost, which model was chosen and why, the checkpoints, the MCP calls and the events. Read-only. The
// workflow graph view (docs/ui.md 6.2) draws the same data.

func init() {
	RegisterExtension(Extension{Name: "explain", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"run.explain": bind(func(ctx context.Context, p struct {
				TaskID string `json:"taskId"`
				RunID  string `json:"runId"`
			}) (any, error) {
				return c.ExplainRun(ctx, p.TaskID, p.RunID)
			}),
			"run.prompt": bind(func(ctx context.Context, p struct {
				TaskID  string `json:"taskId"`
				RunID   string `json:"runId"`
				NodeID  string `json:"nodeId"`
				Step    int    `json:"step"`
				Attempt int    `json:"attempt"`
			}) (any, error) {
				return c.TurnPrompt(ctx, p.TaskID, p.RunID, p.NodeID, p.Step, p.Attempt)
			}),
		}, nil, nil
	}})
}

// RunExplanation is one run of a task, explained.
type RunExplanation struct {
	Run         *workflow.RunInfo   `json:"run,omitempty"`
	Runs        []RunSummary        `json:"runs"`
	Graph       *workflow.GraphView `json:"graph,omitempty"`
	Executions  []ExecView          `json:"executions"`
	Checkpoints []CheckpointView    `json:"checkpoints"`
	MCPCalls    []EventView         `json:"mcpCalls"`
	Events      []EventView         `json:"events"`
	// EventsTruncated says only the latest events are listed.
	EventsTruncated bool `json:"eventsTruncated,omitempty"`
}

// RunSummary names one run of the task (a task restarted after a failure has several).
type RunSummary struct {
	ID       string `json:"id"`
	Workflow string `json:"workflow"`
	Status   string `json:"status"`
	Created  int64  `json:"created"`
}

// ExecView is one node execution.
type ExecView struct {
	NodeID    string `json:"nodeId"`
	Kind      string `json:"kind,omitempty"`
	Step      int    `json:"step"`
	Attempt   int    `json:"attempt"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Started   int64  `json:"started"`
	Finished  int64  `json:"finished,omitempty"`
	SafePoint string `json:"safePoint,omitempty"`
	// LogID is the Logs tab id of this execution's stream, when it streamed something.
	LogID     string        `json:"logId,omitempty"`
	HasPrompt bool          `json:"hasPrompt,omitempty"`
	AgentID   string        `json:"agentId,omitempty"`
	Manifest  []ctxeng.Item `json:"manifest,omitempty"`
	Included  int           `json:"includedBytes,omitempty"`
	Usage     []UsageRow    `json:"usage,omitempty"`
	Decision  *DecisionView `json:"decision,omitempty"`
}

// DecisionView is the routing decision recorded for a turn.
type DecisionView struct {
	ID            string   `json:"id"`
	DecidedBy     string   `json:"decidedBy"`
	Agent         string   `json:"agent,omitempty"`
	ProviderID    string   `json:"providerId,omitempty"`
	Model         string   `json:"model,omitempty"`
	Effort        string   `json:"effort,omitempty"`
	Level         string   `json:"level,omitempty"`
	EstCostUSD    *float64 `json:"estCostUsd,omitempty"`
	EscalatedFrom string   `json:"escalatedFrom,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

// CheckpointView is one checkpoint, without the full state.
type CheckpointView struct {
	ID        string   `json:"id"`
	Step      int      `json:"step"`
	Kind      string   `json:"kind"`
	Parent    string   `json:"parent,omitempty"`
	Created   int64    `json:"created"`
	Status    string   `json:"status"`
	LastNodes []string `json:"lastNodes,omitempty"`
	Waiting   []string `json:"waiting,omitempty"`
	Latest    bool     `json:"latest,omitempty"`
}

// EventView is one persisted event.
type EventView struct {
	Seq     int64           `json:"seq"`
	TS      int64           `json:"ts"`
	Type    string          `json:"type"`
	AgentID string          `json:"agentId,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

const explainEventLimit = 300

// ExplainRun explains a run of a task: runID, or the task's current run.
func (c *Core) ExplainRun(ctx context.Context, taskID, runID string) (RunExplanation, error) {
	out := RunExplanation{Runs: []RunSummary{}, Executions: []ExecView{}, Checkpoints: []CheckpointView{}, MCPCalls: []EventView{}, Events: []EventView{}}
	if taskID == "" {
		return out, errors.New("explain: name a task")
	}
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return out, err
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, workflow, status, created_at FROM workflow_runs WHERE task_id=? ORDER BY created_at`, taskID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r RunSummary
		if err := rows.Scan(&r.ID, &r.Workflow, &r.Status, &r.Created); err != nil {
			rows.Close()
			return out, err
		}
		out.Runs = append(out.Runs, r)
	}
	rows.Close()
	if runID == "" {
		runID = t.RunID
	}
	if runID == "" {
		return out, nil // never started
	}
	known := false
	for _, r := range out.Runs {
		known = known || r.ID == runID
	}
	if !known {
		return out, fmt.Errorf("explain: run %s is not a run of this task", runID)
	}
	info, err := c.Engine.Status(ctx, workflow.RunID(runID))
	if err != nil {
		return out, err
	}
	info.Input, info.Result = nil, nil // the input is the task spec; the result is in the evidence
	out.Run = &info
	var kinds map[string]string
	if g, err := c.Engine.Describe(info.Workflow); err == nil {
		out.Graph = &g
		kinds = map[string]string{}
		for _, n := range g.Nodes {
			kinds[string(n.ID)] = string(n.Kind)
		}
	}

	execs, err := c.Engine.Executions(ctx, workflow.RunID(runID))
	if err != nil {
		return out, err
	}
	decisions := c.runDecisions(ctx, runID)
	// the manifests of the run and the usage of the task are read once, not once per execution (audit 2026-10-16, L18): a run of
	// hundreds of turns asked the database twice for each
	manifests := c.runManifests(ctx, runID)
	usage := c.taskUsage(ctx, taskID)
	for _, x := range execs {
		v := ExecView{NodeID: string(x.NodeID), Kind: kinds[string(x.NodeID)], Step: x.Step, Attempt: x.Attempt, Status: x.Status, Error: x.Error,
			Started: x.Started, Finished: x.Finished, SafePoint: x.SafePoint}
		if x.PartialRef != "" {
			if size, _, ok := runfiles.Stat(x.PartialRef); ok && (size > 0 || x.Status == "started") {
				v.LogID = fmt.Sprintf("nx:%s|%s|%d|%d", runID, x.NodeID, x.Step, x.Attempt)
			}
		}
		if p := pipeline.PromptPath(c.cfg.DataDir, workflow.NodeInfo{RunID: workflow.RunID(runID), NodeID: x.NodeID, Step: x.Step, Attempt: x.Attempt}); p != "" {
			if _, _, ok := runfiles.Stat(p); ok {
				v.HasPrompt = true
			}
		}
		if m, ok := manifests[manifestKey{string(x.NodeID), x.Attempt}]; ok {
			_ = json.Unmarshal([]byte(m.items), &v.Manifest)
			v.AgentID, v.Included = m.agentID, m.total
		}
		// a node runs at several steps with the same attempt number: the decision is the one made while this execution ran
		for i := range decisions {
			d := decisions[i]
			if d.node == string(x.NodeID) && d.attempt == x.Attempt && d.ts >= x.Started && (x.Finished == 0 || d.ts <= x.Finished) {
				v.Decision = &decisions[i].DecisionView
			}
		}
		if x.Started > 0 && (kinds == nil || kinds[string(x.NodeID)] == string(workflow.KindAgentTask) || v.AgentID != "") {
			v.Usage = usageBetween(usage, x.Started, x.Finished)
		}
		out.Executions = append(out.Executions, v)
	}

	cps, err := c.Engine.RunCheckpoints(ctx, workflow.RunID(runID))
	if err == nil {
		for _, cp := range cps {
			cv := CheckpointView{ID: string(cp.ID), Step: cp.Step, Kind: string(cp.Kind), Parent: string(cp.Parent), Created: cp.Created,
				Status: string(cp.State.Status), Latest: cp.ID == info.LatestCP}
			for _, n := range cp.State.LastNodes {
				cv.LastNodes = append(cv.LastNodes, string(n))
			}
			for n := range cp.State.Waiting {
				cv.Waiting = append(cv.Waiting, string(n))
			}
			out.Checkpoints = append(out.Checkpoints, cv)
		}
	}

	evRows, err := c.DB.Reader().QueryContext(ctx, `SELECT seq, ts, type, agent_id, data FROM events WHERE run_id=? ORDER BY seq DESC LIMIT ?`, runID, explainEventLimit+1)
	if err != nil {
		return out, err
	}
	defer evRows.Close()
	for evRows.Next() {
		var e EventView
		var data []byte
		if err := evRows.Scan(&e.Seq, &e.TS, &e.Type, &e.AgentID, &data); err != nil {
			return out, err
		}
		if len(out.Events) == explainEventLimit {
			out.EventsTruncated = true
			break
		}
		switch e.Type {
		case "mcp.call", "mcp.denied", "mcp.egress_blocked", "mcp.secret_redacted", "mcp.auth_failed":
			m := e
			m.Data = data
			out.MCPCalls = append(out.MCPCalls, m)
		}
		out.Events = append(out.Events, e)
	}
	// read newest first so the limit keeps the newest; shown oldest first
	slices.Reverse(out.Events)
	slices.Reverse(out.MCPCalls)
	return out, evRows.Err()
}

type timedDecision struct {
	DecisionView
	node    string
	attempt int
	ts      int64
}

// runDecisions returns the routing decisions of a run, oldest first.
func (c *Core) runDecisions(ctx context.Context, runID string) []timedDecision {
	var out []timedDecision
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, ts, node_id, attempt, decided_by, agent, provider_id, model, effort, level, est_cost_usd, escalated_from, decision
		FROM routing_decisions WHERE run_id=? ORDER BY ts`, runID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var d timedDecision
		var cost *float64
		var raw []byte
		if err := rows.Scan(&d.ID, &d.ts, &d.node, &d.attempt, &d.DecidedBy, &d.Agent, &d.ProviderID, &d.Model, &d.Effort, &d.Level, &cost, &d.EscalatedFrom, &raw); err != nil {
			return out
		}
		d.EstCostUSD = cost
		var dec struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(raw, &dec) == nil {
			d.Reason = dec.Reason
		}
		out = append(out, d)
	}
	return out
}

// explainUsageLimit is the most usage rows of one task an explanation reads.
const explainUsageLimit = 20000

// taskUsage is the task's usage rows, oldest first (at most explainUsageLimit).
func (c *Core) taskUsage(ctx context.Context, taskID string) []UsageRow {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT ts,task_id,agent_id,provider,model,tier,input_tokens,output_tokens,cached_tokens,cache_write_tokens,reasoning_tokens,cost_usd,source,cache_hit
		FROM model_usage WHERE task_id=? ORDER BY id LIMIT ?`, taskID, explainUsageLimit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var u UsageRow
		var hit int
		if err := rows.Scan(&u.TS, &u.TaskID, &u.AgentID, &u.Provider, &u.Model, &u.Tier, &u.Input, &u.Output, &u.Cached, &u.CacheWrite, &u.Reasoning, &u.CostUSD, &u.Source, &hit); err != nil {
			return out
		}
		u.CacheHit = hit != 0
		out = append(out, u)
	}
	return out
}

// usageBetween is the part of a task's usage recorded while a node execution ran (finished 0: still running), in the order it
// was recorded.
func usageBetween(all []UsageRow, from, to int64) []UsageRow {
	if to == 0 {
		to = 1<<62 - 1
	}
	var out []UsageRow
	for _, u := range all {
		if u.TS >= from && u.TS <= to {
			out = append(out, u)
		}
	}
	return out
}

type manifestKey struct {
	node    string
	attempt int
}

type manifestRow struct {
	items, agentID string
	total          int
}

// runManifests is the context manifests of a run by node and attempt (the primary key of the table).
func (c *Core) runManifests(ctx context.Context, runID string) map[manifestKey]manifestRow {
	out := map[manifestKey]manifestRow{}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT node_id, attempt, items, agent_id, total_bytes FROM context_manifests WHERE run_id=?`, runID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k manifestKey
		var m manifestRow
		if err := rows.Scan(&k.node, &k.attempt, &m.items, &m.agentID, &m.total); err != nil {
			return out
		}
		out[k] = m
	}
	return out
}

// TurnPrompt returns the exact prompt a turn of the task's run was sent.
func (c *Core) TurnPrompt(ctx context.Context, taskID, runID, nodeID string, step, attempt int) (string, error) {
	// the turn must be a recorded execution of a run of this task: no name from the caller reaches the path otherwise
	var n int
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM node_executions n JOIN workflow_runs r ON r.id = n.run_id
		WHERE r.id=? AND r.task_id=? AND n.node_id=? AND n.step=? AND n.attempt=?`, runID, taskID, nodeID, step, attempt).Scan(&n); err != nil || n == 0 {
		return "", errors.New("explain: no such turn in this task's runs")
	}
	p := pipeline.PromptPath(c.cfg.DataDir, workflow.NodeInfo{RunID: workflow.RunID(runID), NodeID: workflow.NodeID(nodeID), Step: step, Attempt: attempt})
	if p == "" {
		return "", errors.New("explain: no prompt was kept for this turn")
	}
	b, err := runfiles.ReadAll(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("explain: no prompt was kept for this turn")
	}
	return string(b), err
}
