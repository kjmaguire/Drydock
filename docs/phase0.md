# Drydock: Phase 0 verification

Date: 2026-10-08. Brief: "AI Engineering Team" build prompt v17. This file is the Phase 0 deliverable and the stop-and-report point.

**Status: Phase 0 is complete for everything this environment can verify. It is not complete overall, and work stops here** for two reasons the brief names explicitly:

1. This environment is Linux, not Windows. Every Windows-behavior check (section 7) is listed as not run.
2. The evidence contradicts several assumptions in the brief (section 3). The brief says to stop and report contradictions with the tradeoff rather than substitute technologies, so I have not written `docs/decisions.md` or any design doc, and I have not started Slice 1A.

The raw, per-item evidence lives in four appendices, each written by an independent research pass and spot-read by me:

| Appendix | Covers checklist items |
|---|---|
| [`phase0/go-platform.md`](phase0/go-platform.md) | 1, 2, 3, 3a, 12, 14, 15, 16, 17, 18, 19, 21, and Go dependency versions and licenses |
| [`phase0/agent-clis.md`](phase0/agent-clis.md) | 3b, 3c, 4 to 11, 13, 20 (per-CLI part), 22, 23, 29 |
| [`phase0/model-providers.md`](phase0/model-providers.md) | 8 (provider caches), 16 (provider side), 24 to 26b |
| [`phase0/tooling-integrations.md`](phase0/tooling-integrations.md) | 20 (catalog), 27, 28, 29a, 30, 31, 32 |

Small reproducible probes (SQLite/FTS5, goose migrations, ACP initialize against five agents, tree-sitter, agent-framework-go resume experiment, gitleaks/CEL/mvdan-sh, SDK fakes, Job Object compile check) are in [`../evidence/phase0/`](../evidence/phase0/).

## 1. Method and limits

- Sandbox network was restricted. Reachable: `raw.githubusercontent.com` (any public repo file), the Go module proxy, npm, PyPI, `pkg.go.dev`, `code.claude.com`, `platform.claude.com`, Microsoft Learn through an MCP tool, and web-search snippets. Unreachable: github.com HTML and API, OpenRouter, Cloudflare docs, Google/OpenAI/Gemini docs sites, Linear, Notion, Sentry, Aikido, Hugging Face (one agent tried a connector).
- Where a vendor site was unreachable, evidence comes from the docs folder of the vendor's GitHub repo (read raw), from npm tarballs and Go module zips (read and, for open-source tools, built and run), or from search snippets. Every appendix tags its claims **VERIFIED**, **SNIPPET-ONLY** or **UNVERIFIED**. Search-snippet claims are leads, not facts.
- Nothing was run on Windows. "Windows cross-compile OK" means only that the code links.
- No login, no credentials, and no model call were used. Real stream captures, token and cost figures, mid-tool interrupt behavior, and real permission prompts for all five CLIs are therefore **not** verified.
- Live `initialize` handshakes were run against `gemini --acp`, `opencode acp`, `goose acp`, `claude-agent-acp@0.87.0` and `codex-acp@2.1.1` (outputs in `evidence/phase0/acp-probes/`).

## 2. Stack verdict (section 2 of the brief)

All versions are the latest stable found on 2026-10-08. "OK" means license verified and no blocker found on Linux; Windows behavior is separate (section 7).

| Item | Version | License | Verdict |
|---|---|---|---|
| Wails v3 | v3.0.0-beta.28 (2026-10-05) | MIT | OK with caveat: still **beta**. Builds Windows-only with `CGO_ENABLED=0`; bindings generation ran. Installer: NSIS and MSIX, **no WiX**. Open-issue list UNVERIFIED. |
| modernc.org/sqlite | v1.60.1 (SQLite 3.53.4) | BSD-3-Clause | OK. FTS5 and `bm25()` ran; WAL ran; no CGO. See gotcha in 3.13. |
| zalando/go-keyring | v0.2.8 (+ wincred v1.2.3) | MIT | OK. **2560-byte limit confirmed** in source and by Microsoft docs, so the encrypted-in-SQLite fallback is required. |
| pressly/goose (migrations tool) | v3.28.0 | MIT | OK. Embedded FS plus the modernc driver ran, including an FTS5 external-content table with triggers. |
| coder/acp-go-sdk | v0.13.5 | Apache-2.0 | OK with caveats: pre-1.0, no Windows CI, `usage_update` and prompt `usage` are marked UNSTABLE. ACP spec repo is Apache-2.0; stable protocol version is 1. |
| anthropic-sdk-go | v1.79.1 | MIT | OK. Retry, streaming, cache_control, `WithBaseURL`, and response-header capture ran against local fakes. |
| openai-go | **`/v3`** v3.74.0 | Apache-2.0 | OK. Import path is `github.com/openai/openai-go/v3`. `ChatCompletionAccumulator` drops `CacheWriteTokens`; read the final chunk's usage ourselves. |
| modelcontextprotocol/go-sdk | v1.8.0 | MIT to Apache-2.0 transition (mixed) | OK technically; **needs legal sign-off** on the mixed license. No Windows CI upstream. |
| go-pty | v0.2.3 | MIT | OK with caveat: its attribute list holds only the ConPTY attribute, so it cannot take a Job Object list (3.10). |
| cel-go | `cel.dev/cel-go` v0.32.0 | Apache-2.0 | OK. The old `github.com/google/cel-go` path fails. |
| mvdan/sh | `mvdan.cc/sh/v3` v3.14.1 | BSD-3-Clause | OK for parsing and policy. It is a POSIX shell parser, not a cmd or PowerShell parser (3.12). |
| gitleaks | `github.com/zricethezav/gitleaks/v8` v8.30.1 | MIT | OK as a library (222 default rules; detected a fake key). The `github.com/gitleaks/...` path fails. |
| osv-scanner | v2.6.0 | Apache-2.0 | OK as an external binary (needs Go 1.27 to build from source). Sends package names and versions to osv.dev unless run offline; disclose to users. |
| Playwright | 1.64.0 | Apache-2.0 | OK, needs Node 22+; downloads Chromium at first run. No official Go binding. |
| Wrangler | 4.148.0 | MIT OR Apache-2.0 | OK, needs Node 22+. Cloudflare is introducing a `cf` CLI (beta) to replace it for project commands. |
| gh | v2.102.0 | MIT | OK as an external binary, with gaps (3.9). |
| tree-sitter (official Go bindings) | v0.25.0, grammars MIT | MIT | **Unproven on Windows; requires CGO** (3.5). |
| OpenTelemetry Go | v1.47.0 | Apache-2.0 | OK, but GenAI conventions are in flux (3.12). |
| go-task | v3.54.0 | MIT | OK as dev tooling. |
| go-licenses | `/v2` v2.0.1 | Apache-2.0 | OK as dev tooling. |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause | Job Object functions present; `PROC_THREAD_ATTRIBUTE_JOB_LIST` is **not** defined (value 0x2000D derived from `WinBase.h`). |
| Microsoft agent-framework-go | v0.1.0 (public preview) | MIT | Design reference only. Weaknesses confirmed by experiment (3.11). |
| Agent Room | main | MIT | Reference only. |
| Aider | 0.86.x | Apache-2.0 | Algorithm fully described in the appendix, enough to reimplement. No upstream SQL tags query. |

Agent CLIs: Claude Code 2.1.293 (proprietary), Codex 0.161.0 (Apache-2.0), Gemini CLI 0.63.0 (Apache-2.0), OpenCode 1.18.35 (MIT; repo moved to `anomalyco/opencode`), Goose 1.53.0 (Apache-2.0; repo `aaif-goose/goose`).

**Go toolchain floor.** The app module must declare `go 1.26.0` (sqlite, goose, otel, mvdan/sh, x/sys all do). The sandbox's Go 1.24.7 only worked through toolchain auto-download.

## 3. Contradictions and decisions needed

Each item states the evidence, the tradeoff, and my recommendation. None has been acted on. I need a decision on D1 to D8 before writing `docs/decisions.md`; D9 to D14 are lower-stakes and I will record my recommendation as the decision unless told otherwise.

### 3.1 D1. The Anthropic terms put the Claude Code subscription premise at legal risk

- The legal page says developers building products on the Agent SDK should use API-key auth; Anthropic "does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users," and developers "may not collect, store, or intermediate Claude.ai credentials or session tokens." The Agent SDK overview adds that, unless previously approved, third parties may not "offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK." Consumer terms bar automated access except via API key or where explicitly permitted.
- The same page says it does not prevent "an end user from signing in to the unmodified Claude Code binary with their own Claude subscription."
- The Claude ACP bridge (`@agentclientprotocol/claude-agent-acp`) is an Agent SDK product and bundles its own 234 MB copy of Claude Code, which is not the user's installed `claude` unless `CLAUDE_CODE_EXECUTABLE` is set.
- Tradeoff: the product's second success metric (premium-subscription usage) and "bring your own subscription" model both lean on this.
- **Recommendation:** default to spawning the user's own, unmodified, user-logged-in `claude` headlessly (`claude -p --input-format stream-json --output-format stream-json`, which supports an `interrupt` control message and `--permission-prompt-tool`), never read or move credentials, and keep the ACP bridge disabled until counsel or Anthropic confirms. Get written confirmation before selling. This inverts "ACP first" for Claude only.

### 3.2 D2. Gemini CLI consumer access is reportedly retired

- SNIPPET-ONLY: Google says Gemini CLI stops serving Google AI Pro/Ultra and free individual accounts on 2026-06-18; the successor is the closed-source Antigravity CLI (`agy`), which has its own first-party ACP server (registry `antigravity-acp` 1.3.0, proprietary) and a clause forbidding third-party tools from accessing the service. Gemini CLI still ships (0.63.0, 2026-10-06) with `--acp`, and its repo terms say routing its OAuth through third-party tools is a violation.
- Tradeoff: the "Gemini CLI quota" half of the second success metric may not exist for consumer users. API key, Vertex and enterprise auth still work.
- **Recommendation:** support Gemini CLI with API-key/Vertex auth only; show a warning on the Connection; defer Antigravity until Google answers the orchestration question in writing. Re-verify the cut-off date from an unrestricted network.

### 3.3 D3. Codex CLI cannot be the generic cheap-tier agent

- Codex's config schema accepts only `wire_api = "responses"`. OpenRouter's chat route and Chat-Completions-only hosts therefore cannot back Codex. Whether OpenRouter's Responses endpoint is good enough is UNVERIFIED.
- OpenCode and Goose both support OpenAI-compatible providers first-class (OpenCode via `@ai-sdk/openai-compatible` and `baseURL`, Goose via host/base-path settings and custom providers). Claude Code and Gemini CLI cannot take non-native providers (Anthropic docs: routing Claude Code to non-Claude models through a gateway is unsupported).
- **Recommendation:** make **OpenCode the default cheap-tier agent** and Goose the second. Codex runs on a ChatGPT/API-key subscription (premium tier) and joins the cheap tier only if a Responses endpoint is verified on a networked machine. The brief's list of cheap-tier agents changes accordingly.

### 3.4 D4. The reference cheap model is only available as a free, trainable route

- Cohere North Mini Code exists: Apache-2.0, 30B total / 3B active, 256K context, 64K output, SWE-bench Verified 67.6 (from the Hugging Face model card). On OpenRouter it is `cohere/north-mini-code:free`, priced at $0, with **no paid OpenRouter price found**. OpenRouter's free endpoints may train on or publish prompts (guardrail fields `enable_free_model_training`, `enable_free_model_publication`).
- That makes it `trainable` under the brief's rules: usable only for public-docs CONSULT, never for agent work on private code. Its data policy tag is UNVERIFIED.
- Tradeoff: the cheap-first ladder, and the Slice 2 acceptance metric (share of tasks accepted with a cheap worker only), need a **paid, `private`** cheap model with tool calling. The mechanism exists: OpenRouter accepts `provider:{data_collection:"deny", zdr:true}` per request and can only tighten the account setting, so `private` can be enforced in code and fails closed. Account-level privacy toggles cannot be read through the API.
- **Recommendation:** keep the ladder model-agnostic. Pick the cheap `private` model at setup from `GET /api/v1/models/user` (the effective filtered catalog) and from live prices, with a self-hosted vLLM/SGLang host (North Mini Code weights are Apache-2.0) as the other route. I need a human with an OpenRouter key to confirm current paid cheap coding models and prices; I could not.

### 3.5 D5. tree-sitter needs CGO, which is unproven on Windows (a stated stop condition)

- Official `go-tree-sitter` v0.25.0 requires CGO. On Linux with gcc: 10 grammars, 32 s cold build, 27.8 MB binary, all snippets parsed, a Go tags query captured the expected functions. A `zig cc` cross-build produced a Windows PE (104 s, 28.5 MB) that was never executed. Upstream CI for the bindings and grammars includes `windows-latest`. Native MinGW, Defender and SmartScreen are untested.
- Packaging traps: the tagged SQL grammar (`DerekStride/tree-sitter-sql` v0.3.11) does not build because `parser.c` is gitignored, so it needs a pinned pseudo-version from the `gh-pages` branch or a vendored `parser.c`; the TypeScript grammar is at v0.23.2 (ABI 14); Aider has no SQL tags query and no separate TSX query, so we author those.
- Item 17's acceptance test ("the repo map builds reliably in CI on a Windows runner; if it does not, stop and report") cannot be run here.
- **Recommendation:** make a Windows CI job the first thing built after approval: `windows-latest`, MinGW gcc, pinned grammars, run the parse test, scan the exe with Defender. Do not commit to the repo map until that job is green; the Slice 1 loop does not depend on it.

### 3.6 D6. No Cloudflare token scope separates preview from production

- From Cloudflare's official OpenAPI, both "upload version" and "create deployment" require `Workers Scripts Write`; Pages uses `Pages Write` for preview and production alike, with `--branch` deciding which. No per-Worker token scoping was found. The brief's "credential scoped to preview only" (section 13a) is not achievable by scope alone.
- `wrangler versions upload` Version URLs "use that Worker version's existing configuration and resources," so they hit production bindings (KV/D1/R2). The new `wrangler preview` (Wrangler 4.135+, needs a `previews` block) isolates Durable Objects and, with separate bound resources, KV/D1/R2; service bindings, routes and cron still point at production.
- **Recommendation:** enforce the boundary in the app: (a) preview credentials live in a **separate Cloudflare account**, because an account-scoped token cannot touch another account, which is a real boundary; (b) an agent-process command allowlist (`wrangler preview`, `versions upload`; never `deploy`); (c) production tokens never enter agent processes; (d) separate bindings in the `previews` block of the starter. Accept this and amend section 13a.

### 3.7 D7. Windows process-tree design needs a changed approach

- `x/sys/windows` v0.48.0 has the Job Object API but not the job-list attribute constant (define 0x2000D ourselves). `go-pty` creates its attribute list with capacity 1 for ConPTY only and discards the thread handle, so ConPTY children cannot be spawned suspended and put in a per-agent job through it. The MCP go-sdk `CommandTransport` and the ACP examples kill only the direct child, and `npx.cmd`/`uvx` wrappers leave grandchildren.
- **Recommendation:** put the app process in a `KILL_ON_JOB_CLOSE` job at startup (children inherit it; nested jobs need Windows 8+, untested here); for per-agent kill, create one job per agent and assign the pipe-spawned agent processes suspended-then-resume, and use ConPTY only for the embedded terminal, where tree-kill by job after spawn is acceptable. Prove with the grandchild fixture on Windows (section 7) before relying on it.

### 3.8 D8. Packaging and updating constraints

- Wails v3 builds NSIS (default) and MSIX; **no WiX**. Its built-in updater (Ed25519-verified, GitHub Releases provider) swaps the exe in place, which works for a user-writable install, reportedly (field report only) breaks under a machine-wide Program Files install, and cannot work in MSIX (read-only package). MSIX updates go through App Installer or the Store.
- Signing: Microsoft says EV certificates no longer bypass SmartScreen and Artifact Signing gives no instant trust (weeks and hundreds of clean installs); Microsoft pages disagree on eligibility (countries, a 3-year tax history for organizations). Wails wires only `.pfx`/thumbprint signing via signtool, not Artifact Signing.
- **Recommendation:** NSIS with `INSTALL_SCOPE=user` plus the Wails updater, not MSIX. Do the Artifact Signing eligibility check when the company account is created. Treat SmartScreen warnings on early releases as expected.

### 3.9 Other findings that change the design (recommendation recorded unless overridden)

- **D9. Node is a hard prerequisite.** The Claude and Codex ACP bridges (Claude bridge needs Node 22+ and the Codex bridge pulls in its own Codex copy), Gemini CLI (Node 20+), Wrangler (Node 22+) and Playwright (Node 20+/22) all need it. Connections must detect Node and give install guidance; bridge versions are pinned exactly in manifests because they move weekly. The old `@zed-industries/*-acp` packages are deprecated (use `@agentclientprotocol/claude-agent-acp@0.87.0`, `@agentclientprotocol/codex-acp@2.1.1`).
- **D10. ACP capabilities differ per agent, so the one generic adapter must gate on `agentCapabilities` at runtime.** Gemini and Goose advertise `loadSession` only (no `session/resume`); Gemini emits no `usage_update` (counts sit in non-standard `_meta.quota`); Codex plan quota is structured only via `codex app-server` `account/rateLimits/read`; Claude forwards rate limits as `_meta["_claude/rateLimit"]`. **Goose acts as an ACP agent** (live `initialize` succeeded) and also has an ACP client side, which resolves the conflicting sources.
- **D11. Headless mode cannot answer permission prompts for OpenCode** (`run` auto-rejects unless `--auto`) **or Antigravity** (soft-denies). For these, ACP `session/request_permission` is the only interactive path. OpenCode `run --format json` also drops text deltas and emits tools only when completed, so use `opencode acp`.
- **D12. Observability and metrics.** OTel GenAI semantic conventions are in **Development** status and moved to `open-telemetry/semantic-conventions-genai`; Go `semconv` v1.42 and v1.43 contain no `gen_ai`, and v1.41 names cache-write tokens `cache_creation` while the new registry says `cache_write`. Define our own attribute constants. The OTLP exporter alone makes a 21.5 MB hello binary, so keep export optional. Normalize token fields in one place: Anthropic `input_tokens` excludes cached tokens; OTel's field should include them.
- **D13. OpenRouter rate limits and quota ledger.** Free limits confirmed: 20 requests per minute; 50 per day below 10 credits purchased, 1000 per day at 10 or more. `X-RateLimit-*` and `Retry-After` appear only on **error** responses, so the ledger polls `GET /api/v1/key` (daily counter, not per-minute) and reads `usage` on every response. Gemini 429 bodies carry `quotaId` and `retryDelay`, not `Retry-After`. Brief terms map as `trainable` (report: `public`) and `private`.
- **D14. Integrations and licensing.**
  - Sentry's MCP server is FSL-1.1-Apache-2.0 (source-available, not OSI); Aikido's official MCP (`@aikidosec/mcp`) is AGPL or commercial and is a local stdio server; Notion's local MCP server is unmaintained, so only the remote OAuth server (`https://mcp.notion.com/mcp`) is supported. **Do not bundle any of these**; connect to hosted servers or to user-installed ones.
  - Aikido's public API is OAuth client-credentials only, so every customer creates their own credentials; findings per repository are exportable. Linear, Notion, Sentry and Aikido commercial-resale terms are UNVERIFIED.
  - `gh pr view --json` has no review threads, and `gh repo create` and `gh release create` have no `--json`; resolving threads needs `gh api graphql` (the GitHub MCP server handles threads natively). **VERIFIED (2026-10-08, live read-only test against kjmaguire/Drydock):** `gh api -i -H 'If-None-Match: <etag>'` returns `HTTP 304`, which `internal/git/host` maps to `ErrNotModified`; conditional 304s do not count against the primary rate limit, per GitHub docs. Drydock therefore polls PRs and checks through `gh api` with ETags and needs no token of its own. GraphQL is blocked in this sandbox, so only review-thread resolution (the one GraphQL operation) is unexercised here.
  - Cloudflare docs content is CC BY 4.0 (attribution needed when we redistribute excerpts in a paid product). Microsoft Learn MCP content is CC BY 4.0 with code MIT; terms for commercial use were not found.
  - Cloudflare says to start new projects on Workers, not Pages, and is moving tooling to `cf`; the golden starter should target Workers.

### 3.10 Items that held up

- Workflow engine design basis (14): agent-framework-go's supersteps, `RequestPort`, `Parent`-linked checkpoints, and a pluggable `Store` match the brief. My experiment confirmed its weaknesses (3.11).
- SQLite as single authority (3): see 3.13.
- ACP is a workable primary transport for four of five CLIs (3.1 is the Claude exception).
- Agent Room's tool-call recovery handles one tool and one provider's leak shape; reimplement the idea generally (appendix, item 12).
- Repo map: Aider's algorithm is fully specified in the appendix (tags, weighted multigraph, personalized PageRank, rank distribution to definitions, binary search on the token budget).

### 3.11 Workflow engine: experiment results (item 14)

`evidence/phase0/go-probes/maf/`: a fan-out of A, B, C into a barrier, with B failing in run 1.

- Run 1: A and C completed, B failed; only the checkpoint before the superstep existed.
- Resume 1 from that checkpoint: A, B, C all re-ran (side-effect counters A=2, B=2, C=2).
- A second, concurrent-style resume from the same checkpoint re-ran everything again (counters at 3) and emitted a second OUTPUT. There is no idempotency or lease.
- Executor-local state is checkpointed only if the executor implements the hooks. Workflow-level middleware does not exist (`agent.Middleware` wraps agent runs only).

These are exactly the failure modes the brief's section 4 names. They confirm that our own `node_executions` idempotency keys, per-run lease, and `response_entry` checkpoints are required, and that agent-framework-go can be a reference but not the engine.

### 3.12 Smaller verified gotchas to design around

- mvdan/sh parses POSIX shell and ran correctly on `rm -rf` and `git push --force` pipelines. It does not parse cmd or PowerShell, so dangerous-command detection on Windows needs a separate PowerShell/cmd strategy (open design item for `docs/security.md`).
- Anthropic and OpenAI SDK retries honor `retry-after-ms`/`Retry-After`; `option.WithResponseInto` captures rate-limit headers for the ledger.
- `claude --bare` disables OAuth/keychain (API key only), so it is unusable for subscription users.
- Instruction files: write `AGENTS.md` as canonical and add one-line import files. Claude reads `CLAUDE.md` and falls back to `AGENTS.md` only if no `CLAUDE.md` exists; Gemini reads `GEMINI.md` (configurable `context.fileName`); Codex, OpenCode and Goose read `AGENTS.md` (Goose also `.goosehints`). Only Claude Code has native path-scoped rules (`.claude/rules` with `paths:` frontmatter); all others need our own scoping, so the brief's glob-scoped skills must be delivered by the Context Engine and Workspace MCP server for non-Claude agents. The portable baseline is `AGENTS.md` plus `SKILL.md`.

### 3.13 SQLite gotcha

A deferred transaction that reads, after which another connection commits, fails immediately with `SQLITE_BUSY_SNAPSHOT` when it tries to write; `busy_timeout` does not help. Use a single writer connection (or `_txlock=immediate`) with pooled readers, as the brief already intends. Linux-only numbers: 100,000 event rows at 141k rows/s with 1000-row batches, 65k rows/s at 100-row batches, 25k rows/s one row per commit; a writer plus four readers ran with zero errors. The Windows build is compiled with `SQLITE_MUTEX_NOOP`, so the concurrency test must be re-run on Windows.

## 4. Checklist status

Legend: **Done** = answered from evidence in this session; **Partial** = answered with named gaps; **Not run** = needs Windows, a login, or an unrestricted network.

| # | Item | Status | Where / gap |
|---|---|---|---|
| 1 | Wails v3 | Partial | go-platform §1. Running the window, packaging, and the issue list are not run. |
| 2 | Windows process handling, grandchild fixture | **Not run** | Needs Windows. API presence confirmed (go-platform §14). |
| 3 | modernc sqlite | Done on Linux | go-platform §2. Windows re-run needed. |
| 3a | go-keyring | Partial | Limits confirmed; real Credential Manager round-trip not run. |
| 3b | Per-CLI install, version, login, minimum, terms | Partial | agent-clis §1 to §5, §9, §12. Windows install paths not run; Codex and Google terms SNIPPET-ONLY. |
| 3c | Antigravity | Partial | agent-clis §8. Mostly SNIPPET-ONLY; decision deferred (D2). |
| 4 | Headless mode and output schemas | Done from source/help | agent-clis §1 to §5. Real streams need a login. |
| 5 | Model/effort flags, resume, interrupt, permission modes | Partial | Flags and ACP cancel confirmed. Headless interrupt behavior UNVERIFIED for Codex exec, Gemini, OpenCode run, Goose. |
| 5a | Tool-call visibility | Done | Claude true; Gemini true; OpenCode true (ACP full, `run` completed-only); Codex exec partial, ACP true; Goose ACP true, headless shape UNVERIFIED. |
| 6 | MCP config and usage/cost reporting | Done | agent-clis per-CLI tables. |
| 7 | Auth, native vs WSL | Partial | Credential locations and flows recorded. The WSL run mode for Claude Code is built (decision P41): a distro's install is its own Connection with its own sign-in (the CLI's own `auth status` inside the distro). Proven against a labelled stand-in claude on the Linux host and, by CI's `wsl` job, against a real WSL 2 distro on the GitHub-hosted Windows runner (detection, launch, paths, stream-json, interop, stopping the Linux processes). A real signed-in Claude Code inside a distro, and every other CLI under WSL, not run. |
| 8 | Prompt cache across model changes | Partial | Anthropic (Claude Code docs quote), Gemini and OpenRouter quoted; OpenAI first-party quote UNVERIFIED. |
| 9 | Provider terms on automation | Partial | Anthropic and Gemini quoted from primary sources (D1, D2); OpenAI and Antigravity UNVERIFIED. |
| 10 | Claude Design | Done | A product surface (`claude.ai/design`, `/design`), not a CLI or ACP agent. Not an adapter target. |
| 11 | ACP spec, bridges, transport per CLI | Done | agent-clis §7 with live handshakes. |
| 12 | acp-go-sdk | Done | go-platform §5. Ran its demo agent and client end to end (not a real model). |
| 13 | ACP permissions vs approval system with action-hash binding | Partial | Client API returns an option choice, so hashing `toolCall` kind/rawInput and binding an approval is feasible. Behavior with real agents, and whether agent permission modes skip the request, is not run (needs a login). |
| 14 | Microsoft Agent Framework Go | Done | go-platform §11 and the resume experiment. |
| 15 | Checkpoint storage on SQLite | Partial | Write-rate and concurrency numbers only. Checkpoint size growth, compaction, and the two-instance lease test belong to Slice 1C. |
| 16 | LLM SDKs | Done against fakes | go-platform §6. Real Gemini/Ollama/vLLM usage fields: provider appendix. |
| 17 | tree-sitter on Windows CI | **Not run** | D5. Linux and `zig cc` only. |
| 18 | OpenTelemetry | Done | D12. |
| 19 | Agent Room tool-call recovery | Done | go-platform §12. |
| 20 | MCP across CLIs and the catalog | Partial | Per-CLI mechanism done; catalog URLs, transports, auth, licenses done; result sizes UNVERIFIED (docs servers were not called). |
| 21 | Dependency licenses | Done | Section 2. `THIRD_PARTY_NOTICES.md` is seeded in the design step, after this stop. |
| 22 | Goose | Done | ACP agent confirmed live; headless `--output-format json` and `stream-json` found in source; Ollama and OpenAI-compatible providers documented; Ollama-in-worktree run not done. |
| 23 | Quota visibility per CLI | Done | agent-clis §10: Codex yes (app-server); Claude partial (rate-limit events); Antigravity yes (`-p "/usage"`); Gemini internal only; OpenCode and Goose none. |
| 24 | OpenRouter | Mostly done | model-providers §A. Live prices UNVERIFIED. |
| 25 | OpenCode/Goose/Codex against OpenRouter and generic base URL | Done from docs and schema | Not exercised against a live endpoint. See D3. |
| 26 | Google AI Studio | Partial | Endpoint coverage probed live; free-tier limits per model UNVERIFIED; terms wording UNVERIFIED. |
| 26a | North Mini Code | Done | D4. |
| 26b | SGLang/vLLM hosts | Partial | Flags and endpoints documented; **throughput at 1, 2, 4 concurrent agents needs a GPU host** (procedure in model-providers §E). |
| 27 | Cloudflare tooling | Done from repo docs | D6. Runtime behavior of `wrangler dev` port selection UNVERIFIED. |
| 28 | osv-scanner and Playwright on Windows | Partial | Versions, licenses, assets, and docs done; Windows and WSL runs not run. |
| 29 | Instruction file names and lookup rules | Done | Section 3.12 and agent-clis §11. |
| 29a | Skills registries | Done | tooling-integrations §7. Last-commit dates UNVERIFIED. No official Microsoft Graph skills repo found. |
| 30 | GitHub CLI | Done | D14 and tooling-integrations §3. |
| 31 | Packaging | Done from docs | D8. Nothing built or signed. |
| 32 | Integrations | Partial | Linear, Notion, Aikido API details are mostly SNIPPET-ONLY; Sentry, Aikido MCP, and Notion MCP are from repos. Resale terms UNVERIFIED. |

## 5. Quota visibility (item 23, summary)

| CLI | Remaining plan quota available programmatically? |
|---|---|
| Claude Code | Partially: `rate_limit_event` in the stream (status, resetsAt, utilization) and forwarded by the ACP bridge as `_meta["_claude/rateLimit"]`. No JSON usage command. |
| Codex | Yes: `codex app-server` `account/rateLimits/read` (primary and secondary windows with used percent and reset time). Over ACP only as `/status` text. |
| Gemini CLI | Internal only (`retrieveUserQuota`, surfaced by interactive `/stats`). |
| OpenCode, Goose | None (local accounting only). |
| Antigravity | Yes: `agy -p "/usage"` (SNIPPET-ONLY from its changelog). |

Any approach that proxies CLI traffic to read quota carries the terms risk in D1 and D2; I recommend reading only what each CLI prints or emits itself.

## 6. Recommended transport per CLI (pending D1 to D3)

| CLI | Primary | Fallback | Notes |
|---|---|---|---|
| Claude Code | Headless stream-json of the user's own `claude` (D1) | ACP bridge, off until legal review | Needs CLI 2.1.205+ for interrupt receipts; recommend 2.1.277+. |
| Codex | ACP via `@agentclientprotocol/codex-acp@2.1.1` | `codex exec --json` | Quota via a side channel to `codex app-server`. Responses-only providers. |
| Gemini CLI | ACP `gemini --acp` (API key / Vertex) | `gemini -p -o stream-json` | No resume or usage_update over ACP. |
| OpenCode | ACP `opencode acp` | none (headless `run` is lossy and cannot ask permission) | Default cheap-tier agent. WSL recommended by its docs; native binary exists. |
| Goose | ACP `goose acp` | `goose run --output-format stream-json` | No `session/resume` over ACP. |

Declarative manifest scope (spawn argv, detection, version range, env, auth probe, instruction-file names, skill dirs, parser choice) versus Go (ACP client with capability gating, usage and quota normalizers, five headless parsers, never-touch-tokens auth orchestration, Node detection, provider-config writers with validation) is itemized in agent-clis §12.

## 7. Checks that could not run here (all need Windows, a login, a GPU, or an unrestricted network)

Run these on a Windows 10/11 machine, in this order, before the corresponding code is trusted:

**Status (2026-10-22).** Since the repository went public (2026-10-10, P2 resolved), GitHub's `windows-latest` runners have run, and pass: item 1 (`jobtree` probe and `internal/proc` tests; in WSL mode, stopping a run kills its Linux processes, against a real distro with a stand-in claude, CI's `wsl` job), item 2 (tree-sitter with CGO, ten languages, a Defender scan of the exe), item 3 in part (a Wails sample app's window for 15 seconds and its NSIS user-scope installer; not signing, SmartScreen or the updater), item 4 (the NTFS concurrency and batch probe), item 5 (the 2560-byte limit and `ErrSetDataTooBig`; not the locked store), item 6 (ConPTY with `x/ansi`) and item 7 in part (npm installs, `where`, versions, ACP initialize through `npx.cmd`; no logins, so not credential files or sign-in states). Items 8 to 12 still need a login, a GPU or an unrestricted network. Workflows: `ci`, `verify`, `windows-probes`.

1. **Process trees (item 2).** A fixture agent that spawns grandchildren (for example a script that runs `npm test`, which spawns `node`); kill via per-agent Job Object and via app-level job; confirm no orphans, in native mode and in `wsl` mode (does killing the Windows-side process stop Linux processes; if not, signal the process group in the distro). For `wsl` mode Drydock no longer depends on the answer (P41): every process of a run carries a marker and Stop kills the marked processes and their process groups inside the distro; `TestRealWSLListsProbesAndKillsByMarker` logs what the Job Object kill left alive on the CI runner. Test nested jobs, `CREATE_SUSPENDED` plus job assignment, and ConPTY children.
2. **tree-sitter CGO build in CI (item 17).** `windows-latest`, MinGW gcc, 10 grammars: build, run the parse test, measure exe size, scan the exe with Defender.
3. **Wails v3 run and packaging.** Window, WebView2, service calls, event streaming latency; NSIS user-scope installer; signed vs unsigned SmartScreen behavior; updater swap in a user-scope install.
4. **SQLite on NTFS with Defender active**, rerunning the concurrency and batch benchmarks (the Windows lib is built with `SQLITE_MUTEX_NOOP`).
5. **Credential Manager round-trip** including a value over 2560 bytes (expect `ErrSetDataTooBig`) and a locked-store case.
6. **ConPTY via go-pty** with ANSI output through `charmbracelet/x/ansi`.
7. **Each CLI on native Windows and under WSL:** installers and PATH, `npx.cmd` spawning of ACP bridges, `claude auth status` logged-out exit code, credential file locations (Claude `%USERPROFILE%\.claude\.credentials.json`, Codex `auth.json`, Gemini `oauth_creds.json`, OpenCode `auth.json`), Ctrl-C/Ctrl-Break delivery, Git Bash dependency.
8. **Real streams (needs logins and quota):** one prompt per CLI with stream capture to settle event ordering, cost and cached-token figures, mid-tool interrupt behavior, resume round trips, and real permission prompts (item 13).
9. **Cloudflare:** `wrangler dev` auto-port behavior and workerd file locks when removing worktrees, `wrangler preview` end to end with a separate account token, Playwright first-run download and headed launch from a Wails-spawned process.
10. **`gh`:** `gh auth status --json hosts` output, Windows credential storage, `If-None-Match` handling through `gh api`.
11. **GPU host (item 26b):** vLLM and SGLang throughput with 1, 2 and 4 concurrent agents to set `max_concurrent_agents` defaults; North Mini Code under Ollama on Windows.
12. **Networked re-verification:** OpenRouter live prices and data-policy tag of the free North Mini Code endpoint, Gemini per-model free-tier limits and training terms, OpenAI and Antigravity terms, the Gemini consumer cut-off date, Linear/Notion/Aikido API pages, Notion/Linear resale terms, skills repo last-commit dates, Wails open issues, Azure Artifact Signing eligibility.

## 8. Operational notes

- **Credential exposure in the sandbox.** One research pass dumped the sandbox environment into its own tool output, which includes injected tokens (it reported a Cloudflare API token among them). Nothing from it was recorded in any file here; I scanned the appendices and probes for token patterns and found none. Treat those sandbox tokens as exposed and rotate them if they matter to you.
- The probe in `evidence/phase0/go-probes/misc/main.go` builds its fake AWS key at runtime so secret scanners do not flag the repository.
- Probe outputs under `evidence/phase0/` are from Linux and are not performance claims for Windows.

## 9. What happens next

On your go-ahead, in this order:

1. You decide D1 to D8 (my recommendations are marked). I record all of D1 to D14 and the Phase 0 evidence in `docs/decisions.md`.
2. I write the design documents listed in step 3 of the brief and seed `THIRD_PARTY_NOTICES.md` from the table in section 2.
3. I build Slice 1A through 1E. Work that depends on Windows (process manager, ConPTY, installer, tree-sitter CI) needs a Windows runner or machine; without one I can write and unit-test the Go core on Linux but cannot meet the brief's "tests green on the Windows CI runner" gate.

I recommend a short Windows validation of items 1 to 3 in section 7 before Slice 1A starts, because the process-tree design (D7) and the tree-sitter CGO decision (D5) shape package layout.


## Addendum: results verified after the first pass (2026-10-08)

- **Claude Code headless (real CLI 2.1.294):** `-p --input-format stream-json --output-format stream-json --verbose --include-partial-messages` accepts user messages as `{"type":"user","message":{"role":"user","content":"..."}}`; an interrupt is `control_request{subtype:"interrupt"}`; after an interrupt the turn ends with a result of subtype `error_during_execution` and the session stays alive for the next message. Fixtures in `internal/agents/claude/testdata/` are captured from real output. Measured cost on the haiku model: about $0.0043 per simple turn, $0.0112 per pipeline task, $0.0115 per worker+reviewer loop.
- **GitHub write path:** pushing branches and opening PRs through the sandbox proxy is partly blocked (`POST /git/refs` and remote branch deletion are refused), so the write round trip is an opt-in live test (`DRYDOCK_E2E_GITHUB_WRITE=owner/repo`) and the PR flow is tested against a labeled protocol fixture with a real bare git remote.
- **Still unproven:** every Windows claim (P2), tree-sitter CGO, the Wails shell, NSIS and the updater.
