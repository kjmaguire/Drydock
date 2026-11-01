// Package peer finds the process on the other end of a loopback TCP connection (decision P122, docs/mcp.md section 2 "Peer
// check"). The MCP gateway listens on 127.0.0.1 and every run has its own bearer token; the peer check is hardening on top of the
// token: a request for a run is answered only when the process that opened the connection belongs to that run's agent (the
// agent CLI itself or something it started, such as the stdio shim). Another program on the same computer that got hold of the
// token, from the run's config file or a process listing, is refused.
//
// Windows: the TCP table with owning process ids (iphlpapi GetExtendedTcpTable with TCP_TABLE_OWNER_PID_CONNECTIONS, for IPv4
// and IPv6) gives the process of the client side of the connection; that the table is read and parsed as written here is checked
// by unit tests of the parser on Linux and was cross-compiled and vetted, but never run on Windows (UNVERIFIED). Linux (the
// development host): /proc/net/tcp and /proc/net/tcp6 give the socket's inode, and the process holding that inode is found under
// /proc/<pid>/fd. Elsewhere Owner reports ErrUnsupported and the gateway does not check.
package peer

import (
	"errors"
	"net/netip"
)

// ErrUnsupported is returned where this platform has no way to find a connection's process.
var ErrUnsupported = errors.New("peer: finding the process of a connection is not supported on this platform")

// ErrNotFound is returned when no process holds the connection (it closed, or belongs to a process this user cannot see).
var ErrNotFound = errors.New("peer: no process found for the connection")

// Row is one TCP connection of the system's table: its two ends and the process that owns it (0 when the table does not say).
type Row struct {
	Local, Remote netip.AddrPort
	PID           int
	Inode         uint64 // Linux only: the socket's inode
}

// Owner returns the process id of the client end of a loopback connection: the connection whose local end is client and whose
// remote end is server (the gateway's listening address as the client sees it).
func Owner(client, server netip.AddrPort) (int, error) {
	return owner(unmap(client), unmap(server))
}

// unmap turns an IPv4-mapped IPv6 address into IPv4, as the tables list a connection to 127.0.0.1.
func unmap(a netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(a.Addr().Unmap(), a.Port())
}

// match finds the row of the connection from client to server.
func match(rows []Row, client, server netip.AddrPort) (Row, bool) {
	for _, r := range rows {
		if unmap(r.Local) == client && unmap(r.Remote) == server {
			return r, true
		}
	}
	return Row{}, false
}
