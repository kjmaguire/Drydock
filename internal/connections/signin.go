package connections

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// signInArgs is each CLI's own interactive sign-in, as its vendor documents it. A CLI not listed here has no sign-in Drydock
// can start; its card says how to sign in by hand.
var signInArgs = map[string][]string{
	"claude":   {"auth", "login"},
	"codex":    {"login"},
	"opencode": {"auth", "login"},
}

// SignInCommand is the program and arguments that start a Connection's own sign-in, for a terminal the person watches and
// answers (decision P50): the CLI's unmodified login, in the distro for an install inside WSL (wslExe is wsl.exe there). Drydock
// starts it and never reads, copies or stores what the sign-in leaves behind (D1).
func SignInCommand(c Connection, wslExe string) (string, []string, error) {
	return signInCommand(c, wslExe, runtime.GOOS, os.Getenv("COMSPEC"))
}

func signInCommand(c Connection, wslExe, goos, comspec string) (string, []string, error) {
	sub, ok := signInArgs[c.Adapter]
	if !ok {
		return "", nil, fmt.Errorf("%s has no sign-in command Drydock can start: sign in the way its vendor documents", c.Adapter)
	}
	if !c.Installed || c.Path == "" {
		where := "on this computer"
		if c.RunMode == "wsl" {
			where = "inside the WSL distro " + c.Distro
		}
		return "", nil, fmt.Errorf("%s is not installed %s, so there is nothing to sign in to yet", c.Adapter, where)
	}
	if c.RunMode == "wsl" {
		if wslExe == "" {
			return "", nil, fmt.Errorf("wsl.exe is not available, so the sign-in inside %s cannot be started from here", c.Distro)
		}
		if c.Distro == "" {
			return "", nil, fmt.Errorf("the WSL Connection of %s names no distro", c.Adapter)
		}
		return wslExe, append([]string{"-d", c.Distro, "--", c.Path}, sub...), nil
	}
	if goos == "windows" {
		if l := strings.ToLower(c.Path); strings.HasSuffix(l, ".cmd") || strings.HasSuffix(l, ".bat") {
			// a .cmd (the npm shim) runs through cmd.exe; ConPTY cannot start it on its own
			if comspec == "" {
				comspec = "cmd.exe"
			}
			return comspec, append([]string{"/c", c.Path}, sub...), nil
		}
	}
	return c.Path, append([]string(nil), sub...), nil
}
