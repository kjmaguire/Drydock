import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { MemoryEntry, Skill } from '../../api/extTypes.ts';
import type { McpServer } from '../../api/mcpClient.ts';
import type { Agent, AgentDetail, PresenceState, Project, Task } from '../../api/types.ts';
import { lic, usage } from '../../test/fixtures.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { AgentScreen, nowChip, serversOf, skillsOf } from './agent-screen.tsx';

afterEach(cleanup);

// TEST DOUBLES (labelled): data shaped like the core's answers; nothing here imitates an agent's behaviour.
const agent = (over: Partial<Agent> = {}): Agent => ({
  id: 'agt_1',
  projectId: 'prj_1',
  name: 'Ada',
  adapter: 'claude',
  role: 'backend',
  model: 'sonnet',
  effort: 'high',
  providerId: '',
  permissionMode: 'acceptEdits',
  purpose: 'Backend engineer. Owns webhooks and invoices.',
  status: 'RUNNING',
  runMode: 'wsl',
  wslDistro: 'Ubuntu',
  ...over,
});
const rex = agent({ id: 'agt_2', name: 'Rex', role: 'reviewer', purpose: '', runMode: '', wslDistro: '' });
const task = (over: Partial<Task>): Task => ({ id: 't', projectId: 'prj_1', seq: 1, title: 'x', description: '', status: 'IN_PROGRESS', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: 0, updated: 0, version: 1, ...over });
const tasks: Task[] = [
  task({ id: 't12', seq: 12, title: 'Retry Stripe webhooks with backoff', status: 'REVIEW', assignee: 'agt_1', reviewer: 'agt_2' }),
  task({ id: 't9', seq: 9, title: 'Invoice PDF export', status: 'COMPLETED', assignee: 'agt_1', reviewer: 'agt_2', costUsd: 0.21, started: 1_700_000_000, completed: 1_700_000_840 }),
  task({ id: 't7', seq: 7, title: 'Charges API', status: 'COMPLETED', assignee: 'agt_1', reviewer: 'agt_2', iteration: 1, costUsd: 0.41 }),
  task({ id: 't3', seq: 3, title: 'Subtract', status: 'COMPLETED', assignee: 'agt_2', reviewer: 'agt_1' }),
];
const presence: PresenceState = { agentId: 'agt_1', taskId: 't12', presence: 'Working', activity: 'tool: Write', since: '', updated: '', seq: 1 };
const detail: AgentDetail = {
  agent: agent(),
  runs: [{ id: 'r1', taskId: 't12', pid: 4242, cwd: '/w/t12', status: 'running', startedAt: 1_700_000_000_000, endedAt: 0 }],
  usage: [usage({ costUsd: 0.05 }), usage({ costUsd: 0.07 })],
  isolation: { level: 'guarded', summary: 'Runs in its worktree with acceptEdits.', details: null },
};
const sk = (name: string, over: Partial<Skill> = {}): Skill => ({
  id: `skl_${name}`,
  projectId: 'prj_1',
  name,
  description: `${name}: table tests, fakes, race checks. More words after the first sentence.`,
  globs: [],
  roles: [],
  origin: 'repo',
  contentHash: 'h',
  status: 'accepted',
  audience: 'agents',
  agents: [],
  created: '',
  updated: '',
  ...over,
});
const skills: Skill[] = [sk('go-testing', { agents: ['agt_1'] }), sk('stripe-api', { audience: 'team', roles: ['backend'] }), sk('frontend-design'), sk('waiting', { status: 'offered' })];
const server = (over: Partial<McpServer>): McpServer =>
  ({ id: 'github', display: 'GitHub', transport: 'http', secretEnv: [], hasToken: true, guard: true, readOnlyTools: [], tools: [{ name: 'list_issues', readOnlyHint: true }, { name: 'get_pr', readOnlyHint: true }], discoveredAt: 1, projects: ['prj_1'], createdAt: 1, updatedAt: 1, projectId: 'prj_1', enabled: true, agents: [], ...over }) as McpServer;
const servers: McpServer[] = [server({ agents: [{ agentId: 'agt_1', tools: ['list_issues'] }] }), server({ id: 'linear', display: 'Linear', agents: [{ agentId: 'agt_2', tools: [] }] })];
const notes: MemoryEntry[] = [{ id: 'm1', projectId: 'prj_1', kind: 'convention', title: 'Money is integer cents', body: 'Money is always integer cents; never float.', status: 'accepted', source: '#9 review', created: 1, updated: 1 }];

function show(id: string, client: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'billing-api' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    agentList: async () => [agent(), rex],
    presenceList: async () => [presence],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    taskList: async () => tasks,
    agentGet: async (x) => (x === 'agt_1' ? detail : Promise.reject(new Error(`agent ${x}: not found`))),
    skillList: async () => skills,
    mcpServerList: async () => servers,
    memoryList: async () => notes,
    routeDecisions: async () => [],
    adapterList: async () => [],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  const navigated: string[] = [];
  const r = mount(<AgentScreen id={id} onNavigate={(h) => navigated.push(h)} />, s);
  return { r, s, navigated };
}

const hero = (r: ReturnType<typeof show>['r']) => waitFor(() => r.$('[data-testid="agent-hero"]') ?? (() => { throw new Error('no hero yet'); })(), r);
const chips = (r: ReturnType<typeof show>['r']) => Array.from(r.$$('[data-testid="agent-chip"]')).map((c) => c.textContent!.trim());

describe("an agent's page", () => {
  it('shows the hero: the name as the heading, the purpose, and chips with what it is on, its CLI and model, effort, where it runs and whether it is held in', async () => {
    const { r } = show('agt_1');
    await hero(r);
    const h1 = r.$('h1')!;
    expect(h1.textContent).toBe('Ada');
    expect(r.$('section[aria-labelledby]')!.getAttribute('aria-labelledby')).toBe(h1.id);
    expect(r.$('[data-testid="agent-page-purpose"]')!.textContent).toContain('Owns webhooks and invoices');
    await waitFor(() => expect(chips(r)).toContain('Working on #12'), r);
    await waitFor(() => expect(chips(r)).toContain('guarded'), r);
    expect(chips(r)).toEqual(['Working on #12', 'Claude Code · sonnet', 'effort: high', 'runs in WSL Ubuntu', 'guarded']);
    expect(r.$('[data-testid="agent-chip"][data-live="true"]')!.getAttribute('href')).toBe('/task/t12');
    expect(r.$('[data-testid="agent-chip"][data-isolation="guarded"]')!.getAttribute('title')).toContain('acceptEdits');
  });

  it('lists the crew on the left, each a link to its page, the open one marked; Message opens a direct message', async () => {
    const dms: [string, string][] = [];
    const { r, navigated } = show('agt_1', { roomCreateDM: async (p, a) => (dms.push([p, a]), { id: 'room_dm' } as never) });
    await hero(r);
    const links = Array.from(r.$$('[data-testid="crew-link"]'));
    expect(links.map((l) => l.getAttribute('href'))).toEqual(['/team/agt_1', '/team/agt_2']);
    expect(links.map((l) => l.getAttribute('aria-current'))).toEqual(['page', null]);
    expect(links[1]!.textContent).toContain('reviewer');
    expect(r.$('nav[aria-label="Crew"] a[href="/team"]')!.textContent).toContain('Add an agent');
    await r.act(() => byText(r.container, 'button', 'Message Ada').click());
    await waitFor(() => expect(navigated).toEqual(['/room/room_dm']), r);
    expect(dms).toEqual([['prj_1', 'agt_1']]);
  });

  it('Overview: the task it is on with its step and status, its activity and latest run, numbers counted from the task list, its skills, tools and what the crew remembers', async () => {
    const { r } = show('agt_1');
    await hero(r);
    const now = r.$('[data-testid="agent-now"]')!;
    await waitFor(() => expect(now.querySelector('[data-testid="agent-now-task"]')!.textContent).toContain('#12 Retry Stripe webhooks'), r);
    expect(now.querySelector('[data-testid="agent-now-task"]')!.getAttribute('href')).toBe('/task/t12');
    // REVIEW: work is done, review is current, you and done are ahead
    expect(Array.from(now.querySelectorAll('.steps span')).map((x) => x.getAttribute('data-state'))).toEqual(['done', 'current', null, null]);
    expect(now.querySelector('[data-testid="task-status"]')!.textContent).toContain('In review');
    await waitFor(() => expect(now.querySelector('[data-testid="agent-activity"]')!.textContent).toContain('tool: Write'), r);
    expect(now.querySelector('[data-testid="agent-activity"]')!.textContent).toContain('running');
    // stats: two completed tasks as worker, one as reviewer, one of two passed first time, costs and times only where reported
    expect(now.querySelector('[data-testid="stat-merged"] b')!.textContent).toBe('2');
    expect(now.querySelector('[data-testid="stat-reviewed"] b')!.textContent).toBe('1');
    expect(now.querySelector('[data-testid="stat-first-time"] b')!.textContent).toBe('50%');
    expect(now.querySelector('[data-testid="stat-avg-cost"] b')!.textContent).toBe('$0.31');
    expect(now.querySelector('[data-testid="stat-avg-time"] b')!.textContent).toBe('14 m');
    expect(now.querySelector('[data-testid="stat-spend"] b')!.textContent).toBe('$0.12');
    // skills: the ones given, the whole-team one for its role (included, its switch off limits), the accepted ones it does not have
    await waitFor(() => expect(r.$$('[data-testid="agent-skill"]').length).toBe(3), r);
    const rows = Array.from(r.$$('[data-testid="agent-skill"]'));
    expect(rows.map((x) => [x.getAttribute('data-skill'), x.getAttribute('data-on')])).toEqual([
      ['go-testing', 'true'],
      ['stripe-api', 'true'],
      ['frontend-design', 'false'],
    ]);
    expect(r.container.textContent).toContain('2 of 3 in this project');
    expect(rows[1]!.textContent).toContain('Every backend agent gets it');
    expect(rows[1]!.querySelector('button[role="switch"]')!.getAttribute('aria-disabled')).toBe('true');
    expect(rows[0]!.querySelector('button[role="switch"]')!.getAttribute('aria-disabled')).toBeNull();
    expect(rows[0]!.textContent).toContain('go-testing: table tests, fakes, race checks.');
    expect(rows[0]!.textContent).not.toContain('More words');
    // tools: only the server this agent is granted, with what it was granted
    await waitFor(() => expect(r.$$('[data-testid="agent-tool"]').length).toBe(1), r);
    expect(r.$('[data-testid="agent-tool"]')!.textContent).toContain('GitHub');
    expect(r.$('[data-testid="agent-tool"]')!.textContent).toContain('1 tool granted');
    // memory: the accepted notes, shared by the crew
    await waitFor(() => expect(r.$$('[data-testid="agent-memory"]').length).toBe(1), r);
    expect(r.$('[data-testid="agent-memory"]')!.textContent).toContain('integer cents');
    expect(r.container.textContent).toContain('1 note');
  });

  it('a skill switch gives the agent the skill or takes it away through agent.update, and the list is re-read', async () => {
    const calls: unknown[] = [];
    let listed = 0;
    const { r } = show('agt_1', { skillList: async () => (listed++, skills), agentUpdate: async (u) => (calls.push(u), agent()) });
    await hero(r);
    await waitFor(() => expect(r.$$('[data-testid="agent-skill"]').length).toBe(3), r);
    const before = listed;
    await r.act(() => r.$('[data-testid="agent-skill"][data-skill="frontend-design"] button[role="switch"]')!.click());
    await waitFor(() => expect(calls).toEqual([{ id: 'agt_1', addSkills: ['frontend-design'] }]), r);
    await waitFor(() => expect(listed).toBeGreaterThan(before), r);
    await r.act(() => r.$('[data-testid="agent-skill"][data-skill="go-testing"] button[role="switch"]')!.click());
    await waitFor(() => expect(calls.length).toBe(2), r);
    expect(calls[1]).toEqual({ id: 'agt_1', removeSkills: ['go-testing'] });
  });

  it('the tabs: Skills & tools in full, Memory, and History with its tasks, runs and usage', async () => {
    const { r } = show('agt_1');
    await hero(r);
    await r.act(() => r.$('[data-testid="agent-tab-skills"]')!.click());
    await waitFor(() => expect(r.$$('[data-testid="agent-skill"]').length).toBe(3), r);
    expect(r.$('[data-testid="agent-skill"]')!.textContent).toContain('More words after the first sentence');
    expect(r.$('[data-testid="agent-tool"]')!.textContent).toContain('list_issues');
    await r.act(() => r.$('[data-testid="agent-tab-memory"]')!.click());
    await waitFor(() => expect(r.$$('[data-testid="agent-memory"]').length).toBe(1), r);
    expect(r.container.textContent).toContain('shared by the whole crew');
    await r.act(() => r.$('[data-testid="agent-tab-history"]')!.click());
    await waitFor(() => expect(r.$$('[data-testid="agent-task"]').length).toBe(4), r);
    const first = r.$('[data-testid="agent-task"]')!;
    expect(first.getAttribute('href')).toBe('/task/t12');
    expect(first.textContent).toContain('worker');
    expect(Array.from(r.$$('[data-testid="agent-task"]'))[3]!.textContent).toContain('reviewer');
    expect(r.$$('[data-testid="agent-run"]').length).toBe(1);
    expect(r.$('[data-testid="agent-run"]')!.textContent).toContain('4242');
    expect(r.$$('[data-testid="usage-row"]').length).toBe(2);
  });

  it('an agent without presence, task, purpose or runs says so plainly, and an unknown id is not invented', async () => {
    const { r } = show('agt_2', { agentGet: async () => ({ agent: rex, runs: [], usage: [] }), presenceList: async () => [] });
    await hero(r);
    expect(r.$('h1')!.textContent).toBe('Rex');
    expect(r.container.textContent).toContain('No standing instructions');
    expect(chips(r)).toEqual(['Resting', 'Claude Code · sonnet', 'effort: high']);
    expect(r.$('[data-testid="agent-now"]')!.textContent).toContain('Not on a task right now');
    expect(r.$('[data-testid="agent-activity"]')!.textContent).toContain('No run yet');
    // Rex finished #3 as worker with no rework round, but that task reports no cost or times, and nothing was spent
    expect(r.$('[data-testid="stat-merged"] b')!.textContent).toBe('1');
    expect(r.$('[data-testid="stat-first-time"] b')!.textContent).toBe('100%');
    expect(r.$('[data-testid="stat-avg-cost"]')).toBeNull();
    expect(r.$('[data-testid="stat-avg-time"]')).toBeNull();
    expect(r.$('[data-testid="stat-spend"]')).toBeNull();
    await waitFor(() => expect(r.$$('[data-testid="agent-tool"]').length).toBe(1), r);
    expect(r.$('[data-testid="agent-tool"]')!.textContent).toContain('2 tools');
    cleanup();
    const gone = show('agt_9');
    await waitFor(() => expect(gone.r.container.textContent).toContain('No such agent'), gone.r);
    expect(gone.r.$('[data-testid="agent-hero"]')).toBeNull();
    expect(gone.r.$('a[href="/team"]')).not.toBeNull();
  });

  it('helpers: the chip of what it is on, the skills that reach it, the servers it is granted', () => {
    expect(nowChip(presence, tasks[0])).toBe('Working on #12');
    expect(nowChip(undefined, tasks[0])).toBe('On #12');
    expect(nowChip({ ...presence, taskId: undefined, activity: 'reading' }, undefined)).toBe('reading');
    expect(nowChip(undefined, undefined)).toBe('Resting');
    const team = [{ id: 'agt_1' }, { id: 'agt_2' }];
    expect(skillsOf(skills, agent(), team).mine.map((k) => k.name)).toEqual(['go-testing', 'stripe-api']);
    expect(skillsOf(skills, rex, team).mine.map((k) => k.name)).toEqual([]);
    expect(skillsOf(skills, rex, team).accepted.length).toBe(3);
    // a skill given to an agent no longer on the team does not count as given
    expect(skillsOf([sk('x', { agents: ['agt_gone'] })], agent(), team).mine).toEqual([]);
    expect(serversOf(servers, 'agt_2').map((x) => x.id)).toEqual(['linear']);
  });
});
