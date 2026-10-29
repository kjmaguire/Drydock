package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/peer"
	"github.com/kjmaguire/drydock/internal/proc"
)

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`

// postInitialize posts initialize to a run's endpoint and returns the status (0 when the request failed).
func postInitialize(url, token string) int {
	req, _ := http.NewRequest("POST", url, strings.NewReader(initializeBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func realPeer(o *Options) {
	o.PeerOwner = func(c, s netip.AddrPort) (int, error) { return peer.Owner(c, s) }
	o.PeerMember = proc.InTree
}

// The peer check on the development host, with the real lookups (internal/peer reads /proc, internal/proc follows the parents):
// a run bound to a process answers that process and what it starts (the shim), and refuses another process holding the token.
func TestPeerCheckAnswersOnlyTheRunsProcessTree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the live lookups are tested on Linux; Windows is unverified")
	}
	e := newEnv(t, realPeer)
	// a child of this test plays the agent's shim: bound to the run, it is answered
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-peer"}, PeerCheck: true})
	exe, _ := os.Executable()
	var out bytes.Buffer
	p, err := proc.New().Start(context.Background(), proc.Spec{Path: exe, Env: append(os.Environ(), modeEnv+"=peerpost", "DRYDOCK_MCP_URL="+ep.URL, "DRYDOCK_MCP_TOKEN="+ep.Token)})
	if err != nil {
		t.Fatal(err)
	}
	e.g.BindPeer("r-peer", p.PID())
	_, _ = io.Copy(&out, p.Stdout())
	proc.WaitTimeout(p, 30*time.Second)
	if strings.TrimSpace(out.String()) != "200" {
		t.Fatalf("the run's own process was not answered: %q", out.String())
	}
	// this test process has the token too, but is not in the bound process's tree: refused, and the event says why, no token
	if c := postInitialize(ep.URL, ep.Token); c != http.StatusForbidden {
		t.Fatalf("another process with the token: %d", c)
	}
	ev := e.events(EventPeerRefused)
	if len(ev) != 1 || !bytes.Contains(ev[0].Data, []byte("not the run's agent")) || bytes.Contains(ev[0].Data, []byte(ep.Token)) {
		t.Fatalf("%+v", ev)
	}
	// bound to this process, the same request is answered (a CLI that starts one process per turn binds each)
	e.g.BindPeer("r-peer", os.Getpid())
	if c := postInitialize(ep.URL, ep.Token); c != http.StatusOK {
		t.Fatalf("after binding this process: %d", c)
	}
	// a run without the check is not affected
	plain := e.startRun(RunConfig{Info: RunInfo{RunID: "r-plain"}})
	if c := postInitialize(plain.URL, plain.Token); c != http.StatusOK {
		t.Fatalf("a run without the check: %d", c)
	}
	// the token still comes first: a wrong one is a 401, not a peer lookup
	if c := postInitialize(ep.URL, "wrong"); c != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", c)
	}
}

// The answer is kept per connection; a run never bound refuses after the wait; a platform without a lookup is not checked and
// says so once.
func TestPeerCheckPerConnectionUnboundAndUnsupported(t *testing.T) {
	old := peerBindWait
	peerBindWait = 300 * time.Millisecond
	t.Cleanup(func() { peerBindWait = old })
	var lookups atomic.Int32
	unsupported := atomic.Bool{}
	e := newEnv(t, func(o *Options) {
		o.PeerOwner = func(c, s netip.AddrPort) (int, error) {
			lookups.Add(1)
			if unsupported.Load() {
				return 0, ErrPeerUnsupported
			}
			return 4242, nil
		}
		o.PeerMember = func(root, pid int) (bool, error) { return root == 4242 && pid == 4242, nil } // a labelled test double
	})
	ep := e.startRun(RunConfig{Info: RunInfo{RunID: "r-conn"}, PeerCheck: true})
	if c := postInitialize(ep.URL, ep.Token); c != http.StatusForbidden {
		t.Fatalf("never bound: %d", c)
	}
	if ev := e.events(EventPeerRefused); len(ev) != 1 || !bytes.Contains(ev[0].Data, []byte("never bound")) {
		t.Fatalf("%+v", ev)
	}
	e.g.BindPeer("r-conn", 4242)
	lookups.Store(0)
	for i := 0; i < 3; i++ {
		if c := postInitialize(ep.URL, ep.Token); c != http.StatusOK {
			t.Fatalf("bound: %d", c)
		}
	}
	if n := lookups.Load(); n != 1 {
		t.Fatalf("a keep-alive connection was looked up %d times", n)
	}
	unsupported.Store(true)
	other := e.startRun(RunConfig{Info: RunInfo{RunID: "r-unsup"}, PeerCheck: true})
	e.g.BindPeer("r-unsup", 1)
	for i := 0; i < 2; i++ {
		if c := postInitialize(other.URL, other.Token); c != http.StatusOK {
			t.Fatalf("unsupported platform: %d", c)
		}
	}
	if ev := e.events(EventPeerUnchecked); len(ev) != 1 {
		t.Fatalf("%+v", ev)
	}
	// an ended run refuses at once
	e.g.EndRun("r-conn")
	if c := postInitialize(ep.URL, ep.Token); c != http.StatusUnauthorized {
		t.Fatalf("ended run: %d", c)
	}
}
