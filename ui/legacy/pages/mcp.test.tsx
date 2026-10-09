import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { McpApproval, McpLibraryServer, McpServer } from '../api/mcpClient';
import type { Agent } from '../api/types';
import { inputOf, parseEnv, parseLines } from '../components/McpServerForm';
import { AppServicesProvider } from '../state/context';
import { LicenseProvider } from '../state/license';
import { services } from '../test/fakes';
import McpServersView from './McpServers';

export const agent = (id: string, name: string, role: string): Agent => ({ id, projectId: 'prj_1', name, adapter: 'claude', role, model: '', effort: '', providerId: '', permissionMode: 'acceptEdits', status: 'STOPPED' });
export const pay = agent('agt_1', 'Pay', 'backend');
export const rev = agent('agt_2', 'Rev', 'reviewer');

export const libServer = (over: Partial<McpLibraryServer> = {}): McpLibraryServer => ({
  id: 'tst', display: 'Test server', transport: 'stdio', argv: ['C:\\tools\\srv.exe', '--flag'], env: { MODE: 'x' }, secretEnv: ['API_KEY'], hasToken: false, guard: true,
  readOnlyTools: [], tools: [{ name: 'echo', description: 'echo text', readOnlyHint: true }, { name: 'post_note', readOnlyHint: false }], discoveredAt: 1760000000000, projects: ['prj_1'], createdAt: 1, updatedAt: 1, ...over,
});
export const projServer = (over: Partial<McpServer> = {}): McpServer => ({ ...libServer(), projectId: 'prj_1', enabled: true, agents: [], ...over });

const show = (client: Partial<Client>) =>
  render(
    <AppServicesProvider services={services({ licenseStatus: async () => ({ configured: false, devMode: true, state: 'none', readOnly: false, message: '' }) as never, ...client })}>
      <LicenseProvider>
        <McpServersView projectId="prj_1" agents={[pay, rev]} />
      </LicenseProvider>
    </AppServicesProvider>,
  );

describe('MCP server form helpers', () => {
  it('reads one argument per line and NAME=value lines, and says which line is wrong', () => {
    expect(parseLines('npx\r\n-y\n\n  @x/y@1.0.0 \n')).toEqual(['npx', '-y', '  @x/y@1.0.0 ']);
    expect(parseEnv('A=1\nB=two=three', 'Environment')).toEqual({ A: '1', B: 'two=three' });
    expect(() => parseEnv('NOEQUALS', 'Environment')).toThrow('Environment: "NOEQUALS" is not NAME=value');
  });
  it('rebuilds a save input that keeps the stored secrets without sending them', () => {
    const i = inputOf(libServer());
    expect(i.secretEnv).toEqual({ API_KEY: '' });
    expect(i.bearerToken).toBeUndefined();
  });
});

describe('MCP servers page (one project)', () => {
  it('lists the project servers, which tools only read, and no secret', async () => {
    show({ mcpServerList: async () => [projServer({ readOnlyTools: ['echo'] })], mcpApprovalList: async () => [], mcpLibList: async () => [libServer()] });
    const card = await screen.findByTestId('mcp-server');
    expect(within(card).getByText('reads only')).toBeTruthy();
    expect(within(card).getByText('asks you first')).toBeTruthy();
    expect(card.textContent).not.toContain('sekret');
    expect((within(card).getByLabelText('Pay may use Test server') as HTMLInputElement).checked).toBe(false);
    expect(card.textContent).toContain('No agent can use a server until you tick it here.');
  });

  it('grants and revokes one agent without touching the others', async () => {
    const mcpServerGrant = vi.fn(async () => projServer());
    show({ mcpServerList: async () => [projServer({ agents: [{ agentId: 'agt_2', tools: ['*'] }] })], mcpApprovalList: async () => [], mcpLibList: async () => [libServer()], mcpServerGrant });
    const card = await screen.findByTestId('mcp-server');
    fireEvent.click(within(card).getByLabelText('Pay may use Test server'));
    await waitFor(() => expect(mcpServerGrant).toHaveBeenCalledWith('prj_1', 'tst', 'agt_1', true));
    fireEvent.click(within(card).getByLabelText('Rev may use Test server'));
    await waitFor(() => expect(mcpServerGrant).toHaveBeenLastCalledWith('prj_1', 'tst', 'agt_2', false));
  });

  it('adds a connected server from the library and says where to connect new ones', async () => {
    const mcpServerAdd = vi.fn(async () => projServer());
    show({
      mcpServerList: async () => [],
      mcpApprovalList: async () => [],
      mcpLibList: async () => [libServer({ projects: [] }), libServer({ id: 'other', display: 'Other', projects: [] })],
      mcpServerAdd,
    });
    fireEvent.change(await screen.findByLabelText('Server to add'), { target: { value: 'other' } });
    fireEvent.click(screen.getByRole('button', { name: 'Put into this project' }));
    await waitFor(() => expect(mcpServerAdd).toHaveBeenCalledWith('prj_1', 'other'));
    expect(screen.getByText('No MCP servers in this project yet.')).toBeTruthy();
  });

  it('points to the Library when nothing is connected', async () => {
    show({ mcpServerList: async () => [], mcpApprovalList: async () => [], mcpLibList: async () => [] });
    expect((await screen.findByText(/No servers are connected yet/)).textContent).toContain('Library');
  });

  it('takes a server out of the project only after a confirmation, and keeps it in the library', async () => {
    const mcpServerRemove = vi.fn(async () => undefined);
    show({ mcpServerList: async () => [projServer()], mcpApprovalList: async () => [], mcpLibList: async () => [libServer()], mcpServerRemove });
    const card = await screen.findByTestId('mcp-server');
    fireEvent.click(within(card).getByRole('button', { name: 'Take out of this project' }));
    expect(mcpServerRemove).not.toHaveBeenCalled();
    expect(card.textContent).toContain('it stays in the Library');
    fireEvent.click(within(card).getByRole('button', { name: 'Yes, take it out' }));
    await waitFor(() => expect(mcpServerRemove).toHaveBeenCalledWith('prj_1', 'tst'));
  });

  it('turns a server off for this project only', async () => {
    const mcpServerSetEnabled = vi.fn(async () => projServer({ enabled: false }));
    show({ mcpServerList: async () => [projServer()], mcpApprovalList: async () => [], mcpLibList: async () => [libServer()], mcpServerSetEnabled });
    fireEvent.click(await screen.findByRole('button', { name: 'Turn off in this project' }));
    await waitFor(() => expect(mcpServerSetEnabled).toHaveBeenCalledWith('prj_1', 'tst', false));
  });

  it('shows a call waiting for approval as inert text and sends the decision for exactly that approval', async () => {
    const ap: McpApproval = { id: 'apr_1', server: 'tst', tool: 'post_note', agentId: 'agt_1', args: '{"text":"<img src=x onerror=alert(1)>"}', created: 1760000000000, expiresAt: 1760001800000, projectId: 'prj_1' };
    const mcpApprovalDecide = vi.fn(async () => undefined);
    show({ mcpServerList: async () => [projServer()], mcpApprovalList: async () => [ap], mcpLibList: async () => [libServer()], mcpApprovalDecide });
    const box = await screen.findByTestId('mcp-approval');
    expect(box.textContent).toContain('Pay');
    expect(box.textContent).toContain('<img src=x onerror=alert(1)>');
    expect(box.querySelector('img')).toBeNull();
    fireEvent.click(within(box).getByRole('button', { name: 'Reject' }));
    await waitFor(() => expect(mcpApprovalDecide).toHaveBeenCalledWith('apr_1', false));
  });
});
