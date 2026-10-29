package llm_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// traces records what the Wrapper tells its tracer.
type traces struct {
	mu  sync.Mutex
	got []llm.CallTrace
}

func (r *traces) TraceCall(_ context.Context, c llm.CallTrace) {
	r.mu.Lock()
	r.got = append(r.got, c)
	r.mu.Unlock()
}

func (r *traces) all() []llm.CallTrace {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]llm.CallTrace(nil), r.got...)
}

func TestEveryWrapperCallIsTracedOnceWithoutContent(t *testing.T) {
	secret := "sk-live-ABCDEF0123456789"
	rec := &traces{}
	e := newEnv(t, func(o *llm.Options) { o.Tracer = rec; o.Scanner = llm.NewExactValueScanner(secret) })
	cost := 0.004
	srv := llmtest.New(t, okHandler("the answer names the customer list", &llmtest.Usage{PromptTokens: 300, CompletionTokens: 20, CachedTokens: 100, Cost: &cost}))
	p := e.provider(providers.NewProvider{Name: "or", Kind: providers.KindAggregator, BaseURL: srv.URL + "/v1", Flavor: providers.FlavorOpenRouter})

	r := req(p, publicManifest)
	r.Purpose, r.ProjectID, r.TaskID, r.RunID = "summarize", "prj_1", "task_1", "run_1"
	r.Messages = []llm.Message{{Role: "user", Content: "summarize the payment module"}}
	if _, err := e.w.Complete(e.ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := e.w.Complete(e.ctx, r); err != nil { // the response cache answers this one
		t.Fatal(err)
	}
	blocked := r
	blocked.Messages = []llm.Message{{Role: "user", Content: "the key is " + secret}}
	if _, err := e.w.Complete(e.ctx, blocked); err == nil {
		t.Fatal("a secret went out")
	}
	got := rec.all()
	if len(got) != 3 {
		t.Fatalf("%d traces for three calls", len(got))
	}
	ok, hit, refused := got[0], got[1], got[2]
	if ok.ErrorType != "" || ok.Purpose != "summarize" || ok.Provider != p.ID || ok.ServedBy != p.ID || ok.RequestModel != "m1" || ok.ResponseModel != "m1" ||
		ok.RunID != "run_1" || ok.TaskID != "task_1" || ok.ProjectID != "prj_1" || ok.Usage.Input != 300 || ok.Usage.Output != 20 || !ok.Cost.Known ||
		ok.Cost.USD != 0.004 || ok.CacheHit || ok.End.Before(ok.Start) || ok.Stream {
		t.Fatalf("the call: %+v", ok)
	}
	if !hit.CacheHit || hit.ErrorType != "" || hit.Usage != (llm.Usage{}) {
		t.Fatalf("the cache hit: %+v", hit)
	}
	if refused.ErrorType != "secret_blocked" || refused.Usage != (llm.Usage{}) || refused.ResponseModel != "" {
		t.Fatalf("the refused call: %+v", refused)
	}
	b, _ := json.Marshal(got)
	for _, f := range []string{"payment module", "customer list", secret} {
		if strings.Contains(string(b), f) {
			t.Errorf("%q reached the tracer", f)
		}
	}

	// a streamed call is traced too
	if _, err := e.w.Stream(e.ctx, r, func(llm.Delta) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if all := rec.all(); len(all) != 4 || !all[3].Stream {
		t.Fatalf("%+v", all)
	}
}

func TestErrorTypeIsAClosedClassNeverTheMessage(t *testing.T) {
	for err, want := range map[error]string{
		nil:                                    "",
		context.Canceled:                       "canceled",
		context.DeadlineExceeded:               "timeout",
		&llm.SecretError{}:                     "secret_blocked",
		&llm.DataClassError{}:                  "data_class_refused",
		&llm.QuotaError{}:                      "quota",
		&llm.UnavailableError{Reason: "x"}:     "unavailable",
		&llm.APIError{Kind: llm.KindAuth}:      "auth",
		&llm.APIError{Kind: llm.KindRateLimit}: "rate_limit",
		&llm.StreamInterruptedError{}:          "stream_interrupted",
		errContent{}:                           "error",
	} {
		if got := llm.ErrorType(err); got != want {
			t.Errorf("%T: %q, want %q", err, got, want)
		}
	}
}

type errContent struct{}

func (errContent) Error() string { return "the prompt said: refactor the payment module" }
