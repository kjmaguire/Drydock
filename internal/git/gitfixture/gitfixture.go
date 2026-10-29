// Package gitfixture is a PROTOCOL FIXTURE for tests: a local git server speaking git's smart HTTP protocol through git's own
// `git http-backend` (net/http/cgi), over a repository the test fills. It exists so a docs fetch is tested against real git
// on both ends without the network; it is not a hosting service and proves nothing about GitHub's or another host's limits.
package gitfixture

import (
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Server serves one repository at <URL>/repo.git.
type Server struct {
	*httptest.Server
	Root string // the folder that holds repo.git (a working repository; http-backend serves its .git)
	t    testing.TB
}

// New makes a repository with files (path -> content) committed on branch main and serves it.
func New(t testing.TB, files map[string]string) *Server {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo.git")
	s := &Server{Root: root, t: t}
	s.git(root, "init", "-q", "-b", "main", repo)
	s.git(repo, "config", "uploadpack.allowFilter", "true")
	s.git(repo, "config", "uploadpack.allowAnySHA1InWant", "true")
	s.git(repo, "config", "http.receivepack", "false")
	s.Commit(files, "first")
	exe, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatalf("git --exec-path: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(exe)), "git-http-backend")
	if _, err := os.Stat(backend + ".exe"); err == nil {
		backend += ".exe" // Git for Windows; net/http/cgi runs the path as given
	}
	h := &cgi.Handler{Path: backend, Dir: root, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
		InheritEnv: []string{"PATH", "SYSTEMROOT", "TEMP", "TMP", "HOME"}}
	s.Server = httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

// RepoURL is the repository's URL.
func (s *Server) RepoURL() string { return s.URL + "/repo.git" }

// Commit writes files (an empty content removes the file) and commits them on main; it returns the commit.
func (s *Server) Commit(files map[string]string, msg string) string {
	s.t.Helper()
	repo := filepath.Join(s.Root, "repo.git")
	for p, body := range files {
		full := filepath.Join(repo, filepath.FromSlash(p))
		if body == "" {
			_ = os.Remove(full)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			s.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			s.t.Fatal(err)
		}
	}
	s.git(repo, "add", "-A")
	s.git(repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@localhost", "commit", "-q", "--allow-empty", "-m", msg)
	return strings.TrimSpace(s.git(repo, "rev-parse", "HEAD"))
}

// Git runs git in the repository (for a test that needs a symbolic link, a submodule entry or a tag).
func (s *Server) Git(args ...string) string {
	s.t.Helper()
	return s.git(filepath.Join(s.Root, "repo.git"), args...)
}

func (s *Server) git(dir string, args ...string) string {
	s.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
