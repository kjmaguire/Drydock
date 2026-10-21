> **Evidence appendix (raw agent report, gathered 2026-10-08 on Linux).** Paths such as `/tmp/.../scratchpad/...` refer to an ephemeral sandbox that is not preserved; small reproducible probes are kept in [`evidence/phase0/`](../../evidence/phase0/). Synthesis and decisions needed: [`docs/phase0.md`](../phase0.md).

# Phase 0b: agent CLI evidence report

Date of gathering: 2026-10-08 (sandbox clock). Linux sandbox, restricted network. Nothing was run on Windows. No credentials were entered; no model/API call was made.

Evidence root (all paths below are relative to it unless absolute):
`P0B=/tmp/claude-0/-home-user-Drydock/927585e5-898b-57de-a294-91a4b635e67b/scratchpad/p0b`

Legend
- VERIFIED-HERE: I executed the thing, or read the file/source/package, in this session.
- SNIPPET-ONLY: only a WebSearch snippet (no page body reachable). Treat as a lead, not as fact.
- UNVERIFIED: could not verify; reason given.
- NEEDS-WINDOWS / NEEDS-LOGIN: requires a Windows host or an authenticated account.

Network reality: reachable were registry.npmjs.org, raw.githubusercontent.com, proxy.golang.org, pypi.org, code.claude.com, support.claude.com, www.anthropic.com/legal, platform.claude.com, a GitHub-release download redirect. Unreachable (proxy 403 / DNS): developers.openai.com, geminicli.com, opencode.ai, block.github.io/goose + goose-docs.ai, antigravity.google, developers.googleblog.com, cdn.agentclientprotocol.com, dl.google.com, github.com HTML, openai.com policies. So Codex/Gemini/OpenCode/Goose "official docs" were read from the docs folders inside their GitHub repos (raw) instead of the doc websites.

---------------------------------------------------------------------
## 0. Headline findings that change the design

1. **Gemini CLI is being retired for consumer accounts; the successor (Antigravity CLI `agy`) is closed and has its own first-party ACP server.** SNIPPET-ONLY (Google Developers Blog title and snippets, GitHub discussion #28017 title): "Gemini CLI and the Gemini Code Assist IDE extensions stop serving requests for Google AI Pro and Ultra, as well as those using it free of charge using Gemini Code Assist for individuals", cut-off 2026-06-18. The ACP registry (VERIFIED-HERE, raw) still lists `gemini` 0.63.0 with `--acp`, npm still publishes it (0.63.0 on 2026-10-06), but only API-key / Vertex / enterprise auth is a safe assumption. Registry also has `antigravity-acp` 1.3.0, license "proprietary".
2. **Codex `wire_api` only accepts `responses`.** `codex-rs/core/config.schema.json` `WireApi` has exactly one value, `responses`. A "chat completions only" OpenAI-compatible host (OpenRouter chat route, many local servers) cannot be a Codex provider. Ollama/LM Studio work only because they now expose `/v1/responses` (UNVERIFIED for the server side).
3. **The Codex ACP bridge moved.** `@zed-industries/codex-acp` (0.16.0, 2026-06-08) README says "Development has moved to agentclientprotocol/codex-acp ... built on the new Codex App Server". Current: `@agentclientprotocol/codex-acp` **2.1.1** (2026-10-01). A generic adapter must not hard-code the old package.
4. **Goose ACP conflict is resolved: it is BOTH.** `goose acp` is an ACP *agent* server on stdio (live initialize succeeded, v1.53.0), and Goose also has an ACP *client/provider* (`AcpProvider`) so it can drive Claude/Codex/Amp/Pi. We can drive Goose with our ACP client. Limits: no `session/resume` or `session/fork` advertised (only `loadSession` + list/delete/close), MCP `http` only (no `sse`).
5. **Claude's ACP bridge bundles its own copy of the Claude Code binary (234 MB, v2.1.287) and needs Node >= 22.** Its bundled binary should use the same credential store as the user's `claude` (same product; UNVERIFIED, no login available here), but it is not the user's installed `claude` unless `CLAUDE_CODE_EXECUTABLE` is set. The ACP registry labels the bridge's license "proprietary" while its npm package/LICENSE is Apache-2.0 (the dependency `@anthropic-ai/claude-agent-sdk` is "SEE LICENSE IN README.md" / all-rights-reserved).
6. **Anthropic policy text is the biggest legal risk for any orchestrator, and it explicitly names Agent-SDK-based products** (quotes in section 9). A Go desktop app that never touches credentials and only spawns the user's own unmodified, user-logged-in `claude` is the safest reading, but the Agent SDK page says "Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK." The ACP bridge IS an Agent SDK product. Needs counsel / Anthropic confirmation.
7. **Headless modes cannot do interactive permission prompts for 2 of 5 CLIs** (OpenCode `run` auto-rejects unless `--auto`; Antigravity `-p` soft-denies). For these, ACP `session/request_permission` is the only interactive path. Claude headless can (control protocol / `--permission-prompt-tool`), Gemini/Codex/Goose headless: approval is by policy/mode only (not verified interactive).
8. **Usage/quota via ACP is uneven.** Standard-ish `usage_update` {used,size,cost} exists in Claude/Codex(no cost)/OpenCode/Goose bridges; Gemini has none (token counts hide in non-standard `PromptResponse._meta.quota`). Plan-quota windows: Claude via `_meta["_claude/rateLimit"]` on usage_update; Codex only as markdown in `/status` over ACP but structured via `codex app-server` `account/rateLimits/read`; Gemini only internal (`retrieveUserQuota`) / `/stats`; OpenCode/Goose none. In the Go SDK (`coder/acp-go-sdk` v0.13.5) `usage_update` and prompt `Usage` are marked **UNSTABLE**.
9. **OpenCode on Windows: native binaries exist but the project recommends WSL**; docs say "While OpenCode can run directly on Windows, we recommend using ... WSL".

---------------------------------------------------------------------
## 1. Claude Code

### Findings
| Item | Finding |
|---|---|
| Version | npm `@anthropic-ai/claude-code`: latest **2.1.293** (2026-10-07), stable 2.1.285, next 2.1.294. Ran `claude --version` -> `2.1.293 (Claude Code)`. |
| License | Proprietary. `LICENSE.md`: "(c) Anthropic PBC. All rights reserved. Use is subject to the Legal Agreements outlined here: https://code.claude.com/docs/en/legal-and-compliance". npm field: "SEE LICENSE IN README.md". |
| Install (Windows) | Native: PowerShell `irm https://claude.ai/install.ps1 \| iex`; CMD `curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd`; WinGet `winget install Anthropic.ClaudeCode` (no auto-update); npm `npm install -g @anthropic-ai/claude-code` (package is a thin shim; native binaries come from per-platform optional deps incl. `claude-code-win32-x64`, `-win32-arm64`; engine node >=22). Also `claude install [target]`, `claude update`. |
| Native Windows vs WSL | Native supported: "Windows 10 1809+ or Windows Server 2019+". Git for Windows optional (provides Git Bash for the Bash tool; otherwise PowerShell tool). Sandboxing: "Native Windows ... Not supported" (WSL2 supported). |
| Version command | `claude --version` / `-v`; `claude doctor`. |
| Auth flow | Browser OAuth on first run (`claude auth login [--claudeai\|--console\|--sso\|--email]`); if browser can't call back, paste code. API key via `ANTHROPIC_API_KEY`; long-lived token via `claude setup-token` -> `CLAUDE_CODE_OAUTH_TOKEN`; cloud providers via env (`CLAUDE_CODE_USE_BEDROCK/VERTEX/FOUNDRY`). Precedence documented (cloud creds > bearer > ANTHROPIC_API_KEY > apiKeyHelper > CLAUDE_CODE_OAUTH_TOKEN > profiles > /login). Credentials: Windows `%USERPROFILE%\.claude\.credentials.json`; Linux `~/.claude/.credentials.json` (0600); macOS Keychain; `CLAUDE_CONFIG_DIR` relocates. |
| Non-interactive auth detect | `claude auth status` (default `--json`; `--text` available). Actual output seen: `{"loggedIn": true, "authMethod": "oauth_token", "apiProvider": "firstParty", ...}` (it reported my sandbox's env-injected token; exit 0). The ACP bridge uses the same probe (`claude auth status --json`, 'auth-status.js'). I did not test the logged-out exit code (UNVERIFIED). |
| Headless | `claude -p "<prompt>" --output-format text\|json\|stream-json`; `--input-format stream-json` (realtime streaming input, control protocol incl. `interrupt`, permission `control_request`s); `--include-partial-messages`, `--include-hook-events`, `--replay-user-messages`, `--json-schema`, `--max-budget-usd`, `--no-session-persistence`, `--bare` (skips hooks/CLAUDE.md autodiscovery/keychain; auth strictly API key), `--permission-prompts host\|none`, `--permission-prompt-tool <mcp tool>`. Docs' stream-json example is `--output-format stream-json --verbose --include-partial-messages`; whether `--verbose` is mandatory is UNVERIFIED (needs a logged-in run). |
| Stream event schema (SDKMessage union) | assistant, user, user-replay, result, system(init, compact_boundary, status, informational, api_retry, permission_denied, task_*, hook_*, session_state_changed, files_persisted, ...), stream_event (partials), tool_progress, tool_use_summary, auth_status, rate_limit_event, prompt_suggestion, ... Result: `type:"result", subtype:"success"\|error_*`, `total_cost_usd`, `usage`, `modelUsage{inputTokens,outputTokens,thinkingTokens?,cacheReadInputTokens,cacheCreationInputTokens,webSearchRequests,costUSD,contextWindow,...}`, `permission_denials`, `session_id`, `duration_ms`, `num_turns`, `stop_reason`. `system/init` has model, tools, MCP servers, plugins, optional `capabilities[]` (e.g. `interrupt_receipt_v1`, `interrupt_cancel_queued_v1`; requires >=2.1.205). |
| Model / effort | `--model <alias\|full name>` (aliases opus/sonnet/..); `--effort low\|medium\|high\|xhigh\|max` (docs add `ultracode`); `--fallback-model`. Env `CLAUDE_CODE_EFFORT_LEVEL`. |
| Resume | `--continue/-c`, `--resume/-r <session-id or path to .jsonl>`, `--session-id <uuid>`, `--fork-session`, `--from-pr`. Resume reports whole-conversation cost. |
| Interrupt | SIGINT ends the turn; SIGTERM exits 143 leaving the turn unfinished with no result recorded ("To end the turn instead, send SIGINT, or call the Agent SDK's interrupt()"). On SIGTERM, process tree of running Bash is terminated. `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1` to continue on resume. Windows SIGINT semantics: NEEDS-WINDOWS. |
| Permission modes | `--permission-mode acceptEdits\|auto\|bypassPermissions\|manual\|dontAsk\|plan` (from `--help`; docs also list `default`); `--dangerously-skip-permissions`, `--allowedTools/--disallowedTools`, `--tools`, `--restricted`, `--safe-mode`. |
| MCP | `--mcp-config <files-or-strings...>` + `--strict-mcp-config`; `claude mcp add/add-json/list/get`; scopes local/project(`.mcp.json`)/user(`~/.claude.json`); transports stdio, http (alias `streamable-http`), sse (deprecated, auto fallback >=2.1.265), ws (config only), sdk (host only). Per-session scoping: yes via `--mcp-config --strict-mcp-config` and `--tools`/`--allowedTools mcp__server__tool`. `-p` waits for pending servers up to `MCP_TIMEOUT` (30s) (>=2.1.221). |
| Usage/cost | Result message: `usage` + per-model `modelUsage` with cache read/creation tokens and `total_cost_usd` ("client-side estimates", docs). Per-step usage on each assistant message `message.usage`. |
| Tool-call visibility | **true**. Tool use/result blocks are inside assistant/user messages (BetaMessage); `tool_progress`, `tool_use_summary`; `--forward-subagent-text` for sub-agent blocks. (Block-level shape per Anthropic Messages API; I did not run a live stream.) |
| OpenAI-compatible provider | **No.** Only Anthropic Messages format via `ANTHROPIC_BASE_URL`/gateway; docs: "Anthropic ... doesn't support routing Claude Code to non-Claude models through any gateway." |
| Instruction files | `CLAUDE.md` / `.claude/CLAUDE.md`, `CLAUDE.local.md`, `~/.claude/CLAUDE.md`, managed (`C:\Program Files\ClaudeCode\CLAUDE.md` on Windows), `.claude/rules/**/*.md` (optional `paths:` glob frontmatter), skills `.claude/skills/<name>/SKILL.md` (optional `paths:` glob frontmatter), `@path` imports. `AGENTS.md` read natively since **v2.1.277**, but only if no `CLAUDE.md`/`CLAUDE.local.md` exists in cwd or above (default setting `claude-md-or-agents-md`; `claude-md-and-agents-md` opt-in via `pluginConfigs`). Walk: cwd and every ancestor at launch; subdirectory CLAUDE.md/AGENTS.md load on demand when Read/Write/Edit touches a file there. |
| Prompt cache on model change | Documented: "Each model has its own cache. Switching models recomputes the entire request even when the content is identical." Effort changes mid-session also miss the cache on most models. |
| Quota windows | Streams `rate_limit_event`: `rate_limit_info{status: allowed\|allowed_warning\|rejected, resetsAt?, utilization?, errorCode?: "credits_required", canUserPurchaseCredits?, ...}` (>=2.1.181). `/usage` is interactive (plan bars; last-known cache). No documented `claude usage --json`. |

### Evidence
- `claude --version`, `claude --help`, `claude auth {--help,status,login --help}`, `claude mcp --help`: run from `claude/_anthropic-ai_claude-code-linux-x64/package/claude` (HOME redirected).
- npm metadata `meta/_anthropic-ai_claude-code.json` (dist-tags, optionalDependencies, license).
- Docs fetched from code.claude.com into `claude/docs/`: `setup.md`, `authentication.md`, `headless.md`, `memory.md`, `prompt-caching.md`, `cli-reference.md`, `mcp.md`, `skills.md`, `permission-modes.md`, `model-config.md`, `costs.md`, `llm-gateway.md`, `agent-sdk_typescript.md` (SDKMessage / SDKResultMessage / SDKRateLimitEvent / ModelUsage), `agent-sdk_overview.md`.
- Windows credential path: `authentication.md` "On Windows, credentials are stored in `%USERPROFILE%\.claude\.credentials.json`".
- SIGTERM quote: `headless.md` "Stop a run with SIGTERM".
- Gateway quote: `llm-gateway.md` line 17.

### Verified-here vs needs-Windows / needs-login
- VERIFIED-HERE: version, flags, subcommands, auth status JSON shape, docs content.
- NEEDS-LOGIN: any actual stream (`-p --output-format stream-json`), event ordering, `rate_limit_event` live shape, logged-out exit code of `claude auth status`.
- NEEDS-WINDOWS: install.ps1/winget paths, Git Bash vs PowerShell tool selection, SIGINT/Ctrl-Break delivery to a child started by a Go `exec.Cmd` (CTRL_BREAK_EVENT / process group), `.credentials.json` ACLs, native binary name `claude.exe` under `%USERPROFILE%\.local\bin` (path not verified).

### Concerns
- Largest legal exposure (see section 9). Never read/copy/refresh `.credentials.json` in our app.
- Two possible Claude binaries (user's and the bridge's bundled one) = version skew; prefer `CLAUDE_CODE_EXECUTABLE=<user's claude>`.
- `--bare` disables keychain/OAuth ("Anthropic auth is strictly ANTHROPIC_API_KEY"), so it is unusable for subscription users.
- Claude Code CLI is a ~250 MB native binary per platform; fine for install-detect, not for vendoring.

---------------------------------------------------------------------
## 2. Codex CLI

### Findings
| Item | Finding |
|---|---|
| Version / license | npm `@openai/codex` latest **0.161.0** (2026-10-07; alpha 0.162.0-alpha.20 same week). `codex --version` -> `codex-cli 0.161.0`. Repo `openai/codex` `LICENSE`: Apache 2.0 (README: "licensed under the Apache-2.0 License"). |
| Install (Windows) | README: `powershell -ExecutionPolicy ByPass -c "irm https://chatgpt.com/codex/install.ps1 \| iex"`; `npm install -g @openai/codex` (main package ships `bin/codex.js` + platform pkgs `@openai/codex-win32-x64`, `-win32-arm64`); Homebrew for mac/Linux; GitHub release binaries. Installers pull from `https://releases.openai.com/codex` with GitHub fallback. winget/scoop: not mentioned in README (UNVERIFIED). |
| Native Windows | Yes: win32 packages exist and include `codex-windows-sandbox-setup.exe` and `codex-command-runner.exe` (native sandbox helpers). **Contradiction:** `docs/install.md` still says "Windows 11 **via WSL2**" (stale vs README install.ps1 + win32 packages). |
| Auth | ChatGPT sign-in (browser), `codex login --device-auth` (device code), `codex login --with-api-key` (stdin), `--with-access-token` (stdin), `CODEX_API_KEY`/`OPENAI_API_KEY`. Credential store: `$CODEX_HOME/auth.json` by default (`cli_auth_credentials_store = file\|keyring\|auto`), `forced_login_method` restricts. |
| Detect auth | `codex login status` -> prints `Not logged in`, **exit code 1** (observed). Logged-in output not observed (NEEDS-LOGIN). App-server `account/read` also available. |
| Headless | `codex exec [PROMPT]` (stdin if `-`), `--json` (JSONL events), `-o/--output-last-message FILE`, `--output-schema FILE`, `-m`, `-p <profile>`, `-s read-only\|workspace-write\|danger-full-access`, `--approve-for-me`, `--dangerously-bypass-approvals-and-sandbox`, `-C`, `--add-dir`, `--ephemeral`, `--ignore-user-config`, `--ignore-rules`, `--skip-git-repo-check`, `--oss --local-provider lmstudio\|ollama`, `-c key=value` (any config), `--strict-config`. `codex exec resume [ID\|--last]`, `codex exec fork`. |
| `exec --json` events (source `codex-rs/exec/src/exec_events.rs`) | `thread.started{thread_id}`, `turn.started`, `turn.completed{usage{input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_output_tokens}}`, `turn.failed{error}`, `item.started/updated/completed{item}`, `error{message}`. Item types: `agent_message{text}`, `reasoning{text}`, `command_execution{command,aggregated_output,exit_code,status}`, `file_change{changes[{path,kind}],status}` (no diff text), `mcp_tool_call{server,tool,arguments,result,error,status}`, `collab_tool_call`, `web_search`, `todo_list`, `error`. No cost field. |
| Model / effort | `-m`; effort via `-c model_reasoning_effort="high"` (config key `model_reasoning_effort`, free-form string per model). No dedicated flag in `exec --help`. |
| Resume | `codex exec resume <UUID or thread name>` / `--last` / `--all`; `codex resume`, `codex fork`. |
| Interrupt | `exec`: UNVERIFIED (no doc readable). App-server: `turn/interrupt` request (in `ClientRequest.json`). ACP bridge `session/cancel` -> `interruptSessionTurn`. |
| Approval / sandbox | `approval_policy`: `on-request`, granular, `never`, etc. (AskForApproval oneOf); `sandbox_mode`: read-only / workspace-write / danger-full-access. exec has no interactive prompt path other than app-server. |
| MCP | `codex mcp add NAME (--url URL \| -- CMD...) [--env K=V] [--bearer-token-env-var X] [--oauth-client-id ..]`; config `[mcp_servers.<name>]` in `config.toml`; keys include `command,args,env,url,enabled,enabled_tools,disabled_tools,tools,startup_timeout_sec,tool_timeout_sec,bearer_token_env_var,oauth,...`. **stdio + streamable HTTP**; no SSE (ACP bridge reports `sse:false`). Per-session scoping: yes via `-c mcp_servers.<n>.enabled_tools=[..]` or ACP `session/new.mcpServers`. |
| Usage | exec `turn.completed.usage` incl. cached + cache-write + reasoning tokens (no dollars). App-server `thread/tokenUsage/updated` {last,total{inputTokens,cachedInputTokens,cacheWriteInputTokens,outputTokens,reasoningOutputTokens,totalTokens}, modelContextWindow}. |
| Tool-call visibility | **Partial/true**: shell commands (full command + aggregated output on completion), MCP calls (name+args+result), file changes (paths + kind only), web search. Built-in non-shell tools are not separate event types in exec. Full detail via app-server/ACP. |
| OpenAI-compat provider | `[model_providers.<id>]` with `name, base_url, env_key, requires_openai_auth, http_headers, env_http_headers, query_params, auth, aws, stream_*_retries, supports_websockets, wire_api`; select with `model_provider = "<id>"`; `-c` overrides. **`wire_api` accepts only `responses`** (schema `WireApi` oneOf single value). "Built-in IDs cannot be overridden." Local OSS: `--oss --local-provider ollama\|lmstudio`. => OpenRouter works only on its Responses-compatible endpoint (UNVERIFIED), Chat-Completions-only hosts do not. |
| Instruction files | `AGENTS.md` (+ `AGENTS.override.md`; both strings present in 0.161.0 binary), `project_doc_fallback_filenames` (default []), `project_doc_max_bytes` default 32768, `~/.codex/AGENTS.md` (global; path from docs unreadable, UNVERIFIED), nearer files take precedence ("More-deeply-nested AGENTS.md files take precedence", from bundled prompt). Skills: `.agents/skills`, `.codex/skills` strings present; SKILL.md format. No glob-scoped rule files (execpolicy `.rules` are command policy, not instructions). Config `~/.codex/config.toml`, project `.codex/config.toml`. |
| Quota | App-server `account/rateLimits/read` + `account/rateLimits/updated`: `rateLimits{primary,secondary: {usedPercent, windowDurationMins, resetsAt}, credits{balance,hasCredits,unlimited}, planType, limitId, rateLimitReachedType}`, `rateLimitsByLimitId`, `ordinaryUsageAllowed`; also `account/usage/read`. Through ACP only as markdown in `/status`. |
| App-server | `codex app-server [--listen stdio://\|unix://\|ws://IP:PORT]` JSON-RPC; `generate-json-schema` works offline (ran it). Methods incl. `thread/start,resume,fork`, `turn/start,steer,interrupt`, `account/login/start`, `item/commandExecution/requestApproval`, `mcpServerStatus/list`. This is the richer native transport (and what codex-acp 2.x wraps). Marked "[experimental]". |

### Evidence
- `codex --help`, `codex exec --help`, `codex exec resume --help`, `codex login --help`, `codex login status`, `codex mcp --help`, `codex mcp add --help`, `codex app-server --help`, `codex app-server generate-json-schema` (schemas in `codex/schema/`): binary `codex/c-0.161.0-linux-x64/package/vendor/x86_64-unknown-linux-musl/bin/codex`.
- Win32 payload listing: `codex/c-0.161.0-win32-x64/package/vendor/x86_64-pc-windows-msvc/` (codex.exe, codex-code-mode-host.exe, codex-resources/codex-windows-sandbox-setup.exe, codex-command-runner.exe).
- Raw GH (in `gh/`): `codex_README.md`, `codex_docs_install.md`, `codex_exec_events.rs`, `codex_config.schema.json`, `codex_appserver_README.md`, `codex_LICENSE`.
- `codex/schema/v2/GetAccountRateLimitsResponse.json`, `v2/ThreadTokenUsageUpdatedNotification.json`.
- `strings`-style grep of binary for `AGENTS.override.md` (4 hits), `.agents/skills` (2), `.codex/skills` (2), `auth.json` (4).

### Verified-here vs needs
- VERIFIED-HERE: version, flags, events schema, config schema, app-server schema, `login status` logged-out exit code, Windows payload contents, ACP bridge live initialize.
- NEEDS-LOGIN: real `exec --json` output, `login status` logged-in text, `account/rateLimits/read` values.
- NEEDS-WINDOWS: install.ps1, `CODEX_HOME` default (`%USERPROFILE%\.codex`, not read from a doc), Windows sandbox behavior ("Refusing to create helper binaries under temporary dir" warning appeared on Linux when CODEX_HOME was under /tmp: harmless here), npm shim `codex.cmd`.
- UNVERIFIED (docs unreachable): `~/.codex/AGENTS.md` lookup order details, exec interrupt behavior, Windows winget.

### Concerns
- `wire_api` = responses only (headline #2).
- `docs/install.md` WSL2 statement vs actual native Windows: treat native Windows as supported but test.
- CLI is explicitly experimental for `app-server`.
- codex-acp 2.x exposes quota only as text; for numbers we need an app-server side-channel (a second process or direct JSON-RPC).

---------------------------------------------------------------------
## 3. Gemini CLI

### Findings
| Item | Finding |
|---|---|
| Version / license | npm `@google/gemini-cli` latest **0.63.0** (2026-10-06), preview 0.64.0-preview.0, nightly 0.65.0-nightly.20261008. Ran `node bundle/gemini.js --version` -> `0.63.0`. License Apache-2.0 (package LICENSE; `docs/resources/tos-privacy.md`). |
| Install | `npm install -g @google/gemini-cli`, `npx @google/gemini-cli`, Homebrew, MacPorts, conda (README). **No native installer, no winget/scoop in README.** Requires Node >= 20. |
| Windows | Docs: "Operating System: macOS 15+, Windows 11 24H2+, Ubuntu 20.04+"; shell "Bash, Zsh, or PowerShell". Native (Node) supported; env examples in docs have PowerShell variants. |
| Version cmd | `gemini --version`. |
| Auth | Google-account OAuth (browser; creds cached, file `oauth_creds.json` string present in bundle; under `~/.gemini`, relocatable with `GEMINI_CLI_HOME`), `GEMINI_API_KEY`, Vertex (ADC `gcloud`, `GOOGLE_API_KEY` express, service account `GOOGLE_APPLICATION_CREDENTIALS`), Code Assist org with `GOOGLE_CLOUD_PROJECT`. `.env` lookup `.gemini/.env` upward then `~/.gemini/.env` (`%USERPROFILE%\.gemini\.env`). Docs for headless: "Recommended: Use Gemini API Key or Vertex AI"; also "Headless mode will use your existing authentication method, if an existing authentication credential is cached." No `gemini auth status`; ACP initialize lists authMethods `oauth-personal, gemini-api-key, vertex-ai, gateway`. Auth-state detection: UNVERIFIED (would need file presence/`security.auth.selectedType` in settings). |
| Headless | `gemini -p "<prompt>"` (also triggered when non-TTY); `-o text\|json\|stream-json`. `--skip-trust` or `GEMINI_CLI_TRUST_WORKSPACE=true` for untrusted workspaces in CI. Exit codes: 0 ok, 1 error, 42 input error, 53 turn limit. |
| stream-json events (source `packages/core/src/output/types.ts`, and enum strings confirmed in 0.63.0 bundle) | `init{session_id,model}`, `message{role,content,delta?}`, `tool_use{tool_name,tool_id,parameters}`, `tool_result{tool_id,status,output?,error?}`, `error{severity,message}`, `result{status,error?,stats{total_tokens,input_tokens,output_tokens,cached,input,duration_ms,tool_calls,models{...}}}`; all with `timestamp`. `json` mode: `{session_id,response,stats,error,warnings}`. |
| Model / effort | `-m/--model` (aliases auto/pro/flash/flash-lite or full id), `GEMINI_MODEL`. **No effort flag**; thinking is set in settings `modelConfigs` (`thinkingBudget` / `thinkingLevel: "HIGH"`). |
| Resume | `-r/--resume latest\|<index>\|<uuid>`, `--list-sessions`, `--delete-session`, `--session-id <uuid>` (new session with given UUID), `--session-file`. Sessions stored in `~/.gemini/tmp/<project_hash>/chats/` (project-scoped). |
| Interrupt | headless: UNVERIFIED. ACP: `cancel` -> stopReason `cancelled`. |
| Permission | `--approval-mode default\|auto_edit\|yolo\|plan`, `-y/--yolo`, `--policy`, `--admin-policy`, `--allowed-tools` (deprecated -> Policy Engine). |
| MCP | `settings.json` `mcpServers.<name>` with `command/args/env/cwd` (stdio), `url` (SSE), `httpUrl` (streamable HTTP), `headers`, `trust`, `includeTools`, `excludeTools`, OAuth; `gemini mcp` subcommand; `--allowed-mcp-server-names`. Per-session scoping: via ACP `session/new.mcpServers` (bundle references it) or `--allowed-mcp-server-names`. |
| Usage | stream-json `result.stats` with `cached` vs `input` split and per-model; no dollar cost. |
| Tool-call visibility | **true** (name, id, parameters, output/error). |
| OpenAI-compatible | **No.** Only `GOOGLE_GEMINI_BASE_URL` / `GOOGLE_VERTEX_BASE_URL` (Gemini-protocol proxies; HTTPS unless localhost) and ACP "gateway" auth method with `protocol: "google"`. No OpenAI base_url anywhere in config docs (grep empty). |
| Instruction files | `GEMINI.md` default (`DEFAULT_CONTEXT_FILENAME = "GEMINI.md"` in bundle); configurable `context.fileName` (string or array, e.g. `["CONTEXT.md","GEMINI.md"]`) so `AGENTS.md` can be added by config. Load: `~/.gemini/GEMINI.md`, then workspace dirs and parents, then JIT: when a tool touches a file the CLI scans that directory and ancestors up to a trusted root. `@file.md` imports. Skills: `.gemini/skills/` or alias `.agents/skills/` (alias wins), `~/.gemini/skills` / `~/.agents/skills`. No glob-scoped rules. |
| Quota | Per day: Code Assist individual 1,000; AI Pro 1,500; Ultra 2,000; API key free 250 (docs table). Programmatic remaining-quota: internal `retrieveUserQuota` (buckets modelId, remainingFraction, remainingAmount, resetTime) exists in bundle, surfaced only in interactive `/stats`; not a non-interactive command. |

### Evidence
- `gemini --version`, `--help` from `gemini/_google_gemini-cli/package/bundle/gemini.js` (Node 22).
- Live `gemini --acp` initialize (probe `acp_test/init.py`, output `acp_test/gemini_init.out`): agentInfo `gemini-cli 0.63.0`, `loadSession:true`, `promptCapabilities{image,audio,embeddedContext}`, `mcpCapabilities{http,sse}`; no `sessionCapabilities`.
- Docs in `gh/gem/`: `docs_cli_headless.md`, `docs_cli_acp-mode.md`, `docs_cli_gemini-md.md`, `docs_cli_session-management.md`, `docs_cli_skills.md`, `docs_tools_mcp-server.md`, `docs_reference_configuration.md`, `authentication.mdx`, `docs_resources_quota-and-pricing.md`, `docs_resources_tos-privacy.md`, `x_docs_get-started_installation.mdx`, `types.ts`.
- ACP source in bundle `gemini-6YZTUGIX.js` lines ~15300-15480 (`_meta.quota`), 16644 (capabilities), 16719-16760 (cancel, setSessionMode, unstable_setSessionModel).

### Verified-here vs needs
- VERIFIED-HERE: version, flags, ACP initialize, event schema, docs.
- NEEDS-LOGIN: any real stream; `--acp` prompt turn; `oauth_creds.json` location on Windows (`%USERPROFILE%\.gemini\oauth_creds.json` presumed, UNVERIFIED).
- NEEDS-WINDOWS: `npm` shim `gemini.cmd`, spawn behavior.

### Concerns
- Consumer retirement (headline #1) and the ToS clause about third-party software (section 9).
- ACP side is thinner than others: no `session/resume`, no `usage_update`, usage only in non-standard `_meta.quota`.
- `docs/get-started/authentication.md` and `installation.md` 404 on main (renamed to `.mdx`); expect doc churn. Registry pins `@google/gemini-cli@0.63.0 --acp`.

---------------------------------------------------------------------
## 4. OpenCode

### Findings
| Item | Finding |
|---|---|
| Org / repo | **Moved**: registry says `https://github.com/anomalyco/opencode` (authors "Anomaly"); `raw.githubusercontent.com/sst/opencode/dev/README.md` returns identical content (redirect/alias); default branch is `dev` (`main` README 404). npm `opencode-ai`. |
| Version / license | npm latest **1.18.35** (2026-10-06); `opencode --version` -> `1.18.35`. MIT (`opencode-ai/package/LICENSE`: "MIT License, Copyright (c) 2025 opencode"). |
| Install (Windows) | `scoop install opencode`, `choco install opencode`, `npm i -g opencode-ai@latest`, desktop `.exe` / `scoop bucket add extras; scoop install extras/opencode-desktop` (README). npm package contains `opencode-windows-x64`, `-windows-x64-baseline`, `-windows-arm64`. |
| Native Windows | Binary exists; docs page "Windows (WSL)": "While OpenCode can run directly on Windows, we recommend using ... WSL for the best experience." Env var `OPENCODE_GIT_BASH_PATH` exists. |
| Auth | `opencode providers login [url]` / `logout` / `list` (alias `auth`); credentials in `~/.local/share/opencode/auth.json` (docs) and env keys. `opencode providers list` prints configured providers + detected env vars (non-interactive; usable as auth detect, parse needed). ACP `authMethods: [opencode-login]`. |
| Headless | `opencode run [message..] --format json` (raw JSON events), `-m provider/model`, `-c/--continue`, `-s/--session`, `--fork`, `--agent`, `--variant`, `-f/--file`, `--dir`, `--auto` (auto-approve unless denied), `--attach http://host:port`, `--title`, `--thinking`. Also `opencode serve` (OpenAPI 3.1 at `/doc`, SSE `/global/event`) + JS SDK (`@opencode-ai/sdk/v2`). |
| `run --format json` events (source `cli/cmd/run.ts`) | one JSON per line `{type, timestamp, sessionID, ...}` with `type` in `tool_use` (emitted only when tool state is `completed` or `error`; `part.state{status,input,output,title,metadata,time}`), `step_start`, `step_finish` (`part.cost`, `part.tokens{total?,input,output,reasoning,cache{read,write}}`), `text` (only when complete, no deltas), `reasoning` (needs `--thinking`), `error`. |
| Permissions in `run` | `permission.asked` -> with `--auto`: reply `once`; otherwise prints "permission requested: ...; auto-rejecting" and replies `reject`. => non-interactive headless cannot ask. |
| Model / effort | `-m provider/model`; `--variant` ("provider-specific reasoning effort, e.g., high, max, minimal"); per-model `variants` in config. |
| Resume | `-c`, `-s <id>`, `--fork`; ACP `session/resume`, `session/fork`, `session/list`, `session/close`, `loadSession`. |
| Interrupt | `run`: no SIGINT/abort handling found in `run.ts` (UNVERIFIED default behavior). ACP `cancel` implemented. Server has abort routes (not read). |
| MCP | `opencode.json` `mcp.<name>`: `type: local` (`command[]`, `environment`) or `remote` (`url`, headers, OAuth), `enabled`, `timeout` (default 5000ms); `opencode mcp add/list/auth/logout/debug`; tools namespaced by server prefix; per-agent enable/disable in `tools`. Per-session: ACP `session/new.mcpServers` registered (`registerMcpServers`), or `OPENCODE_CONFIG_CONTENT` inline config. |
| Usage | `step_finish` cost+tokens in JSON; ACP `usage_update{used,size,cost{amount,USD}}` and prompt `usage{inputTokens,outputTokens,totalTokens,cachedReadTokens,cachedWriteTokens,thoughtTokens}`; `opencode stats [--days --models --tools --project]`; `opencode export <sessionID>`. |
| Tool-call visibility | **true** (input+output; completed-only in `run`; start+update in ACP). |
| OpenAI-compatible | **Yes, first-class.** `provider.<id>`: `{"npm":"@ai-sdk/openai-compatible","name":..., "options":{"baseURL":"http://127.0.0.1:1234/v1"}, "models":{...}}` (docs show LM Studio, llama.cpp, Atomic Chat, Helicone, Eden AI); `@ai-sdk/openai` for `/v1/responses`; `baseURL` overridable on any provider; OpenRouter built in. Ollama via same pattern (docs for Ollama not separately read). |
| Instruction files | `AGENTS.md` project (walks up from cwd) and `~/.config/opencode/AGENTS.md`; Claude fallbacks: `CLAUDE.md` (only if no AGENTS.md), `~/.claude/CLAUDE.md`; "first matching file wins in each category"; `instructions: ["CONTRIBUTING.md","docs/*.md",".cursor/rules/*.md"]` globs in `opencode.json`. Disable Claude fallbacks: `OPENCODE_DISABLE_CLAUDE_CODE*`. Skills: `.opencode/skills`, `.claude/skills`, `.agents/skills` (project, walking up to git root) and `~/.config/opencode/skills`, `~/.claude/skills`, `~/.agents/skills`. No frontmatter glob scoping; `instructions` globs are the glob mechanism. |
| Quota | None found (stats are local accounting). |

### Evidence
- `opencode --version/--help/run --help/acp --help/providers/stats/mcp/serve --help` run from `opencode/opencode-linux-x64/package/bin/opencode` (HOME/XDG redirected).
- Live `opencode acp` initialize: `acp_test/opencode_init.out`: `OpenCode 1.18.35`, `loadSession:true`, `mcpCapabilities{http,sse}`, `promptCapabilities{embeddedContext,image}`, `sessionCapabilities{close,fork,list,resume}`, authMethods `opencode-login`.
- Raw GH under `gh/oc/`: `README_dev.md`, `acp.mdx`, `windows-wsl.mdx`, `providers.mdx`, `rules.mdx`, `skills.mdx`, `mcp-servers.mdx`, `config.mdx`, `packages_opencode_src_cli_cmd_run.ts`, `acp_service.ts`, `acp_permission.ts`, `acp_usage.ts`, `types.gen.ts` (StepFinishPart, ToolStateCompleted).
- Registry entry `gh/reg_opencode.json`.

### Verified-here vs needs
- VERIFIED-HERE: version, ACP capabilities (live), JSON event types from source, provider config docs.
- NEEDS-LOGIN/model: any real turn.
- NEEDS-WINDOWS: ConPTY/terminal behaviors, scoop/choco paths, `auth.json` location on Windows (docs only say `~/.local/share/opencode`; Windows XDG mapping UNVERIFIED).

### Concerns
- Headless `run` is lossy (no deltas, completed-only tools) and non-interactive for permissions; use ACP.
- Windows is "supported but WSL recommended"; risk of Windows-only bugs.
- Fast-moving: 1.18.x with `tui-v2` / `native-llm` experimental flags.
- OpenCode's compatibility with CLAUDE.md/.claude/skills means a CLAUDE.md in a repo is read by OpenCode when no AGENTS.md exists; the generator must avoid duplicate/conflicting content.

---------------------------------------------------------------------
## 5. Goose

### Findings
| Item | Finding |
|---|---|
| Org / repo | `aaif-goose/goose` (README: "goose is part of the Agentic AI Foundation (AAIF) at the Linux Foundation"). `block/goose` raw returns identical README/Cargo.toml/LICENSE (alias). ACP registry entry still names `repository: https://github.com/block/goose` but release URLs under `block/goose/releases/...v1.53.0`. |
| Version / license | Released **1.53.0** (registry; npm `@aaif/goose-acp` 1.53.0 on 2026-10-02; `goose --version` -> ` 1.53.0`). `main` Cargo workspace `version = "1.54.0"` (unreleased). Apache-2.0 (LICENSE; Cargo `license = "Apache-2.0"`). |
| Install (Windows) | Desktop app (Windows); CLI via PowerShell script `download_cli.ps1` from the repo, or `download_cli.sh` in Git Bash; registry binary `goose-x86_64-pc-windows-msvc.zip` with `cmd: "./goose-package\\goose.exe"`; npm `@aaif/goose-acp` (bin `goose`) pulls `@aaif/goose-binary-win32-x64`. No winget/scoop in installation doc (grep empty). |
| Native Windows | Yes, per docs: "To install goose natively on Windows, you need one of ... Git Bash (recommended) ... PowerShell". Config at `%APPDATA%\Block\goose\config\config.yaml`. |
| Auth | Provider keys, no OAuth to Goose itself; secrets in OS keyring or `secrets.yaml`; env var per provider (`OPENAI_API_KEY` ...) takes precedence; `goose configure`; some providers browser OAuth (OpenRouter automatic setup). ACP auth method `goose-provider` ("Run `goose configure`"). `goose doctor` exists. Non-interactive auth detect: UNVERIFIED (no `auth status`). |
| Headless | `goose run -t "<text>"` / `-i file|-` / `--recipe <name\|path> --params k=v`; `--output-format text\|json\|stream-json`; `-q`; `--no-session`; `-n/--name`, `-r/--resume`, `--session-id`; `--provider`, `--model`; `--max-turns`; `--with-extension "<cmd>"`, `--with-streamable-http-extension URL`, `--with-builtin`, `--no-profile`; `--system`; `--stats`. Recipes (yaml/json) can enforce structured output via `response.json_schema` + a `final_output` tool. |
| `--output-format json` | single object `{messages: [Message...], metadata{total_tokens, input_tokens?, output_tokens?, cache_read_input_tokens?, cache_write_input_tokens?, cost_usd?, status}}`. |
| `--output-format stream-json` events (source `session/mod.rs` `StreamEvent`) | `{"type":"message","message":Message}`, `{"type":"notification","extension_id":..., "log"{message} \| "progress"{progress,total,message}}`, `{"type":"error","error":str}`, `{"type":"complete", total_tokens, input_tokens, output_tokens, cache_read_input_tokens, cache_write_input_tokens, cost_usd}`. Tool request/response live inside `Message` content; exact JSON shape of `Message` not read (UNVERIFIED). |
| Model / effort | `--model`, `GOOSE_MODEL`, `GOOSE_PROVIDER`; no unified effort flag. Provider-specific: `CLAUDE_THINKING_TYPE` / budget (Anthropic, Databricks), `GEMINI3_THINKING_LEVEL`. |
| Resume | headless: `-r` with `-n`/`--session-id` (last session by default). ACP: `loadSession` + list/delete/close only. For ACP *providers*: "No session fork or resume" (docs). |
| Interrupt | ACP `on_cancel` -> stopReason Cancelled (server.rs lines ~2287, 2400). Headless: UNVERIFIED. |
| Permission | `GOOSE_MODE` = `auto \| approve \| smart_approve \| chat`. |
| MCP ("extensions") | `config.yaml` `extensions:`; CLI `--with-extension` (stdio), `--with-streamable-http-extension`, `--with-builtin`; recipes list extensions. ACP: `mcpCapabilities{http:true, sse:false}` plus stdio; `session/new.mcpServers` merged into extensions (`add_mcp_servers`). |
| Usage | stream-json `complete` + json metadata (tokens incl. cache read/write, `cost_usd`); ACP `usage_update{used,size,cost USD}` + custom `_goose` usage notifications (`accumulated_input_tokens`, `accumulated_output_tokens`, `accumulated_cost`) + prompt `usage`. |
| Tool-call visibility | ACP: **true** (`ToolCallNotifier`, tool_call / tool_call_update, permission requests). Headless: likely true via `Message` tool content (shape UNVERIFIED). |
| OpenAI-compatible | **Yes.** Built-in providers: OpenAI (`OPENAI_API_KEY`, optional `OPENAI_HOST`, `OPENAI_BASE_PATH`, "supports OpenAI-compatible endpoints (e.g., self-hosted LLaMA, vLLM, KServe)"), OpenRouter (`OPENROUTER_API_KEY`, `OPENROUTER_HOST`), Ollama (`OLLAMA_HOST`), LM Studio (localhost:1234), Docker Model Runner, Ramalama; **custom providers** = JSON files "must use OpenAI, Anthropic, or Ollama compatible API formats" with headers/keys; env `GOOSE_PROVIDER__TYPE/__HOST/__API_KEY`. Also local inference (`goose local-models`). |
| Instruction files | `AGENTS.md` then `.goosehints` by default (`CONTEXT_FILE_NAMES` env to add e.g. `CLAUDE.md`, `.cursorrules`); global `~/.config/goose/.goosehints`; hierarchical: working dir up to repo root at start, nested directories loaded when files are accessed. Skills: `~/.agents/skills/`, `.agents/skills/`, `~/.agents/plugins/<p>/`; back-compat `.goose/skills/`, `.claude/skills/`, `~/.claude/skills/`. No glob frontmatter. |
| Quota | None. |
| ACP directions | See section 7; `goose acp` (stdio agent), `goose serve` (ACP over HTTP/WebSocket; needs `GOOSE_SERVER__SECRET_KEY` or `--dangerously-unauthenticated`), and ACP providers (client side). |

### Evidence
- `goose --version`, `goose --help`, `goose run --help` from `inst_goose/node_modules/@aaif/goose-binary-linux-x64/bin/goose` (installed via npm `@aaif/goose-acp@1.53.0`).
- Live `goose acp` initialize: `acp_test/goose_raw.out`: `agentInfo goose 1.53.0`, `loadSession:true`, `promptCapabilities{image:true,audio:false,embeddedContext:true}`, `mcpCapabilities{http:true,sse:false}`, `sessionCapabilities{list,delete,close}`, authMethods `goose-provider`.
- Raw GH under `gh/goose/`: `README_aaif-goose.md`, `Cargo_aaif-goose.toml`, `crates_goose-cli_src_cli.rs` (Acp command at ~831, Run `--output-format` ~313), `crates_goose-cli_src_session_mod.rs` (StreamEvent ~148), `crates_goose_src_acp_mod.rs` (AcpProvider), `acp_server.rs` (capabilities ~1811, usage ~764), docs `d_guides_*.md`, `d_getting-started_*.md`.
- Registry `gh/reg_goose.json`.

### Verified-here vs needs
- VERIFIED-HERE: version, run flags, ACP agent initialize live, StreamEvent enum from source, docs.
- NEEDS-LOGIN/model: any turn; headless JSON examples.
- NEEDS-WINDOWS: PowerShell installer, `goose-package\goose.exe` layout, Git Bash dependency for the developer extension.

### Concerns
- Goose is general-purpose; its tool vocabulary differs (developer extension). Instruction file `AGENTS.md` shared with others is fine.
- Registry/README metadata in flux (block -> aaif-goose; versions 1.53.0 vs main 1.54.0).
- `goose serve` over HTTP/WebSocket is an alternative transport but needs a secret key.

---------------------------------------------------------------------
## 6. Claude Design

### Findings (VERIFIED-HERE from Anthropic's help center HTML, fetched with curl)
- "Claude Design lets you create designs, interactive prototypes, one-pagers, and other visual work by chatting with Claude. It's one of the templates you can start an artifact from, so you can use it in any chat, in Claude Code, and from the Artifacts tab, with on-canvas editing and your design system included."
- Standalone web app: "claude.ai/design". Claude Code integration: `/design` ("use /design to import a design into your codebase, export your code as a live prototype") and `/design-sync` ("pull in your design system").
- Launched 2026-04-17 by Anthropic Labs (TechCrunch URL `techcrunch.com/2026/04/17/...`; SNIPPET-ONLY) on Opus 4.7. Plan availability: beta on Pro/Max/Team/Enterprise per help-center snippet (a newer release note dated 2026-09-16 says Artifacts incl. Claude Design are on every plan; SNIPPET-ONLY, conflicting).
- Exports: HTML bundle, PPTX, PDF, hand-off to Claude Code (admin guide snippet).

### Conclusion
Not a CLI or agent process: it is a product surface (web + artifact template + a Claude Code slash command). No ACP, no headless, no machine-readable API found. For our app it is NOT an adapter target. If a "Claude Design" mention in requirements means something else, UNVERIFIED.

Evidence: `claude/cd_14604416-get-started-with-claude-design.html`, `.../cd_14604406-claude-design-admin-guide-for-team-and-enterprise-plans.html`.

---------------------------------------------------------------------
## 7. ACP: native vs bridges

### 7.1 Matrix (all "VERIFIED-HERE live initialize" unless noted)
| CLI | ACP surface | Command | Maintainer / license | initialize result |
|---|---|---|---|---|
| Claude Code | **Bridge only** | `npx -y @agentclientprotocol/claude-agent-acp@0.87.0` (bin `claude-agent-acp`) | Registry authors: Anthropic, Zed Industries, JetBrains; repo `agentclientprotocol/claude-agent-acp`; package.json author "Zed Industries"; npm license Apache-2.0 (LICENSE "Copyright 2025 Zed Industries, Inc. and contributors"); **registry says `"license": "proprietary"`**; Node >=22; deps `@anthropic-ai/claude-agent-sdk@0.3.287` (bundles Claude Code 2.1.287, 234 MB), `@agentclientprotocol/sdk@1.7.0` | `loadSession:true`; `promptCapabilities{image,embeddedContext}`; `mcpCapabilities{http,sse}`; `sessionCapabilities{additionalDirectories,close,delete,fork,list,resume,subagents}`; `auth{logout}`, `providers{}`; `_meta.claudeCode.promptQueueing`, `_meta.steering`, `authMethods: []` for a client without terminal-auth capability. Old `@zed-industries/claude-code-acp` 0.16.2 is **deprecated**: "This package has been renamed to @agentclientprotocol/claude-agent-acp". |
| Codex | **Bridge** (also native `codex app-server` JSON-RPC, not ACP) | `npx -y @agentclientprotocol/codex-acp@2.1.1` (bin `codex-acp`) | repo `agentclientprotocol/codex-acp`; registry authors OpenAI, JetBrains s.r.o, Zed Industries; Apache-2.0; deps `@openai/codex ^0.159.1` (installed 0.159.3), vscode-jsonrpc, zod; engines unspecified; old `@zed-industries/codex-acp` 0.16.0 deprecated in its README | `loadSession:true`; `promptCapabilities{embeddedContext,image}`; `mcpCapabilities{acp:false,http:true,sse:false}`; `sessionCapabilities{resume,list,close,delete,fork,additionalDirectories,subagents}`; `auth{logout}`; `authMethods`: `api-key` (+ ChatGPT browser, ChatGPT device-code, gateway when client opts in); `_meta.steering` |
| Gemini CLI | **Native** | `gemini --acp` (`--experimental-acp` deprecated alias) | Google, Apache-2.0 | `loadSession:true`; `promptCapabilities{image,audio,embeddedContext}`; `mcpCapabilities{http,sse}`; no `sessionCapabilities`; authMethods `oauth-personal, gemini-api-key, vertex-ai, gateway` |
| OpenCode | **Native** | `opencode acp` | anomalyco, MIT | `loadSession:true`; `mcpCapabilities{http,sse}`; `promptCapabilities{embeddedContext,image}`; `sessionCapabilities{close,fork,list,resume}`; authMethod `opencode-login` |
| Goose | **Native (agent)** and ACP *client* | `goose acp` (stdio); `goose serve` (HTTP/WS) | Apache-2.0 | `loadSession:true`; `promptCapabilities{image:true,audio:false,embeddedContext:true}`; `mcpCapabilities{http:true,sse:false}`; `sessionCapabilities{list,delete,close}`; authMethod `goose-provider` |
| Antigravity | **First-party ACP server binary** (SNIPPET-ONLY on behavior) | registry `antigravity-acp` 1.3.0: `agy_acp_server(.exe)` from `dl.google.com/agy-extensions/releases/<os>/agy-acp-server-1.3.0-*.zip` | Google LLC, license "proprietary", `license_url https://antigravity.google/terms` | not run (dl.google.com unreachable). Community wrappers exist (sibbl/google-antigravity-acp MIT, wraps `agy --input-format=stream-json --output-format=stream-json`). |

All of the above register `agentCapabilities` via the registry JSONs I read: `gh/reg_{gemini,opencode,goose,claude-acp,codex-acp,antigravity-acp}.json`. The registry also documents that entries "return valid `authMethods` in the ACP handshake" (`gh/acp_registry_README.md`); registry URL `cdn.agentclientprotocol.com/registry/v1/latest/registry.json` was unreachable (proxy), so the `agent.json` files were read from the GitHub repo.

### 7.2 Methods and behaviors by bridge (source read)
| Behavior | claude-agent-acp 0.87.0 | codex-acp 2.1.1 | gemini 0.63.0 | opencode 1.18.35 | goose 1.53.0 |
|---|---|---|---|---|---|
| `session/cancel` | yes (`cancel` -> `cancelTurns`, `query.interrupt()`; stopReason `cancelled`) | yes (`cancel` -> `turn/interrupt`) | yes (`cancel`) | yes (`cancel`, stopReason cancelled) | yes (`on_cancel`) |
| `session/request_permission` | yes (`requestPermissionFromClient`, plus ensures `tool_call` exists first) | yes (command, file change, permissions, MCP elicitation) | yes (`connection.requestPermission`) | yes (`acp/permission.ts`) | yes (PermissionOption kinds) |
| `session/update` tool_call / tool_call_update | yes | yes | yes | yes | yes |
| `usage_update` {used,size,cost} | yes, with `cost{amount,USD}` | yes, **no cost** (`used`,`size` only) | **no** | yes with cost | yes with cost (+ custom `_goose` notifications) |
| Prompt `usage` with cachedRead/Write | yes (`cachedReadTokens`,`cachedWriteTokens`) | yes (`cachedReadTokens` from `cachedInputTokens`) | no; `_meta.quota.token_count{input_tokens,output_tokens}` + per-model `model_usage`; no cache split | yes (cache read/write, thought) | yes (`response.usage`) |
| Plan-quota windows | `_meta["_claude/rateLimit"]` on `usage_update` (from `rate_limit_event`) | markdown only in `/status`; structured only via app-server | none | none | none |
| `session/resume` | yes | yes | no (loadSession only) | yes | no (loadSession only) |
| `session/fork` | yes (unstable) | yes | no | yes | module exists; not advertised |
| `session/list` | yes | yes | no | yes | yes |
| Model/mode switching | `session/set_mode`, `set_config_option` (model, effort, fast mode) | `set_config_option`/`unstable_setSessionModel`, modes read-only/workspace-write/agent/agent-full-access | `setSessionMode`, `unstable_setSessionModel` | `setSessionMode`, `unstable_setSessionModel`, `setSessionConfigOption` (variants) | `session/set_mode`, config options |
| Client MCP servers in `session/new` | yes (http, sse, stdio) | yes (stdio, http) | yes | yes | yes (stdio, http) |
| Auth over ACP | terminal-type auth methods (`claude auth login --claudeai/--console`), gateway; `authenticate` | api-key / chat-gpt / chat-gpt-device-code / gateway; custom gateway = `wire_api: responses` only | oauth-personal / gemini-api-key / vertex-ai / gateway | opencode-login (terminal-auth `opencode auth login`) | goose-provider (run `goose configure`) |
| Extra extensions | `_session/steering`, AIR goal/diff patch etc., `_auth/status_update` push | same family (AIR) + `authentication/status` ext method, steering | none | none | `_goose` custom requests/notifications |

Notes
- The Claude bridge `initialize` returns `authMethods: []` for a client that does not advertise terminal-auth; auth status is pushed via `_auth/status_update` (derived from `claude auth status --json`). A generic client must advertise `clientCapabilities.auth.terminal` (or `_meta["terminal-auth"]`) to receive login methods.
- Go SDK: `github.com/coder/acp-go-sdk` v0.13.5 (2026-06-02, Apache-2.0, go 1.21). Includes `session/{new,load,resume,fork,list,close,delete,prompt,cancel,set_mode,set_config_option,request_permission,update}`, `providers/*`, `fs/*`, `terminal/*`, `usage_update`; `schema/version` file = 0.13.5. Marked **UNSTABLE** in its comments: `usage_update` (`SessionUsageUpdate`), prompt-turn `Usage`.
- The ACP TypeScript SDK `@agentclientprotocol/sdk` is at 1.7.0 (2026-10-02, Apache-2.0); all three bridges track it.

### 7.3 Goose conflict resolution (primary source)
- `goose --help` lists `acp  Run goose as an ACP agent server on stdio` and `serve  Start ACP server over HTTP and WebSocket` (ran).
- `crates/goose-cli/src/cli.rs`: "`/// Run goose as an ACP (Agent Client Protocol) agent`" `#[command(about = "Run goose as an ACP agent server on stdio")] Acp {...}` dispatching `goose::acp::server::run(...)`.
- `crates/goose/src/acp/mod.rs` exports `AcpProvider, AcpProviderConfig` (client side) and docs `guides/acp-providers.md`: "goose supports ACP agents as providers ... Claude ACP wraps claude-agent-acp ... Codex ACP ... Amp ... Pi".
- Live initialize against `goose acp` succeeded with `agentInfo{name:"goose",version:"1.53.0"}`.
=> Both statements are true; our ACP client can drive Goose.

### 7.4 Concerns
- Claude/Codex bridges need Node (>=22 for Claude) and each downloads a native CLI copy (Claude 234 MB; Codex ~300 MB `@openai/codex-linux-x64`). On Windows that means Node + npm/npx availability (`npx.cmd`) as a prerequisite, or vendoring.
- Bridges are young and move weekly (0.87.0 stable, 0.87.1-preview.1 same day; codex-acp 2.1.1 stable, 2.1.2-preview.3). Pin exact versions in the manifest.
- Registry marks claude-acp "proprietary" and antigravity-acp "proprietary": redistribution/bundling must be checked.
- Gemini ACP lacks resume/list/usage_update; our "one generic adapter" needs capability gating per agent (read `agentCapabilities` at runtime, do not assume).

---------------------------------------------------------------------
## 8. Antigravity (Google)

Status: SNIPPET-ONLY except where marked VERIFIED-HERE (raw GitHub of `google-antigravity/antigravity-cli`, which holds README + CHANGELOG only; LICENSE 404, so source is not published there).

- What it is today (VERIFIED-HERE, README): "Antigravity CLI understands your codebase, makes edits with your permission, and executes commands"; command `agy`; part of a family with "Antigravity 2.0" desktop GUI; shared agent engine and settings. Latest CHANGELOG entry **1.3.1**. Install: `curl -fsSL https://antigravity.google/cli/install.sh | bash`; Windows PowerShell `irm https://antigravity.google/cli/install.ps1 | iex`; CMD `curl ... install.cmd`. Windows is first-class in the changelog ("Improved the Windows command sandbox so sandboxed commands no longer need administrator rights").
- Successor of Gemini CLI for consumers (SNIPPET-ONLY: Google Developers Blog "An important update: Transitioning Gemini CLI to Antigravity CLI"; GitHub discussion #28017; JetBrains issue AIR-5394). Third-party guides say it is a Go binary (not npm). `agy plugin import gemini` migration importer exists (guide snippet). GEMINI.md and AGENTS.md are read (CHANGELOG: "reading your global rules ... `AGENTS.md`, `GEMINI.md`, `skills.json`, `rules.json`"; "manifests now load from every `.agents/` directory between the working directory and the project root").
- Auth (VERIFIED-HERE, README): "The CLI authenticates via the system keyring, falling back to Google Sign-In if no active session exists"; remote/SSH prints URL; `/logout`. `GEMINI_API_KEY` sessions also supported (CHANGELOG). Headless relies on cached credentials (snippet).
- Headless (CHANGELOG VERIFIED-HERE + docs snippet): `agy -p/--prompt`, `--output-format text|json|stream-json`, `--input-format stream-json` (continuous process, one `result` per turn), `--json-schema`, `--print-timeout`, `--effort` ("Improved reasoning effort level ... selectable with `--effort`"), `-p "/usage"` prints plan-quota reset times (so quota IS available non-interactively: "Improved `-p "/usage"` to show quota reset times in your local time zone ... piped and `--json` output still use UTC timestamps"). Exit code 3 + `AGY_ERROR: {...}` on stderr for agent/model failure. stream-json: `init{conversation_id, init{cwd,tools,permission_mode}}`, `step_update{step_type, state, text_delta?, duration, token usage}`, `result` (docs snippet). Known bugs (issues #902, #1065, #1181 snippets): SUCCESS with empty response, plan mode executing writes. Permissions in print mode: unanswerable requests are soft-denied (CHANGELOG: "silently skipping tool actions they were not permitted to take ... report them as `denied_actions`").
- MCP: `mcp_config.json`, plugins with bundled MCP servers namespaced `<plugin>_<server>`.
- ACP: registry `antigravity-acp` 1.3.0 (first-party, proprietary, Windows binary `agy_acp_server.exe`). Zed/JetBrains threads: JetBrains ticket says ACP "added to Antigravity CLI just last week". `agy --acp` flag existence UNVERIFIED. Community adapters exist (sibbl MIT; dongitran; jiridanek).
- Terms (SNIPPET-ONLY): "Clause 6 of the Antigravity Additional Terms says using third-party software, tools or services to access the Service (e.g. OpenClaw with Antigravity OAuth) is a breach"; a forum reply says launching the official `agy` binary headless as a local child process using its cached login "is a supported workflow" (not shown to be from Google staff). Suspension wave reported Feb 2026. antigravity.google/terms unreachable.
- License: closed (registry "proprietary"; repo has no LICENSE; third-party guide says "not open source").
- Version/min: 1.3.1 latest per CHANGELOG; ACP server 1.3.0.

Concerns: young, fast-changing, closed-source, ToS ambiguous; do not build the adapter before Google answers the orchestration question in writing. Good candidate for "ACP via registry binary" because it is declaratively spawnable.

---------------------------------------------------------------------
## 9. Terms of service (quotes)

### 9.1 Anthropic / Claude Code (VERIFIED-HERE, code.claude.com + anthropic.com)
`https://code.claude.com/docs/en/legal-and-compliance` (saved via fetch; text also in `claude/docs/`):
- "OAuth authentication is intended exclusively for purchasers of Claude Free, Pro, Max, Team, and Enterprise subscription plans and is designed to support ordinary use of Claude Code and other native Anthropic applications."
- "Developers building products or services that interact with Claude's capabilities, including those using the Agent SDK, should use API key authentication through Claude Console or a supported cloud provider. Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow."
- "Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code as described under *Can customers offer Claude Code in their products?* above."
- Hosting section: "The Claude Code binary must not be modified ... customers may not remove, disable, or restrict any authentication method built into it" and "Customers may not pay for, resell, or intermediate Claude usage on their end users' behalf. Each end user must authenticate with their own Anthropic API key, Claude subscription plan credentials, or 3P inference provider credential".
- "Advertised usage limits for Pro and Max plans assume ordinary, individual usage of Claude Code and the Agent SDK."
- Agent SDK overview (`claude/docs/agent-sdk_overview.md` line 44): "Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK. Use the API key authentication methods described in the Quickstart instead."
- Consumer Terms of Service (anthropic.com/legal/consumer-terms, "Effective October 8, 2025"; saved `claude/consumer.txt`): "Except when you are accessing our Services via an Anthropic API Key or where we otherwise explicitly permit it, to access the Services through automated or non-human means, whether through a bot, script, or otherwise." and "You may not share your Account login information, Anthropic API key, or Account credentials with anyone else."
- Support article "Using Claude Code with your Pro or Max plan" (WebFetch summary, quotes unverified verbatim): usage limits "shared across Claude and Claude Code"; setting `ANTHROPIC_API_KEY` makes Claude Code use API billing instead of the subscription.
- Interpretation: spawning the user's own installed, logged-in `claude` as a local child process from a desktop app that never sees tokens is closest to "end user signing in to the unmodified Claude Code binary", but the Agent SDK sentence and the ACP bridge (an Agent SDK product) are a grey zone. Legal review required before shipping subscription-based Claude support.

### 9.2 Google / Gemini CLI (VERIFIED-HERE, `gh/gem/docs_resources_tos-privacy.md`)
- "Directly accessing the services powering Gemini CLI (for example, the Gemini Code Assist service) using third-party software, tools, or services (for example, using OpenClaw with Gemini CLI OAuth) is a violation of applicable terms and policies. Such actions may be grounds for suspension or termination of your account."
- Supported auth: Google account (Gemini Code Assist), Gemini API key, Vertex AI key; terms table per method.
- Interpretation: spawning the official gemini binary is not "directly accessing the services"; routing its OAuth token elsewhere is. Ambiguity remains for orchestrators. Plus the 2026-06-18 consumer cut-off (headline #1).

### 9.3 OpenAI / Codex (docs unreachable)
- Repo README (VERIFIED-HERE): "We recommend signing into your ChatGPT account to use Codex as part of your Plus, Pro, Business, Edu, or Enterprise plan. ... You can also use Codex with an API key". Repo is Apache-2.0.
- SNIPPET-ONLY: developers.openai.com/codex/auth recommends "API key authentication for programmatic Codex CLI workflows, such as CI/CD jobs"; OpenAI launched "Sign in with ChatGPT" for third-party tools (The New Stack, snippet); OpenAI staff pointed to Terms of Use in discussion #8338 without a definitive answer. No policy text could be read here. UNVERIFIED.

### 9.4 OpenCode, Goose
- Open-source licenses only (MIT / Apache-2.0); no service terms of their own. Using Claude/Google subscriptions through them is governed by the provider terms above. Goose's own docs advertise "Use Your Existing Subscriptions ... ACP providers let you use goose with your existing Claude Code or ChatGPT Plus/Pro subscriptions" (that is a statement by Goose, not by Anthropic/OpenAI; see 9.1).
- Antigravity: see section 8 (SNIPPET-ONLY).

---------------------------------------------------------------------
## 10. Quota / rate-limit visibility summary
| CLI | Remaining plan quota available programmatically? | Where |
|---|---|---|
| Claude Code | Partially | stream-json `rate_limit_event.rate_limit_info{status,resetsAt,utilization}` (when the API sends it; >=2.1.181); ACP bridge forwards it in `usage_update._meta["_claude/rateLimit"]`; `/usage` interactive only (cache of last-known bars up to 60 min). No documented JSON command. |
| Codex | **Yes** | `codex app-server` -> `account/rateLimits/read` and `account/rateLimits/updated` {primary/secondary windows with `usedPercent`, `windowDurationMins`, `resetsAt`; credits; planType}; `account/usage/read`. codex-acp shows them only in `/status` text. |
| Gemini CLI | Internal only | `retrieveUserQuota` buckets {modelId, remainingFraction, remainingAmount, resetTime} used by `/stats`; not exposed headless/ACP. Static daily limits in docs. |
| OpenCode | No | local `opencode stats` (cost/tokens). |
| Goose | No | local accumulated tokens/cost only. |
| Antigravity | Yes (headless) | `agy -p "/usage"` prints quota + reset times (CHANGELOG), `--json` UTC timestamps. |

---------------------------------------------------------------------
## 11. Instruction files and rule/skill discovery (for the generator)

| CLI | Primary instruction file(s) | Also reads | Walk / lookup | Glob-scoped rules | Skills |
|---|---|---|---|---|---|
| Claude Code | `CLAUDE.md` or `.claude/CLAUDE.md`; `CLAUDE.local.md` (gitignored); `~/.claude/CLAUDE.md`; managed `C:\Program Files\ClaudeCode\CLAUDE.md` | `AGENTS.md` (>=2.1.277) ONLY when no CLAUDE.md/CLAUDE.local.md in cwd or above (setting `claude-md-and-agents-md` loads both; a CLAUDE.md that `@AGENTS.md`-imports it is the documented bridge) | cwd + all ancestors at launch (root-first, closest last); subdirs on demand when Read/Write/Edit touches a file there; `@path` imports (code spans skipped); HTML comments stripped; target <200 lines | **Yes**: `.claude/rules/**/*.md` with `paths:` frontmatter (list or comma string), unscoped rules load at launch; `~/.claude/rules/` | `.claude/skills/<n>/SKILL.md` (project, walks up to repo root; nested on demand), `~/.claude/skills`; optional `paths:` frontmatter limits auto-activation |
| Codex | `AGENTS.md`, `AGENTS.override.md`; `~/.codex/AGENTS.md` (UNVERIFIED path) | `project_doc_fallback_filenames` config (default none) | Project root to cwd hierarchy; "More-deeply-nested AGENTS.md files take precedence"; `project_doc_max_bytes` 32768 default | No | `.agents/skills`, `.codex/skills` (strings in binary; docs unreachable) |
| Gemini CLI | `GEMINI.md`; `~/.gemini/GEMINI.md` | `context.fileName` string/array (add `AGENTS.md` there) | global, then workspace dirs and parents, then JIT per touched directory up to trusted root; `@file.md` imports; `/memory show`/`reload` | No | `.gemini/skills/` or `.agents/skills/` (alias wins), `~/.gemini/skills`, `~/.agents/skills` |
| OpenCode | `AGENTS.md`; global `~/.config/opencode/AGENTS.md` | `CLAUDE.md` (only if no AGENTS.md), `~/.claude/CLAUDE.md`; `instructions` in `opencode.json` (globs, files) | walks up from cwd; first match per category wins | Via `instructions` globs (e.g. `.cursor/rules/*.md`), not frontmatter | `.opencode/skills`, `.claude/skills`, `.agents/skills`, plus `~/.config/opencode/skills`, `~/.claude/skills`, `~/.agents/skills`; name regex + dir-name match |
| Goose | `AGENTS.md` then `.goosehints`; global `~/.config/goose/.goosehints` | `CONTEXT_FILE_NAMES` env (e.g. `CLAUDE.md`, `.cursorrules`) | working dir up to repo root at start; nested dirs loaded when accessed (git repos) | No | `.agents/skills/`, `~/.agents/skills/`, `~/.agents/plugins/<p>/`; back-compat `.goose/skills`, `.claude/skills`, `~/.claude/skills` |
| Antigravity | `AGENTS.md`, `GEMINI.md`, `rules/` folder, `skills.json`, `rules.json`, `plugins.json`, `agents.json` in `~/.gemini/config` and `.agents/` dirs (CHANGELOG) | | manifests load from every `.agents/` directory between cwd and project root | `rules.json` manifest (details UNVERIFIED) | `.agents/skills/` |

Generator implications (all VERIFIED-HERE from docs above): write `AGENTS.md` as the canonical file; add a one-line `CLAUDE.md` containing `@AGENTS.md` so Claude Code (which prefers CLAUDE.md) and OpenCode (which falls back to CLAUDE.md only when AGENTS.md is missing, so both fine) see one source; write `GEMINI.md` as `@AGENTS.md`-style import or set `context.fileName`; Goose and Codex read AGENTS.md directly; path-scoped content only maps natively to Claude (`.claude/rules/*.md` with `paths:`) and, approximately, OpenCode (`instructions` globs). Avoid emitting the same text in both CLAUDE.md and AGENTS.md without an import because Claude loads only CLAUDE.md by default while OpenCode/Goose may load both.

---------------------------------------------------------------------
## 12. Final table

| CLI | Transport recommended | ToolCallVisibility | Usage reporting | Resume | Interrupt | MCP | OpenAI-compatible provider | Native Windows | Min supported version (proposal) | Declarative vs Go |
|---|---|---|---|---|---|---|---|---|---|---|
| Claude Code 2.1.293 | ACP via `@agentclientprotocol/claude-agent-acp@0.87.0` primary (tool calls, permissions, resume/fork/list, usage+cost, rate-limit meta); headless `claude -p --input-format/--output-format stream-json` as fallback and for CLI-version-independent behavior | true | tokens incl. cache read/creation + `total_cost_usd` (estimate); `_claude/rateLimit` window info | `--resume <id>`, `--continue`, `--fork-session`; ACP `session/resume`,`fork`,`list` | SIGINT or control `interrupt` (receipt, >=2.1.205); ACP `session/cancel`; SIGTERM leaves turn unfinished | `--mcp-config`+`--strict-mcp-config`, ACP `mcpServers` (stdio/http/sse) | **No** (Anthropic format only; non-Claude via gateway unsupported) | Yes (Git for Windows optional) | CLI >=2.1.205 (interrupt/capabilities), recommend >=2.1.277; bridge pinned 0.87.0 | Declarative: spawn/args, detect (`--version`, `auth status` JSON `loggedIn`), env, instruction files, min version. Go: bridge lifecycle (Node check), `_meta` rate-limit parse, auth via terminal-method, Windows interrupt |
| Codex 0.161.0 | ACP via `@agentclientprotocol/codex-acp@2.1.1`; (richer: direct `codex app-server` JSON-RPC for quota); headless `exec --json` fallback | exec: partial (shell, MCP, file paths, web search); ACP: true | exec: cached/cache-write/reasoning tokens, no $; ACP `usage_update` no cost; quota via app-server | `exec resume`, ACP `session/resume`,`fork`,`list` | ACP cancel -> `turn/interrupt`; exec: UNVERIFIED | `mcp_servers` TOML, `-c`, ACP `mcpServers` (stdio/http, no sse) | **Partial**: `model_providers`, but `wire_api = "responses"` only | Yes (npm win32 pkg + sandbox exes; docs/install.md says WSL2, stale) | CLI >=0.159.1 (bridge dep), tested 0.161.0 | Declarative: spawn, `-c` mappings, detect (`login status` exit 1), instruction files. Go: app-server quota client, Responses-only provider validation, API-key vs ChatGPT auth UX |
| Gemini CLI 0.63.0 | ACP native `gemini --acp`; headless `-p -o stream-json` fallback | true | headless: input/cached/output + per-model, no $; ACP: only `_meta.quota` (no cache split, no usage_update) | `--resume latest\|n\|uuid`; ACP `loadSession` only | ACP cancel; headless UNVERIFIED | `settings.json mcpServers` (stdio/sse/http), `--allowed-mcp-server-names`, ACP `mcpServers` | **No** (Gemini-protocol base URL only) | Yes (Node >=20, Windows 11 24H2 recommended) | 0.63.0; require API-key/Vertex/enterprise auth (consumer OAuth retired 2026-06-18, SNIPPET-ONLY) | Declarative: spawn `gemini --acp`, env (`GEMINI_API_KEY`), instruction names. Go: capability-gated adapter (no resume/usage), `_meta.quota` parser, trust-folder handling |
| OpenCode 1.18.35 | ACP native `opencode acp` (headless `run` cannot ask permissions and is lossy) | true (ACP full; run: completed-only) | ACP `usage_update` + cost + cache; run: `step_finish` tokens/cost | ACP `resume/fork/list/close`; `run -c/-s/--fork` | ACP cancel; run: UNVERIFIED | `opencode.json mcp`, ACP `mcpServers`, `OPENCODE_CONFIG_CONTENT` | **Yes** (`@ai-sdk/openai-compatible` + `baseURL`; `@ai-sdk/openai` for responses) | Yes (binary), docs recommend WSL | 1.18.35 | Declarative: spawn `opencode acp`, provider JSON template, instruction names. Go: inline provider config injection, Windows path/ConPTY testing |
| Goose 1.53.0 | ACP native `goose acp`; headless `goose run --output-format stream-json` fallback | ACP true; run likely true (shape UNVERIFIED) | `complete` event + ACP `usage_update` with cost; cache read/write in headless | `run -r -n/--session-id`; ACP `loadSession`/list/delete/close only | ACP cancel; run UNVERIFIED | `extensions:`/`--with-extension`/`--with-streamable-http-extension`, ACP `mcpServers` (stdio/http, no sse) | **Yes** (OpenAI host/base path, OpenRouter, Ollama, LM Studio, custom JSON providers) | Yes (zip `goose-package\goose.exe`) | 1.53.0 | Declarative: spawn `goose acp`, `GOOSE_PROVIDER/MODEL/MODE` env, instruction names (`AGENTS.md`,`.goosehints`). Go: provider setup via env/config, no-resume handling |
| Antigravity 1.3.1 (agy) | ACP via registry `antigravity-acp` 1.3.0 binary (first-party), else `agy -p --input/--output-format stream-json` | headless step_update (tool details reportedly thin, issue #17 snippet); ACP UNVERIFIED | token usage in step_update; `-p "/usage"` quota | stateless by default; resume flags UNVERIFIED | print-mode Ctrl+C non-zero (changelog); ACP UNVERIFIED | `mcp_config.json`, plugins | UNVERIFIED (offers Claude/GPT-OSS models itself) | Yes (install.ps1, Windows sandbox) | >=1.3.0 (ACP server), but gate on legal answer | Declarative: spawn registry binary. Go: unverified protocol quirks; ToS risk |
| Claude Design | N/A (not an agent CLI) | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A | Out of scope: product surface (`claude.ai/design`, `/design` in Claude Code) |

What can be a declarative manifest (all of it is data the generic ACP adapter needs):
- id/name, binary names per OS, install hints (commands above), version probe (`<bin> --version`) and regex, min/recommended versions, npm package + exact version for bridges, spawn argv for ACP (`gemini --acp`, `opencode acp`, `goose acp`, `npx -y @agentclientprotocol/claude-agent-acp@0.87.0`, `npx -y @agentclientprotocol/codex-acp@2.1.1`), env injection (API keys, `CLAUDE_CODE_EXECUTABLE`, `CODEX_PATH`, `CODEX_HOME`, `GEMINI_CLI_HOME`, `OPENCODE_CONFIG_CONTENT`, `GOOSE_*`), auth probe command + success criterion (`claude auth status` -> `.loggedIn`; `codex login status` exit code; others: none), which ACP `authMethods` map to a terminal login command, instruction-file names/import templates, skills dirs, model/effort mapping to ACP config options vs flags, headless fallback argv templates and which event dialect parser to use, ToS note and consent gate flag.

What needs Go code (cannot be pure data):
1. The ACP client (use `coder/acp-go-sdk` v0.13.5 or equivalent) with capability gating from `initialize` (resume/fork/list/usage differ per agent), stdio framing, Windows process lifecycle (Job Objects, `CTRL_BREAK_EVENT`, tree kill), `.cmd` shim resolution for `npx`/`npm`/`gemini`.
2. Permission UX mapping for `session/request_permission` option kinds and the headless fallbacks (`--permission-prompt-tool` for Claude).
3. Usage/quota normalizers: ACP `usage_update`, prompt `usage`, `_meta["_claude/rateLimit"]`, Gemini `_meta.quota`, Codex app-server `account/rateLimits/read` side-channel, `agy -p "/usage"`.
4. Five headless parsers if fallbacks are kept: Claude SDKMessage, Codex `ThreadEvent`, Gemini `JsonStreamEvent`, OpenCode `{type,part}`, Goose `StreamEvent`.
5. Auth orchestration that never touches tokens: run the CLI's own login in a user-visible terminal/PTY, then re-probe.
6. Node prerequisite detection/installation guidance for the Claude/Codex bridges and Gemini.
7. Provider-config writers with validation (Codex responses-only; OpenCode provider JSON; Goose custom provider JSON).

---------------------------------------------------------------------
## 13. Consolidated verified-here vs needs-Windows / needs-login

VERIFIED-HERE (this session)
- Versions and licenses: Claude Code 2.1.293 (ran), Codex 0.161.0 (ran), Gemini 0.63.0 (ran), OpenCode 1.18.35 (ran), Goose 1.53.0 (ran via npm binary), claude-agent-acp 0.87.0, codex-acp 2.1.1, ACP SDK 1.7.0, Go SDK 0.13.5.
- Live ACP `initialize` against: gemini --acp, opencode acp, goose acp, claude-agent-acp, codex-acp (script `acp_test/init.py`, `acp_test/raw.py`; run with a scrubbed env: `env` limited to PATH/HOME/XDG/CODEX_HOME).
- `claude auth status` JSON; `codex login status` exit 1; `codex app-server generate-json-schema`.
- Source/schema reads: exec_events.rs, config.schema.json, gemini types.ts, opencode run.ts/acp service/types.gen.ts, goose cli.rs/session/mod.rs/acp server.rs, bridges' dist JS.
- Docs reads: code.claude.com (16 pages), Gemini/OpenCode/Goose repo docs, Anthropic legal pages.

NEEDS-LOGIN (could not do; would need an authenticated account and model calls)
- Real stream captures for all five (stream-json, exec --json, run --format json, goose stream-json), cost/cached-token numbers, rate-limit event shape, interrupt behavior mid-tool, resume round-trip, permission prompts over ACP.

NEEDS-WINDOWS
- All installers (install.ps1/cmd, winget, scoop, choco, npm shims), PATH layout, credential file locations/ACLs (Claude `%USERPROFILE%\.claude\.credentials.json`; Codex `auth.json`/keyring; Gemini `oauth_creds.json`; OpenCode `auth.json`; Goose keyring), Ctrl-C/Ctrl-Break delivery to children, Git Bash dependencies (Claude optional, Goose), Codex Windows sandbox, `npx.cmd` spawning of bridges, long-path/UNC behavior (Claude docs: UNC symlink targets for instructions are not followed).

UNVERIFIED (reason)
- Codex: `~/.codex/AGENTS.md` details, exec interrupt, developers.openai.com policy (site unreachable). Gemini: auth-state detection, headless interrupt. OpenCode: `run` interrupt, Windows data path. Goose: headless interrupt, `Message` JSON shape. Antigravity: all behavior beyond README/CHANGELOG (site and binaries unreachable). Anthropic support-article quotes (WebFetch paraphrase).
- Registry license mismatch for claude-acp ("proprietary" vs Apache-2.0 LICENSE): cause not explained in any file I read.

Process note: an early diagnostic command accidentally printed the sandbox environment into the tool output (not into this report). Treat sandbox-injected tokens as exposed; nothing from it was used or recorded here.
