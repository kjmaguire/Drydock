package peer

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// The parsers are in a file of their own, built on every platform, so the Windows table layout is tested on the development host.

// Windows TCP table layout (iphlpapi, tcpmib.h): MIB_TCPTABLE_OWNER_PID is a DWORD dwNumEntries followed by that many
// MIB_TCPROW_OWNER_PID rows of six DWORDs: dwState, dwLocalAddr, dwLocalPort, dwRemoteAddr, dwRemotePort, dwOwningPid.
// MIB_TCP6TABLE_OWNER_PID is a DWORD count followed by MIB_TCP6ROW_OWNER_PID rows: ucLocalAddr[16], dwLocalScopeId,
// dwLocalPort, ucRemoteAddr[16], dwRemoteScopeId, dwRemotePort, dwState, dwOwningPid. Addresses are in network byte order (the
// bytes in memory are the address's bytes in order); a port is in network byte order in the low two bytes of its DWORD.
// Integers are little-endian (Windows on x64 and arm64).
const (
	tcpRowSize  = 24
	tcp6RowSize = 56
)

// port reads a port DWORD: the first two bytes in memory are the port in network order.
func port(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }

// ParseTCPTable parses a MIB_TCPTABLE_OWNER_PID buffer.
func ParseTCPTable(b []byte) ([]Row, error) {
	if len(b) < 4 {
		return nil, errors.New("peer: the TCP table is shorter than its count")
	}
	n := int(binary.LittleEndian.Uint32(b))
	if n < 0 || n > (len(b)-4)/tcpRowSize {
		return nil, fmt.Errorf("peer: the TCP table says %d rows but holds %d bytes", n, len(b))
	}
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		r := b[4+i*tcpRowSize : 4+(i+1)*tcpRowSize]
		local := netip.AddrFrom4([4]byte(r[4:8]))
		remote := netip.AddrFrom4([4]byte(r[12:16]))
		rows = append(rows, Row{Local: netip.AddrPortFrom(local, port(r[8:12])), Remote: netip.AddrPortFrom(remote, port(r[16:20])),
			PID: int(binary.LittleEndian.Uint32(r[20:24]))})
	}
	return rows, nil
}

// ParseTCP6Table parses a MIB_TCP6TABLE_OWNER_PID buffer.
func ParseTCP6Table(b []byte) ([]Row, error) {
	if len(b) < 4 {
		return nil, errors.New("peer: the TCP6 table is shorter than its count")
	}
	n := int(binary.LittleEndian.Uint32(b))
	if n < 0 || n > (len(b)-4)/tcp6RowSize {
		return nil, fmt.Errorf("peer: the TCP6 table says %d rows but holds %d bytes", n, len(b))
	}
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		r := b[4+i*tcp6RowSize : 4+(i+1)*tcp6RowSize]
		local := netip.AddrFrom16([16]byte(r[0:16]))
		remote := netip.AddrFrom16([16]byte(r[24:40]))
		rows = append(rows, Row{Local: netip.AddrPortFrom(local, port(r[20:24])), Remote: netip.AddrPortFrom(remote, port(r[44:48])),
			PID: int(binary.LittleEndian.Uint32(r[52:56]))})
	}
	return rows, nil
}

// ParseProcNetTCP parses Linux's /proc/net/tcp (v6 false) or /proc/net/tcp6 (v6 true): after a header line, one connection per
// line with "sl local_address rem_address st ... uid timeout inode ...", addresses as hex words in the kernel's (host, here
// little-endian) byte order and ports as big hex numbers.
func ParseProcNetTCP(text string, v6 bool) ([]Row, error) {
	var rows []Row
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		local, err := procAddr(f[1], v6)
		if err != nil {
			return nil, err
		}
		remote, err := procAddr(f[2], v6)
		if err != nil {
			return nil, err
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("peer: bad inode %q", f[9])
		}
		rows = append(rows, Row{Local: local, Remote: remote, Inode: inode})
	}
	return rows, sc.Err()
}

// procAddr parses "0100007F:1F90" (IPv4) or 32 hex digits and a port (IPv6).
func procAddr(s string, v6 bool) (netip.AddrPort, error) {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, fmt.Errorf("peer: bad address %q", s)
	}
	pn, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("peer: bad port in %q", s)
	}
	raw, err := hex.DecodeString(h)
	if err != nil || (!v6 && len(raw) != 4) || (v6 && len(raw) != 16) {
		return netip.AddrPort{}, fmt.Errorf("peer: bad address %q", s)
	}
	// each 32-bit word is in host order: reverse its bytes
	for w := 0; w+4 <= len(raw); w += 4 {
		raw[w], raw[w+1], raw[w+2], raw[w+3] = raw[w+3], raw[w+2], raw[w+1], raw[w]
	}
	var a netip.Addr
	if v6 {
		a = netip.AddrFrom16([16]byte(raw))
	} else {
		a = netip.AddrFrom4([4]byte(raw))
	}
	return netip.AddrPortFrom(a, uint16(pn)), nil
}
