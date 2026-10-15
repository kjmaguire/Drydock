//go:build windows

package runners

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// FreeBytes is the space the current user may still write on the volume holding path.
func FreeBytes(path string) (uint64, error) {
	root := filepath.VolumeName(path) + `\`
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}

// DevDrives lists the fixed ReFS volumes that Windows says are Dev Drives ("X:"), asking `fsutil devdrv query` about each.
func DevDrives(ctx context.Context) []Drive {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []Drive
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + ":"
		root, _ := windows.UTF16PtrFromString(letter + `\`)
		if windows.GetDriveType(root) != windows.DRIVE_FIXED {
			continue
		}
		fs := make([]uint16, 64)
		if err := windows.GetVolumeInformation(root, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
			continue
		}
		if windows.UTF16ToString(fs) != "ReFS" {
			continue
		}
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		cmd := exec.CommandContext(qctx, "fsutil", "devdrv", "query", letter)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		b, _ := cmd.Output()
		cancel()
		if d, ok := parseDevDrive(letter, string(b)); ok {
			out = append(out, d)
		}
	}
	return out
}
