package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The team lead service: one read-only supervision turn per trigger, a strictly validated JSON answer (one retry, then an
// escalation to the human), the action applied, the intervention stored, posted in the task room by the lead and published
// as lead.intervened. Bounds: at most MaxPerRun interventions per task and run, a turn that failed included (it spent tokens
// too), and one supervision turn in flight per task.

// Bounds of the lead's answer and digest.
const (
	DefaultLeadMaxPerRun = 3
	maxLeadMessage       = 2000
	maxLeadReason        = 600
	maxLeadSplitTasks    = 5
	leadTurnTimeout      = 10 * time.Minute
	maxStandupTasks      = 30
	maxStandupText       = 4000
)

// LeadMember is a team member as the lead sees it.
type LeadMember struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Adapter string `json:"adapter"`
	Load    int    `json:"load"` // open tasks it works on
}

// LeadDeps are what the lead service needs from the rest of the app.
type LeadDeps struct {
	Tasks   *tasks.Service
	Runtime *rt.Runtime
	Bus     events.Bus
	// Team lists the project's (non-archived) agents with their open-task load.
	Team func(ctx context.Context, projectID string) ([]LeadMember, error)
	// Config builds the runtime config of an agent (adapter, model, effort); the lead's turn is made read-only here.
	Config func(ctx context.Context, agentID string) (rt.Config, error)
	// Cwd is where the lead's read-only turn runs: the task's worktree when it has one, else the repository root.
	Cwd func(ctx context.Context, projectID, taskID string) string
	// Assign sets a task's worker and reviewer (both on the team, different agents).
	Assign func(ctx context.Context, taskID, worker, reviewer string) error
	// Post posts a message authored by the lead: in the task's room (taskID set) or the project's general channel. It returns
	// the message id.
	Post func(ctx context.Context, projectID, taskID, agentID, body string) (string, error)
	// Budget refuses the lead's turn when a budget is used up (nil: no budgets).
	Budget func(ctx context.Context, projectID, taskID, agentID string) error
	// Redact scrubs secrets from text before it is stored, published or posted (nil: none).
	Redact func(string) string
	// Brief is the project brief (Deps.ProjectBrief; nil: none).
	Brief func(ctx context.Context, projectID string) string
	// Steer delivers the lead's nudge or escalation to a task's running turn (watchdog.Watchdog.Steer); the monitor's
	// triggers use it. Nil: the message is only posted.
	Steer func(ctx context.Context, taskID, msg string, escalate bool) bool
	// Dev reads a project's development settings (stall minutes, automatic scheduling).
	Dev func(ctx context.Context, projectID string) DevSettings
	// MaxPerRun bounds interventions per task and run (default 3).
	MaxPerRun int
	// Now is the clock (tests inject one; default time.Now).
	Now func() time.Time
}

// Lead is the team lead service. It implements LeadSupervisor.
type Lead struct {
	d          LeadDeps
	mu         sync.Mutex
	inflight   map[string]bool
	stalledAt  map[string]int64
	unassigned map[string]bool
}

// NewLead returns the lead service.
func NewLead(d LeadDeps) *Lead {
	return &Lead{d: d, inflight: map[string]bool{}, stalledAt: map[string]int64{}, unassigned: map[string]bool{}}
}

func (l *Lead) now() time.Time {
	if l.d.Now != nil {
		return l.d.Now()
	}
	return time.Now()
}

func (l *Lead) maxPerRun() int {
	if l.d.MaxPerRun > 0 {
		return l.d.MaxPerRun
	}
	return DefaultLeadMaxPerRun
}

func (l *Lead) redact(s string) string {
	if l.d.Redact != nil {
		return l.d.Redact(s)
	}
	return s
}

// FindLead returns the team's lead (the first agent whose role is a lead role).
func FindLead(team []LeadMember) (LeadMember, bool) {
	for _, m := range team {
		if IsLeadRole(m.Role) {
			return m, true
		}
	}
	return LeadMember{}, false
}

// HasLead reports whether the task's project has a lead that may supervise the task's run now: the trigger is not the lead's
// own turn and the per-run bound is not used up. It runs no agent.
func (l *Lead) HasLead(ctx context.Context, taskID, runID, nodeAgent string) bool {
	t, err := l.d.Tasks.Get(ctx, taskID)
	if err != nil {
		return false
	}
	team, err := l.d.Team(ctx, t.ProjectID)
	if err != nil {
		return false
	}
	lead, ok := FindLead(team)
	if !ok || l.ownTurn(t, nodeAgent, lead.ID) {
		return false
	}
	n, _ := l.count(ctx, t.ID, runID)
	return n < l.maxPerRun()
}

func (l *Lead) ownTurn(t tasks.Task, nodeAgent, leadID string) bool {
	switch nodeAgent {
	case "work":
		return t.Assignee == leadID
	case "review":
		return t.Reviewer == leadID
	case "lead":
		return true
	}
	return false
}

func (l *Lead) count(ctx context.Context, taskID, runID string) (int, error) {
	var n int
	err := l.d.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM lead_interventions WHERE task_id=? AND run_id=?`, taskID, runID).Scan(&n)
	return n, err
}

func (l *Lead) acquire(taskID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight[taskID] {
		return false
	}
	l.inflight[taskID] = true
	return true
}

func (l *Lead) release(taskID string) {
	l.mu.Lock()
	delete(l.inflight, taskID)
	l.mu.Unlock()
}

// Supervise runs one supervision turn for a trigger and applies the reassignment or split it decides. The caller applies
// nudge, escalate and continue in its own context (see LeadOutcome).
func (l *Lead) Supervise(ctx context.Context, tr LeadTrigger) LeadOutcome {
	t, err := l.d.Tasks.Get(ctx, tr.TaskID)
	if err != nil || t.Status.Terminal() {
		return LeadOutcome{}
	}
	team, err := l.d.Team(ctx, t.ProjectID)
	if err != nil {
		return LeadOutcome{}
	}
	lead, ok := FindLead(team)
	if !ok || l.ownTurn(t, tr.NodeAgent, lead.ID) {
		return LeadOutcome{}
	}
	n, err := l.count(ctx, t.ID, tr.RunID)
	if err != nil {
		return LeadOutcome{}
	}
	if n >= l.maxPerRun() {
		return LeadOutcome{Exhausted: true, LeadID: lead.ID, LeadName: lead.Name}
	}
	if !l.acquire(t.ID) {
		return LeadOutcome{}
	}
	defer l.release(t.ID)
	if l.d.Budget != nil {
		if err := l.d.Budget(ctx, t.ProjectID, t.ID, lead.ID); err != nil {
			return LeadOutcome{}
		}
	}
	prompt := LeadPrompt(l.digest(ctx, t, tr, team, lead, n))
	dec, agentID, problems, err := l.ask(ctx, t, tr.RunID, lead, prompt, func(text string) (LeadDecision, string, []string) {
		return ParseLeadDecision(text, LeadCheck{Team: team, Lead: lead, Task: t, Trigger: tr.Kind})
	})
	if err != nil {
		// stored, so it counts toward the bound: a lead whose turns keep failing on a stalled run would otherwise be asked
		// again at every silence, each time spending tokens; the trigger site still behaves as without a lead
		l.recordFailed(ctx, t, tr, lead, err)
		return LeadOutcome{}
	}
	out := LeadOutcome{Intervened: true, LeadID: lead.ID, LeadName: lead.Name}
	if len(problems) > 0 {
		out.Invalid = true
		dec = LeadDecision{Action: LeadEscalate, Message: "The team lead's answer could not be used (" + strings.Join(problems, "; ") + "). A human needs to decide.", Reason: "invalid answer twice"}
	}
	switch dec.Action {
	case LeadReassign:
		if err := l.reassign(ctx, t, team, lead, agentID); err != nil {
			dec = LeadDecision{Action: LeadEscalate, Message: "The team lead wanted to give the task to another agent, but that failed: " + err.Error(), Reason: dec.Reason}
		} else {
			out.AgentID = agentID
		}
	case LeadSplit:
		created, err := l.split(ctx, t, lead, dec.Tasks)
		out.CreatedTasks = created
		if err != nil {
			dec = LeadDecision{Action: LeadEscalate, Message: "The team lead wanted to split the task, but creating the follow-ups failed: " + err.Error(), Reason: dec.Reason}
		}
	}
	dec.Message, dec.Reason = l.redact(dec.Message), l.redact(dec.Reason)
	out.Decision = dec
	out.InterventionID = l.record(ctx, t, tr, lead, out)
	return out
}

// ask runs the lead's turn and validates the answer; an invalid answer gets one retry with the validator's problems.
func (l *Lead) ask(ctx context.Context, t tasks.Task, runID string, lead LeadMember, prompt string, parse func(string) (LeadDecision, string, []string)) (LeadDecision, string, []string, error) {
	text, err := l.turn(ctx, t.ProjectID, t.ID, runID, lead, prompt)
	if err != nil {
		return LeadDecision{}, "", nil, err
	}
	dec, agentID, problems := parse(text)
	if len(problems) == 0 {
		return dec, agentID, nil, nil
	}
	retry := prompt + "\n\nYour previous answer was rejected by the validator. Fix every point and answer again with the JSON object only:\n" + quoted("Validator output", "- "+strings.Join(problems, "\n- "))
	text, err = l.turn(ctx, t.ProjectID, t.ID, runID, lead, retry)
	if err != nil {
		return LeadDecision{}, "", nil, err
	}
	dec, agentID, problems = parse(text)
	return dec, agentID, problems, nil
}

// turn runs one read-only lead turn through the agent runtime (the same path as every other turn, so the lead's purpose,
// practices and MCP gateway apply) and returns its final text.
func (l *Lead) turn(ctx context.Context, projectID, taskID, runID string, lead LeadMember, prompt string) (string, error) {
	if l.d.Runtime == nil || l.d.Config == nil {
		return "", errors.New("lead: no agent runtime")
	}
	cfg, err := l.d.Config(ctx, lead.ID)
	if err != nil {
		return "", err
	}
	cfg.ProjectID, cfg.TaskID = projectID, taskID
	cfg.Spec.PermissionMode, cfg.Spec.ReadOnly = "plan", true // the lead reads; it never edits
	cfg.Spec.Role = RoleLead
	if l.d.Cwd != nil {
		cfg.Spec.Cwd = l.d.Cwd(ctx, projectID, taskID)
	}
	tctx, cancel := context.WithTimeout(ctx, leadTurnTimeout)
	defer cancel()
	in := workflow.NodeInput{NodeInfo: workflow.NodeInfo{RunID: workflow.RunID(runID), ProjectID: projectID, TaskID: taskID, NodeID: "lead", Kind: workflow.KindAgentTask, Meta: map[string]string{"agent": "lead"}}}
	out, err := l.d.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return prompt }})(tctx, in)
	if err != nil {
		return "", err
	}
	var res rt.TurnResult
	_ = json.Unmarshal(out.Value, &res)
	if taskID != "" {
		var cost float64
		for _, u := range res.Usage {
			cost += u.CostUSD
		}
		_, _ = l.d.Tasks.Patch(ctx, taskID, tasks.Update{AddCost: cost})
	}
	return res.Text, nil
}

// reassign gives the task to agentID; the reviewer stays unless it is missing or the new worker, then an independent one is
// picked (a reviewer-role agent first; never the lead, never the worker).
func (l *Lead) reassign(ctx context.Context, t tasks.Task, team []LeadMember, lead LeadMember, agentID string) error {
	if l.d.Assign == nil {
		return errors.New("assigning is not wired in this build")
	}
	reviewer := t.Reviewer
	if reviewer == "" || reviewer == agentID {
		reviewer = pickReviewer(team, agentID, lead.ID)
	}
	if reviewer == "" {
		return errors.New("no other agent on the team can review the work")
	}
	return l.d.Assign(ctx, t.ID, agentID, reviewer)
}

func pickReviewer(team []LeadMember, worker, leadID string) string {
	for _, pass := range []bool{true, false} {
		for _, m := range team {
			if m.ID == worker || m.ID == leadID || IsLeadRole(m.Role) {
				continue
			}
			if pass && NormalizePlanRole(m.Role) != "reviewer" {
				continue
			}
			return m.ID
		}
	}
	return ""
}

// split creates the lead's follow-up tasks in BACKLOG, with the lead's text quoted as agent-authored.
func (l *Lead) split(ctx context.Context, t tasks.Task, lead LeadMember, ts []LeadTask) ([]string, error) {
	var created []string
	for _, nt := range ts {
		desc := fmt.Sprintf("Split from #%d %q by the team lead (%s). The text below was written by an agent:\n\n%s", t.Seq, t.Title, lead.Name,
			quoted("The team lead's description", l.redact(nt.Description)))
		task, err := l.d.Tasks.Create(ctx, tasks.CreateReq{ProjectID: t.ProjectID, Title: l.redact(nt.Title), Description: desc, Acceptance: nt.AcceptanceCriteria, Priority: t.Priority})
		if err != nil {
			return created, err
		}
		created = append(created, task.ID)
	}
	return created, nil
}

// record stores the intervention, posts it in the task room as the lead and publishes lead.intervened.
func (l *Lead) record(ctx context.Context, t tasks.Task, tr LeadTrigger, lead LeadMember, o LeadOutcome) string {
	id := ids.New("lint")
	created, _ := json.Marshal(nonNil(o.CreatedTasks))
	body := l.postBody(ctx, t, tr, lead, o)
	msgID := ""
	if l.d.Post != nil {
		if m, err := l.d.Post(ctx, t.ProjectID, t.ID, lead.ID, body); err == nil {
			msgID = m
		}
	}
	evidence := clip(l.redact(tr.Evidence), 1000)
	_ = l.d.Tasks.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO lead_interventions(id,project_id,task_id,run_id,lead_agent_id,trigger_kind,action,message,reason,target_agent,created_tasks,evidence,invalid,message_id,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, t.ProjectID, t.ID, tr.RunID, lead.ID, tr.Kind, o.Decision.Action, o.Decision.Message, o.Decision.Reason,
			o.AgentID, string(created), evidence, b2i(o.Invalid), msgID, storage.Now())
		return err
	})
	if l.d.Bus != nil {
		data, _ := json.Marshal(map[string]any{"taskId": t.ID, "trigger": tr.Kind, "action": o.Decision.Action, "reason": clip(o.Decision.Reason, 300),
			"interventionId": id, "agent": o.AgentID, "createdTasks": nonNil(o.CreatedTasks), "invalid": o.Invalid})
		_, _ = l.d.Bus.Publish(ctx, events.Event{Type: "lead.intervened", ProjectID: t.ProjectID, TaskID: t.ID, RunID: tr.RunID, AgentID: lead.ID, Data: data})
	}
	return id
}

// recordFailed stores a supervision turn that failed (action failed, the error as its message) and publishes lead.intervened,
// without a post in the task room. It is stored even when ctx has ended (the app closing mid-turn): the turn spent tokens.
func (l *Lead) recordFailed(ctx context.Context, t tasks.Task, tr LeadTrigger, lead LeadMember, cause error) {
	ctx = context.WithoutCancel(ctx)
	id := ids.New("lint")
	msg := clip(l.redact("The team lead's turn failed: "+cause.Error()), maxLeadMessage)
	_ = l.d.Tasks.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO lead_interventions(id,project_id,task_id,run_id,lead_agent_id,trigger_kind,action,message,evidence,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, id, t.ProjectID, t.ID, tr.RunID, lead.ID, tr.Kind, LeadFailed, msg, clip(l.redact(tr.Evidence), 1000), storage.Now())
		return err
	})
	if l.d.Bus != nil {
		data, _ := json.Marshal(map[string]any{"taskId": t.ID, "trigger": tr.Kind, "action": LeadFailed, "interventionId": id, "createdTasks": []string{}})
		_, _ = l.d.Bus.Publish(ctx, events.Event{Type: "lead.intervened", ProjectID: t.ProjectID, TaskID: t.ID, RunID: tr.RunID, AgentID: lead.ID, Data: data})
	}
}

// asked reports whether the lead was already asked about a trigger for the task (and run, unless runID is empty) at or after
// since (ms), whether its turn succeeded or failed. It reads the stored interventions, so a restart does not ask again.
func (l *Lead) asked(ctx context.Context, taskID, runID, kind string, since int64) bool {
	var n int
	err := l.d.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM lead_interventions WHERE task_id=? AND trigger_kind=? AND created_at>=? AND (?='' OR run_id=?)`,
		taskID, kind, since, runID, runID).Scan(&n)
	return err == nil && n > 0
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

var triggerLabels = map[string]string{
	TriggerWatchdog:       "the watchdog stopped a turn",
	TriggerIterationLimit: "a loop ran out of its budget",
	TriggerRepeatedRevise: "the reviewer repeated the same feedback",
	TriggerStalled:        "no progress for a while",
	TriggerUnassigned:     "nobody is assigned",
}

func (l *Lead) postBody(ctx context.Context, t tasks.Task, tr LeadTrigger, lead LeadMember, o LeadOutcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Team lead: %s (%s).\n", o.Decision.Action, triggerLabels[tr.Kind])
	if m := strings.TrimSpace(o.Decision.Message); m != "" {
		b.WriteString(m + "\n")
	}
	if o.AgentID != "" {
		name := o.AgentID
		if team, err := l.d.Team(ctx, t.ProjectID); err == nil {
			for _, m := range team {
				if m.ID == o.AgentID {
					name = m.Name
				}
			}
		}
		fmt.Fprintf(&b, "The task now goes to %s.\n", name)
	}
	for _, id := range o.CreatedTasks {
		if nt, err := l.d.Tasks.Get(ctx, id); err == nil {
			fmt.Fprintf(&b, "Follow-up task #%d: %s (BACKLOG)\n", nt.Seq, nt.Title)
		}
	}
	if r := strings.TrimSpace(o.Decision.Reason); r != "" {
		b.WriteString("Reason: " + r)
	}
	return strings.TrimSpace(b.String())
}

// ---- the answer ----

// LeadCheck is what an answer is validated against.
type LeadCheck struct {
	Team    []LeadMember
	Lead    LeadMember
	Task    tasks.Task
	Trigger string
}

// ParseLeadDecision validates the lead's reply strictly: exactly one JSON object, no unknown fields, a known action, a message
// and a reason within bounds, "agent" only for reassign (a team member who is neither the lead, the reviewer nor already the
// worker), "tasks" only for split (1 to 5 tasks, each with a one-line title and Given/When/Then criteria). It returns the
// decision, the resolved agent id of a reassignment, and the problems (fed back to the lead once).
func ParseLeadDecision(text string, c LeadCheck) (LeadDecision, string, []string) {
	var d LeadDecision
	js, err := extractJSON(text)
	if err != nil {
		return d, "", []string{err.Error()}
	}
	dec := json.NewDecoder(strings.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return LeadDecision{}, "", []string{"the reply is not valid for the schema: " + err.Error()}
	}
	if _, err := dec.Token(); err != io.EOF {
		return LeadDecision{}, "", []string{"there is more than one JSON value in the reply; reply with one object"}
	}
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	d.Action = strings.ToLower(strings.TrimSpace(d.Action))
	d.Message, d.Reason = strings.TrimSpace(cleanText(d.Message)), strings.TrimSpace(cleanText(d.Reason))
	switch d.Action {
	case LeadNudge, LeadReassign, LeadSplit, LeadEscalate, LeadContinue:
	default:
		add("action %q is not one of nudge, reassign, split, escalate, continue", d.Action)
	}
	if d.Message == "" && d.Action != LeadContinue {
		add("message is required for %s", d.Action)
	}
	if len(d.Message) > maxLeadMessage || !utf8.ValidString(d.Message) {
		add("message is longer than %d bytes", maxLeadMessage)
	}
	if d.Reason == "" {
		add("reason is required")
	}
	if len(d.Reason) > maxLeadReason {
		add("reason is longer than %d bytes", maxLeadReason)
	}
	if c.Trigger == TriggerUnassigned && (d.Action == LeadNudge || d.Action == LeadSplit) {
		add("nobody works on this task yet: answer reassign (name the worker), escalate or continue")
	}
	agentID := ""
	if d.Action == LeadReassign {
		m, ok := resolveMember(c.Team, d.Agent)
		switch {
		case strings.TrimSpace(d.Agent) == "":
			add("reassign needs \"agent\": the name of a team member")
		case !ok:
			add("agent %q is not on this team; use one of: %s", d.Agent, memberNames(c.Team, c.Lead.ID))
		case m.ID == c.Lead.ID || IsLeadRole(m.Role):
			add("the team lead never takes a task itself")
		case m.ID == c.Task.Reviewer:
			add("%s reviews this task; the worker and the reviewer must be different agents", m.Name)
		case m.ID == c.Task.Assignee:
			add("%s already works on this task; reassigning to it changes nothing (use nudge)", m.Name)
		default:
			agentID = m.ID
		}
	} else if strings.TrimSpace(d.Agent) != "" {
		add("\"agent\" is only for reassign")
	}
	if d.Action == LeadSplit {
		if len(d.Tasks) == 0 || len(d.Tasks) > maxLeadSplitTasks {
			add("split needs 1 to %d tasks", maxLeadSplitTasks)
		}
		for i := range d.Tasks {
			nt := &d.Tasks[i]
			nt.Title, nt.Description = strings.TrimSpace(cleanText(nt.Title)), strings.TrimSpace(cleanText(nt.Description))
			if nt.Title == "" || utf8.RuneCountInString(nt.Title) > maxTitleRunes || strings.ContainsAny(nt.Title, "\n\t") {
				add("task %d: the title must be one line of 1 to %d characters", i+1, maxTitleRunes)
			}
			if len(nt.Description) > maxDescBytes {
				add("task %d: the description is longer than %d bytes", i+1, maxDescBytes)
			}
			if len(nt.AcceptanceCriteria) == 0 || len(nt.AcceptanceCriteria) > maxAcceptances {
				add("task %d: needs 1 to %d acceptance criteria", i+1, maxAcceptances)
			}
			for j, a := range nt.AcceptanceCriteria {
				a = strings.TrimSpace(cleanText(a))
				nt.AcceptanceCriteria[j] = a
				if cs, probs := ParseCriteriaLines([]string{a}); len(probs) > 0 || len(cs) != 1 || len(a) > maxAcceptBytes {
					add("task %d: criterion %d is not one complete Given/When/Then sentence", i+1, j+1)
				}
			}
		}
	} else if len(d.Tasks) > 0 {
		add("\"tasks\" is only for split")
	}
	if len(problems) > 0 {
		return LeadDecision{}, "", problems
	}
	return d, agentID, nil
}

func resolveMember(team []LeadMember, s string) (LeadMember, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "@"))
	for _, m := range team {
		if m.ID == s {
			return m, true
		}
	}
	for _, m := range team {
		if strings.EqualFold(m.Name, s) {
			return m, true
		}
	}
	return LeadMember{}, false
}

func memberNames(team []LeadMember, leadID string) string {
	var n []string
	for _, m := range team {
		if m.ID != leadID {
			n = append(n, m.Name)
		}
	}
	return strings.Join(n, ", ")
}

// ---- the digest ----

// LeadDigest is everything the lead is shown for one trigger.
type LeadDigest struct {
	Trigger       string
	Evidence      string
	Task          tasks.Task
	Brief         string
	ToolCalls     []string
	FailingCheck  string
	Reviews       []string // the last review feedbacks, newest first
	LastText      string
	DiffStat      string
	Team          []LeadMember
	Lead          LeadMember
	Interventions int
	Max           int
}

func (l *Lead) digest(ctx context.Context, t tasks.Task, tr LeadTrigger, team []LeadMember, lead LeadMember, n int) LeadDigest {
	dg := LeadDigest{Trigger: tr.Kind, Evidence: tr.Evidence, Task: t, Team: team, Lead: lead, Interventions: n, Max: l.maxPerRun(), LastText: tr.LastText}
	if l.d.Brief != nil {
		dg.Brief = clip(strings.TrimSpace(l.d.Brief(ctx, t.ProjectID)), maxBriefPrompt)
	}
	if ev, err := l.d.Tasks.Evidence(ctx, t.ID); err == nil {
		for i := len(ev) - 1; i >= 0; i-- {
			if ev[i].Passed != nil && !*ev[i].Passed && dg.FailingCheck == "" {
				dg.FailingCheck = ev[i].Kind + ": " + clip(ev[i].Summary, 400)
			}
			if ev[i].Kind == "diff" && dg.DiffStat == "" {
				dg.DiffStat = ev[i].Summary
			}
		}
	}
	if rv, err := l.d.Tasks.Reviews(ctx, t.ID); err == nil {
		for i := len(rv) - 1; i >= 0 && len(dg.Reviews) < 2; i-- {
			if rv[i].Verdict == "REVISE" {
				dg.Reviews = append(dg.Reviews, clip(rv[i].Feedback, 800))
			}
		}
	}
	calls, last := l.recentActivity(ctx, t.ID)
	dg.ToolCalls = calls
	if dg.LastText == "" {
		dg.LastText = last
	}
	return dg
}

// recentActivity reads the task's latest tool calls (up to 8, newest last) and the last final message from the event log.
func (l *Lead) recentActivity(ctx context.Context, taskID string) ([]string, string) {
	rows, err := l.d.Tasks.DB.Reader().QueryContext(ctx, `SELECT data FROM events WHERE task_id=? AND type='agent.output' ORDER BY seq DESC LIMIT 400`, taskID)
	if err != nil {
		return nil, ""
	}
	defer rows.Close()
	var calls []string
	last := ""
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			continue
		}
		var e struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		switch e.Kind {
		case workflow.ChunkToolCall:
			if len(calls) < 8 {
				var tc workflow.ToolCall
				if json.Unmarshal(e.Data, &tc) == nil {
					calls = append(calls, tc.Name+" "+clip(oneLine(string(tc.Args)), 120))
				}
			}
		case workflow.ChunkMessageEnd:
			if last == "" {
				var d workflow.TextDelta
				if json.Unmarshal(e.Data, &d) == nil {
					last = clip(strings.TrimSpace(d.Text), 1500)
				}
			}
		}
	}
	for i, j := 0, len(calls)-1; i < j; i, j = i+1, j-1 {
		calls[i], calls[j] = calls[j], calls[i]
	}
	return calls, last
}

// LeadPrompt renders the supervision prompt: the digest, then the answer format. Agent-written text is quoted data.
func LeadPrompt(d LeadDigest) string {
	var b strings.Builder
	b.WriteString("Drydock phase: supervise\n")
	fmt.Fprintf(&b, "You are %s, the team lead. Drydock asks you because %s. Decide one small intervention from the evidence below. You may read files; do not change anything.\n\n", d.Lead.Name, triggerLabels[d.Trigger])
	b.WriteString(briefBlock(d.Brief))
	t := d.Task
	fmt.Fprintf(&b, "Task #%d: %s [%s, review round %d of %d]\n", t.Seq, t.Title, t.Status, t.Iteration, t.MaxIterations)
	if desc := strings.TrimSpace(t.Description); desc != "" {
		b.WriteString(clip(desc, maxDescRework) + "\n")
	}
	if len(t.Acceptance) > 0 {
		b.WriteString("Acceptance criteria:\n")
		for _, a := range t.Acceptance {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	fmt.Fprintf(&b, "\nTrigger: %s\n", d.Trigger)
	if d.Evidence != "" {
		b.WriteString(quoted("Evidence", clip(d.Evidence, 1500)))
	}
	if len(d.ToolCalls) > 0 {
		b.WriteString(quoted("The worker's last tool calls", strings.Join(d.ToolCalls, "\n")))
	}
	if d.FailingCheck != "" {
		b.WriteString("Latest failing check: " + d.FailingCheck + "\n")
	}
	for i, r := range d.Reviews {
		b.WriteString(quoted(fmt.Sprintf("Review feedback %d (newest first)", i+1), r))
	}
	if d.LastText != "" {
		b.WriteString(quoted("The last turn's final text", d.LastText))
	}
	if d.DiffStat != "" {
		b.WriteString("Diff so far: " + d.DiffStat + "\n")
	}
	b.WriteString("\nTeam (name, role, adapter, open tasks):\n")
	for _, m := range d.Team {
		mark := ""
		switch m.ID {
		case t.Assignee:
			mark = " [works on this task]"
		case t.Reviewer:
			mark = " [reviews this task]"
		case d.Lead.ID:
			mark = " [you]"
		}
		fmt.Fprintf(&b, "- %s: %s, %s, %d%s\n", oneLine(m.Name), oneLine(m.Role), m.Adapter, m.Load, mark)
	}
	fmt.Fprintf(&b, "\nYou have intervened %d of at most %d times on this task in this run; after that the human decides.\n", d.Interventions, d.Max)
	b.WriteString(`
Answer with exactly one JSON object and nothing else:
{"action":"nudge|reassign|split|escalate|continue","message":"...","agent":"<team member name, only for reassign>","tasks":[{"title":"...","description":"...","acceptanceCriteria":["Given ..., when ..., then ..."]}],"reason":"..."}
- nudge: the message goes to the worker's next turn as guidance; say exactly what to do differently.
- reassign: give the task to another team member (never yourself, never the reviewer); the message goes to the new worker.
- split: create 1 to 5 follow-up tasks (only "tasks" for split); the current task continues with the message as its narrowed scope.
- escalate: a human decides; the message is your question or recommendation to them.
- continue: nothing to change.
Prefer the smallest intervention that gets the task moving. Escalate when you are unsure or a human decision is needed.`)
	return b.String()
}

// ---- the monitor: stalled and unassigned tasks ----

// MonitorResult is what one monitor pass did.
type MonitorResult struct {
	Stalled    []string `json:"stalled"`
	Unassigned []string `json:"unassigned"`
}

// lastProgress is the time (ms) of the task's latest progress event: anything on the task except the lead's, the scheduler's
// and the watchdog's own events. Zero when there is none.
func (l *Lead) lastProgress(ctx context.Context, taskID string) int64 {
	var ts sql.NullInt64
	_ = l.d.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT MAX(ts) FROM events WHERE task_id=? AND type NOT LIKE 'lead.%' AND type NOT LIKE 'scheduler.%' AND type NOT LIKE 'watchdog.%'`, taskID).Scan(&ts)
	return ts.Int64
}

func (l *Lead) runRunning(ctx context.Context, runID string) bool {
	var st string
	if err := l.d.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT status FROM workflow_runs WHERE id=?`, runID).Scan(&st); err != nil {
		return false
	}
	return workflow.RunStatus(st) == workflow.StatusRunning || workflow.RunStatus(st) == workflow.StatusRecovering
}

// Tick runs the monitor's triggers for one project: a RUNNING task without progress events for the project's stall time
// (trigger stalled), and, while automatic scheduling is on, a READY or BACKLOG task without a worker or reviewer (trigger
// unassigned). Each silence, and each unassigned task until it changes, is asked about once, also across restarts (the stored
// interventions say what was asked). Nothing happens without a lead on the team.
func (l *Lead) Tick(ctx context.Context, projectID string) (MonitorResult, error) {
	res := MonitorResult{Stalled: []string{}, Unassigned: []string{}}
	team, err := l.d.Team(ctx, projectID)
	if err != nil {
		return res, err
	}
	if _, ok := FindLead(team); !ok {
		return res, nil
	}
	dev := DefaultDevSettings()
	if l.d.Dev != nil {
		dev = l.d.Dev(ctx, projectID)
	}
	now := l.now().UnixMilli()
	running, err := l.d.Tasks.List(ctx, tasks.Filter{ProjectID: projectID, Statuses: []tasks.Status{tasks.InProgress}})
	if err != nil {
		return res, err
	}
	for _, t := range running {
		if t.RunID == "" || !l.runRunning(ctx, t.RunID) {
			continue
		}
		last := l.lastProgress(ctx, t.ID)
		if last == 0 {
			last = t.Updated
		}
		idle := time.Duration(now-last) * time.Millisecond
		if idle < dev.LeadStall() {
			continue
		}
		l.mu.Lock()
		seen := l.stalledAt[t.ID] == last
		l.stalledAt[t.ID] = last
		l.mu.Unlock()
		if seen || l.asked(ctx, t.ID, t.RunID, TriggerStalled, last) {
			continue // this silence was already handled (here, or before a restart)
		}
		res.Stalled = append(res.Stalled, t.ID)
		o := l.Supervise(ctx, LeadTrigger{Kind: TriggerStalled, TaskID: t.ID, RunID: t.RunID,
			Evidence: fmt.Sprintf("No progress events for %d minutes while the run is RUNNING (the project's stall time is %d minutes).", int(idle.Minutes()), int(dev.LeadStall().Minutes()))})
		l.applyOutOfBand(ctx, t.ID, o)
	}
	if dev.AutoSchedule {
		waiting, err := l.d.Tasks.List(ctx, tasks.Filter{ProjectID: projectID, Statuses: []tasks.Status{tasks.Ready, tasks.Backlog}})
		if err != nil {
			return res, err
		}
		for _, t := range waiting {
			if t.Assignee != "" && t.Reviewer != "" {
				continue
			}
			l.mu.Lock()
			seen := l.unassigned[t.ID]
			l.unassigned[t.ID] = true
			l.mu.Unlock()
			if seen || l.asked(ctx, t.ID, "", TriggerUnassigned, t.Updated) {
				continue // asked since the task last changed (here, or before a restart)
			}
			res.Unassigned = append(res.Unassigned, t.ID)
			missing := "a worker and a reviewer"
			if t.Assignee != "" {
				missing = "a reviewer"
			} else if t.Reviewer != "" {
				missing = "a worker"
			}
			_ = l.Supervise(ctx, LeadTrigger{Kind: TriggerUnassigned, TaskID: t.ID, RunID: t.RunID,
				Evidence: fmt.Sprintf("The task is %s with no %s, and automatic scheduling is on, so it cannot start.", t.Status, missing)})
		}
	}
	return res, nil
}

// applyOutOfBand delivers a decision on a trigger that came from outside a turn: a nudge into the running turn (or the next
// one), an escalation parks the running turn on a human question.
func (l *Lead) applyOutOfBand(ctx context.Context, taskID string, o LeadOutcome) {
	if !o.Intervened || l.d.Steer == nil {
		return
	}
	switch o.Decision.Action {
	case LeadNudge, LeadReassign, LeadSplit:
		if m := strings.TrimSpace(o.Decision.Message); m != "" {
			l.d.Steer(ctx, taskID, "Message from the team lead:\n"+m, false)
		}
	case LeadEscalate:
		l.d.Steer(ctx, taskID, o.Decision.Message, true)
	}
}

// Loop runs Tick for every project on a timer until ctx ends.
func (l *Lead) Loop(ctx context.Context, every time.Duration, projects func(ctx context.Context) []string) {
	if every <= 0 {
		every = time.Minute
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			for _, p := range projects(ctx) {
				_, _ = l.Tick(ctx, p)
			}
		}
	}
}

// ---- stand-up and the intervention log ----

// StandupResult is what lead.standup returns.
type StandupResult struct {
	MessageID string `json:"messageId"`
	Text      string `json:"text"`
	Tasks     int    `json:"tasks"`
}

// Standup runs one read-only lead turn over the project's active tasks and posts its short status summary (on track / at
// risk / blocked, one line each) in the project's general channel.
func (l *Lead) Standup(ctx context.Context, projectID string) (StandupResult, error) {
	team, err := l.d.Team(ctx, projectID)
	if err != nil {
		return StandupResult{}, err
	}
	lead, ok := FindLead(team)
	if !ok {
		return StandupResult{}, errors.New("this project has no team lead: add an agent with the role \"lead\" to the team first")
	}
	if l.d.Budget != nil {
		if err := l.d.Budget(ctx, projectID, "", lead.ID); err != nil {
			return StandupResult{}, fmt.Errorf("the stand-up did not start: %w", err)
		}
	}
	active, err := l.d.Tasks.List(ctx, tasks.Filter{ProjectID: projectID, Statuses: []tasks.Status{tasks.Ready, tasks.Claimed, tasks.InProgress, tasks.Blocked, tasks.WaitingHuman, tasks.Review, tasks.Verification}, Limit: maxStandupTasks})
	if err != nil {
		return StandupResult{}, err
	}
	names := map[string]string{}
	for _, m := range team {
		names[m.ID] = m.Name
	}
	var b strings.Builder
	b.WriteString("Drydock phase: standup\n")
	fmt.Fprintf(&b, "You are %s, the team lead. Give a short stand-up of the project's active tasks. You may read files; do not change anything.\n\n", lead.Name)
	if l.d.Brief != nil {
		b.WriteString(briefBlock(clip(strings.TrimSpace(l.d.Brief(ctx, projectID)), maxBriefPrompt)))
	}
	if len(active) == 0 {
		b.WriteString("There are no active tasks.\n")
	}
	now := l.now().UnixMilli()
	for _, t := range active {
		lastAct := l.lastProgress(ctx, t.ID)
		if lastAct == 0 {
			lastAct = t.Updated
		}
		verdict := ""
		if rv, err := l.d.Tasks.Reviews(ctx, t.ID); err == nil && len(rv) > 0 {
			verdict = "; last review " + rv[len(rv)-1].Verdict
		}
		worker := names[t.Assignee]
		if worker == "" {
			worker = "nobody"
		}
		fmt.Fprintf(&b, "- #%d %s [%s] worker %s, round %d of %d, last activity %d min ago%s\n", t.Seq, oneLine(clip(t.Title, maxTitleRunes)), t.Status, worker,
			t.Iteration, t.MaxIterations, (now-lastAct)/60000, verdict)
	}
	b.WriteString("\nAnswer in plain text, one line per task: \"#<n> <title>: on track|at risk|blocked: <one-line reason>\", then one line \"Summary: ...\". Nothing else.")
	text, err := l.turn(ctx, projectID, "", "", lead, b.String())
	if err != nil {
		return StandupResult{}, fmt.Errorf("the team lead's stand-up turn failed: %w", err)
	}
	text = clip(strings.TrimSpace(l.redact(cleanText(text))), maxStandupText)
	if text == "" {
		return StandupResult{}, errors.New("the team lead's stand-up was empty")
	}
	body := "Stand-up from the team lead:\n" + text
	msgID := ""
	if l.d.Post != nil {
		if msgID, err = l.d.Post(ctx, projectID, "", lead.ID, body); err != nil {
			return StandupResult{}, fmt.Errorf("posting the stand-up: %w", err)
		}
	}
	if l.d.Bus != nil {
		data, _ := json.Marshal(map[string]any{"projectId": projectID, "messageId": msgID, "tasks": len(active)})
		_, _ = l.d.Bus.Publish(ctx, events.Event{Type: "lead.standup", ProjectID: projectID, AgentID: lead.ID, Data: data})
	}
	return StandupResult{MessageID: msgID, Text: text, Tasks: len(active)}, nil
}

// Intervention is one stored intervention (lead.interventions).
type Intervention struct {
	ID           string   `json:"id"`
	ProjectID    string   `json:"projectId"`
	TaskID       string   `json:"taskId"`
	RunID        string   `json:"runId"`
	LeadAgentID  string   `json:"leadAgentId"`
	Trigger      string   `json:"trigger"`
	Action       string   `json:"action"`
	Message      string   `json:"message"`
	Reason       string   `json:"reason"`
	TargetAgent  string   `json:"targetAgent,omitempty"`
	CreatedTasks []string `json:"createdTasks"`
	Evidence     string   `json:"evidence,omitempty"`
	Invalid      bool     `json:"invalid"`
	MessageID    string   `json:"messageId,omitempty"`
	CreatedAt    int64    `json:"createdAt"`
}

// Interventions lists a project's interventions (or one task's), newest first, at most 200.
func Interventions(ctx context.Context, db *storage.DB, projectID, taskID string) ([]Intervention, error) {
	rows, err := db.Reader().QueryContext(ctx, `SELECT id,project_id,task_id,run_id,lead_agent_id,trigger_kind,action,message,reason,target_agent,created_tasks,evidence,invalid,message_id,created_at
		FROM lead_interventions WHERE project_id=? AND (?='' OR task_id=?) ORDER BY created_at DESC, rowid DESC LIMIT 200`, projectID, taskID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Intervention{}
	for rows.Next() {
		var iv Intervention
		var created string
		var invalid int
		if err := rows.Scan(&iv.ID, &iv.ProjectID, &iv.TaskID, &iv.RunID, &iv.LeadAgentID, &iv.Trigger, &iv.Action, &iv.Message, &iv.Reason, &iv.TargetAgent, &created, &iv.Evidence, &invalid, &iv.MessageID, &iv.CreatedAt); err != nil {
			return nil, err
		}
		iv.Invalid = invalid != 0
		iv.CreatedTasks = []string{}
		_ = json.Unmarshal([]byte(created), &iv.CreatedTasks)
		out = append(out, iv)
	}
	return out, rows.Err()
}
