// Package netguard keeps a remote party from making Drydock contact addresses inside the person's own network. The addresses
// Drydock follows during an MCP server's sign-in (its resource metadata, the authorization server, the token endpoint) and the
// ones a server answers with come from that server, so an untrusted one must not be able to point Drydock at a router, a
// database on the LAN, another process on this computer or a cloud metadata service (audit 2026-10-16, M9).
//
// The check is made where it cannot be raced: the transport resolves the name itself, refuses every address that is not public,
// and dials the address it checked (so an answer that changes between the check and the connection changes nothing); the dialer's
// Control hook looks at the address that is really being connected to as well. With a proxy from the environment the proxy
// resolves the name, so the name is resolved and checked first and the proxy is trusted to connect to what was checked.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Blocked reports whether ip is an address that is not on the public internet: this computer, the local network, link-local
// (cloud metadata is 169.254.169.254), carrier-grade NAT, multicast, the unspecified and reserved ranges, and an IPv6 address that
// embeds one of those (mapped, NAT64, 6to4).
func Blocked(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 100 && v4[1]&0xc0 == 64: // 100.64.0.0/10, carrier-grade NAT
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0.0/24, IETF protocol assignments
			return true
		case v4[0] == 198 && v4[1]&0xfe == 18: // 198.18.0.0/15, benchmarking
			return true
		case v4[0] >= 240: // 240.0.0.0/4, reserved, and the broadcast address
			return true
		}
		return false
	}
	if ip16 := ip.To16(); ip16 != nil {
		// 64:ff9b::/96 (NAT64) carries an IPv4 address in its last four bytes; so does 2002::/16 (6to4) in bytes 2-5
		if ip16[0] == 0x00 && ip16[1] == 0x64 && ip16[2] == 0xff && ip16[3] == 0x9b && allZero(ip16[4:12]) {
			return Blocked(net.IP(ip16[12:16]))
		}
		if ip16[0] == 0x20 && ip16[1] == 0x02 {
			return Blocked(net.IP(ip16[2:6]))
		}
		if ip16[0]&0xfe == 0xfc { // fc00::/7, unique local (IsPrivate covers it; kept explicit for the reader)
			return true
		}
	}
	return false
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// IsLoopbackHost reports whether host names this computer: "localhost" (and its subdomains) or a loopback address.
func IsLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Policy says what a transport may connect to. The zero value allows public addresses over https only.
type Policy struct {
	// AllowHost is a host the person configured themselves (the MCP server's own): it may be contacted whatever it resolves to,
	// over http or https. Empty allows none.
	AllowHost string
	// AllowLoopback also allows any loopback address, for a server that is on this computer (its authorization server may be
	// another port, or 127.0.0.1 where the server was named localhost).
	AllowLoopback bool
	// LookupIP resolves a name; nil uses the system resolver. A hook for tests.
	LookupIP func(ctx context.Context, host string) ([]net.IP, error)
	// Base is the transport to build on; nil clones http.DefaultTransport.
	Base *http.Transport
}

// ErrBlocked is wrapped by every refusal.
var ErrBlocked = errors.New("refusing to contact an address on this computer or its network")

func (p Policy) allowedHost(host string) bool {
	if p.AllowHost != "" && strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(p.AllowHost, ".")) {
		return true
	}
	return p.AllowLoopback && IsLoopbackHost(host)
}

func (p Policy) lookup(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if p.LookupIP != nil {
		return p.LookupIP(ctx, host)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, nil
}

// check resolves host and refuses it unless it is allowed or every address it resolves to is public.
func (p Policy) check(ctx context.Context, host string) ([]net.IP, error) {
	ips, err := p.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s does not resolve to any address", host)
	}
	if p.allowedHost(host) {
		return ips, nil
	}
	for _, ip := range ips {
		if p.AllowLoopback && ip.IsLoopback() && IsLoopbackHost(host) {
			continue
		}
		if Blocked(ip) {
			return nil, fmt.Errorf("%w: %s is %s", ErrBlocked, host, ip)
		}
	}
	return ips, nil
}

// control is the dialer hook that looks at the address a connection is really being made to.
func (p Policy) control(host string) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		h, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(h)
		if ip == nil {
			return fmt.Errorf("%w: %s is not an address", ErrBlocked, h)
		}
		if p.allowedHost(host) || (p.AllowLoopback && IsLoopbackHost(host) && ip.IsLoopback()) {
			return nil
		}
		if Blocked(ip) {
			return fmt.Errorf("%w: %s is %s", ErrBlocked, host, ip)
		}
		return nil
	}
}

// Transport returns a transport that applies the policy to every request, including each hop of a redirect.
func (p Policy) Transport() *http.Transport {
	tr := p.Base
	if tr == nil {
		tr = http.DefaultTransport.(*http.Transport).Clone()
	}
	proxy := tr.Proxy
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := p.check(ctx, host)
		if err != nil {
			return nil, err
		}
		d := &net.Dialer{Timeout: 15 * time.Second, Control: p.control(host)}
		var last error
		for _, ip := range ips { // the address that was checked, not the name again
			c, derr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if derr == nil {
				return c, nil
			}
			last = derr
		}
		return nil, last
	}
	tr.Proxy = func(r *http.Request) (*url.URL, error) {
		if proxy == nil {
			return nil, nil
		}
		pu, err := proxy(r)
		if err != nil || pu == nil {
			return pu, err
		}
		// a proxy connects for us: resolve and check what it will be asked to connect to, since the dial below sees only the proxy
		if _, cerr := p.check(r.Context(), r.URL.Hostname()); cerr != nil {
			return nil, cerr
		}
		return pu, nil
	}
	tr.TLSHandshakeTimeout = 15 * time.Second
	tr.ResponseHeaderTimeout = 60 * time.Second
	return tr
}

// Guard refuses the request before it is made when its scheme is not allowed: https only, except for a host the policy allows.
type Guard struct {
	Base http.RoundTripper
	Policy
}

// RoundTrip implements http.RoundTripper.
func (g Guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" && !g.allowedHost(r.URL.Hostname()) {
		return nil, fmt.Errorf("refusing %s: addresses a server names must be https", r.URL.Redacted())
	}
	return g.Base.RoundTrip(r)
}
