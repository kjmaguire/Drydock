package acp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// These tests run the adapter against the Go SDK's own example agent (github.com/coder/acp-go-sdk/example/agent), built into
// a temp dir and started as a real process. It is a real ACP agent with no model behind it: it streams text, makes two tool
// calls and asks for permission once. It advertises no capabilities and reports no usage.

func exampleAdapter(t *testing.T, mut ...func(*acp.Config)) *acp.Adapter {
	cfg := acp.Config{ID: "sdk-example", Command: exampleAgent(t)}
	for _, m := range mut {
		m(&cfg)
	}
	return acp.New(cfg)
}

func TestSDKExampleAgentTurnWithDefaultRejectingPermission(t *testing.T) {
	t.Parallel()
	ad := exampleAdapter(t)
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"}).(*acp.Session)
	info := s.Info()
	if info.LoadSession || info.Resume || info.MCPHTTP || info.Image || len(info.AuthMethods) != 0 {
		t.Fatalf("the example agent advertises nothing: %+v", info)
	}
	if s.ID() == "" || !strings.HasPrefix(s.ID(), "sess_") {
		t.Fatalf("session id %q", s.ID())
	}
	cs := turn(t, s, "please improve the project")
	if stopReason(t, cs) != "end_turn" {
		t.Fatalf("stop %q", stopReason(t, cs))
	}
	if !strings.Contains(text(cs), "ACP Go Example Agent") {
		t.Fatalf("text %q", text(cs))
	}
	calls := ofKind(cs, workflow.ChunkToolCall)
	results := ofKind(cs, workflow.ChunkToolResult)
	if len(calls) != 2 || len(results) != 1 {
		// the second call (call_2) is rejected: the example agent never completes it after a rejection
		t.Fatalf("calls=%d results=%d %v", len(calls), len(results), kinds(cs))
	}
	c1 := decode[workflow.ToolCall](t, calls[0])
	if c1.ID != "call_1" || c1.Name != "Reading project files" || !strings.Contains(string(c1.Args), "/project/README.md") {
		t.Fatalf("call 1 %+v args=%s", c1, c1.Args)
	}
	r1 := decode[workflow.ToolResult](t, results[0])
	if r1.ID != "call_1" || r1.Name != "Reading project files" || !strings.Contains(r1.Output, "My Project") || r1.IsError || !strings.Contains(string(r1.Args), "README.md") {
		t.Fatalf("result 1 %+v", r1)
	}
	pr := ofKind(cs, workflow.ChunkPermission)
	if len(pr) != 1 {
		t.Fatalf("permission chunks %d", len(pr))
	}
	req := decode[acp.PermissionRequest](t, pr[0])
	if req.ToolCallID != "call_2" || req.Kind != "edit" || len(req.Options) != 2 || !strings.Contains(string(req.Args), "config.json") {
		t.Fatalf("request %+v", req)
	}
	if !strings.Contains(text(cs), "prefer not to make that change") || strings.Contains(text(cs), "successfully updated") {
		t.Fatalf("the default must reject: %q", text(cs))
	}
	if n := len(ofKind(cs, workflow.ChunkUsage)); n != 0 {
		t.Fatalf("the example agent supplies no usage; got %d usage chunks", n)
	}
	if len(ofKind(cs, workflow.ChunkMessageEnd)) < 2 {
		t.Fatalf("message_end per assistant message expected: %v", kinds(cs))
	}
	caps := s.Capabilities()
	if caps.TokenUsage || caps.CostReporting || caps.SessionResume {
		t.Fatalf("capabilities claim more than the agent reported: %+v", caps)
	}
}

func TestSDKExampleAgentTurnWithApproval(t *testing.T) {
	t.Parallel()
	ad := exampleAdapter(t, func(c *acp.Config) { c.Permission = acp.AllowOnce })
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	cs := turn(t, s, "please improve the project")
	if !strings.Contains(text(cs), "successfully updated the configuration") {
		t.Fatalf("approval not delivered: %q", text(cs))
	}
	results := ofKind(cs, workflow.ChunkToolResult)
	if len(results) != 2 {
		t.Fatalf("results %d %v", len(results), kinds(cs))
	}
	r2 := decode[workflow.ToolResult](t, results[1])
	if r2.ID != "call_2" || r2.IsError || !strings.Contains(r2.Output, "Configuration updated") {
		t.Fatalf("result 2 %+v", r2)
	}
}

func TestSDKExampleAgentInterrupt(t *testing.T) {
	t.Parallel()
	s := startSession(t, exampleAdapter(t), agents.RunSpec{AgentID: "a"})
	if err := s.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Output():
	case <-time.After(15 * time.Second):
		t.Fatal("no output")
	}
	if err := s.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs := readUntilDone(t, s, 15*time.Second)
	if stopReason(t, cs) != "cancelled" {
		t.Fatalf("stop %q: %v", stopReason(t, cs), kinds(cs))
	}
	if !alive(s) {
		t.Fatal("process ended with the turn")
	}
	// the session is reusable
	if err := s.Send(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-s.Output():
		if c.Kind != workflow.ChunkText {
			t.Fatalf("first chunk of the next turn: %s", c.Kind)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no output after interrupt")
	}
}

func TestSDKExampleAgentCannotResume(t *testing.T) {
	t.Parallel()
	ad := exampleAdapter(t)
	_, err := ad.Start(context.Background(), proc.New(), agents.RunSpec{AgentID: "a", Cwd: t.TempDir(), ResumeID: "old"})
	if err == nil || !strings.Contains(err.Error(), "cannot be resumed") {
		t.Fatalf("err %v", err)
	}
}
