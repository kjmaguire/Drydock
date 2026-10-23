package core

import (
	"context"
	"errors"

	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// The workspace's crew (decision P97): every project's agents (agent.listAll) and a day's timeline of what each did
// (agent.timeline): its CLI runs (agent_runs) and the times it waited on the person (the approvals table), clipped to a window.

func init() {
	RegisterExtension(Extension{Name: "crew", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"agent.listAll": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.AllAgents(ctx) }),
			"agent.timeline": bind(func(ctx context.Context, p TimelineRequest) (any, error) {
				return c.AgentTimeline(ctx, p)
			}),
		}, nil, nil
	}})
}

// TimelineRequest asks for the crew's timeline of a window: ProjectID "" is every project; Until 0 is now.
type TimelineRequest struct {
	ProjectID string `json:"projectId"`
	Since     int64  `json:"since"` // unix ms
	Until     int64  `json:"until"` // unix ms; 0: now
}

// CrewTimeline is one lane per agent over [Since, Until].
type CrewTimeline struct {
	Since  int64       `json:"since"`
	Until  int64       `json:"until"`
	Agents []AgentLane `json:"agents"`
}

// AgentLane is one agent's runs and waits in the window, oldest first.
type AgentLane struct {
	AgentID   string     `json:"agentId"`
	Name      string     `json:"name"`
	ProjectID string     `json:"projectId"`
	Role      string     `json:"role"`
	Adapter   string     `json:"adapter"`
	Model     string     `json:"model"`
	Archived  bool       `json:"archived,omitempty"` // removed from the team since; listed for the runs it had in the window
	Runs      []RunSpan  `json:"runs"`
	Waits     []WaitSpan `json:"waits"`
}

// Outcomes of a run on the timeline.
const (
	RunWorking   = "working"
	RunFinished  = "finished"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// RunSpan is one CLI run of an agent (an agent_runs row). Start and End are clipped to the window; End is nil while the run is
// still going and the window reaches now. StartedAt and EndedAt are the run's own times (EndedAt 0 while it runs).
type RunSpan struct {
	RunID     string `json:"runId"`
	Start     int64  `json:"start"`
	End       *int64 `json:"end"`
	StartedAt int64  `json:"startedAt"`
	EndedAt   int64  `json:"endedAt"`
	Outcome   string `json:"outcome"` // working, finished, failed, cancelled
	TaskID    string `json:"taskId,omitempty"`
	TaskSeq   int    `json:"taskSeq,omitempty"`
	TaskTitle string `json:"taskTitle,omitempty"`
}

// WaitSpan is a time the agent waited on the person: a request from its creation until it was decided (or it expired or was
// withdrawn). A tool permission request is the asking agent's; a request of the task's workflow (a spec gate, a review) is
// the task's assignee's. Clipped like RunSpan; End is nil while it still waits and the window reaches now.
type WaitSpan struct {
	ApprovalID string `json:"approvalId"`
	Kind       string `json:"kind"` // approval, question, review
	Phase      string `json:"phase,omitempty"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Start      int64  `json:"start"`
	End        *int64 `json:"end"`
	CreatedAt  int64  `json:"createdAt"`
	ResolvedAt int64  `json:"resolvedAt"`
	TaskID     string `json:"taskId,omitempty"`
	TaskSeq    int    `json:"taskSeq,omitempty"`
}

// clipSpan returns a span [start, end) cut to [since, until]; end 0 means it has not ended, and live says the window reaches now
// (an unended span is then open, End nil).
func clipSpan(start, end, since, until int64, live bool) (int64, *int64) {
	if start < since {
		start = since
	}
	switch {
	case end == 0 && live:
		return start, nil
	case end == 0 || end > until:
		end = until
	}
	return start, &end
}

// AgentTimeline returns each agent's runs and waits that overlap the window, one query for the runs and one for the waits.
// Every agent of the scope has a lane, with or without spans.
func (c *Core) AgentTimeline(ctx context.Context, p TimelineRequest) (CrewTimeline, error) {
	now := storage.Now()
	until := p.Until
	if until <= 0 || until > now {
		until = now
	}
	if p.Since <= 0 || p.Since >= until {
		return CrewTimeline{}, errors.New("the timeline needs a start (since, in milliseconds) before its end")
	}
	live := until == now // a span that has not ended reaches the end of the window: it is open
	var team []Agent
	var err error
	if p.ProjectID == "" {
		team, err = c.AllAgents(ctx)
	} else {
		team, err = c.Agents(ctx, p.ProjectID)
	}
	if err != nil {
		return CrewTimeline{}, err
	}
	lanes := map[string]*AgentLane{}
	var order []string
	lane := func(id string) *AgentLane {
		if l, ok := lanes[id]; ok {
			return l
		}
		l := &AgentLane{AgentID: id, Runs: []RunSpan{}, Waits: []WaitSpan{}}
		lanes[id] = l
		order = append(order, id)
		return l
	}
	for _, a := range team {
		l := lane(a.ID)
		l.Name, l.ProjectID, l.Role, l.Adapter, l.Model = a.Name, a.ProjectID, a.Role, a.Adapter, a.Model
	}

	rows, err := c.DB.Reader().QueryContext(ctx, `
SELECT ar.id, ar.agent_id, ar.task_id, ar.status, ar.started_at, ar.ended_at, COALESCE(t.seq, 0), COALESCE(t.title, '')
FROM agent_runs ar JOIN agents a ON a.id = ar.agent_id
LEFT JOIN tasks t ON ar.task_id <> '' AND t.id = ar.task_id
WHERE (ar.ended_at = 0 OR ar.ended_at > ?) AND ar.started_at < ? AND (? = '' OR a.project_id = ?)
ORDER BY ar.started_at, ar.id`, p.Since, until, p.ProjectID, p.ProjectID)
	if err != nil {
		return CrewTimeline{}, err
	}
	for rows.Next() {
		var r RunSpan
		var agentID, status string
		if err := rows.Scan(&r.RunID, &agentID, &r.TaskID, &status, &r.StartedAt, &r.EndedAt, &r.TaskSeq, &r.TaskTitle); err != nil {
			rows.Close()
			return CrewTimeline{}, err
		}
		switch {
		case r.EndedAt == 0:
			r.Outcome = RunWorking
		case status == "FAILED":
			r.Outcome = RunFailed
		case status == "CANCELLED":
			r.Outcome = RunCancelled
		default:
			r.Outcome = RunFinished
		}
		r.Start, r.End = clipSpan(r.StartedAt, r.EndedAt, p.Since, until, live)
		l := lane(agentID)
		l.Runs = append(l.Runs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return CrewTimeline{}, err
	}

	// a request that ended without a decision time (none should) is no wait; a permission mode change is decided as it is made
	rows, err = c.DB.Reader().QueryContext(ctx, `
SELECT ap.id, ap.kind, ap.phase, ap.title, ap.status, ap.created_at, ap.resolved_at, ap.task_id, COALESCE(t.seq, 0),
  COALESCE(NULLIF(CASE WHEN ap.run_id = '' AND json_valid(CAST(ap.payload AS TEXT)) THEN json_extract(CAST(ap.payload AS TEXT), '$.action.agentId') END, ''), t.assignee, '')
FROM approvals ap LEFT JOIN tasks t ON ap.task_id <> '' AND t.id = ap.task_id
WHERE (ap.resolved_at = 0 OR ap.resolved_at > ?) AND ap.created_at < ? AND (? = '' OR ap.project_id = ?)
  AND NOT (ap.resolved_at = 0 AND ap.status <> 'REQUESTED') AND ap.action_kind <> ?
ORDER BY ap.created_at, ap.id`, p.Since, until, p.ProjectID, p.ProjectID, security.KindPermissionEscalation)
	if err != nil {
		return CrewTimeline{}, err
	}
	for rows.Next() {
		var w WaitSpan
		var agentID string
		if err := rows.Scan(&w.ApprovalID, &w.Kind, &w.Phase, &w.Title, &w.Status, &w.CreatedAt, &w.ResolvedAt, &w.TaskID, &w.TaskSeq, &agentID); err != nil {
			rows.Close()
			return CrewTimeline{}, err
		}
		if agentID == "" {
			continue // nobody's wait: a request of no task and no agent
		}
		w.Start, w.End = clipSpan(w.CreatedAt, w.ResolvedAt, p.Since, until, live)
		l := lane(agentID)
		l.Waits = append(l.Waits, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return CrewTimeline{}, err
	}

	out := CrewTimeline{Since: p.Since, Until: until, Agents: []AgentLane{}}
	for _, id := range order {
		l := lanes[id]
		if l.Name == "" { // not in the team listed above: removed since, or of another project
			a, err := c.agent(ctx, id)
			if err != nil || (p.ProjectID != "" && a.ProjectID != p.ProjectID) {
				continue
			}
			l.Name, l.ProjectID, l.Role, l.Adapter, l.Model, l.Archived = a.Name, a.ProjectID, a.Role, a.Adapter, a.Model, a.Archived
		}
		out.Agents = append(out.Agents, *l)
	}
	return out, nil
}
