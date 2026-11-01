import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { AgentDraft } from '../../api/libraryClient.ts';
import type { AdapterInfo, Agent, AgentSpec, AgentUpdate, Project, UpdatedAgent } from '../../api/types.ts';
import { SKIP_CONFIRM_LABEL } from '../../lib/permission.ts';
import { lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { AgentPanelHost, agentEditorOpenFor, closeAgentEditor, openAgentEditor } from './agent-panel.tsx';
import { TeamScreen } from './team-screen.tsx';

afterEach(() => {
  closeAgentEditor();
  cleanup();
});

const claude: AdapterInfo = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready' },
  capabilities: {
    modelSelection: true,
    effortControl: true,
    efforts: ['low', 'medium', 'high', 'xhigh', 'max'],
    models: ['opus', 'sonnet', 'haiku'],
    permissionModes: ['default', 'acceptEdits', 'plan', 'dontAsk', 'auto', 'bypassPermissions'],
  } as AdapterInfo['capabilities'],
};
const agent = (over: Partial<Agent> = {}): Agent => ({
  id: 'agt_1',
  projectId: 'prj_1',
  name: 'Backend Engineer',
  adapter: 'claude',
  role: 'backend',
  model: 'sonnet',
  effort: 'medium',
  providerId: '',
  permissionMode: 'default',
  purpose: 'Write Go that is small and tested.',
  status: 'STOPPED',
  ...over,
});

function must<T>(x: T | null | undefined, msg: string): T {
  if (x === null || x === undefined) throw new Error(msg);
  return x;
}

function svc(agents: Agent[], client: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'calculator', key: 'CAL' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    agentList: async () => agents,
    presenceList: async () => [],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    taskList: async () => [],
    adapterList: async () => [claude],
    agentTemplateList: async () => [],
    skillList: async () => [],
    mcpLibList: async () => [],
    mcpServerList: async () => [],
    crewExtras: async () => ({ skills: [], rooms: [] }),
    agentTimeline: async (since) => ({ since, until: since, agents: [] }),
    taskSummaries: async () => ({ summaries: [], total: 0, partial: false }),
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

const panel = (r: { $: (sel: string) => Element | null }) => r.$('dialog[open] [data-testid="agent-editor"]') as HTMLElement | null;
const radio = (root: ParentNode, group: string, value: string) => must(root.querySelector<HTMLButtonElement>(`[role="radiogroup"][aria-label="${group}"] [data-value="${value}"]`), `no ${group} ${value}`);

describe('the agent editor panel (P137)', () => {
  it('opens from the model chip on a Crew card, changes model, effort and permission, and says where it applies', async () => {
    const calls: AgentUpdate[] = [];
    const a = agent();
    const s = svc([a], { agentUpdate: async (u) => (calls.push(u), { ...a, ...u } as UpdatedAgent) });
    const r = mount(<TeamScreen now={() => Date.now()} />, s);
    const chip = await waitFor(() => must(r.$('[data-testid="agent"] [data-testid="agent-model-chip"]') as HTMLElement | null, 'no chip'), r);
    expect(chip.textContent).toBe('sonnet · medium');
    expect(r.$('[data-testid="agent-permission-chip"]')!.textContent).toBe('Asks before edits');
    await r.act(() => chip.click());
    const ed = await waitFor(() => must(panel(r), 'no panel'), r);
    expect(agentEditorOpenFor()).toBe('agt_1');
    expect(ed.closest('dialog')!.textContent).toContain('in calculator');
    expect(ed.querySelector('[data-testid="agent-runs-on"]')!.textContent).toBe('Claude Code');
    expect(ed.textContent).toContain('Applies from its next turn');
    type(byLabel(ed, 'Model'), 'opus');
    await r.act(() => radio(ed, 'Effort', 'high').click());
    await r.act(() => radio(ed, 'Permissions', 'plan').click());
    expect(ed.querySelector('[data-testid="scope-note"]')!.textContent).toBe('opus · high is used for Backend Engineer in calculator only. Other projects keep their own settings.');
    await r.act(() => byText(ed, 'button', 'Save').click());
    await waitFor(() => expect(calls).toEqual([{ id: 'agt_1', model: 'opus', effort: 'high', permissionMode: 'plan' }]), r);
    await waitFor(() => expect(panel(r)).toBeNull(), r);
  });

  it('Everywhere also saves to the template, and a mode that skips approvals needs the box first', async () => {
    const calls: AgentUpdate[] = [];
    const a = agent({ templateId: 'tpl_1' });
    const s = svc([a], {
      agentUpdate: async (u) => (calls.push(u), { ...a, ...u, everywhere: { templateId: 'tpl_1', templateName: 'Backend Engineer', created: false, agents: 2, skipped: [] } } as UpdatedAgent),
    });
    const saved: string[] = [];
    const r = mount(<AgentPanelHost onSaved={(_, n) => saved.push(n)} />, s);
    await waitFor(() => expect(s.project.agentList.length).toBe(1), r);
    await r.act(() => openAgentEditor('agt_1'));
    const ed = await waitFor(() => must(panel(r), 'no panel'), r);
    await r.act(() => radio(ed, 'Save for', 'everywhere').click());
    expect(ed.querySelector('[data-testid="scope-note"]')!.textContent).toContain("template and to every agent made from it");
    // "Other..." holds the modes that skip approvals; choosing one asks for the box before Save
    const other = byLabel<HTMLElement>(ed, 'Other permission mode');
    type(other, 'bypassPermissions');
    r.root.flush();
    const save = byText<HTMLButtonElement>(ed, 'button', 'Save');
    expect(save.disabled).toBe(true);
    await r.act(() => byLabel<HTMLInputElement>(ed, SKIP_CONFIRM_LABEL).click());
    expect(save.disabled).toBe(false);
    await r.act(() => save.click());
    await waitFor(() => expect(calls).toEqual([{ id: 'agt_1', permissionMode: 'bypassPermissions', confirmSkipsApprovals: true, everywhere: true }]), r);
    await waitFor(() => expect(saved).toEqual(['Saved to the template Backend Engineer and 2 more agents.']), r);
  });

  it('never leaves model and effort disabled while the CLI is read, or when it cannot be', async () => {
    let fail: (e: Error) => void = () => {};
    const s = svc([agent()], { adapterList: () => new Promise<AdapterInfo[]>((_, rej) => (fail = rej)) });
    const r = mount(<AgentPanelHost />, s);
    await waitFor(() => expect(s.project.agentList.length).toBe(1), r);
    await r.act(() => openAgentEditor('agt_1'));
    const ed = await waitFor(() => must(panel(r), 'no panel'), r);
    expect(ed.querySelector('[data-testid="caps-loading"]')).not.toBeNull();
    const model = byLabel<HTMLElement>(ed, 'Model');
    expect(model.hasAttribute('disabled')).toBe(false);
    expect(radio(ed, 'Effort', 'max').disabled).toBe(false);
    await r.act(() => fail(new Error('the CLI did not answer')));
    await waitFor(() => must(ed.querySelector('[data-testid="caps-failed"]'), 'no failure note'), r);
    // the known Claude Code levels stay offered
    expect(radio(ed, 'Effort', 'low').disabled).toBe(false);
  });
});

describe('describe a new agent (P138)', () => {
  const drafted: AgentDraft = {
    name: 'Security Reviewer',
    role: 'security reviewer',
    adapter: 'claude',
    model: 'opus',
    effort: 'high',
    permissionMode: 'plan',
    purpose: 'You review diffs for security problems. You never edit.',
    skills: [],
    tools: [],
    why: 'Read only, because you said "never edit".',
    source: 'rules',
  };

  it('opens on Describe, drafts from a sentence, lets the person change the draft and creates it', async () => {
    const asked: [string, string | undefined][] = [];
    const created: AgentSpec[] = [];
    const s = svc([], {
      agentDraft: async (d, p) => (asked.push([d, p]), drafted),
      agentCreate: async (spec) => (created.push(spec), agent({ ...spec, id: 'agt_9' } as Partial<Agent>)),
    });
    const r = mount(<TeamScreen now={() => Date.now()} />, s);
    const btn = await waitFor(() => {
      const b = byText<HTMLButtonElement>(r.container, 'button', /^New agent/);
      if (b.disabled) throw new Error('no project yet');
      return b;
    }, r);
    await r.act(() => btn.click());
    const box = await waitFor(() => must(r.$('dialog[open] [data-testid="describe-agent"]') as HTMLElement | null, 'no Describe'), r);
    // the starters fill the box and draft at once
    expect(Array.from(box.querySelectorAll('[role="group"][aria-label="Start from"] button')).map((b) => b.textContent)).toEqual(['Builder', 'Reviewer', 'Tester', 'Docs writer', 'Team lead', 'Planner']);
    type(byLabel<HTMLTextAreaElement>(box, 'What should this agent do?'), 'Reviews PRs for security. Never edit code. Best model.');
    r.root.flush();
    await r.act(() => byText(box, 'button', 'Draft agent').click());
    const d = await waitFor(() => must(box.querySelector<HTMLElement>('[data-testid="agent-draft"]'), 'no draft'), r);
    expect(asked).toEqual([['Reviews PRs for security. Never edit code. Best model.', 'prj_1']]);
    expect(d.querySelector('[data-testid="draft-why"]')!.textContent).toContain('never edit');
    expect(box.querySelector('[data-testid="draft-source"]')!.textContent).toContain('no model was used');
    type(byLabel(d, 'Agent name'), 'Sec');
    type(byLabel<HTMLElement>(d, 'Effort'), 'max');
    r.root.flush();
    await r.act(() => byText(d, 'button', 'Create agent').click());
    await waitFor(
      () =>
        expect(created).toEqual([
          { projectId: 'prj_1', name: 'Sec', role: 'security reviewer', adapter: 'claude', model: 'opus', effort: 'max', permissionMode: 'plan', purpose: 'You review diffs for security problems. You never edit.' },
        ]),
      r,
    );
    await waitFor(() => expect(r.$('dialog[open]')).toBeNull(), r);
  });
});
