import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RunnersView } from '../../api/runnersClient.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { conn, kinds } from '../integrations/fixtures.ts';
import { galleryCard } from '../mcp/fixtures.ts';
import { GitHubScreen } from './github-screen.tsx';

afterEach(cleanup);

// TEST DOUBLES only: a partial client answering with the shapes the core returns for github.status, integration.list,
// mcp.gallery and runners.get. Nothing here imitates gh or an agent.

const GH = galleryCard({ id: 'gh', display: 'GitHub', publisher: 'GitHub', brand: 'github', auth: 'bearer', integration: 'github', summary: 'Pull requests, issues, Actions runs.' });

const runners = (over: Partial<RunnersView> = {}): RunnersView => ({
  kinds: [
    { kind: 'windows', available: true, where: 'on this PC', runsOn: '[self-hosted, windows, drydock]' },
    { kind: 'linux', available: false, reason: 'no container engine', where: 'in a container', runsOn: '[self-hosted, linux, drydock]' },
  ],
  devDrives: [],
  slots: [],
  minFreeBytes: 10 * 2 ** 30,
  max: 8,
  ...over,
});

const base = (): Partial<Client> => ({
  licenseStatus: async () => ({ configured: false, devMode: true, state: 'none', readOnly: false, message: '' }) as never,
  appInfo: async () => ({}) as never,
  projectList: async () => [],
  githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle' }),
  integrationList: async () => [],
  integrationKinds: async () => kinds,
  mcpGallery: async () => [GH],
  skillRegistries: async () => [],
  runnersGet: async () => runners(),
});

function show(client: Partial<Client> = {}) {
  const s = testServices({ ...base(), ...client });
  s.workspace.start();
  return mount(<GitHubScreen />, s);
}
type R = ReturnType<typeof show>;
const find = (r: R, sel: string) =>
  waitFor(() => {
    const el = r.$(sel);
    if (!el) throw new Error(`no ${sel}`);
    return el as HTMLElement;
  }, r);

describe('the GitHub settings page', () => {
  it('gathers the gh sign-in, the GitHub connection with its MCP server, and the CI runners', async () => {
    const r = show({
      integrationList: async () => [conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'gh', identity: { account: 'kyle', scopes: ['repo'] }, assignments: [] })],
      runnersGet: async () => runners({ slots: [slot('kyle/pay', 'windows', 'idle'), slot('kyle/pay', 'linux', 'paused'), slot('kyle/web', 'windows', 'busy', 'build')] }),
    });
    expect(r.$('[data-testid="github-page"] h1')!.textContent).toContain('GitHub');
    // the sign-in, as a status, naming the login and that the gh route stores no token
    const sign = await find(r, '[data-testid="gh-signin-state"][data-signed-in="true"]');
    expect(sign.querySelector('[data-testid="gh-login"]')!.textContent).toBe('kyle');
    expect(sign.textContent).toContain('gh is signed in as');
    expect(r.$('[data-testid="gh-signin"]')!.textContent).toContain('On this route Drydock reads and stores no GitHub token');
    // what it does is in the Guide, one link away (the page keeps to the basics)
    expect(r.$('[data-testid="gh-about"]')).toBeNull();
    expect(r.$('[data-testid="github-page"] a[data-testid="learn-more"]')!.getAttribute('href')).toBe('/guide/github');
    // the GitHub tile's own panel: app sync (connected, as the tile shows it) and the gh MCP card; no other vendor
    const panel = await find(r, '[data-testid="gh-connection"] [data-testid="integration-panel"][data-kind="github"]');
    expect(panel.querySelector('[data-testid="integration"][data-kind="github"]')).not.toBeNull();
    expect(panel.querySelector('[data-testid="connection-identity"]')!.textContent).toContain('your gh sign-in (this connection stores no GitHub token)');
    expect(panel.querySelector('[data-testid="connector"][data-id="gh"]')).not.toBeNull();
    expect(r.$$('[data-testid="integration-panel"]')).toHaveLength(1);
    expect(r.$('[data-testid="integration-tile"]')).toBeNull();
    expect(r.$('[data-testid="gh-integrations-link"]')!.getAttribute('href')).toBe('/integrations');
    // the CI runners are managed here, after the connection (they moved from Connections): each runner with its state in words,
    // and the form that adds one
    const section = await find(r, '[data-testid="github-page"] [data-testid="ci-runners-section"]');
    expect(r.$('[data-testid="gh-connection"]')!.compareDocumentPosition(section) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    await waitFor(() => expect(section.querySelectorAll('[data-testid="runner-slot"]')).toHaveLength(3), r);
    const states = Array.from(section.querySelectorAll('[data-testid="runner-slot"]')).map((x) => [x.getAttribute('data-key'), x.querySelector('[data-testid="runner-state"]')!.textContent]);
    expect(states).toEqual([
      ['kyle/pay:windows', 'waiting for a job'],
      ['kyle/pay:linux', 'off'],
      ['kyle/web:windows', 'running build'],
    ]);
    expect(section.querySelector('[data-testid="runner-private-only"]')!.textContent).toContain('Private repositories only');
    expect(section.querySelector('[data-testid="runner-add"] [data-testid="runner-add-button"]')).not.toBeNull();
    expect(section.querySelector('a[data-testid="learn-more"]')!.getAttribute('href')).toBe('/guide/runners');
    // the old summary that pointed to Connections is gone
    expect(r.$('[data-testid="gh-runners"]')).toBeNull();
    expect(r.$('a[href="/connections"]')).toBeNull();
  });

  // audit 2026-10-16, L45: Drydock does hold a GitHub sign-in or token where a person connects the GitHub MCP server or uses Sign
  // in with GitHub, so no sentence on the page may say, without saying which route it speaks of, that it stores no token
  it('never claims, without saying which route it speaks of, that Drydock stores no GitHub token; it says where a sign-in or token is kept and removed', async () => {
    const r = show({ integrationList: async () => [conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'gh', hasCredential: false, assignments: [] })] });
    await find(r, '[data-testid="gh-connection"] [data-testid="connection-identity"]');
    const page = r.$('[data-testid="github-page"]')!.textContent!;
    const claims = page.split(/(?<=[.!?])\s+/).filter((s) => /\b(stores?|reads or stores?|keeps?)\s+(no|nothing)\b|never (reads or )?stores?/i.test(s));
    expect(claims.length).toBeGreaterThan(0);
    for (const c of claims) expect(c).toMatch(/GitHub CLI route|this route|this connection|this way|\bgh\b/i);
    for (const old of ['It never reads or stores a GitHub token', 'Never reads or stores a GitHub token', 'Drydock stores no GitHub token', 'there is nothing of GitHub']) expect(page).not.toContain(old);
    // where the other tokens are kept: a connection or MCP server below keeps its own, in the credential store (the Guide says which and how to remove them)
    const signin = r.$('[data-testid="gh-signin"]')!.textContent!;
    expect(signin).toContain('a connection or MCP server below keeps its own');
    expect(signin).toContain('Windows credential store, never shown');
  });

  it('a GitHub connection made with Sign in with GitHub says its token is in the credential store, not that it is the gh sign-in', async () => {
    const r = show({
      integrationList: async () => [
        conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'oauth', hasCredential: true, identity: { account: 'kyle', scopes: ['repo'], method: 'Sign in with GitHub (device code)' }, assignments: [] }),
      ],
    });
    const identity = await find(r, '[data-testid="gh-connection"] [data-testid="connection-identity"]');
    expect(identity.textContent).toContain('stored in the credential store (never shown)');
    expect(identity.textContent).not.toContain('your gh sign-in');
    expect(identity.textContent).not.toContain('stores no GitHub token');
  });

  it('says in the core\'s words when gh is not installed, and Check again asks once more', async () => {
    let calls = 0;
    const r = show({
      githubStatus: async () => {
        calls++;
        return { installed: false, loggedIn: false, login: '', error: 'the GitHub CLI (gh) is not installed: install it from https://cli.github.com and run `gh auth login`' };
      },
    });
    const sign = await find(r, '[data-testid="gh-signin-state"][data-signed-in="false"]');
    // the core's words are said once on the page (in the connection panel, which shows them wherever it is opened): the sign-in
    // notice gives the way to install without repeating them
    expect(sign.textContent).toContain('GitHub CLI not found');
    expect(sign.textContent).toContain('Install it from');
    expect(sign.textContent).not.toContain('is not installed');
    const page = r.$('[data-testid="github-page"]')!.textContent!;
    await waitFor(() => expect(r.$('[data-testid="github-page"]')!.textContent).toContain('the GitHub CLI (gh) is not installed: install it from https://cli.github.com'), r);
    expect(page.match(/the GitHub CLI \(gh\) is not installed/g)?.length ?? 0).toBeLessThanOrEqual(1);
    expect(sign.querySelector('a[href="https://cli.github.com"]')!.getAttribute('target')).toBe('_blank');
    expect(sign.querySelector('[data-testid="gh-login"]')).toBeNull();
    // the connection panel still offers the connection (its connect form asks gh too), and the runners say none
    await find(r, '[data-testid="integration"][data-kind="github"]');
    await waitFor(() => expect(r.$('[data-testid="runner-slots"]')!.textContent).toContain('No runners yet.'), r);
    const before = calls;
    await r.act(() => byText(r.container, 'button', 'Check again').click());
    await waitFor(() => expect(calls).toBe(before + 1), r);
  });

  it('tells "not signed in" from "GitHub did not answer"', async () => {
    const r = show({ githubStatus: async () => ({ installed: true, loggedIn: false, login: '', error: 'gh is not signed in: run `gh auth login`' }) });
    const sign = await find(r, '[data-testid="gh-signin-state"][data-signed-in="false"]');
    expect(sign.textContent).toContain('gh is not signed in.');
    expect(sign.textContent).toContain('run `gh auth login`');
    expect(sign.textContent).toContain("gh's own credentials never pass through it");
    cleanup();
    const u = show({ githubStatus: async () => ({ installed: true, loggedIn: false, unreachable: true, login: '', error: 'GitHub did not answer within 20 s' }) });
    const slow = await find(u, '[data-testid="gh-signin-state"][data-signed-in="false"]');
    expect(slow.textContent).toContain('GitHub did not answer in time.');
    expect(slow.textContent).toContain('gh may well be signed in');
  });
});

function slot(repo: string, kind: 'windows' | 'linux', state: 'idle' | 'paused' | 'busy', job?: string): RunnersView['slots'][number] {
  return { key: `${repo}:${kind}`, repo, kind, enabled: state !== 'paused', state, job, jobs: 0, since: '' };
}
