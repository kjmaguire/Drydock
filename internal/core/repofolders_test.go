package core

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// repo.folders lists directory names two levels deep, sorted, leaving out hidden, dependency and build folders, files and
// symbolic links, and stops at its cap (X17).
func TestRepoFoldersListsTheRepositorysFoldersTwoLevelsDeep(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"auth/login/deep", "cmd/drydock", ".git/objects", "node_modules/x", "docs", "internal/room"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(filepath.Join(root, "docs"), filepath.Join(root, "linked")) // may fail on a host without links: then nothing to check
	got, err := RepoFolders(root, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"auth/", "auth/login/", "cmd/", "cmd/drydock/", "docs/", "internal/", "internal/room/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got, _ := RepoFolders(root, 1, 2); len(got) != 2 {
		t.Fatalf("the cap: %v", got)
	}
	if _, err := RepoFolders("", 2, 10); err == nil {
		t.Fatal("no folder")
	}
	if _, err := RepoFolders(filepath.Join(root, "missing"), 2, 10); err == nil {
		t.Fatal("a missing folder")
	}
}
