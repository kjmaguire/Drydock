package core_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// OTLP export and the Wrapper's spans through the methods the UI calls (decision P113). The collector is a TEST DOUBLE: a local
// HTTP server that keeps what it receives (internal/telemetry's tests decode the bodies field by field). The model provider is a
// PROTOCOL FIXTURE (llmtest).
func TestTelemetryOTLPThroughTheMethods(t *testing.T) {
	const key = "otlp-key-3c9e1a7b5d2f4e6a"
	dir := t.TempDir()
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 3 * time.Second,
		Credentials: providers.NewMemoryCredentialStoreForTests(),
		Adapters:    map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &app{t: t, c: c}

	var mu sync.Mutex
	var got []struct {
		path, key string
		body      []byte
	}
	col := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, struct {
			path, key string
			body      []byte
		}{r.URL.Path, r.Header.Get("X-Api-Key"), b})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer col.Close()

	type otlpStatus struct {
		Enabled  bool     `json:"enabled"`
		Active   bool     `json:"active"`
		Endpoint string   `json:"endpoint"`
		Encoding string   `json:"encoding"`
		Headers  []string `json:"headers"`
		LastOK   int64    `json:"lastSuccessAt"`
	}
	var st struct {
		Enabled         bool        `json:"enabled"`
		NetworkExporter bool        `json:"networkExporter"`
		OTLP            *otlpStatus `json:"otlp"`
	}
	a.call("telemetry.status", nil, &st)
	if st.Enabled || st.NetworkExporter || st.OTLP == nil || st.OTLP.Enabled || st.OTLP.Endpoint != "" {
		t.Fatalf("off by default, with no endpoint: %+v %+v", st, st.OTLP)
	}
	if _, err := c.Call(context.Background(), "telemetry.otlp.set", mustJSON(map[string]any{"enabled": true, "endpoint": "http://192.0.2.7:4318"})); err == nil ||
		!strings.Contains(err.Error(), "plain http is allowed only to this computer") {
		t.Fatalf("plain http to another computer: %v", err)
	}
	a.call("telemetry.enable", map[string]any{"enabled": true}, &st)
	a.call("telemetry.otlp.set", map[string]any{"enabled": true, "endpoint": col.URL, "encoding": "json", "setHeaders": map[string]string{"X-Api-Key": key}}, &st)
	if !st.NetworkExporter || st.OTLP == nil || !st.OTLP.Active || strings.Join(st.OTLP.Headers, ",") != "X-Api-Key" || st.OTLP.Encoding != "json" {
		t.Fatalf("%+v %+v", st, st.OTLP)
	}

	var res struct {
		OK     bool       `json:"ok"`
		Error  string     `json:"error"`
		Status otlpStatus `json:"status"`
	}
	a.call("telemetry.otlp.test", nil, &res)
	if !res.OK || res.Status.LastOK == 0 {
		t.Fatalf("%+v", res)
	}
	mu.Lock()
	if len(got) != 1 || got[0].path != "/v1/traces" || got[0].key != key || !strings.Contains(string(got[0].body), "drydock.telemetry.test") {
		t.Fatalf("%+v", got)
	}
	mu.Unlock()

	// the Wrapper's own calls are recorded (and so sent) like node executions, with no content
	srv := llmtest.New(t, func(call llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, call, llmtest.Completion{Model: "m1", Content: "the secret plan for the payment module", Usage: &llmtest.Usage{PromptTokens: 40, CompletionTokens: 5}})
	})
	p, err := c.Providers.Create(context.Background(), providers.NewProvider{Name: "fixture", Kind: providers.KindDirect, BaseURL: srv.URL + "/v1", APIKey: "sk-test",
		DataClass: providers.DataPrivate, Tier: providers.TierCheap})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.EconomicsLLM(c).Complete(context.Background(), llm.Request{Provider: p.ID, Model: "m1", Purpose: "summarize", NoCache: true,
		Manifest: &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "task", Sensitivity: llm.SensitivityPublicDocs}}},
		Messages: []llm.Message{{Role: "user", Content: "summarize the payment module"}}}); err != nil {
		t.Fatal(err)
	}
	var doc json.RawMessage
	a.call("telemetry.preview", map[string]any{"limit": 0}, &doc)
	if !strings.Contains(string(doc), `"drydock.llm.chat"`) || !strings.Contains(string(doc), `"gen_ai.usage.input_tokens":40`) || strings.Contains(string(doc), "payment module") {
		t.Fatalf("the Wrapper's call in the sink: %s", doc)
	}

	// the key is in the credential store only
	var settings string
	c.DB.Reader().QueryRow(`SELECT group_concat(key||'='||value, ' ') FROM settings`).Scan(&settings)
	if strings.Contains(settings, key) || !strings.Contains(settings, "telemetry.otlp.headers") {
		t.Fatalf("settings: %s", settings)
	}
	var events string
	c.DB.Reader().QueryRow(`SELECT coalesce(group_concat(data, ' '), '') FROM events`).Scan(&events)
	if strings.Contains(events, key) {
		t.Fatal("the key reached an event")
	}

	// revoking consent: sending stops, and the status says so
	a.call("telemetry.otlp.set", map[string]any{"enabled": false}, &st)
	if st.NetworkExporter || st.OTLP.Active || st.OTLP.Endpoint != col.URL {
		t.Fatalf("%+v %+v", st, st.OTLP)
	}
}
