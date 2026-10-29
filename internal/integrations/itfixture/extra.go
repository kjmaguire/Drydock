package itfixture

// PROTOCOL FIXTURES for decision P114 (webhooks, typed clients, attachments, typed Notion rows, status sync, the comment lookup),
// written from the vendors' documented shapes (docs/integrations.md "As built: webhooks, mappings, status sync"), not from the
// live services:
//
//   - Linear: DrydockIssue (Query.issue), DrydockAttachments (Issue.attachments), DrydockListComments (Issue.comments, the ones the
//     fixture received and any put there), DrydockTeamStates (Team.states); GET /files/<name>, a file "uploaded to Linear", which
//     needs the credential.
//   - Notion: GET /v1/pages/{id}, PATCH /v1/pages/{id} (properties), GET /v1/data_sources/{id} (the property schema), GET
//     /v1/comments?block_id=. A page's Props are its raw property values, in the API's shape.
//   - Sentry: GET /api/0/organizations/{org}/issues/{id}/, the latest event's eventID and projectID, GET
//     /api/0/projects/{org}/{project}/events/{event}/attachments/ and .../{id}/?download=1.
//   - Signing helpers that sign a webhook body the way each vendor's documentation says it does.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LinearAttachment is one attachment of a Linear fixture issue.
type LinearAttachment struct {
	ID, Title, URL string
}

// linearExtra is the extra state of the Linear fixture.
type linearExtra struct {
	mu          sync.Mutex
	attachments map[string][]LinearAttachment
	files       map[string][]byte
	comments    map[string][]map[string]any // issue id -> comments put there (id, body, createdAt)
	failComment string
}

// FailNextComment makes the next commentCreate fail: "before" (nothing is stored) or "after" (the comment is stored but the
// answer is a server error, as when the vendor accepted the call and the answer was lost).
func (l *Linear) FailNextComment(mode string) {
	l.ex.mu.Lock()
	l.ex.failComment = mode
	l.ex.mu.Unlock()
}

func (l *Linear) takeCommentFailure() string {
	l.ex.mu.Lock()
	defer l.ex.mu.Unlock()
	m := l.ex.failComment
	l.ex.failComment = ""
	return m
}

// PutAttachment adds an attachment to an issue.
func (l *Linear) PutAttachment(issueID string, a LinearAttachment) {
	l.ex.mu.Lock()
	defer l.ex.mu.Unlock()
	if l.ex.attachments == nil {
		l.ex.attachments = map[string][]LinearAttachment{}
	}
	l.ex.attachments[issueID] = append(l.ex.attachments[issueID], a)
}

// PutFile serves a file under /files/<name> (an upload to Linear: the credential is required).
func (l *Linear) PutFile(name string, b []byte) {
	l.ex.mu.Lock()
	defer l.ex.mu.Unlock()
	if l.ex.files == nil {
		l.ex.files = map[string][]byte{}
	}
	l.ex.files[name] = b
}

// PutComment adds a comment someone else posted on an issue.
func (l *Linear) PutComment(issueID, id, body string, at time.Time) {
	l.ex.mu.Lock()
	defer l.ex.mu.Unlock()
	if l.ex.comments == nil {
		l.ex.comments = map[string][]map[string]any{}
	}
	l.ex.comments[issueID] = append(l.ex.comments[issueID], map[string]any{"id": id, "body": body, "createdAt": at.UTC().Format(time.RFC3339Nano)})
}

func (l *Linear) serveFile(w http.ResponseWriter, r *http.Request) {
	if !l.guard(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/files/")
	l.ex.mu.Lock()
	b, ok := l.ex.files[name]
	l.ex.mu.Unlock()
	l.count("file", nil, false)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}

func linearNode(i LinearIssue) map[string]any {
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
	return n
}

// serveExtra answers the P114 operations; false when the query is none of them.
func (l *Linear) serveExtra(w http.ResponseWriter, query string, v map[string]any) bool {
	data := func(x any) { writeJSON(w, 200, map[string]any{"data": x}) }
	id, _ := v["id"].(string)
	switch {
	case strings.Contains(query, "DrydockIssue("):
		l.count("issue", nil, false)
		l.mu2.Lock()
		i, ok := l.issues[id]
		l.mu2.Unlock()
		if !ok {
			// Linear answers an unknown id with an error ("Entity not found")
			writeJSON(w, 200, map[string]any{"data": nil, "errors": []any{map[string]any{"message": "Entity not found: Issue", "extensions": map[string]any{"type": "invalid input", "code": "INPUT_ERROR"}}}})
			return true
		}
		data(map[string]any{"issue": linearNode(i)})
	case strings.Contains(query, "DrydockAttachments"):
		l.count("attachments", nil, false)
		l.ex.mu.Lock()
		nodes := []any{}
		for _, a := range l.ex.attachments[id] {
			nodes = append(nodes, map[string]any{"id": a.ID, "title": a.Title, "subtitle": "", "url": a.URL, "createdAt": time.Now().UTC().Format(time.RFC3339)})
		}
		l.ex.mu.Unlock()
		data(map[string]any{"issue": map[string]any{"attachments": map[string]any{"nodes": nodes}}})
	case strings.Contains(query, "DrydockListComments"):
		l.count("comments", nil, false)
		nodes := []any{}
		l.ex.mu.Lock()
		for _, c := range l.ex.comments[id] {
			nodes = append(nodes, c)
		}
		l.ex.mu.Unlock()
		l.mu2.Lock()
		for k, c := range l.Comments {
			if c["issueId"] == id {
				nodes = append(nodes, map[string]any{"id": "cmt-" + strconv.Itoa(k+1), "body": c["body"], "createdAt": time.Now().UTC().Format(time.RFC3339Nano)})
			}
		}
		l.mu2.Unlock()
		data(map[string]any{"issue": map[string]any{"comments": map[string]any{"nodes": nodes}}})
	case strings.Contains(query, "DrydockTeamStates"):
		l.count("team_states", nil, false)
		nodes := []any{}
		for i, s := range linearStates {
			nodes = append(nodes, map[string]any{"id": "st-" + strconv.Itoa(i), "name": s, "type": []string{"unstarted", "started", "completed"}[i]})
		}
		key, _ := v["key"].(string)
		data(map[string]any{"teams": map[string]any{"nodes": []any{map[string]any{"key": key, "states": map[string]any{"nodes": nodes}}}}})
	default:
		return false
	}
	return true
}

// ---- Notion ----

type notionExtra struct {
	mu          sync.Mutex
	dataSources map[string]map[string]any // id -> properties schema
	updates     []map[string]any
}

// PutDataSource sets a data source's property schema (GET /v1/data_sources/{id}).
func (n *Notion) PutDataSource(id string, properties map[string]any) {
	n.ex.mu.Lock()
	defer n.ex.mu.Unlock()
	if n.ex.dataSources == nil {
		n.ex.dataSources = map[string]map[string]any{}
	}
	n.ex.dataSources[id] = properties
}

// PageUpdates returns the PATCH /v1/pages/{id} bodies the fixture received (with "id").
func (n *Notion) PageUpdates() []map[string]any {
	n.ex.mu.Lock()
	defer n.ex.mu.Unlock()
	return append([]map[string]any(nil), n.ex.updates...)
}

func (n *Notion) serveExtra(w http.ResponseWriter, r *http.Request, p string, body map[string]any) bool {
	switch {
	case r.Method == "GET" && strings.HasPrefix(p, "/v1/pages/") && !strings.Contains(strings.TrimPrefix(p, "/v1/pages/"), "/"):
		id := strings.TrimPrefix(p, "/v1/pages/")
		n.count("page", nil, false)
		n.mu2.Lock()
		pg, ok := n.pages[id]
		n.mu2.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"object": "error", "status": 404, "code": "object_not_found"})
			return true
		}
		writeJSON(w, 200, notionPageJSON(pg))
	case r.Method == "PATCH" && strings.HasPrefix(p, "/v1/pages/"):
		id := strings.TrimPrefix(p, "/v1/pages/")
		n.count("update_page", nil, false)
		props, _ := body["properties"].(map[string]any)
		n.mu2.Lock()
		pg, ok := n.pages[id]
		if ok {
			if pg.Props == nil {
				pg.Props = map[string]any{}
			}
			for k, v := range props {
				vm, _ := v.(map[string]any)
				for typ, val := range vm {
					pg.Props[k] = map[string]any{"id": k, "type": typ, typ: val}
				}
			}
			pg.Edited = pg.Edited.Add(time.Second)
			n.pages[id] = pg
		}
		n.mu2.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"object": "error", "status": 404, "code": "object_not_found"})
			return true
		}
		n.ex.mu.Lock()
		n.ex.updates = append(n.ex.updates, map[string]any{"id": id, "properties": props})
		n.ex.mu.Unlock()
		writeJSON(w, 200, notionPageJSON(pg))
	case r.Method == "GET" && strings.HasPrefix(p, "/v1/data_sources/"):
		id := strings.TrimPrefix(p, "/v1/data_sources/")
		n.count("data_source", nil, false)
		n.ex.mu.Lock()
		props, ok := n.ex.dataSources[id]
		n.ex.mu.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"object": "error", "status": 404, "code": "object_not_found"})
			return true
		}
		writeJSON(w, 200, map[string]any{"object": "data_source", "id": id, "properties": props})
	case r.Method == "GET" && p == "/v1/comments":
		n.count("comments", nil, false)
		block := r.URL.Query().Get("block_id")
		out := []any{}
		n.mu2.Lock()
		for k, c := range n.Comments {
			parent, _ := c["parent"].(map[string]any)
			if parent["page_id"] == block {
				out = append(out, map[string]any{"object": "comment", "id": "comment-" + strconv.Itoa(k+1), "rich_text": c["rich_text"], "created_time": time.Now().UTC().Format(time.RFC3339)})
			}
		}
		n.mu2.Unlock()
		writeJSON(w, 200, map[string]any{"object": "list", "results": out, "has_more": false, "next_cursor": nil})
	default:
		return false
	}
	return true
}

// NotionFile is a files property value of one Notion-hosted file (a signed address on another host) and one external link.
func NotionFiles(hostedName, hostedURL, linkName, linkURL string) map[string]any {
	return map[string]any{"type": "files", "files": []any{
		map[string]any{"name": hostedName, "type": "file", "file": map[string]any{"url": hostedURL, "expiry_time": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}},
		map[string]any{"name": linkName, "type": "external", "external": map[string]any{"url": linkURL}},
	}}
}

// ---- Sentry ----

// SentryAttachment is one attachment of an issue's latest event.
type SentryAttachment struct {
	ID, Name, Mime string
	Data           []byte
}

type sentryExtra struct {
	mu          sync.Mutex
	attachments map[string][]SentryAttachment // issue id -> attachments of its latest event
}

// PutAttachment adds an attachment to an issue's latest event.
func (s *Sentry) PutAttachment(issueID string, a SentryAttachment) {
	s.ex.mu.Lock()
	defer s.ex.mu.Unlock()
	if s.ex.attachments == nil {
		s.ex.attachments = map[string][]SentryAttachment{}
	}
	s.ex.attachments[issueID] = append(s.ex.attachments[issueID], a)
}

func (s *Sentry) issueJSON(i SentryIssue) map[string]any {
	pid := ""
	if pr := s.projectBySlug(i.Project); pr != nil {
		pid = pr.ID
	}
	return map[string]any{"id": i.ID, "shortId": i.ShortID, "title": i.Title, "culprit": i.Culprit, "level": i.Level, "status": i.Status, "permalink": i.Permalink,
		"project": map[string]any{"id": pid, "slug": i.Project, "name": strings.ToUpper(i.Project), "platform": "go"}, "count": strconv.Itoa(i.Count), "userCount": i.Users,
		"firstSeen": i.First.UTC().Format(time.RFC3339Nano), "lastSeen": i.Last.UTC().Format(time.RFC3339Nano), "metadata": map[string]any{"type": "Error", "value": i.Title},
		"assignedTo": nil, "priority": "high"}
}

func (s *Sentry) serveExtra(w http.ResponseWriter, r *http.Request, p, orgPrefix string) bool {
	projPrefix := "/api/0/projects/" + s.Org + "/"
	switch {
	case r.Method == "GET" && strings.HasPrefix(p, orgPrefix+"issues/") && strings.Count(strings.TrimPrefix(p, orgPrefix+"issues/"), "/") == 1:
		id := strings.TrimSuffix(strings.TrimPrefix(p, orgPrefix+"issues/"), "/")
		s.count("issue", nil, false)
		s.mu2.Lock()
		i, ok := s.issues[id]
		var out map[string]any
		if ok {
			out = s.issueJSON(i)
		}
		s.mu2.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"detail": "The requested resource does not exist"})
			return true
		}
		writeJSON(w, 200, out)
	case r.Method == "GET" && strings.HasPrefix(p, projPrefix) && strings.Contains(p, "/events/") && strings.Contains(p, "/attachments/"):
		// /api/0/projects/{org}/{project}/events/{event}/attachments/[{id}/]
		rest := strings.Split(strings.Trim(strings.TrimPrefix(p, projPrefix), "/"), "/")
		if len(rest) < 4 || rest[1] != "events" || rest[3] != "attachments" {
			return false
		}
		issue := strings.TrimPrefix(rest[2], "ev-")
		s.ex.mu.Lock()
		atts := s.ex.attachments[issue]
		s.ex.mu.Unlock()
		if len(rest) == 4 {
			s.count("event_attachments", nil, false)
			out := []any{}
			for _, a := range atts {
				out = append(out, map[string]any{"id": a.ID, "event_id": rest[2], "type": "event.attachment", "name": a.Name, "mimetype": a.Mime, "size": len(a.Data),
					"dateCreated": time.Now().UTC().Format(time.RFC3339), "headers": map[string]any{"Content-Type": a.Mime}, "sha1": nil})
			}
			writeJSON(w, 200, out)
			return true
		}
		s.count("attachment_download", nil, false)
		for _, a := range atts {
			if a.ID == rest[4] && r.URL.Query().Get("download") != "" {
				w.Header().Set("Content-Type", a.Mime)
				_, _ = w.Write(a.Data)
				return true
			}
		}
		writeJSON(w, 404, map[string]any{"detail": "The requested resource does not exist"})
	default:
		return false
	}
	return true
}

// ---- webhook signing, as each vendor's documentation describes it ----

func hmacHex(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// LinearWebhook builds a Linear webhook delivery: the body (with webhookTimestamp in ms) and its headers (Linear-Signature,
// Linear-Delivery, Linear-Event), signed with the webhook's signing secret.
func LinearWebhook(secret, delivery, typ, action string, data map[string]any, at time.Time) ([]byte, http.Header) {
	b, _ := json.Marshal(map[string]any{"action": action, "type": typ, "data": data, "url": "https://linear.app/fixture", "webhookId": "wh-1",
		"webhookTimestamp": at.UnixMilli(), "organizationId": "org-1", "createdAt": at.UTC().Format(time.RFC3339Nano)})
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Linear-Signature", hmacHex(secret, b))
	h.Set("Linear-Delivery", delivery)
	h.Set("Linear-Event", typ)
	return b, h
}

// SentryWebhook builds a Sentry integration-platform webhook delivery: Sentry-Hook-Resource, Sentry-Hook-Timestamp (seconds),
// Request-ID and Sentry-Hook-Signature (hex HMAC-SHA256 with the client secret).
func SentryWebhook(secret, requestID, resource, action string, data map[string]any, at time.Time) ([]byte, http.Header) {
	b, _ := json.Marshal(map[string]any{"action": action, "installation": map[string]any{"uuid": "inst-1"}, "data": data, "actor": map[string]any{"type": "application", "id": "sentry", "name": "Sentry"}})
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Request-ID", requestID)
	h.Set("Sentry-Hook-Resource", resource)
	h.Set("Sentry-Hook-Timestamp", strconv.FormatInt(at.Unix(), 10))
	h.Set("Sentry-Hook-Signature", hmacHex(secret, b))
	return b, h
}

// NotionWebhook builds a Notion webhook event (id, timestamp, type, entity) signed as X-Notion-Signature: sha256=<hex HMAC-SHA256
// keyed with the verification token>. As remembered from Notion's documentation; unverified.
func NotionWebhook(token, eventID, typ string, entity map[string]any, at time.Time) ([]byte, http.Header) {
	b, _ := json.Marshal(map[string]any{"id": eventID, "timestamp": at.UTC().Format(time.RFC3339Nano), "workspace_id": "ws-1", "subscription_id": "sub-1", "integration_id": "int-1",
		"type": typ, "entity": entity, "data": map[string]any{}})
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-Notion-Signature", "sha256="+hmacHex(token, b))
	return b, h
}
