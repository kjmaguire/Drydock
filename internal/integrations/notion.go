package integrations

import (
	"context"
	"fmt"
	"strings"
)

// notionAdapter reads and writes Notion through its hosted MCP server (https://mcp.notion.com/mcp).
//
// The tool names notion-search, notion-fetch, notion-create-pages and notion-create-comment are the ones the hosted server
// offers to the development session that built this slice (so the names are observed, not guessed). UNVERIFIED: their
// argument and result shapes (query_type, page_size, start_cursor, the results[] fields, the create-pages payload) and whether the
// server returns pages newest-first. The sync does not rely on an order: it only stops early when a page proves to be sorted.
// Notion's MCP server wants an interactive OAuth with PKCE; this slice has token entry only (see docs/integrations.md
// "As built"), so whether a pasted integration token is accepted is unverified.
type notionAdapter struct{}

func (notionAdapter) Kind() string { return KindNotion }

func (notionAdapter) Needs() []ToolNeed {
	return []ToolNeed{
		{Role: "list", Candidates: []string{"notion-search", "search"}, Required: true},
		{Role: "fetch", Candidates: []string{"notion-fetch", "fetch"}, Required: true},
		{Role: "create_page", Candidates: []string{"notion-create-pages", "create-pages"}, Write: true},
		{Role: "comment", Candidates: []string{"notion-create-comment", "create-comment"}, Write: true},
	}
}

func (notionAdapter) FullSweep() bool { return false }

func (notionAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "create_page", Label: "Create a page", Role: "create_page", Params: []string{"title", "content"}, Description: "Creates a new Notion page (under the target page when one is given)."},
		{Op: "comment", Label: "Comment on a page", Role: "comment", Params: []string{"body"}, Description: "Adds a comment to a Notion page."},
	}
}

func (notionAdapter) Page(ctx context.Context, cl *Client, after string, limit int) (Page, error) {
	args := map[string]any{"query": cl.Conn.Config["query"], "query_type": "internal", "page_size": limit}
	if after != "" {
		args["start_cursor"] = after
	}
	res, err := cl.call(ctx, "list", args)
	if err != nil {
		return Page{}, err
	}
	v, derr := decode(res)
	if derr != nil {
		return Page{}, schemaDrift(KindNotion, derr)
	}
	raws, next, ok := listItems(v, "results", "pages", "items")
	if !ok {
		return Page{}, schemaDrift(KindNotion, fmt.Errorf("no result list found"))
	}
	var pg Page
	pg.Next = next
	for _, r := range raws {
		m := asMap(r)
		if m == nil {
			continue
		}
		id := str(m["id"])
		if id == "" {
			continue
		}
		if t := strings.ToLower(str(m["type"])); t == "user" {
			continue
		}
		edited := str(pick(m, "last_edited_time", "lastEditedTime", "timestamp", "updated_at"))
		pg.Items = append(pg.Items, RawItem{
			ExternalID: id, Kind: ItemPage, URL: str(m["url"]), Title: str(pick(m, "title", "name")), Body: str(pick(m, "highlight", "text", "excerpt")),
			State: str(m["type"]), Created: millis(pick(m, "created_time", "createdTime")), Updated: millis(edited), Etag: edited,
			Parent: str(pick(m, "parent_id", "parent")), NeedsBody: true,
			Meta: map[string]any{"type": str(m["type"])},
		})
	}
	return pg, nil
}

func (notionAdapter) Detail(ctx context.Context, cl *Client, id string) (string, bool, error) {
	res, err := cl.call(ctx, "fetch", map[string]any{"id": id})
	if err != nil {
		return "", false, err
	}
	if v, derr := decode(res); derr == nil {
		if m := asMap(v); m != nil {
			if t := str(pick(m, "text", "content", "markdown", "body")); t != "" {
				return t, true, nil
			}
		}
	}
	if strings.TrimSpace(res.Text) == "" {
		return "", false, nil
	}
	return res.Text, true, nil // the server returned page content as plain text or Markdown
}

func (notionAdapter) TaskMapping(it Item) TaskMapping { return TaskMapping{Label: "requirements"} }

func (notionAdapter) Build(op, target string, p map[string]string, _ map[string]string) (OutCall, error) {
	switch op {
	case "create_page":
		title := strings.TrimSpace(p["title"])
		if title == "" {
			return OutCall{}, errf(ErrBadRequest, "a page needs a title")
		}
		page := map[string]any{"properties": map[string]any{"title": title}, "content": p["content"]}
		args := map[string]any{"pages": []any{page}}
		where := "at the top level of the workspace"
		if target != "" {
			args["parent"] = map[string]any{"page_id": target}
			where = "under page " + target
		}
		return OutCall{Role: "create_page", Args: args, Preview: fmt.Sprintf("Create a Notion page %q %s:\n%s", title, where, Quote(p["content"]))}, nil
	case "comment":
		body := strings.TrimSpace(p["body"])
		if target == "" || body == "" {
			return OutCall{}, errf(ErrBadRequest, "a comment needs a page and text")
		}
		return OutCall{Role: "comment", Args: map[string]any{"page_id": target, "rich_text": []any{map[string]any{"text": map[string]any{"content": body}}}},
			Preview: fmt.Sprintf("Comment on Notion page %s:\n%s", target, Quote(body))}, nil
	}
	return OutCall{}, errf(ErrOpUnsupported, "Notion has no operation %q", op)
}
