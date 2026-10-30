package localllm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
	"github.com/kjmaguire/drydock/internal/proc"
)

// p132Assistant is a LABELLED FIXTURE header of a Gemma 4 assistant as the converter writes one (conversion/gemma.py's
// Gemma4AssistantModel at the TurboQuant fork's b10298-2.0.0): architecture "gemma4-assistant", four blocks, every one an MTP
// layer (nextn_predict_layers is the block count), and the width of its model's hidden state as embedding_length_out.
func p132Assistant(backbone uint32) map[string]any {
	return p129Meta("gemma4-assistant", 4, map[string]any{"gemma4-assistant.nextn_predict_layers": uint32(4),
		"gemma4-assistant.embedding_length_out": backbone, "gemma4-assistant.attention.sliding_window": uint32(512)})
}

var p132AssistantTensors = []string{"token_embd.weight", "output_norm.weight", "nextn.pre_projection.weight", "nextn.post_projection.weight",
	"blk.0.attn_q.weight", "blk.3.ffn_down.weight"}

// The MTP files of the TurboQuant build's own kinds (decision P132), read from their headers as the fork names them at
// b10298-2.0.0: a Gemma 4 assistant ("gemma4-assistant", the name the official b11936 has too) only drafts and reads the width
// of its model's hidden state; the fork's older layouts, a Gemma 4 assistant of before its b10018 sync ("gemma4_assistant") and
// a standalone Qwen 3.5 NextN file ("qwen35_mtp", "qwen35moe_mtp"), are old MTP files that neither build loads.
func TestTheHeaderSaysWhichFilesAreGemma4AssistantsOrOldMTPFiles(t *testing.T) {
	read := func(b []byte) localllm.GGUFInfo {
		t.Helper()
		info, err := localllm.ReadGGUF(writeGGUF(t, b))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	a := read(llamatest.GGUFWithTensors(localllm.Gemma4Assistant, p132Assistant(5376), p132AssistantTensors))
	if a.DraftOnly() != localllm.DraftOnlyAssistant || a.EmbeddingLengthOut != 5376 || a.MTP || a.MTPHead || a.OwnMTP() || localllm.LooksLikeEmbedding(a) {
		t.Fatalf("a Gemma 4 assistant: %+v", a)
	}
	for _, arch := range []string{"gemma4_assistant", "qwen35_mtp", "qwen35moe_mtp"} {
		old := read(llamatest.GGUF(arch, p129Meta(arch, 1, map[string]any{arch + ".nextn_predict_layers": uint32(1)})))
		if old.DraftOnly() != localllm.DraftOnlyOldMTP {
			t.Fatalf("%s: %+v", arch, old)
		}
	}
	// the Gemma 4 model itself chats, and a model without the key hands on its embedding length
	if g := read(llamatest.GGUF(localllm.Gemma4, p129Meta(localllm.Gemma4, 62, nil))); g.DraftOnly() != "" || g.EmbeddingLengthOut != 0 {
		t.Fatalf("a Gemma 4 model: %+v", g)
	}
}

// Which models a Gemma 4 assistant drafts for (decision P132): a Gemma 4 model whose hidden state is as wide as the one it
// names, with its tokenizer, much larger than it (common_speculative_impl_draft_mtp refuses another width); an old MTP file
// drafts for nothing, and nothing drafts for either.
func TestAGemma4AssistantFitsItsModelAndAnOldMTPFileFitsNone(t *testing.T) {
	read := func(b []byte) localllm.GGUFInfo {
		t.Helper()
		info, err := localllm.ReadGGUF(writeGGUF(t, b))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	wide := func(width uint32) map[string]any {
		m := p129Meta(localllm.Gemma4, 62, nil)
		m["gemma4.embedding_length"] = width
		return m
	}
	gemma := read(llamatest.GGUF(localllm.Gemma4, wide(5376)))
	small := read(llamatest.GGUF(localllm.Gemma4, wide(2560)))
	qwen := read(llamatest.GGUF("qwen3", p129Meta("qwen3", 36, nil)))
	a := read(llamatest.GGUFWithTensors(localllm.Gemma4Assistant, p132Assistant(5376), p132AssistantTensors))
	if ok, why := localllm.DraftFits(gemma, a, 17<<30, 400<<20); !ok {
		t.Fatalf("a Gemma 4 assistant for its model: %s", why)
	}
	if ok, why := localllm.DraftFits(small, a, 4<<30, 400<<20); ok || !strings.Contains(why, "a Gemma 4 assistant for a model of 5376 dimensions, and the model has 2560") {
		t.Fatalf("a Gemma 4 assistant for a narrower Gemma 4: %v %s", ok, why)
	}
	if ok, why := localllm.DraftFits(qwen, a, 17<<30, 400<<20); ok || !strings.Contains(why, "a Gemma 4 assistant, and the model is qwen3") {
		t.Fatalf("a Gemma 4 assistant for a Qwen: %v %s", ok, why)
	}
	other := p132Assistant(5376)
	other["tokenizer.ggml.model"] = "llama"
	if ok, why := localllm.DraftFits(gemma, read(llamatest.GGUFWithTensors(localllm.Gemma4Assistant, other, p132AssistantTensors)), 17<<30, 400<<20); ok || why != "its tokenizer is not the model's" {
		t.Fatalf("another tokenizer: %v %s", ok, why)
	}
	if ok, why := localllm.DraftFits(a, gemma, 17<<30, 400<<20); ok || !strings.Contains(why, "a Gemma 4 assistant is drafted for by nothing") {
		t.Fatalf("drafting for an assistant: %v %s", ok, why)
	}
	old := read(llamatest.GGUF("gemma4_assistant", p129Meta("gemma4_assistant", 4, map[string]any{"gemma4_assistant.nextn_predict_layers": uint32(4)})))
	if ok, why := localllm.DraftFits(gemma, old, 17<<30, 400<<20); ok || !strings.Contains(why, "an MTP file made for an older TurboQuant build") {
		t.Fatalf("an old MTP file: %v %s", ok, why)
	}
	if ok, why := localllm.DraftFits(old, gemma, 17<<30, 400<<20); ok || !strings.Contains(why, "an old MTP file is drafted for by nothing") {
		t.Fatalf("drafting for an old MTP file: %v %s", ok, why)
	}
}

// A Gemma 4 assistant keeps no cache of its own: both builds share its model's last full and sliding layers with it
// (llama_model::create_memory for LLM_ARCH_GEMMA4_ASSISTANT at b10298-2.0.0 and b11936), so its estimate is known and nothing.
func TestAGemma4AssistantKeepsNoCacheOfItsOwn(t *testing.T) {
	a, err := localllm.ReadGGUF(writeGGUF(t, llamatest.GGUFWithTensors(localllm.Gemma4Assistant, p132Assistant(5376), p132AssistantTensors)))
	if err != nil {
		t.Fatal(err)
	}
	if kv := localllm.EstimateKV(a, 8192, "f16"); !kv.Known || kv.Bytes != 0 || kv.ContextSize != 8192 || !strings.Contains(kv.Note, "keeps none of its own") {
		t.Fatalf("%+v", kv)
	}
}

// A Gemma 4 assistant runs as the fork's sources take it (decision P132): --model-draft with the file and --spec-type draft-mtp
// (common/arg.cpp's --spec-draft-model, -md, --model-draft and --mtp-head are one flag; common/speculative.cpp maps the fork's
// own "mtp" and "nextn" to draft-mtp), on one server and in the router's preset. An old MTP file runs with nothing.
func TestAGemma4AssistantRunsWithModelDraftAndDraftMTP(t *testing.T) {
	if localllm.SpecOf(localllm.DraftOnlyAssistant) != localllm.SpecMTP || localllm.SpecOf(localllm.DraftOnlyOldMTP) != "" {
		t.Fatalf("spec types: %q %q", localllm.SpecOf(localllm.DraftOnlyAssistant), localllm.SpecOf(localllm.DraftOnlyOldMTP))
	}
	srv := &localllm.Server{PM: proc.New()}
	record := filepath.Join(t.TempDir(), "args.txt")
	gemma := fileModel(t, "google/gemma-4-31B-it-GGUF:Q4_K_M", false)
	a := fileModel(t, "google/gemma-4-31B-it-assistant-GGUF:Q8_0", false)
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--record=" + record), Model: gemma, Drafts: map[string]localllm.Model{gemma.ID: a},
		SpecTypes: map[string]string{gemma.ID: localllm.SpecOf(localllm.DraftOnlyAssistant)}, Port: freePort(t), APIKey: "k", Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(record)
	srv.Stop()
	if !strings.Contains(string(args), "--model-draft\n"+a.Files[0]+"\n--spec-type\ndraft-mtp") || strings.Contains(string(args), "--mtp-head") {
		t.Fatalf("a Gemma 4 assistant: %s", args)
	}
	qwen := fileModel(t, "Qwen/Qwen3-4B-GGUF:Q4_K_M", false)
	rl := localllm.Launch{Program: os.Args[0], Args: llamatest.Args(), Models: []localllm.Model{gemma, qwen}, Drafts: map[string]localllm.Model{gemma.ID: a},
		SpecTypes: map[string]string{gemma.ID: localllm.SpecMTP}, PresetFile: filepath.Join(t.TempDir(), "models.ini"), Port: freePort(t), Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), rl); err != nil {
		t.Fatal(err)
	}
	preset, _ := os.ReadFile(rl.PresetFile)
	srv.Stop()
	if want := "[" + gemma.ID + "]\nmodel = " + gemma.Files[0] + "\nmodel-draft = " + a.Files[0] + "\nspec-type = draft-mtp\n"; !strings.Contains(string(preset), want) {
		t.Fatalf("the preset lacks %q:\n%s", want, preset)
	}
}
