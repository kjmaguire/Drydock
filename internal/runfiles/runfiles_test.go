package runfiles

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A compacted file reads back exactly as the original did, through every reader, and the original is gone.
func TestCompressThenReadBackTheSameBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")
	text := []byte(strings.Repeat(`{"kind":"text","text":"implemented Subtract"}`+"\n", 2000))
	if err := os.WriteFile(path, text, 0o600); err != nil {
		t.Fatal(err)
	}
	before, after, err := Compress(path)
	if err != nil {
		t.Fatal(err)
	}
	if before != int64(len(text)) || after <= 0 || after >= before/10 {
		t.Fatalf("sizes %d -> %d", before, after)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the original is still there")
	}
	if size, compacted, ok := Stat(path); !ok || !compacted || size != int64(len(text)) {
		t.Fatalf("stat %d %v %v", size, compacted, ok)
	}
	if b, err := ReadAll(path); err != nil || !bytes.Equal(b, text) {
		t.Fatalf("ReadAll: %v (%d bytes)", err, len(b))
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	buf := make([]byte, 20)
	if n, err := r.ReadAt(buf, 1); err != nil || n != 20 || !bytes.Equal(buf, text[1:21]) || r.Size() != int64(len(text)) {
		t.Fatalf("ReadAt %q %v size %d", buf[:n], err, r.Size())
	}
	// no leftover temporary file
	if ents, _ := os.ReadDir(dir); len(ents) != 1 || ents[0].Name() != "stream.jsonl.gz" {
		t.Fatalf("dir %v", ents)
	}
}

// A plain file is read as it is, and a missing file is os.ErrNotExist in both forms.
func TestPlainAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "check.log")
	if err := os.WriteFile(path, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if size, compacted, ok := Stat(path); !ok || compacted || size != 3 {
		t.Fatalf("stat %d %v %v", size, compacted, ok)
	}
	if b, err := ReadAll(path); err != nil || string(b) != "ok\n" {
		t.Fatalf("%q %v", b, err)
	}
	missing := filepath.Join(dir, "nope.log")
	if _, _, ok := Stat(missing); ok {
		t.Fatal("a missing file exists")
	}
	if _, err := ReadAll(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadAll: %v", err)
	}
	if _, err := Open(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open: %v", err)
	}
}

func TestCompactableNames(t *testing.T) {
	for name, want := range map[string]bool{"stream.jsonl": true, "test.log": true, "prompt.md": true, "output.json": false, "README.md": false, "stream.jsonl.gz": false} {
		if Compactable(name) != want {
			t.Errorf("%s: want %v", name, want)
		}
	}
}
