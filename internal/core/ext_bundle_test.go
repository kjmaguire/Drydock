package core_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/bundle"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
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
// folder beside drydock.exe (no download), the Security page shows them installed and ready with the built-in rules, and the
// update check offers a newer release when GitHub has one; Update is still the person's.
func TestTheDownloadsScanToolsComeInstalledAndAreOfferedUpdates(t *testing.T) {
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
	// GitHub has osv-scanner v2.8.0: the check offers it, and Opengrep (no newer release listed) is not offered anything
	fx.Add(scantest.ReleaseSpec{Repo: "google/osv-scanner", Tag: "v2.8.0", Assets: map[string][]byte{"osv-scanner_windows_amd64.exe": []byte("MZ 2.8.0")}})
	fx.Add(scantest.ReleaseSpec{Repo: "opengrep/opengrep", Tag: "v1.31.0", Assets: map[string][]byte{"opengrep_windows_x86.exe": ogExe}})
	tools = scan.Tools{}
	a.call("security.tools.checkUpdates", map[string]any{"projectId": p.ID}, &tools)
	if tools.OSV.Latest != "v2.8.0" || tools.Opengrep.Latest != "" {
		t.Fatalf("after the check: osv %q opengrep %q", tools.OSV.Latest, tools.Opengrep.Latest)
	}
	// nothing was installed by the check; Update does it, and then nothing is offered
	tools = scan.Tools{}
	a.call("security.tool.install", map[string]any{"projectId": p.ID, "tool": "osv-scanner"}, &tools)
	tools = a.waitInstall(p.ID, "osv-scanner", "done")
	if tools.OSV.Tag != "v2.8.0" || tools.OSV.Latest != "" {
		t.Fatalf("after Update: %+v", tools.OSV)
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
	// a newer release of the same source and backend: Check for updates names it; nothing is installed by itself
	newer := "b10350-2.1.0"
	rel.Add(llamatest.ReleaseSpec{Tag: newer, Assets: map[string][]byte{"llama-turboquant-windows-x64-vulkan.zip": zip}})
	v = core.LocalView{}
	a.call("local.checkUpdates", nil, &v)
	if v.Runtime.Latest != newer || v.Runtime.Tag != tag {
		t.Fatalf("after the check: tag %q latest %q", v.Runtime.Tag, v.Runtime.Latest)
	}
}
