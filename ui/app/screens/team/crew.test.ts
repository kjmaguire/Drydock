import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AgentLane, RunSpan, WaitSpan } from '../../api/inboxClient.ts';
import type { PresenceState, TaskSummary } from '../../api/types.ts';
import {
  MIN_BLOCK,
  agentLine,
  at,
  cardState,
  clock,
  crewLine,
  crewLookups,
  currentSummary,
  lastFinished,
  laneBlocks,
  localIds,
  localMidnight,
  nextQueued,
  openWait,
  progressOf,
  roomsOf,
  runtimeName,
  timelineTicks,
  timelineWindow,
} from './crew.ts';

// local times of one day (the timeline is drawn in the person's own time)
const day = (h: number, m = 0) => new Date(2026, 9, 22, h, m).getTime();
const NOW = day(16, 0);

// TEST DOUBLES (labelled): spans shaped like core.RunSpan and core.WaitSpan
const run = (over: Partial<RunSpan>): RunSpan => ({ runId: 'r', start: day(9), end: day(10), startedAt: day(9), endedAt: day(10), outcome: 'finished', ...over });
const wait = (over: Partial<WaitSpan>): WaitSpan => ({
  approvalId: 'ap',
  kind: 'approval',
  title: 'Approve the plan',
  status: 'REQUESTED',
  start: day(11),
  end: null,
  createdAt: day(11),
  resolvedAt: 0,
  ...over,
});
const lane = (runs: RunSpan[], waits: WaitSpan[] = []): AgentLane => ({ agentId: 'a1', name: 'Builder', projectId: 'p', role: 'backend', adapter: 'claude', model: 'sonnet', runs, waits });
const key = (seq: number) => `GOC-${seq}`;
const presence = (over: Partial<PresenceState>): PresenceState => ({ agentId: 'a1', presence: 'Working', since: '', updated: '', seq: 1, ...over });
const summary = (over: Partial<TaskSummary>): TaskSummary => ({
  taskId: 't1',
  projectId: 'p',
  projectKey: 'GOC',
  seq: 43,
  title: 'Fix the limiter',
  status: 'IN_PROGRESS',
  labels: [],
  assignee: 'a1',
  criteria: 0,
  step: 'build',
  stepIndex: 2,
  stepCount: 5,
  attempt: 1,
  maxAttempts: 3,
  checks: [],
  pendingCount: 0,
  updatedAt: day(9),
  ...over,
});

describe('the Today timeline', () => {
  it('runs from 08:00 to now, from the first span\'s hour when that is earlier, and never before midnight', () => {
    expect(timelineWindow({ agents: [] }, NOW)).toEqual({ start: day(8), end: NOW });
    expect(timelineWindow({ agents: [lane([run({ start: day(6, 40) })])] }, NOW)).toEqual({ start: day(6), end: NOW });
    expect(timelineWindow({ agents: [lane([], [wait({ start: day(7, 5) })])] }, NOW).start).toBe(day(7));
    // a run that began yesterday comes clipped to midnight by the core: the window starts there
    expect(timelineWindow({ agents: [lane([run({ start: localMidnight(NOW) })])] }, NOW).start).toBe(localMidnight(NOW));
    // before 08:00 the window starts at the hour now is in, and is never empty
    expect(timelineWindow(undefined, day(6, 30))).toEqual({ start: day(6), end: day(6, 30) });
    expect(timelineWindow(undefined, day(6, 0))).toEqual({ start: day(6), end: day(6, 1) });
  });

  it('labels the axis on whole hours, at most five of them, then "now" at the right end', () => {
    const w = { start: day(8), end: NOW };
    const ticks = timelineTicks(w);
    expect(ticks.map((k) => k.label)).toEqual(['08:00', '10:00', '12:00', '14:00', 'now']);
    expect(ticks.map((k) => Math.round(k.left))).toEqual([0, 25, 50, 75, 100]);
    // a short morning: every hour; an hour too close to now to read beside it is left out
    expect(timelineTicks({ start: day(8), end: day(10, 50) }).map((k) => k.label)).toEqual(['08:00', '09:00', '10:00', 'now']);
    expect(timelineTicks({ start: day(8), end: day(10, 10) }).map((k) => k.label)).toEqual(['08:00', '09:00', 'now']);
    // a long day steps by six hours
    expect(timelineTicks({ start: day(0), end: day(23) }).map((k) => k.label)).toEqual(['00:00', '06:00', '12:00', '18:00', 'now']);
    expect(clock(day(9, 5))).toBe('09:05');
  });

  it('places each run by time with its outcome, and says what it is in its label', () => {
    const w = { start: day(8), end: NOW }; // 8 hours: one hour is 12.5%
    const blocks = laneBlocks(
      lane([
        run({ runId: 'r1', start: day(9), end: day(10), startedAt: day(9), endedAt: day(10), outcome: 'finished', taskSeq: 43, taskId: 't1' }),
        run({ runId: 'r2', start: day(10), end: day(10, 30), startedAt: day(10), endedAt: day(10, 30), outcome: 'failed', taskSeq: 43 }),
        run({ runId: 'r3', start: day(11), end: day(11, 6), startedAt: day(11), endedAt: day(11, 6), outcome: 'cancelled' }),
        run({ runId: 'r4', start: day(14), end: null, startedAt: day(14), endedAt: 0, outcome: 'working', taskSeq: 44 }),
      ]),
      w,
      NOW,
      key,
    );
    expect(blocks.map((b) => [b.kind, b.left, b.width])).toEqual([
      ['finished', 12.5, 12.5],
      ['failed', 25, 6.25],
      ['cancelled', 37.5, 1.25],
      ['working', 75, 25],
    ]);
    expect(blocks.map((b) => b.label)).toEqual([
      'Builder · GOC-43 · 09:00–10:00 · finished',
      'Builder · GOC-43 · 10:00–10:30 · failed',
      'Builder · 11:00–11:06 · cancelled',
      'Builder · GOC-44 · 14:00–now · working now',
    ]);
    expect(blocks[0]!.taskId).toBe('t1');
  });

  it('clips a span to the window, keeps a one-minute run visible, and leaves out what is outside', () => {
    const w = { start: day(8), end: NOW };
    const midnight = localMidnight(NOW);
    const blocks = laneBlocks(
      lane([
        // began yesterday: the core clipped it to midnight; the label keeps its real start
        run({ runId: 'y', start: midnight, end: day(9), startedAt: midnight - 20 * 60_000, endedAt: day(9) }),
        run({ runId: 'tiny', start: day(12), end: day(12, 0) + 30_000, startedAt: day(12), endedAt: day(12) + 30_000 }),
        // a run that ends at the right edge stays inside the track
        run({ runId: 'edge', start: NOW - 10_000, end: null, startedAt: NOW - 10_000, endedAt: 0, outcome: 'working' }),
        run({ runId: 'later', start: NOW + 60_000, end: NOW + 120_000 }),
      ]),
      w,
      NOW,
      key,
    );
    expect(blocks.map((b) => b.key)).toEqual(['run:y', 'run:tiny', 'run:edge']);
    expect([blocks[0]!.left, blocks[0]!.width]).toEqual([0, 12.5]);
    expect(blocks[0]!.label).toBe('Builder · yesterday 23:40–09:00 · finished');
    expect(blocks[1]!.width).toBe(MIN_BLOCK);
    expect(blocks[2]!.left + blocks[2]!.width).toBe(100);
    expect(at(w, day(4))).toBe(0);
    expect(at(w, NOW + 1)).toBe(100);
  });

  it('draws waits over the runs: amber while waiting on the person, and the ones decided since', () => {
    const w = { start: day(8), end: NOW };
    const blocks = laneBlocks(
      lane([run({ runId: 'r1', start: day(9), end: day(12) })], [
        wait({ approvalId: 'old', start: day(9), end: day(10), createdAt: day(9), resolvedAt: day(10), status: 'APPROVED', taskSeq: 41 }),
        wait({ approvalId: 'open', start: day(15), end: null, createdAt: day(15), taskSeq: 41 }),
      ]),
      w,
      NOW,
      key,
    );
    expect(blocks.map((b) => [b.key, b.kind, b.left, b.width])).toEqual([
      ['run:r1', 'finished', 12.5, 37.5],
      ['wait:old', 'waited', 12.5, 12.5],
      ['wait:open', 'waiting', 87.5, 12.5],
    ]);
    expect(blocks[2]!.label).toBe('Builder · GOC-41 · 15:00–now · waiting on you');
    expect(blocks[1]!.label).toBe('Builder · GOC-41 · 09:00–10:00 · waited on you');
    expect(openWait(lane([], [wait({ approvalId: 'old', end: day(10), resolvedAt: day(10) }), wait({ approvalId: 'open' })]))?.approvalId).toBe('open');
    expect(openWait(lane([], [wait({ end: day(10), resolvedAt: day(10) })]))).toBeUndefined();
    expect(openWait(undefined)).toBeUndefined();
  });
});

describe('an agent card', () => {
  it('says the state in words: an open request or a held presence is waiting on you', () => {
    expect(cardState(presence({ presence: 'Working' }), false)).toBe('working');
    expect(cardState(presence({ presence: 'Working' }), true)).toBe('waiting-on-you');
    expect(cardState(presence({ presence: 'Needs Attention' }), false)).toBe('waiting-on-you');
    expect(cardState(presence({ presence: 'Idle' }), false)).toBe('idle');
    expect(cardState(undefined, false)).toBe('stopped');
  });

  it('finds the task it is on, the next in its queue and the one it finished last', () => {
    const list = [
      summary({ taskId: 't1', seq: 43, presence: 'Working' }),
      summary({ taskId: 't2', seq: 44, status: 'READY', presence: undefined }),
      summary({ taskId: 't3', seq: 38, status: 'COMPLETED', completedAt: day(9) }),
      summary({ taskId: 't4', seq: 39, status: 'COMPLETED', completedAt: day(11) }),
      summary({ taskId: 't5', seq: 50, assignee: 'other', status: 'READY' }),
    ];
    expect(currentSummary('a1', presence({ taskId: 't2' }), list)?.taskId).toBe('t2');
    expect(currentSummary('a1', presence({}), list)?.taskId).toBe('t1');
    expect(currentSummary('a1', undefined, list)).toBeUndefined();
    expect(nextQueued('a1', list, 't1')?.taskId).toBe('t2');
    expect(lastFinished('a1', list)?.taskId).toBe('t4');
    expect(lastFinished('other', list)).toBeUndefined();
  });

  it('says in one line what the agent is doing, waits on you for, or did last', () => {
    const keyOf = (s: { seq: number; projectKey?: string }) => `${s.projectKey ?? 'X'}-${s.seq}`;
    const current = summary({});
    const next = summary({ taskId: 't2', seq: 44, title: 'Support hex input', status: 'READY' });
    expect(agentLine({ state: 'working', presence: presence({ doing: 'Running go test ./...' }), current, next, keyOf })).toEqual([
      'Running go test ./... on ',
      { key: 'GOC-43' },
      ' Fix the limiter.',
      ' Next in its queue: ',
      { key: 'GOC-44' },
      '.',
    ]);
    expect(agentLine({ state: 'working', presence: presence({ activity: 'Thinking' }), keyOf })).toEqual(['Thinking.']);
    expect(agentLine({ state: 'waiting-on-you', wait: wait({ title: 'Approve the plan.' }), waitKey: 'GOC-41', keyOf })).toEqual([
      'Approve the plan',
      ' on ',
      { key: 'GOC-41' },
      '.',
      ' ',
      { inbox: 'Open the Inbox to decide.' },
    ]);
    expect(agentLine({ state: 'waiting-on-you', presence: presence({ presence: 'Needs Attention', activity: 'Asked to skip approvals' }), keyOf })).toEqual([
      'Asked to skip approvals',
      '.',
      ' ',
      { inbox: 'Open the Inbox to decide.' },
    ]);
    expect(agentLine({ state: 'idle', last: summary({ seq: 38, title: 'Add Multiply', status: 'COMPLETED' }), keyOf })).toEqual(['Last: finished ', { key: 'GOC-38' }, ' Add Multiply.']);
    expect(agentLine({ state: 'idle', next, keyOf })).toEqual(['Next in its queue: ', { key: 'GOC-44' }, ' Support hex input.']);
    expect(agentLine({ state: 'stopped', purpose: 'Reviews every task before it reaches you. Never merges.', keyOf })).toEqual(['Reviews every task before it reaches you.']);
    expect(agentLine({ state: 'idle', keyOf })).toEqual(['Not on a task right now.']);
  });

  it('measures the progress of its task by its steps', () => {
    expect(progressOf(summary({ stepIndex: 2, stepCount: 5 }))).toEqual({ value: 2, max: 5, percent: 40 });
    expect(progressOf(summary({ stepIndex: 9, stepCount: 5 }))).toEqual({ value: 5, max: 5, percent: 100 });
    expect(progressOf(summary({ stepCount: 0 }))).toBeUndefined();
    expect(progressOf(undefined)).toBeUndefined();
  });

  it('names the runtime: Models on this PC for a local helper, Claude Code WSL inside a distro, the CLI on Windows', () => {
    const a = { adapter: 'claude', runMode: '', wslDistro: '' };
    expect(runtimeName(a, { local: true, canWSL: true })).toBe('Models on this PC');
    expect(runtimeName({ ...a, runMode: 'wsl', wslDistro: 'Ubuntu' }, { local: false, canWSL: true })).toBe('Claude Code WSL');
    expect(runtimeName(a, { local: false, canWSL: true, project: { runMode: 'wsl', wslDistro: 'Ubuntu' } })).toBe('Claude Code WSL');
    // an adapter that cannot run in WSL follows a WSL project on Windows
    expect(runtimeName({ ...a, adapter: 'codex' }, { local: false, canWSL: false, project: { runMode: 'wsl' } })).toBe('Codex');
    expect(runtimeName({ ...a, runMode: 'native' }, { local: false, canWSL: true, project: { runMode: 'wsl' } })).toBe('Claude Code');
    expect([...localIds([{ id: 'x', providerId: 'llama' }, { id: 'y', providerId: '' }], { ids: ['z'], providerId: 'llama' })].sort()).toEqual(['x', 'z']);
    expect(localIds([{ id: 'y', providerId: '' }], undefined).size).toBe(0);
  });

  it('names the rooms it is in that not all of its team is in', () => {
    const rooms = [
      { name: 'general', kind: 'channel', agents: ['a', 'b'] },
      { name: 'decisions', kind: 'channel', agents: ['a'] },
      { name: 'pair', kind: 'group', agents: ['b'] },
    ];
    expect(roomsOf('a', ['a', 'b'], rooms)).toEqual([{ name: 'decisions', kind: 'channel' }]);
    expect(roomsOf('b', ['a', 'b'], rooms)).toEqual([{ name: 'pair', kind: 'group' }]);
    // an agent alone on its team: every room is the whole team's
    expect(roomsOf('a', ['a'], rooms)).toEqual([]);
  });

  it('writes the header line', () => {
    expect(crewLine({ agents: 6, working: 3, waiting: 1 })).toBe('6 agents · 3 working · 1 waiting on you');
    expect(crewLine({ agents: 1, working: 0, waiting: 0 })).toBe('1 agent · 0 working · 0 waiting on you');
  });
});

describe('crewLookups', () => {
  it('finds each agent its presence, tasks, team and skills, the first listed where an id comes twice (audit 2026-10-27)', () => {
    const l = crewLookups(
      [presence({ agentId: 'a1', presence: 'Working' }), presence({ agentId: 'a1', presence: 'Idle' }), presence({ agentId: 'a2', presence: 'Idle' })],
      [summary({ taskId: 't1', assignee: 'a1', seq: 1 }), summary({ taskId: 't2', assignee: 'a1', seq: 2 }), summary({ taskId: 't1', assignee: 'a2', seq: 9 }), summary({ taskId: 't3', assignee: '' })],
      [
        { id: 'a1', projectId: 'p' },
        { id: 'a2', projectId: 'q' },
        { id: 'a3', projectId: 'p' },
      ],
      [
        { agentId: 'a1', name: 'go' },
        { agentId: 'a1', name: 'remix' },
      ],
    );
    expect(l.presenceById.get('a1')?.presence).toBe('Working');
    expect(l.presenceById.get('a3')).toBe(undefined);
    expect((l.byAssignee.get('a1') ?? []).map((x) => x.taskId)).toEqual(['t1', 't2']);
    expect((l.byAssignee.get('a2') ?? []).map((x) => x.seq)).toEqual([9]);
    expect(l.byAssignee.has('')).toBe(false);
    expect(l.byTask.get('t1')?.seq).toBe(1);
    expect(l.byTask.get('t3')?.taskId).toBe('t3');
    expect(l.teamOf.get('p')).toEqual(['a1', 'a3']);
    expect(l.teamOf.get('q')).toEqual(['a2']);
    expect(l.skillsOf.get('a1')).toEqual(['go', 'remix']);
    expect(l.skillsOf.get('a2')).toBe(undefined);
  });
});
