package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SelfCheck runs a tool's program the way every scan runs it, over a small folder of its own, and says why the program cannot
// be used (decision P95). Installer.Check calls it before a release becomes the one in use, so a release that changed or
// dropped an option Drydock passes (Opengrep 1.31.0 refused --no-progress-bar, every scan's Code engine then failed with exit
// 2) is never put in place by an update: the release in use stays. Opengrep must finish with the built-in rules over one Go
// file; osv-scanner must finish over a folder with one go.mod (vulnerabilities found or not; no lockfile found is as good).
// o gives the processes, environment and timeout; its Dir, rules and Paths are the check's own.
func SelfCheck(ctx context.Context, o Options, tool string, t Tool) error {
	dir, err := os.MkdirTemp("", "drydock-selfcheck-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	code := filepath.Join(dir, "code")
	if err := os.MkdirAll(code, 0o755); err != nil {
		return err
	}
	o.Dir, o.NoGit, o.RulesDir, o.Paths = code, true, "", Paths{}
	switch tool {
	case "opengrep":
		if o.BuiltinRules, err = WriteBuiltinRules(filepath.Join(dir, "rules")); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(code, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
			return err
		}
		if _, st, reason, _ := scanCode(ctx, &o, t.Path, t); st != Done {
			return checkFailed(st, reason)
		}
	case "osv-scanner":
		if err := os.WriteFile(filepath.Join(code, "go.mod"), []byte("module selfcheck\n\ngo 1.22\n"), 0o644); err != nil {
			return err
		}
		if _, st, reason := scanDeps(ctx, &o, t.Path, t); st != Done && st != Skipped {
			return checkFailed(st, reason)
		}
	default:
		return fmt.Errorf("%q is not a tool Drydock installs", tool)
	}
	return nil
}

func checkFailed(st EngineStatus, reason string) error {
	if st == Cancelled {
		return context.Canceled
	}
	if reason == "" {
		return errors.New("it did not finish")
	}
	return errors.New(reason)
}
