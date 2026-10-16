package mcp

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveMicrosoftLearn runs microsoft_docs_search against the REAL Microsoft Learn MCP server through the gateway,
// with caching. It skips (never fakes) when the server cannot be reached: a probe sends a genuine MCP initialize request
// and the test only proceeds when a JSON-RPC answer comes back. Set DRYDOCK_MCP_NO_LIVE=1 to skip it explicitly.
func TestLiveMicrosoftLearn(t *testing.T) {
	if os.Getenv("DRYDOCK_MCP_NO_LIVE") != "" {
		t.Skip("DRYDOCK_MCP_NO_LIVE is set")
	}
	cat, err := LoadBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := cat.Get("mslearn")

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	probe := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"drydock-probe","version":"1"}}}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, spec.URL, strings.NewReader(probe))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("no network route to %s (outbound access is restricted here): %v", spec.URL, err)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"jsonrpc"`) {
		t.Skipf("%s did not answer an MCP initialize request (HTTP %d, %d bytes: %.120q): treating the live server as unreachable", spec.URL, resp.StatusCode, len(b), string(b))
	}

	e := newEnv(t, nil)
	ep := e.startRun(RunConfig{
		Info:    RunInfo{RunID: "r-live", ProjectID: "p"},
		Servers: []ServerSpec{spec},
		Policy:  allow(map[string][]string{"mslearn": {"microsoft_docs_search"}}),
	})
	cs := e.connect(ep)
	if names := toolNames(t, cs); strings.Join(names, ",") != "fetch_range,mslearn__microsoft_docs_search" {
		t.Fatalf("tools: %v", names)
	}
	q := map[string]any{"query": "Azure Functions consumption plan timeout"}
	r1 := callTool(t, cs, "mslearn__microsoft_docs_search", q)
	if r1.IsError {
		t.Fatalf("live search failed: %s", textOf(r1))
	}
	body1, ok := ExtractBody(textOf(r1))
	if !ok || strings.TrimSpace(body1) == "" {
		t.Fatalf("empty or unmarked live result: %.300s", textOf(r1))
	}
	t.Logf("live Learn search: returned %d bytes to the agent (total %v bytes, truncated=%v, cache=%v)", len(textOf(r1)),
		r1.Meta["drydock"].(map[string]any)["bytes_total"], r1.Meta["drydock"].(map[string]any)["truncated"], r1.Meta["drydock"].(map[string]any)["cache"])
	r2 := callTool(t, cs, "mslearn__microsoft_docs_search", q)
	body2, _ := ExtractBody(textOf(r2))
	if body1 != body2 {
		t.Fatal("the second identical call must be served from the cache with the same body")
	}
	var miss, hit int
	for _, ev := range e.events(EventCall) {
		switch eventData(t, ev)["cache"] {
		case CacheMiss:
			miss++
		case CacheHit:
			hit++
		}
	}
	if miss != 1 || hit != 1 || len(e.events(EventCacheHit)) != 1 {
		t.Fatalf("cache accounting: miss=%d hit=%d", miss, hit)
	}
	e.g.mu.RLock()
	defer e.g.mu.RUnlock()
	for k, u := range e.g.pool {
		if v := e.g.loadVersion(k); v == "" || v != u.version {
			t.Fatalf("server version not recorded: %q (live %q)", v, u.version)
		} else {
			t.Logf("Learn MCP server identifies as %s", v)
		}
	}
}
