import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Skill } from '../../api/extTypes.ts';
import type { AgentTemplate, ClaudePractices } from '../../api/libraryClient.ts';
import type { AdapterInfo, Agent, PrebuiltAgent, Project } from '../../api/types.ts';
import { SKIP_CONFIRM_LABEL } from '../../lib/permission.ts';
import { expiredLicense, lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, drain, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { CATALOG, prebuilt } from '../project/wizard-fixtures.ts';
import { TeamScreen } from './team-screen.tsx';

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

function show(agents: Agent[], client: Partial<Client> = {}, adapters = [claude, opencode]) {
  let listed = 0;
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project],
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
    ...client,
  });
  s.workspace.start();
  s.project.start();
  const navigated: string[] = [];
  const r = mount(<TeamScreen onNavigate={(h) => navigated.push(h)} />, s);
  return { r, s, navigated, listed: () => listed };
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

describe('Team: purpose, model and effort', () => {
  it('creates an agent with a purpose, a model from the adapter and an effort level from the adapter list', async () => {
    const calls: unknown[] = [];
    const { r, listed } = show([], { agentCreate: async (spec) => (calls.push(spec), { ...agent(), ...spec } as Agent) });
    await waitAdapters(r);
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
    // the form is cleared and the team re-read
    await waitFor(() => expect(byLabel<HTMLInputElement>(r.container, 'Agent name').value).toBe(''), r);
    await waitFor(() => expect(listed()).toBeGreaterThan(before), r);
  });

  it('keeps effort free text for an adapter that does not list its levels', async () => {
    const { r } = show([]);
    await waitAdapters(r);
    type(byLabel<HTMLElement>(r.container, 'Adapter'), 'opencode');
    r.root.flush();
    expect(byLabel(r.container, 'Effort').tagName).toBe('INPUT');
  });

  it('shows the agent as a card: role, adapter, model, effort, purpose and presence', async () => {
    const { r } = show([agent()], { presenceList: async () => [{ agentId: 'agt_1', presence: 'Working', since: '', updated: '', seq: 1 }] });
    const card = await waitAgent(r, 'Pay');
    expect(card.textContent).toContain('backend');
    expect(card.textContent).toContain('claude');
    expect(card.textContent).toContain('model sonnet');
    expect(card.textContent).toContain('effort high');
    expect(card.querySelector('[data-testid="agent-purpose"]')!.textContent).toContain('Own the payments service.');
    await waitFor(() => expect(card.querySelector('[data-testid="presence"]')!.textContent).toContain('Working'), r);
  });

  it('the card is a dot with what the agent is doing right now, its name a link to its page, and where it runs (Crew HQ)', async () => {
    const { r } = show([agent({ runMode: 'wsl', wslDistro: 'Ubuntu' })], {
      presenceList: async () => [{ agentId: 'agt_1', presence: 'Working', activity: 'Running tests for webhook retries', since: '', updated: '', seq: 1 }],
    });
    const card = await waitAgent(r, 'Pay');
    expect(card.querySelector('[data-dot="true"]')).not.toBeNull();
    expect(card.querySelector('[data-testid="agent-link"]')!.getAttribute('href')).toBe('/team/agt_1');
    expect(card.querySelector('h3')!.textContent).toBe('Pay');
    await waitFor(() => expect(card.querySelector('.now')!.textContent).toContain('Running tests for webhook retries'), r);
    expect(card.querySelector('[data-testid="agent-runs"]')!.textContent).toBe('in WSL Ubuntu');
    expect(r.$('h1')!.textContent).toBe('Crew');
    // without presence the card says the agent rests, in words
    cleanup();
    const quiet = show([agent()]);
    const c2 = await waitAgent(quiet.r, 'Pay');
    expect(c2.querySelector('.now')!.textContent).toContain('Resting');
    expect(c2.querySelector('[data-testid="agent-runs"]')).toBeNull();
  });

  it('says what a team needs when there is no agent yet', async () => {
    const { r } = show([]);
    await waitFor(() => expect(r.container.textContent).toContain('A task needs a worker and a different agent as reviewer'), r);
    expect(r.$$('[data-testid="agent"]').length).toBe(0);
  });

  it('shows the purpose on the team list and edits model, effort and purpose of an existing agent', async () => {
    const calls: unknown[] = [];
    const { r, listed } = show([agent()], { agentUpdate: async (u) => (calls.push(u), { ...agent(), ...u } as Agent) });
    const card = await waitAgent(r, 'Pay');
    expect(card.querySelector('[data-testid="agent-purpose"]')!.textContent).toContain('Own the payments service.');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    expect(ed.textContent).toContain('cannot change');
    type(byLabel(ed, 'Model'), 'haiku');
    type(byLabel<HTMLElement>(ed, 'Effort'), 'low');
    type(byLabel<HTMLTextAreaElement>(ed, 'Purpose'), 'Billing only.');
    r.root.flush();
    const before = listed();
    await r.act(() => byText(ed, 'button', 'Save changes').click());
    await waitFor(() => expect(calls).toEqual([{ id: 'agt_1', name: 'Pay', role: 'backend', purpose: 'Billing only.', model: 'haiku', effort: 'low', practices: true }]), r);
    await waitFor(() => expect(listed()).toBeGreaterThan(before), r);
    await waitFor(() => expect(editor(r)).toBe(null), r);
  });

  it('shows the core refusal and keeps the editor open', async () => {
    const { r } = show([agent()], {
      agentUpdate: async () => {
        throw new Error('claude accepts these effort levels: low, medium, high, xhigh, max (got "ultra")');
      },
    });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await r.act(() => byText(ed, 'button', 'Save changes').click());
    await waitFor(() => expect(ed.querySelector('[role="alert"]')!.textContent).toContain('accepts these effort levels'), r);
    expect(editor(r)).toBeTruthy();
  });

  it('sends routed models only when the human turned routing on', async () => {
    const calls: Record<string, unknown>[] = [];
    const { r } = show([agent()], { agentUpdate: async (u) => (calls.push(u as never), { ...agent(), ...u } as Agent) });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    const routing = ed.querySelector<HTMLElement>('[data-testid="route-models"]')!;
    await r.act(() => routing.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
    expect(valueOf(byLabel(ed, 'Routed model 1'))).toBe('haiku');
    expect(valueOf(byLabel(ed, 'Tier of routed model 2'))).toBe('premium');
    await r.act(() => byText(ed, 'button', 'Save changes').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]!.routeModels).toEqual([
      { model: 'haiku', tier: 'cheap' },
      { model: 'opus', tier: 'premium' },
    ]);
  });

  it('removes an agent only after a confirmation, and shows why when it is refused', async () => {
    const archived: string[] = [];
    const { r } = show([agent()], {
      agentArchive: async (id) => {
        archived.push(id);
        throw new Error('Pay still has unfinished tasks (Do billing): reassign or cancel them first');
      },
    });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await r.act(() => byText(ed, 'button', 'Remove from team').click());
    expect(archived).toEqual([]);
    expect(ed.textContent).toContain('Its history stays');
    await r.act(() => byText(ed, 'button', 'Yes, remove').click());
    await waitFor(() => expect(archived).toEqual(['agt_1']), r);
    await waitFor(() => expect(ed.querySelector('[role="alert"]')!.textContent).toContain('unfinished tasks'), r);
  });

  it('the card Remove button asks first, then removes and re-reads the team', async () => {
    const archived: string[] = [];
    const { r, listed } = show([agent()], { agentArchive: async (id) => (archived.push(id), agent()) });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Remove Pay').click());
    const box = await waitFor(() => must(r.container.querySelector<HTMLElement>('[data-testid="agent-remove"]'), 'no dialog'), r);
    expect(archived).toEqual([]);
    expect(box.textContent).toContain('Remove Pay from the team? Its history stays.');
    const before = listed();
    await r.act(() => byText(box, 'button', 'Yes, remove').click());
    await waitFor(() => expect(archived).toEqual(['agt_1']), r);
    await waitFor(() => expect(listed()).toBeGreaterThan(before), r);
    await waitFor(() => expect(r.container.querySelector('[data-testid="agent-remove"]')).toBe(null), r);
  });

  it('Inspect opens the inspector in the details panel; Message opens a direct message', async () => {
    const dms: [string, string][] = [];
    const { r, s, navigated } = show([agent()], { roomCreateDM: async (p, a) => (dms.push([p, a]), { id: 'room_dm' } as never) });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Inspect').click());
    expect(s.ui.inspectId).toBe('agt_1');
    expect(s.ui.tab).toBe('inspector');
    await r.act(() => byText(card, 'button', 'Message').click());
    await waitFor(() => expect(navigated).toEqual(['/room/room_dm']), r);
    expect(dms).toEqual([['prj_1', 'agt_1']]);
  });

  it('read-only: creating is turned off with the reason, and the note says so', async () => {
    const { r } = show([], { licenseStatus: async () => expiredLicense });
    await waitAdapters(r);
    await waitFor(() => must(r.container.querySelector('[data-testid="readonly-note"]'), 'no note'), r);
    type(byLabel(r.container, 'Agent name'), 'blocked');
    type(byLabel(r.container, 'Role'), 'backend');
    r.root.flush();
    const make = byText<HTMLButtonElement>(r.container, 'button', 'Create agent');
    expect(make.disabled).toBe(true);
    expect(make.title).toBe(expiredLicense.banner!);
  });

  it('lists adapters ready first, with capabilities that read as not verified when absent', async () => {
    const { r } = show([], {}, [{ ...opencode, id: 'zeta', detection: { adapter: 'zeta', installed: false, status: 'not_installed', installHint: 'npm i -g zeta' } }, claude]);
    await waitFor(() => expect(r.$$('[data-testid="adapter"]').length).toBe(2), r);
    const cards = Array.from(r.$$('[data-testid="adapter"]'));
    expect(cards.map((c) => c.getAttribute('data-adapter'))).toEqual(['claude', 'zeta']);
    expect(cards[1]!.textContent).toContain('Drydock never runs this for you');
    expect(cards[1]!.textContent).toContain('Streaming: not verified');
    expect(cards[0]!.querySelector('[data-cap="modelSelection"]')!.getAttribute('data-verified')).toBe('true');
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
    await waitFor(() => expect(r.container.textContent).toContain('can be added here with one click'), r);
    expect(r.container.querySelector('a[href="/library"]')).toBeTruthy();
  });

  it('saves an existing agent as a template and points to the Library', async () => {
    const calls: string[] = [];
    const { r } = show([agent()], { agentTemplateFromAgent: async (id) => (calls.push(id), template) });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
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
    await waitAdapters(r);
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
    await waitAdapters(r);
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
    await waitAdapters(r);
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
    const { r } = show([agent({ model: 'opus', effort: 'max' })], { claudePractices: async (role) => practices(role) });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await waitFor(() => must(ed.querySelector('[data-testid="claude-practices"]'), 'no box'), r);
    expect(valueOf(byLabel(ed, 'Model'))).toBe('opus');
    expect(valueOf(byLabel(ed, 'Effort'))).toBe('max');
  });

  it('adds a prebuilt agent from the gallery dialog and re-reads the team', async () => {
    const applied: string[] = [];
    const { r, listed } = show([agent()], {
      agentCatalogList: async () => CATALOG,
      agentCatalogApply: async (req) => (applied.push(`${req.id}:${req.projectId}`), { id: req.id, agent: agent({ id: 'agt_9', name: 'Team Lead' }), mcp: { granted: [], skipped: [] }, skills: { assigned: [], shared: [], pending: [], failed: [] }, notes: [] }),
    });
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Add prebuilt agent').disabled).toBe(false), r);
    await r.act(() => byText(r.container, 'button', 'Add prebuilt agent').click());
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
  audience: 'team',
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
    sk('wrangler', { audience: 'agents', agents: ['agt_9'] }),
    sk('waiting', { status: 'offered' }),
    sk('flagged', { status: 'blocked' }),
  ];
  const edge = agent({ id: 'agt_9', name: 'Edge' });

  it('the new-agent form gives the ticked skills; a skill the whole team gets for the role is included and cannot be unticked', async () => {
    const calls: unknown[] = [];
    const { r } = show([edge], { skillList: async () => skills, agentCreate: async (spec) => (calls.push(spec), { ...agent(), ...spec } as Agent) });
    await waitAdapters(r);
    const form = must(r.container.querySelector<HTMLElement>('[data-testid="agent-form"]'), 'no form');
    await waitFor(() => skillBox(form, 'wrangler'), r);
    type(byLabel(form, 'Agent name'), 'Pay');
    type(byLabel(form, 'Role'), 'backend');
    r.root.flush();
    // only accepted skills are listed; the others wait on the Skills page
    expect(Array.from(form.querySelectorAll('[data-testid="agent-skills"] code')).map((c) => c.textContent)).toEqual(['everyone', 'react', 'wrangler']);
    expect(form.querySelector('[data-testid="agent-skills"]')?.textContent).toContain('One skill waits for your decision on the Skills page.');
    const everyone = skillBox(form, 'everyone');
    expect(everyone.input.checked && everyone.input.disabled).toBe(true);
    expect(everyone.who).toBe('Included: it goes to the whole team.');
    expect(skillBox(form, 'react').who).toBe('Now it goes to every Frontend agent.');
    expect(skillBox(form, 'wrangler').who).toBe('Now it goes to only Edge.');
    expect(skillBox(form, 'react').input.disabled || skillBox(form, 'react').input.checked).toBe(false);
    // tick two, change your mind about one
    await r.act(() => skillBox(form, 'react').input.click());
    await r.act(() => skillBox(form, 'wrangler').input.click());
    await r.act(() => skillBox(form, 'wrangler').input.click());
    expect(skillBox(form, 'react').input.checked).toBe(true);
    await r.act(() => byText(form, 'button', 'Create agent').click());
    await waitFor(() => expect(calls).toEqual([{ projectId: 'prj_1', name: 'Pay', role: 'backend', purpose: '', adapter: 'claude', model: '', effort: '', practices: true, skills: ['react'] }]), r);
    // the form is cleared, skills included
    await waitFor(() => expect(skillBox(form, 'react').input.checked).toBe(false), r);
    // a frontend agent gets react with the team
    type(byLabel(form, 'Role'), 'Frontend ');
    r.root.flush();
    expect(skillBox(form, 'react').input.checked && skillBox(form, 'react').input.disabled).toBe(true);
  });

  it('sends no skills when none is ticked, and says when the project has no accepted skill', async () => {
    const calls: Record<string, unknown>[] = [];
    const { r } = show([], { skillList: async () => [sk('later', { status: 'offered' })], agentCreate: async (spec) => (calls.push(spec as never), agent()) });
    await waitAdapters(r);
    const form = must(r.container.querySelector<HTMLElement>('[data-testid="agent-form"]'), 'no form');
    await waitFor(() => expect(form.querySelector('[data-testid="agent-skills"]')?.textContent).toContain('No skill is accepted in this project yet.'), r);
    type(byLabel(form, 'Agent name'), 'Q');
    type(byLabel(form, 'Role'), 'qa');
    r.root.flush();
    await r.act(() => byText(form, 'button', 'Create agent').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect('skills' in calls[0]!).toBe(false);
  });

  it('the editor shows the skills the agent has and sends only what changed', async () => {
    const calls: unknown[] = [];
    const pay = agent();
    const mine = [
      sk('everyone', { agents: ['agt_1'] }), // included by role and given: left alone
      sk('react', { roles: ['Frontend'], agents: ['agt_1'] }),
      sk('wrangler', { audience: 'agents', agents: ['agt_9', 'agt_1'] }),
      sk('docs', { audience: 'agents', agents: [] }),
      sk('flagged', { status: 'blocked', audience: 'agents', agents: ['agt_1'] }), // not shown, never taken away
    ];
    const { r } = show([pay, edge], { skillList: async () => mine, agentUpdate: async (u) => (calls.push(u), pay) });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await waitFor(() => skillBox(ed, 'docs'), r);
    expect(skillBox(ed, 'react').input.checked).toBe(true);
    expect(skillBox(ed, 'react').who).toBe('Now it goes to every Frontend agent.');
    expect(skillBox(ed, 'wrangler').input.checked).toBe(true);
    expect(skillBox(ed, 'wrangler').who).toBe('Now it goes to only Edge.');
    expect(skillBox(ed, 'docs').input.checked).toBe(false);
    expect(skillBox(ed, 'docs').who).toBe('Now it goes to nobody: no agent is chosen yet.');
    expect(skillBox(ed, 'everyone').input.disabled).toBe(true);
    await r.act(() => skillBox(ed, 'wrangler').input.click());
    await r.act(() => skillBox(ed, 'docs').input.click());
    await r.act(() => byText(ed, 'button', 'Save changes').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toEqual({
      id: 'agt_1',
      name: 'Pay',
      role: 'backend',
      purpose: 'Own the payments service.',
      model: 'sonnet',
      effort: 'high',
      practices: true,
      addSkills: ['docs'],
      removeSkills: ['wrangler'],
    });
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
      await waitFor(() => expect(byText<HTMLButtonElement>(t.r.container, 'button', 'Add prebuilt agent').disabled).toBe(false), t.r);
      await t.r.act(() => byText(t.r.container, 'button', 'Add prebuilt agent').click());
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
    const { r } = show([agent({ permissionMode: 'bypassPermissions' })], {
      agentTemplateFromAgent: async () => {
        throw new Error(refusal);
      },
    });
    const card = await waitAgent(r, 'Pay');
    await r.act(() => byText(card, 'button', 'Edit Pay').click());
    const ed = await waitFor(() => must(editor(r), 'no editor'), r);
    await r.act(() => byText(ed, 'button', 'Save as a template').click());
    const note = await waitFor(() => must(ed.querySelector<HTMLElement>('[data-testid="template-needs-confirmation"]'), 'no note yet'), r);
    expect(note.textContent).toContain(refusal);
    expect(note.textContent).toContain('Make the template in the Library');
    expect(note.querySelector('a[href="/library"]')).not.toBeNull();
    expect(ed.textContent).not.toContain('Agent not changed');
  });
});
