package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBlockedCoversWhatIsNotThePublicInternet(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": false, "1.1.1.1": false, "203.0.113.10": false, "2001:4860:4860::8888": false, "172.32.0.1": false, "100.128.0.1": false,
		"127.0.0.1": true, "127.1.2.3": true, "::1": true, "10.1.2.3": true, "172.16.0.1": true, "192.168.1.10": true, "169.254.169.254": true,
		"100.64.0.1": true, "100.127.255.255": true, "0.0.0.0": true, "::": true, "224.0.0.1": true, "ff02::1": true, "fe80::1": true, "fc00::1": true,
		"192.0.0.8": true, "198.18.0.1": true, "198.19.255.255": true, "240.0.0.1": true, "255.255.255.255": true,
		"::ffff:127.0.0.1": true, "::ffff:10.0.0.1": true, "::ffff:8.8.8.8": false,
		"64:ff9b::7f00:1": true, "64:ff9b::a00:1": true, "64:ff9b::808:808": false, // NAT64 carrying 127.0.0.1, 10.0.0.1, 8.8.8.8
		"2002:7f00:1::": true, "2002:a9fe:a9fe::": true, "2002:808:808::": false, // 6to4 carrying 127.0.0.1, 169.254.169.254, 8.8.8.8
	} {
		if got := Blocked(net.ParseIP(ip)); got != want {
			t.Errorf("Blocked(%s) = %v, want %v", ip, got, want)
		}
	}
	if !Blocked(nil) {
		t.Error("no address must be blocked")
	}
	for host, want := range map[string]bool{"localhost": true, "LOCALHOST.": true, "app.localhost": true, "127.0.0.1": true, "::1": true, "10.0.0.1": false, "example.com": false, "localhost.evil.com": false} {
		if IsLoopbackHost(host) != want {
			t.Errorf("IsLoopbackHost(%q) = %v", host, !want)
		}
	}
}

func resolver(answers map[string][]string, calls *atomic.Int32) func(context.Context, string) ([]net.IP, error) {
	return func(_ context.Context, host string) ([]net.IP, error) {
		calls.Add(1)
		var out []net.IP
		for _, a := range answers[host] {
			out = append(out, net.ParseIP(a))
		}
		return out, nil
	}
}

// the transport under test reaches a server on this computer, so what it is allowed to contact is decided by the policy alone
func get(t *testing.T, p Policy, rawurl string) (int, error) {
	t.Helper()
	c := &http.Client{Transport: Guard{Base: p.Transport(), Policy: p}}
	resp, err := c.Get(rawurl)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func TestTransportRefusesNamesThatResolveInsideTheNetworkAndNeverContactsThem(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	var calls atomic.Int32
	p := Policy{LookupIP: resolver(map[string][]string{
		"inside.example":   {"127.0.0.1"},
		"mixed.example":    {"8.8.8.8", "127.0.0.1"}, // one private answer among public ones is enough to refuse
		"metadata.example": {"169.254.169.254"},
	}, &calls)}
	for _, host := range []string{"inside.example", "mixed.example", "metadata.example"} {
		if _, err := get(t, p, "https://"+host+":"+port+"/"); err == nil || !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: %v", host, err)
		}
	}
	if _, err := get(t, p, "http://"+srv.Listener.Addr().String()+"/"); err == nil || !strings.Contains(err.Error(), "must be https") {
		t.Errorf("a plain http address of a server nobody vouched for: %v", err)
	}
	if _, err := get(t, p, "https://127.0.0.1:"+port+"/"); err == nil || !errors.Is(err, ErrBlocked) {
		t.Errorf("a loopback address: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a refused address was contacted %d times", hits.Load())
	}
}

func TestTransportAllowsTheServerThePersonConfiguredAndNothingElseInsideTheNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	var calls atomic.Int32
	answers := map[string][]string{"mcp.lan": {"127.0.0.1"}, "evil.example": {"127.0.0.1"}, "other.lan": {"127.0.0.1"}}
	// the configured server is a name on the network: it and only it may be contacted, over http
	lan := Policy{AllowHost: "mcp.lan", LookupIP: resolver(answers, &calls)}
	if code, err := get(t, lan, "http://mcp.lan:"+port+"/"); err != nil || code != 200 {
		t.Fatalf("the configured server: %d %v", code, err)
	}
	if _, err := get(t, lan, "https://other.lan:"+port+"/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("another name on the network: %v", err)
	}
	// a server on this computer: any loopback name or address, never a public name that resolves there
	local := Policy{AllowHost: "localhost", AllowLoopback: true, LookupIP: resolver(answers, &calls)}
	if code, err := get(t, local, "http://127.0.0.1:"+port+"/"); err != nil || code != 200 {
		t.Fatalf("a loopback address for a local server: %d %v", code, err)
	}
	if _, err := get(t, local, "https://evil.example:"+port+"/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("a public name that resolves to loopback (DNS pointing at this computer): %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits %d", hits.Load())
	}
}

// The address that was checked is the address that is dialled: the name is resolved once, so an answer that changes after the
// check changes nothing (DNS rebinding).
func TestTheNameIsResolvedOnceAndTheCheckedAddressIsTheOneDialled(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	var calls atomic.Int32
	p := Policy{AllowHost: "rebind.example", LookupIP: resolver(map[string][]string{"rebind.example": {"127.0.0.1"}}, &calls)}
	if _, err := get(t, p, "http://rebind.example:"+port+"/"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("the name was resolved %d times for one connection", calls.Load())
	}
	// and the hook on the dialler refuses a blocked address even if something dialled it
	c := Policy{}.control("evil.example")
	if err := c("tcp", "127.0.0.1:80", nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("control accepted loopback: %v", err)
	}
	if err := c("tcp", "8.8.8.8:80", nil); err != nil {
		t.Fatalf("control refused a public address: %v", err)
	}
	if err := c("tcp", "not-an-address:80", nil); err == nil {
		t.Fatal("control accepted a name")
	}
}
