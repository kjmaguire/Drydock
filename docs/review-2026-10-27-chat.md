# Audit and chat performance review, 2026-10-27

On the owner's words: "Do an audit on the app, and do a full review on performance specifically chat performance."

Reviewed on branch `claude/integrations-agents-github` (PR #8) from commit `461735b`, on the Linux development host (4 cores).
Windows is covered by CI, not by this review. Decision P104 lists the behaviour that changed.

## 1. Chat performance

### How it was measured

A new browser test, `ui/e2e/chat-perf.spec.ts`, runs the real app on the real core. The agent CLI is the harness's scripted
stand-in (a TEST DOUBLE that speaks Claude Code's stream-json; `cmd/drydock-e2e`, `Step.EveryMS` in `internal/agents/fakeagent`).
The test:

1. builds a chat history of about 900 rows: three turns, each with 300 tool calls and an answer that has code in it;
2. opens that chat (a reload, until its newest message shows);
3. streams a long Markdown answer (about 3,000 words, with headings, lists, Go code and tables) at one word every 2 ms, about
   500 words a second, which is a fast model;
4. types the next message into the composer while the answer streams.

It records the main thread's time from Chrome's Performance metrics, long tasks (over 50 ms), how long each key takes to
show (Event Timing), the frames drawn, and the events the page receives. It fails above these budgets:

| Measure | Budget |
| --- | --- |
| Main thread busy while streaming | 35% of the time |
| Longest single task | 200 ms |
| A key typed in the composer to show | 100 ms |
| Opening the chat | 2.5 s |

The figures are written to `ui/test-results/chat-perf-report.json`. Setting `DRYDOCK_PERF_PROFILE=1` also prints the stream's
heaviest functions from a CPU profile.

### What it found

A CPU profile of the stream showed the cause. Every `chat.delta`, about 16 a second, redrew the whole chat view: the header,
the composer, and every message row in the list's window. After each redraw the list measured every row with
`getBoundingClientRect`, which forces the browser to lay the page out. Each row also did two things on every redraw:

- it formatted its time with two new `Intl` formatters;
- each code block in it was coloured again.

### Fixed

| What | Where |
| --- | --- |
| The answer that streams in is its own component. It redraws on the feed's new `stream` event, at most once a frame, and the list only keeps to the bottom (`VirtualList.follow`, which measures nothing). The conversation, header and composer are drawn only when something else changes. | `ui/app/screens/chats/chat-view.tsx` (`ChatStream`), `chat-feed.ts`, `ui/app/ui/virtual-list.tsx` |
| A row's time is formatted once for its `createdAt`, with one shared formatter. | `chat-message.tsx` |
| A code block's colouring is worked out once for its code. | `ui/app/ui/markdown.tsx` (`CodeBlock`) |
| `chat.delta` and `chat.thinking` events were kept for ever, a second copy of every answer in the event log. They are now deleted after 30 days with `agent.output`; the chat's stored messages keep the text. | `internal/core/retention.go` |

Tests:

- `chats.test.ts`: the feed sends `stream`, not `change`, for text growing in an answer already shown.
- `chats-screen.test.browser.tsx`: 30 deltas over a 400-message chat draw the list zero times. The view stays at the bottom, and
  text arriving does not move a reader who scrolled up.
- `retention_test.go`: old chat stream events are deleted, and stored messages and turn ends are kept.

The two UI tests fail on the old code.

### Before and after

Same machine, same test, production build.

| Measure | Before | After (two runs) |
| --- | --- | --- |
| Main thread busy while the answer streams | 42% (4.3 s of 10.3 s) | 21%, 21% |
| Slowest key typed while it streams | 192 ms | 40 ms, 40 ms |
| Longest task | 186 ms | 52 ms, 126 ms |
| Frames a second | 51 | 60, 60 |
| JS heap at the end | 119 MB | 61 MB, 58 MB |
| DOM nodes at the end | 39,700 | 28,300, 30,100 |
| Opening the chat (about 900 rows) | 666 ms | 292 ms, 291 ms |
| Send to first streamed text | 574 ms | 167 ms, 171 ms |

### The core

- **Events per answer.** One answer published 351 events, 162 KB on the event stream. Of these, 184 were the runtime's
  `agent.output` (coalesced every 50 ms) and 154 were the chat's own `chat.delta` (coalesced every 60 ms). The text goes out
  twice by design, since the runtime's events are documented for chats too (`docs/chats.md` section 3). The bus commits them in
  groups, so it is cheap. Since the review the chat's copy no longer stays in the database for ever.
- **Reads at scale.** A scratch test, not kept, used 500 chats and 100,000 messages. Each time is the best of 5.

  | Call | Time |
  | --- | --- |
  | `chat.list` | 15 ms |
  | `chat.list` with a search | 43 ms |
  | `chat.get` (latest 200 messages) | 0.9 ms |
  | `chat.messages` (200) | 0.9 ms |
  | `chat.search` | 56 ms |

  Nothing needed changing. `chat.list` is read again only on a chat's start, end and changes, at most every 150 ms.

## 2. Audit

Three independent reviewers read the code changed since the last audit's fixes (`080866d`): the core's chats, agents and
approvals; the core's security scan, local models and GitHub code; and the UI. Each finding was checked by reading its path
again. Each fix has a test that fails on the old code (the one exception is noted).

| # | Severity | What | Fix |
| --- | --- | --- | --- |
| A1 | Medium (security) | A "rest of the task" approval stored the words of the approved command, and the matcher read a `*` or `**` among them as a wildcard. A quoted `'**'` approved for the task then covered any later call of that program with any arguments. | Task rules match word for word (`sameCommands`, `internal/security/taskrules.go`). Test: `curl '**'` approved for the task does not cover `curl https://example.org`. |
| A2 | Medium | The GGUF header reader kept every wanted array under any key with a matching suffix, not just the model's own. A crafted model file could hold gigabytes per read, at every start. Separately, a header with 2^31 layers made the KV estimate loop for about 5 s. A cache too big to add to the model's size wrapped around and said it fits. | Only the architecture's own keys are read, and at most 131,072 values are kept. Implausible sizes are not estimated. The addition saturates. Tests: 40 foreign keys allocated 40 MB, now under 4 MB; the 2^31-layer header returned after 5.8 s, now at once. |
| A3 | Medium | An ACP agent's edit was asked about as a Write of the path alone, so the person did not see what it would write. An ACP delete passed as a change inside the worktree, where `rm -r` would be asked. | An edit carries the agent's own fields as the Write's `content`, so it is shown and hashed. A delete keeps the agent's name and input, so a person decides. |
| A4 | Medium | The chat composer cleared everything once a send was answered, including text typed, chats attached and files added while it was on its way. Two drops of 3 files at once passed the 4-file limit together. | Only what was sent is cleared, and the limit is checked again after the files are read. |
| A5 | Low-medium | A message sent while the chat's agent, folder or project was being changed claimed the chat as it was read. It wrote the old folder back and ran the old settings. | The claim requires the version that was read, and Send reads the chat again (up to 3 times) when a change landed meanwhile. |
| A6 | Low-medium | With Autostart on, the first start after an app update that carried a newer bundled llama.cpp started the server on the old build, which the install was replacing and deleting. | The server starts by itself once the download's build is in place. |
| A7 | Low | A chat tool summary was shortened to 160 characters before the secret scanner ran. A token cut in two was not found, and its first part reached the stored message and the events. | The summary is redacted, then shortened. |
| A8 | Low | A scan report the database refused was handed to readers without being copied, so a dismissal changed the kept report (wrong counts later) and two reads raced. | Readers get a copy (`Report.Clone`). |
| A9 | Low | After the official llama.cpp build was checked for updates and the TurboQuant build installed, the official tag was offered as its update for up to 12 hours. | The newest release is kept with the source and backend it was read for (`offeredLatest`). |
| A10 | Low | A fork's copied attachment files were written inside its transaction. A retry or rollback left files that no row named. | Each attempt starts the new chat's folder afresh, and a failed fork removes it. |
| A11 | Low | An answer could be sent twice. In the Inbox, keys A and D answered again while the item waited to leave. In a chat, a request card stayed clickable after its answer. On the task board, a merged card offered Merge again before the board was re-read. | The keys check the answer already sent. An answered card goes at once. The card says "Merging..." until the board is read again. |
| A12 | Low (performance) | The all-projects Crew page filtered every agent's tasks, skills and team on each render, so the work grew with agents times tasks. | Lookup maps are built once a render. This is a refactor: its behaviour is covered by the Crew page's existing tests, and no new test was added. |

**Checked and found clean:**

- Chat attachment paths: no traversal; files are stored under database ids and checked against their hash.
- Resend's transaction.
- Permission requests: answered once, withdrawn at turn end.
- ACP: answers `allow_once` only.
- The runtime's output cutting.
- The permission gate's edit re-check and `BeforeVisible`.
- Task rules: deleted with their task.
- The AG-UI projection.
- Bundle staging and digest checks.
- Scan tool updates: no downgrade.
- Built-in rules.
- Fingerprints.
- The GitHub repositories query: uses variables, bounded parallelism.
- Project keys.
- Task hand-on.
- UI: stale answers for an old key are dropped; subscriptions and timers are released on unmount; links go through `safeHref`
  and no raw HTML or remote images are rendered; nil lists are normalised.

## 3. Verification

- `go vet` (Linux and `GOOS=windows`) is clean on the changed packages. `go test` passes for `internal/chats`,
  `internal/security`, `internal/localllm`, `internal/scan`, `internal/safe` and `internal/core`.
- `npm test`: 1794 of 1794 pass. `tsc` for the app and the e2e suite is clean.
- `npx playwright test chat-perf` passes within its budgets.
