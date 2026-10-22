package connections_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

// cliNames is testdata/cli_names.json: the cases ui/app/screens/shared/cli-name.test.ts passes too, so the core and the UI
// name an install the same way.
type cliNames struct {
	Products map[string]string `json:"products"`
	Cases    []struct {
		Adapter     string   `json:"adapter"`
		Distro      string   `json:"distro"`
		InstalledIn []string `json:"installedIn"`
		Want        string   `json:"want"`
	} `json:"cases"`
}

func readCLINames(t *testing.T) cliNames {
	t.Helper()
	b, err := os.ReadFile("testdata/cli_names.json")
	if err != nil {
		t.Fatal(err)
	}
	var n cliNames
	if err := json.Unmarshal(b, &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Products) == 0 || len(n.Cases) == 0 {
		t.Fatal("testdata/cli_names.json has no cases")
	}
	return n
}

// The owner: "rename it everywhere just to say Claude CLI WSL, we don't need to see the Ubuntu". An install inside a distro is
// "<product> WSL"; only when the same CLI is installed in another distro too does the name carry the distro, as on the
// Connections page.
func TestCLINameLeavesTheDistroOut(t *testing.T) {
	n := readCLINames(t)
	for id, want := range n.Products {
		if got := connections.ProductName(id); got != want {
			t.Errorf("product name of %s: %q, want %q", id, got, want)
		}
	}
	for _, c := range n.Cases {
		if got := connections.CLIName(c.Adapter, c.Distro, c.InstalledIn); got != c.Want {
			t.Errorf("%s in %q, installed in %q: %q, want %q", c.Adapter, c.Distro, c.InstalledIn, got, c.Want)
		}
	}
	// the same rule for a CLI named by its adapter id (the sign-in terminal's title, P96)
	for _, c := range []struct {
		distro      string
		installedIn []string
		want        string
	}{
		{"", []string{"Ubuntu", "Debian"}, "claude"},
		{"Ubuntu", []string{"Ubuntu"}, "claude WSL"},
		{"Debian", []string{"Ubuntu", "Debian"}, "claude WSL (Debian)"},
	} {
		if got := connections.InstallName("claude", c.distro, c.installedIn); got != c.want {
			t.Errorf("claude in %q, installed in %q: %q, want %q", c.distro, c.installedIn, got, c.want)
		}
	}
}

// InstalledWSL lists the user distros where the CLI was found (a container engine's own distro is not one), from the probes
// the Connections page made, and none for a CLI without the wsl run mode.
func TestInstalledWSLListsTheDistrosThatHaveTheCLI(t *testing.T) {
	needStandInDistro(t)
	f := wsltest.NewFake(t, true, "Ubuntu-26.04", "Debian", "docker-desktop")
	svc := wslService(t, f)
	ctx := context.Background()
	svc.DetectWSL(ctx)
	n := len(f.Calls())
	if got := svc.InstalledWSL(ctx, "claude"); !slices.Equal(got, []string{"Ubuntu-26.04", "Debian"}) {
		t.Fatalf("%q", got)
	}
	for _, c := range f.Calls()[n:] { // the distro list only (wsl.exe -l): the Connections page's probes are reused
		if len(c) == 0 || c[0] != "-l" {
			t.Fatalf("InstalledWSL probed again: %q", f.Calls()[n:])
		}
	}
	if got := connections.CLIName("claude", "Debian", svc.InstalledWSL(ctx, "claude")); got != "Claude Code WSL (Debian)" {
		t.Fatal(got)
	}
	if got := svc.InstalledWSL(ctx, "codex"); len(got) != 0 {
		t.Fatalf("codex cannot run in WSL: %q", got)
	}

	without := wsltest.NewFake(t, false, "Ubuntu")
	if c, _ := wslService(t, without).CheckKey(ctx, "claude@wsl:Ubuntu"); c.Installed {
		t.Skipf("this host has its own claude on a login shell's PATH (%s), which the stand-in distro shares", c.Path)
	}
	if got := wslService(t, without).InstalledWSL(ctx, "claude"); len(got) != 0 {
		t.Fatalf("a distro without the CLI counted: %q", got)
	}
}
