# Drydock: the security scan

One button scans a project's working tree with three engines at once and shows one report (decision P44). The page is
**Security** in a project's sidebar; the Go side is `internal/scan` (engines, runner, SARIF reader) and
`internal/core/ext_security.go` (the UI methods, events and storage). This document says what is built, what each engine
does, what the report never carries, and what is not built.

## 1. What happens when you press Scan

1. The core checks the project has a repository folder on this computer and that no scan of it is running.
2. Three engines start together over that folder (`scan.Start`):

   | Engine | Tool | What it looks for | How it runs |
   |---|---|---|---|
   | Secrets | gitleaks (compiled into Drydock, the default rules) | keys, tokens and passwords in files | Drydock walks the tree itself and runs the rules over every text file, so it can count files and show "1,204 of 5,330 files". It does not enter `.git`, `node_modules` or `.drydock-worktrees`, skips symlinks, files over 2 MiB and files with a NUL byte in their first 8 KiB. |
   | Dependencies | osv-scanner (you install it) | packages in lockfiles and manifests with known vulnerabilities | `osv-scanner scan source --format json -r .`, read with the same parser as the `deps` check (`checks.ParseOSV`): only package names, versions and advisory ids are kept. Unless the tool is run offline it sends package names to osv.dev; nothing else leaves the machine. |
   | Code | Opengrep (you install it) with a rules folder you choose | code patterns the rules mark as unsafe | `opengrep scan -f <rules folder> --sarif-output=<temp file> --no-progress-bar -q .`; the SARIF 2.1.0 file is read (`scan.ParseSARIF`): rule id, level, the message's first line, file and line. |

3. Each engine ends done, skipped (the tool is not installed, there is no rules folder, osv-scanner found no lockfile),
   errored (the tool broke or ran past the ten-minute cap) or cancelled. A skipped or errored engine says why on the page and
   in the report's summary. **It is never a pass.** The secrets engine counts the folders and files it could not read
   (permissions) and says so on its row and in the summary (`unread`); a tree it cannot read at all is an error. A cancelled
   engine keeps the count it reached, never "all files". A report with no findings reads "Nothing found." only when every
   engine ran; otherwise the page names the engines that did not.
4. The report is stored (`security_scans`, migration 00116) once the scan ends; the history on the page lists the last ones
   and any of them can be opened again. A report the database refuses is kept in memory with `storeError` set (the page
   says it was not kept) until the next scan of the project, so the scan the person watched is never replaced by an older one.

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
order (started is published before the scan can report anything). Progress counters (files scanned) are not events: the page reads `security.scan.get`
every half second while a scan runs and re-reads on the events. A scan in progress lives in memory; closing the app cancels
it (a cancelled scan is stored as cancelled).

## 4. The UI methods

| Method | Does |
|---|---|
| `security.tools {projectId}` | what this machine has: gitleaks (built in, its version), osv-scanner and Opengrep (found on PATH, path and `--version`, or an install hint), the project's rules folder and whether it holds `.yaml`/`.yml` files, and `ready` (every engine can run). |
| `security.settings.set {projectId, rulesDir}` | stores the rules folder (a full path: Opengrep runs in the repository and Drydock elsewhere, so a relative path would name two folders). Only a person may: the folder decides which rules read the whole project. |
| `security.scan.start {projectId}` | starts a scan; refused while one runs or when the project has no repository. |
| `security.scan.cancel {projectId}` | cancels the running scan. |
| `security.scan.get {projectId}` | the running scan's snapshot, else the last stored report, else null. |
| `security.scan.list {projectId, limit}` | the history, newest first, without findings. |
| `security.scan.report {id}` | a stored report. |

## 5. Installing the tools (Windows)

Drydock installs nothing and downloads no rules. The page says what is missing and how to get it:

- **osv-scanner**: `osv-scanner_windows_amd64.exe` from https://github.com/google/osv-scanner/releases, named
  `osv-scanner.exe` on your PATH, or `go install github.com/google/osv-scanner/v2/cmd/osv-scanner@latest`.
- **Opengrep**: `opengrep_windows_x86.exe` from https://github.com/opengrep/opengrep/releases, named `opengrep.exe` on your
  PATH, or the repository's `install.ps1`. Opengrep is LGPL-2.1 and ships a native Windows binary; that is why it was
  chosen over Semgrep (a Python distribution with partial Windows support, and registry rules under a licence that does not
  allow use inside a commercial product).
- **Rules**: a folder of Semgrep-syntax rule files. The `opengrep-rules` repository carries LGPL-2.1 with the Commons Clause
  and says its rules are for research, testing and benchmarking, so Drydock does not fetch it for you: point the scan at
  rules you are licensed to use (your own, or a set whose licence you accepted).

## 6. Tests

- `internal/scan`: the secrets engine finds a planted key in a text file and not in `.git`, a binary or an oversized file,
  and the report's JSON never carries the value; the external engines run through the labelled stand-ins
  (`internal/scan/scantest`: a stand-in osv-scanner that prints the documented JSON for one vulnerable package, a stand-in
  Opengrep that writes a SARIF 2.1.0 log with two results) and their running state is visible; a tool that breaks is errored;
  cancel ends the scan cancelled; no rules folder skips the Code engine; `Detect` names what is installed; SARIF severities
  and paths.
- `internal/core/ext_security_test.go`: the methods above end to end against a core: tools, the rules folder, start, a second
  start refused, the events, the stored report without the secret, history, cancel, no repository.
- `ui/e2e/security.spec.ts`: the page against the real core and the stand-in tools: Scan, the three engines done, the
  findings, the key's value absent from the page, the history.

## 7. Not built

- The scan is not a workflow check node yet (the `secrets` and `deps` checks on a task's diff are; docs/brief.md 13a).
- No MCP tool exposes the scan to agents; findings are for the person.
- No Trivy, zizmor, Scorecard or SBOM engine; the SARIF reader is the seam for them.
- The scan runs where Drydock runs (Windows); a repository inside a WSL distro is scanned through its `\\wsl$` path by the
  Windows tools, which is slow and was not measured (decision P41).
- Real osv-scanner and Opengrep runs were not captured in this repository: the parsers are written from the documented
  shapes and the SARIF 2.1.0 specification, and proven against the stand-ins.
