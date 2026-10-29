package packs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// topicEmbedder is a LABELLED TEST DOUBLE of the local embedding model: a text's vector counts its words of two topics,
// releasing code and reading logs, so two passages that share the query's words can still be told apart by what they are
// about. It records what it was asked.
type topicEmbedder struct {
	off   bool
	fail  bool
	asked [][]string
}

var topics = [][]string{{"deploy", "publish", "upload", "release", "live"}, {"log", "console", "print", "tail"}}

func (e *topicEmbedder) Available(context.Context) bool { return !e.off }

func (e *topicEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	e.asked = append(e.asked, texts)
	if e.fail {
		return nil, errors.New("the embedding server answered 500")
	}
	out := make([][]float64, len(texts))
	for i, t := range texts {
		v := make([]float64, len(topics))
		for _, w := range strings.Fields(strings.ToLower(t)) {
			for d, words := range topics {
				for _, x := range words {
					if strings.HasPrefix(w, x) {
						v[d]++
					}
				}
			}
		}
		out[i] = v
	}
	return out, nil
}

// A search ranked by meaning (decision P120): BM25 finds the passages (a few more than asked for), the embedding model orders
// them by how close they are to the query, and the first k stay; the result says so and gives each passage's similarity.
// Without an embedder, while it is not available, or when it fails, the BM25 order stands.
func TestASearchIsRankedByMeaningWhenAnEmbeddingModelIsServed(t *testing.T) {
	docs := t.TempDir()
	writeFiles(t, docs, map[string]string{
		"logs.md":   "# Publish worker logs\n\nPublish worker logs to the console: the worker prints each log line, and tail shows them.\n",
		"deploy.md": "# Going live\n\nTo publish a worker, deploy it: wrangler uploads the worker and makes the release live.\n",
		"other.md":  "# Bindings\n\nA worker reads its bindings from the environment.\n",
	})
	run := func(emb Embedder, k int) SearchResult {
		t.Helper()
		e := newEnv(t, func(o *Options) {
			if emb != nil {
				o.Embedder = emb
			}
		})
		e.install(localManifest("cf", docs, ""))
		e.refresh("cf")
		r, err := e.s.Search(context.Background(), SearchRequest{PackIDs: []string{"cf"}, Query: "publish worker", K: k})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := func(r SearchResult) string {
		if len(r.Hits) == 0 {
			return ""
		}
		return strings.TrimPrefix(r.Hits[0].Citation.URL, "local:")
	}
	plain := run(nil, 5)
	if first(plain) != "logs.md" || plain.Reranked || plain.Hits[0].Similarity != 0 {
		t.Fatalf("BM25 alone puts the page with the words in its heading first: %s %+v", first(plain), plain.Hits)
	}

	emb := &topicEmbedder{}
	ranked := run(emb, 5)
	if first(ranked) != "deploy.md" || !ranked.Reranked || ranked.Hits[0].Similarity <= ranked.Hits[1].Similarity {
		t.Fatalf("by meaning the page about releasing comes first: %s %+v", first(ranked), ranked.Hits)
	}
	if len(emb.asked) != 1 || emb.asked[0][0] != "publish worker" || len(emb.asked[0]) != 1+len(ranked.Hits) {
		t.Fatalf("one call, the query first: %q", emb.asked)
	}
	// k passages are kept from the larger set BM25 found
	one := run(&topicEmbedder{}, 1)
	if len(one.Hits) != 1 || first(one) != "deploy.md" {
		t.Fatalf("%+v", one.Hits)
	}
	for name, e := range map[string]*topicEmbedder{"off": {off: true}, "failing": {fail: true}} {
		r := run(e, 5)
		if first(r) != "logs.md" || r.Reranked || len(r.Hits) != len(plain.Hits) {
			t.Fatalf("%s: %s %+v", name, first(r), r.Hits)
		}
		if name == "off" && len(e.asked) != 0 {
			t.Fatal("an embedder that is not available was asked")
		}
	}
}

func TestCosineAndClip(t *testing.T) {
	if c := cosine([]float64{1, 0}, []float64{1, 0}); c < 0.999 {
		t.Fatal(c)
	}
	if c := cosine([]float64{1, 0}, []float64{0, 1}); c != 0 {
		t.Fatal(c)
	}
	if c := cosine([]float64{0, 0}, []float64{1, 1}); c != 0 {
		t.Fatal(c)
	}
	if c := cosine([]float64{1}, []float64{1, 1}); c != 0 {
		t.Fatal(c)
	}
	if s := clip("héllo", 2); s != "h" {
		t.Fatalf("%q", s)
	}
	if s := clip("abc", 5); s != "abc" {
		t.Fatal(s)
	}
}
