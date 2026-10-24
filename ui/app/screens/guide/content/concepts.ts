import type { Topic } from '../model.ts';

export const CONCEPTS: readonly Topic[] = [
  {
    id: 'crew',
    section: 'concepts',
    title: 'Crew, roles and agents',
    summary: 'An agent is a real coding CLI with a role; a task needs a worker and a different reviewer.',
    keywords: ['team', 'agent', 'role', 'worker', 'reviewer', 'lead', 'template', 'prebuilt'],
    body: [
      { p: 'An **agent** is one of the coding CLIs found on this computer (see [Agents](/agents)) with a name, a role, a model and an effort level. The agents of a project are its **crew**.' },
      {
        ul: [
          'The **role** (backend, reviewer, pm, architect, lead and so on) decides which work an agent is picked for. Roles are plain words you choose.',
          'A task needs a **worker** and a **different agent as reviewer**, so nobody grades their own work.',
          'An agent with the role **lead** supervises the team: it steps in when a task loops, stalls or trips the watchdog, and can post a stand-up. It checks a running task that has shown no progress for the number of minutes set in Project settings (empty or 0 means 15, at most 1440).',
          'A **template** is a team member defined once in the [Library](/library). Applying it on the Crew page makes an ordinary agent in that project. **Prebuilt agents** are templates built into Drydock, with their skills and connectors pinned.',
          '**Standing instructions** are text an agent reads on every turn. The Crew page shows when an agent has none.',
        ],
      },
      { h: 'Models and routing' },
      { p: 'An agent can list the models it may choose between. When it does, the router picks one for every turn from that list only, and records why. Without a list, the agent uses its configured model. See [Routing rules](guide:routing-rules).' },
    ],
    related: ['tasks', 'skills', 'routing-rules', 'connect-cli'],
  },
  {
    id: 'tasks',
    section: 'concepts',
    title: 'Tasks and evidence',
    summary: 'A task is a unit of work with acceptance criteria; evidence shows whether it passed.',
    keywords: ['task', 'board', 'backlog', 'acceptance criteria', 'evidence', 'branch', 'worktree', 'scheduler'],
    body: [
      { p: 'A **task** has a title, a description and acceptance criteria written as Given, When, Then. It works in a branch named `drydock/task_…` in a separate worktree of your repository.' },
      { h: 'The board' },
      { table: { head: ['Column', 'What is in it'], rows: [['Up next', 'Backlog, ready and claimed tasks'], ['Crew working', 'In progress, in review, awaiting your approval, blocked'], ['Needs you', 'Waiting for you, or a run that waits for your decision'], ['Merged / Done', 'Completed, failed or cancelled']] } },
      { h: 'Evidence' },
      { p: 'Build, test and lint always run on a task. The checks you add in Project settings run too. Each result is stored as evidence with its output and duration. A check whose tool is missing is reported as **skipped**, never as passed.' },
      { h: 'The scheduler' },
      { p: 'With **Start ready tasks automatically (scheduler)** on, the scheduler starts ready tasks up to the limit set in Project settings. Anthropic suggests starting with 3 to 5 agents at once: token cost grows with every active agent, coordination gets harder, and more agents rarely finish the work faster. **Start ready tasks now** runs one scheduler pass by hand.' },
    ],
    related: ['workflows', 'approvals', 'delivery', 'status-words'],
  },
  {
    id: 'rooms',
    section: 'concepts',
    title: 'Rooms and channels',
    summary: 'Where you and the crew talk: the project room, a channel for each task, and direct messages.',
    keywords: ['chat', 'channel', 'message', 'mention', 'dm', 'room'],
    body: [
      { p: 'A **room** is a conversation with the crew. The general room belongs to the project, each task has a channel of its own, and you can message one agent directly.' },
      { h: 'Who answers' },
      { p: 'In **Auto** mode simple rules decide who answers a message: an explicit mention first, then the task’s assignee, then a role. `@name` mentions an agent, `@role:` a role, `#` a task, and `/` opens the commands.' },
      { p: '**Chats** (the Home tab) are separate: a chat is one agent CLI in a folder, with or without a project, and it is not counted against a project’s budget.' },
    ],
    related: ['crew', 'budgets'],
  },
  {
    id: 'workflows',
    section: 'concepts',
    title: 'Workflows and checkpoints',
    summary: 'A task runs through a graph of steps; each step is saved so a run can pause and resume.',
    keywords: ['workflow', 'run', 'graph', 'checkpoint', 'loop', 'step', 'node'],
    body: [
      { p: 'A task runs through a **workflow**: a graph of steps such as spec, code, checks, review and your approval. Open a task’s run to see the graph, the step each node is on, and the timeline of checkpoints.' },
      {
        ul: [
          'After every step Drydock writes a **checkpoint**, so a run that pauses (for you, or because a limit was reached) continues from where it stopped.',
          'A step can **loop**: when checks fail, the worker tries again up to the loop limit (shown on the node, for example “loop ≤ 5”).',
          'A run waits in the status **waiting for you** when it needs a decision. The request is in Needs you.',
        ],
      },
      { p: 'The Activity page lists the events behind a run, see [Events](guide:events).' },
    ],
    related: ['tasks', 'approvals', 'events'],
  },
  {
    id: 'approvals',
    section: 'concepts',
    title: 'Approvals and Needs you',
    summary: 'Everything that needs a person arrives in one inbox, and only you can decide it.',
    keywords: ['needs you', 'approve', 'approval', 'review and merge', 'escalation', 'inbox', 'bell'],
    body: [
      { p: '**Needs you** (the bell in the top bar) collects what the crew cannot decide: the final “Review and merge” of a task, a tool call that writes through an MCP server, a limit reached, a question from an agent, and a stuck task the lead escalates. Each item opens where you decide it.' },
      {
        ul: [
          '**Approve** lets the step continue. **Request changes** sends your note back to the worker.',
          'An agent can be escalated to you when it repeats the same tool call, keeps hitting the same error, makes too many tool calls in a turn, runs longer than allowed, fails several calls in a row, or makes no progress.',
          'Only you can change a budget, a routing rule or an integration rule. An agent can propose; it cannot decide.',
        ],
      },
      { p: 'Nothing merges without your approval. See [Delivery modes](guide:delivery) for what happens after you approve.' },
    ],
    related: ['delivery', 'budgets', 'integrations'],
  },
  {
    id: 'delivery',
    section: 'concepts',
    title: 'Delivery: local merge or pull request',
    summary: 'How an approved task reaches your branch, and what CI must do first.',
    keywords: ['merge', 'pull request', 'pr', 'ci', 'branch protection', 'delivery mode', 'no ci'],
    body: [
      { p: 'Under **Delivery** in [Project settings](/project) you choose the **mode** (merge in this repository, or open a pull request on GitHub) and the **merge strategy** (how the task branch is combined with the default branch). A change affects tasks started afterwards, and the core refuses pull request mode when the repository cannot do pull requests.' },
      {
        ul: [
          '**Merge in this repository:** after your approval Drydock merges the task branch into the default branch.',
          '**Pull request:** a finished task is pushed and a pull request is opened, with a description made from the task’s summary, acceptance criteria, evidence and the reviewers’ verdicts. A retry finds the open pull request of the branch instead of opening a second one. CI and reviews are read from GitHub.',
        ],
      },
      { h: 'Before a pull request merges' },
      {
        ul: [
          'CI must have passed on **the commit that was reviewed**, including the checks the branch protection requires. A later push is not merged.',
          'When no CI reports within 2 minutes you are asked to check again, merge without CI this time, or stop.',
          'GitHub’s branch protection is never bypassed and auto-merge is never enabled. Drydock merges through GitHub with the project’s merge method and deletes the task branch.',
          'A repository that really has **no CI** can be declared so in Project settings. A pull request nothing reports on is then merged after the review and your approval; checks that do report still have to pass. Your name and the time are recorded.',
        ],
      },
      { h: 'When the merge is blocked' },
      {
        table: {
          head: ['GitHub says', 'What it means'],
          rows: [
            ['No checks reported', 'No check run and no commit status exists on the reviewed commit. Merging now would merge code no CI tested.'],
            ['Some checks missing', 'Some checks reported, but not every check the branch protection requires.'],
            ['Still running', 'CI started but did not finish in time.'],
            ['Blocked', 'A required review or check is missing or has not passed. The pull request on GitHub shows which.'],
            ['Behind', 'The base branch moved on and its protection wants the branch up to date first. A commit added on GitHub was not reviewed, so Drydock will not merge it unseen.'],
            ['Conflicts', 'The branch conflicts with the base branch.'],
            ['Draft', 'Mark the pull request ready for review on GitHub.'],
            ['Unknown', 'GitHub was still working out whether it can merge; checking again usually settles it.'],
          ],
        },
      },
    ],
    related: ['github', 'project-settings', 'approvals'],
  },
  {
    id: 'budgets',
    section: 'concepts',
    title: 'Budgets and cost',
    summary: 'A budget warns at a percentage and stops new agent turns at 100%.',
    keywords: ['budget', 'limit', 'cost', 'spend', 'tokens', 'estimated', 'reported', 'unknown'],
    body: [
      {
        ul: [
          'A budget **warns** at its warning percentage (80% by default).',
          'At **100%** no new agent turn starts. A task’s run waits for you in Needs you, where you can raise the limit or approve one more turn. Agents stop answering in the room and in direct messages until the limit is raised.',
          'Your own chats are not counted. Only you can change a budget.',
        ],
      },
      { h: 'Reported, estimated, unknown' },
      { p: 'Cost has three sources and they are never merged. **Reported** means the agent CLI said what it cost. **Estimated** means Drydock priced it from a price table. **Unknown** means neither knew: those calls are counted, not priced at zero.' },
    ],
    related: ['approvals', 'evals'],
  },
  {
    id: 'skills',
    section: 'concepts',
    title: 'Skills, registries and packs',
    summary: 'Skills are instructions agents read; packs are reference text they search and cite.',
    keywords: ['skill', 'registry', 'library', 'pack', 'expert pack', 'instructions', 'recommend', 'context'],
    body: [
      { h: 'Skills' },
      {
        ul: [
          'A **skill** is a set of instructions agents read. The [Library](/library/skills) holds every skill of the registries you enabled. Drydock fills it from the allowed GitHub repositories, pinned to a commit, at start and twice a day. Nothing reaches a project until you add it, and adding is your acceptance.',
          'Skills found in the repository itself are offered until you accept them. Accepted skills reach agents as path-scoped rule files that stay out of the diff.',
          'Skills belong to **agents**, never to the whole team: each agent is a specialist in its role. Give an agent its skills on its own page (Crew, then the agent), where you can also add one straight from the Library. A skill that names a role is suggested for the agents of that role.',
          '**Usage** shows what each skill cost and whether it was read. **Loaded**: its full text went into a run. **Listed**: it was only named, for on-demand reading. **Opened**: an agent read it on demand.',
          '**Recommendations** are made by an agent of your team from the project’s plan, tasks and team. You apply or dismiss each one.',
        ],
      },
      { h: 'Which skills go into every turn' },
      { p: 'The repository’s own skills are loaded into every turn (one with paths only when the task names no files or names one of them), and so is an imported skill whose paths match the task’s files. The other skills (a vendor’s, a prebuilt agent’s) are listed by name and purpose and the agent opens one when its work needs it. That costs fewer tokens in every turn; a listed skill the agent does not think of opening is not used. The project can instead load every skill into every turn: large skills with nothing to do with the task then cost tokens on every request, and the run’s context record says when the total is over budget.' },
      { h: 'Expert packs' },
      { p: 'A **pack** is a body of reference text (documentation, standards) that agents search and cite. Everything inside a pack is outside text: it is shown as inert text, passages that look like instructions are quarantined, and nothing in a pack can grant a permission or make a decision. A pack manifest is YAML; a source can be a local folder (type `local_dir` with a path), a list of URLs or a site. Installing does not fetch anything; use Refresh.' },
    ],
    related: ['knowledge', 'untrusted-text'],
  },
  {
    id: 'knowledge',
    section: 'concepts',
    title: 'Memory, requirements and the repo map',
    summary: 'What the crew knows about the project and where each piece came from.',
    keywords: ['memory', 'requirements', 'repo map', 'knowledge', 'decisions', 'conventions'],
    body: [
      {
        ul: [
          '**Memory** holds decisions, conventions and known issues that agents read. Agents (and you) can propose entries; only an entry you accept reaches agents. Secrets are redacted before anything is stored. Memory is shared by the whole crew.',
          '**Requirements** are text you upload or paste. Link its criteria to tasks and see which tasks have passing test evidence. The text is outside text: it is stored with secrets redacted, shown inert, and passages that look like instructions to an agent are quarantined.',
          'The **repo map** is the most important code of the repository as agents get it from the `repo_map` tool: files ranked by how much the rest of the code uses them, each with its most important definition lines, within a token budget. Focus paths rank what those files use higher. It is made on this computer from the repository’s files (`.gitignore` is respected); nothing leaves the machine.',
        ],
      },
    ],
    related: ['skills', 'untrusted-text'],
  },
];
