package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/transport/httpapi"
)

func start(t *testing.T) (*core.Core, *httptest.Server) {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "d"), User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	s := &httpapi.Server{Core: c, Token: "secret"}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return c, ts
}

func do(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		sb.WriteString(sc.Text())
	}
	return resp.StatusCode, sb.String()
}

func rpc(t *testing.T, ts *httptest.Server, method, body string, hdr map[string]string) (int, string) {
	req, _ := http.NewRequest("POST", ts.URL+"/rpc/"+method, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return do(t, req)
}

func TestRPCRequiresTheTokenAndALoopbackHostAndRejectsForeignOrigins(t *testing.T) {
	_, ts := start(t)
	if code, _ := rpc(t, ts, "app.info", "{}", nil); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := rpc(t, ts, "app.info", "{}", map[string]string{"X-Drydock-Token": "wrong"}); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	// a web page in the user's browser cannot call the API even if it somehow had the token
	if code, _ := rpc(t, ts, "app.info", "{}", map[string]string{"X-Drydock-Token": "secret", "Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("foreign origin: %d", code)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/rpc/app.info", strings.NewReader("{}"))
	req.Host = "evil.example"
	req.Header.Set("X-Drydock-Token", "secret")
	if code, _ := do(t, req); code != 403 {
		t.Fatalf("rebinding host: %d", code)
	}
	code, body := rpc(t, ts, "app.info", "{}", map[string]string{"X-Drydock-Token": "secret"})
	if code != 200 || !strings.Contains(body, `"human":"human:kyle"`) {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := rpc(t, ts, "nope", "{}", map[string]string{"X-Drydock-Token": "secret"}); code != 404 || !strings.Contains(body, "unknown method") {
		t.Fatalf("%d %s", code, body)
	}
	notRepo, _ := json.Marshal(map[string]string{"name": "x", "repoPath": filepath.Join(t.TempDir(), "definitely-not-a-repo")})
	if code, body := rpc(t, ts, "project.create", string(notRepo), map[string]string{"X-Drydock-Token": "secret"}); code != 400 || !strings.Contains(body, "not a usable Git repository") {
		t.Fatalf("%d %s", code, body)
	}
}

// The event stream replays what the client missed, then delivers live events, exactly once and in order.
func TestEventStreamReplaysThenFollows(t *testing.T) {
	c, ts := start(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		c.Bus.Publish(ctx, events.Event{Type: "test.before"})
	}
	req, _ := http.NewRequest("GET", ts.URL+"/events?after=0&types=test.*&token=secret", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got := make(chan events.Event, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var e events.Event
				if json.Unmarshal([]byte(d), &e) == nil {
					got <- e
				}
			}
		}
		close(got)
	}()
	var seqs []uint64
	collect := func(n int) {
		for len(seqs) < n {
			select {
			case e, ok := <-got:
				if !ok {
					t.Fatal("stream ended")
				}
				seqs = append(seqs, e.Seq)
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out with %v", seqs)
			}
		}
	}
	collect(3)
	for i := 0; i < 2; i++ {
		c.Bus.Publish(ctx, events.Event{Type: "test.after"})
	}
	c.Bus.Publish(ctx, events.Event{Type: "other.ignored"})
	collect(5)
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("not strictly increasing: %v", seqs)
		}
	}
	select {
	case e := <-got:
		t.Fatalf("filtered event leaked: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
	if code, _ := do(t, mustReq("GET", ts.URL+"/events")); code != 401 {
		t.Fatalf("event stream without token: %d", code)
	}
}

func mustReq(m, u string) *http.Request { r, _ := http.NewRequest(m, u, nil); return r }
