//go:build windows

package datalock

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockOffset is where the claim sits in the file: one byte at 1 GiB, far past the process id the owner writes at the start.
// A lock on Windows is mandatory, not advisory: a byte range locked by one handle cannot be read by any other, so a lock on
// the first byte would hide the process id from the second launch that wants to name it. A range past the end of the file is
// allowed (SQLite's pending byte is placed the same way).
const lockOffset = 1 << 30

// LockFileEx locks a range of bytes for the handle; one byte is enough to make the folder one process's.
func lockFile(f *os.File) error {
	ol := &windows.Overlapped{Offset: lockOffset}
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
}

func unlockFile(f *os.File) error {
	ol := &windows.Overlapped{Offset: lockOffset}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
