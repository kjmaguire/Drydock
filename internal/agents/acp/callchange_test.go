package acp_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// An ACP agent runs a call Drydock allowed by itself, so what it reports of the call afterwards is compared with what was
// allowed (decision P117): the same call goes on untouched, and a call whose input changed after the allow is said to the
// person, reported to the session's OnCallChanged with both hashes, and its turn is cancelled (the stand-in blocks until it
// is, so a missing cancel fails the test by timing out).
func TestACallChangedAfterItWasAllowedStopsTheTurn(t *testing.T) {
	allowed := fakeacp.Tool{ID: "p1", Title: "Write config.json", Kind: "edit", Input: map[string]any{"path": "config.json", "content": "a"}, Locations: []string{"config.json"}}
	ask := func(context.Context, agents.ToolAsk) (bool, string) { return true, "" }

	// unchanged: the update repeats the allowed input, and the turn ends normally
	same := fakeacp.Script{Turns: [][]fakeacp.Step{{{Perm: &fakeacp.Perm{Tool: allowed, ThenInput: map[string]any{"content": "a", "path": "config.json"}}}, {Say: "done"}}}}
	var changes []string
	var mu sync.Mutex
	spec := agents.RunSpec{AgentID: "a", AskPermission: ask, OnCallChanged: func(q agents.ToolAsk, was, now string) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, q.ToolCallID+" "+was+" "+now+" "+string(q.Args))
	}}
	cs := turn(t, startSession(t, fakeAdapter(t, same), spec), "go")
	if stopReason(t, cs) != "end_turn" || len(ofKind(cs, workflow.ChunkError)) != 0 || len(changes) != 0 {
		t.Fatalf("an unchanged call: %v %q, changes %v", kinds(cs), stopReason(t, cs), changes)
	}

	// changed: the agent reports another content for the call it was allowed to make
	changed := fakeacp.Script{Turns: [][]fakeacp.Step{{{Perm: &fakeacp.Perm{Tool: allowed, ThenInput: map[string]any{"path": "config.json", "content": "rm -rf /"}}}, {Block: true}}}}
	cs = turn(t, startSession(t, fakeAdapter(t, changed), spec), "go")
	if stopReason(t, cs) != "cancelled" {
		t.Fatalf("the turn of a changed call: %v %q", kinds(cs), stopReason(t, cs))
	}
	denied := ofKind(cs, workflow.ChunkPermissionDenied)
	if len(denied) != 1 {
		t.Fatalf("one permission_denied: %v", kinds(cs))
	}
	d := decode[workflow.PermissionDenial](t, denied[0])
	if d.ToolUseID != "p1" || !strings.Contains(d.Reason, "changed this call after Drydock allowed it") || !strings.Contains(string(d.Input), "rm -rf") {
		t.Fatalf("denial %+v", d)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 1 || !strings.HasPrefix(changes[0], "p1 ") || !strings.Contains(changes[0], "rm -rf") {
		t.Fatalf("OnCallChanged %v", changes)
	}
	if f := strings.Fields(changes[0]); len(f) < 3 || f[1] == f[2] || len(f[1]) != 64 {
		t.Fatalf("the two hashes %v", f)
	}
}

// The SDK runs an agent's permission request beside its notification queue, so the request can be handled before the
// tool_call the agent sent ahead of it; the SDK's example agent also names another path in its request than in its tool_call.
// That tool_call is the call's announcement, not a change after the allow (CI 2026-10-29: TestSDKExampleAgentTurnWithApproval
// was cancelled about one turn in three); a change reported after it is still caught.
func TestAToolCallHandledAfterItsPermissionRequestIsNoChange(t *testing.T) {
	tool := fakeacp.Tool{ID: "p1", Title: "Write config.json", Kind: "edit", Input: map[string]any{"path": "/project/config.json", "content": "a"}, Locations: []string{"/project/config.json"}}
	asked := map[string]any{"path": "/home/user/project/config.json", "content": "a"}
	ask := func(context.Context, agents.ToolAsk) (bool, string) { return true, "" }
	var changes []string
	var mu sync.Mutex
	spec := agents.RunSpec{AgentID: "a", AskPermission: ask, OnCallChanged: func(q agents.ToolAsk, _, _ string) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, q.ToolCallID)
	}}

	late := fakeacp.Script{Turns: [][]fakeacp.Step{{{Perm: &fakeacp.Perm{Tool: tool, AskInput: asked, LateAnnounce: true}}, {Say: "done"}}}}
	cs := turn(t, startSession(t, fakeAdapter(t, late), spec), "go")
	if stopReason(t, cs) != "end_turn" || len(ofKind(cs, workflow.ChunkPermissionDenied)) != 0 || !strings.Contains(text(cs), "done") {
		t.Fatalf("a late announcement: %v %q %q", kinds(cs), stopReason(t, cs), text(cs))
	}
	c := decode[workflow.ToolCall](t, ofKind(cs, workflow.ChunkToolCall)[0])
	if !strings.Contains(string(c.Args), "/home/user/project/config.json") {
		t.Fatalf("the call shows what the person was asked about: %s", c.Args)
	}
	mu.Lock()
	if len(changes) != 0 {
		t.Fatalf("OnCallChanged %v", changes)
	}
	mu.Unlock()

	changed := fakeacp.Script{Turns: [][]fakeacp.Step{{{Perm: &fakeacp.Perm{Tool: tool, AskInput: asked, LateAnnounce: true,
		ThenInput: map[string]any{"path": "/home/user/project/config.json", "content": "rm -rf /"}}}, {Block: true}}}}
	cs = turn(t, startSession(t, fakeAdapter(t, changed), spec), "go")
	if stopReason(t, cs) != "cancelled" || len(ofKind(cs, workflow.ChunkPermissionDenied)) != 1 {
		t.Fatalf("a change after a late announcement: %v %q", kinds(cs), stopReason(t, cs))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 1 {
		t.Fatalf("OnCallChanged %v", changes)
	}
}
