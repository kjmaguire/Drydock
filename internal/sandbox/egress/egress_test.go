package egress

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeResolver is a labelled test double for DNS: names resolve to the addresses the test says.
type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, s := range f[host] {
		out = append(out, netip.MustParseAddr(s))
	}
	if len(out) == 0 {
		return nil, errors.New("no such host")
	}
	return out, nil
}

func TestTheAllowlistMatchesNamesDomainsAndAddresses(t *testing.T) {
	p, err := New(Options{Allow: []string{"registry.npmjs.org", "*.example.org", "203.0.113.7", "10.1.2.3"}})
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		"registry.npmjs.org": true, "REGISTRY.npmjs.org.": true, "evil-registry.npmjs.org": false, "npmjs.org": false,
		"a.example.org": true, "a.b.example.org": true, "example.org": false, "badexample.org": false,
		"203.0.113.7": true, "203.0.113.8": false, "10.1.2.3": true, "::ffff:10.1.2.3": true,
	} {
		if got := p.hostAllowed(host); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
	if _, err := New(Options{Allow: []string{"https://x"}}); err == nil {
		t.Fatal("a URL is not an allowlist entry")
	}
}

func TestNamesThatResolveToPrivateAddressesAreRefused(t *testing.T) {
	p, _ := New(Options{
		Allow:    []string{"pub.example", "rebind.example", "meta.example", "mixed.example", "corp.example", "10.9.9.9"},
		Resolver: fakeResolver{"pub.example": {"93.184.216.34"}, "rebind.example": {"127.0.0.1"}, "meta.example": {"169.254.169.254"}, "mixed.example": {"192.168.1.5", "93.184.216.35"}, "corp.example": {"10.9.9.9"}},
	})
	ctx := context.Background()
	if addrs, why := p.resolve(ctx, "pub.example", 443); why != "" || len(addrs) != 1 {
		t.Fatalf("public name: %v %q", addrs, why)
	}
	for _, h := range []string{"rebind.example", "meta.example"} {
		if _, why := p.resolve(ctx, h, 443); !strings.Contains(why, "private or local") {
			t.Errorf("%s: %q", h, why)
		}
	}
	if addrs, why := p.resolve(ctx, "mixed.example", 443); why != "" || len(addrs) != 1 || addrs[0].String() != "93.184.216.35" {
		t.Fatalf("only the public address of a mixed answer is used: %v %q", addrs, why)
	}
	if addrs, why := p.resolve(ctx, "10.9.9.9", 80); why != "" || addrs[0].String() != "10.9.9.9" {
		t.Fatalf("a private address listed by itself is allowed: %v %q", addrs, why)
	}
	// a company host: its name resolves to a private address the project listed itself, so the name works too
	if addrs, why := p.resolve(ctx, "corp.example", 443); why != "" || addrs[0].String() != "10.9.9.9" {
		t.Fatalf("a name resolving to a listed private address: %v %q", addrs, why)
	}
	if _, why := p.resolve(ctx, "pub.example", 22); !strings.Contains(why, "port 22") {
		t.Fatalf("other ports are refused: %q", why)
	}
	if _, why := p.resolve(ctx, "other.example", 443); !strings.Contains(why, "allowlist") {
		t.Fatalf("names not listed are refused: %q", why)
	}
}

// startProxy serves p on a loopback port.
func startProxy(t *testing.T, p *Proxy) *url.URL {
	t.Helper()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

func TestTheProxyForwardsAllowedRequestsAndRefusesTheRest(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hello from "+r.URL.Path) }))
	defer web.Close()
	webTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "secure hello") }))
	defer webTLS.Close()
	plainAddr := netip.MustParseAddrPort(strings.TrimPrefix(web.URL, "http://"))
	tlsAddr := netip.MustParseAddrPort(strings.TrimPrefix(webTLS.URL, "https://"))

	var log bytes.Buffer
	p, err := New(Options{
		// the loopback test servers are allowed by address, as a project could list one of its own private hosts
		Allow: []string{"allowed.test", "127.0.0.1"},
		Ports: []int{int(plainAddr.Port()), int(tlsAddr.Port())},
		// allowed.test is listed but resolves to a loopback address that is not: a name never leads to an unlisted private address
		Resolver: fakeResolver{"allowed.test": {"127.0.0.2"}, "denied.test": {"127.0.0.1"}},
		Log:      JSONLog(&log),
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyURL := startProxy(t, p)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // the test server's own certificate

	// plain HTTP, allowed by address
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/x", plainAddr.Port()))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello from /x" {
		t.Fatalf("allowed plain request: %d %q", resp.StatusCode, body)
	}
	// HTTPS through CONNECT, allowed by address
	resp, err = client.Get(fmt.Sprintf("https://127.0.0.1:%d/", tlsAddr.Port()))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "secure hello" {
		t.Fatalf("tunnelled request: %q", body)
	}
	// a listed name that resolves to loopback is refused: names never lead to private addresses
	resp, err = client.Get(fmt.Sprintf("http://allowed.test:%d/", plainAddr.Port()))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "private or local") {
		t.Fatalf("rebinding name: %d %q", resp.StatusCode, body)
	}
	// a name not on the list is refused, for CONNECT too
	if _, err := client.Get(fmt.Sprintf("https://denied.test:%d/", tlsAddr.Port())); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("denied CONNECT: %v", err)
	}
	// a port not on the list is refused
	resp, err = client.Get("http://127.0.0.1:1/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other port: %d", resp.StatusCode)
	}
	// the decisions are logged and can be read back
	ds := ParseLog(&log)
	var allowed, refused int
	for _, d := range ds {
		if d.Allowed {
			allowed++
		} else {
			refused++
		}
	}
	if allowed != 2 || refused != 3 {
		t.Fatalf("decisions: %d allowed, %d refused: %+v", allowed, refused, ds)
	}
}

func TestADirectRequestIsNotAProxyRequest(t *testing.T) {
	p, _ := New(Options{Allow: []string{"x.test"}})
	u := startProxy(t, p)
	resp, err := http.Get(u.String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d", resp.StatusCode)
	}
	// and a raw CONNECT with a bad port gets a refusal, not a tunnel
	c, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT x.test:99999 HTTP/1.1\r\nHost: x.test:99999\r\n\r\n")
	line, _ := bufio.NewReader(c).ReadString('\n')
	if !strings.Contains(line, "403") {
		t.Fatalf("bad port CONNECT: %q", line)
	}
}

func TestFromEnv(t *testing.T) {
	env := map[string]string{"DRYDOCK_EGRESS_ALLOW": " a.test, *.b.test ,,", "DRYDOCK_EGRESS_PORTS": "443, 8443"}
	o, err := FromEnv(func(k string) string { return env[k] })
	if err != nil || len(o.Allow) != 2 || len(o.Ports) != 2 || o.Ports[1] != 8443 {
		t.Fatalf("%+v %v", o, err)
	}
	env["DRYDOCK_EGRESS_PORTS"] = "http"
	if _, err := FromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("a non-numeric port is refused")
	}
}

// The proxy refuses the same addresses Drydock's own connections do, including IPv6 addresses that embed a private IPv4 one
// (NAT64, 6to4) and the reserved IPv4 range (audit 2026-10-16, L53).
func TestTheProxyRefusesWhatDrydocksOwnConnectionsRefuse(t *testing.T) {
	for addr, want := range map[string]bool{
		"10.0.0.1": true, "127.0.0.1": true, "169.254.169.254": true, "100.64.0.1": true, "198.18.0.1": true, "240.0.0.1": true, "255.255.255.255": true,
		"64:ff9b::a00:1": true, "64:ff9b::7f00:1": true, "2002:a00:1::": true, "2002:7f00:1::1": true, "fc00::1": true, "fe80::1": true, "::1": true,
		"::ffff:10.0.0.1": true,
		"93.184.216.34":   false, "2606:4700:4700::1111": false, "64:ff9b::5db8:d822": false, "2002:5db8:d822::1": false,
	} {
		if got := special(netip.MustParseAddr(addr)); got != want {
			t.Errorf("special(%s) = %v, want %v", addr, got, want)
		}
	}
	p, _ := New(Options{Allow: []string{"nat64.example"}, Resolver: fakeResolver{"nat64.example": {"64:ff9b::a00:1"}}})
	if _, why := p.resolve(context.Background(), "nat64.example", 443); !strings.Contains(why, "private or local") {
		t.Fatalf("a name that resolves to a NAT64 form of a private address: %q", why)
	}
}

// A tunnel that carries nothing is closed after the idle time, and one that carries traffic is kept (audit 2026-10-16, L53).
func TestAnIdleTunnelIsClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // an upstream that echoes
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	p, err := New(Options{Allow: []string{"127.0.0.1"}, Ports: []int{port}, IdleTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	proxy := startProxy(t, p)
	open := func() (net.Conn, *bufio.Reader) {
		c, err := net.Dial("tcp", proxy.Host)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		// the first bytes of the tunnel go with the CONNECT request, as a client that does not wait for the answer sends them
		fmt.Fprintf(c, "CONNECT 127.0.0.1:%d HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\nearly", port, port)
		br := bufio.NewReader(c)
		resp, err := http.ReadResponse(br, nil)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("CONNECT: %v %v", resp, err)
		}
		return c, br
	}
	// what was sent with the request reaches the upstream and comes back
	c, br := open()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	early := make([]byte, 5)
	if _, err := io.ReadFull(br, early); err != nil || string(early) != "early" {
		t.Fatalf("the bytes sent with the CONNECT request were lost: %q %v", early, err)
	}
	// traffic keeps it open past the idle time
	for i := 0; i < 4; i++ {
		time.Sleep(150 * time.Millisecond)
		fmt.Fprint(c, "ping")
		pong := make([]byte, 4)
		if _, err := io.ReadFull(br, pong); err != nil || string(pong) != "ping" {
			t.Fatalf("round %d: the tunnel was closed while it carried traffic: %q %v", i, pong, err)
		}
	}
	// silence closes it
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("a byte arrived on a silent tunnel")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("the tunnel was still open after %s of silence", time.Since(start))
	}
}
