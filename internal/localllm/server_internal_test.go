package localllm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Start calls the server ready only when /health answers and the server accepts the key it was started with (audit
// 2026-10-16, L32): a different program that took the port between the check and the start answers /health too.
func TestOnlyAServerThatAcceptsTheKeyIsOurs(t *testing.T) {
	const key = "k-for-this-start"
	serve := func(h http.HandlerFunc) string {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return srv.URL
	}
	ours := serve(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" && r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"model_alias":"m","total_slots":1}`))
	})
	notJSON := serve(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>a dev server</html>`)) })
	refuses := serve(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })

	s := &Server{}
	hc := &http.Client{Timeout: 2 * time.Second}
	ctx := context.Background()
	if !s.ours(ctx, hc, ours, key) {
		t.Error("a server that accepts the key was not taken for ours")
	}
	if s.ours(ctx, hc, ours, "another key") {
		t.Error("a key the server does not accept passed")
	}
	// a program that answers something else than JSON, or refuses, is not ours (one that answers JSON to anything cannot be told
	// from the server by this check, which is why the port is checked free first and the key travels in the environment)
	if s.ours(ctx, hc, notJSON, key) || s.ours(ctx, hc, refuses, key) {
		t.Error("a program that is not llama-server was taken for it")
	}
	if !s.ours(ctx, hc, notJSON, "") {
		t.Error("without a key nothing can be told apart and /health is the whole check")
	}
}
