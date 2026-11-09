import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Project, SetupResult } from '../api/types.ts';
import { lic } from '../test/fixtures.ts';
import { byText, cleanup, drain, mount, testServices, type, waitFor } from '../test/render.tsx';
import { adapterInfo, CATALOG, prebuilt } from '../screens/project/wizard-fixtures.ts';
import { Shell } from './shell.tsx';

afterEach(cleanup);

const project: Project = { id: 'p9', name: 'Payments', repoPath: '', hasRepo: false, defaultBranch: '', mergeStrategy: 'merge', mode: 'local_merge', brief: '', created: 0 };

describe('the first run', () => {
  it('shows the start screen, not the wizard, and keeps it while a start makes the project and its first work (P142)', async () => {
    let created = false;
    let release: (r: SetupResult) => void = () => {};
    const navigated: string[] = [];
    const s = testServices({
      projectList: async () => (created ? [project] : []),
      licenseStatus: async () => lic(),
      appInfo: async () => ({}) as never,
      needsYouList: async () => [],
      taskSummaries: async () => ({ summaries: [], total: 0, partial: false }),
      securityScanRunning: async () => [],
      githubCloneDir: async () => '/home/me/source/repos',
      agentCatalogList: async () => CATALOG.map((e) => prebuilt(e.id, { codex: false })),
      adapterList: async () => [adapterInfo('claude', true, ['opus', 'sonnet'])],
      agentTemplateList: async () => [],
      projectSetup: () => new Promise<SetupResult>((res) => (release = res)),
      chatCreate: async () => ({ id: 'c1' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    s.workspace.start();
    const r = mount(
      <Shell url={new URL('http://localhost/')} onNavigate={(h) => navigated.push(h)}>
        <p data-testid="route-content">the project's pages</p>
      </Shell>,
      s,
    );
    await waitFor(() => expect(r.$('[data-testid="start-text"]')).not.toBeNull(), r);
    const box = r.$('[data-testid="start-text"]') as HTMLTextAreaElement;
    expect(r.$('h1')!.textContent).toBe('What do you want to build?');
    expect(r.$('form[data-step]')).toBeNull();
    await r.act(() => type(box, 'Payments API'));
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', /^Start/).click());
    // the core creates the project first and announces it while the work is still being made
    created = true;
    s.emit({ type: 'project.created', data: { id: 'p9' } });
    await waitFor(() => expect(s.workspace.projectList.map((p) => p.id)).toEqual(['p9']), r);
    await drain();
    r.root.flush();
    expect(r.$('[data-testid="route-content"]')).toBeNull();
    expect(r.$('[data-testid="start-error"]')?.textContent ?? '').toBe('');
    expect(r.$('[data-testid="start-progress"]')!.textContent).toBe('Creating project…');
    release({ project, agents: [], warnings: [] });
    await waitFor(() => expect(navigated).toEqual(['/?item=chat:c1']), r);
    expect(s.workspace.setupOpen).toBe(false);
    await waitFor(() => expect(r.$('[data-testid="route-content"]')).not.toBeNull(), r);
  });
});
