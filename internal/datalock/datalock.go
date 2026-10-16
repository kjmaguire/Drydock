// Package datalock lets one process at a time own a data folder (audit 2026-10-16, H10).
//
// Opening the core is not harmless: it marks the agent runs the last stop left open as failed and takes over the workflow runs
// whose owner is gone. A second process on the same folder (a second launch, the updater's helper, a development server) looks to
// the core exactly like a restart after a crash, so it did that to the first one's live runs before it noticed it should not be
// there. The owner holds an exclusive lock on a file in the folder for as long as it runs; the operating system drops the lock
// when the process ends, however it ends, so a crash never leaves the folder locked.
package datalock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileName is the lock file in the data folder.
const FileName = "drydock.lock"

// ErrHeld is returned when another process owns the folder.
var ErrHeld = errors.New("datalock: the data folder is in use by another Drydock")

// Lock is one process's claim on a data folder.
type Lock struct {
	f *os.File
}

// Acquire claims dir. It fails at once with an error that wraps ErrHeld, naming the owner's process id when it can be read, if
// another process (or another Lock in this one) holds it.
func Acquire(dir string) (*Lock, error) {
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("datalock: %w", err)
	}
	if err := lockFile(f); err != nil {
		owner := ""
		if b, rerr := os.ReadFile(path); rerr == nil {
			if pid := strings.TrimSpace(string(b)); pid != "" && len(pid) < 12 {
				if _, perr := strconv.Atoi(pid); perr == nil {
					owner = " (process " + pid + ")"
				}
			}
		}
		_ = f.Close()
		return nil, fmt.Errorf("%w%s: %s", ErrHeld, owner, dir)
	}
	// who holds it, for the message of the next one that finds it taken
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return &Lock{f: f}, nil
}

// Release gives the folder up. It is safe to call twice and on a nil Lock.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}
