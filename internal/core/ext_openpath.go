package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Opening a task's worktree or a project's repository outside Drydock (effort review X26): "Reveal in Explorer" and "Open in your
// editor" beside the task page's "Open the worktree" and on a changed file of the Diff viewer. The page names only a task or a
// project and, for a file, a path relative to its folder; the core resolves the folder as a terminal does (terminalDir), keeps the
// file inside it (no "..", no absolute path, no symbolic link), and hands the shell (Config.OpenPath) the folder or the file with a
// fixed program: Explorer, or an editor from the allowlist below found on PATH. No program or path the page sends is run, and
// nothing of the path goes into an event or a log.

// PathOpen is what the shell is asked to open (Config.OpenPath).
type PathOpen struct {
	// Path is the folder, or a file inside it, as an absolute path the core resolved.
	Path string
	// File says Path is a file (Explorer opens its folder with the file selected; an editor opens the file).
	File bool
	// With is "explorer" or "editor".
	With string
	// Editor is the editor's program, found on PATH (With "editor").
	Editor string
}

// Editor is a code editor Drydock can hand a folder or a file to.
type Editor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Exe is where it was found on PATH; never sent to the page.
	Exe string `json:"-"`
}

// knownEditors is the allowlist, in the order they are offered: the command each puts on PATH.
var knownEditors = []struct{ id, name, command string }{
	{"vscode", "Visual Studio Code", "code"},
	{"cursor", "Cursor", "cursor"},
	{"windsurf", "Windsurf", "windsurf"},
	{"zed", "Zed", "zed"},
	{"sublime", "Sublime Text", "subl"},
	{"notepadpp", "Notepad++", "notepad++"},
}

// FindEditors returns the editors of the allowlist that look finds (exec.LookPath in the product).
func FindEditors(look func(string) (string, error)) []Editor {
	out := []Editor{}
	for _, e := range knownEditors {
		if p, err := look(e.command); err == nil && p != "" {
			out = append(out, Editor{ID: e.id, Name: e.name, Exe: p})
		}
	}
	return out
}

// cmdMeta are the characters cmd.exe reads in a batch file's arguments: a path holding one is not handed to an editor whose command
// is a .cmd or .bat (Windows runs those through cmd.exe, which would read them).
const cmdMeta = "&|<>^%!\"()"

// EditorArgSafe says whether path can be handed to the editor program exe as an argument.
func EditorArgSafe(exe, path string) bool {
	switch strings.ToLower(filepath.Ext(exe)) {
	case ".cmd", ".bat":
		return !strings.ContainsAny(path, cmdMeta)
	}
	return true
}

// ExplorerCommandLine is the command line Explorer is started with (the desktop app sets it as is: Explorer reads its own
// arguments, and "/select," must be followed by the quoted path). A Windows path cannot hold a double quote.
func ExplorerCommandLine(path string, file bool) string {
	if file {
		return `explorer.exe /select,"` + path + `"`
	}
	return `explorer.exe "` + path + `"`
}

// OpenPathReq is shell.openPath's request: a task or a project, a path inside its folder (optional) and what to open it with.
type OpenPathReq struct {
	TaskID    string `json:"taskId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	Path      string `json:"path,omitempty"`
	With      string `json:"with"`
	Editor    string `json:"editor,omitempty"`
}

// EditorList is shell.editors: whether this window can open a folder outside Drydock, and the editors found.
type EditorList struct {
	Available bool     `json:"available"`
	Editors   []Editor `json:"editors"`
}

func (c *Core) lookPath() func(string) (string, error) {
	if c.cfg.LookPath != nil {
		return c.cfg.LookPath
	}
	return exec.LookPath
}

// Editors lists the editors this computer has (none without a shell to start them).
func (c *Core) Editors() EditorList {
	if c.cfg.OpenPath == nil {
		return EditorList{Editors: []Editor{}}
	}
	return EditorList{Available: true, Editors: FindEditors(c.lookPath())}
}

// OpenPath opens a task's worktree or a project's repository, or a file in it, in Explorer or an editor. Only a person may.
func (c *Core) OpenPath(ctx context.Context, r OpenPathReq, by string) error {
	if c.cfg.OpenPath == nil {
		return errors.New("this window cannot open Explorer or an editor: copy the path instead")
	}
	if !isHuman(by) {
		return errors.New("only a person may open a folder outside Drydock")
	}
	if r.With != "explorer" && r.With != "editor" {
		return errors.New("open with Explorer or an editor")
	}
	dir, _, owner, err := c.terminalDir(ctx, r.ProjectID, r.TaskID)
	if err != nil {
		return err
	}
	if r.TaskID != "" && owner.TaskID == "" && r.Path != "" {
		return errors.New("the task has no worktree any more")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return errors.New("the folder is not there any more")
	}
	open := PathOpen{Path: dir, With: r.With}
	if r.Path != "" {
		full, err := insidePath(dir, r.Path)
		if err != nil {
			return err
		}
		st, err := os.Lstat(full)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return errors.New("that file is not in the folder any more")
		case err != nil:
			return err
		case st.Mode()&os.ModeSymlink != 0:
			return errors.New("a symbolic link is not opened outside Drydock")
		case !st.Mode().IsRegular() && !st.IsDir():
			return errors.New("only a file or a folder can be opened")
		}
		// a folder on the way that is a link to somewhere else is not inside the folder either
		if !resolvesInside(dir, full) {
			return errors.New("that path is not inside the folder")
		}
		open.Path, open.File = full, !st.IsDir()
	}
	if r.With == "editor" {
		var found *Editor
		for _, e := range FindEditors(c.lookPath()) {
			if e.ID == r.Editor {
				found = &e
				break
			}
		}
		if found == nil {
			return errors.New("that editor was not found on this computer")
		}
		if !EditorArgSafe(found.Exe, open.Path) {
			return fmt.Errorf("this path has characters %s cannot be handed safely: copy the path instead", found.Name)
		}
		open.Editor = found.Exe
	}
	return c.cfg.OpenPath(ctx, open)
}

// resolvesInside says whether full, with every link on its way followed, is still inside dir (with its own links followed).
func resolvesInside(dir, full string) bool {
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	f, err := filepath.EvalSymlinks(full)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(d, f)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func init() {
	RegisterExtension(Extension{Name: "openpath", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"shell.editors": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.Editors(), nil }),
			"shell.openPath": bind(func(ctx context.Context, p OpenPathReq) (any, error) {
				if err := c.OpenPath(ctx, p, c.Human()); err != nil {
					return nil, err
				}
				return map[string]bool{"opened": true}, nil
			}),
		}, nil, nil
	}})
}
