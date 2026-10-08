// Package itfixture holds PROTOCOL FIXTURES for the integrations tests: small MCP servers, written with the go-sdk, that
// reproduce the tool names and result shapes docs/integrations.md records for Linear, Notion, Sentry and Aikido. They are NOT
// the vendors' servers. Nothing here was checked against a live vendor (the sandbox cannot reach them), so every tool name and
// result shape marked UNVERIFIED in the adapters is only as correct as this file: the tests prove Drydock's behaviour (cursors,
// dedupe, untrusted handling, approval gating, secrets), not the vendors' APIs.
//
// The package is imported only by tests.
package itfixture

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Common is the state every fixture shares: call counters, the arguments of list calls, the Authorization headers seen, and
// failure injection.
type Common struct {
	mu       sync.Mutex
	calls    map[string]int
	listArgs []map[string]any
	auth     []string
	failNext []string
	revoked  bool
	// Scheme and Token are what the HTTP wrapper expects in Authorization ("Bearer", "Sentry-Bearer").
	Scheme string
	Token  string
}

func (c *Common) init() {
	if c.calls == nil {
		c.calls = map[string]int{}
	}
}

// Calls returns how many times a tool was called.
func (c *Common) Calls(tool string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[tool]
}

// ListArgs returns the arguments of every list call, oldest first.
func (c *Common) ListArgs() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.listArgs...)
}

// AuthSeen returns the Authorization header values the HTTP wrapper saw (proof that the token reached the server).
func (c *Common) AuthSeen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.auth...)
}

// FailNext makes the next list call return a tool-level error with this text (FIFO).
func (c *Common) FailNext(text string) {
	c.mu.Lock()
	c.failNext = append(c.failNext, text)
	c.mu.Unlock()
}

// Revoke makes the HTTP wrapper answer 401 to every request, like a revoked token.
func (c *Common) Revoke(v bool) {
	c.mu.Lock()
	c.revoked = v
	c.mu.Unlock()
}

func (c *Common) hit(tool string, args map[string]any, isList bool) *sdk.CallToolResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	c.calls[tool]++
	if isList {
		cp := map[string]any{}
		for k, v := range args {
			cp[k] = v
		}
		c.listArgs = append(c.listArgs, cp)
		if len(c.failNext) > 0 {
			t := c.failNext[0]
			c.failNext = c.failNext[1:]
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: t}}}
		}
	}
	return nil
}

// Wrap returns an http.Handler enforcing the expected Authorization header.
func (c *Common) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		c.mu.Lock()
		c.auth = append(c.auth, got)
		revoked := c.revoked
		c.mu.Unlock()
		if revoked || (c.Token != "" && got != c.Scheme+" "+c.Token) {
			http.Error(w, "401 unauthorized: invalid token", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// Serve starts the fixture's MCP server over real loopback HTTP and returns the endpoint URL.
func Serve(srv *sdk.Server, c *Common) (*httptest.Server, string) {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(c.Wrap(h))
	return ts, ts.URL + "/mcp"
}

func jsonResult(v any) *sdk.CallToolResult {
	b, _ := json.Marshal(v)
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}
}

func textResult(s string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
}

func argStr(a map[string]any, k string) string { s, _ := a[k].(string); return s }

func argInt(a map[string]any, k string, def int) int {
	switch x := a[k].(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return def
}

// addTool registers a tool taking free-form arguments.
func addTool(srv *sdk.Server, name, desc string, ro bool, fn func(args map[string]any) *sdk.CallToolResult) {
	sdk.AddTool(srv, &sdk.Tool{Name: name, Description: desc, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: ro}},
		func(_ context.Context, _ *sdk.CallToolRequest, in map[string]any) (*sdk.CallToolResult, any, error) {
			return fn(in), nil, nil
		})
}

// page slices items by an opaque cursor "c<offset>".
func page[T any](items []T, cursor string, limit int) (out []T, next string) {
	off := 0
	if strings.HasPrefix(cursor, "c") {
		off, _ = strconv.Atoi(cursor[1:])
	}
	if limit <= 0 {
		limit = 50
	}
	if off > len(items) {
		off = len(items)
	}
	end := off + limit
	if end > len(items) {
		end = len(items)
	}
	out = items[off:end]
	if end < len(items) {
		next = "c" + strconv.Itoa(end)
	}
	return
}

// ---------------------------------------------------------------- Linear

// LinearIssue is one issue of the Linear fixture.
type LinearIssue struct {
	ID, Identifier, Title, Description, URL, State, Assignee, Team string
	Priority                                                       int // 0 none, 1 urgent, 2 high, 3 medium, 4 low
	Labels                                                         []string
	Created, Updated                                               time.Time
	Parent                                                         string
}

// Linear is the Linear MCP fixture: list_issues (newest first, cursor paging), create_comment, update_issue.
type Linear struct {
	Common
	mu2      sync.Mutex
	issues   map[string]LinearIssue
	Comments []map[string]any
	Updates  []map[string]any
	// WrongShape makes list_issues return a payload no parser understands (schema drift).
	WrongShape bool
	// HideTools lists tool names the server does not offer.
	srv *sdk.Server
}

// NewLinear creates the fixture with the expected bearer token.
func NewLinear(token string) *Linear {
	l := &Linear{issues: map[string]LinearIssue{}}
	l.Scheme, l.Token = "Bearer", token
	l.build()
	return l
}

// Put adds or replaces an issue.
func (l *Linear) Put(i LinearIssue) {
	l.mu2.Lock()
	defer l.mu2.Unlock()
	l.issues[i.ID] = i
}

// Server returns the MCP server.
func (l *Linear) Server() *sdk.Server { return l.srv }

// CommentsSent returns a copy of the comments the fixture received.
func (l *Linear) CommentsSent() []map[string]any {
	l.mu2.Lock()
	defer l.mu2.Unlock()
	return append([]map[string]any(nil), l.Comments...)
}

func (l *Linear) build() {
	srv := sdk.NewServer(&sdk.Implementation{Name: "linear-fixture", Version: "0.0.1"}, nil)
	addTool(srv, "list_issues", "List issues, newest first (fixture)", true, func(a map[string]any) *sdk.CallToolResult {
		if r := l.hit("list_issues", a, true); r != nil {
			return r
		}
		if l.WrongShape {
			return textResult(`{"totally":"different"}`)
		}
		l.mu2.Lock()
		all := make([]LinearIssue, 0, len(l.issues))
		for _, i := range l.issues {
			if t := argStr(a, "team"); t != "" && i.Team != t {
				continue
			}
			all = append(all, i)
		}
		l.mu2.Unlock()
		sort.Slice(all, func(x, y int) bool { return all[x].Updated.After(all[y].Updated) })
		pg, next := page(all, argStr(a, "cursor"), argInt(a, "limit", 50))
		out := make([]map[string]any, 0, len(pg))
		for _, i := range pg {
			labels := []map[string]any{}
			for _, lb := range i.Labels {
				labels = append(labels, map[string]any{"name": lb})
			}
			out = append(out, map[string]any{"id": i.ID, "identifier": i.Identifier, "title": i.Title, "description": i.Description, "url": i.URL,
				"priority": map[string]any{"value": i.Priority, "name": [...]string{"No priority", "Urgent", "High", "Medium", "Low"}[i.Priority]},
				"state": map[string]any{"name": i.State}, "assignee": map[string]any{"name": i.Assignee}, "labels": labels,
				"createdAt": i.Created.UTC().Format(time.RFC3339Nano), "updatedAt": i.Updated.UTC().Format(time.RFC3339Nano), "team": map[string]any{"key": i.Team}, "parent": i.Parent})
		}
		return jsonResult(map[string]any{"issues": out, "nextCursor": next, "hasNextPage": next != ""})
	})
	addTool(srv, "create_comment", "Comment on an issue (fixture)", false, func(a map[string]any) *sdk.CallToolResult {
		l.hit("create_comment", a, false)
		l.mu2.Lock()
		l.Comments = append(l.Comments, a)
		l.mu2.Unlock()
		return jsonResult(map[string]any{"id": "cmt-" + strconv.Itoa(len(l.Comments)), "ok": true})
	})
	addTool(srv, "update_issue", "Update an issue (fixture)", false, func(a map[string]any) *sdk.CallToolResult {
		l.hit("update_issue", a, false)
		l.mu2.Lock()
		l.Updates = append(l.Updates, a)
		l.mu2.Unlock()
		return jsonResult(map[string]any{"ok": true})
	})
	l.srv = srv
}

// ---------------------------------------------------------------- Notion

// NotionPage is one page of the Notion fixture.
type NotionPage struct {
	ID, Title, URL, Text string
	Edited               time.Time
}

// Notion is the Notion MCP fixture: notion-search, notion-fetch, notion-create-pages, notion-create-comment.
type Notion struct {
	Common
	mu2      sync.Mutex
	pages    map[string]NotionPage
	Created  []map[string]any
	Comments []map[string]any
	// Unsorted makes notion-search return pages in a scrambled order.
	Unsorted bool
	srv      *sdk.Server
}

// NewNotion creates the fixture.
func NewNotion(token string) *Notion {
	n := &Notion{pages: map[string]NotionPage{}}
	n.Scheme, n.Token = "Bearer", token
	n.build()
	return n
}

// Put adds or replaces a page.
func (n *Notion) Put(p NotionPage) {
	n.mu2.Lock()
	defer n.mu2.Unlock()
	n.pages[p.ID] = p
}

// Server returns the MCP server.
func (n *Notion) Server() *sdk.Server { return n.srv }

func (n *Notion) build() {
	srv := sdk.NewServer(&sdk.Implementation{Name: "notion-fixture", Version: "0.0.1"}, nil)
	addTool(srv, "notion-search", "Search pages (fixture)", true, func(a map[string]any) *sdk.CallToolResult {
		if r := n.hit("notion-search", a, true); r != nil {
			return r
		}
		n.mu2.Lock()
		all := make([]NotionPage, 0, len(n.pages))
		for _, p := range n.pages {
			all = append(all, p)
		}
		n.mu2.Unlock()
		sort.Slice(all, func(x, y int) bool { return all[x].Edited.After(all[y].Edited) })
		if n.Unsorted && len(all) > 2 {
			all[0], all[len(all)-1] = all[len(all)-1], all[0]
		}
		pg, next := page(all, argStr(a, "start_cursor"), argInt(a, "page_size", 50))
		out := []map[string]any{}
		for _, p := range pg {
			out = append(out, map[string]any{"id": p.ID, "title": p.Title, "url": p.URL, "type": "page", "last_edited_time": p.Edited.UTC().Format(time.RFC3339Nano), "highlight": first(p.Text, 80)})
		}
		return jsonResult(map[string]any{"results": out, "next_cursor": next, "has_more": next != ""})
	})
	addTool(srv, "notion-fetch", "Fetch a page as Markdown (fixture)", true, func(a map[string]any) *sdk.CallToolResult {
		n.hit("notion-fetch", a, false)
		n.mu2.Lock()
		p, ok := n.pages[argStr(a, "id")]
		n.mu2.Unlock()
		if !ok {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "page not found"}}}
		}
		return jsonResult(map[string]any{"title": p.Title, "text": p.Text})
	})
	addTool(srv, "notion-create-pages", "Create pages (fixture)", false, func(a map[string]any) *sdk.CallToolResult {
		n.hit("notion-create-pages", a, false)
		n.mu2.Lock()
		n.Created = append(n.Created, a)
		n.mu2.Unlock()
		return jsonResult(map[string]any{"ok": true})
	})
	addTool(srv, "notion-create-comment", "Comment on a page (fixture)", false, func(a map[string]any) *sdk.CallToolResult {
		n.hit("notion-create-comment", a, false)
		n.mu2.Lock()
		n.Comments = append(n.Comments, a)
		n.mu2.Unlock()
		return jsonResult(map[string]any{"ok": true})
	})
	n.srv = srv
}

func first(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ---------------------------------------------------------------- Sentry

// SentryIssue is one issue of the Sentry fixture.
type SentryIssue struct {
	ID, ShortID, Title, Culprit, Level, Status, Permalink, Project, Stack string
	Count, Users                                                          int
	First, Last                                                           time.Time
}

// Sentry is the Sentry MCP fixture: list_issues, get_issue_details, update_issue.
type Sentry struct {
	Common
	mu2     sync.Mutex
	issues  map[string]SentryIssue
	Updates []map[string]any
	srv     *sdk.Server
}

// NewSentry creates the fixture (hosted Sentry expects "Sentry-Bearer <token>").
func NewSentry(token string) *Sentry {
	s := &Sentry{issues: map[string]SentryIssue{}}
	s.Scheme, s.Token = "Sentry-Bearer", token
	s.build()
	return s
}

// Put adds or replaces an issue.
func (s *Sentry) Put(i SentryIssue) {
	s.mu2.Lock()
	defer s.mu2.Unlock()
	s.issues[i.ID] = i
}

// Server returns the MCP server.
func (s *Sentry) Server() *sdk.Server { return s.srv }

func (s *Sentry) build() {
	srv := sdk.NewServer(&sdk.Implementation{Name: "sentry-fixture", Version: "0.0.1"}, nil)
	addTool(srv, "list_issues", "List issues (fixture)", true, func(a map[string]any) *sdk.CallToolResult {
		if r := s.hit("list_issues", a, true); r != nil {
			return r
		}
		s.mu2.Lock()
		all := make([]SentryIssue, 0, len(s.issues))
		for _, i := range s.issues {
			if p := argStr(a, "projectSlugOrId"); p != "" && i.Project != p {
				continue
			}
			all = append(all, i)
		}
		s.mu2.Unlock()
		sort.Slice(all, func(x, y int) bool { return all[x].Last.After(all[y].Last) })
		pg, next := page(all, argStr(a, "cursor"), argInt(a, "limit", 50))
		out := []map[string]any{}
		for _, i := range pg {
			out = append(out, map[string]any{"id": i.ID, "shortId": i.ShortID, "title": i.Title, "culprit": i.Culprit, "level": i.Level, "status": i.Status, "permalink": i.Permalink,
				"project": map[string]any{"slug": i.Project, "name": strings.ToUpper(i.Project)}, "count": strconv.Itoa(i.Count), "userCount": i.Users,
				"firstSeen": i.First.UTC().Format(time.RFC3339Nano), "lastSeen": i.Last.UTC().Format(time.RFC3339Nano), "metadata": map[string]any{"type": "Error", "value": i.Title}})
		}
		return jsonResult(map[string]any{"issues": out, "nextCursor": next})
	})
	addTool(srv, "get_issue_details", "Issue details with stack trace (fixture)", true, func(a map[string]any) *sdk.CallToolResult {
		s.hit("get_issue_details", a, false)
		s.mu2.Lock()
		i, ok := s.issues[argStr(a, "issueId")]
		s.mu2.Unlock()
		if !ok {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "issue not found"}}}
		}
		return jsonResult(map[string]any{"id": i.ID, "title": i.Title, "stacktrace": i.Stack})
	})
	addTool(srv, "update_issue", "Update issue status (fixture)", false, func(a map[string]any) *sdk.CallToolResult {
		s.hit("update_issue", a, false)
		s.mu2.Lock()
		s.Updates = append(s.Updates, a)
		s.mu2.Unlock()
		return jsonResult(map[string]any{"ok": true})
	})
	s.srv = srv
}

// ---------------------------------------------------------------- Aikido

// AikidoFinding is one finding of the Aikido fixture.
type AikidoFinding struct {
	ID          int    `json:"id"`
	Severity    string `json:"severity"`
	Type        string `json:"type"`
	Rule        string `json:"rule"`
	Description string `json:"description"`
	Repo        string `json:"repo"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Status      string `json:"status"`
	UpdatedAt   string `json:"updated_at"`
	FirstAt     string `json:"first_detected_at"`
}

// Aikido is the Aikido MCP fixture (tool names from the published package): aikido_issues_list and the ignore/snooze tools.
type Aikido struct {
	Common
	mu2      sync.Mutex
	findings []AikidoFinding
	Actions  []string
	srv      *sdk.Server
	// Load, when set, is called on every list call to refresh the findings (the stdio fixture re-reads a file).
	Load func() []AikidoFinding
	// KeyCheck, when set, validates the call (the stdio fixture checks AIKIDO_API_KEY).
	KeyCheck func() error
}

// NewAikido creates the fixture.
func NewAikido() *Aikido {
	a := &Aikido{}
	a.build()
	return a
}

// Set replaces the findings.
func (a *Aikido) Set(f []AikidoFinding) {
	a.mu2.Lock()
	a.findings = f
	a.mu2.Unlock()
}

// Server returns the MCP server.
func (a *Aikido) Server() *sdk.Server { return a.srv }

func (a *Aikido) build() {
	srv := sdk.NewServer(&sdk.Implementation{Name: "aikido-fixture", Version: "1.0.27"}, nil)
	deny := func() *sdk.CallToolResult {
		if a.KeyCheck != nil {
			if err := a.KeyCheck(); err != nil {
				return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "401 unauthorized: " + err.Error()}}}
			}
		}
		return nil
	}
	addTool(srv, "aikido_issues_list", "List findings (fixture)", true, func(args map[string]any) *sdk.CallToolResult {
		if r := a.hit("aikido_issues_list", args, true); r != nil {
			return r
		}
		if r := deny(); r != nil {
			return r
		}
		if a.Load != nil {
			a.Set(a.Load())
		}
		a.mu2.Lock()
		defer a.mu2.Unlock()
		out := []map[string]any{}
		for _, f := range a.findings {
			if r := argStr(args, "repo"); r != "" && f.Repo != r {
				continue
			}
			out = append(out, map[string]any{"id": f.ID, "severity": f.Severity, "type": f.Type, "rule": f.Rule, "description": f.Description, "repo": f.Repo,
				"location": map[string]any{"file": f.File, "line": f.Line}, "status": f.Status, "updated_at": f.UpdatedAt, "first_detected_at": f.FirstAt})
		}
		return jsonResult(map[string]any{"issues": out})
	})
	for _, name := range []string{"aikido_ignore_issue", "aikido_unignore_issue", "aikido_snooze_issue", "aikido_unsnooze_issue"} {
		name := name
		addTool(srv, name, name+" (fixture)", false, func(args map[string]any) *sdk.CallToolResult {
			a.hit(name, args, false)
			if r := deny(); r != nil {
				return r
			}
			a.mu2.Lock()
			a.Actions = append(a.Actions, fmt.Sprintf("%s %v", name, args["issueId"]))
			a.mu2.Unlock()
			return jsonResult(map[string]any{"ok": true})
		})
	}
	addTool(srv, "aikido_workspaces_list", "List workspaces (fixture)", true, func(args map[string]any) *sdk.CallToolResult {
		a.hit("aikido_workspaces_list", args, false)
		return jsonResult(map[string]any{"workspaces": []map[string]any{{"id": 1, "name": "fixture"}}})
	})
	a.srv = srv
}
