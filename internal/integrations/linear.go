package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// linearAdapter reads and writes Linear through its GraphQL API (https://api.linear.app/graphql). Linear's official SDK is
// TypeScript (@linear/sdk); there is no official Go SDK, so this is a plain GraphQL client that follows the SDK's schema
// (packages/sdk/src/schema.graphql, read for this change: Query.issues(first, after, filter: IssueFilter, orderBy:
// PaginationOrderBy), IssueFilter.updatedAt and .team, Relay pageInfo{hasNextPage endCursor}, commentCreate, issueUpdate) and
// its error types (extensions.type "ratelimited", "authentication error", "forbidden").
//
// Auth: a personal API key is sent as "Authorization: <key>"; an OAuth access token as "Authorization: Bearer <token>".
type linearAdapter struct{}

func (linearAdapter) Kind() string { return KindLinear }

func (linearAdapter) FullSweep() bool { return false }

func (linearAdapter) Endpoints() []string {
	return []string{"POST /graphql query issues(first, after, filter: {team, project, updatedAt > watermark}, orderBy: updatedAt)"}
}

func (linearAdapter) Ops() []OpInfo {
	return []OpInfo{
		{Op: "comment", Label: "Comment on the issue", Params: []string{"body"}, Description: "Posts a comment on the Linear issue (commentCreate)."},
		{Op: "set_state", Label: "Change the issue status", Params: []string{"state"}, Description: "Moves the Linear issue to another workflow state of its team (issueUpdate)."},
	}
}

type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
		Type string `json:"type"`
	} `json:"extensions"`
}

// graphql posts one GraphQL operation and decodes data into out. Linear reports some failures (rate limit, authentication)
// as GraphQL errors, sometimes with HTTP 400; they are classified here.
func (c *apiClient) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	r, err := c.do(withCallOp(ctx, graphqlOpName(query)), "POST", "/graphql", nil, map[string]any{"query": query, "variables": vars})
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if r != nil && len(r.Body) > 0 {
		_ = json.Unmarshal(r.Body, &env)
	}
	if len(env.Errors) > 0 {
		e := env.Errors[0]
		t := strings.ToLower(e.Extensions.Type + " " + e.Extensions.Code)
		switch {
		case strings.Contains(t, "ratelimit"):
			f := &Failure{Code: CodeRateLimited, Cause: ErrRateLimited, Message: "Linear asked Drydock to slow down (rate limit). Drydock will wait and try again by itself."}
			if r != nil {
				if until := RateHeaders(KindLinear, r.Header, 429, c.now()); !until.IsZero() {
					f.RetryAfter = until.Sub(c.now())
				}
			}
			return f
		case strings.Contains(t, "authentication"):
			return &Failure{Code: CodeUnauthorized, Message: "Linear rejected the credential (authentication error). It may have been revoked or expired. Reconnect Linear on the Integrations page."}
		case strings.Contains(t, "forbidden"), strings.Contains(t, "feature not accessible"):
			return &Failure{Code: CodeForbidden, Message: "Linear refused the request (" + strings.TrimSpace(e.Extensions.Type) + "): the credential lacks a permission. Reconnect Linear with read and write access."}
		}
		if err == nil {
			code := e.Extensions.Code
			if !vendorCodeRe.MatchString(code) {
				code = ""
			}
			return &Failure{Code: CodeOther, Message: "Linear did not accept the request" + map[bool]string{true: " (" + code + ")", false: ""}[code != ""] + "."}
		}
	}
	if err != nil {
		return err
	}
	if out != nil {
		if len(bytes.TrimSpace(env.Data)) == 0 || string(env.Data) == "null" {
			return schemaDrift(KindLinear, fmt.Errorf("no data in the GraphQL answer"))
		}
		dec := json.NewDecoder(bytes.NewReader(env.Data))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return schemaDrift(KindLinear, err)
		}
	}
	return nil
}

func (linearAdapter) Identity(ctx context.Context, a *API) (Identity, error) {
	var d struct {
		Viewer struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"viewer"`
		Organization struct {
			Name   string `json:"name"`
			URLKey string `json:"urlKey"`
		} `json:"organization"`
	}
	if err := a.HTTP.graphql(ctx, `query DrydockIdentity { viewer { name displayName } organization { name urlKey } }`, nil, &d); err != nil {
		return Identity{}, err
	}
	if d.Viewer.Name == "" && d.Organization.Name == "" {
		return Identity{}, schemaDrift(KindLinear, fmt.Errorf("viewer and organization are empty"))
	}
	id := Identity{Account: firstNonEmpty(d.Viewer.Name, d.Viewer.DisplayName), Workspace: d.Organization.Name}
	if k := d.Organization.URLKey; k != "" && issueRefRe.MatchString(k) {
		id.WorkspaceURL = "https://linear.app/" + k
	}
	return id, nil
}

func (linearAdapter) Choices(ctx context.Context, a *API, key string, _ map[string]string) ([]Choice, error) {
	switch key {
	case "team":
		var d struct {
			Teams struct {
				Nodes []struct{ Key, Name string } `json:"nodes"`
			} `json:"teams"`
		}
		if err := a.HTTP.graphql(ctx, `query DrydockTeams { teams(first: 100) { nodes { key name } } }`, nil, &d); err != nil {
			return nil, err
		}
		out := []Choice{}
		for _, t := range d.Teams.Nodes {
			out = append(out, Choice{Value: t.Key, Label: t.Key + " · " + t.Name})
		}
		return out, nil
	case "project":
		var d struct {
			Projects struct {
				Nodes []struct{ ID, Name string } `json:"nodes"`
			} `json:"projects"`
		}
		if err := a.HTTP.graphql(ctx, `query DrydockProjects { projects(first: 100) { nodes { id name } } }`, nil, &d); err != nil {
			return nil, err
		}
		out := []Choice{}
		for _, p := range d.Projects.Nodes {
			out = append(out, Choice{Value: p.ID, Label: p.Name})
		}
		return out, nil
	}
	return nil, errf(ErrBadRequest, "Linear has no setting %q", key)
}

const linearIssuesQuery = `query DrydockIssues($first: Int!, $after: String, $filter: IssueFilter) {
  issues(first: $first, after: $after, filter: $filter, orderBy: updatedAt) {
    nodes {
      id identifier title description url priority createdAt updatedAt
      state { name } assignee { name displayName } labels(first: 20) { nodes { name } }
      team { key } project { name } parent { identifier }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

type linearIssue struct {
	ID          string      `json:"id"`
	Identifier  string      `json:"identifier"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	URL         string      `json:"url"`
	Priority    json.Number `json:"priority"`
	CreatedAt   string      `json:"createdAt"`
	UpdatedAt   string      `json:"updatedAt"`
	State       *struct {
		Name string `json:"name"`
	} `json:"state"`
	Assignee *struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	} `json:"assignee"`
	Labels *struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Team *struct {
		Key string `json:"key"`
	} `json:"team"`
	Project *struct {
		Name string `json:"name"`
	} `json:"project"`
	Parent *struct {
		Identifier string `json:"identifier"`
	} `json:"parent"`
}

// linearPriority maps Linear's priority number (0 none, 1 urgent, 2 high, 3 medium, 4 low) to a word.
func linearPriority(n json.Number) string {
	f, err := n.Float64()
	if err != nil {
		return "none"
	}
	switch int(f) {
	case 1:
		return "urgent"
	case 2:
		return "high"
	case 3:
		return "medium"
	case 4:
		return "low"
	}
	return "none"
}

func linearFilter(q PageQuery) map[string]any {
	f := map[string]any{}
	if t := q.Config["team"]; t != "" {
		f["team"] = map[string]any{"key": map[string]any{"eq": t}}
	}
	if p := q.Config["project"]; p != "" {
		f["project"] = map[string]any{"id": map[string]any{"eq": p}}
	}
	if q.Since > 0 {
		f["updatedAt"] = map[string]any{"gt": time.UnixMilli(q.Since).UTC().Format(time.RFC3339Nano)}
	}
	return f
}

func (linearAdapter) Page(ctx context.Context, a *API, q PageQuery) (Page, error) {
	vars := map[string]any{"first": q.Limit, "filter": linearFilter(q)}
	if q.After != "" {
		vars["after"] = q.After
	}
	var d struct {
		Issues *struct {
			Nodes    []linearIssue `json:"nodes"`
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
		} `json:"issues"`
	}
	if err := a.HTTP.graphql(ctx, linearIssuesQuery, vars, &d); err != nil {
		return Page{}, err
	}
	if d.Issues == nil {
		return Page{}, schemaDrift(KindLinear, fmt.Errorf("no issues connection in the answer"))
	}
	var pg Page
	if d.Issues.PageInfo.HasNextPage {
		pg.Next = d.Issues.PageInfo.EndCursor
	}
	for _, i := range d.Issues.Nodes {
		if i.ID == "" {
			continue
		}
		ref := i.Identifier
		if ref == "" {
			ref = "LIN-" + shortID(i.ID)
		}
		it := RawItem{ExternalID: i.ID, Kind: ItemIssue, IssueRef: ref, URL: i.URL, Title: i.Title, Body: i.Description, Priority: linearPriority(i.Priority),
			Created: millis(i.CreatedAt), Updated: millis(i.UpdatedAt), Etag: i.UpdatedAt, Meta: map[string]any{}}
		if i.State != nil {
			it.State = i.State.Name
		}
		if i.Assignee != nil {
			it.Assignee = firstNonEmpty(i.Assignee.DisplayName, i.Assignee.Name)
		}
		if i.Labels != nil {
			for _, l := range i.Labels.Nodes {
				it.Labels = append(it.Labels, l.Name)
			}
		}
		if i.Team != nil {
			it.Meta["team"] = i.Team.Key
		}
		if i.Project != nil {
			it.Meta["project"] = i.Project.Name
		}
		if i.Parent != nil {
			it.Parent = i.Parent.Identifier
		}
		pg.Items = append(pg.Items, it)
	}
	return pg, nil
}

func (linearAdapter) Detail(context.Context, *API, map[string]string, string) (string, bool, error) {
	return "", false, nil // the listing carries the description
}

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
		return OutCall{Endpoint: "graphql commentCreate", Args: map[string]any{"issueId": target, "body": body},
			Preview: fmt.Sprintf("Comment on Linear issue %s:\n%s", target, Quote(body))}, nil
	case "set_state":
		st := strings.TrimSpace(p["state"])
		if st == "" {
			return OutCall{}, errf(ErrBadRequest, "set_state needs the new status name")
		}
		return OutCall{Endpoint: "graphql issueUpdate(stateId)", Args: map[string]any{"issueId": target, "state": st},
			Preview: fmt.Sprintf("Move Linear issue %s to status %q", target, st)}, nil
	}
	return OutCall{}, errf(ErrOpUnsupported, "Linear has no operation %q", op)
}

func (linearAdapter) Send(ctx context.Context, a *API, call OutCall) (string, error) {
	issue := argString(call.Args, "issueId")
	switch call.Endpoint {
	case "graphql commentCreate":
		var d struct {
			CommentCreate struct {
				Success bool `json:"success"`
				Comment struct {
					ID string `json:"id"`
				} `json:"comment"`
			} `json:"commentCreate"`
		}
		err := a.HTTP.graphql(ctx, `mutation DrydockComment($input: CommentCreateInput!) { commentCreate(input: $input) { success comment { id } } }`,
			map[string]any{"input": map[string]any{"issueId": issue, "body": argString(call.Args, "body")}}, &d)
		if err != nil {
			return "", err
		}
		if !d.CommentCreate.Success {
			return "", &Failure{Code: CodeOther, Message: "Linear did not create the comment."}
		}
		return "comment " + d.CommentCreate.Comment.ID, nil
	case "graphql issueUpdate(stateId)":
		want := argString(call.Args, "state")
		var st struct {
			Issue struct {
				Team struct {
					States struct {
						Nodes []struct{ ID, Name string } `json:"nodes"`
					} `json:"states"`
				} `json:"team"`
			} `json:"issue"`
		}
		if err := a.HTTP.graphql(ctx, `query DrydockStates($id: String!) { issue(id: $id) { team { states(first: 100) { nodes { id name } } } } }`, map[string]any{"id": issue}, &st); err != nil {
			return "", err
		}
		stateID := ""
		for _, n := range st.Issue.Team.States.Nodes {
			if strings.EqualFold(n.Name, want) {
				stateID = n.ID
			}
		}
		if stateID == "" {
			return "", &Failure{Code: CodeOther, Message: fmt.Sprintf("Linear's team for this issue has no status named %q.", want)}
		}
		var d struct {
			IssueUpdate struct {
				Success bool `json:"success"`
			} `json:"issueUpdate"`
		}
		if err := a.HTTP.graphql(ctx, `mutation DrydockSetState($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success } }`,
			map[string]any{"id": issue, "input": map[string]any{"stateId": stateID}}, &d); err != nil {
			return "", err
		}
		if !d.IssueUpdate.Success {
			return "", &Failure{Code: CodeOther, Message: "Linear did not change the status."}
		}
		return "status " + want, nil
	}
	return "", errf(ErrOpUnsupported, "unknown Linear call %q", call.Endpoint)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
