# Drydock: evaluation harness plan and results ledger

Scope: how Drydock measures whether it lowers **cost per accepted task** (the primary success metric) and **premium-subscription usage per accepted task** (the second), without lowering quality (brief sections 0, 7, 23). It defines each metric exactly, names the SQLite tables the numbers come from, specifies the naive broadcast baseline the product is compared against, says how runs are recorded and reproduced, holds the per-slice results table, and applies decision P4 (dogfooding) to this ledger. **No measured value exists yet.** Every result cell below says "not measured"; nothing in this document is an estimate presented as data (brief 0: do not claim learning, savings or capability that is not implemented and measured). Table names come from brief sections 4-6, 8 and 18; **column names are provisional** and must be reconciled with the migrations when the schema is written. Where the brief, decisions and appendices are silent, a choice is marked **proposal**. Decisions in `decisions.md` win over the brief where they differ.

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
10. Column names, the `purpose` field, and watchdog label storage are provisional until migrations exist.
