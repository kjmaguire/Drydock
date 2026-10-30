import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import { DEFAULT_SKILL_DELIVERY, type CheckSettings, type DevSettings } from '../../api/extTypes.ts';
import type { Project } from '../../api/types.ts';
import { lic } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { CheckSettingsPanel, DevSettingsPanel } from './project-dev.tsx';
import { ProjectSettingsScreen } from './project-settings-screen.tsx';
import { repo } from './wizard-fixtures.ts';

afterEach(cleanup);

const project: Project = { id: 'p1', name: 'gocalc', repoPath: '/r', defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 0 };

describe('Project settings', () => {
  function settings(client: Partial<Client> = {}) {
    const s = testServices({
      projectList: async () => [project],
      licenseStatus: async () => lic(),
      appInfo: async () => ({}) as never,
      routeLevelGet: async () => ({ workspace: 'balanced', project: '', effective: 'balanced', source: 'workspace', levels: [] }),
      checksSettingsGet: async () => ({ secrets: true, deps: false, depsOffline: false, scan: false }),
      projectdevGet: async () => ({ maxParallel: 2, autoSchedule: false, respond: true, e2e: { enabled: false }, preview: { enabled: false } }),
      ...client,
    });
    s.workspace.start();
    return mount(<ProjectSettingsScreen />, s);
  }

  it('shows the project and saves mode and strategy through project.update, with the core refusal', async () => {
    const calls: unknown[][] = [];
    const r = settings({
      projectUpdate: async (...a) => {
        calls.push(a);
        throw new Error('pr_flow is not available for this project: origin is not a github.com remote');
      },
    });
    const save = await waitFor(() => byText<HTMLButtonElement>(r.container, 'button', 'Save'), r);
    expect(r.container.textContent).toContain('/r');
    expect(r.container.textContent).toContain('main');
    expect(save.disabled).toBe(true);
    expect(valueOf(byLabel(r.container, 'Mode'))).toBe('local_merge');
    type(byLabel<HTMLElement>(r.container, 'Mode'), 'pr_flow');
    type(byLabel<HTMLElement>(r.container, 'Merge strategy'), 'squash');
    r.root.flush();
    expect(save.disabled).toBe(false);
    await r.act(() => save.click());
    await waitFor(() => expect(r.container.textContent).toMatch(/pr_flow is not available/), r);
    expect(calls).toEqual([['p1', 'pr_flow', 'squash']]);
  });

  it('a project without a repository: says so, turns delivery off, and attaches a local folder once', async () => {
    const attached: [string, string][] = [];
    let lists = 0;
    const bare: Project = { ...project, repoPath: '', hasRepo: false, defaultBranch: '' };
    const r = settings({
      projectList: async () => (lists++, lists > 1 ? [{ ...project, hasRepo: true }] : [bare]),
      projectAttachRepo: async (id, path) => (attached.push([id, path]), { ...project, hasRepo: true }),
    });
    await waitFor(() => expect(r.container.querySelector('[data-testid="no-repo"]')!.textContent).toBe('No repository yet'), r);
    expect(byLabel<HTMLButtonElement>(r.container, 'Mode').disabled).toBe(true);
    expect(r.container.textContent).toContain('Attach a repository first');
    const attach = byText<HTMLButtonElement>(r.container, 'button', 'Attach repository');
    expect(attach.disabled).toBe(true);
    type(byLabel(r.container, 'Repository path'), ' /src/pay ');
    r.root.flush();
    await r.act(() => attach.click());
    await waitFor(() => expect(attached).toEqual([['p1', '/src/pay']]), r);
    // the projects are read again: the repository is there and the attach form is gone
    await waitFor(() => expect(r.container.querySelector('[data-testid="attach-repo"]')).toBeNull(), r);
    expect(r.container.querySelector('[data-testid="no-repo"]')).toBeNull();
  });

  it('makes a Git repository in a folder that is not one, then attaches it (P58)', async () => {
    const calls: string[] = [];
    const bare: Project = { ...project, repoPath: '', hasRepo: false, defaultBranch: '' };
    const r = settings({
      projectList: async () => [bare],
      repoCheck: async (path) => ({ path, state: 'folder', entries: 2, canInit: true, message: 'This folder is not a Git repository. Drydock can make one here (branch main).' }),
      repoInit: async (path) => (calls.push(`init:${path}`), { path, branch: 'main', createdFolder: false, files: 2 }),
      projectAttachRepo: async (id, path) => (calls.push(`attach:${id}:${path}`), { ...project, hasRepo: true }),
    });
    await waitFor(() => byLabel(r.container, 'Repository path'), r);
    type(byLabel(r.container, 'Repository path'), 'D:\\Projects\\app');
    await waitFor(() => expect(r.$('[data-testid="folder-check"]')!.getAttribute('data-state')).toBe('folder'), r);
    const attach = byText<HTMLButtonElement>(r.container, 'button', 'Attach repository');
    expect(attach.disabled).toBe(true);
    await r.act(() => byLabel(r.container, /^Create a Git repository here/).click());
    expect(attach.disabled).toBe(false);
    await r.act(() => attach.click());
    await waitFor(() => expect(calls).toEqual(['init:D:\\Projects\\app', 'attach:p1:D:\\Projects\\app']), r);
  });

  it('attaches a GitHub repository by cloning it with gh first', async () => {
    const calls: unknown[][] = [];
    const bare: Project = { ...project, repoPath: '', hasRepo: false, defaultBranch: '' };
    const r = settings({
      projectList: async () => [bare],
      githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle' }),
      githubOwners: async () => ['kyle'],
      githubRepos: async () => [repo('kyle/pay')],
      githubCloneDir: async () => '/home/kyle/src',
      githubClone: async (rp, dir) => (calls.push(['clone', rp, dir]), { path: '/home/kyle/src/pay', defaultBranch: 'main' }),
      projectAttachRepo: async (id, path) => (calls.push(['attach', id, path]), { ...project, hasRepo: true }),
    });
    await waitFor(() => expect(r.container.querySelector('[data-testid="attach-repo"] [role="radiogroup"]')).not.toBeNull(), r);
    const gh = Array.from(r.container.querySelectorAll<HTMLLabelElement>('[data-testid="attach-repo"] [role="radiogroup"] label')).find((l) => l.textContent!.trim() === 'GitHub')!;
    await r.act(() => gh.querySelector('input')!.click());
    const pick = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'kyle/pay'), r);
    await r.act(() => pick.click());
    await waitFor(() => expect(r.container.querySelector('[data-testid="clone-target"]')!.textContent).toBe('/home/kyle/src/pay'), r);
    await r.act(() => byText(r.container, 'button', 'Clone and attach').click());
    await waitFor(() => expect(calls).toEqual([['clone', 'kyle/pay', '/home/kyle/src'], ['attach', 'p1', '/home/kyle/src/pay']]), r);
  });

  // audit 2026-10-16, L46: init and clone cannot be done twice, so a retry after a failed last step must not repeat them
  describe('Attach a repository, retried after the last step failed', () => {
    const bare: Project = { ...project, repoPath: '', hasRepo: false, defaultBranch: '' };
    const folderCheck = async (path: string) => ({ path, state: 'folder' as const, entries: 2, canInit: true, message: 'This folder is not a Git repository. Drydock can make one here (branch main).' });
    /** A core that refuses the first attach (a transient error) and takes the next. */
    const flaky = (calls: string[], failures = 1) => {
      let left = failures;
      return async (id: string, path: string) => {
        calls.push(`attach:${id}:${path}`);
        if (left-- > 0) throw new Error('the core could not read the folder just now');
        return { ...project, hasRepo: true };
      };
    };
    const hint = (r: ReturnType<typeof settings>) => r.container.querySelector('[data-testid="attach-hint"]')!.textContent;

    it('a folder made into a Git repository is not made again: the retry attaches it, and says so', async () => {
      const calls: string[] = [];
      const r = settings({
        projectList: async () => [bare],
        repoCheck: folderCheck,
        repoInit: async (path) => (calls.push(`init:${path}`), { path, branch: 'main', createdFolder: false, files: 2 }),
        projectAttachRepo: flaky(calls),
      });
      await waitFor(() => byLabel(r.container, 'Repository path'), r);
      type(byLabel(r.container, 'Repository path'), 'D:\\Projects\\app');
      await waitFor(() => expect(r.$('[data-testid="folder-check"]')!.getAttribute('data-state')).toBe('folder'), r);
      await r.act(() => byLabel(r.container, /^Create a Git repository here/).click());
      expect(hint(r)).toBe('Makes the Git repository, then attaches it.');
      await r.act(() => byText(r.container, 'button', 'Attach repository').click());
      await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('the core could not read the folder just now'), r);
      expect(calls).toEqual(['init:D:\\Projects\\app', 'attach:p1:D:\\Projects\\app']);
      expect(hint(r)).toBe('The Git repository was already made in D:\\Projects\\app; attaching it again.');
      await r.act(() => byText(r.container, 'button', 'Attach repository').click());
      await waitFor(() => expect(calls.length).toBe(3), r);
      expect(calls).toEqual(['init:D:\\Projects\\app', 'attach:p1:D:\\Projects\\app', 'attach:p1:D:\\Projects\\app']); // one init
    });

    it('changing the folder after the failure forgets it: the new folder is made into a repository, then attached', async () => {
      const calls: string[] = [];
      const r = settings({
        projectList: async () => [bare],
        repoCheck: folderCheck,
        repoInit: async (path) => (calls.push(`init:${path}`), { path, branch: 'main', createdFolder: false, files: 2 }),
        projectAttachRepo: flaky(calls),
      });
      await waitFor(() => byLabel(r.container, 'Repository path'), r);
      type(byLabel(r.container, 'Repository path'), 'D:\\Projects\\app');
      await waitFor(() => expect(r.$('[data-testid="folder-check"]')!.getAttribute('data-state')).toBe('folder'), r);
      await r.act(() => byLabel(r.container, /^Create a Git repository here/).click());
      await r.act(() => byText(r.container, 'button', 'Attach repository').click());
      await waitFor(() => expect(r.$('[role="alert"]')).not.toBeNull(), r);
      expect(hint(r)).toContain('already made in D:\\Projects\\app');
      // another folder: nothing is remembered about the first one
      type(byLabel(r.container, 'Repository path'), 'D:\\Projects\\other');
      await waitFor(() => expect(r.$('[data-testid="folder-check"]')!.textContent).toContain('Drydock can make one here'), r);
      await waitFor(() => expect(hint(r)).toBe('Makes the Git repository, then attaches it.'), r);
      await r.act(() => byText(r.container, 'button', 'Attach repository').click());
      await waitFor(() => expect(calls).toEqual(['init:D:\\Projects\\app', 'attach:p1:D:\\Projects\\app', 'init:D:\\Projects\\other', 'attach:p1:D:\\Projects\\other']), r);
    });

    const github = (calls: string[], failures = 1) =>
      settings({
        projectList: async () => [bare],
        githubStatus: async () => ({ installed: true, loggedIn: true, login: 'kyle' }),
        githubOwners: async () => ['kyle'],
        githubRepos: async () => [repo('kyle/pay'), repo('kyle/web')],
        githubCloneDir: async () => '/home/kyle/src',
        githubClone: async (rp, dir) => (calls.push(`clone:${rp}:${dir}`), { path: `${dir}/${rp.split('/')[1]}`, defaultBranch: 'main' }),
        projectAttachRepo: flaky(calls, failures),
      });
    const chooseGitHub = async (r: ReturnType<typeof settings>, name: string) => {
      const gh = Array.from(r.container.querySelectorAll<HTMLLabelElement>('[data-testid="attach-repo"] [role="radiogroup"] label')).find((l) => l.textContent!.trim() === 'GitHub')!;
      if (!r.container.querySelector('[data-testid="repo-picker"]')) await r.act(() => gh.querySelector('input')!.click());
      const pick = await waitFor(() => byLabel<HTMLInputElement>(r.container, name), r);
      await r.act(() => pick.click());
    };

    it('a repository that was cloned is not cloned again: the retry attaches the clone, and says so', async () => {
      const calls: string[] = [];
      const r = github(calls);
      await waitFor(() => expect(r.container.querySelector('[data-testid="attach-repo"] [role="radiogroup"]')).not.toBeNull(), r);
      await chooseGitHub(r, 'kyle/pay');
      await waitFor(() => expect(r.container.querySelector('[data-testid="clone-target"]')!.textContent).toBe('/home/kyle/src/pay'), r);
      await r.act(() => byText(r.container, 'button', 'Clone and attach').click());
      await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('the core could not read the folder just now'), r);
      expect(calls).toEqual(['clone:kyle/pay:/home/kyle/src', 'attach:p1:/home/kyle/src/pay']);
      // nothing is cloned a second time, and the button no longer promises a clone
      expect(hint(r)).toBe('The repository was already cloned to /home/kyle/src/pay; attaching it again.');
      expect(() => byText(r.container, 'button', 'Clone and attach')).toThrow();
      await r.act(() => byText(r.container, 'button', 'Attach repository').click());
      await waitFor(() => expect(calls.length).toBe(3), r);
      expect(calls).toEqual(['clone:kyle/pay:/home/kyle/src', 'attach:p1:/home/kyle/src/pay', 'attach:p1:/home/kyle/src/pay']);
    });

    it('choosing another repository, or another folder to clone into, after the failure forgets the clone', async () => {
      const calls: string[] = [];
      const r = github(calls, 2);
      await waitFor(() => expect(r.container.querySelector('[data-testid="attach-repo"] [role="radiogroup"]')).not.toBeNull(), r);
      await chooseGitHub(r, 'kyle/pay');
      await waitFor(() => expect(r.container.querySelector('[data-testid="clone-target"]')!.textContent).toBe('/home/kyle/src/pay'), r);
      await r.act(() => byText(r.container, 'button', 'Clone and attach').click());
      await waitFor(() => expect(r.$('[role="alert"]')).not.toBeNull(), r);
      expect(hint(r)).toContain('already cloned to /home/kyle/src/pay');
      // another repository
      await chooseGitHub(r, 'kyle/web');
      await waitFor(() => expect(hint(r)).toBe('Clones into /home/kyle/src/web, then attaches it.'), r);
      await r.act(() => byText(r.container, 'button', 'Clone and attach').click());
      await waitFor(() => expect(calls).toEqual(['clone:kyle/pay:/home/kyle/src', 'attach:p1:/home/kyle/src/pay', 'clone:kyle/web:/home/kyle/src', 'attach:p1:/home/kyle/src/web']), r);
      await waitFor(() => expect(hint(r)).toContain('already cloned to /home/kyle/src/web'), r);
      // the same repository into another folder
      type(byLabel(r.container, 'Clone into'), '/home/kyle/work');
      await waitFor(() => expect(hint(r)).toBe('Clones into /home/kyle/work/web, then attaches it.'), r);
      await r.act(() => byText(r.container, 'button', 'Clone and attach').click());
      await waitFor(() => expect(calls.slice(4)).toEqual(['clone:kyle/web:/home/kyle/work', 'attach:p1:/home/kyle/work/web']), r);
    });
  });

  it('edits the brief with a character count and saves it through project.setBrief', async () => {
    const saved: [string, string][] = [];
    const r = settings({ projectList: async () => [{ ...project, brief: 'Old brief.' }], projectSetBrief: async (id, b) => (saved.push([id, b]), { ...project, brief: b }) });
    const box = await waitFor(() => byLabel<HTMLTextAreaElement>(r.container, 'Project brief'), r);
    await waitFor(() => expect(box.value).toBe('Old brief.'), r);
    const save = byText<HTMLButtonElement>(r.container, 'button', 'Save brief');
    expect(save.disabled).toBe(true);
    type(box, 'Keep changes small.');
    r.root.flush();
    expect(r.container.querySelector('[data-testid="brief-count"]')!.textContent).toContain('19 / 8000');
    await r.act(() => save.click());
    await waitFor(() => expect(saved).toEqual([['p1', 'Keep changes small.']]), r);
    await waitFor(() => expect(Array.from(r.container.querySelectorAll('[role="status"]')).some((e) => e.textContent === 'Saved.')).toBe(true), r);
  });

  it('shows the stored strategy, and says Saved after the core accepts', async () => {
    const r = settings({ projectList: async () => [{ ...project, mergeStrategy: 'squash' }], projectUpdate: async () => ({ ...project, mergeStrategy: 'rebase' }) });
    await waitFor(() => expect(valueOf(byLabel(r.container, 'Merge strategy'))).toBe('squash'), r);
    type(byLabel<HTMLElement>(r.container, 'Merge strategy'), 'rebase');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Save').click());
    await waitFor(() => expect(Array.from(r.container.querySelectorAll('[role="status"]')).some((e) => e.textContent === 'Saved.')).toBe(true), r);
  });
});

describe('Check settings and development flow', () => {
  const dev: DevSettings = { maxParallel: 2, autoSchedule: false, respond: true, e2e: { enabled: false }, preview: { enabled: false } };
  const mountDev = (projectdevSet: Client['projectdevSet']) => mount(<DevSettingsPanel projectId="prj_1" />, testServices({ projectdevGet: async () => dev, projectdevSet }));

  it('check settings save what the core accepts', async () => {
    const calls: [string, CheckSettings][] = [];
    const r = mount(
      <CheckSettingsPanel projectId="prj_1" />,
      testServices({ checksSettingsGet: async () => ({ secrets: true, deps: false, depsOffline: false, scan: false }), checksSettingsSet: async (p, x) => (calls.push([p, x]), x) }),
    );
    const deps = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Dependency vulnerability scan'), r);
    expect(byLabel<HTMLInputElement>(r.container, 'Offline only (no database download)').disabled).toBe(true);
    await r.act(() => deps.click());
    expect(byLabel<HTMLInputElement>(r.container, 'Offline only (no database download)').disabled).toBe(false);
    await r.act(() => byLabel<HTMLInputElement>(r.container, 'Security scan of the change').click());
    await r.act(() => byText(r.container, 'button', 'Save check settings').click());
    await waitFor(() => expect(calls).toEqual([['prj_1', { secrets: true, deps: true, depsOffline: false, scan: true }]]), r);
    await waitFor(() => expect(r.container.querySelector('[role="status"]')!.textContent).toBe('Saved.'), r);
  });

  it('development settings: parallelism, scheduler, replies, e2e and preview commands as argv lines', async () => {
    const calls: DevSettings[] = [];
    const r = mountDev(async (_p, x) => (calls.push(x), x));
    const par = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Maximum parallel agent turns'), r);
    type(par, '3');
    r.root.flush();
    await r.act(() => byLabel<HTMLInputElement>(r.container, /^Start ready tasks automatically/).click());
    await r.act(() => byLabel<HTMLInputElement>(r.container, /^Run e2e tests/).click());
    type(byLabel<HTMLTextAreaElement>(r.container, /^Command \(one argument per line; empty/), 'npx\nplaywright\ntest');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ maxParallel: 3, autoSchedule: true, respond: true, e2e: { enabled: true, command: ['npx', 'playwright', 'test'] }, preview: { enabled: false, command: [] } });
    await waitFor(() => expect(r.container.querySelector('[role="status"]')!.textContent).toBe('Saved.'), r);
  });

  it('the collision guard is on unless the human switches it off', async () => {
    const calls: DevSettings[] = [];
    const r = mountDev(async (_p, x) => (calls.push(x), x));
    const box = await waitFor(() => byLabel<HTMLInputElement>(r.container, /^Make a task wait while an earlier task/), r);
    expect(box.checked).toBe(true);
    await r.act(() => box.click());
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ collisionGuard: false });
  });

  it('workers may run the project checks only when the human switches it on, with the exposure explained', async () => {
    const calls: DevSettings[] = [];
    const r = mountDev(async (_p, x) => (calls.push(x), x));
    const box = await waitFor(() => byLabel<HTMLInputElement>(r.container, /^Let Claude workers run this project's own checks/), r);
    expect(box.checked).toBe(false);
    expect(r.container.querySelector('[data-testid="worker-checks-note"]')).toBe(null);
    await r.act(() => box.click());
    expect(r.container.querySelector('[data-testid="worker-checks-note"]')!.textContent).toMatch(/exact check commands[\s\S]*any program with your user's rights/);
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ workerChecks: true });
  });

  it('CI before merge is required by default; "no CI" is explained, saved, and then says who declared it and when', async () => {
    const calls: DevSettings[] = [];
    const at = Date.UTC(2026, 9, 9, 12, 0);
    // the core stamps the declaration with the human's identity and the time; the UI never sends its own
    const r = mountDev(async (_p, x) => (calls.push(x), x.ciMode === 'none' ? { ...x, ciNoneBy: 'human:kyle', ciNoneAt: at } : { ...x, ciNoneBy: undefined, ciNoneAt: undefined }));
    const required = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Required (default)'), r);
    const none = byLabel<HTMLInputElement>(r.container, 'This repository has no CI: merge after the review alone');
    expect(required.checked).toBe(true); // a core that sends no ciMode means required
    expect(none.checked).toBe(false);
    expect(r.$('[role="radiogroup"][aria-label="CI before merge"]')).not.toBe(null);
    expect(document.getElementById(required.getAttribute('aria-describedby')!)!.textContent).toMatch(/after CI passed on the reviewed commit[\s\S]*you are asked what to do/);
    expect(document.getElementById(none.getAttribute('aria-describedby')!)!.textContent).toMatch(/Checks that do report must still pass/);
    expect(r.$('[data-testid="ci-none-note"]')).toBe(null);

    await r.act(() => none.click());
    expect(r.$('[data-testid="ci-none-note"]')!.textContent).toBe('Not saved yet: your name and the time are recorded when you save.');
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ ciMode: 'none' });
    await waitFor(() => expect(r.$('[data-testid="ci-none-note"]')!.textContent).toContain('Declared by human:kyle on '), r);

    // back to required: the note goes, and required is what is saved
    await r.act(() => byLabel<HTMLInputElement>(r.container, 'Required (default)').click());
    expect(r.$('[data-testid="ci-none-note"]')).toBe(null);
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(calls.length).toBe(2), r);
    expect(calls[1]).toMatchObject({ ciMode: 'required' });
  });

  it('skills for Claude agents: the default is shown when the core sends none, each choice is explained and saved', async () => {
    const calls: DevSettings[] = [];
    const r = mountDev(async (_p, x) => (calls.push(x), x));
    const relevant = await waitFor(() => byLabel<HTMLInputElement>(r.container, /^Only the ones the task needs/), r);
    const all = byLabel<HTMLInputElement>(r.container, /^All of them, in every turn/);
    expect(r.$('[role="radiogroup"][aria-label="Skills for Claude agents"]')).not.toBe(null);
    expect(relevant.checked).toBe(DEFAULT_SKILL_DELIVERY === 'relevant');
    expect(all.checked).toBe(DEFAULT_SKILL_DELIVERY === 'all');
    expect((DEFAULT_SKILL_DELIVERY === 'all' ? all : relevant).parentElement!.textContent).toContain('(default)');
    expect(document.getElementById(relevant.getAttribute('aria-describedby')!)!.textContent).toMatch(/repository's own skills[\s\S]*listed and opened on request/);
    expect(document.getElementById(all.getAttribute('aria-describedby')!)!.textContent).toMatch(/Every skill goes into every turn[\s\S]*tokens on every request/);
    await r.act(() => (DEFAULT_SKILL_DELIVERY === 'all' ? relevant : all).click());
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ skillDelivery: DEFAULT_SKILL_DELIVERY === 'all' ? 'relevant' : 'all' });
  });

  it("the team lead's stall time: empty means 15, a number is saved, out of range is explained", async () => {
    const calls: DevSettings[] = [];
    const r = mountDev(async (_p, x) => (calls.push(x), x));
    const box = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Team lead: minutes without progress before it checks a task'), r);
    expect(box.value).toBe('');
    expect(box.placeholder).toBe('15');
    type(box, '2000');
    await waitFor(() => expect(r.container.querySelector('[data-testid="stall-note"]')!.textContent).toContain('1440'), r);
    type(box, '30');
    await waitFor(() => expect(r.container.querySelector('[data-testid="stall-note"]')).toBe(null), r);
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ leadStallMinutes: 30 });
  });

  it('the context ceiling: empty means the default, out of range is explained, a number is saved', async () => {
    const calls: DevSettings[] = [];
    const r = mountDev(async (_p, x) => (calls.push(x), x));
    const box = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Most tokens an agent turn starts with'), r);
    expect(box.value).toBe('');
    expect(box.placeholder).toBe('20000');
    expect(r.container.textContent).toMatch(/file excerpts, the repository map and memory are cut first; the task and instructions never are/);
    type(box, '1000');
    await waitFor(() => expect(r.container.querySelector('[data-testid="ceiling-note"]')!.textContent).toContain('4,000 and 200,000'), r);
    type(box, '12000');
    await waitFor(() => expect(r.container.querySelector('[data-testid="ceiling-note"]')).toBe(null), r);
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ contextCeiling: 12000 });
  });

  it('notes that more than five agents at once rarely pays off, and only then', async () => {
    const r = mountDev(async (_p, x) => x);
    const box = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Maximum parallel agent turns'), r);
    expect(r.container.querySelector('[data-testid="parallel-note"]')).toBe(null);
    type(box, '8');
    await waitFor(() => expect(r.container.querySelector('[data-testid="parallel-note"]')!.textContent).toContain('3 to 5'), r);
  });

  it('shows the core refusal when development settings are not saved', async () => {
    const r = mountDev(async () => {
      throw new Error('maxParallel must be between 0 and 16');
    });
    await waitFor(() => byText(r.container, 'button', 'Save development settings'), r);
    await r.act(() => byText(r.container, 'button', 'Save development settings').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('between 0 and 16'), r);
  });
});
