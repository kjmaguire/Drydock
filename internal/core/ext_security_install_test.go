package core_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// installCore is a core whose osv-scanner and opengrep are not installed and whose tool installer reads the PROTOCOL FIXTURE
// of GitHub's releases (scantest.ReleaseFixture); the "program" it installs is replaced by this binary's labelled stand-in,
// since the fixture's bytes are no Windows program.
func installCore(t *testing.T, fx *scantest.ReleaseFixture) *app {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "not-installed")
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		ScanTools: scan.Paths{OSV: scan.Tool{Path: missing, Args: scantest.Args("osv-scanner")},
			Opengrep: scan.Tool{Path: missing, Args: scantest.Args("opengrep")}},
		ScanInstall: func(i *scan.Installer) {
			i.GitHub = host.HTTPAPI{BaseURL: fx.Server.URL}
			i.HTTP = &http.Client{Transport: fx.Transport}
			i.Program = func(in scan.InstalledTool) (string, []string) { return os.Args[0], scantest.Args(in.Tool) }
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

func (a *app) waitInstall(projectID, tool string, want string) scan.Tools {
	a.t.Helper()
	var tools scan.Tools
	a.waitFor("the install of "+tool+" to be "+want, func() bool {
		tools = scan.Tools{} // decoding into the last answer would keep fields the new one leaves out
		a.call("security.tools", map[string]any{"projectId": projectID}, &tools)
		info := tools.OSV
		if tool == "opengrep" {
			info = tools.Opengrep
		}
		return info.Job != nil && info.Job.State == want
	})
	return tools
}

// security.tool.install: Drydock downloads osv-scanner's newest checked release and uses it, the scan runs it, the same call
// installs a newer release and says when there is none, and a listed release older than the installed one is never installed.
func TestAPersonInstallsAScanToolAndUpdatesIt(t *testing.T) {
	fx := scantest.NewReleases(t)
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.6.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.6.0")}})
	a := installCore(t, fx)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var tools scan.Tools
	a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
	if tools.OSV.Found || !tools.CanInstall || tools.OSV.Job != nil {
		t.Fatalf("before: %+v", tools)
	}
	tools = scan.Tools{}
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	if tools.OSV.Job == nil || tools.OSV.Job.State != "running" {
		t.Fatalf("just after Install: %+v", tools.OSV)
	}
	tools = a.waitInstall(p.ID, "osv-scanner", "done")
	if !tools.OSV.Found || tools.OSV.Source != "installed" || tools.OSV.Tag != "v2.6.0" || !strings.Contains(tools.OSV.Version, "standin") || tools.OSV.Job.Phase != "done" {
		t.Fatalf("after installing: %+v (job %+v)", tools.OSV, tools.OSV.Job)
	}
	if tools.Opengrep.Found || tools.Opengrep.Job != nil {
		t.Fatalf("opengrep was touched: %+v", tools.Opengrep)
	}
	// the installed program lives under the app's data, not in the project
	if !strings.Contains(tools.OSV.Path, string(filepath.Separator)+"tools"+string(filepath.Separator)) && tools.OSV.Path != os.Args[0] {
		t.Fatalf("path %s", tools.OSV.Path)
	}

	// the scan runs the installed program: the dependency engine is not skipped as "not installed"
	var started scan.Report
	a.call("security.scan.start", map[string]any{"projectId": p.ID}, &started)
	var rep *scan.Report
	a.waitFor("the scan to finish", func() bool {
		a.call("security.scan.get", map[string]any{"projectId": p.ID}, &rep)
		return rep != nil && rep.Status != scan.StatusRunning
	})
	for _, e := range rep.Engines {
		if e.ID == scan.Deps && (e.Status == scan.Skipped || strings.Contains(e.Reason, "not installed")) {
			t.Fatalf("the installed osv-scanner did not run: %+v", e)
		}
	}

	// Update with nothing newer says so
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	tools = a.waitInstall(p.ID, "osv-scanner", "done")
	if !strings.Contains(tools.OSV.Job.Phase, "already the newest release (v2.6.0)") || tools.OSV.Tag != "v2.6.0" {
		t.Fatalf("update with nothing newer: %+v", tools.OSV.Job)
	}
	// a newer release: Update installs it
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.7.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.7.0")}})
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	tools = a.waitInstall(p.ID, "osv-scanner", "done")
	if tools.OSV.Tag != "v2.7.0" || tools.OSV.Job.Phase != "done" || tools.OSV.Job.Tag != "v2.7.0" {
		t.Fatalf("after updating: %+v (job %+v)", tools.OSV, tools.OSV.Job)
	}

	// the pages were told at each step: the settings events carry the tool and the install's state
	var states []string
	for _, e := range a.eventsOf("security.settings") {
		if e["tool"] == "osv-scanner" && e["install"] != nil {
			states = append(states, e["install"].(string))
		}
	}
	if len(states) < 4 || states[0] != "running" || states[len(states)-1] != "done" {
		t.Fatalf("install events %v", states)
	}

	// GitHub's latest is an older release than the one installed (a backport published later): nothing is downgraded, by
	// the install nor by the update check
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.6.9", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.6.9")}})
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	tools = a.waitInstall(p.ID, "osv-scanner", "done")
	if tools.OSV.Tag != "v2.7.0" || !strings.Contains(tools.OSV.Job.Phase, "already the newest release (v2.7.0)") {
		t.Fatalf("downgraded: %+v (job %+v)", tools.OSV, tools.OSV.Job)
	}
	tools = scan.Tools{}
	a.call("security.tools.checkUpdates", map[string]any{"projectId": p.ID}, &tools)
	if tools.OSV.Latest != "" || tools.OSV.Tag != "v2.7.0" || tools.OSV.Job.State != "done" {
		t.Fatalf("the check offered or started a downgrade: %+v (job %+v)", tools.OSV, tools.OSV.Job)
	}

	// a tool Drydock does not install is refused
	if _, err := a.c.Call(context.Background(), "security.tool.install", []byte(`{"projectId":"`+p.ID+`","tool":"semgrep"}`)); err == nil || !strings.Contains(err.Error(), "not a scan tool") {
		t.Fatal(err)
	}
}

// An install can be cancelled while it downloads; nothing is left installed. A release without a usable Windows asset fails
// the install with the reason, and the page can try again.
func TestAScanToolInstallCanBeCancelledAndAFailureIsNamed(t *testing.T) {
	fx := scantest.NewReleases(t)
	fx.Add(scantest.ReleaseSpec{Repo: "opengrep/opengrep", Tag: "v2.0.0", Assets: map[string][]byte{
		"opengrep_windows_x86.zip": scantest.ZipOf(map[string][]byte{"opengrep.exe": []byte("MZ 2.0"), "libgmp.dll": []byte("dll")})}})
	a := installCore(t, fx)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	release := fx.HoldDownloads()
	defer release()
	var tools scan.Tools
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "opengrep"}, &tools)
	a.waitFor("the download to start", func() bool {
		tools = scan.Tools{}
		a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
		return tools.Opengrep.Job != nil && strings.HasPrefix(tools.Opengrep.Job.Phase, "downloading")
	})
	// a second Install while one runs is refused
	if _, err := a.c.Call(context.Background(), "security.tool.install", []byte(`{"projectId":"`+p.ID+`","tool":"opengrep"}`)); err == nil || !strings.Contains(err.Error(), "already being installed") {
		t.Fatal(err)
	}
	a.call("security.tool.install.cancel", map[string]any{"projectId": p.ID, "tool": "opengrep"}, &tools)
	tools = a.waitInstall(p.ID, "opengrep", "cancelled")
	if tools.Opengrep.Found {
		t.Fatalf("installed although cancelled: %+v", tools.Opengrep)
	}
	release()

	// the release listing has no Windows asset for osv-scanner: the install fails and says so
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.6.0", Assets: map[string][]byte{"osv-scanner_linux_amd64": []byte("elf")}})
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	tools = a.waitInstall(p.ID, "osv-scanner", "failed")
	if !strings.Contains(tools.OSV.Job.Error, "osv-scanner_windows_amd64.exe with a SHA-256 digest") || tools.OSV.Found {
		t.Fatalf("failed install: %+v", tools.OSV.Job)
	}
	// then the Windows asset appears: the next Install succeeds
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.6.1", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.6.1")}})
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	tools = a.waitInstall(p.ID, "osv-scanner", "done")
	if !tools.OSV.Found || tools.OSV.Tag != "v2.6.1" {
		t.Fatalf("after the retry: %+v", tools.OSV)
	}
}
