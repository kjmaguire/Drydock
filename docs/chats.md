# Drydock: Chats

Scope: the Chats section (decisions P29). The human talks to an agent CLI directly (Claude Code by default; any adapter works),
every chat is kept, and an earlier chat can be read, searched, continued in the CLI's own session, forked, attached to a
message, or looked up by a later chat through the `chats` workspace tool. Code: `internal/chats` (service), 
`internal/core/ext_chats.go` (wiring and methods), migration `internal/storage/migrations/00106_chats.sql`. Tests:
`internal/core/chats_test.go` (scripted real-process agents), `internal/core/real_claude_chats_test.go` (the real Claude Code,
skipped without a logged-in `claude`: resume, fallback, a refusal and its allow rule), `internal/chats/text_test.go`,
`internal/mcp/workspace_chats_test.go`. Not verified: a real ACP chat turn, anything on Windows.

## 1. Objects

`Chat`:

```json
{
  "id": "chat_3f9a1c2b7d40", "projectId": "prj_… or empty (no project)", "title": "", "adapter": "claude",
  "model": "", "effort": "", "permissionMode": "default", "skipsApprovals": false,
  "cwd": "C:\\repo", "sessionId": "the CLI's session id, empty before the first turn",
  "allowedTools": ["Bash(npm test)", "Edit"], "readHistory": true,
  "status": "idle | running | error", "lastError": "",
  "pinned": false, "archived": false, "archivedAt": 0, "forkedFrom": "",
  "createdAt": 1791500000000, "updatedAt": 1791500000000,
  "tokensIn": 10, "tokensOut": 5, "costUsd": 0.0002, "costUnknownTurns": 0
}
```

`costUsd` is `null` when no turn reported a cost (unknown, never zero); `costUnknownTurns` counts turns that reported none, so
a non-zero value means `costUsd` covers only the other turns. `skipsApprovals` is true for `bypassPermissions`, `auto` and
`dontAsk`: the UI must show it. Times are Unix milliseconds.

`ListItem` is a `Chat` with two more fields: `"preview"` (the start of the latest user or assistant message, at most 160
characters) and `"messageCount"`.

`Message`:

```json
{
  "id": "cmsg_…", "chatId": "chat_…", "seq": 3, "role": "user | assistant | tool | denied | error | system",
  "body": "text (secret-scanned)", "toolName": "Bash", "toolInput": {"command": "npm test"},
  "turnId": "cturn_…", "createdAt": 1791500000000, "suggestedRule": "Bash(npm test)"
}
```

`toolName`, `toolInput` (JSON, at most 4 KB; a longer one is `{"truncated":true,"preview":"…"}`), `turnId` and
`suggestedRule` are omitted when empty. Roles: `tool` is a tool call (body is a one-line summary such as `Read: src/app.go`);
`denied` is a tool use the CLI refused (Claude) or asked about and Drydock refused (ACP), with the tool and its input;
`suggestedRule` is the rule `chat.allowTool` would need (Claude chats only; empty when no safe exact rule exists, for example
a pipeline); `error` is a turn failure or an agent warning; `system` is a Drydock note ("Stopped by you.", "Continued in a new
session (the previous one could not be resumed)", "Allowed for this chat: Bash(npm test)").

`Hit` (search): `{"chatId", "title", "seq" (0 = the title matched), "role" (omitted for a title), "snippet" (matches in
[brackets]), "archived"}`.

## 2. Methods

Parameters are JSON objects; omitted fields take the default in brackets. Errors are plain sentences.

| Method | Parameters | Result |
|---|---|---|
| `chat.list` | `{projectId?, query?, includeArchived?[false], limit?[500, max 500]}` (`projectId` empty = every chat; `query` = full text over titles and messages) | `[ListItem]`, pinned first, then most recently updated, at most 500 |
| `chat.create` | `{projectId?, cwd?, title?, adapter?["claude"], model?, effort?, permissionMode?, readHistory?[true]}` | `Chat` |
| `chat.get` | `{id}` | `{chat: Chat, messages: [Message] (latest 200, oldest first), hasMore}` |
| `chat.messages` | `{id, beforeSeq?[latest], limit?[100, max 500]}` | `{messages: [Message] oldest first, hasMore}` (`hasMore`: still older messages exist) |
| `chat.send` | `{id, text, attach?: [chatId]}` | `{turnId}`; the turn runs in the background |
| `chat.stop` | `{id}` | `{stopped}` (false when nothing was running) |
| `chat.update` | `{id, title?, pinned?, archived?, model?, effort?, permissionMode?, readHistory?}` | `Chat` |
| `chat.delete` | `{id}` | `{deleted: true}` |
| `chat.search` | `{query, projectId?, limit?[50, max 200]}` | `[Hit]` (archived chats included and marked) |
| `chat.fork` | `{id, uptoSeq?[all]}` | `Chat` (new, messages copied up to and including `uptoSeq`, no session) |
| `chat.allowTool` | `{id, tool}` | `Chat` |
| `chat.disallowTool` | `{id, tool}` | `Chat` |

Rules worth knowing in the UI:

- `chat.create`: the adapter must be known and its Connection gates acknowledged. With `projectId` the chat runs in the project's
  repository root (or its workspace folder when it has none) and `cwd` must be empty. Without a project, `cwd` must be an
  absolute path to an existing folder; with no `cwd` the chat gets a private folder under the data directory. Model and effort
  follow the agent rules (single word, effort from the adapter's list). `permissionMode` defaults to `default` for Claude (it
  then refuses tools outside the mode, and each refusal becomes a `denied` message) and to the agent's own default for ACP
  agents; it is checked against the adapter's modes.
- `chat.send` is refused while a turn runs ("a turn is already running in this chat: wait for it to finish or stop it"), for an
  archived chat, an empty message (limit 100 KiB) or a folder that is gone. `attach` takes up to 5 other chats (not the chat
  itself); their transcripts are given to this turn only. The stored user message is only what the human typed.
- `chat.update` with `archived: true` is refused while a turn runs; `chat.delete` likewise. Model, effort, permission mode and
  allow rules apply from the next turn.
- `chat.fork` keeps adapter, model, effort and allow rules; a mode that skips approvals falls back to the default.
- `chat.allowTool` takes one Claude `--allowedTools` rule: a tool name (`Edit`, `WebFetch`, `mcp__server__tool`) or a tool with
  a specifier (`Bash(npm test)`, `Bash(npm run test:*)`, `Edit(src/**)`). Refused: commas, newlines, more than 200 characters,
  nested parentheses, a leading `-`, a bare `Bash` or a Bash rule that allows every command, and `; & | $ < >` or backquotes in a
  Bash rule. ACP chats refuse allow rules.
- `chat.create` and `chat.send` are refused while the license is read-only.

## 3. Events

Every event carries `chatId` and the chat's project as `projectId` (empty for a chat without a project).

| Type | Data |
|---|---|
| `chat.created` | `{chatId, chat, forkedFrom?}` |
| `chat.updated` | `{chatId, chat}` (settings, rules, title, the end of a turn) |
| `chat.deleted` | `{chatId}` |
| `chat.turn_started` | `{chatId, turnId, attach: [chatId]}` |
| `chat.delta` | `{chatId, turnId, text}`: streamed assistant text, coalesced (about 60 ms, cut at whitespace) |
| `chat.tool` | `{chatId, turnId, name, summary, messageId}` |
| `chat.message` | `{chatId, message: Message}` for every stored message |
| `chat.turn` | `{chatId, turnId, status: "done" \| "error" \| "stopped", usage: {inputTokens, outputTokens, costUsd (null = unknown)}, sessionId, resumed, fallback, error?}` |

The runtime's own `agent.started`, `agent.mcp`, `agent.output`, `agent.completed` and `agent.stopped` events are published as
well, with the chat id as `agentId`.

## 4. A turn

1. The user message is stored (secret-scanned), the chat is marked `running` and `chat.turn_started` is published.
2. The adapter starts through the Agent Runtime in the chat's folder, with the chat's model, effort, mode and allow rules, and
   `ResumeID` = the stored session id when the adapter can resume (Claude `--resume`, ACP `session/load` or `session/resume`).
3. If the session cannot be resumed (the CLI fails before any session or output), a system note is stored and a fresh session
   gets the last ~12,000 characters of the chat, quoted line by line as "the earlier conversation in this chat", followed by the
   new message. A fork's first turn and every turn of an adapter that cannot resume start the same way (`fallback: true`).
4. Output is stored as it arrives: assistant text blocks, tool calls, refusals, warnings. Usage adds up; the CLI's session id is
   recorded; the process is stopped. With no title yet, the first line of the first message (60 characters) becomes the title.
5. `chat.stop` cancels the turn: streamed text so far is kept as an assistant message and "Stopped by you." is added; the session
   (when it started) is kept, so the next message continues it. A turn has 30 minutes.

## 5. The `chats` workspace tool

A chat session whose `readHistory` is on gets one MCP tool, `chats` (Claude: `mcp__drydock__chats`, callable without a prompt),
and nothing else of the workspace. Arguments: `{action: "list" | "search" | "read", query?, k?[10], chat_id?, from_seq?[0],
limit?}`. It reads the chats of the chat's own project and the chats without a project (a chat without a project reads every
chat); archived chats are not listed. Results go through the gateway's usual pipeline (secret scan, untrusted marking, audit).
For ACP agents the tool is attached only when the human allowed the gateway for that CLI (`mcp.try.set`, decisions P20).
