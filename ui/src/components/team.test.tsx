import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { AdapterInfo, Agent } from '../api/types';
import { AppServicesProvider } from '../state/context';
import { services } from '../test/fakes';
import { TeamView } from './Team';

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
    await waitFor(() => expect(agentCreate).toHaveBeenCalledWith({ projectId: 'prj_1', name: 'Pay', role: 'backend', purpose: 'Own payments.', adapter: 'claude', model: 'opus', effort: 'max' }));
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
    await waitFor(() => expect(agentUpdate).toHaveBeenCalledWith({ id: 'agt_1', name: 'Pay', role: 'backend', purpose: 'Billing only.', model: 'haiku', effort: 'low' }));
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
