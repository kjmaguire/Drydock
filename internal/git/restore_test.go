package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A person's restore of a recorded safe point (decision P124): the files that will change are listed first, from the
// worktree's side; the restore keeps the current state under its own undo ref, which restores it again; a ref that is not one
// of the task's safe points is not one.
func TestASafePointRestoreListsItsChangesAndCanBeUndone(t *testing.T) {
	ctx := context.Background()
	r, parent := newRepo(t)
	w, _ := r.CreateWorktree(ctx, WorktreeReq{TaskID: "task_9", Title: "restore", Parent: parent})
	write(t, w.Path, "src/main.go", "package main\n// before the turn\n")
	write(t, w.Path, "notes.txt", "notes\n")
	ref, err := r.RunSafePoint(ctx, w, "run_x", 101)
	if err != nil {
		t.Fatal(err)
	}
	// the turn after the safe point: an edit, a deletion, a new file
	write(t, w.Path, "src/main.go", "package main\n// the turn broke it\n")
	os.Remove(filepath.Join(w.Path, "notes.txt"))
	write(t, w.Path, "junk/new.txt", "junk\n")

	changes, err := r.SafePointChanges(ctx, w, ref)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range changes {
		got = append(got, c.Change+" "+c.Path)
	}
	if strings.Join(got, ", ") != "removed junk/new.txt, restored notes.txt, changed src/main.go" {
		t.Fatalf("changes %v", got)
	}
	// listing changes nothing
	if b, _ := os.ReadFile(filepath.Join(w.Path, "src", "main.go")); !strings.Contains(string(b), "broke") {
		t.Fatal("the listing touched the worktree")
	}

	undoRef := UndoRef(w.TaskID, 1700000000000)
	undo, err := r.RestoreSafePointTo(ctx, w, ref, undoRef)
	if err != nil || undo != undoRef {
		t.Fatalf("restore: %q %v", undo, err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Path, "src", "main.go")); string(b) != "package main\n// before the turn\n" {
		t.Fatalf("main.go %q", b)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "junk", "new.txt")); err == nil {
		t.Fatal("the new file is still there")
	}
	if c, _ := r.SafePointChanges(ctx, w, ref); len(c) != 0 {
		t.Fatalf("nothing is left to restore: %+v", c)
	}
	// the run's safe point did not move; the undo brings the turn's state back
	if c, _ := r.SafePointChanges(ctx, w, undoRef); len(c) != 3 {
		t.Fatalf("the undo would change %+v", c)
	}
	if _, err := r.RestoreSafePointTo(ctx, w, undoRef, UndoRef(w.TaskID, 1700000000001)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(w.Path, "junk", "new.txt")); string(b) != "junk\n" {
		t.Fatalf("undo: %q", b)
	}
	// both restores kept their undo point, listed newest first with the safe point each put back (P127); another task's are
	// not listed
	pts, err := r.UndoPoints(ctx, w.TaskID)
	if err != nil || len(pts) != 2 {
		t.Fatalf("undo points %+v %v", pts, err)
	}
	if pts[0].Ref != UndoRef(w.TaskID, 1700000000001) || pts[0].At != 1700000000001 || pts[0].Restored != undoRef ||
		pts[1].Ref != undoRef || pts[1].Restored != ref {
		t.Fatalf("undo points %+v", pts)
	}
	if other, err := r.UndoPoints(ctx, "task_1"); err != nil || len(other) != 0 {
		t.Fatalf("another task's %+v %v", other, err)
	}

	if _, err := r.SafePointChanges(ctx, w, "refs/drydock/safe/task_9/run_x/999"); err == nil {
		t.Fatal("a safe point that was never recorded")
	}
	for ref, ok := range map[string]bool{
		"refs/drydock/safe/task_9/run_x/101":     true,
		"refs/drydock/safe/task_9/undo/17":       true,
		"refs/drydock/safe/task_10/run_x/101":    false,
		"refs/heads/main":                        false,
		"refs/drydock/safe/task_9/":              false,
		"refs/drydock/safe/task_9/../../heads/x": false,
		"refs/drydock/safe/task_9/x^{tree}":      false,
	} {
		if IsSafePointRef("task_9", ref) != ok {
			t.Errorf("IsSafePointRef(%q) != %v", ref, ok)
		}
	}
}
