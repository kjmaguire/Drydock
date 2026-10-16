package security

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Path guard sentinels. A *PathError wraps exactly one of them, so callers use errors.Is.
var (
	ErrPathEmpty         = errors.New("path is empty")
	ErrPathInvalid       = errors.New("path contains an invalid character")
	ErrPathEscape        = errors.New("path resolves outside the root")
	ErrSymlinkEscape     = errors.New("path leaves the root through a symlink or junction")
	ErrPathDevice        = errors.New("device namespace path")
	ErrPathUNC           = errors.New("UNC path")
	ErrPathDriveRelative = errors.New("drive-relative or root-relative path")
	ErrPathReserved      = errors.New("reserved device name")
	ErrPathADS           = errors.New("alternate data stream")
	ErrPathTrailing      = errors.New("segment ends with a dot or space")
	ErrPathShortName     = errors.New("suspicious 8.3 short name")
	ErrPathRoot          = errors.New("invalid root")
)

// PathError is the typed guard failure.
type PathError struct {
	Kind   error // one of the Err* sentinels
	Path   string
	Detail string
}

func (e *PathError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("pathguard: %s: %q (%s)", e.Kind, e.Path, e.Detail)
	}
	return fmt.Sprintf("pathguard: %s: %q", e.Kind, e.Path)
}

// Unwrap exposes the sentinel.
func (e *PathError) Unwrap() error { return e.Kind }

func perr(kind error, path, detail string) error {
	return &PathError{Kind: kind, Path: path, Detail: detail}
}

// Guard resolves candidate paths against a root (docs/security.md s6). The zero value is not usable; use NewGuard.
type Guard struct {
	// Windows selects Windows path semantics (drive letters, backslashes, reserved names, case-insensitive compare).
	Windows bool
	// NoFS skips every filesystem step (symlink resolution, 8.3 lookup). It exists so the pure-string rules of the
	// Windows flavor can be tested on any host; the product guard always has filesystem access.
	NoFS bool
	// LongPath expands a Windows path to its long form (GetLongPathName). Nil uses the platform implementation
	// (a portable identity stub off Windows).
	LongPath func(string) (string, error)
}

// NewGuard returns the guard for the host OS.
func NewGuard() Guard { return Guard{Windows: runtime.GOOS == "windows"} }

// Resolve is the package-level shortcut for NewGuard().Resolve.
func Resolve(root, candidate string) (string, error) { return NewGuard().Resolve(root, candidate) }

// Resolve returns the cleaned absolute path of candidate (relative candidates join onto root, never the process
// cwd) after proving it stays inside root, or a *PathError. With filesystem access the result has symlinks and
// junctions in its deepest existing ancestor resolved, so callers operate on exactly the path that was checked.
func (g Guard) Resolve(root, candidate string) (string, error) {
	if candidate == "" {
		return "", perr(ErrPathEmpty, candidate, "")
	}
	if strings.ContainsRune(candidate, 0) || strings.ContainsRune(root, 0) {
		return "", perr(ErrPathInvalid, candidate, "NUL byte")
	}
	rp, ok := g.parse(root)
	if !ok || !rp.abs {
		return "", perr(ErrPathRoot, root, "root must be an absolute path")
	}
	if g.Windows {
		if err := g.windowsSyntax(candidate); err != nil {
			return "", err
		}
	}
	cp, ok := g.parse(candidate)
	if !ok {
		return "", perr(ErrPathInvalid, candidate, "unparseable")
	}
	var full parsedPath
	if cp.abs {
		full = cp
	} else {
		full = parsedPath{vol: rp.vol, abs: true, segs: append(append([]string{}, rp.segs...), cp.segs...)}
	}
	full.segs = g.cleanSegs(full.segs)
	if !g.contains(rp, full) {
		return "", perr(ErrPathEscape, candidate, "lexically outside root")
	}
	// report the root part with the root's own spelling (matters for case-insensitive comparison on Windows)
	full.vol = rp.vol
	copy(full.segs, rp.segs)
	if g.Windows {
		if err := g.checkShortNames(full, rp, candidate); err != nil {
			return "", err
		}
	}
	out := g.render(full)
	if g.NoFS {
		return out, nil
	}
	return g.resolveFS(rp, full, candidate)
}

// Inside reports whether Resolve accepts candidate.
func (g Guard) Inside(root, candidate string) bool {
	_, err := g.Resolve(root, candidate)
	return err == nil
}

type parsedPath struct {
	vol  string // "C:" (upper case) on Windows, "" otherwise
	abs  bool
	segs []string
}

func (g Guard) isSep(c byte) bool { return c == '/' || (g.Windows && c == '\\') }

// parse splits a path into volume, rootedness and segments without touching the filesystem.
func (g Guard) parse(p string) (parsedPath, bool) {
	var pp parsedPath
	rest := p
	if g.Windows {
		if len(rest) >= 2 && rest[1] == ':' && isLetter(rest[0]) {
			pp.vol = strings.ToUpper(rest[:2])
			rest = rest[2:]
			if len(rest) > 0 && g.isSep(rest[0]) {
				pp.abs = true
			}
		} else if len(rest) > 0 && g.isSep(rest[0]) {
			// root-relative without a drive: not absolute for our purposes (rejected by windowsSyntax for candidates)
			pp.abs = false
		}
	} else if strings.HasPrefix(rest, "/") {
		pp.abs = true
	}
	for _, s := range g.splitSegs(rest) {
		if s == "" || s == "." {
			continue
		}
		pp.segs = append(pp.segs, s)
	}
	return pp, true
}

func (g Guard) splitSegs(p string) []string {
	if g.Windows {
		p = strings.ReplaceAll(p, "\\", "/")
	}
	return strings.Split(p, "/")
}

// cleanSegs resolves "." and ".." lexically; ".." at the root is dropped.
func (g Guard) cleanSegs(segs []string) []string {
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		switch s {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, s)
		}
	}
	return out
}

func (g Guard) eq(a, b string) bool {
	if g.Windows {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// contains reports whether full is root or below it.
func (g Guard) contains(root, full parsedPath) bool {
	if !g.eq(root.vol, full.vol) || len(full.segs) < len(root.segs) {
		return false
	}
	for i, s := range root.segs {
		if !g.eq(s, full.segs[i]) {
			return false
		}
	}
	return true
}

func (g Guard) render(p parsedPath) string {
	if g.Windows {
		return p.vol + `\` + strings.Join(p.segs, `\`)
	}
	return "/" + strings.Join(p.segs, "/")
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

var reservedNames = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}

// isReservedName reports whether a Windows segment names a legacy device: the name part before the first dot, with
// trailing spaces removed, is CON, PRN, AUX, NUL, COM1-9 or LPT1-9 (also with superscript digits).
func isReservedName(seg string) bool {
	base := seg
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(strings.TrimRight(base, " "))
	if reservedNames[base] {
		return true
	}
	for _, pfx := range []string{"COM", "LPT"} {
		if strings.HasPrefix(base, pfx) {
			d := strings.TrimPrefix(base, pfx)
			if d == "¹" || d == "²" || d == "³" || (len(d) == 1 && d[0] >= '0' && d[0] <= '9') {
				return true
			}
		}
	}
	return false
}

var shortNameRE = regexp.MustCompile(`^[^~.]{1,8}~[0-9]+(\.[^.]{0,3})?$`)

// windowsSyntax applies the pure-string rules of docs/security.md s6 steps (1)-(5) to the raw candidate.
func (g Guard) windowsSyntax(c string) error {
	n := strings.ReplaceAll(c, "/", `\`)
	switch {
	case strings.HasPrefix(n, `\\.\`), strings.HasPrefix(n, `\\?\`), strings.HasPrefix(n, `\??\`), strings.EqualFold(n, `\\.`), strings.EqualFold(n, `\\?`):
		return perr(ErrPathDevice, c, "device namespace")
	case strings.HasPrefix(n, `\\`):
		return perr(ErrPathUNC, c, "")
	}
	rest := n
	if len(n) >= 2 && n[1] == ':' && isLetter(n[0]) {
		rest = n[2:]
		if rest == "" || rest[0] != '\\' {
			return perr(ErrPathDriveRelative, c, "drive-relative")
		}
	} else if strings.HasPrefix(n, `\`) {
		return perr(ErrPathDriveRelative, c, "rooted without a drive")
	}
	for _, seg := range strings.Split(rest, `\`) {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if strings.ContainsRune(seg, ':') {
			return perr(ErrPathADS, c, seg)
		}
		if strings.ContainsAny(seg, `<>"|?*`) {
			return perr(ErrPathInvalid, c, seg)
		}
		for _, r := range seg {
			if r < 0x20 {
				return perr(ErrPathInvalid, c, "control character")
			}
		}
		if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return perr(ErrPathTrailing, c, seg)
		}
		if isReservedName(seg) {
			return perr(ErrPathReserved, c, seg)
		}
	}
	return nil
}

// checkShortNames flags segments below the root that look like 8.3 aliases and differ from the long form.
func (g Guard) checkShortNames(full, root parsedPath, orig string) error {
	for i := len(root.segs); i < len(full.segs); i++ {
		if !shortNameRE.MatchString(full.segs[i]) {
			continue
		}
		if g.NoFS && g.LongPath == nil {
			continue
		}
		prefix := parsedPath{vol: full.vol, abs: true, segs: full.segs[:i+1]}
		long, err := g.longPath()(g.render(prefix))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // nothing exists under that name, so it aliases nothing
			}
			return perr(ErrPathShortName, orig, "cannot verify "+full.segs[i])
		}
		lp, _ := g.parse(long)
		if len(lp.segs) != i+1 || !strings.EqualFold(lp.segs[i], full.segs[i]) {
			return perr(ErrPathShortName, orig, fmt.Sprintf("%s is an alias of %s", full.segs[i], lp.segs[len(lp.segs)-1]))
		}
	}
	return nil
}

func (g Guard) longPath() func(string) (string, error) {
	if g.LongPath != nil {
		return g.LongPath
	}
	return longPathName
}

// resolveFS proves containment against real filesystem state: it resolves the root and the deepest existing ancestor
// of the target (EvalSymlinks follows symlinks and junctions as the OS would) and requires the result inside the
// resolved root. The not-yet-existing tail is appended lexically.
func (g Guard) resolveFS(root, full parsedPath, orig string) (string, error) {
	rootStr := g.render(root)
	realRoot, err := filepath.EvalSymlinks(rootStr)
	if err != nil {
		return "", perr(ErrPathRoot, rootStr, err.Error())
	}
	if st, err := os.Stat(realRoot); err != nil || !st.IsDir() {
		return "", perr(ErrPathRoot, rootStr, "root is not a directory")
	}
	rr, _ := g.parse(realRoot)
	rr.abs = true

	// find the deepest existing ancestor
	n := len(full.segs)
	var existing string
	for ; n >= len(root.segs); n-- {
		cand := g.render(parsedPath{vol: full.vol, abs: true, segs: full.segs[:n]})
		_, err := os.Lstat(cand)
		if err == nil {
			existing = cand
			break
		}
		if errors.Is(err, fs.ErrNotExist) || isNotDir(err) {
			continue
		}
		return "", perr(ErrPathInvalid, orig, err.Error())
	}
	if existing == "" {
		return "", perr(ErrPathEscape, orig, "no existing ancestor inside root")
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		// Lstat succeeded but the link cannot be followed: dangling or looping link. Writing through it could create
		// its target outside the root, so fail closed.
		return "", perr(ErrSymlinkEscape, orig, "unresolvable link: "+err.Error())
	}
	rp, _ := g.parse(real)
	rp.abs = true
	if !g.contains(rr, rp) {
		return "", perr(ErrSymlinkEscape, orig, real)
	}
	rp.segs = append(rp.segs, full.segs[n:]...)
	return g.render(rp), nil
}

func isNotDir(err error) bool {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return strings.Contains(pe.Err.Error(), "not a directory")
	}
	return false
}
