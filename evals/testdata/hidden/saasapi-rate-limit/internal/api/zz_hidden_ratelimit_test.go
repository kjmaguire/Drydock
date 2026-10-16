package api_test

// Hidden acceptance test of the trial task saasapi-rate-limit (evals/tasks). The agents never see it.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/saasapi/internal/api"
	"example.com/saasapi/internal/store"
)

type hiddenClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *hiddenClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *hiddenClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func hiddenLimited(t *testing.T) (http.Handler, *hiddenClock) {
	t.Helper()
	st := store.New()
	st.AddAccount(store.Account{ID: "acc_free", Name: "Free Co", Plan: store.PlanFree})
	st.AddAccount(store.Account{ID: "acc_pro", Name: "Pro Co", Plan: store.PlanPro})
	st.AddAccount(store.Account{ID: "acc_pro2", Name: "Pro Two", Plan: store.PlanPro})
	st.AddKey("k-free", "acc_free")
	st.AddKey("k-free-2", "acc_free")
	st.AddKey("k-pro", "acc_pro")
	st.AddKey("k-pro2", "acc_pro2")
	clk := &hiddenClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return api.New(st, api.Options{Now: clk.now}), clk
}

func hiddenGet(h http.Handler, path, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func hiddenWantLimited(t *testing.T, r *httptest.ResponseRecorder, retryAfter string) {
	t.Helper()
	if r.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d %s", r.Code, r.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || body["error"] != "rate_limited" {
		t.Fatalf("429 body %q: %v", r.Body, err)
	}
	if got := r.Header().Get("Retry-After"); got != retryAfter {
		t.Fatalf("Retry-After %q, want %q", got, retryAfter)
	}
}

func TestHiddenRateLimitWindowAndRetryAfter(t *testing.T) {
	h, clk := hiddenLimited(t)
	if r := hiddenGet(h, "/api/customers", "k-free"); r.Code != http.StatusOK { // t0
		t.Fatalf("first request: %d", r.Code)
	}
	clk.add(10 * time.Second) // t0+10s
	for i := 0; i < 59; i++ {
		if r := hiddenGet(h, "/api/customers", "k-free"); r.Code != http.StatusOK {
			t.Fatalf("request %d of 60: %d", i+2, r.Code)
		}
	}
	hiddenWantLimited(t, hiddenGet(h, "/api/customers", "k-free"), "50") // the t0 request leaves at t0+60s
	hiddenWantLimited(t, hiddenGet(h, "/api/customers", "k-free"), "50") // rejected requests do not count
	if r := hiddenGet(h, "/api/customers", "k-free-2"); r.Code != http.StatusOK {
		t.Fatalf("another key of the same account has its own limit: %d", r.Code)
	}
	clk.add(50 * time.Second) // t0+60s: the t0 request has left (t-60s, t]
	if r := hiddenGet(h, "/api/customers", "k-free"); r.Code != http.StatusOK {
		t.Fatalf("after the oldest request left the window: %d", r.Code)
	}
	hiddenWantLimited(t, hiddenGet(h, "/api/customers", "k-free"), "10") // the t0+10s requests leave at t0+70s
	clk.add(9500 * time.Millisecond)                                     // t0+69.5s
	hiddenWantLimited(t, hiddenGet(h, "/api/customers", "k-free"), "1")  // half a second, rounded up
	clk.add(500 * time.Millisecond)                                      // t0+70s
	if r := hiddenGet(h, "/api/customers", "k-free"); r.Code != http.StatusOK {
		t.Fatalf("at t0+70s: %d", r.Code)
	}
}

func TestHiddenRateLimitPlansUnauthorizedAndHealth(t *testing.T) {
	h, _ := hiddenLimited(t)
	for i := 0; i < 600; i++ {
		if r := hiddenGet(h, "/api/invoices", "k-pro"); r.Code != http.StatusOK {
			t.Fatalf("pro request %d: %d", i+1, r.Code)
		}
	}
	hiddenWantLimited(t, hiddenGet(h, "/api/invoices", "k-pro"), "60")
	for i := 0; i < 100; i++ {
		if r := hiddenGet(h, "/api/customers", "unknown"); r.Code != http.StatusUnauthorized {
			t.Fatalf("unknown key: %d", r.Code)
		}
		if r := hiddenGet(h, "/api/customers", ""); r.Code != http.StatusUnauthorized {
			t.Fatalf("no key: %d", r.Code)
		}
		if r := hiddenGet(h, "/healthz", ""); r.Code != http.StatusOK {
			t.Fatalf("healthz: %d", r.Code)
		}
	}
}

func TestHiddenRateLimitHoldsUnderConcurrency(t *testing.T) {
	h, _ := hiddenLimited(t)
	var ok, limited, other atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				switch hiddenGet(h, "/api/customers", "k-pro2").Code {
				case http.StatusOK:
					ok.Add(1)
				case http.StatusTooManyRequests:
					limited.Add(1)
				default:
					other.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 600 || limited.Load() != 400 || other.Load() != 0 {
		t.Fatalf("1000 concurrent requests on a pro key: %d allowed, %d limited, %d other (want 600, 400, 0)", ok.Load(), limited.Load(), other.Load())
	}
}
