# Drydock

A local-first, Windows-only desktop app where you create a team of AI coding agents, watch them collaborate in a shared Engineering Room, and approve the results.

**Status (2026-10-16).** Slices 1A to 5 of the build brief are implemented and tested, plus a per-task sandbox (P30), CI as a hard merge gate (P31), contract tests against the real agent CLIs (P33) and a head-to-head trial against plain Claude Code (P34). **Windows:** since the repository went public (2026-10-10, decision P2 resolved), every push runs the Go and UI test suites, the Wails build with a WebView2 window, the tree-sitter CGO job, the NSIS installer build and the Windows verification bundle on GitHub-hosted `windows-latest` runners (workflows `ci` and `verify`), which exercise the process-tree kill, ConPTY, the Credential Manager and SQLite there. **Not yet proven on Windows:** a person using the app window, the updater flow (including the restart after an update) and Docker Desktop for the per-task sandbox ([`docs/architecture.md`](docs/architecture.md) section 5; [`docs/windows-runner.md`](docs/windows-runner.md) for running the verification bundle on a PC). Linux is only the development host. Read [`docs/decisions.md`](docs/decisions.md) first (owner decisions, process decisions, open items), then [`docs/architecture.md`](docs/architecture.md) and [`docs/phase0.md`](docs/phase0.md).

What exists and is tested here: event bus and SQLite storage, workflow engine with watchdog, agent runtime (Claude Code headless and a generic ACP client), tasks/worktrees/checks, the iterate-until-verified loop with independent review and human approval, GitHub PR flow, Engineering Room, providers/LLM/router, security (approvals, command analyzer, secrets), MCP gateway wired into agent runs, budgets, evals, expert packs, requirements, skills, spec-gated templates, PlanTeam and scheduler, integrations (Linear, Notion, Sentry, Aikido), license tokens, backups, update policy, a Remix 3 + TypeScript UI (`ui/app`, unit tests and browser tests against the real Go core), and the Wails shell (compiles for Windows, not run). The first measured cost per accepted task is in [`docs/evals.md`](docs/evals.md) section 12 (n=1, not a statistic). A per-task sandbox, which runs a Claude Code worker's commands and the project's checks in a container made for each task, is built and measured on Linux with Docker and real Claude Code, not yet on Windows ([`docs/sandbox.md`](docs/sandbox.md), decision P30).

Development on a non-Windows host: `go test ./...`, `GOOS=windows go vet ./...`, `cd ui && npm ci && npm test && npm run build && npx playwright test`, and `go run ./cmd/drydock-dev` to run the core with the development HTTP transport.

Building `drydock.exe` needs a C compiler, because the repo map's tree-sitter parser is C compiled in with CGO (decision P62):
MinGW gcc on Windows (GitHub's Windows images have it on PATH), the MinGW cross compiler on Linux. Build the UI first
(`cd ui && npm run build`), then, on Windows, `go build -tags production -ldflags "-w -s -H windowsgui" -o drydock.exe .` with
`CGO_ENABLED=1`, or from Linux:
`CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -tags production -ldflags "-w -s -H windowsgui" -o drydock.exe .`
(`task build:windows` does the same). A build with `CGO_ENABLED=0` still works but has no repo map: the `repo_map` tool and the
Repo map page say so.

Proof on Windows by hand (a verification bundle run on a PC; CI itself runs on GitHub-hosted runners only, never a self-hosted one): [`docs/windows-runner.md`](docs/windows-runner.md).
