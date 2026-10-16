package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/runfiles"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// The diff viewer (docs/ui.md 6.1): the files a task changed and both versions of each, for a side-by-side editor. A recorded
// diff is the one Finalize wrote with its evidence; a live diff is the worktree as it is now, while an agent works. Both
// versions come from git (the base commit and the task commit, or the file on disk for a live diff); a recorded diff whose
// commits git no longer has falls back to the hunks in the patch, marked partial.

// diffViewLimit is the largest side shown without the human asking for it; diffViewHardLimit is never exceeded.
const (
	diffViewLimit     = 2 << 20
	diffViewHardLimit = 20 << 20
)

func init() {
	RegisterExtension(Extension{Name: "diffview", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"task.diffFiles": bind(func(ctx context.Context, p diffParams) (any, error) { return c.TaskDiffFiles(ctx, p.ID, p.Live) }),
			"task.diffFile": bind(func(ctx context.Context, p diffParams) (any, error) {
				return c.TaskDiffFile(ctx, p.ID, p.Path, p.Live, p.Force)
			}),
		}, nil, nil
	}})
}

type diffParams struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Live  bool   `json:"live"`
	Force bool   `json:"force"`
}

// DiffFiles is the file list of a task's diff.
type DiffFiles struct {
	// Source is "recorded" (the diff of the last finished attempt), "live" (the worktree now) or "none".
	Source string     `json:"source"`
	Base   string     `json:"base,omitempty"`
	Head   string     `json:"head,omitempty"`
	Files  []DiffFile `json:"files"`
	// LiveAvailable says the task still has a worktree, so a live diff can be asked for.
	LiveAvailable bool `json:"liveAvailable"`
}

// DiffFileContent is both versions of one file. Partial means only the changed regions are known (the commits are gone);
// TooLarge means a side is over the display limit and was left out until the human asks for it.
type DiffFileContent struct {
	Path     string `json:"path"`
	OldPath  string `json:"oldPath,omitempty"`
	Status   string `json:"status"`
	Original string `json:"original"`
	Modified string `json:"modified"`
	Binary   bool   `json:"binary,omitempty"`
	TooLarge bool   `json:"tooLarge,omitempty"`
	Partial  bool   `json:"partial,omitempty"`
	Size     int    `json:"size,omitempty"`
}

// diffSource is everything needed to read one diff: the patch and the two sides.
type diffSource struct {
	kind     string // recorded | live
	patch    string
	base     string
	head     string // commit, or "" for a live diff (the worktree on disk)
	worktree string
	repo     *git.Repo
}

func (c *Core) diffSource(ctx context.Context, id string, live bool) (diffSource, tasks.Task, error) {
	t, err := c.Tasks.Get(ctx, id)
	if err != nil {
		return diffSource{}, t, err
	}
	repo, err := c.repo(ctx, t.ProjectID)
	if err != nil {
		return diffSource{}, t, err
	}
	if live {
		if !worktreeExists(t) {
			return diffSource{}, t, errors.New("this task has no worktree any more; its recorded diff is shown instead")
		}
		w := git.Worktree{Path: t.Worktree, Branch: t.Branch, Base: t.BaseBranch, TaskID: t.ID}
		patch, err := repo.Diff(ctx, w, t.BaseBranch)
		if err != nil {
			return diffSource{}, t, err
		}
		base, _ := repo.MergeBase(ctx, w, t.BaseBranch)
		return diffSource{kind: "live", patch: patch, base: base, worktree: t.Worktree, repo: repo}, t, nil
	}
	ev, err := c.Tasks.Evidence(ctx, id)
	if err != nil {
		return diffSource{}, t, err
	}
	src := diffSource{kind: "recorded", repo: repo}
	var commitRef string
	for i := len(ev) - 1; i >= 0; i-- {
		switch ev[i].Kind {
		case "diff":
			if src.patch == "" && ev[i].Ref != "" {
				b, err := runfiles.ReadConfined(c.cfg.DataDir, ev[i].Ref)
				if err != nil {
					return diffSource{}, t, fmt.Errorf("the recorded diff cannot be read: %w", err)
				}
				src.patch = string(b)
				var d struct {
					Base   string `json:"base"`
					Commit string `json:"commit"`
				}
				_ = json.Unmarshal(ev[i].Data, &d)
				src.base, src.head = d.Base, d.Commit
			}
		case "commit":
			if commitRef == "" {
				commitRef = ev[i].Ref
			}
		}
	}
	if src.patch == "" {
		return diffSource{kind: "none"}, t, nil
	}
	if src.head == "" {
		src.head = commitRef // evidence recorded before the diff kept its commit
	}
	if src.base == "" && src.head != "" && t.BaseBranch != "" {
		src.base, _ = repo.MergeBaseOf(ctx, src.head, t.BaseBranch)
	}
	return src, t, nil
}

func worktreeExists(t tasks.Task) bool {
	if t.Worktree == "" {
		return false
	}
	st, err := os.Stat(t.Worktree)
	return err == nil && st.IsDir()
}

// TaskDiffFiles lists the files of a task's diff.
func (c *Core) TaskDiffFiles(ctx context.Context, id string, live bool) (DiffFiles, error) {
	src, t, err := c.diffSource(ctx, id, live)
	if err != nil {
		return DiffFiles{}, err
	}
	out := DiffFiles{Source: src.kind, Base: src.base, Head: src.head, Files: []DiffFile{}, LiveAvailable: worktreeExists(t)}
	if src.kind == "none" {
		return out, nil
	}
	out.Files = append(out.Files, parseUnifiedDiff(src.patch)...)
	return out, nil
}

// TaskDiffFile returns both versions of one file of a task's diff.
func (c *Core) TaskDiffFile(ctx context.Context, id, path string, live, force bool) (DiffFileContent, error) {
	src, _, err := c.diffSource(ctx, id, live)
	if err != nil {
		return DiffFileContent{}, err
	}
	var f *DiffFile
	files := parseUnifiedDiff(src.patch)
	for i := range files {
		if files[i].Path == path {
			f = &files[i]
			break
		}
	}
	if f == nil {
		return DiffFileContent{}, fmt.Errorf("%s is not in this task's diff", path)
	}
	out := DiffFileContent{Path: f.Path, OldPath: f.OldPath, Status: f.Status, Binary: f.Binary}
	if f.Binary {
		return out, nil
	}
	oldPath := f.Path
	if f.OldPath != "" {
		oldPath = f.OldPath
	}
	original, okO, errO := "", false, error(nil)
	if f.Status != "added" {
		original, okO, errO = src.repo.ShowFile(ctx, src.base, oldPath)
	}
	modified, okM, errM := "", false, error(nil)
	if f.Status != "deleted" {
		if src.kind == "live" {
			modified, okM, errM = readInside(src.worktree, f.Path)
		} else {
			modified, okM, errM = src.repo.ShowFile(ctx, src.head, f.Path)
		}
	}
	gone := errors.Is(errO, git.ErrUnknownRevision) || errors.Is(errM, git.ErrUnknownRevision) ||
		(f.Status != "added" && !okO && errO == nil) || (f.Status != "deleted" && !okM && errM == nil)
	if gone {
		// the commits were collected (or never recorded): show what the patch itself holds
		out.Original, out.Modified = fromHunks(f.hunks)
		out.Partial = true
		return out, nil
	}
	if errO != nil {
		return DiffFileContent{}, errO
	}
	if errM != nil {
		return DiffFileContent{}, errM
	}
	out.Size = max(len(original), len(modified))
	if strings.ContainsRune(original, 0) || strings.ContainsRune(modified, 0) {
		out.Binary = true
		return out, nil
	}
	if out.Size > diffViewHardLimit || (out.Size > diffViewLimit && !force) {
		out.TooLarge = true
		return out, nil
	}
	out.Original, out.Modified = original, modified
	return out, nil
}

// readInside reads rel from a worktree, refusing anything that resolves outside it (.., an absolute path, a symlink).
func readInside(root, rel string) (string, bool, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(filepath.Clean(rel), "..") {
		return "", false, fmt.Errorf("%s is not a path inside the worktree", rel)
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(full)
		return "symlink to " + target + "\n", true, nil
	}
	if !st.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s is not a regular file", rel)
	}
	if st.Size() > diffViewHardLimit {
		return strings.Repeat(" ", diffViewHardLimit+1), true, nil // reported as too large without reading it
	}
	b, err := os.ReadFile(full)
	return string(b), true, err
}
