package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// githubAdapter reads a repository's issues through the user's own GitHub CLI (gh api, internal/git/host): the gh sign-in is
// the credential, and Drydock never stores or sees a GitHub token. Endpoint: GET /repos/{owner}/{repo}/issues (state=open,
// sort=updated, direction=desc, since, labels, per_page at most 100, page numbers). Pull requests come back from that endpoint
// too and are skipped. An imported issue becomes a task whose reference is "#<number>", so its pull request says "Closes #n".
type githubAdapter struct{}

func (githubAdapter) Kind() string { return KindGitHub }

func (githubAdapter) FullSweep() bool { return false }

func (githubAdapter) Endpoints() []string {
	return []string{"gh api GET /repos/{owner}/{repo}/issues?state=open&sort=updated&direction=desc&since=&labels=&per_page=&page="}
}

func (githubAdapter) Ops() []OpInfo { return nil }

// ghFailure maps a gh error to a plain-language failure.
func ghFailure(err error) error {
	if err == nil {
		return nil
	}
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	if errors.Is(err, host.ErrAuth) {
		return &Failure{Code: CodeUnauthorized, Cause: err, Message: "The GitHub CLI (gh) is not signed in. Run `gh auth login` (Connections shows its state); Drydock uses that sign-in and stores no GitHub token."}
	}
	var rl *host.RateLimitError
	if errors.As(err, &rl) {
		return &Failure{Code: CodeRateLimited, Cause: err, RetryAfter: rl.Wait(time.Now(), time.Minute, time.Hour),
			Message: "GitHub asked Drydock to slow down (rate limit). Drydock will wait and try again by itself."}
	}
	if errors.Is(err, host.ErrNotFound) {
		// GitHub also answers 404 for a private repository the sign-in cannot see
		return &Failure{Code: CodeNotFound, Cause: err, Message: "GitHub did not find the repository (HTTP 404). Check the project's GitHub settings, and that your gh sign-in can see the repository."}
	}
	var ae *host.APIError
	if errors.As(err, &ae) && ae.Status == 403 { // a rate-limit 403 is a RateLimitError (above)
		return &Failure{Code: CodeForbidden, Cause: err, Message: "GitHub refused the request (HTTP 403): your gh sign-in lacks a permission for this repository, or an organization policy blocks it. Check `gh auth status` and the repository's access."}
	}
	return Classify(KindGitHub, err, "")
}

func (githubAdapter) Identity(ctx context.Context, a *API) (Identity, error) {
	if a.GH == nil {
		return Identity{}, &Failure{Code: CodeUnreachable, Message: "The GitHub CLI (gh) is not installed. Install it from https://cli.github.com and run `gh auth login`."}
	}
	who, err := host.Viewer(ctx, a.GH)
	if err != nil {
		return Identity{}, ghFailure(err)
	}
	return Identity{Account: who.Login, Workspace: "github.com", WorkspaceURL: "https://github.com/" + url.PathEscape(who.Login), Method: "GitHub CLI (gh) sign-in"}, nil
}

func (githubAdapter) Choices(ctx context.Context, a *API, key string, _ map[string]string) ([]Choice, error) {
	if key != "repo" {
		return nil, errf(ErrBadRequest, "GitHub has no setting %q", key)
	}
	if a.GH == nil {
		return nil, &Failure{Code: CodeUnreachable, Message: "The GitHub CLI (gh) is not installed."}
	}
	rs, err := host.ListRepos(ctx, a.GH, "", "")
	if err != nil {
		return nil, ghFailure(err)
	}
	out := []Choice{}
	for _, r := range rs {
		out = append(out, Choice{Value: r.FullName, Label: r.FullName})
	}
	return out, nil
}

func (githubAdapter) Page(ctx context.Context, a *API, q PageQuery) (Page, error) {
	if a.GH == nil {
		return Page{}, &Failure{Code: CodeUnreachable, Message: "The GitHub CLI (gh) is not installed."}
	}
	repo := q.Config["repo"]
	if !host.ValidFullName(repo) {
		return Page{}, &Failure{Code: CodeNotFound, Message: "The project's GitHub settings name no repository (owner/name)."}
	}
	page := 1
	if q.After != "" {
		if n, err := strconv.Atoi(q.After); err == nil && n > 0 {
			page = n
		}
	}
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	v := url.Values{"state": {"open"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {strconv.Itoa(limit)}, "page": {strconv.Itoa(page)}}
	if q.Since > 0 {
		v.Set("since", time.UnixMilli(q.Since).UTC().Format(time.RFC3339))
	}
	if l := q.Config["labels"]; l != "" {
		v.Set("labels", l)
	}
	type rawIssue struct {
		Number    int                     `json:"number"`
		Title     string                  `json:"title"`
		Body      string                  `json:"body"`
		URL       string                  `json:"html_url"`
		State     string                  `json:"state"`
		CreatedAt string                  `json:"created_at"`
		UpdatedAt string                  `json:"updated_at"`
		PR        *struct{}               `json:"pull_request"`
		Labels    []struct{ Name string } `json:"labels"`
		User      struct{ Login string }  `json:"user"`
		Assignee  *struct{ Login string } `json:"assignee"`
	}
	var raw []rawIssue
	if _, err := a.GH.Do(ctx, "GET", "/repos/"+repo+"/issues?"+v.Encode(), nil, &raw, ""); err != nil {
		return Page{}, ghFailure(err)
	}
	var pg Page
	if len(raw) == limit {
		pg.Next = strconv.Itoa(page + 1)
	}
	for _, r := range raw {
		if r.PR != nil || r.Number <= 0 {
			continue
		}
		it := RawItem{ExternalID: fmt.Sprintf("%s#%d", strings.ToLower(repo), r.Number), Kind: ItemIssue, IssueRef: fmt.Sprintf("#%d", r.Number), URL: r.URL,
			Title: r.Title, Body: r.Body, State: r.State, Created: millis(r.CreatedAt), Updated: millis(r.UpdatedAt), Etag: r.UpdatedAt,
			Meta: map[string]any{"repo": repo, "author": r.User.Login}}
		for _, l := range r.Labels {
			it.Labels = append(it.Labels, l.Name)
		}
		if r.Assignee != nil {
			it.Assignee = r.Assignee.Login
		}
		pg.Items = append(pg.Items, it)
	}
	return pg, nil
}

func (githubAdapter) Detail(context.Context, *API, map[string]string, string) (string, bool, error) {
	return "", false, nil
}

func (githubAdapter) TaskMapping(it Item) TaskMapping { return TaskMapping{Label: "issue"} }

func (githubAdapter) Build(op, _ string, _ map[string]string, _ map[string]string) (OutCall, error) {
	return OutCall{}, errf(ErrOpUnsupported, "GitHub has no outbound operation %q here (pull requests have their own flow)", op)
}

func (githubAdapter) Send(context.Context, *API, OutCall) (string, error) {
	return "", errf(ErrOpUnsupported, "GitHub has no outbound operations here")
}
