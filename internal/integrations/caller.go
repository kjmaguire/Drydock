package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/mcp"
)

// Result is a tool result as the app sees it. Its content is untrusted data.
type Result struct {
	Text       string
	Structured json.RawMessage
	IsError    bool
}

// Caller is the MCP client an integration talks through. The production implementation is the MCP gateway's AppUpstream
// (GatewayFactory); tests install in-memory or loopback fixtures behind the same interface.
type Caller interface {
	// Tools lists the tool names the server offers now. It proves the server is reachable and the token is accepted.
	Tools(ctx context.Context) ([]string, error)
	// Call calls one tool. A returned error means the server could not be reached or refused the session; a tool-level failure
	// is a Result with IsError.
	Call(ctx context.Context, tool string, args any) (Result, error)
}

// CallerFactory builds the Caller for a connection. write selects the endpoint for outbound writes (Linear's hosted server
// has a separate read-only endpoint, docs/mcp.md section 9); sync always asks for the read endpoint.
type CallerFactory interface {
	Caller(ctx context.Context, c Connection, write bool) (Caller, error)
}

// CallerFactoryFunc adapts a function.
type CallerFactoryFunc func(ctx context.Context, c Connection, write bool) (Caller, error)

// Caller implements CallerFactory.
func (f CallerFactoryFunc) Caller(ctx context.Context, c Connection, write bool) (Caller, error) {
	return f(ctx, c, write)
}

// GatewayFactory is the product CallerFactory: it calls through the MCP gateway's pooled upstreams.
type GatewayFactory struct {
	Gateway *mcp.Gateway
	Catalog *mcp.Catalog // default: the built-in catalog
}

type gatewayCaller struct{ up *mcp.AppUpstream }

// builtinCatalog parses the embedded catalog once.
var builtinCatalog = sync.OnceValues(mcp.LoadBuiltin)

// Caller implements CallerFactory.
func (f GatewayFactory) Caller(ctx context.Context, c Connection, write bool) (Caller, error) {
	cat := f.Catalog
	if cat == nil {
		var err error
		if cat, err = builtinCatalog(); err != nil {
			return nil, err
		}
	}
	spec, err := ServerSpecFor(cat, c, write)
	if err != nil {
		return nil, err
	}
	up, err := f.Gateway.AppUpstream(ctx, spec)
	if err != nil {
		return nil, err
	}
	return gatewayCaller{up}, nil
}

func (g gatewayCaller) Tools(ctx context.Context) ([]string, error) { return g.up.Tools(ctx) }

func (g gatewayCaller) Call(ctx context.Context, tool string, args any) (Result, error) {
	r, err := g.up.Call(ctx, tool, args)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: r.Text, Structured: r.Structured, IsError: r.IsError}, nil
}

// ---- decoding tool results ----

// decode parses a tool result as JSON. MCP servers return JSON as a text block (or as structuredContent); anything else is
// schema drift from our point of view.
func decode(r Result) (any, error) {
	raw := bytes.TrimSpace(r.Structured)
	if len(raw) == 0 {
		raw = bytes.TrimSpace([]byte(r.Text))
	}
	if len(raw) == 0 {
		return nil, errors.New("the result is empty")
	}
	if raw[0] != '{' && raw[0] != '[' {
		// tolerate a fenced block or a one-line preface before the JSON
		if i := bytes.IndexAny(raw, "{["); i >= 0 && i < 200 {
			raw = bytes.TrimSpace(bytes.TrimSuffix(bytes.TrimSpace(raw[i:]), []byte("```")))
		} else {
			return nil, errors.New("the result is not JSON")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("the result is not valid JSON: %w", err)
	}
	return v, nil
}

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
		for _, k := range []string{"name", "title", "identifier", "key", "slug", "displayName", "value", "email", "id"} {
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
	case int32:
		return int64(x), true
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
	case map[string]any:
		return num(pick(x, "value", "count", "number"))
	}
	return 0, false
}

func strList(v any) []string {
	var out []string
	for _, e := range asList(v) {
		if s := str(e); s != "" {
			out = append(out, s)
		}
	}
	return out
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
	case json.Number, float64:
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

var (
	reUnauthorized = regexp.MustCompile(`(?i)\b(401|403)\b|unauthori[sz]ed|forbidden|invalid[ _-]?(api[ _-]?key|token|credentials?)|token (is )?(expired|revoked|invalid)|authentication (failed|required)|not authenticated|access denied|permission denied`)
	reRateLimited  = regexp.MustCompile(`(?i)\b429\b|rate[ _-]?limit|too many requests|throttl|quota exceeded|slow down`)
	reRetryAfter   = regexp.MustCompile(`(?i)retry[ _-]?after\D{0,12}(\d+)\s*(ms|milliseconds|seconds|sec|s|minutes|min|m)?\b`)
	reRetryInSecs  = regexp.MustCompile(`(?i)(?:try again|retry|wait)\D{0,20}(\d+)\s*(ms|milliseconds|seconds|sec|s|minutes|min|m)\b`)
)

func parseRetryAfter(s string) time.Duration {
	for _, re := range []*regexp.Regexp{reRetryAfter, reRetryInSecs} {
		m := re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		switch strings.ToLower(m[2]) {
		case "ms", "milliseconds":
			return time.Duration(n) * time.Millisecond
		case "m", "min", "minutes":
			return time.Duration(n) * time.Minute
		default:
			return time.Duration(n) * time.Second
		}
	}
	return 0
}

var vendorNames = map[string]string{KindLinear: "Linear", KindNotion: "Notion", KindSentry: "Sentry", KindAikido: "Aikido"}

func vendor(kind string) string {
	if n := vendorNames[kind]; n != "" {
		return n
	}
	return kind
}

// Classify turns a transport error or a tool-level error text into a Failure. Exactly one of err and toolText is meaningful.
func Classify(kind string, err error, toolText string) *Failure {
	v := vendor(kind)
	text := toolText
	if err != nil {
		text = err.Error()
	}
	f := &Failure{Cause: err}
	switch {
	case reRateLimited.MatchString(text) && !reUnauthorized.MatchString(text):
		f.Code = CodeRateLimited
		f.RetryAfter = parseRetryAfter(text)
		f.Message = v + " asked Drydock to slow down (rate limit). Drydock will wait and try again by itself."
	case reUnauthorized.MatchString(text):
		f.Code = CodeUnauthorized
		f.Message = v + " rejected the token. It may have been revoked, expired or lack access. Paste a new token under Connections > " + v + "."
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(text), "timeout") || strings.Contains(strings.ToLower(text), "timed out"):
		f.Code = CodeTimeout
		f.Message = v + " did not answer in time. Drydock will try again later."
	case err != nil && (errors.Is(err, mcp.ErrUpstreamUnavailable) || isNetErr(err)):
		f.Code = CodeUnreachable
		f.Message = "Drydock could not reach " + v + "'s MCP server (network error, the server is down, or the command is not installed). Existing items are kept; Drydock will retry."
	case err != nil:
		f.Code = CodeOther
		f.Message = "Syncing with " + v + " failed. Existing items are kept; Drydock will retry."
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
