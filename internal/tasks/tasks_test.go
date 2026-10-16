package tasks

import (
	"context"
	"errors"
	"fmt"
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

// A follow-up the team lead split off (origin lead:N) waits in BACKLOG for a person like an imported task does, whatever its
// dependencies say; the origin is a value only the lead's depths can take, and a person moving the task on makes it ordinary
// (audit 2026-10-16, M27).
func TestALeadsFollowUpWaitsForAHumanToMoveItOn(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	own, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Ours"})
	fol, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Split off", Origin: LeadOrigin(1)})
	if err != nil {
		t.Fatal(err)
	}
	if fol.Origin != "lead:1" || fol.LeadDepth() != 1 || !fol.WaitsForAHuman() || fol.Imported() || own.WaitsForAHuman() || own.LeadDepth() != 0 {
		t.Fatalf("origin %q depth %d waits %v imported %v; ours waits %v", fol.Origin, fol.LeadDepth(), fol.WaitsForAHuman(), fol.Imported(), own.WaitsForAHuman())
	}
	if got, _ := s.PromoteReady(ctx, "p1"); len(got) != 1 || got[0] != own.ID {
		t.Fatalf("only the task a person made is promoted, got %v", got)
	}
	if cur, _ := s.Get(ctx, fol.ID); cur.Status != Backlog || cur.Origin != "lead:1" {
		t.Fatalf("%s %q", cur.Status, cur.Origin)
	}
	if _, err := s.Transition(ctx, fol.ID, Ready, TransitionOpts{By: "human:k"}); err != nil {
		t.Fatalf("a person moves it on: %v", err)
	}
	for _, bad := range []string{"lead:0", "lead:3", "lead:-1", "lead:", "lead:1x", "lead:01", "lead: 1", "human:1", "x"} {
		if _, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "bad", Origin: bad}); err == nil {
			t.Errorf("the origin %q was accepted", bad)
		}
	}
	if got := (Task{Origin: "lead:9"}).LeadDepth(); got != 9 {
		t.Fatalf("a stored depth is read as it is: %d", got)
	}
	if got := (Task{Origin: "garbage"}).LeadDepth(); got != 0 {
		t.Fatalf("an origin that is not the lead's is depth %d", got)
	}
}

// The lead may have only so many unfinished follow-ups in a project: the count is taken in the transaction that inserts, and a
// follow-up that is finished or cancelled no longer counts.
func TestTheLeadsOpenFollowUpsAreCappedPerProject(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	if _, err := s.DB.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p2','other','/y',1)`); err != nil {
		t.Fatal(err)
	}
	var first Task
	for i := 0; i < MaxLeadOpen; i++ {
		fol, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: fmt.Sprintf("follow-up %d", i), Origin: LeadOrigin(1 + i%MaxLeadDepth)})
		if err != nil {
			t.Fatalf("follow-up %d: %v", i, err)
		}
		if i == 0 {
			first = fol
		}
	}
	if n, err := s.OpenFromLead(ctx, "p1"); err != nil || n != MaxLeadOpen {
		t.Fatalf("open %d %v", n, err)
	}
	if _, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "one too many", Origin: LeadOrigin(1)}); err == nil || !strings.Contains(err.Error(), "follow-ups open") {
		t.Fatalf("the follow-up past the limit: %v", err)
	}
	// a task a person makes is not limited, and another project has its own count
	if _, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "made by a person"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateReq{ProjectID: "p2", Title: "elsewhere", Origin: LeadOrigin(1)}); err != nil {
		t.Fatalf("another project: %v", err)
	}
	// cancelling one frees a place
	if _, err := s.Transition(ctx, first.ID, Cancelled, TransitionOpts{By: "human:k"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "fits now", Origin: LeadOrigin(1)}); err != nil {
		t.Fatalf("after one was cancelled: %v", err)
	}
	if _, err := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "over again", Origin: LeadOrigin(1)}); err == nil {
		t.Fatal("the limit holds again")
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

func TestCreateRefusesWhatATaskCannotBe(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	if _, err := s.DB.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p2','other','/y',1)`); err != nil {
		t.Fatal(err)
	}
	other, err := s.Create(ctx, CreateReq{ProjectID: "p2", Title: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]CreateReq{
		"no project":            {Title: "x"},
		"blank title":           {ProjectID: "p1", Title: "  "},
		"title too long":        {ProjectID: "p1", Title: strings.Repeat("t", MaxTitleLen+1)},
		"too many iterations":   {ProjectID: "p1", Title: "x", MaxIterations: MaxIterationsCap + 1},
		"priority out of range": {ProjectID: "p1", Title: "x", Priority: MaxPriority + 1},
		"unknown dependency":    {ProjectID: "p1", Title: "x", DependsOn: []string{"task_nope"}},
		"other project's task":  {ProjectID: "p1", Title: "x", DependsOn: []string{other.ID}},
	} {
		if _, err := s.Create(ctx, req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var n int
	_ = s.DB.Reader().QueryRow(`SELECT COUNT(*) FROM tasks WHERE project_id='p1'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d refused tasks were stored anyway", n)
	}
}

// Review feedback, the reason a task is blocked and the text of evidence come from an agent or a tool, and a task keeps them for
// good: with a scanner set they are scanned first (audit 2026-10-16, H7).
func TestWhatAnAgentWroteIsScannedBeforeATaskKeepsIt(t *testing.T) {
	s, _ := newSvc(t)
	const secret = "ghp_0123456789abcdefghijklmnopqrstuvwxyz"
	s.Redact = func(v string) string { return strings.ReplaceAll(v, secret, "[REDACTED]") }
	ctx := context.Background()
	task, _ := s.Create(ctx, CreateReq{ProjectID: "p1", Title: "Scan", MaxIterations: 3})
	driveToReview(t, s, task.ID, "agent-1", "agent-2")

	// evidence: the summary and every string of the data
	if _, err := s.AddEvidence(ctx, task.ID, Evidence{Kind: "test", Passed: ptr(false), Summary: "FAIL: token " + secret + " rejected",
		Data: []byte(`{"output":"curl -H 'Authorization: ` + secret + `' https://x","names":["TestA","` + secret + `"],"count":3}`)}); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Evidence(ctx, task.ID)
	if err != nil || len(ev) != 1 {
		t.Fatalf("%v %d", err, len(ev))
	}
	if strings.Contains(ev[0].Summary, secret) || strings.Contains(string(ev[0].Data), secret) {
		t.Fatalf("evidence kept the secret: %q %s", ev[0].Summary, ev[0].Data)
	}
	if !strings.Contains(ev[0].Summary, "[REDACTED]") || !strings.Contains(string(ev[0].Data), `"count":3`) || !strings.Contains(string(ev[0].Data), "TestA") {
		t.Fatalf("the text around the secret must stay: %q %s", ev[0].Summary, ev[0].Data)
	}

	// the reviewer's verdict
	if _, err := s.AddReview(ctx, task.ID, ReviewRecord{Attempt: 1, Reviewer: "agent-2", Verdict: "REVISE", Feedback: "remove the key " + secret + " from calc.go"}); err != nil {
		t.Fatal(err)
	}
	rv, err := s.Reviews(ctx, task.ID)
	if err != nil || len(rv) != 1 || strings.Contains(rv[0].Feedback, secret) || !strings.Contains(rv[0].Feedback, "remove the key [REDACTED]") {
		t.Fatalf("%v %+v", err, rv)
	}

	// the feedback that becomes the next iteration's context
	addPassingEvidence(t, s, task.ID)
	if _, err := s.Transition(ctx, task.ID, Review, TransitionOpts{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Transition(ctx, task.ID, InProgress, TransitionOpts{Feedback: "the key " + secret + " is in the diff"})
	if err != nil || strings.Contains(got.ReviewFeedback, secret) || !strings.Contains(got.ReviewFeedback, "[REDACTED]") {
		t.Fatalf("%v %q", err, got.ReviewFeedback)
	}

	// why the task is blocked
	b, err := s.Transition(ctx, task.ID, Blocked, TransitionOpts{Reason: "the provider answered 401 for " + secret})
	if err != nil || strings.Contains(b.BlockedReason, secret) || !strings.Contains(b.BlockedReason, "401 for [REDACTED]") {
		t.Fatalf("%v %q", err, b.BlockedReason)
	}

	// nothing in the database holds the secret
	for _, q := range []string{`SELECT summary || COALESCE(data,'') FROM task_evidence`, `SELECT feedback FROM task_reviews`,
		`SELECT review_feedback || blocked_reason FROM tasks`} {
		rows, err := s.DB.Reader().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(v, secret) {
				t.Fatalf("%s holds the secret: %q", q, v)
			}
		}
		rows.Close()
	}
}
