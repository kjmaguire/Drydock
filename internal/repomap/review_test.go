package repomap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Tests for the code review of the first version (P62): the cache's growth across task worktrees, links out of the
// checkout, a trimmed memory cache, a failed cache write and Aider's weight for ALL_CAPS names.

func listOf(rels []string) Lister {
	return func(context.Context, string) ([]string, error) { return rels, nil }
}

// Every task maps a new worktree of the same repository. With one cache key for them all, the project keeps one cache
// file, not one per task that is never read again.
func TestWorktreesOfOneRepositoryShareOneCacheFile(t *testing.T) {
	cache := t.TempDir()
	var calls atomic.Int64
	m := countingMapper(nil, cache, &calls)
	files := map[string]string{"a.go": "//def LookupValue\n", "b.go": "//ref LookupValue\n"}
	for i := 0; i < 4; i++ {
		root := t.TempDir() // a task's worktree
		rels := writeFiles(t, root, files)
		if _, err := m.Map(context.Background(), Request{Root: root, List: listOf(rels), CacheKey: "repository"}); err != nil {
			t.Fatal(err)
		}
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 1 {
		t.Fatalf("four worktrees of one repository left %d cache files: %v", len(entries), entries)
	}
	if calls.Load() != 2 {
		t.Fatalf("the worktrees share their files' tags: %d parses, want 2", calls.Load())
	}
}

// The file stats are kept per path, so each new worktree adds its files; past memoryCap only the last map's are kept.
func TestMemoryStaysBoundedAcrossManyCheckouts(t *testing.T) {
	defer func(c int) { memoryCap = c }(memoryCap)
	memoryCap = 5
	var calls atomic.Int64
	m := countingMapper(nil, "", &calls)
	for i := 0; i < 6; i++ {
		root := t.TempDir()
		rels := writeFiles(t, root, map[string]string{"a.go": "//def A\n", "b.go": "//ref A\n", "c.go": "//def C\n"})
		if _, err := m.Map(context.Background(), Request{Root: root, List: listOf(rels)}); err != nil {
			t.Fatal(err)
		}
		m.mu.Lock()
		n := len(m.stats)
		m.mu.Unlock()
		if n > memoryCap+3 {
			t.Fatalf("after %d checkouts the stats hold %d paths (cap %d)", i+1, n, memoryCap)
		}
	}
}

// A file whose tags were dropped from memory when the cache was trimmed comes back from its cache file, not the parser.
func TestATrimmedCacheIsReadFromDiskAgain(t *testing.T) {
	defer func(c int) { memoryCap = c }(memoryCap)
	memoryCap = 3
	cache := t.TempDir()
	var calls atomic.Int64
	m := countingMapper(nil, cache, &calls)
	ctx := context.Background()
	rootA, rootB := t.TempDir(), t.TempDir()
	relsA := writeFiles(t, rootA, map[string]string{"a1.go": "//def A1\n", "a2.go": "//def A2\n", "a3.go": "//ref A1\n"})
	relsB := writeFiles(t, rootB, map[string]string{"b1.go": "//def B1\n", "b2.go": "//def B2\n", "b3.go": "//ref B1\n"})
	if _, err := m.Map(ctx, Request{Root: rootA, List: listOf(relsA)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Map(ctx, Request{Root: rootB, List: listOf(relsB)}); err != nil { // trims A's tags from memory
		t.Fatal(err)
	}
	before := calls.Load()
	// a new edit time makes the stats miss, so the tags must come from the content hash: memory or the cache file
	for _, rel := range relsA {
		p := filepath.Join(rootA, rel)
		b, _ := os.ReadFile(p)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := m.Map(ctx, Request{Root: rootA, List: listOf(relsA)})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before || res.Cached != 3 {
		t.Fatalf("A's tags were parsed again (%d parses, %d cached) instead of read from its cache file", calls.Load()-before, res.Cached)
	}
}

// A cache file that could not be written is written by the next map, not taken as saved.
func TestAFailedCacheWriteIsTriedAgain(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "projects")
	if err := os.WriteFile(blocker, []byte("a file where the folder should be"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(blocker, "repomap")
	root := t.TempDir()
	rels := writeFiles(t, root, map[string]string{"a.go": "//def LookupValue\n"})
	var calls atomic.Int64
	m := countingMapper(listOf(rels), cache, &calls)
	if _, err := m.Map(context.Background(), Request{Root: root}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Map(context.Background(), Request{Root: root}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Fatalf("the cache file was not written once it could be: %v %v", entries, err)
	}
}

// The map shows only code that is in the checkout, as read_range does: a file that is a link, and a file reached through
// a folder that was replaced by a link (git still lists it from its index), are not read.
func TestLinksOutOfTheCheckoutAreNotRead(t *testing.T) {
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"secrets.go": "//def StripeLiveKeyOutside\n", "pkg/inner.go": "//def InnerOutside\n"})
	root := t.TempDir()
	rels := writeFiles(t, root, map[string]string{"main.go": "//ref StripeLiveKeyOutside\n//def MainEntry\n"})
	if err := os.Symlink(filepath.Join(outside, "secrets.go"), filepath.Join(root, "keys.go")); err != nil {
		t.Skipf("cannot make a link here: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "pkg"), filepath.Join(root, "pkg")); err != nil {
		t.Fatal(err)
	}
	rels = append(rels, "keys.go", "pkg/inner.go")
	var calls atomic.Int64
	m := countingMapper(listOf(rels), "", &calls)
	res, err := m.Map(context.Background(), Request{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped.Linked != 2 || res.Files != 1 || calls.Load() != 1 {
		t.Fatalf("links: %+v, %d files, %d parses", res.Skipped, res.Files, calls.Load())
	}
	if strings.Contains(res.Map, "Outside") {
		t.Fatalf("the map shows code from outside the checkout:\n%s", res.Map)
	}
	// the root itself may be reached through a link
	linkedRoot := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(root, linkedRoot); err != nil {
		t.Fatal(err)
	}
	res, err = m.Map(context.Background(), Request{Root: linkedRoot, List: listOf([]string{"main.go"})})
	if err != nil || res.Files != 1 || res.Skipped.Linked != 0 {
		t.Fatalf("a checkout reached through a link: %+v %v", res, err)
	}
}

func TestAllCapsSnakeNamesWeighAsAidersDo(t *testing.T) {
	for _, c := range []struct {
		ident string
		want  float64
	}{{"MAX_RETRY_ATTEMPTS", 10}, {"DEFAULT_TIMEOUT_MS", 10}, {"MAX_X", 1}, {"__________", 0.1}} {
		if got := identWeight(c.ident, 1); got < c.want*0.999 || got > c.want*1.001 {
			t.Errorf("identWeight(%q) = %v, want %v", c.ident, got, c.want)
		}
	}
}
