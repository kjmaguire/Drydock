package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func fixedResolver(addrs ...string) Resolver {
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range addrs {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func TestCheckTransportRule(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		url      string
		fp       string
		resolver Resolver
		wantTLS  TLSMode
		wantLAN  bool
		wantErr  error
	}{
		{"https system", "https://openrouter.ai/api/v1", "", nil, TLSSystem, false, nil},
		{"https pinned", "https://gpu.lan:8443/v1", strings.Repeat("ab", 32), nil, TLSPinned, false, nil},
		{"loopback v4", "http://127.0.0.1:8000/v1", "", nil, TLSNoneLoop, false, nil},
		{"loopback v6", "http://[::1]:8000/v1", "", nil, TLSNoneLoop, false, nil},
		{"localhost", "http://localhost:11434/v1", "", nil, TLSNoneLoop, false, nil},
		{"rfc1918 10/8", "http://10.1.2.3:8000/v1", "", nil, TLSNonePrivate, true, nil},
		{"rfc1918 172.16/12", "http://172.31.255.1:8000/v1", "", nil, TLSNonePrivate, true, nil},
		{"rfc1918 192.168/16", "http://192.168.1.50:8000/v1", "", nil, TLSNonePrivate, true, nil},
		{"link-local", "http://169.254.10.10:8000/v1", "", nil, TLSNonePrivate, true, nil},
		{"just outside 172.16/12", "http://172.32.0.1:8000/v1", "", nil, "", false, ErrInsecureTransport},
		{"public http literal", "http://8.8.8.8:8000/v1", "", nil, "", false, ErrInsecureTransport},
		{"public http name", "http://api.example.com/v1", "", fixedResolver("93.184.216.34"), "", false, ErrInsecureTransport},
		{"name resolving to LAN", "http://gpu.lan:8000/v1", "", fixedResolver("192.168.1.9"), TLSNonePrivate, true, nil},
		{"name resolving to loopback", "http://gpu.local/v1", "", fixedResolver("127.0.0.1"), TLSNoneLoop, false, nil},
		{"one public among private answers", "http://gpu.lan/v1", "", fixedResolver("192.168.1.9", "93.184.216.34"), "", false, ErrInsecureTransport},
		{"fingerprint on http", "http://127.0.0.1/v1", strings.Repeat("ab", 32), nil, "", false, ErrInvalid},
		{"bad fingerprint", "https://x.example/v1", "abcd", nil, "", false, ErrInvalid},
		{"credentials in URL", "https://user:pw@x.example/v1", "", nil, "", false, ErrInvalid},
		{"ftp", "ftp://x.example/", "", nil, "", false, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr, err := CheckTransport(ctx, c.url, c.fp, c.resolver)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tr.TLS != c.wantTLS || tr.LANUnencrypted != c.wantLAN {
				t.Fatalf("got %+v, want tls=%s lan=%v", tr, c.wantTLS, c.wantLAN)
			}
		})
	}
}

func TestRegistryCRUDKeepsKeysOutOfSQLite(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: "sk-ant-SECRET-VALUE", Tier: TierPremium})
	if p.Flavor != FlavorAnthropic || p.KeyRef == "" || p.TLSMode != TLSSystem || !p.Enabled {
		t.Fatalf("unexpected provider %+v", p)
	}
	if len(p.WireAPIs) != 1 || p.WireAPIs[0] != "anthropic_messages" {
		t.Fatalf("wire apis %v", p.WireAPIs)
	}
	got, err := e.reg.Get(e.ctx, p.ID)
	if err != nil || got.KeyRef != p.KeyRef || got.Name != "anthropic" {
		t.Fatalf("get: %v %+v", err, got)
	}
	key, err := e.reg.APIKey(e.ctx, got)
	if err != nil || key != "sk-ant-SECRET-VALUE" {
		t.Fatalf("api key: %q %v", key, err)
	}
	// The key is in the secret store, not in any database file.
	e.db.Writer().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	for _, f := range []string{e.db.Path(), e.db.Path() + "-wal"} {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "SECRET-VALUE") {
			t.Fatalf("api key found in %s", f)
		}
	}
	// Update re-validates and replaces the key.
	got.Tier = TierCheap
	got.MaxConcurrent = 3
	up, err := e.reg.Update(e.ctx, got, "sk-ant-NEW")
	if err != nil || up.Tier != TierCheap || up.MaxConcurrent != 3 {
		t.Fatalf("update: %v %+v", err, up)
	}
	if k, _ := e.reg.APIKey(e.ctx, up); k != "sk-ant-NEW" {
		t.Fatalf("key after update %q", k)
	}
	bad := up
	bad.BaseURL = "http://8.8.8.8/v1"
	if _, err := e.reg.Update(e.ctx, bad, ""); !errors.Is(err, ErrInsecureTransport) {
		t.Fatalf("update to public http must fail, got %v", err)
	}
	if err := e.reg.SetEnabled(e.ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	if g, _ := e.reg.Get(e.ctx, p.ID); g.Enabled {
		t.Fatal("still enabled")
	}
	list, err := e.reg.List(e.ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if err := e.reg.Delete(e.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.Get(e.ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := e.sec.Get(e.ctx, p.KeyRef); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("secret survived delete: %v", err)
	}
	types := e.eventTypes(t, "provider.")
	if len(types) != 3 || types[0] != "provider.registered" || types[1] != "provider.updated" || types[2] != "provider.removed" {
		t.Fatalf("events %v", types)
	}
}

func TestRegistryValidation(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name string
		np   NewProvider
		want error
	}{
		{"no name", NewProvider{Kind: KindDirect, BaseURL: "https://a.example", DataClass: DataPrivate, Tier: TierCheap}, ErrInvalid},
		{"no data class (no default)", NewProvider{Name: "x", Kind: KindDirect, BaseURL: "https://a.example", Tier: TierCheap}, ErrInvalid},
		{"bad kind", NewProvider{Name: "x", Kind: "weird", BaseURL: "https://a.example", DataClass: DataPrivate, Tier: TierCheap}, ErrInvalid},
		{"bad tier", NewProvider{Name: "x", Kind: KindDirect, BaseURL: "https://a.example", DataClass: DataPrivate, Tier: "gold"}, ErrInvalid},
		{"public http", NewProvider{Name: "x", Kind: KindHost, BaseURL: "http://1.2.3.4:8000/v1", DataClass: DataPrivate, Tier: TierCheap}, ErrInsecureTransport},
		{"private LAN without attestation", NewProvider{Name: "x", Kind: KindHost, BaseURL: "http://192.168.1.5:8000/v1", DataClass: DataPrivate, Tier: TierCheap}, ErrAttestationNeeded},
		{"gemini private without billing attestation", NewProvider{Name: "g", Kind: KindDirect, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", DataClass: DataPrivate, Tier: TierCheap}, ErrAttestationNeeded},
		{"bad overrides json", NewProvider{Name: "x", Kind: KindDirect, BaseURL: "https://a.example", DataClass: DataPrivate, Tier: TierCheap, RequestOverrides: []byte("{nope")}, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := e.reg.Create(e.ctx, c.np); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestRegistryDefaultsAndFlags(t *testing.T) {
	e := newEnv(t)
	gem := e.create(t, NewProvider{Name: "gemini-billed", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", APIKey: "k",
		Attestations: map[string]bool{AttestBillingEnabled: true}})
	if gem.Flavor != FlavorGemini || gem.DataClass != DataPrivate {
		t.Fatalf("gemini %+v", gem)
	}
	free := e.create(t, NewProvider{Name: "gemini-free", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", DataClass: DataTrainable, Tier: TierFree})
	if free.DataClass != DataTrainable {
		t.Fatal("unbilled gemini must be registrable as trainable")
	}
	or := e.create(t, NewProvider{Name: "or", Kind: KindAggregator, BaseURL: "https://openrouter.ai/api/v1/", APIKey: "k"})
	if or.Flavor != FlavorOpenRouter || strings.HasSuffix(or.BaseURL, "/") {
		t.Fatalf("openrouter %+v", or)
	}
	lan := e.create(t, NewProvider{Name: "gpu", Kind: KindHost, BaseURL: "http://192.168.1.50:8000/v1", Attestations: map[string]bool{AttestLANAttested: true}})
	if !lan.LANUnencrypted || lan.TLSMode != TLSNonePrivate || lan.MaxConcurrent != 1 || !lan.PrivateEligible() {
		t.Fatalf("lan host %+v", lan)
	}
	lanTrain := e.create(t, NewProvider{Name: "gpu2", Kind: KindHost, BaseURL: "http://10.0.0.8:8000/v1", DataClass: DataTrainable})
	if !lanTrain.LANUnencrypted || lanTrain.PrivateEligible() {
		t.Fatalf("lan trainable host %+v", lanTrain)
	}
	loop := e.create(t, NewProvider{Name: "local", Kind: KindHost, BaseURL: "http://127.0.0.1:11434/v1", Flavor: FlavorOllama})
	if loop.LANUnencrypted || loop.TLSMode != TLSNoneLoop || loop.MaxConcurrent != 1 || !loop.PrivateEligible() {
		t.Fatalf("loopback ollama %+v", loop)
	}
	// An attested LAN host keeps its flag after a round trip through SQLite.
	got, _ := e.reg.Get(e.ctx, lan.ID)
	if !got.LANUnencrypted || !got.Attested(AttestLANAttested) {
		t.Fatalf("round trip lost flags: %+v", got)
	}
}

func TestCatalogUpsertKeepsUserPrices(t *testing.T) {
	e := newEnv(t)
	p := e.create(t, NewProvider{Name: "or", Kind: KindAggregator, BaseURL: "https://openrouter.ai/api/v1"})
	err := e.reg.UpsertModels(e.ctx, p.ID, []Model{{ID: "a/b", ContextLength: 1000, SupportsTools: true, DataClass: DataPrivate, Tier: TierCheap, InputPerM: 1, OutputPerM: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.reg.SetUserPrice(e.ctx, p.ID, "a/b", 9, 8, 7, 6); err != nil {
		t.Fatal(err)
	}
	// A refresh with new catalog prices must not overwrite the user's value but updates everything else.
	err = e.reg.UpsertModels(e.ctx, p.ID, []Model{{ID: "a/b", ContextLength: 5000, SupportsTools: true, DataClass: DataPrivate, Tier: TierCheap, InputPerM: 100, OutputPerM: 200}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.reg.Model(e.ctx, p.ID, "a/b")
	if err != nil {
		t.Fatal(err)
	}
	if m.InputPerM != 9 || m.OutputPerM != 8 || m.CacheReadPerM != 7 || m.CacheWritePerM != 6 || m.PriceSource != "user" || m.ContextLength != 5000 {
		t.Fatalf("model %+v", m)
	}
	if _, err := e.reg.Model(e.ctx, p.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing model: %v", err)
	}
}

func TestEffectiveDataClass(t *testing.T) {
	e := newEnv(t)
	or := e.create(t, NewProvider{Name: "or", Kind: KindAggregator, BaseURL: "https://openrouter.ai/api/v1"})
	if got := e.reg.EffectiveDataClass(e.ctx, or, "x/y"); got != DataPrivate {
		t.Fatalf("paid private route: %s", got)
	}
	if got := e.reg.EffectiveDataClass(e.ctx, or, "cohere/north-mini-code:free"); got != DataTrainable {
		t.Fatalf(":free must be trainable even before the catalog is fetched: %s", got)
	}
	_ = e.reg.UpsertModels(e.ctx, or.ID, []Model{{ID: "z/z", DataClass: DataTrainable, Tier: TierFree}})
	if got := e.reg.EffectiveDataClass(e.ctx, or, "z/z"); got != DataTrainable {
		t.Fatalf("catalog says trainable: %s", got)
	}
	tr := e.create(t, NewProvider{Name: "t", BaseURL: "https://t.example", DataClass: DataTrainable})
	if got := e.reg.EffectiveDataClass(e.ctx, tr, "anything"); got != DataTrainable {
		t.Fatal("trainable provider")
	}
	// a row that cannot be read has no known class: trainable, with the error, not the provider's private class
	if _, err := e.db.Writer().Exec(`UPDATE model_catalog SET context_length='long' WHERE provider_id=? AND model='z/z'`, or.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := e.reg.DataClassOf(e.ctx, or, "z/z"); got != DataTrainable || err == nil {
		t.Fatalf("unreadable row: %s %v", got, err)
	}
	if got, err := e.reg.DataClassOf(e.ctx, or, "x/y"); got != DataPrivate || err != nil {
		t.Fatalf("no row is the provider's class: %s %v", got, err)
	}
}

// TestDialGuardRefusesRebinding: a name that resolved to a LAN address at registration and later resolves to a public
// address is refused at connect time (DNS rebinding).
func TestDialGuardRefusesRebinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	var public atomic.Bool
	resolver := func(ctx context.Context, host string) ([]netip.Addr, error) {
		if public.Load() {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	port := srv.Listener.Addr().(interface{ String() string }).String()
	port = port[strings.LastIndex(port, ":"):]
	p := Provider{ID: "p", BaseURL: "http://model.lan" + port, TLSMode: TLSNoneLoop}
	c, err := NewHTTPClient(p, resolver, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := http.NewRequest("GET", p.BaseURL, nil)
	first.Close = true // no keep-alive: whatever the platform's timing, the next request dials again
	resp, err := c.Do(first)
	if err != nil {
		t.Fatalf("before rebinding: %v", err)
	}
	resp.Body.Close()
	public.Store(true)
	// Idle connections would hide the rebinding; the guard sits on every new dial.
	c.CloseIdleConnections()
	if _, err := c.Get(p.BaseURL); err == nil || !strings.Contains(err.Error(), "public address") {
		t.Fatalf("after rebinding got %v, want a public-address refusal", err)
	}
}

// TestPinnedFingerprint: a self-signed TLS server is accepted only with its exact SHA-256 fingerprint.
func TestPinnedFingerprint(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	sum := sha256.Sum256(srv.Certificate().Raw)
	good := hex.EncodeToString(sum[:])
	wrong := strings.Repeat("00", 32)

	// Without a pin the self-signed certificate is rejected by the system roots.
	c, _ := NewHTTPClient(Provider{BaseURL: srv.URL, TLSMode: TLSSystem}, nil, 0)
	if _, err := c.Get(srv.URL); err == nil {
		t.Fatal("self-signed server accepted without a pin")
	}
	c, err := NewHTTPClient(Provider{BaseURL: srv.URL, TLSMode: TLSPinned, TLSFingerprint: good}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("pinned: %v", err)
	}
	resp.Body.Close()
	c, _ = NewHTTPClient(Provider{BaseURL: srv.URL, TLSMode: TLSPinned, TLSFingerprint: wrong}, nil, 0)
	if _, err := c.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "pinned fingerprint") {
		t.Fatalf("wrong pin: %v", err)
	}
}
