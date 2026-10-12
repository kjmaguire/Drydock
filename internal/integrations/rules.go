package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
)

// MaxRuleExprBytes bounds a rule expression.
const MaxRuleExprBytes = 1000

// Rule actions.
const (
	ActionCreateTask = "create_task"
	ActionNotify     = "notify"
)

// ruleEnv is the CEL environment of a rule: one variable, `item`, a map of normalized, sanitized facts. Free text can be matched
// (title.contains(...)) but a rule's parameters are fixed by the human who wrote it; item text can never supply them.
//
//	item.source      "linear" | "notion" | "sentry" | "aikido"
//	item.kind        "issue" | "page" | "error_event" | "finding"
//	item.state, item.title, item.assignee, item.priority, item.ref (the IssueRef)
//	item.labels      list of strings
//	item.level       Sentry level (enum, "" otherwise);  item.events, item.users  Sentry counts (0 otherwise)
//	item.severity    Aikido severity (enum, "" otherwise)
//	item.project, item.team, item.repo, item.type   strings
//	item.risk        injection risk score
var ruleEnv = func() *cel.Env {
	e, err := cel.NewEnv(cel.Variable("item", cel.MapType(cel.StringType, cel.DynType)))
	if err != nil {
		panic(err)
	}
	return e
}()

func ruleVars(it Item) map[string]any {
	str := func(k string) string { s, _ := it.Meta[k].(string); return s }
	count := func(k string) int64 {
		switch x := it.Meta[k].(type) {
		case int64:
			return x
		case int:
			return int64(x)
		case float64:
			return int64(x)
		}
		return 0
	}
	labels := make([]any, 0, len(it.Labels))
	for _, l := range it.Labels {
		labels = append(labels, l)
	}
	return map[string]any{"item": map[string]any{
		"source": it.Source, "kind": it.Kind, "state": it.State, "title": it.Title, "assignee": it.Assignee, "priority": it.Priority, "ref": it.IssueRef,
		"labels": labels, "level": str("level"), "events": count("events"), "users": count("users"), "severity": str("severity"),
		"project": str("project"), "team": str("team"), "repo": str("repo"), "type": str("type"), "risk": int64(it.Risk),
	}}
}

func compileRule(expr string) (cel.Program, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, errf(ErrBadRequest, "the rule needs a condition")
	}
	if len(expr) > MaxRuleExprBytes {
		return nil, errf(ErrBadRequest, "the condition is longer than %d characters", MaxRuleExprBytes)
	}
	ast, iss := ruleEnv.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, errf(ErrBadRequest, "the condition does not compile: %v", iss.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, errf(ErrBadRequest, "the condition must be true or false (for example item.level == \"error\" && item.events > 100)")
	}
	prg, err := ruleEnv.Program(ast, cel.CostLimit(20000))
	if err != nil {
		return nil, errf(ErrBadRequest, "the condition is too expensive: %v", err)
	}
	return prg, nil
}

// RuleReq creates, updates or deletes a stored rule. Only a human can send it.
type RuleReq struct {
	ID            string         `json:"id"`      // empty = create
	Version       int64          `json:"version"` // required to update or delete (compare-and-swap)
	IntegrationID string         `json:"integrationId"`
	ProjectID     string         `json:"projectId"`
	Name          string         `json:"name"`
	When          string         `json:"when"`
	Action        string         `json:"action"`
	Params        map[string]any `json:"params"`
	MaxPerDay     *int           `json:"maxPerDay"`
	DryRun        *bool          `json:"dryRun"`
	Enabled       *bool          `json:"enabled"`
	Delete        bool           `json:"delete"`
}

func validateParams(p map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range p {
		switch k {
		case "priority":
			n, ok := num(v)
			if !ok || n < 0 || n > 4 {
				return nil, errf(ErrBadRequest, "params.priority must be 0 to 4")
			}
			out[k] = n
		case "template":
			t, ok := v.(string)
			if !ok || len(t) > 60 || strings.ContainsAny(t, "\n\r") {
				return nil, errf(ErrBadRequest, "params.template must be a short name")
			}
			out[k] = t
		default:
			return nil, errf(ErrBadRequest, "params.%s is not a rule parameter (allowed: priority, template)", k)
		}
	}
	return out, nil
}

// SetRule creates, updates or deletes a rule. New rules always start disabled and in dry-run mode (docs/integrations.md section
// 3, rule 3): enabling and leaving dry-run are later, separate human edits. Updates are compare-and-swap on Version.
func (s *Service) SetRule(ctx context.Context, by string, r RuleReq) (Rule, error) {
	if err := requireHuman(by); err != nil {
		return Rule{}, err
	}
	if r.ID != "" {
		old, err := s.st.GetRule(ctx, r.ID)
		if err != nil {
			return Rule{}, err
		}
		if r.Delete {
			if err := s.st.DeleteRule(ctx, r.ID, r.Version); err != nil {
				return Rule{}, err
			}
			s.emit(ctx, EventRuleChanged, old.ProjectID, "", map[string]any{"ruleId": old.ID, "by": by, "deleted": true})
			return old, nil
		}
		if r.Version != old.Version {
			return old, ErrConflict
		}
		upd := old
		if r.Name != "" {
			upd.Name = strings.TrimSpace(r.Name)
		}
		if r.ProjectID != "" {
			upd.ProjectID = r.ProjectID
		}
		if r.When != "" {
			upd.When = r.When
		}
		if r.Action != "" {
			upd.Action = r.Action
		}
		if r.Params != nil {
			upd.Params = r.Params
		}
		if r.MaxPerDay != nil {
			upd.MaxPerDay = *r.MaxPerDay
		}
		if r.DryRun != nil {
			upd.DryRun = *r.DryRun
		}
		if r.Enabled != nil {
			upd.Enabled = *r.Enabled
		}
		if err := s.checkRule(ctx, &upd); err != nil {
			return old, err
		}
		upd.UpdatedBy = by
		out, err := s.st.UpdateRule(ctx, upd, r.Version)
		if err != nil {
			return old, err
		}
		s.emit(ctx, EventRuleChanged, out.ProjectID, "", map[string]any{"ruleId": out.ID, "by": by, "version": out.Version, "enabled": out.Enabled, "dryRun": out.DryRun})
		return out, nil
	}
	if r.Delete {
		return Rule{}, errf(ErrBadRequest, "there is no rule to delete")
	}
	nr := Rule{IntegrationID: r.IntegrationID, ProjectID: r.ProjectID, Name: strings.TrimSpace(r.Name), When: r.When, Action: r.Action, Params: r.Params, MaxPerDay: 5,
		DryRun: true, Enabled: false, CreatedBy: by, UpdatedBy: by}
	if nr.Action == "" {
		nr.Action = ActionCreateTask
	}
	if r.MaxPerDay != nil {
		nr.MaxPerDay = *r.MaxPerDay
	}
	if err := s.checkRule(ctx, &nr); err != nil {
		return Rule{}, err
	}
	out, err := s.st.InsertRule(ctx, nr)
	if err != nil {
		return Rule{}, err
	}
	s.emit(ctx, EventRuleChanged, out.ProjectID, "", map[string]any{"ruleId": out.ID, "by": by, "version": out.Version, "created": true})
	return out, nil
}

func (s *Service) checkRule(ctx context.Context, r *Rule) error {
	if r.Name == "" || len(r.Name) > 80 {
		return errf(ErrBadRequest, "a rule needs a name of at most 80 characters")
	}
	conn, err := s.st.GetConnection(ctx, r.IntegrationID)
	if err != nil {
		return err
	}
	if r.ProjectID == "" {
		r.ProjectID = conn.ProjectID
	}
	if r.ProjectID == "" {
		if asgs, err := s.st.ListAssignments(ctx, conn.ID); err == nil && len(asgs) == 1 {
			r.ProjectID = asgs[0].ProjectID
		}
	}
	if r.ProjectID == "" {
		return errf(ErrBadRequest, "a rule needs the Drydock project its tasks go to")
	}
	if _, err := s.st.AssignmentFor(ctx, conn.ID, r.ProjectID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: assign %s to the project before adding a rule for it", ErrNotAssigned, vendor(conn.Kind))
		}
		return err
	}
	if s.o.ProjectExists != nil {
		if err := s.o.ProjectExists(ctx, r.ProjectID); err != nil {
			return err
		}
	}
	switch r.Action {
	case ActionCreateTask:
		if conn.Kind == KindNotion {
			return errf(ErrRuleNotAllowed, "Notion pages become requirement candidates through Import, not tasks; use the notify action for a Notion rule")
		}
	case ActionNotify:
	default:
		return errf(ErrBadRequest, "action must be create_task or notify")
	}
	if _, err := compileRule(r.When); err != nil {
		return err
	}
	if r.MaxPerDay < 1 || r.MaxPerDay > 100 {
		return errf(ErrBadRequest, "maxPerDay must be between 1 and 100")
	}
	if r.Params == nil {
		r.Params = map[string]any{}
	}
	p, err := validateParams(r.Params)
	if err != nil {
		return err
	}
	r.Params = p
	return nil
}

// ListRules returns the rules of a connection ("" = all) with their recent outcomes.
type RuleView struct {
	Rule
	Matches []RuleMatch `json:"matches"`
	Today   int         `json:"createdLast24h"`
}

// Rules lists rules with recent matches (the dry-run listing is the "would_create" matches).
func (s *Service) Rules(ctx context.Context, integrationID string) ([]RuleView, error) {
	rs, err := s.st.ListRules(ctx, integrationID)
	if err != nil {
		return nil, err
	}
	out := make([]RuleView, 0, len(rs))
	for _, r := range rs {
		m, err := s.st.Matches(ctx, r.ID, 50)
		if err != nil {
			return nil, err
		}
		n, err := s.st.CountRuleTasks(ctx, r.ID, s.nowMS()-24*time.Hour.Milliseconds())
		if err != nil {
			return nil, err
		}
		out = append(out, RuleView{Rule: r, Matches: m, Today: n})
	}
	return out, nil
}

// RuleOutcome is what one rule did in one evaluation.
type RuleOutcome struct {
	RuleID  string `json:"ruleId"`
	ItemID  string `json:"itemId"`
	Outcome string `json:"outcome"`
	TaskID  string `json:"taskId,omitempty"`
}

// EvaluateRules runs the rules of an assignment's project over the assignment's not-yet-imported items. It is idempotent: an item that already
// has a task is never a candidate (dedupe on (integration, external id)), a rule stops creating at max_per_day tasks in any
// rolling 24 hours (excess items stay eligible and are tried again on the next evaluation), items at or above the risk threshold
// are never created, and a dry-run rule only records what it would do.
func (s *Service) EvaluateRules(ctx context.Context, conn Connection, asg Assignment) []RuleOutcome {
	all, err := s.st.ListRules(ctx, conn.ID)
	if err != nil {
		s.o.Logf("integrations: rules: %v", err)
		return nil
	}
	var rules []Rule
	for _, r := range all {
		if r.ProjectID == asg.ProjectID {
			rules = append(rules, r)
		}
	}
	if len(rules) == 0 {
		return nil
	}
	imported := false
	items, err := s.st.ListItems(ctx, ItemFilter{IntegrationID: conn.ID, AssignmentID: asg.ID, Imported: &imported, Limit: 500})
	if err != nil {
		s.o.Logf("integrations: rules: %v", err)
		return nil
	}
	var outs []RuleOutcome
	for _, r := range rules {
		if !r.Enabled && !r.DryRun {
			continue // a disabled rule that is out of dry-run mode does nothing at all
		}
		prg, err := compileRule(r.When)
		if err != nil {
			s.o.Logf("integrations: rule %s no longer compiles: %v", r.ID, err)
			continue
		}
		for _, it := range items {
			// re-read: a human or an earlier rule may have imported it since the listing
			cur, err := s.st.GetItem(ctx, it.ID)
			if err != nil || cur.TaskID != "" || cur.Gone {
				continue
			}
			val, _, err := prg.ContextEval(ctx, ruleVars(cur))
			if err != nil {
				continue // an expression that cannot be evaluated on this item does not match
			}
			if b, ok := val.Value().(bool); !ok || !b {
				continue
			}
			out := s.applyRule(ctx, conn, r, cur)
			if out.Outcome != "" {
				outs = append(outs, out)
			}
		}
	}
	return outs
}

func (s *Service) previousOutcome(ctx context.Context, ruleID, itemID string) string {
	ms, err := s.st.Matches(ctx, ruleID, 1000)
	if err != nil {
		return ""
	}
	for _, m := range ms {
		if m.ItemID == itemID {
			return m.Outcome
		}
	}
	return ""
}

func (s *Service) applyRule(ctx context.Context, conn Connection, r Rule, it Item) RuleOutcome {
	out := RuleOutcome{RuleID: r.ID, ItemID: it.ID}
	prev := s.previousOutcome(ctx, r.ID, it.ID)
	record := func(outcome, detail string) {
		out.Outcome = outcome
		_ = s.st.RecordMatch(ctx, RuleMatch{RuleID: r.ID, ItemID: it.ID, Outcome: outcome, Detail: detail})
	}
	if it.Risk >= s.o.ThreatThreshold {
		record("risky", fmt.Sprintf("risk %d: %s", it.Risk, strings.Join(it.RiskFlags, ", ")))
		if prev != "risky" {
			s.emit(ctx, EventRuleSkippedRisk, r.ProjectID, "", map[string]any{"ruleId": r.ID, "itemId": it.ID, "risk": it.Risk, "flags": it.RiskFlags})
		}
		return out
	}
	if r.Action == ActionNotify && r.Enabled {
		record("notified", "")
		if prev != "notified" {
			s.emit(ctx, EventRuleWouldCreate, r.ProjectID, "", map[string]any{"ruleId": r.ID, "itemId": it.ID, "action": ActionNotify})
		}
		return out
	}
	if r.DryRun || !r.Enabled || r.Action == ActionNotify {
		// a new rule starts disabled and in dry-run mode: it only lists what it would do (docs/integrations.md section 3)
		record("would_create", "dry run: no task was created")
		if prev != "would_create" {
			s.emit(ctx, EventRuleWouldCreate, r.ProjectID, "", map[string]any{"ruleId": r.ID, "itemId": it.ID, "action": r.Action})
		}
		return out
	}
	used, err := s.st.CountRuleTasks(ctx, r.ID, s.nowMS()-24*time.Hour.Milliseconds())
	if err != nil {
		record("error", "could not count the rule's tasks")
		return out
	}
	if used >= r.MaxPerDay {
		record("rate_limited", fmt.Sprintf("the rule already created %d of its %d tasks allowed per day", used, r.MaxPerDay))
		if prev != "rate_limited" {
			s.emit(ctx, EventRuleRateLimited, r.ProjectID, "", map[string]any{"ruleId": r.ID, "itemId": it.ID, "maxPerDay": r.MaxPerDay})
		}
		return out
	}
	tr, err := s.createTask(ctx, "rule:"+r.ID, it, r.ProjectID, &r)
	switch {
	case errors.Is(err, ErrAlreadyImported):
		record("deduped", "the item already has a task")
		out.TaskID = tr.TaskID
	case err != nil:
		record("error", s.scrub(err.Error()))
	default:
		record("created", "")
		out.TaskID = tr.TaskID
	}
	return out
}
