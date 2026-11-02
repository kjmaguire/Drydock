// Pure helpers for the Activity screen (no DOM, no services).

import type { DrydockEvent } from '../../api/types.ts';
import type { Tone } from '../../ui/tokens.ts';

/** Prefix filter as in the core: "task.*" or "task." match by prefix, anything else exactly. Comma separated; empty matches all. */
export function matchesFilter(type: string, filter: string): boolean {
  const parts = filter
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  if (parts.length === 0) return true;
  return parts.some((p) => (p.endsWith('*') ? type.startsWith(p.slice(0, -1)) : p.endsWith('.') ? type.startsWith(p) : type === p));
}

/**
 * The rows to show, newest first, at most `limit`: events of other projects are left out when `projectId` is given (an
 * event without a project, such as a workspace event, always shows), then the type filter applies.
 */
export function selectRows<E extends Pick<DrydockEvent, 'type' | 'projectId'>>(events: readonly E[], opts: { filter: string; projectId?: string; limit?: number }): E[] {
  const limit = opts.limit ?? 500;
  const out: E[] = [];
  for (let i = events.length - 1; i >= 0 && out.length < limit; i--) {
    const e = events[i]!;
    if (opts.projectId && e.projectId && e.projectId !== opts.projectId) continue;
    if (matchesFilter(e.type, opts.filter)) out.push(e);
  }
  return out;
}

/** The tone of an event type's family, so a long log can be scanned by color as well as by its words. */
export function typeTone(type: string): Tone {
  if (/(failed|error|rejected|refused|denied)/.test(type)) return 'err';
  if (/(paused|warning|exceeded|blocked|watchdog)/.test(type)) return 'warn';
  if (/(completed|approved|merged|accepted|passed)/.test(type)) return 'ok';
  if (type.startsWith('task.') || type.startsWith('workflow.')) return 'info';
  if (type.startsWith('agent.') || type.startsWith('room.')) return 'accent';
  return 'muted';
}

/** The families the Activity chips show (decision: the families of typeTone, named for the person). */
export type Family = 'errors' | 'waiting' | 'agents' | 'tasks' | 'other';

export const FAMILIES: readonly { id: Family; label: string }[] = [
  { id: 'errors', label: 'Errors' },
  { id: 'waiting', label: 'Waiting on you' },
  { id: 'agents', label: 'Agents' },
  { id: 'tasks', label: 'Tasks and runs' },
  { id: 'other', label: 'Everything else' },
];

/** The one family an event type belongs to: an error first, then what waits on the person, then its area. */
export function typeFamily(type: string): Family {
  const tone = typeTone(type);
  if (tone === 'err') return 'errors';
  if (tone === 'warn' || /(requested|waiting|needs_attention|asked)/.test(type)) return 'waiting';
  if (/^(agent|room|lead|chat|presence)\./.test(type)) return 'agents';
  if (/^(task|workflow|run|handoff|review|checkpoint)\./.test(type)) return 'tasks';
  return 'other';
}

/** What a scope link or the run page narrows the log to: one task, run or agent. */
export interface Scope {
  taskId?: string;
  runId?: string;
  agentId?: string;
}

/** The Activity page's filter, remembered per project (an empty `families` shows every family). */
export interface ActivityFilter {
  families: Family[];
  text: string;
  thisProject: boolean;
  scope: Scope;
}

export const defaultFilter = (): ActivityFilter => ({ families: [], text: '', thisProject: true, scope: {} });

const FILTER_KEY = 'drydock.activity.filter.';

/** The filter as kept for a project (or the workspace when there is none); anything unreadable is the default. */
export function readFilter(projectId: string | undefined, storage: Pick<Storage, 'getItem'> | undefined = globalThis.localStorage): ActivityFilter {
  const f = defaultFilter();
  try {
    const raw = storage?.getItem(FILTER_KEY + (projectId ?? ''));
    if (!raw) return f;
    const v = JSON.parse(raw) as Partial<ActivityFilter>;
    const ids = new Set(FAMILIES.map((x) => x.id));
    if (Array.isArray(v.families)) f.families = v.families.filter((x): x is Family => ids.has(x as Family));
    if (typeof v.text === 'string') f.text = v.text.slice(0, 200);
    if (typeof v.thisProject === 'boolean') f.thisProject = v.thisProject;
    if (v.scope && typeof v.scope === 'object') {
      for (const k of ['taskId', 'runId', 'agentId'] as const) {
        const id = (v.scope as Scope)[k];
        if (typeof id === 'string' && id) f.scope[k] = id.slice(0, 100);
      }
    }
  } catch {
    /* a convenience only */
  }
  return f;
}

export function writeFilter(projectId: string | undefined, f: ActivityFilter, storage: Pick<Storage, 'setItem'> | undefined = globalThis.localStorage): void {
  try {
    storage?.setItem(FILTER_KEY + (projectId ?? ''), JSON.stringify(f));
  } catch {
    /* a convenience only */
  }
}

/** Whether the event is in the scope. A run decides alone (its task only names it); a task or an agent must match. */
export function inScope(e: Pick<DrydockEvent, 'taskId' | 'runId' | 'agentId'>, scope: Scope): boolean {
  if (scope.agentId && e.agentId !== scope.agentId) return false;
  if (scope.runId) return e.runId === scope.runId;
  return !scope.taskId || e.taskId === scope.taskId;
}

/** The rows for the page's filter: selectRows with the chosen families and scope as well. */
export function filterRows<E extends Pick<DrydockEvent, 'type' | 'projectId' | 'taskId' | 'runId' | 'agentId'>>(events: readonly E[], f: ActivityFilter, projectId: string | undefined, limit?: number): E[] {
  const fams = new Set(f.families);
  const scoped = events.filter((e) => inScope(e, f.scope) && (fams.size === 0 || fams.has(typeFamily(e.type))));
  return selectRows(scoped, { filter: f.text, projectId: f.thisProject ? projectId : undefined, limit });
}
