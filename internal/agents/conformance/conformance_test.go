package conformance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/conformance"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agents/headless/fakeagy"
	"github.com/kjmaguire/drydock/internal/agents/headless/fakecustom"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// TestMain turns the test binary into one of the two scripted stand-in agents when asked to (the runtime tests' re-exec
// pattern): fakeagent speaks Claude Code's stream-json dialect, fakeacp speaks ACP on the real SDK. Both are real separate
// processes and are labelled as stand-ins; the real CLIs are covered by the Real and Live tests below.
func TestMain(m *testing.M) {
	switch {
	case os.Getenv("DRYDOCK_FAKE_AGENT") == "1":
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	case os.Getenv("DRYDOCK_FAKE_ACP") == "1":
		os.Exit(fakeacp.Run(os.Args[1:], os.Stdin, os.Stdout))
	case os.Getenv("DRYDOCK_FAKE_AGY") == "1": // agy, written from its documentation (headless/fakeagy)
		os.Exit(fakeagy.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	case os.Getenv("DRYDOCK_FAKE_CUSTOM") == "1": // a person's own prompt-and-exit CLI (headless/fakecustom)
		os.Exit(fakecustom.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	code := m.Run()
	if exampleDir != "" {
		_ = os.RemoveAll(exampleDir)
	}
	os.Exit(code)
}

func writeJSON(t *testing.T, v any) string {
	t.Helper()
	b, _ := json.Marshal(v)
	p := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- claude headless -------------------------------------------------------------------------------------------------

func TestClaudeAdapterAgainstTheScriptedAgent(t *testing.T) {
	script := fakeagent.Script{
		Rules: []fakeagent.Rule{
			{Match: "tool please", Steps: []fakeagent.Step{{Say: "reading the file"}, {Tool: "Read", Input: json.RawMessage(`{"file_path":"hello.txt"}`), Result: "hello from file"}, {Say: "hello from file"}}},
			{Match: "long please", Steps: []fakeagent.Step{{Say: "working "}, {Block: true}}},
		},
		Default: []fakeagent.Step{{Say: "OK"}},
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	t.Setenv("DRYDOCK_FAKE_SCRIPT", writeJSON(t, script))
	ad := &claude.Adapter{Exe: os.Args[0]}
	conformance.Run(t, ad, conformance.Options{
		ExpectText: "OK", ExpectUsage: true,
		ToolPrompt: "tool please", ExpectTools: true, ExpectToolArgs: true,
		LongPrompt: "long please",
		// headless claude answers approvals by permission mode or --permission-prompt-tool, so no client-side request can be triggered
		PermissionPrompt: "",
	})
}

func TestClaudeAdapterAgainstTheRealCLI(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "") // never leak the stand-in switch into a real run
	ad := claude.New()
	conformance.Run(t, ad, conformance.Options{
		Spec:       agents.RunSpec{Model: "haiku", AllowedTools: []string{"Read"}, ExtraArgs: []string{"--no-session-persistence"}},
		ExpectText: "OK", ExpectUsage: true,
		ToolPrompt: "Use the Read tool to read hello.txt in the current directory, then reply with its exact content only.", ExpectTools: true, ExpectToolArgs: true,
		LongPrompt: "Count from 1 to 400, one number per line, no other text.",
	})
}

// ---- Antigravity, headless (decision P115) ----------------------------------------------------------------------------

// The adapter the registry builds from antigravity.yaml, against the stand-in written from agy's documentation: a real process
// on the documented stream-json mode. No real agy was run; tool calls, interrupt and resume are not declared, so their steps
// are skipped with the reason.
func TestAntigravityAdapterAgainstTheDocumentedStandIn(t *testing.T) {
	t.Setenv("DRYDOCK_FAKE_AGY", "1")
	t.Setenv("DRYDOCK_FAKE_AGY_SCRIPT", writeJSON(t, fakeagy.Script{Default: []fakeagy.Step{{Think: "short answer"}, {Say: "OK", Usage: &[3]int64{40, 2, 8}}}}))
	r, err := manifests.NewRegistry(manifests.Options{Exe: map[string]string{"antigravity": os.Args[0]}})
	if err != nil {
		t.Fatal(err)
	}
	ad, err := r.Build("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	conformance.Run(t, ad, conformance.Options{ExpectText: "OK", ExpectUsage: true})
}

// ---- a person's own CLI: the custom dialect (decision P115) --------------------------------------------------------------

// customManifest is the template made a person's own, run by the generic runner against the prompt-and-exit stand-in.
func customManifest(t *testing.T, id string) agents.Adapter {
	t.Helper()
	tpl, err := manifests.CustomTemplate()
	if err != nil {
		t.Fatal(err)
	}
	y := strings.Replace(string(tpl), "id: custom,", "id: "+id+",", 1)
	y = strings.Replace(y, "gate: {id: custom-runs-arbitrary-code-v1,", "gate: {id: custom-"+id+",", 1)
	m, err := manifests.ParseUserCustom([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	r, err := manifests.NewRegistry(manifests.Options{Exe: map[string]string{id: os.Args[0]}})
	if err != nil {
		t.Fatal(err)
	}
	ad, err := r.AddCustom(m, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	return ad
}

func customScript(t *testing.T) {
	t.Setenv("DRYDOCK_FAKE_CUSTOM", "1")
	t.Setenv("DRYDOCK_FAKE_CUSTOM_SCRIPT", writeJSON(t, fakecustom.Script{
		Rules: []fakecustom.Rule{
			{Match: "tool please", Steps: []fakecustom.Step{{Say: "reading the file "}, {Tool: "read_file", Args: map[string]any{"path": "hello.txt"}, Out: "hello from file"}, {Say: "hello from file"}}},
			{Match: "long please", Steps: []fakecustom.Step{{Say: "working "}, {SleepMS: 60000}, {Say: "done"}}},
		},
		Default: []fakecustom.Step{{Say: "OK"}},
	}))
}

func TestCustomDialectJSONLinesAgainstTheStandIn(t *testing.T) {
	customScript(t)
	ad := customManifest(t, "mycli")
	conformance.Run(t, ad, conformance.Options{
		PerTurnProcess: true,
		ExpectText:     "OK", ExpectUsage: true,
		ToolPrompt: "tool please", ExpectTools: true, ExpectToolArgs: true,
		LongPrompt: "long please",
	})
}

func TestCustomDialectPlainTextAgainstTheStandIn(t *testing.T) {
	customScript(t)
	ad := customManifestText(t)
	conformance.Run(t, ad, conformance.Options{
		PerTurnProcess: true, NoSessionID: true,
		ExpectText: "OK", ExpectNoUsage: true,
		LongPrompt: "long please",
	})
}

// customManifestText is a plain-text CLI: the stand-in with --text, the prompt as an argument.
func customManifestText(t *testing.T) agents.Adapter {
	t.Helper()
	y := `meta: {schema: 1, id: plaincli, display_name: Plain, kind: agent_cli, custom: true, run_modes: [native]}
binaries: {native: [plain-cli]}
detect: {version: {argv: [--version], regex: '(\d+\.\d+\.\d+)'}}
gate: {id: custom-plaincli, required: true, text: "Runs a program you chose."}
transports:
  primary: headless
  headless:
    argv: [run, --text, --prompt, "{{prompt}}"]
    prompt: {via: arg}
    dialect: {format: text}
`
	m, err := manifests.ParseUserCustom([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	r, err := manifests.NewRegistry(manifests.Options{Exe: map[string]string{"plaincli": os.Args[0]}})
	if err != nil {
		t.Fatal(err)
	}
	ad, err := r.AddCustom(m, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	return ad
}

// ---- generic ACP adapter ---------------------------------------------------------------------------------------------

func TestACPAdapterAgainstTheScriptedACPAgent(t *testing.T) {
	script := fakeacp.Script{
		Models: []string{"m1"}, PromptUsage: &fakeacp.PromptUsage{In: 10, Out: 2},
		Rules: []fakeacp.Rule{
			{Match: "tool please", Steps: []fakeacp.Step{{Say: "reading "}, {Tool: &fakeacp.Tool{ID: "t1", Title: "Read hello.txt", Kind: "read", Input: map[string]any{"path": "hello.txt"}, Output: "hello from file"}}}},
			{Match: "long please", Steps: []fakeacp.Step{{Say: "working "}, {Block: true}}},
			{Match: "permission please", Steps: []fakeacp.Step{{Perm: &fakeacp.Perm{Tool: fakeacp.Tool{ID: "p1", Title: "Write config", Input: map[string]any{"path": "config.json"}}}}}},
		},
		Default: []fakeacp.Step{{Say: "OK"}},
	}
	t.Setenv("DRYDOCK_FAKE_ACP", "1")
	t.Setenv("DRYDOCK_FAKE_ACP_SCRIPT", writeJSON(t, script))
	ad := acp.New(acp.Config{ID: "fakeacp", Command: os.Args[0]})
	conformance.Run(t, ad, conformance.Options{
		ExpectText: "OK", ExpectUsage: true,
		ToolPrompt: "tool please", ExpectTools: true, ExpectToolArgs: true,
		LongPrompt:       "long please",
		PermissionPrompt: "permission please",
		AfterPermission: func(t *testing.T, cs []workflow.Chunk) {
			var txt strings.Builder
			for _, c := range cs {
				if c.Kind == workflow.ChunkText {
					var d workflow.TextDelta
					_ = c.Decode(&d)
					txt.WriteString(d.Text)
				}
			}
			if !strings.Contains(txt.String(), "permission: selected reject") {
				t.Errorf("the default handler must reject, agent saw %q", txt.String())
			}
		},
	})
}

var (
	exampleOnce sync.Once
	exampleDir  string
	exampleBin  string
	exampleErr  error
)

// exampleAgent builds the Go SDK's own example agent (github.com/coder/acp-go-sdk/example/agent) into a temp dir.
func exampleAgent(t *testing.T) string {
	t.Helper()
	exampleOnce.Do(func() {
		exampleDir, exampleErr = os.MkdirTemp("", "acp-example-agent-")
		if exampleErr != nil {
			return
		}
		exampleBin = filepath.Join(exampleDir, "example-agent")
		if filepath.Separator == '\\' {
			exampleBin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", exampleBin, "github.com/coder/acp-go-sdk/example/agent")
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=readonly")
		if out, err := cmd.CombinedOutput(); err != nil {
			exampleErr = &buildErr{err, string(out)}
		}
	})
	if exampleErr != nil {
		t.Skipf("cannot build the SDK example agent: %v", exampleErr)
	}
	return exampleBin
}

type buildErr struct {
	err error
	out string
}

func (b *buildErr) Error() string { return b.err.Error() + ": " + strings.TrimSpace(b.out) }

func TestACPAdapterAgainstTheSDKExampleAgent(t *testing.T) {
	ad := acp.New(acp.Config{ID: "sdk-example", Command: exampleAgent(t)})
	const prompt = "please improve the project"
	conformance.Run(t, ad, conformance.Options{
		Prompt: prompt, ExpectText: "skip the configuration update",
		ExpectNoUsage: true, // the example agent reports no usage and none may be invented
		ExpectTools:   true, ExpectToolArgs: true,
		LongPrompt:       prompt,
		PermissionPrompt: prompt,
		AfterPermission: func(t *testing.T, cs []workflow.Chunk) {
			for _, c := range cs {
				if c.Kind == workflow.ChunkToolResult {
					var tr workflow.ToolResult
					_ = c.Decode(&tr)
					if tr.ID == "call_2" && !tr.IsError && strings.Contains(tr.Output, "Configuration updated") {
						t.Errorf("the rejected call was reported as having run")
					}
				}
			}
		},
	})
}

// ---- live handshakes against the real ACP CLIs (no model credentials here, so no prompts) -----------------------------

func liveACP(t *testing.T, id, envName, bin string, args []string, extraEnv ...string) *acp.Adapter {
	t.Helper()
	cmd, cmdArgs := bin, args
	if v := os.Getenv("DRYDOCK_ACP_" + envName); v != "" {
		f := strings.Fields(v)
		cmd, cmdArgs = f[0], append(f[1:], args...)
	} else if p, err := exec.LookPath(bin); err == nil {
		cmd = p
	} else {
		t.Skipf("%s is not installed here (not on PATH and DRYDOCK_ACP_%s is unset): live handshake not run", bin, envName)
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	env := append([]string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home + "/.config", "XDG_DATA_HOME=" + home + "/.local/share",
		"XDG_STATE_HOME=" + home + "/.local/state", "XDG_CACHE_HOME=" + home + "/.cache", "CODEX_HOME=" + home + "/.codex"}, extraEnv...)
	return acp.New(acp.Config{ID: id, Command: cmd, Args: cmdArgs, Env: env, ScrubEnv: true})
}

func TestLiveACPHandshakes(t *testing.T) {
	for _, tc := range []struct {
		id, env, bin string
		args         []string
		extra        []string
	}{
		{"gemini", "GEMINI", "gemini", []string{"--acp"}, []string{"GEMINI_CLI_TRUST_WORKSPACE=true", "NO_BROWSER=1"}},
		{"opencode", "OPENCODE", "opencode", []string{"acp"}, nil},
		{"goose", "GOOSE", "goose", []string{"acp"}, []string{"GOOSE_MODE=approve"}},
		{"codex-acp", "CODEX_ACP", "codex-acp", nil, nil},
		{"cline", "CLINE", "cline", []string{"--acp"}, nil},
	} {
		t.Run(tc.id, func(t *testing.T) {
			ad := liveACP(t, tc.id, tc.env, tc.bin, tc.args, tc.extra...)
			conformance.Run(t, ad, conformance.Options{
				HandshakeOnly: true,
				SkipReason:    "no model credentials in this environment: a real prompt to " + tc.id + " cannot run, so the turn-based steps are not faked",
			})
		})
	}
}
