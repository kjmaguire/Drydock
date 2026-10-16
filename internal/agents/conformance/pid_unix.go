//go:build !windows

package conformance

import (
	"errors"
	"syscall"
)

// processGone reports whether pid no longer exists (signal 0 probes without delivering anything).
func processGone(pid int) (gone, known bool) {
	err := syscall.Kill(pid, 0)
	return errors.Is(err, syscall.ESRCH), true
}
