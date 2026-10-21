package pipeline_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The CI gate and the merge gate of the pull-request flow (docs/github.md sections 3 and 5), driven through the real engine
// with scripted agents, real git and the labelled GitHub fixture.

func gateRoles(t *testing.T) (*env, *roles) {
	t.Helper()
	ad, cfg, lg := fakeRoles(t,
		fakeagent.Script{
			Rules: []fakeagent.Rule{{Match: "CI job", Steps: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImplDoc + "\n// fixed after CI\n"}}, {Say: "fixed what CI reported"}}}},
			Turns: [][]fakeagent.Step{{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImplDoc}}, {Say: "implemented"}}},
		},
		fakeagent.Script{Default: []fakeagent.Step{{Say: `{"verdict":"SHIP","feedback":""}`}}})
	return setup(t, cfg, ad), &lg
}

func (e *env) ciEvidence(t *testing.T) []tasks.Evidence {
	t.Helper()
	ev, err := e.tasks.Evidence(context.Background(), e.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []tasks.Evidence
	for _, x := range ev {
		if x.Kind == "ci" {
			out = append(out, x)
		}
	}
	return out
}

type ciPayload struct {
	Gate    string   `json:"gate"`
	PR      int      `json:"pr"`
	Head    string   `json:"head"`
	Reason  string   `json:"reason"`
	Missing []string `json:"missing"`
	Notes   []string `json:"notes"`
	State   string   `json:"state"`
}

func payloadOf(t *testing.T, q workflow.PendingRequest) ciPayload {
	t.Helper()
	var p ciPayload
	if err := json.Unmarshal(q.Spec.Payload, &p); err != nil {
		t.Fatalf("payload %s: %v", q.Spec.Payload, err)
	}
	return p
}

// No CI on the pull request is not a pass: the human is asked in plain words, nothing is recorded as passed, checking
// again waits again, and only the human's explicit waiver lets the commit through, recorded with the human's name. The
// approval then merges as usual.
func TestPRFlowWithoutCIAsksTheHumanAndAHumanWaiverPasses(t *testing.T) {
	e, _ := gateRoles(t)
	f := &githubFixture{noCI: true}
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	q := e.pendingOne(t, id)
	if q.NodeID != "ci" || q.Spec.Kind != "question" || q.Spec.Title != "No CI ran on pull request #7 within 60ms. Check again, merge without CI this time, or stop." {
		t.Fatalf("question %+v", q)
	}
	if p := payloadOf(t, q); p.Gate != "ci" || p.Reason != "none" || p.PR != 7 || p.Head == "" {
		t.Fatalf("payload %s", q.Spec.Payload)
	}
	if ev := e.ciEvidence(t); len(ev) != 0 {
		t.Fatalf("a commit nothing reported on must not get a CI result: %+v", ev)
	}

	// checking again waits for CI again (still nothing here) and asks again
	if err := e.answer(t, id, "answered", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusWaitingHuman)
	q2 := e.pendingOne(t, id)
	if q2.ID == q.ID || q2.NodeID != "ci" || payloadOf(t, q2).Reason != "none" {
		t.Fatalf("after checking again: %+v", q2)
	}

	// the human merges without CI this time
	if err := e.answer(t, id, "approved", "human:kyle", `{"waive_ci":true}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusWaitingHuman)
	if q3 := e.pendingOne(t, id); q3.NodeID != "approve" {
		t.Fatalf("the waiver must lead to the merge approval: %+v", q3)
	}
	ev := e.ciEvidence(t)
	if len(ev) != 1 || ev[0].Passed == nil || !*ev[0].Passed || ev[0].Summary != "CI waived by human:kyle for this commit: no CI reported on it" {
		t.Fatalf("the waiver must be recorded with the human's name: %+v", ev)
	}
	if err := e.answer(t, id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusCompleted)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.merged || f.merges != 1 {
		t.Fatalf("merged %v after %d merge calls", f.merged, f.merges)
	}
}

// Only a human: identity can waive CI. An agent's waiver is refused (CI is checked again and the human is asked again,
// told why), and a human's rejection stops the run without a merge.
func TestPRFlowAnAgentCannotWaiveCIAndAHumanCanStop(t *testing.T) {
	e, _ := gateRoles(t)
	f := &githubFixture{noCI: true}
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	q := e.pendingOne(t, id)
	if q.NodeID != "ci" {
		t.Fatalf("question %+v", q)
	}
	if err := e.answer(t, id, "approved", "agent:a2", `{"waive_ci":true}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusWaitingHuman)
	q2 := e.pendingOne(t, id)
	p := payloadOf(t, q2)
	if q2.ID == q.ID || q2.NodeID != "ci" || len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "agent:a2 asked to merge without CI; only a person can decide that") {
		t.Fatalf("an agent's waiver must be refused and said: %+v", q2)
	}
	if ev := e.ciEvidence(t); len(ev) != 0 {
		t.Fatalf("an agent's waiver passed CI: %+v", ev)
	}
	if err := e.answer(t, id, "rejected", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	info := e.await(t, id, workflow.StatusFailed)
	if !strings.Contains(info.FailedReason, "CI of pull request #7: stopped by human:kyle") {
		t.Fatalf("reason %q", info.FailedReason)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.merges != 0 || f.merged {
		t.Fatal("a stopped run merged")
	}
}

// A human declared that the repository has no CI: a commit nothing reports on passes after the review, and the evidence
// says who declared it and when.
func TestPRFlowNoCIDeclaredByAHumanPassesWithEvidence(t *testing.T) {
	e, _ := gateRoles(t)
	ctx := context.Background()
	if err := pipeline.SaveDevSettings(ctx, e.db, "p1", pipeline.DevSettings{MaxParallel: 2, Respond: true, CIMode: pipeline.CINone}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	set := pipeline.LoadDevSettings(ctx, e.db, "p1")
	if set.CIMode != pipeline.CINone || set.CINoneBy != "human:kyle" || set.CINoneAt == 0 {
		t.Fatalf("settings %+v", set)
	}
	f := &githubFixture{noCI: true}
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if q := e.pendingOne(t, id); q.NodeID != "approve" {
		t.Fatalf("pending %+v", q)
	}
	want := "CI declared absent by human:kyle on " + time.UnixMilli(set.CINoneAt).UTC().Format("2006-01-02") + ": no CI reported on it"
	if ev := e.ciEvidence(t); len(ev) != 1 || !*ev[0].Passed || ev[0].Summary != want {
		t.Fatalf("evidence %+v, want %q", ev, want)
	}
}

// "No CI" is a statement about the repository, not a waiver: checks that do report are still waited for, and a failing
// one goes back to the worker with its log.
func TestPRFlowNoCIDeclaredStillHonoursAFailingCheck(t *testing.T) {
	e, lg := gateRoles(t)
	if err := pipeline.SaveDevSettings(context.Background(), e.db, "p1", pipeline.DevSettings{MaxParallel: 2, Respond: true, CIMode: pipeline.CINone}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	f := &githubFixture{failFirst: true}
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if q := e.pendingOne(t, id); q.NodeID != "approve" {
		t.Fatalf("pending %+v", q)
	}
	if wp := prompts(lg.workLog); len(wp) != 2 || !strings.Contains(wp[1], `CI job "lint" concluded failure`) {
		t.Fatalf("the failing check must go back to the worker: %q", wp)
	}
	ev := e.ciEvidence(t)
	if len(ev) != 2 || *ev[0].Passed || !*ev[1].Passed || ev[1].Summary != "1 check(s) passed" {
		t.Fatalf("evidence %+v", ev)
	}
}

// A check that the base branch's protection requires is waited for by name while it runs, well past the grace period.
func TestPRFlowWaitsForARequiredCheckThatIsStillRunning(t *testing.T) {
	e, _ := gateRoles(t)
	f := &githubFixture{required: []string{"test", "deploy"}}
	f.ci = func(commit, look int) (runs, statuses []map[string]any) {
		deploy := map[string]any{"id": 2, "name": "deploy", "status": "in_progress"}
		if look >= 20 { // 20 looks 10ms apart: far longer than the 60ms grace period
			deploy["status"], deploy["conclusion"] = "completed", "success"
		}
		return []map[string]any{{"id": 1, "name": "test", "status": "completed", "conclusion": "success"}, deploy}, nil
	}
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if q := e.pendingOne(t, id); q.NodeID != "approve" {
		t.Fatalf("pending %+v", q)
	}
	if ev := e.ciEvidence(t); len(ev) != 1 || !*ev[0].Passed || ev[0].Summary != "2 check(s) passed" {
		t.Fatalf("evidence %+v", ev)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := f.looks[f.ciSeen[0]]; n != 21 {
		t.Fatalf("CI was read %d times; the running required check must be waited for until it finished (21 reads)", n)
	}
}

// A required check that never reports is treated like no CI for that check: the human is asked, naming it.
func TestPRFlowAsksWhenARequiredCheckNeverReports(t *testing.T) {
	e, _ := gateRoles(t)
	f := &githubFixture{required: []string{"test", "deploy"}} // only "test" reports
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	q := e.pendingOne(t, id)
	p := payloadOf(t, q)
	if q.NodeID != "ci" || q.Spec.Title != "The required check(s) deploy did not report on pull request #7 within 60ms. Check again, merge without CI this time, or stop." ||
		p.Reason != "missing" || strings.Join(p.Missing, ",") != "deploy" {
		t.Fatalf("question %+v", q)
	}
	if ev := e.ciEvidence(t); len(ev) != 0 {
		t.Fatalf("evidence %+v", ev)
	}
}

// Commit statuses (the older status API outside CI services use) are CI too, failing and passing. When the protection of
// the base cannot be read (only an administrator may), every check that reports is waited for and the evidence says so.
func TestPRFlowCommitStatusesCountAsCI(t *testing.T) {
	e, lg := gateRoles(t)
	f := &githubFixture{protectionHidden: true}
	f.ci = func(commit, look int) (runs, statuses []map[string]any) {
		state, desc := "success", "Preview deployed"
		if commit == 0 {
			state, desc = "failure", "Preview build exited with 1"
		}
		return nil, []map[string]any{{"id": 500 + commit, "context": "preview", "state": state, "description": desc, "target_url": "https://preview.test/7"}}
	}
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if q := e.pendingOne(t, id); q.NodeID != "approve" {
		t.Fatalf("pending %+v", q)
	}
	if wp := prompts(lg.workLog); len(wp) != 2 || !strings.Contains(wp[1], `CI job "preview" concluded failure`) || !strings.Contains(wp[1], "Preview build exited with 1") {
		t.Fatalf("a failing status must go back to the worker with its description: %q", wp)
	}
	ev := e.ciEvidence(t)
	if len(ev) != 2 || *ev[0].Passed || ev[0].Summary != "1 of 1 check(s) failed: preview" || !*ev[1].Passed ||
		!strings.HasPrefix(ev[1].Summary, "1 check(s) passed. Note: the branch protection of main could not be read in full") {
		t.Fatalf("evidence %+v", ev)
	}
}

// GitHub's merge state is read before the merge: a pull request GitHub would not merge (blocked by branch protection,
// behind, conflicting, a draft, not worked out yet) is a question in plain words and no merge call is made; checking again
// after the cause is gone merges, with a plain guarded merge request.
func TestPRFlowDoesNotMergeWhatGitHubWouldNotMerge(t *testing.T) {
	e, _ := gateRoles(t)
	f := &githubFixture{mergeState: "blocked"}
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if err := e.answer(t, id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ state, next, title string }{
		{"blocked", "behind", "Not merged: pull request #7 is blocked by the branch protection of main (a required review or a required check is missing or has not passed). Check again, or stop."},
		{"behind", "dirty", "Not merged: pull request #7 is behind main, and the branch protection of main wants it up to date before a merge. Check again, or stop."},
		{"dirty", "draft", "Not merged: pull request #7 has merge conflicts with main. Check again, or stop."},
		{"draft", "unknown", "Not merged: pull request #7 is a draft. Check again, or stop."},
		{"unknown", "clean", "Not merged: GitHub has not worked out whether pull request #7 can be merged. Check again, or stop."},
	} {
		e.await(t, id, workflow.StatusWaitingHuman)
		q := e.pendingOne(t, id)
		if q.NodeID != "merge" || q.Spec.Kind != "question" || q.Spec.Title != c.title || payloadOf(t, q).Gate != "merge" || payloadOf(t, q).State != c.state {
			t.Fatalf("%s: %+v", c.state, q)
		}
		f.mu.Lock()
		merges := f.merges
		f.mergeState = c.next
		f.mu.Unlock()
		if merges != 0 {
			t.Fatalf("%s: GitHub was asked to merge", c.state)
		}
		if err := e.answer(t, id, "answered", "human:kyle", `{"text":"check again"}`); err != nil {
			t.Fatal(err)
		}
	}
	e.await(t, id, workflow.StatusCompleted)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.merges != 1 || !f.merged || len(f.deleted) != 1 {
		t.Fatalf("merges %d merged %v deleted %v", f.merges, f.merged, f.deleted)
	}
	for k := range f.mergeBody {
		if k != "merge_method" && k != "sha" {
			t.Fatalf("the merge request asks for more than a plain guarded merge (%q): %v", k, f.mergeBody)
		}
	}
	if task, _ := e.tasks.Get(context.Background(), e.task.ID); task.Status != tasks.Completed || task.AcceptedBy != "human:kyle" {
		t.Fatalf("task %+v", task)
	}
}

// Stopping at a blocked merge stops the run; nothing is merged.
func TestPRFlowAHumanCanStopABlockedMerge(t *testing.T) {
	e, _ := gateRoles(t)
	f := &githubFixture{mergeState: "behind"}
	src, _ := e.prSetup(t, f)
	id := e.runPR(t, src, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	if err := e.answer(t, id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusWaitingHuman)
	if q := e.pendingOne(t, id); q.NodeID != "merge" || payloadOf(t, q).State != "behind" {
		t.Fatalf("pending %+v", q)
	}
	if err := e.answer(t, id, "rejected", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	info := e.await(t, id, workflow.StatusFailed)
	if !strings.Contains(info.FailedReason, "merge of pull request #7: stopped by human:kyle") {
		t.Fatalf("reason %q", info.FailedReason)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.merges != 0 || f.merged {
		t.Fatal("a stopped merge went through")
	}
}

// Checks still running at the deadline are a question too (never a pass), and a waiver covers CI that did not report or did
// not finish, never a failed check: here the check fails before the human answers, so "merge without CI" still sends the
// failure back to the worker, and the next commit's CI is waited for again (the waiver was for the earlier commit).
func TestPRFlowAWaiverNeverCoversAFailedCheck(t *testing.T) {
	e, lg := gateRoles(t)
	f := &githubFixture{}
	failed := false // guarded by f.mu, like everything the fixture serves
	f.ci = func(commit, look int) (runs, statuses []map[string]any) {
		e2e := map[string]any{"id": 99, "name": "e2e", "status": "queued", "html_url": "https://github.test/job/99"}
		switch {
		case commit > 0:
			e2e["status"], e2e["conclusion"] = "completed", "success"
		case failed:
			e2e["status"], e2e["conclusion"] = "completed", "failure"
		}
		return []map[string]any{e2e}, nil
	}
	src, _ := e.prSetup(t, f)
	short := func(ctx context.Context, projectID string) (pipeline.PRConfig, error) {
		c, err := src(ctx, projectID)
		c.CIMaxWait = 150 * time.Millisecond
		return c, err
	}
	id := e.runPR(t, short, 4)
	e.await(t, id, workflow.StatusWaitingHuman)
	q := e.pendingOne(t, id)
	if q.NodeID != "ci" || q.Spec.Title != "CI on pull request #7 is still running after 150ms (e2e). Check again, merge without CI this time, or stop." || payloadOf(t, q).Reason != "running" {
		t.Fatalf("question %+v", q)
	}
	f.mu.Lock()
	failed = true
	f.mu.Unlock()
	if err := e.answer(t, id, "approved", "human:kyle", `{"waive_ci":true}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusWaitingHuman)
	if q := e.pendingOne(t, id); q.NodeID != "approve" {
		t.Fatalf("pending %+v", q)
	}
	if wp := prompts(lg.workLog); len(wp) != 2 || !strings.Contains(wp[1], `CI job "e2e" concluded failure`) {
		t.Fatalf("the failed check must go back to the worker despite the waiver: %q", wp)
	}
	ev := e.ciEvidence(t)
	if len(ev) != 2 || *ev[0].Passed || ev[0].Summary != "1 of 1 check(s) failed: e2e" || !*ev[1].Passed || ev[1].Summary != "1 check(s) passed" {
		t.Fatalf("evidence %+v", ev)
	}
}
