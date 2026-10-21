package pipeline_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// A blocking gate none of whose checks gave a verdict asks a person instead of passing (audit 2026-10-16, M28). Here the
// security template names one scan this build does not have, so the gate has nothing that can give a verdict.

type verdictQuestion struct {
	Gate   string `json:"gate"`
	Node   string `json:"node"`
	What   string `json:"what"`
	Diff   string `json:"diff"`
	Checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"checks"`
	Notes []string `json:"notes"`
}

// nextRequest waits for a request other than old to be open on the run (the run parked again after an answer) and returns it.
func nextRequest(t *testing.T, eng *workflow.Engine, id workflow.RunID, old workflow.RequestID) workflow.PendingRequest {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if p, err := eng.Pending(context.Background(), id); err == nil && len(p) == 1 && p[0].ID != old {
			return p[0]
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no new request opened on the run\n%s", stuckReport(eng, id))
	return workflow.PendingRequest{}
}

func securityScripts() map[string]fakeagent.Script {
	return map[string]fakeagent.Script{aWork: {Default: []fakeagent.Step{say("nothing to change")}}, aSec: shipReviewerStep(), aReview: shipReviewer()}
}

func decodeVerdict(t *testing.T, p workflow.PendingRequest) verdictQuestion {
	t.Helper()
	var q verdictQuestion
	if err := json.Unmarshal(p.Spec.Payload, &q); err != nil {
		t.Fatalf("payload %s: %v", p.Spec.Payload, err)
	}
	return q
}

func TestTheSecurityGateAsksAPersonWhenNoScanGaveAVerdict(t *testing.T) {
	e := newTpl(t, securityScripts(), tplOpts{title: "Audit", desc: "Audit."})
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "sast-by-nobody")
	e.await(id, workflow.StatusWaitingHuman)
	q1 := e.pending(id)
	q := decodeVerdict(t, q1)
	if q1.NodeID != "gate" || q1.Spec.Kind != "question" || q.Gate != "verdict" || q.Node != "gate" || q.Diff == "" || len(q.Checks) != 1 ||
		q.Checks[0].Name != "sast-by-nobody" || q.Checks[0].Status != "skipped" || !strings.Contains(q.Checks[0].Reason, "not available in this build") {
		t.Fatalf("the gate must ask, naming what could not run: %+v\n%+v", q1, q)
	}
	if !strings.Contains(q1.Spec.Title, "No check gave a result") || !strings.Contains(q1.Spec.Title, "sast-by-nobody") {
		t.Fatalf("title %q", q1.Spec.Title)
	}
	if len(e.prompts(aSec)) != 0 || len(e.evidence()["waiver"]) != 0 {
		t.Fatal("the work went on past a gate nothing had judged")
	}

	// "check again" runs the scans once more; nothing changed, so the gate asks again
	if err := e.respond(id, "answered", "human:kyle", `{"text":"installed the scanner"}`); err != nil {
		t.Fatal(err)
	}
	q2 := nextRequest(t, e.eng, id, q1.ID)
	if q2.NodeID != "gate" || len(e.evidence()["waiver"]) != 0 {
		t.Fatalf("after 'check again' with nothing changed: %+v", q2)
	}

	// an approval that does not say 'waive' is not a waiver either
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	q3 := nextRequest(t, e.eng, id, q2.ID)
	if q3.NodeID != "gate" {
		t.Fatalf("approving the question is not a waiver: %+v", q3)
	}

	// only a person can go on without a result
	if err := e.respond(id, "approved", "agent:rogue", `{"waive_verdict":true}`); err != nil {
		t.Fatal(err)
	}
	q4 := nextRequest(t, e.eng, id, q3.ID)
	if q4.NodeID != "gate" || len(e.evidence()["waiver"]) != 0 {
		t.Fatalf("an agent's waiver passed the gate: %+v", q4)
	}
	if notes := decodeVerdict(t, q4).Notes; len(notes) != 1 || !strings.Contains(notes[0], "only a person can decide") {
		t.Fatalf("the person must be told an agent asked: %v", notes)
	}

	// a person waives it: the gate passes, the waiver is evidence, and the work goes on to the security review
	if err := e.respond(id, "approved", "human:kyle", `{"waive_verdict":true}`); err != nil {
		t.Fatal(err)
	}
	q5 := nextRequest(t, e.eng, id, q4.ID)
	if q5.NodeID != "approve" {
		t.Fatalf("after the waiver the run goes on to the human approval: %+v", q5)
	}
	w := e.evidence()["waiver"]
	if len(w) != 1 || w[0].Passed != nil || !strings.Contains(w[0].Summary, "human:kyle") || !strings.Contains(w[0].Summary, "the security scans") || !strings.Contains(w[0].Summary, "sast-by-nobody") {
		t.Fatalf("waiver evidence %+v", w)
	}
	if len(e.prompts(aSec)) != 1 {
		t.Fatalf("the security review ran %d times", len(e.prompts(aSec)))
	}
}

func TestStoppingAtTheNoVerdictQuestionStopsTheRun(t *testing.T) {
	e := newTpl(t, securityScripts(), tplOpts{title: "Audit", desc: "Audit."})
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "sast-by-nobody")
	e.await(id, workflow.StatusWaitingHuman)
	if err := e.respond(id, "rejected", "human:kyle", ``); err != nil {
		t.Fatal(err)
	}
	info := e.await(id, workflow.StatusFailed)
	if !strings.Contains(info.FailedReason, "stopped the run") || !strings.Contains(info.FailedReason, "human:kyle") {
		t.Fatalf("failed reason %q", info.FailedReason)
	}
	if len(e.prompts(aSec)) != 0 {
		t.Fatal("the security review ran after the run was stopped")
	}
}

// A waiver is for the content it was asked about: when the work changes and the gate runs again with no verdict, it asks again.
func TestAWaiverCoversOnlyTheContentItWasAskedAbout(t *testing.T) {
	sc := map[string]fakeagent.Script{
		aWork: {
			Rules:   []fakeagent.Rule{{Match: "Reviewer feedback", Steps: []fakeagent.Step{writeStep("notes.txt", "log_level=info\n"), say("added the setting")}}},
			Default: []fakeagent.Step{say("nothing to change")},
		},
		aSec: {
			Rules:   []fakeagent.Rule{{Match: "log_level=info", Steps: []fakeagent.Step{say(`{"verdict":"SHIP","feedback":""}`)}}},
			Default: []fakeagent.Step{say(`{"verdict":"REVISE","feedback":"also set log_level=info"}`)},
		},
		aReview: shipReviewer(),
	}
	e := newTpl(t, sc, tplOpts{title: "Audit", desc: "Audit."})
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "sast-by-nobody")
	e.await(id, workflow.StatusWaitingHuman)
	q1 := e.pending(id)
	if err := e.respond(id, "approved", "human:kyle", `{"waive_verdict":true}`); err != nil {
		t.Fatal(err)
	}
	// the security review sends the work back, the worker changes a file, and the gate has no verdict for the new content
	q2 := nextRequest(t, e.eng, id, q1.ID)
	if q2.NodeID != "gate" {
		t.Fatalf("the gate must ask again about changed content: %+v", q2)
	}
	if d1, d2 := decodeVerdict(t, q1).Diff, decodeVerdict(t, q2).Diff; d1 == "" || d1 == d2 {
		t.Fatalf("the two questions are about the same content: %q %q", d1, d2)
	}
	if err := e.respond(id, "approved", "human:kyle", `{"waive_verdict":true}`); err != nil {
		t.Fatal(err)
	}
	q3 := nextRequest(t, e.eng, id, q2.ID)
	if q3.NodeID != "approve" || len(e.evidence()["waiver"]) != 2 {
		t.Fatalf("%+v, %d waivers", q3, len(e.evidence()["waiver"]))
	}
}

// The verification run asks too when every one of its checks was skipped or could not run: a task is not verified by checks
// that never ran.
func TestVerificationAsksAPersonWhenEveryCheckWasSkipped(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "done"}}})
	e := setup(t, cfg, ad)
	if err := checks.SaveSettings(context.Background(), e.db, "p1", checks.Settings{Secrets: false}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	e.deps.Checks = func(dir string) []checks.Spec {
		return []checks.Spec{{Name: "lint", Argv: []string{"lint"}, Dir: dir, InProcess: func(context.Context, proc.Manager, checks.Spec) (checks.Result, error) {
			return checks.SkippedResult("lint", "the linter is not installed"), nil
		}}}
	}
	id, _ := e.run(t, 3, workflow.StatusWaitingHuman)
	time.Sleep(100 * time.Millisecond)
	pend, err := e.eng.Pending(context.Background(), id)
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending %+v %v", pend, err)
	}
	q := decodeVerdict(t, pend[0])
	if pend[0].NodeID != "verify" || q.Gate != "verdict" || q.What != "the checks of this task" || len(q.Checks) != 1 || q.Checks[0].Name != "lint" ||
		!strings.Contains(q.Checks[0].Reason, "the linter is not installed") {
		t.Fatalf("verification must ask, naming what did not run: %+v %+v", pend[0], q)
	}
	ev, _ := e.tasks.Evidence(context.Background(), e.task.ID)
	for _, x := range ev {
		if x.Kind == "lint" && x.Passed != nil {
			t.Fatalf("a skipped check was recorded with a verdict: %+v", x)
		}
	}
	if err := e.answer(t, id, "approved", "human:kyle", `{"waive_verdict":true}`); err != nil {
		t.Fatal(err)
	}
	e.await(t, id, workflow.StatusCompleted)
	var waivers int
	ev, _ = e.tasks.Evidence(context.Background(), e.task.ID)
	for _, x := range ev {
		if x.Kind == "waiver" && strings.Contains(x.Summary, "human:kyle") {
			waivers++
		}
	}
	if waivers != 1 {
		t.Fatalf("%d waivers in the evidence", waivers)
	}
}

// One check that gave a verdict is enough: the skipped ones are recorded, and the gate does not ask.
func TestVerificationDoesNotAskWhenAtLeastOneCheckGaveAVerdict(t *testing.T) {
	ad, cfg := fakeEnv(t, fakeagent.Script{Default: []fakeagent.Step{{Write: &fakeagent.WriteSpec{Path: "calc.go", Content: rightImpl}}, {Say: "done"}}})
	e := setup(t, cfg, ad)
	if err := checks.SaveSettings(context.Background(), e.db, "p1", checks.Settings{Secrets: false}, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	e.deps.Checks = func(dir string) []checks.Spec {
		return append([]checks.Spec{{Name: "lint", Argv: []string{"lint"}, Dir: dir, InProcess: func(context.Context, proc.Manager, checks.Spec) (checks.Result, error) {
			return checks.SkippedResult("lint", "the linter is not installed"), nil
		}}}, checks.Detect(dir)...)
	}
	e.run(t, 3, workflow.StatusCompleted)
}
