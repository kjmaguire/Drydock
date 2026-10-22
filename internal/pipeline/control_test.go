package pipeline_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// A person's request (lead.split): the lead is asked with trigger "requested" and its split is applied as any split, posted
// and stored; the requests do not count toward the bound of interventions per run.
func TestARequestedSplitRunsTheLeadsSplitOutsideTheBound(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{agLead: leadSays(splitAnswer)}, true)
	ctx := context.Background()
	for i := 1; i <= 2; i++ {
		o, err := e.lead.RequestSplit(ctx, e.task.ID, "human:kyle")
		if err != nil || !o.Intervened || o.Decision.Action != pipeline.LeadSplit || len(o.CreatedTasks) != 2 {
			t.Fatalf("request %d: %+v %v", i, o, err)
		}
	}
	lp := e.prompts(agLead)
	if len(lp) != 2 || !strings.Contains(lp[0], "Trigger: requested") || !strings.Contains(lp[0], "a person asked you to split this task") || !strings.Contains(lp[0], "kyle asked you to split") {
		t.Fatalf("lead prompts %q", lp)
	}
	iv := e.interventions(t)
	if len(iv) != 2 || iv[0].Trigger != pipeline.TriggerRequested || iv[0].Action != pipeline.LeadSplit {
		t.Fatalf("interventions %+v", iv)
	}
	// requests are not interventions the bound counts: the triggers still have all of theirs
	if !e.lead.HasLead(ctx, e.task.ID, e.task.RunID, "work") {
		t.Fatal("a person's requests used up the lead's bound")
	}
	e.mu.Lock()
	if len(e.posts) != 2 || !strings.Contains(e.posts[0], "Team lead: split (a person asked you to split this task)") {
		t.Fatalf("posts %q", e.posts)
	}
	e.mu.Unlock()
}

// The lead must answer a request with split or escalate: a nudge is refused by the validator and asked again.
func TestARequestedSplitAcceptsOnlySplitOrEscalate(t *testing.T) {
	p, _, problems := pipeline.ParseLeadDecision(`{"action":"nudge","message":"keep going","reason":"fine as is"}`, pipeline.LeadCheck{Trigger: pipeline.TriggerRequested})
	if len(problems) != 1 || !strings.Contains(problems[0], "a person asked you to split this task") {
		t.Fatalf("nudge on a request: %+v %q", p, problems)
	}
	if _, _, problems := pipeline.ParseLeadDecision(`{"action":"escalate","message":"It is one small change.","reason":"too small to split"}`, pipeline.LeadCheck{Trigger: pipeline.TriggerRequested}); len(problems) != 0 {
		t.Fatalf("escalate on a request: %q", problems)
	}
	e := newLeadEnv(t, map[string]fakeagent.Script{agLead: leadSays(`{"action":"escalate","message":"It is one small change; splitting adds nothing.","reason":"too small"}`)}, true)
	o, err := e.lead.RequestSplit(context.Background(), e.task.ID, "human:kyle")
	if err != nil || o.Decision.Action != pipeline.LeadEscalate || len(o.CreatedTasks) != 0 {
		t.Fatalf("an escalation: %+v %v", o, err)
	}
}

// Without a lead on the team a request is refused with ErrNoLead, and a finished task cannot be split.
func TestARequestedSplitNeedsALeadAndAnOpenTask(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{}, false)
	if _, err := e.lead.RequestSplit(context.Background(), e.task.ID, "human:kyle"); !errors.Is(err, pipeline.ErrNoLead) {
		t.Fatalf("without a lead: %v", err)
	}
	e2 := newLeadEnv(t, map[string]fakeagent.Script{agLead: leadSays(splitAnswer)}, true)
	if _, err := e2.tasks.Transition(context.Background(), e2.task.ID, tasks.Cancelled, tasks.TransitionOpts{By: "human:kyle"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.lead.RequestSplit(context.Background(), e2.task.ID, "human:kyle"); err == nil || !strings.Contains(err.Error(), "nothing left to split") {
		t.Fatalf("a cancelled task: %v", err)
	}
	if len(e2.prompts(agLead)) != 0 {
		t.Fatal("no turn for a refused request")
	}
}

// A task a person owns: the scheduler never starts it (even with a reviewer set), and the lead's monitor does not try to
// staff it under automatic scheduling.
func TestATaskAPersonOwnsIsLeftAloneByTheSchedulerAndTheLead(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{agLead: leadSays(`{"action":"reassign","agent":"Backend #2","message":"take it","reason":"free"}`)}, true)
	ctx := context.Background()
	e.tasks.Transition(ctx, e.task.ID, tasks.Cancelled, tasks.TransitionOpts{By: "human:test"}) // only the task below is in play
	me := "human:kyle"
	mine, _ := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Write the release notes", Description: "notes"})
	if _, err := e.tasks.Patch(ctx, mine.ID, tasks.Update{Assignee: &me}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Transition(ctx, mine.ID, tasks.Ready, tasks.TransitionOpts{By: me}); err != nil {
		t.Fatal(err)
	}
	started := 0
	s := pipeline.NewScheduler(pipeline.SchedulerDeps{Tasks: e.tasks, DB: e.db, Bus: e.bus,
		Dev:   func(context.Context, string) pipeline.DevSettings { return pipeline.DevSettings{MaxParallel: 2} },
		Start: func(context.Context, tasks.Task) (workflow.RunID, error) { started++; return "run_x", nil }})
	res, err := s.Tick(ctx, "p1")
	if err != nil || started != 0 || len(res.Skipped) != 1 || res.Skipped[0].TaskID != mine.ID || !strings.Contains(res.Skipped[0].Reason, "a person owns this task") {
		t.Fatalf("tick %+v %v (started %d)", res, err, started)
	}
	rv := agReview
	e.tasks.Patch(ctx, mine.ID, tasks.Update{Reviewer: &rv})
	if res, _ := s.Tick(ctx, "p1"); started != 0 || len(res.Skipped) != 1 {
		t.Fatalf("with a reviewer: %+v (started %d)", res, started)
	}
	e.devs.AutoSchedule = true
	if res, err := e.lead.Tick(ctx, "p1"); err != nil || len(res.Unassigned) != 0 {
		t.Fatalf("the lead looked for a worker for a person's task: %+v %v", res, err)
	}
	if got, _ := e.tasks.Get(ctx, mine.ID); got.Assignee != me {
		t.Fatalf("assignee %q", got.Assignee)
	}
}

// WouldWait says why a task started now would wait: an earlier task under way that changes the same files (named), or the
// project's parallel-agent limit; nil when it would start.
func TestWouldWaitNamesTheTaskItWaitsFor(t *testing.T) {
	e := newLeadEnv(t, map[string]fakeagent.Script{}, false)
	ctx := context.Background()
	max := 1
	s := pipeline.NewScheduler(pipeline.SchedulerDeps{Tasks: e.tasks, DB: e.db, Bus: e.bus,
		Dev: func(context.Context, string) pipeline.DevSettings { return pipeline.DevSettings{MaxParallel: max} }})
	next, _ := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Add Multiply", Description: "x", Files: []string{"calc.go"}})
	if w, err := s.WouldWait(ctx, next); err != nil || w != nil {
		t.Fatalf("nothing is under way: %+v %v", w, err)
	}
	// the harness task is under way on calc.go
	if _, err := e.db.Writer().Exec(`UPDATE tasks SET status='IN_PROGRESS', started_at=1, files=?, version=version+1 WHERE id=?`, `["calc.go"]`, e.task.ID); err != nil {
		t.Fatal(err)
	}
	w, err := s.WouldWait(ctx, next)
	if err != nil || w == nil || w.Behind == nil || w.Behind.ID != e.task.ID || !strings.Contains(w.Reason, `#1 "Implement Subtract"`) {
		t.Fatalf("collision: %+v %v", w, err)
	}
	other, _ := e.tasks.Create(ctx, tasks.CreateReq{ProjectID: "p1", Title: "Add Divide", Description: "x", Files: []string{"div.go"}})
	if w, _ := s.WouldWait(ctx, other); w != nil {
		t.Fatalf("other files, no run: %+v", w)
	}
	if _, err := e.db.Writer().Exec(`INSERT INTO workflow_runs(id,workflow,status,project_id,task_id,topology_hash,created_at,updated_at) VALUES('run_busy','x','RUNNING','p1',?,'t',1,1)`, e.task.ID); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.WouldWait(ctx, other); w == nil || w.Behind != nil || !strings.Contains(w.Reason, "limit of 1 parallel agents") {
		t.Fatalf("at the limit: %+v", w)
	}
	max = 2
	if w, _ := s.WouldWait(ctx, other); w != nil {
		t.Fatalf("room again: %+v", w)
	}
}
