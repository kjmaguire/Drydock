import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ExportManifest, ExportPreview, ImportPreview, ImportResult, ImportStats } from '../../api/pkgTypes.ts';
import type { Project } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { DataScreen } from './data-screen.tsx';

afterEach(cleanup);

const manifest = (over: Partial<ExportManifest> = {}): ExportManifest => ({
  format: 1,
  kind: 'drydock-project',
  appVersion: '0.0.0-dev',
  schemaVersion: 30,
  projectId: 'prj_1',
  projectName: 'Alpha',
  created: 1760000000000,
  includesRuns: false,
  files: [
    { path: 'drydock.db', size: 4096, sha256: 'a' },
    { path: 'manifest.json', size: 100, sha256: 'b' },
  ],
  tables: { tasks: 3 },
  scan: { dbRedactions: 2, dbByRule: { 'generic-api-key': 2 }, runsFilesScanned: 0, runsRedactions: 0 },
  neverIncluded: ['secrets and Credential Manager entries', 'the repository', 'worktree contents'],
  ...over,
});
const proj = (id: string, name: string): Project => ({ id, name }) as Project;
const pending = (over: Partial<ImportStats> = {}): ImportStats => ({
  memoryPending: 2,
  skillsPending: 1,
  requirementChunksQuarantined: 3,
  routingRulesDisabled: 1,
  runsNotResumed: 1,
  tasksBlocked: 1,
  approvalsCancelled: 2,
  providersNeedingKeys: [{ id: 'prv_1', name: 'OpenRouter', baseUrl: 'https://openrouter.ai/api/v1' }],
  ...over,
});
const imported = (over: Partial<ImportResult> = {}): ImportResult => ({
  projectId: 'prj_new',
  name: 'Alpha (imported)',
  sourceProjectId: 'prj_1',
  counts: {},
  idsRemapped: 10,
  runFilesRestored: 0,
  migratedForward: false,
  pending: pending(),
  sourceSchema: 30,
  ...over,
});

const tid = (root: ParentNode, id: string) => {
  const el = root.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  if (!el) throw new Error(`no ${id}`);
  return el;
};
const none = (root: ParentNode, id: string) => root.querySelector(`[data-testid="${id}"]`) === null;
const btn = (root: ParentNode, name: string) => byText<HTMLButtonElement>(root, 'button', name);
const noButton = (root: ParentNode, name: string) => !Array.from(root.querySelectorAll('button')).some((b) => (b.textContent ?? '').trim() === name);

function show(client: Partial<Client>, projects: Project[] = [proj('prj_1', 'Alpha')]) {
  let list = projects;
  const s = testServices({ projectList: async () => list, ...client });
  s.workspace.start();
  const r = mount(<DataScreen />, s);
  return Object.assign(r, {
    s,
    setProjects(p: Project[]) {
      list = p;
      s.emit({ type: 'project.created' });
    },
  });
}

describe('DataScreen', () => {
  it('previews an export: files, secret-scan result and what is never included', async () => {
    const calls: unknown[] = [];
    const r = show({ projectExportPreview: async (p, runs) => (calls.push([p, runs]), { manifest: manifest(), totalBytes: 4196, includeRuns: false }) });
    await waitFor(() => expect(btn(r.container, 'Preview the export').disabled).toBe(false), r);
    expect(r.$('h1')!.textContent).toContain('Export and import');
    await r.act(() => btn(r.container, 'Preview the export').click());
    const pv = await waitFor(() => tid(r.container, 'export-preview'), r);
    expect(calls).toEqual([['prj_1', false]]);
    expect(pv.getAttribute('data-fresh')).toBe('true');
    expect(pv.querySelector('table[aria-label="Files in the export"]')!.textContent).toContain('drydock.db');
    expect(tid(r.container, 'export-scan').textContent).toContain('2');
    expect(tid(r.container, 'export-scan').textContent).toContain('generic-api-key');
    expect(tid(r.container, 'export-scan').textContent).toContain('Raw run output is not included');
    expect(tid(r.container, 'export-never').textContent).toContain('the repository');
  });

  it('exports into the default place without run output', async () => {
    const calls: unknown[] = [];
    const r = show({
      projectExportPreview: async () => ({ manifest: manifest(), totalBytes: 4196, includeRuns: false }),
      projectExport: async (p, o) => (calls.push([p, o]), { path: 'C:\\data\\exports\\alpha.drydock-project.zip', size: 5000, manifest: manifest() }),
    });
    await waitFor(() => expect(btn(r.container, 'Preview the export').disabled).toBe(false), r);
    expect(noButton(r.container, 'Export')).toBe(true);
    await r.act(() => btn(r.container, 'Preview the export').click());
    await waitFor(() => tid(r.container, 'export-preview'), r);
    await r.act(() => btn(r.container, 'Export').click());
    expect((await waitFor(() => tid(r.container, 'export-path'), r)).textContent).toContain('alpha.drydock-project.zip');
    expect(calls).toEqual([['prj_1', { dest: undefined, includeRuns: false, acknowledgeRuns: false }]]);
    expect(tid(r.container, 'export-result').textContent).toContain('Nothing was uploaded');
  });

  it('makes raw run output opt-in with an acknowledge step, and a changed choice invalidates the preview', async () => {
    const exports: unknown[] = [];
    const r = show({
      projectExportPreview: async (_p, runs): Promise<ExportPreview> => ({
        manifest: manifest({ includesRuns: runs, scan: { dbRedactions: 0, runsFilesScanned: 12, runsRedactions: 4, runsByRule: { 'aws-key': 4 } } }),
        totalBytes: 9000,
        includeRuns: runs,
        warning: runs ? 'Raw run output can contain your source code.' : undefined,
      }),
      projectExport: async (p, o) => (exports.push([p, o]), { path: 'x.zip', size: 1, manifest: manifest() }),
    });
    await waitFor(() => expect(btn(r.container, 'Preview the export').disabled).toBe(false), r);
    await r.act(() => byLabel(r.container, /Include raw run output/).click());
    await r.act(() => btn(r.container, 'Preview the export').click());
    expect((await waitFor(() => tid(r.container, 'export-runs-warning'), r)).textContent).toContain('source code');
    expect(tid(r.container, 'export-scan').textContent).toContain('12 file(s) scanned');
    expect(tid(r.container, 'export-scan').textContent).toContain('aws-key');
    expect(btn(r.container, 'Export').disabled).toBe(true);
    await r.act(() => byLabel(r.container, /I have read the secret-scan result/).click());
    expect(btn(r.container, 'Export').disabled).toBe(false);
    await r.act(() => btn(r.container, 'Export').click());
    await waitFor(() => expect(exports).toEqual([['prj_1', { dest: undefined, includeRuns: true, acknowledgeRuns: true }]]), r);
    // turning the option off hides the stale preview and the export button
    await r.act(() => byLabel(r.container, /Include raw run output/).click());
    expect(none(r.container, 'export-preview')).toBe(true);
    expect(noButton(r.container, 'Export')).toBe(true);
  });

  it('exports the chosen project to the given path', async () => {
    const calls: unknown[] = [];
    const r = show(
      {
        projectExportPreview: async (p) => (calls.push(p), { manifest: manifest({ projectName: 'Beta' }), totalBytes: 1, includeRuns: false }),
        projectExport: async (p, o) => (calls.push([p, o]), { path: 'D:\\b.zip', size: 1, manifest: manifest() }),
      },
      [proj('prj_1', 'Alpha'), proj('prj_2', 'Beta')],
    );
    const sel = await waitFor(() => {
      const el = byLabel<HTMLSelectElement>(r.container, 'Project to export');
      if (el.options.length !== 2) throw new Error('not yet');
      return el;
    }, r);
    await r.act(() => type(sel, 'prj_2'));
    await r.act(() => btn(r.container, 'Preview the export').click());
    await waitFor(() => tid(r.container, 'export-preview'), r);
    type(byLabel(r.container, /^Save to/), ' D:\\b.zip ');
    await r.act(() => btn(r.container, 'Export').click());
    await waitFor(() => expect(calls).toEqual(['prj_2', ['prj_2', { dest: 'D:\\b.zip', includeRuns: false, acknowledgeRuns: false }]]), r);
  });

  it('follows a project that appears after the page opened, and imports an archive only once', async () => {
    let imports = 0;
    const previews: unknown[] = [];
    const preview: ImportPreview = { manifest: manifest(), appSchema: 30, needsMigration: false, counts: { tasks: 3 }, pending: pending() };
    const r = show(
      {
        projectImportPreview: async () => preview,
        projectImport: async () => (imports++, imported({ idsRemapped: 1 })),
        projectExportPreview: async (p, runs) => (previews.push([p, runs]), { manifest: manifest(), totalBytes: 1, includeRuns: false }),
      },
      [],
    );
    await waitFor(() => btn(r.container, 'Preview the export'), r);
    expect(btn(r.container, 'Preview the export').disabled).toBe(true);
    type(byLabel(r.container, /Archive path/), 'a.zip');
    await r.act(() => btn(r.container, 'Preview the import').click());
    await waitFor(() => tid(r.container, 'import-preview'), r);
    type(byLabel(r.container, /Local repository to link/), 'C:\\r');
    await r.act(() => btn(r.container, 'Import as a new project').click());
    await waitFor(() => tid(r.container, 'import-result'), r);
    // the preview and the import button are gone, so a second click cannot import the archive again
    expect(noButton(r.container, 'Import as a new project')).toBe(true);
    expect(imports).toBe(1);
    // the project list now has the new project: export is usable without remounting
    r.setProjects([proj('prj_new', 'Alpha (imported)')]);
    await waitFor(() => expect(btn(r.container, 'Preview the export').disabled).toBe(false), r);
    await r.act(() => btn(r.container, 'Preview the export').click());
    await waitFor(() => expect(previews).toEqual([['prj_new', false]]), r);
  });

  it('previews an import and lists everything that comes in untrusted, then imports with a repo path', async () => {
    const previews: string[] = [];
    const imports: unknown[] = [];
    const preview: ImportPreview = { manifest: manifest(), appSchema: 30, needsMigration: true, counts: { tasks: 3 }, pending: pending() };
    const r = show({
      projectImportPreview: async (p) => (previews.push(p), preview),
      projectImport: async (req) => (imports.push(req), imported({ counts: { tasks: 3 } })),
    });
    await waitFor(() => btn(r.container, 'Preview the import'), r);
    expect(btn(r.container, 'Preview the import').disabled).toBe(true);
    type(byLabel(r.container, /Archive path/), 'C:\\x\\a.drydock-project.zip');
    await r.act(() => btn(r.container, 'Preview the import').click());
    const u = await waitFor(() => tid(r.container, 'import-untrusted'), r);
    expect(previews).toEqual(['C:\\x\\a.drydock-project.zip']);
    expect(tid(r.container, 'import-preview').textContent).toContain('will be migrated forward');
    expect(u.textContent).toContain('Memory entries proposed: 2');
    expect(u.textContent).toContain('Skills offered: 1');
    expect(u.textContent).toContain('Providers that come without keys: 1');
    expect(u.textContent).toContain('disabled and unauthenticated');
    expect(u.textContent).toContain('OpenRouter');
    expect(u.textContent).toContain('Runs not resumed (cancelled): 1');
    expect(u.textContent).toContain('Approvals cancelled: 2');
    expect(btn(r.container, 'Import as a new project').disabled).toBe(true);
    type(byLabel(r.container, /Local repository to link/), 'C:\\src\\repo');
    type(byLabel(r.container, /New project name/), 'Copy');
    await r.act(() => btn(r.container, 'Import as a new project').click());
    expect((await waitFor(() => tid(r.container, 'import-result-name'), r)).textContent).toBe('Alpha (imported)');
    expect(imports).toEqual([{ path: 'C:\\x\\a.drydock-project.zip', repoPath: 'C:\\src\\repo', name: 'Copy' }]);
    expect(tid(r.container, 'import-result').getAttribute('data-project-id')).toBe('prj_new');
    expect(tid(r.container, 'import-result').textContent).toContain('It came in untrusted');
  });

  it('a changed archive path drops the preview', async () => {
    const preview: ImportPreview = { manifest: manifest(), appSchema: 30, needsMigration: false, counts: {}, pending: pending() };
    const r = show({ projectImportPreview: async () => preview });
    type(await waitFor(() => byLabel(r.container, /Archive path/), r), 'a.zip');
    await r.act(() => btn(r.container, 'Preview the import').click());
    await waitFor(() => tid(r.container, 'import-preview'), r);
    await r.act(() => type(byLabel(r.container, /Archive path/), 'b.zip'));
    expect(none(r.container, 'import-preview')).toBe(true);
  });

  it('shows the core error for a bad archive', async () => {
    const r = show({
      projectImportPreview: async () => {
        throw new Error('not a Drydock project archive');
      },
    });
    type(await waitFor(() => byLabel(r.container, /Archive path/), r), 'nope.zip');
    await r.act(() => btn(r.container, 'Preview the import').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('not a Drydock project archive'), r);
  });

  it('shows the core error for a bad repository', async () => {
    const preview: ImportPreview = { manifest: manifest(), appSchema: 30, needsMigration: false, counts: {}, pending: pending() };
    const r = show({
      projectImportPreview: async () => preview,
      projectImport: async () => {
        throw new Error('C:\\nope is not a Git repository');
      },
    });
    type(await waitFor(() => byLabel(r.container, /Archive path/), r), 'a.zip');
    await r.act(() => btn(r.container, 'Preview the import').click());
    await waitFor(() => tid(r.container, 'import-preview'), r);
    type(byLabel(r.container, /Local repository to link/), 'C:\\nope');
    await r.act(() => btn(r.container, 'Import as a new project').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('is not a Git repository'), r);
    // the preview stays so the path can be fixed
    expect(none(r.container, 'import-preview')).toBe(false);
  });
});
