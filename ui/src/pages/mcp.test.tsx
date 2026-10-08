import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { McpApproval, McpServer } from '../api/mcpClient';
import type { Agent } from '../api/types';
import { AppServicesProvider } from '../state/context';
import { LicenseProvider } from '../state/license';
import { services } from '../test/fakes';
import McpServersView, { inputOf, parseEnv, parseLines } from './McpServers';

const agent = (id: string, name: string, role: string): Agent => ({ id, projectId: 'prj_1', name, adapter: 'claude', role, model: '', effort: '', providerId: '', permissionMode: 'acceptEdits', status: 'STOPPED' });
const pay = agent('agt_1', 'Pay', 'backend');
const rev = agent('agt_2', 'Rev', 'reviewer');

const server = (over: Partial<McpServer> = {}): McpServer => ({
  projectId: 'prj_1', id: 'tst', display: 'Test server', transport: 'stdio', argv: ['C:\\tools\\srv.exe', '--flag'], env: { MODE: 'x' }, secretEnv: ['API_KEY'], hasToken: false, guard: true,
  readOnlyTools: [], tools: [{ name: 'echo', description: 'echo text', readOnlyHint: true }, { name: 'post_note', readOnlyHint: false }], discoveredAt: 1760000000000, enabled: true, agents: [], createdAt: 1, updatedAt: 1, ...over,
});

const show = (client: Partial<Client>) =>
  render(
    <AppServicesProvider services={services({ licenseStatus: async () => ({ configured: false, devMode: true, state: 'none', readOnly: false, message: '' }) as never, ...client })}>
      <LicenseProvider>
        <McpServersView projectId="prj_1" agents={[pay, rev]} />
      </LicenseProvider>
    </AppServicesProvider>,
  );

describe('MCP servers helpers', () => {
  it('reads one argument per line and NAME=value lines, and says which line is wrong', () => {
    expect(parseLines('npx\r\n-y\n\n  @x/y@1.0.0 \n')).toEqual(['npx', '-y', '  @x/y@1.0.0 ']);
    expect(parseEnv('A=1\nB=two=three', 'Environment')).toEqual({ A: '1', B: 'two=three' });
    expect(() => parseEnv('NOEQUALS', 'Environment')).toThrow('Environment: "NOEQUALS" is not NAME=value');
  });
  it('rebuilds a save input that keeps the stored secrets without sending them', () => {
    const i = inputOf(server());
    expect(i.secretEnv).toEqual({ API_KEY: '' });
    expect(i.bearerToken).toBeUndefined();
  });
});

describe('MCP servers page', () => {
  it('lists servers with secrets as names only, and tools the human has not marked read-only', async () => {
    show({ mcpServerList: async () => [server()], mcpApprovalList: async () => [] });
    const card = await screen.findByTestId('mcp-server');
    expect(card.textContent).toContain('API_KEY');
    expect(card.textContent).not.toContain('sekret');
    expect((within(card).getByLabelText('echo only reads') as HTMLInputElement).checked).toBe(false);
    expect(card.textContent).toContain('the server says it only reads');
    expect((within(card).getByLabelText('Pay may use Test server') as HTMLInputElement).checked).toBe(false);
    expect(card.textContent).toContain('No agent can use a server until you tick it here.');
  });

  it('grants and revokes an agent by sending the whole new set', async () => {
    const mcpServerAccessSet = vi.fn(async (_p: string, _s: string, agents: { agentId: string; tools: string[] }[]) => server({ agents }));
    show({ mcpServerList: async () => [server({ agents: [{ agentId: 'agt_2', tools: ['*'] }] })], mcpApprovalList: async () => [], mcpServerAccessSet });
    const card = await screen.findByTestId('mcp-server');
    fireEvent.click(within(card).getByLabelText('Pay may use Test server'));
    await waitFor(() => expect(mcpServerAccessSet).toHaveBeenCalledWith('prj_1', 'tst', [{ agentId: 'agt_2', tools: ['*'] }, { agentId: 'agt_1', tools: ['*'] }]));
    fireEvent.click(within(card).getByLabelText('Rev may use Test server'));
    await waitFor(() => expect(mcpServerAccessSet).toHaveBeenLastCalledWith('prj_1', 'tst', []));
  });

  it('marking a tool read-only saves the server with its secrets untouched', async () => {
    const mcpServerSave = vi.fn(async (_i: object) => server());
    show({ mcpServerList: async () => [server()], mcpApprovalList: async () => [], mcpServerSave });
    fireEvent.click(await screen.findByLabelText('echo only reads'));
    await waitFor(() => expect(mcpServerSave).toHaveBeenCalled());
    expect(mcpServerSave.mock.calls[0]![0]).toMatchObject({ id: 'tst', readOnlyTools: ['echo'], secretEnv: { API_KEY: '' } });
  });

  it('adds a stdio server: arguments one per line, env lines, a secret, then refreshes', async () => {
    const mcpServerSave = vi.fn(async (_i: object) => server());
    show({ mcpServerList: async () => [], mcpApprovalList: async () => [], mcpServerSave });
    fireEvent.click(await screen.findByRole('button', { name: 'Add an MCP server' }));
    const form = await screen.findByTestId('mcp-form');
    const f = within(form);
    fireEvent.change(f.getByLabelText(/Short id/), { target: { value: 'tst' } });
    fireEvent.change(f.getByLabelText('Name'), { target: { value: 'Test server' } });
    fireEvent.change(f.getByLabelText(/Command, one argument per line/), { target: { value: 'C:\\tools\\srv.exe\n--flag' } });
    fireEvent.change(f.getByLabelText(/Environment, NAME=value/), { target: { value: 'MODE=x' } });
    fireEvent.click(f.getByRole('button', { name: 'Add a secret' }));
    fireEvent.change(f.getByLabelText('Secret name 1'), { target: { value: 'API_KEY' } });
    fireEvent.change(f.getByLabelText('Secret value 1'), { target: { value: 'abc123' } });
    expect((f.getByLabelText('Secret value 1') as HTMLInputElement).type).toBe('password');
    fireEvent.click(f.getByRole('button', { name: 'Add server' }));
    await waitFor(() => expect(mcpServerSave).toHaveBeenCalled());
    expect(mcpServerSave.mock.calls[0]![0]).toMatchObject({
      projectId: 'prj_1', id: 'tst', display: 'Test server', transport: 'stdio', argv: ['C:\\tools\\srv.exe', '--flag'], env: { MODE: 'x' }, secretEnv: { API_KEY: 'abc123' }, guard: true,
    });
  });

  it('shows a bad environment line without calling the core, and the core refusal otherwise', async () => {
    const mcpServerSave = vi.fn(async () => {
      throw new Error('plain http is allowed only for loopback hosts');
    });
    show({ mcpServerList: async () => [], mcpApprovalList: async () => [], mcpServerSave });
    fireEvent.click(await screen.findByRole('button', { name: 'Add an MCP server' }));
    const f = within(await screen.findByTestId('mcp-form'));
    fireEvent.change(f.getByLabelText(/Short id/), { target: { value: 'web' } });
    fireEvent.change(f.getByLabelText('Name'), { target: { value: 'Remote' } });
    fireEvent.change(f.getByLabelText(/Command, one argument per line/), { target: { value: 'x' } });
    fireEvent.change(f.getByLabelText(/Environment, NAME=value/), { target: { value: 'BROKEN' } });
    fireEvent.click(f.getByRole('button', { name: 'Add server' }));
    expect((await screen.findByRole('alert')).textContent).toContain('"BROKEN" is not NAME=value');
    expect(mcpServerSave).not.toHaveBeenCalled();
    fireEvent.change(f.getByLabelText(/Environment, NAME=value/), { target: { value: '' } });
    fireEvent.change(f.getByLabelText('Kind'), { target: { value: 'http' } });
    fireEvent.change(f.getByLabelText(/Address/), { target: { value: 'http://example.com/mcp' } });
    fireEvent.click(f.getByRole('button', { name: 'Add server' }));
    expect((await screen.findByRole('alert')).textContent).toContain('plain http');
  });

  it('shows a call waiting for approval as inert text and sends the decision for exactly that approval', async () => {
    const ap: McpApproval = { id: 'apr_1', server: 'tst', tool: 'post_note', agentId: 'agt_1', args: '{"text":"<img src=x onerror=alert(1)>"}', created: 1760000000000, expiresAt: 1760001800000, projectId: 'prj_1' };
    const mcpApprovalDecide = vi.fn(async () => undefined);
    show({ mcpServerList: async () => [server()], mcpApprovalList: async () => [ap], mcpApprovalDecide });
    const box = await screen.findByTestId('mcp-approval');
    expect(box.textContent).toContain('Pay');
    expect(box.textContent).toContain('<img src=x onerror=alert(1)>');
    expect(box.querySelector('img')).toBeNull();
    fireEvent.click(within(box).getByRole('button', { name: 'Reject' }));
    await waitFor(() => expect(mcpApprovalDecide).toHaveBeenCalledWith('apr_1', false));
  });

  it('removes a server only after a confirmation', async () => {
    const mcpServerDelete = vi.fn(async () => undefined);
    show({ mcpServerList: async () => [server()], mcpApprovalList: async () => [], mcpServerDelete });
    const card = await screen.findByTestId('mcp-server');
    fireEvent.click(within(card).getByRole('button', { name: 'Remove' }));
    expect(mcpServerDelete).not.toHaveBeenCalled();
    fireEvent.click(within(card).getByRole('button', { name: 'Yes, remove' }));
    await waitFor(() => expect(mcpServerDelete).toHaveBeenCalledWith('prj_1', 'tst'));
  });
});
