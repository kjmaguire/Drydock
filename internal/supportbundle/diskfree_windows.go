//go:build windows

package supportbundle

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// DiskFree reports the free and total bytes of the volume holding path.
func DiskFree(path string) (free, total uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var avail, tot, totFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &tot, &totFree); err != nil {
		return 0, 0, err
	}
	return avail, tot, nil
}

// OSVersion returns the Windows version and build, for example "10.0.22631".
func OSVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
