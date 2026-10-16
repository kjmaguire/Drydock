package llm

import (
	"net/http"
	"testing"
	"time"
)

var now0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// TestClassifyHTTP covers the error shapes of docs/phase0/model-providers.md (OpenRouter A.3, Anthropic D, Gemini C/F,
// OpenAI) as response bodies a provider sends; they are wire-format fixtures, not captures from live services.
func TestClassifyHTTP(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		h          http.Header
		body       string
		kind       ErrorKind
		retryAfter time.Duration
		hasRetry   bool
		daily      bool
	}{
		{"openai rate limit with retry-after", 429, hdr("Retry-After", "2"), `{"error":{"message":"Rate limit reached","type":"requests","code":"rate_limit_exceeded"}}`, KindRateLimit, 2 * time.Second, true, false},
		{"openai insufficient_quota", 429, nil, `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`, KindQuota, 0, false, false},
		{"openrouter 429 with epoch-ms reset header", 429, hdr("X-RateLimit-Reset", "1791460812000"), `{"error":{"message":"Rate limit exceeded","code":429}}`, KindRateLimit, 12 * time.Second, true, false},
		{"openrouter 402 credits", 402, nil, `{"error":{"code":402,"message":"Insufficient credits. Add more using https://openrouter.ai/credits"}}`, KindQuota, 0, false, false},
		{"openrouter 402 in-flight budget", 402, hdr("Retry-After", "5"), `{"error":{"code":402,"message":"x","metadata":{"limit_source":"openrouter_in_flight_budget"}}}`, KindRateLimit, 5 * time.Second, true, false},
		{"openrouter 404 no endpoint for data policy", 404, nil, `{"error":{"code":404,"message":"No endpoints found matching your data policy (Zero data retention). Configure: https://openrouter.ai/settings/privacy"}}`, KindNoEndpoint, 0, false, false},
		{"openrouter 503 no endpoints", 503, nil, `{"error":{"code":503,"message":"No endpoints found for x"}}`, KindNoEndpoint, 0, false, false},
		{"plain 503", 503, hdr("Retry-After", "1"), `{"error":{"message":"overloaded"}}`, KindOverloaded, time.Second, true, false},
		{"anthropic 529", 529, nil, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, KindOverloaded, 0, false, false},
		{"anthropic 429 retry-after", 429, hdr("retry-after", "30"), `{"type":"error","error":{"type":"rate_limit_error","message":"rate"}}`, KindRateLimit, 30 * time.Second, true, false},
		{"anthropic spend limit has no retry-after", 429, nil, `{"type":"error","error":{"type":"rate_limit_error","message":"cap","details":{"error_code":"enforced_spend_limit_reached"}}}`, KindQuota, 0, false, false},
		{"anthropic covered model needs retention", 400, nil, `{"type":"error","error":{"type":"invalid_request_error","message":"In order to access this model, your organization or workspace must have data retention enabled."}}`, KindRetention, 0, false, false},
		{"gemini per-day quota ignores the short retryDelay", 429, nil, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"27s"}]}}`, KindQuota, 27 * time.Second, true, true},
		{"gemini per-minute quota uses retryDelay", 429, nil, `[{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier"}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"12s"}]}}]`, KindRateLimit, 12 * time.Second, true, false},
		{"gemini invalid key is a 400", 400, nil, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`, KindAuth, 0, false, false},
		{"401", 401, nil, `{"error":{"message":"Incorrect API key provided"}}`, KindAuth, 0, false, false},
		{"403", 403, nil, `{"error":"forbidden"}`, KindAuth, 0, false, false},
		{"400 invalid", 400, nil, `{"error":{"message":"bad json"}}`, KindInvalid, 0, false, false},
		{"422", 422, nil, `{"detail":"x"}`, KindInvalid, 0, false, false},
		{"500", 500, nil, `not json at all`, KindServer, 0, false, false},
		{"502", 502, nil, ``, KindServer, 0, false, false},
		{"504", 504, nil, ``, KindServer, 0, false, false},
		{"408", 408, nil, ``, KindServer, 0, false, false},
		{"string error body", 500, nil, `{"error":"boom"}`, KindServer, 0, false, false},
	}
	for _, tc := range cases {
		e := ClassifyHTTP("p", tc.status, tc.h, []byte(tc.body), now0)
		if e.Kind != tc.kind || e.Status != tc.status || e.HasRetryAfter != tc.hasRetry || e.RetryAfter != tc.retryAfter || e.DailyQuota != tc.daily {
			t.Errorf("%s: got kind=%s retryAfter=%v(%v) daily=%v, want %s %v(%v) %v", tc.name, e.Kind, e.RetryAfter, e.HasRetryAfter, e.DailyQuota, tc.kind, tc.retryAfter, tc.hasRetry, tc.daily)
		}
	}
}

func TestKindPolicies(t *testing.T) {
	retry := map[ErrorKind]bool{KindRateLimit: true, KindOverloaded: true, KindServer: true, KindNetwork: true}
	fallback := map[ErrorKind]bool{KindInvalid: false, KindStream: false}
	for _, k := range []ErrorKind{KindRateLimit, KindQuota, KindOverloaded, KindServer, KindNetwork, KindAuth, KindInvalid, KindNoEndpoint, KindRetention, KindStream} {
		if k.Retryable() != retry[k] {
			t.Errorf("%s retryable = %v", k, k.Retryable())
		}
		want, ok := fallback[k]
		if !ok {
			want = true
		}
		if k.FallbackEligible() != want {
			t.Errorf("%s fallback eligible = %v", k, k.FallbackEligible())
		}
	}
}

func TestManifestCeiling(t *testing.T) {
	pub := ManifestEntry{SourceType: "doc", Sensitivity: SensitivityPublicDocs}
	syn := ManifestEntry{SourceType: "probe", Sensitivity: SensitivitySynthetic}
	priv := ManifestEntry{SourceType: "diff", SourceRef: "abc", Sensitivity: SensitivityPrivate}
	untagged := ManifestEntry{SourceType: "x"}
	var nilM *Manifest
	cases := []struct {
		name string
		m    *Manifest
		want Sensitivity
	}{
		{"nil", nilM, SensitivityPrivate},
		{"empty", &Manifest{}, SensitivityPrivate},
		{"public", &Manifest{Entries: []ManifestEntry{pub}}, SensitivityPublicDocs},
		{"synthetic", &Manifest{Entries: []ManifestEntry{syn}}, SensitivityPublicDocs},
		{"public and synthetic", &Manifest{Entries: []ManifestEntry{pub, syn}}, SensitivityPublicDocs},
		{"one private", &Manifest{Entries: []ManifestEntry{pub, priv}}, SensitivityPrivate},
		{"untagged", &Manifest{Entries: []ManifestEntry{pub, untagged}}, SensitivityPrivate},
	}
	for _, tc := range cases {
		if got := tc.m.Ceiling(); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	if got := (&Manifest{Entries: []ManifestEntry{pub, priv, untagged}}).PrivateSources(); len(got) != 2 || got[0] != "diff:abc" || got[1] != "x" {
		t.Errorf("private sources %v", got)
	}
}
