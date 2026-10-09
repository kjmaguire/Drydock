# Shell commands for Claude workers: what would need your approval

Status: **proposal, not built.** Nothing here is enabled. It needs the owner's decision (section 5).

## 1. Where things stand

A Claude worker can read, search and edit files in its own worktree. It cannot run shell commands: in headless mode a command that
is not on the allowed list is refused. So the worker cannot run the tests while it works. Drydock runs the project's checks itself
after every worker turn (`internal/checks`: build, test, lint and, where present, typecheck) and sends the failures back as
feedback. That is correct and safe, but each failure costs a whole extra turn (tokens, time).

Letting the worker run the same commands inside its turn would catch most of those failures earlier.

## 2. What the commands are

Drydock already detects them per project (`checks.Detect`), so the list is generated, not typed by hand:

| Project has | Commands (exact) |
|---|---|
| `go.mod` | `go build ./...`, `go test ./...`, `go vet ./...` |
| `package.json` scripts | `npm run build --silent`, `npm run test --silent`, `npm run lint --silent`, `npm run typecheck --silent` (only the scripts that exist) |
| a Taskfile with the standard targets | `task build`, `task test`, `task lint`, `task typecheck` (only those that exist, and only if `task` is installed) |

These are the same commands Drydock runs after the turn, in the same worktree.

## 3. The tiers

**Tier 1: exactly those commands, nothing appended.** A rule matches the whole command line, so `go test ./... -exec "..."` does not
match. Recommended.

**Tier 2: command prefixes, for targeted runs** (`go test ./pkg -run TestX`, `npx vitest run file`). More useful and easier to abuse:
- `go test`/`go build`/`go vet` accept `-exec`, `-toolexec` and `-vettool`, which run any program; a prefix rule allows them.
- `npx <name>` downloads and runs a package from the network if it is not installed. Never allow `npx` as a prefix.
- Allowing `gofmt -l` is harmless; `gofmt -w` edits files (fine inside the worktree, the editor tool can already do that).

**Tier 3: read-only git** (`git status`, `git diff`, `git log`): convenient, but the worker can already read files with its own tools.
Optional.

## 4. What stays refused whatever you choose

- Anything that changes git history or the branch: `git commit`, `push`, `checkout`, `reset`, `rebase`, `merge`, `stash`, `clean`, `config`.
  Drydock owns git; the worker only edits files.
- Anything that installs or fetches code: `npm install`/`ci`, `go get`/`install`, `pip`, `npx <anything>`, `curl`, `wget`, `Invoke-WebRequest`.
- Anything that runs arbitrary code: `go run`, `go generate`, `sh -c`, `bash -c`, `cmd /c`, `powershell`, `python -c`.
- Anything outside the worktree: absolute paths, `..`, other drives.
- The reviewer role gets none of this. A reviewer reads.

## 5. The risk you are accepting, and what to decide

Running tests means running code the agent wrote (and `npm run test` runs whatever the agent put in `package.json`), with your user's
rights. There is no operating-system sandbox on Windows in v1 (docs/security.md). That exposure already exists, because Drydock's own
checks run the same commands after the turn; allowing them during the turn changes when they run, not what runs. What it does add is
an agent that can iterate on its own output, and the chance that a model passes a flag you did not foresee (Tier 2 only).

Proposal:
1. **Tier 1 only**, as a per-project switch in Development settings ("Let workers run this project's checks"), **off by default**,
   workers only, commands generated from the detected checks.
2. Not Tier 2 for now.
3. Tier 3 only if you want it.

Not verified: that Claude Code's exact-command rules match `npm run test --silent` the same way on Windows (`npm` is `npm.cmd`); that
would be checked on the first Windows run before this is turned on there.
