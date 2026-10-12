package integrations

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// NotionVersion is the Notion-Version header Drydock sends. Notion's versions are dated; 2026-03-11 is the latest (its upgrade
// guide renames archived to in_trash, replaces the flat "after" of Append Block Children with a position object, and renames
// the transcription block to meeting_notes). Databases became containers of data sources in 2025-09-03: a data source is
// queried with POST /v1/data_sources/{id}/query and found with POST /v1/search filtered to object "data_source".
const NotionVersion = "2026-03-11"

// notionAdapter reads and writes Notion through its REST API (https://api.notion.com/v1). The official SDK is JavaScript
// (@notionhq/client, makenotion/notion-sdk-js); there is no official Go SDK, so this is a plain HTTP client following that SDK's
// endpoint definitions (src/api-endpoints: search, data-sources query, pages/{id}/markdown, comments, pages, users/me).
//
// Auth: the internal integration secret (ntn_...) as "Authorization: Bearer <secret>". The integration sees only the pages and
// data sources a person shared with it: that sharing is the permission boundary.
type notionAdapter struct{}

func (notionAdapter) Kind() string { return KindNotion }

func (notionAdapter) FullSweep() bool { return false }

func (notionAdapter) Endpoints() []string {
	return []string{"POST /v1/data_sources/{id}/query (sorted by last_edited_time, filtered after the watermark)", "POST /v1/search (pages, sorted by last_edited_time)", "GET /v1/pages/{id}/markdown"}
}

func (notionAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "create_page", Label: "Create a page", Params: []string{"title", "content"}, Description: "Creates a Notion page under the target page (POST /v1/pages, Markdown content)."},
		{Op: "comment", Label: "Comment on a page", Params: []string{"body"}, Description: "Adds a comment to a Notion page (POST /v1/comments)."},
	}
}

func (notionAdapter) Identity(ctx context.Context, a *API) (Identity, error) {
	var me map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/v1/users/me", nil, nil, &me); err != nil {
		return Identity{}, err
	}
	if str(me["object"]) != "user" {
		return Identity{}, schemaDrift(KindNotion, fmt.Errorf("users/me is not a user"))
	}
	id := Identity{Account: str(me["name"])}
	if bot := asMap(me["bot"]); bot != nil {
		id.Workspace = str(bot["workspace_name"])
		if own := asMap(bot["owner"]); str(own["type"]) == "workspace" {
			id.Method = "Internal integration"
		}
	}
	return id, nil
}

// notionTitle reads a page's title from its properties (the one property of type "title").
func notionTitle(page map[string]any) string {
	for _, v := range asMap(page["properties"]) {
		p := asMap(v)
		if str(p["type"]) != "title" {
			continue
		}
		var b strings.Builder
		for _, t := range asList(p["title"]) {
			b.WriteString(str(asMap(t)["plain_text"]))
		}
		return b.String()
	}
	// a data source carries its title at the top level
	var b strings.Builder
	for _, t := range asList(page["title"]) {
		b.WriteString(str(asMap(t)["plain_text"]))
	}
	return b.String()
}

func (notionAdapter) Choices(ctx context.Context, a *API, key string, _ map[string]string) ([]Choice, error) {
	if key != "data_source" {
		return nil, errf(ErrBadRequest, "Notion has no setting %q", key)
	}
	out := []Choice{}
	cursor := ""
	for i := 0; i < 5; i++ {
		body := map[string]any{"filter": map[string]any{"property": "object", "value": "data_source"}, "page_size": 100}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		var res map[string]any
		if _, err := a.HTTP.json(ctx, "POST", "/v1/search", nil, body, &res); err != nil {
			return nil, err
		}
		for _, r := range asList(res["results"]) {
			m := asMap(r)
			if id := str(m["id"]); id != "" {
				out = append(out, Choice{Value: id, Label: firstNonEmpty(notionTitle(m), id)})
			}
		}
		if hm, _ := res["has_more"].(bool); !hm {
			break
		}
		cursor = str(res["next_cursor"])
	}
	return out, nil
}

func (notionAdapter) Page(ctx context.Context, a *API, q PageQuery) (Page, error) {
	limit := q.Limit
	if limit > 100 { // Notion's page_size maximum
		limit = 100
	}
	var res map[string]any
	if ds := q.Config["data_source"]; ds != "" {
		body := map[string]any{"page_size": limit, "sorts": []any{map[string]any{"timestamp": "last_edited_time", "direction": "descending"}}, "result_type": "page"}
		if q.Since > 0 {
			body["filter"] = map[string]any{"timestamp": "last_edited_time", "last_edited_time": map[string]any{"on_or_after": time.UnixMilli(q.Since).UTC().Format(time.RFC3339)}}
		}
		if q.After != "" {
			body["start_cursor"] = q.After
		}
		if _, err := a.HTTP.json(ctx, "POST", "/v1/data_sources/"+url.PathEscape(ds)+"/query", nil, body, &res); err != nil {
			return Page{}, err
		}
	} else {
		body := map[string]any{"page_size": limit, "filter": map[string]any{"property": "object", "value": "page"},
			"sort": map[string]any{"timestamp": "last_edited_time", "direction": "descending"}}
		if s := q.Config["query"]; s != "" {
			body["query"] = s
		}
		if q.After != "" {
			body["start_cursor"] = q.After
		}
		if _, err := a.HTTP.json(ctx, "POST", "/v1/search", nil, body, &res); err != nil {
			return Page{}, err
		}
	}
	results, ok := res["results"].([]any)
	if !ok {
		return Page{}, schemaDrift(KindNotion, fmt.Errorf("no results list"))
	}
	var pg Page
	if hm, _ := res["has_more"].(bool); hm {
		pg.Next = str(res["next_cursor"])
	}
	for _, r := range results {
		m := asMap(r)
		id := str(m["id"])
		if id == "" || str(m["object"]) != "page" {
			continue
		}
		state := "page"
		if t, _ := m["in_trash"].(bool); t {
			state = "in_trash"
		} else if t, _ := m["archived"].(bool); t { // the name before 2026-03-11
			state = "in_trash"
		}
		edited := str(m["last_edited_time"])
		parent := ""
		if p := asMap(m["parent"]); p != nil {
			parent = str(pick(p, "page_id", "data_source_id", "database_id"))
		}
		pg.Items = append(pg.Items, RawItem{
			ExternalID: id, Kind: ItemPage, URL: str(m["url"]), Title: notionTitle(m), State: state,
			Created: millis(m["created_time"]), Updated: millis(edited), Etag: edited, Parent: parent, NeedsBody: true,
			Meta: map[string]any{"type": "page"},
		})
	}
	return pg, nil
}

// Detail reads a page as Markdown (GET /v1/pages/{id}/markdown).
func (notionAdapter) Detail(ctx context.Context, a *API, _ map[string]string, id string) (string, bool, error) {
	var res map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/v1/pages/"+url.PathEscape(id)+"/markdown", nil, nil, &res); err != nil {
		return "", false, err
	}
	md := str(res["markdown"])
	if t, _ := res["truncated"].(bool); t && md != "" {
		md += "\n\n[Notion truncated this page]"
	}
	return md, strings.TrimSpace(md) != "", nil
}

func (notionAdapter) TaskMapping(it Item) TaskMapping { return TaskMapping{Label: "requirements"} }

func (notionAdapter) Build(op, target string, p map[string]string, _ map[string]string) (OutCall, error) {
	switch op {
	case "create_page":
		title := strings.TrimSpace(p["title"])
		if title == "" {
			return OutCall{}, errf(ErrBadRequest, "a page needs a title")
		}
		if target == "" {
			return OutCall{}, errf(ErrBadRequest, "a Notion page needs a parent page (the target) that is shared with the integration")
		}
		args := map[string]any{"parent": map[string]any{"page_id": target},
			"properties": map[string]any{"title": map[string]any{"title": []any{map[string]any{"text": map[string]any{"content": title}}}}}}
		if c := p["content"]; strings.TrimSpace(c) != "" {
			args["markdown"] = c
		}
		return OutCall{Endpoint: "POST /v1/pages", Args: args, Preview: fmt.Sprintf("Create a Notion page %q under page %s:\n%s", title, target, Quote(p["content"]))}, nil
	case "comment":
		body := strings.TrimSpace(p["body"])
		if target == "" || body == "" {
			return OutCall{}, errf(ErrBadRequest, "a comment needs a page and text")
		}
		return OutCall{Endpoint: "POST /v1/comments", Args: map[string]any{"parent": map[string]any{"page_id": target}, "rich_text": []any{map[string]any{"text": map[string]any{"content": body}}}},
			Preview: fmt.Sprintf("Comment on Notion page %s:\n%s", target, Quote(body))}, nil
	}
	return OutCall{}, errf(ErrOpUnsupported, "Notion has no operation %q", op)
}

func (notionAdapter) Send(ctx context.Context, a *API, call OutCall) (string, error) {
	var path string
	switch call.Endpoint {
	case "POST /v1/pages":
		path = "/v1/pages"
	case "POST /v1/comments":
		path = "/v1/comments"
	default:
		return "", errf(ErrOpUnsupported, "unknown Notion call %q", call.Endpoint)
	}
	var res map[string]any
	if _, err := a.HTTP.json(ctx, "POST", path, nil, call.Args, &res); err != nil {
		return "", err
	}
	return str(res["object"]) + " " + str(res["id"]), nil
}
