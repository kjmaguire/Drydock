package integrations

import (
	"context"
	"fmt"
	"strings"
)

// sentryAdapter reads and writes Sentry through its hosted MCP server (https://mcp.sentry.dev/mcp, Authorization:
// Sentry-Bearer <token>).
//
// UNVERIFIED: the tool names (list_issues, get_issue_details, update_issue) and the argument and result shapes are the shapes the
// server is reported to use; the natural-language search tool (search_issues) needs an LLM key on the server side and is only a
// fallback candidate. The issue fields (shortId, level, status, count as a string, userCount, firstSeen, lastSeen, permalink,
// project{slug}) are the public Sentry REST issue fields, which the server is expected to pass through.
type sentryAdapter struct{}

func (sentryAdapter) Kind() string { return KindSentry }

func (sentryAdapter) Needs() []ToolNeed {
	return []ToolNeed{
		{Role: "list", Candidates: []string{"list_issues", "search_issues"}, Required: true},
		{Role: "detail", Candidates: []string{"get_issue_details", "get_issue"}},
		{Role: "update", Candidates: []string{"update_issue"}, Write: true},
	}
}

func (sentryAdapter) FullSweep() bool { return false }

func (sentryAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "resolve", Label: "Resolve the issue", Role: "update", Params: nil, Description: "Marks the Sentry issue resolved."},
		{Op: "ignore", Label: "Ignore the issue", Role: "update", Params: nil, NeverAuto: true, Description: "Marks the Sentry issue ignored (it stops alerting)."},
	}
}

func (sentryAdapter) Page(ctx context.Context, cl *Client, after string, limit int) (Page, error) {
	q := cl.Conn.Config["query"]
	if q == "" {
		q = "is:unresolved"
	}
	args := map[string]any{"query": q, "sort": "date", "limit": limit}
	if o := cl.Conn.Config["organization"]; o != "" {
		args["organizationSlug"] = o
	}
	if p := cl.Conn.Config["project"]; p != "" {
		args["projectSlugOrId"] = p
	}
	if after != "" {
		args["cursor"] = after
	}
	res, err := cl.call(ctx, "list", args)
	if err != nil {
		return Page{}, err
	}
	v, derr := decode(res)
	if derr != nil {
		return Page{}, schemaDrift(KindSentry, derr)
	}
	raws, next, ok := listItems(v, "issues", "results", "items")
	if !ok {
		return Page{}, schemaDrift(KindSentry, fmt.Errorf("no issue list found in the result"))
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
			id = str(m["shortId"])
		}
		if id == "" {
			continue
		}
		events, _ := num(m["count"])
		users, _ := num(pick(m, "userCount", "user_count"))
		level := strings.ToLower(str(m["level"]))
		last := str(pick(m, "lastSeen", "last_seen"))
		proj := slugOf(m["project"])
		meta := map[string]any{"level": level, "events": events, "users": users, "project": proj, "culprit": str(m["culprit"]),
			"environment": str(m["environment"]), "firstSeen": str(pick(m, "firstSeen", "first_seen")), "lastSeen": last}
		short := str(pick(m, "shortId", "short_id"))
		if short == "" {
			short = "SEN-" + shortID(id)
		}
		body := str(m["culprit"])
		if md := asMap(m["metadata"]); md != nil {
			body = strings.TrimSpace(str(md["type"]) + ": " + str(md["value"]) + "\n" + body)
		}
		pg.Items = append(pg.Items, RawItem{
			ExternalID: id, Kind: ItemErrorEvent, IssueRef: short, URL: str(pick(m, "permalink", "url")), Title: str(m["title"]), Body: body,
			State: str(m["status"]), Assignee: str(pick(m, "assignedTo", "assigned_to")), Priority: level,
			Created: millis(pick(m, "firstSeen", "first_seen")), Updated: millis(last), Etag: last, Meta: meta,
		})
	}
	return pg, nil
}

// Detail fetches the issue's details for its stack trace. It is called only for an item that is about to become a task.
func (sentryAdapter) Detail(ctx context.Context, cl *Client, id string) (string, bool, error) {
	if cl.Tools["detail"] == "" {
		return "", false, nil
	}
	args := map[string]any{"issueId": id}
	if o := cl.Conn.Config["organization"]; o != "" {
		args["organizationSlug"] = o
	}
	res, err := cl.call(ctx, "detail", args)
	if err != nil {
		return "", false, err
	}
	if v, derr := decode(res); derr == nil {
		if m := asMap(v); m != nil {
			if t := findStack(m); t != "" {
				return t, true, nil
			}
			if t := str(pick(m, "text", "content", "details")); t != "" {
				return t, true, nil
			}
		}
	}
	return res.Text, strings.TrimSpace(res.Text) != "", nil
}

// findStack looks for a stack trace string anywhere in a decoded detail object.
func findStack(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range []string{"stacktrace", "stackTrace", "stack_trace", "traceback"} {
			if s, ok := x[k].(string); ok && s != "" {
				return s
			}
		}
		for _, e := range x {
			if s := findStack(e); s != "" {
				return s
			}
		}
	case []any:
		for _, e := range x {
			if s := findStack(e); s != "" {
				return s
			}
		}
	}
	return ""
}

func (sentryAdapter) TaskMapping(it Item) TaskMapping {
	lvl, _ := it.Meta["level"].(string)
	return TaskMapping{Priority: priorityTask(lvl), Label: "bug"}
}

func (sentryAdapter) Build(op, target string, _ map[string]string, cfg map[string]string) (OutCall, error) {
	if target == "" {
		return OutCall{}, errf(ErrBadRequest, "a Sentry write needs the issue id as its target")
	}
	status := map[string]string{"resolve": "resolved", "ignore": "ignored"}[op]
	if status == "" {
		return OutCall{}, errf(ErrOpUnsupported, "Sentry has no operation %q (comments are not supported through the MCP server)", op)
	}
	args := map[string]any{"issueId": target, "status": status}
	if o := cfg["organization"]; o != "" {
		args["organizationSlug"] = o
	}
	return OutCall{Role: "update", Args: args, Preview: fmt.Sprintf("Set Sentry issue %s to %s", target, status)}, nil
}

// slugOf renders a project reference: its slug when it is an object, else the string itself.
func slugOf(v any) string {
	if m := asMap(v); m != nil {
		if s := str(m["slug"]); s != "" {
			return s
		}
	}
	return str(v)
}
