# Drydock: the security scan

One button scans a project's working tree with three engines at once and shows one report (decision P44). The page is
**Security** in a project's sidebar. The same scan also reads **any folder on this computer** without a project, a
repository just downloaded or about to be imported (decision P61, section 8): the workspace page **Security scan**
(`/security-scan`), opened from Home's "Security scan" tile or the workspace sidebar. The Go side is `internal/scan`
(engines, runner, SARIF reader) and `internal/core/ext_security.go` (the UI methods, events and storage); the UI's shared
part is `ui/app/screens/security/scan-panel.tsx`. This document says what is built, what each engine does, what the report
never carries, and what is not built.

## 1. What happens when you press Scan

1. The core checks the project has a repository folder on this computer (or, for a folder scan, that the folder may be read:
   section 8) and that no scan of it is running.
2. Three engines start together over that folder (`scan.Start`):

   | Engine | Tool | What it looks for | How it runs |
   |---|---|---|---|
   | Secrets | gitleaks (compiled into Drydock, the default rules) | keys, tokens and passwords in files | Drydock walks the tree itself and runs the rules over every text file, so it can count files and show "1,204 of 5,330 files". It does not enter `.git`, `node_modules` or `.drydock-worktrees`, skips symlinks, files over 2 MiB and files with a NUL byte in their first 8 KiB. |
   | Dependencies | osv-scanner (comes installed with Drydock, section 5) | packages in lockfiles and manifests with known vulnerabilities | `osv-scanner scan source --format json --no-call-analysis=all -r .`, read with the same parser as the `deps` check (`checks.ParseOSV`): only package names, versions and advisory ids are kept. Unless the tool is run offline it sends package names to osv.dev; nothing else leaves the machine. Call analysis is off (P61): it is the one part of osv-scanner that runs other programs (Go's, on by default, runs the go toolchain over the repository). |
   | Code | Opengrep (comes installed with Drydock) with the built-in rules, and a folder of your own if you add one | code patterns the rules mark as unsafe | `opengrep scan -f <built-in rules> [-f <your rules folder>] --sarif-output=<temp file> -q [--no-git-ignore] .` (`--no-git-ignore` for a folder scan, section 8); the SARIF 2.1.0 file is read (`scan.ParseSARIF`): rule id, level, the message's first line, file and line. |

3. Each engine ends done, skipped (the tool is not installed, there are no rules, osv-scanner found no lockfile),
   errored (the tool broke or ran past the ten-minute cap) or cancelled. A skipped or errored engine says why on the page and
   in the report's summary. **It is never a pass.** The secrets engine counts the folders and files it could not read
   (permissions) and says so on its row and in the summary (`unread`); a tree it cannot read at all is an error. A cancelled
   engine keeps the count it reached, never "all files". A report with no findings reads "Nothing found." only when every
   engine ran; otherwise the page names the engines that did not.
4. The report is stored (`security_scans`, migration 00116) once the scan ends; the history on the page lists the last ones
   and any of them can be opened again. A report the database refuses is kept in memory with `storeError` set (the page
   says it was not kept) until the next scan of the project, so the scan the person watched is never replaced by an older one.
   A folder scan's report is stored in the same table under the folder's key (`project_id` = `folder:<full path>`, lower
   case on Windows; no project id has that form), so it belongs to no project and its history is the folder's.

The external tools run through the Process Manager with the scrubbed tool environment (`proc.ToolEnv`, docs/security.md s8)
plus the variables the person passes to project commands: a tool that reads the whole project gets none of Drydock's own
environment.

## 2. What the report holds, and what it never holds

A finding is: engine, severity, rule, a short title, a file and a line (and for a dependency the package, version and
advisory ids). Secrets are high; a vulnerable dependency is high; a code finding takes its SARIF level (error high, warning
medium, note low) or, when the rule gives a `security-severity` score, that score (9+ critical, 7+ high, 4+ medium).

Never in a report, an event, a log or the database:

- **the secret's value**. The secrets engine drops gitleaks' matched text before the finding leaves its function; the page
  says "The value is not shown".
- **the vulnerable line of code**. Only the file and line are kept.
- **a tool's raw output**. A reason is the tool's first stderr line, control characters removed, capped, and passed through
  the app's secret scanner (`security.Scanner.Redact`); every title is too. The whole report is redacted once more as a
  JSON document before it is stored.

A report keeps at most 2,000 findings; past that the rest are counted (`counts`) and `truncated` is set.

## 3. Events and progress

`security.scan.started`, `security.scan.engine` (one per engine state change, naming the engine), `security.scan.finished`
(with `stored`: whether the report reached the database) and `security.settings` are published with the project id, in that
order (started is published before the scan can report anything). A folder scan's events carry no project id and name the
folder (`folder`); the workspace rules folder's `security.settings` carries `workspace: true`. Progress counters (files scanned) are not events: the page reads `security.scan.get`
every half second while a scan runs and re-reads on the events. A scan in progress lives in memory; closing the app cancels
it (a cancelled scan is stored as cancelled).

## 4. The UI methods

| Method | Does |
|---|---|
| `security.tools {projectId}` (or `{}`: the folder scans') | what this machine has: gitleaks (built in, its version), osv-scanner and Opengrep (path, `--version` and how each was found: `chosen`, `path` or `folder`, or an install hint; a chosen file that is gone is named), the project's rules folder and whether it holds `.yaml`/`.yml` files, and `ready` (every engine can run). |
| `security.settings.set {projectId, rulesDir}` or `{workspace: true, rulesDir}` | stores the project's rules folder, or the one folder scans use (a full path: Opengrep runs in the repository and Drydock elsewhere, so a relative path would name two folders). Only a person may: the folder decides which rules read the whole project. The page's **Choose folder...** asks the window's folder dialog (`fs.pickFolder`, P58) and then makes this call with the folder chosen; cancelling changes nothing. |
| `security.tool.set {projectId, tool, path}` | stores the program a person chose for `osv-scanner` or `opengrep`, for the whole workspace (a program is on the computer, not in a project); `""` goes back to finding it. The path must be a full path to an existing file (an `.exe` on Windows); quotes from Explorer's "Copy as path" are removed. Only a person may: the program reads the whole project. The programs are probed (`--version`) once per change: the answer goes to the caller and, for the next three seconds, to every page that re-reads on the `security.settings` event, so a program whose first run is slow is paid for once; a program deleted or replaced meanwhile is probed again at once. |
| `security.tool.choose {projectId, tool}` | opens the window's own Open dialog (the desktop app, through Wails; filter `*.exe`) and stores the file chosen; cancelling changes nothing. Where the window has no dialog (the browser in development) it is refused and the page offers only the path field (`security.tools` says `canChoose`). |
| `security.tool.install {projectId, tool}` | Drydock installs the tool itself (decision P66): in the background it reads the tool's GitHub release listing (no sign-in), picks the newest stable release that carries the Windows program with a SHA-256 digest, downloads it from that repository only, refuses it unless the digest matches, and puts the program (unpacked from Opengrep's archive with its DLLs, or the exe renamed) under `<data>\tools\<tool>\<tag>\`. The same call is **Update**: with the installed release already the newest it ends "already the newest release (vX)". Only a person may. The answer, and every later `security.tools`, carries the install's state in `job` (`running` with the phase and the bytes so far, `done`, `failed` with the reason, `cancelled`); each change is announced on `security.settings` (`tool`, `install`). `security.tools` says `canInstall` (Windows). |
| `security.tool.install.cancel {projectId, tool}` | stops a running install; nothing is left half installed (a download is checked and placed as a whole). |
| `security.tool.remove {projectId, tool}` | deletes the release Drydock installed; the tool is looked for on PATH again. Only a person may. |
| `security.scan.start {projectId}` or `{folder}` | starts a scan; refused while one of the same project or folder runs, when the project has no repository, for a folder section 8 refuses, and for anyone but a person. |
| `security.scan.cancel {projectId}` or `{folder}` | cancels the running scan. |
| `security.scan.get {projectId}` or `{folder}` | the running scan's snapshot, else the last stored report, else null. |
| `security.scan.list {projectId, limit}` or `{folder, limit}` | the history, newest first, without findings (a folder scan's names its `folder`). |
| `security.scan.folders {limit}` | the recent folder scans of every folder: those running first, then the stored ones, newest first. |
| `security.scan.report {id}` | a stored report. |

## 5. Installing the tools (Windows)

Drydock comes with both tools and with rules (decision P77). The Windows download carries a `tools` folder beside
`drydock.exe` with the pinned releases of `packaging/bundle.json` (built by `cmd/drydock-bundle` in CI, each asset checked
against the SHA-256 digest GitHub recorded, listed in `tools/bundle.json`). At its first start Drydock installs them from that
folder with the same check and placement as **Install** below, so a scan works with nothing downloaded. A tool the person
removed (`<tool>.removed` beside the installs) is not installed again from the folder.

Drydock looks for newer releases of an installed tool three minutes after it starts and then twice a day; the Security page's
**Check for updates** looks now. A newer release is named on the tool's row ("v1.32.0 is out") and its button reads **Update to
v1.32.0**. Nothing is installed without the person pressing it. On the Security page each external tool's row has **Install**
(or **Update** once Drydock installed it) and, for an installed one, **Remove** (decision P66):

- **Install** reads the tool's own GitHub release listing (`google/osv-scanner`, `opengrep/opengrep`; no sign-in, the
  repositories are public), takes the newest stable release (no draft, no pre-release) that carries the Windows program with
  the SHA-256 digest GitHub recorded at upload, downloads that asset from that repository only, checks it, and refuses it when
  the digest does not match (the download is deleted). The program goes to `%LOCALAPPDATA%\Drydock\tools\<tool>\<tag>\`
  (the app's data folder): `osv-scanner_windows_amd64.exe` renamed `osv-scanner.exe`; Opengrep's `opengrep_windows_x86.zip`
  (2.0 and later; `opengrep_windows_x86.exe` before) unpacked with the runners' guarded unzip so `opengrep.exe` keeps its DLLs
  beside it. No install script from the internet runs (Opengrep's `install.ps1` is not used). The row shows the download's
  progress; the install runs in the background and survives leaving the page.
- **Update** is the same call: it looks the newest release up and installs it when it is newer than the one installed,
  otherwise says "already the newest release (vX)".
- **Remove** deletes Drydock's copy; the tool is then looked for on PATH again.
- Without Drydock's install, the page still says how to get each tool by hand: `osv-scanner_windows_amd64.exe` from
  https://github.com/google/osv-scanner/releases named `osv-scanner.exe` on your PATH (or `go install
  github.com/google/osv-scanner/v2/cmd/osv-scanner@latest`); `opengrep_windows_x86.zip` from
  https://github.com/opengrep/opengrep/releases with `opengrep.exe` and its DLLs kept together in a folder on your PATH (or
  the repository's `install.ps1`). Opengrep is LGPL-2.1 and ships a native Windows binary; that is why it was chosen over
  Semgrep (a Python distribution with partial Windows support, and registry rules under a licence that does not allow use
  inside a commercial product).
- **Finding them** (decision P55): a program a person chose on the Security page ("Choose file..." or its full path) comes
  first, then the release Drydock installed, then the PATH Drydock started with, then the folders where installers put
  programs: on Windows the user's and the machine's PATH as they are now in the registry (so a tool installed while Drydock
  runs is found without restarting it), WinGet's `%LOCALAPPDATA%\Microsoft\WinGet\Links`, Scoop's
  `%USERPROFILE%\scoop\shims`, `%USERPROFILE%\go\bin` (and `%GOBIN%`) and Chocolatey's `%ProgramData%\chocolatey\bin`.
  The panel says which: "Chosen file", "Installed by Drydock (vX) at", "Found at", or "Found (not on the PATH Drydock
  started with) at".
- **Built-in rules** (P77): the rules in `internal/scan/rules` are compiled into Drydock, written once per version to
  `<data>\rules\builtin-<digest>\` (rule files and each set's `LICENSE` only), and given to Opengrep first on every scan.
  They are Drydock's own rules (34, for JavaScript and TypeScript, Python, Go, Java and C#, each with a test fixture under
  `internal/scan/testdata/rules`) and two MIT sets copied at a fixed commit: `dgryski/semgrep-go` (66 rules, Go) and
  `0xdea/semgrep-rules` (49 rules, C and C++). `internal/scan/rules/README.md` names the commits and why the other public sets
  are not built in: `opengrep-rules` carries the Commons Clause and says it is for research, testing and benchmarking (P44),
  Semgrep's own set is under the Semgrep Rules License, and Trail of Bits' set is AGPL-3.0. The page lists each built-in set
  with its rules, languages and licence.
- **Your own rules** (optional): a folder of Semgrep-syntax rule files (`.yaml` or `.yml`, found through its sub-folders;
  `.git` is skipped) runs as well as the built-in rules. The person chooses it under **Add a folder of your own rules** with
  **Choose folder...** (the window's own folder dialog, shown in the desktop app, saved at once) or types its full path and
  presses Save (P74). A folder that cannot be used is left out of the scan and the page says why.
- **Where to get more rules** (P74) is a help on the page, folded away. It is text and links only: nothing is fetched or run
  by the page, the links open in the person's browser, and it recommends no set. It lists public repositories of Semgrep-syntax
  rules with what each one's own licence file or README said on 2026-10-16, a Git command that clones one into a folder
  (`git clone -c core.longpaths=true --depth 1 https://github.com/OWNER/REPOSITORY C:\tools\REPOSITORY`), and one rule of
  Drydock's own to try the Code engine with: saved as a `.yaml` file in an empty folder that is then chosen, it flags each
  `fmt.Println` call in the Go files Opengrep scans. A test (`TestTheDemoRuleOnTheSecurityPageIsAValidRuleFile`) reads the rule
  from the page's source and requires a valid rule file that Drydock accepts as a rules folder. What was read:

  | Repository | What its licence file or README said | Rules for |
  |---|---|---|
  | `opengrep/opengrep-rules` | `LICENSE`: LGPL 2.1 with the "Commons Clause" License Condition v1.0 (no right to Sell the rules: practising the rights to provide third parties, for a fee or other consideration, a product or service whose value derives entirely or substantially from them). README: "These rules are intended for research, testing & benchmarking." | a fork of Semgrep's rules |
  | `semgrep/semgrep-rules` | `LICENSE`: "Semgrep Rules License v1.0. For more details, visit https://semgrep.dev/legal/rules-license". The terms page could not be reached from the development host, so they are linked, not restated. | Semgrep's own rules |
  | `trailofbits/semgrep-rules` | `LICENSE`: GNU Affero General Public License, version 3 | Trail of Bits' public rules |
  | `0xdea/semgrep-rules` | `LICENSE`: MIT | C (and sometimes C++) |
  | `dgryski/semgrep-go` | `LICENSE`: MIT | Go |

  A licence can change, and what applies depends on how the rules are used (your own code, inside a company, in a product): the
  page tells the person to read it. The Code engine is skipped for want of rules only when the built-in rules could not be
  written. Its reason, when it is skipped because the rules folder is not usable, ends
  with where to choose rules and where to get them (`rulesHelp`), because a report is read away from the field that mends it;
  the scan's summary keeps only the reason's first sentence.

## 6. Tests

- `internal/scan`: the secrets engine finds a planted key in a text file and not in `.git`, a binary or an oversized file,
  and the report's JSON never carries the value; the external engines run through the labelled stand-ins
  (`internal/scan/scantest`: a stand-in osv-scanner that prints the documented JSON for one vulnerable package, a stand-in
  Opengrep that writes a SARIF 2.1.0 log with two results) and their running state is visible; a tool that breaks is errored;
  cancel ends the scan cancelled; no rules folder skips the Code engine; `Detect` names what is installed; SARIF severities
  and paths.
- `internal/core/ext_security_test.go`: the methods above end to end against a core: tools, the rules folder, start, a second
  start refused, the events, the stored report without the secret, history, cancel, no repository.
- Installing the tools (P66): `internal/scan/install_test.go` (the newest checked release is picked, drafts, pre-releases,
  releases without the Windows asset and assets without a digest passed over, Opengrep's archive preferred to its exe; the
  program placed, renamed or unpacked with its DLLs, the replaced release removed, a mismatching digest refused, a download
  from another repository refused, Remove) against a PROTOCOL FIXTURE of GitHub's releases API (`scantest.ReleaseFixture`);
  `internal/core/ext_security_install_test.go` (Install runs in the background and the tools say so, the scan runs the
  installed program, Update with nothing newer says so and with a newer release installs it, the settings events, a chosen
  program still first, Remove, cancel while downloading, a second Install or a Remove while one runs refused, a failed install
  named and retried); the browser tests of the tools panel; `ui/e2e/security.spec.ts` (Install, Update, the scan with the
  installed tool, Remove, against the harness's fixture release and stand-in program).
- `ui/e2e/security.spec.ts`: the page against the real core and the stand-in tools: Scan, the three engines done, the
  findings, the key's value absent from the page, the history; and from Home's tile, a folder that is not a project scanned
  the same way, its report reopened from the recent folder scans, no project made.
- The rules folder's help and picker (P74): `internal/scan` (`TestEveryUnusableRulesFolderSkipsTheCodeEngineWithWhereToChoose`:
  each way the folder can be unusable ends the Code engine skipped with where to choose rules; the summary keeps the first
  sentence; `TestTheDemoRuleOnTheSecurityPageIsAValidRuleFile`), `rules-sources.test.ts`, the browser tests of both Security
  pages (Choose folder... saves at once and cancelling saves nothing, the workspace page's saves the folder scans' folder and
  leaves the folder to scan alone, only the typed path where the window has no dialog; the help's sets, links, command and
  rule, open while the folder has no rules and a choice of the person standing) and `ui/e2e/security.spec.ts` (the folder
  dialog's stand-in, the help, the skipped reason on the page).
- `internal/scan/sarif_real_test.go` reads the SARIF of a real run of Semgrep OSS 1.182.0 (`testdata/semgrep-oss-1.182.0.sarif`): a result
  with no `level` takes its rule's default, the matched code in `region.snippet` is in no finding.
- Folder scans (P61): `internal/core/ext_security_folder_test.go` (a folder scan runs the three engines with the stand-ins,
  carries no project in its report, events and history, is found by folder and across folders, the workspace rules folder
  is not a project's, refused folders, cancel), `ext_security_internal_test.go` (only a person starts a scan of either
  kind), `internal/scan` (osv-scanner's and Opengrep's arguments), and the browser tests of
  `workspace-security-screen.test.browser.tsx`, Home and the sidebar.

## 7. Not built

- The scan is not a workflow check node yet (the `secrets` and `deps` checks on a task's diff are; docs/brief.md 13a).
- No MCP tool exposes the scan to agents; findings are for the person.
- No Trivy, zizmor, Scorecard or SBOM engine; the SARIF reader is the seam for them.
- The scan runs where Drydock runs (Windows); a repository inside a WSL distro is scanned through its `\\wsl$` path by the
  Windows tools, which is slow and was not measured (decision P41).
- A real Opengrep run was not captured in this repository: its SARIF reader is written from the SARIF 2.1.0 specification
  and proven against the stand-in and against the SARIF of a real run of Semgrep OSS 1.182.0, the engine Opengrep was forked
  from (`internal/scan/testdata/semgrep-oss-1.182.0.sarif`, made over the Security page's demo rule: two findings in two
  files, none for `fmt.Printf`, `fmt.Sprint` or a Python file); osv-scanner 2.7.0 was run for real (its JSON is `internal/checks/testdata/osv-scanner-2.7.0.json`).
  `--no-git-ignore` (P61) is written from Opengrep's documented flags, not run against the real binary here.
- The installer (P66) was not run against the real GitHub releases: the asset names come from the tools' own release
  configuration (osv-scanner's `.goreleaser.yml`, Opengrep's `install.ps1`) and the digest from GitHub's documented API
  field; the fixture answers in that shape. The first Install on a PC proves it. Drydock does not look for new releases by
  itself (Update does, on request) and installs the x86-64 build only (neither tool publishes a Windows ARM64 build).
- A folder is chosen with "Choose folder..." (the window's own dialog through `fs.pickFolder`, decision P58; shown only in the desktop app) or typed or pasted.
- No rules are shipped: P44 stands. A starter set of Drydock's own would reverse it and is the owner's call; the one rule on the
  page (P74) is help text that the person saves themselves, not something Drydock applies.
- Not verified (P74): the page's rule was run through Semgrep OSS 1.182.0 (two findings, none wrong) but not through Opengrep
  itself, and the Git command was not run against a real clone (no Opengrep binary can be run on the development host);
  the rule has the shape of the one in Opengrep's README and a test keeps it a valid rule file. The licence wording was read from the repositories on 2026-10-16, and the Semgrep Rules License's terms page could not
  be read there. Windows: how the Choose folder... button sits in the rules form and the folder dialog it opens.
- The `deps` check on a task's diff (`internal/checks`) keeps its own osv-scanner invocation; P61 changed only the scan's.

## 8. Scanning a folder that is not a project (decision P61)

The workspace page **Security scan** (`/security-scan`; Home's "Security scan" tile, the workspace sidebar, Ctrl+K) scans a
folder by its full path, or one of the projects (its working tree, its rules folder and its history, as on its own page).
It shows the same engines, progress, findings and tools (`ScanPanel`), and as history the recent folder scans of every folder;
picking one shows that folder again.

**Which folders.** A full path (Explorer's quotes are removed, a trailing separator is the same folder) to a folder that is
there. Refused: a relative path, a file, a whole drive, Drydock's own data folder, a folder inside it (the database, the
task worktrees: a project's are scanned as the project) and a folder that holds it. Symbolic links are resolved before the
checks, and the engines read the resolved folder.

**Rules folder.** Folder scans use the workspace's rules folder (`security.settings.set {workspace: true}`; the harness's
`ScanRulesDir` until one is set); each project keeps its own. The tools' programs (P55) are the workspace's already.

**It only reads files.** Nothing in this path runs the folder's own code:

- the secrets engine is Drydock walking the folder and running gitleaks' default rules in process; it reads no file of the
  repository as configuration (no `.gitleaks.toml`, no `.gitleaksignore`);
- osv-scanner parses lockfiles and manifests itself; `--no-call-analysis=all` switches off call analysis, the part that runs
  the go toolchain (Go's is on by default and may fetch a toolchain or modules the repository names) or cargo. It does not
  run npm, pip, Maven or any package manager or script of the repository. Its network use is the advisory lookup at
  osv.dev (and, for some manifests, the public registries' dependency data); nothing of the code is sent;
- Opengrep parses the files with the rules folder's patterns and runs none of them. Inside a git repository it lists the
  files with `git ls-files`; a folder scan passes `--no-git-ignore`, so git never runs in a folder whose `.git/config` came
  from somewhere else (a `core.fsmonitor` there would name a program for git to run). A project's own scan keeps git's view.

Two of the tools still read a file of the repository that can narrow what they report: osv-scanner's `osv-scanner.toml`
(ignored advisories) and Opengrep's `.semgrepignore` (skipped paths). The page says so; the secrets engine follows no file of
the repository.

The tools run as before: through the Process Manager, with the scrubbed tool environment, in the folder, ten minutes at most.
Only a person starts a folder scan (`human:` identity, like a project's).
