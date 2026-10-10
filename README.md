# Drydock

A local-first, Windows-only desktop app where you create a team of AI coding agents, watch them collaborate in a shared Engineering Room, and approve the results.

**Status (2026-10-08).** Slices 1A to 5 of the build brief are implemented and tested **on a Linux development host**. **Nothing has run on Windows**: GitHub Actions has not been able to start a Windows runner for this repository (decision P2), so every Windows claim (process-tree kill, ConPTY, Credential Manager, the Wails shell and WebView2, the installer and updater, tree-sitter CGO) is unproven. Read [`docs/decisions.md`](docs/decisions.md) first (owner decisions, process decisions, open items), then [`docs/architecture.md`](docs/architecture.md) and [`docs/phase0.md`](docs/phase0.md).

What exists and is tested here: event bus and SQLite storage, workflow engine with watchdog, agent runtime (Claude Code headless and a generic ACP client), tasks/worktrees/checks, the iterate-until-verified loop with independent review and human approval, GitHub PR flow, Engineering Room, providers/LLM/router, security (approvals, command analyzer, secrets), MCP gateway wired into agent runs, budgets, evals, expert packs, requirements, skills, spec-gated templates, PlanTeam and scheduler, integrations (Linear, Notion, Sentry, Aikido), license tokens, backups, update policy, a Remix 3 + TypeScript UI (`ui/app`, unit tests and browser tests against the real Go core), and the Wails shell (compiles for Windows, not run). The first measured cost per accepted task is in [`docs/evals.md`](docs/evals.md) section 12 (n=1, not a statistic).

Development on a non-Windows host: `go test ./...`, `GOOS=windows go vet ./...`, `cd ui && npm ci && npm test && npm run build && npx playwright test`, and `go run ./cmd/drydock-dev` to run the core with the development HTTP transport.

Proof on Windows without GitHub-hosted runners (a verification bundle run by hand, or a self-hosted runner on your PC): [`docs/windows-runner.md`](docs/windows-runner.md).
