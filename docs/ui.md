# Drydock: UI information architecture

Scope: this document turns brief section 21 into an implementable UI plan for Slices 1A to 1E: the four-region layout, every screen that exists in those slices, the screens that must be shown honestly as "not built yet", the component inventory (Monaco diff, React Flow, cmdk, TanStack Virtual, xterm.js), message card types, the Needs-you inbox, the Connections page, Providers settings, the Agent Inspector, the Wails v3 integration contract (services, events, generated bindings, replay by event `seq`), keyboard map, theming, state management, streaming performance rules, accessibility basics, and a slice-by-slice delivery table. Owner decisions in `decisions.md` (D1-D14, P1-P6) win over the brief. Facts about Wails come from `phase0/go-platform.md` section 1; anything not in the brief, decisions or appendices is marked **proposal** (a design choice made here) or **unverified**. Nothing in this document has been run on Windows (P2).

**Status (what exists, honestly).** A UI is built in `ui/` (React 19, TypeScript 7 strict, Vite 8, no CSS framework; dependencies are `react`, `react-dom`, `marked` used only as a Markdown lexer, and dev tooling). The pages after the first screen are code-split. It covers the following.

- **Slice 1 screens.** Project create with the core's own error shown; team setup with adapter detection and capabilities (absent means "not verified"); the Connections page (status, usable, prerequisites, gates only the human accepts, Test connection behind a cost warning); the Providers page (the API key never enters UI state or the page, validation errors verbatim, price-source badges, "not reported" for unknown prices); project and task channels (author badges, markers, ERROR styling, tool check cards, composer, threads, members, direct messages, archived task channels); Tasks list/create/assign/start/cancel; Task detail with evidence, reviews, plain unified diff, usage rows with reported/estimated badges and routing decisions; the human review panel bound to the pending request id; the Agent Inspector; the Needs you inbox; Activity; Pull Requests and GitHub issue import; project delivery mode and merge strategy; Settings.
- **Slice 2 (economics).** Budgets (warning at the warn percentage, paused at 100%, unknown-cost calls counted and never priced at zero, a resume form on the paused task that can raise limits); the Evals report (the core's Markdown tables rendered inert, headline ratios with n/a for zero denominators, local export); Routing (rules edited against their version, a dry-run preview, cheap-model selection and the opt-in startup probe with a cost warning, "Explain why this model" on tasks and in the inspector; a decision with decidedBy `static` says it was not routed); Telemetry in Settings (off by default, local only, a preview of exactly what an export holds); check settings (secrets, dependencies).
- **Slice 3 (knowledge).** Expert packs (install from a manifest, refresh, attach, search and consult with citations, quarantined passages released only by the human); Requirements (upload or paste, versions, criteria linked to tasks, requirement -> task -> evidence trace, stale links); Milestones; Skills (repository and registry skills are offered and the human accepts or rejects each, registries allowlist, deliveries); Memory (proposals accepted or rejected by the human, search, delete).
- **Slice 4 (development flow).** Template picker on Start; the spec gate panel (spec rendered inert, Given/When/Then criteria, Accept, edit criteria, Reject with feedback); evidence labels for spec, spec_accept, red_test, green_test, design, e2e, screenshot and preview (a skipped e2e or preview shows SKIPPED); PlanTeam (propose, dependency-closed selection, edits, Accept, Reject); scheduler status and tick; project development settings (parallelism, scheduler, replies, e2e and preview commands); room modes, invites, agent turns and reply-now with a plain-language explanation of each mode; preview start and stop.
- **Slice 5 (integrations).** Connect (token in an uncontrolled password input, cleared on submit), test, sync, disconnect; items rendered inert with an injection-risk score and import by item id (blocked at risk 3 or more); rules (human-only, new rules start disabled and dry-run); outbound writes (the approval id and the exact text shown, Approve and Send are separate buttons bound to the outbox id); Notion pages read as inert requirements text. Needs you links budget pauses, spec reviews and outbound approvals to where they are answered.
- **Packaging.** A persistent license banner above the whole app (loud and plain when licensing is not configured in the build, an alert when the app is read-only, a warning in grace), and a `LicenseProvider` that reads `license.status` so mutating controls are disabled before the core refuses them, with the core's own reason as the tooltip and a note (create project/agent/provider, connect an integration, start a task, answer or approve a request, budget resume, plan propose/accept, scheduler tick, start a preview, integration approve/send); the core's refusal text (it wraps `ErrReadOnly`) is still shown verbatim if a call is refused anyway. Pages: License (state, days left, entitlements, what stays usable when read-only, token import through an uncontrolled field that is cleared and never shown back, or a file path, and remove); Backups (back up now, list, verify, restore only after a successful verify, restore is STAGED for the next start with a pending banner that comes from the core and a cancel, the replaced databases it keeps, schedule and retention settings, folder location with the sync-folder/network warning, last error); Export and import (export preview with file list, secret-scan result and what is never included, raw run output opt-in behind an acknowledgement, export to a local path; import preview and import into a NEW project linked to a local repository path, with everything that arrives untrusted listed: memory proposed, skills offered, requirement chunks quarantined, routing rules disabled, runs not resumed, approvals cancelled, providers needing keys); Support bundle (preview of files, sizes and scrub report with nothing written, opt-in run inclusion behind an acknowledgement, create, never uploaded); and an Updates section in Settings that says updates are configured at build time and the update flow (Wails updater plus the pre-update backup and rollback policy) has not run on Windows (no buttons). License, Backups, Export and import, Support bundle, Providers, Connections, Routing and Settings open even before a project exists, so an unlicensed install can always reach License. Paths are typed into plain text fields (no native file dialogs).

Transport: `ui/src/api/transport.ts` (HTTP + Server-Sent Events) and `ui/src/api/wails.ts` (chosen by `main.tsx` when a WebView2/Wails host is detected, otherwise HTTP). Start-up is exact: read `events.head`, open the stream after it, then let screens read their projections; screens re-read on matching events and on every reconnect. **Tested:** 218 vitest unit/component tests and 27 Playwright tests that drive the real Go core (`cmd/drydock-e2e`; the scripted fake agent and an in-memory credential store are the only test doubles) in Chromium on Linux. The default harness is a build with no license keys; a second harness process (`-licensed`, a TEST HARNESS MODE with a throwaway Ed25519 key, never a product feature) starts with no license and prints two signed test tokens so the read-only banner, disabled controls, import, remove and recovery run against the real licensing code. Coverage includes approve, request-changes, reload equality, XSS, a budget pause and resume, the Feature template through the spec gate to red and green, PlanTeam, an expert pack from a local folder, a requirement upload and trace, skills offered then accepted, and an integration connect with no vendor server, and for packaging: a backup made, listed and verified, a restore staged and shown pending after a reload and cancelled, a project exported and imported into a second project, and a support bundle whose preview and file on disk do not contain a secret planted in a message. The MCP library and MCP servers pages (connect a stdio or https server once, drag it onto a project or an agent, reusable agent templates dragged into a project, find its tools, mark tools read-only, grant agents, approve or reject waiting calls; secrets are write-only and shown as names) are covered by vitest and a Playwright test that adds a real stdio server (the harness binary re-executed as a fixture). The collision guard has vitest coverage (the project setting, a waiting task's reason and the release button) and a Playwright test against the real core (two tasks changing calc.go: the second is Blocked naming the first task and the file, the release button lets it go on). **Not done / not verified:** a staged restore was never applied (that needs an app restart, which the browser harness cannot do), so the restart-and-apply path is covered only by the core's own Go tests; the licensed harness covers none and expired and active, but not the grace state or a clock set back (grace and the days-left line are unit-tested with fixtures only); the support bundle with an included run was only run as far as the core's refusal of an unknown run id (acknowledgement and the scan-first flow are unit-tested); the update flow does not exist in the UI and has not run on Windows; anything in WebView2 or on Windows (the Wails transport was written from the Wails v3.0.0-beta.28 source and tested only against fakes; the production CSP has not been checked against WebView2). Test connection on the Connections page and the Providers probe against a real provider were not run. Integrations were never exercised against a vendor (there is none here): only connect, a failing test, rules and the empty lists were run. The GitHub screens were not run against GitHub. Preview start and stop and the e2e command setting were not run end to end. Monaco, xterm, React Flow, cmdk, TanStack Virtual and zustand are not used. There is no bottom dock, command palette, slash commands, attachments or workflow graph. Accessibility was designed for (landmarks, labels, `role=log`, arrow-key sidebar, text plus shape for status) but no screen-reader or contrast audit was run.

## 1. Principles (from brief sections 0, 16, 21)

- Dense, keyboard-first, dark and light, restrained motion. Slack/Discord information architecture with engineering panels.
- **Build only screens for features that exist** (brief 0, 21). No screen, tab, or button ships ahead of its slice. Where a user would reasonably expect a feature, the UI states that it is not built yet and names the slice (section 4). No fake data, no placeholder charts, no simulated agents (brief 0).
- **All agent output, room messages, GitHub text and retrieved content are untrusted** (brief 16). Markdown is rendered through a sanitizer with raw HTML disabled, remote images are not auto-loaded, links open in the external browser after showing the target, and text can never trigger an approval. Approvals are answered only by buttons bound to an approval id (brief 12: bound to a hash of the exact action).
- The UI holds no authority: SQLite is the single authority (brief 3). The UI is a projection of persisted events plus service calls; it can be closed and reopened at any time and must catch up from the log (brief 24, Slice 1A).
- Estimated and reported numbers are never mixed silently (brief 7): every token or cost figure carries a source badge (`reported` or `estimated`), and a value a CLI does not report is shown as `not reported`, never as 0.

## 2. Layout (brief 21)

```
+-----------+---------------------------------------------+------------------+
| Left      | Center                                      | Right column     |
| sidebar   | conversation (message cards + composer)     | tabs: Thread |   |
|           | or a page (Tasks, Workflow, Connections...) | Task | Inspector |
|           |                                             |                  |
|           +---------------------------------------------+------------------+
|           | Bottom dock: Terminal | Diff | Logs | Activity (model/tokens)   |
+-----------+-------------------------------------------------------------- +
```

- **Left sidebar (fixed width, collapsible):** workspace and project switcher; **Needs you** inbox with count badge (always first); channels; DMs; groups; Project section (Overview, Requirements, Tasks, Milestones, Activity, Integrations); Workspace section (Connections, Providers, Settings). Only entries whose feature exists are rendered; section 4 lists what is absent per slice.
- **Center:** either a conversation (virtualized message list, section 6) with the composer (mentions, slash commands, `[DECISION] [TODO] [STATUS] [RESULT]` markers, attachments), or a page (Tasks, Task review, Workflow, Pull Requests, Connections, Providers, Settings). One center view at a time; back/forward history is kept per window.
- **Right column (resizable, collapsible):** tabs Thread, Task details, Agent Inspector. Selecting an agent presence dot, a card author or a task opens the matching tab. It never shows a feature that is not built.
- **Bottom dock (resizable, collapsible):** Terminal (xterm.js, one tab per terminal), Diff (Monaco diff for the selected task), Logs (agent raw output and process stderr), Activity (model and token activity: per-run usage rows, source badges). The dock keeps its tab state per project.
- Window chrome: Wails window (WebView2). Minimum supported layout width: 1100 px with the right column collapsed; below 900 px the sidebar collapses to icons (**proposal**, not measured on Windows).

## 3. Screens that exist in Slices 1A-1E

| # | Screen | Slice | Content and rules |
|---|---|---|---|
| S1 | First-run and onboarding | 1A | Terms notice for D1 (own, unmodified, user-logged-in `claude`; Drydock never reads or transmits Claude credentials; ACP bridge off). Steps: Connections -> add provider (optional) -> create workspace/project -> team -> first message. Target under ten minutes (see `packaging.md`). |
| S2 | Connections | 1A (agents, providers' API-key checks); `gh` card in 1E | Section 8. |
| S3 | Providers settings | 1A | Section 9. |
| S4 | Project create / open | 1A | Pick a local Git repository (Git is driven through the `git` CLI, brief 2). Project Overview shows repo path, branch, and team. |
| S5 | Team setup | 1A | Agent = (Provider, Role, Expert Packs[]) plus policy (brief 1). 1A offers provider, role, model, effort (only as far as the adapter reports the capability, brief 5), permission mode. Packs column is absent until Slice 3. Templates set defaults, not locks. As built: the add form and a per-agent Edit panel carry name, role, **purpose** (standing instructions sent with every run), model (the adapter's aliases are suggested) and effort (a list when the adapter lists its levels); the adapter is fixed after creation; Remove from team archives the agent after a confirmation and is refused while it owns unfinished tasks (docs/decisions.md P14). |
| S6 | Engineering Room (channel and DM) | 1A | Message cards (section 5), presence, composer. 1A: one project channel plus DMs with each agent. Task channels from 1B. Room modes `auto`/`sequential`/`moderator`: the mode selector is **not built** until a mode other than the default exists (**proposal**: Slice 4). |
| S7 | Needs you inbox | 1A minimal, grows each slice | Section 7. |
| S8 | Agent Inspector (right tab) | 1A, fields grow | Section 10. |
| S9 | Activity (Project) | 1A | Event log viewer over the `events` table (type, seq, time, scope), filter by prefix (`agent.*`, `task.*`, ...). Useful for the 1A acceptance of replay. |
| S10 | Tasks list and Task details | 1B | States per brief 12; owner, worktree, branch, attempts, safe points (`refs/aet/<task>/<attempt>`), checks. Rollback to a safe point is a human action with confirmation; the UI never resets a worktree on its own. |
| S11 | Task channel | 1B | Conversation scoped to a task; tool members (`Tests`, `Lint`, `Types`, `Build`) post check results at zero cost. `Secrets` is Slice 2 (P5), `CI` is 1E, `Preview` unassigned. |
| S12 | Diff and Terminal docks | 1B | Monaco diff for the task worktree; xterm.js over ConPTY (go-pty, D7: ConPTY children are tree-killed by walking the job after spawn). |
| S13 | Dirty-worktree dialog | 1B | A dirty worktree is never deleted without approval: modal listing tracked and untracked changes, creating an approval row. |
| S14 | Workflow view | 1C | React Flow graph of the run plus the checkpoint timeline (section 6.2); watchdog events and trips on nodes; request nodes link to the inbox. |
| S15 | Review screen | 1D | Changed files, diff, test results, reviewer/QA/security results as evidence; actions Accept, Reject, Request Changes, Ask Agent, Merge (brief 13). Merge is a human action using the per-project strategy. |
| S16 | Run explanation | 1D | "The run is fully explained by manifest, usage, checkpoints and events" (brief 24): context manifest (source, included/excluded, size, reason, priority), usage rows, checkpoints, events, MCP per-call audit events. Read-only. |
| S17 | Instruction files | 1D | Generated `AGENTS.md` (canonical) plus one-line import files for `CLAUDE.md`, `GEMINI.md`, `.goosehints` (phase0 3.12), regeneration status, human edits offered back as memory updates (accept/reject). |
| S18 | Pull Requests view | 1E | PR list and detail: description (generated, editable before push), CI check results (failing logs fed back to the worker), review-comment round trip (all comment text untrusted), issue import, "merge on GitHub" is a human-initiated link-out/action; auto-merge is never enabled (brief 13). |
| S19 | Settings | 1A | Appearance (theme), data location (read-only display), keyboard map (read-only in v1), About (version, licenses link, the "Not built yet" list from section 4). |
| S20 | Command palette | 1A | cmdk overlay, section 6.3. |

Packaging pages (Updates, License, Backups, Export/Import, Support bundle, Diagnostics consent) are specified in `packaging.md`. The brief assigns them to no slice; **proposal**: Updates and Support bundle ship with the first installer release, License/Backups/Export before the first paid release.

## 4. "Not built yet": what the UI must say honestly

Rendered in Settings > About as a list (feature, slice). Since this table was written, the core and UI gained the Slice 2 to 5 features (router rules and explanations, budgets, secrets/deps/e2e/preview checks, expert packs, requirements, skills, memory, milestones, room modes, templates, PlanTeam, integrations); the Settings > About list is the current truth and inline only where a user would otherwise look for it. Inline text pattern: "Not built yet (Slice N). <what happens instead>". Nothing here has a disabled screen, mock chart, or fake value.

| Feature | Slice | What the UI shows meanwhile |
|---|---|---|
| Model Router, "Why this model?", routing decisions, escalation records | 2 | Inspector row "Why this model?": "Not routed. Model chosen by team setup (static assignment behind the `ModelRouter` interface)." Brief 6: assignment is static until Slice 2. |
| Budgets (warn at 80%, pause at 100%), per-run ceilings | 2 (**proposal**) | Usage is displayed; no limit controls exist. |
| `secrets` check (gitleaks) | 2 (P5) | Tool member absent. |
| `deps`, `e2e`, `preview` checks | unassigned | Absent. |
| Expert Packs, Requirements, Skills, Memory UI | 3 | Project nav hides Requirements; team setup has no Packs column. |
| Autonomous multi-agent development, `moderator`/`auto` room modes, group chats | 4 (**proposal**) | Absent. |
| Linear, Notion, Sentry, Aikido integrations | 5 | Integrations nav entry absent; Connections shows only `gh` (1E) and agent CLIs. |
| Milestones; Markdown minutes and ADR-candidate export | unassigned | Absent. (PR description drafts arrive with 1E.) |
| Repo map view | gated by D5 | Absent until the Windows CI job is green. |
| Cloud sync, accounts, team sharing | never (brief 0: no cloud component) | Not mentioned in the UI. |

## 5. Message card types (brief 11)

Common envelope on every card: `id`, `room_id`, `thread_id?`, `seq` (event sequence that created it), `version` (compare-and-swap, brief 18), `created_at`, `author {kind: human|agent|tool|system, agent_id?, tool_name?, display}`, `type`, `mentions[]`, `marker?` (`DECISION|TODO|STATUS|RESULT`), `body` (Markdown, untrusted), `body_ref?` (byte range into raw output on disk for long bodies), `usage_ref?`. Field names are **proposed** until `room-protocol.md` and `interfaces.md` fix them.

| Type | Card-specific fields | Rendering and actions |
|---|---|---|
| `CHAT` | body, reply count | Plain bubble; thread opens in the right column. |
| `TASK` | task_id, title, state, assignee, attempt | Compact card; state chip uses brief 12 states; click opens Task details. |
| `DECISION` | statement, rationale, alternatives?, adr_candidate flag | Highlighted; ADR candidate chip. Export to ADR is not built (section 4). |
| `STATUS` | agent_id, presence state, short text | Collapsible into a one-line system row; consecutive statuses merge. |
| `RESULT` | task_id, summary, evidence[] (check ids, diff stat, test summary), verdict? (`SHIP`/`REVISE` for reviews) | "Evidence" list links to checks and diff; verdict chip. A task enters REVIEW only with evidence (brief 12). |
| `QUESTION` | asked_by, addressed_to (human or agent), options?, request_id (typed request port id) | Answer box; answering calls `Resume(run, response)` for a request node or posts to the agent for a chat question. Mirrors to the inbox when addressed to a human. |
| `HANDOFF` | from_agent, to_agent, artifact_ref (handoff artifact), reason (`context_pressure`, `escalation`, `watchdog`, `review`) | Shows artifact size and expands to the artifact; never a transcript (brief 7). |
| `ERROR` | source (adapter, process, git, mcp, provider), code, message, retryable, run_ref | Red card with retry/fallback status taken from events; no destructive action. |
| `APPROVAL_REQUEST` | approval_id, kind (`approval|question|review`), action summary, action hash (short), risk class, expires_at?, state | Buttons Approve / Reject bound to `approval_id`; shows exact command, path or diff summary the hash covers. States `REQUESTED|APPROVED|REJECTED|EXPIRED|CANCELLED|CONSUMED` (brief 12). Duplicates the inbox item; answering in either updates both. |
| `APPROVAL` | approval_id, decision, decided_by, decided_at | Immutable record card. |
| `SYSTEM` | event_type, text | Muted row (agent started, process killed, adapter drift, connection degraded). |

Tool members (`Tests`, `Lint`, `Types`, `Build`, later `Secrets`, `CI`, `Preview`) post `RESULT` cards authored by `tool`: command, exit code, duration, pass/fail, first N failing lines with "show full output" (range fetch, not preload). Presence states: Working, Thinking, Idle, Waiting, Completed, Needs Attention, Failed, Paused (brief 11), always shown as icon plus text, not color alone. Streaming agent output appears as one in-progress card that is finalized into `CHAT`/`RESULT` when the turn ends; partial text is never persisted as a final card.

## 6. Component inventory

### 6.1 Monaco diff
Read-only `DiffEditor` for task diffs (original = merge base or safe-point ref, modified = worktree), inline and side-by-side toggle, file list with +/- counts, per-file collapse, jump to next change. Large diffs: load per file on demand; files over a size threshold (**proposal**, tune after measurement) render a "large file" placeholder with an explicit "render anyway" button. Models are disposed when a file leaves view. Theme follows the app theme (section 12). Inline comments and "Ask Agent about this hunk" arrive with 1D review; no edit-in-place in Slice 1. Bundling Monaco workers under the Wails asset server is **unverified** (needs a spike in 1B).

**As built (2026-10-09).** `ui/src/dock/DiffViewer.tsx` + `MonacoDiff.tsx` (monaco-editor, lazy chunk; only the editor API, the
diff editor, find, folding, copy, context menu and the language definitions are imported; links are not, since file content is
untrusted). The core serves `task.diffFiles` (the files of the recorded diff, or of the live worktree) and `task.diffFile` (both
versions of one file: the base and task commits from git, or the file on disk for a live diff, refusing paths outside the
worktree); a recorded diff whose commits git has collected falls back to the patch's hunks, marked partial. Files over 2 MB wait
for "Show it anyway"; binary files are not shown. Monaco's editor worker is bundled by Vite and served from the app's own origin
(the CSP's `default-src 'self'` covers it); not yet run under the Wails asset server.

### 6.2 React Flow workflow graph with checkpoint timeline (Slice 1C)
- Nodes by kind (brief 4): `agent_task`, `check`, `request`, `route`, `subworkflow` (a subworkflow node expands into a nested graph view). Edge kinds: direct, conditional, fan-out, fan-in (barrier), loop (drawn with iteration count and the limit, default five for Iterate Until Verified).
- Node status from `node_executions` and events: pending, running, completed, failed, waiting_human, skipped; attempt number badge; for `agent_task` nodes: agent, provider, model, tier, tokens, cost (with source badges). `request` nodes show the typed request and a link to the inbox item.
- Layout is computed deterministically (left-to-right layered) so a re-render does not move nodes; supersteps are columns. Positions are not persisted in Slice 1.
- **Checkpoint timeline** below the graph: one marker per checkpoint ordered by lineage (`parent id`), marker kind `entry`, `step`, `response_entry`; branches from time travel drawn as parallel lanes; latest checkpoint is authoritative for run state (brief 12). Selecting a marker puts the graph in read-only "as of checkpoint" mode (earlier checkpoints are never modified). Actions: Resume latest (after a crash), Time travel from here (starts a new lineage branch, confirmation), Replay from `entry`. A run lease holder indicator shows whether this instance owns the run (`run_leases`).
- Large graphs: only the active run's graph is mounted; node components are memoized; off-screen elements are not rendered (React Flow option for visible-only rendering, **unverified** against the pinned version).

**As built (2026-10-09), together with the run explanation (S16).** One page per task run, `#/task/<id>/run[/<run id>]`
(`ui/src/pages/RunView.tsx`), linked from the task page and from each run in the Agent Inspector. The core's `run.explain`
returns the run, its graph (`workflow.Engine.Describe`: nodes, edges with kind, `when`, loop limit), every node execution with
its timestamps, safe point, context manifest, routing decision, the usage recorded while it ran and its Logs id, the run's
checkpoints (kind, step, run status, the nodes of that superstep, the nodes waiting on a human, which one is latest), the MCP
audit events and the latest 300 events. `run.prompt` returns the exact prompt a turn was sent: Finalize-style, it is written
next to the turn's `stream.jsonl` on disk (never the database) and is only served for a recorded execution of a run of that
task. Tabs: Graph (React Flow, `@xyflow/react`, lazy chunk; deterministic layered layout in `ui/src/lib/graphLayout.ts`; node
state from executions; loop edges dashed with their limit; read-only, nodes are not draggable), Turns (each turn expands into
"Why this model", "What it was given" (the manifest: source, included, size, priority, reason), the exact prompt on request,
its usage and a link to its stream), Checkpoints, MCP calls, Events. The checkpoint timeline under the graph puts the graph in
"as of checkpoint" mode (node states from the executions up to that step). Not built: Resume / Time travel / Replay actions
(the engine has no service call for them yet); subworkflow nodes are drawn as one node (their nested view is in the data but
not expanded).

### 6.3 cmdk command palette
Opened with `Ctrl+K`. Groups: Navigate (rooms, DMs, tasks, screens), Agents (focus, interrupt, restart, open inspector), Tasks (create, open, move per allowed transition), Actions (open terminal, toggle dock, switch theme, test connection), Recent. Commands are registered by screens and filtered by context (a command whose feature is not built is not registered). Destructive commands open the same approval flow as buttons; the palette never bypasses an approval. Fuzzy matching is done by cmdk over a bounded list (recent 200 entities); larger sets query the Go side (FTS5) on input (**proposal**).

### 6.4 TanStack Virtual lists
Used for: message lists (bottom-anchored, dynamic row height, "jump to latest" pill when scrolled up, no auto-scroll while the user has scrolled away), task lists, the event log, inbox, changed-file lists, logs. Rules: stable row keys (`id`), measured heights cached per id, overscan small (**proposal**: 6 rows), expanded/collapsed state stored outside the row component so recycling does not lose it, and `role="log"`/`role="list"` semantics with accessible names (section 14).

### 6.5 xterm.js terminal (Slice 1B)
One xterm instance per ConPTY session (go-pty, ConPTY, 80x25 initial then resize via `ResizePseudoConsole`). Output path: Go reads PTY, coalesces to frames, emits a typed event per terminal id (binary payload encoding **proposal**); the UI writes to xterm directly (never through zustand state) and acknowledges bytes consumed so Go can pause reading when the UI lags (flow control, **proposal**, measure on Windows). Input goes back by service call. The embedded terminal is also where CLI logins run (Connections, section 8) so Drydock never handles tokens. ANSI handling server-side uses `charmbracelet/x/ansi` only for stripping/width in logs (phase0 appendix 10); ConPTY re-renders and emits its own cursor sequences, which must be checked on Windows (unverified). Terminal tabs are killed with the process tree when closed.

**As built (2026-10-09).** `internal/terminal` (go-pty: ConPTY on Windows, a Unix pty elsewhere) behind `terminal.open/read/write/
resize/close/list`. A terminal opens in a task's worktree or a project's repository, never at a path the caller names. Output is
kept in a 1 MB ring per terminal and read by offset with a long poll (`terminal.read` waits up to 20 s), so both transports carry
it on the ordinary call path; a reader that falls behind is told how many bytes were dropped. Output is never written to the event
log or the database. Closing a terminal ends its process tree: on Windows the shell is put in its own kill-on-close Job Object
right after it starts (`proc.AttachTree`; D7), on Linux the session is killed. The human's terminal inherits Drydock's full
environment (it is the human's shell, not an agent's). `ui/src/dock/Xterm.tsx` (xterm.js 6 + fit addon, lazy chunk) writes
output straight into xterm. Up to 8 terminals at once. **Unverified on Windows:** ConPTY behaviour, PowerShell as the default
shell (pwsh when installed), the job assignment race for children started in the first instant.

The Logs tab (`ui/src/dock/LogsPanel.tsx`) lists a task's check logs (evidence rows with a log file) and agent streams (the
`stream.jsonl` of each node execution that streamed something) through `logs.list`, and reads them by byte range through
`logs.read` (the tail first, earlier output on request, new output followed while running). An agent stream is rendered as
readable lines (text as it streamed, `[tool]`, `[result]`, `[usage]`, `[error]`); a log is named by an id Drydock gave it, never
by a path. The dock's open state, tab and height are kept per project; Ctrl+J toggles it and Ctrl+Shift+D opens the Diff tab.

### 6.6 Other components
Markdown renderer (sanitized, incremental for streaming), code block with copy, presence dot, usage badge (`reported`/`estimated`/`not reported`), approval card, toast and OS notification bridge (Wails ships a notifications service in `pkg/services/notifications`; behavior on Windows unverified), empty-state panel, error boundary per region, resizable splitter.

## 7. Needs-you inbox

The inbox is the single list of things that wait on a human. It is a projection of tables, not a separate store. Sources and the slice they appear in:

| Item kind | Source | Appears | Actions |
|---|---|---|---|
| Approval (tool permission from ACP/headless permission prompt tool; dirty-worktree deletion; destructive command) | `approvals` (`kind=approval`) | 1A (agent permission requests), 1B (worktree) | Approve, Reject; shows exact action and hash. `EXPIRED` items show greyed with reason. |
| Question | `approvals` (`kind=question`) / `QUESTION` card | 1A | Answer inline. |
| Review (spec gate, diff review) | `approvals` (`kind=review`) / workflow `request` node | 1C-1D | Open Review screen; Accept/Reject/Request Changes. |
| Task `WAITING_HUMAN` | `tasks` | 1B | Open task; clears only by human action (brief 12). |
| Agent `NEEDS_ATTENTION` / watchdog stage 2 | `watchdog trips`, agent state | 1C | Open handoff artifact, evidence of the trip, then resume, reassign or cancel. |
| Connection degraded / adapter drift | `adapter.drift`, connection status | 1A | Open Connections. |
| PR needs action (CI failed, review comments, ready to merge) | `github.*` events | 1E | Open PR. |
| Budget pause | `budget.*` | Slice 2 | Not built. |

Rules: items sorted by age then risk; each shows the originating agent, task, and run; `BLOCKED` is not an inbox item (it clears by itself, brief 12); count badge in the sidebar and in the window title; a new item raises an OS notification once (setting to disable). Answering is idempotent (receipts are idempotent by `(scope, receipt_id)`, brief 18): double clicks and reconnects cannot answer twice. The Resume call routes the answer and writes a `response_entry` checkpoint (brief 4); the UI does not assume success until the `approval.*` event arrives.

## 8. Connections page (brief 5, D1, D2, D3, D9, D10)

Shown first for new users. One card per Connection (agent CLI, later `gh`), status enum `not_installed | unsupported_version | unauthenticated | ready | degraded`. Card fields: name, detected path and version, run mode (`native` / `wsl`), supported range from `compat.yaml`, auth state, last "Test connection" result and time, capability matrix with each capability marked `verified` / `unknown` / `no` (brief 5: report only verified capabilities), warnings, actions.

Actions: **Detect** (re-scan PATH), **Install guidance** (copyable commands, links; nothing is downloaded on the user's behalf), **Log in** (opens the embedded terminal running the CLI's own login flow; Drydock reads only the exit code and re-probes), **Test connection** (runs the conformance short form against the real CLI; for API-key providers: `/v1/models` check then the key goes to the credential store), **View conformance history**.

Per-CLI honest notices:
- Claude Code: D1 text; "Uses your own `claude` login. Drydock never touches credentials." The ACP bridge option is absent in v1.
- Gemini CLI: API-key or Vertex auth only; warning that consumer sign-in was reported retired on 2026-06-18 (SNIPPET-ONLY, re-verify).
- Antigravity: adapter behind an explicit acknowledgement gate stating the terms risk in plain language and recommending an AI Studio or Vertex key (D2). Not enabled until acknowledged; acknowledgement stored with timestamp.
- Codex: the Connection test proves `wire_api = "responses"` against the real endpoint before it can join the cheap tier (D3).
- OpenCode (default cheap-tier agent) and Goose: driven over ACP (D10, D11); WSL recommended by OpenCode's docs.
- **Prerequisites card:** Node 22+ detection (needed by ACP bridges, Gemini CLI, Wrangler, Playwright; D9), Git, and (1E) `gh`. Missing prerequisite shows install guidance only.
- `unsupported_version` shows the verified range; `degraded` shows the failing conformance case and links the log.

## 9. Providers settings (brief 6, 8; D4, D13)

Table of providers: name, base URL, kind (`aggregator|direct|host`), key reference (never the key; shows "stored in Windows Credential Manager" or "encrypted in SQLite" for values over 2560 bytes), `data_class` (`private|trainable`), `tier` (`premium|cheap|free`), status, last check. `trainable` rows carry a persistent badge: "Public documentation questions only; never receives repo content." Detail panel: served models (OpenRouter: catalog from `GET /api/v1/models/user`, refreshed at setup and in the background; D4), tool-call probe result per model, price per Mtok in/out with the editable pricing table's last-updated date, quota (OpenRouter `GET /api/v1/key` plus `usage` per response; rate-limit headers where present; D13), `max_concurrent_agents` for hosts, TLS rule (plain HTTP only on loopback/private addresses; self-signed pinned by fingerprint, brief 16). OpenRouter private routes display "requests carry `provider:{data_collection:"deny", zdr:true}`; fails closed" (D4). Add-provider validates with `/v1/models` before saving. Slice 1A scope: registry, add/edit/test, OpenRouter served-model list. Quota ledger values are shown when present; the UI never invents a limit (Gemini limits are configured by the user, D13).

## 10. Agent Inspector (right column tab; brief 21)

Fields, grouped. Anything the adapter cannot report is `not reported`.

| Group | Fields | Slice |
|---|---|---|
| Identity | agent id, provider, role, expert packs (absent before 3), permission mode, run mode | 1A |
| State | agent state (`STARTING, IDLE, WORKING, WAITING, NEEDS_ATTENTION, PAUSED, COMPLETED, FAILED, STOPPED`), presence, current task, current run/node/attempt | 1A |
| Process | PID, started at, job/tree status ("tree killed" confirmation after stop), working directory, worktree path, branch, dirty flag | 1A (process), 1B (worktree) |
| Session | session id, fresh or continued, resume capability (from ACP `agentCapabilities`, D10), handoff count | 1A |
| Model | provider, model, effort, tier, source of the choice ("team setup" until Slice 2) | 1A |
| **Why this model?** | Slice 1A-1E: "Not routed: static assignment." From Slice 2: the decision record (agent, provider, model, effort, team size, reason, confidence, estimated cost and tokens, policies applied, escalated/deescalated from, alternatives; brief 6) | 2 |
| **Context occupancy** | used / size tokens and percent from the adapter's usage report (ACP `usage_update` {used,size,cost} where present; unstable in the Go SDK, phase0 appendix 5), bar with the 70% marker (idle handoff threshold, brief 5), "not reported" for CLIs without it (Gemini over ACP has no `usage_update`) | 1A display; handoff behaviour per brief 5 when implemented |
| Usage | input, output, cached, cache-write tokens; cost; source badge; per-run and per-task totals; tier | 1A |
| Plan quota | rate-limit window where the CLI emits it (Claude `rate_limit_event`, Codex app-server, Antigravity `/usage`); "not available" for OpenCode/Goose/Gemini (phase0 section 5) | 1A, as supported |
| **Watchdog** | state (`ok`, `warned`, `stage 1 nudged`, `stage 2 paused`), counters: tool calls this turn of 60, elapsed of 20 min, consecutive tool errors of 5, tool-spin window (n of 8), stagnation score; `ToolCallVisibility` flag and "reduced detection" notice when false; trip history with evidence and normalized signature (brief 7) | 1C |
| Capabilities | the capability matrix (brief 5) with verified/unknown | 1A |
| Actions | Interrupt, Pause, Restart, Stop (kills tree), Open terminal in worktree, Open DM | 1A |

## 11. Wails v3 integration (facts from `phase0/go-platform.md` section 1)

- Wails v3 is **beta** (`v3.0.0-beta.28`, pinned, MIT); `@wailsio/runtime` npm `3.0.0-beta.28`. Wails imports exist only in `app/` (brief 2, 20); `internal/` never imports `app/`.
- **Services:** `application.NewService(&T{})` or `NewServiceWithOptions(inst, ServiceOptions{Name, Route, MarshalError})`. Optional `ServiceStartup(ctx, options) error` (ctx cancelled just before shutdown; startup in registration order; an error aborts `App.Run`) and `ServiceShutdown() error` (reverse order). An instance implementing `http.Handler` is mounted on the asset server at `Route`. **Proposal:** one thin service per facade in `app/` (Project, Room, Agent, Task, Approval, Connection, Provider, Workflow, Review, Terminal, Event, Settings, Update, License), each delegating to `internal` interfaces; `MarshalError` maps domain errors to `{code, message, retryable}` so the UI never parses strings.
- **Events:** `app.Event.Emit(name, data...)`, `On(name, cb)` (returns unsubscribe), `OnMultiple`, `Off`; typed events through `application.RegisterEvent[Data](name)`, which validates payloads and feeds binding generation, so the TypeScript event payload types are generated, not hand-written. The Event Bus remains the in-process source (brief 10); Wails events are only the transport.
- **Bindings:** `wails3 generate bindings -ts` (flags include `-d dir`, `-names`). Observed output `frontend/bindings/<pkg>/<service>.ts` with functions such as `Greet(name): $CancellablePromise<string>` calling `$Call.ByID(<number>, ...)` from `@wailsio/runtime`; 259 packages and one service processed in 3.4 s. Calls are by numeric ID (names via `-names`). **Rules:** a Taskfile target regenerates bindings before every UI build; generated files are never hand-edited; CI fails if regeneration produces a diff. `$CancellablePromise` lets the UI cancel a call when a view unmounts.
- **Large payloads:** the `http.Handler` mount can serve range reads of raw output, diffs and artifacts (`GET .../raw/<run>/<node>/<attempt>?offset=&len=`) so big bodies do not travel through events. Seen in source, not exercised (**unverified**); the same channel must apply the output-truncation rule: long tool output is fetched by range, never preloaded (brief 7).
- **Event replay after reconnect (brief 10, 24):** every persisted event has a monotonic `seq`. Protocol (**proposal**): (1) the UI keeps `lastSeq` per window; (2) on startup, window reload, or focus after sleep, it subscribes first and buffers live events, then calls `EventService.Replay(afterSeq=lastSeq, limit)` repeatedly until it returns the head; (3) buffered live events with `seq <= lastSeq` are dropped, others applied in order; (4) every live envelope carries `seq` and `prev_seq` (the previous published seq), and a mismatch triggers another Replay instead of assuming gaplessness (the brief promises monotonic, not contiguous, sequence numbers); (5) the first launch reads a snapshot (current projections via services) at a known `seq` and replays from there. The UI never renders an event it has already applied. Acceptance for 1A: kill the UI process or reload the window mid-stream and the transcript is identical afterward; restart the app and the UI catches up from the log.
- Wails emit latency under streaming load and `ServiceStartup` ordering under crash restart are **unverified** on Windows (phase0 section 7, item 3). Single-instance support exists for Windows (`single_instance_windows.go`); Drydock must enable it because SQLite has a single writer and `run_leases` assume one engine instance.

## 12. Theming (dark and light)

CSS custom properties are the only color source: semantic tokens (`--bg-0..3`, `--fg-0..2`, `--border`, `--accent`, status colors `--ok/--warn/--err/--info`, presence colors, diff add/remove). Default follows `prefers-color-scheme`; a Settings override (System/Dark/Light) is stored in `settings`. The theme is applied by one attribute on the root; no component hard-codes a hex value. Third-party widgets are themed from the same tokens: a Monaco theme pair, an xterm theme object (16 ANSI colors defined for both themes and checked for contrast), React Flow's color mode and edge/node colors from tokens. Status never relies on color alone (icon plus text). Motion: 100-150 ms opacity/transform only, disabled under `prefers-reduced-motion` (**proposal** for numbers). Dark first for contrast checks, light checked equally.

## 13. State management: zustand

Choice: **zustand** (one store per domain: connection, rooms, agents, tasks, approvals, workflow, ui). Reasons: (1) selector subscriptions give per-component re-render control, which matters with a streaming transcript; (2) stores are usable outside React, so the Wails event handler writes straight into the store without a provider tree; (3) small, no boilerplate, easy to test in isolation; (4) no server-cache semantics are needed because the source of truth is the event log, not request/response caches (this is why TanStack Query is not adopted). Rules: store state is derived by a pure reducer `applyEvent(state, event)` shared by live events and replay, keyed on `seq`, so replay and live paths cannot diverge; service calls return snapshots that seed the store at a known `seq`; streaming text lives in an external buffer (section 15), not in the store; persisted UI preferences (theme, panel sizes, last room) go to the settings service, with `localStorage` only as a cache that may be empty.

## 14. Keyboard map (defaults; **proposal**, rebinding is not built in v1)

| Keys | Action |
|---|---|
| `Ctrl+K` | Command palette |
| `Ctrl+Shift+K` | Jump to Needs you inbox |
| `Alt+Up` / `Alt+Down` | Previous / next room or DM in the sidebar |
| `Ctrl+1` ... `Ctrl+4` | Focus sidebar, center, right column, dock |
| `Ctrl+B` | Toggle sidebar |
| `Ctrl+J` | Toggle bottom dock |
| ``Ctrl+` `` | Focus terminal |
| `Ctrl+Enter` | Send message; `Enter` newline in multi-line mode (setting) |
| `/` in empty composer | Slash command list; `@` mentions agents; `#` tasks |
| `Esc` | Close overlay; in composer with a running agent, `Esc Esc` interrupts the focused agent (confirm) |
| `Ctrl+.` | Open Agent Inspector for the focused agent |
| `J` / `K`, `Enter`, `A`, `R` in inbox | Move, open, approve, reject (confirm for destructive kinds) |
| `Ctrl+Shift+D` | Open Diff dock for current task |

Avoid collisions with WebView2 browser accelerators (`F5`, `Ctrl+R`, `F12`, `Ctrl+Shift+I`, `Ctrl+P`); whether Wails lets the app disable them is **unverified**, so production builds must test that none reloads or opens devtools unintentionally.

## 15. Performance rules for streaming output

Targets are goals, not measurements; none has been measured (Windows only).
1. Coalesce in Go: token deltas are batched per run into frames (default window 30-50 ms, **proposal**) before being emitted; the UI never receives per-token events. Persisted events reference byte ranges in raw output on disk (brief 4) rather than carrying every delta, to protect the event table write rate (Linux numbers: 25k rows/s single-row commits, 141k rows/s at 1000-row batches; NTFS unmeasured).
2. Per-frame UI work is `O(visible)`: streaming text is appended to a buffer outside React and flushed to the in-progress card at most once per animation frame; only that card re-renders.
3. Markdown: finalized blocks are memoized and never re-parsed; only the trailing open block is re-parsed per flush. Code blocks highlight after the block closes.
4. All lists virtualized (6.4); a message body longer than a threshold shows head and tail with "show range" (fetch by range), never rendering megabytes.
5. Background rooms accumulate state but do not render; when a room is not mounted its events only update counters and last-message previews.
6. One event listener per event type at the app root, not per component; handlers do `O(1)` store writes and batch updates inside one `requestAnimationFrame` tick.
7. Terminal output bypasses the stores (6.5). Logs dock uses virtualization with a capped in-memory window (**proposal**: 5,000 lines) and fetches older ranges on scroll.
8. Heavy components (Monaco, React Flow, xterm) are code-split and mounted on first use; Monaco models and React Flow instances are disposed on unmount.
9. Backpressure: if the UI lags (frame budget exceeded for N flushes), the UI sends `Replay`-style resync and drops intermediate deltas of in-progress cards, because the raw output is on disk and authoritative.
10. A performance test per slice measures events/s accepted, flush time, and dropped-frame rate with a synthetic-free source (a real agent run or recorded real stream), recorded in `evals.md` notes; no number is claimed before it exists.

## 16. Accessibility basics

Everything operable by keyboard with visible focus rings; logical tab order across regions and landmark roles (`navigation`, `main`, `complementary`, `region` for the dock). Message list is `role="log"` with polite live announcements for new human-addressed items only (not per streamed token); the inbox count is announced when it changes. Status communicated by icon plus text. Contrast at WCAG 2.1 AA (4.5:1 text, 3:1 UI) in both themes, including diff colors, ANSI palettes, and graph edges. Respect `prefers-reduced-motion`. Dialogs trap focus and restore it. Targets at least 24 px. Library accessibility modes (Monaco screen-reader mode, xterm screen-reader mode, React Flow keyboard focus) are to be verified against the pinned versions (**unverified**); the graph also offers a list view of nodes and checkpoints as the accessible alternative. Text scaling and Windows high-contrast mode are tested manually before first release.

## 17. UI delivery by slice

| Slice | UI delivered | Not yet (shown as such) | Verified by |
|---|---|---|---|
| 1A | S1-S9, S19, S20; Connections (agents, API-key providers), Providers, team setup, Room with cards `CHAT/STATUS/RESULT/ERROR/SYSTEM/QUESTION/APPROVAL_REQUEST/APPROVAL`, presence, Inspector (identity, state, process, model, usage, capabilities), basic inbox, replay protocol, usage capture visible | Router explanations, tasks, diff, terminal, workflows | Real agent completes a message with usage under `cheap`; kill agent kills the tree; UI restart catches up from the log |
| 1B | S10-S13; `TASK` cards; Monaco diff; xterm terminal; check tool members; safe points; worktree in Inspector; inbox gains worktree approvals and `WAITING_HUMAN` | Workflow view, watchdog, review screen | Real coding task on the golden starter with real checks |
| 1C | S14 workflow view and checkpoint timeline; watchdog panel in Inspector; stage-2 items in inbox; request-port items | Context manifest viewer, PR view | Engine acceptance tests (brief 4) reflected in the UI without hidden state |
| 1D | S15-S17; `HANDOFF`, `DECISION` cards; MCP audit view; manifest viewer | PR flow, router | The run explained entirely by manifest, usage, checkpoints, events |
| 1E | S18 Pull Requests; `gh` Connection card; CI tool member; review-comment round trip | Integrations, packs | `gh` auth detected; PR created with generated description; human merge via GitHub |
| 2+ | Router UI, budgets, packs, requirements, skills, autonomy, integrations | per section 4 | per `evals.md` |

## Open questions / unverified

- Wails emit latency and ordering under streaming load on Windows; WebView2 shortcut suppression; notification service behavior on Windows; Monaco worker bundling under the asset server; React Flow and xterm option names against pinned versions (all need a Windows spike in 1A/1B).
- Whether streaming deltas are persisted as events or only referenced by byte range (this document assumes ranges); `room-protocol.md` and `interfaces.md` must fix event and message field names, which here are proposals.
- Which slice owns room modes `moderator`/`auto`, budgets UI, and group chats (brief names none; proposals above).
- Exact contents of `compat.yaml` and the adapter manifest fields shown on Connection cards.
- ACP `usage_update` and prompt `usage` are marked UNSTABLE in `coder/acp-go-sdk` v0.13.5; context occupancy display may need adapter-specific sources.
- Rendering of the generated PR description and whether the PR description is editable before push (brief says generated, immutable exports for minutes/PR drafts, brief 11): assumed editable before push, exported copy immutable.
- Accessibility of third-party widgets and minimum window sizes have not been tested.
