# Drydock: state machines

Scope: the task, agent, workflow-run, approval and watchdog states and the exact transitions and guards implemented in code (`internal/tasks/machine.go`, `internal/tasks/service.go`, `internal/workflow`, `internal/watchdog`). Where a table here disagrees with the code, the code's tests win and this file is fixed in the same change. Brief section 12 is the origin.

## 1. Task

States: `BACKLOG, READY, CLAIMED, IN_PROGRESS, BLOCKED, WAITING_HUMAN, REVIEW, VERIFICATION, COMPLETED, FAILED, CANCELLED`. Terminal: `COMPLETED, FAILED, CANCELLED`.

| From | To | Guard (enforced in `Service.guard`) |
|---|---|---|
| BACKLOG | READY | every dependency is `COMPLETED` |
| READY | CLAIMED | an assignee is set |
| CLAIMED | IN_PROGRESS | none |
| IN_PROGRESS | BLOCKED | reason recorded (`blocked_reason`) |
| IN_PROGRESS | WAITING_HUMAN | none |
| IN_PROGRESS | REVIEW | evidence present: latest `test` evidence **passed**, a `diff`, a `commit`; a reviewer is set and **differs from the assignee** |
| BLOCKED | IN_PROGRESS | none (clears `blocked_reason`; not an iteration) |
| WAITING_HUMAN | IN_PROGRESS | none (a human decision) |
| REVIEW | VERIFICATION | the latest review is `SHIP` from a reviewer other than the assignee |
| REVIEW | IN_PROGRESS | feedback is non-empty; `iteration + 1 < max_iterations`; increments `iteration` |
| VERIFICATION | COMPLETED | caller identity starts with `human:`; latest `test` evidence passed; sets `accepted_by` |
| VERIFICATION | IN_PROGRESS | as REVIEW to IN_PROGRESS |
| any non-terminal | FAILED, CANCELLED | none; `FAILED` also writes a `dead_letters` row |

`BLOCKED` means something prevents progress and clears when the impediment clears (dependency, provider quota, broken environment). `WAITING_HUMAN` means an agent can continue as soon as a human decides, and clears only by human action. Transitions are compare-and-swap on `version`; a lost race re-reads and re-validates. Transitioning to the current state is a no-op. Events: `task.created/ready/claimed/started/blocked/waiting_human/review/verification/completed/failed/cancelled/resumed/evidence`.

Tested by `TestStateMachineTable` (27 allowed/denied pairs), the evidence/reviewer guard test, the human-acceptance test, iteration limit, dependency gating, dead letters and a 25-way concurrent patch test.

## 2. Workflow run

`PENDING → RUNNING`; `RUNNING → PAUSED`; `RUNNING → WAITING_HUMAN` (a request port is pending and nothing else can run) `→ RUNNING` (response delivered); `RUNNING → RECOVERING` (after an app restart, while the engine takes over the lease and restores from the latest checkpoint) `→ RUNNING`; terminal `COMPLETED, FAILED, CANCELLED`. The latest checkpoint is authoritative for position; `workflow_runs.status` is a cached derivation updated in the same transaction as each checkpoint. See [`workflow-engine.md`](workflow-engine.md).

Checkpoint kinds: `entry` (before step 0; carries the input), `step` (after each superstep and on completion), `response_entry` (when a human response is delivered, before the step that consumes it). Order is by parent lineage, never by step number.

## 3. Approval (request port)

`REQUESTED → APPROVED | REJECTED | EXPIRED | CANCELLED`; `APPROVED → CONSUMED` once the authorized action runs. The first answer wins by compare-and-swap (`UPDATE ... WHERE status='REQUESTED'`); a second answer gets `ErrRequestConsumed`. An approval is bound to a hash of the exact action; the `internal/security` service verifies the hash on consumption and the single-use rule.

## 4. Agent (session)

`STARTING → IDLE ⇄ WORKING` (thinking is a sub-state of working); `WORKING → WAITING` (human, approval, dependency); `→ NEEDS_ATTENTION` (stuck, repeated errors, watchdog stage 2); `→ PAUSED` (user or budget); `→ COMPLETED` (turn done, returns to idle); `→ FAILED` (crash, auth failure; `FAILED → STARTING` by restart); `STOPPED` terminal for a deliberate stop. Implemented as `agent_runs.status` (`WORKING`, `STOPPED`, `FAILED`) plus `agent.started/waiting/completed/failed/stopped` events; the Room derives presence from events (`internal/room`).

## 5. Watchdog trip policy (per task)

Hard signals: `tool_spin`, `error_echo`, `tool_call_cap`, `time_cap`, `tool_error_streak`. Soft: `stagnation` (warning only).
- **Stage 1** (first hard trip since the last human reset): cancel the turn (protocol interrupt), continue the same session with one nudge naming the evidence. For `error_echo` the nudge is queued and delivered with the agent's next turn.
- **Stage 2** (second hard trip): write a handoff artifact, park the node on a `question` request (task → `WAITING_HUMAN`, agent → `NEEDS_ATTENTION`), notify.
- A human resuming with guidance calls `Watchdog.Reset(task)`; the count restarts from zero.

## Open questions / unverified

- Agent presence states `STARTING`/`STOPPED` have no counterpart in the brief's presence list; the room doc proposes a mapping.
- `PAUSED` run state is defined but no UI action drives it yet.
