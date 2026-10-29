package integrations

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Typed Linear GraphQL calls beyond the listing (decision P114): one issue by id (a webhook names it), its attachments, its
// comments (the marker lookup before a retry) and its team's workflow states (the status mapping editor). Same client, credential,
// rate limit and failure handling as sync; written from the SDK's schema (packages/sdk/src/schema.graphql of @linear/sdk 100.0.0:
// Query.issue(id), Issue.attachments, Issue.comments, Attachment {id title subtitle url createdAt}, Team.states).

// linearUploadsHost serves files uploaded to Linear; a download from it needs the credential (any other attachment URL is a link
// to another site and is never fetched with it).
const linearUploadsHost = "uploads.linear.app"

const linearIssueQuery = `query DrydockIssue($id: String!) {
  issue(id: $id) {
    id identifier title description url priority createdAt updatedAt
    state { name } assignee { name displayName } labels(first: 20) { nodes { name } }
    team { key } project { id name } parent { identifier }
  }
}`

// Fetch reads one issue and says whether it is in the assignment's scope (its team and project).
func (linearAdapter) Fetch(ctx context.Context, a *API, cfg map[string]string, id string) (RawItem, bool, error) {
	var d struct {
		Issue *linearIssue `json:"issue"`
	}
	if err := a.HTTP.graphql(ctx, linearIssueQuery, map[string]any{"id": id}, &d); err != nil {
		var f *Failure
		if asFailure(err, &f) && f.Code == CodeNotFound {
			return RawItem{}, false, nil // deleted, or never there
		}
		return RawItem{}, false, err
	}
	if d.Issue == nil || d.Issue.ID == "" {
		return RawItem{}, false, nil
	}
	if t := cfg["team"]; t != "" && (d.Issue.Team == nil || !strings.EqualFold(d.Issue.Team.Key, t)) {
		return RawItem{}, false, nil
	}
	if p := cfg["project"]; p != "" && (d.Issue.Project == nil || d.Issue.Project.ID != p) {
		return RawItem{}, false, nil
	}
	return linearRaw(*d.Issue), true, nil
}

// Attachments lists an issue's attachments. Linear's attachments are mostly links to other sites (a pull request, a Sentry
// issue); only a file uploaded to Linear is downloadable.
func (linearAdapter) Attachments(ctx context.Context, a *API, _ map[string]string, it Item) ([]RawAttachment, error) {
	var d struct {
		Issue *struct {
			Attachments struct {
				Nodes []struct {
					ID, Title, Subtitle, URL, CreatedAt string
				} `json:"nodes"`
			} `json:"attachments"`
		} `json:"issue"`
	}
	if err := a.HTTP.graphql(ctx, `query DrydockAttachments($id: String!) { issue(id: $id) { attachments(first: 50) { nodes { id title subtitle url createdAt } } } }`,
		map[string]any{"id": it.ExternalID}, &d); err != nil {
		return nil, err
	}
	if d.Issue == nil {
		return nil, &Failure{Code: CodeNotFound, Message: "Linear did not find the issue."}
	}
	out := []RawAttachment{}
	for _, n := range d.Issue.Attachments.Nodes {
		if n.ID == "" {
			continue
		}
		dl := false
		if u, err := url.Parse(n.URL); err == nil && ((u.Scheme == "https" && strings.EqualFold(u.Hostname(), linearUploadsHost)) || sameHost(a, u)) {
			dl = true
		}
		out = append(out, RawAttachment{ExternalID: n.ID, Name: firstNonEmpty(n.Title, n.Subtitle, n.ID), URL: n.URL, Downloadable: dl})
	}
	return out, nil
}

// sameHost reports whether u is on the connection's API host (a local fixture or a self-hosted server).
func sameHost(a *API, u *url.URL) bool {
	b, err := url.Parse(a.HTTP.base)
	return err == nil && strings.EqualFold(b.Host, u.Host)
}

// FetchAttachment downloads a file uploaded to Linear (the credential goes only to Linear's uploads host or its API host).
func (linearAdapter) FetchAttachment(ctx context.Context, a *API, _ map[string]string, _ Item, att RawAttachment, max int64) ([]byte, string, error) {
	if !att.Downloadable {
		return nil, "", &Failure{Code: CodeOther, Message: "This attachment is a link to another site, not a file Linear keeps."}
	}
	return a.fetchFile(ctx, att.URL, linearUploadsHost, max)
}

// FindComment looks for a comment with the marker's text on an issue.
func (linearAdapter) FindComment(ctx context.Context, a *API, issueID, marker string, since int64) (string, bool, error) {
	var d struct {
		Issue *struct {
			Comments struct {
				Nodes []struct {
					ID, Body, CreatedAt string
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"issue"`
	}
	if err := a.HTTP.graphql(ctx, `query DrydockListComments($id: String!) { issue(id: $id) { comments(first: 100) { nodes { id body createdAt } } } }`,
		map[string]any{"id": issueID}, &d); err != nil {
		return "", false, err
	}
	if d.Issue == nil {
		return "", false, &Failure{Code: CodeNotFound, Message: "Linear did not find the issue."}
	}
	for _, c := range d.Issue.Comments.Nodes {
		if since > 0 && millis(c.CreatedAt) > 0 && millis(c.CreatedAt) < since {
			continue
		}
		if hashHex(normComment(c.Body))[:16] == marker {
			return c.ID, true, nil
		}
	}
	return "", false, nil
}

// MappingFields lists the team's workflow states (status mapping); Linear has no typed properties.
func (linearAdapter) MappingFields(ctx context.Context, a *API, cfg map[string]string, scope string) ([]MappingField, error) {
	if scope != MapStatus {
		return []MappingField{}, nil
	}
	team := cfg["team"]
	if team == "" {
		return nil, &Failure{Code: CodeNotFound, Message: "Choose the Linear team in this project's settings first: workflow states belong to a team."}
	}
	var d struct {
		Teams struct {
			Nodes []struct {
				Key    string `json:"key"`
				States struct {
					Nodes []struct{ Name, Type string } `json:"nodes"`
				} `json:"states"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := a.HTTP.graphql(ctx, `query DrydockTeamStates($key: String!) { teams(first: 1, filter: {key: {eq: $key}}) { nodes { key states(first: 100) { nodes { name type } } } } }`,
		map[string]any{"key": team}, &d); err != nil {
		return nil, err
	}
	out := []MappingField{}
	for _, t := range d.Teams.Nodes {
		for _, s := range t.States.Nodes {
			if s.Name != "" {
				out = append(out, MappingField{Name: s.Name, Type: s.Type})
			}
		}
	}
	if len(out) == 0 {
		return nil, &Failure{Code: CodeNotFound, Message: fmt.Sprintf("Linear lists no workflow states for team %s.", team)}
	}
	return out, nil
}
