package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The TurboQuant build's own MTP files on the Models page and in the server's options (decision P132): a Gemma 4 assistant is
// offered to the Gemma 4 model it was made for (its hidden state as wide as the one the assistant names), marked with its kind,
// never served for chat, takes no cache of its own and runs with --model-draft and --spec-type draft-mtp; an old MTP file (the
// fork's layouts of before its b10018 sync) is marked, offered to nothing and refused for chat with its own reason.
func TestAGemma4AssistantIsOfferedAndRunAndAnOldMTPFileIsNot(t *testing.T) {
	rig := newLocalRig(t)
	record := filepath.Join(t.TempDir(), "args.txt")
	rig.standin = []string{"--load-ms=300", "--record=" + record}
	rig.hub.AddModel("acme/gemma4-GGUF", 10, nil, map[string][]byte{"gemma4-Q4_0.gguf": padded("gemma4", qwenHeader("gemma4", nil), 30000)})
	rig.hub.AddModel("acme/gemma4-assistant-GGUF", 10, nil, map[string][]byte{"gemma4-assistant-Q8_0.gguf": paddedWith("gemma4-assistant",
		qwenHeader("gemma4-assistant", map[string]any{"gemma4-assistant.nextn_predict_layers": uint32(2), "gemma4-assistant.embedding_length_out": uint32(64)}),
		[]string{"token_embd.weight", "nextn.pre_projection.weight", "blk.0.attn_q.weight"}, 4000)})
	rig.hub.AddModel("acme/gemma4-assistant-old-GGUF", 10, nil, map[string][]byte{"gemma4-assistant-old-Q8_0.gguf": padded("gemma4_assistant",
		qwenHeader("gemma4_assistant", map[string]any{"gemma4_assistant.nextn_predict_layers": uint32(2)}), 4000)})
	rig.hub.AddModel("acme/qwen3-GGUF", 10, nil, map[string][]byte{"qwen3-Q4_0.gguf": padded("qwen3", qwenHeader("qwen3", nil), 30000)})
	a := localCore(t, rig)
	localCall(a, "local.install", map[string]any{"backend": "vulkan"})
	a.waitFor("the install", func() bool {
		v := localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	localCall(a, "local.settings.set", map[string]any{"port": freePort(t)})
	gemma, assistant, old, qwen := "acme/gemma4-GGUF:Q4_0", "acme/gemma4-assistant-GGUF:Q8_0", "acme/gemma4-assistant-old-GGUF:Q8_0", "acme/qwen3-GGUF:Q4_0"
	for _, id := range []string{gemma, assistant, old, qwen} {
		repo, variant, _ := strings.Cut(id, ":")
		download(a, repo, variant)
	}
	v := localCall(a, "local.get", nil)
	for id, kind := range map[string]string{gemma: "", assistant: "assistant", old: "old-mtp", qwen: ""} {
		if m := localModel(t, v, id); m.DraftKind != kind {
			t.Fatalf("%s: the kind %q, want %q", id, m.DraftKind, kind)
		}
	}
	if m := localModel(t, v, gemma); strings.Join(m.Drafts, ",") != assistant {
		t.Fatalf("the Gemma 4 model's drafts: %v", m.Drafts)
	}
	if m := localModel(t, v, qwen); len(m.Drafts) != 0 {
		t.Fatalf("the Qwen model's drafts: %v", m.Drafts)
	}
	if err := a.callErr("local.server.start", map[string]any{"modelId": assistant}); err == nil || !strings.Contains(err.Error(), "is a Gemma 4 assistant") {
		t.Fatalf("an assistant served for chat: %v", err)
	}
	if err := a.callErr("local.server.start", map[string]any{"modelId": old}); err == nil || !strings.Contains(err.Error(), "no engine Drydock installs loads it") {
		t.Fatalf("an old MTP file served for chat: %v", err)
	}
	if err := a.callErr("local.model.set", map[string]any{"id": gemma, "draft": old}); err == nil {
		t.Fatal("an old MTP file chosen as a draft")
	}

	v = localCall(a, "local.model.set", map[string]any{"id": gemma, "draft": assistant})
	g, d := localModel(t, v, gemma), localModel(t, v, assistant)
	if d.KV == nil || !d.KV.Known || d.KV.Bytes != 0 || g.KV == nil || g.LoadBytes != g.Size+g.KV.Bytes+d.Size {
		t.Fatalf("the model with its assistant takes %d: %+v %+v", g.LoadBytes, g.KV, d.KV)
	}
	localCall(a, "local.server.start", map[string]any{"modelId": gemma})
	if args, _ := os.ReadFile(record); !strings.Contains(string(args), "--model-draft\n"+d.Files[0]+"\n--spec-type\ndraft-mtp") {
		t.Fatalf("%s", args)
	}
	localCall(a, "local.server.stop", nil)
}
