package connections

import (
	"context"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// The name the app gives an install of an agent CLI, in one place for the core (a chat's note when it moves, the sign-in
// terminal's title). ui/app/screens/shared/cli-name.ts is the same rule for the UI (the Connections page, the chats' CLI pickers
// and header); testdata/cli_names.json holds the cases both sides' tests pass, so the two cannot drift apart.

// productNames is the product name of each built-in adapter (cli-name.ts PRODUCT_NAMES, the same table).
var productNames = map[string]string{
	"claude":   "Claude Code",
	"codex":    "Codex",
	"gemini":   "Gemini CLI",
	"cline":    "Cline",
	"opencode": "OpenCode",
	"goose":    "Goose",
}

// ProductName is the CLI's product name ("Claude Code" for claude); a custom manifest's CLI is named by its adapter id.
func ProductName(adapter string) string {
	if n, ok := productNames[adapter]; ok {
		return n
	}
	return adapter
}

// CLIName is the name of adapter's install: its product name on Windows (distro ""), "Claude Code WSL" for its install inside
// a WSL distro, without the distro (the owner: "we don't need to see the Ubuntu"). Only when the CLI is installed in another
// distro too (installedIn: the distros where it is installed) does the name carry the distro, so the two can be told apart:
// "Claude Code WSL (Debian)".
func CLIName(adapter, distro string, installedIn []string) string {
	return InstallName(ProductName(adapter), distro, installedIn)
}

// InstallName is CLIName's rule for a CLI called name (the sign-in terminal's title says the adapter id, "claude WSL", P96).
func InstallName(name, distro string, installedIn []string) string {
	if distro == "" {
		return name
	}
	for _, d := range installedIn {
		if d != distro {
			return name + " WSL (" + distro + ")"
		}
	}
	return name + " WSL"
}

// InstalledWSL lists the user distros where adapter id's CLI is installed, in wsl.exe's order: what CLIName needs to tell two
// installs apart. A probe made in the last few minutes is reused (the Connections page has just made one). None without WSL
// or for a CLI that cannot run there.
func (s *Service) InstalledWSL(ctx context.Context, id string) []string {
	o := s.options()
	m, ok := s.Registry.Set.Get(id)
	if !ok || !hasWSL(m) || o.WSL == nil {
		return nil
	}
	if _, err := o.WSL.Path(); err != nil {
		return nil
	}
	ds, _, err := o.WSL.Distros(ctx)
	if err != nil {
		return nil
	}
	o.probeCached = true
	var out []string
	for _, d := range ds {
		if !wsl.UserDistro(d.Name) {
			continue
		}
		if c := CheckWSL(ctx, m, d, o); c.Installed && c.Status != agents.StatusNotInstalled {
			out = append(out, d.Name)
		}
	}
	return out
}
