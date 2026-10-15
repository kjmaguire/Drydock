package runners

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const relBody = `## What's Changed
* things

## Windows x64
` + "```" + `
Invoke-WebRequest -Uri https://github.com/actions/runner/releases/download/v2.330.0/actions-runner-win-x64-2.330.0.zip
` + "```" + `
- actions-runner-win-x64-2.330.0.zip <!-- BEGIN SHA win-x64 -->aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa<!-- END SHA win-x64 -->
- actions-runner-linux-x64-2.330.0.tar.gz <!-- BEGIN SHA linux-x64 -->BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB<!-- END SHA linux-x64 -->
- actions-runner-linux-arm64-2.330.0.tar.gz <!-- BEGIN SHA linux-arm64 -->cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc<!-- END SHA linux-x64 -->
- actions-runner-osx-x64-2.330.0.tar.gz <!-- BEGIN SHA osx-x64 -->dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd<!-- END SHA osx-x64 -->
- noexternals <!-- BEGIN SHA linux-x64_noexternals -->eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee<!-- END SHA linux-x64_noexternals -->
`

func asset(name string) struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
} {
	return struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	}{name, DownloadPrefix + "v2.330.0/" + name, 1}
}

// The release notes' SHA-256 markers decide which archives are offered: one without a matching pair of markers, a variant
// archive, an archive of another version or one served from elsewhere is left out.
func TestParseReleaseKeepsOnlyArchivesWithAPublishedSHA256(t *testing.T) {
	raw := rawRelease{TagName: "v2.330.0", Body: relBody}
	for _, n := range []string{"actions-runner-win-x64-2.330.0.zip", "actions-runner-linux-x64-2.330.0.tar.gz",
		"actions-runner-linux-arm64-2.330.0.tar.gz", "actions-runner-linux-x64-2.330.0-noexternals.tar.gz",
		"actions-runner-win-arm64-2.330.0.zip", "actions-runner-osx-x64-2.329.0.tar.gz"} {
		raw.Assets = append(raw.Assets, asset(n))
	}
	evil := asset("actions-runner-osx-x64-2.330.0.tar.gz")
	evil.URL = "https://example.com/actions-runner-osx-x64-2.330.0.tar.gz"
	raw.Assets = append(raw.Assets, evil)
	rel, err := parseRelease(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "2.330.0" || len(rel.Assets) != 2 {
		t.Fatalf("%+v", rel)
	}
	if a := rel.Assets[WinX64]; a.SHA256 != strings.Repeat("a", 64) || a.Name != "actions-runner-win-x64-2.330.0.zip" {
		t.Fatalf("%+v", a)
	}
	if a := rel.Assets[LinuxX64]; a.SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("upper-case hex is read as lower case: %+v", a)
	}
	// the arm64 marker pair does not match, win-arm64 has no marker, osx is the wrong version or address
	for _, p := range []Platform{LinuxARM64, WinARM64, "osx-x64"} {
		if _, ok := rel.Assets[p]; ok {
			t.Fatalf("%s was offered", p)
		}
	}
	for _, bad := range []rawRelease{{TagName: "latest", Body: relBody}, {TagName: "v2.330.0", Prerelease: true, Body: relBody},
		{TagName: "v2.330.0", Body: "no markers", Assets: raw.Assets}} {
		if _, err := parseRelease(bad); err == nil {
			t.Fatalf("accepted %+v", bad.TagName)
		}
	}
}

// githubTransport answers requests for the runner's release downloads from files, as github.com would.
type githubTransport struct {
	files map[string][]byte
	hits  atomic.Int32
}

func (g *githubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	g.hits.Add(1)
	b, ok := g.files[r.URL.String()]
	if !ok {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: r}, nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A download is checked against the published SHA-256: a match is kept and reused without downloading again; a mismatch is
// deleted and refused; an address outside the runner's release downloads is never fetched.
func TestDownloadChecksTheSHA256AndReusesAMatchingArchive(t *testing.T) {
	body := makeZip(t, map[string]string{"run.cmd": "x"})
	url := DownloadPrefix + "v2.330.0/actions-runner-win-x64-2.330.0.zip"
	tr := &githubTransport{files: map[string][]byte{url: body}}
	client := &http.Client{Transport: tr}
	dir := t.TempDir()
	a := Asset{Name: "actions-runner-win-x64-2.330.0.zip", URL: url, SHA256: sum(body)}
	path, err := Download(context.Background(), client, a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, body) {
		t.Fatal("wrong file")
	}
	if _, err := Download(context.Background(), client, a, dir); err != nil || tr.hits.Load() != 1 {
		t.Fatalf("not reused: %v, %d downloads", err, tr.hits.Load())
	}
	bad := a
	bad.Name = "actions-runner-win-x64-2.330.1.zip"
	bad.SHA256 = strings.Repeat("0", 64)
	tr.files[url+"1"] = body
	bad.URL = url + "1"
	if _, err := Download(context.Background(), client, bad, dir); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("the mismatched download was left: %v", ents)
	}
	away := a
	away.URL = "https://example.com/runner.zip"
	if _, err := Download(context.Background(), client, away, t.TempDir()); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatal(err)
	}
}

// Unzip keeps every entry inside the folder: names that climb out or are absolute are refused and nothing is left behind.
func TestUnzipRefusesEntriesThatLeaveTheFolder(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.zip")
	os.WriteFile(good, makeZip(t, map[string]string{"run.cmd": "@echo off", "bin/Runner.Listener.exe": "MZ", "bin/": ""}), 0o644)
	out := filepath.Join(dir, "out")
	if err := Unzip(good, out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "bin", "Runner.Listener.exe")); string(b) != "MZ" {
		t.Fatal("not unpacked")
	}
	if err := Unzip(good, out); err == nil {
		t.Fatal("unpacked over an existing folder")
	}
	for i, name := range []string{"../escape.txt", "bin/../../escape.txt", "/etc/escape", `C:\escape.txt`, `..\escape.txt`} {
		z := filepath.Join(dir, fmt.Sprintf("bad%d.zip", i))
		os.WriteFile(z, makeZip(t, map[string]string{"ok.txt": "fine", name: "bad"}), 0o644)
		dst := filepath.Join(dir, fmt.Sprintf("bad%d", i))
		if err := Unzip(z, dst); err == nil {
			t.Fatalf("%s was unpacked", name)
		}
		if _, err := os.Stat(dst); err == nil {
			t.Fatalf("%s left a folder", name)
		}
		if _, err := os.Stat(dst + ".partial"); err == nil {
			t.Fatalf("%s left a partial folder", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("a file escaped")
	}
}

func TestParseDevDrive(t *testing.T) {
	if d, ok := parseDevDrive("D:", "This is a trusted developer volume.\r\n"); !ok || !d.Trusted || d.Letter != "D:" {
		t.Fatal(d, ok)
	}
	if d, ok := parseDevDrive("E:", "This is an untrusted developer volume."); !ok || d.Trusted {
		t.Fatal(d, ok)
	}
	if _, ok := parseDevDrive("C:", "This is not a developer volume."); ok {
		t.Fatal("C: is not a Dev Drive")
	}
	if _, ok := parseDevDrive("C:", "Error:  Access is denied."); ok {
		t.Fatal("an error is not a Dev Drive")
	}
}

func TestMachineNameAndParseRepo(t *testing.T) {
	if n := MachineName("KYLE's Desktop.local"); n != "kyle-s-desktop-local" {
		t.Fatal(n)
	}
	if n := MachineName("!!!"); n != "pc" {
		t.Fatal(n)
	}
	for in, want := range map[string]string{"kjmaguire/shop": "kjmaguire/shop", "https://github.com/kjmaguire/shop.git": "kjmaguire/shop",
		"git@github.com:kjmaguire/shop.git": "kjmaguire/shop", " kjmaguire/my.app_2 ": "kjmaguire/my.app_2"} {
		r, err := ParseRepo(in)
		if err != nil || r.String() != want {
			t.Fatalf("%q: %v %v", in, r, err)
		}
	}
	for _, bad := range []string{"shop", "https://gitlab.com/a/b", "a/..", "-bad/x", "a/b/c d", ""} {
		if _, err := ParseRepo(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
