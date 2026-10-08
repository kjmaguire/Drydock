package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

const human = "human:kyle"

var (
	scanOnce sync.Once
	scanner  *security.Scanner
)

func sharedScanner() *security.Scanner {
	scanOnce.Do(func() {
		var err error
		if scanner, err = security.NewScanner(); err != nil {
			panic(err)
		}
	})
	return scanner
}

type env struct {
	t    *testing.T
	s    *Service
	db   *storage.DB
	bus  *events.SQLBus
	repo string
}

// newEnv builds a Service over real SQLite and a real Git repository registered as project p1.
func newEnv(t *testing.T, files map[string]string) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 1024)
	repo := filepath.Join(t.TempDir(), "repo")
	writeFiles(t, repo, map[string]string{"README.md": "# demo\n"})
	writeFiles(t, repo, files)
	if _, err := git.Init(ctx, repo, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','demo',?,1)`, repo); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, bus: bus, repo: repo}
	e.s = &Service{DB: db, Bus: bus, Scanner: sharedScanner()}
	if err := e.s.EnsureRegistries(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close(); db.Close() })
	return e
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// gitRun runs git in the repo with a fixed identity.
func (e *env) gitRun(args ...string) string {
	e.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = e.repo
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (e *env) events(prefix string) []events.Event {
	e.t.Helper()
	evs, err := e.bus.Replay(context.Background(), 0, events.Filter{Types: []string{prefix}}, 1000)
	if err != nil {
		e.t.Fatal(err)
	}
	return evs
}

func evData(t *testing.T, ev events.Event) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ev.Data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// skillMD renders a SKILL.md.
func skillMD(name, desc, meta, body string) string {
	m := ""
	if meta != "" {
		m = "metadata:\n" + meta
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n%s---\n%s\n", name, desc, m, body)
}

func metaLines(globs, roles string) string {
	var b strings.Builder
	if globs != "" {
		b.WriteString("  drydock.globs: \"" + globs + "\"\n")
	}
	if roles != "" {
		b.WriteString("  drydock.roles: \"" + roles + "\"\n")
	}
	return b.String()
}

const d1Skill = "Never edit an applied migration; add a new numbered file."

// ---- PROTOCOL FIXTURE: GitHub as the registry fetcher sees it ----
//
// A local HTTP server answering GET /repos/<owner>/<name>/commits/<ref> with {"sha": ...} and GET /<owner>/<name>/tar.gz/<sha>
// with a .tar.gz whose single top-level directory is <name>-<sha>, as api.github.com and codeload.github.com do. It stands in
// for GitHub because the sandbox cannot reach it; the real services were never contacted (unverified).
type ghFixture struct {
	*httptest.Server
	mu      sync.Mutex
	commits map[string]string            // "owner/name@ref" -> sha
	trees   map[string]map[string]string // "owner/name@sha" -> path -> content
	hits    map[string]int
}

func newGHFixture(t *testing.T) *ghFixture {
	g := &ghFixture{commits: map[string]string{}, trees: map[string]map[string]string{}, hits: map[string]int{}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

func (g *ghFixture) publish(repo, ref, sha string, files map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.commits[repo+"@"+ref] = sha
	g.trees[repo+"@"+sha] = files
}

func (g *ghFixture) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hits[r.URL.Path]++
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(parts) == 5 && parts[0] == "repos" && parts[3] == "commits":
		sha, ok := g.commits[parts[1]+"/"+parts[2]+"@"+parts[4]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"sha":%q}`, sha)
	case len(parts) == 4 && parts[2] == "tar.gz":
		tree, ok := g.trees[parts[0]+"/"+parts[1]+"@"+parts[3]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-gzip")
		w.Write(buildTarball(parts[1]+"-"+parts[3], tree))
	default:
		http.NotFound(w, r)
	}
}

func buildTarball(top string, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		body := files[n]
		tw.WriteHeader(&tar.Header{Name: top + "/" + n, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func (e *env) useFixture(g *ghFixture) {
	f := packs.NewFetcher(packs.FetchPolicy{AllowedHosts: []string{"127.0.0.1"}, NoRobots: true, AllowLoopback: true, RPS: 1000, MaxBytes: 1 << 20, Timeout: 5 * time.Second}, nil)
	e.s.Fetcher = &GitHubFetcher{Fetcher: f, APIBase: g.URL, TarballBase: g.URL}
}

const sha1 = "1111111111111111111111111111111111111111"
const sha2 = "2222222222222222222222222222222222222222"

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
