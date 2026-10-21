package core

import (
	"encoding/json"
	"testing"

	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
)

func TestNeedsContextIsTheNewestDiffChecksAndVerdict(t *testing.T) {
	yes, no := true, false
	stat := func(f, i, d int) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"Files": f, "Insertions": i, "Deletions": d, "base": "abc"})
		return b
	}
	ev := []tasks.Evidence{
		{Kind: "test", Attempt: 1, Passed: &no},
		{Kind: "diff", Attempt: 1, Data: stat(3, 40, 2)},
		// the second attempt: test fails then passes (the latest per kind counts), e2e passes, lint fails
		{Kind: "test", Attempt: 2, Passed: &no},
		{Kind: "test", Attempt: 2, Passed: &yes},
		{Kind: "e2e", Attempt: 2, Passed: &yes},
		{Kind: "lint", Attempt: 2, Passed: &no},
		{Kind: "diff", Attempt: 2, Data: stat(1, 4, 1)},
		{Kind: "commit", Attempt: 2},
	}
	rv := []tasks.ReviewRecord{{Verdict: "REVISE"}, {Verdict: "SHIP"}}
	got := needsContextOf(tasks.Task{Assignee: "agt_rita"}, ev, rv)
	want := room.NeedsContext{Agent: "agt_rita", HasDiff: true, Files: 1, Insertions: 4, Deletions: 1, ChecksPassed: 2, ChecksTotal: 3, Verdict: "SHIP"}
	if *got != want {
		t.Fatalf("context = %+v, want %+v", *got, want)
	}
	// a task with nothing recorded yet: no diff, no checks, no verdict
	if got := needsContextOf(tasks.Task{}, nil, nil); *got != (room.NeedsContext{}) {
		t.Fatalf("empty context = %+v", *got)
	}
}
