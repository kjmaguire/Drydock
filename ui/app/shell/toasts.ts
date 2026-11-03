// What a toast says (decision G2), as pure functions the shell's <Toasts/> host calls: a new Inbox item, a task that ended and
// a chat that answered, each only when the person is not already looking at it. A toast is one quiet line with one action
// (Open); the window's own notification (G1) is the core's and the shell's, not these.

import type { DrydockEvent, NeedsYouItem, TaskSummary } from '../api/types.ts';
import { routes } from '../routes.ts';
import { KIND_LABEL, needsTarget } from '../screens/needs-you/needs-you.ts';
import type { ToastInit } from '../state/ui.ts';

/**
 * The Inbox's items seen so far, to tell which are new. The first list seeds it and says nothing (what waited before the
 * window opened is the Inbox's count, not news), and so does the first list read after the stream reopens (epoch): what
 * arrived while the core was out of reach is in the count, and a burst of toasts for it would be noise.
 */
export class NeedsDiff {
  #seen: Set<string> | undefined;
  #epoch = -1;

  /** The items of `items` not seen before; [] for the list that seeds it. */
  next(items: readonly NeedsYouItem[], epoch: number): NeedsYouItem[] {
    const keys = new Set(items.map((it) => it.key));
    const seed = this.#seen === undefined || epoch !== this.#epoch;
    const fresh = seed ? [] : items.filter((it) => !this.#seen!.has(it.key));
    this.#seen = keys; // an item answered and asked again is new again
    this.#epoch = epoch;
    return fresh;
  }
}

/** Where the person is: the toast for what they are looking at is not shown. */
export interface Here {
  /** The Inbox is open: it lists every new item itself. */
  inbox: boolean;
  /** The task of the page (its own page, its run, Work's open task, its channel). */
  taskId?: string;
  /** The chat open. */
  chatId?: string;
}

/** The project and task a toast names: never the item's own words (a request's text can hold a command or a prompt). */
export interface Where {
  projectName?: string;
  task?: Pick<TaskSummary, 'projectKey' | 'seq' | 'title'>;
}

function taskName(task: Where['task']): string {
  if (!task) return '';
  const key = task.projectKey && task.seq ? `${task.projectKey}-${task.seq}` : '';
  return [key, task.title].filter(Boolean).join(' ');
}

/** A new Inbox item: its kind, its project and task; Open goes where it is answered. */
export function needsToast(it: NeedsYouItem, here: Here, where: Where = {}): ToastInit | undefined {
  if (here.inbox) return undefined;
  if (it.taskId && it.taskId === here.taskId) return undefined;
  if (it.chatId && it.chatId === here.chatId) return undefined;
  const kind = KIND_LABEL[it.kind] ?? 'Something';
  return {
    text: `Needs you: ${kind.toLowerCase()}`,
    detail: [where.projectName, taskName(where.task)].filter(Boolean).join(' · ') || undefined,
    tone: 'warn',
    action: { label: 'Open', href: needsTarget(it) },
  };
}

const ENDED: Record<string, { text: string; tone: ToastInit['tone'] }> = {
  'task.completed': { text: 'Task finished', tone: 'ok' },
  'task.failed': { text: 'Task failed', tone: 'warn' },
  'task.cancelled': { text: 'Task cancelled', tone: 'info' },
};

/** A task that ended elsewhere: its key and title when the window knows them. */
export function taskToast(e: DrydockEvent, here: Here, task?: Where['task']): ToastInit | undefined {
  const ended = ENDED[e.type];
  if (!ended || !e.taskId || e.taskId === here.taskId) return undefined;
  return {
    text: ended.text,
    detail: taskName(task) || undefined,
    tone: ended.tone,
    action: { label: 'Open', href: routes.task.href({ id: e.taskId }) },
  };
}

/** A chat that answered (or failed) while the person was elsewhere; a turn they stopped says nothing. Its title is not shown: a
 * chat is often named after its first prompt. */
export function chatToast(e: DrydockEvent, here: Here, projectName?: string): ToastInit | undefined {
  if (e.type !== 'chat.turn') return undefined;
  const d = (e.data ?? {}) as { chatId?: unknown; status?: unknown };
  const chatId = typeof d.chatId === 'string' ? d.chatId : '';
  if (!chatId || chatId === here.chatId) return undefined;
  if (d.status !== 'done' && d.status !== 'error') return undefined;
  return {
    text: d.status === 'done' ? 'A chat answered' : 'A chat turn failed',
    detail: projectName || undefined,
    tone: d.status === 'done' ? 'ok' : 'warn',
    action: { label: 'Open', href: routes.chat.href({ id: chatId }) },
  };
}
