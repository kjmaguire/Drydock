package fakeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMain lets this test binary act as a stdio MCP server with one permission_prompt tool (a LABELLED TEST DOUBLE of
// Drydock's gateway tool): it allows a Bash command "go test ./..." and refuses everything else, and appends each request to
// $DRYDOCK_FAKEAGENT_PERMSERVER_LOG.
func TestMain(m *testing.M) {
	if os.Getenv("DRYDOCK_FAKEAGENT_PERMSERVER") == "1" {
		os.Exit(permServer())
	}
	os.Exit(m.Run())
}

func permServer() int {
	srv := sdk.NewServer(&sdk.Implementation{Name: "perm-double", Version: "1"}, nil)
	type req struct {
		ToolName  string         `json:"tool_name"`
		Input     map[string]any `json:"input"`
		ToolUseID string         `json:"tool_use_id"`
	}
	sdk.AddTool(srv, &sdk.Tool{Name: "permission_prompt"}, func(_ context.Context, _ *sdk.CallToolRequest, r req) (*sdk.CallToolResult, any, error) {
		if f, err := os.OpenFile(os.Getenv("DRYDOCK_FAKEAGENT_PERMSERVER_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			b, _ := json.Marshal(r)
			fmt.Fprintf(f, "%s\n", b)
			f.Close()
		}
		ans := map[string]any{"behavior": "deny", "message": "refused by the double"}
		if r.ToolName == "Bash" && r.Input["command"] == "go test ./..." {
			ans = map[string]any{"behavior": "allow", "updatedInput": r.Input}
		}
		b, _ := json.Marshal(ans)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil, nil
	})
	if err := srv.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		return 1
	}
	return 0
}

// With --permission-prompt-tool the scripted agent asks the named MCP tool about a step its rules do not cover, as Claude Code
// does: an allow runs the step, a refusal becomes an error result with the tool's message and a permission denial. A step the
// allow rules cover is not asked about.
func TestAskedToolsGoToThePermissionPromptTool(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "requests.jsonl")
	cfg := filepath.Join(dir, "mcp.json")
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"drydock": map[string]any{"type": "stdio", "command": os.Args[0],
		"env": map[string]string{"DRYDOCK_FAKEAGENT_PERMSERVER": "1", "DRYDOCK_FAKEAGENT_PERMSERVER_LOG": log}}}})
	if err := os.WriteFile(cfg, b, 0o600); err != nil {
		t.Fatal(err)
	}
	sc, _ := json.Marshal(Script{Default: []Step{
		{Tool: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`), Result: "ok  pkg", Ask: true},
		{Tool: "Bash", Input: json.RawMessage(`{"command":"rm -rf /"}`), Result: "gone", Ask: true},
		{Tool: "Bash", Input: json.RawMessage(`{"command":"go vet ./..."}`), Result: "vetted", Ask: true},
		{Say: "done"},
	}})
	sp := filepath.Join(dir, "script.json")
	if err := os.WriteFile(sp, sc, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	var out bytes.Buffer
	Run([]string{"-p", "work", "--mcp-config", cfg, "--permission-prompt-tool", "mcp__drydock__permission_prompt", "--allowedTools", "Bash(go vet ./...)"}, strings.NewReader(""), &out)
	stream := out.String()
	for _, want := range []string{`"content":"ok  pkg","is_error":false`, `"content":"refused by the double","is_error":true`, `"content":"vetted","is_error":false`} {
		if !strings.Contains(stream, want) {
			t.Fatalf("stream lacks %s:\n%s", want, stream)
		}
	}
	if strings.Count(stream, `"tool_name":"Bash"`) != 1 || !strings.Contains(stream, `rm -rf /`) {
		t.Fatalf("only the refused call is a permission denial:\n%s", stream)
	}
	raw, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"tool_name":"Bash"`) || !strings.Contains(lines[0], `"tool_use_id":"toolu_fake_0001"`) || !strings.Contains(lines[1], "rm -rf /") {
		t.Fatalf("requests the permission tool saw:\n%s", raw)
	}

	// without the flag the agent refuses at once, as headless Claude Code does
	out.Reset()
	Run([]string{"-p", "work", "--mcp-config", cfg}, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "haven't granted it yet") {
		t.Fatalf("without --permission-prompt-tool:\n%s", out.String())
	}
}
