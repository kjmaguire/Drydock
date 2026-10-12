package integrations

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// sentryAdapter reads and writes Sentry through its web API, version v0 (https://sentry.io/api/0/; a self-hosted Sentry or a
// regional host is the connection's API URL). sentry-go is Sentry's SDK for reporting errors, not a client of this API, and
// there is no official Go API client, so this is a plain HTTP client following the API reference (getsentry/sentry-api-schema
// openapi-derefed.json, read for this change) and the docs pages auth, pagination, rate limits and permissions
// (getsentry/sentry-docs docs/api).
//
// Endpoints: GET /api/0/ (who the token is, with auth.scopes), GET /api/0/organizations/, GET
// /api/0/organizations/{org}/projects/, GET /api/0/organizations/{org}/issues/ (sort=date, query, project, limit at most 100,
// Link-header cursors), GET .../issues/{id}/events/latest/ (the stack trace), PUT .../issues/{id}/ (status). Auth: a user,
// organization or internal-integration token, or an OAuth access token, as "Authorization: Bearer <token>".
type sentryAdapter struct{}

func (sentryAdapter) Kind() string { return KindSentry }

func (sentryAdapter) FullSweep() bool { return false }

func (sentryAdapter) Endpoints() []string {
	return []string{"GET /api/0/organizations/{org}/issues/?project=&query=&sort=date&limit= (Link cursors)", "GET /api/0/organizations/{org}/issues/{id}/events/latest/ (on import)"}
}

func (sentryAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "resolve", Label: "Resolve the issue", Params: nil, Description: "Marks the Sentry issue resolved (PUT .../issues/{id}/ status=resolved)."},
		{Op: "ignore", Label: "Ignore the issue", Params: nil, NeverAuto: true, Description: "Marks the Sentry issue ignored: it stops alerting (status=ignored)."},
	}
}

func (sentryAdapter) Identity(ctx context.Context, a *API) (Identity, error) {
	var root map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/api/0/", nil, nil, &root); err != nil {
		return Identity{}, err
	}
	auth := asMap(root["auth"])
	user := asMap(root["user"])
	if auth == nil && user == nil {
		// GET /api/0/ answers {"auth": null, "user": null} to a request it did not authenticate
		return Identity{}, &Failure{Code: CodeUnauthorized, Message: "Sentry did not recognize the token. Reconnect Sentry on the Integrations page."}
	}
	id := Identity{Account: str(pick(user, "name", "username"))}
	for _, s := range asList(pick(auth, "scopes")) {
		if v := str(s); v != "" {
			id.Scopes = append(id.Scopes, v)
		}
	}
	var orgs []any
	if _, err := a.HTTP.json(ctx, "GET", "/api/0/organizations/", url.Values{"per_page": {"100"}}, nil, &orgs); err == nil {
		var names []string
		for _, o := range orgs {
			if n := str(asMap(o)["name"]); n != "" {
				names = append(names, n)
			}
		}
		id.Workspace = strings.Join(names, ", ")
		if len(names) > 3 {
			id.Workspace = strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
		}
	}
	return id, nil
}

func (sentryAdapter) Choices(ctx context.Context, a *API, key string, cfg map[string]string) ([]Choice, error) {
	var path string
	switch key {
	case "organization":
		path = "/api/0/organizations/"
	case "project":
		org := cfg["organization"]
		if org == "" {
			return nil, errf(ErrBadRequest, "choose the Sentry organization first")
		}
		path = "/api/0/organizations/" + url.PathEscape(org) + "/projects/"
	default:
		return nil, errf(ErrBadRequest, "Sentry has no setting %q", key)
	}
	var list []any
	if _, err := a.HTTP.json(ctx, "GET", path, url.Values{"per_page": {"100"}}, nil, &list); err != nil {
		return nil, err
	}
	out := []Choice{}
	for _, e := range list {
		m := asMap(e)
		if slug := str(m["slug"]); slug != "" {
			out = append(out, Choice{Value: slug, Label: firstNonEmpty(str(m["name"]), slug)})
		}
	}
	return out, nil
}

func (sentryAdapter) Page(ctx context.Context, a *API, q PageQuery) (Page, error) {
	org := q.Config["organization"]
	if org == "" {
		return Page{}, &Failure{Code: CodeNotFound, Message: "Choose the Sentry organization in the project's Sentry settings."}
	}
	query := q.Config["query"]
	if query == "" {
		query = "is:unresolved"
	}
	limit := q.Limit
	if limit > 100 { // "The maximum is 100."
		limit = 100
	}
	v := url.Values{"query": {query}, "sort": {"date"}, "limit": {fmt.Sprint(limit)}}
	if p := q.Config["project"]; p != "" {
		v.Set("project", p)
	}
	if q.After != "" {
		v.Set("cursor", q.After)
	}
	var list []any
	r, err := a.HTTP.json(ctx, "GET", "/api/0/organizations/"+url.PathEscape(org)+"/issues/", v, nil, &list)
	if err != nil {
		return Page{}, err
	}
	pg := Page{Next: nextLinkCursor(r.Header)}
	for _, e := range list {
		m := asMap(e)
		id := str(m["id"])
		if id == "" {
			continue
		}
		events, _ := num(m["count"]) // a string in the API ("count": {"type": "string"})
		users, _ := num(m["userCount"])
		level := strings.ToLower(str(m["level"]))
		last := str(m["lastSeen"])
		proj := ""
		if pm := asMap(m["project"]); pm != nil {
			proj = str(pm["slug"])
		}
		short := str(m["shortId"])
		if short == "" {
			short = "SEN-" + shortID(id)
		}
		body := str(m["culprit"])
		if md := asMap(m["metadata"]); md != nil {
			body = strings.TrimSpace(str(md["type"]) + ": " + str(md["value"]) + "\n" + body)
		}
		assignee := ""
		if as := asMap(m["assignedTo"]); as != nil {
			assignee = str(as["name"])
		}
		pg.Items = append(pg.Items, RawItem{
			ExternalID: id, Kind: ItemErrorEvent, IssueRef: short, URL: str(m["permalink"]), Title: str(m["title"]), Body: body,
			State: str(m["status"]), Assignee: assignee, Priority: level,
			Created: millis(m["firstSeen"]), Updated: millis(last), Etag: last,
			Meta: map[string]any{"level": level, "events": events, "users": users, "project": proj, "culprit": str(m["culprit"]),
				"firstSeen": str(m["firstSeen"]), "lastSeen": last, "priority": str(m["priority"])},
		})
	}
	return pg, nil
}

// Detail reads the latest event of an issue and renders its stack trace. It is called only for an issue that is becoming a task.
func (sentryAdapter) Detail(ctx context.Context, a *API, cfg map[string]string, id string) (string, bool, error) {
	org := cfg["organization"]
	if org == "" {
		return "", false, nil
	}
	var ev map[string]any
	if _, err := a.HTTP.json(ctx, "GET", "/api/0/organizations/"+url.PathEscape(org)+"/issues/"+url.PathEscape(id)+"/events/latest/", nil, nil, &ev); err != nil {
		return "", false, err
	}
	t := sentryStack(ev)
	return t, t != "", nil
}

// sentryStack renders the exception entries of an event: "Type: value" and the frames, innermost last as Sentry stores them.
func sentryStack(ev map[string]any) string {
	var b strings.Builder
	for _, e := range asList(ev["entries"]) {
		em := asMap(e)
		if str(em["type"]) != "exception" {
			continue
		}
		for _, v := range asList(asMap(em["data"])["values"]) {
			vm := asMap(v)
			fmt.Fprintf(&b, "%s: %s\n", str(vm["type"]), str(vm["value"]))
			for _, f := range asList(asMap(vm["stacktrace"])["frames"]) {
				fm := asMap(f)
				line, _ := num(fm["lineNo"])
				fmt.Fprintf(&b, "  at %s (%s:%d)\n", firstNonEmpty(str(fm["function"]), "?"), firstNonEmpty(str(fm["filename"]), str(fm["absPath"]), "?"), line)
				// the frame's own code line, from its context ([[line number, text], ...])
				for _, cl := range asList(fm["context"]) {
					if pair := asList(cl); len(pair) == 2 {
						if n, ok := num(pair[0]); ok && n == line {
							fmt.Fprintf(&b, "      %s\n", strings.TrimSpace(str(pair[1])))
						}
					}
				}
			}
		}
	}
	return strings.TrimSpace(b.String())
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
		return OutCall{}, errf(ErrOpUnsupported, "Sentry has no operation %q", op)
	}
	org := cfg["organization"]
	if org == "" {
		return OutCall{}, errf(ErrBadRequest, "the project's Sentry settings name no organization")
	}
	return OutCall{Endpoint: "PUT /api/0/organizations/{org}/issues/{id}/", Args: map[string]any{"organization": org, "issueId": target, "status": status},
		Preview: fmt.Sprintf("Set Sentry issue %s (organization %s) to %s", target, org, status)}, nil
}

func (sentryAdapter) Send(ctx context.Context, a *API, call OutCall) (string, error) {
	if call.Endpoint != "PUT /api/0/organizations/{org}/issues/{id}/" {
		return "", errf(ErrOpUnsupported, "unknown Sentry call %q", call.Endpoint)
	}
	path := "/api/0/organizations/" + url.PathEscape(argString(call.Args, "organization")) + "/issues/" + url.PathEscape(argString(call.Args, "issueId")) + "/"
	var res map[string]any
	if _, err := a.HTTP.json(ctx, "PUT", path, nil, map[string]any{"status": argString(call.Args, "status")}, &res); err != nil {
		return "", err
	}
	return "status " + firstNonEmpty(str(res["status"]), argString(call.Args, "status")), nil
}
