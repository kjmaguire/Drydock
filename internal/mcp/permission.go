package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/security"
)

// The permission prompt tool (docs/security.md s4 and s5). Claude Code started with --permission-prompt-tool
// mcp__drydock__permission_prompt calls it, in a headless session, before it uses a tool its allow rules do not cover, with
// {"tool_name", "input", "tool_use_id"}, and reads the answer from a single text block holding JSON: {"behavior":"allow",
// "updatedInput":{...}} or {"behavior":"deny","message":"..."} (read from the CLI 2.1.296 itself; a live call is contract
// CLAUDE-PERMPROMPT-01). The answer is not agent-facing text, so it skips the result pipeline (no untrusted wrapper, no
// shaping): the CLI parses it.

// permissionPromptAllowed reports whether this run's policy offers the tool. Only an entry naming it exactly does: a glob such
// as "*" never hands a run the tool that answers its CLI's permission prompts.
func (r *Run) permissionPromptAllowed() bool {
	return r.workspaceUsable() && slices.Contains(r.cfg.Policy.Allow[WorkspaceServerID], ToolPermissionPrompt)
}

func permissionPromptTool() *sdk.Tool {
	return &sdk.Tool{
		Name: ToolPermissionPrompt,
		Description: "Drydock answers Claude Code's permission prompts with this tool (--permission-prompt-tool). Claude Code calls it " +
			"by itself; do not call it: a call grants nothing and a person sees it.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"tool_name", "input"},
			"properties": map[string]any{
				"tool_name":   map[string]any{"type": "string"},
				"input":       map[string]any{"type": "object"},
				"tool_use_id": map[string]any{"type": "string"},
			},
		},
	}
}

// permissionAnswer is the JSON text the CLI reads.
func permissionAnswer(allow bool, input json.RawMessage, message string) *sdk.CallToolResult {
	var b []byte
	if allow {
		// updatedInput is the exact input that was decided on: the CLI runs the tool with it
		b, _ = json.Marshal(map[string]any{"behavior": "allow", "updatedInput": input})
	} else {
		b, _ = json.Marshal(map[string]any{"behavior": "deny", "message": message})
	}
	return textResult(string(b), false)
}

func (r *Run) permissionPrompt(ctx context.Context, c *callRec, args json.RawMessage) *sdk.CallToolResult {
	refuse := func(reason, message string) *sdk.CallToolResult {
		c.decision, c.reason, c.upstreamStatus = DecisionDenied, reason, "ok"
		return permissionAnswer(false, nil, message)
	}
	var req struct {
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	}
	if err := json.Unmarshal(args, &req); err != nil || strings.TrimSpace(req.ToolName) == "" {
		return refuse("malformed permission request", "Drydock could not read this permission request, so it refused it.")
	}
	in := bytes.TrimSpace(req.Input)
	if len(in) == 0 || in[0] != '{' {
		return refuse("malformed permission request", "Drydock could not read the input of this "+req.ToolName+" call, so it refused it.")
	}
	p := r.g.opts.Permissions
	if p == nil {
		return refuse("no permission service", "Drydock cannot answer permission requests in this run, so it refused this "+req.ToolName+" call.")
	}
	// the call waits for a person at most as long as the run lives
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-r.done:
			cancel()
		case <-wctx.Done():
		}
	}()
	ans := p.Permit(wctx, PermissionRequest{Run: r.cfg.Info, CallID: c.id, ToolName: req.ToolName, Input: in, ToolUseID: req.ToolUseID})
	c.upstreamStatus = "ok"
	if !ans.Allow {
		c.decision, c.reason = DecisionDenied, "permission for "+req.ToolName+": "+ans.Outcome
		msg := ans.Message
		if msg == "" {
			msg = "Drydock refused this " + req.ToolName + " call."
		}
		return permissionAnswer(false, nil, msg)
	}
	c.decision, c.reason = DecisionAllowed, "permission for "+req.ToolName+": "+ans.Outcome
	// The CLI runs the tool with the input it is sent back. That is the canonical text the approval hashed and a person read, not
	// the agent's own bytes (a duplicate key or another spelling could be read differently there; audit 2026-10-16, H8).
	canon, err := security.CanonicalJSON(json.RawMessage(in))
	if err != nil {
		return refuse("input not canonical", "Drydock could not read the input of this "+req.ToolName+" call again after it was approved, so it refused it.")
	}
	return permissionAnswer(true, json.RawMessage(canon), "")
}
