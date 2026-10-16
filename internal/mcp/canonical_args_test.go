package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/safe"
)

// A real MCP server whose only tool is write-class and records the arguments exactly as they reached it.
func startRecordingUpstream(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := sdk.NewServer(&sdk.Implementation{Name: "recorder", Version: "1.0.0"}, nil)
	srv.AddTool(&sdk.Tool{Name: "put", Description: "a write-class tool that records its raw arguments", InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			mu.Lock()
			got = append(got, string(req.Params.Arguments))
			mu.Unlock()
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "stored"}}}, nil
		})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// What a person approves is the hash of the canonical arguments and the text made from them. The call used to be made with the
// agent's own bytes: a duplicate key that a server reads first-wins, or another spelling of a number, made the call differ from
// what was approved (audit 2026-10-16, H8). It is now made with the bytes that were hashed.
func TestAWriteIsMadeWithTheBytesThatWereHashedAndShown(t *testing.T) {
	ts, received := startRecordingUpstream(t)
	var asked []ApprovalRequest
	e := newEnv(t, func(o *Options) {
		o.Approver = approverFunc(func(r ApprovalRequest) (bool, error) {
			asked = append(asked, r)
			return true, nil
		})
	})
	spec := httpSpec("rec", ts.URL, func(s *ServerSpec) {
		s.Trust = TrustPrivateData
		s.DataClass = ContentPrivate
		s.Cache.Scope = "project"
		s.ReadOnlyTools = nil
		s.SideEffectsTools = []string{"put"}
	})
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-canon", ProjectID: "p"}, Servers: []ServerSpec{spec}, Policy: allow(map[string][]string{"rec": {"*"}})})
	cs := e.connect(ep)
	// a duplicate key (the first one is harmless-looking), keys out of order, a number spelled with an exponent
	raw := json.RawMessage(`{"path":"/safe/place","zeta":1e2,"path":"/etc/cron.d/evil","alpha":"aA"}`)
	r := callTool(t, cs, "rec__put", raw)
	if r.IsError || !strings.Contains(textOf(r), "stored") {
		t.Fatalf("the approved write did not run: %s", textOf(r))
	}
	if len(asked) != 1 || len(received()) != 1 {
		t.Fatalf("asked %d, received %d", len(asked), len(received()))
	}
	sum := sha256.Sum256([]byte(received()[0]))
	if hex.EncodeToString(sum[:]) != asked[0].ArgsHash {
		t.Fatalf("the server received arguments that were not the ones hashed for the approval:\nreceived %s\nshown    %s", received()[0], asked[0].Args)
	}
	if received()[0] != string(asked[0].Args) {
		t.Fatalf("received %s, shown %s", received()[0], asked[0].Args)
	}
	if strings.Contains(received()[0], "/safe/place") {
		t.Fatalf("the shadowed duplicate reached the server: %s", received()[0])
	}
}

// The SDK serves each request on a goroutine of its own and does not recover. A panic anywhere under a tool call (here an approver
// with a bug) used to end the whole application; it is now an error to that one call and the run goes on (audit 2026-10-16, H9).
func TestAPanicUnderAToolCallIsAnErrorToThatCallOnly(t *testing.T) {
	ts, received := startRecordingUpstream(t)
	var reports int
	safe.SetReporter(func(safe.Panic) { reports++ })
	t.Cleanup(func() { safe.SetReporter(nil) })
	armed := true
	e := newEnv(t, func(o *Options) {
		o.Approver = approverFunc(func(r ApprovalRequest) (bool, error) {
			if armed {
				panic("a bug in the approver")
			}
			return true, nil
		})
	})
	spec := httpSpec("rec", ts.URL, func(s *ServerSpec) {
		s.Trust = TrustPrivateData
		s.DataClass = ContentPrivate
		s.Cache.Scope = "project"
		s.ReadOnlyTools = nil
		s.SideEffectsTools = []string{"put"}
	})
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-panic", ProjectID: "p"}, Servers: []ServerSpec{spec}, Policy: allow(map[string][]string{"rec": {"*"}})})
	cs := e.connect(ep)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "rec__put", Arguments: map[string]any{"x": 1}})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("a call that panicked looks fine: %+v", res)
	}
	if len(received()) != 0 {
		t.Fatal("the call reached the server although its approval panicked")
	}
	if reports != 1 {
		t.Fatalf("%d panics reported", reports)
	}
	// the run still serves calls
	armed = false
	if r := callTool(t, cs, "rec__put", map[string]any{"x": 2}); r.IsError || len(received()) != 1 {
		t.Fatalf("the run did not survive: %s", textOf(r))
	}
}
