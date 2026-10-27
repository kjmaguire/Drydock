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

// The update offered is the newest release of the installed build's own source and backend: a release read for another build
// (the official one, before the TurboQuant build was installed) is not offered as its update (audit 2026-10-27).
func TestAnUpdateIsOfferedOnlyForTheBuildItWasReadFor(t *testing.T) {
	official, _ := localllm.SourceByID(localllm.SourceOfficial)
	tq, _ := localllm.SourceByID(localllm.SourceTurboQuant)
	off := localllm.Installed{Source: official.ID, Backend: localllm.Vulkan, Tag: "b6000"}
	quant := localllm.Installed{Source: tq.ID, Backend: localllm.Vulkan, Tag: "b10298-2.0.0"}
	if got := offeredLatest("b6100", buildKey(off), off, official); got != "b6100" {
		t.Fatalf("the official build's own update: %q", got)
	}
	if got := offeredLatest("b6100", buildKey(off), quant, tq); got != "" {
		t.Fatalf("the TurboQuant build was offered the official tag %q", got)
	}
	cpu := off
	cpu.Backend = localllm.CPU
	if got := offeredLatest("b6100", buildKey(off), cpu, official); got != "" {
		t.Fatalf("another backend was offered %q", got)
	}
	if got := offeredLatest("b10350-2.1.0", buildKey(quant), quant, tq); got != "b10350-2.1.0" {
		t.Fatalf("the TurboQuant build's own update: %q", got)
	}
}
