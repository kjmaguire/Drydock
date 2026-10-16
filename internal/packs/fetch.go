package packs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// UserAgent is sent on every crawl request. The robots.txt group named DrydockBot applies to it.
const UserAgent = "DrydockBot/1 (+local docs indexer; user-configured sources only)"

// Fetch errors callers react to.
var (
	ErrHostNotAllowed = errors.New("host is not in the pack's allowed_hosts")
	ErrDeniedPath     = errors.New("path is in the pack's deny_paths")
	ErrRobots         = errors.New("robots.txt disallows this path")
	ErrNoSignal       = errors.New("robots.txt does not grant the required Content-Signal")
	ErrTooLarge       = errors.New("page exceeds crawl.max_page_bytes")
	ErrContentType    = errors.New("content type is not text")
	ErrBlockedAddr    = errors.New("address is not routable on the public internet (SSRF protection)")
	ErrRedirect       = errors.New("redirect refused")
)

// FetchPolicy bounds one crawl. Every limit is enforced in the fetcher, not left to callers.
type FetchPolicy struct {
	AllowedHosts  []string
	DenyPaths     []string
	RequireSignal string // "" | "ai-input" | "ai-train" | "search"
	RPS           float64
	MaxBytes      int64
	Timeout       time.Duration
	MaxRedirects  int // default 3
	// AllowLoopback permits http:// and loopback addresses for the allowed hosts. Used by tests and by a deliberately local
	// mirror; the product's crawler leaves it false. Private ranges, link-local and metadata addresses stay blocked.
	AllowLoopback bool
	// NoRobots skips the robots.txt and content-signal checks. It is for API endpoints a human configured explicitly
	// (skill registries on api.github.com), which are not crawls; page crawling never sets it.
	NoRobots bool
	// UseSystemProxy sends requests through HTTP(S)_PROXY. The address check cannot see the final address then (the proxy
	// resolves it), so it is off by default.
	UseSystemProxy bool
}

// Conditional carries validators from an earlier fetch.
type Conditional struct{ ETag, LastModified string }

// Response is a fetched document.
type Response struct {
	URL          string // final URL after same-host redirects
	Status       int
	Body         []byte
	ContentType  string
	ETag         string
	LastModified string
	NotModified  bool
}

// Fetcher is the allowlisted, SSRF-safe, polite HTTP client packs use. It checks, in order: the URL (https, no credentials,
// allowed host, not a denied path), robots.txt and the content signal, the rate limit, then dials with a connect-time check
// that the address is public. Redirects must stay on the original host and are re-checked. Bodies and time are capped.
type Fetcher struct {
	policy FetchPolicy
	client *http.Client
	ua     string

	rmu    sync.Mutex
	robots map[string]*Robots // host -> parsed (cached for the life of the Fetcher: one refresh)
	rate   struct {
		mu   sync.Mutex
		next time.Time
	}
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// NewFetcher builds a Fetcher. base may be nil (http.DefaultTransport settings are copied); tests can inject a transport.
func NewFetcher(p FetchPolicy, base http.RoundTripper) *Fetcher {
	if p.MaxBytes <= 0 {
		p.MaxBytes = 2 << 20
	}
	if p.Timeout <= 0 {
		p.Timeout = 20 * time.Second
	}
	if p.MaxRedirects <= 0 {
		p.MaxRedirects = 3
	}
	if p.RPS <= 0 {
		p.RPS = 2
	}
	f := &Fetcher{policy: p, ua: UserAgent, robots: map[string]*Robots{}, now: time.Now, sleep: sleepCtx}
	if base == nil {
		base = f.transport()
	}
	f.client = &http.Client{Transport: base, Timeout: p.Timeout}
	f.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > p.MaxRedirects {
			return fmt.Errorf("%w: more than %d redirects", ErrRedirect, p.MaxRedirects)
		}
		if !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
			return fmt.Errorf("%w: %s redirects to another host (%s)", ErrRedirect, via[0].URL.Host, req.URL.Host)
		}
		return f.checkURL(req.URL)
	}
	return f
}

func (f *Fetcher) transport() http.RoundTripper {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: f.dialControl}
	t := &http.Transport{
		DialContext: d.DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 8, MaxIdleConnsPerHost: 4,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: f.policy.Timeout,
	}
	if f.policy.UseSystemProxy {
		t.Proxy = http.ProxyFromEnvironment
	}
	return t
}

// dialControl runs on the resolved address just before connecting, so DNS rebinding and hosts that resolve to internal
// addresses are caught at the point of use.
func (f *Fetcher) dialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: %q is not an IP literal", ErrBlockedAddr, host)
	}
	if err := CheckPublicIP(ip, f.policy.AllowLoopback); err != nil {
		return err
	}
	return nil
}

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "100::/64", "2001:db8::/32", "fec0::/10",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// CheckPublicIP refuses addresses that are not publicly routable: loopback (unless allowLoopback), RFC 1918 and unique-local
// ranges, link-local (including the 169.254.169.254 metadata address), carrier-grade NAT, multicast, unspecified,
// documentation and reserved ranges. IPv4-mapped IPv6 addresses are judged as their IPv4 form.
func CheckPublicIP(ip net.IP, allowLoopback bool) error {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	} else if len(ip) == net.IPv6len && ip[0] == 0x20 && ip[1] == 0x02 { // 6to4 embeds an IPv4 address: judge that one
		return CheckPublicIP(net.IPv4(ip[2], ip[3], ip[4], ip[5]), allowLoopback)
	}
	switch {
	case ip.IsLoopback():
		if allowLoopback {
			return nil
		}
	case ip.IsPrivate(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsMulticast(), ip.IsUnspecified():
	default:
		for _, n := range blockedNets {
			if n.Contains(ip) {
				return fmt.Errorf("%w: %s", ErrBlockedAddr, ip)
			}
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrBlockedAddr, ip)
}

func (f *Fetcher) checkURL(u *url.URL) error {
	switch u.Scheme {
	case "https":
	case "http":
		if !(f.policy.AllowLoopback && IsLoopbackHost(u.Hostname())) {
			return fmt.Errorf("plain http is not allowed: %s", u.Redacted())
		}
	default:
		return fmt.Errorf("scheme %q is not allowed", u.Scheme)
	}
	if u.User != nil {
		return errors.New("URLs with credentials are refused")
	}
	if !HostAllowed(f.policy.AllowedHosts, u.Hostname()) {
		return fmt.Errorf("%w: %s", ErrHostNotAllowed, u.Hostname())
	}
	p := u.EscapedPath()
	for _, d := range f.policy.DenyPaths {
		if strings.HasPrefix(p, d) || strings.HasPrefix(u.Path, d) {
			return fmt.Errorf("%w: %s", ErrDeniedPath, d)
		}
	}
	return nil
}

// Get fetches rawURL. A 304 returns NotModified with no body. Non-2xx statuses are errors (HTTPError).
func (f *Fetcher) Get(ctx context.Context, rawURL string, cond Conditional) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}
	if !f.policy.NoRobots {
		if err := f.checkRobots(ctx, u); err != nil {
			return nil, err
		}
	}
	return f.do(ctx, u, cond, true, false, f.policy.MaxBytes, "")
}

// GetAPI fetches a document from an API endpoint with the API's own media type as Accept (GitHub's REST API answers 415 to
// the markdown and HTML types a page fetch asks for). The address, host, redirect, size and text checks are Get's, except that
// a media type the caller named in accept is taken as text (GitHub answers application/vnd.github.sha, the bare commit id,
// with that type); its body still may not hold a NUL byte.
func (f *Fetcher) GetAPI(ctx context.Context, rawURL, accept string) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}
	if !f.policy.NoRobots {
		if err := f.checkRobots(ctx, u); err != nil {
			return nil, err
		}
	}
	return f.do(ctx, u, Conditional{}, true, false, f.policy.MaxBytes, accept)
}

// GetBinary fetches a non-text document (a source tarball) with the same address, host, redirect and time checks and its own
// size cap. It exists for skill registries; pack crawling uses Get.
func (f *Fetcher) GetBinary(ctx context.Context, rawURL string, maxBytes int64) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}
	if !f.policy.NoRobots {
		if err := f.checkRobots(ctx, u); err != nil {
			return nil, err
		}
	}
	return f.do(ctx, u, Conditional{}, true, true, maxBytes, "")
}

// HTTPError is a non-success response.
type HTTPError struct {
	Status int
	URL    string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("GET %s: HTTP %d", e.URL, e.Status) }

func (f *Fetcher) do(ctx context.Context, u *url.URL, cond Conditional, polite, binary bool, maxBytes int64, accept string) (*Response, error) {
	if polite {
		if err := f.wait(ctx); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.ua)
	switch {
	case accept != "":
		req.Header.Set("Accept", accept)
	case binary:
		req.Header.Set("Accept", "application/gzip, application/x-gzip, application/octet-stream, */*;q=0.1")
	default:
		req.Header.Set("Accept", "text/markdown, text/plain;q=0.9, text/html;q=0.8, application/xml;q=0.7")
	}
	if cond.ETag != "" {
		req.Header.Set("If-None-Match", cond.ETag)
	}
	if cond.LastModified != "" {
		req.Header.Set("If-Modified-Since", cond.LastModified)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := &Response{URL: resp.Request.URL.String(), Status: resp.StatusCode, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), ContentType: resp.Header.Get("Content-Type")}
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		return out, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return out, &HTTPError{Status: resp.StatusCode, URL: u.String()}
	}
	if resp.ContentLength > maxBytes {
		return out, fmt.Errorf("%w: %d bytes", ErrTooLarge, resp.ContentLength)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return out, err
	}
	if int64(len(body)) > maxBytes {
		return out, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, maxBytes)
	}
	if !binary && !textual(out.ContentType, body) && (!askedFor(accept, out.ContentType) || bytes.IndexByte(body, 0) >= 0) {
		return out, fmt.Errorf("%w: %q", ErrContentType, out.ContentType)
	}
	out.Body = body
	return out, nil
}

// askedFor reports whether ct's media type is one the request's Accept header named (wildcards do not count).
func askedFor(accept, ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	if ct == "" {
		return false
	}
	for _, t := range strings.Split(accept, ",") {
		t = strings.ToLower(strings.TrimSpace(strings.SplitN(t, ";", 2)[0]))
		if t == ct && !strings.Contains(t, "*") {
			return true
		}
	}
	return false
}

func textual(ct string, body []byte) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch {
	case strings.HasPrefix(ct, "text/"), ct == "application/xml", ct == "application/xhtml+xml", ct == "application/x-markdown", ct == "application/json", strings.HasSuffix(ct, "+xml"), strings.HasSuffix(ct, "+json"):
	case ct == "" || ct == "application/octet-stream":
		if !strings.HasPrefix(http.DetectContentType(body), "text/") {
			return false
		}
	default:
		return false
	}
	for _, b := range body[:min(len(body), 4096)] {
		if b == 0 {
			return false
		}
	}
	return true
}

// wait enforces the polite request rate across all workers.
func (f *Fetcher) wait(ctx context.Context) error {
	f.rate.mu.Lock()
	now := f.now()
	at := f.rate.next
	if at.Before(now) {
		at = now
	}
	f.rate.next = at.Add(time.Duration(float64(time.Second) / f.policy.RPS))
	f.rate.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		return f.sleep(ctx, d)
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// robotsFor fetches and caches robots.txt for the URL's host. 404 and other 4xx mean "no rules"; a server error or an
// unreachable robots.txt means "disallow everything" (RFC 9309); the fetch itself passes the same address checks.
func (f *Fetcher) robotsFor(ctx context.Context, u *url.URL) (*Robots, error) {
	host := strings.ToLower(u.Host)
	f.rmu.Lock()
	r, ok := f.robots[host]
	f.rmu.Unlock()
	if ok {
		return r, nil
	}
	ru := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/robots.txt"}
	resp, err := f.do(ctx, ru, Conditional{}, true, false, 512<<10, "")
	switch {
	case err == nil:
		r = ParseRobots(string(resp.Body))
	case errors.As(err, new(*HTTPError)) && err.(*HTTPError).Status < 500:
		r = &Robots{signals: map[string]string{}}
	default:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrBlockedAddr) || errors.Is(err, ErrRedirect) {
			return nil, err // a policy refusal, not an unreachable server: keep the cause visible
		}
		r = &Robots{DisallowAll: true, signals: map[string]string{}}
	}
	f.rmu.Lock()
	f.robots[host] = r
	f.rmu.Unlock()
	return r, nil
}

func (f *Fetcher) checkRobots(ctx context.Context, u *url.URL) error {
	r, err := f.robotsFor(ctx, u)
	if err != nil {
		return err
	}
	if !r.Allowed(f.ua, u.RequestURI()) {
		return fmt.Errorf("%w: %s", ErrRobots, u.RequestURI())
	}
	if sig := f.policy.RequireSignal; sig != "" {
		if v, ok := r.Signal(sig); !ok || v != "yes" {
			return fmt.Errorf("%w: %s", ErrNoSignal, sig)
		}
	}
	return nil
}
