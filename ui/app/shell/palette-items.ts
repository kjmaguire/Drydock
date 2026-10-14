// What the command palette offers and how it ranks a query. Navigation, tasks, agents and harmless actions only: nothing
// here approves, merges, deletes or stops anything, so the palette can never bypass an approval.

import type { Agent, Room, Task } from '../api/types.ts';
import { chatTitle, type ChatListItem } from '../api/chatClient.ts';
import { parseRoute } from '../lib/route.ts';
import { routes } from '../routes.ts';

export type Group = 'Navigate' | 'Chats' | 'Tasks' | 'Agents' | 'Actions';
export const GROUPS: Group[] = ['Navigate', 'Chats', 'Tasks', 'Agents', 'Actions'];

export interface PaletteItem {
  id: string;
  group: Group;
  label: string;
  hint?: string;
  keywords?: string[];
  /** Either a path to go to or an action. */
  href?: string;
  action?: string;
}

export const SCREENS: [string, string, string[]?][] = [
  [routes.workspace.href(), 'Home', ['workspace', 'projects', 'start']],
  [routes.needsYou.href(), 'Needs you', ['inbox', 'approvals']],
  [routes.chats.href(), 'Chats', ['claude', 'conversation']],
  [routes.tasks.href(), 'Tasks'],
  [routes.team.href(), 'Team', ['agents']],
  [routes.plan.href(), 'Plan with PlanTeam'],
  [routes.pulls.href(), 'Pull requests', ['pr', 'github']],
  [routes.requirements.href(), 'Requirements'],
  [routes.milestones.href(), 'Milestones'],
  [routes.memory.href(), 'Memory'],
  [routes.skills.href(), 'Project skills', ['skills']],
  [routes.librarySkills.href(), 'Skill library', ['skills', 'registry', 'registries']],
  [routes.packs.href(), 'Expert packs'],
  [routes.budgets.href(), 'Budgets'],
  [routes.evals.href(), 'Evals'],
  [routes.activity.href(), 'Activity'],
  [routes.security.href(), 'Security scan', ['secrets', 'vulnerabilities', 'gitleaks', 'osv', 'opengrep']],
  [routes.project.href(), 'Project settings'],
  [routes.projectIntegrations.href(), 'Project integrations', ['mcp servers', 'grants', 'approvals', 'app sync', 'assignments']],
  [routes.library.href(), 'Library: agents and MCP servers', ['templates', 'prebuilt']],
  [routes.integrations.href(), 'Integrations', ['mcp', 'connectors', 'agent tools', 'servers']],
  [routes.connections.href(), 'Connections'],
  [routes.providers.href(), 'Providers'],
  [routes.routing.href(), 'Model routing'],
  [routes.backups.href(), 'Backups'],
  [routes.data.href(), 'Export and import'],
  [routes.support.href(), 'Support bundle'],
  [routes.license.href(), 'License'],
  [routes.settings.href(), 'Settings and About', ['theme', 'preferences']],
];

/** How many recent chats the palette offers (and asks the core for). */
export const PALETTE_CHATS = 8;

/** The Chats screen is open (the list, or a conversation): New chat opens its dialog there, over what is shown. */
export function isChatsPath(pathname: string): boolean {
  const name = parseRoute(pathname).name;
  return name === 'chats' || name === 'chat';
}

export function buildItems(rooms: readonly Room[], tasks: readonly Task[], agents: readonly Agent[], chats: readonly ChatListItem[] = []): PaletteItem[] {
  const out: PaletteItem[] = [];
  for (const r of rooms.filter((r) => !r.archived)) {
    const dm = r.kind === 'dm';
    const name = dm ? (agents.find((a) => a.id === r.name.slice(r.name.lastIndexOf('+') + 1))?.name ?? r.name) : r.name;
    out.push({ id: `room:${r.id}`, group: 'Navigate', label: dm ? `Direct message ${name}` : `#${name}`, hint: dm ? 'direct message' : r.kind, href: routes.room.href({ id: r.id }) });
  }
  for (const [href, label, keywords] of SCREENS) out.push({ id: `screen:${href}`, group: 'Navigate', label, href, keywords });
  out.push({ id: 'chat:new', group: 'Chats', label: 'New chat', keywords: ['claude', 'conversation', 'ask'], action: 'chat:new' });
  for (const c of chats.filter((c) => !c.archived).slice(0, PALETTE_CHATS)) {
    out.push({ id: `chat:${c.id}`, group: 'Chats', label: chatTitle(c), hint: c.status === 'running' ? 'running' : 'chat', keywords: ['chat'], href: routes.chat.href({ id: c.id }) });
  }
  out.push({ id: 'task:new', group: 'Tasks', label: 'Create a task', href: routes.taskNew.href() });
  for (const t of tasks.slice(0, 200)) {
    out.push({ id: `task:${t.id}`, group: 'Tasks', label: `#${t.seq} ${t.title}`, hint: t.status, keywords: [String(t.seq), t.status], href: routes.task.href({ id: t.id }) });
    if (t.runId) out.push({ id: `run:${t.id}`, group: 'Tasks', label: `Workflow of #${t.seq} ${t.title}`, keywords: ['graph', 'explain', 'run'], href: routes.run.href({ id: t.id }) });
  }
  for (const a of agents) {
    out.push({ id: `inspect:${a.id}`, group: 'Agents', label: `Inspect ${a.name}`, hint: a.role, action: `inspect:${a.id}` });
    out.push({ id: `dm:${a.id}`, group: 'Agents', label: `Message ${a.name}`, hint: a.role, action: `dm:${a.id}` });
  }
  out.push(
    { id: 'act:terminal', group: 'Actions', label: 'Open a terminal', keywords: ['shell', 'console'], action: 'dock:terminal' },
    { id: 'act:diff', group: 'Actions', label: 'Show the diff of the current task', hint: 'Ctrl+Shift+D', action: 'dock:diff' },
    { id: 'act:logs', group: 'Actions', label: 'Show the logs of the current task', action: 'dock:logs' },
    { id: 'act:activity', group: 'Actions', label: 'Show model and token activity', action: 'dock:activity' },
    { id: 'act:dock', group: 'Actions', label: 'Toggle the dock', hint: 'Ctrl+J', action: 'dock:toggle' },
    { id: 'act:details', group: 'Actions', label: 'Toggle the details panel', action: 'details:toggle' },
    { id: 'act:project', group: 'Actions', label: 'Add a project', href: routes.projectNew.href() },
    { id: 'act:theme-system', group: 'Actions', label: 'Theme: follow the system', action: 'theme:system' },
    { id: 'act:theme-dark', group: 'Actions', label: 'Theme: dark', action: 'theme:dark' },
    { id: 'act:theme-light', group: 'Actions', label: 'Theme: light', action: 'theme:light' },
  );
  return out;
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

/** The matching items, best first, grouped in GROUPS order when the query is empty. */
export function filterItems(items: PaletteItem[], query: string, limit = 60): PaletteItem[] {
  if (!query.trim()) return items.slice(0, limit);
  return items
    .map((it, i) => ({ it, s: score(it, query), i }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s || a.i - b.i)
    .slice(0, limit)
    .map((x) => x.it);
}
