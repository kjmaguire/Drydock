package security

import (
	"encoding/json"
	"errors"
	"testing"
)

// P88: only Claude's prompt tool can run a changed call, and a change must be a JSON object; the edit is held canonical and
// taken once.
func TestAnEditIsHeldOnlyForAClaudeCallAndOnlyOnce(t *testing.T) {
	g := &PermissionGate{}
	for _, tool := range []string{"codex:Bash", "gemini:edit", "Bash", ""} {
		if err := g.SetEdit("a1", tool, json.RawMessage(`{"command":"ls"}`)); !errors.Is(err, ErrEditNotPossible) {
			t.Fatalf("%q: %v", tool, err)
		}
	}
	for _, in := range []string{`"ls"`, `[1]`, `null`, `{bad`, ``} {
		if err := g.SetEdit("a1", "claude:Bash", json.RawMessage(in)); !errors.Is(err, ErrEditNotPossible) {
			t.Fatalf("%q: %v", in, err)
		}
	}
	if err := g.SetEdit("a1", "claude:Bash", json.RawMessage(`{ "description":"x", "command":"ls" }`)); err != nil {
		t.Fatal(err)
	}
	got, ok := g.takeEdit("a1")
	if !ok || string(got) != `{"command":"ls","description":"x"}` {
		t.Fatalf("%v %s", ok, got)
	}
	if _, ok := g.takeEdit("a1"); ok {
		t.Fatal("an edit is taken once")
	}
	_ = g.SetEdit("a2", "claude:Write", json.RawMessage(`{"file_path":"a"}`))
	g.ClearEdit("a2")
	if _, ok := g.takeEdit("a2"); ok {
		t.Fatal("a cleared edit is gone")
	}
}
