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

// Draft heads that are not models (decision P120). A model that carries MTP heads names its MTP layers
// (<arch>.nextn_predict_layers) and lists the tensor llama.cpp looks for (blk.<last>.nextn.eh_proj.weight); one that names the
// layers but whose converter left the tensors out has none. A split model's heads are in a later file, found by HasTensor. An
// EAGLE-3 draft (architecture eagle3) drafts for the model whose tokenizer and embedding length it has, and for no other.
func TestTheHeaderSaysWhichModelsHaveDraftHeads(t *testing.T) {
	read := func(b []byte) localllm.GGUFInfo {
		t.Helper()
		info, err := localllm.ReadGGUF(writeGGUF(t, b))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	words := make([]string, 4096)
	for i := range words {
		words[i] = "w"
	}
	meta := func(arch string, more map[string]any) map[string]any {
		m := map[string]any{"tokenizer.ggml.model": "gpt2", "tokenizer.ggml.tokens": words, "tokenizer.ggml.bos_token_id": uint32(1), "tokenizer.ggml.eos_token_id": uint32(2),
			arch + ".block_count": uint32(48), arch + ".attention.head_count": uint32(32), arch + ".embedding_length": uint32(4096)}
		for k, v := range more {
			m[k] = v
		}
		return m
	}
	tensors := []string{"token_embd.weight", "blk.0.attn_q.weight", "blk.47.attn_q.weight", "blk.47.nextn.eh_proj.weight", "blk.47.nextn.enorm.weight", "output.weight"}
	mtp := read(llamatest.GGUFWithTensors("glm4moe", meta("glm4moe", map[string]any{"glm4moe.nextn_predict_layers": uint32(1)}), tensors))
	if !mtp.MTP || mtp.NextNLayers != 1 || localllm.MTPTensor(mtp) != "blk.47.nextn.eh_proj.weight" {
		t.Fatalf("%+v", mtp)
	}
	stripped := read(llamatest.GGUFWithTensors("glm4moe", meta("glm4moe", map[string]any{"glm4moe.nextn_predict_layers": uint32(1)}), tensors[:3]))
	if stripped.MTP || stripped.NextNLayers != 1 {
		t.Fatalf("the heads were left out: %+v", stripped)
	}
	plain := read(llamatest.GGUFWithTensors("qwen3", meta("qwen3", nil), []string{"blk.47.nextn.eh_proj.weight"}))
	if plain.MTP || localllm.MTPTensor(plain) != "" {
		t.Fatalf("no MTP layers named: %+v", plain)
	}
	// the later file of a split model has no architecture of its own; the heads are found in it by name
	shard := writeGGUF(t, llamatest.GGUFWithTensors("", map[string]any{"split.no": uint32(1), "split.count": uint32(2)}, tensors[3:]))
	if ok, err := localllm.HasTensor(shard, localllm.MTPTensor(stripped)); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, err := localllm.HasTensor(shard, "blk.0.attn_q.weight"); err != nil || ok {
		t.Fatalf("%v %v", ok, err)
	}

	target := read(llamatest.GGUF("qwen3", meta("qwen3", nil)))
	eagle := read(llamatest.GGUF("eagle3", meta("eagle3", map[string]any{"eagle3.block_count": uint32(1), "eagle3.target_hidden_size": uint32(4096)})))
	if !eagle.Eagle3() || eagle.TargetHiddenSize != 4096 || localllm.LooksLikeEmbedding(eagle) {
		t.Fatalf("%+v", eagle)
	}
	if ok, why := localllm.DraftFits(target, eagle, 8<<30, 400<<20); !ok {
		t.Fatalf("an EAGLE-3 draft of the target's tokenizer and size: %s", why)
	}
	other := read(llamatest.GGUF("qwen3", meta("qwen3", map[string]any{"qwen3.embedding_length": uint32(2560)})))
	if ok, why := localllm.DraftFits(other, eagle, 8<<30, 400<<20); ok || !strings.Contains(why, "4096 dimensions") {
		t.Fatalf("an EAGLE-3 draft for a model of other dimensions: %v %s", ok, why)
	}
	if ok, why := localllm.DraftFits(eagle, target, 8<<30, 400<<20); ok || !strings.Contains(why, "EAGLE-3") {
		t.Fatalf("drafting for an EAGLE-3 draft: %v %s", ok, why)
	}
	words[0] = "x"
	foreign := read(llamatest.GGUF("eagle3", meta("eagle3", map[string]any{"tokenizer.ggml.model": "llama"})))
	if ok, _ := localllm.DraftFits(target, foreign, 8<<30, 400<<20); ok {
		t.Fatal("an EAGLE-3 draft of another tokenizer drafts")
	}
}

// The heads run with their spec type, named on the command line (one model) and in the preset (the router): an MTP model with
// --spec-type draft-mtp and no draft file, an EAGLE-3 draft with --model-draft and --spec-type draft-eagle3. The stand-in
// refuses a draft whose spec type does not use it, and an EAGLE-3 type without a draft.
func TestDraftHeadsRunWithTheirSpecType(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	record := filepath.Join(t.TempDir(), "args.txt")
	glm := fileModel(t, "ggml-org/GLM-4.5-Air-GGUF:Q4_K_M", false)
	qwen := fileModel(t, "Qwen/Qwen3-8B-GGUF:Q4_K_M", false)
	eagle := fileModel(t, "AngelSlim/Qwen3-8B_eagle3-GGUF:BF16", false)
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--record=" + record), Model: glm, SpecTypes: map[string]string{glm.ID: localllm.SpecMTP},
		Port: freePort(t), APIKey: "k", Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(record)
	if !strings.Contains(string(args), "--spec-type\ndraft-mtp") || strings.Contains(string(args), "--model-draft") {
		t.Fatalf("%s", args)
	}
	srv.Stop()
	l.Model, l.Drafts, l.SpecTypes = qwen, map[string]localllm.Model{qwen.ID: eagle}, map[string]string{qwen.ID: localllm.SpecEagle3}
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(record)
	if !strings.Contains(string(args), "--model-draft\n"+eagle.Files[0]+"\n--spec-type\ndraft-eagle3") {
		t.Fatalf("%s", args)
	}
	srv.Stop()

	tmp := t.TempDir()
	rl := localllm.Launch{Program: os.Args[0], Args: llamatest.Args(), Models: []localllm.Model{glm, qwen}, Drafts: map[string]localllm.Model{qwen.ID: eagle},
		SpecTypes: map[string]string{glm.ID: localllm.SpecMTP, qwen.ID: localllm.SpecEagle3}, PresetFile: filepath.Join(tmp, "models.ini"), Port: freePort(t), Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), rl); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	preset, _ := os.ReadFile(rl.PresetFile)
	for _, want := range []string{"[" + glm.ID + "]\nmodel = " + glm.Files[0] + "\nspec-type = draft-mtp\n",
		"[" + qwen.ID + "]\nmodel = " + qwen.Files[0] + "\nmodel-draft = " + eagle.Files[0] + "\nspec-type = draft-eagle3\n"} {
		if !strings.Contains(string(preset), want) {
			t.Fatalf("the preset lacks %q:\n%s", want, preset)
		}
	}
	srv.Stop()
	for name, args := range map[string][]string{
		"eagle without a draft": append(llamatest.Args(), "--spec-type", "draft-eagle3"),
		"draft with mtp":        append(llamatest.Args(), "--model-draft", eagle.Files[0], "--spec-type", "draft-mtp"),
	} {
		if err := srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: args, Model: glm, Port: freePort(t), Timeout: 30 * time.Second}); err == nil {
			t.Errorf("%s: started", name)
		}
	}
}
