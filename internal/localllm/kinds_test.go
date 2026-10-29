package localllm_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
)

// tokenized is a LABELLED FIXTURE: a header with an architecture, a tokenizer of vocab tokens and its end tokens.
func tokenized(arch, tok string, vocab int, bos, eos uint32, more map[string]any) []byte {
	words := make([]string, vocab)
	for i := range words {
		words[i] = "w"
	}
	meta := map[string]any{"tokenizer.ggml.model": tok, "tokenizer.ggml.tokens": words, "tokenizer.ggml.bos_token_id": bos, "tokenizer.ggml.eos_token_id": eos,
		arch + ".block_count": uint32(4), arch + ".attention.head_count": uint32(8), arch + ".embedding_length": uint32(512)}
	for k, v := range more {
		meta[k] = v
	}
	return llamatest.GGUF(arch, meta)
}

// The header says which models make embeddings (a pooling type, or an encoder architecture), and which smaller model can draft
// for a larger one: the same family, the same tokenizer, at most half the size (decision P111).
func TestTheHeaderSaysWhatAModelIsForAndWhichModelsCanDraft(t *testing.T) {
	read := func(b []byte) localllm.GGUFInfo {
		t.Helper()
		info, err := localllm.ReadGGUF(writeGGUF(t, b))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	big := read(tokenized("qwen3moe", "gpt2", 151936, 151643, 151645, nil))
	if big.Tokenizer != "gpt2" || big.VocabSize != 151936 || big.BOS != 151643 || big.EOS != 151645 || big.Pooling || localllm.LooksLikeEmbedding(big) {
		t.Fatalf("%+v", big)
	}
	small := read(tokenized("qwen3", "gpt2", 151936, 151643, 151645, nil))
	if ok, why := localllm.DraftFits(big, small, 18<<30, 1<<30); !ok {
		t.Fatalf("a dense Qwen3 drafts for a Qwen3 MoE: %s", why)
	}
	cases := map[string]struct {
		draft     localllm.GGUFInfo
		size      uint64
		wantWords string
	}{
		"bigger":         {small, 10 << 30, "not much smaller"},
		"other family":   {read(tokenized("llama", "gpt2", 151936, 151643, 151645, nil)), 1 << 30, "same family"},
		"other vocab":    {read(tokenized("qwen3", "gpt2", 151000, 151643, 151645, nil)), 1 << 30, "vocabulary has 151000"},
		"other end":      {read(tokenized("qwen3", "gpt2", 151936, 1, 151645, nil)), 1 << 30, "first and last tokens"},
		"other kind":     {read(tokenized("qwen3", "llama", 151936, 151643, 151645, nil)), 1 << 30, "tokenizer is not"},
		"embedding":      {read(tokenized("qwen3", "gpt2", 151936, 151643, 151645, map[string]any{"qwen3.pooling_type": uint32(3)})), 1 << 30, "embedding model"},
		"no tokenizer":   {read(llamatest.GGUF("qwen3", map[string]any{"qwen3.block_count": uint32(4)})), 1 << 30, "tokenizer is not"},
		"vocab in slack": {read(tokenized("qwen3", "gpt2", 151936+128, 151643, 151645, nil)), 1 << 30, ""},
	}
	for name, c := range cases {
		ok, why := localllm.DraftFits(big, c.draft, 18<<30, c.size)
		if c.wantWords == "" {
			if !ok {
				t.Errorf("%s: refused: %s", name, why)
			}
			continue
		}
		if ok || !strings.Contains(why, c.wantWords) {
			t.Errorf("%s: %v %q", name, ok, why)
		}
	}
	emb := read(tokenized("qwen3", "gpt2", 151669, 151643, 151643, map[string]any{"qwen3.pooling_type": uint32(3)}))
	if !emb.Pooling || !localllm.LooksLikeEmbedding(emb) {
		t.Fatalf("a model with a pooling type makes embeddings: %+v", emb)
	}
	if bert := read(llamatest.GGUF("nomic-bert", map[string]any{"nomic-bert.block_count": uint32(12)})); !localllm.LooksLikeEmbedding(bert) {
		t.Fatalf("an encoder makes embeddings: %+v", bert)
	}
	// another architecture's pooling type is not this model's
	if other := read(tokenized("qwen3", "gpt2", 10, 1, 2, map[string]any{"llama.pooling_type": uint32(1)})); other.Pooling {
		t.Fatalf("%+v", other)
	}
}
