# Drydock: Connections (agent CLIs and model providers)

Scope: this document specifies the Connections feature (brief section 5, "Connections (productized adapters)", and section 21, "Connections page first for new users"). A Connection is the user-visible, persisted state of one external thing Drydock depends on: an agent CLI (Claude Code, Codex, Gemini CLI, OpenCode, Goose, Antigravity, a custom CLI), a model provider (OpenRouter, Google AI Studio, direct Anthropic/OpenAI, local or LAN hosts), or a prerequisite tool (Node, git, `gh`). It covers detection, install guidance (never a download), authentication through the CLI's own login in the embedded terminal, verification ("Test connection" as the short form of the conformance suite), the status model, the declarative adapter manifest and `compat.yaml`, drift detection, the headless contract, the manifest-versus-Go boundary, the capability matrix and how ACP `agentCapabilities` fill it, Node detection (D9), terms notices, and the instruction-file generator mapping. Owner decisions in [`decisions.md`](decisions.md) win over the brief; the Linux-only evidence limits are in [`phase0.md`](phase0.md) section 1. Evidence tags: `[cli §n]` = [`phase0/agent-clis.md`](phase0/agent-clis.md), `[prov §X]` = [`phase0/model-providers.md`](phase0/model-providers.md), `[go §n]` = [`phase0/go-platform.md`](phase0/go-platform.md), `[tool §n]` = [`phase0/tooling-integrations.md`](phase0/tooling-integrations.md), `Dn` = decisions.md. Anything not backed by one of these is labelled **design** (our choice) or **unverified**. Integrations (Linear, Notion, Sentry, Aikido, GitHub) reuse this model and are specified in [`integrations.md`](integrations.md) and [`github.md`](github.md).

## 1. Connection model

| Field | Meaning |
|---|---|
| `id`, `kind` | `agent_cli`, `model_provider`, `tool`, `integration`, `mcp_server` (the last two live in integrations.md and mcp.md). |
| `status` | One of `not_installed`, `unsupported_version`, `unauthenticated`, `ready`, `degraded` (brief section 5). Computed, never typed in by the user. |
| `reason` | Machine code plus human text, e.g. `prereq_missing:node`, `version_below_min`, `auth_probe_false`, `test_failed:handshake`, `version_untested`, `transport_fallback`. |
| `gates[]` | Unmet acknowledgements (terms notices, section 11). **Design:** the brief's status enum has no "gated" value, so a gate does not change `status`; `usable = status == ready && all gates acked`. The UI shows "Acknowledge terms to enable". |
| `detected`, `capabilities`, `last_test` | Resolved binary, version, run mode (`native` or `wsl`), Node; the capability matrix (section 6), each cell `{value, provenance: observed\|declared\|unknown}`; the latest short-form report (section 5). |

Detection and tests run only on explicit triggers: app start (cheap probes only), Connections page open, "Re-check", "Test connection", and a version change. Nothing in this feature uploads source code; the only network calls are provider key validation and the user-pressed Test connection.

## 2. Detection

Pipeline per agent CLI (all steps through the Process Manager, argv arrays only, never a shell string, stdin closed, 10 s timeout, run inside a throwaway job):

1. **Resolve the binary.** Manifest `binaries` lists names in priority order; resolve `.exe`, then `.cmd`/`.bat` shims (`npx.cmd`, `gemini.cmd`, `codex.cmd`) through `PATH` and `PATHEXT` [cli §12, go §9]. A `.cmd` shim is launched via `cmd.exe /d /s /c` with escaping done in one tested Go function (argument escaping for `.cmd` is an open item [go]). Known install locations (e.g. `claude.exe` under `%USERPROFILE%\.local\bin`) are **unverified** [cli §1] and only used as secondary search hints.
2. **Version probe.** `<bin> --version` and the manifest regex. Recorded verbatim: `claude --version` prints `2.1.293 (Claude Code)`, `codex --version` prints `codex-cli 0.161.0`, `goose --version` prints ` 1.53.0` [cli §1, §2, §5]. The regex captures the first semver.
3. **Prerequisites** (`requires: [{tool: node, min: "22"}]`, section 10), then **compare** with `compat.yaml` (section 8): below `min` is `unsupported_version`; above the newest `inspected`/`verified` version is allowed but `degraded` with `reason=version_untested` until a Test passes on it. The auth probe (section 4) runs only when these pass.
4. **Run mode.** `native` is the default. `wsl` is a manifest-declared alternative (OpenCode's docs recommend WSL [cli §4]); a WSL install has its own credential store and its own `gh`/CLI state, so each run mode is a separate Connection instance. Behavior under WSL is **unverified** (phase0 section 7, item 7).

**Install guidance, never a download.** Manifest `install[]` entries are display text with a "Copy" and an "Open in terminal" button; the user presses Enter themselves. Drydock never runs an installer, never fetches a binary, never edits `PATH`. The guidance text for each CLI is in its manifest (section 7). Windows install commands are from [cli §1-§5, §8]; none were executed (NEEDS-WINDOWS).

## 3. Status evaluation

Evaluation order (first match wins):

| # | Condition | Status | Notes |
|---|---|---|---|
| 1 | No binary resolved | `not_installed` | Guidance shown. |
| 2 | A prerequisite required by *every* transport is missing | `not_installed` | `reason=prereq_missing:node`. |
| 3 | Version below manifest `min` | `unsupported_version` | Shows min and recommended. |
| 4 | Auth probe says logged out; or there is no probe (Gemini, Goose) and the last Test failed with an auth error. Until the first Test such CLIs show `ready` with auth provenance `unknown` | `unauthenticated` | "Sign in" opens the embedded terminal. |
| 5 | Last Test failed; or a required capability was not observed; or only a fallback transport works (e.g. Codex without Node uses `exec --json`); or version untested; or quota read failing | `degraded` | Agent usable only if the router's required capabilities still hold. |
| 6 | Otherwise | `ready` | |

## 4. Authentication

Principle (D1, brief section 5): Drydock never reads, copies, refreshes, transmits or stores a CLI's subscription credential. Login happens by running the CLI's own login command in the embedded ConPTY terminal (go-pty, [go]); on exit Drydock re-runs the probe. The credential locations below are listed so tests and the support bundle can **exclude** them, not so code can open them.

| CLI | Login command (embedded terminal) | Non-interactive probe | Credential store (never touched) |
|---|---|---|---|
| Claude Code | `claude auth login [--claudeai\|--console\|--sso\|--email]` | `claude auth status --json`, ready when `.loggedIn == true`. The logged-out exit code is **unverified**: parse the JSON, treat missing JSON as unknown, then fall through to Test. | `%USERPROFILE%\.claude\.credentials.json`; `CLAUDE_CONFIG_DIR` relocates [cli §1] |
| Codex | `codex login` (browser) or `codex login --device-auth` | `codex login status`: logged out prints `Not logged in`, exit 1 (observed); logged-in text unverified | `$CODEX_HOME\auth.json`, or keyring per `cli_auth_credentials_store` [cli §2] |
| Gemini CLI | `gemini` interactive, or API key / Vertex via env (`GEMINI_API_KEY`, ADC, `GOOGLE_APPLICATION_CREDENTIALS`) | none (no `gemini auth status`); ACP `authMethods` lists `oauth-personal, gemini-api-key, vertex-ai, gateway` [cli §3] | `~/.gemini/oauth_creds.json` (`GEMINI_CLI_HOME`), presumed Windows path unverified |
| OpenCode | `opencode providers login [url]` (alias `auth`) | `opencode providers list` (non-interactive; output needs parsing) | `~/.local/share/opencode/auth.json` (Windows mapping unverified) [cli §4] |
| Goose | `goose configure` (provider keys; some providers browser OAuth) | none; `goose doctor` exists but its output is unverified | OS keyring or `secrets.yaml` [cli §5] |
| Antigravity | start `agy` interactively: it "authenticates via the system keyring, falling back to Google Sign-In"; `/logout` signs out | candidate: `agy -p "/usage"` (non-interactive, prints quota); unverified as an auth probe | system keyring [cli §8] |

Claude-specific rules (D1): never run `claude` with `--bare` (it disables OAuth and keychain use, so it breaks subscription users [cli §1]); warn on the Connection when `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` is set in the environment, because the API key switches Claude Code to API billing (support-article paraphrase, **unverified verbatim** [cli §9.1]); never inject either variable ourselves unless the user chose an "API key" Claude Connection.

**API-key providers** (OpenRouter, Google AI Studio, direct Anthropic and OpenAI, hosts): the user pastes a key; Drydock validates it and stores it with `go-keyring` (values over 2560 bytes go encrypted into SQLite with the key in Credential Manager [go §3]). Validation by provider:

| Provider | Validation call | Gotchas |
|---|---|---|
| OpenRouter | `GET /api/v1/key` (limit, usage, `is_free_tier`, `free_model_daily_requests`) then `GET /api/v1/models/user` (effective filtered catalog, D4) | `/key` has no privacy fields; account privacy toggles are not API-readable, so show a first-run checklist and always send `provider:{data_collection:"deny", zdr:true}` on `private` routes [prov §A]. |
| Google AI Studio | `POST .../v1beta/openai/chat/completions` with a 1-token probe; `GET .../openai/models` with a key is **unverified** | An invalid key returns **HTTP 400**, not 401 [prov §C]. `private` only if the user attests a billing-linked key. |
| Anthropic / OpenAI direct | `GET /v1/models` | OpenAI terms evidence is secondary; re-verify before shipping [prov §D]. |
| vLLM / SGLang / Ollama | `GET /health` + `GET /v1/models`; Ollama `GET /api/version`, `/api/tags`, `/api/ps` | vLLM `--api-key` leaves non-`/v1` routes open; Ollama has no inbound auth or TLS and defaults to 4k context below 24 GiB VRAM (warn unless `num_ctx` is at least 64000) [prov §E]. |

Brief section 16 applies to every host: no TLS only on loopback or private addresses; public hosts require verified HTTPS (self-signed pinned by fingerprint). Provider data class and tier (`private|trainable`, `premium|cheap|free`) are Connection fields set at registration; D13 maps the report's `public` to `trainable`. The Codex provider-config writer rejects any provider that does not expose the Responses API (`wire_api = "responses"` is the only accepted value, D3), and the Test proves it against the real endpoint.

## 5. Test connection (the conformance short form)

"Test connection" runs the first steps of the per-adapter conformance suite (`internal/agents/conformance`) against the real install, on the user's button press, in an empty temp directory, with a hard token and time cap shown before it runs. Per P3, if the CLI is not installed or not logged in, the step is recorded as **skipped with a reason**, never faked.

| Step | Check | Output |
|---|---|---|
| T1 | Binary, version, prereqs, then the auth probe (section 4) | `detected`, auth provenance |
| T2 | Spawn through the Process Manager into a per-agent Job Object (pipes, env, cwd), then the transport handshake: ACP `initialize` (store `agentCapabilities`, `authMethods`) or the headless init event (Claude `system/init` with `capabilities[]`, Gemini `init`, Codex `thread.started`) | raw capability evidence |
| T3 | One trivial turn ("reply with the single word OK") on the cheapest model the adapter can select | assistant text seen; `usage` present/absent; cost present/absent; tool-call events present/absent (recorded, not assumed) |
| T4 | Cancel a second trivial turn mid-stream (ACP `session/cancel`, Claude control `interrupt`) | interrupt observed/absent; stopReason |
| T5 | Tree kill: have the agent run a command that spawns a grandchild, kill the Job, verify no survivor | proves D7 for this CLI on this machine (Windows only) |
| T6 | Gateway reachability: attach the per-run MCP endpoint and call the Workspace `ping`-class tool through the CLI | MCP transport that actually works (stdio vs HTTP, [`mcp.md`](mcp.md)) |
| T7 | Provider path (only for cheap-tier agents): the configured provider answers one request with tool calling | proves the Responses-only rule for Codex, OpenAI-compatible hosts for OpenCode and Goose |

The full suite adds: resume round trip, permission request answered and denied, malformed-output injection (labelled mock, failure boundary only), long-output truncation, rate-limit event parsing, and context-pressure handoff seeding. Cell values are written back to the capability matrix with provenance `observed`. The report (`ConformanceReport{Adapter, CLIVersion, BridgeVersion, Platform, Form: short|full, Steps[]{ID, Status: pass|fail|skipped, Reason, EvidenceRef}, Observed map[cap]Cap, TokensUsed}`) is stored as a row with raw output on disk; `TokensUsed` is measured, never silently estimated (brief section 7).
## 6. Capability matrix

Brief section 5 capabilities (decide on these, never on provider names outside adapters, D10). Seed values are **declared** from [cli §12] and become `observed` after a Test. "ACP" = value populated from `initialize`/`session/new` when that transport is used.

| Capability | Claude Code | Codex | Gemini CLI | OpenCode | Goose | Antigravity |
|---|---|---|---|---|---|---|
| headless / streaming | yes (`--include-partial-messages`) | yes (`exec --json`) | yes (`-p -o stream-json`, `delta`) | ACP only (D11) | yes (`run --output-format stream-json`) | yes (`-p`, `text_delta`) |
| model selection | `--model` | `-m`; ACP config option | `-m` | `provider/model` | `--model`, `GOOSE_MODEL` | unknown |
| effort control | `--effort low..max` | `-c model_reasoning_effort` | no (settings `modelConfigs`) | `--variant` / ACP config | no unified flag | `--effort` |
| session resume | `--resume`, `--continue` | `exec resume`; ACP `resume` | headless `--resume`; ACP `loadSession` only | ACP `resume/fork/list/close` | headless `-r`; ACP `loadSession` only | unknown |
| interrupt | control `interrupt` (>=2.1.205) | ACP cancel; `exec` unverified | ACP cancel; headless unverified | ACP cancel | ACP cancel; headless unverified | unverified (print-mode Ctrl+C exits non-zero) |
| MCP transports | stdio, http, sse | stdio, http | stdio, http, sse | stdio/local, remote | stdio, http (no sse) | `mcp_config.json`; scoping unverified |
| token usage | result `usage`, `modelUsage` | `turn.completed.usage` | `result.stats` (ACP: `_meta.quota` only) | `step_finish`; ACP `usage_update` | `complete`; ACP `usage_update` | `step_update` tokens |
| cost reporting | `total_cost_usd` (client estimate) | no dollars | no dollars | yes | yes (`cost_usd`) | unknown |
| permission modes | `--permission-mode`, `--permission-prompt-tool` | `-s` sandbox + approval policy | `--approval-mode` | ACP permission requests | `GOOSE_MODE` | `--mode`; unanswered requests soft-denied |
| image input | unknown (ACP bridge: yes) | ACP yes | ACP yes | ACP yes | ACP yes | unknown |
| tool-call visibility | yes | `exec` partial, ACP yes | yes | ACP yes, `run` completed-only | ACP yes | headless thin (issue snippet) |

**How ACP `agentCapabilities` populate cells (D10).** The generic ACP adapter reads `initialize` once per session and never assumes by provider name:

| ACP field | Matrix cell |
|---|---|
| `loadSession: true` | `session_resume.load = true` |
| `sessionCapabilities.resume` / `fork` / `list` / `close` / `delete` | `session_resume.resume`, `.fork`, `.list`, `.close`, `.delete` (OpenCode: close, fork, list, resume; Goose: list, delete, close; Gemini: none [cli §7.1]) |
| `promptCapabilities.image` / `audio` / `embeddedContext` | `image_input`, `audio_input`, `embedded_context` |
| `mcpCapabilities.http` / `.sse` / `.acp` | `mcp.transports` (Goose and Codex: `sse:false`) |
| `authMethods` | `auth.methods`; a client must advertise `clientCapabilities.auth.terminal` (or `_meta["terminal-auth"]`) to receive Claude bridge login methods [cli §7.2 notes] |
| `agentInfo.name/version` | the bridge/agent version recorded in `detected` |
| baseline `session/cancel`, `session/request_permission`, `tool_call` updates | `interrupt`, `permission_prompt`, `tool_call_visibility` = observed true for all five in source [cli §7.2]; confirmed only by the T4 cancel step |
| first `usage_update{used,size,cost}` or prompt `usage` | `token_usage` (and `cost_reporting` when `cost` is present). Absent for Gemini; Codex lacks cost [cli §7.2] |
| `session/new` response (modes, config options) | `model_selection`, `effort_control`, `permission_modes`; field shapes are **unverified** until probed live |

Merge rule: `effective(cap) = observed if present, else declared if the manifest declares it and ACP does not contradict it, else unknown`. The router and the watchdog only read `effective`; `unknown` is treated as absent for gating. Watchdog needs `tool_call_visibility`; without it `ToolCallVisibility=false` and reduced detection runs (brief section 7).

## 7. Adapter manifest schema and examples

Location: `internal/agents/adapters/manifests/<id>.yaml` (embedded; user custom CLIs in `<app data>/adapters/custom/<id>.yaml`). Validated at load by a Go struct; unknown keys are errors. Argv templates are arrays; `{{var}}` placeholders are typed (`model`, `effort`, `session_id`, `cwd`, `mcp.config_path`) and substituted without shell interpretation. Top-level keys: `meta{id, display_name, kind, license, tier, run_modes}, binaries, install[], requires[], detect, versions, auth, transports{primary, acp, headless}, provider_config, mcp, usage, capabilities_declared, instructions, terms, gate`. `{a: b}` flow style is used for density; documents are separated by `---`.

```yaml
# claude.yaml (D1: headless primary; ACP bridge present but disabled)
meta: {schema: 1, id: claude, display_name: Claude Code, kind: agent_cli, license: proprietary, tier: premium, run_modes: [native, wsl]}   # [cli §1]
binaries: {native: [claude.exe, claude.cmd], wsl: [claude]}
install:                                             # display only; Drydock never executes these
  - {shell: powershell, command: 'irm https://claude.ai/install.ps1 | iex'}
  - {shell: cmd, command: 'curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd'}
  - {shell: cmd, command: 'winget install Anthropic.ClaudeCode', note: no auto-update}
  - {shell: cmd, command: 'npm install -g @anthropic-ai/claude-code', requires: [{tool: node, min: "22"}]}
detect: {version: {argv: [--version], regex: '^(\d+\.\d+\.\d+)'}}
versions: {min: "2.1.205", recommended: "2.1.277"}    # interrupt receipts / AGENTS.md fallback [cli §12]
auth: {login: {argv: [auth, login], variants: [[--claudeai], [--console], [--sso], [--email]], terminal: true}, probe: {argv: [auth, status, --json], parse: json, ready_when: '$.loggedIn == true'}, warn_env: [ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN], forbidden_flags: [--bare]}
transports:
  primary: headless
  headless:
    argv: [-p, --input-format, stream-json, --output-format, stream-json, --include-partial-messages, --permission-prompt-tool, "{{mcp.permission_tool}}", --mcp-config, "{{mcp.config_path}}", --strict-mcp-config]
    optional: {model: [--model, "{{model}}"], effort: [--effort, "{{effort}}"], resume: [--resume, "{{session_id}}"], permission_mode: [--permission-mode, "{{mode}}"]}
    prompt: stdin_stream_json
    parser: claude_sdkmessage                        # Go
    interrupt: {via: stdin_control, message: interrupt, needs: system_init.capabilities.interrupt_receipt_v1}
    probe_unknown: [verbose_flag_required]           # unverified [cli §1]
  acp: {enabled: false, argv: [npx, -y, "@agentclientprotocol/claude-agent-acp@0.87.0"], env: {CLAUDE_CODE_EXECUTABLE: "{{resolved_binary}}"}, requires: [{tool: node, min: "22"}], enable_when: written_confirmation_recorded}
mcp: {inject: config_file, transports: [stdio, http], per_session: true, http_headers: unverified}
usage: {normalizer: claude_result_modelusage, quota: {source: stream, normalizer: claude_rate_limit_event}}
capabilities_declared: {streaming: true, structured_output: true, model_selection: true, effort_control: true, session_resume: true, interrupt: true, mcp: true, token_usage: true, cost_reporting: estimate, headless: true, permission_modes: true, image_input: unknown, tool_call_visibility: true}
instructions: {primary: CLAUDE.md, template: "@AGENTS.md", rules_dir: .claude/rules, rules_scope_key: paths, skills_dir: .claude/skills}
terms: {notice: claude-own-login-v1}
---
meta: {id: codex, license: Apache-2.0, tier: premium}   # cheap tier only on a Responses-API provider (D3)
binaries: {native: [codex.exe, codex.cmd]}
install: [{shell: powershell, command: 'powershell -ExecutionPolicy ByPass -c "irm https://chatgpt.com/codex/install.ps1 | iex"'}, {shell: cmd, command: 'npm install -g @openai/codex', requires: [{tool: node, min: "22"}]}]
detect: {version: {argv: [--version], regex: 'codex-cli (\d+\.\d+\.\d+)'}}
versions: {min: "0.159.1", recommended: "0.161.0"}    # bridge dependency floor [cli §12]
auth: {login: {argv: [login], variants: [[--device-auth]], terminal: true}, probe: {argv: [login, status], parse: exit_code, ready_when: 'exit == 0', not_ready_when: 'exit == 1'}}
transports:
  primary: acp
  acp: {argv: [npx, -y, "@agentclientprotocol/codex-acp@2.1.1"], requires: [{tool: node, min: "22"}]}
  headless: {argv: [exec, --json, -C, "{{cwd}}", -s, workspace-write, "-"], optional: {model: [-m, "{{model}}"], effort: [-c, 'model_reasoning_effort="{{effort}}"']}, prompt: stdin, parser: codex_threadevent, interrupt: unverified}
usage: {normalizer: codex_turn_usage, quota: {source: side_channel, argv: [app-server, --listen, "stdio://"], method: account/rateLimits/read}}
provider_config: {writer: codex_toml, file: .codex/config.toml, rule: wire_api_responses_only}   # D3
mcp: {inject: acp_session_new, transports: [stdio, http], fallback: {flag: "-c", key: mcp_servers.drydock, bearer_env: true}}
---
meta: {id: gemini, license: Apache-2.0, tier: premium}
binaries: {native: [gemini.cmd, gemini.exe]}
install: [{shell: cmd, command: 'npm install -g @google/gemini-cli', requires: [{tool: node, min: "22"}]}]   # CLI needs >=20; D9 sets 22
detect: {version: {argv: [--version], regex: '^(\d+\.\d+\.\d+)'}}
versions: {min: "0.63.0", recommended: "0.63.0"}
auth: {allowed_kinds: [gemini-api-key, vertex-ai], blocked_kinds: [oauth-personal], env_from_keyring: {GEMINI_API_KEY: "keyring:gemini-api-key"}, probe: none}   # D2: consumer sign-in reported retired 2026-06-18
transports:
  primary: acp
  acp: {argv: [gemini, --acp], env: {GEMINI_CLI_TRUST_WORKSPACE: "true"}}
  headless: {argv: [-p, -o, stream-json, --approval-mode, default, --skip-trust], optional: {model: [-m, "{{model}}"], resume: [--resume, "{{session_id}}"]}, parser: gemini_stream_json, interrupt: unverified}
usage: {normalizer: gemini_result_stats, acp_normalizer: gemini_meta_quota, quota: static_config}
---
meta: {id: opencode, license: MIT, tier: cheap}      # repo anomalyco/opencode [cli §4]; D3 default cheap-tier agent
binaries: {native: [opencode.exe, opencode.cmd], wsl: [opencode]}
install: [{shell: cmd, command: 'scoop install opencode'}, {shell: cmd, command: 'choco install opencode'}, {shell: cmd, command: 'npm i -g opencode-ai@latest'}]
detect: {version: {argv: [--version], regex: '^(\d+\.\d+\.\d+)'}}
versions: {min: "1.18.35", recommended: "1.18.35"}
auth: {login: {argv: [providers, login], terminal: true}, probe: {argv: [providers, list], parse: text_providers}}
transports: {primary: acp, acp: {argv: [opencode, acp]}, headless: none}      # D11: `run` cannot answer permissions, drops deltas
provider_config: {writer: opencode_inline_json, env: OPENCODE_CONFIG_CONTENT, template: {provider: {"<id>": {npm: "@ai-sdk/openai-compatible", name: "<name>", options: {baseURL: "<url>"}, models: {}}}}}
---
meta: {id: goose, license: Apache-2.0, tier: cheap}   # aaif-goose/goose (the agent, not the migrations tool)
binaries: {native: [goose.exe]}
install: [{shell: powershell, command: 'download_cli.ps1 from the aaif-goose/goose repository (exact URL not recorded)'}, {shell: cmd, command: 'npm install -g @aaif/goose-acp', requires: [{tool: node, min: "22"}]}]
detect: {version: {argv: [--version], regex: '(\d+\.\d+\.\d+)'}}
versions: {min: "1.53.0", recommended: "1.53.0"}
auth: {login: {argv: [configure], terminal: true}, probe: none}
transports:
  primary: acp
  acp: {argv: [goose, acp], env: {GOOSE_MODE: approve}}            # auto|approve|smart_approve|chat
  headless: {argv: [run, --output-format, stream-json, --no-profile], optional: {model: [--model, "{{model}}"], resume: [-r, --session-id, "{{session_id}}"]}, parser: goose_streamevent, interrupt: unverified}
provider_config: {writer: goose_env, env: [GOOSE_PROVIDER, GOOSE_MODEL, OPENAI_HOST, OPENAI_BASE_PATH, OPENROUTER_API_KEY, OPENROUTER_HOST, OLLAMA_HOST]}
---
meta: {id: antigravity, license: proprietary, tier: premium}   # D2: official `agy` only, behind an acknowledgement gate
binaries: {native: [agy.exe]}
install: [{shell: powershell, command: 'irm https://antigravity.google/cli/install.ps1 | iex'}, {shell: cmd, command: 'curl ... install.cmd (see antigravity.google/cli)'}]
detect: {version: {argv: [--version], regex: '(\d+\.\d+\.\d+)'}}                 # flag unverified
versions: {min: "1.3.0", recommended: "1.3.1"}
auth: {login: {argv: [], terminal: true, note: "interactive agy; Google Sign-In if no keyring session"}, probe: {argv: [-p, /usage], unverified: true}}
gate: {id: antigravity-third-party-v1, required: true}                           # section 11
transports:
  primary: headless
  headless: {argv: [-p, --input-format, stream-json, --output-format, stream-json, --mode, "{{conservative_mode}}"], optional: {effort: [--effort, "{{effort}}"]}, parser: agy_stream_json, denied_actions: surface_to_human}   # --mode values unverified; exit 3 + AGY_ERROR on stderr
  acp: {enabled: false, note: "registry antigravity-acp 1.3.0 (agy_acp_server.exe), evaluated later (D2)"}
usage: {quota: {argv: [-p, /usage, --json], parse: json_utc}}
---
meta: {id: my-cli, kind: agent_cli, custom: true}   # user-authored, headless contract (section 9); custom forces a "runs arbitrary code" acknowledgement
binaries: {native: ['C:\tools\my-cli.exe']}
detect: {version: {argv: [--version], regex: '(\d+\.\d+\.\d+)'}}
transports:
  primary: headless
  headless: {argv: [run, --json, --cwd, "{{cwd}}"], prompt: {via: stdin}, exit_codes: {0: success, 1: failure}, dialect: {format: jsonl, type_field: type, map: {assistant_text: {type: message, text: $.text}, tool_call: {type: tool, name: $.tool, args: $.input}, tool_result: {type: tool_result, id: $.id, ok: $.ok}, usage: {type: usage, input: $.in, output: $.out}, result: {type: done, status: $.status}}}}
capabilities_declared: {model_selection: unknown, effort_control: unknown, session_resume: unknown, interrupt: unknown}
```

A custom dialect can claim `tool_call_visibility` only if both `tool_call` and `tool_result` are mapped; unmapped means `unknown`. Custom manifests cannot name Go parsers, cannot unset a gate, and cannot extend env inheritance beyond a declared list. Per-CLI MCP fallbacks (`settings.json`, `OPENCODE_CONFIG_CONTENT`, `--with-extension`) are in [`mcp.md`](mcp.md); instruction files in section 14.

## 8. `compat.yaml` and version handling

`internal/agents/adapters/compat.yaml` is shipped with the app and updated with releases. It records what was actually run, with the evidence class, so the UI never says "verified" for something only inspected.

```yaml
schema: 1
adapters:
  claude: {min: "2.1.205", recommended: "2.1.277", entries: [{version: "2.1.293", class: inspected, date: 2026-10-08, platform: linux, method: "--help, auth status, docs", windows_conformance: pending}]}
  codex:  {min: "0.159.1", entries: [{version: "0.161.0", class: inspected, date: 2026-10-08, bridge: "@agentclientprotocol/codex-acp@2.1.1"}]}
  gemini: {min: "0.63.0"}  # opencode 1.18.35 and goose 1.53.0 likewise: class inspected, method acp initialize, 2026-10-08
  antigravity: {min: "1.3.0", entries: [{version: "1.3.1", class: docs_only, date: 2026-10-08}]}
node: {min: "22"}
blocked: []
```

Classes: `docs_only` < `inspected` (Linux, no login) < `verified_short` (Windows Test passed) < `verified_full` (Windows full suite). Until Actions or a Windows host runs the suite (P2), no entry may exceed `inspected`. Bridge packages are pinned exactly (D9); `@zed-industries/*-acp` packages are deprecated and must not be used [cli §0, §7].

## 9. The headless contract

A CLI is adapter-eligible if it provides (brief section 5): (a) a non-interactive prompt-and-exit mode; (b) a machine-readable output stream carrying text, tool calls and a final result; (c) an exit code; (d) working-directory control (we set cwd in the Process Manager, so a flag is optional). Optional and recorded as `present|absent|unknown`: model, effort, resume, usage, permission prompts. Normalized events every parser emits (Go, one parser per dialect: Claude SDKMessage, Codex ThreadEvent, Gemini JsonStreamEvent, OpenCode `{type,part}`, Goose StreamEvent, Antigravity `init/step_update/result` [cli §12]):

`session` (id, model), `message_delta`, `message`, `tool_call{id,name,args}`, `tool_result{id,status,output}`, `usage{input,output,cached_read,cached_write,reasoning,cost?,source}`, `rate_limit{status,resets_at,utilization}`, `permission_request{id,action}`, `result{status,error?}`, `error`. Parsers never panic on unknown event types: they count them (`adapter_unknown_events`), which feeds drift detection. Token fields are normalized once (D12); `cost` carries `source: reported|estimated`.

## 10. Node prerequisite (D9)

Node 22 or newer is a documented prerequisite for the ACP bridges, Gemini CLI, Wrangler and Playwright (D9). Evidence minimums are lower for some (Gemini CLI engine >=20, Playwright core >=20, `@aikidosec/mcp` >=18.19 [cli §3, tool §2, §5]); D9 sets the single floor 22 and the manifest key `node.min` is 22 for all of them. Detection: `node --version`, then resolve `npm.cmd`/`npx.cmd`; show "Node 22 or newer is required: install it from nodejs.org or your package manager" (no installer command is in the evidence, so none is shown). The Node Connection is a `tool` Connection; adapters reference it via `requires`. Effects: Claude headless and OpenCode (native binary) need no Node; Codex and Gemini ACP paths do; with Node missing Codex drops to `degraded` on the `exec --json` fallback, Gemini becomes `not_installed` (npm is its only documented install). Bridge launches use `npx -y <pkg>@<exact pin>`; a launch under `npx.cmd` leaves grandchildren unless the whole tree is in the agent's Job Object (D7).

## 11. Terms notices and acknowledgement gates

Brief section 16: provider terms for driving users' CLIs are recorded and warnings shown. Each notice has an id and a version; acknowledgement is stored as `{notice_id, version, text_hash, acked_at}` and re-requested when `text_hash` changes. Notice text lives in the manifest `terms.notice` (resolved to a localized string table), not in code.

| CLI | Notice (summary) | Gate | Source |
|---|---|---|---|
| Claude Code | First-run notice (D1): Drydock starts your own, unmodified, logged-in `claude`; it never reads, copies, refreshes or transmits Claude credentials; API key auth is recommended by Anthropic for products; the ACP bridge is off pending written confirmation. | notice shown once, no block | D1, [cli §9.1] |
| Codex | Sign-in uses your ChatGPT plan or an API key. OpenAI's position on third-party orchestration could not be read. | notice only | [cli §9.3], **unverified** |
| Gemini CLI | Consumer sign-in (AI Pro/Ultra and free individual) was reported retired on 2026-06-18 (re-verify). Use an AI Studio or Vertex API key. Google's repo terms call routing Gemini CLI OAuth through third-party tools a violation. | warning banner; `oauth-personal` blocked | D2, [cli §9.2] |
| Antigravity | Research found no Google statement permitting third-party orchestration and a clause barring third-party tools from accessing the service, with enforcement history (account suspensions reported Feb 2026). Drydock drives only the official `agy` you installed and logged into and never touches credentials. An AI Studio or Vertex key is recommended where available. | **acknowledgement required** before `usable` | D2, [cli §8] (snippet-level) |
| OpenCode, Goose | Open-source licenses; the terms of whatever provider you configure apply. Using Claude or Google subscriptions through them is governed by those vendors' terms. | notice only | [cli §9.4] |
| Claude ACP bridge | Disabled. Enabling needs a recorded written confirmation from Anthropic or counsel. | hard gate | D1 |

## 12. Drift detection

Two layers, neither downloads anything onto a user's machine:

1. **Local (every user).** On each app start and each CLI spawn, the version is compared with the last-seen version. A change triggers the cheap probes (T1, T2) and a capability diff against the stored matrix. Runtime counters raise drift without a version change: parser unknown-event rate, handshake field changes (`agentCapabilities` diff), exit-code-class shifts, and `usage` fields disappearing. Result: `adapter.drift` event `{adapter, from, to, kind: version|capability|schema|exit, details_ref}`, Connection moves to `degraded` with `reason=drift:<kind>` until a Test passes.
2. **Scheduled conformance (project CI, brief section 5).** A scheduled job on a Windows runner installs the latest dist-tag of each npm-distributed CLI or bridge (`@anthropic-ai/claude-code`, `@openai/codex`, `@google/gemini-cli`, `opencode-ai`, `@aaif/goose-acp`, `@agentclientprotocol/*`), runs the conformance suite where logins exist (P3: skipped with reason otherwise), and opens a change to `compat.yaml`. It is blocked today by P2 (Actions unavailable).

## 13. What is Go code and what is manifest

| Manifest (data) | Go (cannot be data) |
|---|---|
| ids, names, binary names per OS, install text | ACP client with capability gating from `initialize` (`coder/acp-go-sdk` v0.13.5 behind our interface), stdio framing, Windows process lifecycle, Job Objects, `.cmd` shim resolution |
| version probe and regex, min/recommended | Permission UX mapping of `session/request_permission` kinds; Claude `--permission-prompt-tool` server side |
| spawn argv, optional flag templates, env, pins | Usage and quota normalizers: ACP `usage_update`, prompt `usage`, `_claude/rateLimit`, Gemini `_meta.quota`, Codex `account/rateLimits/read` client, `agy -p "/usage"` |
| auth login argv, probe argv and success rule; instruction filenames, import templates, skills dirs | Five-plus-one headless parsers (section 9); auth orchestration that never touches tokens (run login in a PTY, re-probe) |
| which parser or normalizer name to use | Node detection and guidance; provider-config writers with validation (Codex Responses-only, OpenCode provider JSON, Goose env/custom JSON) |
| terms notice id, gate flag, ToS text key | Conformance steps and the capability merge rule; gate enforcement |

## 14. Instruction-file generator mapping

Brief section 9: the app owns `CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.goosehints` in every worktree, generated from project memory and standing instructions, regenerated on change. Rules from [cli §11]:

| File | Content | Why |
|---|---|---|
| `AGENTS.md` | **Canonical** full content. Keep under 32768 bytes (Codex `project_doc_max_bytes` default) and warn above 200 lines for Claude's guidance. | Read natively by Codex, OpenCode, Goose, Antigravity; Claude 2.1.277+ reads it only when no `CLAUDE.md` exists. |
| `CLAUDE.md` | One line: `@AGENTS.md` (plus a Claude-only appendix if needed). | Claude prefers `CLAUDE.md`; the `@` import is the documented bridge. Avoids duplicate text that OpenCode or Goose might load twice. |
| `GEMINI.md` | `@AGENTS.md` import; alternatively set `context.fileName` to `["AGENTS.md","GEMINI.md"]`. | Gemini reads `GEMINI.md`, supports `@file.md` imports [cli §3]. Antigravity reads both. |
| `.goosehints` | Only Goose-specific supplements, or a short pointer. Goose reads `AGENTS.md` then `.goosehints`; whether both are concatenated is **unverified**, so never duplicate content. | Avoid double loading. |
| `.claude/rules/<name>.md` | Glob-scoped rules with `paths:` frontmatter, generated from skills. | Only Claude has native path-scoped rules. Other CLIs get skills through the Context Engine and the Workspace `skills` tool ([`expert-packs.md`](expert-packs.md)). |

Ownership: each generated file starts with `<!-- drydock:generated sha256=<hash of body> -->`. On regeneration, a hash mismatch means a human edited it; the edit is offered back as a memory update (brief section 9) and the file is rewritten only after the user chooses. Whether generated files are excluded from commits (for example via the worktree's `info/exclude`) is a project setting; default is **unverified** (section 15). OpenCode falls back to `CLAUDE.md` only when `AGENTS.md` is absent, so the single `AGENTS.md` plus import files is safe [cli §4].

## 15. Open questions / unverified

- Windows behavior of every step: install paths, `.cmd` shims, `npx.cmd` argument escaping, Ctrl-C/Ctrl-Break delivery, WSL run mode, credential file locations (phase0 section 7, items 1, 7, 8). No Test step has run on Windows (P2).
- `claude auth status` logged-out exit code; whether `--verbose` is required for `stream-json`; `--permission-prompt-tool` versus `--permission-prompts host` as the approval path (both exist in `--help` [cli §1]; D1 names the former).
- `codex login status` logged-in output; `codex exec` interrupt; whether the Codex bridge uses the user's `codex` or its own bundled copy, and `CODEX_PATH` semantics [cli §12]. Gemini and Goose have no auth probe; Antigravity's `--version`, `--mode` value set, `-p "/usage"` as an auth probe, `--acp`, resume and interrupt behavior are all unverified; Antigravity terms evidence is snippet-level. Gemini consumer cut-off date must be re-verified from an unrestricted network.
- Contradictions: [cli §3] says Gemini CLI needs Node >=20, D9 says 22 (D9 wins). [cli §12] lists Claude ACP and the Antigravity registry binary as primary; D1 and D2 make headless primary (decisions win).
- The brief's status enum has no "gated" state; the `gates[]` field is a design addition for owner review. Whether generated instruction files are git-ignored per worktree is undecided.
- Which fields the ACP `session/new` response exposes for model, mode and effort selection has not been probed live; `http` MCP header support per CLI is unverified (see [`mcp.md`](mcp.md)).
- OpenAI direct-API training and retention terms are secondary evidence; Gemini billing status cannot be detected from a key.
