import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { CheckSettings, DevSettings } from '../../api/extTypes.ts';
import type { LicenseView } from '../../api/pkgTypes.ts';
import type { Project } from '../../api/types.ts';
import { expiredLicense, lic, unconfiguredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { CheckSettingsPanel, DevSettingsPanel } from './project-dev.tsx';
import { ProjectCreateScreen } from './project-create-screen.tsx';
import { ProjectSettingsScreen } from './project-settings-screen.tsx';

afterEach(cleanup);

const project: Project = { id: 'p1', name: 'gocalc', repoPath: '/r', defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: 0 };

function services(view: LicenseView, client: Partial<Client> = {}) {
  const s = testServices({ projectList: async () => [], licenseStatus: async () => view, appInfo: async () => ({}) as never, ...client });
  s.workspace.start();
  return s;
}

describe('Project create: license and the core refusal', () => {
  it('disables nothing when licensing is not configured', async () => {
    const r = mount(<ProjectCreateScreen />, services(unconfiguredLicense));
    await waitFor(() => expect(r.services.workspace.license.view).toBeTruthy(), r);
    type(byLabel(r.container, 'Repository path'), 'C:\\r');
    r.root.flush();
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Create project').disabled).toBe(false);
    expect(r.container.querySelector('[data-testid="readonly-note"]')).toBe(null);
    expect(r.$('h1')!.textContent).toContain('New project');
  });

  it('is turned off when expired, with the core reason as the title and a way to License', async () => {
    const r = mount(<ProjectCreateScreen />, services(expiredLicense));
    const note = await waitFor(() => {
      const n = r.container.querySelector('[data-testid="readonly-note"]');
      if (!n) throw new Error('no note');
      return n;
    }, r);
    type(byLabel(r.container, 'Repository path'), 'C:\\r');
    r.root.flush();
    const btn = byText<HTMLButtonElement>(r.container, 'button', 'Create project');
    expect(btn.disabled).toBe(true);
    expect(btn.title).toBe(expiredLicense.banner!);
    expect(note.textContent).toContain('Open License');
  });

  it('a grace period disables nothing', async () => {
    const r = mount(<ProjectCreateScreen />, services(lic({ state: 'grace', daysLeft: 3, banner: 'License expired; full functionality for 3 more grace day(s).' })));
    await waitFor(() => expect(r.services.workspace.license.view).toBeTruthy(), r);
    type(byLabel(r.container, 'Repository path'), 'C:\\r');
    r.root.flush();
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Create project').disabled).toBe(false);
  });

  it('shows the core refusal text verbatim when a mutating call is still refused', async () => {
    const refusal = 'Your license has expired, so Drydock is read-only: creating projects is turned off.';
    const r = mount(
      <ProjectCreateScreen />,
      services(lic(), {
        projectCreate: async () => {
          throw new Error(refusal);
        },
      }),
    );
    type(byLabel(r.container, 'Repository path'), 'C:\\r');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Create project').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain(refusal), r);
  });

  it('creates the project from the trimmed path and name, selects it, re-reads the projects and goes home', async () => {
    const calls: [string, string][] = [];
    let lists = 0;
    const navigated: string[] = [];
    const s = services(lic(), {
      projectCreate: async (name, repo) => (calls.push([name, repo]), { ...project, id: 'p9', name: 'mine' }),
      projectList: async () => (lists++, lists > 1 ? [project, { ...project, id: 'p9', name: 'mine' }] : []),
    });
    const r = mount(<ProjectCreateScreen first onNavigate={(h) => navigated.push(h)} />, s);
    expect(r.$('h1')!.textContent).toContain('Welcome to Drydock');
    expect(r.container.textContent).toContain('merges only after you approve');
    const create = byText<HTMLButtonElement>(r.container, 'button', 'Create project');
    expect(create.disabled).toBe(true); // a path is required
    type(byLabel(r.container, 'Repository path'), '  C:\\src\\repo ');
    type(byLabel(r.container, 'Project name (optional)'), ' mine ');
    r.root.flush();
    await r.act(() => create.click());
    await waitFor(() => expect(navigated).toEqual(['/']), r);
    expect(calls).toEqual([['mine', 'C:\\src\\repo']]);
    await waitFor(() => expect(s.workspace.projectId).toBe('p9'), r);
  });
});

describe('Project settings', () => {
  function settings(client: Partial<Client> = {}) {
    const s = testServices({
      projectList: async () => [project],
      licenseStatus: async () => lic(),
      appInfo: async () => ({}) as never,
      checksSettingsGet: async () => ({ secrets: true, deps: false, depsOffline: false }),
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
    expect(byLabel<HTMLSelectElement>(r.container, 'Mode').value).toBe('local_merge');
    type(byLabel<HTMLSelectElement>(r.container, 'Mode'), 'pr_flow');
    type(byLabel<HTMLSelectElement>(r.container, 'Merge strategy'), 'squash');
    r.root.flush();
    expect(save.disabled).toBe(false);
    await r.act(() => save.click());
    await waitFor(() => expect(r.container.textContent).toMatch(/pr_flow is not available/), r);
    expect(calls).toEqual([['p1', 'pr_flow', 'squash']]);
  });

  it('shows the stored strategy, and says Saved after the core accepts', async () => {
    const r = settings({ projectList: async () => [{ ...project, mergeStrategy: 'squash' }], projectUpdate: async () => ({ ...project, mergeStrategy: 'rebase' }) });
    await waitFor(() => expect(byLabel<HTMLSelectElement>(r.container, 'Merge strategy').value).toBe('squash'), r);
    type(byLabel<HTMLSelectElement>(r.container, 'Merge strategy'), 'rebase');
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
      testServices({ checksSettingsGet: async () => ({ secrets: true, deps: false, depsOffline: false }), checksSettingsSet: async (p, x) => (calls.push([p, x]), x) }),
    );
    const deps = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Dependency vulnerability scan'), r);
    expect(byLabel<HTMLInputElement>(r.container, 'Offline only (no database download)').disabled).toBe(true);
    await r.act(() => deps.click());
    expect(byLabel<HTMLInputElement>(r.container, 'Offline only (no database download)').disabled).toBe(false);
    await r.act(() => byText(r.container, 'button', 'Save check settings').click());
    await waitFor(() => expect(calls).toEqual([['prj_1', { secrets: true, deps: true, depsOffline: false }]]), r);
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
