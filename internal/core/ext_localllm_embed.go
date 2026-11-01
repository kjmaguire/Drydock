package core

import (
	"context"
	"sync"

	"github.com/kjmaguire/drydock/internal/localllm"
)

// The embedding server's one use in Drydock (decision P120): ranking the passages a pack search found by meaning. It is the
// person's choice (LocalSettings.RankPacks, off by default) and local only: the texts go to the embedding server on 127.0.0.1,
// with the key only the core holds. Without an embedding model served, the search keeps its BM25 order (internal/packs).

// localRegistry holds each core's local models extension, for the pack search that starts on its own.
var localRegistry sync.Map // *Core -> *localExt

// localEmbedder is the pack search's Embedder: the local models extension of the core, looked up at each call.
type localEmbedder struct{ c *Core }

func (e localEmbedder) ext() *localExt {
	if v, ok := localRegistry.Load(e.c); ok {
		return v.(*localExt)
	}
	return nil
}

// Available says whether a search may be ranked by meaning now: the person turned it on and an embedding model is served.
func (e localEmbedder) Available(ctx context.Context) bool {
	l := e.ext()
	if l == nil || !l.settings(ctx).RankPacks {
		return false
	}
	st := l.m.Embed.State()
	return st.State == "running" && st.Embeddings
}

func (e localEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	l := e.ext()
	if l == nil {
		return nil, localllm.ErrNoEmbeddings
	}
	return l.m.Embed.Embed(ctx, texts)
}
