package core_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// "Reveal in Explorer" and "Open in your editor" (effort review X26): the shell is handed only a project's folder or a file confined
// inside it, with Explorer or an editor of the allowlist found on PATH, and only for a person.
func TestOpenPathHandsTheShellOnlyAConfinedPathWithAFixedProgram(t *testing.T) {
	var opened []core.PathOpen
	dataDir := filepath.Join(t.TempDir(), "data")
	c, err := core.Open(context.Background(), core.Config{DataDir: dataDir, User: "kyle", LeaseTTL: 3 * time.Second,
		// TEST DOUBLES: the shell that starts Explorer or an editor, and PATH holding only VS Code's command
		OpenPath: func(_ context.Context, p core.PathOpen) error { opened = append(opened, p); return nil },
		LookPath: func(name string) (string, error) {
			if name == "code" {
				return `C:\Program Files\Microsoft VS Code\bin\code.cmd`, nil
			}
			return "", errors.New("not found")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	p, repo := a.setup("gocalc")

	var eds core.EditorList
	a.call("shell.editors", map[string]any{}, &eds)
	if !eds.Available || len(eds.Editors) != 1 || eds.Editors[0].ID != "vscode" || eds.Editors[0].Name != "Visual Studio Code" {
		t.Fatalf("editors %+v", eds)
	}

	var got map[string]bool
	a.call("shell.openPath", map[string]any{"projectId": p.ID, "with": "explorer"}, &got)
	a.call("shell.openPath", map[string]any{"projectId": p.ID, "path": "calc.go", "with": "explorer"}, &got)
	a.call("shell.openPath", map[string]any{"projectId": p.ID, "path": "calc.go", "with": "editor", "editor": "vscode"}, &got)
	want := []core.PathOpen{
		{Path: repo, With: "explorer"},
		{Path: filepath.Join(repo, "calc.go"), File: true, With: "explorer"},
		{Path: filepath.Join(repo, "calc.go"), File: true, With: "editor", Editor: `C:\Program Files\Microsoft VS Code\bin\code.cmd`},
	}
	if len(opened) != 3 || opened[0] != want[0] || opened[1] != want[1] || opened[2] != want[2] {
		t.Fatalf("opened %+v", opened)
	}

	// nothing outside the folder, no link, no program the page names, no editor that is not there
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("x"), 0o644)
	if runtime.GOOS != "windows" {
		os.Symlink(outside, filepath.Join(repo, "link.txt"))
		os.Symlink(filepath.Dir(outside), filepath.Join(repo, "linkdir"))
	}
	os.WriteFile(filepath.Join(repo, "a&b.go"), []byte("x"), 0o644)
	bad := []map[string]any{
		{"projectId": p.ID, "path": "../secret.txt", "with": "explorer"},
		{"projectId": p.ID, "path": outside, "with": "explorer"},
		{"projectId": p.ID, "path": "missing.go", "with": "explorer"},
		{"projectId": p.ID, "path": "calc.go", "with": "cmd.exe"},
		{"projectId": p.ID, "path": "calc.go", "with": "editor", "editor": "cursor"},
		{"projectId": p.ID, "path": "calc.go", "with": "editor", "editor": `C:\Windows\System32\calc.exe`},
		// a batch file's arguments are read by cmd.exe: a path with & in it is not handed to one
		{"projectId": p.ID, "path": "a&b.go", "with": "editor", "editor": "vscode"},
		{"with": "explorer"},
	}
	if runtime.GOOS != "windows" {
		bad = append(bad, map[string]any{"projectId": p.ID, "path": "link.txt", "with": "explorer"},
			map[string]any{"projectId": p.ID, "path": "linkdir/secret.txt", "with": "explorer"})
	}
	for _, b := range bad {
		if err := a.callErr("shell.openPath", b); err == nil {
			t.Errorf("%v was opened", b)
		}
	}
	if len(opened) != 3 {
		t.Fatalf("the shell was asked to open %+v", opened[3:])
	}
	if err := c.OpenPath(context.Background(), core.OpenPathReq{ProjectID: p.ID, With: "explorer"}, "agent:agt_1"); err == nil || len(opened) != 3 {
		t.Fatalf("an agent opened a folder: %v", err)
	}

	// a build without a shell lists no editors and says to copy the path
	b := open(t, "")
	var none core.EditorList
	b.call("shell.editors", map[string]any{}, &none)
	if none.Available || len(none.Editors) != 0 {
		t.Fatalf("without a shell: %+v", none)
	}
	if err := b.callErr("shell.openPath", map[string]any{"projectId": p.ID, "with": "explorer"}); err == nil || !strings.Contains(err.Error(), "copy the path") {
		t.Fatalf("without a shell: %v", err)
	}
}

func TestOpenPathHelpers(t *testing.T) {
	if got := core.ExplorerCommandLine(`C:\code\my app\calc.go`, true); got != `explorer.exe /select,"C:\code\my app\calc.go"` {
		t.Fatalf("select: %s", got)
	}
	if got := core.ExplorerCommandLine(`C:\code\my app`, false); got != `explorer.exe "C:\code\my app"` {
		t.Fatalf("folder: %s", got)
	}
	for path, ok := range map[string]bool{`C:\a\b.go`: true, `C:\a&b.go`: false, `C:\100%.go`: false, `C:\x^y`: false, `C:\(1).go`: false} {
		if core.EditorArgSafe(`C:\bin\code.CMD`, path) != ok {
			t.Errorf("%s through a .cmd: want %v", path, ok)
		}
		if !core.EditorArgSafe(`C:\bin\zed.exe`, path) {
			t.Errorf("%s to an .exe", path)
		}
	}
	found := core.FindEditors(func(n string) (string, error) {
		if n == "zed" || n == "subl" {
			return "/bin/" + n, nil
		}
		return "", errors.New("no")
	})
	if len(found) != 2 || found[0].ID != "zed" || found[1].ID != "sublime" {
		t.Fatalf("found %+v", found)
	}
}

// A task whose worktree folder was removed (after the merge, tasks.worktree still names it) is not opened as the project's main
// repository: shell.openPath refuses it with a path or without, and task.get says the worktree is gone so the page hides the menu.
func TestOpenPathRefusesATaskWhoseWorktreeWasRemoved(t *testing.T) {
	var opened []core.PathOpen
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		// TEST DOUBLES: the shell that starts Explorer or an editor, and PATH holding only VS Code's command
		OpenPath: func(_ context.Context, p core.PathOpen) error { opened = append(opened, p); return nil },
		LookPath: func(name string) (string, error) {
			if name == "code" {
				return `C:\Program Files\Microsoft VS Code\bin\code.cmd`, nil
			}
			return "", errors.New("not found")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	p, _ := a.setup("gocalc")
	var task tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Implement Subtract", Description: "make Subtract return a - b"}, &task)

	// no worktree yet: nothing opens either
	if err := a.callErr("shell.openPath", map[string]any{"taskId": task.ID, "with": "explorer"}); err == nil {
		t.Fatal("a task without a worktree opened the repository")
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt, "calc.go"), []byte("package calc\n"), 0o644)
	if _, err := c.DB.Writer().Exec(`UPDATE tasks SET worktree=? WHERE id=?`, wt, task.ID); err != nil {
		t.Fatal(err)
	}
	var d core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &d)
	if d.WorktreeGone {
		t.Fatal("the worktree is there but task.get says it is gone")
	}
	var got map[string]bool
	a.call("shell.openPath", map[string]any{"taskId": task.ID, "with": "explorer"}, &got)
	if len(opened) != 1 || opened[0] != (core.PathOpen{Path: wt, With: "explorer"}) {
		t.Fatalf("opened %+v", opened)
	}

	// the merge removes the folder; tasks.worktree still names it
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	a.call("task.get", map[string]string{"id": task.ID}, &d)
	if !d.WorktreeGone || d.Task.Worktree != wt {
		t.Fatalf("after the removal: gone %v, worktree %q", d.WorktreeGone, d.Task.Worktree)
	}
	for _, b := range []map[string]any{
		{"taskId": task.ID, "with": "explorer"},
		{"taskId": task.ID, "with": "editor", "editor": "vscode"},
		{"taskId": task.ID, "path": "calc.go", "with": "explorer"},
	} {
		err := a.callErr("shell.openPath", b)
		if err == nil || !strings.Contains(err.Error(), "no worktree any more") {
			t.Errorf("%v: %v", b, err)
		}
	}
	if len(opened) != 1 {
		t.Fatalf("the shell was asked to open %+v", opened[1:])
	}
}
