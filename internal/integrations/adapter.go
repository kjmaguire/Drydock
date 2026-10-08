package integrations

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ToolNeed names a role a vendor tool plays and the tool names that can play it. The names of Linear's, Notion's and Sentry's
// hosted MCP tools were not verifiable in Phase 0 (docs/integrations.md section 10), so every role lists candidates and Test
// connection reports which one the server actually offers. Aikido's names come from the published package's tool list.
type ToolNeed struct {
	Role       string
	Candidates []string
	Required   bool // sync cannot run without it
	Write      bool // used only for approved outbound writes
}

// OpInfo describes one outbound operation.
type OpInfo struct {
	Op          string   `json:"op"`
	Label       string   `json:"label"`
	Role        string   `json:"-"` // the ToolNeed role it calls
	Params      []string `json:"params"`
	NeverAuto   bool     `json:"neverAuto"` // suppresses a signal or changes state in bulk: never eligible for a standing approval
	Description string   `json:"description"`
}

// RawItem is one item as an adapter understood it, before sanitizing.
type RawItem struct {
	ExternalID string
	Kind       string
	IssueRef   string
	URL        string
	Title      string
	Body       string // description / page text / finding detail
	State      string
	Labels     []string
	Assignee   string
	Priority   string // urgent | high | medium | low | none
	Parent     string
	Created    int64
	Updated    int64
	Etag       string
	Meta       map[string]any
	// NeedsBody is true when Body is only a summary and the full text must be fetched with Detail (Notion).
	NeedsBody bool
}

// Page is one page of a listing, newest first where the vendor supports an order.
type Page struct {
	Items []RawItem
	Next  string // the vendor's cursor for the next page; "" = no more
}

// Client is an adapter's view of a connected server: the caller plus the tool names resolved for each role.
type Client struct {
	Caller
	Conn  Connection
	Tools map[string]string // role -> tool name
}

// call calls the tool of a role and returns a failure when the server reported an error.
func (c *Client) call(ctx context.Context, role string, args map[string]any) (Result, error) {
	name := c.Tools[role]
	if name == "" {
		return Result{}, &Failure{Code: CodeToolMissing, Message: fmt.Sprintf("%s's MCP server does not offer a tool for %q, so Drydock cannot do this.", vendor(c.Conn.Kind), role), Cause: ErrToolMissing}
	}
	res, err := c.Caller.Call(ctx, name, args)
	if err != nil {
		return Result{}, Classify(c.Conn.Kind, err, "")
	}
	if res.IsError {
		return res, Classify(c.Conn.Kind, nil, res.Text)
	}
	return res, nil
}

// Adapter is the per-vendor knowledge: which tools to call, how to read their results, and how to build outbound calls.
type Adapter interface {
	Kind() string
	// Needs lists the tool roles.
	Needs() []ToolNeed
	// Page fetches one page of items. after is the vendor cursor ("" = first page). It must be read-only.
	Page(ctx context.Context, cl *Client, after string, limit int) (Page, error)
	// Detail returns the full text for an item whose list entry was a summary (Notion body, Sentry stack trace). ok=false means
	// there is nothing more to fetch.
	Detail(ctx context.Context, cl *Client, externalID string) (text string, ok bool, err error)
	// FullSweep reports that every sync lists everything (no vendor cursor or order), so items missing afterwards are gone.
	FullSweep() bool
	// Ops lists the outbound operations.
	Ops() []OpInfo
	// Build builds the exact tool call for an outbound operation.
	Build(op, target string, params map[string]string, cfg map[string]string) (OutCall, error)
	// TaskMapping maps an item to the task fields (priority, kind label) the importer needs.
	TaskMapping(it Item) TaskMapping
}

// OutCall is the exact outbound call: tool role, arguments and a preview for the human.
type OutCall struct {
	Role    string         `json:"role"`
	Tool    string         `json:"tool"` // filled when the tool is resolved against the server
	Args    map[string]any `json:"args"`
	Preview string         `json:"preview"`
}

// TaskMapping carries what an importer takes from an item that is not free text.
type TaskMapping struct {
	Priority int    // Drydock priority: higher is more urgent (0 none ... 4 urgent)
	Label    string // "bug", "security", "requirements", "issue"
}

// registry of adapters.
var adapters = map[string]Adapter{}

func register(a Adapter) { adapters[a.Kind()] = a }

// AdapterFor returns the adapter of a kind.
func AdapterFor(kind string) (Adapter, bool) { a, ok := adapters[kind]; return a, ok }

func init() {
	register(linearAdapter{})
	register(notionAdapter{})
	register(sentryAdapter{})
	register(aikidoAdapter{})
}

// resolveTools maps roles to the tool names a server offers. missing lists required roles with no match; optionalMissing lists
// the others.
func resolveTools(needs []ToolNeed, offered []string) (resolved map[string]string, missing, optionalMissing []string) {
	have := map[string]bool{}
	for _, n := range offered {
		have[n] = true
	}
	resolved = map[string]string{}
	for _, n := range needs {
		found := ""
		for _, cand := range n.Candidates {
			if have[cand] {
				found = cand
				break
			}
		}
		switch {
		case found != "":
			resolved[n.Role] = found
		case n.Required:
			missing = append(missing, n.Candidates[0])
		default:
			optionalMissing = append(optionalMissing, n.Candidates[0])
		}
	}
	sort.Strings(missing)
	sort.Strings(optionalMissing)
	return
}

// listItems extracts the item list and the paging cursor from a decoded listing: an array, or an object holding one under a
// well-known key.
func listItems(v any, keys ...string) (items []any, next string, ok bool) {
	if l, isList := v.([]any); isList {
		return l, "", true
	}
	m := asMap(v)
	if m == nil {
		return nil, "", false
	}
	found := false
	for _, k := range keys {
		if l, isList := m[k].([]any); isList {
			items, found = l, true
			break
		}
	}
	if !found {
		// one level down: {"data": {"issues": [...]}}
		for _, outer := range []string{"data", "result", "results"} {
			if inner := asMap(m[outer]); inner != nil {
				if its, nx, ok := listItems(inner, keys...); ok {
					return its, nx, true
				}
			}
		}
		return nil, "", false
	}
	next = str(pick(m, "nextCursor", "next_cursor", "cursor", "endCursor", "end_cursor", "next"))
	if pi := asMap(pick(m, "pageInfo", "page_info")); pi != nil {
		if hn, has := pi["hasNextPage"].(bool); has && !hn {
			next = ""
		} else if next == "" {
			next = str(pick(pi, "endCursor", "end_cursor"))
		}
	}
	if hm, has := pick(m, "hasMore", "has_more", "hasNextPage", "has_next_page").(bool); has && !hm {
		next = ""
	}
	return items, next, true
}

func clampText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// priorityTask maps the normalized priority words to Drydock's task priority (higher is more urgent).
func priorityTask(p string) int {
	switch strings.ToLower(p) {
	case "urgent", "critical", "fatal":
		return 4
	case "high", "error":
		return 3
	case "medium", "warning":
		return 2
	case "low", "info":
		return 1
	}
	return 0
}
