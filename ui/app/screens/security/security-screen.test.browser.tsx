import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { EngineResult, ScanCounts, ScanFinding, ScanReport, ScanSummary, SecurityTools } from '../../api/securityTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { SecurityScreen } from './security-screen.tsx';

afterEach(cleanup);

// TEST DATA: no string here looks like a credential. A secrets finding carries only the rule, the file, the line and a title.
const engine = (over: Partial<EngineResult>): EngineResult => ({ id: 'secrets', name: 'Secrets', tool: 'gitleaks', status: 'pending', findings: 0, ...over });
const counts = (over: Partial<ScanCounts> = {}): ScanCounts => ({ critical: 0, high: 0, medium: 0, low: 0, info: 0, total: 0, ...over });
const report = (over: Partial<ScanReport>): ScanReport => ({
  id: 'scan1',
  projectId: 'p1',
  dir: 'C:\\repo',
  status: 'done',
  by: 'human:kyle',
  startedAt: 1_700_000_000_000,
  finishedAt: 1_700_000_002_300,
  engines: [
    engine({ status: 'done', startedAt: 1_700_000_000_000, finishedAt: 1_700_000_001_200, findings: 1, version: '8.21.0' }),
    engine({ id: 'deps', name: 'Dependencies', tool: 'osv-scanner', status: 'done', startedAt: 1_700_000_000_000, finishedAt: 1_700_000_002_300, findings: 1 }),
    engine({ id: 'sast', name: 'Code patterns', tool: 'opengrep', status: 'done', startedAt: 1_700_000_000_000, finishedAt: 1_700_000_001_900, findings: 2 }),
  ],
  findings: [],
  counts: counts(),
  ...over,
});
const findings: ScanFinding[] = [
  { engine: 'sast', severity: 'medium', rule: 'go.lang.security.audit.crypto.math-random-used', title: 'math/rand is not a secure source of randomness.', file: 'calc.go', line: 30 },
  {
    engine: 'deps',
    severity: 'medium',
    rule: 'GO-2021-0113',
    title: 'Out-of-bounds read in golang.org/x/text/language',
    package: 'golang.org/x/text',
    version: '0.3.7',
    ids: ['GO-2021-0113', 'CVE-2021-38561'],
  },
  { engine: 'secrets', severity: 'high', rule: 'aws-access-token', title: 'AWS access token', file: 'notes.txt', line: 1 },
  { engine: 'sast', severity: 'high', rule: 'go.lang.security.audit.dangerous-exec-command', title: 'A command is built from input.', file: 'calc.go', line: 12 },
];
const tools = (over: Partial<SecurityTools> = {}): SecurityTools => ({
  gitleaks: { tool: 'gitleaks', builtIn: true, found: true, version: '8.21.0' },
  osv: { tool: 'osv-scanner', found: true, path: 'C:\\Tools\\osv-scanner.exe', version: '2.0.2' },
  opengrep: { tool: 'opengrep', found: true, path: 'C:\\Tools\\opengrep.exe', version: '1.1.0' },
  rulesDir: 'C:\\rules',
  rulesOk: true,
  ready: true,
  ...over,
});

function show(client: Partial<Client>, project: Record<string, unknown> = { id: 'p1', name: 'gocalc', hasRepo: true }) {
  const s = testServices({
    projectList: async () => [project] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    securityTools: async () => tools(),
    securityScanGet: async () => null,
    securityScanList: async () => [],
    ...client,
  });
  s.workspace.start();
  const r = mount(<SecurityScreen />, s);
  return { r, s };
}
type R = ReturnType<typeof show>['r'];
const rows = (r: R) => Array.from(r.$$('[data-testid="scan-engine"]')) as HTMLElement[];
const status = (r: R) => r.$('[data-testid="scan-status"]') as HTMLElement;
const scanButton = (r: R) => r.$('[data-testid="scan-button"]') as HTMLButtonElement;
const waitStatus = (r: R, st: string) => waitFor(() => expect(status(r).getAttribute('data-status')).toBe(st), r);

describe('Security scan page', () => {
  it('a running scan: three engine rows with their state, the secrets bar counts files, the overall bar averages, Scan is off and Cancel is there', async () => {
    let gets = 0;
    const running = report({
      status: 'running',
      finishedAt: undefined,
      engines: [
        engine({ status: 'running', startedAt: 1_700_000_000_000, done: 10, total: 20 }),
        engine({ id: 'deps', name: 'Dependencies', tool: 'osv-scanner', status: 'running', startedAt: 1_700_000_000_000 }),
        engine({ id: 'sast', name: 'Code patterns', tool: 'opengrep', status: 'pending' }),
      ],
    });
    const { r } = show({ securityScanGet: async () => (gets++, running) });
    await waitStatus(r, 'running');
    expect(status(r).textContent).toBe('Scanning… 0 of 3 engines finished');
    const list = rows(r);
    expect(list.map((e) => e.getAttribute('data-engine'))).toEqual(['secrets', 'deps', 'sast']);
    expect(list.map((e) => e.getAttribute('data-status'))).toEqual(['running', 'running', 'pending']);
    const bars = list.map((e) => e.querySelector('[data-testid="engine-progress"]')!);
    expect(bars[0]!.getAttribute('aria-valuenow')).toBe('50');
    expect(bars[0]!.getAttribute('aria-busy')).toBe('true');
    expect(list[0]!.textContent).toContain(`${(10).toLocaleString()} of ${(20).toLocaleString()} files`);
    // an engine that cannot count: busy, no value
    expect(bars[1]!.getAttribute('aria-valuenow')).toBeNull();
    expect(bars[1]!.getAttribute('aria-busy')).toBe('true');
    expect(bars[1]!.getAttribute('data-determinate')).toBe('false');
    expect(bars[2]!.getAttribute('aria-valuenow')).toBe('0');
    expect(bars[2]!.getAttribute('aria-busy')).toBeNull();
    // (0.5 + 0 + 0) / 3
    expect(r.$('[data-testid="scan-overall"]')!.getAttribute('aria-valuenow')).toBe('17');
    expect(scanButton(r).disabled).toBe(true);
    expect(scanButton(r).textContent).toBe('Scanning…');
    expect(r.$('[data-testid="cancel-button"]')).not.toBeNull();
    // the rows show the scan, not the tools' readiness
    expect(r.$('[data-testid="findings"]')).toBeNull();
    // the counters are polled every 500 ms while the scan runs (the interval starts with the first render, in real time)
    const before = gets;
    await waitFor(() => expect(gets).toBeGreaterThan(before), r, 3000);
    await waitFor(() => expect(gets).toBeGreaterThan(before + 1), r, 3000);
  });

  it('a done scan lists the findings by severity, the chips filter them, a secret is named but never shown', async () => {
    const done = report({ findings, counts: counts({ high: 2, medium: 2, total: 4 }), summary: '2 high and 2 medium findings in 3 files.' });
    const { r } = show({
      securityScanGet: async () => done,
      securityScanList: async () => [{ id: 'scan1', projectId: 'p1', status: 'done', by: 'human:kyle', startedAt: done.startedAt, finishedAt: done.finishedAt, counts: done.counts } as ScanSummary],
    });
    await waitStatus(r, 'done');
    expect(status(r).textContent).toContain('2 high and 2 medium findings in 3 files.');
    expect(status(r).textContent).toContain('by kyle');
    expect(status(r).textContent).not.toContain('human:');
    expect(r.$('[data-testid="scan-overall"]')!.getAttribute('aria-valuenow')).toBe('100');
    expect(scanButton(r).disabled).toBe(false);
    expect(scanButton(r).textContent).toBe('Scan');
    expect(r.$('[data-testid="cancel-button"]')).toBeNull();

    const list = rows(r);
    expect(list.map((e) => e.getAttribute('data-status'))).toEqual(['done', 'done', 'done']);
    expect(list[0]!.textContent).toContain('1 finding');
    expect(list[0]!.textContent).toContain('in 1.2 s');
    expect(list[0]!.textContent).toContain('gitleaks 8.21.0');
    expect(list[2]!.textContent).toContain('2 findings');
    expect(list[0]!.querySelector('[data-testid="engine-progress"]')!.getAttribute('aria-valuenow')).toBe('100');

    const table = r.$('[data-testid="findings"]')!;
    const trs = () => Array.from(table.querySelectorAll('[data-testid="finding"]')) as HTMLElement[];
    expect(trs().map((x) => x.getAttribute('data-severity'))).toEqual(['high', 'high', 'medium', 'medium']);
    // within a severity: by file, then line
    expect(trs().map((x) => x.getAttribute('data-engine'))).toEqual(['sast', 'secrets', 'sast', 'deps']);
    const secret = trs()[1]!;
    expect(secret.textContent).toContain('aws-access-token');
    expect(secret.textContent).toContain('notes.txt:1');
    expect(secret.textContent).toContain('AWS access token The value is not shown.');
    const dep = trs()[3]!;
    expect(dep.textContent).toContain('golang.org/x/text@0.3.7');
    const links = Array.from(dep.querySelectorAll('a')) as HTMLAnchorElement[];
    expect(links.map((a) => a.textContent)).toEqual(['GO-2021-0113', 'CVE-2021-38561']);
    expect(links[0]!.getAttribute('href')).toBe('https://osv.dev/vulnerability/GO-2021-0113');
    expect(links[0]!.getAttribute('target')).toBe('_blank');
    expect(links[0]!.getAttribute('rel')).toBe('noreferrer');
    // nothing on the page resembles the value the rule matched
    expect(r.container.textContent).not.toContain('AKIA');
    expect(r.$('[data-testid="findings-count"]')!.textContent).toBe('4 findings');

    const chips = () => Array.from(r.$$('[data-testid="sev-filter"]')) as HTMLButtonElement[];
    expect(chips().map((c) => c.getAttribute('data-severity'))).toEqual(['all', 'high', 'medium']);
    expect(chips()[0]!.textContent).toBe('All 4');
    expect(chips()[0]!.getAttribute('aria-pressed')).toBe('true');
    await r.act(() => chips()[1]!.click());
    expect(trs().map((x) => x.getAttribute('data-severity'))).toEqual(['high', 'high']);
    expect(chips()[1]!.getAttribute('aria-pressed')).toBe('true');
    expect(chips()[0]!.getAttribute('aria-pressed')).toBe('false');
    expect(r.$('[data-testid="findings-count"]')!.textContent).toBe('2 of 4 findings');
    const engines = () => Array.from(r.$$('[data-testid="engine-filter"]')) as HTMLButtonElement[];
    expect(engines().map((c) => c.getAttribute('data-engine'))).toEqual(['all', 'secrets', 'deps', 'sast']);
    await r.act(() => engines()[3]!.click());
    expect(trs().map((x) => x.getAttribute('data-engine'))).toEqual(['sast']);
    await r.act(() => chips()[0]!.click());
    expect(trs().map((x) => x.getAttribute('data-engine'))).toEqual(['sast', 'sast']);

    // the history names the scan
    const items = Array.from(r.$$('[data-testid="scan-history-item"]')) as HTMLButtonElement[];
    expect(items.length).toBe(1);
    expect(items[0]!.getAttribute('data-id')).toBe('scan1');
    expect(items[0]!.textContent).toContain('by kyle');
    expect(items[0]!.textContent).toContain('2 high, 2 medium');
  });

  it('a done scan with nothing found says so, and an errored engine shows its reason', async () => {
    const done = report({
      engines: [
        engine({ status: 'done', findings: 0 }),
        engine({ id: 'deps', name: 'Dependencies', tool: 'osv-scanner', status: 'skipped', reason: 'osv-scanner is not installed.' }),
        engine({ id: 'sast', name: 'Code patterns', tool: 'opengrep', status: 'errored', reason: 'opengrep exited with status 2.' }),
      ],
    });
    const { r } = show({ securityScanGet: async () => done });
    await waitStatus(r, 'done');
    expect(status(r).textContent).toContain('Nothing found.');
    expect(r.$('[data-testid="findings-empty"]')!.textContent).toBe('Nothing found.');
    expect(r.$('[data-testid="findings"]')).toBeNull();
    const list = rows(r);
    expect(list.map((e) => e.getAttribute('data-status'))).toEqual(['done', 'skipped', 'errored']);
    expect(list[0]!.textContent).toContain('nothing found');
    const reasons = Array.from(r.$$('[data-testid="engine-reason"]')).map((e) => e.textContent);
    expect(reasons).toEqual(['osv-scanner is not installed.', 'opengrep exited with status 2.']);
  });

  it('pressing Scan starts a scan of the project, and a project without a repository cannot scan', async () => {
    const starts: string[] = [];
    const { r } = show({ securityScanStart: async (projectId: string) => (starts.push(projectId), report({ status: 'running', finishedAt: undefined })) });
    await waitStatus(r, 'idle');
    expect(status(r).textContent).toBe('No scan yet.');
    // before any scan the rows show the tools' readiness
    await waitFor(() => expect(rows(r).map((e) => e.getAttribute('data-status'))).toEqual(['ready', 'ready', 'ready']), r);
    expect(r.$('[data-testid="scan-overall"]')).toBeNull();
    expect(scanButton(r).disabled).toBe(false);
    await r.act(() => scanButton(r).click());
    await waitFor(() => expect(starts).toEqual(['p1']), r);

    cleanup();
    const bare = show({}, { id: 'p1', name: 'later', repoPath: '', hasRepo: false });
    await waitFor(() => expect(bare.r.$('[data-testid="security-no-repo"]')).not.toBeNull(), bare.r);
    expect(bare.r.$('[data-testid="security-no-repo"]')!.textContent).toContain('needs a repository');
    expect(bare.r.$('[data-testid="security-no-repo"] a')!.getAttribute('href')).toBe('/project');
    expect(scanButton(bare.r).disabled).toBe(true);
  });

  it('Cancel asks the core to stop the running scan', async () => {
    const cancels: string[] = [];
    const { r } = show({
      securityScanGet: async () => report({ status: 'running', finishedAt: undefined, engines: [engine({ status: 'running' })] }),
      securityScanCancel: async (projectId: string) => (cancels.push(projectId), report({ status: 'cancelled' })),
    });
    await waitStatus(r, 'running');
    await r.act(() => (r.$('[data-testid="cancel-button"]') as HTMLButtonElement).click());
    await waitFor(() => expect(cancels).toEqual(['p1']), r);
  });

  it('the tools panel: built-in gitleaks, a missing tool with its install hint, the rules note, and saving the rules folder', async () => {
    const sets: [string, string][] = [];
    // the core answers with the saved state once the folder was set
    const unset = tools({
      osv: { tool: 'osv-scanner', found: false, install: 'winget install Google.OSVScanner' },
      opengrep: { tool: 'opengrep', found: true, path: 'C:\\Tools\\opengrep.exe', version: '1.1.0' },
      rulesDir: '',
      rulesOk: false,
      rulesNote: 'No rules folder is set.',
      ready: false,
    });
    const { r } = show({
      securityTools: async () => (sets.length > 0 ? tools() : unset),
      securitySettingsSet: async (projectId: string, rulesDir: string) => (sets.push([projectId, rulesDir]), tools()),
    });
    const panel = await waitFor(() => {
      const p = r.$('[data-testid="security-tools"]');
      if (!p || !p.querySelector('[data-testid="tool"]')) throw new Error('not yet');
      return p as HTMLElement;
    }, r);
    const tl = Array.from(panel.querySelectorAll('[data-testid="tool"]')) as HTMLElement[];
    expect(tl.map((x) => x.getAttribute('data-tool'))).toEqual(['gitleaks', 'osv-scanner', 'opengrep']);
    expect(tl.map((x) => x.getAttribute('data-found'))).toEqual(['true', 'false', 'true']);
    expect(tl[0]!.textContent).toContain('built into Drydock, 8.21.0');
    expect(tl[1]!.textContent).toContain('Not installed');
    expect(tl[1]!.textContent).toContain('winget install Google.OSVScanner');
    expect(tl[2]!.textContent).toContain('Found at C:\\Tools\\opengrep.exe (1.1.0)');
    expect(panel.querySelector('[data-testid="rules-note"]')!.textContent).toContain('No rules folder is set.');
    expect(panel.textContent).toContain('rules you are licensed to use');
    // the engine rows say which tool is missing before any scan ran
    expect(rows(r).map((e) => e.getAttribute('data-status'))).toEqual(['ready', 'missing', 'missing']);
    expect(rows(r)[1]!.textContent).toContain('not installed');
    expect(rows(r)[2]!.textContent).toContain('no rules');

    const field = byLabel<HTMLInputElement>(panel, 'Rules folder');
    expect(field.getAttribute('data-testid')).toBe('rules-dir');
    expect(field.value).toBe('');
    type(field, 'C:\\rules');
    await r.act(() => byText(panel, 'button', 'Save').click());
    await waitFor(() => expect(sets).toEqual([['p1', 'C:\\rules']]), r);
    await waitFor(() => expect(panel.textContent).toContain('Rules found.'), r);
    expect(panel.querySelector('[data-testid="rules-note"]')).toBeNull();
  });

  it('a history item opens that report, and a running scan takes the display back', async () => {
    const old = report({ id: 'scan0', startedAt: 1_699_000_000_000, finishedAt: 1_699_000_001_000, findings: [findings[2]!], counts: counts({ high: 1, total: 1 }) });
    let live: ScanReport = report({ id: 'scan1' });
    const asked: string[] = [];
    const { r, s } = show({
      securityScanGet: async () => live,
      securityScanList: async () => [
        { id: 'scan1', projectId: 'p1', status: 'done', by: 'human:kyle', startedAt: live.startedAt, counts: live.counts },
        { id: 'scan0', projectId: 'p1', status: 'done', by: 'human:kyle', startedAt: old.startedAt, counts: old.counts },
      ],
      securityScanReport: async (id: string) => (asked.push(id), old),
    });
    await waitStatus(r, 'done');
    expect(r.$('[data-testid="findings-empty"]')).not.toBeNull();
    const item = await waitFor(() => {
      const x = r.$('[data-testid="scan-history-item"][data-id="scan0"]');
      if (!x) throw new Error('not yet');
      return x as HTMLButtonElement;
    }, r);
    await r.act(() => item.click());
    await waitFor(() => expect(r.$$('[data-testid="finding"]').length).toBe(1), r);
    expect(asked).toEqual(['scan0']);
    expect(item.getAttribute('aria-pressed')).toBe('true');
    // a new scan starts: the stream says so and the running one is shown
    live = report({ id: 'scan2', status: 'running', finishedAt: undefined, engines: [engine({ status: 'running' })], findings: [] });
    s.emit({ type: 'security.scan.started', projectId: 'p1' });
    await waitStatus(r, 'running');
    expect(r.$$('[data-testid="finding"]').length).toBe(0);
  });
});
