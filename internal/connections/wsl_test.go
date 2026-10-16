package connections_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/wsl"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

func needStandInDistro(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in distro runs Linux scripts on a Linux development host; Windows CI drives the real wsl.exe")
	}
	for _, tool := range []string{"/usr/bin/setsid", "/usr/bin/bash", "/usr/bin/getent"} {
		if _, err := os.Stat(tool); err != nil {
			t.Skipf("%s is missing on this host", tool)
		}
	}
}

func wslService(t *testing.T, f *wsltest.Fake) *connections.Service {
	t.Helper()
	svc, err := connections.NewService(connections.Config{Detection: connections.Options{WSL: &wsl.Host{Exe: f.Exe}, Home: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestEachUserDistroIsAConnectionOfItsOwnWithItsOwnSignIn(t *testing.T) {
	needStandInDistro(t)
	f := wsltest.NewFake(t, true, "Ubuntu", "Debian", "docker-desktop")
	svc := wslService(t, f)
	cs := svc.DetectWSL(context.Background())
	if len(cs) != 2 || cs[0].Key != "claude@wsl:Debian" || cs[1].Key != "claude@wsl:Ubuntu" {
		t.Fatalf("%d connections: %+v", len(cs), cs)
	}
	c := cs[1]
	if c.Adapter != "claude" || c.RunMode != agents.RunModeWSL || c.Distro != "Ubuntu" || !c.Installed || c.Version != "2.1.296" ||
		c.Path != filepath.Join(f.Home, ".local", "bin", "claude") || c.Authenticated == nil || !*c.Authenticated || c.AuthDetail != "claude.ai" ||
		c.Account != "standin@example.invalid" {
		t.Fatalf("%+v", c)
	}
	if c.WSL == nil || c.WSL.Home != f.Home || c.WSL.MountRoot != "/mnt/" || !c.WSL.Setsid || c.WSL.WSLVer != 2 || len(c.WSL.Notes) == 0 {
		t.Fatalf("facts: %+v", c.WSL)
	}
	// the development host has no interop: honestly degraded, with what to do about it, and not usable
	if c.Status != agents.StatusDegraded || c.Reason != connections.ReasonWSL || c.Usable || !strings.Contains(strings.Join(c.Problems, " "), "[interop] enabled=true") {
		t.Fatalf("status %s reason %s usable %v problems %v", c.Status, c.Reason, c.Usable, c.Problems)
	}
	// a key can be checked on its own, and the native Connection is unchanged by the distro's
	if one, err := svc.CheckKey(context.Background(), "claude@wsl:Ubuntu"); err != nil || one.Key != c.Key {
		t.Fatalf("%+v %v", one, err)
	}
	if _, err := svc.CheckKey(context.Background(), "claude@wsl:NoSuch"); err == nil {
		t.Fatal("an unknown distro is an error")
	}
	if _, err := svc.CheckKey(context.Background(), "codex@wsl:Ubuntu"); err == nil {
		t.Fatal("codex has no WSL run mode")
	}
}

func TestASignedOutDistroIsUnauthenticatedAndSaysToSignInInsideIt(t *testing.T) {
	needStandInDistro(t)
	f := wsltest.NewFake(t, true, "Ubuntu")
	if err := os.WriteFile(filepath.Join(f.Home, ".drydock-standin-signed-out"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := wslService(t, f).CheckKey(context.Background(), "claude@wsl:Ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != agents.StatusUnauthenticated || c.Authenticated == nil || *c.Authenticated || !strings.Contains(strings.Join(c.Problems, " "), "wsl -d Ubuntu") {
		t.Fatalf("%+v", c)
	}
	// it says what the CLI answered and as whom it was asked, so a sign-in made as another distro user is recognisable
	p := strings.Join(c.Problems, " ")
	if !strings.Contains(p, `"loggedIn": false`) || !strings.Contains(p, "auth status --json` answered") || !strings.Contains(p, "HOME "+f.Home) {
		t.Fatalf("the problem does not say what the check saw: %s", p)
	}
}

func TestAFailingSignInCheckInADistroSaysWhyItFailed(t *testing.T) {
	needStandInDistro(t)
	f := wsltest.NewFake(t, true, "Ubuntu")
	if err := os.WriteFile(filepath.Join(f.Home, ".drydock-standin-auth-broken"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := wslService(t, f).CheckKey(context.Background(), "claude@wsl:Ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	p := strings.Join(c.Problems, " ")
	if c.Status != agents.StatusUnauthenticated || !strings.Contains(p, "exited 2 without its JSON answer: Error: EACCES: permission denied") || !strings.Contains(p, "checked as") {
		t.Fatalf("%+v", c)
	}
}

func TestADistroWithoutClaudeShowsTheDistrosInstallLine(t *testing.T) {
	needStandInDistro(t)
	f := wsltest.NewFake(t, false, "Ubuntu")
	c, err := wslService(t, f).CheckKey(context.Background(), "claude@wsl:Ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if c.Installed && !strings.HasPrefix(c.Path, f.Home) {
		t.Skipf("this host has its own claude on a login shell's PATH (%s), which the stand-in distro shares", c.Path)
	}
	if c.Status != agents.StatusNotInstalled || c.Installed || c.InstallHint != "inside the distro: curl -fsSL https://claude.ai/install.sh | bash" {
		t.Fatalf("%+v", c)
	}
}

func TestNoWSLMeansNoWSLConnections(t *testing.T) {
	svc, err := connections.NewService(connections.Config{Detection: connections.Options{WSL: &wsl.Host{Exe: filepath.Join(t.TempDir(), "no-wsl")}}})
	if err != nil {
		t.Fatal(err)
	}
	if cs := svc.DetectWSL(context.Background()); len(cs) != 0 {
		t.Fatalf("%+v", cs)
	}
}

func TestTestConnectionRunsTheDistrosInstallAndKeepsItsOwnResult(t *testing.T) {
	needStandInDistro(t)
	f := wsltest.NewFake(t, true, "Ubuntu")
	svc := wslService(t, f)
	rep, err := svc.TestConnection(context.Background(), "claude", connections.TestOptions{Distro: "Ubuntu", Prompt: true, Model: "haiku"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Key != "claude@wsl:Ubuntu" || rep.RunMode != agents.RunModeWSL || rep.Distro != "Ubuntu" || !rep.OK {
		t.Fatalf("%+v", rep)
	}
	for _, s := range rep.Steps {
		switch s.ID {
		case "T1", "T2", "T3", "T5":
			if s.Status != connections.StepPass {
				t.Errorf("%s %s: %s", s.ID, s.Status, s.Reason)
			}
		}
		if s.ID == "T3" && !strings.Contains(s.Detail, "standin turn=1") {
			t.Errorf("T3 answered %q", s.Detail)
		}
		if s.ID == "T5" && !strings.Contains(s.Detail, "inside the distro") {
			t.Errorf("T5: %s", s.Detail)
		}
	}
	// the result is the distro's: it does not touch the native Connection's
	svc.RecordTest(rep.Key, connections.Report{OK: false, Steps: []connections.Step{{ID: "T2", Status: connections.StepFail, Reason: "boom"}}})
	c, _ := svc.CheckKey(context.Background(), "claude@wsl:Ubuntu")
	if !strings.Contains(c.Reason, connections.ReasonTestFailed) && c.Reason != connections.ReasonWSL {
		t.Fatalf("reason %s", c.Reason)
	}
	if n, _ := svc.Check(context.Background(), "claude"); strings.HasPrefix(n.Reason, connections.ReasonTestFailed) {
		t.Fatal("the distro's failed Test degraded the native Connection")
	}
}

// Decision P50 through the stand-in WSL: the sign-in of a distro's install (named in any case, as Windows names distros) runs
// the CLI found by the probe with the login shell's PATH and the run marker, starts wsl.exe with nothing of Windows copied in,
// and is the CLI's own login: answering it signs the install in.
func TestTheSignInInsideADistroIsTheCLIsOwnLoginWithTheLoginShellsPath(t *testing.T) {
	needStandInDistro(t)
	f := wsltest.NewFake(t, true, "Ubuntu")
	if err := os.WriteFile(filepath.Join(f.Home, ".drydock-standin-signed-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := wslService(t, f)
	t.Setenv("WSLENV", "ANTHROPIC_API_KEY/u")
	t.Setenv("ANTHROPIC_API_KEY", "sk-not-for-the-distro")
	c, si, err := svc.SignIn(context.Background(), "claude@wsl:ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if c.Key != "claude@wsl:Ubuntu" || c.SignIn != "claude auth login" || si.Program != f.Exe || si.Distro != "Ubuntu" || si.Marker == "" {
		t.Fatalf("%+v %+v", c, si)
	}
	for _, kv := range si.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "WSLENV=") && kv != "WSLENV=" {
			t.Fatalf("wsl.exe would copy %s into the distro", kv)
		}
	}
	cmd := exec.Command(si.Program, si.Args...)
	cmd.Dir, cmd.Env, cmd.Stdin = si.Dir, si.Env, strings.NewReader("\n")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "marker=yes login-path=yes") || !strings.Contains(string(out), "Signed in to the stand-in.") {
		t.Fatalf("%v: %s", err, out)
	}
	if one, err := svc.CheckKey(context.Background(), "claude@wsl:Ubuntu"); err != nil || one.Authenticated == nil || !*one.Authenticated {
		t.Fatalf("not signed in afterwards: %+v %v", one, err)
	}

	// what cannot be signed in to is refused with the reason
	for key, want := range map[string]string{"nope": "no Connection named nope", "antigravity": "has no sign-in command", "claude@wsl:Nope": "no WSL distro named Nope"} {
		if _, _, err := svc.SignIn(context.Background(), key); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", key, err)
		}
	}
}

// ReadyWSL names the distros where an agent can start Claude Code (decision P58): signed in there, the WSL default distro
// first, never a container engine's own distro; a signed-out install does not count, nor a CLI without the WSL run mode.
func TestReadyWSLListsTheDistrosWhereAnAgentCanStartTheDefaultFirst(t *testing.T) {
	needStandInDistro(t)
	f := wsltest.NewFake(t, true, "Ubuntu-26.04", "Debian", "docker-desktop")
	svc := wslService(t, f)
	ctx := context.Background()
	ready := svc.ReadyWSL(ctx, "claude", true)
	if len(ready) != 2 || ready[0].Distro != "Ubuntu-26.04" || ready[0].WSL == nil || !ready[0].WSL.Default || ready[1].Distro != "Debian" {
		t.Fatalf("%+v", ready)
	}
	if got := svc.ReadyWSL(ctx, "codex", true); len(got) != 0 {
		t.Fatalf("codex cannot start in WSL: %+v", got)
	}
	// the cached probes answer without probing again
	n := len(f.Calls())
	if again := svc.ReadyWSL(ctx, "claude", false); len(again) != 2 {
		t.Fatalf("%+v", again)
	}
	for _, c := range f.Calls()[n:] { // the distro list only (wsl.exe -l)
		if len(c) == 0 || c[0] != "-l" {
			t.Fatalf("a cached ReadyWSL probed again: %q", f.Calls()[n:])
		}
	}
	if err := os.WriteFile(filepath.Join(f.Home, ".drydock-standin-signed-out"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := svc.ReadyWSL(ctx, "claude", true); len(got) != 0 {
		t.Fatalf("a signed-out install counted: %+v", got)
	}
}
