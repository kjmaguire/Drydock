# Contract tests: what Drydock assumes about the agent CLIs

Drydock drives real agent CLIs: Claude Code headless (stream-json), and ACP agents (Codex through its bridge, Gemini, OpenCode,
Goose, Cline). Most tests use a scripted fake agent, and the worst bugs found in real use lived in the gap between that fake and
the real CLI. Three examples: Claude Code answers a `plan`-mode session started with `--model haiku` with a pricier model;
`--setting-sources user` also skips the project's CLAUDE.md, its imports and `.claude/rules`; on Windows `claude.cmd` runs
through cmd.exe, which cuts a multi-line argument at the first line break.

Each such assumption is now a named **contract** with a test against the real CLI. The suite runs nightly, when the adapters
change, and whenever a CLI releases a new version, so a change in the CLI is found by the suite instead of by a user.

## How it fits together

| Part | Where |
|---|---|
| The catalog: id, CLI, the assumption in one sentence, why Drydock depends on it (with the file), the tests that check it | `internal/contract/catalog.go` |
| The new contract tests (the existing real-CLI tests stay where they are and are referenced) | `internal/contract/*_test.go` |
| The runner: finds the CLIs, runs the tests, maps the results back, writes the report, checks for new versions | `cmd/drydock-contract` |
| The pins it compares with (shared with the Software Versions audit) | `internal/agents/pins`, read from `internal/agents/manifests/cli/*.yaml` |
| The scheduled workflow | `.github/workflows/contract.yml` |

A unit test parses the packages the catalog names and fails if a referenced test (or subtest name) does not exist, if a named
file is missing, or if a contract is not explained in this document.

The runner runs one test per `go test -json` invocation, free checks first, then the paid ones in catalog order while the
budget lasts. A contract **fails** when any of its tests fails or did not run (renamed, not building), **passes** when at least
one passed (the report says which others were skipped), and is **skipped** only when all were. The runner exits 1 when any
contract fails.

## The contracts

Free checks start the CLI with an empty stream-json input: it parses its arguments and exits without a model turn. Paid checks
send a few words to the cheapest model (haiku).

| Contract | What it guards | Check |
|---|---|---|
| CLAUDE-ARGV-01 | Every flag the adapter builds (model, effort, read-only spec, `--setting-sources user`, `--mcp-config` with `--strict-mcp-config`, `--append-system-prompt-file`, allowed tools) and every permission mode Drydock offers is accepted. A renamed flag would stop every agent from starting. | free |
| CLAUDE-EFFORT-01 | Each effort Drydock offers is accepted. Claude Code only *warns* about an unknown effort and runs at its default, so an ignored effort would change cost and quality unseen. The check also notes efforts the CLI offers that Drydock does not. | free |
| CLAUDE-STREAM-01 | The stream-json events have the shape the parser reads: `system/init` (session id, model, tools), `assistant` text and `tool_use` blocks, `user` `tool_result` blocks, `stream_event` text deltas, and the `result` (subtype, stop reason, session id). The same output is fed through Drydock's parser. The conformance suite against the real CLI is the second test. | paid |
| CLAUDE-USAGE-01 | The result has `total_cost_usd` and `modelUsage` keyed by model id with the token counts and `costUSD` the parser reads, and they add up. | paid |
| CLAUDE-USAGE-02 | When one process answers several turns, each result's `total_cost_usd` and `modelUsage` are running totals of the process and `usage` is the turn's own; Drydock's parser records the difference, so each turn is counted once (checked on the real stream). | paid |
| CLAUDE-USAGE-03 | A session resumed in a new process (`--resume`) reports totals that include its earlier processes; started from the session's last totals (`RunSpec.UsageBaseline`, kept per chat in `chats.cli_usage`), the parser records only the new turn. | paid |
| CLAUDE-PROMPTFILE-01 | A 12-line `--append-system-prompt-file` reaches the model through its last line. Drydock passes purpose, practices and project memory this way. | paid |
| CLAUDE-RO-01 | The adapter's read-only argv (`--permission-mode default --disallowedTools Edit,Write,MultiEdit,NotebookEdit,Bash`) keeps the requested model (every `modelUsage` key is haiku) and writes nothing. | paid |
| CLAUDE-PLAN-01 | Drydock's own code asks for plan mode only together with `ReadOnly` (a static scan of every non-test Go file), and the adapter maps a read-only spec to the default mode. A paid observation records which model answers plan mode started with haiku. | static + paid |
| CLAUDE-SETTINGS-01 | With `--setting-sources user` a repository's own hook (`.claude/settings.json`) does not run and its CLAUDE.md is not loaded (the adapter appends it itself; a native copy would appear twice and bring back imports from outside the folder). A control run without the flag shows both do happen otherwise. | paid |
| CLAUDE-RESTRICTED-01 | `--restricted` removes Bash, PowerShell, REPL and WebFetch, keeps Read, Write and Edit, refuses `bypassPermissions`, and in `acceptEdits` lets the file tools write `package.json`, `Makefile`, `tsconfig.json`, a workflow and `.gitignore` but not `.claude/settings.json`, `.git/hooks/pre-commit` or `../outside.txt`. The per-task sandbox depends on this confinement. | free + paid |
| CLAUDE-RESTRICTED-02 | Under `--restricted` an instruction given with `--append-system-prompt-file` is obeyed with no tool use: the project memory Drydock appends for sandboxed workers reaches the model. It also records whether the project's CLAUDE.md is in context *natively* under `--restricted` (no, on 2.1.296); if that ever turns to yes the report shows a warning, because the memory would then appear twice. | paid |
| CLAUDE-SESSION-01 | A claude started by Drydock from inside a Claude Code session reports a session of its own: the adapter removes the enclosing session's variables. The runner gives only this test the enclosing session's variables (when it was itself started inside one; elsewhere a made-up `CLAUDE_CODE_SESSION_ID` stands in), and a control run shows whether a child that inherits them reports the outer session. In a remote Claude Code session `CLAUDE_CODE_REMOTE_SESSION_ID` does it (2.1.296): the child reports the outer id and writes into that session's transcript. | paid |
| CLAUDE-RESUME-01 | `--resume` continues the same session, and an unknown id fails before any turn so a chat can fall back to its transcript. | paid (existing chat test) |
| CLAUDE-INTERRUPT-01 | An interrupt on stdin is acknowledged, ends the turn, not the process, and the same session answers the next message. | paid (existing tests) |
| CLAUDE-MCP-01 | `--mcp-config` with `--strict-mcp-config` loads only the configured server: a repository's `.mcp.json`, the user's own servers and the MCP servers of installed plugins do not join, and the configured server's tools are offered. The existing gateway tests prove a real call works. Started the way Drydock starts agents (`--setting-sources user`). | paid |
| CLAUDE-QUIET-01 | Started as Drydock starts every session (`--disable-slash-commands`, the deny list `claude.UnattendedDenied`, the environment `claude.QuietEnv`), Claude Code offers no skills and no slash commands, none of the denied tools, and `CLAUDE_CODE_DISABLE_CRON` and `CLAUDE_CODE_DISABLE_WORKFLOWS` still remove their tools (checked against a run with the CLI's defaults). A tool nobody reviewed (not denied, not in the test's list of tools a session is meant to have) is a warning, so each tool a new version adds gets a decision. The prompt is `/context`, which the CLI answers itself with slash commands off, and the API address points at a closed local port, so even a changed CLI cannot spend. Measured with 2.1.296: 12 tools, no skills, no commands, against 41 tools, 68 skills and 105 commands by default. | free |
| CLAUDE-TOKENS-01 | The first request of a session Drydock starts is no larger than the CLI's defaults with the user's settings, and a second session in another worktree of the same repository reads the shared part from the prompt cache. Measured with 2.1.296 on haiku: 9,971 tokens against 27,590; the second worktree wrote 2,828 and read 7,141. A first request past 20,000 tokens is a warning. | paid |
| CLAUDE-MCPWAIT-01 | With `CLAUDE_AUTO_BACKGROUND_TASKS=false`, `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` and `MCP_TOOL_TIMEOUT=3900000` (what the sandbox sets), a 130-second MCP tool call is waited for and its result reaches the model in the same turn. Without them 2.1.296 moves the call to the background after 120 seconds and the turn ends without the result. Slow: about two and a half minutes. | paid, slow |
| CLAUDE-DENIAL-01 | A write the default mode refuses appears in `result.permission_denials` with the tool name, its id and its input, does not happen, and the parser reports it. | paid |
| ACP-INIT-01 | For each ACP agent installed (claude-agent-acp, codex-acp, gemini, opencode, goose, cline): `initialize` answers protocol version 1, the agent's name and version, and its authentication methods (the Claude bridge lists none to a client without terminal-auth, as phase 0 recorded; the others must list theirs). A throwaway home folder and a scrubbed environment are used, so no configuration or credential is read. | free |
| ACP-PROMPT-01 | A signed-in ACP agent answers a prompt with streamed text and a stop reason. Skipped with "no credentials" unless the agent is listed in `DRYDOCK_CONTRACT_ACP_CREDENTIALS`; never sent through the Claude ACP bridge, which stays disabled (decision D1). | paid |
| WIN-ARGV-01 | On Windows a multi-line `--append-system-prompt` through the npm-installed `claude.cmd` does not arrive whole, and the same text through `--append-system-prompt-file` does. Skipped elsewhere, and where Claude Code is a native `claude.exe`. | paid, Windows only |

Notes and warnings a test writes (`contract-note:`, `contract-warning:` in its log) are collected into the report, for
example which model answered the plan-mode observation.

## Running it

The suite needs Go, the CLIs to check on `PATH`, and for the paid checks a signed-in `claude` (`claude auth login`, or
`CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`, or `ANTHROPIC_API_KEY`).

```sh
# Linux or macOS, from the repository root
go run ./cmd/drydock-contract --budget-usd 1.50                  # every contract
go run ./cmd/drydock-contract --cli claude --skip-slow           # only Claude Code, without the 2.5-minute check
go run ./cmd/drydock-contract --check-updates                    # also compare the pins with the newest npm releases
go run ./cmd/drydock-contract --check-updates --latest           # and run the suite against what is newer
go run ./cmd/drydock-contract --list                             # print the catalog
```

```powershell
# Windows (PowerShell), from the repository root; WIN-ARGV-01 runs only here, and only with an npm install (claude.cmd)
npm install -g @anthropic-ai/claude-code      # or: go run ./cmd/drydock-contract --install-pinned claude
go run ./cmd/drydock-contract --budget-usd 1.50
```

The report goes to `contract-report/` (`--out`): `contract-report.md` for people, `contract-report.json` for tools, and the raw
`go test -json` output of each test under `logs/`. The folder is ignored by git.

Flags: `--cli` (program names or manifest ids, comma separated: `claude`, `goose`, `codex`, `claude-agent-acp`, ...), `--out`,
`--budget-usd` (default 1.00), `--latest` (install the newest version of each CLI with `npm i -g --prefix <temp>` and put it
first on `PATH`), `--check-updates`, `--skip-slow`, `--timeout` (per test, default 20 minutes), `--install-pinned <cli>` (for CI:
`npm install -g` the pinned version unless the CLI is on `PATH`), `--list`.

One test can be run directly, too: `DRYDOCK_REAL_CLAUDE=1 go test ./internal/contract -run TestClaudeStreamJSONEventShapes -v`.
Without `DRYDOCK_REAL_CLAUDE=1` the paid checks skip; the free checks run wherever `claude` is installed, and the static check
always runs. The same switch gates every other Go test that sends prompts to the real claude (`contract.RealClaudeSkip`: the
runtime, core, pipeline, MCP, responder, connections and conformance tests), so `go test ./...` on a machine where claude is
signed in spends nothing; before 2026-10-10 those tests ran whenever claude was installed and signed in. Tests that send no
prompt (detection, the ACP handshake) still run wherever the CLI is installed.

Environment the tests read:

| Variable | Meaning |
|---|---|
| `DRYDOCK_REAL_CLAUDE=1` | run the paid Claude checks and every other test that prompts the real claude (the runner sets it) |
| `DRYDOCK_NO_REAL_AGENTS=1` | skip every real-CLI test (the runner removes it) |
| `DRYDOCK_CONTRACT_BUDGET_USD`, `DRYDOCK_CONTRACT_LEDGER` | the run's budget and the file the paid checks book their spend in (set by the runner) |
| `DRYDOCK_ACP_<NAME>` | the command of an ACP agent when it is not on `PATH` (`CLAUDE_ACP`, `CODEX_ACP`, `GEMINI`, `OPENCODE`, `GOOSE`, `CLINE`; the same variables as the live tests) |
| `DRYDOCK_CONTRACT_ACP_CREDENTIALS` | ACP agents that are signed in here (comma separated), so their prompt-turn contract runs with the user's own configuration |

The runner removes the variables a running Claude Code session sets for its children (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`,
`CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_REMOTE_SESSION_ID`, `CLAUDE_CODE_MESSAGING_SOCKET`,
`CLAUDE_CODE_MESSAGING_TOKEN`), so the suite measures the CLI as it runs on a user's machine even when it is started from
inside Claude Code. It also drops an empty `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` (an unset GitHub secret).

## Cost

Every paid check uses haiku with a few words of prompt; most cost a tenth of a cent to half a cent. The exception is the
plan-mode observation, which is answered by a pricier model by design (about $0.015 on 2.1.296). The first full run reported
$0.039 for the new checks (see "First runs" below). The existing tests the catalog refers to do not report their spend; the
report counts them at their estimate (`EstUSD` in the catalog, deliberately high: $0.34 for all nine), so the real total is
lower than "reported plus estimated".

Three guards keep it bounded: the runner does not start a paid test whose estimate no longer fits in `--budget-usd`; each new
paid check reads the run's ledger before it starts and skips once the budget is spent; and each claude process it starts gets
`--max-budget-usd` with what is left (at most $0.50).

## The workflow

`.github/workflows/contract.yml` runs nightly, on demand (with a budget and a "latest" switch), and on pushes that change
`internal/agents/**`, `internal/contract/**` or the workflow. It checks out the repository, sets up Go from `go.mod` and Node 24,
installs the pinned Claude Code unless one is already installed, runs the suite with `--check-updates` and a budget cap, adds the
report to the run's summary and uploads `contract-report/` as an artifact.

- **Runner.** `runs-on` reads the repository variable `CONTRACT_RUNS_ON` (a JSON value, for example `"windows-latest"` or
  `["self-hosted","windows"]`) and defaults to `ubuntu-latest`. WIN-ARGV-01 needs a Windows runner.
- **Sign-in.** Add one repository secret: `CLAUDE_CODE_OAUTH_TOKEN` (made with `claude setup-token` on a machine where you are
  signed in; it uses your subscription) or `ANTHROPIC_API_KEY` (billed to the API account). Without either, the paid checks skip
  with "claude is not signed in here" and the free ones still run.
- **Status.** GitHub Actions does not start runners for this repository yet (decision P2), so the workflow has not run; it was
  checked by review and by parsing the YAML.

## Update policy

A new CLI version is adopted only after the suite passes on it:

1. `--check-updates` shows a newer release (nightly, or by hand).
2. `go run ./cmd/drydock-contract --check-updates --latest` runs the suite against it.
3. If every contract passes, move the pin: `versions.recommended` (or the bridge version) in
   `internal/agents/manifests/cli/<id>.yaml`, a `compat.yaml` entry saying what was run, then the Software Versions rule
   (`docs/software-versions.md`).
4. If a contract fails, the version is **not adopted**: Drydock keeps (or pins back to) the previous version until the adapter is
   fixed. Add the version to `blocked` in `compat.yaml` with the contract id as the reason, so Connections marks it unsupported
   instead of letting users run into the problem. A warning in the report (a check that passed but saw a change, such as
   CLAUDE.md now loading natively under `--restricted`) needs a decision before the pin moves.

## Adding a contract

Write the assumption as one sentence and the reason with the file that depends on it, add the entry to
`internal/contract/catalog.go` with the test that checks it (an existing real test, or a new one in `internal/contract`), give a
paid test an `EstUSD` and `Env: real`, and explain it in the table above. Paid checks call `paidClaude` (gate and budget) and
book their spend through the process helper; keep prompts to a few words and the model to haiku.

## First runs (2026-10-10, Claude Code 2.1.296 on Linux)

`go run ./cmd/drydock-contract --budget-usd 1.20 --check-updates`, with goose 1.54.0, cline 3.0.70, the Claude ACP bridge 0.88.0
and the codex-acp bridge 2.1.1 installed (Gemini and OpenCode were not): **18 passed, 3 failed, 9 skipped** (Gemini and OpenCode
not installed, no ACP agent signed in, WIN-ARGV-01 off Windows), $0.039 reported. A second run,
`--cli claude-agent-acp --check-updates --latest --budget-usd 0`, installed the bridge's newest release (0.89.0) into a
temporary npm prefix and ACP-INIT-01 passed on it.

What the first run found:

- **CLAUDE-USAGE-02 fails.** In one process, `result.total_cost_usd` and `modelUsage` are what the process has spent so far
  (turn 2 reported 17,778 tokens: turn 1's 8,854 plus its own 8,924), while `result.usage` is the turn's own. The parser books
  every result as one turn, so a process with several results (an interrupt and its follow-up, watchdog nudges, kept sessions)
  is over-counted. One-turn processes (workflow nodes, chats, room replies) are not affected.
- **CLAUDE-MCP-01 fails here.** `--strict-mcp-config` kept a repository's `.mcp.json` out, but not the MCP servers of the
  plugins installed for this user: 31 plugin servers joined and the connected one's three tools were offered to the agent, with
  and without `--setting-sources user`. A machine without plugins passes.
- **CLAUDE-SESSION-01 fails on this branch.** Run from a remote Claude Code session, a child reports the outer session's id (and
  writes into its transcript) because of `CLAUDE_CODE_REMOTE_SESSION_ID`, which the adapter's list did not strip yet; a
  separate adapter change adds it, with `CLAUDE_CODE_MESSAGING_SOCKET` and `CLAUDE_CODE_MESSAGING_TOKEN`. A made-up
  `CLAUDE_CODE_SESSION_ID` on its own is not adopted.
- **`--restricted` does not load CLAUDE.md natively** (CLAUDE-RESTRICTED-02's observation): an instruction in CLAUDE.md is not
  followed and the model says the text is not in its context. Asked a direct question it can still read the file with its file
  tools, which made an earlier probe look like native loading. The memory Drydock appends is obeyed.
- **Plan mode with `--model haiku` is answered by `claude-sonnet-5-5`** (CLAUDE-PLAN-01's observation, $0.015 for one word).
  Drydock's own code asks for plan mode only with `ReadOnly` (8 places), but the adapter still offers plan mode to the human for
  chats and agents, and such a session reaches the CLI as `--permission-mode plan`.
- `--permission-mode` no longer lists `default` (it lists `manual`) but still accepts it. An unknown `--effort` is only warned
  about and the turn runs at the default effort (and, without `--model`, on the default model).
- Under `--restricted`, Bash, PowerShell, REPL and WebFetch are gone; writes to `.claude/settings.json`, `.git/hooks/pre-commit`
  and `../outside.txt` are refused while `package.json`, `Makefile`, `tsconfig.json`, a workflow and `.gitignore` are written.
  Tools such as `Task`, `WebSearch` and `Skill` remain.
- CLAUDE-MCPWAIT-01: a 130-second MCP call's result arrived in the same turn (2m14s) with the three variables set.
- The version watch: Claude Code 2.1.296 is installed and newest while the pin is 2.1.295; the Claude ACP bridge (0.89.0) and
  Codex (0.162.1) have newer releases than their pins.

### The fixes, and the run after them

The three failures were Drydock's, and are fixed:

- **CLAUDE-USAGE-02:** the parser (`internal/agents/claude/parser.go`) keeps what a process's earlier results reported and records
  each result as the difference; the scripted stand-in (`internal/agents/fakeagent`) now reports running totals the same way. The
  contract checks both halves on the real stream: the CLI still reports running totals (if it starts reporting each turn's own,
  the parser would under-count, so the check fails then too) and the parser's two usage records add up to the CLI's total.
- **CLAUDE-MCP-01:** every session that is not restricted gets `--disallowedTools mcp__plugin_*` (`claude.PluginToolsDenied`):
  installed plugins' MCP servers still start, as when you run Claude Code yourself, but none of their tools is offered, so the
  agent's only MCP tools are the gateway's. Measured: with the rule, 31 plugin servers started, 0 of their tools were offered
  and the gateway's server stayed connected; `--safe-mode`, which does keep plugins out, also drops the `--mcp-config` server, so
  it cannot be used. Restricted (sandboxed) sessions load no plugin at all.
- **CLAUDE-SESSION-01:** the adapter strips `CLAUDE_CODE_REMOTE_SESSION_ID`, `CLAUDE_CODE_MESSAGING_SOCKET` and
  `CLAUDE_CODE_MESSAGING_TOKEN` (decision P30's commit).
- **CLAUDE-USAGE-03 (new, found while fixing USAGE-02):** the running totals belong to the *session*, not the process: a
  turn resumed in a new process reported 17,824 tokens (8,880 from the first process plus 8,944 of its own) and twice the cost,
  so every chat turn after the first was counted together with all the turns before it. The parser now reports its running
  totals with each usage record (`UsageChunk.Totals`), a chat keeps them with its session id, and the next turn starts its
  parser from them. Measured again afterwards: 5,294 = 2,615 + 2,679 tokens, the turn recorded once.
- **Also seen:** with `--disallowedTools mcp__plugin_*` a turn's context fell from about 8,850 to about 2,600 tokens on this
  machine: the 31 plugin servers' tool definitions were sent with every turn of every agent before.
- **The gateway itself:** Claude Code starts a session even when an MCP server fails and only says so in `system/init`
  (`"status":"failed"`). A session given Drydock's gateway now stops its turn with a clear error when the gateway did not
  connect, instead of working on without Drydock's tools (found when the trial harness's own binary could not serve the
  gateway's stdio shim).

`go run ./cmd/drydock-contract --cli claude --skip-slow --budget-usd 1.00` afterwards: **16 passed, 0 failed, 4 skipped**
(MCPWAIT-01 left out by `--skip-slow` after passing in the first run, the ACP prompt turn with no credentials (D1), WIN-ARGV-01
off Windows), $0.036 reported. The real-Claude tests of the gateway (`TestRealClaudeTransports/stdio-shim`,
`TestRealClaudeCallsTheSearchPackToolThroughTheGeneratedConfig/acceptEdits`) passed with the plugin rule in place.

