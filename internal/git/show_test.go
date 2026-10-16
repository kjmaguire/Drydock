package git

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

// The size of a file in a commit is asked of git before the file is read, and a file over the limit is never read into memory
// (audit 2026-10-16, L22): a file of hundreds of megabytes used to be read whole before anything was said about its size.
func TestAFileInACommitIsReadOnlyWhenItIsNoLargerThanAllowed(t *testing.T) {
	ctx := context.Background()
	r, _ := newRepo(t)
	const big = 40 << 20
	write(t, r.Root, "big.txt", strings.Repeat("0123456789abcdef\n", big/17+1)[:big])
	write(t, r.Root, "small.txt", "hello\n")
	if _, err := r.Commit(ctx, Worktree{Path: r.Root}, "add files"); err != nil {
		t.Fatal(err)
	}
	head, err := r.Head(ctx, Worktree{Path: r.Root})
	if err != nil {
		t.Fatal(err)
	}

	if n, ok, err := r.BlobSize(ctx, head, "big.txt"); err != nil || !ok || n != big {
		t.Fatalf("size of the big file: %d %v %v", n, ok, err)
	}
	if n, ok, err := r.BlobSize(ctx, head, "small.txt"); err != nil || !ok || n != 6 {
		t.Fatalf("size of the small file: %d %v %v", n, ok, err)
	}
	if _, ok, err := r.BlobSize(ctx, head, "missing.txt"); err != nil || ok {
		t.Fatalf("a file that is not there: %v %v", ok, err)
	}
	if _, _, err := r.BlobSize(ctx, "0000000000000000000000000000000000000000", "small.txt"); !errors.Is(err, ErrUnknownRevision) {
		t.Fatalf("a commit that is not there: %v", err)
	}
	if text, ok, err := r.ShowFile(ctx, head, "small.txt"); err != nil || !ok || text != "hello\n" {
		t.Fatalf("a small file: %q %v %v", text, ok, err)
	}

	// over the default limit: refused, and not read
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	text, ok, err := r.ShowFile(ctx, head, "big.txt")
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrBlobTooLarge) || !ok || text != "" {
		t.Fatalf("a file over the limit: %d bytes, %v, %v", len(text), ok, err)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 8<<20 {
		t.Fatalf("the file over the limit was read into memory: %d bytes allocated for a %d byte file", grown, big)
	}
	// a caller may allow more, or less
	if text, ok, err := r.ShowFileMax(ctx, head, "big.txt", big); err != nil || !ok || len(text) != big {
		t.Fatalf("a file within a larger limit: %d %v %v", len(text), ok, err)
	}
	if _, _, err := r.ShowFileMax(ctx, head, "small.txt", 5); !errors.Is(err, ErrBlobTooLarge) {
		t.Fatalf("a file over a smaller limit: %v", err)
	}
}
