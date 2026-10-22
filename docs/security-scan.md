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
   | Code | Opengrep (comes installed with Drydock) with the built-in rules | code patterns the rules mark as unsafe | `opengrep scan -f <built-in rules> --sarif-output=<temp file> -q [--no-git-ignore] .` (`--no-git-ignore` for a folder scan, section 8); the SARIF 2.1.0 file is read (`scan.ParseSARIF`): rule id, level, the message's first line, file and line. |

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
- **the vulnerable line of code**. Only the file and line are kept, and the finding's fingerprint, a hash (section 9). The
  code around a finding is read from the folder when a person asks for it, secrets masked, and is never stored.
- **a tool's raw output**. A reason is the tool's first stderr line, control characters removed, capped, and passed through
  the app's secret scanner (`security.Scanner.Redact`); every title is too. The whole report is redacted once more as a
  JSON document before it is stored.

A report keeps at most 2,000 findings; past that the rest are counted (`counts`) and `truncated` is set.

## 3. Events and progress

`security.scan.started`, `security.scan.engine` (one per engine state change, naming the engine), `security.scan.finished`
(with `stored`: whether the report reached the database) and `security.settings` are published with the project id, in that
order (started is published before the scan can report anything). A folder scan's events carry no project id and name the
folder (`folder`). Progress counters (files scanned) are not events: the page reads `security.scan.get`
every half second while a scan runs and re-reads on the events. A scan in progress lives in memory; closing the app cancels
it (a cancelled scan is stored as cancelled).

## 4. The UI methods

| Method | Does |
|---|---|
| `security.tools {projectId}` (or `{}`: the folder scans') | what this machine has: gitleaks (built in, its version), osv-scanner and Opengrep (path, `--version` and how each was found: `installed`, `path` or `folder`, or an install hint), the built-in rules, and `ready` (every engine can run). |
| `security.tool.install {projectId, tool}` | Drydock installs the tool itself (decision P66): in the background it reads the tool's GitHub release listing (no sign-in), picks the newest stable release that carries the Windows program with a SHA-256 digest, downloads it from that repository only, refuses it unless the digest matches, and puts the program (unpacked from Opengrep's archive with its DLLs, or the exe renamed) under `<data>\tools\<tool>\<tag>\`. The same call is **Update**: with the installed release already the newest it ends "already the newest release (vX)". Only a person may. The answer, and every later `security.tools`, carries the install's state in `job` (`running` with the phase and the bytes so far, `done`, `failed` with the reason, `cancelled`); each change is announced on `security.settings` (`tool`, `install`). `security.tools` says `canInstall` (Windows). |
| `security.tool.install.cancel {projectId, tool}` | stops a running install; nothing is left half installed (a download is checked and placed as a whole). |
| `security.scan.start {projectId}` or `{folder}` | starts a scan; refused while one of the same project or folder runs, when the project has no repository, for a folder section 8 refuses, and for anyone but a person. |
| `security.scan.cancel {projectId}` or `{folder}` | cancels the running scan. |
| `security.scan.get {projectId}` or `{folder}` | the running scan's snapshot, else the last stored report, else null. |
| `security.scan.list {projectId, limit}` or `{folder, limit}` | the history, newest first, without findings (a folder scan's names its `folder`). |
| `security.scan.folders {limit}` | the recent folder scans of every folder: those running first, then the stored ones, newest first. |
| `security.scan.report {id}` | a stored report. |

## 5. Installing the tools (Windows)

Drydock comes with both tools and with rules (decision P77), and keeps them itself (decision P95): the Security page has no
tools section, only each engine's readiness. The Windows download carries a `tools` folder beside `drydock.exe` with the
pinned releases of `packaging/bundle.json` (built by `cmd/drydock-bundle`, each asset checked against the SHA-256 digest GitHub
recorded, listed in `tools/bundle.json`). At its first start Drydock installs them from that folder with the same check and
placement as the install below, so a scan works with nothing downloaded. With `AutoSetup` (the desktop app), a tool the folder
did not carry, or whose copy failed its check, is downloaded instead, and so is every tool when the folder's `bundle.json`
cannot be read. Earlier versions let a person remove a tool and kept it out with a `<tool>.removed` marker; nothing can install
it from the page now, so the marker is ignored (and deleted when the tool is installed again). A rules folder or program a
person chose in an earlier version (`security:rules:*`, `security:workspace-rules`, `security:toolpath:*`) is deleted at start
for the same reason.

Drydock looks for newer releases of an installed tool three minutes after it starts and then twice a day, and installs a newer
one itself under `AutoSetup` (`security.tools.checkUpdates` installs it too), but only a release whose tag is newer than the
one installed: an older release GitHub lists as latest is never installed. The install (`security.tool.install`, which the
page no longer offers; Drydock runs the same job) works like this:

- **Install** reads the tool's own GitHub release listing (`google/osv-scanner`, `opengrep/opengrep`; no sign-in, the
  repositories are public), takes the newest stable release (no draft, no pre-release) that carries the Windows program with
  the SHA-256 digest GitHub recorded at upload, downloads that asset from that repository only, checks it, and refuses it when
  the digest does not match (the download is deleted). The program goes to `%LOCALAPPDATA%\Drydock\tools\<tool>\<tag>\`
  (the app's data folder): `osv-scanner_windows_amd64.exe` renamed `osv-scanner.exe`; Opengrep's `opengrep_windows_x86.zip`
  (2.0 and later; `opengrep_windows_x86.exe` before) unpacked with the runners' guarded unzip so `opengrep.exe` keeps its DLLs
  beside it. No install script from the internet runs (Opengrep's `install.ps1` is not used). The row shows the download's
  progress; the install runs in the background and survives leaving the page. Before the release becomes the one in use it
  is run the way a scan runs it (`scan.SelfCheck`: Opengrep with the built-in rules over one Go file, osv-scanner over a
  folder with a `go.mod`); a release that fails, such as one that dropped an option Drydock passes, is deleted and the one in
  use stays ("Opengrep vX failed Drydock's test scan, so it is not used: ...").
- **Update** is the same call: it looks the newest release up and installs it when it is newer than the one installed,
  otherwise says "already the newest release (vX)".
- Without Drydock's install, the page still says how to get each tool by hand: `osv-scanner_windows_amd64.exe` from
  https://github.com/google/osv-scanner/releases named `osv-scanner.exe` on your PATH (or `go install
  github.com/google/osv-scanner/v2/cmd/osv-scanner@latest`); `opengrep_windows_x86.zip` from
  https://github.com/opengrep/opengrep/releases with `opengrep.exe` and its DLLs kept together in a folder on your PATH (or
  the repository's `install.ps1`). Opengrep is LGPL-2.1 and ships a native Windows binary; that is why it was chosen over
  Semgrep (a Python distribution with partial Windows support, and registry rules under a licence that does not allow use
  inside a commercial product).
- **Finding them** (decision P55): the release Drydock installed comes first, then the PATH Drydock started with, then the folders where installers put
  programs: on Windows the user's and the machine's PATH as they are now in the registry (so a tool installed while Drydock
  runs is found without restarting it), WinGet's `%LOCALAPPDATA%\Microsoft\WinGet\Links`, Scoop's
  `%USERPROFILE%\scoop\shims`, `%USERPROFILE%\go\bin` (and `%GOBIN%`) and Chocolatey's `%ProgramData%\chocolatey\bin`.
  `security.tools` says which.
- **Built-in rules** (P77): the rules in `internal/scan/rules` are compiled into Drydock, written once per version to
  `<data>\rules\builtin-<digest>\` (rule files and each set's `LICENSE` only), and given to Opengrep first on every scan.
  They are Drydock's own rules (34, for JavaScript and TypeScript, Python, Go, Java and C#, each with a test fixture under
  `internal/scan/testdata/rules`) and two MIT sets copied at a fixed commit: `dgryski/semgrep-go` (66 rules, Go) and
  `0xdea/semgrep-rules` (49 rules, C and C++). `internal/scan/rules/README.md` names the commits and why the other public sets
  are not built in: `opengrep-rules` carries the Commons Clause and says it is for research, testing and benchmarking (P44),
  Semgrep's own set is under the Semgrep Rules License, and Trail of Bits' set is AGPL-3.0. The page lists each built-in set
  with its rules, languages and licence.
- **Rules of your own** were a folder a person could add on the Security page (P74), with a help on where to get more. The
  page no longer has it (decision P95); the Code engine runs the built-in rules. The e2e harness still gives Opengrep a folder
  of extra rules through `core.Config.ScanRulesDir`.

## 6. Tests

- `internal/scan`: the secrets engine finds a planted key in a text file and not in `.git`, a binary or an oversized file,
  and the report's JSON never carries the value; the external engines run through the labelled stand-ins
  (`internal/scan/scantest`: a stand-in osv-scanner that prints the documented JSON for one vulnerable package, a stand-in
  Opengrep that writes a SARIF 2.1.0 log with two results) and their running state is visible; a tool that breaks is errored;
  cancel ends the scan cancelled; no rules folder skips the Code engine; `Detect` names what is installed; SARIF severities
  and paths.
- `internal/core/ext_security_test.go`: the methods above end to end against a core: tools, the built-in rules, start, a second
  start refused, the events, the stored report without the secret, history, cancel, no repository.
- Installing the tools (P66): `internal/scan/install_test.go` (the newest checked release is picked, drafts, pre-releases,
  releases without the Windows asset and assets without a digest passed over, Opengrep's archive preferred to its exe; the
  program placed, renamed or unpacked with its DLLs, the replaced release removed, a mismatching digest refused, a download
  from another repository refused, Remove; release tags ordered by their numbers) against a PROTOCOL FIXTURE of GitHub's
  releases API (`scantest.ReleaseFixture`); `internal/scan/selfcheck_test.go` (the test scan passes the stand-ins and fails a
  program that breaks or no longer knows an option; a release that fails it is not used and the one in use stays);
  `internal/core/ext_security_install_test.go` (Install runs in the background and the tools say so, the scan runs the
  installed program, Update with nothing newer says so and with a newer release installs it, an older "latest" is neither
  installed nor offered, the settings events, cancel while downloading, a second Install while one runs refused, a failed
  install named and retried); `ext_security_tools_test.go` (the choices of an earlier version are forgotten at start and the
  methods that stored them are gone); `ext_bundle_test.go` (AutoSetup with no tools folder and with a broken one, a tool an
  earlier version removed comes back).
- `ui/e2e/security.spec.ts`: the page against the real core and the stand-in tools: Scan, the three engines done, the
  findings, the key's value absent from the page, the history; and from Home's tile, a folder that is not a project scanned
  the same way, its report reopened from the recent folder scans, no project made.
- An unusable rules folder (the harness's): `internal/scan` (`TestEveryUnusableRulesFolderSkipsTheCodeEngineWithWhereToChoose`).
- `internal/scan/sarif_real_test.go` reads the SARIF of a real run of Semgrep OSS 1.182.0 (`testdata/semgrep-oss-1.182.0.sarif`): a result
  with no `level` takes its rule's default, the matched code in `region.snippet` is in no finding.
- Folder scans (P61): `internal/core/ext_security_folder_test.go` (a folder scan runs the three engines with the stand-ins,
  carries no project in its report, events and history, is found by folder and across folders, refused folders, cancel), `ext_security_internal_test.go` (only a person starts a scan of either
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
folder by its full path, or one of the projects (its working tree and its history, as on its own page).
It shows the same engines, progress, findings and tools (`ScanPanel`), and as history the recent folder scans of every folder;
picking one shows that folder again.

**Which folders.** A full path (Explorer's quotes are removed, a trailing separator is the same folder) to a folder that is
there. Refused: a relative path, a file, a whole drive, Drydock's own data folder, a folder inside it (the database, the
task worktrees: a project's are scanned as the project) and a folder that holds it. Symbolic links are resolved before the
checks, and the engines read the resolved folder.

**Rules.** Folder scans run the built-in rules, as a project's scans do (the harness adds `ScanRulesDir`).

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

## 9. One finding: fingerprint, dismiss, the code around it, a task (decision P97)

**Fingerprint.** Every finding has `fingerprint`: 24 hex digits of a SHA-256 over the engine, the rule, the file and a hash of
the finding's line text, whitespace collapsed and redacted by the app's scanner. The secrets engine hashes the line with the
secret replaced by the scanner's mask (`[REDACTED:<rule>]`), so the value is never part of it (another value on the same line
has the same fingerprint); a code finding's lines (at most 20) are read from the file at scan time; a dependency finding
hashes `package@version` and its advisory ids. A finding without text hashes its line number. A built-in rule is named by its
own id, not the folder Opengrep read it from, so a new version of the rules keeps the fingerprints. Two findings with the
same fingerprint in one report get `-2`, `-3` in report order. The fingerprint stays the same in later scans while the line
reads the same, wherever it moves.

**Dismiss.** `security.finding.dismiss {projectId | folder, fingerprint, reason}` (`not_real`, `accepted_risk`,
`false_positive`; the finding must be in the target's last scan) and `security.finding.restore {projectId | folder,
fingerprint}` keep a row per target and fingerprint (`security_dismissals`, migration 00132; deleted with the project, not
exported). Every report that is read (`security.scan.get`, `security.scan.report`) and every finished scan before it is stored
marks the dismissed findings (`dismissed`, `dismissedBy`, `dismissedAt`, `dismissReason`) and leaves them out of `counts`,
the engines' `findings` and the summary ("1 finding you dismissed is not counted"); `dismissed` on the report and on each
engine counts them. The history (`security.scan.list`) keeps each scan's counts as it was stored. `security.finding.dismissed
{projectId | folder}` lists them, newest first. Only a person dismisses or restores. Events `security.finding.dismissed` and
`security.finding.restored` carry the fingerprint.

**The code around it.** `security.finding.context {scanId, fingerprint | index, around}` reads the finding's file now, from the
folder its scan read (a folder scan's folder must still pass section 8's rules; a project's scan's folder must still be the
project's repository): `around` lines before and after (2 by default, at most 10, 40 lines in all, each at most 400 bytes).
gitleaks' rules run over the whole file and every secret they find is masked, a match over several lines masked whole; a
secrets finding whose secret is no longer where it was has its lines masked whole; every line goes through the app's scanner.
`changed` says the line no longer reads as at the scan. A path that leaves the folder (`..`, an absolute path, a link that
leads out) is refused; a binary file, one over 2 MiB, one that is gone and a dependency finding answer with a `note` and no
lines. It is read-only.

**Plain words.** The same answer carries `advice {meaning, steps, refs}`: for a secret by its gitleaks rule family (AWS,
GitHub, GitLab, private key, generic API key, JWT, Slack, Stripe, OpenAI, Anthropic, Google Cloud, Azure, npm, PyPI, Hugging
Face; others by gitleaks' own description); for a dependency the advisories and the version to upgrade to (`fixed`: the lowest
version above the installed one that fixes every advisory osv.dev names a fix for, read from the records' affected ranges;
`unfixed` lists the advisories without one); for a built-in code rule its own message (first sentence: the meaning; the rest:
the steps) and its CWE; for any other rule the finding's title.

**A task.** `security.finding.task {scanId, fingerprint | index, assignee?}` makes a BACKLOG task in the scan's project: the
title names what to do and where ("Remove the secret in notes.txt:3 and revoke it", "Upgrade x from 1.0 to 1.2 (known
vulnerabilities)"), the description the rule, engine, severity, file and line, the advice, and the finding; never a secret.
`issueRef` is `scan:<scanId>:<fingerprint>` (an imported task: no scheduler moves it on by itself). With `assignee` the task is
given to that agent with another agent of the team as reviewer (a reviewer-role one first, never the lead). A finding with a
task that is not failed or cancelled gets no second one. A folder scan has no project and is refused with the reason. Only a
person makes one.
