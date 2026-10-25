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
	// Incomplete is what GitHub refused in an otherwise answered repository (a fine-grained token without a permission, SAML
	// on one part): each refusal as "<field>: <message>", the field being the last name in the error's path
	// ("statusCheckRollup: Resource not accessible by personal access token"), several joined by "; ". Empty when the whole
	// repository was answered. A refused field reads as its empty value (CINone, 0 open pull requests): Incomplete is how a
	// caller tells that from an answer.
	Incomplete string `json:"incomplete,omitempty"`
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
		Errors []graphQLError `json:"errors"`
	}
	body := map[string]any{"query": overviewQuery, "variables": map[string]string{"owner": owner, "name": name}}
	if _, err := api.Do(ctx, "POST", "/graphql", body, &out, ""); err != nil {
		return RepoOverview{}, err
	}
	r := out.Data.Repository
	if r == nil {
		return RepoOverview{}, overviewError(owner, name, out.Errors)
	}
	// GitHub can answer with the repository and an error for one field it refused (a fine-grained token without the
	// checks permission, SAML on one part): what it did send is kept, a refused CI rollup reads as none and refused pull
	// requests as 0, and Incomplete names what was refused, so neither is taken for an answer.
	o := RepoOverview{FullName: r.NameWithOwner, URL: r.URL, Private: r.IsPrivate, CI: CINone, OpenPRs: r.PullRequests.TotalCount}
	if len(out.Errors) > 0 {
		o.Incomplete = incomplete(out.Errors)
	}
	if b := r.DefaultBranchRef; b != nil {
		o.DefaultBranch = b.Name
		if b.Target != nil && b.Target.StatusCheckRollup != nil {
			o.CI = rollupState(b.Target.StatusCheckRollup.State)
		}
	}
	return o, nil
}

// graphQLError is one entry of a GraphQL answer's errors: GitHub's type (NOT_FOUND, FORBIDDEN, RATE_LIMITED), its message, and
// the path of the field it is about (field names and list indexes, so []any), empty for an error about the whole request.
type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

// incomplete words the errors GitHub sent beside an answered repository, for RepoOverview.Incomplete: "<field>: <message>" for
// each, the field being the last name in the error's path (a list index is not a name), the message alone when the path has
// none; several are joined by "; ".
func incomplete(errs []graphQLError) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		field := ""
		for _, p := range e.Path {
			if s, ok := p.(string); ok {
				field = s
			}
		}
		if field == "" {
			parts = append(parts, e.Message)
			continue
		}
		parts = append(parts, field+": "+e.Message)
	}
	return strings.Join(parts, "; ")
}

// overviewError is why a repository was not answered: GitHub's own words, with ErrNotFound when it says there is no such
// repository (or none the sign-in can see) and ErrRateLimited when it refused for its rate limit (GraphQL says so with a 200).
func overviewError(owner, name string, errs []graphQLError) error {
	if len(errs) == 0 {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, owner, name)
	}
	msgs := make([]string, 0, len(errs))
	notFound, limited := false, false
	for _, e := range errs {
		msgs = append(msgs, e.Message)
		notFound = notFound || e.Type == "NOT_FOUND"
		limited = limited || e.Type == "RATE_LIMITED"
	}
	err := errors.New(strings.Join(msgs, "; "))
	switch {
	case limited:
		return fmt.Errorf("%w: %s/%s: %v", ErrRateLimited, owner, name, err)
	case notFound:
		return fmt.Errorf("%w: %s/%s: %v", ErrNotFound, owner, name, err)
	}
	return err
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
