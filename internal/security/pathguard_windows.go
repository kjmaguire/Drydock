//go:build windows

package security

import "golang.org/x/sys/windows"

// longPathName expands 8.3 short names with GetLongPathNameW. A missing path returns an error satisfying
// errors.Is(err, fs.ErrNotExist).
func longPathName(p string) (string, error) {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetLongPathName(in, &buf[0], uint32(len(buf)))
		if err != nil {
			return "", err
		}
		if int(n) <= len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
}
