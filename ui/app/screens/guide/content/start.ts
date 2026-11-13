import type { Topic } from '../model.ts';

export const START: readonly Topic[] = [
  {
    id: 'start',
    section: 'start',
    title: 'What Drydock is',
    summary: 'A desktop app where a crew of coding agents works on your repository and you decide what merges.',
    keywords: ['introduction', 'overview', 'loop', 'how it works', 'getting started'],
    body: [
      { p: 'Drydock runs coding agent programs you already have (Claude Code, Codex, Gemini CLI and others) as a **crew** on a project. Each agent works in its own copy of your repository. Nothing reaches your branch until you approve it.' },
      { h: 'The loop' },
      {
        ol: [
          'You describe a **task** and its acceptance criteria.',
          'A **worker** agent writes the change in a separate worktree.',
          'Drydock runs the checks (build, test, lint, and any you added) and records the **evidence**.',
          'A different agent, the **reviewer**, reads the change and gives a verdict.',
          'You get the result in the **Inbox**. You merge, ask for changes, or stop.',
          'Only then does the change merge, locally or through a pull request on GitHub.',
        ],
      },
      { p: 'Everything runs on this computer. Your source code is never uploaded by Drydock; the agent CLIs talk to their own services the way they always do.' },
      { note: 'The pages stay short on purpose. When a page has more to say, it has a **Learn more** link that opens the matching Guide topic.' },
    ],
    related: ['overview', 'crew', 'tasks', 'approvals', 'privacy'],
  },
  {
    id: 'overview',
    section: 'start',
    title: 'Reading the Work view',
    summary: 'What each part of a project’s Work view tells you, and where to go from it.',
    keywords: ['work', 'overview', 'home', 'dashboard', 'today', 'insights', 'needs you', 'crew'],
    body: [
      { p: 'Work is the first page of a project: its tasks and chats on the left, the one you open in the middle, the crew on the right.' },
      {
        ul: [
          '**The list** groups the tasks and chats: **Needs you** (waiting for your decision) first, then **Running**, then **Earlier**. Search filters it; **+** starts a chat in this project.',
          '**The middle** shows the task or chat you opened: a task with its decision card when it waits for you, its steps, checks and activity.',
          '**Crew** on the right shows each agent and what it is doing, then the project’s figures: spend so far, how often work passed its first check, the last security scan, the tasks merged, and the next milestone. A figure that was never measured shows **n/a**, not zero.',
          '**Board**, **Pull requests** and **Rooms** under the list open the project’s board, its pull requests and its rooms (with the unread count).',
        ],
      },
      { p: 'The tabs under the project’s name (Work, Crew, Settings) belong to the project. Everything that waits for you, in every project, is also in the [Inbox](/needs-you).' },
    ],
    related: ['status-words', 'approvals', 'budgets', 'evals'],
  },
];
