package fakeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The scripted agent's side of Claude Code's --permission-prompt-tool (a TEST DOUBLE of the CLI's behaviour, read from Claude
// Code 2.1.296): a tool use the allow rules do not cover is not refused at once but asked of the named MCP tool, with
// {"tool_name", "input", "tool_use_id"}, through the server of the same name in --mcp-config. The answer is one text block of
// JSON: {"behavior":"allow","updatedInput":{...}} runs the tool with updatedInput, {"behavior":"deny","message":"..."} refuses
// it with the message. Anything else is a refusal, as the real CLI treats an invalid answer.

// permissionAnswer is what the permission tool decided.
type permissionAnswer struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput"`
	Message      string          `json:"message"`
}

// askPermission calls the permission prompt tool and returns its answer. ok is false when the answer is a refusal.
func (a *agent) askPermission(tool string, input json.RawMessage, toolUseID string) (permissionAnswer, bool) {
	ans, err := callPermissionTool(a.permTool, a.mcpConfig, tool, input, toolUseID)
	if path := os.Getenv("DRYDOCK_FAKE_PERMISSION_LOG"); path != "" {
		if f, ferr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); ferr == nil {
			rec := map[string]any{"tool": tool, "behavior": ans.Behavior, "message": ans.Message, "updatedInput": ans.UpdatedInput}
			if err != nil {
				rec["error"] = err.Error()
			}
			b, _ := json.Marshal(rec)
			fmt.Fprintf(f, "%s\n", b)
			f.Close()
		}
	}
	if err != nil {
		return permissionAnswer{Behavior: "deny", Message: "The permission prompt tool failed: " + err.Error()}, false
	}
	return ans, ans.Behavior == "allow"
}

func callPermissionTool(name, configPath, tool string, input json.RawMessage, toolUseID string) (permissionAnswer, error) {
	server, bare, ok := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
	if !strings.HasPrefix(name, "mcp__") || !ok {
		return permissionAnswer{}, fmt.Errorf("tool %s (passed via --permission-prompt-tool) must be an MCP tool", name)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return permissionAnswer{}, err
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return permissionAnswer{}, err
	}
	entry, ok := cfg.MCPServers[server]
	if !ok || entry.Command == "" {
		return permissionAnswer{}, fmt.Errorf("MCP tool %s (passed via --permission-prompt-tool) not found", name)
	}
	cmd := exec.Command(entry.Command, entry.Args...)
	cmd.Env = os.Environ()
	for k, v := range entry.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := sdk.NewClient(&sdk.Implementation{Name: "fakeagent", Version: "1"}, nil)
	sess, err := c.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return permissionAnswer{}, err
	}
	defer func() { _ = sess.Close() }()
	var in any
	if err := json.Unmarshal(input, &in); err != nil {
		return permissionAnswer{}, err
	}
	res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: bare, Arguments: map[string]any{"tool_name": tool, "input": in, "tool_use_id": toolUseID}})
	if err != nil {
		return permissionAnswer{}, err
	}
	if len(res.Content) != 1 {
		return permissionAnswer{}, errors.New("the permission prompt tool must answer with a single text block")
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		return permissionAnswer{}, errors.New("the permission prompt tool must answer with a single text block")
	}
	var ans permissionAnswer
	if err := json.Unmarshal([]byte(tc.Text), &ans); err != nil || (ans.Behavior != "allow" && ans.Behavior != "deny") {
		return permissionAnswer{}, fmt.Errorf("the permission prompt tool returned an invalid permission result: %q", tc.Text)
	}
	return ans, nil
}
