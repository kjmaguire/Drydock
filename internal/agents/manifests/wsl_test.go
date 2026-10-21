package manifests_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// The WSL run mode (decision P41): Claude Code declares it, its distro install line is shown only on a WSL Connection, and a
// wsl install line on a CLI without the run mode is a manifest error.
func TestClaudeDeclaresTheWSLRunModeWithItsOwnInstallLine(t *testing.T) {
	m := get(t, "claude")
	if !reflect.DeepEqual(m.Meta.RunModes, []string{"native", "wsl"}) || !reflect.DeepEqual(m.Binaries.WSL, []string{"claude"}) {
		t.Fatalf("%v %v", m.Meta.RunModes, m.Binaries.WSL)
	}
	if h := m.WSLInstallHint(); h != "inside the distro: curl -fsSL https://claude.ai/install.sh | bash" {
		t.Fatalf("WSL hint %q", h)
	}
	if strings.Contains(m.InstallHint(), "install.sh") {
		t.Fatal("the Windows hint must not be the distro's")
	}
	// OpenCode declares WSL as a fact about the CLI, but its ACP adapter cannot start it in a distro: it must not offer the mode
	reg, err := manifests.NewRegistry(manifests.Options{})
	if err != nil {
		t.Fatal(err)
	}
	oc, err := reg.Build("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(oc.Capabilities().RunModes, []string{"native"}) || manifests.WSLLaunchable(get(t, "opencode")) || !manifests.WSLLaunchable(m) {
		t.Fatalf("opencode offers %v", oc.Capabilities().RunModes)
	}
	g := *get(t, "goose")
	g.Install = append(append([]manifests.Install(nil), g.Install...), manifests.Install{Shell: "sh", RunMode: "wsl", Command: "x"})
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "run_mode wsl") {
		t.Fatalf("goose has no wsl run mode: %v", err)
	}
}
