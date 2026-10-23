package localllm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/localllm"
	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
)

func writeGGUF(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// qwenLike is a LABELLED FIXTURE: the attention metadata of a small grouped-query model (28 layers, 12 heads, 2 of them keeping
// keys and values, embedding 1536, so keys and values of 128), with a tokenizer vocabulary before it the reader must skip.
func qwenLike() []byte {
	vocab := make([]string, 5000)
	for i := range vocab {
		vocab[i] = strings.Repeat("t", i%7+1)
	}
	return llamatest.GGUF("qwen2", map[string]any{
		"tokenizer.ggml.tokens": vocab, "tokenizer.ggml.token_type": make([]int32, 5000), "general.name": "Qwen-ish 1.5B",
		"qwen2.block_count": uint32(28), "qwen2.context_length": uint32(32768), "qwen2.embedding_length": uint32(1536),
		"qwen2.attention.head_count": uint32(12), "qwen2.attention.head_count_kv": uint32(2), "qwen2.rope.freq_base": float32(1e6),
	})
}

// The KV cache estimate (decision P97) reads the header of a GGUF file written by the test and sizes the cache: per layer the
// key-value heads times the key and value lengths, times the tokens, times the bytes per value of the cache type.
func TestTheKVCacheIsEstimatedFromTheGGUFHeader(t *testing.T) {
	info, err := localllm.ReadGGUF(writeGGUF(t, qwenLike()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Architecture != "qwen2" || info.BlockCount != 28 || info.ContextLength != 32768 || info.EmbeddingLength != 1536 || len(info.HeadCount) != 1 || info.HeadCountKV[0] != 2 {
		t.Fatalf("%+v", info)
	}
	// 28 layers x 2 heads x (128 + 128) = 14336 values a token
	const perToken = 28 * 2 * 256
	for _, c := range []struct {
		cache string
		want  uint64
	}{{"f16", perToken * 8192 * 2}, {"q8_0", perToken * 8192 * 34 / 32}, {"q4_0", perToken * 8192 * 18 / 32}} {
		kv := localllm.EstimateKV(info, 8192, c.cache)
		if !kv.Known || kv.Bytes != c.want || kv.ContextSize != 8192 || kv.CacheType != c.cache || kv.Note != "" {
			t.Fatalf("%s: %+v, want %d", c.cache, kv, c.want)
		}
	}
	// context 0: the model's own, and the note says so
	if kv := localllm.EstimateKV(info, 0, "f16"); !kv.Known || kv.Bytes != perToken*32768*2 || kv.ContextSize != 32768 || !strings.Contains(kv.Note, "own context of 32768") {
		t.Fatalf("%+v", kv)
	}
	// the TurboQuant build's types are not documented: unknown, with the reason
	if kv := localllm.EstimateKV(info, 8192, "turbo4"); kv.Known || kv.Bytes != 0 || !strings.Contains(kv.Note, "does not say") {
		t.Fatalf("%+v", kv)
	}

	// key and value lengths given, heads per layer (a layer without attention keeps nothing), a sliding window: an upper bound
	info, err = localllm.ReadGGUF(writeGGUF(t, llamatest.GGUF("gemma3", map[string]any{
		"gemma3.block_count": uint32(4), "gemma3.context_length": uint32(131072), "gemma3.embedding_length": uint32(2560),
		"gemma3.attention.head_count": uint32(8), "gemma3.attention.head_count_kv": []uint32{4, 4, 0, 4},
		"gemma3.attention.key_length": uint32(256), "gemma3.attention.value_length": uint32(256), "gemma3.attention.sliding_window": uint32(1024),
	})))
	if err != nil {
		t.Fatal(err)
	}
	if kv := localllm.EstimateKV(info, 4096, "f16"); !kv.Known || kv.Bytes != 3*4*512*4096*2 || !strings.Contains(kv.Note, "upper bound") {
		t.Fatalf("%+v", kv)
	}
	// a recurrent model's header has no attention heads: unknown
	info, _ = localllm.ReadGGUF(writeGGUF(t, llamatest.GGUF("mamba", map[string]any{"mamba.block_count": uint32(64), "mamba.context_length": uint32(1 << 20)})))
	if kv := localllm.EstimateKV(info, 0, "q8_0"); kv.Known || !strings.Contains(kv.Note, "attention layers") {
		t.Fatalf("%+v", kv)
	}
}

// What is not a GGUF version 2 or 3 header is refused, never guessed: other bytes, version 1, a header cut short, a string
// longer than the limit.
func TestAFileThatIsNotAGGUFHeaderIsRefused(t *testing.T) {
	good := qwenLike()
	v1 := append([]byte("GGUF\x01\x00\x00\x00"), good[8:]...)
	huge := append([]byte(nil), good[:24]...)
	huge = append(huge, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f)
	for name, b := range map[string][]byte{"text": []byte("not a model at all"), "v1": v1, "cut": good[:len(good)/2], "huge": huge, "empty": nil} {
		if _, err := localllm.ReadGGUF(writeGGUF(t, b)); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}

// Fit with the cache: a model that fits the GPU alone but not with its cache runs on the CPU (partly offloaded); an unknown
// cache leaves the model's own fit.
func TestTheFitCountsTheModelAndItsKVCache(t *testing.T) {
	d := localllm.Devices{Probed: true, Devices: []localllm.Device{{Kind: "CUDA", Total: 16 << 30, Free: 14 << 30}}}
	m := localllm.Memory{Total: 64 << 30, Available: 48 << 30, Known: true}
	kv := &localllm.KVCache{Known: true, Bytes: 4 << 30}
	if f := localllm.FitWithKV(12<<30, kv, d, m); f.Target != "cpu" || !f.Fits || f.KVBytes != 4<<30 || f.NeedBytes != 16<<30 {
		t.Fatalf("%+v", f)
	}
	if f := localllm.FitWithKV(8<<30, kv, d, m); f.Target != "gpu" || f.NeedBytes != 12<<30 {
		t.Fatalf("%+v", f)
	}
	if f := localllm.FitWithKV(12<<30, &localllm.KVCache{}, d, m); f.Target != "gpu" || f.KVBytes != 0 || f.NeedBytes != 0 {
		t.Fatalf("%+v", f)
	}
}
