import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { EngineResult, ScanCounts, ScanReport, ScanSummary, SecurityTools } from '../../api/securityTypes.ts';
import { cleanup, drain, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { WorkspaceSecurityScreen } from './workspace-security-screen.tsx';

afterEach(cleanup);

// TEST DATA: no string here looks like a credential. A secrets finding carries only the rule, the file, the line and a title.
const DIR = 'C:\\Users\\kyle\\Downloads\\shop';
const counts = (over: Partial<ScanCounts> = {}): ScanCounts => ({ critical: 0, high: 0, medium: 0, low: 0, info: 0, total: 0, ...over });
const engine = (over: Partial<EngineResult>): EngineResult => ({ id: 'secrets', name: 'Secrets', tool: 'gitleaks', status: 'done', findings: 0, ...over });
const report = (over: Partial<ScanReport> = {}): ScanReport => ({
  id: 'scn1',
  projectId: '',
  dir: DIR,
  status: 'done',
  by: 'human:kyle',
  startedAt: Date.now() - 2_000,
  finishedAt: Date.now() - 1_000,
  engines: [
    engine({ findings: 1 }),
    engine({ id: 'deps', name: 'Dependencies', tool: 'osv-scanner' }),
    engine({ id: 'sast', name: 'Code', tool: 'opengrep' }),
  ],
  findings: [{ engine: 'secrets', severity: 'high', rule: 'aws-access-token', title: 'AWS access token', file: 'notes.txt', line: 1, fingerprint: 'fp1' }],
  counts: counts({ high: 1, total: 1 }),
  ...over,
});
const summary = (r: ScanReport): ScanSummary => ({ id: r.id, projectId: r.projectId, folder: r.projectId ? undefined : r.dir, status: r.status, by: r.by, startedAt: r.startedAt, finishedAt: r.finishedAt, counts: r.counts });
const tools = (over: Partial<SecurityTools> = {}): SecurityTools => ({
  gitleaks: { tool: 'gitleaks', builtIn: true, found: true, version: '8.21.0' },
  osv: { tool: 'osv-scanner', found: true, path: 'C:\\Tools\\osv-scanner.exe' },
  opengrep: { tool: 'opengrep', found: true, path: 'C:\\Tools\\opengrep.exe' },
  builtinRules: true,
  builtinRuleCount: 149,
  rulesDir: '',
  rulesOk: false,
  ready: true,
  ...over,
});

const PROJECTS = [
  { id: 'p1', name: 'gocalc', repoPath: 'C:\\code\\gocalc', hasRepo: true },
  { id: 'p2', name: 'idea', repoPath: '', hasRepo: false },
];

function show(client: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => PROJECTS as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    securityWorkspaceTools: async () => tools(),
    securityTools: async () => tools(),
    securityScanFolders: async () => [],
    securityFolderScanGet: async () => null,
    securityScanGet: async () => null,
    securityScanList: async () => [],
    securityFindingContext: async (scanId: string) => ({
      scanId,
      finding: report().findings[0]!,
      lines: [{ n: 1, text: 'aws_access_key_id = [REDACTED:aws-access-token]', hit: true, masked: true }],
      advice: { meaning: 'Anyone who can read the file can use the key.', steps: ['Turn the key off.'] },
    }),
    agentList: async () => [],
    ...client,
  });
  s.workspace.start();
  const r = mount(<WorkspaceSecurityScreen />, s);
  return { r, s };
}
type R = ReturnType<typeof show>['r'];
const scanButton = (r: R) => r.$('[data-testid="scan-button"]') as HTMLButtonElement;
const folderField = (r: R) => r.$('[data-testid="scan-folder"]') as HTMLInputElement;
const status = (r: R) => (r.$('[data-testid="scan-status"]') as HTMLElement).getAttribute('data-status');
const chips = (r: R) => Array.from(r.$$('[data-testid="scan-history-item"]')) as HTMLButtonElement[];
const radio = async (r: R, id: string) =>
  r.act(() => {
    const x = r.$(`[data-testid="${id}"]`) as HTMLInputElement;
    x.checked = true;
    x.dispatchEvent(new Event('change', { bubbles: true }));
  });

describe('the workspace Security scan (graphite, P97)', () => {
  it('scans a typed folder: "a folder, not a project", only files read, the finding without the value, the folder in the recent scans', async () => {
    const started: string[] = [];
    const asked: string[] = [];
    let stored: ScanReport | null = null;
    const { r } = show({
      securityFolderScanStart: async (folder: string) => {
        started.push(folder);
        stored = report();
        return report({ status: 'running', finishedAt: undefined });
      },
      securityFolderScanGet: async (folder: string) => (asked.push(folder), stored),
      securityScanFolders: async () => (stored ? [summary(stored)] : []),
    });
    await waitFor(() => expect(scanButton(r)).not.toBeNull(), r);
    expect(r.$('h1')!.textContent).toBe('Security scan');
    expect(r.$('[data-testid="scan-target-note"]')!.textContent).toBe('a folder, not a project');
    expect(folderField(r).getAttribute('aria-label')).toBe('Folder to scan');
    expect(r.$('[data-testid="scan-reads-only"]')!.textContent).toBe('Only files are read. Nothing in the folder runs.');
    // a folder is the default; nothing to scan until a path is typed
    expect((r.$('[data-testid="scan-target-folder"]') as HTMLInputElement).checked).toBe(true);
    expect(scanButton(r).disabled).toBe(true);
    expect(status(r)).toBe('idle');
    await waitFor(() => expect(r.$$('[data-testid="scan-engine"][data-status="ready"]').length).toBe(3), r);
    expect(r.$('[data-testid="security-tools"]')).toBeNull();
    expect(r.$('[data-testid="scan-history"]')!.textContent).toContain('No scans yet.');

    type(folderField(r), `  ${DIR}\\  `);
    await waitFor(() => expect(scanButton(r).disabled).toBe(false), r);
    await r.act(() => scanButton(r).click());
    await waitFor(() => expect(started).toEqual([`${DIR}\\`]), r);
    // the page follows the folder the core scanned (its cleaned full path)
    await waitFor(() => expect(asked).toContain(DIR), r);
    await waitFor(() => expect(status(r)).toBe('done'), r);
    expect(folderField(r).value).toBe(DIR);
    expect(r.$('[data-testid="scan-reads-only"]')!.textContent).toBe('Only files were read. Nothing in the folder ran.');
    const secret = r.$('[data-testid="finding"][data-engine="secrets"]')!;
    expect(secret.textContent).toContain('notes.txt:1 · aws-access-token');
    await waitFor(() => expect(r.$$('[data-testid="masked-secret"]').length).toBe(1), r);
    expect(r.container.textContent).not.toContain('REDACTED');
    expect(r.container.textContent).not.toContain('AKIA');
    // the recent scans name the folder by its last part, with its count and when
    await waitFor(() => expect(chips(r).length).toBe(1), r);
    expect(chips(r)[0]!.getAttribute('data-folder')).toBe(DIR);
    expect(chips(r)[0]!.querySelector('.name')!.textContent).toBe('shop');
    expect(chips(r)[0]!.querySelector('.count')!.textContent).toBe('1');
    expect(chips(r)[0]!.querySelector('.when')!.textContent).toBe('now');
  });

  it('a folder scan belongs to no project: the pane says no task can be made, and the button is off', async () => {
    const { r } = show({ securityScanFolders: async () => [summary(report())], securityFolderScanGet: async (f: string) => (f === DIR ? report() : null), securityScanReport: async () => report() });
    const item = await waitFor(() => {
      const x = r.$('[data-testid="scan-history-item"]');
      if (!x) throw new Error('not yet');
      return x as HTMLButtonElement;
    }, r);
    await r.act(() => item.click());
    await waitFor(() => expect(r.$('[data-testid="finding-task"]')).not.toBeNull(), r);
    const task = r.$('[data-testid="finding-task"]') as HTMLButtonElement;
    expect(task.disabled).toBe(true);
    expect(task.textContent).toBe('Make a task');
    expect(r.$('[data-testid="finding-task-folder"]')!.textContent).toBe('A folder scan belongs to no project, so no task can be made from it.');
    expect(r.$('[data-testid="finding-task-agent"]')).toBeNull();
  });

  it('opens a past folder scan from the recent scans and shows its folder', async () => {
    const other = report({ id: 'scn0', dir: 'D:\\imports\\tool', findings: [], counts: counts(), startedAt: Date.now() - 3 * 3_600_000 });
    const opened: string[] = [];
    const { r } = show({
      securityScanFolders: async () => [summary(other)],
      securityScanReport: async (id: string) => (opened.push(id), other),
    });
    const item = await waitFor(() => {
      const x = r.$('[data-testid="scan-history-item"][data-id="scn0"]');
      if (!x) throw new Error('not yet');
      return x as HTMLButtonElement;
    }, r);
    expect(item.querySelector('.name')!.textContent).toBe('tool');
    expect(item.querySelector('.when')!.textContent).toBe('3 h ago');
    await r.act(() => item.click());
    await waitFor(() => expect(opened).toEqual(['scn0']), r);
    await waitFor(() => expect(folderField(r).value).toBe('D:\\imports\\tool'), r);
    await waitFor(() => expect(r.$('[data-testid="findings-empty"]')!.textContent).toBe('Nothing found.'), r);
    expect(item.getAttribute('aria-pressed')).toBe('true');
    expect(scanButton(r).disabled).toBe(false);
  });

  it('shows a folder scan that is still running when the page opens', async () => {
    const running = report({ id: 'scn2', status: 'running', finishedAt: undefined, engines: [engine({ status: 'running', done: 3, total: 6 })], findings: [], counts: counts() });
    const { r } = show({
      securityScanFolders: async () => [summary(running)],
      securityFolderScanGet: async (folder: string) => (folder === DIR ? running : null),
    });
    await waitFor(() => expect(status(r)).toBe('running'), r);
    expect(folderField(r).value).toBe(DIR);
    expect(r.$('[data-testid="cancel-button"]')).not.toBeNull();
    await waitFor(() => expect(chips(r)[0]?.querySelector('.count')!.textContent).toBe('scanning'), r);
  });

  it("scans one of the projects instead: the project's repository, the recent scans of folders and projects together; a project without a repository can't be chosen", async () => {
    const starts: string[] = [];
    const folderScan = report({ id: 'f1', startedAt: Date.now() - 60_000 });
    const projectScan: ScanSummary = { id: 'old', projectId: 'p1', status: 'done', by: 'human:kyle', startedAt: Date.now() - 2 * 3_600_000, counts: counts() };
    const { r } = show({
      securityScanStart: async (projectId: string) => (starts.push(projectId), report({ projectId, status: 'running', finishedAt: undefined })),
      securityScanFolders: async () => [summary(folderScan)],
      securityScanList: async (projectId: string) => (projectId === 'p1' ? [projectScan] : []),
    });
    // the recent scans are every folder's and every project's, newest first
    await waitFor(() => expect(chips(r).map((c) => c.querySelector('.name')!.textContent)).toEqual(['shop', 'gocalc']), r);
    expect(chips(r)[1]!.getAttribute('data-project')).toBe('p1');
    expect(chips(r)[1]!.querySelector('.count')!.textContent).toBe('0');
    await radio(r, 'scan-target-project');
    const select = await waitFor(() => {
      const x = r.$('[data-testid="scan-project"]');
      if (!x) throw new Error('not yet');
      return x as HTMLElement;
    }, r);
    const options = optionsOf(select);
    expect(options.map((o) => [o.value, o.disabled])).toEqual([
      ['p1', false],
      ['p2', true],
    ]);
    expect(options[1]!.label).toContain('no repository yet');
    expect(valueOf(select)).toBe('p1');
    expect(r.$('[data-testid="scan-folder"]')).toBeNull();
    expect(r.$('[data-testid="scan-target-path"]')!.textContent).toBe('C:\\code\\gocalc');
    expect(r.$('[data-testid="scan-target-note"]')!.textContent).toBe("the project's repository");
    await waitFor(() => expect(scanButton(r).disabled).toBe(false), r);
    await r.act(() => scanButton(r).click());
    await waitFor(() => expect(starts).toEqual(['p1']), r);
  });

  it("a project's chip in the recent scans switches to that project and opens its report", async () => {
    const projectReport = report({ id: 'pr1', projectId: 'p1', dir: 'C:\\code\\gocalc' });
    const opened: string[] = [];
    const { r } = show({
      securityScanList: async (projectId: string) => (projectId === 'p1' ? [summary(projectReport)] : []),
      securityScanReport: async (id: string) => (opened.push(id), projectReport),
    });
    const item = await waitFor(() => {
      const x = r.$('[data-testid="scan-history-item"][data-id="pr1"]');
      if (!x) throw new Error('not yet');
      return x as HTMLButtonElement;
    }, r);
    await r.act(() => item.click());
    await waitFor(() => expect(opened).toEqual(['pr1']), r);
    await waitFor(() => expect((r.$('[data-testid="scan-target-project"]') as HTMLInputElement).checked).toBe(true), r);
    await waitFor(() => expect(r.$$('[data-testid="finding"]').length).toBe(1), r);
    expect(r.$('[data-testid="scan-target-path"]')!.textContent).toBe('C:\\code\\gocalc');
  });

  it('R scans the typed folder, but typing an R in the folder field types it', async () => {
    const started: string[] = [];
    const { r } = show({ securityFolderScanStart: async (folder: string) => (started.push(folder), report({ status: 'running', finishedAt: undefined })) });
    await waitFor(() => expect(folderField(r)).not.toBeNull(), r);
    type(folderField(r), DIR);
    await waitFor(() => expect(scanButton(r).disabled).toBe(false), r);
    await r.act(() => folderField(r).dispatchEvent(new KeyboardEvent('keydown', { key: 'r', bubbles: true, cancelable: true })));
    await drain();
    expect(started).toEqual([]);
    await r.act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'r', cancelable: true })));
    await waitFor(() => expect(started).toEqual([DIR]), r);
  });
});
