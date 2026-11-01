import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Skill } from '../../api/extTypes.ts';
import type { AgentLane, RunSpan, WaitSpan } from '../../api/inboxClient.ts';
import type { AgentTemplate, ClaudePractices } from '../../api/libraryClient.ts';
import type { AdapterInfo, Agent, PrebuiltAgent, PresenceState, Project, TaskSummary } from '../../api/types.ts';
import { SKIP_CONFIRM_LABEL } from '../../lib/permission.ts';
import { expiredLicense, lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, drain, fromMenu, menuItems, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { CATALOG, prebuilt } from '../project/wizard-fixtures.ts';
import { AgentEditor } from './agent-panel.tsx';
import { TeamScreen, WorkspaceCrewScreen } from './team-screen.tsx';

afterEach(cleanup);

const claude: AdapterInfo = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready' },
  capabilities: { modelSelection: true, effortControl: true, efforts: ['low', 'medium', 'high', 'xhigh', 'max'], models: ['opus', 'sonnet', 'haiku'] } as AdapterInfo['capabilities'],
};
const opencode: AdapterInfo = {
  id: 'opencode',
  detection: { adapter: 'opencode', installed: true, status: 'ready' },
  capabilities: { modelSelection: true, effortControl: true } as AdapterInfo['capabilities'],
};
const agent = (over: Partial<Agent> = {}): Agent => ({
  id: 'agt_1',
  projectId: 'prj_1',
  name: 'Pay',
  adapter: 'claude',
  role: 'backend',
  model: 'sonnet',
  effort: 'high',
  providerId: '',
  permissionMode: 'acceptEdits',
  purpose: 'Own the payments service.',
  status: 'STOPPED',
  ...over,
});

// one day in local time: the page is drawn at 16:00 (its clock is given), its timeline from 08:00
const day = (h: number, m = 0) => new Date(2026, 9, 22, h, m).getTime();
const NOW = day(16);
// TEST DOUBLES (labelled): an agent's lane of agent.timeline, empty unless a test gives it runs or waits
const lane = (a: Agent, over: Partial<AgentLane> = {}): AgentLane => ({ agentId: a.id, name: a.name, projectId: a.projectId, role: a.role, adapter: a.adapter, model: a.model, runs: [], waits: [], ...over });

function svc(agents: Agent[], client: Partial<Client> = {}, adapters = [claude, opencode]) {
  let listed = 0;
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc', key: 'GOC' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    agentList: async () => (listed++, agents),
    presenceList: async () => [],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    taskList: async () => [],
    adapterList: async () => adapters,
    agentTemplateList: async () => [],
    skillList: async () => [],
    crewExtras: async () => ({ skills: [], rooms: [] }),
    agentTimeline: async (since) => ({ since, until: NOW, agents: agents.map((a) => lane(a)) }),
    taskSummaries: async () => ({ summaries: [], total: 0, partial: false }),
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return { s, listed: () => listed };
}

function show(agents: Agent[], client: Partial<Client> = {}, adapters = [claude, opencode]) {
  const { s, listed } = svc(agents, client, adapters);
  const navigated: string[] = [];
  const r = mount(<TeamScreen now={() => NOW} onNavigate={(h) => navigated.push(h)} />, s);
  return { r, s, navigated, listed };
}

function must<T>(x: T | null | undefined, msg: string): T {
  if (x === null || x === undefined) throw new Error(msg);
  return x;
}

async function waitAdapters(r: ReturnType<typeof show>['r']) {
  await waitFor(() => {
    if (optionsOf(byLabel(r.container, 'Adapter')).length === 0) throw new Error('no adapters yet');
  }, r);
}

async function waitAgent(r: ReturnType<typeof show>['r'], name: string) {
  return waitFor(() => {
    const el = r.container.querySelector<HTMLElement>(`[data-testid="agent"][data-name="${name}"]`);
    if (!el) throw new Error(`no agent ${name}`);
    return el;
  }, r);
}

const editor = (r: ReturnType<typeof show>['r']) => r.container.querySelector<HTMLElement>('[data-testid="agent-editor"]');

/** Opens the header's New agent dialog (the form is in it) and waits for the adapters. */
async function openNew(r: ReturnType<typeof show>['r']) {
  const btn = await waitFor(() => {
    const b = byText<HTMLButtonElement>(r.container, 'button', /^New agent/);
    if (b.disabled) throw new Error('no project yet');
    return b;
  }, r);
  await r.act(() => btn.click());
  // New agent opens on Describe; the full form is Configure (decision P138)
  const tab = await waitFor(() => must(r.container.querySelector<HTMLElement>('dialog[open] [data-testid="new-agent-configure"]'), 'no tabs'), r);
  await r.act(() => tab.click());
  await waitFor(() => must(r.container.querySelector('dialog[open] [data-testid="agent-form"]'), 'no form'), r);
  await waitAdapters(r);
}

/** Opens the header's Templates dialog: the saved templates, then the prebuilt gallery. */
async function openTemplates(r: ReturnType<typeof show>['r']) {
  const btn = await waitFor(() => {
    const b = byText<HTMLButtonElement>(r.container, 'button', 'Templates');
    if (b.disabled) throw new Error('no project yet');
    return b;
  }, r);
  await r.act(() => btn.click());
  return waitFor(() => must(r.container.querySelector<HTMLElement>('dialog[open] [data-testid="crew-templates"]'), 'no dialog'), r);
}

/** The agent editor panel's form as the Crew card or the agent's page opens it, with the services the page has. */
function edit(a: Agent, client: Partial<Client> = {}, others: Agent[] = []) {
  const { s } = svc([a, ...others], client);
  let done = 0;
  const r = mount(<AgentEditor agent={a} adapter={claude} caps="ready" onDone={() => done++} onCancel={() => {}} />, s);
  return { r, s, done: () => done };
}

describe('Team: purpose, model and effort', () => {
  it('creates an agent with a purpose, a model from the adapter and an effort level from the adapter list', async () => {
    const calls: unknown[] = [];
    const { r, listed } = show([], { agentCreate: async (spec) => (calls.push(spec), { ...agent(), ...spec } as Agent) });
    await openNew(r);
    type(byLabel(r.container, 'Agent name'), 'Pay');
    type(byLabel(r.container, 'Role'), 'backend');
    type(byLabel<HTMLTextAreaElement>(r.container, 'Purpose (optional)'), ' Own payments. ');
    type(byLabel(r.container, 'Model'), 'opus');
    const effort = byLabel<HTMLElement>(r.container, 'Effort');
    expect(effort.getAttribute('role')).toBe('combobox');
    expect(optionsOf(effort).map((o) => o.value)).toEqual(['', 'low', 'medium', 'high', 'xhigh', 'max']);
    type(effort, 'max');
    r.root.flush();
    const before = listed();
    await r.act(() => byText(r.container, 'button', 'Create agent').click());
    await waitFor(() => expect(calls).toEqual([{ projectId: 'prj_1', name: 'Pay', role: 'backend', purpose: 'Own payments.', adapter: 'claude', model: 'opus', effort: 'max', practices: true }]), r);
    // the dialog closes and the team is re-read
    await waitFor(() => expect(r.container.querySelector('dialog[open]')).toBe(null), r);
    await waitFor(() => expect(listed()).toBeGreaterThan(before), r);
  });

  it('keeps effort free text for an adapter that does not list its levels', async () => {
    const { r } = show([]);
    await openNew(r);
    type(byLabel<HTMLElement>(r.container, 'Adapter'), 'opencode');
    r.root.flush();
    expect(byLabel(r.container, 'Effort').tagName).toBe('INPUT');
  });

  it('says what a team needs when there is no agent yet', async () => {
    const { r } = show([]);
    await waitFor(() => expect(r.container.textContent).toContain('A task needs a worker and a different agent as reviewer'), r);
    expect(r.$$('[data-testid="agent"]').length).toBe(0);
  });

  it('the editor changes model, effort and purpose of an existing agent', async () => {
    const calls: unknown[] = [];
    const { r, done } = edit(agent(), { agentUpdate: async (u) => (calls.push(u), { ...agent(), ...u } as Agent) });
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    // the CLI is shown, not offered: a different CLI is a new agent
    expect(must(ed.querySelector('[data-testid="agent-runs-on"]'), 'no Runs on').textContent).toBe('Claude Code');
    type(byLabel(ed, 'Model'), 'haiku');
    // effort is a segmented choice of the CLI's levels, and the default
    const effort = must(ed.querySelector<HTMLElement>('[role="radiogroup"][aria-label="Effort"]'), 'no Effort');
    expect(Array.from(effort.querySelectorAll('[role="radio"]')).map((b) => b.textContent)).toEqual(['default', 'low', 'medium', 'high', 'xhigh', 'max']);
    expect(effort.querySelector('[aria-checked="true"]')!.textContent).toBe('high');
    await r.act(() => effort.querySelector<HTMLButtonElement>('[data-value="low"]')!.click());
    type(byLabel<HTMLTextAreaElement>(ed, 'Instructions'), 'Billing only.');
    r.root.flush();
    await r.act(() => byText(ed, 'button', 'Save').click());
    // only what changed is sent (an Everywhere save carries exactly these)
    await waitFor(() => expect(calls).toEqual([{ id: 'agt_1', purpose: 'Billing only.', model: 'haiku', effort: 'low' }]), r);
    await waitFor(() => expect(done()).toBe(1), r);
  });

  it('shows the core refusal and keeps the editor open', async () => {
    const { r, done } = edit(agent(), {
      agentUpdate: async () => {
        throw new Error('claude accepts these effort levels: low, medium, high, xhigh, max (got "ultra")');
      },
    });
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await r.act(() => byText(ed, 'button', 'Save').click());
    await waitFor(() => expect(ed.querySelector('[role="alert"]')!.textContent).toContain('accepts these effort levels'), r);
    expect(done()).toBe(0);
  });

  it('sends routed models only when the human turned routing on', async () => {
    const calls: Record<string, unknown>[] = [];
    const { r } = edit(agent(), { agentUpdate: async (u) => (calls.push(u as never), { ...agent(), ...u } as Agent) });
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    const routing = ed.querySelector<HTMLElement>('[data-testid="route-models"]')!;
    await r.act(() => routing.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
    expect(valueOf(byLabel(ed, 'Routed model 1'))).toBe('haiku');
    expect(valueOf(byLabel(ed, 'Tier of routed model 2'))).toBe('premium');
    await r.act(() => byText(ed, 'button', 'Save').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]!.routeModels).toEqual([
      { model: 'haiku', tier: 'cheap' },
      { model: 'opus', tier: 'premium' },
    ]);
  });

  it('removes an agent only after a confirmation, and shows why when it is refused', async () => {
    const archived: string[] = [];
    const { r } = edit(agent(), {
      agentArchive: async (id) => {
        archived.push(id);
        throw new Error('Pay still has unfinished tasks (Do billing): reassign or cancel them first');
      },
    });
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await r.act(() => byText(ed, 'button', 'Remove from team').click());
    expect(archived).toEqual([]);
    expect(ed.textContent).toContain('Its history stays');
    await r.act(() => byText(ed, 'button', 'Yes, remove').click());
    await waitFor(() => expect(archived).toEqual(['agt_1']), r);
    await waitFor(() => expect(ed.querySelector('[role="alert"]')!.textContent).toContain('unfinished tasks'), r);
  });

  it('read-only: creating is turned off with the reason, and the note says so', async () => {
    const { r } = show([], { licenseStatus: async () => expiredLicense });
    await openNew(r);
    await waitFor(() => must(r.container.querySelector('[data-testid="readonly-note"]'), 'no note'), r);
    type(byLabel(r.container, 'Agent name'), 'blocked');
    type(byLabel(r.container, 'Role'), 'backend');
    r.root.flush();
    const make = byText<HTMLButtonElement>(r.container, 'button', 'Create agent');
    expect(make.disabled).toBe(true);
    expect(make.title).toBe(expiredLicense.banner!);
  });
});

const template: AgentTemplate = { id: 'tpl_1', name: 'Payments dev', role: 'backend', purpose: 'Own payments.', adapter: 'claude', model: 'sonnet', effort: 'high', permissionMode: '', providerId: '', mcpServers: ['docs'], skills: [], createdAt: 1, updatedAt: 1 };
const noSkills = { assigned: [], shared: [], pending: [], failed: [] };

describe('Team: templates', () => {
  it('adds an agent from a template and says what it was granted', async () => {
    const calls: unknown[][] = [];
    const { r, listed } = show([], {
      agentTemplateList: async () => [template],
      agentTemplateApply: async (...a) => (calls.push(a), { agent: agent({ name: 'Payments dev' }), granted: ['docs'], skipped: [], skills: noSkills }),
    });
    await openTemplates(r);
    const name = await waitFor(() => byLabel(r.container, 'Name (optional)'), r);
    type(name, ' Billing ');
    r.root.flush();
    const before = listed();
    await r.act(() => byText(r.container, 'button', 'Add to this team').click());
    await waitFor(() => expect(calls).toEqual([['tpl_1', 'prj_1', 'Billing']]), r);
    await waitFor(() => expect(r.container.querySelector('[role="status"]')!.textContent).toContain('Payments dev joined the team, with docs'), r);
    await waitFor(() => expect(listed()).toBeGreaterThan(before), r);
  });

  it('points to the Library when there is no template', async () => {
    const { r } = show([]);
    await openTemplates(r);
    await waitFor(() => expect(r.container.textContent).toContain('can be added here with one click'), r);
    expect(r.container.querySelector('a[href="/library"]')).toBeTruthy();
  });

  it('saves an existing agent as a template and points to the Library', async () => {
    const calls: string[] = [];
    const { r } = edit(agent(), { agentTemplateFromAgent: async (id) => (calls.push(id), template) });
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await r.act(() => byText(ed, 'button', 'Save as a template').click());
    await waitFor(() => expect(calls).toEqual(['agt_1']), r);
    await waitFor(() => expect(ed.querySelector('[data-testid="saved-as-template"]')!.textContent).toContain('Payments dev'), r);
  });
});

describe('Claude best practices', () => {
  const practices = (role: string): ClaudePractices => ({
    version: 1,
    role,
    family: role === 'reviewer' ? 'reviewer' : 'worker',
    text: role === 'reviewer' ? 'Working practices ... You do not modify files.' : 'Working practices ... Never skip, delete or weaken a test',
    preset: role === 'reviewer' ? { model: 'sonnet', effort: 'high', why: 'Review is where verification matters.' } : { model: 'sonnet', effort: 'medium', why: 'Sonnet handles most coding tasks.' },
  });

  it('fills the recommended model and effort for the role until the human changes them, and shows exactly what is added', async () => {
    const asked: string[] = [];
    const created: Record<string, unknown>[] = [];
    const { r } = show([], {
      claudePractices: async (role) => (asked.push(role), practices(role)),
      agentCreate: async (spec) => (created.push(spec as never), { ...agent(), ...spec } as Agent),
    });
    await openNew(r);
    type(byLabel(r.container, 'Role'), 'backend');
    const box = await waitFor(() => must(r.container.querySelector<HTMLElement>('[data-testid="claude-practices"]'), 'no box'), r);
    await waitFor(() => expect(valueOf(byLabel(r.container, 'Model'))).toBe('sonnet'), r);
    expect(valueOf(byLabel(r.container, 'Effort'))).toBe('medium');
    expect(box.textContent).toContain('Recommended for a backend (worker)');
    expect(box.querySelector('[data-testid="claude-practices-text"]')!.textContent).toContain('Never skip, delete or weaken a test');
    // a new role moves the recommendation while the fields are still the recommended ones
    type(byLabel(r.container, 'Role'), 'reviewer');
    await waitFor(() => expect(valueOf(byLabel(r.container, 'Effort'))).toBe('high'), r);
    // once the human picks something else, the recommendation stops overwriting it
    type(byLabel<HTMLElement>(r.container, 'Effort'), 'low');
    type(byLabel(r.container, 'Role'), 'backend');
    await waitFor(() => expect(asked.filter((x) => x === 'backend').length).toBe(2), r);
    // the answer for backend is on screen (the step that would have overwritten the effort has run)
    await waitFor(() => expect(box.textContent).toContain('Recommended for a backend (worker)'), r);
    expect(valueOf(byLabel(r.container, 'Effort'))).toBe('low');
    // the switch is on by default and can be turned off
    const sw = byLabel<HTMLInputElement>(r.container, 'Follow Claude Code best practices');
    expect(sw.checked).toBe(true);
    await r.act(() => sw.click());
    type(byLabel(r.container, 'Agent name'), 'Pay');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Create agent').click());
    await waitFor(() => expect(created.length).toBe(1), r);
    expect(created[0]).toMatchObject({ role: 'backend', model: 'sonnet', effort: 'low', practices: false });
  });

  it('choosing the adapter that is already chosen keeps the recommended model and effort', async () => {
    const { r } = show([], { claudePractices: async (role) => practices(role) });
    await openNew(r);
    type(byLabel(r.container, 'Role'), 'backend');
    await waitFor(() => expect(valueOf(byLabel(r.container, 'Model'))).toBe('sonnet'), r);
    type(byLabel<HTMLElement>(r.container, 'Adapter'), 'claude');
    r.root.flush();
    expect(valueOf(byLabel(r.container, 'Model'))).toBe('sonnet');
    expect(valueOf(byLabel(r.container, 'Effort'))).toBe('medium');
  });

  it('shows nothing for an adapter that is not Claude Code', async (t) => {
    const asked: string[] = [];
    const { r } = show([], { claudePractices: async (role) => (asked.push(role), practices(role)) });
    await openNew(r);
    // the recommendation is asked 200 ms after the role changes (a timer): fake time runs past it
    const timers = t.useFakeTimers();
    type(byLabel<HTMLElement>(r.container, 'Adapter'), 'opencode');
    type(byLabel(r.container, 'Role'), 'backend');
    r.root.flush();
    timers.advance(1000);
    await drain();
    r.root.flush();
    expect(r.container.querySelector('[data-testid="claude-practices"]')).toBe(null);
    expect(asked).toEqual([]);
  });

  it('an existing agent shows the recommendation but keeps its own model and effort', async () => {
    const { r } = edit(agent({ model: 'opus', effort: 'max' }), { claudePractices: async (role) => practices(role) });
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await waitFor(() => must(ed.querySelector('[data-testid="claude-practices"]'), 'no box'), r);
    expect(valueOf(byLabel(ed, 'Model'))).toBe('opus');
    expect(ed.querySelector('[role="radiogroup"][aria-label="Effort"]')!.getAttribute('data-value')).toBe('max');
  });

  it('adds a prebuilt agent from the Templates dialog and re-reads the team', async () => {
    const applied: string[] = [];
    const { r, listed } = show([agent()], {
      agentCatalogList: async () => CATALOG,
      agentCatalogApply: async (req) => (applied.push(`${req.id}:${req.projectId}`), { id: req.id, agent: agent({ id: 'agt_9', name: 'Team Lead' }), mcp: { granted: [], skipped: [] }, skills: { assigned: [], shared: [], pending: [], failed: [] }, notes: [] }),
    });
    await openTemplates(r);
    const dialog = await waitFor(() => must(r.container.querySelector('dialog[open] [data-testid="add-prebuilt"]'), 'no dialog'), r);
    await waitFor(() => byText(dialog, 'button', 'Add Team Lead'), r);
    const before = listed();
    await r.act(() => byText(dialog, 'button', 'Add Team Lead').click());
    await waitFor(() => expect(applied).toEqual(['team-lead:prj_1']), r);
    await waitFor(() => expect(listed()).toBeGreaterThan(before), r);
  });
});

const sk = (name: string, over: Partial<Skill> = {}): Skill => ({
  id: `skl_${name}`,
  projectId: 'prj_1',
  name,
  description: `${name} rules`,
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

/** The checkbox of a skill in the Skills section inside root, with the line that says who gets it. */
function skillBox(root: ParentNode, name: string) {
  const field = must(root.querySelector<HTMLElement>('[data-testid="agent-skills"]'), 'no Skills section');
  const label = must(
    Array.from(field.querySelectorAll('label')).find((l) => l.querySelector('code')?.textContent === name),
    `no skill ${name} (have ${Array.from(field.querySelectorAll('code')).map((c) => c.textContent).join(', ')})`,
  );
  return { input: must(label.querySelector<HTMLInputElement>('input'), 'no checkbox'), who: label.querySelector('small')?.textContent ?? '' };
}

describe('Team: skills', () => {
  const skills = [
    sk('everyone'),
    sk('react', { roles: ['Frontend'] }),
    sk('wrangler', { agents: ['agt_9'] }),
    sk('waiting', { status: 'offered' }),
    sk('flagged', { status: 'blocked' }),
  ];
  const edge = agent({ id: 'agt_9', name: 'Edge' });

  it('the new-agent form gives the ticked skills, nothing by its role: a skill naming the role comes first, suggested (P76)', async () => {
    const calls: unknown[] = [];
    const { r } = show([edge], { skillList: async () => skills, agentCreate: async (spec) => (calls.push(spec), { ...agent(), ...spec } as Agent) });
    await openNew(r);
    const form = must(r.container.querySelector<HTMLElement>('[data-testid="agent-form"]'), 'no form');
    await waitFor(() => skillBox(form, 'wrangler'), r);
    type(byLabel(form, 'Agent name'), 'Pay');
    type(byLabel(form, 'Role'), 'Frontend');
    r.root.flush();
    // only accepted skills are listed, the one naming the role first; the others wait on the Skills page
    expect(Array.from(form.querySelectorAll('[data-testid="agent-skills"] code')).map((c) => c.textContent)).toEqual(['react', 'everyone', 'wrangler']);
    expect(form.querySelector('[data-testid="agent-skills"]')?.textContent).toContain('One skill waits for your decision on the Skills page.');
    expect(skillBox(form, 'react').who).toBe('Suggested for Frontend. Not given to another agent.');
    expect(skillBox(form, 'everyone').who).toBe('Not given to another agent.');
    expect(skillBox(form, 'wrangler').who).toBe('Also given to Edge.');
    // nothing is ticked or locked by the role
    for (const n of ['react', 'everyone', 'wrangler']) expect(skillBox(form, n).input.checked || skillBox(form, n).input.disabled).toBe(false);
    // tick two, change your mind about one
    await r.act(() => skillBox(form, 'react').input.click());
    await r.act(() => skillBox(form, 'wrangler').input.click());
    await r.act(() => skillBox(form, 'wrangler').input.click());
    expect(skillBox(form, 'react').input.checked).toBe(true);
    await r.act(() => byText(form, 'button', 'Create agent').click());
    await waitFor(() => expect(calls).toEqual([{ projectId: 'prj_1', name: 'Pay', role: 'Frontend', purpose: '', adapter: 'claude', model: '', effort: '', practices: true, skills: ['react'] }]), r);
    // the dialog closes with the form, skills included
    await waitFor(() => expect(r.container.querySelector('[data-testid="agent-form"]')).toBe(null), r);
  });

  it('sends no skills when none is ticked, and says when the project has no accepted skill', async () => {
    const calls: Record<string, unknown>[] = [];
    const { r } = show([], { skillList: async () => [sk('later', { status: 'offered' })], agentCreate: async (spec) => (calls.push(spec as never), agent()) });
    await openNew(r);
    const form = must(r.container.querySelector<HTMLElement>('[data-testid="agent-form"]'), 'no form');
    await waitFor(() => expect(form.querySelector('[data-testid="agent-skills"]')?.textContent).toContain('No skill is accepted in this project yet.'), r);
    type(byLabel(form, 'Agent name'), 'Q');
    type(byLabel(form, 'Role'), 'qa');
    r.root.flush();
    await r.act(() => byText(form, 'button', 'Create agent').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect('skills' in calls[0]!).toBe(false);
  });

  it('the editor shows the skills the agent has as chips, adds and takes away, and sends only what changed', async () => {
    const calls: unknown[] = [];
    const pay = agent();
    const mine = [
      sk('everyone', { agents: ['agt_1'] }), // given: left alone
      sk('react', { roles: ['Frontend'], agents: ['agt_1'] }),
      sk('wrangler', { agents: ['agt_9', 'agt_1'] }),
      sk('docs', { agents: [] }),
      sk('flagged', { status: 'blocked', agents: ['agt_1'] }), // not shown, never taken away
    ];
    const { r } = edit(pay, { skillList: async () => mine, agentUpdate: async (u) => (calls.push(u), pay) }, [edge]);
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    const chips = () => Array.from(ed.querySelectorAll('[data-testid="skill-chip"]')).map((c) => c.textContent!.trim());
    await waitFor(() => expect(chips()).toEqual(['everyone', 'react', 'wrangler']), r);
    await r.act(() => byText(ed, 'button', 'Take wrangler away').click());
    expect(chips()).toEqual(['everyone', 'react']);
    const add = byText<HTMLElement>(ed, 'button', 'Add a skill or tool');
    expect(await menuItems(r, add)).toEqual(['wrangler', 'docs']);
    await fromMenu(r, add, 'docs');
    await waitFor(() => expect(chips()).toEqual(['everyone', 'react', 'docs']), r);
    await r.act(() => byText(ed, 'button', 'Save').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toEqual({ id: 'agt_1', addSkills: ['docs'], removeSkills: ['wrangler'] });
  });
});

describe('Team: a permission mode that skips approvals (M1)', () => {
  const refusal = 'needs your confirmation: the permission mode "auto" lets this agent act without asking you first. Confirm it (confirmSkipsApprovals) to use it';
  const risky = { ...template, permissionMode: 'auto' };
  const applied = { agent: agent({ name: 'Payments dev' }), granted: [], skipped: [], skills: noSkills };
  const addBtn = (r: ReturnType<typeof show>['r']) => byText<HTMLButtonElement>(r.container, 'button', 'Add to this team');
  const box = (root: ParentNode) => byLabel<HTMLInputElement>(root, SKIP_CONFIRM_LABEL);

  it('marks the crew card of an agent that has such a mode with the red badge, and no other', async () => {
    const { r } = show([agent({ permissionMode: 'bypassPermissions' }), agent({ id: 'agt_2', name: 'Rex', permissionMode: 'acceptEdits' }), agent({ id: 'agt_3', name: 'Dan', permissionMode: 'dontAsk' })]);
    const pay = await waitAgent(r, 'Pay');
    expect(pay.querySelector('[data-testid="skips-approvals-badge"]')!.textContent).toBe('skips approvals');
    expect((await waitAgent(r, 'Dan')).querySelector('[data-testid="skips-approvals-badge"]')).not.toBeNull();
    expect((await waitAgent(r, 'Rex')).querySelector('[data-testid="skips-approvals-badge"]')).toBeNull();
  });

  it('adding an agent from a template with such a mode needs the box: Add stays off until it is ticked, and the core is told', async () => {
    const calls: unknown[][] = [];
    const { r } = show([], { agentTemplateList: async () => [risky], agentTemplateApply: async (...a) => (calls.push(a), applied) });
    await openTemplates(r);
    await waitFor(() => addBtn(r), r);
    const warning = r.container.querySelector('[data-testid="skip-approvals-warning"]')!;
    expect(warning.textContent).toContain('"auto" lets agents made from Payments dev act without asking you first');
    expect(addBtn(r).disabled).toBe(true);
    await r.act(() => addBtn(r).click());
    expect(calls).toEqual([]);
    await r.act(() => box(r.container).click());
    expect(addBtn(r).disabled).toBe(false);
    await r.act(() => addBtn(r).click());
    await waitFor(() => expect(calls).toEqual([['tpl_1', 'prj_1', undefined, true]]), r);
    // the box is cleared for the next one
    await waitFor(() => expect(box(r.container).checked).toBe(false), r);
  });

  it('the box belongs to the template it was ticked for', async () => {
    const other = { ...risky, id: 'tpl_2', name: 'Docs writer' };
    const { r } = show([], { agentTemplateList: async () => [risky, other], agentTemplateApply: async () => applied });
    await openTemplates(r);
    await waitFor(() => addBtn(r), r);
    await r.act(() => box(r.container).click());
    expect(addBtn(r).disabled).toBe(false);
    type(byLabel<HTMLElement>(r.container, 'Template'), 'tpl_2');
    r.root.flush();
    expect(r.container.querySelector('[data-testid="skip-approvals-warning"]')!.textContent).toContain('Docs writer');
    expect(box(r.container).checked).toBe(false);
    expect(addBtn(r).disabled).toBe(true);
  });

  it('a template that keeps approvals is added as it always was, with no box and no confirmation', async () => {
    const calls: unknown[][] = [];
    const { r } = show([], { agentTemplateList: async () => [{ ...template, permissionMode: 'plan' }], agentTemplateApply: async (...a) => (calls.push(a), applied) });
    await openTemplates(r);
    await waitFor(() => addBtn(r), r);
    expect(r.container.querySelector('[data-testid="skip-approvals"]')).toBeNull();
    await r.act(() => addBtn(r).click());
    await waitFor(() => expect(calls).toEqual([['tpl_1', 'prj_1', undefined]]), r);
  });

  it('shows the core\'s refusal next to the box when the template is refused anyway', async () => {
    const { r } = show([], {
      agentTemplateList: async () => [risky],
      agentTemplateApply: async () => {
        throw new Error(refusal);
      },
    });
    await openTemplates(r);
    await waitFor(() => addBtn(r), r);
    await r.act(() => box(r.container).click());
    await r.act(() => addBtn(r).click());
    const err = await waitFor(() => must(r.container.querySelector('[data-testid="skip-approvals-error"]'), 'no refusal yet'), r);
    expect(err.textContent).toBe(refusal);
    expect(r.container.textContent).not.toContain('Agent not created');
  });

  describe('a prebuilt agent', () => {
    const risky_ = (): PrebuiltAgent => {
      const e = prebuilt('team-lead');
      return { ...e, preferred: { ...e.preferred, permissionMode: 'bypassPermissions' } };
    };
    const result = (id: string) => ({ id, agent: agent({ id: 'agt_9', name: 'Team Lead' }), mcp: { granted: [], skipped: [] }, skills: noSkills, notes: [] });
    async function openGallery(client: Partial<Client>) {
      const t = show([agent()], { agentCatalogList: async () => [risky_(), prebuilt('backend-engineer')], ...client });
      await openTemplates(t.r);
      const dialog = await waitFor(() => must(t.r.container.querySelector<HTMLElement>('dialog[open] [data-testid="add-prebuilt"]'), 'no dialog'), t.r);
      await waitFor(() => byText(dialog, 'button', 'Add Team Lead'), t.r);
      return { ...t, dialog };
    }

    it('whose mode skips approvals is not added by one click: it asks for the box first, then tells the core', async () => {
      const requests: unknown[] = [];
      const { r, dialog } = await openGallery({ agentCatalogApply: async (req) => (requests.push(req), result(req.id)) });
      await r.act(() => byText(dialog, 'button', 'Add Team Lead').click());
      const ask = await waitFor(() => must(dialog.querySelector<HTMLElement>('[data-testid="prebuilt-confirm"]'), 'no confirmation yet'), r);
      expect(requests).toEqual([]);
      expect(ask.textContent).toContain('Team Lead comes with the permission mode "bypassPermissions"');
      const add = () => byText<HTMLButtonElement>(ask, 'button', 'Add Team Lead');
      expect(add().disabled).toBe(true);
      await r.act(() => box(ask).click());
      expect(add().disabled).toBe(false);
      await r.act(() => add().click());
      await waitFor(() => expect(requests).toEqual([{ id: 'team-lead', projectId: 'prj_1', installSkills: true, connectMcp: true, confirmSkipsApprovals: true }]), r);
      await waitFor(() => expect(dialog.querySelector('[data-testid="prebuilt-confirm"]')).toBeNull(), r);
      expect(dialog.querySelector('[data-testid="prebuilt-added"]')!.textContent).toContain('Team Lead joined the team');
    });

    it('can be left unconfirmed with Cancel, and an agent that keeps approvals is added at once without the flag', async () => {
      const requests: Record<string, unknown>[] = [];
      const { r, dialog } = await openGallery({ agentCatalogApply: async (req) => (requests.push({ ...req }), result(req.id)) });
      const cards = Array.from(dialog.querySelectorAll('[data-testid="prebuilt"]'));
      await r.act(() => byText(dialog, 'button', 'Add Team Lead').click());
      await waitFor(() => dialog.querySelector('[data-testid="prebuilt-confirm"]') ?? (() => { throw new Error('no confirmation yet'); })(), r);
      await r.act(() => byText(dialog, 'button', 'Cancel').click());
      expect(dialog.querySelector('[data-testid="prebuilt-confirm"]')).toBeNull();
      expect(requests).toEqual([]);
      await r.act(() => byText(dialog, 'button', 'Add Backend Engineer').click());
      await waitFor(() => expect(requests.map((q) => q.id)).toEqual(['backend-engineer']), r);
      expect('confirmSkipsApprovals' in requests[0]!).toBe(false);
      expect(cards.length).toBe(2);
    });

    it('shows the red badge on the card of an entry that comes with such a mode, and names the mode in its details', async () => {
      const { r, dialog } = await openGallery({});
      const [lead, engineer] = Array.from(dialog.querySelectorAll('[data-testid="prebuilt"]'));
      expect(lead!.querySelector('[data-testid="skips-approvals-badge"]')!.textContent).toBe('skips approvals');
      expect(engineer!.querySelector('[data-testid="skips-approvals-badge"]')).toBeNull();
      await r.act(() => byText(lead as HTMLElement, 'button', 'What Team Lead comes with').click());
      await waitFor(() => must(r.container.querySelector('[data-testid="prebuilt-mode"]'), 'no details yet'), r);
      expect(r.container.querySelector('[data-testid="prebuilt-mode"]')!.textContent).toContain('bypassPermissions');
    });

    it('shows the core\'s refusal next to the box when the entry is refused anyway', async () => {
      const { r, dialog } = await openGallery({
        agentCatalogApply: async () => {
          throw new Error(refusal);
        },
      });
      await r.act(() => byText(dialog, 'button', 'Add Team Lead').click());
      const ask = await waitFor(() => must(dialog.querySelector<HTMLElement>('[data-testid="prebuilt-confirm"]'), 'no confirmation yet'), r);
      await r.act(() => box(ask).click());
      await r.act(() => byText<HTMLButtonElement>(ask, 'button', 'Add Team Lead').click());
      const err = await waitFor(() => must(dialog.querySelector('[data-testid="skip-approvals-error"]'), 'no refusal yet'), r);
      expect(err.textContent).toBe(refusal);
      expect(dialog.textContent).not.toContain('Agent not added');
    });
  });

  it('saving an agent that has such a mode as a template says to make the template in the Library, where it can be confirmed', async () => {
    const { r } = edit(agent({ permissionMode: 'bypassPermissions' }), {
      agentTemplateFromAgent: async () => {
        throw new Error(refusal);
      },
    });
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await r.act(() => byText(ed, 'button', 'Save as a template').click());
    const note = await waitFor(() => must(ed.querySelector<HTMLElement>('[data-testid="template-needs-confirmation"]'), 'no note yet'), r);
    expect(note.textContent).toContain(refusal);
    expect(note.textContent).toContain('Make the template in the Library');
    expect(note.querySelector('a[href="/library"]')).not.toBeNull();
    expect(ed.textContent).not.toContain('Agent not changed');
  });
});


// TEST DOUBLES (labelled): spans and task rows shaped like core.RunSpan, core.WaitSpan and core.TaskSummary
const runSpan = (over: Partial<RunSpan>): RunSpan => ({ runId: 'r', start: day(9), end: day(10), startedAt: day(9), endedAt: day(10), outcome: 'finished', ...over });
const waitSpan = (over: Partial<WaitSpan>): WaitSpan => ({ approvalId: 'ap', kind: 'approval', title: 'Approve the plan', status: 'REQUESTED', start: day(15), end: null, createdAt: day(15), resolvedAt: 0, ...over });
const row = (over: Partial<TaskSummary>): TaskSummary => ({
  taskId: 't43',
  projectId: 'prj_1',
  projectKey: 'GOC',
  seq: 43,
  title: 'Fix the limiter',
  status: 'IN_PROGRESS',
  labels: [],
  assignee: 'agt_1',
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
const at = (agentId: string, over: Partial<PresenceState> = {}): PresenceState => ({ agentId, presence: 'Idle', since: '', updated: '', seq: 1, ...over });

const ada = agent({ id: 'agt_1', name: 'Ada', purpose: '' });
const bo = agent({ id: 'agt_2', name: 'Bo', purpose: '', model: 'opus' });
const cy = agent({ id: 'agt_3', name: 'Cy', purpose: 'Reviews every task before it reaches you. Never merges.', permissionMode: 'bypassPermissions' });
const crew = [ada, bo, cy];

/** The day of the crew: Ada finished GOC-43's first run and works on it now; Bo failed early and waits on you; Cy did nothing. */
const today = (since: number) => ({
  since,
  until: NOW,
  agents: [
    lane(ada, {
      runs: [
        runSpan({ runId: 'r1', start: day(9), end: day(10), startedAt: day(9), endedAt: day(10), outcome: 'finished', taskId: 't43', taskSeq: 43 }),
        runSpan({ runId: 'r2', start: day(14), end: null, startedAt: day(14), endedAt: 0, outcome: 'working', taskId: 't43', taskSeq: 43 }),
      ],
    }),
    lane(bo, {
      runs: [runSpan({ runId: 'r3', start: day(6, 30), end: day(7), startedAt: day(6, 30), endedAt: day(7), outcome: 'failed', taskSeq: 41 })],
      waits: [
        waitSpan({ approvalId: 'old', start: day(11), end: day(12), createdAt: day(11), resolvedAt: day(12), status: 'APPROVED' }),
        waitSpan({ approvalId: 'ap1', title: 'Approve the plan', start: day(15), end: null, createdAt: day(15), taskId: 't41', taskSeq: 41 }),
      ],
    }),
    lane(cy),
  ],
});

function showCrew(client: Partial<Client> = {}, agents: Agent[] = crew) {
  const asked: [number, number, string][] = [];
  const t = show(agents, {
    presenceList: async () => [at('agt_1', { presence: 'Working', taskId: 't43', doing: 'Running go test ./...' }), at('agt_2', { presence: 'Waiting' })],
    agentTimeline: async (since, until = 0, projectId = '') => (asked.push([since, until, projectId]), today(since)),
    taskSummaries: async () => ({
      summaries: [
        row({}),
        row({ taskId: 't44', seq: 44, title: 'Support hex input', status: 'READY', stepIndex: 0 }),
        row({ taskId: 't41', seq: 41, title: 'Parse hex', assignee: 'agt_2', status: 'WAITING_HUMAN' }),
        row({ taskId: 't38', seq: 38, title: 'Add Multiply', assignee: 'agt_3', status: 'COMPLETED', completedAt: day(8, 30) }),
      ],
      total: 4,
      partial: false,
    }),
    // Cy's accepted skills, and the project's channels with their agents (crew.extras leaves out task rooms and direct
    // messages): everyone is in general (a card does not name it), Cy alone in decisions
    crewExtras: async () => ({
      skills: [
        { agentId: 'agt_3', name: 'code-review' },
        { agentId: 'agt_3', name: 'security-review' },
      ],
      rooms: [
        { projectId: 'prj_1', name: 'decisions', kind: 'channel', agents: ['agt_3'] },
        { projectId: 'prj_1', name: 'general', kind: 'channel', agents: ['agt_1', 'agt_2', 'agt_3'] },
      ],
    }),
    ...client,
  });
  return { ...t, asked };
}

const blocks = (r: ReturnType<typeof show>['r'], agentId: string) =>
  Array.from(r.$$(`[data-testid="crew-lane"][data-agent="${agentId}"] [data-testid="crew-block"]`)).map((b) => [b.getAttribute('data-kind'), Number(b.getAttribute('data-left')), Number(b.getAttribute('data-width')), b.getAttribute('title')]);

describe('Crew (graphite): the header, Today and the cards', () => {
  it('says how many agents there are, how many work and how many wait on you, with Templates and New agent (N)', async () => {
    const { r } = showCrew();
    await waitFor(() => expect(r.$('[data-testid="crew-figures"]')!.textContent).toBe('3 agents · 1 working · 1 waiting on you'), r);
    expect(r.$('h1')!.textContent).toBe('Crew');
    expect(r.$('[data-testid="figure-working"]')!.textContent).toBe('1 working');
    expect(r.$('[data-testid="figure-waiting"]')!.textContent).toBe('1 waiting on you');
    expect(byText(r.container, 'button', 'Templates')).toBeTruthy();
    const make = byText(r.container, 'button', /^New agent/);
    expect(make.querySelector('kbd')!.textContent).toBe('N');
    expect(make.getAttribute('aria-keyshortcuts')).toBe('N');
  });

  it("draws Today from the first run's hour to now: a lane per agent, its runs and waits placed by time, each saying what it is", async () => {
    const { r, asked } = showCrew();
    await waitFor(() => expect(blocks(r, 'agt_1').length).toBe(2), r);
    // the day is asked from local midnight, for this project
    expect(asked[0]).toEqual([new Date(2026, 9, 22).getTime(), 0, 'prj_1']);
    // Bo's failed run at 06:30 moves the start to 06:00: ten hours to now, one hour is 10%
    const tl = r.$('[data-testid="crew-timeline"]')!;
    expect(Number(tl.getAttribute('data-start'))).toBe(day(6));
    expect(Array.from(r.$$('[data-testid="crew-axis"] span')).map((x) => x.textContent)).toEqual(['06:00', '09:00', '12:00', 'now']);
    expect(Array.from(r.$$('[data-testid="crew-lane"] .name')).map((n) => n.textContent)).toEqual(['Ada', 'Bo', 'Cy']);
    expect(blocks(r, 'agt_1')).toEqual([
      ['finished', 30, 10, 'Ada · GOC-43 · 09:00–10:00 · finished'],
      ['working', 80, 20, 'Ada · GOC-43 · 14:00–now · working now'],
    ]);
    expect(blocks(r, 'agt_2')).toEqual([
      ['failed', 5, 5, 'Bo · GOC-41 · 06:30–07:00 · failed'],
      ['waited', 50, 10, 'Bo · 11:00–12:00 · waited on you'],
      ['waiting', 90, 10, 'Bo · GOC-41 · 15:00–now · waiting on you'],
    ]);
    // a lane with nothing today says so to assistive tech, and the legend names the colours
    expect(blocks(r, 'agt_3')).toEqual([]);
    expect(r.$('[data-testid="crew-lane"][data-agent="agt_3"] ul')!.textContent).toBe('Nothing yet today.');
    expect(r.$('[data-testid="crew-legend"]')!.textContent).toBe('working nowfinished runfailed runwaiting on you');
    // each block says what it is in words too
    expect(r.$('[data-testid="crew-lane"][data-agent="agt_1"] [data-testid="crew-block"] .sr-only')!.textContent).toBe('Ada · GOC-43 · 09:00–10:00 · finished');
  });

  it('clips a run that began yesterday to midnight, and keeps 08:00 when nothing was earlier', async () => {
    const midnight = new Date(2026, 9, 22).getTime();
    const { r } = showCrew({
      agentTimeline: async (since) => ({ since, until: NOW, agents: [lane(ada, { runs: [runSpan({ runId: 'y', start: midnight, end: day(2), startedAt: midnight - 3_600_000, endedAt: day(2) })] }), lane(bo), lane(cy)] }),
    });
    await waitFor(() => expect(blocks(r, 'agt_1').length).toBe(1), r);
    expect(blocks(r, 'agt_1')).toEqual([['finished', 0, 12.5, 'Ada · yesterday 23:00–02:00 · finished']]);
    cleanup();
    const quiet = showCrew({ agentTimeline: async (since) => ({ since, until: NOW, agents: [lane(ada), lane(bo), lane(cy)] }) });
    await waitFor(() => expect(quiet.r.$$('[data-testid="crew-lane"]').length).toBe(3), quiet.r);
    await waitFor(() => expect(Number(quiet.r.$('[data-testid="crew-timeline"]')!.getAttribute('data-start'))).toBe(day(8)), quiet.r);
    expect(Array.from(quiet.r.$$('[data-testid="crew-axis"] span')).map((x) => x.textContent)).toEqual(['08:00', '10:00', '12:00', '14:00', 'now']);
  });

  it('reads the day again on agent, run and approval events, and not on others', async () => {
    const { r, s, asked } = showCrew();
    await waitFor(() => expect(asked.length).toBe(1), r);
    for (const type of ['run.started', 'approval.requested', 'agent.updated']) {
      const before = asked.length;
      s.emit({ type });
      await waitFor(() => expect(asked.length).toBeGreaterThan(before), r);
    }
    await drain();
    const before = asked.length;
    s.emit({ type: 'memory.added' });
    await drain();
    await drain();
    expect(asked.length).toBe(before);
  });

  it('moves "now" every minute: the running block grows with the clock', async (t) => {
    const timers = t.useFakeTimers();
    let clockNow = NOW;
    const { s } = svc(crew, {
      presenceList: async () => [at('agt_1', { presence: 'Working' })],
      agentTimeline: async (since) => ({ since, until: clockNow, agents: [lane(ada, { runs: [runSpan({ runId: 'w', start: day(14), end: null, startedAt: day(14), endedAt: 0, outcome: 'working' })] }), lane(bo), lane(cy)] }),
    });
    const r = mount(<TeamScreen now={() => clockNow} />, s);
    await waitFor(() => expect(blocks(r, 'agt_1').length).toBe(1), r);
    expect(blocks(r, 'agt_1')[0]!.slice(1, 3)).toEqual([75, 25]);
    clockNow = day(18);
    await timers.advanceAsync(60_000);
    await drain();
    r.root.flush();
    // 08:00 to 18:00 now: the run from 14:00 is 40% of the track, at its right end
    expect(blocks(r, 'agt_1')[0]!.slice(1, 3)).toEqual([60, 40]);
    expect(blocks(r, 'agt_1')[0]![3]).toBe('Ada · 14:00–now · working now');
  });

  it('a card per agent: who it is, what it runs on, its state, what it is doing or did, its progress and its chips', async () => {
    const { r } = showCrew();
    const a = await waitAgent(r, 'Ada');
    await waitFor(() => expect(a.getAttribute('data-state')).toBe('working'), r);
    expect(a.querySelector('[data-dot="true"]')).not.toBeNull();
    expect(a.querySelector('[data-testid="agent-link"]')!.getAttribute('href')).toBe('/team/agt_1');
    expect(a.querySelector('[data-testid="agent-runs"]')!.textContent).toBe('Claude Code · sonnet');
    expect(a.querySelector('[data-testid="presence"]')!.textContent).toBe('working');
    await waitFor(() => expect(a.querySelector('[data-testid="agent-doing"]')!.textContent).toBe('Running go test ./... on GOC-43 Fix the limiter. Next in its queue: GOC-44.'), r);
    expect(Array.from(a.querySelectorAll('[data-testid="agent-task"]')).map((k) => k.textContent)).toEqual(['GOC-43', 'GOC-44']);
    const bar = a.querySelector('[data-testid="agent-progress"]')!;
    expect([bar.getAttribute('role'), bar.getAttribute('aria-valuenow'), bar.getAttribute('aria-valuemax'), bar.getAttribute('aria-label')]).toEqual(['progressbar', '2', '5', 'GOC-43: step 2 of 5']);
    // a working card shows its progress, not its chips (its task room is not one of "its rooms")
    expect(a.querySelectorAll('[data-testid="agent-chip"]').length).toBe(0);

    // waiting on you: amber, says what it asked and links to the Inbox; its name still opens its page
    const b = await waitAgent(r, 'Bo');
    await waitFor(() => expect(b.getAttribute('data-state')).toBe('waiting-on-you'), r);
    expect(b.querySelector('[data-testid="presence"]')!.textContent).toBe('waiting on you');
    expect(b.querySelector('[data-testid="agent-runs"]')!.textContent).toBe('Claude Code · opus');
    await waitFor(() => expect(b.querySelector('[data-testid="agent-doing"]')!.textContent).toBe('Approve the plan on GOC-41. Open the Inbox to decide.'), r);
    expect(b.querySelector('[data-testid="agent-inbox"]')!.getAttribute('href')).toBe('/needs-you');
    expect(b.querySelector('[data-testid="agent-link"]')!.getAttribute('href')).toBe('/team/agt_2');
    expect(b.querySelector('[data-testid="agent-progress"]')).toBeNull();
    // the whole card is one link: a point anywhere on it reaches the agent's page; on a waiting card, the Inbox (its name still
    // reaches the agent's page)
    const hit = (el: Element) => {
      el.scrollIntoView({ block: 'center' });
      const box = el.getBoundingClientRect();
      return document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2)?.closest('a')?.getAttribute('href');
    };
    expect(hit(a.querySelector('[data-testid="agent-doing"]')!)).toBe('/team/agt_1');
    expect(hit(a.querySelector('[data-testid="agent-runs"]')!)).toBe('/team/agt_1');
    expect(hit(b.querySelector('[data-testid="agent-runs"]')!)).toBe('/needs-you');
    expect(hit(b.querySelector('[data-testid="agent-link"]')!)).toBe('/team/agt_2');
    expect(a.querySelector('[data-testid="agent-link"]')!.getBoundingClientRect().height).toBeGreaterThanOrEqual(28);

    // stopped (no presence): its last finished task, its skills and rooms, and the red badge of its mode
    const c = await waitAgent(r, 'Cy');
    expect(c.querySelector('[data-testid="presence"]')!.textContent).toBe('stopped');
    await waitFor(() => expect(c.querySelector('[data-testid="agent-doing"]')!.textContent).toBe('Last: finished GOC-38 Add Multiply.'), r);
    await waitFor(() => expect(Array.from(c.querySelectorAll('[data-testid="agent-chip"]')).map((x) => x.textContent)).toEqual(['code-review', 'security-review', 'decisions']), r);
    expect(c.querySelector('[data-testid="skips-approvals-badge"]')!.textContent).toBe('skips approvals');
    expect(a.querySelector('[data-testid="skips-approvals-badge"]')).toBeNull();
  });

  it('names the runtime as the person knows it: Claude Code WSL inside a distro, Models on this PC for a local helper', async () => {
    const wsl = agent({ id: 'agt_w', name: 'Linux', runMode: 'wsl', wslDistro: 'Ubuntu', model: '' });
    const loc = agent({ id: 'agt_l', name: 'Local helper', adapter: 'opencode', providerId: 'llama', model: 'gpt-oss-20b' });
    const { r } = showCrew(
      {
        presenceList: async () => [at('agt_l', { presence: 'Working', doing: 'Editing format.go' })],
        localGet: async () => ({ usedBy: [], settings: { providerId: 'llama' } }) as never,
        agentTimeline: async (since) => ({ since, until: NOW, agents: [lane(wsl), lane(loc)] }),
        taskSummaries: async () => ({ summaries: [], total: 0, partial: false }),
      },
      [wsl, loc],
    );
    const w = await waitAgent(r, 'Linux');
    expect(w.querySelector('[data-testid="agent-runs"]')!.textContent).toBe('Claude Code WSL · default model');
    expect(w.querySelector('[data-testid="agent-runs"]')!.getAttribute('title')).toBe('Role: backend. Runs inside the WSL distro Ubuntu');
    const l = await waitAgent(r, 'Local helper');
    await waitFor(() => expect(l.querySelector('[data-testid="agent-runs"]')!.textContent).toBe('Models on this PC · gpt-oss-20b'), r);
    await waitFor(() => expect(l.querySelector('[data-testid="agent-doing"]')!.textContent).toBe('Editing format.go.'), r);
  });

  it('N opens New agent, but not while typing, with a modifier, or with the palette open', async () => {
    const { r, s } = showCrew();
    await waitAgent(r, 'Ada');
    const key = (target: EventTarget, init: KeyboardEventInit = {}) => r.act(() => target.dispatchEvent(new KeyboardEvent('keydown', { key: 'n', bubbles: true, cancelable: true, ...init })));
    const input = document.createElement('input');
    document.body.append(input);
    await key(input);
    input.remove();
    await key(window, { ctrlKey: true });
    s.ui.setPalette(true);
    await key(window);
    s.ui.setPalette(false);
    expect(r.$('dialog[open]')).toBeNull();
    await key(window);
    // it opens on Describe (decision P138)
    const form = await waitFor(() => must(r.$('dialog[open] [data-testid="describe-agent"]'), 'no form'), r);
    expect(form.closest('dialog')!.textContent).toContain('New agent');
  });

  it('says what a team needs when there is no agent yet, and draws no timeline', async () => {
    const { r } = showCrew({ agentTimeline: async (since) => ({ since, until: NOW, agents: [] }) }, []);
    await waitFor(() => expect(r.container.textContent).toContain('A task needs a worker and a different agent as reviewer'), r);
    expect(r.$('[data-testid="crew-timeline"]')).toBeNull();
    expect(r.$$('[data-testid="agent"]').length).toBe(0);
    expect(r.$('[data-testid="crew-figures"]')!.textContent).toBe('0 agents · 0 working · 0 waiting on you');
  });
});

describe("Crew (graphite): the workspace's page", () => {
  const shopAgent = agent({ id: 'agt_s', projectId: 'prj_2', name: 'Shopper', model: '' });
  const projects = [{ id: 'prj_1', name: 'gocalc', key: 'GOC' }, { id: 'prj_2', name: 'shop-api', key: 'SHOP' }] as Project[];

  function showAll(client: Partial<Client> = {}) {
    const asked: string[] = [];
    const skillsFor: string[] = [];
    const { s } = svc([ada], {
      projectList: async () => projects,
      agentListAll: async () => [ada, bo, shopAgent],
      presenceList: async () => [at('agt_s', { presence: 'Working' })],
      agentTimeline: async (since, _u, projectId = '') => (
        asked.push(projectId),
        { since, until: NOW, agents: [lane(ada), lane(bo), lane(shopAgent, { runs: [runSpan({ runId: 's1', start: day(12), end: null, endedAt: 0, startedAt: day(12), outcome: 'working', taskSeq: 7 })] })] }
      ),
      crewExtras: async (pid) => (skillsFor.push(pid), { skills: [], rooms: [] }),
      ...client,
    });
    const r = mount(<WorkspaceCrewScreen now={() => NOW} />, s);
    return { r, s, asked, skillsFor };
  }

  it("lists every project's agents, grouped under each project with its colour, and the day of all of them", async () => {
    const { r, asked, skillsFor } = showAll();
    await waitFor(() => expect(r.$$('[data-testid="crew-project"]').length).toBe(2), r);
    // named as the sidebar names it, apart from one project's Crew
    expect(r.$('h1')!.textContent).toBe('All crews');
    const groups = Array.from(r.$$('[data-testid="crew-project"]'));
    expect(groups.map((g) => g.querySelector('h2')!.textContent)).toEqual(['gocalc2 agents', 'shop-api1 agent']);
    expect(groups.map((g) => Array.from(g.querySelectorAll('[data-testid="agent"]')).map((x) => x.getAttribute('data-name')))).toEqual([['Ada', 'Bo'], ['Shopper']]);
    await waitFor(() => expect(r.$('[data-testid="crew-figures"]')!.textContent).toBe('3 agents · 1 working · 0 waiting on you'), r);
    // the timeline is every project's ('' to the core), each lane marked with its project's colour, a task named by its project's key
    expect(asked[0]).toBe('');
    await waitFor(() => expect(blocks(r, 'agt_s').map((b) => b[3])).toEqual(['Shopper · SHOP-7 · 12:00–now · working now']), r);
    expect(r.$('[data-testid="crew-lane"][data-agent="agt_s"] .name')!.getAttribute('title')).toBe('Shopper (shop-api)');
    expect(r.$('[data-testid="crew-lane"][data-agent="agt_s"] .sq')).not.toBeNull();
    // the skills and rooms of every project in one read ('' to the core), not one a project (review 2026-10-27)
    await waitFor(() => expect(skillsFor).toEqual(['']), r);
  });

  it('opening an agent of another project opens that project; New agent asks which project', async () => {
    const created: Record<string, unknown>[] = [];
    const { r, s } = showAll({ agentCreate: async (spec) => (created.push(spec as never), shopAgent) });
    const shop = await waitAgent(r, 'Shopper');
    await waitFor(() => expect(s.workspace.projectId).toBe('prj_1'), r);
    const link = shop.querySelector<HTMLAnchorElement>('[data-testid="agent-link"]')!;
    link.addEventListener('click', (e) => e.preventDefault()); // the test stays on the page
    await r.act(() => link.click());
    expect(s.workspace.projectId).toBe('prj_2');
    // the dialog's project picker starts on the open project, and the form makes the agent in the one chosen
    await r.act(() => byText(r.container, 'button', /^New agent/).click());
    const pick = await waitFor(() => byLabel<HTMLElement>(r.container, 'Project'), r);
    expect(optionsOf(pick).map((o) => o.label)).toEqual(['gocalc', 'shop-api']);
    expect(valueOf(pick)).toBe('prj_2');
    type(pick, 'prj_1');
    r.root.flush();
    await r.act(() => (must(r.$('dialog[open] [data-testid="new-agent-configure"]'), 'no tabs') as HTMLElement).click());
    await waitAdapters(r);
    type(byLabel(r.container, 'Agent name'), 'Pay');
    type(byLabel(r.container, 'Role'), 'backend');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Create agent').click());
    await waitFor(() => expect(created.map((c) => c.projectId)).toEqual(['prj_1']), r);
  });
});

describe("Crew (graphite): where the projects' agents run", () => {
  it('is read for every project in one request (project.runModes), never one request per project', async () => {
    let all = 0;
    const perProject: string[] = [];
    const { r } = showCrew({
      projectRunModes: async () => (all++, { prj_1: { runMode: 'native' } }),
      projectRunMode: async (id: string) => (perProject.push(id), { runMode: 'native' }),
    });
    await waitAgent(r, 'Ada');
    await waitFor(() => expect(all).toBe(1), r);
    expect(perProject).toEqual([]);
  });
});
