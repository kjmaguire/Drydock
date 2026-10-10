// Package egress is the network allowlist of a task sandbox (docs/sandbox.md): an HTTP proxy that lets the workspace reach the
// hosts on the project's allowlist and nothing else. It runs in its own small container, the only one of the task with a
// route out; the workspace and the services sit on an internal network and reach the outside only through it. Names are
// resolved here and the connection goes to the address that was checked, so a name that resolves to a private, loopback or
// link-local address (the human's machine, the engine's host gateway, a cloud metadata service) is refused unless that
// address itself is on the allowlist.
package egress

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options configure a Proxy.
type Options struct {
	// Allow lists host names ("registry.npmjs.org"), domains ("*.example.com": every name under example.com) and IP addresses.
	Allow []string
	// Ports are the destination ports allowed (default 80 and 443).
	Ports []int
	// Resolver resolves names (default net.DefaultResolver).
	Resolver interface {
		LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
	}
	// Log receives one decision per request (default: discarded).
	Log func(Decision)
	// DialTimeout bounds connecting upstream (default 15 seconds).
	DialTimeout time.Duration
}

// Decision is what the proxy did with one request; the proxy logs it as one JSON line so Drydock can show the human what was
// refused.
type Decision struct {
	Time    time.Time `json:"time"`
	Method  string    `json:"method"`
	Host    string    `json:"host"`
	Port    int       `json:"port"`
	Allowed bool      `json:"allowed"`
	Reason  string    `json:"reason,omitempty"`
}

// Proxy is the allowlist proxy. It implements http.Handler.
type Proxy struct {
	names   map[string]bool
	domains []string // "example.com" for "*.example.com"
	ips     map[netip.Addr]bool
	ports   map[int]bool
	opts    Options
	mu      sync.Mutex
}

// New builds a proxy from options. Invalid allowlist entries are an error.
func New(o Options) (*Proxy, error) {
	p := &Proxy{names: map[string]bool{}, ips: map[netip.Addr]bool{}, ports: map[int]bool{}, opts: o}
	for _, a := range o.Allow {
		a = strings.ToLower(strings.TrimSpace(a))
		switch {
		case a == "":
		case strings.HasPrefix(a, "*."):
			p.domains = append(p.domains, strings.TrimPrefix(a, "*."))
		default:
			if ip, err := netip.ParseAddr(a); err == nil {
				p.ips[ip.Unmap()] = true
			} else if strings.ContainsAny(a, "/:*@ ") {
				return nil, fmt.Errorf("egress: %q is not a host name, domain or address", a)
			} else {
				p.names[a] = true
			}
		}
	}
	if len(o.Ports) == 0 {
		o.Ports = []int{80, 443}
	}
	for _, port := range o.Ports {
		p.ports[port] = true
	}
	if p.opts.Resolver == nil {
		p.opts.Resolver = net.DefaultResolver
	}
	if p.opts.DialTimeout <= 0 {
		p.opts.DialTimeout = 15 * time.Second
	}
	return p, nil
}

// hostAllowed reports whether a name (or literal address) is on the allowlist.
func (p *Proxy) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if ip, err := netip.ParseAddr(host); err == nil {
		return p.ips[ip.Unmap()]
	}
	if p.names[host] {
		return true
	}
	for _, d := range p.domains {
		if strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// special reports addresses a sandbox must not reach by name: loopback, private, link-local, unique-local, carrier-grade NAT,
// unspecified and multicast. Such an address is reachable only when it is itself on the allowlist.
func special(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	return netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("192.0.0.0/24").Contains(ip) || netip.MustParsePrefix("198.18.0.0/15").Contains(ip)
}

// resolve checks host and port and returns the addresses the proxy may connect to, or why not.
func (p *Proxy) resolve(ctx context.Context, host string, port int) ([]netip.Addr, string) {
	if !p.ports[port] {
		return nil, fmt.Sprintf("port %d is not allowed", port)
	}
	if !p.hostAllowed(host) {
		return nil, "not on this project's allowlist"
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip.Unmap()}, "" // a literal address on the allowlist is allowed as it is, even a private one
	}
	addrs, err := p.opts.Resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, "the name does not resolve"
	}
	var ok []netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if special(a) && !p.ips[a] {
			continue
		}
		ok = append(ok, a)
	}
	if len(ok) == 0 {
		return nil, "the name resolves only to private or local addresses"
	}
	return ok, ""
}

func (p *Proxy) log(d Decision) {
	if p.opts.Log == nil {
		return
	}
	d.Time = time.Now().UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opts.Log(d)
}

// dial connects to the first reachable checked address.
func (p *Proxy) dial(ctx context.Context, addrs []netip.Addr, port int) (net.Conn, error) {
	d := net.Dialer{Timeout: p.opts.DialTimeout}
	var last error
	for _, a := range addrs {
		c, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(a, uint16(port)).String())
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

// ServeHTTP handles CONNECT tunnels (HTTPS and anything else over TCP) and plain HTTP requests in absolute form.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	if r.URL == nil || !r.URL.IsAbs() || (r.URL.Scheme != "http") {
		http.Error(w, "drydock egress: send requests through this proxy as a proxy (http://host/... or CONNECT host:443)", http.StatusBadRequest)
		return
	}
	host, port := splitHostPort(r.URL.Host, 80)
	addrs, why := p.resolve(r.Context(), host, port)
	p.log(Decision{Method: r.Method, Host: host, Port: port, Allowed: why == "", Reason: why})
	if why != "" {
		http.Error(w, fmt.Sprintf("drydock egress: %s:%d refused: %s", host, port, why), http.StatusForbidden)
		return
	}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return p.dial(ctx, addrs, port)
		},
		ResponseHeaderTimeout: 2 * time.Minute,
	}
	defer tr.CloseIdleConnections()
	out := r.Clone(r.Context())
	out.RequestURI = ""
	removeHopHeaders(out.Header)
	resp, err := tr.RoundTrip(out)
	if err != nil {
		http.Error(w, "drydock egress: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	removeHopHeaders(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	host, port := splitHostPort(r.Host, 443)
	addrs, why := p.resolve(r.Context(), host, port)
	p.log(Decision{Method: "CONNECT", Host: host, Port: port, Allowed: why == "", Reason: why})
	if why != "" {
		http.Error(w, fmt.Sprintf("drydock egress: %s:%d refused: %s", host, port, why), http.StatusForbidden)
		return
	}
	up, err := p.dial(r.Context(), addrs, port)
	if err != nil {
		http.Error(w, "drydock egress: "+err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "drydock egress: tunnelling is not supported here", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		client.Close()
		up.Close()
		return
	}
	go pipe(up, buf.Reader, client)
	go pipe(client, up, up)
}

// pipe copies src to dst and closes both ends when done.
func pipe(dst io.Writer, src io.Reader, closer io.Closer) {
	_, _ = io.Copy(dst, src)
	_ = closer.Close()
	if c, ok := dst.(io.Closer); ok {
		_ = c.Close()
	}
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func removeHopHeaders(h http.Header) {
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

func splitHostPort(hostport string, def int) (string, int) {
	host, ps, err := net.SplitHostPort(hostport)
	if err != nil {
		return strings.Trim(hostport, "[]"), def
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port <= 0 || port > 65535 {
		return host, -1
	}
	return host, port
}

// JSONLog writes decisions as JSON lines to w (the container's log, which Drydock reads back).
func JSONLog(w io.Writer) func(Decision) {
	bw := bufio.NewWriter(w)
	return func(d Decision) {
		b, _ := json.Marshal(d)
		_, _ = bw.Write(append(b, '\n'))
		_ = bw.Flush()
	}
}

// ParseLog reads the decisions back from the proxy container's log; lines that are not decisions are skipped.
func ParseLog(r io.Reader) []Decision {
	var out []Decision
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var d Decision
		if json.Unmarshal(sc.Bytes(), &d) == nil && d.Host != "" {
			out = append(out, d)
		}
	}
	return out
}

// ErrBadConfig is returned by FromEnv for an unusable environment.
var ErrBadConfig = errors.New("egress: bad configuration")

// FromEnv reads the options a sandbox passes to the proxy container: DRYDOCK_EGRESS_ALLOW (comma separated) and
// DRYDOCK_EGRESS_PORTS (comma separated, default 80,443).
func FromEnv(getenv func(string) string) (Options, error) {
	var o Options
	for _, h := range strings.Split(getenv("DRYDOCK_EGRESS_ALLOW"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			o.Allow = append(o.Allow, h)
		}
	}
	for _, s := range strings.Split(getenv("DRYDOCK_EGRESS_PORTS"), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 || n > 65535 {
			return o, fmt.Errorf("%w: port %q", ErrBadConfig, s)
		}
		o.Ports = append(o.Ports, n)
	}
	return o, nil
}
