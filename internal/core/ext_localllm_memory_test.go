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
	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// openLocal is localCore on a data folder of the test's choosing, so a second core can start on what the first one left.
func openLocal(t *testing.T, rig *localRig, dataDir string, creds providers.CredentialStore) *core.Core {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters:    map[string]agents.Adapter{"claude": &claude.Adapter{Exe: "claude-not-used"}},
		Credentials: creds,
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
	return c
}

// smallModel is a LABELLED FIXTURE: a GGUF header written by the test (llamatest.GGUF) with the attention metadata of a small
// grouped-query model, 28 layers of 2 key-value heads of 128, served as the Hub's file. It is not a model: the stand-in
// llama-server never reads it.
func smallModel() []byte {
	return llamatest.GGUF("qwen2", map[string]any{"general.name": "a test fixture, not a model",
		"qwen2.block_count": uint32(28), "qwen2.context_length": uint32(32768), "qwen2.embedding_length": uint32(1536),
		"qwen2.attention.head_count": uint32(12), "qwen2.attention.head_count_kv": uint32(2)})
}

// Decision P97 on the Models on this PC page: each downloaded model's KV cache is estimated from its GGUF header at the context
// size and cache type the server starts with, and its fit counts the model and the cache; the served model's memory (model,
// cache, the GPU's total and free) is there for the memory bar; the agents whose provider is the server are listed with their
// projects; and the server says it was started by a person, or by Drydock at its start (autostart).
func TestModelsOnThisPCShowTheKVCacheTheAgentsThatUseItAndHowTheServerStarted(t *testing.T) {
	rig := newLocalRig(t)
	gguf := smallModel()
	rig.hub.AddModel("acme/small-GGUF", 5, nil, map[string][]byte{"small-Q8_0.gguf": gguf})
	dataDir := filepath.Join(t.TempDir(), "data")
	creds := providers.NewMemoryCredentialStoreForTests()
	c := openLocal(t, rig, dataDir, creds)
	a := &app{t: t, c: c}
	localCall(a, "local.install", map[string]any{"backend": "vulkan"})
	var v core.LocalView
	a.waitFor("the install", func() bool {
		v = localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	port := freePort(t)
	localCall(a, "local.settings.set", map[string]any{"port": port, "contextSize": 8192, "cacheType": "q8_0"})
	localCall(a, "local.model.download", map[string]any{"repo": "acme/small-GGUF", "variant": "Q8_0"})
	a.waitFor("the download", func() bool {
		v = localCall(a, "local.get", nil)
		return len(v.Downloads) == 1 && v.Downloads[0].State != "running"
	})
	const kvBytes = 28 * 2 * 256 * 8192 * 34 / 32
	if len(v.Models) != 1 || v.Models[0].KV == nil || !v.Models[0].KV.Known || v.Models[0].KV.Bytes != kvBytes || v.Models[0].KV.ContextSize != 8192 || v.Models[0].KV.CacheType != "q8_0" {
		t.Fatalf("%+v %+v", v.Models, v.Models[0].KV)
	}
	if f := v.Models[0].Fit; f.KVBytes != kvBytes || f.NeedBytes != uint64(len(gguf))+kvBytes || f.Target != "gpu" {
		t.Fatalf("fit %+v", f)
	}
	if v.Served != nil || len(v.UsedBy) != 0 || v.Server.Autostarted {
		t.Fatalf("nothing served yet: %+v %+v", v.Served, v.UsedBy)
	}

	// served by a person
	v = localCall(a, "local.server.start", map[string]any{"modelId": "acme/small-GGUF:Q8_0"})
	if v.Server.State != "running" || v.Server.Autostarted || v.Server.StartedAt.IsZero() || v.Server.CacheType != "q8_0" || v.Server.ContextSize != 8192 {
		t.Fatalf("%+v", v.Server)
	}
	s := v.Served
	if s == nil || s.ModelID != "acme/small-GGUF:Q8_0" || s.ModelBytes != uint64(len(gguf)) || s.KVBytes != kvBytes || !s.KV.Known || s.Device != "gpu" ||
		!strings.Contains(s.DeviceName, "Stand-in GPU") || s.DeviceTotal != 16384<<20 || s.DeviceFree != 14336<<20 || s.Fit.Target != "gpu" {
		t.Fatalf("served %+v", s)
	}
	// a cache type the build cannot size: the estimate says why; the served one keeps what it runs with until it starts again
	v = localCall(a, "local.settings.set", map[string]any{"cacheType": "f16", "contextSize": 0})
	if v.Served == nil || v.Served.KV.CacheType != "q8_0" || v.Served.KVBytes != kvBytes {
		t.Fatalf("the served model is measured as it runs: %+v", v.Served)
	}

	// used by: the agents of every project whose provider is the server's row
	var p1, p2 core.Project
	a.call("project.create", map[string]any{"name": "alpha"}, &p1)
	a.call("project.create", map[string]any{"name": "beta"}, &p2)
	var local1, local2, other core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p2.ID, Name: "Helper", Adapter: "claude", Role: "backend", Model: "fake-1", ProviderID: v.Settings.ProviderID}, &local2)
	a.call("agent.create", core.AgentSpec{ProjectID: p1.ID, Name: "Builder", Adapter: "claude", Role: "backend", Model: "fake-1", ProviderID: v.Settings.ProviderID}, &local1)
	a.call("agent.create", core.AgentSpec{ProjectID: p1.ID, Name: "Cloud", Adapter: "claude", Role: "backend", Model: "fake-1"}, &other)
	v = localCall(a, "local.get", nil)
	if len(v.UsedBy) != 2 || v.UsedBy[0] != (core.LocalUser{AgentID: local1.ID, Name: "Builder", ProjectID: p1.ID, ProjectName: "alpha"}) ||
		v.UsedBy[1] != (core.LocalUser{AgentID: local2.ID, Name: "Helper", ProjectID: p2.ID, ProjectName: "beta"}) {
		t.Fatalf("used by %+v", v.UsedBy)
	}

	// Drydock starts again: the server is started by itself (Autostart is on and a model was served), and says so
	c.Close()
	c2 := openLocal(t, rig, dataDir, creds)
	t.Cleanup(c2.Close)
	a2 := &app{t: t, c: c2}
	a2.waitFor("the server to be started again", func() bool {
		v = localCall(a2, "local.get", nil)
		return v.Server.State == "running"
	})
	if !v.Server.Autostarted || v.Server.StartedAt.IsZero() || v.Server.ModelID != "acme/small-GGUF:Q8_0" {
		t.Fatalf("%+v", v.Server)
	}
	// started with the settings saved since: f16 at the model's own context
	if v.Served == nil || v.Served.KV.CacheType != "f16" || v.Served.KV.ContextSize != 32768 || v.Served.KVBytes != 28*2*256*32768*2 || !strings.Contains(v.Served.KV.Note, "own context") {
		t.Fatalf("%+v", v.Served)
	}
	if len(v.UsedBy) != 2 {
		t.Fatalf("%+v", v.UsedBy)
	}
	// a person's start is not an autostart
	localCall(a2, "local.server.stop", nil)
	v = localCall(a2, "local.server.start", map[string]any{"modelId": "acme/small-GGUF:Q8_0"})
	if v.Server.Autostarted {
		t.Fatalf("%+v", v.Server)
	}
}
