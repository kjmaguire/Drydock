package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// OAuth access tokens expire while an upstream connection stays open, so the credential is asked for on every request, and a
// credential that cannot be had stops the request instead of sending it without one.
func TestAuthTransportAsksForTheCredentialOnEveryRequest(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = append(seen, r.Header.Get("Authorization")) }))
	defer srv.Close()
	n := 0
	fail := false
	hc := &http.Client{Transport: &authTransport{base: http.DefaultTransport, header: "Authorization", dynamic: func(ctx context.Context) (string, error) {
		if fail {
			return "", errors.New("signed out")
		}
		n++
		return fmt.Sprintf("Bearer t%d", n), nil
	}}}
	for i := 0; i < 2; i++ {
		resp, err := hc.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if strings.Join(seen, ",") != "Bearer t1,Bearer t2" {
		t.Fatalf("headers sent: %v", seen)
	}
	fail = true
	if _, err := hc.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "signed out") {
		t.Fatalf("a missing credential must stop the request: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("a request went out without its credential: %v", seen)
	}
}
