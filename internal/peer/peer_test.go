package peer

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"runtime"
	"testing"
)

// The Windows TCP tables are built here byte by byte in the layout tcpmib.h documents, so the parser that runs on Windows is
// tested on the development host.
func tcpRow(local, remote netip.AddrPort, pid uint32) []byte {
	r := make([]byte, tcpRowSize)
	binary.LittleEndian.PutUint32(r[0:], 5) // MIB_TCP_STATE_ESTAB
	l4, r4 := local.Addr().As4(), remote.Addr().As4()
	copy(r[4:8], l4[:])
	r[8], r[9] = byte(local.Port()>>8), byte(local.Port())
	copy(r[12:16], r4[:])
	r[16], r[17] = byte(remote.Port()>>8), byte(remote.Port())
	binary.LittleEndian.PutUint32(r[20:], pid)
	return r
}

func tcp6Row(local, remote netip.AddrPort, pid uint32) []byte {
	r := make([]byte, tcp6RowSize)
	l, rm := local.Addr().As16(), remote.Addr().As16()
	copy(r[0:16], l[:])
	r[20], r[21] = byte(local.Port()>>8), byte(local.Port())
	copy(r[24:40], rm[:])
	r[44], r[45] = byte(remote.Port()>>8), byte(remote.Port())
	binary.LittleEndian.PutUint32(r[48:], 5)
	binary.LittleEndian.PutUint32(r[52:], pid)
	return r
}

func buildTable(rows ...[]byte) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(len(rows)))
	for _, r := range rows {
		b = append(b, r...)
	}
	return b
}

func TestParseTheWindowsTCPTables(t *testing.T) {
	gw := netip.MustParseAddrPort("127.0.0.1:50123")
	shim := netip.MustParseAddrPort("127.0.0.1:61001")
	other := netip.MustParseAddrPort("127.0.0.1:61002")
	b := buildTable(tcpRow(shim, gw, 4242), tcpRow(gw, shim, 100), tcpRow(other, gw, 777))
	rows, err := ParseTCPTable(b)
	if err != nil || len(rows) != 3 {
		t.Fatalf("%+v %v", rows, err)
	}
	if r, ok := match(rows, shim, gw); !ok || r.PID != 4242 {
		t.Fatalf("%+v %v", r, ok)
	}
	if r, ok := match(rows, other, gw); !ok || r.PID != 777 {
		t.Fatalf("%+v", r)
	}
	if _, ok := match(rows, netip.MustParseAddrPort("127.0.0.1:1"), gw); ok {
		t.Fatal("matched a connection that is not in the table")
	}
	// a count larger than the buffer is refused, not read past
	bad := buildTable(tcpRow(shim, gw, 1))
	binary.LittleEndian.PutUint32(bad, 9)
	if _, err := ParseTCPTable(bad); err == nil {
		t.Fatal("a short table was accepted")
	}
	if _, err := ParseTCPTable([]byte{1}); err == nil {
		t.Fatal("a buffer without a count was accepted")
	}

	gw6 := netip.MustParseAddrPort("[::1]:50123")
	shim6 := netip.MustParseAddrPort("[::1]:61001")
	rows6, err := ParseTCP6Table(buildTable(tcp6Row(shim6, gw6, 99)))
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := match(rows6, shim6, gw6); !ok || r.PID != 99 {
		t.Fatalf("%+v", rows6)
	}
}

func TestParseProcNetTCP(t *testing.T) {
	// two rows as the Linux kernel writes them: 127.0.0.1:44444 -> 127.0.0.1:8080, and ::1 in tcp6
	v4 := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:AD9C 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 123456 1 0000000000000000 20 4 30 10 -1\n" +
		"   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 99 1 0000000000000000 100 0 0 10 0\n"
	rows, err := ParseProcNetTCP(v4, false)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
	r, ok := match(rows, netip.MustParseAddrPort("127.0.0.1:44444"), netip.MustParseAddrPort("127.0.0.1:8080"))
	if !ok || r.Inode != 123456 {
		t.Fatalf("%+v", rows)
	}
	v6 := "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000000000000000000001000000:AD9C 00000000000000000000000001000000:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 777 1 0000000000000000 20 4 30 10 -1\n"
	rows6, err := ParseProcNetTCP(v6, true)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := match(rows6, netip.MustParseAddrPort("[::1]:44444"), netip.MustParseAddrPort("[::1]:8080")); !ok || r.Inode != 777 {
		t.Fatalf("%+v", rows6)
	}
	if _, err := ParseProcNetTCP("h\n 0: zz:1 00:0 01 a b c d e f 1\n", false); err == nil {
		t.Fatal("a bad address was accepted")
	}
}

// On the development host the lookup runs for real: a connection this test opens to itself belongs to this process.
func TestOwnerFindsThisProcessOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the live lookup is tested on Linux; Windows is unverified")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	acc := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		acc <- c
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sc := <-acc
	defer sc.Close()
	client := netip.MustParseAddrPort(c.LocalAddr().String())
	server := netip.MustParseAddrPort(ln.Addr().String())
	pid, err := Owner(client, server)
	if err != nil || pid != os.Getpid() {
		t.Fatalf("pid %d (want %d): %v", pid, os.Getpid(), err)
	}
	if _, err := Owner(netip.MustParseAddrPort("127.0.0.1:1"), server); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}
