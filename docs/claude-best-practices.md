# Claude Code best practices in Drydock

Scope: what Anthropic's Claude Code documentation recommends for running agents and parallel work, which of it Drydock applies
automatically to a Claude Code agent, and which of it Drydock does not do (yet). Sources were read on 2026-10-08 and are linked
below; they describe Claude Code 2.1.x (Drydock was checked against 2.1.295). Decision records: `decisions.md` P16 and P17.

Sources: [Agent teams](https://code.claude.com/docs/en/agent-teams), [Costs](https://code.claude.com/docs/en/costs),
[Run agents in parallel](https://code.claude.com/docs/en/agents), [Best practices](https://code.claude.com/docs/en/best-practices),
[Subagents](https://code.claude.com/docs/en/sub-agents), [Worktrees](https://code.claude.com/docs/en/worktrees),
[Memory (CLAUDE.md)](https://code.claude.com/docs/en/memory), [Model configuration](https://code.claude.com/docs/en/model-config),
[Headless mode](https://code.claude.com/docs/en/headless).

## 1. How Drydock relates to Claude's own agent features

Drydock is the orchestrator. Each Drydock agent is one headless `claude -p` session in its own Git worktree. Anthropic's agent
teams (a lead that spawns teammates) are interactive-only and "Claude doesn't spawn teammates" in `-p` mode, so a Drydock agent
never forms a team of its own; the team is the one the human builds in Drydock. What the docs say about teams therefore applies to
how Drydock should coordinate its agents, not to anything inside one session.

## 2. What the documentation recommends, and what Drydock does

| Recommendation (source) | Drydock |
|---|---|
| **Give Claude a way to verify its work**: tests, a build, a linter; "the difference between a session you watch and one you walk away from" (best practices) | Drydock runs the project's checks after every worker turn and sends failures back; an independent reviewer and a human approval follow. By default the worker cannot run tests itself (Bash is not pre-approved); a project can switch on "Let Claude workers run this project's own checks" (off by default, decision P24), which allows exact check command lines only (`worker-commands.md`). The practices text tells it that checks run for it and to fix root causes. |
| **Address root causes, not symptoms; do not suppress the error** (best practices) | In the worker practices text: never skip, delete or weaken a test or suppress an error to pass a check. |
| **Explore, plan, then code** for multi-file or unfamiliar changes (best practices) | Templates have spec, design and test-first phases. Worker practices text: read the code and tests first and settle the approach before a multi-file change. |
| **Be specific; point at files and existing patterns** (best practices) | Task prompts carry the title, description and acceptance criteria; practices text: follow the patterns already in the code, add no dependencies. |
| **Keep CLAUDE.md short; every line must prevent a mistake**; "Bloated CLAUDE.md files cause Claude to ignore your actual instructions"; aim under 200 lines (best practices, costs, memory) | The practices text is about 200 words and a test keeps it under 330. Generated `AGENTS.md` carries accepted memory only up to a budget (newest first, 8 KB; the rest through `search_memory`), and each turn's context manifest says when what Claude gets in every turn (AGENTS.md and `.claude/rules`) is over 32 KB; with skill delivery `relevant` (the default) only the repository's own skills (one with `paths` unless the task's files match none of them) and the imported skills matching the task's files are rules (decision P37). Before P37 nothing budgeted them. Claude Code started with `--setting-sources user` does not read them itself; the adapter appends `CLAUDE.md` (with its `@AGENTS.md` import) and `.claude/rules/*.md` to the system prompt, at most 64 KB a file and 256 KB in all (section 5). |
| **Fresh context for review; a reviewer prompted to find gaps will find some, so tell it to flag only what affects correctness or the stated requirements** (best practices, "Add an adversarial review step") | The reviewer is a different agent in a fresh, read-only session: Claude Code in its default permission mode with Edit, Write, MultiEdit, NotebookEdit and Bash denied (`RunSpec.ReadOnly`; plan mode is not used, see section 5). The review prompt and the reviewer practices text say: ask for a revision only for correctness, requirements, security or unrequested changes; style, extra abstraction and tests for impossible cases are optional notes. |
| **Clear context between unrelated tasks; sessions that need two corrections should restart** (best practices, costs) | A task's first work turn is a fresh session. A later work turn of the same run continues that session (`--resume`) while the previous turn ended below 70 % of the model's context window, and its prompt still carries only the task and the latest feedback (token-control.md rule 16). At or above 70 %, on an escalation or any change of model or agent, after a watchdog pause or an interrupted attempt, it starts a fresh session seeded with a handoff artifact Drydock writes, never the transcript (decision P39, token-control.md "As built: session policy and handoffs"). Not applied: restarting a session after two corrections; below the threshold the session continues. |
| **Avoid file conflicts: two teammates editing the same file leads to overwrites; give each a different set of files** (agent teams) | Worktree per task prevents live overwrites. A task whose files overlap an earlier running task waits for it, and the base is brought into the branch and the checks re-run before review and merge (`parallel-work.md`). The planner practices text asks for tasks that own different files and to say which files each touches. |
| **Team size: start with 3 to 5; token cost scales linearly; 5 to 6 tasks per teammate** (agent teams) | Default `maxParallel` is 2, the settings page allows up to 16. The Development settings show a note above 5. |
| **Size tasks as self-contained units with a clear deliverable** (agent teams) | The planner prompt asks for tasks one agent can build and verify on its own branch. |
| **Use Sonnet for teammates; reserve Opus for complex architectural decisions; Haiku for simple subagent work** (costs, model-config) | Role presets suggest a model and effort when a Claude agent is created (section 3). |
| **Effort: `medium` for day-to-day work with a clear scope, `high` where verification matters or edge cases are likely, `max` "prone to overthinking"** (model-config) | The presets use medium for workers, high for reviewers and architects. |
| **Agent teams use roughly 7x the tokens of a session in plan mode; shut teammates down when done** (costs) | Per-task cost, budgets and the first-measurement report are in evals.md. A Drydock agent session ends with its turn. |
| **Hooks are deterministic, instructions are advisory** (best practices, memory) | Drydock's checks, review and approvals are the deterministic gates; the practices text is advisory and says so. |
| **Subagents run in their own context and report back a summary** (sub-agents) | Allowed (the tool is not blocked); their cost is part of the session's reported cost. |
| **Prefer CLI tools over MCP servers for context efficiency; disable unused MCP servers** (costs) | Agents only get the MCP servers the human granted them, and tool definitions are deferred by the CLI. |

## 3. What is applied automatically to a Claude Code agent

When the adapter is Claude Code, the switch "Follow Claude Code best practices" is on. It can be turned off per agent and per
template. What it does:

1. Adds a short, role-specific working-practices text to the system prompt of every session of the agent
   (`--append-system-prompt-file`: a temporary file, never a command-line argument, because `claude.cmd` runs through cmd.exe, which cuts a command line at its first newline), before the human's own purpose text. Four families, picked from the role of the session (a review
   turn of any agent is `reviewer`): worker (writes code), reviewer (judges, never modifies), planner (pm, architect, ux designer:
   words, not code), lead (team lead, manager, supervisor: supervises, never writes code, decides crisply, prefers the smallest
   intervention, escalates to the human when unsure; decision P26, `PracticesVersion` 2). The exact text is shown in the
   create/edit form and returned by the `claude.practices` method.
2. Records the version of the text in the `agent.started` event (`practices`), so a run says which wording it was given.
3. Suggests a model and effort for the role and fills them in until the human changes them (workers, planners and the lead: sonnet,
   medium; reviewers: sonnet, high; architects: opus, high). The suggestion is shown with the reason; nothing is forced.
4. Starts Claude Code with `--setting-sources user` (section 5) and a trimmed environment (security.md), and appends the project's own instruction files to the system prompt, because that flag stops Claude Code reading them (section 5).

These are always applied, with or without the switch: read-only sessions (reviewer, security review, room and DM replies, planner, team lead, spec and design turns) run Claude Code in its default permission mode with `--disallowedTools Edit,Write,MultiEdit,NotebookEdit,Bash`, not in plan mode, and get no tool-wide `Read` allow rule, so they read inside their working folder only (room replies pre-approve only Drydock's own `mcp__drydock__*` tools).

The wording of an instruction matters. Measured with Claude Code 2.1.295 (haiku, low effort), a style instruction placed after a
tool call was followed in about 2 of 9 runs when worded "does not override the task" and in 7 of 8 when worded "always in force,
how you work" (decisions P14). Treat the practices text as advisory: a model can ignore it, and nothing in Drydock's safety depends
on it.

## 4. What Drydock does not do yet

- **Workers run the project's checks themselves only when the project allows it.** By default Bash is not pre-approved, so a
  worker finds out that a check failed only on its next turn. Since 2026-10-09 a project can allow exact whole-command rules from
  a fixed list (decision P24, `worker-commands.md`; off by default; tiers 2 and 3 are not built).
- **Overlap detection is path-based.** It cannot see two tasks that start without planned files and write the same file at the same moment, nor changes to different files that break each other; sync and the re-run checks catch those (`parallel-work.md` section 3).
- **No `maxTurns` or per-run budget flag** is passed to the CLI; Drydock's own budgets pause a run between turns.
- **No plan mode for workers.** Planning is done by separate phases (spec, design) in the templates, not by the worker.
- Subagent definitions (`.claude/agents`) and skills of the repository are not managed by Drydock for Claude agents.
- Agent teams, agent view, dynamic workflows and `/batch` are not used: Drydock is the orchestrator.

## 5. A security finding from reading the headless page

`claude -p` loads the settings of the working directory, and a repository's `.claude/settings.json` can define hooks (commands). The
headless page says so: without `--bare`, a `-p` session "runs the hooks in a project's `.claude/settings.json` ... even in a folder
you've never trusted". Measured here with 2.1.295: a `UserPromptSubmit` hook in a repository ran during a plain `claude -p`, and
did not run with `--setting-sources user` (nor with `--settings '{"disableAllHooks":true}'`). Drydock agents run in the human's own
repositories, including ones cloned from elsewhere, so Drydock starts Claude Code with `--setting-sources user`: the human's own
user-level settings and hooks still apply, a repository's do not. `--bare`, which the page recommends for scripted calls, is not
usable because it never reads the human's login (decision D1). `--strict-mcp-config` already keeps a repository's `.mcp.json`
servers out. Test: `TestRealClaudeDoesNotRunARepositorysOwnHooks` (real CLI, with a control run that proves the hook runs
otherwise).

The same flag also stops Claude Code loading the project's memory files: `CLAUDE.md`, the `AGENTS.md` it imports and `.claude/rules` (measured with 2.1.296). The adapter therefore reads `CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md` (following `@` imports only inside the working folder) and `.claude/rules/*.md` itself and appends them to the system prompt, which goes in a file with `--append-system-prompt-file` (`internal/agents/claude/project_memory.go`, decision P16). A rule's `paths:` front matter is not applied that way: every rule is in every turn. Settings files are never read. Test: `TestRealClaudeGetsTheProjectInstructionsWithUserSettingsOnly`.

Read-only sessions do not use plan mode: Claude Code answers a plan-mode session started with `--model haiku` with Sonnet (since 2.0.17; measured with 2.1.296: the init event reports haiku, every message is Sonnet, about 10 to 50 times the cost). They run in the default mode with every writing tool denied instead (`RunSpec.ReadOnly`, `internal/agents/claude/adapter.go`). Test: `TestRealClaudeReadOnlySessionKeepsItsModelAndCannotWrite`.
