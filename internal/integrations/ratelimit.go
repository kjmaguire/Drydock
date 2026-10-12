package integrations

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimit is a vendor's documented request budget for one credential. Drydock spends it evenly (a token bucket that refills
// at Requests per Per and holds at most Burst), so a long sync never runs into the limit, and it also obeys what the vendor
// says in its response headers (RateHeaders), which wins whenever it is stricter.
type RateLimit struct {
	Requests int           // per Per; 0 = no documented number (headers only)
	Per      time.Duration //
	Burst    int           // how many requests may go out back to back
	Doc      string        // the documentation the numbers come from
	Note     string        // the limit in words, as shown on the Integrations page
}

// The documented limits. Where a vendor publishes no number (Sentry) or the number is only known from a search snippet
// (Aikido), the note says so; the response headers are honored either way.
var (
	// Linear: "API key: 2,500 requests per hour per user; OAuth app: 5,000 requests per hour per user (or app user)", plus a
	// complexity budget reported in X-RateLimit-Complexity-* headers.
	limitLinearKey   = RateLimit{Requests: 2500, Per: time.Hour, Burst: 50, Doc: "https://linear.app/developers/rate-limiting", Note: "2,500 requests per hour per user with an API key; complexity points as reported by Linear's headers"}
	limitLinearOAuth = RateLimit{Requests: 5000, Per: time.Hour, Burst: 50, Doc: "https://linear.app/developers/rate-limiting", Note: "5,000 requests per hour per user with an OAuth sign-in; complexity points as reported by Linear's headers"}
	// Notion: "an average of three requests per second per integration"; 429 rate_limited with Retry-After.
	limitNotion = RateLimit{Requests: 3, Per: time.Second, Burst: 3, Doc: "https://developers.notion.com/reference/request-limits", Note: "an average of 3 requests per second per integration; Retry-After on 429"}
	// Sentry: "Each endpoint has its own maximum number of requests and window size" (fixed window per caller and endpoint, plus
	// a concurrency limit); the numbers are only in the X-Sentry-Rate-Limit-* headers of each response.
	limitSentry = RateLimit{Doc: "https://docs.sentry.io/api/ratelimits/", Note: "per endpoint and caller, published only in the X-Sentry-Rate-Limit-* headers, which Drydock follows"}
	// Aikido: 20 calls per minute per workspace (search snippet of apidocs.aikido.dev; the page could not be read).
	limitAikido = RateLimit{Requests: 20, Per: time.Minute, Burst: 5, Doc: "https://apidocs.aikido.dev/", Note: "20 requests per minute per workspace (from a snippet of Aikido's docs; Retry-After on 429)"}
	// GitHub: gh calls the REST API; "authenticated users can make 5,000 requests per hour"; gh reports a limit as an error
	// Drydock maps to a rate-limit backoff.
	limitGitHub = RateLimit{Doc: "https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api", Note: "5,000 requests per hour for a signed-in user (GitHub's primary limit, through gh)"}
)

// RateLimitFor returns the documented budget of a kind and authentication.
func RateLimitFor(kind, auth string) RateLimit {
	switch kind {
	case KindLinear:
		if auth == AuthOAuth {
			return limitLinearOAuth
		}
		return limitLinearKey
	case KindNotion:
		return limitNotion
	case KindSentry:
		return limitSentry
	case KindAikido:
		return limitAikido
	case KindGitHub:
		return limitGitHub
	}
	return RateLimit{}
}

// limiter spends a RateLimit evenly and holds requests while the vendor said to wait.
type limiter struct {
	mu     sync.Mutex
	rl     RateLimit
	tokens float64
	last   time.Time
	until  time.Time // the vendor said: nothing before this
	now    func() time.Time
	sleep  func(ctx context.Context, d time.Duration) error
	// maxWait bounds how long one request waits here; a longer wait fails as rate limited and the sync backs off instead
	// (so a manual Test or a shutdown is never stuck behind an hour-long window).
	maxWait time.Duration
}

func newLimiter(rl RateLimit) *limiter {
	return &limiter{rl: rl, tokens: float64(rl.Burst), now: time.Now, sleep: sleepCtx, maxWait: 30 * time.Second}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// wait reserves one request and sleeps until it may go out.
func (l *limiter) wait(ctx context.Context, kind string) error {
	l.mu.Lock()
	now := l.now()
	var d time.Duration
	if l.until.After(now) {
		d = l.until.Sub(now)
	}
	if l.rl.Requests > 0 && l.rl.Per > 0 {
		rate := float64(l.rl.Requests) / l.rl.Per.Seconds() // tokens per second
		if !l.last.IsZero() {
			l.tokens += now.Sub(l.last).Seconds() * rate
		}
		if max := float64(l.rl.Burst); l.tokens > max {
			l.tokens = max
		}
		l.last = now
		l.tokens--
		if l.tokens < 0 {
			if w := time.Duration(-l.tokens / rate * float64(time.Second)); w > d {
				d = w
			}
		}
	}
	if d > l.maxWait {
		if l.rl.Requests > 0 {
			l.tokens++ // the request is not sent: give its reservation back
		}
		l.mu.Unlock()
		return &Failure{Code: CodeRateLimited, RetryAfter: d, Cause: ErrRateLimited,
			Message: vendor(kind) + " asked Drydock to slow down (rate limit). Drydock will wait and try again by itself."}
	}
	l.mu.Unlock()
	return l.sleep(ctx, d)
}

// hold records that the vendor said not to send anything before t.
func (l *limiter) hold(t time.Time) {
	l.mu.Lock()
	if t.After(l.until) {
		l.until = t
	}
	l.mu.Unlock()
}

// RateHeaders reads a vendor's rate-limit response headers and returns the time before which no request should be sent
// (zero when the headers allow more requests now).
//
//   - Sentry: X-Sentry-Rate-Limit-Remaining and X-Sentry-Rate-Limit-Reset ("UTC seconds from epoch"), and the concurrency pair.
//   - Linear: X-RateLimit-Requests-Remaining / -Reset and X-RateLimit-Complexity-Remaining / -Reset (epoch milliseconds).
//   - Any vendor: Retry-After (seconds or an HTTP date) on a 429.
func RateHeaders(kind string, h http.Header, status int, now time.Time) time.Time {
	var until time.Time
	later := func(t time.Time) {
		if t.After(until) {
			until = t
		}
	}
	if status == http.StatusTooManyRequests {
		if d := retryAfterHeader(h.Get("Retry-After"), now); d > 0 {
			later(now.Add(d))
		}
	}
	switch kind {
	case KindSentry:
		if h.Get("X-Sentry-Rate-Limit-Remaining") == "0" {
			later(epochAny(h.Get("X-Sentry-Rate-Limit-Reset")))
		}
	case KindLinear:
		for _, p := range []string{"X-RateLimit-Requests", "X-RateLimit-Complexity"} {
			if rem := strings.TrimSpace(h.Get(p + "-Remaining")); rem != "" {
				if n, err := strconv.ParseFloat(rem, 64); err == nil && n <= 0 {
					later(epochAny(h.Get(p + "-Reset")))
				}
			}
		}
	}
	if until.After(now.Add(24 * time.Hour)) { // a nonsense reset is not obeyed for longer than a day
		until = now.Add(24 * time.Hour)
	}
	return until
}

// retryAfterHeader parses Retry-After: delta seconds or an HTTP date.
func retryAfterHeader(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 {
		return time.Duration(n * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// epochAny parses a Unix time in seconds or milliseconds.
func epochAny(v string) time.Time {
	v = strings.TrimSpace(v)
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return time.Time{}
	}
	if f > 1e12 {
		return time.UnixMilli(int64(f))
	}
	return time.Unix(int64(f), int64((f-float64(int64(f)))*1e9))
}
