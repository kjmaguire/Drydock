package core_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// padded is a LABELLED FIXTURE model file: a GGUF header (llamatest.GGUF) and n bytes of filler after it, so its size is n.
func padded(arch string, meta map[string]any, n int) []byte {
	b := llamatest.GGUF(arch, meta)
	if len(b) < n {
		b = append(b, make([]byte, n-len(b))...)
	}
	return b
}

// qwenHeader is the header of a Qwen3-family fixture: the same tokenizer for every one (so they can draft for each other), and
// a small attention block so the KV cache is a few kilobytes at 4096 tokens.
func qwenHeader(arch string, more map[string]any) map[string]any {
	words := make([]string, 300)
	for i := range words {
		words[i] = "w"
	}
	m := map[string]any{"tokenizer.ggml.model": "gpt2", "tokenizer.ggml.tokens": words, "tokenizer.ggml.bos_token_id": uint32(1), "tokenizer.ggml.eos_token_id": uint32(2),
		arch + ".block_count": uint32(2), arch + ".context_length": uint32(4096), arch + ".embedding_length": uint32(64),
		arch + ".attention.head_count": uint32(4), arch + ".attention.head_count_kv": uint32(1)}
	for k, v := range more {
		m[k] = v
	}
	return m
}

// download fetches one variant through the RPC methods and waits for it.
func download(a *app, repo, variant string) core.LocalView {
	a.t.Helper()
	localCall(a, "local.model.download", map[string]any{"repo": repo, "variant": variant})
	var v core.LocalView
	id := repo + ":" + variant
	a.waitFor("the download of "+id, func() bool {
		v = localCall(a, "local.get", nil)
		for _, d := range v.Downloads {
			if d.Target == id {
				return d.State != "running"
			}
		}
		return false
	})
	for _, d := range v.Downloads {
		if d.Target == id && d.State != "done" {
			a.t.Fatalf("%s: %+v", id, d)
		}
	}
	return v
}

func modelOf(v core.LocalView, id string) (m struct {
	found              bool
	embedding          bool
	drafts             []string
	draft, projector   string
	loadBytes, kvBytes uint64
}) {
	for _, x := range v.Models {
		if x.ID == id {
			m.found, m.embedding, m.drafts, m.draft, m.projector, m.loadBytes = true, x.Embedding, x.Drafts, x.Draft, x.Projector, x.LoadBytes
			if x.KV != nil {
				m.kvBytes = x.KV.Bytes
			}
		}
	}
	return m
}

// Several models at once, vision, a draft model and an embedding model, through the RPC methods (decision P111): the page's
// view says which model is an embedding model (from its header), which models can draft for which, what each takes loaded and
// the room for them; the person picks a draft and keeps two models loaded, which runs the router with both (the projector and
// the draft in its preset) and makes both the provider's models; what does not fit, more than four, or an embedding model among
// them is refused; the embedding model is served on its own.
func TestSeveralModelsStayLoadedWithTheirProjectorAndDraftAndTheEmbeddingModelIsServedOnItsOwn(t *testing.T) {
	rig := newLocalRig(t)
	record := filepath.Join(t.TempDir(), "args.txt")
	rig.standin = []string{"--load-ms=300", "--record=" + record}
	rig.hub.AddModel("acme/coder-GGUF", 50, nil, map[string][]byte{"coder-Q4_K_M.gguf": padded("qwen3moe", qwenHeader("qwen3moe", nil), 40000)})
	rig.hub.AddModel("acme/coder-draft-GGUF", 40, nil, map[string][]byte{"coder-draft-Q8_0.gguf": padded("qwen3", qwenHeader("qwen3", nil), 6000)})
	rig.hub.AddModel("acme/vision-GGUF", 30, nil, map[string][]byte{"vision-Q4_K_M.gguf": padded("gemma3", map[string]any{"gemma3.block_count": uint32(2)}, 20000),
		"mmproj-vision-F16.gguf": padded("clip", nil, 2000)})
	rig.hub.AddModel("acme/embed-GGUF", 20, nil, map[string][]byte{"embed-Q8_0.gguf": padded("qwen3", qwenHeader("qwen3", map[string]any{"qwen3.pooling_type": uint32(3)}), 3000)})
	// a model whose own context makes a KV cache of about 140 GB: it fits nothing alongside another
	rig.hub.AddModel("acme/huge-GGUF", 10, nil, map[string][]byte{"huge-Q4_0.gguf": padded("llama", map[string]any{"llama.block_count": uint32(64),
		"llama.context_length": uint32(131072), "llama.embedding_length": uint32(8192), "llama.attention.head_count": uint32(64), "llama.attention.head_count_kv": uint32(64)}, 5000)})
	a := localCore(t, rig)
	localCall(a, "local.install", map[string]any{"backend": "vulkan"})
	a.waitFor("the install", func() bool {
		v := localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	port := freePort(t)
	localCall(a, "local.settings.set", map[string]any{"port": port, "embeddingPort": freePort(t)})
	var vs []struct {
		Name      string `json:"name"`
		Projector *struct {
			Path string `json:"path"`
		} `json:"projector"`
	}
	a.call("local.model.variants", map[string]any{"repo": "acme/vision-GGUF"}, &vs)
	if len(vs) != 1 || vs[0].Projector == nil || vs[0].Projector.Path != "mmproj-vision-F16.gguf" {
		t.Fatalf("the projector goes with the variant: %+v", vs)
	}
	for _, r := range [][2]string{{"acme/coder-GGUF", "Q4_K_M"}, {"acme/coder-draft-GGUF", "Q8_0"}, {"acme/vision-GGUF", "Q4_K_M"}, {"acme/embed-GGUF", "Q8_0"}, {"acme/huge-GGUF", "Q4_0"}} {
		download(a, r[0], r[1])
	}
	v := localCall(a, "local.get", nil)
	coder, draft, vision, embed := "acme/coder-GGUF:Q4_K_M", "acme/coder-draft-GGUF:Q8_0", "acme/vision-GGUF:Q4_K_M", "acme/embed-GGUF:Q8_0"
	if m := modelOf(v, embed); !m.embedding || len(m.drafts) != 0 {
		t.Fatalf("the embedding model is read as one: %+v", m)
	}
	if m := modelOf(v, coder); m.embedding || strings.Join(m.drafts, ",") != draft || m.draft != "" || m.loadBytes < 40000 {
		t.Fatalf("the coder can be drafted for by the small Qwen3, nothing else: %+v", m)
	}
	if m := modelOf(v, vision); m.projector == "" || len(m.drafts) != 0 {
		t.Fatalf("the vision model has its projector: %+v", m)
	}
	if v.Load.Max != 4 || v.Load.BudgetBytes != 15<<30 || v.Load.LoadedBytes != 0 {
		t.Fatalf("the room: %+v", v.Load)
	}
	// a draft is one that fits the model; an embedding model is never one
	if err := a.callErr("local.model.set", map[string]any{"id": coder, "draft": vision}); err == nil || !strings.Contains(err.Error(), "cannot draft") {
		t.Fatal(err)
	}
	v = localCall(a, "local.model.set", map[string]any{"id": coder, "draft": draft})
	if m := modelOf(v, coder); m.draft != draft || m.loadBytes != modelOf(v, draft).loadBytes+40000+m.kvBytes {
		t.Fatalf("the draft is counted with the model: %+v", m)
	}

	// what cannot be loaded together is refused before anything starts
	for _, c := range []struct {
		ids  []string
		want string
	}{
		{[]string{coder, embed}, "embedding model"},
		{[]string{coder, "acme/huge-GGUF:Q4_0"}, "need about"},
		{[]string{coder, vision, draft, embed, "acme/huge-GGUF:Q4_0"}, "at most 4"},
		{[]string{coder, coder}, "twice"},
	} {
		if err := a.callErr("local.server.start", map[string]any{"modelIds": c.ids}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: %v", c.ids, err)
		}
	}
	if v = localCall(a, "local.get", nil); v.Server.State != "stopped" && v.Server.State != "" {
		t.Fatalf("%+v", v.Server)
	}

	// two models: the router, both models in its preset (the coder with its draft, the vision model with its projector)
	v = localCall(a, "local.server.start", map[string]any{"modelIds": []string{coder, vision}})
	if v.Server.State != "running" || !v.Server.Router || strings.Join(v.Server.Models, ",") != coder+","+vision || strings.Join(v.Settings.Loaded, ",") != coder+","+vision || v.Settings.ModelID != coder {
		t.Fatalf("%+v %+v", v.Server, v.Settings)
	}
	if v.Load.LoadedBytes != modelOf(v, coder).loadBytes+modelOf(v, vision).loadBytes || v.Served == nil || len(v.Served.Models) != 2 || v.Served.ModelBytes != 40000+6000+22000 {
		t.Fatalf("the served models' memory: %+v %+v", v.Load, v.Served)
	}
	args, _ := os.ReadFile(record)
	presetAt := ""
	if lines := strings.Split(string(args), "\n"); len(lines) > 0 {
		for i, l := range lines {
			if l == "--models-preset" && i+1 < len(lines) {
				presetAt = lines[i+1]
			}
		}
	}
	preset, _ := os.ReadFile(presetAt)
	if !strings.Contains(string(preset), "["+coder+"]") || !strings.Contains(string(preset), "model-draft = ") || !strings.Contains(string(preset), "mmproj = ") || !strings.Contains(string(preset), "["+vision+"]") {
		t.Fatalf("the preset %s:\n%s", presetAt, preset)
	}
	var models []providers.Model
	a.call("provider.models", map[string]any{"id": v.Settings.ProviderID}, &models)
	if len(models) != 2 || models[0].ID != coder || models[1].ID != vision || !models[0].SupportsTools || models[0].ContextLength != 4096 {
		t.Fatalf("the provider lists each loaded model: %+v", models)
	}
	// a model the server keeps, or the draft of one, cannot be removed
	for _, id := range []string{vision, draft} {
		if err := a.callErr("local.model.remove", map[string]any{"id": id}); err == nil || !strings.Contains(err.Error(), "stop the server") {
			t.Fatalf("%s: %v", id, err)
		}
	}
	// marking a served model as an embedding model is refused
	if err := a.callErr("local.model.set", map[string]any{"id": vision, "embedding": true}); err == nil {
		t.Fatal("a served chat model was marked as an embedding model")
	}

	// the embedding model, on its own server; a chat model is not served there
	if err := a.callErr("local.embeddings.start", map[string]any{"modelId": coder}); err == nil || !strings.Contains(err.Error(), "not an embedding model") {
		t.Fatal(err)
	}
	v = localCall(a, "local.embeddings.start", map[string]any{"modelId": embed})
	if v.Embed.State != "running" || !v.Embed.Embeddings || v.Embed.ModelID != embed || v.Embed.Port != v.Settings.EmbeddingPort || v.Settings.EmbeddingModel != embed || v.Server.State != "running" {
		t.Fatalf("%+v %+v", v.Embed, v.Settings)
	}
	if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", v.Settings.EmbeddingPort)); err != nil || resp.StatusCode != 200 {
		t.Fatalf("the embedding server: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	if err := a.callErr("local.model.remove", map[string]any{"id": embed}); err == nil || !strings.Contains(err.Error(), "embeddings") {
		t.Fatal(err)
	}
	v = localCall(a, "local.embeddings.stop", nil)
	if v.Embed.State != "stopped" {
		t.Fatalf("%+v", v.Embed)
	}

	// one model again: the single-model command line, the provider's one model
	v = localCall(a, "local.server.start", map[string]any{"modelId": vision})
	if v.Server.Router || strings.Join(v.Settings.Loaded, ",") != vision {
		t.Fatalf("%+v %+v", v.Server, v.Settings)
	}
	if args, _ := os.ReadFile(record); !strings.Contains(string(args), "--mmproj\n") || strings.Contains(string(args), "--models-preset") {
		t.Fatalf("%s", args)
	}
	localCall(a, "local.server.stop", nil)
	// a removed model leaves the settings: the draft goes with it
	v = localCall(a, "local.model.remove", map[string]any{"id": draft})
	if m := modelOf(v, coder); m.draft != "" || len(v.Settings.Drafts) != 0 {
		t.Fatalf("%+v %+v", m, v.Settings)
	}
}

// A gated model (decision P111): listed and marked; without a token the download is refused at once with the page to accept its
// licence on; the token is kept in the credential store (never in the settings), shown masked and sent with the download; it
// can be removed.
func TestAGatedModelDownloadsWithTheHuggingFaceToken(t *testing.T) {
	rig := newLocalRig(t)
	const tok = "hf_dRydOcKtEsTtOkEn0123456789"
	rig.hub.AddGated("meta-llama/Llama-3.2-1B-Instruct-GGUF", tok, map[string][]byte{"Llama-3.2-1B-Instruct-Q4_K_M.gguf": make([]byte, 4000)})
	a := localCore(t, rig)
	var repos []struct {
		ID    string `json:"id"`
		Gated string `json:"gated"`
	}
	a.call("local.models.search", map[string]any{"query": "llama-3.2"}, &repos)
	if len(repos) != 1 || repos[0].Gated != "auto" {
		t.Fatalf("%+v", repos)
	}
	err := a.callErr("local.model.download", map[string]any{"repo": "meta-llama/Llama-3.2-1B-Instruct-GGUF", "variant": "Q4_K_M"})
	if err == nil || !strings.Contains(err.Error(), "gated model") || !strings.Contains(err.Error(), "https://huggingface.co/meta-llama/Llama-3.2-1B-Instruct-GGUF") {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not-a-token", "hf_short", "hf_with space in it"} {
		if err := a.callErr("local.hfToken.set", map[string]any{"token": bad}); err == nil || !strings.Contains(err.Error(), "starts with hf_") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	v := localCall(a, "local.hfToken.set", map[string]any{"token": "  " + tok + "\n"})
	if !v.HFToken.Set || v.HFToken.Masked != "hf_…6789" {
		t.Fatalf("%+v", v.HFToken)
	}
	// the token is in no answer, no setting and no event
	var raw map[string]any
	a.call("local.get", nil, &raw)
	if s := fmt.Sprint(raw); strings.Contains(s, tok) {
		t.Fatal("the view carries the token")
	}
	var setting string
	_ = a.c.DB.Reader().QueryRow(`SELECT value FROM settings WHERE key='local:settings'`).Scan(&setting)
	if strings.Contains(setting, tok) {
		t.Fatal("the settings hold the token")
	}
	var events int
	if err := a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM events WHERE CAST(data AS TEXT) LIKE ?`, "%"+tok+"%").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatal("an event holds the token")
	}
	v = download(a, "meta-llama/Llama-3.2-1B-Instruct-GGUF", "Q4_K_M")
	if len(v.Models) != 1 || v.Models[0].ID != "meta-llama/Llama-3.2-1B-Instruct-GGUF:Q4_K_M" {
		t.Fatalf("%+v", v.Models)
	}
	if seen := rig.hub.AuthSeen("/meta-llama/Llama-3.2-1B-Instruct-GGUF/resolve/main/Llama-3.2-1B-Instruct-Q4_K_M.gguf"); len(seen) != 1 || seen[0] != "Bearer "+tok {
		t.Fatalf("the download was sent %q", seen)
	}
	for _, seen := range rig.hub.AuthSeen("/api/models") {
		if seen != "" {
			t.Fatal("a listing was sent the token")
		}
	}
	// a token the account has no access with: the job says so, with the page
	localCall(a, "local.model.remove", map[string]any{"id": "meta-llama/Llama-3.2-1B-Instruct-GGUF:Q4_K_M"})
	localCall(a, "local.hfToken.set", map[string]any{"token": "hf_anotheraccount0000"})
	localCall(a, "local.model.download", map[string]any{"repo": "meta-llama/Llama-3.2-1B-Instruct-GGUF", "variant": "Q4_K_M"})
	a.waitFor("the refused download", func() bool {
		v = localCall(a, "local.get", nil)
		return len(v.Downloads) == 1 && v.Downloads[0].State != "running"
	})
	if d := v.Downloads[0]; d.State != "failed" || !strings.Contains(d.Error, "no access") || d.Licence != "https://huggingface.co/meta-llama/Llama-3.2-1B-Instruct-GGUF" {
		t.Fatalf("%+v", d)
	}
	v = localCall(a, "local.hfToken.remove", nil)
	if v.HFToken.Set || v.HFToken.Masked != "" {
		t.Fatalf("%+v", v.HFToken)
	}
}
