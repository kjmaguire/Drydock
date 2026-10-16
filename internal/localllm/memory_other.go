//go:build !windows

package localllm

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// SystemMemory reads /proc/meminfo on the development host (Drydock runs on Windows; this keeps the tests honest).
func SystemMemory() Memory {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return Memory{}
	}
	defer f.Close()
	var m Memory
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(v)
		if len(fields) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(fields[0], 10, 64)
		switch k {
		case "MemTotal":
			m.Total = n * 1024
		case "MemAvailable":
			m.Available = n * 1024
		}
	}
	m.Known = m.Total > 0 && m.Available > 0
	return m
}
