# AG-UI review, 2026-10-25

Scope: Drydock's typed activity vocabulary (`internal/agui`, decisions P87 and P88), which is modelled on the AG-UI protocol, and every
path it joins: the two agent adapters that produce the events it reads (Claude Code's stream-json, the ACP agents Codex, Gemini CLI,
OpenCode, goose and Cline), the runtime that publishes them, the permission gate whose requests and answers become its interrupt and
resume events, the `task.activity` read, and the task page that draws it; and the other readers of the same agent events (presence,
Needs you, the run page, the chat, the logs), where one turn must not be told two ways. Reviewed at commit `b88ce80` (branch
`claude/integrations-agents-github`, PR #8) against AG-UI 1.0.3: the specification pages and the TypeScript SDK at commit `47300a4`
of `ag-ui-protocol/ag-ui` (2026-10-21), and the published packages `@ag-ui/core@1.0.3` and `@ag-ui/client@1.0.3` (2026-10-16).

Nothing in the repository was changed by the review itself; section 8, "Status after the fixes", says what was changed afterwards.

## 1. Verdict

The vocabulary is sound as a protocol. Every thread Drydock projects from its stored events, including the failing, cut-short and
interrupted ones its tests build, validates event by event against the published 1.0.3 schemas and passes the reference client's
verifier (`verifyEvents`) with no error: 35 threads and 252 events in one run, 49 threads in another, zero disagreements between
Drydock's own `CheckOrder` and the reference. The event names, the outcome object, the `reasoning` role, the disjoint message id
spaces, the millisecond timestamps and the never-null optionals are all as 1.0 requires. "Modelled on AG-UI 1.0" remains an
accurate label: nothing changed in the protocol between 1.0.0 and 1.0.3, and the `cancelled` outcome Drydock uses is in the
published schema, not a Drydock invention. Where Drydock deviates on purpose (a turn that waits for a person stays one run, with
`drydock.interrupt` and `drydock.resume` as CUSTOM events, instead of ending with an `interrupt` outcome and resuming as a new run),
the deviation is documented and the reference client accepts it.

What does not work is not the protocol but what Drydock puts into it. Five defects would show a person wrong or stale information
on the task page in ordinary use:

- An ACP agent's sentence is shown twice whenever a thought or a tool result lands between the streamed text and its message end;
  the same projector branch doubles a Claude turn's reasoning and text the moment Claude Code streams thinking text (today it sends
  the thinking block empty, so this part is latent).
- A Claude Code turn that failed after the model had said anything (an API error mid-turn, an execution error) is shown as a turn
  that finished, because the result's `stop_reason` is the last message's, not the failure's; P87 promises RUN_ERROR here.
- The Activity re-reads only on `agent.*` events, so after a person allows a command the row keeps saying "Waiting for you" for
  the whole run of the allowed command; the request itself can also be missed in a race.
- A Claude Code tool result over about 240 KiB, including every result the parser clips to keep its start and end, becomes a
  stand-in the projector cannot read, so the call shows "no result".
- An ACP session's start-up settings warning (an effort or permission mode the agent does not offer: the default `acceptEdits`
  triggers it on every ACP session) is reported as the reason the first turn failed when that turn made only tool calls.

The rest is conformance for a consumer Drydock does not have yet (its extra fields `seq`, `stopReason`, `isError` and the ids on
RUN_ERROR are stripped by the reference client, which the protocol's `metadata` field exists to carry; RUN_STARTED does not say
`protocolVersion`), places where the other readers of the same events tell one turn differently (presence, the chat, the run
page, Needs you after a crash), the cut of long arguments and results, and documents that do not yet mention the vocabulary at all.

## 2. How this review was done

- Eight independent reviewers, one per area: field-level conformance to the 1.0.3 schemas; ordering conformance against the real
  verifier; the Claude Code path end to end; the ACP path end to end; the permission gate as interrupt and resume; the core read
  and the UI; consistency with the other readers of agent events and with the documents; and a review of the protocol itself as
  an architect would write it for the owner. Each read the code, ran targeted tests, and wrote throwaway probes where a claim
  needed one (the probes were deleted; the repository was left unchanged).
- Every finding was then handed to independent skeptics told to refute it: one tracing the code, and for medium and higher a
  second reproducing it (a parser input, an event sequence through `agui.Project`, a UI unit test, a browser test, or a Node
  script against the published packages). A finding is listed below only with its verification outcome; where a skeptic
  narrowed or corrected a claim, the corrected form is what is written here. Two findings were refuted and are kept in section 6
  so nobody raises them again.
- The real packages were exercised: `@ag-ui/core/schemas` (`EventSchema`) and `@ag-ui/client` (`enforceEvents`,
  `transformChunks`, `verifyEvents`, the reference reducer) over threads dumped from `agui.Project`, plus hand-built edge
  streams to learn what the reference accepts and rejects. Claude Code's behaviour was read from the installed 2.1.310 bundle and
  the recorded 2.1.294 fixtures; no paid real-agent run was made.
- Fifteen of the skeptics' runs did not complete (the account's weekly usage limit): the reproducing skeptic of eight medium
  findings, and the code-tracing skeptic of six low ones. The eight are marked "verified: code" below (confirmed by one skeptic
  reading the code, not reproduced); of the six, five were checked afterwards by reading the code and the documents and are
  marked "verified by reading", and one is marked "reported, not verified". The completeness critic that was to close the
  review did not run either; the "What was not reviewed" list below stands in for it.
- What could not be done here: nothing was run on Windows or in WebView2; the real Claude Code and the real ACP agents were not
  started (the fixtures and the labelled doubles stood in); CopilotKit itself was not run.

What was not reviewed: the chat's own tool-call cards (they read the chunks directly, not the vocabulary); the dock's Logs tab
beyond its reading of the same chunk kinds; the responder's use of agent events; the e2e specs' coverage of the Activity (read,
not run against the real core); the trial and skill-watch readers of tool calls (they read the bus, not the vocabulary).

## 3. The protocol, and what Drydock takes from it

AG-UI 1.0 is an event protocol between an agent and a user interface: a run (`RUN_STARTED` to `RUN_FINISHED` or `RUN_ERROR`)
inside a thread, text and reasoning messages streamed as start, content and end, tool calls as start, arguments, end and result,
activity snapshots and deltas, steps, state and message snapshots, sub-agent attribution, and CUSTOM events for whatever the
protocol does not name. Every event carries `type`, an optional millisecond `timestamp`, an optional `rawEvent` and an optional
`metadata` object; RUN_STARTED may declare `protocolVersion`; RUN_FINISHED carries an `outcome` object (`success`, `interrupt` or
`cancelled`) and may carry `usage`; a turn that needs a person ends with the `interrupt` outcome and resumes as a new run with a
`resume` entry. The published client validates each event against a loose schema, strips top-level keys the schema does not
declare (with a warning), verifies the ordering rules, and reduces the stream to messages for a UI such as CopilotKit.

Drydock (P87) does not speak AG-UI on a wire. `agui.Project` turns an agent process's stored bus events (`agent.started`,
`agent.waiting`, `agent.output`, `agent.completed`, `agent.failed`, `agent.stopped`, and the gate's `approval.requested` and
`permission.decided`) into one thread per process and one run per turn, at read time, for `task.activity`; the task page's
Activity folds the events into turns and steps. It uses RUN_*, TEXT_MESSAGE_*, REASONING_MESSAGE_*, TOOL_CALL_*,
ACTIVITY_SNAPSHOT (the ACP plan) and CUSTOM; it adds `seq` on every event, `stopReason` on RUN_FINISHED, `isError` on
TOOL_CALL_RESULT and the thread and run ids on RUN_ERROR; it clips arguments and results to 8 KiB; and it keeps a permission
wait inside the run as `drydock.interrupt` and `drydock.resume`, because neither Claude Code's prompt nor an ACP `session/prompt`
ends while the gate waits. It does not project usage, sub-agents, steps, state or message snapshots, and declares no
capabilities. The generated `ui/app/api/agui.gen.ts` is the UI's only type source, pinned by a Go test; no `@ag-ui` package is a
dependency, and this review confirmed that claim.

The protocol as published did not move: the `@ag-ui/core` 1.0.0 and 1.0.3 type declarations are byte-identical (and so is the
1.0.4 canary), and the changelog lists 1.0.1 to 1.0.3 as maintenance releases. `cancelled`, `protocolVersion`, usage,
sub-agents and the reasoning span were all in 1.0.0.

## 4. What works

Verified by the reviewers and the skeptics, with the evidence they left:

- **Wire acceptance by the real consumer.** Every projected thread from the package's own test streams (a full turn with
  reasoning, plan, text, tool calls and an error result; a cancelled second run with an unanswered call; a permission wait and
  its answer; a start failure; an `is_error` turn; clipped arguments; a zero-time event) passes `EventSchema.safeParse` for every
  event and the client's `enforceEvents`, `transformChunks` and `verifyEvents` pipeline (35 threads, 252 events; a second run
  with hand-built edge streams, 49 threads). The reference reducer assembles reasoning, activity, assistant and tool messages
  from them.
- **Ordering.** `CheckOrder` is a faithful subset of `verify.ts` for every event Drydock emits (first event RUN_STARTED, nothing
  after RUN_FINISHED or RUN_ERROR but RUN_STARTED, open messages and calls closed before RUN_FINISHED, content and end only
  between the start and end of the same id), and it is deliberately stricter in two places the projector cannot reach (a leading
  RUN_ERROR, RUN_ERROR after RUN_FINISHED). The real verifier agreed with it on every thread the projector produced.
- **Shapes.** Event names match the 1.0 `EventType` enum, with none of the deprecated `THINKING_*` names; RUN_FINISHED's outcome
  is an object with `success` or `cancelled`; REASONING_MESSAGE_START carries the fixed `reasoning` role; TOOL_CALL_RESULT
  carries its own message id, the call id, string content and the `tool` role; ACTIVITY_SNAPSHOT's content is an object with
  `entries` and `replace` is explicit; message id spaces are disjoint (`m`, `r`, `t`, `c`, `plan`); timestamps are Unix
  milliseconds; no optional is ever null.
- **The permission gate joins the right thread.** `approval.requested` and `permission.decided` carry the agent run as their run
  id (both the Claude and the ACP askers), the task id and the agent id, so `task.activity`'s filter finds them and the projector
  attaches the interrupt and the resume to the run that asked; a workflow's own approval carries the workflow run and opens no
  thread. Every answered path publishes `permission.decided` with the approval id and one of `approved`, `approved_with_edits`,
  `rejected`, `timed_out`, `cancelled`, `denied`, and the UI names each. A late answer is dropped by the gate (the approval is
  cancelled when the turn's context ends) and the Activity says "Not answered before the turn ended".
- **Secrets stay out.** Streamed text is redacted word by word before publication (`redact.NewStream`); every other `agent.*`
  payload (tool arguments, results, plans, error text, permission requests) and the gate's events go through the bus redactor
  before storage; `permission.decided` stores only digests and ids; the approval title is redacted and capped at 200 bytes; the
  projection adds no stored data (it is computed on read).
- **The Claude path.** Text and thinking deltas are coalesced for 50 ms and never interleave wrongly (a kind change flushes
  first); a message that came whole is emitted as one message; a result with `stop_reason` null and `is_error` ends RUN_ERROR
  with its subtype as the code; an unreadable result is a fatal adapter-drift error and ends the run; a start failure projects to
  its own RUN_STARTED and RUN_ERROR; the nudge timeout ends the run as cancelled; unknown system subtypes are ignored without
  drift.
- **The ACP path.** Plain text is not doubled when a tool call or the turn end follows the deltas; a message id change flushes
  the previous message; the runtime preserves chunk order on the bus; a tool call is emitted once and its result once when
  terminal, with a failed status as `isError`; `tool_call` precedes `permission_request`; the asker decides over the adapter's
  default and selects `allow_once` only; Interrupt and Stop cancel pending asks; `end_turn`, `cancelled`, `error` and `refusal`
  map to the right run ends; a lost connection mid-turn is a fatal chunk that ends the run as RUN_ERROR and folds the process's
  `agent.failed` into it; replayed history after `session/load` is suppressed; child-session updates are folded under prefixed
  ids and cannot collide.
- **The read and the UI.** `task.activity` pages by seq with an exact type list, keeps the newest 20000 events and says when it
  truncated; the UI's `turnsOf` opens a turn for events read from the middle, draws a result without its start, folds reasoning
  into a Thought step, and drops a resume for a wait it never saw; the live throttle re-reads at most once per 750 ms under a
  steady stream and drops out-of-order answers; the 1 s tick runs only while a step is running; the virtual list follows the
  bottom.
- **Consistency that holds.** The dock's Logs and the Activity read the same chunk kinds the same way (`[tool error]` is
  `isError`, `[turn ended]` is the stop reason); the presence tracker is rebuilt from lifecycle events only and reads every agent
  as stopped after a restart, consistent with `CloseStale`'s `app_stopped` failure and the Activity's RUN_ERROR; a chat's agent
  events carry no task id, so nothing of a chat reaches a task's Activity; P87's statements about the 8 KiB cut, the 750 ms
  throttle, the pinned generated types and `CheckOrder` on every projected thread are all true of the code.

## 5. Findings

Severity: **high** means a person sees wrong information in ordinary use; **medium** means wrong or missing information on a
real path, or a rule two readers break between them; **low** means cosmetic, an edge path, a document or a comment. Verification:
"code + reproduced" means both skeptics confirmed it and one reproduced it; "code" means the code-tracing skeptic confirmed it
and the reproducing one did not run; "by reading" means checked afterwards against the code or the document named; "reported"
means nobody checked it.

| # | Severity | Area | Finding | Verification |
|---|---|---|---|---|
| H1 | high | projection, ACP, Claude | An ACP agent's sentence is shown twice after a thought or a tool result; the same branch doubles Claude's per-block messages | code + reproduced |
| H2 | high | Claude parser, projection, chat | A Claude Code turn that failed after it had said something is shown as finished | code + reproduced |
| M1 | medium | UI | The Activity re-reads only on `agent.*` events: the answer to a permission request never re-reads, the request can be missed | code + reproduced |
| M2 | medium | runtime, bus | A Claude Code tool result over about 240 KiB, including every clipped one, vanishes from the Activity | code + reproduced |
| M3 | medium | ACP, projection, chat | An ACP session's start-up settings warning is reported as the reason its first turn failed | code + reproduced |
| M4 | medium | ACP, gate, projection | A refused permission request leaves no trace: the ACP default reject, the gate's deny, a task rule's allow | code |
| M5 | medium | gate, UI | "Approve for this task" is recorded as a plain approval and the Activity says "Allowed once by you" | code |
| M6 | medium | presence | Presence says Working while a Claude worker waits for permission; the Activity and Needs you say the person is waited for | code |
| M7 | medium | core | After a crash during a permission wait the request stays REQUESTED forever; the readers disagree about it | code |
| M8 | medium | projection, UI | A command output cut at 8 KiB shows the cut marker as the failing reason and loses the failures past the cut | code |
| M9 | medium | core, UI | A thread whose start fell below the 20000-event cap is labelled from the agent row, not from what the process ran as | code |
| M10 | medium | core, UI | Every Activity read replays the whole history; reads overlap under a steady stream and the Activity freezes until a pause | code (measured) |
| M11 | medium | projection, UI | Arguments over 8 KiB are cut into invalid JSON; the UI loses the file path and the diff stat of a large Write | code + reproduced |
| M12 | medium (latent) | projection | Drydock's extra fields are stripped by the reference client; `metadata` is the protocol's carrier for them | code + reproduced |
| L1 | low (latent) | Claude parser | Claude's thinking text is emitted twice when the CLI streams it | code + reproduced |
| L2 | low | projection | RUN_STARTED does not declare `protocolVersion` | code + reproduced |
| L3 | low | projection | Tool-call ids are deduplicated per thread, not per run | code + reproduced |
| L4 | low | Claude parser, projection | A tool result whose call was never seen gets an empty tool name | code + reproduced |
| L5 | low | Claude parser, projection | Two text blocks after their deltas: the second block is emitted again | code + reproduced |
| L6 | low | runtime | A publish with a cancelled turn context drops the event about half the time | code (measured) |
| L7 | low | generator | The generated TypeScript types are looser than the spec's literals | code |
| L8 | low | projection tests | `CheckOrder` is credited to the wrong package; the order test's fixtures omit roles | code |
| L9 | low | gate, projection | The interrupt is neither ordered against nor linked to its tool call | code |
| L10 | low | ACP | A failed `session/prompt` shows the raw JSON-RPC error object | code |
| L11 | low | ACP | A tool call still pending at turn end is shown last and without a state | code |
| L12 | low | gate, UI | The internal `acp:` marker is shown in wait titles | code |
| L13 | low | ACP, UI | ACP's standard tool kind is not used to classify steps | code |
| L14 | low | UI | A running read grouped with earlier reads shows the group's age | code |
| L15 | low | UI | A process with no turn yet reads "0 turns" | code |
| L16 | low | presence | `agent.completed` with an error stop reason reads Completed, then Idle, in presence | code |
| L17 | low | chat, projection | The chat and the Activity judge a turn's end by two different rules | code |
| L18 | low | runtime, run page | A clean exit mid-turn is a failed node on the run page and a cancelled run in the Activity | reported |
| L19 | low | run page | The run explanation's Events tab lists none of the agents' events or permission decisions | by reading |
| L20 | low | docs | `docs/interfaces.md` does not mention `internal/agui` or `task.activity`; its chunk list is incomplete | by reading |
| L21 | low | docs | `docs/ui.md` describes three surfaces named Activity and not the task's feed; the Guide's event table says `agent.*` is agent CRUD only and omits `permission.decided` | by reading |
| L22 | low | comments | The package comment says usage has no place in the vocabulary; `compact`'s comment is wrong | by reading |
| R1 | refuted | ACP, gate | The cancelled permission answer races the turn end on an ACP Interrupt | refuted by code |
| R2 | refuted | Claude, gate | Interrupting a Claude worker leaves its permission prompt open for 30 minutes | refuted by code + reproduced |

### High

**H1. An ACP agent's sentence is shown twice after a thought or a tool result.** The ACP client streams every
`agent_message_chunk` as a text delta while accumulating it, and emits the accumulated text again as one `message_end` only at the
next tool call or at turn end (`internal/agents/acp/client.go` flushMessage, `session.go` endTurn). The projector closes the open
text message on a thinking chunk and on a tool result (`internal/agui/agui.go` output, the ChunkThinking and ChunkToolResult
cases), so when the `message_end` then arrives with no delta in between, `p.text` is empty and the "a message that came whole"
branch (around line 509) emits TEXT_MESSAGE_START, CONTENT and END with the whole text a second time. `CheckOrder` and the real
verifier accept the stream; the task page draws two identical paragraphs. Reproduced through the real adapter and the labelled
double `fakeacp` for text, thought, tool call (Say, Think, Tool) and for every permission step (tool call, text, tool result,
message end). The condition is exactly "no text delta between the closing event and the message end"; text, result, more text,
message end does not double. The same branch doubles a Claude Code turn when a whole thinking block lands after the text deltas
(the parser re-emits the block, see L1) and when one assistant message carries two text blocks after their deltas (L5). The fix
belongs in the projector, not the adapters: the runtime and the chat store each `message_end` as one assistant message, so
flushing on a thought would split them.

**H2. A Claude Code turn that failed after it had said something is shown as finished.** `Parser.result`
(`internal/agents/claude/parser.go`, around line 411) takes the result's `stop_reason` as the done chunk's stop reason and falls
back to the subtype only when `stop_reason` is empty; the result's error becomes a plain non-fatal error chunk. Claude Code 2.1.x
sets `stop_reason` on every result from the last assistant message, so a failure after the model has produced a message carries
`tool_use` (max turns, an execution error mid tool loop) or `stop_sequence` (an API error). The projector's `complete` fails the
run only for an `error`/`error_*` stop reason or when the turn gave no answer and did not end with `end_turn`; a turn that said
anything therefore ends RUN_FINISHED `success` with stop reason `tool_use`, and the error text is absent from the run's end. In
the API-error path the CLI also writes an assistant message whose text is the error ("API Error: 529 ...", marked
`is_api_error_message`); the parser turns it into a `message_end`, so the words appear as the agent's own text and set
`answered`, and even a first-call API error ends as success. The chat applies the same rule and records the turn done (it keeps
the error as a red row); the runtime returns the turn as succeeded to the workflow. The covering tests passed only because the
fake agent's failing shape has no `stop_reason` and no API-error message, which the real CLI does not match. Reproduced with
four result lines through the real parser and six turns through the projector. Fix: a failed result's stop reason must be the
subtype or the CLI's `terminal_reason` (present on every result), never the last message's; the API-error message must be an
error chunk, not an answer; the fake agent's failing shapes must be the CLI's.

### Medium

**M1. The Activity re-reads only on `agent.*` events.** `TaskActivity`'s query (`ui/app/screens/tasks/activity-panel.tsx`,
around line 440) accepts `e.type.startsWith('agent.')`, although the core stores and projects `approval.requested` and
`permission.decided`. The request usually still appears live because the worker's `tool_call` output is published about 45 ms
before the approval row and its 750 ms throttled reload reads the stored request; it is missed when an already-pending throttle
timer fires in that gap. The answer never re-reads: after a person allows a command no `agent.*` event arrives until the tool's
result, so the row keeps saying "Waiting for you ... answer it under Needs your decision" for the command's whole run time
(refusals flip quickly because the error result follows at once). Reproduced in a browser test with the panel's real store and
the recorder double for the read, and the event order measured through the real gate in a core test. Fix: widen the predicate to
`approval.requested` and `permission.decided` with the same task id, with a browser test.

**M2. A big Claude Code tool result vanishes from the Activity.** The runtime publishes a tool result as an `agent.output` event
`{kind, data}`; the bus stores an event whose data exceeds `events.MaxEventData` (256 KiB) as a stand-in `{bytes, head,
truncated: true}` with no kind, which the projector drops. The Claude parser's `clipOutput` keeps 256 KiB plus a marker, exactly
the cap, so every clipped result and every raw result from about 230 to 260 KiB (JSON escaping decides) is a stand-in: the audit
M22 head-and-tail excerpt never reaches storage, the call shows as running during the turn and "No result was reported for this
call" afterwards, and `isError` is lost. The ACP path (64 KiB clip), the chat (reads the chunks whole) and the Logs page are
unaffected; no secret leaks (redaction runs on the stand-in's head). Reproduced through the real runtime with 200 KiB to 2 MiB
fake results. Fix: bound the stored event in the runtime against `MaxEventData` (head and tail), not the parser, whose whole
output the chat may keep.

**M3. An ACP session's start-up settings warning is shown as the reason its first turn failed.** `applyRequested`
(`internal/agents/acp/session.go`) warns, as an uncoded non-fatal error chunk, when a requested effort, permission mode or model
is not one the agent offers, and `openSession` when an MCP server needs a transport the agent did not advertise. These are
buffered and first read by `RunTurnImages` after it published `agent.waiting{working}`, so they are stored inside the first run.
The projector records an uncoded error as the run's last error and fails a run that has one, no answer and a stop reason other
than `end_turn`: a first turn with tool calls only that stopped with `max_turn_requests`, `max_tokens` or `refusal` ends
RUN_ERROR whose message is the settings note and whose real stop reason sits only in `code`, which the task page does not show.
The permission-mode variant is the everyday trigger: `CreateAgent` defaults to `acceptEdits`, nothing maps it to ACP mode ids,
and `applyRequested` warns on every ACP session. Only the session's first run is affected; the identical second turn is
RUN_FINISHED. Reproduced through the real runtime, bus and projector with `fakeacp`. The rule itself (an error, no answer, no
`end_turn` is a failed turn) is documented in P87 and tested; the defect is that a warning that pre-dates the turn is attributed
as its failure. Fix: give these warnings a code, as adapter drift has, so neither the Activity nor the chat counts them.

**M4. A refused permission request leaves no trace in the Activity.** The projector has no case for the `permission_request`
chunk and emits `drydock.resume` only for a `permission.decided` whose approval id matches an announced interrupt. Two paths are
invisible: a session with no asker (read-only, sandboxed, or permission prompts off), where the ACP client's default `RejectOnce`
answers and the gate never runs, so no `permission.decided` exists at all; and the gate's hard deny, `allowed_by_rule` and
`approved_for_task`, whose `permission.decided` carries no approval id. In the denial cases the person sees a failed tool call
with an empty result and no word that Drydock refused it or why; the UI's "Refused by Drydock's rules" text is unreachable
except for an edited approval. The same holds for the Claude CLI's own `permission_denials`, which become `permission_denied`
chunks the chat shows as "Not allowed" rows and the Activity drops (the CLI also emits an error tool result for them, so the
step at least shows as failed). Confirmed by the code-tracing skeptic, who reproduced the ACP case through the real adapter.

**M5. "Approve for this task" is recorded as a plain approval.** When a person answers with scope `task`, the core stores the
rule, then decides the approval as an ordinary approval; the gate's wait loop can only record outcome `approved` for that
request, with no rule id on its `permission.decided` or audit entry; only later calls the rule covers get `approved_for_task`.
The projector emits `drydock.resume {outcome: approved}` and the Activity says "Allowed once by you", misstating the breadth of
the grant, which no screen on the task page lists otherwise. `waitOutcomeText` has no `approved_for_task` case and would print
the raw token. Confirmed by code.

**M6. Presence says Working while a Claude worker waits for permission.** On the Claude path the wait reaches the bus only as
`approval.requested` and `permission.decided`, which `room.Derive` ignores and the tracker never subscribes to; only the ACP
client's `permission_request` chunk sets Waiting "awaiting permission". The Agents page, the crew cards and the inspector say
Working "tool: Bash" for the whole wait while the task Activity says "Waiting for you" and Needs you shows the request. Confirmed
by code; the first reviewer reproduced it in a throwaway room test.

**M7. After a crash during a permission wait the request stays REQUESTED forever.** When the process dies without `Core.Close`,
nothing cancels or expires the approval row: `CloseStale` closes only the agent run, recovery re-runs the turn (which asks again,
creating a second identical request), and no caller of `ExpireDue` or `CancelForRun` runs outside the gate's own waiting loop.
Needs you and `task.get` list the dead request indefinitely beside the live duplicate; `task.summaries` drops it after the
30-minute TTL; the Activity shows the old turn as RUN_ERROR "the app stopped while this agent was running" with the wait "Not
answered before the turn ended". Within the TTL a person's answer moves the dead row to APPROVED with nobody to consume it.
Confirmed by code.

**M8. A cut command output shows the cut marker as the failing reason.** `agui.clip` keeps the first 8 KiB of a result and
appends "[cut by Drydock: longer than the activity view keeps]"; `activity-steps.ts` treats the text as the command's output, so
for a failed run whose output exceeds 8 KiB (`go test ./...` on a repository with more than roughly 130 packages, or a verbose
failing package) `testFailures` finds no failure past the cut and `lastLine` returns the marker as the step's reason: "Ran go
test ./... · failed" with the marker beneath, and the failing test named nowhere on the task page. The "failed" badge stays
right; the stored event still holds the tail. Confirmed by code.

**M9. A truncated thread is labelled from the agent row.** When `task.activity` is truncated (more than 20000 stored events), a
thread whose `agent.started` fell below the cut is labelled by `TaskActivity` from the agent row's current adapter and model,
with the run mode and distro left empty although the row carries them. The row is not what the process ran as: the router or a
fallback hop sets the process's model per turn and a later `agent.update` rewrites the row, while `agent_runs` stores no model or
run mode. The thread's row, and the head when it is the only surviving thread, then read "Builder · Claude Code · <current
model>". Confirmed by code; `internal/core/activity_test.go` has no test of the cap path.

**M10. Every Activity read replays the whole history.** `task.activity` pages a task's whole stored `agent.*` history from seq 0
on every read and keeps the newest 20000 (its comment says only the newest are read; the loop reads all). The Activity re-reads
at most once per 750 ms while output streams, and `Live` re-arms the throttle when the timer fires without waiting for the load,
while `reload` drops any answer a newer reload has superseded. Once a single read takes longer than 750 ms, reads overlap and no
answer is shown until the stream pauses for a full interval: the Activity freezes during each long text stream and catches up at
the next pause. Measured on this host: 760 to 877 ms per read for a task with 100000 stored events (101 pages, 5.2 MB of JSON),
about 250 ms at the 20000 cap, 25 ms for a 2000-event task. Fix: an incremental read (after seq) or a per-task projection kept in
the core, and a throttle that waits for the load in flight.

**M11. Arguments over 8 KiB are cut into invalid JSON.** `startCall` emits a call's arguments as one TOOL_CALL_ARGS delta cut by
`clip`, which puts a raw newline and the marker inside a JSON string literal: neither valid nor truncated JSON, so partial-JSON
repair (the reference reducer's untruncate step) fails where a bare prefix would parse. The visible cost is in Drydock's own UI:
for a Write or Edit whose arguments exceed 8 KiB (any file over about 8 KB written by Claude Code, whose arguments are the whole
input) `JSON.parse` fails, `pathOf` returns nothing and the step line reads "Edited a file" with no file name or line counts.
Reproduced in Go, Node (with `untruncate-json`) and a UI unit test. Fix: keep the arguments a valid JSON document by cutting
long string values inside it, and let the UI prove `pathOf` on clipped arguments.

**M12. Drydock's extra fields are stripped by the reference client (latent).** Every projected event carries `seq` at top
level, RUN_FINISHED `stopReason`, TOOL_CALL_RESULT `isError` and RUN_ERROR `threadId`/`runId`; AG-UI 1.0 declares none. The
loose schemas accept them, but the shipped client runs `enforceEvents` before verification on every pipeline and strips each
undeclared key with a console warning (reproduced: a 13-event turn gives 15 warnings; the failed tool result reaches the reducer
as a plain tool message indistinguishable from a success; the verifier accepts the stream, so the loss is silent). No person is
affected today: Drydock is the only consumer and its UI reads the fields from the generated types. The protocol's `metadata`
object is the sanctioned carrier and survives (TOOL_CALL_RESULT's metadata is merged into the minted tool message; RUN_FINISHED
and RUN_ERROR keep theirs). Worth doing now: it is cheap and it is the interoperability the owner asked about. Note that AG-UI
1.0 itself has no producer-side failure marker on TOOL_CALL_RESULT (`ToolMessage.error` is a client-side field the apply path
never lifts), so a stock AG-UI UI would still not render the failure; a Drydock-aware one would.

### Low

**L1. Claude's thinking text is emitted twice when the CLI streams it (latent).** The parser emits a thinking chunk per
`thinking_delta` and again for the whole block in the following `assistant` event; `partialSeen` is set and never read. The
projector appends the whole block to the open reasoning message ("let me looklet me look") and the chat stores it twice.
Latent today: headless Claude Code 2.1.294 to 2.1.310 without `--thinking-display` delivers the thinking block empty (the fixture
shows `"thinking": ""` with 161 thinking tokens), so most turns show no Claude reasoning at all rather than doubled reasoning. It
surfaces the moment thinking text flows (a user setting, a future CLI or API default). Reproduced through the real parser.

**L2. RUN_STARTED does not declare `protocolVersion`.** The 1.0 migration notes say a 1.0 producer declares it; the client treats
an absent value exactly like "1.0", so nothing misbehaves. A constant and a regenerated type close it.

**L3. Tool-call ids are deduplicated per thread.** `projector.started` is never reset per run, so a call id reused in a later
turn of the same process loses its TOOL_CALL_START, ARGS and END; the UI's thread-wide tool map then overwrites the earlier step
and the later turn shows no step at all. Needs an agent that breaks its own protocol (Claude's ids are unique; the ACP SDK
requires uniqueness within a session), so low; reset per run anyway, as the verifier and `CheckOrder` do.

**L4. A tool result whose call was never seen gets an empty tool name.** The Claude parser yields `ToolResult{Name: ""}` for a
`tool_result` whose `tool_use` it never saw (an unreadable assistant event counted as drift); the projector stores
`toolCallName: ""`, which the schema accepts and the UI draws as "Tool". The ACP adapter already substitutes "tool".

**L5. Two text blocks after their deltas: the second is emitted again.** When one API message carries two text blocks, the
deltas of both open one text message and the parser emits one `message_end` per block; the first closes the message, the second
finds none open and is emitted whole. Rare (a multi-text-block message in Claude Code's main loop); the H1 fix covers it when it
checks the streamed text rather than only the open message.

**L6. A publish with a cancelled turn context drops the event about half the time.** `observe` and the `agent.waiting` and
`agent.completed` publishes pass the turn's context to `Bus.Publish`, which gives up with `ctx.Err()`; when the task is
cancelled in the same instant a chunk is pending, the loop's select picks the chunk half the time and the publish then drops it
(measured 537 of 1000). A lost `agent.completed` makes the Activity say cancelled while the run record says done. The usage and
delta publishers already detach from cancellation; these should too.

**L7. The generated TypeScript types are looser than the spec's literals.** `ts.go` fixes a literal only for `type` and the
reasoning role; `role` on text and tool messages and `RunOutcome.type` are `string`. Drydock always emits the right values and a
Go test asserts them, so cosmetic.

**L8. `CheckOrder` is credited to the wrong package; the order test's fixtures omit roles.** `order.go` says the verifier is
"@ag-ui/core 1.0.3" (it ships in `@ag-ui/client`); the fixtures of `TestCheckOrderRefusesWhatAGUIsVerifierRefuses` never set a
role, so their marshalled form fails the real schema while passing the verifier, and one case (a lone RUN_ERROR) is Drydock's
deliberate strictness, which the test's name does not say.

**L9. The interrupt is neither ordered against nor linked to its tool call.** The tool call (runtime goroutine) and the gate's
`approval.requested` (handler goroutine) are published without synchronisation; in every probe the call landed first, and if the
order ever flips the only effect is the wait drawn above its tool card. `InterruptValue` carries no `toolCallId` although the
ACP asker knows it; AG-UI's `Interrupt` has that field.

**L10. A failed `session/prompt` shows the raw JSON-RPC error object.** `endTurn` emits "prompt failed: " plus
`err.Error()`, and the SDK's `RequestError.Error()` is the compact JSON `{"code":-32603,"message":"Internal
error","data":{"error":"rate limited"}}`; the task page prints it verbatim, as do the chat and the connection test.

**L11. A tool call still pending at turn end is shown last and without a state.** An ACP call announced as pending with no
input and never updated is held by `progressTool` and only emitted by `flushTools` at turn end, so its card appears last, dated
at the turn's end, with "No result was reported for this call". Cosmetic ordering; the hold is deliberate.

**L12. The internal `acp:` marker is shown in wait titles.** An ACP request the analyzer cannot map to Bash, Read or Write gets
tool "acp:<title>", and the marker is shown in four places: the Activity's wait row, the task page's permission card, the Needs
you row and its detail. The prefix exists to keep ACP names out of the Claude tool tables; it should not reach a person.

**L13. ACP's standard tool kind is not used to classify steps.** The compact steps classify a call by its name alone; the ACP
adapter keeps the kind only when the agent sent no input, so every ACP agent but the Claude Code bridge yields generic "tool"
steps labelled with the phrase title, and consecutive reads are not grouped.

**L14. A running read grouped with earlier reads shows the group's age.** `stepsOf` merges consecutive reads and keeps the first
read's time; a running later read shows "41 s" where it began a second ago. Visible only with an ACP agent whose reads take
longer than the 750 ms throttle.

**L15. A process with no turn yet reads "0 turns".** A thread whose stored events project to no event is returned with an empty
list and drawn as "<agent> · <CLI> · <model> · 0 turns". Reachable only in the truncation edge (the start-up window is covered
by `agent.waiting{working}` before the first read).

**L16. `agent.completed` with an error stop reason reads Completed in presence.** `room.Derive` makes it Completed with the raw
stop reason as the activity text, which the Agents page maps to Idle for the one or two seconds until the caller's Stop yields
Stopped; the Activity says RUN_ERROR for the same event.

**L17. The chat and the Activity judge a turn's end by two different rules.** The Activity fails a run on any `error`/`error_*`
stop reason even after an answer, as P87 states; the chat marks such a turn done with a red error row. The Activity ignores coded
adapter-drift warnings for the turn's error; the chat counts them. So a Claude turn with a first-seen drift event, no finished
message and a stop reason that is neither `end_turn` nor `error_*` is an error in the chat and a success in the Activity. One
shared rule fixes both and M3.

**L18. A clean exit mid-turn is a failed node on the run page and a cancelled run in the Activity (reported, not verified).** A
process that exits cleanly mid-turn, or a failed Send, is said to end the thread as RUN_FINISHED `cancelled` while the run page
shows the node execution failed. The runtime could publish `agent.failed{phase: run}` so both say the same.

**L19. The run explanation's Events tab lists none of the agents' events.** `ExplainRun` reads `events WHERE run_id = ?` with
the workflow run id; the agents' events and the gate's carry the agent run id, so the tab lists `workflow.*` (and `mcp.*`) only.
Verified by reading `internal/core/ext_explain.go`.

**L20. `docs/interfaces.md` does not mention `internal/agui` or `task.activity`**, and its Agents section lists the chunk
vocabulary without `plan`, `permission_request`, `permission_denied` and `log`. Verified by reading.

**L21. `docs/ui.md` describes three surfaces named Activity** (the project's event log page, the dock's usage tab, and the
task's feed, the last not described at all); the Guide's event table (`ui/app/screens/guide/content/reference.ts`) describes
`agent.*` as "An agent was added or changed" and lists no `permission.decided` under approvals. Verified by reading.

**L22. Comments.** The package comment says usage "has no place in the vocabulary" (AG-UI 1.0 carries `TokenUsage[]` on
RUN_FINISHED and RUN_ERROR); `compact`'s comment says it joins tool arguments (it joins consecutive text and reasoning deltas).
Verified by reading.

### Refuted

**R1. The cancelled permission answer does not race the turn end on an ACP Interrupt.** The only production caller of
Interrupt is the watchdog's `CancelTurn`, which sets `ctl.await`, and `RunTurnImages` then holds the done chunk until a nudge
arrives or the 5 s nudge wait elapses before publishing `agent.completed`; the gate's `permission.decided{cancelled}` lands
inside the open run and the Activity shows "Withdrawn before anyone answered". A note only: the client answers the agent
"cancelled" without waiting for the asker, so the record and the turn's end run concurrently, and only a multi-second stall of
three local SQLite writes could reorder them.

**R2. Interrupting a Claude worker does not leave its permission prompt open.** Claude Code aborts the in-flight
`permission_prompt` MCP call and its SDK sends `notifications/cancelled`; the shim relays it, the gateway cancels the handler
context, and `PermissionGate.ask` cancels the approval and publishes `permission.decided{cancelled}` within milliseconds
(verified for Drydock's side through the real shim; the CLI side read from the bundle, not a live run). Residual notes: the
Claude path relies on the CLI's cancel notification where the ACP session cancels explicitly, and the fake agent does not abort
its prompt call on interrupt, so the path is untested with the double.

### Notes

- **N1. The protocol did not move.** `@ag-ui/core` 1.0.0 and 1.0.3 declarations are byte-identical, as is the 1.0.4 canary;
  `cancelled`, `protocolVersion`, usage, sub-agents and the reasoning span are 1.0.0 shapes. The `interrupts` page's prose still
  shows only `success` and `interrupt`; the schema is authoritative.
- **N2. Features with a declared home that Drydock leaves empty.** Usage chunks are dropped although RUN_FINISHED and RUN_ERROR
  carry `TokenUsage[]`; calls that never received a result are forgotten at run end although the success outcome has
  `pendingToolCallIds`; TOOL_CALL_START omits `parentMessageId` although the projector knows the message it just closed; the
  REASONING_START/END span is not sent (the verifier does not require it); `rawEvent` could carry the original chunk.
- **N3. `isError` is Drydock's own.** TOOL_CALL_RESULT has no error channel in 1.0 and the reference reducer copies only
  messageId, toolCallId, content and role; the protocol's `error` exists only on the materialised tool message. Worth a line in
  the package doc, and worth watching for a later revision.
- **N4. Run ids restart after the cap.** A thread whose early turns were cut gets `:1` for what an untruncated read called `:7`;
  the UI does not key on run ids across reads, so only a consumer that stores by run id would notice.
- **N5. A watchdog interrupt and nudge keep two CLI turns inside one run** and leave no trace of the interrupt or the nudge; a
  CUSTOM `drydock.nudge` would make the intervention visible.
- **N6. `terminal_reason` and `permission_denials` are unused.** The CLI's `terminal_reason` is its own word for how the turn
  ended (`completed`, `max_turns`, `api_error`, `aborted_streaming`, ...) and would replace the stop-reason heuristics (H2);
  `permission_denials` become chunks the Activity drops (M4). Since the fixes: the parser reads `terminal_reason` for every
  failed result (the stop reason is `error_<terminal_reason>` when the subtype does not name the failure); `permission_denials`
  stay out of the Activity on purpose (see M4 in section 8).
- **N7. The interrupt shape.** Two conformant upgrades exist: cheap, shape the CUSTOM values as AG-UI's `Interrupt` (`id`,
  `reason`, `message`, `toolCallId`, `expiresAt` as RFC 3339, `metadata`) and `ResumeEntry` (`interruptId`, `status`, `payload`);
  structural, end the run at the wait with the `interrupt` outcome and resume as a new run, exactly the spec's tool-bound flow,
  which costs the one-run-per-turn invariant and buys nothing while Drydock is the only consumer.
- **N8. Sub-agents and steps.** OpenCode child sessions arrive with the ids SUBAGENT_* needs (today folded under prefixed call
  ids with their text dropped); Claude Code sub-agents are invisible to the parser (no `parent_tool_use_id` read). STEP_* is a
  poor fit for workflow nodes, which span turns.
- **N9. State and message snapshots and content parts** are available but of low value until an external reader exists; diffs
  are already flattened at the adapter.
- **N10. Capabilities** are a client-side `getCapabilities()`, not a wire event; `agents.Capabilities` maps onto them when an
  endpoint exists.
- **N11. What "working" means.** Today Drydock is producer and sole consumer and the stream verifiably passes the published 1.0.3
  client; the deprecated THINKING_* names, the draft meta events, the protobuf transport's gaps (it cannot carry TOOL_CALL_RESULT,
  reasoning or activity events) and SSE do not touch Drydock. A CopilotKit reader would need an HTTP endpoint, the metadata fix
  (M12) and a thread-replay model; the vocabulary itself would not have to change.

## 6. Recommendations

**Now, in this branch:** H1, H2 and L1 together (the projector keeps the streamed text and the parser stops re-emitting the
thinking block; a failed Claude result carries a failing stop reason and the API-error message is an error, with the fake agent
made realistic); M1 (the re-read predicate, with a browser test); M2 (bound the stored event in the runtime); M3 and L17 together
(one shared rule for "did this turn fail" that both the chat and the Activity read, and a code on settings warnings); M4 (project
refusals and task-rule decisions as a CUSTOM event, and let an ACP session with nobody to ask say so); M5 (record
`approved_for_task` on the answered request); M6 and L16 (presence reads the gate's events); M8 and M11 (results keep head and
tail, arguments stay valid JSON, the UI strips the markers); M12, L2, L3, L4, L8, L22 (metadata, `protocolVersion`, per-run ids,
the placeholder name, the attribution and the comments); L10 (unwrap the JSON-RPC error); L14 and L15 (the two small UI wrongs);
L20 (`docs/interfaces.md`).

**Next:** M7 (cancel the approvals of the runs `CloseStale` closed and sweep expired rows periodically); M10 (an incremental
`task.activity` read and a throttle that waits for the load in flight; this needs a design of its own); M9 (record model and
run mode on the `agent_runs` row at spawn, or say "start not shown"); L6 (detach the chunk and completion publishes from the
turn's cancellation); L9 and N7 (carry `toolCallId` on the interrupt and shape the CUSTOM values as AG-UI's); L12 and L13 (drop
the `acp:` marker from what a person sees; carry ACP's kind through the chunk); L18 and L19 (one word for a turn the agent
abandoned; stamp agent events with the workflow run or widen the explain query); N2 (usage on RUN_FINISHED, `pendingToolCallIds`,
`parentMessageId`); N6 (`terminal_reason` as the first word on how a turn ended).

**Not for now:** N7's structural upgrade, N8, N9, N10 and an AG-UI HTTP endpoint: each is a design decision for the day an
outside reader exists, and nothing in the vocabulary has to change first.

## 7. Open questions for the owner

- Should later identical calls allowed by a task rule (`approved_for_task` without an approval) be shown in the Activity at all?
  This review's fix shows them as decided rows, the way a refusal is shown.
- Should `task.activity` keep arguments as JSON a future AG-UI consumer can accumulate (this review's fix keeps them valid), or is
  a display string enough?
- When an outside reader is wanted, should a thread be the agent process (as now) or the task, given AG-UI's thread is "the
  conversation"?

## 8. Status after the fixes

The fixes landed in this branch on 2026-10-26 (decision P100), written by five implementers, one per file cluster, then read
by three independent reviewers (Go correctness and tests; conformance against the published packages, with a probe that ran
the projected threads through `@ag-ui/core/schemas` and the `@ag-ui/client` pipeline again; the UI), whose confirmed findings
(nine low, one medium: a process stopped before its first turn would have read "starting") were fixed in turn. Every change
ships with tests against the labelled doubles `fakeagent` and `fakeacp`; no AG-UI package became a dependency.

| # | Status | What changed |
|---|---|---|
| H1 | fixed | The projector keeps the text streamed in the open run that no `message_end` has taken yet; a `message_end` that repeats the front of it takes that text and adds no second message, so a thought or a tool result between the deltas and the message end shows the sentence once (ACP's Say, Think, Tool; every permission step; Claude's per-block messages, three blocks tested). A message with other words is its own, however short. `TestAnAcpSentenceIsShownOnce...` |
| H2 | fixed | A failed Claude Code result never ends with the last message's stop reason: the done chunk carries the subtype when it names the failure (`error_during_execution`, `error_max_turns`, ...), else `error_` plus the CLI's `terminal_reason` (`error_api_error`, ...), else `error`. The CLI's API-error assistant message (`is_api_error_message` on the event, verified in the 2.1.310 binary) is an error chunk, not an answer. The fake agent's failing shapes are the CLI's (`Step.Fail`, `ModelErrors`). Proven end to end from the fake agent through the real parser to RUN_ERROR. |
| M1 | fixed | The Activity re-reads on `approval.requested` and `permission.decided` for its task as well as on `agent.*`; the browser test fails with the old predicate. |
| M2 | fixed | The runtime bounds a stored `tool_result` event against `events.MaxEventData` (head and tail around a marker that says how many bytes are missing), so no result becomes a stand-in; a 300 KiB fake result reaches the Activity as TOOL_CALL_RESULT. |
| M3 | fixed | The ACP settings warnings (effort, permission mode, model, MCP transport) carry `workflow.CodeSettings`; `workflow.TurnFailed` never counts a coded warning. A tool-only first turn ending `max_turn_requests` is RUN_FINISHED and, in a chat, done. |
| M4 | fixed | CUSTOM `drydock.decided` {tool, outcome, reason, toolCallId, rule}: a gate deny or a task rule's allow that asked nobody (`allowed_by_rule` is left out on purpose), and the refusal an ACP session with nobody to ask now reports as a `permission_denied` chunk with its reason (outcome `refused`). The Activity draws it as a decided row ("Refused by Drydock's rules: Bash (rule: ...)", "Refused without asking: Write: config.json (nobody could be asked in this session, so Drydock refused the request)"). The CLI's own `permission_denials` stay out: a core test showed they would repeat, with the wrong words, a refusal the resume row already tells, and the failed result shows the step as failed. |
| M5 | fixed | `PermissionGate.MarkApprovedForTask` lets the answered request itself be recorded `approved_for_task` with its rule (event, audit entry, resume outcome); the Activity says "Allowed by you for this task". `docs/security.md` lists the outcome. |
| M6 | fixed | `room.Derive` reads `approval.requested` (a permission title) as Waiting "awaiting permission" and `permission.decided` as Working again, keeping what the agent was doing; both types are in the presence and lifecycle filters. `docs/room-protocol.md` says so. |
| M7 | open | Recommended for a change of its own (cancel the approvals of the runs `CloseStale` closed; sweep expired rows). |
| M8 | fixed | A result over 8 KiB keeps its first 6 KiB and last 2 KiB around a marker line; the UI strips the marker lines before it reads a command's last line or its test failures and keeps them where the raw result is shown. |
| M9 | open | Recommended (record model and run mode on the `agent_runs` row, or say "start not shown"). |
| M10 | open | Recommended; needs a design of its own (an incremental read, a throttle that waits for the load in flight). |
| M11 | fixed | Arguments over 8 KiB stay one valid JSON document: the longest string values are cut inside it (sorted keys so a tie cuts the same string on every read; numbers kept as written), so a large Write keeps its path and line counts. |
| M12 | fixed | `metadata.drydock` {seq, stopReason, isError, threadId, runId} replaces the top-level extras; a test holds every event's top-level keys to exactly the fields the 1.0.3 schema declares for its type; the conformance reviewer's probe saw zero stripped keys and the verifier pass. |
| L1 | fixed | A thinking block whose deltas were streamed is not emitted again; the text blocks of one assistant event are one `message_end`. |
| L2 | fixed | RUN_STARTED carries `protocolVersion: "1.0"` (`PROTOCOL_VERSION` in the generated types). |
| L3 | fixed | Tool-call ids are deduplicated per run. |
| L4 | fixed | A result whose call was never seen is named `tool`. |
| L5 | fixed | Covered by H1's rule (one `message_end` per block takes its own part of the streamed text). |
| L6 | open | Recommended (detach the chunk and completion publishes from the turn's cancellation). |
| L7 | open | Cosmetic; the generator would need a union-of-literals form. |
| L8 | fixed | `CheckOrder` credits `@ag-ui/client` 1.0.3; the order test's fixtures were left as they are (they test ordering, not the schema). |
| L9 | open | Recommended with N7. |
| L10 | fixed | A failed `session/prompt` reads "prompt failed: Internal error: rate limited" (the JSON-RPC message, then `data.error` or a short rendering of `data`). |
| L11 | open | Cosmetic; the hold is deliberate. |
| L12 | open | Recommended. |
| L13 | open | Recommended. |
| L14 | fixed | A running read grouped with earlier reads counts from the read that runs (`runningSince`). |
| L15 | fixed | A thread with no turn yet reads "starting" when it is the newest and is left out otherwise; one whose process was stopped before its first turn reads "stopped before its first turn" (`AguiThread.ended`, from the reviewers' medium finding). |
| L16 | fixed | `agent.completed` with an error stop reason is presence Failed "turn failed: <stop>" (the same `workflow.TurnFailed`), not Completed. |
| L17 | fixed | The chat's `turnRun.finish` reads `workflow.TurnFailed`: an `error_*` stop after an answer is a failed turn in the chat too; a coded warning never fails one. |
| L18 | open | Reported, not verified; recommended. |
| L19 | open | Recommended. |
| L20 | fixed | `docs/interfaces.md` has the Activity section and the full chunk list. |
| L21 | fixed | `docs/ui.md` names the three surfaces; the Guide's table describes `agent.*` and `permission.decided`. |
| L22 | fixed | The package comment says usage is not projected yet; `compact`'s comment is right; the comment also says the schema is the same from 1.0.0 to 1.0.3 and that the `metadata.drydock` fields are Drydock's own. |
| N2, N6 | partly | `terminal_reason` is read for failed results. Usage on RUN_FINISHED, `pendingToolCallIds` and `parentMessageId` stay recommended. |

Verification of the branch after the fixes, on this Linux host: `go test ./...` passes in every package (`internal/core` was run
again on its own after three local-model tests failed for want of disk space, a failure the pushed HEAD shows too when the drive
is nearly full, and after one test showed that projecting the CLI's own `permission_denials` would repeat a refusal the resume row
already tells, which narrowed M4's rule); `gofmt`, `go vet` on Linux and Windows and golangci-lint (0 issues) are clean; the UI
suite passes 1762 of 1762 tests with the typecheck and the build; the Playwright end-to-end suite against the real core passes 78
of 78. Not verified here: Windows and WebView2, the real Claude Code and ACP agents (the labelled doubles stood in, with the fake
agent's failing shapes taken from the 2.1.310 binary's own strings).
