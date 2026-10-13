package connections

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
)

// Decision P50: the sign-in a card starts is the CLI's own login, in a terminal the person answers; inside WSL it runs in the
// distro through wsl.exe, and on Windows the npm .cmd shim runs through cmd.exe.
func TestSignInCommandIsTheCLIsOwnLoginWhereTheCLIIs(t *testing.T) {
	native := Connection{Detection: agents.Detection{Adapter: "claude", Installed: true, Path: "/usr/local/bin/claude"}, RunMode: "native"}
	if p, a, err := signInCommand(native, "", "linux", ""); err != nil || p != "/usr/local/bin/claude" || strings.Join(a, " ") != "auth login" {
		t.Fatalf("native: %q %v %v", p, a, err)
	}
	cmd := Connection{Detection: agents.Detection{Adapter: "claude", Installed: true, Path: `C:\Users\k\AppData\Roaming\npm\claude.CMD`}, RunMode: "native"}
	if p, a, err := signInCommand(cmd, "", "windows", `C:\Windows\system32\cmd.exe`); err != nil || p != `C:\Windows\system32\cmd.exe` || strings.Join(a, " ") != `/c C:\Users\k\AppData\Roaming\npm\claude.CMD auth login` {
		t.Fatalf("npm shim: %q %v %v", p, a, err)
	}
	if p, _, err := signInCommand(cmd, "", "windows", ""); err != nil || p != "cmd.exe" {
		t.Fatalf("no COMSPEC: %q %v", p, err)
	}
	exe := Connection{Detection: agents.Detection{Adapter: "codex", Installed: true, Path: `C:\tools\codex.exe`}, RunMode: "native"}
	if p, a, err := signInCommand(exe, "", "windows", "cmd.exe"); err != nil || p != `C:\tools\codex.exe` || strings.Join(a, " ") != "login" {
		t.Fatalf("exe: %q %v %v", p, a, err)
	}
	wsl := Connection{Detection: agents.Detection{Adapter: "claude", Installed: true, Path: "/home/k/.local/bin/claude"}, RunMode: "wsl", Distro: "Ubuntu-26.04"}
	p, a, err := signInCommand(wsl, `C:\Windows\System32\wsl.exe`, "windows", "cmd.exe")
	if err != nil || p != `C:\Windows\System32\wsl.exe` || strings.Join(a, " ") != "-d Ubuntu-26.04 -- /home/k/.local/bin/claude auth login" {
		t.Fatalf("wsl: %q %v %v", p, a, err)
	}
	if _, _, err := signInCommand(wsl, "", "windows", ""); err == nil || !strings.Contains(err.Error(), "wsl.exe") {
		t.Fatalf("no wsl.exe: %v", err)
	}
}

func TestSignInCommandRefusesWhatItCannotStart(t *testing.T) {
	missing := Connection{Detection: agents.Detection{Adapter: "claude"}, RunMode: "wsl", Distro: "Ubuntu-26.04"}
	if _, _, err := signInCommand(missing, "wsl.exe", "windows", ""); err == nil || !strings.Contains(err.Error(), "not installed inside the WSL distro Ubuntu-26.04") {
		t.Fatalf("not installed: %v", err)
	}
	unknown := Connection{Detection: agents.Detection{Adapter: "gemini", Installed: true, Path: "/usr/bin/gemini"}, RunMode: "native"}
	if _, _, err := signInCommand(unknown, "", "linux", ""); err == nil || !strings.Contains(err.Error(), "no sign-in command") {
		t.Fatalf("unknown CLI: %v", err)
	}
}
