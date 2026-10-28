package security

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Task allow rules (decision P97): a person who approves an agent's tool permission request may approve it for the rest of the
// task instead of once. The rule is bound to the project, the task, the agent and the tool, and covers the same call only: for a
// shell tool the same programs with the same arguments (the analyzer's invocations, matched like a stored rule with no
// patterns), for any other tool the same input (its digest). A later request it covers is approved without asking and recorded
// as such (outcome PermissionApprovedForTask, with the rule). It never covers what the rules mark as dangerous: a hard deny, an
// unreadable command, a path outside the worktree, any approval kind but an ordinary command or tool (deletes, pushes, deploys,
// secrets, migrations), a push or a merge, or a write inside .git. The rules live as long as their task; the core stores them
// and deletes them when the task ends.

// PermissionApprovedForTask is the outcome of a request a person's task allow rule approved without asking.
const PermissionApprovedForTask = "approved_for_task"

// ErrOnlyOnce is returned when a request cannot be approved for the rest of its task; the error says why.
var ErrOnlyOnce = errors.New("this call can only be approved once")

// TaskAllowRule is a person's "approve for this task" of one call.
type TaskAllowRule struct {
	ID      string `json:"id"`
	Project string `json:"projectId"`
	TaskID  string `json:"taskId"`
	AgentID string `json:"agentId"`
	Tool    string `json:"tool"` // the approval's tool, "<cli>:<tool>"
	// Program is the shell command's first program ("go"); empty for any other tool.
	Program string `json:"program,omitempty"`
	// Commands are the shell command's invocations, programs with their exact arguments; empty for any other tool.
	Commands []StoredRule `json:"commands,omitempty"`
	// InputDigest is InputDigest(tool, input) of the approved input of a tool that is not a shell.
	InputDigest string `json:"inputDigest,omitempty"`
	// Summary is what the rule allows as a person reads it: the command, or where the call goes.
	Summary    string `json:"summary"`
	ApprovalID string `json:"approvalId"` // the request it was approved on
	CreatedBy  string `json:"createdBy"`
	CreatedAt  int64  `json:"createdAt"`
}

// waitingAsk is a request whose call waits for a person: what an approval for the rest of the task is derived from.
type waitingAsk struct {
	scope PermissionScope
	td    ToolDecision
}

// pushOrMerge are the subcommands of the Git and forge programs that publish or merge work: never approved for a task.
var pushOrMerge = map[string]bool{"push": true, "merge": true}

// taskScopeRefusal says why a request cannot be approved for the rest of its task; "" when it can.
func (td ToolDecision) taskScopeRefusal() string {
	if td.Verdict != VerdictAsk {
		return "it is not a request a person answers"
	}
	tool := strings.ToLower(toolOfActionName(td.Action.Tool))
	if strings.Contains(tool, "push") || strings.Contains(tool, "merge") {
		return "it pushes or merges, which is asked every time"
	}
	if rep := td.report; rep != nil {
		cd := td.command
		switch {
		case len(rep.HardDeny) > 0 || rep.Confidence == Opaque:
			return "Drydock cannot read the whole command"
		case !rep.AllInside():
			return "it names a path outside the worktree"
		case td.Kind != KindShellCommand:
			return "it is a " + strings.ReplaceAll(td.Kind, "_", " ") + ", which is asked every time"
		case cd == nil || (cd.Verdict == VerdictAsk && !cd.Remember):
			return "the rules never remember this kind of command"
		}
		if cd.Verdict == VerdictAsk {
			for _, k := range cd.Kinds {
				if k != KindShellCommand {
					return "it is a " + strings.ReplaceAll(k, "_", " ") + ", which is asked every time"
				}
			}
		}
		for _, k := range rep.Kinds {
			if k != KindShellCommand {
				return "it is a " + strings.ReplaceAll(k, "_", " ") + ", which is asked every time"
			}
		}
		for _, c := range rep.Classes {
			if classKind(c) != "" {
				return "the analyzer marks it as " + string(c)
			}
		}
		if len(rep.Invocations) == 0 {
			return "it runs no program Drydock can name"
		}
		for _, inv := range rep.Invocations {
			if inv.StdinArgs {
				return "its arguments are only known when it runs"
			}
			for _, a := range inv.Args {
				if a.Dynamic || a.Glob {
					return "its arguments are only known when it runs"
				}
			}
			for _, rd := range inv.Redirects {
				if rd.Dynamic {
					return "its arguments are only known when it runs"
				}
			}
			switch inv.Program {
			case "git", "gh", "glab", "hub":
				for _, a := range inv.Args {
					if pushOrMerge[strings.ToLower(a.Text)] {
						return "it pushes or merges, which is asked every time"
					}
				}
			}
		}
		return ""
	}
	switch {
	case td.Kind != KindAgentTool:
		return "it is a " + strings.ReplaceAll(td.Kind, "_", " ") + ", which is asked every time"
	case td.gitDir:
		return "it writes inside .git, which is asked every time"
	case td.Action.ArgvDigest == "":
		return "Drydock cannot name its input"
	}
	return ""
}

// toolOfActionName is the tool of an approval's "<cli>:<tool>".
func toolOfActionName(t string) string {
	if _, tool, ok := strings.Cut(t, ":"); ok {
		return tool
	}
	return t
}

// taskRuleOf is the rule that approving td for the rest of its task stores (without its id and author).
func taskRuleOf(scope PermissionScope, td ToolDecision) TaskAllowRule {
	r := TaskAllowRule{Project: scope.Project, TaskID: scope.TaskID, AgentID: scope.AgentID, Tool: td.Action.Tool}
	if td.report != nil {
		for _, inv := range td.report.Invocations {
			s := StoredRule{Program: inv.Program, Args: []string{}}
			for _, a := range inv.Args {
				s.Args = append(s.Args, a.Text)
			}
			for _, rd := range inv.Redirects {
				s.Redirects = append(s.Redirects, redirectWords(rd))
			}
			r.Commands = append(r.Commands, s)
		}
		if len(r.Commands) > 0 {
			r.Program = r.Commands[0].Program
		}
		r.Summary = cutRunes(td.Display, 200)
		return r
	}
	r.InputDigest = td.Action.ArgvDigest
	r.Summary = permissionSummary(td)
	return r
}

// matches reports whether the rule covers a request of scope: the same project, task, agent and tool, a request that could be
// approved for the task at all, and the same call.
func (r TaskAllowRule) matches(scope PermissionScope, td ToolDecision) bool {
	if r.Project != scope.Project || r.TaskID != scope.TaskID || r.AgentID != scope.AgentID || r.Tool != td.Action.Tool || scope.TaskID == "" {
		return false
	}
	if td.taskScopeRefusal() != "" {
		return false
	}
	if td.report != nil {
		if len(r.Commands) == 0 {
			return false
		}
		return sameCommands(r.Commands, *td.report)
	}
	return r.InputDigest != "" && r.InputDigest == td.Action.ArgvDigest
}

// sameCommands reports whether every invocation of the report is one of the rule's commands exactly: the same program, the
// same arguments and the same redirections, word for word (a rule for "echo x > notes.txt" does not cover "echo x > .gitignore"). A task rule stores the words the person approved, so "*" or "**" among them is a word (an
// argument that was quoted), never a wildcard as it is in a hand-written rule (audit 2026-10-27: "node '**'" approved for the
// task let "node -e ..." through).
func sameCommands(cmds []StoredRule, rep CommandReport) bool {
	if len(rep.Invocations) == 0 || rep.Confidence == Opaque {
		return false
	}
	for _, inv := range rep.Invocations {
		ok := false
		for _, c := range cmds {
			if sameInvocation(c, inv) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func sameInvocation(c StoredRule, inv Invocation) bool {
	if c.Program != inv.Program || len(c.Args) != len(inv.Args) || inv.StdinArgs {
		return false
	}
	for i, a := range inv.Args {
		if a.Dynamic || a.Glob || a.Text != c.Args[i] {
			return false
		}
	}
	if len(c.Redirects) != len(inv.Redirects) {
		return false
	}
	for i, rd := range inv.Redirects {
		if rd.Dynamic || redirectWords(rd) != c.Redirects[i] {
			return false
		}
	}
	return true
}

// redirectWords is how a task rule keeps a redirection: its operator, a space, its target.
func redirectWords(rd Redirect) string {
	return rd.Op + " " + rd.Target
}

// taskRule returns the person's task allow rule that covers a request, if one does.
func (g *PermissionGate) taskRule(ctx context.Context, scope PermissionScope, td ToolDecision) (TaskAllowRule, bool) {
	if g.TaskRules == nil || scope.TaskID == "" || td.Verdict != VerdictAsk {
		return TaskAllowRule{}, false
	}
	for _, r := range g.TaskRules(ctx, scope, td.Action.Tool) {
		if r.matches(scope, td) {
			return r, true
		}
	}
	return TaskAllowRule{}, false
}

// TaskRuleFor returns the rule that approving the waiting request approvalID for the rest of its task would store (without its
// id, author and time), or an error wrapping ErrOnlyOnce that says why the call can only be approved once. Only a request whose
// call still waits can be answered so: the rule is derived from that call's own analysis.
func (g *PermissionGate) TaskRuleFor(approvalID string) (TaskAllowRule, error) {
	v, ok := g.asks.Load(approvalID)
	if !ok {
		return TaskAllowRule{}, fmt.Errorf("%w: no call waits on this request any more", ErrOnlyOnce)
	}
	w := v.(waitingAsk)
	if w.scope.TaskID == "" {
		return TaskAllowRule{}, fmt.Errorf("%w: it belongs to no task", ErrOnlyOnce)
	}
	if why := w.td.taskScopeRefusal(); why != "" {
		return TaskAllowRule{}, fmt.Errorf("%w: %s", ErrOnlyOnce, why)
	}
	if g.Approvals != nil && g.Approvals.redact(w.td.Display) != w.td.Display {
		return TaskAllowRule{}, fmt.Errorf("%w: it holds what looks like a secret, which Drydock does not keep", ErrOnlyOnce)
	}
	r := taskRuleOf(w.scope, w.td)
	r.ApprovalID = approvalID
	return r, nil
}
