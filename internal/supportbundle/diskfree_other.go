//go:build !windows

package supportbundle

import "syscall"

// DiskFree reports the free and total bytes of the volume holding path.
func DiskFree(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), nil
}

// OSVersion describes the operating system (best effort).
func OSVersion() string { return "" }
