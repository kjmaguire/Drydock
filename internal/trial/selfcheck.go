package trial

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// SolutionsDir holds a reference solution per task, relative to the repository root: <id>.patch, a patch against the
// fixture applied with git apply, or <id>/, files laid over the fixture.
const SolutionsDir = "evals/testdata/solutions"

// SelfCheck proves a task's hidden check can tell a result apart: it must fail on the untouched fixture and, when the task
// has a reference solution, pass on the fixture with that solution applied.
func SelfCheck(ctx context.Context, root string, t Task) error {
	dir, err := os.MkdirTemp("", "drydock-selfcheck-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	bare := filepath.Join(dir, "fixture")
	if err := copyTree(filepath.Join(root, t.Fixture), bare, nil); err != nil {
		return err
	}
	if res := Accept(ctx, root, t, bare); res.Error != "" {
		return fmt.Errorf("%s: the hidden check could not run: %s", t.ID, res.Error)
	} else if res.Passed {
		return fmt.Errorf("%s: the hidden check passes on the untouched fixture, so it proves nothing", t.ID)
	}
	patch := filepath.Join(root, SolutionsDir, t.ID+".patch")
	overlay := filepath.Join(root, SolutionsDir, t.ID)
	_, perr := os.Stat(patch)
	_, oerr := os.Stat(overlay)
	if perr != nil && oerr != nil {
		return nil // no reference solution: only the first half can be checked
	}
	solved := filepath.Join(dir, "solved")
	if err := copyTree(filepath.Join(root, t.Fixture), solved, nil); err != nil {
		return err
	}
	if perr == nil {
		if _, err := git(ctx, solved, "apply", "--whitespace=nowarn", patch); err != nil {
			return fmt.Errorf("%s: the reference solution does not apply to the fixture: %w", t.ID, err)
		}
	} else if err := copyTree(overlay, solved, nil); err != nil {
		return err
	}
	res := Accept(ctx, root, t, solved)
	if !res.Passed {
		msg := res.Error
		if n := len(res.Commands); n > 0 {
			msg = res.Commands[n-1].Command + ": " + res.Commands[n-1].Output
		}
		return fmt.Errorf("%s: the hidden check fails on the reference solution: %s", t.ID, msg)
	}
	return nil
}
