import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { IntegrationView } from '../../api/extTypes.ts';
import type { McpGalleryCard } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, mount, testServices, type, typeKeys, waitFor } from '../../test/render.tsx';
import { IntegrationsScreen } from '../integrations/integrations-screen.tsx';
import { devLicense, galleryCard, libServer, pay, rev } from './fixtures.ts';
import { identityLine } from './gallery.tsx';

afterEach(cleanup);

/** The element, or a throw so waitFor keeps waiting. */
const must = <T,>(x: T | null | undefined): T => {
  if (x == null) throw new Error('not there yet');
  return x;
};

// TEST DOUBLES (labelled): the client below answers like the core's mcplib.gallery, mcplib.connect, integration.list and
// skill.registries. The cards sit on the Integrations page (decision P47), as rows in the Agent tools group of their vendor's
// detail (decision P98). GitHub's row opens its own page, so its detail is opened by its address (/integrations/github).
function show(client: Partial<Client>, opts: { projects?: { id: string; name: string }[]; initial?: string } = {}) {
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
    providerList: async () => [],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return mount(<IntegrationsScreen initial={opts.initial} go={() => {}} open={() => {}} />, s);
}
type R = ReturnType<typeof show>;
const rowOf = (r: R, kind: string) => waitFor(() => must(r.$(`[data-testid="integration-row"][data-kind="${kind}"]`)) as HTMLElement, r);
/** The state word at the end of a row, or "⚠ " and the problem's name where a warning sign stands in for the word. */
const stateOf = (el: Element) => {
  const alert = el.querySelector('[data-testid="row-alert"]');
  return alert ? `⚠ ${alert.getAttribute('aria-label')}` : (el.querySelector('[data-tone]')?.textContent ?? '');
};
/** Chooses the vendor's row, so its detail (and its cards) is on the page. */
const openVendor = async (r: R, kind: string) => {
  const row = await rowOf(r, kind);
  if (row.getAttribute('aria-current') !== 'true') await r.act(() => row.click());
  return waitFor(() => must(r.$(`[data-testid="integration-panel"][data-kind="${kind}"]`)) as HTMLElement, r);
};
/** GitHub's agent tools, by their address (/integrations/tools-github: its MCP server, apart from its settings, decision P106). */
const openGitHub = (r: R) => waitFor(() => must(r.$('[data-testid="integration-panel"][data-kind="tools-github"]')) as HTMLElement, r);
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

describe('the Integrations page: one row per vendor, with the MCP connector cards as rows in its detail', () => {
  it('lists a row for every integration kind and every MCP-only vendor (Cloudflare on one row), with the state in a word', async () => {
    const r = show(
      {
        mcpGallery: async () => [MSLEARN, GH, LINEAR, CFDOCS, { ...CFAPI, status: 'needs_sign_in' }, { ...CTX7, status: 'connected', libraryId: 'ctx7' }],
        integrationList: async () => [linearConn],
      },
      { projects: [] },
    );
    await rowOf(r, 'ctx7');
    const rows = Array.from(r.$$('[data-testid="integration-row"]')) as HTMLElement[];
    // then OpenRouter under Model providers (decision P103)
    // a service's MCP server is a row of its own under Agent tools, beside the vendors that have only servers (decision P106)
    expect(rows.map((t) => t.getAttribute('data-kind'))).toEqual(['linear', 'notion', 'sentry', 'aikido', 'github', 'tools-linear', 'tools-github', 'mslearn', 'cloudflare', 'ctx7', 'openrouter']);
    // the integration kinds keep data-connected; the agent tools rows say so, under Agent tools
    expect(rows.slice(0, 5).map((t) => t.getAttribute('data-mcp'))).toEqual([null, null, null, null, null]);
    expect(rows.slice(5, 10).map((t) => t.getAttribute('data-mcp'))).toEqual(['true', 'true', 'true', 'true', 'true']);
    expect(rows[10]!.getAttribute('data-provider')).toBe('true');
    expect(rows.slice(5, 10).every((t) => t.closest('[data-testid="list-group-tools"]'))).toBe(true);
    expect(rows[10]!.closest('[data-testid="list-group-models"]')).not.toBeNull();
    expect(rows[0]!.getAttribute('data-connected')).toBe('true');
    // one word each: what works, what was never set up; waiting for a sign-in is Set up, not a problem
    expect(rows.map(stateOf)).toEqual(['On', 'Set up', 'Set up', 'Set up', 'Set up', 'Set up', 'Set up', 'Set up', 'Set up', 'On', 'Set up']);
    expect(rows[5]!.textContent).toContain('Linear');
    expect(rows[5]!.textContent).toContain('Tools for agents');
    expect(rows[7]!.textContent).toContain('Microsoft Learn');
    expect(rows[7]!.textContent).toContain('Tools for agents · 1 tool');
    expect(rows[7]!.querySelector('[data-brand="microsoft"]')).not.toBeNull();
    expect(rows[8]!.textContent).toContain('Cloudflare');
    expect(rows[8]!.textContent).toContain('Documentation, API');
    // the first row's detail shows; the page has no MCP page to link to any more
    expect(r.$$('[data-testid="integration-panel"]').length).toBe(1);
    expect(r.container.querySelector('a[href="/mcp"]')).toBeNull();
  });

  it("Linear's app sync and its connector card are two rows (P106); selecting Cloudflare shows its two cards", async () => {
    const r = show({ mcpGallery: async () => [MSLEARN, LINEAR, CFDOCS, CFAPI], integrationList: async () => [linearConn] });
    const sync = await openVendor(r, 'linear');
    expect(sync.querySelector('[data-testid="integration-sync"] [data-testid="integration"][data-kind="linear"]')).not.toBeNull();
    expect(sync.querySelector('[data-testid="connection-identity"]')!.textContent).toContain('kyle');
    expect(sync.querySelector('[data-testid="integration-agents"]')).toBeNull();
    const lin = await openVendor(r, 'tools-linear');
    expect(lin.querySelector('[data-testid="integration-sync"]')).toBeNull();
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
    expect(cf.querySelector('[data-testid="integration-agents"] h3')!.textContent).toBe('Agent tools');
    // an agent-tools vendor's servers are drawn whole: their facts are rows, not closed
    expect(cf.querySelector('[data-testid="connector"] details > summary')?.textContent).not.toBe('Details');
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
    // the row's label says License; its value is the licence alone, not "License MIT" beside it again
    const licence = [...ml.querySelectorAll('[data-part="label"]')].find((l) => l.textContent === 'License')!;
    expect(licence.nextElementSibling!.textContent).toBe('MIT');
    expect(ml.querySelector('[data-testid="connector-status"]')!.textContent).toBe('Not connected');
    expect(ml.querySelector('a[target="_blank"]')!.getAttribute('href')).toBe('https://github.com/MicrosoftDocs/mcp');
    // no project yet: connecting still works; there is nothing about projects on the card
    expect(byText<HTMLButtonElement>(ml, 'button', 'Connect Microsoft Learn').disabled).toBe(false);
    expect(ml.querySelector('[role="combobox"]')).toBeNull();
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
    // the row follows
    await waitFor(() => expect(stateOf(r.$('[data-testid="integration-row"][data-kind="mslearn"]')!)).toBe('On'), r);
  });

  it('asks for a token, sends it once, and never shows it again', async () => {
    const calls: unknown[][] = [];
    let cards: McpGalleryCard[] = [GH];
    const r = show(
      {
        mcpGallery: async () => cards,
        mcpConnect: async (...a: unknown[]) => {
          calls.push(a);
          cards = [{ ...GH, status: 'connected', libraryId: 'gh', account: 'octocat' }];
          return { card: cards[0]! };
        },
      },
      { initial: 'tools-github' },
    );
    await openGitHub(r);
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

  it('keeps every key typed into the token field, and sends the whole token (H12)', async () => {
    const calls: unknown[][] = [];
    let cards: McpGalleryCard[] = [GH];
    const r = show(
      {
        mcpGallery: async () => cards,
        mcpConnect: async (...a: unknown[]) => {
          calls.push(a);
          cards = [{ ...GH, status: 'connected', libraryId: 'gh', account: 'octocat' }];
          return { card: cards[0]! };
        },
      },
      { initial: 'tools-github' },
    );
    await openGitHub(r);
    const gh = await cardOf(r, 'gh');
    await r.act(() => byText(gh, 'button', 'Connect GitHub').click());
    const field = byLabel<HTMLInputElement>(gh, 'Token for GitHub');
    await typeKeys(field, 'ghp_typed_key_by_key', r);
    expect(field.value).toBe('ghp_typed_key_by_key');
    await r.act(() => (gh.querySelector('[data-testid="connector-token"] button[type="submit"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual([['gh', 'ghp_typed_key_by_key']]), r);
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
    await openVendor(r, 'tools-linear');
    const lin = await cardOf(r, 'linear');
    await r.act(() => byText(lin, 'button', 'Connect Linear').click());
    const link = await waitFor(() => must(r.$('[data-id="linear"] [data-testid="oauth-link"]')) as HTMLAnchorElement, r);
    expect(link.getAttribute('href')).toBe('https://linear.app/oauth/authorize?x=1');
    expect(link.getAttribute('target')).toBe('_blank');
    expect((r.$('[data-id="linear"] [data-testid="connector-status"]') as HTMLElement).textContent).toBe('Needs sign-in');
    // the row: a sign-in still to finish is Set up, not a problem (decision P106)
    await waitFor(() => expect(stateOf(r.$('[data-testid="integration-row"][data-kind="tools-linear"]')!)).toBe('Set up'), r);
  });

  it('a card that needs a sign-in shows one Sign in button, not a Sign-in heading with a badge and a second button', async () => {
    const r = show({
      mcpGallery: async () => [{ ...LINEAR, status: 'needs_sign_in', libraryId: 'linear' }],
      mcpOAuthStatus: async () => ({ id: 'linear', state: 'idle', signedIn: false }),
    });
    await openVendor(r, 'tools-linear');
    const lin = await cardOf(r, 'linear');
    const panel = await waitFor(() => must(lin.querySelector('[data-testid="oauth-panel"][data-state="idle"]')) as HTMLElement, r);
    const buttons = Array.from(panel.querySelectorAll('button'));
    expect(buttons.map((b) => b.textContent?.trim())).toEqual(['Sign in']);
    expect(buttons[0]!.getAttribute('title')).toBe("Your browser opens the server's own sign-in page.");
    expect(panel.textContent?.trim()).toBe('Sign in');
    // the card as a whole: one sign-in control (its status badge still says Needs sign-in)
    expect(Array.from(lin.querySelectorAll('button')).filter((b) => /sign.?in/i.test(b.textContent ?? '')).length).toBe(1);
    expect(lin.querySelector('[data-testid="connector-status"]')!.textContent).toBe('Needs sign-in');
  });

  it('says why a server failed and offers to try again', async () => {
    const tries: unknown[][] = [];
    const r = show({
      mcpGallery: async () => [galleryCard({ status: 'error', libraryId: 'mslearn', detail: 'could not reach Microsoft Learn: connection refused' })],
      mcpLibDiscover: (async (...a: unknown[]) => (tries.push(a), libServer())) as never,
    });
    expect(stateOf(await rowOf(r, 'mslearn'))).toBe('⚠ Could not connect');
    await openVendor(r, 'mslearn');
    const ml = await cardOf(r, 'mslearn');
    expect(ml.querySelector('[role="alert"]')!.textContent).toContain('connection refused');
    expect(ml.querySelector('[data-testid="connector-status"]')!.textContent).toBe('Error');
    await r.act(() => byText(ml, 'button', 'Try again').click());
    await waitFor(() => expect(tries).toEqual([['mslearn']]), r);
  });

  it("a connected card says how many projects have it, offers no project controls (those are on a project's Integrations page), and disconnects", async () => {
    const deletes: unknown[][] = [];
    const r = show(
      {
        mcpGallery: async () => [galleryCard({ status: 'connected', libraryId: 'mslearn', projects: ['prj_1', 'prj_2'] })],
        mcpLibDelete: (async (...a: unknown[]) => void deletes.push(a)) as never,
      },
      { projects: [{ id: 'prj_1', name: 'gocalc' }, { id: 'prj_2', name: 'Beta' }] },
    );
    await openVendor(r, 'mslearn');
    const ml = await cardOf(r, 'mslearn');
    expect(ml.querySelector('[data-testid="connector-projects"]')!.textContent).toBe("In 2 projects. Add it to a project, and grant it to agents, on that project's Integrations page.");
    expect(ml.textContent).not.toContain('gocalc');
    expect(ml.querySelector('[role="combobox"]')).toBeNull();
    expect(ml.querySelector('input[type="checkbox"]')).toBeNull();
    for (const name of ['Add to project', 'Grant to agents']) expect(() => byText(ml, 'button', name)).toThrow();
    await r.act(() => byText(ml, 'button', 'Disconnect').click());
    await r.act(() => byText(ml, 'button', 'Yes, disconnect').click());
    await waitFor(() => expect(deletes).toEqual([['mslearn']]), r);
  });

  it("skills from this vendor: each registry by name and license, with the skill library linked; nothing is added to a project here", async () => {
    const r = show({ mcpGallery: async () => [MSLEARN, CFDOCS, CFAPI], skillRegistries: async () => REGISTRIES as never });
    const cf = await openVendor(r, 'cloudflare');
    const skills = await waitFor(() => must(cf.querySelector('[data-testid="integration-skills"]')) as HTMLElement, r);
    // the two Cloudflare cards name the same registry: listed once
    const regs = await waitFor(() => {
      const li = Array.from(skills.querySelectorAll('[data-testid="vendor-registry"]'));
      if (li.length !== 1 || !li[0]!.textContent?.includes('cloudflare/skills')) throw new Error('not yet');
      return li as HTMLElement[];
    }, r);
    expect(regs[0]!.textContent).toContain('license Apache-2.0');
    expect(skills.querySelectorAll('button').length).toBe(0); // no "Add all to <project>"
    expect(skills.textContent).not.toContain('gocalc');
    expect(skills.querySelector('[data-testid="skills-link"]')!.getAttribute('href')).toBe('/library/skills');

    // Microsoft publishes two registries; the selective-only one says its skills are picked one at a time in the skill library
    const ms = await openVendor(r, 'mslearn');
    const msSkills = await waitFor(() => must(ms.querySelector('[data-testid="integration-skills"]')) as HTMLElement, r);
    const rows = await waitFor(() => {
      const li = Array.from(msSkills.querySelectorAll('[data-testid="vendor-registry"]'));
      if (li.length !== 2) throw new Error('not yet');
      return li as HTMLElement[];
    }, r);
    expect(rows.map((x) => x.getAttribute('data-registry'))).toEqual(['microsoft-skills', 'msdocs-mcp-skills']);
    expect(rows[0]!.querySelector('[data-testid="vendor-selective"]')!.textContent).toContain('pick its skills one at a time in the skill library');
    expect(rows[0]!.querySelector('[data-testid="vendor-selective"] a')!.getAttribute('href')).toBe('/library/skills');
    expect(rows[1]!.querySelector('[data-testid="vendor-selective"]')).toBeNull();
  });

  it('clips the descriptions a server gives its tools (L48)', async () => {
    const long = `${'Reads and writes everything. '.repeat(3000)}LAST WORDS`;
    const r = show(
      {
        mcpGallery: async () => [galleryCard({ id: 'gh', display: 'GitHub', publisher: 'GitHub', brand: 'github', auth: 'bearer', integration: 'github', tools: [{ name: 'huge', description: long, readOnlyHint: true }] })],
        mcpLibList: async () => [libServer({ id: 'mine', display: 'My tracker', serverInfo: { name: 'acme', version: '1', description: long } })],
      },
      { initial: 'tools-github' },
    );
    await openGitHub(r);
    const gh = await cardOf(r, 'gh');
    await r.act(() => (gh.querySelector('details summary') as HTMLElement).click());
    const tool = await waitFor(() => must(gh.querySelector('[data-testid="connector-tool-description"]')) as HTMLElement, r);
    expect(tool.textContent!.length).toBeLessThan(340);
    expect(gh.textContent).not.toContain('LAST WORDS');
  });

  it("a connected card links its server's own settings (which tools only read, Edit); the servers you add yourself are rows of their own", async () => {
    const r = show({
      mcpGallery: async () => [galleryCard({ status: 'connected', libraryId: 'mslearn' })],
      mcpLibList: async () => [libServer({ id: 'mslearn', display: 'Microsoft Learn', catalogId: 'mslearn' }), libServer({ id: 'mine', display: 'My tracker' })],
    });
    await openVendor(r, 'mslearn');
    const ml = await cardOf(r, 'mslearn');
    expect(ml.querySelector('[data-testid="connector-settings"]')!.getAttribute('href')).toBe('/integrations/server-mslearn');
    const mine = await waitFor(() => must(r.$('[data-testid="list-group-servers"] [data-key="server-mine"]')) as HTMLElement, r);
    expect(mine.textContent).toContain('My tracker');
    expect(r.$('[data-testid="list-group-servers"] [data-key="server-mslearn"]')).toBeNull();
    expect(r.$('a[href="/library"]')).toBeNull();
  });

  it('names identities as plain text', () => {
    expect(identityLine(undefined)).toBe('');
    expect(identityLine({ name: 'x', version: '1' })).toBe('x 1');
    expect(identityLine({ name: 'x', title: 'X Server' })).toBe('X Server (x)');
  });
});
