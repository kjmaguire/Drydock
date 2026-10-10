package contract

import (
	"os"
	"strings"
)

// Environment variables shared by the runner and the contract tests.
const (
	// EnvRealClaude turns on the paid checks against the real claude (the existing real tests use the same switch).
	EnvRealClaude = "DRYDOCK_REAL_CLAUDE"
	// EnvNoRealAgents skips every real-CLI test (the repository's existing switch).
	EnvNoRealAgents = "DRYDOCK_NO_REAL_AGENTS"
	// EnvBudget is the spend cap in US dollars for the whole run; paid checks skip once it is spent.
	EnvBudget = "DRYDOCK_CONTRACT_BUDGET_USD"
	// EnvLedger is the file the paid checks append their spend to (one JSON line each), shared across test processes.
	EnvLedger = "DRYDOCK_CONTRACT_LEDGER"
	// EnvACPCredentials lists the ACP targets that are signed in here (comma separated), so their prompt-turn contracts run.
	EnvACPCredentials = "DRYDOCK_CONTRACT_ACP_CREDENTIALS"
)

// CLIDrydock is the CLI name of contracts that check Drydock's own code rather than a CLI.
const CLIDrydock = "drydock"

// Log line prefixes a contract test writes with t.Log; the runner collects them into the report.
const (
	NotePrefix    = "contract-note: "    // an observation worth reading (which model answered, what was seen)
	WarningPrefix = "contract-warning: " // the check passed, but something changed that needs a decision
	SpendPrefix   = "contract-spend: "   // what a paid check spent (also in the ledger)
)

// EnclosingSessionEnv are the variables a running Claude Code session sets for the processes it starts. A claude started with
// them reports the enclosing session's id and can attach to it (measured with 2.1.296: CLAUDE_CODE_REMOTE_SESSION_ID alone
// does it). The runner removes them from the test environment so the suite measures the CLI as it runs on a user's machine;
// the adapter strips the same list from every agent it starts (CLAUDE-SESSION-01).
var EnclosingSessionEnv = []string{
	"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_REMOTE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
}

// WithoutVars returns env without the named variables (names compare case-insensitively, as Windows does).
func WithoutVars(env []string, names ...string) []string {
	drop := map[string]bool{}
	for _, n := range names {
		drop[strings.ToUpper(n)] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[strings.ToUpper(k)] {
			out = append(out, kv)
		}
	}
	return out
}

// CleanEnv is the process environment without the enclosing Claude Code session's variables.
func CleanEnv() []string { return WithoutVars(os.Environ(), EnclosingSessionEnv...) }

// ACPTarget is an ACP agent the ACP contracts run against. Its arguments and environment come from the manifest's ACP
// transport (internal/agents/manifests/cli/<adapter>.yaml), so the contract follows what Drydock really starts.
type ACPTarget struct {
	Name    string // the program that speaks ACP: the subtest name and the report's CLI column
	Adapter string // manifest id
	Bin     string // command looked up on PATH
	// Env names the override DRYDOCK_ACP_<Env>: a full command prefix (the same variables the live tests read).
	Env string
	// Bridge is true for an ACP bridge Drydock starts with `npx -y <package>@<pin>`; the contract runs the installed bridge.
	Bridge bool
	// Extra is environment a headless start needs (taken from the live tests).
	Extra []string
	// NoAuthMethods is set for an agent that lists no authentication methods to a client like Drydock's: the Claude bridge
	// answers authMethods [] unless the client advertises terminal-auth (docs/phase0/agent-clis.md section 7).
	NoAuthMethods bool
}

// ACPTargets are the ACP agents Drydock can drive: the two npx bridges and the four CLIs that speak ACP themselves.
var ACPTargets = []ACPTarget{
	{Name: "claude-agent-acp", Adapter: "claude", Bin: "claude-agent-acp", Env: "CLAUDE_ACP", Bridge: true, NoAuthMethods: true},
	{Name: "codex-acp", Adapter: "codex", Bin: "codex-acp", Env: "CODEX_ACP", Bridge: true},
	{Name: "gemini", Adapter: "gemini", Bin: "gemini", Env: "GEMINI", Extra: []string{"GEMINI_CLI_TRUST_WORKSPACE=true", "NO_BROWSER=1"}},
	{Name: "opencode", Adapter: "opencode", Bin: "opencode", Env: "OPENCODE"},
	{Name: "goose", Adapter: "goose", Bin: "goose", Env: "GOOSE", Extra: []string{"GOOSE_MODE=approve"}},
	{Name: "cline", Adapter: "cline", Bin: "cline", Env: "CLINE"},
}

// ACPTargetByName returns the target with that name.
func ACPTargetByName(name string) (ACPTarget, bool) {
	for _, t := range ACPTargets {
		if t.Name == name {
			return t, true
		}
	}
	return ACPTarget{}, false
}

// OverrideVar is the environment variable that points the contract at a specific command for this target.
func (t ACPTarget) OverrideVar() string { return "DRYDOCK_ACP_" + t.Env }
