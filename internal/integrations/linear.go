package integrations

import (
	"context"
	"fmt"
	"strings"
)

// linearAdapter reads and writes Linear through its hosted MCP server (https://mcp.linear.app/mcp, read-only endpoint
// /mcp/readonly; docs/integrations.md section 5).
//
// UNVERIFIED: Phase 0 could not reach Linear's documentation or MCP server. The tool names (list_issues, create_comment,
// update_issue) and the argument and result shapes below are the shapes Linear's MCP server is reported to use and the fixture
// reproduces; the parser is deliberately permissive (arrays or {issues|nodes|items}, priorities as numbers, words or
// {value,name}). Test connection reports which candidate names the real server offers.
type linearAdapter struct{}

func (linearAdapter) Kind() string { return KindLinear }

func (linearAdapter) Needs() []ToolNeed {
	return []ToolNeed{
		{Role: "list", Candidates: []string{"list_issues", "linear_list_issues", "search_issues"}, Required: true},
		{Role: "comment", Candidates: []string{"create_comment", "linear_create_comment", "save_comment"}, Write: true},
		{Role: "update", Candidates: []string{"update_issue", "save_issue", "linear_update_issue"}, Write: true},
	}
}

func (linearAdapter) FullSweep() bool { return false }

func (linearAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "comment", Label: "Comment on the issue", Role: "comment", Params: []string{"body"}, Description: "Posts a comment on the Linear issue."},
		{Op: "set_state", Label: "Change the issue status", Role: "update", Params: []string{"state"}, Description: "Moves the Linear issue to another workflow state."},
	}
}

func linearPriorityWord(v any) string {
	if m := asMap(v); m != nil {
		if n, ok := num(m["value"]); ok {
			return linearPriorityWord(n)
		}
		return strings.ToLower(str(m["name"]))
	}
	switch x := v.(type) {
	case int64:
		return [...]string{"none", "urgent", "high", "medium", "low"}[clampIdx(int(x), 5)]
	}
	if n, ok := num(v); ok {
		return linearPriorityWord(n)
	}
	switch w := strings.ToLower(strings.TrimSpace(str(v))); w {
	case "urgent", "high", "medium", "low", "none":
		return w
	case "no priority", "":
		return "none"
	default:
		return w
	}
}

func clampIdx(i, n int) int {
	if i < 0 || i >= n {
		return 0
	}
	return i
}

func (linearAdapter) Page(ctx context.Context, cl *Client, after string, limit int) (Page, error) {
	args := map[string]any{"orderBy": "updatedAt", "limit": limit}
	if after != "" {
		args["cursor"] = after
	}
	if t := cl.Conn.Config["team"]; t != "" {
		args["team"] = t
	}
	res, err := cl.call(ctx, "list", args)
	if err != nil {
		return Page{}, err
	}
	v, derr := decode(res)
	if derr != nil {
		return Page{}, schemaDrift(KindLinear, derr)
	}
	raws, next, ok := listItems(v, "issues", "nodes", "items", "results")
	if !ok {
		return Page{}, schemaDrift(KindLinear, fmt.Errorf("no issue list found in the result"))
	}
	var pg Page
	pg.Next = next
	for _, r := range raws {
		m := asMap(r)
		if m == nil {
			continue
		}
		id := str(pick(m, "id", "identifier"))
		if id == "" {
			continue
		}
		ident := str(m["identifier"])
		if ident == "" {
			ident = "LIN-" + shortID(id)
		}
		it := RawItem{
			ExternalID: id, Kind: ItemIssue, IssueRef: ident, URL: str(m["url"]), Title: str(m["title"]), Body: str(pick(m, "description", "body")),
			State: str(pick(m, "state", "status")), Labels: labelNames(m["labels"]), Assignee: str(pick(m, "assignee", "assigneeName")),
			Priority: linearPriorityWord(m["priority"]), Parent: str(pick(m, "parent", "parentId")),
			Created: millis(m["createdAt"]), Updated: millis(pick(m, "updatedAt", "updated_at")), Etag: str(pick(m, "updatedAt", "updated_at")),
			Meta: map[string]any{"team": str(pick(m, "team", "teamKey")), "project": str(m["project"])},
		}
		pg.Items = append(pg.Items, it)
	}
	return pg, nil
}

func (linearAdapter) Detail(context.Context, *Client, string) (string, bool, error) { return "", false, nil }

func (linearAdapter) TaskMapping(it Item) TaskMapping {
	return TaskMapping{Priority: priorityTask(it.Priority), Label: "issue"}
}

func (linearAdapter) Build(op, target string, p map[string]string, _ map[string]string) (OutCall, error) {
	if target == "" {
		return OutCall{}, errf(ErrBadRequest, "a Linear write needs the issue id as its target")
	}
	switch op {
	case "comment":
		body := strings.TrimSpace(p["body"])
		if body == "" {
			return OutCall{}, errf(ErrBadRequest, "a comment needs text")
		}
		return OutCall{Role: "comment", Args: map[string]any{"issueId": target, "body": body},
			Preview: fmt.Sprintf("Comment on Linear issue %s:\n%s", target, Quote(body))}, nil
	case "set_state":
		st := strings.TrimSpace(p["state"])
		if st == "" {
			return OutCall{}, errf(ErrBadRequest, "set_state needs the new status name")
		}
		return OutCall{Role: "update", Args: map[string]any{"id": target, "state": st},
			Preview: fmt.Sprintf("Move Linear issue %s to status %q", target, st)}, nil
	}
	return OutCall{}, errf(ErrOpUnsupported, "Linear has no operation %q", op)
}

func labelNames(v any) []string {
	var out []string
	for _, e := range asList(v) {
		if s := str(e); s != "" {
			out = append(out, s)
		}
	}
	if m := asMap(v); m != nil { // {"nodes":[{"name":..}]}
		out = append(out, labelNames(m["nodes"])...)
	}
	return out
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

func schemaDrift(kind string, cause error) *Failure {
	return &Failure{Code: CodeSchemaDrift, Cause: cause,
		Message: vendor(kind) + " returned data in a shape Drydock does not recognize (the server may have changed). Existing items are kept and the connection is marked degraded."}
}
