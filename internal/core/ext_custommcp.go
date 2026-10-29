package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/git"
)

// placeCustomMCPFile writes a run's MCP configuration where a person's own CLI reads it from its working folder (its manifest's
// mcp.file, decision P117) and returns what removes it again when the run ends. The file holds the run's gateway token, so:
// a file already there is never overwritten (it is the person's or the repository's, not Drydock's); in a git checkout the path
// is excluded locally before anything is written, so a commit made during the turn does not take the token along; the folders
// it lives in must not lead outside the working folder; and it is written for the owner only. Folders Drydock had to create
// are removed with the file when they are empty.
func placeCustomMCPFile(ctx context.Context, cwd, rel string, data []byte) (remove func(), err error) {
	if strings.TrimSpace(cwd) == "" {
		return nil, errors.New("this session has no working folder to put the MCP configuration in")
	}
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, fmt.Errorf("the working folder could not be read: %s", scrubPath(err))
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("%s is already in the working folder, and Drydock does not overwrite a file it did not write: the CLI runs without the gateway", rel)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("could not check %s: %s", rel, scrubPath(err))
	}
	// the folders on the way, created where missing (the outermost one first, so they can be removed innermost first)
	var made []string
	for d := filepath.Dir(target); d != root && strings.HasPrefix(d, root+string(filepath.Separator)); d = filepath.Dir(d) {
		if _, err := os.Lstat(d); errors.Is(err, fs.ErrNotExist) {
			made = append([]string{d}, made...)
		}
	}
	undo := func() {
		for i := len(made) - 1; i >= 0; i-- {
			_ = os.Remove(made[i]) // only an empty folder goes
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		undo()
		return nil, fmt.Errorf("could not make the folder for %s: %s", rel, scrubPath(err))
	}
	// a folder on the way that is a link elsewhere would put the token outside the working folder
	if parent, err := filepath.EvalSymlinks(filepath.Dir(target)); err != nil || (parent != root && !strings.HasPrefix(parent, root+string(filepath.Separator))) {
		undo()
		return nil, fmt.Errorf("%s leads outside the working folder: the CLI runs without the gateway", rel)
	}
	if repo, err := git.Open(ctx, root); err == nil {
		inRepo, rerr := filepath.Rel(repo.Root, target)
		if rerr != nil || strings.HasPrefix(inRepo, "..") {
			undo()
			return nil, fmt.Errorf("%s is outside the repository it would be excluded from", rel)
		}
		if err := repo.LocalIgnore(ctx, git.Worktree{Path: repo.Root}, []string{filepath.ToSlash(inRepo)}); err != nil {
			undo()
			return nil, fmt.Errorf("could not keep %s out of git, so it was not written: %v", rel, err)
		}
	} else if !errors.Is(err, git.ErrNotARepo) {
		undo()
		return nil, fmt.Errorf("could not tell whether the working folder is a git checkout: %v", err)
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		undo()
		return nil, fmt.Errorf("could not write %s: %s", rel, scrubPath(err))
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(target)
		undo()
		return nil, fmt.Errorf("could not write %s: %s", rel, scrubPath(werr))
	}
	return func() {
		_ = os.Remove(target)
		undo()
	}, nil
}
