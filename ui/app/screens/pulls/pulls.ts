// The Pull requests page's issue list (C11): read once when the page opens and kept for a minute, filtered by label on the page,
// and the words of what an import did.

import type { GitHubIssue, Task, TaskCreateResult } from '../../api/types.ts';

/** How long a read of the open issues is shown again without asking GitHub (Refresh issues always asks). */
export const ISSUES_FRESH_MS = 60_000;

/** The labels typed in the filter, trimmed, lower-cased, empty ones dropped. */
export function labelList(text: string): string[] {
  return text
    .split(',')
    .map((l) => l.trim().toLowerCase())
    .filter(Boolean);
}

/** Whether an issue carries every label typed (GitHub's own labels filter means all of them), ignoring case. */
export function hasLabels(issue: Pick<GitHubIssue, 'labels'>, wanted: readonly string[]): boolean {
  const have = new Set((issue.labels ?? []).map((l) => l.toLowerCase()));
  return wanted.every((l) => have.has(l));
}

/** The task an issue was already imported as (its issue reference is "#<number>"), newest first in the list given. */
export function importedTask<T extends Pick<Task, 'issueRef'>>(tasks: readonly T[], n: number): T | undefined {
  return tasks.find((x) => x.issueRef === `#${n}`);
}

/** What an import did, in words: started with the pair, made but not started (and why), or a backlog task. */
export function importLine(n: number, task: Pick<TaskCreateResult, 'seq' | 'startError'>, started: { worker: string; reviewer: string } | undefined): string {
  if (task.startError) return `Issue #${n} is task #${task.seq}, but it did not start: ${task.startError}`;
  if (started) return `Issue #${n} is task #${task.seq}. ${started.worker} builds it and ${started.reviewer} reviews it; it has started.`;
  return `Issue #${n} is task #${task.seq} in the backlog.`;
}
