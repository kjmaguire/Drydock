package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---- reading untyped JSON (vendor payloads are untrusted; every field is optional) ----

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// pick returns the first present key of m.
func pick(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// str renders a scalar, or the name/title/key/slug/value of an object, as a string.
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case map[string]any:
		for _, k := range []string{"name", "title", "identifier", "key", "slug", "displayName", "value", "id"} {
			if s := str(x[k]); s != "" {
				return s
			}
		}
	}
	return ""
}

func num(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := x.Float64(); err == nil {
			return int64(f), true
		}
	case float64:
		return int64(x), true
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

var timeLayouts = []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"}

// millis parses an RFC 3339 string or a Unix timestamp (seconds or milliseconds) into milliseconds.
func millis(v any) int64 {
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		for _, l := range timeLayouts {
			if t, err := time.Parse(l, x); err == nil {
				return t.UnixMilli()
			}
		}
		if n, ok := num(x); ok {
			return normalizeEpoch(n)
		}
	case json.Number, float64, int64, int:
		if n, ok := num(x); ok {
			return normalizeEpoch(n)
		}
	}
	return 0
}

func normalizeEpoch(n int64) int64 {
	if n > 0 && n < 100_000_000_000 { // seconds
		return n * 1000
	}
	return n
}

func clampText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// priorityTask maps the normalized priority words to Drydock's task priority (higher is more urgent).
func priorityTask(p string) int {
	switch strings.ToLower(p) {
	case "urgent", "critical", "fatal":
		return 4
	case "high", "error":
		return 3
	case "medium", "warning":
		return 2
	case "low", "info":
		return 1
	}
	return 0
}

// ---- classifying failures (plain language) ----

// Failure is a classified failure with a message a person can act on.
type Failure struct {
	Code       string
	Message    string
	RetryAfter time.Duration
	Cause      error
}

func (f *Failure) Error() string { return f.Message }
func (f *Failure) Unwrap() error { return f.Cause }

func schemaDrift(kind string, cause error) *Failure {
	return &Failure{Code: CodeSchemaDrift, Cause: cause,
		Message: vendor(kind) + " returned data in a shape Drydock does not recognize (the API may have changed). Existing items are kept and the connection is marked degraded."}
}

var (
	reUnauthorized = regexp.MustCompile(`(?i)\b401\b|unauthori[sz]ed|invalid[ _-]?(api[ _-]?key|token|credentials?)|token (is )?(expired|revoked|invalid)|authentication (failed|required)|not authenticated`)
	// a credential that works but may not do this one thing (a missing scope, a resource not shared with it)
	reForbidden   = regexp.MustCompile(`(?i)\b403\b|forbidden|access denied|permission denied|not accessible by|insufficient (scope|permission)s?`)
	reRateLimited = regexp.MustCompile(`(?i)\b429\b|rate[ _-]?limit|too many requests|throttl|quota exceeded|slow down`)
	reProxy       = regexp.MustCompile(`(?i)proxy|tunnel`)
)

var vendorNames = map[string]string{KindLinear: "Linear", KindNotion: "Notion", KindSentry: "Sentry", KindAikido: "Aikido", KindGitHub: "GitHub"}

func vendor(kind string) string {
	if n := vendorNames[kind]; n != "" {
		return n
	}
	return kind
}

// Classify turns a transport error, or an error text from a tool such as gh, into a Failure. Exactly one of err and text is
// meaningful.
func Classify(kind string, err error, text string) *Failure {
	var f0 *Failure
	if errors.As(err, &f0) {
		return f0
	}
	v := vendor(kind)
	if err != nil {
		text = err.Error()
	}
	f := &Failure{Cause: err}
	switch {
	case err != nil && reProxy.MatchString(text):
		// A refusal by a corporate or sandbox proxy is not the vendor rejecting the credential (a CONNECT 403 looks like one).
		f.Code = CodeUnreachable
		f.Message = "A proxy between Drydock and " + v + " refused the connection (" + strings.TrimSpace(firstLine(text)) + "). This is a network or proxy setting, not a problem with your credential."
	case reRateLimited.MatchString(text) && !reUnauthorized.MatchString(text) && !reForbidden.MatchString(text):
		f.Code = CodeRateLimited
		f.Message = v + " asked Drydock to slow down (rate limit). Drydock will wait and try again by itself."
	case reUnauthorized.MatchString(text):
		f.Code = CodeUnauthorized
		f.Message = v + " rejected the credential. It may have been revoked or expired. Reconnect " + v + " on the Integrations page."
	case reForbidden.MatchString(text):
		f.Code = CodeForbidden
		f.Message = v + " refused the request: the credential lacks a permission or the resource is not shared with it. Check its scopes, or reconnect " + v + "."
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(text), "timeout") || strings.Contains(strings.ToLower(text), "timed out"):
		f.Code = CodeTimeout
		f.Message = v + " did not answer in time. Drydock will try again later."
	case err != nil && isNetErr(err):
		f.Code = CodeUnreachable
		f.Message = "Drydock could not reach " + v + " (network error or the service is down). Existing items are kept; Drydock will retry."
	case err != nil:
		f.Code = CodeOther
		f.Message = "Talking to " + v + " failed. Existing items are kept; Drydock will retry."
	default:
		f.Code = CodeOther
		f.Message = v + " reported an error for this request. Existing items are kept; Drydock will retry."
	}
	return f
}

func isNetErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne)
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
