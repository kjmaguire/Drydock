package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// The bundle is built from the pinned releases, each file checked against GitHub's digest, and bundle.json names them; the
// app's seeding reads it back. A pin that names no checked release fails the build. Against PROTOCOL FIXTURES of GitHub's
// releases (scantest, llamatest): nothing here is GitHub.
func TestTheBundleIsBuiltFromThePinnedCheckedReleases(t *testing.T) {
	ctx := context.Background()
	fx := scantest.NewReleases(t)
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.7.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ osv")}})
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.8.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ osv newer")}})
	fx.Add(scantest.ReleaseSpec{Repo: "opengrep/opengrep", Tag: "v1.31.0", Assets: map[string][]byte{"opengrep_windows_x86.exe": []byte("MZ og")}})
	tq, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	lf := llamatest.NewReleases(t, tq.Repo)
	tag := "turboquant-windows-x64-vulkan-d86eb0b"
	lf.Add(llamatest.ReleaseSpec{Tag: tag, Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": llamatest.ZipOf(map[string][]byte{localllm.ServerName(): []byte("server")})}})

	var pins Pins
	if err := json.Unmarshal([]byte(`{"scan":[{"tool":"osv-scanner","tag":"v2.7.0"},{"tool":"opengrep","tag":"v1.31.0"}],"llama":{"source":"turboquant","backend":"vulkan","tag":"`+tag+`"}}`), &pins); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	// the scan tools' API and downloads go to the scan fixture; the llama ones to the llama fixture
	api := splitAPI{scan: host.HTTPAPI{BaseURL: fx.Server.URL}, llama: host.HTTPAPI{BaseURL: lf.Server.URL}, llamaRepo: tq.Repo}
	client := &http.Client{Transport: splitTransport{scan: fx.Transport, llama: lf.Transport, llamaRepo: tq.Repo}}
	m, err := Build(ctx, pins, api, client, out, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Scan) != 2 || m.Scan[0].Tag != "v2.7.0" || m.Llama == nil || m.Llama.Tag != tag || m.Llama.Backend != "vulkan" || m.Built != "2026-10-20T09:00:00Z" {
		t.Fatalf("%+v", m)
	}
	// the pinned release, not the newest
	if b, _ := os.ReadFile(filepath.Join(out, "osv-scanner_windows_amd64.exe")); string(b) != "MZ osv" {
		t.Fatalf("osv-scanner %q", b)
	}
	back, err := bundle.Read(out)
	if err != nil || len(back.Scan) != 2 || back.Llama.Archive.SHA256 != m.Llama.Archive.SHA256 {
		t.Fatalf("%v %+v", err, back)
	}
	// what the app's installer does with it: osv-scanner is staged and installed from the folder
	inst := &scan.Installer{Root: t.TempDir()}
	if in, did, err := inst.SeedFromBundle(ctx, out, back.Scan[0]); err != nil || !did || in.Tag != "v2.7.0" {
		t.Fatalf("%v %v %+v", err, did, in)
	}

	// a pin with no such release fails the build
	pins.Scan[1].Tag = "v9.9.9"
	if _, err := Build(ctx, pins, api, client, t.TempDir(), time.Now()); err == nil || !strings.Contains(err.Error(), "v9.9.9") {
		t.Fatalf("%v", err)
	}
	// the repository's own pins name the TurboQuant Vulkan build and the two scan tools
	raw, err := os.ReadFile(filepath.Join("..", "..", "packaging", "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mine Pins
	if err := json.Unmarshal(raw, &mine); err != nil || len(mine.Scan) != 2 || mine.Llama == nil || mine.Llama.Source != "turboquant" || mine.Llama.Backend != "vulkan" {
		t.Fatalf("%v %+v", err, mine)
	}
	for _, p := range mine.Scan {
		if _, ok := scan.SourceOf(p.Tool); !ok || !strings.HasPrefix(p.Tag, "v") {
			t.Errorf("pin %+v", p)
		}
	}
}

// splitAPI sends the llama source's requests to its fixture and every other to the scan tools' fixture.
type splitAPI struct {
	scan, llama host.API
	llamaRepo   string
}

func (s splitAPI) Do(ctx context.Context, method, path string, body, out any, etag string) (string, error) {
	if strings.Contains(path, s.llamaRepo) {
		return s.llama.Do(ctx, method, path, body, out, etag)
	}
	return s.scan.Do(ctx, method, path, body, out, etag)
}

func (s splitAPI) Raw(ctx context.Context, path string) (string, error) { return s.scan.Raw(ctx, path) }

type splitTransport struct {
	scan, llama http.RoundTripper
	llamaRepo   string
}

func (s splitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.Contains(r.URL.Path, s.llamaRepo) {
		return s.llama.RoundTrip(r)
	}
	return s.scan.RoundTrip(r)
}

// The Software Versions page (docs/software-versions.md) shows what the download carries: the versions it lists as used for the
// bundled tools are the pins of packaging/bundle.json.
func TestThePinsAreTheVersionsTheSoftwarePageShows(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "packaging", "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pins Pins
	if err := json.Unmarshal(raw, &pins); err != nil {
		t.Fatal(err)
	}
	notes, err := os.ReadFile(filepath.Join("..", "..", "docs", "software-notes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var n struct {
		Used map[string]string `json:"used"`
	}
	if err := json.Unmarshal(notes, &n); err != nil {
		t.Fatal(err)
	}
	if len(pins.Scan) == 0 || pins.Llama == nil {
		t.Fatalf("pins %+v", pins)
	}
	for _, p := range pins.Scan {
		if got := n.Used["tool:"+p.Tool]; got != strings.TrimPrefix(p.Tag, "v") {
			t.Errorf("%s: the bundle pins %s, the notes say %q", p.Tool, p.Tag, got)
		}
	}
	if got := n.Used["tool:llama.cpp-"+pins.Llama.Source]; got != pins.Llama.Tag {
		t.Errorf("llama.cpp: the bundle pins %s, the notes say %q", pins.Llama.Tag, got)
	}
}
