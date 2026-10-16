# Software versions

Drydock stays as current as it safely can. Every piece of third-party software it uses is measured against the newest release and
shown on one page: **Drydock Software Versions** (an Artifact): https://claude.ai/artifact/9MAaRtjEkYQJ9L8xHQeQE5

## What is measured

`cmd/drydock-audit` reads what the repository really pins and asks the registries what is newest. Nothing is typed by hand except
the notes (below).

| Part | Read from | Newest from |
|---|---|---|
| Go toolchain | `go.mod` (`go` directive), `go env GOVERSION` | the Go module proxy (`golang.org/toolchain`) |
| Go libraries, direct and indirect | `go list -m -u -json all` | the Go module proxy |
| Which direct libraries product code imports | `go list` on the Linux and Windows builds | |
| UI libraries and tools | `ui/package.json`, `ui/package-lock.json` | `npm outdated` |
| Node and npm | the installed ones | `npm view node@22`, `npm view npm` |
| GitHub Actions in CI | `.github/workflows/*.yml` | git tags of each action |
| Agent CLIs and ACP bridges | `internal/agents/manifests/cli/*.yaml` | `npm view` |
| Tools you install (gh, task, osv-scanner, go-licenses, govulncheck, wrangler) | the version `docs/software-notes.json` records as documented | the Go proxy or npm |
| Security advisories | | `govulncheck` (Go; pinned to a release, the same one `.github/workflows/security.yml` runs), `npm audit` (UI) |
| Documentation that names a different version than the code | `docs/decisions.md` | |

The result is `docs/software-versions.json` (committed). `docs/software-notes.json` is hand-written: what each piece is for, why
it is deliberately held back when it is, and the documented versions of the tools you install. The tool prints any chosen piece
that has no note, so a new dependency cannot slip in undocumented.

## The rule

Whenever a change adds, removes or updates software (a Go module, an npm package, a CI action, an agent CLI or bridge pin, a tool),
or implements a feature that starts using a new piece of software:

1. Add or update its entry in `docs/software-notes.json` (what it is for; why it is held back, if it is).
2. Commit the change itself first, then run `go run ./cmd/drydock-audit -artifact-dir <scratch dir>` (or `task audit`) and commit the new
   `docs/software-versions.json`. The report names the commit it measured; measured with uncommitted changes it says `<commit>-dirty`, because
   the versions it lists are then not the ones that commit pins.
3. Run `go run ./cmd/drydock-notices` and commit `THIRD_PARTY_NOTICES.md` when it changes: its Go and UI tables are generated from
   the report you just made, the UI lock file and the Go module cache (`go mod download` first), and a test fails when they disagree.
4. Refresh the Artifact (below) and say in the change what version each piece landed on.
5. Update `docs/decisions.md` section 2 if it names a version that changed (the audit lists any that now disagree).

## When a lookup fails

The audit never turns a failure into good news (audit 2026-10-16, L51). A program that ends with a failure code is an error even
when it printed something first; `npm outdated` and `npm audit` exit 1 to say they found something, which is not a failure, but
exit 1 with nothing listed is. A package or module whose newest version could not be looked up is shown as **unknown**, not as
current, and the failed lookup is listed in the report (`lookupsFailed`). A scanner whose output stops part-way or is not what it
should be (`govulncheck` JSON cut off, `npm audit` answering with an error object) reports "did not run", with the reason, instead
of "0 vulnerabilities". The report is written either way, and the exit status is 2 when any lookup failed, so a measurement made
without the network cannot pass for a complete one. `govulncheck` is run at the release pinned in `cmd/drydock-audit/vuln.go`
(and in the workflow), recorded in `docs/software-notes.json` as the version used.

## Refreshing the Artifact

The page's own source is `docs/software-versions-page.html` (publish it as `files/index.html` of the Artifact only when the design changes).

The page is the Artifact type "Dashboard" over four data files. To refresh, after running the audit with `-artifact-dir`:

1. For each of `items.json`, `vulns.json`, `drift.json`, `meta.json`: upload it to the Artifact as an asset
   (Artifact tool, publish with `url`, `asset: true`, `file_path`).
2. For each dataset (`datasets/items`, `vulns`, `drift`, `meta`): `update` its `source.url` to the new `/_blob/...` address and
   `updated` to `{at, by}`. Delete the old assets afterwards.
3. The page itself (`files/index.html`) changes only when the design does.

A dataset with no rows (`vulns.json`, `drift.json` on a clean audit) is written as one placeholder row `{"none": "yes"}`, because the
Dashboard type refuses a file it can read no rows from; the page leaves that row out and shows its empty-state sentence.

## Libraries only in the module graph

A Go library that another library's go.mod lists, but of which no package is compiled into Drydock or its tests, is shown under
"Only in the module graph". Its version is the one the libraries that list it ask for, so it is not chased (decision P23): the audit
gives each one that remark by itself (no hand-written hold needed), says where its version comes from (`module graph (not in go.mod)`
when go.mod does not require it), and leaves it out of the summary of how far behind Drydock is (`counts.graphOnly` says how many there are).

## Known limits

- The "newest" column is the newest stable release. Whether it is safe to move is a decision; `hold` in the notes records the
  reasons something is deliberately behind (for example the Wails beta, or the Node types that must follow the Node line).
- Security advisories come from two databases (the Go vulnerability database and npm's advisory feed). They do not cover the agent
  CLIs, Node itself or the tools you install.
- Tools such as `gh` and `osv-scanner` are not pinned anywhere in the repository; the version shown is what the documentation says
  the project was written against, not what is installed on your machine.
- Windows-only code is covered by the import check (the Windows build is listed), but nothing here has been run on Windows.
