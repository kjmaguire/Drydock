# Drydock: decisions

Date: 2026-10-08. Evidence: [`phase0.md`](phase0.md) and its appendices. Section numbers refer to [`brief.md`](brief.md). Status values: **locked** (owner approved or no evidence against), **provisional** (needs a Windows or networked check named in the entry), **open** (owner action needed).

## 1. Owner decisions on the Phase 0 contradictions

| ID | Decision | Status |
|---|---|---|
| D1 | Claude Code is driven by spawning the user's own, unmodified, user-logged-in `claude` headlessly (`-p`, stream-json in and out, `--permission-prompt-tool` for approvals). Drydock never reads, copies, refreshes or transmits Claude credentials. The Claude ACP bridge is off by default and ships only after written confirmation from Anthropic or counsel. A first-run notice states the terms and that the user's own login is used. | locked |
| D2 | Gemini CLI is supported with API-key or Vertex auth, with a warning on the Connection that consumer sign-in was reported retired on 2026-06-18 (re-verify). **Antigravity gets an adapter** because it is now the owner's route into Google's CLI. It drives only the official `agy` binary the user installed and logged into (headless stream-json in and out, `--mode`, `--effort`, `-p "/usage"` for quota), never touches credentials, and is behind an explicit acknowledgement gate. Research found no Google statement permitting third-party orchestration and a terms clause barring third-party tools from accessing the service, with enforcement history (account suspensions in Feb 2026); the gate says so in plain language and recommends an AI Studio or Vertex key where available. The first-party `antigravity-acp` registry binary is a second transport, evaluated later. | locked, with a terms risk the owner accepted |
| D3 | **Both Codex and OpenCode** are cheap-tier-capable agents. OpenCode is the default (OpenAI-compatible providers, ACP native). Codex uses its subscription or an API key by default; it joins the cheap tier only on a provider that exposes the Responses API (`wire_api = "responses"` is the only value Codex accepts), which Drydock validates in the provider-config writer and which the Connection test proves against the real endpoint. Goose is the third. | locked |
| D4 | OpenRouter is **model-agnostic**: no model is hard-coded as "the cheap model". At setup and then in the background, the catalog is refreshed from `GET /api/v1/models/user` (the effective filtered catalog), and the router chooses the cheapest `private` model with tool calling that passes a startup tool-call probe. Every request to OpenRouter on a `private` route carries `provider:{data_collection:"deny", zdr:true}` and fails closed. `:free` routes are `trainable` and only used for public-docs CONSULT. Cohere North Mini Code is a candidate when a paid private route or a self-hosted endpoint exists, not an assumption. | locked |
| D5 | The tree-sitter repo map proceeds, gated by a Windows CI job (MinGW gcc, pinned grammars, parse test, Defender scan). Until that job is green on a Windows runner the repo map is not built and Slice 1 does not depend on it: no tree-sitter code is in the module (only the Phase 0 probe, its own module in `evidence/phase0/go-probes/ts`), and the workspace `repo_map` tool answers with a fixed "gated (decision D5)" message (corrected 2026-10-09: this said "behind a build tag"; there is no build tag). | locked; the CI job (`repomap-cgo`) is green and the owner decided to build it (2026-10-15): **built** (P62), `internal/repomap` with tree-sitter through CGO behind `//go:build cgo`; the product build is CGO with MinGW gcc; a build without CGO has no repo map and says so. Defender scan of the CGO exe still to do |
| D6 | Cloudflare is an **agent-side template**, not part of Drydock's own infrastructure: Drydock is a desktop app. The golden starter (`testdata/starter-cloudflare-saas`) is a project template that agents build in; the preview/production separation applies to those projects: a separate Cloudflare account (or none) for previews, an agent-process command allowlist, no production token in agent processes, separate bindings in the `previews` block. Amends brief section 13a: the "preview-only credential" is an account boundary and an allowlist, not a token scope. | locked |
| D7 | Process trees: the Drydock process joins a `KILL_ON_JOB_CLOSE` job at startup; each pipe-spawned agent also gets its own job (assigned suspended, then resumed); ConPTY children (the embedded terminal) are tree-killed by walking the job after spawn. The job-list attribute constant (0x2000D) is defined in our code. Proven by the `jobtree` probe on a Windows host before `internal/proc` is called done. | locked; **blocked on Actions** for proof |
| D8 | Packaging: NSIS installer with `INSTALL_SCOPE=user`, no MSIX or WiX in v1. Updates use Wails v3's built-in updater (`app.Updater`) with a custom `Provider` pointed at Drydock's own release feed (not GitHub Releases, which cannot serve a private commercial repo without tokens in the client), `Channel` set to `stable` or `beta`, SHA-256 digests and an Ed25519-signed manifest with the public key pinned in the app, release notes shown by the built-in update window, and the previous executable kept for rollback. Signing: Azure Artifact Signing or an OV certificate, decided at company-account creation; plain signtool via `wails3 tool sign` until then. | locked |
| D9 | Node 22+ is a documented prerequisite for the ACP bridges, Gemini CLI, Wrangler and Playwright. Connections detects Node and shows install guidance. Bridge packages are pinned exactly in manifests. | locked |
| D10 | The generic ACP adapter gates on `agentCapabilities` returned by `initialize` (resume, fork, list, usage, MCP transports differ per agent) and never assumes a capability from the provider name. Goose is driven as an ACP agent. | locked |
| D11 | OpenCode is driven over `opencode acp` (headless `run` cannot answer permissions and drops deltas). Antigravity headless `-p` soft-denies unanswered permissions, so its adapter runs in a conservative `--mode` and surfaces denied actions to the human. | locked |
| D12 | Telemetry attribute names are our own constants (OTel GenAI conventions are in Development and moved repositories; Go `semconv` v1.42+ has no `gen_ai`). OTLP export is optional and off by default. Token fields are normalized once (cached tokens included in input in the OTel view). | locked |
| D13 | The quota ledger polls `GET /api/v1/key` for OpenRouter and reads `usage` on every response; it reads `anthropic-ratelimit-*` and `x-ratelimit-*` headers where they exist; Gemini limits are configured. `Retry-After` and `retryDelay` are honored on errors. The brief's `trainable` is the term used (the provider report's `public` maps to it). | locked |
| D14 | Integrations: do not bundle Sentry's MCP server (FSL), Aikido's MCP server (AGPL or commercial) or Notion's retired local MCP server; connect to hosted servers or to user-installed ones. Aikido auth is per-customer client credentials. `gh` has no review-thread data, so thread read/resolve uses `gh api graphql`; ETag polling uses Go's HTTP client directly. | locked |

## 2. Stack decisions (brief section 2), with the evidence that settled them

| Layer | Decision | Evidence and caveats |
|---|---|---|
| Language and toolchain | Go, `go 1.27.3` in `go.mod` (moved up from 1.26.0 on 2026-10-09, to 1.27.3 on 2026-10-20) | sqlite, goose, otel, mvdan/sh, x/sys need 1.26 or newer; the whole module graph was updated with it. |
| Desktop shell | Wails v3 `v3.0.0-beta.34`, isolated in `app/`, pinned | MIT, beta, builds Windows-only with `CGO_ENABLED=0` (Wails needs no CGO; since P62 the product exe is built with `CGO_ENABLED=1` for the repo map's parser); bindings generation proven; open issues unchecked. Accepted risk (brief risk register). |
| Storage | `modernc.org/sqlite` v1.61.0, WAL, FTS5; migrations with `pressly/goose` v3.29.0 | FTS5/bm25/WAL proven on Linux; single writer connection plus pooled readers; `BEGIN IMMEDIATE`/`_txlock=immediate` for write transactions. Re-run on NTFS. |
| Secrets | `zalando/go-keyring` v0.2.8; values over 2560 bytes are encrypted into SQLite with the key in Credential Manager | Limit confirmed in source and Microsoft docs. |
| Workflow engine | In-house on SQLite; agent-framework-go is a design reference only | Experiment: re-runs completed siblings after a partial superstep and double-emits on duplicate resume. We provide idempotency keys, leases and `response_entry` checkpoints. |
| LLM clients | `anthropic-sdk-go` v1.84.0, `github.com/openai/openai-go/v3` v3.85.0 | Read final-chunk usage ourselves (`CacheWriteTokens` is dropped by the accumulator). |
| ACP | `github.com/coder/acp-go-sdk` v0.13.5 behind our own interface | Pre-1.0, no Windows CI; `usage_update` and prompt `usage` unstable. |
| MCP | `github.com/modelcontextprotocol/go-sdk` v1.8.0 behind our own interface | Mixed MIT/Apache-2.0 license in transition: legal sign-off before release. `CommandTransport` kills only the direct child: servers are started under our process manager. |
| Policy and analysis | `cel.dev/cel-go` v0.32.0; `mvdan.cc/sh/v3` v3.14.1; `github.com/zricethezav/gitleaks/v8` v8.30.1 | mvdan/sh parses POSIX only: Windows command analysis needs a PowerShell/cmd strategy (see `security.md`). |
| Terminal | `aymanbagabas/go-pty` v0.2.3 (ConPTY) plus `charmbracelet/x/ansi` v0.11.9 | Run on Windows to confirm; cannot take a job list (see D7). |
| Verification tools | osv-scanner 2.7.0, Opengrep 1.31.0, Playwright 1.64.1, Wrangler 4.151.0 as external tools; osv-scanner and Opengrep come in the download (`packaging/bundle.json`, P77) and are updated by Drydock from their GitHub releases, checked against GitHub's SHA-256 digest (P66) | osv-scanner sends package metadata to osv.dev unless offline: disclosed. Opengrep (LGPL-2.1, native Windows binary) is the security scan's Code engine (P44); Drydock ships built-in rules for it (P77). |
| Local models | llama.cpp's `llama-server`, the newest weekly Windows build (not pinned: the Software Versions page shows the newest build as the one used), MIT, from `ggml-org/llama.cpp`'s GitHub release, checked against GitHub's SHA-256 digest; GGUF models from public Hugging Face repositories, checked against the Hub's SHA-256 | P65, `docs/local-models.md`. The TurboQuant fork (`AtomicBot-ai/atomic-llama-cpp-turboquant`) is the build the download carries (release `b10298-2.0.0`, P77). Not yet run against the real llama.cpp |
| Repo map | `github.com/tree-sitter/go-tree-sitter` v0.25.0 (CGO) + MIT grammars: Go v0.25.0, TypeScript/TSX v0.23.2, JavaScript v0.25.0, Python v0.25.0, C# v0.23.5, Java v0.23.5, Rust v0.24.2; Aider algorithm | D5, P62: built in `internal/repomap` (Linux; the Windows CI jobs build it with MinGW gcc). The grammars' own `queries/tags.scm` plus our extra queries (TSX and JSX element references, which no upstream query covers, and TS, JS, Python, C#, Java, Rust additions). PHP and SQL, probed in Phase 0, are not built. The product exe is built with `CGO_ENABLED=1`. |
| Telemetry | OpenTelemetry Go v1.47.0, optional | D12. |
| Dev tasks and notices | go-task v3.54.0; `google/go-licenses/v2` v2.0.1 | |
| Git hosting | `gh` v2.103.0 as a user-installed Connection | `gh` is not bundled. |
| Per-task sandbox | Docker (Docker Desktop on Windows), the only supported engine (P135), which you install, driven only through the `docker` command (tested with Docker Engine 29.9.2 on Linux; 29.8.2 when P30 was decided); egress proxy built from `cmd/drydock-egress` | P30. No engine is bundled and no image of Drydock's is pulled from a registry. |

## 3. Process decisions

- **P1. Development host versus product target.** Drydock ships for Windows 10/11 only and CI targets `windows-latest` only. The sandbox used to build it is Linux, so Go packages other than `internal/proc` and `app/` are written to compile and test on any host, purely so they can be developed here. `internal/proc` has a Windows implementation (`proc_windows.go`, Job Objects) and a minimal portable `os/exec` fallback (direct-child kill only) used by tests on a non-Windows development host; the fallback is not a product path and is not shipped. Tree-kill semantics are only asserted by the Windows tests.
- **P2. Windows CI is currently unavailable.** GitHub Actions jobs for this private repository fail before a runner is assigned (a plain Ubuntu `echo` job fails in 3 seconds with no steps and no runner), which points to an account-level setting (billing, spending limit, or Actions disabled for private repositories). The owner must resolve it. Until then every Windows-only claim is **unproven**, workflows are committed so they run as soon as Actions works, and no slice is marked done on the Windows gate. **Resolved 2026-10-10:** the repository was made public, which grants GitHub-hosted runners at no cost; `windows-latest` jobs run on every push (`ci`, `verify`), and because the repository is public no self-hosted runner may be attached to it (P32's second route is closed; the audit of 2026-10-16, M19, made every workflow name its hosted runner outright).
- **P3. Real-agent integration tests.** The sandbox has the Claude Code CLI with the session's own credentials, but no network route to OpenRouter, Google, or OpenAI and no other CLI logins. Integration tests against real CLIs therefore run when the CLI is installed and authenticated and are reported as **skipped, with a reason** otherwise; no test is faked to pass. Results are recorded in `docs/evals.md`.
- **P4. Dogfooding.** The Engineering Room is a Windows desktop app and cannot run in the cloud sandbox, so Slices 2 to 5 cannot be built through the app here. Per the brief's fallback clause ("fall back to direct work only when the app is the thing that is broken"), this is recorded now: Slices 2 to 5 are built directly until the app runs on the owner's machine, and `docs/evals.md` reports no dogfooding numbers rather than inventing any.
- **P5. Secrets hygiene.** The sandbox exposed injected tokens in a tool output during Phase 0. Nothing was recorded; the owner should rotate them. Repository scanning with gitleaks is part of the Slice 2 `secrets` check and a pre-commit hook in the Taskfile.
- **P6. Module layout.** One Go module at the repository root, `github.com/kjmaguire/drydock`. The Phase 0 probes under `evidence/` are separate modules and are not part of the product build.

- **P7. Review text on PRs is untrusted.** Only OWNER, MEMBER and COLLABORATOR authors can start an automatic rework, and their text reaches the agent quoted as data. Everything else is shown to the human in the approval and is never acted on by an agent. Drydock's own comments carry a marker and are never read back. Merging is a `human:` action and is guarded by the reviewed head SHA, so a push after review cannot be merged unseen.
- **P8. OpenRouter `data_class`.** The `private` filter can be enforced on requests Drydock makes itself (router/LLM client) but cannot be injected into requests that an agent CLI makes; CLIs pointed at OpenRouter rely on the account-level privacy setting, which the Providers page tells the user to verify (refines D4).

- **P9. Provider attestations.** Private routes over plain http to a private-network host and private Gemini routes require an explicit user attestation (`lan_attested`, `billing_enabled`) recorded on the provider; without it registration is refused. Owner may change the attestation model.

- **P10. Slice 2 economics choices (2026-10-08).** (a) A budget at 100% parks the run on an approval request that **re-runs the held turn after the human answers** (`RequestSpec.Retry` in `internal/workflow`, a backward-compatible engine change); approving without raising the limit lets one more turn through, rejecting fails the run. The pause is at the turn boundary: a running turn completes. (b) Cost that no CLI or provider reported is **unknown, never zero**; budgets and evals share one classification (`docs/evals.md` s11.2). (c) The startup tool-call probe spends a few billed requests, so running it automatically at every start is **opt-in** (`route.probe.startup`); selecting is always available on demand. (d) Agent turns are **not** yet routed through the router: until `core.agentConfig` is changed, the extension records one `static` decision per agent turn (the agent's own assignment) so outcomes join from day one, labeled as static everywhere. (e) Evals correct the usage `tier` of CLI agents that run on a registered provider at read time, because the runtime stamps `premium` on every agent today. (f) Telemetry stores only allowlisted, identifier-shaped attributes; free text cannot be recorded.

- **P11. Repository skills are offer-only by default.** A skill found in the project's own `.aet/skills/` is offered to the human and delivered only after an explicit accept; the setting `skills.manual_accept_repo = 0` restores automatic acceptance for committed, unmodified, scanned skills. Reason: an agent-authored skill merged through an approved diff would otherwise become standing instructions without its own decision. Registry skills were never auto-accepted.

- **P12. Licensing that is not configured is not a locked app (2026-10-08).** The license verification keys are a build-time variable (`internal/core.LicensePublicKeys`, like the update key in `app/updates_windows.go`). A build with none is an unlicensed or development build: `license.status` reports `configured:false` and `state:none` with a loud banner and a startup log line ("licensing is not configured in this build"), and **everything stays allowed** (all update channels too); importing a license says licensing is not configured. Enforcement exists only when keys are embedded; then no license, or an expired one, is read-only (`docs/packaging.md` s10) and running work is parked, not lost. Release builds must set the keys; a release checklist item, not a runtime guess.
- **P13. Retention is suspended while the license is read-only (2026-10-08).** This confirms the proposal in `packaging.md` s10 as the implemented behavior pending the owner's sign-off: `Core.RetentionAllowed()` is false in the expired state, and with no license in a build that has keys; the compactor and deleter (30 days compact, 90 days delete, brief 18; `Core.RunRetention`, built 2026-10-11) call it before touching data and do nothing when it is false. Nothing is deleted because of a license state. Imported project data is untrusted by construction (`packaging.md` "As built"): memory proposed, skills offered, requirement chunks quarantined, rules disabled, approvals cancelled, runs never resumed.
- **P14. Team management: purpose, edit, remove (2026-10-08).** An agent has a human-written **purpose** (standing instructions, at most 4,000 characters) that `Runtime.Instructions` adds to the system prompt of every session the agent starts, from every path (workflow turns, templates, room replies): Claude gets it as `--append-system-prompt` (checked against the real CLI 2.1.294: with the text the model follows it, without it does not) (since 2026-10-09 through `--append-system-prompt-file`, a temporary file: on Windows an npm-installed `claude.cmd` runs through cmd.exe, which cuts a multi-line argument at its first newline), ACP agents get it in front of the first prompt of the session because ACP has no system-prompt channel. The framing says the instructions are always in force and describe HOW the agent works while the task says WHAT to work on; the independent review and the human's approval do not depend on the prompt, the workflow enforces them. The wording was chosen by measuring the real CLI (haiku, low effort, a style instruction followed after a tool call): a wording that told the model the instructions do not override the task was obeyed in about 2 of 9 runs, the adopted wording in 7 of 8 (n is small; behavior-type purposes such as "never touch the UI" were not measured, and a model can always ignore a soft instruction). It is trusted because the human typed it; therefore an **imported archive's purpose is dropped and a permission mode that skips approvals (`bypassPermissions`, `auto`, `dontAsk`) is lowered to `acceptEdits`** (reported in the import preview). Model and effort can be edited after creation (the adapter cannot: make a new agent). Effort is checked against the levels the adapter lists (`Capabilities.Efforts`; Claude: low, medium, high, xhigh, max, from `claude --help`), model and effort must be a single word not starting with `-`, and a model is only suggested from the adapter's aliases because full model names are free text. Adapters whose CLI reports its own choices in `session/new` (the ACP ones) have no list yet, so their values are not checked ahead of time. Removing an agent **archives** it (history, usage and room messages keep pointing at the row; it leaves the team, cannot be assigned or mentioned) and is refused while the agent owns an unfinished task or is running. Migration `00090_agent_purpose.sql`.
- **P15. MCP servers: connected once in a library, put into projects, granted to agents (2026-10-08).** The human connects a server once in the workspace **MCP library** (a stdio command, or an https URL; plain http only on loopback; credentials included) and drags it onto a project, then grants it to individual agents of that project. Rules: (1) **deny by default** at both steps: a project reaches only a server it was put into, an agent only a server it was granted inside its own project (`project_mcp_servers`, `agent_mcp_access`); (2) the server is always `private_data` (project-scoped, no caching, absent from runs on a trainable provider route); (3) the HUMAN declares which tools only read; every other tool waits for a human approval for each call, shown with its exact arguments in Needs you and on the page, and the call stays pending until answered (an agent's text can never answer it; only `human:` can); the server's own `readOnlyHint` is shown as a suggestion and never trusted; (4) the **send guard** (on by default) refuses arguments with code fences, secrets, large or long text before they leave; (5) tokens and secret environment values live only in the credential vault (`mcp:<id>:token`, `mcp:<id>:env:<NAME>`), the database keeps names and booleans, the scanner is taught the values so a server cannot echo them, and a stdio server starts with an allowlisted environment, not the app's; (6) results are shaped and wrapped as untrusted text like every upstream; (7) the library, project membership and grants are **never exported**, an archive must not bring a program onto this machine (`mcp_library`, `project_mcp_servers`, `agent_mcp_access` are in `projectio.ExcludedTables`). "Find tools" starts the server and stores its tool list so a CLI is not kept waiting for a slow server to start. Verified with the real Claude Code CLI 2.1.295 on Linux (it called a user-added stdio server through the generated entry and received the agent's purpose); the other CLIs still get no MCP (docs/mcp.md section 10). Migration `00091_project_mcp_servers.sql`.
- **P16. Repository hooks do not run in agent sessions (2026-10-08).** Measured with Claude Code 2.1.295: a hook in the repository's `.claude/settings.json` runs inside `claude -p` (a repository an agent works in could therefore run commands the human never approved, with the agent's environment); `--setting-sources user` stops it. Drydock starts Claude agents with `--setting-sources user` (`Adapter.UserSettingsOnly`), so the human's own user settings (login, preferences) apply and the repository's do not. That flag also stops Claude Code loading the project's memory files (CLAUDE.md, the AGENTS.md it imports, `.claude/rules`; measured with 2.1.296, found in the 2026-10-09 full pass): the adapter therefore reads those files from the working folder (imports followed only inside it, settings files never) and appends their text to the system prompt (`internal/agents/claude/project_memory.go`). Agents also start with a scrubbed environment (`proc.ScrubEnv`: a small allowlist plus `CLAUDE_*` and `ANTHROPIC_*`; names added by the human in `agent.pass_env`, names only) so `GITHUB_TOKEN`, cloud keys and the like are not inherited; verified that the CLI still authenticates. This is **isolation by configuration, not an operating-system sandbox**: measured with `acceptEdits`, reads, writes and shell commands outside the task folder are refused, but a permission mode that skips approvals removes that, which is why imports lower such modes (P14). The agent's page states this as "guarded" or "open".
- **P17. Claude working-practices profile (2026-10-08).** Every Claude agent gets, automatically and unless the human switches it off for that agent, a short role-specific text of working practices (worker, reviewer, planner) added to its system prompt before its purpose: derived from Anthropic's published guidance for agent teams (docs/claude-best-practices.md lists each source and how it is applied), versioned (`PracticesVersion`, recorded in the `agent.started` event so a run shows which version it followed), and kept short because every added token is paid on every turn. New Claude agents also get role presets (workers: sonnet, medium effort; reviewers: sonnet, high; architects/planners: opus, high) that the human can change. Other adapters get no practices text: it was written for, and measured against, Claude.
- **P18. Agent templates (2026-10-08).** A working agent's setup (adapter, model, effort, permission mode, purpose, practices on/off, library servers to grant) can be saved as a reusable template and applied into any project (`agenttemplate.*`). Applying creates an ordinary agent, grants the listed library servers (which puts a server into the project if it was not there yet, as dropping a server onto an agent does; one that no longer exists is reported as skipped) and picks a unique name. Templates are workspace data and are never exported with a project.
- **P19. Parallel work: isolation, waiting, sync, stale-proof merge (2026-10-08).** See `docs/parallel-work.md`. Decisions: (a) worktrees are the isolation guarantee; (b) a task waits (BLOCKED, with the reason) while an EARLIER active task (started first) has an overlapping footprint (planned files plus observed changes), the scheduler does not start a task whose planned files overlap a running one, the human can release a waiting task, and the project can switch the guard off (`collisionGuard`, default on); (c) the base branch is brought into a task's branch and the checks run again before review and before merge; a conflict is handed to the task's own worker; a branch that is behind is never merged; (d) a change a human approved is remembered by its git patch id and merges after a clean sync without a second approval, any difference asks again; (e) diffs compare with the merge base; merges to one repository are serialized; (f) a reopen caused only by the base moving (`TransitionOpts.Resync`, system only) does not use up an iteration. The guard cannot see future or semantic collisions; the sync and the re-run checks are the backstop (parallel-work.md section 3).

- **P20. MCP for the other agent CLIs is the human's opt-in (2026-10-09).** The gateway reaches Codex, Gemini, OpenCode and Goose only if the human allows it for that CLI (`mcp.try.set`; default off), through ACP `session/new.mcpServers` as the stdio shim, and every such session is marked unverified in `agent.mcp` and in the manifest. The reason it is not simply on: the only proof that exists is against a scripted ACP agent, which shows Drydock builds and passes the entry correctly and that an MCP client can use it, not that any vendor CLI honours it. Claude Code stays on by default because real runs prove it. Antigravity has no known mechanism and cannot be allowed. A bug found on the way: the Codex configuration reported its transport as direct HTTP while the file it applied used the shim.

- **P21. Software versions are measured and published (2026-10-09).** `cmd/drydock-audit` measures every piece of third-party software (Go toolchain and libraries, UI packages, CI actions, agent CLIs and ACP bridges, tools) against the newest release, scans for known vulnerabilities (govulncheck, npm audit) and compares the documentation with the code; the result is `docs/software-versions.json` and the Artifact "Drydock Software Versions". Any change that adds or updates software refreshes both (`docs/software-versions.md`, `CLAUDE.md`). The policy is to stay as current as is safe, with a written reason (`hold` in `docs/software-notes.json`) for anything deliberately behind.

- **P22. OAuth sign-in for web MCP servers (2026-10-09).** Servers that want an account (Linear, Notion, Sentry and the like) can be connected with OAuth through the official MCP SDK's authorization-code handler instead of being hand-rolled: the human signs in in their own browser, Drydock keeps the tokens only in the credential vault and refreshes them, the gateway fetches the access token per request, and agents never see any of it (`docs/mcp.md` "OAuth sign-in"). Choices: the page only links the server's sign-in address when it is https (or local for a local server); the flow's network requests refuse addresses inside the user's network; one answer per flow; sign-out and any change of the server's address delete the stored sign-in. No pre-registered client entry yet, so a provider without dynamic client registration cannot be used. Migration `00094_mcp_oauth.sql`. New dependency `golang.org/x/oauth2` (the SDK already required it).

- **P23. Everything moved to the newest release (2026-10-09).** On the owner's instruction ("upgrades, all of them") the Go toolchain (1.27.2), every Go library, every UI package (React 19, Vite 8, Vitest 5, TypeScript 7, jsdom 29, Playwright 1.64, marked 18), the CI actions (checkout, setup-go, setup-node, upload-artifact at v7) and the agent pins that could be checked here (Claude Code 2.1.295 verified by the conformance suite against the real CLI; Goose 1.54.0 and the Claude ACP bridge 0.88.0 by a live ACP handshake; Codex 0.162.0 by version and login status only) were updated together, with the full Go, vitest and browser suites passing. Left behind on purpose: `@types/node` follows the Node line the project is on (22) and Node itself is the sandbox's. Libraries that are only in the module graph and not compiled into the app or its tests (about 150, mostly through gitleaks) are listed on the Software Versions page under their own heading and are not chased. The GitHub Actions bumps and the new Go version have not run in CI (decision P2).

- **P24. Claude workers may run the project's own checks, if the project says so (2026-10-09).** Approved by the owner after `docs/worker-commands.md`. A per-project development setting, **off by default** ("Let Claude workers run this project's own checks", `workerChecks`; only a human identity can change it), gives a Claude **worker** turn (the plain work node and the template implementation node) `--allowedTools` entries that are exact command lines from one fixed list (`checks.WorkerRules`): `go build|test|vet ./...`, `npm run <build|test|lint|typecheck> --silent`, `task <build|test|lint|typecheck>`. Each rule is the whole command line, nothing appended, no prefixes, no wildcard. The worker gets the whole list, so a `go.mod`, script or Taskfile target it creates during the turn is runnable; its system prompt names the commands Drydock will run after the turn (found with the same detection Verify uses, `Deps.Checks` or `checks.Detect`, limited to the list) and says to type them exactly. Reviewers, planners, room replies and other CLIs get none. A project import switches it off. Exposure: these commands run the project's own code, which the worker can edit in the same turn, so **with the switch on a worker can in practice run any program with the user's rights** (git, downloads, shells); the exact rules only stop it typing other commands directly. There is no operating-system sandbox on Windows (docs/security.md). The settings note and the agent's isolation panel say this. **Not verified:** that Claude Code's exact-command rule matching treats `npm run test --silent` the same on Windows, where `npm` is `npm.cmd`; check on the first Windows run before relying on it there. Tests: `internal/checks/workerrules_test.go`, `internal/pipeline/workerchecks_internal_test.go`, `internal/core/worker_checks_test.go` (real argument list and system prompt of worker and reviewer turns, with and without the switch; isolation panel), `internal/projectio` (import switches it off), vitest, `round3.spec.ts`.

- **P25. The UI moves from React to Remix 3, with a new design (2026-10-09).** On the owner's instruction ("swap out React for Remix 3 ... rebuild the whole UI ... something that models Discord and Slack very well, maybe a bit of a Linear look"), after an audit of the Remix 3 repository and its agent skills. This supersedes the brief's stack rows that name React, React Flow and cmdk (brief section 2 "Desktop shell" and "UI libraries", and the architecture line). Choices: `remix` 3.0.0 components (JSX, not React) and its SPA router (`remix/routes`, `remix/router`, `remix/spa`) with path routes and the browser Navigation API, which WebView2 has; the Go side serves `index.html` for app paths (`internal/uiassets`, both the Wails shell and the dev transport); `@remix-run/ui` 0.12.1 headless widgets (pinned exactly, Remix calls it unstable) wrapped in our own kit; our own SVG workflow graph instead of React Flow and our own command palette instead of cmdk; Lucide icons and the Inter font bundled for offline use; plain Vite 8 builds it (Remix has no bundler), with Pitlane's Remix plugin (`@pitlane/vite-plugin-remix` 0.9.0, SPA mode, chunk import map off for the CSP) for component hot reloading in development, added on the owner's suggestion; `remix test` runs unit tests in Node and component tests in a real Chromium page (it needs Node 24.3+, so the UI tooling and CI moved to Node 24 LTS; decision D9's Node 22+ for the user's agent bridges is unchanged). Removed: react, react-dom, @vitejs/plugin-react, @testing-library, jsdom, vitest, @xyflow/react, cmdk. Kept: Monaco, xterm, marked (lexer only; no HTML is ever rendered), the API client, the event store and the CSP. Conventions are in `ui/app/README.md`. The owner approved the design from the first screenshots. **Not verified:** WebView2 on Windows (Navigation API interception, the Go fallback on a reload, Monaco workers under the asset server).

- **P26. Orchestration fixes and the Team Lead (2026-10-09).** An audit found ten gaps in how runs keep agents on task; all ten are fixed, and the owner's "one agent that manages the entire team" is built as the Team Lead (`docs/workflow-engine.md` sections 4, 9 and 13; `docs/state-machines.md` sections 1, 5 and 6). Choices: (a) **every work pass carries the task** (title, description clipped on rework, criteria) and only the latest feedback; human guidance is rendered as text. Token rule 16 still holds: no previous-session history. (b) **The accepted spec** (without its criteria) and the design notes go to every builder pass, bounded. (c) **Limits are a real decision**: every task workflow ends its loops in `escalate -> limit -> resume/abort`; a human continue resets all loop counters of the run and gives 5 more review rounds; stop cancels the task. Repeated near-identical REVISE feedback (2 rounds, similarity 0.8) escalates the same way. The engine gained `DeliverAnswer`, `Halt` and `ResetLoops` (backward compatible); the graph changes alter topology hashes, so a run parked by an older build cannot resume (its old `limit` dead-ended anyway). (d) **Watchdog pauses** re-run the node with the answer: guidance reaches the agent, a reject cancels the task and ends the run; thresholds per node (`ForNode`), handoff artifacts and resets are wired. (e) **Reviewers** see 48 KB of diff and are told when it is truncated (with a per-file stat); the verdict fallback is strict. (f) A task without criteria asks the worker for a 1-3 bullet definition of done that the reviewer checks against. (g) The project brief is a hook (`Deps.ProjectBrief`, no column in this change) used by the prompts and by AGENTS.md (`Engine.Standing`); the planner sees open tasks and the team with adapters. (h) **The Team Lead** is an agent with the role `lead`. Drydock, not the model, decides when it is asked (watchdog stage 1, iteration limit, repeated REVISE, no progress for `leadStallMinutes`, default 15, unassigned task under automatic scheduling); the lead decides what, in one strictly validated JSON object (nudge, reassign, split, escalate, continue; one retry, then escalate). It runs read-only through the normal runtime, so purpose, practices (its own short text, `PracticesVersion` 2) and budgets apply; at most 3 interventions per task and run, one in flight per task, never on its own turns; every intervention is stored (`lead_interventions`, migration 00103, not exported), posted by the lead and published (`lead.intervened`). A split continues the current task with the lead's message as its narrowed scope (deciding to stop a task stays a human's call). `continue` means "no intervention": the default behaviour applies, so at a limit the human is still asked. Without a lead nothing changes. Stand-up: `lead.standup` posts a status in #general. **Not verified:** a real model as the lead (all tests use the labelled scripted agent); lead turns take no parallel-agent slot.
- **P27. Prebuilt agents (2026-10-09).** On the owner's instruction ("a few Prebuilt Agents for Claude and one in particular that manages the entire team ... for UI with Skills from popular sources ... Cloudflare Docs ... Microsoft Docs/Learn ... and anything else you can think of", and the choice of Claude Code or Gemini per agent). Drydock ships an embedded catalog of ready team members (`internal/agentcatalog/catalog.yaml`): a Team Lead supervisor (role `lead`, Claude Opus high: keeps the team on task, makes sure tasks finish with evidence, stops loops, escalates), Product Planner, Software Architect, UI Engineer, UI & Accessibility Reviewer, Cloudflare Engineer, Microsoft & Azure Engineer, Backend, Database and DevOps Engineers, Code Reviewer, Test Engineer, Security Reviewer, Technical Writer, MCP Server Builder and a Gemini-first Fast Generalist. Each has standing instructions (its purpose), a preferred CLI, model and effort with alternatives (the ACP CLIs' models are left to the CLI's default because they are reported at runtime), built-in MCP catalog servers that need no secret (`cfdocs`, `mslearn`, `ctx7`) and registry skills referenced by allowlisted registry, name and pinned commit (Cloudflare's, Anthropic's `frontend-design` and `mcp-builder` (Apache-2.0), Vercel's, Microsoft Learn's); no skill text is copied into the repository. The loader is strict and a test proves every reference resolves against what ships. `agentcatalog.apply` (and `applyMany` for the new-project wizard) creates an ordinary agent; with `connectMcp` it adds the servers to the library (`mcplib.addFromCatalog`, idempotent by URL) and grants them; with `installSkills` it imports each skill at its pin and assigns it to the agent (per-agent assignment, `agent_skills`). **The rule: the human choosing a prebuilt agent with "install skills" is the acceptance of those skills.** The card shows each skill's name, registry and pin before the choice, so the choice is a decision about exactly those skills, the same decision `skill.accept` records (`decided_by` is the human, the audit entry says `via: prebuilt agent <id>`). Its limits: only a skill that passed the license check and the scan is accepted (blocked stays blocked and is reported, a skill the human rejected earlier stays rejected, both as pending); `per_skill_review` registries (Anthropic) count as reviewed by this choice, which is why the catalog names only skills whose license was read (Apache-2.0); a skill the project already delivers by role to the whole team is left shared rather than narrowed to the newcomer, and an accepted skill at a newer pin is accepted again on this choice; agent templates (P18) that carry skills follow the same rule, since the human saved them. Nothing is installed or connected without the flag, a failure on one skill or server never undoes the agent, and applying is always a human action. Practices (P17) stay Claude-only. Parser change on the way: top-level front-matter keys outside the Agent Skills format are ignored and recorded instead of refused (Microsoft Learn's skills carry Claude Code's `context: fork`), and a registry skill whose front-matter name differs from its directory (Vercel's) is stored under the directory name. Migrations `00100_agent_skills.sql`, `00101_agent_template_skills.sql`, `00102_skill_parse_notes.sql`. **Not verified:** the registries themselves at those pins through the real GitHub (the SKILL.md files were read from local clones and pass Drydock's parser and scan; the tests use a protocol fixture); the real docs servers; whether Gemini, Codex, OpenCode or Goose read delivered skills the way the delivery table expects; the Team Lead's behaviour in the pipeline (its supervision loop belongs to the pipeline work, not to this catalog).
- **P28. Projects without a repository, the project brief, and the new-project wizard (2026-10-09).** On the owner's instruction ("make a project without being forced to do a repository path ... add that after"; "a wizard, choosing connectors, choosing which GitHub repo, making your agents"). (a) A project can be created with a name only (`project.create` with an empty `repoPath`, `project.setup` with `repo.kind = none`, `project.import` without a repository). The team, room, tasks and planning work; everything that changes code (`task.start`, `task.startTemplate`, the scheduler, pull-request delivery, diffs) refuses with "this project has no repository yet: attach one in the project's settings first". Room replies and the terminal run in a private folder (`<data>/projects/<id>/workspace`), never in the app's own working directory; the repository skills scan does nothing (a relative path would have read the app's folder). (b) `project.attachRepo` links a repository once; it is **never swapped** afterwards because task branches, worktrees and merges belong to it (a different repository is a different project). (c) The **project brief** (`project.setBrief`, at most 8,000 characters, secret-scanned and redacted before it is stored) is the human's description of the project, given to every agent as standing context; bounded because it is paid on every turn. (d) GitHub repository choice uses the user's own `gh` (no token stored): `github.status`, `github.owners`, `github.repos` (own, collaborator and organization repositories, most recently pushed first, archived ones left out, at most 500) and `github.clone` (`gh repo clone owner/name <folder>/<name>`, default folder `%USERPROFILE%\source\repos`; the name is validated as owner/name so no URL or path can be smuggled in; a failed clone's folder is removed; a non-empty destination is refused). (e) `project.setup` is the wizard's single call: it validates everything it can before creating anything, then makes the project, and every later step (brief, pull-request mode, connectors, agents, skills) that fails becomes a warning in the result instead of undoing the project, so the human gets a usable project and a list of what to fix.
- **P29. Chats and Cline (2026-10-09).** On the owner's request ("a section where I use Claude Code CLI: open new chats, keep chat history, and a previous chat can be read / continued"; "Cline as an agent CLI"). **Chats** (`internal/chats`, `internal/core/ext_chats.go`, contract in [`chats.md`](chats.md)): (a) a chat is the human's own conversation with one adapter (Claude Code by default), stored with every message, tool call, refusal and usage, and is not a project record (never exported, `projectio.ExcludedTables`); (b) it runs in its project's repository root (or the project's workspace folder when it has no repository), or in an absolute folder the human picked, or, with neither, in a private folder under the data directory; (c) each turn is one `Runtime.Spawn` (agent id = the chat id, so `agent.*` events, MCP attach and `model_usage` apply; `agent_runs` rows are not written because that table's `agent_id` references team members, so `chat_turns` keeps the run id instead) that continues the CLI's own session (`--resume`, ACP `session/load`) and is stopped after the turn; (d) **one turn at a time**: a second send is refused, not queued; (e) when the session cannot be resumed (an error before any session or output, as claude 2.1.295 prints for an unknown id), a fresh session gets the last ~12,000 characters of the chat as a quoted transcript ("the earlier conversation in this chat", every line prefixed `> `, data not instructions) and a system note says so; a fork (messages copied up to a point, no session) and an adapter without resume start the same way; (f) other chats reach a turn two ways: attached by the human to one message (bounded, oldest trimmed, labelled with title and date, quoted as data), or through a **read-only `chats` workspace tool** (list, search, read) given only to chat sessions whose "read history" is on (default on), scoped to the chat's own project plus chats without a project (a chat without a project sees all), and to nothing else of the workspace (no team tools, no library servers); (g) permissions: the chat's mode is the adapter's (Claude default `default`, so headless Claude refuses tools outside it and the refusal is stored with the tool and input), plus per-chat Claude `--allowedTools` rules validated before storing (one line, at most 200 characters, no commas, no nested parentheses, no bare `Bash` or Bash wildcard, no chaining, pipes, redirection or substitution in a Bash rule) with a suggested exact rule offered for each refusal; a mode that skips approvals is only ever the human's explicit choice for that chat, is shown (`skipsApprovals`), and is not inherited by a fork; ACP agents' permission requests are refused by the application-wide handler and shown as refusals, with no allow rules (only Claude honours them); (h) every stored body, title and tool input (bounded JSON, 4 KB) passes the secret scanner first, `chat.delta` is flushed at whitespace so a token arriving in pieces is still redacted, and the human's message is redacted before the agent sees it; (i) usage adds up per chat; cost is null when no turn reported one and turns without a reported cost are counted (P10b); (j) `chat.create` and `chat.send` are refused while the license is read-only; a turn a crash left running is marked failed at start and never re-run; (k) the automatic title is the first line of the first message (60 characters), no model call. Migration `00106_chats.sql`. Verified with the real Claude Code 2.1.295 on Linux (haiku, `TestRealClaudeChatResumesFallsBackAndShowsRefusals`): the resumed session remembers the first turn, a session id the CLI does not have falls back to the transcript and still remembers, a `touch` through Bash is refused in `default` mode and shown with the suggested rule `Bash(touch drydock_probe.txt)`, and with that rule allowed the next turn runs it. The Claude parser now reports `permission_denials` as `permission_denied` chunks and the `errors` of a failed result, both from captured real 2.1.295 output. **Cline** (`cli/cline.yaml`, Apache-2.0): driven over `cline --acp` by the generic ACP client, pinned at 3.0.70, checked live on Linux (version, `initialize` with `loadSession` and image prompts, auth methods `cline, cline-pass, openai-codex`, `session/new` answering authentication required; compat `inspected`); `npm i -g cline` with Node 22+ because its npm launcher is a Node script (D9); never started with `--auto-approve` or `--yolo`; no ACP usage or cost (unknown); MCP only by the human's opt-in through `session/new.mcpServers` (P20), and marked unverified because Cline 3.0.70 stores that list without reading it. Not verified: any real Cline prompt turn (no sign-in here), a real ACP chat turn (only the scripted ACP agent), chats on Windows.
- **P30. A container per task for the agents' commands (2026-10-10).** On the owner's request ("a sandbox per task with full freedom inside"), after the honest answer that Drydock could not build a real application while a worker either runs no commands or runs the project's code on the user's machine (P24). Design and threat model in [`sandbox.md`](sandbox.md). (a) A per-project switch of its own (`sandbox:<project id>`, **off by default**, human identity only, recorded with who and when, never carried by an import) makes every task of the project get containers on the machine's Docker-compatible engine, driven only through the `docker` command: a workspace with the task's worktree at `/workspace`, the services the definition lists on an internal network, and by default an egress proxy (`cmd/drydock-egress`, built from this repository, imported locally with `docker import`, no registry) as the only way out, which allows listed hosts on ports 80 and 443 and refuses names that resolve to private, loopback or link-local addresses. (b) The definition is `.drydock/sandbox.json`, read from the **default branch only**, so an agent's change to it counts after a human merged it; unknown fields are refused. (c) A Claude Code worker turn (plain work node, template implementation node, template roles that write) starts with `--restricted` and `acceptEdits` (restricted mode removes every tool that runs commands or code and refuses `bypassPermissions`) and runs commands only through five gateway tools (`sandbox_run/start/logs/stop/info`) that `docker exec` in the workspace; restricted mode does not load CLAUDE.md, so the adapter appends it (contract CLAUDE-RESTRICTED-02). Its environment sets `CLAUDE_AUTO_BACKGROUND_TASKS=false`, `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` and `MCP_TOOL_TIMEOUT=3900000`, because Claude Code 2.1.296 otherwise moved a tool call past 120 seconds to the background and ended the turn without its result. (d) The project's checks run in the container; the in-process secret scan and dependency audit stay on the machine; the e2e check and the preview do not run on the machine in a sandboxed project (recorded as skipped, buttons refuse). (e) A sandbox that cannot start becomes a question for the human; another CLI's worker turn becomes a question too (its ACP terminal is not routed into the container). (f) The sandbox is removed when the task completes, fails or is cancelled, and leftovers are pruned at start; Drydock touches only objects carrying its own labels. **Measured** on Linux with Docker 29.8.2 and real Claude Code 2.1.296 (claude-haiku alias): the worker had no shell, ran `go test ./...` and a 130-second command through `sandbox_run`, the checks passed in the container, the change merged and the sandbox was removed (`TestRealClaudeWorksInTheSandbox`). **Not verified:** Docker Desktop on Windows (bind mounts of Windows folders, CRLF checkouts, its host gateway on an internal network, speed); the release build must ship `drydock-egress-linux-amd64` and `-arm64` next to `drydock.exe`. Tests: `internal/sandbox` (real engine, offline images from `internal/sandbox/sandboxtest`), `internal/sandbox/egress`, `internal/pipeline/sandbox_internal_test.go`, `internal/mcp/workspace_sandbox_test.go`, `internal/agents/claude/sandboxed_test.go`, `internal/core/sandbox_test.go`, `internal/core/real_claude_sandbox_test.go`, `ui/app/screens/project/project-sandbox.test.browser.tsx`.
- **P31. CI is a hard merge gate; branch protection is read before a merge, never bypassed (2026-10-10).** On the owner's request ("CI as a hard gate"). In pull request mode Drydock never merges a commit that no CI tested on its own judgement: the `ci` node counts check runs **and** commit statuses and waits by name for the checks the base branch's protection and rulesets require (a 403/404 on the admin-only protection endpoint falls back to every check that reports, noted in the evidence). No CI within 2 minutes, a required check that never reports, or checks still running after 30 minutes become a Needs-you question with three answers: check CI again, merge without CI this time (`{"waive_ci": true}`; evidence "CI waived by human:<name>"; only a `human:` identity, only that commit, never over a failed check), or stop. The answer is read from the question's approvals row, because the watchdog's check middleware takes answers delivered into a check node's inbox as its own. A per-project setting **CI before merge** (`ciMode`, default `required`) is stored like `workerChecks`: only a human identity can change it and an import sets it back to `required`; `none` is a human's declaration that the repository has no CI, recorded with who and when (`ciNoneBy`/`ciNoneAt`), after which a commit nothing reports on passes with evidence "CI declared absent by <who> on <date>" while checks that do report still count. Before merging, the merge step reads GitHub's `mergeable_state` and does not merge a pull request that is blocked, behind, conflicting, a draft or unknown (a question: check again or stop); the merge request carries only the method, the reviewed head SHA and the commit title, and Drydock never uses an administrator override (`gh pr merge --admin`). The task page has buttons for both questions (`ui/app/screens/tasks/merge-gate.tsx`). Tests: `internal/git/host/ci_test.go` (a labelled test double for `gh` that records the exact merge arguments), `internal/pipeline/cigate_test.go`, `internal/core/ci_gate_test.go`, `internal/projectio`, the UI browser tests, `ui/e2e/round3.spec.ts`, and the env-gated `TestRealGitHubMergeThroughGh` against a real throwaway repository. **Not verified:** real GitHub (until that test runs), what the branch view shows non-administrators about required checks, and Windows.
- **P32. Windows proof without GitHub-hosted runners (2026-10-10).** On the owner's request ("Windows proof on every push"), because P2 still holds. Two ways, both documented in [`windows-runner.md`](windows-runner.md): (a) a **verification bundle** (`go run ./cmd/drydock-winbundle`, `task verify:bundle`): Drydock's own test binaries compiled for Windows, `drydock-e2e.exe`, `drydock-verify.exe` and, by default, a Go toolchain, in one zip the owner extracts and runs on the PC (`.\drydock-verify.exe`, optionally `--with-claude`, about one dollar, and `--with-ui`); it runs the machine facts, a whole task through the real core (`core-smoke`), ConPTY (`core-terminal`), process cleanup at shutdown (`core-shutdown`, `tasklist`) and the Windows-relevant packages' tests, and writes `drydock-verify-<date>.zip` with a Markdown and a JSON report and every step's log; (b) a **self-hosted runner** on the PC: setting the repository variable `WINDOWS_RUNS_ON` to `["self-hosted","windows","x64"]` points every Windows job of `ci.yml`, `windows-probes.yml` and the new `verify.yml` at it, so each push is tested on Windows; `verify.yml` runs on push only while that variable is set, so it can never use GitHub-hosted minutes by surprise. CI sets `DRYDOCK_NO_REAL_AGENTS=1`, so a runner with a signed-in `claude` does not spend the owner's money on every push. **Measured** on Linux only: the same bundle built for Linux and run with no Go on PATH passed 23 steps, 0 failed, 2 skipped (opt-in); a bundle without Go on a machine without Go skipped the Go-dependent tests with that reason instead of failing. **Not verified:** anything on Windows (tasklist, registry facts, ConPTY, Job Objects, `claude.cmd`), the workflows on GitHub, and the runner registration steps. The owner must run the bundle or register the runner; nothing here can. The Credential Manager test used to delete Drydock's own key-encryption entry when it finished, which on a PC with an installed Drydock would have made its stored provider keys unreadable; it now deletes that entry only when it created it (found reviewing the merge, before anyone ran the bundle). Tests: `internal/winverify` (including the opt-in `TestBundleEndToEnd`), `cmd/drydock-verify`, `cmd/drydock-winbundle`.
- **P33. Contract tests against the real agent CLIs; a CLI version is adopted only after they pass (2026-10-10).** On the owner's request ("real-CLI contract tests, nightly and on every CLI update"). Every assumption Drydock makes about an agent CLI is a named contract (`internal/contract`: id, CLI, the assumption in one sentence, why and where Drydock depends on it, the tests that check it; [`contract-tests.md`](contract-tests.md)), run against the real CLIs by `cmd/drydock-contract` nightly, on demand and on adapter changes (`.github/workflows/contract.yml`; haiku; a budget cap; one secret, `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token` or `ANTHROPIC_API_KEY`) and whenever `--check-updates` sees a newer npm release. **The rule: a CLI version is adopted only after the suite passes on it** (`--check-updates --latest`); a version that breaks a contract is not adopted and Drydock keeps the previous one (`versions.recommended` or the bridge pin, plus `blocked` in `compat.yaml` naming the contract) until the adapter is fixed, and a warning (a check that passed but saw a change) needs a decision before the pin moves. Free checks (flags with an empty stream-json input, ACP `initialize`, a static scan that every plan-mode request is read-only) run first; paid checks book the spend the CLI reports in a ledger, stop at the budget, and give each claude they start `--max-budget-usd`. The first run on Claude Code 2.1.296 failed three contracts, all Drydock's: (a) a result's `total_cost_usd` and `modelUsage` are running totals of the process, so a process answering several turns was over-counted; the parser now records the difference (CLAUDE-USAGE-02), and because those totals belong to the session, a turn resumed in a new process (every chat turn after the first) also counted all earlier turns: the parser now reports its running totals with each usage record and a chat passes them back as the next process's baseline (`chats.cli_usage`, migration 00108; CLAUDE-USAGE-03, measured 5,294 = 2,615 + 2,679 tokens recorded once); (b) `--strict-mcp-config` does not keep installed plugins' MCP servers out (31 joined, their tools offered); every non-restricted session now gets `--disallowedTools mcp__plugin_*`, which keeps their tools out while the gateway stays connected (`--safe-mode` would drop the gateway too) (CLAUDE-MCP-01); (c) the enclosing session's `CLAUDE_CODE_REMOTE_SESSION_ID` reached agents; stripped (CLAUDE-SESSION-01). Also: Claude Code starts a session even when an MCP server fails and says so only in `system/init`, so a session given Drydock's gateway now stops its turn with a clear error when the gateway did not connect (a sandboxed worker would otherwise have no way to run a command); and denying plugin tools cut a turn's context on this machine from about 8,850 to about 2,600 tokens. After the fixes: **16 passed, 0 failed, 4 skipped** for Claude Code ($0.036), and USAGE-02 and the new USAGE-03 passed again after the session-totals change. Pins moved under the rule: Claude Code 2.1.296 (suite passed), the Claude ACP bridge 0.89.0 (ACP-INIT-01 passed on it); Codex 0.162.1 is held at 0.162.0 because no contract runs Codex itself here. **Not verified:** the workflow itself (P2), Windows (WIN-ARGV-01), Gemini and OpenCode (not installed here), and every ACP prompt turn (no credentials, D1).
- **P34. A head-to-head trial against plain Claude Code (2026-10-10).** On the owner's request ("a head-to-head trial against plain Claude Code"). `cmd/drydock-trial` runs each task of `evals/tasks` in two arms from the same start commit with the same model, prompt, CLI version, dollar ceiling, wall-clock cap and trimmed environment, interleaved: **D**, Drydock's plain workflow through the core's own methods (worker and independent reviewer, the project's checks, the per-task sandbox), and **B1**, one plain `claude -p` session with Bash allowed on the machine (docs/evals.md section 5). Hidden acceptance tests the agents never see (`evals/testdata/hidden`, each proven to fail on the fixture and pass on a reference solution) judge both, and stand in for the human's accept at D's merge approval; any other question Drydock asks the human ends a D run as failed. Each run is recorded under `evals/results` (run.json, the arm's diff, and B1's CLI stream or D's events, usage rows and check evidence), and the report shows raw rows with medians and ranges only, never a mean or a significance claim (docs/evals.md section 1). **Rule:** no claim that Drydock beats or matches plain Claude Code is made from fewer tasks than section 6.1 asks (20) or fewer than three repeats each; until then a trial is a pilot and is reported as such. **Pilot (2026-10-10, haiku, four tasks, k = 3, D in checks mode because this environment's container engine stopped):** both arms 12 of 12 accepted; D cost $0.0229 per run at the median against $0.0165 (all runs $0.3255 against $0.1972) and took 1m22s against 1m8s. On these tasks the review loop added cost without changing the outcome; the suite needs harder tasks to show where it helps (docs/evals.md section 13). **Hard suite (2026-10-10, `tally`, 23 tasks frozen before the first run, haiku, k = 3, D in checks mode; docs/evals.md sections 13.1 and 13.2):** D 68 of 69 runs accepted, B1 66 of 69; task by task D better on 2, worse on none, equal on 21 (exact sign test p = 0.5); cost per accepted task $0.1331 against $0.1318, median wall time 4m7s against 3m13s. This trial meets the rule's size and supports no claim either way: on precise tickets one plain session did about as well, and every rejection was a rule its prompt stated. Telling the arms apart needs tasks one session fails often (implicit rules, larger changes, a weaker model). Tests: `internal/trial` (both arms end to end with the scripted stand-in, the sandboxed arm against a real engine, the suite, the hidden checks, the report), `cmd/drydock-trial`.
- **P35. Skills are chosen when an agent is made, and every skill says who gets it (2026-10-10).** On the owner's request ("When we create our agent, we choose the model and effort. Can we put the skills into our agent creation and or maybe we have a template one where it will pull the skills and use them? Or does it need to inject itself in with every new agent?", then "do all 3"). Nothing has to be injected per agent: accepted skills were already written into each task's worktree at the start of every task for the agents they apply to. What was missing was choosing them. (a) The new-agent and edit-agent forms have a Skills section listing the project's accepted skills; a skill the whole team gets for the agent's role is shown as included. `agent.create` takes `skills` and `agent.update` takes `addSkills` and `removeSkills`, each checked before anything changes, and the editor sends only what the human changed, so a skill it does not show (one waiting for a decision) is never taken away. (b) The agent template form lists the template's registry skills and adds from those Drydock knows (`agenttemplate.skillChoices`: the prebuilt agents' skills and the registry skills imported into a project here, from allowlisted registries, blocked and rejected ones left out; since P96 also every usable skill of the workspace library). A save that does not send `skills` keeps them: the Library's form dropped a template's skills on every edit before. (c) Every skill has an audience (`skills.audience`, migration `00109`): `team` (the roles it names, agents added later included, plus agents given it) or `agents` (only those). The Skills page shows who gets each skill and changes it (`skill.audience`). **This replaces P27's narrowing rule.** Giving a skill to an agent used to send it to the assigned agents only, so giving a QA skill to a backend agent took it from QA, and installing a prebuilt agent whose skill the team had for other roles left it pending with "assign it on the Skills page", a control that did not exist. Now giving takes the skill from nobody and the install gives it; a skill new to the project still goes to its agent only. The migration starts every skill that had an assignment as `agents`, so nobody gained or lost a skill on upgrade. Safety is unchanged: only accepted skills are delivered, giving a skill never accepts it, and these are human actions (P11, P27). Tests: `internal/skills` (`Reaches`, `SetAudience`), `internal/storage` (the migration), `internal/core` (agents made and edited with skills, templates keeping and offering skills, prebuilt installs sharing and adding), `internal/projectio` (the audience is exported), the UI forms and the Skills page in Chromium, and `ui/e2e/round14.spec.ts` against the real core. **Not verified:** on Windows by hand. The template form offers only skills Drydock already knows; to carry another registry skill, import it on a project's Skills page first (since P96 it also offers every skill of the workspace library).
- **P36. Every Claude Code session runs quiet, and nothing spends tokens unless a person asked (2026-10-10).** On the owner's request ("make sure when we add claude code either by API and or CLI that it's not burning tokens"). Adding Claude Code spends nothing (detection and provider checks never prompt); subscription and API-key use run the same CLI with the same settings. Every session Drydock starts gets `claude.QuietEnv` (no auto memory, workflows, advisor, cron, background tasks, idle check-ins, prompt suggestions or away summaries; inherited values replaced), `--disable-slash-commands` (the person's skill list, 9,200 tokens here, no longer in every request; Drydock's own skills reach Claude as rules) and `claude.UnattendedDenied` (tools that schedule turns, reach past the session, change the Claude Code setup or leave the worktree). Measured with 2.1.296 on haiku: the first request of a session fell from 27,463 to 9,846 tokens, and a session in a second worktree writes 2,673 new tokens to the cache instead of 12,200; the whole Claude contract suite passed with them (20/0/3). Contracts CLAUDE-QUIET-01 (free) and CLAUDE-TOKENS-01 (paid) keep both true on every CLI version. Spend nobody asked for is closed: a failed team-lead turn counts toward its bound and what the lead was asked survives a restart; an agent turn cut off by stops is re-run once on its own, then a person decides; Test connection's prompt is off by default; Drydock's Go tests that prompt the real claude run only with `DRYDOCK_REAL_CLAUDE=1`; the Connection says plainly that an `ANTHROPIC_API_KEY` in the environment bills every agent turn per token, also for a subscriber. Measured and left: `--exclude-dynamic-system-prompt-sections` (no effect across worktrees), `CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS` (147 tokens, removes git safety rules); not changed yet: path-scoped rules and skills are appended in full rather than on demand (to be measured for quality first). `token-control.md` "As built: Claude Code sessions".

## 4. Locked versus open

Locked: everything in section 1 except D5 and D7 proof, and the stack rows in section 2.

Open, owner action:
1. ~~Fix GitHub Actions for the private repository.~~ **Done 2026-10-10:** the repository was made public, which grants free Actions minutes, and `windows-probes`, `ci` and `verify` run on GitHub-hosted Windows runners. Registering a self-hosted runner on a public repository is refused (`docs/windows-runner.md`); the verification bundle on a PC remains available (P32).
2. Delete the leftover remote branch `claude/diag-actions` (the git proxy here refuses remote deletes).
3. Rotate the sandbox-injected tokens.
4. Confirm with Anthropic or counsel the position in D1 before shipping Claude subscription support; confirm Google's position for D2 if shipping Antigravity commercially.
5. Provide an OpenRouter key and a paid private cheap model preference when the owner wants D4's live probe run.
6. Try the WSL run mode (P41) on the PC: install Claude Code inside the distro and sign in there, run Test connection on its card with the tiny prompt, and run one task with an agent set to run in WSL, once with the repository on the Dev Drive and once with one inside the distro.
7. **The license of the source (audit 2026-10-16, M20).** `LICENSE` is the BSD 3-Clause License, and the repository has been public since 2026-10-10; a grant made by publishing cannot be taken back for the commits already out. `THIRD_PARTY_NOTICES.md` used to call the product "proprietary commercial software"; it now says what `LICENSE` says (the signed license tokens of `internal/license` are a feature of the packaged app, not a license of the source). If later releases are to be sold on other terms, the owner chooses: keep BSD and sell what the license does not give away (signed builds, updates, support), or change `LICENSE` for new commits (which does not reach what is published). Nothing in the code depends on the answer.
8. **GitHub settings only the owner can make (audit 2026-10-16, M19, L52, L55).** Create the environment `contract` (Settings, Environments; deployment branches: `main`; add a required reviewer if every paid run should be approved), put `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` in it and delete any repository secret of the same name (`docs/contract-tests.md`); turn on private vulnerability reporting (`SECURITY.md` points to it); decide whether the old eval transcripts, which held the account's plugin and connector names until they were redacted in the tree, should also be removed from the repository's history (a rewrite of a public repository, so not done here).
- **P37. Skills, memory, outside text and MCP tools: give agents what fits, measure it, and hold outside input to one standard (2026-10-10).** On the owner's request (after a review of whether integrations, skills and MCP servers help or hurt: "If you can work on all 5"). (1) **Measured** (docs/evals.md section 14): on the `tally` suite with haiku, two repeats, putting the twelve prebuilt agents' skills into every Claude turn cost 1.9 times as much as no skills ($0.19 against $0.10 per accepted task) with acceptance no better (43 of 46 against 45 of 46, p = 0.5); listing them instead cost the same as none (45 of 46). In a six-run follow-up the agents opened a skill written for the codebase, when it was only listed, in 2 of 6 runs. (2) **Skill delivery** is a project setting (`DevSettings.SkillDelivery`, development settings "Skills for Claude agents"): `relevant`, the default, loads into every turn the repository's own skills (`.aet/skills`; one with `paths` unless the task names files and none matches) and the imported ones whose globs match the task's file hints; every other skill is one line of `AGENTS.md` and a file in `.claude/drydock-skills` the agent reads when the work needs it. A repository skill without `paths` is recorded with fidelity `exact` (it is in every turn, as under `all`); a skill loaded on the task's file hints with `run_start`. `all` keeps the earlier behaviour. Other CLIs are unchanged. (A first version decided by walking the worktree's files for tasks without hints; a review found it slow, blind to `.gitignore`, fooled by generated and agent-made files and too generous with broad registry skills, and origin replaced it.) (3) **Memory and budget**: accepted memory goes into `AGENTS.md` newest first up to 8 KB, each entry cut to 600 characters, the rest through `search_memory`; each turn's context manifest lists the rule and on-demand skill files with their sizes and says when what Claude gets in every turn is over 32 KB. (4) **Outside text and tools**: a GitHub issue import gets the cleaning a connection's items get; the core refuses to make a task of an item or issue at or over the injection-risk threshold, whoever asks; a task imported from outside stays in BACKLOG until a human moves it on, even with automatic scheduling, and the team lead is not asked about it before that; an MCP grant that names no tools gets the server's tools as known now, by name, never `*`; listed tools whose name, description or schema reads like instructions are flagged, left out of grants and shown as held back. (5) **Live checks** per vendor (`TestLive*` in `internal/integrations`), read-only, run with the owner's tokens; not run here (no credentials). Tests: `internal/skills`, `internal/context`, `internal/core` (relevant delivery in the real loop, the over-budget manifest, GitHub cleaning and refusal, MCP grants and flags), `internal/integrations` (refusal, the live check against the fixture), `internal/tasks`, `internal/pipeline`, `internal/trial` (repository skills, prebuilt imports, reads, compare), the settings and Library screens in Chromium, `ui/e2e/round6.spec.ts`. **Not verified:** whether skills raise acceptance on tickets that leave rules to them (this suite's tickets state every rule); the origin rule is covered by tests, not by a paid run; every vendor fact until the live checks run.

- **P38. The audit's fixes, and where they change what was documented (2026-10-11).** On the owner's request ("a full pass of the app and fix anything you come across"), after the audit (`docs/audit-2026-10-11.md`, its section 9 lists every finding's status). Decisions taken on the way: (1) Drydock's git calls never run repository hooks or fsmonitor, and a change to a repository's own git configuration, hooks or info/attributes stops Drydock's git there until a person approves it (a new approval gate, `repo_config_changed`; `security.md` s3). (2) Checks, the preview command and the dependency scan get the agents' environment allowlist plus toolchain variables, not the app's environment (`security.md` s8). (3) The watchdog hashes the first and last 2 KB of a long output plus its length, still at most about 4 KB as brief §7 asks, instead of the first 4 KB alone. (4) An ACP agent that offers a model choice refuses to start on a model it does not offer. (5) A task allows at most 20 iterations. (6) `agent.output` events are deleted after 30 days and chunk events are no longer stored; the raw output and checkpoints of ended runs are compacted after 30 days and deleted after 90 (brief §18; built after the owner's "wrap these up"). (7) A Linux CI job runs the sandbox tests with Docker, a test-only exception to P1. (8) The trial harness refuses a binary built from uncommitted changes, and the comparison tool follows P34's size rule. Not built and now said so: the 70 % context handoff, the escalation handoff, Claude's permission-prompt tool, the token-control proving tests (the two handoffs and the proving tests were built since: P39; the permission-prompt tool: P40).
- **P39. A work turn continues its session below 70 % context occupancy; otherwise a fresh session gets a handoff Drydock writes (2026-10-11).** On the owner's request to build what audit F8 found missing (brief §5, `token-control.md` rules 5 and 6, `model-routing.md` s8). (1) Between two work turns of a run, the session continues (`--resume`, with the session's usage totals as the baseline) when the CLI can resume, the agent, CLI and model are unchanged and the previous turn ended below 70 % of the model's context window; a turn is never cut, the decision is taken when the next turn starts. (2) At or above 70 %, on an escalation the router marks with `Handoff` (and any other change of model or agent), after a watchdog pause, on a retry of an interrupted attempt, or when the resume fails, the next turn is a fresh session seeded with a handoff artifact. (3) The artifact is synthesized deterministically by Drydock (the task and criteria, the previous summary, diff stat and touched files, failing checks, remaining feedback, open questions), redacted, capped at 8 KB, stored in `handoffs` with the trigger as its reason; it is not asked of the outgoing agent, which would cost a turn, and it is never a transcript. (4) The decision is recorded in the work node's output, the context manifest and an `agent.session` event. (5) A CLI that reports no occupancy is never continued; Drydock's own occupancy estimate is not built. (6) The proving-test column of `token-control.md` s1 names only tests that exist. Not done: restarting a session after two corrections (the Claude best-practices advice); continuation across runs of a task.
- **P40. Claude workers outside a sandbox ask Drydock through the permission prompt tool (2026-10-11).** On the owner's request (audit F17: the tool was documented but not built). (1) A Claude worker turn in a project without the sandbox is started with `--permission-prompt-tool mcp__drydock__permission_prompt`, and the gateway offers it that tool; read-only sessions, chats and sandboxed workers do not get it (their mode, or the container, decides). (2) The analyzer answers first: what the decision table allows (fully parsed, read-only or inside the worktree; file tools inside the worktree) is allowed, a hard deny is refused with the reason, and the rest waits for a person. `exec_code` is asked rather than allowed, because a request means the CLI's own mode did not permit it (s7 allows it only when the mode does). (3) The person's answer is a security-service approval bound to the hash of exactly the tool and the input (plus the task and the agent run), not a workflow request port: the CLI's tool call is in flight, and a workflow request would end the turn. It shows in Needs you and on the task page (Allow once / Refuse), is answered through `run.respond` by the human only, and is consumed once by the waiting call; the default 30-minute lifetime is the timeout, and a run that ends refuses. (4) Every decision is recorded (`permission.decided`, the audit log) with the input's digest, never the input. (5) The request and answer shapes come from the CLI's own code (2.1.296) and the Agent SDK's types; a live turn is contract CLAUDE-PERMPROMPT-01 (paid), not run yet. See `security.md` s4 and s5, `mcp.md` "As built".
- **P41. Claude Code can run inside WSL; the worktrees live where the repository lives (2026-10-12).** On the owner's request to build out running Claude Code inside WSL 2 instead of the native `claude.exe` (whose shell tool falls back to PowerShell without Git Bash), with repositories on a Windows Dev Drive. (1) **Connections.** A Claude Code install inside a WSL distro is a Connection of its own, one per distro (key `claude@wsl:<distro>`), with its own sign-in and its own Test (`internal/connections/wsl.go`). Distros come from `wsl.exe -l -q` (UTF-16LE, decoded in `internal/wsl`); the container engines' own distros (`docker-desktop*`, `rancher-desktop*`, `podman-machine*`) are never probed. The probe runs inside the distro through the person's login shell (claude lives in `~/.local/bin`, which only `~/.profile` puts on the PATH; `~/.local/bin` and `~/.claude/local` are also looked at directly), and reads the Linux HOME and PATH, the mount root (`wslpath -u 'C:\'`, else `[automount] root` in `/etc/wsl.conf`; `/mnt` is never assumed), interop, the networking mode, setsid, git, and the CLI's own `--version` and `auth status`. Drydock never reads, copies or moves a credential between Windows and the distro (D1): the person signs in inside the distro. (2) **Launch.** An agent runs on Windows, inside a distro, or as its project says (a project default a person sets; agents follow it unless they choose); only an adapter with the start path offers the mode (Claude Code; OpenCode's manifest declares WSL as a fact but its ACP adapter cannot start there, so it is not offered). The adapter starts `wsl.exe -d <distro> --cd <linux path> --exec /usr/bin/env DRYDOCK_WSL_RUN=<marker> HOME=<linux home> PATH=<login PATH> /bin/sh -c 'setsid -w ...' <claude> <args>`, stream-json over wsl.exe's pipes as natively. Every path it hands the CLI is translated (the working folder, the instructions file, `--mcp-config` and the shim command inside it, `--settings`, `--add-dir` and the other path flags; the project memory is read on the Windows side into the instructions file; handoffs are text in the prompt); 8.3 short names in `%TEMP%` are expanded first. (3) **Environment.** Only what `WSLENV` names crosses: `CLAUDE_*` and `ANTHROPIC_*` (not the Windows-only `CLAUDE_CONFIG_DIR`, `CLAUDE_CODE_GIT_BASH_PATH`, `CLAUDE_CODE_TMPDIR`, `CLAUDE_CODE_SHELL`), proxies and certificate paths (`/p`), the run's own variables and the person's names. HOME and PATH are the distro's, on the command line; no secret is ever on a command line. (4) **Stop.** Killing wsl.exe's Job Object does not reliably end the Linux side, so every process of a run carries a marker; Stop, and the end of the process, kill every marked process and the process groups they lead inside the distro (the claude process is a session leader through `setsid -w`) before Wait answers. (5) **Drydock's tools from inside.** Claude reaches the MCP gateway through the stdio shim, `drydock.exe mcp-shim`, a Windows program; the distro starts it through interop and it talks to the gateway on the Windows loopback, so it works in NAT and mirrored networking alike (a direct loopback HTTP entry would need mirrored networking). The copy of the MCP config the distro reads names the shim's variables in its own `WSLENV`. Interop off is shown on the Connection with the fix and refuses a start that needs the gateway. Drydock gives Claude Code no hooks (P16: `--setting-sources user`), so there is nothing else to reach. (6) **Security.** The analyzer reads a WSL worker's command as bash with Unix paths and the path guard maps `/mnt/<drive>` and the distro's own files to their real Windows locations (`docs/security.md` s6, s7); the sandbox (P30) and WSL are refused together, with the reason, from every side (an agent cannot move into WSL in a sandboxed project, the sandbox cannot be switched on while an agent runs in WSL, a worker turn of a WSL agent in a sandboxed project becomes a question, and a template role that writes in the worktree, such as the test writer, is refused with the same reason: every pipeline turn, worker, reviewer, role or planner, resolves its agent's run mode the same way before the sandbox is set up), because the sandbox runs an agent's commands in a container from Windows and a CLI inside a distro would run its own there. WSL is not a sandbox either: with interop on the agent reaches every Windows drive and can start Windows programs. (7) **Where worktrees live.** A worktree is only as fast for the agent as the disk it is on, and git inside the distro must be able to follow its links. A repository inside a distro (`\\wsl.localhost\<distro>\...` or `\\wsl$\...`) is therefore driven by the distro's own git (Drydock's hardened chokepoint, `git.Runner`, runs `wsl.exe ... git` with the same `-c` settings) and its task worktrees are made next to it inside the distro (`<parent>/.drydock-worktrees/<project>`): Windows git through `\\wsl.localhost` would write worktree links that the distro's git cannot follow and would prune. A Windows git that still reaches a distro path gets `-c safe.directory=<that path>` on its own command line; the person's global configuration is never changed. A repository on a Windows drive keeps its worktrees in the data folder, as natively. **Performance trade-off:** a repository on a Windows drive (a Dev Drive included: `D:\src\app` is `/mnt/d/src/app`) is read by an agent in the distro across the WSL 9P file share, which is markedly slower for many small files (installs, builds, searches); a repository inside the distro is on the distro's own disk and fast for the agent, while Drydock's own reads of it go the other way across `\\wsl.localhost`. The Connection says so. (8) **Proof.** On the Linux development host, labelled test doubles (a stand-in `wsl.exe` that runs programs as wsl.exe does, giving them only what `WSLENV` names, and a POSIX sh stand-in `claude` that speaks stream-json) prove the whole path: `internal/wsl` (`TestKillEndsEveryProcessOfTheRunEvenAfterWslExeIsGone`), `internal/agents/claude` (`TestAWSLSessionRunsInTheDistroStreamsAndStopKillsItsLinuxProcesses`), `internal/connections` (`TestEachUserDistroIsAConnectionOfItsOwnWithItsOwnSignIn`, `TestTestConnectionRunsTheDistrosInstallAndKeepsItsOwnResult`), `internal/core` (`TestAnAgentChoosesWSLAndTheSandboxIsRefusedWithIt`, `TestATemplateRoleInWSLIsRefusedInASandboxedProject`), `internal/git`, `internal/security`, the UI's browser tests and `ui/e2e/wsl.spec.ts`. CI's `wsl` job installs Ubuntu 24.04 under WSL 2 on the GitHub-hosted Windows runner (`Vampire/setup-wsl` v7) and runs `TestRealWSLSession`, `TestRealWSLListsProbesAndKillsByMarker` and `TestRealWSLRepository` against it with the stand-in claude: detection, launch, path mapping on a drive and inside the distro, stream-json both ways, the shim started through interop with the gateway's variables, a repository inside the distro, and that stop kills the Linux processes. **Not proven** until it runs on a PC: a real, signed-in Claude Code inside a distro (its sign-in, a real turn, its permission prompts with Linux paths, whether it passes the MCP config's environment to the shim on every WSL version), mirrored networking, a Dev Drive's speed through 9P, and anything on the owner's own distros (the first CI run is also the first run of the `wsl` job).
- **P42. Integrations are connected at the workspace level and assigned to projects; Drydock calls each vendor's own API (2026-10-12).** On the owner's request ("build out is a true Integration for Linear, Notion, Sentry, and Akidio ... We would click on Linear and be not be able to change the name etc. Also we should be able to turn on Integrations without assigning it to a project ... Also add their logos." and "There doesn't seem to be a option under Integrations for integrating GitHub."). (1) **Connect once, assign separately.** A connection belongs to the workspace and holds the credential (still only in the OS credential store) and the account it reaches; it needs no project. An assignment gives it to one project with that project's scope (Linear team or project, Notion data source or search text, Sentry organization, project and query, Aikido repository, GitHub repository and labels). One connection serves several projects; each assignment has its own cursor, items and rules. There is one connection per vendor; Reconnect changes its credential. Migration 00114 turns every project connection into a connection plus an assignment with the same credential reference, scope, cursor, items, tasks and rules; a connection that had no project keeps its items until its first assignment adopts them. (2) **Fixed identity.** A connection is named after its vendor and cannot be renamed; its logo is the vendor's (Aikido's mark now comes from Aikido's own repositories). What it is (account, workspace, scopes where the API reports them, how it was made, when it was last checked) is read from the vendor's API on Test, never typed. (3) **The vendors' own APIs, not their MCP servers, for app sync.** No vendor has an official Go SDK (Linear's is TypeScript, Notion's JavaScript, sentry-go reports errors and is no API client, Aikido has none), so Drydock has plain HTTPS clients that follow each API's documentation and SDK sources: Linear GraphQL, Notion REST with `Notion-Version: 2026-03-11` and the data sources API, Sentry web API v0, Aikido public REST API v1 with OAuth client credentials, and GitHub through the user's `gh` (on the `gh` route Drydock stores no GitHub token; the GitHub MCP server connected in the Library keeps its own sign-in or token in the Windows credential store, never shown, deleted by Disconnect: audit 2026-10-16, L45). The documented rate limits are in the code. Sign-in exists where the vendor allows a public client: Linear (PKCE) and Sentry (device flow), on when a build registers an OAuth client id; Notion's public OAuth needs a client secret, so Notion uses an internal integration secret. (4) **MCP stays separate.** Giving agents a vendor's tools is the MCP screen's business; the Integrations page links there. Outbound writes proposed through the MCP tools before this change and not yet sent are cancelled with their approvals by the migration and must be proposed again. Details: `integrations.md` "As built: workspace connections".
- **P43. MCP servers are connected from a connector gallery, and a custom server is known by its serverInfo (2026-10-12).** On the owner's request ("There seems to be real no identifier for connecting Existing MCP Servers. We should set it up so it connects to MCPs and we set that connection up like how other aps do it ... Aikido, Sentry, Linear, Notion, Microsoft MCP, CloudFlare MCP ... GitHub MCP", and "When I see the prebuilt agents and says 2 skills or 3 skills etc. Or ctx7 ... I don't know which skill the prebuilt stuff is using"). (1) **Gallery.** The MCP page shows every catalog server as a card, as Claude Desktop, Cursor and VS Code do: logo, name, publisher, what it gives agents, sign-in kind, license, the vendor page it was checked against, and a state (Not connected, Connected as the account the server reports, Needs sign-in, Needs a token, Error with the reason). One Connect (`mcplib.connect`, no project needed) runs the existing OAuth sign-in (P22), takes a token into the credential vault, or starts the program a user-install template names; the result is an ordinary library server, so P15 holds unchanged (deny by default, private data, guard on, approvals for every tool not declared read-only) and Add to project, Grant to agents and Disconnect are the library's. (2) **Re-checked catalog.** Each vendor's server was re-checked against the vendor's own sources (the blocked docs sites through their GitHub repositories, npm metadata and search extracts, as `mcp.md` lists): Cloudflare's API server is added and its deprecated per-product servers are not, its documentation server has one tool; hosted Sentry signs in with OAuth and dynamic registration (a pasted token keeps `Sentry-Bearer`, an OAuth token is always sent as Bearer); GitHub's hosted OAuth needs an app Drydock does not have, so it takes a personal access token, and the local `github-mcp-server` is a read-only template; Aikido has no hosted server of its own and stays an AGPL template (now 1.0.28) whose scan tools the send guard cannot let reach source code; D14 is unchanged. (3) **Identity.** A server is identified by what it says about itself: the `serverInfo` (name, title, version) and instructions of its answer to `initialize`, kept with the library server after each discovery and forgotten when its address changes, and for a catalog entry the account its identity tool reports, forgotten when the credential changes. These are the server's claims, shown as data and never passed to an agent; the last connection error is kept so a card can say why it failed. A custom server is added by display name and address or command and is shown by that identity; servers can be imported from a `claude_desktop_config.json` or `mcp.json`, every environment value and bearer token going to the vault. (4) **Prebuilt agents say what they come with.** Their MCP badges show the server's full name and logo, the skills badge lists the skills, and opening an agent shows its model and CLI, each skill's description from its SKILL.md at the pin, source repository and commit and license, and each MCP server's name, publisher and summary. The descriptions are recorded in `internal/skills/registry.yaml` from the pinned commits; an agent catalog entry naming an unrecorded skill is refused. Migration `00112_mcp_gallery.sql`. **Not verified:** any real vendor server (none was contacted), Cloudflare's dynamic client registration, the Notion tool names (from a connected client, not Notion's docs), Windows.
- **P44. One button scans the working tree with three engines, and a finding never carries what it found (2026-10-12).** On the owner's request ("a Security Scanner, that we just press 1 button and it does all those scans ... watch the scan process go through with its different tools in a progress bar"), after the review of a brief that cloned the repository into a temporary folder and ran gitleaks, osv-scanner and Semgrep over it. (1) **No clone.** The scan runs over the project's own working tree on this computer: a project already has its checkout, a fresh clone would need a second GitHub sign-in and scan only the default branch, and on Windows `os.RemoveAll` fails on git's read-only pack files. (2) **Opengrep, not Semgrep.** Semgrep is a Python distribution with partial Windows support and its registry rules are under a licence that does not allow use inside a commercial product; Opengrep (the LGPL-2.1 fork, native Windows binary, the same rule syntax) is the Code engine. Drydock ships and downloads no rules: the `opengrep-rules` repository carries LGPL-2.1 with the Commons Clause and says its rules are for research, testing and benchmarking, so the person chooses a rules folder they are licensed to use (`security.settings.set`; only a person may). (3) **What is kept.** gitleaks runs in process over every text file (Drydock walks the tree so it can show files scanned), osv-scanner's JSON is read for identifiers only (the `deps` check's parser), Opengrep's SARIF 2.1.0 is read for rule, level, message, file and line; the matched secret, the vulnerable line and a tool's raw output are never in a finding, an event or the database, every title and reason goes through the app's secret scanner, and the stored report is redacted once more as a document. (4) **Never a pass.** A tool that is not installed, a missing rules folder, a tool that broke or ran past ten minutes ends its engine skipped or errored, named on the page and in the summary. (5) **Progress without event spam.** Engine state changes are events (`security.scan.started/engine/finished`); the counters are read from the running scan (`security.scan.get`) twice a second by the page and are not persisted. Package `internal/scan`, extension `internal/core/ext_security.go`, migration `00116_security_scans.sql`, page `/security`, document [`security-scan.md`](security-scan.md). **Not verified:** a real osv-scanner or Opengrep run (parsers written from the documented shapes and proven against labelled stand-ins), Windows, a repository inside a WSL distro.
- **P45. The shipped MCP catalog offers hosted servers only (2026-10-13).** On the owner's request ("for the GitHub, Sentry and Aikido lets get rid of the Local program ones for now"). The gallery showed three local-program cards next to the hosted ones: `ghlocal` (github-mcp-server 2.0.2, read-only), `sentryx` (`@sentry/mcp-server` 0.42.0, for self-hosted Sentry or a token) and `aikido` (`@aikidosec/mcp` 1.0.28, AGPL; its scan tool would send code to Aikido, which Drydock refuses, so only its issue tools were usable). They are removed from `internal/mcp/catalog.yaml`; GitHub and Sentry keep their hosted cards (api.githubcopilot.com with a personal access token; mcp.sentry.dev with OAuth or a `Sentry-Bearer` token), and Aikido stays an Integrations connection (app sync through its REST API) with no MCP card, since Aikido publishes no hosted MCP server that the evidence found. Nothing else changes: the schema still allows a stdio server (`template: true`, never bundled, D14) as a library or project server a person adds by hand, and the gallery's connect path for templates is still tested with a test-double catalog. The catalog test now fails if a stdio entry or a template reappears in the shipped file. The three pins were not part of the software audit (Drydock never installed them), so `docs/software-versions.json` is unchanged. **Open:** self-hosted Sentry users have no MCP path until a hosted entry takes a base URL; they keep app sync.
- **P46. A skill registry is imported whole and watched twice a day (2026-10-13).** On the owner's request ("lets have it that it always imports all the skills and we choose which one we want to implement ... scanning the repo once or twice a day looking for changes in skills or additions, and it notifying us"). (1) **Import all, accept one by one.** "Import all" on the Skills page (`skill.importAll`) brings every valid skill of a registry into the project as offered, pinned to one commit, validated and scanned like a single import; the person still accepts each skill (the P11 and P27 rule stands: a skill reaches an agent only after a person's acceptance). The Microsoft registry stays one-at-a-time: it is marked `selective_only` and its own README warns that loading all 175 skills causes context rot. With the `relevant` delivery of P37, holding every skill of a registry in a project costs little per turn: the ones matching the task load, the rest are listed. (2) **Twice a day, tell, never act.** Every enabled registry a project imports from is checked two minutes after start and every twelve hours: one API call for the default branch's commit, the tarball only when it moved, a snapshot of names, descriptions and hashes (never the text) in `skill_registry_watch`. New and changed skills are computed against each project's skills as they are now and shown on the Skills page with Import buttons and a Check now; a project is told once per upstream commit through a notice and a Needs-you item in its alerts channel and `skill.registry_updates`. No check imports, accepts or changes a skill. (3) **Why a snapshot rather than a stored diff.** A diff stored at check time goes stale the moment the person imports one of the skills; computing against the current skills keeps the page honest between checks. Package `internal/skills` (`watch.go`), extension `internal/core/ext_skillwatch.go`, migration 00117, document [`expert-packs.md`](expert-packs.md) as-built. **Not verified:** the real GitHub API (the fixtures answer as api.github.com and codeload.github.com do; Drydock's fetcher has not been run against them from this sandbox); the first run on a PC with many registries (one call per watched registry per pass, well inside GitHub's unauthenticated limit of 60 an hour).
- **P47. Integrations and MCP servers are one page (2026-10-13).** On the owner's request ("lets merge the Integrations and MCP Servers pages together"). One page, Integrations (`/integrations`; `/mcp` stays as an alias), with one tile per vendor: the five integration kinds (GitHub, Linear, Notion, Sentry, Aikido) and the MCP-only connectors (Microsoft Learn, Cloudflare as one tile for its documentation and API servers, Context7). A tile's panel has up to three sections: **App sync** (the connection, its test, assignments, rules, outbound; the five kinds), **Agent tools (MCP)** (the vendor's connector cards: connect, tools, add to project, grant to agents) and **Skills from this vendor** (the vendor's skill registries, named by the catalog's new `skill_registries` field: Cloudflare's `cloudflare-skills`, Microsoft's `msdocs-mcp-skills`, `msdocs-agent-skills` and `microsoft-skills`; the core checks every id names a registry the Skills page knows). Below: your own servers, and with a project the approvals waiting and the project's servers, as the MCP page had them. The "MCP servers" sidebar entry is gone. Why: the vendors were already linked in the data (`integration` on the catalog entries), and two pages for one vendor made people look twice for one credential.
- **P48. The Library holds every registry skill; a project only chooses (2026-10-13).** On the owner's request ("the Library should import all the skills automatically, and only under the project is where we choose which skills we want to use"). (1) **Drydock fills the library itself.** Every enabled registry is synced into `skill_library` (migration 00118) two minutes after start and every twelve hours (the P46 schedule): one API call for the default branch, the tarball only when the commit moved, every nameable skill validated, license-checked and scanned like an import and stored `available` or `blocked` (with the reason); a skill the registry drops is marked `gone`, never deleted. The Microsoft registry is synced too: its `selective_only` warning is about loading 175 skills into context, and the library loads nothing into anything. (2) **Choosing is accepting.** `skill.libraryAdd` copies a library skill into a project as ACCEPTED, with the person recorded as the decider (the P27 rule: the person's choice is the acceptance); `skill.libraryAddAll` adds every available skill of a registry. A blocked or gone skill cannot be added; the repository's own skill of the same name wins; a skill the person rejected and adds again is accepted again, because the add is a new decision. (3) **A project copy moves only by a person's hand.** A sync changes the library, never a project's copy; `skill.registryStatus` lists, per project, the copies behind the library, and adding the skill again is the update. The notice of P46 now says what a sync brought to the library and which project skills are behind, once per upstream commit. (4) **The network import stays** (`skill.import`, `skill.importAll`) for the prebuilt agents and templates that pin a commit; the Skills page adds from the library. Package `internal/skills` (`library.go`, `watch.go`), extension `internal/core/ext_skillwatch.go`, document [`expert-packs.md`](expert-packs.md). **Not verified:** the real GitHub API; the first sync of ten registries on a PC (ten API calls and up to ten tarballs, the Microsoft one the largest; a tarball over 16 MiB fails that registry's sync and is reported on its tile).
- **P49. What a skill costs and whether anyone read it (2026-10-13).** On the owner's request ("know which skills are being used and which are not, which cost us tokens, and recommend getting rid of a skill that is used but not providing value"). Deliveries already recorded what reached a run (the full text, or an index line to read on demand). Now **uses** are recorded too (`skill_uses`): Claude reading a skill's on-demand file (an `agent.output` tool call `Read` of `.claude/drydock-skills/<name>.md`, Windows paths included) and an agent fetching a skill through the skills tool. `skill.usage` reports per project skill: loaded, listed, opened, tasks reached, estimated tokens (four characters per token: the whole text per full delivery, one index line per listing), last use, and a recommendation written from the numbers: **remove** when it was listed in five or more tasks and never opened; **narrow** when it loads in full everywhere because it names no paths; **keep** when it is opened or loads only for matching files; none below five tasks. The reason says what the numbers cannot tell: whether a skill raised the work's quality is a trial's job (`docs/evals.md` section 14), not a counter's. **Not verified:** a real Claude Code run reading a skill file on the PC (the hook is proven against the event shape the adapter emits, the same one the trial harness counted).
- **P50. A Connection's sign-in starts from its card (2026-10-13).** On the owner's request, after the WSL Claude Code card kept failing with "not signed in inside the distro" ("could we just build that prompt into the app?"). The card of a CLI whose manifest names a terminal sign-in (`auth.login`, the one place it is written down: `claude auth login`, `codex login`, `opencode providers login`, `goose configure`, `cline auth`) has a Sign in button, and the Connection carries that command (`signIn`) so the card and the core cannot disagree. The core (`connection.signIn`, `connections.Service.SignIn`) checks that one Connection, then starts the command, unmodified, in a terminal shown in the card itself (a Connection is a workspace page, open with no project, where the dock is not), and the person answers it there. A native CLI runs by its full path (an npm `.cmd` shim through `cmd.exe` by its full path: the pseudo-terminal searches no PATH). Inside WSL it starts as an agent starts there (the tool, HOME and login-shell PATH the probe found, so a node-script install finds node; the run marker; `wsl.exe` with the base allowlist and an empty `WSLENV`), but interactive: no `setsid`, so it keeps the terminal and Ctrl+C. One sign-in per Connection runs at a time (asking again returns its terminal). When the program ends, however it ends, whatever of it is left inside the distro is killed by its marker (killing `wsl.exe` does not end it) and the core publishes `connection.sign_in_finished`, on which the page re-checks. Drydock never reads, copies or stores what the sign-in leaves behind (D1 holds: the CLI writes its own credential file, in the distro for a distro install); a CLI not installed, or whose manifest names no terminal sign-in, is refused with the reason. A terminal whose program ended no longer counts toward the eight running terminals. This is the "run login in a PTY, re-probe" orchestration `docs/connections.md` section 11 had planned; a code review of the first version (15 findings) shaped this one.
- **P51. Home holds the workspace; a project shows only itself; a project can be deleted (2026-10-13).** On the owner's request ("the only way I can see skills is if I am in a project ... there is basically no home screen ... lets separate the Project stuff from the main workplace stuff. And give an option to delete a project in the project settings"). (1) **Two sidebars, chosen by the page.** A workspace page (Home `/home`, Chats, the skill library `/library/skills`, Agents and MCP servers `/library`, Integrations, Connections, Providers, Routing, License, Backups, Export and import, Support bundle, Settings, and New project) shows the workspace's sidebar: Home, Chats, the projects (each opens that project), Library, Connect and App. A project's page shows that project's sidebar: Home first (back to the workspace), Needs you, Tasks, Pull Requests, Plan, Team, Activity, the channels, the Project and Knowledge sections and the agents; no workspace links. A workspace page shows no repository banner and no details panel (nothing of a project); the dock stays, so a running terminal is not cut. The switcher says Home on a workspace page and lists Home, the projects, New project and App settings; choosing a project always opens it at its home (its channel). With no project at all every page shows the workspace sidebar. The app still opens on the last project (`/`), since that is where the work is; Home is one click away. (2) **Home.** The projects as tiles (where each lives, what needs you in each: one Needs-you read per project) and the workspace areas as link tiles. (3) **The skill library has its own page.** It was a section at the bottom of the Library page, so a person looking for skills outside a project did not find it; now it is Library > Skills, and the Library page holds agent templates, prebuilt agents and MCP servers. A project's own Skills page (who gets each, usage, the repository's skills) stays in the project. (4) **Deleting a project** (`project.delete`, Project settings > Delete project). The person types the project's name; the core checks it again. It refuses while work runs in the project (a workflow run that has not ended, waiting ones included; an agent answering in a channel; a security scan), naming the task, because stopping work belongs to the person. The busy check and the delete are one write transaction, so no run can start in between. The rows go per `projectio.DeleteSteps` (children first); `projectio.KeptOnDelete` names every table whose rows stay and why, and a test fails when a migration adds a table that is in neither list (the pattern of the export allow-list). What stays: the **event log** (append-only, kept indefinitely), the **audit log** (append-only by trigger; the delete itself is audited), **model usage** (what was spent stays counted by workspace budgets and reports), the human's **chats** (unlinked from the project), workspace connections, providers, packs and the libraries. Per-project settings are every settings key that ends with the project id. Then Drydock removes what it made on disk: each task worktree (with `git worktree remove --force`, so changes nobody committed in a worktree go; the person was told so and typed the name), its own worktree folder, sandboxes, run folders, previews, attachments, the project-without-repository folder, MCP cache files and integration item bodies, and prunes git's worktree list. **The repository folder, its branches (task branches included) and its commits are never touched**; Drydock's safety refs stay with them. Anything that could not be removed is reported, and the project is gone either way: a worktree beside a repository that cannot be opened (a WSL repository whose distribution is not running) is named, not silently left. The audit entry is written in the delete's transaction. A security scan that started in the moment between the busy check and the transaction is cancelled, and a scan report is only stored while its project exists. Home reads each project's Needs-you list on its own: one that cannot be read says so on its tile. The UI then opens Home, which says what was deleted. Package `internal/projectio` (`delete.go`), core `internal/core/project_delete.go`, UI `screens/workspace`, `shell/sidebar.tsx`. **Not verified:** on Windows, a worktree folder held open by an editor or a running program (git refuses; the delete reports it and the folder stays for the person to remove).
- **P52. Workspace pages carry no project; skills are picked compactly and an agent of the team recommends who gets what (2026-10-14).** On the owner's requests ("anything project related lets remove from Home and keep only in the project"; "Agent CLIs to me kind of lands under Connections"; one Sign in button; "first see the available MCP Servers we have prebuilt into the App, and then the ability to add a custom MCP Server"; registry sync errors for Microsoft Docs, Stripe and Remix; "the boxes are too big in Skills"; short descriptions that expand; "highlight multiple skills to add to a project"; "an Agentic AI giving recommendations on which Agent should get which Integration and Skill ... as the project evolves"; Claude in WSL for a chat without a project). (1) **No project on a workspace page.** The Library (agents and MCP servers) lost its Projects column, drag and drop and "Put in project"; the workspace Integrations page only connects, tests, signs in and disconnects (a connection says in how many projects it is). A project's MCP servers, their grants to agents, the approvals waiting and its integration assignments, rules and outbound moved to a new project page, **Integrations** (`/project/integrations`, Project group); templates are applied on the Team page. (2) **Agent CLIs and the MCP gateway** are on the Connections page, below the CLI tiles. (3) **MCP sign-in** is one Sign in button; signed in, one line with Sign out. **Connect an MCP server** lists the servers built into Drydock first, then a custom server and import. (4) **Registry sync** (measured on shallow clones of the real repositories): the commit is resolved with GitHub's `application/vnd.github.sha` media type (MicrosoftDocs/Agent-Skills' commit JSON is over the 1 MiB page cap); tarballs up to 64 MB (stripe/ai 19.4 MB, remix-run/remix 19.1 MB, merill/msgraph 50.7 MB) with the unpacked stream bounded at 512 MB instead of 32 MB; a SKILL.md up to 128 KiB (anthropics/skills claude-api is 105 KB, MicrosoftDocs skills up to 90 KB). The Remix registry is Remix 3 (`remix-run/remix`, root `.agents/skills/remix`; its other 18 skills are for working on the framework), not React Router; curated registries are brought in line with `registry.yaml` at every start (a retired one leaves with its library rows). merill/msgraph stays: it is not a copy of a Microsoft registry (no official Graph API skill was found), but its skill needs 112 MB of indexes and executables Drydock does not import, which its notes say. (5) **Skills shown compactly**: a registry tile is one line ("20 skills · 3 blocked · 2 new", "Sync failed"), a library skill a row with its first sentence that opens to the whole description; rows are picked and added with one button, or taken from Library > Skills to a project. (6) **Recommendations** (`docs/expert-packs.md` section 9): one read-only turn of an agent of the team (the lead unless the person picks another) reads the project and what the workspace offers and recommends which agent gets which skill, MCP server or integration, with a reason; the core keeps only items that name what exists and says why it dropped the rest; the person applies or dismisses each; nothing is applied by the agent. Picks from the Library arrive on the project's Skills page, to add at once or to be judged. The project asks again by itself after 5 completed tasks, at most once a day, within the budgets, with a lead, unless switched off (announced in #general): the owner asked for it, the bound keeps it from spending unattended (P36). (7) **Chats in WSL**: the new-chat picker lists each Claude Code install inside a WSL distro; the chat keeps its distro and every turn runs there through the agents' WSL launch (a chat without a project gets `~/.drydock/chats/<id>` in the distro). Tests: see the docs named and `ui/e2e/recommend.spec.ts`, `workspace.spec.ts`, `wsl.spec.ts`. A review afterwards found that a turn cut short by a crash or by closing the app stayed "running" and refused every later turn of its project; such turns are now recorded as interrupted at start, a project is not deleted while a turn runs, and applying or dismissing an item is one locked step (tests in `recommend_test.go` fail on the old code). A real recommendation was verified afterwards with the Claude Code installed on the development host (2.1.299; the recommended version stays 2.1.296 under P33) on haiku, both asked for and as the project evolved (`docs/expert-packs.md` section 9, `TestRealLeadRecommendsSkillsAndServersThatFitTheProject`). **Not verified:** the registry sync against the real GitHub API, a chat in a real distro on a PC.
- **P53. Borrowed from OpenCode and claude-code-router (2026-10-15).** Ideas taken from reading their source; each sub-item stands on its own.
  - **(a) OpenCode over ACP: resumed cost, compaction, sub-agents.** Read from OpenCode v2's `packages/cli/src/acp`; harmless on the pinned 1.18.35. (1) `usage_update.cost` is the session's cumulative cost, so a process that loads or resumes a session counted every earlier turn again (the scripted agent recorded 0.06 for a 0.02 turn); the ACP adapter now reports the session's total in `UsageChunk.Totals` and starts from `RunSpec.UsageBaseline`, the CLAUDE-USAGE-03 fix (`chats.cli_usage`, no migration). (2) OpenCode reports a compaction as `session_info_update` `_meta["opencode/compaction"]`; a completed one is counted into the turn's usage and P39 hands the next work turn to a fresh session (`context_pressure`). ACP's own `session.compaction` capability is not advertised: the pinned acp-go-sdk 0.13.5 can neither send it nor decode `compaction_update`. (3) Only the OpenCode manifest opts in to `_meta["opencode/child-session-updates"]` (`transports.acp.client_meta`); a sub-agent's tool calls become the turn's tool events, its text no longer joins the parent's message; the notification lacks ACP's `_` prefix, so the adapter renames it. (4) The inline config states `{"compaction":{"auto":true}}`: Drydock owns the 70 % handoff between turns, OpenCode's own compaction stays only as the overflow guard inside a turn, and `auto` is the one key 1.x and 2.x share (`token-control.md`). Contracts OPENCODE-USAGE-01, OPENCODE-COMPACT-01, OPENCODE-CHILD-01 (paid, real CLI only). **Not verified:** any of it against a real OpenCode (not installed here, no credentials).
  - **(b) Fallback chain.** A routing rule may name a `fallback` chain (up to 4 models); an agent turn whose model or provider is unavailable (rate-limited, overloaded, 5xx or down, model not found) before it made a tool call re-runs on the next model of the chain: at most 2 hops, each model once, the same tier or cheaper (moving up stays escalation), the budget read again before each hop and a used-up budget ends it, never another provider or login for an agent CLI (D1), never on the work's own errors (refusal, prompt too long, failing tests). Recorded as a decision with `routing.fallback` and in "Why this model?"; the end of a chain as `routing.blocked`; the Routing page's rule editor has the field. `model-routing.md` s15.
  - **(c) OpenRouter uptime.** From claude-code-router's OpenRouter endpoint chooser. Before each of Drydock's own OpenRouter calls
    (`internal/llm` only; agent CLIs that call OpenRouter themselves are out of scope), the Wrapper reads the model's endpoints and
    their recent uptime (`GET /api/v1/models/{author}/{slug}/endpoints`, `uptime_last_5m` else `uptime_last_30m`, cached 5
    minutes) and asks OpenRouter to skip those below a threshold (98% by default, per provider in its settings, 0 is off) through
    `provider.ignore`, not `order`, so sticky routing stays. A failed lookup changes nothing; when every endpoint is below the
    threshold the best are kept; a "no endpoints" answer while skipping is resent without. Changes publish
    `provider.endpoints_avoided` and the provider's panel lists what is skipped. Details and what is unverified:
    `docs/model-routing.md`, "As built: OpenRouter endpoint uptime".
- **P54. Agents are told the SDK versions the repository uses (2026-10-15, on the owner's request).** Asked whether syncing
  vendors' SDKs (Linear, Microsoft) into Drydock is worth it: not as Drydock's own dependencies (Linear and Notion publish no Go
  SDK, Microsoft Graph's Go SDK is very large for an integration Drydock does not have, Sentry's is for reporting errors; the
  direct API clients are about 1,400 lines with their rate limits tested), and not as copied reference text (the skill
  registries, Context7 and Microsoft Learn already give agents current documentation, and P37 measured that preloading costs
  without helping). The gap was the version: an agent asks for the latest documentation while the repository pins an older
  major. AGENTS.md now has a "Library versions" section read from the worktree's manifests and lockfiles (npm, pnpm, yarn,
  Go, NuGet, Python), curated SDKs and frameworks only, at most 40, validated so a manifest cannot inject text; the lead's
  recommendation turn sees the same list. Files only, nothing is run or contacted. `docs/token-control.md`, "As built:
  library versions in the instructions".
- **P55. A scan tool's program can be chosen, and tools installed while Drydock runs are found (2026-10-15, on the owner's
  request).** The owner saw osv-scanner and Opengrep "not installed" on the Security page. Drydock still installed neither
  then (P44; since P66 it installs both on request), but finding them was PATH-only, and a Windows program only sees the PATH it started with, so a tool installed while
  Drydock ran needed a restart and one installed elsewhere needed a PATH edit. Now: a person can choose each tool's program
  with the window's own Open dialog ("Choose file...", through Wails' dialog; the core gets it as `Config.PickFile`, so the
  method, its checks and its tests are the core's) or type its full path; the choice is the workspace's and "Use PATH
  instead" forgets it. Without a choice, Drydock also looks in the PATH as it is now in the registry and in the folders
  WinGet, Scoop, Chocolatey and `go install` use. `docs/security-scan.md` sections 4 and 5. **Not verified:** the Wails
  dialog and the registry read on a PC (built and vetted for Windows; tested here with the e2e harness's labelled stand-in
  dialog and a replaced folder list).
- **P56. CI runners on the PC for private repositories (2026-10-15, on the owner's request).** Asked whether Drydock should
  set up GitHub Actions self-hosted runners, the owner wanted it OS-agnostic: Drydock builds apps of any kind, so the runner
  for a job should depend on the operating system the job needs. Built: per private repository, a **Windows** runner (on the
  PC, as the person, in a fresh folder per job on the first Dev Drive, else the app's data folder, or a chosen folder) and a
  **Linux** runner (a fresh container per job on Docker Desktop, which runs on WSL 2: no drives mounted, no engine socket,
  resource limits); workflows choose with `runs-on: [self-hosted, Windows|Linux, drydock]`. Every runner is ephemeral and
  registered just in time (`generate-jitconfig`), takes one job, and its folder or container and its registration are
  deleted afterwards, so `_work`, tool caches and Docker leftovers never accumulate (the disk-cleanup cron jobs a long-lived
  runner needs are designed away). The newest runner release is used only after its SHA-256 matches the one in its release
  notes. Its one-job configuration passes in the runner's environment, never on a command line or in a file, log, event or
  the database. Rules: private repositories only (public and internal refused, re-checked before every registration;
  GitHub's own advice, and the reason the owner's public Drydock repository keeps GitHub's runners), an administered
  repository, a person's action for every change, the person's explicit acceptance that a Windows runner's jobs run as them,
  at most 8 runners, a job waits while less than 10 GB is free. **Runners only run CI**: Drydock's existing pull-request
  loop reads the results, has the worker fix a failure and pushes (docs/github.md section 5); a runner never pushes or holds
  write credentials. Connections page, "CI runners". `docs/ci-runners.md`. **Verified** with test doubles end to end, and in
  CI on every push against the real newest release on Windows and Linux (download, SHA-256, unpack or image build, start,
  configuration read from the environment), nothing registered. **Not verified:** a real job on a real private repository on
  a PC, Docker Desktop on Windows, and `fsutil`'s wording for a Dev Drive.
- **P58. A project from scratch, and agents run where Claude Code is (2026-10-15, on the owner's request).** Creating a
  project failed on a folder that was not yet a repository ("... is not a usable Git repository"), the Agent CLI picker said
  Claude Code was "not installed" while it was connected in WSL, and the stand-up failed with "Claude Code is not installed
  on Windows, but it is installed in the WSL distro Ubuntu-26.04". (1) **Choose folder.** The project folder (the wizard and
  Project settings' attach form) has "Choose folder..." for the window's own folder dialog (`Config.PickFolder`, Wails'
  dialog with directories only; `fs.pickFolder`, a person only, "" on Cancel, refused with "type the folder's full path"
  where there is none); typing still works. The hint recommends a Dev Drive on Windows and names the ones found
  (`fs.folderHints`, `runners.DevDrives` asked once). (2) **Create the repository.** `repo.check` says what is at the folder
  (a repository, a repository without a commit, a plain folder, a missing one, one inside another work tree). For a plain
  or missing folder the person may tick "Create a Git repository here": `repo.init` (or `project.setup` with `repo.init`)
  makes the folder, runs `git init -b main` and a first commit (empty, or what is in the folder less what its `.gitignore`
  leaves out) through Drydock's git chokepoint, the distro's git for a folder inside a distro. A folder that is already a
  repository is never touched, and one inside another repository's work tree is refused naming that repository; only a
  person may. (3) **The pickers.** A CLI not usable on Windows but ready in a distro (`connections.ReadyWSL`: found, signed
  in, an accepted version; a distro degraded only by its setup, interop off or an unknown mount root, counts, since the
  start names the fix) is available, "claude (recommended) - in WSL Ubuntu-26.04" (`agentcatalog.list`'s `wslDistro`,
  `adapter.list`'s `wslDistro`). (4) **Where agents run.** A new project's default is set from the Connections: Windows when
  Claude Code is installed there and usable (or Drydock cannot tell), else the first distro where it is ready, recorded as
  set by `drydock` with the reason, which Project settings shows. At run time an agent that follows a default nobody chose
  (its project's Windows default, not set by a person) and whose CLI is not installed on Windows runs in the one distro where
  it is ready, or the WSL default distro among several; the turn's `agent.run_mode_auto` event and its `agent.started` say
  where and why. An agent or a project a person set to Windows, and a sandboxed project, are never moved: the clear error
  stays. `docs/connections.md` section 4b. **Verified** with the labelled stand-in WSL (internal/core
  `TestANewProjectRunsItsAgentsInTheDistroWhereClaudeCodeIs` and
  `TestAnAgentFollowingTheDefaultRunsWhereClaudeCodeIsAndAPersonsChoiceIsKept`, which fail on the old behaviour;
  `TestRepoInitMakesARepositoryAndNeverTouchesOne`), the UI tests and `ui/e2e/workspace.spec.ts` with the harness's stand-in
  dialog. **Not verified:** the real Wails folder dialog and `fsutil` on a PC, and a real Claude Code in a real distro.
- **P60. Chat from Home, and a chat's properties: skills and MCP servers (2026-10-15, on the owner's request).** The owner
  wanted a chat on Home to use like the Claude CLI in a terminal, on the person's own signed-in Claude Code (subscription, not
  per-token billing), without a project, with skills and MCP servers chosen per chat. Chats already drove the CLI (P29); now
  Home starts with a composer (Start chat: no project, a private folder, Claude Code; Options: the new chat dialog without a
  project) and the latest chats without a project. A chat has properties, set when it is made and in the open chat's
  Properties dialog: the agent CLI and where it runs, the folder, **skills from the workspace skill library** and **MCP servers
  from the workspace MCP library** (stored with the chat, migration 00122). Choosing a skill for a chat is the person's
  acceptance for that chat (as adding one to a project is, P48); nothing is copied into a project. Both reach the session only
  through Drydock's MCP gateway: skills on demand as in P37 (an index in the instructions, the text through the `skills` tool,
  which for a chat session serves only its chosen skills), never as files written into a folder the person owns; servers as
  an agent's grant (tools named when chosen, flagged ones left out, every tool not marked read-only behind an approval, shown
  in the chat). A CLI that cannot be given the gateway is refused both. Skills and servers apply from the next message in the
  same session; a new CLI, distro or folder drops the session and the next turn gets the conversation as a transcript. The quiet
  environment of P36 is unchanged. D1 holds: Drydock runs the person's own CLI and never reads its sign-in. `docs/chats.md`
  section 6. **Verified** with the scripted Claude stand-in calling the gateway (`internal/core/chat_properties_test.go`),
  browser tests and `ui/e2e/home-chat.spec.ts`. **Not verified:** a real Claude Code reading a chat's skill through the tool,
  and the folder picker (typed paths only here).
- **P61. A security scan of any folder, from Home (2026-10-15, on the owner's request).** The owner wanted to run the
  security scan (P44) on any repository at will from Home, for a project imported from elsewhere or a repository to check
  before trusting it. Built: the workspace page **Security scan** (`/security-scan`, Home's tile, the workspace sidebar),
  which scans a folder typed by its full path, or one of the projects, with the same engines, progress, findings, tools and
  history (`ScanPanel`, shared with the project's page). A folder scan belongs to no project: its reports are stored under
  the folder's key (`folder:<path>` in `security_scans.project_id`, no migration), its events carry no project, its history
  is the folder's and the recent folder scans list every folder; it uses a workspace rules folder (each project keeps its
  own). Refused: a relative path, a file, a whole drive, Drydock's data folder, inside it or around it; only a person starts
  one. **It only reads files**, said plainly on the page: gitleaks runs in process; osv-scanner gets
  `--no-call-analysis=all` (its call analysis is the one part that runs other programs: the go toolchain); Opengrep gets
  `--no-git-ignore` on a folder scan, so git, whose `.git/config` may have come with the folder, never runs there. The
  folder's `osv-scanner.toml` and `.semgrepignore` can still narrow what those two report; the page says so.
  `docs/security-scan.md` section 8. **Not verified:** the two flags against the real tools (written from their documented
  options; the stand-ins accept any), Windows paths on a PC (case-insensitive keys and the drive and data-folder checks are
  built for it and vetted with `GOOS=windows`); the folder is typed until the folder dialog of another change lands.
- **P59. Integrations look like Home, and a connection can be checked against the person's own account (2026-10-15, on the
  owner's request).** (1) "We are not keeping with the UI standard with the cards like we are in the Home Page": both
  Integrations pages now have Home's shape, groups of tiles under h2 headings (workspace: Services, Agent tools, your own
  servers; a project: its connected services as tiles with the scope it imports), each tile with logo, name, purpose, state
  and a state badge (the kit's `Tile` gained `badge`), and the selected tile's panel as one card under its group. Functions,
  labels and test ids are kept. (2) The live checks were Go tests run by hand with tokens in the environment. Each connected
  service now has **Check against your account** (`integration.verify`): the same read-only checks, now product code the live
  tests call (`Service.Verify`), with the credential Drydock already holds and about six calls: who it is, one list of
  choices, one page of up to two assignments with every item checked for an id, title, reference and update time, one item's
  full text. The report gives each call's HTTP status, counts, and what did not match (a field and its JSON type, never a
  value); nothing is written to the vendor or stored from it. The outcome (time, ok, summary, counts; no content, no secret)
  is kept per connection until a reconnect, shown as "Verified against your account on <date>", and published as
  `integration.verified`. `docs/integrations.md`, "As built: check against your account". **Not verified:** against a real
  vendor account; that is what the button is for.
- **P62. The repo map, with tree-sitter through CGO (2026-10-15, on the owner's decision).** D5 gated the repo map on a
  Windows CI job proving that CGO tree-sitter builds; that job (`repomap-cgo`) is green, and the owner decided to build it
  with the real tree-sitter parser (C, through CGO), accepting that the Windows build needs a C compiler. Built:
  (1) **`internal/repomap`**: Aider's algorithm (docs/phase0/go-platform.md section 13). Files come from `git ls-files
  --cached --others --exclude-standard` through Drydock's hardened git (so .gitignore is honoured and the configuration
  guard applies; a repository in a WSL distro is listed by the distro's git); vendored folders (node_modules, vendor,
  third_party, dist, ...), generated files (a "Code generated ... DO NOT EDIT" or "@generated" header, names such as .min.js
  or .pb.go), binary files, files over 512 KB, files past the 5,000th and links (a file that is a link, or one reached
  through a folder replaced by a link or junction: git lists it from its index; the map shows only code in the checkout,
  as `read_range` does) are skipped and counted. Each file is parsed with
  `github.com/tree-sitter/go-tree-sitter` v0.25.0 and its grammar (Go, TypeScript, TSX, JavaScript with JSX, Python, C#,
  Java, Rust; all MIT), and the grammar's own `queries/tags.scm` (copied unchanged, with its license) plus a query authored
  here for what it leaves out (`queries/*-extra.scm`: JSX element references for TSX and JavaScript, which no upstream
  query has; TypeScript type aliases, enums and heritage; Python bases and imports; C# structs, records, enums and
  static access; Java enums, records and declared types; Rust path calls, uses and type references) give definitions
  and references. The file graph, Aider's identifier weights (x10 for long camel/snake names, x0.1 for private names and
  names defined in more than five files, x50 from a focus file; snake_case counts in any case, so MAX_RETRY_ATTEMPTS weighs
  x10 as in Aider), personalized PageRank (networkx's: alpha 0.85,
  personalised toward the focus paths) and the rank shared out to definitions follow Aider; the rendering prints each
  file's top definition lines with their enclosing definitions ("│" lines, "⋮" gaps), files in PageRank order (Aider sorts
  them by name), and a binary search keeps it within the token budget (256 to 8,192, default 2,048, chars/4; a hard
  limit, where Aider allows 15% over). Tags are cached by content hash in memory (at most 50,000 entries, with the file
  stats bounded the same way, so a long session that maps a new worktree per task does not grow) and in one file per
  project under its data folder (`projects/<id>/repomap`, removed with the project; the repository and all its task
  worktrees share it, since a worktree is gone after its task), so a repeated map parses only changed files. The cache's
  version names the parser, the grammar module releases and the queries, so a new grammar release parses again; a cache
  write that failed is tried again by the next map, and concurrent writes each use their own temporary file. (2) **Build tags**: only `parser_cgo.go` (`//go:build cgo`) imports tree-sitter; `parser_nocgo.go` makes a build
  without CGO answer `repomap.ErrNoParser`, and `mcp.RepoMapGatedMessage` now says that this build has no repo map because
  it was built without the C parser. (3) **Wiring**: the Workspace tool `repo_map` gets the core's mapper (the run's
  worktree, the project's git) and is one of the read tools every role gets in a CGO build; AGENTS.md gains a section
  "Repository map" of 1,024 tokens in repositories of 25 or more source files (token rule 9: the initial package carries a
  token-budgeted map), recorded in the context manifest with its size, or as left out with the reason (a context-engine
  section with no body and a reason is now recorded as excluded); mapping is bounded to 20 seconds there. (4) **UI**: the
  Repo map page (Knowledge group, `/repo-map`, `repomap.get`) shows the ranked map of the project's repository with a
  focus-path field, a token budget and the ranking table; without the parser it says so plainly. The Settings "not built"
  entry is gone. (5) **CI and build**: the `go` and `repomap-cgo` jobs run with `CGO_ENABLED=1` (and the `go` job also
  vets and tests the build without CGO), the `app` job builds drydock.exe with CGO and checks with `go version -m` that
  tree-sitter is in it; `task build:windows` builds with CGO. From Linux the product cross-compiles with
  `CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -tags production -ldflags "-w -s -H windowsgui" -o drydock.exe .`
  (verified here: a 100.4 MB PE32+ GUI exe, 11 MB more than without CGO). The Windows verification bundle stays CGO-free
  (it is cross-compiled on a host that may lack MinGW), so its repo map tests check the build without the parser.
  Tests: `internal/repomap` (the pure half with a labelled test double of the parser: ranking, focus, budget, filters,
  caches, in both builds; the real parser over `testdata/polyglot`, every language's definitions and references and a
  whole map of a real git checkout with ignored, vendored and generated files; the build without CGO), `internal/git`
  (`ListFiles`), `internal/mcp`, `internal/context`, `internal/pipeline` (the manifest's repo map line),
  `internal/core` (`TestRepoMapPageAndAgentsSection`, the gateway's `repo_map` through a real MCP client), the page's
  browser tests and `ui/e2e/repomap.spec.ts` (the real parser in the harness). **Not verified:** the CGO exe run on
  Windows (CI builds it; nobody has opened it), Defender and SmartScreen on it, whether the AGENTS.md map lowers what a
  task costs (no eval compares), and a real agent CLI calling `repo_map`.
- **P63. Every turn of an agent is routed (2026-10-15, on the owner's request).** The owner asked to route every turn. Before
  this, only work and review turns were routed; room replies never reached the router, and a template role turn (pm, tests,
  architect, security) was decided on the worker's list, recorded as the worker's work turn, and then run on the role agent's
  configured model regardless. Now (1) **every agent turn goes through the same router with the same rules**: the agent's own
  list of models (none: `static`, as before), the same tiers and Balanced level, budget checks, escalation, the P53 fallback
  chain and the same decision records and "Why this model?". The router chooses only among that agent's models on its own CLI
  and login (D1); a person's chat keeps the model the person picked. (2) **Each kind of turn is a run kind of its own** that a
  rule can match (`run.kind`): `room_reply`, `spec`, `tests`, `design`, `security_review`, with a task class and weight
  (`router.TurnSignals`): a reply is light (cheap first, no escalation counters), the spec (ambiguity 0.7) and the design
  (`architecture`) are premium under the shipped rules, the tests are work (cheap first, tool calling required, escalating on
  their own counter), the security review is premium and, like a review, prefers a model other than the worker's. Counters are
  per kind, so tests turns no longer count as cheap work iterations. (3) **The shipped `reviewer-differs` rule covers the
  security review**; a stored copy whose condition was never edited is brought up to date at start, an edited one is left
  alone. (4) **Shown where decisions are shown**: the run's node panel, the task's decisions (labelled by kind), the room's
  turn list (Model and "Why this model?"), the reply's payload (`model`, `decision_id`); the Routing page's preview and rule
  editor offer the new kinds. A reply's decisions carry its room turn id as run id, so project export and deletion include
  them; the evaluation's stratum still comes from the task's own work and review turns, and a reply's decision gets no
  task outcome and is left out of the routing report (a review finding, fixed the same day). `docs/model-routing.md`, "As built:
  every turn of an agent is routed". **Not routed:** the team lead's and the recommendation turns (P52), the planner. **Not
  verified:** with a real CLI (the scripted fake agent stands in).
- **P64. Crew HQ: the UI's second design (2026-10-15, on the owner's request).** The owner found the Linear-style UI "okay, but
  plain", asked for a facelift inspired by OpenAI's Dots, X's Grok Bot and Amp, rejected a first pass that only restyled the
  sidebar, and chose "Crew HQ" from three full redesigns shown as mockups (an inbox-and-board tool, a conversation-first
  app, and this). The look: a warm light canvas with white cards, black pill buttons and blue links (the dark theme is the
  same page at night); every agent is a "dot", a disc in its own bright colour with two rounded-square eyes that blink,
  look around while it works, look up while it waits on you and close while it is paused (`ui/data.tsx` Avatar; the eyes
  are decoration, presence is always in words beside them). The navigation: the sidebar's 38 links in 8 groups became one
  top bar with five tabs of a project (**Overview, Tasks, Rooms, Crew, Knowledge**) or four of the workspace (Home, Chats,
  Library, Security scan), search, the bell (Needs you, with its count) and the gear (Settings). A tab with sub-pages shows
  them as pills under the bar (Tasks: Board, Plan, Pull requests, Milestones; Knowledge: Requirements, Expert packs, Skills,
  Memory, Repo map; Overview: Activity, Budgets, Evals report, Security; Library: Agents and MCP servers, Skills); every
  settings page sits in a rail behind the gear (this project, the workspace, the app); the rooms in a rail on the Rooms tab.
  Nothing was removed: every route, screen, label and test id survives, and `ui/e2e/helpers.ts` knows which hub holds
  which link. The project's home is the new **Overview** (a greeting with the day's numbers, what needs you, the crew as
  dots with what each is doing, the tasks in motion, the rooms) instead of a redirect to #general; the **Knowledge** tab
  gathers requirements, expert packs, skills, memory and the repo map behind an ask box that starts a chat, with coverage
  and what was learned lately; the Overview's **Insights** card sums the spend, the week's merges, the first failing
  check and the open security findings. **Tasks** is a board (Up next, Crew working, Needs you, Merged / Done; a task with
  an item in Needs you lands there whatever its status) with the old grouped list one toggle away (remembered in
  localStorage); the **task page** reads like a story: the title with its state, the description as prose, a step bar
  (Spec, Code, Checks, Review, You, each done, current, to do or failed with a short note), then the evidence and reviews,
  with the decision card, the properties (writer and reviewer as dots, branch, cost, time, model, attempt) and the
  Terminal and Logs in a column on the right. The **Crew** tab shows each agent as a big dot with its presence in words
  and what it is doing; an agent's own page (`/team/:id`) has a hero with chips (presence, CLI and model, permission
  mode), Right now (the task, its progress, the run), its numbers (merged, reviewed, spent), Skills, Tools, Remembers and
  History. A **room** has a tile with its icon and its topic under the name, the agents' pill beneath, the person's own
  messages as a dark bubble on the right, a tool's check results under a tool tile, a task room marked by its writer's
  dot in the rail, and a round send button. Approvals still happen only where the item is decided (the task page, the
  project's Integrations), never from a card. Tests: `ui/app/shell/topbar.test.browser.tsx`
  (the bar, the hubs, the rails), `styles/contrast.test.ts` (every text pair of the new tokens at 4.5:1 in both themes),
  the screens' browser tests, and the whole e2e suite under the new navigation. **Not verified:** on Windows by a person
  (CI builds and tests it), and whether the dots read well to someone who did not ask for them.
- **P65. Models on this PC with llama.cpp (2026-10-15, on the owner's request).** The owner asked for "a source for those who
  want to locally host a model on their computer", chose llama.cpp ("the right way to go"), asked for Google's TurboQuant, and
  placed it under Providers ("I think that is what we intended": the brief's `host` kind). Built in `internal/localllm` and
  the core's `local.*` methods, `docs/local-models.md`. (1) **Official builds, checked.** Drydock downloads the newest weekly
  Windows build of `ggml-org/llama.cpp` for the chosen backend (Vulkan, CUDA, CUDA 12.4, ROCm, CPU; CUDA with its runtime
  archive), accepts it only when its SHA-256 matches the digest GitHub recorded at upload, unpacks it with the runners'
  guarded unzip into its own folder, and asks it for its devices. It does not run llama.app's install script (a remote
  script piped into PowerShell, as Llama-Windows does). (2) **TurboQuant.** Research on the day showed the TurboQuant KV
  cache types are not in llama.cpp: the maintainers closed PR #21089 on 2026-06-02 ("no one has presented evidence that would
  justify the opportunity cost"), while the Hadamard rotation of the attention inputs (PR #21038, merged 2026-04-01) gives
  the existing `q8_0`/`q4_0` caches most of TurboQuant's quality gain. The owner decided: "skip the forks, use the official
  builds and the q8_0/q4_0 cache"; then pointed at Atomic Chat, which runs upstream llama.cpp by default and the AtomicBot
  TurboQuant fork as a second, selectable engine. Drydock does the same: the official build is the default and the fork
  (`AtomicBot-ai/atomic-llama-cpp-turboquant`, tagged releases only, the same digest check) is a second **source** a person
  opts into behind a plain warning (a community build Drydock has not reviewed, weeks to months behind upstream); `turbo4`
  and `turbo3` are offered only while that source is chosen. The Python library the owner also sent (OnlyTerp/turboquant) is
  for Transformers and vLLM, not llama.cpp, and changed nothing. (3) **Models** come from public Hugging Face repositories
  (ggml-org's own GGUF uploads as suggestions, search for the rest), grouped into variants by quantization with shards
  joined, each file checked against the SHA-256 the Hub records, downloaded in the background with resume, with a fit hint
  (weights against one GPU's free memory, else three quarters of the free RAM). Gated repositories are not offered: Drydock
  holds no Hugging Face token. (4) **One server, one model, on loopback**, `llama-server --alias <owner/repo:VARIANT> --jinja
  -ngl auto -ctk/-ctv q8_0` on port 9933 (one above llama.cpp's own), waited for at `/health`, then registered as the provider
  "Models on this PC (llama.cpp)" (`kind=host`, new `flavor=llamacpp`, `private`, `cheap`, `max_concurrent=1`); the probe tells
  a llama-server apart by `/props` and lists its models as taking tool calls with the context the server runs with, so the
  router may give agent work to them (a host listing never said so before: `supported_parameters` is OpenRouter's). The
  router mode of llama-server (several models, `--models-dir`) is not used: its naming is undocumented; a restart switches
  the model. (5) **Reuse.** `runners.Download` grew `DownloadWith` (the archive's source prefix, size limit and progress);
  the device list, the fit and the server supervisor are new. **Tests** with a labelled stand-in llama-server (the test
  binary) and protocol fixtures of GitHub's releases API and the Hub, in `internal/localllm`, the core and the e2e harness.
  **Not verified:** anything against the real llama.cpp, GitHub release or Hub (asset names and layouts as read on
  2026-10-15; `local-models.md` section 9); the owner must run it on the PC once. Software: `tool:llama.cpp` (b11669 at the
  time of writing; not pinned, the newest checked build is installed).
- **P66. Drydock installs the scan's tools itself (2026-10-16, on the owner's request).** The Security page still said
  osv-scanner and Opengrep were "not installed" after P55, because Drydock installed neither and the owner's PC had neither;
  asked why not build the install into the app, the owner said to ("why don't we just build that into the app?") and asked
  for an Update button to the newest release. Built in `internal/scan/install.go`, the core's `security.tool.install`,
  `security.tool.install.cancel` and `security.tool.remove`, and the tools panel; `docs/security-scan.md` sections 4 and 5.
  (1) **The tool's own release, checked.** Install reads the tool's GitHub release listing without sign-in, takes the newest
  stable release whose Windows asset carries the SHA-256 digest GitHub recorded at upload (osv-scanner's
  `osv-scanner_windows_amd64.exe`, from its goreleaser name template; Opengrep's `opengrep_windows_x86.zip` from 2.0, with
  `opengrep.exe` and its DLLs, else `opengrep_windows_x86.exe`, as its `install.ps1` says), downloads it from that repository
  only, refuses a mismatch, and keeps it under the app's data (`tools\<tool>\<tag>\`), unpacked with the runners' guarded
  unzip where it is an archive. The same path as the local models' builds (P65); the same refusal of a remote install script
  (Opengrep's `install.ps1` is not run). Both licences allow this (Apache-2.0, LGPL-2.1 as a separate program). (2) **Order
  of precedence.** A program a person chose (P55) still comes first, then Drydock's install, then PATH and the installers'
  folders: a person's explicit choice is never overridden by a download. (3) **Update, on request.** The same call looks the
  newest release up and installs it when newer, else says "already the newest release"; Drydock does not poll GitHub for
  releases by itself (the Software Versions audit is where currency is measured). Remove deletes Drydock's copy. (4) **In the
  background, visible.** The install runs after the call returns; its state (phase, bytes so far, result or reason) comes
  with `security.tools` and each change is announced on `security.settings`, so every open Security page follows it and a
  page left and reopened shows how the last one ended. Only a person may install or remove (the program reads the whole
  project). **Tests** against a PROTOCOL FIXTURE of GitHub's releases API and downloads (`scantest.ReleaseFixture`) with the
  labelled stand-in in place of the installed program, in `internal/scan`, the core, the browser tests and the e2e harness.
  **Not verified:** a real download from GitHub (asset names and the digest field as documented on 2026-10-16); the owner's
  first Install on the PC proves it. Software: `tool:osv-scanner` 2.7.0 and `tool:opengrep` 1.31.0 (installed releases
  follow the newest; the audit measures the documented ones).
- **P67. Local models page: built in, "Not recommended", Close suggestions (2026-10-16, on the owner's request).** The owner
  asked whether llama.cpp and TurboQuant could be "built right into the app itself so we don't have to side load it", whether a
  model too big for the PC could say "not recommended", and for a way to close the suggestions. The first was already so (P65:
  Drydock fetches the checked build and the models itself; nothing is side-loaded or added to PATH) but the page did not say it;
  the runtime card now says before Install that Drydock downloads and runs llama.cpp itself (source, backend, digest check, its
  own folder, nothing by hand), with the official build as the default and the TurboQuant fork as a second source behind its
  unreviewed warning. The fit badge speaks plainly: "Fits the GPU", "Runs on the CPU (slow)", "Not recommended", short so a row
  does not wrap, with the full sentence and the numbers the core measured ("needs about 80.0 GB, 15.0 GB free", from the
  variant's size and `Fit.availableBytes`) as the badge's tooltip and, for the two that will not run well, in the row. "Runs on
  the CPU" is not "CPU only": with a GPU present llama.cpp offloads what fits, so the sentence says no GPU has room for the whole
  model. A not-recommended variant still cannot be downloaded (the UI is the only gate; the core refuses by disk only): a model
  llama-server could not load would only cost disk. "Close suggestions" / "Close results" takes the list and its open
  repository off the page so the section shrinks back; Show suggestions returns. Browser tests and the e2e spec cover the three.
- **P68. Settings are the app's; a project's settings live in the project; GitHub has one page (2026-10-16, on the owner's
  request).** The owner found the gear's rail cramped by the "This project" and "Workspace" groups and asked to keep Settings
  for the app only, with the project's settings under the project, and asked whether GitHub deserved a section of its own
  (yes). The Settings rail holds two groups: Workspace (Integrations, Connections, GitHub, Providers, Routing) and App (Settings
  and About, License, Backups, Export and import, Support bundle). Project settings and the project's Integrations are the last
  two pills of the project's Overview hub, set apart after a gap and a hairline (Project settings carries a small gear), so a
  person finds them where the project is and the Overview tab is current on them; a sixth top-bar tab would break the five-tab
  contract of P64 and a project rail would bring the crowding back. GitHub gets a page under Settings (`/github`): the gh
  sign-in state with Check again, what Drydock does with GitHub in plain words (pushes and pull requests through the person's
  own gh sign-in, CI and reviews read back, merge only after approval with branch protection respected, never a stored token),
  the GitHub connection and MCP server as the same panel the Integrations tile opens (one implementation, `VendorDetail`),
  and the repositories with CI runners, which are still managed on Connections. The GitHub tile stays on Integrations and
  points to the page. Test ids, URLs, Delete project and the palette entries are unchanged; the e2e helper knows the new
  places; the layout audit covers `/github`.
- **P69. A project's skills are one-line rows; every registry publisher has a mark; the Remix registry offers its whole skills
  folder (2026-10-16, on the owner's requests).** (1) The owner's screenshot of Knowledge > Skills showed four tall cards ("a 1
  line item ... and if we want to read more we can click on it and it will expand"). "In this project" is now `ProjectSkillRows`
  (skill-rows.tsx): per skill one row with the logo, name, first sentence and a state badge; the whole row is the button and
  opens in place to the existing `SkillPanel` (inline, no frame), one row at a time, in the same list style as the Library's rows
  (P52) rather than a third one. Test ids and data attributes stay on the row (`skill-tile`, `data-name/status/check/update`; the
  panel sits in `skill-tile-detail`). Registries stay tiles. (2) OpenAI, Remix, Stripe and Vercel tiles showed no logo: the
  Remix, Stripe and Vercel marks come from Simple Icons 16.35.0 (CC0), OpenAI's from LobeHub 1.95.1 (MIT) like Codex, Goose and
  Antigravity; Groq still has none (no licensed source). The "Cloudflare missing under MCP Servers" report was the connected
  Cloudflare API server: a connected server is looked up by its catalog id and `cfapi` had no alias (`cfdocs` had); fixed, and a
  browser test asserts every catalog brand and id and every `SKILL_BRANDS` value has a mark. (3) The `remix` registry root is
  `.agents/skills/` ("fix that to the right directory"; it supersedes the root P52 chose): all 19 skills reach the Library (the
  Remix 3 app skill and 18 written for contributors to the framework) and the person picks, as with every registry;
  `EnsureRegistries` forgets the old root's library rows so the next sync refills it.
- **P70. Dev Drives are detected without administrator rights, and the page says why none was found (2026-10-16, on the
  owner's report).** `fsutil devdrv query` needs administrator rights (Microsoft's fsutil page: "You must log on as an
  administrator ... to use fsutil") and Drydock runs as the signed-in person, so an unelevated Drydock never saw the owner's Dev
  Drive. Drydock now asks each fixed ReFS volume itself: it opens `\\.\X:` with no access right and sends
  FSCTL_QUERY_PERSISTENT_VOLUME_STATE (ntifs.h; the DEV_VOLUME 0x2000 and TRUSTED_VOLUME 0x4000 bits, Windows 11 22H2 and later),
  with fsutil only as the fallback when that query fails. The answer (`runners.DevDriveScan`) is asked once per core and shared
  by the CI runners' folder and the new project's folder hint, and when it is empty it carries a reason in words ("no fixed
  drive on this PC uses ReFS, and a Dev Drive always does"; "D: uses ReFS but Windows does not mark it as a Dev Drive"; "the Dev
  Drive check is not available on this Windows version (...), and fsutil devdrv query, which needs administrator rights, said
  ...") that the UI shows after "No Dev Drive was found:". The folder for Windows jobs is chosen with the window's folder dialog
  (the kit's Choose folder..., saved at once; the path stays in the box when the save is refused) or typed and saved with Use
  this folder. CI's Windows job runs the volume query against C: so the FSCTL path is exercised on a real Windows (never a Dev
  Drive, never a panic). **Not verified:** a real Dev Drive on the owner's PC; the Connections page saying "on your Dev Drive X:"
  after this build proves it.
- **P71. OAuth apps the owner registers; the Connections order; "MCP servers you added" (2026-10-16, on the owner's words:
  "Linear, Notion, Sentry, Aikido, GitHub don't have OAuth, can we fix that. It looks like we have OAuth for the MCP Server side
  of things"; "Under connectors lets group both claudes together and put custom at the end"; "lets fix that to say MCP Servers
  you added").** The vendors' MCP servers sign in without setup because they support dynamic client registration (P22); their
  own APIs do not, so a sign-in needs an OAuth app registered with the vendor. Drydock now takes that app's public client id from
  the owner (`integration.oauthApp.set`, kept in the `settings` table; a self-hosted Sentry's URL beside it; never a client
  secret: Linear uses PKCE, Sentry and GitHub the device flow), offers the registration on the connect form whenever a sign-in
  is off (one sentence, the vendor's registration page, the client id field), and turns the Sign in button on once saved; a
  build's client id still works and a pasted one wins. GitHub gets a real second method, Sign in with GitHub (device code),
  beside the gh way: its token goes to the credential store like the others and the same REST API gh speaks is called directly.
  Notion (public OAuth needs a client secret at the token exchange) and Aikido (client credentials only) keep their keys and say
  why in one visible sentence. Linear's loopback redirect port is fixed (41739) so the callback URL can be registered. The
  Connections tiles are ordered Claude Code (with its WSL installs), the other vendors by name (with theirs), custom manifests
  last (the core marks `custom` on the Connection). The section of servers added by hand is "MCP servers you added" with "Add
  an MCP server". **Not verified:** any live vendor sign-in; Linear's acceptance of a loopback callback and a secret-less PKCE
  exchange, GitHub's device flow details and Notion's and Aikido's constraints are from memory (their documentation was
  unreachable from the development host); whether Sentry refreshes a device-flow token without a client secret (its docs show
  client_secret on refresh). The owner's first sign-in with a registered app proves each.

**P72. Dropdowns are the kit's own control, never the native `<select>` (2026-10-16).** WebView2 draws the native option
  and suggestion popups itself: they ignore the app's theme, and the model field's suggestion list appeared where the OS put
  it, not under the field (the owner: "choosing model opus just goes far right"). Every dropdown is now `ui/app/ui/select.tsx`:
  a combobox button over a themed, keyboard-complete listbox in an anchored popover, controlled by the form (`value` and
  `onChange`, options as `{ value, label, hint?, disabled? }`), with `onChange` only for a different option (the agent form
  relies on it: choosing the adapter again keeps the model and effort). A model name is chosen from the adapter's aliases
  with "Another model..." for a typed full name (`ui/app/screens/shared/model-field.tsx`), so free text stays possible
  without a native datalist. Two free-text fields keep a native suggestion list (`<input list>`): the agent's role and an
  integration setting's choices, where the typed text is the value and the list only offers; they are the one place WebView2
  still draws a popup. Tests drive dropdowns through `choose()`, `optionsOf()` and `valueOf()` (browser) and `choose()` plus
  `data-value` (Playwright); a new screen that renders a native `<select>` is a review failure. **Not verified:** the look
  on Windows/WebView2 (Linux host only); the diagnosis of the far-right list rests on how WebView2 places native popups,
  and the fix removes every native option popup.

**P73. The 2026-10-16 audit: every finding fixed, in the order of its severity (2026-10-16).** A read-only audit of the whole
  repository (ten slices, one Critical, twelve High, twenty-eight Medium, fifty-seven Low) was followed by fixes for all of
  them, each with a test that fails without the fix. The pieces that change how Drydock behaves are listed here; the
  per-finding record is in the commit messages. **Git (C1, M5):** a call in a task's worktree is pinned to the git directory the
  repository registered for it (`GIT_DIR`, `GIT_WORK_TREE`), a folder git does not know as a worktree is refused, nested
  repositories (submodules, embedded repositories) are opaque: not staged, not asked whether they are modified, not counted as
  work, and a worktree that holds one is not removed without force (`internal/git/pin.go`, docs/security.md s3 item 2a). The
  skills scan asks git through the project's guarded `Repo`; the security scan's tools get git settings in their environment
  (`proc.GitConfigEnv`). **Not verified:** the same fixes against a repository inside a WSL distro (the `GIT_DIR` values go
  through the runner's path translation, covered by its existing tests, but no distro ran here) and on Windows git.
  **Files and programs (H1, H2):** a file Drydock reads from an agent-writable folder (project memory, the context engine's
  inputs, library versions, skills) is opened through `internal/safefs` (`os.Root`: a link that leaves the folder reads
  nothing), and every child process starts with `NoDefaultCurrentDirectoryInExePath=1` so cmd.exe does not run a `node.cmd`
  the agent left in its working directory (`proc.WithSafeExeSearch`, `proc.HardenProcess`). **Imports (H3, H4, M10, M11):** an
  archive is opened read-only through `storage.OpenUntrusted` with size and ratio limits, every table row passes an
  `Accept` hook before it is inserted (ids are checked against the importing project, settings through the same sanitiser
  the UI uses, evidence and node-execution references must stay inside `runs/` of the imported project), and the evidence
  readers open files only through `runfiles.ReadConfined`. **CI definitions (H5, M8):** a push whose diff changes a workflow or
  another CI definition waits for a person who sees the paths (`pipeline.GateCIPush`, evidence `ci_push_approved` bound to the
  head and the paths); the runner pool re-checks its registrations while it runs. **Updater (H6, M18):** an update is accepted
  only when its manifest carries a signature that verifies against the key compiled into the build, the download is bounded in
  time and size, the helper that restarts the app may break away from the app's job object. **Secrets in what is stored (H7):**
  agent and tool output, a run's records, check lines, room payloads, task feedback and evidence, and the events that carry
  them are scanned before they are written (`internal/redact`, docs/security.md s8). **Approvals (H8, M1, M2):** the text a person
  approves from puts the short fields (where a call goes) first, shows the whole input up to 64 KiB, cuts the long strings in the
  middle with the size of the gap written in, lists the paths and addresses apart and refuses what cannot be shown
  (`security.ReviewInput`); MCP calls and the permission prompt's `updatedInput` are made with the bytes that were hashed; a
  permission mode that skips approvals needs `confirmSkipsApprovals` on every path and is recorded as an approved
  `permission_mode_escalation` approval; a run withholds MCP tools whose text changed since a person reviewed them, tools marked as
  instructions and tools that were not in the reviewed list (`internal/mcp/toolreview.go`); the UI writes out control and format
  characters in agent text on every decision card (`ui/app/lib/visible.ts`). **Crashes (H9, H10, L4, L12):** a panic in a goroutine
  ended the whole app and every run in it; every goroutine now starts through `internal/safe`, the RPC dispatcher and the
  handlers that the ACP and MCP SDKs call on their own goroutines recover, a contained panic is logged with its stack and
  published once per place as `app.panic` (secrets scanned out), and the three known triggers are fixed (a read range near the
  largest int, a robots.txt that is not UTF-8, an empty hash file beside a backup). A process owns its data folder through a lock
  file (`internal/datalock`), so a second process cannot open a core on a live one; on Windows the updater's helper mode runs first
  in `main`, the Wails application object (whose single-instance check hands a second launch to the first and exits) is created
  before the core, a failed start shows a message box and writes `<data>\logs\startup.log`, and a job object that cannot be made
  no longer stops the start. **Not verified here:** every Windows-only piece (the job-object test, the updater's audit hook, the
  cmd.exe test, the start-up order, the message box) runs on the Windows runner only.
  **Spend and lookups (M4, M12, M21):** the budget gate is installed before any start-up step that can fail and fails closed (a
  budget that cannot be read, a turn whose scope cannot be resolved or a usage record that cannot be written stops the turn
  instead of letting it run unmetered); the role and planner agents of a turn are found through its input; a budget at its hard
  limit pauses the task for a person and the turn does not fall back to the agent's configured model
  (`router.BlockedError.Budget`). The indexes SQLite cannot match against a parameter (`WHERE col <> ''`) are full indexes
  (migration 00123, plan test `internal/storage/plans_test.go`); `task.list` takes a limit, `task.count` and the agent's
  `usageTotals` (a SQL sum) say how much there is in all, so a screen never presents the newest rows as the whole.
  **Merging (M14, L10, L15):** the stale-base check is made again under the repository's merge lock (`Repo.MergeChecked`), together
  with a check that the branch is still at the commit the merge was prepared for; the person's answer counts only for the commit
  the request showed (`review_requested` evidence) and the reviewer was shown, and a branch that moved goes back to the checks;
  the patch id is `git patch-id --verbatim`, so a change of whitespace alone is a different change; a merge that fails for any
  reason leaves the main working tree clean, a conflict list that cannot be read is an error, and restoring a safe point removes
  files only inside the worktree. **Presence (M15):** the rebuild at start reads the lifecycle events of the last two days
  (`room.LifecycleFilter`, `events.SQLBus.SeqBefore`) and shows every agent that is not running as stopped, instead of reading a
  month of tool output. **License clock (M16):** a forward step of more than three days is remembered only after the clock has read
  that far ahead for three days (the start of a pending step is kept across restarts), so a clock that was wrong for an hour
  cannot leave a license judged by a date years away; `license.resetClock` puts a record that is ahead of a correct clock right,
  only then, and writes the old and new times to the audit log. **Secrets (M17):** `scrub.Text` masks every copy of a finding's
  value, not only the one the scanner located.
  **Agent CLIs and the folder they work in (M3, M7):** a manifest lists the files its CLI acts on (`repo_config`), and the runtime
  does not start the CLI in a worktree that has any until a person trusted their fingerprint (`project.agentConfig`,
  `project.trustAgentConfig`, docs/security.md s3 item 2b); the sandbox definition on the default branch runs only when it is the one
  the person confirmed when they switched the sandbox on, and a merged change is listed and confirmed with `sandbox.trust`
  (docs/sandbox.md).
  **Output and processes (M6):** what a command prints is bounded wherever it is kept: a git call keeps at most 256 MiB of
  standard output and is ended past it (`git.ErrOutputTooLarge`), a check keeps the first 2 MiB and the last 4 MiB of its output
  in memory (`proc.HeadTail`), writes its log file up to 64 MiB, collects failing test names as the lines go by and keeps
  reading after a line too long for the scanner (it used to block the command until it timed out), a preview's log and a
  sandbox command's log stop at their limits, and the container client's answer is capped. Every command the app starts
  directly (`proc.Harden`) stops waiting for its output pipes five seconds after it was cancelled, so a child that holds them
  cannot keep a timed-out call alive, and opens no console window on Windows. **Not verified:** the no-window flag
  (Windows-only code; vet only here).
  **Network and credentials (M9):** MCP sign-in goes through `internal/netguard` (non-public addresses refused, the checked
  address dialled, a local-network server does not open the network, the callback answers only its own state); an upstream
  response is cut at 64 MiB; a provider's stored key is forgotten when its address changes (another scheme, host or port)
  unless a key is given for the new one, and its client refuses a redirect to another address (other headers than
  Authorization travel on a redirect in Go); an MCP server's bearer token or secret is not handed to another address or
  another program (docs/mcp.md).
  **Updates, retention and what is stored (M13, L5, M22, M26):** an update that was staged is remembered (`updatepolicy.Record`,
  setting `update.pending`), and the start of the new version releases the pre-update backup from the prune exemption after it has
  run two minutes (`updatepolicy.ConfirmHealthy`); a marker that nothing released stops holding its backup after 30 days
  (`backup.MaxKeepAge`), so no update leaves a database-sized file behind for good. The daily retention pass also purges recorded
  telemetry past its window. An event sent over the follow stream keeps its type and ids, and an oversized `data` (over 256 KiB,
  `core.MaxFollowData`) goes as a stand-in that says how large it was and keeps its first 4 KiB (`events.ForTransport`); the bus
  itself stores nothing larger than 256 KiB whole either (see L2 below), the Claude parser keeps at most 256 KiB of one tool result, and
  a memory proposal's title and body are limited (200 and 8,000 characters, `memory.CheckSize`) before they reach a person.
  **Text that reached the lead (M27):** a follow-up the team lead splits off carries its origin (`lead:N`, N steps from a task a
  person made; migration 00124, `Task.Origin`), stays in BACKLOG until a person moves it on even under automatic scheduling
  (`Task.WaitsForAHuman`: the scheduler does not promote it and the lead is not asked to staff it), and is limited to two steps
  and 25 unfinished follow-ups per project (`tasks.MaxLeadDepth`, `tasks.MaxLeadOpen`), counted in the transaction that inserts,
  so text from a file, a page or an issue cannot fan out into paid work on its own. A split over a limit is refused whole and
  becomes a question to the person. **Gates with no verdict (M28):** a blocking gate (the verification run, a security scan
  gate) whose every check was skipped or could not run no longer passes: it asks a person, who can have it check again, go on
  without a result this time (`{"waive_verdict": true}`, for that exact content, recorded as `waiver` evidence, not accepted
  from an agent) or stop the run (`internal/pipeline/verdict.go`).
  **Environments, local models and network edges (L6, L7, L8, L19, L24, L25, L31-L33, L53):** a proxy address reaches an
  agent's CLI and a project command without its password unless the person passes the variable on by name
  (`proc.WithoutProxyCredentials`), and a git call that stays on this machine starts from the allowlist of project commands, not the
  app's whole environment (push, fetch and the other calls that reach a remote keep it: they need the person's login); a scrub that
  keeps nothing is an empty environment, never "inherit everything". A plain MCP environment value the secret scanner would change
  is refused, and the sign-in tokens of OAuth servers are taught to the scanner at every start. Workspace and service containers drop
  all capabilities but the nine development tools use and get memory limits with no swap behind them (not run against a real engine
  here); the egress proxy shares Drydock's own list of refused addresses (NAT64, 6to4 and the reserved range included) and closes idle
  tunnels. The local model server demands a random key (in its environment, new at every start, held by its provider row) and
  Drydock checks `/props` with it before it calls the server ready; `llama-server --list-devices` runs in the same trimmed
  environment; a model folder name can no longer be `..`. The gateway's failure bookkeeping is bounded by the shape of a run id and a
  global ceiling. A staged restore is found again by name in the backup folder, as a plain file with the staged hash.
  **Licence, the event bus and smaller edges (L1, L2, L3, L9, L11, L13, L16, L17; the core side of L38, L39 and M23):** `recommend.start` is a paid-work method like
  `task.start` (`gatedMethods`, and `Core.Recommend` asks `RequireLicensed("start_work")` itself); keys that cannot be read leave
  Drydock read-only and say why, not "unlicensed" (`keysBroken`, fail closed), a licence file is read up to a limit (`maxLicenseFile`),
  and a licence is good only for the app versions it names (`min_app`, `max_app`; the unused `mid` claim is gone). Every event is
  stored with at most 256 KiB of `data` (`events.MaxEventData`; a larger one is a stand-in that says how large it was and keeps its
  first 32 KiB, data that is not JSON likewise), and a consumer that must see every event of a kind reads through `events.Watch`/
  `events.Consume`, which subscribe before they return and, when the bus drops a slow subscriber, replay what was missed and
  carry on, where a bare `Subscribe` loop ended for good (the responder, presence, economics, the sandbox, agent and skill watchers,
  the adapter-drift and limit watchers). A terminal keeps its last 1 MiB for a reader that falls behind but cuts back once it holds
  2 MiB, in place, instead of copying a megabyte for every 32 KiB read, and a terminal whose program ended also ends what it left
  running and closes its job. A preview command's printed address is probed only when it is on this computer (and a redirect
  to another computer is not followed), and the e2e check copies a screenshot only when it is an image inside the worktree's
  `test-results` or `playwright-report` folders. A task that waits at the collision guard works out its own footprint once and
  looks again less and less often; the scheduler says why a task waits once, not at every pass. The text of an instruction
  file the repository brought is introduced as the repository's own, and a person's edit of a generated file is offered back
  to memory once, redacted and cut to what a person can read whole. The refusal of an environment variable name that is not one (a
  pasted `NAME=value`) names the line and does not repeat what is on it (`agentenv.set`, `mcplib.save`). A support bundle is never written over a file unless the request says so (`supportbundle.create {overwrite}`; an
  existing file is answered `{exists: true}` and left alone) and never into the app's own data folder outside its support folder
  (L39 core side). A link the window shows is opened in the person's own browser through the shell: `shell.openExternal {url}`
  passes only an http or https address with a host and no password and a plain mail address (`core.ExternalURL`) to
  `Config.OpenURL` (Wails `Browser.OpenURL` in the desktop app, which hands anything to the system's handler and so must never
  see a program path, a file or a protocol such as `ms-msdt:`), for a person only (M23 core side). **Not verified:** the Windows
  shell's `OpenURL`; nothing else in this batch is Windows-only.
  **Status writes, the Claude stream and small leaks (L14, L26, L27, L29, L30):** an answer to a request that none of the step's
  edges takes is refused with the answers it does take (`workflow.ErrNoRoute`; a rejection still ends a run that has no edge for
  it), a run cancelled while its answer was being recorded is not brought back to RUNNING and the answer is not recorded, a paused
  run is resumed only while it is still paused, and a failed run's status is written with retries. An event of a known type in a
  shape the Claude parser cannot read is counted as drift and warned about once, a line that starts like an event but is not JSON
  likewise, and an unreadable `result` ends the turn with an error (`Parser.unreadable`). The Claude session's stdout reader gives
  up delivering once the session is stopped, so a reader that left with a full buffer no longer holds it and its process for good.
  An MCP write approval whose call ended is cancelled with a context that outlives the call (it stayed open for 30 minutes), and
  the docs now say what is true of ACP permission requests: no handler asks a person, every one is refused, so an ACP agent that
  asks before each tool call can do nothing that needs asking (the approval path in security.md s6 is a design, not built). A pack
  the last run left "refreshing" is failed when the store opens, so it is tried again. **Not verified:** the ACP approval path is
  not built.
  **Reads, writes and files (L18, L20, L21, L22, L23, L28, L34, L35):** the sidebar's unread counts stop at 1,000 per room and skip
  messages without mentions; a room export is read, rendered and scanned outside the write transaction and takes at most 20,000
  messages; a run explanation reads a run's manifests and a task's usage once, and the evals report reads at most the newest 2,000
  tasks and says what it left out. A project archive is limited to 100,000 entries, 4 GiB, 1 GiB for `project.db` and 512 MiB for
  any other file, and its rows are written in one transaction that is given three minutes of the one writer (kept atomic on
  purpose: an imported project exists whole, with everything held back that has to be, or not at all). The database's durability is
  stated as it is (WAL with `synchronous=NORMAL`: a commit survives the app crashing, a power cut can lose the last moments) and
  pinned by a test; `FULL` was measured and not chosen. The diff viewer asks git for a file's size before reading it
  (`Repo.BlobSize`, `ShowFileMax`; a blob over the limit is never read into memory), and retention keeps the files in a run's
  `evidence/` folder that a task's evidence still names. A read-only Claude session is denied `PowerShell` as well as `Bash`. The
  skill glob matcher remembers its answers, so many `**` in a glob no longer take exponential time. The Windows runner's start-up
  sweep deletes only folders named like this app's jobs. **Not verified:** `PowerShell` as a tool name and the sweep run on a PC.
  **CI, the repository's own checks and records (M19, M20, L49 to L52, L54 to L57):** every job of every workflow names its
  GitHub-hosted runner outright (`windows-latest`, `ubuntu-latest`); the repository variables `WINDOWS_RUNS_ON` and
  `CONTRACT_RUNS_ON` that could send a fork's pull request to a self-hosted runner on the owner's PC are gone, and so are the
  self-hosted instructions for a public repository. Each workflow has a read-only `permissions:` block, each job a time limit and a
  checkout that does not keep the token in the workspace, and a PowerShell step of several commands stops at the first one that
  fails (a failing `go vet` was masked by the command after it); `windows-probes` installs the `wails3` CLI at the version `go.mod`
  uses. `internal/ciconfig` reads the workflows and fails if any of that stops being true. The contract workflow's sign-in belongs
  to the environment `contract` (only `main`), only its suite step holds it, and the newest agent CLIs are installed by
  `drydock-contract --install-latest` in a step that holds no secret, with npm in a scrubbed environment (`--ignore-scripts` cannot
  be used: Claude Code's npm package links its native binary in an install script), and run by `--latest-from` in the suite step.
  `security.yml` runs `govulncheck` (pinned to v1.9.0, as the audit tool does), `npm audit` and CodeQL weekly, by hand and when a
  dependency file changes, outside the merge gate; Dependabot proposes updates with a 14-day cooldown; `SECURITY.md` says how to
  report privately; a test scans the tracked files for secrets with the product's own rules, and `.gitignore` now names env files,
  databases, keys and Playwright results (a test fails if a tracked file is one it ignores; the stale `test-results/.last-run.json`
  is untracked). `drydock-audit` no longer publishes false assurance: a program that exits with a failure is an error even if it
  printed something, a package or module whose lookup failed is "unknown" (not "current"), a scanner that was cut off or answered
  with an error says "did not run", and the exit status is 2 when any lookup failed. The trial's acceptance runs the agent's code in a
  `proc.ToolEnv` environment with scratch home and temporary folders, and accepts only when every hidden test was seen to report a
  pass (`go test -json`), so a `TestMain` that never runs the tests cannot pass it. Recorded runs are written without the account's
  plugins, connectors and commands or the machine's Claude and session folders (`Record.Write`; `drydock-trial redact` rewrote the
  committed ones). `THIRD_PARTY_NOTICES.md` is generated (`cmd/drydock-notices`) for every Go module in the build and every UI
  package the runtime dependencies install, names the MPL-2.0 ones with where to get their source, and says what `LICENSE` says; a
  test keeps it equal to the measured module list (M20 is open item 7 of section 4: the license itself is the owner's call).
  **Not done or not verified:** `Vampire/setup-wsl` stays on its major tag (pinning it to a commit needs its repository, which was
  out of this work's reach); the environment, its secrets, private vulnerability reporting and the history of the old eval
  transcripts are the owner's (open item 8). `security.yml`, CodeQL and the new Windows runs of the tests first ran on the pull
  request: CodeQL's nine alerts (integer conversions, a path guard in two archive readers, a credential in a probe's output, a size
  computation) were fixed in the code except two it cannot be satisfied on (a certificate pin, and an authorization server stand-in),
  whose queries the configuration switches off, with a test that holds the code to the reason (see `docs/security.md`); the Windows runs found a lock that hid its owner's process id (`internal/datalock`), tests that compared a
  path by its spelling or read a source file with CRLF, and tests that need the repository where the verification bundle has none.

**P74. The Security page says where rules come from, and the rules folder is chosen with the folder dialog (2026-10-19, on the
  owner's words: "I installed OpenGrep but I can't run it without rules where do I get the rules?").** The page asked for "a
  folder of rules you are licensed to use" (P44) but offered only a typed path and said nothing of where rules come from, and a
  scan without them skipped the Code engine with a reason that sat in the report, away from the field that fixes it. Now:
  (1) **Where do I get rules?** under the rules folder lists public repositories of Semgrep-syntax rules with what each one's own
  licence file or README said when it was read (2026-10-16: the Opengrep set LGPL-2.1 with the Commons Clause and "research,
  testing & benchmarking"; Semgrep's a licence file that points to its terms, which are linked, not restated; Trail of Bits AGPL-3.0;
  0xdea and dgryski MIT), a Git command that clones one, and one rule to try the Code engine with; it is open while the folder has
  no usable rules, folds away once it has, and keeps what the person did with it. (2) **Choose folder...** (the window's folder
  dialog through `fs.pickFolder`, P58) saves the folder at once, with the call Save makes; only a person may, cancelling changes
  nothing, and without a dialog (the browser in development) only the typed path is offered. (3) The Code engine's skipped reason
  ends with where to choose rules and where to get them; the scan's summary keeps its first sentence. (4) The rows of osv-scanner
  and Opengrep, wrapped in a block by P66's install rows, are laid out as the list's rows again (their name ran into the text
  after it; a browser test reads the rows' computed layout). **P44 stands:** Drydock
  still ships and downloads no rules; the page lists and links, recommends no set and gives no legal advice, and the one rule on
  it is help text the person saves themselves (a test keeps it a valid rule file). A starter set of Drydock's own would reverse
  P44 and is the owner's call. Document: [`security-scan.md`](security-scan.md) section 5. **Checked:** the page's rule was run
  through Semgrep OSS 1.181.0, the engine Opengrep was forked from: two findings in the two Go files, none for `fmt.Printf`,
  `fmt.Sprint` or a Python file; its SARIF is now a test fixture of the reader. **Not verified:** the rule and the Git command
  against Opengrep itself and a real clone (no Opengrep binary can be run on the development host); the Semgrep Rules
  License's terms (the page was unreachable from there); the look of the button and the dialog on Windows.

- **P75. The UI audit of 2026-10-19: one type scale, a Guide for the detail, and each fact said once (2026-10-19, on the owner's words:
  "the UI still needs a little bit of love ... duplication, things that are too close between columns and tables, type things, maybe
  too much context or information ... maybe we should build a guide and reference most things there and keep the really basic info on
  the main UI", then "do all high, medium, and low").** The audit ([`ui-audit-2026-10-19.md`](ui-audit-2026-10-19.md)) measured 41
  pages of the real app and found 33 type sizes against a scale of 6, 60 paragraphs over 200 characters, 18 pages saying the same
  thing twice or more, 58 targets under 28 px and a dozen rows whose neighbours were closer than 8 px. What was built: (1) **Type**:
  a title token (28 px), four weights (400, 500, 600, 700), one size for code (`--text-sm`); `styles/source-rules.test.ts` fails a
  literal size, a fifth weight or "file(s)" in the source. (2) **Frame**: reading columns are left-aligned under the title (they were
  centred, which put their left edge at a different x on each page), every page title has an icon (`Page`'s `icon` is required unless
  the page draws its own head), titles are in sentence case. (3) **The Guide** (S23): 31 topics; the pages lost their intros
  (Routing, Settings, Budgets, Connections, Library, Skills, Packs, Project settings, GitHub, Security, Evals, Providers, Support,
  Backups, Data, License, Memory, Requirements, Repo map, Plan, Pulls, Activity) and keep one subtitle line, their controls and their
  data; a warning that belongs to a click (the sandbox has no shell, workers may run the project's own code, the scan only reads
  files) stays beside it, shortened. The Evals page's full report is behind a disclosure; the Crew page's eight adapter cards are one
  table; the shared `Disclosure`, `dataTable`, `listBox`, `stackList`, `tightList` and `actionsEnd` replace copies in a dozen files.
  (4) **Each fact once**: the Overview's figures are in the Today card only; a routing rule shows its priority and its action once
  (the exact JSON is the badge's tooltip); a run status is said in words, once; an agent whose program is not running is "Stopped"
  everywhere (it was "Resting", "stopped" and "Paused (stopped)"); a tile does not say "not connected" under a badge that says it;
  `/mcp` opens `/integrations`; "task(s)" and its kind are counted nouns (`plural`). (5) **Room and targets**: no two neighbouring
  texts closer than 8 px, checkboxes and radios 18 px inside a 28 px label, switches 40 x 24, filters and tabs 28 px. (6) **Kept
  fixed**: `ui/e2e/density.spec.ts` measures every page at 1366 and 1024 wide and fails on a paragraph over 200 characters, a size
  or weight off the scale, neighbours under 8 px, a control under 28 x 24, text said three times, or horizontal overflow; it lists
  the one exemption it has (a container engine's own error). **Not done:** the Guide's topics are written from what the pages said and
  the docs; nobody who has not built Drydock has read them yet. The look on Windows (Segoe UI at 125-150 %) of the larger switches and
  the table is not seen.


- **P76. Skills belong to agents, never to the whole team; the Crew page is a dashboard (2026-10-20, on the owner's words: "Remember
  skills are agent-based, not entire project-based. Each agent needs to be a specialist. We don't want all our agents being the jack of
  all trades. They need to master their role", "we don't see the ability to add or remove skills from Agents when in the project", and
  "let's not show the first agent on the window; let's see more of a dashboard ... and let us click on the agents to see the additional
  info").** Supersedes the whole-team audience of P35. (1) **Who gets a skill**: only the agents it is given to (`skills.Reaches`). A
  new skill starts with the agents whose role it names (`drydock.roles`), or with nobody when it names none; an agent added later gets
  nothing by its role. `skill.audience` refuses the whole team with the reason; a Library add takes the agents chosen (they replace the
  role's suggestion). (2) **Existing skills**: every whole-team skill is given, at start and after a project import, to the agents it
  reached (its roles' agents and the agents given it); nobody loses a skill (`GiveTeamSkillsToAgents`, logged and published as
  `skill.audience` by `drydock:P76`). (3) **Where**: an agent's own page lists its skills, each with Remove, and adds more from the
  project's skills or straight from the Library for that agent alone, a skill naming its role first, marked as suggested; the Skills
  page asks which agents before adding from the Library (no "Add all from a registry" there any more) and its editor is a list of
  agents; the new-agent and edit forms tick skills, nothing locked by role. (4) **The lead's recommendations** name the agents for
  every skill; one for nobody in particular is dropped with the reason, and an older "whole team" item cannot be applied. (5) **The
  trial's Drydock arm** gives its bundle to the worker and the reviewer, as every recorded run had it. (6) **Crew**: the figures (at
  work now, tasks in progress or review, tasks waiting for you, skills given and agents with none), then one compact row per agent
  (dot, name and role, what it is doing now and its task, CLI and model, its skill count, its actions); a row opens the agent's page.
  Tests: the skills package (giving by role at insert, refusing the team, converting old skills with nothing lost), the core (start-up
  conversion, prebuilt agents, recommendations), the screens, and `ui/e2e/agent-skills.spec.ts` (a Library skill added on one agent's
  page reaches only that agent, and Remove takes it away). **Not verified:** whether agents do better as specialists; no trial was run.
- **P77. The download comes with osv-scanner, Opengrep, built-in rules and llama.cpp with TurboQuant; newer releases are offered
  (2026-10-20, on the owner's words: "I want the app to already have the dependencies installed for OSV-Scanner and OpenGrep and then
  have the ability to update them if they are out of date", "I don't want to choose where the opengrep rules are; just build them into
  the app", and "I don't want to install llama.ccp, I want it already installed with Google's TurboQuant built in ... If there are
  updates to llama.ccp, we will do those periodically when checking for updates").** Amends P44 and P66 (Drydock ships the tools and
  rules now) and P65 (the bundled build is the TurboQuant fork). (1) **The bundle**: `packaging/bundle.json` pins osv-scanner v2.7.0,
  Opengrep v1.31.0 and the fork's release `b10298-2.0.0` (its newest release, 2026-10-05; its per-commit `turboquant-windows-x64-*` tags are pre-release dev builds, which Drydock does not install). The CI app
  job runs `cmd/drydock-bundle`, which reads each pinned release, downloads its Windows file from that repository only, refuses it
  unless the SHA-256 GitHub recorded matches, and writes `tools/` beside `drydock.exe` with `tools/bundle.json` (the files, their
  addresses and digests). The artifact is `drydock-windows`. (2) **Seeding**: at start, the core copies each bundled file into the
  installer's cache after checking it again (`bundle.Stage`) and runs the ordinary install, which checks it a third time and fetches
  nothing. A tool the person removed stays removed (`<tool>.removed`, `removed.json`); a build the person installed is left alone; an
  app update with a newer bundled build replaces only an earlier bundled one (`bundled.json`). The first bundled llama.cpp sets the
  settings to the fork, its backend and `turbo4`, unless the person already saved settings. (3) **Updates**: three minutes after start
  and every twelve hours the core reads the newest release of each installed tool and of the installed llama.cpp source and backend;
  a newer one is shown ("Update to v1.32.0") and installed only when the person presses it. **Check for updates** on the Security page
  and on the llama.cpp card checks now. (4) **Built-in rules**: `internal/scan/rules` is compiled in (`go:embed`), written once per
  content digest to `<data>\rules\builtin-<digest>\` and given to Opengrep first (`-f`) on every scan; a folder of the person's own
  is optional and added with a second `-f` when usable. The sets: Drydock's own 34 rules (JavaScript and TypeScript, Python, Go, Java,
  C#; each with a fixture that a rule-engine test runs, 34 of 34 passing under Semgrep OSS 1.181.0), and two MIT sets copied at a fixed
  commit: dgryski/semgrep-go (66 rules) and 0xdea/semgrep-rules (49 rules, C and C++). Left out on licence grounds: opengrep-rules
  (Commons Clause, "for research, testing & benchmarking"), semgrep-rules (Semgrep Rules License) and Trail of Bits' set (AGPL-3.0),
  which stays the owner's call. Tests: `internal/bundle`, the seeding in `internal/scan` and `internal/localllm`, the core
  (`TestTheDownloadsScanToolsComeInstalledAndAreOfferedUpdates`, `TestTheDownloadsLlamaBuildComesInstalledWithTurboQuant`),
  `cmd/drydock-bundle` (built from checked releases; the pins match `docs/software-notes.json`), the screens, and
  `ui/e2e/security.spec.ts`. **Not verified:** the pinned release files were not fetched from the development host (GitHub's release
  downloads are blocked there), so the first CI run of the app job is what proves the tags and file names; the fork's code was not
  reviewed; the built-in rules were not run by Opengrep itself, only by Semgrep OSS, whose rule syntax Opengrep reads.
- **P78. Everything moved to its newest release again (2026-10-20, on the owner's words: "Lets go ahead and get things to their
  latest update").** As in P23 and the round of 2026-10-16: Go 1.27.3 (go.mod; the CI jobs read it from there), every Go module
  (`go get -u -t ./...`: openai-go 3.82.0, Wails v3 beta.34 published the same day, modernc.org/libc 1.79.0 and the indirect
  ones), lucide 1.60.0 (with npm 11, as CI's Node 24 ships, so the lock file keeps its platform fields), the agent CLIs and
  bridge (Claude Code 2.1.304, the Claude ACP bridge 0.94.0, OpenCode 1.19.3, goose 1.55.0, Cline 3.0.73), the sandbox's
  suggested images on Debian trixie (13) instead of bookworm, the CI runner image on Ubuntu 26.04 LTS (the runner's own
  dependency script lists its libicu), the skills the prebuilt agents name at the newest commits of anthropics/skills and
  cloudflare/skills (each SKILL.md read again: only wrangler's description changed, every licence the same), and the Linear schema
  Drydock follows to `@linear/sdk` 98.0.0 (every type and field Drydock's queries and mutations use is unchanged). Checked here:
  the agent CLIs with the contract suite's free contracts (11 passed, 0 failed) and the live ACP handshake, without a sign-in;
  the Windows exe built with CGO; the Go and UI suites. **Left as they were, with the reason on the Software Versions page:** the
  Phase 0 probe workflow's pins (its evidence), the Docker and git versions the tests were measured with (no newer engine or git
  ran here), the development host's Node and npm, and the genproto root module (a pseudo-version that moves with the libraries
  that need it). **Not verified here:** a paid contract or a prompt turn on the new CLIs (no sign-in), the runner image on
  Ubuntu 26.04 (CI's sandbox job builds it and starts the runner), and the trixie images (suggestions; nothing pulls them in
  the tests).
- **P79. The holds left by P78 are cleared where Drydock controls the pin (2026-10-20, on the owner's words: "fix those").** The
  Phase 0 probe workflow follows the newest releases now (Go 1.27.3, Node 24, Claude Code 2.1.305, Codex 0.163.0, Gemini CLI
  0.65.0, OpenCode 1.19.3, goose 1.55.0, the Codex and Claude ACP bridges 2.2.2 and 0.94.0, wails3 beta.34); a push that changes
  the workflow runs it again on Windows, so its evidence is re-recorded at these versions, and the audit no longer holds its pins.
  The manifests recommend Claude Code 2.1.305 and Gemini CLI 0.65.0, released after P78 (the free contracts: 9 passed, 0 failed;
  Gemini's live ACP handshake with the same capability matrix). Libraries only in the module graph (P23) are shown on the Software
  Versions page as "not compiled in" rather than as a gap: none of their packages is built into Drydock or its tests on Linux or
  Windows, and `go mod tidy` drops a requirement on a module that provides no package, so Drydock cannot pin them (the genproto
  root module among them; its hand-written hold is gone). **Still held, with the reason on the page:** wrangler 4.151.0
  (Cloudflare's newest `@cloudflare/vite-plugin`, 1.64.0, pins it exactly; 4.151.3 would put a second wrangler in the starter),
  and the development host's own Node, npm, git and Docker (changing this machine's setup was not allowed from the session; it
  is the owner's call, in the environment's setup script). The next morning's audit found anthropic-sdk-go 1.84.0 and openai-go 3.83.0, taken the same way
  (the llm, provider and probe tests pass).
- **P80. gh, Semgrep and llama.cpp moved to their newest releases too (2026-10-21, on the owner's words: "update gh, semgrep and
  llama.cpp too").** The GitHub CLI Drydock is written against is v2.102.1 (a patch release; still MIT, still user-installed and
  not bundled). Semgrep OSS, the development-only engine that checks the built-in rules against their fixtures, is 1.182.0: every
  fixture passes under it, and the real SARIF capture the reader is tested on was taken again with it
  (`internal/scan/testdata/semgrep-oss-1.182.0.sarif`, the same two findings and the same matched code as 1.181.0's). llama.cpp is
  not pinned (P65): Drydock installs the newest build that carries the backend's archive with a SHA-256 digest. Its builds come out
  several times a day (b11774 when this was written, b11776 an hour later), so a recorded number was behind by the next audit:
  the audit now shows it at the newest build, and docs/software-notes.json records none. The TurboQuant build in the download
  (P77) is unchanged, b10298-2.0.0 being that fork's newest release. The same audit found openai-go 3.84.0, taken the same way.
  Govulncheck now runs from the audit again (through the database's mirror, `DRYDOCK_AUDIT_VULNDB`); a finding that stops at the
  module, with the vulnerable package not imported, is no longer counted as in what is built (GO-2026-5932 on x/crypto's
  openpgp, which Drydock never imports; govulncheck's own report leaves it out too).
- **P81. The development host's Node, npm, git and Docker moved to their newest releases (2026-10-21, on the owner's words:
  "update node, npm, git and docker on this machine too").** Node 24.22.0, the newest of the project's line and the one CI
  installs (from nodejs.org, its SHA-256 checked against SHASUMS256.txt; 26 is newer but not yet LTS), with npm 12.2.0 on top;
  git 2.56.0, built from the v2.56.0 tag of github.com/git/git into /usr/local (Ubuntu 24.04 ships 2.43.0, and the host's network
  reaches neither kernel.org nor the git-core PPA); Docker Engine, CLI and plugins 29.9.2 from Docker's apt repository. Measured on
  them: the full Go suite (82 packages), the UI's 1472 tests, its build and the browser tests, and the sandbox tests against a
  running 29.9.2 daemon as CI's sandbox job runs them (24 passed, none skipped). npm 12 runs no package's install script unless it
  is approved; esbuild's, which only checks its platform binary, is skipped with no effect, and the lock file is unchanged. The
  holds on the four rows are gone. The host is a cloud container rebuilt from its environment: these versions last until it is
  rebuilt, unless the environment's setup script installs them too.
- **P82. Claude Code 2.1.306, and npm 12's install scripts (2026-10-21, on the owner's words: "update claude code to 2.1.306
  too").** The manifest recommends 2.1.306; the free contracts pass on it (8 passed, 0 failed, the Claude ACP bridge 0.94.0's
  handshake among them; no sign-in here, so no paid contract and no prompt turn), and the Phase 0 probe workflow, the WSL stand-in
  and its tests follow it. Installing it with the development host's npm 12 (P81) found that npm 12 runs no package's install
  script unless the command allows it: Claude Code's links its native binary, and without it `claude` does not start. Three agent
  packages ship one (Claude Code, opencode, cline), so the npm install line Drydock shows for each now allows its own package's
  script (`npm install -g --allow-scripts=@anthropic-ai/claude-code @anthropic-ai/claude-code`), and the contract tool's own
  installs (`--install-pinned`, `--latest`, `--install-latest`) do the same; npm 11, which Node 24 ships, accepts the flag too
  (checked with 11.19.1). Tests keep both.
- **P83. Every agent's permission requests reach the person (2026-10-21, on the owner's words about the CopilotKit/AG-UI
  comparison: "Lets go for it").** Until now only Claude workers' permission prompts reached a person (P40); every ACP agent's
  `session/request_permission` (Codex, Gemini CLI, OpenCode, goose, Cline) was refused by the default handler, so an agent that
  asks before a tool call could do nothing that needed asking. Each session now carries its own asker (`RunSpec.AskPermission`).
  Worker turns outside a sandbox get one from the runtime that sends the request through the same gate as a Claude worker's:
  the call is read as the analyzer's tool vocabulary only where that reading is exact (a bare command is Bash, one path read is
  Read, one path edited or deleted is Write), the rules allow or refuse what they decide, and anything else becomes an approval
  of exactly that call on the task page and in Needs you. Chats ask the person in the chat: the request is listed with the call
  and its input, Allow once or Refuse answers it, it expires after 30 minutes and is withdrawn when the turn ends. An approval
  selects the agent's `allow_once` option only, never `allow_always`. Read-only and sandboxed sessions still refuse. Not built:
  comparing the call the agent then makes with the approved one (docs/security.md s5).
- **P84. Tool calls are shown as cards with how they went (2026-10-21, the CopilotKit/AG-UI comparison, phase 2).** A chat's tool
  calls showed only the tool name and its input; whether a call succeeded, what it returned and how long it took were dropped
  (chats stored no tool result). Each call now keeps the CLI's id of the call, its status (done or failed), its result
  (redacted, at most 8 KiB) and its duration on its own message (migration 00126), `chat.tool_result` reports it as it happens,
  and a fork carries it. The UI draws each call as a card: a status icon (a spinner while its turn runs, a check, a cross, or
  "no result reported"), a readable name (MCP tools as "server · tool", Drydock's own as "Drydock · tool"), the call in one
  line and its duration; opened, an edit shows the change as removed and added lines, a shell command shows `$ command` and its
  output, anything else its input and result. All of it is text, never HTML.
- **P85. Narrow windows, and consecutive checks as one row (2026-10-21, the CopilotKit/AG-UI comparison, phase 2; the UI review's
  P0 items).** On Windows people snap Drydock to half the screen beside their editor (about 960 px wide), and there the details
  panel and the rails left a room's conversation a few hundred pixels wide; at phone width the page scrolled sideways. Below
  1200 px the details panel starts as its slim strip and, opened, floats over the page instead of taking its width (the choice is
  kept for the session only, so a wide window keeps the stored one). Below 900 px the Rooms rail, the settings rail and the chat
  list become drawers behind a button and close on navigation; with no chat open, the chat list is the whole screen. Below 1240 px
  the top tabs are icons that keep their names (title and accessible name), and below 720 px the bar wraps with the tabs on their
  own row and the Guide link, the composer hint, the dock's keycaps and the room's mode switch hidden. In a room, consecutive tool
  checks (lint, tests, review gates) outside a thread are one "Checks" row that says "N/M passed" or "K failed: A, B" with the
  total time, closed when all passed and open when any failed; each check inside keeps its status, its log path and, for a
  failure, its summary. A browser test measures both widths on the real app (nothing wider than the window, the log at least
  480 px at 960 px).
- **P86. One status vocabulary, and a chat's request in Needs you (2026-10-21, the CopilotKit/AG-UI comparison, phase 2; the UI
  review's P0 "status vocabulary").** Pages said an agent's state in the core's own words, and differently: "Paused (budget)",
  "Needs Attention", "Completed", a raw "away", and "Unknown" for an agent that had not run. Every page now says one of seven
  words, from one mapping (`ui/app/lib/format.ts` `agentStatus`): Working (Working, Thinking), Waiting for you (Needs Attention,
  Paused, or waiting on a permission request), Waiting (waiting on anything else), Starting, Idle (Idle, Completed), Error
  (Failed) and Stopped (the program is not running, or never ran); never "Unknown". What the agent is doing stays in its
  activity text (the tooltip, the Crew cards). The dot's color, pulse and the avatar's eyes follow the same status, and the
  Guide's table says the seven words. Needs you also missed a kind of waiting: a chat whose CLI asks to use a tool (P83) waited
  only inside the chat. The core's `needsyou.list` now adds each waiting chat request (kind `chat_permission`, with its chat;
  opening it goes to the chat, where Allow once and Refuse are), so the count in the top bar is everything that waits for a
  person, and every list and count of Needs you reloads on the same events (`needsChanged`).
- **P87. A typed activity vocabulary modelled on AG-UI, and a task's Activity (2026-10-21, the CopilotKit/AG-UI comparison,
  phase 3).** An agent's run reached the UI only as raw `agent.output` chunks, which no screen drew: the task page showed
  evidence and reviews but not what the agents did while they worked. `internal/agui` names that in a vocabulary modelled on the
  AG-UI protocol 1.0: RUN_STARTED / RUN_FINISHED / RUN_ERROR per turn, TEXT_MESSAGE_START / CONTENT / END, REASONING_MESSAGE_*,
  TOOL_CALL_START / ARGS / END / RESULT, ACTIVITY_SNAPSHOT for an agent's plan (ACP `plan` updates), and AG-UI CUSTOM events
  `drydock.interrupt` / `drydock.resume` for a turn that waits for a person (P88). It is not a second record: `agui.Project` reads
  the `agent.*`, `approval.requested` and `permission.decided` events the bus already stored for a task (pure, deterministic; one
  thread per agent process; consecutive deltas joined; arguments and results cut to 8 KiB) and `task.activity` returns it.
  A turn ends once: RUN_FINISHED with AG-UI's outcome object (`{type: "success"}`, or `{type: "cancelled"}` for a turn that
  was interrupted or whose program ended during it), or RUN_ERROR for a turn that failed, including one the agent reported
  with a done chunk (a Claude result with `is_error`, stop reason `error_*`; an ACP prompt that failed, stop reason `error`; a
  turn that carried a non-fatal error, gave no answer and did not end with `end_turn`, as the chat reads a turn; a turn left
  open when the next starts is read the same way), with the turn's last error as the message. A failure
  outside a turn (a start that failed: the runtime stamps that `agent.failed` with the run id the process would have had; a
  crash between turns) is a run of its own that errored, and the failures that follow a RUN_ERROR before the agent moves on
  from that turn (the process's `agent.failed` after a fatal error chunk) add their words to it rather than sending a second
  one; an `agent.failed` with phase `bookkeeping` (Drydock could not record the run's end) is no turn failure and is left out,
  as is a permission answer that comes after its turn ended. REASONING_MESSAGE_START carries `role: "reasoning"`. `agui.CheckOrder`
  applies the ordering rules of AG-UI's verifier (client `verify/verify.ts`) and the tests run every projected thread through
  it. The
  UI's types are generated from the Go structs (`internal/tsgen`, a reflection-based writer that reads types as encoding/json
  does; `go run ./cmd/drydock-tsgen` writes `ui/app/api/agui.gen.ts`, and a Go test fails while the file is out of date), so the
  core and the page cannot disagree on a field. The task page's new Activity section draws each agent process: its plan as a
  checklist, its newest turn open (messages, reasoning folded away, each tool call as the chat's card from P84, each wait for a
  person and its answer), earlier turns folded to one line; it re-reads as the task's events arrive, throttled to once per
  750 ms (a live read can now opt into streaming events: `LiveOptions.noisy`, a throttle rather than a debounce, so a steady
  stream never postpones the read). Nothing new is installed: the vocabulary is modelled on AG-UI's event names; no AG-UI
  package is a dependency.
- **P88. Approve with edits, and the waits in the activity (2026-10-21, the CopilotKit/AG-UI comparison, phase 3).** A
  worker's permission request (P40, P83) could be allowed once or refused, nothing between: a person who saw `npm install
  left-pad` and wanted `npm install left-pad@1.3.0 --ignore-scripts` had to refuse and hope. The task page now offers Edit before
  allowing where the CLI can run a changed call (Claude workers: the prompt answer's `updatedInput`; ACP agents can only allow
  or refuse) and the whole input is shown (not shortened or redacted). The changed call is checked by the rules again, so a
  hard-deny stands even for a person's edit; the approval the person answered is consumed as before (its hash binds the call
  they read) and the CLI runs the changed input. The edit stays in memory: the approvals table, the events and the audit get
  only `approved_with_edits` and the changed input's digest. In the task's Activity each permission request is a wait inside
  the worker's turn ("Waiting for you: Bash: npm install left-pad") until it is answered ("Allowed by you with your changes",
  "Refused by Drydock's rules", "Nobody answered in time"); docs/security.md s5 step 6.
- **P89. Chat basics: code blocks, a reading column, the agent's reasoning, edit and regenerate (2026-10-21, the
  CopilotKit/AG-UI comparison, phase 4).** Four gaps a person meets in every chat. (1) Code in a message was a plain block:
  every fenced block now names its language, has a Copy button (a word, not an icon: a rendered message holds no svg) and
  light colouring of comments, strings, numbers and keywords for the languages agents write most (`ui/app/lib/highlight.ts`,
  a scanner of a few hundred lines; nothing installed; runs drawn as text, code past 20,000 characters left plain). (2) An
  answer ran the full width of the window; it now stops at 760 px. (3) The CLI's reasoning (thinking) was dropped: it now
  streams as `chat.thinking` (coalesced and cut at whitespace like `chat.delta`, so the redactor sees whole tokens) and is kept,
  redacted and bounded to 16 KiB, as `thinking` on the tool call or answer it came before (migration 00127; a fork carries
  it); the UI shows it open under the typing while it streams and folded on the row after. Reasoning with nothing after it
  (a turn stopped mid-thought) is not kept. (4) There was no way to correct a question or ask again: `chat.resend` replaces the
  person's message at a seq (edited, or unchanged to regenerate) and everything after it, and runs the turn again. A CLI
  session cannot be rewound to an earlier message, so the session is dropped and the new turn starts a fresh one given the
  conversation before it as quoted history (the path a fork's first turn takes, `fallback: true`); the check, the removal and
  the session reset happen in the transaction that claims the turn, so nothing is removed when the turn cannot start. Edit
  is offered on the person's messages and Regenerate on the newest answer, neither while a turn runs; the editor says that
  sending replaces the message and everything after it.
- **P90. Files and pictures in chats (2026-10-21, the CopilotKit/AG-UI comparison, phase 4).** A chat could be given other
  chats but no file. `chat.send` now takes up to four files (10 MB each). They are kept on disk under the chat's attachment
  folder named by their id (the name the person gave is only shown and quoted, never a path, so no name reaches the file
  system), readable by the owner only; the row holds name, type, size and hash, never content (migration 00128), and the message
  lists them. A text file (UTF-8, no NUL, at most 1 MB) is quoted to the agent as data, redacted, at most 32 KB of it; a
  picture (PNG, JPEG, GIF, WebP, at most 5 MB) is given beside the prompt to an agent that takes pictures
  (`agents.ImageSender`: ACP image blocks for an agent that advertised `promptCapabilities.image`; Claude Code's stream-json
  content blocks); anything else is named with its type and size. A chat whose agent does not take pictures is refused one
  before anything is kept. Claude Code is not yet declared to take pictures: its manifest says `image_input: unknown`, and
  the adapter declares it once the new paid contract CLAUDE-IMAGE-01 (a one-colour square the model must name) passes on a
  signed-in run; the sending code and the stand-in that reads it are tested now. The person reads a file back through
  `chat.attachment` (checked against its hash; a file changed on disk is refused, and a turn that would give it fails rather
  than run without it). A fork copies the files under new ids, a resend keeps the replaced message's files, deleting the chat
  deletes them, and the quoted history of a fresh session names them. The composer takes files from its button, a drop or a
  paste, as removable chips; a message shows its files as the room's file cards (Save, and Show image for a picture; SVG is never
  rendered), now shared by rooms and chats.
- **P91. An event inspector, and "Try in a chat" (2026-10-21, the CopilotKit/AG-UI comparison, phase 4).** The Run page listed a
  run's events by type and time only; what happened was in payloads nobody could see. The run explanation now carries each
  event's payload as the bus stored it (redacted), cut past 16 KB as the event stream cuts it, and the Events tab is an
  inspector: a filter that matches words in the type, the agent or the payload, each row opening to its payload as text, and
  Copy as JSON. "Try from here" for a task run is not offered, on purpose: a checkpoint holds the workflow's state but not the
  worktree, so a run branched from an earlier point would run its later steps on today's files while claiming to resume the
  earlier state; and a finished task (completed, failed, cancelled) is terminal in the state machine, so a branched run could
  not move it. The engine's `ResumeFrom` and `Replay` stay unexposed. What is offered instead is sound: on an agent's turn,
  Try in a chat sends the exact prompt that turn was given as the first message of a new chat with the same agent CLI and model
  in the project's folder (not the task's worktree), to see what it does with that input again, or with a change, without
  touching the task; a prompt larger than a chat message (100 KB) is refused before a chat is made. Chats keep their own
  "from here": fork from a message (P29) and edit and send again (P89).
- **P92. The learning loop: agents and reviews propose memory (2026-10-21, the CopilotKit/AG-UI comparison, phase 4).** Project
  memory (decisions, conventions, API contracts, rejected approaches, known issues, preferences) reached agents through the
  generated AGENTS.md and `search_memory`, but only a person wrote it, and what a person taught in a review was lost after the
  task. Two paths now propose entries; neither accepts one. (1) Every role's Workspace tools include `propose_memory`: an agent
  proposes a durable lesson (kind, title, body), stored as a proposal through the same path as a person's (kind checked, size
  bounded, redacted), its source naming the agent and the task; at most 5 per session. (2) A person's change request on a
  review, in their own words, becomes a `preference` proposal titled "From the review of #N <title>", its source the task
  (a request under 20 characters teaches nothing and proposes nothing). Accepting stays human-only (`DecideMemory`), and only
  accepted entries reach later agents. The Memory page says where each proposal came from in words ("Backend #1, working on #3
  ...", "your change request on #3 ...", "your edit of AGENTS.md").
- **P93. The UI review's P1 items (2026-10-21, the CopilotKit/AG-UI comparison, phase 4).** The review of the running app named
  nine places where a person could not see or decide easily; each is fixed where the thing lives. (P1-1) A task's Needs-you item
  carries a context (`room.NeedsContext`, filled by the core from the task's evidence and reviews): the assignee, the newest
  diff's size, the newest attempt's checks (the latest result per kind) and the newest reviewer verdict, shown as one line; the
  review card shows the +/- counts of the task's newest patch (what Approve merges; it says when that is newer than the request)
  and "See the patch" brings the page's one patch viewer, the Diff section, into view. Approving from the Needs-you list is not added: a decision is
  made on the item's page beside its evidence, so nothing is approved unseen. (P1-2) The chat header keeps the chips; the folder
  and the tokens and cost moved into Properties; "and N turns' cost unknown" replaces "+ N turns unknown"; the canned retry is a
  small note, not the person's message. (P1-3) A refused tool offers one primary "Allow and retry" with "Only allow" beside it.
  (P1-4) The chat list pins the open chat when its filter would hide it. (P1-5) A room's mode, agents and folded-away turn
  telemetry are the details panel's Room tab, opened by the mode chip, not a bar over the messages. (P1-6) A chat that is only
  one marker line is stored as the structured message itself (one DECISION for `/decision`, not a chat and its derived copy;
  room-protocol s3); "untrusted" is an icon with its explanation as the tooltip; everyone's messages are left-aligned rows, the
  person's own faintly tinted (replacing P64's right-hand bubble). (P1-7) The run graph opens at its fit only when that is 70 %
  or more, else at 70 % around the node the run is at, which is selected. (P1-8) One home per thing: the MCP servers you add are
  kept in the Library (Integrations links there), the Library has no second Skills link, a project's page is "Security" and the
  workspace's "Security scan", the dock is only on a project's pages, and the Guide is the "?" button's own surface. (P1-9) The
  Plan page leads with a proposal under review and folds the goal form under it. Found while testing: a chat opened mid-turn
  could drop a tool result that came before its row; the chat feed now keeps results by message id.
- **P94. The 2026-10-21 security releases and the npm compromises (on the owner's words: "Do a full review of our app look for all
  software packages and update the artifact. NPM and PHP were compromised to lets make sure were running the fixes").** What was
  checked and what changed. (1) *Node.js* published security releases on 2026-10-20/21 (24.22.1, 22.23.4, 26.12.1; TLS hostname
  checks, DNS name truncation, the Brotli decoder). The development host moved to 24.22.1 and its second Node to 22.23.4, each
  checked against SHASUMS256.txt and that file's signature (releaser key 5BE8A3F6C8A5C01D106C0AD820B1A390B168D356). CI's
  `actions/setup-node` steps now say `check-latest: true`: with `node-version: "24"` alone the action takes the runner image's
  cached 24.x, which can lag a security release by days; a test (`cmd/drydock-contract`) holds every setup-node step to it.
  (2) *npm compromises*: the UI and starter lock files were searched for the known bad releases (tensorlake 0.5.144, the
  Shai-Hulud worm of 2026-10-08; axios 1.14.1 and 0.30.4 and plain-crypto-js, March; @nubjs/nub 0.9.4) and none is there;
  `npm audit` (which carries npm's malware advisories) finds nothing in either; only esbuild, fsevents and workerd have install
  scripts, as before. New releases taken in this pass (vite 8.3.5 with rolldown 1.2.15 and postcss 8.5.31, lucide 1.61.0) were
  read before they were taken: each carries npm provenance, none has an install script, vite's and lucide's changes were
  compared file by file with the versions they replace. (3) *PHP* published 8.5.12, 8.4.27, 8.3.36 and 8.2.35 for
  CVE-2026-109618 and CVE-2026-109619 (remote code execution). Drydock has no PHP dependency (the PHP tree-sitter grammar is
  only in the Go module graph); the development host's PHP 8.3 moved to Ubuntu's security build 8.3.6-0ubuntu0.24.04.13, whose
  changelog names both CVEs, and its Composer to 2.10.3 (signature checked against the Packagist release key). (4) Also behind
  and moved: openai-go 3.85.0, modernc.org/sqlite 1.61.0, klauspost/pgzip 1.3.0, gh 2.103.0, the LobeHub icon set 1.97.0
  (the four copied marks are byte-identical). Wrangler stays at 4.151.0 (its hold still applies). Not done here: the
  `npm audit signatures` check could not reach Sigstore from this machine and osv.dev was refused by its network policy, so
  provenance was read from the registry's metadata instead of verified, and the Go vulnerability lookup ran in CI only.
  (5) Same day, after the review of this change: @linear/sdk 99.0.0 came out; every type, field and enum value Drydock's Linear
  queries and mutations use (Issue and its fields, IssueFilter, the updatedAt order, viewer, organization, teams, projects,
  workflow states, CommentCreateInput, IssueUpdateInput) is in 99.0.0 and none is deprecated, so the schema Drydock follows
  moved to 99.0.0. The audit's Node row now reads the newest Node overall from nodejs.org too, names every security release of
  the installed line newer than the installed version, and reports a failed nodejs.org lookup instead of falling back to the
  npm package that lags.
- **P95. The scan's tools and the model engine are Drydock's own: no tools section, no "Install llama.cpp", and Check for
  updates installs (2026-10-22, on the owner's words: "OpenGrep has an error when scanning", "Under Security, lets delete the UI
  part where it has gitleaks, osv-scanner, opengrep and open grep rules", "Check for update should then install the update",
  "You didn't build llama.cpp into the app merged with the TurboQwant from Google ... I don't want to hit a button that says
  install llama.cpp, I don't even want it to say llama.cpp").** (1) *The Opengrep error.* Every scan passed `--no-progress-bar`,
  which Opengrep 1.31.0 does not know: it exited 2 and printed its usage line, which the page showed as the reason. The option is
  gone (`-q` already keeps the progress bar off); the reason for a tool's error is now its error line without terminal colours;
  the stand-in refuses an unknown option as Opengrep does; and a test runs the real Opengrep with the scan's exact arguments and
  the built-in rules (`DRYDOCK_TEST_OPENGREP`), which CI's app job points at the Opengrep in the Windows download. (2) *Why the
  engine was missing.* The zips built by hand on 2026-10-21 carried `drydock.exe` and its checksums only, without the tools
  folder `cmd/drydock-bundle` writes (CI's download has it), and the app installed tools only from that folder. With the new
  `core.Config.AutoSetup` (set by the desktop app, off in tests) a scan tool that is not installed, or the model engine when it is neither installed nor
  removed by a person, is fetched from its GitHub releases at start, checked against GitHub's SHA-256, as an install job the
  pages show; the engine is the TurboQuant build with `turbo4` unless the person chose otherwise, for the suggested backend when
  the fork builds it, else Vulkan. So a download without the tools folder sets itself up at its first start (CI's download carries the folder; a zip built on the development host cannot, since `cmd/drydock-bundle` reads the releases through GitHub's API, which this host may not reach). (3) *The Security page* shows no
  tools section: no Install, Update, Remove, program path or rules folder, and no rule-set help; each engine row still says
  whether its tool is ready. What the page could set and no longer shows is gone from the core too (`security.settings.set`,
  `security.tool.set`, `security.tool.choose`, `security.tool.remove`): a rules folder or program chosen before, and the
  marker a removed tool left, would have applied where nobody can see or clear them, so the next start deletes them and the
  scan runs Drydock's own tools with the built-in rules. The twice-daily check installs a newer osv-scanner or Opengrep
  release itself under AutoSetup, only when its tag is newer than the one installed (no downgrade to an older "latest"), and
  only once it has run the way a scan runs it (`scan.SelfCheck`, a small folder of its own): a release that breaks an option
  Drydock passes, as 1.31.0 did, is deleted and the one in use stays. A tools folder whose `bundle.json` cannot be read is
  set up like one that is missing. (4) *Local models* never name
  llama.cpp: the card says "Setting up local models" with the progress, or "Set up" (the TurboQuant build, nothing to choose)
  when the start could not do it; an installed engine is "TurboQuant build <tag> · <backend>" with one button, Check for
  updates, which installs a newer build of the same source and backend (it reads "Update to <tag>" when the twice-daily check
  found one); the engine's Update and Remove buttons are gone. The provider Drydock registers is "Models on this PC" (a row an
  earlier version named "Models on this PC (llama.cpp)" is renamed at start; any other name is the person's); Check for
  updates stops the server for the install and starts it again on the same model, and the build sources are "TurboQuant build" and "Official weekly build".
  (5) *CI runners* ("migrate/merge that into the GitHub Window") are a section of the GitHub page, where its read-only runner
  summary was; Connections no longer shows them (this changes where P56 and P68 put them). (6) *A project's Integrations* ("make
  those more like the cards we see under Integrations under Workspace") use the workspace page's order and tiles: Services
  (the app-sync tiles, formerly "App sync"), then Agent tools, one tile per MCP server (the project's own first, then the
  library's not yet added) with what it is, which of the project's agents may use it and an on/off/not added badge; the open
  tile's panel holds the grants, Turn off, Take out or Add. A vendor's app sync and its MCP server stay two tiles.
- **P96. A WSL install is "Claude Code WSL", without the distro; a new agent template offers the whole skill library (2026-10-22,
  on the owner's words).** (1) "Anything that has Claude CLI WSL Ubuntu, rename it everywhere just to say Claude CLI WSL, we don't
  need to see the Ubuntu." The product name stays the one the app uses everywhere else ("Claude Code", `adapterLabel`), so the
  names read "Claude Code WSL": the Connections tile and card (the tile's line is "2.1.296 in WSL"; the distro's logo moved from
  the card's heading to its Runs row, which keeps "inside the WSL 2 distro Ubuntu"), the new chat dialog's choice and the open
  chat's adapter chip (distro in its tooltip), the note a chat gets when it moves ("Now runs with Claude Code WSL in ..."), the
  wizard's, Team page's and gallery's "claude (recommended) - in WSL", an agent's "runs in WSL" chip (distro in its tooltip), the
  sign-in terminal's title ("Sign in: claude WSL") and, with one distro, the run-mode pickers' "In WSL". The distro stays in the
  Connection key (`claude@wsl:<distro>`), the stored `wslDistro`, the card's details and the tooltips. Two installs must still
  be told apart, so the distro comes back only then, and minimally: the same CLI in two distros makes each tile and each chat
  choice "Claude Code WSL (Debian)", and two distros in a run-mode picker are "In WSL: Debian". (2) "When I was going to build an
  new Agent Template Remix skills was missing from the list." The template form's "Add a skill" list
  (`agenttemplate.skillChoices`, `internal/core/ext_agenttemplates.go`) was built from the registry skills imported into some
  project and the skills the prebuilt agents pin, and never read the workspace library (`skill_library`, P48). The Remix registry
  (P69) is in the library but no prebuilt agent names it, so until a project imported a Remix skill none was offered; the Skills
  page, an agent's page and a chat offered them all along. The list now adds every library skill that can be added (available,
  not gone), pinned to the library's commit, marked "from the Library"; applying the template imports it from the registry at
  that pin as before. The form reads the list again on `skill.library_synced` and `skill.imported`, and with nothing to add it
  links the Library, which fills from the registries at start and twice a day (Sync now fills it at once). Tested:
  `TestTheTemplateFormOffersTheLibrarysSkillsIncludingRemix` (internal/core, against the GitHub stand-in; it fails on the old
  list), `TestCLINameLeavesTheDistroOut` (internal/chats), the core's sign-in title in `wsl_test.go`, and the UI's
  `connections`, `chats`, `team`, `run-mode`, `wizard` unit tests and their browser tests; `ui/e2e/wsl.spec.ts` follows the new names.
- **P97. Graphite: the UI's third design, after the next generation of Linear (2026-10-22, on the owner's words).** The owner
  asked for "a few screenshot design ideas of what this app would look like if it was like the next generation Linear", chose
  the fresh look, asked for more screens, and then said "okay build this exactly" about the eight-screen canvas (Inbox, Board,
  Task page, Command menu, Crew, Security scan, Chat, Models on this PC). Their answers: a dark theme with a matching light one,
  a sidebar that folds, and the screens' features built in the core too. Graphite replaces P64's look and its top bar; P64's
  hub pages, rails and test ids stay. (1) The look: a near-black canvas (`--bg-app` #0b0c0f, the light theme #f8f8f9) with
  raised greys for surfaces, one periwinkle accent, amber for "waiting on you" and teal for "passed"; Inter for text and
  JetBrains Mono (`@fontsource-variable/jetbrains-mono`, pinned exactly) for task keys, paths, numbers and commands; a type scale
  of 12, 12.5, 13, 15, 18 and 22 px (the density budget follows it); radii 5, 7, 10 and 14; 32 px buttons (28 small); pill tabs.
  Agents are discs in their own hue with their first letter (drawn by CSS, so a name is never read twice); the eyes went. Every
  colour is still a `light-dark()` token held at 4.5:1 by `styles/contrast.test.ts`. (2) The shell is a sidebar
  (`shell/sidebar.tsx`, landmark "Main", arrow keys move through it): the workspace switcher, search (Ctrl+K), the Inbox with
  its count, Running, My tasks, Chats, the projects (the open one shows its tabs Overview, Tasks, Rooms, Crew, Knowledge), the
  workspace's Security, Connections, Models on this PC and Library, then Settings, the Guide, the local engine's state and the
  event stream. "[" or its button folds it to a 56 px rail of icons whose names stay the links' names; the choice is kept
  (`drydock.sidebar`), and a window under 1024 px starts folded (a phone-sized one uses a 44 px rail). (3) The core gained what
  the screens show: a project key and task numbers (`GOC-41`), task labels, workspace-wide lists (needs you, agents, running
  scans, task summaries with step, checks and what an agent is doing), `approval.get` and "allow for this task" rules, steering,
  pausing and handing a task, assigning it to a person, asking the lead to split it, an assignment preview, dismissable
  security findings with their code context and a task made from one, a chat's project, and what a local model is used by,
  with its KV cache estimate. Each has Go tests end to end through `a.call`, and every screen has unit, browser and e2e tests.
  (4) The eight screens, each with its e2e spec against the real core: the **Inbox** (`/needs-you`, every project's: Needs you,
  Running, Done today; J/K, A, D, E, Enter; an approval's detail with "Approve for this task") and **My tasks**; the **board**
  (Board, List, Timeline; filters Agent, Label, Needs me; cards with key, step, checks bar, verdict, Merge, View diff; C for a
  new task); the **task page** (breadcrumb and key, the run's pill, the criteria as a checklist, the activity as steps, steer
  with Ctrl+Enter, Pause, Open the worktree (O), Hand to another agent); the **command menu** (the page's task as a chip,
  Assign / Actions / Go to, a "What happens" panel from `task.assignPreview`, prefixes > # @, G then I, T or C); **Crew**
  (`/team` and every project's `/crew`: Today's timeline of runs and waits, a card per agent, N for a new agent); the
  **Security scan** (engine tiles, findings with the code around each and the secret drawn as hidden, What to do, Make a task,
  Open the file, Not a real key, Recent scans; R scans again); **Chats** (the list with project, CLI and when; your messages
  as bubbles; a waiting request as an inline card with Allow once, Allow in this chat, Deny; Move to a project; Ctrl+N); and
  **Models on this PC** (`/models`, its own workspace page: Serving, Memory split into the model and its KV cache, Engine,
  the model table with fit badges, "/" to search the Hub; Providers links to it). Where a screen differs from its mockup it
  is because the core has no such data (a diff hunk without context lines) or the app's rules (12 px is the smallest size,
  Ctrl rather than Cmd on Windows, no "/ commands" a chat does not have). Every browser test now fails when a page draws two
  siblings with one key, and a source rule holds every corner to the 14 px radius.
- **P98. Lists, not boxes: Agents, one Integrations page, one Models page, and a GitHub page of rows (2026-10-24, on the
  owner's words).** The owner asked to merge Integrations and Connections, to give Agents its own page, and to make the pages
  "really easy to navigate and not have such massive boxes to look at ... Draw inspiration from other apps", the GitHub page
  too; then, on the plan and its mockups (the canvas "Drydock Integrations, Agents & GitHub"): "choose what you think is
  appropriate" for the merged page's name, yes to moving the MCP servers people add themselves out of the Library, "model
  providers and models on this PC is kind of the same thing ... just merge both", and yes to a repositories table. (1) **The
  pattern** (`ui/app/ui/master-detail.tsx`, after Linear's settings rows, Raycast and VS Code's extension lists, Cursor's MCP
  settings and Apple's grouped settings): a filterable list of 44 px rows under quiet group headings (a logo, the name over
  one line, the state in words with a dot) beside the chosen row's detail; "/" focuses the filter, the arrow keys move through
  the rows; the detail is a `DetailHeader` (logo, name, state pill, one line, actions) over `SettingsGroup`s of `SettingRow`s
  (label, value, control) and `FactGrid`s, with long explanations cut to one line and a Guide link. Below 1024 px the list
  fills the page and a chosen row opens its detail in its place with Back. Each item has an address (`/agents/<key>`,
  `/integrations/<key>`, `/models/<key>`); a choice replaces the address and the screen stays mounted. (2) **The sidebar**
  reads Crew, Security, Agents, Integrations, Models, Library; the Settings rail keeps Routing and the app's pages. Old
  addresses lead on: `/connections` to Agents, `/providers` to Models, `/mcp` to Integrations; the command menu finds Agents by
  "connections" and Models by "providers". (3) **Agents** (`screens/agents/`, once Connections): the agent CLIs, Installed then
  Not installed, each Ready, Sign in, Accept notice, Untested, Test failed, Unavailable or Install; the detail's header holds
  Test connection (its confirmation a dialog), Sign in and Check again, at most one notice with its fix, and the tabs Overview
  (notices to accept, the facts, the distro, the MCP gateway as one switch per CLI in place of the separate gateway list, Used
  by: the crew agents in any project that run on this CLI), Test results and Install and update. (4) **Integrations**
  (`screens/integrations/`): Services (app sync), Agent tools (vendors' MCP servers) and Your servers (the MCP servers people
  add themselves, moved from the Library, with Add MCP server as a dialog); filters All, On, Needs you; a service not yet
  connected shows three steps, the way to connect, only the field it needs and what it reads; a connected one its connection,
  checks, projects and its vendor's MCP server as rows. GitHub's row opens the GitHub page. The Library keeps agent templates
  and skills. A project's own Integrations page uses the same rows. (5) **Models** (`screens/models/`, Providers merged in):
  On this PC (the engine, the server, memory, the models and the Hugging Face search, as before) then the providers and Add a
  provider; the local server's own provider entry is the This PC row, never a second one. The models an agent CLI uses
  through its own sign-in stay with that CLI on Agents. (6) **GitHub** (`screens/github/`): a breadcrumb back to
  Integrations, the sign-in as one line with chips, Access as rows (the gh sign-in, issue sync, GitHub's MCP server), a new
  **Repositories** table and the CI runners as a table with Add runner in a dialog. The table is the core's new
  `github.projectRepos` (`internal/core/ext_github_repos.go`, `internal/git/host/overview.go`): one GraphQL call through the
  hardened gh per repository per refresh (default branch, its latest commit's check rollup, open pull requests; at most four
  at once, 20 s each, answers kept 60 s, projects sharing a repository share the call), errors per row, rows marked
  `needs_gh` without gh; tested through `a.call` with a labelled stand-in gh. Every page has browser tests and its e2e specs
  against the real core; the density and layout audits measure the new addresses. **Not proven** until it runs on a PC: the
  repositories table against real GitHub.
- **P99. drydock.exe carries its own icon, version information and manifest; signing is prepared and off (2026-10-24, on the
  owner's words: the built exe showed "just a little default window" icon, and "how to make the app more trusted by
  Windows").** The exe embedded no resources at all. (1) Every build (CI's `app` job, `task build:windows`) runs go-winres
  v0.3.3 on `build/windows/winres.json`, which makes `rsrc_windows_amd64.syso` (not committed: a binary that changes with each
  version) for `go build` to link: icon group 3, the only one (the ID Wails loads for every window and the first group
  Explorer shows) with Drydock's mark from `ui/public/favicon.svg` at 16 to 256 pixels, 16 and 20 drawn from their own
  thicker, pixel-aligned sources (`build/windows/make-icon.mjs` regenerates the `.ico` and a 1024 px master with Chromium);
  a version resource (Drydock, drydock.exe, the build's version); and a manifest (`asInvoker`, per-monitor v2 DPI, Windows
  10/11, Common Controls 6, `longPathAware`, UTF-8 active code page, the last not yet run on Windows and the first to remove if
  text turns up mangled). (2) One version: `app.Version` (now `app/version.go`, on every platform) and the definition's
  default are kept equal by a test, and `VERSION=` on the Taskfile feeds both go-winres and the linker. (3) Wails sets its
  icon as the window's big icon only, at the system size; `app/icon_windows.go` sets the small and big icons from resource 3
  at the window's DPI when it is shown and when its DPI changes, so the title bar shows the drawn 16/20 px image. (4)
  `cmd/drydock-winident` (with `github.com/tc-hib/winres` v0.3.1) reads the built exe back and fails CI without the icon,
  the version or the manifest; Linux tests check the definition and the checker on cross-compiled executables. (5) CI signs
  `drydock.exe` with `azure/artifact-signing-action@v2` (SHA-256, RFC 3161 timestamp) on a push to `main` once the owner sets
  the Artifact Signing secrets and variables, and skips it otherwise; D8's choice between Artifact Signing and an OV
  certificate stays open (an OV path is not wired). The version resource's CompanyName is "Kyle Maguire", the LICENSE's
  holder, until a company and certificate exist. docs/windows-identity.md.
- **P100. The AG-UI review and its fixes (2026-10-26, on the owner's words: "Do a review of the AG-UI protocol and make sure it
  is going to work between our agents and the rest of the platform").** `docs/agui-review-2026-10-25.md` is the review: the
  vocabulary of P87 passes the published `@ag-ui/client` 1.0.3 pipeline on every projected thread, and the defects were in what
  the adapters and the projector put into it, and in the other readers of the same events, not in the protocol. What changed:
  (1) **One rule for a failed turn.** `workflow.TurnFailed` (a stop reason `error` or `error_*` fails the turn whether or not the
  agent answered; an uncoded error with no answer and no `end_turn` fails it; a warning with a Code never does) is read by the
  Activity (`agui.complete`), the chat (`turnRun.finish`) and presence (`room.Derive`), so one turn is never told two ways. The
  ACP settings warnings (a model, an effort, a permission mode or an MCP transport the agent does not offer) carry
  `workflow.CodeSettings`, so the default `acceptEdits` no longer makes an ACP session's first tool-only turn a failed one. (2)
  **A failed Claude Code result fails the run.** The parser's done chunk carries the subtype when it names the failure
  (`error_during_execution`, `error_max_turns`, ...), else `error_` plus the CLI's `terminal_reason`, else `error`, never the last
  message's `stop_reason`; the CLI's API-error assistant message (`is_api_error_message`) is an error chunk, not an answer; the
  fake agent's failing shapes are the CLI's. (3) **No sentence twice.** The projector keeps the text streamed in the open run that
  no `message_end` has taken yet, and a `message_end` that repeats the front of it takes that text and adds no second message (an
  ACP thought or tool result between the deltas and the message end; Claude's per-block messages); the Claude parser emits a
  thinking block once when its deltas were streamed and the text blocks of one assistant event as one `message_end`. (4)
  **Conformance for an outside reader.** Drydock's own fields (`seq`, `stopReason`, `isError`, RUN_ERROR's thread and run) ride in
  `metadata.drydock`, the object the protocol keeps for them, so the published client strips nothing; RUN_STARTED declares
  `protocolVersion` "1.0"; tool-call ids are deduplicated per run; a nameless result's call is "tool"; a Go test holds every
  event's top-level keys to exactly the fields the 1.0.3 schema declares. (5) **Cuts that keep what matters.** A result over 8 KiB
  keeps its first 6 KiB and last 2 KiB around a marker line (test runners put the verdict at the end); arguments over 8 KiB stay
  one valid JSON document (the longest string values are cut inside it, deterministically, numbers kept as written), so a large
  Write keeps its path and line counts; the runtime bounds a stored tool result against the bus cap, so no result becomes a
  stand-in the Activity cannot read; the UI strips the marker lines before it reads a command's last line or its test failures.
  (6) **Refusals and decisions are visible.** A third CUSTOM event, `drydock.decided` {tool, outcome, reason, toolCallId, rule},
  carries a gate deny or a task rule's allow that asked nobody (the ordinary `allowed_by_rule` of every call is left out) and
  the refusal an ACP session with nobody to ask now reports (`PermissionDenial.Reason`); the Activity draws it as a decided row
  with who decided and why. The CLI's own `permission_denials` stay out: the failed result shows the step as failed, and when
  the gate was asked the resume row already says who refused. "Approve for this task" is recorded on the answered request as
  `approved_for_task` with its rule (`PermissionGate.MarkApprovedForTask`), so the Activity says "Allowed by you for this task"
  and the audit log names the rule. (7) **The Activity is live for the gate.** It re-reads on `approval.requested` and
  `permission.decided` as well as `agent.*`, so an allowed command no longer reads "Waiting for you" for its whole run. (8)
  **Presence agrees.** `room.Derive` reads the gate's two events (Waiting "awaiting permission" while a Claude worker waits,
  Working again when answered) and a turn that ended with an error stop reason is Failed "turn failed: <stop>", not Completed. (9)
  Smaller: an ACP `session/prompt` failure is worded from the JSON-RPC error's message, not its JSON; a thread with no turn yet
  reads "starting" or "stopped before its first turn" (`AguiThread.ended`), never "0 turns"; a running read shows its own time;
  an unknown wait outcome reads "Decided (<token>)" rather than the bare token; `docs/interfaces.md` gains the Activity section
  and the full chunk list; `docs/ui.md` names the three surfaces called Activity; the Guide's event table says what `agent.*` and
  `permission.decided` are. Left for later, with the reasons in the review's section 6: the dead permission request after a crash
  (M7), the incremental `task.activity` read (M10), the truncated thread's label (M9), the publishes a cancelled turn drops (L6),
  `toolCallId` on the interrupt and AG-UI-shaped interrupt values (L9, N7), the `acp:` marker and ACP's tool kind (L12, L13),
  the run page's two readings of an abandoned turn and its Events tab (L18, L19), usage on RUN_FINISHED (N2). Not done: an AG-UI
  HTTP endpoint, sub-agents, steps and snapshots, which wait for an outside reader. No new software: the AG-UI packages were used
  only as test oracles in a scratch folder and are not dependencies.
- **P101. The UI audit of 2026-10-26 and its fixes (on the owner's words: "Do a full UI Audit, make sure the UI is in good shape,
  things are formatted nicely and designed well. Any suggestions please send them over").** [`ui-audit-2026-10-26.md`](ui-audit-2026-10-26.md)
  read every page of the graphite UI on the real core, light and dark. What changed: (1) the body no longer turns on Inter's
  tabular figures, which also widen the hyphen, so hyphenated names read without gaps; a number that must line up sets
  `tabular-nums` itself, and a source rule keeps it off the body. (2) A `Disclosure` draws a chevron, so a closed group no longer
  reads as a stray line. (3) Learn more is an inline link with padding for its 28 px target, so it no longer lifts the line it sits
  in. (4) `emptyLine` is running text, not flex items, so a link in its sentence has a word space, not an 8 px gap. (5) The Library
  rows on Skills start at the left. (6) A room's name adds nothing to its row's smallest width, so a long one ends in an ellipsis
  inside its card or rail. (7) A settings group is a query container: under 520 px its values go under their labels, as in a narrow
  window. (8) Smaller: an agent run's status in words, the licence alone after "License", commands from the core as code instead
  of backticks, "Crew" where five sentences said "Team page". The 27 suggestions of the audit (two items called Crew in the
  sidebar, a folding sidebar at 1024 px, one page header, facts said two or three times, Routing's row actions, words) are left
  for the owner to choose from.
- **P102. Six of the UI audit's suggestions, on the owner's go-ahead (2026-10-26: "1. Sure 2. Okay 3. Yup 4. Yes 5. Works for me
  6. Okay. Lets work on all of them").** (1) The sidebar's workspace pages are named apart from one project's: **All crews**
  (`/crew`, titled so) and **Security scan**; a project's Security page is titled **Security**, as its pill. (2) The sidebar folds to
  its rail under 1200 px (`NARROW_SIDEBAR`, was 1024), unless the person chose. (3) One page header: GitHub is a `Page` with its
  icon, subtitle, Learn more and an **All integrations** link (no breadcrumb); Models' Learn more sits beside its subtitle; the scan
  header has the shield. (4) Said once: an agent CLI's detail shows no state pill while a notice says what is in the way, and the
  notice offers no button the header has; GitHub without gh says it in the hero alone (the connect form's `ghStateAbove`, its gh
  button turned off until gh is signed in, one Check again); an agent's page says "Stopped" in the hero's chip only. (5) Lighter:
  Routing's rules and an agent's hero put their occasional actions in a "..." (`MoreMenu` in `ui/menu.tsx`; Delete and Remove still
  ask first, Remove in the danger colour); the run graph draws the condition pills of the selected node's edges only, over the nodes,
  and keeps every condition as its edge's tooltip. (6) Words: "N connected", "spent so far", the reviewer's verdict as "approved" or
  "asked for changes" (`verdictWords`), "Search" in the sidebar's box. The other suggestions of the audit stay open.
- **P103. OpenRouter on the Integrations page (2026-10-26, on the owner's words: "Were missing OpenRouter as an integration in
  here as well").** OpenRouter stays a model provider that Models owns (its models, prices, key limits and endpoint uptime are on
  its page there), and the Integrations page lists it in a **Model providers** group, beside the services, agent tools and servers.
  Without an OpenRouter provider its panel is Models' own provider form filled in for OpenRouter (name, aggregator,
  `https://openrouter.ai/api/v1`, private, cheap), so the key is all a person types; with one, its state and **Open in Models**. One
  store and one form; an OpenRouter provider is recognised by its address, whatever it is called. `docs/integrations.md`, "As built:
  OpenRouter on the Integrations page".
- **P104. The audit and chat performance review of 2026-10-27.** On the owner's words: "Do an audit on the app, and do a full
  review on performance specifically chat performance." Report: `docs/review-2026-10-27-chat.md`. Behaviour that changed: (1) a
  streamed chat answer redraws itself only (the feed's `stream` event), not the conversation; (2) `chat.delta` and
  `chat.thinking` events are deleted after 30 days with `agent.output`; (3) a "rest of the task" approval matches the approved
  command word for word: a quoted `*` or `**` among its words is that word, never a wildcard; (4) an ACP edit is asked about with
  what it writes (the agent's own fields as the Write's `content`), and an ACP delete is always asked (it is not read as a Write
  of its path); (5) a chat turn claims the chat only at the version it read, and reads it again when a change landed meanwhile;
  (6) a chat tool summary is redacted before it is shortened; (7) the local server starts by itself only once the download's
  llama.cpp build is in place, and an update read for one build's source and backend is never offered to another; (8) a GGUF
  header is read for its own architecture's keys only, and a KV estimate refuses sizes no model has. Amended after the code
  review of the audit's own fixes (report section 4): (3) covers the command's redirections too, word for word; (4) shows an
  edit's one written text as itself, and an edit whose fields name another file than its location is asked as the agent's own
  call; the composer takes a message out of the box when it is sent and puts it back if the send fails; (7) also holds when the
  build is fetched rather than taken from the download; (8) keeps at most 64 keys, each named exactly `<arch>.<key>`.
- **P105. Release readiness: a release pipeline proven on a clean Windows runner, two approval bypasses closed, crash reports,
  the D1 notice (2026-10-27).** On the owner's words, answering the production-readiness review ("1. fix that 3. Sure build that
  4. Can you prove any of that? 5. Sure 6. Maybe do a quick scan for anything catastrophic"). (1) **Releases**
  (`.github/workflows/release.yml`, `packaging/windows/drydock.nsi`, `CHANGELOG.md`): a tag `v*` builds drydock.exe with its
  version, the tools it carries and the egress proxies, a per-user NSIS installer (`RequestExecutionLevel user`,
  `%LOCALAPPDATA%\Programs\Drydock`, HKCU only, a Start menu entry; it refuses to install or uninstall over a running app with exit
  4, tested by opening the exe for writing because Windows lets a running exe be renamed; the uninstaller keeps the data unless the
  person says otherwise), the update zip and SHA256SUMS; both files are signed when Azure Artifact Signing is configured; after
  install-test passes, a **draft** release is made, which a person publishes. A pull request touching the release path runs build
  and install-test. NSIS is pinned (3.13). (2) **Proof on Windows** (install-test, a clean `windows-latest` runner; the
  `installed-app-proof` artifact keeps screenshots and timings): Defender scans the installer and the exe; the silent install
  writes nothing under HKLM or the data folder and the installer runs as invoker; the shipped exe starts and draws its window,
  checked through UI Automation (`packaging/windows/ci/window-check.ps1`), and opens no WebView2 debugging port; a second start
  hands over to the first and exits; an install over the running app is refused. Wails clears WebView2's own debugging switches
  (`WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS`), so no other program can attach to the shipped window; the deeper flows run against a
  test variant (`app.DebugPort`, set only by that build, never shipped) placed in the same install folder and driven over CDP
  (`ui/e2e-installed`: the D1 notice, a project on a real repository, eleven workspace pages, a backup, a support bundle); the
  shipped build installed again opens on the data it made; the uninstaller removes the program and keeps the data. (3) **D1
  notice** (`ext_notices.go`, `shell/terms-notice.tsx`): built, it had never been. Until the person says "I understand", the top
  of the window says the agents run as them, on their own sign-in and plan, under their own agreement with the provider (the
  Anthropic terms linked), and that Drydock never reads, copies or sends Claude credentials; it is kept with the version of the
  text and audited. The written confirmation D1 asks for is drafted as a request to Anthropic or counsel (a Claude Docs document
  the owner keeps; not in the repository). (4) **Crash reports** (`internal/crashreport`, `ext_crashes.go`, the Support page):
  Go's crash output (`debug.SetCrashOutput`) goes to a file of the process's own; the next start makes a redacted report of it,
  keeps ten, and support bundles carry them. Local only. (5) **The scan** (three read-only reviews: data loss, security,
  availability) found nothing that loses data or bricks the app, and two approval bypasses, both fixed with tests: an agent's
  `git config core.fsmonitor ...` (and alias.*, core.hooksPath, diff.<driver>.textconv, --global and the rest) was allowed as a
  change inside the worktree although it makes git run a program, and in a linked worktree writes the repository's own config;
  now reading is allowed and writing only for a short list of keys that name no program; and `tar -I sh`, `tar --to-command`,
  `--checkpoint-action`, `zip -TT`, `sort --compress-program`, `install --strip-program` and `patch -e` were allowed; now they
  are asked (also abbreviated, clustered, and tar's dashless form). Smaller findings fixed: retention skips a run folder it cannot
  touch instead of stopping at it every day; a panic in one extension's Start leaves the rest of the app working; a backup a
  person took is kept for the daily window; project export never writes over a file; deleting a task worktree whose folder is
  gone forgets only that worktree (`git worktree prune` also forgot the person's own worktrees on an unplugged drive). **Not
  proven:** real signed-in agent CLIs on Windows (the owner's), the updater (no feed or key yet), SmartScreen with a signed build,
  Windows 10 (the runner is Windows Server), a locked Credential Manager.

- **P106. Integrations without "Needs sign-in", GitHub beside its list, vendors' agent tools, Home at start, the GPU named, one
  scanner update, Agent templates and Skills apart (2026-10-29).** On the owner's words: "when things are not signed in, it
  doesn't necessarily mean we actually have Linear or Notion etc. So lets go ahead and get rid of the Needs Sign In (as it also
  pushes it to the right and you have to scroll) ... If there is an error, lets just put like a Triangle with ! on it, so when you
  click on it, the app screen tells you what the problem is. For Github when we open it, it supresses the left pane, lets keep the
  left pane there ... When I added Linears Agent Tools, it does not show up under the Agent Tools section ... When I open DryDock
  it always opens to the project. I would rather it just open the Home Screen. I also get this message on Models on this PC: No GPU
  found ... That isn't true I have a GPU. Under Security Scan ... Just one button that updates the entire thing. Under Workspace,
  should we separate Agent Templates and Skills into there own Category instead of Library". (1) **A row's state**
  (`integrations.ts`, `ui/master-detail.tsx`): a server waiting for its sign-in or token, one of your own without tools yet, and a
  provider not checked yet are **Set up**, not "Needs you"; only a failure needs the person (a refused key, a failed check, a
  failing sync, a server that could not connect, OpenRouter down), and its row shows a warning triangle (`row-alert`, named by
  the state for screen readers) instead of words; the detail opens with what is wrong (`RowState.problem`, `row-problem`). The
  list's rows no longer widen it (a grid row now shrinks, `& > li { min-width: 0 }`): a long line under a name pushed the state
  past the list's edge and the list scrolled sideways (measured: 331 px of content in 319). (2) **GitHub is a row of
  Integrations** (`/integrations/github`; `/github` redirects there): its detail is the GitHub settings that were a page of their
  own (`GitHubDetail`: the gh sign-in, issue sync, the MCP server, repositories, CI runners), so the list stays beside it.
  (3) **Agent tools lists every vendor's MCP servers** (`agentToolVendors`): Linear, Notion, Sentry and GitHub have a row there
  too (`tools-<vendor>`), apart from their app sync under Services; a connected vendor server used to show only inside the app
  sync detail and was left out of Your servers, so it was nowhere under Agent tools. Aikido publishes no MCP server Drydock knows
  of (its entry was removed earlier, P45/D14); one can be added under Your servers by its address. (4) **The window opens on
  Home** (`app.StartURL = "/home"`, kept equal to the UI's Home route by a test); `/` stays the chosen project's page. (5) **The
  GPU** (`internal/localllm`): the engine's environment keeps what a GPU driver and its loader read (`SYSTEMDRIVE`,
  `DRIVERDATA`, the processor facts, `CUDA_PATH*`, `VK_*`, `GGML_*`; none carries a credential), which the agents' allowlist had
  stripped; a device line without its memory is read; when the engine lists no GPU, Drydock reads Windows' display adapters
  from the registry (`DisplayAdapters`, no program started) and keeps the engine's own backend lines (`Devices.Note`), and This
  PC says "Windows has <card>, but the engine could not use it" with "What the engine said", instead of "No GPU found". Not
  proven on the owner's PC: whether the stripped variables were the cause there. (6) **Update scanners** (Security scan and a
  project's Security page): one button calls `security.tools.checkUpdates`, which installs the newest OSV-Scanner and Opengrep,
  and says per tool what it did; Gitleaks is compiled into Drydock and updates with the app, and the note says so. (7) **Agent
  templates and Skills** are two sidebar entries (`nav-library`, `nav-library-skills`) instead of Library with two pills; the
  page `/library` is titled Agent templates.

- **P107. Chats start in the chat, without a project, and name their model, effort and mode (2026-10-29).** On the owner's
  words: "when we create a new chat, can we just go right to the chat window, and then from there we can choose the Model, Effort,
  and Permission. And instead of it just saying Adapter Default, can you just show the Model Name, Effort and Permission Mode" and
  "under chats, I don't want it to be assigned to a project, sometimes I Just want to have a chat that is not assigned to any
  project". (1) New chat on the Chats page (button, Ctrl+N, palette) calls `chat.create {}` and opens the chat: no project (the
  core always allowed one; the dialog had preselected the current project), Claude Code, the chat's private folder. The new chat
  dialog stays for Home's Options. The chat list shows every chat by default. (2) The core keeps what the CLI said its session
  runs with (`chats.cli_model`, `cli_permission_mode`, migration 00133, from Claude Code's `system/init`); a new chat that
  chooses no model starts with the default its CLI last reported; a change of model, CLI or mode clears them until the next
  turn. (3) The composer's chips say "Opus 5.5 · medium effort" instead of "default model": the effort shown when none is chosen
  is Claude Code's documented per-model default (headless `system/init` carries no effort), so a person's own `effortLevel`
  setting can make it differ; "CLI default (Opus 5.5)" and "default (medium)" replace "adapter default" in the settings. Not
  proven against a real Claude Code: the `permissionMode` field of its init message (the stand-in sends it; the SDK's type
  names it).

- **P108. Routing as a wiring diagram (2026-10-29).** On the owner's words, with a picture of mode and feature cards wired to one
  node: "when it comes to routing, is it possible to have this design at all?" and "go ahead and build the routing diagram". The
  Routing page opens with **Wiring** (`screens/routing/wiring-diagram.tsx`, layout and words in `wiring.ts`): a card per level of
  agent work (cheapest, fast, balanced, quality, maximum quality, each its own colour) and per other kind of turn at the
  balanced level (review, room reply, spec, tests, design, security review, consult, summarize, classify, grey), each naming the
  model the router would choose now ("Haiku 5.5", its provider and effort) or "Blocked" with the router's reason as its
  tooltip, wired by a curve into one Router node. The answers are dry runs with the Preview panel's defaults (role backend,
  private context), all in one call (`route.previews`, new: up to 32 `route.preview`s, each answer or its error in order; the
  smoke test flags a page that asks one method fourteen times): nothing is recorded and no quota is taken; they are read again when a rule, a provider or the
  cheap model changes. When every card is blocked for the same reason (no provider yet), the page says it once, with a link to
  Models. The model names are `lib/model-name.ts`, shared with the chat chips (P107).

- **P109. Import Claude Code's own chats into Chats (2026-10-29).** On the owner's "Yes" to the one item that waited on their
  decision (a one-time, read-only import that continues each session), with the shape proposed for it. **Import from Claude Code** on the Chats screen (beside New,
  and under an empty list) lists the sessions Claude Code kept on this PC, every one not imported yet chosen, and copies the
  chosen ones in (`chat.importList`, `chat.import`; `internal/chats/claudeimport.go`, `transcript.go`; docs/chats.md section 7).
  Sources: `%CLAUDE_CONFIG_DIR%` or `%USERPROFILE%\.claude`, and `~/.claude` in each WSL distro where Claude Code is installed,
  read through `\\wsl.localhost\<distro>`. Only `projects/<folder>/<session id>.jsonl` is opened (never the sign-in, settings,
  `.claude.json` or plugins: D1); links are not followed; a file over 64 MB, a line over 8 MB and a line that is not readable
  JSON are skipped. A chat gets the person's messages and the text answers at their original times, a tool call as "Used tool
  <name>", no reasoning, tool output or sub-agent lines, everything redacted before it is stored; its title is the session's
  name, summary or first line. It is a Claude Code chat (in the distro for a distro's session) whose session id is the imported
  one, so its next turn resumes that session in the session's folder; a session in a project's repository folder joins that
  project, any other is a chat without one (P107). The session id is unique among imported chats (migration
  `00134_chat_import.sql`): importing again does nothing, deleting the chat frees it. Sessions Drydock started (its chats' and
  agents' sessions, its own folders) are not listed. **Not verified:** the transcript format against a real Claude Code (it is
  undocumented), anything on Windows, a real resume of an interactive session, and the first turn's cost (Claude Code may count
  the session's earlier cost again; its baseline is in `.claude.json`, closed by D1).
- **P116. Restart now after a staged restore, and the WebView2 version in the support bundle (2026-10-29).** On the owner's
  words, answering the list of what is not built: "Proceed with those items". (1) The Backups page shows **Restart now** beside
  a staged restore when the window can restart (`app.info` `canRestart`); `shell.restart` (a person only) asks the shell
  (`core.Config.Restart`, `app/restart_windows.go`) to start `drydock.exe` again with `CREATE_BREAKAWAY_FROM_JOB` (the app job
  allows it, as for the updater's helper) and quit; the new process carries the old one's process id in
  `DRYDOCK_RESTART_AFTER`, removes it from its environment, and waits up to a minute for that process to end before the
  single-instance check, which would otherwise hand the launch back to the closing app. Running agents stop as on any quit. A
  browser build says to quit and reopen. (2) The support bundle's `os.webview2` is the version the window reports
  (`core.Config.WebView2Version`, from Wails' environment info). Neither has been run by hand on Windows.
- **P111. Local models: several loaded at once, vision, drafts, embeddings and gated models (2026-10-29).** On the owner's
  words, answering the list of what is not built: "Proceed with those items" (the items of docs/local-models.md section 10 but
  Linux and macOS, which a Windows-only app does not need). (1) **Several models at once**: the person keeps up to four
  downloaded models loaded (llama.cpp's `--models-max` default), Load too and Unload on the model rows, within the memory the
  page already estimates (each model's files, KV cache and draft against the fit rule's room); what does not fit beside the
  loaded ones is refused on the page and by the core (`local.server.start {modelIds}`). Two or more run llama-server's router
  mode with a preset file Drydock writes (a section per model id: `model`, `mmproj`, `model-draft`, `spec-type`,
  `load-on-startup`), `--models-max`, its own empty `LLAMA_CACHE`, and the shared options on its command line, as llama.cpp's
  `tools/server/README.md` at build b11936 (and the TurboQuant fork's at b10298-2.0.0) describes them; a change to the set starts
  the server again. The provider stays one row: the probe offers each model the router lists as loaded, with the context its
  instance runs with (`/props?model=`). (2) **Vision**: a repository's projector goes with each variant, is checked and kept with
  it, and is passed with `--mmproj`. (3) **Drafts**: a model can be drafted for by a downloaded model of the same family and
  tokenizer at most half its size (read from both GGUF headers), chosen on its row and run with `--model-draft` and
  `--spec-type draft-simple`. (4) **Embeddings**: a model whose header names a pooling type or an encoder architecture, or that
  the person marks so, is served only by a second llama-server of its own (`--embeddings`, port 9934, its own key) and is not a
  provider (the router would offer it for chat). (5) **A Hugging Face token** for gated models: kept in the credential store
  like a provider's key, taught to the redaction scanner, shown as `hf_…1234`, removable; sent as the Authorization header of a
  file download from the Hub's own address only (dropped on the redirect to its CDN, never with a listing). A gated repository
  is listed and marked; without a token its download is refused at once with the page to accept the licence on, and a download
  the Hub refuses says why with that page. The stand-in llama-server runs both of its modes and refuses a projector or draft
  that is missing and a draft without its spec type; the Hub fixture gates files by token and redirects downloads to a CDN.
  No migration: the choices are in the `local:settings` JSON and each model's `model.json`. docs/local-models.md section 11.
  **Not built:** loading or unloading one model while the others run (the router's load and unload calls), anything in Drydock
  that reads embeddings, a projector's memory beyond its file, draft heads that are not models (MTP, EAGLE-3). **Not verified:**
  any of it against a real llama.cpp build, the real Hub or a real token, or on Windows.
- **P110. Expert packs read PDF, DOCX and git repositories, and a model answers their questions (2026-10-29).** On the owner's
  words, answering the list of what is not built: "Proceed with those items". From the expert-pack list
  (docs/expert-packs.md, "Not built"), everything but the embedding index, which v1 does not plan. (1) **PDF and DOCX**
  (`internal/docparse`): a PDF through `github.com/ledongthuc/pdf` (pure Go, BSD-3-Clause, the newest commit's pseudo-version
  0.0.0-20261020125853-5e46805f718e, since the module has no tags), page by page; a DOCX through `archive/zip` and
  `encoding/xml` alone (headings, lists, tables). Every file is untrusted: 32 MB, 1000 pages, 16 MB of text, 128 MB unpacked
  for a DOCX whatever the zip says, 60 seconds, at most two PDFs at once, a reader panic is the file's error, and a PDF without a
  text layer is refused as a scan (no OCR). A pack's `local_dir` reads them; a PDF passage keeps its page (migration `00136`,
  `Citation.page`) and a DOCX passage its headings. The Expert packs page makes a private pack of picked files
  (`pack.createFromFiles`, `pack.files`, `pack.addFiles`, `pack.removeFile`; its own `packs/<id>/files` folder), and the
  Requirements page reads a PDF or DOCX into the text box to be checked (`requirement.extract`). (2) **`git_docs`**
  (`git.FetchDocs`): url, ref and path; a fresh bare repository, a shallow fetch with no blobs, then only the wanted Markdown,
  MDX and text blobs by id, read with `cat-file`; nothing checked out, no hooks, submodules, symbolic links, lazy fetch,
  credentials, proxy or personal git configuration; the address checked as the crawler checks it and pinned for git; a size
  bound (`crawl.max_repo_bytes`, 256 MB); the commit each version read is recorded (migration `00137`); the pack's refresh
  schedule applies. (3) **Answers and summaries by a model** (`internal/packs/packmodel`): CONSULT routes run kind `consult`
  and a refresh digest `summarize` through the app's router and LLM client at level cheapest, with the pack's context ceiling
  (a private pack never reaches a trainable provider); an answer must cite supplied sources only, the sources it used are
  quoted, and with no model or a router that blocks, the answer is the cited passages with the reason. (4) **Decided against:**
  OpenCode `instructions` globs, because OpenCode already finds the run-start skill copies in `.agents/skills` and
  `instructions` would load each matched skill into every turn (the cost P37 measured), and Drydock's OpenCode configuration
  is unverified. **Not verified:** `git_docs` against a real host and on Windows, a real model answering a consult, large files
  through the Wails bridge, PDFs from real producers beyond the tests' hand-written ones. docs/expert-packs.md "As built: PDF
  and DOCX, packs of files, git_docs and routed answers"; docs/model-routing.md "As built: expert-pack consults".

- **P112. The LLM tiebreak, pre-call budget reservations, wall-time ceilings, the lead's turns routed and the retention
  refusal acted on (2026-10-29).** On the owner's words, answering the list of what is not built: "Proceed with those items"
  (the routing items of `architecture.md` section 5 and `model-routing.md`, except the OTLP exporter and the B0 arm, which are
  another decision's). (1) **Tiebreak**: when the ranking leaves candidates within 10% of each other (or without a price, as
  an agent CLI's own models), one call to the configured cheap model (`route.cheap.*`) chooses from a description that holds
  no content of the work (the turn's kind, class, level and weights, the candidates' facts); off by default, its own daily
  budget ($0.25) and timeout (8 s), the ranking's choice on any failure, recorded on the decision (`DecidedBy "llm"`, "Why
  this model?"); never in a dry run, at Cheapest, on a pin or a fallback hop. (2) **Reservations** (migration
  `00140_budget_reservations.sql`): before every agent turn its estimate is reserved against its project, task, run and agent
  budgets with what the turns in flight hold, in one write transaction, and held on its provider's quota windows; a turn
  that does not fit waits on the budget pause; settled, released, or expired at start. (3) **Wall-time ceilings** per level
  and per agent (none by default; migration `00141_turn_stops.sql`): a turn past it is stopped with the reason on its decision
  and outcome (`routing.stopped`); a workflow turn then waits for a person, one that did no work may move along its fallback
  chain. (4) **The team lead's and the recommendation turns** (P52) are routed like the role turns of P63: run kinds `lead`
  and `recommend`, the same budgets, fallback and records; no list keeps the configured model (`static`). (5) **Retention**:
  a model a provider refuses because the organization has no data retention enabled (Anthropic's Covered Models) is set aside
  (migration `00142_model_retention.sql`): the router leaves it out with what to do, the call or turn moves to the next model,
  which keeps no data, or ends saying why; a person clears it on the Routing page. The Routing page has a "Tiebreak and turn
  limits" section; the wiring names the new kinds and shows a turn's ceiling and a tie. Details and differences from the
  design: `docs/model-routing.md` "As built: tiebreak, reservations, ceilings, the lead's turns and the retention refusal".
  **Not verified:** a real cheap model's tiebreak, a real CLI stopped at a ceiling on Windows, every CLI version's retention
  wording, holds during a real provider's CLI turn.
- **P114. Integrations: webhooks, typed clients, mappings, status sync, auto-approve classes, attachments (2026-10-29).** On the
  owner's words, answering the list docs/integrations.md gave as not built: "Proceed with those items". (1) **Webhooks**: a
  receiver, off by default, listening on 127.0.0.1 only at a port the person chooses, for Linear (`Linear-Signature`, the signed
  `webhookTimestamp` within 60 s, as `@linear/sdk` 100.0.0 checks), Sentry (`Sentry-Hook-Signature` with the client secret,
  `Sentry-Hook-Timestamp` within 5 minutes, `Request-ID`, from Sentry's webhook documentation) and Notion (`X-Notion-Signature`
  keyed with the verification token, the event's `timestamp` and `id`; **unverified**, from memory: Notion's documentation was
  unreachable). A delivery is refused when it is unsigned, signed wrongly, larger than 1 MiB, stale or a replay (its id is kept a
  day); a verified one only queues a read of the item it names through the normal adapter, so nothing in the payload becomes item
  data. Drydock does not expose the port: the page says, always, that the person must (their own tunnel). The secrets are in the
  credential store; Notion's verification token is used only when the person says so. (2) **Typed clients**: the REST and GraphQL
  clients exist since P42; added are the typed calls the listing could not do (one item, attachments, comments, team states, a
  data source's schema, a page's properties), through the same client, credential, rate limit and time-outs; a file a vendor keeps
  on another host goes through `internal/netguard` without the credential. (3) **`integration_mappings`** per assignment, chosen by
  a person: vendor states and task statuses with a direction; which Notion property fills state, priority, assignee, labels.
  (4) **Notion data sources as typed rows**: a row's properties with their types, cleaned and scored like any outside text, the
  first status property its state, a `unique_id` its reference, `item.props` in rules. (5) **Two-way status sync**: a linked task
  entering a mapped status proposes the vendor write through the ordinary outbox (Linear `set_state`, Notion `set_status`, Sentry
  `resolve`/`unresolve`); the vendor's state of a linked item is offered to the person as a change to the task, never applied by
  itself (P37). (6) **`link_task` rules**: link a matching item to a named task, or to the project's task with the item's
  reference; disabled and dry run first, daily limit, risky items never. (7) **Auto-approve classes**: a closed list,
  `linkback_comment` and `status_sync`, each off until a person turns it on for one connection in one project; nothing destructive,
  suppressing or in bulk; only Drydock's own templated writes name a class, never an agent's or a tool's proposal; the approval is
  decided in the name of the person who turned it on and the send is recorded `auto: true`. (8) **The comment lookup before a
  retry**: a failed comment is first looked for on the target by a hash of its text; found, it is recorded as sent; otherwise the
  same call is proposed again for a new approval. (9) **The weekly automatic sweep**, in the backups' scheduler style: on unless
  turned off; a due poll becomes a full sweep once a week. (10) **Attachments**: listed and downloaded only on a person's action,
  capped at 10 MiB, kept under the app's data folder, a text file only as its secret-redacted copy, which is the only text of it an
  agent is given. Migrations 00144 to 00147. UI: Webhooks and sweeps under Background on the workspace's Integrations page;
  Mappings, Auto-approve, Full sync, attachments, typed properties, the offered status and Retry on a project's Integrations page.
  docs/integrations.md "As built: webhooks, mappings, status sync". **Not built:** Aikido and GitHub webhooks, a relay of Drydock's
  own (never), attachments of GitHub and Aikido items and Notion file blocks, importing a Notion row as a task, a vendor's status
  applied without the person. **Not verified:** any of it against a live vendor (protocol fixtures only), Notion's signature
  scheme least of all, or on Windows.
- **P113. OTLP export of the local telemetry, spans for the LLM Wrapper's calls, and the B0 arm of the evaluation
  (2026-10-29).** On the owner's words, answering the list of what was not built: "Proceed with those items". (1) **OTLP
  export** (`internal/telemetry`): off by default; a person gives a collector's endpoint (https, or plain http only to this
  computer, checked on the connection itself; no credentials, query or fragment in it) and turns sending on, and telemetry must be
  on too. What is sent is what the local sink stores, under the same closed allowlist (applied again by the encoders), so the
  local rows are the log of everything sent and the preview shows what leaves (`packaging.md`, diagnostics). OTLP/HTTP with
  protobuf (default) or JSON, **written by hand** rather than through the OpenTelemetry SDK: the spans and metrics already exist,
  only their wire form was needed, and the SDK with its protobuf and exporter modules would add dependencies (the "about 15 MB"
  `packaging.md` names) for no gain; no module was added. Header values (an API key) in the credential store, never in settings,
  the status, a log or an event. A bounded queue (2048), batches of 256 every 5 s, retries with exponential backoff and
  `Retry-After` for a transport failure, 429, 502, 503 and 504, drop-and-count on a full queue, a refusal or exhausted retries;
  turning it or telemetry off empties the queue and stops a send in flight. A status line (sent, waiting, dropped, last success,
  last error in Drydock's own words with header values removed) and "Send a test span" (also kept locally). Methods
  `telemetry.otlp.set` and `telemetry.otlp.test`; the Telemetry group of Settings. (2) **The Wrapper's calls**: `llm.Options.Tracer`
  hears of every call, refused ones included, with no field for content; the sink records a `drydock.llm.chat` span and duration,
  token and cost metrics; new allowlisted keys `drydock.llm.cache_hit`, `drydock.llm.fallback`, `error.type` (a closed class).
  (3) **B0** (`internal/trial/b0.go`, `drydock-trial run -arms ...,B0`): the naive broadcast team of `evals.md` section 5 as a
  harness arm: a worker and a reviewer on one model, each turn a new Claude Code session given the whole room in full (every
  message, tool call and tool output), free-for-all until both say DONE in one round, five rounds, the wall-clock cap or the
  run's dollar ceiling (a recorded `ceiling`); no router, cheap tier, handoff, cache or watchdog. It records the arm's rounds,
  stop and broadcast bytes besides section 6.2's fields. No migration. Details: `token-control.md` "As built: OTLP export and the
  Wrapper's spans", `evals.md` section 15. Built since by P121: gzip bodies, exporting rows recorded before sending was on,
  content export (removed by P126), crash reports and usage counters as their own categories, B0 with CLIs other than Claude
  Code and on a cheap provider. **Not verified:** a real collector or vendor backend (the protobuf encoding is checked only against a decoder written from the
  same field numbers), Windows, a proxy; **B0 is not measured**: its mechanics and records are proven against the scripted
  stand-in agent only, and every B0 row of `evals.md` 7.1 stays "not measured" until it is run with a signed-in Claude Code.
- **P115. The per-role MCP tool table, Antigravity's headless start, the custom CLI dialect and live packs' docs servers in
  agent runs (2026-10-29).** On the owner's words, answering the list of what is not built: "Proceed with those items" (the
  agents items of `architecture.md` section 5). (1) **Role table** (`internal/mcp/rolepolicy.go`): section 3 of
  [`mcp.md`](mcp.md) is the gateway's policy, a row per role (the template roles, then every other role, which keeps all a
  grant gives) and a column per Workspace tool (`read_range`, `repo_map`, `skills`, `search_memory`, `search_requirements`,
  `search_pack`, act) and kind of server (docs servers, GitHub, trackers, Sentry, other, by the catalog entry a library
  server was connected from). PM reads requirements, memory and packs, the UX Designer requirements, packs and files, a
  reviewer acts by `room` and `handoff`; `propose_memory` stays with every role (P92), `repo_map` is on by default for every
  role (P62), the act column still needs `mcp.act_tools`. A project changes any cell on its Integrations page (migration
  `00148`, `mcp_role_policy`; `mcp.rolePolicy.get/set`), which also shows the act switch for the first time. **CEL rules**:
  `cel.dev/cel-go` was already a dependency (security, router), so a project keeps up to 32 boolean expressions over `role`,
  `column`, `server`, `tool`, `adapter` and `task` that only take tools away (`mcp_policy_rules`, `mcp.policyRule.add/remove`;
  checked when saved, a set that no longer compiles gives a run no MCP and says why). **A task narrows** its agents' tools to
  exposed names or globs (migration `00149`, `task_mcp_tools`; `task.mcpTools.get/set`, the task page's "Agent tools"); it
  never widens. Rules and narrowing are enforced by the gateway on every list and call (`RunPolicy.Refuse`); the permission
  prompt and the sandbox's command tools are exempt (they carry how a turn works). Every change is audited. (2)
  **Antigravity** starts (`internal/agents/headless`): `agy -p --input-format stream-json --output-format stream-json --mode
  default` with `--effort`, a stream-json user message per stdin line, `init`, `step_update` and `result` read as the
  documentation names them, `denied_actions` as permission_denied chunks (D11), `AGY_ERROR` and exit 3 as the turn's error;
  interrupt, resume and model choice are not offered; still behind its acknowledgement gate. **The custom dialect**: a
  person's manifest in `<app data>/adapters/custom/<id>.yaml`, added and removed on the Agents page
  (`connection.custom.list/template/add/remove`), validated strictly (its own gate `custom-<id>`, the prompt on stdin or as
  `{{prompt}}`, only `cwd`, `model`, `effort`, `session_id` and `prompt` placeholders, field paths per event, no provider,
  MCP or keychain sections, native only, refused when it looks like it holds a secret) and run by a generic runner: one
  process per turn, JSON lines by type and field path or plain text, the session id carried to the next turn's
  `{{session_id}}`, interrupt by ending the turn's process. The conformance suite gained per-turn sessions and runs against
  both stand-ins (`fakeagy`, written from agy's documentation; `fakecustom`, the template's CLI). (3) **Live packs' docs
  servers** reach an agent run: the catalog server of a live or hybrid pack attached for the agent, with exactly the pack's
  tool, under the role's docs column, through the gateway's catalog trust, cache, send guard and untrusted marking
  (`addLivePackServers`); `search_pack` still queries live packs itself. **Not verified:** a real `agy` (every field name,
  the `--mode` value, the input line, exit codes), a real custom CLI, any of it on Windows (a `.cmd` program, the prompt as
  an argument through `cmd.exe`). **Not built:** MCP for Antigravity (its documented headless mode has no way to take a
  per-run MCP configuration; P117). Built since by P117: MCP for custom CLIs (the manifest's `mcp` section), the
  acknowledgement asked again when a custom manifest's file changes on disk, interrupt for Antigravity.
- **P117. MCP for custom CLIs, interrupt for Antigravity, a custom CLI's acknowledgement bound to its manifest file, and the
  call after an approval compared with the approved one (2026-10-29).** On the owner's words, answering the list of what is
  still not built: "Yes" (the "can be built now" group; the agents items of P115's "Not built" line). (1) **MCP for custom
  CLIs**: a person's manifest may carry an `mcp` section, `{via: arg, format: mcpServers, args: [..., "{{mcp_config}}"]}`
  (a flag that names the file) or `{via: file, format: mcpServers, file: <relative .json path>}` (a file the CLI reads from
  its working folder), checked as strictly as the rest of the manifest (known keys only, the one format, `{{mcp_config}}`
  exactly once and no other placeholder, a clean relative path inside the working folder and outside `.git`). The
  configuration is the `mcpServers` JSON with the run's single `drydock` entry (the stdio shim). Like the other unproven
  CLIs (P20) the gateway reaches it only after a person allows it for that CLI (`mcp.try.set`), then under the role's row of
  the tool table, rules and narrowing (P115); a custom CLI without the section can never be allowed. The file route never
  overwrites a file that is there, keeps the path out of git (`info/exclude`) before the token is written, refuses a folder
  on the way that leads outside the working folder, writes it for the owner only and removes it (and the folders it made)
  when the run ends. Proven with the custom stand-in reading the configuration it was given by either route, starting the
  shim and listing and calling gateway tools. (2) **MCP for Antigravity: not built.** agy's MCP servers live in its own
  `mcp_config.json` (phase0 section 8); its documented headless mode (`-p`, `--input-format`, `--output-format`, `--mode`,
  `--effort`, `--json-schema`, `--print-timeout`) has no flag, variable or per-session file for an MCP configuration, and
  writing the gateway into the person's own `mcp_config.json` would change their setup for every agy they run. Nothing was
  invented; Antigravity stays "cannot be given it". (3) **Interrupt for Antigravity**: print mode documents one way to stop
  a turn, Ctrl+C, which ends the process with a non-zero code. Interrupt ends agy's process tree through the existing kill
  path (the Job Object on Windows; the direct process on the development host), the turn ends "cancelled", the session
  stays (Wait answers only when agy exits by itself or Stop ends it), and the next message starts agy again as a new
  conversation (print mode has no documented resume), said in a log chunk. With no turn running it does nothing. A custom
  CLI's interrupt (ending the turn's process) was built by P115. The conformance suite's interrupt step runs for both
  stand-ins. (4) **A custom CLI's acknowledgement is bound to its manifest file**: the SHA-256 of the file a person
  acknowledged is kept (`customcli.ack_sha256.<id>` in settings). Whenever the file is looked at again (the Agents page
  lists the CLIs or checks one, a person acknowledges, any session of it starts) a file whose bytes changed is loaded again
  if it still validates, and the acknowledgement is taken back (audited and published as `connection.custom_changed`) until
  a person gives it for the new file; a file that no longer loads cannot be acknowledged. At start a file changed while the
  app was closed, and an acknowledgement kept from before the hash was, are asked again. There is no file watcher: the
  checks run at those moments. `Runtime.StartGate` checks a gated CLI's acknowledgement at every session start (before,
  only making an agent or a chat did), so a changed file stops the next run. The Agents page marks the notice "changed" and
  says why (`GateStatus.changed`). (5) **The call after an approval is compared with the approved one**, wherever a call is
  approved by hash and then executed: write-class MCP calls (the approver gets its own copy of the bytes; the core computes
  the descriptor, its digest too, from the call's bytes when it asks and again when it consumes, and refuses a request whose
  hash is not of its bytes; the gateway compares the target and the hash of the bytes about to be sent with the approved
  ones, in constant time, right before the call is made); ACP permission requests (an ACP agent runs an allowed call by
  itself, so what it reports of the call afterwards is compared with what was allowed: its kind, input and, for read, edit,
  delete and move, its paths; on the first difference the person is told, the turn is cancelled, and
  `permission.call_changed` is audited and published with both hashes, never the input). Claude's permission prompts (the
  gate recomputes the descriptor from the request it answers and the answer's `updatedInput` is the exact input) and
  integration writes (`Execute` rebuilds the descriptor from the stored row) already did; tests now tamper with the
  approval under each path and are refused. No migration (settings keys only), no new dependency, no RPC method. Details:
  `connections.md` "As built: MCP, interrupt and acknowledgement", `security.md` section 5, `mcp.md` section 10.
  **Not verified:** any real custom CLI, a real agy, Windows (the Job Object tree kill of agy, a `.cmd` program, the
  working-folder file's ACL). **Not built:** MCP for Antigravity (above); a file watcher on the custom folder (the checks run
  when the file is looked at); preventing a changed ACP call (Drydock can only detect it and stop the turn: the agent runs
  its calls itself); the comparison for a chat's permission requests is the same, but only an agent turn's is audited.
- **P119. A ceiling on the whole context package, files as ranges, the missing handoff fields, verified agent handoffs and
  rule 15 over every deterministic package (2026-10-29).** On the owner's word, answering the list of what is still not built:
  "Yes" to building the whole group that can be built now (token rules 9, 11, 5 and 6, and 15 of `token-control.md`). (1) **The
  package ceiling** (`internal/context/assemble.go`): a work turn's package (the instruction files the CLI reads, the handoff, the
  prompt as pieces) is assembled under a ceiling in estimated tokens, a project setting (`DevSettings.ContextCeiling`, the
  Development flow group; default 20,000, 4,000 to 200,000). Over it, file excerpts, the repo map, memory, the skill list, design
  notes, the spec, the diff, the handoff and check output are cut or left out in that fixed order (`ctxeng.CutOrder`), the latest
  piece of a kind first; the task and the role, project and pack instructions never are. A cut quoted block stays closed and ends
  with a pointer to the rest. The generated AGENTS.md keeps within half the ceiling by the same order and names what it left out
  in its last line. Every piece is a manifest entry (`kind`, `tokens`, `exclusion: over_budget` and the reason) and every
  package gets a `budget:package` line; review and role turns are measured, not cut. (2) **Files as ranges**
  (`internal/pipeline/files.go`): the files a task names and the worktree files a failing check points at enter a work turn's
  package (a later pass only the latter), at most 6 and 24 KB; a file of more than 150 lines (or 12 KB) only as at most two ranges around the lines the check
  names or the first lines that mention the task's identifiers, else its head, with a `read_range` pointer; redacted, read
  through `safefs`, binaries left out. (3) **Handoffs**: Drydock's artifact gained `goal`, `acceptanceStatus` (by the recorded
  facts), `worktreeRef` (safe point and head), `decisions`, `files` with content hashes, `failingCheckSignature`,
  `commandsOfNote`, `evidenceRefs` and `nextStep`. The `handoff` tool takes the fields an agent can state (with `checks` as
  `{name, passed}`), and a worker that has it is asked in its own prompt to leave one when it stops early (no extra paid turn).
  Before a fresh session is seeded, the newest unchecked handoff the previous turn's agent wrote during that turn is checked
  (`pipeline.VerifyAgentHandoff`: required fields, size bounds, no secret, files that exist or were changed, check claims equal
  to the latest recorded results); one that passes gives the session its account and next step, one that fails falls back to
  Drydock's, and the result is stored on the agent's handoff (migration `00155`, `handoffs.verification`, classified in
  `internal/projectio`), in Drydock's artifact, the manifest and the `agent.session` event. (4) **Rule 15**:
  `TestDeterministicPackagesDoNotImportLLM` runs the `go list -deps` boundary over router, watchdog, room (markers, minutes,
  inbox), checks (the summarizer), context, repomap and budget, and proves it can see a dependency; `docs/evals.md` defines no
  eval-corpus assertion, `token-control.md`'s original proposal did ("`model_usage.purpose` in the deterministic set is empty in
  the eval corpus"), and it is sound, so it is built as the report's rule 15 section (`evals.DeterministicPurposes`) with a
  source scan that no product code names such a purpose. No new dependency, no new RPC method; migration 00156 was not needed.
  **Not built:** cutting review and role prompts, a separate turn that asks for a handoff, a per-criterion acceptance status, a
  tokenizer. **Not verified:** a real agent writing a handoff with the tool (the tests store one the way the tool does), any
  effect on what a task costs (no eval run), Windows.
- **P118. A per-project routing level, a workflow budget scope and budget items in the Inbox, turn grants the runtime
  enforces, and the group-collaboration workflow node (2026-10-29).** On the owner's words, answering the list of what is not
  built: "Yes" to building the whole "can be built now" group (the routing, budget and turn-taking items). (1) **Level**
  ([`model-routing.md`](model-routing.md) "As built: the workspace's level and a project's own"): the workspace has a level
  (Balanced until a person chooses another on the Routing page) and a project may choose its own on Project settings
  (`route.level.get/set`, settings rows, no migration); every agent turn, static decision and wall-time ceiling of the
  project uses it, and each decision records the level and whether it was the project's own, which "Why this model?" says.
  (2) **Budgets** ([`token-control.md`](token-control.md) "As built: workflow budgets, budget items, turn grants"): a
  `workflow` scope (migration `00152` rebuilds `budgets` for the CHECK and adds `budget_reservations.workflow`), counting every
  run of one workflow in the project, seen by the gate, the reservation and the router's budget state, set on the Budgets
  page from the workflows the core runs (`budget.workflows`); and a `budget` kind in `room.NeedsYou`: a warning, a limit
  reached and a run's budget pause are typed items with `budgetState` and what to do (a warning or limit opens the Budgets
  page and can be dismissed; a dismissal never hides the alert fired again after a limit change). (3) **Turn grants**
  ([`room-protocol.md`](room-protocol.md) s6 "As built"): the room's scheduler stores a single-use grant (room, thread,
  trigger, agent, round, 30-minute expiry; migration `00153`, `room_turn_grants`) for each reply it plans and each turn of a
  group discussion; `agents/runtime.Spawn` refuses a session that names a room without a grant it can redeem (never issued,
  changed, another agent's, used, expired, or no scheduler connected), and a fallback hop gets a renewed grant. The run kinds
  that need one are turns in a room; task work, review, the template roles, the planner, the lead, recommendations, a
  person's chat and a connection test need none. The reply rules that lived in the responder (an agent's message in an
  auto room wakes only whom it mentions; a DM's agent answers its person) moved into the scheduler. (4) **Group node**
  ([`workflow-engine.md`](workflow-engine.md) s14): `workflow.KindGroup` and `responder.GroupNode`, a bounded discussion
  between named agents in a room on the sequential round counter (default 3 rounds), ending early when every agent passes,
  resolved by the scheduler into a DECISION candidate or a blocking QUESTION, its result routed `decision` or `question` to
  the next node; tested in the engine against the scripted stand-in agent. A promoted `[DECISION]` line no longer counts as
  a turn in the round counters. UI: Project settings' "Model routing level", the Routing page's "Level", the workflow scope
  in the budget form, budget items in the Inbox, a group node's icon in the run graph. **Not built:** a shipped template
  that uses the group node and a way to start one from the UI, group and reply turns counted against a workflow budget, a
  task-level or template-level routing level, the `ContextPlan` and `MaxRounds` inside a grant, ending a discussion on
  `SHIP`. **Not verified:** any of it with a real agent CLI (only the scripted stand-in), Windows.
- **P123. Resume, time travel and replay of a run, and the nested subworkflow view (2026-10-29).** On the owner's words,
  answering the list of what is still not built: "Yes" (the "can be built now" group; `ui.md` 6.2's "Not built: Resume /
  Time travel / Replay actions (the engine has no service call for them yet); subworkflow nodes are drawn as one node").
  (1) **Engine calls** (`internal/workflow/timetravel.go`; `workflow-engine.md` section 6 says exactly what each does and does
  not do). `Continue(run, by)` resumes, in place, a run that FAILED or that a dead driver left RUNNING once its lease ran out:
  claim, lease, one fenced compare-and-swap to RUNNING (the only write that takes a run out of FAILED, and only on a person's
  word) with an `entry` checkpoint whose parent is the latest of the same run; the step that stopped runs again as after a
  crash (finished nodes reused, the failed or cut-off one at its next attempt with `Previous`); the same run id keeps its
  budgets, approvals (a rejection given stays a rejection: the gate reads it again and the run fails again without asking),
  loop counters and safe points. `TimeTravel(checkpoint, by)` forks a new run with the state as of any checkpoint; its first
  checkpoint's parent is that checkpoint, its state carries `Origin{fork, run, checkpoint, step, by}`, and its open requests
  get new approval rows in the transaction that creates it, the parked node keeping its inbox (`ResumeFrom` used to drop a
  `Retry` request's inbox and write the requests in a second transaction). `Replay(run, from, by)` runs an ended run's nodes
  again from a checkpoint of its lineage (the entry by default) with the recorded inboxes, `Origin{replay}`; a node marked
  `MetaEffect` (`Node.Effect(what)`, metadata outside the topology hash; the pipeline marks `merge`, the GitHub merge,
  `push_pr` and `plancreate`) never runs there without a fresh approval of the replay: a person's approval that is its own
  input (the answer just before it in this run) or the request it raises first (gate `replay`, consumed by the one
  execution it lets through; a rejection ends the replay with the node not run). None of the three rewrites a checkpoint, an
  execution or an approval of another run, and none rewinds anything outside the engine. Two engine fixes on the way: a
  subworkflow's run has an id derived from the parent's execution (`SubRunID`), and one that parks at a request is
  cancelled with its request and fails the step (it used to wait forever, its request open in the inbox); the safe point
  before an agent turn is recorded under the run's own ref (`refs/drydock/safe/<task>/<run>/<n>`), so a restart, a fork or a
  replay of a task at the same step and attempt no longer moves the safe point an earlier run's execution names. (2) **RPC
  and the run page** (`internal/core/ext_runcontrol.go`; `ui/app/screens/run`): `run.resume {taskId, runId}`,
  `run.timeTravel {taskId, runId, checkpointId}`, `run.replay {taskId, runId, checkpointId?}`, gated like `task.start`. For a
  task: one live run (a fork or a replay cancels the task's run when it waits for a person or is paused, and is refused
  while it works); a task that has ended is not run again (its state machine never leaves COMPLETED, CANCELLED or FAILED);
  the task's status goes back to what it was at the checkpoint (read from its transition events) as far as the state machine
  and its guards allow, through IN_PROGRESS as a resync, which uses no iteration (a note says what could not be put back);
  the run that goes on becomes the task's run; the task channel says what the person did. The task's own records
  (iterations used, evidence, reviews) and the worktree's files stay as they are. `run.explain` gains `actions` (which of the
  three are possible and why not, the run a fork would stop, who drives the run), `origin`, each checkpoint's `next` nodes
  and `resumed` mark, and `subruns`. The page: Resume and Replay from the start under the summary, Time travel from here and
  Replay from here on a checkpoint of the timeline, each behind a confirmation that says what runs first and again (from
  the run's own executions), which steps ask first in a replay, which run is stopped first and that the files are not put
  back; a forked or replayed run links the run it came from; "Driven by" is the lease holder indicator of `ui.md` 6.2.
  (3) **Nested subworkflow view**: a subworkflow node's panel shows its nested run and "Show its nodes in the graph", which
  draws its nodes after it in a frame (nested ones inside), with their states from the nested run and their own panels. No
  migration (00161 was not needed: the origin is in the checkpoint state, the resume is an `entry` checkpoint, the nested run
  is found by its id, the effect is graph metadata), no new dependency. Tests: the engine's (a failed run resumed from its last
  good checkpoint, a child process killed mid-turn taken over once and not while its lease holds, a waiting or cancelled run
  refused, a rejection kept; a fork that keeps the original run byte-identical and asks its request afresh with its inbox; a
  replay that refuses a push without approval, ends on a rejection and runs it once on an approval; the approval just before
  a merge counting; nested run ids, a parked subworkflow cancelled, a marked step inside a replayed subworkflow not run), the
  core's (time travel and replay of a task run through the RPC methods to the approval and the merge with the earlier run's
  history unchanged, a resumed failed run keeping its rejected budget pause, the nested run in the explanation), the
  pipeline's (every merge, push and task creation marked), the git package's (two runs' safe points), UI unit tests and the
  run page e2e spec. **Not built:** putting the worktree's files back on time travel (the engine never resets a worktree;
  the safe point restore stays a human action the page does not offer yet); a subworkflow that waits for a person; running
  an ended task again. **Not verified:** any of it on Windows; the nested view with a shipped workflow (none uses a
  subworkflow).
- **P120. Local models: one model loaded or unloaded beside the others, the embedding server ranks pack searches, MTP and
  EAGLE-3 drafts (2026-10-29).** On the owner's word, answering the list of what is still not built: "Yes" to building the
  whole group that can be built now (`docs/local-models.md` section 10 and P111's not-built list). (1) **One model in or out
  while the others run**: while the router runs, a change to the models kept loaded is made on it with llama.cpp's own calls
  (`POST /models/unload`, the preset written and read again with `GET /models?reload=1`, `POST /models/load`, then the wait for
  the listing to say `loaded`), as `tools/server/README.md` and `server-models.cpp` at b11936 and the TurboQuant fork's README
  at b10298-2.0.0 describe them; the process, its key and the models kept stay; the router is started with `--models-max 4`
  and stays one with one model left. A server of one model, a changed setting of the router's command line or a kept model
  whose draft or projector changed starts the server again as before; a server that does not answer the calls as documented
  is started again too and says so (the server's `note`, the log). The labelled stand-in answers the calls as
  `server-models.cpp` does (the least recently loaded model unloaded past `--models-max`, `--no-router-calls` for a build
  without them). (2) **The embedding server's use**: ranking a pack search by meaning, opt-in (`rankPacks`, off by default),
  local only: BM25 finds three times the passages asked for (at most 30), the embedding server embeds the query and each
  passage's heading and first 1,200 bytes in one call, the passages are ordered by cosine similarity and the first k kept; with
  it off, no embedding model served or a failed call, the BM25 order stands. No vector is stored: no index. Chosen over
  re-ranking memory search because pack search is where questions and documents use different words (an agent's "publish a
  worker" against the docs' "deploy") and BM25 already gives a bounded candidate set with today's order as the fallback;
  memory entries are short and in the project's own words. (3) **Draft heads that are not models**, both supported by both
  builds Drydock installs (read in their `common/arg.cpp`, `common/speculative.cpp` and `src/llama-arch.cpp`): a model whose
  GGUF names MTP layers and lists the last block's `nextn.eh_proj` tensor (any file of a split model) offers "Its own MTP heads"
  as its draft (`drafts: {id: "mtp"}`) and runs with `--spec-type draft-mtp`; an EAGLE-3 draft (architecture `eagle3`) is
  offered to the model with its tokenizer and, when the file says it, its embedding length, runs with `--model-draft` and
  `--spec-type draft-eagle3`, and is never served for chat. The Models page marks both ("MTP", "EAGLE-3 draft", "with MTP",
  "with EAGLE-3"). No migration (settings JSON and the GGUF header), no new dependency, no new RPC method (a `rankPacks`
  setting, `local.model.set` with `draft: "mtp"`, the server state's `loading` and `note`, the models' `mtp` and `eagle3`).
  Details: `local-models.md` section 12, `expert-packs.md` "As built: ranking by meaning". **Not built:** DFlash and DSpark
  drafts and an MTP head shipped as a file of its own; the first Load too on a server of one model still starts it again (it
  has no router); the MTP context's memory in the estimate; an embedding index; the provider's catalog keeps a model the router
  no longer lists (as for every provider). **Not verified:** all of it against a real llama.cpp build (the router's calls, a
  reload that keeps a loaded model, MTP and EAGLE-3 runs, the fork's EAGLE-3 key `extract_layers` against b11936's
  `target_layers`), a real embedding model's order against BM25's (no eval run), Windows.
- **P121. gzip bodies, sending what was recorded before, content export (removed by P126), crash reports and usage
  counters as categories of their own, and B0 with other CLIs and on a cheap provider (2026-10-29).** On the owner's word, answering the list of what is
  still not built: "Yes" to building the whole "can be built now" group (the items of P113's "Not built" line). (1) **gzip**
  (`internal/telemetry/otlp.go`): request bodies are gzip-compressed with `Content-Encoding: gzip`, a setting
  (`telemetry.otlp.compression`: automatic, gzip or none; automatic is gzip for https and none for a collector on this
  computer); the stand-in collector gunzips every request. (2) **What was recorded before** (`backfill.go`, migration `00158`,
  `otlp_sent_at` on `telemetry_spans` and `telemetry_metrics`): each row a collector accepted is marked; a change that turns
  sending on may carry `backfill {sinceMs, maxRows}` (off unless chosen), which queues the rows recorded before that no
  collector has had, since a date within the 30 days telemetry keeps, the newest up to a cap (default 500, at most 1000),
  through the same queue, batching, retries and encoders (so the allowlist is applied again); telemetry must be on, and the
  status keeps what the last one queued and what the cap left out. (3) **Content export** (removed by P126): the rules allow it with consent,
  and say so: brief section 10 ("never includes prompts, code, tool output, or secrets unless explicitly enabled") and
  `packaging.md` section 9 ("unless the user explicitly enables content export, which is a separate opt-in with a warning");
  `security.md` principle 4 forbids an *automatic* upload of source, and this is a person's separate choice to a collector of
  their own. Never secrets: the Wrapper hands content to a tracer only when it asks (`llm.ContentTracer`) and never for a call
  its secret or data-class guard refused; the sink records `gen_ai.system_instructions`, `gen_ai.input.messages` and
  `gen_ai.output.messages` (the GenAI conventions' JSON message shape) with the scanner's redaction applied to every text
  and again to the document (`internal/redact`), each at most 32 KB (cut part by part); every other attribute keeps the
  identifier rule. Off by default; `telemetry.content.set` refuses to turn it on without `acknowledged` (the Settings page's
  dialog says what it sends); turning it off stops recording content and the encoders leave it out of rows still waiting.
  The local rows hold what was sent, as before. Only the Wrapper's own calls carry content: the agent CLIs' sessions never
  pass through it. (4) **Categories** (`internal/core/ext_diagnostics.go`, `diagnostics.status`, `diagnostics.set`): crash
  reports `local` (the default, as before) or `off`, stored with who and when; off pauses this process's crash output at once
  (`crashreport.Pause`, `Resume` when turned back on), the next start removes what a crash left unread
  (`crashreport.Discard`), and a support bundle leaves the kept reports out. Usage counters, off by default: the core counts
  the name of each method the window calls (`Core.Call`, never its parameters), writes one counter point per feature
  (`drydock.feature.uses`, `drydock.feature`) to the local sink every five minutes and at close, and OTLP export sends them
  with the rest; they are recorded under their own switch even while telemetry is off. The Settings page has a Diagnostics
  group and, in Telemetry, the compression dropdown, "Also send what was recorded before" and the content switch.
  (5) **B0** (`internal/trial/b0cli.go`): `B0.CLI` and `B0.Adapter` run the team on any CLI Drydock builds an adapter for from
  its manifest (one new session per turn, the normalized text, tool calls, tool results and usage making the room and the
  record; the ceiling checked against each usage report, there being no budget flag), and `B0.Provider` (base URL, the
  environment variable of its key, optional prices) points Claude Code at an Anthropic-compatible endpoint (its gateway
  variables, every model alias set to the run's model) or OpenCode at an OpenAI-compatible one (an inline configuration);
  other CLIs refuse a provider. With prices the cost is estimated from the tokens (`costSource: estimated`). The record and
  the report name the CLI and the provider; `drydock-trial run -b0-cli -b0-provider -b0-provider-url -b0-provider-key-env
  -b0-price-in -b0-price-out`. Proven against the labelled test doubles only (the scripted ACP agent through the real ACP
  adapter, which gained `ReportEnv`, and the stand-in for Claude Code). New methods `diagnostics.status`, `diagnostics.set`,
  `telemetry.content.set` (removed by P126); migration `00158` (classified with the telemetry tables, which a project export leaves out); no
  new dependency. **Not built:** sending crash reports anywhere (a report still leaves only in a support bundle), content of
  the agent CLIs' own sessions, a provider for CLIs other than Claude Code and OpenCode. **Not verified:** a real collector
  decoding the gzip bodies or the content attributes, Windows (crash output paused and resumed in the desktop app), a real
  B0 run with any CLI or provider: every B0 row of `evals.md` 7.1 stays "not measured", and the run commands are in
  `evals.md` section 15.
- **P122. GitHub and Aikido webhooks, attachments of GitHub and Aikido items, Notion file blocks, OAuth for catalog servers
  without dynamic registration, and the peer check (2026-10-29).** On the owner's words, answering the list of what is still
  not built: "Yes" (the "can be built now" group; `integrations.md` "Not built, unverified" and `mcp.md` "Not built: OAuth for
  catalog entries ..., the Windows peer check (`GetExtendedTcpTable`)"). (1) **Webhooks** on the existing receiver (still off by
  default, 127.0.0.1 only, every delivery a hint whose item is read again through the API): `/hooks/github` checks
  `X-Hub-Signature-256` ("sha256=" + hex HMAC-SHA256 of the raw body, GitHub's documentation), takes `X-GitHub-Delivery` as
  the replay guard and, because GitHub signs no time, the `updated_at` of the issue or comment in the signed body within an
  hour; `issues` and `issue_comment` name `owner/repo#n` (a pull request's comment names nothing), `ping` is answered and
  ignored, and the issue is read with `GET /repos/{owner}/{repo}/issues/{n}` through gh (a new `Fetch` of the GitHub adapter:
  its repository, the assignment's labels, not a pull request). Where the person has no tunnel, GitHub's own forwarder is
  offered: the endpoint lists `gh webhook forward --repo=<repo> --events=issues,issue_comment --url=<receiver> --secret=...`
  for each assigned repository (the gh extension `cli/gh-webhook`, which GitHub documents for testing; Drydock shows the
  command and never runs it). `/hooks/aikido` checks `X-Aikido-Webhook-Signature` (HMAC-SHA256 of the body with the signing
  secret, hex or base64 since Aikido does not say which) and `dispatched_at` within 30 seconds, as Aikido's documentation
  (search-engine extracts of apidocs.aikido.dev; the page could not be opened) says; with no delivery id the replay guard is
  the signed body's hash; an `issue.*` event queues one sync of the connection (Aikido's API reads findings only as one export,
  so a burst of deliveries collapses into one). (2) **Attachments**: a GitHub issue's files are the uploads its body links
  (`github.com/user-attachments/...`, `/<owner>/<repo>/files/...`, `user-images` and `private-user-images.githubusercontent.com`),
  downloadable without any credential through the guarded client (a gh token never goes to a file host; a private
  repository's file, which GitHub serves only to a signed-in browser, is refused in plain words), every other link listed and
  never fetched; an Aikido finding has no files in its API as far as it could be read, so its links (its page, its references)
  are listed and never fetched; a Notion page's file, image, pdf, video and audio blocks, also inside toggles and columns (three
  levels, 25 listings at most, `start_cursor` pages), are listed beside the files properties, and a Notion-hosted one is
  downloaded from a fresh `GET /v1/blocks/{id}` without the credential. Migration 00159 records where an attachment was found
  (`integration_attachments.source`: issue, event, property, page body, issue body, finding), and the item shows it. (3)
  **OAuth for catalog servers**: the sign-in of P22 registered Drydock dynamically, so GitHub's hosted server (no dynamic
  registration; "each host app must configure a GitHub App or OAuth App") was token-only. A library server signing in with
  OAuth can now hold a client the person registered with the provider (`mcplib.oauth.client {id, clientId, clientSecret}`:
  both in the same vault as the tokens, `mcp:<id>:oauth-client`, the secret taught to the scanner; migration 00160 keeps only
  `mcp_library.oauth_client`), used as the SDK's `PreregisteredClient`; changing or removing it, turning OAuth off or moving the
  server signs out. A catalog entry's `auth.oauth_app {register_url, help}` says such an app can be used; the shipped `gh` entry
  has one (token still the default), and `mcplib.connect {catalogId, clientId, clientSecret}` connects it that way and starts
  the sign-in. A provider with neither registration nor a client says what to do. The fake provider (a test double) can start
  without a registration endpoint and with a pre-registered client whose loopback redirect may use any port. (4) **The peer
  check**: there was none on any platform, so it was built for both. `internal/peer.Owner` finds the process of a loopback
  connection's client end: on Windows the TCP table with owning process ids (`GetExtendedTcpTable`, TCP_TABLE_OWNER_PID_CONNECTIONS,
  IPv4 and IPv6, through `iphlpapi.dll` because `golang.org/x/sys/windows` has no wrapper), on Linux `/proc/net/tcp{,6}` and the
  socket's inode under `/proc/<pid>/fd`; the table parsers are built everywhere and tested on Linux with tables laid out as
  `tcpmib.h` documents. `proc.InTree(root, pid)` answers whether a process belongs to a started one: `IsProcessInJob` with the
  Job Object Drydock created for it on Windows (through `kernel32.dll`), the parent chain otherwise. A gateway run started with
  `PeerCheck` answers only connections whose process is in the tree of a process bound to it; the runtime binds every process
  an adapter starts for the session (a headless CLI starts one per turn), the moment it exists and before the adapter talks to
  it; anything else holding the token gets 403 and `mcp.peer_refused` (rate-limited like `mcp.auth_failed`); the answer is kept
  per connection; a platform with no lookup is not checked and says so once (`mcp.peer_unchecked`). Every agent session gets it
  except one in a WSL distro, which connects from inside the distro where neither the table nor the job can follow it. Tests
  whose own process plays the agent bind themselves. No new dependency (`golang.org/x/sys` was already one). Tests:
  `internal/integrations/p122_test.go` (GitHub's and Aikido's deliveries: no secret, a wrong one, a tampered body, stale,
  replayed, ping, a pull request's comment, another repository, a deleted issue, the payload's text never stored, the
  forwarder command without the secret, both encodings of Aikido's signature, one sync for a burst; GitHub's body files with
  no credential sent, the private file refused, links never fetched; Aikido's links; Notion's file blocks across pages and a
  toggle, fetched from a fresh block read), `internal/peer` (both Windows tables, `/proc/net/tcp{,6}`, this process found for
  a real connection), `internal/proc` (the parent walk, a child in its parent's tree and not the reverse),
  `internal/mcp/peercheck_test.go` (a child process of the test bound to the run answered and the test process refused, with
  the real lookups; per connection; never bound; unsupported), `internal/core` (`mcp_oauth_client_test.go`: sign-in through a
  registered client against the fake provider without registration, nothing secret in any table, event or log, removal and
  OAuth off forgetting it, the gallery connect; `mcp_peercheck_test.go`: a spawned session refuses the test process), the
  runtime's binding test, UI unit tests (`p122.test.browser.tsx`, the gallery's) and the e2e specs integrations, round9 and
  smoke. **Not verified:** any vendor live (every shape is a fixture written from the sources named; Aikido's least of all,
  from search extracts); `gh webhook forward` itself; GitHub's hosted MCP server's OAuth (that its metadata leads to
  github.com's authorization server with PKCE and accepts a loopback redirect on any port); everything on Windows: the TCP table
  call and IsProcessInJob were cross-compiled and vetted (`GOOS=windows go vet`), never run.
- **P124. Group discussions held to their workflow's budgets, the Design Review template, a subworkflow that waits for a
  person, putting a worktree's files back, and running an ended task again (2026-10-29).** On the owner's words after the
  "can be built now" group (P117 to P123) was built: "ok keep going", answering the "Not built" lines P118 and P123 wrote
  (`architecture.md` section 5 "Turn-taking and budgets" and "Run page actions", `token-control.md`, `workflow-engine.md`
  section 6 and s14, `room-protocol.md` s6, `ui.md` 6.2). (1) **Group-discussion turns and workflow budgets**
  ([`token-control.md`](token-control.md), [`workflow-engine.md`](workflow-engine.md) s14 "As built (decision P124)"): a turn
  of a group node names the workflow run that holds it on its session (`rt.Config.WorkflowRun`: `agent_runs.workflow_run_id`,
  migration `00161`, classified in `internal/projectio`, and `model_usage.run_id`), so the `run` and `workflow` scopes count it
  wherever the discussion is held (a channel has no task, so the task's window could not find it); the run and workflow scopes
  count a session that names its run toward that run alone. Each turn is checked (`responder.BudgetScope`, the core's
  `EconomicsBudgetBlockFor`) and reserved (P112, `ReplyTurn.RunID`/`Workflow`, the router's budget state read for the run)
  against them too; one at its limit parks the node on the budget pause (`budget.PauseError`, a `workflow.GateError` with the
  pause's payload: the Inbox's budget item, answered by `budget.resume` or `run.respond`) instead of failing it. The Enforcer
  wraps group nodes (no discussion starts over a limit; warnings at 80 % and crossings after it), and a person's approval lets
  one more turn through, after which the budgets are checked again (`budget.WithoutApproval`); the node run again continues
  the same discussion (its topic is keyed by the message that reached the node, not the step). (2) **A shipped template that
  uses the group node**: `design-review` ("Design Review", `internal/pipeline/design.go`), brief s4's bounded debate before
  implementation: prepare, then a discussion of at most 3 rounds between the architect (required role), the worker and the
  reviewer in the task's channel on the task as written; a DECISION stands as the design, a discussion that does not agree asks
  the person the question it ended on (`designq`, kind `question`, phase `design`; the Inbox shows that request in place of the
  room's QUESTION it carries, `payload.questionId`), and the answer (or an approval to build on what was said) is the design; a
  rejection cancels the task. The design is the task's `design` evidence, which every implementation pass carries as design
  notes; then the common tail. `TemplateDeps.Discuss` and `TaskRoom` connect it to the room's responder
  (`responder.GroupNodeWith`). (3) **A subworkflow that waits for a person** ([`workflow-engine.md`](workflow-engine.md)
  section 6, `internal/workflow/subwait.go`): the nested run records its parent (`workflow_runs.parent_run_id`, migration
  `00162`), is in the parent's project (its requests in the Inbox, under the parent's task) but in no task (so it never splits
  the task's budget window). When it parks, the parent parks on a request that names it (`RequestSpec.Child`, no approvals row
  of its own) and shows its question as its own ("Waiting on <subworkflow>'s question: ...", kind and payload of the nested
  request); an answer to the parent's request goes to the nested run's; when the nested run ends the parent goes on with its
  result, or fails with the step's inbox kept so a resume runs it again with a new nested run. Cancelling the parent cancels the
  nested run. Restarts: nested graphs are registered with their parent; a recovered parent step takes up the nested run an
  earlier attempt started instead of a second one; recovery finds a parent parked on a nested run that ended while nobody could
  wake it. Inside a replay, a nested step that changes something outside the run now asks in the nested run, the replay
  waiting. (4) **Putting a worktree's files back** (`internal/core/runrestore.go`, [`ui.md`](ui.md) 6.2): a person-only action
  on the run page, `run.restorePreview` (the files that change: restored, changed, removed) then `run.restore` to one of the
  task's safe points (`git.IsSafePointRef`), refused while the task's run works or an agent session of the task is working,
  the current state kept under `refs/drydock/safe/<task>/undo/<ms>` (`git.UndoRef`, `Repo.RestoreSafePointTo`; restoring it
  undoes the restore), with `task.files_restored`, an audit row `worktree.restore` and a line in the task channel. Time travel
  takes `restoreFiles`: the files go back, before the new run starts, to the checkpoint's safe point, the one taken before the
  first agent turn after it (`run.explain` names it per checkpoint; none when no turn ran after it, and then it is refused with
  nothing changed). (5) **Running an ended task again**: decided from the docs. The task state machine never leaves
  COMPLETED, FAILED or CANCELLED (brief 12, `state-machines.md`), a failed task is a dead letter kept for the evals, and the
  evals count cost per task (an accepted task's cost must stay that task's), so reopening the ended task is ruled out; "again"
  is a new task with the same work (title, description, criteria, files, labels, priority, template, worker and reviewer;
  `tasks.AgainOrigin`, origin `again:<task>`, `Task.AgainOf`) that starts its workflow from the first node in a fresh worktree
  from the base branch, the ended task, its runs and records kept as they ended. Only a failed or cancelled task is run again
  (`task.runAgain`, person-only): a completed task's work was merged, and more work on it is a new task. Dependencies and an
  issue reference are not copied (the first were met when it ran; the second would link the vendor item twice). UI: Put
  files back... (safe points named by the turn they were taken before, the files listed, then the undo), the time travel box
  "Also put the worktree's files back to this point", Run again as a new task. New RPC methods `run.restorePreview`,
  `run.restore`, `task.runAgain` (gated like `task.start` on the license, in `ext_economics_test.go`'s list); no new
  dependency. Tests: the budget package's (a session that names its run counts toward it alone), the responder's against the
  real budget store and Enforcer (a workflow budget pauses the third turn of a channel discussion, one approval one turn in the
  same thread, a rejection ends the run; a budget already over holds the node before its topic), the pipeline's Design Review
  tests against the scripted stand-in (agreed, asked and answered, rejected; the graph and the catalog), the core's (the shipped
  template held to a workflow budget through the RPC methods; a safe point put back, refused while busy and for a foreign ref,
  undone, audited; time travel with the files put back, refused without a safe point; a cancelled task run again as a new task,
  twice), the engine's (`subwait_test.go`: answer through the parent and directly, cancel, a cancelled nested run resumed into a
  new one, both waits across a restart, recovery of a parent whose nested run ended elsewhere, a recovered parent waiting for its
  running nested run; the nested replay gate), the git package's (the change list, the restore and its undo, the ref check), the
  tasks package's (the origin), UI unit tests and the run page e2e spec (`round11.spec.ts`, the restore and its undo, Run again
  to the new task's approval). **Not verified:** any of it with a real agent CLI (the scripted stand-in only) or on Windows; the
  Design Review template's value (whether a real discussion between agents improves the design or what a task costs, no eval
  run). **Not built:** ending a discussion on `SHIP`; a discussion node in the other templates; a shipped workflow that uses a
  subworkflow (the waiting is tested with test graphs only); restoring an undo ref of an earlier restore from the page after the
  dialog is closed (the ref is in the event, the audit row and the task channel).
- **P125. Review and role prompts cut to the package ceiling, a turn that asks the outgoing agent for a handoff, a status for
  each acceptance criterion, and no tokenizer (2026-10-29).** On the owner's words, "ok keep going", answering the "Not built"
  lines P119 wrote (`token-control.md` "As built: package ceiling, file ranges and verified handoffs": "cutting review and role
  turn prompts to the ceiling (measured only); a turn that asks the outgoing agent for a handoff ...; a per-criterion acceptance
  status ...; a tokenizer"). (1) **Role prompts under the ceiling** (`internal/pipeline/prompts.go`, `review.go`, `plan.go`):
  the reviewer's, security reviewer's, spec writer's, architect's, test writer's and planner's prompts are package pieces,
  assembled under the project's ceiling like a work turn's (`Deps.rolePackage`; the planner's manifest has no task), in the fixed
  order of `ctxeng.CutOrder`. Never cut: the role's instructions and the shape of its answer, the task with its acceptance
  criteria or the definition of done that stands in for them, and the diff under review (`ctxeng.Piece.Keep`, new: a kind that
  may be cut elsewhere that this package never cuts; `diffSection` still bounds it at 48 KB). Cut, with a pointer: the evidence
  list, the accepted spec, a previous spec (before its feedback), feedback, the planner's requirements. A prompt that fits is
  the same bytes as before; a package whose uncut pieces are over the ceiling is sent as it is and says `OVER THE CEILING`. A
  reviewer of criteria with ids is asked to name the id of one it finds not met. (2) **The handoff turn**
  (`internal/pipeline/handoffturn.go`): when a work turn replaces a session for context pressure, an escalation or a model
  change, Drydock first resumes the outgoing session for a read-only turn that asks only for a handoff with the `handoff` tool,
  inside the same node execution, with a budget of 4,000 new tokens (not read from the cache) and 2 minutes; it is not asked
  after a crash or a watchdog stop (section 3 of `token-control.md` says Drydock writes those), when another agent takes over,
  when the session could not be resumed, when the CLI cannot resume or the agent has no handoff tool, or when the session has
  less than 4,000 tokens of its window left (`over_budget`). What it writes is checked by `VerifyAgentHandoff` as before; when it
  is not asked, does not answer in time, ends without a handoff, answers badly or goes over its budget, the fresh session gets
  Drydock's handoff, and the outcome and reason are in the work node's output, Drydock's artifact (`handoffRequest`), the manifest
  and the `agent.session` event. The turn's cost goes on the task. This replaces P39's "no extra paid turn" for those three
  triggers. (3) **A status for each criterion** (`pipeline.CriteriaStatus`, `tasks.Service.MarkCriterion`, migration `00163`
  `acceptance_marks`, classified in `internal/projectio`; `task.criterion.mark`, a new RPC method gated as `approve` except for
  not met and reopen): open, met, not met or waived, with what showed it. Decided from the docs: a check confirms met or shows
  not met (the test names that carry the criterion's id, P97's `AcceptanceState`); a review only shows not met (a REVISE that
  names the id; a reviewer is an agent and a SHIP accepts nothing); a person confirms, says not met, waives with a reason or
  reopens, as the core's person (only a person accepts a task or waives CI or a verdict); an agent only claims a criterion met,
  with evidence, through the handoff tool's new `criteria`, a claim that is shown and never sets the status. A waiver holds until
  the person changes it; otherwise the newest fact counts; a mark binds only the text it was made on. The handoff carries it
  (`criteria`, `criteriaSummary`), an agent's handoff that claims a criterion met that something says is not fails its check,
  and the task page shows each status, what showed it, a claim, and a menu for the person's word. (4) **No tokenizer**: the
  pure-Go ones (`github.com/tiktoken-go/tokenizer` v0.10.0, `github.com/pkoukk/tiktoken-go` v0.1.8, `github.com/sugarme/tokenizer`
  v0.3.0, read from the Go module proxy) count OpenAI's encodings or Hugging Face files, not the Claude models most turns run on
  (Anthropic publishes no tokenizer for them, only a counting API call that would send the package again), so one would trade an
  estimate for another estimate and a dependency; chars/4 stays, to be looked at again when an eval run shows it matters or for
  local models. Migration 00164 was not needed; no new dependency. Tests: `internal/pipeline` (`rolecut_internal_test.go` per
  prompt kind, `p125_test.go` with the scripted Claude stand-in: the review turn under a 4,000-token ceiling, which sessions are
  asked, a turn that does not answer in time, one over its budget, one without a handoff; `acceptance_test.go`),
  `internal/tasks` (`criteria_test.go`), `internal/core` (`p125_test.go`: the stand-in calling the real gateway's handoff tool,
  a good handoff used, a bad one rejected, no answer, a session without room not asked, a claim on the task page, an unknown
  criterion refused by the tool, a person's marks; the method list), `internal/projectio`, UI unit tests (`criteria.test.browser.tsx`,
  `task-page.test.ts`, `client.test.ts`, `task-detail-screen.test.browser.tsx`) and the e2e specs `task-page` and `smoke`.
  **Not verified:** a real agent CLI answering the handoff turn or writing a handoff (only the stand-in); whether another CLI's
  read-only mode lets the `handoff` tool through (a refused call means Drydock's handoff); the budget's reading of a CLI's usage
  other than Claude Code's; whether any of it lowers what a task costs (no eval run); Windows. **Not built:** a review finding
  for a criterion written without an id, a check that confirms a criterion other than through test names, asking the outgoing
  agent after another agent takes over.
- **P126. Content export removed (2026-10-29).** The owner was told what P121's content export does (a separate opt-in,
  off by default, that adds the redacted prompts and completions of Drydock's own model calls, consult answers, digest
  summaries and the routing tiebreak, as the OpenTelemetry GenAI content attributes of the spans sent to the person's own
  OTLP collector) and that nothing depends on it, and answered: "remove it". Removed: `telemetry.content.set` and the
  `telemetry.content_changed` event; the switch in the sink (`SetContent`, `ContentEnabled`, the `content` field of
  `telemetry.status`, `diagnostics.status` and the OTLP status); `llm.ContentTracer`, `llm.CallContent`, `CallTrace.Content`
  and where the Wrapper filled it; the `gen_ai.system_instructions`, `gen_ai.input.messages` and `gen_ai.output.messages`
  keys, `SanitizeContent` and their encoding in both OTLP encodings; the Settings switch and its dialog
  (`telemetry-content.tsx`), the client call and types, the Guide's paragraph, and their tests. Why: the benefit was narrow
  (only Drydock's own model calls were ever included, never an agent CLI's session, where nearly all the work and tokens
  are), and without it `security.md` principle 4 holds without exception again: telemetry export never carries prompts,
  code, tool output or secrets, whatever a person turns on. No migration: a setting row a person stored for the switch
  (`telemetry.content.*`) is simply ignored (the feature never shipped), migration `00158` keeps its comment, and
  `otlp_sent_at` stays because the backfill uses it; a row an older build stored with content attributes is sent without
  them, since `Sanitize` keeps only allowlisted keys. Stays as P121 built it: gzip bodies, "Also send what was recorded
  before", the crash report and usage counter categories, and B0. Tests: `internal/telemetry/p126_test.go` runs the real
  Wrapper against the protocol fixture with telemetry, OTLP sending in both encodings, the backfill and the old setting all
  on, and finds only allowlisted identifier-shaped attributes, and none of the prompt, system text, tool call, tool output
  or answer, in the stored rows, the preview and every request the collector stand-in decodes (it fails against P121's
  code); the backfill test keeps a row with an old content attribute; `TestTheRemovedContentSwitchIsGoneAndItsSettingIgnored`
  (core); the Settings browser test and `e2e/diagnostics.spec.ts` find no such switch. Docs: `security.md` principle 4,
  `packaging.md` section 9, `token-control.md`, `ui.md` S19, `architecture.md`. No new dependency.
- **P127. Discussions that end on `SHIP` or a decision, the bound and the context plan inside a turn grant, a task's own
  routing level and a template's, undo points on the run page, and where discussions and subworkflows belong (2026-10-30).**
  On the owner's words after P124 to P126 were built: "ok keep going", answering the "Not built" lines P118, P123 and P124
  wrote (`room-protocol.md` s6, `token-control.md` "turn grants", `model-routing.md` "As built: the workspace's level and a
  project's own", `workflow-engine.md` s14, `ui.md` 6.2, `architecture.md` section 5). (1) **Ending on `SHIP` or a decision**
  ([`room-protocol.md`](room-protocol.md) s6 "As built (decision P127)", [`workflow-engine.md`](workflow-engine.md) s14): rule
  7's "the group returns SHIP/a decision", decided from the conventions already in use, not a new one: a turn whose reply
  states a `DECISION` (the `[DECISION]` line the discussion prompt asked for, or a reply that is one, P93) or is exactly `SHIP`
  (the review's single-word verdict, read as `pipeline.ParseVerdict` reads it) ends the discussion at that turn. The result
  says why (`Ended`: `bound`, `passes`, `ship`, `decision`) and whose turn (`EndedBy`), and so do the SYSTEM line in the thread
  and `room.debate_bounded` (`room.EndDiscussion`; `ApplyDebateBound` is its bound case). The resolution stays the bound's: a
  proposed `DECISION` stands as the candidate, else a person is asked; a `SHIP` is an agent's word and decides nothing alone, so
  without a decision written down the question says an agent returned SHIP (in Design Review an approval without text then
  builds on what was said). The prompt tells the agents both endings. (2) **The bound and the plan in the grant**
  ([`room-protocol.md`](room-protocol.md) s6 rules 2 and 4, s11): the scheduler writes `MaxRounds` (the room's bound, or the
  discussion's) and a `ContextPlan` (trigger, task, the agent's roles and previous message, the thread window, the mention and
  size bounds, defaults filled in) into every grant (migration `00164`: `room_turn_grants.max_rounds`, `context_plan`; the
  table was already left out of exports). A granted turn is selected by `room.ContextForGrant` from the grant's plan, nothing
  recomputed; it and `RedeemGrant` refuse a grant whose bound or plan differs from what was stored, or whose round is past its
  bound; a renewed grant carries the used one's plan; a group turn's prompt says the round and bound of its grant. (3) **A
  task's own level and a template's** ([`model-routing.md`](model-routing.md) "As built: a task's own level and a
  template's"): a person sets a task's level on the task page's new Routing row (`route.level.get/set` with `taskId`, an
  event; migration `00165`, `tasks.route_level`, skipped in a project export as the project's own level is); a template may
  declare one (`pipeline.Template.Level`; Security declares Quality, the premium-only class of fixing security findings).
  Order, decided from section 3 and brief s1 ("team templates set defaults"): the task's own, its template's, the project's,
  the workspace's. The template sits over the project because a task chooses its template, which says what kind of work it is;
  the project's level is a default for all its work; a person overrides either for one task. `LevelSource` gains `task` and
  `template` (`LevelTemplate` names it) and "Why this model?" says it; templates are code, so there is no template control. No
  new RPC method: `route.level.get/set` take `taskId`. (4) **A shipped workflow that uses a subworkflow: none, decided**
  ([`workflow-engine.md`](workflow-engine.md) s14): a nested run belongs to no task (P124, so it never splits the task's budget
  window), so an agent turn in one would get no safe point before it and would leave the parent run's and its workflow's
  budgets (undoing P124's workflow budget on the Design Review discussion); the candidates (the design discussion as a nested
  graph, a fix-then-verify child) run agent turns and none is shared by two templates, so nesting would buy only a frame in the
  graph. A shipped subworkflow waits for a nested run that keeps its parent's task. `TestNoShippedTemplateRunsASubworkflow`
  holds it. (5) **Undo points on the run page** ([`ui.md`](ui.md) 6.2 "As built (decision P127)"): Put files back... lists
  the undo points earlier restores kept (`run.explain` `undoPoints`, `git.Repo.UndoPoints`: newest first, each with when its
  restore was made and what it put back), each restored like a safe point (the files first, person-only, refused while a turn
  runs, keeping an undo point of its own), whenever the person opens the dialog again. (6) **A discussion node in the other
  templates: none, decided per template** ([`workflow-engine.md`](workflow-engine.md) s14): brief s22 gives Feature, Bug,
  Security, Full Stack SaaS and Iterate Until Verified their gates (a person's spec gate and approval, tests first, the scan,
  one independent reviewer) and no discussion, and s7 makes each agent turn a cost to justify; a person picks Design Review for
  a task that needs one, until an eval run shows a discussion pays. No new dependency. Tests: `internal/agents/responder`
  (`group_test.go`: ending on a decision, on SHIP, on a round of passes and at the bound against the scripted stand-in through
  the engine; a group turn seeing what its grant's plan says; `IsShip`), `internal/room` (a grant's plan decides the selection,
  five changed grants refused at the selection and the redemption, a used one, a renewed one's plan, a round past its bound),
  `internal/agents/runtime` (a widened plan and a raised bound refused at `Spawn`), `internal/pipeline` (the Design Review
  discussion ending on its DECISION; no subworkflow, discussions in Design Review only), `internal/router` (the task and template
  lines, stored and loaded), `internal/core` (`TestATasksOwnLevelAndItsTemplatesRouteItsTurns`; the Design Review budget test
  now ending on the architect's round-2 decision; the restore test with the undo points), the git package's restore test,
  `internal/projectio`, UI unit tests (`routing.test.ts`, `task-level.test.browser.tsx`, `run-model.test.ts`,
  `run-screen.test.browser.tsx`) and the e2e specs `routing-level`, `round11`, `task-page` and `smoke`. **Not verified:** any of
  it with a real agent CLI (the scripted stand-in only) or on Windows; whether ending on the first decision makes designs better
  or worse than a round of passes (no eval run). **Not built:** the scheduler's `Complete` call; a subworkflow in a shipped
  workflow (see 4).
- **P128. Drydock's own occupancy estimate, a fresh session after two corrections, the outgoing agent asked after a takeover,
  criteria a named check confirms, findings about how criteria are written, and a subscription's warning downshifting premium
  use (2026-10-30).** On the owner's words, after P124 to P126 were built and pushed: "ok keep going", answering the "Not built"
  lines P39, P119 and P125 wrote and two more (P39 (5): "Drydock's own occupancy estimate is not built"; P39 "Not done:
  restarting a session after two corrections"; P125: "a review finding for a criterion written without an id, a check that
  confirms a criterion other than through test names, asking the outgoing agent after another agent takes over";
  `model-routing.md` s6: "`allowed_warning` downshifts premium use (not built)"). (1) **The estimate**
  (`internal/pipeline/estimate.go`, [`token-control.md`](token-control.md) "As built: occupancy estimate, two corrections, the
  outgoing agent, criteria by check"): after a work turn whose CLI reported no occupancy, the work node's output carries
  `contextEstimate`: the packages Drydock sent into the session (chars/4, instruction files counted each turn) plus what came
  back, the larger of what the runtime saw (`TurnResult.SeenBytes`, new: messages, tool calls and tool results) and the output
  tokens reported, with a 25 % margin and 10,000 tokens for the CLI's own prompt and tools, against the catalog's context length
  of the model (`Deps.ContextWindow`, `Core.modelWindow`: the agent's provider, else the smallest any provider lists) or 128,000.
  The session continues only below **50 %**, chosen so that an estimate 40 % under the real occupancy still hands off before the
  real one reaches 70 %; the decision (`estimated`, threshold 50), its reason (and so the manifest line) and the `agent.session`
  event say it is an estimate; a reported figure always wins. (2) **Two corrections**: a correction is a work turn sent back by a
  failing check, a reviewer's REVISE or a person's Request changes (`pipeline.Corrected`; guidance, a merge conflict and a
  watchdog notice are not), counted per session in the work output (`corrections`); after the second, the next work turn is a
  fresh session with a handoff, reason `corrections` (after context pressure in the order of reasons), and the outgoing session
  is asked for its handoff as for context pressure. (3) **The outgoing agent after a takeover**: the P125 handoff turn runs on the
  outgoing agent with its own work config, its session and model, the same budgets, before the new agent starts (not when it
  no longer runs the CLI its session was on); `HandoffRequest.agent` names it; Drydock's handoff on any failure, as before. For
  every trigger, an agent whose own handoff from its turn already passes Drydock's checks is not asked ("nothing is missing"),
  which the brief described as the existing behaviour and P125 had not built. (4) **Criteria by check**: a criterion naming
  `[check: <name>]` or `(check: <name>)` is confirmed met or shown not met by the newest result with a verdict of that check
  kind (`CriterionStatus.check`), beside the test-name trace; any other evidence never confirms, and a name the project does not
  run leaves it open. (5) **Findings**: after each review, `pipeline.AcceptanceFindings` records, once per task, `review_finding`
  evidence without a verdict and a `task.review_finding` event for a criterion without an id (severity low, on the security scan's
  scale; it suggests the next free id) and one naming a check the project does not run (medium; the checks Verify detects,
  secrets and deps when on, e2e and preview when set up, and kinds the task already has); neither changes the verdict or blocks the
  criteria that have ids. (6) **`allowed_warning`** ([`model-routing.md`](model-routing.md) "As built: a subscription's warning
  downshifts premium use"): `agentlimits` keeps a warning per CLI and limit type until the reset or the next report of that limit
  (also from the ACP bridge's shape, unverified), the agent's candidates carry it (`Candidate.QuotaWarning`), and
  `router.QuotaDownshift` makes the cheap tier the first choice (`quota-warning-downshift`) unless the level is Maximum Quality,
  the task is risky, a person pinned the turn or a rule chose the tier; a rule requiring premium (premium-only classes, an
  escalation) still keeps it premium; "Why this model?" says which (`DecisionFacts.QuotaWarning`, `QuotaDownshift`). The
  labelled stand-in gained `Script.NoOccupancy` (a CLI that reports no occupancy). No migration (00166 and 00167 were not
  needed), no new RPC method, no new dependency. Tests: `internal/pipeline/p128_test.go` (the review findings and a lint-named
  criterion end to end; named checks; the takeover asked, answered and seeding the new agent; not asked when its handoff passes;
  asked and silent; the estimate's arithmetic, a session continued on it and one handed off with a small window; two corrections
  then a fresh session; what counts as a correction), `p125_test.go`'s table, `internal/agents/runtime` (`SeenBytes`),
  `internal/router/quotawarning_test.go`, `internal/core/p128_test.go` (the warning's life, its wiring to the candidates, the
  catalog window). **Not verified:** the estimate's margins against a real CLI's own figure, a real outgoing agent of another
  CLI answering, a real subscription's `allowed_warning`, whether any of it lowers what a task costs (no eval run), Windows. The
  task page lists a finding in its evidence ("Review finding", `screens/tasks/tasks.ts`, its unit test). **Not built:** a finding
  shown beside its criterion, and the check a criterion names shown on the task page.
- **P129. Local models: DFlash, DSpark and MTP head drafts, the MTP context's memory, a catalog that follows the server; Settings >
  About, three shortcuts, Copy that says when it cannot, and the task page's criteria with their check and findings
  (2026-10-30).** On the owner's words after P124 to P128: "ok keep going", answering the "Not built" lines of
  `local-models.md` section 10 (DFlash and DSpark drafts, an MTP head shipped as a file of its own, the MTP context's memory,
  "the provider's catalog keeps a model the router no longer lists"), `ui.md` (S19: "the app version, a licenses link and a
  keyboard map on this page"; section 14's shortcuts; the Connections "a Copy button for install commands") and P128's ("a
  finding shown beside its criterion, and the check a criterion names shown on the task page"). (1) **Drafts**
  ([`local-models.md`](local-models.md) section 13), read in both builds' sources at b11936 and b10298-2.0.0 (`common/arg.cpp`,
  `common/speculative.cpp`, `docs/speculative.md`, `src/llama-arch.cpp`, the converter), never guessed: a file of architecture
  `dflash` is a DFlash draft, one listing `markov_w1.weight` a DSpark draft, each offered to a model with its tokenizer whose
  layers reach `dflash.target_layers` and run with `--model-draft` and `--spec-type draft-dflash` or `draft-dspark`; a file of a
  model's MTP heads alone (what `convert_hf_to_gguf.py --mtp` writes: the model's architecture, the MTP tensor, no tensor of the
  main blocks) is an MTP head, offered to a model of its architecture, embedding length and main layers and run with
  `--model-draft <file> --spec-type draft-mtp` (the fork's `--mtp-head` is another name for `--model-draft`; the official build
  has no `--mtp-head`). `--spec-draft-n-max` and `-fa on` stay the person's: the builds clamp the draft size to the trained block,
  default flash attention to `auto`, and refuse a DSpark draft only for a `--spec-draft-p-min` above its default of 0. Each such
  file only drafts: never served for chat (refused by the core), no Load too, marked on the Models page ("DFlash draft",
  "DSpark draft", "MTP head"; `draftKind`). (2) **The MTP context's memory**: llama.cpp keeps a model's MTP layers out of its own
  cache and gives them to an MTP context as long as the model's, so `EstimateKV` counts the main layers and `EstimateMTPKV` the MTP
  ones; a model drafting with its heads adds that context to `loadBytes` (`mtpKvBytes`), its fit and the served memory, and a head
  file's cache is its context's. It also fixes the served memory of a model drafting with its own heads, which counted the size
  of the first model listed. A projector's memory beyond its file is left out on purpose: it depends on the image and the batch,
  which no GGUF number gives. (3) **The catalog**: after each probe of the local provider that the server answered, the rows of
  models its listing no longer has are removed (`providers.Registry.DropUnlisted`), only for that provider, whose list is
  Drydock's own; a turn pinned to an unloaded model finds no candidate. (4) **Settings > About** (`screens/settings/about.tsx`):
  the version and build (`app.info` now names `core.AppVersion` and `AppBuild`, which the release sets), "Third-party
  licenses" (a new method, `app.licenses`, reads THIRD_PARTY_NOTICES.md from beside the executable, where the installer puts it,
  at most 1 MB of text, and the page draws it with the Markdown renderer; a build from the source tree says it has none beside
  it; no file is opened outside the window, since `shell.openExternal` takes only web and mail addresses), and the keyboard map,
  read-only, from `lib/keys.ts` `KEYMAP`, which lists only shortcuts that exist. (5) **Shortcuts**: `Ctrl+B` folds the sidebar,
  `Ctrl+.` opens the Agent Inspector on an agent's page, `Esc Esc` in a chat's message box stops the agent answering (the first
  Esc says so, the second within 1.5 s stops it); all ignored inside the terminal and Monaco. Not built, each for want of a
  target: `Ctrl+.` in a direct-message room (its room record has no agent id), `Esc Esc` in a room's composer (no running agent
  of its own), `Ctrl+1`-`4` (no region focus model), ``Ctrl+` `` and `Ctrl+Shift+K` (reached otherwise), `Alt+Up/Down`. (6) **Copy**:
  the Agents page has had a Copy button for the install command since P98; it is now named "Copy the command" and says to select
  the command and press Ctrl+C when the window refuses the clipboard. (7) **Criteria**: the task page shows the check a criterion
  names and the review findings about it beside it (the UI's `CriterionStatus` gained `check`; the types are written by hand in
  `api/types.ts`, `internal/tsgen` generates only the AG-UI events). No migration (00168 and 00169 were not needed), no new
  dependency, one new RPC method (`app.licenses`). Tests: `internal/localllm/drafts129_test.go`, `draftheads_test.go`,
  `internal/core/ext_localllm_p129_test.go`, `ext_localllm_inplace_test.go`, `ext_shell_test.go`, `ext_economics_test.go`'s list;
  the UI's `models.test.ts`, `models-screen.test.browser.tsx`, `settings-screen.test.browser.tsx`, `agents-screen.test.browser.tsx`,
  `chats-screen.test.browser.tsx`, `sidebar.test.browser.tsx`, `a11y.test.browser.tsx`, `task-page.test.ts`,
  `criteria.test.browser.tsx`. **Not built:** a projector's memory beyond its file, the TurboQuant build's own MTP assistant files
  (`gemma4_assistant`, the older `qwen35*_mtp`), the shortcuts above, rebinding. **Not verified:** a real DFlash, DSpark or MTP
  head file against either build (fixture GGUF headers and the labelled stand-in only), the MTP context's real memory against
  the estimate, the clipboard in WebView2, Windows. Shown beside its criterion, a finding is one paragraph of the task page, so
  the two finding texts were shortened to keep within the density budget of 200 characters with their label (the check's name
  cut to 20; `TestAFindingBesideItsCriterionKeepsToTheDensityBudget`, found by `e2e/density.spec.ts`).
- **P130. Rooms and the Inbox: the scheduler's `Complete`, five more Inbox items with a rule against duplicates, muting a room,
  join rules and the request to join, `Ctrl+.` in a direct message and `Esc Esc` in a room's composer (2026-10-30).** On the
  owner's words after P124 to P129: "ok keep going", answering the "Not built" lines of `room-protocol.md` s6 ("`Complete` (the
  turn row records the end)", P127's "the scheduler's `Complete` call"), s9 ("items for task `WAITING_HUMAN` as such, agent
  `NEEDS_ATTENTION`/`FAILED`, Connection problems and unread mentions ..., muting a room from the UI"), s10 ("(c) stored join
  rules and the 'request to join' question are not built") and P129's ("`Ctrl+.` in a direct-message room", "`Esc Esc` in a
  room's composer"). (1) **`Complete`** ([`room-protocol.md`](room-protocol.md) s6 "As built (decision P130)"):
  `room.Complete(grant, result)` closes a grant with its turn's outcome (done, passed, empty, failed, stopped, interrupted) and
  reply (migration 00166), publishes `room.turn_completed`, and names the message the next speakers are planned from (the reply
  of a done or passed turn; nothing after an empty, failed, stopped or interrupted one), so the grant used, its round and what
  follows are decided by the scheduler in one place; the responder ends every granted turn through it (`responder.turn`'s
  `end`) and keeps writing the `room_turns` row for what the turn cost. A grant is completed once, its reply must be the agent's
  own message in that room, one completed before its agent started is closed and can no longer start a turn, a renewal closes
  the used grant as `renewed` and can be made once, and open grants are completed as interrupted at start. (2) **Stopping**: a
  new method `room.stop` (`responder.Stop`) cancels the turns running in a room, which complete as `stopped`, post nothing they
  said and one SYSTEM line ("You stopped Ada's answer."), and end their chain. (3) **Inbox items** (s9 "As built (decision
  P130)"): `task_waiting`, `mention` (the oldest 50 unread), `join_request` from the room's tables; `agent` (presence Failed or
  Needs Attention) and `connection` (an agent CLI an agent uses whose newest check says not ready; `connections.Service.Last`
  keeps the newest check of each Connection, so the Inbox starts no CLI and a CLI nothing checked since start has no item) from
  what the core holds. **The rule against duplicates**: one item per cause, the one that says most wins: a task's waiting item
  gives way to any other item of the task, a mention to the blocking question its message already is, an agent's item to the
  watchdog pause that put it there; one Connection item stands for every agent using the CLI. A mention and an agent's item can
  be marked done; a task waiting, a CLI not ready and a request to join cannot. (4) **Muting** (`room.setMuted`, the room's
  "..." menu, Room actions > Mute room): the person's member state `muted` (it existed in the core); a muted room counts only
  messages that mention the person by name, in its unread count, its badge and the Inbox (a role mention does not), and the
  Rooms rail shows it with a struck bell and reads ", muted" with its name. This changes P118-era behaviour ("muted rooms count
  unread but do not notify"): `TestUnreadAndMentionCounts` now expects 0 for a muted room. (5) **Join rules and the request to
  join** (s10, migration 00167, methods `room.joinRules`, `room.joinRule.add`, `room.joinRule.remove`, `room.answerJoin`, the
  `room` tool's action `request_join`): the design left choices open; each was read the safest way and written down there: a
  rule reaches only task rooms created after it, matches the files the task names when its room is created (never the
  worktree), and adds its agent as an observer unless the person chose active; a request names a channel of the agent's own
  project, keeps a redacted reason of at most 300 characters as data, and a person's Let in adds the agent as an observer. (6)
  **Shortcuts**: a DM's room record carries its agent (`Room.agentId`, from the DM's name, which `CreateDM` makes of the human
  and the agent: no migration needed), so `Ctrl+.` opens its Agent Inspector; `Esc Esc` in a room's message box stops the agent
  answering there (the first Esc says "Press Esc again to stop <name>", the second within 1.5 s calls `room.stop`), beside a
  Stop button shown while a turn runs (`room.turns`); both ignored inside the terminal and Monaco, like P129's. No new
  dependency. Tests: `internal/room/p130_test.go` (Complete, renewal and interruption, muted counts, task and mention items
  and their duplicates, join rules, requests and the DM's agent), `inbox_test.go`'s muted count,
  `internal/agents/responder/p130_test.go` (every turn completed by the scheduler, Stop ends the turn and its chain),
  `internal/core/p130_test.go` (agent and Connection items, muting and the DM through the core, join rules and `request_join`),
  `ext_economics_test.go`'s list, `internal/projectio` (the new tables deleted with a project, not exported); the UI's
  `roomClient.test.ts`, `room-agents.test.browser.tsx` (join rules), `room-screen.test.browser.tsx` (Esc Esc, Stop, Mute),
  `needs-you.test.ts`, `needs-you-screen.test.browser.tsx` (Let in), `a11y.test.browser.tsx` (`Ctrl+.` in a DM),
  `hubnav.test.browser.tsx` (muted), and `e2e/rooms.spec.ts`; `smoke`, `density`, `inbox`, `round2`, `round3`, `crew` and
  `guide` pass. **Not built:** desktop (Windows toast) notifications (they need an AppUserModelID, `docs/windows-identity.md`),
  a join rule matched again when a task names its files later, a rule for channels. **Not verified:** a real agent CLI calling
  `request_join` through the Workspace MCP server (the core's provider is tested directly), Windows. Found by Windows CI: an adapter
  a caller put in place of the built one (`Config.Adapters`, the e2e core's scripted claude) raises no Connection item, since the
  check reads the installed CLI its agents never start (`TestAgentAndConnectionItemsFollowWhatTheCoreHolds`, winverify's smoke).
- **P131. The security scan as a check and as the agents' tool, and the sandbox for other CLIs (2026-10-30).** On the owner's
  words after P129: "ok keep going", answering the "Not built" lines of `security-scan.md` section 7 ("The scan is not a
  workflow check node yet", "No MCP tool exposes the scan to agents; findings are for the person") and `sandbox.md` section 7
  ("Only Claude Code agents can work in a sandboxed project ... its ACP terminal requests are not routed into the container
  yet", "No port forwarding from the sandbox to the human's machine"). (1) **The `scan` check**
  ([`security-scan.md`](security-scan.md) section 10): the scan's Code engine (Opengrep with the built-in rules and the rules
  folder) and Dependencies engine (osv-scanner) over a task's worktree (`scan.CheckSpec`, a scan of some engines:
  `scan.Options.Engines`), after build, test and lint in Verify when the project turns it on (Verification checks, "Security
  scan of the change"; `checks.Settings.Scan`, migration 00168, off by default, exported with the project). A finding counts
  only when it is the task's: on a line it added, or in a manifest or lockfile it changed; a dismissed one does not. Skipped
  with the reasons without the tools, errored when one broke and none judged, never a pass; git is not asked for the worktree's
  files. The Security template judges the whole tree (its task is what is there), by name or joining its default scans when the
  setting is on; the SaaS template's gate judges the change. (2) **`security_findings`**, a read-only Workspace tool in a new
  column of the role table (no for PM and UX Designer): the project's last finished scan, its findings not dismissed, filtered
  and capped, redacted whole. It cannot start a scan, dismiss or change rules. **No line of code in it**, decided: the scan
  read the person's own checkout, which may hold changes not committed, and that text would go to the agent's model provider;
  the agent reads the source in its worktree, and the file and line find it. (3) **Other CLIs in a sandboxed project**
  ([`sandbox.md`](sandbox.md) section 9): a CLI whose manifest says how it asks before every command it runs itself
  (`transports.acp.sandbox`, with its source: Gemini CLI in `default` mode, Goose with `GOOSE_MODE=approve`, Cline) gets its ACP
  terminal routed into the container (`agents.RunSpec.Terminals`; quoted, in its folder, only the agent's own variables,
  `sandbox_run`'s limits, output streamed, `sandbox.command` with `via: terminal`), the manifest's mode replacing the agent's own
  and required (the session does not start without it; leaving it stops the turn), its permission requests answered by the
  sandbox's rules (commands refused with where to run them, reads and edits inside the worktree allowed but not into `.git`,
  links followed), and an `execute` call it reports as ended without asking and without a terminal of the session stops the
  turn and is recorded (`sandbox.escape`): detection after the fact, said so. Codex and OpenCode declare nothing (their
  defaults run commands without asking) and their turns stay a question for the person, which now names the CLIs that can.
  (4) **Port forwarding: judged and not built here (built in P134)**: a forwarder container on the internal network publishing `127.0.0.1` only is
  buildable, but it is not small (a relay mode of the egress binary and its image, a per-task record, a method, the task page,
  Docker Desktop's loopback publishing on Windows unchecked) and it needs the owner's choice: an agent's page opened in the
  person's browser at `127.0.0.1:<port>` shares the cookies of every local app on 127.0.0.1 (cookies are not separated by
  port), so it would open in a window of Drydock's own with its own storage, or on another loopback address. Migration 00168
  only (00169 was not needed), no new dependency, no new RPC method. Tests: `internal/scan/check_test.go`,
  `internal/pipeline/scan_check_test.go`, `sandbox_internal_test.go`, `internal/mcp/workspace_security_test.go` and the role
  table tests, `internal/agents/acp/sandbox_test.go`, `internal/sandbox/terminal_test.go`, `internal/core/ext_security_check_test.go`,
  `ext_sandbox_acp_internal_test.go`, `sandbox_acp_test.go` (the whole loop with a real container engine, Docker 29.9, and the
  scripted ACP agent as the worker), `internal/projectio`; the UI's `project-screens.test.browser.tsx` and
  `project-sandbox.test.browser.tsx`. **Not built:** port forwarding, Codex and OpenCode in a sandboxed project, an agent starting
  a scan or seeing a finding's lines, a lockfile compared with its base. **Not verified:** a real Opengrep or osv-scanner run of
  the check (labelled stand-ins), Gemini CLI, Goose and Cline in a sandboxed project (the mode ids, whether each uses the
  client's terminal, how often each runs a command without asking), Windows.
- **P132. Shortcuts: four regions, Ctrl+\`, Ctrl+Shift+K, Alt+Up/Down and rebinding; the Agents card's conformance history and
  capability matrix; the TurboQuant build's own MTP files (2026-10-30).** On the owner's words after P129: "ok keep going",
  answering the "Not built" lines of `ui.md` section 14 and S19 ("`Ctrl+1`-`4` (there is no region focus model yet), ``Ctrl+` ``,
  `Ctrl+Shift+K`, `Alt+Up/Down` and rebinding"), its Connections line ("Still not built: conformance history and the capability
  matrix on the card") and `local-models.md` section 10 ("MTP heads of the TurboQuant build's own kinds: a Gemma 4 assistant
  file ... and the fork's older standalone `qwen35*_mtp` files are not offered"). (1) **Regions**: the shell is four regions,
  each marked `data-region` (`shell/regions.ts`): `Ctrl+1` the sidebar (the link of the page open), `Ctrl+2` the page (`#main`,
  the skip link's target), `Ctrl+3` the details panel (its chosen tab, or Show details when it is closed), `Ctrl+4` the dock
  (its chosen tab); a region the page does not have (a workspace page has no panel or dock) leaves the key alone. ``Ctrl+` ``
  opens the dock on its terminal and puts the keyboard there, or closes the dock when it shows the terminal already;
  `Ctrl+Shift+K` opens the Inbox; `Alt+Up` and `Alt+Down` go to the room above or below in the Rooms rail's order (team, task,
  direct), or the chat above or below in the chat list, and leave a dropdown's arrows alone. All are ignored inside the
  terminal and Monaco, like the others (`shortcutOf` in `lib/keys.ts`); `Ctrl+.` in a direct-message room and Esc Esc in a
  room's composer are not this decision's. A key that changes with Shift or is a dead key on some layouts (`` ` `` on a German
  one) is read by its place on the keyboard (`KeyboardEvent.code`), letters by the layout. (2) **Rebinding**: the fourteen
  shortcuts with Ctrl or Alt carry an id in `KEYMAP` (palette, Inbox, sidebar, the four regions, dock, terminal, diff,
  inspector, the two room keys, new chat); Settings > Keyboard shortcuts (`screens/settings/keyboard.tsx`) waits for the new
  keys after Change (Esc cancels), says at once why keys cannot be used and sends nothing then: Ctrl or Alt must be held (a
  plain key would fire while typing), the key one the map can name (a letter, digit, punctuation key, arrow, Home, End, Page
  Up or Down, F1 to F12), nothing the window or the fields keep (`RESERVED`: reload, find, print, developer tools, close, the
  editing keys, Ctrl+Enter), and no other shortcut on the keys, by default or bound. Save sends every binding to two new methods,
  `keys.get` and `keys.set` (`internal/core/ext_keys.go`), which check the same and keep the ones that differ from the defaults
  in the settings row `ui.keybindings` for the workspace, with `keys.changed` for the other windows; Reset and Reset all go back.
  The page and the core each hold the list; `TestTheCoresShortcutsAreTheKeyboardMaps` reads `keys.ts` and fails when they part.
  The About map, the sidebar's search, the dock's hint and New chat show the keys in use. (3) **Detect**: the line's Detect is
  the Agents page's **Check all again**, which reads `connection.list` again and so runs the core's detection of every CLI and
  distro afresh (built with P98); no second button was added. (4) **Conformance history**: every Test connection run is kept in
  a new table, `connection_tests` (migration 00170; 00171 was not needed), the newest 30 of each install: the CLI version, the
  transport, passed, failed and skipped counts, whether the tiny prompt was sent, the first failed step's reason through the
  secret scanner, the time, and the report's capability matrix; no output of the CLI. Neither a project export nor a project's
  deletion touches it (it is this machine's, `projectio`). A new method, `connection.history`, answers an install's runs, newest
  first, and its **capability matrix**: what the adapter declares (its capabilities, with the provenance its manifest gives
  each, and its permission modes), with the cells of the newest run that filled one over it (`connections.CardMatrix`). The
  Test results tab shows "What it can do" (the capability in words, Yes or No, the evidence) and "Past tests" (when, passed or
  not, version, counts, the prompt, the time, the reason); the session's report no longer repeats the matrix beside it. (5) **The
  TurboQuant build's own MTP files**, read in its source at b10298-2.0.0 (`src/llama-arch.cpp`, `src/llama-model.cpp`,
  `src/models/gemma4-assistant.cpp`, `conversion/gemma.py`, `common/arg.cpp`, `common/speculative.cpp`, `MTP.md`, `NEXTN.md`,
  fetched through the proxy) and in the official b11936 (`src/llama-arch.cpp`, `src/llama-model.cpp`, `common/speculative.cpp`,
  `common/arg.cpp`): a **Gemma 4 assistant** is architecture `gemma4-assistant` in both (the converter writes its blocks as MTP
  layers and its model's hidden width as `embedding_length_out`), shares the cache of its model (both builds' `create_memory`),
  and is run with `--model-draft <file> --spec-type draft-mtp` (the fork's `--mtp-head` and `-md` are one flag with
  `--model-draft`, its "mtp" and "nextn" aliases of `draft-mtp` since its b10018 sync). It is offered to a Gemma 4 model whose
  hidden state is as wide (`common_speculative_impl_draft_mtp` refuses another width) with its tokenizer, on either build, since
  both load it; it never chats, has no Load too and keeps no cache of its own in the estimate. The fork's **older layouts** are
  loaded by neither build at these tags: a Gemma 4 assistant converted before b10018 (`gemma4_assistant`, which the fork's
  `llama_model_create` refuses by name: "predates the b10018 sync and is no longer supported") and a standalone Qwen 3.5 NextN
  file (`qwen35_mtp`, `qwen35moe_mtp`: `NEXTN.md` still calls it a fallback, but no `llama-arch.cpp` names the architecture),
  so such a file is marked "Old MTP file" ("no engine here loads it"), offered to nothing and refused for chat with that
  reason. No new dependency; three new RPC methods (`connection.history`, `keys.get`, `keys.set`) and one event
  (`keys.changed`). Tests: `internal/localllm/assistant132_test.go`, `internal/core/ext_localllm_p132_test.go`,
  `ext_connhistory_test.go`, `ext_keys_test.go`, `ext_economics_test.go`'s list, `internal/connections/history_test.go`,
  `internal/projectio` (the table decided for export, deletion and an older archive); the UI's `lib/keys.test.ts`,
  `shell/regions.test.browser.tsx`, `a11y.test.browser.tsx`, `settings/keyboard.test.browser.tsx`, `settings-screen.test.browser.tsx`,
  `agents.test.ts`, `agents-screen.test.browser.tsx`, `models.test.ts`, `models-screen.test.browser.tsx`, `client.test.ts`; the e2e
  `keys.spec.ts` (new) and `wsl.spec.ts` (the history after a reload), with `smoke` and `density`.
  **Not built:** `Ctrl+.` in a direct-message room and Esc Esc in a room's composer (P130's), a binding of a key pressed after
  another (the chords G then I, J and K stay as they are), a binding per person rather than per workspace. **Not verified:** a
  real Gemma 4 assistant file against either build (fixture GGUF headers and the labelled stand-in only) and whether it speeds
  a model up, the keys in WebView2 on Windows (that WebView2 leaves Ctrl+1 to 4, Ctrl+\` and Alt+arrows to the page), a real
  CLI's Test connection history beyond the test double's.
- **P133. A join rule is matched again when a task names its files later (2026-10-30).** On the owner's words "when you are done
  doing the CI, continue on to the next tasks autonomously throughout the night", from the "Not built" line of P130. A task
  names its files twice: when a person creates it, and when a work turn finishes (the pipeline stores the files of the diff,
  `tasks.files`). The rule matched only the first, so a security agent with a rule for `auth/` joined a task that said so at
  creation and missed one that only touched `auth/` while it was worked. Now the pipeline tells the room after it stored the
  files (`Deps.OnFiles`, the core's `joinRulesAgain`), and `Service.ReapplyJoinRules` matches the task's room against the rules
  again: an archived room, a task with no room and an unknown task change nothing and are not errors. The same safe readings as
  P130 hold: a rule never reaches a room made before it, the agent joins as an observer unless the person chose active, and a
  rule acts **once** in a room, judged by the SYSTEM line it wrote (`join_rule` with the rule's id): calling again adds nothing
  and an agent a person took out of the room is not brought back by the next turn (removing a member deletes its row, so the
  line is the only record that the rule had acted). No migration. Tests: `TestAJoinRuleIsMatchedAgainWhenATaskNamesItsFilesLater`
  (core: no files, then files, a task outside the prefix, a second call, a removed agent, an archived room; mutating the
  once-only check fails it), `TestTheRoomIsToldWhenAWorkTurnNamedTheTasksFiles` (pipeline: the hook is called once with the
  files already stored). **Not built:** a rule for channels, desktop notifications. **Not verified:** a real work turn on a
  real CLI naming the files, Windows.
- **P133 (2). A vulnerable package the base already had is not the task's (2026-10-30).** The same night's second item, from
  `security-scan.md` section 7 ("the check does not compare a lockfile with its base"). A task that changed one line of a
  lockfile owned every vulnerable package in it, which it cannot fix: the worker was sent back for them until the iteration limit
  parked the task. `scan.CheckOptions.BaseFile` (the pipeline wires it to the repository at the task's merge base,
  `Repo.ShowFileMax`, 64 MiB) gives the base's copy of each file the counted dependency findings are in; those copies are written
  to a temporary folder (a path that is not local to it is never read) and the dependency engine runs over it with the same
  tool, environment and redaction. A finding is the base's when the file, package and version are the same (the advisory ids are
  left out of the key: the database can move between two runs a minute apart); another version of the package is a new finding
  for the task, as is a file the base does not have. The comparison runs only in the judging-the-change mode and only when a
  dependency finding would count, so a task that touches no lockfile pays nothing. If the base cannot be read or scanned the
  comparison is not made: every finding still counts (never fewer than before) and the output says "not compared with the base".
  Tests: `TestADependencyTheBaseAlreadyHadIsNotTheTasks` and `TestTheBaseNotBeingReadableLeavesEveryFindingCounted` (scan: the
  stand-in osv-scanner got `--follow-tree`, which answers from the go.mod of the folder it scans; mutating the subtraction fails
  the first), `TestAVulnerablePackageTheBaseHadIsNotTheWorkersToFix` and `TestAVulnerablePackageTheWorkerBroughtGoesBackToTheWorker`
  (pipeline: a real git repository, the base read at the merge base, the stand-in tools; without the wiring the first never
  passes). **Not built:** a cache of the tools' results across passes of Verify. **Not verified:** a real osv-scanner over a
  base folder of one lockfile (the stand-in follows go.mod only), a lockfile osv-scanner reads only together with its manifest.
- **P134. Port forwarding from a task's sandbox, shown in a window of Drydock's own (2026-10-31).** P131 judged this and did not
  build it because it needed the owner's choice: an agent's page opened in the person's browser at `127.0.0.1:<port>` shares the
  cookies of every other local app on 127.0.0.1 (cookies are not separated by port). The owner chose, on being asked which of the
  options to take: "go with option 1", a window of Drydock's own with a storage partition of its own. **As built:** (1) a
  forwarder container (the egress program in a new `relay` mode, `egress.Relay`; the same local image, no registry) on a bridge
  network of the task's own, publishing `127.0.0.1` only, attached to the task's internal network, relaying to the workspace's port
  and nothing else; it carries the task's labels and `drydock.forward=<port>`, so the engine is the record and the sandbox's
  removal takes it (no table, no migration). (2) four methods for a person only (`sandbox.forward.list/open/close/view`), audit
  actions `sandbox.forward.opened/closed/viewed` and task events; an agent has no way to open a port or a window; `view` takes
  the address from the engine's record. (3) the viewer: WebView2 takes one profile folder per process and Wails 3 gives a
  window no profile of its own, so the "storage partition of its own" is a second process of `drydock.exe` (`sandbox-view`)
  with no service, no core and a new profile folder deleted when the window closes, accepting only `http://127.0.0.1:<port>/`
  (`internal/sandboxview`); four at most; a child of the app's Job Object. (4) the task page's "Sandbox app" part, shown while the
  task's sandbox runs. The browser harness has no viewer and says so; nothing falls back to a browser. No new dependency (the
  software-versions page is unchanged). Tests: `internal/sandbox/egress/relay_test.go` (both directions, a dead target, the
  connection limit, idle close, the environment); `internal/sandbox/forward_test.go` (**against a real engine, Docker 29.9**: an app
  in an internal-network workspace reached from the host through the forwarder, published on loopback only, the same forward
  returned again, stopped, removed with the sandbox, the limit of five, a missing program said); `internal/core/sandbox_forward_test.go`
  (a real sandbox, the viewer opener as a labelled double: the person only, the address from the engine, the audit and the events);
  `internal/sandboxview` (every address but the loopback port refused, arguments, title, a profile per viewer, leftovers); the UI's
  `sandbox-app.test.browser.tsx` and `client.test.ts`; the release workflow's install-test opens the real window on the Windows
  runner against a local server and checks its title, its profile folder and its removal. **Review fixes, same day (a code review of the first build):** the relay's idle time is the
  connection's, not a direction's (a page that only pushes, an event stream or a long download, was cut at the idle time);
  `Existing` says "no sandbox" only when the engine says there is no such container (a stopped daemon is shown as that); a forwarder
  whose `run` fails after creating its container is removed; the leftover profiles are cleaned after the single-instance gate (a
  second launch used to delete the first instance's live profiles); the window title is made once (it was suffixed twice);
  the app, not the viewer, makes and removes the viewer's profile folder (`sandboxview.Launcher`, with tests for the count and the
  folders), so a slot is free when the viewer's process ends and not when WebView2 lets go of its files; the list is reused for 3 seconds
  per task and dropped by an open or a close; the viewer's asset server answers 403 to every request (`sandboxview.ClosedAssets`),
  which closes Wails' `/wails/runtime` (dialogs, clipboard, opening addresses, quitting) to the page. **Not verified:** Docker
  Desktop's publishing of a loopback port on Windows; that the viewer's WebView2 has no other channel to the app (it has no
  service and the HTTP runtime is refused; Wails 3 beta.34 gives no way to turn off the `wails:` window messages the runtime
  injects into any page, which only move or resize this window; not tested on Windows); the window's navigation away from
  127.0.0.1 (Wails 3 beta.34 has no navigation filter; a link in the page is followed; the window is isolated and nothing of
  Drydock's is there to reach).

- **P135. Docker is the only container engine (2026-10-31).** The owner: "skip Podman and Rancher Desktop altogether, we will
  stick with Docker". The sandbox (P30) and its port forwarding (P134) support Docker only, Docker Desktop on Windows, still
  driven through the `docker` command. Podman and Rancher Desktop are not supported, not tested and not offered: the docs and the
  project's sandbox page name Docker Desktop alone. `DRYDOCK_CONTAINER_CLI` stays, as the path of the `docker` client when it is not
  on the PATH and as the tests' hook for a stand-in client. The WSL probe still leaves out the `rancher-desktop*` and
  `podman-machine*` distros, so a person who has one installed does not see it offered as a place to run an agent. Tests: none
  changed behaviour; `ui/app/screens/project/project-sandbox.test.browser.tsx` still covers the page's engine message.

- **P136. Drydock's Windows application identity, and a started chat turn always announced (2026-10-31).** The owner: "do the
  publish fix now and do what you can for the windows app identity". (a) **AppUserModelID** `Drydock.Drydock`: the app sets it
  on its process before any window, and the per-user installer writes it on the Start menu shortcut, so the pinned and the
  running Drydock are one taskbar button and Windows notifications and jump lists can be built on it later
  (docs/windows-identity.md section 6). The sandbox app window keeps its own. Tests: `app/appid_test.go` (the installer's
  `APPID` equals the app's), `app/appid_windows_test.go` (the process carries it, on the Windows runner), the release
  workflow's install-test (the installed shortcut carries it). Not verified: the taskbar grouping itself. (b) **Chat events
  after a cancelled request:** `chats.Send` published the person's message, the running chat and `chat.turn_started` with the
  caller's request context; a request cancelled after the turn was claimed left the turn running and the page never told it
  started (CI on main, 2026-10-31: `publish chat.turn_started: context canceled`, with the chat-perf e2e's streaming bubble
  never appearing). After the claim, Send uses a context the caller's cancellation does not reach. Test:
  `internal/chats/send_cancel_test.go` (it fails without the change, naming `chat.turn_started`). Whether this was the only
  cause of that e2e failure is not proven: the run's trace could not be read from this environment.

- **P137. An agent is edited in place, in a side panel; agents remember their template (2026-11-01).** The owner: "the ability to
  edit existing agents when we add it to a project. As we don't seem to be able to change the agents when added like what effort
  and model etc." The causes: Edit existed only on the agent's own page; its model and effort fields were disabled until the
  adapter's capabilities had loaded and stayed disabled when the read failed; the editor and the add form had no permission mode.
  (a) **The panel** (`ui/app/screens/team/agent-panel.tsx`, a `Dialog` with `side`) opens from the Crew card's model chip,
  permission chip and "..." menu, the agent page's Edit (the old dialog is gone), `openAgentEditor(agentId)` on any page that
  renders one `<AgentPanelHost />`, and `/team?edit=<agentId>`. It has Runs on (shown, not changed: another CLI is a new agent),
  Model, Effort (segmented), Permissions (segmented: Read only = `plan`, Asks before edits = `default`, Edits freely =
  `acceptEdits`; any other mode the CLI lists under "Other...", the skip-approvals confirmation as before), Skills and tools
  (chips; project skills through `agent.update`, library MCP servers through `mcpserver.grant`), Instructions, This project |
  Everywhere, and "More settings" (name, role, where it runs, best practices, routing, Save as a template, Remove). A save sends
  only the fields that changed and says "Applies from its next turn". Model and effort are never disabled: while the
  capabilities load, or when they cannot be read, the claude adapter's known lists are offered (`agent-edit.ts` `KNOWN_CAPS`), and
  another CLI takes typed text; the same holds in the add form (`ModelEffortFields`), which also gained a permission mode. The
  agent page's hero shows two lines of the instructions with "Show all". (b) **The template link:** `agents.template_id`
  (migration 00171) is set by `agenttemplate.apply` (and so by `project.setup`, which applies templates through it) and by
  `agenttemplate.fromAgent`; deleting a template clears it; it is not exported (templates belong to the workspace). (c)
  **Everywhere** is `agent.update` with `everywhere: true` (one call, so the person confirms one save): the agent is changed, then
  the fields the call set among instructions, model, effort and permission mode are saved to its template and to every other
  agent made from that template, in any project (an agent that refuses the change is named in the answer; the others
  still take it). An agent without a template, or whose template was deleted, is linked to the template of its
  name and CLI when one exists, else saved as a new template (`fromAgent`'s spec) and linked; later "Everywhere" saves then
  update it. Agents still do not follow their template on their own: only an Everywhere save carries a change. This is the
  simplest behaviour that makes the switch mean what it says without silently changing other projects. Tests:
  `internal/core/ext_agentdraft_test.go` (TestEverywhereSavesTheChangeToTheTemplateAndItsOtherAgents),
  `internal/projectio` (the older-schema import), `ui/app/screens/team/agent-edit.test.ts`, `agent-panel.test.browser.tsx`, the
  ported editor tests in `team-screen.test.browser.tsx` and `run-mode.test.browser.tsx`, `ui/e2e/agent-editing.spec.ts` (model,
  effort and permission from the Crew card, kept after a reload; Everywhere saves a template), and `round3`, `round5`, `round14`.

- **P138. Describe a new agent (2026-11-01).** New agent is one dialog with the tabs Describe (the default), Configure (the form)
  and From a template (saved templates and the prebuilt gallery; the header's Templates opens it). Describe takes one sentence
  (or a starter: Builder, Reviewer, Tester, Docs writer, Team lead, Planner), calls **`agent.draft`** `{description,
  projectId?}` and shows the drafted agent as fields (name, CLI, model, effort, permissions, skills and tools, instructions)
  with a one-line "why"; Create agent calls `agent.create` (and `mcpserver.grant` for its tools). Nothing is saved before that.
  The core drafts through the app's own one-shot model path (the economics router picks the cheapest model for a `classify`
  run, the LLM client calls it, as the expert packs' answers and summaries do) with a JSON-only prompt that lists what is
  installed; the answer is clamped: the CLI must be one this PC has (the installed ones, Claude Code first; every registered one
  when none is installed), a model, effort or permission mode the CLI does not list is dropped, a mode that skips approvals is
  never drafted, skills must be accepted skills of the project and tools library MCP servers. With no model reachable, or an
  answer that is not a usable draft, a **rules-based drafter** writes it from keywords (security, review, lead, plan, test, docs,
  frontend, backend give the role and its Claude preset; "never edit", "read only", "just comment" give `plan`; "best model",
  "hardest" the strongest model at high effort; "cheap", "fast" the smallest at low effort), so the feature works offline. The
  description is never logged or put in an event, and the model call is not cached (`NoCache`); the created agent's
  instructions are recorded as any agent's are. Tests: `internal/core/ext_agentdraft_internal_test.go` (a table of
  descriptions for the rules; the model path with a labelled test double for the model call: a good answer, clamping, and the
  fallbacks), `ext_agentdraft_test.go` (the RPC without a model, nothing saved, no event carries the description),
  `ui/app/screens/team/agent-draft.test.ts`, `agent-panel.test.browser.tsx` (Describe, draft, edit, create) and
  `ui/e2e/agent-editing.spec.ts` (a sentence drafted by the rules and created; a starter chip).

- **P139. The calm chat (2026-11-01).** The owner approved the "simple UI" ideas ("simplifying how much text there is,
  simplifying chat"); idea 3 and its mockup are built. (a) **Bursts:** consecutive tool calls between two pieces of the agent's
  text are one line ("Read 2 files, edited 1 file, ran 1 command · 2.2 s"; reasoning before them folds into it as "Thought for
  6 s", measured from the end of the row before, and nothing under a second is timed), opened to today's tool cards; a failed
  call opens its line and its card by itself; a running line names its current step. Grouping is a pure function of the
  message list, worked out again only when the feed replaces its list, so long histories and streaming cost no more (the
  chat-perf budgets hold; 300 calls are one row). (b) **Changes:** the file edits of one turn (from one of the person's messages
  to the next) roll up into one "Changed N files +A −D" card after the turn's last row, its Review changes a dialog of the diffs
  made from the edit calls' inputs (Edit, MultiEdit, Write, ACP old/new text). A chat has no worktree, so the dock's Diff (a
  task's) is not it. (c) **Header:** title, the folder chip (the project's name for a project chat, the folder in its tooltip),
  one warning chip when the CLI is missing, not signed in or the last turn failed (it opens Properties to pick another CLI), and
  ⋯: the CLI and its state, where the chat runs, its skills and servers, Allowed tools (a second view of the same panel),
  Properties, Fork, Move to a project. The state's words stay in the header for screen readers (`chat-status`). (d)
  **Composer:** chips for the agent CLI (Properties), model · effort and permission mode (today's popovers), attach files and
  chats, Stop, Send (an accent button). (e) **Suggested replies:** after the final answer, when its last sentence is an offer or a
  question, up to three chips derived on the page from the words (no model call); "Make it a task" is left out: a chat has no
  path to create a task today. (f) **Empty states** are the composer with the cursor in it, and one line with New chat. (g)
  **Ctrl+O** toggles verbose, kept per viewer in `localStorage` (read and written in try/catch). (h) `ChatConversation` renders
  one chat without the list, for the project Work view to embed. (i) **"Starting..." fixed:** `chat.create` (and a CLI change in
  `chat.update`) checked the CLI's notice with a full `Connections.Check`, which starts the CLI for its version and sign-in
  (prerequisites, version and auth probes, each up to its timeout): seconds on Windows, and as long as the timeouts for a CLI
  that hangs, so New chat sat on "Starting...". It now reads only the gates (`connections.Service.Gates`, no process started); a
  refusal shows one line with "Choose another agent" (the new chat dialog). Tests: `ui/app/screens/chats/chat-groups.test.ts`
  (grouping, changes, replies, verbose), `chats-screen.test.browser.tsx` ("The calm chat (P139)", ChatConversation),
  `internal/connections/detect_test.go` `TestGatesStartNoCLI` (a hanging CLI: answered at once), e2e `ui/e2e/chats.spec.ts` (the
  calm chat against the real core: the line, its cards, the changes card and its review, Ctrl+O across a reload, the replies, a
  failed call open), and the chat specs moved to ⋯ (`chats`, `home-chat`, `knowledge`, `wsl`). Not verified: on Windows.

- **P140. Six places in the sidebar: one Settings and one Agents library (2026-11-01).** The owner approved the "simple UI"
  ideas 6 and 7 ("merging windows together, simplifying how much text there is"). (a) **Sidebar:** the Drydock name with search
  and the fold, **New** (Ctrl+N; on Chats the key keeps making a new chat), **Home**, **Inbox** (with its count), **Chats**, the
  **Projects** (the open one keeps its tabs under it for now), and at the foot **Agents** and **Settings** with the engine and
  the event stream as one quiet line each. Running and My tasks became tabs of the Inbox (Running, and Mine); All crews, Security
  scan, the agent CLIs, Integrations, Models, Agent templates, Skills and the Guide left the sidebar. The project switcher left
  too: the Projects list opens a project, New starts one. What New opens is said in one place (`ui/app/shell/new.ts`, today
  `/project/new`), so the coming start screen takes it over there. (b) **One Settings:** ten sections (General, Agent CLIs,
  Models, Routing & budgets, Integrations, GitHub, Security scan, Backups & data, Shortcuts, Help & about) in a registry
  (`ui/app/shell/settings-sections.ts`) that the Settings rail, its search, the section pills and the tests read. The existing
  screens are rendered inside the frame, unchanged but for their titles and an intro line (the page Agents is titled **Agent
  CLIs**, so only the library is called Agents); every old address opens its screen with its section marked (`/models`,
  `/agents/claude`, `/integrations/linear`, `/connections`, `/providers`, `/mcp`, `/github`, `/data`...), and two sections got
  addresses of their own (`/settings/shortcuts`, `/settings/about`). `/github` is now GitHub's own section instead of a redirect
  to `/integrations/github`, which still shows it as an Integrations row. (c) **One Agents library:** `/library` is My agents
  (the agent templates, as cards: avatar, name, one line of purpose, the model and effort chip, a ⋯ menu with Edit and Delete),
  `/library/starters` the prebuilt agents read-only (Use opens the agent form filled from the starter and saves a new agent of
  mine; the servers it names that are not in the library are left for Integrations), `/library/skills` the skill library, and
  All crews (`/crew`) a pill set apart. "New agent" goes through one function of the library screen (`openNewAgent`), so the
  Describe flow can take it over. (d) The command menu reaches every page that left the sidebar (Running, My tasks, All crews,
  Starter agents, Keyboard shortcuts, Help and about, Agent CLIs, Models, Security scan...). Not done: "in N projects" on an
  agent card (an agent does not record the template it came from; counting by name would guess). Tests:
  `ui/app/shell/settings-sections.test.ts` (the registry, the route map, the search), `sidebar.test.browser.tsx`,
  `hubnav.test.browser.tsx`, `palette-items.test.ts`, `lib/route.test.ts`, `screens/library/*`, `screens/settings/*`; e2e
  `ui/e2e/navigation.spec.ts` (the sidebar's six places, every moved page from Settings and by its old address, the search,
  the Agents tabs, Ctrl+K to Models and Security scan) and the specs whose navigation moved (`e2e/helpers.ts` opens a hub's hub).

- **P141. A project is one workspace: Work · Crew · Settings (2026-11-01).** The owner approved the "simple UI" idea 2 ("merging
  windows together, simplifying how much text there is"). (a) **One tab bar:** a project's pages have one bar, the project's name
  then **Work** (`/`), **Crew** (`/team`) and **Settings** (`/project`), with New task on Work. It replaces the project's five
  sidebar sub-items (Overview, Tasks, Rooms, Crew, Knowledge) and the Overview's pills (Activity, Budgets, Evals report, Security,
  Project settings, Integrations); the sidebar's projects are one line each and open Work. `hubOf` gives each route its tab:
  Work holds the board, a task (and its runs), new task, the pull requests and the rooms (rooms are conversations, so they stay
  beside the work, with the Rooms rail, rather than in Settings); Crew holds `/team` and an agent's page, unchanged; Settings
  holds the project's settings and Integrations and every page that left, as sections of a rail drawn by the workspace Settings'
  own `SettingsRail` from a second registry (`ui/app/shell/project-sections.ts`: General, Integrations, Knowledge with its five
  pages as pills, Plan & milestones, Activity, Budgets, Evals report, Security). Every old address keeps working and marks its
  tab; the old test ids live on (`nav-overview` is Work, `nav-team` Crew, `nav-tasks`, `nav-pulls`, `nav-rooms` are links under
  Work's list, `nav-project`, `nav-knowledge`, `nav-budgets`... are rail sections). (b) **Work** (`screens/work/`): the left
  column is "Search this project", New chat (a chat of this project, opened in place) and one list of the project's tasks and
  chats, grouped Needs you (an Inbox item names it, by urgency; or the task waits for a person), Running (newest first; a task not
  yet started is there with a hollow dot, since it is open work) and Earlier, one line under each title ("Ready to merge · +42
  −3", "Ada · 4 min", "Merged · yesterday", "Chat · Tue"), all from reads the app already had (`task.list`, `needsyou.list`,
  `chat.list` with the project). The centre is the open item, named in the URL as `/?item=task:<id>` or `/?item=chat:<id>` (so
  Reload, Back and a link from another page, such as the coming start screen, open it): a task's own page embedded (the same
  `TaskDetailScreen`, its properties under the work; `/task/:id` still opens it alone) or the chat (`ChatConversation`, P139);
  with nothing named, the first that needs you, else the first running, else one line and Describe a task. The open task is the
  page's task for the shell (dock, details panel, Ctrl+K). The right column is the crew: avatar with its state, name, "Model ·
  effort"; a click opens P137's editor in place (`openAgentEditor`; the one place that says so is `screens/work/edit-agent.ts`,
  whose link is `/team?edit=<id>` for a new window), "+" goes to the Crew tab (the Crew page has no outside opener for New agent
  yet); under it Spent so far, First check, Security and Merged this week, each linking its page. (c) **The Overview page is
  removed**: its needs-you cards, crew cards, in-motion rows and rooms card are the list, the rail and Rooms; its Today and
  Insights numbers are the rail's four lines (the counts of open tasks, working agents and items are the list itself). Not done:
  the mockup's branch and pull-request chip in the tab bar (no cheap read gives "2 open PRs" for the bar), and "Checks passing"
  as one word (the rail shows the first-check rate the core measures instead). Tests: `ui/app/screens/work/work.test.ts`
  (grouping, order, lines, search, the item opened by default), `work-screen.test.browser.tsx` (the list, the centre by URL, the
  crew rail and its editor, the stats, New chat, the empty state), `lib/route.test.ts` (the tabs of every route, `?item=`),
  `shell/hubnav.test.browser.tsx` (the tab bar, the project Settings rail and pills), `sidebar.test.browser.tsx`; e2e
  `ui/e2e/work.spec.ts` (a started task under Needs you and open in the centre, a project chat in the list, Back and Reload, the
  crew rail opening the editor, every old address under its tab) and the specs whose navigation moved (`e2e/helpers.ts`:
  `projectNav` looks for the tab bar, `HUB_OF` maps the old ids to Work, Crew and Settings; a new project now lands on Work).
