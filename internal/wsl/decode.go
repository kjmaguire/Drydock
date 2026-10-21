// Package wsl reaches the Windows Subsystem for Linux from the Windows side (decision P41, docs/connections.md section 4b).
// It lists the distros (`wsl.exe -l -q`, whose output is UTF-16LE), probes one distro through the person's own login shell,
// maps paths between the two sides, and builds the wsl.exe command lines Drydock starts an agent with and stops it with.
//
// Nothing here reads, copies or transmits a credential (decision D1): a CLI's sign-in inside a distro is the CLI's own,
// reported only by the CLI's own status command.
package wsl

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// DecodeOutput turns what wsl.exe printed into text. wsl.exe writes its own messages and lists in UTF-16LE (with or without a
// byte order mark) unless WSL_UTF8=1 is set, while a Linux program it runs writes whatever that program writes (UTF-8): this
// accepts both. Carriage returns are dropped.
func DecodeOutput(b []byte) string {
	if utf16LE(b) {
		if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
			b = b[2:]
		}
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return clean(string(utf16.Decode(u)))
	}
	return clean(string(b))
}

// utf16LE guesses the encoding: a UTF-16LE byte order mark, or text whose odd bytes are mostly NUL (ASCII in UTF-16LE).
func utf16LE(b []byte) bool {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return true
	}
	if len(b) < 2 {
		return false
	}
	zeros, pairs := 0, 0
	for i := 1; i < len(b); i += 2 {
		pairs++
		if b[i] == 0 {
			zeros++
		}
	}
	return zeros*2 > pairs
}

func clean(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\x00", "")
	return strings.TrimPrefix(s, "\ufeff")
}

// ParseList reads `wsl.exe -l -q`: one distro name per line.
func ParseList(out string) []string {
	var names []string
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		n := strings.TrimSpace(strings.TrimPrefix(l, "\ufeff"))
		if n == "" || strings.ContainsAny(n, " \t") || seen[strings.ToLower(n)] {
			continue // a name has no spaces: anything else is a message wsl.exe printed
		}
		seen[strings.ToLower(n)] = true
		names = append(names, n)
	}
	return names
}

// Distro is one installed distribution.
type Distro struct {
	Name    string `json:"name"`
	Default bool   `json:"default,omitempty"`
	State   string `json:"state,omitempty"`   // Running, Stopped, Installing ... as wsl.exe -l -v prints it ("" unknown)
	Version int    `json:"version,omitempty"` // 1 or 2 (0 unknown)
}

// ParseVerbose reads `wsl.exe -l -v`: a header, then "[*] NAME STATE VERSION" per distro (the header is localized, so it is
// recognized by its last column not being a number).
func ParseVerbose(out string) []Distro {
	var ds []Distro
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		def := strings.HasPrefix(l, "*")
		f := strings.Fields(strings.TrimPrefix(l, "*"))
		if len(f) < 3 {
			continue
		}
		v, err := strconv.Atoi(f[len(f)-1])
		if err != nil {
			continue
		}
		ds = append(ds, Distro{Name: strings.Join(f[:len(f)-2], " "), State: f[len(f)-2], Version: v, Default: def})
	}
	return ds
}

// systemDistros are distributions other software installs for itself; they have no user shell and no agent CLI, and starting
// one to probe it would start that product's VM.
var systemDistros = []string{"docker-desktop", "rancher-desktop", "podman-machine"}

// UserDistro reports whether a distro is one a person works in (not a container engine's own).
func UserDistro(name string) bool {
	n := strings.ToLower(name)
	for _, p := range systemDistros {
		if strings.HasPrefix(n, p) {
			return false
		}
	}
	return n != ""
}
