package core

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/tasks"
)

// The Inbox's words for a review and merge name the action its button names (critique 2026-11-08).
func TestMergeDescriptionSaysMerge(t *testing.T) {
	d := ApprovalDetail{Kind: "review", Phase: "merge"}
	if got, want := approvalDescription(d, &tasks.Task{Seq: 1, Title: "Add Pow", BaseBranch: "main"}, ""), "Task 1 (Add Pow) is ready to merge into main. Merge it, or ask for changes."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := approvalDescription(d, nil, ""), "The task is ready to merge. Merge it, or ask for changes."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// with the project's key, the task is named as the board names it (critique 2026-11-13: GOC-1, #1 and "Task 1")
	if got, want := approvalDescription(d, &tasks.Task{Seq: 1, Title: "Add Pow", BaseBranch: "main"}, "GOC"), "GOC-1 (Add Pow) is ready to merge into main. Merge it, or ask for changes."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := approvalDescription(ApprovalDetail{Gate: PermissionGate, Tool: "Bash", Agent: &ApprovalAgent{Name: "Ada"}}, &tasks.Task{Seq: 4, Title: "Fix it"}, "GOC"), "Ada wants to use Bash for GOC-4 (Fix it). Its own rules do not allow it, so it waits for your answer; approving allows only this exact call."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
