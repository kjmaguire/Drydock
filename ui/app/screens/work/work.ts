// Work (decision P141): a project's tasks and chats as one list, grouped Needs you / Running / Earlier, each row one line of
// title and one faint line ("Ready to merge · +42 −3", "Ada · 4 min", "Merged · yesterday", "Chat · Tue"). Pure functions of the
// reads the screen already has (the project's task list, its Needs-you items and its chats), so the grouping, the order, the
// search and the item opened by default are tested without a page.

import type { Chat } from '../../api/chatClient.ts';
import type { NeedsYouItem, Task } from '../../api/types.ts';
import { workItemKey, type WorkItemRef } from '../../lib/route.ts';
import { listWhen } from '../chats/chats.ts';
import { KIND_LABEL } from '../needs-you/needs-you.ts';
import { ms } from '../overview/overview.ts';

export type WorkGroup = 'needs' | 'running' | 'earlier';
/** The row's dot: amber waits on you, accent runs, hollow has not started, teal merged, red failed, grey otherwise. */
export type WorkTone = 'needs' | 'running' | 'idle' | 'done' | 'failed' | 'quiet';

export interface WorkRow {
  ref: WorkItemRef;
  /** "task:<id>" or "chat:<id>", the ?item= value. */
  key: string;
  title: string;
  group: WorkGroup;
  tone: WorkTone;
  /** The faint line under the title. */
  line: string;
  /** When it last changed (ms), for the order. */
  at: number;
  /** The most urgent Needs-you item's severity (lower is more urgent); 99 for a row that needs nothing. */
  severity: number;
}

export const GROUP_TITLE: Record<WorkGroup, string> = { needs: 'Needs you', running: 'Running', earlier: 'Earlier' };

const RUNNING = new Set(['CLAIMED', 'IN_PROGRESS', 'REVIEW', 'VERIFICATION']);
const DONE_WORDS: Record<string, string> = { COMPLETED: 'Merged', FAILED: 'Failed', CANCELLED: 'Cancelled' };

export type WorkChat = Pick<Chat, 'id' | 'title' | 'status' | 'updatedAt' | 'createdAt'>;

export interface WorkInput {
  tasks: readonly Task[];
  chats: readonly WorkChat[];
  needs: readonly NeedsYouItem[];
  /** An agent id's name ("" when unknown). */
  agentName?: (id: string) => string;
  now?: number;
}

/** What a Needs-you item asks, in a word or two, with the change's size when it has one: "Ready to merge · +42 −3". */
export function needLine(it: NeedsYouItem): string {
  const what =
    it.kind === 'review' && it.phase === 'merge' ? 'Ready to merge' : it.kind === 'review' && it.phase === 'spec' ? 'Accept the spec' : it.kind === 'chat_permission' ? 'Asks to use a tool' : (KIND_LABEL[it.kind] ?? 'Needs you');
  const c = it.context;
  return c?.hasDiff ? `${what} · +${c.insertions} −${c.deletions}` : what;
}

const join = (...parts: string[]) => parts.filter(Boolean).join(' · ');

/** Every task and chat of the project as a row, in the list's order: Needs you by urgency, then each group newest first. */
export function workRows(input: WorkInput): WorkRow[] {
  const now = input.now ?? Date.now();
  const name = input.agentName ?? (() => '');
  const needOf = new Map<string, NeedsYouItem>();
  for (const it of input.needs) {
    const k = it.taskId ? `task:${it.taskId}` : it.chatId ? `chat:${it.chatId}` : '';
    if (!k) continue;
    const had = needOf.get(k);
    if (!had || it.severity < had.severity) needOf.set(k, it);
  }
  const rows: WorkRow[] = [];
  for (const x of input.tasks) {
    const ref: WorkItemRef = { kind: 'task', id: x.id };
    const key = workItemKey(ref);
    const need = needOf.get(key);
    const at = ms(x.completed ?? x.updated ?? x.created);
    const when = listWhen(at, now);
    const who = x.assignee ? (x.assignee.startsWith('human:') ? x.assignee.slice(6) : name(x.assignee)) : '';
    let row: Pick<WorkRow, 'group' | 'tone' | 'line'>;
    if (need) row = { group: 'needs', tone: 'needs', line: needLine(need) };
    else if (x.status === 'WAITING_HUMAN') row = { group: 'needs', tone: 'needs', line: join('Waiting for you', when) };
    else if (DONE_WORDS[x.status]) row = { group: 'earlier', tone: x.status === 'COMPLETED' ? 'done' : x.status === 'FAILED' ? 'failed' : 'quiet', line: join(DONE_WORDS[x.status]!, when) };
    else if (RUNNING.has(x.status)) row = { group: 'running', tone: 'running', line: join(who || 'Running', when) };
    else if (x.status === 'BLOCKED') row = { group: 'running', tone: 'quiet', line: join('Blocked', when) };
    else row = { group: 'running', tone: 'idle', line: join(who ? `${who}, not started` : 'Not started', when) };
    rows.push({ ref, key, title: x.title, at, severity: need?.severity ?? 99, ...row });
  }
  for (const c of input.chats) {
    const ref: WorkItemRef = { kind: 'chat', id: c.id };
    const key = workItemKey(ref);
    const need = needOf.get(key);
    const at = ms(c.updatedAt || c.createdAt);
    const title = c.title || 'New chat';
    if (need) rows.push({ ref, key, title, at, severity: need.severity, group: 'needs', tone: 'needs', line: join(needLine(need), 'Chat') });
    else if (c.status === 'running') rows.push({ ref, key, title, at, severity: 99, group: 'running', tone: 'running', line: 'Chat · answering' });
    else rows.push({ ref, key, title, at, severity: 99, group: 'earlier', tone: c.status === 'error' ? 'failed' : 'quiet', line: join('Chat', c.status === 'error' ? 'failed' : '', listWhen(at, now)) });
  }
  const order: Record<WorkGroup, number> = { needs: 0, running: 1, earlier: 2 };
  return rows.sort((a, b) => order[a.group] - order[b.group] || (a.group === 'needs' ? a.severity - b.severity : 0) || b.at - a.at || a.key.localeCompare(b.key));
}

/** The rows of each group, in order (a group with none is an empty list). */
export function groupWork(rows: readonly WorkRow[]): Record<WorkGroup, WorkRow[]> {
  const out: Record<WorkGroup, WorkRow[]> = { needs: [], running: [], earlier: [] };
  for (const r of rows) out[r.group].push(r);
  return out;
}

/** The rows whose title or line holds every word of the search, ignoring case. */
export function filterWork(rows: readonly WorkRow[], query: string): WorkRow[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return [...rows];
  return rows.filter((r) => {
    const text = `${r.title} ${r.line}`.toLowerCase();
    return words.every((w) => text.includes(w));
  });
}

/**
 * The item the centre shows: the one the URL names (also when the list does not hold it, an older task for instance), else the
 * most relevant one: the first that needs you, else the first running. Undefined: nothing to show (the empty state).
 */
export function openItem(rows: readonly WorkRow[], named: WorkItemRef | undefined): WorkItemRef | undefined {
  if (named) return named;
  return (rows.find((r) => r.group === 'needs') ?? rows.find((r) => r.group === 'running' && r.tone === 'running'))?.ref;
}

/** An agent's model and effort in a few words: "Opus 5.5 · high", "Default model", "Sonnet". */
export function modelEffort(model: string | undefined, effort: string | undefined, modelName: (id: string) => string): string {
  const m = model?.trim() ? modelName(model) : 'Default model';
  return effort?.trim() ? `${m} · ${effort.trim()}` : m;
}
