package llm_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// contentTraces is a tracer that asks for content (as the telemetry sink does while content export is on, decision P121).
type contentTraces struct {
	traces
	want bool
}

func (r *contentTraces) WantsContent() bool { return r.want }

func TestTheWrapperHandsOverContentOnlyToATracerThatAsksAndNeverForARefusedCall(t *testing.T) {
	secret := "sk-live-ABCDEF0123456789"
	rec := &contentTraces{}
	e := newEnv(t, func(o *llm.Options) { o.Tracer = rec; o.Scanner = llm.NewExactValueScanner(secret) })
	srv := llmtest.New(t, okHandler("the answer names the customer list", &llmtest.Usage{PromptTokens: 30, CompletionTokens: 5}))
	p := e.provider(providers.NewProvider{Name: "or", Kind: providers.KindAggregator, BaseURL: srv.URL + "/v1", Flavor: providers.FlavorOpenRouter})
	r := req(p, publicManifest)
	r.NoCache = true
	r.System = []llm.Block{{Text: "be brief"}}
	r.Messages = []llm.Message{{Role: "user", Content: "summarize the payment module"}}
	if _, err := e.w.Complete(e.ctx, r); err != nil { // the tracer does not want content yet
		t.Fatal(err)
	}
	rec.want = true
	if _, err := e.w.Complete(e.ctx, r); err != nil {
		t.Fatal(err)
	}
	blocked := r
	blocked.Messages = []llm.Message{{Role: "user", Content: "the key is " + secret}}
	if _, err := e.w.Complete(e.ctx, blocked); err == nil {
		t.Fatal("a secret went out")
	}
	got := rec.all()
	if len(got) != 3 {
		t.Fatalf("%d traces", len(got))
	}
	if got[0].Content != nil {
		t.Fatalf("content for a tracer that did not ask: %+v", got[0].Content)
	}
	c := got[1].Content
	if c == nil || strings.Join(c.System, "|") != "be brief" || len(c.Messages) != 1 || c.Messages[0].Content != "summarize the payment module" ||
		c.Output != "the answer names the customer list" || c.FinishReason == "" {
		t.Fatalf("the call's content: %+v", c)
	}
	if got[2].ErrorType != "secret_blocked" || got[2].Content != nil {
		t.Fatalf("a call the secret guard refused hands over no content: %+v", got[2])
	}
}
