package contract

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// The GitHub CLI contracts (GH-*). They read a public repository through the installed gh with the user's own sign-in and
// write nothing; they cost nothing. The repository is GITHUB_REPOSITORY in a GitHub Actions job, DRYDOCK_CONTRACT_GH_REPO when
// set, else Drydock's own. Its pull request DRYDOCK_CONTRACT_GH_PR (default: the most recently updated one) is read.

func installedGh(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("gh")
	if err != nil {
		t.Skip("gh is not installed")
	}
	return path
}

func ghRepo() (owner, name string) {
	r := os.Getenv("DRYDOCK_CONTRACT_GH_REPO")
	if r == "" {
		r = os.Getenv("GITHUB_REPOSITORY")
	}
	if r == "" {
		r = "kjmaguire/Drydock"
	}
	owner, name, _ = strings.Cut(r, "/")
	return owner, name
}

// signedInGitHub is a GitHub client over the installed gh; the test is skipped when gh is not signed in.
func signedInGitHub(t *testing.T) host.GitHub {
	t.Helper()
	installedGh(t)
	owner, name := ghRepo()
	g := host.GitHub{API: host.GhCLI{Timeout: 30 * time.Second}, Owner: owner, Repo: name}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var repo struct {
		FullName string `json:"full_name"`
	}
	if _, err := g.API.Do(ctx, "GET", "/repos/"+owner+"/"+name, nil, &repo, ""); errors.Is(err, host.ErrAuth) {
		t.Skip("gh is not signed in")
	} else if err != nil {
		t.Fatalf("read %s/%s through gh: %v", owner, name, err)
	}
	return g
}

// GH-AUTH-01: without a sign-in, gh api exits with code 4, and Drydock's client reports host.ErrAuth for it (which the app shows
// as "gh is not signed in"), whatever the message.
func TestGhSignedOutExitsWithCodeFour(t *testing.T) {
	gh := installedGh(t)
	cfg := t.TempDir()
	t.Setenv("GH_CONFIG_DIR", cfg)
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "api", "user")
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 4 {
		t.Fatalf("signed-out gh api user: %v (want exit code 4): %s", err, out)
	}
	t.Logf("contract-note: signed-out gh says: %s", strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]))
	if _, err := (host.GhCLI{Bin: gh}).Do(ctx, "GET", "/user", nil, nil, ""); !errors.Is(err, host.ErrAuth) {
		t.Fatalf("Drydock's client: %v, want host.ErrAuth", err)
	}
}

// GH-API-01: gh api -i prints the status line and headers before the body, the response carries an ETag, and a request that
// sends it back with If-None-Match is answered 304 through gh (Drydock's conditional polling of pull requests and checks).
func TestGhAPIIncludeHeadersAndConditionalRequests(t *testing.T) {
	g := signedInGitHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path := "/repos/" + g.Owner + "/" + g.Repo
	var repo struct {
		FullName string `json:"full_name"`
	}
	etag, err := g.API.Do(ctx, "GET", path, nil, &repo, "")
	if err != nil || etag == "" || !strings.EqualFold(repo.FullName, g.Owner+"/"+g.Repo) {
		t.Fatalf("first read: etag %q, body %+v, %v", etag, repo, err)
	}
	if _, err := g.API.Do(ctx, "GET", path, nil, &repo, etag); !errors.Is(err, host.ErrNotModified) {
		t.Fatalf("a read with If-None-Match: %v, want host.ErrNotModified (304)", err)
	}
}

// GH-REST-01: the REST fields Drydock's GitHub flow reads are there with the types it decodes: a pull request's
// mergeable_state, the app slug of each check run, and the author_association of reviews and comments.
func TestGhRESTFieldsDrydockReads(t *testing.T) {
	g := signedInGitHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	n := 0
	if v := os.Getenv("DRYDOCK_CONTRACT_GH_PR"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil {
			t.Fatalf("DRYDOCK_CONTRACT_GH_PR=%q: %v", v, err)
		}
	} else {
		var prs []struct {
			Number int `json:"number"`
		}
		if _, err := g.API.Do(ctx, "GET", "/repos/"+g.Owner+"/"+g.Repo+"/pulls?state=all&sort=updated&direction=desc&per_page=1", nil, &prs, ""); err != nil || len(prs) == 0 {
			t.Skipf("no pull request to read in %s/%s: %v", g.Owner, g.Repo, err)
		}
		n = prs[0].Number
	}
	// mergeable_state: a raw read shows the key is present, and Drydock's PR type decodes it
	var raw map[string]any
	if _, err := g.API.Do(ctx, "GET", "/repos/"+g.Owner+"/"+g.Repo+"/pulls/"+strconv.Itoa(n), nil, &raw, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["mergeable_state"].(string); !ok {
		t.Fatalf("pull %d has no string mergeable_state: %T", n, raw["mergeable_state"])
	}
	pr, _, err := g.GetPR(ctx, n, "")
	if err != nil || pr.Number != n || pr.MergeableState == "" || pr.Head.SHA == "" {
		t.Fatalf("GetPR: %+v %v", pr, err)
	}
	t.Logf("contract-note: pull %d mergeable_state=%s (%s)", n, pr.MergeableState, pr.MergeStatus())
	// check runs: every one names its app
	st, err := g.Checks(ctx, pr.Head.SHA)
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	for _, r := range st.Runs {
		if r.Kind != host.KindCheckRun {
			continue
		}
		runs++
		if r.AppSlug == "" {
			t.Errorf("check run %q has no app.slug", r.Name)
		}
	}
	t.Logf("contract-note: %d check runs on %s", runs, pr.Head.SHA[:7])
	// reviews and comments: every one carries an author_association
	evs, err := g.Reviews(ctx, n, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Association == "" {
			t.Errorf("%s %d has no author_association", e.Kind, e.ID)
		}
	}
	t.Logf("contract-note: %d reviews and comments on pull %d", len(evs), n)
}
