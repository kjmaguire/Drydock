# Proving Drydock on Windows

Drydock is Windows-only. **Since 2026-10-10 the repository is public, so GitHub-hosted Windows runners cost nothing** and
every push runs `.github/workflows/ci.yml` (vet and every Go test, the UI tests, the app build) and `verify.yml` (the
verification bundle below) on `windows-latest`; `windows-probes.yml` runs the Phase 0 probes when they change or by hand.
That is the first way to get the proof. The other is to run the bundle on a PC by hand:

**Run the verification bundle by hand.** A zip of Drydock's own tests compiled for Windows, plus a program that runs them
and writes a report. Nothing to install except Git; no Go needed. See [By hand](#by-hand-the-verification-bundle).

**There is no self-hosted runner, and no variable that could name one** (audit 2026-10-16, M19). A pull request from anyone's
fork runs the workflows, so a self-hosted runner attached to a public repository would run a stranger's code on the PC it
sits on (GitHub's own advice is never to do that). Every job in `ci.yml`, `verify.yml`, `windows-probes.yml` and
`contract.yml` says `runs-on: windows-latest` (or `ubuntu-latest` for the Linux sandbox job) outright, with a read-only token
that the checkout does not keep in the workspace, and a test (`internal/ciconfig`) fails if a workflow names its runner by a
variable, lacks a time limit or a `permissions:` block, or keeps the token. Registering a runner on the owner's PC is a thing
for a private repository only, and Drydock's own CI runners feature (docs/ci-runners.md) does it for those, refusing a
repository that is public.

The `app` job also checks the built `drydock.exe` carries its icon, version information and manifest, and signs it once code
signing is configured: [windows-identity.md](windows-identity.md).

The bundle writes the same report wherever it runs: `drydock-verify-report.md` (for people) and `drydock-verify-report.json`, zipped with every
step's log into `drydock-verify-<date>.zip`.

## What the bundle checks

`drydock-verify --list` prints every step with one sentence on what it proves. In order:

| Step | What it proves |
|---|---|
| `facts` | The machine: Windows name, version and build, CPU, memory, administrator or not, Developer Mode, long paths, git's `core.autocrlf`, and the versions of git, gh, node, npm, claude, docker and go. Never an environment variable's value or a token. |
| `core-smoke` | The real core (`cmd/drydock-e2e`: SQLite, event bus, workflow engine, git worktrees, the project's checks, review and merge) runs a whole task through the same calls the UI makes: project, worker and reviewer (the scripted test-double agent), task, the human review request, approval, `COMPLETED`, and the change merged into the repository. |
| `core-terminal` | A terminal opened through the core starts PowerShell on ConPTY in the project, runs a typed command and returns its output. |
| `core-shutdown` | When the core stops (cleanly, by closing its input), no process it started is left running (checked with `tasklist`). |
| `tests:<package>` | One per package with Windows-specific code or tests, plus storage (SQLite), the Claude adapter (instructions file and argv), CLI detection, git worktrees, process control and the packages with real-Claude tests: that package's own test binary, run in its own folder, with every test's result. |
| `real-claude` | Only with `--with-claude`: the tests that start the real `claude` CLI (usually the npm launcher `claude.cmd` on Windows), including a full worker-and-reviewer task. They use the signed-in account and cost roughly one dollar. |
| `ui` | Only with `--with-ui`, from a source checkout with Node 24: `npm ci`, `npm test`, `npm run build`, `npx playwright test` in `ui/`. |

A step passes, fails, or is skipped, and a skip always says why (for example "not requested" or "no Go is available").
Tests that call the Go toolchain themselves (the core runs `go build`, `go test` and `go vet` on the Go fixture project as
the project's checks) use Go from PATH, or the copy of Go the bundle carries when the PC has none; a bundle built with
`-with-go=false` skips them on a PC without Go, with that reason.

What the bundle does not cover: the Wails window and WebView2, the installer and updater, tree-sitter with CGO, and real
ACP CLIs (those are the jobs of `ci.yml` and `windows-probes.yml`). `internal/terminal` has no tests that build on Windows;
`core-terminal` covers ConPTY end to end instead.

## By hand: the verification bundle

**Build it** on any machine with Go and a checkout (Linux or Windows):

```sh
go run ./cmd/drydock-winbundle -goos windows -out dist-windows/verify
```

This cross-compiles (no CGO) every test binary into `dist-windows/verify/tests/<package>/` next to the `testdata` it reads,
plus `drydock-verify.exe`, `drydock-e2e.exe`, the Go toolchain in `go/` (leave it out with `-with-go=false`), `bundle.json`
and `README-VERIFY.txt`, and zips it all into `dist-windows/drydock-windows-verify.zip`. `dist-windows/` is git-ignored:
bundles are never committed.

**Run it** on the PC (the README in the zip says the same):

1. Right-click the zip and choose **Extract All**. Open the extracted folder.
2. Click the address bar, type `powershell` and press Enter. If Windows refuses to start the programs ("Windows protected
   your PC" or Smart App Control), run `Get-ChildItem -Recurse | Unblock-File` once in that window.
3. Run `.\drydock-verify.exe` (add `--with-claude` to include the real Claude Code tests). It takes about 10 to 30
   minutes (about 6 on the Linux development host).
4. Send back the `drydock-verify-<date>.zip` it names at the end.

It needs Git for Windows on PATH and nothing else. It installs nothing and changes no setting: temporary folders go under
`%TEMP%` and are removed, everything listens on 127.0.0.1 only, and the package tests get CI's git settings (an identity
and `core.autocrlf false`) through a config file in the run's temp folder, named by `GIT_CONFIG_GLOBAL` and including
the user's own global file, rather than by editing any git config; Drydock's own git calls read it too. The
Credential Manager test stores and deletes its own test entry; it leaves Drydock's key-encryption entry alone when a Drydock
build on the PC already has one (that entry decrypts the provider keys the build keeps) and deletes only one it created.

**On the development host** the same bundle can be built for Linux, which is how the orchestration itself is proven:

```sh
go run ./cmd/drydock-winbundle -goos linux -out dist-windows/linux && dist-windows/linux/drydock-verify
DRYDOCK_BUNDLE_E2E=1 go test -run TestBundleEndToEnd -timeout 60m ./internal/winverify   # build, unzip, run, check the report
```
