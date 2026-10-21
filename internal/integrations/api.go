package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// API is an authenticated client for one connection's vendor API. Exactly one of HTTP (Linear, Notion, Sentry, Aikido) and GH
// (GitHub, the user's own gh) is set.
type API struct {
	Conn Connection
	HTTP *apiClient
	GH   host.API
}

// apiClient calls a vendor's HTTP API: base URL, the Authorization header, fixed headers (Notion-Version), the documented rate
// limit, and plain-language failures. Response bodies are untrusted data: they are parsed, never logged, and the vendor's error
// text reaches the person only through the classified message (secret-scanned by the service).
type apiClient struct {
	kind    string
	base    string // scheme://host[:port][/prefix], no trailing slash
	hc      *http.Client
	auth    func(ctx context.Context, renew bool) (string, error) // the Authorization header value; renew after a 401
	renew   bool                                                  // the credential can be renewed (Aikido's client credentials)
	headers http.Header
	lim     *limiter
	now     func() time.Time
	maxBody int64
}

// apiResponse is a decoded HTTP answer.
type apiResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

const userAgent = "Drydock (desktop app; https://github.com/kjmaguire/drydock)"

// CheckAPIBase accepts an API base URL: https, or http only on a loopback address (local fixtures and test servers). A
// credential never goes to a plain-http remote host.
func CheckAPIBase(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	if cl, _ := stripUnsafe(raw); cl != raw || len(raw) > 300 || strings.ContainsAny(raw, " \t\n<>\"") {
		return "", errf(ErrBadRequest, "the API URL is not a valid URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errf(ErrBadRequest, "the API URL must look like https://host[/path]")
	}
	switch u.Scheme {
	case "https":
	case "http":
		h := u.Hostname()
		if ip := net.ParseIP(h); !(h == "localhost" || (ip != nil && ip.IsLoopback())) {
			return "", errf(ErrBadRequest, "the API URL must use https (plain http is accepted only for a server on this computer)")
		}
	default:
		return "", errf(ErrBadRequest, "the API URL must use https")
	}
	return raw, nil
}

// do sends one request. path starts with "/"; q and body may be nil. A 401 on a renewable credential is retried once with a
// renewed one.
func (c *apiClient) do(ctx context.Context, method, path string, q url.Values, body any) (*apiResponse, error) {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	for attempt := 0; ; attempt++ {
		if err := c.lim.wait(ctx, c.kind); err != nil {
			return nil, err
		}
		authz, err := c.auth(ctx, attempt > 0)
		if err != nil {
			return nil, err
		}
		var rd io.Reader
		if raw != nil {
			rd = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return nil, err
		}
		for k, vs := range c.headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		if raw != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			traceCall(ctx, method, path, 0)
			return nil, Classify(c.kind, err, "")
		}
		traceCall(ctx, method, path, resp.StatusCode)
		b, rerr := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
		resp.Body.Close()
		if rerr != nil {
			return nil, Classify(c.kind, rerr, "")
		}
		if int64(len(b)) > c.maxBody {
			return nil, &Failure{Code: CodeSchemaDrift, Cause: errors.New("response too large"),
				Message: vendor(c.kind) + " sent a response larger than Drydock accepts (" + fmt.Sprint(c.maxBody>>20) + " MB). Narrow the project's scope."}
		}
		if until := RateHeaders(c.kind, resp.Header, resp.StatusCode, c.now()); !until.IsZero() {
			c.lim.hold(until)
		}
		r := &apiResponse{Status: resp.StatusCode, Header: resp.Header, Body: b}
		if resp.StatusCode == http.StatusUnauthorized && c.renew && attempt == 0 {
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return r, nil
		}
		return r, c.statusFailure(r)
	}
}

// getJSON / sendJSON decode a successful response into out; a body that does not decode is schema drift.
func (c *apiClient) json(ctx context.Context, method, path string, q url.Values, body, out any) (*apiResponse, error) {
	r, err := c.do(ctx, method, path, q, body)
	if err != nil {
		return r, err
	}
	if out != nil && len(bytes.TrimSpace(r.Body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(r.Body))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return r, schemaDrift(c.kind, fmt.Errorf("%s %s: %w", method, path, err))
		}
	}
	return r, nil
}

// a vendor error code is letters and underscores (rate_limited, object_not_found, RATELIMITED): anything else, which could be
// echoed request data or a credential, is not shown
var vendorCodeRe = regexp.MustCompile(`^[A-Za-z_]{1,40}$`)

// vendorCode extracts a short machine code from an error body ({"code": "unauthorized"}, {"error": "invalid_grant"}), never
// free text: a body is untrusted and may echo request data.
func vendorCode(b []byte) string {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	for _, k := range []string{"code", "error", "error_code", "type"} {
		if s, ok := m[k].(string); ok && vendorCodeRe.MatchString(s) {
			return s
		}
	}
	return ""
}

func (c *apiClient) statusFailure(r *apiResponse) *Failure {
	v := vendor(c.kind)
	code := vendorCode(r.Body)
	suffix := fmt.Sprintf(" (HTTP %d%s)", r.Status, map[bool]string{true: ", " + code, false: ""}[code != ""])
	switch {
	case r.Status == http.StatusTooManyRequests || code == "rate_limited" || code == "RATELIMITED":
		f := &Failure{Code: CodeRateLimited, Cause: ErrRateLimited, Message: v + " asked Drydock to slow down (rate limit). Drydock will wait and try again by itself."}
		if until := RateHeaders(c.kind, r.Header, http.StatusTooManyRequests, c.now()); !until.IsZero() {
			f.RetryAfter = until.Sub(c.now())
		}
		return f
	case r.Status == http.StatusUnauthorized:
		return &Failure{Code: CodeUnauthorized, Message: v + " rejected the credential" + suffix + ". It may have been revoked or expired. Reconnect " + v + " on the Integrations page."}
	case r.Status == http.StatusForbidden:
		// the credential is valid (that would be a 401) but may not read this resource: a scope or sharing problem of this
		// request, not of the connection
		return &Failure{Code: CodeForbidden, Message: v + " refused the request" + suffix + ": the credential lacks a permission or the resource is not shared with it. Check its scopes, or reconnect " + v + "."}
	case r.Status == http.StatusNotFound:
		return &Failure{Code: CodeNotFound, Message: v + " did not find what Drydock asked for" + suffix + ". Check the project's settings for this integration."}
	case r.Status >= 500:
		return &Failure{Code: CodeUnreachable, Message: v + " had a server error" + suffix + ". Existing items are kept; Drydock will retry."}
	}
	return &Failure{Code: CodeOther, Message: v + " did not accept the request" + suffix + "."}
}

// ---- Link header (Sentry, RFC 8288) ----

var linkPartRe = regexp.MustCompile(`<([^>]*)>((?:\s*;\s*[a-zA-Z_]+\s*=\s*"[^"]*")*)`)
var linkParamRe = regexp.MustCompile(`([a-zA-Z_]+)\s*=\s*"([^"]*)"`)

// nextLinkCursor returns the cursor of the rel="next" link when it says results="true" (Sentry: cursors are always returned
// for both directions, and results says whether the next page has anything).
func nextLinkCursor(h http.Header) string {
	for _, v := range h.Values("Link") {
		for _, m := range linkPartRe.FindAllStringSubmatch(v, -1) {
			params := map[string]string{}
			for _, p := range linkParamRe.FindAllStringSubmatch(m[2], -1) {
				params[strings.ToLower(p[1])] = p[2]
			}
			if params["rel"] != "next" || params["results"] != "true" {
				continue
			}
			if c := params["cursor"]; c != "" {
				return c
			}
			if u, err := url.Parse(m[1]); err == nil {
				return u.Query().Get("cursor")
			}
		}
	}
	return ""
}
