package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/security"
)

// scriptedPermits is a labelled test double for the core's permission service: it answers from a fixed table and can hold a
// request until released or its context ends.
type scriptedPermits struct {
	mu      sync.Mutex
	seen    []PermissionRequest
	answer  PermissionAnswer
	hold    chan struct{}
	waiting chan struct{}
}

func (s *scriptedPermits) Permit(ctx context.Context, req PermissionRequest) PermissionAnswer {
	s.mu.Lock()
	s.seen = append(s.seen, req)
	s.mu.Unlock()
	if s.hold != nil {
		if s.waiting != nil {
			close(s.waiting)
		}
		select {
		case <-s.hold:
		case <-ctx.Done():
			return PermissionAnswer{Outcome: "cancelled", Message: "The run ended before anyone answered."}
		}
	}
	return s.answer
}

type permAnswer struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput"`
	Message      string          `json:"message"`
}

// parseAnswer checks the shape Claude Code requires (a single text block holding the JSON answer) and decodes it.
func parseAnswer(t *testing.T, res *sdk.CallToolResult) permAnswer {
	t.Helper()
	if res.IsError || len(res.Content) != 1 {
		t.Fatalf("the answer must be one text block and not an error: %+v", res)
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("not a text block: %T", res.Content[0])
	}
	var a permAnswer
	if err := json.Unmarshal([]byte(tc.Text), &a); err != nil {
		t.Fatalf("the text is not the JSON answer the CLI parses: %q", tc.Text)
	}
	return a
}

func TestPermissionPromptIsOfferedOnlyWhenThePolicyAllowsIt(t *testing.T) {
	p := &scriptedPermits{answer: PermissionAnswer{Allow: true, Outcome: "approved"}}
	e := newEnv(t, func(o *Options) { o.Permissions = p })
	without := e.connect(e.startRun(RunConfig{Info: RunInfo{RunID: "r-plain"}, Policy: allow(map[string][]string{WorkspaceServerID: {"read_range"}})}))
	if contains(toolNames(t, without), ToolPermissionPrompt) {
		t.Fatal("a run whose policy does not name the tool must not see it")
	}
	res := callTool(t, without, ToolPermissionPrompt, map[string]any{"tool_name": "Bash", "input": map[string]any{"command": "ls"}})
	if !res.IsError || !strings.Contains(textOf(res), "denied") || len(p.seen) != 0 {
		t.Fatalf("the tool answered a run that was not given it: %s", textOf(res))
	}
	glob := e.connect(e.startRun(RunConfig{Info: RunInfo{RunID: "r-glob"}, Policy: allow(map[string][]string{WorkspaceServerID: {"*"}})}))
	if contains(toolNames(t, glob), ToolPermissionPrompt) {
		t.Fatal("a glob must not hand a run the permission tool; only an entry naming it does")
	}
	with := e.connect(e.startRun(RunConfig{Info: RunInfo{RunID: "r-worker"}, Policy: allow(map[string][]string{WorkspaceServerID: {"read_range", ToolPermissionPrompt}})}))
	if !contains(toolNames(t, with), ToolPermissionPrompt) {
		t.Fatal("the worker run must see the permission tool")
	}
}

// The CLI's request reaches the permission service exactly, an allow sends back the exact input as updatedInput, a refusal
// carries the service's message, and every answer is audited without the input.
func TestPermissionPromptAnswersInTheShapeTheCLIReads(t *testing.T) {
	p := &scriptedPermits{answer: PermissionAnswer{Allow: true, Outcome: "approved"}}
	e := newEnv(t, func(o *Options) { o.Permissions = p })
	cs := e.connect(e.startRun(RunConfig{Info: RunInfo{RunID: "r1", TaskID: "task_1", AgentID: "agt_1", Worktree: "/w"},
		Policy: allow(map[string][]string{WorkspaceServerID: {ToolPermissionPrompt}})}))
	in := map[string]any{"command": "npm install left-pad", "description": "add a dependency"}
	a := parseAnswer(t, callTool(t, cs, ToolPermissionPrompt, map[string]any{"tool_name": "Bash", "input": in, "tool_use_id": "toolu_1"}))
	var got map[string]any
	_ = json.Unmarshal(a.UpdatedInput, &got)
	if a.Behavior != "allow" || got["command"] != in["command"] || got["description"] != in["description"] || len(got) != 2 {
		t.Fatalf("allow answer: %+v", a)
	}
	if len(p.seen) != 1 || p.seen[0].ToolName != "Bash" || p.seen[0].ToolUseID != "toolu_1" || p.seen[0].Run.TaskID != "task_1" || p.seen[0].Run.Worktree != "/w" {
		t.Fatalf("the request reached the service as %+v", p.seen)
	}
	var sent map[string]any
	if json.Unmarshal(p.seen[0].Input, &sent) != nil || sent["command"] != "npm install left-pad" {
		t.Fatalf("input %s", p.seen[0].Input)
	}

	p.answer = PermissionAnswer{Outcome: "rejected", Message: "A person refused this Bash call."}
	a = parseAnswer(t, callTool(t, cs, ToolPermissionPrompt, map[string]any{"tool_name": "Bash", "input": in}))
	if a.Behavior != "deny" || a.Message != "A person refused this Bash call." || len(a.UpdatedInput) != 0 {
		t.Fatalf("deny answer: %+v", a)
	}
	// malformed requests are refused without asking anyone
	for _, bad := range []map[string]any{{"input": in}, {"tool_name": "Bash"}, {"tool_name": "Bash", "input": "ls"}} {
		if a := parseAnswer(t, callTool(t, cs, ToolPermissionPrompt, bad)); a.Behavior != "deny" {
			t.Fatalf("%v was not refused: %+v", bad, a)
		}
	}
	if len(p.seen) != 2 {
		t.Fatalf("malformed requests reached the service: %d", len(p.seen))
	}
	var decisions []string
	for _, ev := range e.events(EventCall) {
		d := eventData(t, ev)
		if d["tool"] != ToolPermissionPrompt {
			continue
		}
		decisions = append(decisions, d["decision"].(string))
		raw, _ := json.Marshal(d)
		if strings.Contains(string(raw), "left-pad") {
			t.Fatalf("the audit event carries the input: %s", raw)
		}
	}
	if strings.Join(decisions, ",") != "allowed,denied,denied,denied,denied" {
		t.Fatalf("mcp.call decisions %v", decisions)
	}
}

// With no permission service, and when the run ends while a request waits, the answer is a refusal.
func TestPermissionPromptRefusesWithoutAServiceAndWhenTheRunEnds(t *testing.T) {
	e := newEnv(t, nil)
	pol := allow(map[string][]string{WorkspaceServerID: {ToolPermissionPrompt}})
	cs := e.connect(e.startRun(RunConfig{Info: RunInfo{RunID: "r-none"}, Policy: pol}))
	if a := parseAnswer(t, callTool(t, cs, ToolPermissionPrompt, map[string]any{"tool_name": "Bash", "input": map[string]any{"command": "ls"}})); a.Behavior != "deny" {
		t.Fatalf("no service: %+v", a)
	}

	p := &scriptedPermits{answer: PermissionAnswer{Allow: true}, hold: make(chan struct{}), waiting: make(chan struct{})}
	e2 := newEnv(t, func(o *Options) { o.Permissions = p })
	t.Cleanup(func() { close(p.hold) }) // runs before the gateway closes, so a failing check cannot hang the test
	cs2 := e2.connect(e2.startRun(RunConfig{Info: RunInfo{RunID: "r-wait"}, Policy: pol}))
	done := make(chan *sdk.CallToolResult, 1)
	go func() {
		res, err := cs2.CallTool(context.Background(), &sdk.CallToolParams{Name: ToolPermissionPrompt, Arguments: map[string]any{"tool_name": "WebFetch", "input": map[string]any{"url": "https://x"}}})
		if err != nil {
			res = &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}}}
		}
		done <- res
	}()
	select {
	case <-p.waiting:
	case <-time.After(10 * time.Second):
		t.Fatal("the request never reached the service")
	}
	go e2.g.EndRun("r-wait") // closing the sessions waits for calls in flight
	select {
	case res := <-done:
		// the session may be closed before the answer is written; either way nothing was allowed
		if !res.IsError {
			if a := parseAnswer(t, res); a.Behavior != "deny" {
				t.Fatalf("a closed run allowed the call: %+v", a)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting request did not end with the run")
	}
	var cancelled bool
	for _, ev := range e2.events(EventCall) {
		if d := eventData(t, ev); d["tool"] == ToolPermissionPrompt && d["decision"] == DecisionDenied && strings.Contains(d["reason"].(string), "cancelled") {
			cancelled = true
		}
	}
	if !cancelled {
		t.Fatal("the refused request must be audited as cancelled")
	}
}

// The CLI runs the tool with the input it is sent back. That is the canonical text the approval hashed and a person read, not the
// agent's own bytes: a duplicate key would otherwise let a call differ from the one approved (audit 2026-10-16, H8).
func TestPermissionPromptSendsBackTheCanonicalInputThatWasDecidedOn(t *testing.T) {
	p := &scriptedPermits{answer: PermissionAnswer{Allow: true, Outcome: "approved"}}
	e := newEnv(t, func(o *Options) { o.Permissions = p })
	cs := e.connect(e.startRun(RunConfig{Info: RunInfo{RunID: "r-canon", TaskID: "task_1", Worktree: "/w"},
		Policy: allow(map[string][]string{WorkspaceServerID: {ToolPermissionPrompt}})}))
	raw := json.RawMessage(`{"tool_name":"Write","input":{"file_path":"/safe/place","content":"x","file_path":"/etc/cron.d/evil"}}`)
	a := parseAnswer(t, callTool(t, cs, ToolPermissionPrompt, raw))
	want, err := security.CanonicalJSON(json.RawMessage(`{"file_path":"/etc/cron.d/evil","content":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if a.Behavior != "allow" || string(a.UpdatedInput) != string(want) {
		t.Fatalf("updatedInput %s, want %s", a.UpdatedInput, want)
	}
	if len(p.seen) != 1 || string(p.seen[0].Input) != string(want) {
		t.Fatalf("the service was asked about %s, want %s", p.seen[0].Input, want)
	}
}
