package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/docparse/docfixture"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/providers"
)

// TEST-DOUBLE PROVIDER: an llmtest protocol fixture speaking the OpenAI chat completions wire format, registered as a model
// provider like any other. It answers a consult with the JSON the answer instruction asks for and a summarize call with a
// line; what it is asked is kept so the test can see what left the app. It proves nothing about a real model's answers.
type packModel struct {
	srv    *llmtest.Server
	mu     sync.Mutex
	answer string // the consult reply
	asked  []string
}

func newPackModel(t *testing.T) *packModel {
	pm := &packModel{answer: `{"answer": "Rotate the gateway token every ninety days [1].", "used": [1]}`}
	pm.srv = llmtest.New(t, func(call llmtest.Call, w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(call.Body)
		pm.mu.Lock()
		pm.asked = append(pm.asked, string(b))
		reply := pm.answer
		pm.mu.Unlock()
		if strings.Contains(string(b), "You summarize what changed") {
			reply = "Added the operator manual (three pages on starting and rotating the gateway)."
		}
		model, _ := call.Body["model"].(string)
		llmtest.Respond(w, call, llmtest.Completion{Model: model, Content: reply, Usage: &llmtest.Usage{PromptTokens: 400, CompletionTokens: 30}})
	})
	return pm
}

func (pm *packModel) calls() []string {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return append([]string(nil), pm.asked...)
}

func openWithProviders(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 3 * time.Second,
		Credentials: providers.NewMemoryCredentialStoreForTests(),
		Adapters:    map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &app{t: t, c: c}
}

func (a *app) addModelProvider(pm *packModel, class providers.DataClass, model string) {
	a.t.Helper()
	var prov providers.Provider
	a.call("provider.create", map[string]any{"Name": "Fixture " + string(class), "Kind": "direct", "BaseURL": pm.srv.URL + "/v1", "APIKey": "sk-fixture-key-1234567890",
		"DataClass": string(class), "Tier": "cheap"}, &prov)
	if err := a.c.Providers.UpsertModels(context.Background(), prov.ID, []providers.Model{
		{ID: model, InputPerM: 0.1, OutputPerM: 0.2, ContextLength: 128000, DataClass: class, Tier: providers.TierCheap},
	}); err != nil {
		a.t.Fatal(err)
	}
}

func manualPack(a *app, id, visibility string) {
	a.t.Helper()
	pdf := docfixture.PDF("%PDF-1.4", "Operator Manual", []string{
		docfixture.TextPage("Starting the gateway", "Run the gateway with the service account, never as root."),
		"",
		docfixture.TextPage("Rotating credentials", "Rotate the gateway token every ninety days with rotate-token."),
	})
	var pk packs.Pack
	a.call("pack.createFromFiles", map[string]any{"id": id, "title": "Operator manual", "visibility": visibility, "license": "CC-BY-4.0",
		"files": []map[string]any{{"name": "manual.pdf", "data": pdf}}}, &pk)
	var rr packs.RefreshResult
	a.call("pack.refresh", map[string]any{"id": id}, &rr)
	if rr.Added != 1 {
		a.t.Fatalf("%+v", rr)
	}
}

const question = "how often is the gateway token rotated?"

func TestPackConsultWithNoModelAnswersWithTheCitedPassages(t *testing.T) {
	a := openWithProviders(t)
	manualPack(a, "ops", "private")
	var c packs.Consult
	a.call("pack.consult", map[string]any{"packIds": []string{"ops"}, "question": question}, &c)
	if !c.Found || c.Route != packs.RouteRetrievalOnly || c.Model != "" || !strings.Contains(c.Degraded, "No model is set up") {
		t.Fatalf("%+v", c)
	}
	if c.Sources[0].Citation.Page != 3 || c.Sources[0].Used {
		t.Fatalf("%+v", c.Sources[0])
	}
}

func TestPackConsultIsAnsweredByTheRoutedModelAndQuotesWhatItUsed(t *testing.T) {
	a := openWithProviders(t)
	pm := newPackModel(t)
	a.addModelProvider(pm, providers.DataPrivate, "fixture-private")
	manualPack(a, "ops", "private")
	before := len(pm.calls())

	var c packs.Consult
	a.call("pack.consult", map[string]any{"packIds": []string{"ops"}, "question": question}, &c)
	if !c.Found || c.Route != packs.RoutePrivate || c.Model != "fixture-private" || c.Degraded != "" || c.Answer != "Rotate the gateway token every ninety days [1]." {
		t.Fatalf("%+v", c)
	}
	if len(c.Citations) != 1 || c.Citations[0].Page != 3 || !c.Sources[0].Used || c.Usage.InputTokens != 400 {
		t.Fatalf("the answer cites and quotes the passage it used: %+v", c)
	}
	asked := pm.calls()[before:]
	if len(asked) != 1 || !strings.Contains(asked[0], "Answer only from the numbered sources") || !strings.Contains(asked[0], "untrusted") ||
		!strings.Contains(asked[0], "Rotate the gateway token every ninety days") || !strings.Contains(asked[0], question) {
		t.Fatalf("the model got the fixed instruction, the question and the wrapped passages: %v", asked)
	}
	// the router decided it, with the consult run kind and a private ceiling, and the decision is recorded
	var kind, ceiling string
	if err := a.c.DB.Reader().QueryRow(`SELECT run_kind, json_extract(request,'$.ContextCeiling') FROM routing_decisions ORDER BY ts DESC, rowid DESC LIMIT 1`).Scan(&kind, &ceiling); err != nil {
		t.Fatal(err)
	}
	if kind != "consult" || ceiling != "private" {
		t.Fatalf("decision %s %s", kind, ceiling)
	}

	// an answer that cites a source that was not supplied is not shown; the passages are
	pm.mu.Lock()
	pm.answer = `{"answer": "Every ninety days [7].", "used": [1]}`
	pm.mu.Unlock()
	a.call("pack.consult", map[string]any{"packIds": []string{"ops"}, "question": "gateway token rotation interval"}, &c)
	if c.Route != packs.RouteRetrievalOnly || !strings.Contains(c.Degraded, "cites [7]") || strings.Contains(c.Answer, "[7]") {
		t.Fatalf("%+v", c)
	}
}

func TestAPrivatePackNeverReachesATrainableModel(t *testing.T) {
	a := openWithProviders(t)
	pm := newPackModel(t)
	a.addModelProvider(pm, providers.DataTrainable, "fixture-trainable")
	manualPack(a, "ops", "private")
	before := len(pm.calls())

	var c packs.Consult
	a.call("pack.consult", map[string]any{"packIds": []string{"ops"}, "question": question}, &c)
	if c.Route != packs.RouteRetrievalOnly || !strings.Contains(c.Degraded, "the model router chose no model") || !c.Found {
		t.Fatalf("%+v", c)
	}
	if n := len(pm.calls()) - before; n != 0 {
		t.Fatalf("a trainable provider was called %d times with a private pack", n)
	}

	// a public pack with a short question may use the trainable route
	manualPack(a, "public-manual", "public")
	a.call("pack.consult", map[string]any{"packIds": []string{"public-manual"}, "question": question}, &c)
	if c.Route != packs.RouteTrainable || c.Model != "fixture-trainable" {
		t.Fatalf("%+v", c)
	}
	// the same public pack with a question carrying code may not
	a.call("pack.consult", map[string]any{"packIds": []string{"public-manual"}, "question": "why does this fail?\n```\nrotate-token --all\n```"}, &c)
	if c.Route != packs.RouteRetrievalOnly || c.Degraded == "" {
		t.Fatalf("%+v", c)
	}
}

func TestPackDigestIsSummarizedByTheRoutedModel(t *testing.T) {
	a := openWithProviders(t)
	pm := newPackModel(t)
	a.addModelProvider(pm, providers.DataPrivate, "fixture-private")
	pdf := docfixture.PDF("%PDF-1.4", "Operator Manual", []string{docfixture.TextPage("Starting the gateway", "Run the gateway with the service account.")})
	var pk packs.Pack
	a.call("pack.createFromFiles", map[string]any{"id": "ops", "title": "Ops", "files": []map[string]any{{"name": "manual.pdf", "data": pdf}}}, &pk)
	// turn the digest on: the pack's own manifest, installed again with the digest enabled
	yaml, err := packs.FilesManifest(packs.NewFilesPack{ID: "ops", Title: "Ops"})
	if err != nil {
		t.Fatal(err)
	}
	a.call("pack.install", map[string]string{"yaml": string(yaml) + "refresh:\n  digest: {enabled: true}\n"}, &pk)
	var rr packs.RefreshResult
	a.call("pack.refresh", map[string]any{"id": "ops"}, &rr)
	if rr.Digest == nil || !strings.Contains(rr.Digest.Summary, "Added the operator manual") {
		t.Fatalf("%+v", rr.Digest)
	}
	asked := pm.calls()
	if len(asked) != 1 || strings.Contains(asked[0], "service account") {
		t.Fatalf("the summary is asked from titles and headings, not page text: %v", asked)
	}
	var kind string
	if err := a.c.DB.Reader().QueryRow(`SELECT run_kind FROM routing_decisions ORDER BY ts DESC, rowid DESC LIMIT 1`).Scan(&kind); err != nil || kind != "summarize" {
		t.Fatalf("%v %s", err, kind)
	}
}
