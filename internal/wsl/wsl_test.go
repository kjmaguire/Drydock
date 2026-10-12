package wsl_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/wsl"
	"github.com/kjmaguire/drydock/internal/wsl/wsltest"
)

func TestMain(m *testing.M) {
	if wsltest.IsStandIn() { // this test binary is the LABELLED stand-in wsl.exe (internal/wsl/wsltest)
		os.Exit(wsltest.StandIn(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func utf16le(s string, bom bool) []byte {
	var out []byte
	if bom {
		out = []byte{0xFF, 0xFE}
	}
	for _, c := range utf16.Encode([]rune(s)) {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func TestDecodeOutputReadsUTF16LEWithAndWithoutAByteOrderMarkAndUTF8(t *testing.T) {
	for name, b := range map[string][]byte{
		"utf16 with bom":    utf16le("Ubuntu-24.04\r\nDebian\r\ndocker-desktop\r\n", true),
		"utf16 without bom": utf16le("Ubuntu-24.04\r\nDebian\r\ndocker-desktop\r\n", false),
		"utf8 (WSL_UTF8=1)": []byte("Ubuntu-24.04\r\nDebian\r\ndocker-desktop\r\n"),
	} {
		got := wsl.ParseList(wsl.DecodeOutput(b))
		if !slices.Equal(got, []string{"Ubuntu-24.04", "Debian", "docker-desktop"}) {
			t.Errorf("%s: %q", name, got)
		}
	}
	// a name with non-ASCII letters survives the UTF-16 decoding
	if got := wsl.ParseList(wsl.DecodeOutput(utf16le("Débian\r\n", true))); !slices.Equal(got, []string{"Débian"}) {
		t.Errorf("non-ASCII name: %q", got)
	}
	// what wsl.exe prints when nothing is installed is a message, not a distro
	msg := "Windows Subsystem for Linux has no installed distributions.\r\nYou can resolve this by installing a distribution.\r\n"
	if got := wsl.ParseList(wsl.DecodeOutput(utf16le(msg, true))); len(got) != 0 {
		t.Errorf("a message was read as distros: %q", got)
	}
	if wsl.UserDistro("docker-desktop-data") || wsl.UserDistro("rancher-desktop") || !wsl.UserDistro("Ubuntu") {
		t.Error("the container engines' own distros must not be probed")
	}
}

func TestParseVerboseReadsDefaultStateAndVersion(t *testing.T) {
	out := wsl.DecodeOutput(utf16le("  NAME            STATE           VERSION\r\n* Ubuntu-24.04    Running         2\r\n  Legacy          Stopped         1\r\n", true))
	ds := wsl.ParseVerbose(out)
	if len(ds) != 2 || ds[0] != (wsl.Distro{Name: "Ubuntu-24.04", Default: true, State: "Running", Version: 2}) || ds[1] != (wsl.Distro{Name: "Legacy", State: "Stopped", Version: 1}) {
		t.Fatalf("%+v", ds)
	}
}

func TestPathsMapDrivesUnderTheDistrosOwnMountRootAndDistroFilesBothWays(t *testing.T) {
	p := wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}
	cases := []struct{ win, linux string }{
		{`D:\src\app`, "/mnt/d/src/app"}, // a Dev Drive is an ordinary drive letter
		{`C:\Users\k\AppData\Local\Temp\drydock-claude-prompt-1.md`, "/mnt/c/Users/k/AppData/Local/Temp/drydock-claude-prompt-1.md"},
		{`E:\`, "/mnt/e"},
		{`\\wsl.localhost\Ubuntu\home\k\app`, "/home/k/app"},
		{`\\wsl$\ubuntu\home\k\app`, "/home/k/app"}, // the older alias, any case of the distro name
		{`//wsl.localhost/Ubuntu/home/k/app`, "/home/k/app"},
		{`\\?\UNC\wsl.localhost\Ubuntu\home\k`, "/home/k"},
		{"/home/k/already", "/home/k/already"},
	}
	for _, c := range cases {
		got, err := p.ToLinux(c.win)
		if err != nil || got != c.linux {
			t.Errorf("ToLinux(%q) = %q, %v; want %q", c.win, got, err, c.linux)
		}
	}
	// a changed automount root ([automount] root = /) is used, never /mnt
	if got, err := (wsl.Paths{Distro: "Ubuntu", MountRoot: "/"}).ToLinux(`D:\x`); err != nil || got != "/d/x" {
		t.Errorf("root /: %q %v", got, err)
	}
	for _, bad := range []string{`\\wsl.localhost\Debian\home\k`, `\\server\share\x`, `relative\x`, `D:x`, ""} {
		if got, err := p.ToLinux(bad); err == nil {
			t.Errorf("ToLinux(%q) = %q, want a refusal", bad, got)
		}
	}
	if _, err := (wsl.Paths{Distro: "Ubuntu"}).ToLinux(`C:\x`); err == nil {
		t.Error("an unknown mount root must refuse a drive path, not guess /mnt")
	}
	back := []struct{ linux, win string }{
		{"/mnt/d/src/app", `D:\src\app`},
		{"/mnt/c", `C:\`},
		{"/mnt/data/x", `\\wsl.localhost\Ubuntu\mnt\data\x`}, // not a drive letter: the distro's own folder
		{"/home/k/app/../app/x", `\\wsl.localhost\Ubuntu\home\k\app\x`},
		{"/", `\\wsl.localhost\Ubuntu\`},
	}
	for _, c := range back {
		got, ok := p.ToWindows(c.linux)
		if !ok || got != c.win {
			t.Errorf("ToWindows(%q) = %q; want %q", c.linux, got, c.win)
		}
	}
	if !p.OnDrive("/mnt/d/src") || p.OnDrive("/home/k") {
		t.Error("OnDrive")
	}
	if d, lin, ok := wsl.ParseUNC(`\\wsl.localhost\Ubuntu-24.04\home\k\..\k\app`); !ok || d != "Ubuntu-24.04" || lin != "/home/k/app" {
		t.Errorf("ParseUNC: %q %q %v", d, lin, ok)
	}
}

// ".." in a Windows path stops at the drive root, as Windows resolves it; it never climbs into the distro's mount root
// (D:\..\..\mnt\d\wt\x is D:\mnt\d\wt\x on Windows, so it must not become /mnt/d/wt/x). A distro path's ".." stops at the
// distro's "/".
func TestToLinuxKeepsDotDotInsideTheDriveRoot(t *testing.T) {
	p := wsl.Paths{Distro: "Ubuntu", MountRoot: "/mnt/"}
	cases := []struct{ win, linux string }{
		{`D:\..\..\mnt\d\wt\x`, "/mnt/d/mnt/d/wt/x"}, // the reproduction: not /mnt/d/wt/x
		{`D:\..`, "/mnt/d"},
		{`D:\..\..\..`, "/mnt/d"},
		{`D:\a\..\..\b`, "/mnt/d/b"},
		{`D:/../c/Users/k/.ssh`, "/mnt/d/c/Users/k/.ssh"},
		{`D:\wt/..\../mnt/c\Windows`, "/mnt/d/mnt/c/Windows"}, // mixed slashes
		{`d:\src\.\app\..\lib`, "/mnt/d/src/lib"},
		{`\\wsl.localhost\Ubuntu\..\..`, "/"},
		{`\\wsl.localhost\Ubuntu\home\..\..\..\etc`, "/etc"},
		{`//wsl$/Ubuntu/../../mnt/c`, "/mnt/c"}, // the distro's own /mnt/c, reached from its "/"
		{"/mnt/d/../../etc", "/etc"},
	}
	for _, c := range cases {
		got, err := p.ToLinux(c.win)
		if err != nil || got != c.linux {
			t.Errorf("ToLinux(%q) = %q, %v; want %q", c.win, got, err, c.linux)
		}
	}
	// with the mount root at "/" the drive is the top folder, and ".." still stops there
	if got, err := (wsl.Paths{Distro: "Ubuntu", MountRoot: "/"}).ToLinux(`D:\..\c\x`); err != nil || got != "/d/c/x" {
		t.Errorf("root /: %q %v", got, err)
	}
	// ".." or "." is never taken as the distro's name: such a path is refused, not climbed out of
	for _, bad := range []string{`\\wsl.localhost\..\Ubuntu\etc`, `\\wsl.localhost\.\Ubuntu`, `\\wsl$\Debian\..\Ubuntu\x`} {
		if got, err := p.ToLinux(bad); err == nil {
			t.Errorf("ToLinux(%q) = %q, want a refusal", bad, got)
		}
	}
}

func TestMountRootComesFromWslpathOrWslConf(t *testing.T) {
	if r := wsl.MountRootFrom("/mnt/c/", nil); r != "/mnt/" {
		t.Error(r)
	}
	if r := wsl.MountRootFrom("/c/", nil); r != "/" {
		t.Error(r)
	}
	conf := []string{"[boot]", "systemd=true", "[automount]", `root = "/win/" # changed`}
	if r := wsl.MountRootFrom("", conf); r != "/win/" {
		t.Error(r)
	}
	if r := wsl.MountRootFrom("", nil); r != "" {
		t.Errorf("guessed %q", r)
	}
}

func TestParseProbeReadsOnlyMarkedLines(t *testing.T) {
	out := "Welcome to Ubuntu (a login banner)\n\n@@drydock@@ home=/home/k\n@@drydock@@ path=/home/k/.local/bin:/usr/bin\n" +
		"@@drydock@@ kernel=6.6.87.2-microsoft-standard-WSL2\n@@drydock@@ mountc=/mnt/c/\n@@drydock@@ wslconf=[interop]\n" +
		"@@drydock@@ wslconf=enabled=false\n@@drydock@@ interop=1\n@@drydock@@ netmode=Mirrored\n@@drydock@@ setsid=/usr/bin/setsid\n" +
		"@@drydock@@ tool=/home/k/.local/bin/claude\n@@drydock@@ version=2.1.296 (Claude Code) \n@@drydock@@ authexit=0\n" +
		"@@drydock@@ auth={\"loggedIn\": true}\n@@drydock@@ done=1\n"
	in := wsl.ParseProbe("Ubuntu", out)
	if in.Home != "/home/k" || in.Path != "/home/k/.local/bin:/usr/bin" || !in.WSL2 || in.Paths.MountRoot != "/mnt/" || in.NetMode != "mirrored" ||
		!in.Setsid || in.Tool != "/home/k/.local/bin/claude" || in.Version != "2.1.296 (Claude Code)" || in.AuthExit != 0 || !in.Complete {
		t.Fatalf("%+v", in)
	}
	if in.Interop {
		t.Error("[interop] enabled=false in wsl.conf turns interop off whatever binfmt says")
	}
}

func TestForwardNamesOnlyTheChosenVariablesInWSLENVAndReplacesAnInheritedOne(t *testing.T) {
	env := []string{"WSLENV=USERPROFILE/p:GITHUB_TOKEN", "PATH=C:\\Windows", "ANTHROPIC_API_KEY=sk-x", "SSL_CERT_FILE=C:\\certs\\ca.pem", "GITHUB_TOKEN=g"}
	keep := func(n string) bool { return n == "ANTHROPIC_API_KEY" || n == "SSL_CERT_FILE" }
	got := wsl.Forward(env, keep, func(n string) bool { return n == "SSL_CERT_FILE" })
	var wslenv string
	for _, kv := range got {
		if v, ok := strings.CutPrefix(kv, "WSLENV="); ok {
			if wslenv != "" {
				t.Fatal("two WSLENV entries")
			}
			wslenv = v
		}
	}
	if wslenv != "ANTHROPIC_API_KEY:SSL_CERT_FILE/p" {
		t.Fatalf("WSLENV=%q", wslenv)
	}
}

func TestLaunchArgsPutTheMarkerAndEnvBeforeTheProgramAndNeverUseAShellForTheProgramsArguments(t *testing.T) {
	l := wsl.Launch{Distro: "Ubuntu", Dir: "/mnt/d/src/app", Marker: "abc", Env: []string{"HOME=/home/k", "PATH=/home/k/.local/bin:/usr/bin"},
		Argv: []string{"/home/k/.local/bin/claude", "-p", "--append-system-prompt-file", "/mnt/c/t/p.md", "it's $(not run)"}}
	a := l.Args()
	want := []string{"-d", "Ubuntu", "--cd", "/mnt/d/src/app", "--exec", "/usr/bin/env", "DRYDOCK_WSL_RUN=abc", "HOME=/home/k", "PATH=/home/k/.local/bin:/usr/bin", "/bin/sh", "-c"}
	if !slices.Equal(a[:len(want)], want) {
		t.Fatalf("%q", a)
	}
	if tail := a[len(a)-5:]; !slices.Equal(tail, l.Argv) {
		t.Fatalf("the program's arguments must be passed as they are: %q", tail)
	}
}

// requireHostTools skips where the stand-in distro cannot run (it runs the scripts on this host).
func requireHostTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in wsl.exe runs Linux scripts on a Linux development host; Windows CI drives the real wsl.exe (TestRealWSL*)")
	}
	for _, tool := range []string{"bash", "setsid", "getent"} {
		if _, err := os.Stat("/usr/bin/" + tool); err != nil {
			t.Skipf("%s is not on this host", tool)
		}
	}
}

func TestStandInHostListsDistrosAndProbesThroughALoginShell(t *testing.T) {
	requireHostTools(t)
	f := wsltest.NewFake(t, true, "Ubuntu-24.04", "docker-desktop")
	h := &wsl.Host{Exe: f.Exe, Env: func() []string { return wsl.BaseEnv(os.Environ()) }}
	ds, msg, err := h.Distros(context.Background())
	if err != nil || msg != "" || len(ds) != 2 || ds[0].Name != "Ubuntu-24.04" || !ds[0].Default || ds[0].Version != 2 {
		t.Fatalf("%+v %q %v", ds, msg, err)
	}
	in, err := h.Probe(context.Background(), "Ubuntu-24.04", "claude", []string{"--version"}, []string{"auth", "status"}, true)
	if err != nil {
		t.Fatal(err)
	}
	// claude lives in ~/.local/bin, which only a login shell's ~/.profile puts on the PATH
	if in.Tool != filepath.Join(f.Home, ".local", "bin", "claude") || !strings.Contains(in.Path, filepath.Join(f.Home, ".local", "bin")) || in.Home != f.Home {
		t.Fatalf("%+v", in)
	}
	if !strings.HasPrefix(in.Version, "2.1.296") || in.AuthExit != 0 || !strings.Contains(in.Auth, `"loggedIn": true`) || !in.Setsid {
		t.Fatalf("%+v", in)
	}
	if c, ok := h.Cached("ubuntu-24.04", "claude"); !ok || c.Tool != in.Tool {
		t.Fatal("the probe is cached by distro name, ignoring case")
	}
	if _, err := h.Probe(context.Background(), "NoSuchDistro", "claude", []string{"--version"}, nil, true); err == nil {
		t.Fatal("an unknown distro must be an error")
	}
}

// TestKillEndsEveryProcessOfTheRunEvenAfterWslExeIsGone proves the stop path on a development host: the stand-in wsl.exe,
// like the real one, does not take the program down when it is killed. Kill (the marker sweep) must end the program and its
// grandchildren, including one that started a session of its own.
func TestKillEndsEveryProcessOfTheRunEvenAfterWslExeIsGone(t *testing.T) {
	requireHostTools(t)
	f := wsltest.NewFake(t, false, "Ubuntu")
	h := &wsl.Host{Exe: f.Exe}
	marker := wsl.NewMarker()
	l := wsl.Launch{Distro: "Ubuntu", Dir: t.TempDir(), Marker: marker, Env: []string{"PATH=/usr/bin:/bin"},
		Argv: []string{"/bin/sh", "-c", "setsid sleep 600 >/dev/null 2>&1 </dev/null & sleep 601 >/dev/null 2>&1 </dev/null & echo started; exec sleep 602"}}
	pm := proc.New()
	p, err := pm.Start(context.Background(), proc.Spec{Path: f.Exe, Args: l.Args(), Env: wsl.Forward(os.Environ(), func(string) bool { return false }, nil)})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if n, _ := p.Stdout().Read(buf); !strings.Contains(string(buf[:n]), "started") {
		t.Fatalf("no output: %q", buf[:n])
	}
	waitFor(t, func() bool { pids, _ := h.Marked(context.Background(), "Ubuntu", marker); return len(pids) >= 3 })
	_ = p.Kill(true) // what the Job Object does on Windows: wsl.exe is gone ...
	<-p.Wait()
	if pids, _ := h.Marked(context.Background(), "Ubuntu", marker); len(pids) < 3 {
		t.Fatalf("... but its program and grandchildren should have outlived it here, as they can under WSL: %v", pids)
	}
	n, err := h.Kill(context.Background(), "Ubuntu", marker)
	if err != nil || n < 3 {
		t.Fatalf("killed %d: %v", n, err)
	}
	waitFor(t, func() bool { pids, _ := h.Marked(context.Background(), "Ubuntu", marker); return len(pids) == 0 })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
