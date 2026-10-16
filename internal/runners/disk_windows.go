//go:build windows

package runners

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/kjmaguire/drydock/internal/proc"
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

// fsctlQueryPersistentVolumeState is FSCTL_QUERY_PERSISTENT_VOLUME_STATE (ntifs.h): CTL_CODE(FILE_DEVICE_FILE_SYSTEM, 143,
// METHOD_BUFFERED, FILE_ANY_ACCESS). It needs no access right on the volume, so any user may ask.
const fsctlQueryPersistentVolumeState = 0x9023C

// fileFsPersistentVolumeInformation is FILE_FS_PERSISTENT_VOLUME_INFORMATION (ntifs.h): the flags asked for (FlagMask) go in
// and the volume's flags (VolumeFlags) come out. Version is 1.
type fileFsPersistentVolumeInformation struct {
	VolumeFlags uint32
	FlagMask    uint32
	Version     uint32
	Reserved    uint32
}

// volumeStateFlags asks volume "X:" for its Dev Drive bits without administrator rights: the volume is opened with no access
// right (FILE_SHARE_READ|FILE_SHARE_WRITE, OPEN_EXISTING) and FSCTL_QUERY_PERSISTENT_VOLUME_STATE is sent. Windows 11 22H2
// (September 2023 update) and later know the two bits; an older Windows answers with an error or with the bits clear.
func volumeStateFlags(letter string) (uint32, error) {
	p, err := windows.UTF16PtrFromString(`\\.\` + letter)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return 0, fmt.Errorf("opening the volume: %w", err)
	}
	defer windows.CloseHandle(h)
	in := fileFsPersistentVolumeInformation{FlagMask: persistentVolumeStateDevVolume | persistentVolumeStateTrustedVolume, Version: 1}
	var out fileFsPersistentVolumeInformation
	var n uint32
	if err := windows.DeviceIoControl(h, fsctlQueryPersistentVolumeState,
		(*byte)(unsafe.Pointer(&in)), uint32(unsafe.Sizeof(in)),
		(*byte)(unsafe.Pointer(&out)), uint32(unsafe.Sizeof(out)), &n, nil); err != nil {
		return 0, fmt.Errorf("FSCTL_QUERY_PERSISTENT_VOLUME_STATE: %w", err)
	}
	return out.VolumeFlags, nil
}

// fsutilDevDrive is the fallback for a Windows whose volume state query failed: `fsutil devdrv query X:` (which needs
// administrator rights, so from an unelevated Drydock it usually prints an error). It returns what fsutil said, in one line.
func fsutilDevDrive(ctx context.Context, letter string) (Drive, bool, string) {
	qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(qctx, "fsutil", "devdrv", "query", letter)
	proc.Harden(cmd)
	b, err := cmd.CombinedOutput()
	out := firstLine(string(b))
	if out == "" && err != nil {
		out = err.Error()
	}
	d, ok := parseDevDrive(letter, string(b))
	return d, ok, out
}

// checkVolume says whether the fixed ReFS volume "X:" is a Dev Drive: by its volume state flags, else (when that query fails)
// by what fsutil says.
func checkVolume(ctx context.Context, letter string) volumeCheck {
	c := volumeCheck{Letter: letter}
	flags, err := volumeStateFlags(letter)
	if err == nil {
		c.Drive, c.IsDev = devDriveFromFlags(letter, flags)
		return c
	}
	c.QueryErr = err
	c.Drive, c.IsDev, c.Fsutil = fsutilDevDrive(ctx, letter)
	return c
}

// isFixedReFS reports whether "X:" is a fixed drive formatted with ReFS, which every Dev Drive is.
func isFixedReFS(letter string) bool {
	root, err := windows.UTF16PtrFromString(letter + `\`)
	if err != nil {
		return false
	}
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return false
	}
	fs := make([]uint16, 64)
	if err := windows.GetVolumeInformation(root, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		return false
	}
	return windows.UTF16ToString(fs) == "ReFS"
}

// DevDrives lists the fixed ReFS volumes that Windows says are Dev Drives ("X:"), asking each volume for its state flags (no
// administrator rights needed) and falling back to `fsutil devdrv query` where that query fails. When none is found it says
// why.
func DevDrives(ctx context.Context) DevDriveScan {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return devDriveScan(nil, err)
	}
	var checks []volumeCheck
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A'+i)) + ":"
		if !isFixedReFS(letter) {
			continue
		}
		checks = append(checks, checkVolume(ctx, letter))
	}
	return devDriveScan(checks, nil)
}
