package integrations

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// TaskResult is the outcome of an import.
type TaskResult struct {
	ItemID   string `json:"itemId"`
	TaskID   string `json:"taskId"`
	IssueRef string `json:"issueRef"`
	Existing bool   `json:"existing"` // the item already had a task; nothing was created
	// Requirements is set for a Notion page: it became a requirements-source candidate, not a task.
	Requirements bool     `json:"requirements,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// Import turns one item into work. Only a human can call it (it is the "human action" of docs/integrations.md section 3):
//
//   - Linear issue -> BACKLOG task with the title and description quoted, labels and priority mapped, IssueRef "ENG-123".
//   - Sentry issue -> BACKLOG bug task with a secret-scanned stack trace excerpt.
//   - Aikido finding -> BACKLOG security task with the severity mapped to the task priority.
//   - Notion page -> a requirements-source candidate: the event integration.notion_page_ready is emitted and the text is
//     available through NotionPage; the Requirements area decides what to do with it.
func (s *Service) Import(ctx context.Context, by, itemID, projectOverride string) (TaskResult, error) {
	if err := requireHuman(by); err != nil {
		return TaskResult{}, err
	}
	it, err := s.st.GetItem(ctx, itemID)
	if err != nil {
		return TaskResult{}, err
	}
	conn, err := s.st.GetConnection(ctx, it.IntegrationID)
	if err != nil {
		return TaskResult{}, err
	}
	if it.Kind == ItemPage {
		return s.notionCandidate(ctx, by, conn, it)
	}
	project := projectOverride
	if project == "" {
		project = conn.ProjectID
	}
	if project == "" {
		return TaskResult{}, errf(ErrBadRequest, "this connection has no Drydock project; choose one first")
	}
	tr, err := s.createTask(ctx, by, it, project, nil)
	if errors.Is(err, ErrAlreadyImported) {
		return tr, nil
	}
	return tr, err
}

func (s *Service) notionCandidate(ctx context.Context, by string, conn Connection, it Item) (TaskResult, error) {
	s.emit(ctx, EventNotionPageReady, conn.ProjectID, "", map[string]any{
		"integrationId": conn.ID, "itemId": it.ID, "externalId": it.ExternalID, "hash": it.ContentHash, "risk": it.Risk, "by": by,
		"projectId": conn.ProjectID, "untrusted": true,
	})
	return TaskResult{ItemID: it.ID, IssueRef: it.IssueRef, Requirements: true, Warnings: riskWarnings(it.RiskFlags)}, nil
}

// NotionPageText is a Notion page handed to the Requirements area: sanitized, secret-redacted, size-limited, and still data.
type NotionPageText struct {
	ItemID      string   `json:"itemId"`
	ProjectID   string   `json:"projectId"`
	ExternalID  string   `json:"externalId"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Text        string   `json:"text"`
	ContentHash string   `json:"contentHash"`
	Untrusted   bool     `json:"untrusted"` // always true: the Requirements area wraps it like any retrieved document
	Risk        int      `json:"risk"`
	RiskFlags   []string `json:"riskFlags"`
	Truncated   bool     `json:"truncated"`
}

// NotionPage returns the stored text of a Notion item for the Requirements area (the method behind
// integration.notion_page_ready).
func (s *Service) NotionPage(ctx context.Context, itemID string) (NotionPageText, error) {
	it, err := s.st.GetItem(ctx, itemID)
	if err != nil {
		return NotionPageText{}, err
	}
	if it.Kind != ItemPage {
		return NotionPageText{}, errf(ErrBadRequest, "item %s is not a Notion page", itemID)
	}
	text := s.st.ReadBody(it)
	if text == "" {
		text = it.Excerpt
	}
	return NotionPageText{ItemID: it.ID, ProjectID: it.ProjectID, ExternalID: it.ExternalID, Title: it.Title, URL: it.URL, Text: text, ContentHash: it.ContentHash,
		Untrusted: true, Risk: it.Risk, RiskFlags: it.RiskFlags, Truncated: it.Truncated}, nil
}

// createTask creates the BACKLOG task for an item. by is "human:<name>" or "rule:<id>". It is the single place external text
// becomes task text, and it only ever puts that text inside a quoted block.
func (s *Service) createTask(ctx context.Context, by string, it Item, projectID string, rule *Rule) (TaskResult, error) {
	if s.o.CreateTask == nil {
		return TaskResult{}, errors.New("integrations: no task creator is configured")
	}
	lk := s.lock("item:" + it.ID)
	lk.Lock()
	defer lk.Unlock()
	cur, err := s.st.GetItem(ctx, it.ID)
	if err != nil {
		return TaskResult{}, err
	}
	if cur.TaskID != "" {
		return TaskResult{ItemID: cur.ID, TaskID: cur.TaskID, IssueRef: cur.IssueRef, Existing: true}, ErrAlreadyImported
	}
	if cur.Kind == ItemPage {
		return TaskResult{}, errf(ErrNotImportable, "a Notion page is a requirements source, not a task")
	}
	it = cur
	if projectID != "" && s.o.ProjectExists != nil {
		if err := s.o.ProjectExists(ctx, projectID); err != nil {
			return TaskResult{}, err
		}
	}
	conn, err := s.st.GetConnection(ctx, it.IntegrationID)
	if err != nil {
		return TaskResult{}, err
	}
	ad, ok := AdapterFor(conn.Kind)
	if !ok {
		return TaskResult{}, errf(ErrBadRequest, "unknown integration %q", conn.Kind)
	}
	mapping := ad.TaskMapping(it)
	var warnings []string

	// Sentry: the stack trace is fetched only for an issue that is becoming a task, scanned, and quoted separately.
	stack := ""
	if conn.Kind == KindSentry {
		stack, warnings = s.fetchStack(ctx, conn, ad, it, warnings)
	}

	body := s.st.ReadBody(it)
	if body == "" {
		body = it.Excerpt
	}
	titleFlags := titleFlagsOf(it.Title)
	taskTitle := it.IssueRef + ": " + it.Title
	if len(titleFlags) > 0 {
		taskTitle = it.IssueRef + ": (title withheld: instruction-like text; see the quoted source below)"
		warnings = append(warnings, "the title contained instruction-like text and was replaced")
	}
	taskTitle = oneLine(taskTitle, MaxTaskTitleRunes)

	var head strings.Builder
	fmt.Fprintf(&head, "Imported from %s %s by %s.\n", vendor(conn.Kind), it.IssueRef, by)
	if facts := safeFacts(it); facts != "" {
		head.WriteString(facts + "\n")
	}
	srcText := "Title: " + it.Title
	if it.State != "" {
		srcText += "\nStatus: " + it.State
	}
	if len(it.Labels) > 0 {
		srcText += "\nLabels: " + strings.Join(it.Labels, ", ")
	}
	if it.Assignee != "" {
		srcText += "\nAssignee: " + it.Assignee
	}
	if strings.TrimSpace(body) != "" {
		srcText += "\n\n" + body
	}
	q, _ := truncateRunes(srcText, MaxQuoteRunes)
	allWarnings := append(riskWarnings(it.RiskFlags), warnings...)
	desc := head.String() + "\n" + QuotedBlock(vendor(conn.Kind)+" "+it.IssueRef, it.URL, allWarnings, q)
	if stack != "" {
		desc += "\n" + QuotedBlock("stack trace excerpt (secret-scanned)", "", nil, stack)
	}

	prio := mapping.Priority
	template := ""
	if rule != nil {
		if p, ok := num(rule.Params["priority"]); ok {
			prio = int(p)
		}
		template, _ = rule.Params["template"].(string)
	}
	taskID, err := s.o.CreateTask(ctx, TaskInput{ProjectID: projectID, Title: taskTitle, Description: desc, Priority: prio, IssueRef: it.IssueRef, Template: template})
	if err != nil {
		return TaskResult{}, err
	}
	won, err := s.st.ClaimItem(ctx, it.ID, taskID, by)
	if err != nil {
		return TaskResult{}, err
	}
	if !won {
		return TaskResult{ItemID: it.ID, TaskID: taskID}, errf(ErrAlreadyImported, "another import won the race; task %s is a duplicate and should be cancelled", taskID)
	}
	data := map[string]any{"integrationId": conn.ID, "itemId": it.ID, "taskId": taskID, "ref": it.IssueRef, "by": by, "label": mapping.Label, "priority": prio}
	if rule != nil {
		data["ruleId"] = rule.ID
	} else {
		data["human"] = by
	}
	s.emit(ctx, EventTaskCreated, projectID, taskID, data)
	return TaskResult{ItemID: it.ID, TaskID: taskID, IssueRef: it.IssueRef, Warnings: allWarnings}, nil
}

func (s *Service) fetchStack(ctx context.Context, conn Connection, ad Adapter, it Item, warnings []string) (string, []string) {
	cl, fail := s.client(ctx, conn, ad, false)
	if fail != nil {
		return "", append(warnings, "the stack trace could not be fetched: "+fail.Message)
	}
	text, has, err := ad.Detail(ctx, cl, it.ExternalID)
	if err != nil {
		var f *Failure
		msg := err.Error()
		if errors.As(err, &f) {
			msg = f.Message
		}
		return "", append(warnings, "the stack trace could not be fetched: "+s.scrub(msg))
	}
	if !has {
		return "", warnings
	}
	c := s.san.Text(ctx, text, MaxStackRunes)
	return excerptLines(c.Text, 60), warnings
}

// excerptLines keeps the first n lines.
func excerptLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("… (%d more lines)", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

func titleFlagsOf(title string) []string {
	_, flags := Score(title, 0)
	var out []string
	for _, f := range flags {
		if f != "hidden-characters" && f != "long-base64" && f != "hidden-comment" {
			out = append(out, f)
		}
	}
	return out
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	s, _ = truncateRunes(s, n)
	return s
}

// safeFacts renders only enum-constrained or numeric facts in the (trusted) header of a task description.
func safeFacts(it Item) string {
	var parts []string
	if lvl, ok := it.Meta["level"].(string); ok && levelEnum[lvl] {
		parts = append(parts, "level "+lvl)
	}
	if n, ok := num(it.Meta["events"]); ok && n > 0 {
		parts = append(parts, strconv.FormatInt(n, 10)+" events")
	}
	if n, ok := num(it.Meta["users"]); ok && n > 0 {
		parts = append(parts, strconv.FormatInt(n, 10)+" users")
	}
	if sev, ok := it.Meta["severity"].(string); ok && severityEnum[sev] {
		parts = append(parts, "severity "+sev)
	}
	if priorityEnum[it.Priority] && it.Source == KindLinear && it.Priority != "none" {
		parts = append(parts, "priority "+it.Priority)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Facts: " + strings.Join(parts, ", ") + "."
}
