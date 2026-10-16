import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { DrydockEvent } from '../../api/types.ts';
import { matchesFilter, selectRows, typeTone } from './activity.ts';

const ev = (seq: number, type: string, extra: Partial<DrydockEvent> = {}): DrydockEvent => ({ seq, time: '2026-01-01T00:00:00Z', type, ...extra });

describe('activity helpers', () => {
  it('filters by type prefix as the core does', () => {
    expect(matchesFilter('task.created', '')).toBe(true);
    expect(matchesFilter('task.created', 'task.*')).toBe(true);
    expect(matchesFilter('task.created', 'agent.*, task.')).toBe(true);
    expect(matchesFilter('task.created', 'task.created')).toBe(true);
    expect(matchesFilter('task.created', 'task.cre')).toBe(false);
    expect(matchesFilter('room.message_posted', 'task.*')).toBe(false);
  });

  it('shows newest first, keeps events without a project, leaves out other projects and caps the list', () => {
    const events = [ev(1, 'task.created', { projectId: 'p1' }), ev(2, 'agent.created', { projectId: 'p2' }), ev(3, 'license.changed'), ev(4, 'task.completed', { projectId: 'p1' })];
    expect(selectRows(events, { filter: '', projectId: 'p1' }).map((e) => e.seq)).toEqual([4, 3, 1]);
    expect(selectRows(events, { filter: '' }).map((e) => e.seq)).toEqual([4, 3, 2, 1]);
    expect(selectRows(events, { filter: 'task.*', projectId: 'p1' }).map((e) => e.seq)).toEqual([4, 1]);
    expect(selectRows(events, { filter: '', limit: 2 }).map((e) => e.seq)).toEqual([4, 3]);
  });

  it('colors failures, warnings and successes', () => {
    expect(typeTone('task.failed')).toBe('err');
    expect(typeTone('watchdog.paused')).toBe('warn');
    expect(typeTone('task.completed')).toBe('ok');
    expect(typeTone('task.created')).toBe('info');
    expect(typeTone('license.changed')).toBe('muted');
  });
});
