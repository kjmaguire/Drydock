package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A PROTOCOL FIXTURE of GitHub's GraphQL endpoint: one repository per name, in the shapes GitHub documents for
// Repository.defaultBranchRef, Commit.statusCheckRollup and Repository.pullRequests.
func TestOverviewReadsCIAndPullRequestsInOneRequest(t *testing.T) {
	repos := map[string]string{
		"acme/green":  `{"nameWithOwner":"acme/green","url":"https://github.com/acme/green","isPrivate":true,"defaultBranchRef":{"name":"main","target":{"statusCheckRollup":{"state":"SUCCESS"}}},"pullRequests":{"totalCount":2}}`,
		"acme/red":    `{"nameWithOwner":"acme/red","url":"https://github.com/acme/red","isPrivate":false,"defaultBranchRef":{"name":"trunk","target":{"statusCheckRollup":{"state":"FAILURE"}}},"pullRequests":{"totalCount":0}}`,
		"acme/broken": `{"nameWithOwner":"acme/broken","url":"https://github.com/acme/broken","defaultBranchRef":{"name":"main","target":{"statusCheckRollup":{"state":"ERROR"}}},"pullRequests":{"totalCount":1}}`,
		"acme/busy":   `{"nameWithOwner":"acme/busy","url":"https://github.com/acme/busy","defaultBranchRef":{"name":"main","target":{"statusCheckRollup":{"state":"PENDING"}}},"pullRequests":{"totalCount":5}}`,
		"acme/wait":   `{"nameWithOwner":"acme/wait","url":"https://github.com/acme/wait","defaultBranchRef":{"name":"main","target":{"statusCheckRollup":{"state":"EXPECTED"}}},"pullRequests":{"totalCount":0}}`,
		"acme/quiet":  `{"nameWithOwner":"acme/quiet","url":"https://github.com/acme/quiet","defaultBranchRef":{"name":"main","target":{"statusCheckRollup":null}},"pullRequests":{"totalCount":0}}`,
		"acme/empty":  `{"nameWithOwner":"acme/empty","url":"https://github.com/acme/empty","defaultBranchRef":null,"pullRequests":{"totalCount":0}}`,
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/graphql" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var req struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &req); err != nil {
			t.Errorf("body %s: %v", b, err)
		}
		for _, want := range []string{"statusCheckRollup", "pullRequests(states:OPEN)", "defaultBranchRef"} {
			if !strings.Contains(req.Query, want) {
				t.Errorf("query lacks %s: %s", want, req.Query)
			}
		}
		full := req.Variables["owner"] + "/" + req.Variables["name"]
		switch full {
		case "acme/partial": // the repository, with the CI rollup refused (a token without the checks permission)
			fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"acme/partial","url":"https://github.com/acme/partial","defaultBranchRef":{"name":"main","target":{"statusCheckRollup":null}},"pullRequests":{"totalCount":3}}},"errors":[{"type":"FORBIDDEN","path":["repository","defaultBranchRef","target","statusCheckRollup"],"message":"Resource not accessible by personal access token"}]}`)
			return
		case "acme/noprs": // the repository, with its pull requests refused (a token without the pull requests permission)
			fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"acme/noprs","url":"https://github.com/acme/noprs","defaultBranchRef":{"name":"main","target":{"statusCheckRollup":{"state":"SUCCESS"}}},"pullRequests":null}},"errors":[{"type":"FORBIDDEN","path":["repository","pullRequests"],"message":"Resource not accessible by personal access token","locations":[{"line":1,"column":180}]}]}`)
			return
		case "acme/twice": // two fields refused, one error without a path: every refusal is named
			fmt.Fprint(w, `{"data":{"repository":{"nameWithOwner":"acme/twice","url":"https://github.com/acme/twice","defaultBranchRef":{"name":"main","target":{"statusCheckRollup":null}},"pullRequests":null}},"errors":[{"type":"FORBIDDEN","path":["repository","defaultBranchRef","target","statusCheckRollup"],"message":"Resource not accessible by personal access token"},{"type":"FORBIDDEN","path":["repository","pullRequests",0],"message":"Resource not accessible by personal access token"},{"message":"Something went wrong while executing your query."}]}`)
			return
		case "acme/limited": // GraphQL's primary limit is a 200 with an error of its own type
			fmt.Fprint(w, `{"data":null,"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded for user ID 1."}]}`)
			return
		case "acme/refused":
			fmt.Fprint(w, `{"data":{"repository":null},"errors":[{"type":"FORBIDDEN","message":"SAML enforcement"}]}`)
			return
		}
		repo, ok := repos[full]
		if !ok {
			fmt.Fprintf(w, `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","path":["repository"],"message":"Could not resolve to a Repository with the name '%s'."}]}`, full)
			return
		}
		fmt.Fprintf(w, `{"data":{"repository":%s}}`, repo)
	}))
	defer srv.Close()
	api := HTTPAPI{BaseURL: srv.URL, Token: "fixture"}
	want := map[string]RepoOverview{
		"acme/green":  {FullName: "acme/green", URL: "https://github.com/acme/green", Private: true, DefaultBranch: "main", CI: CIPassing, OpenPRs: 2},
		"acme/red":    {FullName: "acme/red", URL: "https://github.com/acme/red", DefaultBranch: "trunk", CI: CIFailing},
		"acme/broken": {FullName: "acme/broken", URL: "https://github.com/acme/broken", DefaultBranch: "main", CI: CIFailing, OpenPRs: 1},
		"acme/busy":   {FullName: "acme/busy", URL: "https://github.com/acme/busy", DefaultBranch: "main", CI: CIRunning, OpenPRs: 5},
		"acme/wait":   {FullName: "acme/wait", URL: "https://github.com/acme/wait", DefaultBranch: "main", CI: CIRunning},
		"acme/quiet":  {FullName: "acme/quiet", URL: "https://github.com/acme/quiet", DefaultBranch: "main", CI: CINone},
		"acme/empty":  {FullName: "acme/empty", URL: "https://github.com/acme/empty", CI: CINone},
	}
	for full, w := range want {
		owner, name, _ := strings.Cut(full, "/")
		got, err := Overview(context.Background(), api, owner, name)
		if err != nil || got != w {
			t.Errorf("%s: %+v %v, want %+v", full, got, err, w)
		}
	}
	if calls != len(want) {
		t.Fatalf("%d requests for %d repositories: one each", calls, len(want))
	}
	// a repository answered in full names nothing as refused
	if got, _ := Overview(context.Background(), api, "acme", "green"); got.Incomplete != "" {
		t.Fatalf("a whole answer: %+v", got)
	}
	// an error for one field keeps the rest of the answer, and the refused field is named, so "no CI" is not taken for an answer
	if got, err := Overview(context.Background(), api, "acme", "partial"); err != nil || got != (RepoOverview{FullName: "acme/partial", URL: "https://github.com/acme/partial", DefaultBranch: "main", CI: CINone, OpenPRs: 3, Incomplete: "statusCheckRollup: Resource not accessible by personal access token"}) {
		t.Fatalf("a partial answer: %+v %v", got, err)
	}
	// refused pull requests read as none open, with the refusal named beside them
	if got, err := Overview(context.Background(), api, "acme", "noprs"); err != nil || got != (RepoOverview{FullName: "acme/noprs", URL: "https://github.com/acme/noprs", DefaultBranch: "main", CI: CIPassing, OpenPRs: 0, Incomplete: "pullRequests: Resource not accessible by personal access token"}) {
		t.Fatalf("refused pull requests: %+v %v", got, err)
	}
	// several refusals are all named: the last name in each path, and the message alone for an error with no path
	if got, err := Overview(context.Background(), api, "acme", "twice"); err != nil || got.Incomplete != "statusCheckRollup: Resource not accessible by personal access token; pullRequests: Resource not accessible by personal access token; Something went wrong while executing your query." {
		t.Fatalf("two refusals: %+v %v", got, err)
	}
	if _, err := Overview(context.Background(), api, "acme", "limited"); !errors.Is(err, ErrRateLimited) || !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Fatalf("GraphQL's rate limit: %v", err)
	}
	if _, err := Overview(context.Background(), api, "acme", "refused"); err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) || !strings.Contains(err.Error(), "SAML") {
		t.Fatalf("no repository and GitHub's reason: %v", err)
	}
	if _, err := Overview(context.Background(), api, "acme", "gone"); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "Could not resolve") {
		t.Fatalf("a missing repository: %v", err)
	}
}

// Through the user's gh (the LABELED TEST DOUBLE gh of TestMain records its command line): one `gh api -i -X POST /graphql`
// with the query on stdin, so nothing is put on the command line but the path.
func TestOverviewGoesThroughGhAPI(t *testing.T) {
	log := filepath.Join(t.TempDir(), "gh.jsonl")
	t.Setenv("DRYDOCK_FAKE_GH_LOG", log)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// the double answers {}: no repository, which is "not found"
	if _, err := Overview(context.Background(), GhCLI{Bin: exe}, "o", "r"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty answer: %v", err)
	}
	calls := readGhCalls(t, log)
	if len(calls) != 1 {
		t.Fatalf("gh ran %d times, want 1", len(calls))
	}
	args := strings.Join(calls[0].Args, " ")
	if !strings.HasPrefix(args, "api -i -X POST") || calls[0].Args[len(calls[0].Args)-1] != "/graphql" || !strings.Contains(args, "--input -") {
		t.Fatalf("gh %s", args)
	}
	var body struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}
	if err := json.Unmarshal(calls[0].Stdin, &body); err != nil || body.Variables["owner"] != "o" || body.Variables["name"] != "r" || !strings.Contains(body.Query, "statusCheckRollup") {
		t.Fatalf("stdin %s: %v", calls[0].Stdin, err)
	}
}
