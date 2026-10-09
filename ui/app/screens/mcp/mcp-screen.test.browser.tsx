import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { McpApproval } from '../../api/mcpClient.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { devLicense, libServer, pay, projServer, rev } from './fixtures.ts';
import { McpScreen } from './mcp-screen.tsx';

afterEach(cleanup);

type Calls = unknown[][];
function rec<R>(calls: Calls, result: (...args: unknown[]) => R) {
  return async (...args: unknown[]) => {
    calls.push(args);
    return result(...args);
  };
}

function show(client: Partial<Client>, opts: { projects?: { id: string; name: string }[] } = {}) {
  const s = testServices({
    licenseStatus: async () => devLicense,
    appInfo: async () => ({}) as never,
    projectList: async () => (opts.projects ?? [{ id: 'prj_1', name: 'gocalc' }]) as never,
    agentList: async () => [pay, rev],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    presenceList: async () => [],
    taskList: async () => [],
    mcpApprovalList: async () => [],
    mcpLibList: async () => [libServer()],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return mount(<McpScreen />, s);
}
type R = ReturnType<typeof show>;
const server = (r: R) =>
  waitFor(() => {
    const c = r.$('[data-testid="mcp-server"]');
    if (!c?.querySelector('[aria-label="Pay may use Test server"]')) throw new Error('not yet');
    return c as HTMLElement;
  }, r);

describe('MCP servers page (one project)', () => {
  it('lists the project servers, which tools only read, and no secret', async () => {
    const r = show({ mcpServerList: async () => [projServer({ readOnlyTools: ['echo'] })] });
    const card = await server(r);
    const tools = Array.from(card.querySelectorAll('[data-testid="mcp-tool"]'));
    expect(tools.map((x) => x.textContent)).toEqual(['echo reads only', 'post_note asks you first']);
    expect(card.textContent).not.toContain('sekret');
    expect(byLabel<HTMLInputElement>(card, 'Pay may use Test server').checked).toBe(false);
    expect(card.textContent).toContain('No agent can use a server until you tick it here.');
    expect(r.$('h1')!.textContent).toContain('MCP servers');
  });

  it('grants and revokes one agent without touching the others', async () => {
    const grants: Calls = [];
    const r = show({ mcpServerList: async () => [projServer({ agents: [{ agentId: 'agt_2', tools: ['*'] }] })], mcpServerGrant: rec(grants, () => projServer()) as never });
    const card = await server(r);
    expect(byLabel<HTMLInputElement>(card, 'Rev may use Test server').checked).toBe(true);
    await r.act(() => byLabel(card, 'Pay may use Test server').click());
    await waitFor(() => expect(grants).toEqual([['prj_1', 'tst', 'agt_1', true]]), r);
    await waitFor(() => expect(byLabel<HTMLInputElement>(card, 'Rev may use Test server').disabled).toBe(false), r);
    await r.act(() => byLabel(card, 'Rev may use Test server').click());
    await waitFor(() => expect(grants[1]).toEqual(['prj_1', 'tst', 'agt_2', false]), r);
  });

  it('adds a connected server from the library and says where to connect new ones', async () => {
    const adds: Calls = [];
    const r = show({
      mcpServerList: async () => [],
      mcpLibList: async () => [libServer({ projects: [] }), libServer({ id: 'other', display: 'Other', projects: [] })],
      mcpServerAdd: rec(adds, () => projServer()) as never,
    });
    const sel = await waitFor(() => byLabel<HTMLSelectElement>(r.container, 'Server to add'), r);
    await r.act(() => type(sel, 'other'));
    await r.act(() => byText(r.container, 'button', 'Put into this project').click());
    await waitFor(() => expect(adds).toEqual([['prj_1', 'other']]), r);
    expect(r.container.textContent).toContain('No MCP servers in this project yet.');
  });

  it('points to the Library when nothing is connected', async () => {
    const r = show({ mcpServerList: async () => [], mcpLibList: async () => [] });
    const p = await waitFor(() => {
      const el = Array.from(r.$$('p')).find((x) => x.textContent?.includes('No servers are connected yet.'));
      if (!el) throw new Error('not yet');
      return el;
    }, r);
    expect(p.textContent).toContain('Library');
    expect(p.querySelector('a')!.getAttribute('href')).toBe('/library');
  });

  it('takes a server out of the project only after a confirmation, and keeps it in the library', async () => {
    const removes: Calls = [];
    const r = show({ mcpServerList: async () => [projServer()], mcpServerRemove: rec(removes, () => undefined) as never });
    const card = await server(r);
    await r.act(() => byText(card, 'button', 'Take out of this project').click());
    expect(removes).toEqual([]);
    expect(card.textContent).toContain('it stays in the Library');
    await r.act(() => byText(card, 'button', 'Yes, take it out').click());
    await waitFor(() => expect(removes).toEqual([['prj_1', 'tst']]), r);
  });

  it('turns a server off for this project only', async () => {
    const sets: Calls = [];
    const r = show({ mcpServerList: async () => [projServer()], mcpServerSetEnabled: rec(sets, () => projServer({ enabled: false })) as never });
    const card = await server(r);
    await r.act(() => byText(card, 'button', 'Turn off in this project').click());
    await waitFor(() => expect(sets).toEqual([['prj_1', 'tst', false]]), r);
  });

  it('shows a call waiting for approval as inert text and sends the decision for exactly that approval', async () => {
    const ap: McpApproval = { id: 'apr_1', server: 'tst', tool: 'post_note', agentId: 'agt_1', args: '{"text":"<img src=x onerror=alert(1)>"}', created: 1760000000000, expiresAt: 1760001800000, projectId: 'prj_1' };
    const decisions: Calls = [];
    const r = show({ mcpServerList: async () => [projServer()], mcpApprovalList: async () => [ap], mcpApprovalDecide: rec(decisions, () => undefined) as never });
    const box = await waitFor(() => {
      const b = r.$('[data-testid="mcp-approval"]');
      if (!b?.textContent?.includes('Pay')) throw new Error('not yet');
      return b as HTMLElement;
    }, r);
    expect(box.textContent).toContain('<img src=x onerror=alert(1)>');
    expect(box.querySelector('img')).toBeNull();
    await r.act(() => byText(box, 'button', 'Reject').click());
    await waitFor(() => expect(decisions).toEqual([['apr_1', false]]), r);
  });

  it('read-only: approving and turning on or off are disabled, rejecting is not', async () => {
    const ap: McpApproval = { id: 'apr_1', server: 'tst', tool: 'post_note', agentId: 'agt_1', args: '{}', created: 1, expiresAt: 2, projectId: 'prj_1' };
    const r = show({ licenseStatus: async () => expiredLicense, mcpServerList: async () => [projServer()], mcpApprovalList: async () => [ap] });
    const box = await waitFor(() => {
      const b = r.$('[data-testid="mcp-approval"]');
      if (!b) throw new Error('not yet');
      return b as HTMLElement;
    }, r);
    await waitFor(() => expect(byText<HTMLButtonElement>(box, 'button', 'Approve this call').disabled).toBe(true), r);
    expect(byText<HTMLButtonElement>(box, 'button', 'Reject').disabled).toBe(false);
    const card = await server(r);
    expect(byText<HTMLButtonElement>(card, 'button', 'Turn off in this project').disabled).toBe(true);
  });

  it('re-reads the servers when an MCP event arrives', async () => {
    let list = [projServer()];
    const r = show({ mcpServerList: async () => list });
    await server(r);
    list = [projServer(), projServer({ id: 'two', display: 'Second server' })];
    r.services.store.ingest({ seq: 5000, time: '', type: 'mcp.server_added' } as never);
    await waitFor(() => expect(r.$$('[data-testid="mcp-server"]').length).toBe(2), r);
  });

  it('asks for a project when none exists', async () => {
    const r = show({ mcpServerList: async () => [] }, { projects: [] });
    await waitFor(() => expect(r.container.textContent).toContain('No project selected'), r);
    expect(r.$('a[href="/project/new"]')).not.toBeNull();
  });
});
