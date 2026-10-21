package mcp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Confinement for the Workspace server's file access (brief section 16, security.md section 6). This is a small local
// implementation: internal/security/pathguard is being built separately and is not imported. It resolves symlinks and
// junctions the way the OS does (EvalSymlinks, which on Windows follows reparse points), requires the final path to be
// inside the worktree, re-verifies after open to narrow the check/use race, and refuses protected files. The residual race
// against a hostile concurrent process, and hard links to outside files, are not covered: a worktree is not a sandbox.

// ErrOutsideRoot is returned for a path that resolves outside the worktree.
var ErrOutsideRoot = errors.New("path is outside the worktree")

// ErrProtectedPath is returned for paths the Workspace server never serves (git internals, credential files).
var ErrProtectedPath = errors.New("path is protected and cannot be read through the gateway")

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// lexicalCheck rejects requests that need no filesystem access to reject. windows selects the Windows-only rules
// (device namespaces, alternate data streams, reserved names, trailing dots and spaces).
func lexicalCheck(requested string, windows bool) error {
	if requested == "" {
		return errors.New("path is empty")
	}
	if len(requested) > 4096 {
		return errors.New("path is too long")
	}
	if strings.ContainsRune(requested, 0) {
		return errors.New("path contains a NUL byte")
	}
	if strings.HasPrefix(requested, `\\`) || strings.HasPrefix(requested, "//") && windows {
		return errors.New("UNC paths and device namespaces are not allowed")
	}
	if !windows {
		return nil
	}
	rest := requested
	if len(rest) >= 2 && rest[1] == ':' {
		if !isASCIILetter(rest[0]) {
			return errors.New("invalid drive")
		}
		rest = rest[2:]
		if rest == "" || (rest[0] != '\\' && rest[0] != '/') {
			return errors.New("drive-relative paths are not allowed")
		}
	}
	if strings.Contains(rest, ":") {
		return errors.New("alternate data streams are not allowed")
	}
	for _, seg := range strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." || seg == ".." {
			continue
		}
		if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return errors.New("segments ending in a dot or space are not allowed")
		}
		base := seg
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if windowsReserved[strings.ToUpper(strings.TrimRight(base, " "))] {
			return fmt.Errorf("reserved device name %q", seg)
		}
	}
	return nil
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// protectedRel reports whether a worktree-relative path is never served.
func protectedRel(rel string) bool {
	segs := strings.Split(filepath.ToSlash(rel), "/")
	for i, s := range segs {
		ls := strings.ToLower(s)
		if ls == ".git" {
			return true
		}
		if i == len(segs)-1 && sensitiveName(ls) {
			return true
		}
	}
	return false
}

func sensitiveName(ls string) bool {
	switch ls {
	case ".env.example", ".env.sample", ".env.template", ".env.dist":
		return false
	case ".npmrc", ".netrc", ".pgpass", ".git-credentials", "credentials", "credentials.json", "secrets.json", "service-account.json", "terraform.tfvars":
		return true
	}
	if ls == ".env" || strings.HasPrefix(ls, ".env.") {
		return true
	}
	for _, p := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
		if strings.HasPrefix(ls, p) {
			return true
		}
	}
	for _, sfx := range []string{".pem", ".pfx", ".p12", ".key", ".keystore", ".jks", ".kdbx"} {
		if strings.HasSuffix(ls, sfx) {
			return true
		}
	}
	return false
}

// ConfinePath resolves requested (worktree-relative, or absolute inside the worktree) to its canonical absolute path and
// the worktree-relative slash path. The path must exist.
func ConfinePath(root, requested string) (abs, rel string, err error) {
	return confine(root, requested, runtime.GOOS == "windows")
}

func confine(root, requested string, windows bool) (string, string, error) {
	if err := lexicalCheck(requested, windows); err != nil {
		return "", "", err
	}
	if root == "" {
		return "", "", errors.New("the run has no worktree")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", fmt.Errorf("worktree is not accessible: %w", err)
	}
	p := requested
	if !filepath.IsAbs(p) {
		p = filepath.Join(rootReal, filepath.FromSlash(p))
	}
	p = filepath.Clean(p)
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A missing path under a symlink that leaves the root must not be distinguishable from one that stays inside.
			if !within(rootReal, p) && !within(rootAbs, p) {
				return "", "", ErrOutsideRoot
			}
			return "", "", fmt.Errorf("%w: %s", os.ErrNotExist, filepath.ToSlash(relOr(rootReal, p, requested)))
		}
		return "", "", err
	}
	if !within(rootReal, real) {
		return "", "", ErrOutsideRoot
	}
	rel, err := filepath.Rel(rootReal, real)
	if err != nil {
		return "", "", ErrOutsideRoot
	}
	if rel == "." {
		return real, ".", nil
	}
	if protectedRel(rel) {
		return "", "", ErrProtectedPath
	}
	return real, filepath.ToSlash(rel), nil
}

func relOr(root, p, fallback string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return fallback
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// openConfined opens a regular file inside the worktree and re-verifies after the open that the path still resolves to the
// opened file and still lies inside the root.
func openConfined(root, requested string) (*os.File, string, error) {
	abs, rel, err := ConfinePath(root, requested)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, "", err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, "", err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, "", fmt.Errorf("%s is not a regular file", rel)
	}
	if abs2, _, err := ConfinePath(root, requested); err != nil || abs2 != abs {
		f.Close()
		return nil, "", ErrOutsideRoot
	}
	if st2, err := os.Stat(abs); err != nil || !os.SameFile(st, st2) {
		f.Close()
		return nil, "", ErrOutsideRoot
	}
	return f, rel, nil
}

const maxScanBytes = 16 << 20

// readLineRange reads lines [start, end] (1-based, inclusive; end<=0 means to the end) of a worktree file, stopping once
// maxBytes of output are collected. It returns the text, the last line included, the total number of lines seen
// (complete only when !capped) and whether output was cut by maxBytes.
func readLineRange(f *os.File, start, end, maxBytes int) (out string, last, total int, cut, capped bool, err error) {
	head := make([]byte, 8192)
	n, _ := io.ReadFull(f, head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return "", 0, 0, false, false, errors.New("binary file: read_range serves text files only")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, false, false, err
	}
	rd := bufio.NewReaderSize(io.LimitReader(f, maxScanBytes+1), 64<<10)
	var b strings.Builder
	lineNo := 0
	read := 0
	for {
		line, rerr := rd.ReadString('\n')
		if line != "" {
			lineNo++
			read += len(line)
			if read > maxScanBytes {
				capped = true
				lineNo--
				break
			}
			if lineNo >= start && (end <= 0 || lineNo <= end) && !cut {
				if b.Len()+len(line) > maxBytes {
					cut = true
					if b.Len() == 0 { // a single line longer than the budget: return its head rather than nothing
						b.WriteString(truncateUTF8(line, maxBytes))
						last = lineNo
					}
				} else {
					b.WriteString(line)
					last = lineNo
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return b.String(), last, lineNo, cut, capped, nil
}
