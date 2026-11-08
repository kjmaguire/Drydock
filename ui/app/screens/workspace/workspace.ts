// The plain text of the workspace Home (decision P142): the projects to pick up, the most recently active first.

import type { Chat } from '../../api/chatClient.ts';
import type { NeedsYouItem, Project, TaskSummary } from '../../api/types.ts';
import { relTime } from '../chats/chats.ts';

/** A project's card under "Pick up where you left off": what runs, whether it needs you, and one line of what is happening. */
export interface PickUp {
  project: Project;
  running: number;
  needs: number;
  /** When something last happened (a task's update, else the project's creation), ms since the epoch. */
  last: number;
  /** One line: the oldest item that needs you, what a running task does, or the newest task's state or chat. */
  line: string;
}

const ms = (n: number): number => (n > 1e12 ? n : n * 1000);

/**
 * The projects to pick up, the most recently active first (decision P142), from the workspace's own reads: every project's task
 * summaries (task.summaries with no project), the items that need the person and the chats (a project with only a chat in it was
 * "Nothing started yet", critique 2026-11-08).
 */
export function pickUp(
  projects: readonly Project[],
  summaries: readonly Pick<TaskSummary, 'projectId' | 'status' | 'title' | 'updatedAt' | 'doing'>[],
  needs: readonly Pick<NeedsYouItem, 'projectId' | 'title'>[],
  running: readonly string[],
  chats: readonly Pick<Chat, 'projectId' | 'title' | 'updatedAt'>[] = [],
): PickUp[] {
  return projects
    .map((p) => {
      const mine = summaries.filter((x) => x.projectId === p.id).sort((a, b) => b.updatedAt - a.updatedAt);
      const run = mine.filter((x) => running.includes(x.status));
      const need = needs.filter((n) => n.projectId === p.id);
      const chat = chats.filter((c) => c.projectId === p.id).sort((a, b) => ms(b.updatedAt) - ms(a.updatedAt))[0];
      const created = ms(p.created);
      const taskAt = mine[0]?.updatedAt ?? 0;
      const chatAt = chat ? ms(chat.updatedAt) : 0;
      const last = Math.max(created, taskAt, chatAt);
      const line = need[0]
        ? need[0].title
        : run[0]
          ? `${run[0].title}${run[0].doing ? `: ${run[0].doing}` : ''}`
          : chat && chatAt >= taskAt
            ? `Chat: ${chat.title || 'New chat'}`
            : mine[0]
              ? `Last: ${mine[0].title} (${mine[0].status.toLowerCase().replace(/_/g, ' ')})`
              : 'Nothing started yet.';
      return { project: p, running: run.length, needs: need.length, last, line };
    })
    .sort((a, b) => b.last - a.last);
}

/** A project's pill: "needs you", "N running" or when something last happened; the cards and the Where menu's Recent say it alike. */
export function pickPill(item: Pick<PickUp, 'needs' | 'running' | 'last'>, now = Date.now()): { tone: '' | 'warn' | 'run'; text: string } {
  if (item.needs > 0) return { tone: 'warn', text: 'needs you' };
  if (item.running > 0) return { tone: 'run', text: `${item.running} running` };
  return { tone: '', text: relTime(item.last, now) };
}
