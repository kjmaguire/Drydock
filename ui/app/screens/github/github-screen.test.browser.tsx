import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RunnersView } from '../../api/runnersClient.ts';
import type { ProjectRepoRow, ProjectRepos } from '../../api/types.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { conn, kinds } from '../integrations/fixtures.ts';
import { galleryCard, projServer } from '../mcp/fixtures.ts';
import { GitHubScreen, signInState } from './github-screen.tsx';

afterEach(cleanup);

// TEST DOUBLES only: a partial client answering with the shapes the core returns for github.status, github.projectRepos,
// integration.list, integration.kinds, mcp.gallery, mcpserver.list and runners.get. Nothing here imitates gh or an agent.

const GH = galleryCard({
  id: 'gh',
  display: 'GitHub',
  publisher: 'GitHub',
  brand: 'github',
  auth: 'bearer',
  integration: 'github',
  summary: 'Pull requests, issues, Actions runs.',
  tools: [
    { name: 'get_issue', readOnlyHint: true },
    { name: 'create_issue', readOnlyHint: false },
  ],
  readOnlyTools: ['get_issue'],
});

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

const row = (over: Partial<ProjectRepoRow>): ProjectRepoRow => ({ projectId: 'prj_1', project: 'gocalc', state: 'local', openPullRequests: 0, ...over });
const repos = (rows: ProjectRepoRow[], ghInstalled = true): ProjectRepos => ({ ghInstalled, rows });
const projects = [
  { id: 'prj_1', name: 'gocalc', repoPath: '/r', hasRepo: true, defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 2 },
  { id: 'prj_2', name: 'website', repoPath: '/w', hasRepo: true, defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 1 },
];

const base = (): Partial<Client> => ({
  licenseStatus: async () => ({ configured: false, devMode: true, state: 'none', readOnly: false, message: '' }) as never,
  appInfo: async () => ({}) as never,
  projectList: async () => projects as never,
  githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle', version: '2.83.1' }),
  githubProjectRepos: async () => repos([]),
  integrationList: async () => [],
  integrationKinds: async () => kinds,
  mcpGallery: async () => [GH],
  mcpServerList: async () => [],
  skillRegistries: async () => [],
  runnersGet: async () => runners(),
});

function show(client: Partial<Client> = {}) {
  const s = testServices({ ...base(), ...client });
  s.workspace.start();
  const r = mount(<GitHubScreen />, s);
  return Object.assign(r, { s });
}
type R = ReturnType<typeof show>;
const find = (r: R, sel: string) =>
  waitFor(() => {
    const el = r.$(sel);
    if (!el) throw new Error(`no ${sel}`);
    return el as HTMLElement;
  }, r);
const text = (r: R, sel: string) => r.$(sel)?.textContent ?? '';
/** The words of an element as a person reads them: its texts, a space apart (a state and what it means are drawn apart). */
const words = (el: Element | null): string => {
  if (!el) return '';
  const out: string[] = [];
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = w.nextNode(); n; n = w.nextNode()) if (n.textContent?.trim()) out.push(n.textContent.trim());
  return out.join(' ');
};
const line = (r: R, sel: string) => words(r.$(sel));

describe('the GitHub settings page', () => {
  it('is reached from Integrations: the breadcrumb, the title, the Guide', async () => {
    const r = show();
    const h1 = await find(r, '[data-testid="github-page"] h1');
    expect(h1.textContent).toBe('GitHub');
    const crumb = r.$('nav[aria-label="Breadcrumb"] a')!;
    expect(crumb.textContent).toBe('Integrations');
    expect(crumb.getAttribute('href')).toBe('/integrations');
    // what the page leaves out is in the Guide, one link away
    const guide = r.$('[data-testid="github-page"] header a[data-testid="learn-more"]')!;
    expect(guide.getAttribute('href')).toBe('/guide/github');
    expect(guide.textContent).toBe('Guide');
    // the old stacked boxes are gone
    for (const old of ['gh-connection', 'gh-integrations-link', 'gh-runners', 'integration-tile', 'gh-about']) expect(r.$(`[data-testid="${old}"]`)).toBeNull();
    expect(r.$('a[href="/connections"]')).toBeNull();
  });

  it('says who gh is signed in as, with which version, and shows issue sync, the MCP server and the runners as chips', async () => {
    const r = show({
      integrationList: async () => [conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'gh', identity: { account: 'kyle', scopes: ['repo'] }, assignments: [] })],
      runnersGet: async () => runners({ slots: [slot('kyle/pay', 'windows', 'idle'), slot('kyle/pay', 'linux', 'paused'), slot('kyle/web', 'windows', 'busy', 'build')] }),
    });
    const hero = await find(r, '[data-testid="gh-signin-state"][data-signed-in="true"]');
    expect(hero.querySelector('[data-testid="gh-login"]')!.textContent).toBe('kyle');
    expect(hero.textContent).toContain('Signed in as @kyle');
    expect(hero.textContent).toContain('With the GitHub CLI 2.83.1 · Drydock reads no token on this route');
    // the person's avatar: initials drawn by CSS, decoration beside the name
    expect(hero.querySelector('[data-letters]')!.getAttribute('data-letters')).toBe('KY');
    await waitFor(() => expect(text(r, '[data-testid="gh-chip-runners"]')).toBe('3 runners'), r);
    await waitFor(() => expect(text(r, '[data-testid="gh-chip-sync"]')).toBe('Issue sync on'), r);
    expect(text(r, '[data-testid="gh-chip-mcp"]')).toBe('MCP for agents off');
    expect(r.$('[data-testid="gh-chip-sync"] [data-tone]')!.getAttribute('data-tone')).toBe('ok');
    // signed in: no Check again in the hero (the sign-in row has it)
    expect(Array.from(hero.querySelectorAll('button'))).toHaveLength(0);
  });

  it('lists the access as rows: the gh sign-in, issue sync with Manage, and the MCP server with its tools and grants', async () => {
    const calls: string[] = [];
    const r = show({
      integrationList: async () => [
        conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'gh', identity: { account: 'kyle', scopes: ['repo'] }, assignments: [{ ...conn().assignments![0]!, projectId: 'prj_1' }, { ...conn().assignments![0]!, id: 'asg_2', projectId: 'prj_2' }] }),
      ],
      mcpGallery: async () => [{ ...GH, status: 'connected', libraryId: 'gh', projects: ['prj_1', 'prj_2'], account: 'kyle' }],
      mcpServerList: async (projectId) => {
        calls.push(projectId);
        return projectId === 'prj_1'
          ? [projServer({ id: 'gh', agents: [{ agentId: 'a1', tools: [] }, { agentId: 'a2', tools: [] }] }), projServer({ id: 'other', agents: [{ agentId: 'a3', tools: [] }] })]
          : [projServer({ id: 'gh', projectId: 'prj_2', agents: [{ agentId: 'a4', tools: [] }] })];
      },
    });
    const access = await find(r, 'section[aria-label="Access"][data-testid="gh-access"]');
    expect(Array.from(access.querySelectorAll('[data-part="label"]')).map((x) => x.textContent)).toEqual(['GitHub CLI sign-in', 'Issue sync', 'MCP server for agents']);
    await waitFor(() => expect(line(r, '[data-testid="gh-state"]')).toBe('Signed in pushes branches, opens pull requests, reads CI'), r);
    expect(r.$('[data-testid="access-gh"] [data-part="control"] button')!.textContent).toBe('Check again');
    await waitFor(() => expect(line(r, '[data-testid="sync-state"]')).toBe('Connected as kyle uses the gh sign-in · 2 projects'), r);
    // connected: no connect form, Manage opens the connection's own panel (Test, Reconnect, Disconnect)
    expect(r.$('[data-testid="sync-connect"]')).toBeNull();
    await waitFor(() => expect(line(r, '[data-testid="mcp-state"]')).toBe('Connected Official · 2 tools · every write waits for you · granted to 3 agents'), r);
    expect(calls.sort()).toEqual(['prj_1', 'prj_2']);
    expect(text(r, '[data-testid="mcp-open"]')).toBe('Manage');

    await r.act(() => (r.$('[data-testid="sync-manage"]') as HTMLButtonElement).click());
    const dlg = await find(r, 'dialog[open]');
    expect(dlg.querySelector('h2')!.textContent).toBe('Issue sync');
    const identity = await find(r, 'dialog[open] [data-testid="integration"][data-kind="github"] [data-testid="connection-identity"]');
    expect(identity.textContent).toContain('your gh sign-in (this connection stores no GitHub token)');
    expect(dlg.textContent).toContain('Disconnect');
    await r.act(() => (dlg.querySelector('button[aria-label="Close"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('dialog[open]')).toBeNull(), r);

    // the MCP server's own card, in a dialog
    await r.act(() => (r.$('[data-testid="mcp-open"]') as HTMLButtonElement).click());
    const card = await find(r, 'dialog[open] [data-testid="connector"][data-id="gh"]');
    expect(card.getAttribute('data-status')).toBe('connected');
    expect(r.$('dialog[open] h2')!.textContent).toBe('MCP server for agents');
  });

  it('connects issue sync inline when it is not connected: the gh sign-in or the device code, the integrations screen\'s own form', async () => {
    const connected: unknown[] = [];
    let list: ReturnType<typeof conn>[] = [];
    const r = show({
      integrationList: async () => list,
      integrationConnect: async (req) => {
        connected.push(req);
        list = [conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'gh', identity: { account: 'kyle' }, assignments: [] })];
        return { connection: list[0]! } as never;
      },
    });
    await waitFor(() => expect(line(r, '[data-testid="sync-state"]')).toBe('Off a repository\'s issues into the projects you assign'), r);
    const form = await find(r, '[data-testid="access-sync"] + [data-testid="sync-connect"] form[aria-label="Connect GitHub"]');
    expect(Array.from(form.querySelectorAll('fieldset label')).map((l) => l.textContent)).toEqual(['Use the GitHub CLI sign-in', 'Sign in with GitHub (device code)']);
    // the gh sign-in it would use is said where the choice is
    await waitFor(() => expect(form.querySelector('[data-testid="gh-status"]')!.textContent).toContain('gh is signed in as kyle'), r);
    await r.act(() => byText(form, 'button', 'Use the GitHub CLI sign-in').click());
    await waitFor(() => expect(JSON.parse(JSON.stringify(connected))).toEqual([{ kind: 'github' }]), r);
    await waitFor(() => expect(text(r, '[data-testid="sync-state"]')).toContain('Connected as kyle'), r);
    expect(r.$('[data-testid="sync-connect"]')).toBeNull();
    expect(line(r, '[data-testid="mcp-state"]')).toBe('Not connected Official · 2 tools · every write waits for you');
    expect(text(r, '[data-testid="mcp-open"]')).toBe('Connect');
  });

  // audit 2026-10-16, L45: Drydock does hold a GitHub sign-in or token where a person connects the GitHub MCP server or uses Sign
  // in with GitHub, so no sentence on the page may say, without saying which route it speaks of, that it stores no token
  it('never claims, without saying which route it speaks of, that Drydock stores no GitHub token; it says where the other tokens are kept', async () => {
    const r = show({ integrationList: async () => [conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'gh', hasCredential: false, assignments: [] })] });
    await find(r, '[data-testid="sync-state"]');
    await find(r, '[data-testid="gh-signin-state"][data-signed-in="true"]');
    const page = r.$('[data-testid="github-page"]')!.textContent!;
    const claims = page.split(/(?<=[.!?·])\s+/).filter((s) => /\b(stores?|reads?( or stores?)?|keeps?)\s+(no|nothing)\b|never (reads or )?stores?/i.test(s));
    expect(claims.length).toBeGreaterThan(0);
    for (const c of claims) expect(c).toMatch(/GitHub CLI route|this route|this connection|this way|\bgh\b/i);
    for (const old of ['It never reads or stores a GitHub token', 'Never reads or stores a GitHub token', 'Drydock stores no GitHub token', 'there is nothing of GitHub']) expect(page).not.toContain(old);
    // where the other tokens are kept: issue sync by device code and the MCP server keep their own, in the credential store
    expect(text(r, '[data-testid="gh-tokens"]')).toBe('Issue sync by device code and the MCP server keep their own token in the Windows credential store, never shown.');
  });

  it('a GitHub connection made with Sign in with GitHub says its token is in the credential store, not that it is the gh sign-in', async () => {
    const r = show({
      integrationList: async () => [
        conn({ id: 'int_gh', kind: 'github', name: 'GitHub', authKind: 'oauth', hasCredential: true, identity: { account: 'kyle', scopes: ['repo'], method: 'Sign in with GitHub (device code)' }, assignments: [] }),
      ],
    });
    await waitFor(() => expect(line(r, '[data-testid="sync-state"]')).toBe('Connected as kyle Sign in with GitHub (device code) · no project yet'), r);
    await r.act(() => (r.$('[data-testid="sync-manage"]') as HTMLButtonElement).click());
    const identity = await find(r, 'dialog[open] [data-testid="connection-identity"]');
    expect(identity.textContent).toContain('stored in the credential store (never shown)');
    expect(identity.textContent).not.toContain('your gh sign-in');
    expect(identity.textContent).not.toContain('stores no GitHub token');
  });

  it("says when gh is not installed, with the fix, and Check again asks once more", async () => {
    let calls = 0;
    const r = show({
      githubStatus: async () => {
        calls++;
        return { installed: false, loggedIn: false, login: '', error: 'the GitHub CLI (gh) is not installed: install it from https://cli.github.com and run `gh auth login`' };
      },
      githubProjectRepos: async () => repos([row({ state: 'needs_gh', repo: 'kyle/gocalc', url: 'https://github.com/kyle/gocalc' })], false),
    });
    const hero = await find(r, '[data-testid="gh-signin-state"][data-signed-in="false"]');
    // the core's words are said once on the page (in the connect form, which shows them wherever it is opened): the hero gives
    // the way to install without repeating them
    expect(hero.textContent).toContain('GitHub CLI not found');
    expect(hero.textContent).toContain('Install it from cli.github.com');
    expect(hero.textContent).not.toContain('is not installed');
    await waitFor(() => expect(r.$('[data-testid="github-page"]')!.textContent).toContain('the GitHub CLI (gh) is not installed: install it from https://cli.github.com'), r);
    const page = r.$('[data-testid="github-page"]')!.textContent!;
    expect(page.match(/the GitHub CLI \(gh\) is not installed/g)?.length ?? 0).toBeLessThanOrEqual(1);
    const link = hero.querySelector('a[href="https://cli.github.com"]')!;
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.hasAttribute('data-rmx-document')).toBe(true);
    expect(hero.querySelector('[data-testid="gh-login"]')).toBeNull();
    expect(line(r, '[data-testid="gh-state"]')).toBe('Not installed pull requests and CI need it');
    // the repositories still show, needing gh; the runners say none
    await waitFor(() => expect(text(r, '[data-testid="repo-row"] [data-testid="repo-ci"]')).toBe('Needs gh'), r);
    await waitFor(() => expect(r.$('[data-testid="runner-slots"]')!.textContent).toContain('No runners yet.'), r);
    const before = calls;
    await r.act(() => byText(hero, 'button', 'Check again').click());
    await waitFor(() => expect(calls).toBe(before + 1), r);
    await r.act(() => (r.$('[data-testid="access-gh"] [data-part="control"] button') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toBe(before + 2), r);
  });

  it('tells "not signed in" from "GitHub did not answer"', async () => {
    const r = show({ githubStatus: async () => ({ installed: true, loggedIn: false, login: '', error: 'gh is not signed in: run `gh auth login`' }) });
    const hero = await find(r, '[data-testid="gh-signin-state"][data-signed-in="false"]');
    expect(hero.textContent).toContain('gh is not signed in.');
    // the core's words only repeat the heading here, so the fix is said once
    expect(hero.textContent).toContain('Run gh auth login in a terminal');
    expect(hero.textContent).not.toContain('gh is not signed in: run');
    expect(hero.textContent).toContain("gh's own credentials never pass through Drydock");
    expect(line(r, '[data-testid="gh-state"]')).toContain('Not signed in');
    cleanup();
    const u = show({ githubStatus: async () => ({ installed: true, loggedIn: false, unreachable: true, login: '', error: 'GitHub did not answer within 20 s' }) });
    const slow = await find(u, '[data-testid="gh-signin-state"][data-signed-in="false"]');
    expect(slow.textContent).toContain('GitHub did not answer in time.');
    expect(slow.textContent).toContain('gh may well be signed in');
    expect(line(u, '[data-testid="gh-state"]')).toContain('GitHub did not answer');
  });

  it('says it is checking gh, and the error when github.status fails', async () => {
    const r = show({ githubStatus: () => new Promise(() => {}) });
    await waitFor(() => expect(text(r, '[data-testid="gh-signin-state"]')).toContain('Checking gh'), r);
    cleanup();
    const bad = show({ githubStatus: async () => Promise.reject(new Error('the core is not answering')) });
    await waitFor(() => expect(text(bad, '[data-testid="gh-signin-state"] [role="alert"]')).toContain('the core is not answering'), bad);
  });
});

describe('the Repositories table', () => {
  const all = (): ProjectRepoRow[] => [
    row({ projectId: 'prj_1', project: 'gocalc', state: 'github', repo: 'kyle/gocalc', url: 'https://github.com/kyle/gocalc', defaultBranch: 'main', ci: 'passing', openPullRequests: 2, checkedAt: 1 }),
    row({ projectId: 'prj_2', project: 'website', state: 'github', repo: 'kyle/website', url: 'https://github.com/kyle/website', defaultBranch: 'trunk', ci: 'failing', openPullRequests: 1 }),
    row({ projectId: 'prj_3', project: 'busy', state: 'github', repo: 'kyle/busy', url: 'https://github.com/kyle/busy', defaultBranch: 'main', ci: 'running', openPullRequests: 0 }),
    row({ projectId: 'prj_4', project: 'quiet', state: 'github', repo: 'kyle/quiet', url: 'https://github.com/kyle/quiet', defaultBranch: 'main', ci: 'none', openPullRequests: 0 }),
    row({ projectId: 'prj_5', project: 'notes', state: 'local' }),
    row({ projectId: 'prj_6', project: 'draft', state: 'no_repo' }),
    row({ projectId: 'prj_7', project: 'broken', state: 'error', repo: 'kyle/broken', url: 'https://github.com/kyle/broken', error: 'gh: HTTP 502: Bad Gateway' }),
  ];

  it('lists each project with its repository, the CI of its default branch in words and its open pull requests; a failure is its row\'s', async () => {
    const r = show({ githubProjectRepos: async () => repos(all()) });
    await waitFor(() => expect(r.$$('[data-testid="repo-row"]').length).toBe(7), r);
    const table = r.$('section[aria-label="Repositories"] table[aria-label="Repositories"]')!;
    expect(Array.from(table.querySelectorAll('th')).map((th) => th.textContent)).toEqual(['Project', 'Repository', 'CI', 'Pull requests', 'Open']);
    const cells = (id: string) => {
      const tr = r.$(`[data-testid="repo-row"][data-project="${id}"]`)!;
      return Array.from(tr.querySelectorAll('td')).map((td) => words(td));
    };
    expect(cells('prj_1')).toEqual(['gocalc', 'kyle/gocalc', 'Passing on main', '2 open', 'Open']);
    expect(cells('prj_2')).toEqual(['website', 'kyle/website', 'Failing on trunk', '1 open', 'Open']);
    expect(cells('prj_3')).toEqual(['busy', 'kyle/busy', 'Running on main', 'None open', 'Open']);
    expect(cells('prj_4')).toEqual(['quiet', 'kyle/quiet', 'No checks on main', 'None open', 'Open']);
    expect(cells('prj_5')).toEqual(['notes', 'Local folder', 'Not on GitHub', '—', 'Open']);
    expect(cells('prj_6')).toEqual(['draft', 'No repository', 'Not on GitHub', '—', 'Open']);
    expect(cells('prj_7')).toEqual(['broken', 'kyle/broken', 'Could not read gh: HTTP 502: Bad Gateway', '—', 'Open']);
    // the state is a dot and words, the tone set from it
    const tone = (id: string) => r.$(`[data-testid="repo-row"][data-project="${id}"] [data-testid="repo-ci"] [data-tone]`)!.getAttribute('data-tone');
    expect(['prj_1', 'prj_2', 'prj_3', 'prj_4', 'prj_7'].map(tone)).toEqual(['ok', 'err', 'info', 'muted', 'err']);
    // the repository opens on github.com, the way the app opens another site
    const link = r.$('[data-testid="repo-row"][data-project="prj_1"] a[data-testid="repo-link"]')!;
    expect(link.getAttribute('href')).toBe('https://github.com/kyle/gocalc');
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.hasAttribute('data-rmx-document')).toBe(true);
    expect(r.$('[data-testid="repo-row"][data-project="prj_5"] a[data-testid="repo-link"]')).toBeNull();
    // the project's colour square, as the sidebar draws it
    expect((r.$('[data-testid="repo-row"][data-project="prj_1"] i[aria-hidden="true"]') as HTMLElement).style.background).not.toBe('');
  });

  it('Open goes to the project\'s Pull requests (its Overview without a GitHub repository) and selects the project', async () => {
    const r = show({ githubProjectRepos: async () => repos([row({ projectId: 'prj_2', project: 'website', state: 'github', repo: 'kyle/website', url: 'https://github.com/kyle/website', ci: 'passing' }), row({ projectId: 'prj_1', project: 'gocalc', state: 'local' })]) });
    await waitFor(() => expect(r.$$('[data-testid="repo-row"]').length).toBe(2), r);
    await waitFor(() => expect(r.s.workspace.projectId).toBe('prj_1'), r);
    const open = (id: string) => r.$(`[data-testid="repo-row"][data-project="${id}"] [data-testid="repo-open"]`) as HTMLAnchorElement;
    expect(open('prj_2').getAttribute('href')).toBe('/pulls');
    expect(open('prj_2').getAttribute('aria-label')).toBe('Open the pull requests of website');
    expect(open('prj_1').getAttribute('href')).toBe('/');
    expect(open('prj_1').getAttribute('aria-label')).toBe('Open gocalc');
    // the test page stays where it is; the link's own handler still runs
    r.container.addEventListener('click', (e) => e.preventDefault(), { capture: true });
    await r.act(() => open('prj_2').click());
    await waitFor(() => expect(r.s.workspace.projectId).toBe('prj_2'), r);
  });

  it('says it is reading, shows the error of the whole read, has an empty state, and Refresh asks GitHub again', async () => {
    const r = show({ githubProjectRepos: () => new Promise(() => {}) });
    await waitFor(() => expect(text(r, '[data-testid="gh-repos"]')).toContain('Reading your repositories'), r);
    cleanup();
    const bad = show({ githubProjectRepos: async () => Promise.reject(new Error('unknown method "github.projectRepos"')) });
    await waitFor(() => expect(text(bad, '[data-testid="gh-repos"] [role="alert"]')).toContain('unknown method'), bad);
    cleanup();
    const asked: boolean[] = [];
    const empty = show({ githubProjectRepos: async (refresh) => (asked.push(!!refresh), repos([])) });
    const box = await find(empty, '[data-testid="repos-empty"]');
    expect(box.textContent).toContain('No projects yet');
    expect(box.querySelector('a')!.getAttribute('href')).toBe('/project/new');
    expect(asked).toEqual([false]);
    await empty.act(() => (empty.$('[data-testid="repos-refresh"]') as HTMLButtonElement).click());
    await waitFor(() => expect(asked).toEqual([false, true]), empty);
  });

  it('reads again when a project is added', async () => {
    let n = 0;
    const r = show({ githubProjectRepos: async () => (n++, repos(n > 1 ? [row({})] : [])) });
    await find(r, '[data-testid="repos-empty"]');
    r.s.emit({ type: 'project.created', data: {} as never });
    await waitFor(() => expect(r.$$('[data-testid="repo-row"]').length).toBe(1), r);
  });
});

describe('words', () => {
  it('names the sign-in state', () => {
    expect(signInState(undefined)).toBeUndefined();
    expect(signInState({ installed: true, loggedIn: true, login: 'k' })).toEqual({ text: 'Signed in', tone: 'ok' });
    expect(signInState({ installed: false, loggedIn: false, login: '' })!.text).toBe('Not installed');
    expect(signInState({ installed: true, loggedIn: false, unreachable: true, login: '' })!.text).toBe('GitHub did not answer');
    expect(signInState({ installed: true, loggedIn: false, login: '' })!.text).toBe('Not signed in');
  });
});

function slot(repo: string, kind: 'windows' | 'linux', state: 'idle' | 'paused' | 'busy', job?: string): RunnersView['slots'][number] {
  return { key: `${repo}:${kind}`, repo, kind, enabled: state !== 'paused', state, job, jobs: 0, since: '' };
}
