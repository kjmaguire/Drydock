package core

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/localllm"
)

// Only a person cancels a model download (or clears its row): an agent's identity, or none, is refused before anything is
// looked at, like every other local.* change.
func TestOnlyAPersonCancelsAModelDownload(t *testing.T) {
	l := &localExt{downloads: map[string]*localDownload{}}
	for _, by := range []string{"agent:a1", "human:", ""} {
		if _, err := l.cancelDownload(context.Background(), "acme/tiny-GGUF:Q4_0", by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("%q: %v", by, err)
		}
	}
}

// A download whose row was taken out while it ran (the model was removed) ends quietly: nothing to report, no panic.
func TestADownloadWhoseRowIsGoneEndsQuietly(t *testing.T) {
	l := &localExt{downloads: map[string]*localDownload{}}
	l.m = &localllm.Manager{Hub: &localllm.Hub{BaseURL: "http://127.0.0.1:9"}, Store: &localllm.Store{Dir: t.TempDir()}}
	v := localllm.Variant{Name: "Q4_0", Size: 3, Files: []localllm.File{{Path: "tiny-Q4_0.gguf", Size: 3, SHA256: strings.Repeat("0", 64)}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l.runDownload(ctx, "acme/tiny-GGUF", v, "acme/tiny-GGUF:Q4_0") // no row under the id, and the core (l.c) is nil
	if len(l.downloads) != 0 {
		t.Fatalf("%+v", l.downloads)
	}
}
