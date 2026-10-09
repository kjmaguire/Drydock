import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { AgentTemplate } from '../api/libraryClient';
import type { McpLibraryServer, McpOAuthStatus } from '../api/mcpClient';
import type { AdapterInfo, Project } from '../api/types';
import { AppServicesProvider } from '../state/context';
import { LicenseProvider } from '../state/license';
import { services } from '../test/fakes';
import LibraryPage from './Library';
import { libServer, pay, projServer, rev } from './mcp.test';

const projects = [{ id: 'prj_1', name: 'Alpha' }, { id: 'prj_2', name: 'Beta' }] as Project[];
const tpl = (over: Partial<AgentTemplate> = {}): AgentTemplate => ({
  id: 'tpl_1', name: 'Payments dev', role: 'backend', purpose: 'Own the payments service.', adapter: 'claude', model: 'sonnet', effort: 'high', permissionMode: '', providerId: '', mcpServers: ['tst'], createdAt: 1, updatedAt: 1, ...over,
});
const claude = { id: 'claude', detection: { adapter: 'claude', installed: true, status: 'ready' }, capabilities: { modelSelection: true, effortControl: true, efforts: ['low', 'high'], models: ['sonnet'], permissionModes: ['acceptEdits', 'plan'] } } as unknown as AdapterInfo;

/** A DataTransfer that keeps what was set, as a browser does between dragstart and drop. */
const dataTransfer = () => {
  const d: Record<string, string> = {};
  return { setData: (k: string, v: string) => (d[k] = v), getData: (k: string) => d[k] ?? '', effectAllowed: '', dropEffect: '' };
};
/** Drag `from` and drop on `to`. */
const dragDrop = (from: HTMLElement, to: HTMLElement) => {
  const dt = dataTransfer();
  fireEvent.dragStart(from, { dataTransfer: dt });
  fireEvent.dragEnter(to, { dataTransfer: dt });
  fireEvent.dragOver(to, { dataTransfer: dt });
  fireEvent.drop(to, { dataTransfer: dt });
  fireEvent.dragEnd(from, { dataTransfer: dt });
};

const show = (client: Partial<Client> = {}) =>
  render(
    <AppServicesProvider
      services={services({
        licenseStatus: async () => ({ configured: false, devMode: true, state: 'none', readOnly: false, message: '' }) as never,
        adapterList: async () => [claude],
        mcpStatus: async () => ({ running: true, address: '127.0.0.1:1', adapters: [] }),
        mcpLibList: async () => [libServer({ projects: [] })],
        agentTemplateList: async () => [tpl()],
        mcpServerList: async (p: string) => (p === 'prj_1' ? [projServer({ agents: [{ agentId: 'agt_2', tools: ['*'] }] })] : []),
        agentList: async (p: string) => (p === 'prj_1' ? [pay, rev] : []),
        ...client,
      })}
    >
      <LicenseProvider>
        <LibraryPage projects={projects} />
      </LicenseProvider>
    </AppServicesProvider>,
  );

const alpha = async () => within(await screen.findByLabelText('Project Alpha'));

describe('Library page', () => {
  it('shows connected servers (secrets as names only), templates and the projects with their team', async () => {
    show();
    const card = await screen.findByTestId('lib-mcp');
    expect(card.textContent).toContain('API_KEY');
    expect(card.textContent).not.toContain('sekret');
    expect(card.textContent).toContain('Not in any project yet.');
    expect((await screen.findByTestId('lib-template')).textContent).toContain('Payments dev');
    const a = await alpha();
    expect(a.getByText('Rev')).toBeTruthy();
    expect(a.getByLabelText('Servers Rev may use').textContent).toContain('Test server');
  });

  it('dropping a server on a project puts it there', async () => {
    const mcpServerAdd = vi.fn(async () => projServer({ projectId: 'prj_2' }));
    show({ mcpServerAdd });
    const card = await screen.findByTestId('lib-mcp');
    const beta = (await screen.findAllByTestId('lib-project')).find((z) => z.textContent?.includes('Beta'))!;
    dragDrop(card, beta);
    await waitFor(() => expect(mcpServerAdd).toHaveBeenCalledWith('prj_2', 'tst'));
    expect((await screen.findByTestId('library-note')).textContent).toContain('Test server is now in Beta.');
  });

  it('dropping a server on an agent lets that agent use it', async () => {
    const mcpServerGrant = vi.fn(async () => projServer());
    show({ mcpServerGrant });
    const card = await screen.findByTestId('lib-mcp');
    const payZone = (await screen.findAllByTestId('lib-agent')).find((z) => z.textContent?.includes('Pay'))!;
    dragDrop(card, payZone);
    await waitFor(() => expect(mcpServerGrant).toHaveBeenCalledWith('prj_1', 'tst', 'agt_1', true));
    expect((await screen.findByTestId('library-note')).textContent).toContain('Pay can now use Test server.');
  });

  it('dropping a template on a project adds the team member, and the note says what was granted and what was not', async () => {
    const agentTemplateApply = vi.fn(async () => ({ agent: { ...pay, name: 'Payments dev' }, granted: ['tst'], skipped: ['docs: there is no MCP server "docs" in the library'] }));
    show({ agentTemplateApply });
    const card = await screen.findByTestId('lib-template');
    const beta = (await screen.findAllByTestId('lib-project')).find((z) => z.textContent?.includes('Beta'))!;
    dragDrop(card, beta);
    await waitFor(() => expect(agentTemplateApply).toHaveBeenCalledWith('tpl_1', 'prj_2'));
    const note = (await screen.findByTestId('library-note')).textContent ?? '';
    expect(note).toContain('Payments dev joined Beta');
    expect(note).toContain('Test server');
    expect(note).toContain('Not granted: docs');
  });

  it('ignores what does not belong (a foreign drop, a drop with nothing in it); a template dropped on an agent row lands in that agent\'s project, not on the agent', async () => {
    const mcpServerGrant = vi.fn(async () => projServer());
    const agentTemplateApply = vi.fn(async () => ({ agent: pay, granted: [], skipped: [] }));
    const mcpServerAdd = vi.fn();
    show({ mcpServerGrant, agentTemplateApply, mcpServerAdd });
    const template = await screen.findByTestId('lib-template');
    const payZone = (await screen.findAllByTestId('lib-agent')).find((z) => z.textContent?.includes('Pay'))!;
    dragDrop(template, payZone); // an agent takes servers, not team members: the drop falls through to the project panel around it
    const foreign = dataTransfer();
    foreign.setData('text/plain', 'https://example.com/x');
    const project = (await screen.findAllByTestId('lib-project'))[0]!;
    fireEvent.drop(project, { dataTransfer: foreign });
    fireEvent.drop(project, { dataTransfer: dataTransfer() });
    await new Promise((r) => setTimeout(r, 50));
    expect(mcpServerGrant).not.toHaveBeenCalled();
    expect(mcpServerAdd).not.toHaveBeenCalled();
    expect(agentTemplateApply).toHaveBeenCalledTimes(1);
    expect(agentTemplateApply).toHaveBeenCalledWith('tpl_1', 'prj_1');
  });

  it('works without dragging: a project select and a button for servers and templates, and a Give button on an agent', async () => {
    const mcpServerAdd = vi.fn(async () => projServer());
    const agentTemplateApply = vi.fn(async () => ({ agent: pay, granted: [], skipped: [] }));
    const mcpServerGrant = vi.fn(async () => projServer());
    show({ mcpServerAdd, agentTemplateApply, mcpServerGrant });
    const card = await screen.findByTestId('lib-mcp');
    fireEvent.change(within(card).getByLabelText('Put Test server in project'), { target: { value: 'prj_2' } });
    fireEvent.click(within(card).getByRole('button', { name: 'Put in project' }));
    await waitFor(() => expect(mcpServerAdd).toHaveBeenCalledWith('prj_2', 'tst'));
    const template = await screen.findByTestId('lib-template');
    fireEvent.click(within(template).getByRole('button', { name: 'Put in project' }));
    await waitFor(() => expect(agentTemplateApply).toHaveBeenCalledWith('tpl_1', 'prj_1'));
    fireEvent.click(await screen.findByRole('button', { name: 'Give to Pay' }));
    await waitFor(() => expect(mcpServerGrant).toHaveBeenCalledWith('prj_1', 'tst', 'agt_1', true));
  });

  it('removes a server from an agent or from a project with the x on its chip', async () => {
    const mcpServerGrant = vi.fn(async () => projServer());
    const mcpServerRemove = vi.fn(async () => undefined);
    show({ mcpServerGrant, mcpServerRemove });
    fireEvent.click(await screen.findByLabelText('Stop Rev using Test server'));
    await waitFor(() => expect(mcpServerGrant).toHaveBeenCalledWith('prj_1', 'tst', 'agt_2', false));
    fireEvent.click(await screen.findByLabelText('Take Test server out of Alpha'));
    await waitFor(() => expect(mcpServerRemove).toHaveBeenCalledWith('prj_1', 'tst'));
  });

  it('marks a tool read-only without sending or losing the stored secrets', async () => {
    const mcpLibSave = vi.fn(async () => libServer());
    show({ mcpLibSave });
    fireEvent.click(await screen.findByLabelText('echo only reads'));
    await waitFor(() => expect(mcpLibSave).toHaveBeenCalled());
    expect((mcpLibSave.mock.calls[0] as unknown[])[0]).toMatchObject({ id: 'tst', readOnlyTools: ['echo'], secretEnv: { API_KEY: '' } });
  });

  it('connects a server once: arguments one per line, a secret field that is a password', async () => {
    const mcpLibSave = vi.fn(async () => libServer());
    show({ mcpLibList: async () => [], mcpLibSave });
    fireEvent.click(await screen.findByRole('button', { name: 'Connect an MCP server' }));
    const f = within(await screen.findByTestId('mcp-form'));
    fireEvent.change(f.getByLabelText(/Short id/), { target: { value: 'tst' } });
    fireEvent.change(f.getByLabelText('Name'), { target: { value: 'Test server' } });
    fireEvent.change(f.getByLabelText(/Command, one argument per line/), { target: { value: 'C:\\tools\\srv.exe\n--flag' } });
    fireEvent.click(f.getByRole('button', { name: 'Add a secret' }));
    fireEvent.change(f.getByLabelText('Secret name 1'), { target: { value: 'API_KEY' } });
    fireEvent.change(f.getByLabelText('Secret value 1'), { target: { value: 'abc123' } });
    expect((f.getByLabelText('Secret value 1') as HTMLInputElement).type).toBe('password');
    fireEvent.click(f.getByRole('button', { name: 'Connect server' }));
    await waitFor(() => expect(mcpLibSave).toHaveBeenCalled());
    expect((mcpLibSave.mock.calls[0] as unknown[])[0]).toMatchObject({ id: 'tst', display: 'Test server', transport: 'stdio', argv: ['C:\\tools\\srv.exe', '--flag'], secretEnv: { API_KEY: 'abc123' }, guard: true });
    expect((await screen.findByTestId('library-note')).textContent).toContain('is connected');
  });

  it('disconnecting needs a confirmation that says what it removes', async () => {
    const mcpLibDelete = vi.fn(async () => undefined);
    show({ mcpLibList: async () => [libServer({ projects: ['prj_1', 'prj_2'] })], mcpLibDelete });
    const card = await screen.findByTestId('lib-mcp');
    fireEvent.click(within(card).getByRole('button', { name: 'Disconnect' }));
    expect(mcpLibDelete).not.toHaveBeenCalled();
    expect(card.textContent).toContain('leaves 2 project(s)');
    fireEvent.click(within(card).getByRole('button', { name: 'Yes, disconnect' }));
    await waitFor(() => expect(mcpLibDelete).toHaveBeenCalledWith('tst'));
  });

  it('creates a template with the servers it should get', async () => {
    const agentTemplateSave = vi.fn(async (t: object) => ({ ...tpl(), ...t }) as AgentTemplate);
    show({ agentTemplateList: async () => [], agentTemplateSave });
    fireEvent.click(await screen.findByRole('button', { name: 'New agent template' }));
    const f = within(await screen.findByTestId('template-form'));
    fireEvent.change(f.getByLabelText('Template name'), { target: { value: 'Payments dev' } });
    fireEvent.change(f.getByLabelText('Role'), { target: { value: 'backend' } });
    fireEvent.change(f.getByLabelText(/Purpose/), { target: { value: 'Own payments.' } });
    fireEvent.change(f.getByLabelText('Effort'), { target: { value: 'high' } });
    fireEvent.click(f.getByLabelText('Template gets Test server'));
    fireEvent.click(f.getByRole('button', { name: 'Create template' }));
    await waitFor(() => expect(agentTemplateSave).toHaveBeenCalled());
    expect((agentTemplateSave.mock.calls[0] as unknown[])[0]).toMatchObject({ name: 'Payments dev', role: 'backend', purpose: 'Own payments.', adapter: 'claude', effort: 'high', mcpServers: ['tst'] });
  });

  it('agent CLIs: Claude is verified, an unproven CLI is off until the human allows it, one with no mechanism cannot be allowed', async () => {
    const mcpTrySet = vi.fn(async (_adapter: string, enabled: boolean) => ({
      running: true,
      address: '',
      adapters: [
        { adapter: 'claude', attached: true, canTry: false, trying: false },
        { adapter: 'codex', attached: enabled, canTry: true, trying: enabled, reason: 'codex: unverified' },
        { adapter: 'antigravity', attached: false, canTry: false, trying: false, reason: 'antigravity: unproven' },
      ],
    }));
    show({
      mcpStatus: async () => ({
        running: true,
        address: '',
        adapters: [
          { adapter: 'claude', attached: true, canTry: false, trying: false },
          { adapter: 'codex', attached: false, canTry: true, trying: false, reason: 'codex: unverified' },
          { adapter: 'antigravity', attached: false, canTry: false, trying: false, reason: 'antigravity: unproven' },
        ],
      }),
      mcpTrySet,
    });
    const list = await screen.findByTestId('cli-gateway');
    await waitFor(() => expect(within(list).getByText('verified')).toBeTruthy());
    expect(within(list).queryByLabelText('Let claude use the gateway')).toBeNull();
    expect(within(list).queryByLabelText('Let antigravity use the gateway')).toBeNull();
    expect(list.textContent).toContain('antigravity: unproven');
    const box = within(list).getByLabelText('Let codex use the gateway') as HTMLInputElement;
    expect(box.checked).toBe(false);
    fireEvent.click(box);
    await waitFor(() => expect(mcpTrySet).toHaveBeenCalledWith('codex', true));
  });

  describe('OAuth sign-in of a web server', () => {
    const oauthServer = (over: Partial<McpLibraryServer> = {}) =>
      libServer({ id: 'lin', display: 'Linear', transport: 'http', url: 'https://mcp.example.com/mcp', argv: undefined, env: undefined, secretEnv: [], oauth: true, oauthAt: 0, tools: [], projects: [], ...over });
    const status = (over: Partial<McpOAuthStatus> = {}): McpOAuthStatus => ({ id: 'lin', state: 'idle', signedIn: false, ...over });

    it('connecting a server with OAuth sends oauth true and no token', async () => {
      const mcpLibSave = vi.fn(async () => oauthServer());
      show({ mcpLibList: async () => [], mcpLibSave });
      fireEvent.click(await screen.findByRole('button', { name: 'Connect an MCP server' }));
      const f = within(await screen.findByTestId('mcp-form'));
      fireEvent.change(f.getByLabelText(/Short id/), { target: { value: 'lin' } });
      fireEvent.change(f.getByLabelText('Name'), { target: { value: 'Linear' } });
      fireEvent.change(f.getByLabelText('Kind'), { target: { value: 'http' } });
      fireEvent.change(f.getByLabelText(/Address/), { target: { value: 'https://mcp.example.com/mcp' } });
      fireEvent.change(f.getByLabelText('Sign-in'), { target: { value: 'oauth' } });
      expect(f.queryByLabelText(/Bearer token/)).toBeNull();
      fireEvent.click(f.getByRole('button', { name: 'Connect server' }));
      await waitFor(() => expect(mcpLibSave).toHaveBeenCalled());
      const sent = (mcpLibSave.mock.calls[0] as unknown[])[0] as Record<string, unknown>;
      expect(sent).toMatchObject({ id: 'lin', transport: 'http', url: 'https://mcp.example.com/mcp', oauth: true });
      expect(sent.bearerToken).toBeUndefined();
    });

    it('not signed in: Sign in asks the core to begin, then links the server\'s own sign-in page and can cancel', async () => {
      const mcpOAuthBegin = vi.fn(async (_id?: string) => status({ state: 'waiting', url: 'https://auth.example.com/authorize?client_id=c1&state=s' }));
      const mcpOAuthCancel = vi.fn(async () => status());
      let waiting = false;
      show({
        mcpLibList: async () => [oauthServer()],
        mcpOAuthStatus: async () => (waiting ? status({ state: 'waiting', url: 'https://auth.example.com/authorize?client_id=c1&state=s' }) : status()),
        mcpOAuthBegin: async (id: string) => {
          waiting = true;
          return mcpOAuthBegin(id);
        },
        mcpOAuthCancel,
      });
      const panel = within(await screen.findByTestId('oauth-panel'));
      expect(panel.getByText('not signed in')).toBeTruthy();
      expect(panel.queryByRole('button', { name: 'Sign out' })).toBeNull();
      fireEvent.click(panel.getByRole('button', { name: 'Sign in' }));
      await waitFor(() => expect(mcpOAuthBegin).toHaveBeenCalled());
      const link = (await screen.findByTestId('oauth-link')) as HTMLAnchorElement;
      expect(link.href).toBe('https://auth.example.com/authorize?client_id=c1&state=s');
      expect(link.target).toBe('_blank');
      expect(link.rel).toContain('noopener');
      fireEvent.click(within(await screen.findByTestId('oauth-waiting')).getByRole('button', { name: 'Cancel' }));
      await waitFor(() => expect(mcpOAuthCancel).toHaveBeenCalledWith('lin'));
    });

    it('never links an address that is not safe to open', async () => {
      show({ mcpLibList: async () => [oauthServer()], mcpOAuthStatus: async () => status({ state: 'waiting', url: 'javascript:alert(1)' }) });
      const waiting = await screen.findByTestId('oauth-waiting');
      expect(within(waiting).queryByTestId('oauth-link')).toBeNull();
      expect(waiting.textContent).toContain('not safe to open');
    });

    it('signed in: says so and signs out; a failed sign-in says why', async () => {
      const mcpOAuthSignOut = vi.fn(async () => status());
      show({ mcpLibList: async () => [oauthServer({ oauthAt: 1760000000000 })], mcpOAuthStatus: async () => status({ signedIn: true, at: 1760000000000 }), mcpOAuthSignOut });
      const panel = within(await screen.findByTestId('oauth-panel'));
      await waitFor(() => expect(panel.getByText(/signed in/)).toBeTruthy());
      expect(panel.getByRole('button', { name: 'Sign in again' })).toBeTruthy();
      fireEvent.click(panel.getByRole('button', { name: 'Sign out' }));
      await waitFor(() => expect(mcpOAuthSignOut).toHaveBeenCalledWith('lin'));
    });

    it('shows the reason when the sign-in did not finish', async () => {
      show({ mcpLibList: async () => [oauthServer()], mcpOAuthStatus: async () => status({ state: 'failed', error: 'the provider refused the sign-in: access_denied' }) });
      expect((await screen.findByTestId('oauth-error')).textContent).toContain('access_denied');
    });
  });
});
