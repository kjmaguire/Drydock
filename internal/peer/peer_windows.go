//go:build windows

package peer

import (
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UNVERIFIED on Windows: cross-compiled and vetted only. golang.org/x/sys/windows has no wrapper for GetExtendedTcpTable, so it is
// called through iphlpapi.dll.
var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	afInet                      = 2
	afInet6                     = 23
	tcpTableOwnerPIDConnections = 4 // TCP_TABLE_OWNER_PID_CONNECTIONS: connections only, no listeners
	errInsufficientBuffer       = 122
)

// table reads the TCP table of one address family, growing the buffer while Windows says it is too small.
func table(af uint32) ([]byte, error) {
	if err := procGetExtendedTcpTable.Find(); err != nil {
		return nil, ErrUnsupported
	}
	size := uint32(16 << 10)
	for attempt := 0; attempt < 5; attempt++ {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDConnections, 0)
		switch r {
		case 0:
			return buf, nil
		case errInsufficientBuffer:
			size += 4 << 10 // connections may have opened meanwhile
			continue
		default:
			return nil, fmt.Errorf("peer: GetExtendedTcpTable failed: %w", windows.Errno(r))
		}
	}
	return nil, errors.New("peer: the TCP table kept growing")
}

func owner(client, server netip.AddrPort) (int, error) {
	af, parse := uint32(afInet), ParseTCPTable
	if client.Addr().Is6() {
		af, parse = afInet6, ParseTCP6Table
	}
	b, err := table(af)
	if err != nil {
		return 0, err
	}
	rows, err := parse(b)
	if err != nil {
		return 0, err
	}
	if r, ok := match(rows, client, server); ok && r.PID != 0 {
		return r.PID, nil
	}
	return 0, ErrNotFound
}
