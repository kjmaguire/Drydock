package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// GitHub calls beyond the listing (decision P122): one issue by number (a webhook names it) and the files in an issue's body.
// Same gh (or Sign in with GitHub) client, credential and failure handling as sync; written from GitHub's REST documentation
// (GET /repos/{owner}/{repo}/issues/{issue_number}).

// ghIssue is an issue as GET /repos/{owner}/{repo}/issues/{issue_number} returns it.
type ghIssue struct {
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

// item is the issue as sync stores it (the same fields Page reads).
func (r ghIssue) item(repo string) RawItem {
	it := RawItem{ExternalID: fmt.Sprintf("%s#%d", strings.ToLower(repo), r.Number), Kind: ItemIssue, IssueRef: fmt.Sprintf("#%d", r.Number), URL: r.URL,
		Title: r.Title, Body: r.Body, State: r.State, Created: millis(r.CreatedAt), Updated: millis(r.UpdatedAt), Etag: r.UpdatedAt,
		Meta: map[string]any{"repo": repo, "author": r.User.Login}, Closed: r.State == "closed"}
	for _, l := range r.Labels {
		it.Labels = append(it.Labels, l.Name)
	}
	if r.Assignee != nil {
		it.Assignee = r.Assignee.Login
	}
	return it
}

// splitGitHubID splits an item id ("owner/repo#12") into the repository and the issue number.
func splitGitHubID(id string) (repo string, number int, ok bool) {
	repo, n, found := strings.Cut(id, "#")
	if !found || !host.ValidFullName(repo) {
		return "", 0, false
	}
	number, err := strconv.Atoi(n)
	if err != nil || number <= 0 {
		return "", 0, false
	}
	return repo, number, true
}

func (githubAdapter) issue(ctx context.Context, a *API, repo string, n int) (*ghIssue, error) {
	if a.GH == nil {
		return nil, &Failure{Code: CodeUnreachable, Message: "The GitHub CLI (gh) is not installed."}
	}
	var r ghIssue
	if _, err := a.GH.Do(ctx, "GET", fmt.Sprintf("/repos/%s/issues/%d", repo, n), nil, &r, ""); err != nil {
		if errors.Is(err, host.ErrNotFound) {
			return nil, nil // deleted, transferred, or never there
		}
		return nil, ghFailure(a.Conn, err)
	}
	return &r, nil
}

// Fetch reads one issue and says whether it is in the assignment's scope: its repository, every label the assignment filters
// on, and not a pull request. A closed issue is in scope and read as closed, as sync reads it.
func (g githubAdapter) Fetch(ctx context.Context, a *API, cfg map[string]string, id string) (RawItem, bool, error) {
	repo, n, ok := splitGitHubID(id)
	if !ok || !strings.EqualFold(repo, cfg["repo"]) {
		return RawItem{}, false, nil
	}
	r, err := g.issue(ctx, a, cfg["repo"], n)
	if err != nil || r == nil {
		return RawItem{}, false, err
	}
	if r.PR != nil || r.Number != n {
		return RawItem{}, false, nil
	}
	it := r.item(cfg["repo"])
	for _, want := range strings.Split(cfg["labels"], ",") {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		found := false
		for _, have := range it.Labels {
			found = found || strings.EqualFold(have, want)
		}
		if !found {
			return it, false, nil
		}
	}
	return it, true, nil
}

// GitHub has no attachment API: a file a person adds to an issue is uploaded to GitHub and its address written into the body
// (an image as ![name](address), any other file as [name](address)). The addresses of GitHub's uploads, as GitHub writes them
// today and wrote them before: github.com/user-attachments/assets/<uuid> and /files/<id>/<name>, github.com/<owner>/<repo>/
// files/<id>/<name>, and user-images.githubusercontent.com / private-user-images.githubusercontent.com. Those are listed as
// downloadable files; every other link in the body is a link and never fetched. A download goes through the guarded client
// WITHOUT any credential (a gh token is never sent to a file host), so a file of a public repository arrives and a file of a
// private one, which GitHub serves only to a signed-in browser, is refused with a plain message. From GitHub's documentation
// of attaching files and from the addresses GitHub writes; unverified against a live private repository.

var (
	mdLinkRe  = regexp.MustCompile(`(!?)\[([^\]\n]{0,200})\]\((https?://[^\s)]{1,1000})\)`)
	htmlSrcRe = regexp.MustCompile(`<img[^>]{0,500}?\ssrc="(https?://[^"\s]{1,1000})"`)
)

// githubUpload reports whether u is a file uploaded to GitHub.
func githubUpload(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	switch h {
	case "user-images.githubusercontent.com", "private-user-images.githubusercontent.com":
		return true
	case "github.com":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "user-attachments" && (parts[1] == "assets" || parts[1] == "files") {
			return true
		}
		return len(parts) >= 4 && parts[2] == "files"
	}
	return false
}

// githubBodyFiles lists the links of an issue body: GitHub's uploads (downloadable) and the other links, in the order they
// appear, each once.
func githubBodyFiles(body string) []RawAttachment {
	out := []RawAttachment{}
	seen := map[string]bool{}
	add := func(name, raw string) {
		u, err := url.Parse(raw)
		if err != nil || seen[u.String()] || len(out) >= 100 {
			return
		}
		seen[u.String()] = true
		up := githubUpload(u)
		if name == "" {
			name = u.Path[strings.LastIndex(u.Path, "/")+1:]
		}
		out = append(out, RawAttachment{ExternalID: "body:" + hashHex(u.String())[:24], Name: firstNonEmpty(name, u.Hostname()), URL: u.String(), Downloadable: up, Source: "issue body"})
	}
	type hit struct {
		at        int
		name, url string
	}
	var hits []hit
	for _, m := range mdLinkRe.FindAllStringSubmatchIndex(body, -1) {
		hits = append(hits, hit{m[0], body[m[4]:m[5]], body[m[6]:m[7]]})
	}
	for _, m := range htmlSrcRe.FindAllStringSubmatchIndex(body, -1) {
		hits = append(hits, hit{m[0], "", body[m[2]:m[3]]})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	for _, h := range hits {
		add(strings.TrimSpace(h.name), h.url)
	}
	return out
}

// Attachments lists the files and links of the issue's body, read again from GitHub.
func (g githubAdapter) Attachments(ctx context.Context, a *API, cfg map[string]string, it Item) ([]RawAttachment, error) {
	repo, n, ok := splitGitHubID(it.ExternalID)
	if !ok {
		return nil, &Failure{Code: CodeNotFound, Message: "This is not a GitHub issue Drydock can read."}
	}
	r, err := g.issue(ctx, a, repo, n)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, &Failure{Code: CodeNotFound, Message: "GitHub did not find the issue."}
	}
	return githubBodyFiles(r.Body), nil
}

// FetchAttachment downloads a file uploaded to GitHub, found again in the issue's current body, without any credential.
func (g githubAdapter) FetchAttachment(ctx context.Context, a *API, cfg map[string]string, it Item, att RawAttachment, max int64) ([]byte, string, error) {
	if !att.Downloadable {
		return nil, "", &Failure{Code: CodeOther, Message: "This is a link to another site, not a file uploaded to GitHub."}
	}
	repo, n, ok := splitGitHubID(it.ExternalID)
	if !ok {
		return nil, "", &Failure{Code: CodeNotFound, Message: "This is not a GitHub issue Drydock can read."}
	}
	r, err := g.issue(ctx, a, repo, n)
	if err != nil {
		return nil, "", err
	}
	if r == nil {
		return nil, "", &Failure{Code: CodeNotFound, Message: "GitHub did not find the issue."}
	}
	for _, f := range githubBodyFiles(r.Body) {
		if f.ExternalID == att.ExternalID && f.Downloadable {
			b, ct, err := a.fetchFile(ctx, f.URL, "", max)
			var fl *Failure
			if errors.As(err, &fl) && strings.Contains(fl.Message, "HTTP 404") {
				fl.Message = "GitHub did not send the file (HTTP 404). GitHub serves a private repository's files only to a signed-in browser: open it on GitHub."
			}
			return b, ct, err
		}
	}
	return nil, "", &Failure{Code: CodeNotFound, Message: "The issue no longer links this file."}
}
