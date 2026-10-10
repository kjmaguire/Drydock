package acp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Limits for client-side file access.
const (
	MaxReadBytes  = 8 << 20
	MaxWriteBytes = 16 << 20
)

// ErrOutsideCwd is returned for any path that resolves outside the session working directory.
var ErrOutsideCwd = errors.New("path is outside the session working directory")

// guard confines the client-side fs and terminal methods to the session cwd. This is a security boundary: the agent is
// untrusted input. Containment is enforced twice: lexically (after Clean, so ../ cannot climb out) and by the OS through
// os.Root, which refuses to follow a symlink or junction that leaves the root and has no check-then-open race.
type guard struct {
	root string // absolute, lexical working directory as given
	real string // root with symlinks resolved
	r    *os.Root
}

func newGuard(cwd string) (*guard, error) {
	if cwd == "" {
		return nil, errors.New("acp: session cwd is empty")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("acp: session cwd: %w", err)
	}
	r, err := os.OpenRoot(real)
	if err != nil {
		return nil, fmt.Errorf("acp: open session cwd: %w", err)
	}
	return &guard{root: abs, real: real, r: r}, nil
}

func (g *guard) close() { _ = g.r.Close() }

func within(base, p string) (string, bool) {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// rel maps a client-supplied path (absolute, or relative to the cwd) to a path relative to the root. It is a lexical
// check only; the OS-level check happens when the file is opened through g.r.
func (g *guard) rel(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: invalid path", ErrOutsideCwd)
	}
	if !filepath.IsAbs(p) {
		// rooted-but-not-absolute (\x on Windows) and drive-relative (C:x) forms are ambiguous: refuse them
		if filepath.VolumeName(p) != "" || os.IsPathSeparator(p[0]) {
			return "", fmt.Errorf("%w: %q", ErrOutsideCwd, p)
		}
		p = filepath.Join(g.root, p)
	}
	p = filepath.Clean(p)
	candidates := []struct{ base, path string }{{g.root, p}, {g.real, p}}
	// the path may name the working folder in another form than the session's: on Windows a short 8.3 name (RUNNER~1 for
	// runneradmin) or another symlink to it. Its resolved form is compared too; the open still goes through g.r.
	if r, err := resolveExisting(p); err == nil {
		candidates = append(candidates, struct{ base, path string }{g.real, r})
	}
	for _, c := range candidates {
		if rel, ok := within(c.base, c.path); ok {
			if runtime.GOOS == "windows" && strings.Contains(rel, ":") {
				return "", fmt.Errorf("%w: alternate data streams are not allowed", ErrOutsideCwd)
			}
			return rel, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrOutsideCwd, p)
}

// resolveExisting resolves the symlinks (and, on Windows, the short 8.3 names) of the longest part of p that exists, and
// joins the rest back on: a file that does not exist yet still resolves through its folder.
func resolveExisting(p string) (string, error) {
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest), nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", errors.New("acp: no part of the path exists")
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// readFile reads a text file (at most MaxReadBytes), returning lines [line, line+limit) when line (1-based) or limit is set.
func (g *guard) readFile(p string, line, limit *int) (string, error) {
	rel, err := g.rel(p)
	if err != nil {
		return "", err
	}
	f, err := g.r.Open(rel)
	if err != nil {
		return "", mapRootErr(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %q", p)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxReadBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > MaxReadBytes {
		return "", fmt.Errorf("file is larger than %d bytes", MaxReadBytes)
	}
	s := string(b)
	if line == nil && limit == nil {
		return s, nil
	}
	lines := strings.SplitAfter(s, "\n")
	start := 0
	if line != nil && *line > 1 {
		start = *line - 1
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := len(lines)
	if limit != nil && *limit >= 0 && start+*limit < end {
		end = start + *limit
	}
	return strings.Join(lines[start:end], ""), nil
}

// writeFile writes content to p, creating parent directories. Writes into .git are refused (hooks and config there run code).
func (g *guard) writeFile(p, content string) error {
	if len(content) > MaxWriteBytes {
		return fmt.Errorf("content is larger than %d bytes", MaxWriteBytes)
	}
	rel, err := g.rel(p)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("not a file path: %q", p)
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") {
			return fmt.Errorf("%w: writes to .git are not allowed", ErrOutsideCwd)
		}
	}
	if dir := filepath.Dir(rel); dir != "." {
		if err := g.r.MkdirAll(dir, 0o755); err != nil {
			return mapRootErr(err)
		}
	}
	return mapRootErr(g.r.WriteFile(rel, []byte(content), 0o644))
}

// dir resolves a working directory for a terminal: it must exist, be a directory and lie inside the root.
func (g *guard) dir(p string) (string, error) {
	if p == "" {
		return g.real, nil
	}
	rel, err := g.rel(p)
	if err != nil {
		return "", err
	}
	st, err := g.r.Stat(rel)
	if err != nil {
		return "", mapRootErr(err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("not a directory: %q", p)
	}
	return filepath.Join(g.real, rel), nil
}

// mapRootErr turns os.Root's "path escapes from parent" into ErrOutsideCwd so callers see one error for every escape.
func mapRootErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "escapes from parent") {
		return fmt.Errorf("%w: %v", ErrOutsideCwd, err)
	}
	return err
}
