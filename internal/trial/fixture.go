package trial

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Prepare makes dir a fresh Git repository holding a copy of the task's fixture as one commit on main, and returns that
// commit: the start commit both arms get.
func Prepare(ctx context.Context, root string, t Task, dir string) (string, error) {
	if err := copyTree(filepath.Join(root, t.Fixture), dir, nil); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "Start of trial task " + t.ID}} {
		if _, err := git(ctx, dir, args...); err != nil {
			return "", err
		}
	}
	return git(ctx, dir, "rev-parse", "HEAD")
}

// commitAll commits whatever changed in dir (used to add the Drydock arm's sandbox definition).
func commitAll(ctx context.Context, dir, msg string) error {
	if _, err := git(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	_, err := git(ctx, dir, "commit", "-q", "-m", msg)
	return err
}

// git runs git in dir with a fixed identity and no user or system configuration, so the machine's settings (hooks, signing,
// a global ignore file) do not change what the arms start from.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Drydock Trial", "GIT_AUTHOR_EMAIL=trial@drydock.invalid", "GIT_COMMITTER_NAME=Drydock Trial", "GIT_COMMITTER_EMAIL=trial@drydock.invalid")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// ResultDiff is what an arm changed: the task's fixture against tree (without .git and .drydock), new files included, read
// from copies so the arm's repository and index are not touched.
func ResultDiff(ctx context.Context, root string, t Task, tree string) (string, error) {
	dir, err := os.MkdirTemp("", "drydock-diff-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if err := copyTree(filepath.Join(root, t.Fixture), filepath.Join(dir, "start"), nil); err != nil {
		return "", err
	}
	if err := copyTree(tree, filepath.Join(dir, "result"), map[string]bool{".drydock": true}); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--no-color", "--no-ext-diff", "start", "result")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) { // 1 means "they differ"
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

// copyTree copies the regular files and directories of src into dst (created), skipping .git and anything skip names.
func copyTree(src, dst string, skip map[string]bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel != "." && (d.Name() == ".git" || skip[filepath.ToSlash(rel)]) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyFile(p, target, info.Mode().Perm())
		}
		return nil // symlinks and devices are not part of a fixture or a result
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
