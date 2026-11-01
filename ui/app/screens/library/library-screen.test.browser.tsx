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

function show(client: Partial<Client> = {}, tab: 'mine' | 'starters' = 'mine') {
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
  const r = mount(<LibraryScreen tab={tab} onNavigate={() => {}} />, s);
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
describe('Agents > My agents (decision P140)', () => {
  it('shows my agents as cards: avatar, name, one line of purpose, the model chip and the state; no panel until one is clicked; the MCP servers are in Integrations', async () => {
    const r = show({
      mcpLibList: async () => [libServer({ projects: [] }), libServer({ id: 'web', display: 'Web thing', transport: 'http', projects: ['prj_1', 'prj_2'] })],
      agentTemplateList: async () => [tpl(), tpl({ id: 'tpl_2', name: 'Lonely', role: '', adapter: 'codex', model: '', mcpServers: ['gone'] })],
    });

    const tplTiles = await waitFor(() => {
      const l = r.$$('ul[aria-label="My agents"] [data-testid="lib-template-tile"]');
      if (l.length !== 2) throw new Error('not yet');
      return Array.from(l) as HTMLElement[];
    }, r);
    const purpose = (el: HTMLElement) => el.querySelector('[data-testid="lib-template-purpose"]')!.textContent;
    const chip = (el: HTMLElement) => el.querySelector('[data-testid="lib-template-model"]')!.textContent;
    expect(tplTiles[0]!.querySelector('strong')!.textContent).toBe('Payments dev');
    expect(tplTiles[0]!.querySelector('[data-letters]')).not.toBeNull();
    expect(purpose(tplTiles[0]!)).toBe('Own the payments service.');
    expect(chip(tplTiles[0]!)).toBe('sonnet · high');
    expect(tplTiles[0]!.textContent).toContain('Gets 1 server');
    expect(purpose(tplTiles[1]!)).toBe('Own the payments service.');
    // no model set: the chip names the agent CLI
    expect(chip(tplTiles[1]!)).toBe('codex');
    expect(tplTiles[1]!.textContent).toContain('1 server not connected');
    expect(tplTiles[1]!.querySelector('small[data-tone="warn"]')).not.toBeNull();
    // each card has its ⋯ menu (Edit, Delete) beside the card's button, never inside it
    expect(r.$$('[data-testid="lib-template-more"]').length).toBe(2);
    expect(tplTiles[0]!.querySelector('[data-testid="lib-template-more"]')).toBeNull();

    // nothing is open until a tile is clicked
    expect(r.$('[data-testid="lib-template"]')).toBeNull();
    // the servers you add yourself moved to Integrations (decision P98): no section, no tile, no connect button here
    expect(r.$('ul[aria-label="MCP servers"]')).toBeNull();
    expect(r.$('[data-testid="lib-mcp-tile"]')).toBeNull();
    expect(r.container.textContent).not.toContain('Web thing');
    expect(() => byText(r.container, 'button', 'Connect an MCP server')).toThrow();
    expect(r.$('[data-testid="library-servers-link"]')!.getAttribute('href')).toBe('/integrations');
    expect(r.$('header')!.textContent).toContain('2 agents');
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
    const grid = r.$('ul[aria-label="My agents"]')!;
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
      const l = r.$$('ul[aria-label="My agents"] [data-testid="lib-template-tile"]');
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
    const empty = await one(r, '[data-testid="lib-template-empty"]');
    expect(empty.textContent).toContain('No agents of your own yet.');
    // a starter, or an agent of a project's Crew (named Crew, not Team)
    expect(Array.from(empty.querySelectorAll('a')).map((a) => [a.textContent, a.getAttribute('href')])).toEqual([
      ['starter', '/library/starters'],
      ['Crew', '/team'],
    ]);
    expect(r.$('ul[aria-label="My agents"]')).toBeNull();
    expect(r.$('[data-testid="lib-mcp-empty"]')).toBeNull();
  });

  it("shows a template's panel and says where a project gets one", async () => {
    const r = show();
    const tp = await pick(r, 'lib-template');
    expect(tp.textContent).toContain('Payments dev');
    expect(tp.querySelector('[data-testid="lib-template-where"] a')!.getAttribute('href')).toBe('/team');
    expect(tp.querySelector('[data-testid="lib-template-where"] a')!.textContent).toBe('Crew');
    // the server it gives, by its name
    expect(tp.querySelector('ul[aria-label="MCP servers it gets"]')!.textContent).toContain('Test server');
    expect(r.$('h1')!.textContent).toContain('My agents');
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
    await one(r, '[data-testid="lib-template-empty"], [aria-label="My agents"]');
    expect(r.$('[data-testid="library-skills"]')).toBeNull();
    expect(r.$('a[href="/library/skills"]')).toBeNull();
  });

  it('Starters: the prebuilt agents, read-only; Use opens the agent form filled from the starter and saves it as my agent', async () => {
    const saved: Calls = [];
    const went: string[] = [];
    const s = testServices({
      licenseStatus: async () => devLicense,
      appInfo: async () => ({}) as never,
      projectList: async () => projects,
      adapterList: async () => [claude],
      mcpLibList: async () => [libServer({ projects: [] })],
      agentTemplateList: async () => [],
      agentCatalogList: async () => CATALOG,
      agentTemplateSkillChoices: async () => [],
      claudePractices: async () => ({ role: '', text: '' }) as never,
      agentTemplateSave: rec(saved, (x) => ({ ...tpl(), ...(x as object), id: 'tpl_new' })) as never,
    });
    s.workspace.start();
    const r = mount(<LibraryScreen tab="starters" onNavigate={(h) => went.push(h)} />, s);
    const sec = await one(r, '[data-testid="library-prebuilt"]');
    await waitFor(() => expect(sec.querySelectorAll('[data-testid="prebuilt"]').length).toBe(CATALOG.length), r);
    expect(r.$('h1')!.textContent).toContain('Starters');
    expect(sec.textContent).toContain('Cloudflare Engineer');
    // read-only: nothing adds an agent to a project here; every card can still be opened to see what it comes with (decision P43)
    const names = Array.from(sec.querySelectorAll('[data-testid="prebuilt"] button')).map((b) => (b.getAttribute('aria-label') ?? b.textContent ?? '').trim());
    expect(names.filter((n) => n.startsWith('Add')).length).toBe(0);
    expect(names).toContain('What Cloudflare Engineer comes with');
    const first = CATALOG[0]!;
    await r.act(() => sec.querySelector<HTMLButtonElement>(`button[aria-label="Use ${first.name}"]`)!.click());
    const tf = await one(r, '[data-testid="template-form"]');
    expect(r.$('dialog[open]')!.textContent).toContain(`Save ${first.name} as my agent`);
    expect((byLabel(tf, 'Template name') as HTMLInputElement).value).toBe(first.name);
    expect((byLabel(tf, 'Role') as HTMLInputElement).value).toBe(first.role);
    r.root.flush();
    await r.act(() => byText(tf, 'button', 'Create template').click());
    await waitFor(() => expect(saved.length).toBe(1), r);
    // a new one: no id is sent, so the starter never overwrites an agent of mine
    expect((saved[0]![0] as { id?: string }).id).toBe(undefined);
    expect((saved[0]![0] as { name: string }).name).toBe(first.name);
    await waitFor(() => expect(went).toEqual(['/library']), r);
    expect(note(r)).toBe(`${first.name} is one of your agents now.`);
  });

  it('opens the panel of a template just made: the next step is there', async () => {
    let tpls: ReturnType<typeof tpl>[] = [];
    const r = show({
      agentTemplateList: async () => tpls,
      agentTemplateSave: async () => ((tpls = [tpl()]), tpls[0]!),
    } as never);
    await waitFor(() => expect(r.container.textContent).toContain('No agents of your own yet.'), r);
    await r.act(() => byText(r.container, 'button', 'New agent').click());
    const tf = await one(r, '[data-testid="template-form"]');
    type(byLabel(tf, 'Template name'), 'Payments dev');
    type(byLabel(tf, 'Role'), 'backend');
    r.root.flush();
    await r.act(() => byText(tf, 'button', 'Create template').click());
    const tp = await one(r, '[data-testid="lib-template"]');
    expect(tp.getAttribute('data-template')).toBe('Payments dev');
    expect(note(r)).toBe('Payments dev is one of your agents now.');
  });

  it('deleting an agent needs a confirmation; agents made from it stay', async () => {
    const deletes: Calls = [];
    const r = show({ agentTemplateDelete: rec(deletes, () => undefined) as never });
    const card = await pick(r, 'lib-template');
    await r.act(() => byText(card, 'button', 'Delete').click());
    expect(deletes).toEqual([]);
    const confirm = await one(r, '[data-testid="lib-template-delete"]');
    expect(r.$('dialog[open]')!.textContent).toContain('Delete Payments dev?');
    expect(confirm.textContent).toContain('Agents already made from it stay in their projects.');
    await r.act(() => byText(confirm, 'button', 'Yes, delete').click());
    await waitFor(() => expect(deletes).toEqual([['tpl_1']]), r);
    await waitFor(() => expect(note(r)).toBe('The agent Payments dev was deleted. Agents already made from it stay.'), r);
  });

  it("the card's ⋯ menu opens Edit with the agent's own form", async () => {
    const r = show();
    const more = await one(r, '[data-testid="lib-template-more"]');
    expect(more.getAttribute('aria-label')).toBe('More for Payments dev');
    await r.act(() => more.click());
    const edit = await waitFor(() => {
      const it = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((m) => m.textContent === 'Edit');
      if (!it) throw new Error('no menu');
      return it;
    }, r);
    await r.act(() => edit.click());
    const tf = await one(r, '[data-testid="template-form"]');
    expect((byLabel(tf, 'Template name') as HTMLInputElement).value).toBe('Payments dev');
    expect(byText(tf, 'button', 'Save template')).not.toBeNull();
  });

  it('New agent opens the agent form (the form itself belongs to the team screen)', async () => {
    const r = show({ agentTemplateList: async () => [] });
    await waitFor(() => expect(r.container.textContent).toContain('No agents of your own yet.'), r);
    expect(r.$('dialog[open]')).toBeNull();
    await r.act(() => byText(r.container, 'button', 'New agent').click());
    await waitFor(() => expect(r.$('dialog[open] [data-testid="template-form"]')).not.toBeNull(), r);
  });
});
