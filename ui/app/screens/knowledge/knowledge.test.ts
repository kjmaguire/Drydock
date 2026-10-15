import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { MemoryEntry, RequirementTrace, TraceTask } from '../../api/extTypes.ts';
import { authorOf, countText, coverageOf, coverageText, coveredCount, decisionMessages, latestNotes, liveTraces, memoryBlurb, packsBlurb, requirementsBlurb } from './knowledge.ts';

const task = (taskId: string, status: string, over: Partial<TraceTask> = {}): TraceTask => ({ taskId, title: taskId, status, linkedVersion: 1, stale: false, criteria: null, evidence: null, testsPassing: false, hasTests: false, ...over });
const trace = (tasks: TraceTask[] | null, over: Partial<RequirementTrace> = {}): RequirementTrace => ({
  requirement: { id: 'r1', projectId: 'p', title: 'R', source: 'upload', version: 1, contentHash: '', archived: false, created: '', updated: '', chunks: 1, quarantined: 0, links: tasks?.length ?? 0, staleLinks: 0 },
  tasks,
  criteria: 1,
  tasksCompleted: tasks?.filter((x) => x.status === 'COMPLETED').length ?? 0,
  tasksStale: 0,
  verified: false,
  ...over,
});
const note = (over: Partial<MemoryEntry>): MemoryEntry => ({ id: 'm1', projectId: 'p', kind: 'convention', title: 'T', body: '', status: 'accepted', created: 1, updated: 1, ...over });

describe('coverage', () => {
  it('no task yet when nothing is linked', () => {
    expect(coverageOf(trace(null))).toEqual({ label: 'no task yet', tone: 'muted' });
    expect(coverageOf(trace([]))).toEqual({ label: 'no task yet', tone: 'muted' });
  });

  it('a completed task covers; verified only when the core says so', () => {
    expect(coverageOf(trace([task('t1', 'IN_PROGRESS'), task('t2', 'COMPLETED')]))).toEqual({ label: 'covered', tone: 'ok', taskId: 't2' });
    expect(coverageOf(trace([task('t2', 'COMPLETED')], { verified: true }))).toEqual({ label: 'verified', tone: 'ok', taskId: 't2' });
  });

  it('a task under way is in progress; one that has not started is planned; failed and cancelled are said', () => {
    expect(coverageOf(trace([task('t1', 'READY'), task('t2', 'REVIEW')]))).toEqual({ label: 'in progress', tone: 'warn', taskId: 't2' });
    expect(coverageOf(trace([task('t1', 'BACKLOG')]))).toEqual({ label: 'planned', tone: 'info', taskId: 't1' });
    expect(coverageOf(trace([task('t1', 'CANCELLED'), task('t2', 'FAILED')]))).toEqual({ label: 'failed', tone: 'err', taskId: 't2' });
    expect(coverageOf(trace([task('t1', 'CANCELLED')]))).toEqual({ label: 'cancelled', tone: 'muted', taskId: 't1' });
  });

  it('the chip names the task number when it is known', () => {
    const c = coverageOf(trace([task('t2', 'COMPLETED')]));
    expect(coverageText(c, [{ id: 't2', seq: 9 }])).toBe('covered · #9');
    expect(coverageText(c, [])).toBe('covered');
    expect(coverageText(coverageOf(trace(null)), [{ id: 't2', seq: 9 }])).toBe('no task yet');
  });

  it('counts the live requirements and the covered ones', () => {
    const archived = trace([task('t1', 'COMPLETED')]);
    archived.requirement = { ...archived.requirement, archived: true };
    const list = [trace([task('t1', 'COMPLETED')]), trace(null), trace([task('t3', 'IN_PROGRESS')]), archived];
    expect(liveTraces(list)).toHaveLength(3);
    expect(coveredCount(liveTraces(list))).toBe(1);
    expect(requirementsBlurb(liveTraces(list))).toBe('1 covered by tasks');
    expect(requirementsBlurb([trace(null)])).toBe('none covered by a task yet');
    expect(requirementsBlurb([])).toBe('what the project must do');
  });
});

describe('memory notes', () => {
  const agents = [{ id: 'agt1', name: 'Ada' }];

  it('names the agent, the person or whoever the core recorded', () => {
    expect(authorOf('agt1', agents)).toEqual({ kind: 'agent', id: 'agt1', name: 'Ada' });
    expect(authorOf('human:kyle', agents)).toEqual({ kind: 'human', id: 'human:kyle', name: 'kyle' });
    expect(authorOf('human:', agents)).toEqual({ kind: 'human', id: 'human:', name: 'you' });
    expect(authorOf('importer', agents)).toEqual({ kind: 'other', id: 'importer', name: 'importer' });
    expect(authorOf(undefined, agents)).toBe(undefined);
    expect(authorOf('', agents)).toBe(undefined);
  });

  it('the latest notes come first and the list is capped', () => {
    const list = [note({ id: 'a', updated: 1 }), note({ id: 'b', updated: 3 }), note({ id: 'c', updated: 2 })];
    expect(latestNotes(list, 2).map((x) => x.id)).toEqual(['b', 'c']);
    expect(list.map((x) => x.id)).toEqual(['a', 'b', 'c']);
  });

  it('the blurb counts accepted notes and the ones waiting for a person', () => {
    expect(memoryBlurb([])).toBe('notes the crew writes down');
    expect(memoryBlurb([note({}), note({ status: 'rejected' })])).toBe('1 accepted');
    expect(memoryBlurb([note({}), note({ status: 'proposed' }), note({ status: 'proposed' })])).toBe('1 accepted · 2 waiting for you');
  });
});

describe('the other cards', () => {
  it('packs are named, three at most', () => {
    expect(packsBlurb([])).toBe('reference material the crew reads');
    expect(packsBlurb([{ title: 'Stripe API' }, { title: 'Go style' }])).toBe('Stripe API, Go style');
    expect(packsBlurb([{ title: 'A' }, { title: 'B' }, { title: 'C' }, { title: 'D' }, { title: 'E' }])).toBe('A, B, C and 2 more');
  });

  it('decisions count the messages people and agents posted, and say when the page was full', () => {
    const m = (type: string, kind: 'human' | 'agent' | 'system') => ({ type, author: { kind, id: 'x' } });
    expect(decisionMessages([m('CHAT', 'human'), m('DECISION', 'agent'), m('SYSTEM', 'system'), m('CHAT', 'system')])).toBe(2);
    expect(countText(7, false)).toBe('7');
    expect(countText(200, true)).toBe('200+');
  });
});
