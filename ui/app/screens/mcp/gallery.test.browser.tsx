import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { IntegrationView } from '../../api/extTypes.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { IntegrationsScreen } from '../integrations/integrations-screen.tsx';
import { devLicense, galleryCard, libServer, pay, projServer, rev } from './fixtures.ts';
import { identityLine } from './gallery.tsx';

afterEach(cleanup);

/** The element, or a throw so waitFor keeps waiting. */
const must = <T,>(x: T | null | undefined): T => {
  if (x == null) throw new Error('not there yet');
  return x;
};

// TEST DOUBLES (labelled): the client below answers like the core's mcplib.gallery, mcplib.connect, integration.list and
// skill.registries. The cards sit on the Integrations page (decision P47), in the Agent tools section of their vendor's tile.
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
    mcpServerList: async () => [],
    mcpLibList: async () => [],
    mcpGallery: async () => [],
    skillRegistries: async () => [],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return mount(<IntegrationsScreen />, s);
}
type R = ReturnType<typeof show>;
const tileOf = (r: R, kind: string) => waitFor(() => must(r.$(`[data-testid="integration-tile"][data-kind="${kind}"]`)) as HTMLElement, r);
/** Opens the vendor's tile, so its cards are on the page. */
const openVendor = async (r: R, kind: string) => {
  const tile = await tileOf(r, kind);
  if (tile.getAttribute('aria-pressed') !== 'true') await r.act(() => tile.click());
  return waitFor(() => must(r.$(`[data-testid="integration-panel"][data-kind="${kind}"]`)) as HTMLElement, r);
};
const cardOf = (r: R, id: string) => waitFor(() => must(r.$(`[data-testid="connector"][data-id="${id}"]`)) as HTMLElement, r);

const GH = galleryCard({
  id: 'gh',
  display: 'GitHub',
  publisher: 'GitHub',
  brand: 'github',
  auth: 'bearer',
  tokenHelp: 'A fine-grained personal access token.',
  integration: 'github',
  summary: 'Pull requests, issues, Actions runs and code of your repositories.',
});
const LINEAR = galleryCard({ id: 'linear', display: 'Linear', publisher: 'Linear', brand: 'linear', auth: 'oauth', integration: 'linear', tools: [] });
const CFDOCS = galleryCard({ id: 'cfdocs', display: 'Cloudflare Documentation', publisher: 'Cloudflare', brand: 'cloudflare', group: 'Cloudflare', skillRegistries: ['cloudflare-skills'] });
const CFAPI = galleryCard({ id: 'cfapi', display: 'Cloudflare API', publisher: 'Cloudflare', brand: 'cloudflare', group: 'Cloudflare', auth: 'oauth', skillRegistries: ['cloudflare-skills'] });
const CTX7 = galleryCard({ id: 'ctx7', display: 'Context7', publisher: 'Upstash', brand: 'context7', auth: 'bearer', authOptional: true });
const MSLEARN = galleryCard({ skillRegistries: ['microsoft-skills', 'msdocs-mcp-skills'] });

const linearConn: IntegrationView = {
  id: 'int_1',
  kind: 'linear',
  name: 'Linear',
  projectId: '',
  status: 'ok',
  statusDetail: '',
  authKind: 'api_key',
  config: {},
  pollIntervalS: 0,
  enabled: true,
  created: 0,
  updated: 0,
  version: 1,
  hasCredential: true,
  sync: { state: 'ok', lastSuccessAt: 0, lastAttemptAt: 0, itemsSeen: 0, itemsLastSync: 0, consecutiveFailures: 0, resuming: false },
  tools: { syncTools: [], ops: [] },
  identity: { account: 'kyle', workspace: 'Acme' },
  checkedAt: 1,
  assignments: [],
};

const REGISTRIES = [
  { id: 'cloudflare-skills', repo: 'cloudflare/skills', root: 'skills/', license: 'Apache-2.0', trust: 'vendor_official', enabled: true, curated: true },
  { id: 'microsoft-skills', repo: 'microsoft/skills', license: 'MIT', trust: 'vendor_official', importMode: 'selective_only', notes: '175 skills; repo warns loading all causes context rot', enabled: true, curated: true },
  { id: 'msdocs-mcp-skills', repo: 'MicrosoftDocs/mcp', license: 'content CC BY 4.0, code MIT', trust: 'vendor_official', enabled: true, curated: true },
];

describe('the Integrations page: one tile per vendor, with the MCP connector cards in its panel', () => {
  it('shows a tile for every integration kind and every MCP-only vendor (Cloudflare on one tile), with the state of both channels', async () => {
    const r = show(
      {
        mcpGallery: async () => [MSLEARN, GH, LINEAR, CFDOCS, { ...CFAPI, status: 'needs_sign_in' }, { ...CTX7, status: 'connected', libraryId: 'ctx7' }],
        integrationList: async () => [linearConn],
      },
      { projects: [] },
    );
    await tileOf(r, 'ctx7');
    const tiles = Array.from(r.$$('[data-testid="integration-tile"]')) as HTMLElement[];
    expect(tiles.map((t) => t.getAttribute('data-kind'))).toEqual(['linear', 'notion', 'sentry', 'aikido', 'github', 'mslearn', 'cloudflare', 'ctx7']);
    // the integration kinds keep data-connected; the MCP-only vendors say so
    expect(tiles.slice(0, 5).map((t) => t.getAttribute('data-mcp'))).toEqual([null, null, null, null, null]);
    expect(tiles.slice(5).map((t) => t.getAttribute('data-mcp'))).toEqual(['true', 'true', 'true']);
    expect(tiles[0]!.getAttribute('data-connected')).toBe('true');
    // both channels where the vendor has them
    expect(tiles[0]!.textContent).toContain('Sync: connected as kyle · Agents: not connected');
    expect(tiles[3]!.textContent).toContain('Sync: not connected');
    expect(tiles[3]!.textContent).not.toContain('Agents:');
    expect(tiles[4]!.textContent).toContain('Sync: not connected · Agents: not connected');
    expect(tiles[5]!.textContent).toContain('Microsoft Learn');
    expect(tiles[5]!.textContent).toContain('Agents: not connected');
    expect(tiles[5]!.textContent).not.toContain('Sync:');
    expect(tiles[5]!.querySelector('[data-brand="microsoft"]')).not.toBeNull();
    expect(tiles[6]!.textContent).toContain('Cloudflare');
    expect(tiles[6]!.textContent).toContain('Documentation, API');
    expect(tiles[6]!.textContent).toContain('Agents: needs sign-in');
    expect(tiles[7]!.textContent).toContain('Agents: connected');
    // one tile is selected at a time; the page has no MCP page to link to any more
    expect(r.$('[data-testid="integration-panel"]')).toBeNull();
    expect(r.container.querySelector('a[href="/mcp"]')).toBeNull();
  });

  it('selecting Linear shows its app sync panel and its connector card together; selecting Cloudflare shows its two cards', async () => {
    const r = show({ mcpGallery: async () => [MSLEARN, LINEAR, CFDOCS, CFAPI], integrationList: async () => [linearConn] });
    const lin = await openVendor(r, 'linear');
    expect(lin.querySelector('[data-testid="integration-sync"] [data-testid="integration"][data-kind="linear"]')).not.toBeNull();
    expect(lin.querySelector('[data-testid="connection-identity"]')!.textContent).toContain('kyle');
    const agents = lin.querySelector('[data-testid="integration-agents"]')!;
    expect(agents.querySelectorAll('[data-testid="connector"]').length).toBe(1);
    const card = agents.querySelector('[data-testid="connector"][data-id="linear"]') as HTMLElement;
    expect(card.textContent).toContain('Sign in (OAuth)');
    expect(card.textContent).not.toContain('Drydock also imports'); // same page now
    expect(card.querySelector('a[href="/integrations"]')).toBeNull();
    expect(lin.querySelector('[data-testid="integration-skills"]')).toBeNull(); // Linear publishes no skill registry

    const cf = await openVendor(r, 'cloudflare');
    expect(r.$$('[data-testid="integration-panel"]').length).toBe(1); // the Linear panel closed
    expect(cf.querySelector('[data-testid="integration-sync"]')).toBeNull(); // no app sync for an MCP-only vendor
    const cards = Array.from(cf.querySelectorAll('[data-testid="connector"]')).map((c) => c.getAttribute('data-id'));
    expect(cards).toEqual(['cfdocs', 'cfapi']);
    expect(cf.textContent).toContain('Agent tools (MCP)');
  });

  it('a Microsoft Learn card has logo, name, publisher, what it gives agents, sign-in kind, license, docs and state', async () => {
    const r = show({ mcpGallery: async () => [MSLEARN] }, { projects: [] });
    await openVendor(r, 'mslearn');
    const ml = await cardOf(r, 'mslearn');
    expect(ml.querySelector('[data-brand="microsoft"]')).not.toBe(null);
    expect(ml.querySelector('h4')!.textContent).toBe('Microsoft Learn');
    expect(ml.textContent).toContain('Microsoft · official');
    expect(ml.textContent).toContain('Search and read official Microsoft');
    expect(ml.textContent).toContain('No account needed');
    expect(ml.textContent).toContain('License MIT');
    expect(ml.querySelector('[data-testid="connector-status"]')!.textContent).toBe('Not connected');
    expect(ml.querySelector('a[target="_blank"]')!.getAttribute('href')).toBe('https://github.com/MicrosoftDocs/mcp');
    // no project yet: connecting still works, putting it in a project waits
    const line = await waitFor(() => must(r.$('[data-testid="no-project"]')) as HTMLElement, r);
    expect(line.querySelector('a')!.getAttribute('href')).toBe('/project/new');
  });

  it('connects a server that needs no account with one click', async () => {
    const calls: unknown[][] = [];
    let cards: McpGalleryCard[] = [MSLEARN];
    const r = show({
      mcpGallery: async () => cards,
      mcpConnect: async (...a: unknown[]) => {
        calls.push(a);
        cards = [galleryCard({ status: 'connected', libraryId: 'mslearn', toolsListed: true, serverInfo: { name: 'learn-mcp', title: 'Microsoft Learn', version: '1.2' } })];
        return { card: cards[0]! };
      },
    });
    await openVendor(r, 'mslearn');
    const ml = await cardOf(r, 'mslearn');
    await r.act(() => byText(ml, 'button', 'Connect Microsoft Learn').click());
    await waitFor(() => expect(calls).toEqual([['mslearn', undefined]]), r);
    const after = await waitFor(() => {
      const c = r.$('[data-id="mslearn"]') as HTMLElement;
      if (c.dataset.status !== 'connected') throw new Error('not yet');
      return c;
    }, r);
    expect(after.querySelector('[data-testid="connector-account"]')!.textContent).toContain('the server says it is Microsoft Learn (learn-mcp 1.2)');
    // the tile follows
    await waitFor(() => expect((r.$('[data-testid="integration-tile"][data-kind="mslearn"]') as HTMLElement).textContent).toContain('Agents: connected'), r);
  });

  it('asks for a token, sends it once, and never shows it again', async () => {
    const calls: unknown[][] = [];
    let cards: McpGalleryCard[] = [GH];
    const r = show({
      mcpGallery: async () => cards,
      mcpConnect: async (...a: unknown[]) => {
        calls.push(a);
        cards = [{ ...GH, status: 'connected', libraryId: 'gh', account: 'octocat' }];
        return { card: cards[0]! };
      },
    });
    await openVendor(r, 'github');
    const gh = await cardOf(r, 'gh');
    await r.act(() => byText(gh, 'button', 'Connect GitHub').click());
    expect(calls.length).toBe(0); // the token comes first
    const field = byLabel<HTMLInputElement>(gh, 'Token for GitHub');
    expect(field.type).toBe('password');
    expect(gh.textContent).toContain('A fine-grained personal access token.');
    type(field, 'ghp_secret_value_123');
    await r.act(() => (gh.querySelector('[data-testid="connector-token"] button[type="submit"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual([['gh', 'ghp_secret_value_123']]), r);
    const after = await waitFor(() => {
      const c = r.$('[data-id="gh"]') as HTMLElement;
      if (c.dataset.status !== 'connected') throw new Error('not yet');
      return c;
    }, r);
    expect(after.querySelector('[data-testid="connector-account"]')!.textContent).toContain('Connected as octocat');
    expect(r.container.innerHTML).not.toContain('ghp_secret_value_123');
  });

  it('starts the browser sign-in for an OAuth server and links the sign-in page', async () => {
    let cards: McpGalleryCard[] = [LINEAR];
    const r = show({
      mcpGallery: async () => cards,
      mcpConnect: async () => {
        cards = [{ ...LINEAR, status: 'needs_sign_in', libraryId: 'linear', detail: 'Sign in with your Linear account to finish connecting.' }];
        return { card: cards[0]!, oauth: { id: 'linear', state: 'waiting', url: 'https://linear.app/oauth/authorize?x=1', signedIn: false } };
      },
      mcpOAuthStatus: async () => ({ id: 'linear', state: 'waiting', url: 'https://linear.app/oauth/authorize?x=1', signedIn: false }),
    });
    await openVendor(r, 'linear');
    const lin = await cardOf(r, 'linear');
    await r.act(() => byText(lin, 'button', 'Connect Linear').click());
    const link = await waitFor(() => must(r.$('[data-id="linear"] [data-testid="oauth-link"]')) as HTMLAnchorElement, r);
    expect(link.getAttribute('href')).toBe('https://linear.app/oauth/authorize?x=1');
    expect(link.getAttribute('target')).toBe('_blank');
    expect((r.$('[data-id="linear"] [data-testid="connector-status"]') as HTMLElement).textContent).toBe('Needs sign-in');
    await waitFor(() => expect((r.$('[data-testid="integration-tile"][data-kind="linear"]') as HTMLElement).textContent).toContain('Agents: needs sign-in'), r);
  });

  it('says why a server failed and offers to try again', async () => {
    const tries: unknown[][] = [];
    const r = show({
      mcpGallery: async () => [galleryCard({ status: 'error', libraryId: 'mslearn', detail: 'could not reach Microsoft Learn: connection refused' })],
      mcpLibDiscover: (async (...a: unknown[]) => (tries.push(a), libServer())) as never,
    });
    expect((await tileOf(r, 'mslearn')).textContent).toContain('Agents: error');
    await openVendor(r, 'mslearn');
    const ml = await cardOf(r, 'mslearn');
    expect(ml.querySelector('[role="alert"]')!.textContent).toContain('connection refused');
    expect(ml.querySelector('[data-testid="connector-status"]')!.textContent).toBe('Error');
    await r.act(() => byText(ml, 'button', 'Try again').click());
    await waitFor(() => expect(tries).toEqual([['mslearn']]), r);
  });

  it('adds a connected server to a project, grants it to an agent, and disconnects it', async () => {
    const adds: unknown[][] = [];
    const grants: unknown[][] = [];
    const deletes: unknown[][] = [];
    const r = show(
      {
        mcpGallery: async () => [galleryCard({ status: 'connected', libraryId: 'mslearn', projects: ['prj_1'] })],
        mcpServerAdd: (async (...a: unknown[]) => (adds.push(a), projServer())) as never,
        mcpServerGrant: (async (...a: unknown[]) => (grants.push(a), projServer())) as never,
        mcpLibDelete: (async (...a: unknown[]) => void deletes.push(a)) as never,
      },
      { projects: [{ id: 'prj_1', name: 'gocalc' }, { id: 'prj_2', name: 'Beta' }] },
    );
    await openVendor(r, 'mslearn');
    const ml = await cardOf(r, 'mslearn');
    expect(ml.textContent).toContain('In: gocalc.');
    const sel = await waitFor(() => byLabel<HTMLSelectElement>(ml, 'Project to add Microsoft Learn to'), r);
    expect(Array.from(sel.options).map((o) => o.textContent)).toEqual(['Beta']);
    await r.act(() => byText(ml, 'button', 'Add to project').click());
    await waitFor(() => expect(adds).toEqual([['prj_2', 'mslearn']]), r);
    await r.act(() => byText(ml, 'button', 'Grant to agents').click());
    const box = await waitFor(() => byLabel<HTMLInputElement>(ml, 'Pay may use Microsoft Learn'), r);
    await r.act(() => box.click());
    await waitFor(() => expect(grants).toEqual([['prj_1', 'mslearn', 'agt_1', true]]), r);
    await r.act(() => byText(ml, 'button', 'Disconnect').click());
    await r.act(() => byText(ml, 'button', 'Yes, disconnect').click());
    await waitFor(() => expect(deletes).toEqual([['mslearn']]), r);
  });

  it("skills from this vendor: Add all puts the vendor's Library skills into the current project, accepted; a selective-only registry points to the Skills page", async () => {
    const imports: unknown[][] = [];
    const r = show({
      mcpGallery: async () => [MSLEARN, CFDOCS, CFAPI],
      skillRegistries: async () => REGISTRIES as never,
      skillLibraryAddAll: (async (...a: unknown[]) => (imports.push(a), { added: ['wrangler', 'workers'], unchanged: ['d1'], blocked: [], skipped: [{ name: 'x', reason: 'invalid' }] })) as never,
    });
    const cf = await openVendor(r, 'cloudflare');
    const skills = await waitFor(() => must(cf.querySelector('[data-testid="integration-skills"]')) as HTMLElement, r);
    // the two Cloudflare cards name the same registry: listed once
    const regs = await waitFor(() => {
      const li = Array.from(skills.querySelectorAll('[data-testid="vendor-registry"]'));
      if (li.length !== 1 || !li[0]!.textContent?.includes('cloudflare/skills')) throw new Error('not yet');
      return li as HTMLElement[];
    }, r);
    expect(regs[0]!.textContent).toContain('license Apache-2.0');
    const btn = byText<HTMLButtonElement>(regs[0]!, 'button', 'Add all to gocalc');
    expect(btn.disabled).toBe(false);
    await r.act(() => btn.click());
    await waitFor(() => expect(imports).toEqual([['prj_1', 'cloudflare-skills']]), r);
    const result = await waitFor(() => must(skills.querySelector('[data-testid="vendor-add-result"]')) as HTMLElement, r);
    expect(result.textContent).toBe('2 added, 1 unchanged, 1 skipped');
    expect(skills.querySelector('a[href="/skills"]')).not.toBeNull();

    // Microsoft publishes two registries; the selective-only one cannot be imported whole
    const ms = await openVendor(r, 'mslearn');
    const msSkills = await waitFor(() => must(ms.querySelector('[data-testid="integration-skills"]')) as HTMLElement, r);
    const rows = await waitFor(() => {
      const li = Array.from(msSkills.querySelectorAll('[data-testid="vendor-registry"]'));
      if (li.length !== 2) throw new Error('not yet');
      return li as HTMLElement[];
    }, r);
    expect(rows.map((x) => x.getAttribute('data-registry'))).toEqual(['microsoft-skills', 'msdocs-mcp-skills']);
    const selective = byText<HTMLButtonElement>(rows[0]!, 'button', 'Add all to gocalc');
    expect(selective.disabled).toBe(true);
    expect(rows[0]!.querySelector('[data-testid="vendor-selective"]')!.textContent).toContain('pick its skills one at a time on the Skills page');
    expect(rows[0]!.querySelector('[data-testid="vendor-selective"] a')!.getAttribute('href')).toBe('/skills');
    expect(byText<HTMLButtonElement>(rows[1]!, 'button', 'Add all to gocalc').disabled).toBe(false);
  });

  it('skills from this vendor without a project: one line, and the Skills page link', async () => {
    const r = show({ mcpGallery: async () => [CFDOCS, CFAPI], skillRegistries: async () => REGISTRIES as never }, { projects: [] });
    const cf = await openVendor(r, 'cloudflare');
    const skills = await waitFor(() => must(cf.querySelector('[data-testid="integration-skills"]')) as HTMLElement, r);
    expect(skills.textContent).toContain("Choose a project to add this vendor's skills to.");
    await waitFor(() => expect(skills.textContent).toContain('cloudflare/skills'), r);
    expect(skills.querySelector('[data-testid="vendor-import-all"]')).toBeNull();
    expect(skills.querySelector('a[href="/skills"]')).not.toBeNull();
  });

  it('shows your own servers by what they say about themselves, and imports a claude_desktop_config.json', async () => {
    const saves: unknown[] = [];
    const discovered: unknown[] = [];
    const r = show({
      mcpLibList: async () => [
        libServer({
          id: 'mine',
          display: 'My tracker',
          serverInfo: { name: 'acme-tracker', title: 'Acme Tracker', version: '3.2.1', instructions: 'Use search first.' },
        }),
      ],
      mcpLibSave: (async (i: unknown) => (saves.push(i), libServer())) as never,
      mcpLibDiscover: (async (id: unknown) => (discovered.push(id), libServer())) as never,
    });
    const own = await waitFor(() => must(r.$('[data-testid="own-servers"] [data-testid="own-server"][data-id="mine"]')) as HTMLElement, r);
    expect(own.querySelector('[data-testid="own-server-identity"]')!.textContent).toBe('Identifies itself as Acme Tracker (acme-tracker 3.2.1)');
    expect(own.textContent).toContain('Use search first.');

    await r.act(() => byText(r.container, 'button', 'Import from a file').click());
    const panel = await waitFor(() => must(document.querySelector('[data-testid="mcp-import"]')) as HTMLElement, r);
    type(byLabel<HTMLTextAreaElement>(panel, 'Configuration (JSON)'), JSON.stringify({ mcpServers: { docs: { url: 'https://learn.microsoft.com/api/mcp' }, gh: { command: 'github-mcp-server', args: ['stdio'], env: { GITHUB_PERSONAL_ACCESS_TOKEN: 'ghp_x' } } } }));
    await r.act(() => byText(panel, 'button', 'Read servers').click());
    await waitFor(() => expect(panel.querySelectorAll('[data-testid="import-candidate"]').length).toBe(2), r);
    expect(panel.textContent).toContain('GITHUB_PERSONAL_ACCESS_TOKEN) are kept in the credential store');
    await r.act(() => byText(panel, 'button', 'Import 2 servers').click());
    await waitFor(() => expect(discovered).toEqual(['docs', 'gh']), r);
    expect(saves).toEqual([
      { id: 'docs', display: 'docs', transport: 'http', url: 'https://learn.microsoft.com/api/mcp' },
      { id: 'gh', display: 'gh', transport: 'stdio', argv: ['github-mcp-server', 'stdio'], secretEnv: { GITHUB_PERSONAL_ACCESS_TOKEN: 'ghp_x' } },
    ]);
  });

  it('names identities as plain text', () => {
    expect(identityLine(undefined)).toBe('');
    expect(identityLine({ name: 'x', version: '1' })).toBe('x 1');
    expect(identityLine({ name: 'x', title: 'X Server' })).toBe('X Server (x)');
  });
});
