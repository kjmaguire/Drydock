package core_test

import (
	"context"
	"fmt"
	"net"
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
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/providers"
)

type localRig struct {
	releases *llamatest.ReleaseFixture
	hub      *llamatest.HubFixture
	standin  []string // the stand-in's options
}

// localCore is a core whose local models talk to the PROTOCOL FIXTURES of GitHub's releases and the Hugging Face Hub and
// start the LABELLED STAND-IN llama-server (this test binary) in place of the placeholder the fixture's archive holds.
func localCore(t *testing.T, rig *localRig) *app {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters:    map[string]agents.Adapter{"claude": &claude.Adapter{Exe: "claude-not-used"}},
		Credentials: providers.NewMemoryCredentialStoreForTests(),
		LocalModels: func(m *localllm.Manager) {
			m.GitHub = host.HTTPAPI{BaseURL: rig.releases.Server.URL}
			m.HTTP = &http.Client{Transport: rig.releases.Transport}
			m.Hub = &localllm.Hub{BaseURL: rig.hub.URL}
			m.Program = func(localllm.Installed) (string, []string) { return os.Args[0], llamatest.Args(rig.standin...) }
			m.Memory = func() localllm.Memory { return localllm.Memory{Total: 32 << 30, Available: 20 << 30, Known: true} }
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

// localCall decodes the view into a fresh value: a field the answer leaves out must not keep an older call's value.
func localCall(a *app, method string, params any) core.LocalView {
	a.t.Helper()
	var v core.LocalView
	a.call(method, params, &v)
	return v
}

// freePort is a port nothing listens on right now, for the tests that serve: the default 9933 is shared with every other
// Drydock (the e2e harness, a second test binary) that may run on the machine at the same time.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func newLocalRig(t *testing.T) *localRig {
	src, _ := localllm.SourceByID(localllm.SourceOfficial)
	rel := llamatest.NewReleases(t, src.Repo)
	rel.Add(llamatest.ReleaseSpec{Tag: "b11600", Prerelease: true, Body: "an older build", Assets: map[string][]byte{"llama-b11600-bin-win-vulkan-x64.zip": llamatest.ServerZip()}})
	rel.Add(llamatest.ReleaseSpec{Tag: "b11669", Prerelease: true, Body: "## What's new\nLiliCorr speculative decoding", Assets: map[string][]byte{
		"llama-b11669-bin-win-vulkan-x64.zip": llamatest.ServerZip(), "llama-b11669-bin-win-cpu-x64.zip": llamatest.ServerZip(),
	}})
	hub := llamatest.NewHub(t)
	gguf := make([]byte, 6000)
	for i := range gguf {
		gguf[i] = byte(i * 13)
	}
	hub.AddModel("ggml-org/gpt-oss-20b-GGUF", 900000, []string{"reasoning"}, map[string][]byte{"gpt-oss-20b-mxfp4.gguf": gguf})
	hub.AddModel("acme/tiny-GGUF", 10, nil, map[string][]byte{"tiny-Q4_0.gguf": gguf[:3000], "tiny-Q8_0.gguf": gguf})
	return &localRig{releases: rel, hub: hub, standin: []string{"--load-ms=300"}}
}

// The whole path a person takes on the Providers page: install the newest official build (checked, unpacked, its devices
// read), find a model on the Hub, download it (checked), start the server on it, and find it registered as a provider whose
// model takes tool calls; then stop it, change the cache type, and remove the model and the build.
func TestAPersonInstallsLlamaCppDownloadsAModelAndServesIt(t *testing.T) {
	rig := newLocalRig(t)
	a := localCore(t, rig)
	var v core.LocalView
	v = localCall(a, "local.get", nil)
	if !v.Supported || v.Runtime.Installed || len(v.Sources) != 2 || v.Sources[0].ID != "official" || v.Settings.CacheType != "q8_0" || v.Settings.Port != 9933 || len(v.Backends) != 5 {
		t.Fatalf("before: %+v", v)
	}
	if v.Sources[1].Warning == "" || !strings.Contains(strings.Join(v.Sources[1].CacheTypes, ","), "turbo4") || strings.Contains(strings.Join(v.Sources[0].CacheTypes, ","), "turbo") {
		t.Fatalf("sources: %+v", v.Sources)
	}

	// install: a background job the page follows (the archive is held back until the job has been seen running: from
	// the in-process fixture a 6 KB archive otherwise installs before the call answers, as it did on the Windows runner)
	release := rig.releases.HoldDownloads()
	v = localCall(a, "local.install", map[string]any{"backend": "vulkan"})
	if v.Runtime.Job == nil || v.Runtime.Job.State != "running" || v.Settings.Backend != "vulkan" {
		t.Fatalf("install started: %+v", v.Runtime.Job)
	}
	if err := a.callErr("local.install", map[string]any{}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatal(err)
	}
	release()
	a.waitFor("the install to finish", func() bool {
		v = localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	if v.Runtime.Job.State != "done" || !v.Runtime.Installed || v.Runtime.Tag != "b11669" || v.Runtime.Backend != "vulkan" || v.Runtime.Source != "official" {
		t.Fatalf("installed: %+v %+v", v.Runtime, v.Runtime.Job)
	}
	if !v.Runtime.Devices.Probed || len(v.Runtime.Devices.Devices) != 1 || v.Runtime.Devices.Devices[0].Kind != "CUDA" {
		t.Fatalf("devices: %+v", v.Runtime.Devices)
	}
	// installing again finds the build is the newest
	v = localCall(a, "local.install", map[string]any{})
	a.waitFor("the second install to finish", func() bool {
		v = localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	if v.Runtime.Job.State != "done" || !strings.Contains(v.Runtime.Job.Phase, "already the newest") {
		t.Fatalf("%+v", v.Runtime.Job)
	}

	// the Hub: suggestions, search, a repository's files with the fit
	var repos []localllm.Repo
	a.call("local.models.suggested", nil, &repos)
	if len(repos) != 1 || repos[0].ID != "ggml-org/gpt-oss-20b-GGUF" {
		t.Fatalf("suggested %+v", repos)
	}
	a.call("local.models.search", map[string]any{"query": "tiny"}, &repos)
	if len(repos) != 1 || repos[0].ID != "acme/tiny-GGUF" {
		t.Fatalf("search %+v", repos)
	}
	var vs []localllm.Variant
	a.call("local.model.variants", map[string]any{"repo": "acme/tiny-GGUF"}, &vs)
	if len(vs) != 2 || vs[0].Name != "Q4_0" || vs[0].Fit == nil || vs[0].Fit.Target != "gpu" || !vs[0].Fit.Fits {
		t.Fatalf("variants %+v", vs)
	}

	// the server's port is a setting: a free one here, since 9933 may be taken by another Drydock on this machine
	port := freePort(t)
	if v = localCall(a, "local.settings.set", map[string]any{"port": port}); v.Settings.Port != port {
		t.Fatalf("%+v", v.Settings)
	}

	// download: checked against the Hub's SHA-256, then listed with its fit
	v = localCall(a, "local.model.download", map[string]any{"repo": "acme/tiny-GGUF", "variant": "Q4_0"})
	if len(v.Downloads) != 1 || v.Downloads[0].Target != "acme/tiny-GGUF:Q4_0" {
		t.Fatalf("download started: %+v", v.Downloads)
	}
	a.waitFor("the download to finish", func() bool {
		v = localCall(a, "local.get", nil)
		return len(v.Downloads) == 1 && v.Downloads[0].State != "running"
	})
	if v.Downloads[0].State != "done" || len(v.Models) != 1 || v.Models[0].ID != "acme/tiny-GGUF:Q4_0" || v.Models[0].Size != 3000 || v.Models[0].Fit == nil || v.ModelsBytes != 3000 {
		t.Fatalf("downloaded: %+v %+v", v.Downloads, v.Models)
	}
	if err := a.callErr("local.model.download", map[string]any{"repo": "acme/tiny-GGUF", "variant": "F99"}); err == nil || !strings.Contains(err.Error(), "no file named") {
		t.Fatal(err)
	}

	// serve it: the server loads the model, becomes a provider whose model the router may use for agent work
	v = localCall(a, "local.server.start", map[string]any{"modelId": "acme/tiny-GGUF:Q4_0"})
	if v.Server.State != "running" || v.Server.ModelID != "acme/tiny-GGUF:Q4_0" || v.Server.BaseURL != fmt.Sprintf("http://127.0.0.1:%d/v1", port) || v.Settings.ProviderID == "" || v.Settings.ModelID != "acme/tiny-GGUF:Q4_0" {
		t.Fatalf("serving: %+v %+v", v.Server, v.Settings)
	}
	var provs []providers.Provider
	a.call("provider.list", nil, &provs)
	var prov *providers.Provider
	for i := range provs {
		if provs[i].ID == v.Settings.ProviderID {
			prov = &provs[i]
		}
	}
	if prov == nil || prov.Kind != providers.KindHost || prov.Flavor != providers.FlavorLlamaCpp || prov.Tier != providers.TierCheap || prov.DataClass != providers.DataPrivate || prov.BaseURL != fmt.Sprintf("http://127.0.0.1:%d/v1", port) || prov.MaxConcurrent != 1 || prov.KeyRef != "" {
		t.Fatalf("provider: %+v", prov)
	}
	var models []providers.Model
	a.call("provider.models", map[string]any{"id": prov.ID}, &models)
	if len(models) != 1 || models[0].ID != "acme/tiny-GGUF:Q4_0" || !models[0].SupportsTools || models[0].ContextLength != 4096 || !models[0].IsFree {
		t.Fatalf("catalog: %+v", models)
	}
	if err := a.callErr("local.model.remove", map[string]any{"id": "acme/tiny-GGUF:Q4_0"}); err == nil || !strings.Contains(err.Error(), "stop the server") {
		t.Fatal(err)
	}
	if len(v.Log) == 0 || !strings.Contains(strings.Join(v.Log, "\n"), "cache q8_0") {
		t.Fatalf("log: %v", v.Log)
	}

	// settings: the cache type must be one the build has; the fork's turbo types are refused on the official build
	if err := a.callErr("local.settings.set", map[string]any{"cacheType": "turbo4"}); err == nil || !strings.Contains(err.Error(), "not a KV cache type") {
		t.Fatal(err)
	}
	v = localCall(a, "local.settings.set", map[string]any{"cacheType": "q4_0", "contextSize": 65536})
	if v.Settings.CacheType != "q4_0" || v.Settings.ContextSize != 65536 {
		t.Fatalf("%+v", v.Settings)
	}
	if err := a.callErr("local.settings.set", map[string]any{"port": 80}); err == nil {
		t.Fatal("port 80 accepted")
	}

	// stop, then the model and the build can go
	v = localCall(a, "local.server.stop", nil)
	if v.Server.State != "stopped" {
		t.Fatalf("%+v", v.Server)
	}
	v = localCall(a, "local.model.remove", map[string]any{"id": "acme/tiny-GGUF:Q4_0"})
	if len(v.Models) != 0 || v.Settings.ModelID != "" {
		t.Fatalf("after remove: %+v %+v", v.Models, v.Settings)
	}
	v = localCall(a, "local.runtime.remove", nil)
	if v.Runtime.Installed || v.Runtime.Job != nil {
		t.Fatalf("after runtime remove: %+v", v.Runtime)
	}
	if err := a.callErr("local.server.start", map[string]any{"modelId": "acme/tiny-GGUF:Q4_0"}); err == nil || !strings.Contains(err.Error(), "install one first") {
		t.Fatal(err)
	}
	// the provider row stays (its history, prices, quotas); the health loop finds it down
	a.call("provider.list", nil, &provs)
	if len(provs) != 1 {
		t.Fatalf("providers after: %+v", provs)
	}
}

// A build whose --list-devices fails is asked once per build, like one that answers: the failure is shown on the page and
// the next reads of the page do not run the program again. The installed build's cache types are the ones the settings
// accept, whatever source is chosen for the next install.
func TestAFailedDeviceProbeIsKeptAndTheInstalledBuildDecidesTheCacheTypes(t *testing.T) {
	rig := newLocalRig(t)
	record := filepath.Join(t.TempDir(), "args.txt")
	rig.standin = []string{"--devices=fail", "--record=" + record}
	a := localCore(t, rig)
	var v core.LocalView
	localCall(a, "local.install", map[string]any{"backend": "cpu"})
	a.waitFor("the install to finish", func() bool {
		v = localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	if v.Runtime.Devices.Probed || !strings.Contains(v.Runtime.Devices.Error, "told not to list devices") {
		t.Fatalf("devices: %+v", v.Runtime.Devices)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatal("the program was not run for its devices")
	}
	_ = os.Remove(record)
	for range 3 {
		v = localCall(a, "local.get", nil)
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("a failed device probe was run again")
	}
	if !strings.Contains(v.Runtime.Devices.Error, "told not to list devices") {
		t.Fatalf("the failure was lost: %+v", v.Runtime.Devices)
	}
	// the fork is chosen for the next install (which does not happen): the official build on this PC still decides
	v = localCall(a, "local.settings.set", map[string]any{"source": "turboquant"})
	if v.Settings.Source != "turboquant" || v.Runtime.Source != "official" {
		t.Fatalf("%+v %+v", v.Settings, v.Runtime)
	}
	if err := a.callErr("local.settings.set", map[string]any{"cacheType": "turbo4"}); err == nil || !strings.Contains(err.Error(), "official") {
		t.Fatalf("turbo4 on the official build: %v", err)
	}
	if v = localCall(a, "local.settings.set", map[string]any{"cacheType": "f16"}); v.Settings.CacheType != "f16" {
		t.Fatalf("%+v", v.Settings)
	}
}

// A build that cannot load the model is reported as failed with the program's last line; a second start on the same
// settings restarts the server, and the server left running is stopped with the core.
func TestAServerThatFailsIsReportedAndARestartReplacesIt(t *testing.T) {
	rig := newLocalRig(t)
	rig.standin = []string{"--fail-start"}
	a := localCore(t, rig)
	var v core.LocalView
	v = localCall(a, "local.install", map[string]any{"backend": "cpu"})
	a.waitFor("the install to finish", func() bool {
		v = localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	localCall(a, "local.settings.set", map[string]any{"port": freePort(t)})
	// --fail-start also fails --list-devices? no: the stand-in lists devices before failing, so nothing is known either way
	v = localCall(a, "local.model.download", map[string]any{"repo": "acme/tiny-GGUF", "variant": "Q8_0"})
	a.waitFor("the download to finish", func() bool {
		v = localCall(a, "local.get", nil)
		return len(v.Downloads) == 1 && v.Downloads[0].State != "running"
	})
	if err := a.callErr("local.server.start", map[string]any{"modelId": "acme/tiny-GGUF:Q8_0"}); err == nil || !strings.Contains(err.Error(), "told to fail") {
		t.Fatal(err)
	}
	v = localCall(a, "local.get", nil)
	if v.Server.State != "failed" || !strings.Contains(v.Server.Error, "code 1") || v.Settings.ModelID != "" {
		t.Fatalf("%+v %+v", v.Server, v.Settings)
	}
	// a build that works: the restart replaces the failed one
	rig.standin = []string{}
	v = localCall(a, "local.server.start", map[string]any{"modelId": "acme/tiny-GGUF:Q8_0"})
	if v.Server.State != "running" {
		t.Fatalf("%+v", v.Server)
	}
	pid := v.Server.PID
	v = localCall(a, "local.server.start", map[string]any{"modelId": "acme/tiny-GGUF:Q8_0"})
	if v.Server.State != "running" || v.Server.PID == pid {
		t.Fatalf("restart: %+v (was %d)", v.Server, pid)
	}
	// the download row can be cleared once it is done
	v = localCall(a, "local.model.download.cancel", map[string]any{"id": "acme/tiny-GGUF:Q8_0"})
	if len(v.Downloads) != 0 {
		t.Fatalf("%+v", v.Downloads)
	}
}
