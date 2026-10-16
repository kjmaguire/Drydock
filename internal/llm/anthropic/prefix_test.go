package anthropic_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/anthropic"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
)

// bodyRecorder keeps the exact bytes of every request body the client sends.
type bodyRecorder struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (r *bodyRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.bodies = append(r.bodies, b)
		r.mu.Unlock()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	return http.DefaultTransport.RoundTrip(req)
}

// TestPrefixByteStable (token rule 7): two requests that differ only in the last user message are byte-identical up to that
// message, and the same request is the same bytes every time, so the provider's prompt cache can serve the whole stable part
// (tools, system blocks, earlier turns) of the second call.
func TestPrefixByteStable(t *testing.T) {
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, r *http.Request) { msgOK(c, w) })
	rec := &bodyRecorder{}
	c, err := anthropic.NewWithConfig(prov(srv), "sk-ant-test", &http.Client{Transport: rec}, anthropic.Config{})
	if err != nil {
		t.Fatal(err)
	}
	withTail := func(tail string) llm.Request {
		r := request()
		r.Messages[len(r.Messages)-1].Content = tail
		return r
	}
	for _, tail := range []string{"TAIL-ONE: summarize", "TAIL-ONE: summarize", "TAIL-TWO: list the risks instead"} {
		if _, err := c.Complete(context.Background(), withTail(tail)); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.bodies) != 3 {
		t.Fatalf("%d request bodies recorded", len(rec.bodies))
	}
	if !bytes.Equal(rec.bodies[0], rec.bodies[1]) {
		t.Fatalf("the same request built twice differs:\n%s\n%s", rec.bodies[0], rec.bodies[1])
	}
	a, b := rec.bodies[0], rec.bodies[2]
	common := 0
	for common < len(a) && common < len(b) && a[common] == b[common] {
		common++
	}
	tail := bytes.Index(a, []byte("TAIL-ONE"))
	if tail < 0 || !bytes.Contains(a, []byte("tools-and-rules prefix")) || !bytes.Contains(a, []byte("file body")) {
		t.Fatalf("request body %s", a)
	}
	if common < tail {
		t.Fatalf("the bodies diverge at byte %d, before the last message at byte %d:\n%s\n%s", common, tail, a[:min(len(a), common+80)], b[:min(len(b), common+80)])
	}
	// apart from the last message's text the two bodies are the same bytes: the earlier turns, and the tools and system blocks
	// (the encoder writes those after the messages; the provider caches in its own order: tools, system, messages)
	if !bytes.Equal(bytes.Replace(a, []byte("TAIL-ONE: summarize"), []byte("X"), 1), bytes.Replace(b, []byte("TAIL-TWO: list the risks instead"), []byte("X"), 1)) {
		t.Fatalf("the requests differ beyond their last message:\n%s\n%s", a, b)
	}
}
