package localllm

import (
	"context"
	"regexp"
	"slices"
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

// Devices is what `llama-server --list-devices` said, and the graphics adapters Windows reports.
type Devices struct {
	Probed  bool     `json:"probed"` // false: the program did not answer; nothing is known
	Devices []Device `json:"devices"`
	Error   string   `json:"error,omitempty"`
	// Adapters are the display adapters Windows has drivers for ("NVIDIA GeForce RTX 4070"), whatever the program found: a GPU
	// here and none in Devices means this build could not use it, not that the PC has none. Empty off Windows.
	Adapters []string `json:"adapters,omitempty"`
	// Note is what the program said about its backends (lines of ggml_vulkan, ggml_cuda, load_backend) when it listed no GPU:
	// why it could not use one, in its own words.
	Note string `json:"note,omitempty"`
}

// HasGPU says whether the program listed a device other than the CPU.
func (d Devices) HasGPU() bool {
	for _, x := range d.Devices {
		if x.Kind != "CPU" {
			return true
		}
	}
	return false
}

// deviceLine is "  CUDA0: NVIDIA GeForce RTX 4060 Ti (15944 MiB, 14143 MiB free)"; plainLine is a device without its memory
// ("  Vulkan0: AMD Radeon RX 7800 XT"), which some builds print for a device that does not report it.
var (
	deviceLine = regexp.MustCompile(`^\s+([A-Za-z]+\d*):\s+(.*?)\s*\((\d+)\s*([KMGT]i?B),\s*(\d+)\s*([KMGT]i?B)\s+free\)\s*$`)
	plainLine  = regexp.MustCompile(`^\s+([A-Za-z]+\d*):\s+(\S.*?)\s*$`)
)

// backendLine is a line in which the program says what it made of a backend: found, loaded, failed or skipped.
var backendLine = regexp.MustCompile(`(?i)^\s*(ggml_vulkan|ggml_cuda\w*|ggml_hip\w*|ggml_backend\w*|load_backend|register_backend)\b.*|(?i).*\b(no (vulkan|cuda|gpu) devices?|failed to load|vulkan.*(error|not found))\b.*`)

// backendNote collects the program's backend lines (at most 12, each at most 200 characters) for Devices.Note.
func backendNote(out string) string {
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || !backendLine.MatchString(l) {
			continue
		}
		if len(l) > 200 {
			l = l[:200] + "..."
		}
		keep = append(keep, l)
		if len(keep) == 12 {
			break
		}
	}
	return strings.Join(keep, "\n")
}

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
		var dev Device
		if m := deviceLine.FindStringSubmatch(l); m != nil {
			dev = Device{ID: m[1], Name: strings.TrimSpace(m[2]), Total: bytesOf(m[3], m[4]), Free: bytesOf(m[5], m[6])}
		} else if m := plainLine.FindStringSubmatch(l); m != nil {
			dev = Device{ID: m[1], Name: m[2]}
		} else {
			continue
		}
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

// ListDevices asks the installed server program for its devices (`--list-devices`, 20 s at most) and Windows for its display
// adapters. A program that does not answer leaves Probed false: the caller then knows nothing rather than "CPU only". When it
// lists no GPU, Note keeps what it said about its backends.
func ListDevices(ctx context.Context, pm proc.Manager, program string, args []string) Devices {
	d := listDevices(ctx, pm, program, args)
	d.Adapters = DisplayAdapters()
	return d
}

func listDevices(ctx context.Context, pm proc.Manager, program string, args []string) Devices {
	// the same environment the server itself starts with, not the app's whole one (audit 2026-10-16, L31)
	res := proc.Probe(ctx, pm, proc.Spec{Path: program, Args: append(append([]string{}, args...), "--list-devices"), Dir: dirOf(program), Env: childEnv(), Label: "llama-devices"}, 20*time.Second)
	if res.Err != nil && res.Stdout == "" && res.Stderr == "" {
		return Devices{Error: res.Err.Error()}
	}
	d, ok := ParseDevices(res.Stdout + "\n" + res.Stderr)
	if ok && !d.HasGPU() {
		d.Note = backendNote(res.Stdout + "\n" + res.Stderr)
	}
	if !ok {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = "the program printed no device list"
		}
		return Devices{Error: msg}
	}
	return d
}

// Adapters keeps the adapter names that are a GPU: trimmed, each once, without Microsoft's fallback display drivers.
func Adapters(names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || strings.HasPrefix(strings.ToLower(n), "microsoft ") || slices.Contains(out, n) {
			continue
		}
		out = append(out, n)
	}
	return out
}
