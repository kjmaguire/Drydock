import type { Topic } from '../model.ts';
import { RULES_READ_ON, RULE_SOURCES, repoUrl } from '../../security/rules-sources.ts';

export const REFERENCE: readonly Topic[] = [
  {
    id: 'status-words',
    section: 'reference',
    title: 'Status words',
    summary: 'What a task status, an agent state and a check result mean, and what to do about each.',
    keywords: ['status', 'state', 'idle', 'working', 'paused', 'stopped', 'resting', 'waiting', 'blocked', 'verification'],
    body: [
      { h: 'Task statuses' },
      {
        table: {
          head: ['Shown as', 'Means'],
          rows: [
            ['Backlog', 'Described, not started and not ready to run.'],
            ['Ready', 'Can be started; the scheduler or **Start** picks it up.'],
            ['Claimed', 'An agent has taken it and is about to begin.'],
            ['In progress', 'The worker is writing the change.'],
            ['Blocked', 'It cannot go on until something else is done.'],
            ['Waiting for you', 'A run needs a decision from you (see Needs you).'],
            ['In review', 'The reviewer is reading the change.'],
            ['Awaiting your approval', 'Checks passed and the review is done; the final decision is yours.'],
            ['Completed', 'You approved it and it was delivered.'],
            ['Failed, Cancelled', 'It ended without being delivered.'],
          ],
        },
      },
      { h: 'Agent states' },
      {
        table: {
          head: ['Shown as', 'Means'],
          rows: [
            ['Working', 'The agent is on a turn: writing, thinking or using a tool.'],
            ['Waiting for you', 'The agent is held until a person answers: a permission request, an approval, or something it stopped on. Needs you lists it.'],
            ['Waiting', 'The agent waits on something other than you, such as another agent.'],
            ['Starting', 'The agent’s program is starting.'],
            ['Idle', 'The agent is not on a turn; its last turn finished.'],
            ['Error', 'The agent’s last turn failed. Hover the status to read why.'],
            ['Stopped', 'The agent’s program is not running (also before its first start). It starts when it is given work.'],
          ],
        },
      },
      { h: 'Check results' },
      { p: '**Pass** and **Fail** are what the check returned. **Skipped** means the tool the check needs is missing; it is never counted as a pass. **n/a** means nothing was measured, which is different from a measured zero.' },
    ],
    related: ['tasks', 'approvals', 'evals'],
  },
  {
    id: 'routing-rules',
    section: 'reference',
    title: 'Model routing and its rules',
    summary: 'How the router picks a model for each turn, and the language of its rules.',
    keywords: ['router', 'routing', 'rule', 'when', 'then', 'priority', 'cel', 'tier', 'cheap', 'premium', 'escalate', 'fallback', 'effort', 'quota', 'preview', 'probe'],
    body: [
      { h: 'What the router decides' },
      { p: 'The router chooses the model for every turn of an agent whose human listed the models it may choose between (Crew, Edit): its task work and reviews, its replies in the room, and the template steps it takes (the spec, the tests written first, the design, the security review). It chooses only among that agent’s own models and never picks another agent’s CLI. Other agents use their configured model. The router also decides for the app’s own model calls. Every turn records a decision so outcomes can be compared; where a decision was static, the explanation says so. **Why this model?** in a run, task or room shows the reason.' },
      { h: 'Rules' },
      { p: 'Rules are evaluated by the router in **priority** order, highest first. **Invariants** (data class, quota, budget hard limit) always win. Only you can change rules. Each rule has a **WHEN** condition and a **THEN** effect.' },
      {
        table: {
          head: ['WHEN can read', 'Examples'],
          rows: [
            ['`task.*`', '`task.role == \'Security\'`, `task.worker_model`, `task.risk`, `task.cheap_iterations >= 3`'],
            ['`candidate.*`', '`candidate.tier == \'premium\'`, `candidate.model`, `candidate.quota_remaining_pct < 0.10`'],
            ['`run.*`', '`run.kind in [\'review\',\'security_review\']`'],
            ['`budget.*`', '`budget.pct_used >= 0.8`'],
          ],
        },
      },
      {
        table: {
          head: ['THEN effect', 'Does'],
          rows: [
            ['`prefer_tier: cheap`', 'Prefers a cheap model.'],
            ['`set_effort: high`', 'Sets the agent’s effort level for the turn.'],
            ['`penalty: 0.5`', 'Makes a candidate less likely without forbidding it (for example, prefer a reviewer on a different model).'],
            ['`escalate_to: premium`, `handoff: true`', 'Moves to a premium model and starts a fresh session seeded with a handoff note.'],
            ['`deny: true`', 'Forbids a candidate.'],
          ],
        },
      },
      { h: 'Automatic escalation' },
      { p: 'An agent set to start cheap moves to a premium model after three cheap tries that fail their checks, or when the reviewer keeps sending premium work back. It routes every turn of that agent (task work, reviews, template steps and room replies), only from its own list, on its own CLI.' },
      { h: 'Fallback' },
      { p: 'When the chosen model or its provider is unavailable (rate-limited, overloaded, down, model not found) before the turn does any work, the turn moves to the next model in the fallback chain: at most 2 hops, the same tier or cheaper, within the budget. The work’s own errors never fall back.' },
      { h: 'Preview a decision' },
      { p: '**Preview a decision** is a dry run with the same rules, providers, quotas and budgets as a real decision. Nothing is recorded and no quota is taken.' },
      { h: 'Cheap model selection' },
      { p: 'For each enabled cheap, private provider Drydock can test the cheapest few models with a small tool-call check and pick a default and a fallback. The check sends a few synthetic requests to the provider and can cost a little. The **startup probe** repeats it every time Drydock starts, and is off unless you turn it on.' },
    ],
    related: ['crew', 'providers', 'budgets'],
  },
  {
    id: 'events',
    section: 'reference',
    title: 'Events in the Activity log',
    summary: 'What the event names mean and how to filter the log.',
    keywords: ['activity', 'event', 'log', 'stream', 'seq', 'filter', 'prefix', 'workflow', 'room.presence_changed'],
    body: [
      { p: 'The Activity page shows the event log as the core persisted it, newest first. Each row has a sequence number, the time, the event **type**, its **scope** (the task and run it belongs to) and its data. **Stream: open** means this window is receiving new events live.' },
      { p: 'Type a prefix in the filter, for example `task.` or `workflow.`, to show only that family. **This project only** hides events of other projects.' },
      {
        table: {
          head: ['Family', 'Examples', 'Means'],
          rows: [
            ['`task.*`', '`task.evidence`', 'A task changed or collected evidence (a check result, a review request).'],
            ['`workflow.*`', '`workflow.node_completed`, `workflow.step_completed`, `workflow.checkpoint_written`, `workflow.paused`, `workflow.request_raised`', 'A run moved through its graph; each completed step writes a checkpoint.'],
            ['`approval.*`, `permission.*`', '`approval.requested`, `permission.decided`', 'Something is waiting for a decision (see Needs you), or a permission request was answered: by a person, by a rule, or by nobody in time.'],
            ['`room.*`', '`room.presence_changed`, `room.message_posted`', 'A conversation changed.'],
            ['`agent.*`', '`agent.created`, `agent.started`, `agent.output`, `agent.completed`', 'An agent was added or changed, a process started, streamed what it did (text, reasoning, tool calls and results), or ended a turn. The task page\'s Activity is read from these.'],
          ],
        },
      },
      { p: 'Payloads of events never leave this computer: a support bundle lists only event types and ids.' },
    ],
    related: ['workflows', 'support-bundle'],
  },
  {
    id: 'agent-environment',
    section: 'reference',
    title: 'The environment agents inherit',
    summary: 'Agent programs start with a trimmed environment; here is what that means and how to add to it.',
    keywords: ['environment', 'variables', 'env', 'path', 'aws', 'bedrock', 'token', 'claude_', 'anthropic_'],
    body: [
      { p: 'Agent programs start with a **trimmed environment**, not everything Drydock was started with. A token another tool left in your environment (GitHub, a cloud provider) is not handed to an agent that a prompt could ask to print it.' },
      { p: 'Claude Code keeps everything named `CLAUDE_*` or `ANTHROPIC_*`, so its own sign-in keeps working.' },
      { p: 'Under **What agents inherit** in [Settings and about](/settings) you can name more variables to pass, for example `AWS_PROFILE` or `CLAUDE_CODE_USE_BEDROCK`. Give names only, one per line; a value is refused. The change applies from the next agent start. The page lists the names every agent keeps (such as `PATH`, `PATHEXT`, `SYSTEMROOT`, `TEMP`, `HOME` and `USERPROFILE`) as the core reports them.' },
    ],
    related: ['connect-cli', 'privacy'],
  },
  {
    id: 'capabilities',
    section: 'reference',
    title: 'What a connection test shows',
    summary: 'The capability matrix of an agent CLI and the evidence behind each value.',
    keywords: ['capability', 'matrix', 'verified', 'not available', 'provenance', 'structured output', 'model selection', 'install guidance'],
    body: [
      { p: 'A connection test starts the real CLI and reports a verdict, the steps it took, what is limited, and a **capability matrix**: for each capability (such as structured output or model selection) its value and the **evidence** for it.' },
      {
        table: {
          head: ['Evidence', 'Means'],
          rows: [
            ['observed', 'Seen during this test.'],
            ['declared', 'The CLI says so; the test did not check it.'],
            ['unknown', 'Not verified.'],
          ],
        },
      },
      { p: 'On the Crew page an agent\u2019s capabilities list works the same way: a capability that is **absent** from the list was not verified, which is not proof that the CLI lacks it. Install guidance is advice for you: Drydock never runs an install for you.' },
    ],
    related: ['connect-cli'],
  },
  {
    id: 'rule-licences',
    section: 'reference',
    title: 'Rule sets and their licences',
    summary: 'Public sets of Semgrep-syntax rules and what each licence says, as read on one day.',
    keywords: ['rules', 'licence', 'license', 'semgrep', 'opengrep', 'commons clause', 'lgpl', 'agpl', 'mit'],
    body: [
      { p: `Drydock comes with rules built in: its own, and two sets under the MIT licence (dgryski/semgrep-go for Go and 0xdea/semgrep-rules for C and C++). They run on every scan. The other public sets below are not built in, on the terms of their licences. These public repositories hold sets, with what each one’s own licence file or README said on ${RULES_READ_ON}. A licence can change, so read it yourself and use a set only in the way it allows. This is not a recommendation and not legal advice.` },
      {
        table: {
          head: ['Repository', 'Licence, as the repository said', 'For'],
          rows: RULE_SOURCES.map((s) => [`[${s.repo}](${repoUrl(s.repo)})`, s.terms ? `${s.licence} [Terms](${s.terms})` : s.licence, s.covers]),
        },
      },
    ],
    related: ['scan'],
  },
  {
    id: 'evals',
    section: 'reference',
    title: 'How the evals report counts',
    summary: 'What each figure of the Evals report is, and why some show n/a.',
    keywords: ['evals', 'report', 'completion', 'first check', 'cheap-worker', 'accepted', 'n/a', 'export', 'median'],
    body: [
      { p: 'The Evals report shows how the system performed on **your own tasks**: counts and medians from your history, not claims of significance. A rate over a zero denominator shows **n/a**, because no measurement is not a measured 0.' },
      {
        table: {
          head: ['Figure', 'How it is counted'],
          rows: [
            ['Tasks started / accepted', 'An **accepted** task reached Completed after its checks passed and you approved it.'],
            ['Completion', 'Accepted tasks divided by started tasks.'],
            ['First check test pass', 'Tasks whose first test run passed.'],
            ['Final test pass (accepted)', 'Accepted tasks whose last test run passed.'],
            ['Cheap-worker share of accepted', 'Accepted tasks in which every worker turn ran on a cheap, private model and nothing escalated. A single premium final review does not disqualify.'],
            ['Routing decisions accepted without escalation', 'Router decisions of accepted tasks that never had to move up.'],
            ['Active / failed / cancelled', 'The other tasks, so the counts add up.'],
          ],
        },
      },
      { p: 'Cost is shown as **reported**, **estimated** and **unknown**, and these are never merged. Unknown rows are calls whose cost no CLI or provider reported and no price table covers; they are counted, never priced at zero. A premium verdict is counted only for reviewers that have usage rows (their tier is known). **Export to a local file** writes the report on this computer; it stays there.' },
    ],
    related: ['budgets', 'tasks'],
  },
];
