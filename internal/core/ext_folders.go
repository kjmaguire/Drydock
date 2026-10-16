package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/runners"
)

// A project's folder (decision P58): the window's own folder dialog ("Choose folder..."), what is at a folder a person typed
// or chose (repo.check), and making a Git repository there when there is none (repo.init): the folder is made when it does
// not exist, `git init` names the branch main, and a first commit holds what is in the folder, so task worktrees can be made
// from it. A folder that is already a repository is never touched, and a folder inside another repository's work tree is
// refused. Every git call goes through Drydock's chokepoint (internal/git: no hooks, the scrubbed environment, the distro's
// own git for a folder inside a WSL distro).

func init() {
	RegisterExtension(Extension{Name: "folders", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		x := &foldersExt{c: c}
		return map[string]Method{
			"fs.pickFolder": bind(func(ctx context.Context, p struct {
				Title string `json:"title"`
			}) (any, error) {
				path, err := c.PickFolder(ctx, p.Title, c.Human())
				if err != nil {
					return nil, err
				}
				return map[string]string{"path": path}, nil
			}),
			"fs.folderHints": bind(func(ctx context.Context, _ struct{}) (any, error) { return x.hints(ctx), nil }),
			"repo.check": bind(func(ctx context.Context, p struct {
				Path string `json:"path"`
			}) (any, error) {
				return c.CheckRepoFolder(ctx, p.Path), nil
			}),
			"repo.init": bind(func(ctx context.Context, p struct {
				Path string `json:"path"`
			}) (any, error) {
				return c.InitRepository(ctx, p.Path, c.Human())
			}),
		}, nil, nil
	}})
}

type foldersExt struct {
	c *Core
}

// FolderHints is what the project folder's field says beside the path: whether the window has a folder dialog, whether this
// is Windows (where a Dev Drive is recommended), the Dev Drives found, and when none was found on Windows, why.
type FolderHints struct {
	CanPick        bool            `json:"canPick"`
	Windows        bool            `json:"windows"`
	DevDrives      []runners.Drive `json:"devDrives"`
	DevDriveReason string          `json:"devDriveReason,omitempty"`
}

func (x *foldersExt) hints(ctx context.Context) FolderHints {
	dev := x.c.devDrives(ctx)
	h := FolderHints{CanPick: x.c.cfg.PickFolder != nil, Windows: runtime.GOOS == "windows", DevDrives: dev.Drives, DevDriveReason: dev.Reason}
	if h.DevDrives == nil {
		h.DevDrives = []runners.Drive{}
	}
	return h
}

// devDrives is this PC's Dev Drives, asked once (Config.DevDrives when set, else the volumes themselves on Windows, nothing
// elsewhere): the project folder's hint and the CI runners' Windows folder share the answer.
func (c *Core) devDrives(ctx context.Context) runners.DevDriveScan {
	c.devOnce.Do(func() {
		switch {
		case c.cfg.DevDrives != nil:
			c.dev = c.cfg.DevDrives(ctx)
		case runtime.GOOS == "windows":
			c.dev = runners.DevDrives(ctx)
		}
	})
	return c.dev
}

// isHuman reports whether by names a person ("human:<name>").
func isHuman(by string) bool { return strings.HasPrefix(by, "human:") && len(by) > len("human:") }

// PickFolder opens the window's folder dialog and returns the folder the person chose ("" when they cancelled). Only a person
// may; without a dialog (the browser harness without its stand-in, a build without a window) it says to type the path.
func (c *Core) PickFolder(ctx context.Context, title, by string) (string, error) {
	if c.cfg.PickFolder == nil {
		return "", errors.New("this window cannot open a folder dialog: type the folder's full path instead")
	}
	if !isHuman(by) {
		return "", errors.New("only a person may choose a folder")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Choose the project's folder"
	}
	if len(title) > 200 {
		title = title[:200]
	}
	path, err := c.cfg.PickFolder(ctx, FolderPick{Title: title})
	if err != nil {
		return "", fmt.Errorf("the folder dialog: %w", err)
	}
	return strings.TrimSpace(path), nil
}

// Repository folder states (RepoFolder.State).
const (
	FolderRepository = "repository" // a Git repository with a checked-out branch: a project can use it as it is
	FolderNoBranch   = "no_branch"  // a Git repository without a commit on a branch: commit once first (Drydock does not)
	FolderInside     = "inside"     // inside another repository's work tree: refused
	FolderPlain      = "folder"     // a folder that is not a repository: Drydock can make one here
	FolderMissing    = "missing"    // nothing there yet: Drydock can make the folder and the repository
	FolderNotFolder  = "not_folder" // a file
	FolderInvalid    = "invalid"    // not a full path, a drive that is not there, a folder git cannot read
)

// RepoFolder is what is at a folder a person gave for a project's repository.
type RepoFolder struct {
	Path    string `json:"path"`
	State   string `json:"state"`
	Root    string `json:"root,omitempty"`   // repository, no_branch, inside: the work tree's top folder
	Branch  string `json:"branch,omitempty"` // repository: the checked-out branch
	Entries int    `json:"entries"`          // folder: the files and folders at its top
	CanInit bool   `json:"canInit"`          // Drydock may make a repository here (folder, missing)
	Message string `json:"message"`
}

// cleanFolderArg trims a path a person typed or pasted (spaces, surrounding quotes from "Copy as path").
func cleanFolderArg(path string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(path), `"`))
}

// samePath reports whether a and b name one folder (symbolic links followed; letter case ignored on Windows).
func samePath(a, b string) bool {
	norm := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return p
	}
	x, y := norm(a), norm(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(x, y)
	}
	return x == y
}

// CheckRepoFolder says what is at path for a project's repository. It reads only: nothing is made or changed.
func (c *Core) CheckRepoFolder(ctx context.Context, path string) RepoFolder {
	path = cleanFolderArg(path)
	f := RepoFolder{Path: path, State: FolderInvalid}
	if path == "" {
		f.Message = "Enter the folder's full path."
		return f
	}
	if err := needFullPath("the folder", path); err != nil {
		f.Message = err.Error()
		return f
	}
	if strings.ContainsAny(path, "\x00\r\n") {
		f.Message = "the folder path contains a control character"
		return f
	}
	run, err := c.repoRunner(ctx, path)
	if err != nil {
		f.Message = err.Error()
		return f
	}
	inside := func(top string) RepoFolder {
		f.State, f.Root = FolderInside, top
		f.Message = fmt.Sprintf("%s is inside the Git repository at %s. A project's repository cannot be inside another repository's work tree: choose a folder outside it, or %s itself.", path, top, top)
		return f
	}
	st, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		parent := filepath.Dir(path)
		for {
			if pst, perr := os.Stat(parent); perr == nil && pst.IsDir() {
				break
			}
			up := filepath.Dir(parent)
			if up == parent {
				f.Message = fmt.Sprintf("%s cannot be made: no folder above it exists (is the drive there?)", path)
				return f
			}
			parent = up
		}
		top, gerr := git.WorkTreeOf(ctx, parent, run)
		if gerr != nil {
			f.Message = fmt.Sprintf("%s cannot be checked: %v", parent, gerr)
			return f
		}
		if top != "" {
			return inside(top)
		}
		f.State, f.CanInit = FolderMissing, true
		f.Message = "This folder does not exist yet. Drydock can make it with a new Git repository (branch main)."
		return f
	case err != nil:
		f.Message = fmt.Sprintf("%s cannot be read: %v", path, err)
		return f
	case !st.IsDir():
		f.State, f.Message = FolderNotFolder, fmt.Sprintf("%s is a file, not a folder.", path)
		return f
	}
	top, err := git.WorkTreeOf(ctx, path, run)
	if err != nil {
		f.Message = fmt.Sprintf("%s cannot be checked: %v", path, err)
		return f
	}
	if top == "" {
		ents, _ := os.ReadDir(path)
		f.State, f.CanInit, f.Entries = FolderPlain, true, len(ents)
		if len(ents) == 0 {
			f.Message = "This folder is not a Git repository. Drydock can make one here (branch main, with an empty first commit)."
		} else {
			f.Message = fmt.Sprintf("This folder is not a Git repository. Drydock can make one here (branch main); its first commit holds the %d %s in the folder (what a .gitignore leaves out stays out).", len(ents), plural(len(ents), "item", "items"))
		}
		return f
	}
	if !samePath(top, path) {
		return inside(top)
	}
	f.Root = top
	repo, err := c.openRepo(ctx, path)
	if err != nil {
		f.Message = fmt.Sprintf("%s is a Git repository Drydock cannot open: %v", path, err)
		return f
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil || branch == "" || branch == "HEAD" {
		f.State, f.Message = FolderNoBranch, "This is a Git repository without a commit on a branch: commit once on a branch first (Drydock leaves an existing repository as it is)."
		return f
	}
	f.State, f.Branch, f.Root = FolderRepository, branch, repo.Root
	f.Message = fmt.Sprintf("A Git repository on the branch %s.", branch)
	return f
}

// RepoInit is what repo.init made.
type RepoInit struct {
	Path          string `json:"path"`
	Branch        string `json:"branch"`
	CreatedFolder bool   `json:"createdFolder"`
	Files         int    `json:"files"` // in the first commit
}

// InitBranch is the branch a repository Drydock makes starts on.
const InitBranch = "main"

// InitRepository makes a Git repository at path for a new project: the folder when it does not exist, `git init -b main`
// and a first commit of what is in it. Only a person may. A folder that is already a repository (with or without a commit)
// is left as it is, and one inside another repository's work tree is refused, both with the reason.
func (c *Core) InitRepository(ctx context.Context, path, by string) (RepoInit, error) {
	if !isHuman(by) {
		return RepoInit{}, errors.New("only a person may make a Git repository")
	}
	if err := c.RequireLicensed(ctx, "create_project"); err != nil {
		return RepoInit{}, err
	}
	f := c.CheckRepoFolder(ctx, path)
	switch f.State {
	case FolderMissing, FolderPlain:
	case FolderRepository, FolderNoBranch:
		return RepoInit{}, fmt.Errorf("%s is already a Git repository: Drydock leaves it as it is (choose it as the project's repository)", f.Path)
	default:
		return RepoInit{}, errors.New(f.Message)
	}
	run, err := c.repoRunner(ctx, f.Path)
	if err != nil {
		return RepoInit{}, err
	}
	res := RepoInit{Path: f.Path, Branch: InitBranch}
	if f.State == FolderMissing {
		if err := os.MkdirAll(f.Path, 0o755); err != nil {
			return RepoInit{}, fmt.Errorf("making the folder %s: %w", f.Path, err)
		}
		res.CreatedFolder = true
	}
	repo, err := git.InitIn(ctx, f.Path, InitBranch, run)
	if err != nil {
		if res.CreatedFolder {
			_ = os.RemoveAll(f.Path) // Drydock made it a moment ago: nothing of the person's is in it
		}
		return RepoInit{}, fmt.Errorf("making the Git repository in %s: %w", f.Path, err)
	}
	if n, err := repo.TrackedFiles(ctx); err == nil {
		res.Files = n
	}
	c.publish(ctx, "repo.created", "", map[string]any{"path": res.Path, "branch": res.Branch, "createdFolder": res.CreatedFolder, "files": res.Files, "by": by})
	return res, nil
}
