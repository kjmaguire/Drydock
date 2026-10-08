import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { AdapterInfo, Agent } from '../api/types';
import { AppServicesProvider } from '../state/context';
import { services } from '../test/fakes';
import { AgentInspector } from './Inspector';
import { AgentEnvSection } from './Settings';
import { TeamView } from './Team';
import type { AgentTemplate } from '../api/libraryClient';

const claude: AdapterInfo = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready' } as AdapterInfo['detection'],
  capabilities: { modelSelection: true, effortControl: true, efforts: ['low', 'medium', 'high', 'xhigh', 'max'], models: ['opus', 'sonnet', 'haiku'] } as AdapterInfo['capabilities'],
};
const opencode: AdapterInfo = {
  id: 'opencode',
  detection: { adapter: 'opencode', installed: true, status: 'ready' } as AdapterInfo['detection'],
  capabilities: { modelSelection: true, effortControl: true } as AdapterInfo['capabilities'],
};
const agent = (over: Partial<Agent> = {}): Agent => ({
  id: 'agt_1', projectId: 'prj_1', name: 'Pay', adapter: 'claude', role: 'backend', model: 'sonnet', effort: 'high', providerId: '', permissionMode: 'acceptEdits', purpose: 'Own the payments service.', status: 'STOPPED', ...over,
});

const show = (agents: Agent[], client: Partial<Client> = {}, adapters = [claude, opencode]) => {
  const onCreated = vi.fn();
  render(
    <AppServicesProvider services={services({ adapterList: async () => adapters, ...client })}>
      <TeamView projectId="prj_1" agents={agents} presence={[]} onCreated={onCreated} />
    </AppServicesProvider>,
  );
  return onCreated;
};

describe('Team: purpose, model and effort', () => {
  it('creates an agent with a purpose, a model from the adapter and an effort level from the adapter list', async () => {
    const agentCreate = vi.fn(async (s: object) => ({ ...agent(), ...s }) as Agent);
    show([], { agentCreate });
    fireEvent.change(await screen.findByLabelText('Agent name'), { target: { value: 'Pay' } });
    fireEvent.change(screen.getByLabelText('Role', { exact: true }), { target: { value: 'backend' } });
    fireEvent.change(screen.getByLabelText(/Purpose/), { target: { value: ' Own payments. ' } });
    fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'opus' } });
    const effort = screen.getByLabelText('Effort') as HTMLSelectElement;
    expect(effort.tagName).toBe('SELECT');
    expect(Array.from(effort.options).map((o) => o.value)).toEqual(['', 'low', 'medium', 'high', 'xhigh', 'max']);
    fireEvent.change(effort, { target: { value: 'max' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create agent' }));
    await waitFor(() => expect(agentCreate).toHaveBeenCalledWith({ projectId: 'prj_1', name: 'Pay', role: 'backend', purpose: 'Own payments.', adapter: 'claude', model: 'opus', effort: 'max', practices: true }));
  });

  it('keeps effort free text for an adapter that does not list its levels', async () => {
    show([], {});
    fireEvent.change(await screen.findByLabelText('Adapter', { exact: true }), { target: { value: 'opencode' } });
    expect((screen.getByLabelText('Effort') as HTMLElement).tagName).toBe('INPUT');
  });

  it('shows the purpose on the team list and edits model, effort and purpose of an existing agent', async () => {
    const agentUpdate = vi.fn(async (u: object) => ({ ...agent(), ...u }) as Agent);
    const onCreated = show([agent()], { agentUpdate });
    expect((await screen.findByTestId('agent-purpose')).textContent).toContain('Own the payments service.');
    fireEvent.click(screen.getByRole('button', { name: 'Edit Pay' }));
    const editor = await screen.findByTestId('agent-editor');
    expect(editor.textContent).toContain('cannot change');
    const ed = within(editor);
    fireEvent.change(ed.getByLabelText('Model'), { target: { value: 'haiku' } });
    fireEvent.change(ed.getByLabelText('Effort'), { target: { value: 'low' } });
    fireEvent.change(ed.getByLabelText('Purpose'), { target: { value: 'Billing only.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(agentUpdate).toHaveBeenCalledWith({ id: 'agt_1', name: 'Pay', role: 'backend', purpose: 'Billing only.', model: 'haiku', effort: 'low', practices: true }));
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
    expect(screen.queryByTestId('agent-editor')).toBeNull();
  });

  it('shows the core refusal and keeps the editor open', async () => {
    const agentUpdate = vi.fn(async () => {
      throw new Error('claude accepts these effort levels: low, medium, high, xhigh, max (got "ultra")');
    });
    show([agent()], { agentUpdate });
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Pay' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Save changes' }));
    expect((await screen.findByRole('alert')).textContent).toContain('accepts these effort levels');
    expect(screen.getByTestId('agent-editor')).toBeTruthy();
  });

  it('removes an agent only after a confirmation, and shows why when it is refused', async () => {
    const agentArchive = vi.fn(async (_id: string): Promise<Agent> => {
      throw new Error('Pay still has unfinished tasks (Do billing): reassign or cancel them first');
    });
    show([agent()], { agentArchive });
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Pay' }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove from team' }));
    expect(agentArchive).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Yes, remove' }));
    await waitFor(() => expect(agentArchive).toHaveBeenCalledWith('agt_1'));
    expect((await screen.findByRole('alert')).textContent).toContain('unfinished tasks');
  });
});

const template: AgentTemplate = { id: 'tpl_1', name: 'Payments dev', role: 'backend', purpose: 'Own payments.', adapter: 'claude', model: 'sonnet', effort: 'high', permissionMode: '', providerId: '', mcpServers: ['docs'], createdAt: 1, updatedAt: 1 };

describe('Team: templates', () => {
  it('adds an agent from a template and says what it was granted', async () => {
    const agentTemplateApply = vi.fn(async () => ({ agent: agent({ name: 'Payments dev' }), granted: ['docs'], skipped: [] }));
    const onCreated = show([], { agentTemplateList: async () => [template], agentTemplateApply });
    fireEvent.change(await screen.findByLabelText('Name (optional)'), { target: { value: ' Billing ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add to this team' }));
    await waitFor(() => expect(agentTemplateApply).toHaveBeenCalledWith('tpl_1', 'prj_1', 'Billing'));
    expect((await screen.findByRole('status')).textContent).toContain('Payments dev joined the team, with docs');
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
  });

  it('saves an existing agent as a template and points to the Library', async () => {
    const agentTemplateFromAgent = vi.fn(async () => template);
    show([agent()], { agentTemplateFromAgent });
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Pay' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Save as a template' }));
    await waitFor(() => expect(agentTemplateFromAgent).toHaveBeenCalledWith('agt_1'));
    expect((await screen.findByTestId('saved-as-template')).textContent).toContain('Payments dev');
  });
});

describe('Agent environment and isolation', () => {
  it('shows what agents keep, saves names only and shows the core refusal of a value', async () => {
    let fail = false;
    const agentEnvSet = vi.fn(async (names: string[]) => {
      if (fail) throw new Error('"AWS_PROFILE=work" is not an environment variable name (give names only, never values)');
      return { pass: names, kept: ['PATH'], prefixes: ['CLAUDE_'] };
    });
    render(
      <AppServicesProvider services={services({ agentEnvGet: async () => ({ pass: ['AWS_PROFILE'], kept: ['PATH', 'HOME'], prefixes: ['CLAUDE_', 'ANTHROPIC_'] }), agentEnvSet })}>
        <AgentEnvSection />
      </AppServicesProvider>,
    );
    const box = (await screen.findByLabelText(/Also pass these variables/)) as HTMLTextAreaElement;
    await waitFor(() => expect(box.value).toBe('AWS_PROFILE'));
    fireEvent.change(box, { target: { value: 'AWS_PROFILE\n\n CLAUDE_CODE_USE_BEDROCK ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(agentEnvSet).toHaveBeenCalledWith(['AWS_PROFILE', 'CLAUDE_CODE_USE_BEDROCK']));
    expect((await screen.findByRole('status')).textContent).toContain('Saved');
    fail = true;
    fireEvent.change(box, { target: { value: 'AWS_PROFILE=work' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toContain('names only');
  });

  it('the inspector states the isolation in plain words, and warns when the permission mode lets the agent act freely', async () => {
    const detail = (level: 'guarded' | 'open') => ({
      agent: agent(),
      runs: [],
      usage: [],
      isolation: { level, summary: level === 'guarded' ? 'Claude Code checks keep it to its folder. This is not an operating-system sandbox.' : 'Its permission mode lets it act without asking.', details: ['Its own worktree.'] },
    });
    const { unmount } = render(
      <AppServicesProvider services={services({ agentGet: async () => detail('guarded') })}>
        <AgentInspector agentId="agt_1" presence={[]} />
      </AppServicesProvider>,
    );
    const box = await screen.findByTestId('inspector-isolation');
    expect(box.getAttribute('data-level')).toBe('guarded');
    expect(box.textContent).toContain('not an operating-system sandbox');
    unmount();
    render(
      <AppServicesProvider services={services({ agentGet: async () => detail('open') })}>
        <AgentInspector agentId="agt_1" presence={[]} />
      </AppServicesProvider>,
    );
    const open = await screen.findByTestId('inspector-isolation');
    expect(open.getAttribute('data-level')).toBe('open');
    expect(open.textContent).toContain('not held in');
  });
});

describe('Claude best practices', () => {
  const practices = (role: string) => ({
    version: 1,
    role,
    family: role === 'reviewer' ? ('reviewer' as const) : ('worker' as const),
    text: role === 'reviewer' ? 'Working practices ... You do not modify files.' : 'Working practices ... Never skip, delete or weaken a test',
    preset: role === 'reviewer' ? { model: 'sonnet', effort: 'high', why: 'Review is where verification matters.' } : { model: 'sonnet', effort: 'medium', why: 'Sonnet handles most coding tasks.' },
  });

  it('fills the recommended model and effort for the role until the human changes them, and shows exactly what is added', async () => {
    const claudePractices = vi.fn(async (role: string) => practices(role));
    const agentCreate = vi.fn(async (s: object) => ({ ...agent(), ...s }) as Agent);
    show([], { claudePractices, agentCreate });
    fireEvent.change(await screen.findByLabelText('Role', { exact: true }), { target: { value: 'backend' } });
    const box = await screen.findByTestId('claude-practices');
    await waitFor(() => expect((screen.getByLabelText('Model') as HTMLInputElement).value).toBe('sonnet'));
    expect((screen.getByLabelText('Effort') as HTMLSelectElement).value).toBe('medium');
    expect(box.textContent).toContain('Recommended for a backend (worker)');
    expect(within(box).getByTestId('claude-practices-text').textContent).toContain('Never skip, delete or weaken a test');
    // a new role moves the recommendation while the fields are still the recommended ones
    fireEvent.change(screen.getByLabelText('Role', { exact: true }), { target: { value: 'reviewer' } });
    await waitFor(() => expect((screen.getByLabelText('Effort') as HTMLSelectElement).value).toBe('high'));
    // once the human picks something else, the recommendation stops overwriting it
    fireEvent.change(screen.getByLabelText('Effort'), { target: { value: 'low' } });
    fireEvent.change(screen.getByLabelText('Role', { exact: true }), { target: { value: 'backend' } });
    await waitFor(() => expect(claudePractices).toHaveBeenCalledWith('backend'));
    await new Promise((r) => setTimeout(r, 300));
    expect((screen.getByLabelText('Effort') as HTMLSelectElement).value).toBe('low');
    // the switch is on by default and can be turned off
    const sw = screen.getByLabelText('Follow Claude Code best practices') as HTMLInputElement;
    expect(sw.checked).toBe(true);
    fireEvent.click(sw);
    fireEvent.change(screen.getByLabelText('Agent name'), { target: { value: 'Pay' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create agent' }));
    await waitFor(() => expect(agentCreate).toHaveBeenCalled());
    expect(agentCreate.mock.calls[0]![0]).toMatchObject({ role: 'backend', model: 'sonnet', effort: 'low', practices: false });
  });

  it('shows nothing for an adapter that is not Claude Code', async () => {
    const claudePractices = vi.fn(async (role: string) => practices(role));
    show([], { claudePractices });
    fireEvent.change(await screen.findByLabelText('Adapter', { exact: true }), { target: { value: 'opencode' } });
    fireEvent.change(screen.getByLabelText('Role', { exact: true }), { target: { value: 'backend' } });
    await new Promise((r) => setTimeout(r, 300));
    expect(screen.queryByTestId('claude-practices')).toBeNull();
    expect(claudePractices).not.toHaveBeenCalled();
  });
});
