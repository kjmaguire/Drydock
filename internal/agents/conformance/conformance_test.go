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
	if os.Getenv("DRYDOCK_NO_REAL_AGENTS") == "1" {
		t.Skip("DRYDOCK_NO_REAL_AGENTS=1")
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
