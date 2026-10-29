package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/packs"
)

// The embedding server's use in Drydock (decision P120): with the person's "rank pack searches by meaning" on and an embedding
// model served, a pack search's passages are ordered by the embedding server's vectors (the stand-in's WordVector, which knows
// shared words, not meaning); with it off, or no embedding model served, the BM25 order stands.
func TestPackSearchesAreRankedByMeaningWithTheEmbeddingModel(t *testing.T) {
	rig := newLocalRig(t)
	rig.hub.AddModel("acme/embed-GGUF", 20, nil, map[string][]byte{"embed-Q8_0.gguf": padded("qwen3", qwenHeader("qwen3", map[string]any{"qwen3.pooling_type": uint32(3)}), 3000)})
	a := localCore(t, rig)
	localCall(a, "local.install", map[string]any{"backend": "vulkan"})
	a.waitFor("the install", func() bool {
		v := localCall(a, "local.get", nil)
		return v.Runtime.Job != nil && v.Runtime.Job.State != "running"
	})
	localCall(a, "local.settings.set", map[string]any{"port": freePort(t), "embeddingPort": freePort(t)})
	download(a, "acme/embed-GGUF", "Q8_0")

	docs := t.TempDir()
	for name, body := range map[string]string{
		"logs.md":    "# Publish worker logs\n\nPublish worker logs to the console: the worker prints each log line, and tail shows every one of them while it runs.\n",
		"release.md": "# Release\n\nPublish a worker.\n",
	} {
		if err := os.WriteFile(filepath.Join(docs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.call("pack.install", map[string]string{"yaml": "schema: 1\nid: cf\ntitle: CF docs\nkind: indexed\nvisibility: private\nsources:\n  - {type: local_dir, path: \"" + strings.ReplaceAll(docs, `\`, `/`) + "\"}\n"}, nil)
	a.call("pack.refresh", map[string]any{"id": "cf"}, nil)
	search := func() packs.SearchResult {
		t.Helper()
		var sr packs.SearchResult
		a.call("pack.search", map[string]any{"query": "publish worker", "packIds": []string{"cf"}}, &sr)
		if len(sr.Hits) != 2 {
			t.Fatalf("%+v", sr.Hits)
		}
		return sr
	}
	first := func(sr packs.SearchResult) string { return strings.TrimPrefix(sr.Hits[0].Citation.URL, "local:") }

	if sr := search(); first(sr) != "logs.md" || sr.Reranked {
		t.Fatalf("BM25: %s %v", first(sr), sr.Reranked)
	}
	// on, but no embedding model served: BM25
	v := localCall(a, "local.settings.set", map[string]any{"rankPacks": true})
	if !v.Settings.RankPacks {
		t.Fatalf("%+v", v.Settings)
	}
	if sr := search(); first(sr) != "logs.md" || sr.Reranked {
		t.Fatalf("no embedding model: %s %v", first(sr), sr.Reranked)
	}
	localCall(a, "local.embeddings.start", map[string]any{"modelId": "acme/embed-GGUF:Q8_0"})
	sr := search()
	if first(sr) != "release.md" || !sr.Reranked || sr.Hits[0].Similarity <= sr.Hits[1].Similarity || sr.Hits[1].Similarity <= 0 {
		t.Fatalf("by meaning: %s %+v", first(sr), sr.Hits)
	}
	// off again: BM25, with the embedding model still served
	localCall(a, "local.settings.set", map[string]any{"rankPacks": false})
	if sr := search(); first(sr) != "logs.md" || sr.Reranked {
		t.Fatalf("off: %s %v", first(sr), sr.Reranked)
	}
	localCall(a, "local.settings.set", map[string]any{"rankPacks": true})
	localCall(a, "local.embeddings.stop", nil)
	if sr := search(); first(sr) != "logs.md" || sr.Reranked {
		t.Fatalf("stopped: %s %v", first(sr), sr.Reranked)
	}
}
