package contract_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/contract"
)

// permMCPServer is a real stdio MCP server, a labelled test fixture standing in for Drydock's gateway: its one tool,
// permission_prompt, records every request it gets (one JSON line in DRYDOCK_CONTRACT_PERM_LOG) and allows, with the input
// unchanged as updatedInput, a Bash command that contains DRYDOCK_CONTRACT_PERM_ALLOW; it refuses everything else with the
// message DRYDOCK_CONTRACT_PERM_DENY. The answer has the shape internal/mcp/permission.go sends.
func permMCPServer() int {
	srv := sdk.NewServer(&sdk.Implementation{Name: "drydock-contract-perm", Version: "1"}, nil)
	type req struct {
		ToolName  string         `json:"tool_name"`
		Input     map[string]any `json:"input"`
		ToolUseID string         `json:"tool_use_id"`
	}
	sdk.AddTool(srv, &sdk.Tool{Name: "permission_prompt", Description: "Answers Claude Code's permission prompts."},
		func(_ context.Context, cr *sdk.CallToolRequest, r req) (*sdk.CallToolResult, any, error) {
			if f, err := os.OpenFile(os.Getenv("DRYDOCK_CONTRACT_PERM_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintf(f, "%s\n", cr.Params.Arguments)
				f.Close()
			}
			ans := map[string]any{"behavior": "deny", "message": os.Getenv("DRYDOCK_CONTRACT_PERM_DENY")}
			if cmd, _ := r.Input["command"].(string); r.ToolName == "Bash" && strings.Contains(cmd, os.Getenv("DRYDOCK_CONTRACT_PERM_ALLOW")) {
				ans = map[string]any{"behavior": "allow", "updatedInput": r.Input}
			}
			b, _ := json.Marshal(ans)
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil, nil
		})
	if err := srv.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "perm mcp server:", err)
		return 1
	}
	return 0
}

// CLAUDE-PERMPROMPT-01, the free half: the worker argv with --permission-prompt-tool is accepted (an empty stream-json input
// parses the arguments and exits without a model turn). Claude Code does not look the tool up before the first prompt, so this
// half cannot see a wrong tool name: the paid half does.
func TestClaudeAcceptsThePermissionPromptToolFlag(t *testing.T) {
	exe := installedClaude(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "mcp.json")
	writeFile(t, cfg, `{"mcpServers":{}}`)
	args := drydockClaude().Args(agents.RunSpec{PermissionMode: "acceptEdits", MCPConfigPath: cfg, PermissionPrompts: true, PermissionPromptTool: claude.PermissionPromptTool})
	if !strings.Contains(strings.Join(args, " "), "--permission-prompt-tool "+claude.PermissionPromptTool) {
		t.Fatalf("the adapter did not pass the flag: %v", args)
	}
	code, _, stderr := startOnly(t, exe, dir, append(args, "--no-session-persistence")...)
	if code != 0 || strings.Contains(stderr, "unknown option") || strings.Contains(stderr, "Unknown --") {
		t.Fatalf("claude refused the worker argv with --permission-prompt-tool (exit %d): %s", code, tail(stderr))
	}
	// control: a misspelled flag is refused, so the check above can see a refusal
	if code, _, stderr := startOnly(t, exe, dir, streamArgs("--permission-prompt-tools", claude.PermissionPromptTool)...); code == 0 {
		t.Logf("%sa misspelled --permission-prompt-tools was accepted (stderr %q): this check may not see a refused flag", contract.NotePrefix, tail(stderr))
	}
}

// CLAUDE-PERMPROMPT-01 (paid): started as Drydock starts a worker outside a sandbox (acceptEdits, the gateway's config, and
// --permission-prompt-tool mcp__drydock__permission_prompt), Claude Code asks the permission tool before a Bash command its
// rules do not cover, with {"tool_name","input","tool_use_id"}; an answer {"behavior":"allow","updatedInput":...} runs the
// command, and {"behavior":"deny","message":...} refuses it and hands the message to the model. These details were read from
// the CLI's code (2.1.296) and the Agent SDK's types, not observed on a live turn: this check is that observation.
func TestClaudeAsksThePermissionPromptTool(t *testing.T) {
	exe, maxUSD := paidClaude(t, 0.03)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	allowWord, denyMarker := randomWord("ALLOWED"), randomWord("REFUSED-BY-DRYDOCK")
	log := filepath.Join(t.TempDir(), "requests.jsonl")
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	writeFile(t, cfg, fmt.Sprintf(`{"mcpServers":{"drydock":{"type":"stdio","command":%q,"args":[],"env":{%q:"perm","DRYDOCK_CONTRACT_PERM_LOG":%q,"DRYDOCK_CONTRACT_PERM_ALLOW":%q,"DRYDOCK_CONTRACT_PERM_DENY":%q}}}}`,
		self, mcpServerEnv, log, allowWord, denyMarker))
	spec := agents.RunSpec{Model: "haiku", PermissionMode: "acceptEdits", MCPConfigPath: cfg, PermissionPrompts: true,
		PermissionPromptTool: claude.PermissionPromptTool, AllowedTools: []string{"mcp__drydock__*"}}
	args := append(drydockClaude().Args(spec), "--no-session-persistence", "--max-budget-usd", usd(maxUSD))
	if !strings.Contains(strings.Join(args, " "), "--permission-prompt-tool "+claude.PermissionPromptTool) {
		t.Fatalf("the adapter did not pass the flag: %v", args)
	}
	p := startClaude(t, "CLAUDE-PERMPROMPT-01", exe, dir, childEnv(), args...)
	tr := p.turn("Use the Bash tool twice, one command per call, in this order: `echo " + allowWord + " > allowed.txt` and then " +
		"`echo other > refused.txt`. Run both even if one is refused. Do not use any other tool. Then reply DONE.")
	p.close()

	raw, _ := os.ReadFile(log)
	var reqs []event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e event
		if json.Unmarshal([]byte(line), &e) == nil {
			reqs = append(reqs, e)
		}
	}
	if len(reqs) == 0 {
		t.Fatalf("Claude Code never called the permission prompt tool (uses %v, reply %q, stderr %s)", tr.uses, tr.text, tail(p.stderr.String()))
	}
	ids := map[string]bool{}
	for _, u := range tr.uses {
		ids[u.ID] = true
	}
	for _, r := range reqs {
		if r.str("tool_name") != "Bash" || r.obj("input").str("command") == "" {
			t.Errorf("a request without tool_name Bash and input.command: %v (keys %v)", r, keys(r))
		}
		if id := r.str("tool_use_id"); id == "" || !ids[id] {
			t.Logf("%stool_use_id %q is not one of the turn's tool uses %v", contract.NotePrefix, id, ids)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "allowed.txt")); err != nil || !strings.Contains(string(b), allowWord) {
		t.Errorf("the allowed command did not run (allow with updatedInput): %v %q", err, b)
	}
	if exists(filepath.Join(dir, "refused.txt")) {
		t.Error("the refused command ran")
	}
	var told bool
	for _, e := range tr.events {
		if e.str("type") == "user" {
			b, _ := json.Marshal(e)
			told = told || strings.Contains(string(b), denyMarker)
		}
	}
	if !told {
		t.Errorf("the refusal's message did not reach the model as the tool result")
	}
	t.Logf("%s%d permission requests; denials %v", contract.NotePrefix, len(reqs), tr.result["permission_denials"])
}
