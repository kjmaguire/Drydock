//go:build windows

package backup

import "golang.org/x/sys/windows"

// isRemoteDrive reports whether a drive such as "Z:" is a mapped network drive (UNC volumes are caught by the path text).
func isRemoteDrive(volume string) bool {
	if len(volume) != 2 || volume[1] != ':' {
		return false
	}
	p, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(p) == windows.DRIVE_REMOTE
}
