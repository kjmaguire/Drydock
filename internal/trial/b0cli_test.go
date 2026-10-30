package trial

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/acp/fakeacp"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
)

// B0 with a CLI other than Claude Code and on another provider (decision P121), proven against the repository's labelled test
// doubles: the scripted ACP agent (fakeacp) as every member of a team driven through Drydock's real ACP adapter, and the scripted
// stand-in for Claude Code (fakeagent). Neither is a real CLI or a real provider; nothing here measures what a team costs.

// acpTeam is a B0 whose team is the scripted ACP agent, as the adapter Drydock builds from an ACP manifest drives it.
func acpTeam(t *testing.T, cli string, script fakeacp.Script) (B0, Task, string) {
	t.Helper()
	root := repoRoot(t)
	x := task(t, "saasapi-invoice-rules")
	repo := t.TempDir()
	if _, err := Prepare(context.Background(), root, x, repo); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(script)
	sp := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(sp, b, 0o644); err != nil {
		t.Fatal(err)
	}
	ad := acp.New(acp.Config{ID: cli, Command: os.Args[0], Env: []string{"DRYDOCK_FAKE_ACP=1", "DRYDOCK_FAKE_ACP_SCRIPT=" + sp}, StartTimeout: 30 * time.Second})
	return B0{CLI: cli, Adapter: ad, Model: "m-1", CeilingUSD: 1, Wall: time.Minute, Root: root, Hidden: []string{"evals/testdata", "zz_hidden_"}}, x, repo
}

func cost(v float64) *float64 { return &v }

func TestB0RunsAnotherCLIThroughItsAdapterAndBroadcastsTheWholeRoom(t *testing.T) {
	x := task(t, "saasapi-invoice-rules")
	var b B0
	var repo string
	script := func(repo string) fakeacp.Script {
		return fakeacp.Script{
			PromptUsage: &fakeacp.PromptUsage{In: 40, Out: 8},
			Rules: []fakeacp.Rule{
				{Match: "You are the worker", Steps: []fakeacp.Step{
					{Tool: &fakeacp.Tool{ID: "t1", Title: "go test ./internal/billing/", Kind: "execute", Input: map[string]any{"command": "go test ./internal/billing/"},
						Output: "TOOL-OUTPUT-ACP-9: --- FAIL: TestRounding"}},
					{FSWrite: &fakeacp.FSOp{Path: filepath.Join(repo, "internal/billing/invoice.go"), Content: solution(t, x.ID, "internal/billing/invoice.go")}},
					{Say: "Fixed the rounding rules.\nDONE"},
					{UsageUpdate: &fakeacp.UsageUpdate{Used: 100, Size: 200000, TurnCost: cost(0.0003)}},
				}},
				// the reviewer agrees only when its prompt holds the worker's tool output: the room crossed in full
				{Match: "TOOL-OUTPUT-ACP-9", Steps: []fakeacp.Step{{Say: "I read the worker's test output and the fix.\nDONE"},
					{UsageUpdate: &fakeacp.UsageUpdate{Used: 120, Size: 200000, TurnCost: cost(0.0003)}}}},
			},
			Default: []fakeacp.Step{{Say: "The room did not reach me."}},
		}
	}
	repo = t.TempDir()
	if _, err := Prepare(context.Background(), repoRoot(t), x, repo); err != nil {
		t.Fatal(err)
	}
	b, _, _ = acpTeam(t, "gemini", script(repo))
	var transcript bytes.Buffer
	rec, room := b.Run(context.Background(), x, repo, &transcript)
	if rec.Outcome != "" || rec.Stop != "consensus" || rec.Rounds != 1 || rec.Sessions != 2 || rec.AgentCLI != "gemini" || rec.Provider != "" {
		t.Fatalf("record %+v", rec)
	}
	if !near(rec.CostUSD, 0.0006) || rec.CallsWithoutCost != 0 || rec.Tokens.Input != 80 || rec.Tokens.Output != 16 {
		t.Fatalf("cost and tokens add up to what the agent reported: %+v", rec)
	}
	if !strings.Contains(rec.ArmDetail, "2 gemini agents (worker, reviewer)") || len(rec.Notes) != 1 || !strings.Contains(rec.Notes[0], "no per-session budget flag") {
		t.Fatalf("%s %v", rec.ArmDetail, rec.Notes)
	}
	var calls, outputs int
	for _, e := range room {
		switch {
		case e.Kind == "tool_call" && e.Agent == "worker" && strings.Contains(e.Text, "go test ./internal/billing/"):
			calls++
		case e.Kind == "tool_output" && strings.Contains(e.Text, "TOOL-OUTPUT-ACP-9"):
			outputs++
		}
	}
	last := room[len(room)-1]
	if calls != 1 || outputs != 1 || last.Agent != "reviewer" || last.Kind != "said" || !strings.Contains(last.Text, "I read the worker's test output") {
		t.Fatalf("room %+v", room)
	}
	if n := strings.Count(transcript.String(), `"type":"b0_chunk"`); n == 0 || !strings.Contains(transcript.String(), `"agent":"reviewer"`) {
		t.Fatalf("the transcript holds both sessions' chunks: %d", n)
	}
	if res := Accept(context.Background(), b.Root, x, repo); !res.Passed {
		t.Fatalf("the worker wrote the reference solution through the client's file system, the hidden check must pass: %+v", res)
	}
}

func TestB0WithAnotherCLIStopsAtTheCeilingAndOnAFailedSession(t *testing.T) {
	t.Run("the ceiling, as the usage report crosses it", func(t *testing.T) {
		b, x, repo := acpTeam(t, "codex", fakeacp.Script{Default: []fakeacp.Step{{Say: "working"}, {UsageUpdate: &fakeacp.UsageUpdate{Used: 1, Size: 10, TurnCost: cost(0.002)}},
			{Say: "more"}}})
		b.CeilingUSD = 0.001
		rec, _ := b.Run(context.Background(), x, repo, &bytes.Buffer{})
		if rec.Outcome != Ceiling || rec.Stop != "ceiling" || rec.Sessions != 1 || !strings.Contains(rec.Reason, "during the worker's turn in round 1") {
			t.Fatalf("record %+v", rec)
		}
	})
	t.Run("a session that fails", func(t *testing.T) {
		b, x, repo := acpTeam(t, "codex", fakeacp.Script{Default: []fakeacp.Step{{ErrorPrompt: "model overloaded"}}})
		rec, _ := b.Run(context.Background(), x, repo, &bytes.Buffer{})
		if rec.Outcome != Failed || rec.Stop != "error" || rec.Sessions != 1 || !strings.Contains(rec.Reason, "the worker's codex session reported an error") || !strings.Contains(rec.Reason, "model overloaded") {
			t.Fatalf("record %+v", rec)
		}
	})
	t.Run("a CLI without its adapter", func(t *testing.T) {
		b, x, repo := acpTeam(t, "codex", fakeacp.Script{})
		b.Adapter = nil
		rec, _ := b.Run(context.Background(), x, repo, &bytes.Buffer{})
		if rec.Outcome != Failed || !strings.Contains(rec.Reason, "needs its adapter") || rec.Sessions != 0 {
			t.Fatalf("record %+v", rec)
		}
	})
}

const testKey = "b0-test-key-7Q2w9E4r6T8y"

func TestB0OnAnotherProviderWithClaudeCode(t *testing.T) {
	t.Setenv("B0_TEST_KEY", testKey)
	b, x, repo, _, argsLog := b0Rig(t, fakeagent.Script{Default: []fakeagent.Step{{Say: "Done here.\nDONE"}}})
	b.Provider = &B0Provider{ID: "cheapco", BaseURL: "https://api.cheap.example/anthropic", KeyEnv: "B0_TEST_KEY", InputPerMTok: 0.5, OutputPerMTok: 2}
	env := strings.Join(b.Env(nil), "\n")
	for _, want := range []string{"ANTHROPIC_BASE_URL=https://api.cheap.example/anthropic", "ANTHROPIC_AUTH_TOKEN=" + testKey, "ANTHROPIC_DEFAULT_SONNET_MODEL=haiku",
		"ANTHROPIC_SMALL_FAST_MODEL=haiku", "DRYDOCK_FAKE_AGENT=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("Claude Code's environment lacks %s", want)
		}
	}
	rec, _ := b.Run(context.Background(), x, repo, &bytes.Buffer{})
	if rec.Outcome != "" || rec.Stop != "consensus" || rec.AgentCLI != "claude" || rec.Provider != "cheapco" || rec.CostSource != "estimated" {
		t.Fatalf("record %+v", rec)
	}
	want := float64(rec.Tokens.Input+rec.Tokens.CacheRead+rec.Tokens.CacheWrite)*0.5/1e6 + float64(rec.Tokens.Output)*2/1e6
	if !near(rec.CostUSD, want) || rec.CostUSD == 0 || near(rec.CostUSD, 0.0004) {
		t.Fatalf("the cost is the provider's price of the tokens, not the CLI's figure: %v, want %v", rec.CostUSD, want)
	}
	if !strings.Contains(rec.ArmDetail, "on the provider cheapco (cost estimated at $0.5/$2 per million input/output tokens)") {
		t.Fatal(rec.ArmDetail)
	}
	raw, _ := json.Marshal(rec)
	args, _ := os.ReadFile(argsLog)
	if strings.Contains(string(raw), testKey) || strings.Contains(string(args), testKey) {
		t.Fatal("the provider's key is in the record or the command line")
	}
	if !strings.Contains(string(args), `"--model","haiku"`) {
		t.Fatalf("one model: %s", args)
	}
}

func TestB0OnAnotherProviderWithOpenCode(t *testing.T) {
	t.Setenv("B0_TEST_KEY", testKey)
	b, x, repo := acpTeam(t, "opencode", fakeacp.Script{Models: []string{"cheapco/m-1"},
		Default: []fakeacp.Step{{ReportConfig: true}, {ReportEnv: []string{"B0_TEST_KEY", "OPENCODE_CONFIG_CONTENT"}}, {Say: "\nDONE"}}})
	b.Provider = &B0Provider{ID: "cheapco", BaseURL: "https://api.cheap.example/v1", KeyEnv: "B0_TEST_KEY"}
	var cfg struct {
		Model      string `json:"model"`
		Compaction struct {
			Auto bool `json:"auto"`
		} `json:"compaction"`
		Provider map[string]struct {
			NPM     string `json:"npm"`
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
			Models map[string]any `json:"models"`
		} `json:"provider"`
	}
	for _, kv := range b.ProviderEnv() {
		if v, ok := strings.CutPrefix(kv, "OPENCODE_CONFIG_CONTENT="); ok {
			if err := json.Unmarshal([]byte(v), &cfg); err != nil {
				t.Fatal(err)
			}
		}
	}
	p := cfg.Provider["cheapco"]
	if cfg.Model != "cheapco/m-1" || !cfg.Compaction.Auto || p.NPM != "@ai-sdk/openai-compatible" || p.Options.BaseURL != "https://api.cheap.example/v1" ||
		p.Options.APIKey != "{env:B0_TEST_KEY}" || p.Models["m-1"] == nil {
		t.Fatalf("OpenCode's inline configuration: %+v", cfg)
	}
	rec, room := b.Run(context.Background(), x, repo, &bytes.Buffer{})
	if rec.Stop != "consensus" || rec.Provider != "cheapco" || rec.CostSource != "" || rec.CallsWithoutCost != 2 {
		t.Fatalf("record %+v", rec)
	}
	said := ""
	for _, e := range room {
		said += e.Text + "\n"
	}
	if !strings.Contains(said, "model=cheapco/m-1") || !strings.Contains(said, "B0_TEST_KEY="+testKey) || !strings.Contains(said, `"baseURL":"https://api.cheap.example/v1"`) {
		t.Fatalf("the session started on the provider's model with its environment:\n%s", said)
	}
}

func TestB0RefusesAProviderItCannotPointTheCLIAt(t *testing.T) {
	t.Setenv("B0_TEST_KEY", testKey)
	ok := B0Provider{ID: "cheapco", BaseURL: "https://api.cheap.example/v1", KeyEnv: "B0_TEST_KEY"}
	for name, c := range map[string]struct {
		cli string
		p   B0Provider
		why string
	}{
		"a CLI B0 cannot point":  {"gemini", ok, "does not know how to point gemini"},
		"plain http elsewhere":   {"claude", B0Provider{ID: "cheapco", BaseURL: "http://api.cheap.example", KeyEnv: "B0_TEST_KEY"}, "must be https"},
		"credentials in the URL": {"claude", B0Provider{ID: "cheapco", BaseURL: "https://u:p@api.cheap.example", KeyEnv: "B0_TEST_KEY"}, "without credentials"},
		"an empty key":           {"claude", B0Provider{ID: "cheapco", BaseURL: "https://api.cheap.example", KeyEnv: "B0_NO_SUCH_KEY"}, "is empty"},
		"a bad id":               {"claude", B0Provider{ID: "cheap co", BaseURL: "https://api.cheap.example", KeyEnv: "B0_TEST_KEY"}, "letters, digits"},
		"a negative price":       {"claude", B0Provider{ID: "cheapco", BaseURL: "https://api.cheap.example", KeyEnv: "B0_TEST_KEY", InputPerMTok: -1}, "negative"},
	} {
		b := B0{CLI: c.cli, Model: "m", Provider: &c.p}
		if c.cli != CLIClaude {
			b.Adapter = acp.New(acp.Config{ID: c.cli, Command: "unused"})
		}
		if err := b.Check(); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v", name, err)
		}
	}
	local := B0{Model: "m", Provider: &B0Provider{ID: "local", BaseURL: "http://127.0.0.1:8080", KeyEnv: "B0_TEST_KEY"}}
	if err := local.Check(); err != nil {
		t.Fatalf("a provider on this computer: %v", err)
	}
}
