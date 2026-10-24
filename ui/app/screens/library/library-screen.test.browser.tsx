import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { claude, devLicense, libServer, pay, projects, projServer, rev, tpl } from '../mcp/fixtures.ts';
import { CATALOG } from '../project/wizard-fixtures.ts';
import { LibraryScreen } from './library-screen.tsx';

afterEach(cleanup);

type Calls = unknown[][];
/** A recording stand-in for one client method (a labelled test double). */
function rec<R>(calls: Calls, result: (...args: unknown[]) => R) {
  return async (...args: unknown[]) => {
    calls.push(args);
    return result(...args);
  };
}

function show(client: Partial<Client> = {}) {
  const s = testServices({
    licenseStatus: async () => devLicense,
    appInfo: async () => ({}) as never,
    projectList: async () => projects,
    adapterList: async () => [claude],
    mcpStatus: async () => ({ running: true, address: '127.0.0.1:1', adapters: [] }),
    mcpLibList: async () => [libServer({ projects: [] })],
    agentTemplateList: async () => [tpl()],
    mcpServerList: async (p: string) => (p === 'prj_1' ? [projServer({ agents: [{ agentId: 'agt_2', tools: ['*'] }] })] : []),
    agentList: async (p: string) => (p === 'prj_1' ? [pay, rev] : []),
    mcpGallery: async () => [],
    skillRegistries: async () => [],
    skillRegistryStatus: async () => [],
    skillLibraryList: async () => [],
    skillList: async () => [],
    ...client,
  });
  s.workspace.start();
  const r = mount(<LibraryScreen />, s);
  return r;
}
type R = ReturnType<typeof show>;

const one = (r: R, sel: string) => waitFor(() => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
}, r);
const note = (r: R) => r.$('[data-testid="library-note"]')!.textContent ?? '';
/** Click the first template tile and return the panel that opens below the grid. */
async function pick(r: R, kind: 'lib-template'): Promise<HTMLElement> {
  const tile = await one(r, `[data-testid="${kind}-tile"]`);
  await r.act(() => tile.click());
  return one(r, `[data-testid="${kind}"]`);
}
describe('Library page', () => {
  it('shows the templates as tiles: what each is and its state, and no panel until one is clicked; the MCP servers are in Integrations', async () => {
    const r = show({
      mcpLibList: async () => [libServer({ projects: [] }), libServer({ id: 'web', display: 'Web thing', transport: 'http', projects: ['prj_1', 'prj_2'] })],
      agentTemplateList: async () => [tpl(), tpl({ id: 'tpl_2', name: 'Lonely', role: '', adapter: 'codex', mcpServers: ['gone'] })],
    });

    const tplTiles = await waitFor(() => {
      const l = r.$$('ul[aria-label="Agent templates"] [data-testid="lib-template-tile"]');
      if (l.length !== 2) throw new Error('not yet');
      return Array.from(l) as HTMLElement[];
    }, r);
    expect(tplTiles[0]!.textContent).toBe('Payments devbackend, on claudeGets 1 server');
    expect(tplTiles[0]!.querySelector('[data-brand="claude"]')).not.toBeNull();
    expect(tplTiles[1]!.textContent).toBe('LonelyTeam member on codex1 server not connected');
    expect(tplTiles[1]!.querySelector('small[data-tone="warn"]')).not.toBeNull();

    // nothing is open until a tile is clicked
    expect(r.$('[data-testid="lib-template"]')).toBeNull();
    // the servers you add yourself moved to Integrations (decision P98): no section, no tile, no connect button here
    expect(r.$('ul[aria-label="MCP servers"]')).toBeNull();
    expect(r.$('[data-testid="lib-mcp-tile"]')).toBeNull();
    expect(r.container.textContent).not.toContain('Web thing');
    expect(() => byText(r.container, 'button', 'Connect an MCP server')).toThrow();
    expect(r.$('[data-testid="library-servers-link"]')!.getAttribute('href')).toBe('/integrations');
    expect(r.$('header')!.textContent).toContain('2 templates');
  });

  it('clicking a template tile opens its panel below the grid, another tile switches it, the same tile closes it', async () => {
    const r = show({ agentTemplateList: async () => [tpl(), tpl({ id: 'tpl_2', name: 'Second' })] });
    const tiles = await waitFor(() => {
      const l = r.$$('[data-testid="lib-template-tile"]');
      if (l.length !== 2) throw new Error('not yet');
      return Array.from(l) as HTMLElement[];
    }, r);
    await r.act(() => tiles[0]!.click());
    const panel = await one(r, '[data-testid="lib-template"]');
    expect(panel.getAttribute('data-template')).toBe('Payments dev');
    expect(tiles[0]!.getAttribute('aria-pressed')).toBe('true');
    const grid = r.$('ul[aria-label="Agent templates"]')!;
    expect(grid.compareDocumentPosition(panel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    await r.act(() => tiles[1]!.click());
    await waitFor(() => expect(r.$('[data-testid="lib-template"]')!.getAttribute('data-template')).toBe('Second'), r);
    expect(r.$$('[data-testid="lib-template"]').length).toBe(1);
    await r.act(() => tiles[1]!.click());
    await waitFor(() => expect(r.$('[data-testid="lib-template"]')).toBeNull(), r);
  });

  it('marks a template whose permission mode skips approvals with the red badge on its tile and in its panel, and no other template (M1)', async () => {
    const r = show({ agentTemplateList: async () => [tpl({ permissionMode: 'bypassPermissions' }), tpl({ id: 'tpl_2', name: 'Careful', permissionMode: 'plan' })] });
    const tiles = await waitFor(() => {
      const l = r.$$('ul[aria-label="Agent templates"] [data-testid="lib-template-tile"]');
      if (l.length !== 2) throw new Error('not yet');
      return Array.from(l) as HTMLElement[];
    }, r);
    const badge = tiles[0]!.querySelector('[data-testid="tile-badge"]')!;
    expect(badge.textContent).toBe('skips approvals');
    expect(badge.getAttribute('data-tone')).toBe('err');
    expect(tiles[1]!.querySelector('[data-testid="tile-badge"]')).toBeNull();
    await r.act(() => tiles[0]!.click());
    const panel = await one(r, '[data-testid="lib-template"]');
    expect(panel.querySelector('[data-testid="skips-approvals-badge"]')!.textContent).toBe('skips approvals');
    await r.act(() => tiles[1]!.click());
    await waitFor(() => expect(r.$('[data-testid="lib-template"]')!.getAttribute('data-template')).toBe('Careful'), r);
    expect(r.$('[data-testid="lib-template"] [data-testid="skips-approvals-badge"]')).toBeNull();
  });

  it('says so in one line when there are no templates', async () => {
    const r = show({ mcpLibList: async () => [], agentTemplateList: async () => [] });
    expect((await one(r, '[data-testid="lib-template-empty"]')).textContent).toContain('No templates yet.');
    expect(r.$('ul[aria-label="Agent templates"]')).toBeNull();
    expect(r.$('[data-testid="lib-mcp-empty"]')).toBeNull();
  });

  it("shows a template's panel and says where a project gets one", async () => {
    const r = show();
    const tp = await pick(r, 'lib-template');
    expect(tp.textContent).toContain('Payments dev');
    expect(tp.querySelector('[data-testid="lib-template-where"] a')!.getAttribute('href')).toBe('/team');
    // the server it gives, by its name
    expect(tp.querySelector('ul[aria-label="MCP servers it gets"]')!.textContent).toContain('Test server');
    expect(r.$('h1')!.textContent).toContain('Library');
  });

  it('carries nothing of a single project: no project names, no agents, no put-in-project controls', async () => {
    const reads: Calls = [];
    const r = show({
      mcpLibList: async () => [libServer({ projects: ['prj_1', 'prj_2'] })],
      mcpServerList: (async (p: string) => (reads.push(['mcpServerList', p]), [projServer()])) as never,
      skillList: (async (p: string) => (reads.push(['skillList', p]), [])) as never,
    });
    // wait until the workspace knows its projects, so a project column would have rendered by now
    await waitFor(() => expect(r.services.workspace.projectList.length).toBe(2), r);
    const tp = await pick(r, 'lib-template');
    const text = r.container.textContent ?? '';
    // neither the projects nor their agents (Pay and Rev in Alpha) are named
    for (const name of ['Alpha', 'Beta', 'Rev']) expect([name, text.includes(name)]).toEqual([name, false]);
    expect(r.$('[data-agent]')).toBeNull();
    // no project's servers or skills are even read
    expect(reads).toEqual([]);
    expect(r.$('[data-testid="lib-project"]')).toBeNull();
    expect(r.$('[data-testid="lib-agent"]')).toBeNull();
    expect(r.$$('select').length).toBe(0);
    expect(() => byText(r.container, 'button', 'Put in project')).toThrow();
    expect(tp.querySelector('[data-in-project]')).toBeNull();
    // the agent CLIs and the gateway are on the Agents page
    expect(r.$('[data-testid="cli-gateway"]')).toBeNull();
  });

  it('has no skills section of its own and no second link to them: the Skills pill under Library is the way (UI review P1-8)', async () => {
    const r = show();
    await one(r, '[data-testid="lib-template-empty"], [aria-label="Agent templates"]');
    expect(r.$('[data-testid="library-skills"]')).toBeNull();
    expect(r.$('a[href="/library/skills"]')).toBeNull();
  });

  it('shows the prebuilt agents as a read-only section next to the templates', async () => {
    const r = show({ agentCatalogList: async () => CATALOG });
    const sec = await one(r, '[data-testid="library-prebuilt"]');
    await waitFor(() => expect(sec.querySelectorAll('[data-testid="prebuilt"]').length).toBe(CATALOG.length), r);
    expect(sec.textContent).toContain('Cloudflare Engineer');
    // read-only: nothing adds an agent here; every card can still be opened to see what it comes with (decision P43)
    const names = Array.from(sec.querySelectorAll('[data-testid="prebuilt"] button')).map((b) => (b.getAttribute('aria-label') ?? b.textContent ?? '').trim());
    expect(names.filter((n) => n.startsWith('Add')).length).toBe(0);
    expect(names).toContain('What Cloudflare Engineer comes with');
  });

  it('opens the panel of a template just made: the next step is there', async () => {
    let tpls: ReturnType<typeof tpl>[] = [];
    const r = show({
      agentTemplateList: async () => tpls,
      agentTemplateSave: async () => ((tpls = [tpl()]), tpls[0]!),
    } as never);
    await waitFor(() => expect(r.container.textContent).toContain('No templates yet.'), r);
    await r.act(() => byText(r.container, 'button', 'New agent template').click());
    const tf = await one(r, '[data-testid="template-form"]');
    type(byLabel(tf, 'Template name'), 'Payments dev');
    type(byLabel(tf, 'Role'), 'backend');
    r.root.flush();
    await r.act(() => byText(tf, 'button', 'Create template').click());
    const tp = await one(r, '[data-testid="lib-template"]');
    expect(tp.getAttribute('data-template')).toBe('Payments dev');
    expect(note(r)).toBe('The template Payments dev was saved.');
  });

  it('deleting a template needs a confirmation; agents made from it stay', async () => {
    const deletes: Calls = [];
    const r = show({ agentTemplateDelete: rec(deletes, () => undefined) as never });
    const card = await pick(r, 'lib-template');
    await r.act(() => byText(card, 'button', 'Delete').click());
    expect(deletes).toEqual([]);
    expect(card.textContent).toContain('Agents already made from it stay.');
    await r.act(() => byText(card, 'button', 'Yes, delete').click());
    await waitFor(() => expect(deletes).toEqual([['tpl_1']]), r);
  });

  it('opens the template form for a new template (the form itself belongs to the team screen)', async () => {
    const r = show({ agentTemplateList: async () => [] });
    await waitFor(() => expect(r.container.textContent).toContain('No templates yet.'), r);
    expect(r.$('dialog[open]')).toBeNull();
    await r.act(() => byText(r.container, 'button', 'New agent template').click());
    await waitFor(() => expect(r.$('dialog[open]')!.textContent).toContain('New agent template'), r);
  });
});
