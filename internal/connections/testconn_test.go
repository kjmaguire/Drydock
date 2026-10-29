package connections_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/connections"
)

// fakeACPService installs the scripted ACP agent (a copy of this test binary) as `fakeacp-bin` on PATH, describes it with a
// manifest, and returns a Service over just that manifest.
func fakeACPService(t *testing.T, script fakeacp.Script, mut ...func(*connections.Config)) *connections.Service {
	t.Helper()
	dir := t.TempDir()
	self, _ := os.Executable()
	bin := filepath.Join(dir, "fakeacp-bin")
	if runtime.GOOS == "windows" {
		bin += ".exe" // Windows finds programs on PATH by extension (PATHEXT)
	}
	if err := os.Symlink(self, bin); err != nil {
		if err := copyFile(self, bin); err != nil {
			t.Fatalf("cannot install the stand-in agent: %v", err)
		}
	}
	b, _ := json.Marshal(script)
	sp := filepath.Join(dir, "script.json")
	_ = os.WriteFile(sp, b, 0o644)
	t.Setenv("PATH", dir)
	t.Setenv("DRYDOCK_FAKE_ACP", "1")
	t.Setenv("DRYDOCK_FAKE_ACP_SCRIPT", sp)
	const y = `
meta: {schema: 1, id: fakeacp, display_name: Scripted ACP agent, kind: agent_cli, license: MIT, tier: cheap}
binaries: {native: [fakeacp-bin]}
install: [{shell: sh, command: 'echo this must never be executed'}]
detect: {version: {argv: [--version], regex: '(\d+\.\d+\.\d+)'}}
versions: {min: "0.0.1", recommended: "0.0.1"}
transports: {primary: acp, acp: {argv: [fakeacp-bin]}}
capabilities_declared: {session_resume: true, token_usage: true, model_selection: true}
`
	set, err := manifests.Load(fstest.MapFS{"m/fakeacp.yaml": {Data: []byte(y)}}, "m")
	if err != nil {
		t.Fatal(err)
	}
	cp, err := manifests.LoadCompat([]byte("schema: 1\nnode: {min: '22'}\nadapters:\n  fakeacp: {min: '0.0.1', recommended: '0.0.1', entries: []}\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := connections.Config{Manifests: manifests.Options{Set: set, Compat: cp}}
	for _, m := range mut {
		m(&cfg)
	}
	svc, err := connections.NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func stepMap(r connections.Report) map[string]connections.Step {
	m := map[string]connections.Step{}
	for _, s := range r.Steps {
		m[s.ID] = s
	}
	return m
}

func TestHandshakeOnlyTestSpendsNothingAndReportsTheMatrix(t *testing.T) {
	svc := fakeACPService(t, fakeacp.Script{Resume: true, MCPHTTP: true, Models: []string{"m1", "m2"}, Modes: []string{"ask", "plan"}})
	opts := connections.TestOptions{}
	if d := connections.Describe(opts); !strings.Contains(d, "No prompt is sent") {
		t.Fatalf("describe: %s", d)
	}
	rep, err := svc.TestConnection(context.Background(), "fakeacp", opts)
	if err != nil {
		t.Fatal(err)
	}
	s := stepMap(rep)
	for _, id := range []string{"T1", "T2", "T2b"} {
		if s[id].Status != connections.StepPass {
			t.Errorf("%s: %+v", id, s[id])
		}
	}
	for _, id := range []string{"T3", "T4", "T5", "T6", "T7"} {
		if s[id].Status != connections.StepSkipped || s[id].Reason == "" {
			t.Errorf("%s must be skipped with a reason: %+v", id, s[id])
		}
	}
	if !rep.OK || rep.TokensUsed != 0 || len(rep.Usage) != 0 {
		t.Fatalf("%+v", rep)
	}
	// capability matrix with provenance
	want := map[string]struct {
		val  any
		prov string
	}{
		"session_resume": {true, "observed"}, "image_input": {false, "observed"}, "mcp_http": {true, "observed"}, "mcp_sse": {false, "observed"},
		"model_selection": {true, "observed"}, "interrupt": {true, "declared"}, "tool_call_visibility": {true, "declared"},
		"token_usage":  {true, "declared"}, // from the manifest until a prompt shows it
		"session_load": {false, "observed"}, "session_resume_method": {"resume", "observed"},
	}
	for k, w := range want {
		if c, ok := rep.Matrix[k]; !ok || c.Value != w.val || c.Provenance != w.prov {
			t.Errorf("matrix[%s] = %+v, want %+v", k, c, w)
		}
	}
	if !rep.Capabilities.SessionResume || !rep.Capabilities.ModelSelection || len(rep.Capabilities.PermissionModes) != 2 {
		t.Errorf("caps %+v", rep.Capabilities)
	}
	if !strings.Contains(strings.Join(rep.AgentMatrix, " "), "session.resume=true") {
		t.Errorf("agent matrix %v", rep.AgentMatrix)
	}
	// plain-language limits: no image input, no effort control
	joined := strings.Join(rep.Degraded, "\n")
	if !strings.Contains(joined, "Image input") || !strings.Contains(joined, "Effort control") {
		t.Errorf("degraded notes: %q", joined)
	}
	if strings.Contains(joined, "Session resume") || strings.Contains(joined, "MCP over HTTP") {
		t.Errorf("notes for things that work: %q", joined)
	}
	if !strings.HasPrefix(rep.Summary, "fakeacp 0.0.1 over acp: Test connection passed") {
		t.Errorf("summary %q", rep.Summary)
	}
	// the manifest's install text was displayed, never executed (it would have failed loudly or left a file)
	if rep.Platform == "" || rep.Form != "short" {
		t.Errorf("%+v", rep)
	}
}

func TestPromptStepMeasuresUsageAndRecordsProvenance(t *testing.T) {
	svc := fakeACPService(t, fakeacp.Script{Models: []string{"m1"}, PromptUsage: &fakeacp.PromptUsage{In: 11, Out: 3, CachedRead: 2},
		Turns: [][]fakeacp.Step{{{Say: "OK"}}}})
	rep, err := svc.TestConnection(context.Background(), "fakeacp", connections.TestOptions{Prompt: true, Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	s := stepMap(rep)
	if s["T3"].Status != connections.StepPass || !strings.Contains(s["T3"].Detail, `"OK"`) || !strings.Contains(s["T3"].Detail, "usage reported: yes") {
		t.Fatalf("T3 %+v", s["T3"])
	}
	if s["T5"].Status != connections.StepPass {
		t.Fatalf("T5 %+v", s["T5"])
	}
	if rep.TokensUsed != 16 || len(rep.Usage) != 1 || rep.Usage[0].Source != "reported" {
		t.Fatalf("tokens %d usage %+v", rep.TokensUsed, rep.Usage)
	}
	if c := rep.Matrix["token_usage"]; c.Value != true || c.Provenance != "observed" {
		t.Fatalf("token_usage %+v", c)
	}
	if c := rep.Matrix["cost_reporting"]; c.Value != false || c.Provenance != "observed" {
		t.Fatalf("cost_reporting %+v", c)
	}
	if c := rep.Matrix["streaming"]; c.Value != true || c.Provenance != "observed" {
		t.Fatalf("streaming %+v", c)
	}
	joined := strings.Join(rep.Degraded, "\n")
	if !strings.Contains(joined, "Cost: the agent reported no dollar cost") || strings.Contains(joined, "Token usage") {
		t.Fatalf("degraded %q", joined)
	}
	if !strings.Contains(connections.Describe(connections.TestOptions{Prompt: true, Model: "m1"}), `model "m1"`) {
		t.Fatal("describe must name the model before it runs")
	}
}

func TestPromptWithoutUsageSaysSoInsteadOfEstimating(t *testing.T) {
	svc := fakeACPService(t, fakeacp.Script{Turns: [][]fakeacp.Step{{{Say: "OK"}}}})
	rep, _ := svc.TestConnection(context.Background(), "fakeacp", connections.TestOptions{Prompt: true, Model: "anything"})
	s := stepMap(rep)
	if s["T3"].Status != connections.StepPass || rep.TokensUsed != 0 || len(rep.Usage) != 0 {
		t.Fatalf("%+v tokens=%d", s["T3"], rep.TokensUsed)
	}
	if c := rep.Matrix["token_usage"]; c.Value != false || c.Provenance != "observed" {
		t.Fatalf("token_usage %+v", c)
	}
	if !strings.Contains(strings.Join(rep.Degraded, "\n"), "records none rather than estimating") {
		t.Fatalf("degraded %v", rep.Degraded)
	}
}

func TestPromptIsSkippedWithoutOptInOrModel(t *testing.T) {
	svc := fakeACPService(t, fakeacp.Script{})
	rep, _ := svc.TestConnection(context.Background(), "fakeacp", connections.TestOptions{Prompt: true})
	if s := stepMap(rep)["T3"]; s.Status != connections.StepSkipped || !strings.Contains(s.Reason, "no model is configured") {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(connections.Describe(connections.TestOptions{Prompt: true}), "No prompt will be sent") {
		t.Fatal("describe")
	}
}

func TestCancelStepProvesInterruptKeepsTheSessionAlive(t *testing.T) {
	svc := fakeACPService(t, fakeacp.Script{Rules: []fakeacp.Rule{{Match: "Count from", Steps: []fakeacp.Step{{Say: "1 2 3 "}, {Block: true}}}}, Default: []fakeacp.Step{{Say: "OK"}}})
	rep, _ := svc.TestConnection(context.Background(), "fakeacp", connections.TestOptions{Prompt: true, Model: "m", Cancel: true})
	s := stepMap(rep)
	if s["T3"].Status != connections.StepPass || s["T4"].Status != connections.StepPass || !strings.Contains(s["T4"].Detail, "cancelled") || s["T5"].Status != connections.StepPass {
		t.Fatalf("%+v", rep.Steps)
	}
	if c := rep.Matrix["interrupt"]; c.Value != true || c.Provenance != "observed" {
		t.Fatalf("interrupt %+v", c)
	}
}

func TestAuthRequiredIsReportedInPlainLanguage(t *testing.T) {
	svc := fakeACPService(t, fakeacp.Script{AuthRequired: true, Name: "needs-login"})
	rep, _ := svc.TestConnection(context.Background(), "fakeacp", connections.TestOptions{Prompt: true, Model: "m"})
	s := stepMap(rep)
	if s["T2"].Status != connections.StepPass || s["T2b"].Status != connections.StepFail || !strings.Contains(s["T2b"].Reason, "needs you to sign in or add an API key") {
		t.Fatalf("%+v", rep.Steps)
	}
	if s["T3"].Status != connections.StepSkipped || !strings.Contains(s["T3"].Reason, "no session could be created") {
		t.Fatalf("T3 %+v", s["T3"])
	}
	if rep.OK {
		t.Fatal("a failed session/new is not a pass")
	}
	if !strings.Contains(strings.Join(rep.Degraded, "\n"), "sign in") {
		t.Fatalf("degraded %v", rep.Degraded)
	}
	if got := rep.FailureReason(); !strings.HasPrefix(got, "T2b:") {
		t.Fatalf("failure reason %q", got)
	}
	// recording the failed test degrades the Connection until a passing one
	svc.RecordTest("fakeacp", rep)
	c, _ := svc.Check(context.Background(), "fakeacp")
	if c.Status != agents.StatusDegraded || !strings.HasPrefix(c.Reason, "test_failed:") {
		t.Fatalf("%+v", c)
	}
}

func TestMissingCLIIsSkippedNotFaked(t *testing.T) {
	svc := fakeACPService(t, fakeacp.Script{})
	t.Setenv("PATH", t.TempDir())
	rep, _ := svc.TestConnection(context.Background(), "fakeacp", connections.TestOptions{Prompt: true, Model: "m"})
	s := stepMap(rep)
	if s["T1"].Status != connections.StepFail {
		t.Fatalf("T1 %+v", s["T1"])
	}
	for _, id := range []string{"T2", "T3", "T4", "T5", "T6", "T7"} {
		if s[id].Status != connections.StepSkipped || !strings.Contains(s[id].Reason, "T1 did not pass") {
			t.Errorf("%s: %+v", id, s[id])
		}
	}
	if rep.OK {
		t.Fatal("nothing ran: this must not read as a pass")
	}
	if _, err := svc.TestConnection(context.Background(), "nope", connections.TestOptions{}); err == nil {
		t.Fatal("unknown adapter id")
	}
}

func TestGateBlocksStartUntilAcknowledged(t *testing.T) {
	stubEnv(t, stubTable{"agy": {"--version": ok1("1.3.1\n"), "-p /usage": ok1("ok\n")}})
	svc, err := connections.NewService(connections.Config{})
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := svc.TestConnection(context.Background(), "antigravity", connections.TestOptions{})
	s := stepMap(rep)
	if s["T1"].Status != connections.StepPass || s["T2"].Status != connections.StepSkipped || !strings.Contains(s["T2"].Reason, "antigravity-third-party-v1") {
		t.Fatalf("%+v", rep.Steps)
	}
	svc.Acknowledge("antigravity-third-party-v1")
	// from here the agy on PATH is the stand-in written from agy's documentation (headless/fakeagy), not the probe stub
	t.Setenv("DRYDOCK_FAKE_AGY", "1")
	rep, _ = svc.TestConnection(context.Background(), "antigravity", connections.TestOptions{})
	s = stepMap(rep)
	if s["T2"].Status != connections.StepPass || s["T5"].Status != connections.StepPass {
		t.Fatalf("after acknowledging, the documented headless mode starts and stops: %+v", rep.Steps)
	}
	if !strings.Contains(strings.Join(rep.Degraded, "\n"), "marked unverified") {
		t.Fatalf("degraded %v", rep.Degraded)
	}
}

// claude, driven through the registry's headless adapter against the scripted Claude stream-json agent (fakeagent): a real
// process speaking the real dialect, labelled as a stand-in. The real CLI is covered by TestRealClaudeConnection.
func TestClaudeHeadlessConnectionAgainstTheScriptedAgent(t *testing.T) {
	self, _ := os.Executable()
	dir := t.TempDir()
	script := fakeagent.Script{Rules: []fakeagent.Rule{{Match: "Count from", Steps: []fakeagent.Step{{Say: "1 2 3"}, {Block: true}}}}, Default: []fakeagent.Step{{Say: "OK"}}}
	b, _ := json.Marshal(script)
	sp := filepath.Join(dir, "s.json")
	_ = os.WriteFile(sp, b, 0o644)
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", sp)
	svc, err := connections.NewService(connections.Config{Manifests: manifests.Options{ClaudeExe: self}})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.TestConnection(context.Background(), "claude", connections.TestOptions{Prompt: true, Model: "fake-1", Cancel: true})
	if err != nil {
		t.Fatal(err)
	}
	s := stepMap(rep)
	for _, id := range []string{"T1", "T2", "T3", "T4", "T5"} {
		if s[id].Status != connections.StepPass {
			t.Errorf("%s: %+v", id, s[id])
		}
	}
	if rep.CLIVersion != "9.9.9" || rep.Transport != "headless" {
		t.Errorf("%+v", rep)
	}
	if rep.TokensUsed == 0 || rep.Usage[0].Source != "reported" {
		t.Errorf("usage %+v tokens=%d", rep.Usage, rep.TokensUsed)
	}
	if c := rep.Matrix["interrupt"]; c.Value != true || c.Provenance != "observed" {
		t.Errorf("interrupt %+v", c)
	}
	// headless claude: the manifest's declarations stand, cells without a declaration are unknown
	if c := rep.Matrix["image_input"]; c.Value != false || c.Provenance != "unknown" {
		t.Errorf("image_input %+v", c)
	}
	// ... and the report says so in plain words: what is left off, why, and what would verify it
	want := "Not yet seen working with this CLI, so Drydock does not rely on it: image input. A contract test that runs the real CLI (docs/contract-tests.md) verifies it, and the CLI's manifest then declares it; until then Drydock leaves it off."
	if !slices.Contains(rep.Degraded, want) {
		t.Errorf("degraded %q", rep.Degraded)
	}
}

func TestPassingTestClearsVersionUntestedForThatVersionOnly(t *testing.T) {
	svc := fakeACPService(t, fakeacp.Script{})
	ctx := context.Background()
	c, _ := svc.Check(ctx, "fakeacp")
	if c.Status != agents.StatusDegraded || c.Reason != connections.ReasonVersionUntested {
		t.Fatalf("before any Test: %+v", c)
	}
	rep, _ := svc.TestConnection(ctx, "fakeacp", connections.TestOptions{})
	if !rep.OK {
		t.Fatalf("%+v", rep)
	}
	svc.RecordTest("fakeacp", rep)
	if c, _ = svc.Check(ctx, "fakeacp"); c.Status != agents.StatusReady {
		t.Fatalf("after a passing Test: %+v", c)
	}
	rep.CLIVersion = "0.0.2" // a different version was tested
	svc.RecordTest("fakeacp", rep)
	if c, _ = svc.Check(ctx, "fakeacp"); c.Status != agents.StatusDegraded {
		t.Fatalf("a pass on another version must not clear it: %+v", c)
	}
}

func TestPackageLevelTestConnection(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	rep, err := connections.TestConnection(context.Background(), "opencode", connections.TestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || stepMap(rep)["T1"].Status != connections.StepFail || stepMap(rep)["T2"].Status != connections.StepSkipped {
		t.Fatalf("%s", rep.Summary)
	}
	if _, err := connections.TestConnection(context.Background(), "nope", connections.TestOptions{}); err == nil {
		t.Fatal("unknown adapter")
	}
}
