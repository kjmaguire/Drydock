//go:build windows

package sandbox

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow keeps the engine client from opening a console window: Drydock is a windowed app, and every console program it
// starts would otherwise flash one.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
