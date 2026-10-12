//go:build windows

package wsl

import "golang.org/x/sys/windows"

// longPath expands 8.3 short names (C:\Users\KYLEMA~1\AppData\Local\Temp, which Windows puts in %TEMP% for long user names)
// to the long names a distro sees under its mount root. A path that does not exist is returned unchanged.
func longPath(p string) string {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetLongPathName(u, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return p
		}
		if int(n) <= len(buf) {
			return windows.UTF16ToString(buf[:n])
		}
		buf = make([]uint16, n)
	}
}
