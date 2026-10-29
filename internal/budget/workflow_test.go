package budget_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/budget"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The workflow scope (decision P118, token-control rule 14): a budget on every run of one workflow, in one project or in all.

func TestAWorkflowBudgetNamesOneWorkflowAndKeepsItsProject(t *testing.T) {
	e := newEnv(t)
	for name, b := range map[string]budget.Budget{
		"every workflow": {Scope: budget.ScopeWorkflow, ScopeID: budget.Any, ProjectID: "prj1", LimitUSD: f(1)},
		"not a name":     {Scope: budget.ScopeWorkflow, ScopeID: "prj1/iuv", LimitUSD: f(1)},
	} {
		if _, err := e.st.Set(e.ctx, b, "human:kyle"); !errors.Is(err, budget.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	b := e.set(budget.Budget{Scope: budget.ScopeWorkflow, ScopeID: "iuv", ProjectID: "prj1", LimitUSD: f(1), Enabled: true})
	if b.ProjectID != "prj1" || budget.InstanceOf(b) != "prj1/iuv" {
		t.Fatalf("a workflow budget keeps its project: %+v (instance %q)", b, budget.InstanceOf(b))
	}
	all := e.set(budget.Budget{Scope: budget.ScopeWorkflow, ScopeID: "iuv", LimitUSD: f(1), Enabled: true})
	if budget.InstanceOf(all) != "iuv" {
		t.Fatalf("instance %q", budget.InstanceOf(all))
	}
	// the project's budget list shows it, between the project's own and the tasks'
	e.set(budget.Budget{Scope: budget.ScopeProject, ScopeID: "prj1", LimitUSD: f(9), Enabled: true})
	e.set(budget.Budget{Scope: budget.ScopeTask, ScopeID: budget.Any, ProjectID: "prj1", LimitUSD: f(9), Enabled: true})
	bs, err := e.st.List(e.ctx, budget.Filter{ProjectID: "prj1"})
	if err != nil || len(bs) != 3 || bs[0].Scope != budget.ScopeProject || bs[1].Scope != budget.ScopeWorkflow || bs[2].Scope != budget.ScopeTask {
		t.Fatalf("list %+v %v", bs, err)
	}
}

func TestAWorkflowBudgetCountsEveryRunOfItsWorkflowAndNothingElse(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.secondTask()
	e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,assignee,reviewer,created_at,updated_at) VALUES('t3','prj2',1,'t3','IN_PROGRESS','agtOther','',1,1)`)
	e.exec(`INSERT INTO workflow_runs(id,workflow,status,project_id,task_id,topology_hash,created_at,updated_at) VALUES
		('r1','iuv','COMPLETED','prj1','t1','h',1000,1000),
		('r2','other','COMPLETED','prj1','t1','h',5000,5000),
		('r3','iuv','RUNNING','prj1','t2','h',1000,1000),
		('r4','iuv','RUNNING','prj2','t3','h',1000,1000)`)
	e.usage(row{task: "t1", agent: "agtW", in: 1, cost: 0.01, ts: 1500})      // r1 (iuv) by its window
	e.usage(row{task: "t1", agent: "agtW", in: 2, cost: 0.02, ts: 5500})      // r2 (other): not iuv
	e.usage(row{task: "t2", agent: "agtW", in: 4, cost: 0.04, ts: 1500})      // r3 (iuv)
	e.usage(row{task: "", agent: "agtW", in: 8, cost: 0.08, run: "r3"})       // a row that names its run
	e.usage(row{task: "t3", agent: "agtOther", in: 16, cost: 0.16, ts: 1500}) // r4: iuv in the other project
	e.exec(`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at) VALUES('a1','agtW','t1','STOPPED',1500),('a2','agtW','t1','STOPPED',5500),('a3','agtOther','t3','STOPPED',1500)`)
	inProject, err := budget.Usage(e.ctx, e.db, budget.ScopeWorkflow, "prj1/iuv")
	if err != nil || inProject.Tokens() != 13 || inProject.Turns != 1 {
		t.Fatalf("iuv in prj1: %+v %v", inProject, err)
	}
	everywhere, _ := budget.Usage(e.ctx, e.db, budget.ScopeWorkflow, "iuv")
	if everywhere.Tokens() != 29 || everywhere.Turns != 2 {
		t.Fatalf("iuv everywhere: %+v", everywhere)
	}
	// a budget on the workflow in prj1 applies to a turn of an iuv run there, not to one of another workflow or project
	e.set(budget.Budget{Scope: budget.ScopeWorkflow, ScopeID: "iuv", ProjectID: "prj1", LimitTokens: i(10), Enabled: true})
	sts, err := e.st.StatusFor(e.ctx, budget.ScopeIDs{Project: "prj1", Task: "t2", Run: "r3", Workflow: "iuv"})
	if err != nil || len(sts) != 1 || sts[0].InstanceID != "prj1/iuv" || sts[0].State != budget.StateExceeded {
		t.Fatalf("status %+v %v", sts, err)
	}
	if !strings.HasPrefix(sts[0].Message, "workflow iuv budget at 130%") {
		t.Fatalf("message %q", sts[0].Message)
	}
	for _, ids := range []budget.ScopeIDs{{Project: "prj1", Task: "t1", Run: "r2", Workflow: "other"}, {Project: "prj2", Task: "t3", Run: "r4", Workflow: "iuv"}} {
		if sts, _ := e.st.StatusFor(e.ctx, ids); len(sts) != 0 {
			t.Fatalf("%+v: %+v", ids, sts)
		}
	}
}

func TestAWorkflowBudgetPausesTheNextTurnOfItsWorkflowOnly(t *testing.T) {
	r := newRig(t)
	r.secondTask()
	r.set(budget.Budget{Scope: budget.ScopeWorkflow, ScopeID: "spendy", ProjectID: "prj1", LimitTokens: i(1000), Enabled: true})
	r.graph("spendy", 1200, 0.5) // the worker alone takes the workflow past its limit
	r.graph("calm", 1200, 0.5)
	run := r.start("spendy")
	info := r.wait(run, workflow.StatusWaitingHuman)
	if r.turns.Load() != 1 {
		t.Fatalf("the reviewer turn must not have started: %d turns", r.turns.Load())
	}
	pend, _ := r.eng.Pending(r.ctx, info.ID)
	if len(pend) != 1 || pend[0].NodeID != "review" || !strings.Contains(pend[0].Spec.Title, "workflow spendy budget") {
		t.Fatalf("%+v", pend)
	}
	// a run of another workflow in the same project is not held by it
	other, err := r.eng.Start(r.ctx, "calm", nil, workflow.StartOptions{ProjectID: "prj1", TaskID: "t2"})
	if err != nil {
		t.Fatal(err)
	}
	r.wait(other, workflow.StatusCompleted)
	if r.turns.Load() != 3 {
		t.Fatalf("the other workflow's two turns ran: %d turns", r.turns.Load())
	}
}

func TestAWorkflowBudgetHoldsWhatItsTurnsInFlightReserved(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.secondTask()
	e.set(budget.Budget{Scope: budget.ScopeWorkflow, ScopeID: "iuv", ProjectID: "prj1", LimitUSD: f(1), Enabled: true})
	// two runs of the workflow on two tasks: no task, run or agent budget is shared, only the workflow's
	if _, err := e.st.Reserve(e.ctx, budget.Hold{Project: "prj1", Task: "t1", Run: "r1", Agent: "agtW", Workflow: "iuv", EstUSD: 0.7}); err != nil {
		t.Fatal(err)
	}
	_, err := e.st.Reserve(e.ctx, budget.Hold{Project: "prj1", Task: "t2", Run: "r2", Agent: "agtR", Workflow: "iuv", EstUSD: 0.7})
	var re *budget.RefusedError
	if !errors.As(err, &re) || !strings.Contains(re.Message, "workflow iuv budget has $0.3000 left after $0.0000 spent and $0.7000 held by 1 turn(s) in flight") {
		t.Fatalf("the second turn of the workflow does not fit: %v", err)
	}
	// a turn of another workflow is not held by it
	if _, err := e.st.Reserve(e.ctx, budget.Hold{Project: "prj1", Task: "t2", Run: "r3", Agent: "agtR", Workflow: "bug", EstUSD: 0.7}); err != nil {
		t.Fatalf("another workflow: %v", err)
	}
}

// A session that names the workflow run it belongs to (a turn of a group discussion, decision P124) counts toward that run and
// its workflow alone: in a channel it has no task, and in the task's room it does not fall into another run's window.
func TestASessionThatNamesItsRunCountsTowardThatRunAlone(t *testing.T) {
	e := newEnv(t)
	e.task("t1", "prj1", "agtW", "agtR")
	e.exec(`INSERT INTO workflow_runs(id,workflow,status,project_id,task_id,topology_hash,created_at,updated_at) VALUES
		('r1','iuv','RUNNING','prj1','t1','h',1000,1000),
		('r2','design','RUNNING','prj1','','h',1200,1200)`)
	e.exec(`INSERT INTO agent_runs(id,agent_id,task_id,status,started_at,workflow_run_id) VALUES
		('a1','agtW','t1','STOPPED',1500,''),
		('a2','agtW','','STOPPED',1500,'r2'),
		('a3','agtR','t1','STOPPED',1600,'r2')`)
	e.usage(row{task: "", agent: "agtW", in: 3, run: "r2", ts: 1500})
	e.usage(row{task: "t1", agent: "agtR", in: 5, run: "r2", ts: 1600})
	e.usage(row{task: "t1", agent: "agtW", in: 7, ts: 1500})
	for _, c := range []struct {
		scope         budget.Scope
		id            string
		turns, tokens int64
	}{
		{budget.ScopeRun, "r1", 1, 7},
		{budget.ScopeRun, "r2", 2, 8},
		{budget.ScopeWorkflow, "prj1/design", 2, 8},
		{budget.ScopeWorkflow, "prj1/iuv", 1, 7},
		{budget.ScopeTask, "t1", 2, 12},
	} {
		sp, err := budget.Usage(e.ctx, e.db, c.scope, c.id)
		if err != nil || sp.Turns != c.turns || sp.Tokens() != c.tokens {
			t.Errorf("%s %s: %+v %v (want %d turns, %d tokens)", c.scope, c.id, sp, err, c.turns, c.tokens)
		}
	}
}

// secondTask adds a second task to prj1 (task gives every task the number 1).
func (e *env) secondTask() {
	e.exec(`INSERT INTO tasks(id,project_id,seq,title,status,assignee,reviewer,created_at,updated_at) VALUES('t2','prj1',2,'t2','IN_PROGRESS','agtW','agtR',1,1)`)
}
