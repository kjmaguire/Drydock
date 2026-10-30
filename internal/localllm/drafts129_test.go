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

// p129Meta is a LABELLED FIXTURE header: a gpt2 tokenizer of 4,096 tokens, and the attention metadata of a model of the given
// layers (32 heads, 8 keeping keys and values, embedding 4096, so keys and values of 128).
func p129Meta(arch string, layers uint32, more map[string]any) map[string]any {
	words := make([]string, 4096)
	for i := range words {
		words[i] = "w"
	}
	m := map[string]any{"tokenizer.ggml.model": "gpt2", "tokenizer.ggml.tokens": words, "tokenizer.ggml.bos_token_id": uint32(1),
		"tokenizer.ggml.eos_token_id": uint32(2), arch + ".block_count": layers, arch + ".embedding_length": uint32(4096),
		arch + ".attention.head_count": uint32(32), arch + ".attention.head_count_kv": uint32(8)}
	for k, v := range more {
		m[k] = v
	}
	return m
}

// The draft files decision P129 added, read from their headers as both builds name them. A DFlash draft is of architecture
// "dflash" and names the layers of its model it reads (dflash.target_layers); one with the Markov head (markov_w1.weight) is
// a DSpark draft. An MTP head shipped as a file of its own (convert_hf_to_gguf.py --mtp) is the model's architecture with the
// MTP layers counted in its block count, the MTP tensors and none of the model's own blocks; a model that carries its heads
// lists both, in either order.
func TestTheHeaderSaysWhichFilesAreDFlashDSparkOrMTPHeadDrafts(t *testing.T) {
	read := func(b []byte) localllm.GGUFInfo {
		t.Helper()
		info, err := localllm.ReadGGUF(writeGGUF(t, b))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	dflash := read(llamatest.GGUFWithTensors("dflash", p129Meta("dflash", 5, map[string]any{"dflash.target_layers": []uint32{2, 18, 35}, "dflash.block_size": uint32(16)}),
		[]string{"blk.0.attn_q.weight", "fc.weight"}))
	if dflash.DraftOnly() != localllm.DraftOnlyDFlash || dflash.TargetLayer != 35 || dflash.DSpark || localllm.LooksLikeEmbedding(dflash) {
		t.Fatalf("DFlash: %+v", dflash)
	}
	dspark := read(llamatest.GGUFWithTensors("dflash", p129Meta("dflash", 5, map[string]any{"dflash.target_layers": []uint32{2, 18}}),
		[]string{"blk.0.attn_q.weight", "markov_w1.weight", "markov_w2.weight"}))
	if dspark.DraftOnly() != localllm.DraftOnlyDSpark || !dspark.DSpark || dspark.TargetLayer != 18 {
		t.Fatalf("DSpark: %+v", dspark)
	}
	nextn := map[string]any{"qwen35.nextn_predict_layers": uint32(1)}
	head := read(llamatest.GGUFWithTensors("qwen35", p129Meta("qwen35", 49, nextn),
		[]string{"token_embd.weight", "output_norm.weight", "output.weight", "blk.48.nextn.eh_proj.weight", "blk.48.attn_q.weight"}))
	if !head.MTP || !head.MTPHead || head.OwnMTP() || head.DraftOnly() != localllm.DraftOnlyMTPHead {
		t.Fatalf("an MTP head file: %+v", head)
	}
	for name, tensors := range map[string][]string{
		"blocks first": {"token_embd.weight", "blk.0.attn_q.weight", "blk.47.attn_q.weight", "blk.48.nextn.eh_proj.weight"},
		"heads first":  {"blk.48.nextn.eh_proj.weight", "token_embd.weight", "blk.0.attn_q.weight"},
	} {
		own := read(llamatest.GGUFWithTensors("qwen35", p129Meta("qwen35", 49, nextn), tensors))
		if !own.MTP || own.MTPHead || !own.OwnMTP() || own.DraftOnly() != "" {
			t.Fatalf("%s: a model with its own heads: %+v", name, own)
		}
	}
	// a file of heads alone that names no MTP layers is not one (llama.cpp would not find the heads either)
	if plain := read(llamatest.GGUFWithTensors("qwen35", p129Meta("qwen35", 49, nil), []string{"blk.48.nextn.eh_proj.weight"})); plain.MTP || plain.MTPHead {
		t.Fatalf("%+v", plain)
	}
}

// Which models a DFlash, DSpark or MTP head draft drafts for (decision P129): a DFlash or DSpark draft is of no family (it
// inherits its model's tokenizer) and fits a model with the layers it reads; an MTP head file fits a model of its own
// architecture, embedding length and layers (a model converted without its heads, or with them); nothing drafts for a draft.
func TestDFlashAndMTPHeadDraftsFitTheirModel(t *testing.T) {
	read := func(b []byte) localllm.GGUFInfo {
		t.Helper()
		info, err := localllm.ReadGGUF(writeGGUF(t, b))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	target := read(llamatest.GGUF("qwen3", p129Meta("qwen3", 36, nil)))
	shallow := read(llamatest.GGUF("qwen3", p129Meta("qwen3", 24, nil)))
	dflash := read(llamatest.GGUFWithTensors("dflash", p129Meta("dflash", 5, map[string]any{"dflash.target_layers": []uint32{2, 18, 35}}), []string{"blk.0.attn_q.weight"}))
	if ok, why := localllm.DraftFits(target, dflash, 8<<30, 900<<20); !ok {
		t.Fatalf("a DFlash draft for its model: %s", why)
	}
	if ok, why := localllm.DraftFits(shallow, dflash, 8<<30, 900<<20); ok || !strings.Contains(why, "reads layer 35") {
		t.Fatalf("a DFlash draft for a model without the layers it reads: %v %s", ok, why)
	}
	if ok, why := localllm.DraftFits(dflash, target, 8<<30, 900<<20); ok || !strings.Contains(why, "a DFlash draft is drafted for by nothing") {
		t.Fatalf("drafting for a DFlash draft: %v %s", ok, why)
	}

	nextn := map[string]any{"qwen35.nextn_predict_layers": uint32(1)}
	head := read(llamatest.GGUFWithTensors("qwen35", p129Meta("qwen35", 49, nextn), []string{"token_embd.weight", "blk.48.nextn.eh_proj.weight"}))
	without := read(llamatest.GGUF("qwen35", p129Meta("qwen35", 48, nil)))                                            // converted with --no-nextn
	with := read(llamatest.GGUFWithTensors("qwen35", p129Meta("qwen35", 49, nextn), []string{"blk.0.attn_q.weight"})) // names its layers
	for name, m := range map[string]localllm.GGUFInfo{"without its heads": without, "with its layers named": with} {
		if ok, why := localllm.DraftFits(m, head, 17<<30, 2<<30); !ok {
			t.Fatalf("an MTP head for a model %s: %s", name, why)
		}
	}
	other := read(llamatest.GGUF("qwen35", p129Meta("qwen35", 64, nil)))
	if ok, why := localllm.DraftFits(other, head, 17<<30, 2<<30); ok || !strings.Contains(why, "MTP head for a qwen35 model of 48 layers") {
		t.Fatalf("an MTP head for a model of other layers: %v %s", ok, why)
	}
	if ok, _ := localllm.DraftFits(target, head, 17<<30, 2<<30); ok {
		t.Fatal("an MTP head for a model of another architecture")
	}
	if ok, why := localllm.DraftFits(head, without, 17<<30, 2<<30); ok || !strings.Contains(why, "an MTP head is drafted for by nothing") {
		t.Fatalf("%v %s", ok, why)
	}
}

// The MTP context's memory (decision P129): llama.cpp keeps a model's MTP layers out of its own cache and gives them to the MTP
// draft context, as long as the model's and of the same cache type, so the model's estimate counts its own layers, the MTP
// context's the MTP layers, and together they are every layer. A file of heads alone takes the MTP context's only.
func TestTheMTPContextIsEstimatedApartFromTheModelsCache(t *testing.T) {
	nextn := map[string]any{"qwen35.nextn_predict_layers": uint32(1)}
	own, err := localllm.ReadGGUF(writeGGUF(t, llamatest.GGUFWithTensors("qwen35", p129Meta("qwen35", 49, nextn), []string{"blk.0.attn_q.weight", "blk.48.nextn.eh_proj.weight"})))
	if err != nil {
		t.Fatal(err)
	}
	const perLayer = 8 * (128 + 128) * 8192 * 2 // key-value heads x (key + value) x tokens x f16
	main, mtp := localllm.EstimateKV(own, 8192, "f16"), localllm.EstimateMTPKV(own, 8192, "f16")
	if !main.Known || main.Bytes != 48*perLayer || !mtp.Known || mtp.Bytes != perLayer || mtp.ContextSize != 8192 || mtp.CacheType != "f16" {
		t.Fatalf("the model's %+v, the MTP context's %+v", main, mtp)
	}
	head, err := localllm.ReadGGUF(writeGGUF(t, llamatest.GGUFWithTensors("qwen35", p129Meta("qwen35", 49, nextn), []string{"token_embd.weight", "blk.48.nextn.eh_proj.weight"})))
	if err != nil {
		t.Fatal(err)
	}
	if kv := localllm.EstimateKV(head, 8192, "f16"); !kv.Known || kv.Bytes != perLayer {
		t.Fatalf("a head file: %+v", kv)
	}
	plain, _ := localllm.ReadGGUF(writeGGUF(t, llamatest.GGUF("qwen3", p129Meta("qwen3", 36, nil))))
	if kv := localllm.EstimateKV(plain, 8192, "f16"); kv.Bytes != 36*perLayer {
		t.Fatalf("a model without MTP layers: %+v", kv)
	}
	if kv := localllm.EstimateMTPKV(plain, 8192, "f16"); kv.Known || !strings.Contains(kv.Note, "no MTP layers") {
		t.Fatalf("%+v", kv)
	}
}

// The new drafts run with the flags both builds document (docs/speculative.md, common/arg.cpp at b11936 and b10298-2.0.0): a
// DFlash or DSpark draft with --model-draft and --spec-type draft-dflash or draft-dspark, an MTP head file with --model-draft
// and --spec-type draft-mtp (the TurboQuant build's --mtp-head is another name for --model-draft, which the official build alone
// has); --spec-draft-n-max and -fa are left to the builds' defaults. On the router, the same in each model's section.
func TestDFlashDSparkAndMTPHeadDraftsRunWithTheirSpecType(t *testing.T) {
	srv := &localllm.Server{PM: proc.New()}
	record := filepath.Join(t.TempDir(), "args.txt")
	qwen := fileModel(t, "Qwen/Qwen3-4B-GGUF:Q4_K_M", false)
	qwen35 := fileModel(t, "Qwen/Qwen3.5-27B-GGUF:Q4_K_M", false)
	dflash := fileModel(t, "z-lab/Qwen3-4B-DFlash-GGUF:BF16", false)
	dspark := fileModel(t, "acme/Qwen3-4B-DSpark-GGUF:BF16", false)
	head := fileModel(t, "acme/Qwen3.5-27B-MTP-GGUF:Q8_0", false)
	for _, c := range []struct {
		draft localllm.Model
		spec  string
	}{{dflash, localllm.SpecDFlash}, {dspark, localllm.SpecDSpark}} {
		l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--record=" + record), Model: qwen, Drafts: map[string]localllm.Model{qwen.ID: c.draft},
			SpecTypes: map[string]string{qwen.ID: c.spec}, Port: freePort(t), APIKey: "k", Timeout: 30 * time.Second}
		if err := srv.Start(context.Background(), l); err != nil {
			t.Fatal(err)
		}
		args, _ := os.ReadFile(record)
		if !strings.Contains(string(args), "--model-draft\n"+c.draft.Files[0]+"\n--spec-type\n"+c.spec) || strings.Contains(string(args), "--spec-draft-n-max") ||
			strings.Contains(string(args), "\n-fa\n") {
			t.Fatalf("%s: %s", c.spec, args)
		}
		srv.Stop()
	}
	l := localllm.Launch{Program: os.Args[0], Args: llamatest.Args("--record=" + record), Model: qwen35, Drafts: map[string]localllm.Model{qwen35.ID: head},
		SpecTypes: map[string]string{qwen35.ID: localllm.SpecOf(localllm.DraftOnlyMTPHead)}, Port: freePort(t), APIKey: "k", Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if args, _ := os.ReadFile(record); !strings.Contains(string(args), "--model-draft\n"+head.Files[0]+"\n--spec-type\ndraft-mtp") {
		t.Fatalf("an MTP head file: %s", args)
	}
	srv.Stop()

	rl := localllm.Launch{Program: os.Args[0], Args: llamatest.Args(), Models: []localllm.Model{qwen, qwen35},
		Drafts:    map[string]localllm.Model{qwen.ID: dspark, qwen35.ID: head},
		SpecTypes: map[string]string{qwen.ID: localllm.SpecDSpark, qwen35.ID: localllm.SpecMTP}, PresetFile: filepath.Join(t.TempDir(), "models.ini"), Port: freePort(t), Timeout: 30 * time.Second}
	if err := srv.Start(context.Background(), rl); err != nil {
		t.Fatal(err)
	}
	preset, _ := os.ReadFile(rl.PresetFile)
	srv.Stop()
	for _, want := range []string{"[" + qwen.ID + "]\nmodel = " + qwen.Files[0] + "\nmodel-draft = " + dspark.Files[0] + "\nspec-type = draft-dspark\n",
		"[" + qwen35.ID + "]\nmodel = " + qwen35.Files[0] + "\nmodel-draft = " + head.Files[0] + "\nspec-type = draft-mtp\n"} {
		if !strings.Contains(string(preset), want) {
			t.Fatalf("the preset lacks %q:\n%s", want, preset)
		}
	}
	for _, kind := range []string{localllm.DraftOnlyEagle3, localllm.DraftOnlyDFlash, localllm.DraftOnlyDSpark, localllm.DraftOnlyMTPHead} {
		if localllm.SpecOf(kind) == "" {
			t.Errorf("%s runs with no spec type", kind)
		}
	}
	if err := srv.Start(context.Background(), localllm.Launch{Program: os.Args[0], Args: append(llamatest.Args(), "--spec-type", "draft-dflash"), Model: qwen,
		Port: freePort(t), Timeout: 30 * time.Second}); err == nil {
		srv.Stop()
		t.Error("draft-dflash without a draft started")
	}
}
