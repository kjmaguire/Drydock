package acp_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/workflow"
)

func permScript(opts ...fakeacp.PermOption) fakeacp.Script {
	return fakeacp.Script{Turns: [][]fakeacp.Step{{{Perm: &fakeacp.Perm{Tool: fakeacp.Tool{ID: "p1", Title: "Write config.json", Kind: "edit", Input: map[string]any{"path": "config.json"}}, Options: opts}}}}}
}

func TestPermissionRequestIsSurfacedAndDefaultRejects(t *testing.T) {
	ad := fakeAdapter(t, permScript())
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	cs := turn(t, s, "edit it")
	pr := ofKind(cs, workflow.ChunkPermission)
	if len(pr) != 1 {
		t.Fatalf("permission chunks: %v", kinds(cs))
	}
	req := decode[acp.PermissionRequest](t, pr[0])
	if req.ID == "" || req.ToolCallID != "p1" || req.Tool != "Write config.json" || req.Kind != "edit" || !strings.Contains(string(req.Args), "config.json") || len(req.Options) != 2 {
		t.Fatalf("request %+v", req)
	}
	// the tool_call precedes its permission request
	var callIdx, permIdx = -1, -1
	for i, c := range cs {
		if c.Kind == workflow.ChunkToolCall {
			callIdx = i
		}
		if c.Kind == workflow.ChunkPermission {
			permIdx = i
		}
	}
	if callIdx < 0 || callIdx > permIdx {
		t.Fatalf("tool_call must precede permission_request: %v", kinds(cs))
	}
	if !strings.Contains(text(cs), "permission: selected reject") {
		t.Fatalf("default must reject once, got %q", text(cs))
	}
	if stopReason(t, cs) != "end_turn" {
		t.Fatal(stopReason(t, cs))
	}
}

func TestDefaultNeverApproves(t *testing.T) {
	// only allow options offered: the default handler cancels rather than approving
	ad := fakeAdapter(t, permScript(fakeacp.PermOption{ID: "yes", Name: "Yes", Kind: acp.KindAllowOnce}, fakeacp.PermOption{ID: "always", Name: "Always", Kind: acp.KindAllowAlways}))
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "go")
	if !strings.Contains(text(cs), "permission: cancelled") {
		t.Fatalf("got %q", text(cs))
	}
	// reject_always is used when there is no reject_once
	ad = fakeAdapter(t, permScript(fakeacp.PermOption{ID: "yes", Name: "Yes", Kind: acp.KindAllowOnce}, fakeacp.PermOption{ID: "never", Name: "Never", Kind: acp.KindRejectAlways}))
	cs = turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "go")
	if !strings.Contains(text(cs), "permission: selected never") {
		t.Fatalf("got %q", text(cs))
	}
	if acp.RejectOnce(context.Background(), acp.PermissionRequest{}) != "" {
		t.Fatal("no options must cancel")
	}
}

func TestPluggableHandlerDecides(t *testing.T) {
	var got atomic.Value
	h := func(_ context.Context, req acp.PermissionRequest) string {
		got.Store(req)
		return "allow"
	}
	ad := fakeAdapter(t, permScript(), func(c *acp.Config) { c.Permission = h })
	cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "go")
	if !strings.Contains(text(cs), "permission: selected allow") {
		t.Fatalf("got %q", text(cs))
	}
	if r, _ := got.Load().(acp.PermissionRequest); r.Tool != "Write config.json" {
		t.Fatalf("handler saw %+v", r)
	}
}

func TestUnknownOptionIdIsNotAnApproval(t *testing.T) {
	for _, id := range []string{"", "allow-everything", "ALLOW"} {
		ad := fakeAdapter(t, permScript(), func(c *acp.Config) {
			c.Permission = func(context.Context, acp.PermissionRequest) string { return id }
		})
		cs := turn(t, startSession(t, ad, agents.RunSpec{AgentID: "a"}), "go")
		if !strings.Contains(text(cs), "permission: cancelled") {
			t.Fatalf("id %q: got %q", id, text(cs))
		}
	}
}

func TestInterruptCancelsAPendingPermissionRequest(t *testing.T) {
	entered := make(chan struct{})
	ctxDone := make(chan struct{})
	h := func(ctx context.Context, _ acp.PermissionRequest) string {
		close(entered)
		<-ctx.Done()
		close(ctxDone)
		return "allow" // too late: must be discarded
	}
	ad := fakeAdapter(t, permScript(), func(c *acp.Config) { c.Permission = h })
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	_ = s.Send(context.Background(), "go")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never called")
	}
	if err := s.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctxDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler context was not cancelled by Interrupt")
	}
	cs := readUntilDone(t, s, 10*time.Second)
	if strings.Contains(text(cs), "selected allow") || !strings.Contains(text(cs), "permission: cancelled") {
		t.Fatalf("a late answer must be discarded: %q", text(cs))
	}
}

func TestStopCancelsAPendingPermissionRequest(t *testing.T) {
	entered := make(chan struct{})
	ctxDone := make(chan struct{})
	ad := fakeAdapter(t, permScript(), func(c *acp.Config) {
		c.Permission = func(ctx context.Context, _ acp.PermissionRequest) string {
			close(entered)
			<-ctx.Done()
			close(ctxDone)
			return ""
		}
	})
	s := startSession(t, ad, agents.RunSpec{AgentID: "a"})
	_ = s.Send(context.Background(), "go")
	<-entered
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctxDone:
	case <-time.After(5 * time.Second):
		t.Fatal("handler context not cancelled by Stop")
	}
}
