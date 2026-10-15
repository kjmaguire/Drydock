# Drydock: Chats

Scope: the Chats section (decisions P29). The human talks to an agent CLI directly (Claude Code by default; any adapter works),
every chat is kept, and an earlier chat can be read, searched, continued in the CLI's own session, forked, attached to a
message, or looked up by a later chat through the `chats` workspace tool. Code: `internal/chats` (service), 
`internal/core/ext_chats.go` (wiring and methods), migration `internal/storage/migrations/00106_chats.sql`. Tests:
`internal/core/chats_test.go` (scripted real-process agents), `internal/core/real_claude_chats_test.go` (the real Claude Code,
skipped without a logged-in `claude`: resume, fallback, a refusal and its allow rule), `internal/chats/text_test.go`,
`internal/mcp/workspace_chats_test.go`, `internal/core/chats_wsl_test.go` (a chat inside a WSL distro, through the stand-in
wsl.exe and the LABELLED stand-in claude of `internal/wsl/wsltest`), `internal/core/chat_properties_test.go` (a chat's skills
and MCP servers reaching its session, decision P60). Not verified: a real ACP chat turn, anything on Windows (a chat in a real
distro is covered by the agent run mode's real-distro tests only as far as the shared start goes), a real Claude Code reading a
chat's skill through the skills tool (section 6).

Home (`/home`) starts with a chat (decision P60): type a message and Start chat makes a chat without a project in its private
folder with Claude Code, sends the message and opens the chat; Options opens the new chat dialog without a project (folder,
agent CLI and where it runs, model, skills, MCP servers). The five latest chats without a project are listed under it.

## 1. Objects

`Chat`:

```json
{
  "id": "chat_3f9a1c2b7d40", "projectId": "prj_… or empty (no project)", "title": "", "adapter": "claude",
  "wslDistro": "empty (on Windows) or the WSL distro the CLI runs in, e.g. Ubuntu",
  "model": "", "effort": "", "permissionMode": "default", "skipsApprovals": false,
  "cwd": "C:\\repo", "sessionId": "the CLI's session id, empty before the first turn",
  "allowedTools": ["Bash(npm test)", "Edit"], "readHistory": true,
  "skills": [{"registryId": "cloudflare-skills", "name": "wrangler"}],
  "mcpServers": [{"serverId": "tst", "tools": ["echo", "envlen"]}],
  "status": "idle | running | error", "lastError": "",
  "pinned": false, "archived": false, "archivedAt": 0, "forkedFrom": "",
  "createdAt": 1791500000000, "updatedAt": 1791500000000,
  "tokensIn": 10, "tokensOut": 5, "costUsd": 0.0002, "costUnknownTurns": 0, "version": 7
}
```

`costUsd` is `null` when no turn reported a cost (unknown, never zero); `costUnknownTurns` counts turns that reported none, so
a non-zero value means `costUsd` covers only the other turns. `skipsApprovals` is true for `bypassPermissions`, `auto` and
`dontAsk`: the UI must show it. Times are Unix milliseconds. `version` goes up by one with every write to the chat's row: two
snapshots of a chat (a method's result, a `chat.updated` event, `chat.get`) are ordered by it, never by `updatedAt` (the wall
clock can step back, and a write can be stamped before an earlier one commits). `chat.update`, `chat.allowTool` and
`chat.disallowTool` read, change and write the row in one transaction and return the committed row. `skills` are workspace
library skills chosen for the chat and `mcpServers` workspace library MCP servers granted to it with the tools each may call
(both empty lists when none; section 6).

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
| `chat.create` | `{projectId?, cwd?, title?, adapter?["claude"], wslDistro?[""], model?, effort?, permissionMode?, readHistory?[true], skills?[[]], mcpServers?[[]]}` | `Chat` |
| `chat.get` | `{id}` | `{chat: Chat, messages: [Message] (latest 200, oldest first), hasMore}` |
| `chat.messages` | `{id, beforeSeq?[latest], limit?[100, max 500]}` | `{messages: [Message] oldest first, hasMore}` (`hasMore`: still older messages exist) |
| `chat.send` | `{id, text, attach?: [chatId]}` | `{turnId, version}`; the turn runs in the background. `version` is the chat's version once the turn was claimed: a snapshot at that version or later shows the turn (running or over) |
| `chat.stop` | `{id}` | `{stopped}` (false when nothing was running) |
| `chat.update` | `{id, title?, pinned?, archived?, model?, effort?, permissionMode?, readHistory?, adapter?, wslDistro?, cwd?, skills?, mcpServers?}` | `Chat` |
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
- `chat.create` with `wslDistro` runs the chat's CLI inside that WSL distro, its own install with its own sign-in (decision
  P41; the Connection `claude@wsl:<distro>`). Refused, naming the distro: an adapter that cannot run in WSL (only Claude Code
  can), a distro this machine does not have (or a container engine's own), Claude Code not found there, Claude Code signed out
  there (the CLI's own `auth status`, from the probe cache; a cached "signed out" is probed again before it refuses, so a
  fresh sign-in counts at once), or a project whose sandbox is on. Folders: a project's chat runs in the project's folder as
  usual (the CLI gets its Linux form: `/mnt/<drive>/...`, or the distro's own path for a repository inside it). A chat without a
  project and without `cwd` gets a private folder in the distro user's home, `~/.drydock/chats/<chat id>`, made by the
  distro's user through wsl.exe and stored the Windows way (`\\wsl.localhost\<distro>\home\<user>\.drydock\chats\<chat id>`).
  `cwd` may be a folder inside the distro (its Linux path, or `\\wsl.localhost\<distro>\...`; checked inside the distro, a
  folder of another distro is refused) or a Windows folder (checked on Windows, reached through the distro's mount root).
  `wslDistro` is fixed for the chat's life; a fork keeps it.
- `chat.send` is refused while a turn runs ("a turn is already running in this chat: wait for it to finish or stop it"), for an
  archived chat, an empty message (limit 100 KiB) or a folder that is gone. A chat in WSL is checked again first: its distro
  gone or its CLI signed out there refuses the send with a message naming the distro, before the chat is claimed; its private
  folder is made again when it was removed. `attach` takes up to 5 other chats (not the chat
  itself); their transcripts are given to this turn only. The stored user message is only what the human typed.
- `chat.update` with `archived: true` is refused while a turn runs; `chat.delete` likewise. Model, effort, permission mode,
  allow rules, skills and MCP servers apply from the next turn, which continues the session. `adapter`, `wslDistro` and `cwd`
  change where the chat runs (decision P60): refused while a turn runs, checked as on create (`cwd` `""` is the chat's private
  folder; a project chat takes none; a new distro moves a chat in its private folder to the private folder of the other side),
  and the CLI session is dropped, so the next turn starts a new session given the conversation so far (`fallback: true`). A new
  adapter keeps model, effort and permission mode only when it takes them, and checks the chat's skills and servers again. Each
  change of where the chat runs, of its skills and of its servers is recorded as a `system` message.
- `skills` (at most 20): each `{registryId, name}` must be in the workspace skill library, available (not blocked) and still in
  its registry; two chosen skills may not share a name. `mcpServers` (at most 10): each `serverId` must be connected in the
  workspace MCP library; a grant without `tools` gets the server's tools as known now, by name, without the flagged ones
  (listing them first when they were never listed), as an agent's grant does; named tools are kept as given. Both are refused
  for an agent CLI that cannot be given Drydock's MCP gateway (Claude Code can; an ACP CLI only after `mcp.try.set`).
- `chat.fork` keeps adapter, folder, model, effort, allow rules, skills and MCP servers; a mode that skips approvals falls back
  to the default.
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
| `chat.updated` | `{chatId, chat}` (settings, rules, title, the start and the end of a turn) |
| `chat.deleted` | `{chatId}` |
| `chat.turn_started` | `{chatId, turnId, attach: [chatId]}` |
| `chat.delta` | `{chatId, turnId, text}`: streamed assistant text, coalesced (about 60 ms, cut at whitespace) |
| `chat.tool` | `{chatId, turnId, name, summary, messageId}` |
| `chat.message` | `{chatId, message: Message}` for every stored message |
| `chat.turn` | `{chatId, turnId, status: "done" \| "error" \| "stopped", usage: {inputTokens, outputTokens, costUsd (null = unknown)}, sessionId, resumed, fallback, error?}` |

The runtime's own `agent.started`, `agent.mcp`, `agent.output`, `agent.completed` and `agent.stopped` events are published as
well, with the chat id as `agentId`.

## 4. A turn

1. The chat is claimed (`running`), the user message is stored (secret-scanned), and the running chat is published
   (`chat.updated`, then `chat.turn_started`).
2. The adapter starts through the Agent Runtime in the chat's folder, with the chat's model, effort, mode and allow rules, and
   `ResumeID` = the stored session id when the adapter can resume (Claude `--resume`, ACP `session/load` or `session/resume`).
   Claude Code starts as for every Drydock session (decision P36): without the person's skills and slash commands, so a message
   such as `/compact` is answered "isn't available" (automatic compaction still works), with the quiet environment and the
   tools no Drydock session uses denied. The person's full Claude Code setup is in the embedded terminal. A chat with a
   `wslDistro` sets the run mode in its spec (`RunMode: wsl`), so the adapter's own WSL start runs it, exactly as an agent's:
   wsl.exe with the folder and every path converted to the distro's form, the distro's own HOME and PATH, only the allowlisted
   environment passed through `WSLENV`, the MCP gateway (the `chats` tool) through the Windows shim, and the run marker, so
   stopping the turn kills every Linux process it started. Drydock never reads, copies or moves the distro's Claude sign-in
   (D1): the only sign-in check is the CLI's own `auth status`.
3. If the session cannot be resumed (the CLI fails before any session or output), a system note is stored and a fresh session
   gets the last ~12,000 characters of the chat, quoted line by line as "the earlier conversation in this chat", followed by the
   new message. A fork's first turn and every turn of an adapter that cannot resume start the same way (`fallback: true`).
4. Output is stored as it arrives: assistant text blocks, tool calls, refusals, warnings. Usage adds up; the CLI's session id is
   recorded; the process is stopped. With no title yet, the first line of the first message (60 characters) becomes the title.
   Claude Code reports a resumed session's cost and tokens including every earlier process of that session, so the chat keeps
   the session's running totals (`chats.cli_usage`) and the next turn starts its parser from them: each turn is counted once
   (contract CLAUDE-USAGE-03; a new or fallback session starts from zero). An ACP agent's `usage_update.cost` is the session's
   cumulative cost too (OpenCode), so the ACP adapter keeps the same totals and starts a loaded or resumed session from them
   (contract OPENCODE-USAGE-01, decision P53 a).
5. `chat.stop` cancels the turn: streamed text so far is kept as an assistant message and "Stopped by you." is added; the session
   (when it started) is kept, so the next message continues it. The CLI is killed before it saves the partial answer to its own
   session (observed with claude 2.1.296), so when the next turn resumes that session Drydock puts a note in front of the new
   message: the text the person saw of the stopped answer (its last 4,000 characters, quoted line by line), or that the answer
   was stopped before any text (`stoppedNote` in `internal/chats/turn.go`). A turn has 30 minutes.

## 5. The `chats` workspace tool

A chat session whose `readHistory` is on gets the `chats` tool (Claude: `mcp__drydock__chats`) and the gateway's `fetch_range`
(continues a truncated result; `mcp__drydock__fetch_range`), both callable without a prompt, and nothing else of the workspace
or the library unless the person chose it for the chat (section 6; `planChatMCP` in `internal/core/ext_chats.go`). Arguments: `{action: "list" | "search" | "read", query?, k?[10], chat_id?, from_seq?[0],
limit?}`. It reads the chats of the chat's own project and the chats without a project (a chat without a project reads every
chat). `list` leaves archived chats out (at most 100, default 30); `search` includes them, marked `archived` (at most 20 hits,
default 10); `read` reads any readable chat by id, archived or not (at most 200 messages, default 50, each cut at 4,000
characters). Results go through the gateway's usual pipeline (secret scan, untrusted marking, audit).
For ACP agents the tool is attached only when the human allowed the gateway for that CLI (`mcp.try.set`, decisions P20).

## 6. A chat's skills and MCP servers (decision P60)

Chosen in the new chat dialog (also from Home's Options) or the open chat's Properties dialog, stored with the chat
(`chats.skills`, `chats.mcp_servers`, migration `00122_chat_properties.sql`), and given to every turn's session from the next
message on. The session otherwise starts exactly as every Drydock Claude Code session (decision P36: the quiet environment,
`--disable-slash-commands`, the unattended deny list, `--setting-sources user`).

- **Skills** reach the CLI the on-demand way of decision P37: every turn's system prompt carries an index (`skills.ChatIndex`:
  "Skills chosen for this chat", one line per skill with its description and globs, and how to read one), and the skill's text
  comes from the gateway's `skills` tool (`mcp__drydock__skills`, action `get`; `list` and `for_paths` too), which for a chat
  session (agent id `chat_*`, role `chat`) serves only the library skills chosen for that chat, bodies cut at 20,000
  characters (`chatSkillsTool` in `internal/core/ext_chats.go`). Nothing is written into the chat's folder, which may be the
  person's own folder or repository. A skill blocked or gone since it was chosen is left out of the index and the tool. Skill
  use is not counted in `skill_uses` (that table is per project, decision P49).
- **MCP servers** are granted through the gateway the way an agent's grants are (`mcpServersForChat`, sharing `specsFor` with
  `mcpServersForRun`): the library server's spec (credentials only by vault reference), the tools named in the chat's grant,
  results shaped, secret-scanned and marked untrusted, the guard on, and every tool the person did not declare read-only behind
  an approval for that exact call. A chat's pending approvals are shown in the chat itself, above the composer, with Approve
  and Reject (`ChatApprovals`: `mcpserver.approval.list` filtered by the chat's id; the approval carries the chat's project, so
  a project chat's calls also appear in that project's Needs you). A server disconnected from the library is left out.
- The session's gateway entry lists `chats` (with `readHistory`), `skills` (with skills), `fetch_range` and the granted
  servers' tools, and nothing else; with none of them the session has no MCP entry at all.
