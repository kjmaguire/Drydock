package core_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
)

// A provider's detail (models, prices, quota) and the human's edits: settings, a replaced key, a price, a quota limit.
func TestProviderDetailAndEdits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	creds := providers.NewMemoryCredentialStoreForTests()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 3 * time.Second, Credentials: creds,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &app{t: t, c: c}
	// PROTOCOL FIXTURE: an OpenAI-compatible server
	srv := llmtest.New(t, func(call llmtest.Call, w http.ResponseWriter, r *http.Request) {
		llmtest.Respond(w, call, llmtest.Completion{Model: "m", Content: "ok"})
	})
	var p providers.Provider
	a.call("provider.create", map[string]any{"Name": "Fixture", "Kind": "direct", "BaseURL": srv.URL + "/v1", "APIKey": "sk-fixture-key-1234567890", "DataClass": "private", "Tier": "cheap"}, &p)
	if err := c.Providers.UpsertModels(context.Background(), p.ID, []providers.Model{{ID: "model-a", InputPerM: 0.5, OutputPerM: 1.5, SupportsTools: true, Tier: providers.TierCheap, DataClass: providers.DataPrivate}}); err != nil {
		t.Fatal(err)
	}

	var d core.ProviderDetailView
	a.call("provider.detail", map[string]any{"id": p.ID}, &d)
	if d.Provider.ID != p.ID || len(d.Models) != 1 || d.Models[0].InputPerM != 0.5 || len(d.Quota) != 0 {
		t.Fatalf("detail %+v", d)
	}
	// only OpenRouter publishes key limits
	a.call("provider.detail", map[string]any{"id": p.ID, "refreshKey": true}, &d)
	if d.OpenRouterKey != nil || !strings.Contains(d.KeyError, "only OpenRouter") {
		t.Fatalf("key refresh %+v", d)
	}

	// edit the settings and replace the key; the key is never returned
	name, tier, maxc := "Renamed", "premium", 3
	var up providers.Provider
	a.call("provider.update", map[string]any{"id": p.ID, "name": name, "tier": tier, "maxConcurrent": maxc, "apiKey": "sk-replaced-key-0987654321"}, &up)
	if up.Name != "Renamed" || up.Tier != providers.TierPremium || up.MaxConcurrent != 3 {
		t.Fatalf("updated %+v", up)
	}
	if k, _ := c.Providers.APIKey(context.Background(), up); k != "sk-replaced-key-0987654321" {
		t.Fatalf("key not replaced")
	}
	for _, bad := range []map[string]any{
		{"id": p.ID, "name": "  "},
		{"id": p.ID, "maxConcurrent": -1},
		{"id": p.ID, "tier": "gold"},
		{"id": p.ID, "baseUrl": "ftp://nope"},
	} {
		if _, err := a.c.Call(t.Context(), "provider.update", mustJSON(bad)); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}

	// a price set by the human sticks, and says so
	a.call("provider.setPrice", map[string]any{"providerId": p.ID, "model": "model-a", "inputPerM": 2, "outputPerM": 6}, nil)
	a.call("provider.detail", map[string]any{"id": p.ID}, &d)
	if d.Models[0].InputPerM != 2 || d.Models[0].OutputPerM != 6 || d.Models[0].PriceSource != "user" || d.Models[0].PriceUpdatedAt.IsZero() {
		t.Fatalf("price %+v", d.Models[0])
	}
	for _, bad := range []map[string]any{
		{"providerId": p.ID, "model": "model-a", "inputPerM": -1},
		{"providerId": p.ID, "model": "model-a", "outputPerM": 1e9},
		{"providerId": p.ID, "model": "nope", "inputPerM": 1},
	} {
		if _, err := a.c.Call(t.Context(), "provider.setPrice", mustJSON(bad)); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}

	// a quota the provider does not publish, configured by the human
	a.call("provider.setQuota", map[string]any{"providerId": p.ID, "model": "model-a", "kind": "rpd", "limit": 250}, nil)
	a.call("provider.detail", map[string]any{"id": p.ID}, &d)
	if len(d.Quota) != 1 || d.Quota[0].Kind != "rpd" || d.Quota[0].Limit != 250 || d.Quota[0].Source != "configured" {
		t.Fatalf("quota %+v", d.Quota)
	}
	for _, bad := range []map[string]any{
		{"providerId": p.ID, "kind": "weekly", "limit": 5},
		{"providerId": p.ID, "kind": "rpm", "limit": 0},
		{"providerId": "prov_nope", "kind": "rpm", "limit": 5},
	} {
		if _, err := a.c.Call(t.Context(), "provider.setQuota", mustJSON(bad)); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}
