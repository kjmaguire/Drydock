package packs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

const human = "human:kyle"

var (
	scanOnce sync.Once
	scanner  *security.Scanner
)

// sharedScanner is the real gitleaks-backed scanner (building it is slow, so tests share one).
func sharedScanner(t testing.TB) *security.Scanner {
	scanOnce.Do(func() {
		var err error
		scanner, err = security.NewScanner()
		if err != nil {
			panic(err)
		}
	})
	return scanner
}

type env struct {
	t     *testing.T
	s     *Store
	db    *storage.DB
	bus   *events.SQLBus
	dir   string
	now   *time.Time
	clock func() time.Time
}

type envOpt func(*Options)

// newEnv builds a Store over a real SQLite database (FTS5 included) in a temp dir, with the real secret scanner.
func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 1024)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	e := &env{t: t, db: db, bus: bus, dir: dir, now: &now}
	e.clock = func() time.Time { return *e.now }
	o := Options{DB: db, Bus: bus, Dir: filepath.Join(dir, "packs"), Scanner: sharedScanner(t), Now: e.clock, Logf: t.Logf}
	for _, f := range opts {
		f(&o)
	}
	e.s, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close(); db.Close() })
	if _, err := db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('prj1','demo','/x',1)`); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) events(prefix string) []events.Event {
	e.t.Helper()
	evs, err := e.bus.Replay(context.Background(), 0, events.Filter{Types: []string{prefix}}, 10000)
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

// writeFiles writes name->content under dir.
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

// localManifest is a private pack over a local directory.
func localManifest(id, dir string, extra string) []byte {
	return []byte(fmt.Sprintf("schema: 1\nid: %s\ntitle: %s docs\nkind: indexed\nvisibility: private\nsources:\n  - {type: local_dir, path: %q}\n%s", id, id, dir, extra))
}

func (e *env) install(manifest []byte) Pack {
	e.t.Helper()
	p, err := e.s.Install(context.Background(), human, manifest)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) refresh(id string) RefreshResult {
	e.t.Helper()
	r, err := e.s.Refresh(context.Background(), human, id, RefreshOptions{})
	if err != nil {
		e.t.Fatalf("refresh %s: %v", id, err)
	}
	return r
}

func (e *env) search(q string, ids ...string) SearchResult {
	e.t.Helper()
	r, err := e.s.Search(context.Background(), SearchRequest{PackIDs: ids, Query: q, K: 5})
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// ---- protocol fixture ----
//
// PROTOCOL FIXTURE (not a real docs site): a local HTTP server that speaks the parts of the web the crawler depends on
// (robots.txt with Content-Signal, llms.txt, sitemap.xml, ETag/If-None-Match, redirects, content types). It exists because
// the sandbox has no route to the real docs sites; the real sites' structure is unverified (docs/expert-packs.md section 11).
type docSite struct {
	*httptest.Server
	mu       sync.Mutex
	pages    map[string]string // path -> body (served as text/markdown unless it starts with <html)
	robots   string
	hits     map[string]int
	status   map[string]int // path -> forced status
	extra    map[string]http.HandlerFunc
	etagless bool
}

func newDocSite(t *testing.T) *docSite {
	d := &docSite{pages: map[string]string{}, hits: map[string]int{}, status: map[string]int{}, extra: map[string]http.HandlerFunc{},
		robots: "User-agent: *\nDisallow: /private/\nContent-Signal: ai-train=no, search=yes, ai-input=yes\n"}
	d.Server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.Close)
	return d
}

func (d *docSite) set(path, body string) {
	d.mu.Lock()
	d.pages[path] = body
	d.mu.Unlock()
}

func (d *docSite) hitCount(path string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hits[path]
}

func (d *docSite) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.hits[r.URL.Path]++
	body, ok := d.pages[r.URL.Path]
	forced := d.status[r.URL.Path]
	extra := d.extra[r.URL.Path]
	robots := d.robots
	d.mu.Unlock()
	if extra != nil {
		extra(w, r)
		return
	}
	if forced != 0 {
		http.Error(w, "forced", forced)
		return
	}
	if r.URL.Path == "/robots.txt" {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, robots)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	ct := "text/markdown; charset=utf-8"
	switch {
	case strings.HasPrefix(body, "<html") || strings.HasPrefix(body, "<!doctype"):
		ct = "text/html"
	case strings.HasSuffix(r.URL.Path, ".xml"):
		ct = "application/xml"
	case strings.HasSuffix(r.URL.Path, "llms.txt"):
		ct = "text/plain"
	}
	if !d.etagless {
		etag := `"` + HashHex(body)[:12] + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", ct)
	fmt.Fprint(w, body)
}

func (d *docSite) host() string { return strings.TrimPrefix(d.URL, "http://") }

// urlManifest builds a manifest crawling the fixture. loopback fetching is enabled in the Store options of these tests.
func (d *docSite) manifest(id, sources, crawlExtra string) []byte {
	return []byte(fmt.Sprintf(`schema: 1
id: %s
title: %s fixture docs
kind: indexed
visibility: public
license: {content: CC-BY-4.0, attribution_required: true, attribution: "Fixture Docs, CC BY 4.0"}
sources:
%s
crawl: {allowed_hosts: ["127.0.0.1"], rps: 10, concurrency: 2, timeout: 5s%s}
`, id, id, sources, crawlExtra))
}

func loopback(o *Options) { o.AllowLoopbackFetch = true }
