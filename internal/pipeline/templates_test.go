package pipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

func featureScripts() map[string]fakeagent.Script {
	return map[string]fakeagent.Script{
		aPM:     {Default: []fakeagent.Step{say(mulSpec)}},
		aQA:     {Default: []fakeagent.Step{writeStep("multiply_test.go", mulTests), say("wrote the tests")}},
		aWork:   {Default: []fakeagent.Step{writeStep("calc.go", calcWithMul), say("implemented Multiply")}},
		aReview: shipReviewer(),
	}
}

func featureRoles() map[string]string {
	return map[string]string{pipeline.RolePM: aPM, pipeline.RoleTests: aQA}
}

// The Feature template end to end: a PM writes a spec, the human accepts it, tests are written first and fail (red), the
// implementation makes them pass (green), an independent reviewer ships, a human merges. The gate: no test-writing and no
// implementation turn exists before the acceptance.
func TestFeatureTemplateSpecGateRedGreenAndHumanMerge(t *testing.T) {
	e := newTpl(t, featureScripts(), tplOpts{})
	id := e.start(pipeline.WorkflowFeature, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)

	// parked at the spec review: the PM ran, nobody else did, nothing is in the worktree, the task waits for the human
	p := e.pending(id)
	if p.NodeID != "specreview" || p.Spec.Kind != "review" {
		t.Fatalf("pending %+v", p)
	}
	var sp pipeline.SpecReviewPayload
	if err := json.Unmarshal(p.Spec.Payload, &sp); err != nil || sp.Phase != "spec" || len(sp.Criteria) != 2 || sp.Criteria[0].ID != "AC1" || !strings.Contains(sp.Spec, "Multiply") {
		t.Fatalf("payload %s (%v)", p.Spec.Payload, err)
	}
	if n := len(e.prompts(aQA)) + len(e.prompts(aWork)) + len(e.prompts(aReview)); n != 0 {
		t.Fatalf("NOTHING may run before the spec is accepted; %d agent turns started", n)
	}
	if got := e.get(); got.Status != tasks.WaitingHuman {
		t.Fatalf("task %s", got.Status)
	}
	if st, _ := e.repo.Status(context.Background(), gitWT(e.get())); st.Dirty() {
		t.Fatalf("worktree must be untouched at the gate: %+v", st)
	}
	if pp := e.prompts(aPM); len(pp) != 1 || !strings.Contains(pp[0], "Drydock phase: spec") || !strings.Contains(pp[0], "Given") {
		t.Fatalf("pm prompt %q", pp)
	}

	// the human accepts
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman) // the final approval
	p = e.pending(id)
	if p.NodeID != "approve" {
		t.Fatalf("pending %+v", p)
	}

	task := e.get()
	if task.Status != tasks.Verification || len(task.Acceptance) != 2 || !strings.HasPrefix(task.Acceptance[0], "AC1: Given") {
		t.Fatalf("task %s acceptance %v", task.Status, task.Acceptance)
	}
	tp := e.prompts(aQA)
	if len(tp) != 1 || !strings.Contains(tp[0], "Drydock phase: write-tests") || !strings.Contains(tp[0], "AC2") || !strings.Contains(tp[0], "FAIL right now") {
		t.Fatalf("tests prompt %q", tp)
	}
	wp := e.prompts(aWork)
	if len(wp) != 1 || !strings.Contains(wp[0], "currently fail") || !strings.Contains(wp[0], "undefined: Multiply") {
		t.Fatalf("the implementer must be told which tests fail: %q", wp)
	}
	ev := e.evidence()
	for _, kind := range []string{"spec", "spec_accept", "red_test", "green_test", "test", "diff", "commit"} {
		if len(ev[kind]) == 0 {
			t.Errorf("evidence %q missing; have %v", kind, keys(ev))
		}
	}
	if r := ev["red_test"][0]; r.Passed != nil || !strings.Contains(r.Summary, "RED") {
		t.Fatalf("red evidence %+v", r)
	}
	if g := ev["green_test"][0]; g.Passed == nil || !*g.Passed || !strings.Contains(g.Summary, "GREEN") {
		t.Fatalf("green evidence %+v", g)
	}
	if a := ev["spec_accept"][0]; a.Passed == nil || !*a.Passed || !strings.Contains(string(a.Data), "human:kyle") {
		t.Fatalf("acceptance evidence %+v", a)
	}
	// order: the spec was accepted before the red run before the green run
	var order []string
	all, _ := e.tasks.Evidence(context.Background(), task.ID)
	for _, x := range all {
		switch x.Kind {
		case "spec_accept", "red_test", "green_test":
			order = append(order, x.Kind)
		}
	}
	if strings.Join(order, ",") != "spec_accept,red_test,green_test" {
		t.Fatalf("evidence order %v", order)
	}
	// the review was independent
	rv, _ := e.tasks.Reviews(context.Background(), task.ID)
	if len(rv) != 1 || rv[0].Verdict != "SHIP" || rv[0].Reviewer != aReview {
		t.Fatalf("reviews %+v", rv)
	}
	// the approval request summarizes it all and main is untouched
	var sum pipeline.ApproveSummary
	json.Unmarshal(p.Spec.Payload, &sum)
	haveRed := false
	for _, x := range sum.Evidence {
		haveRed = haveRed || x.Kind == "red_test"
	}
	if !haveRed || len(sum.Reviews) != 1 {
		t.Fatalf("approval payload %+v", sum)
	}
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go")); strings.Contains(string(m), "Multiply") {
		t.Fatal("nothing may reach main before approval")
	}

	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusCompleted)
	if got := e.get(); got.Status != tasks.Completed || got.AcceptedBy != "human:kyle" {
		t.Fatalf("task %+v", got)
	}
	m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go"))
	mt, _ := os.ReadFile(filepath.Join(e.repo.Root, "multiply_test.go"))
	if !strings.Contains(string(m), "Multiply") || !strings.Contains(string(mt), "AC1") {
		t.Fatalf("merged content: %s / %s", m, mt)
	}
}

func keys(m map[string][]tasks.Evidence) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}

// A spec without Given/When/Then criteria never reaches the human: it goes back to the PM with the problems.
func TestSpecWithoutCriteriaGoesBackToThePMNotToTheHuman(t *testing.T) {
	sc := featureScripts()
	sc[aPM] = fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "The spec cannot be used", Steps: []fakeagent.Step{say(mulSpec)}}},
		Default: []fakeagent.Step{say("I think we should add multiplication. It should be easy and fast, and users will like it a lot.")},
	}
	e := newTpl(t, sc, tplOpts{})
	id := e.start(pipeline.WorkflowFeature, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)
	pp := e.prompts(aPM)
	if len(pp) != 2 || !strings.Contains(pp[1], "no acceptance criteria in Given/When/Then form") || !strings.Contains(pp[1], "I think we should add") {
		t.Fatalf("the PM must get the problems and its previous text: %q", pp)
	}
	if p := e.pending(id); p.NodeID != "specreview" {
		t.Fatalf("pending %+v", p)
	}
	// only valid specs are evidence
	if n := len(e.evidence()["spec"]); n != 1 {
		t.Fatalf("spec evidence %d", n)
	}
}

// Rejecting the spec sends the human's words to the PM; accepting with edited criteria replaces them.
func TestHumanRejectsTheSpecThenAcceptsWithEditedCriteria(t *testing.T) {
	sc := featureScripts()
	sc[aPM] = fakeagent.Script{
		Rules:   []fakeagent.Rule{{Match: "also cover negative numbers", Steps: []fakeagent.Step{say(mulSpec + "\n- Given -2 and 5, when Multiply is called, then it returns -10")}}},
		Default: []fakeagent.Step{say(mulSpec)},
	}
	e := newTpl(t, sc, tplOpts{})
	id := e.start(pipeline.WorkflowFeature, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)
	if err := e.respond(id, "rejected", "human:kyle", `{"feedback":"also cover negative numbers"}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman)
	pp := e.prompts(aPM)
	if len(pp) != 2 || !strings.Contains(pp[1], "The human reviewer sent the spec back: also cover negative numbers") || !strings.Contains(pp[1], "Your previous spec") {
		t.Fatalf("pm prompts %q", pp)
	}
	if got := e.get(); got.Status != tasks.WaitingHuman {
		t.Fatalf("the task waits for the human again: %s", got.Status)
	}
	if n := len(e.prompts(aQA)) + len(e.prompts(aWork)); n != 0 {
		t.Fatalf("a rejected spec must not start any building: %d", n)
	}
	// the human edits the criteria while accepting: a single, different criterion
	edit := `{"criteria":["Given 6 and 7, when Multiply is called, then it returns 42"]}`
	if err := e.respond(id, "approved", "human:kyle", edit); err != nil {
		t.Fatal(err)
	}
	// the tests then carry AC1 only, so the scripted tests (AC1 and AC2) still trace to it
	e.await(id, workflow.StatusWaitingHuman)
	got := e.get()
	if len(got.Acceptance) != 1 || !strings.Contains(got.Acceptance[0], "returns 42") || !strings.HasPrefix(got.Acceptance[0], "AC1:") {
		t.Fatalf("edited acceptance %v", got.Acceptance)
	}
}

// Invalid human edits do not accept anything: they go back to the PM as feedback.
func TestInvalidEditedCriteriaAreNotAccepted(t *testing.T) {
	e := newTpl(t, featureScripts(), tplOpts{})
	id := e.start(pipeline.WorkflowFeature, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)
	if err := e.respond(id, "approved", "human:kyle", `{"criteria":["it should multiply"]}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman)
	if n := len(e.prompts(aQA)); n != 0 {
		t.Fatalf("invalid criteria must not unlock the build: %d tests turns", n)
	}
	pp := e.prompts(aPM)
	if len(pp) != 2 || !strings.Contains(pp[1], "criteria you edited are not usable") {
		t.Fatalf("pm prompts %q", pp)
	}
	if len(e.evidence()["spec_accept"]) != 0 {
		t.Fatal("no acceptance may be recorded")
	}
}

// An agent identity can never accept a spec, even if it somehow reaches the request port.
func TestAgentCannotAcceptTheSpec(t *testing.T) {
	e := newTpl(t, featureScripts(), tplOpts{})
	id := e.start(pipeline.WorkflowFeature, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)
	if err := e.respond(id, "approved", aPM, `{}`); err != nil {
		t.Fatal(err)
	}
	info := e.await(id, workflow.StatusFailed)
	if !strings.Contains(info.FailedReason, "only be accepted by a human") {
		t.Fatalf("reason %q", info.FailedReason)
	}
	if n := len(e.prompts(aQA)) + len(e.prompts(aWork)); n != 0 {
		t.Fatalf("an agent's acceptance must not unlock the build: %d turns", n)
	}
	if len(e.evidence()["spec_accept"]) != 0 {
		t.Fatal("no acceptance evidence for an agent")
	}
	// and the second lock: the test-writing, design and implementation nodes refuse to start for a task whose spec nobody
	// accepted, even if the graph were wired wrongly
	e2 := newTpl(t, featureScripts(), tplOpts{})
	g := e2.td.FeatureGraph()
	raw, _ := json.Marshal(pipeline.TemplateInput{TaskID: e2.task.ID, Roles: featureRoles()})
	for _, node := range []string{"tests", "work"} {
		in := workflow.NodeInput{NodeInfo: workflow.NodeInfo{Workflow: pipeline.WorkflowFeature, NodeID: workflow.NodeID(node)}, Input: raw}
		if _, err := g.Nodes[workflow.NodeID(node)].Handler(context.Background(), in); err == nil || !strings.Contains(err.Error(), "nothing is implemented until a human accepts the spec") {
			t.Fatalf("%s must refuse without an accepted spec: %v", node, err)
		}
	}
	if len(e2.prompts(aQA))+len(e2.prompts(aWork)) != 0 {
		t.Fatal("no agent may have been started")
	}
}

// Tests that already pass prove nothing: they go back to the test writer, and after the bound a human decides.
func TestTestsThatPassBeforeTheImplementationAreRejected(t *testing.T) {
	sc := featureScripts()
	passing := "package gocalc\n\nimport \"testing\"\n\n// AC1 AC2 both already hold\nfunc TestAC1AC2_Add(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n}\n"
	sc[aQA] = fakeagent.Script{Default: []fakeagent.Step{writeStep("multiply_test.go", passing), say("wrote tests")}}
	e := newTpl(t, sc, tplOpts{})
	id := e.start(pipeline.WorkflowFeature, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)
	e.respond(id, "approved", "human:kyle", `{}`)
	e.await(id, workflow.StatusWaitingHuman)
	p := e.pending(id)
	if p.NodeID != "redlimit" {
		t.Fatalf("after the bound a human decides: %+v", p)
	}
	tp := e.prompts(aQA)
	if len(tp) != 4 || !strings.Contains(tp[1], "do not test the new behaviour") {
		t.Fatalf("test writer prompts %d: %q", len(tp), tp)
	}
	if len(e.prompts(aWork)) != 0 || len(e.evidence()["red_test"]) != 0 {
		t.Fatal("no implementation and no red evidence without a failing test")
	}
	// the human may waive red; the work then runs and no red evidence exists to claim green against
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman)
	if len(e.prompts(aWork)) != 1 || len(e.evidence()["green_test"]) != 0 {
		t.Fatalf("work turns %d green %d", len(e.prompts(aWork)), len(e.evidence()["green_test"]))
	}
}

// The test-writing step may only touch tests, and every accepted criterion must be traceable to a test.
func TestTestWritingStepIsScopedToTestsAndTracesTheCriteria(t *testing.T) {
	sc := featureScripts()
	onlyAC1 := strings.Replace(mulTests, "AC2", "second", -1)
	sc[aQA] = fakeagent.Script{
		Rules: []fakeagent.Rule{
			{Match: "no test refers to AC2", Steps: []fakeagent.Step{writeStep("multiply_test.go", mulTests), say("traced")}},
			{Match: "non-test files changed", Steps: []fakeagent.Step{writeStep("calc.go", greenCalc), writeStep("multiply_test.go", onlyAC1), say("reverted calc.go")}},
		},
		Default: []fakeagent.Step{writeStep("calc.go", calcWithMul), writeStep("multiply_test.go", mulTests), say("did too much")},
	}
	e := newTpl(t, sc, tplOpts{})
	id := e.start(pipeline.WorkflowFeature, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)
	e.respond(id, "approved", "human:kyle", `{}`)
	e.await(id, workflow.StatusWaitingHuman)
	tp := e.prompts(aQA)
	if len(tp) != 3 || !strings.Contains(tp[1], "non-test files changed") || !strings.Contains(tp[1], "calc.go") || !strings.Contains(tp[2], "no test refers to AC2") {
		t.Fatalf("prompts %q", tp)
	}
	if e.pending(id).NodeID != "approve" {
		t.Fatal("the flow must reach the final approval after the tests were fixed")
	}
}

func TestIsTestPath(t *testing.T) {
	yes := []string{"calc_test.go", "pkg/x_test.go", "src/app.test.ts", "src/app.spec.tsx", "tests/api.py", "test/foo.js", "e2e/app.spec.ts", "__tests__/a.js", "test_utils.py", "testdata/fixture.json", "a/b/conftest.py"}
	no := []string{"calc.go", "src/app.ts", "README.md", "latest.go", "contest.go", "protest/x.go", "attestation.go"}
	for _, p := range yes {
		if !pipeline.IsTestPath(p) {
			t.Errorf("%s should be a test path", p)
		}
	}
	for _, p := range no {
		if pipeline.IsTestPath(p) {
			t.Errorf("%s should not be a test path", p)
		}
	}
}

const buggyDouble = greenCalc + "\n// Double returns twice a.\nfunc Double(a int) int { return a + a + 1 }\n"
const fixedDouble = greenCalc + "\n// Double returns twice a.\nfunc Double(a int) int { return a + a }\n"
const reproTest = "package gocalc\n\nimport \"testing\"\n\nfunc TestDoubleOfTwoIsFour(t *testing.T) {\n\tif got := Double(2); got != 4 {\n\t\tt.Fatalf(\"Double(2) = %d, want 4\", got)\n\t}\n}\n"

// Bug template: a reproducing test must fail first; only then is the fix attempted.
func TestBugTemplateReproducesWithAFailingTestFirst(t *testing.T) {
	sc := map[string]fakeagent.Script{
		aQA:     {Default: []fakeagent.Step{writeStep("double_test.go", reproTest), say("reproduced")}},
		aWork:   {Default: []fakeagent.Step{writeStep("calc.go", fixedDouble), say("fixed")}},
		aReview: shipReviewer(),
	}
	e := newTpl(t, sc, tplOpts{files: map[string]string{"calc.go": buggyDouble}, title: "Double is off by one", desc: "Double(2) returns 5 instead of 4."})
	id := e.start(pipeline.WorkflowBug, map[string]string{pipeline.RoleTests: aQA})
	e.await(id, workflow.StatusWaitingHuman)
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("pending %+v", p)
	}
	qp := e.prompts(aQA)
	if len(qp) != 1 || !strings.Contains(qp[0], "Drydock phase: reproduce") || !strings.Contains(qp[0], "Double(2) returns 5") {
		t.Fatalf("repro prompt %q", qp)
	}
	ev := e.evidence()
	if len(ev["red_test"]) != 1 || !strings.Contains(ev["red_test"][0].Summary, "TestDoubleOfTwoIsFour") || len(ev["green_test"]) != 1 {
		t.Fatalf("red/green evidence missing: %v", keys(ev))
	}
	if len(ev["spec"])+len(ev["spec_accept"]) != 0 {
		t.Fatal("the Bug template has no spec gate")
	}
	// reproduced first, fixed second
	if wp := e.prompts(aWork); len(wp) != 1 || !strings.Contains(wp[0], "TestDoubleOfTwoIsFour") {
		t.Fatalf("the fixer must be told the failing reproduction: %q", wp)
	}
	e.respond(id, "approved", "human:kyle", `{}`)
	e.await(id, workflow.StatusCompleted)
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go")); !strings.Contains(string(m), "return a + a }") {
		t.Fatalf("main:\n%s", m)
	}
}

func TestRegisterTemplatesIsRepeatableOnARunningEngine(t *testing.T) {
	e := newTpl(t, featureScripts(), tplOpts{})
	id := e.start(pipeline.WorkflowFeature, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)
	for i := 0; i < 2; i++ {
		if err := pipeline.RegisterTemplates(e.eng, e.td); err != nil {
			t.Fatalf("registering again while a run waits: %v", err)
		}
	}
	// the waiting run still resumes after the re-registration
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman)
}

func TestTemplateCatalogAndRoleResolution(t *testing.T) {
	names := map[string]pipeline.Template{}
	for _, tp := range pipeline.Templates() {
		names[tp.Name] = tp
	}
	for _, want := range []string{"iterate-until-verified", "feature", "bug", "security", "full-stack-saas"} {
		if _, ok := names[want]; !ok {
			t.Errorf("template %s missing", want)
		}
	}
	if !names["feature"].SpecGate || !names["full-stack-saas"].SpecGate || names["bug"].SpecGate || names["security"].SpecGate {
		t.Fatal("the spec gate belongs to Feature and Full Stack SaaS only")
	}
	team := []pipeline.TeamMember{{ID: "w", Role: "Backend"}, {ID: "p", Role: "Product Manager"}, {ID: "q", Role: "QA"}, {ID: "s", Role: "Security"}, {ID: "r", Role: "Reviewer"}}
	res, err := pipeline.ResolveRoles(names["feature"].Roles, "w", "r", team, nil)
	if err != nil || res.Roles["pm"] != "p" || res.Roles["tests"] != "q" {
		t.Fatalf("%v %+v", err, res)
	}
	// no QA agent: the worker writes the tests, and the result says so
	res, err = pipeline.ResolveRoles(names["feature"].Roles, "w", "r", []pipeline.TeamMember{{ID: "w", Role: "Backend"}, {ID: "p", Role: "pm"}, {ID: "r", Role: "reviewer"}}, nil)
	if err != nil || res.Roles["tests"] != "w" || len(res.Notes) == 0 {
		t.Fatalf("%v %+v", err, res)
	}
	// no PM: a clear refusal, and an override fixes it ("defaults, not locks")
	noPM := []pipeline.TeamMember{{ID: "w", Role: "Backend"}, {ID: "r", Role: "reviewer"}, {ID: "x", Role: "Frontend"}}
	if _, err = pipeline.ResolveRoles(names["feature"].Roles, "w", "r", noPM, nil); err == nil || !strings.Contains(err.Error(), "pm") {
		t.Fatalf("a missing PM must be refused: %v", err)
	}
	if res, err = pipeline.ResolveRoles(names["feature"].Roles, "w", "r", noPM, map[string]string{"pm": "x"}); err != nil || res.Roles["pm"] != "x" {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err = pipeline.ResolveRoles(names["feature"].Roles, "w", "r", noPM, map[string]string{"pm": "ghost"}); err == nil {
		t.Fatal("an override naming an agent outside the team is refused")
	}
	// the security reviewer must not be the worker
	if _, err = pipeline.ResolveRoles(names["security"].Roles, "s", "r", team, nil); err == nil {
		t.Fatal("security reviewer == worker must be refused")
	}
	if res, err = pipeline.ResolveRoles(names["security"].Roles, "w", "r", team, nil); err != nil || res.Roles["security"] != "s" {
		t.Fatalf("%v %+v", err, res)
	}
}

// The Bug template's pull request variant: reproduce and fix as before, then the branch is pushed and a PR opened, CI runs
// (the first run fails and goes back to the worker with no human), the human approves, and the merge goes through GitHub.
func TestBugTemplatePullRequestVariant(t *testing.T) {
	sc := map[string]fakeagent.Script{
		aQA: {Default: []fakeagent.Step{writeStep("double_test.go", reproTest), say("reproduced")}},
		aWork: {
			Rules:   []fakeagent.Rule{{Match: "CI job", Steps: []fakeagent.Step{writeStep("calc.go", fixedDouble+"\n// Double has a doc comment now.\n"), say("answered CI")}}},
			Default: []fakeagent.Step{writeStep("calc.go", fixedDouble), say("fixed")},
		},
		aReview: shipReviewer(),
	}
	f := &githubFixture{failFirst: true}
	e := newTpl(t, sc, tplOpts{files: map[string]string{"calc.go": buggyDouble}, title: "Double is off by one", desc: "Double(2) returns 5 instead of 4.", pr: f})
	id := e.start(pipeline.WorkflowBug+pipeline.PRSuffix, map[string]string{pipeline.RoleTests: aQA})
	e.await(id, workflow.StatusWaitingHuman)
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("pending %+v", p)
	}
	f.mu.Lock()
	opened := f.opened
	f.mu.Unlock()
	if opened != 1 {
		t.Fatalf("PR opened %d times", opened)
	}
	ev := e.evidence()
	if len(ev["red_test"]) != 1 || len(ev["ci"]) != 2 || len(ev["pr"]) != 1 {
		t.Fatalf("evidence: red %d ci %d pr %d", len(ev["red_test"]), len(ev["ci"]), len(ev["pr"]))
	}
	if wp := e.prompts(aWork); len(wp) != 2 || !strings.Contains(wp[1], "CI") {
		t.Fatalf("the CI failure must go back to the worker: %q", wp)
	}
	e.respond(id, "approved", "human:kyle", `{}`)
	e.await(id, workflow.StatusCompleted)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.merged {
		t.Fatal("the merge must go through GitHub")
	}
	// nothing was merged locally: main is unchanged
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "calc.go")); strings.Contains(string(m), "return a + a }") {
		t.Fatal("a pull request variant must not merge locally")
	}
}
