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
| Tools you install (gh, task, go-licenses, govulncheck, wrangler) | the version `docs/software-notes.json` records as documented | the Go proxy or npm |
| Tools the Windows download carries (osv-scanner, Opengrep, the TurboQuant llama.cpp build) | the pins of `packaging/bundle.json` (decision P77), not a copy in the notes | git tags; for the TurboQuant fork, GitHub's release listing read with the app's own picker (the newest release with the Windows build and a digest), or its release tags (`bNNNN-X.Y.Z`) when the listing cannot be read, which the item's note then says |
| Agent CLIs not on npm (the Antigravity CLI) | the manifest's recommended version | none: no feed; the row says to check by hand |
| Copied into Drydock: the built-in Opengrep rule sets, the Simple Icons and LobeHub logo paths | the commit in `internal/scan/builtin_rules.go`; the versions in `ui/app/ui/brand-logo.tsx` | the repository's newest commit; npm |
| Skills the prebuilt agents name at a commit | `internal/skills/registry.yaml` | the registry repository's newest commit |
| Container images Drydock pulls or builds on (the CI runner container, the sandbox's suggested images) | the image names in the Go code (`internal/runners/launch.go`, `internal/sandbox/config.go`) | Docker's official-images library: still published, and the newest Debian or Ubuntu release |
| Vendor APIs and protocols (MCP revision, Linear schema, Notion-Version, X-GitHub-Api-Version, anthropic-version) | the code that sends them | MCP: the specification's dated tags; Linear: `@linear/sdk` on npm; the others publish versions only on a web page, so their rows say where to look |
| What CI workflows install at a version (`npm install -g`, `npx -y`, `go install`, `go run`) | `.github/workflows/*.yml` | npm or the Go proxy; a pin that differs from the manifests says so, and the Phase 0 probes carry a hold (they keep the versions they verified) |
| Git, and Semgrep OSS (development only, checks the built-in rules) | the versions `docs/software-notes.json` records | their git tags |
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
(and in the workflow), recorded in `docs/software-notes.json` as the version used, and built with the Go the project is built with
(`GOTOOLCHAIN` is set to `go env GOVERSION`: a scanner built with an older Go cannot load packages that need a newer one). Where
`vuln.go.dev` cannot be reached, `-vulndb https://storage.googleapis.com/go-vulndb` (or `DRYDOCK_AUDIT_VULNDB`) reads the same
database from its storage bucket, and `file:///path` reads a mirror; without it the scan says "did not run" and the exit status is 2.

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

## Not tracked, and why (the sweep of 2026-10-20)

A sweep of the code, CI and packaging for software Drydock uses found these, which the page leaves out on purpose:

- **Parts of Windows or of a person's PC**: `wsl.exe` and the tools inside a distro, PowerShell and `cmd.exe` (the terminal and
  sign-ins), `fsutil` (Dev Drive), `nvidia-smi` (looked for, never run), the WebView2 runtime (evergreen), the default browser,
  and the helpers Git for Windows starts (ssh, credential helpers). Windows updates them; Drydock pins none.
- **What a CI runner image brings**: `windows-latest` and `ubuntu-latest` are floating labels; MinGW gcc, Git for Windows,
  PowerShell 7 and Chocolatey come with the image. The Playwright browser follows `@playwright/test` and the CodeQL bundle
  follows `github/codeql-action`, both tracked. NSIS is installed unpinned by the Phase 0 probe job only.
- **Content a person chooses**: models from the Hugging Face Hub, the skill registries Drydock syncs at their newest commit (by
  design: the Library shows what is new), and container images a project's own sandbox configuration names.
- **Services without a version**: the hosted MCP servers of `internal/mcp/catalog.yaml` (Microsoft Learn, Cloudflare docs and API,
  GitHub, Context7, Linear, Notion, Sentry), and the Sentry (`/api/0`) and Aikido (`/api/public/v1`) APIs, whose version is only
  in the path.
- **Not part of the product**: the Phase 0 probe modules (`evidence/phase0/**/go.mod`, a record of what was verified), the
  starter template's packages (`testdata/starter-cloudflare-saas`), the trial fixtures, and Dependabot (a GitHub service).

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
