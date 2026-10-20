package scan_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/runners"
	"github.com/kjmaguire/drydock/internal/scan"
)

// bundled writes a release file into a download's tools folder and returns its bundle entry.
func bundled(t *testing.T, dir, tool, tag, name string, body []byte) bundle.ScanTool {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	src, _ := scan.SourceOf(tool)
	sum := sha256.Sum256(body)
	return bundle.ScanTool{Tool: tool, Tag: tag, Asset: runners.Asset{Name: name, URL: src.DownloadPrefix() + tag + "/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}}
}

// noNetwork fails any request: a bundled install must fetch nothing.
type noNetwork struct{ t *testing.T }

func (n noNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("a bundled install went to the network: %s", r.URL)
	return nil, http.ErrUseLastResponse
}

// Decision P77: the download's tools come installed. The bundled release is put in place at the first start without the
// network; a later start changes nothing; an app update that carries a newer release replaces the earlier bundled one, but
// never a release the person installed; a tool the person removed stays removed; a file that does not match is refused.
func TestTheBundledToolsArePutInPlaceOnceAndThePersonsChoicesStand(t *testing.T) {
	ctx := context.Background()
	dl := t.TempDir()
	root := t.TempDir()
	inst := &scan.Installer{Root: root, HTTP: &http.Client{Transport: noNetwork{t}}}
	osv := bundled(t, dl, "osv-scanner", "v2.7.0", "osv-scanner_windows_amd64.exe", []byte("MZ osv 2.7.0"))
	og := bundled(t, dl, "opengrep", "v1.31.0", "opengrep_windows_x86.exe", []byte("MZ opengrep 1.31.0"))

	in, did, err := inst.SeedFromBundle(ctx, dl, osv)
	if err != nil || !did || in.Tag != "v2.7.0" || filepath.Base(in.Path) != "osv-scanner.exe" {
		t.Fatalf("%v %v %+v", err, did, in)
	}
	if b, _ := os.ReadFile(in.Path); string(b) != "MZ osv 2.7.0" {
		t.Fatalf("program %q", b)
	}
	if _, did, err = inst.SeedFromBundle(ctx, dl, og); err != nil || !did {
		t.Fatalf("opengrep: %v %v", err, did)
	}
	// the next start: already in place
	if _, did, err = inst.SeedFromBundle(ctx, dl, osv); err != nil || did {
		t.Fatalf("seeded twice: %v %v", err, did)
	}

	// an app update carries osv-scanner v2.8.0: it replaces the earlier bundled release
	dl2 := t.TempDir()
	osv2 := bundled(t, dl2, "osv-scanner", "v2.8.0", "osv-scanner_windows_amd64.exe", []byte("MZ osv 2.8.0"))
	if in, did, err = inst.SeedFromBundle(ctx, dl2, osv2); err != nil || !did || in.Tag != "v2.8.0" {
		t.Fatalf("newer bundle: %v %v %+v", err, did, in)
	}

	// the person installs Opengrep v1.32.0 themselves (Update): a download that carries v1.31.0 leaves it alone
	og2 := bundled(t, t.TempDir(), "opengrep", "v1.32.0", "opengrep_windows_x86.exe", []byte("MZ opengrep 1.32.0"))
	if _, err := bundle.Stage(dl2, og2.Asset, "https://github.com/opengrep/opengrep/releases/download/", filepath.Join(root, "opengrep", "cache")); err == nil {
		t.Fatal("staged a file that is not in the download")
	}
	stage := t.TempDir()
	og2 = bundled(t, stage, "opengrep", "v1.32.0", "opengrep_windows_x86.exe", []byte("MZ opengrep 1.32.0"))
	if _, err := bundle.Stage(stage, og2.Asset, "https://github.com/opengrep/opengrep/releases/download/", filepath.Join(root, "opengrep", "cache")); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Install(ctx, scan.ToolRelease{Tool: "opengrep", Tag: "v1.32.0", Asset: og2.Asset}, nil); err != nil {
		t.Fatal(err)
	}
	if in, did, err = inst.SeedFromBundle(ctx, dl, og); err != nil || did || in.Tag != "v1.32.0" {
		t.Fatalf("the person's own install was replaced: %v %v %+v", err, did, in)
	}

	// removed by the person: the bundled copy is not put back; installed again, the mark goes
	if err := inst.Remove("osv-scanner"); err != nil {
		t.Fatal(err)
	}
	if _, did, err = inst.SeedFromBundle(ctx, dl2, osv2); err != nil || did {
		t.Fatalf("a removed tool came back: %v %v", err, did)
	}
	if _, ok := inst.Installed("osv-scanner"); ok {
		t.Fatal("installed after removal")
	}

	// a file that does not match the bundle's SHA-256 is refused, and nothing is installed from it
	bad := t.TempDir()
	tampered := bundled(t, bad, "osv-scanner", "v2.9.0", "osv-scanner_windows_amd64.exe", []byte("MZ osv 2.9.0"))
	if err := os.WriteFile(filepath.Join(bad, tampered.Asset.Name), []byte("MZ something else"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := &scan.Installer{Root: t.TempDir(), HTTP: &http.Client{Transport: noNetwork{t}}}
	if _, did, err := fresh.SeedFromBundle(ctx, bad, tampered); err == nil || did || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a changed file was installed: %v %v", err, did)
	}
	// a file that says it came from somewhere else is refused
	elsewhere := tampered
	elsewhere.Asset.URL = "https://example.com/osv-scanner.exe"
	if _, _, err := fresh.SeedFromBundle(ctx, bad, elsewhere); err == nil || !strings.Contains(err.Error(), "did not come from") {
		t.Fatalf("%v", err)
	}
}

// A download without a tools folder, or with one that has no bundle.json, carries nothing; a manifest in another format is refused.
func TestABundleManifestIsReadOrAbsent(t *testing.T) {
	if m, err := bundle.Read(""); m != nil || err != nil {
		t.Fatal(m, err)
	}
	dir := t.TempDir()
	if m, err := bundle.Read(dir); m != nil || err != nil {
		t.Fatal(m, err)
	}
	os.WriteFile(filepath.Join(dir, bundle.FileName), []byte(`{"version":2}`), 0o644)
	if _, err := bundle.Read(dir); err == nil {
		t.Fatal("a manifest of another format was read")
	}
	os.WriteFile(filepath.Join(dir, bundle.FileName), []byte(`{"version":1,"scan":[{"tool":"osv-scanner","tag":"v2.7.0","asset":{"name":"a.exe","url":"u","sha256":"x","size":1}}]}`), 0o644)
	m, err := bundle.Read(dir)
	if err != nil || len(m.Scan) != 1 || m.Scan[0].Tag != "v2.7.0" {
		t.Fatal(m, err)
	}
}
