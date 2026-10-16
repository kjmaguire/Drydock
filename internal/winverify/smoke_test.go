package winverify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDecodeEnvelope(t *testing.T) {
	var out struct {
		ID string `json:"id"`
	}
	if err := decodeEnvelope("project.create", 200, []byte(`{"result":{"id":"proj_1"}}`), &out); err != nil || out.ID != "proj_1" {
		t.Fatalf("%v %+v", err, out)
	}
	var re *rpcError
	err := decodeEnvelope("task.start", 400, []byte(`{"error":"a task without a team cannot start"}`+"\n"), nil)
	if !errors.As(err, &re) || re.Status != 400 || re.Msg != "a task without a team cannot start" {
		t.Fatalf("refused call: %v", err)
	}
	// the transport's guards answer in plain text
	if err := decodeEnvelope("app.info", 401, []byte("unauthorized\n"), nil); !errors.As(err, &re) || re.Msg != "unauthorized" {
		t.Fatalf("guard: %v", err)
	}
	if err := decodeEnvelope("app.info", 200, []byte("<html>"), nil); err == nil || !strings.Contains(err.Error(), "envelope") {
		t.Fatalf("not JSON: %v", err)
	}
	if err := decodeEnvelope("task.cancel", 200, []byte(`{"result":null}`), &out); err != nil {
		t.Fatalf("a null result: %v", err)
	}
}

// The client speaks the UI's transport (ui/app/api/transport.ts): POST /rpc/<method>, JSON body, X-Drydock-Token. The
// server here is a TEST DOUBLE of the core's HTTP envelope, for the request shape and error handling only; the real core
// is driven in TestTheCoreStepsAgainstTheRealE2ECore.
func TestCallSpeaksTheUITransport(t *testing.T) {
	var seen struct {
		method, path, token, ctype string
		body                       map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.method, seen.path, seen.token, seen.ctype = r.Method, r.URL.Path, r.Header.Get("X-Drydock-Token"), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &seen.body)
		if seen.path == "/rpc/task.start" {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"no team"}`)
			return
		}
		io.WriteString(w, `{"result":"run_123"}`)
	}))
	defer srv.Close()
	s := &coreSession{base: srv.URL, token: "tok", http: srv.Client()}
	var runID string
	if err := s.call(context.Background(), "task.get", map[string]string{"id": "task_1"}, &runID); err != nil || runID != "run_123" {
		t.Fatalf("%v %q", err, runID)
	}
	if seen.method != "POST" || seen.path != "/rpc/task.get" || seen.token != "tok" || seen.ctype != "application/json" || seen.body["id"] != "task_1" {
		t.Fatalf("request %+v", seen)
	}
	if err := s.call(context.Background(), "app.info", nil, nil); err != nil || seen.body == nil {
		t.Fatalf("a call without parameters sends {}: %v %v", err, seen.body)
	}
	err := s.call(context.Background(), "task.start", map[string]string{"id": "x"}, nil)
	var re *rpcError
	if !errors.As(err, &re) || re.Msg != "no team" || !strings.Contains(err.Error(), "task.start: HTTP 400") {
		t.Fatalf("%v", err)
	}
}

func TestTerminalProbeAndEscapeStripping(t *testing.T) {
	for shell, typed := range map[string]string{`C:\Program Files\PowerShell\7\pwsh.exe`: "Write-Output", `C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe`: "Write-Output",
		`C:\WINDOWS\system32\cmd.exe`: "set /a", "/bin/sh": "echo $(("} {
		cmd, want := terminalProbe(shell)
		if !strings.HasPrefix(cmd, typed) || want != "8534567" || strings.Contains(cmd, want) {
			t.Errorf("%s: %q / %q (the typed text must not contain the answer)", shell, cmd, want)
		}
	}
	// ConPTY output: a title, colours and cursor moves around the answer
	screen := "\x1b]0;C:\\WINDOWS\\system32\\cmd.exe\x07\x1b[?25l\x1b[93mWrite-Output\x1b[0m (7300000+1234567)\r\n\x1b[?25h8534567\r\n\x1b[2;1HPS C:\\work> "
	if got := stripANSI(screen); !strings.Contains(got, "8534567") || strings.Contains(got, "\x1b") || !strings.Contains(got, "PS C:\\work>") {
		t.Fatalf("%q", got)
	}
}

// buildE2E builds cmd/drydock-e2e into a bundle-shaped folder with the fixture project, as drydock-winbundle lays it out.
func buildE2E(t *testing.T) (*Env, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the e2e core")
	}
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("needs %s", tool)
		}
	}
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "cmd", "drydock-e2e")); err != nil {
		t.Skip("cmd/drydock-e2e is not here (a bundle run): the core-smoke step covers it")
	}
	bundle := t.TempDir()
	m := &Manifest{E2E: "drydock-e2e" + exeSuffix(runtime.GOOS), Fixture: "tests/testdata/fixtures/gocalc", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	cmd := exec.Command("go", "build", "-o", filepath.Join(bundle, m.E2E), "./cmd/drydock-e2e")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/drydock-e2e: %v\n%s", err, out)
	}
	if err := copyTree(filepath.Join(root, "testdata", "fixtures"), filepath.Join(bundle, "tests", "testdata", "fixtures"), nil); err != nil {
		t.Fatal(err)
	}
	env := &Env{Bundle: bundle, Manifest: m, goFound: true}
	return env, bundle
}

// The three core steps against the real core (cmd/drydock-e2e: real SQLite, git, workflow engine, checks, merge; the agent
// is its scripted test double), exactly as drydock-verify runs them from a bundle.
func TestTheCoreStepsAgainstTheRealE2ECore(t *testing.T) {
	env, _ := buildE2E(t)
	logs := t.TempDir()
	var progress bytes.Buffer
	res := RunSteps(context.Background(), []Step{coreSmokeStep(), coreTerminalStep(), coreShutdownStep()}, env, logs, &progress)
	for _, r := range res {
		if r.Status != Pass {
			b, _ := os.ReadFile(filepath.Join(logs, logName(r.ID)))
			t.Fatalf("%s: %s %s\n%s\nlog:\n%s", r.ID, r.Status, r.Reason, r.Excerpt, b)
		}
	}
	for _, want := range []string{"COMPLETED", "the merge landed", "test pass", "SHIP"} {
		if !strings.Contains(res[0].Excerpt, want) {
			t.Errorf("smoke excerpt lacks %q:\n%s", want, res[0].Excerpt)
		}
	}
	if !strings.Contains(res[1].Excerpt, "printed 8534567") || !strings.Contains(res[2].Excerpt, "stopped by itself") ||
		!strings.Contains(res[2].Excerpt, "no process of") {
		t.Errorf("terminal:\n%s\nshutdown:\n%s", res[1].Excerpt, res[2].Excerpt)
	}
	b, _ := os.ReadFile(filepath.Join(logs, logName("core-smoke")))
	if strings.Contains(string(b), "token=") && !strings.Contains(string(b), "token=[REDACTED]") {
		t.Fatalf("the core's token reached the log:\n%s", b)
	}
	if env.core != nil {
		t.Fatal("the shutdown step must hand the core back")
	}
}

// When the core cannot start, the smoke fails with the reason and the steps that need the core skip with theirs.
func TestTheCoreStepsWithoutACore(t *testing.T) {
	env := &Env{Bundle: t.TempDir(), Manifest: &Manifest{E2E: "drydock-e2e" + exeSuffix(runtime.GOOS), Fixture: "tests/testdata/fixtures/gocalc"}, goFound: true}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git (the smoke skips without it)")
	}
	res := RunSteps(context.Background(), []Step{coreSmokeStep(), coreTerminalStep(), coreShutdownStep()}, env, t.TempDir(), nil)
	if res[0].Status != Fail || !strings.Contains(res[0].Reason, "the bundle has no drydock-e2e") {
		t.Fatalf("smoke %+v", res[0])
	}
	for _, r := range res[1:] {
		if r.Status != Skip || !strings.Contains(r.Reason, "core-smoke") {
			t.Fatalf("%s: %+v", r.ID, r)
		}
	}
	env.goFound = false
	res = RunSteps(context.Background(), []Step{coreSmokeStep()}, env, t.TempDir(), nil)
	if res[0].Status != Skip || !strings.Contains(res[0].Reason, "go build, go test and go vet") {
		t.Fatalf("without Go: %+v", res[0])
	}
}
