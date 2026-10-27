package core_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
)

// --- the LABELLED stand-in gh -------------------------------------------------------------------------------------------
//
// A TEST DOUBLE of the GitHub CLI: this test binary, linked as `gh` into a folder the test puts first on PATH, so the core
// finds it the way it finds the real one and runs it through the same hardened runner (host.GhCLI). It answers `gh
// --version`, `gh api -i ... /user` and `gh api -i -X POST --input - /graphql` from a fixture, and records every call. It
// never reaches GitHub.

const (
	ghStandInFixture = "DRYDOCK_GH_STANDIN_FIXTURE"
	ghStandInLog     = "DRYDOCK_GH_STANDIN_LOG"
)

type ghFixture struct {
	Login string                   `json:"login"`
	Repos map[string]ghFixtureRepo `json:"repos"`
}

type ghFixtureRepo struct {
	// Repository is the GraphQL repository object GitHub would answer with.
	Repository json.RawMessage `json:"repository,omitempty"`
	// Errors is the GraphQL errors array sent beside the repository, when GitHub refused part of it (a token without a
	// permission): the repository is answered with the refused field null, and each error names the field in its path.
	Errors json.RawMessage `json:"errors,omitempty"`
	// Fail is what gh prints on stderr before it exits 1 (GitHub answered with an error, the network failed).
	Fail string `json:"fail,omitempty"`
}

func isGhStandIn() bool {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe") == "gh" && os.Getenv(ghStandInFixture) != ""
}

func ghStandIn() int {
	var fx ghFixture
	b, err := os.ReadFile(os.Getenv(ghStandInFixture))
	if err != nil || json.Unmarshal(b, &fx) != nil {
		fmt.Fprintln(os.Stderr, "stand-in gh: no fixture")
		return 2
	}
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("gh version 2.83.1 (2025-11-13)\nhttps://github.com/cli/cli/releases/tag/v2.83.1")
		return 0
	}
	if len(args) == 0 || args[0] != "api" {
		fmt.Fprintf(os.Stderr, "stand-in gh: unexpected %q\n", args)
		return 2
	}
	path := args[len(args)-1]
	in, _ := io.ReadAll(os.Stdin)
	var req struct {
		Variables map[string]string `json:"variables"`
	}
	_ = json.Unmarshal(in, &req)
	repo := ""
	if path == "/graphql" {
		repo = req.Variables["owner"] + "/" + req.Variables["name"]
	}
	if f, err := os.OpenFile(os.Getenv(ghStandInLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		line, _ := json.Marshal(map[string]any{"args": args, "repo": repo})
		fmt.Fprintf(f, "%s\n", line)
		f.Close()
	}
	reply := func(body string) int {
		fmt.Printf("HTTP/2.0 200 OK\r\nContent-Type: application/json; charset=utf-8\r\n\r\n%s", body)
		return 0
	}
	switch path {
	case "/user":
		return reply(fmt.Sprintf(`{"login":%q}`, fx.Login))
	case "/graphql":
		r, ok := fx.Repos[repo]
		switch {
		case !ok:
			return reply(fmt.Sprintf(`{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","path":["repository"],"message":"Could not resolve to a Repository with the name '%s'."}]}`, repo))
		case r.Fail != "":
			fmt.Fprintln(os.Stderr, r.Fail)
			return 1
		case r.Errors != nil:
			return reply(fmt.Sprintf(`{"data":{"repository":%s},"errors":%s}`, r.Repository, r.Errors))
		}
		return reply(fmt.Sprintf(`{"data":{"repository":%s}}`, r.Repository))
	}
	fmt.Fprintf(os.Stderr, "stand-in gh: no answer for %s\n", path)
	return 1
}

// pathFor puts dirs, then a folder with git (and no gh), on PATH for the test.
func pathFor(t *testing.T, dirs ...string) {
	t.Helper()
	gitExe, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	gitDir := filepath.Dir(gitExe)
	for _, n := range []string{"gh", "gh.exe"} {
		if _, err := os.Stat(filepath.Join(gitDir, n)); err == nil {
			// gh lives beside git (/usr/local/bin): link git alone into a folder of its own
			own := t.TempDir()
			if err := os.Symlink(gitExe, filepath.Join(own, filepath.Base(gitExe))); err != nil {
				t.Skipf("cannot link git apart from gh: %v", err)
			}
			gitDir = own
			break
		}
	}
	t.Setenv("PATH", strings.Join(append(dirs, gitDir), string(os.PathListSeparator)))
}

// ghOnPath puts the stand-in gh first on PATH with this fixture and returns the file it records its calls in.
func ghOnPath(t *testing.T, fx ghFixture) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	if err := os.Link(exe, filepath.Join(bin, name)); err != nil {
		b, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, name), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture := filepath.Join(t.TempDir(), "gh-fixture.json")
	b, _ := json.Marshal(fx)
	if err := os.WriteFile(fixture, b, 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "gh-calls.jsonl")
	t.Setenv(ghStandInFixture, fixture)
	t.Setenv(ghStandInLog, log)
	pathFor(t, bin)
	return log
}

// ghCalls is the repositories the stand-in was asked about (one entry per GraphQL call), and how many calls it had in all.
func ghCalls(t *testing.T, log string) (repos []string, all int) {
	t.Helper()
	f, err := os.Open(log)
	if os.IsNotExist(err) {
		return nil, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c struct {
			Args []string `json:"args"`
			Repo string   `json:"repo"`
		}
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		all++
		if c.Repo != "" {
			repos = append(repos, c.Repo)
			if got := strings.Join(c.Args, " "); !strings.HasPrefix(got, "api -i -X POST") || !strings.Contains(got, "--input -") {
				t.Fatalf("a repository is read with one gh api POST, its query on stdin: gh %s", got)
			}
		}
	}
	return repos, all
}

func repoWithOrigin(t *testing.T, origin string) string {
	t.Helper()
	dir := newRepo(t)
	if origin != "" {
		gitIn(t, dir, "remote", "add", "origin", origin)
	}
	return dir
}

func rowsByProject(rs core.ProjectRepos) map[string]core.ProjectRepoRow {
	out := map[string]core.ProjectRepoRow{}
	for _, r := range rs.Rows {
		out[r.Project] = r
	}
	return out
}

func repoJSON(full, branch, rollup string, prs int, private bool) json.RawMessage {
	state := "null"
	if rollup != "" {
		state = fmt.Sprintf(`{"state":%q}`, rollup)
	}
	return json.RawMessage(fmt.Sprintf(`{"nameWithOwner":%q,"url":"https://github.com/%s","isPrivate":%t,"defaultBranchRef":{"name":%q,"target":{"statusCheckRollup":%s}},"pullRequests":{"totalCount":%d}}`,
		full, full, private, branch, state, prs))
}

// The GitHub page's Repositories table through the app's call path, with the stand-in gh on PATH: each project with its
// repository, the CI state of the default branch (passing, failing, running, none) and the open pull requests, one gh call per
// repository (two projects of one repository share it), a project without a GitHub remote or without a repository, a
// repository whose read fails, which is that row's error alone, a repository GitHub answered only in part, which names what was
// refused, and a repository folder git cannot read, which is unreadable in git's words. The answers are kept briefly; refresh
// asks again.
func TestGitHubProjectReposReadEachRepositoryOnceThroughGh(t *testing.T) {
	log := ghOnPath(t, ghFixture{Login: "kyle", Repos: map[string]ghFixtureRepo{
		"acme/green":  {Repository: repoJSON("acme/green", "main", "SUCCESS", 2, true)},
		"acme/red":    {Repository: repoJSON("acme/red", "trunk", "FAILURE", 1, false)},
		"acme/busy":   {Repository: repoJSON("acme/busy", "main", "PENDING", 0, true)},
		"acme/quiet":  {Repository: repoJSON("acme/quiet", "main", "", 0, true)},
		"acme/broken": {Fail: "gh: HTTP 502: Bad Gateway (https://api.github.com/graphql)"},
		// the repository, with its CI rollup refused (a fine-grained token without the checks permission)
		"acme/partial": {Repository: repoJSON("acme/partial", "main", "", 3, false),
			Errors: json.RawMessage(`[{"type":"FORBIDDEN","path":["repository","defaultBranchRef","target","statusCheckRollup"],"message":"Resource not accessible by personal access token"}]`)},
	}})
	a := open(t, "")
	create := func(name, origin string) string {
		var p core.Project
		repo := ""
		if origin != "-" {
			repo = repoWithOrigin(t, origin)
		}
		a.call("project.create", map[string]any{"name": name, "repoPath": repo}, &p)
		return repo
	}
	create("green", "https://github.com/acme/green.git")
	create("green-too", "git@github.com:acme/green.git")
	create("red", "https://github.com/acme/red")
	create("busy", "ssh://git@github.com/acme/busy.git")
	create("quiet", "https://github.com/acme/quiet.git")
	create("broken", "https://github.com/acme/broken.git")
	create("gone", "https://github.com/acme/gone.git")
	create("partial", "https://github.com/acme/partial.git")
	localDir := create("local", "")
	create("elsewhere", "https://gitlab.com/acme/elsewhere.git")
	create("no-repo", "-")
	// a repository folder that went away is unreadable, not a local repository and not a GitHub one
	movedDir := create("moved", "https://github.com/acme/moved.git")
	if err := os.RemoveAll(movedDir); err != nil {
		t.Fatal(err)
	}
	// a repository whose .git/config breaks later (garbage written over it) turns unreadable, in git's words, on the next read
	garbageDir := create("garbage", "")
	garbageConfig := filepath.Join(garbageDir, ".git", "config")
	goodConfig, err := os.ReadFile(garbageConfig)
	if err != nil {
		t.Fatal(err)
	}

	var st core.GitHubStatus
	a.call("github.status", nil, &st)
	if !st.Installed || !st.LoggedIn || st.Login != "kyle" || st.Version != "2.83.1" {
		t.Fatalf("status through the stand-in gh: %+v", st)
	}
	_, before := ghCalls(t, log)

	var rs core.ProjectRepos
	a.call("github.projectRepos", nil, &rs)
	if !rs.GhInstalled || len(rs.Rows) != 13 {
		t.Fatalf("table %+v", rs)
	}
	rows := rowsByProject(rs)
	check := func(name, state, repo, ci string, prs int) core.ProjectRepoRow {
		t.Helper()
		r, ok := rows[name]
		if !ok {
			t.Fatalf("no row for %s: %+v", name, rs.Rows)
		}
		if r.State != state || r.Repo != repo || r.CI != ci || r.OpenPRs != prs || r.ProjectID == "" {
			t.Fatalf("%s: %+v, want %s %s %s %d", name, r, state, repo, ci, prs)
		}
		return r
	}
	green := check("green", core.RepoStateGitHub, "acme/green", "passing", 2)
	if green.URL != "https://github.com/acme/green" || !green.Private || green.DefaultBranch != "main" || green.CheckedAt == 0 || green.Error != "" || green.Incomplete != "" {
		t.Fatalf("green %+v", green)
	}
	check("green-too", core.RepoStateGitHub, "acme/green", "passing", 2)
	if red := check("red", core.RepoStateGitHub, "acme/red", "failing", 1); red.DefaultBranch != "trunk" || red.Private {
		t.Fatalf("red: the default branch is GitHub's: %+v", red)
	}
	check("busy", core.RepoStateGitHub, "acme/busy", "running", 0)
	check("quiet", core.RepoStateGitHub, "acme/quiet", "none", 0)
	if broken := check("broken", core.RepoStateError, "acme/broken", "", 0); !strings.Contains(broken.Error, "502") || broken.URL != "https://github.com/acme/broken" {
		t.Fatalf("a failed read is the row's error, in gh's words: %+v", broken)
	}
	if gone := check("gone", core.RepoStateError, "acme/gone", "", 0); !strings.Contains(gone.Error, "no repository acme/gone") {
		t.Fatalf("a repository GitHub does not show: %+v", gone)
	}
	// a repository answered in part is a GitHub row whose refused field reads as none, with the refusal named beside it
	if partial := check("partial", core.RepoStateGitHub, "acme/partial", "none", 3); partial.Incomplete != "statusCheckRollup: Resource not accessible by personal access token" || partial.Error != "" {
		t.Fatalf("a partial answer names what GitHub refused: %+v", partial)
	}
	check("local", core.RepoStateLocal, "", "", 0)
	check("elsewhere", core.RepoStateLocal, "", "", 0)
	check("no-repo", core.RepoStateNoRepo, "", "", 0)
	check("garbage", core.RepoStateLocal, "", "", 0)
	// the folder that went away is said to be gone, in the words git.Open uses, whether or not the repository was open already;
	// nothing of Drydock's stands in front (the page puts the state word there)
	if moved := check("moved", core.RepoStateUnreadable, "", "", 0); moved.Error != "git: not a git repository: "+movedDir || moved.URL != "" {
		t.Fatalf("a folder that cannot be read: %+v", moved)
	}

	repos, all := ghCalls(t, log)
	counts := map[string]int{}
	for _, r := range repos {
		counts[r]++
	}
	want := map[string]int{"acme/green": 1, "acme/red": 1, "acme/busy": 1, "acme/quiet": 1, "acme/broken": 1, "acme/gone": 1, "acme/partial": 1}
	if fmt.Sprint(counts) != fmt.Sprint(want) || all-before != 7 {
		t.Fatalf("one gh call per repository: %v (%d calls)", counts, all-before)
	}

	// read again at once: the answers are reused, the partial one with what was refused; only the failed reads are asked again
	a.call("github.projectRepos", nil, &rs)
	if rows := rowsByProject(rs); rows["green"].CI != "passing" || rows["partial"].Incomplete != "statusCheckRollup: Resource not accessible by personal access token" {
		t.Fatalf("cached table %+v", rs)
	}
	if repos2, _ := ghCalls(t, log); len(repos2)-len(repos) != 2 {
		t.Fatalf("a fresh answer is reused, a failure asked again: %v", repos2[len(repos):])
	}
	// refresh asks GitHub again for every repository
	a.call("github.projectRepos", map[string]any{"refresh": true}, &rs)
	if repos3, _ := ghCalls(t, log); len(repos3)-len(repos)-2 != 7 {
		t.Fatalf("refresh reads every repository again: %v", repos3)
	}

	// the origin remotes are kept like GitHub's answers: a remote added since, or a configuration broken since, shows on a
	// refresh
	gitIn(t, localDir, "remote", "add", "origin", "https://github.com/acme/green.git")
	if err := os.WriteFile(garbageConfig, []byte("this is not a git configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.call("github.projectRepos", nil, &rs)
	if rows := rowsByProject(rs); rows["local"].State != core.RepoStateLocal || rows["garbage"].State != core.RepoStateLocal {
		t.Fatalf("a fresh remote is reused: %+v %+v", rows["local"], rows["garbage"])
	}
	a.call("github.projectRepos", map[string]any{"refresh": true}, &rs)
	if l := rowsByProject(rs)["local"]; l.State != core.RepoStateGitHub || l.Repo != "acme/green" {
		t.Fatalf("refresh reads the remotes again: %+v", l)
	}
	if g := rowsByProject(rs)["garbage"]; g.State != core.RepoStateUnreadable || !strings.Contains(g.Error, "config") || g.Repo != "" || g.URL != "" {
		t.Fatalf("a repository git cannot read is unreadable, in git's words: %+v", g)
	}
	// an unreadable folder is not kept: once it reads again, the next table shows it without a refresh (decoded afresh: a
	// field left out of the JSON would keep its old value in rs)
	if err := os.WriteFile(garbageConfig, goodConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	var repaired core.ProjectRepos
	a.call("github.projectRepos", nil, &repaired)
	if g := rowsByProject(repaired)["garbage"]; g.State != core.RepoStateLocal || g.Error != "" {
		t.Fatalf("a repaired folder reads again at once: %+v", g)
	}
}

// Without gh the table still lists every project and its repository, marked as needing gh, and nothing is run.
func TestGitHubProjectReposWithoutGh(t *testing.T) {
	pathFor(t) // git only
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "green", "repoPath": repoWithOrigin(t, "https://github.com/acme/green.git")}, &p)
	a.call("project.create", map[string]any{"name": "local", "repoPath": repoWithOrigin(t, "")}, &p)
	var st core.GitHubStatus
	a.call("github.status", nil, &st)
	if st.Installed {
		t.Fatalf("gh must not be found: %+v", st)
	}
	var rs core.ProjectRepos
	start := time.Now()
	a.call("github.projectRepos", nil, &rs)
	rows := rowsByProject(rs)
	if rs.GhInstalled || len(rs.Rows) != 2 {
		t.Fatalf("table %+v", rs)
	}
	if g := rows["green"]; g.State != core.RepoStateNeedsGh || g.Repo != "acme/green" || g.URL != "https://github.com/acme/green" || g.CI != "" || g.CheckedAt != 0 {
		t.Fatalf("green without gh: %+v", g)
	}
	if l := rows["local"]; l.State != core.RepoStateLocal {
		t.Fatalf("local: %+v", l)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("without gh the table must not wait for anything (%s)", time.Since(start))
	}
}

// No project: an empty table, not an error.
func TestGitHubProjectReposEmpty(t *testing.T) {
	a := open(t, "")
	var rs core.ProjectRepos
	a.call("github.projectRepos", nil, &rs)
	if rs.Rows == nil || len(rs.Rows) != 0 {
		t.Fatalf("empty table %+v", rs)
	}
	if _, err := a.c.Call(context.Background(), "github.projectRepos", []byte(`{"refresh":"yes"}`)); err == nil {
		t.Fatal("a refresh that is not a boolean must be refused")
	}
}
