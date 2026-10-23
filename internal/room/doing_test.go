package room

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestADoingLineNamesTheCallAndItsArgument(t *testing.T) {
	long := "go test ./internal/... -run TestSomethingVeryLongIndeed -count=1 -timeout 20m -v -race"
	for _, c := range []struct {
		name string
		args any
		want string
	}{
		// Claude's tools
		{"Edit", map[string]any{"file_path": "/home/u/repo/internal/limiter.go", "old_string": "a", "new_string": "b"}, "Editing limiter.go"},
		{"MultiEdit", map[string]any{"file_path": `C:\Users\u\repo\limiter.go`}, "Editing limiter.go"},
		{"Write", map[string]any{"file_path": "docs/notes.md", "content": "secret stuff"}, "Writing notes.md"},
		{"Read", map[string]any{"file_path": "/r/parse.go"}, "Reading parse.go"},
		{"Bash", map[string]any{"command": "go test ./..."}, "Running go test ./..."},
		{"Bash", map[string]any{"command": long}, "Running " + long[:DoingCommandLen] + "..."},
		{"Grep", map[string]any{"pattern": "parseExp", "path": "internal"}, "Searching for parseExp"},
		{"Glob", map[string]any{"pattern": "**/*.go"}, "Searching for **/*.go"},
		{"LS", map[string]any{"path": "/r/internal/"}, "Listing internal"},
		{"TodoWrite", map[string]any{"todos": []any{}}, "Updating the plan"},
		{"WebFetch", map[string]any{"url": "https://user:pw@pkg.go.dev/net/http?x=1"}, "Reading pkg.go.dev"},
		{"WebSearch", map[string]any{"query": "sqlite json_each"}, "Searching the web for sqlite json_each"},
		{"Task", map[string]any{"description": "Find the parser", "prompt": "..."}, "Delegating: Find the parser"},
		{"mcp__drydock__read_file", map[string]any{"path": "a/b.go"}, "Reading b.go"},
		// Gemini's and OpenCode's names
		{"run_shell_command", map[string]any{"command": "npm test"}, "Running npm test"},
		{"replace", map[string]any{"file_path": "x/y.ts"}, "Editing y.ts"},
		{"search_file_content", map[string]any{"pattern": "TODO"}, "Searching for TODO"},
		{"list_directory", map[string]any{"dir_path": "src"}, "Listing src"},
		{"write_todos", nil, "Updating the plan"},
		{"edit", map[string]any{"filePath": "/r/a.go"}, "Editing a.go"},
		// an argv with its shell wrapper, as Codex sends it
		{"shell", map[string]any{"command": []any{"bash", "-lc", "cargo build"}}, "Running cargo build"},
		{"exec", map[string]any{"command": []any{"make", "check"}}, "Running make check"},
		// an ACP call named by its title, with a location or without anything to show
		{"Read parse.go", map[string]any{"kind": "read", "locations": []any{map[string]any{"path": "/r/parse.go"}}}, "Reading parse.go"},
		{"Run go vet", map[string]any{"kind": "execute"}, "Run go vet"},
		// nothing to show
		{"Edit", nil, "Editing files"},
		{"Bash", map[string]any{}, "Running a command"},
		{"frobnicate", map[string]any{"x": 1}, "Using frobnicate"},
		{"", nil, "Using a tool"},
	} {
		b, _ := json.Marshal(c.args)
		if got := DoingLine(c.name, b); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.name, b, got, c.want)
		}
	}
}

func TestPresenceKeepsWhatTheAgentIsDoingUntilTheTurnEnds(t *testing.T) {
	out := func(kind string, data any) eventT {
		return eventT{"agent.output", map[string]any{"kind": kind, "data": data}}
	}
	var s PresenceState
	steps := []struct {
		e    eventT
		want string
	}{
		{eventT{"agent.waiting", map[string]any{"state": "working"}}, ""},
		{out("thinking", map[string]any{"text": "hm"}), ""},
		{out("tool_call", map[string]any{"name": "Edit", "args": map[string]any{"file_path": "/r/limiter.go"}}), "Editing limiter.go"},
		{out("tool_result", map[string]any{"name": "Edit"}), "Editing limiter.go"},
		{out("text", map[string]any{"text": "now the tests"}), "Editing limiter.go"},
		{out("tool_call", map[string]any{"name": "Bash", "args": map[string]any{"command": "go test ./..."}}), "Running go test ./..."},
		{out("permission_request", map[string]any{}), "Running go test ./..."},
		{eventT{"agent.completed", map[string]any{"stopReason": "end_turn"}}, ""},
	}
	for i, st := range steps {
		next, ok := Derive(s, ev(st.e.typ, "a1", st.e.data))
		if !ok {
			t.Fatalf("step %d did not apply", i)
		}
		if next.Doing != st.want {
			t.Fatalf("step %d (%s): doing %q, want %q", i, st.e.typ, next.Doing, st.want)
		}
		s = next
	}
	// the old activity text is unchanged for the screens that read it
	s, _ = Derive(s, ev("agent.output", "a1", map[string]any{"kind": "tool_call", "data": map[string]any{"name": "Edit"}}))
	if s.Activity != "tool: Edit" || s.Doing != "Editing files" {
		t.Fatalf("%+v", s)
	}
}

type eventT struct {
	typ  string
	data any
}

// The arguments are scanned before the line keeps a part of them: a token the cut at DoingCommandLen splits is still found.
func TestTheTrackerRedactsTheArgumentsBeforeTheLineIsCut(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	tr := NewTracker(e.bus, TrackerOptions{Redact: func(s string) string { return strings.ReplaceAll(s, "hunter2hunter2", "[X]") }})
	if err := tr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cmd := "curl -u me:" + strings.Repeat("x", DoingCommandLen-16) + "hunter2hunter2 https://example.com"
	publish(t, e, "agent.output", "a1", map[string]any{"kind": "tool_call", "data": map[string]any{"name": "Bash", "args": map[string]any{"command": cmd}}})
	var got string
	eventually(t, 2*time.Second, func() bool {
		s, _ := tr.Get("a1")
		got = s.Doing
		return got != ""
	})
	if strings.Contains(got, "hunter") || !strings.Contains(got, "[X]") {
		t.Fatalf("doing %q", got)
	}
}
