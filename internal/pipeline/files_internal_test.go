package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

func writeLines(t *testing.T, dir, name string, n int, line func(i int) string) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(line(i) + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Ranges around nearby hits merge, at most two ranges of at most 100 lines are taken, and a hit past the end is ignored.
func TestRangesMergeAndStayBounded(t *testing.T) {
	if got := ranges([]int{100, 110}, 500); len(got) != 1 || got[0] != [2]int{80, 130} {
		t.Fatalf("merged %v", got)
	}
	if got := ranges([]int{300, 50, 450}, 500); len(got) != 2 || got[0] != [2]int{30, 70} || got[1] != [2]int{280, 320} {
		t.Fatalf("two ranges %v", got)
	}
	var many []int
	for i := 100; i < 400; i += 30 {
		many = append(many, i)
	}
	if got := ranges(many, 500); got[0][1]-got[0][0]+1 > maxRangeLines {
		t.Fatalf("range over %d lines: %v", maxRangeLines, got)
	}
	if got := ranges([]int{900}, 500); len(got) != 1 || got[0] != [2]int{1, fileHeadLines} {
		t.Fatalf("past the end %v", got)
	}
}

// A long file with no relevant line enters as its head; a short file of long lines is not whole; a long line is cut; a binary
// file and a path outside the worktree are left out with the reason.
func TestExcerptFileBounds(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, dir, "long.go", 400, func(i int) string { return "// plain line" })
	fx := excerptFile(dir, "long.go", nil, []string{"NotThere"})
	if fx.Whole || len(fx.Ranges) != 1 || fx.Ranges[0] != [2]int{1, fileHeadLines} || !strings.Contains(fx.Why, "first lines") || strings.Contains(fx.Text, "\n41| ") {
		t.Fatalf("head %+v", fx.Ranges)
	}
	writeLines(t, dir, "wide.go", 100, func(i int) string { return strings.Repeat("y", 500) })
	fx = excerptFile(dir, "wide.go", nil, nil)
	if fx.Whole || !strings.Contains(fx.Text, "[line cut]") {
		t.Fatalf("wide %+v", fx.Ranges)
	}
	os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("ab\x00cd"), 0o644)
	if fx := excerptFile(dir, "blob.bin", nil, nil); fx.Skip != "not included: a binary file" {
		t.Fatalf("binary %+v", fx)
	}
	if fx := excerptFile(dir, "../outside.go", nil, nil); fx.Skip == "" {
		t.Fatal("a path out of the worktree was read")
	}
}

// The files of a package: the task's paths (globs are not files), then the ones a failing check names, at most six; secrets in
// an excerpt are redacted.
func TestFileExcerptsPicksAndBoundsTheFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go"} {
		writeLines(t, dir, n, 3, func(i int) string { return "package x" })
	}
	writeLines(t, dir, "key.go", 2, func(i int) string { return `const k = "AKIAIOSFODNN7EXAMPLE"` })
	d := Deps{Redact: func(s string) string { return strings.ReplaceAll(s, "AKIAIOSFODNN7EXAMPLE", "[REDACTED]") }}
	tk := tasks.Task{Worktree: dir, Files: []string{"key.go", "src/*.go", "a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go"}}
	fx := d.fileExcerpts(t.Context(), tk, workflow.NodeInput{})
	if len(fx) != 8 {
		t.Fatalf("%d excerpts: %+v", len(fx), fx)
	}
	if strings.Contains(fx[0].Text, "AKIA") || !strings.Contains(fx[0].Text, "[REDACTED]") {
		t.Fatalf("not redacted: %q", fx[0].Text)
	}
	if fx[6].Skip == "" || !strings.Contains(fx[6].Skip, "at most 6 files") || fx[7].Path != "g.go" || fx[7].Skip == "" {
		t.Fatalf("bound: %+v", fx[6:])
	}
	// a later pass: only the worktree file the failing check names, around its line; the toolchain's path is not the worktree's
	fx = d.fileExcerpts(t.Context(), tk, inboxWithCheck(t, "g.go:2: undefined: y\n/usr/lib/go/src/fmt/print.go:10: x"))
	if len(fx) != 1 || fx[0].Path != "g.go" || fx[0].Skip != "" || !fx[0].Whole {
		t.Fatalf("later pass: %+v", fx)
	}
}

func inboxWithCheck(t *testing.T, output string) workflow.NodeInput {
	t.Helper()
	b, _ := json.Marshal(workflow.CheckResult{Name: "build", Passed: false, ExitCode: 1, Output: output})
	return workflow.NodeInput{Inbox: []workflow.Message{{From: "verify", Value: b}}}
}
