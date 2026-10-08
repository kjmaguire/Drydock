package integrations

import (
	"context"
	"fmt"
	"strings"
)

// aikidoAdapter reads and writes Aikido through the user-installed stdio server @aikidosec/mcp (AGPL or commercial, never
// bundled: D14). The tool names come from the published package's tool list (aikido_issues_list, aikido_ignore_issue,
// aikido_unignore_issue, aikido_snooze_issue, aikido_unsnooze_issue, aikido_workspaces_list). UNVERIFIED: their arguments and the
// finding fields (no payload was observed in Phase 0): the parser accepts the REST export field names it expects
// (id, severity, type, rule, affected_package, location, repo, status, first_detected_at, updated_at).
//
// Aikido has no cursor evidence, so every sync lists the findings and diffs them by content hash (FullSweep).
type aikidoAdapter struct{}

func (aikidoAdapter) Kind() string { return KindAikido }

func (aikidoAdapter) Needs() []ToolNeed {
	return []ToolNeed{
		{Role: "list", Candidates: []string{"aikido_issues_list"}, Required: true},
		{Role: "ignore", Candidates: []string{"aikido_ignore_issue"}, Write: true},
		{Role: "unignore", Candidates: []string{"aikido_unignore_issue"}, Write: true},
		{Role: "snooze", Candidates: []string{"aikido_snooze_issue"}, Write: true},
		{Role: "unsnooze", Candidates: []string{"aikido_unsnooze_issue"}, Write: true},
	}
}

func (aikidoAdapter) FullSweep() bool { return true }

func (aikidoAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "ignore", Label: "Ignore the finding", Role: "ignore", Params: []string{"reason"}, NeverAuto: true, Description: "Suppresses a security finding in Aikido."},
		{Op: "unignore", Label: "Stop ignoring the finding", Role: "unignore", Params: nil, NeverAuto: true, Description: "Brings an ignored finding back."},
		{Op: "snooze", Label: "Snooze the finding", Role: "snooze", Params: []string{"until", "reason"}, NeverAuto: true, Description: "Hides a security finding until a date."},
		{Op: "unsnooze", Label: "Stop snoozing the finding", Role: "unsnooze", Params: nil, NeverAuto: true, Description: "Brings a snoozed finding back."},
	}
}

func severityWord(v any) string {
	if n, ok := num(v); ok {
		switch {
		case n >= 90:
			return "critical"
		case n >= 70:
			return "high"
		case n >= 40:
			return "medium"
		}
		return "low"
	}
	switch w := strings.ToLower(strings.TrimSpace(str(v))); w {
	case "critical", "high", "medium", "low":
		return w
	case "moderate":
		return "medium"
	case "info", "informational":
		return "low"
	default:
		return w
	}
}

func (aikidoAdapter) Page(ctx context.Context, cl *Client, after string, limit int) (Page, error) {
	args := map[string]any{"status": "open"}
	if w := cl.Conn.Config["workspace"]; w != "" {
		args["workspaceId"] = w
	}
	if r := cl.Conn.Config["repo"]; r != "" {
		args["repo"] = r
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
		return Page{}, schemaDrift(KindAikido, derr)
	}
	raws, next, ok := listItems(v, "issues", "findings", "items", "results")
	if !ok {
		return Page{}, schemaDrift(KindAikido, fmt.Errorf("no finding list found in the result"))
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
		sev := severityWord(pick(m, "severity", "severity_score"))
		loc := ""
		if lm := asMap(m["location"]); lm != nil {
			loc = str(lm["file"])
			if ln := str(lm["line"]); ln != "" {
				loc += ":" + ln
			}
		} else {
			loc = str(pick(m, "file", "path", "location"))
		}
		typ := str(pick(m, "type", "issue_type"))
		title := str(pick(m, "rule", "title", "summary", "name"))
		if title == "" {
			title = typ + " finding"
		}
		var body strings.Builder
		if d := str(pick(m, "description", "details", "summary")); d != "" {
			body.WriteString(d + "\n")
		}
		if pkg := str(pick(m, "affected_package", "package")); pkg != "" {
			body.WriteString("Package: " + pkg + "\n")
		}
		if cve := str(pick(m, "cve_id", "cve")); cve != "" {
			body.WriteString("CVE: " + cve + "\n")
		}
		if loc != "" {
			body.WriteString("Location: " + loc + "\n")
		}
		if fix := str(pick(m, "remediation", "fix", "fixed_version")); fix != "" {
			body.WriteString("Suggested fix: " + fix + "\n")
		}
		updated := str(pick(m, "updated_at", "last_detected_at", "first_detected_at"))
		pg.Items = append(pg.Items, RawItem{
			ExternalID: id, Kind: ItemFinding, IssueRef: "AIK-" + id, URL: str(pick(m, "url", "link")), Title: title, Body: body.String(),
			State: str(m["status"]), Labels: nonEmpty(typ), Priority: sev,
			Created: millis(pick(m, "first_detected_at", "created_at")), Updated: millis(updated), Etag: updated,
			Meta: map[string]any{"severity": sev, "type": typ, "repo": str(pick(m, "repo", "code_repo_name", "repository")), "location": loc},
		})
	}
	return pg, nil
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func (aikidoAdapter) Detail(context.Context, *Client, string) (string, bool, error) {
	return "", false, nil
}

func (aikidoAdapter) TaskMapping(it Item) TaskMapping {
	sev, _ := it.Meta["severity"].(string)
	return TaskMapping{Priority: priorityTask(sev), Label: "security"}
}

func (aikidoAdapter) Build(op, target string, p map[string]string, _ map[string]string) (OutCall, error) {
	if target == "" {
		return OutCall{}, errf(ErrBadRequest, "an Aikido write needs the finding id as its target")
	}
	switch op {
	case "ignore":
		reason := strings.TrimSpace(p["reason"])
		if reason == "" {
			return OutCall{}, errf(ErrBadRequest, "ignoring a finding needs a reason (it suppresses a security signal)")
		}
		return OutCall{Role: "ignore", Args: map[string]any{"issueId": target, "reason": reason}, Preview: fmt.Sprintf("IGNORE Aikido finding %s (suppresses a security signal). Reason:\n%s", target, Quote(reason))}, nil
	case "snooze":
		until, reason := strings.TrimSpace(p["until"]), strings.TrimSpace(p["reason"])
		if until == "" || reason == "" {
			return OutCall{}, errf(ErrBadRequest, "snoozing a finding needs a date (until) and a reason")
		}
		return OutCall{Role: "snooze", Args: map[string]any{"issueId": target, "until": until, "reason": reason}, Preview: fmt.Sprintf("SNOOZE Aikido finding %s until %s. Reason:\n%s", target, until, Quote(reason))}, nil
	case "unignore":
		return OutCall{Role: "unignore", Args: map[string]any{"issueId": target}, Preview: "Stop ignoring Aikido finding " + target}, nil
	case "unsnooze":
		return OutCall{Role: "unsnooze", Args: map[string]any{"issueId": target}, Preview: "Stop snoozing Aikido finding " + target}, nil
	}
	return OutCall{}, errf(ErrOpUnsupported, "Aikido has no operation %q", op)
}
