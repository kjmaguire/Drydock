package pipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// PlanTeam (brief section 24, Slice 4): given a goal and optionally a requirements text, a planner agent proposes tasks with
// dependencies, roles and acceptance criteria. The reply is JSON and is validated strictly. Nothing becomes a task without
// a human action: the proposal is put to the human as a `review` request, and only the accepted tasks are created, in
// BACKLOG, with their dependencies. Readiness (BACKLOG to READY) and starting are the scheduler's job.

// WorkflowPlan is the name of the planner workflow.
const WorkflowPlan = "plan-team"

// Limits on a proposal.
const (
	maxPlanTasks    = 30
	maxTitleRunes   = 120
	maxDescBytes    = 4000
	maxAcceptances  = 12
	maxPlanFiles    = 20
	maxAcceptBytes  = 600
	maxNotesBytes   = 2000
	maxGoalBytes    = 4000
	maxRequirements = 64 << 10
)

// ProposedTask is one task of a proposal.
type ProposedTask struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Role        string   `json:"role"`
	DependsOn   []string `json:"dependsOn"`
	Acceptance  []string `json:"acceptance"`
	Template    string   `json:"template,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	// Files are the files or folders the task will change, relative to the repository (optional). The scheduler does not start
	// a task whose files overlap a running task's (docs/parallel-work.md).
	Files []string `json:"files,omitempty"`
}

// Proposal is the planner's validated reply.
type Proposal struct {
	Tasks []ProposedTask `json:"tasks"`
	Notes string         `json:"notes,omitempty"`
}

// PlanRoles are the roles a planned task can ask for (brief section 1).
var PlanRoles = []string{"pm", "architect", "ux designer", "frontend", "backend", "database", "devops", "qa", "security", "reviewer"}

var keyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// NormalizePlanRole maps a role as written to its canonical form, or "" if it is not a known role.
func NormalizePlanRole(s string) string {
	n := normRole(s)
	for _, r := range PlanRoles {
		if n == r {
			return r
		}
	}
	switch n {
	case "product manager", "product":
		return "pm"
	case "ux", "designer", "ui designer", "ux/ui designer":
		return "ux designer"
	case "front end", "frontend engineer":
		return "frontend"
	case "back end", "backend engineer":
		return "backend"
	case "tester", "test", "quality", "quality assurance":
		return "qa"
	case "ops", "devops engineer", "sre":
		return "devops"
	case "db", "data":
		return "database"
	}
	return ""
}

func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return -1
		}
		return r
	}, s)
}

// extractJSON returns the JSON text of a planner reply: the whole reply, or the single fenced block that is the whole reply.
// Prose around the JSON is a problem (the reply is machine input; a model that chats gets one chance to fix it).
func extractJSON(text string) (string, error) {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "```") {
		nl := strings.IndexByte(t, '\n')
		if nl < 0 || !strings.HasSuffix(t, "```") {
			return "", errors.New("the reply starts a code fence that does not close; reply with the JSON object only")
		}
		t = strings.TrimSpace(strings.TrimSuffix(t[nl+1:], "```"))
		if strings.Contains(t, "```") {
			return "", errors.New("the reply holds more than one code block; reply with one JSON object only")
		}
	}
	if !strings.HasPrefix(t, "{") {
		return "", errors.New("the reply is not a JSON object; reply with the JSON object only, with no text before or after it")
	}
	return t, nil
}

// ParseProposal validates a planner reply strictly. Unknown fields, extra values after the object, duplicate keys,
// unknown roles or templates, dependencies on missing tasks, cycles, and acceptance criteria that are not complete
// Given/When/Then sentences are all problems. The returned problems are fed back to the planner verbatim.
func ParseProposal(text string) (Proposal, []string) {
	var p Proposal
	js, err := extractJSON(text)
	if err != nil {
		return p, []string{err.Error()}
	}
	dec := json.NewDecoder(strings.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Proposal{}, []string{"the reply is not valid for the schema: " + err.Error()}
	}
	if _, err := dec.Token(); err != io.EOF {
		return Proposal{}, []string{"there is more than one JSON value in the reply; reply with one object"}
	}
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	if len(p.Tasks) == 0 {
		add("the plan has no tasks")
	}
	if len(p.Tasks) > maxPlanTasks {
		add("the plan has %d tasks; the limit is %d (group the work)", len(p.Tasks), maxPlanTasks)
		return Proposal{}, problems
	}
	if len(p.Notes) > maxNotesBytes || !utf8.ValidString(p.Notes) {
		add("notes are longer than %d bytes or not valid UTF-8", maxNotesBytes)
	}
	p.Notes = cleanText(p.Notes)
	known := map[string]bool{}
	for _, t := range p.Tasks {
		known[t.Key] = true
	}
	seen := map[string]bool{}
	for i := range p.Tasks {
		t := &p.Tasks[i]
		where := fmt.Sprintf("task %d (%q)", i+1, t.Key)
		switch {
		case !keyRE.MatchString(t.Key):
			add("%s: key must match %s", where, keyRE)
		case seen[t.Key]:
			add("%s: duplicate key", where)
		}
		seen[t.Key] = true
		t.Title, t.Description = strings.TrimSpace(cleanText(t.Title)), strings.TrimSpace(cleanText(t.Description))
		if t.Title == "" || utf8.RuneCountInString(t.Title) > maxTitleRunes || strings.ContainsAny(t.Title, "\n\t") {
			add("%s: the title must be one line of 1 to %d characters", where, maxTitleRunes)
		}
		if len(t.Description) > maxDescBytes {
			add("%s: the description is longer than %d bytes", where, maxDescBytes)
		}
		if r := NormalizePlanRole(t.Role); r == "" {
			add("%s: role %q is not one of %s", where, t.Role, strings.Join(PlanRoles, ", "))
		} else {
			t.Role = r
		}
		if t.Template != "" {
			if tpl, ok := TemplateByName(t.Template); !ok {
				add("%s: template %q is unknown", where, t.Template)
			} else {
				t.Template = tpl.Name
			}
		}
		if t.Priority < 0 || t.Priority > 9 {
			add("%s: priority must be 0 to 9", where)
		}
		deps := map[string]bool{}
		for _, d := range t.DependsOn {
			switch {
			case d == t.Key:
				add("%s: depends on itself", where)
			case !known[d]:
				add("%s: depends on %q, which is not a task of this plan", where, d)
			case deps[d]:
				add("%s: lists %q twice", where, d)
			}
			deps[d] = true
		}
		if len(t.Files) > maxPlanFiles {
			add("%s: names %d files; the limit is %d", where, len(t.Files), maxPlanFiles)
		}
		for j, f := range t.Files {
			f = strings.TrimSpace(f)
			t.Files[j] = f
			if f == "" || len(f) > 200 || strings.HasPrefix(f, "/") || strings.HasPrefix(f, "\\") || strings.Contains(f, "..") || strings.ContainsAny(f, "\x00\r\n") || (len(f) > 1 && f[1] == ':') {
				add("%s: file %d %q is not a path inside the repository", where, j+1, truncate(f, 80))
			}
		}
		if len(t.Acceptance) == 0 {
			add("%s: needs at least one acceptance criterion", where)
		}
		if len(t.Acceptance) > maxAcceptances {
			add("%s: has %d acceptance criteria; the limit is %d", where, len(t.Acceptance), maxAcceptances)
		}
		for j, a := range t.Acceptance {
			a = strings.TrimSpace(cleanText(a))
			t.Acceptance[j] = a
			if len(a) > maxAcceptBytes {
				add("%s: criterion %d is longer than %d bytes", where, j+1, maxAcceptBytes)
				continue
			}
			cs, probs := ParseCriteriaLines([]string{a})
			if len(probs) > 0 || len(cs) != 1 {
				add("%s: criterion %d %q is not one complete Given/When/Then sentence", where, j+1, truncate(a, 80))
			}
		}
	}
	if len(problems) == 0 {
		if cyc := findCycle(p); cyc != "" {
			add("the dependencies form a cycle: %s", cyc)
		}
	}
	if len(problems) > 0 {
		return Proposal{}, problems
	}
	return p, nil
}

// findCycle returns a description of a dependency cycle, or "".
func findCycle(p Proposal) string {
	deps := map[string][]string{}
	for _, t := range p.Tasks {
		deps[t.Key] = t.DependsOn
	}
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var cycle string
	var visit func(k string) bool
	visit = func(k string) bool {
		color[k] = grey
		stack = append(stack, k)
		for _, d := range deps[k] {
			switch color[d] {
			case grey:
				i := 0
				for i < len(stack) && stack[i] != d {
					i++
				}
				cycle = strings.Join(append(append([]string(nil), stack[i:]...), d), " -> ")
				return true
			case white:
				if visit(d) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[k] = black
		return false
	}
	for _, t := range p.Tasks {
		if color[t.Key] == white && visit(t.Key) {
			return cycle
		}
	}
	return ""
}

// TopoOrder returns the task keys with every task after its dependencies (ties in proposal order).
func TopoOrder(p Proposal) []string {
	done := map[string]bool{}
	var out []string
	pending := append([]ProposedTask(nil), p.Tasks...)
	for len(pending) > 0 {
		var rest []ProposedTask
		progressed := false
		for _, t := range pending {
			ready := true
			for _, d := range t.DependsOn {
				if !done[d] {
					ready = false
				}
			}
			if ready {
				done[t.Key] = true
				out = append(out, t.Key)
				progressed = true
			} else {
				rest = append(rest, t)
			}
		}
		if !progressed {
			break // a cycle: ParseProposal already refused it
		}
		pending = rest
	}
	return out
}

// PlanPrompt asks the planner for the proposal.
func PlanPrompt(goal, requirements string, team []TeamMember, feedback string) string {
	var b strings.Builder
	b.WriteString("Drydock phase: plan\n")
	b.WriteString("You are the planner of an engineering team. Break the goal into a small set of tasks, each of which one agent can build and verify on its own branch. You may read the repository to ground the plan. Do not write or change any code.\n\n")
	fmt.Fprintf(&b, "Goal (from the human):\n%s\n", strings.TrimSpace(goal))
	if strings.TrimSpace(requirements) != "" {
		b.WriteString("\n" + quoted("Requirements", clip(requirements, 24000)))
	}
	if len(team) > 0 {
		b.WriteString("\nTeam roles available: ")
		seen := map[string]bool{}
		var rs []string
		for _, m := range team {
			if r := NormalizePlanRole(m.Role); r != "" && !seen[r] {
				seen[r] = true
				rs = append(rs, r)
			}
		}
		sort.Strings(rs)
		b.WriteString(strings.Join(rs, ", ") + "\n")
	}
	if feedback != "" {
		b.WriteString("\nYour previous reply was rejected by the validator. Fix every point and answer again:\n" + quoted("Validator output", feedback))
	}
	templates := make([]string, 0, len(Templates()))
	for _, t := range Templates() {
		templates = append(templates, t.Name)
	}
	fmt.Fprintf(&b, `
Answer with exactly one JSON object and nothing else (no prose, no code fence needed):
{"tasks":[{"key":"short-id","title":"one line","description":"what and why","role":"backend","dependsOn":["other-key"],"acceptance":["Given <state>, when <action>, then <observable result>"],"template":"feature","priority":0,"files":["internal/auth/"]}],"notes":"optional remarks for the human"}

Rules:
- key: lower case letters, digits, "-" or "_", unique, at most 32 characters. dependsOn lists keys of tasks in this plan that must be COMPLETED first; no cycles.
- role is one of: %s.
- Every task has 1 to %d acceptance criteria, each one complete sentence containing Given, when and then.
- template is optional: one of %s. Use "feature" for new behaviour, "bug" for defects, "security" for security work.
- priority 0 to 9 (higher first), optional. At most %d tasks. Prefer fewer, larger-than-trivial tasks over many tiny ones.
- Tasks that can run at the same time must change different files. If two tasks need the same file, make the later one depend on the earlier one (dependsOn). List the files or folders a task will touch in "files" (relative to the repository, at most %d); the scheduler holds back a task whose files overlap one that is running.
Nothing is created until a human reviews your plan.`, strings.Join(PlanRoles, ", "), maxAcceptances, strings.Join(templates, ", "), maxPlanTasks, maxPlanFiles)
	return b.String()
}

// PlanDeps are the services the planner workflow needs.
type PlanDeps struct {
	Deps
	// PlannerConfig builds the runtime config for the planner agent of a project.
	PlannerConfig func(ctx context.Context, projectID, agentID string) (rt.Config, error)
	// Team lists the project's agents (for the prompt and for assigning accepted tasks).
	Team func(ctx context.Context, projectID string) ([]TeamMember, error)
	// Assign sets the worker and reviewer of a created task from its role. Optional; failures are reported, not fatal.
	Assign func(ctx context.Context, t tasks.Task, role string) error
}

// PlanInput is the run input of the plan-team workflow.
type PlanInput struct {
	PlanID       string `json:"planId"`
	ProjectID    string `json:"projectId"`
	Goal         string `json:"goal"`
	Requirements string `json:"requirements,omitempty"`
	PlannerAgent string `json:"plannerAgent,omitempty"`
}

// PlanReviewPayload is what the human sees when asked to review a plan.
type PlanReviewPayload struct {
	Phase  string         `json:"phase"` // "plan"
	PlanID string         `json:"planId"`
	Goal   string         `json:"goal"`
	Tasks  []PlanTaskView `json:"tasks"`
	Notes  string         `json:"notes,omitempty"`
	Order  []string       `json:"order"`
	Note   string         `json:"note"`
}

// PlanTaskView is a proposed task as shown to the human.
type PlanTaskView struct {
	ProposedTask
	Assignable bool `json:"assignable"` // the team has an agent for the role
}

// PlanResult is what the create node reports.
type PlanResult struct {
	Created    map[string]string `json:"created"` // key -> task id
	Unassigned []string          `json:"unassigned,omitempty"`
	By         string            `json:"by"`
}

func (pd PlanDeps) planInput(in workflow.NodeInput) (PlanInput, error) {
	var pi PlanInput
	if err := json.Unmarshal(in.Input, &pi); err != nil || pi.PlanID == "" || pi.ProjectID == "" {
		return pi, fmt.Errorf("plan: run input must hold planId and projectId: %v", err)
	}
	return pi, nil
}

func (pd PlanDeps) setPlan(ctx context.Context, id, status string, proposal *Proposal, accepted map[string]string, errText, by string) {
	_ = pd.Tasks.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		pj, aj := "", ""
		if proposal != nil {
			b, _ := json.Marshal(proposal)
			pj = string(b)
		}
		if accepted != nil {
			b, _ := json.Marshal(accepted)
			aj = string(b)
		}
		decided := int64(0)
		if status == "ACCEPTED" || status == "REJECTED" {
			decided = storage.Now()
		}
		_, err := tx.ExecContext(ctx, `UPDATE plans SET status=?, proposal=CASE WHEN ?<>'' THEN ? ELSE proposal END, accepted_tasks=CASE WHEN ?<>'' THEN ? ELSE accepted_tasks END,
			error=?, decided_at=CASE WHEN ?>0 THEN ? ELSE decided_at END, decided_by=CASE WHEN ?<>'' THEN ? ELSE decided_by END WHERE id=?`,
			status, pj, pj, aj, aj, errText, decided, decided, by, by, id)
		return err
	})
}

// planNode runs the planner agent (read-only, project root).
func (pd PlanDeps) planNode() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		pi, err := pd.planInput(in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		feedback, _ := feedbackFromInbox(in)
		var team []TeamMember
		if pd.Team != nil {
			team, _ = pd.Team(ctx, pi.ProjectID)
		}
		cfg, err := pd.PlannerConfig(ctx, pi.ProjectID, pi.PlannerAgent)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		repo, err := pd.Repo(ctx, pi.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		cfg.ProjectID = pi.ProjectID
		cfg.Spec.Cwd = repo.Root
		cfg.Spec.PermissionMode = "plan"
		prompt := PlanPrompt(pi.Goal, pi.Requirements, team, feedback)
		out, err := pd.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return prompt }})(ctx, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var res rt.TurnResult
		_ = json.Unmarshal(out.Value, &res)
		b, _ := json.Marshal(map[string]any{"text": clip(res.Text, 60000)})
		return workflow.NodeOutput{Value: b}, nil
	}
}

// planCheck validates the reply; an invalid one goes back to the planner (bounded), a valid one is stored on the plan.
func (pd PlanDeps) planCheck() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		pi, err := pd.planInput(in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var reply struct {
			Text string `json:"text"`
		}
		for _, m := range in.Inbox {
			_ = json.Unmarshal(m.Value, &reply)
		}
		p, problems := ParseProposal(reply.Text)
		if len(problems) > 0 {
			b, _ := json.Marshal(map[string]any{"feedback": "- " + strings.Join(problems, "\n- ")})
			return workflow.NodeOutput{Value: b, Route: "invalid"}, nil
		}
		pd.setPlan(ctx, pi.PlanID, "PROPOSED", &p, nil, "", "")
		b, _ := json.Marshal(p)
		return workflow.NodeOutput{Value: b, Route: "valid"}, nil
	}
}

// planFailed records that the planner never produced a valid plan.
func (pd PlanDeps) planFailed() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		pi, err := pd.planInput(in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		fb, _ := feedbackFromInbox(in)
		pd.setPlan(ctx, pi.PlanID, "FAILED", nil, nil, "the planner did not produce a valid plan: "+truncate(fb, 1500), "")
		return workflow.NodeOutput{}, fmt.Errorf("the planner did not produce a valid plan: %s", truncate(fb, 600))
	}
}

// planReview puts the proposal to the human as a `review` request listing the proposed tasks.
func (pd PlanDeps) planReview() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		pi, err := pd.planInput(in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var p Proposal
		for _, m := range in.Inbox {
			_ = json.Unmarshal(m.Value, &p)
		}
		var team []TeamMember
		if pd.Team != nil {
			team, _ = pd.Team(ctx, pi.ProjectID)
		}
		views := make([]PlanTaskView, len(p.Tasks))
		for i, t := range p.Tasks {
			views[i] = PlanTaskView{ProposedTask: t}
			for _, m := range team {
				if NormalizePlanRole(m.Role) == t.Role {
					views[i].Assignable = true
				}
			}
		}
		payload, _ := json.Marshal(PlanReviewPayload{Phase: "plan", PlanID: pi.PlanID, Goal: pi.Goal, Tasks: views, Notes: p.Notes, Order: TopoOrder(p),
			Note: "Approve to create the tasks in BACKLOG (all of them, or only data.accept = a list of keys that is closed under dependsOn). data.edits may change title, description, role, template, priority or acceptance of a task. Reject to discard the plan."})
		return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "review", Title: fmt.Sprintf("Review the plan: %s (%d tasks)", truncate(pi.Goal, 60), len(p.Tasks)), Payload: payload}}, nil
	}
}

// PlanAcceptData is the human's answer data for a plan review.
type PlanAcceptData struct {
	Accept []string                `json:"accept,omitempty"`
	Edits  map[string]ProposedTask `json:"edits,omitempty"`
}

// planCreate creates the accepted tasks. Only a human decision gets here; the accepted set must be closed under
// dependencies (a task cannot be created without the tasks it waits for).
func (pd PlanDeps) planCreate() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		pi, err := pd.planInput(in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		h, ok := humanBodyOf(in)
		if !ok || !isHuman(h.By) {
			return workflow.NodeOutput{}, fmt.Errorf("a plan can only be accepted by a human; got %q", h.By)
		}
		var p Proposal
		if raw, ok := in.Shared["out/plancheck"]; ok {
			_ = json.Unmarshal(raw, &p)
		}
		if len(p.Tasks) == 0 {
			return workflow.NodeOutput{}, errors.New("no validated plan to accept")
		}
		var ad PlanAcceptData
		if len(h.Data) > 0 && string(h.Data) != "null" {
			if err := json.Unmarshal(h.Data, &ad); err != nil {
				return workflow.NodeOutput{}, fmt.Errorf("the accept data is not valid: %w", err)
			}
		}
		// edits are applied to the proposal and the whole thing is validated again, exactly like a planner reply
		if len(ad.Edits) > 0 {
			for i := range p.Tasks {
				if e, ok := ad.Edits[p.Tasks[i].Key]; ok {
					t := p.Tasks[i]
					if e.Title != "" {
						t.Title = e.Title
					}
					if e.Description != "" {
						t.Description = e.Description
					}
					if e.Role != "" {
						t.Role = e.Role
					}
					if e.Template != "" {
						t.Template = e.Template
					}
					if e.Priority != 0 {
						t.Priority = e.Priority
					}
					if len(e.Acceptance) > 0 {
						t.Acceptance = e.Acceptance
					}
					if len(e.Files) > 0 {
						t.Files = e.Files
					}
					p.Tasks[i] = t
				}
			}
			b, _ := json.Marshal(p)
			vp, problems := ParseProposal(string(b))
			if len(problems) > 0 {
				return workflow.NodeOutput{}, fmt.Errorf("the edits are not valid: %s", strings.Join(problems, "; "))
			}
			p = vp
		}
		accepted := map[string]bool{}
		if len(ad.Accept) == 0 {
			for _, t := range p.Tasks {
				accepted[t.Key] = true
			}
		} else {
			for _, k := range ad.Accept {
				accepted[k] = true
			}
		}
		byKey := map[string]ProposedTask{}
		for _, t := range p.Tasks {
			byKey[t.Key] = t
		}
		for k := range accepted {
			if _, ok := byKey[k]; !ok {
				return workflow.NodeOutput{}, fmt.Errorf("accepted key %q is not in the plan", k)
			}
		}
		for _, t := range p.Tasks {
			if !accepted[t.Key] {
				continue
			}
			for _, d := range t.DependsOn {
				if !accepted[d] {
					return workflow.NodeOutput{}, fmt.Errorf("task %q depends on %q, which was not accepted", t.Key, d)
				}
			}
		}
		res := PlanResult{Created: map[string]string{}, By: h.By}
		// idempotent across a crash: tasks already created for this plan are reused
		var prior map[string]string
		var priorJSON string
		_ = pd.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT accepted_tasks FROM plans WHERE id=?`, pi.PlanID).Scan(&priorJSON)
		_ = json.Unmarshal([]byte(priorJSON), &prior)
		for _, k := range TopoOrder(p) {
			if !accepted[k] {
				continue
			}
			if id := prior[k]; id != "" {
				if _, err := pd.Tasks.Get(ctx, id); err == nil {
					res.Created[k] = id
					continue
				}
			}
			t := byKey[k]
			var deps []string
			for _, d := range t.DependsOn {
				deps = append(deps, res.Created[d])
			}
			desc := t.Description
			if desc == "" {
				desc = t.Title
			}
			nt, err := pd.Tasks.Create(ctx, tasks.CreateReq{ProjectID: pi.ProjectID, Title: t.Title, Description: desc + "\n\n(planned from the goal: " + truncate(pi.Goal, 200) + ")",
				Priority: t.Priority, Template: t.Template, Acceptance: t.Acceptance, Files: t.Files, DependsOn: deps})
			if err != nil {
				pd.setPlan(ctx, pi.PlanID, "PROPOSED", nil, res.Created, "creating "+k+": "+err.Error(), "")
				return workflow.NodeOutput{}, fmt.Errorf("create %s: %w", k, err)
			}
			res.Created[k] = nt.ID
			pd.setPlan(ctx, pi.PlanID, "PROPOSED", nil, res.Created, "", "") // remember progress so a retry does not duplicate
			if pd.Assign != nil {
				if err := pd.Assign(ctx, nt, t.Role); err != nil {
					res.Unassigned = append(res.Unassigned, k)
				}
			}
		}
		pd.setPlan(ctx, pi.PlanID, "ACCEPTED", nil, res.Created, "", h.By)
		pd.emitPlan(ctx, pi, "plan.accepted", map[string]any{"planId": pi.PlanID, "created": res.Created, "by": h.By})
		b, _ := json.Marshal(res)
		return workflow.NodeOutput{Value: b}, nil
	}
}

func (pd PlanDeps) planReject() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		pi, err := pd.planInput(in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		h, ok := humanBodyOf(in)
		if !ok || !isHuman(h.By) {
			return workflow.NodeOutput{}, fmt.Errorf("a plan can only be rejected by a human; got %q", h.By)
		}
		pd.setPlan(ctx, pi.PlanID, "REJECTED", nil, nil, "", h.By)
		pd.emitPlan(ctx, pi, "plan.rejected", map[string]any{"planId": pi.PlanID, "by": h.By})
		return workflow.NodeOutput{Value: json.RawMessage(`{"rejected":true}`)}, nil
	}
}

func (pd PlanDeps) emitPlan(ctx context.Context, pi PlanInput, typ string, data map[string]any) {
	if pd.Bus == nil {
		return
	}
	pd.emit(ctx, typ, tasks.Task{ProjectID: pi.ProjectID}, data)
}

// PlanGraph is the plan-team workflow: planner turn, strict validation (bounded retries), human review, creation.
func (pd PlanDeps) PlanGraph() *workflow.Graph {
	g := workflow.NewGraph(WorkflowPlan).Add(
		workflow.AgentTask("plan", pd.planNode()).With("agent", "planner"),
		workflow.Route("plancheck", pd.planCheck()),
		workflow.Route("planfailed", pd.planFailed()),
		&workflow.Node{ID: "planreview", Kind: workflow.KindRequest, Handler: pd.planReview()},
		workflow.Route("plancreate", pd.planCreate()),
		workflow.Route("planreject", pd.planReject()),
	)
	g.Edge("plan", "plancheck").
		EdgeWhen("plancheck", "planreview", "valid").
		Loop("plancheck", "plan", "invalid", 2, "planfailed").
		EdgeWhen("planreview", "plancreate", "approved").
		EdgeWhen("planreview", "planreject", "rejected").
		Loop("planreview", "planreview", "answered", 3, "planreject")
	return g.Start("plan")
}

// NewPlan inserts the plan row and returns its id; the caller starts the workflow with PlanInput.
func NewPlan(ctx context.Context, db *storage.DB, projectID, goal, requirements, plannerAgent string) (PlanInput, error) {
	goal = strings.TrimSpace(cleanText(goal))
	switch {
	case goal == "":
		return PlanInput{}, errors.New("plan: a goal is required")
	case len(goal) > maxGoalBytes:
		return PlanInput{}, fmt.Errorf("plan: the goal is longer than %d bytes (put detail in the requirements)", maxGoalBytes)
	case len(requirements) > maxRequirements:
		return PlanInput{}, fmt.Errorf("plan: the requirements are longer than %d bytes", maxRequirements)
	case !utf8.ValidString(requirements):
		return PlanInput{}, errors.New("plan: the requirements are not valid UTF-8 text")
	}
	pi := PlanInput{PlanID: ids.New("plan"), ProjectID: projectID, Goal: goal, Requirements: requirements, PlannerAgent: plannerAgent}
	sha := ""
	if requirements != "" {
		sum := sha256.Sum256([]byte(requirements))
		sha = hex.EncodeToString(sum[:])
	}
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO plans(id,project_id,goal,requirements_sha,planner_agent,status,created_at) VALUES(?,?,?,?,?,'PLANNING',?)`,
			pi.PlanID, projectID, goal, sha, plannerAgent, storage.Now())
		return err
	})
	return pi, err
}

// Plan is a stored plan.
type Plan struct {
	ID        string            `json:"id"`
	ProjectID string            `json:"projectId"`
	RunID     string            `json:"runId"`
	Goal      string            `json:"goal"`
	Status    string            `json:"status"`
	Planner   string            `json:"plannerAgent"`
	Proposal  *Proposal         `json:"proposal,omitempty"`
	Created   map[string]string `json:"createdTasks,omitempty"`
	Error     string            `json:"error,omitempty"`
	CreatedAt int64             `json:"createdAt"`
	DecidedAt int64             `json:"decidedAt,omitempty"`
	DecidedBy string            `json:"decidedBy,omitempty"`
}

const planCols = `id,project_id,run_id,goal,status,planner_agent,proposal,accepted_tasks,error,created_at,decided_at,decided_by`

func scanPlan(sc interface{ Scan(...any) error }) (Plan, error) {
	var p Plan
	var prop, acc string
	if err := sc.Scan(&p.ID, &p.ProjectID, &p.RunID, &p.Goal, &p.Status, &p.Planner, &prop, &acc, &p.Error, &p.CreatedAt, &p.DecidedAt, &p.DecidedBy); err != nil {
		return p, err
	}
	if prop != "" && prop != "{}" {
		var pr Proposal
		if json.Unmarshal([]byte(prop), &pr) == nil {
			p.Proposal = &pr
		}
	}
	_ = json.Unmarshal([]byte(acc), &p.Created)
	return p, nil
}

// GetPlan loads a plan.
func GetPlan(ctx context.Context, db *storage.DB, id string) (Plan, error) {
	p, err := scanPlan(db.Reader().QueryRowContext(ctx, `SELECT `+planCols+` FROM plans WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("plan %s not found", id)
	}
	return p, err
}

// ListPlans lists a project's plans, newest first.
func ListPlans(ctx context.Context, db *storage.DB, projectID string) ([]Plan, error) {
	rows, err := db.Reader().QueryContext(ctx, `SELECT `+planCols+` FROM plans WHERE project_id=? ORDER BY created_at DESC, rowid DESC LIMIT 200`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPlanRun records the workflow run that owns a plan.
func SetPlanRun(ctx context.Context, db *storage.DB, planID, runID string) error {
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE plans SET run_id=? WHERE id=?`, runID, planID)
		return err
	})
}
