package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// a synthetic GitHub-token-shaped string (the same shape the room redaction test uses); it is not a credential
var fakeToken = "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "aB3dE5"

// Security template: scan, fix, verify, re-scan, a security-role reviewer, human approval. The deps scan is skipped honestly
// (no osv-scanner here) and a scan the build does not know is reported as not available, never as a pass.
func TestSecurityTemplateScansFixesRescansAndNeedsAHumanMerge(t *testing.T) {
	sc := map[string]fakeagent.Script{
		aWork: {
			Rules:   []fakeagent.Rule{{Match: "security scan of the tree found", Steps: []fakeagent.Step{writeStep("config.txt", "token=${DEPLOY_TOKEN}\n"), say("moved the token to an environment variable")}}},
			Default: []fakeagent.Step{say("nothing to do")},
		},
		aSec:    shipReviewerStep(),
		aReview: shipReviewer(),
	}
	e := newTpl(t, sc, tplOpts{files: map[string]string{"config.txt": "token=" + fakeToken + "\n"}, title: "Security audit of config", desc: "Audit the repository for leaked secrets and fix what is found."})
	e.td.DepsBinary = "/nonexistent/osv-scanner"
	// rebuild with the missing binary so the deps scan is forced to skip
	pipeline.RegisterTemplates(e.eng, e.td)
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "secrets", "deps", "sast-by-nobody")
	e.await(id, workflow.StatusWaitingHuman)
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("pending %+v", p)
	}
	ev, _ := e.tasks.Evidence(context.Background(), e.task.ID)
	var secrets []tasks.Evidence
	byKind := map[string][]tasks.Evidence{}
	for _, x := range ev {
		byKind[x.Kind] = append(byKind[x.Kind], x)
		if x.Kind == "secrets" {
			secrets = append(secrets, x)
		}
	}
	// the first scan failed (the token), the gate scan after the fix passed; the finding never shows the secret
	var sawFail, sawPass bool
	for _, x := range secrets {
		if x.Passed != nil && !*x.Passed {
			sawFail = true
		}
		if x.Passed != nil && *x.Passed {
			sawPass = true
		}
		if strings.Contains(x.Summary, fakeToken) {
			t.Fatal("the secret must never be in evidence")
		}
	}
	if !sawFail || !sawPass {
		t.Fatalf("secrets evidence: fail=%v pass=%v (%d items)", sawFail, sawPass, len(secrets))
	}
	for _, kind := range []string{"deps", "sast-by-nobody"} {
		if len(byKind[kind]) == 0 || byKind[kind][0].Passed != nil || !strings.Contains(byKind[kind][0].Summary, "skipped") {
			t.Fatalf("%s must be recorded as skipped without a verdict: %+v", kind, byKind[kind])
		}
	}
	if !strings.Contains(byKind["sast-by-nobody"][0].Summary, "not available in this build") {
		t.Fatalf("%+v", byKind["sast-by-nobody"])
	}
	wp := e.prompts(aWork)
	if len(wp) != 1 || !strings.Contains(wp[0], "security scan of the tree found") || !strings.Contains(wp[0], "secrets: FAILED") || strings.Contains(wp[0], fakeToken) {
		t.Fatalf("the worker must be told the findings (never the secret): %q", wp)
	}
	sp := e.prompts(aSec)
	if len(sp) != 1 || !strings.Contains(sp[0], "Drydock phase: security-review") || !strings.Contains(sp[0], "secrets") {
		t.Fatalf("security prompt %q", sp)
	}
	if len(e.prompts(aReview)) != 0 {
		t.Fatal("the generic reviewer is replaced by the security-role reviewer in this template")
	}
	rv, _ := e.tasks.Reviews(context.Background(), e.task.ID)
	if len(rv) != 1 || rv[0].Reviewer != aSec || rv[0].Verdict != "SHIP" {
		t.Fatalf("reviews %+v", rv)
	}
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "config.txt")); !strings.Contains(string(m), fakeToken) {
		t.Fatal("main must still hold the old file until the human merges")
	}
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusCompleted)
	if m, _ := os.ReadFile(filepath.Join(e.repo.Root, "config.txt")); strings.Contains(string(m), "ghp_") || !strings.Contains(string(m), "DEPLOY_TOKEN") {
		t.Fatalf("merged config.txt:\n%s", m)
	}
}

func shipReviewerStep() fakeagent.Script { return shipReviewer() }

// The security reviewer can send the work back, and a scan that still fails at the gate loops to the worker.
func TestSecurityGateLoopsBackWhileAFindingRemains(t *testing.T) {
	sc := map[string]fakeagent.Script{
		aWork: {
			// first pass leaves the token in place, second pass (after the gate feedback) removes it
			Rules:   []fakeagent.Rule{{Match: "check failed", Steps: []fakeagent.Step{writeStep("config.txt", "token=${DEPLOY_TOKEN}\n"), say("removed it for real")}}},
			Default: []fakeagent.Step{writeStep("notes.txt", "looked at it\n"), say("looked, found nothing")},
		},
		aSec:    shipReviewerStep(),
		aReview: shipReviewer(),
	}
	e := newTpl(t, sc, tplOpts{files: map[string]string{"config.txt": "token=" + fakeToken + "\n"}, title: "Audit", desc: "Audit for secrets."})
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "secrets")
	e.await(id, workflow.StatusWaitingHuman)
	wp := e.prompts(aWork)
	if len(wp) != 2 || !strings.Contains(wp[1], `"secrets" check failed`) {
		t.Fatalf("the second pass carries the failing scan: %q", wp)
	}
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("pending %+v", p)
	}
}

func TestSecurityReviewerCanSendWorkBack(t *testing.T) {
	sc := map[string]fakeagent.Script{
		aWork: {
			Rules:   []fakeagent.Rule{{Match: "Reviewer feedback", Steps: []fakeagent.Step{writeStep("config.txt", "token=${DEPLOY_TOKEN}\nlog_level=info\n"), say("added the missing setting")}}},
			Default: []fakeagent.Step{writeStep("config.txt", "token=${DEPLOY_TOKEN}\n"), say("fixed")},
		},
		aSec: {
			Rules:   []fakeagent.Rule{{Match: "log_level=info", Steps: []fakeagent.Step{say(`{"verdict":"SHIP","feedback":""}`)}}},
			Default: []fakeagent.Step{say(`{"verdict":"REVISE","feedback":"also set log_level=info so tokens are never logged"}`)},
		},
		aReview: shipReviewer(),
	}
	e := newTpl(t, sc, tplOpts{files: map[string]string{"config.txt": "token=" + fakeToken + "\n"}, title: "Audit", desc: "Audit."})
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "secrets")
	e.await(id, workflow.StatusWaitingHuman)
	rv, _ := e.tasks.Reviews(context.Background(), e.task.ID)
	if len(rv) != 2 || rv[0].Verdict != "REVISE" || rv[1].Verdict != "SHIP" {
		t.Fatalf("reviews %+v", rv)
	}
	if wp := e.prompts(aWork); len(wp) != 2 || !strings.Contains(wp[1], "log_level=info") {
		t.Fatalf("worker prompts %q", wp)
	}
}

const e2eRunner = `
const fs = require('fs');
fs.mkdirSync('test-results', {recursive: true});
fs.writeFileSync('test-results/home.png', 'png');
fs.writeFileSync('test-results/e2e-report.json', JSON.stringify({suites: [{title: 'app', specs: [
  {title: 'home loads', file: 'app.spec.ts', tests: [{status: 'expected', results: [{attachments: [{name: 'screenshot', contentType: 'image/png', path: process.cwd() + '/test-results/home.png'}]}]}]}
], suites: []}]}));
console.log('1 passed');
`

const previewScript = `
const http = require('http');
const port = Number(process.argv[2]);
http.createServer((req, res) => { res.statusCode = 200; res.end('preview of the change'); }).listen(port, '127.0.0.1');
`

// Full Stack SaaS: the Feature flow plus an optional design step, the e2e check, a security scan and a preview, with the
// project settings switching e2e and preview on. Both are external tools (node here), run for real.
func TestFullStackSaaSRunsDesignE2EScanAndPreviewBeforeTheHumanApproves(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed on this machine")
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	sc := featureScripts()
	sc[aArch] = fakeagent.Script{Default: []fakeagent.Step{say("Touch calc.go only; add Multiply next to Add.")}}
	e := newTpl(t, sc, tplOpts{preview: true, files: map[string]string{".gitignore": "test-results/\n", "e2e-run.js": e2eRunner, "preview-server.js": previewScript}})
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	e.dev.E2E = pipeline.E2ESettings{Enabled: true, Command: []string{"node", "e2e-run.js"}}
	e.dev.Preview = pipeline.PreviewSetup{Enabled: true, Command: []string{"node", "preview-server.js", fmt.Sprint(port)}, URL: url}
	roles := featureRoles()
	roles[pipeline.RoleArchitect] = aArch

	id := e.start(pipeline.WorkflowSaaS, roles)
	e.await(id, workflow.StatusWaitingHuman)
	if n := len(e.prompts(aArch)) + len(e.prompts(aQA)) + len(e.prompts(aWork)); n != 0 {
		t.Fatalf("design, tests and work wait for the spec: %d turns", n)
	}
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman)
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("pending %+v", p)
	}
	ap := e.prompts(aArch)
	if len(ap) != 1 || !strings.Contains(ap[0], "Drydock phase: design") {
		t.Fatalf("architect prompts %q", ap)
	}
	if wp := e.prompts(aWork); len(wp) != 1 || !strings.Contains(wp[0], "Touch calc.go only") {
		t.Fatalf("the implementer gets the architect's notes: %q", wp)
	}
	ev := e.evidence()
	if len(ev["design"]) != 1 || len(ev["e2e"]) != 1 || ev["e2e"][0].Passed == nil || !*ev["e2e"][0].Passed || !strings.Contains(ev["e2e"][0].Summary, "1 passed") {
		t.Fatalf("evidence kinds %v e2e %+v", keys(ev), ev["e2e"])
	}
	if len(ev["screenshot"]) != 1 || !strings.Contains(ev["screenshot"][0].Ref, "e2e-screenshots") {
		t.Fatalf("screenshots %+v", ev["screenshot"])
	}
	if _, err := os.Stat(ev["screenshot"][0].Ref); err != nil {
		t.Fatalf("the screenshot copy must exist: %v", err)
	}
	if len(ev["secrets"]) == 0 || len(ev["preview"]) != 1 || ev["preview"][0].Passed == nil || !*ev["preview"][0].Passed || ev["preview"][0].Ref != url {
		t.Fatalf("scan/preview evidence: %v %+v", keys(ev), ev["preview"])
	}
	// the preview is up while the human decides
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("the preview must be running during the approval: %v", err)
	}
	resp.Body.Close()
	var sum pipeline.ApproveSummary
	json.Unmarshal(e.pending(id).Spec.Payload, &sum)
	haveURL := false
	for _, x := range sum.Evidence {
		haveURL = haveURL || (x.Kind == "preview" && x.Ref == url)
	}
	if !haveURL {
		t.Fatalf("the human's approval request must carry the preview URL: %+v", sum.Evidence)
	}
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusCompleted)
}

// With e2e and preview left at their defaults (off) the SaaS template records them as skipped, never as passed.
func TestSaaSE2EAndPreviewAreSkippedHonestlyWhenOff(t *testing.T) {
	e := newTpl(t, featureScripts(), tplOpts{})
	id := e.start(pipeline.WorkflowSaaS, featureRoles())
	e.await(id, workflow.StatusWaitingHuman)
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman)
	ev := e.evidence()
	if len(ev["e2e"]) != 1 || ev["e2e"][0].Passed != nil || !strings.Contains(ev["e2e"][0].Summary, "skipped") {
		t.Fatalf("e2e evidence %+v", ev["e2e"])
	}
	if len(ev["preview"]) != 1 || ev["preview"][0].Passed != nil || !strings.Contains(ev["preview"][0].Summary, "not available") {
		t.Fatalf("preview evidence %+v", ev["preview"])
	}
	if len(ev["design"]) != 0 {
		t.Fatal("no architect on the team: the design step is skipped")
	}
}

// Scans are pluggable by name: a scan registered on the template's registry runs like the built-in ones, and its result is
// evidence under its own name.
func TestSecurityTemplateRunsAPluggedInScanByName(t *testing.T) {
	sc := map[string]fakeagent.Script{aWork: {Default: []fakeagent.Step{say("nothing to change")}}, aSec: shipReviewerStep(), aReview: shipReviewer()}
	e := newTpl(t, sc, tplOpts{title: "Audit", desc: "Audit."})
	reg := pipeline.NewScanRegistry()
	reg.Register("govet", func(ctx context.Context, td pipeline.TemplateDeps, tk tasks.Task) (checks.Spec, error) {
		return checks.Spec{Name: "govet", Argv: []string{"go", "vet", "./..."}, Dir: tk.Worktree, Parser: "go"}, nil
	})
	e.td.Scans = reg
	if err := pipeline.RegisterTemplates(e.eng, e.td); err != nil {
		t.Fatal(err)
	}
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "govet")
	e.await(id, workflow.StatusWaitingHuman)
	ev := e.evidence()
	if len(ev["govet"]) != 2 || ev["govet"][0].Passed == nil || !*ev["govet"][0].Passed || ev["govet"][1].Passed == nil || !*ev["govet"][1].Passed {
		t.Fatalf("the plugged-in scan runs at the first scan and at the gate: %+v", ev["govet"])
	}
	if len(ev["secrets"])+len(ev["deps"]) > 0 {
		// Verify's own secrets check may add "secrets"; the template's default scans must not have run
		for _, x := range ev["deps"] {
			t.Fatalf("the default deps scan must not run when the run names its scans: %+v", x)
		}
	}
}
