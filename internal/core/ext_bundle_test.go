package core_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/runners"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// writeBundle makes a download's tools folder: the release files and bundle.json, as cmd/drydock-bundle writes them.
func writeBundle(t *testing.T, files map[string][]byte, m bundle.Manifest) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, bundle.FileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func assetOf(prefix, tag, name string, body []byte) runners.Asset {
	sum := sha256.Sum256(body)
	return runners.Asset{Name: name, URL: prefix + tag + "/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
}

// Decision P77: a download's tools come installed. The app starts with osv-scanner and Opengrep put in place from the tools
// folder beside drydock.exe (no download), the Security page shows them installed and ready with the built-in rules, and
// Check for updates installs a newer release when GitHub has one (decision P95).
func TestTheDownloadsScanToolsComeInstalledAndCheckForUpdatesInstallsANewerOne(t *testing.T) {
	fx := scantest.NewReleases(t)
	osvExe, ogExe := []byte("MZ osv 2.7.0"), []byte("MZ opengrep 1.31.0")
	osvSrc, _ := scan.SourceOf("osv-scanner")
	ogSrc, _ := scan.SourceOf("opengrep")
	dir := writeBundle(t, map[string][]byte{"osv-scanner_windows_amd64.exe": osvExe, "opengrep_windows_x86.exe": ogExe}, bundle.Manifest{Version: 1, Scan: []bundle.ScanTool{
		{Tool: "osv-scanner", Tag: "v2.7.0", Asset: assetOf(osvSrc.DownloadPrefix(), "v2.7.0", "osv-scanner_windows_amd64.exe", osvExe)},
		{Tool: "opengrep", Tag: "v1.31.0", Asset: assetOf(ogSrc.DownloadPrefix(), "v1.31.0", "opengrep_windows_x86.exe", ogExe)},
	}})
	missing := filepath.Join(t.TempDir(), "not-installed")
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		BundleDir: dir,
		ScanTools: scan.Paths{OSV: scan.Tool{Path: missing, Args: scantest.Args("osv-scanner")}, Opengrep: scan.Tool{Path: missing, Args: scantest.Args("opengrep")}},
		ScanInstall: func(i *scan.Installer) {
			i.GitHub = host.HTTPAPI{BaseURL: fx.Server.URL}
			i.HTTP = &http.Client{Transport: fx.Transport} // the fixture serves only what is added to it: the seed asks nothing
			i.Program = func(in scan.InstalledTool) (string, []string) { return os.Args[0], scantest.Args(in.Tool) }
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var tools scan.Tools
	a.waitFor("the download's tools to be in place", func() bool {
		tools = scan.Tools{}
		a.call("security.tools", map[string]any{"projectId": p.ID}, &tools)
		return tools.OSV.Source == "installed" && tools.Opengrep.Source == "installed"
	})
	if tools.OSV.Tag != "v2.7.0" || tools.Opengrep.Tag != "v1.31.0" || !tools.Ready || !tools.BuiltinRules || tools.OSV.Latest != "" {
		t.Fatalf("%+v", tools)
	}
	if n := fx.Downloads.Load(); n != 0 {
		t.Fatalf("the download's tools were fetched from GitHub: %d downloads", n)
	}
	// GitHub has osv-scanner v2.8.0: Check for updates installs it, and Opengrep (no newer release listed) is left as it is
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.8.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.8.0")}})
	fx.Add(scantest.ReleaseSpec{Repo: "opengrep/opengrep", Tag: "v1.31.0", Assets: map[string][]byte{"opengrep_windows_x86.exe": ogExe}})
	tools = scan.Tools{}
	a.call("security.tools.checkUpdates", map[string]any{"projectId": p.ID}, &tools)
	if tools.OSV.Job == nil || tools.Opengrep.Latest != "" { // running, or already done on a fast machine
		t.Fatalf("after the check: osv %+v, opengrep latest %q", tools.OSV.Job, tools.Opengrep.Latest)
	}
	tools = a.waitInstall(p.ID, "osv-scanner", "done")
	if tools.OSV.Tag != "v2.8.0" || tools.OSV.Latest != "" || tools.Opengrep.Tag != "v1.31.0" {
		t.Fatalf("after the check's install: osv %+v, opengrep %+v", tools.OSV, tools.Opengrep)
	}
}

// Decision P77: llama.cpp with the TurboQuant KV cache types comes installed. The app starts with the download's build in
// place, and the settings follow it (the TurboQuant source and its first cache type, turbo4) since the person had chosen none.
func TestTheDownloadsLlamaBuildComesInstalledWithTurboQuant(t *testing.T) {
	tq, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	zip := llamatest.ZipOf(map[string][]byte{localllm.ServerName(): []byte("server"), "ggml-vulkan.dll": []byte("dll")})
	tag := "b10298-2.0.0"
	dir := writeBundle(t, map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": zip}, bundle.Manifest{Version: 1,
		Llama: &bundle.LlamaBuild{Source: tq.ID, Tag: tag, Backend: string(localllm.Vulkan), Archive: assetOf(tq.DownloadPrefix(), tag, "llama-turboquant-windows-x64-vulkan.zip", zip)}})
	rel := llamatest.NewReleases(t, tq.Repo)
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		BundleDir: dir,
		LocalModels: func(m *localllm.Manager) {
			m.GitHub = host.HTTPAPI{BaseURL: rel.Server.URL}
			m.HTTP = &http.Client{Transport: rel.Transport}
			m.Program = func(in localllm.Installed) (string, []string) { return os.Args[0], llamatest.Args() }
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var v core.LocalView
	a.waitFor("the download's llama.cpp build to be in place", func() bool {
		v = core.LocalView{}
		a.call("local.get", nil, &v)
		return v.Runtime.Installed
	})
	if v.Runtime.Source != localllm.SourceTurboQuant || v.Runtime.Tag != tag || v.Runtime.Backend != localllm.Vulkan {
		t.Fatalf("%+v", v.Runtime)
	}
	a.waitFor("the settings to follow the build", func() bool {
		v = core.LocalView{}
		a.call("local.get", nil, &v)
		return v.Settings.Source == localllm.SourceTurboQuant
	})
	if v.Settings.CacheType != "turbo4" || v.Settings.Backend != localllm.Vulkan {
		t.Fatalf("settings %+v", v.Settings)
	}
	// the page says the build came with the download, and nothing newer is known yet
	if !v.Runtime.Bundled || v.Runtime.Latest != "" {
		t.Fatalf("bundled %v latest %q", v.Runtime.Bundled, v.Runtime.Latest)
	}
	// a backport to an older line, published after the bundled release, is not offered: it would be a downgrade
	rel.Add(llamatest.ReleaseSpec{Tag: "b10269-1.7.1", Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": zip}})
	v = core.LocalView{}
	a.call("local.checkUpdates", nil, &v)
	if v.Runtime.Latest != "" {
		t.Fatalf("a backport was offered: %q", v.Runtime.Latest)
	}
	// a newer release of the same source and backend: Check for updates installs it (decision P95)
	newer := "b10350-2.1.0"
	rel.Add(llamatest.ReleaseSpec{Tag: newer, Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": zip}})
	v = core.LocalView{}
	a.call("local.checkUpdates", nil, &v)
	if v.Runtime.Job == nil || v.Runtime.Job.Kind != "install" { // running, or already done on a fast machine
		t.Fatalf("after the check: %+v", v.Runtime.Job)
	}
	a.waitFor("the newer build to be installed", func() bool {
		v = core.LocalView{}
		a.call("local.get", nil, &v)
		return v.Runtime.Tag == newer
	})
	if v.Runtime.Source != localllm.SourceTurboQuant || v.Runtime.Backend != localllm.Vulkan || v.Runtime.Latest != "" {
		t.Fatalf("after the update: %+v", v.Runtime)
	}
}

// Decision P95: a download without its tools folder (the zips built by hand on 2026-10-21 had none), or with one whose
// bundle.json cannot be read, still sets itself up. With AutoSetup, the app downloads osv-scanner, Opengrep and the TurboQuant
// build at its first start, checked against GitHub's digests, with no button pressed. A scan tool removed in an earlier
// version (which kept it out with a marker) comes back, since nothing on the page can install it now; the model build a
// person removed stays removed (the Local models page has Set up for it).
func TestWithoutTheToolsFolderTheAppInstallsTheScannersAndTheTurboQuantBuildItself(t *testing.T) {
	fx := scantest.NewReleases(t)
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.7.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.7.0")}})
	fx.Add(scantest.ReleaseSpec{Repo: "opengrep/opengrep", Tag: "v1.31.0", Assets: map[string][]byte{"opengrep_windows_x86.exe": []byte("MZ 1.31.0")}})
	tq, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	zip := llamatest.ZipOf(map[string][]byte{localllm.ServerName(): []byte("server"), "ggml-vulkan.dll": []byte("dll")})
	rel := llamatest.NewReleases(t, tq.Repo)
	rel.Add(llamatest.ReleaseSpec{Tag: "b10298-2.0.0", Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": zip}})
	data := filepath.Join(t.TempDir(), "data")
	missing := filepath.Join(t.TempDir(), "not-installed")
	empty := t.TempDir() // a folder with no bundle.json: the download carried nothing
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "bundle.json"), []byte(`{"version":1,"scan":[`), 0o644); err != nil {
		t.Fatal(err)
	}
	open := func(bundleDir string) *app {
		c, err := core.Open(context.Background(), core.Config{DataDir: data, User: "kyle", LeaseTTL: 3 * time.Second, AutoSetup: true,
			Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
			BundleDir: bundleDir,
			ScanTools: scan.Paths{OSV: scan.Tool{Path: missing, Args: scantest.Args("osv-scanner")}, Opengrep: scan.Tool{Path: missing, Args: scantest.Args("opengrep")}},
			ScanInstall: func(i *scan.Installer) {
				i.GitHub = host.HTTPAPI{BaseURL: fx.Server.URL}
				i.HTTP = &http.Client{Transport: fx.Transport}
				i.Program = func(in scan.InstalledTool) (string, []string) { return os.Args[0], scantest.Args(in.Tool) }
			},
			LocalModels: func(m *localllm.Manager) {
				m.GitHub = host.HTTPAPI{BaseURL: rel.Server.URL}
				m.HTTP = &http.Client{Transport: rel.Transport}
				m.Program = func(in localllm.Installed) (string, []string) { return os.Args[0], llamatest.Args() }
			}})
		if err != nil {
			t.Fatal(err)
		}
		return &app{t: t, c: c}
	}
	a := open(empty)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	a.waitInstall(p.ID, "opengrep", "done")
	tools := a.waitInstall(p.ID, "osv-scanner", "done")
	if tools.OSV.Tag != "v2.7.0" || tools.Opengrep.Tag != "v1.31.0" || !tools.Ready {
		t.Fatalf("%+v", tools)
	}
	var v core.LocalView
	a.waitFor("the TurboQuant build to be set up", func() bool {
		v = core.LocalView{}
		a.call("local.get", nil, &v)
		return v.Runtime.Installed
	})
	if v.Runtime.Source != localllm.SourceTurboQuant || v.Runtime.Backend != localllm.Vulkan || v.Settings.CacheType != "turbo4" {
		t.Fatalf("runtime %+v settings %+v", v.Runtime, v.Settings)
	}
	// Opengrep removed by an earlier version (its folder gone, the marker written) and the build removed by the person; the
	// next start's tools folder cannot be read
	a.call("local.runtime.remove", nil, nil)
	a.c.Close()
	tools0 := filepath.Join(data, "tools")
	if err := os.RemoveAll(filepath.Join(tools0, "opengrep")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools0, "opengrep.removed"), []byte("2026-10-01T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llamaDownloads := rel.Downloads.Load()
	a = open(broken)
	t.Cleanup(a.c.Close)
	tools = a.waitInstall(p.ID, "opengrep", "done")
	if tools.Opengrep.Source != "installed" || tools.Opengrep.Tag != "v1.31.0" || tools.OSV.Tag != "v2.7.0" {
		t.Fatalf("opengrep did not come back: %+v", tools)
	}
	time.Sleep(500 * time.Millisecond) // the start's setup runs in the background: give it the time to (wrongly) begin
	v = core.LocalView{}
	a.call("local.get", nil, &v)
	if v.Runtime.Installed || v.Runtime.Job != nil || rel.Downloads.Load() != llamaDownloads {
		t.Fatalf("the removed build came back: %+v", v.Runtime)
	}
}

// A tools folder whose bundle.json cannot be read sets local models up the way a download without one does (decision P95).
func TestABrokenToolsFolderStillSetsUpTheTurboQuantBuild(t *testing.T) {
	tq, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	zip := llamatest.ZipOf(map[string][]byte{localllm.ServerName(): []byte("server"), "ggml-vulkan.dll": []byte("dll")})
	rel := llamatest.NewReleases(t, tq.Repo)
	rel.Add(llamatest.ReleaseSpec{Tag: "b10298-2.0.0", Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": zip}})
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "bundle.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second, AutoSetup: true,
		Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		BundleDir: broken,
		LocalModels: func(m *localllm.Manager) {
			m.GitHub = host.HTTPAPI{BaseURL: rel.Server.URL}
			m.HTTP = &http.Client{Transport: rel.Transport}
			m.Program = func(in localllm.Installed) (string, []string) { return os.Args[0], llamatest.Args() }
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var v core.LocalView
	a.waitFor("the TurboQuant build to be set up", func() bool {
		v = core.LocalView{}
		a.call("local.get", nil, &v)
		return v.Runtime.Installed
	})
	if v.Runtime.Source != localllm.SourceTurboQuant || v.Runtime.Tag != "b10298-2.0.0" {
		t.Fatalf("%+v", v.Runtime)
	}
}

// An update of the app that carries a newer bundled llama.cpp build installs it before the server starts by itself
// (Autostart): the server never starts on the build being replaced and deleted (audit 2026-10-27).
func TestAfterAnAppUpdateTheServerStartsOnTheNewBundledBuild(t *testing.T) {
	tq, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	zip := llamatest.ZipOf(map[string][]byte{localllm.ServerName(): []byte("server"), "ggml-vulkan.dll": []byte("dll")})
	bundleOf := func(tag string) string {
		return writeBundle(t, map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": zip}, bundle.Manifest{Version: 1,
			Llama: &bundle.LlamaBuild{Source: tq.ID, Tag: tag, Backend: string(localllm.Vulkan), Archive: assetOf(tq.DownloadPrefix(), tag, "llama-turboquant-windows-x64-vulkan.zip", zip)}})
	}
	rig := newLocalRig(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	creds := providers.NewMemoryCredentialStoreForTests()
	// every run of a build records its arguments in a file of its own, numbered in order: the build's --list-devices probe
	// (the Models page reads the devices of whatever build is installed) runs it too, and is not a start of the server
	records := t.TempDir()
	var mu sync.Mutex
	runs := 0
	open := func(bundleDir string) *core.Core {
		c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
			Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: "claude-not-used"}}, Credentials: creds, BundleDir: bundleDir,
			LocalModels: func(m *localllm.Manager) {
				m.GitHub = host.HTTPAPI{BaseURL: rig.releases.Server.URL}
				m.HTTP = &http.Client{Transport: rig.releases.Transport}
				m.Hub = &localllm.Hub{BaseURL: rig.hub.URL}
				m.Program = func(in localllm.Installed) (string, []string) {
					mu.Lock()
					runs++
					record := filepath.Join(records, fmt.Sprintf("%04d_%s", runs, in.Tag))
					mu.Unlock()
					return os.Args[0], llamatest.Args(append([]string{"--record=" + record}, rig.standin...)...)
				}
			}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := open(bundleOf("b10298-2.0.0"))
	a := &app{t: t, c: c}
	a.waitFor("the bundled build", func() bool { return localCall(a, "local.get", nil).Runtime.Tag == "b10298-2.0.0" })
	localCall(a, "local.settings.set", map[string]any{"port": freePort(t)})
	localCall(a, "local.model.download", map[string]any{"repo": "acme/tiny-GGUF", "variant": "Q8_0"})
	a.waitFor("the download", func() bool {
		v := localCall(a, "local.get", nil)
		return len(v.Downloads) == 1 && v.Downloads[0].State != "running"
	})
	if v := localCall(a, "local.server.start", map[string]any{"modelId": "acme/tiny-GGUF:Q8_0"}); v.Server.State != "running" {
		t.Fatalf("%+v", v.Server)
	}
	c.Close()

	mu.Lock()
	before := runs
	mu.Unlock()
	c2 := open(bundleOf("b10350-2.1.0")) // the app updated, with a newer bundled build
	t.Cleanup(c2.Close)
	a2 := &app{t: t, c: c2}
	var v core.LocalView
	a2.waitFor("the server to start by itself", func() bool {
		v = localCall(a2, "local.get", nil)
		return v.Server.State == "running"
	})
	var started []string // the build each start of the server after the update ran, in order
	files, err := os.ReadDir(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		var n int
		var tag string
		if _, err := fmt.Sscanf(f.Name(), "%d_%s", &n, &tag); err != nil || n <= before {
			continue
		}
		args, err := os.ReadFile(filepath.Join(records, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(args), "--list-devices") {
			started = append(started, tag)
		}
	}
	if v.Runtime.Tag != "b10350-2.1.0" || len(started) == 0 || started[0] != "b10350-2.1.0" {
		t.Fatalf("installed %s; the server started on %v", v.Runtime.Tag, started)
	}
}

// With Autostart on and no build here (its server deleted, as an antivirus may do), the start fetches the TurboQuant build and
// the server starts by itself once that is in place (review 2026-10-27: Autostart ran before the fetch, found no build, and
// never ran again).
func TestWithAutostartTheServerStartsOnceAMissingBuildIsFetched(t *testing.T) {
	tq, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	zip := llamatest.ZipOf(map[string][]byte{localllm.ServerName(): []byte("server"), "ggml-vulkan.dll": []byte("dll")})
	rel := llamatest.NewReleases(t, tq.Repo)
	rel.Add(llamatest.ReleaseSpec{Tag: "b10298-2.0.0", Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": zip}})
	rig := newLocalRig(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	empty := t.TempDir() // the download carries no tools folder
	creds := providers.NewMemoryCredentialStoreForTests()
	open := func() *app {
		c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second, AutoSetup: true,
			Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: "claude-not-used"}}, Credentials: creds, BundleDir: empty,
			LocalModels: func(m *localllm.Manager) {
				m.GitHub = host.HTTPAPI{BaseURL: rel.Server.URL}
				m.HTTP = &http.Client{Transport: rel.Transport}
				m.Hub = &localllm.Hub{BaseURL: rig.hub.URL}
				m.Program = func(in localllm.Installed) (string, []string) { return os.Args[0], llamatest.Args(rig.standin...) }
			}})
		if err != nil {
			t.Fatal(err)
		}
		return &app{t: t, c: c}
	}
	a := open()
	var v core.LocalView
	a.waitFor("the TurboQuant build to be set up", func() bool {
		v = localCall(a, "local.get", nil)
		return v.Runtime.Installed && v.Runtime.Job != nil && v.Runtime.Job.State == "done"
	})
	localCall(a, "local.settings.set", map[string]any{"port": freePort(t)})
	localCall(a, "local.model.download", map[string]any{"repo": "acme/tiny-GGUF", "variant": "Q8_0"})
	a.waitFor("the download", func() bool {
		v := localCall(a, "local.get", nil)
		return len(v.Downloads) == 1 && v.Downloads[0].State != "running"
	})
	if v := localCall(a, "local.server.start", map[string]any{"modelId": "acme/tiny-GGUF:Q8_0"}); v.Server.State != "running" {
		t.Fatalf("%+v", v.Server)
	}
	server := v.Runtime.Server
	a.c.Close()
	if err := os.Remove(server); err != nil {
		t.Fatal(err)
	}

	a = open()
	t.Cleanup(a.c.Close)
	a.waitFor("the server to start by itself on the fetched build", func() bool {
		v = localCall(a, "local.get", nil)
		return v.Server.State == "running"
	})
	if !v.Runtime.Installed || v.Runtime.Job == nil || v.Runtime.Job.Kind != "install" || v.Runtime.Job.State != "done" {
		t.Fatalf("the build was not set up again: %+v %+v", v.Runtime, v.Runtime.Job)
	}
}
