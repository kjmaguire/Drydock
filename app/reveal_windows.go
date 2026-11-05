//go:build windows

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// reveal opens File Explorer with path selected. The core passes only a file it wrote this run (core.RevealFile).
func reveal(_ context.Context, path string) error {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	explorer := filepath.Join(root, "explorer.exe")
	line, err := revealCmdLine(explorer, path)
	if err != nil {
		return err
	}
	cmd := exec.Command(explorer)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // explorer hands the window to the running shell and exits (often with code 1)
	return nil
}
