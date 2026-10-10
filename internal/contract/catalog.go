package contract

// Package paths of the tests the catalog refers to.
const (
	pkgContract    = "./internal/contract"
	pkgRuntime     = "./internal/agents/runtime"
	pkgConformance = "./internal/agents/conformance"
	pkgCore        = "./internal/core"
	pkgMCP         = "./internal/mcp"
)

// real is the environment of a paid check against the real claude.
var real = []string{EnvRealClaude + "=1"}

// All returns the catalog, in the order the report lists it.
func All() []Contract {
	cs := []Contract{
		{
			ID: "CLAUDE-ARGV-01", CLI: "claude", Adapter: "claude",
			Assumption: "Every flag Drydock passes when it starts Claude Code, and every permission mode it offers, is accepted by the CLI.",
			Why:        "The adapter builds one argv for every agent, reviewer, room reply and chat; a renamed or removed flag makes all of them fail to start.",
			Files:      []string{"internal/agents/claude/adapter.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestClaudeAcceptsTheFlagsDrydockPasses"}},
		},
		{
			ID: "CLAUDE-EFFORT-01", CLI: "claude", Adapter: "claude",
			Assumption: "--effort accepts each effort Drydock offers (low, medium, high, xhigh, max) without the CLI ignoring it.",
			Why:        "Agents and chats carry an effort the human chose; Claude Code only warns about an unknown value and then runs at its default, so a silently ignored effort changes cost and quality unseen.",
			Files:      []string{"internal/agents/claude/adapter.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestClaudeAcceptsEveryEffortDrydockOffers"}},
		},
		{
			ID: "CLAUDE-STREAM-01", CLI: "claude", Adapter: "claude",
			Assumption: "stream-json output has the system/init, assistant (text, tool_use), user (tool_result), stream_event text_delta and result events in the shape Drydock's parser reads.",
			Why:        "Every Claude turn reaches Drydock only through the parser: session id, streamed text, tool calls and results, usage and the stop reason.",
			Files:      []string{"internal/agents/claude/parser.go"},
			Tests: []TestRef{
				{Package: pkgContract, Test: "TestClaudeStreamJSONEventShapes", EstUSD: 0.02, Env: real},
				{Package: pkgConformance, Test: "TestClaudeAdapterAgainstTheRealCLI", EstUSD: 0.05},
			},
		},
		{
			ID: "CLAUDE-USAGE-01", CLI: "claude", Adapter: "claude",
			Assumption: "The result event reports total_cost_usd and a modelUsage map keyed by model id with token counts and costUSD.",
			Why:        "Budgets, the eval report and the cost shown for chats and runs come from these fields; a missing cost must show as unknown, never as zero.",
			Files:      []string{"internal/agents/claude/parser.go", "internal/agents/runtime/runtime.go"},
			Tests: []TestRef{
				{Package: pkgContract, Test: "TestClaudeResultReportsCostAndModelUsage", EstUSD: 0.01, Env: real},
				{Package: pkgRuntime, Test: "TestRealClaudeTurnWithToolUseAndUsage", EstUSD: 0.02},
			},
		},
		{
			ID: "CLAUDE-USAGE-02", CLI: "claude", Adapter: "claude",
			Assumption: "When one claude process answers several turns, each result's total_cost_usd and modelUsage are running totals of the process and usage is the turn's own; Drydock's parser records the difference, so each turn is counted once.",
			Why:        "The parser turns every result into one usage row and the runtime adds the rows up (an interrupted turn and its follow-up, watchdog nudges, kept sessions). Measured with 2.1.296: turn 2's modelUsage said 17,778 tokens = 8,854 + 8,924. If the CLI starts reporting each turn's own totals, the parser's rule would under-count, so the check fails then too.",
			Files:      []string{"internal/agents/claude/parser.go", "internal/agents/runtime/runtime.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestClaudeReportsEachTurnsOwnUsage", EstUSD: 0.01, Env: real}},
		},
		{
			ID: "CLAUDE-USAGE-03", CLI: "claude", Adapter: "claude",
			Assumption: "A session resumed in a new process (--resume) reports total_cost_usd and modelUsage including its earlier processes; started from the session's last totals (RunSpec.UsageBaseline), Drydock's parser records only the new turn.",
			Why:        "Chats resume their CLI session in a new process for every turn. Measured with 2.1.296: the resumed turn reported 17,824 tokens = 8,880 from the first process + 8,944 its own, and twice the cost, so without the baseline every chat turn after the first was counted with all the ones before it.",
			Files:      []string{"internal/agents/claude/parser.go", "internal/chats/turn.go"},
			Tests: []TestRef{
				{Package: pkgContract, Test: "TestClaudeResumedSessionReportsItsWholeTotals", EstUSD: 0.01, Env: real},
				{Package: pkgCore, Test: "TestAResumedChatSessionCountsEachTurnOnce"},
			},
		},
		{
			ID: "CLAUDE-PROMPTFILE-01", CLI: "claude", Adapter: "claude",
			Assumption: "A multi-line system prompt addition given with --append-system-prompt-file reaches the model in full, through its last line.",
			Why:        "Drydock passes purpose, practices and project memory in a file because cmd.exe cuts a multi-line argument to claude.cmd at the first line break.",
			Files:      []string{"internal/agents/claude/adapter.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestClaudeAppendSystemPromptFileReachesTheModel", EstUSD: 0.01, Env: real}},
		},
		{
			ID: "CLAUDE-RO-01", CLI: "claude", Adapter: "claude",
			Assumption: "Drydock's read-only spec (--permission-mode default with Edit, Write, MultiEdit, NotebookEdit and Bash disallowed) keeps the requested model and writes nothing.",
			Why:        "Reviewers, room replies, planners and the team lead run read-only on the model the human chose; plan mode would switch a haiku session to a pricier model.",
			Files:      []string{"internal/agents/claude/adapter.go", "internal/agents/agents.go"},
			Tests: []TestRef{
				{Package: pkgContract, Test: "TestClaudeReadOnlySpecKeepsTheModelAndDeniesWrites", EstUSD: 0.03, Env: real},
				{Package: pkgRuntime, Test: "TestRealClaudeReadOnlySessionKeepsItsModelAndCannotWrite", EstUSD: 0.03},
			},
		},
		{
			ID: "CLAUDE-PLAN-01", CLI: "claude", Adapter: "claude",
			Assumption: "Drydock never starts Claude Code in plan mode on its own: every place that asks for plan mode also marks the session read-only, which the adapter maps to the default mode.",
			Why:        "Claude Code answers a plan-mode session started with --model haiku with a pricier model; the observation records which model answers today.",
			Files:      []string{"internal/agents/agents.go", "internal/agents/claude/adapter.go", "internal/core/core.go"},
			Tests: []TestRef{
				{Package: pkgContract, Test: "TestDrydockNeverStartsClaudeInPlanMode"},
				{Package: pkgContract, Test: "TestClaudePlanModeHaikuObservation", EstUSD: 0.10, Env: real},
			},
		},
		{
			ID: "CLAUDE-SETTINGS-01", CLI: "claude", Adapter: "claude",
			Assumption: "With --setting-sources user, a repository's own hooks (.claude/settings.json) do not run and its CLAUDE.md is not loaded.",
			Why:        "Unattended agents must not run commands someone else wrote into a repository (decision P16); the adapter appends the project memory itself, so a natively loaded CLAUDE.md would appear twice and bring back imports from outside the folder.",
			Files:      []string{"internal/agents/claude/adapter.go", "internal/agents/claude/project_memory.go"},
			Tests: []TestRef{
				{Package: pkgContract, Test: "TestClaudeUserSettingSourcesSkipRepositoryHooksAndMemory", EstUSD: 0.02, Env: real},
				{Package: pkgCore, Test: "TestRealClaudeDoesNotRunARepositorysOwnHooks", EstUSD: 0.03},
				{Package: pkgRuntime, Test: "TestRealClaudeGetsTheProjectInstructionsWithUserSettingsOnly", EstUSD: 0.02},
			},
		},
		{
			ID: "CLAUDE-RESTRICTED-01", CLI: "claude", Adapter: "claude",
			Assumption: "--restricted removes Bash and the other command tools, refuses bypassPermissions, and in acceptEdits mode lets the file tools write ordinary project files (package.json, Makefile, tsconfig.json, workflows, .gitignore) but not .claude/settings.json, .git/ or anything outside the working directory.",
			Why:        "The per-task sandbox runs workers with --restricted and relies on exactly this confinement.",
			Files:      []string{"internal/agents/claude/adapter.go"},
			Tests: []TestRef{
				{Package: pkgContract, Test: "TestClaudeRestrictedModeRefusesBypassPermissions"},
				{Package: pkgContract, Test: "TestClaudeRestrictedModeConfinesTools", EstUSD: 0.08, Env: real},
			},
		},
		{
			ID: "CLAUDE-RESTRICTED-02", CLI: "claude", Adapter: "claude",
			Assumption: "Under --restricted an instruction given with --append-system-prompt-file is obeyed without any tool use; the project's CLAUDE.md is not loaded natively (an observation, a warning if that changes).",
			Why:        "Sandboxed workers get the project memory only from what Drydock appends; if the CLI also loaded CLAUDE.md natively the memory would appear twice.",
			Files:      []string{"internal/agents/claude/adapter.go", "internal/agents/claude/project_memory.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestClaudeRestrictedModeObeysTheAppendedProjectMemory", EstUSD: 0.02, Env: real}},
		},
		{
			ID: "CLAUDE-SESSION-01", CLI: "claude", Adapter: "claude",
			Assumption: "A claude started by Drydock from inside a Claude Code session reports a session of its own, because the adapter removes the enclosing session's variables.",
			Why:        "A child that inherits them reports the outer session's id (measured with 2.1.296 in a remote Claude Code session, where CLAUDE_CODE_REMOTE_SESSION_ID does it) and can append to it, which breaks resume and mixes transcripts.",
			Files:      []string{"internal/agents/claude/adapter.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestClaudeStartedByDrydockGetsItsOwnSession", EstUSD: 0.02, Env: real, Enclosing: true}},
		},
		{
			ID: "CLAUDE-RESUME-01", CLI: "claude", Adapter: "claude",
			Assumption: "--resume <session id> continues the same session with its history, and an unknown id fails before any turn so Drydock can fall back to a transcript.",
			Why:        "Chats continue the CLI's own session on every turn (decision P29).",
			Files:      []string{"internal/chats/turn.go", "internal/agents/claude/adapter.go"},
			Tests:      []TestRef{{Package: pkgCore, Test: "TestRealClaudeChatResumesFallsBackAndShowsRefusals", EstUSD: 0.10}},
		},
		{
			ID: "CLAUDE-INTERRUPT-01", CLI: "claude", Adapter: "claude",
			Assumption: "A control_request interrupt on stdin is acknowledged and ends the turn without ending the process, and the same session answers the next message.",
			Why:        "Stop and the watchdog's steer cancel a turn without losing the session.",
			Files:      []string{"internal/agents/claude/adapter.go", "internal/agents/runtime/runtime.go"},
			Tests: []TestRef{
				{Package: pkgRuntime, Test: "TestRealClaudeInterruptThenContinueSameSession", EstUSD: 0.03},
				{Package: pkgConformance, Test: "TestClaudeAdapterAgainstTheRealCLI", EstUSD: 0.05},
			},
		},
		{
			ID: "CLAUDE-MCP-01", CLI: "claude", Adapter: "claude",
			Assumption: "With --mcp-config and --strict-mcp-config the user's and the repository's MCP servers stay out, and with Drydock's --disallowedTools mcp__plugin_* no tool of an installed plugin's server is offered, so the agent sees only the gateway's tools.",
			Why:        "Every agent reaches tools and project data only through Drydock's gateway (docs/mcp.md). Measured with 2.1.296: --strict-mcp-config does not keep plugins' servers out (31 joined, their tools offered); the deny rule keeps their tools out while the gateway stays connected, and --safe-mode would drop the gateway too.",
			Files:      []string{"internal/agents/claude/adapter.go", "docs/mcp.md"},
			Tests: []TestRef{
				{Package: pkgContract, Test: "TestClaudeStrictMCPConfigLoadsOnlyTheGivenServer", EstUSD: 0.02, Env: real},
				{Package: pkgMCP, Test: "TestRealClaudeTransports", Subtest: "stdio-shim", EstUSD: 0.03},
				{Package: pkgCore, Test: "TestRealClaudeCallsTheSearchPackToolThroughTheGeneratedConfig", Subtest: "acceptEdits", EstUSD: 0.03},
			},
		},
		{
			ID: "CLAUDE-MCPWAIT-01", CLI: "claude", Adapter: "claude",
			Assumption: "With CLAUDE_AUTO_BACKGROUND_TASKS=false, CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1 and MCP_TOOL_TIMEOUT=3900000, an MCP tool call that takes longer than two minutes is waited for in -p stream-json mode and its result reaches the model in the same turn.",
			Why:        "Sandboxed workers run long commands through an MCP tool and Drydock sets these variables for them (internal/pipeline/sandbox.go); without them Claude Code 2.1.296 moves a call to the background after 120 seconds and the worker ends its turn without the result.",
			Files:      []string{"internal/agents/claude/adapter.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestClaudeWaitsForALongMCPToolCall", EstUSD: 0.03, Env: real, Slow: true}},
		},
		{
			ID: "CLAUDE-DENIAL-01", CLI: "claude", Adapter: "claude",
			Assumption: "A tool call the permission mode refuses in a headless session appears in result.permission_denials with the tool name, its id and its input, and does not run.",
			Why:        "Chats and runs show each refusal with a suggested allow rule; the parser reads permission_denials.",
			Files:      []string{"internal/agents/claude/parser.go", "internal/chats/turn.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestClaudeReportsRefusedToolCallsAsPermissionDenials", EstUSD: 0.02, Env: real}},
		},
	}
	for _, t := range ACPTargets {
		cs = append(cs, Contract{
			ID: "ACP-INIT-01", CLI: t.Name, Adapter: t.Adapter,
			Assumption: "The ACP initialize handshake answers with protocol version 1, the agent's name and version, and its authentication methods (none for the Claude bridge, which lists them only to a client with terminal-auth).",
			Why:        "Every ACP session starts with initialize; Drydock reads the capabilities from it and names the sign-in methods when session/new asks for authentication.",
			Files:      []string{"internal/agents/acp/adapter.go", "internal/agents/acp/caps.go", "internal/agents/manifests/cli/" + t.Adapter + ".yaml"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestACPInitializeHandshake", Subtest: t.Name}},
		})
	}
	for _, t := range ACPTargets {
		cs = append(cs, Contract{
			ID: "ACP-PROMPT-01", CLI: t.Name, Adapter: t.Adapter,
			Assumption: "A signed-in ACP agent opens a session, answers a prompt with streamed text and ends the turn with a stop reason.",
			Why:        "Every ACP turn Drydock runs (tasks, room replies, chats) is session/new plus session/prompt.",
			Files:      []string{"internal/agents/acp/session.go"},
			Tests:      []TestRef{{Package: pkgContract, Test: "TestACPPromptTurn", Subtest: t.Name, EstUSD: 0.05}},
		})
	}
	cs = append(cs, Contract{
		ID: "WIN-ARGV-01", CLI: "claude", Adapter: "claude", Platform: "windows",
		Assumption: "On Windows a multi-line argument to the npm-installed claude.cmd does not arrive whole (cmd.exe ends the command line at the first line break), while the same text through --append-system-prompt-file does.",
		Why:        "This is why the adapter passes the system prompt addition in a file; if the cut ever stops happening the file is still correct, but if the file path broke, Windows agents would lose their instructions.",
		Files:      []string{"internal/agents/claude/adapter.go"},
		Tests:      []TestRef{{Package: pkgContract, Test: "TestWindowsMultilineArgumentThroughClaudeCmd", EstUSD: 0.02, Env: real}},
	})
	return cs
}
