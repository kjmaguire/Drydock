# Drydock: Windows packaging, updates, licensing, data ownership

Scope: how Drydock is built, installed, signed, updated, licensed, backed up, exported and supported on Windows 10/11 (brief section 21 "Commercial packaging", section 26; decision D8). It covers the NSIS user-scope installer produced by `wails3 package`, the WebView2 bootstrapper, signing options and the SmartScreen reality, the built-in Wails updater with a custom Provider and Ed25519-signed manifests, channels, release notes, rollback, data ownership (one SQLite file plus a `runs/` directory), project export/import, scheduled backups, support bundles with scrubbing, consent-based diagnostics, the offline Ed25519 license token scheme, onboarding in under ten minutes, Defender false-positive handling, a release checklist, and the list of things that can only be proven on a Windows machine. Evidence comes from `phase0/tooling-integrations.md` section 4 and `phase0/go-platform.md` section 1 (read from Wails `v3.0.0-beta.28` source on Linux). **Nothing in this document has been built, signed, installed or run on Windows**; GitHub Actions is currently unavailable for the private repository (P2), so no release pipeline has executed. Items not in the brief, decisions or appendices are marked **proposal** or **unverified**.

## 1. Decisions in force

| Source | Rule |
|---|---|
| D8 | NSIS installer with `INSTALL_SCOPE=user`; no MSIX or WiX in v1. Updates via Wails v3 built-in updater (`app.Updater`) with a custom `Provider` on Drydock's own release feed (not GitHub Releases), `Channel` `stable` or `beta`, SHA-256 digests, Ed25519-signed manifest with the public key pinned in the app, release notes in the built-in update window, previous executable kept for rollback. Signing: Azure Artifact Signing or an OV certificate, decided at company-account creation; `wails3 tool sign` (signtool) until then. |
| Brief 21 | Code-signed installer, stable and beta channels with rollback, offline Ed25519 license tokens with grace period and trial, expired license = read-only and never deletes data, export/import, scheduled local backups, support bundle, diagnostics off by default, onboarding under ten minutes. |
| Brief 0, 16 | No cloud component; never upload user source code automatically. Windows-only; no macOS/Linux targets. |
| P2 | Windows CI is unavailable; every Windows claim here is unproven until it runs. |
| D1, D2 | First-run notices about Claude (own login only) and Antigravity/Gemini terms ship in the product; Claude subscription support does not ship commercially before written confirmation from Anthropic or counsel (decisions section 4, open item 4). |

## 2. Build and installer

- **Toolchain:** Go `1.26.0` (go.mod floor from sqlite, goose, otel, mvdan/sh, x/sys). Wails `v3.0.0-beta.28` pinned; the installer template and updater below are that version's behavior and may change between betas.
- **Build:** `wails3 package` runs the Taskfile `package` task selecting `FORMAT` (default `nsis`). Build flags in the Wails Windows Taskfile: `-tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui"`, `CGO_ENABLED=0` by default. A 10.4 MB hello-app exe was produced with `CGO_ENABLED=0` on Linux; the real app size is unmeasured.
- **CGO tension (D5):** the tree-sitter repo map requires CGO (28.5 MB Zig-built exe with ten grammars was never executed). Wails' default and the Defender-risk analysis assume `CGO_ENABLED=0`. Until the Windows CI job for D5 is green the repo map stays behind a build tag and release builds are CGO-free. If the repo map later ships, the release build needs a MinGW toolchain and a fresh Defender/SmartScreen check; whether CGO binaries draw more detections is **unverified** (no source found).
- **Installer:** NSIS via `makensis`, template `project.nsi.tmpl`, output `build/windows/nsis/<App>-installer.exe`. With `INSTALL_SCOPE=user` the Taskfile passes `-DWAILS_INSTALL_SCOPE=user -DREQUEST_EXECUTION_LEVEL=user`: no administrator rights, no Program Files. (The template default is `machine`; Drydock overrides it.) Install directory under the user profile: exact default path is **unverified** until the installer is run.
- **WebView2:** `wails3 generate webview2bootstrapper` embeds the roughly 2 MB Evergreen bootstrapper, run silently by the installer (`/silent /install`). Evergreen runtime is preinstalled on Windows 11 and on the vast majority of Windows 10 machines, so the bootstrapper mostly no-ops. An offline machine without WebView2 cannot install it from the bootstrapper: the installer shows a clear error with the fixed-version/offline-installer option (**proposal**).
- **Uninstaller:** removes program files and shortcuts, leaves app data (section 6) by default, with an unchecked option "also delete my Drydock data". Consistent with "never deletes data".
- **Single instance:** enable the Windows single-instance support (`single_instance_windows.go` exists) because SQLite has one writer and `run_leases` assume one engine instance; the updater helper also relies on the old process exiting.
- **Notices:** `THIRD_PARTY_NOTICES.md` (generated with `google/go-licenses/v2` plus hand-maintained entries) ships in the install directory and is linked from Settings > About. Cloudflare docs excerpts are CC BY 4.0 and need attribution if redistributed; the MCP go-sdk's mixed MIT/Apache-2.0 license needs legal sign-off before the first release (decisions section 2).
- **No bundled third-party CLIs:** `gh`, `osv-scanner`, agent CLIs, Node are user-installed Connections (D9, brief 5: nothing downloaded on the user's behalf). This also avoids inheriting their Defender false positives (cli/cli issue #13306 reported a Trojan:Script/Wacatac flag on `gh`, SEARCH-only).

## 3. Signing and SmartScreen reality

| Option | Cost (Microsoft table) | Facts | Fit |
|---|---|---|---|
| OV code-signing certificate (`.pfx` or cert-store thumbprint) | about $150-300 per year | Wired in Wails: `wails3 tool sign` shells out to `signtool.exe /f <pfx> /p ... /sha1 <thumbprint>` with a timestamp URL; `sign:installer` task; password kept in the OS keychain via `wails3 setup signing`. | Works with the existing Taskfile. |
| EV certificate | about $400+ per year | Microsoft: EV certificates **no longer bypass SmartScreen**; paying the premium only for that is "no longer justified". | Not recommended for SmartScreen reasons. |
| Azure Artifact Signing (formerly Trusted Signing) | about $9.99 per month | No hardware token; **does not provide instant SmartScreen trust** (typically several weeks and hundreds of clean installs); certificates are short-lived (about 3 days); signing needs the Artifact Signing Client Tools (`winget install -e --id Microsoft.Azure.ArtifactSigningClientTools`) with a dlib and `metadata.json`; plain SignTool does not work. **Not wired in Wails** (`sign.go`: built-in signing not implemented). | Needs a custom `sign` task replacing `wails3 tool sign`. |
| Unsigned | free | SmartScreen "Windows protected your PC"; Windows 11 Smart App Control blocks unsigned files without a positive reputation. | Development builds only. |
| Microsoft Store / MSIX | Store re-signs for free | Rejected by D8 (read-only package breaks the swap updater; MSIX updates go via App Installer/Store). | Not v1. |

- **Eligibility conflict (unresolved):** Microsoft pages disagree. One set says organizations in USA, Canada, EU, UK and individuals in USA and Canada only; the quickstart adds Australia, New Zealand, Japan, South Korea, Singapore, Switzerland, Norway, Israel; another page adds "verifiable tax history of three or more years" for organizations. A brand-new company may be ineligible. D8 therefore defers the choice to company-account creation, when this check must be re-done.
- **Wording gap in D8:** "plain signtool via `wails3 tool sign` until then" requires a certificate (`.pfx` or thumbprint). Until one exists, early builds are **unsigned**; D8 does not say so. Treat SmartScreen warnings on early releases as expected (phase0 3.8) and state it in release notes and onboarding.
- **Reputation:** applies to publisher identity plus file hash; signing every release with the same identity lets later releases inherit; there is no manual submission path for consumer reputation. Sign both the application exe and the installer, always with a trusted timestamp, so a certificate rotation does not invalidate old signatures.
- **Key custody (proposal):** the signing certificate or Azure identity is used only from the owner's release machine/pipeline; never in agent processes (relates to D6/P5).

## 4. Updater, channels, rollback

**Mechanism (from Wails beta.28 source, `v3/pkg/updater`):** `app.Updater` with `Config{CurrentVersion, Providers, PublicKey, CheckInterval, ...}`; providers supply release metadata; the updater verifies the SHA digest and an optional signature (`ed25519`, `ed25519ph`, `ecdsa-p256`) against a **public key pinned in app config**, downloads, verifies, unzips, then spawns a detached helper that waits for the PID to exit and renames the new exe over the old (`renameOrCopy`). The CLI `wails3 updater genkey|sign|manifest|verify` implements the "Wails Update Manifest protocol". The shipped provider is GitHub Releases; the `Provider` interface is `Name()`, `Check(ctx, CheckRequest) (*Release, error)` and `Download(ctx, *Release, io.Writer, onProgress)`. **Finding (read from source, beta.28):** Wails already ships a generic `providers/endpoint` provider for the Wails Update Manifest protocol (static JSON per channel, `{{channel}}`/`{{platform}}`/`{{arch}}` placeholders, a client-side channel check, and auth headers that never leave the manifest host), so Drydock needs **no custom provider**: `app/updates_windows.go` configures `endpoint` with the feed URL and the pinned Ed25519 public key (both build-time; unset means updates are reported as not configured). Drydock's own rules live in `internal/updatepolicy` (tested here): blockers (license channel, downgrade, running workflows or live agents), the pre-update database backup, and keeping the previous executable under `previous\<version>\`. Rollback itself (swapping the kept executable back) and the update UI are **not built** and nothing in this flow has run on Windows.

**Drydock configuration (D8):**
1. A custom `Provider` fetches `<feed>/<channel>/manifest.json` from Drydock's own static feed. No GitHub tokens in the client. The feed serves only binaries and manifests; it receives no source code and no user data. The update check reveals the client IP, version and channel to the feed host; this must be disclosed and is the only network call Drydock makes on its own behalf besides user-configured providers (a nuance to the brief's "no cloud component", which concerns source code). A setting disables automatic checks; "Check now" always works.
2. `Channel`: `stable` (default) or `beta`. Separate manifests per channel; a version has a channel-qualified identity (`1.4.0` vs `1.5.0-beta.2`). Switching from beta to stable never downgrades automatically; it waits for the next stable version (**proposal**).
3. Signing: `genkey` once on an offline machine; the private key never enters CI that runs untrusted code; the public key is compiled into the app. Rotation (**proposal**): the app pins two keys (current and next) so a rotation release can be verified by the old key.
4. Artifacts: update payload is a zip containing the new exe (the updater unzips), listed in the manifest with SHA-256 and size; the installer is used only for first install and repair.
5. **Release notes:** shown by the built-in update window; content comes from the manifest (format and Markdown support **unverified**). Notes must state schema migrations and any breaking changes.
6. **Install location requirement:** the in-place swap works for a user-writable directory; a machine-wide Program Files install reportedly breaks it (field report, search-only). This is why D8 mandates `INSTALL_SCOPE=user`. MSIX cannot self-update this way.

**Rollback (D8: keep the previous exe):** before the helper swaps, Drydock copies the running exe to `<app data>\previous\<version>\Drydock.exe` (**proposal**; whether Wails already retains the old file is **unverified**). Settings > Updates lists "Roll back to <version>" using the same detached-helper swap. Two safeguards because the SQLite schema moves forward:
- Take a pre-update backup of the database (section 8) before applying migrations, named with the old and new versions.
- Migrations with the migrations tool (`pressly/goose`) are written expand-only within a release window so the previous exe can still open the database (**proposal**); if the previous exe finds a schema version it does not know it must refuse to start and offer "restore pre-update backup", never write.
Rolling back after data was created by a newer version loses nothing silently: the UI says so and keeps the newer database file under `backups\`.

## 5. Update safety rules

Never update while a run is `RUNNING` or an agent process is alive without confirmation; update applies at next quit or "Restart now" after agents are stopped cleanly (the process tree is killed by the app job; D7). Pending approvals survive: they live in SQLite and runs resume from the latest checkpoint after restart (brief 4). A failed verification (digest or signature) aborts, deletes the download, and posts a `SYSTEM` message; the app keeps running. Downgrades are refused unless initiated through rollback.

## 6. Data ownership

Drydock keeps everything it owns in one place. Brief 18: SQLite is the single authority; raw output lives on disk under `<app data>/runs/<run_id>/`.

```
%LOCALAPPDATA%\Drydock\            (proposal; see open questions)
  drydock.db  (+ -wal, -shm)       the single SQLite file: all state, events, usage, checkpoints, settings
  runs\<run_id>\<node>\<attempt>\  raw agent output, tool output (brief 4, 18)
  backups\                         scheduled and pre-update backups (section 8)
  previous\<version>\              prior executables for rollback
  updates\                         downloads in progress
  logs\                            app logs (scrubbed of secrets)
  support\                         generated support bundles
```

- **Not stored by Drydock:** secrets are in Windows Credential Manager (`zalando/go-keyring`); values over 2560 bytes (the Windows blob limit confirmed by Microsoft docs) are encrypted into SQLite with the key in Credential Manager. Worktrees and repositories belong to the user and live wherever their repository is; Drydock never deletes a dirty worktree without approval. Agent CLI credentials stay in each CLI's own store (D1: Drydock never reads or copies them).
- **Why local, not Roaming:** WAL-mode SQLite on a roaming profile or an OneDrive/network path risks lock and sync problems (file locking on those paths is **unverified** and listed for Windows testing). The path is user-configurable; Drydock warns if the chosen location is a known sync folder (**proposal**).
- **Retention (brief 18):** events and usage kept indefinitely; raw output and checkpoints of completed runs compacted after 30 days and deleted after 90. See the license conflict in section 10.
- **Exclusive access:** the single-instance guard plus the single writer connection (phase0 3.13); a second process opening the database is a bug.

## 7. Project export and import

- **Export** (Project > Export): writes a `.drydock-project.zip` containing `manifest.json` (format version, app version, goose schema version, project id, created time, file list with SHA-256), `project.db` (a new SQLite file produced by the same migrations, populated with this project's rows: rooms and messages, tasks, workflows and checkpoints, approvals, handoffs, usage, routing records, memory, settings for the project), optional `runs\` raw output (default **off**; when on, a warning that raw output can contain source code and a scan result from the secret scanner is shown), and an exports/ folder of immutable Markdown minutes and PR description drafts if present. Never included: secrets, Credential Manager entries, encrypted-secret rows, agent CLI credentials, worktree contents, the user's repository, provider API keys.
- **Import:** validates the manifest and hashes; refuses a `project.db` with a schema version newer than the running app (offers update); migrates older ones forward; imports as a new project (IDs remapped) so it can never overwrite an existing one; imported instruction files, skills and memory are untrusted data (brief 16) and require explicit acceptance before they are used or written into any worktree. Re-linking to a local repository path is a required step. Provider definitions are imported without keys and show `unauthenticated`.
- Licensing: export and import work in read-only (expired) mode (section 10).
- Open: ID format and cross-project foreign keys (e.g. shared providers) are not defined in the brief; the export must be designed against the final schema.

## 8. Scheduled backups

- In-app scheduler (no Windows Task Scheduler, so nothing runs while Drydock is closed). Default **proposal**: daily when the app is idle, keep the last 7 daily and 4 weekly, plus a pre-update backup that is exempt from pruning until the update is confirmed healthy.
- Mechanism: SQLite `VACUUM INTO '<file>'` on a reader connection (consistent snapshot under WAL; SQLite 3.53.4 bundled), then `PRAGMA integrity_check` on the copy and a SHA-256 recorded next to it. Backups contain only the database; `runs\` raw output is excluded by default because of its size and 90-day lifetime (option to include).
- Restore: Settings > Backups > Restore; the app closes agents, moves the current database to `backups\replaced-<time>.db`, installs the chosen file, runs forward migrations. A restore is never destructive.
- Failure (disk full, locked file, path unreachable) posts a `SYSTEM` message and an inbox item; backups are never silently skipped.
- Backup location is user-configurable; same sync-folder warning as section 6. Backups hold the same sensitive content as the database (room messages, code excerpts), but never secrets from Credential Manager.

## 9. Support bundle and diagnostics

**Support bundle** (Settings > Support > Create bundle): a local `.zip`, never uploaded automatically; the user reviews the file list and a preview before saving and sends it themselves. Contents: app version and build, Windows version/build, WebView2 version, install scope, goose schema version, Connections with detected CLI versions and the compat verdicts, last conformance results, Node/Git/`gh` versions, settings with secrets removed, scrubbed app logs, the last N events as **type, seq, time and ids only** (no payloads), watchdog trip signatures (normalized, no content), error codes, disk free space. Excluded by default: room messages, prompts, diffs, tool output, source files, memory, requirements, raw run output, environment variable values (names only).
- **Scrubbing pipeline (proposal):** (1) allow-list assembly (bundle builders are explicit, nothing is added by globbing); (2) run every text file through the gitleaks library (v8.30.1, 222 default rules verified to detect a fake key) and replace matches with `[REDACTED:<rule>]`; (3) replace the user profile path, machine name and user name with tokens; (4) refuse to include a file if scanning fails; (5) show the scrub report (counts per rule) in the bundle. Opt-in "include run X": adds scrubbed events with payloads and raw output for one run after the user sees the secret-scan result; the bundle manifest records that it was included.
- Scrubbing reduces but does not eliminate leakage of proprietary code; the UI says so.

**Diagnostics (consent-based, off by default; brief 10, D12):** categories the user enables individually: (a) crash reports (stack trace, versions, no memory contents), (b) anonymous usage counters (feature use counts), (c) OpenTelemetry export. OTel is optional and off; exported attributes are our own constants (D12); it never includes prompts, code, tool output or secrets unless the user explicitly enables content export, which is a separate opt-in with a warning. A "what would be sent" preview and a local log of everything sent are mandatory. Revoking consent stops sending immediately. **Open:** Drydock operates no collector today; until the owner chooses one, diagnostics write to a local sink only and OTLP export targets a user-specified endpoint (phase0 appendix 8 suggested a local SQLite sink with on-demand export, which also saves roughly 15 MB of exporter dependencies).

## 10. License tokens (offline Ed25519)

The scheme is Drydock's own and **has not been verified or reviewed**. It uses Go's `crypto/ed25519` (no CGO) and works with no network.

- **Token format (proposal):** `base64url(payload_json) "." base64url(signature)`, delivered as a pasted string or a `.drydock-license` file. Signature is Ed25519 over `"drydock-license-v1\n" || payload_bytes` (domain separation). The app embeds a list of public keys by `kid` (supports rotation); the private key lives offline with the vendor and never ships.
- **Payload fields:** `v` (schema version), `kid`, `jti` (license id), `kind` (`trial|paid`), `sub` (customer id, not necessarily an email), `name` (display), `plan`, `seats` (informational in v1), `features[]`, `channels[]` allowed (`stable`, `beta`), `iat`, `nbf`, `exp`, `grace_days`, optional `mid` (machine fingerprint hash; machine binding is **not** in v1 unless the owner decides), `min_app` / `max_app` (optional version window).
- **States:** `none -> trial -> active -> grace -> expired`. `trial`: a `kind=trial` token (preferred, signed, issued from the vendor website) or, if the owner chooses no-network trials, a locally recorded start date (trivially resettable by deleting data; accepted weakness). Trial length is an owner decision (brief gives none). `active`: now within `[nbf, exp]`. `grace`: `exp < now <= exp + grace_days`, full functionality plus a persistent banner and inbox item. `expired`: read-only (below). Renewal = import a new token; a newer valid token replaces an older one; there is no revocation list offline.
- **Clock handling:** the app stores the highest timestamp it has seen (in `settings`); a system clock set back beyond a tolerance does not extend validity (tamper-evident, not tamper-proof). A token with `nbf` in the future is rejected with an explicit message.
- **Read-only on expiry (brief 21, never deletes data):** allowed: opening projects, reading rooms, tasks, diffs, runs, events and usage, exports, backups, restore, support bundle, license import, uninstall. Not allowed: starting agents, tasks or workflows, creating worktrees, approving agent actions, merging, PR creation, adding connections. Running agents are stopped cleanly (process tree killed via the app job; D7), checkpoints let runs resume after renewal.
- **Data is never deleted by the license state.** This conflicts with brief 18 retention (raw output and checkpoints deleted after 90 days) if the license stays expired: **decision proposed here:** the retention compactor and deleter are suspended while expired, so nothing is deleted because of expiry. Needs owner confirmation.
- **Limits stated honestly:** a determined user can patch the binary; the scheme protects against casual sharing and expiry honesty, not piracy. No obfuscation claim is made (Garble is supported by Wails but its effect on Defender is unknown, section 12). Key compromise response: ship an update that adds a new `kid` and drops the old; old tokens stop working only after that update.

## 11. Onboarding in under ten minutes

Target: from running the installer to a first real agent reply in the Room in under ten minutes on a machine that already has Git and one agent CLI. **Unmeasured**; must be timed on a clean Windows VM by someone who has not seen the app.

| Step | Budget (target) | Notes |
|---|---|---|
| Install (per-user NSIS, WebView2 usually present) | 1 min | No admin prompt; SmartScreen warning possible until reputation accrues. |
| First-run notice (D1 terms) and theme | 0.5 min | Plain language; one button. |
| Connections scan | 0.5 min | Detect Claude/Codex/Gemini/OpenCode/Goose, Node 22+, Git. |
| Install/log in a CLI | 3 min | Install guidance only (nothing downloaded for the user, D9); login in the embedded terminal. Dominates the time. |
| Optional provider key (OpenRouter) | 1.5 min | Validated with `/v1/models`; stored in Credential Manager. |
| Create project from a local repo; choose team template | 1 min | Defaults, not locks. |
| Test connection (conformance short form) | 1 min | Real CLI call; can use tokens. |
| First message in the Room | 1 min | Streaming reply with usage shown. |

If a prerequisite is missing, the page says exactly what to install and re-checks automatically. A user who never installs an agent CLI can still use the app to view the Not-built-yet list but cannot do work; the UI never fakes an agent (brief 0).

## 12. Defender and SmartScreen false positives

Facts (tooling-integrations 4d): Microsoft says contextual exclusions are not a reliable fix; the supported path is to submit the file to Microsoft Security Intelligence for analysis; Microsoft does not run a known-list program, but consistently signed programs may be added to the known list in some cases; SmartScreen is not Defender Antivirus. Go binaries are frequently flagged by `!ml` heuristics (microsoft/go #1255, golang/go #38821, cli/cli #13306; search-only), with conflicting reports on whether `-ldflags "-s -w"` matters. Drydock is an unusual binary: it spawns processes, uses Job Objects and ConPTY, writes into worktrees, and downloads nothing, which may resemble malware heuristics (**unverified**).

Plan:
1. Sign every release identically (section 3).
2. Before publishing: scan the installer and exe with current Defender definitions on a clean Windows VM and with VirusTotal; compare builds with and without `-w -s` once and record the result; do not use packers or Garble (**proposal**, avoids known heuristic triggers).
3. If flagged: submit to Microsoft Security Intelligence with the release hash, delay the feed manifest until the verdict clears, and record the submission id in the release file.
4. Support text: how to read Protection history, restore, and report; never instruct users to turn off protection or add broad exclusions; if an exclusion is the user's choice, it is for the install folder only and Drydock's data directory is explicitly not recommended.
5. Keep the release hash list public so users can verify.

## 13. Release checklist

1. Tag; changelog written; breaking and migration notes included in release notes.
2. Windows CI green on `windows-latest` (blocked by P2 until fixed): unit, integration (real CLIs where installed; skipped tests listed with reasons, P3), D5 and D7 jobs.
3. `go-licenses` run; `THIRD_PARTY_NOTICES.md` updated; legal gate cleared (MCP go-sdk license, Aider attribution, CC BY attributions); D1 position confirmed in writing before any release that includes Claude subscription support.
4. Build with pinned Go and Wails; record exact versions and commit.
5. Sign exe, then build and sign installer; timestamp; verify with `signtool verify /pa`.
6. Install on clean Windows 10 and 11 VMs (user scope, no admin); SmartScreen and Defender observations recorded; uninstall leaves data.
7. Upgrade test from the previous stable and previous beta; rollback test; pre-update backup created.
8. Run the onboarding timing test (section 11) and the recorded eval smoke suite (`evals.md`).
9. `wails3 updater sign` and `manifest`; `verify` with the pinned public key; publish to `beta` first, soak, then `stable`.
10. Migration test: open a database created by the previous release; open one from two releases back.
11. Support bundle test: generate, run gitleaks over it, confirm no secrets or source.
12. Verify the license flow: valid, expiring, grace, expired read-only, clock set back.
13. Submit to Microsoft Security Intelligence if any detection; archive hashes.

## 14. Things that need a Windows machine to prove

(Cross-reference `phase0.md` section 7; none has been run.)
- NSIS user-scope installer builds with `makensis`, runs without elevation, WebView2 bootstrapper behavior, uninstall keeps data.
- Signed vs unsigned SmartScreen and Smart App Control behavior; Artifact Signing end-to-end with the signtool dlib (not supported by `wails3 tool sign`).
- Wails updater: custom Provider, signature verification with the pinned key, in-place swap under a user-scope install, behavior when the old process is slow to exit, what happens to `DisplayVersion` in the Windows uninstall entry (reported stale), rollback swap.
- SQLite on NTFS with Defender active and the `SQLITE_MUTEX_NOOP` build; `VACUUM INTO` backup under concurrent writes; WAL on OneDrive paths.
- Credential Manager round trip including a value over 2560 bytes (`ErrSetDataTooBig` expected) and a locked store.
- Job Object kill of process trees in native and `wsl` modes, ConPTY via go-pty (D7 `jobtree` probe); the app's own job and nested-job behavior when launched from the installer, a launcher, or CI.
- Defender result for the real binary, with and without `-w -s`, with and without the CGO repo map; false-positive submission process.
- Single-instance behavior and window startup time; event streaming latency (`ui.md`).
- Time-to-first-message on a clean VM (onboarding); Node and CLI detection (`npx.cmd` spawning).
- License token verification performance and clock-rollback handling.

## Open questions / unverified

- The custom `Provider` interface, the manifest schema details, release-notes format, and whether Wails retains the replaced exe: all must be read from `v3/pkg/updater` before implementation.
- Default install directory for the user-scope NSIS build; whether per-user installs show correctly in Apps & features.
- Artifact Signing eligibility for a new company (Microsoft pages conflict); which signing path ships first; D8 does not state that pre-certificate builds are unsigned.
- Feed hosting provider and who operates it; whether an update check counts as a "cloud component" for the owner's purposes.
- Trial length, trial issuance (signed token vs local), grace length (`grace_days`), and whether machine binding (`mid`) is wanted.
- Retention versus expiry conflict (section 10): suspension of deletion while expired is a proposal.
- Whether to roam or exclude `%LOCALAPPDATA%` data from backup software; worktree location policy (outside this document's scope).
- Diagnostics collector: none exists; defaults here are local-only.
- Whether CGO (tree-sitter) changes Defender behavior; whether Garble or `-w -s` change it.
- Wails v3 is beta; installer and updater behavior may change between betas; Wails open-issue list was not checked.
