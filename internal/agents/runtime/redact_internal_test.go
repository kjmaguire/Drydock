package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// captureBus records what is published.
type captureBus struct {
	mu  sync.Mutex
	evs []events.Event
}

func (b *captureBus) Publish(_ context.Context, e events.Event) (uint64, error) {
	b.mu.Lock()
	b.evs = append(b.evs, e)
	b.mu.Unlock()
	return uint64(len(b.evs)), nil
}
func (b *captureBus) Subscribe(events.Filter) (<-chan events.Event, func()) { return nil, func() {} }
func (b *captureBus) Replay(context.Context, uint64, events.Filter, int) ([]events.Event, error) {
	return nil, nil
}

func (b *captureBus) texts() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, e := range b.evs {
		if e.Type != "agent.output" {
			continue
		}
		var d struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(e.Data, &d)
		var t workflow.TextDelta
		if json.Unmarshal(d.Data, &t) == nil && (d.Kind == workflow.ChunkText || d.Kind == workflow.ChunkThinking) {
			out = append(out, t.Text)
		}
	}
	return out
}

const secret = "sk-live-AbCdEfGhIjKlMnOpQrStUvWx"

func textChunk(kind, s string) workflow.Chunk {
	b, _ := json.Marshal(workflow.TextDelta{Text: s})
	return workflow.Chunk{Kind: kind, Data: b}
}

// A key the model prints a few characters at a time is never published, and never split across two events that each look clean.
func TestStreamedAgentTextIsPublishedWholeWordsWithSecretsRedacted(t *testing.T) {
	bus := &captureBus{}
	rt := &Runtime{Bus: bus, Redact: func(s string) string { return strings.ReplaceAll(s, secret, "[REDACTED:key]") }}
	m := &Managed{rt: rt, RunID: "arun_1"}
	text := "Reading the file. The key is " + secret + " so I will not use it. Done."
	for i := 0; i < len(text); i += 3 {
		m.observe(context.Background(), textChunk(workflow.ChunkText, text[i:min(i+3, len(text))]))
		if i%30 == 0 {
			time.Sleep(60 * time.Millisecond) // lets the 50 ms window publish what is whole
		}
	}
	m.observe(context.Background(), workflow.Chunk{Kind: workflow.ChunkLog, Data: json.RawMessage(`{}`)}) // ends the text
	got := strings.Join(bus.texts(), "")
	if got != "Reading the file. The key is [REDACTED:key] so I will not use it. Done." {
		t.Fatalf("published text %q", got)
	}
	for _, piece := range bus.texts() {
		if strings.Contains(piece, "sk-live") || strings.Contains(piece, "AbCdEf") {
			t.Fatalf("a piece of the key was published: %q", piece)
		}
	}
	// the word still held when the agent is stopped is not lost
	bus2 := &captureBus{}
	m2 := &Managed{rt: &Runtime{Bus: bus2, Redact: rt.Redact}, RunID: "arun_2"}
	m2.observe(context.Background(), textChunk(workflow.ChunkText, "all done"))
	m2.flushDelta()
	if got := strings.Join(bus2.texts(), ""); got != "all done" {
		t.Fatalf("on stop: %q", got)
	}
}

// Without a redactor the text is published as it always was, a piece at a time.
func TestWithoutARedactorTextIsPublishedAsItArrives(t *testing.T) {
	bus := &captureBus{}
	m := &Managed{rt: &Runtime{Bus: bus}, RunID: "arun_3"}
	m.observe(context.Background(), textChunk(workflow.ChunkText, "par"))
	m.flushDelta()
	if got := strings.Join(bus.texts(), ""); got != "par" {
		t.Fatalf("%q", got)
	}
}
