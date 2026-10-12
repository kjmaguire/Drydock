package wsl

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Paths maps paths between Windows and one distro. MountRoot is where the distro mounts Windows drives, read from the distro
// itself (`wslpath -u 'C:\'`, else `[automount] root` in /etc/wsl.conf; never assumed): "/mnt/" unless the person changed it.
type Paths struct {
	Distro    string `json:"distro"`
	MountRoot string `json:"mountRoot"`
}

// ErrNotMapped is returned for a path that has no form on the other side (a network share, another distro's files).
var ErrNotMapped = errors.New("wsl: the path has no form inside the distro")

// UNCHost is the host name Windows 11 serves distro files under (\\wsl.localhost\<distro>\...); \\wsl$\ is the older alias.
const UNCHost = "wsl.localhost"

// ParseUNC splits a distro file path as Windows names it (\\wsl.localhost\<distro>\..., \\wsl$\<distro>\..., either slash, with
// or without the \\?\UNC\ prefix) into the distro and the Linux path. ok is false for anything else.
func ParseUNC(p string) (distro, linux string, ok bool) {
	s := strings.ReplaceAll(p, "/", `\`)
	low := strings.ToLower(s)
	for _, pre := range []string{`\\?\unc\`, `\\.\unc\`} {
		if strings.HasPrefix(low, pre) {
			s, low = `\\`+s[len(pre):], `\\`+low[len(pre):]
		}
	}
	var rest string
	switch {
	case strings.HasPrefix(low, `\\`+UNCHost+`\`):
		rest = s[len(`\\`+UNCHost+`\`):]
	case strings.HasPrefix(low, `\\wsl$\`):
		rest = s[len(`\\wsl$\`):]
	default:
		return "", "", false
	}
	distro, tail, _ := strings.Cut(rest, `\`)
	if distro == "" {
		return "", "", false
	}
	return distro, path.Clean("/" + strings.ReplaceAll(tail, `\`, "/")), true
}

// UNC is the Windows path of a Linux path inside distro: \\wsl.localhost\<distro>\<path with backslashes>.
func UNC(distro, linux string) string {
	p := path.Clean("/" + linux)
	if p == "/" {
		return `\\` + UNCHost + `\` + distro + `\`
	}
	return `\\` + UNCHost + `\` + distro + strings.ReplaceAll(p, "/", `\`)
}

// root is MountRoot with exactly one trailing slash ("" when unknown).
func (p Paths) root() string {
	r := strings.TrimSpace(p.MountRoot)
	if r == "" {
		return ""
	}
	return strings.TrimRight(r, "/") + "/"
}

// IsWindowsPath reports whether s is written the Windows way: a drive path (C:\x, C:/x) or a UNC path (\\host\share).
func IsWindowsPath(s string) bool {
	return driveLetter(s) || strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, `//`) && len(s) > 2 && s[2] != '/'
}

func driveLetter(s string) bool {
	return len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') && (s[0]|0x20 >= 'a' && s[0]|0x20 <= 'z')
}

// ToLinux maps a Windows path to the path the distro sees: a drive path under the mount root (D:\src\app with root /mnt/ is
// /mnt/d/src/app; a Dev Drive is an ordinary drive letter), a path of this distro's own files (\\wsl.localhost\<distro>\...) to
// its Linux path. ".." stops at the drive root (or the distro's "/"), as Windows resolves it, so it never reaches the mount
// root. A path that is already a Linux absolute path is returned cleaned. Another distro's files, network shares,
// relative and drive-relative paths are refused: they have no reliable form inside the distro.
func (p Paths) ToLinux(win string) (string, error) {
	switch {
	case win == "":
		return "", fmt.Errorf("%w: empty path", ErrNotMapped)
	case strings.HasPrefix(win, "/") && !strings.HasPrefix(win, "//"):
		return path.Clean(win), nil
	}
	if d, lin, ok := ParseUNC(win); ok {
		if !strings.EqualFold(d, p.Distro) {
			return "", fmt.Errorf("%w: %s is in the distro %s, not %s", ErrNotMapped, win, d, p.Distro)
		}
		return lin, nil
	}
	if driveLetter(win) {
		r := p.root()
		if r == "" {
			return "", fmt.Errorf("%w: the distro's mount root for Windows drives is not known", ErrNotMapped)
		}
		if strings.Contains(win, "~") {
			win = longPath(win) // a short name (RUNNER~1) means nothing to the distro
		}
		// the tail is cleaned on its own, rooted at the drive, so ".." stops at the drive root as it does on Windows
		// (D:\..\..\mnt\d\x is D:\mnt\d\x); cleaning after the mount root is prefixed would climb out of the drive
		rest := path.Clean("/" + strings.ReplaceAll(win[3:], `\`, "/"))
		return path.Clean(r + strings.ToLower(win[:1]) + rest), nil
	}
	if strings.HasPrefix(win, `\\`) || strings.HasPrefix(win, "//") {
		return "", fmt.Errorf("%w: %s is a network path", ErrNotMapped, win)
	}
	return "", fmt.Errorf("%w: %s is not an absolute path", ErrNotMapped, win)
}

// ToWindows maps a Linux absolute path of the distro to where it really is on Windows: under the mount root it is a drive path
// (/mnt/d/src is D:\src), anywhere else it is the distro's own file (\\wsl.localhost\<distro>\home\...). ok is false for a
// relative path.
func (p Paths) ToWindows(linux string) (string, bool) {
	if !strings.HasPrefix(linux, "/") {
		return "", false
	}
	c := path.Clean(linux)
	if r := p.root(); r != "" {
		rel, found := strings.CutPrefix(c+"/", r)
		if found && len(rel) >= 2 && rel[1] == '/' && (rel[0] >= 'a' && rel[0] <= 'z' || rel[0] >= 'A' && rel[0] <= 'Z') {
			rest := strings.TrimSuffix(rel[2:], "/")
			return strings.ToUpper(rel[:1]) + `:\` + strings.ReplaceAll(rest, "/", `\`), true
		}
	}
	return UNC(p.Distro, c), true
}

// OnDrive reports whether a Linux path is a Windows drive seen through the mount root (read across the 9P file share, which is
// slower than the distro's own disk).
func (p Paths) OnDrive(linux string) bool {
	w, ok := p.ToWindows(linux)
	return ok && driveLetter(w)
}

// MountRootFrom reads the mount root from what `wslpath -u 'C:\'` printed (/mnt/c/ means /mnt/), else from the lines of
// /etc/wsl.conf ([automount] root = ...). "" when neither says.
func MountRootFrom(wslpathC string, wslConf []string) string {
	s := strings.TrimSpace(wslpathC)
	if strings.HasPrefix(s, "/") {
		s = strings.TrimRight(s, "/")
		if strings.HasSuffix(strings.ToLower(s), "/c") {
			return strings.TrimSuffix(s[:len(s)-1], "/") + "/"
		}
	}
	if v, ok := ConfValue(wslConf, "automount", "root"); ok && strings.HasPrefix(v, "/") {
		return strings.TrimRight(v, "/") + "/"
	}
	return ""
}

// ConfValue reads key in [section] of an ini-style file given as lines (/etc/wsl.conf, .wslconfig). Names compare without case.
func ConfValue(lines []string, section, key string) (string, bool) {
	in := false
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || l[0] == '#' || l[0] == ';' {
			continue
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			in = strings.EqualFold(strings.TrimSpace(l[1:len(l)-1]), section)
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			v = strings.TrimSpace(v)
			if i := strings.IndexAny(v, "#;"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			return strings.Trim(v, `"`), true
		}
	}
	return "", false
}
