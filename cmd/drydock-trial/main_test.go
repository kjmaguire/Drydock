package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The plain arm runs with this machine's whole file system: its repositories must not sit next to the hidden checks.
func TestRunRefusesAWorkDirectoryInsideTheRepository(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), []string{"-arms", "B1", "-tasks", "saasapi-invoice-rules", "-claude", "/bin/echo", "-work", filepath.Join(root, "trial-work")})
	if err == nil || !strings.Contains(err.Error(), "-work must be outside the repository") {
		t.Fatalf("a work directory inside the repository: %v", err)
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
		if err := run(context.Background(), append(c.args, "-claude", "/bin/echo")); err == nil || !strings.Contains(err.Error(), c.want) {
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
