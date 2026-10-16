package itfixture

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The fixture redirects an authorization only back to this computer, where the app's sign-in listens (RFC 8252), and answers a
// redirect anywhere else with an error, as a real server that knows its client does (audit 2026-10-16, CodeQL: an open redirect).
func TestTheAuthorizationRedirectsOnlyToThisComputer(t *testing.T) {
	o := NewOAuth("client-1")
	srv := httptest.NewServer(o.Server())
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	authorize := func(redirect string) *http.Response {
		t.Helper()
		q := url.Values{"client_id": {"client-1"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {"abc"},
			"state": {"st"}, "redirect_uri": {redirect}}
		resp, err := client.Get(srv.URL + "/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	for _, ok := range []string{"http://127.0.0.1:41739/callback", "http://localhost:8080/cb", "http://[::1]:9/cb"} {
		resp := authorize(ok)
		if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), strings.SplitN(ok, "?", 2)[0]+"?") || !strings.Contains(resp.Header.Get("Location"), "state=st") {
			t.Errorf("%s: %d %s", ok, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	for _, bad := range []string{"https://evil.example/cb", "http://evil.example/cb", "http://127.0.0.1.evil.example/cb", "https://127.0.0.1/cb", "//evil.example/cb", "javascript:alert(1)", "/relative", ""} {
		if resp := authorize(bad); resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" {
			t.Errorf("%q was redirected to: %d %s", bad, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}
