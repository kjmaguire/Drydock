package checks_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/proc"
)

// These tests use the real external tools on this machine: node (to stand in for a project's own e2e/preview commands)
// and, for the starter, the Playwright that is already installed in testdata/starter-cloudflare-saas/node_modules. They
// never install anything. They are skipped, with the reason, when node is absent.

func needNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed on this machine (Node 22+ is a documented prerequisite, decision D9)")
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectE2EFindsOnlyWhatTheProjectDefinesAndNeverInstalls(t *testing.T) {
	// nothing defined: honest "not defined", with the places looked at
	empty := t.TempDir()
	if p := checks.DetectE2E(empty); p.Defined || !strings.Contains(p.Reason, "defines no e2e target") {
		t.Fatalf("%+v", p)
	}

	// a package.json script
	pj := t.TempDir()
	write(t, pj, "package.json", `{"scripts":{"build":"x","e2e":"playwright test"}}`)
	p := checks.DetectE2E(pj)
	if !p.Defined || p.Source != "package.json:e2e" || strings.Join(p.Spec.Argv, " ") != "npm run e2e --silent" || p.Spec.Name != "e2e" {
		t.Fatalf("%+v", p)
	}
	// test:e2e wins over e2e
	write(t, pj, "package.json", `{"scripts":{"e2e":"a","test:e2e":"b"}}`)
	if p := checks.DetectE2E(pj); p.Source != "package.json:test:e2e" {
		t.Fatalf("%+v", p)
	}

	// a Taskfile target: used only when go-task is installed, otherwise reported as the reason
	tf := t.TempDir()
	write(t, tf, "Taskfile.yml", "version: '3'\ntasks:\n  test:e2e:\n    cmds: [echo hi]\n")
	p = checks.DetectE2E(tf)
	if _, err := exec.LookPath("task"); err == nil {
		if !p.Defined || p.Source != "taskfile:test:e2e" {
			t.Fatalf("%+v", p)
		}
	} else if p.Defined || !strings.Contains(p.Reason, "go-task is not installed") {
		t.Fatalf("without go-task the Taskfile target cannot run, and the reason must say so: %+v", p)
	}

	// a Playwright config whose runner is not installed is NOT defined (npx would download it); installed, it runs --no-install
	pw := t.TempDir()
	write(t, pw, "playwright.config.ts", "export default {}")
	if p := checks.DetectE2E(pw); p.Defined || !strings.Contains(p.Reason, "not installed") {
		t.Fatalf("%+v", p)
	}
	write(t, pw, "node_modules/@playwright/test/package.json", `{"name":"@playwright/test"}`)
	p = checks.DetectE2E(pw)
	if !p.Defined || p.Source != "playwright.config" || strings.Join(p.Spec.Argv, " ") != "npx --no-install playwright test" {
		t.Fatalf("%+v", p)
	}
}

func TestRunE2ESkippedIsHonest(t *testing.T) {
	dir := t.TempDir()
	r, err := checks.RunE2E(context.Background(), proc.New(), dir, checks.E2EOptions{Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Skipped || r.Passed || !checks.IsSkipped(r.Result) || !strings.Contains(r.SkipReason, "no e2e target") {
		t.Fatalf("an undefined e2e must be a skip, never a pass: %+v", r)
	}
	r, _ = checks.RunE2E(context.Background(), proc.New(), dir, checks.E2EOptions{Enabled: false, Command: []string{"node", "-e", "process.exit(1)"}}, nil)
	if !r.Skipped || !strings.Contains(r.SkipReason, "switched off") {
		t.Fatalf("a disabled project setting must skip even when a command is configured: %+v", r)
	}
	if checks.IsSkipped(checks.Result{}) {
		t.Fatal("a zero result is not a skip")
	}
}

const pwReportScript = `
const fs = require('fs');
fs.mkdirSync('test-results', {recursive: true});
fs.writeFileSync('test-results/shot1.png', 'png-bytes');
const fail = process.env.E2E_FAIL === '1';
const report = {suites: [{title: 'app.spec.ts', specs: [], suites: [{title: 'nested', specs: [
  {title: 'login works', file: 'app.spec.ts', tests: [{status: 'expected', results: [{attachments: [{name: 'screenshot', contentType: 'image/png', path: process.cwd() + '/test-results/shot1.png'}]}]}]},
  {title: 'create flow', file: 'app.spec.ts', tests: [{status: fail ? 'unexpected' : 'expected', results: [{attachments: [{name: 'trace', contentType: 'application/zip', path: '/x/trace.zip'}]}]}]},
  {title: 'later', file: 'app.spec.ts', tests: [{status: 'skipped', results: []}]}
], suites: []}]}]};
fs.writeFileSync('test-results/e2e-report.json', JSON.stringify(report));
console.log(fail ? '  ✘  2 [chromium] › app.spec.ts:20:1 › create flow (1.2s)' : '  ok');
process.exit(fail ? 1 : 0);
`

func TestRunE2EReadsPlaywrightReportAndKeepsScreenshotsOutsideTheWorktree(t *testing.T) {
	needNode(t)
	dir := t.TempDir()
	write(t, dir, "run.js", pwReportScript)
	logs := t.TempDir()
	opts := checks.E2EOptions{Enabled: true, Command: []string{"node", "run.js"}, LogDir: logs}

	r, err := checks.RunE2E(context.Background(), proc.New(), dir, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Skipped || !r.Passed || r.Summary == nil || r.Summary.Expected != 2 || r.Summary.Skipped != 1 || r.Summary.Unexpected != 0 || r.Source != "settings" {
		t.Fatalf("%+v", r)
	}
	if len(r.Screenshots) != 1 || !strings.HasPrefix(r.Screenshots[0], logs) {
		t.Fatalf("screenshots must be copied under the log dir (the worktree is deleted after merge), only images: %v", r.Screenshots)
	}
	if b, err := os.ReadFile(r.Screenshots[0]); err != nil || string(b) != "png-bytes" {
		t.Fatalf("copy: %v %q", err, b)
	}

	opts.Env = []string{"E2E_FAIL=1"}
	r, err = checks.RunE2E(context.Background(), proc.New(), dir, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed || r.ExitCode != 1 || r.Summary == nil || r.Summary.Unexpected != 1 || len(r.FailingTests) != 1 || r.FailingTests[0] != "app.spec.ts > create flow" {
		t.Fatalf("a failing e2e names the failing test from the report: %+v", r)
	}
	if b, _ := os.ReadFile(r.LogRef); !strings.Contains(string(b), "create flow") {
		t.Fatal("the full log must be on disk")
	}
}

func TestRunE2EDoesNotTrustAStaleReport(t *testing.T) {
	needNode(t)
	dir := t.TempDir()
	write(t, dir, "test-results/e2e-report.json", `{"suites":[{"title":"x","specs":[{"title":"old","file":"a.ts","tests":[{"status":"unexpected","results":[]}]}],"suites":[]}]}`)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "test-results/e2e-report.json"), old, old); err != nil {
		t.Fatal(err)
	}
	r, err := checks.RunE2E(context.Background(), proc.New(), dir, checks.E2EOptions{Enabled: true, Command: []string{"node", "-e", "console.log('fine')"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || r.Summary != nil {
		t.Fatalf("a report from before this run says nothing about it: %+v", r)
	}
}

// The real starter project: detection finds its e2e target, and the real Playwright installed in its node_modules is run
// through the e2e check as an external tool (listing tests needs no browser and no network).
func TestStarterProjectE2EPlumbingWithRealPlaywright(t *testing.T) {
	needNode(t)
	starter, err := filepath.Abs(filepath.Join("..", "..", "testdata", "starter-cloudflare-saas"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(starter, "node_modules", "@playwright", "test")); err != nil {
		t.Skip("testdata/starter-cloudflare-saas/node_modules is not installed here (an untracked directory); Drydock never runs npm install for you")
	}
	plan := checks.DetectE2E(starter)
	if !plan.Defined || !strings.Contains(plan.Source, "e2e") {
		t.Fatalf("the starter defines an e2e target: %+v", plan)
	}
	t.Logf("starter e2e detected via %s: %s", plan.Source, strings.Join(plan.Spec.Argv, " "))
	r, err := checks.RunE2E(context.Background(), proc.New(), starter,
		checks.E2EOptions{Enabled: true, Command: []string{"npx", "--no-install", "playwright", "test", "--list"}, LogDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Skipped || !r.Passed || !strings.Contains(r.Output, "Total: 4 tests") {
		t.Fatalf("%+v", r)
	}
}

// Running the whole starter e2e needs a Chromium and wrangler's workerd; it is opt-in so a normal run does not depend on them.
func TestStarterProjectFullE2E(t *testing.T) {
	if os.Getenv("DRYDOCK_RUN_STARTER_E2E") != "1" {
		t.Skip("set DRYDOCK_RUN_STARTER_E2E=1 (and PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH or an installed Playwright Chromium) to run the starter's browser tests; no Chromium is installed in this sandbox")
	}
	starter, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "starter-cloudflare-saas"))
	r, err := checks.RunE2E(context.Background(), proc.New(), starter, checks.E2EOptions{Enabled: true, LogDir: t.TempDir(), Timeout: 8 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Skipped || !r.Passed {
		t.Fatalf("%s\n%+v", r.Output, r)
	}
	if len(r.Screenshots) == 0 {
		t.Fatal("the starter's Playwright config takes screenshots")
	}
}

// ---- preview ----

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

const previewServer = `
const http = require('http');
const port = Number(process.env.PREVIEW_PORT);
const delay = Number(process.env.PREVIEW_DELAY_MS || 0);
setTimeout(() => {
  const srv = http.createServer((req, res) => { res.statusCode = req.url === '/api/health' ? 200 : 404; res.end('ok'); });
  srv.listen(port, '127.0.0.1', () => {
    if (process.env.PREVIEW_PRINT === '1') console.log('Preview available at http://127.0.0.1:' + srv.address().port + '/api/health');
  });
}, delay);
`

func reachable(url string) bool {
	c := &http.Client{Timeout: time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func TestPreviewStartsProbesPostsTheURLAndStopsTheProcess(t *testing.T) {
	needNode(t)
	dir := t.TempDir()
	write(t, dir, "server.js", previewServer)
	port := freePort(t)
	health := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	h, r, err := checks.StartPreview(context.Background(), proc.New(), dir, checks.PreviewOptions{
		Enabled: true, Command: []string{"node", "server.js"}, HealthURL: health, Timeout: 20 * time.Second, LogDir: t.TempDir(),
		Env: []string{fmt.Sprintf("PREVIEW_PORT=%d", port), "PREVIEW_DELAY_MS=400"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h == nil || !r.Passed || r.URL != health || r.Skipped || r.Name != "preview" || !strings.Contains(r.Output, health) {
		t.Fatalf("%+v", r)
	}
	if !reachable(health) {
		t.Fatal("the preview must be serving while the handle is held")
	}
	h.Stop()
	select {
	case <-h.Exited():
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not end the process")
	}
	if reachable(health) {
		t.Fatal("the preview must be down after Stop")
	}
	h.Stop() // idempotent
}

func TestPreviewTakesTheURLTheCommandPrints(t *testing.T) {
	needNode(t)
	dir := t.TempDir()
	write(t, dir, "server.js", previewServer)
	// port 0: the server takes a port the system gives it, so only the URL it prints can say where it is (a port picked
	// here and closed again could be taken by another test before the server binds it)
	h, r, err := checks.StartPreview(context.Background(), proc.New(), dir, checks.PreviewOptions{
		Enabled: true, Command: []string{"node", "server.js"}, Timeout: 20 * time.Second,
		Env: []string{"PREVIEW_PORT=0", "PREVIEW_PRINT=1"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	if !r.Passed || !regexp.MustCompile(`^http://127\.0\.0\.1:[1-9][0-9]*/api/health$`).MatchString(r.URL) || !reachable(r.URL) {
		t.Fatalf("%+v", r)
	}
}

func TestPreviewFailuresAreReportedAndNothingIsLeftRunning(t *testing.T) {
	needNode(t)
	dir := t.TempDir()
	// the command dies before any URL answers
	h, r, err := checks.StartPreview(context.Background(), proc.New(), dir, checks.PreviewOptions{
		Enabled: true, Command: []string{"node", "-e", "console.error('boom'); process.exit(3)"}, HealthURL: "http://127.0.0.1:1/never", Timeout: 10 * time.Second,
	}, nil)
	if err != nil || h != nil || r.Passed || !strings.Contains(r.Output, "code 3") || !strings.Contains(r.Output, "boom") {
		t.Fatalf("h=%v err=%v %+v", h, err, r)
	}
	// nothing ever answers
	port := freePort(t)
	start := time.Now()
	h, r, err = checks.StartPreview(context.Background(), proc.New(), dir, checks.PreviewOptions{
		Enabled: true, Command: []string{"node", "-e", "setTimeout(()=>{}, 60000)"}, HealthURL: fmt.Sprintf("http://127.0.0.1:%d/", port), Timeout: 1500 * time.Millisecond,
	}, nil)
	if err != nil || h != nil || r.Passed || !strings.Contains(r.Output, "no answer") || time.Since(start) > 20*time.Second {
		t.Fatalf("h=%v err=%v %+v", h, err, r)
	}
	// a command that exits cleanly without printing a URL
	h, r, err = checks.StartPreview(context.Background(), proc.New(), dir, checks.PreviewOptions{
		Enabled: true, Command: []string{"node", "-e", "console.log('done')"}, Timeout: 5 * time.Second,
	}, nil)
	if err != nil || h != nil || r.Passed || !strings.Contains(r.Output, "without printing a URL") {
		t.Fatalf("h=%v err=%v %+v", h, err, r)
	}
}

func TestPreviewRefusesDeploymentCommandsAndHonoursSettings(t *testing.T) {
	for _, argv := range [][]string{
		{"npx", "wrangler", "deploy"},
		{"npm", "run", "publish"},
		{"wrangler", "versions", "upload", "--env", "production"},
		{"task", "x", "--environment=production"},
	} {
		if err := checks.RefusePreviewCommand(argv); !errors.Is(err, checks.ErrPreviewDeploy) {
			t.Errorf("%v must be refused, got %v", argv, err)
		}
	}
	if err := checks.RefusePreviewCommand([]string{"npx", "wrangler", "preview", "--name", "x"}); err != nil {
		t.Errorf("wrangler preview is the preview command: %v", err)
	}
	h, _, err := checks.StartPreview(context.Background(), proc.New(), t.TempDir(), checks.PreviewOptions{Enabled: true, Command: []string{"wrangler", "deploy"}}, nil)
	if !errors.Is(err, checks.ErrPreviewDeploy) || h != nil {
		t.Fatalf("a deploy command must not start: %v", err)
	}
	h, r, err := checks.StartPreview(context.Background(), proc.New(), t.TempDir(), checks.PreviewOptions{Enabled: false, Command: []string{"node", "-v"}}, nil)
	if err != nil || h != nil || !r.Skipped || !strings.Contains(r.SkipReason, "switched off") {
		t.Fatalf("%v %+v", err, r)
	}
	h, r, _ = checks.StartPreview(context.Background(), proc.New(), t.TempDir(), checks.PreviewOptions{Enabled: true}, nil)
	if h != nil || !r.Skipped || !strings.Contains(r.SkipReason, "no preview command") {
		t.Fatalf("%+v", r)
	}
	// detection: a package.json preview script
	pj := t.TempDir()
	write(t, pj, "package.json", `{"scripts":{"preview":"x"}}`)
	if argv, src, ok := checks.DetectPreview(pj); !ok || src != "package.json:preview" || argv[0] != "npm" {
		t.Fatalf("%v %s %v", argv, src, ok)
	}
}
