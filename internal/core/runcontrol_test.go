package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The run page's Resume, Time travel and Replay (P123) through the methods the UI calls. Every agent is the scripted stand-in
// (fakeagent: a labelled test double).

func (c *crew) explain(taskID, runID string) core.RunExplanation {
	c.a.t.Helper()
	var x core.RunExplanation
	c.a.call("run.explain", map[string]string{"taskId": taskID, "runId": runID}, &x)
	return x
}

// stored is what the database holds of a run: its checkpoints (state included), node executions and answered approvals (a
// request still open is cancelled with the run when a fork stops it, as any cancel does).
func (c *crew) stored(runID string) string {
	c.a.t.Helper()
	var out []string
	q := func(sql string) {
		rows, err := c.a.c.DB.Reader().Query(sql, runID)
		if err != nil {
			c.a.t.Fatal(err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			b, _ := json.Marshal(vals)
			out = append(out, string(b))
		}
	}
	q(`SELECT id, step, kind, parent_id, state, created_at FROM workflow_checkpoints WHERE run_id=? ORDER BY rowid`)
	q(`SELECT node_id, step, attempt, status, output, error, safe_point_ref FROM node_executions WHERE run_id=? ORDER BY rowid`)
	q(`SELECT id, status, decision, decided_by FROM approvals WHERE run_id=? AND status NOT IN ('REQUESTED','CANCELLED') ORDER BY rowid`)
	return strings.Join(out, "\n")
}

// Time travel from the checkpoint after prepare: the task's waiting run is stopped and kept as it was, a new run goes on from
// there with the task back in progress, reaches the approval again and merges on the person's answer. A replay of the earlier
// run then runs it again from its entry, asking at the approval as any run does.
func TestTimeTravelAndReplayOfATaskRun(t *testing.T) {
	c := newCrew(t, fakeagent.Script{Rules: []fakeagent.Rule{reviewerShips}, Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: impl}}, {Say: "done"}}})
	a := c.a
	tk := c.task("Implement Subtract")
	first := c.assignAndStart(tk)
	c.awaitApproval(tk.ID)

	x := c.explain(tk.ID, first)
	if x.Actions == nil || !x.Actions.TimeTravel || x.Actions.Supersedes != first || x.Actions.Resume || x.Actions.Replay {
		t.Fatalf("a waiting run can be forked from (stopping it), not resumed or replayed: %+v", x.Actions)
	}
	if !strings.Contains(x.Actions.ReplayWhy, "ended") || !strings.Contains(x.Actions.ResumeWhy, "waiting for you") {
		t.Fatalf("the page says why: %+v", x.Actions)
	}
	var afterPrepare core.CheckpointView
	for _, cp := range x.Checkpoints {
		if cp.Step == 1 && cp.Kind == "step" {
			afterPrepare = cp
		}
	}
	if afterPrepare.ID == "" || strings.Join(afterPrepare.Next, ",") != "work" {
		t.Fatalf("the checkpoint after prepare names what runs next: %+v", afterPrepare)
	}
	before := c.stored(first)
	mustErr(t, a.callErr("run.timeTravel", map[string]string{"taskId": tk.ID, "runId": first}), "choose the checkpoint")
	mustErr(t, a.callErr("run.replay", map[string]string{"taskId": tk.ID, "runId": first}), "Only a run that has ended")

	var res core.RunControlResult
	a.call("run.timeTravel", map[string]string{"taskId": tk.ID, "runId": first, "checkpointId": afterPrepare.ID}, &res)
	if res.RunID == "" || res.RunID == first || res.Superseded != first || res.TaskStatus != string(tasks.InProgress) || res.Note != "" {
		t.Fatalf("result %+v", res)
	}
	if got := c.detail(tk.ID).Task.RunID; got != res.RunID {
		t.Fatalf("the task's run is the fork: %s", got)
	}
	old, _ := a.c.Engine.Status(context.Background(), workflow.RunID(first))
	if old.Status != workflow.StatusCancelled {
		t.Fatalf("the waiting run was stopped: %s", old.Status)
	}
	if after := c.stored(first); after != before {
		t.Fatalf("the earlier run's history changed:\n%s\n---\n%s", before, after)
	}
	d := c.awaitApproval(tk.ID)
	fork := c.explain(tk.ID, res.RunID)
	if fork.Origin == nil || fork.Origin.Kind != workflow.OriginFork || string(fork.Origin.Run) != first || fork.Origin.Step != 1 || fork.Origin.By != "human:kyle" {
		t.Fatalf("origin %+v", fork.Origin)
	}
	if len(fork.Runs) != 2 {
		t.Fatalf("the task ran twice: %+v", fork.Runs)
	}
	for _, e := range fork.Executions {
		if e.NodeID == "prepare" {
			t.Fatal("prepare ran before the checkpoint: the fork does not run it again")
		}
	}
	if c.eventCount(tk.ID, "task.run_forked") != 1 {
		t.Fatal("the fork is announced on the task")
	}

	// the earlier run (cancelled now) is replayed from its entry: the fork, waiting, is stopped
	again := c.explain(tk.ID, first)
	if !again.Actions.Replay || again.Actions.Supersedes != res.RunID || again.Actions.Resume {
		t.Fatalf("the ended run can be replayed: %+v", again.Actions)
	}
	var rep core.RunControlResult
	a.call("run.replay", map[string]string{"taskId": tk.ID, "runId": first}, &rep)
	if rep.Superseded != res.RunID || rep.RunID == "" {
		t.Fatalf("replay %+v", rep)
	}
	d = c.awaitApproval(tk.ID)
	rx := c.explain(tk.ID, rep.RunID)
	if rx.Origin == nil || rx.Origin.Kind != workflow.OriginReplay || rx.Origin.Step != 0 {
		t.Fatalf("replay origin %+v", rx.Origin)
	}
	a.call("run.respond", map[string]any{"runId": rep.RunID, "requestId": d.Pending[0].ID, "decision": "approved"}, nil)
	a.waitFor("the merge", func() bool { return c.detail(tk.ID).Task.Status == tasks.Completed })
	var asked int
	_ = a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM approvals WHERE run_id=?`, rep.RunID).Scan(&asked)
	if asked != 1 {
		t.Fatalf("the approval just before the merge is its fresh approval: the replay asked %d times", asked)
	}
	done := c.explain(tk.ID, rep.RunID)
	if done.Actions.TimeTravel || done.Actions.Replay || done.Actions.Resume || !strings.Contains(done.Actions.ReplayWhy, "completed") {
		t.Fatalf("a completed task is not run again: %+v", done.Actions)
	}
	mustErr(t, a.callErr("run.replay", map[string]string{"taskId": tk.ID, "runId": rep.RunID}), "completed")
}

// A run that failed goes on from its last good checkpoint on a person's word, under the same approvals: a budget pause the
// person rejected stays rejected, so the run fails again without starting a turn.
func TestResumeOfAFailedTaskRunKeepsItsApprovals(t *testing.T) {
	a := open(t, "")
	s := a.econSetup()
	a.call("budget.set", map[string]any{"scope": "project", "scopeId": s.project.ID, "limitTokens": 1}, nil)
	var runID string
	a.call("task.start", map[string]string{"id": s.task.ID}, &runID)
	d := a.econPending(s.task.ID, 1)
	mustErr(t, a.callErr("run.resume", map[string]string{"taskId": s.task.ID, "runId": runID}), "waiting for you")
	a.call("budget.resume", map[string]any{"runId": runID, "decision": "rejected"}, nil)
	a.waitFor("the run to fail", func() bool {
		a.call("task.get", map[string]string{"id": s.task.ID}, &d)
		return d.Run != nil && d.Run.Status == "FAILED"
	})
	reason := d.Run.FailedReason
	var x core.RunExplanation
	a.call("run.explain", map[string]string{"taskId": s.task.ID, "runId": runID}, &x)
	if x.Actions == nil || !x.Actions.Resume || !x.Actions.TimeTravel || !x.Actions.Replay || x.Actions.Supersedes != "" {
		t.Fatalf("a failed run can be resumed, forked from or replayed: %+v", x.Actions)
	}
	var res core.RunControlResult
	a.call("run.resume", map[string]string{"taskId": s.task.ID, "runId": runID}, &res)
	if res.RunID != runID {
		t.Fatalf("a resume goes on in the same run: %+v", res)
	}
	a.waitFor("the resumed run to stop again", func() bool {
		a.call("task.get", map[string]string{"id": s.task.ID}, &d)
		return d.Run != nil && d.Run.Status == "FAILED" && d.Run.Updated > x.Run.Updated
	})
	if d.Run.FailedReason != reason {
		t.Fatalf("the rejection stands: %q, was %q", d.Run.FailedReason, reason)
	}
	var usage []core.UsageRow
	a.call("usage.list", map[string]any{"taskId": s.task.ID}, &usage)
	if len(usage) != 1 {
		t.Fatalf("no turn started after the resume: %d usage rows", len(usage))
	}
	a.call("run.explain", map[string]string{"taskId": s.task.ID, "runId": runID}, &x)
	resumed := 0
	for _, cp := range x.Checkpoints {
		if cp.Resumed {
			resumed++
		}
	}
	if resumed != 1 {
		t.Fatalf("the resume is on the timeline once: %+v", x.Checkpoints)
	}
	var n int
	_ = a.c.DB.Reader().QueryRow(`SELECT COUNT(*) FROM approvals WHERE run_id=?`, runID).Scan(&n)
	if n != 1 {
		t.Fatalf("nothing asked the person again: %d approvals", n)
	}
	mustErr(t, a.callErr("run.resume", map[string]string{"taskId": "nope", "runId": runID}), "")
	mustErr(t, a.callErr("run.timeTravel", map[string]string{"taskId": s.task.ID, "runId": runID, "checkpointId": "cp_nope"}), "not one of this run's")
}

// A subworkflow node's nested run is explained with the parent's: its own executions, found from the parent's execution of the
// node, and its graph inside the parent's (docs/ui.md 6.2). The graph is a test's own, registered on the app's engine.
func TestExplainShowsTheNestedRunOfASubworkflow(t *testing.T) {
	a := open(t, "")
	p, _ := a.setup("gocalc")
	var tk tasks.Task
	a.call("task.create", tasks.CreateReq{ProjectID: p.ID, Title: "Nested"}, &tk)
	ok := func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		return workflow.NodeOutput{Value: json.RawMessage(`"ok"`)}, nil
	}
	inner := workflow.Sequential("test-inner", workflow.AgentTask("draft", ok), workflow.Check("lint", ok))
	outer := workflow.Sequential("test-outer", workflow.Route("prepare", ok), &workflow.Node{ID: "build", Kind: workflow.KindSubworkflow, Sub: inner})
	if err := a.c.Engine.Register(outer); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run, err := a.c.Engine.Start(ctx, "test-outer", map[string]string{"taskId": tk.ID}, workflow.StartOptions{ProjectID: p.ID, TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	a.waitFor("the run", func() bool { info, _ := a.c.Engine.Status(ctx, run); return info.Status == workflow.StatusCompleted })
	var x core.RunExplanation
	a.call("run.explain", map[string]string{"taskId": tk.ID, "runId": string(run)}, &x)
	if len(x.Runs) != 1 {
		t.Fatalf("the nested run is not one of the task's runs: %+v", x.Runs)
	}
	var build *workflow.NodeView
	for i := range x.Graph.Nodes {
		if x.Graph.Nodes[i].ID == "build" {
			build = &x.Graph.Nodes[i]
		}
	}
	if build == nil || build.Sub == nil || len(build.Sub.Nodes) != 2 {
		t.Fatalf("the nested graph is in the parent's: %+v", build)
	}
	if len(x.Subruns) != 1 {
		t.Fatalf("subruns %+v", x.Subruns)
	}
	s := x.Subruns[0]
	if s.NodeID != "build" || s.Step != 2 || s.Attempt != 1 || s.RunID != string(workflow.SubRunID(run, "build", 2, 1)) || s.Status != "COMPLETED" {
		t.Fatalf("subrun %+v", s)
	}
	if len(s.Executions) != 2 || s.Executions[0].NodeID != "draft" || s.Executions[0].Kind != "agent_task" || s.Executions[1].NodeID != "lint" || s.Executions[1].Status != "done" {
		t.Fatalf("nested executions %+v", s.Executions)
	}
}
