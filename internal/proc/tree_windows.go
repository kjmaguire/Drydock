//go:build windows

package proc

import (
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

// AttachTree puts an already running process in its own kill-on-close Job Object and returns a function that ends the job,
// killing the process and every descendant started after the call. It is for processes Drydock cannot start suspended (a
// ConPTY shell, decision D7: "tree-killed by walking the job after spawn"); a child started in the instant before the
// assignment is not in the job, but the app-level job still ends it with Drydock. UNPROVEN on Windows (P2).
func AttachTree(pid int) (kill func() error, err error) {
	job, err := newKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("assign process %d to a job: %w", pid, err)
	}
	var once sync.Once
	return func() error {
		var err error
		once.Do(func() {
			err = windows.TerminateJobObject(job, 1)
			windows.CloseHandle(job)
		})
		return err
	}, nil
}
