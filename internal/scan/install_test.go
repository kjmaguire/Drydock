package scan_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

func source(t *testing.T, tool string) scan.ToolSource {
	t.Helper()
	src, ok := scan.SourceOf(tool)
	if !ok {
		t.Fatal("no source for", tool)
	}
	return src
}

// The newest stable release that carries the Windows program with a digest is picked: drafts, pre-releases, a release
// without the Windows asset and an asset without a digest are passed over. For Opengrep the archive (2.0 and later) is
// preferred to the single exe (before 2.0).
func TestTheNewestCheckedReleaseIsPicked(t *testing.T) {
	fx := scantest.NewReleases(t)
	osv := source(t, "osv-scanner")
	fx.Add(scantest.ReleaseSpec{Repo: osv.Repo, Tag: "v2.5.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.5.0"), "osv-scanner_linux_amd64": []byte("elf")}})
	fx.Add(scantest.ReleaseSpec{Repo: osv.Repo, Tag: "v2.6.0", Assets: map[string][]byte{"osv-scanner_linux_amd64": []byte("elf only")}})
	fx.Add(scantest.ReleaseSpec{Repo: osv.Repo, Tag: "v2.7.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.7.0 unchecked")}, NoDigest: []string{"osv-scanner_windows_amd64.exe"}})
	fx.Add(scantest.ReleaseSpec{Repo: osv.Repo, Tag: "v2.8.0-rc1", Prerelease: true, Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ rc")}})
	fx.Add(scantest.ReleaseSpec{Repo: osv.Repo, Tag: "v2.8.0", Draft: true, Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ draft")}})
	api := host.HTTPAPI{BaseURL: fx.Server.URL}
	rel, err := scan.LatestToolRelease(context.Background(), api, osv)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v2.5.0" || rel.Asset.Name != "osv-scanner_windows_amd64.exe" || len(rel.Asset.SHA256) != 64 || !strings.HasPrefix(rel.Asset.URL, osv.DownloadPrefix()) {
		t.Fatalf("%+v", rel)
	}

	og := source(t, "opengrep")
	fx.Add(scantest.ReleaseSpec{Repo: og.Repo, Tag: "v1.31.0", Assets: map[string][]byte{"opengrep_windows_x86.exe": []byte("MZ 1.31")}})
	fx.Add(scantest.ReleaseSpec{Repo: og.Repo, Tag: "v2.0.0", Assets: map[string][]byte{
		"opengrep_windows_x86.exe": []byte("MZ old style"), "opengrep_windows_x86.zip": scantest.ZipOf(map[string][]byte{"opengrep.exe": []byte("MZ 2.0"), "libgmp.dll": []byte("dll")})}})
	rel, err = scan.LatestToolRelease(context.Background(), api, og)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v2.0.0" || rel.Asset.Name != "opengrep_windows_x86.zip" {
		t.Fatalf("%+v", rel)
	}

	// a repository with no usable release says so
	other := scan.ToolSource{Tool: "osv-scanner", Label: "x", Repo: "acme/empty", Assets: []string{"x.exe"}, Program: "x.exe"}
	fx.Add(scantest.ReleaseSpec{Repo: other.Repo, Tag: "v1.0.0", Assets: map[string][]byte{"y.exe": []byte("MZ")}})
	if _, err := scan.LatestToolRelease(context.Background(), api, other); err == nil || !strings.Contains(err.Error(), "x.exe with a SHA-256 digest") {
		t.Fatalf("err %v", err)
	}
}

// Install downloads the asset, checks it, puts the program under the tool's own folder (renamed for a bare exe, unpacked
// with its DLLs for an archive), records the release, removes the release it replaces, and installing the same release
// again downloads nothing. Remove deletes it all.
func TestInstallPutsTheCheckedProgramInPlace(t *testing.T) {
	fx := scantest.NewReleases(t)
	osv, og := source(t, "osv-scanner"), source(t, "opengrep")
	fx.Add(scantest.ReleaseSpec{Repo: osv.Repo, Tag: "v2.6.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.6.0")}})
	fx.Add(scantest.ReleaseSpec{Repo: og.Repo, Tag: "v2.0.0", Assets: map[string][]byte{
		"opengrep_windows_x86.zip": scantest.ZipOf(map[string][]byte{"opengrep.exe": []byte("MZ 2.0"), "libgmp.dll": []byte("dll")})}})
	api := host.HTTPAPI{BaseURL: fx.Server.URL}
	root := t.TempDir()
	inst := &scan.Installer{Root: root, GitHub: api, HTTP: &http.Client{Transport: fx.Transport}}
	if _, ok := inst.Installed("osv-scanner"); ok {
		t.Fatal("installed before any install")
	}
	ctx := context.Background()
	rel, err := scan.LatestToolRelease(ctx, api, osv)
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	in, err := inst.Install(ctx, rel, func(p scan.InstallProgress) {
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Tool != "osv-scanner" || in.Tag != "v2.6.0" || filepath.Base(in.Path) != "osv-scanner.exe" || !strings.HasPrefix(in.Path, filepath.Join(root, "osv-scanner")) {
		t.Fatalf("%+v", in)
	}
	if b, err := os.ReadFile(in.Path); err != nil || string(b) != "MZ 2.6.0" {
		t.Fatalf("program %q %v", b, err)
	}
	if strings.Join(phases, ",") != "downloading,unpacking,done" {
		t.Fatalf("phases %v", phases)
	}
	got, ok := inst.Installed("osv-scanner")
	if !ok || got.Path != in.Path || got.Tag != "v2.6.0" {
		t.Fatalf("installed: %+v %v", got, ok)
	}
	// the Tool the scan runs is the installed program
	if tool := inst.ToolFor(got); tool.Path != in.Path || !tool.Installed || tool.Chosen {
		t.Fatalf("tool %+v", tool)
	}
	// the same release again downloads nothing new (the download was removed after it was placed, so the asset comes
	// again; what matters is that the install stays correct and the old folder does not pile up)
	fx.Add(scantest.ReleaseSpec{Repo: osv.Repo, Tag: "v2.7.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.7.0")}})
	rel, err = scan.LatestToolRelease(ctx, api, osv)
	if err != nil || rel.Tag != "v2.7.0" {
		t.Fatal(rel, err)
	}
	in2, err := inst.Install(ctx, rel, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(in.Path)); !os.IsNotExist(err) {
		t.Fatal("the replaced release is still there")
	}
	if b, _ := os.ReadFile(in2.Path); string(b) != "MZ 2.7.0" {
		t.Fatalf("program %q", b)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "osv-scanner", "cache"))
	if len(entries) != 0 {
		t.Fatalf("the cache keeps %d file(s)", len(entries))
	}

	// an archive: the program and its DLLs stay together
	rel, err = scan.LatestToolRelease(ctx, api, og)
	if err != nil {
		t.Fatal(err)
	}
	in3, err := inst.Install(ctx, rel, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(in3.Path) != "opengrep.exe" {
		t.Fatalf("%+v", in3)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(in3.Path), "libgmp.dll")); err != nil {
		t.Fatal("the DLL is not beside the program:", err)
	}
	if _, ok := inst.Installed("opengrep"); !ok {
		t.Fatal("opengrep not installed")
	}

	if err := inst.Remove("opengrep"); err != nil {
		t.Fatal(err)
	}
	if _, ok := inst.Installed("opengrep"); ok {
		t.Fatal("opengrep still installed after Remove")
	}
	if _, ok := inst.Installed("osv-scanner"); !ok {
		t.Fatal("removing one tool removed the other")
	}
	if err := inst.Remove("gitleaks"); err == nil {
		t.Fatal("a tool Drydock does not install was removed")
	}
}

// A download whose bytes do not match the digest GitHub recorded is deleted and refused; nothing is installed.
func TestAProgramThatDoesNotMatchItsDigestIsRefused(t *testing.T) {
	fx := scantest.NewReleases(t)
	osv := source(t, "osv-scanner")
	fx.Add(scantest.ReleaseSpec{Repo: osv.Repo, Tag: "v2.6.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ good")}})
	api := host.HTTPAPI{BaseURL: fx.Server.URL}
	rel, err := scan.LatestToolRelease(context.Background(), api, osv)
	if err != nil {
		t.Fatal(err)
	}
	rel.Asset.SHA256 = strings.Repeat("0", 64) // what GitHub "recorded" is not what the download is
	inst := &scan.Installer{Root: t.TempDir(), GitHub: api, HTTP: &http.Client{Transport: fx.Transport}}
	if _, err := inst.Install(context.Background(), rel, nil); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err %v", err)
	}
	if _, ok := inst.Installed("osv-scanner"); ok {
		t.Fatal("installed although refused")
	}
	// a download from anywhere but the tool's own repository is refused before anything is fetched
	rel.Asset.URL = "https://github.com/evil/osv-scanner/releases/download/v2.6.0/osv-scanner_windows_amd64.exe"
	if _, err := inst.Install(context.Background(), rel, nil); err == nil || !strings.Contains(err.Error(), "refusing to download") {
		t.Fatalf("err %v", err)
	}
}
