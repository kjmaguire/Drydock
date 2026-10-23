import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { DismissReason, EngineResult, ScanCounts, ScanFinding, ScanFindingContext, ScanReport, ScanSummary, SecurityTools } from '../../api/securityTypes.ts';
import type { ScanTarget } from '../../api/securityClient.ts';
import { choose, cleanup, drain, mount, optionsOf, testServices, waitFor } from '../../test/render.tsx';
import { SecurityScreen } from './security-screen.tsx';

afterEach(cleanup);

// TEST DATA: no string here looks like a credential. A secrets finding carries only the rule, the file, the line and a title, and
// the code around it comes back from the core with the secret already masked ("[REDACTED:<rule>]"), as security.finding.context does.
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
    engine({ status: 'done', startedAt: 1_700_000_000_000, finishedAt: 1_700_000_000_100, findings: 1, version: '8.21.0' }),
    engine({ id: 'deps', name: 'Dependencies', tool: 'osv-scanner', status: 'done', startedAt: 1_700_000_000_000, finishedAt: 1_700_000_001_700, findings: 1 }),
    engine({ id: 'sast', name: 'Code', tool: 'opengrep', status: 'done', startedAt: 1_700_000_000_000, finishedAt: 1_700_000_002_700, findings: 2 }),
  ],
  findings: [],
  counts: counts(),
  ...over,
});
const findings: ScanFinding[] = [
  { engine: 'sast', severity: 'medium', rule: 'go.lang.security.audit.crypto.math-random-used', title: 'math/rand used where crypto/rand is needed', file: 'token.go', line: 3, fingerprint: 'fp-rand' },
  {
    engine: 'deps',
    severity: 'high',
    rule: 'Go',
    title: 'golang.org/x/text 0.3.7 has known vulnerabilities: GO-2021-0113, CVE-2021-38561',
    file: 'go.mod',
    package: 'golang.org/x/text',
    version: '0.3.7',
    ids: ['GO-2021-0113', 'CVE-2021-38561'],
    fingerprint: 'fp-deps',
  },
  { engine: 'secrets', severity: 'high', rule: 'aws-access-token', title: 'An AWS access key is written in a file', file: 'config/dev.env', line: 4, fingerprint: 'fp-key' },
  { engine: 'sast', severity: 'high', rule: 'go.lang.security.audit.dangerous-exec-command', title: 'A command is built from input', file: 'run.go', line: 5, fingerprint: 'fp-exec' },
];
const done = (over: Partial<ScanReport> = {}) => report({ findings, counts: counts({ high: 3, medium: 1, total: 4 }), ...over });
/** What security.finding.context answers: the lines as the core read them, the secret masked. */
const context = (scanId: string, fp: string): ScanFindingContext => {
  const f = findings.find((x) => x.fingerprint === fp)!;
  if (f.engine === 'secrets') {
    return {
      scanId,
      finding: f,
      lines: [
        { n: 3, text: 'REGION=ca-central-1' },
        { n: 4, text: 'AWS_ACCESS_KEY_ID=[REDACTED:aws-access-token]', hit: true, masked: true },
        { n: 5, text: 'LOG_LEVEL=debug' },
      ],
      advice: {
        meaning: 'Anyone who can read this file can use the key.',
        steps: ['Turn the key off in AWS, then make a new one.', 'Keep it in a secret store, not in the repository.', 'Remove it from the file and from the Git history.'],
        refs: [],
      },
    };
  }
  if (f.engine === 'deps') {
    return { scanId, finding: f, lines: [], note: 'A dependency finding names a package in go.mod, not a line of code.', advice: { meaning: 'Code that uses it can be attacked.', steps: ['Upgrade it.'], refs: ['GO-2021-0113', 'CVE-2021-38561'] } };
  }
  return {
    scanId,
    finding: f,
    lines: [{ n: f.line!, text: 'exec.Command("sh", "-c", cmd)', hit: true }],
    advice: { meaning: `${f.title}.`, steps: ['Do not build commands from input.'], refs: ["CWE-78: Improper Neutralization of Special Elements used in an OS Command ('OS Command Injection')"] },
  };
};
const tools = (over: Partial<SecurityTools> = {}): SecurityTools => ({
  gitleaks: { tool: 'gitleaks', builtIn: true, found: true, version: '8.21.0' },
  osv: { tool: 'osv-scanner', found: true, path: 'C:\\Tools\\osv-scanner.exe', version: '2.0.2' },
  opengrep: { tool: 'opengrep', found: true, path: 'C:\\Tools\\opengrep.exe', version: '1.1.0' },
  builtinRuleCount: 149,
  builtinRules: true,
  rulesDir: '',
  rulesOk: false,
  ready: true,
  ...over,
});
const AGENTS = [
  { id: 'agt_lead', projectId: 'p1', name: 'Lead', role: 'lead' },
  { id: 'agt_rev', projectId: 'p1', name: 'Reviewer', role: 'reviewer' },
  { id: 'agt_build', projectId: 'p1', name: 'Builder', role: 'builder' },
];

function show(client: Partial<Client>, project: Record<string, unknown> = { id: 'p1', name: 'gocalc', repoPath: 'C:\\code\\gocalc', hasRepo: true }) {
  const s = testServices({
    projectList: async () => [project] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    securityTools: async () => tools(),
    securityScanGet: async () => null,
    securityScanList: async () => [],
    securityFindingContext: async (scanId: string, fp: string) => context(scanId, fp),
    agentList: async () => AGENTS as never,
    ...client,
  });
  s.workspace.start();
  const r = mount(<SecurityScreen />, s);
  return { r, s };
}
type R = ReturnType<typeof show>['r'];
const tiles = (r: R) => Array.from(r.$$('[data-testid="scan-engine"]')) as HTMLElement[];
const status = (r: R) => r.$('[data-testid="scan-status"]') as HTMLElement;
const scanButton = (r: R) => r.$('[data-testid="scan-button"]') as HTMLButtonElement;
const rows = (r: R) => Array.from(r.$$('[data-testid="finding"]')) as HTMLButtonElement[];
const pane = (r: R) => r.$('[data-testid="finding-pane"]') as HTMLElement;
const waitStatus = (r: R, st: string) => waitFor(() => expect(status(r).getAttribute('data-status')).toBe(st), r);
const press = (key: string, target: EventTarget = window) => target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
const summary = (x: ScanReport): ScanSummary => ({ id: x.id, projectId: x.projectId, status: x.status, by: x.by, startedAt: x.startedAt, finishedAt: x.finishedAt, counts: x.counts });
const el = <T extends Element = HTMLElement>(r: R, sel: string): Promise<T> =>
  waitFor(() => {
    const x = r.$(sel);
    if (!x) throw new Error(`no ${sel} yet`);
    return x as unknown as T;
  }, r);

/** Opens the pane's dismiss menu and chooses an item by its words. */
async function dismissAs(r: R, label: string) {
  const trigger = await el<HTMLButtonElement>(r, '[data-testid="finding-dismiss"]');
  const shown = () => !!r.$('[role="menu"]')?.closest('[popover]')?.matches(':popover-open');
  await r.act(() => trigger.click());
  await waitFor(() => {
    if (!shown() && trigger.getAttribute('aria-expanded') !== 'true') trigger.click();
    expect(shown()).toBe(true);
  }, r);
  const item = (Array.from(r.$$('[role="menuitem"]')) as HTMLElement[]).find((x) => x.textContent!.trim() === label);
  if (!item) throw new Error(`no menu item ${label}`);
  await r.act(() => item.click());
}

describe('Security scan of a project (graphite, P97)', () => {
  it('a running scan: three tiles with their state, the secrets bar counts files, the overall bar, Scan is off and Cancel is there', async () => {
    let gets = 0;
    const running = report({
      status: 'running',
      finishedAt: undefined,
      engines: [
        engine({ status: 'running', startedAt: 1_700_000_000_000, done: 10, total: 20, findings: 1 }),
        engine({ id: 'deps', name: 'Dependencies', tool: 'osv-scanner', status: 'running', startedAt: 1_700_000_000_000 }),
        engine({ id: 'sast', name: 'Code', tool: 'opengrep', status: 'pending' }),
      ],
    });
    const { r } = show({ securityScanGet: async () => (gets++, running) });
    await waitStatus(r, 'running');
    expect(status(r).textContent).toBe('Scanning… 0 of 3 engines finished');
    const list = tiles(r);
    expect(list.map((e) => e.getAttribute('data-engine'))).toEqual(['secrets', 'deps', 'sast']);
    expect(list.map((e) => e.getAttribute('data-status'))).toEqual(['running', 'running', 'pending']);
    expect(list.map((e) => e.querySelector('[data-testid="engine-state"]')!.textContent)).toEqual([`running · ${(10).toLocaleString()} of ${(20).toLocaleString()} files`, 'running', 'waiting']);
    expect(list[0]!.querySelector('[data-testid="engine-count"]')!.textContent).toBe('1 secret, value never shown');
    const bars = list.map((e) => e.querySelector('[data-testid="engine-progress"]'));
    expect(bars[0]!.getAttribute('aria-valuenow')).toBe('50');
    expect(bars[0]!.getAttribute('aria-busy')).toBe('true');
    // an engine that cannot count: busy, no value; one that waits has no bar
    expect(bars[1]!.getAttribute('aria-valuenow')).toBeNull();
    expect(bars[1]!.getAttribute('data-determinate')).toBe('false');
    expect(bars[2]).toBeNull();
    expect(r.$('[data-testid="scan-overall"]')!.getAttribute('aria-valuenow')).toBe('17');
    expect(scanButton(r).disabled).toBe(true);
    expect(scanButton(r).textContent).toContain('Scanning…');
    expect(r.$('[data-testid="cancel-button"]')).not.toBeNull();
    expect(r.$('[data-testid="findings"]')).toBeNull();
    // the counters are polled while the scan runs
    const before = gets;
    await waitFor(() => expect(gets).toBeGreaterThan(before + 1), r, 3000);
  });

  it('a done scan: the header, the tiles with time and count, "4 findings", the list by severity with the first selected', async () => {
    const { r } = show({
      securityScanGet: async () => done(),
      securityScanList: async () => [summary(done())],
    });
    await waitStatus(r, 'done');
    expect(r.$('h1')!.textContent).toBe('Security scan');
    expect(r.$('[data-testid="scan-target-path"]')!.textContent).toBe('C:\\code\\gocalc');
    expect(r.$('[data-testid="scan-target-note"]')!.textContent).toBe('gocalc');
    expect(scanButton(r).disabled).toBe(false);
    expect(scanButton(r).textContent).toBe('Scan againR');
    expect(scanButton(r).querySelector('kbd')!.textContent).toBe('R');
    expect(r.$('[data-testid="cancel-button"]')).toBeNull();
    expect(r.$('[data-testid="scan-overall"]')).toBeNull();

    const list = tiles(r);
    expect(list.map((e) => e.getAttribute('data-status'))).toEqual(['done', 'done', 'done']);
    expect(list.map((e) => e.querySelector('.name')!.textContent)).toEqual(['Secrets', 'Dependencies', 'Code']);
    expect(list.map((e) => e.querySelector('.tool')!.textContent)).toEqual(['gitleaks', 'osv-scanner', 'Opengrep · 149 built-in rules']);
    expect(list.map((e) => e.querySelector('[data-testid="engine-state"]')!.textContent)).toEqual(['done · 100 ms', 'done · 1.7 s', 'done · 2.7 s']);
    expect(list.map((e) => e.querySelector('[data-testid="engine-count"]')!.textContent)).toEqual(['1 secret, value never shown', '1 vulnerable package', '2 unsafe patterns']);
    expect(list[0]!.querySelector('.tool')!.getAttribute('title')).toBe('gitleaks 8.21.0');

    expect(r.$('[data-testid="findings-count"]')!.textContent).toBe('4 findings');
    expect(r.$('[data-testid="scan-reads-only"]')!.textContent).toBe('Only files were read. Nothing in the folder ran.');

    await waitFor(() => expect(rows(r).length).toBe(4), r);
    // high first, then by file and line
    expect(rows(r).map((x) => x.getAttribute('data-fingerprint'))).toEqual(['fp-key', 'fp-deps', 'fp-exec', 'fp-rand']);
    const [key, dep, exec, rand] = rows(r);
    expect(key!.querySelector('[data-testid="severity"]')!.textContent).toBe('High');
    expect(key!.querySelector('.title')!.textContent).toBe('An AWS access key is written in a file');
    expect(key!.querySelector('.place')!.textContent).toBe('config/dev.env:4 · aws-access-token');
    expect(key!.querySelector('.engine')!.textContent).toBe('Secrets');
    expect(dep!.querySelector('.title')!.textContent).toBe('golang.org/x/text 0.3.7 has known vulnerabilities');
    expect(dep!.querySelector('.place')!.textContent).toBe('go.mod · GO-2021-0113, CVE-2021-38561');
    expect(exec!.querySelector('.place')!.textContent).toBe('run.go:5 · dangerous-exec-command');
    expect(exec!.getAttribute('data-rule')).toBe('go.lang.security.audit.dangerous-exec-command');
    expect(rand!.querySelector('[data-testid="severity"]')!.textContent).toBe('Medium');
    expect(rand!.getAttribute('data-severity')).toBe('medium');
    // the first is selected
    expect(rows(r).map((x) => x.getAttribute('aria-current'))).toEqual(['true', null, null, null]);

    // the recent scans: the project, its count, when
    const chips = Array.from(r.$$('[data-testid="scan-history-item"]')) as HTMLButtonElement[];
    expect(chips.length).toBe(1);
    expect(chips[0]!.querySelector('.name')!.textContent).toBe('gocalc');
    expect(chips[0]!.querySelector('.count')!.textContent).toBe('4');
    expect(chips[0]!.getAttribute('aria-pressed')).toBe('true');
    expect(r.$('[data-testid="scan-history"]')!.textContent).toContain('Recent scans');
  });

  it("the selected finding's pane: severity and engine, title, meaning, the lines with the secret drawn as a hidden chip, what to do", async () => {
    const asked: string[] = [];
    const { r } = show({
      securityScanGet: async () => done(),
      securityFindingContext: async (scanId: string, fp: string) => (asked.push(`${scanId}:${fp}`), context(scanId, fp)),
    });
    await waitFor(() => expect(r.$('[data-testid="finding-lines"]')).not.toBeNull(), r);
    expect(asked).toEqual(['scan1:fp-key']);
    expect(pane(r).getAttribute('aria-label')).toBe('Finding');
    expect(r.$('[data-testid="finding-pane-severity"]')!.textContent).toBe('High · Secrets');
    expect(r.$('[data-testid="finding-pane-title"]')!.textContent).toBe('An AWS access key is written in a file');
    expect(r.$('[data-testid="finding-meaning"]')!.textContent).toBe('Anyone who can read this file can use the key.');
    const lines = Array.from(r.$$('[data-testid="finding-lines"] .l')) as HTMLElement[];
    expect(lines.map((l) => l.getAttribute('data-n'))).toEqual(['3', '4', '5']);
    expect(lines.map((l) => l.getAttribute('data-hit'))).toEqual([null, 'true', null]);
    expect(lines[1]!.textContent).toBe('4AWS_ACCESS_KEY_ID=•••••••• hidden');
    expect(lines[1]!.querySelectorAll('[data-testid="masked-secret"]').length).toBe(1);
    // neither a value nor the core's mask is ever text on the page
    expect(r.container.textContent).not.toContain('REDACTED');
    expect(r.container.textContent).not.toContain('AKIA');
    const steps = Array.from(r.$$('[data-testid="finding-steps"] li')).map((x) => x.textContent);
    expect(steps).toEqual(['Turn the key off in AWS, then make a new one.', 'Keep it in a secret store, not in the repository.', 'Remove it from the file and from the Git history.']);
    expect(r.$('[data-testid="finding-dismiss"]')!.textContent).toBe('Not a real key');
    expect(r.$('[data-testid="finding-open-file"]')!.textContent).toBe('Open the file');

    // a dependency: no lines, the note, its advisories as links that open outside the app
    await r.act(() => rows(r)[1]!.click());
    await waitFor(() => expect(r.$('[data-testid="finding-note"]')!.textContent).toBe('A dependency finding names a package in go.mod, not a line of code.'), r);
    expect(r.$('[data-testid="finding-lines"]')).toBeNull();
    const links = Array.from(r.$$('[data-testid="finding-refs"] a')) as HTMLAnchorElement[];
    expect(links.map((a) => a.textContent)).toEqual(['GO-2021-0113', 'CVE-2021-38561']);
    expect(links[0]!.getAttribute('href')).toBe('https://osv.dev/vulnerability/GO-2021-0113');
    expect(links[0]!.getAttribute('target')).toBe('_blank');
    expect(links[0]!.hasAttribute('data-rmx-document')).toBe(true);
    expect(r.$('[data-testid="finding-dismiss"]')!.textContent).toBe('Dismiss');
    // a code finding: its CWE links to its page
    await r.act(() => rows(r)[2]!.click());
    await waitFor(() => expect(r.$('[data-testid="finding-refs"] a')!.textContent).toBe('CWE-78'), r);
    expect(r.$('[data-testid="finding-refs"] a')!.getAttribute('href')).toBe('https://cwe.mitre.org/data/definitions/78.html');
    expect(asked).toEqual(['scan1:fp-key', 'scan1:fp-deps', 'scan1:fp-exec']);
  });

  it('J and K, and the arrows inside the list, move the selection; never while typing', async () => {
    const { r } = show({ securityScanGet: async () => done() });
    await waitFor(() => expect(rows(r).length).toBe(4), r);
    const current = () => rows(r).findIndex((x) => x.getAttribute('aria-current') === 'true');
    expect(current()).toBe(0);
    await r.act(() => press('j'));
    await waitFor(() => expect(current()).toBe(1), r);
    await r.act(() => press('j'));
    await r.act(() => press('j'));
    await r.act(() => press('j'));
    await waitFor(() => expect(current()).toBe(3), r);
    await r.act(() => press('k'));
    await waitFor(() => expect(current()).toBe(2), r);
    await waitFor(() => expect(r.$('[data-testid="finding-pane-title"]')!.textContent).toBe('A command is built from input'), r);
    // the arrows from a row move the keyboard with the selection
    rows(r)[2]!.focus();
    await r.act(() => press('ArrowUp', rows(r)[2]!));
    await waitFor(() => expect(current()).toBe(1), r);
    await waitFor(() => expect(document.activeElement).toBe(rows(r)[1]), r);
    await r.act(() => press('ArrowDown', rows(r)[1]!));
    await waitFor(() => expect(current()).toBe(2), r);
    // with Ctrl, or inside a field, the letters are not the page's
    await r.act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'j', ctrlKey: true })));
    const input = document.createElement('input');
    r.container.appendChild(input);
    await r.act(() => press('j', input));
    await drain();
    expect(current()).toBe(2);
  });

  it('R scans again (outside a field), and not while a scan runs', async () => {
    const starts: string[] = [];
    let live: ScanReport = done();
    const { r } = show({
      securityScanGet: async () => live,
      securityScanStart: async (projectId: string) => {
        starts.push(projectId);
        live = report({ id: 'scan2', status: 'running', finishedAt: undefined, engines: [engine({ status: 'running' })] });
        return live;
      },
    });
    await waitStatus(r, 'done');
    expect(scanButton(r).getAttribute('aria-keyshortcuts')).toBe('R');
    const input = document.createElement('input');
    r.container.appendChild(input);
    await r.act(() => press('r', input));
    await drain();
    expect(starts).toEqual([]);
    await r.act(() => press('r'));
    await waitFor(() => expect(starts).toEqual(['p1']), r);
    await waitStatus(r, 'running');
    await r.act(() => press('R'));
    await drain();
    expect(starts).toEqual(['p1']);
  });

  it('"Not a real key" dismisses the secret: the core is asked, the count drops, and it is under "1 dismissed" with Restore', async () => {
    const dismissed: [ScanTarget, string, DismissReason][] = [];
    const restored: [ScanTarget, string][] = [];
    let live = done();
    const after = () =>
      done({
        findings: findings.map((f) => (f.fingerprint === 'fp-key' ? { ...f, dismissed: true, dismissReason: 'not_real' as const, dismissedBy: 'human:kyle' } : f)),
        counts: counts({ high: 2, medium: 1, total: 3 }),
        dismissed: 1,
      });
    const { r } = show({
      securityScanGet: async () => live,
      securityFindingDismiss: async (target: ScanTarget, fp: string, reason: DismissReason) => {
        dismissed.push([target, fp, reason]);
        live = after();
        return live;
      },
      securityFindingRestore: async (target: ScanTarget, fp: string) => {
        restored.push([target, fp]);
        live = done();
        return live;
      },
    });
    await waitFor(() => expect(rows(r).length).toBe(4), r);
    const items = await (async () => {
      await dismissAs(r, 'Not a real key');
      return dismissed;
    })();
    await waitFor(() => expect(items).toEqual([[{ projectId: 'p1' }, 'fp-key', 'not_real']]), r);
    await waitFor(() => expect(rows(r).length).toBe(3), r);
    expect(r.$('[data-testid="findings-count"]')!.textContent).toBe('3 findings');
    // the next finding is selected in its place
    expect(rows(r)[0]!.getAttribute('aria-current')).toBe('true');
    expect(rows(r)[0]!.getAttribute('data-fingerprint')).toBe('fp-deps');
    const toggle = r.$('[data-testid="dismissed-toggle"]') as HTMLButtonElement;
    expect(toggle.textContent).toBe('1 dismissed');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(r.$('[data-testid="dismissed-finding"]')).toBeNull();
    await r.act(() => toggle.click());
    const row = await el(r, '[data-testid="dismissed-finding"]');
    expect(row.getAttribute('data-fingerprint')).toBe('fp-key');
    expect(row.textContent).toContain('not real');
    await r.act(() => (row.querySelector('[data-testid="finding-restore"]') as HTMLButtonElement).click());
    await waitFor(() => expect(restored).toEqual([[{ projectId: 'p1' }, 'fp-key']]), r);
    await waitFor(() => expect(rows(r).length).toBe(4), r);
    expect(r.$('[data-testid="findings-dismissed"]')).toBeNull();
  });

  it('a code finding is dismissed as a false positive or an accepted risk from the same menu', async () => {
    const dismissed: DismissReason[] = [];
    const { r } = show({
      securityScanGet: async () => done(),
      securityFindingDismiss: async (_t: ScanTarget, _fp: string, reason: DismissReason) => (dismissed.push(reason), done()),
    });
    await waitFor(() => expect(rows(r).length).toBe(4), r);
    await r.act(() => rows(r)[2]!.click());
    await waitFor(() => expect(r.$('[data-testid="finding-dismiss"]')!.textContent).toBe('Dismiss'), r);
    await dismissAs(r, 'The rule is wrong here');
    await waitFor(() => expect(dismissed).toEqual(['false_positive']), r);
    expect((Array.from(r.$$('[role="menuitem"]')) as HTMLElement[]).map((x) => x.textContent!.trim())).toEqual(['The rule is wrong here', 'A risk I accept', 'Not a real problem']);
  });

  it("makes a task for the project's builder, or another agent chosen; never the lead", async () => {
    const tasks: [string, string, string][] = [];
    const { r } = show({
      securityScanGet: async () => done(),
      securityFindingTask: async (scanId: string, fp: string, assignee = '') => {
        tasks.push([scanId, fp, assignee]);
        return { task: { id: 'tsk_1', title: 'Remove the secret in config/dev.env:4 and revoke it' } as never, finding: findings[2]! };
      },
    });
    const button = await waitFor(() => {
      const b = r.$('[data-testid="finding-task"]') as HTMLButtonElement | null;
      if (!b || b.textContent !== 'Make a task for Builder') throw new Error('not yet');
      return b;
    }, r);
    const picker = r.$('[data-testid="finding-task-agent"]') as HTMLElement;
    expect(optionsOf(picker).map((o) => o.value)).toEqual(['agt_build', 'agt_rev']);
    await r.act(() => button.click());
    await waitFor(() => expect(tasks).toEqual([['scan1', 'fp-key', 'agt_build']]), r);
    const made = await el(r, '[data-testid="finding-task-made"]');
    expect(made.textContent).toContain('Remove the secret in config/dev.env:4 and revoke it');
    expect(made.querySelector('a')!.getAttribute('href')).toBe('/task/tsk_1');
    expect((r.$('[data-testid="finding-task"]') as HTMLButtonElement).disabled).toBe(true);
    // another finding, another agent
    await r.act(() => rows(r)[1]!.click());
    await waitFor(() => expect(r.$('[data-testid="finding-task-made"]')).toBeNull(), r);
    await r.act(() => choose(r.$('[data-testid="finding-task-agent"]') as HTMLElement, 'agt_rev'));
    await waitFor(() => expect(r.$('[data-testid="finding-task"]')!.textContent).toBe('Make a task for Reviewer'), r);
    await r.act(() => (r.$('[data-testid="finding-task"]') as HTMLButtonElement).click());
    await waitFor(() => expect(tasks[1]).toEqual(['scan1', 'fp-deps', 'agt_rev']), r);
  });

  it('a refused task says why (a finding has one task)', async () => {
    const { r } = show({
      securityScanGet: async () => done(),
      securityFindingTask: async () => {
        throw new Error('the task "Remove the secret" (tsk_1) was already made from this finding');
      },
    });
    const button = await el<HTMLButtonElement>(r, '[data-testid="finding-task"]');
    await waitFor(() => expect(button.textContent).toBe('Make a task for Builder'), r);
    await r.act(() => button.click());
    await waitFor(() => expect(pane(r).querySelector('[role="alert"]')!.textContent).toContain('already made from this finding'), r);
  });

  it('"Open the file" shows the whole file read-only in a dialog, the secret hidden', async () => {
    const read: string[] = [];
    const { r } = show({
      securityScanGet: async () => done(),
      securityFileRead: async (scanId: string, fp: string) => {
        read.push(`${scanId}:${fp}`);
        return {
          ...context(scanId, fp),
          lines: [
            { n: 1, text: '# dev settings' },
            { n: 2, text: 'APP=shop' },
            { n: 3, text: 'REGION=ca-central-1' },
            { n: 4, text: 'AWS_ACCESS_KEY_ID=[REDACTED:aws-access-token]', hit: true, masked: true },
            { n: 5, text: 'LOG_LEVEL=debug' },
          ],
          more: true,
        };
      },
    });
    const open = await el<HTMLButtonElement>(r, '[data-testid="finding-open-file"]');
    await r.act(() => open.click());
    await waitFor(() => expect(read).toEqual(['scan1:fp-key']), r);
    const dialog = await el(r, 'dialog[open]');
    await waitFor(() => expect(dialog.querySelectorAll('[data-testid="file-lines"] .l').length).toBe(5), r);
    expect(dialog.querySelector('h2')!.textContent).toBe('config/dev.env');
    expect(dialog.querySelector('[data-testid="file-lines"] [data-hit="true"]')!.getAttribute('data-n')).toBe('4');
    expect(dialog.querySelectorAll('[data-testid="masked-secret"]').length).toBe(1);
    expect(dialog.querySelector('[data-testid="file-more"]')!.textContent).toBe('The file is longer: these are its first 5 lines.');
    expect(dialog.textContent).not.toContain('REDACTED');
    await r.act(() => (dialog.querySelector('button[aria-label="Close"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('dialog[open]')).toBeNull(), r);
  });

  it('a done scan with nothing found says so, and an errored engine shows its reason', async () => {
    const empty = report({
      engines: [
        engine({ status: 'done', findings: 0 }),
        engine({ id: 'deps', name: 'Dependencies', tool: 'osv-scanner', status: 'skipped', reason: 'osv-scanner is not installed.' }),
        engine({ id: 'sast', name: 'Code', tool: 'opengrep', status: 'errored', reason: 'opengrep exited with status 2.' }),
      ],
    });
    const { r } = show({ securityScanGet: async () => empty });
    await waitStatus(r, 'done');
    expect(status(r).textContent).toBe('Nothing found');
    const note = r.$('[data-testid="findings-empty"]')!;
    expect(note.textContent).toBe('Nothing found by Secrets; Dependencies and Code did not run (see above).');
    expect(note.getAttribute('data-checked')).toBe('false');
    expect(r.$('[data-testid="findings"]')).toBeNull();
    const list = tiles(r);
    expect(list.map((e) => e.getAttribute('data-status'))).toEqual(['done', 'skipped', 'errored']);
    expect(list[0]!.querySelector('[data-testid="engine-count"]')!.textContent).toBe('0 secrets, values never shown');
    expect(Array.from(r.$$('[data-testid="engine-reason"]')).map((e) => e.textContent)).toEqual(['osv-scanner is not installed.', 'opengrep exited with status 2.']);
    expect(pane(r).textContent).toContain('Nothing to look at');
  });

  it('before any scan the tiles say whether each tool is ready; Scan starts a scan of the project; without a repository it cannot', async () => {
    const starts: string[] = [];
    const { r } = show({ securityScanStart: async (projectId: string) => (starts.push(projectId), report({ status: 'running', finishedAt: undefined })) });
    await waitStatus(r, 'idle');
    expect(status(r).textContent).toBe('No scan yet');
    expect(r.$('[data-testid="scan-reads-only"]')!.textContent).toBe('Only files are read. Nothing in the folder runs.');
    await waitFor(() => expect(tiles(r).map((e) => e.getAttribute('data-status'))).toEqual(['ready', 'ready', 'ready']), r);
    expect(tiles(r)[0]!.querySelector('[data-testid="engine-state"]')!.textContent).toBe('ready');
    expect(scanButton(r).textContent).toBe('ScanR');
    await r.act(() => scanButton(r).click());
    await waitFor(() => expect(starts).toEqual(['p1']), r);

    cleanup();
    const bare = show({}, { id: 'p1', name: 'later', repoPath: '', hasRepo: false });
    await waitFor(() => expect(bare.r.$('[data-testid="security-no-repo"]')).not.toBeNull(), bare.r);
    expect(bare.r.$('[data-testid="security-no-repo"]')!.textContent).toContain('needs a repository');
    expect(bare.r.$('[data-testid="security-no-repo"] a')!.getAttribute('href')).toBe('/project');
    expect(bare.r.$('[data-testid="scan-target-path"]')!.textContent).toBe('No repository yet');
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

  it('without the built-in rules the Code tile says it has no rules; a missing tool says so; there is no tools section', async () => {
    const { r } = show({ securityTools: async () => tools({ builtinRules: false, builtinRuleCount: 0, opengrep: { tool: 'opengrep', found: true }, osv: { tool: 'osv-scanner', found: false } }) });
    await waitFor(() => expect(tiles(r)[2]!.getAttribute('data-status')).toBe('missing'), r);
    expect(tiles(r)[2]!.textContent).toContain('no rules');
    expect(tiles(r)[1]!.getAttribute('data-status')).toBe('missing');
    expect(tiles(r)[1]!.textContent).toContain('not installed');
    for (const id of ['security-tools', 'tool', 'tool-install', 'tools-check-updates', 'tool-pick', 'tool-path', 'builtin-rules', 'own-rules', 'rules-dir', 'rules-sources']) {
      expect(r.$(`[data-testid="${id}"]`)).toBeNull();
    }
  });

  it('a recent scan opens its report, and a running scan takes the display back', async () => {
    const old = report({ id: 'scan0', startedAt: 1_699_000_000_000, finishedAt: 1_699_000_001_000, findings: [findings[2]!], counts: counts({ high: 1, total: 1 }) });
    let live: ScanReport = report({ id: 'scan1' });
    const asked: string[] = [];
    const { r, s } = show({
      securityScanGet: async () => live,
      securityScanList: async () => [summary(live), summary(old)],
      securityScanReport: async (id: string) => (asked.push(id), old),
    });
    await waitStatus(r, 'done');
    expect(r.$('[data-testid="findings-empty"]')).not.toBeNull();
    const item = await el<HTMLButtonElement>(r, '[data-testid="scan-history-item"][data-id="scan0"]');
    expect(item.querySelector('.count')!.textContent).toBe('1');
    await r.act(() => item.click());
    await waitFor(() => expect(rows(r).length).toBe(1), r);
    expect(asked).toEqual(['scan0']);
    expect(item.getAttribute('aria-pressed')).toBe('true');
    live = report({ id: 'scan2', status: 'running', finishedAt: undefined, engines: [engine({ status: 'running' })], findings: [] });
    s.emit({ type: 'security.scan.started', projectId: 'p1' });
    await waitStatus(r, 'running');
    expect(rows(r).length).toBe(0);
  });

  describe('a report with thousands of findings (L37)', () => {
    const N = 2000;
    const many: ScanFinding[] = [
      ...Array.from({ length: 3 }, (_, i): ScanFinding => ({ engine: 'secrets', severity: 'critical', rule: 'aws-access-token', title: 'AWS access token', file: `keys/k${i}.txt`, line: 1, fingerprint: `k${i}` })),
      ...Array.from({ length: N }, (_, i): ScanFinding => ({ engine: 'sast', severity: i < N / 2 ? 'high' : 'medium', rule: `rule.${i % 7}`, title: `Finding number ${i}`, file: `src/file-${String(i).padStart(4, '0')}.go`, line: i + 1, fingerprint: `f${i}` })),
    ];
    const big = report({ findings: many, truncated: true, counts: counts({ critical: 3, high: N / 2, medium: N / 2, total: N + 3 }) });
    const frames = () => new Promise<void>((res) => requestAnimationFrame(() => requestAnimationFrame(() => res())));

    it('draws only the findings near the view and still reaches the first and the last', async () => {
      const { r } = show({ securityScanGet: async () => big, securityFindingContext: async (scanId: string) => ({ scanId, finding: many[0]!, lines: [], advice: { meaning: '', steps: [] } }) });
      await waitStatus(r, 'done');
      const sc = await el(r, '[data-testid="findings"]');
      await waitFor(() => expect(rows(r).length).toBeGreaterThan(3), r);
      await frames();
      expect(sc.getAttribute('role')).toBe('list');
      expect(sc.getAttribute('aria-label')).toBe('Findings');
      expect(sc.getAttribute('data-windowed')).toBe('true');
      expect(sc.getAttribute('data-rows')).toBe(String(N + 3));
      expect(rows(r).length).toBeLessThan(120);
      expect(r.$('[data-testid="findings-count"]')!.textContent).toBe(`${(N + 3).toLocaleString()} findings`);
      expect(rows(r)[0]!.querySelector('[data-testid="severity"]')!.textContent).toBe('Critical');
      expect(rows(r)[0]!.querySelector('.place')!.textContent).toBe('keys/k0.txt:1 · aws-access-token');
      expect(getComputedStyle(sc).overflowY).toBe('auto');
      expect(sc.clientHeight).toBeLessThanOrEqual(520);
      expect(sc.getAttribute('tabindex')).toBe('0');
      expect(r.$('[data-testid="findings-truncated"]')).not.toBeNull();
      const scrollTo = async (top: number) => {
        const before = sc.scrollTop;
        const moved = new Promise((res) => sc.addEventListener('scroll', res, { once: true }));
        sc.scrollTop = top;
        if (sc.scrollTop !== before) await moved;
        r.root.flush();
      };
      await scrollTo(sc.scrollHeight);
      await waitFor(() => expect(rows(r).at(-1)!.querySelector('.place')!.textContent).toContain(`src/file-${N - 1}.go:${N}`), r);
      expect(rows(r).some((x) => x.textContent!.includes('keys/k0.txt'))).toBe(false);
      await scrollTo(0);
      await waitFor(() => expect(rows(r)[0]!.textContent).toContain('keys/k0.txt:1'), r);
    });
  });

  describe('the counters of a running scan are read one read at a time (L37)', () => {
    const running = () => report({ status: 'running', finishedAt: undefined, engines: [engine({ status: 'running', startedAt: 1_700_000_000_000, done: 10, total: 20 })] });

    // TEST DOUBLE (labelled): the core's security.scan.get, answering each read when the test says so
    function slowCore() {
      const asked: ((r: ScanReport) => void)[] = [];
      return { asked, get: () => new Promise<ScanReport>((res) => asked.push(res)) };
    }
    const turn = async (r: R) => {
      await drain();
      r.root.flush();
      await drain();
    };

    it('starts no second read while the first is pending, however long the core takes', async (t) => {
      const timers = t.useFakeTimers();
      const core = slowCore();
      const { r } = show({ securityScanGet: core.get });
      await turn(r);
      expect(core.asked.length).toBe(1);
      core.asked[0]!(running());
      await turn(r);
      expect(status(r).getAttribute('data-status')).toBe('running');
      await timers.advanceAsync(499);
      await turn(r);
      expect(core.asked.length).toBe(1);
      await timers.advanceAsync(1);
      await turn(r);
      expect(core.asked.length).toBe(2);
      for (let i = 0; i < 120; i++) await timers.advanceAsync(1000);
      await turn(r);
      expect(core.asked.length).toBe(2);
      core.asked[1]!(running());
      await turn(r);
      await timers.advanceAsync(499);
      await turn(r);
      expect(core.asked.length).toBe(2);
      await timers.advanceAsync(1);
      await turn(r);
      expect(core.asked.length).toBe(3);
    });

    it('stops reading when the scan is done, and when the panel is gone', async (t) => {
      const timers = t.useFakeTimers();
      const core = slowCore();
      const { r } = show({ securityScanGet: core.get });
      await turn(r);
      core.asked[0]!(running());
      await turn(r);
      await timers.advanceAsync(500);
      await turn(r);
      expect(core.asked.length).toBe(2);
      core.asked[1]!(report({ status: 'done' }));
      await turn(r);
      expect(status(r).getAttribute('data-status')).toBe('done');
      for (let i = 0; i < 60; i++) await timers.advanceAsync(1000);
      await turn(r);
      expect(core.asked.length).toBe(2);

      cleanup();
      const again = slowCore();
      const gone = show({ securityScanGet: again.get });
      await turn(gone.r);
      again.asked[0]!(running());
      await turn(gone.r);
      await timers.advanceAsync(500);
      await turn(gone.r);
      expect(again.asked.length).toBe(2);
      again.asked[1]!(running());
      await turn(gone.r);
      cleanup();
      for (let i = 0; i < 60; i++) await timers.advanceAsync(1000);
      await drain();
      expect(again.asked.length).toBe(2);
    });
  });
});
