# Drydock: AI Engineering Team, build brief (v17, 2026-10-08)

This is the owner's build brief, preserved verbatim in substance so design docs and agents can cite section numbers. Where Phase 0 evidence or an owner decision changes it, `docs/decisions.md` wins. The product is called **Drydock**.

You are the principal architect and lead engineer. Build a commercial, local-first, **Windows-only (v1)** desktop app where a human creates a team of AI coding agents, watches them collaborate in a shared Engineering Room, and approves the results.

Product principle: build software with an AI engineering team, not an AI assistant. The value is the orchestration layer (routing, handoffs, isolation, review, cost and token control), not the chat UI.

Primary success metric: **cost per accepted task**, with **premium-subscription usage per accepted task** (Claude Code and Gemini CLI quota consumed) as the second. Measure both from Slice 1 onward and design every subsystem to reduce them without lowering quality.

## 0. How to work

1. **Phase 0 (verify).** Inspect the current docs and source for everything in section 27. Write `docs/phase0.md`. Stop and report.
2. **Record decisions.** `docs/decisions.md` captures the Phase 0 evidence for each choice in section 2. If evidence contradicts a choice, stop and report; do not substitute technology on your own.
3. **Design docs.** `docs/architecture.md`, `interfaces.md`, `state-machines.md`, `workflow-engine.md`, `repo-structure.md`, `security.md`, `model-routing.md`, `token-control.md`, `room-protocol.md`, `expert-packs.md`, `mcp.md`, `github.md`, `integrations.md`, `connections.md`, `ui.md`, `packaging.md`, and seed `THIRD_PARTY_NOTICES.md`.
4. **Build** Slice 1A through 1E (section 24), proving the real loop before anything else.

Rules:
- No mocks, no fake agents, no fake Git, tests, costs, routing, or file changes. A mock is allowed only to test a failure boundary, and it is labeled. Unimplemented functionality is shown honestly in the UI.
- Do not over-engineer Slice 1. Do not build UI for features that don't exist yet.
- Do not claim learning, savings, or capability that isn't implemented and measured.
- Ask the human only on genuinely blocking ambiguity. Otherwise decide, record in `docs/decisions.md`, continue.
- Never upload user source code anywhere automatically. There is no cloud component.
- Windows-only in v1. Do not add macOS or Linux build targets, CI jobs, or code paths.
- Every slice ships with tests. Unit tests cover state machines, the router, the context engine, and the engine's checkpoint logic; integration tests run against real CLIs, real Git, and real fixture repos; CI runs on a Windows runner. Nothing is marked done with failing tests.
- Dogfood from Slice 2 on: build the remaining slices through the Engineering Room on this repository, reporting measured cloud tokens and cost per accepted task in `docs/evals.md`.
- Terminology: "Goose" is the coding agent (`aaif-goose/goose`); the migrations tool is `pressly/goose`. Refer to the latter as "the migrations tool".

## 1. Core concepts: three independent axes

An agent is `(Provider, Role, Expert Packs[])` plus a policy.
- **Provider:** Claude Code, Codex, Gemini CLI, OpenCode, Goose, Claude Design, Antigravity, custom CLI (any CLI meeting the headless contract in section 5).
- **Role:** PM, Architect, UX Designer, Frontend, Backend, Database, DevOps, QA, Security, Reviewer.
- **Expert Pack:** attachable domain knowledge.

Providers are never hard-coded agent types. Multiple instances of one provider are required; each has its own role, context, task, worktree, status, model, effort, conversation, session, and permissions. Team templates set defaults, not model locks.

## 2. The stack

| Layer | Decision |
|---|---|
| Desktop shell | Wails v3, isolated in `app/`. Go core with no Wails imports. React + TypeScript UI. |
| Storage | SQLite via `modernc.org/sqlite` (pure Go), FTS5, WAL. Migrations with `pressly/goose` (the migrations tool). |
| Secrets | Windows Credential Manager via `zalando/go-keyring`; large secrets encrypted in SQLite with the key in the credential store. |
| Orchestration | In-house Go workflow engine on SQLite: supersteps, typed request ports, checkpoints with lineage, exactly-once node effects, middleware. Informed by Microsoft Agent Framework (MIT) and Pregel; reimplemented. |
| Model Router | Our own, in Go. |
| App-owned LLM calls | Our own `LLMClient` on `anthropic-sdk-go` and `openai-go`. |
| Agents | Real CLI processes. OpenCode, Goose, and Codex CLI run cheap-tier models through OpenRouter or a local host. |
| Model providers | OpenAI-compatible HTTPS APIs via `openai-go`: OpenRouter (aggregator; `:free` models for public-docs questions only), Google AI Studio, direct Anthropic/OpenAI, local/LAN hosts. Each carries `data_class` and `tier`; a quota ledger tracks limits. |
| Adapter transport | ACP via one generic client on `coder/acp-go-sdk` for CLIs that speak it; headless JSON for the rest. |
| Git | Drive the `git` CLI. GitHub via `gh` behind `GitHost`. |
| MCP | Official `modelcontextprotocol/go-sdk`; an MCP Gateway per agent run with per-role allowlists, caching, shaping; a Workspace server exposing the app's knowledge. |
| Event bus | In-process Go bus; Wails events are only the UI transport. |
| Terminal | `aymanbagabas/go-pty` (ConPTY) with xterm.js. |
| Policy | `google/cel-go` rules. Command analysis: `mvdan/sh`. Secret scanning: `gitleaks`. |
| Verification tools | `osv-scanner`, Playwright, Wrangler as deterministic `check` nodes. |
| Repo map | tree-sitter + PageRank in Go (Aider technique). |
| Telemetry | OpenTelemetry Go SDK with OTLP exporter. |
| UI libraries | Monaco, React Flow, cmdk, TanStack Virtual, xterm.js. |
| Dev tasks | `go-task/task`. Notices: `google/go-licenses`. |

## 3. Architecture

Wails v3 UI (React + TS) over Wails services and events (transport only); Go application core, one process (Event Bus with persisted events; Workflow Engine with checkpoints; Model Router, Budget, Context Engine, Memory; Room, Tasks, Approvals, Security policy; Agent Runtime, Adapters, Process Manager; Git/Worktrees, GitHost, MCP Manager; LLMClient); external agent CLIs, each in its own Git worktree.

SQLite is the single authority for all state. The Workflow Engine owns execution order. Workflow nodes never call model providers: an `agent_task` node asks the Agent Runtime to execute a task; the router picks provider, model, effort; the Runtime drives the CLI; the result returns as the node output.

## 4. Workflow engine

`internal/workflow`, no dependency on Wails or any provider. Graph of nodes and edges built with Go builders (sequential, concurrent fan-out/fan-in, conditional, group collaboration with bounded debate, subworkflows). Node kinds: `agent_task`, `check`, `request`, `route`, `subworkflow`. Edge kinds: direct, conditional, fan-out, fan-in (barrier), loop. Supersteps: all ready nodes run concurrently, outputs delivered at a barrier, a checkpoint written. Parallel nodes write only their own namespaced outputs.

Checkpoints (`workflow_checkpoints`): id, run id, workflow name, step, kind (`entry`, `step`, `response_entry`), parent checkpoint id, serialized state, created time. `entry` before step 0; `step` after every superstep; `response_entry` when a human response is delivered. Order by lineage (parent id). Resume from latest after a crash; "time travel" from an earlier checkpoint starts a new lineage branch; earlier checkpoints are never modified.

Request ports: a `request` node emits a typed request, stored in pending requests, creates an `approvals` row (`approval`, `question`, `review`), run enters `WAITING_HUMAN`; `Resume(run, response)` routes the answer and writes a `response_entry` checkpoint. Agent tool approvals use the same mechanism.

Exactly-once effects: idempotency key `(run_id, node_id, step, attempt)` written to `node_executions` before any side effect; on resume, completed nodes of an interrupted superstep are not re-run; started-but-unfinished nodes re-run once under a new attempt in the same worktree, seeded with any handoff or partial output; a per-run lease (`run_leases`) lets only one engine instance advance a run; side effects are idempotent Go operations keyed as above.

Middleware pipeline per `agent_task`: budget check, context assembly, security policy, watchdog, telemetry, execute, usage recording, retry policy; stream-aware hooks (start, chunk, end, error). Streaming: the engine tees every stream (UI copy and collected value); checkpoints store references to raw output on disk (`<app data>/runs/<run_id>/<node>/<attempt>/`).

Events: `workflow.*` plus OpenTelemetry spans per run, step, node.

Engine acceptance tests (Slice 1C; real processes): approval resume across app kill; partial superstep (finished node not re-run); duplicate resume (only one proceeds); time travel; replay from entry checkpoint; streaming with crash mid-stream; cancellation and loop limits; watchdog (repeat-read nudge then pause, error echo, per-turn cap, and a false-positive control in Iterate Until Verified).

Slices 1A and 1B use a trivial sequential runner behind the `Orchestrator` interface; 1C replaces it. Nodes and middleware types are shared.

## 5. Agents, runtime, processes

Pipeline: Adapter, Agent Runtime, Process Manager. Adapters know one CLI (detection, version, capabilities, configuration, startup, auth expectations, MCP config, model/effort selection, session behavior, output parsing) and report only verified capabilities. The Runtime handles lifecycle, monitoring, health, restart, interrupt, messaging, sessions, timeouts, supervision, crash recovery. The Process Manager owns PID, stdout, stderr, signals, environment, working directory, process trees: one Job Object per agent with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`; closing the job kills the tree. In `wsl` run mode, tree kill also signals the process group inside the distro if needed.

Capability matrix (normalize and decide against capabilities, never provider names outside adapters): streaming, structured output, model selection, effort control, session resume, interrupt, MCP, token usage, cost reporting, headless, permission modes, image input, tool-call visibility, run mode (`native`, `wsl`).

Transports: ACP (one generic client; permission requests become workflow requests or approvals) and headless structured output with a per-CLI parser. Headless contract: non-interactive prompt-and-exit mode, machine-readable output stream (text, tool calls, final result), exit code, working directory control; model, effort, resume, usage, permission prompts optional and recorded as present or unknown.

Connections (productized adapters): detection and install guidance (nothing downloaded on the user's behalf), authentication via the CLI's own login flow in the embedded terminal (API-key providers validated with `/v1/models` then stored in the credential store), verification ("Test connection" runs the conformance short form), status `not_installed | unsupported_version | unauthenticated | ready | degraded`. Adapter manifests (`internal/agents/adapters/manifests/<id>.yaml`) are declarative; `compat.yaml` records verified CLI versions. Conformance suite per adapter against real installs; a scheduled job runs it on latest versions and emits `adapter.drift`.

Sessions: fresh per independent task; same-session continuation within a task only when cheaper or better. Context pressure: at 70% occupancy when idle, ask for a verified handoff artifact, then start a fresh session seeded with it, within one node execution.

## 6. Model Router

Independent Go package; never calls providers; deterministic and rule-based; LLM only for genuine ambiguity. Decision record: agent, provider, model, effort, team size, reason, confidence, estimated cost and tokens, policies applied, escalated/deescalated from, alternatives. v1 signals: role, task type, complexity heuristic, risk flag, failures so far, explicit instruction and stored policies, remaining budget, model availability and capabilities. Levels: Cheapest, Fast, Balanced, Quality, Maximum Quality, Custom.

Providers are registered in Settings (name, base URL, key reference, kind: `aggregator`, `direct`, `host`) with `data_class` (`private` or `trainable`) and `tier` (`premium`, `cheap`, `free`). The Context Engine never sends repo content, diffs, room history, project memory or private-derived pack content to a `trainable` provider; `trainable` providers exist only for Expert Pack CONSULT over public documentation. Quota ledger (`provider_quotas`) reserves a slot before routing and records usage and rate-limit headers after.

Cheap-first ladder: (1) cheap-first task classes (iteration, test-fix loops, boilerplate, tested refactors, exploration, docs, first-pass review) go to a `cheap`, `private` tool-calling model; (2) premium-only: architecture, security review, ambiguous or high-risk tasks, final `SHIP` review above a diff size; (3) review economics: cheap worker iterates until checks pass and a cheap reviewer returns `SHIP`, then one premium review; after two premium `REVISE`, one watchdog stage 2, or three cheap iterations without passing checks, escalate with a handoff artifact (`routing.escalated`); (4) unavailable or quota exhausted: next provider in tier, then next tier up (`routing.fallback`), or `BLOCKED` if policy pins a tier; (5) hosts never exceed `max_concurrent_agents`.

Override priority: explicit user instruction, stored CEL policies, budget hard limits, router heuristics. Until Slice 2 assignment is static behind the `ModelRouter` interface. Outcome data is recorded from day one.

## 7. Token and cost control

Rules enforced in code: no broadcast context; orchestrated turn-taking; bounded debate (about 3 rounds); team-size routing; handoff artifacts not transcripts; session policy; cache-friendly prompts (stable prefix first); truncate tool output (raw kept on disk, fetch by range); retrieve don't preload; deterministic tools before LLMs; ranges and diffs over whole files; per-run ceilings with human escalation; exact-match response cache; budgets at project/workflow/task/agent level (warn at 80%, pause at 100%); no LLM where rules suffice; minimal iteration context; watchdog.

Watchdog (`internal/watchdog`, pure Go, no LLM): signals — tool spin (same `(tool, canonical args, normalized result)` 3 times in a rolling window of 8), error echo (same failure signature and unchanged diff hash at a `check`/back edge), hard caps (60 tool calls, 20 minutes, 5 consecutive tool errors per turn), soft stagnation (bigram Jaccard at least 0.85 three times on texts of 200+ chars: warn and lower tool-spin threshold to 2). Normalization: strip timestamps, durations, temp paths, hex addresses, ANSI, trailing whitespace; hash at most the first 4 KB. Stage 1 (first trip on a task): cancel the turn and nudge with the evidence. Stage 2 (second trip): handoff artifact, task `WAITING_HUMAN`, agent `NEEDS_ATTENTION`, notification. Never freezes a process. Adapters must emit normalized `tool_call`/`tool_result`; CLIs without them record `ToolCallVisibility=false` and run reduced detection.

Measurement: input, output, cached tokens, context size, cost, sources and exclusions for every run; estimated vs reported never mixed silently; tokens and cost per accepted task by role, workflow and tier; editable pricing table with last-updated date.

## 8. LLM access

`LLMClient` for the app's own calls (expert questions, summaries, classification): `Complete`/`Stream` with provider, model, effort, max tokens; returns content, usage (input, output, cached, cache-write), cost, model used. Wrapper reserves quota, honors `Retry-After`, retries with backoff, falls back to router-approved alternatives, refuses to send private context to a `trainable` provider, and keeps an exact-match SQLite `llm_cache` (never for side-effecting calls). Keys come from the credential store; every call writes `model_usage`.

## 9. Context Engine

Per-run controlled context package: task, role instructions, project instructions, pack instructions, relevant room messages, relevant files, git diff, previous results and handoffs, workflow state, memory hits, MCP results. Each run stores a manifest (source, included/excluded, size, reason, priority). Old room history is compressed into rolling digests. Generated instruction files (`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.goosehints`) are owned by the app in every worktree, generated from project memory and standing instructions, regenerated on change, with human edits offered back as memory updates. Skills are glob-scoped Markdown rules in `.aet/skills/`, resolved from repository scans and curated registries (never arbitrary GitHub), pinned to commits, scanned for secrets and injection, and delivered only when globs or roles match. Repo map: tree-sitter symbols, personalized PageRank, token-budgeted, cached by content hash (Aider technique, Apache-2.0, attribution in notices).

## 10. Event Bus

In-process, usable without Wails. `Publish` persists with a monotonic sequence then fans out; slow subscribers never block; `Replay(afterSeq)` for UI reconnect; append-only. Core events: `agent.*`, `adapter.drift`, `task.*`, `workflow.*`, `routing.*`, `git.*`, `github.*`, `approval.*`, `budget.*`, `watchdog.*`, `skill.*`, `provider.*`, `room.*`, `requirement.*`, `milestone.*`, `integration.*`, `mcp.*`, `pack.*`. OpenTelemetry export is optional and never includes prompts, code, tool output, or secrets unless explicitly enabled.

## 11. Engineering Room

Structured messages (`CHAT, TASK, DECISION, STATUS, RESULT, QUESTION, HANDOFF, ERROR, APPROVAL_REQUEST, APPROVAL, SYSTEM`) with mentions, threads, presence (Working, Thinking, Idle, Waiting, Completed, Needs Attention, Failed, Paused). Room modes: `auto`, `sequential`, `moderator`. Tool members (`Tests`, `Lint`, `Types`, `Secrets`, `CI`, `Preview`) post check results at zero cost. Markers `[DECISION] [TODO] [STATUS] [RESULT]`. Exports as Markdown minutes and PR description drafts (immutable); `DECISION` messages become ADR candidates. Conversation kinds on one `rooms` table: channels, task channels, DMs, group chats. Project section: Overview, Requirements, Tasks, Milestones, Activity, Integrations.

Integrations (Linear, Notion, Sentry, Aikido, GitHub) are Connections with app sync (polling with cursors into `external_items`) and agent access through official MCP servers where they exist; nothing becomes a task without a human action or a stored rule; outbound writes are approval-gated by default; inbound content is untrusted. Requirements are uploaded or synced documents chunked into FTS5 and cited like an Expert Pack.

## 12. Tasks and state machines

Task states: `BACKLOG, READY, CLAIMED, IN_PROGRESS, BLOCKED, WAITING_HUMAN, REVIEW, VERIFICATION, COMPLETED, FAILED, CANCELLED`. `BLOCKED` means something prevents progress and clears when the impediment clears; `WAITING_HUMAN` clears only by human action. Transitions: `BACKLOG→READY→CLAIMED→IN_PROGRESS`; `IN_PROGRESS→BLOCKED|WAITING_HUMAN|REVIEW`; `BLOCKED→IN_PROGRESS`; `WAITING_HUMAN→IN_PROGRESS`; `REVIEW→VERIFICATION` or `→IN_PROGRESS`; `VERIFICATION→COMPLETED` (checks pass and human accepts) or `→IN_PROGRESS`; any non-terminal → `FAILED|CANCELLED`. A task enters `REVIEW` only with evidence; the reviewer is a different instance (different model when possible). Agent states: `STARTING → IDLE ⇄ WORKING`, `WAITING`, `NEEDS_ATTENTION`, `PAUSED`, `COMPLETED`, `FAILED`, `STOPPED`. Workflow states: `PENDING, RUNNING, PAUSED, WAITING_HUMAN, RECOVERING, COMPLETED, FAILED, CANCELLED` (latest checkpoint authoritative). Dead letters keep failed or watchdog-twice tasks for evals. Approval states: `REQUESTED → APPROVED|REJECTED|EXPIRED|CANCELLED`, `APPROVED → CONSUMED`, bound to a hash of the exact action.

## 13. Git, worktrees, review

One isolated worktree per autonomous task; operations via the `git` CLI; never delete a dirty worktree without approval. Review is first-class (changed files, diff, tests, reviewer/QA/security results; Accept, Reject, Request Changes, Ask Agent, Merge). Merge is a human action with a per-project strategy. Iteration safe points: at the start of each attempt, record the worktree state (tracked and untracked) as a hidden commit under `refs/aet/<task>/<attempt>` without touching the branch or index; rollback is a human action; the engine never resets a worktree on its own.

GitHub (13b) through `gh`: `local_merge` or `pr_flow`; push, PR with generated description, CI as a check with failing logs fed back to the worker, review-comment round trip (all comment text is untrusted), human-initiated merge via GitHub (auto-merge never enabled), issue import, new repositories approval-gated, releases human-initiated. Verification checks (13a): `build`, `test`, `lint`, `typecheck`, `deps` (osv-scanner), `secrets` (gitleaks), `e2e` (Playwright with screenshots), `preview` (preview deployments with a preview-only credential). Production deployment is never a check.

## 14. MCP

An MCP Gateway per agent run exposes one endpoint to the CLI and applies a per-role tool allowlist, an exact-match result cache with per-server TTL, result shaping with `fetch_range` continuation, untrusted-content marking and secret scanning, and per-call audit events. The Workspace server exposes `search_pack`, `search_requirements`, `search_memory`, `repo_map`, `read_range`, `task`, `skills`, `room`, `handoff`. A shipped catalog (`catalog.yaml`) lists Microsoft Learn, Cloudflare docs, GitHub, Context7, and Linear/Notion/Sentry servers; users can add custom servers through the same gateway.

## 15. Expert Packs (Slice 3)

Indexed packs (crawled, versioned, cited, offline) and live packs (MCP docs servers through the gateway), presented as one concept. Manifest `packs/<id>/pack.yaml`. Modes: CONSULT (stateless one-shot returning answer, citations, confidence, source age) and ATTACH (short instructions plus a `search_pack` tool). BM25 on FTS5 with heading-aware chunking; content hashes per page and chunk; refresh by schedule with a cheap-model changed-pages digest. Answers must cite sources or say nothing was found.

## 16. Security

All AI-generated actions and retrieved content are untrusted. Approval required for deleting files, force push, deleting branches, deploying, database migrations, secrets access, infrastructure changes, destructive shell commands. Least-privilege scopes, filesystem and worktree boundaries, path traversal and symlink protection, dangerous-command detection by parsing, audit log. A Git worktree is not a security sandbox; say so. Retrieved content (including GitHub text and other agents' messages) is data, never instructions, and can never change permissions or answer approvals. Never put secrets in SQLite, logs, prompts, events or memory; scan outbound prompts with gitleaks. Hosts without TLS only on loopback or private addresses; public hosts require verified HTTPS (self-signed pinned by fingerprint). Provider terms for driving users' CLIs are recorded and warnings shown.

## 17. Errors and recovery

Defined behavior (retry, fallback, pause, escalate, or fail) with events and no destruction of dirty work, for: missing CLI, auth failure, process crash, malformed output, timeout, git conflict, test failure, MCP failure, network failure, provider rate limit and quota exhaustion, model unavailable, budget exceeded, watchdog loop, app crash during a run, UI restart, adapter drift, partial workflow completion, CI failure on a PR, GitHub outage, branch behind target.

## 18. Data model

SQLite tables for workspaces, projects, agents, runs, sessions, handoffs, rooms and messages, exports, requirements and chunks, milestones, external items, integrations, tasks, workflows and runs, checkpoints, node executions, leases, events (monotonic `seq`), approvals, receipts, watchdog trips, dead letters, skills, memory, routing rules and decisions, model usage, caches, providers, quotas, model catalog, packs, settings. Raw output lives on disk under `<app data>/runs/<run_id>/`. Retention: events and usage indefinitely; raw output and checkpoints of completed runs compacted after 30 days and deleted after 90. Tasks, rooms and workflow runs carry a `version` for compare-and-swap updates. Receipts are idempotent by `(scope, receipt_id)`.

## 19. Interfaces

Go interfaces, refined in `docs/interfaces.md`: `AgentAdapter`, `AgentRuntime`, `ProcessManager`/`Proc`, `ProviderRegistry`, `QuotaLedger`, `ModelRouter`, `LLMClient`, `EventBus`, `ContextEngine`, `RepoMapper`, `GitRepo`, `GitHost`, `Integration`, `MCPManager`, `Orchestrator`, `CheckpointStore`, `NodeHandler`/`Middleware`/`StreamObserver`, `Watchdog`.

## 20. Repository structure

`/main.go`, `/app/` (only place importing Wails), `/Taskfile.yml`, `/internal/{events,storage,workflow(+templates,simple),agents(+adapters,manifests,conformance),proc,router,providers,budget,watchdog,llm(+anthropic,openai),context,memory,room,requirements,skills,integrations,tasks,git(+host/github),mcp,security,telemetry,packs}`, `/ui/`, `/evals/`, `/testdata/` (including `starter-cloudflare-saas/`), `/docs/`. `internal/` never imports `app/`; subsystems communicate through interfaces.

## 21. UI

Serious commercial developer-tool UI: dense, keyboard-first, dark and light, restrained motion. Slack/Discord information architecture with engineering panels. Left sidebar (workspace and project switcher, Needs you inbox, channels, DMs, groups, project section, workspace section), center conversation or page with typed message cards and a composer (mentions, slash commands, markers, attachments), right column (thread, task details, Agent Inspector), bottom dock (terminal, diff, logs, model and token activity). Command palette, Connections page first for new users, Workflow view with React Flow and checkpoint timeline, Providers settings, Agent Inspector including context occupancy, watchdog status and "Why this model?". Build only screens for features that exist.

Commercial packaging: code-signed Windows installer, in-app updater with stable and beta channels and rollback, offline-capable Ed25519 license tokens with grace period and trial (an expired license makes data read-only, never deletes it), project export/import, scheduled local backups, support bundle, consent-based diagnostics (off by default), onboarding in under ten minutes.

## 22. Workflow templates

Full Stack SaaS, Feature, Bug, Security (each ending in human approval), and Iterate Until Verified. Spec gate for Feature and Full Stack SaaS: PM-role task writes a spec with Given/When/Then criteria, a `review` request puts it to the human, nothing is implemented until accepted; criteria are turned into tests. Golden starter: an opinionated Cloudflare stack template under `testdata/starter-cloudflare-saas/`. Iterate Until Verified: work in a fresh session, deterministic checks, a different agent reviews with `SHIP`/`REVISE`, loop with only the feedback, exit on `SHIP` to human approval; default five iterations; a worker's claim of completion never ends the loop.

## 23. Evaluation

Eval harness early: cost and premium usage per accepted task by role, workflow and tier; cheap-only acceptance share; premium `REVISE` rate on cheap-worker tasks; routing quality; completion, test success, iterations to `SHIP`; reliability and resume; watchdog trips per accepted task and false-trip rate. Every claimed improvement needs numbers; report in `docs/evals.md`.

## 24. Slices

- **1A Room and real agent:** project, team, Engineering Room, one real CLI agent, streaming output, presence, messages, Event Bus with persistence and replay, Wails transport, token usage capture, conformance suite, provider registry (OpenRouter shows served models; an OpenCode agent on a cheap-tier model completes a message with usage recorded under `cheap`). Killing the agent process kills its tree; app restart catches the UI up from the log.
- **1B Task, worktree, code:** tasks, worktree and branch per task, real coding task on the golden starter, diff, terminal, `build`/`test`/`lint`/`typecheck` checks as tool members, safe points, trivial sequential runner. A dirty worktree is never deleted without approval.
- **1C Workflow engine:** section 4 in full plus the Watchdog; all engine acceptance tests pass.
- **1D Complete loop:** task, context package, isolated worktree, second-agent review with evidence, human diff review, approve, commit and merge, as a workflow on the engine; generated instruction files; MCP Gateway with Workspace tools and docs servers; the run is fully explained by manifest, usage, checkpoints and events.
- **1E GitHub flow:** `gh` Connection, `pr_flow`, PR with generated description, `ci` check, review-comment round trip, human merge through GitHub, issue import, Pull Requests view.
- **2 Routing and economics; 3 Expert Packs, Requirements, Skills; 4 Autonomous multi-agent development; 5 Integrations** (Linear, Notion, Sentry, Aikido).

## 25. Risk register (summary)

Critical: agents run with the user's OS permissions and worktrees are not a sandbox; provider terms and CLI stability. High: workflow engine correctness, Windows process model and WSL, CLI output fragility, token blowup, scope creep. Medium: Wails v3 beta churn, ACP bridges needing Node, SQLite write contention, CGO for tree-sitter on Windows, SDK changes, router quality, cheap-first routing sending work to weaker models, free-tier and aggregator terms changing, dependency licenses.

## 26. Licensing

Commercial product. Maintain `THIRD_PARTY_NOTICES.md` (project, repository, version and commit, license, modifications, attribution) for every dependency or reused architecture. Reference projects reimplemented, not transplanted: Agent Room (MIT), Microsoft Agent Framework for Go (MIT), Aider (Apache-2.0, repo-map technique).

## 27. Phase 0 checklist

See `docs/phase0.md` for the checklist and status. Items 1-32 cover platform, agent CLIs, ACP, workflow engine design, LLM SDKs, repo map and telemetry, reference projects, Goose, quota visibility, model providers, SaaS build tooling, instruction files, skills registries, GitHub CLI, packaging, and integrations.

## 28. Deliverables

`docs/phase0.md`, `docs/decisions.md`, the design docs listed in section 0, `docs/evals.md`, `THIRD_PARTY_NOTICES.md`, Slice 1A-1E implementation in order with tests green on the Windows CI runner, then Slices 2-5 through the app itself. At the end of design, state the recommended architecture, what is built first, what is explicitly not built yet, and which decisions are locked.
