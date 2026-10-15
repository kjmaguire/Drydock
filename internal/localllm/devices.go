package localllm

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// Device is a compute device llama.cpp found.
type Device struct {
	ID    string `json:"id"`   // "CUDA0", "Vulkan0"
	Kind  string `json:"kind"` // CUDA | Vulkan | ROCm | CPU | other
	Name  string `json:"name"`
	Total uint64 `json:"totalBytes"`
	Free  uint64 `json:"freeBytes"`
}

// Devices is what `llama-server --list-devices` said.
type Devices struct {
	Probed  bool     `json:"probed"` // false: the program did not answer; nothing is known
	Devices []Device `json:"devices"`
	Error   string   `json:"error,omitempty"`
}

// deviceLine is "  CUDA0: NVIDIA GeForce RTX 4060 Ti (15944 MiB, 14143 MiB free)".
var deviceLine = regexp.MustCompile(`^\s+([A-Za-z]+\d*):\s+(.*?)\s*\((\d+)\s*([KMGT]i?B),\s*(\d+)\s*([KMGT]i?B)\s+free\)\s*$`)

// ParseDevices reads the "Available devices:" block of the program's output.
func ParseDevices(out string) (Devices, bool) {
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "Available devices:") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return Devices{}, false
	}
	d := Devices{Probed: true, Devices: []Device{}}
	for _, l := range lines[start:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			break
		}
		if strings.TrimSpace(l) == "(none)" {
			continue
		}
		m := deviceLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		dev := Device{ID: m[1], Name: strings.TrimSpace(m[2]), Total: bytesOf(m[3], m[4]), Free: bytesOf(m[5], m[6])}
		dev.Kind = kindOf(dev.ID)
		d.Devices = append(d.Devices, dev)
	}
	return d, true
}

func kindOf(id string) string {
	l := strings.ToLower(id)
	switch {
	case strings.HasPrefix(l, "cuda"):
		return "CUDA"
	case strings.HasPrefix(l, "vulkan"):
		return "Vulkan"
	case strings.HasPrefix(l, "rocm"), strings.HasPrefix(l, "hip"):
		return "ROCm"
	case strings.HasPrefix(l, "cpu"):
		return "CPU"
	}
	return "other"
}

func bytesOf(n, unit string) uint64 {
	v, _ := strconv.ParseUint(n, 10, 64)
	u := strings.ToUpper(unit)
	step := uint64(1000)
	if strings.Contains(u, "I") {
		step = 1024
	}
	switch u[0] {
	case 'K':
		return v * step
	case 'M':
		return v * step * step
	case 'G':
		return v * step * step * step
	case 'T':
		return v * step * step * step * step
	}
	return v
}

// ListDevices asks the installed server program for its devices (`--list-devices`, 20 s at most). A program that does not
// answer leaves Probed false: the caller then knows nothing rather than "CPU only".
func ListDevices(ctx context.Context, pm proc.Manager, program string, args []string) Devices {
	res := proc.Probe(ctx, pm, proc.Spec{Path: program, Args: append(append([]string{}, args...), "--list-devices"), Dir: dirOf(program), Label: "llama-devices"}, 20*time.Second)
	if res.Err != nil && res.Stdout == "" && res.Stderr == "" {
		return Devices{Error: res.Err.Error()}
	}
	d, ok := ParseDevices(res.Stdout + "\n" + res.Stderr)
	if !ok {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = "the program printed no device list"
		}
		return Devices{Error: msg}
	}
	return d
}
