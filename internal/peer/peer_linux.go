//go:build linux

package peer

import (
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
)

// owner reads the connection's socket inode from /proc/net/tcp{,6} and finds the process holding it under /proc/<pid>/fd.
func owner(client, server netip.AddrPort) (int, error) {
	var inode uint64
	for _, f := range []struct {
		path string
		v6   bool
	}{{"/proc/net/tcp", false}, {"/proc/net/tcp6", true}} {
		b, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		rows, err := ParseProcNetTCP(string(b), f.v6)
		if err != nil {
			return 0, err
		}
		if r, ok := match(rows, client, server); ok && r.Inode != 0 {
			inode = r.Inode
			break
		}
	}
	if inode == 0 {
		return 0, ErrNotFound
	}
	want := "socket:[" + strconv.FormatUint(inode, 10) + "]"
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, p := range procs {
		fds, err := os.ReadDir(filepath.Join(p, "fd"))
		if err != nil {
			continue // another user's process, or gone
		}
		for _, fd := range fds {
			if l, err := os.Readlink(filepath.Join(p, "fd", fd.Name())); err == nil && l == want {
				return strconv.Atoi(filepath.Base(p))
			}
		}
	}
	return 0, ErrNotFound
}
