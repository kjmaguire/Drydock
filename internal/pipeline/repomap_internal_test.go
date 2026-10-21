package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// The manifest measures the repo map section of a generated AGENTS.md (decision P62): from its heading to the next one.
func TestRepoMapSectionBytesMeasuresTheSection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "AGENTS.md")
	section := "## Repository map\n\nThe most important code.\n\n````text\ngo/store.go:\n│func NewStore() *Store {\n⋮\n````\n"
	body := "# Project instructions\n\n## Skills\n\nsome\n\n" + section + "\n## How to work here\n\n- tips\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := repoMapSectionBytes(p), len(section)+1; got != want {
		t.Fatalf("section bytes = %d, want %d", got, want)
	}
	if err := os.WriteFile(p, []byte("# Project instructions\n\n## How to work here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := repoMapSectionBytes(p); n != 0 {
		t.Fatalf("no section: %d", n)
	}
	if n := repoMapSectionBytes(filepath.Join(dir, "missing.md")); n != 0 {
		t.Fatalf("no file: %d", n)
	}
}
