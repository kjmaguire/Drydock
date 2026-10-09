package host

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Account is the GitHub user `gh` is signed in as.
type Account struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

// RepoSummary is one repository the user can reach, for choosing a project's repository.
type RepoSummary struct {
	FullName      string `json:"fullName"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
	DefaultBranch string `json:"defaultBranch"`
	HTMLURL       string `json:"htmlUrl"`
	PushedAt      string `json:"pushedAt"`
}

// Viewer returns the signed-in account.
func Viewer(ctx context.Context, api API) (Account, error) {
	var a Account
	_, err := api.Do(ctx, "GET", "/user", nil, &a, "")
	return a, err
}

// Orgs lists the organizations the signed-in user belongs to (logins, sorted).
func Orgs(ctx context.Context, api API) ([]string, error) {
	var raw []struct {
		Login string `json:"login"`
	}
	if _, err := api.Do(ctx, "GET", "/user/orgs?per_page=100", nil, &raw, ""); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, o := range raw {
		out = append(out, o.Login)
	}
	sort.Strings(out)
	return out, nil
}

// MaxRepoPages bounds ListRepos (100 repositories per page).
const MaxRepoPages = 5

// ListRepos lists the repositories the signed-in user can reach (own, collaborator, organization member), most recently pushed
// first. owner narrows it to one account or organization; query keeps repositories whose name or description contains it
// (case-insensitive). Archived repositories are left out: agents cannot push to them.
func ListRepos(ctx context.Context, api API, owner, query string) ([]RepoSummary, error) {
	type raw struct {
		FullName      string `json:"full_name"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		Private       bool   `json:"private"`
		Archived      bool   `json:"archived"`
		Fork          bool   `json:"fork"`
		DefaultBranch string `json:"default_branch"`
		HTMLURL       string `json:"html_url"`
		PushedAt      string `json:"pushed_at"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := []RepoSummary{}
	for page := 1; page <= MaxRepoPages; page++ {
		var rs []raw
		path := fmt.Sprintf("/user/repos?per_page=100&sort=pushed&affiliation=%s&page=%d", url.QueryEscape("owner,collaborator,organization_member"), page)
		if _, err := api.Do(ctx, "GET", path, nil, &rs, ""); err != nil {
			return nil, err
		}
		for _, r := range rs {
			if r.Archived || (owner != "" && !strings.EqualFold(r.Owner.Login, owner)) {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(r.FullName), q) && !strings.Contains(strings.ToLower(r.Description), q) {
				continue
			}
			out = append(out, RepoSummary{FullName: r.FullName, Owner: r.Owner.Login, Name: r.Name, Description: r.Description, Private: r.Private,
				Archived: r.Archived, Fork: r.Fork, DefaultBranch: r.DefaultBranch, HTMLURL: r.HTMLURL, PushedAt: r.PushedAt})
		}
		if len(rs) < 100 {
			break
		}
	}
	return out, nil
}

var fullNameRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)

// ValidFullName reports whether s is an owner/name a clone may be asked for (no URL, no path tricks).
func ValidFullName(s string) bool {
	if !fullNameRe.MatchString(s) {
		return false
	}
	name := s[strings.IndexByte(s, '/')+1:]
	return name != "." && name != ".." && !strings.HasSuffix(name, ".git")
}
