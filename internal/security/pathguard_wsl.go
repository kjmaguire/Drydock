package security

import (
	"strings"

	"github.com/kjmaguire/drydock/internal/wsl"
)

// posixLexical is the guard for paths inside a WSL distro: POSIX rules (case-sensitive, "/" only), lexical. The distro's own
// files are reached from Windows through \\wsl.localhost, where Linux symlinks cannot be resolved reliably, so a symlink inside
// the distro that leaves the worktree is not caught (docs/security.md s6, "WSL"); paths on a Windows drive are also checked by
// the Windows guard with the filesystem.
var posixLexical = Guard{NoFS: true}

// ResolveWSL is Resolve for an agent running inside a WSL distro (decision P41). root is the worktree as Windows names it
// (D:\src\wt, or \\wsl.localhost\<distro>\home\k\wt); candidate is a path as the agent wrote it: a Linux path (/mnt/d/...,
// /home/k/...), relative to root, or a Windows path an interop command used (C:\x). The candidate is mapped into the distro,
// checked against the root there, and returned as its real location on Windows: a drive path (checked again on that drive
// for junctions and symlinks when g has filesystem access) or a \\wsl.localhost path. A home-relative path (~) is refused:
// the agent's HOME is the distro's, never inside a worktree.
func (g Guard) ResolveWSL(p wsl.Paths, root, candidate string) (string, error) {
	if candidate == "" {
		return "", perr(ErrPathEmpty, candidate, "")
	}
	if strings.ContainsRune(candidate, 0) {
		return "", perr(ErrPathInvalid, candidate, "NUL byte")
	}
	lroot, err := p.ToLinux(root)
	if err != nil {
		return "", perr(ErrPathRoot, root, err.Error())
	}
	cand := candidate
	if strings.HasPrefix(cand, "~") {
		return "", perr(ErrPathEscape, candidate, "the distro's home directory")
	}
	if wsl.IsWindowsPath(cand) {
		lin, err := p.ToLinux(cand)
		if err != nil {
			return "", perr(ErrPathEscape, candidate, err.Error())
		}
		cand = lin
	}
	lin, err := posixLexical.Resolve(lroot, cand)
	if err != nil {
		return "", err
	}
	win, ok := p.ToWindows(lin)
	if !ok {
		return "", perr(ErrPathInvalid, candidate, "not an absolute path inside the distro")
	}
	if g.Windows && !g.NoFS && p.OnDrive(lin) && p.OnDrive(lroot) {
		wroot, _ := p.ToWindows(lroot)
		return g.Resolve(wroot, win)
	}
	return win, nil
}
