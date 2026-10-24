package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The CI state of a repository's default branch, as the GitHub page's Repositories table shows it.
const (
	CIPassing = "passing" // every check of the latest commit passed
	CIFailing = "failing" // a check failed, errored or was cancelled
	CIRunning = "running" // a check has not finished (or a required one has not reported yet)
	CINone    = "none"    // nothing reported on the commit, or the repository has no commits
)

// RepoOverview is what one call tells about a repository: its default branch, the CI state of that branch's latest commit
// and how many pull requests are open.
type RepoOverview struct {
	FullName      string `json:"fullName"`
	URL           string `json:"url"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"defaultBranch"`
	CI            string `json:"ci"`
	OpenPRs       int    `json:"openPullRequests"`
}

// overviewQuery reads everything in one request: GraphQL's statusCheckRollup summarizes the commit's check runs and commit
// statuses (REST would need a call for each, and one more for the pull requests).
const overviewQuery = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){nameWithOwner url isPrivate defaultBranchRef{name target{... on Commit{statusCheckRollup{state}}}} pullRequests(states:OPEN){totalCount}}}`

// Overview reads a repository's default branch, its CI state and its open pull requests in one GraphQL request through api
// (`gh api /graphql` for GhCLI: the user's own sign-in, nothing stored by Drydock).
func Overview(ctx context.Context, api API, owner, name string) (RepoOverview, error) {
	var out struct {
		Data struct {
			Repository *struct {
				NameWithOwner    string `json:"nameWithOwner"`
				URL              string `json:"url"`
				IsPrivate        bool   `json:"isPrivate"`
				DefaultBranchRef *struct {
					Name   string `json:"name"`
					Target *struct {
						StatusCheckRollup *struct {
							State string `json:"state"`
						} `json:"statusCheckRollup"`
					} `json:"target"`
				} `json:"defaultBranchRef"`
				PullRequests struct {
					TotalCount int `json:"totalCount"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	body := map[string]any{"query": overviewQuery, "variables": map[string]string{"owner": owner, "name": name}}
	if _, err := api.Do(ctx, "POST", "/graphql", body, &out, ""); err != nil {
		return RepoOverview{}, err
	}
	if len(out.Errors) > 0 {
		msgs := make([]string, 0, len(out.Errors))
		notFound := false
		for _, e := range out.Errors {
			msgs = append(msgs, e.Message)
			notFound = notFound || e.Type == "NOT_FOUND"
		}
		err := errors.New(strings.Join(msgs, "; "))
		if notFound {
			err = fmt.Errorf("%w: %s/%s: %v", ErrNotFound, owner, name, err)
		}
		return RepoOverview{}, err
	}
	r := out.Data.Repository
	if r == nil {
		return RepoOverview{}, fmt.Errorf("%w: %s/%s", ErrNotFound, owner, name)
	}
	o := RepoOverview{FullName: r.NameWithOwner, URL: r.URL, Private: r.IsPrivate, CI: CINone, OpenPRs: r.PullRequests.TotalCount}
	if b := r.DefaultBranchRef; b != nil {
		o.DefaultBranch = b.Name
		if b.Target != nil && b.Target.StatusCheckRollup != nil {
			o.CI = rollupState(b.Target.StatusCheckRollup.State)
		}
	}
	return o, nil
}

// rollupState maps GraphQL's StatusState (EXPECTED, ERROR, FAILURE, PENDING, SUCCESS) to the table's words.
func rollupState(s string) string {
	switch s {
	case "SUCCESS":
		return CIPassing
	case "FAILURE", "ERROR":
		return CIFailing
	case "PENDING", "EXPECTED":
		return CIRunning
	}
	return CINone
}
