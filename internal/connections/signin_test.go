package connections

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/wsl"
)

// Decision P50: the sign-in a card starts is the CLI's own login, by its full path; on Windows the npm .cmd shim runs through
// cmd.exe by cmd.exe's full path (the pseudo-terminal searches no PATH), and a relative override is the process's, not the
// terminal folder's.
func TestNativeSignInIsTheCLIsOwnLoginByItsFullPath(t *testing.T) {
	login := []string{"auth", "login"}
	if p, a, err := nativeSignIn("/usr/local/bin/claude", login, "linux", "", ""); err != nil || p != "/usr/local/bin/claude" || strings.Join(a, " ") != "auth login" {
		t.Fatalf("native: %q %v %v", p, a, err)
	}
	shim := `C:\Users\k\AppData\Roaming\npm\claude.CMD`
	if p, a, err := nativeSignIn(shim, login, "windows", `C:\Windows\system32\cmd.exe`, `C:\Windows`); err != nil || p != `C:\Windows\system32\cmd.exe` || strings.Join(a, " ") != `/c `+shim+` auth login` {
		t.Fatalf("npm shim: %q %v %v", p, a, err)
	}
	if p, _, err := nativeSignIn(shim, login, "windows", "", `C:\Windows`); err != nil || p != `C:\Windows\System32\cmd.exe` {
		t.Fatalf("no ComSpec: %q %v (a bare cmd.exe would be looked for in the terminal's folder)", p, err)
	}
	if p, a, err := nativeSignIn(`C:\tools\codex.exe`, []string{"login"}, "windows", "cmd.exe", ""); err != nil || p != `C:\tools\codex.exe` || strings.Join(a, " ") != "login" {
		t.Fatalf("exe: %q %v %v", p, a, err)
	}
	wd, _ := os.Getwd()
	if p, _, err := nativeSignIn(filepath.Join("bin", "claude"), login, "linux", "", ""); err != nil || p != filepath.Join(wd, "bin", "claude") {
		t.Fatalf("relative: %q %v", p, err)
	}
	if _, _, err := nativeSignIn("drydock-no-such-cli", login, "linux", "", ""); err == nil {
		t.Fatal("a name that is not on PATH")
	}
}

// Inside WSL the login starts as an agent starts there (the probed tool, the login shell's HOME and PATH, the run marker that
// lets Drydock kill what is left), but keeps the terminal: no setsid launcher between wsl.exe and the CLI.
func TestWSLSignInRunsLikeAnAgentButKeepsTheTerminal(t *testing.T) {
	in := wsl.Info{Tool: "/home/k/.npm-global/bin/claude", Home: "/home/k", Path: "/home/k/.nvm/versions/node/v24/bin:/usr/bin"}
	l := wslSignIn("Ubuntu-26.04", in, []string{"auth", "login"})
	if l.Marker == "" || !l.Interactive {
		t.Fatalf("%+v", l)
	}
	want := []string{"-d", "Ubuntu-26.04", "--cd", "/home/k", "--exec", "/usr/bin/env", wsl.RunEnv + "=" + l.Marker, "HOME=/home/k",
		"PATH=/home/k/.nvm/versions/node/v24/bin:/usr/bin", "/home/k/.npm-global/bin/claude", "auth", "login"}
	if a := l.Args(); !slices.Equal(a, want) {
		t.Fatalf("%q", a)
	}
	if a := wslSignIn("Ubuntu", wsl.Info{Tool: "/usr/local/bin/claude"}, []string{"auth", "login"}).Args(); a[3] != "~" {
		t.Fatalf("no probed HOME: %q", a)
	}
}

// The command comes from each manifest's own auth.login, the one place a CLI's sign-in is written down: every CLI that names
// one gets a button, with its exact words; one that only says how (a note) gets none.
func TestSignInComesFromTheManifest(t *testing.T) {
	svc, err := NewService(Config{})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"claude":      "claude auth login",
		"codex":       "codex login",
		"opencode":    "opencode providers login",
		"goose":       "goose configure",
		"cline":       "cline auth",
		"antigravity": "",
	} {
		m, ok := svc.Registry.Set.Get(id)
		if !ok {
			t.Fatalf("no manifest %s", id)
		}
		if got := signInText(m, m.Binaries.Native); got != want {
			t.Fatalf("%s: %q, want %q", id, got, want)
		}
	}
	m, _ := svc.Registry.Set.Get("claude")
	if got := signInText(m, m.Binaries.WSL); got != "claude auth login" {
		t.Fatalf("wsl: %q", got)
	}
}
