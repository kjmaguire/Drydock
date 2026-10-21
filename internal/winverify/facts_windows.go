//go:build windows

package winverify

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// osFacts reads the Windows name, version and build from the registry and RtlGetVersion, the CPU name from the registry,
// the memory size from GlobalMemoryStatusEx, and three settings that explain some test results.
func osFacts(f *Facts) {
	v := windows.RtlGetVersion()
	f.OS = "Windows"
	f.Build = fmt.Sprintf("%d", v.BuildNumber)
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		if name, _, err := k.GetStringValue("ProductName"); err == nil && name != "" {
			// Windows 11 still says "Windows 10" in ProductName; the build number tells them apart
			if v.BuildNumber >= 22000 && strings.HasPrefix(name, "Windows 10") {
				name = "Windows 11" + strings.TrimPrefix(name, "Windows 10")
			}
			f.OS = name
		}
		if dv, _, err := k.GetStringValue("DisplayVersion"); err == nil {
			f.OSVersion = dv
		}
		if ubr, _, err := k.GetIntegerValue("UBR"); err == nil {
			f.Build = fmt.Sprintf("%d.%d", v.BuildNumber, ubr)
		}
		k.Close()
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		if name, _, err := k.GetStringValue("ProcessorNameString"); err == nil {
			f.CPU = strings.TrimSpace(name)
		}
		k.Close()
	}
	f.RAMBytes = totalMemory()
	elevated := windows.GetCurrentProcessToken().IsElevated()
	f.Elevated = &elevated
	f.DeveloperMode = regFlag(`SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock`, "AllowDevelopmentWithoutDevLicense")
	f.LongPaths = regFlag(`SYSTEM\CurrentControlSet\Control\FileSystem`, "LongPathsEnabled")
}

func regFlag(path, name string) *bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		b := false
		return &b
	}
	defer k.Close()
	n, _, err := k.GetIntegerValue(name)
	b := err == nil && n == 1
	return &b
}

// memoryStatusEx is MEMORYSTATUSEX (x/sys/windows has no wrapper for GlobalMemoryStatusEx).
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func totalMemory() uint64 {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return 0
	}
	return m.TotalPhys
}
