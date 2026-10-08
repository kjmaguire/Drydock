package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
)

// Sentinels for errors.Is.
var (
	ErrDataClass         = errors.New("llm: data_class violation")
	ErrQuota             = errors.New("llm: quota exhausted")
	ErrSecretBlocked     = errors.New("llm: outbound prompt contains a secret")
	ErrProviderUnavailable = errors.New("llm: provider unavailable")
)

// DataClassError is returned when a request whose context manifest holds private (or untagged) sources targets a
// trainable provider or model. Reaching this error is the guard working; reaching the provider would be a bug.
type DataClassError struct {
	Provider, Model string
	Target          providers.DataClass // the class that forced the refusal
	Sources         []string            // the private manifest sources
	Reason          string
}

func (e *DataClassError) Error() string {
	r := e.Reason
	if r == "" {
		r = fmt.Sprintf("%s target cannot receive private context", e.Target)
	}
	return fmt.Sprintf("llm: refused to send private context to %s/%s: %s (sources: %s)", e.Provider, e.Model, r, strings.Join(e.Sources, ", "))
}
func (e *DataClassError) Is(t error) bool { return t == ErrDataClass }

// QuotaError means the target cannot take the call before RetryAt (zero when unknown): a quota window is exhausted,
// or the provider asked for a wait longer than the backoff ceiling, or the account has no credit left.
type QuotaError struct {
	Provider, Model string
	RetryAt         time.Time
	Scope           string // window kind, "retry-after", "credits", "spend_limit", "daily_quota"
	Cause           error
}

func (e *QuotaError) Error() string {
	s := fmt.Sprintf("llm: quota exhausted on %s/%s (%s)", e.Provider, e.Model, e.Scope)
	if !e.RetryAt.IsZero() {
		s += ", retry at " + e.RetryAt.UTC().Format(time.RFC3339)
	}
	return s
}
func (e *QuotaError) Is(t error) bool { return t == ErrQuota }
func (e *QuotaError) Unwrap() error   { return e.Cause }

// Finding is a secret scanner finding. It never carries the secret itself.
type Finding struct {
	Rule  string `json:"rule"`
	Where string `json:"where,omitempty"` // filled by the Wrapper: system[0], messages[3], tools[1]
}

// SecretError is returned when the scanner found secrets in the outbound request.
type SecretError struct{ Findings []Finding }

func (e *SecretError) Error() string {
	var rules []string
	for _, f := range e.Findings {
		rules = append(rules, f.Rule+"@"+f.Where)
	}
	return "llm: outbound prompt blocked, secrets found: " + strings.Join(rules, ", ")
}
func (e *SecretError) Is(t error) bool { return t == ErrSecretBlocked }

// UnavailableError means the target provider cannot be used (disabled, unknown).
type UnavailableError struct {
	Provider string
	Reason   string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("llm: provider %s unavailable: %s", e.Provider, e.Reason)
}
func (e *UnavailableError) Is(t error) bool { return t == ErrProviderUnavailable }

// ErrorKind classifies a provider error.
type ErrorKind string

const (
	KindRateLimit  ErrorKind = "rate_limit"        // temporary 429; wait and retry
	KindQuota      ErrorKind = "quota"             // credits, spend cap or daily quota: retrying will not help
	KindOverloaded ErrorKind = "overloaded"        // 529, 503
	KindServer     ErrorKind = "server"            // 5xx, timeouts
	KindNetwork    ErrorKind = "network"           // transport failure
	KindAuth       ErrorKind = "auth"              // key rejected
	KindInvalid    ErrorKind = "invalid"           // the request itself is wrong
	KindNoEndpoint ErrorKind = "no_endpoint"       // OpenRouter: no endpoint satisfies the data policy (fail closed)
	KindRetention  ErrorKind = "retention_needed"  // Anthropic Covered Models need data retention enabled
	KindStream     ErrorKind = "stream_interrupted" // failed after output was delivered
)

// Retryable reports whether waiting and repeating the same request can succeed.
func (k ErrorKind) Retryable() bool {
	return k == KindRateLimit || k == KindOverloaded || k == KindServer || k == KindNetwork
}

// FallbackEligible reports whether another provider might succeed where this one failed. A malformed request fails
// everywhere, so it is not eligible.
func (k ErrorKind) FallbackEligible() bool { return k != KindInvalid && k != KindStream }

// APIError is a classified provider error.
type APIError struct {
	Provider      string
	Status        int
	Kind          ErrorKind
	Code, Message string
	Header        http.Header
	RetryAfter    time.Duration
	HasRetryAfter bool
	DailyQuota    bool // the exhausted quota resets daily (Gemini PerDay quota ids): stop retrying even if retryDelay is short
	Cause         error
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("llm: provider %s returned HTTP %d (%s)", e.Provider, e.Status, e.Kind)
	if e.Message != "" {
		m := e.Message
		if len(m) > 300 {
			m = m[:300] + "..."
		}
		s += ": " + m
	}
	return s
}
func (e *APIError) Unwrap() error { return e.Cause }

// Hints that identify error shapes (OpenRouter, Anthropic, Gemini; docs/phase0/model-providers.md A.3, D, F).
const (
	hintNoEndpoint = "no endpoints found"
	hintDataPolicy = "data policy"
	hintRetention  = "data retention enabled"
)

// ClassifyHTTP turns a non-2xx response into an APIError. body is the (possibly truncated) response body.
func ClassifyHTTP(provider string, status int, h http.Header, body []byte, now time.Time) *APIError {
	e := &APIError{Provider: provider, Status: status, Header: h}
	m := errorMap(body)
	e.Message, _ = m["message"].(string)
	e.Code = anyString(m["code"])
	typ, _ := m["type"].(string)
	lower := strings.ToLower(e.Message)
	if d, ok := providers.RetryAfter(h, now); ok {
		e.RetryAfter, e.HasRetryAfter = d, true
	}
	var limitSource string
	if md, ok := m["metadata"].(map[string]any); ok {
		limitSource, _ = md["limit_source"].(string)
	}
	gemQuotaID, gemDelay := geminiDetails(m["details"])
	if !e.HasRetryAfter && gemDelay > 0 {
		e.RetryAfter, e.HasRetryAfter = gemDelay, true
	}
	switch {
	case e.Code == "insufficient_quota" || typ == "insufficient_quota":
		e.Kind = KindQuota
	case detailsErrorCode(m["details"]) == "enforced_spend_limit_reached":
		e.Kind = KindQuota
	case strings.Contains(gemQuotaID, "PerDay"):
		e.Kind, e.DailyQuota = KindQuota, true
	case status == 402 && limitSource == "openrouter_in_flight_budget":
		e.Kind = KindRateLimit
	case status == 402:
		e.Kind = KindQuota
	case status == 429:
		e.Kind = KindRateLimit
	case status == 529:
		e.Kind = KindOverloaded
	case (status == 404 || status == 503) && (strings.Contains(lower, hintNoEndpoint) || strings.Contains(lower, hintDataPolicy)):
		e.Kind = KindNoEndpoint
	case status == 503:
		e.Kind = KindOverloaded
	case status == 500 || status == 502 || status == 504 || status == 408:
		e.Kind = KindServer
	case status == 401 || status == 403:
		e.Kind = KindAuth
	case status == 400 && strings.Contains(lower, hintRetention):
		e.Kind = KindRetention
	case status == 400 && (strings.Contains(lower, "api key") || strings.Contains(lower, "api_key")):
		e.Kind = KindAuth // Gemini answers an invalid key with 400, not 401 (MP C)
	case status >= 500:
		e.Kind = KindServer
	default:
		e.Kind = KindInvalid
	}
	return e
}

// errorMap extracts the "error" object from the common error body shapes: {"error":{...}}, [{"error":{...}}],
// {"error":"text"}.
func errorMap(body []byte) map[string]any {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return map[string]any{}
	}
	if arr, ok := v.([]any); ok && len(arr) > 0 {
		v = arr[0]
	}
	top, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	switch e := top["error"].(type) {
	case map[string]any:
		return e
	case string:
		return map[string]any{"message": e}
	}
	return top
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%d", int(t))
	}
	return ""
}

// detailsErrorCode reads Anthropic's error.details.error_code.
func detailsErrorCode(d any) string {
	if m, ok := d.(map[string]any); ok {
		s, _ := m["error_code"].(string)
		return s
	}
	return ""
}

// geminiDetails reads the QuotaFailure quotaId and RetryInfo retryDelay from a Gemini 429 body's details array.
func geminiDetails(d any) (quotaID string, delay time.Duration) {
	arr, ok := d.([]any)
	if !ok {
		return "", 0
	}
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if vs, ok := m["violations"].([]any); ok {
			for _, v := range vs {
				if vm, ok := v.(map[string]any); ok {
					if id, _ := vm["quotaId"].(string); id != "" && quotaID == "" {
						quotaID = id
					}
				}
			}
		}
		if rd, _ := m["retryDelay"].(string); rd != "" {
			if dur, err := time.ParseDuration(rd); err == nil {
				delay = dur
			}
		}
	}
	return quotaID, delay
}
