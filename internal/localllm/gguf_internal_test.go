package localllm

import (
	"strings"
	"testing"
)

// The header reader keeps a model's own keys and no others: a key that only ends like one is not read, and however many keys
// a header names before its architecture, at most ggufMaxKeys are kept (review 2026-10-27: 1<<20 keys of 60 KB each, named
// "a.<filler>.block_count", were all kept).
func TestTheGGUFReaderKeepsOnlyAFewKeys(t *testing.T) {
	for key, want := range map[string]bool{
		"qwen2.block_count": true, "qwen2.attention.head_count_kv": true, "general.architecture": true,
		"qwen2." + strings.Repeat("x", 60<<10) + ".block_count": false, ".block_count": false, "qwen2.block_counts": false, "block_count": false,
	} {
		if got := wanted(key); got != want {
			t.Errorf("wanted(%.40q) = %v", key, got)
		}
	}
	var k ggufKept
	n := 0
	for k.take(1) {
		n++
		if n > ggufMaxKeys {
			break
		}
	}
	if n != ggufMaxKeys {
		t.Fatalf("%d keys kept, want %d", n, ggufMaxKeys)
	}
	if k = (ggufKept{}); k.take(ggufMaxKept+1) || !k.take(ggufMaxKept) || k.take(1) {
		t.Fatalf("the values over all keys are not bounded: %+v", k)
	}
}
