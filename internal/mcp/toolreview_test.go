package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// liveDigests lists the real upstream's tools the way a person's "list the tools" does and fingerprints each.
func liveDigests(t *testing.T, url string) map[string]string {
	t.Helper()
	c := sdk.NewClient(&sdk.Implementation{Name: "reviewer", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cs, err := c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: url, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	out := map[string]string{}
	for tl, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		out[tl.Name] = ToolDigest(tl.Name, tl.Description, tl.InputSchema)
	}
	if len(out) < 5 {
		t.Fatalf("the test server lists %d tools", len(out))
	}
	return out
}

// Audit 2026-10-16, M2: what the model reads of a tool is the server's text. A run keeps from the model a tool whose text changed
// since a person reviewed it, a tool the review marked, and a tool that was not in the reviewed list, unless the grant names it.
func TestARunWithholdsToolsThatAreNotWhatAPersonReviewed(t *testing.T) {
	ts, _ := startHTTPUpstream(t, "", nil)
	digests := liveDigests(t, ts.URL)
	e := newEnv(t, nil)
	reviewed := func(s *ServerSpec) {
		s.Tools = []ToolDecl{
			{Name: "echo", Digest: digests["echo"]},                    // as reviewed
			{Name: "counter", Digest: "00" + digests["counter"][2:]},   // the server rewrote it since
			{Name: "inject", Digest: digests["inject"], Flagged: true}, // marked as instructions
			{Name: "secret", Digest: digests["secret"]},                // as reviewed
		}
	}
	n := 0
	list := func(allowed ...string) []string {
		n++
		ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-review-" + string(rune('a'+n)), ProjectID: "p"}, Servers: []ServerSpec{httpSpec("srv", ts.URL, reviewed)},
			Policy: allow(map[string][]string{"srv": allowed})})
		cs := e.connect(ep)
		// the first call connects to the server; the list after it is the server's own text, not the reviewed copy a run lists
		// from before it has contacted the server
		before := toolNames(t, cs)
		if r := callTool(t, cs, "srv__echo", map[string]any{"text": "hello"}); r.IsError {
			t.Fatalf("echo: %s", textOf(r))
		}
		lazy := map[string]bool{}
		for _, n := range before {
			lazy[n] = true
		}
		if lazy["srv__inject"] && !contains(allowed, "inject") {
			t.Fatalf("the first list offered the marked tool without a grant that names it: %v", before)
		}
		var names []string
		for _, n := range toolNames(t, cs) {
			if strings.HasPrefix(n, "srv__") {
				names = append(names, strings.TrimPrefix(n, "srv__"))
			}
		}
		return names
	}
	// a grant of everything: only the tools as reviewed and unmarked reach the model; "big", "write" and the others were not listed
	if got := list("*"); strings.Join(got, ",") != "echo,secret" {
		t.Fatalf("with a wildcard grant the model was offered %v", got)
	}
	// naming a tool is a person's decision: a marked one and one that was never listed are offered; a rewritten one still is not
	if got := list("echo", "inject", "big", "counter"); strings.Join(got, ",") != "big,echo,inject" {
		t.Fatalf("with named tools the model was offered %v", got)
	}

	// the same holds for a call made without listing first
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-review-call", ProjectID: "p"}, Servers: []ServerSpec{httpSpec("srv", ts.URL, reviewed)}, Policy: allow(map[string][]string{"srv": {"*"}})})
	cs := e.connect(ep)
	_ = toolNames(t, cs) // connecting upstream caches its tools
	if r := callTool(t, cs, "srv__echo", map[string]any{"text": "ok"}); r.IsError || !strings.Contains(textOf(r), "echo: ok") {
		t.Fatalf("a reviewed tool: %s", textOf(r))
	}
	for tool, want := range map[string]string{"counter": "changed", "inject": "instructions", "big": "not in the list"} {
		r := callTool(t, cs, "srv__"+tool, map[string]any{"bytes": 10})
		if !r.IsError || !strings.Contains(textOf(r), "withheld") || !strings.Contains(textOf(r), want) {
			t.Errorf("%s was not withheld for %q: %s", tool, want, textOf(r))
		}
	}
	reasons := map[string]string{}
	for _, ev := range e.events(EventToolWithheld) {
		d := eventData(t, ev)
		reasons[d["tool"].(string)] = d["reason"].(string)
	}
	for tool, want := range map[string]string{"counter": "changed", "inject": "instructions", "big": "not in the list"} {
		if !strings.Contains(reasons[tool], want) {
			t.Errorf("mcp.tool_withheld for %s says %q", tool, reasons[tool])
		}
	}
}

func TestAServerWithoutAReviewIsNotAffected(t *testing.T) {
	ts, _ := startHTTPUpstream(t, "", nil)
	e := newEnv(t, nil)
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-noreview", ProjectID: "p"}, Servers: []ServerSpec{httpSpec("srv", ts.URL, nil)}, Policy: allow(map[string][]string{"srv": {"*"}})})
	names := toolNames(t, e.connect(ep))
	for _, want := range []string{"srv__echo", "srv__counter", "srv__inject", "srv__big"} {
		if !contains(names, want) {
			t.Errorf("%s is missing from %v", want, names)
		}
	}
	if len(e.events(EventToolWithheld)) != 0 {
		t.Fatal("a server with no review withheld a tool")
	}
}
