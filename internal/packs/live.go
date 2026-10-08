package packs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/mcp"
)

// LiveSpec names the tool of a catalog server that answers a live pack.
type LiveSpec struct{ MCP, Tool, Arg string }

// LiveHit is one result of a live search.
type LiveHit struct {
	URL, Title, Text string
	Score            float64
}

// LiveSearcher reaches MCP docs servers. The production implementation goes through the internal/mcp gateway so a live
// pack gets the same allowlist, egress rules (no code, no secrets, short queries), cache, secret scan and untrusted
// marking as an agent's own call; this package does not talk to docs servers itself.
type LiveSearcher interface {
	Search(ctx context.Context, spec LiveSpec, query string, k int) ([]LiveHit, error)
	// Invalidate drops cached results of a server (called after the pack's local index is refreshed).
	Invalidate(ctx context.Context, mcpID string)
}

// RunGateway is the part of *mcp.Gateway that live packs use.
type RunGateway interface {
	StartRun(cfg mcp.RunConfig) (*mcp.Endpoint, error)
	EndRun(runID string)
	InvalidateCache(ctx context.Context, serverID string) (int, error)
}

// GatewayLive implements LiveSearcher over a gateway. Each search opens a short-lived service run whose policy allows exactly
// one tool of one server, calls it as an MCP client over the run's loopback endpoint (so the whole gateway pipeline applies),
// and closes the run.
type GatewayLive struct {
	GW RunGateway
	// Specs resolves a catalog server by id. Default: the built-in catalog.
	Specs   func(id string) (mcp.ServerSpec, bool)
	Timeout time.Duration

	once sync.Once
	cat  *mcp.Catalog
	cerr error
}

// ErrLiveUnavailable wraps failures to reach a live source (so callers can say "offline" rather than "nothing found").
var ErrLiveUnavailable = errors.New("live source unavailable")

type bearerRT struct{ tok string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

func (g *GatewayLive) spec(id string) (mcp.ServerSpec, bool) {
	if g.Specs != nil {
		return g.Specs(id)
	}
	g.once.Do(func() { g.cat, g.cerr = mcp.LoadBuiltin() })
	if g.cerr != nil {
		return mcp.ServerSpec{}, false
	}
	return g.cat.Get(id)
}

// Search implements LiveSearcher.
func (g *GatewayLive) Search(ctx context.Context, ls LiveSpec, query string, k int) ([]LiveHit, error) {
	spec, ok := g.spec(ls.MCP)
	if !ok {
		return nil, fmt.Errorf("%w: server %q is not in the MCP catalog", ErrLiveUnavailable, ls.MCP)
	}
	to := g.Timeout
	if to <= 0 {
		to = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	runID := ids.New("plive")
	ep, err := g.GW.StartRun(mcp.RunConfig{
		Info:      mcp.RunInfo{RunID: runID, AgentID: "packs", Role: "packs"},
		DataClass: mcp.DataClassPrivate, // the run is ours (no model provider is involved), so every server stays usable
		Policy:    mcp.RunPolicy{Allow: map[string][]string{spec.ID: {ls.Tool}}},
		Servers:   []mcp.ServerSpec{spec},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLiveUnavailable, err)
	}
	defer g.GW.EndRun(runID)
	client := sdk.NewClient(&sdk.Implementation{Name: "drydock-packs", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: ep.URL, HTTPClient: &http.Client{Transport: bearerRT{ep.Token}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLiveUnavailable, err)
	}
	defer cs.Close()
	arg := ls.Arg
	if arg == "" {
		arg = "query"
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: spec.ID + mcp.ToolSeparator + ls.Tool, Arguments: map[string]any{arg: query}})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLiveUnavailable, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text.WriteString(tc.Text)
			text.WriteString("\n")
		}
	}
	body := text.String()
	if inner, ok := mcp.ExtractBody(body); ok {
		body = inner
	}
	if res.IsError {
		return nil, fmt.Errorf("%w: %s", ErrLiveUnavailable, truncateRunes(strings.TrimSpace(body), 300))
	}
	hits := ParseLiveResults(strings.TrimSpace(body), spec.ID, ls.Tool)
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// Invalidate implements LiveSearcher.
func (g *GatewayLive) Invalidate(ctx context.Context, mcpID string) {
	_, _ = g.GW.InvalidateCache(ctx, mcpID)
}

// ParseLiveResults reads what docs servers return. The Cloudflare docs server returns {"results":[{similarity,url,title,text}]}
// (docs/mcp.md section 9); other servers' formats are unverified, so JSON with a results/items array of objects that carry a
// url, title and text/content/excerpt is understood and anything else becomes one hit whose URL names the server and tool.
// A result that carries no text at all is dropped.
func ParseLiveResults(body, server, tool string) []LiveHit {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		var arr []map[string]any
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(body), &obj) == nil {
			for _, key := range []string{"results", "items", "hits", "documents", "data"} {
				if raw, ok := obj[key]; ok && json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
					break
				}
			}
		} else if json.Unmarshal([]byte(body), &arr) != nil {
			arr = nil
		}
		var out []LiveHit
		for _, e := range arr {
			text := firstStr(e, "text", "content", "excerpt", "snippet", "body", "description")
			if strings.TrimSpace(text) == "" {
				continue
			}
			h := LiveHit{URL: firstStr(e, "url", "link", "source", "contentUrl"), Title: firstStr(e, "title", "name", "heading"), Text: text}
			if f, ok := e["similarity"].(float64); ok {
				h.Score = f
			} else if f, ok := e["score"].(float64); ok {
				h.Score = f
			}
			if h.URL == "" {
				h.URL = "mcp://" + server + "/" + tool
			}
			out = append(out, h)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []LiveHit{{URL: "mcp://" + server + "/" + tool, Title: server + " " + tool, Text: body}}
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
