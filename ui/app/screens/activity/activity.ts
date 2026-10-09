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
export function selectRows(events: readonly DrydockEvent[], opts: { filter: string; projectId?: string; limit?: number }): DrydockEvent[] {
  const limit = opts.limit ?? 500;
  const out: DrydockEvent[] = [];
  for (let i = events.length - 1; i >= 0 && out.length < limit; i--) {
    const e = events[i]!;
    if (opts.projectId && e.projectId && e.projectId !== opts.projectId) continue;
    if (matchesFilter(e.type, opts.filter)) out.push(e);
  }
  return out;
}

/** A short, single-line view of an event's data (at most 200 characters); empty when there is none. */
export function dataPreview(data: unknown, max = 200): string {
  if (data === undefined) return '';
  try {
    return (JSON.stringify(data) ?? '').slice(0, max);
  } catch {
    return '';
  }
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
