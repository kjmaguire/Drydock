package packs

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
)

// Ranking by meaning (decision P120): when the person turns it on and an embedding model is served on this PC, the passages
// the BM25 search found are ranked again by how close their meaning is to the query, so a passage that answers the question in
// other words than it was asked in comes first. It is no index: nothing is stored, the passages are the ones BM25 found (a
// few more than asked for), and when no embedding model runs, or it fails, the BM25 order stands.

// Embedder makes one vector per text. Available says whether it can be asked now (the person's setting is on and the
// embedding server runs); Embed answers the vectors in the order of the texts.
type Embedder interface {
	Available(ctx context.Context) bool
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// rerankPool is how many passages BM25 finds for a search ranked by meaning: three times the passages asked for, at most 30.
func rerankPool(k int) int { return min(3*k, 30) }

// embedChars bounds the text of a passage that is embedded: the start of it, with its heading path, which is what an
// embedding model of a 512-token batch (llama-server's default) takes in one piece.
const embedChars = 1200

// rerank orders hits by the cosine similarity of their text to the query, the closest first (ties keep the BM25 order), and
// records each one's similarity. It reports false, leaving hits as they are, when the embedder fails.
func rerank(ctx context.Context, e Embedder, query string, hits []Hit) (bool, error) {
	if len(hits) < 2 {
		return false, nil
	}
	texts := make([]string, 0, len(hits)+1)
	texts = append(texts, query)
	for _, h := range hits {
		texts = append(texts, clip(strings.TrimSpace(h.Citation.HeadingPath+"\n"+h.Text), embedChars))
	}
	vecs, err := e.Embed(ctx, texts)
	if err != nil {
		return false, err
	}
	if len(vecs) != len(texts) {
		return false, errors.New("the embedder answered another number of vectors")
	}
	for i := range hits {
		hits[i].Similarity = cosine(vecs[0], vecs[i+1])
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].Similarity > hits[b].Similarity })
	return true, nil
}

// clip is s cut to at most n bytes, at a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// cosine is the cosine similarity of two vectors (0 when either is all zeros or their lengths differ).
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
