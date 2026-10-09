import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Project, SetupResult } from '../api/types.ts';
import { lic } from '../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../test/render.tsx';
import { adapterInfo, CATALOG, prebuilt } from '../screens/project/wizard-fixtures.ts';
import { Shell } from './shell.tsx';

afterEach(cleanup);

const project: Project = { id: 'p9', name: 'Payments', repoPath: '', hasRepo: false, defaultBranch: '', mergeStrategy: 'merge', mode: 'local_merge', brief: '', created: 0 };

describe('the first run', () => {
  it('keeps the wizard while the setup runs and shows its result, although the new project appears meanwhile', async () => {
    let created = false;
    let release: (r: SetupResult) => void = () => {};
    const s = testServices({
      projectList: async () => (created ? [project] : []),
      licenseStatus: async () => lic(),
      appInfo: async () => ({}) as never,
      githubStatus: async () => ({ installed: false, loggedIn: false, login: '', error: 'gh is not installed' }),
      agentCatalogList: async () => CATALOG.map((e) => prebuilt(e.id, { codex: false })),
      adapterList: async () => [adapterInfo('claude', true, ['opus', 'sonnet'])],
      agentTemplateList: async () => [],
      mcpLibList: async () => [],
      mcpCatalog: async () => [],
      projectSetup: () => new Promise<SetupResult>((res) => (release = res)),
    });
    s.workspace.start();
    const r = mount(
      <Shell url={new URL('http://localhost/')}>
        <p data-testid="route-content">the project's pages</p>
      </Shell>,
      s,
    );
    const btn = (name: string) => byText<HTMLButtonElement>(r.container, 'button', name);
    await waitFor(() => byLabel(r.container, 'Project name'), r);
    type(byLabel(r.container, 'Project name'), 'Payments');
    await r.act(() => btn('Next').click());
    await r.act(() => (Array.from(r.$$('[role="radiogroup"][aria-label="Repository source"] label')).find((l) => l.textContent!.trim() === 'Later')!.querySelector('input') as HTMLInputElement).click());
    await r.act(() => btn('Next').click());
    await r.act(() => btn('Next').click());
    await r.act(() => btn('Next').click());
    await waitFor(() => expect(r.$('form')!.getAttribute('data-step')).toBe('Review'), r);
    await r.act(() => btn('Create project').click());
    // the core creates the project first and announces it while the agents are still being added
    created = true;
    s.emit({ type: 'project.created', data: { id: 'p9' } });
    await waitFor(() => expect(s.workspace.projectList.map((p) => p.id)).toEqual(['p9']), r);
    await new Promise((res) => setTimeout(res, 50));
    r.root.flush();
    expect(r.$('[data-testid="route-content"]')).toBeNull();
    expect(r.$('form')!.getAttribute('data-step')).toBe('Review');
    release({ project, agents: [], warnings: ['connector linear: sign in on the Library page'] });
    await waitFor(() => expect(r.$('[data-testid="setup-result"]')).not.toBeNull(), r);
    expect(r.$('[data-testid="setup-warnings"]')!.textContent).toContain('sign in on the Library page');
    expect(r.$('[data-testid="route-content"]')).toBeNull();
    expect(s.workspace.setupOpen).toBe(true);
  });
});
