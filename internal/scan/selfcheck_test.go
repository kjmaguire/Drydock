package scan_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// SelfCheck runs each tool with exactly a scan's arguments: the stand-ins (which refuse an option they do not know, as
// Opengrep 1.31.0 does) pass, and a program that fails names why.
func TestTheSelfCheckRunsEachToolTheWayAScanDoes(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"osv-scanner", "opengrep"} {
		if err := scan.SelfCheck(ctx, scan.Options{}, name, tool(name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		err := scan.SelfCheck(ctx, scan.Options{}, name, tool(name, "--fail"))
		if err == nil || !strings.Contains(err.Error(), "exited 2: "+name+" stand-in: told to fail") {
			t.Fatalf("%s told to fail: %v", name, err)
		}
	}
	// an Opengrep that no longer knows an option every scan passes is caught here, before a scan meets it
	if err := scan.SelfCheck(ctx, scan.Options{}, "opengrep", tool("opengrep", "--no-progress-bar")); err == nil || !strings.Contains(err.Error(), "unknown option --no-progress-bar") {
		t.Fatal(err)
	}
	if err := scan.SelfCheck(ctx, scan.Options{}, "gitleaks", tool("opengrep")); err == nil {
		t.Fatal("checked a tool Drydock does not install")
	}
}

// A release that fails Installer.Check is not put in place: it is deleted, and the release in use stays.
func TestAReleaseThatFailsTheCheckIsNotUsed(t *testing.T) {
	fx := scantest.NewReleases(t)
	og := source(t, "opengrep")
	fx.Add(scantest.ReleaseSpec{Repo: og.Repo, Tag: "v1.31.0", Assets: map[string][]byte{"opengrep_windows_x86.exe": []byte("MZ 1.31.0")}})
	api := host.HTTPAPI{BaseURL: fx.Server.URL}
	inst := &scan.Installer{Root: t.TempDir(), GitHub: api, HTTP: &http.Client{Transport: fx.Transport},
		// the stand-in replaces the program; v1.32.0 is told to fail, as a release that broke an option would
		Program: func(in scan.InstalledTool) (string, []string) {
			if in.Tag == "v1.32.0" {
				return os.Args[0], scantest.Args("opengrep", "--fail")
			}
			return os.Args[0], scantest.Args("opengrep")
		}}
	inst.Check = func(ctx context.Context, name string, t scan.Tool) error {
		return scan.SelfCheck(ctx, scan.Options{}, name, t)
	}
	ctx := context.Background()
	rel, err := scan.LatestToolRelease(ctx, api, og)
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	if _, err := inst.Install(ctx, rel, func(p scan.InstallProgress) {
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(phases, ",") != "downloading,unpacking,checking,done" {
		t.Fatalf("phases %v", phases)
	}
	fx.Add(scantest.ReleaseSpec{Repo: og.Repo, Tag: "v1.32.0", Assets: map[string][]byte{"opengrep_windows_x86.exe": []byte("MZ 1.32.0")}})
	if rel, err = scan.LatestToolRelease(ctx, api, og); err != nil || rel.Tag != "v1.32.0" {
		t.Fatal(rel, err)
	}
	_, err = inst.Install(ctx, rel, nil)
	if err == nil || !strings.Contains(err.Error(), "Opengrep v1.32.0 failed Drydock's test scan, so it is not used") || !strings.Contains(err.Error(), "told to fail") {
		t.Fatal(err)
	}
	in, ok := inst.Installed("opengrep")
	if !ok || in.Tag != "v1.31.0" {
		t.Fatalf("the release in use did not stay: %+v %v", in, ok)
	}
	if _, err := os.Stat(in.Path); err != nil {
		t.Fatal(err)
	}
}
