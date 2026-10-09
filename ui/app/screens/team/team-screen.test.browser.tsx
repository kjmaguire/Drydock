import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { AgentTemplate, ClaudePractices } from '../../api/libraryClient.ts';
import type { AdapterInfo, Agent, Project } from '../../api/types.ts';
import { expiredLicense, lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { CATALOG } from '../project/wizard-fixtures.ts';
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
    if (byLabel<HTMLSelectElement>(r.container, 'Adapter').options.length === 0) throw new Error('no adapters yet');
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
    const effort = byLabel<HTMLSelectElement>(r.container, 'Effort');
    expect(effort.tagName).toBe('SELECT');
    expect(Array.from(effort.options).map((o) => o.value)).toEqual(['', 'low', 'medium', 'high', 'xhigh', 'max']);
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
    type(byLabel<HTMLSelectElement>(r.container, 'Adapter'), 'opencode');
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
    type(byLabel<HTMLSelectElement>(ed, 'Effort'), 'low');
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
    expect(byLabel<HTMLInputElement>(ed, 'Routed model 1').value).toBe('haiku');
    expect(byLabel<HTMLSelectElement>(ed, 'Tier of routed model 2').value).toBe('premium');
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

const template: AgentTemplate = { id: 'tpl_1', name: 'Payments dev', role: 'backend', purpose: 'Own payments.', adapter: 'claude', model: 'sonnet', effort: 'high', permissionMode: '', providerId: '', mcpServers: ['docs'], createdAt: 1, updatedAt: 1 };

describe('Team: templates', () => {
  it('adds an agent from a template and says what it was granted', async () => {
    const calls: unknown[][] = [];
    const { r, listed } = show([], {
      agentTemplateList: async () => [template],
      agentTemplateApply: async (...a) => (calls.push(a), { agent: agent({ name: 'Payments dev' }), granted: ['docs'], skipped: [] }),
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
    await waitFor(() => expect(byLabel<HTMLInputElement>(r.container, 'Model').value).toBe('sonnet'), r);
    expect(byLabel<HTMLSelectElement>(r.container, 'Effort').value).toBe('medium');
    expect(box.textContent).toContain('Recommended for a backend (worker)');
    expect(box.querySelector('[data-testid="claude-practices-text"]')!.textContent).toContain('Never skip, delete or weaken a test');
    // a new role moves the recommendation while the fields are still the recommended ones
    type(byLabel(r.container, 'Role'), 'reviewer');
    await waitFor(() => expect(byLabel<HTMLSelectElement>(r.container, 'Effort').value).toBe('high'), r);
    // once the human picks something else, the recommendation stops overwriting it
    type(byLabel<HTMLSelectElement>(r.container, 'Effort'), 'low');
    type(byLabel(r.container, 'Role'), 'backend');
    await waitFor(() => expect(asked.filter((x) => x === 'backend').length).toBe(2), r);
    await new Promise((res) => setTimeout(res, 300));
    r.root.flush();
    expect(byLabel<HTMLSelectElement>(r.container, 'Effort').value).toBe('low');
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

  it('shows nothing for an adapter that is not Claude Code', async () => {
    const asked: string[] = [];
    const { r } = show([], { claudePractices: async (role) => (asked.push(role), practices(role)) });
    await waitAdapters(r);
    type(byLabel<HTMLSelectElement>(r.container, 'Adapter'), 'opencode');
    type(byLabel(r.container, 'Role'), 'backend');
    await new Promise((res) => setTimeout(res, 300));
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
    expect(byLabel<HTMLInputElement>(ed, 'Model').value).toBe('opus');
    expect(byLabel<HTMLSelectElement>(ed, 'Effort').value).toBe('max');
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
