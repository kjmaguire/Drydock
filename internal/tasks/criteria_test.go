package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/events"
)

// Who may say what about an acceptance criterion (decision P125): a person confirms, says not met, waives with a reason or
// reopens; an agent only claims a criterion met, with evidence. A mark keeps the hash of the criterion's text.
func TestOnlyAPersonConfirmsOrWaivesACriterionAndAnAgentOnlyClaims(t *testing.T) {
	s, bus := newSvc(t)
	ctx := context.Background()
	tk, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "t", Acceptance: []string{"AC1: Given a, when b, then c", "AC2: Given d, when e, then f"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, mark := range []string{MarkMet, MarkNotMet, MarkOpen, MarkWaived} {
		if _, err := s.MarkCriterion(ctx, tk.ID, 0, mark, "a1", "because"); !errors.Is(err, ErrNotAPerson) {
			t.Errorf("an agent's %s: %v", mark, err)
		}
		if _, err := s.MarkCriterion(ctx, tk.ID, 0, mark, "human:", "because"); !errors.Is(err, ErrNotAPerson) {
			t.Errorf("an empty person's %s: %v", mark, err)
		}
	}
	for _, tc := range []struct {
		index       int
		mark, actor string
		note, want  string
	}{
		{0, MarkWaived, "human:kyle", "", "say why"},
		{0, MarkClaimMet, "human:kyle", "TestAC1 passes", "a claim is an agent's"},
		{0, MarkClaimMet, "a1", " ", "needs its evidence"},
		{2, MarkMet, "human:kyle", "", "has 2 acceptance criteria; there is no criterion 3"},
		{-1, MarkMet, "human:kyle", "", "there is no criterion 0"},
		{0, "done", "human:kyle", "", "unknown mark"},
		{0, MarkMet, "human:kyle", strings.Repeat("x", MaxMarkNote+1), "longer than 600"},
	} {
		if _, err := s.MarkCriterion(ctx, tk.ID, tc.index, tc.mark, tc.actor, tc.note); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s by %s on %d: %v, want %q", tc.mark, tc.actor, tc.index, err, tc.want)
		}
	}
	if _, err := s.MarkCriterion(ctx, tk.ID, 1, MarkClaimMet, "a1", "TestAC2 passes"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkCriterion(ctx, tk.ID, 0, MarkWaived, "human:kyle", "out of scope for this release"); err != nil {
		t.Fatal(err)
	}
	marks, err := s.CriterionMarks(ctx, tk.ID)
	if err != nil || len(marks) != 2 {
		t.Fatalf("marks %+v %v", marks, err)
	}
	if m := marks[1]; m.Criterion != 0 || m.Mark != MarkWaived || m.Actor != "human:kyle" || m.SHA != CriterionSHA("AC1: Given a, when b, then c") || m.Note != "out of scope for this release" {
		t.Fatalf("waiver %+v", m)
	}
	if marks[0].SHA == marks[1].SHA {
		t.Fatal("two criteria have the same hash")
	}
	evs, _ := bus.Replay(ctx, 0, events.Filter{TaskID: tk.ID, Types: []string{"task.criterion"}}, 0)
	if len(evs) != 2 {
		t.Fatalf("task.criterion events %d", len(evs))
	}
}

// A note is redacted by the task service's scanner before it is stored.
func TestACriterionNoteIsRedacted(t *testing.T) {
	s, _ := newSvc(t)
	s.Redact = func(v string) string { return strings.ReplaceAll(v, "sk-live-123", "[REDACTED]") }
	ctx := context.Background()
	tk, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "t", Acceptance: []string{"Given a, when b, then c"}})
	m, err := s.MarkCriterion(ctx, tk.ID, 0, MarkMet, "human:kyle", "checked with key sk-live-123")
	if err != nil || strings.Contains(m.Note, "sk-live-123") {
		t.Fatalf("%+v %v", m, err)
	}
}
