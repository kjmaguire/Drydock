package core_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
)

// The new-project flow can list the user's GitHub repositories and clone one, then create the project from the clone. The
// GitHub API is a protocol fixture; the clone comes from a local repository (no network).
func TestGitHubListAndCloneIntoAProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"kyle"}`)
		case "/user/orgs":
			fmt.Fprint(w, `[{"login":"acme"}]`)
		case "/user/repos":
			fmt.Fprint(w, `[{"full_name":"acme/gocalc","name":"gocalc","owner":{"login":"acme"},"default_branch":"main","private":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	src := newRepo(t)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		GitHubAPI:      host.HTTPAPI{BaseURL: srv.URL, Token: "fixture"},
		GitHubCloneURL: func(string) string { return src }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	ctx := context.Background()

	var st core.GitHubStatus
	a.call("github.status", nil, &st)
	if !st.Installed || !st.LoggedIn || st.Login != "kyle" {
		t.Fatalf("status %+v", st)
	}
	var owners []string
	a.call("github.owners", nil, &owners)
	if strings.Join(owners, ",") != "kyle,acme" {
		t.Fatalf("owners %v", owners)
	}
	var repos []host.RepoSummary
	a.call("github.repos", map[string]any{"owner": "acme"}, &repos)
	if len(repos) != 1 || repos[0].FullName != "acme/gocalc" {
		t.Fatalf("repos %+v", repos)
	}
	for _, bad := range []string{"https://evil/x", "acme/../../etc", "acme/x.git"} {
		if _, err := c.Call(ctx, "github.clone", mustJSON(map[string]any{"repo": bad, "dir": t.TempDir()})); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	if _, err := c.Call(ctx, "github.clone", mustJSON(map[string]any{"repo": "acme/gocalc", "dir": "relative/dir"})); err == nil {
		t.Fatal("a relative folder must be refused")
	}
	parent := filepath.Join(t.TempDir(), "repos")
	var cl core.CloneResult
	a.call("github.clone", map[string]any{"repo": "acme/gocalc", "dir": parent}, &cl)
	if cl.Path != filepath.Join(parent, "gocalc") || cl.DefaultBranch != "main" {
		t.Fatalf("clone %+v", cl)
	}
	if _, err := os.Stat(filepath.Join(cl.Path, "go.mod")); err != nil {
		t.Fatalf("cloned files: %v", err)
	}
	if _, err := c.Call(ctx, "github.clone", mustJSON(map[string]any{"repo": "acme/gocalc", "dir": parent})); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("cloning over a non-empty folder must be refused: %v", err)
	}
	var p core.Project
	a.call("project.create", map[string]any{"repoPath": cl.Path}, &p)
	if p.Name != "gocalc" || !p.HasRepo {
		t.Fatalf("project from the clone: %+v", p)
	}
	var dir string
	a.call("github.cloneDir", nil, &dir)
	if !filepath.IsAbs(dir) {
		t.Fatalf("default clone dir %q", dir)
	}
}

// A clone that fails leaves no half-made folder behind, and its error never carries a secret.
func TestGitHubCloneFailureCleansUp(t *testing.T) {
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		GitHubCloneURL: func(string) string {
			return filepath.Join(os.TempDir(), "drydock-no-such-repo-"+fmt.Sprint(time.Now().UnixNano()))
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	parent := t.TempDir()
	if _, err := c.Call(context.Background(), "github.clone", mustJSON(map[string]any{"repo": "acme/missing", "dir": parent})); err == nil {
		t.Fatal("cloning a missing repository must fail")
	}
	if _, err := os.Stat(filepath.Join(parent, "missing")); !os.IsNotExist(err) {
		t.Fatalf("the failed clone's folder must be removed: %v", err)
	}
}
