package core_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// threeModels is a rig with three small Qwen3-family models, installed, at a free port; it gives their ids.
func threeModels(t *testing.T, rig *localRig) (*app, [3]string) {
	t.Helper()
	for _, n := range []string{"a", "b", "c"} {
		rig.hub.AddModel("acme/"+n+"-GGUF", 10, nil, map[string][]byte{n + "-Q4_0.gguf": padded("qwen3", qwenHeader("qwen3", nil), 9000)})
	}
	a := localCore(t, rig)
	localCall(a, "local.install", map[string]any{"backend": "vulkan"})
	a.waitFor("the install", func() bool {
		v := localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	localCall(a, "local.settings.set", map[string]any{"port": freePort(t), "embeddingPort": freePort(t)})
	var ids [3]string
	for i, n := range []string{"a", "b", "c"} {
		download(a, "acme/"+n+"-GGUF", "Q4_0")
		ids[i] = "acme/" + n + "-GGUF:Q4_0"
	}
	return a, ids
}

func providerModels(a *app, id string) string {
	a.t.Helper()
	var models []providers.Model
	a.call("provider.models", map[string]any{"id": id}, &models)
	var out []string
	for _, m := range models {
		out = append(out, m.ID)
	}
	return strings.Join(out, ",")
}

// Load too and Unload while the router runs leave the other models loaded (decision P120): the server is not started again
// (the same process, the same start time, the provider's key unchanged), the router is asked to load or unload the one model,
// and the provider's catalog follows at once.
func TestLoadTooAndUnloadLeaveTheOtherModelsLoaded(t *testing.T) {
	rig := newLocalRig(t)
	calls := filepath.Join(t.TempDir(), "calls.txt")
	rig.standin = []string{"--load-ms=300", "--calls=" + calls}
	a, m := threeModels(t, rig)
	v := localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[1]}})
	pid, started, provider := v.Server.PID, v.Server.StartedAt, v.Settings.ProviderID
	if !v.Server.Router || providerModels(a, provider) != m[0]+","+m[1] {
		t.Fatalf("%+v", v.Server)
	}
	var key string
	_ = a.c.DB.Reader().QueryRow(`SELECT key_ref FROM providers WHERE id=?`, provider).Scan(&key)

	v = localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[1], m[2]}})
	if v.Server.PID != pid || !v.Server.StartedAt.Equal(started) || strings.Join(v.Server.Models, ",") != m[0]+","+m[1]+","+m[2] || v.Server.Note != "" {
		t.Fatalf("Load too started the server again: %+v", v.Server)
	}
	if strings.Join(v.Settings.Loaded, ",") != m[0]+","+m[1]+","+m[2] || providerModels(a, provider) != m[0]+","+m[1]+","+m[2] {
		t.Fatalf("%+v; the provider lists %s", v.Settings, providerModels(a, provider))
	}
	// Unload: the router drops the model (the catalog keeps a model the listing no longer has, as for every provider)
	v = localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[2]}})
	if v.Server.PID != pid || strings.Join(v.Server.Models, ",") != m[0]+","+m[2] || strings.Join(v.Settings.Loaded, ",") != m[0]+","+m[2] {
		t.Fatalf("Unload: %+v", v.Server)
	}
	if resp, err := http.Get(v.Server.BaseURL + "/models"); err != nil {
		t.Fatal(err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), m[1]) || !strings.Contains(string(body), m[2]) {
			t.Fatalf("the router lists: %s", body)
		}
	}
	var after string
	_ = a.c.DB.Reader().QueryRow(`SELECT key_ref FROM providers WHERE id=?`, provider).Scan(&after)
	if after != key {
		t.Fatal("the provider's key changed without a new start")
	}
	if got, _ := os.ReadFile(calls); string(got) != "reload *\nload "+m[2]+"\nunload "+m[1]+"\nreload *\n" {
		t.Fatalf("the router's calls:\n%s", got)
	}
	// a change of the cache type cannot be made on the running router: it starts again, without a note (that is the expected way)
	localCall(a, "local.settings.set", map[string]any{"cacheType": "q4_0"})
	v = localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[1], m[2]}})
	if v.Server.PID == pid || v.Server.CacheType != "q4_0" || v.Server.Note != "" {
		t.Fatalf("%+v", v.Server)
	}
}

// A server that does not answer the router's load and unload calls is started again on the new set, and says so, on the page
// (the server's note) and in the log (decision P120).
func TestAServerWithoutTheRouterCallsIsStartedAgainAndSaysSo(t *testing.T) {
	rig := newLocalRig(t)
	rig.standin = []string{"--load-ms=300", "--no-router-calls"}
	a, m := threeModels(t, rig)
	v := localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[1]}})
	pid := v.Server.PID
	v = localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[1], m[2]}})
	if v.Server.PID == pid || v.Server.State != "running" || strings.Join(v.Server.Models, ",") != m[0]+","+m[1]+","+m[2] {
		t.Fatalf("%+v", v.Server)
	}
	if !strings.HasPrefix(v.Server.Note, "Started again on the new set of models: ") || !strings.Contains(v.Server.Note, "/models/load") || !strings.Contains(v.Server.Note, "404") {
		t.Fatalf("the note: %q", v.Server.Note)
	}
	if providerModels(a, v.Settings.ProviderID) != m[0]+","+m[1]+","+m[2] {
		t.Fatal(providerModels(a, v.Settings.ProviderID))
	}
	// the next start that needs no change says nothing more
	localCall(a, "local.server.stop", nil)
	if v = localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[1]}}); v.Server.Note != "" {
		t.Fatalf("%q", v.Server.Note)
	}
}

// paddedWith is padded with a tensor list in the header (llamatest.GGUFWithTensors): a LABELLED FIXTURE model file.
func paddedWith(arch string, meta map[string]any, tensors []string, n int) []byte {
	b := llamatest.GGUFWithTensors(arch, meta, tensors)
	if len(b) < n {
		b = append(b, make([]byte, n-len(b))...)
	}
	return b
}

// Draft heads that are not models, on the Models page and in the server's options (decision P120): a model whose file carries
// MTP heads offers them as its draft ("mtp") and runs with --spec-type draft-mtp; an EAGLE-3 draft is offered to the model whose
// tokenizer and embedding length it has, runs with --spec-type draft-eagle3, and is never served for chat; a model without MTP
// heads cannot choose them.
func TestDraftHeadsAreOfferedAndRunWithTheirSpecType(t *testing.T) {
	rig := newLocalRig(t)
	record := filepath.Join(t.TempDir(), "args.txt")
	rig.standin = []string{"--load-ms=300", "--record=" + record}
	rig.hub.AddModel("acme/glm-GGUF", 10, nil, map[string][]byte{"glm-Q4_0.gguf": paddedWith("glm4moe",
		qwenHeader("glm4moe", map[string]any{"glm4moe.nextn_predict_layers": uint32(1), "glm4moe.embedding_length": uint32(128)}), []string{"blk.0.attn_q.weight", "blk.1.nextn.eh_proj.weight"}, 30000)})
	rig.hub.AddModel("acme/target-GGUF", 10, nil, map[string][]byte{"target-Q4_0.gguf": padded("qwen3", qwenHeader("qwen3", nil), 30000)})
	rig.hub.AddModel("acme/target-eagle3-GGUF", 10, nil, map[string][]byte{"target-eagle3-BF16.gguf": padded("eagle3",
		qwenHeader("eagle3", map[string]any{"eagle3.block_count": uint32(1), "eagle3.target_hidden_size": uint32(64)}), 4000)})
	a := localCore(t, rig)
	localCall(a, "local.install", map[string]any{"backend": "vulkan"})
	a.waitFor("the install", func() bool {
		v := localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	localCall(a, "local.settings.set", map[string]any{"port": freePort(t)})
	glm, target, eagle := "acme/glm-GGUF:Q4_0", "acme/target-GGUF:Q4_0", "acme/target-eagle3-GGUF:BF16"
	for _, r := range [][2]string{{"acme/glm-GGUF", "Q4_0"}, {"acme/target-GGUF", "Q4_0"}, {"acme/target-eagle3-GGUF", "BF16"}} {
		download(a, r[0], r[1])
	}
	v := localCall(a, "local.get", nil)
	heads := map[string][2]bool{}
	for _, x := range v.Models {
		heads[x.ID] = [2]bool{x.MTP, x.Eagle3}
	}
	if heads[glm] != [2]bool{true, false} || heads[eagle] != [2]bool{false, true} || heads[target] != [2]bool{} {
		t.Fatalf("%v", heads)
	}
	if m := modelOf(v, glm); strings.Join(m.drafts, ",") != "mtp" {
		t.Fatalf("the MTP model's drafts: %v", m.drafts)
	}
	if m := modelOf(v, target); strings.Join(m.drafts, ",") != eagle {
		t.Fatalf("the target's drafts: %v", m.drafts)
	}
	if m := modelOf(v, eagle); len(m.drafts) != 0 {
		t.Fatalf("the EAGLE-3 draft's drafts: %v", m.drafts)
	}
	if err := a.callErr("local.server.start", map[string]any{"modelId": eagle}); err == nil || !strings.Contains(err.Error(), "EAGLE-3 draft") {
		t.Fatalf("an EAGLE-3 draft served for chat: %v", err)
	}
	if err := a.callErr("local.model.set", map[string]any{"id": target, "draft": "mtp"}); err == nil || !strings.Contains(err.Error(), "no MTP heads") {
		t.Fatal(err)
	}
	localCall(a, "local.model.set", map[string]any{"id": glm, "draft": "mtp"})
	v = localCall(a, "local.model.set", map[string]any{"id": target, "draft": eagle})
	if modelOf(v, glm).draft != "mtp" || modelOf(v, target).draft != eagle || v.Settings.Drafts[glm] != "mtp" {
		t.Fatalf("%+v", v.Settings.Drafts)
	}

	v = localCall(a, "local.server.start", map[string]any{"modelId": glm})
	if args, _ := os.ReadFile(record); !strings.Contains(string(args), "--spec-type\ndraft-mtp") || strings.Contains(string(args), "--model-draft") {
		t.Fatalf("%s", args)
	}
	localCall(a, "local.server.stop", nil)
	localCall(a, "local.server.start", map[string]any{"modelId": target})
	if args, _ := os.ReadFile(record); !strings.Contains(string(args), "--spec-type\ndraft-eagle3") || !strings.Contains(string(args), "--model-draft\n") {
		t.Fatalf("%s", args)
	}
	// both on the router: each section with its own
	v = localCall(a, "local.server.start", map[string]any{"modelIds": []string{target, glm}})
	if !v.Server.Router || v.Server.State != "running" {
		t.Fatalf("%+v", v.Server)
	}
	presetAt := ""
	args, _ := os.ReadFile(record)
	lines := strings.Split(string(args), "\n")
	for i, l := range lines {
		if l == "--models-preset" && i+1 < len(lines) {
			presetAt = lines[i+1]
		}
	}
	preset, _ := os.ReadFile(presetAt)
	if !strings.Contains(string(preset), "["+glm+"]\nmodel = ") || !strings.Contains(string(preset), "spec-type = draft-mtp") || !strings.Contains(string(preset), "spec-type = draft-eagle3") {
		t.Fatalf("%s", preset)
	}
}
