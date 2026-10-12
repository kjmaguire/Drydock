package integrations

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// The documented budgets are in the code, by kind and authentication.
func TestDocumentedRateLimits(t *testing.T) {
	cases := []struct {
		kind, auth string
		n          int
		per        time.Duration
	}{
		{KindLinear, AuthAPIKey, 2500, time.Hour},
		{KindLinear, AuthOAuth, 5000, time.Hour},
		{KindNotion, AuthAPIKey, 3, time.Second},
		{KindAikido, AuthClientCredentials, 20, time.Minute},
		{KindSentry, AuthAPIKey, 0, 0}, // published per endpoint in headers only
	}
	for _, c := range cases {
		rl := RateLimitFor(c.kind, c.auth)
		if rl.Requests != c.n || rl.Per != c.per || rl.Doc == "" || rl.Note == "" {
			t.Errorf("%s/%s: %+v", c.kind, c.auth, rl)
		}
	}
}

// The limiter spends the budget evenly: after the burst, requests are spaced at the documented rate.
func TestLimiterSpacesRequestsAtTheDocumentedRate(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var slept []time.Duration
	l := newLimiter(RateLimit{Requests: 3, Per: time.Second, Burst: 3})
	l.now = func() time.Time { return now }
	l.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		now = now.Add(d)
		return nil
	}
	for i := 0; i < 6; i++ {
		if err := l.wait(context.Background(), KindNotion); err != nil {
			t.Fatal(err)
		}
	}
	// three go out at once (the burst), then one every third of a second
	want := []time.Duration{0, 0, 0, 333333333, 333333333, 333333333}
	for i, d := range slept {
		if diff := d - want[i]; diff > time.Millisecond || diff < -time.Millisecond {
			t.Fatalf("waits %v", slept)
		}
	}
	// over an hour the Linear budget never exceeds 2,500 requests
	now = now.Add(time.Hour)
	ll := newLimiter(limitLinearKey)
	start := now
	ll.now = func() time.Time { return now }
	ll.sleep = func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }
	n := 0
	for now.Sub(start) < time.Hour {
		if err := ll.wait(context.Background(), KindLinear); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n > 2500+limitLinearKey.Burst || n < 2400 {
		t.Fatalf("%d requests in an hour", n)
	}
}

// When the vendor says to wait longer than a request may wait, the request is not sent and fails as rate limited.
func TestLimiterHoldsUntilTheVendorsReset(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	l := newLimiter(RateLimit{})
	l.now = func() time.Time { return now }
	var slept time.Duration
	l.sleep = func(_ context.Context, d time.Duration) error { slept += d; return nil }
	l.hold(now.Add(5 * time.Second))
	if err := l.wait(context.Background(), KindSentry); err != nil || slept != 5*time.Second {
		t.Fatalf("%v %v", err, slept)
	}
	l.hold(now.Add(10 * time.Minute))
	err := l.wait(context.Background(), KindSentry)
	var f *Failure
	if !errors.As(err, &f) || f.Code != CodeRateLimited || f.RetryAfter != 10*time.Minute {
		t.Fatalf("%v", err)
	}
}

func TestRateHeadersOfEachVendor(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	h := http.Header{}
	h.Set("X-Sentry-Rate-Limit-Remaining", "0")
	h.Set("X-Sentry-Rate-Limit-Reset", strconv.FormatInt(now.Add(30*time.Second).Unix(), 10))
	if got := RateHeaders(KindSentry, h, 200, now); !got.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("sentry %v", got)
	}
	h.Set("X-Sentry-Rate-Limit-Remaining", "12")
	if got := RateHeaders(KindSentry, h, 200, now); !got.IsZero() {
		t.Fatalf("sentry with requests left %v", got)
	}
	l := http.Header{}
	l.Set("X-RateLimit-Complexity-Remaining", "0")
	l.Set("X-RateLimit-Complexity-Reset", strconv.FormatInt(now.Add(time.Minute).UnixMilli(), 10))
	l.Set("X-RateLimit-Requests-Remaining", "100")
	if got := RateHeaders(KindLinear, l, 200, now); !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("linear complexity %v", got)
	}
	r := http.Header{}
	r.Set("Retry-After", "17")
	if got := RateHeaders(KindNotion, r, 429, now); !got.Equal(now.Add(17 * time.Second)) {
		t.Fatalf("retry-after seconds %v", got)
	}
	r.Set("Retry-After", now.Add(2*time.Minute).Format(http.TimeFormat))
	if got := RateHeaders(KindAikido, r, 429, now); !got.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("retry-after date %v", got)
	}
	if got := RateHeaders(KindAikido, r, 200, now); !got.IsZero() {
		t.Fatal("Retry-After counts only on a 429")
	}
	r.Set("Retry-After", "999999999")
	if got := RateHeaders(KindNotion, r, 429, now); got.After(now.Add(24 * time.Hour)) {
		t.Fatalf("a nonsense wait is capped: %v", got)
	}
}

func TestSentryLinkHeaderCursor(t *testing.T) {
	h := http.Header{}
	h.Add("Link", `<https://sentry.io/api/0/organizations/acme/issues/?&cursor=1420837590:0:1>; rel="previous"; results="false"; cursor="1420837590:0:1", <https://sentry.io/api/0/organizations/acme/issues/?&cursor=1420837533:0:0>; rel="next"; results="true"; cursor="1420837533:0:0"`)
	if got := nextLinkCursor(h); got != "1420837533:0:0" {
		t.Fatalf("%q", got)
	}
	// the docs' first example has no cursor parameter: it is read from the URL
	h2 := http.Header{}
	h2.Add("Link", `<https://sentry.io/api/0/x/?&cursor=0:100:0>; rel="next"; results="true"`)
	if got := nextLinkCursor(h2); got != "0:100:0" {
		t.Fatalf("%q", got)
	}
	// results="false" means there is nothing on the next page
	h3 := http.Header{}
	h3.Add("Link", `<https://sentry.io/api/0/x/?&cursor=0:0:1>; rel="previous"; results="false"; cursor="0:0:1", <https://sentry.io/api/0/x/?&cursor=0:200:0>; rel="next"; results="false"; cursor="0:200:0"`)
	if got := nextLinkCursor(h3); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestAPIBaseIsHTTPSOrLoopback(t *testing.T) {
	for _, ok := range []string{"https://sentry.example.com", "https://sentry.example.com/", "http://127.0.0.1:8080", "http://localhost:9", "http://[::1]:5"} {
		if _, err := CheckAPIBase(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://sentry.example.com", "ftp://x", "https://u:p@x", "https://x/?q=1", "https://x/‮", "javascript:alert(1)"} {
		if _, err := CheckAPIBase(bad); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestVendorErrorCodesNeverEchoAValueThatCouldBeACredential(t *testing.T) {
	if got := vendorCode([]byte(`{"code":"rate_limited"}`)); got != "rate_limited" {
		t.Fatal(got)
	}
	for _, b := range []string{`{"code":"lin_api_Zk3Pq9TvL2mXw8RaB5nYd7CjHs4UeF6g"}`, `{"error":"has spaces in it"}`, `not json`} {
		if got := vendorCode([]byte(b)); got != "" {
			t.Fatalf("%s -> %q", b, got)
		}
	}
}
