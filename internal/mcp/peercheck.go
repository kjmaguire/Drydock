package mcp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// The peer check (decision P122; section 2 "Peer check"): hardening on top of the run's bearer token. For a run started with
// RunConfig.PeerCheck, a request is answered only when the process that opened the TCP connection belongs to the run's agent:
// Options.PeerOwner maps the connection to a process id (internal/peer: GetExtendedTcpTable on Windows, /proc on Linux) and
// Options.PeerMember asks whether that process is in the tree of the process the runtime bound to the run (internal/proc.InTree:
// the run's Job Object on Windows, the parent chain on Linux). The answer is kept per connection, so a keep-alive connection is
// looked up once. A platform without a lookup (peer.ErrUnsupported) is not checked and the run says so once in
// mcp.peer_unchecked; any other failure refuses, because a token alone is then all that stands between another program and the
// run.

// EventPeerRefused is a request refused because its connection does not come from the run's agent.
const (
	EventPeerRefused   = "mcp.peer_refused"
	EventPeerUnchecked = "mcp.peer_unchecked"
)

// peerBindWait is how long a request waits for the runtime to say which process the run belongs to: the CLI connects only after
// it started, and the runtime binds the moment its process exists, so this is a guard, not a delay anyone should see.
var peerBindWait = 10 * time.Second

// ErrPeerUnsupported is what Options.PeerOwner returns on a platform where a connection's process cannot be found.
var ErrPeerUnsupported = errors.New("mcp: the peer check is not supported on this platform")

type peerState struct {
	bound chan struct{}
	once  sync.Once
	mu    sync.Mutex
	roots []int // the session's processes, newest last
	noted bool  // mcp.peer_unchecked was published
}

// maxPeerRoots bounds the processes one run remembers (a headless CLI starts one process per turn).
const maxPeerRoots = 64

func newPeerState() *peerState { return &peerState{bound: make(chan struct{})} }

// BindPeer tells the gateway that a process belongs to a run (the agent CLI's; a CLI that starts one process per turn binds
// each), so its connections can be checked. The runtime calls it as soon as a process is started; a run started without
// PeerCheck ignores it.
func (g *Gateway) BindPeer(runID string, pid int) {
	r := g.run(runID)
	if r == nil || r.peer == nil || pid <= 0 {
		return
	}
	r.peer.mu.Lock()
	r.peer.roots = append(r.peer.roots, pid)
	if len(r.peer.roots) > maxPeerRoots {
		r.peer.roots = r.peer.roots[len(r.peer.roots)-maxPeerRoots:]
	}
	r.peer.mu.Unlock()
	r.peer.once.Do(func() { close(r.peer.bound) })
}

type connKey struct{}

// connPeer holds, per TCP connection, the runs whose check it passed.
type connPeer struct {
	mu   sync.Mutex
	runs map[string]bool
}

func (g *Gateway) connContext(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, &connPeer{runs: map[string]bool{}})
}

// peerOK checks a request's connection for a run with the check on. It writes the refusal itself and returns false.
func (g *Gateway) peerOK(w http.ResponseWriter, req *http.Request, run *Run) bool {
	if run.peer == nil || g.opts.PeerOwner == nil || g.opts.PeerMember == nil {
		return true
	}
	cp, _ := req.Context().Value(connKey{}).(*connPeer)
	if cp != nil {
		cp.mu.Lock()
		ok := cp.runs[run.cfg.Info.RunID]
		cp.mu.Unlock()
		if ok {
			return true
		}
	}
	refuse := func(reason string, pid int) bool {
		g.peerRefused(req, run, reason, pid)
		http.Error(w, "forbidden: this connection does not come from the run's agent", http.StatusForbidden)
		return false
	}
	t := time.NewTimer(peerBindWait)
	defer t.Stop()
	select {
	case <-run.peer.bound:
	case <-run.done:
		return refuse("the run has ended", 0)
	case <-req.Context().Done():
		return false
	case <-t.C:
		return refuse("the run's agent process was never bound", 0)
	}
	run.peer.mu.Lock()
	roots := append([]int(nil), run.peer.roots...)
	run.peer.mu.Unlock()
	client, err1 := netip.ParseAddrPort(req.RemoteAddr)
	la, _ := req.Context().Value(http.LocalAddrContextKey).(net.Addr)
	var server netip.AddrPort
	var err2 error
	if la != nil {
		server, err2 = netip.ParseAddrPort(la.String())
	} else {
		err2 = errors.New("no local address")
	}
	if err1 != nil || err2 != nil {
		return refuse("the connection's addresses are unknown", 0)
	}
	pid, err := g.opts.PeerOwner(client, server)
	if errors.Is(err, ErrPeerUnsupported) {
		run.peer.mu.Lock()
		first := !run.peer.noted
		run.peer.noted = true
		run.peer.mu.Unlock()
		if first {
			g.publish(req.Context(), EventPeerUnchecked, run.cfg.Info, "", map[string]any{"reason": "this platform cannot find a connection's process"})
		}
		return true
	}
	if err != nil {
		return refuse("the connection's process could not be found", 0)
	}
	in := false
	for i := len(roots) - 1; i >= 0 && !in; i-- {
		ok, err := g.opts.PeerMember(roots[i], pid)
		in = err == nil && ok
	}
	if !in {
		return refuse("the connecting process is not the run's agent or one it started", pid)
	}
	if cp != nil {
		cp.mu.Lock()
		cp.runs[run.cfg.Info.RunID] = true
		cp.mu.Unlock()
	}
	return true
}

// peerRefused publishes mcp.peer_refused with the same limits as mcp.auth_failed: a flood cannot fill the event log.
func (g *Gateway) peerRefused(req *http.Request, run *Run, reason string, pid int) {
	g.authMu.Lock()
	key := "peer:" + run.cfg.Info.RunID
	b := g.authSeen[key]
	if b == nil {
		if len(g.authSeen) >= 1024 {
			g.authSeen = map[string]*bucket{}
		}
		b = newBucket(5, 5.0/60, g.now)
		g.authSeen[key] = b
	}
	g.authMu.Unlock()
	if !b.allow() || !g.authAll.allow() {
		return
	}
	data := map[string]any{"reason": reason, "remote": req.RemoteAddr}
	if pid > 0 {
		data["pid"] = pid
	}
	g.publish(req.Context(), EventPeerRefused, run.cfg.Info, "", data)
}
