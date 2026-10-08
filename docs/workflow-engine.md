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

## Open questions / unverified

- Windows file-locking behavior for `stream.jsonl` appends during kill tests (the tests run on any host; only Windows CI can confirm).
- Checkpoint size growth and compaction thresholds are set from measurements after Slice 1C (target: state under 256 KB per checkpoint for typical runs; not yet measured).
- Whether `modernc.org/sqlite` on Windows (built with `SQLITE_MUTEX_NOOP`) needs extra serialization beyond the single writer connection; the Windows probe will tell.
