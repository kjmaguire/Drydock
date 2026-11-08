package core

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/tasks"
)

// The Inbox's words for a review and merge name the action its button names (critique 2026-11-08).
func TestMergeDescriptionSaysMerge(t *testing.T) {
	d := ApprovalDetail{Kind: "review", Phase: "merge"}
	if got, want := approvalDescription(d, &tasks.Task{Seq: 1, Title: "Add Pow", BaseBranch: "main"}), "Task 1 (Add Pow) is ready to merge into main. Merge it, or ask for changes."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := approvalDescription(d, nil), "The task is ready to merge. Merge it, or ask for changes."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
