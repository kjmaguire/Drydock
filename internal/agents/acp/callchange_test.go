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
