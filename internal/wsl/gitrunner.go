package wsl

import (
	"fmt"
	"os"
	"strings"
)

// GitRunner runs Drydock's git calls for a repository that lives inside a distro with the distro's own git (decision P41), so
// the worktrees it makes link to each other with Linux paths, which the agent's git and the person's git inside the distro can
// read. It implements internal/git.Runner: the hardening flags are the same, Windows paths in the arguments and environment
// (the worktree folder, a temporary index, the empty hooks folder) are translated, and the environment Drydock sets for git
// goes on the command line through /usr/bin/env (it holds no secret: identity, editor and pager settings, paths).
type GitRunner struct {
	Exe   string // wsl.exe
	Paths Paths
}

// Command implements internal/git.Runner.
func (g GitRunner) Command(dir string, env, args []string) (string, []string, []string, error) {
	ldir, err := g.Paths.ToLinux(dir)
	if err != nil {
		return "", nil, nil, fmt.Errorf("git in the WSL distro %s: %w", g.Paths.Distro, err)
	}
	argv := []string{"-d", g.Paths.Distro, "--cd", ldir, "--exec", "/usr/bin/env"}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if IsWindowsPath(v) {
			if v, err = g.Paths.ToLinux(v); err != nil {
				return "", nil, nil, fmt.Errorf("git in the WSL distro %s: %s: %w", g.Paths.Distro, k, err)
			}
		}
		argv = append(argv, k+"="+v)
	}
	argv = append(argv, "git")
	for i, a := range args {
		t, err := g.arg(a, i > 0 && args[i-1] == "-c")
		if err != nil {
			return "", nil, nil, err
		}
		argv = append(argv, t)
	}
	return g.Exe, argv, BaseEnv(os.Environ()), nil
}

// arg translates a Windows path argument, or, after -c, the value of a key=value setting (-c core.hooksPath=C:/...). Other
// arguments (a commit message) are passed as they are.
func (g GitRunner) arg(a string, setting bool) (string, error) {
	if IsWindowsPath(a) {
		l, err := g.Paths.ToLinux(a)
		if err != nil {
			return "", fmt.Errorf("git in the WSL distro %s: %w", g.Paths.Distro, err)
		}
		return l, nil
	}
	if k, v, ok := strings.Cut(a, "="); ok && setting && IsWindowsPath(v) {
		l, err := g.Paths.ToLinux(v)
		if err != nil {
			return "", fmt.Errorf("git in the WSL distro %s: %w", g.Paths.Distro, err)
		}
		return k + "=" + l, nil
	}
	return a, nil
}

// HostPath implements internal/git.Runner: a path git printed, where Windows finds it.
func (g GitRunner) HostPath(p string) string {
	if w, ok := g.Paths.ToWindows(p); ok {
		return w
	}
	return p
}

// LinuxPath is p, a path of this machine, as git inside the distro names it.
func (g GitRunner) LinuxPath(p string) (string, error) {
	return g.Paths.ToLinux(p)
}
