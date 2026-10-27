package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The workspace inbox and approvals (decision P97): the detail of one request for the inbox's right pane (approval.get), marking
// an item done (needsyou.ack), and the person's "approve for this task" rules of agents' tool permission requests
// (security.TaskAllowRule; run.respond with scope "task" makes one, approval.rule.remove ends one, and each ends with its task).
// The crew's data for the workspace (agent.listAll, agent.timeline) is in ext_crew.go.

func init() {
	RegisterExtension(Extension{Name: "inbox", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"approval.get": bind(func(ctx context.Context, p idParam) (any, error) { return c.ApprovalDetail(ctx, p.ID) }),
			"approval.rule.remove": bind(func(ctx context.Context, p idParam) (any, error) {
				if err := c.RemoveTaskRule(ctx, p.ID, c.Human()); err != nil {
					return nil, err
				}
				return map[string]bool{"removed": true}, nil
			}),
			"needsyou.ack": bind(func(ctx context.Context, p struct {
				Key string `json:"key"`
			}) (any, error) {
				if err := c.AckNeedsYou(ctx, p.Key, c.Human()); err != nil {
					return nil, err
				}
				return map[string]bool{"acked": true}, nil
			}),
		}, nil, nil
	}})
}

// Events of the task allow rules. Both carry the rule's task and agent; removed also says why ("removed" by the person, or
// "task ended").
const (
	EventTaskRuleAdded   = "approval.rule.added"
	EventTaskRuleRemoved = "approval.rule.removed"
	// EventNeedsYouAcked: a person marked an inbox item done ({key, kind}); it has left the inbox.
	EventNeedsYouAcked = "needsyou.acked"
)

const taskRuleCols = `r.id,r.project_id,r.task_id,r.agent_id,r.tool,r.program,r.commands,r.input_digest,r.summary,r.approval_id,r.created_by,r.created_at`

func scanTaskRules(rows *sql.Rows) ([]security.TaskAllowRule, error) {
	defer rows.Close()
	out := []security.TaskAllowRule{}
	for rows.Next() {
		var r security.TaskAllowRule
		var cmds string
		if err := rows.Scan(&r.ID, &r.Project, &r.TaskID, &r.AgentID, &r.Tool, &r.Program, &cmds, &r.InputDigest, &r.Summary, &r.ApprovalID, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cmds), &r.Commands)
		out = append(out, r)
	}
	return out, rows.Err()
}

// openTask is the SQL condition that the rule's task (alias t) has not ended: the rules of an ended task are deleted with it,
// and never apply even while that runs.
const openTask = `t.status NOT IN ('COMPLETED','FAILED','CANCELLED')`

// taskRules is the permission gate's TaskRules: the person's rules of an open task for one agent and tool. A rule that cannot be
// read applies to nothing (the request is asked).
func (c *Core) taskRules(ctx context.Context, scope security.PermissionScope, tool string) []security.TaskAllowRule {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT `+taskRuleCols+` FROM permission_allow_rules r JOIN tasks t ON t.id = r.task_id
WHERE r.task_id=? AND r.agent_id=? AND r.tool=? AND r.project_id=? AND `+openTask+` ORDER BY r.created_at, r.id`, scope.TaskID, scope.AgentID, tool, scope.Project)
	if err != nil {
		return nil
	}
	out, _ := scanTaskRules(rows)
	return out
}

// TaskRules lists a task's allow rules, oldest first (task.get, approval.get).
func (c *Core) TaskRules(ctx context.Context, taskID string) ([]security.TaskAllowRule, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT `+taskRuleCols+` FROM permission_allow_rules r JOIN tasks t ON t.id = r.task_id
WHERE r.task_id=? AND `+openTask+` ORDER BY r.created_at, r.id`, taskID)
	if err != nil {
		return nil, err
	}
	return scanTaskRules(rows)
}

// addTaskRule stores a rule the person made by approving a request for its task.
func (c *Core) addTaskRule(ctx context.Context, r security.TaskAllowRule, by string) (security.TaskAllowRule, error) {
	r.ID, r.CreatedBy, r.CreatedAt = ids.New("par"), by, storage.Now()
	cmds, _ := json.Marshal(r.Commands)
	if len(r.Commands) == 0 {
		cmds = []byte("[]")
	}
	err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO permission_allow_rules(id,project_id,task_id,agent_id,tool,program,commands,input_digest,summary,approval_id,created_by,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Project, r.TaskID, r.AgentID, r.Tool, r.Program, string(cmds), r.InputDigest, r.Summary, r.ApprovalID, r.CreatedBy, r.CreatedAt)
		return err
	})
	return r, err
}

// deleteTaskRules deletes rules by a condition and returns the ones deleted.
func (c *Core) deleteTaskRules(ctx context.Context, where string, args ...any) ([]security.TaskAllowRule, error) {
	var gone []security.TaskAllowRule
	err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+taskRuleCols+` FROM permission_allow_rules r WHERE `+where, args...)
		if err != nil {
			return err
		}
		if gone, err = scanTaskRules(rows); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM permission_allow_rules WHERE id IN (SELECT r.id FROM permission_allow_rules r WHERE `+where+`)`, args...)
		return err
	})
	return gone, err
}

func (c *Core) publishTaskRule(ctx context.Context, typ string, r security.TaskAllowRule, data map[string]any) {
	data["rule"] = r.ID
	b, _ := json.Marshal(data)
	_, _ = c.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: r.Project, TaskID: r.TaskID, AgentID: r.AgentID, Data: b})
}

// RemoveTaskRule ends one task allow rule: the next such request is asked again. Only a person can.
func (c *Core) RemoveTaskRule(ctx context.Context, id, by string) error {
	if !isPerson(by) {
		return errors.New("only a person may remove an allow rule")
	}
	gone, err := c.deleteTaskRules(ctx, `r.id=?`, id)
	if err != nil {
		return err
	}
	if len(gone) == 0 {
		return fmt.Errorf("there is no allow rule %s (it ended with its task, or was removed)", id)
	}
	c.publishTaskRule(ctx, EventTaskRuleRemoved, gone[0], map[string]any{"reason": "removed", "by": by})
	return nil
}

// endTaskRules deletes a task's rules when it ends (tasks.Hooks.OnTerminal).
func (c *Core) endTaskRules(ctx context.Context, t tasks.Task) {
	gone, err := c.deleteTaskRules(ctx, `r.task_id=?`, t.ID)
	if err != nil {
		c.logf("task %s: ending its allow rules: %v", t.ID, err)
		return
	}
	for _, r := range gone {
		c.publishTaskRule(ctx, EventTaskRuleRemoved, r, map[string]any{"reason": "task ended", "status": string(t.Status)})
	}
}

// approveForTask answers a waiting permission request with an approval for the rest of its task: the rule is stored before the
// approval is decided, so the agent's next identical call (which may come the moment the waiting call is answered) finds it.
func (c *Core) approveForTask(ctx context.Context, gate *security.PermissionGate, requestID string) error {
	rule, err := gate.TaskRuleFor(requestID)
	if err != nil {
		return err
	}
	saved, err := c.addTaskRule(ctx, rule, c.Human())
	if err != nil {
		return err
	}
	gate.MarkApprovedForTask(requestID, saved.ID) // the answered call is recorded as approved for the task, with its rule
	if err := gate.Approvals.Decide(ctx, requestID, security.DecisionApprove, c.Human()); err != nil {
		gate.ClearApprovedForTask(requestID)
		_, _ = c.deleteTaskRules(context.WithoutCancel(ctx), `r.id=?`, saved.ID)
		return err
	}
	c.publishTaskRule(ctx, EventTaskRuleAdded, saved, map[string]any{"tool": toolOfAction(saved.Tool), "summary": saved.Summary, "approval": requestID, "by": c.Human()})
	return nil
}

// AckNeedsYou marks an inbox item done for the person (a failed check, a watchdog pause, a question): it leaves the inbox. A
// request (an approval, a review, a chat's tool request) leaves it when it is answered, so it cannot be marked done.
func (c *Core) AckNeedsYou(ctx context.Context, key, by string) error {
	if !isPerson(by) {
		return errors.New("only a person may mark an inbox item done")
	}
	kind, _, _ := strings.Cut(key, ":")
	switch kind {
	case "approval", "chat-ask":
		return errors.New("a request leaves the inbox when you answer it: approve or deny it instead")
	case "question", "check", "watchdog":
	default:
		return fmt.Errorf("there is no inbox item %q", key)
	}
	items, err := c.NeedsYou(ctx, "")
	if err != nil {
		return err
	}
	var item *room.NeedsYouItem
	for i := range items {
		if items[i].Key == key {
			item = &items[i]
		}
	}
	if item == nil {
		return fmt.Errorf("%s is not in the inbox (it was answered or marked done)", key)
	}
	if err := c.Room.AckNeedsYou(ctx, key, room.Human(strings.TrimPrefix(by, "human:"))); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]any{"key": key, "kind": string(item.Kind), "by": by})
	_, _ = c.Bus.Publish(ctx, events.Event{Type: EventNeedsYouAcked, ProjectID: item.ProjectID, TaskID: item.TaskID, Data: b})
	return nil
}

// ApprovalDetail is one request as the inbox's right pane shows it. The tool and input are the gate's display copy (secrets
// redacted, a long value cut, Shortened says so); the hash binds the exact action.
type ApprovalDetail struct {
	ID string `json:"id"`
	// Kind is the request's kind: approval, question or review. ActionKind is the security kind of a security-service
	// approval (shell_command, agent_tool, permission_mode_escalation...); Phase tells review gates apart (spec, merge, plan).
	Kind       string `json:"kind"`
	ActionKind string `json:"actionKind,omitempty"`
	Phase      string `json:"phase,omitempty"`
	// Gate is "permission_prompt" for an agent's tool permission request (answered with run.respond on RunID).
	Gate        string         `json:"gate,omitempty"`
	Title       string         `json:"title"`
	Description string         `json:"description"` // what is being decided, in plain words
	Reason      string         `json:"reason,omitempty"`
	Tool        string         `json:"tool,omitempty"`
	Input       string         `json:"input,omitempty"`
	Shell       bool           `json:"shell"`
	Targets     []string       `json:"targets"`
	Shortened   bool           `json:"shortened"`
	Editable    bool           `json:"editable"`
	Agent       *ApprovalAgent `json:"agent,omitempty"`
	ProjectID   string         `json:"projectId"`
	ProjectName string         `json:"projectName,omitempty"`
	Task        *ApprovalTask  `json:"task,omitempty"`
	// RunID is the workflow run to answer the request on (run.respond {runId, requestId: id}); empty when it is not answered
	// (a permission mode change is recorded already decided).
	RunID string `json:"runId,omitempty"`
	// AgentRunID is the agent process (arun_...) that asks, for a tool permission request.
	AgentRunID string `json:"agentRunId,omitempty"`
	Worktree   string `json:"worktree,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	ExpiresAt  int64  `json:"expiresAt,omitempty"`
	ResolvedAt int64  `json:"resolvedAt,omitempty"`
	ActionHash string `json:"actionHash,omitempty"`
	Status     string `json:"status"`
	DecidedBy  string `json:"decidedBy,omitempty"`
	// PermissionMode is the change a permission_mode_escalation approval allowed (From is "" for a new agent or template).
	PermissionMode *ModeChange `json:"permissionMode,omitempty"`
	// ApproveForTask says whether the request can be approved for the rest of its task (run.respond scope "task");
	// ApproveForTaskNote says why not when it cannot.
	ApproveForTask     bool   `json:"approveForTask"`
	ApproveForTaskNote string `json:"approveForTaskNote,omitempty"`
	// Rules are the task's allow rules in force.
	Rules []security.TaskAllowRule `json:"rules"`
	// Payload is a workflow request's own payload, as the task page reads it (a spec, a merge summary); absent for a
	// security-service approval, whose fields are above.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ApprovalAgent is the agent a request is about.
type ApprovalAgent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Adapter string `json:"adapter"`
	Model   string `json:"model"`
}

// ApprovalTask is the task a request belongs to.
type ApprovalTask struct {
	ID     string       `json:"id"`
	Seq    int          `json:"seq"`
	Title  string       `json:"title"`
	Status tasks.Status `json:"status"`
}

// ModeChange is a change of permission mode.
type ModeChange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ApprovalDetail returns one request of the approvals table with what the inbox shows about it.
func (c *Core) ApprovalDetail(ctx context.Context, id string) (ApprovalDetail, error) {
	var d ApprovalDetail
	var payload []byte
	var taskID string
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT id,run_id,kind,action_kind,phase,title,payload,action_hash,status,decided_by,created_at,expires_at,resolved_at,project_id,task_id
FROM approvals WHERE id=?`, id).Scan(&d.ID, &d.RunID, &d.Kind, &d.ActionKind, &d.Phase, &d.Title, &payload, &d.ActionHash, &d.Status, &d.DecidedBy,
		&d.CreatedAt, &d.ExpiresAt, &d.ResolvedAt, &d.ProjectID, &taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return ApprovalDetail{}, fmt.Errorf("there is no request %s", id)
	}
	if err != nil {
		return ApprovalDetail{}, err
	}
	d.Targets, d.Rules = []string{}, []security.TaskAllowRule{}
	var t *tasks.Task
	if taskID != "" {
		if tk, err := c.Tasks.Get(ctx, taskID); err == nil {
			t = &tk
			d.Task = &ApprovalTask{ID: tk.ID, Seq: tk.Seq, Title: tk.Title, Status: tk.Status}
			d.Worktree = tk.Worktree
			if d.Rules, err = c.TaskRules(ctx, tk.ID); err != nil {
				return ApprovalDetail{}, err
			}
		} else {
			d.Task = &ApprovalTask{ID: taskID}
		}
	}
	if p, err := c.GetProject(ctx, d.ProjectID); err == nil {
		d.ProjectName = p.Name
	}
	agentID := ""
	if d.RunID == "" { // a security-service approval: its payload is the gate's display copy
		gate, err := c.permissions().init()
		if err != nil {
			return ApprovalDetail{}, err
		}
		info, err := gate.Approvals.Get(ctx, id)
		if err != nil {
			return ApprovalDetail{}, err
		}
		a := info.Action
		agentID, d.Reason, d.Cwd, d.AgentRunID = a.AgentID, info.Reason, a.Cwd, a.RunID
		d.Tool, d.Input, d.Shell, d.Shortened = toolOfAction(a.Tool), a.Command, a.Shell != "", a.Shortened
		for _, tg := range a.Targets {
			if x := firstNonEmpty(tg.Path, tg.Remote); x != "" {
				d.Targets = append(d.Targets, x)
			}
		}
		switch {
		case strings.HasPrefix(d.Title, security.PermissionTitlePrefix):
			d.Gate, d.Editable = PermissionGate, permissionEditable(a)
			if t != nil {
				d.RunID = t.RunID // answered through the task's run (run.respond checks the request is of that task)
			}
			if d.Status == "REQUESTED" {
				if _, err := gate.TaskRuleFor(id); err != nil {
					d.ApproveForTaskNote = strings.TrimPrefix(err.Error(), security.ErrOnlyOnce.Error()+": ")
				} else {
					d.ApproveForTask = true
				}
			}
		case d.ActionKind == security.KindPermissionEscalation:
			mc := &ModeChange{To: a.Command}
			if len(a.Argv) == 2 {
				mc.From, mc.To = a.Argv[0], a.Argv[1]
			}
			d.PermissionMode = mc
		}
	} else {
		d.Payload = json.RawMessage(payload)
		if t != nil {
			agentID = t.Assignee
		}
	}
	if agentID != "" {
		ag := &ApprovalAgent{ID: agentID}
		if a, err := c.agent(ctx, agentID); err == nil {
			ag.Name, ag.Adapter, ag.Model = a.Name, a.Adapter, a.Model
		}
		d.Agent = ag
	}
	d.Description = approvalDescription(d, t)
	return d, nil
}

// permissionEditable says whether a person can change a permission request's input before approving it: only where the CLI
// can run a changed call (Claude's prompt tool) and the input shown is the whole input (P88).
func permissionEditable(a security.ActionDescriptor) bool {
	return strings.HasPrefix(a.Tool, "claude:") && !a.Shortened && !strings.Contains(a.Command, "[REDACTED")
}

// approvalDescription says in plain words what a request asks the person to decide.
func approvalDescription(d ApprovalDetail, t *tasks.Task) string {
	who := "An agent"
	if d.Agent != nil && d.Agent.Name != "" {
		who = d.Agent.Name
	}
	task := "the task"
	if t != nil {
		task = fmt.Sprintf("task %d (%s)", t.Seq, t.Title)
	}
	firstLine := func(s string) string {
		line, _, _ := strings.Cut(s, "\n")
		return line
	}
	switch {
	case d.Gate == PermissionGate && d.Shell:
		return who + " wants to run " + firstLine(d.Input) + " for " + task + ". Its own rules do not allow this command, so it waits for your answer; approving allows only this exact command."
	case d.Gate == PermissionGate:
		where := ""
		if len(d.Targets) > 0 {
			where = " on " + d.Targets[0]
		}
		return who + " wants to use " + d.Tool + where + " for " + task + ". Its own rules do not allow it, so it waits for your answer; approving allows only this exact call."
	case d.PermissionMode != nil:
		subject := "agents made from a template"
		if d.Agent != nil {
			subject = who
		}
		if d.PermissionMode.From == "" {
			return "The permission mode " + d.PermissionMode.To + " was chosen for " + subject + ": it acts without asking you first."
		}
		return "The permission mode of " + subject + " changed from " + d.PermissionMode.From + " to " + d.PermissionMode.To + ": it acts without asking you first."
	case d.Kind == "review" && d.Phase == "spec":
		return "The spec of " + task + " is written. Accept it to start building, or send it back with what to change."
	case d.Kind == "review" && d.Phase == "merge":
		into := ""
		if t != nil && t.BaseBranch != "" {
			into = " into " + t.BaseBranch
		}
		return "The change of " + task + " is ready. Approve to merge it" + into + ", or ask for changes."
	case d.Kind == "review" && d.Phase == "plan":
		return "A plan is ready. Accept it to make its tasks, or send it back with what to change."
	case d.Kind == "question":
		return who + " asks you: " + d.Title
	}
	return firstNonEmpty(d.Reason, d.Title)
}
