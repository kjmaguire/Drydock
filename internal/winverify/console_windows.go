//go:build windows

package winverify

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetConsoleProcessList = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")

// OwnConsole reports whether this process is the only one attached to its console, which is the case when it was started
// by a double-click in Explorer: the window would close as soon as it exits.
func OwnConsole() bool {
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}
