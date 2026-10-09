# Working in this repository

Drydock is a Windows-only desktop app (Go core, React/TypeScript UI). The brief is `docs/brief.md`, decisions are in
`docs/decisions.md`. Linux is only the development host.

- Build and test: `GOFLAGS=-mod=mod go test ./...`, `go vet ./...` and `GOOS=windows go vet ./...`; UI: `cd ui && npm test && npm run build`;
  browser tests: `cd ui && npx playwright test` (needs `npm run build` first, the harness serves `ui/dist`).
- Nothing is marked done with a failing test. Every change ships with tests. No mocks of agents except labelled test doubles.
- Never upload source code; no secrets in logs, events or the database.

## Software versions (standing rule)

The page **Drydock Software Versions** (https://claude.ai/artifact/9MAaRtjEkYQJ9L8xHQeQE5) must always show what Drydock really uses.
Whenever you add, remove or update a Go module, npm package, GitHub Action, agent CLI or ACP bridge pin, or a tool, or implement a
feature that starts using a new piece of software, follow `docs/software-versions.md` ("The rule"): update
`docs/software-notes.json`, re-run `go run ./cmd/drydock-audit`, commit `docs/software-versions.json`, refresh the Artifact, and report
which version each piece landed on. The aim is to stay as current as safely possible: prefer the newest stable release and write down
a reason (`hold`) when something is deliberately behind.
