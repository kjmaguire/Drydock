// Pure helpers for the Plan screen (PlanTeam): the dependency-closed selection, the edits that go with an accept, and where
// a plan is in the goal -> proposal -> review -> create flow. The planner's text is untrusted and never decides anything.

import type { Plan, ProposedTask } from '../../api/extTypes.ts';
import type { Tone } from '../../ui/tokens.ts';

/** Everything a task depends on, transitively, plus the task itself. */
export function closure(keys: Iterable<string>, tasks: readonly ProposedTask[]): Set<string> {
  const by = new Map(tasks.map((t) => [t.key, t]));
  const out = new Set<string>();
  const visit = (k: string) => {
    if (out.has(k) || !by.has(k)) return;
    out.add(k);
    for (const d of by.get(k)!.dependsOn ?? []) visit(d);
  };
  for (const k of keys) visit(k);
  return out;
}

/** Remove a task and everything that depends on it, so the selection stays dependency-closed. */
export function deselect(selected: ReadonlySet<string>, key: string, tasks: readonly ProposedTask[]): Set<string> {
  const out = new Set(selected);
  out.delete(key);
  let changed = true;
  while (changed) {
    changed = false;
    for (const t of tasks) {
      if (out.has(t.key) && (t.dependsOn ?? []).some((d) => !out.has(d))) {
        out.delete(t.key);
        changed = true;
      }
    }
  }
  return out;
}

/** Toggle one task: selecting brings in what it needs, clearing drops what needs it. */
export function toggle(selected: ReadonlySet<string>, key: string, on: boolean, tasks: readonly ProposedTask[]): Set<string> {
  return on ? closure([...selected, key], tasks) : deselect(selected, key, tasks);
}

/** Only the edits to tasks that are being created are sent. */
export function editsFor(selected: ReadonlySet<string>, edits: Readonly<Record<string, ProposedTask>>): Record<string, ProposedTask> {
  return Object.fromEntries(Object.entries(edits).filter(([k]) => selected.has(k)));
}

/** One item per non-empty line, trimmed. */
export const lines = (s: string): string[] =>
  s
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);

/** Newest first. */
export const newestFirst = (plans: readonly Plan[]): Plan[] => [...plans].sort((a, b) => b.createdAt - a.createdAt);

export const planTone = (status: string): Tone => (status === 'PROPOSED' ? 'warn' : status === 'ACCEPTED' ? 'ok' : status === 'FAILED' ? 'err' : 'muted');

export const STEPS = ['Goal', 'Proposal', 'Review', 'Create'] as const;

export type StepState = 'done' | 'current' | 'working' | 'failed' | 'todo';

/** The state of each step of the flow for the plan on screen (undefined: nothing proposed yet, so the goal is next). */
export function stepStates(plan: Plan | undefined): StepState[] {
  const s = plan?.status;
  if (!plan) return ['current', 'todo', 'todo', 'todo'];
  if (s === 'PLANNING') return ['done', 'working', 'todo', 'todo'];
  if (s === 'FAILED') return ['done', 'failed', 'todo', 'todo'];
  if (s === 'REJECTED') return ['done', 'done', 'failed', 'todo'];
  if (s === 'ACCEPTED') return ['done', 'done', 'done', 'done'];
  return ['done', 'done', 'current', 'todo'];
}

/** The words for the plan's place in the flow, shown next to the stepper. */
export function stageText(plan: Plan | undefined): string {
  switch (plan?.status) {
    case undefined:
      return 'Describe a goal to get a proposal.';
    case 'PLANNING':
      return 'The planner is drafting a task graph.';
    case 'PROPOSED':
      return 'Choose the tasks to create, edit them if you like, then accept.';
    case 'ACCEPTED':
      return 'Accepted: the chosen tasks were created.';
    case 'REJECTED':
      return 'Rejected: nothing was created.';
    case 'FAILED':
      return 'The planner did not produce a plan.';
    default:
      return plan!.status.toLowerCase();
  }
}
