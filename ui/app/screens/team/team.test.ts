import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AdapterInfo, Agent, AgentRun, PresenceState, Task } from '../../api/types.ts';
import { adapterName, adapterOption, agentStats, appliedNote, crewFigures, currentTask, defaultRoutes, doing, minutesText, presenceWords, presetUntouched, purposeExcerpt, routesForUpdate, runStatusWords, runsWhere, runsWhereTitle, skillCount, sortAdapters, spentLabel, stepIndex } from './team.ts';

const mk = (id: string, status: string, installed: boolean) => ({ id, detection: { adapter: id, installed, status }, capabilities: {} as never }) as AdapterInfo;

describe('team helpers', () => {
  it('sorts ready adapters first, then installed ones, then by id', () => {
    expect(sortAdapters([mk('gemini', 'not_installed', false), mk('claude', 'ready', true), mk('codex', 'not_installed', false)]).map((a) => a.id)).toEqual(['claude', 'codex', 'gemini']);
    expect(sortAdapters([mk('b', 'unauthenticated', true), mk('z', 'not_installed', false), mk('a', 'ready', true)]).map((a) => a.id)).toEqual(['a', 'b', 'z']);
  });

  it('names an adapter with its status, or says it is not installed', () => {
    expect(adapterOption(mk('claude', 'ready', true))).toBe('claude (ready)');
    expect(adapterOption(mk('codex', 'not_installed', false))).toBe('codex (not installed)');
  });

  it('shortens a long purpose to 160 characters', () => {
    expect(purposeExcerpt('short')).toBe('short');
    const long = 'x'.repeat(200);
    expect(purposeExcerpt(long)).toBe(`${'x'.repeat(160)}...`);
  });

  it('turns routing on with haiku and opus when the adapter offers both, else two empty rows', () => {
    expect(defaultRoutes(['opus', 'sonnet', 'haiku'])).toEqual([
      { model: 'haiku', tier: 'cheap' },
      { model: 'opus', tier: 'premium' },
    ]);
    expect(defaultRoutes([])).toEqual([
      { model: '', tier: 'cheap' },
      { model: '', tier: 'premium' },
    ]);
  });

  it('sends routed models only when they changed, without empty rows', () => {
    expect(routesForUpdate([], undefined)).toBe(undefined);
    expect(routesForUpdate([{ model: 'haiku', tier: 'cheap' }], [{ model: 'haiku', tier: 'cheap' }])).toBe(undefined);
    expect(routesForUpdate([{ model: 'haiku', tier: 'cheap' }, { model: ' ', tier: 'premium' }], [])).toEqual([{ model: 'haiku', tier: 'cheap' }]);
    expect(routesForUpdate([], [{ model: 'haiku', tier: 'cheap' }])).toEqual([]);
  });

  it('says who joined from a template, what was granted and what was not, and what its skills did', () => {
    const agent = { name: 'Payments dev' } as Agent;
    const none = { assigned: [], shared: [], pending: [], failed: [] };
    expect(appliedNote({ agent, granted: ['docs'], skipped: [], skills: none })).toBe('Payments dev joined the team, with docs.');
    expect(appliedNote({ agent, granted: [], skipped: ['gh: not connected'], skills: none })).toBe('Payments dev joined the team. Not granted: gh: not connected.');
    expect(
      appliedNote({
        agent,
        granted: [],
        skipped: [],
        skills: { assigned: ['wrangler'], shared: ['go-style'], pending: [{ name: 'durable-objects', reason: 'blocked: injection' }], failed: [{ name: 'x', reason: 'not found' }] },
      }),
    ).toBe(
      'Payments dev joined the team. Skills: wrangler. Already with the whole team: go-style. Skill durable-objects waits for your decision: blocked: injection Skill x not installed: not found',
    );
  });

  it('says what an agent is doing in one line, where it runs, and names its CLI', () => {
    const p = (over: Partial<PresenceState>): PresenceState => ({ agentId: 'a', presence: 'Working', since: '', updated: '', seq: 1, ...over });
    expect(doing(undefined)).toBe('Stopped');
    expect(doing(p({ activity: 'tool: Write', label: 'on #3' }))).toBe('tool: Write');
    expect(doing(p({ label: 'stopped', presence: 'Paused' }))).toBe('Stopped');
    expect(doing(p({ label: 'waiting for you', presence: 'Paused' }))).toBe('waiting for you');
    expect(doing(p({}))).toBe('Working');
    expect(doing(p({ presence: 'Needs Attention' }))).toBe('Waiting for you');
    expect(presenceWords(undefined)).toBe('Stopped');
    expect(presenceWords(p({ presence: 'Paused', label: 'stopped' }))).toBe('Stopped');
    expect(presenceWords(p({ presence: 'Paused', label: 'budget' }))).toBe('Waiting for you');
    expect(runsWhere({ runMode: 'wsl', wslDistro: 'Ubuntu' })).toBe('in WSL'); // the distro in the tooltip only
    expect(runsWhereTitle({ runMode: 'wsl', wslDistro: 'Ubuntu' })).toBe('Runs inside the WSL distro Ubuntu');
    expect(runsWhereTitle({ runMode: 'native', wslDistro: '' })).toBeUndefined();
    expect(runsWhere({ runMode: 'native' })).toBe('on Windows');
    expect(runsWhere({})).toBe('');
    expect(adapterName('claude')).toBe('Claude Code');
    expect(adapterName('zeta')).toBe('zeta');
  });

  it('finds the task an agent is on: from its presence, else its running run, else its first open task as worker', () => {
    const task = (over: Partial<Task>): Task => ({ id: 't', projectId: 'p', seq: 1, title: 'x', description: '', status: 'IN_PROGRESS', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: 0, updated: 0, version: 1, ...over });
    const tasks = [task({ id: 't1', seq: 1, assignee: 'a', status: 'COMPLETED' }), task({ id: 't2', seq: 2, assignee: 'a' }), task({ id: 't3', seq: 3, assignee: 'b' })];
    const run = (over: Partial<AgentRun>): AgentRun => ({ id: 'r', taskId: '', pid: 0, cwd: '', status: 'running', startedAt: 1, endedAt: 0, ...over });
    const presence: PresenceState = { agentId: 'a', taskId: 't3', presence: 'Working', since: '', updated: '', seq: 1 };
    expect(currentTask('a', presence, [], tasks)?.id).toBe('t3');
    expect(currentTask('a', undefined, [run({ taskId: 't3' }), run({ taskId: 't1', status: 'completed' })], tasks)?.id).toBe('t3');
    expect(currentTask('a', undefined, [run({ taskId: 't1', status: 'completed' })], tasks)?.id).toBe('t2');
    expect(currentTask('b', undefined, [], [tasks[0]!])).toBeUndefined();
    // a task of another project (not in the list) is not invented
    expect(currentTask('b', { ...presence, taskId: 'gone' }, [], [tasks[0]!])).toBeUndefined();
  });

  it('places a task status on the work, review, you, done strip', () => {
    expect(stepIndex('READY')).toBe(-1);
    expect(stepIndex('IN_PROGRESS')).toBe(0);
    expect(stepIndex('REVIEW')).toBe(1);
    expect(stepIndex('WAITING_HUMAN')).toBe(2);
    expect(stepIndex('VERIFICATION')).toBe(2);
    expect(stepIndex('COMPLETED')).toBe(3);
    expect(stepIndex('FAILED')).toBe(-1);
  });

  it('counts an agent\'s stats from the task list and leaves out what no task reports', () => {
    const task = (over: Partial<Task>): Task => ({ id: 't', projectId: 'p', seq: 1, title: 'x', description: '', status: 'COMPLETED', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: 0, updated: 0, version: 1, ...over });
    expect(agentStats('a', [])).toEqual({ merged: 0, reviewed: 0, firstTime: undefined, avgCostUsd: undefined, avgMinutes: undefined });
    const tasks = [
      task({ id: 't1', assignee: 'a', reviewer: 'b', costUsd: 0.2, started: 1_000_000, completed: 1_000_600 }), // seconds: 10 min
      task({ id: 't2', assignee: 'a', reviewer: 'b', iteration: 2, costUsd: 0.4, started: 1_700_000_000_000, completed: 1_700_000_000_000 + 20 * 60_000 }),
      task({ id: 't3', assignee: 'a', status: 'IN_PROGRESS', costUsd: 9 }), // not done: not counted
      task({ id: 't4', assignee: 'b', reviewer: 'a' }),
    ];
    const st = agentStats('a', tasks);
    expect(st.merged).toBe(2);
    expect(st.reviewed).toBe(1);
    expect(st.firstTime).toBe(0.5);
    expect(st.avgCostUsd).toBeCloseTo(0.3, 6);
    expect(st.avgMinutes).toBeCloseTo(15, 6);
    const b = agentStats('b', tasks);
    expect(b).toMatchObject({ merged: 1, reviewed: 2, firstTime: 1, avgCostUsd: undefined, avgMinutes: undefined });
    expect(minutesText(0.4)).toBe('under 1 m');
    expect(minutesText(14.4)).toBe('14 m');
    expect(minutesText(65)).toBe('1 h 05 m');
    expect(minutesText(3000)).toBe('2.1 d');
  });

  it('applies the Claude preset only while the fields are empty or still hold the last preset', () => {
    expect(presetUntouched('', '', undefined)).toBe(true);
    expect(presetUntouched('sonnet', 'medium', { model: 'sonnet', effort: 'medium' })).toBe(true);
    expect(presetUntouched('sonnet', 'low', { model: 'sonnet', effort: 'medium' })).toBe(false);
    expect(presetUntouched('opus', '', undefined)).toBe(false);
  });

  it('captions the spend figure with how many usage rows it covers and how many are estimated (M21)', () => {
    expect(spentLabel({ rows: 120, estimated: 30 })).toBe('spent in all 120 usage rows, 30 estimated');
    expect(spentLabel({ rows: 1, estimated: 0 })).toBe('spent in all 1 usage row');
    expect(spentLabel({ rows: 1234, estimated: 0 })).toBe('spent in all 1,234 usage rows');
  });

  it("counts the Crew page's figures: agents, at work, waiting on you (by presence or an open request), stopped", () => {
    const agents = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }, { id: 'e' }];
    const presence = [
      { agentId: 'a', presence: 'Working', since: '', updated: '', seq: 1 },
      { agentId: 'b', presence: 'Paused', label: 'stopped', since: '', updated: '', seq: 1 },
      { agentId: 'd', presence: 'Needs Attention', since: '', updated: '', seq: 1 },
      { agentId: 'e', presence: 'Thinking', since: '', updated: '', seq: 1 },
    ] as PresenceState[];
    // c has no presence at all: stopped too (it never started); e works but has a request open: it waits on you
    expect(crewFigures(agents, presence, new Set(['e']))).toEqual({ agents: 5, working: 1, waiting: 2, stopped: 2 });
    expect(crewFigures(agents, presence)).toEqual({ agents: 5, working: 2, waiting: 1, stopped: 2 });
    const skills = [
      { status: 'accepted', agents: ['a', 'b'] },
      { status: 'accepted', agents: ['a', 'gone'] },
      { status: 'offered', agents: ['c'] }, // not accepted: given to nobody yet
    ];
    expect(skillCount(skills, 'a')).toBe(2);
    expect(skillCount(skills, 'c')).toBe(0);
  });
});

describe('runStatusWords', () => {
  it("says an agent run's end in words, not the core's constant", () => {
    expect(runStatusWords('WORKING')).toBe('Running');
    expect(runStatusWords('running')).toBe('Running');
    expect(runStatusWords('STOPPED')).toBe('Last run ended');
    expect(runStatusWords('CANCELLED')).toBe('Last run cancelled');
    expect(runStatusWords('FAILED')).toBe('Last run failed');
    expect(runStatusWords('WAITING_HUMAN')).toBe('Waiting human');
    expect(runStatusWords('')).toBe('Run');
  });
});
