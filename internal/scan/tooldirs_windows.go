//go:build windows

package scan

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// extraToolDirs are the PATH folders as they are now in the registry (the user's, then the machine's: a program only sees the
// PATH it was started with, so a tool installed while Drydock runs would otherwise need a restart) and the folders installers
// put programs in: WinGet's links, Scoop's shims, Chocolatey's bin and `go install`'s bin.
func extraToolDirs() []string {
	var out []string
	out = append(out, registryPath(registry.CURRENT_USER, `Environment`)...)
	out = append(out, registryPath(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)...)
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		out = append(out, filepath.Join(local, "Microsoft", "WinGet", "Links"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, "scoop", "shims"), filepath.Join(home, "go", "bin"))
	}
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		out = append(out, gobin)
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		out = append(out, filepath.Join(pd, "chocolatey", "bin"))
	}
	return out
}

// registryPath reads a Path value (REG_EXPAND_SZ or REG_SZ) and expands it into folders.
func registryPath(root registry.Key, sub string) []string {
	k, err := registry.OpenKey(root, sub, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if err != nil {
		return nil
	}
	if x, err := registry.ExpandString(v); err == nil {
		v = x
	}
	var out []string
	for _, d := range strings.Split(v, ";") {
		if d = strings.TrimSpace(strings.Trim(d, `"`)); d != "" {
			out = append(out, d)
		}
	}
	return out
}
