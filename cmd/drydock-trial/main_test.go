package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// The plain arm runs with this machine's whole file system: its repositories must not sit next to the hidden checks.
func TestRunRefusesAWorkDirectoryInsideTheRepository(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, work := range []string{filepath.Join(root, "trial-work"), root} {
		// the check comes before claude is ever started, so no claude is needed here
		err = run(context.Background(), []string{"-arms", "B1", "-tasks", "saasapi-invoice-rules", "-claude", "no-such-claude", "-work", work})
		if err == nil || !strings.Contains(err.Error(), "-work must be outside the repository") {
			t.Fatalf("work directory %s: %v", work, err)
		}
	}
	if insideRepo(root, filepath.Join(filepath.Dir(root), "elsewhere")) {
		t.Fatal("a sibling of the repository counts as inside it")
	}
}

func TestRunRefusesUnknownArmsTasksAndModes(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-arms", "D,B2"}, `unknown arm "B2"`},
		{[]string{"-tasks", "no-such-task"}, "no such task: no-such-task"},
		{[]string{"-d-mode", "vm"}, "-d-mode is sandbox or checks"},
	} {
		if err := run(context.Background(), append(c.args, "-claude", "no-such-claude")); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

func TestSelfcheckProvesAHiddenCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and tests the fixture twice")
	}
	if err := selfcheck(context.Background(), []string{"-tasks", "saasapi-csv-export"}); err != nil {
		t.Fatal(err)
	}
}

// The record names the Drydock the binary was built from, not wherever the repository has moved since.
func TestDrydockCommitIsTheBinarysOwn(t *testing.T) {
	stamped := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"}, {Key: "vcs.modified", Value: "true"}}}
	if got := drydockCommit(t.TempDir(), stamped); got != "0123456789ab+dirty" {
		t.Fatalf("stamped binary: %q", got)
	}
	stamped.Settings[1].Value = "false"
	if got := drydockCommit(t.TempDir(), stamped); got != "0123456789ab" {
		t.Fatalf("stamped binary, clean tree: %q", got)
	}
	// no stamp (go run): the repository's HEAD
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=x", "-c", "user.email=x@x.invalid", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "--short=12", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := drydockCommit(repo, &debug.BuildInfo{}); got != strings.TrimSpace(string(head)) {
		t.Fatalf("unstamped binary: %q, want the repository's HEAD %s", got, head)
	}
}
