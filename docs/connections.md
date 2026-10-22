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
4. **Run mode.** `native` is the default. `wsl` is a manifest-declared alternative (OpenCode's docs recommend WSL [cli §4]); a WSL install has its own credential store and its own `gh`/CLI state, so each run mode is a separate Connection instance. Built for Claude Code (section 4b, decision P41); for the other CLIs the mode stays a manifest fact and is not offered.

### 4b. WSL Connections (as built, decision P41)

`internal/wsl`, `internal/connections/wsl.go`, `internal/agents/claude/wsl.go`.

| Step | What happens |
|---|---|
| Distros | `wsl.exe -l -q` (its output is UTF-16LE with or without a byte order mark; `WSL_UTF8=1` output is read too), then `-l -v` for the default, the state and the WSL version. Container engines' own distros (`docker-desktop*`, `rancher-desktop*`, `podman-machine*`) are left out. No wsl.exe, or no distro: no WSL Connection. |
| Probe | `wsl.exe -d <distro> --exec /bin/sh -c <script>`: the script finds the default user's login shell (`getent passwd`; fish, nu and csh are replaced by bash) and reports through it, as a login shell, because Claude Code's installer puts it in `~/.local/bin`, which only `~/.profile` adds to the PATH (`~/.local/bin`, `~/.claude/local` and `~/.npm-global/bin` are also looked at directly). It reports the Linux HOME and PATH, the default user, the kernel (WSL 2 or 1), the mount root (`wslpath -u 'C:\'`, else `[automount] root` in `/etc/wsl.conf`; never assumed), interop (`binfmt_misc/WSLInterop` and `[interop] enabled`), the networking mode (`wslinfo --networking-mode`, else `networkingMode` in `%USERPROFILE%\.wslconfig`), setsid, git, and the CLI's `--version` and `auth status` output. Only marked lines are read, so a login banner does not confuse it. Results are cached 5 minutes for starts; the Connections page always probes afresh. |
| Connection | One per distro, key `claude@wsl:<distro>`, `runMode: wsl`, the distro's facts and plain notes (networking, the performance trade-off). Status: not found inside the distro (with the distro's install line, `curl -fsSL https://claude.ai/install.sh \| bash`, shown, never run), the version against `compat.yaml`, then the sign-in from the CLI's own `auth status` inside the distro (since P50 the card's **Sign in inside <distro>** button runs the manifest's `auth.login` (`claude auth login`) in the distro, with the login shell's PATH and the run marker, in a terminal in the card; what is left of it in the distro is killed by the marker when it ends, and the card re-probes), followed by what Drydock saw: the command that ran, its answer (or its exit code and the first line of its error output, with long opaque words masked) and the distro user and HOME it ran as, so a sign-in made as another user (root, say) is recognisable. An agent left on Windows when Claude Code is installed only in a distro fails with a message naming that distro and the "Where agents run" setting, when a person chose Windows for it or its project; otherwise Drydock runs it in the distro (decision P58, the Agents row). Interop off or an unknown mount root degrade it (`wsl_setup`) with the fix and make it not usable. A warning says when `ANTHROPIC_API_KEY` (or `CLAUDE_CODE_OAUTH_TOKEN`) is set on Windows, because it crosses into the distro too. |
| Test | `connection.test` with `distro` runs the steps against the distro's install (T2 starts it through wsl.exe, T3 sends the tiny prompt when asked, T5 stops it and kills its Linux processes) and keeps the result under the distro's key; the native Connection's result is separate. |
| Agents | An agent runs natively, in a distro, or as its project says (project settings, "Where agents run"); the forms offer it only for an adapter that can (`Capabilities.RunModes`), and `wsl.distros` lists the distros for them without probing. |
| Where Claude Code is (P58) | `connections.Service.ReadyWSL` lists the distros where an agent can start the CLI: found, signed in (or the sign-in unknown), an accepted version, every gate acknowledged; a distro degraded only by its setup (`wsl_setup`: interop off, an unknown mount root) counts, because the start then refuses with the fix. The WSL default distro comes first. Its uses: (1) **the pickers**: a CLI not usable on Windows but ready in a distro is available, `agentcatalog.list`'s availability and `adapter.list` carry `wslDistro`, and the wizard, the Team page and the gallery say "claude (recommended) - in WSL" (the distro is in `wslDistro` and the gallery's tooltip, not in the label: P96); (2) **a new project**: its default "Where agents run" is Windows when Claude Code is installed there and usable (or Drydock cannot tell, a configured program), else the first ready distro, stored as set by `drydock` with the reason (`project.runMode`'s `by` and `why`, shown in Project settings); (3) **at run time**: an agent that follows a default nobody chose (its project's Windows default, not set by a person) and whose CLI is not installed on Windows (`agents.NativeLocator`: the configured program, PATH, the native installer's folders; nothing is started) runs in the one ready distro, or the WSL default distro among several, with a recent probe reused; the run publishes `agent.run_mode_auto` (`runMode`, `wslDistro`, `why`) before it starts and `agent.started` carries the same. An agent or a project a person set to Windows, and a sandboxed project (P30 and WSL are refused together), are never moved: the clear error above stays. |
| Chats | The new chat dialog's **Agent CLI** lists each install inside a distro as its own choice ("Claude Code WSL 2.1.296", named as on the Connections page; only when the CLI is offered from two distros does each carry its distro, "Claude Code WSL (Debian)", P96; the Windows one then reads "Claude Code on Windows"); an open chat's adapter chip reads "Claude Code WSL" with the distro in its tooltip, from `connection.list` read while the dialog is open. An install that is not signed in, on an unsupported version or with a notice to accept is listed disabled with the reason; a degraded one is offered, as on Windows. The choice is stored on the chat (`wslDistro`, migration 00120) and every turn starts the CLI through the adapter's own WSL start, exactly as an agent's (docs/chats.md section 4). |

**Gateway reachability under WSL.** Claude reaches Drydock's MCP gateway through the stdio shim `drydock.exe mcp-shim`, which the distro starts through interop and which talks to the gateway on the Windows loopback: it works in NAT networking (where the distro cannot reach Windows `localhost`) and in mirrored networking alike. The direct HTTP entry would need mirrored networking and is not used. Without interop the gateway cannot be reached: the Connection says so and a start that needs the gateway is refused.

**Performance.** An agent in a distro reads a repository on a Windows drive (a Dev Drive included) across the WSL 9P file share, which is slow for many small files; a repository inside the distro is on the distro's own disk and fast for the agent, and its task worktrees are made inside the distro next to it (P41). Drydock's own reads of such a repository go the other way, across `\\wsl.localhost`.

**Install guidance, never a download.** Manifest `install[]` entries are display text with a "Copy" and an "Open in terminal" button; the user presses Enter themselves. (As built: the card shows the install hint as plain text, "Drydock never runs this for you"; the Copy and Open in terminal buttons are not built, and neither are Re-check or Sign in buttons: the page detects again each time it loads and on `connection.*` events.) Drydock never runs an installer, never fetches a binary, never edits `PATH`. The guidance text for each CLI is in its manifest (section 7). Windows install commands are from [cli §1-§5, §8]; none were executed (NEEDS-WINDOWS).

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
| Claude Code | `claude auth login [--claudeai\|--console\|--sso\|--email]` | `claude auth status --json`, ready when `.loggedIn == true`. The logged-out exit code is **unverified**: parse the JSON, treat missing JSON as unknown, then fall through to Test. As built, the adapter's own `Detect` runs `claude auth status` (2.1.296 prints JSON without the flag) and `--version` through the Process Manager with a timeout that kills the whole tree (`proc.Probe`, since 2026-10-11); a non-zero exit with no JSON counts as signed out. | `%USERPROFILE%\.claude\.credentials.json`; `CLAUDE_CONFIG_DIR` relocates [cli §1] |
| Codex | `codex login` (browser) or `codex login --device-auth` | `codex login status`: logged out prints `Not logged in`, exit 1 (observed); logged-in text unverified | `$CODEX_HOME\auth.json`, or keyring per `cli_auth_credentials_store` [cli §2] |
| Gemini CLI | `gemini` interactive, or API key / Vertex via env (`GEMINI_API_KEY`, ADC, `GOOGLE_APPLICATION_CREDENTIALS`) | none (no `gemini auth status`); ACP `authMethods` lists `oauth-personal, gemini-api-key, vertex-ai, gateway` [cli §3] | `~/.gemini/oauth_creds.json` (`GEMINI_CLI_HOME`), presumed Windows path unverified |
| OpenCode | `opencode providers login [url]` (alias `auth`) | `opencode providers list` (non-interactive; output needs parsing) | `~/.local/share/opencode/auth.json` (Windows mapping unverified) [cli §4] |
| Goose | `goose configure` (provider keys; some providers browser OAuth) | none; `goose doctor` exists but its output is unverified | OS keyring or `secrets.yaml` [cli §5] |
| Cline | `cline auth` (Cline account, ClinePass, ChatGPT subscription, or `--provider <id> --apikey ...`) | none; ACP `session/new` answers `-32000 Authentication required` without a sign-in (observed, 3.0.70); ACP `authMethods`: `cline, cline-pass, openai-codex` | `~/.cline` (its own files; Windows path unverified) |
| Antigravity | start `agy` interactively: it "authenticates via the system keyring, falling back to Google Sign-In"; `/logout` signs out | candidate: `agy -p "/usage"` (non-interactive, prints quota); unverified as an auth probe | system keyring [cli §8] |

Claude-specific rules (D1): never run `claude` with `--bare` (it disables OAuth and keychain use, so it breaks subscription users [cli §1]); warn on the Connection when `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` is set in the environment, because the API key switches Claude Code to API billing (support-article paraphrase, **unverified verbatim** [cli §9.1]); never inject either variable ourselves unless the user chose an "API key" Claude Connection. **Checked against 2.1.296 (2026-10-10):** a headless (`-p`) run resolves its key in this order: `ANTHROPIC_API_KEY`, then an `apiKeyHelper`, then the login, and `claude auth status` (and every session's `system/init`) reports it as `apiKeySource`. Every Drydock agent runs headless and gets `ANTHROPIC_*` from the environment, so a key the person set for other tools bills every agent turn per token to that key's account even when they are signed in with a subscription. The warning says so: "ANTHROPIC_API_KEY is set in the environment Drydock starts agents in: every agent turn is billed per token to that API key's account, not to the Claude subscription you are signed in with. Remove it from the environment to use the subscription (the value is never read by Drydock)."

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

**As built (`internal/connections/test.go`, `internal/core/connections.go`).** The Connections card asks before it runs, with "Also send one tiny prompt", off by default (it is the only step that spends tokens, so a test spends nothing unless the person ticks it; 2026-10-10). The steps: **T1** Install, version and prerequisites (then the auth state); **T2** Start and handshake (ACP `initialize`, plus **T2b** `session/new` for ACP agents; for headless Claude the process must start and stay alive, the init event is confirmed by T3); **T3** One tiny prompt ("Reply with the single word OK.") only when the box is ticked, on Claude's `haiku` or else the first model the adapter lists, skipped when not requested, no model, not signed in or no session; **T4** Interrupt a running turn, which the app never requests ("not requested: it needs a second, longer prompt"; the real-CLI test asks for it); **T5** Stop and process cleanup (the process must be gone 5 s after Stop; killing grandchildren is asserted only by the Windows tests); **T6** Gateway reachability is always skipped ("checked on the agent's first real run instead: its Inspector and the MCP page show whether Drydock's workspace tools were attached"); **T7** Provider path is skipped ("no provider is configured for this agent"). An unaccepted terms notice skips everything from T2 on. Only the outcome (pass or fail, the first failure's reason, the CLI version) is kept, in the `settings` table under `connection.test.<adapter>`, and it survives a restart: a failure makes the Connection `degraded` (`test_failed:<reason>`), a pass clears `version_untested` for exactly that version. The full report is shown once in the page and is not stored; there is no conformance history.
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

Location: `internal/agents/adapters/manifests/<id>.yaml` (embedded; user custom CLIs in `<app data>/adapters/custom/<id>.yaml`). Validated at load by a Go struct; unknown keys are errors. Argv templates are arrays; `{{var}}` placeholders are typed (`model`, `effort`, `session_id`, `cwd`, `mcp.config_path`) and substituted without shell interpretation. Top-level keys: `meta{id, display_name, kind, license, tier, run_modes}, binaries, install[], requires[], detect, versions, auth, transports{primary, acp, headless}, provider_config, mcp, usage, capabilities_declared, instructions, terms, gate`. `{a: b}` flow style is used for density; documents are separated by `---`. `repo_config` lists the files in the folder the CLI works in that it acts on (hooks, MCP servers, plugins, environment files; relative paths, a trailing `/` for a folder): the runtime does not start the CLI in a folder that has any of them until a person trusted exactly those files (`docs/security.md` s3 item 2b).

```yaml
# claude.yaml (D1: headless primary; ACP bridge present but disabled)
meta: {schema: 1, id: claude, display_name: Claude Code, kind: agent_cli, license: proprietary, tier: premium, run_modes: [native, wsl]}   # [cli §1]
binaries: {native: [claude.exe, claude.cmd], wsl: [claude]}
install:                                             # display only; Drydock never executes these
  - {shell: powershell, command: 'irm https://claude.ai/install.ps1 | iex'}
  - {shell: cmd, command: 'curl -fsSL https://claude.ai/install.cmd -o install.cmd && install.cmd && del install.cmd'}
  - {shell: cmd, command: 'winget install Anthropic.ClaudeCode', note: no auto-update}
  - {shell: cmd, command: 'npm install -g --allow-scripts=@anthropic-ai/claude-code @anthropic-ai/claude-code', requires: [{tool: node, min: "22"}]}
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
  acp: {enabled: false, argv: [npx, -y, "@agentclientprotocol/claude-agent-acp@0.88.0"], env: {CLAUDE_CODE_EXECUTABLE: "{{resolved_binary}}"}, requires: [{tool: node, min: "22"}], enable_when: written_confirmation_recorded}
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
install: [{shell: cmd, command: 'scoop install opencode'}, {shell: cmd, command: 'choco install opencode'}, {shell: cmd, command: 'npm i -g --allow-scripts=opencode-ai opencode-ai@latest'}]
detect: {version: {argv: [--version], regex: '^(\d+\.\d+\.\d+)'}}
versions: {min: "1.18.35", recommended: "1.18.35"}
auth: {login: {argv: [providers, login], terminal: true}, probe: {argv: [providers, list], parse: text_providers}}
transports: {primary: acp, acp: {argv: [opencode, acp], env: {OPENCODE_CONFIG_CONTENT: '{"compaction":{"auto":true}}'}, client_meta: {opencode/child-session-updates: true}}, headless: none}      # D11: `run` cannot answer permissions, drops deltas; env and client_meta: P53 (a)
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

Classes: `docs_only` < `inspected` (Linux, no login) < `verified_linux` (a real turn on Linux with the user's own login) < `verified_short` (Windows Test passed) < `verified_full` (Windows full suite). GitHub's Windows runners have no CLI logins, so no entry goes above `verified_linux` until a signed-in Test passes on a Windows PC. Bridge packages are pinned exactly (D9); `@zed-industries/*-acp` packages are deprecated and must not be used [cli §0, §7].

## 9. The headless contract

A CLI is adapter-eligible if it provides (brief section 5): (a) a non-interactive prompt-and-exit mode; (b) a machine-readable output stream carrying text, tool calls and a final result; (c) an exit code; (d) working-directory control (we set cwd in the Process Manager, so a flag is optional). Optional and recorded as `present|absent|unknown`: model, effort, resume, usage, permission prompts. Normalized events every parser emits (Go, one parser per dialect: Claude SDKMessage, Codex ThreadEvent, Gemini JsonStreamEvent, OpenCode `{type,part}`, Goose StreamEvent, Antigravity `init/step_update/result` [cli §12]):

`session` (id, model), `message_delta`, `message`, `tool_call{id,name,args}`, `tool_result{id,status,output}`, `usage{input,output,cached_read,cached_write,reasoning,cost?,source}`, `rate_limit{status,resets_at,utilization}`, `permission_request{id,action}`, `result{status,error?}`, `error`. Parsers never panic on unknown event types: they count them (`adapter_unknown_events`), which feeds drift detection. As built (Claude, 2026-10-11): the parser counts unknown top-level types per session (`Parser.UnknownEvents`) and emits a non-fatal "adapter drift" warning the first time each appears; the known no-op types (`tool_progress`, `autocompact_state`, `active_goal`) stay quiet. An event of a type the parser knows (`assistant`, `user`, `stream_event`, `system`) whose body is in a shape it cannot read is drift too, kind `<type> (unreadable)`, counted and warned about once, where it used to be dropped without a word; a line that starts like an event but is not JSON is `(unreadable line)`; and a `result` that cannot be read is a fatal error of the turn (its outcome and cost are unknown), not a silence the turn waits through (audit 2026-10-16, L27). The warning carries a code (`adapter_drift`) and the event type; the core records each new type per adapter (kept across restarts), publishes `adapter.drift` `{adapter, kind: schema, eventType}` once per type, and the Connection shows `degraded` with reason `drift:schema` and the types named (still usable) until a Test connection passes (`internal/core/adapterdrift.go`). Drift of the other kinds below (version, capability, exit) is not raised yet. Token fields are normalized once (D12); `cost` carries `source: reported|estimated`.

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
| OpenCode, Goose, Cline | Open-source licenses; the terms of whatever provider you configure apply. Using Claude or Google subscriptions through them is governed by those vendors' terms. | notice only | [cli §9.4] |
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
| auth login argv, probe argv and success rule; instruction filenames, import templates, skills dirs | Five-plus-one headless parsers (section 9); auth orchestration that never touches tokens (run login in a PTY, re-probe: built on 2026-10-13 as `connection.signIn` from the manifest's `auth.login`, decision P50) |
| which parser or normalizer name to use | Node detection and guidance; provider-config writers with validation (Codex Responses-only, OpenCode provider JSON, Goose env/custom JSON) |
| terms notice id, gate flag, ToS text key | Conformance steps and the capability merge rule; gate enforcement |

## 14. Instruction-file generator mapping

Brief section 9: the app owns `CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.goosehints` in every worktree, generated from project memory and standing instructions, regenerated on change. Rules from [cli §11]:

| File | Content | Why |
|---|---|---|
| `AGENTS.md` | **Canonical** full content. Keep under 32768 bytes (Codex `project_doc_max_bytes` default) and warn above 200 lines for Claude's guidance. | Read natively by Codex, OpenCode, Goose, Antigravity. Claude Code, started by Drydock with `--setting-sources user`, does not read it itself; the Claude adapter appends it (through `CLAUDE.md`) to the system prompt (see below). |
| `CLAUDE.md` | One line: `@AGENTS.md` (plus a Claude-only appendix if needed). | The `@` import is Claude's documented bridge; as built the Claude adapter resolves it itself. Avoids duplicate text that OpenCode or Goose might load twice. |
| `GEMINI.md` | `@AGENTS.md` import; alternatively set `context.fileName` to `["AGENTS.md","GEMINI.md"]`. | Gemini reads `GEMINI.md`, supports `@file.md` imports [cli §3]. Antigravity reads both. |
| `.goosehints` | Only Goose-specific supplements, or a short pointer. Goose reads `AGENTS.md` then `.goosehints`; whether both are concatenated is **unverified**, so never duplicate content. | Avoid double loading. |
| `.claude/rules/<name>.md` | Glob-scoped rules with `paths:` frontmatter, generated from skills. | Only Claude has native path-scoped rules, but with user settings only Claude Code does not load them; the adapter appends every rule file in full, so `paths:` is not applied. Other CLIs get skills through the Context Engine and the Workspace `skills` tool ([`expert-packs.md`](expert-packs.md)). |

**As built for Claude Code (decision P16).** Drydock starts Claude Code with `--setting-sources user`, so a repository's own settings cannot add hooks or allowed tools; Claude Code then also skips the project's `CLAUDE.md`, its `@` imports and `.claude/rules`. The adapter (`internal/agents/claude/project_memory.go`) reads `CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md` (with `@` imports inside the working folder, up to 4 levels) and `.claude/rules/*.md` from the working folder and appends them to the system prompt, passed in a temporary file with `--append-system-prompt-file` (a long command line breaks under Windows `cmd.exe`). Each file is cut at 64 KB, all of them at 256 KB; imports from outside the folder are left as text; settings files are never read.

Ownership: each generated file starts with `<!-- drydock:generated sha256=<hash of body> -->`. On regeneration, a hash mismatch means a human edited it; the edit is offered back as a memory update (brief section 9) and the file is rewritten only after the user chooses. Whether generated files are excluded from commits (for example via the worktree's `info/exclude`) is a project setting; default is **unverified** (section 15). OpenCode falls back to `CLAUDE.md` only when `AGENTS.md` is absent, so the single `AGENTS.md` plus import files is safe [cli §4].

## 15. Open questions / unverified

- Windows behavior of every step: install paths, `.cmd` shims, `npx.cmd` argument escaping, Ctrl-C/Ctrl-Break delivery, credential file locations (phase0 section 7, items 1, 7, 8). No signed-in Test step has run on Windows: CI's Windows runners install the CLIs from npm and run an ACP initialize through `npx.cmd` (`windows-probes`), but have no logins. The WSL run mode is proven against a real WSL 2 distro only with a stand-in claude (CI's `wsl` job, P41); a real, signed-in Claude Code inside a distro, mirrored networking and the 9P speed of a Dev Drive are not.
- `claude auth status` logged-out exit code; whether `--verbose` is required for `stream-json`; `--permission-prompt-tool` versus `--permission-prompts host` as the approval path (both exist in `--help` [cli §1]; D1 names the former).
- `codex login status` logged-in output; `codex exec` interrupt; whether the Codex bridge uses the user's `codex` or its own bundled copy, and `CODEX_PATH` semantics [cli §12]. Gemini and Goose have no auth probe; Antigravity's `--version`, `--mode` value set, `-p "/usage"` as an auth probe, `--acp`, resume and interrupt behavior are all unverified; Antigravity terms evidence is snippet-level. Gemini consumer cut-off date must be re-verified from an unrestricted network.
- Contradictions: [cli §3] says Gemini CLI needs Node >=20, D9 says 22 (D9 wins). [cli §12] lists Claude ACP and the Antigravity registry binary as primary; D1 and D2 make headless primary (decisions win).
- The brief's status enum has no "gated" state; the `gates[]` field is a design addition for owner review. Whether generated instruction files are git-ignored per worktree is undecided.
- Which fields the ACP `session/new` response exposes for model, mode and effort selection has not been probed live; `http` MCP header support per CLI is unverified (see [`mcp.md`](mcp.md)).
- OpenAI direct-API training and retention terms are secondary evidence; Gemini billing status cannot be detected from a key.

## 16. As built (2026-10-08, Linux sandbox)

- **Where things are:** manifests are embedded from `internal/agents/manifests/cli/*.yaml` (claude, codex, gemini, opencode, goose, antigravity, a `custom` template) with a strict validator, `compat.yaml` and a `Registry`; the generic ACP client is `internal/agents/acp` (scripted stand-in agent in `acp/fakeacp`); detection, gates and Test Connection are `internal/connections`; the adapter contract suite is `internal/agents/conformance`.
- **Resolved by running the real CLIs:** `claude -p --output-format stream-json` requires `--verbose` (in the manifest). `codex-acp@2.1.1` runs its own bundled codex (0.159.3), not the installed one. `opencode providers list` printing "0 credentials" does not mean unusable (free models exist), so auth is reported as unknown. `session/new` without credentials fails with "authentication required" (gemini, codex-acp) or "provider not configured" (goose). Live ACP `initialize` of gemini 0.63.0, opencode 1.18.35, goose 1.53.0 and codex-acp 2.1.1 matches the capability matrix (run with `DRYDOCK_ACP_<NAME>` env vars; otherwise skipped with the reason). Real claude 2.1.294 passes conformance and Test Connection T1 to T5 (the real-CLI test asks for T4; the app's Test never does, see section 5).
- **Added:** compat class `verified_linux` (between `inspected` and `verified_short`) for a real Linux turn that is not a Windows proof.
- **Not built / unverified:** no real prompt turn for any ACP CLI (no model credentials here), so real tool names, args and whether `inputTokens` includes cached tokens are unknown; Antigravity `Start` and the `custom` declarative dialect return "not implemented" (no captured fixtures); the codex headless fallback is declared, not built; everything on Windows (`.cmd` shims, tree kill, the logged-out `claude auth status` exit code); whether the Codex bridge honours `CODEX_PATH`.

## 17. Cline (added 2026-10-09, decisions P29)

Cline (Apache-2.0, github.com/cline/cline) is the eighth manifest, `internal/agents/manifests/cli/cline.yaml`, driven by the generic ACP client like OpenCode and Goose. It is listed on the Connections page (`connection.list`), and an agent or a chat can be created with adapter `cline`.

| Item | Value | Evidence |
|---|---|---|
| Install (shown, never run) | `npm i -g --allow-scripts=cline cline`, Node 22+ | README; the package's `bin/cline` is a Node launcher (`#!/usr/bin/env node`) for the bundled platform binary, so Node is needed at run time too (D9) |
| Detection | `cline --version` prints `3.0.70`; binaries `cline.cmd`, `cline.exe` | observed on Linux (npm package in a scratch folder) |
| Pin | min and recommended `3.0.70`, compat class `inspected` | `TestLiveACPHandshake/cline`, `TestLiveACPConnections/cline`, `TestLiveACPHandshakes/cline` with `DRYDOCK_ACP_CLINE=<path>` |
| Transport | `cline --acp`; no `--auto-approve`/`--yolo`, so every tool call reaches Drydock as `session/request_permission`, which Drydock refuses (as built no handler asks a person, so nothing is approved: audit 2026-10-16, L29) | `apps/cli/src/acp/permissions.ts` (options allow once, allow always, reject) |
| Capabilities from `initialize` (D10) | `loadSession: true`, image prompts; no resume, fork, list, close; no MCP http or sse | observed; matches `apps/cli/src/acp/acpAgent.ts` |
| Session | modes `plan` and `act` (default act); model is a `session/set_config_option` (`model`, category model; a `provider` option shares the category and comes first, so Drydock's last-wins reading picks `model`); no effort option; an `auto_approve` boolean option Drydock never sets | source |
| Usage and cost | none over ACP (the agent's usage events are dropped in `session-updates.ts`): cost is unknown, never zero | source |
| Auth | `cline auth` in the embedded terminal; no status probe; `session/new` fails with `-32000 Authentication required` until signed in (or `CLINE_API_KEY` is set) | observed |
| MCP | only through ACP `session/new.mcpServers`, and only if the human allows it (`mcp.try.set`, P20). Cline 3.0.70 stores that list on the session but nothing in `apps/cli` reads it back, so the gateway's tools are most likely not offered to the model: marked unverified | source reading |
| Instructions | reads the workspace `AGENTS.md` (and its own `.clinerules`) | `sdk/packages/core/src/extensions/config/user-instruction-config-loader.ts` |

Not verified: a real prompt turn (no sign-in here), tool names and arguments as Cline reports them, `session/load` of a real stored session, anything on Windows (the npm `.cmd` shim, tree kill of node plus the Bun-based binary).
