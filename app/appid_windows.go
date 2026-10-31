//go:build windows

package app

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32                                     = windows.NewLazySystemDLL("shell32.dll")
	procSetCurrentProcessExplicitAppUserModelID = shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	procGetCurrentProcessExplicitAppUserModelID = shell32.NewProc("GetCurrentProcessExplicitAppUserModelID")
)

// setAppUserModelID gives this process Drydock's application identity. It must run before the first window is made: Windows
// reads it when a window first shows on the taskbar. Only the app calls it; a sandbox app's window (sandboxview), a process
// of its own, keeps the identity Windows derives, so it gets a taskbar button of its own and is never taken for Drydock.
func setAppUserModelID(id string) error {
	p, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return err
	}
	if hr, _, _ := procSetCurrentProcessExplicitAppUserModelID.Call(uintptr(unsafe.Pointer(p))); hr != 0 {
		return fmt.Errorf("SetCurrentProcessExplicitAppUserModelID: HRESULT 0x%08x", uint32(hr))
	}
	return nil
}

// currentAppUserModelID is the identity this process set, or "" when it set none.
func currentAppUserModelID() (string, error) {
	var p *uint16
	hr, _, _ := procGetCurrentProcessExplicitAppUserModelID.Call(uintptr(unsafe.Pointer(&p)))
	if hr != 0 {
		return "", fmt.Errorf("GetCurrentProcessExplicitAppUserModelID: HRESULT 0x%08x", uint32(hr))
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(p))
	return windows.UTF16PtrToString(p), nil
}
