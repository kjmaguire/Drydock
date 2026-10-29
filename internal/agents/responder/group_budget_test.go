package responder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Decision P124: a group discussion's turns count toward the budgets of the workflow run that holds it (the run and the
// workflow scopes, as well as the project, task and agent ones a reply counts toward), even in a channel that has no task,
// and a budget at its limit parks the node on the budget pause instead of failing it. The budgets are the real store and the
// real Enforcer on the node (as the core wires them), the turns real processes of the labelled stand-in agent.

func TestAGroupDiscussionCountsTowardItsWorkflowBudgetAndPausesAtTheLimit(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("Use SQLite."))
	b := h.agent("agt_b", "Bo", "backend", say("No: use Postgres."))
	rm := h.channel("design", a, b)
	store := &budget.Store{DB: h.db}
	// the hook the core gives the responder (EconomicsBudgetBlockFor): every scope of the turn, a person's approval honoured
	h.budget = func(ctx context.Context, s responder.BudgetScope, ag responder.Agent) error {
		sts, err := store.StatusFor(ctx, budget.ScopeIDs{Project: s.ProjectID, Task: s.TaskID, Run: s.RunID, Workflow: s.Workflow, Agent: ag.ID})
		if err != nil || budget.ApprovedOverLimit(ctx) {
			return err
		}
		for _, st := range sts {
			if st.State == budget.StateExceeded {
				return &budget.PauseError{Over: []budget.Status{st}, Text: "the " + st.Message + ", so agents do not answer"}
			}
		}
		return nil
	}
	if _, err := store.Set(h.ctx, budget.Budget{Scope: budget.ScopeWorkflow, ScopeID: "group-test", ProjectID: h.project, LimitTurns: ptr(int64(2)), Enabled: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	eng, _ := h.groupEngine(rm, []string{a, b}, 3)
	eng.Use(workflow.KindGroup, (&budget.Enforcer{Store: store, Bus: h.bus, Consume: eng.MarkConsumed}).Middleware())
	id, err := eng.Start(h.ctx, "group-test", "Which database?", workflow.StartOptions{ProjectID: h.project})
	if err != nil {
		t.Fatal(err)
	}

	// round 1 (two turns) takes the workflow to its limit of two turns: the third turn does not start, the run waits
	pend := h.awaitPause(eng, id)
	if !strings.Contains(pend.Spec.Title, "workflow group-test budget") {
		t.Fatalf("pause %+v", pend.Spec)
	}
	var p map[string]any
	_ = json.Unmarshal(pend.Spec.Payload, &p)
	if p["gate"] != budget.PauseGate || p["node"] != "discuss" {
		t.Fatalf("the pause is the budget gate's: %s", pend.Spec.Payload)
	}
	h.assertCounted(id, 2)
	thread := h.discussionThread(rm)

	// one approval lets one more turn through, in the same thread, then the limit holds again
	if err := eng.Resume(h.ctx, id, workflow.Response{RequestID: pend.ID, Decision: "approved", By: "human:kyle"}); err != nil {
		t.Fatal(err)
	}
	again := h.awaitPause(eng, id)
	if again.ID == pend.ID {
		t.Fatal("the same request")
	}
	h.assertCounted(id, 3)
	if got := h.discussionThread(rm); got != thread {
		t.Fatalf("the discussion goes on in its thread: %s, then %s", thread, got)
	}
	ms, _ := h.room.Thread(h.ctx, thread)
	var said []string
	for _, m := range ms {
		if m.Author.Kind == room.AuthorAgent {
			said = append(said, m.Author.ID)
		}
	}
	if strings.Join(said, ",") != strings.Join([]string{a, b, a}, ",") {
		t.Fatalf("turns %v", said)
	}

	// a rejection ends the run with the discussion where it was
	if err := eng.Resume(h.ctx, id, workflow.Response{RequestID: again.ID, Decision: "rejected", By: "human:kyle"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	defer cancel()
	info, err := eng.Await(ctx, id, func(i workflow.RunInfo) bool { return i.Status.Terminal() })
	if err != nil || info.Status != workflow.StatusFailed || !strings.Contains(info.FailedReason, "rejected") {
		t.Fatalf("run %s: %s (%v)", info.Status, info.FailedReason, err)
	}
	h.assertCounted(id, 3)
}

// A budget already at its limit before the discussion starts holds the node before its topic is posted (the Enforcer on the
// node), as it holds an agent turn.
func TestAGroupDiscussionDoesNotStartOverItsRunBudget(t *testing.T) {
	h := newHarness(t, responder.Options{})
	a := h.agent("agt_a", "Ada", "architect", say("x"))
	b := h.agent("agt_b", "Bo", "backend", say("y"))
	rm := h.channel("design", a, b)
	store := &budget.Store{DB: h.db}
	if _, err := store.Set(h.ctx, budget.Budget{Scope: budget.ScopeWorkflow, ScopeID: "group-test", ProjectID: h.project, LimitTurns: ptr(int64(1)), Enabled: true}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	eng, _ := h.groupEngine(rm, []string{a, b}, 3)
	eng.Use(workflow.KindGroup, (&budget.Enforcer{Store: store, Bus: h.bus, Consume: eng.MarkConsumed}).Middleware())
	// an earlier run of the workflow used up its one turn
	h.db.Writer().Exec(`INSERT INTO workflow_runs(id,workflow,status,project_id,task_id,topology_hash,created_at,updated_at) VALUES('run_old','group-test','COMPLETED',?,'','h',1,1)`, h.project)
	h.db.Writer().Exec(`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at,workflow_run_id) VALUES('ar_old',?,'','STOPPED',1,'run_old')`, a)
	id, err := eng.Start(h.ctx, "group-test", "Which database?", workflow.StartOptions{ProjectID: h.project})
	if err != nil {
		t.Fatal(err)
	}
	pend := h.awaitPause(eng, id)
	if !strings.Contains(pend.Spec.Title, "workflow group-test budget") {
		t.Fatalf("%+v", pend.Spec)
	}
	if ms := h.messages(rm); len(ms) != 0 {
		t.Fatalf("nothing was posted: %s", authors(ms))
	}
}

func ptr[T any](v T) *T { return &v }

// awaitPause waits for run to wait on one budget pause and returns it.
func (h *harness) awaitPause(eng *workflow.Engine, run workflow.RunID) workflow.PendingRequest {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 60*time.Second)
	defer cancel()
	info, err := eng.Await(ctx, run, func(i workflow.RunInfo) bool { return i.Status == workflow.StatusWaitingHuman || i.Status.Terminal() })
	if err != nil || info.Status != workflow.StatusWaitingHuman {
		h.t.Fatalf("run %s (%s): %v", info.Status, info.FailedReason, err)
	}
	ps, err := eng.Pending(h.ctx, run)
	if err != nil || len(ps) != 1 {
		h.t.Fatalf("pending %+v %v", ps, err)
	}
	return ps[0]
}

// assertCounted checks that the run has n agent sessions and their usage counted toward it and toward its workflow's budget.
func (h *harness) assertCounted(run workflow.RunID, n int) {
	h.t.Helper()
	var sessions, usage int
	h.db.Reader().QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE workflow_run_id=?`, string(run)).Scan(&sessions)
	h.db.Reader().QueryRow(`SELECT COUNT(DISTINCT agent_run_id) FROM model_usage WHERE run_id=?`, string(run)).Scan(&usage)
	if sessions != n || usage != n {
		h.t.Fatalf("sessions %d, usage of %d sessions, want %d", sessions, usage, n)
	}
	for _, sc := range []struct {
		scope budget.Scope
		id    string
	}{{budget.ScopeRun, string(run)}, {budget.ScopeWorkflow, h.project + "/group-test"}} {
		sp, err := budget.Usage(h.ctx, h.db, sc.scope, sc.id)
		if err != nil || sp.Turns != int64(n) || sp.Tokens() == 0 {
			h.t.Fatalf("%s %s: %+v %v", sc.scope, sc.id, sp, err)
		}
	}
}

// discussionThread is the id of the room's one discussion topic.
func (h *harness) discussionThread(rm room.Room) string {
	h.t.Helper()
	var ids []string
	for _, m := range h.messages(rm) {
		if m.Type == room.TypeSystem && m.PayloadString("event") == "group_discussion" {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) != 1 {
		h.t.Fatalf("%d discussions: %s", len(ids), fmt.Sprint(ids))
	}
	return ids[0]
}
