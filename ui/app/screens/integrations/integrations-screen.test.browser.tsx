import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Handle } from 'remix/component';
import type { Client } from '../../api/client.ts';
import type { IntegrationVerifyRecord, IntegrationVerifyReport } from '../../api/extTypes.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { galleryCard, libServer, pay, rev } from '../mcp/fixtures.ts';
import { asg, conn, kinds, project, project2 } from './fixtures.ts';
import { IntegrationsScreen } from './integrations-screen.tsx';

afterEach(cleanup);

// TEST DOUBLES only: a partial client answering with the shapes internal/integrations returns. This is the workspace's page:
// connecting once, as a list and the chosen row's detail (decision P98). A project's scope, items, rules and outbound writes
// are on the project's Integrations page (project-integrations-screen.test.browser.tsx); the vendors' MCP cards are tested in
// screens/mcp/gallery.test.browser.tsx and your own servers in server-detail.test.browser.tsx.

type Calls = unknown[][];
function rec<R>(calls: Calls, result: (...args: unknown[]) => R) {
  return async (...args: unknown[]) => {
    calls.push(args);
    return result(...args);
  };
}

/** The client the page reads: the integrations (this file's subject) and, as empty TEST DOUBLES, the MCP side. */
const baseClient = (): Partial<Client> => ({
  licenseStatus: async () => ({ configured: false, devMode: true, state: 'none', readOnly: false, message: '' }) as never,
  appInfo: async () => ({}) as never,
  projectList: async () => [project, project2] as never,
  agentList: async () => [pay, rev],
  roomList: async () => [],
  roomUnread: async () => [],
  needsYouList: async () => [],
  presenceList: async () => [],
  taskList: async () => [],
  integrationKinds: async () => kinds,
  mcpGallery: async () => [],
  mcpLibList: async () => [],
  mcpServerList: async () => [],
  mcpApprovalList: async () => [],
  skillRegistries: async () => [],
});

/** Where the page sent the window: `went` the addresses it replaced (the choice), `opened` the pages it opened. */
function show(client: Partial<Client>, initial?: string) {
  const s = testServices({ ...baseClient(), ...client });
  s.workspace.start();
  s.project.start();
  const went: string[] = [];
  const opened: string[] = [];
  const r = mount(<IntegrationsScreen initial={initial} go={(h) => void went.push(h)} open={(h) => void opened.push(h)} />, s);
  return Object.assign(r, { went, opened });
}
type R = ReturnType<typeof show>;
const find = (r: R, sel: string) =>
  waitFor(() => {
    const el = r.$(sel);
    if (!el) throw new Error(`no ${sel}`);
    return el as HTMLElement;
  }, r);
const button = (r: R, root: ParentNode, name: string) => waitFor(() => byText<HTMLButtonElement>(root, 'button', name), r);
const row = (r: R, kind: string) => find(r, `[data-testid="integration-row"][data-kind="${kind}"]`);
/** Choose a vendor's row and return its detail. */
const open = async (r: R, kind: string) => {
  const it = await row(r, kind);
  await r.act(() => it.click());
  return find(r, `[data-testid="integration"][data-kind="${kind}"]`);
};
const statusOf = (el: Element) => el.querySelector('[data-tone]')?.textContent ?? '';
const menuOpen = (r: R) => Array.from(r.$$('[role="menu"]')).some((m) => m.closest('[popover]')?.matches(':popover-open'));
/** Open a kit Menu by its trigger (one still finishing its last selection ignores a click, so it is repeated) and choose an item. */
async function fromMenu(r: R, trigger: HTMLElement, item: string) {
  await r.act(() => trigger.click());
  await waitFor(() => {
    if (!menuOpen(r) && trigger.getAttribute('aria-expanded') !== 'true') trigger.click();
    expect(menuOpen(r)).toBe(true);
  }, r);
  const hit = await waitFor(() => {
    const el = Array.from(r.$$('[popover]:popover-open [role="menuitem"]')).find((m) => m.textContent?.trim() === item) as HTMLElement | undefined;
    if (!el) throw new Error(`no menu item ${item}`);
    return el;
  }, r);
  await r.act(() => hit.click());
}

describe('Integrations: the list', () => {
  it('lists the five services with their fixed logos and names under Services, GitHub as a link to its own page; the first opens', async () => {
    const r = show({ integrationList: async () => [], mcpGallery: async () => [galleryCard({ id: 'linear', display: 'Linear', publisher: 'Linear', brand: 'linear', auth: 'oauth', integration: 'linear', tools: [] })] });
    const services = await find(r, 'nav[aria-label="All integrations"] [data-testid="list-group-services"]');
    await waitFor(() => expect(services.querySelectorAll('[data-testid="integration-row"]').length).toBe(5), r);
    const rows = Array.from(services.querySelectorAll<HTMLElement>('[data-testid="integration-row"]'));
    expect(rows.map((x) => x.getAttribute('data-kind'))).toEqual(['linear', 'notion', 'sentry', 'aikido', 'github']);
    expect(services.querySelector('h2')!.textContent).toBe('Servicesapp sync');
    for (const x of rows) {
      expect(x.querySelector('[data-brand]')).not.toBeNull();
      // one word of state at the end of the row
      expect(statusOf(x)).toBe('Set up');
    }
    expect(rows[3]!.querySelector('[data-brand="aikido"] path')).not.toBeNull(); // Aikido's own mark, not a lettermark
    expect(rows[0]!.textContent).toContain('Issues and projects');
    // GitHub's row is a link to its own page (its sign-in, connection, server and CI runners)
    expect(rows[4]!.tagName).toBe('A');
    expect(rows[4]!.getAttribute('href')).toBe('/github');
    // nothing chosen: the first row's detail, which no one can rename
    const card = await find(r, '[data-testid="integration"][data-kind="linear"]');
    expect(rows[0]!.getAttribute('aria-current')).toBe('true');
    expect(card.querySelector('[data-testid="integration-name"]')!.textContent).toBe('Linear');
    expect(card.querySelector('h2')!.textContent).toContain('Linear');
    expect(Array.from(card.querySelectorAll('label')).some((l) => /name/i.test(l.textContent ?? ''))).toBe(false);
    // the sync detail and the agents' tools of the same vendor are one detail (decision P47)
    expect(r.$('[data-testid="integration-sync"]')!.contains(card)).toBe(true);
    const agents = await find(r, '[data-testid="integration-agents"]');
    expect(agents.querySelector('[data-testid="connector"][data-id="linear"]')).not.toBeNull();
    expect(r.$('[data-testid="mcp-link"]')).toBeNull();
    // the API it calls, and its documentation
    expect(card.querySelector('[data-testid="api-info"]')!.textContent).toContain('2,500 requests per hour');
    // the address is left alone until a row is chosen
    expect(r.went).toEqual([]);
  });

  it('groups Services, Agent tools and Your servers; each row says its state in a word; the filters count All, On and Needs you', async () => {
    const r = show({
      integrationList: async () => [conn({ assignments: [asg(), asg({ id: 'asg_2', projectId: 'prj_2' })] }), conn({ id: 'int_2', kind: 'sentry', name: 'Sentry', status: 'unauthenticated', assignments: [] })],
      mcpGallery: async () => [
        galleryCard({ id: 'cfdocs', display: 'Cloudflare Documentation', publisher: 'Cloudflare', brand: 'cloudflare', group: 'Cloudflare' }),
        galleryCard({ status: 'connected', libraryId: 'mslearn' }),
      ],
      mcpLibList: async () => [libServer({ id: 'pg', display: 'postgres-local' }), libServer({ id: 'mslearn', display: 'Microsoft Learn', catalogId: 'mslearn' }), libServer({ id: 'new', display: 'Fresh', tools: [] })],
    });
    const nav = await find(r, 'nav[aria-label="All integrations"]');
    await waitFor(() => expect(nav.querySelectorAll('[data-testid="integration-row"]').length).toBe(9), r);
    expect(Array.from(nav.querySelectorAll('section > h2')).map((h) => h.textContent)).toEqual(['Servicesapp sync', 'Agent toolsMCP', 'Your serversMCP']);
    const state = (key: string) => statusOf(nav.querySelector(`[data-key="${key}"]`)!);
    expect(state('linear')).toBe('On');
    expect(state('sentry')).toBe('Key refused');
    expect(state('notion')).toBe('Set up');
    expect(state('mslearn')).toBe('On');
    expect(state('cloudflare')).toBe('Set up');
    expect(state('server-pg')).toBe('On');
    expect(state('server-new')).toBe('No tools yet');
    // a fact under the name: the projects of a service, the tools of a vendor or a server
    expect(nav.querySelector('[data-key="linear"]')!.textContent).toContain('Issues and projects · 2 projects');
    expect(nav.querySelector('[data-key="mslearn"]')!.textContent).toContain('· 1 tool');
    expect(nav.querySelector('[data-key="server-pg"]')!.textContent).toContain('Program on this PC · 2 tools');
    // a server connected from a vendor's card is that vendor's row, not one of yours
    expect(nav.querySelector('[data-key="server-mslearn"]')).toBeNull();
    expect(nav.querySelector('[data-testid="list-group-tools"] [data-kind="cloudflare"][data-mcp="true"]')).not.toBeNull();
    expect(nav.querySelector('[data-testid="list-group-servers"] [data-testid="add-server-row"]')!.textContent).toContain('Add a server');
    // the filters and their counts; the header counts what is on
    const f = (k: string) => nav.querySelector<HTMLButtonElement>(`[data-testid="list-filter-${k}"]`)!;
    expect([f('all').textContent, f('on').textContent, f('needs').textContent]).toEqual(['All 9', 'On 3', 'Needs you 2']);
    expect(r.$('header')!.textContent).toContain('Connected once for the whole workspace · 3 on');
    await r.act(() => f('needs').click());
    await waitFor(() => expect(Array.from(nav.querySelectorAll('[data-testid="integration-row"]')).map((x) => x.getAttribute('data-key'))).toEqual(['sentry', 'server-new']), r);
    expect(f('needs').getAttribute('aria-pressed')).toBe('true');
    await r.act(() => f('on').click());
    await waitFor(() => expect(Array.from(nav.querySelectorAll('[data-testid="integration-row"]')).map((x) => x.getAttribute('data-key'))).toEqual(['linear', 'mslearn', 'server-pg']), r);
    await r.act(() => f('all').click());
    // the filter box narrows by name and by the line under it
    const box = nav.querySelector<HTMLInputElement>('[data-testid="list-filter"]')!;
    expect(box.getAttribute('aria-label')).toBe('Filter integrations');
    await r.act(() => type(box, 'postgres'));
    await waitFor(() => expect(Array.from(nav.querySelectorAll('[data-testid="integration-row"]')).map((x) => x.getAttribute('data-key'))).toEqual(['server-pg']), r);
  });

  it('a filter with nothing in it says so', async () => {
    const r = show({ integrationList: async () => [] });
    await row(r, 'linear');
    await r.act(() => (r.$('[data-testid="list-filter-on"]') as HTMLElement).click());
    expect((await find(r, '[data-testid="list-empty"]')).textContent).toBe('Nothing is on yet.');
    await r.act(() => (r.$('[data-testid="list-filter-needs"]') as HTMLElement).click());
    await waitFor(() => expect(r.$('[data-testid="list-empty"]')!.textContent).toBe('Nothing needs you.'), r);
  });

  it('choosing a row opens its detail and puts it in the address; the address opens a row, and the page follows when it changes', async () => {
    const r = show({ integrationList: async () => [conn()], mcpGallery: async () => [galleryCard({ id: 'cfdocs', display: 'Cloudflare Documentation', publisher: 'Cloudflare', brand: 'cloudflare', group: 'Cloudflare' })] });
    await open(r, 'linear');
    expect(r.went).toEqual([]); // already the chosen one
    const cf = await row(r, 'cloudflare');
    await r.act(() => cf.click());
    const panel = await find(r, '[data-testid="integration-panel"][data-kind="cloudflare"]');
    expect(panel.querySelector('h2')!.textContent).toBe('Cloudflare');
    expect(r.$$('[data-testid="integration-panel"]').length).toBe(1);
    expect(cf.getAttribute('aria-current')).toBe('true');
    expect(r.went).toEqual(['/integrations/cloudflare']);
    // the arrow keys move through the rows (the GitHub link is skipped) and the address follows
    await r.act(() => cf.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true })));
    await waitFor(() => expect(r.went.at(-1)).toBe('/integrations/aikido'), r);
    await find(r, '[data-testid="integration"][data-kind="aikido"]');

    // opened from the address, and following it while mounted
    cleanup();
    const s = testServices({ ...baseClient(), integrationList: async () => [conn()] });
    s.workspace.start();
    const ctl: { set?: (k: string) => void } = {};
    function Host(handle: Handle) {
      let initial = 'notion';
      ctl.set = (k) => ((initial = k), void handle.update());
      return () => <IntegrationsScreen initial={initial} go={() => {}} open={() => {}} />;
    }
    const h = mount(<Host />, s);
    await waitFor(() => expect(h.$('[data-testid="integration"][data-kind="notion"]')).not.toBeNull(), h);
    expect(h.$('[data-key="notion"]')!.getAttribute('aria-current')).toBe('true');
    const page = h.$('[data-testid="integrations"]');
    await h.act(() => ctl.set!('sentry'));
    await waitFor(() => expect(h.$('[data-testid="integration"][data-kind="sentry"]')).not.toBeNull(), h);
    expect(h.$('[data-testid="integrations"]')).toBe(page); // the same page, not a new one
    // an address that names nothing says so
    await h.act(() => ctl.set!('nope'));
    await waitFor(() => expect(h.container.textContent).toContain('Nothing here by that name'), h);
  });

  it('Add MCP server and the Add a server row open the same dialog', async () => {
    const r = show({ integrationList: async () => [] });
    await row(r, 'linear');
    expect(r.$('dialog[open]')).toBeNull();
    await r.act(() => byText(r.container, 'button', 'Add MCP server').click());
    const dialog = await find(r, 'dialog[open]');
    expect(dialog.textContent).toContain('Add an MCP server');
    expect(dialog.querySelector('[data-testid="mcp-form"]')).not.toBeNull();
    await r.act(() => byText(dialog, 'button', 'Cancel').click());
    await waitFor(() => expect(r.$('dialog[open]')).toBeNull(), r);
    await r.act(() => (r.$('[data-testid="add-server-row"]') as HTMLElement).click());
    await waitFor(() => expect(r.$('dialog[open] [data-testid="mcp-form"]')).not.toBeNull(), r);
  });

  it('points to the MCP gateway on the Agents page for the other agent CLIs, and to the Guide', async () => {
    const r = show({ integrationList: async () => [] });
    const link = await find(r, '[data-testid="gateway-note"] a[href="/agents"]');
    expect(link.closest('p')!.textContent).toContain('MCP gateway');
    expect(r.$('header a[data-testid="learn-more"]')!.getAttribute('href')).toBe('/guide/integrations');
    expect(r.$('a[href="/library"]')).toBeNull(); // servers are not in the Library any more
  });

  it('says why when the integrations cannot be read', async () => {
    const r = show({
      integrationList: async () => {
        throw new Error('the core is not answering');
      },
    });
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('the core is not answering'), r);
  });
});

describe('Integrations: setting a service up', () => {
  it('shows three steps, the way to connect as one choice, only the field it needs, Advanced closed, and what it reads', async () => {
    const r = show({ integrationList: async () => [], mcpGallery: async () => [galleryCard({ id: 'linear', display: 'Linear', publisher: 'Linear', brand: 'linear', auth: 'oauth', integration: 'linear', tools: [] })] });
    const card = await open(r, 'linear');
    expect(card.querySelector('[data-testid="detail-status"]')!.textContent).toBe('Not connected');
    expect(Array.from(card.querySelectorAll('[data-testid="setup-steps"] li')).map((l) => l.textContent)).toEqual(['1Connect', '2Check your account', '3Assign to a project']);
    expect(card.querySelector('[data-testid="setup-steps"] li[aria-current="step"]')!.textContent).toBe('1Connect');
    const radios = Array.from(card.querySelectorAll<HTMLInputElement>('fieldset input[type="radio"]'));
    expect(radios.map((x) => [x.value, x.checked])).toEqual([
      ['api_key', true],
      ['oauth', false],
    ]);
    expect(card.querySelector('fieldset legend')!.textContent).toBe('How to connect');
    // the API key, and the address under Advanced (closed)
    expect(byLabel<HTMLInputElement>(card, 'API key').type).toBe('password');
    const adv = Array.from(card.querySelectorAll('details')).find((d) => d.querySelector('summary')!.textContent === 'Advanced')!;
    expect(adv.open).toBe(false);
    expect(adv.querySelector('label')!.textContent).toContain('API URL');
    const reads = card.querySelector('[data-testid="integration-reads"]')!;
    expect(reads.querySelectorAll('li').length).toBe(3);
    expect(reads.textContent).toContain('Only the issues the credential can see, through the Linear GraphQL API');
    expect(reads.textContent).toContain("Linear's own MCP server can be connected for agents below");
    expect(reads.querySelector('a[data-testid="learn-more"]')!.getAttribute('href')).toBe('/guide/integrations');
  });

  it('connect without a project: the key leaves the field before the request, is never rendered, and a refusal is shown verbatim', async () => {
    const msg = 'integrations: invalid request: the token must be 8 to 4096 printable characters';
    const calls: Calls = [];
    const r = show({
      integrationList: async () => [],
      integrationConnect: rec(calls, () => {
        throw new Error(msg);
      }) as never,
    });
    const card = await open(r, 'linear');
    const tok = await waitFor(() => byLabel<HTMLInputElement>(card, 'API key'), r);
    expect(tok.type).toBe('password');
    tok.value = 'lin_api_SECRET123';
    await r.act(() => byText(card, 'button', 'Connect Linear').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]![0]).toEqual({ kind: 'linear', id: undefined, token: 'lin_api_SECRET123', baseUrl: undefined });
    expect((calls[0]![0] as Record<string, unknown>).projectId).toBeUndefined();
    await waitFor(() => expect(r.container.textContent).toContain(msg), r);
    expect(tok.value).toBe('');
    expect(r.container.innerHTML).not.toContain('lin_api_SECRET123');
  });

  it('connecting switches the detail to the connection, with the test report that connecting made', async () => {
    let list = [] as ReturnType<typeof conn>[];
    const r = show({
      integrationList: async () => list,
      integrationConnect: (async () => {
        list = [conn({ assignments: [] })];
        return { connection: list[0], test: { integrationId: 'int_1', ok: true, summary: 'Linear is connected.', steps: [{ id: 'identity', name: 'Accepted by Linear', ok: true, detail: 'Kyle in Acme' }], at: 1 } };
      }) as never,
    });
    const card = await open(r, 'linear');
    byLabel<HTMLInputElement>(card, 'API key').value = 'lin_api_KEY12345';
    await r.act(() => byText(card, 'button', 'Connect Linear').click());
    await waitFor(() => expect(r.$('[data-testid="integration"][data-kind="linear"]')!.getAttribute('data-connected')).toBe('true'), r);
    const now = r.$('[data-testid="integration"][data-kind="linear"]')!;
    expect((await find(r, '[data-testid="int-report"]')).getAttribute('data-ok')).toBe('true');
    expect(now.querySelector('[data-testid="connection-identity"]')!.textContent).toContain('Kyle Maguire');
    expect(now.querySelector('[data-testid="integration-assigned"]')!.textContent).toContain('Not assigned to a project yet.');
    await waitFor(() => expect(statusOf(r.$('[data-key="linear"]')!)).toBe('On'), r);
  });

  it('a connect answer with a test report of another shape is an error on the page, not a crash later (L43)', async () => {
    const calls: Calls = [];
    const r = show({
      integrationList: async () => [],
      // TEST DOUBLE: the core's answer with a report whose steps are not a list
      integrationConnect: rec(calls, () => ({ connection: conn(), test: { integrationId: 'int_1', ok: true, summary: 'passed', steps: 'none', at: 1 } })) as never,
    });
    const card = await open(r, 'linear');
    const tok = await waitFor(() => byLabel<HTMLInputElement>(card, 'API key'), r);
    tok.value = 'lin_api_KEY12345';
    await r.act(() => byText(card, 'button', 'Connect Linear').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    await waitFor(() => expect(card.querySelector('[role="alert"]')!.textContent).toContain('test report in a shape this page can not show'), r);
    expect(r.$('[data-testid="int-report"]')).toBeNull();
    // the page still works
    expect(byText(card, 'button', 'Connect Linear')).not.toBeNull();
  });

  it('Aikido connects with its region, client id and secret; the secret is cleared', async () => {
    const calls: Calls = [];
    const r = show({ integrationList: async () => [], integrationConnect: rec(calls, () => ({ connection: conn({ kind: 'aikido', name: 'Aikido' }) })) as never });
    const card = await open(r, 'aikido');
    await r.act(() => type(byLabel<HTMLElement>(card, 'Region'), 'us'));
    await r.act(() => type(byLabel(card, 'Client ID'), 'client-1'));
    const sec = byLabel<HTMLInputElement>(card, 'Client secret');
    sec.value = 'AIK_SECRET_VALUE';
    await r.act(() => byText(card, 'button', 'Connect Aikido').click());
    await waitFor(() => expect(calls[0]![0]).toEqual({ kind: 'aikido', id: undefined, region: 'us', clientId: 'client-1', clientSecret: 'AIK_SECRET_VALUE' }), r);
    expect(sec.value).toBe('');
  });

  it('a sign-in with the device code shows the code and connects once signed in; a sign-in without an app offers the registration instead', async () => {
    const calls: Calls = [];
    let state = 'waiting';
    const r = show({
      integrationList: async () => [],
      integrationOAuthStart: async () => ({ id: 'oas_1', kind: 'sentry', method: 'device', state, url: 'https://sentry.io/oauth/device/?user_code=ABCD-EFGH', userCode: 'ABCD-EFGH', expiresAt: 0 }),
      integrationOAuthStatus: async () => ({ id: 'oas_1', kind: 'sentry', method: 'device', state, url: '', expiresAt: 0 }),
      integrationConnect: rec(calls, () => ({ connection: conn({ kind: 'sentry', name: 'Sentry' }) })) as never,
    });
    const lin = await open(r, 'linear');
    // Linear's sign-in is off (no OAuth app), but it can be chosen: it then shows where to register one, not a Sign in button
    expect((lin.querySelector('input[type="radio"][value="oauth"]') as HTMLInputElement).disabled).toBe(false);
    await r.act(() => (lin.querySelector('input[type="radio"][value="oauth"]') as HTMLInputElement).click());
    await waitFor(() => expect(lin.querySelector('[data-testid="oauth-app-setup"]')).not.toBeNull(), r);
    expect(Array.from(lin.querySelectorAll('button')).some((b) => b.textContent?.trim() === 'Sign in with Linear')).toBe(false);
    const card = await open(r, 'sentry');
    // Sentry's app comes with the build: the sign-in is on and says so
    await r.act(() => (card.querySelector('input[type="radio"][value="oauth"]') as HTMLInputElement).click());
    await waitFor(() => expect(card.querySelector('[data-testid="oauth-app-in-use"]')!.textContent).toContain('this build carries (client id sen_client_build)'), r);
    await r.act(() => byText(card, 'button', 'Sign in with Sentry (device code)').click());
    const code = await find(r, '[data-testid="oauth-user-code"]');
    expect(code.textContent).toBe('ABCD-EFGH');
    const link = (await find(r, '[data-testid="integration-oauth-link"]')) as HTMLAnchorElement;
    expect(link.target).toBe('_blank');
    state = 'signed_in';
    await waitFor(() => expect(calls.length).toBe(1), r, 4000);
    expect(calls[0]![0]).toMatchObject({ kind: 'sentry', oauthSession: 'oas_1' });
  });

  it('a slow sign-in status is asked one at a time, and the sign-in connects exactly once', async () => {
    const calls: Calls = [];
    let state = 'waiting';
    let inflight = 0;
    let most = 0;
    const r = show({
      integrationList: async () => [],
      integrationOAuthStart: async () => ({ id: 'oas_2', kind: 'sentry', method: 'device', state: 'waiting', url: 'https://sentry.io/oauth/device/?user_code=WXYZ-1234', userCode: 'WXYZ-1234', expiresAt: 0 }),
      integrationOAuthStatus: async () => {
        inflight++;
        most = Math.max(most, inflight);
        const answer = state; // what the core knew when asked
        await new Promise((res) => setTimeout(res, 2500)); // slower than the 1 s between polls
        inflight--;
        return { id: 'oas_2', kind: 'sentry', method: 'device', state: answer, url: '', expiresAt: 0 };
      },
      integrationConnect: rec(calls, () => ({ connection: conn({ kind: 'sentry', name: 'Sentry' }) })) as never,
    });
    const card = await open(r, 'sentry');
    await r.act(() => (card.querySelector('input[type="radio"][value="oauth"]') as HTMLInputElement).click());
    state = 'signed_in';
    await r.act(() => byText(card, 'button', 'Sign in with Sentry (device code)').click());
    await waitFor(() => expect(calls.length).toBe(1), r, 8000);
    await new Promise((res) => setTimeout(res, 4000)); // any overlapping poll would have answered by now
    expect(calls.length).toBe(1);
    expect(most).toBe(1);
  });

  it('a sign-in that needs an OAuth app: register it at the vendor, paste the client id, and the Sign in button appears', async () => {
    const saves: Calls = [];
    let registered = false;
    const linearKinds = () =>
      kinds.map((k) =>
        k.kind !== 'linear'
          ? k
          : {
              ...k,
              auth: (k.auth ?? []).map((m) =>
                m.id !== 'oauth' ? m : { ...m, available: registered, note: registered ? undefined : m.note, oauthApp: { ...m.oauthApp!, clientId: registered ? 'lin_client_pasted' : undefined, source: registered ? 'setting' : undefined } },
              ),
            },
      );
    const r = show({
      integrationList: async () => [],
      integrationKinds: async () => linearKinds(),
      integrationOAuthAppSet: rec(saves, (kind, clientId) => ((registered = true), { kind: kind as string, clientId: clientId as string, source: 'setting', registerUrl: 'https://linear.app/settings/api/applications/new' })) as never,
    });
    const card = await open(r, 'linear');
    await r.act(() => (card.querySelector('input[type="radio"][value="oauth"]') as HTMLInputElement).click());
    const setup = await find(r, '[data-testid="oauth-app-setup"]');
    // one plain sentence: what is needed, where to register, what to paste; the redirect the app must allow
    expect(setup.textContent).toContain('Sign in needs an OAuth app: register one at Linear and paste its client id here. A client id is not a secret');
    expect(setup.textContent).toContain('Allow the redirect URL http://127.0.0.1:41739/callback.');
    const link = setup.querySelector('[data-testid="oauth-app-register"]') as HTMLAnchorElement;
    expect(link.href).toBe('https://linear.app/settings/api/applications/new');
    expect(link.target).toBe('_blank');
    // no Sentry URL field for Linear; the client id is a plain input (not a password: it is not a secret)
    expect(Array.from(setup.querySelectorAll('label')).some((l) => l.textContent?.includes('Linear URL'))).toBe(false);
    const input = byLabel<HTMLInputElement>(setup, 'Client ID');
    expect(input.type).toBe('text');
    await r.act(() => type(input, 'lin_client_pasted'));
    await r.act(() => byText(setup, 'button', 'Save the client ID').click());
    await waitFor(() => expect(saves.length).toBe(1), r);
    expect(saves[0]).toEqual(['linear', 'lin_client_pasted', undefined]);
    // the catalog is read again: the sign-in is on, with its button, and says which app it uses
    const signIn = await waitFor(() => byText<HTMLButtonElement>(card, 'button', 'Sign in with Linear'), r);
    expect(signIn.disabled).toBe(false);
    expect(card.querySelector('[data-testid="oauth-app-setup"]')).toBeNull();
    expect(card.querySelector('[data-testid="oauth-app-in-use"]')!.textContent).toContain('you registered (client id lin_client_pasted)');
    // Change it brings the registration back, with the id filled in and a Cancel
    await r.act(() => byText(card, 'button', 'Change it').click());
    const again = await find(r, '[data-testid="oauth-app-setup"]');
    expect(byLabel<HTMLInputElement>(again, 'Client ID').value).toBe('lin_client_pasted');
    await r.act(() => byText(again, 'button', 'Cancel').click());
    await waitFor(() => expect(card.querySelector('[data-testid="oauth-app-setup"]')).toBeNull(), r);
  });

  it("Sentry's OAuth app can carry a self-hosted URL; the client id is never typed as a password", async () => {
    const saves: Calls = [];
    const r = show({
      integrationList: async () => [],
      integrationOAuthAppSet: rec(saves, () => ({ kind: 'sentry', clientId: 'sen_client_mine', baseUrl: 'https://sentry.example.com', source: 'setting', registerUrl: '' })) as never,
    });
    const card = await open(r, 'sentry');
    await r.act(() => (card.querySelector('input[type="radio"][value="oauth"]') as HTMLInputElement).click());
    await r.act(() => byText(card, 'button', 'Change it').click());
    const setup = await find(r, '[data-testid="oauth-app-setup"]');
    expect(setup.textContent).toContain('Turn on its device flow.');
    expect(setup.textContent).toContain('This build carries one already; yours replaces it.');
    await r.act(() => type(byLabel<HTMLInputElement>(setup, 'Client ID'), 'sen_client_mine'));
    await r.act(() => type(byLabel<HTMLInputElement>(setup, 'Sentry URL'), 'https://sentry.example.com'));
    await r.act(() => byText(setup, 'button', 'Save the client ID').click());
    await waitFor(() => expect(saves[0]).toEqual(['sentry', 'sen_client_mine', 'https://sentry.example.com']), r);
    expect(card.querySelector('input[type="password"]')).toBeNull();
  });

  it('Notion and Aikido say in one visible sentence why there is no Sign in button', async () => {
    const r = show({ integrationList: async () => [] });
    const notion = await open(r, 'notion');
    const why = await waitFor(() => notion.querySelector('[data-testid="integration-no-signin"]') as HTMLElement, r);
    expect(why.textContent).toBe('There is no Sign in button for Notion: its public OAuth needs a client secret at the token exchange, which a desktop app cannot keep, so Notion connects with an internal integration secret.');
    expect(notion.querySelector('input[type="radio"][value="oauth"]')).toBeNull();
    const aikido = await open(r, 'aikido');
    await waitFor(() => expect(aikido.querySelector('[data-testid="integration-no-signin"]')!.textContent).toContain('There is no Sign in button for Aikido: its API offers OAuth client credentials only'), r);
    // Linear has a Sign in (when an app is registered): no such sentence
    const linear = await open(r, 'linear');
    expect(linear.querySelector('[data-testid="integration-no-signin"]')).toBeNull();
  });

  it('GitHub (/integrations/github) offers the gh sign-in first and a Sign in with GitHub beside it; the gh state shows only for the gh way', async () => {
    const r = show({ integrationList: async () => [], githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle' }) }, 'github');
    const card = await find(r, '[data-testid="integration"][data-kind="github"]');
    await waitFor(() => expect(card.querySelector('[data-testid="gh-status"]')!.textContent).toContain('gh is signed in as kyle'), r);
    const radios = Array.from(card.querySelectorAll('input[type="radio"]')) as HTMLInputElement[];
    expect(radios.map((x) => x.value)).toEqual(['gh', 'oauth']);
    expect(radios[0]!.checked).toBe(true);
    await r.act(() => radios[1]!.click());
    const setup = await find(r, '[data-testid="oauth-app-setup"]');
    expect(setup.textContent).toContain('register one at GitHub and paste its client id here');
    expect(setup.textContent).toContain('Turn on its device flow. Scopes: repo read:org.');
    expect(card.querySelector('[data-testid="gh-status"]')).toBeNull();
    expect(Array.from(card.querySelectorAll('button')).some((b) => b.textContent?.trim() === 'Use the GitHub CLI sign-in')).toBe(false);
  });

  it('GitHub uses the gh sign-in: its state is shown and no token is asked for', async () => {
    const calls: Calls = [];
    const r = show(
      {
        integrationList: async () => [],
        githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle' }),
        integrationConnect: rec(calls, () => ({ connection: conn({ kind: 'github', name: 'GitHub' }) })) as never,
      },
      'github',
    );
    const card = await find(r, '[data-testid="integration"][data-kind="github"]');
    await waitFor(() => expect(card.querySelector('[data-testid="gh-status"]')!.textContent).toContain('gh is signed in as kyle'), r);
    // the claim is about connecting this way only: a Sign in with GitHub does keep a token (audit L45)
    expect(card.querySelector('[data-testid="gh-status"]')!.textContent).toContain('A connection made this way uses that sign-in and stores no GitHub token.');
    expect(card.querySelector('input[type="password"]')).toBeNull();
    // the detail points to the GitHub page that gathers everything about GitHub
    expect(r.$('[data-testid="integration-panel"][data-kind="github"] [data-testid="github-page-link"]')!.getAttribute('href')).toBe('/github');
    await r.act(() => byText(card, 'button', 'Use the GitHub CLI sign-in').click());
    await waitFor(() => expect(calls[0]![0]).toEqual({ kind: 'github', id: undefined }), r);
  });

  it('read-only: Connect is turned off and says why', async () => {
    const r = show({ integrationList: async () => [], licenseStatus: async () => expiredLicense });
    const card = await open(r, 'linear');
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).not.toBeNull(), r);
    expect(byText<HTMLButtonElement>(card, 'button', 'Connect Linear').disabled).toBe(true);
  });
});

describe('Integrations: a connected service', () => {
  it('shows what its API reported as rows, with Test, Reconnect and Disconnect (both in the … menu, Disconnect asks first)', async () => {
    const disc: Calls = [];
    const r = show({
      integrationList: async () => [conn()],
      integrationTest: async () => ({ integrationId: 'int_1', ok: true, steps: [{ id: 'identity', name: 'Accepted by Linear', ok: true, detail: 'Kyle in Acme' }], summary: 'Linear is connected.', at: 1 }),
      integrationDisconnect: rec(disc, () => undefined) as never,
    });
    expect((await find(r, '[data-testid="integration-row"][data-kind="linear"][data-connected="true"]')).textContent).toContain('On');
    const card = await open(r, 'linear');
    expect(card.querySelector('[data-testid="detail-status"]')!.textContent).toBe('Connected');
    expect(card.querySelector('[data-testid="integration-line"]')!.textContent).toBe('Not synced yet · personal API key');
    const id = await find(r, '[data-testid="connection-identity"]');
    expect(id.textContent).toContain('Kyle Maguire');
    expect(id.textContent).toContain('Acme');
    expect(id.textContent).toContain('read, write');
    expect(id.textContent).toContain('Personal API key');
    expect(id.textContent).toContain('stored in the credential store (never shown)');
    expect(id.textContent).toContain('Issues, as untrusted text you review');
    expect(id.textContent).toContain('Comment on the issue: each after you approve it');
    expect((id.querySelector('a') as HTMLAnchorElement).href).toBe('https://linear.app/acme');
    await r.act(() => byText(card, 'button', 'Test').click());
    const rep = await find(r, '[data-testid="int-report"]');
    expect(rep.getAttribute('data-ok')).toBe('true');
    expect(rep.textContent).toContain('Accepted by Linear: ok');
    // reconnect opens the credential form for the same connection
    await fromMenu(r, card.querySelector('[data-testid="integration-more"]') as HTMLElement, 'Reconnect');
    await button(r, card, 'Reconnect Linear');
    await r.act(() => byText(card, 'button', 'Cancel the reconnect').click());
    await waitFor(() => expect(() => byText(card, 'button', 'Reconnect Linear')).toThrow(), r);
    // disconnecting asks first
    await fromMenu(r, card.querySelector('[data-testid="integration-more"]') as HTMLElement, 'Disconnect');
    expect(disc).toEqual([]);
    expect((await find(r, '[data-testid="disconnect-confirm"]')).textContent).toContain('removes the credential, every project assignment');
    await r.act(() => byText(card, 'button', 'Yes, disconnect Linear').click());
    await waitFor(() => expect(disc).toEqual([['int_1']]), r);
  });

  it('the credential of a GitHub connection is the gh sign-in only when it was connected that way; a Sign in with GitHub keeps its token in the credential store (L45)', async () => {
    const viaGh = show({ integrationList: async () => [conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'gh', hasCredential: false, assignments: [] })] }, 'github');
    const ghCredential = (await find(viaGh, '[data-testid="connection-identity"]')).textContent!;
    expect(ghCredential).toContain('your gh sign-in (this connection stores no GitHub token)');
    expect(viaGh.$('[data-testid="integration-line"]')!.textContent).toBe('Not assigned to a project yet · your gh sign-in');
    cleanup();
    const viaOAuth = show(
      { integrationList: async () => [conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'oauth', hasCredential: true, identity: { account: 'kyle', method: 'Sign in with GitHub (device code)' }, assignments: [] })] },
      'github',
    );
    const oauthCredential = (await find(viaOAuth, '[data-testid="connection-identity"]')).textContent!;
    expect(oauthCredential).toContain('stored in the credential store (never shown)');
    expect(oauthCredential).not.toContain('your gh sign-in');
    expect(oauthCredential).not.toContain('stores no GitHub token');
  });

  it('a connection that needs you says so in its row and its header', async () => {
    const r = show({ integrationList: async () => [conn({ status: 'unauthenticated', statusDetail: 'Linear rejected the key (HTTP 401).' })] });
    const it = await row(r, 'linear');
    await waitFor(() => expect(statusOf(it)).toBe('Key refused'), r);
    const card = await open(r, 'linear');
    expect(card.querySelector('[data-testid="detail-status"]')!.textContent).toBe('Needs you');
    expect(card.querySelector('[data-testid="connection-identity"]')!.textContent).toContain('Linear rejected the key (HTTP 401).');
  });

  it("names the projects it is assigned to, each opening that project's Integrations page; Assign to a project offers the others", async () => {
    const now = Date.now();
    const r = show({
      integrationList: async () => [conn({ assignments: [asg({ sync: { state: 'ok', lastSuccessAt: now - 4 * 60_000, lastAttemptAt: 0, itemsSeen: 41, itemsLastSync: 1, consecutiveFailures: 0, resuming: false } })], legacyItems: 0 })],
      projectList: async () => [project, project2, { ...project2, id: 'prj_3', name: 'third' }] as never,
      mcpGallery: async () => [
        galleryCard({ id: 'linear', display: 'Linear', publisher: 'Linear', brand: 'linear', auth: 'oauth', integration: 'linear', tools: [], status: 'connected', libraryId: 'linear', projects: ['prj_1'] }),
      ],
    });
    await waitFor(() => expect(r.services.workspace.projectList.length).toBe(3), r);
    const card = await open(r, 'linear');
    expect(card.querySelector('[data-testid="integration-line"]')!.textContent).toBe('Synced 4 min ago · personal API key');
    const projects = await find(r, '[data-testid="integration-projects"]');
    const rows = Array.from(projects.querySelectorAll('[data-testid="integration-project"]'));
    expect(rows.length).toBe(1);
    expect(rows[0]!.textContent).toContain(project.name);
    expect(rows[0]!.textContent).toContain('Team: ENG · 41 items');
    expect(card.querySelector('[data-testid="integration-assigned"]')!.textContent).toContain('Assigned to 1 project.');
    expect(card.querySelector('[data-testid="integration-assigned"]')!.textContent).toContain('on its own Integrations page');
    // Open: that project becomes the chosen one and its Integrations page opens
    await r.act(() => byText(rows[0]!, 'button', `Open ${project.name}`).click());
    expect(r.opened).toEqual(['/project/integrations']);
    expect(r.services.workspace.project?.id).toBe(project.id);
    // Assign to a project lists the projects it is not assigned to; choosing one opens its page to choose what it imports
    await fromMenu(r, projects.querySelector('[data-testid="assign-to-project"]') as HTMLElement, 'third');
    expect(r.opened).toEqual(['/project/integrations', '/project/integrations']);
    expect(r.services.workspace.project?.id).toBe('prj_3');
    expect(Array.from(r.$$('[role="menuitem"]')).map((m) => m.textContent?.trim())).not.toContain(project.name);
    // the vendor's server is one row among the connection's, with its facts closed
    const connector = await find(r, '[data-testid="connector"][data-id="linear"]');
    expect(connector.querySelector('[data-testid="connector-projects"]')!.textContent).toContain('In 1 project.');
    expect(connector.querySelector('details > summary')!.textContent).toBe('Details');
    // nothing of a project is chosen here: no scope form, no grants, no approvals
    expect(r.$('[data-testid="assignment"]')).toBeNull();
    expect(r.$('[data-testid="assign-form"]')).toBeNull();
    expect(r.$('[data-testid="mcp-server"]')).toBeNull();
    expect(r.$('[data-testid="mcp-approvals"]')).toBeNull();
    expect(r.$('[role="combobox"][aria-label="Project"]')).toBeNull();
    for (const name of [pay.name, rev.name]) expect([name, (r.container.textContent ?? '').includes(name)]).toEqual([name, false]);
  });

  it('Sync now syncs the connection and says what it found', async () => {
    const syncs: Calls = [];
    const r = show({ integrationList: async () => [conn()], integrationSync: rec(syncs, () => ({ seen: 3, new: 2, changed: 1, gone: 0 })) as never });
    const card = await open(r, 'linear');
    await r.act(() => byText(card, 'button', 'Sync now').click());
    await waitFor(() => expect(syncs).toEqual([['int_1']]), r);
    await waitFor(() => expect(card.querySelector('[data-testid="integration-synced"]')!.textContent).toBe('3 seen: 2 new, 1 changed, 0 gone.'), r);
    // nothing to sync without a project
    cleanup();
    const none = show({ integrationList: async () => [conn({ assignments: [] })] });
    const c2 = await open(none, 'linear');
    await waitFor(() => expect(c2.querySelector('[data-testid="connection-identity"]')).not.toBeNull(), none);
    expect(() => byText(c2, 'button', 'Sync now')).toThrow();
  });

  it('Check against your account: reads the account with the stored credential, shows counts and calls, and the row says when it was verified', async () => {
    const calls: Calls = [];
    let verified: IntegrationVerifyRecord | undefined;
    const report: IntegrationVerifyReport = {
      integrationId: 'int_1',
      kind: 'linear',
      ok: true,
      at: Date.UTC(2026, 9, 15, 10),
      summary: 'Linear answered every check (3 calls, 3 items read) and nothing differed from what Drydock expects.',
      steps: [
        { id: 'credential', name: 'Credential stored', ok: true, detail: 'Personal API key, in the credential store (never shown).' },
        { id: 'identity', name: 'Who the credential is', ok: true, detail: 'Linear accepted the credential; the account is named, the workspace is named.' },
        { id: 'choices', name: 'List team choices', ok: true, detail: 'Linear listed 2 team choice(s).' },
        { id: 'read', name: 'Read issues', ok: true, detail: 'Linear returned 3 issues.' },
      ],
      calls: [
        { endpoint: 'POST /graphql (DrydockIdentity)', status: 200, ok: true },
        { endpoint: 'POST /graphql (DrydockTeams)', status: 200, ok: true },
        { endpoint: 'POST /graphql (DrydockIssues)', status: 200, ok: true },
      ],
      read: { accountNamed: true, workspaceNamed: true, scopes: 0, choices: 2, assignments: 1, items: 3, morePages: false, detailRead: false },
      mismatches: [],
      notes: [],
    };
    const r = show({
      integrationList: async () => [conn({ verified })],
      integrationVerify: rec(calls, () => {
        verified = { at: report.at, ok: true, summary: report.summary, calls: 3, failedCalls: 0, items: 3, mismatches: 0 };
        return report;
      }) as never,
    });
    const card = await open(r, 'linear');
    await waitFor(() => expect(card.querySelector('[data-testid="integration-verified"]')!.textContent).toContain('Not checked against your account yet'), r);
    await r.act(() => byText(card, 'button', 'Check against your account').click());
    await waitFor(() => expect(calls).toEqual([['int_1']]), r);
    const rep = await find(r, '[data-testid="verify-report"]');
    expect(rep.getAttribute('data-ok')).toBe('true');
    expect(rep.textContent).toContain('nothing differed from what Drydock expects');
    const read = rep.querySelector('[data-testid="verify-read"]')!.textContent!;
    expect(read).toContain('Items read3');
    expect(read).toContain('Choices listed2');
    expect(read).toContain('3 of 3 answered');
    expect(Array.from(rep.querySelectorAll('[data-testid="verify-call"]')).map((c) => c.getAttribute('data-status'))).toEqual(['200', '200', '200']);
    expect(rep.querySelectorAll('[data-testid="verify-step"][data-ok="true"]').length).toBe(4);
    // the list is read again: the detail says it was verified, the row stays On
    await waitFor(() => expect(card.querySelector('[data-testid="integration-verified"]')!.textContent).toMatch(/^Verified against your account on .*2026/), r);
    expect(statusOf(await row(r, 'linear'))).toBe('On');
    // no credential anywhere on the page
    expect(r.$('input[type="password"]')).toBeNull();
  });

  it('a check that did not pass names the refused call and its status, and what did not match; the row says Check failed', async () => {
    const refused: IntegrationVerifyReport = {
      integrationId: 'int_1',
      kind: 'linear',
      ok: false,
      at: 1,
      summary: 'Linear did not pass the check: Linear rejected the credential (HTTP 401, unauthorized).',
      steps: [
        { id: 'credential', name: 'Credential stored', ok: true, detail: 'Personal API key.' },
        { id: 'identity', name: 'Who the credential is', ok: false, detail: 'Linear rejected the credential (HTTP 401, unauthorized).' },
      ],
      calls: [{ endpoint: 'POST /graphql (DrydockIdentity)', status: 401, ok: false }],
      read: { accountNamed: false, workspaceNamed: false, scopes: 0, choices: 0, assignments: 0, items: 0, morePages: false, detailRead: false },
      mismatches: [],
      notes: [],
    };
    const drift: IntegrationVerifyReport = {
      ...refused,
      summary: 'Linear answered, but 1 thing did not match what Drydock expects.',
      steps: [
        { id: 'identity', name: 'Who the credential is', ok: true, detail: 'ok' },
        { id: 'read', name: 'Read issues', ok: false, detail: 'Linear answered in a shape Drydock does not recognize.' },
      ],
      calls: [
        { endpoint: 'POST /graphql (DrydockIdentity)', status: 200, ok: true },
        { endpoint: 'POST /graphql (DrydockIssues)', status: 200, ok: true },
      ],
      read: { ...refused.read, accountNamed: true, workspaceNamed: true },
      mismatches: ['Read issues: the field "issues.nodes" is a JSON string where Drydock expects a list'],
    };
    let next = refused;
    const r = show({
      integrationList: async () => [conn({ verified: { at: Date.UTC(2026, 9, 15), ok: false, summary: refused.summary, calls: 1, failedCalls: 1, items: 0, mismatches: 0 } })],
      integrationVerify: async () => next,
    });
    const it = await row(r, 'linear');
    await waitFor(() => expect(statusOf(it)).toBe('Check failed'), r);
    expect(r.$('[data-testid="list-filter-needs"]')!.textContent).toBe('Needs you 1');
    const card = await open(r, 'linear');
    expect(card.querySelector('[data-testid="integration-verified"]')!.textContent).toContain('did not pass: Linear did not pass the check');
    await r.act(() => byText(card, 'button', 'Check against your account').click());
    const rep = await find(r, '[data-testid="verify-report"]');
    expect(rep.getAttribute('data-ok')).toBe('false');
    expect(rep.querySelector('[data-testid="verify-read"]')).toBeNull(); // nothing was read
    expect(rep.querySelector('[data-testid="verify-step"][data-ok="false"]')!.textContent).toContain('HTTP 401');
    expect(rep.querySelector('[data-testid="verify-call"]')!.getAttribute('data-status')).toBe('401');
    next = drift;
    await r.act(() => byText(card, 'button', 'Check against your account').click());
    await waitFor(() => expect(rep.querySelector('[data-testid="verify-mismatch"]')!.textContent).toContain('"issues.nodes" is a JSON string where Drydock expects a list'), r);
    expect(rep.querySelector('[data-testid="verify-read"]')).not.toBeNull();
  });
});
