//go:build windows

package app

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// restartAfterEnv carries the process id of the Drydock that asked to be restarted to the one it started. The new process waits
// for that one to end before its single-instance check, which would otherwise hand the launch to the closing app and exit.
const restartAfterEnv = "DRYDOCK_RESTART_AFTER"

// restartWait is how long a restarted Drydock waits for the old one to close before it starts anyway.
const restartWait = 60 * time.Second

// restart starts this executable again and quits (decision P116: "Restart now" after a staged restore). The new process breaks
// away from the app's kill-on-close job (which allows it, proc.EnterAppJob) or it would die with this one; the app job ends every
// agent process tree when this process exits, as on any quit.
func restart(wa *application.App) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), restartAfterEnv+"="+strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Drydock could not start again: %w", err)
	}
	_ = cmd.Process.Release()
	// the answer to the page goes out first; the window closes a moment later
	time.AfterFunc(300*time.Millisecond, wa.Quit)
	return nil
}

// waitForRestartedFrom waits, in a process started by restart, for the old one to end. Anything else in the variable is ignored.
func waitForRestartedFrom() {
	v := os.Getenv(restartAfterEnv)
	if v == "" {
		return
	}
	_ = os.Unsetenv(restartAfterEnv) // agents started by this process must not see it
	pid, err := strconv.ParseUint(v, 10, 32)
	if err != nil || pid == 0 || int(pid) == os.Getpid() {
		return
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, uint32(restartWait/time.Millisecond))
}
