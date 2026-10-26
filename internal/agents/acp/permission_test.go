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
	// nobody could be asked, so Drydock refused: the refusal is on the record, after the request and before the call's failure
	// (AG-UI review 2026-10-25, "a refused ACP request leaves no trace")
	denied := ofKind(cs, workflow.ChunkPermissionDenied)
	if len(denied) != 1 {
		t.Fatalf("one permission_denied expected: %v", kinds(cs))
	}
	d := decode[workflow.PermissionDenial](t, denied[0])
	if d.ToolName != "Write config.json" || d.ToolUseID != "p1" || d.Reason != "nobody could be asked in this session, so Drydock refused the request" || !strings.Contains(string(d.Input), "config.json") {
		t.Fatalf("denial %+v", d)
	}
	deniedIdx, resultIdx := -1, -1
	for i, c := range cs {
		switch c.Kind {
		case workflow.ChunkPermissionDenied:
			deniedIdx = i
		case workflow.ChunkToolResult:
			resultIdx = i
		}
	}
	if !(permIdx < deniedIdx && deniedIdx < resultIdx) {
		t.Fatalf("permission_denied belongs between the request and the tool_result: %v", kinds(cs))
	}
}

// noDenial fails when the turn carries a permission_denied chunk: with an asker or an adapter handler, somebody was asked.
func noDenial(t *testing.T, cs []workflow.Chunk) {
	t.Helper()
	if len(ofKind(cs, workflow.ChunkPermissionDenied)) != 0 {
		t.Fatalf("somebody was asked, so Drydock refused nothing on its own: %v", kinds(cs))
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
	noDenial(t, cs)
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
	// the agent also cancels its own pending request when it handles session/cancel, so it may see either our
	// "cancelled" outcome or its own request-cancelled error; what must never happen is an approval
	if strings.Contains(text(cs), "selected allow") || !(strings.Contains(text(cs), "permission: cancelled") || strings.Contains(text(cs), "permission: error")) {
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

// A session's own asker (a worker turn, a chat) decides over the adapter's handler: it sees the call as the agent described it,
// an allow selects allow_once only (never allow_always), a refusal selects reject_once.
func TestTheSessionsAskerDecidesAndAllowsOnlyOnce(t *testing.T) {
	var got atomic.Value
	allow := atomic.Bool{}
	spec := agents.RunSpec{AgentID: "a", AskPermission: func(_ context.Context, q agents.ToolAsk) (bool, string) {
		got.Store(q)
		return allow.Load(), "no"
	}}
	adapterSaw := atomic.Bool{}
	opts := []fakeacp.PermOption{{ID: "always", Name: "Always", Kind: acp.KindAllowAlways}, {ID: "once", Name: "Once", Kind: acp.KindAllowOnce}, {ID: "no", Name: "No", Kind: acp.KindRejectOnce}}
	ad := fakeAdapter(t, permScript(opts...), func(c *acp.Config) {
		c.Permission = func(context.Context, acp.PermissionRequest) string { adapterSaw.Store(true); return "always" }
	})

	allow.Store(true)
	cs := turn(t, startSession(t, ad, spec), "go")
	if !strings.Contains(text(cs), "permission: selected once") {
		t.Fatalf("an allow selects allow_once: %q", text(cs))
	}
	q, _ := got.Load().(agents.ToolAsk)
	if q.ToolCallID != "p1" || q.Tool != "Write config.json" || q.Kind != "edit" || !strings.Contains(string(q.Args), "config.json") {
		t.Fatalf("the asker saw %+v", q)
	}
	if adapterSaw.Load() {
		t.Fatal("the adapter's handler must not decide when the session has its own asker")
	}
	noDenial(t, cs)

	allow.Store(false)
	cs = turn(t, startSession(t, ad, spec), "go")
	if !strings.Contains(text(cs), "permission: selected no") {
		t.Fatalf("a refusal selects reject_once: %q", text(cs))
	}
	noDenial(t, cs) // the person refused; that is the asker's record (the chat stores it), not Drydock's own refusal

	// an agent that offers only allow_always gets a cancelled request, not a standing approval
	ad = fakeAdapter(t, permScript(fakeacp.PermOption{ID: "always", Name: "Always", Kind: acp.KindAllowAlways}))
	allow.Store(true)
	cs = turn(t, startSession(t, ad, spec), "go")
	if !strings.Contains(text(cs), "permission: cancelled") {
		t.Fatalf("allow_always is never chosen for a person's one approval: %q", text(cs))
	}
}
