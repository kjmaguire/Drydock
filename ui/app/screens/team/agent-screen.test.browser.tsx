import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { LibrarySkill, MemoryEntry, Skill } from '../../api/extTypes.ts';
import type { McpServer } from '../../api/mcpClient.ts';
import type { Agent, AgentDetail, PresenceState, Project, Task } from '../../api/types.ts';
import { lic, usage, usageTotals } from '../../test/fixtures.ts';
import { byText, cleanup, fromMenu, menuItems, mount, testServices, type, waitFor } from '../../test/render.tsx';
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
  usageTotals: usageTotals([usage({ costUsd: 0.05 }), usage({ costUsd: 0.07 })]),
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
// skills belong to agents (P76): go-testing is Ada's; stripe-api names her role (a suggestion, not hers); frontend-design is nobody's
const skills: Skill[] = [sk('go-testing', { agents: ['agt_1'] }), sk('stripe-api', { roles: ['backend'] }), sk('frontend-design'), sk('waiting', { status: 'offered' })];
const libSkill = (name: string, over: Partial<LibrarySkill> = {}): LibrarySkill => ({
  id: `l-${name}`,
  registryId: 'cloudflare-skills',
  repo: 'cloudflare/skills',
  name,
  description: `${name} from the Library.`,
  license: 'Apache-2.0',
  globs: [],
  roles: [],
  sourcePath: `skills/${name}`,
  pinnedCommit: 'abc',
  contentHash: 'h',
  hasScripts: false,
  status: 'available',
  blockReason: '',
  risk: 0,
  flags: [],
  gone: false,
  updated: '',
  ...over,
});
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
    skillLibraryList: async () => [libSkill('wrangler'), libSkill('stripe-api'), libSkill('evil', { status: 'blocked' })],
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
    expect(chips(r)).toEqual(['Working on #12', 'Claude Code · sonnet', 'effort: high', 'runs in WSL', 'guarded']);
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

  it('Inspect opens the inspector in the details panel; Remove asks first, then removes the agent and goes back to the crew', async () => {
    const archived: string[] = [];
    const { r, s, navigated } = show('agt_1', { agentArchive: async (id) => (archived.push(id), agent()) });
    await hero(r);
    // Message and Edit are buttons; the rest is in "..." with Remove last, in the danger colour (UI audit 2026-10-26)
    const more = r.$('[data-testid="agent-more"]') as HTMLElement;
    expect(await menuItems(r, more)).toEqual(['Inspect', 'All agents', 'Remove Ada...']);
    expect(r.$('[role="menuitem"][data-tone="danger"]')!.textContent!.trim()).toBe('Remove Ada...');
    expect(r.$('button[aria-label="Remove Ada"]')).toBeNull();
    await fromMenu(r, more, 'Inspect');
    expect(s.ui.inspectId).toBe('agt_1');
    expect(s.ui.tab).toBe('inspector');
    await fromMenu(r, more, 'Remove Ada...');
    const box = await waitFor(() => r.$('[data-testid="agent-remove"]') ?? (() => { throw new Error('no dialog'); })(), r);
    expect(archived).toEqual([]);
    expect(box.textContent).toContain('Remove Ada from the team? Its history stays.');
    await r.act(() => byText(box as HTMLElement, 'button', 'Yes, remove').click());
    await waitFor(() => expect(archived).toEqual(['agt_1']), r);
    await waitFor(() => expect(navigated).toEqual(['/team']), r);
  });

  it("the team lead's page carries its stand-up and its interventions; another agent's page has neither", async () => {
    const lead = agent({ id: 'agt_l', name: 'Lead', role: 'lead' });
    const { r } = show('agt_l', {
      agentList: async () => [agent(), lead],
      agentGet: async () => ({ ...detail, agent: lead }),
      leadInterventions: async () => [],
    });
    await hero(r);
    const sec = await waitFor(() => r.$('section[aria-label="Team lead"]') ?? (() => { throw new Error('no lead section'); })(), r);
    await waitFor(() => byText(sec as HTMLElement, 'button', 'Run stand-up'), r);
    expect(sec.textContent).toContain('steps in when a task loops');
    cleanup();
    const other = show('agt_1', { agentList: async () => [agent(), lead], leadInterventions: async () => [] });
    await hero(other.r);
    expect(other.r.$('section[aria-label="Team lead"]')).toBeNull();
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
    // the run's status in words, never the core's constant
    expect(now.querySelector('[data-testid="agent-activity"]')!.textContent).toContain('Running · started');
    // stats: two completed tasks as worker, one as reviewer, one of two passed first time, costs and times only where reported
    expect(now.querySelector('[data-testid="stat-merged"] b')!.textContent).toBe('2');
    expect(now.querySelector('[data-testid="stat-reviewed"] b')!.textContent).toBe('1');
    expect(now.querySelector('[data-testid="stat-first-time"] b')!.textContent).toBe('50%');
    expect(now.querySelector('[data-testid="stat-avg-cost"] b')!.textContent).toBe('$0.31');
    expect(now.querySelector('[data-testid="stat-avg-time"] b')!.textContent).toBe('14 m');
    expect(now.querySelector('[data-testid="stat-spend"] b')!.textContent).toBe('$0.12');
    // skills: only the ones given to her (P76: the one naming her role is not hers until given), each with Remove
    await waitFor(() => expect(r.$$('[data-testid="agent-skill"]').length).toBe(1), r);
    const rows = Array.from(r.$$('[data-testid="agent-skill"]'));
    expect(rows.map((x) => x.getAttribute('data-skill'))).toEqual(['go-testing']);
    expect(r.container.textContent).toContain('1 given to Ada');
    expect(rows[0]!.querySelector('[data-testid="agent-skill-remove"]')!.getAttribute('aria-label')).toBe('Remove go-testing from Ada');
    expect(r.$('[data-testid="agent-skills-more"]')!.textContent).toBe('Add skills');
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

  it('Remove takes a skill from the agent; Add gives a project skill, or adds a Library skill for this agent only, suggested ones first', async () => {
    const calls: unknown[] = [];
    const libAdds: unknown[][] = [];
    let listed = 0;
    const { r } = show('agt_1', {
      skillList: async () => (listed++, skills),
      agentUpdate: async (u) => (calls.push(u), agent()),
      skillLibraryAdd: async (...a: unknown[]) => (libAdds.push(a), sk('wrangler', { agents: ['agt_1'] })),
    });
    await hero(r);
    await waitFor(() => expect(r.$$('[data-testid="agent-skill"]').length).toBe(1), r);
    const before = listed;
    await r.act(() => (r.$('[data-testid="agent-skill"][data-skill="go-testing"] [data-testid="agent-skill-remove"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual([{ id: 'agt_1', removeSkills: ['go-testing'] }]), r);
    await waitFor(() => expect(listed).toBeGreaterThan(before), r);

    // Add skills opens the Skills tab: the project's skills she lacks, then the Library's not in the project (no blocked one)
    await r.act(() => (r.$('[data-testid="agent-skills-more"]') as HTMLButtonElement).click());
    const choices = () => Array.from(r.$$('[data-testid="agent-skill-choice"]')).map((x) => `${x.getAttribute('data-from')}:${x.getAttribute('data-skill')}`);
    await waitFor(() => expect(choices()).toEqual(['project:stripe-api', 'project:frontend-design', 'library:wrangler']), r);
    expect(r.$('[data-testid="agent-skill-choice"][data-skill="stripe-api"]')!.textContent).toContain('suggested for backend');
    expect(r.$('[data-testid="agent-skill-choice"][data-skill="wrangler"]')!.textContent).toContain('cloudflare/skills');
    await r.act(() => (r.$('[data-testid="agent-skill-choice"][data-skill="stripe-api"] [data-testid="agent-skill-add"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls[1]).toEqual({ id: 'agt_1', addSkills: ['stripe-api'] }), r);
    // a Library skill goes to the project for her alone
    await r.act(() => (r.$('[data-testid="agent-skill-choice"][data-skill="wrangler"] [data-testid="agent-skill-add"]') as HTMLButtonElement).click());
    await waitFor(() => expect(libAdds).toEqual([['prj_1', 'cloudflare-skills', 'wrangler', 'agents', ['agt_1']]]), r);
    await waitFor(() => expect(r.$('[data-testid="agent-skill-added"]')!.textContent).toBe('wrangler was given to Ada.'), r);
    // the words typed narrow the list
    await r.act(() => type(r.$('input[type="search"]') as HTMLInputElement, 'wrang'));
    await waitFor(() => expect(choices()).toEqual(['library:wrangler']), r);
  });

  it('the tabs: Skills & tools in full, Memory, and History with its tasks, runs and usage', async () => {
    const { r } = show('agt_1');
    await hero(r);
    await r.act(() => r.$('[data-testid="agent-tab-skills"]')!.click());
    await waitFor(() => expect(r.$$('[data-testid="agent-skill"]').length).toBe(1), r);
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

  it("a stopped agent's activity block does not repeat that it stopped: the hero's chip says it", async () => {
    const stopped: PresenceState = { ...presence, taskId: '', presence: 'Paused', label: 'stopped', activity: 'stopped' };
    const { r } = show('agt_1', { presenceList: async () => [stopped] });
    await hero(r);
    await waitFor(() => expect(chips(r)[0]).toContain('Stopped'), r);
    const block = r.$('[data-testid="agent-activity"]')!.textContent!;
    expect(block.includes('●')).toBe(false);
    expect(block).toContain('· started');
    expect(r.$('[data-testid="agent-now"] h2')!.textContent).toBe('Right now');
  });

  it('an agent without presence, task, purpose or runs says so plainly, and an unknown id is not invented', async () => {
    const { r } = show('agt_2', { agentGet: async () => ({ agent: rex, runs: [], usage: [], usageTotals: usageTotals() }), presenceList: async () => [] });
    await hero(r);
    expect(r.$('h1')!.textContent).toBe('Rex');
    expect(r.container.textContent).toContain('No standing instructions');
    expect(chips(r)).toEqual(['Stopped', 'Claude Code · sonnet', 'effort: high']);
    expect(r.$('[data-testid="agent-now"]')!.textContent).toContain('Not on a task right now');
    expect(r.$('[data-testid="agent-activity"]')!.textContent).toContain('No run yet');
    // "Stopped" is said once, in the hero's chip: not again beside Right now (UI audit 2026-10-26)
    expect(r.$('[data-testid="agent-now"] h2')!.textContent).toBe('Right now');
    expect(/stopped/i.test(r.$('[data-testid="agent-now"]')!.textContent!)).toBe(false);
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

  it('the spend is the core\'s sum over every usage row, labelled with how many, and the list says it holds only the newest 50 (M21)', async () => {
    // 50 listed rows of half a cent, but 120 rows in all that cost $60: the page must not add up the 50
    const newest = Array.from({ length: 50 }, () => usage({ costUsd: 0.005 }));
    const many: AgentDetail = { ...detail, usage: newest, usageTotals: usageTotals(newest, { rows: 120, costUsd: 60, estimated: 30, inputTokens: 1200, outputTokens: 600 }) };
    const { r } = show('agt_1', { agentGet: async () => many });
    await hero(r);
    const spend = await waitFor(() => r.$('[data-testid="stat-spend"]') ?? (() => { throw new Error('no spend yet'); })(), r);
    expect(spend.querySelector('b')!.textContent).toBe('$60.00');
    expect(spend.querySelector('small')!.textContent).toBe('spent so far, 30 of 120 costs estimated');
    await r.act(() => r.$('[data-testid="agent-tab-history"]')!.click());
    await waitFor(() => expect(r.$$('[data-testid="usage-row"]').length).toBe(50), r);
    expect(r.$('section[aria-label="Usage"] h2')!.textContent).toContain('$60.00 in all 120 rows');
    expect(r.$('[data-testid="usage-cut"]')!.textContent).toBe('Showing the newest 50 of 120 usage rows.');
    expect(r.container.textContent).not.toContain('$0.25');
  });

  it('says nothing is cut when the list holds every usage row, and shows no spend for an agent with none', async () => {
    const { r } = show('agt_1');
    await hero(r);
    await waitFor(() => expect(r.$('[data-testid="stat-spend"] b')!.textContent).toBe('$0.12'), r);
    expect(r.$('[data-testid="stat-spend"] small')!.textContent).toBe('spent so far');
    await r.act(() => r.$('[data-testid="agent-tab-history"]')!.click());
    await waitFor(() => expect(r.$$('[data-testid="usage-row"]').length).toBe(2), r);
    expect(r.$('[data-testid="usage-cut"]')).toBeNull();
    expect(r.$('section[aria-label="Usage"] h2')!.textContent).toContain('$0.12 in all 2 rows');
  });

  it('says the numbers and the task list come from the newest tasks while the project has more (M21)', async () => {
    const { r } = show('agt_1', { taskCount: async () => ({ total: 999 }) });
    await hero(r);
    const note = await waitFor(() => r.$('[data-testid="task-total-notice"]') ?? (() => { throw new Error('no notice yet'); })(), r);
    expect(note.textContent).toContain('Showing the newest 4 of 999 tasks.');
    expect(note.textContent).toContain('counted from these tasks only');
    cleanup();
    const all = show('agt_1', { taskCount: async () => ({ total: 4 }) });
    await hero(all.r);
    await waitFor(() => expect(all.r.$('[data-testid="stat-merged"] b')!.textContent).toBe('2'), all.r);
    expect(all.r.$('[data-testid="task-total-notice"]')).toBeNull();
  });

  it('marks an agent whose permission mode skips approvals in its hero and in the crew list, and no other (M1)', async () => {
    const risky = agent({ permissionMode: 'bypassPermissions' });
    const { r } = show('agt_1', { agentList: async () => [risky, rex], agentGet: async () => ({ ...detail, agent: risky }) });
    await hero(r);
    await waitFor(() => expect(r.$('[data-testid="agent-hero"] [data-testid="skips-approvals-badge"]')).not.toBeNull(), r);
    expect(r.$('[data-testid="agent-hero"] [data-testid="skips-approvals-badge"]')!.textContent).toBe('skips approvals');
    const [first, second] = Array.from(r.$$('[data-testid="crew-link"]'));
    expect(first!.querySelector('[data-testid="skips-approvals-badge"]')).not.toBeNull();
    expect(second!.querySelector('[data-testid="skips-approvals-badge"]')).toBeNull();
    // the chips the hero lists are still its own: the badge is not one of them
    expect(chips(r)).not.toContain('skips approvals');
  });

  describe('a project with hundreds of skills and notes (L37)', () => {
    const N = 400;
    // every one given to Ada: her own list is the long one
    const manySkills: Skill[] = Array.from({ length: N }, (_, i) => sk(`skill-${String(i).padStart(3, '0')}`, { agents: ['agt_1'] }));
    const manyNotes: MemoryEntry[] = Array.from({ length: N }, (_, i) => ({
      id: `m${i}`,
      projectId: 'prj_1',
      kind: 'convention',
      title: `Note number ${i}`,
      body: `Body of note ${i}.\n\nA second paragraph of note ${i}.`,
      status: 'accepted',
      source: `#${i} review`,
      created: i,
      updated: i,
    }));
    const frames = () => new Promise<void>((res) => requestAnimationFrame(() => requestAnimationFrame(() => res())));
    type R = ReturnType<typeof show>['r'];
    const skillRows = (r: R) => Array.from(r.$$('[data-testid="agent-skill"]')) as HTMLElement[];
    const noteRows = (r: R) => Array.from(r.$$('[data-testid="agent-memory"]')) as HTMLElement[];
    /** Scrolls a scroller to `top` and lets the window follow. */
    const scrollTo = async (r: R, sc: HTMLElement, top: number) => {
      const before = sc.scrollTop;
      const moved = new Promise((res) => sc.addEventListener('scroll', res, { once: true }));
      sc.scrollTop = top;
      if (sc.scrollTop !== before) await moved;
      r.root.flush();
    };

    it('draws only the skills near the view in the Skills & tools tab, reaches the first and the last, and the Remove of a row that was scrolled to still works', async () => {
      const calls: unknown[] = [];
      const { r } = show('agt_1', { skillList: async () => manySkills, agentUpdate: async (u) => (calls.push(u), agent()) });
      await hero(r);
      // the overview shows five, whole, and says how many there are
      await waitFor(() => expect(skillRows(r).length).toBe(5), r);
      expect(r.$('[data-testid="agent-skills-list"]')!.getAttribute('data-windowed')).toBe('false');
      expect(byText(r.container, 'button', `All ${N} skills, and add more`)).toBeTruthy();

      await r.act(() => r.$('[data-testid="agent-tab-skills"]')!.click());
      const sc = await waitFor(() => (r.$('[data-testid="agent-skills-list"][data-windowed="true"]') as HTMLElement | null) ?? (() => { throw new Error('no scroller yet'); })(), r);
      await waitFor(() => expect(skillRows(r).length).toBeGreaterThan(3), r);
      await frames();
      // a named list that can be reached by keyboard and holds a window of the skills, not all of them
      expect(sc.getAttribute('role')).toBe('list');
      expect(sc.getAttribute('aria-label')).toBe("Ada's skills");
      expect(sc.getAttribute('tabindex')).toBe('0');
      expect(sc.getAttribute('data-rows')).toBe(String(N));
      expect(skillRows(r).length).toBeLessThan(80);
      expect(skillRows(r).every((x) => x.getAttribute('role') === 'listitem')).toBe(true);
      expect(getComputedStyle(sc).overflowY).toBe('auto');
      expect(sc.clientHeight).toBeLessThanOrEqual(640);
      expect(sc.scrollHeight).toBeGreaterThan(N * 40);
      expect(skillRows(r)[0]!.getAttribute('data-skill')).toBe('skill-000');

      await scrollTo(r, sc, sc.scrollHeight);
      await waitFor(() => expect(skillRows(r).at(-1)!.getAttribute('data-skill')).toBe(`skill-${N - 1}`), r);
      expect(skillRows(r).some((x) => x.getAttribute('data-skill') === 'skill-000')).toBe(false);
      expect(skillRows(r).length).toBeLessThan(80);
      // Remove in the window works as it does in a short list
      await r.act(() => (r.$(`[data-testid="agent-skill"][data-skill="skill-${N - 1}"] [data-testid="agent-skill-remove"]`) as HTMLButtonElement).click());
      await waitFor(() => expect(calls).toEqual([{ id: 'agt_1', removeSkills: [`skill-${N - 1}`] }]), r);
      // and the way back to the top
      await scrollTo(r, sc, 0);
      await waitFor(() => expect(skillRows(r)[0]!.getAttribute('data-skill')).toBe('skill-000'), r);
    });

    it('draws only the notes near the view in the Memory tab, each as Markdown, and reaches the first and the last', async () => {
      const { r } = show('agt_1', { memoryList: async () => manyNotes });
      await hero(r);
      // the overview shows three, whole
      await waitFor(() => expect(noteRows(r).length).toBe(3), r);
      expect(r.$('[data-testid="agent-memory-list"]')).toBeNull();
      await r.act(() => r.$('[data-testid="agent-tab-memory"]')!.click());
      const sc = await waitFor(() => (r.$('[data-testid="agent-memory-list"][data-windowed="true"]') as HTMLElement | null) ?? (() => { throw new Error('no scroller yet'); })(), r);
      await waitFor(() => expect(noteRows(r).length).toBeGreaterThan(2), r);
      await frames();
      expect(sc.getAttribute('role')).toBe('list');
      expect(sc.getAttribute('aria-label')).toBe('Accepted notes');
      expect(sc.getAttribute('tabindex')).toBe('0');
      expect(sc.getAttribute('data-rows')).toBe(String(N));
      expect(noteRows(r).length).toBeLessThan(80);
      expect(noteRows(r).every((x) => x.getAttribute('role') === 'listitem')).toBe(true);
      expect(sc.clientHeight).toBeLessThanOrEqual(640);
      expect(sc.scrollHeight).toBeGreaterThan(N * 60);
      // a note is still drawn as Markdown: two paragraphs, its title, its kind and where it came from
      const first = noteRows(r)[0]!;
      expect(first.querySelector('b')!.textContent).toBe('Note number 0');
      expect(first.querySelectorAll('p').length).toBe(2);
      expect(first.textContent).toContain('A second paragraph of note 0.');
      expect(first.textContent).toContain('convention · from #0 review');

      await scrollTo(r, sc, sc.scrollHeight);
      await waitFor(() => expect(noteRows(r).at(-1)!.querySelector('b')!.textContent).toBe(`Note number ${N - 1}`), r);
      expect(noteRows(r).some((x) => x.querySelector('b')!.textContent === 'Note number 0')).toBe(false);
      expect(noteRows(r).length).toBeLessThan(80);
      await scrollTo(r, sc, 0);
      await waitFor(() => expect(noteRows(r)[0]!.querySelector('b')!.textContent).toBe('Note number 0'), r);
    });

    it('a short list in these tabs is drawn whole, in the page, as a list of items', async () => {
      const { r } = show('agt_1');
      await hero(r);
      await r.act(() => r.$('[data-testid="agent-tab-skills"]')!.click());
      await waitFor(() => expect(skillRows(r).length).toBe(1), r);
      const list = r.$('[data-testid="agent-skills-list"]')!;
      expect(list.getAttribute('data-windowed')).toBe('false');
      expect(list.getAttribute('role')).toBe('list');
      expect(list.getAttribute('tabindex')).toBeNull();
      expect(skillRows(r).every((x) => x.getAttribute('role') === 'listitem' && list.contains(x))).toBe(true);
      await r.act(() => r.$('[data-testid="agent-tab-memory"]')!.click());
      await waitFor(() => expect(noteRows(r).length).toBe(1), r);
      expect(r.$('[data-testid="agent-memory-list"]')!.getAttribute('data-windowed')).toBe('false');
    });
  });

  it('helpers: the chip of what it is on, the skills that reach it, the servers it is granted', () => {
    expect(nowChip(presence, tasks[0])).toBe('Working on #12');
    expect(nowChip(undefined, tasks[0])).toBe('On #12');
    expect(nowChip({ ...presence, taskId: undefined, activity: 'reading' }, undefined)).toBe('reading');
    expect(nowChip(undefined, undefined)).toBe('Stopped');
    const team = [{ id: 'agt_1' }, { id: 'agt_2' }];
    expect(skillsOf(skills, agent(), team).mine.map((k) => k.name)).toEqual(['go-testing']); // the one naming her role is not hers
    expect(skillsOf(skills, rex, team).mine.map((k) => k.name)).toEqual([]);
    expect(skillsOf(skills, rex, team).accepted.length).toBe(3);
    // a skill given to an agent no longer on the team does not count as given
    expect(skillsOf([sk('x', { agents: ['agt_gone'] })], agent(), team).mine).toEqual([]);
    expect(serversOf(servers, 'agt_2').map((x) => x.id)).toEqual(['linear']);
  });
});
