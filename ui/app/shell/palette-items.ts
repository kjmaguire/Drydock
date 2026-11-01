// What the command menu offers and how it ranks a query. Navigation, tasks, agents and harmless actions; with a task in
// context (the task's page, its workflow, its room) also what can be done with that task: hand it to an agent of the team
// (or to the person), ask the team lead to split it, scan its project, ask an agent CLI about it. Nothing here approves,
// merges, deletes or stops anything, so the menu can never bypass an approval.

import type { Agent, Room, Task } from '../api/types.ts';
import { chatTitle, type ChatListItem } from '../api/chatClient.ts';
import type { AssignPreview } from '../api/controlClient.ts';
import { hubOf, parseRoute } from '../lib/route.ts';
import { topicPath } from '../screens/guide/model.ts';
import { GUIDE_TOPICS } from '../screens/guide/topics.ts';
import { routes } from '../routes.ts';

export type Group = 'Assign' | 'Actions' | 'Go to' | 'Navigate' | 'Chats' | 'Tasks' | 'Agents';
/** The groups of the menu with nothing typed and no task in context (the order they are listed in). */
export const GROUPS: Group[] = ['Navigate', 'Chats', 'Tasks', 'Agents', 'Actions'];
/** The groups with nothing typed and a task in context: what can be done with it, then where to go. */
export const CONTEXT_GROUPS: Group[] = ['Assign', 'Actions', 'Go to'];

/** What an option does to the task in context (the menu runs it with the client). */
export type TaskOp = { kind: 'assign'; assignee: string; agentId?: string } | { kind: 'split' } | { kind: 'scan' } | { kind: 'ask' };

/** The prefixes that narrow the menu: settings pages and actions, tasks, agents. */
export type Scope = '>' | '#' | '@';

export interface PaletteItem {
  id: string;
  group: Group;
  label: string;
  hint?: string;
  keywords?: string[];
  /** Either a path to go to, an action of the shell, or something done to the task in context. */
  href?: string;
  action?: string;
  op?: TaskOp;
  /** The prefix that lists it ('>' settings and actions, '#' tasks, '@' agents). */
  scope?: Scope;
  /** About the task in context: listed with nothing typed while a task is in context. */
  context?: boolean;
  /** Keys shown beside it (a shortcut: ["G", "C"] is G then C). */
  keys?: string[];
  /** The agent it names, drawn as its avatar. */
  agent?: { id: string; name: string };
  /** What running it does, in one line (the "What happens" panel). */
  what?: string;
}

/** G then a letter goes to a screen, from anywhere but a text field (palette.tsx listens for it). */
export const GO_CHORDS: Record<string, string> = { i: routes.needsYou.href(), t: routes.tasks.href(), c: routes.team.href() };
const chordOf = (href: string): string[] | undefined => {
  const k = Object.keys(GO_CHORDS).find((x) => GO_CHORDS[x] === href);
  return k ? ['G', k.toUpperCase()] : undefined;
};

export const SCREENS: [string, string, string[]?][] = [
  [routes.workspace.href(), 'Home', ['workspace', 'projects', 'start']],
  [routes.home.href(), 'Work', ['overview', 'project', 'tasks and chats', 'today', 'dashboard']],
  [routes.needsYou.href(), 'Inbox', ['needs you', 'approvals', 'running', 'done today']],
  // Running and My tasks are tabs of the Inbox (decision P140)
  [`${routes.needsYou.href()}?tab=running`, 'Running', ['inbox', 'in progress', 'working', 'scans']],
  [routes.myTasks.href(), 'My tasks', ['assigned', 'mine', 'inbox']],
  [routes.rooms.href(), 'Rooms', ['channels', 'chat', 'general']],
  [routes.knowledge.href(), 'Knowledge', ['requirements', 'packs', 'memory', 'repo map']],
  [routes.chats.href(), 'Chats', ['claude', 'conversation']],
  [routes.tasks.href(), 'Tasks'],
  [routes.team.href(), 'Crew', ['agents', 'team']],
  [routes.plan.href(), 'Plan with PlanTeam'],
  [routes.pulls.href(), 'Pull requests', ['pr', 'github']],
  [routes.requirements.href(), 'Requirements'],
  [routes.milestones.href(), 'Milestones'],
  [routes.memory.href(), 'Memory'],
  [routes.repoMap.href(), 'Repo map', ['symbols', 'code map', 'tree-sitter', 'pagerank', 'definitions']],
  [routes.skills.href(), 'Project skills', ['skills']],
  [routes.librarySkills.href(), 'Skill library', ['skills', 'registry', 'registries', 'library', 'agents']],
  [routes.packs.href(), 'Expert packs'],
  [routes.budgets.href(), 'Budgets'],
  [routes.evals.href(), 'Evals'],
  [routes.activity.href(), 'Activity'],
  [routes.security.href(), 'Project security', ['secrets', 'vulnerabilities', 'gitleaks', 'osv', 'opengrep']],
  [routes.securityScan.href(), 'Security scan of any repository', ['scan', 'audit', 'repository', 'download', 'import', 'secrets', 'vulnerabilities']],
  [routes.project.href(), 'Project settings'],
  [routes.projectIntegrations.href(), 'Project integrations', ['mcp servers', 'grants', 'approvals', 'app sync', 'assignments']],
  // the Agents library (decision P140): My agents (the templates), Starters, Skills (above) and every project's crew
  [routes.library.href(), 'My agents', ['agents', 'agent templates', 'templates', 'library', 'new agent']],
  [routes.libraryStarters.href(), 'Starter agents', ['agents', 'prebuilt', 'gallery', 'starters', 'library']],
  [routes.crew.href(), 'All crews', ['agents', 'every project', 'crew', 'team']],
  [routes.integrations.href(), 'Integrations', ['mcp', 'connectors', 'agent tools', 'servers', 'linear', 'notion', 'sentry', 'aikido']],
  // the agent CLIs, a section of Settings (decision P140; once the page Agents)
  [routes.agents.href(), 'Agent CLIs', ['agents', 'connections', 'agent cli', 'claude code', 'codex', 'gemini', 'sign in', 'wsl', 'mcp gateway']],
  [routes.github.href(), 'GitHub', ['gh', 'sign-in', 'pull requests', 'runners', 'repository', 'repositories', 'ci']],
  [routes.models.href(), 'Models', ['providers', 'api keys', 'openrouter', 'endpoints', 'models on this pc', 'local models', 'gguf', 'hugging face', 'gpu', 'kv cache', 'turboquant', 'engine']],
  [routes.routing.href(), 'Model routing', ['routing and budgets', 'rules', 'turn limits']],
  [routes.backups.href(), 'Backups'],
  [routes.data.href(), 'Export and import'],
  [routes.support.href(), 'Support bundle'],
  [routes.license.href(), 'License'],
  [routes.settings.href(), 'Settings', ['general', 'theme', 'preferences', 'telemetry', 'diagnostics', 'updates']],
  [routes.settingsShortcuts.href(), 'Keyboard shortcuts', ['keys', 'shortcuts', 'bindings', 'hotkeys']],
  [routes.settingsAbout.href(), 'Help and about', ['about', 'version', 'licenses', 'help', 'session']],
  [routes.guide.href(), 'Guide', ['help', 'documentation', 'how to', 'learn']],
  // every Guide topic is found by its title and keywords: "how do I scan a repository" opens the topic
  ...GUIDE_TOPICS.map((g): [string, string, string[]] => [topicPath(g.id), `Guide: ${g.title}`, ['help', ...(g.keywords ?? [])]]),
];

/** A settings page: the gear's pages and the project's own settings (listed by ">"). */
function isSettingsPage(href: string): boolean {
  const r = parseRoute(href);
  return hubOf(r) === 'settings' || r.name === 'project' || r.name === 'project-integrations';
}

/** How many recent chats the palette offers (and asks the core for). */
export const PALETTE_CHATS = 8;

/** The Chats screen is open (the list, or a conversation): New chat opens its dialog there, over what is shown. */
export function isChatsPath(pathname: string): boolean {
  const name = parseRoute(pathname).name;
  return name === 'chats' || name === 'chat';
}

/** The words for the team lead's role (internal/pipeline/roles.go roleSynonyms, the same list). */
const LEAD_ROLES = ['lead', 'team lead', 'teamlead', 'manager', 'supervisor', 'engineering manager'];

/** The agent's role names the team lead (pipeline.IsLeadRole): it supervises and is never a task's worker. */
export function isLeadRole(role: string): boolean {
  const n = role.toLowerCase().trim().replace(/[_-]/g, ' ').split(/\s+/).filter(Boolean).join(' ');
  return LEAD_ROLES.includes(n);
}

/** The team's lead (pipeline.FindLead: the first agent whose role is a lead role). */
export const findLead = (agents: readonly Agent[]): Agent | undefined => agents.find((a) => isLeadRole(a.role));

/** A task's identifier: "GOC-41" with the project's key, "#41" without one (an older core). */
export const taskKey = (seq: number, projectKey?: string): string => (projectKey ? `${projectKey}-${seq}` : `#${seq}`);

/** The task the menu acts on, and what it needs to name the options for it. */
export interface TaskContext {
  task: Pick<Task, 'id' | 'seq' | 'title' | 'description' | 'projectId'>;
  projectName: string;
  /** The project has a repository to scan. */
  hasRepo: boolean;
  /** app.info's human ("human:kyle"): Assign to me. Absent until it is read. */
  human?: string;
  /** The agents whose provider is the server of the models on this PC. */
  localIds: ReadonlySet<string>;
  /** Where an agent's CLI runs, in words ("Claude Code WSL"). */
  runtime: (a: Agent) => string;
  /** The agent CLI a chat about the task starts with ("Claude Code"); absent when none can start one. */
  askCli?: string;
}

export interface BuildOptions {
  projectKey?: string;
  context?: TaskContext;
}

/** The options about the task in context: Assign, then Actions. */
export function contextItems(agents: readonly Agent[], c: TaskContext, projectKey?: string): PaletteItem[] {
  const out: PaletteItem[] = [];
  const key = taskKey(c.task.seq, projectKey);
  const lead = findLead(agents);
  const workers = agents.filter((a) => !isLeadRole(a.role));
  const assign = (a: Agent, hint: string): PaletteItem => ({
    id: `assign:${a.id}`,
    group: 'Assign',
    label: `Assign to ${a.name}`,
    hint,
    keywords: ['assign', a.name, a.role],
    op: { kind: 'assign', assignee: a.id, agentId: a.id },
    scope: '@',
    context: true,
    agent: { id: a.id, name: a.name },
  });
  for (const a of workers.filter((a) => !c.localIds.has(a.id))) out.push(assign(a, c.runtime(a)));
  for (const a of workers.filter((a) => c.localIds.has(a.id))) out.push(assign(a, 'Models on this PC'));
  if (lead) {
    out.push({
      id: 'split',
      group: 'Assign',
      label: 'Ask the team lead to split it',
      keywords: ['split', 'lead', lead.name, 'break down'],
      op: { kind: 'split' },
      context: true,
      keys: ['⇧', 'S'],
      agent: { id: lead.id, name: lead.name },
      what: `${lead.name} reads ${key} and splits it into smaller tasks in the backlog, or says why it should stay whole.`,
    });
  }
  if (c.human) {
    out.push({ id: 'assign:me', group: 'Assign', label: 'Assign to me', keywords: ['assign', 'me', 'myself', 'take'], op: { kind: 'assign', assignee: c.human }, context: true, keys: ['I'] });
  }
  if (c.hasRepo) {
    out.push({
      id: 'scan',
      group: 'Actions',
      label: `Scan ${c.projectName} for secrets, packages and unsafe code`,
      keywords: ['security', 'scan', 'secrets', 'vulnerabilities'],
      op: { kind: 'scan' },
      scope: '>',
      context: true,
      what: `Scans ${c.projectName}'s repository on this PC; the findings show on Project security.`,
    });
  }
  if (c.askCli) {
    out.push({
      id: 'ask',
      group: 'Actions',
      label: `Ask ${c.askCli} about this task`,
      keywords: ['chat', 'ask', 'question'],
      op: { kind: 'ask' },
      scope: '>',
      context: true,
      what: `Starts a chat with ${c.askCli} in ${c.projectName}, sends ${key}'s title and description as the first message, and opens it.`,
    });
  }
  return out;
}

export function buildItems(rooms: readonly Room[], tasks: readonly Task[], agents: readonly Agent[], chats: readonly ChatListItem[] = [], opts: BuildOptions = {}): PaletteItem[] {
  const out: PaletteItem[] = [];
  const { projectKey, context } = opts;
  if (context) out.push(...contextItems(agents, context, projectKey));
  for (const r of rooms.filter((r) => !r.archived)) {
    const dm = r.kind === 'dm';
    const name = dm ? (agents.find((a) => a.id === r.name.slice(r.name.lastIndexOf('+') + 1))?.name ?? r.name) : r.name;
    out.push({ id: `room:${r.id}`, group: 'Navigate', label: dm ? `Direct message ${name}` : `#${name}`, hint: dm ? 'direct message' : r.kind, href: routes.room.href({ id: r.id }), what: dm ? `Opens your direct messages with ${name}.` : `Opens the room ${name}.` });
  }
  for (const [href, label, keywords] of SCREENS) {
    const keys = chordOf(href);
    // with a task in context, the screens with a G shortcut are the menu's "Go to"
    out.push({ id: `screen:${href}`, group: keys && context ? 'Go to' : 'Navigate', label, href, keywords, keys, context: keys && context ? true : undefined, scope: isSettingsPage(href) ? '>' : undefined, what: `Goes to ${label}.` });
  }
  out.push({ id: 'chat:new', group: 'Chats', label: 'New chat', keywords: ['claude', 'conversation', 'ask'], action: 'chat:new', scope: '>', what: 'Opens the New chat dialog on Chats.' });
  for (const c of chats.filter((c) => !c.archived).slice(0, PALETTE_CHATS)) {
    out.push({ id: `chat:${c.id}`, group: 'Chats', label: chatTitle(c), hint: c.status === 'running' ? 'running' : 'chat', keywords: ['chat'], href: routes.chat.href({ id: c.id }), what: 'Opens the chat.' });
  }
  out.push({ id: 'task:new', group: 'Tasks', label: 'Create a task', href: routes.taskNew.href(), scope: '#', what: 'Opens the form for a new task.' });
  for (const t of tasks.slice(0, 200)) {
    const key = taskKey(t.seq, projectKey);
    out.push({ id: `task:${t.id}`, group: 'Tasks', label: `${key} ${t.title}`, hint: t.status, keywords: [String(t.seq), key, t.status], href: routes.task.href({ id: t.id }), scope: '#', what: `Opens ${key}.` });
    if (t.runId) out.push({ id: `run:${t.id}`, group: 'Tasks', label: `Workflow of ${key} ${t.title}`, keywords: ['graph', 'explain', 'run', key], href: routes.run.href({ id: t.id }), scope: '#', what: `Opens the workflow graph of ${key}.` });
  }
  for (const a of agents) {
    const agent = { id: a.id, name: a.name };
    out.push({ id: `agent:${a.id}`, group: 'Agents', label: `Open ${a.name}'s page`, hint: a.role, keywords: [a.name, 'agent', 'crew'], href: routes.agent.href({ id: a.id }), scope: '@', agent, what: `Goes to ${a.name}'s page on Crew.` });
    out.push({ id: `inspect:${a.id}`, group: 'Agents', label: `Inspect ${a.name}`, hint: a.role, action: `inspect:${a.id}`, scope: '@', agent, what: `Shows ${a.name} in the details panel.` });
    out.push({ id: `dm:${a.id}`, group: 'Agents', label: `Message ${a.name}`, hint: a.role, action: `dm:${a.id}`, scope: '@', agent, what: `Opens your direct messages with ${a.name}.` });
  }
  const act = (id: string, label: string, action: string, what: string, extra: Partial<PaletteItem> = {}): PaletteItem => ({ id, group: 'Actions', label, action, scope: '>', what, ...extra });
  out.push(
    act('act:terminal', 'Open a terminal', 'dock:terminal', 'Opens a terminal in the dock, in the project folder.', { keywords: ['shell', 'console'] }),
    act('act:diff', 'Show the diff of the current task', 'dock:diff', "Opens the dock on the task's changes.", { hint: 'Ctrl+Shift+D' }),
    act('act:logs', 'Show the logs of the current task', 'dock:logs', "Opens the dock on the task's logs."),
    act('act:activity', 'Show model and token activity', 'dock:activity', 'Opens the dock on the model calls and their tokens.'),
    act('act:dock', 'Toggle the dock', 'dock:toggle', 'Shows or hides the dock under the page.', { hint: 'Ctrl+J' }),
    act('act:details', 'Toggle the details panel', 'details:toggle', 'Shows or hides the details panel beside the page.'),
    { id: 'act:project', group: 'Actions', label: 'Add a project', href: routes.projectNew.href(), scope: '>', what: 'Opens the form for a new project.' },
    act('act:theme-system', 'Theme: follow the system', 'theme:system', 'Follows the light or dark setting of Windows.'),
    act('act:theme-dark', 'Theme: dark', 'theme:dark', 'Switches Drydock to the dark theme.'),
    act('act:theme-light', 'Theme: light', 'theme:light', 'Switches Drydock to the light theme.'),
  );
  return out;
}

/** A query's prefix (">" settings and actions, "#" tasks, "@" agents) and the words after it. */
export function parseQuery(query: string): { scope?: Scope; text: string } {
  const q = query.trimStart();
  const c = q[0];
  if (c === '>' || c === '#' || c === '@') return { scope: c, text: q.slice(1).trim() };
  return { text: q.trim() };
}

/**
 * Rank an item for a query: 0 = no match. Exact label beats a prefix, a prefix beats the start of a word, a word start
 * beats a substring, and a substring beats letters in order. Keywords count a little less than the label.
 */
export function score(item: PaletteItem, query: string): number {
  const q = query.trim().toLowerCase();
  if (!q) return 1;
  const one = (text: string): number => {
    const s = text.toLowerCase();
    if (s === q) return 1000;
    if (s.startsWith(q)) return 800 - Math.min(s.length, 200);
    const words = s.split(/[\s#/:._-]+/);
    if (words.some((w) => w.startsWith(q))) return 600 - Math.min(s.length, 200);
    const i = s.indexOf(q);
    if (i >= 0) return 400 - i;
    let j = 0;
    for (const ch of s) if (ch === q[j]) j++;
    return j === q.length ? 100 : 0;
  };
  let best = one(item.label);
  for (const k of item.keywords ?? []) best = Math.max(best, one(k) * 0.8);
  return best;
}

/** How many options of each group the menu lists before anything is typed (without a task in context). */
export const PER_GROUP = 12;

/**
 * The matching items, best first. A prefix keeps only its kind (">" settings pages and actions, "#" tasks, "@" agents). With
 * nothing typed: the options about the task in context when there is one, else the first PER_GROUP of each group.
 */
export function filterItems(items: PaletteItem[], query: string, limit = 60): PaletteItem[] {
  const { scope, text } = parseQuery(query);
  const pool = scope ? items.filter((i) => i.scope === scope) : items;
  if (!text) {
    if (scope) return pool.slice(0, limit);
    if (pool.some((i) => i.context)) return pool.filter((i) => i.context).slice(0, limit);
    // a few of each group, so every group is in reach before anything is typed
    const per = new Map<Group, number>();
    return pool.filter((i) => {
      const n = per.get(i.group) ?? 0;
      per.set(i.group, n + 1);
      return n < PER_GROUP;
    });
  }
  return pool
    .map((it, i) => ({ it, s: score(it, text), i }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s || a.i - b.i)
    .slice(0, limit)
    .map((x) => x.it);
}

/**
 * The shown items under their group headings, in the order the keyboard moves through them. With nothing typed the groups
 * come in their fixed order; with a query, a group comes where its best match ranks, so the first option is the best one.
 */
export function arrange(shown: readonly PaletteItem[], query: string): { group: Group; items: PaletteItem[] }[] {
  const { scope, text } = parseQuery(query);
  let order: Group[];
  if (text || scope) {
    order = [];
    for (const it of shown) if (!order.includes(it.group)) order.push(it.group);
  } else {
    order = shown.some((i) => i.context) ? CONTEXT_GROUPS : GROUPS;
  }
  return order.map((group) => ({ group, items: shown.filter((i) => i.group === group) })).filter((g) => g.items.length > 0);
}

/** A check kind in the words of the "What happens" panel ("tests", "types"); an unknown kind is shown as it is. */
const CHECK_WORDS: Record<string, string> = { test: 'tests', typecheck: 'types', deps: 'deps', secrets: 'secrets', lint: 'lint', build: 'build', e2e: 'end-to-end tests', ci: 'CI' };

/** "a, b and c". */
export function joinWords(words: readonly string[]): string {
  if (words.length <= 1) return words[0] ?? '';
  return `${words.slice(0, -1).join(', ')} and ${words[words.length - 1]}`;
}

/** One step of what an assignment does; `code` is shown in the mono font after `text`. */
export interface PreviewStep {
  text: string;
  code?: string;
}

/** What assigning does, step by step, from the core's preview (task.assignPreview). */
export function previewSteps(p: AssignPreview): PreviewStep[] {
  const out: PreviewStep[] = [];
  const name = p.agent?.name ?? '';
  if (p.person) {
    out.push({ text: p.stopsRun ? 'Stops the run that is going on; the worktree stays' : 'No agent works on it: it is yours on the board' });
    out.push({ text: 'Its branch stays ', code: p.branch });
    return out;
  }
  if (p.stopsRun) out.push({ text: `Stops the current turn; ${name} takes over in the same worktree` });
  out.push(p.hasWorktree ? { text: 'Goes on in its worktree on ', code: p.branch } : { text: 'Starts in its own worktree on ', code: p.branch });
  if (p.specGate) out.push({ text: 'Writes the spec first; you accept it at the spec gate' });
  if (p.checks.length) out.push({ text: `Runs ${joinWords(p.checks.map((c) => CHECK_WORDS[c] ?? c))} before review` });
  if (p.pushes && p.asksBeforePush) out.push({ text: 'Asks you before it pushes or merges' });
  else if (p.pushes) out.push({ text: 'Pushes after the review; asks you before it merges' });
  else if (p.asksBeforeMerge) out.push({ text: 'Asks you before it merges' });
  return out;
}

/** Why the core would refuse the assignment, or '' when it takes it (shown in the panel's box instead of the queue). */
export function previewBlock(p: AssignPreview): string {
  if (p.person || !p.agent || p.reviewer) return '';
  return `Nobody else on the team can review ${p.agent.name}'s work: add another agent first.`;
}

/** The agent's queue and why the task would wait, or '' when it starts at once (the panel's box). */
export function previewWait(p: AssignPreview, projectKey?: string): string {
  if (!p.waits || p.person) return '';
  const name = p.agent?.name ?? 'The agent';
  const busy = p.queue.filter((q) => q.status !== 'READY' && q.status !== 'BACKLOG').length;
  const queue = busy > 0 ? `${name} has ${busy} ${busy === 1 ? 'task' : 'tasks'} in progress. ` : '';
  if (p.waitsFor) return `${queue}This one waits until ${taskKey(p.waitsFor.seq, projectKey)} is finished: both change the same code.`;
  return `${queue}This one waits for a free slot: ${p.waitReason || 'the project runs as many agents as it allows'}.`;
}
