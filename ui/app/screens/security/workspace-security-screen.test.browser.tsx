import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { EngineResult, ScanCounts, ScanReport, ScanSummary, SecurityTools } from '../../api/securityTypes.ts';
import { cleanup, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
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
  startedAt: 1_700_000_000_000,
  finishedAt: 1_700_000_001_000,
  engines: [
    engine({ findings: 1 }),
    engine({ id: 'deps', name: 'Dependencies', tool: 'osv-scanner' }),
    engine({ id: 'sast', name: 'Code', tool: 'opengrep' }),
  ],
  findings: [{ engine: 'secrets', severity: 'high', rule: 'aws-access-token', title: 'AWS access token', file: 'notes.txt', line: 1 }],
  counts: counts({ high: 1, total: 1 }),
  ...over,
});
const summary = (r: ScanReport): ScanSummary => ({ id: r.id, projectId: '', folder: r.dir, status: r.status, by: r.by, startedAt: r.startedAt, finishedAt: r.finishedAt, counts: r.counts });
const tools = (over: Partial<SecurityTools> = {}): SecurityTools => ({
  gitleaks: { tool: 'gitleaks', builtIn: true, found: true, version: '8.21.0' },
  osv: { tool: 'osv-scanner', found: true, path: 'C:\\Tools\\osv-scanner.exe' },
  opengrep: { tool: 'opengrep', found: true, path: 'C:\\Tools\\opengrep.exe' },
  rulesDir: '',
  rulesOk: false,
  rulesNote: 'No rules folder chosen: the Code engine is skipped until one is.',
  ready: false,
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
    securityTools: async () => tools({ rulesDir: 'C:\\rules\\gocalc', rulesOk: true, ready: true }),
    securityScanFolders: async () => [],
    securityFolderScanGet: async () => null,
    securityScanGet: async () => null,
    securityScanList: async () => [],
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

describe('the workspace Security page', () => {
  it('says the scan only reads files, then scans a typed folder: the findings without the value, the folder, the history', async () => {
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
    const notice = r.$('[data-testid="scan-reads-only"]')!;
    expect(notice.textContent).toContain('only reads files');
    expect(notice.textContent).toContain("never runs the repository's code");
    expect(notice.textContent).toContain('safe for a repository you just downloaded');
    // a folder is the default; nothing to scan until a path is typed
    expect((r.$('[data-testid="scan-target-folder"]') as HTMLInputElement).checked).toBe(true);
    expect(scanButton(r).disabled).toBe(true);
    expect(status(r)).toBe('idle');
    // the engines and whether each tool is ready; no tools section (decision P95)
    await waitFor(() => expect(r.$$('[data-testid="scan-engine"]').length).toBe(3), r);
    expect(r.$('[data-testid="security-tools"]')).toBeNull();

    type(folderField(r), `  ${DIR}\\  `);
    await waitFor(() => expect(scanButton(r).disabled).toBe(false), r);
    await r.act(() => scanButton(r).click());
    await waitFor(() => expect(started).toEqual([`${DIR}\\`]), r);
    // the page follows the folder the core scanned (its cleaned full path)
    await waitFor(() => expect(asked).toContain(DIR), r);
    await waitFor(() => expect(status(r)).toBe('done'), r);
    expect(folderField(r).value).toBe(DIR);
    expect(r.$('[data-testid="scan-dir"]')!.textContent).toContain(DIR);
    const secret = r.$('[data-testid="finding"][data-engine="secrets"]')!;
    expect(secret.textContent).toContain('notes.txt:1');
    expect(secret.textContent).toContain('The value is not shown.');
    expect(r.container.textContent).not.toContain('AKIA');
    // the recent folder scans name their folder
    await waitFor(() => expect(r.$$('[data-testid="scan-history-item"]').length).toBe(1), r);
    const item = r.$('[data-testid="scan-history-item"]')!;
    expect(item.getAttribute('data-folder')).toBe(DIR);
    expect(item.textContent).toContain(DIR);
    expect(r.$('[data-testid="scan-history"]')!.textContent).toContain('Recent folder scans');
  });

  it('opens a past folder scan from the history and shows its folder', async () => {
    const other = report({ id: 'scn0', dir: 'D:\\imports\\tool', findings: [], counts: counts(), startedAt: 1_699_000_000_000 });
    const opened: string[] = [];
    const { r } = show({
      securityScanFolders: async () => [summary(other)],
      securityScanReport: async (id: string) => (opened.push(id), other),
      securityFolderScanGet: async (folder: string) => (folder === other.dir ? other : null),
    });
    const item = await waitFor(() => {
      const x = r.$('[data-testid="scan-history-item"][data-id="scn0"]');
      if (!x) throw new Error('not yet');
      return x as HTMLButtonElement;
    }, r);
    await r.act(() => item.click());
    await waitFor(() => expect(opened).toEqual(['scn0']), r);
    await waitFor(() => expect(folderField(r).value).toBe('D:\\imports\\tool'), r);
    await waitFor(() => expect(r.$('[data-testid="scan-dir"]')!.textContent).toContain('D:\\imports\\tool'), r);
    expect(r.$('[data-testid="findings-empty"]')!.textContent).toBe('Nothing found.');
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
  });

  it("scans one of the projects instead: its working tree and its history; a project without a repository can't be chosen", async () => {
    const starts: string[] = [];
    const { r } = show({
      securityScanStart: async (projectId: string) => (starts.push(projectId), report({ projectId, status: 'running', finishedAt: undefined })),
      securityScanList: async () => [{ id: 'old', projectId: 'p1', status: 'done', by: 'human:kyle', startedAt: 1_699_000_000_000, counts: counts() }],
    });
    await waitFor(() => expect(r.$('[data-testid="scan-target-project"]')).not.toBeNull(), r);
    await r.act(() => {
      const radio = r.$('[data-testid="scan-target-project"]') as HTMLInputElement;
      radio.checked = true;
      radio.dispatchEvent(new Event('change', { bubbles: true }));
    });
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
    // the project's own history, as on its Security page
    await waitFor(() => expect(r.$$('[data-testid="scan-history-item"]').length).toBe(1), r);
    expect(r.$('[data-testid="scan-history"]')!.textContent).toContain('History of gocalc');
    await waitFor(() => expect(scanButton(r).disabled).toBe(false), r);
    await r.act(() => scanButton(r).click());
    await waitFor(() => expect(starts).toEqual(['p1']), r);
  });
});
