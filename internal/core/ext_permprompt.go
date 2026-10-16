package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/agents/claude"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/workflow"
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
			"gate": PermissionGate, "tool": strings.TrimPrefix(info.Action.Tool, "claude:"), "input": info.Action.Command,
			"targets": targets, "shortened": info.Action.Shortened,
			"kind": info.ActionKind, "reason": info.Reason, "agentId": info.Action.AgentID, "expiresAt": info.ExpiresAt,
		})
		out = append(out, workflow.PendingRequest{ID: workflow.RequestID(id), Spec: workflow.RequestSpec{Kind: "approval", Title: info.Title, Payload: payload, ActionHash: info.ActionHash}})
	}
	return out
}
