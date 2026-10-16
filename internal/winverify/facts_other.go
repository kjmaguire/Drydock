//go:build !windows

package winverify

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// osFacts reads the system name and kernel release, the CPU model and the memory size from /etc/os-release and /proc
// (Linux, the development host). Other systems get their GOOS name.
func osFacts(f *Facts) {
	f.OS = runtime.GOOS
	if v := readKeyValue("/etc/os-release", "PRETTY_NAME", "="); v != "" {
		f.OS = strings.Trim(v, `"`)
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		f.Kernel = strings.TrimSpace(string(b))
	}
	f.CPU = readKeyValue("/proc/cpuinfo", "model name", ":")
	if v := readKeyValue("/proc/meminfo", "MemTotal", ":"); v != "" {
		if kb, err := strconv.ParseUint(strings.Fields(v)[0], 10, 64); err == nil {
			f.RAMBytes = kb * 1024
		}
	}
}

// readKeyValue returns the value of the first "key<sep>value" line of a file.
func readKeyValue(path, key, sep string) string {
	fh, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), sep)
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
