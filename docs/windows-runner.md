# Proving Drydock on Windows

Drydock is Windows-only. **Since 2026-10-10 the repository is public, so GitHub-hosted Windows runners cost nothing** and
every push runs `.github/workflows/ci.yml` (vet and every Go test, the UI tests, the app build) and `verify.yml` (the
verification bundle below) on `windows-latest`; `windows-probes.yml` runs the Phase 0 probes when they change or by hand.
That is the first way to get the proof. The other two need the owner's PC:

1. **Run the verification bundle by hand.** A zip of Drydock's own tests compiled for Windows, plus a program that runs them
   and writes a report. Nothing to install except Git; no Go needed. See [By hand](#by-hand-the-verification-bundle).
2. **Register a self-hosted runner on the PC** and point the workflows at it with one repository variable. Then every
   workflow (CI, the Phase 0 probes, and `verify.yml`, which builds and runs the bundle) runs there on each push.
   See [A self-hosted runner](#a-self-hosted-runner-on-your-pc).

**Do not attach a self-hosted runner to a public repository.** A pull request from anyone's fork runs the workflows, so it
could run its own code on the runner's PC (GitHub's own advice). With the repository public, use the GitHub-hosted runners;
keep option 2 for a private repository only.

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

## A self-hosted runner on your PC

### What it needs on the PC

- Windows 10 or 11, x64.
- **Git for Windows** (https://git-scm.com), installed for all users.
- **PowerShell 7** (`winget install --id Microsoft.PowerShell`): the workflows use `shell: pwsh`.
- **Go and Node** are installed for each job by `actions/setup-go` (the version in `go.mod`, today 1.27.2) and
  `actions/setup-node` (Node 24 for the UI, 22 for the probes) into the runner's own tool folder. Install them yourself only
  if you also want to run things by hand.
- Optional: **gh** (the GitHub CLI; tests that look for it skip without it), **gcc** (MinGW; the tree-sitter jobs
  `repomap-cgo` and `treesitter` fail without a C compiler), **Chocolatey** with administrator rights (the NSIS installer
  step of the probes' `wails` job).
- A few GB of free disk for the Go build cache and `node_modules`.

### Register the runner

1. Create a **standard (not administrator) Windows account** just for the runner, for example `drydock-ci`
   (Settings > Accounts > Other users > Add account).
2. On GitHub: the repository's **Settings > Actions > Runners > New self-hosted runner**, choose **Windows** and **x64**.
   GitHub shows the exact PowerShell commands with the current runner version, its checksum and a one-time token. They look
   like this; copy GitHub's own, not these:

   ```powershell
   mkdir C:\actions-runner; cd C:\actions-runner
   Invoke-WebRequest -Uri https://github.com/actions/runner/releases/download/v<version>/actions-runner-win-x64-<version>.zip -OutFile actions-runner-win-x64-<version>.zip
   if((Get-FileHash -Path actions-runner-win-x64-<version>.zip -Algorithm SHA256).Hash.ToUpper() -ne '<checksum>'.ToUpper()){ throw 'Computed checksum did not match' }
   Add-Type -AssemblyName System.IO.Compression.FileSystem ; [System.IO.Compression.ZipFile]::ExtractToDirectory("$PWD/actions-runner-win-x64-<version>.zip", "$PWD")
   ./config.cmd --url https://github.com/kjmaguire/Drydock --token <token>
   ```

   Use a folder outside your own user folder (such as `C:\actions-runner`) and give the runner account full control of it.
3. `config.cmd` asks a few questions. Keep the default runner group and name. **Labels:** the runner always gets
   `self-hosted`, `Windows` and `X64`; you may add one of your own (for example `drydock`). Labels match without regard to
   case. Keep the default work folder `_work`.
4. Choose how it runs:
   - **As a Windows service** (starts with the PC): answer `Y` when asked, and give the runner account (`.\drydock-ci`) and
     its password when asked which account the service should use. Installing the service needs an administrator
     PowerShell once; the service itself runs as the standard account.
   - **Only while you want it:** answer `N`, sign in as the runner account and start `run.cmd` in the runner folder. It
     takes jobs until you close the window.
5. The runner appears as **Idle** under Settings > Actions > Runners.

### Point the workflows at it

Every job in `ci.yml`, `windows-probes.yml` and `verify.yml` has

```yaml
runs-on: ${{ fromJSON(vars.WINDOWS_RUNS_ON || '"windows-latest"') }}
```

so it runs on GitHub's `windows-latest` unless the repository variable `WINDOWS_RUNS_ON` says otherwise. Set it under
**Settings > Secrets and variables > Actions > Variables > New repository variable**: name `WINDOWS_RUNS_ON`, value

```json
["self-hosted","windows","x64"]
```

(add your own label to the list, for example `["self-hosted","windows","x64","drydock"]`, to pick that runner only). With
the GitHub CLI: `gh variable set WINDOWS_RUNS_ON --body '["self-hosted","windows","x64"]'`. Delete the variable to go back
to GitHub-hosted runners. While it is set and the runner is offline, jobs wait in the queue for it.

If GitHub Actions itself is switched off for the repository (Settings > Actions > General), no runner helps; the bundle by
hand still works.

### What runs there

- `ci.yml` on pushes to `main` and on pull requests: `go vet` and `go test ./...` (real agent CLIs are never used there:
  `DRYDOCK_NO_REAL_AGENTS=1`), the tree-sitter CGO probe, the UI tests and build, and the Wails app build.
- `windows-probes.yml` on pushes to `claude/**` that touch the probes, or by hand: it **installs** agent CLIs with
  `npm install -g` and `wails3` with `go install` for the runner account, and NSIS with Chocolatey.
- `verify.yml` on pushes to `main` and `claude/**` (not for documentation-only changes, and only while `WINDOWS_RUNS_ON`
  is set, so it never spends GitHub-hosted minutes by itself), or by hand from the Actions tab (**Run workflow**, with the
  `with_claude` box for the real Claude Code tests): builds the bundle without a Go copy inside and runs `drydock-verify`.
  The results zip is attached to the run as the artifact `drydock-verify-results`.

### Security, in plain words

- **The runner runs whatever is pushed to this repository**, including commits made by Claude sessions, with the rights of
  the account it runs as. Anyone who can push to the repository can run programs on your PC.
- **Use a dedicated standard (non-admin) Windows account** for it. Do not run it as yourself or as an administrator.
- **Keep the repository private.** On a public repository anyone could open a pull request whose code then runs on your PC;
  GitHub advises never to use self-hosted runners with public repositories.
- **A runner on a personal PC can see what its account can see**: files that account can read, your home network, and
  anything signed in under that account. Do not sign the runner account in to Claude Code or `gh` unless you want the tests
  that use them to run (`with_claude` spends that account's Claude usage).
- Workflows can install programs for that account (the probes do). Look at a workflow change before you let it run.
- When you do not need it, stop the service (Services: "GitHub Actions Runner ...") or remove the runner: Settings > Actions
  > Runners > the runner > Remove, then `./config.cmd remove --token <token>` in the runner folder.
