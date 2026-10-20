package llamatest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ReleaseFixture is a PROTOCOL FIXTURE of GitHub's releases API and release downloads for one repository: the JSON shape
// the real API answers (tag_name, draft, prerelease, assets with browser_download_url, size and the sha256 digest GitHub
// records at upload) and the archives behind the download addresses. Nothing here is GitHub.
type ReleaseFixture struct {
	Server *httptest.Server
	// Transport sends requests for https://github.com/... to the fixture; everything else is refused.
	Transport http.RoundTripper
	Repo      string
	mu        sync.Mutex
	releases  []map[string]any
	files     map[string][]byte // download path -> bytes
	hold      chan struct{}     // when set, downloads wait for it to close (HoldDownloads)
	Downloads atomic.Int64
}

// ReleaseSpec is one release the fixture lists.
type ReleaseSpec struct {
	Tag        string
	Draft      bool
	Prerelease bool
	Body       string
	// Assets maps an asset name to the archive's bytes (ZipOf builds one). An asset named in NoDigest is listed without one.
	Assets   map[string][]byte
	NoDigest []string
}

// NewReleases starts the fixture for a repository ("owner/name") for a test.
func NewReleases(t testing.TB, repo string) *ReleaseFixture {
	f := StartReleases(repo)
	t.Cleanup(f.Close)
	return f
}

// Close stops the fixture.
func (f *ReleaseFixture) Close() { f.Server.Close() }

// StartReleases starts the fixture for a repository ("owner/name"); the caller closes it.
func StartReleases(repo string) *ReleaseFixture {
	f := &ReleaseFixture{Repo: repo, files: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+repo+"/releases", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.releases)
	})
	// one release by its tag, as GitHub answers it
	mux.HandleFunc("/repos/"+repo+"/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		tag := strings.TrimPrefix(r.URL.Path, "/repos/"+repo+"/releases/tags/")
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, rel := range f.releases {
			if rel["tag_name"] == tag {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rel)
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		b, ok := f.files[r.URL.Path]
		hold := f.hold
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		f.Downloads.Add(1)
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(b)
	})
	f.Server = httptest.NewServer(mux)
	f.Transport = rewrite{to: f.Server.URL, hosts: []string{"https://github.com", "https://api.github.com"}}
	return f
}

// HoldDownloads makes every archive download wait until the returned function is called (or the request is given up),
// so a test can look at an install while it is running: a 6 KB archive from an in-process server installs in a few
// milliseconds otherwise, before the call that started it has answered.
func (f *ReleaseFixture) HoldDownloads() (release func()) {
	ch := make(chan struct{})
	f.mu.Lock()
	f.hold = ch
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			if f.hold == ch {
				f.hold = nil
			}
			f.mu.Unlock()
			close(ch)
		})
	}
}

// Add lists a release (newest first: add the newest last, the fixture sorts by the order added, last first).
func (f *ReleaseFixture) Add(spec ReleaseSpec) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var assets []map[string]any
	names := make([]string, 0, len(spec.Assets))
	for n := range spec.Assets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b := spec.Assets[n]
		p := "/" + f.Repo + "/releases/download/" + spec.Tag + "/" + n
		f.files[p] = b
		a := map[string]any{"name": n, "browser_download_url": "https://github.com" + p, "size": len(b)}
		if !contains(spec.NoDigest, n) {
			sum := sha256.Sum256(b)
			a["digest"] = "sha256:" + hex.EncodeToString(sum[:])
		}
		assets = append(assets, a)
	}
	rel := map[string]any{"tag_name": spec.Tag, "name": spec.Tag, "body": spec.Body, "draft": spec.Draft, "prerelease": spec.Prerelease, "assets": assets}
	f.releases = append([]map[string]any{rel}, f.releases...)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ZipOf builds a zip archive of the files (name -> bytes), folders made by the names.
func ZipOf(files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, _ := zw.Create(n)
		_, _ = w.Write(files[n])
	}
	_ = zw.Close()
	return buf.Bytes()
}

// rewrite sends requests for the named hosts to a fixture.
type rewrite struct {
	to    string
	hosts []string
}

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	u := r.URL.String()
	for _, h := range rw.hosts {
		if strings.HasPrefix(u, h+"/") {
			nu, err := url.Parse(rw.to + strings.TrimPrefix(u, h))
			if err != nil {
				return nil, err
			}
			r2 := r.Clone(r.Context())
			r2.URL = nu
			r2.Host = nu.Host
			return http.DefaultTransport.RoundTrip(r2)
		}
	}
	return nil, fmt.Errorf("fixture: refusing %s", u)
}

// HubFixture is a PROTOCOL FIXTURE of the Hugging Face Hub: /api/models (search and author listings), the repository tree
// with lfs oid and size, and /resolve downloads with Range support. Nothing here is the Hub.
type HubFixture struct {
	Server *httptest.Server
	URL    string
	mu     sync.Mutex
	repos  map[string]*hubRepo
	// Requests counts resolve downloads.
	Requests atomic.Int64
	// Hits counts every request by path prefix, for tests.
	hits map[string]int
}

type hubRepo struct {
	author    string
	downloads int64
	tags      []string
	gated     bool
	files     map[string][]byte
}

// NewHub starts the fixture for a test.
func NewHub(t testing.TB) *HubFixture {
	f := StartHub()
	t.Cleanup(f.Close)
	return f
}

// Close stops the fixture.
func (f *HubFixture) Close() { f.Server.Close() }

// StartHub starts the fixture; the caller closes it.
func StartHub() *HubFixture {
	f := &HubFixture{repos: map[string]*hubRepo{}, hits: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.Server.URL
	return f
}

// ServerZip is a build archive holding a placeholder llama-server (the stand-in program replaces it when started).
func ServerZip() []byte {
	return ZipOf(map[string][]byte{"llama-server.exe": []byte("MZ placeholder: a Drydock test double, not llama.cpp"), "LICENSE": []byte("MIT")})
}

// AddModel adds a public repository with files (path -> bytes).
func (f *HubFixture) AddModel(repo string, downloads int64, tags []string, files map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	author, _, _ := strings.Cut(repo, "/")
	f.repos[repo] = &hubRepo{author: author, downloads: downloads, tags: tags, files: files}
}

// AddGated adds a gated repository (it must never be offered).
func (f *HubFixture) AddGated(repo string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	author, _, _ := strings.Cut(repo, "/")
	f.repos[repo] = &hubRepo{author: author, gated: true, files: map[string][]byte{}}
}

// Hits is how many requests started with a path prefix.
func (f *HubFixture) Hits(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for p, c := range f.hits {
		if strings.HasPrefix(p, prefix) {
			n += c
		}
	}
	return n
}

func (f *HubFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/models":
		f.list(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/models/") && strings.Contains(r.URL.Path, "/tree/main"):
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/models/"), "/tree/main")
		f.tree(w, r, repo)
	case strings.Contains(r.URL.Path, "/resolve/main/"):
		f.resolve(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *HubFixture) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	type row struct {
		id   string
		repo *hubRepo
	}
	var rows []row
	for id, rp := range f.repos {
		if a := q.Get("author"); a != "" && rp.author != a {
			continue
		}
		if s := q.Get("search"); s != "" && !strings.Contains(strings.ToLower(id), strings.ToLower(s)) {
			continue
		}
		rows = append(rows, row{id, rp})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].repo.downloads > rows[j].repo.downloads })
	out := []map[string]any{}
	for _, rw := range rows {
		tags := append([]string{"gguf", "text-generation"}, rw.repo.tags...)
		m := map[string]any{"id": rw.id, "modelId": rw.id, "downloads": rw.repo.downloads, "likes": rw.repo.downloads / 100,
			"lastModified": time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), "tags": tags, "private": false}
		if rw.repo.gated {
			m["gated"] = "auto"
		} else {
			m["gated"] = false
		}
		out = append(out, m)
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *HubFixture) tree(w http.ResponseWriter, r *http.Request, repo string) {
	f.mu.Lock()
	rp, ok := f.repos[repo]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	names := make([]string, 0, len(rp.files))
	for n := range rp.files {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []map[string]any{}
	for _, n := range names {
		b := rp.files[n]
		e := map[string]any{"type": "file", "path": n, "size": len(b), "oid": "blob"}
		if strings.HasSuffix(n, ".gguf") {
			sum := sha256.Sum256(b)
			e["lfs"] = map[string]any{"oid": hex.EncodeToString(sum[:]), "size": len(b), "pointerSize": 134}
		}
		out = append(out, e)
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *HubFixture) resolve(w http.ResponseWriter, r *http.Request) {
	repo, file, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/resolve/main/")
	f.mu.Lock()
	rp, ok := f.repos[repo]
	var b []byte
	if ok {
		b, ok = rp.files[file]
	}
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.Requests.Add(1)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")
	if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=") {
		start, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
		if start > 0 && start < len(b) {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(b)-1, len(b)))
			w.Header().Set("Content-Length", strconv.Itoa(len(b)-start))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(b[start:])
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}
