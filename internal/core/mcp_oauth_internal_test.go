package core

import (
	"net"
	"net/http"
	"testing"
)

type okTransport struct{ hit *bool }

func (o okTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*o.hit = true
	return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
}

// The addresses a server names during sign-in (its metadata, the authorization server, the token endpoint) must not reach into
// the user's own network or a cloud metadata service, unless the MCP server itself is on that network.
func TestOAuthGuardRefusesAddressesInsideTheNetwork(t *testing.T) {
	for _, c := range []struct {
		url        string
		allowLocal bool
		want       bool // reaches the network
	}{
		{"https://169.254.169.254/latest/meta-data", false, false},
		{"https://10.0.0.5/token", false, false},
		{"https://192.168.1.10/token", false, false},
		{"https://127.0.0.1:9/token", false, false},
		{"https://[::1]/token", false, false},
		{"https://100.64.0.1/token", false, false},
		{"http://203.0.113.10/token", false, false}, // not https
		{"https://203.0.113.10/token", false, true},
		{"http://127.0.0.1:8080/token", true, true}, // a local MCP server and its own authorization server
		{"https://169.254.169.254/x", true, true},   // a local server vouches for what it names
	} {
		hit := false
		g := guardedTransport{base: okTransport{&hit}, allowLocal: c.allowLocal}
		req, _ := http.NewRequest("GET", c.url, nil)
		_, err := g.RoundTrip(req)
		if hit != c.want || (err == nil) != c.want {
			t.Errorf("%s (allowLocal=%v): reached=%v err=%v, want reached=%v", c.url, c.allowLocal, hit, err, c.want)
		}
	}
	for ip, want := range map[string]bool{"8.8.8.8": false, "10.1.2.3": true, "172.16.0.1": true, "172.32.0.1": false, "100.127.255.255": true, "100.128.0.1": false, "fe80::1": true, "2001:4860:4860::8888": false} {
		if got := blockedIP(net.ParseIP(ip)); got != want {
			t.Errorf("blockedIP(%s) = %v, want %v", ip, got, want)
		}
	}
	if !isLocalHost("localhost") || !isLocalHost("127.0.0.1") || isLocalHost("example.com") {
		t.Error("isLocalHost")
	}
}
