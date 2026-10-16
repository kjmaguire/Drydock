package core

import (
	"slices"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/mcp"
)

// Only a marked Claude worker turn outside a sandbox gets the permission tool: a sandboxed worker runs restricted with its
// commands in the container, a read-only turn's mode decides everything, and no other CLI has the mechanism.
func TestOfferPermissionPromptsOnlyToClaudeWorkersOutsideASandbox(t *testing.T) {
	for _, c := range []struct {
		name  string
		cfg   rt.Config
		offer bool
	}{
		{"worker", rt.Config{Adapter: "claude", Spec: agents.RunSpec{PermissionPrompts: true}}, true},
		{"sandboxed worker", rt.Config{Adapter: "claude", Spec: agents.RunSpec{PermissionPrompts: true, Sandboxed: true}}, false},
		{"read-only", rt.Config{Adapter: "claude", Spec: agents.RunSpec{PermissionPrompts: true, ReadOnly: true}}, false},
		{"not marked", rt.Config{Adapter: "claude"}, false},
		{"another CLI", rt.Config{Adapter: "codex", Spec: agents.RunSpec{PermissionPrompts: true}}, false},
	} {
		cfg := c.cfg
		pl := mcpPlan{Policy: mcp.RunPolicy{Allow: map[string][]string{mcp.WorkspaceServerID: {"read_range"}}}}
		offerPermissionPrompts(&cfg, &pl)
		got := slices.Contains(pl.Policy.Allow[mcp.WorkspaceServerID], mcp.ToolPermissionPrompt)
		if got != c.offer || (cfg.Spec.PermissionPromptTool != "") != c.offer || slices.Contains(pl.Tools, mcp.ToolPermissionPrompt) != c.offer {
			t.Errorf("%s: offered=%v tool=%q tools=%v", c.name, got, cfg.Spec.PermissionPromptTool, pl.Tools)
		}
	}
	// a timeout set earlier is kept
	cfg := rt.Config{Adapter: "claude", Spec: agents.RunSpec{PermissionPrompts: true, Env: []string{"MCP_TOOL_TIMEOUT=5"}}}
	pl := mcpPlan{Policy: mcp.RunPolicy{Allow: map[string][]string{}}}
	offerPermissionPrompts(&cfg, &pl)
	if !slices.Equal(cfg.Spec.Env, []string{"MCP_TOOL_TIMEOUT=5"}) {
		t.Fatalf("env %v", cfg.Spec.Env)
	}
}
