package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
)

// A sandboxed ACP worker's permission requests (decision P131): what its CLI would run itself is refused with where to run it;
// reads and edits inside the worktree are allowed, writes into .git are not; a path outside, a link that leads out and any other
// kind of call are refused. Nobody is asked.
func TestASandboxedACPWorkersRequestsAreAnsweredByTheSandboxRules(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "wt")
	outside := filepath.Join(base, "secret.txt")
	for _, d := range []string{filepath.Join(root, "src"), filepath.Join(root, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "src", "key")
	linked := os.Symlink(outside, link) == nil
	ask := sandboxAsker(root)
	cases := []struct {
		q     agents.ToolAsk
		allow bool
		why   string
	}{
		{agents.ToolAsk{Kind: "execute", Tool: "Shell", Args: []byte(`{"command":"go test ./..."}`)}, false, "sandbox_run"},
		{agents.ToolAsk{Kind: "read", Locations: []string{"src/main.go"}}, true, ""},
		{agents.ToolAsk{Kind: "search", Locations: []string{filepath.Join(root, "src")}}, true, ""},
		{agents.ToolAsk{Kind: "edit", Locations: []string{"src/new/file.go"}}, true, ""},
		{agents.ToolAsk{Kind: "move", Locations: []string{"src/a.go", "src/b.go"}}, true, ""},
		{agents.ToolAsk{Kind: "read", Locations: []string{".git/config"}}, true, ""},
		{agents.ToolAsk{Kind: "edit", Locations: []string{".git/config"}}, false, ".git is Drydock's"},
		{agents.ToolAsk{Kind: "delete", Locations: []string{".git"}}, false, ".git is Drydock's"},
		{agents.ToolAsk{Kind: "read", Locations: []string{outside}}, false, "outside the working folder"},
		{agents.ToolAsk{Kind: "edit", Locations: []string{"../secret.txt"}}, false, "outside the working folder"},
		{agents.ToolAsk{Kind: "move", Locations: []string{"src/a.go", "../b.go"}}, false, "outside the working folder"},
		{agents.ToolAsk{Kind: "edit"}, false, "names no path"},
		{agents.ToolAsk{Kind: "fetch", Tool: "Fetch"}, false, "only reads and edits"},
		{agents.ToolAsk{Kind: "other", Tool: "Something"}, false, "only reads and edits"},
	}
	if linked {
		cases = append(cases, struct {
			q     agents.ToolAsk
			allow bool
			why   string
		}{agents.ToolAsk{Kind: "read", Locations: []string{"src/key"}}, false, "outside the working folder"})
	}
	for _, c := range cases {
		ok, msg := ask(context.Background(), c.q)
		if ok != c.allow || !strings.Contains(msg, c.why) {
			t.Errorf("%s %v: allow=%v %q", c.q.Kind, c.q.Locations, ok, msg)
		}
	}
}
