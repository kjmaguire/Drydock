package telemetry_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/backends"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/security/secretadapt"
	"github.com/kjmaguire/drydock/internal/telemetry"
)

// The real LLM Wrapper with the sink as its tracer, against a PROTOCOL FIXTURE (llmtest: a local server speaking the OpenAI chat
// completions wire protocol). The prompt and the answer carry text telemetry must never hold.
func TestWrapperCallsAreRecordedAsContentFreeSpansAndMetrics(t *testing.T) {
	r := newRig(t)
	if err := r.sink.SetEnabled(r.ctx, true, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	cost := 0.003
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, _ *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "vendor/model-x-0901", Content: codeText + " " + toolOutput, Usage: &llmtest.Usage{PromptTokens: 500, CompletionTokens: 60, Cost: &cost}})
	})
	reg := providers.NewRegistry(r.db, providers.RegistryOptions{Secrets: providers.NewMemorySecretStoreForTests(), Bus: r.bus})
	p, err := reg.Create(r.ctx, providers.NewProvider{Name: "or", Kind: providers.KindAggregator, Flavor: providers.FlavorOpenRouter, BaseURL: srv.URL + "/v1",
		APIKey: "sk-test", DataClass: providers.DataPrivate, Tier: providers.TierCheap})
	if err != nil {
		t.Fatal(err)
	}
	sc := secretadapt.LLM(scanner(t))
	w, err := llm.NewWrapper(llm.Options{Registry: reg, DB: r.db, Factory: backends.Factory, Scanner: sc, Bus: r.bus, Tracer: r.sink})
	if err != nil {
		t.Fatal(err)
	}
	req := llm.Request{Provider: p.ID, Model: "vendor/model-x", Purpose: "summarize", ProjectID: "prj_1", RunID: "run_1", NoCache: true,
		Manifest: &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "task", Sensitivity: llm.SensitivityPublicDocs}}},
		Messages: []llm.Message{{Role: "user", Content: promptText}}}
	if _, err := w.Complete(r.ctx, req); err != nil {
		t.Fatal(err)
	}
	leak := req
	leak.Messages = []llm.Message{{Role: "user", Content: promptText + " " + fakeToken()}}
	if _, err := w.Complete(r.ctx, leak); err == nil {
		t.Fatal("the scanner let a token out")
	}

	doc, err := r.sink.Preview(r.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	forbiddenIn(t, "the telemetry of the Wrapper's calls", raw)
	if len(doc.Spans) != 2 {
		t.Fatalf("one span per call: %d", len(doc.Spans))
	}
	ok, refused := doc.Spans[0], doc.Spans[1]
	if ok.Name != "drydock.llm.chat" || ok.Status != "ok" || ok.TraceID != telemetry.TraceID("run_1") || ok.Attrs[telemetry.AttrGenAIProvider] != p.ID ||
		ok.Attrs[telemetry.AttrGenAIResponseModel] != "vendor/model-x-0901" || ok.Attrs[telemetry.AttrGenAIInputTokens] != float64(500) ||
		ok.Attrs[telemetry.AttrCostSource] != "reported" || ok.Attrs[telemetry.AttrPurpose] != "summarize" {
		t.Fatalf("the call's span: %+v", ok)
	}
	if refused.Status != "error" || refused.Attrs[telemetry.AttrErrorType] != "secret_blocked" || refused.Attrs[telemetry.AttrGenAIInputTokens] != nil {
		t.Fatalf("the refused call's span: %+v", refused)
	}
	sums := map[string]float64{}
	for _, m := range doc.Metrics {
		key := m.Name
		if tt, ok := m.Attrs[telemetry.AttrGenAITokenType].(string); ok {
			key += "/" + tt
		}
		sums[key] += m.Value
	}
	if sums[telemetry.MetricTokenUsage+"/input"] != 500 || sums[telemetry.MetricTokenUsage+"/output"] != 60 || sums[telemetry.MetricCostUSD] != 0.003 {
		t.Fatalf("metrics %v", sums)
	}
	if _, ok := sums[telemetry.MetricLLMTime]; !ok {
		t.Fatalf("no duration metric: %v", sums)
	}

	// off, nothing is recorded
	r.sink.SetEnabled(r.ctx, false, "human:kyle")
	w.Complete(r.ctx, req)
	if st, _ := r.sink.Status(r.ctx); st.Spans != 2 {
		t.Fatalf("recorded while off: %+v", st)
	}
}

func scanner(t *testing.T) *security.Scanner {
	t.Helper()
	sc, err := security.NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	return sc
}
