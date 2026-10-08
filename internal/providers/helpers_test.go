package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
)

// env is a real SQLite database, a real event bus and a registry over them. The secret store is the labeled
// in-memory test store (the product store is Windows-only).
type env struct {
	db   *storage.DB
	bus  *events.SQLBus
	reg  *Registry
	sec  *MemorySecretStore
	pr   *Prober
	ctx  context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 256)
	t.Cleanup(func() { bus.Close(); db.Close() })
	sec := NewMemorySecretStoreForTests()
	reg := NewRegistry(db, RegistryOptions{Secrets: sec, Bus: bus})
	return &env{db: db, bus: bus, reg: reg, sec: sec, pr: NewProber(reg, bus), ctx: ctx}
}

// eventTypes returns the persisted event types in order.
func (e *env) eventTypes(t *testing.T, prefix string) []string {
	t.Helper()
	evs, err := e.bus.Replay(e.ctx, 0, events.Filter{Types: []string{prefix + "*"}}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

func (e *env) events(t *testing.T, typ string) []events.Event {
	t.Helper()
	evs, err := e.bus.Replay(e.ctx, 0, events.Filter{Types: []string{typ}}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func (e *env) create(t *testing.T, np NewProvider) Provider {
	t.Helper()
	if np.Name == "" {
		np.Name = "p-" + time.Now().Format("150405.000000")
	}
	if np.Kind == "" {
		np.Kind = KindDirect
	}
	if np.DataClass == "" {
		np.DataClass = DataPrivate
	}
	if np.Tier == "" {
		np.Tier = TierCheap
	}
	p, err := e.reg.Create(e.ctx, np)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// fixture is a PROTOCOL FIXTURE: an httptest server that answers the real wire protocols the probes speak
// (OpenAI /v1/models, host /health, OpenRouter /key and /models/user, ...). It records requests so tests can assert
// what was sent. It is not a mock of Drydock code.
type fixture struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []recorded
	h    map[string]func(w http.ResponseWriter, r *http.Request)
}

type recorded struct {
	Path, Auth, APIKey, GoogKey, Query string
}

func newFixture(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) *fixture {
	t.Helper()
	f := &fixture{h: routes}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), APIKey: r.Header.Get("x-api-key"),
			GoogKey: r.Header.Get("x-goog-api-key"), Query: r.URL.RawQuery})
		h := f.h[r.URL.Path]
		f.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fixture) set(path string, h func(w http.ResponseWriter, r *http.Request)) {
	f.mu.Lock()
	f.h[path] = h
	f.mu.Unlock()
}

func (f *fixture) seen(path string) []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recorded
	for _, r := range f.reqs {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func jsonBody(v any) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

func status(code int) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}
