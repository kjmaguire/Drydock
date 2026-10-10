// Package pins lists the agent CLIs and ACP bridges that Drydock installs from npm, at the versions its manifests pin, and
// asks the npm registry for the newest published version of each. cmd/drydock-audit (the Software Versions page) and
// cmd/drydock-contract (the contract suite's version watch and --latest runs) both read the pins from here, so the two never
// disagree about what Drydock pins.
package pins

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// Kinds of pin.
const (
	// KindCLI is the agent CLI itself, at the version its manifest recommends (versions.recommended).
	KindCLI = "cli"
	// KindBridge is an ACP bridge Drydock starts with `npx -y <package>@<version>`, at that exact version.
	KindBridge = "bridge"
)

// Pin is one npm-distributed program Drydock depends on.
type Pin struct {
	Adapter string `json:"adapter"`       // manifest id (claude, codex, ...)
	Kind    string `json:"kind"`          // KindCLI or KindBridge
	Package string `json:"package"`       // npm package name, without a version
	Version string `json:"version"`       // the recommended (CLI) or exact (bridge) version
	Min     string `json:"min,omitempty"` // the minimum supported version (CLI only)
	Bin     string `json:"bin"`           // the command the package installs, as looked up on PATH
	File    string `json:"file"`          // the manifest the pin comes from, relative to the repository root
}

// ID is the identifier the Software Versions page uses: cli:<adapter> or bridge:<package>.
func (p Pin) ID() string {
	if p.Kind == KindBridge {
		return "bridge:" + p.Package
	}
	return "cli:" + p.Adapter
}

// Embedded returns the pins of the manifests shipped with Drydock.
func Embedded() ([]Pin, error) {
	set, err := manifests.Embedded()
	if err != nil {
		return nil, err
	}
	return FromSet(set), nil
}

// FromSet extracts the pins from a manifest set in manifest id order, each CLI before its bridges. A CLI is pinned when its
// manifest recommends a version and offers an `npm install -g` install line; a bridge when its ACP transport is an npx command
// with an exact version.
func FromSet(s *manifests.Set) []Pin {
	var out []Pin
	for _, m := range s.All() {
		file := "internal/agents/manifests/cli/" + m.Meta.ID + ".yaml"
		if m.Versions.Recommended != "" {
			for _, in := range m.Install {
				if pkg, ok := NPMPackage(in.Command); ok {
					out = append(out, Pin{Adapter: m.Meta.ID, Kind: KindCLI, Package: pkg, Version: m.Versions.Recommended, Min: m.Versions.Min,
						Bin: binName(m.Binaries.Native), File: file})
					break
				}
			}
		}
		if t := m.Transports.ACP; t != nil {
			if pkg, ver, ok := npxPackage(t.Argv); ok {
				out = append(out, Pin{Adapter: m.Meta.ID, Kind: KindBridge, Package: pkg, Version: ver, Bin: path.Base(pkg), File: file})
			}
		}
	}
	return out
}

// NPMPackage returns the package an `npm install -g <pkg>` (or `npm i -g`) command line installs, without a version or tag:
// "npm i -g opencode-ai@latest" gives "opencode-ai".
func NPMPackage(command string) (string, bool) {
	f := strings.Fields(command)
	if len(f) < 3 || f[0] != "npm" || (f[1] != "install" && f[1] != "i") {
		return "", false
	}
	global := false
	for _, a := range f[2:] {
		switch {
		case a == "-g" || a == "--global":
			global = true
		case strings.HasPrefix(a, "-"):
		default:
			if !global {
				return "", false
			}
			name, _ := splitSpec(a)
			return name, name != ""
		}
	}
	return "", false
}

var exactVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// npxPackage reads `npx [-y] <package>@<exact version>` from an ACP transport argv.
func npxPackage(argv []string) (pkg, version string, ok bool) {
	if len(argv) < 2 || argv[0] != "npx" {
		return "", "", false
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		name, ver := splitSpec(a)
		if name != "" && exactVersion.MatchString(ver) {
			return name, ver, true
		}
		return "", "", false
	}
	return "", "", false
}

// splitSpec splits "name@version" and "@scope/name@version" (the version may be empty).
func splitSpec(spec string) (name, version string) {
	i := strings.LastIndex(spec, "@")
	if i <= 0 {
		return spec, ""
	}
	return spec[:i], spec[i+1:]
}

// binName is the command a CLI installs: the first native binary name without a Windows extension (claude.exe gives claude).
func binName(native []string) string {
	if len(native) == 0 {
		return ""
	}
	n := native[0]
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		n = strings.TrimSuffix(n, ext)
	}
	return n
}

// Latest asks the npm registry for the newest published version of pkg (`npm view <pkg> version`).
func Latest(ctx context.Context, pkg string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "npm", "view", pkg, "version")
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("npm view %s version: %v: %s", pkg, err, strings.TrimSpace(errb.String()))
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	// `npm view pkg@range version` prints "pkg@x.y.z 'x.y.z'" lines; a plain package prints the bare version
	if f := strings.Fields(last); len(f) > 0 {
		last = strings.Trim(f[len(f)-1], "'")
	}
	if last == "" {
		return "", fmt.Errorf("npm view %s version printed nothing", pkg)
	}
	return last, nil
}
