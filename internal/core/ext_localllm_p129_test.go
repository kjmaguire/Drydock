package core_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/localllm"
)

// localModel is a model of the view by its id (a test fails without it).
func localModel(t *testing.T, v core.LocalView, id string) localllm.Model {
	t.Helper()
	for _, m := range v.Models {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no model %s", id)
	return localllm.Model{}
}

// DFlash, DSpark and MTP head drafts on the Models page and in the server's options (decision P129): each is offered to the
// model it was made for, marked with its kind, never served for chat, and run with --model-draft and its --spec-type; what a
// model takes loaded counts the MTP context of its MTP heads, its own or a head file's.
func TestDFlashDSparkAndMTPHeadDraftsAreOfferedRunAndCounted(t *testing.T) {
	rig := newLocalRig(t)
	record := filepath.Join(t.TempDir(), "args.txt")
	rig.standin = []string{"--load-ms=300", "--record=" + record}
	nextn := map[string]any{"qwen35.nextn_predict_layers": uint32(1), "qwen35.block_count": uint32(3)}
	rig.hub.AddModel("acme/qwen3-GGUF", 10, nil, map[string][]byte{"qwen3-Q4_0.gguf": padded("qwen3", qwenHeader("qwen3", map[string]any{"qwen3.block_count": uint32(4)}), 30000)})
	rig.hub.AddModel("acme/qwen3-DFlash-GGUF", 10, nil, map[string][]byte{"qwen3-DFlash-BF16.gguf": paddedWith("dflash",
		qwenHeader("dflash", map[string]any{"dflash.target_layers": []uint32{1, 4}}), []string{"blk.0.attn_q.weight"}, 4000)})
	rig.hub.AddModel("acme/qwen3-DSpark-GGUF", 10, nil, map[string][]byte{"qwen3-DSpark-BF16.gguf": paddedWith("dflash",
		qwenHeader("dflash", map[string]any{"dflash.target_layers": []uint32{2, 4}}), []string{"blk.0.attn_q.weight", "markov_w1.weight"}, 4000)})
	// a model converted without its heads (--no-nextn), and its heads converted alone (--mtp): the model's architecture, its
	// block count with the MTP layer, the MTP tensors and none of the model's blocks
	rig.hub.AddModel("acme/qwen35-GGUF", 10, nil, map[string][]byte{"qwen35-Q4_0.gguf": paddedWith("qwen35", qwenHeader("qwen35", nil),
		[]string{"token_embd.weight", "blk.0.attn_q.weight", "blk.1.attn_q.weight"}, 30000)})
	rig.hub.AddModel("acme/qwen35-MTP-GGUF", 10, nil, map[string][]byte{"mtp-qwen35-Q8_0.gguf": paddedWith("qwen35", qwenHeader("qwen35", nextn),
		[]string{"token_embd.weight", "output.weight", "blk.2.nextn.eh_proj.weight", "blk.2.attn_q.weight"}, 4000)})
	// a model that carries its heads
	rig.hub.AddModel("acme/glm-GGUF", 10, nil, map[string][]byte{"glm-Q4_0.gguf": paddedWith("glm4moe",
		qwenHeader("glm4moe", map[string]any{"glm4moe.nextn_predict_layers": uint32(1), "glm4moe.block_count": uint32(3)}),
		[]string{"blk.0.attn_q.weight", "blk.1.attn_q.weight", "blk.2.nextn.eh_proj.weight"}, 30000)})
	a := localCore(t, rig)
	localCall(a, "local.install", map[string]any{"backend": "vulkan"})
	a.waitFor("the install", func() bool {
		v := localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	localCall(a, "local.settings.set", map[string]any{"port": freePort(t)})
	qwen, dflash, dspark := "acme/qwen3-GGUF:Q4_0", "acme/qwen3-DFlash-GGUF:BF16", "acme/qwen3-DSpark-GGUF:BF16"
	qwen35, head, glm := "acme/qwen35-GGUF:Q4_0", "acme/qwen35-MTP-GGUF:Q8_0", "acme/glm-GGUF:Q4_0"
	for _, id := range []string{qwen, dflash, dspark, qwen35, head, glm} {
		repo, variant, _ := strings.Cut(id, ":")
		download(a, repo, variant)
	}
	v := localCall(a, "local.get", nil)
	for id, kind := range map[string]string{qwen: "", dflash: "dflash", dspark: "dspark", qwen35: "", head: "mtp-head", glm: ""} {
		if m := localModel(t, v, id); m.DraftKind != kind || m.Eagle3 {
			t.Fatalf("%s: the kind %q, want %q", id, m.DraftKind, kind)
		}
	}
	if m := localModel(t, v, qwen); len(m.Drafts) != 2 || !slices.Contains(m.Drafts, dflash) || !slices.Contains(m.Drafts, dspark) {
		t.Fatalf("the Qwen3 model's drafts: %v", m.Drafts)
	}
	if m := localModel(t, v, qwen35); strings.Join(m.Drafts, ",") != head {
		t.Fatalf("the Qwen3.5 model's drafts: %v", m.Drafts)
	}
	if m := localModel(t, v, glm); strings.Join(m.Drafts, ",") != "mtp" || !m.MTP {
		t.Fatalf("the GLM model's drafts: %v", m.Drafts)
	}
	for _, id := range []string{dflash, dspark, head} {
		if m := localModel(t, v, id); len(m.Drafts) != 0 || m.MTP {
			t.Fatalf("%s drafts for itself: %+v", id, m)
		}
	}
	for id, words := range map[string]string{dflash: "is a DFlash draft", dspark: "is a DSpark draft", head: "is an MTP head"} {
		if err := a.callErr("local.server.start", map[string]any{"modelId": id}); err == nil || !strings.Contains(err.Error(), words) {
			t.Fatalf("%s served for chat: %v", id, err)
		}
	}
	if err := a.callErr("local.model.set", map[string]any{"id": qwen35, "draft": dflash}); err == nil {
		t.Fatal("a DFlash draft for a model it was not made for")
	}

	localCall(a, "local.model.set", map[string]any{"id": qwen, "draft": dspark})
	localCall(a, "local.model.set", map[string]any{"id": qwen35, "draft": head})
	v = localCall(a, "local.model.set", map[string]any{"id": glm, "draft": "mtp"})
	// what each takes loaded: its file and cache, and the draft's (a head file's cache is its MTP context's), or its own MTP
	// context's (one layer of its three, its own cache the other two)
	q35, h, g := localModel(t, v, qwen35), localModel(t, v, head), localModel(t, v, glm)
	if h.KV == nil || !h.KV.Known || q35.KV == nil || h.KV.Bytes*2 != q35.KV.Bytes {
		t.Fatalf("the head file's cache is one layer of the model's two: %+v %+v", h.KV, q35.KV)
	}
	if q35.LoadBytes != q35.Size+q35.KV.Bytes+h.Size+h.KV.Bytes || q35.MTPKVBytes != h.KV.Bytes {
		t.Fatalf("the model with its head file takes %d (MTP context %d)", q35.LoadBytes, q35.MTPKVBytes)
	}
	if g.MTPKVBytes == 0 || g.MTPKVBytes*2 != g.KV.Bytes || g.LoadBytes != g.Size+g.KV.Bytes+g.MTPKVBytes || g.Fit == nil || g.Fit.NeedBytes < g.Size+g.KV.Bytes+g.MTPKVBytes {
		t.Fatalf("the model with its own heads: %+v", g)
	}

	localCall(a, "local.server.start", map[string]any{"modelId": qwen})
	if args, _ := os.ReadFile(record); !strings.Contains(string(args), "--model-draft\n"+localModel(t, v, dspark).Files[0]+"\n--spec-type\ndraft-dspark") {
		t.Fatalf("%s", args)
	}
	localCall(a, "local.server.stop", nil)
	v = localCall(a, "local.server.start", map[string]any{"modelId": qwen35})
	if args, _ := os.ReadFile(record); !strings.Contains(string(args), "--model-draft\n"+h.Files[0]+"\n--spec-type\ndraft-mtp") {
		t.Fatalf("%s", args)
	}
	if v.Served == nil || v.Served.ModelBytes != q35.Size+h.Size || v.Served.KVBytes != q35.KV.Bytes+h.KV.Bytes {
		t.Fatalf("the served memory: %+v", v.Served)
	}
	localCall(a, "local.server.stop", nil)
	v = localCall(a, "local.server.start", map[string]any{"modelId": glm})
	if v.Served == nil || v.Served.ModelBytes != g.Size || v.Served.KVBytes != g.KV.Bytes+g.MTPKVBytes {
		t.Fatalf("the served memory with its own heads: %+v", v.Served)
	}
}

// The local provider's catalog drops a model the router no longer lists (decision P129): after an Unload, and after a start on
// fewer models, the router cannot be routed to it: a turn pinned to it finds no candidate.
func TestTheLocalCatalogDropsAModelTheServerNoLongerLists(t *testing.T) {
	rig := newLocalRig(t)
	rig.standin = []string{"--load-ms=300"}
	a, m := threeModels(t, rig)
	v := localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[1]}})
	provider := v.Settings.ProviderID
	type preview struct {
		Blocked  bool   `json:"blocked"`
		Reason   string `json:"reason"`
		Decision struct {
			Model string `json:"Model"`
		} `json:"decision"`
	}
	pinned := func(model string) preview {
		var pv preview
		a.call("route.preview", map[string]any{"request": map[string]any{"RunKind": "agent_task", "TaskType": "test_fix", "Level": "balanced",
			"Explicit": map[string]any{"Provider": provider, "Model": model}}}, &pv)
		return pv
	}
	if pv := pinned(m[1]); pv.Blocked || pv.Decision.Model != m[1] {
		t.Fatalf("a loaded model: %+v", pv)
	}
	v = localCall(a, "local.server.start", map[string]any{"modelIds": []string{m[0], m[2]}})
	if !v.Server.Router || providerModels(a, provider) != m[0]+","+m[2] {
		t.Fatalf("after the Unload the provider lists %s", providerModels(a, provider))
	}
	if pv := pinned(m[1]); !pv.Blocked || !strings.Contains(pv.Reason, "not among the known candidates") {
		t.Fatalf("an unloaded model is still routed to: %+v", pv)
	}
	if pv := pinned(m[2]); pv.Blocked || pv.Decision.Model != m[2] {
		t.Fatalf("the model loaded instead: %+v", pv)
	}
	// a start on fewer models (a server of one): only it is offered
	localCall(a, "local.server.stop", nil)
	v = localCall(a, "local.server.start", map[string]any{"modelId": m[2]})
	if v.Server.State != "running" || providerModels(a, v.Settings.ProviderID) != m[2] {
		t.Fatalf("after a start on one model the provider lists %s", providerModels(a, v.Settings.ProviderID))
	}
	if pv := pinned(m[0]); !pv.Blocked {
		t.Fatalf("a model left out of the start is still routed to: %+v", pv)
	}
}
