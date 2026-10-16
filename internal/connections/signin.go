package connections

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// SignIn is how to start a Connection's own sign-in in a terminal the person watches and answers (decision P50): the CLI's
// unmodified login as its manifest names it (auth.login), started where the CLI is. Drydock starts it and never reads, copies
// or stores what the sign-in leaves behind (D1).
type SignIn struct {
	Program string
	Args    []string
	// Env is the program's whole environment: nil for a native CLI (Drydock's own, as a terminal's shell gets), wsl.exe's base
	// allowlist with an empty WSLENV for a sign-in inside a distro (nothing of Windows is copied in).
	Env []string
	Dir string // the Windows folder the program starts in
	// Distro and Marker are set for a sign-in inside WSL: every process of it carries the marker (wsl.RunEnv), so whatever is
	// left when its terminal ends is killed with Host.Kill, as an agent run's are.
	Distro, Marker string
}

// loginArgv is the manifest's own sign-in command (after the program name), or nil when it names none Drydock can start in a
// terminal (only a note, as Antigravity's says to run the CLI interactively).
func loginArgv(m *manifests.Manifest) []string {
	if l := m.Auth.Login; l != nil && l.Terminal && len(l.Argv) > 0 {
		return l.Argv
	}
	return nil
}

// signInText is the sign-in command as a person would type it ("claude auth login"), for the card; "" none.
func signInText(m *manifests.Manifest, binaries []string) string {
	argv := loginArgv(m)
	if argv == nil {
		return ""
	}
	name := m.Meta.ID
	if len(binaries) > 0 {
		name = binaries[0]
		for _, ext := range []string{".exe", ".cmd", ".bat"} {
			if strings.HasSuffix(strings.ToLower(name), ext) {
				name = name[:len(name)-len(ext)]
			}
		}
	}
	return name + " " + strings.Join(argv, " ")
}

// SignIn resolves how to start the sign-in of the Connection key names (the adapter id, or WSLKey for an install inside a
// distro). It checks that one Connection only, not every CLI and distro.
func (s *Service) SignIn(ctx context.Context, key string) (Connection, SignIn, error) {
	id, distro, isWSL := ParseWSLKey(key)
	if !isWSL {
		id = key
	}
	m, ok := s.Registry.Set.Get(id)
	if !ok {
		return Connection{}, SignIn{}, fmt.Errorf("no Connection named %s", id)
	}
	argv := loginArgv(m)
	if argv == nil {
		return Connection{}, SignIn{}, fmt.Errorf("%s has no sign-in command Drydock can start: sign in the way its vendor documents", m.Meta.DisplayName)
	}
	c, err := s.CheckKey(ctx, key)
	if err != nil {
		if isWSL {
			return Connection{}, SignIn{}, fmt.Errorf("no Connection of %s inside the WSL distro %s: %w", id, distro, err)
		}
		return Connection{}, SignIn{}, err
	}
	if !c.Installed || c.Path == "" {
		where := "on this computer"
		if isWSL {
			where = "inside the WSL distro " + c.Distro
		}
		return c, SignIn{}, fmt.Errorf("%s is not installed %s, so there is nothing to sign in to yet", m.Meta.DisplayName, where)
	}
	if !isWSL {
		prog, args, err := nativeSignIn(c.Path, argv, runtime.GOOS, os.Getenv("ComSpec"), os.Getenv("SystemRoot"))
		if err != nil {
			return c, SignIn{}, err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return c, SignIn{}, fmt.Errorf("sign in: %w", err)
		}
		return c, SignIn{Program: prog, Args: args, Dir: home}, nil
	}
	h := s.options().WSL
	if h == nil {
		return c, SignIn{}, errors.New("WSL is not available in this build")
	}
	exe, err := h.Path()
	if err != nil {
		return c, SignIn{}, fmt.Errorf("the sign-in inside %s cannot be started from here: %w", c.Distro, err)
	}
	// the login shell's HOME and PATH the probe found: a CLI that is a node script (an npm or nvm install) needs node from
	// the PATH ~/.profile builds, which wsl.exe's own shell does not read
	tool := m.Binaries.WSL[0]
	in, ok := h.Cached(c.Distro, tool)
	if !ok || in.Tool == "" {
		if in, err = h.Probe(ctx, c.Distro, tool, m.Detect.Version.Argv, nil, false); err != nil {
			return c, SignIn{}, err
		}
	}
	l := wslSignIn(c.Distro, in, argv)
	return c, SignIn{Program: exe, Args: l.Args(), Env: h.Environ(), Dir: os.TempDir(), Distro: c.Distro, Marker: l.Marker}, nil
}

// wslSignIn starts the CLI's login inside the distro as an agent starts there (the probed tool, HOME and PATH, the run marker),
// but interactive: it keeps the terminal, so the person answers it and Ctrl+C reaches it.
func wslSignIn(distro string, in wsl.Info, argv []string) wsl.Launch {
	dir := in.Home
	if dir == "" {
		dir = "~"
	}
	var env []string
	if in.Home != "" {
		env = append(env, "HOME="+in.Home)
	}
	if in.Path != "" {
		env = append(env, "PATH="+in.Path)
	}
	return wsl.Launch{Distro: distro, Dir: dir, Marker: wsl.NewMarker(), Env: env, Argv: append([]string{in.Tool}, argv...), Interactive: true}
}

// nativeSignIn is the program and arguments of a native CLI's login: the CLI itself by its absolute path, or on Windows an
// npm .cmd/.bat shim through cmd.exe by its full path (a pseudo-terminal starts neither a script nor a bare name).
func nativeSignIn(exe string, argv []string, goos, comspec, systemRoot string) (string, []string, error) {
	if !isAbs(goos, exe) {
		// a BinaryOverride may be relative: it means the process's working folder, never the terminal's
		p, err := exe, error(nil)
		if !strings.ContainsAny(p, `/\`) {
			p, err = exec.LookPath(p)
		}
		if err == nil {
			p, err = filepath.Abs(p)
		}
		if err != nil {
			return "", nil, fmt.Errorf("the sign-in cannot find %s: %w", exe, err)
		}
		exe = p
	}
	if goos == "windows" {
		if l := strings.ToLower(exe); strings.HasSuffix(l, ".cmd") || strings.HasSuffix(l, ".bat") {
			if comspec == "" && systemRoot != "" {
				comspec = systemRoot + `\System32\cmd.exe`
			}
			if comspec == "" {
				comspec = "cmd.exe" // the terminal looks a bare name up on PATH
			}
			return comspec, append([]string{"/c", exe}, argv...), nil
		}
	}
	return exe, append([]string(nil), argv...), nil
}

// isAbs reports whether p is absolute by goos's rules, not the host's (a Windows drive or UNC path, a Unix path from /),
// so the tests of either platform's command run on both.
func isAbs(goos, p string) bool {
	if goos == "windows" {
		return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`)
	}
	return path.IsAbs(p) // a Unix path, judged by Unix rules on any host
}
