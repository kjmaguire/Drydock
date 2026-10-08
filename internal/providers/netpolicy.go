package providers

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Resolver resolves a host name to addresses. The default is the system resolver; tests inject fixed answers.
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

// SystemResolver resolves with net.DefaultResolver.
func SystemResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// Transport is the verdict of the unencrypted-LAN rule for a base URL (brief section 16).
type Transport struct {
	TLS            TLSMode
	LANUnencrypted bool // plain http on a private (non-loopback) address
}

// allowedPlain reports whether plain http may reach a: loopback, link-local or private (RFC 1918, and IPv6 ULA).
func allowedPlain(a netip.Addr) (loopback bool, ok bool) {
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return true, true
	case a.IsPrivate(), a.IsLinkLocalUnicast():
		return false, true
	}
	return false, false
}

// NormalizeFingerprint returns the lower-case hex SHA-256 fingerprint, accepting "AA:BB:..." and "sha256:..." forms.
func NormalizeFingerprint(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "sha256:")
	s = strings.ReplaceAll(s, ":", "")
	if len(s) != 64 {
		return "", fmt.Errorf("%w: fingerprint must be 64 hex characters (SHA-256)", ErrInvalid)
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", fmt.Errorf("%w: fingerprint is not hex", ErrInvalid)
	}
	return s, nil
}

// CheckTransport applies the unencrypted-LAN rule: https is always fine (pinned when a fingerprint is given); plain
// http only to loopback, link-local or private addresses, and every address a host name resolves to must qualify.
// Plain http on a private non-loopback address is flagged LANUnencrypted.
func CheckTransport(ctx context.Context, baseURL, fingerprint string, resolve Resolver) (Transport, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return Transport{}, fmt.Errorf("%w: base_url %q is not a URL", ErrInvalid, baseURL)
	}
	if u.User != nil {
		return Transport{}, fmt.Errorf("%w: credentials must not be embedded in base_url", ErrInvalid)
	}
	switch u.Scheme {
	case "https":
		if fingerprint != "" {
			if _, err := NormalizeFingerprint(fingerprint); err != nil {
				return Transport{}, err
			}
			return Transport{TLS: TLSPinned}, nil
		}
		return Transport{TLS: TLSSystem}, nil
	case "http":
	default:
		return Transport{}, fmt.Errorf("%w: unsupported scheme %q", ErrInvalid, u.Scheme)
	}
	if fingerprint != "" {
		return Transport{}, fmt.Errorf("%w: a TLS fingerprint needs an https base_url", ErrInvalid)
	}
	addrs, err := hostAddrs(ctx, u.Hostname(), resolve)
	if err != nil {
		return Transport{}, err
	}
	allLoop := true
	for _, a := range addrs {
		loop, ok := allowedPlain(a)
		if !ok {
			return Transport{}, fmt.Errorf("%w: %s resolves to public address %s", ErrInsecureTransport, u.Hostname(), a)
		}
		allLoop = allLoop && loop
	}
	if allLoop {
		return Transport{TLS: TLSNoneLoop}, nil
	}
	return Transport{TLS: TLSNonePrivate, LANUnencrypted: true}, nil
}

func hostAddrs(ctx context.Context, host string, resolve Resolver) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{a}, nil
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	if resolve == nil {
		resolve = SystemResolver
	}
	addrs, err := resolve(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot resolve %s for the transport check: %v", ErrInvalid, host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: %s has no addresses", ErrInvalid, host)
	}
	return addrs, nil
}

// NewHTTPClient builds the HTTP client for a provider. Plain-http providers dial through a guard that rechecks the
// resolved address at connect time (a DNS answer that changes to a public address is refused: DNS rebinding), and
// pinned providers verify the leaf certificate's SHA-256 instead of the system roots.
func NewHTTPClient(p Provider, resolve Resolver, timeout time.Duration) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	switch p.TLSMode {
	case TLSNoneLoop, TLSNonePrivate:
		tr.Proxy = nil // never route LAN or loopback traffic through an environment proxy
		tr.DialContext = guardedDial(resolve)
	case TLSPinned:
		fp, err := NormalizeFingerprint(p.TLSFingerprint)
		if err != nil {
			return nil, err
		}
		want, _ := hex.DecodeString(fp)
		tr.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// The chain is not verified against system roots; VerifyConnection pins the leaf instead.
			InsecureSkipVerify: true, //nolint:gosec // pinned by SHA-256 in VerifyConnection
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return fmt.Errorf("providers: no server certificate")
				}
				sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(sum[:], want) != 1 {
					return fmt.Errorf("providers: server certificate does not match the pinned fingerprint")
				}
				return nil
			},
		}
	}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}

func guardedDial(resolve Resolver) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		addrs, err := hostAddrs(ctx, host, resolve)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			if _, ok := allowedPlain(a); !ok {
				return nil, fmt.Errorf("%w: %s resolved to public address %s at connect time", ErrInsecureTransport, host, a)
			}
		}
		var last error
		for _, a := range addrs { // dial the checked address itself, not the name, so it cannot change between check and connect
			c, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}
}
