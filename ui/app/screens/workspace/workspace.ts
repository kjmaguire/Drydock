// The plain text of the workspace Home (decision P142): the projects to pick up, the most recently active first.

import type { NeedsYouItem, Project, TaskSummary } from '../../api/types.ts';

/** A project's card under "Pick up where you left off": what runs, whether it needs you, and one line of what is happening. */
export interface PickUp {
  project: Project;
  running: number;
  needs: number;
  /** When something last happened (a task's update, else the project's creation), ms since the epoch. */
  last: number;
  /** One line: the oldest item that needs you, what a running task does, or the newest task's state. */
  line: string;
}

/**
 * The projects to pick up, the most recently active first (decision P142), from the workspace's own reads: every project's task
 * summaries (task.summaries with no project) and the items that need the person.
 */
export function pickUp(
  projects: readonly Project[],
  summaries: readonly Pick<TaskSummary, 'projectId' | 'status' | 'title' | 'updatedAt' | 'doing'>[],
  needs: readonly Pick<NeedsYouItem, 'projectId' | 'title'>[],
  running: readonly string[],
): PickUp[] {
  return projects
    .map((p) => {
      const mine = summaries.filter((x) => x.projectId === p.id).sort((a, b) => b.updatedAt - a.updatedAt);
      const run = mine.filter((x) => running.includes(x.status));
      const need = needs.filter((n) => n.projectId === p.id);
      const created = p.created > 1e12 ? p.created : p.created * 1000;
      const last = Math.max(created, mine[0]?.updatedAt ?? 0);
      const line = need[0]
        ? need[0].title
        : run[0]
          ? `${run[0].title}${run[0].doing ? `: ${run[0].doing}` : ''}`
          : mine[0]
            ? `Last: ${mine[0].title} (${mine[0].status.toLowerCase().replace(/_/g, ' ')})`
            : 'Nothing started yet.';
      return { project: p, running: run.length, needs: need.length, last, line };
    })
    .sort((a, b) => b.last - a.last);
}
