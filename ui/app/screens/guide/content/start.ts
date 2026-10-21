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
          'You get the result in **Needs you**. You approve, ask for changes, or stop.',
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
    title: 'Reading the Overview',
    summary: 'What each part of the Overview tells you, and where to go from it.',
    keywords: ['home', 'dashboard', 'today', 'insights', 'needs you'],
    body: [
      { p: 'The Overview is the first page of a project. It shows what needs a person, who is on the crew, and what is under way.' },
      {
        ul: [
          '**Needs you** lists approvals, reviews and questions as they happen. Each opens where you decide it. When it is empty, nothing is waiting for you.',
          '**Your crew** shows each agent and what it is doing. [Manage crew](/team) adds, changes or removes agents.',
          '**In motion** lists the tasks that are running. [All tasks](/tasks) opens the board.',
          '**Today** counts what was merged today, the open tasks, the agents working and the items that need you.',
          '**Insights** shows spend so far, tasks merged this week, how often work passed its first check, and the project’s last security scan. A figure that was never measured shows **n/a**, not zero.',
        ],
      },
      { p: 'The tabs under the title (Activity, Budgets, Evals report, Security) and the two links at the right (Project settings, Integrations) belong to the project.' },
    ],
    related: ['status-words', 'approvals', 'budgets', 'evals'],
  },
];
