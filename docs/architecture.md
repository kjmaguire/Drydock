# Drydock: architecture

Scope: the recommended architecture, how the built pieces fit, what is built first, what is explicitly not built yet, and which decisions are locked. It describes the code as it exists in this repository; where a subsystem is specified but not built it is marked **not built**. Owner decisions in [`decisions.md`](decisions.md) win over the brief ([`brief.md`](brief.md)). Evidence for choices is in [`phase0.md`](phase0.md).

## 1. Shape

One Windows desktop process: a Go core with no UI dependency, wrapped by a Wails v3 shell, driving real agent CLI processes, each in its own Git worktree.

```
 Wails v3 UI (React + TypeScript)                                       [built; unproven on Windows]
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
| `internal/providers`, `internal/llm`, `internal/router` | provider registry, quota ledger, LLM client + cache + data_class guard, static router + CEL | built, tested on Linux |
| `internal/security` | approvals with action-hash binding, path guard, command analyzer, secret scanning, audit chain | built, tested on Linux |
| `internal/mcp` | gateway, workspace server, catalog | built, tested on Linux |
| `app/`, `ui/`, `main.go` | Wails shell (compiles for Windows, never run) and React UI (130 unit + 16 browser tests against the real core) | built; Windows unproven |
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

Every step is a checkpoint; kill the app at any point and `Recover()` resumes it without re-running finished nodes.

## 4. Build order

Built first, in this order (each with tests): storage, events, process manager, workflow engine, watchdog, agent contracts + Claude adapter + runtime, tasks, git, checks, pipeline, context. Windows-only proof of the process manager, tree-sitter CGO, Wails packaging and the installer is the next gate (P2: blocked on GitHub Actions for this repository).

## 5. Explicitly not built yet (2026-10-08)

Everything below is absent or only partly done; items marked built elsewhere in this document are tested on Linux only.

- **Windows proof (P2):** nothing has run on Windows. Wails shell and WebView2, NSIS installer and updater flow, Job Object tree kill, ConPTY terminal, Credential Manager, tree-sitter CGO job.
- **Repo map** (tree-sitter, gated by D5), the bottom dock (terminal/diff/logs), command palette, slash commands, attachments, the workflow graph view, a Providers detail panel (quotas), project export/import, the support bundle, scheduled backups UI, license screen, Claude Design and PR-flow variants of the spec-gated templates.
- **Routing:** agent turns are not routed through the Model Router (`core.agentConfig` assigns each agent's configured model; decisions are recorded as `static`); LLM tiebreak; pre-call budget reservation and wall-time ceilings; the OTLP exporter (the local telemetry sink exists, D12); the B0/B1 baseline arms of the evaluation.
- **Agents:** only Claude Code gets an MCP config (its own workspace tools and the MCP servers the human connects in the library, puts into a project and grants to the agent, decisions P15); Codex, Gemini, OpenCode and Goose run without the workspace tools unless the human allows the gateway for that CLI (decisions P20, unverified with any real CLI), Antigravity cannot be given it; no real prompt turn has been run for any ACP agent (no credentials here); Antigravity and the custom-CLI dialect are not implemented; upstream docs servers are not passed to agent runs; OAuth flows; the full per-role MCP policy table.
- **Integrations:** every vendor shape is a fixture written from documentation; no live Linear/Notion/Sentry/Aikido call has been made; OAuth is not built.
- **Packs and requirements:** PDF/DOCX parsing, `git_docs` sources, model-synthesized answers.

## 6. Locked decisions

D1 to D14 and P1 to P11 in [`decisions.md`](decisions.md). In particular: Claude Code is driven headlessly from the user's own login (D1); OpenCode and Codex are both cheap-tier-capable and Codex needs a Responses-API provider (D3); OpenRouter is model-agnostic with `private` enforced per request (D4); NSIS user-scope installer and Wails' built-in updater with its generic endpoint provider (D8); process trees via kill-on-close Jobs (D7).

## Open questions / unverified

- Everything Windows (P2). Until the `windows-probes` workflow runs, treat `internal/proc` tree-kill, ConPTY, Credential Manager, NSIS and tree-sitter CGO as unproven.
- Checkpoint size growth and compaction thresholds are unmeasured.
- Whether `modernc.org/sqlite` on Windows (built with `SQLITE_MUTEX_NOOP`) needs additional serialization beyond the single writer connection.
