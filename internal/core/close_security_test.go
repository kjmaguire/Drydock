package core_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// heldCheck is a tool installer's check (the test scan a release runs before it is used) that holds the first install open until
// released, then answers as the real check does when the core is closing: cancelled.
type heldCheck struct {
	entered, release chan struct{}
	held, returned   atomic.Bool
}

func newHeldCheck() *heldCheck {
	return &heldCheck{entered: make(chan struct{}), release: make(chan struct{})}
}

func (h *heldCheck) check(ctx context.Context, _ string, _ scan.Tool) error {
	if !h.held.CompareAndSwap(false, true) {
		return nil
	}
	close(h.entered)
	<-h.release
	defer h.returned.Store(true)
	return ctx.Err()
}

// closeWhileHeld closes the core while the install is held, releases it a while after Close began, and checks that Close waited
// for the install and that nothing in the data folder changes once Close has returned.
func closeWhileHeld(t *testing.T, c *core.Core, dataDir string, h *heldCheck) {
	t.Helper()
	select {
	case <-h.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the install did not reach its check")
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(h.release)
	}()
	c.Close()
	if !h.returned.Load() {
		t.Error("Close returned while the tool install was still running")
	}
	before := snapshotDir(t, dataDir)
	time.Sleep(500 * time.Millisecond)
	after := snapshotDir(t, dataDir)
	for path, mod := range after {
		if was, ok := before[path]; !ok || !was.Equal(mod) {
			t.Errorf("written after Close returned: %s", path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("removed after Close returned: %s", path)
		}
	}
}

// An install a person asked for (security.tool.install) is cancelled by Close and Close waits for it: the release it was
// placing under <data>/tools is not written or removed after Close returns.
func TestCloseWaitsForAToolInstall(t *testing.T) {
	fx := scantest.NewReleases(t)
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.6.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.6.0")}})
	h := newHeldCheck()
	dataDir := filepath.Join(t.TempDir(), "data")
	missing := filepath.Join(t.TempDir(), "not-installed")
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		ScanTools: scan.Paths{OSV: scan.Tool{Path: missing, Args: scantest.Args("osv-scanner")}, Opengrep: scan.Tool{Path: missing, Args: scantest.Args("opengrep")}},
		ScanInstall: func(i *scan.Installer) {
			i.GitHub = host.HTTPAPI{BaseURL: fx.Server.URL}
			i.HTTP = &http.Client{Transport: fx.Transport}
			i.Program = func(in scan.InstalledTool) (string, []string) { return os.Args[0], scantest.Args(in.Tool) }
			i.Check = h.check
		}})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			c.Close()
		}
	})
	a := &app{t: t, c: c}
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var tools scan.Tools
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	closed = true
	closeWhileHeld(t, c, dataDir, h)
}

// The download's tools are seeded in the background at start (decision P77); Close stops the seed and waits for it, so no copy
// is placed or cleaned up under <data>/tools after Close returns.
func TestCloseWaitsForTheDownloadsToolSeed(t *testing.T) {
	osvExe := []byte("MZ osv 2.7.0")
	osvSrc, _ := scan.SourceOf("osv-scanner")
	dir := writeBundle(t, map[string][]byte{"osv-scanner_windows_amd64.exe": osvExe}, bundle.Manifest{Version: 1, Scan: []bundle.ScanTool{
		{Tool: "osv-scanner", Tag: "v2.7.0", Asset: assetOf(osvSrc.DownloadPrefix(), "v2.7.0", "osv-scanner_windows_amd64.exe", osvExe)},
	}})
	fx := scantest.NewReleases(t)
	h := newHeldCheck()
	dataDir := filepath.Join(t.TempDir(), "data")
	missing := filepath.Join(t.TempDir(), "not-installed")
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		BundleDir: dir,
		ScanTools: scan.Paths{OSV: scan.Tool{Path: missing, Args: scantest.Args("osv-scanner")}, Opengrep: scan.Tool{Path: missing, Args: scantest.Args("opengrep")}},
		ScanInstall: func(i *scan.Installer) {
			i.GitHub = host.HTTPAPI{BaseURL: fx.Server.URL}
			i.HTTP = &http.Client{Transport: fx.Transport}
			i.Program = func(in scan.InstalledTool) (string, []string) { return os.Args[0], scantest.Args(in.Tool) }
			i.Check = h.check
		}})
	if err != nil {
		t.Fatal(err)
	}
	closeWhileHeld(t, c, dataDir, h)
}
