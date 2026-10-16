//go:build !windows

package runners

import (
	"context"

	"golang.org/x/sys/unix"
)

// FreeBytes is the space an unprivileged user may still write on the file system holding path.
func FreeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// DevDrives is Windows only: nothing is found and there is no reason to give.
func DevDrives(context.Context) DevDriveScan { return DevDriveScan{} }
