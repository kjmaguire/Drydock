import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { IntegrationVerifyRecord, IntegrationVerifyReport } from '../../api/extTypes.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { galleryCard, pay, rev } from '../mcp/fixtures.ts';
import { asg, conn, kinds, project, project2 } from './fixtures.ts';
import { IntegrationsScreen } from './integrations-screen.tsx';

afterEach(cleanup);

// TEST DOUBLES only: a partial client answering with the shapes internal/integrations returns. This is the workspace's page:
// connecting once. A project's assignments, items, rules and outbound writes are on the project's Integrations page
// (project-integrations-screen.test.browser.tsx).

type Calls = unknown[][];
function rec<R>(calls: Calls, result: (...args: unknown[]) => R) {
  return async (...args: unknown[]) => {
    calls.push(args);
    return result(...args);
  };
}

/** The client the merged page reads: the integrations (this file's subject) and, as empty TEST DOUBLES, the MCP side. */
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
});

function show(client: Partial<Client>, initial?: string) {
  const s = testServices({ ...baseClient(), ...client });
  s.workspace.start();
  s.project.start();
  return mount(<IntegrationsScreen initial={initial} />, s);
}
type R = ReturnType<typeof show>;
const find = (r: R, sel: string) =>
  waitFor(() => {
    const el = r.$(sel);
    if (!el) throw new Error(`no ${sel}`);
    return el as HTMLElement;
  }, r);
const button = (r: R, root: ParentNode, name: string) => waitFor(() => byText<HTMLButtonElement>(root, 'button', name), r);
const open = async (r: R, kind: string) => {
  const tile = await find(r, `[data-testid="integration-tile"][data-kind="${kind}"]`);
  await r.act(() => tile.click());
  return find(r, `[data-testid="integration"][data-kind="${kind}"]`);
};

describe('Integrations', () => {
  it('shows the five services with their fixed logos and names; clicking Linear opens Linear, which no one can rename', async () => {
    const r = show({ integrationList: async () => [], mcpGallery: async () => [galleryCard({ id: 'linear', display: 'Linear', publisher: 'Linear', brand: 'linear', auth: 'oauth', integration: 'linear', tools: [] })] });
    await waitFor(() => expect(r.container.textContent).toContain('Nothing is connected.'), r);
    const tiles = Array.from(r.$$('[data-testid="integration-tile"]')) as HTMLElement[];
    expect(tiles.map((t) => t.getAttribute('data-kind'))).toEqual(['linear', 'notion', 'sentry', 'aikido', 'github']);
    for (const t of tiles) {
      expect(t.querySelector('[data-brand]')).not.toBeNull();
      expect(t.textContent).toContain('Sync: not connected');
    }
    expect(tiles[0]!.textContent).toContain('Sync: not connected · Agents: not connected'); // Linear has an MCP card too
    expect(tiles[3]!.textContent).not.toContain('Agents:'); // Aikido has no MCP card
    expect(tiles[3]!.querySelector('[data-brand="aikido"] path')).not.toBeNull(); // Aikido's own mark, not a lettermark
    const card = await open(r, 'linear');
    expect(card.querySelector('[data-testid="integration-name"]')!.textContent).toBe('Linear');
    expect(card.querySelector('[role="img"][aria-label="Linear"]')).not.toBeNull();
    // there is nowhere to type a name
    expect(Array.from(card.querySelectorAll('label')).some((l) => /name/i.test(l.textContent ?? ''))).toBe(false);
    // the sync panel and the agents' tools of the same vendor are on one tile (decision P47)
    expect(r.$('[data-testid="integration-sync"]')!.contains(card)).toBe(true);
    const agents = await find(r, '[data-testid="integration-agents"]');
    expect(agents.querySelector('[data-testid="connector"][data-id="linear"]')).not.toBeNull();
    expect(r.$('[data-testid="mcp-link"]')).toBeNull();
    // the API it calls, and its documentation
    expect(card.querySelector('[data-testid="api-info"]')!.textContent).toContain('2,500 requests per hour');
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

  it('Aikido connects with its region, client id and secret; the secret is cleared', async () => {
    const calls: Calls = [];
    const r = show({ integrationList: async () => [], integrationConnect: rec(calls, () => ({ connection: conn({ kind: 'aikido', name: 'Aikido' }) })) as never });
    const card = await open(r, 'aikido');
    await r.act(() => type(byLabel<HTMLSelectElement>(card, 'Region'), 'us'));
    await r.act(() => type(byLabel(card, 'Client ID'), 'client-1'));
    const sec = byLabel<HTMLInputElement>(card, 'Client secret');
    sec.value = 'AIK_SECRET_VALUE';
    await r.act(() => byText(card, 'button', 'Connect Aikido').click());
    await waitFor(() => expect(calls[0]![0]).toEqual({ kind: 'aikido', id: undefined, region: 'us', clientId: 'client-1', clientSecret: 'AIK_SECRET_VALUE' }), r);
    expect(sec.value).toBe('');
  });

  it('a sign-in with the device code shows the code and connects once signed in; an unavailable sign-in cannot be picked', async () => {
    const calls: Calls = [];
    let state = 'waiting';
    const r = show({
      integrationList: async () => [],
      integrationOAuthStart: async () => ({ id: 'oas_1', kind: 'sentry', method: 'device', state, url: 'https://sentry.io/oauth/device/?user_code=ABCD-EFGH', userCode: 'ABCD-EFGH', expiresAt: 0 }),
      integrationOAuthStatus: async () => ({ id: 'oas_1', kind: 'sentry', method: 'device', state, url: '', expiresAt: 0 }),
      integrationConnect: rec(calls, () => ({ connection: conn({ kind: 'sentry', name: 'Sentry' }) })) as never,
    });
    const lin = await open(r, 'linear');
    expect((lin.querySelector('input[type="radio"][value="oauth"]') as HTMLInputElement).disabled).toBe(true);
    const card = await open(r, 'sentry');
    await r.act(() => (card.querySelector('input[type="radio"][value="oauth"]') as HTMLInputElement).click());
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

  it('GitHub uses the gh sign-in: its state is shown and no token is asked for', async () => {
    const calls: Calls = [];
    const r = show({
      integrationList: async () => [],
      githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle' }),
      integrationConnect: rec(calls, () => ({ connection: conn({ kind: 'github', name: 'GitHub' }) })) as never,
    });
    const card = await open(r, 'github');
    await waitFor(() => expect(card.querySelector('[data-testid="gh-status"]')!.textContent).toContain('gh is signed in as kyle'), r);
    expect(card.querySelector('input[type="password"]')).toBeNull();
    // the tile stays here, and points to the GitHub settings page that gathers everything about GitHub
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

  it('a connection shows what its API reported, with Test, Reconnect and Disconnect', async () => {
    const disc: Calls = [];
    const r = show({
      integrationList: async () => [conn()],
      integrationTest: async () => ({ integrationId: 'int_1', ok: true, steps: [{ id: 'identity', name: 'Accepted by Linear', ok: true, detail: 'Kyle in Acme' }], summary: 'Linear is connected.', at: 1 }),
      integrationDisconnect: rec(disc, () => undefined) as never,
    });
    const tile = await find(r, '[data-testid="integration-tile"][data-kind="linear"][data-connected="true"]');
    expect(tile.textContent).toContain('Sync: connected as Kyle Maguire');
    const card = await open(r, 'linear');
    const id = await find(r, '[data-testid="connection-identity"]');
    expect(id.textContent).toContain('Kyle Maguire');
    expect(id.textContent).toContain('Acme');
    expect(id.textContent).toContain('read, write');
    expect(id.textContent).toContain('Personal API key');
    expect(id.textContent).toContain('stored in the credential store (never shown)');
    expect((id.querySelector('a') as HTMLAnchorElement).href).toBe('https://linear.app/acme');
    await r.act(() => byText(card, 'button', 'Test').click());
    const rep = await find(r, '[data-testid="int-report"]');
    expect(rep.getAttribute('data-ok')).toBe('true');
    expect(rep.textContent).toContain('Accepted by Linear: ok');
    // reconnect opens the credential form for the same connection
    await r.act(() => byText(card, 'button', 'Reconnect').click());
    await button(r, card, 'Reconnect Linear');
    // disconnecting asks first
    await r.act(() => byText(card, 'button', 'Disconnect').click());
    expect(disc).toEqual([]);
    await r.act(() => byText(card, 'button', 'Yes, disconnect Linear').click());
    await waitFor(() => expect(disc).toEqual([['int_1']]), r);
  });

  it("carries nothing of a single project: a connection's projects are a count, assigning is on the project's own page", async () => {
    const r = show({
      integrationList: async () => [conn({ assignments: [asg(), asg({ id: 'asg_2', projectId: 'prj_2' })], legacyItems: 0 })],
      mcpGallery: async () => [
        galleryCard({ id: 'linear', display: 'Linear', publisher: 'Linear', brand: 'linear', auth: 'oauth', integration: 'linear', tools: [], status: 'connected', libraryId: 'linear', projects: ['prj_1'] }),
      ],
    });
    // wait until the workspace knows its projects, so anything naming them would have rendered by now
    await waitFor(() => expect(r.services.workspace.projectList.length).toBe(2), r);
    const card = await open(r, 'linear');
    await waitFor(() => expect(card.querySelector('[data-testid="integration-assigned"]')!.textContent).toContain('Assigned to 2 projects.'), r);
    expect(card.querySelector('[data-testid="integration-assigned"]')!.textContent).toContain("on the project's Integrations page");
    const connector = await find(r, '[data-testid="connector"][data-id="linear"]');
    expect(connector.querySelector('[data-testid="connector-projects"]')!.textContent).toContain('In 1 project.');
    const text = r.container.textContent ?? '';
    for (const name of [project.name, project2.name, pay.name, rev.name]) expect([name, text.includes(name)]).toEqual([name, false]);
    expect(r.$('[data-testid="assignment"]')).toBeNull();
    expect(r.$('[data-testid="assign-form"]')).toBeNull();
    expect(r.$('[data-testid="mcp-server"]')).toBeNull();
    expect(r.$('[data-testid="mcp-approvals"]')).toBeNull();
    for (const name of ['Assign to project', 'Add to project', 'Grant to agents']) expect(() => byText(r.container, 'button', name)).toThrow();
    expect(r.$('select[aria-label="Project"]')).toBeNull();
  });

  it('is laid out like Home: a Services group and an Agent tools group of tiles with state badges, the open tile’s panel under its own group', async () => {
    const r = show({
      integrationList: async () => [conn({ verified: { at: Date.UTC(2026, 9, 14, 9), ok: true, summary: 'Linear answered every check.', calls: 3, failedCalls: 0, items: 3, mismatches: 0 } })],
      mcpGallery: async () => [galleryCard({ id: 'cfdocs', display: 'Cloudflare Documentation', publisher: 'Cloudflare', brand: 'cloudflare', group: 'Cloudflare' })],
    });
    const services = await find(r, '[data-testid="integrations-services"]');
    const tools = await find(r, '[data-testid="integrations-tools"]');
    expect(services.querySelector('h2')!.textContent).toBe('Services');
    expect(tools.querySelector('h2')!.textContent).toBe('Agent tools');
    expect(services.querySelectorAll('[data-testid="integration-tile"]').length).toBe(5);
    expect(tools.querySelectorAll('[data-testid="integration-tile"][data-mcp="true"]').length).toBe(1);
    const badges = Array.from(services.querySelectorAll('[data-testid="tile-badge"]')).map((b) => b.textContent);
    expect(badges).toEqual(['verified', 'not connected', 'not connected', 'not connected', 'not connected']);
    // Linear's panel opens under the services, Cloudflare's under the agent tools, one at a time
    await open(r, 'linear');
    expect(services.querySelector('[data-testid="integration-panel"][data-kind="linear"]')).not.toBeNull();
    const cf = tools.querySelector('[data-testid="integration-tile"][data-kind="cloudflare"]') as HTMLElement;
    await r.act(() => cf.click());
    await waitFor(() => expect(tools.querySelector('[data-testid="integration-panel"][data-kind="cloudflare"] h3')!.textContent).toBe('Cloudflare'), r);
    expect(r.$$('[data-testid="integration-panel"]').length).toBe(1);
  });

  it('Check against your account: reads the account with the stored credential, shows counts and calls, and the card says when it was verified', async () => {
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
    // the list is read again: the card and its tile say it was verified
    await waitFor(() => expect(card.querySelector('[data-testid="integration-verified"]')!.textContent).toMatch(/^Verified against your account on .*2026/), r);
    const tile = r.$('[data-testid="integration-tile"][data-kind="linear"]') as HTMLElement;
    await waitFor(() => expect(tile.querySelector('[data-testid="tile-badge"]')!.textContent).toBe('verified'), r);
    // no credential anywhere on the page
    expect(r.$('input[type="password"]')).toBeNull();
  });

  it('a check that did not pass names the refused call and its status, and what did not match', async () => {
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
    const tile = await find(r, '[data-testid="integration-tile"][data-kind="linear"]');
    await waitFor(() => expect(tile.querySelector('[data-testid="tile-badge"]')!.textContent).toBe('check failed'), r);
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

  it('points to the MCP gateway on the Connections page for the agent CLIs', async () => {
    const r = show({ integrationList: async () => [] });
    const link = await find(r, 'p a[href="/connections"]');
    expect(link.closest('p')!.textContent).toContain('MCP gateway');
    expect(r.$('a[href="/library"]')).not.toBeNull(); // Connect servers in the Library
  });
});
