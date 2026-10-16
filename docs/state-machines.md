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

**At an escalation (decision P26).** When a workflow loop runs out (or the reviewer repeats itself) and the question goes to the human, an `IN_PROGRESS` task moves to `WAITING_HUMAN`; a task in `REVIEW` stays there (the state machine has no REVIEW -> WAITING_HUMAN edge). A human (or team lead) "continue" raises `max_iterations` to `iteration + 6` when the limit was used up and moves the task back to `IN_PROGRESS` (from `REVIEW`, `VERIFICATION`, `WAITING_HUMAN` or `BLOCKED`, with the guidance as feedback), except when the work resumes at `sync` or the approval, which keep `VERIFICATION`. A human "stop" (reject, or `data.action: "stop"`) moves the task to `CANCELLED`, as does rejecting a watchdog pause. A team lead's reassignment changes the assignee (and, when needed, the reviewer) through the same rules as `task.assign`.

## 2. Workflow run

`PENDING → RUNNING`; `RUNNING → PAUSED`; `RUNNING → WAITING_HUMAN` (a request port is pending and nothing else can run) `→ RUNNING` (response delivered); `RUNNING → RECOVERING` (after an app restart, while the engine takes over the lease and restores from the latest checkpoint) `→ RUNNING`; terminal `COMPLETED, FAILED, CANCELLED`. The latest checkpoint is authoritative for position; `workflow_runs.status` is a cached derivation updated in the same transaction as each checkpoint. See [`workflow-engine.md`](workflow-engine.md).

Checkpoint kinds: `entry` (before step 0; carries the input), `step` (after each superstep and on completion), `response_entry` (when a human response is delivered, before the step that consumes it). Order is by parent lineage, never by step number.

## 3. Approval (request port)

`REQUESTED → APPROVED | REJECTED | EXPIRED | CANCELLED`; `APPROVED → CONSUMED` once the authorized action runs. The first answer wins by compare-and-swap (`UPDATE ... WHERE status='REQUESTED'`); a second answer gets `ErrRequestConsumed`. An approval is bound to a hash of the exact action; the `internal/security` service verifies the hash on consumption and the single-use rule.

## 4. Agent (session)

`STARTING → IDLE ⇄ WORKING` (thinking is a sub-state of working); `WORKING → WAITING` (human, approval, dependency); `→ NEEDS_ATTENTION` (stuck, repeated errors, watchdog stage 2); `→ PAUSED` (user or budget); `→ COMPLETED` (turn done, returns to idle); `→ FAILED` (crash, auth failure; as built there is no automatic restart: a new turn is a new agent run, so `FAILED` is terminal for that run); `STOPPED` terminal for a deliberate stop. Implemented as `agent_runs.status` (`WORKING`, `STOPPED`, `FAILED`) plus `agent.started/waiting/completed/failed/stopped` events; the Room derives presence from events (`internal/room`).

## 5. Watchdog trip policy (per task)

Hard signals: `tool_spin`, `error_echo`, `tool_call_cap`, `time_cap`, `tool_error_streak`. Soft: `stagnation` (warning only).
- **Stage 1** (first hard trip since the last human reset): cancel the turn (protocol interrupt), continue the same session with one nudge naming the evidence. For `error_echo` the nudge is queued and delivered with the agent's next turn.
- **Stage 2** (second hard trip): write a handoff artifact (`handoffs`, reason `watchdog`), park the node on a `question` request, notify. The workflow run becomes `WAITING_HUMAN`; the task keeps its state (usually `IN_PROGRESS`): the watchdog does not move it to `WAITING_HUMAN`. The agent's presence shows Needs Attention (derived from the watchdog event; `agent_runs` has no such status). The request is `Retry` + `DeliverAnswer`: the node runs again after the answer.
- A human answering the pause (with or without guidance) resets the count (`Watchdog.Reset(task)`) and the guidance reaches the agent's next turn as text; rejecting it cancels the task and ends the run. The count also resets when a human continues past an iteration limit and when the task enters `REVIEW` (it moved on). A reset row is written only when there is a count.
- **With a team lead** on the project (decision P26), a stage-1 trip stops the turn and asks the lead instead of the generic nudge: its nudge, reassignment or split restarts the work with its message (a fresh turn), its escalation parks the node on a question with its message (the task again keeps its state). The second hard trip is still stage 2. Without a lead, stage 1 is unchanged.
- **Where it shows.** A watchdog or lead pause, like a budget pause or a GitHub problem in the PR flow, is a Needs-you item while the task's state stays as it was. Only the escalation at a loop limit (section 1) and the spec review move a task to `WAITING_HUMAN`. The Tasks page therefore lists under "Waiting on you" every task in `WAITING_HUMAN`, `VERIFICATION` or `BLOCKED` plus every task that has a Needs-you item, whatever its state (`ui/app/screens/tasks/tasks.ts`).

## 6. Team lead intervention (per task and run)

Trigger fires (watchdog stage 1, iteration limit, repeated REVISE, stalled, unassigned) → **no lead, bound used up (3 per task and run), a supervision turn already in flight for the task, the trigger is the lead's own turn, or a budget refuses the turn** → the default behaviour, unchanged → otherwise one read-only lead turn → answer valid → applied (`nudge`, `reassign`, `split`, `escalate`, `continue`) | answer invalid → one retry with the problems → valid → applied | invalid → `escalate` (marked invalid). Each applied decision is one `lead_interventions` row, one message by the lead in the task channel and one `lead.intervened` event. A turn that fails (an error or the time limit) is one row with action `failed` and one event, no message, and counts toward the bound; the trigger site then behaves as without a lead. A stalled task is asked about once per silence (a new progress event re-arms it; the lead's own turn on the task is not progress); an unassigned task once until it changes. Both are read from the stored rows, so they hold across restarts.

## Open questions / unverified

- Agent presence states `STARTING`/`STOPPED` have no counterpart in the brief's presence list; the room doc proposes a mapping.
- `PAUSED` run state is defined but no UI action drives it yet.
