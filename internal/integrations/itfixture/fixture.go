// Package itfixture holds PROTOCOL FIXTURES for the integrations tests: small local HTTP servers that answer the way each
// vendor's API documents (Linear GraphQL, Notion REST with Notion-Version, Sentry web API v0 with Link-header cursors and
// X-Sentry-Rate-Limit headers, Aikido's public REST API with client credentials, GitHub's REST issues endpoint, and an OAuth
// server for PKCE and the device flow). They are NOT the vendors' services: every shape here was written from the vendors'
// documentation and SDK sources (see docs/integrations.md "As built: workspace connections"), so the tests prove Drydock's
// behaviour (cursors, dedupe, untrusted handling, approval gating, secrets, rate limits) against those documented shapes, not
// the live APIs. The live checks (live_test.go) are the run against the real services.
//
// The package is imported only by tests and by the end-to-end harness (cmd/drydock-e2e), always labelled as a fixture.
package itfixture

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fail is an injected failure: the next list call answers with it.
type Fail struct {
	Status  int
	Headers map[string]string
	Body    string
}

// Common is the state every fixture shares: call counters, the parameters of list calls, the Authorization headers seen, and
// failure injection.
type Common struct {
	mu       sync.Mutex
	calls    map[string]int
	listArgs []map[string]any
	auth     []string
	headers  []http.Header
	failNext []Fail
	failWhen []standingFail
	revoked  bool
	// Scheme and Token are what the fixture expects in Authorization ("Bearer", or "" for Linear's bare API key).
	Scheme string
	Token  string
}

// Calls returns how many times an endpoint (a fixture label such as "issues" or "markdown") was called.
func (c *Common) Calls(label string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[label]
}

// ListArgs returns the parameters of every list call, oldest first.
func (c *Common) ListArgs() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.listArgs...)
}

// AuthSeen returns the Authorization header values the fixture saw (proof that the credential reached the server).
func (c *Common) AuthSeen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.auth...)
}

// HeaderSeen returns the values of one request header across every request.
func (c *Common) HeaderSeen(name string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, h := range c.headers {
		out = append(out, h.Get(name))
	}
	return out
}

// FailNext makes the next list call answer with f (FIFO).
func (c *Common) FailNext(f Fail) {
	c.mu.Lock()
	c.failNext = append(c.failNext, f)
	c.mu.Unlock()
}

type standingFail struct {
	key, value string
	f          Fail
}

// FailWhen makes every list call whose parameter key (as ListArgs records it: "team", "data_source", "project", ...) equals
// value answer with f, until ClearFailWhen. It stands for a scope the credential may not read: other scopes still answer.
func (c *Common) FailWhen(key, value string, f Fail) {
	c.mu.Lock()
	c.failWhen = append(c.failWhen, standingFail{key, value, f})
	c.mu.Unlock()
}

// ClearFailWhen removes every standing failure.
func (c *Common) ClearFailWhen() {
	c.mu.Lock()
	c.failWhen = nil
	c.mu.Unlock()
}

// Revoke makes the fixture answer 401 to every request, like a revoked credential.
func (c *Common) Revoke(v bool) {
	c.mu.Lock()
	c.revoked = v
	c.mu.Unlock()
}

// count records a call; for a list call it also records its parameters and returns an injected failure, if any.
func (c *Common) count(label string, args map[string]any, isList bool) *Fail {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[label]++
	if !isList {
		return nil
	}
	cp := map[string]any{}
	for k, v := range args {
		cp[k] = v
	}
	c.listArgs = append(c.listArgs, cp)
	if len(c.failNext) > 0 {
		f := c.failNext[0]
		c.failNext = c.failNext[1:]
		return &f
	}
	for _, sf := range c.failWhen {
		if v, ok := args[sf.key]; ok && v == sf.value {
			f := sf.f
			return &f
		}
	}
	return nil
}

func writeFail(w http.ResponseWriter, f *Fail) {
	for k, v := range f.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.Status)
	_, _ = w.Write([]byte(f.Body))
}

// guard checks the Authorization header; it answers 401 itself and returns false when the credential is wrong.
func (c *Common) guard(w http.ResponseWriter, r *http.Request) bool {
	got := r.Header.Get("Authorization")
	c.mu.Lock()
	c.auth = append(c.auth, got)
	c.headers = append(c.headers, r.Header.Clone())
	revoked := c.revoked
	c.mu.Unlock()
	want := c.Token
	if c.Scheme != "" {
		want = c.Scheme + " " + c.Token
	}
	if revoked || (c.Token != "" && got != want) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "unauthorized", "message": "invalid credential"})
		return false
	}
	return true
}

// Serve starts a fixture handler over real loopback HTTP and returns the server and its base URL.
func Serve(h http.Handler) (*httptest.Server, string) {
	ts := httptest.NewServer(h)
	return ts, ts.URL
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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

// ---------------------------------------------------------------- Linear (GraphQL)

// LinearIssue is one issue of the Linear fixture.
type LinearIssue struct {
	ID, Identifier, Title, Description, URL, State, Assignee, Team string
	Priority                                                       int // 0 none, 1 urgent, 2 high, 3 medium, 4 low
	Labels                                                         []string
	Created, Updated                                               time.Time
	Parent                                                         string
}

// Linear is the Linear GraphQL fixture (POST /graphql). It answers the operations Drydock sends, by operation name:
// DrydockIdentity (viewer, organization), DrydockTeams, DrydockProjects, DrydockIssues (filter team.key.eq, project.id.eq,
// updatedAt.gt; orderBy updatedAt, newest first; Relay first/after), DrydockComment (commentCreate), DrydockStates and
// DrydockSetState (issueUpdate stateId). Errors follow the shape of Linear's SDK: errors[].extensions.type.
type Linear struct {
	Common
	mu2      sync.Mutex
	issues   map[string]LinearIssue
	Comments []map[string]any
	Updates  []map[string]any
	// WrongShape makes the issues query answer data nobody expects (schema drift).
	WrongShape bool
	// Viewer and Org are what DrydockIdentity returns.
	Viewer, Org, OrgKey string
}

// NewLinear creates the fixture expecting a personal API key (sent without "Bearer").
func NewLinear(token string) *Linear {
	l := &Linear{issues: map[string]LinearIssue{}, Viewer: "Kyle Fixture", Org: "Fixture Org", OrgKey: "fixture"}
	l.Token = token
	return l
}

// Put adds or replaces an issue.
func (l *Linear) Put(i LinearIssue) {
	l.mu2.Lock()
	defer l.mu2.Unlock()
	l.issues[i.ID] = i
}

// Get returns an issue by id.
func (l *Linear) Get(id string) LinearIssue {
	l.mu2.Lock()
	defer l.mu2.Unlock()
	return l.issues[id]
}

// Delete removes an issue.
func (l *Linear) Delete(id string) {
	l.mu2.Lock()
	defer l.mu2.Unlock()
	delete(l.issues, id)
}

// CommentsSent returns a copy of the comments the fixture received.
func (l *Linear) CommentsSent() []map[string]any {
	l.mu2.Lock()
	defer l.mu2.Unlock()
	return append([]map[string]any(nil), l.Comments...)
}

// Server returns the HTTP handler.
func (l *Linear) Server() http.Handler { return http.HandlerFunc(l.serve) }

var linearStates = []string{"Todo", "In Progress", "Done"}

func (l *Linear) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != "/graphql" {
		http.NotFound(w, r)
		return
	}
	if !l.guard(w, r) {
		return
	}
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"errors": []any{map[string]any{"message": "bad json", "extensions": map[string]any{"type": "graphql error"}}}})
		return
	}
	data := func(v any) { writeJSON(w, 200, map[string]any{"data": v}) }
	v := req.Variables
	switch {
	case strings.Contains(req.Query, "DrydockIdentity"):
		l.count("identity", nil, false)
		data(map[string]any{"viewer": map[string]any{"name": l.Viewer, "displayName": strings.ToLower(l.Viewer)}, "organization": map[string]any{"name": l.Org, "urlKey": l.OrgKey}})
	case strings.Contains(req.Query, "DrydockTeams"):
		l.count("teams", nil, false)
		data(map[string]any{"teams": map[string]any{"nodes": []any{map[string]any{"key": "ENG", "name": "Engineering"}, map[string]any{"key": "OPS", "name": "Operations"}}}})
	case strings.Contains(req.Query, "DrydockProjects"):
		l.count("projects", nil, false)
		data(map[string]any{"projects": map[string]any{"nodes": []any{map[string]any{"id": "proj-1", "name": "Launch"}}}})
	case strings.Contains(req.Query, "DrydockIssues"):
		filter, _ := v["filter"].(map[string]any)
		team := dig(filter, "team", "key", "eq")
		since := dig(filter, "updatedAt", "gt")
		after, _ := v["after"].(string)
		firstN := 50
		if f, ok := v["first"].(float64); ok {
			firstN = int(f)
		}
		args := map[string]any{"team": team, "since": since, "after": after, "first": firstN}
		if f := l.count("issues", args, true); f != nil {
			writeFail(w, f)
			return
		}
		if l.WrongShape {
			data(map[string]any{"totally": "different"})
			return
		}
		var sinceT time.Time
		if since != "" {
			sinceT, _ = time.Parse(time.RFC3339Nano, since)
		}
		l.mu2.Lock()
		all := make([]LinearIssue, 0, len(l.issues))
		for _, i := range l.issues {
			if team != "" && i.Team != team {
				continue
			}
			if !sinceT.IsZero() && !i.Updated.After(sinceT) {
				continue
			}
			all = append(all, i)
		}
		l.mu2.Unlock()
		sort.Slice(all, func(x, y int) bool { return all[x].Updated.After(all[y].Updated) })
		pg, next := page(all, after, firstN)
		nodes := make([]any, 0, len(pg))
		for _, i := range pg {
			labels := []any{}
			for _, lb := range i.Labels {
				labels = append(labels, map[string]any{"name": lb})
			}
			n := map[string]any{"id": i.ID, "identifier": i.Identifier, "title": i.Title, "description": i.Description, "url": i.URL, "priority": i.Priority,
				"createdAt": i.Created.UTC().Format(time.RFC3339Nano), "updatedAt": i.Updated.UTC().Format(time.RFC3339Nano),
				"state": map[string]any{"name": i.State}, "labels": map[string]any{"nodes": labels}, "team": map[string]any{"key": i.Team}, "project": nil, "parent": nil}
			if i.Assignee != "" {
				n["assignee"] = map[string]any{"name": i.Assignee, "displayName": i.Assignee}
			} else {
				n["assignee"] = nil
			}
			if i.Parent != "" {
				n["parent"] = map[string]any{"identifier": i.Parent}
			}
			nodes = append(nodes, n)
		}
		data(map[string]any{"issues": map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": next != "", "endCursor": next}}})
	case strings.Contains(req.Query, "DrydockComment"):
		in, _ := v["input"].(map[string]any)
		l.count("commentCreate", nil, false)
		l.mu2.Lock()
		l.Comments = append(l.Comments, in)
		n := len(l.Comments)
		l.mu2.Unlock()
		data(map[string]any{"commentCreate": map[string]any{"success": true, "comment": map[string]any{"id": "cmt-" + strconv.Itoa(n)}}})
	case strings.Contains(req.Query, "DrydockStates"):
		l.count("states", nil, false)
		nodes := []any{}
		for i, s := range linearStates {
			nodes = append(nodes, map[string]any{"id": "st-" + strconv.Itoa(i), "name": s})
		}
		data(map[string]any{"issue": map[string]any{"team": map[string]any{"states": map[string]any{"nodes": nodes}}}})
	case strings.Contains(req.Query, "DrydockSetState"):
		l.count("issueUpdate", nil, false)
		in, _ := v["input"].(map[string]any)
		id, _ := v["id"].(string)
		st, _ := in["stateId"].(string)
		l.mu2.Lock()
		l.Updates = append(l.Updates, map[string]any{"id": id, "stateId": st})
		if is, ok := l.issues[id]; ok {
			if k, err := strconv.Atoi(strings.TrimPrefix(st, "st-")); err == nil && k < len(linearStates) {
				is.State = linearStates[k]
				l.issues[id] = is
			}
		}
		l.mu2.Unlock()
		data(map[string]any{"issueUpdate": map[string]any{"success": true}})
	default:
		writeJSON(w, 400, map[string]any{"errors": []any{map[string]any{"message": "unknown operation", "extensions": map[string]any{"type": "graphql error"}}}})
	}
}

// LinearRateLimited is the failure Linear answers when a credential spent its budget: HTTP 400 with a "ratelimited" GraphQL
// error and the X-RateLimit headers (reset in epoch milliseconds).
func LinearRateLimited(reset time.Time) Fail {
	return Fail{Status: 400, Headers: map[string]string{"X-RateLimit-Requests-Limit": "2500", "X-RateLimit-Requests-Remaining": "0", "X-RateLimit-Requests-Reset": strconv.FormatInt(reset.UnixMilli(), 10)},
		Body: `{"errors":[{"message":"Rate limit exceeded","extensions":{"type":"ratelimited","code":"RATELIMITED"}}]}`}
}

// LinearForbidden is Linear refusing a valid credential something it may not see: a GraphQL error of type "forbidden".
func LinearForbidden() Fail {
	return Fail{Status: 400, Body: `{"errors":[{"message":"Forbidden","extensions":{"type":"forbidden","code":"FORBIDDEN","userPresentableMessage":"You do not have access to this team."}}]}`}
}

func dig(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

// ---------------------------------------------------------------- Notion (REST, Notion-Version)

// NotionPage is one page of the Notion fixture.
type NotionPage struct {
	ID, Title, URL, Text string
	Edited               time.Time
	DataSource           string // the data source the page belongs to ("" = a standalone page)
}

// Notion is the Notion REST fixture: GET /v1/users/me (a bot of an internal integration), POST /v1/search (filter object
// page or data_source, sort last_edited_time, start_cursor, page_size), POST /v1/data_sources/{id}/query, GET
// /v1/pages/{id}/markdown, POST /v1/pages, POST /v1/comments. Every request must carry a Notion-Version header.
type Notion struct {
	Common
	mu2      sync.Mutex
	pages    map[string]NotionPage
	Created  []map[string]any
	Comments []map[string]any
	// Unsorted makes search return pages in a scrambled order.
	Unsorted       bool
	Bot, Workspace string
}

// NewNotion creates the fixture.
func NewNotion(token string) *Notion {
	n := &Notion{pages: map[string]NotionPage{}, Bot: "Drydock fixture", Workspace: "Fixture Workspace"}
	n.Scheme, n.Token = "Bearer", token
	return n
}

// Put adds or replaces a page.
func (n *Notion) Put(p NotionPage) {
	n.mu2.Lock()
	defer n.mu2.Unlock()
	n.pages[p.ID] = p
}

// Get returns a page by id.
func (n *Notion) Get(id string) NotionPage {
	n.mu2.Lock()
	defer n.mu2.Unlock()
	return n.pages[id]
}

// Server returns the HTTP handler.
func (n *Notion) Server() http.Handler { return http.HandlerFunc(n.serve) }

func notionPageJSON(p NotionPage) map[string]any {
	parent := map[string]any{"type": "workspace", "workspace": true}
	if p.DataSource != "" {
		parent = map[string]any{"type": "data_source_id", "data_source_id": p.DataSource}
	}
	return map[string]any{"object": "page", "id": p.ID, "url": p.URL, "in_trash": false, "created_time": p.Edited.Add(-time.Hour).UTC().Format(time.RFC3339),
		"last_edited_time": p.Edited.UTC().Format(time.RFC3339), "parent": parent,
		"properties": map[string]any{"Name": map[string]any{"id": "title", "type": "title", "title": []any{map[string]any{"type": "text", "plain_text": p.Title}}}}}
}

func (n *Notion) serve(w http.ResponseWriter, r *http.Request) {
	if !n.guard(w, r) {
		return
	}
	if r.Header.Get("Notion-Version") == "" {
		writeJSON(w, 400, map[string]any{"object": "error", "code": "missing_version", "message": "Notion-Version header failed validation"})
		return
	}
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == "/v1/users/me":
		n.count("me", nil, false)
		writeJSON(w, 200, map[string]any{"object": "user", "id": "bot-1", "type": "bot", "name": n.Bot,
			"bot": map[string]any{"owner": map[string]any{"type": "workspace", "workspace": true}, "workspace_name": n.Workspace}})
	case r.Method == "POST" && p == "/v1/search":
		filter, _ := body["filter"].(map[string]any)
		if filter["value"] == "data_source" {
			n.count("search_data_sources", nil, false)
			writeJSON(w, 200, map[string]any{"object": "list", "results": []any{map[string]any{"object": "data_source", "id": "ds-1",
				"title": []any{map[string]any{"plain_text": "Requirements"}}}}, "has_more": false, "next_cursor": nil})
			return
		}
		n.list(w, "search", body, "")
	case r.Method == "POST" && strings.HasPrefix(p, "/v1/data_sources/") && strings.HasSuffix(p, "/query"):
		ds := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/data_sources/"), "/query")
		n.list(w, "query", body, ds)
	case r.Method == "GET" && strings.HasPrefix(p, "/v1/pages/") && strings.HasSuffix(p, "/markdown"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/pages/"), "/markdown")
		n.count("markdown", nil, false)
		n.mu2.Lock()
		pg, ok := n.pages[id]
		n.mu2.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"object": "error", "code": "object_not_found"})
			return
		}
		writeJSON(w, 200, map[string]any{"object": "page_markdown", "id": id, "markdown": pg.Text, "truncated": false, "unknown_block_ids": []any{}})
	case r.Method == "POST" && p == "/v1/pages":
		n.count("create_page", nil, false)
		n.mu2.Lock()
		n.Created = append(n.Created, body)
		n.mu2.Unlock()
		writeJSON(w, 200, map[string]any{"object": "page", "id": "new-page-1"})
	case r.Method == "POST" && p == "/v1/comments":
		n.count("comment", nil, false)
		n.mu2.Lock()
		n.Comments = append(n.Comments, body)
		n.mu2.Unlock()
		writeJSON(w, 200, map[string]any{"object": "comment", "id": "comment-1"})
	default:
		writeJSON(w, 404, map[string]any{"object": "error", "code": "invalid_request_url"})
	}
}

func (n *Notion) list(w http.ResponseWriter, label string, body map[string]any, ds string) {
	cursor, _ := body["start_cursor"].(string)
	size := 50
	if f, ok := body["page_size"].(float64); ok {
		size = int(f)
	}
	query, _ := body["query"].(string)
	args := map[string]any{"start_cursor": cursor, "page_size": size, "query": query, "data_source": ds}
	if f := n.count(label, args, true); f != nil {
		writeFail(w, f)
		return
	}
	n.mu2.Lock()
	all := make([]NotionPage, 0, len(n.pages))
	for _, p := range n.pages {
		if ds != "" && p.DataSource != ds {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(p.Title), strings.ToLower(query)) {
			continue
		}
		all = append(all, p)
	}
	n.mu2.Unlock()
	sort.Slice(all, func(x, y int) bool { return all[x].Edited.After(all[y].Edited) })
	if n.Unsorted && len(all) > 2 {
		all[0], all[len(all)-1] = all[len(all)-1], all[0]
	}
	pg, next := page(all, cursor, size)
	out := []any{}
	for _, p := range pg {
		out = append(out, notionPageJSON(p))
	}
	var nc any
	if next != "" {
		nc = next
	}
	writeJSON(w, 200, map[string]any{"object": "list", "results": out, "next_cursor": nc, "has_more": next != ""})
}

// NotionRateLimited is Notion's 429: code rate_limited and a Retry-After in seconds.
func NotionRateLimited(seconds int) Fail {
	return Fail{Status: 429, Headers: map[string]string{"Retry-After": strconv.Itoa(seconds)}, Body: `{"object":"error","status":429,"code":"rate_limited","message":"You have been rate limited."}`}
}

// NotionRestricted is Notion's 403: the integration's capabilities do not allow the operation on that resource.
func NotionRestricted() Fail {
	return Fail{Status: 403, Body: `{"object":"error","status":403,"code":"restricted_resource","message":"API token does not have access to this resource."}`}
}

// ---------------------------------------------------------------- Sentry (web API v0)

// SentryIssue is one issue of the Sentry fixture.
type SentryIssue struct {
	ID, ShortID, Title, Culprit, Level, Status, Permalink, Project, Stack string
	Count, Users                                                          int
	First, Last                                                           time.Time
}

// SentryProject is one project of the Sentry fixture's organization. Sentry serializes project ids as strings of digits.
type SentryProject struct{ ID, Slug, Name string }

// Sentry is the Sentry web API fixture: GET /api/0/ (user and auth.scopes), GET /api/0/organizations/, GET
// /api/0/organizations/{org}/projects/ (id, slug, name), GET /api/0/organizations/{org}/issues/ (project: repeated integer
// project ids, as Sentry documents it; a slug is a 400 and an id outside the organization a 403; query, sort=date, limit,
// cursor; a Link header with rel="next" results="true|false" cursor="..."), GET .../issues/{id}/events/latest/ (exception
// entries), PUT .../issues/{id}/. Every answer carries the X-Sentry-Rate-Limit-* headers. An issue's Project is its project's
// slug; Put registers a project it has not seen.
type Sentry struct {
	Common
	mu2      sync.Mutex
	issues   map[string]SentryIssue
	projects []SentryProject
	Updates  []map[string]any
	Org      string
	// Remaining is what X-Sentry-Rate-Limit-Remaining says (default 39 of 40).
	Remaining int
	ResetAt   time.Time
}

// NewSentry creates the fixture (Authorization: Bearer <token>) for organization "acme".
func NewSentry(token string) *Sentry {
	s := &Sentry{issues: map[string]SentryIssue{}, Org: "acme", Remaining: 39,
		projects: []SentryProject{{ID: "4505321", Slug: "web", Name: "Web"}, {ID: "4505322", Slug: "api", Name: "API"}}}
	s.Scheme, s.Token = "Bearer", token
	return s
}

// Put adds or replaces an issue.
func (s *Sentry) Put(i SentryIssue) {
	s.mu2.Lock()
	defer s.mu2.Unlock()
	if i.Project != "" && s.projectBySlug(i.Project) == nil {
		s.projects = append(s.projects, SentryProject{ID: strconv.Itoa(4505321 + len(s.projects)), Slug: i.Project, Name: strings.ToUpper(i.Project)})
	}
	s.issues[i.ID] = i
}

func (s *Sentry) projectBySlug(slug string) *SentryProject {
	for i := range s.projects {
		if s.projects[i].Slug == slug {
			return &s.projects[i]
		}
	}
	return nil
}

// ProjectID returns the id of a project by its slug ("" when there is none).
func (s *Sentry) ProjectID(slug string) string {
	s.mu2.Lock()
	defer s.mu2.Unlock()
	if p := s.projectBySlug(slug); p != nil {
		return p.ID
	}
	return ""
}

// RemoveProject deletes a project (its issues stay, unreachable by that project's id).
func (s *Sentry) RemoveProject(slug string) {
	s.mu2.Lock()
	defer s.mu2.Unlock()
	for i := range s.projects {
		if s.projects[i].Slug == slug {
			s.projects = append(s.projects[:i], s.projects[i+1:]...)
			return
		}
	}
}

// Get returns an issue by id.
func (s *Sentry) Get(id string) SentryIssue {
	s.mu2.Lock()
	defer s.mu2.Unlock()
	return s.issues[id]
}

// Server returns the HTTP handler.
func (s *Sentry) Server() http.Handler { return http.HandlerFunc(s.serve) }

func (s *Sentry) serve(w http.ResponseWriter, r *http.Request) {
	s.mu2.Lock()
	rem, reset := s.Remaining, s.ResetAt
	s.mu2.Unlock()
	if reset.IsZero() {
		reset = time.Now().Add(time.Second)
	}
	w.Header().Set("X-Sentry-Rate-Limit-Limit", "40")
	w.Header().Set("X-Sentry-Rate-Limit-Remaining", strconv.Itoa(rem))
	w.Header().Set("X-Sentry-Rate-Limit-Reset", strconv.FormatInt(reset.Unix(), 10))
	w.Header().Set("X-Sentry-Rate-Limit-ConcurrentLimit", "25")
	w.Header().Set("X-Sentry-Rate-Limit-ConcurrentRemaining", "24")
	if !s.guard(w, r) {
		return
	}
	p := r.URL.Path
	orgPrefix := "/api/0/organizations/" + s.Org + "/"
	switch {
	case r.Method == "GET" && p == "/api/0/":
		s.count("root", nil, false)
		writeJSON(w, 200, map[string]any{"version": "0", "auth": map[string]any{"scopes": []string{"org:read", "project:read", "event:read", "event:write"}}, "user": map[string]any{"name": "Kyle Fixture", "username": "kyle"}})
	case r.Method == "GET" && p == "/api/0/organizations/":
		s.count("organizations", nil, false)
		writeJSON(w, 200, []any{map[string]any{"slug": s.Org, "name": "Acme Corp"}})
	case r.Method == "GET" && p == orgPrefix+"projects/":
		s.count("projects", nil, false)
		s.mu2.Lock()
		out := []any{}
		for _, pr := range s.projects {
			out = append(out, map[string]any{"id": pr.ID, "slug": pr.Slug, "name": pr.Name, "platform": "go"})
		}
		s.mu2.Unlock()
		writeJSON(w, 200, out)
	case r.Method == "GET" && p == orgPrefix+"issues/":
		q := r.URL.Query()
		args := map[string]any{"project": q.Get("project"), "query": q.Get("query"), "sort": q.Get("sort"), "cursor": q.Get("cursor"), "limit": q.Get("limit")}
		if f := s.count("issues", args, true); f != nil {
			writeFail(w, f)
			return
		}
		// "project": an array of integer project ids. Sentry refuses anything else, and an id the organization does not have
		want := map[string]bool{}
		for _, pid := range q["project"] {
			if _, err := strconv.ParseInt(pid, 10, 64); err != nil {
				writeJSON(w, 400, map[string]any{"detail": "Invalid project parameter. Values must be numbers."})
				return
			}
			want[pid] = true
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		s.mu2.Lock()
		slugs := map[string]bool{}
		for pid := range want {
			found := false
			for _, pr := range s.projects {
				if pr.ID == pid {
					slugs[pr.Slug], found = true, true
				}
			}
			if !found && pid != "-1" { // -1 is "every project"
				s.mu2.Unlock()
				writeJSON(w, 403, map[string]any{"detail": "You do not have permission to perform this action."})
				return
			}
		}
		if want["-1"] {
			slugs = map[string]bool{}
		}
		all := make([]SentryIssue, 0, len(s.issues))
		for _, i := range s.issues {
			if len(slugs) > 0 && !slugs[i.Project] {
				continue
			}
			all = append(all, i)
		}
		s.mu2.Unlock()
		sort.Slice(all, func(x, y int) bool { return all[x].Last.After(all[y].Last) })
		pg, next := page(all, q.Get("cursor"), limit)
		base := "http://" + r.Host + p
		results := "false"
		nc := "0:" + strconv.Itoa(len(all)) + ":0"
		if next != "" {
			results, nc = "true", next
		}
		w.Header().Set("Link", `<`+base+`?cursor=0:0:1>; rel="previous"; results="false"; cursor="0:0:1", <`+base+`?cursor=`+nc+`>; rel="next"; results="`+results+`"; cursor="`+nc+`"`)
		out := []any{}
		for _, i := range pg {
			s.mu2.Lock()
			pid := ""
			if pr := s.projectBySlug(i.Project); pr != nil {
				pid = pr.ID
			}
			s.mu2.Unlock()
			out = append(out, map[string]any{"id": i.ID, "shortId": i.ShortID, "title": i.Title, "culprit": i.Culprit, "level": i.Level, "status": i.Status, "permalink": i.Permalink,
				"project": map[string]any{"id": pid, "slug": i.Project, "name": strings.ToUpper(i.Project), "platform": "go"}, "count": strconv.Itoa(i.Count), "userCount": i.Users,
				"firstSeen": i.First.UTC().Format(time.RFC3339Nano), "lastSeen": i.Last.UTC().Format(time.RFC3339Nano), "metadata": map[string]any{"type": "Error", "value": i.Title},
				"assignedTo": nil, "priority": "high"})
		}
		writeJSON(w, 200, out)
	case r.Method == "GET" && strings.HasPrefix(p, orgPrefix+"issues/") && strings.HasSuffix(p, "/events/latest/"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, orgPrefix+"issues/"), "/events/latest/")
		s.count("latest_event", nil, false)
		s.mu2.Lock()
		i, ok := s.issues[id]
		s.mu2.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"detail": "The requested resource does not exist"})
			return
		}
		// Stack is written like a Python traceback: `File "f", line n, in fn` lines (each followed by its code line), and the
		// last line "Type: value". The event carries it the way Sentry does: an exception entry with frames and context lines.
		frames := []any{}
		excType, excValue := "Error", i.Title
		lines := strings.Split(i.Stack, "\n")
		for n := 0; n < len(lines); n++ {
			l := strings.TrimSpace(lines[n])
			var file, fn string
			var lineNo int
			if m := pyFrameRe.FindStringSubmatch(l); m != nil {
				file, fn = m[1], m[3]
				lineNo, _ = strconv.Atoi(m[2])
				fr := map[string]any{"filename": file, "function": fn, "lineNo": lineNo, "inApp": true}
				if n+1 < len(lines) && strings.HasPrefix(lines[n+1], "    ") {
					fr["context"] = []any{[]any{lineNo, strings.TrimSpace(lines[n+1])}}
					n++
				}
				frames = append(frames, fr)
				continue
			}
			if k := strings.Index(l, ": "); k > 0 && !strings.HasPrefix(l, "Traceback") {
				excType, excValue = l[:k], l[k+2:]
			}
		}
		writeJSON(w, 200, map[string]any{"id": "ev-1", "groupID": id, "entries": []any{map[string]any{"type": "exception", "data": map[string]any{"values": []any{
			map[string]any{"type": excType, "value": excValue, "stacktrace": map[string]any{"frames": frames}}}}}}})
	case r.Method == "PUT" && strings.HasPrefix(p, orgPrefix+"issues/"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, orgPrefix+"issues/"), "/")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.count("update", nil, false)
		s.mu2.Lock()
		s.Updates = append(s.Updates, map[string]any{"organization": s.Org, "issueId": id, "status": body["status"]})
		s.mu2.Unlock()
		writeJSON(w, 200, map[string]any{"status": body["status"]})
	default:
		writeJSON(w, 404, map[string]any{"detail": "The requested resource does not exist"})
	}
}

var pyFrameRe = regexp.MustCompile(`^File "([^"]+)", line (\d+), in (.+)$`)

// SetRemaining sets what the rate-limit headers say: remaining requests and when the window resets.
func (s *Sentry) SetRemaining(n int, reset time.Time) {
	s.mu2.Lock()
	s.Remaining, s.ResetAt = n, reset
	s.mu2.Unlock()
}

// SentryRateLimited is Sentry's 429 with Retry-After.
func SentryRateLimited(seconds int) Fail {
	return Fail{Status: 429, Headers: map[string]string{"Retry-After": strconv.Itoa(seconds)}, Body: `{"detail":"You are attempting to use this endpoint too frequently. Limit is 40 requests in 1 seconds"}`}
}

// ---------------------------------------------------------------- Aikido (public REST API, client credentials)

// AikidoFinding is one finding of the Aikido fixture, in the export's field names.
type AikidoFinding struct {
	ID              int    `json:"id"`
	GroupID         int    `json:"group_id"`
	Severity        string `json:"severity"`
	SeverityScore   int    `json:"severity_score"`
	Type            string `json:"type"`
	Rule            string `json:"rule"`
	AffectedPackage string `json:"affected_package"`
	AffectedFile    string `json:"affected_file"`
	CveID           string `json:"cve_id"`
	CodeRepoName    string `json:"code_repo_name"`
	CodeRepoID      int    `json:"code_repo_id"`
	Status          string `json:"status"`
	FirstDetectedAt int64  `json:"first_detected_at"` // Unix seconds
}

// Aikido is the Aikido public REST API fixture: POST /api/oauth/token (client credentials, HTTP Basic), then with the bought
// Bearer token: GET /api/public/v1/workspace, GET /api/public/v1/repositories/code, GET /api/public/v1/issues/export
// (format=json, filter_status, filter_code_repo_id), PUT /api/public/v1/issues/{id}/{ignore|unignore|snooze|unsnooze}.
type Aikido struct {
	Common
	mu2          sync.Mutex
	findings     []AikidoFinding
	Actions      []string
	ClientID     string
	ClientSecret string
	issued       map[string]bool
	n            int
	TokenCalls   int
}

// NewAikido creates the fixture accepting these client credentials.
func NewAikido(clientID, clientSecret string) *Aikido {
	return &Aikido{ClientID: clientID, ClientSecret: clientSecret, issued: map[string]bool{}}
}

// Set replaces the findings.
func (a *Aikido) Set(f []AikidoFinding) {
	a.mu2.Lock()
	a.findings = f
	a.mu2.Unlock()
}

// ExpireTokens invalidates every access token issued so far (the next API call answers 401).
func (a *Aikido) ExpireTokens() {
	a.mu2.Lock()
	a.issued = map[string]bool{}
	a.mu2.Unlock()
}

// Server returns the HTTP handler.
func (a *Aikido) Server() http.Handler { return http.HandlerFunc(a.serve) }

func (a *Aikido) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/oauth/token" {
		id, secret, ok := r.BasicAuth()
		_ = r.ParseForm()
		a.mu2.Lock()
		a.TokenCalls++
		if !ok || id != a.ClientID || secret != a.ClientSecret || r.PostForm.Get("grant_type") != "client_credentials" {
			a.mu2.Unlock()
			writeJSON(w, 401, map[string]any{"error": "invalid_client"})
			return
		}
		a.n++
		tok := "aik_access_" + strconv.Itoa(a.n) + "_Zq8Wm3Xr"
		a.issued[tok] = true
		a.mu2.Unlock()
		writeJSON(w, 200, map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": 3600, "scope": "issues:read issues:write repositories:read"})
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	a.mu.Lock()
	a.auth = append(a.auth, r.Header.Get("Authorization"))
	revoked := a.revoked
	a.mu.Unlock()
	a.mu2.Lock()
	valid := a.issued[got]
	a.mu2.Unlock()
	if revoked || !valid {
		writeJSON(w, 401, map[string]any{"error": "unauthorized"})
		return
	}
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == "/api/public/v1/workspace":
		a.count("workspace", nil, false)
		writeJSON(w, 200, map[string]any{"id": 77, "name": "Fixture Security"})
	case r.Method == "GET" && p == "/api/public/v1/repositories/code":
		a.count("repositories", nil, false)
		writeJSON(w, 200, []any{map[string]any{"id": 11, "name": "drydock"}, map[string]any{"id": 12, "name": "website"}})
	case r.Method == "GET" && p == "/api/public/v1/issues/export":
		q := r.URL.Query()
		args := map[string]any{"format": q.Get("format"), "filter_status": q.Get("filter_status"), "filter_code_repo_id": q.Get("filter_code_repo_id"), "filter_code_repo_name": q.Get("filter_code_repo_name")}
		if f := a.count("export", args, true); f != nil {
			writeFail(w, f)
			return
		}
		a.mu2.Lock()
		out := []AikidoFinding{}
		for _, f := range a.findings {
			if rid := q.Get("filter_code_repo_id"); rid != "" && strconv.Itoa(f.CodeRepoID) != rid {
				continue
			}
			if rn := q.Get("filter_code_repo_name"); rn != "" && f.CodeRepoName != rn {
				continue
			}
			if st := q.Get("filter_status"); st != "" && st != "all" && f.Status != st {
				continue
			}
			out = append(out, f)
		}
		a.mu2.Unlock()
		writeJSON(w, 200, out)
	case r.Method == "PUT" && strings.HasPrefix(p, "/api/public/v1/issues/"):
		rest := strings.Split(strings.TrimPrefix(p, "/api/public/v1/issues/"), "/")
		if len(rest) != 2 {
			http.NotFound(w, r)
			return
		}
		a.count(rest[1], nil, false)
		a.mu2.Lock()
		a.Actions = append(a.Actions, rest[1]+" "+rest[0])
		a.mu2.Unlock()
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		writeJSON(w, 404, map[string]any{"error": "not_found"})
	}
}
