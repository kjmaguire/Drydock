package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/workflow"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// Permission prompts of Claude workers outside a sandbox (docs/security.md s4 and s5, decision P40). The pipeline marks such a
// turn (RunSpec.PermissionPrompts); mcpAttach offers it the gateway's permission_prompt tool and the adapter passes
// --permission-prompt-tool. Each request goes through security.PermissionGate: the analyzer allows or refuses what the rules
// decide, and anything else becomes an approval bound to the hash of exactly that tool and input, shown in Needs you and on the
// task page, answered by the human through Respond, and consumed once by the waiting tool call. The tool call waits in the CLI
// meanwhile, so the approval is a security-service approval tied to the task, not a workflow request port (which would end the
// turn).

// PermissionGate is the gate kind of a permission request on the task page (TaskDetail.Pending payload "gate").
const PermissionGate = "permission_prompt"

// permissionToolTimeoutMS is how long the CLI waits for one gateway tool call in a worker that asks Drydock: longer than the
// approval's lifetime, so a person's late answer or the expiry, not the CLI, ends the wait.
var permissionToolTimeoutMS = fmt.Sprintf("%d", (security.DefaultApprovalTTL.Milliseconds() + 5*60*1000))

// permissionService is the gateway's PermissionPrompter.
type permissionService struct {
	c    *Core
	once sync.Once
	gate *security.PermissionGate
	err  error
}

func (p *permissionService) init() (*security.PermissionGate, error) {
	p.once.Do(func() {
		an, err := security.NewAnalyzer()
		if err != nil {
			p.err = err
			return
		}
		audit := security.NewAudit(p.c.DB, p.c.Scanner)
		p.gate = &security.PermissionGate{
			Approvals: security.NewApprovals(p.c.DB, p.c.Bus, audit, security.WithApprovalRedactor(p.c.Scanner)),
			Analyzer:  an, Audit: audit, Bus: p.c.Bus,
			// no project stores allow rules or CEL restrictions in this build: the decision table alone decides
		}
	})
	return p.gate, p.err
}

// Permit implements mcp.PermissionPrompter.
func (p *permissionService) Permit(ctx context.Context, req mcp.PermissionRequest) mcp.PermissionAnswer {
	gate, err := p.init()
	if err != nil {
		return mcp.PermissionAnswer{Outcome: security.PermissionDenied, Message: "Drydock's command analyzer is not available, so it refused this call."}
	}
	out := gate.Decide(ctx,
		security.PermissionScope{Project: req.Run.ProjectID, TaskID: req.Run.TaskID, RunID: req.Run.RunID, AgentID: req.Run.AgentID},
		security.ToolRequest{Tool: req.ToolName, Input: json.RawMessage(req.Input), Root: req.Run.Worktree, WSL: req.Run.WSL})
	return mcp.PermissionAnswer{Allow: out.Allow, Message: out.Message, Outcome: out.Outcome}
}

// offerPermissionPrompts gives a marked worker turn the permission tool: the policy allows it, the CLI is told to ask it, and
// the CLI waits long enough for a person. Only Claude has the mechanism; read-only and sandboxed turns never get it.
func offerPermissionPrompts(cfg *rt.Config, pl *mcpPlan) {
	if !cfg.Spec.PermissionPrompts || cfg.Spec.ReadOnly || cfg.Spec.Sandboxed || cfg.Adapter != "claude" {
		return
	}
	pl.Policy.Allow[mcp.WorkspaceServerID] = append(pl.Policy.Allow[mcp.WorkspaceServerID], mcp.ToolPermissionPrompt)
	pl.Tools = append(pl.Tools, mcp.ToolPermissionPrompt)
	cfg.Spec.PermissionPromptTool = claude.PermissionPromptTool
	for _, e := range cfg.Spec.Env {
		if strings.HasPrefix(e, "MCP_TOOL_TIMEOUT=") {
			return
		}
	}
	cfg.Spec.Env = append(append([]string(nil), cfg.Spec.Env...), "MCP_TOOL_TIMEOUT="+permissionToolTimeoutMS)
}

// isPermissionRequest reports whether an approval row is an open permission request (a security-service row, not a workflow
// request), and the task it belongs to.
func (c *Core) permissionRequestTask(ctx context.Context, id string) (taskID string, ok bool) {
	var runID, title string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT run_id, title, task_id FROM approvals WHERE id=?`, id).Scan(&runID, &title, &taskID); err != nil {
		return "", false
	}
	return taskID, runID == "" && strings.HasPrefix(title, security.PermissionTitlePrefix)
}

// respondPermission records the human's answer to a permission request shown on the page of the run's task. The waiting tool
// call reads it, consumes the approval and answers the CLI.
func (c *Core) respondPermission(ctx context.Context, runID, requestID, taskID, decision string) error {
	info, err := c.Engine.Status(ctx, workflow.RunID(runID))
	if err != nil {
		return err
	}
	if info.TaskID == "" || info.TaskID != taskID {
		return errors.New("this permission request belongs to another task")
	}
	var d security.Decision
	switch decision {
	case "approved":
		d = security.DecisionApprove
	case "rejected":
		d = security.DecisionReject
	default:
		return fmt.Errorf("a permission request is approved or rejected, not %q", decision)
	}
	gate, err := c.permissions().init()
	if err != nil {
		return err
	}
	return gate.Approvals.Decide(ctx, requestID, d, c.Human())
}

// permissions returns the Core's permission service (the one the gateway uses).
func (c *Core) permissions() *permissionService {
	c.permOnce.Do(func() { c.perm = &permissionService{c: c} })
	return c.perm
}

// permissionPending lists a task's open permission requests as pending requests of its run, for the task page. The payload
// holds the tool and its input as stored for display (secrets redacted; the short fields, where the call goes, come first), the
// paths and addresses the call names, whether a long value is cut in the input, the approval kind, why it was asked and when it
// expires; the hash binds the exact whole input.
func (c *Core) permissionPending(ctx context.Context, taskID string) []workflow.PendingRequest {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id FROM approvals WHERE status='REQUESTED' AND run_id='' AND task_id=? AND title LIKE ? ORDER BY created_at, rowid`,
		taskID, security.PermissionTitlePrefix+"%")
	if err != nil {
		return nil
	}
	var idl []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			idl = append(idl, id)
		}
	}
	rows.Close()
	if len(idl) == 0 {
		return nil
	}
	gate, err := c.permissions().init()
	if err != nil {
		return nil
	}
	var out []workflow.PendingRequest
	for _, id := range idl {
		info, err := gate.Approvals.Get(ctx, id)
		if err != nil || info.Status != "REQUESTED" {
			continue
		}
		targets := []string{}
		for _, t := range info.Action.Targets {
			if x := firstNonEmpty(t.Path, t.Remote); x != "" {
				targets = append(targets, x)
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"gate": PermissionGate, "tool": toolOfAction(info.Action.Tool), "input": info.Action.Command,
			"targets": targets, "shortened": info.Action.Shortened,
			"kind": info.ActionKind, "reason": info.Reason, "agentId": info.Action.AgentID, "expiresAt": info.ExpiresAt,
		})
		out = append(out, workflow.PendingRequest{ID: workflow.RequestID(id), Spec: workflow.RequestSpec{Kind: "approval", Title: info.Title, Payload: payload, ActionHash: info.ActionHash}})
	}
	return out
}

// permissionAsker is Runtime.Permissions: an ACP worker turn outside a sandbox (the pipeline sets PermissionPrompts) asks over
// session/request_permission instead of a gateway tool, and its requests go through the same gate as a Claude worker's: the
// analyzer allows or refuses what the rules decide, anything else becomes an approval of exactly that call on the task page.
// Claude workers ask through the gateway (offerPermissionPrompts); reviewers, planners and other read-only turns, sandboxed
// turns and chats (which ask in the chat) get nil here, so their requests stay refused.
func (c *Core) permissionAsker(ctx context.Context, cfg *rt.Config, runID string) agents.PermissionAsker {
	if !cfg.Spec.PermissionPrompts || cfg.Spec.ReadOnly || cfg.Spec.Sandboxed || cfg.Adapter == "claude" || cfg.Spec.Cwd == "" {
		return nil
	}
	scope := security.PermissionScope{Project: cfg.ProjectID, TaskID: cfg.TaskID, RunID: runID, AgentID: cfg.AgentID}
	root, cli := cfg.Spec.Cwd, cfg.Adapter
	var paths *wsl.Paths
	if cfg.Spec.IsWSL() {
		p, ok := c.wslPaths(ctx, cfg.Spec.WSLDistro)
		if !ok {
			return func(context.Context, agents.ToolAsk) (bool, string) {
				return false, "The WSL distro " + cfg.Spec.WSLDistro + " could not be probed, so Drydock cannot check this call and refused it."
			}
		}
		paths = &p
	}
	return func(ctx context.Context, q agents.ToolAsk) (bool, string) {
		gate, err := c.permissions().init()
		if err != nil {
			return false, "Drydock's command analyzer is not available, so it refused this call."
		}
		req := acpToolRequest(q)
		req.Root, req.CLI, req.WSL = root, cli, paths
		out := gate.Decide(ctx, scope, req)
		return out.Allow, out.Message
	}
}

// acpBenignShellKeys are the fields an ACP shell call may carry besides its command without changing what runs: a call with
// any other field (a working folder, an environment) is not read as a plain command, so a person sees it whole.
var acpBenignShellKeys = map[string]bool{"command": true, "description": true, "explanation": true, "timeout": true, "is_background": true}

// acpToolRequest reads an ACP tool call as the analyzer's tool vocabulary where that reading is exact, so the same rules decide
// it as decide a Claude call: a shell call that is only a command string is Bash; a read or search of one path is Read; an
// edit or a delete of one path is Write. Anything else (a move between two paths, a fetch, a call whose fields would change
// what runs) keeps the agent's own name and input, which no Drydock rule allows, so a person decides it.
func acpToolRequest(q agents.ToolAsk) security.ToolRequest {
	var fields map[string]any
	_ = json.Unmarshal(q.Args, &fields)
	switch q.Kind {
	case "execute":
		cmd, ok := fields["command"].(string)
		plain := ok && strings.TrimSpace(cmd) != ""
		for k := range fields {
			if !acpBenignShellKeys[k] {
				plain = false
			}
		}
		if plain {
			in, _ := json.Marshal(map[string]string{"command": cmd})
			return security.ToolRequest{Tool: "Bash", Input: in}
		}
	case "read", "search":
		if len(q.Locations) == 1 {
			in, _ := json.Marshal(map[string]string{"file_path": q.Locations[0]})
			return security.ToolRequest{Tool: "Read", Input: in}
		}
	case "edit", "delete":
		if len(q.Locations) == 1 {
			in, _ := json.Marshal(map[string]string{"file_path": q.Locations[0]})
			return security.ToolRequest{Tool: "Write", Input: in}
		}
	}
	name := strings.TrimSpace(firstNonEmpty(q.Tool, q.Kind))
	if name == "" {
		name = "tool"
	}
	in := q.Args
	if len(in) == 0 {
		in = json.RawMessage("null")
	}
	return security.ToolRequest{Tool: "acp:" + name, Input: in}
}

// toolOfAction is the tool a permission approval names, without the asking CLI ("claude:Bash" and "gemini:Bash" are Bash).
func toolOfAction(t string) string {
	if _, tool, ok := strings.Cut(t, ":"); ok {
		return tool
	}
	return t
}
