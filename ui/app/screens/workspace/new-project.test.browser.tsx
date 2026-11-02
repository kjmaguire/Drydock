import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Project, SetupRequest } from '../../api/types.ts';
import { cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { adapterInfo, CATALOG } from '../project/wizard-fixtures.ts';
import { WorkspaceHomeScreen } from './workspace-home.tsx';

afterEach(() => {
  cleanup();
  window.history.replaceState(window.history.state, '', '/');
});

const project = (id: string, name: string, over: Partial<Project> = {}): Project =>
  ({ id, name, repoPath: `/src/${name}`, hasRepo: true, defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: Date.now(), ...over }) as Project;

// TEST DOUBLES only: a partial client with no projects yet, the catalog and one CLI. Starting work would call these: the test
// records any call, and New project must make none.
function show(client: Partial<Client> = {}) {
  const started: string[] = [];
  const setups: SetupRequest[] = [];
  const s = testServices({
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    projectList: async () => [],
    needsYouList: async () => [],
    taskSummaries: async () => ({ summaries: [], total: 0, partial: false }),
    securityScanRunning: async () => [],
    agentCatalogList: async () => CATALOG,
    adapterList: async () => [adapterInfo('claude', true, ['opus', 'sonnet', 'haiku'])],
    // the usual code folder when asked for it; the last clone went elsewhere (a new folder must not follow it)
    githubCloneDir: async (usual?: boolean) => (usual ? '/home/me/source/repos' : '/d/scratch'),
    agentTemplateList: async () => [],
    agentList: async () => [],
    projectSetup: async (req) => (setups.push(req), { project: project('p9', req.name), agents: [], warnings: [] }),
    taskCreate: async () => (started.push('task'), {}) as never,
    taskStart: async () => (started.push('start'), '') as never,
    chatCreate: async () => (started.push('chat'), {}) as never,
    chatSend: async () => (started.push('send'), {}) as never,
    ...client,
  });
  s.workspace.start();
  const navigated: string[] = [];
  const chosen: string[] = [];
  s.workspace.selectProject = (id: string) => void chosen.push(id);
  const r = mount(<WorkspaceHomeScreen onNavigate={(h) => navigated.push(h)} />, s);
  return { r, s, navigated, chosen, started, setups };
}

const $ = <T extends Element = HTMLElement>(r: { $: (s: string) => Element | null }, sel: string) => r.$(sel) as T | null;
const dialog = (r: { $: (s: string) => Element | null }) => $<HTMLDialogElement>(r, '[data-testid="new-project-dialog"]');
const create = (r: { $: (s: string) => Element | null }) => $<HTMLButtonElement>(r, '[data-testid="new-project-create"]')!;
const where = (r: { $: (s: string) => Element | null }, v: string) => $<HTMLButtonElement>(r, `[data-testid="new-project-where"] [data-value="${v}"]`)!;

describe('New project on Home (P143)', () => {
  it('a New project button beside the heading opens the dialog: a name and a new folder; Create makes the project, starts nothing and lands on Work', async () => {
    const { r, navigated, chosen, started, setups } = show();
    const btn = await waitFor(() => {
      const b = $<HTMLButtonElement>(r, '[data-testid="home-new-project"]');
      if (!b) throw new Error('no New project');
      return b;
    }, r);
    expect(btn.textContent!.trim()).toBe('New project');
    expect(btn.closest('.top')!.querySelector('h1')!.textContent).toBe('What do you want to build?');
    expect(dialog(r)!.open).toBe(false);
    await r.act(() => btn.click());
    await waitFor(() => expect(dialog(r)!.open).toBe(true), r);
    expect(where(r, 'new').getAttribute('aria-checked')).toBe('true');
    expect(Array.from(r.$$('[data-testid="new-project-where"] [role="radio"]')).map((b) => b.textContent)).toEqual(['New folder', 'Open a folder', 'Clone from GitHub']);
    expect(create(r).disabled).toBe(true);
    await r.act(() => type($(r, '[data-testid="new-project-name"]')!, 'Pay Portal'));
    await waitFor(() => expect($<HTMLInputElement>(r, '[data-testid="new-project-folder"]')!.value).toBe('/home/me/source/repos/pay-portal'), r);
    await waitFor(() => expect(create(r).disabled).toBe(false), r);
    await r.act(() => create(r).click());
    await waitFor(() => expect(navigated).toEqual(['/']), r);
    expect(setups).toHaveLength(1);
    expect(setups[0]!.name).toBe('Pay Portal');
    expect(setups[0]!.brief).toBe('');
    expect(setups[0]!.repo).toEqual({ kind: 'local', path: '/home/me/source/repos/pay-portal', init: true });
    expect(setups[0]!.agents.map((a) => a.prebuilt)).toEqual(['backend-engineer']);
    expect(setups[0]!.mode).toBe('local_merge');
    expect(started).toEqual([]);
    expect(chosen).toEqual(['p9']);
    await waitFor(() => expect(dialog(r)!.open).toBe(false), r);
  });

  it('Open a folder: the name follows the folder until one is typed', async () => {
    const { r, setups } = show({
      repoCheck: async (path: string) => ({ path, state: 'repository', message: 'A Git repository on branch main.', canInit: false }) as never,
    });
    await waitFor(() => expect($(r, '[data-testid="home-new-project"]')).not.toBeNull(), r);
    await r.act(() => $(r, '[data-testid="home-new-project"]')!.click());
    await r.act(() => where(r, 'folder').click());
    const field = await waitFor(() => {
      const f = r.$('[data-testid="folder-field"] input') as HTMLInputElement | null;
      if (!f) throw new Error('no folder field');
      return f;
    }, r);
    await r.act(() => type(field, '/src/gocalc'));
    await waitFor(() => expect($<HTMLInputElement>(r, '[data-testid="new-project-name"]')!.value).toBe('gocalc'), r);
    await waitFor(() => expect(r.$('[data-testid="folder-check"]')!.getAttribute('data-state')).toBe('repository'), r, 3000);
    await r.act(() => type($(r, '[data-testid="new-project-name"]')!, 'calculator'));
    await waitFor(() => expect(create(r).disabled).toBe(false), r);
    await r.act(() => create(r).click());
    await waitFor(() => expect(setups).toHaveLength(1), r);
    expect(setups[0]!.name).toBe('calculator');
    expect(setups[0]!.repo).toEqual({ kind: 'local', path: '/src/gocalc' });
  });

  it('/home?new=project&where=clone opens it on Clone from GitHub (the old wizard address leads here)', async () => {
    window.history.replaceState(window.history.state, '', '/home?new=project&where=clone');
    const { r } = show({ githubStatus: async () => ({ installed: false, loggedIn: false, login: '', error: 'the GitHub CLI (gh) is not installed' }) });
    await waitFor(() => expect(dialog(r)!.open).toBe(true), r);
    expect(where(r, 'clone').getAttribute('aria-checked')).toBe('true');
    await waitFor(() => expect(r.$('[data-testid="github-unavailable"]')!.textContent).toContain('Choose New folder or Open a folder instead.'), r);
    // the address is put back: Reload does not open it again
    expect(window.location.search).toBe('');
  });

  it('an error stays in the dialog; Cancel closes it; read-only turns Create off', async () => {
    const { r, navigated } = show({
      projectSetup: async () => {
        throw new Error('the folder is inside another repository');
      },
    });
    await waitFor(() => expect($(r, '[data-testid="home-new-project"]')).not.toBeNull(), r);
    await r.act(() => $(r, '[data-testid="home-new-project"]')!.click());
    await r.act(() => type($(r, '[data-testid="new-project-name"]')!, 'x'));
    await waitFor(() => expect(create(r).disabled).toBe(false), r);
    await r.act(() => create(r).click());
    await waitFor(() => expect(r.$('[data-testid="new-project-dialog"] [role="alert"]')!.textContent).toContain('inside another repository'), r);
    expect(navigated).toEqual([]);
    const cancel = Array.from(r.$$('[data-testid="new-project-dialog"] button')).find((b) => b.textContent === 'Cancel') as HTMLButtonElement;
    await r.act(() => cancel.click());
    await waitFor(() => expect(dialog(r)!.open).toBe(false), r);
    cleanup();

    const ro = show({ licenseStatus: async () => ({ configured: true, state: 'none', readOnly: true, reason: 'No valid license' }) as never });
    await waitFor(() => expect($(ro.r, '[data-testid="home-new-project"]')).not.toBeNull(), ro.r);
    await ro.r.act(() => $(ro.r, '[data-testid="home-new-project"]')!.click());
    await ro.r.act(() => type($(ro.r, '[data-testid="new-project-name"]')!, 'x'));
    await waitFor(() => expect(ro.r.$('[data-testid="readonly-note"]')).not.toBeNull(), ro.r);
    expect(create(ro.r).disabled).toBe(true);
  });
});
