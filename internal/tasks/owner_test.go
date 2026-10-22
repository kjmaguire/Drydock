package tasks

import (
	"context"
	"errors"
	"testing"
)

// A task a person owns (assignee "human:<name>") is listed under that person, and no agent can claim it.
func TestATaskAPersonOwnsIsListedForThemAndIsNeverClaimed(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	mine, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Write the release notes"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Fix the parser"})
	if _, err := s.Patch(ctx, mine.ID, Update{Assignee: ptr("human:kyle")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patch(ctx, other.ID, Update{Assignee: ptr("agt_1"), Reviewer: ptr("agt_2")}); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx, Filter{Assignee: "human:kyle"})
	if err != nil || len(got) != 1 || got[0].ID != mine.ID || !got[0].OwnedByPerson() {
		t.Fatalf("my tasks %+v %v", got, err)
	}
	if n, _ := s.Count(ctx, Filter{Assignee: "human:kyle"}); n != 1 {
		t.Fatalf("count %d", n)
	}
	if (Task{Assignee: "agt_1"}).OwnedByPerson() || (Task{Assignee: "human:"}).OwnedByPerson() || (Task{}).OwnedByPerson() {
		t.Fatal("only human:<name> is a person")
	}
	if _, err := s.Transition(ctx, mine.ID, Ready, TransitionOpts{By: "human:kyle"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Transition(ctx, mine.ID, Claimed, TransitionOpts{By: "system"})
	var te *TransitionError
	if !errors.As(err, &te) || te.Reason != "a person owns this task: no agent claims it" {
		t.Fatalf("claiming a person's task: %v", err)
	}
}
