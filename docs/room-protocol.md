# Drydock: Engineering Room protocol

Scope: this document specifies the Engineering Room (`internal/room`): conversation kinds, the structured message types and their JSON schema, mentions, threads and markers, presence states, turn-taking and room modes, tool members, exports (minutes, ADR candidates, PR description drafts), unread tracking and the "Needs you" inbox, how agents join channels, the mapping between room behavior and the `room.*` and related events, and exactly what an agent receives from a room (never the whole workspace). Section numbers "brief §N" refer to `docs/brief.md` (the room is brief §11); D1-D14 to `docs/decisions.md`; security rules to `docs/security.md`; budget rules to `docs/token-control.md`. SQLite is the single authority for all state (brief §3): the Event Bus carries notifications of state, and the UI never treats an event as the record. Most of this is built in `internal/room` and tested on Linux; the "As built" notes in sections 1, 9, 10 and 13 and section 15 say where the code differs from the design. Statements not found in the brief or decisions are marked **design** (a proposal for owner review) and carry no evidence claim.

## 1. Conversation kinds

One `rooms` table (brief §11).

| Kind | Purpose | Created by | Members | Lifetime |
|---|---|---|---|---|
| `channel` | Project-wide topics (`#general`, `#architecture`) | Human | Humans and agents added by humans | Until archived |
| `task` | One channel per task, named `task-<id>`; the task conversation of record | Automatically at task creation (human action or stored rule creates the task) | Task owner, assigned agent(s), reviewer(s), tool members | Archived at `COMPLETED`/`CANCELLED`/`FAILED`; messages kept |
| `dm` | Human with one agent | Human | Exactly 2 | Persistent |
| `group` | Ad hoc multi-agent discussion, optionally with humans | Human | Explicit list | Until archived |

```
rooms(id, project_id, kind, name, topic, task_id NULL, mode, mode_params_json, archived_at NULL,
      created_by, created_at, version)             -- version: compare-and-swap (brief §18)
room_members(room_id, member_kind, member_id, role, state, added_by, added_at)
messages(id, room_id, seq, type, author_kind, author_id, thread_root_id NULL, reply_to_id NULL,
         body, payload_json, refs_json, markers_json, mentions_json, trust, supersedes_id NULL,
         created_at, run_id NULL, task_id NULL)    -- immutable rows
message_attachments(message_id, name, mime, bytes, sha256, path)
room_reads(user_id, room_id, last_read_seq, last_mention_read_seq)
room_digests(id, room_id, from_seq, to_seq, text, produced_by, created_at)   -- rolling compression
exports(id, room_id, kind, from_seq, to_seq, content, sha256, created_by, created_at)  -- immutable
```

**As built.** Every project gets `#general`, `#reviews`, `#decisions` and `#alerts` (`EnsureDefaultChannels`). There is no RPC or UI to create another channel or a group: `CreateChannel`, `CreateGroup` and `ConvertGroupToChannel` exist in `internal/room` but are not exposed. A task room is named `task-<number>-<title slug>` and is archived on the `task.completed`, `task.cancelled` or `task.failed` event. A DM is opened from the Team page, the Agent Inspector or the sidebar (`room.createDM`). The tables differ from the sketch above: read cursors are columns of `room_members`, exports are `room_exports`, attachments `room_attachments`; there is no `room_digests` table (a thread digest is computed when a turn's context is built).

`seq` is a per-room monotonic integer assigned by the single writer inside the insert transaction. The Event Bus `seq` is separate (global, monotonic, brief §10). Project section pages (Overview, Requirements, Tasks, Milestones, Activity, Integrations) are views over other tables and are not rooms.

## 2. Message envelope and types

Messages are **immutable**. A correction is a new message with `supersedes_id`; removal is a human tombstone that records who and why in the audit log (security.md s12). Streaming agent output is not stored as messages: chunks go to the run output store and `agent.output` events drive a live card in the UI (brief §4: raw output on disk), and the finished turn becomes one message. This keeps the room small and keeps token cost of room reads bounded (token-control rule 1).

Types (brief §11): `CHAT, TASK, DECISION, STATUS, RESULT, QUESTION, HANDOFF, ERROR, APPROVAL_REQUEST, APPROVAL, SYSTEM`.

| Type | Who may author | Meaning | Payload (beyond the envelope) |
|---|---|---|---|
| `CHAT` | human, agent | Free text | none |
| `TASK` | human, system, PM-role agent (as `proposed`) | Task statement or assignment | `task_id, title, acceptance[] (Given/When/Then), assignee, state` |
| `DECISION` | human, agent (as `proposed`) | A decision; becomes an ADR candidate (s8) | `title, decision, rationale, alternatives[], status: proposed\|accepted\|superseded, supersedes?` |
| `STATUS` | agent, tool, system | Progress or state change | `state, summary, task_id?, percent?` |
| `RESULT` | agent, tool | Outcome with evidence | `summary, evidence[] (run output refs, check ids, diff ref), diff_stat?, check_status?` |
| `QUESTION` | agent, human | A question to someone; `blocking` questions create a request | `to, blocking, options[]?, request_id?` |
| `HANDOFF` | agent, system | Handoff artifact posted | `handoff_id, from, to, artifact_ref, summary` |
| `ERROR` | system, tool, agent runtime | Failure notice | `code, class, recoverable, run_id, node_id, retry_policy?` |
| `APPROVAL_REQUEST` | **system only** | Human approval needed | `approval_id, kind, action_hash, canonical_action_summary, expires_at` |
| `APPROVAL` | **system only, written from a human UI decision** | Decision record | `approval_id, decision, decided_by, action_hash` |
| `SYSTEM` | system | Room events (member added, mode changed, debate bound reached, escalation) | `event, params` |

Hard rules: `APPROVAL_REQUEST` and `APPROVAL` are rejected at insert unless the writer is the approvals service acting on a call from the UI service layer (security.md s5). An `APPROVAL`-looking text in a `CHAT` is just text. Messages authored by agents carry `trust="untrusted"` for recipients; human messages are `trusted` as instructions; tool and system messages are `trusted` as data about their own facts but their quoted output (test logs) is data (security.md s10).

JSON Schema (draft 2020-12, abridged; the Go types and the TypeScript bindings are generated from one definition):

```json
{
  "$id": "drydock/room-message/v1",
  "type": "object",
  "required": ["id", "room_id", "seq", "type", "author", "body", "created_at", "trust"],
  "properties": {
    "id": {"type": "string", "pattern": "^msg_[0-9A-HJKMNP-TV-Z]{26}$"},
    "room_id": {"type": "string"},
    "seq": {"type": "integer", "minimum": 1},
    "type": {"enum": ["CHAT","TASK","DECISION","STATUS","RESULT","QUESTION","HANDOFF","ERROR","APPROVAL_REQUEST","APPROVAL","SYSTEM"]},
    "author": {"type": "object", "required": ["kind","id"],
               "properties": {"kind": {"enum": ["human","agent","tool","system"]}, "id": {"type": "string"}, "display": {"type": "string"}}},
    "thread_root_id": {"type": ["string","null"]},
    "reply_to_id": {"type": ["string","null"]},
    "supersedes_id": {"type": ["string","null"]},
    "body": {"type": "string", "maxLength": 32768},
    "markers": {"type": "array", "items": {"type": "object", "required": ["marker","line"],
                "properties": {"marker": {"enum": ["DECISION","TODO","STATUS","RESULT"]}, "line": {"type": "integer"}, "text": {"type": "string"}}}},
    "mentions": {"type": "array", "items": {"type": "object", "required": ["kind","id"],
                 "properties": {"kind": {"enum": ["agent","role","human","tool","task","room","message"]}, "id": {"type": "string"}, "span": {"type": "array", "items": {"type": "integer"}, "minItems": 2, "maxItems": 2}}}},
    "refs": {"type": "object", "properties": {"task_id": {}, "run_id": {}, "node_id": {}, "approval_id": {}, "checkpoint_id": {}, "handoff_id": {}, "commit": {}, "export_id": {}}},
    "attachments": {"type": "array", "items": {"type": "object", "required": ["name","mime","bytes","sha256"]}},
    "trust": {"enum": ["trusted","untrusted"]},
    "created_at": {"type": "string", "format": "date-time"},
    "payload": {}
  },
  "allOf": [
    {"if": {"properties": {"type": {"const": "APPROVAL_REQUEST"}}},
     "then": {"properties": {"author": {"properties": {"kind": {"const": "system"}}},
                             "payload": {"required": ["approval_id","kind","action_hash","expires_at"]}}}},
    {"if": {"properties": {"type": {"const": "APPROVAL"}}},
     "then": {"properties": {"author": {"properties": {"kind": {"const": "system"}}},
                             "payload": {"required": ["approval_id","decision","decided_by","action_hash"]}}}},
    {"if": {"properties": {"type": {"const": "DECISION"}}},
     "then": {"properties": {"payload": {"required": ["title","decision","status"]}}}}
  ]
}
```

(Per-type payload schemas follow the table above; ULID format for ids is a **design** choice.) Body limit 32 KiB and attachment storage on disk (hash recorded, content never inlined in prompts) are **design** defaults.

## 3. Mentions, threads, markers

**Mentions.** Syntax `@handle` (agent instance or human), `@role:Reviewer` (resolved to the room's member with that role), `@Tests` (tool member), `#task-<id>`, `#room-name`, `^msg_<id>` (message reference). Resolution happens server-side at insert and the resolved targets are stored in `mentions_json`; an unresolved mention produces a `SYSTEM` hint and no wake. A mention is a delivery rule, not an instruction channel: it makes the target eligible for a turn according to the room mode (s6); it never grants a permission. Mentioning an agent who is not a member produces a prompt to add the agent, which is a human action (s10). Agent-to-agent mentions are allowed only where the room mode allows agent turns and count toward the debate round counter (s6).

**Threads.** `thread_root_id` points at the first message of a thread; replies set both `thread_root_id` and `reply_to_id`. Threads are one level deep (design). Task rooms use the root `TASK` message as the thread anchor for the task's work. The right-hand thread panel shows one thread. An agent working a thread receives that thread, not the channel (s11).

**Markers.** A line in a `CHAT`/`STATUS`/`RESULT` body that starts with `[DECISION]`, `[TODO]`, `[STATUS]` or `[RESULT]` is parsed by a deterministic parser (no LLM) into `markers_json`. Effects are proposals, never silent state changes: `[DECISION]` creates a `proposed` decision candidate linked to the message; `[TODO]` creates a task-checklist entry on the room's task (or a backlog proposal in a channel) that a human or a stored rule must confirm to become a `TASK`; `[STATUS]` and `[RESULT]` are display hints that fill the corresponding card fields. Markers inside quoted or untrusted blocks are ignored.

## 4. Presence

Brief §11 presence states: Working, Thinking, Idle, Waiting, Completed, Needs Attention, Failed, Paused. Presence is derived from agent state (brief §12) and run telemetry, never stored as chat.

| Presence | Derivation |
|---|---|
| Working | agent `WORKING` with a tool call in flight or output streaming |
| Thinking | agent `WORKING`, last event is an agent thought chunk (ACP `agent_thought_chunk`) or a model request with no output yet |
| Idle | agent `IDLE`; also `STARTING` (sub-label "starting") |
| Waiting | agent `WAITING`: awaiting a turn grant, a human response, an approval, or a lease |
| Completed | agent `COMPLETED` (task finished, awaiting nothing) |
| Needs Attention | agent `NEEDS_ATTENTION` (watchdog stage 2, repeated errors, adapter drift on its provider) |
| Failed | agent `FAILED` |
| Paused | agent `PAUSED`; also budget-paused scope |

Gap: the brief's agent states include `STOPPED` and `STARTING`, which have no presence state of their own. **Design**: `STARTING` shows as Idle with a label; `STOPPED` shows as Paused with the label "stopped". Humans have presence `online/away` only (design). Tool members have no presence, only a last-result badge. Presence changes are coalesced (at most one `room.presence_changed` per member per second, design) so a busy agent does not flood the bus.

## 5. Posting rights and the approval path

Agents post through the Workspace MCP server's `room` tool (brief §14) or, for turn results, through the Agent Runtime converting the finished turn into a message. The `room` tool enforces: agent may post only `CHAT, STATUS, RESULT, QUESTION, HANDOFF, ERROR`, and may post `DECISION`/`TASK` only as `proposed`; rate cap per turn (default 3 messages, **design**) and body size cap; secret scan before insert (security.md s8; findings redact or block); untrusted marking. `APPROVAL_REQUEST` is created by the approvals service when a workflow request port or an agent permission request needs a human (brief §4); the UI renders the **canonical action** from the descriptor (security.md s5), the human clicks, the UI service layer calls `Decide`, and the approvals service writes the `APPROVAL` message. No agent, tool, MCP server, GitHub or integration text can reach this path.

## 6. Turn-taking and room modes

Agents act only when granted a turn (token-control rules 1 to 3). Modes are per room (`rooms.mode`):

| Mode | Who speaks next | Cost behavior |
|---|---|---|
| `auto` | Deterministic rules choose eligible members: explicit mentions first, then the assignee of the thread/task, then a role match; at most one agent turn per trigger unless the rule set allows parallel independent turns; no LLM chooses the speaker (token-control rule 15) | Default for task rooms |
| `sequential` | Members take turns in a fixed order, one round at a time; each member speaks once per round and may pass | Bounded debate |
| `moderator` | A moderator (a human, or a designated agent role such as PM or Architect) names the next speaker; if the moderator is an agent its decisions are ordinary agent turns and are billed | For structured design discussions |

```go
type TurnScheduler interface {
    OnMessage(ctx context.Context, m Message) ([]TurnGrant, error) // may return none
    Complete(ctx context.Context, g TurnGrant, res TurnResult) error
}
type TurnGrant struct{ ID, RoomID, ThreadID string; Member MemberRef; Round int; MaxRounds int; ContextPlan ContextPlan; Expires time.Time }
```

Rules: (1) The Agent Runtime refuses to start a turn without a `TurnGrant`. (2) Debate is bounded: per thread or topic a round counter increments each time a different agent responds to another agent; the default bound is 3 (brief §7 "about 3 rounds"); at the bound the scheduler posts a `SYSTEM` message and either creates a `DECISION` candidate from the converged position or raises a human `QUESTION`; further agent turns on that topic need a human message. (3) A human message always preempts: it resets the round counter and is eligible for immediate grants. (4) Grants carry a `ContextPlan` (s11) so the Context Engine knows what to include. (5) Team size and who may speak are also limited by the router's `TeamSize` (model-routing s1). (6) Hosts never exceed `max_concurrent_agents` (brief §6), so grants queue; a queued agent shows presence Waiting. (7) Group-collaboration workflow nodes (brief §4) use the same scheduler in `sequential` mode with the node's bound; the node ends when the bound is reached or the group returns `SHIP`/a decision.

Task rooms in the Iterate Until Verified template (brief §22) are not a conversation among agents: the engine drives worker and reviewer turns, and the room shows the resulting `RESULT`/`STATUS` messages, so chat-style mentions do not create extra turns there.

## 7. Tool members

`Tests`, `Lint`, `Types`, `Secrets`, `CI`, `Preview` (brief §11) post check results at zero model cost. They are members with `author.kind="tool"`, produced by deterministic `check` nodes and external check sources, never by an LLM.

| Tool member | Check kind (brief §13a) | Source | Notes |
|---|---|---|---|
| Tests | `test` (and `e2e` results when present) | project test command (human-configured) / Playwright with screenshots | Failure summary uses the normalized failure signature (token-control s5) |
| Lint | `lint` | project command | |
| Types | `typecheck` (and `build`) | project command | |
| Secrets | `secrets` | gitleaks over the worktree (brief §13a) | Reports rule ids and locations, never secret values |
| CI | `ci` | GitHub checks via `gh` (Slice 1E); failing logs are fed back to the worker as untrusted data | |
| Preview | `preview` | preview deployment with the preview-only credential/account (D6) | Posts the URL; production deployment is never a check |

Gap: `build`, `deps` (osv-scanner) and `e2e` are check kinds in the brief with no named tool member. **Design**: `build` is reported by Types, `e2e` by Tests, and `deps` by a seventh member `Deps` (osv-scanner discloses that package metadata goes to osv.dev unless offline, TI s2). Check payload: `{check_kind, status: pass|fail|error|skipped, duration_ms, command_digest, failure_signature?, summary (bounded), log_ref, diff_hash}`. A human can run a tool member from the composer (`/run tests`) which only enqueues the deterministic check; mentioning a tool member in prose does not execute anything.

## 8. Exports

Exports are immutable rows in `exports` (brief §11); regenerating creates a new row. As built (audit 2026-10-16, L18) an export is made from at most 20,000 messages (`Options.MaxExportMessages`; a longer range is refused with the count), and the messages are read, rendered and scanned for secrets outside the write transaction: the database has one writer and the whole read used to hold it. The transaction that follows only checks the receipt again and inserts, so two requests with one receipt make one export.

- **Minutes (Markdown).** Deterministic template from a message range (default: the room or task from creation to now): participants, decisions (with their message ids), TODOs and their status, results and check outcomes, open questions, approvals and their outcomes, links to run/checkpoint ids. No LLM by default. An optional summary section can be produced through `LLMClient` on a `private` provider; it is labeled generated and cached.
- **ADR candidates.** Every `DECISION` message, plus accepted `[DECISION]` markers, yields a candidate: `{title, status, context, decision, consequences, alternatives, source_message_ids}`. A human accepts, edits or rejects. Acceptance creates a document proposal, and writing it into the repository (for example `docs/adr/NNNN-title.md`) is a normal task in a worktree with human review; the room never commits on its own. Candidate format is **design** (a MADR-like layout).
- **PR description draft.** Assembled from the task, its `RESULT` messages, check results, diff stat and accepted decisions. It passes the secret scan, and the text sent to GitHub through `gh` is an outbound write that needs approval by default (brief §11, security.md s5). GitHub review-comment text returning into the room is untrusted.

Each export stores `sha256` of its content and the covered `seq` range so it can be verified against the immutable messages.

## 9. Unread and the "Needs you" inbox

**Unread (counts stop at 1,000, audit 2026-10-16 L18: `room.UnreadCap`; a count of 1,000 means 1,000 or more).** Per user and room: `room_reads.last_read_seq`; unread count = messages with `seq > last_read_seq` that the user may see; a separate mention cursor drives the badge. The cursor advances when the user views a room position (client report, batched), and updates are idempotent. Muted rooms count unread but do not notify.

**Needs you.** A derived list, not a table of messages: a query over source-of-truth tables, so it cannot drift from reality and items vanish when the underlying state changes.

| Item | Source | Resolves when |
|---|---|---|
| Approval waiting | `approvals` in `REQUESTED` | decided, expired, cancelled |
| Blocking question to a human | `QUESTION` with `blocking` and `request_id` | answered |
| Review requested (spec gate, diff review, `SHIP` review) | pending request of kind `review` | human response |
| Task `WAITING_HUMAN` | `tasks.state` | state changes |
| Agent `NEEDS_ATTENTION` or `FAILED` | `agents.state` | state changes |
| Budget paused | `budget.paused` scope state | resumed or limit raised |
| Provider consent or Connection problem (`unauthenticated`, `degraded`, drift) | Connections status | fixed |
| Mentions of me not yet read | `messages` with mention cursor | read |

Ordering: severity (approvals and failures first), then age. The inbox is per user; in v1 there is one local user. Desktop notifications use the Wails notifications service (listed among built-in services, GP s1); behavior on Windows is unverified.

**As built (`room.NeedsYou`, `internal/room/inbox.go`).** The items are: open rows of the `approvals` table (approvals, reviews and questions; a budget pause, a watchdog or Team Lead question and a GitHub problem in the PR flow arrive this way), blocking `QUESTION` messages to a human until a human replies in that thread, the latest failing check per room and check kind, and stage-2 watchdog pauses. A human can dismiss a blocking question, a failed check or a watchdog pause item (`room_inbox_acks`). Not built: items for task `WAITING_HUMAN` as such, agent `NEEDS_ATTENTION`/`FAILED`, Connection problems and unread mentions (mentions are counted per room in the sidebar instead), muting a room from the UI, and desktop notifications. The Tasks page lists every task with an item here under "Waiting on you".

## 10. How agents join channels

Agents never join on their own. Membership rows are written by (a) a human action (add to channel, group or DM), (b) task creation, which adds the assignee, the reviewer and the tool members to the task room, or (c) a stored rule a human created (for example "Security agent joins every `task` room touching `auth/`"). An agent can ask to join through a `QUESTION` ("request to join #architecture") that becomes a `Needs you` item; granting it is a human action. Member states: `active` (eligible for turns), `observer` (read access, no turns unless mentioned), `muted`. Removing an agent revokes the Workspace `room` tool scope for that room immediately. The Workspace MCP `room` tool checks membership on every call (read and write); a non-member agent cannot read a room even if it knows the id.

**As built.** (a) A human adds or removes agents with `room.invite` and `room.removeMember` (the room's agents bar) and opens DMs; (b) task creation adds the owner, assignee, reviewer and every tool member to the task room; (c) stored join rules and the "request to join" question are not built. The member states `active`, `observer` and `muted` exist in the core. The `room` tool (actions `read` and `post`) reaches only channels and task rooms of the run's project, never DMs or groups, and refuses a room the agent is not a member of (`internal/core/mcp_providers.go`).

## 11. What agents receive (never the whole workspace)

A turn's room context is selected by the Context Engine from the `TurnGrant.ContextPlan`; the sources, in priority order, and their bounds:

1. The triggering message and its thread (last K messages of the thread, default K=12, **design**; older thread parts come as a `room_digest`).
2. The task statement and acceptance criteria, `DECISION` messages accepted for this task, and any `HANDOFF` addressed to this agent.
3. Messages that mention the agent or its role since its last turn (bounded by count and tokens).
4. A rolling digest of older history in this room (brief §9), produced incrementally by a cheap `private` model through `LLMClient` or by deterministic extraction of markers and decisions when no model is available.
5. Retrieval on demand through the Workspace `room` tool: `read_thread(thread_id, range)`, `read_messages(from_seq, to_seq)`, `search_room(query)` over FTS5, `list_decisions()`; calls are bounded, secret-scanned, and logged by the Gateway (brief §14).

Excluded by construction: other rooms the agent is not a member of; the channel's full history; other agents' raw transcripts and private DMs; workspace-wide memory beyond `search_memory` hits; anything older than the digest unless fetched. Agent-authored messages from others arrive wrapped as untrusted data with attribution; human messages are the only instructions besides the role and project instructions. The manifest lists every included message by id with the reason (token-control s2), so a human can see exactly what an agent was given.

## 12. Event mapping

Events are persisted with the global monotonic `seq` and fanned out; the UI reconnects with `Replay(afterSeq)` (brief §10). Room events carry ids, counts and headers; bodies are fetched from `messages` by id so private content is not duplicated into the event log, and OpenTelemetry export never includes message bodies (brief §10).

| Event | Emitted when | Payload (ids only) | UI effect |
|---|---|---|---|
| `room.created`, `room.archived` | room lifecycle | room id, kind | sidebar |
| `room.member_added`, `room.member_removed`, `room.member_state_changed` | s10 | room id, member | member list |
| `room.mode_changed` | mode edit by human | room id, old, new | mode chip, `SYSTEM` message |
| `room.message_posted` | any message insert | room id, message id, `seq`, type, author | timeline, unread, inbox |
| `room.presence_changed` | s4 (coalesced) | member, presence | presence dots |
| `room.turn_granted`, `room.turn_completed` | scheduler | grant id, member, round | Waiting/Working, round counter |
| `room.debate_bounded` | round bound reached | thread id, rounds | `SYSTEM` message and `DECISION`/`QUESTION` |
| `room.marker_promoted` | marker created a candidate | message id, kind | candidate list |
| `room.digest_created` | rolling digest | room id, seq range | none (internal) |
| `room.export_created` | export row written | export id, kind | export list |
| `room.read_cursor_updated` | cursor update | user, room, seq | unread badge |

Cross-family mappings: `approval.requested` writes `APPROVAL_REQUEST`; `approval.decided` writes `APPROVAL`; `approval.mismatch` writes `ERROR`; `task.*` state changes write `STATUS`/`SYSTEM` into the task room; `routing.escalated` and `routing.fallback` write `SYSTEM` (the reason string from model-routing `Explain`); `watchdog.trip` writes `SYSTEM` for stage 1 and `ERROR` with a Needs-you item for stage 2; `budget.warn`/`budget.paused` write `SYSTEM`; `agent.*` transitions drive presence only (no message); check completion writes the tool member `RESULT`; `git.*` and `github.*` events write `STATUS`; `workflow.*` transitions write `STATUS` into the task room at superstep boundaries only (not per node chunk).

## 13. Concurrency and consistency

Rooms carry `version` for compare-and-swap updates (mode, name, membership batches); messages are append-only. Duplicate delivery is harmless: message inserts accept an optional client-supplied `idempotency_key` (receipts idempotent by `(scope, receipt_id)`, brief §18), which also protects agent posts that are retried after a crash. On restart the UI catches up from the event log (brief §24, Slice 1A).

**As built.** Mode changes and group conversion are compare-and-swap on `version` (the `room.setMode` call reads the current version itself). The `IdempotencyKey` on a post is used by the core's own posts (the debate bound and its question); a human post from the UI (`room.post`) carries none. Presence is not recomputed from agent state: the presence tracker rebuilds it at start by replaying the `agent.*` and `watchdog.*` events from the log, then follows live events (`internal/room/presence.go`).

## 14. Tests

Unit: marker parser golden corpus (including quoted and untrusted blocks); mention resolution; schema validation, with `APPROVAL`/`APPROVAL_REQUEST` insert rejection for non-system authors; scheduler mode tables and round bounds (a labeled participant stub only for the always-disagree failure boundary); presence derivation table; unread cursor idempotency; `needs_you` query resolves when state changes; export immutability and hash verification; membership revocation blocks the `room` tool. Integration (real CLI where available, otherwise skipped with a reason, P3): an agent receives only its thread context (manifest assertion); streaming output does not create messages; app restart catches up from `Replay`; a room with three real agents under `sequential` mode stops at 3 rounds.

## 15. As built: agents in the room (`internal/agents/responder`, wired by `internal/core/ext_agents.go`)

Until Slice 4 the room had turn-taking rules (`PlanTurns`) and context selection (`ContextFor`) but nothing that ran an agent. The responder is that piece. It subscribes to `room.message_posted` and acts only on a message whose author is a `human:` identity; tool, system and agent messages never start a conversation.

**One reply.** For each speaker `PlanTurns` returns, the responder inserts a `room_turns` row (migration 00070; `UNIQUE(trigger_id, agent_id)` makes a turn happen at most once per triggering message, even if an event is delivered twice), builds a bounded context with `ContextFor` (the trigger, its thread, the task statement and accepted decisions, handoffs, mentions since the agent's last turn; nothing else from the room, nothing from other rooms), saves a manifest (`context_manifests`, node `room_reply`, listing every message included or excluded and why), waits for a slot (global cap, then the project/provider limiter; a queued agent shows Waiting), and runs one fresh agent session through the Agent Runtime as a read-only session (`ReadOnly`: Claude Code in its default mode with Edit, Write, MultiEdit, NotebookEdit, Bash and PowerShell denied, and no tool-wide Read allow rule, so it reads inside its working folder only; plan mode is not used because Claude Code answers a plan-mode haiku session with Sonnet; other CLIs get their plan mode), the task worktree (task room, when it exists) or the project root as cwd, a 5 minute timeout and a 40 tool-call cap. The final text is posted as an untrusted `CHAT` authored by the agent, as a thread reply to the message it answers (`ReplyToID`), after the room's secret redaction, with a payload carrying the turn id, model, context size, input/output tokens and cost; `model_usage` rows come from the runtime as for any turn (tier `premium`, provider label of the agent), the cost is added to the task in a task room, and presence follows the runtime's `agent.*` events (Thinking, Completed). A failed, timed-out or empty turn posts one system `ERROR`. The agent's reply can never answer an approval: the responder has no call into the workflow engine or the approvals service, the room refuses `APPROVAL` types from agents, and a test has an agent say "APPROVED" while a real approval waits (it stays `REQUESTED`).

**Chains and bounds.** After a reply the responder asks `PlanTurns` about the reply, so mentions, the sequential order and the moderator's naming continue a conversation, and it stops when the room says `Bounded` (the debate bound: 3 agent-to-agent rounds in `auto`/`moderator`, 3 full passes in `sequential`), calling `ApplyDebateBound` (a `SYSTEM` message and either the proposed `DECISION` or a blocking human `QUESTION`). Further guards: a hard cap of 12 agent turns per human message; one chain per room at a time in arrival order; a newer human message stops a running chain before its next turn (rule 3, preemption); a `PASS` reply in a sequential room is recorded as a pass (payload `pass`) so the round counter sees it; turns left `running` by a crash are marked `interrupted` at startup and never re-run (a re-run would be a new, billed turn nobody asked for).

**Modes.** `auto` is the default. `sequential` runs the fixed order (`room.setMode` with `order`). `moderator` is implemented as: a human message with no mention wakes the moderator agent, whose prompt tells it to name at most `maxParallel` speakers with `@handle` and to state a `[DECISION]` when converged; the members it names speak (reason `named_by_moderator`), and each member's reply wakes the moderator again until the bound. A human moderator names speakers by mentioning them. `room.setMode {roomId, mode, order, moderator, maxRounds, maxParallel}` validates that the order and the moderator are members, `room.invite` and `room.removeMember` add and remove agents (human action), `room.turns` lists the turns, `room.reply {messageId}` answers a human message on demand.

**Deviations from section 6 and 11.** (1) In an `auto` room an agent's message wakes only members it explicitly mentions; the owner and role fallbacks are used for human messages. Letting the fallbacks chain agent to agent would spend turns nobody asked for. (2) Replies are threaded under the human message, so the second speaker in a debate sees the question and the first reply (the room's context selection is per thread). (3) A DM adds the last 6 messages before the trigger to the turn (a DM is one conversation and thread selection alone would give the agent amnesia between questions); the manifest lists them as `dm_recent`. (4) In a DM the agent always answers; in a channel nobody is woken without a mention, the task's owner or a role hint.

**Tests.** Scripted stand-in agent (labeled) for the mechanics: exactly-once turns, thread placement, bounded context (an unrelated message never reaches the prompt), usage and presence, the event loop (non-human messages wake nobody), sequential bound at 6 turns for 2 agents x 3 passes, pass handling, auto ping-pong bound, moderator order, DM window, secret redaction, failure/empty/tool-cap/gate paths, hard cap, disabled project, interrupted turns, preemption. One real Claude Code (haiku, read-only mode, `TestRealClaudeAnswersInTheRoomReadOnly`) reads a file in the project and answers, cannot quote a file outside the project, is served by haiku, records usage and leaves the project unchanged. Budgets also gate replies: a reply does not start while a budget that applies (project, agent, or the task's in a task room) is exceeded, and the refusal is posted. Not tested: three real agents in a sequential room (the stand-in covers the mechanics with two and three), and the UI.

## Open questions / unverified

- Message ids as ULIDs, 32 KiB body cap, 3 posts per turn, thread depth of one, K=12 thread window, presence coalescing at 1 second, and the minutes/ADR/PR templates are **design** values with no evidence behind them; they need owner review and tuning.
- Presence gap: brief §11 has no state for agent `STARTING` or `STOPPED`; proposed mapping above needs a decision.
- Tool-member gap: `build`, `deps` and `e2e` have no member in the brief; proposed mapping above (adds `Deps`) needs a decision.
- Wails event throughput under streaming load and Windows notification behavior are unverified (phase0 s7 item 3).
- Whether every supported CLI can use the Workspace `room` tool through its MCP configuration in practice is unverified: MCP support is documented per CLI (AC s1 to s5) but no live run exists; the fallback is the Runtime posting the finished turn.
- Rolling digests use a model call; the quality of cheap-model digests is unmeasured, and the deterministic fallback loses prose context.
- Multi-user rooms (several humans) are not designed: v1 assumes one local user; read cursors and inbox are keyed by user for future use.
- Interaction with Iterate Until Verified (engine-driven turns instead of chat turns) should be confirmed against `workflow-engine.md` once written.
