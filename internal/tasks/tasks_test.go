package tasks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
)

func newSvc(t *testing.T) (*Service, *events.SQLBus) {
	t.Helper()
	db, err := storage.OpenTemp(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 1024)
	t.Cleanup(func() { bus.Close(); db.Close() })
	if _, err := db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','demo','/x',1)`); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: db, Bus: bus}, bus
}

func ptr[T any](v T) *T { return &v }

func TestStateMachineTable(t *testing.T) {
	type tr struct {
		from, to Status
		ok       bool
	}
	cases := []tr{
		{Backlog, Ready, true}, {Ready, Claimed, true}, {Claimed, InProgress, true},
		{InProgress, Blocked, true}, {InProgress, WaitingHuman, true}, {InProgress, Review, true},
		{Blocked, InProgress, true}, {WaitingHuman, InProgress, true},
		{Review, Verification, true}, {Review, InProgress, true},
		{Verification, Completed, true}, {Verification, InProgress, true},
		{Backlog, Failed, true}, {Review, Cancelled, true}, {Blocked, Failed, true},
		// not allowed
		{Backlog, InProgress, false}, {Ready, InProgress, false}, {Claimed, Review, false}, {InProgress, Completed, false},
		{Blocked, Review, false}, {WaitingHuman, Review, false}, {Review, Completed, false}, {Verification, Review, false},
		{Completed, InProgress, false}, {Failed, Ready, false}, {Cancelled, Backlog, false}, {Completed, Failed, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.ok {
			t.Errorf("%s -> %s: got %v want %v", c.from, c.to, got, c.ok)
		}
	}
	if len(Next(Completed)) != 0 {
		t.Error("terminal states have no successors")
	}
}

func driveToReview(t *testing.T, s *Service, id, assignee, reviewer string) {
	t.Helper()
	ctx := context.Background()
	mustT := func(to Status) {
		if _, err := s.Transition(ctx, id, to, TransitionOpts{By: "system"}); err != nil {
			t.Fatalf("-> %s: %v", to, err)
		}
	}
	s.Patch(ctx, id, Update{Assignee: &assignee, Reviewer: &reviewer})
	mustT(Ready)
	mustT(Claimed)
	mustT(InProgress)
}

func addPassingEvidence(t *testing.T, s *Service, id string) {
	t.Helper()
	ctx := context.Background()
	for _, e := range []Evidence{{Kind: "test", Passed: ptr(true), Summary: "12 passed"}, {Kind: "diff", Summary: "+10 -2"}, {Kind: "commit", Ref: "abc123"}} {
		if _, err := s.AddEvidence(ctx, id, e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReviewRequiresEvidenceAndDifferentReviewer(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	task, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Add subtract"})
	driveToReview(t, s, task.ID, "agent-1", "agent-1")

	// no evidence
	_, err := s.Transition(ctx, task.ID, Review, TransitionOpts{By: "agent-1"})
	var te *TransitionError
	if !errors.As(err, &te) || !strings.Contains(te.Reason, "evidence") {
		t.Fatalf("want evidence error, got %v", err)
	}
	// failing tests are not evidence
	s.AddEvidence(ctx, task.ID, Evidence{Kind: "test", Passed: ptr(false), Summary: "1 failed"})
	s.AddEvidence(ctx, task.ID, Evidence{Kind: "diff"})
	s.AddEvidence(ctx, task.ID, Evidence{Kind: "commit", Ref: "x"})
	if _, err := s.Transition(ctx, task.ID, Review, TransitionOpts{}); err == nil {
		t.Fatal("failing tests must not allow REVIEW")
	}
	s.AddEvidence(ctx, task.ID, Evidence{Kind: "test", Passed: ptr(true), Summary: "all pass"})
	// reviewer == assignee
	if _, err := s.Transition(ctx, task.ID, Review, TransitionOpts{}); err == nil || !strings.Contains(err.Error(), "different agent instance") {
		t.Fatalf("same reviewer must be refused, got %v", err)
	}
	rv := "agent-2"
	s.Patch(ctx, task.ID, Update{Reviewer: &rv})
	if _, err := s.Transition(ctx, task.ID, Review, TransitionOpts{}); err != nil {
		t.Fatal(err)
	}
}

func TestFullHappyPathNeedsHumanAcceptance(t *testing.T) {
	s, bus := newSvc(t)
	ctx := context.Background()
	task, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Feature", Acceptance: []string{"Given x When y Then z"}})
	driveToReview(t, s, task.ID, "agent-1", "agent-2")
	addPassingEvidence(t, s, task.ID)
	if _, err := s.Transition(ctx, task.ID, Review, TransitionOpts{}); err != nil {
		t.Fatal(err)
	}
	// verification needs a SHIP from a different reviewer
	if _, err := s.Transition(ctx, task.ID, Verification, TransitionOpts{}); err == nil {
		t.Fatal("no SHIP yet")
	}
	s.AddReview(ctx, task.ID, Review_{Reviewer: "agent-2", Verdict: "SHIP"}.r())
	if _, err := s.Transition(ctx, task.ID, Verification, TransitionOpts{}); err != nil {
		t.Fatal(err)
	}
	// an agent (or system) cannot complete it
	for _, by := range []string{"agent-2", "system", ""} {
		if _, err := s.Transition(ctx, task.ID, Completed, TransitionOpts{By: by}); err == nil {
			t.Fatalf("%q must not be able to accept a task", by)
		}
	}
	done, err := s.Transition(ctx, task.ID, Completed, TransitionOpts{By: "human:kyle"})
	if err != nil || done.Status != Completed || done.AcceptedBy != "human:kyle" || done.Completed == 0 {
		t.Fatalf("complete: %+v %v", done, err)
	}
	// terminal: nothing moves it
	if _, err := s.Transition(ctx, task.ID, InProgress, TransitionOpts{Feedback: "x"}); err == nil {
		t.Fatal("terminal state must be final")
	}
	evs, _ := bus.Replay(ctx, 0, events.Filter{TaskID: task.ID}, 0)
	got := map[string]bool{}
	for _, e := range evs {
		got[e.Type] = true
	}
	for _, want := range []string{"task.created", "task.ready", "task.claimed", "task.started", "task.review", "task.verification", "task.completed", "task.evidence"} {
		if !got[want] {
			t.Errorf("missing event %s (have %v)", want, got)
		}
	}
}

type Review_ struct {
	Reviewer, Verdict, Feedback string
}

func (r Review_) r() ReviewRecord {
	return ReviewRecord{Reviewer: r.Reviewer, Verdict: r.Verdict, Feedback: r.Feedback}
}

func TestRequestedChangesRequireFeedbackAndCountIterations(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	task, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Iter", MaxIterations: 2})
	driveToReview(t, s, task.ID, "agent-1", "agent-2")
	addPassingEvidence(t, s, task.ID)
	for i := 1; i <= 2; i++ {
		if _, err := s.Transition(ctx, task.ID, Review, TransitionOpts{}); err != nil {
			t.Fatalf("iteration %d review: %v", i, err)
		}
		if _, err := s.Transition(ctx, task.ID, InProgress, TransitionOpts{}); err == nil {
			t.Fatal("REVISE without feedback must be refused")
		}
		got, err := s.Transition(ctx, task.ID, InProgress, TransitionOpts{Feedback: "handle the nil case"})
		if i < 2 {
			if err != nil || got.Iteration != i || got.ReviewFeedback != "handle the nil case" {
				t.Fatalf("iteration %d: %+v %v", i, got, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "iteration limit") {
			t.Fatalf("limit not enforced: %v", err)
		}
	}
}

func TestDependenciesGateReadiness(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	a, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "A"})
	b, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "B", DependsOn: []string{a.ID}})
	if _, err := s.Transition(ctx, b.ID, Ready, TransitionOpts{}); err == nil || !strings.Contains(err.Error(), "dependency") {
		t.Fatalf("B must wait for A: %v", err)
	}
	if got, _ := s.PromoteReady(ctx, "p1"); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("only A should be promoted, got %v", got)
	}
	// complete A the long way
	driveToReview(t, s, a.ID, "agent-1", "agent-2")
	addPassingEvidence(t, s, a.ID)
	s.Transition(ctx, a.ID, Review, TransitionOpts{})
	s.AddReview(ctx, a.ID, ReviewRecord{Reviewer: "agent-2", Verdict: "SHIP"})
	s.Transition(ctx, a.ID, Verification, TransitionOpts{})
	if _, err := s.Transition(ctx, a.ID, Completed, TransitionOpts{By: "human:k"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PromoteReady(ctx, "p1"); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("B should now be promoted, got %v", got)
	}
}

// A task made from outside text (it carries the reference of its source) is never moved to READY by the scheduler: a human
// moves it on, and then it is an ordinary task.
func TestImportedTasksWaitForAHumanToMoveThemOn(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	own, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Ours"})
	imp, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "ENG-1: from Linear", IssueRef: "ENG-1"})
	if !imp.Imported() || own.Imported() {
		t.Fatal(imp.Imported(), own.Imported())
	}
	if got, _ := s.PromoteReady(ctx, "p1"); len(got) != 1 || got[0] != own.ID {
		t.Fatalf("only our own task is promoted, got %v", got)
	}
	if cur, _ := s.Get(ctx, imp.ID); cur.Status != Backlog {
		t.Fatalf("%s", cur.Status)
	}
	if _, err := s.Transition(ctx, imp.ID, Ready, TransitionOpts{By: "human:k"}); err != nil {
		t.Fatalf("a human moves it on: %v", err)
	}
}

func TestBlockedVersusWaitingHuman(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	task, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Blocky"})
	driveToReview(t, s, task.ID, "a1", "a2")
	b, err := s.Transition(ctx, task.ID, Blocked, TransitionOpts{Reason: "provider openrouter has no quota left"})
	if err != nil || b.BlockedReason == "" {
		t.Fatalf("%+v %v", b, err)
	}
	resumed, _ := s.Transition(ctx, task.ID, InProgress, TransitionOpts{})
	if resumed.BlockedReason != "" || resumed.Iteration != 0 {
		t.Fatalf("clearing a block must clear the reason and not count an iteration: %+v", resumed)
	}
	if _, err := s.Transition(ctx, task.ID, WaitingHuman, TransitionOpts{Reason: "watchdog"}); err != nil {
		t.Fatal(err)
	}
}

func TestFailedTaskBecomesDeadLetter(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	task, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Doomed", Template: "iterate-until-verified"})
	if _, err := s.Transition(ctx, task.ID, Failed, TransitionOpts{Reason: "agent crashed repeatedly"}); err != nil {
		t.Fatal(err)
	}
	var n int
	var reason string
	s.DB.Reader().QueryRow(`SELECT COUNT(*), MAX(reason) FROM dead_letters WHERE task_id=?`, task.ID).Scan(&n, &reason)
	if n != 1 || reason != "agent crashed repeatedly" {
		t.Fatalf("dead letters %d %q", n, reason)
	}
}

func TestConcurrentPatchesNeverLoseUpdates(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	task, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Race"})
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Patch(ctx, task.ID, Update{AddCost: 0.01}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _ := s.Get(ctx, task.ID)
	if got.CostUSD < 0.2499 || got.CostUSD > 0.2501 {
		t.Fatalf("cost %.4f, want 0.25 (lost updates)", got.CostUSD)
	}
	if got.Version < 26 {
		t.Fatalf("version %d", got.Version)
	}
}

func TestSequenceNumbersPerProjectAndHooks(t *testing.T) {
	s, _ := newSvc(t)
	var created, terminal []string
	s.Hooks = Hooks{OnCreate: func(_ context.Context, t Task) { created = append(created, t.ID) }, OnTerminal: func(_ context.Context, t Task) { terminal = append(terminal, t.ID) }}
	ctx := context.Background()
	a, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "one"})
	b, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "two"})
	if a.Seq != 1 || b.Seq != 2 {
		t.Fatalf("seq %d %d", a.Seq, b.Seq)
	}
	s.Transition(ctx, a.ID, Cancelled, TransitionOpts{By: "human:k"})
	if len(created) != 2 || len(terminal) != 1 || terminal[0] != a.ID {
		t.Fatalf("hooks created=%v terminal=%v", created, terminal)
	}
	if _, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "  "}); err == nil {
		t.Fatal("empty title must be refused")
	}
}

// A task reopened because the base branch moved needs no feedback and keeps its iteration count; only the system may do it.
func TestResyncReopensWithoutSpendingAnIteration(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	task, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Resync", MaxIterations: 2})
	driveToReview(t, s, task.ID, "agent-1", "agent-2")
	addPassingEvidence(t, s, task.ID)
	if _, err := s.Transition(ctx, task.ID, Review, TransitionOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, task.ID, InProgress, TransitionOpts{By: "human:kyle", Resync: true}); err == nil {
		t.Fatal("only the system may resync")
	}
	for i := 0; i < 3; i++ {
		got, err := s.Transition(ctx, task.ID, InProgress, TransitionOpts{By: "system", Reason: "main moved", Resync: true})
		if err != nil || got.Iteration != 0 {
			t.Fatalf("resync %d: %+v %v", i, got, err)
		}
		if _, err := s.Transition(ctx, task.ID, Review, TransitionOpts{}); err != nil {
			t.Fatal(err)
		}
	}
}
