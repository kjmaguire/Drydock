# Drydock: architecture

Scope: the recommended architecture, how the built pieces fit, what is built first, what is explicitly not built yet, and which decisions are locked. It describes the code as it exists in this repository; where a subsystem is specified but not built it is marked **not built**. Owner decisions in [`decisions.md`](decisions.md) win over the brief ([`brief.md`](brief.md)). Evidence for choices is in [`phase0.md`](phase0.md).

## 1. Shape

One Windows desktop process: a Go core with no UI dependency, wrapped by a Wails v3 shell, driving real agent CLI processes, each in its own Git worktree.

```
 Wails v3 UI (Remix 3 + TypeScript)                                     [built; unproven on Windows]
        │  Wails services + events (transport only; replay by event seq)
 ┌──────▼──────────────────────────────────────────────────────────────┐
 │ Go core (one process, no Wails imports)                              │
 │  events (bus, persisted, replay) ── storage (SQLite, WAL, goose)     │
 │  workflow engine ── checkpoints, leases, request ports               │
 │      middleware: watchdog, safe points, budget (Slice 2), telemetry  │
 │  pipeline: prepare → work → verify → finalize → review → approve →   │
 │            merge (nodes composed into graphs/templates)              │
 │  tasks (state machine)   git (worktrees, safe points)   checks       │
 │  agents: Adapter/Session contracts ─ claude (headless) ─ acp ─ runtime│
 │  context engine + memory ─ generated instruction files + manifests   │
 │  room (channels, presence, turn-taking)   providers / llm / router   │
 │  security: approvals, path guard, command analysis, secrets, audit   │
 │  mcp: gateway + workspace server ─ proc: Windows Job Objects         │
 └──────┬──────────────────────────────────────────────────────────────┘
        │ OS processes (killed as trees)
 claude, codex, gemini, opencode, goose, agy ... each in its own worktree
```

Ownership rules that the code enforces:

- **SQLite is the single authority.** Domain state, events, usage and workflow checkpoints are rows; raw agent output and check logs are files under `<app data>/runs/<run>/` referenced by path. One write connection (`BEGIN IMMEDIATE`), a pool of read connections, WAL.
- **The workflow engine owns execution order.** Nodes never call model providers; an `agent_task` node asks the Agent Runtime to run a turn, and the runtime drives the CLI.
- **Everything observable is an event.** `events.Bus.Publish` persists with a monotonic sequence (group commit) and then fans out; the UI replays from its last seen sequence. A subscriber that falls behind is closed and re-syncs by replay; publishers never block.
- **Humans, not agents, decide.** Only a `human:`-prefixed identity can accept a task (`tasks`), approve a merge (`pipeline.Merge`) or answer an approval (`security`). Agent messages are data.

## 2. Packages (what exists)

| Package | Responsibility | Status |
|---|---|---|
| `internal/storage` | SQLite open/migrate, writer/reader pools, `WriteTx` | built, tested |
| `internal/events` | bus, persistence, replay, filters | built, tested |
| `internal/proc` | process manager; Windows Job Objects (kill trees); dev-host fallback | built; Windows behavior **unproven** (P2) |
| `internal/workflow` | engine: supersteps, checkpoints with lineage, request ports, fenced leases, exactly-once, middleware, streaming | built; all section 4 acceptance tests pass (kill/recover with real processes) |
| `internal/watchdog` | tool spin, error echo, hard caps, stagnation; two-stage policy; trip store | built; acceptance (a)-(d) pass |
| `internal/agents` | capability matrix, Adapter/Session contracts | built |
| `internal/agents/claude` | headless stream-json adapter + parser (real captured fixtures) | built; verified against real Claude Code 2.1.294 |
| `internal/agents/runtime` | managed sessions, `agent_runs`, `agent.*` events, usage rows, `AgentTask` handler, interrupt+nudge | built |
| `internal/agents/fakeagent` | scripted real-process agent speaking Claude's dialect (tests only, labeled) | built |
| `internal/agents/acp`, `manifests`, `conformance`; `internal/connections` | generic ACP client, YAML manifests, detection/status, conformance suite | built, tested on Linux |
| `internal/tasks` | state machine with guards, CAS transitions, evidence, reviews, dead letters | built |
| `internal/git` | worktrees, status, diff, diff hash, commit, merge strategies, safe points, local ignore | built |
| `internal/checks` | build/test/lint/typecheck runner, failing-test parsing, truncation | built |
| `internal/pipeline` | workflow nodes + `task-pipeline`, `iterate-until-verified` and `iterate-until-verified-pr` graphs | built; real two-agent loop passes; PR flow tested against a labeled GitHub protocol fixture plus a real bare remote |
| `internal/git/host` | GitHub over the user's `gh` (REST, ETag polling): PRs, checks and logs, reviews, merge, issues, releases | built; live read-only test verified (304 to `ErrNotModified`) |
| `internal/memory`, `internal/context` | project memory (FTS5), generated instruction files, context manifests | built |
| `internal/room` | channels, DMs, task channels, messages, presence, turn-taking, exports | built, tested on Linux |
| `internal/chats` | the human's own chats with an agent CLI: history, resume or transcript fallback, attachments, the `chats` workspace tool, allow rules ([`chats.md`](chats.md), decisions P29) | built; tested on Linux against the scripted agents and the real Claude Code 2.1.295; no real ACP chat turn |
| `internal/providers`, `internal/llm`, `internal/router` | provider registry, quota ledger, LLM client + cache + data_class guard, static router + CEL | built, tested on Linux |
| `internal/security` | approvals with action-hash binding, path guard, command analyzer, secret scanning, audit chain | built, tested on Linux |
| `internal/mcp` | gateway, workspace server, catalog | built, tested on Linux |
| `app/`, `ui/`, `main.go` | Wails shell (compiles for Windows, never run) and the Remix 3 UI in `ui/app` (unit tests and browser tests against the real core) | built; Windows unproven |
| `internal/integrations`, `internal/packs`, `internal/skills`, `internal/requirements` | Slices 3 to 5 | see their own status |
| `internal/budget`, `internal/evals`, `internal/telemetry`, `internal/llm/probe`; `secrets`/`deps` in `internal/checks` | Slice 2: budgets and pause, evaluation metrics and `routing_outcomes`, local telemetry sink, tool-call probe and cheap-model selection, the two extra checks | built and tested on Linux; wired by `internal/core/ext_economics.go`; Windows unproven (P2); see [`token-control.md`](token-control.md), [`model-routing.md`](model-routing.md), [`evals.md`](evals.md) "As built" sections |

## 3. The core loop (Slice 1D), as implemented

1. A human creates a task (`BACKLOG`). Run input is `{taskId}`.
2. `prepare`: create the worktree and branch idempotently, write the generated instruction files (git-ignored), move the task to `IN_PROGRESS`.
3. Before every agent attempt the engine records a **safe point** (hidden commit under `refs/drydock/safe/<task>/<n>`), stored on the attempt's `node_executions` row.
4. `work`: a fresh agent session runs one turn in the worktree. First pass gets the task spec; later passes get only the latest failure or feedback. The watchdog observes the stream and may nudge or pause.
5. `verify`: build, test, lint (as detected) run for real; evidence is recorded per check; failure routes back to `work` (bounded loop), exhaustion parks a human request.
6. `finalize`: commit, diff and commit evidence, task to `REVIEW`.
7. `review`: a different agent instance reads the diff plus evidence and returns `SHIP` or `REVISE`. An unparseable answer is `REVISE`.
8. `approve`: a `review` request carries the summary to the human. Reject loops back with their feedback; approve proceeds.
9. `merge`: only a `human:` approval is accepted; merge strategy and branch from the project; conflicts park a question; success completes the task and removes the worktree if clean.

Every step is a checkpoint; kill the app at any point and the run resumes without re-running finished nodes. At start `Recover()` takes every run whose lease has expired (or that has none); `Engine.WatchRecover` then checks again every third of the lease TTL (every 10 s with the default 30 s TTL), so a run whose dead owner's lease was still valid at start (killed and restarted within the TTL) is taken over as soon as that lease expires (`internal/workflow/engine.go`, started in `internal/core/core.go`).

## 4. Build order

Built first, in this order (each with tests): storage, events, process manager, workflow engine, watchdog, agent contracts + Claude adapter + runtime, tasks, git, checks, pipeline, context. Windows-only proof of the process manager, tree-sitter CGO, Wails packaging and the installer is the next gate (P2: blocked on GitHub Actions for this repository).

## 5. Explicitly not built yet (2026-10-10)

Everything below is absent or only partly done; items marked built elsewhere in this document are tested on Linux only.

- **Windows proof (P2):** since the repository went public (2026-10-10), every push runs the whole Go and UI test suites, the Wails build, the tree-sitter CGO job and the Windows verification bundle on GitHub-hosted `windows-latest` (workflows `ci` and `verify`; `windows-probes` when its paths change), and they pass: process-tree cleanup with Job Objects, a terminal through ConPTY, Credential Manager, SQLite, git, MCP, the pipeline, the Wails build with a WebView2 window, the NSIS installer and tree-sitter with CGO. Not yet done on Windows: a person using the app window, the updater flow, and Docker Desktop for the per-task sandbox. The verification bundle and self-hosted runner support (P32, [`windows-runner.md`](windows-runner.md)) remain for a machine of the owner's.
- **Built 2026-10-10, Linux only:** the per-task sandbox for Claude workers (P30, [`sandbox.md`](sandbox.md); other CLIs cannot work in a sandboxed project yet); CI as a hard merge gate with branch protection read and never bypassed (P31; the real-GitHub merge test exists and has not run); the contract suite against the real CLIs (P33, [`contract-tests.md`](contract-tests.md)); the head-to-head trial against plain Claude Code, B1 (P34, [`evals.md`](evals.md) section 13; B0 is not built).
- Claude Design variants of the spec-gated templates. (Built since the first version of this list, Linux only: the Crew HQ design (decision P64: one top bar with five tabs, hub pills, the Overview, agents as dots), the repo map (tree-sitter with CGO, decision P62: `internal/repomap`, the `repo_map` tool, a section of AGENTS.md and the Repo map page), the bottom dock with terminal, diff, logs and activity, the command palette, slash commands, attachments, the workflow graph view, the Providers detail panel with quotas, project export/import, the support bundle, the backups screen with its schedule, the license screen, and PR-flow (`-pr`) variants of the templates; see `ui/app/screens` and `internal/core/ext_packaging*.go`.)
- **Routing:** every turn of an agent is routed (decision P63) when its human listed the models the router may choose between (Team page): task work and reviews, its room replies, and the template role turns (the pm's spec, the tests written first, the architect's design, the security review). Each kind of turn is a run kind of its own (`room_reply`, `spec`, `tests`, `design`, `security_review`) with a task class and weight the rules see; the router picks among exactly that agent's models and never moves the turn to another CLI or login (`internal/core/turnrouting.go`), with the same budgets, escalation, fallback chain (P53) and decision records as work turns. An agent with no list runs its configured model, recorded as a `static` decision; a person's chat keeps the model the person chose. Not built: LLM tiebreak; pre-call budget reservation and wall-time ceilings; the OTLP exporter (the local telemetry sink exists, D12); the B0 baseline arm of the evaluation (B1 is built, P34).
- **Agents:** only Claude Code gets an MCP config (its own workspace tools and the MCP servers the human connects in the library, puts into a project and grants to the agent, decisions P15); Codex, Gemini, OpenCode and Goose run without the workspace tools unless the human allows the gateway for that CLI (decisions P20, unverified with any real CLI), Antigravity cannot be given it; no real prompt turn has been run for any ACP agent (no credentials here); Antigravity and the custom-CLI dialect are not implemented; upstream docs servers are not passed to agent runs; OAuth sign-in exists for servers connected in the library (decisions P22) but has only run against a fake provider; the full per-role MCP policy table.
- **Integrations:** every vendor shape is a fixture written from documentation; no live Linear/Notion/Sentry/Aikido call has been made. OAuth sign-in to the Linear, Notion and Sentry hosted MCP servers is built but has only run against a fake provider ([`integrations.md`](integrations.md), "As built: OAuth sign-in"); Aikido is token-only.
- **Packs and requirements:** PDF/DOCX parsing, `git_docs` sources, model-synthesized answers.

## 6. Locked decisions

D1 to D14 and P1 to P11 in [`decisions.md`](decisions.md). In particular: Claude Code is driven headlessly from the user's own login (D1); OpenCode and Codex are both cheap-tier-capable and Codex needs a Responses-API provider (D3); OpenRouter is model-agnostic with `private` enforced per request (D4); NSIS user-scope installer and Wails' built-in updater with its generic endpoint provider (D8); process trees via kill-on-close Jobs (D7).

## Open questions / unverified

- Windows by hand (P2): the app window used by a person, the updater, and Docker Desktop for the sandbox. The automated parts run on GitHub's Windows runners on every push (section 5).
- Checkpoint size growth and compaction thresholds are unmeasured.
- Whether `modernc.org/sqlite` on Windows (built with `SQLITE_MUTEX_NOOP`) needs additional serialization beyond the single writer connection.
