// Package wsltest holds LABELLED TEST DOUBLES for the WSL run mode. They are never presented as WSL or as Claude Code.
//
//   - StandIn is a stand-in for wsl.exe on a Linux development host, which has no WSL. It answers `-l -q` and `-l -v` in
//     UTF-16LE as wsl.exe does, and runs `-d <distro> [--cd <dir>] --exec <program> ...` directly on the host with the
//     environment wsl.exe gives a program: a Linux HOME and PATH of the stand-in distro, and only the Windows-side variables
//     WSLENV names. Like wsl.exe, killing it does not kill the program it started, which is the problem the run mode's stop
//     has to solve.
//   - ClaudeStandIn is a POSIX sh stand-in for `claude` that a test installs in the stand-in distro's ~/.local/bin (or, on
//     Windows CI, in a real distro).
package wsltest

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// ClaudeStandIn is the stand-in claude script (claude-standin.sh).
//
//go:embed claude-standin.sh
var ClaudeStandIn []byte

// Environment of the stand-in wsl.exe (set by the wrapper script Fake writes, so a scrubbed environment cannot lose them).
const (
	EnvStandIn = "DRYDOCK_WSL_STANDIN"         // "1": the test binary acts as wsl.exe
	envDistros = "DRYDOCK_WSL_STANDIN_DISTROS" // comma-separated, the first is the default
	envHome    = "DRYDOCK_WSL_STANDIN_HOME"    // the Linux HOME of the stand-in distro
	envLog     = "DRYDOCK_WSL_STANDIN_LOG"     // every invocation's argv is appended here, one JSON array per line
)

// IsStandIn reports whether this process was started as the stand-in wsl.exe. A test package's TestMain calls StandIn then.
func IsStandIn() bool { return os.Getenv(EnvStandIn) == "1" }

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := []byte{0xFF, 0xFE}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

// StandIn is the stand-in wsl.exe's main.
func StandIn(args []string) int {
	if f := os.Getenv(envLog); f != "" {
		if fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			b, _ := json.Marshal(args)
			_, _ = fh.Write(append(b, '\n'))
			_ = fh.Close()
		}
	}
	distros := strings.Split(os.Getenv(envDistros), ",")
	if len(args) >= 2 && (args[0] == "-l" || args[0] == "--list") {
		var b strings.Builder
		switch args[1] {
		case "-q", "--quiet":
			for _, d := range distros {
				b.WriteString(d + "\r\n")
			}
		case "-v", "--verbose":
			b.WriteString("  NAME            STATE           VERSION\r\n")
			for i, d := range distros {
				mark := " "
				if i == 0 {
					mark = "*"
				}
				fmt.Fprintf(&b, "%s %-15s %-15s %d\r\n", mark, d, "Running", 2)
			}
		}
		_, _ = os.Stdout.Write(utf16le(b.String()))
		return 0
	}
	var distro, dir string
	i := 0
	for ; i < len(args); i++ {
		switch args[i] {
		case "-d", "--distribution":
			i++
			if i < len(args) {
				distro = args[i]
			}
			continue
		case "--cd":
			i++
			if i < len(args) {
				dir = args[i]
			}
			continue
		case "-e", "--exec":
			i++
		default:
			_, _ = os.Stdout.Write(utf16le("stand-in wsl.exe: unsupported argument " + args[i] + "\r\n"))
			return 1
		}
		break
	}
	known := false
	for _, d := range distros {
		known = known || strings.EqualFold(d, distro)
	}
	if !known {
		_, _ = os.Stdout.Write(utf16le("There is no distribution with the supplied name.\r\nError code: Wsl/Service/WSL_E_DISTRO_NOT_FOUND\r\n"))
		return 1
	}
	if i >= len(args) {
		_, _ = os.Stdout.Write(utf16le("stand-in wsl.exe: nothing to run\r\n"))
		return 1
	}
	cmd := exec.Command(args[i], args[i+1:]...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = linuxEnv(distro)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if c := ee.ExitCode(); c > 0 {
				return c
			}
			return 1
		}
		_, _ = os.Stdout.Write(utf16le("stand-in wsl.exe: " + err.Error() + "\r\n"))
		return 1
	}
	return 0
}

// linuxEnv is what wsl.exe gives a program it starts: the distro's own HOME, PATH and user, and the variables WSLENV names
// (flags after a slash dropped: the stand-in's paths are already Linux paths).
func linuxEnv(distro string) []string {
	home := os.Getenv(envHome)
	env := []string{"HOME=" + home, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8",
		"WSL_DISTRO_NAME=" + distro, "USER=" + os.Getenv("USER"), "LOGNAME=" + os.Getenv("LOGNAME"), "TERM=dumb"}
	for _, n := range strings.Split(os.Getenv("WSLENV"), ":") {
		name, _, _ := strings.Cut(n, "/")
		if name == "" {
			continue
		}
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// Fake is a stand-in WSL on a development host: Exe is the stand-in wsl.exe, Home the stand-in distro's Linux HOME (with a
// ~/.profile that puts ~/.local/bin on the PATH, as Ubuntu's does), Log the file every invocation is recorded in.
type Fake struct {
	Exe, Home, Log string
	Distros        []string
}

// NewFake writes a stand-in WSL under a temporary directory. The calling test binary must call StandIn from its TestMain when
// IsStandIn is true. With claude set, the stand-in claude is installed in ~/.local/bin.
func NewFake(t testing.TB, claude bool, distros ...string) *Fake {
	t.Helper()
	if len(distros) == 0 {
		distros = []string{"Ubuntu"}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := &Fake{Exe: filepath.Join(dir, "wsl"), Home: filepath.Join(dir, "home"), Log: filepath.Join(dir, "wsl.log"), Distros: distros}
	if err := os.MkdirAll(filepath.Join(f.Home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := "if [ -d \"$HOME/.local/bin\" ]; then PATH=\"$HOME/.local/bin:$PATH\"; fi\n"
	if err := os.WriteFile(filepath.Join(f.Home, ".profile"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	// bash -l reads ~/.bash_profile first when it exists; the host's own must not stand in for the stand-in distro's
	if err := os.WriteFile(filepath.Join(f.Home, ".bash_profile"), []byte(". \"$HOME/.profile\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the stand-in distro's wslpath: only -u of a drive path, under the default mount root
	wslpath := "#!/bin/sh\n# LABELLED TEST DOUBLE: wslpath of the stand-in distro (internal/wsl/wsltest)\n[ \"$1\" = -u ] || exit 1\n" +
		"d=$(printf '%s' \"$2\" | cut -c1 | tr 'A-Z' 'a-z'); r=$(printf '%s' \"$2\" | cut -c4- | tr '\\\\' '/')\nprintf '/mnt/%s/%s\\n' \"$d\" \"$r\"\n"
	if err := os.WriteFile(filepath.Join(f.Home, ".local", "bin", "wslpath"), []byte(wslpath), 0o755); err != nil {
		t.Fatal(err)
	}
	if claude {
		if err := os.WriteFile(filepath.Join(f.Home, ".local", "bin", "claude"), Script(), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	script := fmt.Sprintf("#!/bin/sh\n# LABELLED TEST DOUBLE: the stand-in wsl.exe (internal/wsl/wsltest)\n%s=1 %s=%s %s=%s %s=%s exec %s \"$@\"\n",
		EnvStandIn, envDistros, q(strings.Join(distros, ",")), envHome, q(f.Home), envLog, q(f.Log), q(self))
	if err := os.WriteFile(f.Exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// Script is the stand-in claude script with LF line ends whatever the checkout did to it.
func Script() []byte { return bytes.ReplaceAll(ClaudeStandIn, []byte("\r\n"), []byte("\n")) }

// EnvInteropCheck makes a test binary answer the stand-in claude's interop check (see InteropCheckMain) instead of testing.
const EnvInteropCheck = "DRYDOCK_INTEROP_CHECK"

// IsInteropCheck reports whether this process was started by the stand-in claude as the MCP server command.
func IsInteropCheck() bool { return os.Getenv(EnvInteropCheck) == "1" }

// InteropCheckMain answers the stand-in claude's interop check: one line saying which of the gateway's variables arrived (the
// token's length only, never its value).
func InteropCheckMain() int {
	u := os.Getenv("DRYDOCK_MCP_URL")
	fmt.Printf("interop-ok tokenlen=%d url=%s\n", len(os.Getenv("DRYDOCK_MCP_TOKEN")), u)
	return 0
}

// InstallClaude puts the stand-in claude in ~/.local/bin of a real distro through wsl.exe. It refuses to replace a claude that
// is not the stand-in, so a person's real install is never overwritten.
func InstallClaude(wslExe, distro string) error {
	check := exec.Command(wslExe, "-d", distro, "--exec", "/bin/sh", "-c",
		`f="$HOME/.local/bin/claude"; if [ -e "$f" ] && ! grep -q "LABELLED TEST DOUBLE" "$f"; then echo real; fi`)
	out, err := check.Output()
	if err != nil {
		return fmt.Errorf("checking the distro's claude: %w", err)
	}
	if strings.Contains(string(out), "real") {
		return errors.New("~/.local/bin/claude in the distro is a real Claude Code install; the stand-in is not installed over it")
	}
	cmd := exec.Command(wslExe, "-d", distro, "--exec", "/bin/sh", "-c",
		`mkdir -p "$HOME/.local/bin" && cat > "$HOME/.local/bin/claude" && chmod 755 "$HOME/.local/bin/claude"`)
	cmd.Stdin = bytes.NewReader(Script())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("installing the stand-in claude: %v: %s", err, out)
	}
	return nil
}

// Calls returns the argv of every stand-in wsl.exe invocation so far.
func (f *Fake) Calls() [][]string {
	b, _ := os.ReadFile(f.Log)
	var out [][]string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if l != "" {
			var a []string
			if json.Unmarshal([]byte(l), &a) == nil {
				out = append(out, a)
			}
		}
	}
	return out
}
