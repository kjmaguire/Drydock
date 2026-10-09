# Drydock: repository structure

Scope: where things live and the dependency rules between packages. `internal/` packages never import `app/`; subsystems communicate through the small interfaces in their own packages; only `internal/proc` and `app/` contain Windows-specific syscalls (decision P1: the Go core compiles and tests on any host for development, but Windows is the only product target, and the only CI target).

```
/go.mod                  module github.com/kjmaguire/drydock (go 1.26.0)
/main.go, /app/          Wails v3 shell and services — the only place that imports Wails    [not built]
/ui/                     React + TypeScript (Vite): HTTP/SSE dev transport, screens for what the core has [built; tested in Chromium on Linux against the Go core, never run in WebView2]
/Taskfile.yml            go-task: ui:install, ui:build, ui:test, ui:e2e, go:test, build:dev  [written; go-task itself not run here; lint, generate, package tasks not built]
/cmd/
  drydock-dev/           core + HTTP/SSE dev transport serving ui/dist (development tool)
  drydock-e2e/           TEST HARNESS for ui/e2e: real core + scripted fakeagent as the claude adapter; `-licensed` embeds a throwaway license key (never shipped)
/internal/
  ids/                   prefixed random ids
  storage/               SQLite (modernc), writer/reader pools, embedded goose migrations
    migrations/          00001 core · 00002 workflow · 00003 tasks · 00005 memory/context ·
                         00010-19 providers/llm · 00020-29 room · 00030-39 security · 00040-49 mcp
  events/                bus, persistence, replay
  proc/                  process manager (Windows Job Objects; dev-host fallback)
  workflow/              engine, store, graph builders, chunk payload types
  watchdog/              deterministic circuit breaker (observer + middleware)
  agents/                capability matrix; Adapter/Session
    claude/              headless stream-json adapter + parser (testdata = real captured output)
    acp/                 generic ACP client adapter
    manifests/           embedded YAML manifests + compat.yaml
    runtime/             managed sessions, AgentTask node handler
    fakeagent/           scripted real-process test agent (not shipped)
    conformance/         adapter contract suite
  connections/           detection, status, test-connection
  tasks/                 task state machine and storage
  git/                   worktrees, diff, commit, merge, safe points, local ignore
  checks/                build/test/lint/typecheck runner
  pipeline/              workflow nodes and the task-pipeline / iterate-until-verified graphs
  memory/, context/      project memory; context engine, generated instruction files, manifests
  room/                  conversations, messages, presence, turn-taking, exports
  providers/, llm/, router/   provider registry + quota ledger; LLM client; model router
  security/              approvals, path guard, command analysis, secrets, audit
  mcp/                   gateway, workspace server, catalog
  tools/                 build-tag-only dependency pins (not compiled into the product)
/testdata/
  fixtures/gocalc/       small Go module with failing tests (integration fixture)
  starter-cloudflare-saas/   golden starter (agent-side template, decision D6)
/evidence/phase0/        reproducible probes from Phase 0 (separate modules, not product code)
/.github/workflows/      windows-probes.yml (Phase 0 checks), ci.yml (Windows)
/docs/                   design documents; docs/phase0/ holds the raw evidence appendices
```

Dependency direction (arrows = "may import"): `app → everything`; `pipeline → agents/runtime, tasks, git, checks, context, workflow`; `agents/runtime → agents, events, storage, proc, workflow`; `watchdog → workflow`; `workflow → events, storage`; `tasks, memory, git, checks → storage/events/proc`; nothing in `internal/` imports `app/`. Provider-specific logic lives only inside `agents/<adapter>` packages and manifests; no provider-name checks elsewhere.

## Open questions / unverified

- `ui/` status: built against the development HTTP/SSE transport and a Wails transport written from the Wails v3.0.0-beta.28 source. Screens cover Slices 1 to 5 and the packaging features (license and read-only banner, backups with staged restore, project export/import, support bundle, an honest Updates note) as far as the core exposes them. 218 vitest tests and 27 Playwright e2e tests against the real Go core (scripted fake agent and in-memory credential store as the only test doubles) pass on Linux/Chromium. Nothing has run in WebView2 or on Windows. Monaco, xterm, React Flow, cmdk and TanStack Virtual are not used. The layout of `app/` is specified in [`ui.md`](ui.md) section 11.
- Whether the Wails module (beta) should live in the root module or its own is undecided; the root module is assumed.
