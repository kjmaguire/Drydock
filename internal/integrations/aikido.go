package integrations

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AikidoRegions are Aikido's regional hosts (the same four the official @aikidosec/mcp package knows).
var AikidoRegions = map[string]string{
	"eu": "https://app.aikido.dev",
	"us": "https://app.us.aikido.dev",
	"me": "https://app.me.aikido.dev",
	"au": "https://app.au.aikido.dev",
}

// aikidoAdapter reads and writes Aikido through its public REST API (<region host>/api/public/v1). Aikido publishes no SDK in
// any language; the API is OAuth 2.0 client credentials only: POST <host>/api/oauth/token with grant_type=client_credentials
// and HTTP Basic client id and secret, which a workspace admin creates under Integrations > Public REST API with scopes such as
// issues:read. The token is cached until it expires and fetched again on a 401 (auth.go).
//
// UNVERIFIED (apidocs.aikido.dev could not be read from this environment; the paths come from search snippets of that site and
// the issue fields from the export as a community client reads it): GET /issues/export (format=json, filter_status,
// filter_code_repo_id; everything at once, no cursor), GET /repositories/code, GET /workspace, and PUT /issues/{id}/ignore,
// /unignore, /snooze (snooze_until, a Unix time) and /unsnooze. The parser is permissive and a shape it does not know degrades
// the connection with schema drift instead of guessing.
type aikidoAdapter struct{}

func (aikidoAdapter) Kind() string { return KindAikido }

// FullSweep: the export has no cursor, so every sync lists the open findings and the missing ones become gone.
func (aikidoAdapter) FullSweep() bool { return true }

func (aikidoAdapter) Endpoints() []string {
	return []string{"GET /api/public/v1/issues/export?format=json&filter_status=open[&filter_code_repo_id=]"}
}

func (aikidoAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "ignore", Label: "Ignore the finding", Params: []string{"reason"}, NeverAuto: true, Description: "Suppresses a security finding in Aikido."},
		{Op: "unignore", Label: "Stop ignoring the finding", Params: nil, NeverAuto: true, Description: "Brings an ignored finding back."},
		{Op: "snooze", Label: "Snooze the finding", Params: []string{"until", "reason"}, NeverAuto: true, Description: "Hides a security finding until a date (YYYY-MM-DD)."},
		{Op: "unsnooze", Label: "Stop snoozing the finding", Params: nil, NeverAuto: true, Description: "Brings a snoozed finding back."},
	}
}

func (aikidoAdapter) Identity(ctx context.Context, a *API) (Identity, error) {
	var w map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/api/public/v1/workspace", nil, nil, &w); err != nil {
		return Identity{}, err
	}
	name := str(pick(w, "name", "workspace_name", "workspaceName"))
	if name == "" {
		if inner := asMap(w["workspace"]); inner != nil {
			name = str(inner["name"])
		}
	}
	return Identity{Workspace: name}, nil
}

func (aikidoAdapter) Choices(ctx context.Context, a *API, key string, _ map[string]string) ([]Choice, error) {
	if key != "repo" {
		return nil, errf(ErrBadRequest, "Aikido has no setting %q", key)
	}
	out := []Choice{}
	for page := 0; page < 10; page++ {
		var list []any
		if _, err := a.HTTP.json(ctx, "GET", "/api/public/v1/repositories/code", url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}, nil, &list); err != nil {
			return nil, err
		}
		for _, e := range list {
			m := asMap(e)
			id := str(m["id"])
			if id != "" {
				out = append(out, Choice{Value: id, Label: firstNonEmpty(str(m["name"]), str(m["full_name"]), id)})
			}
		}
		if len(list) < 100 {
			break
		}
	}
	return out, nil
}

func severityWord(v any) string {
	switch w := strings.ToLower(strings.TrimSpace(str(v))); w {
	case "critical", "high", "medium", "low":
		return w
	case "moderate":
		return "medium"
	case "info", "informational":
		return "low"
	}
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
	return ""
}

func (aikidoAdapter) Page(ctx context.Context, a *API, q PageQuery) (Page, error) {
	v := url.Values{"format": {"json"}, "filter_status": {"open"}}
	if r := q.Config["repo"]; r != "" {
		if _, err := strconv.Atoi(r); err == nil {
			v.Set("filter_code_repo_id", r)
		} else {
			v.Set("filter_code_repo_name", r)
		}
	}
	var raw any
	if _, err := a.HTTP.json(ctx, "GET", "/api/public/v1/issues/export", v, nil, &raw); err != nil {
		return Page{}, err
	}
	list, ok := raw.([]any)
	if !ok {
		if m := asMap(raw); m != nil {
			list, ok = pick(m, "issues", "data").([]any)
		}
	}
	if !ok {
		return Page{}, schemaDrift(KindAikido, fmt.Errorf("the export is not a list of issues"))
	}
	var pg Page
	for _, e := range list {
		m := asMap(e)
		id := str(m["id"])
		if id == "" {
			continue
		}
		sev := severityWord(pick(m, "severity", "severity_score"))
		typ := str(pick(m, "type", "issue_type"))
		title := str(pick(m, "rule", "title", "summary"))
		if title == "" {
			title = typ + " finding"
		}
		loc := str(pick(m, "affected_file", "file"))
		if md := asMap(asMap(m["issue_type_metadata"])["sast"]); md != nil && loc != "" {
			if ln, ok := num(md["start_line"]); ok && ln > 0 {
				loc += ":" + strconv.FormatInt(ln, 10)
			}
		}
		var body strings.Builder
		if d := str(pick(m, "description", "details")); d != "" {
			body.WriteString(d + "\n")
		}
		if pkg := str(m["affected_package"]); pkg != "" {
			body.WriteString("Package: " + pkg + "\n")
		}
		if cve := str(m["cve_id"]); cve != "" {
			body.WriteString("CVE: " + cve + "\n")
		}
		if loc != "" {
			body.WriteString("Location: " + loc + "\n")
		}
		if os := asMap(asMap(m["issue_type_metadata"])["open_source"]); os != nil {
			var fixes []string
			for _, p := range asList(os["patched_versions"]) {
				fixes = append(fixes, str(p))
			}
			if len(fixes) > 0 {
				body.WriteString("Patched versions: " + strings.Join(fixes, ", ") + "\n")
			}
		}
		first := millis(m["first_detected_at"])
		updated := first
		for _, k := range []string{"updated_at", "ignored_at", "closed_at", "last_detected_at"} {
			if t := millis(m[k]); t > updated {
				updated = t
			}
		}
		repo := str(pick(m, "code_repo_name", "container_repo_name", "repo"))
		pg.Items = append(pg.Items, RawItem{
			ExternalID: id, Kind: ItemFinding, IssueRef: "AIK-" + id, URL: str(pick(m, "url", "link")), Title: title, Body: body.String(),
			State: str(m["status"]), Labels: nonEmpty(typ), Priority: sev, Created: first, Updated: updated,
			Etag: fmt.Sprintf("%s/%s/%d", str(m["status"]), str(m["severity_score"]), updated),
			Meta: map[string]any{"severity": sev, "type": typ, "repo": repo, "location": loc, "group": str(m["group_id"])},
		})
	}
	return pg, nil
}

func (aikidoAdapter) Detail(context.Context, *API, map[string]string, string) (string, bool, error) {
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
	ep := "PUT /api/public/v1/issues/{id}/" + op
	switch op {
	case "ignore":
		reason := strings.TrimSpace(p["reason"])
		if reason == "" {
			return OutCall{}, errf(ErrBadRequest, "ignoring a finding needs a reason (it suppresses a security signal)")
		}
		return OutCall{Endpoint: ep, Args: map[string]any{"issueId": target, "reason": reason}, Preview: fmt.Sprintf("IGNORE Aikido finding %s (suppresses a security signal). Reason:\n%s", target, Quote(reason))}, nil
	case "snooze":
		until, reason := strings.TrimSpace(p["until"]), strings.TrimSpace(p["reason"])
		if until == "" || reason == "" {
			return OutCall{}, errf(ErrBadRequest, "snoozing a finding needs a date (until, YYYY-MM-DD) and a reason")
		}
		t, err := time.Parse("2006-01-02", until)
		if err != nil {
			return OutCall{}, errf(ErrBadRequest, "until must be a date written YYYY-MM-DD")
		}
		return OutCall{Endpoint: ep, Args: map[string]any{"issueId": target, "snooze_until": t.UTC().Unix(), "reason": reason},
			Preview: fmt.Sprintf("SNOOZE Aikido finding %s until %s. Reason:\n%s", target, until, Quote(reason))}, nil
	case "unignore":
		return OutCall{Endpoint: ep, Args: map[string]any{"issueId": target}, Preview: "Stop ignoring Aikido finding " + target}, nil
	case "unsnooze":
		return OutCall{Endpoint: ep, Args: map[string]any{"issueId": target}, Preview: "Stop snoozing Aikido finding " + target}, nil
	}
	return OutCall{}, errf(ErrOpUnsupported, "Aikido has no operation %q", op)
}

func (aikidoAdapter) Send(ctx context.Context, a *API, call OutCall) (string, error) {
	op, ok := strings.CutPrefix(call.Endpoint, "PUT /api/public/v1/issues/{id}/")
	if !ok || (op != "ignore" && op != "unignore" && op != "snooze" && op != "unsnooze") {
		return "", errf(ErrOpUnsupported, "unknown Aikido call %q", call.Endpoint)
	}
	body := map[string]any{}
	for k, v := range call.Args {
		if k != "issueId" {
			body[k] = v
		}
	}
	if _, err := a.HTTP.json(ctx, "PUT", "/api/public/v1/issues/"+url.PathEscape(argString(call.Args, "issueId"))+"/"+op, nil, body, nil); err != nil {
		return "", err
	}
	return op + " " + argString(call.Args, "issueId"), nil
}
