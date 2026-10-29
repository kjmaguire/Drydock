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
// /api/0/organizations/{org}/projects/, GET /api/0/organizations/{org}/issues/ (sort=date, query, project (integer ids), limit at most 100,
// Link-header cursors), GET .../issues/{id}/events/latest/ (the stack trace), PUT .../issues/{id}/ (status). Auth: a user,
// organization or internal-integration token, or an OAuth access token, as "Authorization: Bearer <token>".
type sentryAdapter struct{}

func (sentryAdapter) Kind() string { return KindSentry }

// FullSweep: every sync walks the whole query (by default is:unresolved), and issues missing from a complete walk are gone.
// Sentry's issue search has no filter or sort by the time an issue's status changed (sort is date = lastSeen, new, freq,
// user, trends or inbox), and resolving an issue does not change its lastSeen, so a walk that stops at a lastSeen watermark
// would never see a resolve; the unresolved set itself is what tells which issues are still open.
func (sentryAdapter) FullSweep() bool { return true }

func (sentryAdapter) Endpoints() []string {
	return []string{"GET /api/0/organizations/{org}/issues/?project=<id>&query=&sort=date&limit= (Link cursors; every page each sync)", "GET /api/0/organizations/{org}/issues/{id}/events/latest/ (on import)"}
}

func (sentryAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "resolve", Label: "Resolve the issue", Params: nil, Description: "Marks the Sentry issue resolved (PUT .../issues/{id}/ status=resolved)."},
		{Op: "ignore", Label: "Ignore the issue", Params: nil, NeverAuto: true, Description: "Marks the Sentry issue ignored: it stops alerting (status=ignored)."},
		{Op: "unresolve", Label: "Reopen the issue", Params: nil, Description: "Marks the Sentry issue unresolved again (status=unresolved)."},
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
	if key == "project" {
		// the issues endpoint filters by project id (an array of integers): the value is the id, the label the name
		projects, err := sentryProjects(ctx, a, cfg["organization"])
		if err != nil {
			return nil, err
		}
		out := []Choice{}
		for _, p := range projects {
			out = append(out, Choice{Value: p.ID, Label: firstNonEmpty(p.Name, p.Slug)})
		}
		return out, nil
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

type sentryProject struct{ ID, Slug, Name string }

// sentryProjects lists an organization's projects (GET /api/0/organizations/{org}/projects/, Link-header cursors). Projects
// without a numeric id are skipped: the issues endpoint could not filter by them.
func sentryProjects(ctx context.Context, a *API, org string) ([]sentryProject, error) {
	var out []sentryProject
	cursor := ""
	for page := 0; page < 20; page++ {
		v := url.Values{"per_page": {"100"}}
		if cursor != "" {
			v.Set("cursor", cursor)
		}
		var list []any
		r, err := a.HTTP.json(ctx, "GET", "/api/0/organizations/"+url.PathEscape(org)+"/projects/", v, nil, &list)
		if err != nil {
			return nil, err
		}
		for _, e := range list {
			m := asMap(e)
			if id := str(m["id"]); isDigits(id) {
				out = append(out, sentryProject{ID: id, Slug: str(m["slug"]), Name: str(m["name"])})
			}
		}
		if cursor = nextLinkCursor(r.Header); cursor == "" {
			break
		}
	}
	return out, nil
}

func isDigits(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// sentryProjectID returns the numeric id of the assignment's project setting. Assignments made before Drydock sent ids hold
// the project's slug, which the issues endpoint does not accept; it is looked up in the organization's projects.
func sentryProjectID(ctx context.Context, a *API, org, project string) (string, error) {
	if isDigits(project) {
		return project, nil
	}
	projects, err := sentryProjects(ctx, a, org)
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.Slug == project {
			return p.ID, nil
		}
	}
	return "", &Failure{Code: CodeNotFound, Message: fmt.Sprintf("The Sentry project %q in this project's Sentry settings is not in organization %s any more, or the token cannot see it. Choose the Sentry project again.",
		clampText(project, 60), clampText(org, 60))}
}

// ResolveScope turns a legacy project slug in an assignment's settings into the project's id, so the assignment is stored
// with the id once and later syncs need no lookup.
func (sentryAdapter) ResolveScope(ctx context.Context, a *API, cfg map[string]string) (map[string]string, bool, error) {
	p := cfg["project"]
	if p == "" || isDigits(p) || cfg["organization"] == "" {
		return cfg, false, nil
	}
	id, err := sentryProjectID(ctx, a, cfg["organization"], p)
	if err != nil {
		return cfg, false, err
	}
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}
	out["project"] = id
	return out, true, nil
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
		// "project": an array of integer project ids; a slug is refused (sync stores the id after its first lookup)
		id, err := sentryProjectID(ctx, a, org, p)
		if err != nil {
			return Page{}, err
		}
		v.Set("project", id)
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
		if str(m["id"]) == "" {
			continue
		}
		pg.Items = append(pg.Items, sentryRaw(m))
	}
	return pg, nil
}

// sentryRaw reads one issue of the API's answer (a listing's entry or a single issue).
func sentryRaw(m map[string]any) RawItem {
	id := str(m["id"])
	events, _ := num(m["count"]) // a string in the API ("count": {"type": "string"})
	users, _ := num(m["userCount"])
	level := strings.ToLower(str(m["level"]))
	last := str(m["lastSeen"])
	proj, projID := "", ""
	if pm := asMap(m["project"]); pm != nil {
		proj, projID = str(pm["slug"]), str(pm["id"])
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
	status := str(m["status"])
	return RawItem{
		ExternalID: id, Kind: ItemErrorEvent, IssueRef: short, URL: str(m["permalink"]), Title: str(m["title"]), Body: body,
		// a query that also lists resolved or ignored issues: they are not open work
		State: status, Assignee: assignee, Priority: level, Closed: status == "resolved" || status == "ignored",
		Created: millis(m["firstSeen"]), Updated: millis(last), Etag: last,
		Meta: map[string]any{"level": level, "events": events, "users": users, "project": proj, "projectId": projID, "culprit": str(m["culprit"]),
			"firstSeen": str(m["firstSeen"]), "lastSeen": last, "priority": str(m["priority"])},
	}
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
	status := map[string]string{"resolve": "resolved", "ignore": "ignored", "unresolve": "unresolved"}[op]
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
