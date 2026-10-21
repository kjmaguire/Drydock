package providers

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HeaderWindow is one quota window read from response headers.
type HeaderWindow struct {
	Kind      WindowKind
	Limit     float64
	Remaining float64
	ResetAt   time.Time // zero when the headers carry no reset
}

// ParseRateLimitHeaders reads the rate-limit headers a provider sends on a response (docs/phase0/model-providers.md F):
// Anthropic anthropic-ratelimit-{requests,tokens,input-tokens,output-tokens}-{limit,remaining,reset} (reset RFC 3339),
// and the OpenAI-style x-ratelimit-{limit,remaining,reset}-{requests,tokens} (reset a duration such as "6m0s").
// OpenRouter sends none of these on success (only on errors), and some OpenAI-compatible hosts send odd values, so
// every field is optional and unparsable values are skipped.
func ParseRateLimitHeaders(h http.Header, now time.Time) []HeaderWindow {
	var out []HeaderWindow
	for _, a := range []struct {
		prefix string
		kind   WindowKind
	}{
		{"requests", WindowRPM}, {"tokens", WindowTPM}, {"input-tokens", WindowITPM}, {"output-tokens", WindowOTPM},
	} {
		lim, ok1 := num(h.Get("anthropic-ratelimit-" + a.prefix + "-limit"))
		rem, ok2 := num(h.Get("anthropic-ratelimit-" + a.prefix + "-remaining"))
		if !ok1 || !ok2 {
			continue
		}
		w := HeaderWindow{Kind: a.kind, Limit: lim, Remaining: rem}
		if t, err := time.Parse(time.RFC3339, h.Get("anthropic-ratelimit-"+a.prefix+"-reset")); err == nil {
			w.ResetAt = t.UTC()
		}
		out = append(out, w)
	}
	for _, a := range []struct {
		suffix string
		kind   WindowKind
	}{{"requests", WindowRPM}, {"tokens", WindowTPM}} {
		lim, ok1 := num(h.Get("x-ratelimit-limit-" + a.suffix))
		rem, ok2 := num(h.Get("x-ratelimit-remaining-" + a.suffix))
		if !ok1 || !ok2 || lim <= 0 {
			continue
		}
		w := HeaderWindow{Kind: a.kind, Limit: lim, Remaining: rem}
		if d, ok := duration(h.Get("x-ratelimit-reset-" + a.suffix)); ok {
			w.ResetAt = now.Add(d)
		}
		out = append(out, w)
	}
	return out
}

// RetryAfter returns how long the provider asks the caller to wait: retry-after-ms, then Retry-After (seconds or an
// HTTP date), then the OpenRouter error header X-RateLimit-Reset (epoch milliseconds). Treat it as a minimum.
func RetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if v, ok := num(h.Get("retry-after-ms")); ok && v >= 0 {
		return time.Duration(v * float64(time.Millisecond)), true
	}
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, ok := num(v); ok && secs >= 0 {
			return time.Duration(secs * float64(time.Second)), true
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := t.Sub(now); d > 0 {
				return d, true
			}
			return 0, true
		}
	}
	if v, ok := num(h.Get("X-RateLimit-Reset")); ok && v > 1e12 { // epoch ms; smaller numbers are not this header's shape
		if d := time.UnixMilli(int64(v)).Sub(now); d > 0 {
			return d, true
		}
	}
	return 0, false
}

func num(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// duration parses "1s", "6m0s", "20ms", or a bare number of seconds.
func duration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, true
	}
	if f, ok := num(s); ok && f >= 0 {
		return time.Duration(f * float64(time.Second)), true
	}
	return 0, false
}
