//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/kjmaguire/drydock/internal/core"
)

// openPath opens a task's worktree or a project's repository, or a file in it, in Explorer or a code editor (effort review X26).
// The core has resolved and confined the path and found the editor on PATH (core.OpenPath); here only Explorer from the Windows
// folder or that editor is started, with the path as its one argument. Either breaks away from the app's kill-on-close job: an
// editor the person opened stays open when Drydock closes.
func openPath(_ context.Context, p core.PathOpen) error {
	var cmd *exec.Cmd
	switch p.With {
	case "explorer":
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		exe := filepath.Join(root, "explorer.exe")
		cmd = exec.Command(exe)
		// Explorer reads its own command line: "/select," and the quoted path, as core.ExplorerCommandLine writes it
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: core.ExplorerCommandLine(p.Path, p.File), CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
	case "editor":
		if p.Editor == "" {
			return fmt.Errorf("no editor was named")
		}
		cmd = exec.Command(p.Editor, p.Path)
		flags := uint32(windows.CREATE_BREAKAWAY_FROM_JOB)
		if ext := strings.ToLower(filepath.Ext(p.Editor)); ext == ".cmd" || ext == ".bat" {
			flags |= windows.CREATE_NO_WINDOW // the command's console, not the editor's window (core.EditorArgSafe checked the path)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	default:
		return fmt.Errorf("open with Explorer or an editor")
	}
	cmd.Dir = filepath.Dir(p.Path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start it: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
