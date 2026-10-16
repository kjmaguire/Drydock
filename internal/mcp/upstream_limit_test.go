package mcp

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An upstream server that answers without end is cut off at the limit: the reader gets an error, and memory does not follow the
// server (audit 2026-10-16, M9).
func TestAnUpstreamResponseThatNeverEndsIsCutOffAtTheLimit(t *testing.T) {
	old := maxUpstreamBody
	maxUpstreamBody = 1 << 20
	defer func() { maxUpstreamBody = old }()
	chunk := []byte(strings.Repeat("x", 4096))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	c := &http.Client{Transport: limitedTransport{base: http.DefaultTransport}}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if !errors.Is(err, errUpstreamTooLarge) || n != 1<<20 {
		t.Fatalf("read %d bytes, error %v", n, err)
	}
}

func TestAnUpstreamResponseUnderTheLimitIsReadWhole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer srv.Close()
	c := &http.Client{Transport: limitedTransport{base: upstreamTransport()}}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != `{"ok":true}` {
		t.Fatalf("%q %v", b, err)
	}
	if tr, ok := upstreamTransport().(*http.Transport); !ok || tr.ResponseHeaderTimeout <= 0 {
		t.Fatal("the upstream transport waits for a response's headers for ever")
	}
}
