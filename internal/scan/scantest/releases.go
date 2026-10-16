package scantest

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ReleaseFixture is a PROTOCOL FIXTURE of GitHub's releases API and release downloads for the tool repositories Drydock
// installs from (decision P66): the JSON shape the real API answers (tag_name, draft, prerelease, assets with
// browser_download_url, size and the sha256 digest GitHub records at upload) and the files behind the download addresses,
// for any number of repositories. Nothing here is GitHub; the "programs" it serves are whatever bytes a test gives it.
type ReleaseFixture struct {
	Server *httptest.Server
	// Transport sends requests for https://github.com/... to the fixture; everything else is refused.
	Transport http.RoundTripper
	mu        sync.Mutex
	releases  map[string][]map[string]any // repo -> releases, newest first
	files     map[string][]byte           // download path -> bytes
	hold      chan struct{}               // when set, downloads wait for it to close (HoldDownloads)
	Downloads atomic.Int64
}

// ReleaseSpec is one release the fixture lists for a repository.
type ReleaseSpec struct {
	Repo       string // "owner/name"
	Tag        string
	Draft      bool
	Prerelease bool
	Body       string
	// Assets maps an asset name to its bytes (ZipOf builds an archive). An asset named in NoDigest is listed without one.
	Assets   map[string][]byte
	NoDigest []string
}

// NewReleases starts the fixture for a test.
func NewReleases(t testing.TB) *ReleaseFixture {
	f := StartReleases()
	t.Cleanup(f.Close)
	return f
}

// StartReleases starts the fixture; the caller closes it.
func StartReleases() *ReleaseFixture {
	f := &ReleaseFixture{releases: map[string][]map[string]any{}, files: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if repo, ok := strings.CutPrefix(r.URL.Path, "/repos/"); ok && strings.HasSuffix(repo, "/releases") {
			repo = strings.TrimSuffix(repo, "/releases")
			f.mu.Lock()
			list := f.releases[repo]
			f.mu.Unlock()
			if list == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
			return
		}
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
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(b)
	})
	f.Server = httptest.NewServer(mux)
	f.Transport = rewriteTo{to: f.Server.URL, hosts: []string{"https://github.com", "https://api.github.com"}}
	return f
}

// Close stops the fixture.
func (f *ReleaseFixture) Close() { f.Server.Close() }

// HoldDownloads makes every download wait until the returned function is called (or the request is given up), so a test
// can look at an install while it runs, or cancel it: a few bytes from an in-process server install in milliseconds
// otherwise.
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

// Add lists a release (the one added last is listed first, as GitHub lists the newest first).
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
		p := "/" + spec.Repo + "/releases/download/" + spec.Tag + "/" + n
		f.files[p] = b
		a := map[string]any{"name": n, "browser_download_url": "https://github.com" + p, "size": len(b)}
		if !containsString(spec.NoDigest, n) {
			sum := sha256.Sum256(b)
			a["digest"] = "sha256:" + hex.EncodeToString(sum[:])
		}
		assets = append(assets, a)
	}
	rel := map[string]any{"tag_name": spec.Tag, "name": spec.Tag, "body": spec.Body, "draft": spec.Draft, "prerelease": spec.Prerelease, "assets": assets}
	f.releases[spec.Repo] = append([]map[string]any{rel}, f.releases[spec.Repo]...)
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ZipOf builds a zip archive of the files (name -> bytes).
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

// rewriteTo sends requests for the named hosts to the fixture.
type rewriteTo struct {
	to    string
	hosts []string
}

func (rw rewriteTo) RoundTrip(r *http.Request) (*http.Response, error) {
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
