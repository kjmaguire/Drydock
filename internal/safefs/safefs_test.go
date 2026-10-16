package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// link makes a symbolic link, or skips the test where this machine does not allow one (Windows without developer mode).
func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

func TestReadFileFollowsOnlyLinksThatStayInsideTheRoot(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "CREDENTIALS")
	root := t.TempDir()
	write(t, filepath.Join(root, "AGENTS.md"), "rules")
	write(t, filepath.Join(root, "docs", "a.md"), "docs")
	link(t, "AGENTS.md", filepath.Join(root, "CLAUDE.md"))                      // inside: the usual CLAUDE.md -> AGENTS.md
	link(t, filepath.Join(outside, "secret"), filepath.Join(root, "STOLEN.md")) // absolute, outside
	link(t, filepath.Join("..", filepath.Base(outside), "secret"), filepath.Join(root, "REL.md"))
	link(t, outside, filepath.Join(root, "linkdir")) // a folder link to outside

	if b, err := ReadFile(root, "CLAUDE.md", 0); err != nil || string(b) != "rules" {
		t.Fatalf("a link inside the root: %q %v", b, err)
	}
	for _, rel := range []string{"STOLEN.md", "REL.md", "linkdir/secret", "../" + filepath.Base(outside) + "/secret", "docs/../../" + filepath.Base(outside) + "/secret"} {
		if b, err := ReadFile(root, rel, 0); err == nil {
			t.Fatalf("%s read %q from outside the root", rel, b)
		}
	}
	if _, err := ReadFile(root, "docs", 0); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("a folder: %v", err)
	}
	if b, err := ReadFile(root, "docs/a.md", 2); err != nil || string(b) != "do" {
		t.Fatalf("a bounded read: %q %v", b, err)
	}
	if _, err := ReadFile(root, "missing.md", 0); !os.IsNotExist(err) {
		t.Fatalf("a missing file: %v", err)
	}
}

func TestWriteFileReplacesALinkInsteadOfWritingThroughIt(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "important")
	write(t, victim, "KEEP")
	root := t.TempDir()
	link(t, victim, filepath.Join(root, "AGENTS.md"))
	link(t, outside, filepath.Join(root, "escape"))

	if err := WriteFile(root, "AGENTS.md", []byte("generated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "KEEP" {
		t.Fatalf("the file the link pointed to was written: %q", b)
	}
	st, err := os.Lstat(filepath.Join(root, "AGENTS.md"))
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("the link was not replaced by a file: %v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "AGENTS.md")); string(b) != "generated" {
		t.Fatalf("content %q", b)
	}
	if err := WriteFile(root, "escape/new.md", []byte("x"), 0o644); err == nil {
		t.Fatal("a write through a folder link left the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.md")); err == nil {
		t.Fatal("a file appeared outside the root")
	}
	if err := WriteFile(root, "../x.md", []byte("x"), 0o644); err == nil {
		t.Fatal("a write with .. left the root")
	}
	// plain writes, folders and removal
	if err := MkdirAll(root, ".claude/rules", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(root, ".claude/rules/a.md", []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(root, ".claude/rules/a.md", []byte("longer than before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := ReadFile(root, ".claude/rules/a.md", 0); string(b) != "longer than before" {
		t.Fatalf("rewrite: %q", b)
	}
	if err := Remove(root, ".claude/rules/a.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := Lstat(root, ".claude/rules/a.md"); !os.IsNotExist(err) {
		t.Fatalf("after Remove: %v", err)
	}
	if err := MkdirAll(root, "escape/sub", 0o755); err == nil || strings.Contains(err.Error(), "exists") {
		t.Logf("MkdirAll through a link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "sub")); err == nil {
		t.Fatal("MkdirAll created a folder outside the root")
	}
}
