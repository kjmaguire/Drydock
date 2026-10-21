package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kjmaguire/drydock/internal/netguard"
)

// The addresses a server names during sign-in (its metadata, the authorization server, the token endpoint) must not reach into
// the user's own network or a cloud metadata service. A server on this computer may name loopback addresses; a server on the
// local network may be contacted itself and does not open the rest of the network (audit 2026-10-16, M9).
func TestOAuthPolicyIsDecidedByWhereTheConfiguredServerIs(t *testing.T) {
	for _, c := range []struct {
		server       string
		wantHost     string
		wantLoopback bool
	}{
		{"https://mcp.example.com/mcp", "mcp.example.com", false},
		{"http://localhost:8080/mcp", "localhost", true},
		{"http://127.0.0.1:8080/mcp", "127.0.0.1", true},
		{"http://[::1]:8080/mcp", "::1", true},
		{"http://192.168.1.10:8080/mcp", "192.168.1.10", false}, // the LAN is not "local": only the server itself is allowed
		{"http://10.0.0.5/mcp", "10.0.0.5", false},
		{"https://app.localhost/mcp", "app.localhost", true},
	} {
		p := oauthPolicy(c.server)
		if p.AllowHost != c.wantHost || p.AllowLoopback != c.wantLoopback {
			t.Errorf("%s: %+v", c.server, p)
		}
	}
}

func TestOAuthClientReachesTheConfiguredServerAndNothingInsideTheNetworkThatItNames(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()
	// a server on this computer: it and its authorization server (another loopback address and port) are reachable
	local := oauthHTTPClient(srv.URL + "/mcp")
	resp, err := local.Get(srv.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if hits != 1 {
		t.Fatalf("hits %d", hits)
	}
	// but what it names beyond loopback is refused: the cloud metadata address and the local network
	for _, target := range []string{"https://169.254.169.254/latest/meta-data", "https://10.0.0.5/token", "https://192.168.1.10/token"} {
		if _, err := local.Get(target); !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("%s from a local server: %v", target, err)
		}
	}
	// a remote server cannot name this computer at all
	remote := oauthHTTPClient("https://mcp.example.com/mcp")
	for _, target := range []string{srv.URL + "/token", "https://127.0.0.1:9/token", "https://[::1]/token", "https://100.64.0.1/token", "http://203.0.113.10/token"} {
		if _, err := remote.Get(target); err == nil {
			t.Errorf("%s was reachable from a remote server's sign-in", target)
		}
	}
	if hits != 1 {
		t.Fatalf("a refused address was contacted: hits %d", hits)
	}
}
