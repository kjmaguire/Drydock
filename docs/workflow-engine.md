# Workflow engine (`internal/workflow`)

Scope: the in-house orchestration engine from brief section 4. It runs graphs of nodes in supersteps, checkpoints to SQLite after every step, pauses at human request ports, and resumes exactly once after a crash. It has no dependency on Wails or on any agent provider. This document is the contract the code and the acceptance tests follow; where the code diverges, the code's tests win and this file is fixed in the same change.

## 1. Why we do not use Microsoft Agent Framework for Go

Phase 0 (`evidence/phase0/go-probes/maf`) measured its resume behavior with a fan-out of A, B, C into a barrier, B failing:

| Observation | Consequence for Drydock |
|---|---|
| Checkpoint written only after the whole superstep; on failure A and C (already successful) re-ran on resume (counters 2/2) | Per-node `node_executions` rows; completed nodes of an interrupted step are reused, not re-run |
| Two resumes of one checkpoint both ran everything and emitted two outputs | Per-run lease plus request consumption by compare-and-swap |
| No automatic crash recovery; host must rebuild the workflow identically | `Recover()` scans for runs not in a terminal or waiting state and resumes them under a lease; topology hash check |
| Executor-local state is not snapshotted unless hooks are implemented | All node state lives in the checkpoint (shared state map and inboxes); handlers are stateless functions |
| No workflow-level middleware or retry | Our middleware pipeline and retry policy |

We reuse its ideas (supersteps, typed request ports, `Parent`-linked checkpoints, pluggable store), not its code.

## 2. Model

```
Graph{Name, Nodes, Edges, Entry}
Node{ID, Kind(agent_task|check|request|route|subworkflow), Handler, Request?, Sub?}
Edge{From, To, Kind(direct|conditional|fanout|fanin|loop), Cond?, MaxIter?}
Message{From, To, Value(json), Iteration}
```

Graphs are Go values built by builders (`Sequential`, `FanOutFanIn`, `Loop`, `Conditional`, `Debate`); they are never serialized. A checkpoint stores a topology hash (`sha256` of sorted node ids, kinds and edges); resume refuses a graph whose hash differs (`ErrTopologyMismatch`).

Rules enforced at `Graph.Validate()`: unique ids; every edge endpoint exists; at least one entry; fan-in nodes list all expected sources; loop edges carry `MaxIter > 0`; request nodes have a `RequestSpec`; no two nodes share a shared-state key (parallel nodes write only their own namespace `out/<nodeID>`).

## 3. Execution: supersteps

State `RunState` (the serialized checkpoint payload):

```
Step            int                      // number of completed supersteps
Inboxes         map[NodeID][]Message     // messages that will be consumed in step Step+1
Barriers        map[NodeID][]NodeID      // fan-in: sources already delivered
Shared          map[string]json.RawMessage // "out/<node>" results, "in" run input
Pending         map[RequestID]PendingRequest
Waiting         map[NodeID]RequestID     // request nodes parked on a human
LoopCounts      map[EdgeKey]int
Done            map[NodeID]bool          // informational
```

One superstep:
1. **Select** ready nodes: nodes with a non-empty inbox, except fan-in nodes, which are ready only when every declared source has delivered (barrier complete).
2. **Execute** all ready nodes concurrently. Each execution first writes a `node_executions` row `(run_id, node_id, step, attempt)` in `started` state (the idempotency key), then runs `middleware(handler)`. Completed rows from an earlier interrupted attempt of this same step are reused: their recorded output is used and the handler is not called.
3. **Barrier**: wait for all. If any node errored after its retry policy, the step fails: the run goes `FAILED` unless an `error` edge exists. Nothing from a failed step is routed.
4. **Route** outputs: for every edge from a finished node, evaluate conditions against the output (`Cond`), enforce loop counters (a loop edge past `MaxIter` routes to the loop's `onExhausted` target or fails the run), and append messages to the next inboxes. Request nodes do not complete: their request is stored in `Pending` and the node is parked in `Waiting`.
5. **Checkpoint** (`kind=step`) in one transaction together with `workflow_runs` status and the emitted events. The checkpoint's `parent` is the previous checkpoint of this lineage.
6. If no node is ready and `Pending` is non-empty the run becomes `WAITING_HUMAN` and `Run` returns. If nothing is ready and nothing is pending the run is `COMPLETED` with the sink outputs as its result.

Before step 1 of a new run an `entry` checkpoint is written containing the input. Replaying a run from its entry checkpoint with deterministic handlers reproduces the same node sequence (tested).

## 4. Request ports (human in the loop)

A `request` node's handler returns a `RequestSpec{Kind(approval|question|review), Title, Payload, ActionHash?}`. The engine inserts an `approvals` row (`REQUESTED`), stores `PendingRequest` in the checkpoint, parks the node, and emits `workflow.request_raised` and `approval.requested`. Agent tool approvals raised through ACP `session/request_permission` create the same row, bound to the hash of the exact action.

`Resume(run, Response{RequestID, Decision, Data})`:
1. acquire the run lease (fails with `ErrLeaseHeld` if another instance holds it);
2. in one transaction, CAS the approval `REQUESTED → APPROVED|REJECTED` (a second answer to the same request fails with `ErrRequestConsumed`: duplicate resume proceeds exactly once);
3. write a `response_entry` checkpoint (parent = latest) that records the response, before any node consumes it;
4. deliver the response as the output of the waiting request node, route it, and continue supersteps.

An approval marked `APPROVED` is moved to `CONSUMED` when the action it authorizes actually runs.

**Retry requests (added with budgets, 2026-10-08).** A `RequestSpec` with `Retry: true` parks a node whose work is still owed (the budget gate in front of an agent turn). The pending request keeps the node's inbox; `Resume` restores it instead of routing the answer as the node's output, so the node runs again in the next superstep and reads the human's decision from the `approvals` row (APPROVED is consumed by the run it allows, REJECTED is the node's to act on; the budget gate fails the run). Without `Retry` the answer is the node's output, as before (the watchdog's pauses still work that way). Test: `TestRetryRequestRerunsTheNodeAfterTheAnswer`.

## 5. Crash recovery and exactly-once effects

- **Lease**: `run_leases(run_id, owner, expires_at)`; acquired with an atomic `INSERT ... ON CONFLICT DO UPDATE WHERE expires_at < now`; renewed by a heartbeat goroutine at one third of the TTL (default TTL 30 s); released on exit. Owner ids are `hostname:pid:random`.
- **Recover()** (called at startup): select runs with status `RUNNING` or `RECOVERING` whose lease is expired; for each, set `RECOVERING`, acquire the lease, load the latest checkpoint, and continue the loop. Runs in `WAITING_HUMAN`, `PAUSED` or terminal states are untouched.
- **Interrupted step**: the checkpoint after step *N* holds the inboxes for step *N+1*. Re-executing step *N+1*: rows in state `done` supply recorded outputs; rows in state `started` (no result) are retried once with `attempt+1` in the same worktree, seeded with the previous attempt's partial output reference and any handoff artifact; nodes with no row run normally. This is the partial-superstep guarantee.
- **Side effects** (agent processes, worktrees, commits, merges) are idempotent Go operations keyed by `(run_id, node_id, step, attempt)`; handlers receive the key and must use it (for example the worktree path is derived from the task id, and `git worktree add` is skipped when it already exists).
- **Agent processes die with the app** (Job Object `KILL_ON_JOB_CLOSE`), so re-attaching is never attempted; the worktree and its uncommitted work survive.

## 6. Time travel and replay

`ResumeFrom(checkpoint)` creates a **new run** (a branch) whose `entry`-like first checkpoint has `parent = checkpoint.ID` and copies that checkpoint's state; the original run and every earlier checkpoint are untouched. `Lineage(run)` follows parent links across runs, so the UI draws the branch. `Replay(run)` starts a new run from the entry checkpoint's recorded input.

## 7. Middleware and observers

```go
type NodeHandler func(ctx context.Context, in NodeInput) (NodeOutput, error)
type Middleware  func(next NodeHandler) NodeHandler
```

Order for `agent_task` nodes (outermost first): `Telemetry → Budget → Context → Security → Watchdog → Usage → Retry → handler`. `check` nodes use `Telemetry → Watchdog(OnCheckResult) → Retry → handler`. Middleware implements stream hooks by also implementing `StreamObserver{OnStart, OnChunk, OnEnd, OnError}`; the engine registers it for the node's lifetime.

## 8. Streaming

A handler emits chunks through `in.Emit(Chunk)` while it works and returns the collected value. The engine, per chunk: (1) appends it to `runs/<run>/<node>/<attempt>/stream.jsonl` and syncs every 64 chunks or 250 ms, (2) calls observers' `OnChunk`, (3) publishes a `workflow.node_chunk` event (lossy to slow subscribers, never blocking). Downstream nodes receive only the complete value. A crash loses at most the unsynced tail; `node_executions.partial_ref` points at the file so the retry attempt can seed itself from it. Checkpoints hold references, never stream content.

## 9. Watchdog integration

The watchdog (see `token-control.md`) is both a middleware and a `StreamObserver`. It reads `tool_call` / `tool_result` chunks mid-turn and `CheckResult`s at `check` nodes and loop back edges. A `Trip` makes the engine cancel the node's context with `ErrWatchdogTrip{Signal, Stage, Evidence}`; the agent runtime's handler translates stage 1 into "cancel the turn, continue the session with the nudge message" and stage 2 into "write a handoff, return a `RequestSpec` of kind `question` so the task goes to `WAITING_HUMAN`". Engine and watchdog agree only on `Verdict` and `TurnController` (`CancelTurn`, `Send`), both defined in `internal/workflow`.

## 10. Persistence

Tables (migration `0001`): `workflow_runs(id, workflow, status, input_ref, parent_checkpoint_id, topology_hash, version, created_at, updated_at)`, `workflow_checkpoints(id, run_id, workflow, step, kind, parent_id, state, created_at)`, `node_executions(run_id, node_id, step, attempt, status, output, error, partial_ref, safe_point_ref, started_at, finished_at)` with primary key `(run_id, node_id, step, attempt)`, `run_leases(run_id, owner, expires_at)`, `watchdog_trips`. Run status is derived from the latest checkpoint but cached in `workflow_runs.status` with a `version` column updated by compare-and-swap. State JSON is stored as a BLOB; bulky payloads (outputs over 64 KB) go to `runs/<run>/<node>/<attempt>/output.json` and the row holds the reference.

## 11. Acceptance tests (Slice 1C release gate)

All use real SQLite files and real OS processes; the "kill the app" tests re-execute the test binary as a child process (`os/exec`) and kill it.

| Test | What it proves |
|---|---|
| `TestApprovalResumeAcrossKill` | Child process runs to a `request` node and is killed; a second process recovers, answers once, run completes exactly once |
| `TestPartialSuperstep` | Two parallel nodes; child killed after one finishes; on resume the finished node's side-effect counter stays 1 and the other completes exactly once |
| `TestDuplicateResume` | Two concurrent `Resume` calls: one succeeds, one gets `ErrLeaseHeld` or `ErrRequestConsumed`; counter is 1 |
| `TestTimeTravel` | `ResumeFrom` an earlier checkpoint creates a new branch; original lineage and checkpoints byte-identical |
| `TestReplayFromEntry` | Replay reproduces the same ordered node sequence |
| `TestStreaming` | Chunks reach a subscriber before the node finishes; downstream receives the complete value; kill mid-stream leaves durable chunks and the retry sees `partial_ref` |
| `TestCancellationAndLoopLimits` | Context cancel stops the run; a loop with `MaxIter` exits to `onExhausted` |
| `TestWatchdog*` | See `token-control.md`: spin→nudge→pause, error echo, hard cap, and the false-positive control |

## 12. As built: Slice 4 templates, spec gate, PlanTeam, parallel execution (`internal/pipeline`, `internal/checks`, `internal/core/ext_agents.go`)

Everything here is added through new files; no engine file changed. The core registers it from the extension `agents-dev`.

**Post-start registration.** Extensions register graphs after `core.Open` has started the engine, while runs may be in flight. `Engine.Register` was already mutex-guarded, but it silently replaces a graph by name, and a replacement with another topology would strand every waiting run (`ErrTopologyMismatch` on resume). `Engine.RegisterNew` (`workflow/register.go`) refuses a replacement whose topology hash differs (`ErrGraphChanged`), accepts the same topology (new handler closures), and is safe against concurrent `Start`/`Resume` (tested under `-race`, including a waiting run resuming after re-registration).

**Templates** (`pipeline/templates.go`; catalog from `template.list`; `task.startTemplate {taskId, template, agents?, scans?}`):

| Template | Graph (`->` direct, `-x->` on a route) | Extra roles |
|---|---|---|
| `iterate-until-verified` | unchanged (`review.go`), started by `task.start` or `task.startTemplate` | none |
| `feature` | prepare -> spec -> speccheck -valid-> **specreview (human)** -approved-> specaccept -> tests -> testscope -ok-> **red** -red-> work -> verify -pass-> finalize -> review -ship-> **approve (human)** -> merge | `pm` required; `tests` falls back to the worker |
| `bug` | prepare -> repro -> testscope -> red -> work -> verify -> finalize -> review -> approve -> merge | `tests` falls back to the worker |
| `security` | prepare -> scan (informational) -> work -> verify -> gate (scan again, blocking) -> finalize -> sreview (security role) -> approve -> merge | `security` required, never the worker |
| `full-stack-saas` | feature, with an optional `design` node (architect; skipped when the team has none) after the spec gate, e2e inside `verify`, and `secscan` -> `preview` between the review and the approval | `pm` required; `tests`, `architect` optional |

Every loop is bounded (5 by default; 3 for spec and test rework) and exhausts into a `limit` question, whose "reject" cancels the task (`abort`). Roles are resolved once, when the run starts (`pipeline.ResolveRoles`: overrides win, then the first team member whose role matches by name, the PM/security/architect never the worker) and are frozen in the run input (`TemplateInput.Roles`), so a resume or a replay uses the same agents. "Templates set defaults, not locks": any role can be overridden per run, `task.assign` stays the way to set the worker and reviewer, and the project settings switch e2e and preview.

**Spec gate.** `spec` is a read-only (`plan` permission mode) PM turn. `speccheck` parses it deterministically (`pipeline.ParseSpec`): at least one criterion, every criterion a complete Given/When/Then (one-line or Gherkin multi-line with And/But), no duplicates; a spec that fails goes back to the PM with the problems and never reaches the human. `specreview` raises a `review` request (payload `phase: "spec"`, the criteria with ids AC1..., the spec text) and moves the task to `WAITING_HUMAN`. Nothing else can run: the graph has no other path to `tests`/`work`, and the test-writing, design and implementation nodes also refuse to start unless the task has `spec_accept` evidence whose `by` is a `human:` identity (two independent locks, both tested; the golden-path test asserts from the agent prompt log that no test-writing or implementation turn exists before the acceptance). `specaccept` accepts only a `human:` identity (an agent identity fails the run), writes the criteria, with their ids, into `tasks.Acceptance`, records `spec_accept` evidence and returns the task to `IN_PROGRESS`. A human reject sends their text to the PM (`specrevise`); the human may also answer with `data.criteria` (Given/When/Then lines) to replace the criteria, and invalid edits go back as feedback instead of being accepted.

**Test first, red then green.** `tests` (or `repro` for a bug) writes tests; `testscope` checks, from `git status`, that only test files changed (by path convention, `pipeline.IsTestPath`) and, for a feature, that every accepted criterion id appears in a changed test (a textual match on `AC1`, the weakest honest form of traceability); `red` runs the project's own `test` command and requires it to FAIL: a pass sends the tests back (3 times, then a `redlimit` question; a human may approve building without red, which leaves no red evidence and therefore no green claim). Evidence: `red_test` (no verdict, the failing names and log), `green_test` (passed) written by `verifyTDD` after the project's checks pass with the same command. Limits stated plainly: a compile error counts as red (Go: `build failed` with no test names), "green" means the same test command now exits 0, not that each previously failing test was matched by name, and the criterion-to-test trace is textual.

**Security template.** The scans are pluggable by name (`pipeline.ScanRegistry`; `TemplateInput.Scans`, default `secrets`, `deps`): a registered name wins, a name that matches a detected project check (`build`, `test`, `lint`, `typecheck`) runs as that check, any other name is recorded as skipped ("not available in this build"), never as a pass. The built-ins use the economics worker's `checks.SecretsSpec` over the whole tree (diff against the empty tree) and `checks.DepsSpec` (skipped when `osv-scanner` is absent). The first scan is informational and its findings go into the worker's prompt (names and locations, never secret values); the second (`gate`) blocks. Skipped and errored checks are evidence without a verdict and show in the room as `skipped`/`error` from the owning tool member.

**e2e and preview checks** (brief 13a; `checks/e2e.go`, `checks/preview.go`). `e2e` runs the project's own command, in this order: Taskfile target `test:e2e` then `e2e` (when go-task is installed), `package.json` script `test:e2e` then `e2e`, then `npx --no-install playwright test` when a Playwright config exists and `@playwright/test` is already in `node_modules` (Drydock never installs anything). It reads the Playwright JSON report (only a report newer than the run), names failing tests from it, and copies up to 20 screenshots under the run directory so the evidence outlives the worktree. Disabled in the project settings, or not defined: a skipped result (`Result.Skipped`, shared with the economics worker's checks), not a pass. `preview` starts the project's preview command under the process manager (setting `preview.command`, else a Taskfile/`package.json` target named `preview`), refuses commands that look like a production deployment (a lexical guard, not a sandbox; the starter's own `preview` script enforces D6), waits for the health URL (setting, or the first URL the command prints) and posts the URL as the Preview member. The process belongs to the preview manager, is stopped when the task ends, when the human uses `preview.stop`, and before the `merge` node removes the worktree. Both are off by default and are switched on per project by a human (`projectdev.set`). On demand: `check.e2e {id}`, `preview.start {id}`, `preview.stop {id}`, `preview.list`.

**PlanTeam.** `plan.propose {projectId, goal, requirements?, plannerAgentId?}` starts the `plan-team` workflow: a read-only planner turn (the PM or architect by default), strict validation (`pipeline.ParseProposal`: one JSON object and nothing else, unknown fields refused, keys, roles, templates, dependencies, no cycles, every acceptance a complete Given/When/Then, at most 30 tasks; control and format characters stripped; a reply that fails goes back to the planner twice, then the plan is `FAILED`), then a `review` request listing the proposed tasks. Requirements text is quoted data in the prompt. Nothing is a task until `plan.accept {planId, accept?, edits?}`: the accepted set must be closed under dependencies, edits are re-validated like a planner reply, tasks are created in `BACKLOG` with `DependsOn` and `Template`, assigned by role (the least loaded matching agent and an independent reviewer; unassigned ones are reported), and a retry after a crash reuses the tasks already created. `plan.reject`, `plan.get`, `plan.list`. The plan row (`plans`, migration 00070) keeps the proposal, the created task ids and who decided.

**Parallel execution.** `scheduler.tick {projectId}` (and a loop that runs only for projects with `autoSchedule` on, on task events and a 5 s timer) promotes `BACKLOG` tasks whose dependencies are `COMPLETED` (`tasks.PromoteReady`), then starts `READY` tasks that have a worker and a reviewer, highest priority then oldest first, within `maxParallel` (runs that are executing count; a run parked for a human does not hold an agent) and each provider's free `max_concurrent`, reporting why each skipped task was skipped. A task starts its template (`task.Template`, set by the planner) or the default workflow, each in its own worktree and branch. Admission is best effort; the invariant is enforced at the agent turn: `pipeline.Limiter` is workflow middleware on every `agent_task`, registered outermost with `Engine.UseOutermost` (also in `register.go`) so the time a turn is queued does not count against the watchdog's wall-clock cap, which starts inside the watchdog middleware (and the room responder's gate), which makes the turn wait for a project slot and for `providers.Monitor.Acquire` on the agent's provider, so resumed runs and room replies obey the same limits. Tested: four tasks, limit 2, real worktrees (peak exactly 2, never 3), a provider with `max_concurrent 1` serializing two agents, and the conflicting merge parked as a question by the existing `Merge` node. Merging stays a human action: every template ends at `approve`, and no scheduler code path merges.

**Project settings** (`projectdev.get/set`, stored in `settings` under `devloop:<project>`; human identity only): `maxParallel` (default 2), `autoSchedule` (default off), `respond` (default on), `e2e.{enabled,command,timeoutSec}`, `preview.{enabled,command,url,timeoutSec}`.

**Starting a template.** `task.startTemplate` and the scheduler start a task in its template; the base `task.start` still always starts `iterate-until-verified` (it does not read `task.Template`), so a task created with a spec-gated template and started with `task.start` skips the gate. Making `task.start` dispatch on `task.Template` is a one-line change in `core/ops.go` (`StartTask`) that this extension cannot make.

**Not built / unverified.** The templates end in the local merge: for a `pr_flow` project `task.startTemplate` refuses with a message (the PR variant of each template is not built). No UI exists for any of this. Real-agent coverage: one real Claude Code (haiku) planner test and one real room-reply test; the template flows, the spec gate and the scheduler are tested with the scripted stand-in agent (labeled), not with real agents. The starter project's e2e was exercised as far as `playwright test --list` (real Playwright, no browser exists in the sandbox); its full browser run is an opt-in test (`DRYDOCK_RUN_STARTER_E2E=1`). Everything Windows is unproven (P2): process-tree kill of a preview server on Windows, and the Windows path handling of the test-file heuristic, are compiled (`GOOS=windows go vet`) but not run.

## Open questions / unverified

- Windows file-locking behavior for `stream.jsonl` appends during kill tests (the tests run on any host; only Windows CI can confirm).
- Checkpoint size growth and compaction thresholds are set from measurements after Slice 1C (target: state under 256 KB per checkpoint for typical runs; not yet measured).
- Whether `modernc.org/sqlite` on Windows (built with `SQLITE_MUTEX_NOOP`) needs extra serialization beyond the single writer connection; the Windows probe will tell.
