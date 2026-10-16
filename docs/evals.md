# Drydock: evaluation harness plan and results ledger

Scope (updated 2026-10-08 with the Slice 2 evaluation code, section 11): how Drydock measures whether it lowers **cost per accepted task** (the primary success metric) and **premium-subscription usage per accepted task** (the second), without lowering quality (brief sections 0, 7, 23). It defines each metric exactly, names the SQLite tables the numbers come from, specifies the naive broadcast baseline the product is compared against, says how runs are recorded and reproduced, holds the per-slice results table, and applies decision P4 (dogfooding) to this ledger. **No measured value exists yet.** Every result cell below says "not measured"; nothing in this document is an estimate presented as data (brief 0: do not claim learning, savings or capability that is not implemented and measured). Table names come from brief sections 4-6, 8 and 18; **column names are provisional** and must be reconciled with the migrations when the schema is written. Where the brief, decisions and appendices are silent, a choice is marked **proposal**. Decisions in `decisions.md` win over the brief where they differ.

## 1. Principles

1. Every claimed improvement needs numbers, the task set, `n`, the arm, and the commit (brief 23). A number without those is not recorded.
2. Estimated and reported values are different columns and are never summed silently (brief 7). A CLI that does not report a value yields `not reported`, not 0.
3. Real systems only: real CLIs, real Git, real fixture repositories, real checks (brief 0). A mock is allowed only to test a failure boundary and is labeled; mocked runs are never entered in the results table.
4. Runs that cannot execute in the current environment are recorded as **skipped with a reason** (P3): the sandbox has the Claude Code CLI with the session's credentials but no route to OpenRouter, Google or OpenAI and no other CLI logins; Windows-only behavior is unproven while Actions is unavailable (P2).
5. Eval overhead is separated from product cost (section 3.9) so measurement does not inflate or hide spend.
6. Small samples are reported as raw per-task rows with medians and ranges, never with significance claims.

## 2. Vocabulary

- **Task**: one `tasks` row. **Cohort**: a set of tasks run under one arm, one slice build and one configuration (section 5).
- **Accepted task**: a task that reached `COMPLETED` through `VERIFICATION -> COMPLETED`, which requires checks to pass **and** a human accept (brief 12). In the automated eval suite the human accept is replaced by the fixture's hidden acceptance checks plus the reviewer verdict; those runs are labeled `eval_accept` and are reported separately from product accepts (proposal). Dogfooding and field data use real human accepts only.
- **Tier**: provider `tier` of the call's provider: `premium`, `cheap`, `free` (brief 6). Subscription CLIs (Claude Code, Codex on a subscription, Antigravity) are `premium`. `free` is `trainable` and only used for public-docs CONSULT (brief 6, D4), so it never appears in worker runs on private code.
- **Attributed**: a `model_usage` row carries `task_id`, `run_id`, `node_id`, `attempt`, `agent_id`, `provider`, `model`, `tier`, `purpose` (proposal). Rows without a task are **unattributed** (for example app-owned `LLMClient` expert questions).

## 3. Metrics and exact definitions

### 3.1 Cost per accepted task (primary)
A report reads the newest 2,000 tasks of its period at most (`Options.MaxCohort`; a task's facts take about ten reads of the database) and says in its not-measured list how many older tasks it left out (audit 2026-10-16, L18).

`CPAT = (sum of cost over all model_usage rows in the cohort, attributed or not, any task outcome) / (number of accepted tasks in the cohort)`. The numerator includes failed, cancelled, escalated, retried and discarded work, reviewers, watchdog stage-1 retries, handoff generation and unattributed calls made during the cohort window, because waste is part of what an accepted task costs. Also report `CPAT_accepted_only` (numerator limited to rows of accepted tasks) next to it; the gap between the two is the waste share. Cost is split into `cost_reported` (value returned by the provider or CLI: OpenRouter usage credits, API usage) and `cost_estimated` (computed from tokens and the editable pricing table, with its last-updated date recorded; brief 7). Both are published; the headline column is `cost_reported` where every call has one, otherwise the table shows both and flags the cohort `mixed sources`. For subscription CLIs the reported dollar value is an estimate (Claude Code `total_cost_usd`) and the real cost to the user is flat-rate quota; those calls are therefore included in dollars as `cost_estimated` and counted again in metric 3.2.
Source: `model_usage` (brief 8: every call writes a row; brief 7: input, output, cached tokens, context size, cost, sources and exclusions). Denominator: `tasks` where `state = COMPLETED` and the accept receipt exists (`approvals`).

### 3.2 Premium usage per accepted task (second metric)
Premium usage is layered because plan quota is not uniformly observable (phase0 section 5).
- **3.2a Premium tokens per accepted task** = sum over `model_usage` rows with `tier = premium` of input, output, cached-read, cache-write tokens, reported separately (not summed into one number across providers: Anthropic `input_tokens` excludes cached tokens while the normalized view includes them; D12 says normalize once in the writer), divided by accepted tasks (same cohort numerator rule as 3.1: all premium usage in the cohort).
- **3.2b Premium turns per accepted task** = count of premium `agent_task` executions (`node_executions` joined to `model_usage`) / accepted tasks. Turns are comparable across CLIs even when token fields are missing.
- **3.2c Premium quota consumed per accepted task** = change in the provider's quota window utilization between task start and end, divided by accepted tasks, **only where the CLI reports it**: Claude `rate_limit_event` (status, resetsAt, utilization), Codex `account/rateLimits/read` (`usedPercent` per window), Antigravity `-p "/usage"`. Gemini CLI exposes quota only internally; OpenCode and Goose have none, so those rows read `not available`. This value is confounded by usage outside Drydock on the same account and by window resets; it is reported as indicative and never as the headline. Gemini consumer quota may not exist (D2).
- Headline premium metric: 3.2a plus 3.2b. 3.2c is context.

### 3.3 Cheap-only acceptance share
Two variants, both always reported (the brief names the metric; phase0 3.4 phrases it as "accepted with a cheap worker only"):
- **Cheap-worker share** = accepted tasks in which every `agent_task` node with a worker role ran on a `cheap`, `private` model and no escalation occurred, divided by accepted tasks. A single premium final review (brief 6 ladder step 3) does **not** disqualify.
- **Strict cheap-only share** = accepted tasks with zero premium-tier `model_usage` rows (including review), divided by accepted tasks.
Which one is the Slice 2 acceptance metric is an owner decision (open question 1); until then both are published. Tasks in premium-only classes (architecture, security review, ambiguous/high-risk, large-diff SHIP review; brief 6 ladder step 2) are reported as a separate stratum so the share is not diluted by tasks that must be premium.

### 3.4 Premium REVISE rate on cheap-worker tasks
`premium_REVISE_rate = (premium review verdicts equal to REVISE on cheap-worker tasks) / (all premium review verdicts on cheap-worker tasks)`. Also report the per-task form: share of cheap-worker tasks with at least one premium `REVISE`, and the share that hit the escalation rule (`routing.escalated` after two premium REVISE; brief 6). A review verdict that fails to parse as `SHIP` or `REVISE` is counted as `invalid` and excluded from the denominator but reported. Source: review node outputs in `node_executions` / checkpoints and `RESULT` messages carrying a structured verdict field (the verdict must be structured, not parsed from prose; open question 3).

### 3.5 Watchdog trips and false-trip rate
- **Trips per accepted task** = rows in watchdog trips in the cohort / accepted tasks, split by signal (tool spin, error echo, hard cap: tool calls, wall-clock, consecutive tool errors; soft stagnation) and by stage (stage 1 nudge, stage 2 pause with `WAITING_HUMAN`/`NEEDS_ATTENTION`; brief 7). Dead letters with two trips are counted separately.
- **False-trip rate** = (trips labeled false) / (trips labeled), with the label coverage (labeled / all trips) printed beside it. A trip is **false** when a reviewer, looking at the trip evidence (normalized signature, the repeated calls, the diff hash), judges the agent was making progress. Labels come from the Needs-you resolution of a stage-2 trip ("real loop / not a loop") and from offline review of stage-1 trips sampled from `watchdog` evidence. No label is inferred automatically; unlabeled trips stay unlabeled.
- **False-positive control**: the Iterate Until Verified workflow contains a scenario with legitimate repetition (the brief's engine acceptance test); the expected trips are 0 and any trip there is a failed test, reported in section 6.2.
- Adapters without `ToolCallVisibility` run reduced detection; those cohorts are marked `reduced detection` because their trip count is not comparable.

### 3.6 Resume reliability
Measured by the engine acceptance tests (brief 4, Slice 1C, real processes) and by field counters:
- **Acceptance suite pass rate**: approval resume across app kill; partial superstep (finished node not re-run); duplicate resume (only one proceeds); time travel; replay from entry checkpoint; streaming with crash mid-stream; cancellation and loop limits; watchdog cases. Reported as pass/fail per test per run on the Windows runner (blocked by P2).
- **Resume success rate** = resumes that reach the next checkpoint without a duplicated effect and without manual repair / resumes attempted (`workflow_checkpoints` rows of kind `response_entry` and post-crash recoveries).
- **Duplicate effects** = count of side effects executed more than once for one idempotency key `(run_id, node_id, step, attempt)` in `node_executions`. The target is exactly 0; any non-zero value fails the release.
- **Orphan processes after kill** = processes alive after the agent's job is closed (D7). Must be 0.
- **UI catch-up** = events missing from the UI transcript after restart or reconnect compared with the `events` table, by `seq`. Must be 0.

### 3.7 Other brief-23 metrics (defined, collected from the slice that makes them observable)
Tokens and cost per accepted task by role, workflow and tier; completion rate (accepted / started); test success on first check; iterations to `SHIP` (Iterate Until Verified, default five); routing quality (Slice 2: share of routing decisions later judged right by outcome, where `routing.escalated` and `routing.fallback` are failure signals; the judging rule is set when the router exists); context efficiency (manifest bytes included vs excluded, cache-read share, truncated tool output share).

### 3.8 Slicing
Every metric can be broken down by workflow, role, tier, adapter/CLI, model, task class (brief 6 cheap-first classes vs premium-only), diff size band, and project. A breakdown with fewer than a minimum count of accepted tasks (proposal: 5) is shown only as raw rows.

### 3.9 Exclusions and overhead
Rows with `purpose` in {`connection_test`, `conformance`, `tool_call_probe` (D4), `eval_baseline_setup`, `pricing_probe`} are excluded from CPAT and listed as **overhead** with their own totals. Unattributed product calls stay in the numerator. Exclusions are recorded with their reason in the run record so a reader can recompute.

## 4. Data sources

| Needed for | Table (brief) | Fields used (provisional) |
|---|---|---|
| Cost, tokens, tier | `model_usage` | task_id, run_id, node_id, attempt, agent_id, provider, model, tier, input/output/cached/cache_write tokens, context size, cost_reported, cost_estimated, cost_source, source (`reported|estimated`), purpose, ts |
| Pricing | model catalog / pricing table | price in/out per Mtok, cache prices, last-updated date |
| Acceptance | `tasks`, `approvals`, `receipts` | state history, accepting approval, version |
| Routing | routing decisions, rules, `provider_quotas` | model, tier, reason, escalated/deescalated from, `routing.fallback` |
| Execution | `workflows`, runs, `workflow_checkpoints`, `node_executions`, `run_leases` | kind, parent, idempotency key, attempts, lease holder |
| Reviews | `node_executions` outputs, `RESULT` messages | verdict `SHIP|REVISE|invalid`, reviewer agent/model/tier |
| Watchdog | watchdog trips, dead letters | signal, stage, evidence signature, label (`real|false|unlabeled`) |
| Premium quota | `provider_quotas`, adapter quota events | window, used percent/utilization, resets_at, sampled_at |
| Reliability | `events` (monotonic `seq`), `sessions`, `handoffs`, `agents` | restarts, kills, handoff counts, process exits |
| Context | per-run context manifest | source, included/excluded, size, reason, priority |

Quota and usage availability per CLI follows phase0 section 5 and appendix agent-clis section 10; ACP `usage_update` and prompt `usage` are marked UNSTABLE in `coder/acp-go-sdk` v0.13.5, so usage capture is itself validated against real streams in Slice 1A before any CPAT is trusted (open question 5).

## 5. The naive broadcast baseline

Purpose: show what an AI team costs when none of Drydock's controls exist, on the same tasks, so savings are measured and not asserted. It lives in `evals/` (brief 20) as a harness arm, not in the product, and drives the same real CLIs.

| Arm | Definition |
|---|---|
| **B0 naive broadcast** | Team of N agents (same roles as the Drydock arm). Every agent receives every room message and every tool output in full each turn (broadcast context); all agents run on one premium model chosen by the user's default; free-for-all turn-taking: every agent may reply to every message until a stop condition; no router, no cheap tier, no handoff artifacts (full transcripts cross sessions), no truncation, no cache-friendly prompt ordering, no exact-match response cache, no watchdog; sessions continue until context limits. Same hard stop as the Drydock arm so runs end: maximum five iterations and a wall-clock cap (brief 22) and a dollar ceiling set per run. |
| **B1 single premium agent** | One premium agent, no team, same stop conditions. A lower bound on cost for tasks that do not need a team. |
| **D(slice)** | Drydock at the slice under test with its real routing, context engine, turn-taking, handoffs, watchdog and token controls enabled as implemented. Features not built in that slice are off and the row says so. |

Fairness rules: same fixture repository and start commit, same task prompt, same acceptance checks, same CLI versions (from `compat.yaml`), same pricing table date, same machine; arms interleaved in time to spread provider variance; each (task, arm) repeated at least `k` times (proposal: k = 3) because model output is nondeterministic; worktrees are fresh per run. A per-run budget ceiling prevents runaway baseline spend; hitting it is a recorded outcome (`ceiling`), not a discarded run. The baseline needs real credentials, quota and (for cheap arms) a network route to a private cheap provider, so it cannot run in the sandbox (P3) and is entered as "not measured".

## 6. Recording results

### 6.1 Task suite
`evals/tasks/*.yaml` (proposal): `id`, `fixture` (path under `testdata/`, for example `starter-cloudflare-saas`), `start_commit`, `prompt`, `class` (iteration, test-fix loop, boilerplate, tested refactor, exploration, docs, first-pass review, architecture, security review, high-risk), `risk`, `size`, `acceptance` (commands run by the harness, hidden from agents), `expected_roles`. Suite size is an owner decision; proposal: at least 20 tasks spanning cheap-first and premium-only classes before any headline number is published. Fixture repositories are our own, so run records may be committed.

### 6.2 Run record
Each run writes `evals/results/<date>_<slice>_<arm>_<task>_<k>/run.json` plus a copy of the relevant `events` range and the usage rows, from a harness export (not hand-entered). `run.json` fields: commit SHA, app version, slice, arm, task id, repeat index, outcome (`accepted`, `rejected`, `failed`, `cancelled`, `ceiling`, `skipped`), skip reason, Windows build, CLI versions and adapter manifest versions, models and tiers used, pricing table last-updated date, token and cost totals split by source, premium-turn and premium-token totals, quota samples, review verdicts, watchdog trips with labels, resume events, wall-clock time, and the `eval_accept` result. The evaluation uses a separate data directory so that eval runs do not pollute a user's database (proposal).

### 6.3 Ledger
This file's tables are regenerated from the run records by a script (to be written) so rows cannot drift from data; humans add only notes. Each row names its cohort, the commit and `n`. A row is added even for a skipped cohort, with the reason.

## 7. Per-slice results

Legend: `not measured` = no run exists; `n/a (slice)` = the metric cannot be observed in that slice because the feature does not exist yet; `skipped (reason)` = a run was attempted or planned and could not execute. All Windows-dependent cells are `not measured` while P2 holds.

### 7.1 Economics

| Slice | Arm | Commit | n tasks (accepted) | CPAT reported | CPAT estimated | CPAT accepted-only | Premium tokens / accepted | Premium turns / accepted | Quota delta (where reported) | Cheap-worker share | Strict cheap-only share |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1A | D(1A) | not measured | not measured | not measured | not measured | n/a (no tasks) | not measured | n/a (no tasks) | not measured | n/a (static assignment) | n/a (no tasks) |
| 1B | B1 | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | n/a | n/a |
| 1B | D(1B) | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | n/a (static assignment) | n/a |
| 1C | B0 | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | n/a | n/a |
| 1C | D(1C) | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | n/a (static assignment) | n/a |
| 1D | B0 | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | n/a | n/a |
| 1D | D(1D) | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | n/a (static assignment) | not measured |
| 1E | D(1E) | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | n/a (static assignment) | not measured |
| 2 | D(2) | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured |
| 3-5 | D(n) | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured |

### 7.2 Quality and reliability

| Slice | Premium REVISE rate | Watchdog trips / accepted | False-trip rate (label coverage) | Watchdog false-positive control | Resume success rate | Duplicate effects | Orphan processes | UI catch-up misses | Iterations to SHIP | Engine acceptance suite (Windows) |
|---|---|---|---|---|---|---|---|---|---|---|
| 1A | n/a (no reviews) | n/a (watchdog is 1C) | n/a | n/a | n/a | n/a | not measured (tree-kill proof is blocked, D7/P2) | not measured | n/a | n/a |
| 1B | n/a | n/a | n/a | n/a | n/a (trivial runner) | n/a | not measured | not measured | n/a | n/a |
| 1C | n/a (reviews arrive in 1D) | not measured | not measured | not measured | not measured | not measured | not measured | not measured | n/a | not measured (needs Windows runner) |
| 1D | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured |
| 1E | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured |
| 2+ | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured | not measured |

### 7.3 What each slice can honestly report
- **1A:** usage capture only (tokens, cost, source, tier, context size per message); verifies the data pipeline. No CPAT: there are no tasks. Real-agent evidence of an OpenCode agent on a `cheap`-tier model with usage recorded needs an OpenRouter route (P3).
- **1B:** first CPAT on the golden starter with static assignment; B1 baseline feasible with one premium CLI.
- **1C:** resume reliability, duplicate effects, watchdog trips and the false-positive control; first B0 comparison.
- **1D:** first complete loop, premium REVISE rate, run explained by manifest, usage, checkpoints and events.
- **1E:** CI-check runs and PR-flow overhead; `gh` data; no new economic metric beyond CPAT.
- **2:** the router and cheap-first ladder make cheap-worker share, REVISE rate and escalation meaningful; routing quality starts here.

### 7.4 Integration test status (P3)

| Check | Status |
|---|---|
| Claude Code CLI real-session tests | skipped unless the CLI is installed and authenticated in the running environment; reason recorded per run |
| Codex, Gemini CLI, OpenCode, Goose, Antigravity real-session tests | skipped (no logins in the Phase 0 sandbox) |
| OpenRouter, Google, OpenAI calls | skipped (no network route / no key) |
| Windows-only tests (`internal/proc` tree kill, ConPTY, Job Objects, installer) | skipped (Actions unavailable, P2) |

## 8. Dogfooding rule (decision P4)

The brief says to build Slices 2 to 5 through the Engineering Room on this repository and report measured cloud tokens and cost per accepted task here (brief 0, 23), with a fallback to direct work only when the app is itself broken. P4 records that the Engineering Room is a Windows desktop app that cannot run in the cloud sandbox where Drydock is being built, so **Slices 2 to 5 are built directly until the app runs on the owner's machine**.

Consequences for this ledger:
1. **Dogfooding numbers: none.** No dogfooding cost per accepted task is reported or implied. The section below stays empty rather than being filled with estimates.
2. **Trigger to start:** the app runs on the owner's Windows machine, the Windows gate (Actions or owner-run equivalent) is green for the slice in progress, and the owner confirms. From then on each slice is built through the Room.
3. **Recording once started (proposal):** each dogfooded change is committed with a trailer `Drydock-Task: <task id>`; the run records reference those task ids; every direct (non-Room) commit during dogfooding is listed with the reason the app could not be used ("the app is the thing that is broken"), and the share of direct commits is reported with the dogfood CPAT so the number cannot be read without it.
4. **Separate from the fixture suite:** dogfood tasks are real and heterogeneous; their CPAT is not comparable with the fixture-suite CPAT, and selection bias (the developer chooses what to put through the Room) is stated next to every dogfood number.
5. Until then the 1A-1E rows come from the fixture suite on real CLIs where the environment allows it, and from nothing where it does not.

| Dogfood period | Slice built via the Room | Tasks accepted | Direct commits (with reasons) | Measured cloud tokens | CPAT | Notes |
|---|---|---|---|---|---|---|
| none yet (P4) | none | not measured | not measured | not measured | not measured | Slices 2 to 5 are built directly until the app runs on the owner's machine |

## 9. Reporting rules

- Publish per-task rows first, aggregates second; medians with min and max; `n` always present.
- Improvement claims compare the same task set, same repeats, same pricing date, same CLI versions; a changed condition starts a new baseline.
- Cost claims separate `reported` from `estimated`; quota claims appear only where the CLI reports quota.
- Savings versus the naive baseline are stated as the measured ratio of CPAT and premium usage with both arms' `n`; nothing is stated for a metric not measured.
- Any metric whose underlying feature is not implemented is shown `n/a (slice)` and is never back-filled.
- Pricing table changes are logged with dates; old rows keep the price they were computed with.

## 10. Contradictions and decisions affecting this plan

- Brief 0 and 23 require dogfooding from Slice 2; **P4 overrides** (section 8).
- Brief 7 and 21 name Gemini CLI quota as part of the second metric; D2 reports consumer Gemini sign-in retired and gives Gemini API-key or Vertex auth, where no plan quota exists; the premium metric is therefore tokens and turns first, quota where reported (section 3.2).
- Brief 2 lists Codex as a cheap-tier agent; D3 limits it to Responses-API providers, so Codex joins `cheap` cohorts only when its Connection test proves that.
- Brief 6 requires a cheap `private` tool-calling model; D4 makes the choice model-agnostic from the live catalog. Cohorts must record the model actually used rather than a named reference model.

## 11. As built: how each metric is computed

Built in `internal/evals` (2026-10-08, Slice 2). It reads the real database and nothing else: no value is estimated, entered by hand or back-filled, and no number from a run is recorded in this file. `evals.report` renders the tables below as Markdown from the current rows (`Report.Markdown()`), `evals.export` writes the same report and the raw per-task rows as JSON to `<data dir>/evals/`. Where a denominator is zero the figure is printed `n/a (n=0)`, never `0`. A breakdown with fewer than 5 accepted tasks (`MinN`, default 5) prints totals and the raw per-task rows but no per-task average (3.8).

**Deviations from sections 3-4 above, all forced by what the schema holds:** the table and column names are now the real ones (`model_usage`, `tasks`, `task_reviews`, `task_evidence`, `watchdog_trips`, `workflow_runs`, `events`, `routing_decisions`, `routing_outcomes`); false-trip labels come from human actions already in the database (11.8), not from a labeling screen; premium quota deltas (3.2c) are not computed; there is no per-trip `real` label.

**11.1 Cohort and accepted task.** Cohort: tasks created before `untilMs` whose completion time (or last update, if open) is at or after `sinceMs`, optionally for one project. A task is **accepted** when `tasks.status = 'COMPLETED'` and `accepted_by` starts with `human:`. Usage is windowed by `model_usage.ts`. With a project filter, a usage row belongs to the project through its `task_id` (task's project) or, when it has no task, through its `agent_id` (agent's project); a row with neither belongs to no project and is not counted for it.

**11.2 Cost source (never mixed).** One classification, shared with budgets (`budget.CostKindExpr`): `cost_source='reported'` is reported; `'computed_from_table'` is estimated; for rows written by the agent runtime (which leaves `cost_source` at its default `'unknown'` and stores the CLI's own figure in `cost_usd`) a positive figure is reported (or estimated when `source='estimated'`) and a **zero is unknown, not free**; a cache hit is a known zero. Every dollar figure is printed as `reported $a / estimated $b / unknown n call(s)`. Unknown rows are counted and never priced at zero, so any dollar figure is a lower bound when unknown rows exist. The Claude CLI's `total_cost_usd` counts as reported but is the CLI's client-side estimate (token-control s9).

**11.3 Tier.** The tier of a usage row is the tier of the agent's registered provider when the agent has a `provider_id`, otherwise the row's own `tier`. Reason: the agent runtime stamps the tier its caller passes and the core passes `premium` for every agent (`core.agentConfig`), so a cheap-tier OpenCode agent would otherwise count as premium. A subscription CLI agent has no registered provider and stays `premium`.

**11.4 Cost per accepted task (3.1).** `AllWindow` = every usage row in the window whose purpose is not overhead (attributed to a task or not); `AcceptedOnly` = the rows of accepted tasks. Per accepted task = total / number of accepted tasks, only if at least one exists. Overhead purposes (`connection_test`, `conformance`, `tool_call_probe`, `eval_baseline_setup`, `pricing_probe`) are summed apart and excluded from both. Grouped by role (the agent's role; rows with no agent are `(no agent)`), by workflow (the workflow of the task's run) and by tier (11.3); for role and tier the denominator is all accepted tasks (the groups add up to the whole), for workflow it is the accepted tasks of that workflow.

**11.5 Premium usage per accepted task (3.2).** Over accepted tasks: premium **turns** = distinct `agent_run_id` among premium-tier rows; premium **calls** = premium rows with no agent run (app-owned LLM calls); premium **tokens** = input, output, cached and cache-write tokens as recorded, kept per provider and never summed across providers (D12). Quota consumed is `not available`: no CLI rate-limit sample is stored.

**11.6 Cheap-only acceptance share (3.3).** Over accepted tasks that have usage rows for their worker (the rest are counted as unclassified). *Cheap-worker*: every row of the task's assignee is cheap tier and the task did not escalate (a `routing.escalated` event or a stage-2 watchdog trip); a premium review does not disqualify. *Strict cheap-only*: not one premium-tier row on the task. Stratum: from the task's routing decisions (`TaskType` architecture or security_review, or `Risk`, is `premium_only`; any other known type is `general`; no decision is `unclassified`).

**11.7 Premium REVISE rate on cheap-worker tasks (3.4).** Cheap-worker tasks are classified by the worker's tier alone (an escalated cheap-worker task stays in, because its premium verdicts are the point). A *premium verdict* is a `task_reviews` row by a non-human reviewer whose usage rows on that task include a premium-tier row; a reviewer with no usage rows is unknown tier and excluded (counted), human verdicts are excluded (counted). Reported: premium REVISE / premium verdicts; tasks with at least one premium REVISE / cheap-worker tasks with a premium verdict; escalated / cheap-worker tasks. `task_reviews` only stores SHIP or REVISE (an unparseable answer is stored as REVISE), so the `invalid` verdict class of 3.4 cannot be reported.

**11.8 Watchdog (3.5).** Trips are `watchdog_trips` rows other than `reset` and `stagnation` in the window (stagnation is a soft warning, counted apart; `reset` is a human resuming); split by signal and stage; per accepted task = trips / accepted tasks. The report's `falseTrips` counts trips a human **overrode** inside the same run: a `reset` row for the task after the trip and before the task's next workflow run began, or a human acknowledgement of its Needs-you item (`room_inbox_acks`, `watchdog:<id>`). As built this is not a false-trip label (audit F19, 2026-10-11): answering a pause so that a genuinely stuck agent tries again also resets it, so real trips can be counted, while a trip nobody acted on counts in neither direction. No person is asked whether a trip was real, so the false-trip rate of section 3.5 is **not measured**; the report's note says so. Measuring it needs an explicit real/false answer on the pause. The system never labels a trip itself.

**11.9 Completion, tests, iterations (3.7).** Completion = accepted / started (`started_at > 0`). First-check test pass = tasks whose first `test` evidence passed / tasks with test evidence; final = accepted tasks whose last `test` evidence passed / accepted tasks with test evidence. Iterations to SHIP = for accepted tasks, the number of non-human reviews up to and including the first SHIP (median, min, max, n; tasks shipped only by a human are not in it).

**11.10 Resume reliability (3.6, database part only).** Runs with a `workflow.recovering` event in the window, plus runs that failed their recovery (`failed_reason` starting `recover:`). Recovered = later COMPLETED or WAITING_HUMAN; failed = FAILED; running and human-cancelled runs count neither way. Reliability = recovered / (recovered + failed). Duplicate effects, orphan processes and UI catch-up are not derivable from the database and are listed as not measured.

**11.11 Routing quality (3.7).** From `routing_decisions` joined to `routing_outcomes`: a decision is *good* when its task was accepted and not escalated, bad when the task failed, was cancelled or escalated, and pending while the task is open. Grouped by `decided_by`, tier, provider and model. Agent work and review turns are routed only for an agent whose human listed the models the router may choose between (Team page; `internal/core/turnrouting.go`); those decisions are `decided_by` `rules` (or `explicit`). For every other agent the extension records one `static` decision per turn that is simply the agent's own configured adapter and model, so for them this section measures configured assignments, not router choices.

**11.12 `routing_outcomes`.** One row per routing decision of a task when it ends (`task.completed`, `task.failed`, `task.cancelled` on the event bus; the tasks service's `OnTerminal` hook is taken by the core), or one task-level row (`decision_id = ''`) when the task had none. Idempotent on `(task_id, decision_id)`; a cursor in `settings` replays terminal events missed while the app was down. Columns hold the same quantities as above (iterations, SHIP/REVISE counts, premium REVISE count, escalated, checks passed, watchdog trips, tokens, cost split by source with `cost_source` of none/reported/estimated/mixed/unknown, `premium_subscription_units` = premium tokens, premium turns, wall time, failure class). `mixed` is also used when some calls have no cost at all, so a lower bound is never labeled `reported`.

**Verified by:** `internal/evals/evals_test.go` seeds a real SQLite database with rows shaped like the app's, computes every figure above by hand in the test comments and compares; it also runs the empty-database, small-n, filter and recorder-restart cases. These tests prove the arithmetic and the n/a rules. They are not product measurements and no cost, saving or acceptance figure from them belongs in section 7.

## Open questions / unverified

1. Which "cheap-only" definition is the Slice 2 acceptance metric (cheap-worker share vs strict)? The owner has not said.
2. Task suite size, repeat count `k`, and minimum `n` per breakdown are proposals.
3. Review verdicts must be a structured field; the storage location for verdicts is not defined in the brief.
4. Whether the human accept in field data can be sampled for later regression (accepted then reverted) is not in the brief.
5. ACP `usage_update`/`usage` are UNSTABLE; real stream captures for cost, cached-token figures and cache-write were never obtained (Phase 0 had no logins). Usage correctness must be validated against real streams before any CPAT is trusted.
6. OpenRouter live prices, a paid `private` cheap model with tool calling, and `:free` data policy are unverified (D4); no cheap-tier cohort can run until the owner supplies a key and a preference.
7. Premium quota deltas depend on CLI-reported windows; Claude's rate-limit event availability varies by CLI version (reported from 2.1.181), Codex needs the `codex app-server` side channel, Antigravity `/usage` is SNIPPET-ONLY.
8. The `eval_accept` proxy versus a real human accept: how closely hidden checks track human acceptance is unknown.
9. GPU-host throughput for local cheap-tier hosts (1, 2, 4 concurrent agents) is unmeasured and affects `max_concurrent_agents` defaults.
10. Column names, the `purpose` field and watchdog label storage are now the real ones (section 11); the `tier` stamped on agent-runtime usage rows is wrong for agents on a registered cheap provider and is corrected at read time (11.3), not at write time.

## 12. First measured data point (2026-10-08, n=1: an observation, not a statistic)

Measured with `DRYDOCK_REAL_CLAUDE=1 go test ./internal/core -run TestRealClaudeLoopThroughTheCoreAndTheEvalReport` on the Linux development sandbox: the product loop through the same UI methods the app uses, with real Claude Code 2.1.294 (both agents configured with model `haiku`, the owner's login) as worker and as independent reviewer on `testdata/fixtures/gocalc` ("implement Subtract"), a human approval, and the merge. Numbers come from `evals.report` over the real database:

| Accepted tasks | Premium turns per accepted task | Cost per accepted task (CLI-reported) | Tokens (in / out / cached / cache-write) | Iterations to SHIP | First test run passed |
|---|---|---|---|---|---|
| 1 | 2 (one worker turn, one review turn) | $0.0983 | 2,993 / 1,076 / 178,049 / 36,926 | 1 | yes |

What this does and does not say: it is one task of trivial size, so it is not a baseline and supports no claim about savings. The review turn may not have run on haiku: at the time the reviewer ran in Claude Code's `plan` permission mode, and Claude Code answers a plan-mode session started with `--model haiku` with Sonnet (found later and measured with 2.1.296; see `RunSpec.ReadOnly` in `internal/agents/agents.go`). The `model_usage` rows of this run were not re-checked, so the $0.0983 may include a Sonnet review turn. Since 2026-10-09 the reviewer runs read-only in the default mode with every writing tool denied (`internal/core/core.go`, `agentConfig`) and keeps the configured model; the measurement has not been repeated. Most of the cost is Claude Code's own context overhead (the cached and cache-write tokens), which is why a two-turn task costs about ten cents even though the code change is one line. The cheap-tier share is 0/1 because both agents ran a subscription CLI (premium); no cheap-tier model was used, so none of the routing economics (cheap-first, escalation) has been measured yet. Neither agent had a list of models for the router, so its decisions are `static` and the routing-quality section measures configured assignments. The B1 baseline arm is built since 2026-10-10 (section 13); B0 is not. Nothing was measured on Windows.

## 13. Head-to-head trial: Drydock against plain Claude Code (B1)

Built 2026-10-10 on the owner's request ("a head-to-head trial against plain Claude Code"): `internal/trial`, `cmd/drydock-trial`,
`evals/` (decision P34). It answers one question with measured runs: on the same task, does Drydock's loop get an accepted
result as often as one plain Claude Code session, and at what cost and time?

**The arms.** *D*: Drydock's plain workflow through the core's own methods (the ones the UI calls), on a fresh core per run: a
worker and an independent reviewer, both Claude Code with the same model, the project's checks, up to five iterations, and the
per-task sandbox (P30) for the worker's commands. At the merge approval the hidden acceptance check stands in for the human:
it passes and the harness approves the merge, it fails and the harness cancels the task (`rejected`). Any other question
Drydock asks a human ends the run as `failed` (no human answers in a trial). *B1*: one plain `claude -p` session with the
task as its prompt, `--permission-mode acceptEdits --allowedTools Bash`, on this machine, no reviewer.

**What both get.** The same fixture as one commit on `main`; the same prompt (title and text); the same model; the same CLI
version; the same dollar ceiling (B1 `--max-budget-usd`, D a project budget) and wall-clock cap; the same trimmed environment
(Drydock's agent allowlist, outside the session that started the harness, background tasks off); arms interleaved, so neither
always runs first. Acceptance is the same for both, on a copy of the result: the task's hidden test files laid over it, then
`go vet ./...` and `go test -count=1 ./...` (the arm's own tests included), offline. Cost is Claude Code's own
`total_cost_usd` in both arms (API list prices; not what a subscription costs).

**The suite** (`evals/tasks`, fixture `testdata/fixtures/saasapi`, a small multi-tenant billing API in standard-library Go):
`saasapi-invoice-rules` (a money bug with exact rounding rules), `saasapi-rate-limit` (per-key sliding window, Retry-After,
concurrency), `saasapi-csv-export` (tenant isolation, RFC 4180, spreadsheet formula injection), `saasapi-invoice-pages`
(cursor pagination that neither skips nor repeats under concurrent writes). Every hidden check is shown to fail on the
untouched fixture and pass on a reference solution (`drydock-trial selfcheck`, and `internal/trial`'s tests).

**Limits, stated rather than hidden.** Four tasks, not the twenty this plan asks for before a headline number; one model per
trial. B1 can read the whole machine (a session whose tool calls name the hidden checks is reported and not counted). D's
sandbox image here is built offline from this machine's programs: Go and a basic toolset, no `git` (which does not work in a
task's container anyway, P30), while B1 has the whole host. Linux only.

**Found by the harness before the pilot.** The first smoke run's D worker had no gateway: Claude Code starts the gateway's
stdio shim as `<executable> mcp-shim`, and the harness binary did not serve it, so the worker could not run a command and
finished "not verified". The harness was fixed, the run discarded, and the product now stops a turn whose gateway failed to
connect instead of letting it work blind (P33).

**Pilot results (2026-10-10; a pilot, not a headline: four tasks, k = 3, one model).** Generated reports:
`evals/results/2026-10-10-pilot-checks-report.md` and `evals/results/2026-10-10-pilot-report.md`; every run's record is next to
them. Drydock commit 895299e, Claude Code 2.1.296, model `haiku` (reported `claude-haiku-5-5`), Linux.

The planned arm D ran the worker in the per-task sandbox. That trial (`2026-10-10-pilot`) stopped after one pair because this
environment's container engine stopped at its session time limit: on CSV export both arms were accepted (D $0.0227 in 1m49s,
B1 $0.0150 in 53s). The pilot was then run in full with D's worker running the project's own checks on the machine instead
(P24, trial `2026-10-10-pilot-checks`):

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 12 | 12 | $0.3255 | $0.0271 | $0.0229 ($0.0135 to $0.0501) | 1m22s (46s to 3m9s) |
| B1 | 12 | 12 | $0.1972 | $0.0164 | $0.0165 ($0.0061 to $0.0292) | 1m8s (29s to 3m55s) |

Per task, D's median cost per run against B1's: CSV export $0.0235 against $0.0154, invoice pages $0.0407 against $0.0231,
invoice rules $0.0142 against $0.0086, rate limit $0.0223 against $0.0188. D's reviewer asked for changes in 2 of 12 runs
(CSV export #3, invoice pages #3); both then passed. Whether those first versions would have failed the hidden check, and what
the reviewer objected to, was not recorded: the harness exported only the first 500 events of a task. It now reads them all
and keeps each review's feedback in `run.json`.

**What this says.** On four small, fully specified tasks, one plain haiku session already passed every hidden check, so
Drydock's worker-plus-reviewer loop cost more (about 1.4 times per run at the median, 1.65 times over all runs) and took a
little longer without changing the outcome. **What it does not say:** anything about a quality difference (no task here
defeated the plain session, so there was nothing to catch), larger or vaguer tasks, other models, or what the sandbox, the CI
gate and the human approval protect against, which an acceptance rate does not measure. To learn where Drydock earns its cost,
the suite needs tasks a single session fails often enough to tell the arms apart: multi-file features, fixes that break
something elsewhere, requirements whose obvious implementation passes the visible tests and not the hidden ones; at least 20
of them (section 6.1), three repeats each, and a stronger model as well as haiku.

### 13.1 The hard suite: `tally`, 23 tasks (built 2026-10-10)

Built on the owner's request ("build the harder trial with 20+ tasks") after the pilot could not tell the arms apart.

**The fixture.** `testdata/fixtures/tally`: a multi-tenant billing platform for SaaS companies in standard-library Go, about
6,000 lines in 13 packages (store, money, billing, payments with a fake gateway, subscriptions and renewals, webhooks with
signing and delivery, jobs, mail, reports, snapshots with a migration, auth, a 40-endpoint JSON API, and `apitest`, which
builds the whole service with fakes), with its own tests (about 70). It holds the bugs some tasks are about, planted the way
such bugs appear in real code: an email index shared across tenants, an unscoped lookup copied into one handler, a
delete without the tenant, an error message that confirms another tenant's object exists, `AddDate` at month ends, a
renewal that bills before it moves the period, an invoice view that indexes `lines[0]`, a list that JSON-copies every row
before paging, and a 2-decimal assumption in money parsing and formatting.

**The tasks** (`evals/tasks/tally-*.yaml`, suite `tally`): 6 security reviews (tenant isolation, a role matrix, SSRF in
webhook URLs, timestamped webhook signatures with rotation, hashed API keys with a stored-data migration, invite tokens),
10 iterations (proration, tiered pricing, coupons, webhook retries, soft delete, audit log, CSV import, seat limits,
invoice numbers with snapshot version 3, time-zone reports), 4 test-fix loops (month-end renewals, duplicate renewal
invoices, currency minor units, an incident from a log line), 2 high-risk changes (idempotent payments, graceful
shutdown) and 1 tested refactor (making the invoice list fast without changing a byte). Each prompt is a precise ticket: what
to build and every rule the hidden check holds it to, nothing the check does not test. 7 are high risk, 16 medium; 7
large, 14 medium, 2 small.

**The hidden checks** (`evals/testdata/hidden/tally-*`) go through the HTTP API and the Go names the prompt gives, with
the fake clock instead of sleeps and deterministic concurrency (a held gateway charge, channels). Each is proven to fail
on the untouched fixture and to pass on a reference solution (`evals/testdata/solutions/tally-*.patch`) together with the
fixture's own tests: `drydock-trial selfcheck -suite tally`, and `internal/trial`'s tests (which CI also runs on Windows).

**How the checks were reviewed.** Five agents wrote the checks and solutions, one per area, from one brief: test what the
prompt says, all of it, and nothing it does not; accept every reasonable reading where the prompt is silent; no sleeps to
move time; pass on Windows too. Each ran its reference solution with single requirements taken out (deliberately broken
variants: 65 for the four billing tasks alone, nine for graceful shutdown) to show the check catches each one, and with
alternative designs to show it accepts them. Every concurrency check passed 20 repeats on its solution (most with all four
CPUs kept busy) and failed on the fixture every time; the speed check passed 3 repeats under full load. Decisions taken on
their reports, before the freeze:

- The tiered-pricing prompt said "No usage: no invoice, as today", which is not true of the fixture (it bills an empty
  invoice: the zero-line task's bug); "as today" was removed. The currency check no longer requires exact prices above 2^53
  minor units: the prompt does not ask for it, and the fixture itself formats prices through floats.
- Strict where the prompt is explicit: a webhook delivery's JSON shows `"next_attempt_at": null` (the prompt: it "shows"
  it, "null when no attempt is planned"); a renewal with a working card is charged exactly once (the prompt says at most
  once, and never charging would break renewals); the audit log's `changes` name exactly the fields whose value changed.
- Where two rules of a prompt overlap, both answers pass (the only owner demoting themselves: 403 or 409); boundaries the
  prompts leave open (exactly 7 days, 24 hours, 30 days, 60 seconds) are not tested.
- The invoice-list speed check judges each request by the fastest of up to 10 tries against the prompt's 250 milliseconds,
  so load does not fail a fast implementation (about 1 ms a page). One that still sorts every invoice on each request (100
  to 240 ms a page here, not "well under 100") may pass or fail with the machine's load; both arms run under the same load.

**Pre-registration.** The suite, its hidden checks and solutions, and the harness were committed before the first run of
this trial; no task, check or solution is changed after a run of it has started (a fix to a check would start a new
trial). The analysis is fixed here too: per arm, acceptance and cost per accepted task with medians and ranges; per task,
D's acceptance against B1's (the report's task-by-task table); and, because the suite meets decision P34's size (at least
20 tasks with 3 counted runs per arm), an exact two-sided sign test over the tasks where the arms' acceptance differs. A
claim is made only from that table and test. Caveat found by the audit of 2026-10-11 (F19): 46 of the 69 D records carry a
Drydock commit marked `+dirty` (the binary was built from a tree with uncommitted changes); the caveats below argue the trial
paths did not change, which the audit did not verify independently. `drydock-trial` now refuses to run from such a binary
unless `-allow-dirty` is passed.

**Conditions.** Model `haiku` first, three repeats; arm D in checks mode (P24), because this environment has no container
engine running (the sandbox pilot showed the sandbox works; it changes where commands run, not the review loop). A run
gets at most $3 (B1 `--max-budget-usd`, D a project budget) and 40 minutes. The runs are sharded over parallel processes
(`drydock-trial run -suite tally -tasks ...`), each with its own core and CLI sessions.


### 13.2 Result: haiku, three repeats (2026-10-10)

The report is `evals/results/2026-10-10-tally-haiku-report.md` (`drydock-trial report -trial 2026-10-10-tally-haiku`), with
the 138 run records next to it. Claude Code 2.1.296; `--model haiku` was answered by `claude-haiku-5-5` in every run of
both arms. The runs went in 12 parallel shards on one 4-CPU Linux machine, so wall times include waiting for the CPUs.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 69 | 68 | $9.0503 | $0.1331 | $0.0870 ($0.0305 to $0.5487) | 4m7s (1m34s to 9m39s) |
| B1 | 69 | 66 | $8.6999 | $0.1318 | $0.0681 ($0.0222 to $0.4247) | 3m13s (59s to 7m15s) |

Read per run first: the median D run cost 28% more than the median B1 run ($0.0870 against $0.0681) and took about a minute
longer; over all runs D cost 4% more. Cost per accepted task came out about the same only because D was accepted on two more
runs.

**The pre-registered comparison.** Task by task, D was accepted more often than B1 on 2 tasks (graceful shutdown and webhook
SSRF: 3 of 3 against 2 of 3), less often on none, and as often on 21 (every one 3 of 3 in both arms except proration, 2 of 3
in both). The exact two-sided sign test over the 2 tasks where the arms differ gives p = 0.5. By class: high-risk 6 of 6
against 5 of 6, security review 18 of 18 against 17 of 18, iteration 29 of 30 each, test-fix loops 12 of 12 each, the
refactor 3 of 3 each.

**The four rejections**, each re-run three times on the run's own result (each failed the same way every time, so none is
a flaky check), and each a rule its prompt states:
- B1, graceful shutdown, repeat 2: a second Shutdown while the first is waiting does not return at once ("a second call
  ... returns at once").
- B1, webhook SSRF, repeat 2: `https://[fe80::1%25eth0]/` is accepted (link-local with a zone; the prompt lists fe80::/10).
- B1 repeat 3 and D repeat 2, proration: an upgrade while a quantity downgrade is pending applies the pending quantity
  instead of clearing it ("A later upgrade ... clears the pending change"). D's reviewer shipped that run on its first look.

**The review loop.** In 17 of D's 69 runs the reviewer asked for changes at least once (13 runs once, 4 twice), and all 17
ended accepted; the run D lost was one the reviewer shipped at once. Whether those 17 first versions would have failed the
hidden check is not known, because a record keeps only the final diff; the next trial should keep the version each review
saw and run the hidden check on it too.

**What it says.** With haiku and precise tickets, one plain Claude Code session solved this suite almost as reliably as
Drydock's worker and reviewer: 66 of 69 runs against 68 of 69. Drydock's two extra tasks point the way one would hope (a
concurrency rule and a security edge case), but two tasks of 23 are no evidence of a difference (p = 0.5). By decision
P34's rule this trial supports neither "Drydock beats plain Claude Code" nor the opposite: on this suite they did about the
same. Cost per accepted task was the same ($0.133 against $0.132): a D run costs more at the median ($0.087 against $0.068)
and takes about a minute longer, and its extra accepted runs paid for that. A suite that can tell the arms apart needs tasks
one session fails often: tickets that leave rules implicit in the codebase, as real tickets do, instead of stating every
rule the check holds them to; larger multi-step changes; or a weaker model. The review-loop counterfactual above belongs
in it.

**Caveats.** One model, one machine, Linux; D ran in checks mode, not in the sandbox. Cost is Claude Code's own estimate
(`total_cost_usd`, at API prices); every session reported one. Repeats 2 and 3 record Drydock as `babb3c1a149b+dirty`: the
harness took the repository's HEAD when the batch started, and by then the repository had moved on, with uncommitted
Windows fixes in internal/checks and internal/core. The binary that ran all 138 runs was built from cba4f9e, and nothing
under testdata/fixtures/tally, evals/tasks, evals/testdata, internal/trial or cmd/drydock-trial differs between the two
commits. The harness now records the commit its binary was built from. The speed task's sensitivity to load (section 13.1)
did not come into play: every run of it was accepted in both arms. The trial ran before decision P36 (2026-10-10): every
session Drydock starts now carries about 17,600 fewer tokens in each request (the person's skill list, tools no Drydock
session uses), so D's cost figures above are from the heavier setup; B1 is plain Claude Code and is unchanged. Not re-run.

## 14. Skills: none, all in every turn, or only the relevant ones (2026-10-10, decision P37)

**Question** (the owner's: "do integrations, skills and MCP servers help build a better product, or hurt?"). Every
accepted skill used to go to Claude in full in every turn, whatever the task. Does that help, cost, or hurt, and does
giving only the relevant ones lose anything?

**Design.** Arm D only, the `tally` suite (23 tasks, section 13.1), haiku, two repeats each, checks mode, Drydock
`60019f9be6f2`, Claude Code 2.1.296 (`claude-haiku-5-5` answered every run), after decision P36. Three variants, run at the
same time in 12 shards on one 4-CPU machine:
- **none**: the project has no skills.
- **all**: the twelve skills of the prebuilt agents (Cloudflare, Workers, Wrangler, Durable Objects, React, composition
  patterns, frontend design, web design and writing guidelines, Microsoft docs and code reference, MCP builder; 87 KB of
  `SKILL.md`, fetched at the catalog's pinned commits by `drydock-trial fetch-skills`), accepted for both agents and put into
  every turn as `.claude/rules`. None of them is about a Go billing service: this is what a person gets after installing a
  few prebuilt agents' skills, on a task they do not cover.
- **relevant**: the same twelve, with skill delivery `relevant`: none has globs, so all twelve are listed in `AGENTS.md` and
  their text is in `.claude/drydock-skills` for the agent to read.

Run with `drydock-trial run -arms D -skills-dir <bundle> -d-skills all|relevant`; compared with `drydock-trial compare`
(`evals/results/2026-10-10-skills-compare.md`, 138 run records next to it).

| Variant | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Prompt tokens per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|---|
| none | 46 | 45 | $4.3668 | $0.0970 | $0.0583 ($0.0207 to $0.4096) | 1,345,194 (391,839 to 4,151,216) | 4m0s (1m38s to 18m34s) |
| all | 46 | 43 | $8.2401 | $0.1916 | $0.1221 ($0.0407 to $0.5641) | 2,359,046 (889,117 to 7,658,851) | 3m51s (1m47s to 13m46s) |
| relevant | 46 | 45 | $4.3790 | $0.0973 | $0.0586 ($0.0240 to $0.3890) | 1,556,865 (347,963 to 5,072,330) | 3m55s (1m21s to 7m31s) |

Prompt tokens are fresh input plus cache reads and writes over every turn of a run. Over all runs, cache writes were 5.2 M
(none), 8.6 M (all) and 5.2 M (relevant) tokens; cache reads 67.6 M, 117.8 M and 72.4 M.

**Result.**
- **All skills in every turn cost 1.9 times as much and did not help.** Cost per accepted task $0.19 against $0.10; every
  turn re-read the 87 KB. Acceptance was 43 of 46 against 45 of 46: task by task worse on 2 tasks (graceful shutdown 0 of 2
  against 1 of 2, renewal dedupe 1 of 2 against 2 of 2), better on none. With two repeats per task this is below the size decision P34 asks for (20 tasks, 3 repeats), so no
  significance test applies and no claim of harm to quality is made either. (An earlier version of this section quoted sign
  test p-values here; the comparison tool printed them regardless of size until the audit of 2026-10-11, F19.)
- **Relevant cost the same as none** ($4.38 against $4.37; the index added about 16% prompt tokens at the median) and was
  accepted as often (45 of 46; better on graceful shutdown, worse on proration; no test, as above).
- **The agents opened none of the twelve** in the relevant runs (0 reads in 46 runs): right, since none applies.
- The five runs not accepted: three on the graceful-shutdown rule "a second Shutdown returns at once" (two in all, one in
  none; plain Claude Code failed the same rule in section 13.2), one proration rule (relevant), one iteration limit (all).

**Follow-up: does an agent open a skill that does apply?** Six API tasks of the suite (coupons, CSV import, soft delete,
audit log, seat limits, idempotent payments), one run each, relevant delivery, the twelve plus one skill written for this
codebase (`tally-api`: how Tally's handlers answer errors, read bodies, page lists, scope by organization and are tested;
written from the fixture's own code, not from the hidden checks; globs `internal/api/**`, `internal/apitest/**`). Trial
`2026-10-10-skills-probe`. All six accepted; the agents opened `tally-api` in **2 of 6** runs and none of the others. A
skill made for the codebase, offered only by its name and description, was missed four times out of six.

**Decision (P37).** The default skill delivery is `relevant`, decided by where a skill comes from: the repository's own
skills (`.aet/skills`, written for this codebase, the kind the follow-up found missed when only listed) load into every
turn, except that one its author scoped with `paths` is listed when the task names files and none of them matches; a skill
imported from a registry (a vendor's, a prebuilt agent's: the kind the main trial measured) loads only when its globs match
the task's file hints, and is otherwise listed. `all` stays available per project. A first version scoped hint-less tasks
to the files on disk in the worktree; a code review showed that walk was slow, ignored `.gitignore`, counted Drydock's own
generated files and files an agent made, and loaded broad registry skills; origin replaced it, and a second review kept a
repository skill's own `paths` in force.

**How the harness gives skills.** `-skills-dir` commits skills into the task's repository, so they are the repository's
own; `-prebuilt-skills` imports the prebuilt agents' skills from their registries at the catalog's pins, as installing those
agents does. The main trial and the follow-up used `-skills-dir` for every skill (`-prebuilt-skills` came after), so the
twelve prebuilt skills were repository skills there. None of the twelve declares `paths`, and the harness's tasks carry no
file hints, so the first version's rule (a skill loaded when its globs matched a file of the worktree) listed all twelve,
which is what the shipped rule does with an imported skill without `paths`: for these twelve the measured `relevant` variant
matches what a person gets after installing the prebuilt agents. A registry skill that does declare `paths` loads under
the shipped rule when a task's files match them; no measured run had one. Under the shipped rule the same twelve given
through `-skills-dir` would load into every turn (repository skills without `paths`), which is the `all` variant's cost.

**Caveats.** One model, one suite with precise tickets where skills could hardly help (acceptance near the ceiling), one
machine under load (wall times include waiting for CPUs). The follow-up is six runs: an observation, not a rate. The
origin rule was set after the follow-up and is covered by tests, not by another paid run. A suite whose
tickets leave rules to the codebase's skills is what would show whether skills raise acceptance.
