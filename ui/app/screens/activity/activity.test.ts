import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { DrydockEvent } from '../../api/types.ts';
import { defaultFilter, filterRows, inScope, matchesFilter, mergeRows, readDepth, readFilter, selectRows, typeFamily, typeTone, writeDepth, writeFilter } from './activity.ts';

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

  it('puts each type in one chip family: errors, waiting on you, agents, tasks and runs, everything else (X51)', () => {
    expect(typeFamily('task.failed')).toBe('errors');
    expect(typeFamily('agent.failed')).toBe('errors');
    expect(typeFamily('approval.requested')).toBe('waiting');
    expect(typeFamily('watchdog.paused')).toBe('waiting');
    expect(typeFamily('agent.needs_attention')).toBe('waiting');
    expect(typeFamily('agent.started')).toBe('agents');
    expect(typeFamily('room.message_posted')).toBe('agents');
    expect(typeFamily('task.created')).toBe('tasks');
    expect(typeFamily('workflow.step_started')).toBe('tasks');
    expect(typeFamily('license.changed')).toBe('other');
  });

  it('remembers the filter per project and reads anything unreadable as the default (X51)', () => {
    const kept = new Map<string, string>();
    const storage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v) };
    expect(readFilter('p1', storage)).toEqual(defaultFilter());
    writeFilter('p1', { families: ['errors'], text: 'task.*', thisProject: false, scope: { taskId: 't1' } }, storage);
    expect(readFilter('p1', storage)).toEqual({ families: ['errors'], text: 'task.*', thisProject: false, scope: { taskId: 't1' } });
    expect(readFilter('p2', storage)).toEqual(defaultFilter());
    kept.set('drydock.activity.filter.p3', '{"families":["nope","agents"],"text":5,"scope":{"runId":7}}');
    expect(readFilter('p3', storage)).toEqual({ families: ['agents'], text: '', thisProject: true, scope: {} });
    kept.set('drydock.activity.filter.p4', 'not json');
    expect(readFilter('p4', storage)).toEqual(defaultFilter());
    const refusing = { getItem: () => { throw new Error('blocked'); }, setItem: () => { throw new Error('blocked'); } };
    expect(readFilter('p1', refusing)).toEqual(defaultFilter());
    writeFilter('p1', defaultFilter(), refusing);
  });

  it('narrows to one task, run or agent, and to the chosen families (X51, X52)', () => {
    expect(inScope({ taskId: 't1' }, { taskId: 't1' })).toBe(true);
    expect(inScope({ taskId: 't2' }, { taskId: 't1' })).toBe(false);
    // a run decides alone: its task only names it
    expect(inScope({ runId: 'r1' }, { runId: 'r1', taskId: 't1' })).toBe(true);
    expect(inScope({ runId: 'r2', taskId: 't1' }, { runId: 'r1', taskId: 't1' })).toBe(false);
    expect(inScope({ agentId: 'a1' }, { agentId: 'a1' })).toBe(true);
    const events = [ev(1, 'task.created', { taskId: 't1' }), ev(2, 'task.failed', { taskId: 't2' }), ev(3, 'agent.started', { agentId: 'a1', taskId: 't1' }), ev(4, 'approval.requested', { taskId: 't1' })];
    const f = defaultFilter();
    expect(filterRows(events, { ...f, families: ['errors', 'waiting'] }, undefined).map((e) => e.seq)).toEqual([4, 2]);
    expect(filterRows(events, { ...f, scope: { taskId: 't1' } }, undefined).map((e) => e.seq)).toEqual([4, 3, 1]);
    expect(filterRows(events, { ...f, scope: { taskId: 't1' }, text: 'agent.*' }, undefined).map((e) => e.seq)).toEqual([3]);
  });

  it('merges two ascending row lists into one, each seq once (X55 review)', () => {
    const a = [{ seq: 1, k: 'a' }, { seq: 4, k: 'a' }, { seq: 9, k: 'a' }];
    const b = [{ seq: 2, k: 'b' }, { seq: 4, k: 'b' }, { seq: 12, k: 'b' }];
    expect(mergeRows(a, b).map((x) => `${x.seq}${x.k}`)).toEqual(['1a', '2b', '4a', '9a', '12b']);
    expect(mergeRows([], b)).toEqual(b);
    expect(mergeRows(a, [])).toEqual(a);
  });
});

describe('the remembered depth (X53)', () => {
  it('reads back what was written per project, and anything unreadable as none', () => {
    const kept = new Map<string, string>();
    const store = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v) };
    expect(readDepth('p1', store)).toBe(0);
    writeDepth('p1', 3, store);
    expect(readDepth('p1', store)).toBe(3);
    expect(readDepth('p2', store)).toBe(0);
    kept.set('drydock.activity.depth.p2', 'lots');
    expect(readDepth('p2', store)).toBe(0);
    const broken = { getItem: () => { throw new Error('denied'); } };
    expect(readDepth('p1', broken)).toBe(0);
  });
});
