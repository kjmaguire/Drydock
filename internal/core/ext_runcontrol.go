package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The run page's actions on a task's run (decision P123, docs/ui.md 6.2): Resume a run that stopped without finishing, Time
// travel from one of its checkpoints into a new run, and Replay a finished run from one of its checkpoints. The engine's calls
// (workflow.Engine.Continue, TimeTravel, Replay) say what runs again; this file adds what a task needs around them: one live
// run per task (a fork or a replay stops the task's run when it is waiting for a person or paused, and is refused while that
// run is working), the task's status brought back to where the checkpoint was, the task pointed at the run that goes on, and
// a line in the task's channel. A task that has ended (completed, cancelled, failed) is not run again: its state machine never
// leaves those states.

func init() {
	RegisterExtension(Extension{Name: "runcontrol", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		type runParam struct {
			TaskID       string `json:"taskId"`
			RunID        string `json:"runId"`
			CheckpointID string `json:"checkpointId"`
			// RestoreFiles (time travel): also put the worktree's files back to the safe point recorded after the checkpoint
			RestoreFiles bool `json:"restoreFiles"`
		}
		type restoreParam struct {
			TaskID string `json:"taskId"`
			RunID  string `json:"runId"`
			Ref    string `json:"ref"`
		}
		return map[string]Method{
			"run.resume": bind(func(ctx context.Context, p runParam) (any, error) {
				return c.ResumeRun(ctx, p.TaskID, p.RunID, c.Human())
			}),
			"run.timeTravel": bind(func(ctx context.Context, p runParam) (any, error) {
				return c.timeTravelRun(ctx, p.TaskID, p.RunID, p.CheckpointID, c.Human(), p.RestoreFiles)
			}),
			"run.replay": bind(func(ctx context.Context, p runParam) (any, error) {
				return c.ReplayRun(ctx, p.TaskID, p.RunID, p.CheckpointID, c.Human())
			}),
			// decision P124: a safe point's files back (shown first, undoable), and an ended task run again as a new task
			"run.restorePreview": bind(func(ctx context.Context, p restoreParam) (any, error) {
				return c.RestorePreview(ctx, p.TaskID, p.RunID, p.Ref)
			}),
			"run.restore": bind(func(ctx context.Context, p restoreParam) (any, error) {
				return c.RestoreSafePoint(ctx, p.TaskID, p.RunID, p.Ref, c.Human())
			}),
			"task.runAgain": bind(func(ctx context.Context, p struct {
				TaskID string `json:"taskId"`
			}) (any, error) {
				return c.RunAgain(ctx, p.TaskID, c.Human())
			}),
		}, nil, nil
	}})
}

// RunActions says which of the run page's actions a run of a task allows now, and why one does not.
type RunActions struct {
	Resume     bool `json:"resume"`
	TimeTravel bool `json:"timeTravel"`
	Replay     bool `json:"replay"`
	// ResumeWhy, TimeTravelWhy and ReplayWhy say, in a sentence, why an action is off.
	ResumeWhy     string `json:"resumeWhy,omitempty"`
	TimeTravelWhy string `json:"timeTravelWhy,omitempty"`
	ReplayWhy     string `json:"replayWhy,omitempty"`
	// Supersedes is the task's run that a fork or a replay stops (cancels) first: it is waiting for a person or paused.
	Supersedes       string `json:"supersedes,omitempty"`
	SupersedesStatus string `json:"supersedesStatus,omitempty"`
	// Lease is who drives the run (run_leases): this Drydock, another one, or nobody.
	Lease workflow.LeaseInfo `json:"lease"`
	// Restore says whether the task's worktree can be put back to a safe point now (P124), RestoreWhy why not.
	Restore    bool   `json:"restore"`
	RestoreWhy string `json:"restoreWhy,omitempty"`
	// RunAgain says whether the task, which has ended, can be run again as a new task (P124), RunAgainWhy why not.
	RunAgain    bool   `json:"runAgain"`
	RunAgainWhy string `json:"runAgainWhy,omitempty"`
}

// RunControlResult is the run that goes on after an action.
type RunControlResult struct {
	RunID string `json:"runId"`
	// Superseded is the run the action stopped first, if any.
	Superseded string `json:"superseded,omitempty"`
	// TaskStatus is the task's status once the action started the run; Note says what could not be put back (best effort).
	TaskStatus string `json:"taskStatus"`
	Note       string `json:"note,omitempty"`
	// Restored is the restore a time travel made of the worktree's files (P124), when it was asked for.
	Restored *RestoreResult `json:"restored,omitempty"`
}

// runActions works out what the run page may do with run info of task t.
func (c *Core) runActions(ctx context.Context, t tasks.Task, info workflow.RunInfo) RunActions {
	var a RunActions
	a.Lease, _ = c.Engine.Lease(ctx, info.ID)
	a.RestoreWhy = c.restoreRefusal(ctx, t)
	a.Restore = a.RestoreWhy == ""
	a.RunAgainWhy = runAgainRefusal(t)
	a.RunAgain = a.RunAgainWhy == ""
	off := func(why string) RunActions {
		return RunActions{ResumeWhy: why, TimeTravelWhy: why, ReplayWhy: why, Lease: a.Lease, Restore: a.Restore, RestoreWhy: a.RestoreWhy, RunAgain: a.RunAgain, RunAgainWhy: a.RunAgainWhy}
	}
	if t.Status.Terminal() {
		why := fmt.Sprintf("Task #%d is %s: its runs can be read, not run again.", t.Seq, strings.ToLower(string(t.Status)))
		if a.RunAgain {
			why = fmt.Sprintf("Task #%d is %s: its runs can be read. Run it again as a new task to go on.", t.Seq, strings.ToLower(string(t.Status)))
		}
		return off(why)
	}
	if t.OwnedByPerson() {
		return off(fmt.Sprintf("A person has taken task #%d on, so no agent works on it.", t.Seq))
	}
	working := func(s workflow.RunStatus) bool {
		return s == workflow.StatusRunning || s == workflow.StatusRecovering || s == workflow.StatusPending
	}
	orphan := working(info.Status) && !a.Lease.Held && !a.Lease.Driving
	// the task's live run other than this one, if any
	var other *workflow.RunInfo
	if t.RunID != "" && t.RunID != string(info.ID) {
		if cur, err := c.Engine.Status(ctx, workflow.RunID(t.RunID)); err == nil && !cur.Status.Terminal() {
			other = &cur
		}
	}

	switch {
	case info.Status == workflow.StatusFailed || orphan:
		a.Resume = other == nil
		if other != nil {
			a.ResumeWhy = fmt.Sprintf("Another run of this task is %s: one run of a task goes on at a time.", statusWords(other.Status))
		}
	case info.Status == workflow.StatusWaitingHuman:
		a.ResumeWhy = "The run is waiting for you: answer it on the task page."
	case info.Status == workflow.StatusPaused:
		a.ResumeWhy = "The run is paused: resume the task on its page."
	case working(info.Status):
		a.ResumeWhy = "The run is working."
	case info.Status == workflow.StatusCompleted:
		a.ResumeWhy = "The run completed."
	case info.Status == workflow.StatusCancelled:
		a.ResumeWhy = "The run was cancelled: time travel from one of its checkpoints to go on."
	}

	// a fork or a replay becomes the task's run: the live one stops first, unless it is working
	live := other
	if !info.Status.Terminal() {
		live = &info
	}
	busy := ""
	if live != nil {
		switch {
		case live.Status == workflow.StatusWaitingHuman || live.Status == workflow.StatusPaused:
			a.Supersedes, a.SupersedesStatus = string(live.ID), string(live.Status)
		case live.ID == info.ID && orphan:
			busy = "The run stopped when Drydock did: resume it instead, or wait for Drydock to take it over."
		default:
			busy = "The task's run is working: pause the task or wait for the run to stop first."
		}
	}
	a.TimeTravel, a.TimeTravelWhy = busy == "", busy
	switch {
	case !info.Status.Terminal():
		a.ReplayWhy = fmt.Sprintf("Only a run that has ended is replayed; this one is %s.", statusWords(info.Status))
	case busy != "":
		a.ReplayWhy = busy
	default:
		a.Replay = true
	}
	return a
}

func statusWords(s workflow.RunStatus) string {
	switch s {
	case workflow.StatusWaitingHuman:
		return "waiting for you"
	case workflow.StatusRecovering:
		return "being recovered"
	}
	return strings.ToLower(string(s))
}

// taskRun reads task taskID and its run runID (which must be one of the task's runs).
func (c *Core) taskRun(ctx context.Context, taskID, runID string) (tasks.Task, workflow.RunInfo, error) {
	if taskID == "" || runID == "" {
		return tasks.Task{}, workflow.RunInfo{}, errors.New("name the task and its run")
	}
	t, err := c.Tasks.Get(ctx, taskID)
	if err != nil {
		return t, workflow.RunInfo{}, err
	}
	info, err := c.Engine.Status(ctx, workflow.RunID(runID))
	if err != nil {
		return t, info, err
	}
	if info.TaskID != t.ID {
		return t, info, fmt.Errorf("run %s is not a run of task #%d", runID, t.Seq)
	}
	return t, info, nil
}

// engineWords turns the engine's refusals into what the run page says.
func engineWords(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, workflow.ErrLeaseHeld):
		return fmt.Errorf("another Drydock is driving this run right now (%w)", err)
	case errors.Is(err, workflow.ErrTopologyMismatch):
		return fmt.Errorf("this run's workflow has changed in this version of Drydock, so its checkpoints no longer fit it (%w)", err)
	case errors.Is(err, workflow.ErrUnknownWorkflow):
		return fmt.Errorf("this run's workflow is not in this version of Drydock (%w)", err)
	}
	return err
}

// ResumeRun resumes a task's run that failed, or that stopped when Drydock did, from its latest checkpoint
// (workflow.Engine.Continue): the step that failed or was cut off runs again in the same worktree, under the run's budgets and
// approvals. The run becomes the task's run again.
func (c *Core) ResumeRun(ctx context.Context, taskID, runID, by string) (RunControlResult, error) {
	if !isPerson(by) {
		return RunControlResult{}, errors.New("only a person resumes a run")
	}
	if err := c.RequireLicensed(ctx, "start_task"); err != nil {
		return RunControlResult{}, err
	}
	ctx, unlock := c.lockTaskStart(ctx, taskID)
	defer unlock()
	t, info, err := c.taskRun(ctx, taskID, runID)
	if err != nil {
		return RunControlResult{}, err
	}
	if a := c.runActions(ctx, t, info); !a.Resume {
		return RunControlResult{}, errors.New(a.ResumeWhy)
	}
	if err := c.requireRepo(ctx, t.ProjectID); err != nil {
		return RunControlResult{}, err
	}
	if err := c.Engine.Continue(ctx, info.ID, by); err != nil {
		return RunControlResult{}, engineWords(err)
	}
	t = c.pointTaskAt(ctx, t, runID)
	c.publishTask(ctx, "task.run_resumed", t, "", map[string]any{"taskId": t.ID, "runId": runID, "by": by, "from": info.Status})
	c.noteInRoom(ctx, t, fmt.Sprintf("%s resumed the run that %s. The step that stopped runs again.", personName(by), stoppedWords(info.Status)),
		map[string]any{"code": "run_resumed", "by": by, "runId": runID})
	return RunControlResult{RunID: runID, TaskStatus: string(t.Status)}, nil
}

func stoppedWords(s workflow.RunStatus) string {
	if s == workflow.StatusFailed {
		return "failed"
	}
	return "stopped when Drydock did"
}

// TimeTravelRun starts a new run of the task from checkpoint cpID of its run runID (workflow.Engine.TimeTravel), with the state
// as of that checkpoint. The task's run, when it waits for a person or is paused, is cancelled first (its history stays); the
// task's status goes back to what it was at the checkpoint, as far as its state machine allows; the new run becomes the task's.
func (c *Core) TimeTravelRun(ctx context.Context, taskID, runID, cpID, by string) (RunControlResult, error) {
	return c.branchRun(ctx, taskID, runID, cpID, by, false, false)
}

// timeTravelRun is TimeTravelRun that, with restore, also puts the worktree's files back to the safe point recorded right
// after the checkpoint (P124) before the new run starts: refused, with nothing changed, while an agent turn runs in the
// worktree or when no agent turn ran after the checkpoint (no files were recorded for it).
func (c *Core) timeTravelRun(ctx context.Context, taskID, runID, cpID, by string, restore bool) (RunControlResult, error) {
	return c.branchRun(ctx, taskID, runID, cpID, by, false, restore)
}

// ReplayRun runs the nodes of the task's ended run runID again in a new run, from checkpoint cpID (empty: its entry), with the
// recorded inputs (workflow.Engine.Replay); every step that merges, pushes or creates tasks asks for a fresh approval first.
// Otherwise as TimeTravelRun.
func (c *Core) ReplayRun(ctx context.Context, taskID, runID, cpID, by string) (RunControlResult, error) {
	return c.branchRun(ctx, taskID, runID, cpID, by, true, false)
}

func (c *Core) branchRun(ctx context.Context, taskID, runID, cpID, by string, replay, restore bool) (RunControlResult, error) {
	what := "time travel"
	if replay {
		what = "replay"
	}
	if !isPerson(by) {
		return RunControlResult{}, fmt.Errorf("only a person starts a %s", what)
	}
	if err := c.RequireLicensed(ctx, "start_task"); err != nil {
		return RunControlResult{}, err
	}
	ctx, unlock := c.lockTaskStart(ctx, taskID)
	defer unlock()
	t, info, err := c.taskRun(ctx, taskID, runID)
	if err != nil {
		return RunControlResult{}, err
	}
	a := c.runActions(ctx, t, info)
	if replay && !a.Replay {
		return RunControlResult{}, errors.New(a.ReplayWhy)
	}
	if !replay && !a.TimeTravel {
		return RunControlResult{}, errors.New(a.TimeTravelWhy)
	}
	if !replay && cpID == "" {
		return RunControlResult{}, errors.New("choose the checkpoint to travel back to")
	}
	if err := c.requireRepo(ctx, t.ProjectID); err != nil {
		return RunControlResult{}, err
	}
	// the checkpoint must be one of this run's (time travel) or of its history (a replay may start at the entry it forked from)
	cps, err := c.Engine.RunCheckpoints(ctx, info.ID)
	if err != nil {
		return RunControlResult{}, err
	}
	if replay {
		if cps, err = c.Engine.Checkpoints(ctx, info.ID); err != nil {
			return RunControlResult{}, err
		}
	}
	var from *workflow.Checkpoint
	for i := range cps {
		if (cpID == "" && i == 0) || string(cps[i].ID) == cpID {
			from = &cps[i]
			break
		}
	}
	if from == nil {
		return RunControlResult{}, fmt.Errorf("checkpoint %s is not one of this run's", cpID)
	}
	// the files go back before anything else changes: refused here, the fork does not happen either
	safeRef := ""
	if restore {
		execs, err := c.Engine.Executions(ctx, info.ID)
		if err != nil {
			return RunControlResult{}, err
		}
		if safeRef = safePointAfter(execs, from.Step); safeRef == "" {
			return RunControlResult{}, fmt.Errorf("no agent turn ran after step %d, so no safe point holds the files of that point; time travel without putting the files back", from.Step)
		}
		if why := c.restoreRefusal(ctx, t); why != "" {
			return RunControlResult{}, errors.New(why)
		}
	}

	res := RunControlResult{}
	if a.Supersedes != "" {
		if err := c.Engine.Cancel(ctx, workflow.RunID(a.Supersedes)); err != nil && !errors.Is(err, workflow.ErrRunTerminal) {
			return RunControlResult{}, fmt.Errorf("stop the task's run first: %w", err)
		}
		res.Superseded = a.Supersedes
	}
	if safeRef != "" {
		r, err := c.restoreTo(ctx, t, runID, safeRef, by, fmt.Sprintf("time travel from step %d", from.Step))
		if err != nil {
			return res, fmt.Errorf("the files could not be put back, so the time travel did not start: %w", err)
		}
		res.Restored = &r
	}
	t, res.Note = c.alignTaskTo(ctx, t, from.Created, fmt.Sprintf("%s by %s from step %d", what, personName(by), from.Step))
	var id workflow.RunID
	if replay {
		id, err = c.Engine.Replay(ctx, info.ID, from.ID, by)
	} else {
		id, err = c.Engine.TimeTravel(ctx, from.ID, by)
	}
	if err != nil {
		return res, engineWords(err)
	}
	res.RunID = string(id)
	t = c.pointTaskAt(ctx, t, res.RunID)
	res.TaskStatus = string(t.Status)
	typ, code, line := "task.run_forked", "run_forked", fmt.Sprintf("%s went back to step %d of an earlier run: a new run goes on from there, and the earlier run is kept as it was.", personName(by), from.Step)
	if replay {
		typ, code, line = "task.run_replayed", "run_replayed", fmt.Sprintf("%s replays an earlier run from step %d. Steps that merge, push or create tasks ask first.", personName(by), from.Step)
	}
	c.publishTask(ctx, typ, t, "", map[string]any{"taskId": t.ID, "runId": res.RunID, "from": runID, "checkpoint": from.ID, "step": from.Step, "by": by, "superseded": res.Superseded})
	c.noteInRoom(ctx, t, line, map[string]any{"code": code, "by": by, "runId": res.RunID, "from": runID})
	return res, nil
}

// pointTaskAt makes runID the task's run (the task page, its pending requests and the scheduler read it).
func (c *Core) pointTaskAt(ctx context.Context, t tasks.Task, runID string) tasks.Task {
	if t.RunID == runID {
		return t
	}
	if nt, err := c.Tasks.Patch(ctx, t.ID, tasks.Update{RunID: &runID}); err == nil {
		return nt
	}
	t.RunID = runID
	return t
}

// taskStatusAt is the status task taskID had at time at (Unix ms): the target of the last transition recorded by then.
func (c *Core) taskStatusAt(ctx context.Context, taskID string, at int64) (tasks.Status, bool) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT data FROM events WHERE task_id=? AND ts<=? AND type IN
		('task.ready','task.claimed','task.started','task.resumed','task.blocked','task.waiting_human','task.review','task.verification')
		ORDER BY seq DESC LIMIT 1`, taskID, at)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		var d struct {
			To tasks.Status `json:"to"`
		}
		if rows.Scan(&data) == nil && json.Unmarshal(data, &d) == nil && d.To != "" {
			return d.To, true
		}
	}
	return "", false
}

// alignTaskTo brings the task's status back to what it was at time at, as far as the state machine allows: a task that is
// further on (in review, verification, waiting or blocked) goes back to IN_PROGRESS as a resync (no iteration is used up), then
// forward to the status it had (REVIEW, VERIFICATION, WAITING_HUMAN or BLOCKED) when the guards allow it. A task that had not
// started at that time is left for the run's prepare step. Best effort: what could not be put back is returned as a note, and
// the run's own nodes move the task on as they always do. The task's records (iterations used, evidence, reviews) stay.
func (c *Core) alignTaskTo(ctx context.Context, t tasks.Task, at int64, why string) (tasks.Task, string) {
	want, ok := c.taskStatusAt(ctx, t.ID, at)
	if !ok {
		want = tasks.Ready // nothing recorded yet: the run had not moved the task
	}
	switch want {
	case tasks.Backlog, tasks.Ready, tasks.Claimed:
		want = tasks.InProgress
		if t.Status == tasks.Backlog || t.Status == tasks.Ready || t.Status == tasks.Claimed || t.Status == tasks.InProgress {
			return t, ""
		}
	}
	if t.Status == want {
		return t, ""
	}
	var path []tasks.Status
	if t.Status != tasks.InProgress {
		path = append(path, tasks.InProgress)
	}
	switch want {
	case tasks.Review:
		path = append(path, tasks.Review)
	case tasks.Verification:
		path = append(path, tasks.Review, tasks.Verification)
	case tasks.WaitingHuman, tasks.Blocked:
		path = append(path, want)
	}
	for _, to := range path {
		nt, err := c.Tasks.Transition(ctx, t.ID, to, tasks.TransitionOpts{By: "system", Reason: why, Resync: to == tasks.InProgress})
		if err != nil {
			return t, fmt.Sprintf("The task stayed %s (it was %s at that point): %v", t.Status, want, err)
		}
		t = nt
	}
	return t, ""
}
