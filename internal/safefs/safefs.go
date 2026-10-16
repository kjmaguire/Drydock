// Package safefs reads and writes files inside a folder that someone else can write to: an agent's worktree. Every path is
// resolved by os.Root, which refuses ".." and any symbolic link or junction that leads out of the folder and has no
// check-then-open gap, so a repository that commits CLAUDE.md as a link to the user's credentials file (or to the app's database)
// gets nothing read through it, and a generated file is never written through a link to somewhere else (audit 2026-10-16, H1).
package safefs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ErrNotRegular is returned for a path that is a folder, a device, a pipe or anything else but a file.
var ErrNotRegular = errors.New("safefs: not a regular file")

// open opens root for the operations below; the caller closes it.
func open(root string) (*os.Root, error) {
	return os.OpenRoot(root)
}

// ReadFile reads at most max bytes (all of it when max <= 0) of the file rel (slash- or OS-separated) under root. A path that
// leaves root by ".." or through a symbolic link is an error; a link that stays inside root is followed. Anything but a regular
// file is ErrNotRegular, decided on the file that was opened.
func ReadFile(root, rel string, max int64) ([]byte, error) {
	r, err := open(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	rel = filepath.FromSlash(rel)
	// a pipe would block the open itself, so look first; the handle is checked again below
	if st, err := r.Stat(rel); err != nil {
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	f, err := r.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil {
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	var src io.Reader = f
	if max > 0 {
		src = io.LimitReader(f, max)
	}
	return io.ReadAll(src)
}

// Lstat is os.Lstat for rel under root: the entry itself, not what a link points to. A path that leaves root is an error.
func Lstat(root, rel string) (os.FileInfo, error) {
	r, err := open(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return r.Lstat(filepath.FromSlash(rel))
}

// MkdirAll makes rel and its parents under root.
func MkdirAll(root, rel string, perm os.FileMode) error {
	r, err := open(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return r.MkdirAll(filepath.FromSlash(rel), perm)
}

// WriteFile writes data to the file rel under root. A symbolic link at rel is removed first, so the link is replaced by a file
// and nothing it pointed to is written; a path that leaves root through a link is an error. The folder of rel must exist.
func WriteFile(root, rel string, data []byte, perm os.FileMode) error {
	r, err := open(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	rel = filepath.FromSlash(rel)
	if st, err := r.Lstat(rel); err == nil && st.Mode()&os.ModeSymlink != 0 {
		if err := r.Remove(rel); err != nil {
			return err
		}
	}
	f, err := r.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Remove removes the file rel under root (the entry itself, never what a link points to).
func Remove(root, rel string) error {
	r, err := open(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return r.Remove(filepath.FromSlash(rel))
}
