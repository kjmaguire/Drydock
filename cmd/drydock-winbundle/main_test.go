package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A mistyped -out must never empty a folder that is not an earlier bundle; the builder stops before compiling anything.
func TestARefusedOutputFolderIsReportedAndKept(t *testing.T) {
	foreign := t.TempDir()
	keep := filepath.Join(foreign, "notes.txt")
	os.WriteFile(keep, []byte("mine"), 0o644)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-goos", "linux", "-out", foreign, "-zip", "-"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "refusing to replace it") {
		t.Fatalf("stderr: %s", stderr.String())
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("the folder's file must still be there")
	}
	if code := run(context.Background(), []string{"-goos"}, &stdout, &stderr); code != 2 {
		t.Fatalf("a flag without its value: %d", code)
	}
}
