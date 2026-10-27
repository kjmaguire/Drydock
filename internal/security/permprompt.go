package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// Permission prompts (docs/security.md s4 and s5): an agent CLI asks Drydock before it uses a tool its own allow rules do not
// cover (Claude Code's --permission-prompt-tool, answered by the gateway's permission_prompt tool). The request runs through the
// analyzer: what the rules allow is allowed, what the analyzer refuses is denied with the reason, and everything else waits for
// a person's answer on an approval bound to the hash of exactly that tool and that input. The agent never answers its own
// request: only Decide, which refuses every principal but "human:<name>", moves the approval.

// KindAgentTool is the approval kind of a permission request for a tool no Drydock rule classifies (a web fetch, a tool of
// another MCP server, a write into .git): the person decides.
const KindAgentTool = "agent_tool"

// PermissionTitlePrefix starts the title of every permission-request approval, so Needs you and the task page can tell
// them apart from other approvals.
const PermissionTitlePrefix = "Permission request: "

// Outcomes of a permission request, as recorded in the permission.decided event and the audit log.
const (
	PermissionAllowedByRule = "allowed_by_rule"
	PermissionApproved      = "approved"
	PermissionDenied        = "denied"    // the analyzer refused it (hard deny), or it could not be checked
	PermissionRejected      = "rejected"  // a person refused it
	PermissionTimedOut      = "timed_out" // nobody answered before the approval expired
	PermissionCancelled     = "cancelled" // the run ended, or the CLI gave up waiting
	// PermissionApprovedEdited: a person approved the call with input they changed (P88); the CLI runs the changed input.
	PermissionApprovedEdited = "approved_with_edits"
)

// EventPermissionDecided is published once per permission request with the outcome. It never carries the input, only its
// digest.
const EventPermissionDecided = "permission.decided"

// ToolRequest is one permission request of an agent CLI.
type ToolRequest struct {
	Tool  string          // the CLI's tool name: "Bash", "Edit", "WebFetch", "mcp__github__create_issue"
	Input json.RawMessage // the exact input the CLI will run the tool with
	Root  string          // the worktree the agent works in
	// CLI names the agent CLI that asks ("claude" when empty): the approval's tool is "<cli>:<tool>".
	CLI string
	// WSL is set when the agent runs inside a WSL distro (decision P41): its shell commands are bash with Unix paths and its
	// file paths are Linux paths, mapped to their real Windows locations before they are checked against Root.
	WSL *wsl.Paths
}

// ToolDecision is the analyzer's answer to a ToolRequest.
type ToolDecision struct {
	Verdict Verdict
	Kind    string   // approval kind when Verdict is ask
	Reasons []string // why; never the input itself
	// Action is the descriptor an approval is bound to. The caller adds the identity (project, task, run, agent).
	Action ActionDescriptor
	// Display is the input as a person reads it: the command of a shell tool, else the input as JSON with the short fields (where
	// it goes) first, shown whole up to MaxReviewBytes (ReviewInput).
	Display string
	// Shortened is set when a long value is cut in Display; the approval still covers the whole input.
	Shortened bool

	// what a task allow rule is checked against (taskrules.go): the analysis of a shell command and its decision, and whether a
	// file tool writes inside .git
	report  *CommandReport
	command *CommandDecision
	gitDir  bool
}

// shellTools are the command tools of Claude Code, with the flavor their command is parsed as.
var shellTools = map[string]ShellKind{"Bash": ShellBash, "PowerShell": ShellPowerShell}

// fileTools maps the file tools of Claude Code to whether they write.
var fileTools = map[string]bool{
	"Read": false, "Glob": false, "Grep": false, "LS": false, "NotebookRead": false,
	"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true,
}

// PermissionPromptToolName is the gateway tool's bare name; a request for it (the agent asking about the tool that answers
// its own requests) is always denied.
const PermissionPromptToolName = "permission_prompt"

// InputDigest is the SHA-256 of the canonical JSON of {"tool": tool, "input": input}: what a permission approval binds.
func InputDigest(tool string, input json.RawMessage) (string, error) {
	in := input
	if len(in) == 0 {
		in = json.RawMessage("null")
	}
	b, err := CanonicalJSON(map[string]any{"tool": tool, "input": in})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ClassifyToolRequest runs a permission request through the analyzer and the path guard. pol may be nil (no stored rules).
// Shell tools are analyzed as commands (docs/security.md s7); exec_code, which the decision table allows only when the CLI's
// own mode permits, is asked here, because a request is the CLI saying that its mode did not permit it. File tools are
// checked with the path guard: inside the worktree is allowed (a write into .git is asked), a known CLI credential file is
// denied, secret material and anything outside the worktree are asked. Every other tool is asked.
func ClassifyToolRequest(ctx context.Context, an *RuleAnalyzer, pol *Policy, req ToolRequest) (ToolDecision, error) {
	digest, err := InputDigest(req.Tool, req.Input)
	if err != nil {
		return ToolDecision{}, fmt.Errorf("security: the input of %s is not JSON: %w", req.Tool, err)
	}
	rv, rerr := ReviewInput(req.Input)
	d := ToolDecision{Display: rv.Text, Shortened: rv.Shortened}
	d.Action = ActionDescriptor{Tool: firstNonEmpty(req.CLI, "claude") + ":" + req.Tool, Cwd: req.Root, Command: d.Display, ArgvDigest: digest, Targets: rv.Targets, Shortened: rv.Shortened}
	var fields map[string]any
	_ = json.Unmarshal(req.Input, &fields)
	str := func(k string) string { s, _ := fields[k].(string); return s }

	ask := func(kind string, why ...string) (ToolDecision, error) {
		d.Verdict, d.Kind, d.Action.Kind = VerdictAsk, kind, kind
		d.Reasons = append(d.Reasons, why...)
		if len(d.Reasons) == 0 {
			d.Reasons = append(d.Reasons, "the analyzer classifies it as "+kind)
		}
		return d, nil
	}
	deny := func(why ...string) (ToolDecision, error) {
		d.Verdict, d.Kind, d.Action.Kind = VerdictDeny, "", KindAgentTool
		d.Reasons = append(d.Reasons, why...)
		return d, nil
	}
	allow := func(why string) (ToolDecision, error) {
		d.Verdict, d.Kind, d.Action.Kind = VerdictAllow, "", KindAgentTool
		d.Reasons = append(d.Reasons, why)
		return d, nil
	}

	switch {
	case req.Tool == "" || strings.HasSuffix(req.Tool, "__"+PermissionPromptToolName) || req.Tool == PermissionPromptToolName:
		return deny("the permission tool answers the agent's requests; the agent cannot ask about it")
	case req.Root == "":
		return deny("the request names no worktree, so nothing can be checked")
	}
	if _, isShell := shellTools[req.Tool]; !isShell && rerr != nil {
		// a person cannot be shown this input, so a person cannot allow it (a shell command is shown whole, as it runs)
		return deny("its input is too large or too long for a person to read (" + rerr.Error() + ")")
	}

	if shell, ok := shellTools[req.Tool]; ok {
		cmd := str("command")
		d.Action.Shell, d.Action.Command, d.Display = string(shell), cmd, cmd
		if strings.TrimSpace(cmd) == "" {
			return ask(KindShellUnanalyzable, "the request carries no command")
		}
		in := CommandInput{Raw: cmd, Shell: shell, Cwd: req.Root, Root: req.Root, WSL: req.WSL}
		if req.WSL != nil {
			in.Shell = ShellBash // a Linux shell, whatever the tool is called
			d.Action.Shell = string(ShellBash)
		}
		rep, err := an.Analyze(ctx, in)
		if err != nil {
			return ToolDecision{}, err
		}
		cd := pol.Decide(rep)
		desc := DescribeCommand(in, rep, cd.Kind)
		desc.Tool, desc.ArgvDigest = d.Action.Tool, digest
		d.Action = desc
		d.report, d.command = &rep, &cd
		d.Reasons = append(d.Reasons, cd.Reasons...)
		switch {
		case cd.Verdict == VerdictDeny:
			d.Verdict, d.Action.Kind = VerdictDeny, KindShellDestructive
			return d, nil
		case cd.Verdict == VerdictAllow && rep.HasClass(ClassExecCode):
			return ask(KindShellCommand, "it runs code the analyzer cannot see into, and the CLI's own rules did not allow it")
		case cd.Verdict == VerdictAllow:
			d.Verdict, d.Action.Kind = VerdictAllow, KindShellCommand
			if cd.Stored >= 0 {
				d.Reasons = append(d.Reasons, "a stored rule of the project allows it")
			} else {
				d.Reasons = append(d.Reasons, "read-only or a change inside the worktree, fully analyzed")
			}
			return d, nil
		}
		return ask(firstNonEmpty(cd.Kind, KindShellCommand))
	}

	if writes, ok := fileTools[req.Tool]; ok {
		path := firstNonEmpty(str("file_path"), firstNonEmpty(str("notebook_path"), str("path")))
		if path == "" {
			if writes {
				return ask(KindAgentTool, "the request names no file")
			}
			return allow("reads in the working folder")
		}
		norm := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
		for _, c := range an.Rules().CredentialPaths {
			if strings.Contains(norm, c) {
				return deny("a known CLI credential file (" + c + ") is never readable by an agent")
			}
		}
		resolve := an.guard.Resolve
		if req.WSL != nil {
			p := *req.WSL
			resolve = func(root, c string) (string, error) { return an.guard.ResolveWSL(p, root, c) }
		}
		full, gerr := resolve(req.Root, path)
		if gerr == nil {
			d.Action.Targets = []Target{{Path: full}}
		} else {
			d.Action.Targets = []Target{{Path: path}}
		}
		if an.Rules().IsSecretPath(strings.ReplaceAll(path, `\`, "/")) {
			return ask(KindSecretsAccess, "the path looks like secret material")
		}
		if gerr != nil {
			var pe *PathError
			if errors.As(gerr, &pe) {
				return ask(KindFSOutsideWorktree, "the path is outside the worktree ("+pe.Kind.Error()+")")
			}
			return ask(KindFSOutsideWorktree, "the path could not be checked against the worktree")
		}
		root := req.Root
		if r, err := resolve(req.Root, "."); err == nil {
			root = r // resolved the way full was (on Windows an 8.3 short name such as RUNNER~1 is expanded in both)
		}
		if writes && touchesGitDir(root, full) {
			d.gitDir = true
			return ask(KindAgentTool, "it writes inside .git, where hooks and configuration run code")
		}
		if writes {
			return allow("a change inside the worktree")
		}
		return allow("a read inside the worktree")
	}
	return ask(KindAgentTool, "no Drydock rule covers this tool")
}

// touchesGitDir reports whether full (a resolved path inside root) is in root's .git folder or file. When full does not
// start with root as written (two spellings of one folder), it fails closed: any path component named .git counts.
func touchesGitDir(root, full string) bool {
	f := strings.ReplaceAll(full, `\`, "/")
	r := strings.TrimRight(strings.ReplaceAll(root, `\`, "/"), "/")
	if len(f) < len(r) || !strings.EqualFold(f[:len(r)], r) || (len(f) > len(r) && f[len(r)] != '/') {
		for _, part := range strings.Split(f, "/") {
			if strings.EqualFold(part, ".git") {
				return true
			}
		}
		return false
	}
	rel := strings.TrimPrefix(f[len(r):], "/")
	first, _, _ := strings.Cut(rel, "/")
	return strings.EqualFold(first, ".git")
}

// permissionSummary is the one line the approval lists show for a request: where it goes, else the first line of what it
// does. The whole input is on the approval card.
func permissionSummary(td ToolDecision) string {
	if td.Action.Shell == "" { // a shell command is its own summary; any other tool is summed up by where it goes
		for _, t := range td.Action.Targets {
			if t.Path != "" {
				return cutRunes(t.Path, 160)
			}
			if t.Remote != "" {
				return cutRunes(t.Remote, 160)
			}
		}
	}
	line, _, _ := strings.Cut(td.Display, "\n")
	return cutRunes(line, 160)
}

// cutRunes returns s cut to at most n bytes at a character boundary, with an ellipsis when it was cut.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

// PermissionScope is who asks: the identity an approval is bound to besides the action.
type PermissionScope struct {
	Project, TaskID string
	RunID           string // the agent run (arun_...): one CLI process
	AgentID         string
}

// PermissionOutcome is the answer to a permission request.
type PermissionOutcome struct {
	Allow      bool
	Message    string // for a refusal: what the agent is told
	Outcome    string // one of the Permission* outcomes
	Kind       string // approval kind, when one was asked
	ApprovalID string
	// UpdatedInput, when set, is the input the person approved instead of the agent's (canonical JSON): the CLI runs the
	// tool with it. Only an approval with edits sets it.
	UpdatedInput json.RawMessage
	// Rule is the person's task allow rule that approved the call (outcome PermissionApprovedForTask): the rule that was stored
	// when they approved this very request for the task, or the one that covered it without asking.
	Rule string
}

// PermissionGate answers permission requests: analyzer first, then a person on a hashed approval.
type PermissionGate struct {
	Approvals *Approvals
	Analyzer  *RuleAnalyzer
	// Policy returns the project's stored policy (stored allow rules and CEL restrictions); nil means none.
	Policy func(ctx context.Context, project string) *Policy
	Audit  *Audit
	Bus    events.Bus
	TTL    time.Duration // how long a person has to answer (default DefaultApprovalTTL)
	Poll   time.Duration // how often the approval is read while waiting (default 250 ms)
	// TaskRules returns the person's allow rules of the task for this agent and tool (TaskAllowRule, taskrules.go); nil or
	// none means every request that is asked waits for a person.
	TaskRules func(ctx context.Context, scope PermissionScope, tool string) []TaskAllowRule

	edits sync.Map // approval id -> json.RawMessage: input a person changed before approving (kept in memory only)
	asks  sync.Map // approval id -> waitingAsk: the request a waiting approval is for, while its call waits
	// forTask maps an approval id to the id of the task allow rule a person's "approve for this task" stored for it, so the
	// answered request itself is recorded as approved for the task, with its rule, and not as a plain approval.
	forTask sync.Map
}

// ErrEditNotPossible is returned for an edit the request cannot take: not a JSON object, or a CLI that cannot run an
// edited call (an ACP agent's request: its protocol allows or refuses a call, it cannot change one).
var ErrEditNotPossible = errors.New("this call cannot be approved with edits")

// SetEdit holds the input a person changed for a waiting request, for the moment it is approved (P88). The edit lives in
// memory only: it may hold text the person typed, and the approvals table keeps no secrets. Call it right before the approval
// is decided; ClearEdit drops it when the decision fails.
func (g *PermissionGate) SetEdit(approvalID, tool string, input json.RawMessage) error {
	if !strings.HasPrefix(tool, "claude:") { // the approval's tool is "<cli>:<tool>"; only Claude's prompt tool takes a changed input
		return ErrEditNotPossible
	}
	canon, err := CanonicalJSON(input)
	if err != nil || len(canon) == 0 || canon[0] != '{' {
		return fmt.Errorf("%w: the input must be a JSON object", ErrEditNotPossible)
	}
	g.edits.Store(approvalID, json.RawMessage(canon))
	return nil
}

// ClearEdit drops a held edit.
func (g *PermissionGate) ClearEdit(approvalID string) { g.edits.Delete(approvalID) }

// MarkApprovedForTask tells the gate that the approval about to be decided is a person's "approve for this task" (P97), with
// the id of the task allow rule stored for it: the waiting call is then recorded with outcome PermissionApprovedForTask and the
// rule, like the later calls the rule covers, so the Activity does not say the grant was for one call. Call it right before
// the approval is decided; ClearApprovedForTask drops the mark when the decision fails. An approval with edits is never
// task-scoped: the mark is ignored when an edit is held.
func (g *PermissionGate) MarkApprovedForTask(approvalID, ruleID string) {
	g.forTask.Store(approvalID, ruleID)
}

// ClearApprovedForTask drops a mark set by MarkApprovedForTask.
func (g *PermissionGate) ClearApprovedForTask(approvalID string) { g.forTask.Delete(approvalID) }

func (g *PermissionGate) takeEdit(approvalID string) (json.RawMessage, bool) {
	v, ok := g.edits.LoadAndDelete(approvalID)
	if !ok {
		return nil, false
	}
	return v.(json.RawMessage), true
}

// Decide answers one request. It blocks until the rules decide or a person answers, the approval expires, or ctx ends (the run
// closed or the CLI gave up), and records the outcome. It never returns an allow that is not either a rule or a consumed
// approval of exactly this tool and input.
func (g *PermissionGate) Decide(ctx context.Context, scope PermissionScope, req ToolRequest) PermissionOutcome {
	if g == nil || g.Analyzer == nil {
		return PermissionOutcome{Outcome: PermissionDenied, Message: "Drydock cannot check permission requests right now, so it refused this one."}
	}
	var pol *Policy
	if g.Policy != nil {
		pol = g.Policy(ctx, scope.Project)
	}
	td, err := ClassifyToolRequest(ctx, g.Analyzer, pol, req)
	if err != nil {
		out := PermissionOutcome{Outcome: PermissionDenied, Message: "Drydock could not check this request (" + err.Error() + "), so it refused it."}
		g.record(ctx, scope, req, td, out)
		return out
	}
	switch td.Verdict {
	case VerdictAllow:
		out := PermissionOutcome{Allow: true, Outcome: PermissionAllowedByRule}
		g.record(ctx, scope, req, td, out)
		return out
	case VerdictDeny:
		out := PermissionOutcome{Outcome: PermissionDenied, Message: "Drydock refused this " + req.Tool + " call: " + strings.Join(td.Reasons, "; ") + ". This is not approvable from an agent; do it another way or stop and say what you needed."}
		g.record(ctx, scope, req, td, out)
		return out
	}
	if r, ok := g.taskRule(ctx, scope, td); ok {
		out := PermissionOutcome{Allow: true, Outcome: PermissionApprovedForTask, Kind: td.Kind, Rule: r.ID}
		g.record(ctx, scope, req, td, out)
		return out
	}
	out := g.ask(ctx, scope, req, td, pol)
	g.record(ctx, scope, req, td, out)
	return out
}

func (g *PermissionGate) bind(scope PermissionScope, a ActionDescriptor) ActionDescriptor {
	a.Project, a.TaskID, a.RunID, a.AgentID = scope.Project, scope.TaskID, scope.RunID, scope.AgentID
	return a
}

func (g *PermissionGate) ask(ctx context.Context, scope PermissionScope, req ToolRequest, td ToolDecision, pol *Policy) PermissionOutcome {
	out := PermissionOutcome{Kind: td.Kind}
	if g.Approvals == nil {
		out.Outcome, out.Message = PermissionDenied, "this "+req.Tool+" call needs a person's approval and none can be asked here, so Drydock refused it."
		return out
	}
	ttl := g.TTL
	if ttl <= 0 {
		ttl = DefaultApprovalTTL
	}
	poll := g.Poll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	action := g.bind(scope, td.Action)
	reason := "A Claude worker asks to use " + req.Tool + ", which its rules do not allow. Allow only this exact call."
	if req.CLI != "" && req.CLI != "claude" {
		reason = "A " + req.CLI + " worker asks to use " + req.Tool + ", which its rules do not allow. Allow only this exact call."
	}
	if len(td.Reasons) > 0 {
		reason += " Analyzer: " + strings.Join(td.Reasons, "; ") + "."
	}
	// the waiting call is registered before the request can be seen: a person who approves it for the rest of the task at
	// once (TaskRuleFor) must not be told that no call waits on it
	var registered string
	id, err := g.Approvals.Request(ctx, action, reason, WithTTL(ttl), WithTitle(PermissionTitlePrefix+req.Tool+": "+permissionSummary(td)),
		BeforeVisible(func(id string) {
			registered = id
			g.asks.Store(id, waitingAsk{scope: scope, td: td})
		}))
	if err != nil {
		if registered != "" {
			g.asks.Delete(registered)
		}
		out.Outcome, out.Message = PermissionDenied, "Drydock could not ask a person about this call, so it refused it."
		return out
	}
	out.ApprovalID = id
	defer g.asks.Delete(id)
	defer g.forTask.Delete(id) // a mark nobody consumed (the approval was refused, or did not match) is not kept
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		info, err := g.Approvals.Get(context.WithoutCancel(ctx), id)
		if err != nil {
			out.Outcome, out.Message = PermissionDenied, "Drydock lost track of the approval for this call, so it refused it."
			return out
		}
		switch info.Status {
		case "REQUESTED":
			if info.ExpiresAt > 0 && g.Approvals.nowMS() > info.ExpiresAt {
				_, _ = g.Approvals.ExpireDue(context.WithoutCancel(ctx))
				continue
			}
		case "APPROVED":
			// the executor proves its action: the descriptor is computed again from the request that is about to be answered
			again, cerr := ClassifyToolRequest(context.WithoutCancel(ctx), g.Analyzer, nil, req)
			if cerr != nil {
				out.Outcome, out.Message = PermissionDenied, "Drydock could not check this call again after it was approved, so it refused it."
				return out
			}
			again.Action.Kind = td.Action.Kind // the kind the person saw; everything else is recomputed
			edited, hasEdit := g.takeEdit(id)
			if hasEdit {
				// the person changed the input: the changed call is checked by the rules like any call (a refusal stands even
				// for a person's edit), and only then is the approval of the request they answered spent on it
				ed := req
				ed.Input = edited
				check, cerr := ClassifyToolRequest(context.WithoutCancel(ctx), g.Analyzer, pol, ed)
				if cerr != nil || check.Verdict == VerdictDeny {
					_ = g.Approvals.Cancel(context.WithoutCancel(ctx), id, "system")
					why := "it could not be checked"
					if cerr == nil {
						why = strings.Join(check.Reasons, "; ")
					}
					out.Outcome, out.Message = PermissionDenied, "The call as a person changed it is one Drydock's rules refuse ("+why+"), so Drydock refused it. Continue without it, or stop and say what you needed."
					return out
				}
			}
			if err := g.Approvals.Consume(context.WithoutCancel(ctx), id, g.bind(scope, again.Action)); err != nil {
				out.Outcome, out.Message = PermissionDenied, "The approval did not match this exact call, so Drydock refused it."
				return out
			}
			out.Allow, out.Outcome = true, PermissionApproved
			if rule, forTask := g.forTask.LoadAndDelete(id); forTask && !hasEdit {
				out.Outcome, out.Rule = PermissionApprovedForTask, rule.(string)
			}
			if hasEdit {
				out.Outcome, out.UpdatedInput = PermissionApprovedEdited, edited
			}
			return out
		case "REJECTED":
			out.Outcome, out.Message = PermissionRejected, "A person refused this "+req.Tool+" call in Drydock. Do not try to reach the same result another way; continue without it, or stop and say what you needed."
			return out
		case "EXPIRED":
			out.Outcome, out.Message = PermissionTimedOut, fmt.Sprintf("Nobody answered within %s, so Drydock refused this %s call. Continue without it, or stop and say what you needed.", ttl.Round(time.Minute), req.Tool)
			return out
		default: // CANCELLED, CONSUMED by someone else
			out.Outcome, out.Message = PermissionCancelled, "The request for this "+req.Tool+" call was withdrawn, so Drydock refused it."
			return out
		}
		select {
		case <-ctx.Done():
			_ = g.Approvals.Cancel(context.WithoutCancel(ctx), id, "system")
			out.Outcome, out.Message = PermissionCancelled, "The run ended before anyone answered, so Drydock refused this "+req.Tool+" call."
			return out
		case <-tick.C:
		}
	}
}

// record publishes permission.decided and appends an audit entry: tool, outcome, kind, approval and the input's digest. The
// input itself is never recorded here (the approval's display copy, redacted, is the only stored form).
func (g *PermissionGate) record(ctx context.Context, scope PermissionScope, req ToolRequest, td ToolDecision, out PermissionOutcome) {
	data := map[string]any{"tool": req.Tool, "outcome": out.Outcome, "allowed": out.Allow, "kind": out.Kind, "inputDigest": td.Action.ArgvDigest}
	if len(out.UpdatedInput) > 0 { // what ran instead, as a digest: the edit itself is never stored
		sum := sha256.Sum256(out.UpdatedInput)
		data["editedInputDigest"] = hex.EncodeToString(sum[:])
	}
	if out.ApprovalID != "" {
		data["approval"] = out.ApprovalID
	}
	if out.Rule != "" {
		data["rule"] = out.Rule
	}
	ctx = context.WithoutCancel(ctx)
	if g.Audit != nil {
		_, _ = g.Audit.Append(ctx, AuditEntry{Actor: "system", Action: EventPermissionDecided, Subject: firstNonEmpty(out.ApprovalID, scope.RunID), Detail: data})
	}
	if g.Bus != nil {
		_, _ = events.Emit(ctx, g.Bus, EventPermissionDecided, events.Event{ProjectID: scope.Project, RunID: scope.RunID, TaskID: scope.TaskID, AgentID: scope.AgentID}, data)
	}
}
