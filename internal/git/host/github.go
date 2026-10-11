package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"
)

// GitHub operates on one repository.
type GitHub struct {
	API   API
	Owner string
	Repo  string
}

func (g GitHub) p(format string, a ...any) string {
	return fmt.Sprintf("/repos/%s/%s", g.Owner, g.Repo) + fmt.Sprintf(format, a...)
}

// PR is a pull request.
type PR struct {
	Number         int    `json:"number"`
	URL            string `json:"html_url"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	Mergeable      *bool  `json:"mergeable"`
	MergeableState string `json:"mergeable_state"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	NodeID string `json:"node_id"`
}

// PRRequest opens a PR.
type PRRequest struct {
	Title, Body, Head, Base string
	Draft                   bool
}

// OpenPR creates a pull request. Head is the pushed branch.
func (g GitHub) OpenPR(ctx context.Context, r PRRequest) (PR, error) {
	var pr PR
	_, err := g.API.Do(ctx, "POST", g.p("/pulls"), map[string]any{"title": r.Title, "body": r.Body, "head": r.Head, "base": r.Base, "draft": r.Draft}, &pr, "")
	return pr, err
}

// FindOpenPR returns the open pull request whose head is branch (of this repository), or nil when there is none. The flow
// looks before it opens one, so a retry after a crash or a lost answer adopts the pull request it already opened instead of
// failing on GitHub's "a pull request already exists".
func (g GitHub) FindOpenPR(ctx context.Context, branch string) (*PR, error) {
	var prs []PR
	q := "?state=open&per_page=10&head=" + url.QueryEscape(g.Owner+":"+branch)
	if _, err := g.API.Do(ctx, "GET", g.p("%s", "/pulls"+q), nil, &prs, ""); err != nil {
		return nil, err
	}
	for i := range prs {
		if prs[i].Head.Ref == branch {
			return &prs[i], nil
		}
	}
	return nil, nil
}

// maxPages bounds how many pages of 100 a list read follows (10,000 items): a list longer than that is not a pull request's.
const maxPages = 100

// listAll reads every page of a list endpoint whose path already carries per_page=100, page by page until a short page.
func listAll[T any](ctx context.Context, api API, path string) ([]T, error) {
	var all []T
	for page := 1; page <= maxPages; page++ {
		var items []T
		if _, err := api.Do(ctx, "GET", fmt.Sprintf("%s&page=%d", path, page), nil, &items, ""); err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < 100 {
			break
		}
	}
	return all, nil
}

// GetPR fetches a PR. A non-empty etag makes the call conditional: ErrNotModified costs no rate limit.
func (g GitHub) GetPR(ctx context.Context, n int, etag string) (PR, string, error) {
	var pr PR
	e, err := g.API.Do(ctx, "GET", g.p("/pulls/%d", n), nil, &pr, etag)
	return pr, e, err
}

// UpdatePR changes title/body/state ("open" or "closed").
func (g GitHub) UpdatePR(ctx context.Context, n int, fields map[string]any) (PR, error) {
	var pr PR
	_, err := g.API.Do(ctx, "PATCH", g.p("/pulls/%d", n), fields, &pr, "")
	return pr, err
}

// ClosePR closes without merging.
func (g GitHub) ClosePR(ctx context.Context, n int) error {
	_, err := g.UpdatePR(ctx, n, map[string]any{"state": "closed"})
	return err
}

// CheckRun is one CI result on a commit: a check run (GitHub Actions and other apps; for Actions the check run id is also
// the job id) or a commit status (the older status API that many outside CI services still use). Both count as CI.
type CheckRun struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`       // a check run's name, or a status's context
	Status      string `json:"status"`     // queued | in_progress | completed; a status: pending | completed
	Conclusion  string `json:"conclusion"` // success | failure | cancelled | skipped | timed_out | neutral | action_required; a status: success | failure | error
	URL         string `json:"html_url"`
	Kind        string `json:"kind"`                  // KindCheckRun or KindStatus
	Description string `json:"description,omitempty"` // a status's description (a status has no job log)
	AppSlug     string `json:"-"`
}

// The two kinds of CI result.
const (
	KindCheckRun = "check_run"
	KindStatus   = "status"
)

// CIState summarizes checks for a commit.
type CIState struct {
	Runs    []CheckRun
	Pending bool
	Failed  []CheckRun
	Passed  bool // every run completed and none failed (and at least one run exists)
	// Missing are the required checks (CIWait.Required) that had not reported when WaitCI stopped waiting.
	Missing []string
}

type rawRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"html_url"`
	App        struct {
		Slug string `json:"slug"`
	} `json:"app"`
}

type rawStatus struct {
	ID          int64  `json:"id"`
	Context     string `json:"context"`
	State       string `json:"state"` // error | failure | pending | success
	Description string `json:"description"`
	TargetURL   string `json:"target_url"`
}

// CICache lets repeated Checks of one commit use conditional requests: GitHub answers an unchanged first page with 304,
// which does not count against the rate limit. Only a commit whose results fit on one page is cached.
type CICache struct {
	sha                 string
	runsETag, statsETag string
	runs                []rawRun
	stats               []rawStatus
}

// Checks lists what CI reported on a commit, its check runs and its commit statuses, and summarizes them.
func (g GitHub) Checks(ctx context.Context, sha string) (CIState, error) {
	return g.ChecksCached(ctx, sha, nil)
}

// ChecksCached is Checks with conditional requests through c (nil reads everything afresh).
func (g GitHub) ChecksCached(ctx context.Context, sha string, c *CICache) (CIState, error) {
	if c != nil && c.sha != sha {
		*c = CICache{sha: sha}
	}
	runs, err := pages(ctx, g.API, g.p("/commits/%s/check-runs?per_page=100", sha), cacheTag(c, true), func(page []byte) (int, int, []rawRun, error) {
		var r struct {
			Total int      `json:"total_count"`
			Runs  []rawRun `json:"check_runs"`
		}
		err := json.Unmarshal(page, &r)
		return r.Total, len(r.Runs), r.Runs, err
	})
	if err != nil {
		return CIState{}, err
	}
	// the combined status holds the latest status of each context; its own "state" is "pending" when there is none, so only
	// the list is read
	stats, err := pages(ctx, g.API, g.p("/commits/%s/status?per_page=100", sha), cacheTag(c, false), func(page []byte) (int, int, []rawStatus, error) {
		var r struct {
			Total    int         `json:"total_count"`
			Statuses []rawStatus `json:"statuses"`
		}
		err := json.Unmarshal(page, &r)
		return r.Total, len(r.Statuses), r.Statuses, err
	})
	if err != nil {
		return CIState{}, err
	}
	if c != nil {
		if runs.notModified {
			runs.items = c.runs
		} else {
			c.runs, c.runsETag = runs.items, runs.etag
		}
		if stats.notModified {
			stats.items = c.stats
		} else {
			c.stats, c.statsETag = stats.items, stats.etag
		}
	}
	var s CIState
	add := func(cr CheckRun) {
		s.Runs = append(s.Runs, cr)
		if cr.Status != "completed" {
			s.Pending = true
			return
		}
		switch cr.Conclusion {
		case "failure", "timed_out", "cancelled", "action_required", "error":
			s.Failed = append(s.Failed, cr)
		}
	}
	for _, r := range runs.items {
		add(CheckRun{ID: r.ID, Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, URL: r.URL, Kind: KindCheckRun, AppSlug: r.App.Slug})
	}
	for _, st := range stats.items {
		cr := CheckRun{ID: st.ID, Name: st.Context, Status: "completed", Conclusion: st.State, URL: st.TargetURL, Kind: KindStatus, Description: st.Description}
		if st.State == "pending" {
			cr.Status, cr.Conclusion = "pending", ""
		}
		add(cr)
	}
	s.Passed = len(s.Runs) > 0 && !s.Pending && len(s.Failed) == 0
	return s, nil
}

func cacheTag(c *CICache, runs bool) *string {
	switch {
	case c == nil:
		return nil
	case runs:
		return &c.runsETag
	}
	return &c.statsETag
}

type paged[T any] struct {
	items       []T
	etag        string // of the first page, when the list fit on it
	notModified bool   // the first page was unchanged (the caller keeps what it had)
}

// pages reads an object-wrapped list ({"total_count": n, "<list>": [...]}) page by page until total_count items are read or
// a page is short. With etag non-nil, the first page is asked for conditionally; a list longer than a page is never cached.
func pages[T any](ctx context.Context, api API, path string, etag *string, decode func([]byte) (total, n int, items []T, err error)) (paged[T], error) {
	var out paged[T]
	for page := 1; page <= maxPages; page++ {
		prev := ""
		if page == 1 && etag != nil {
			prev = *etag
		}
		var raw json.RawMessage
		tag, err := api.Do(ctx, "GET", fmt.Sprintf("%s&page=%d", path, page), nil, &raw, prev)
		if errors.Is(err, ErrNotModified) && page == 1 && prev != "" {
			out.notModified = true
			return out, nil
		}
		if err != nil {
			return out, err
		}
		total, n, items, err := decode(raw)
		if err != nil {
			return out, fmt.Errorf("decode %s: %w", path, err)
		}
		out.items = append(out.items, items...)
		if page == 1 && n >= total {
			out.etag = tag
		}
		if n < 100 || len(out.items) >= total {
			break
		}
	}
	return out, nil
}

// FailedLog returns the log of a failed Actions job (the check run id is the job id), or an explanatory error.
func (g GitHub) FailedLog(ctx context.Context, jobID int64) (string, error) {
	return g.API.Raw(ctx, g.p("/actions/jobs/%d/logs", jobID))
}

// ReviewEvent is any text a person (or bot) left on a PR. All of it is untrusted data (brief section 16).
type ReviewEvent struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"` // review | inline | issue_comment
	Author      string    `json:"author"`
	Association string    `json:"association"`     // OWNER MEMBER COLLABORATOR CONTRIBUTOR NONE ...
	State       string    `json:"state,omitempty"` // for reviews: APPROVED, CHANGES_REQUESTED, COMMENTED
	Body        string    `json:"body"`
	Path        string    `json:"path,omitempty"`
	Line        int       `json:"line,omitempty"`
	InReplyTo   int64     `json:"inReplyTo,omitempty"`
	Created     time.Time `json:"created"`
}

// Trusted reports whether the author is a repository collaborator-level identity. Only trusted authors' change requests
// become automatic REVISE feedback; everyone else's text is shown to the human and never acted on by itself.
func (e ReviewEvent) Trusted() bool {
	switch e.Association {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}

type review struct {
	ID    int64                  `json:"id"`
	State string                 `json:"state"`
	Body  string                 `json:"body"`
	User  struct{ Login string } `json:"user"`
	Assoc string                 `json:"author_association"`
	At    time.Time              `json:"submitted_at"`
}

type inlineComment struct {
	ID    int64                  `json:"id"`
	Body  string                 `json:"body"`
	Path  string                 `json:"path"`
	Line  int                    `json:"line"`
	Reply int64                  `json:"in_reply_to_id"`
	User  struct{ Login string } `json:"user"`
	Assoc string                 `json:"author_association"`
	At    time.Time              `json:"created_at"`
}

type issueComment struct {
	ID    int64                  `json:"id"`
	Body  string                 `json:"body"`
	User  struct{ Login string } `json:"user"`
	Assoc string                 `json:"author_association"`
	At    time.Time              `json:"created_at"`
}

// Reviews returns reviews, inline comments and issue comments on the PR created after since (zero = all), oldest first.
func (g GitHub) Reviews(ctx context.Context, n int, since time.Time) ([]ReviewEvent, error) {
	var out []ReviewEvent
	reviews, err := listAll[review](ctx, g.API, g.p("/pulls/%d/reviews?per_page=100", n))
	if err != nil {
		return nil, err
	}
	for _, r := range reviews {
		out = append(out, ReviewEvent{ID: r.ID, Kind: "review", Author: r.User.Login, Association: r.Assoc, State: r.State, Body: r.Body, Created: r.At})
	}
	inline, err := listAll[inlineComment](ctx, g.API, g.p("/pulls/%d/comments?per_page=100", n))
	if err != nil {
		return nil, err
	}
	for _, c := range inline {
		out = append(out, ReviewEvent{ID: c.ID, Kind: "inline", Author: c.User.Login, Association: c.Assoc, Body: c.Body, Path: c.Path, Line: c.Line, InReplyTo: c.Reply, Created: c.At})
	}
	issue, err := listAll[issueComment](ctx, g.API, g.p("/issues/%d/comments?per_page=100", n))
	if err != nil {
		return nil, err
	}
	for _, c := range issue {
		out = append(out, ReviewEvent{ID: c.ID, Kind: "issue_comment", Author: c.User.Login, Association: c.Assoc, Body: c.Body, Created: c.At})
	}
	var filtered []ReviewEvent
	for _, e := range out {
		if since.IsZero() || e.Created.After(since) {
			filtered = append(filtered, e)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Created.Before(filtered[j].Created) })
	return filtered, nil
}

// Reply answers an inline review comment thread.
func (g GitHub) Reply(ctx context.Context, n int, commentID int64, body string) error {
	_, err := g.API.Do(ctx, "POST", g.p("/pulls/%d/comments/%d/replies", n, commentID), map[string]string{"body": body}, nil, "")
	return err
}

// Comment posts an issue-style comment on a PR or issue.
func (g GitHub) Comment(ctx context.Context, n int, body string) (int64, error) {
	var c struct {
		ID int64 `json:"id"`
	}
	_, err := g.API.Do(ctx, "POST", g.p("/issues/%d/comments", n), map[string]string{"body": body}, &c, "")
	return c.ID, err
}

// MergeStrategy for GitHub merges.
type MergeStrategy string

const (
	MergeMerge  MergeStrategy = "merge"
	MergeSquash MergeStrategy = "squash"
	MergeRebase MergeStrategy = "rebase"
)

// Merge merges the PR. It is human-initiated only (the caller must hold the human's approval); branch protection rules
// are respected: GitHub refuses a merge that required checks or reviews do not allow, and that error is returned as is.
// The request carries the merge method, the head guard and the commit title, never anything that asks for administrator
// rights or a bypass (the pull-request flow also reads MergeState first and does not call Merge on a blocked pull request).
// shaGuard, if set, makes GitHub refuse the merge when the PR head moved since the human reviewed it.
func (g GitHub) Merge(ctx context.Context, n int, strategy MergeStrategy, shaGuard, commitTitle string) (string, error) {
	body := map[string]any{"merge_method": string(strategy)}
	if shaGuard != "" {
		body["sha"] = shaGuard
	}
	if commitTitle != "" {
		body["commit_title"] = commitTitle
	}
	var res struct {
		SHA    string `json:"sha"`
		Merged bool   `json:"merged"`
	}
	if _, err := g.API.Do(ctx, "PUT", g.p("/pulls/%d/merge", n), body, &res, ""); err != nil {
		return "", err
	}
	if !res.Merged {
		return "", fmt.Errorf("github reported the PR as not merged")
	}
	return res.SHA, nil
}

// DeleteBranch removes a remote branch (after merge, when the project says so).
func (g GitHub) DeleteBranch(ctx context.Context, branch string) error {
	_, err := g.API.Do(ctx, "DELETE", g.p("/git/refs/heads/%s", url.PathEscape(branch)), nil, nil, "")
	return err
}

// Issue is an imported issue (text is untrusted).
type Issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	URL    string   `json:"html_url"`
	State  string   `json:"state"`
	Labels []string `json:"labels"`
	Author string   `json:"author"`
}

// Issues lists open issues (pull requests are excluded).
func (g GitHub) Issues(ctx context.Context, labels string, since time.Time) ([]Issue, error) {
	q := "?state=open&per_page=100"
	if labels != "" {
		q += "&labels=" + url.QueryEscape(labels)
	}
	if !since.IsZero() {
		q += "&since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	}
	type rawIssue struct {
		Number int                     `json:"number"`
		Title  string                  `json:"title"`
		Body   string                  `json:"body"`
		URL    string                  `json:"html_url"`
		State  string                  `json:"state"`
		PR     *struct{}               `json:"pull_request"`
		Labels []struct{ Name string } `json:"labels"`
		User   struct{ Login string }  `json:"user"`
	}
	raw, err := listAll[rawIssue](ctx, g.API, g.p("%s", "/issues"+q))
	if err != nil {
		return nil, err
	}
	var out []Issue
	for _, r := range raw {
		if r.PR != nil {
			continue
		}
		is := Issue{Number: r.Number, Title: r.Title, Body: r.Body, URL: r.URL, State: r.State, Author: r.User.Login}
		for _, l := range r.Labels {
			is.Labels = append(is.Labels, l.Name)
		}
		out = append(out, is)
	}
	return out, nil
}

// CreateIssue is used by tests and by "create issue from a task" (approval-gated by the caller).
func (g GitHub) CreateIssue(ctx context.Context, title, body string) (Issue, error) {
	var is Issue
	_, err := g.API.Do(ctx, "POST", g.p("/issues"), map[string]string{"title": title, "body": body}, &is, "")
	return is, err
}

// CloseIssue closes an issue as completed.
func (g GitHub) CloseIssue(ctx context.Context, n int) error {
	_, err := g.API.Do(ctx, "PATCH", g.p("/issues/%d", n), map[string]string{"state": "closed", "state_reason": "completed"}, nil, "")
	return err
}

// Release creates a GitHub release (human-initiated only).
func (g GitHub) Release(ctx context.Context, tag, name, body, target string, prerelease bool) (string, error) {
	var r struct {
		URL string `json:"html_url"`
	}
	_, err := g.API.Do(ctx, "POST", g.p("/releases"), map[string]any{"tag_name": tag, "name": name, "body": body, "target_commitish": target, "prerelease": prerelease}, &r, "")
	return r.URL, err
}

// Repo describes a created repository.
type Repo struct {
	FullName string `json:"full_name"`
	CloneURL string `json:"clone_url"`
	HTMLURL  string `json:"html_url"`
}

// CreateRepo creates a repository for the authenticated user. Side effect outside the machine: callers gate it with an approval.
func CreateRepo(ctx context.Context, api API, name, description string, private bool) (Repo, error) {
	var r Repo
	_, err := api.Do(ctx, "POST", "/user/repos", map[string]any{"name": name, "description": description, "private": private, "auto_init": false}, &r, "")
	return r, err
}

// ResolveThread marks a review thread resolved (GraphQL: the one operation REST cannot do). Only the GhCLI API supports it.
func ResolveThread(ctx context.Context, api API, threadNodeID string) error {
	gq, ok := api.(interface {
		GraphQL(ctx context.Context, query string, vars map[string]string) (json.RawMessage, error)
	})
	if !ok {
		return fmt.Errorf("host: this API implementation cannot call GraphQL")
	}
	_, err := gq.GraphQL(ctx, `mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{id isResolved}}}`, map[string]string{"id": threadNodeID})
	return err
}
