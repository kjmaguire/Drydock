import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { EngineResult, ScanCounts, ScanFinding, ScanReport } from '../../api/securityTypes.ts';
import { formatDate } from '../../lib/format.ts';
import {
  toolsUpdateText,
  agoText,
  byName,
  countsText,
  durationText,
  engineFraction,
  engineStateText,
  engineTone,
  engineWords,
  findingId,
  findingsText,
  findingTitle,
  folderName,
  lineParts,
  lineText,
  overallPercent,
  placeText,
  refLabel,
  refUrl,
  reportTarget,
  severityLabel,
  severityTone,
  shortRule,
  sortFindings,
  statusText,
  summaryTone,
  taskAgents,
  toolLabel,
  whereText,
  emptyFindingsText,
} from './security.ts';

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
  engines: [],
  findings: [],
  counts: counts(),
  ...over,
});
const finding = (over: Partial<ScanFinding>): ScanFinding => ({ engine: 'sast', severity: 'medium', rule: 'r', title: 't', ...over });

describe('engine progress', () => {
  it('counts a finished engine as 1, the secrets engine by its files, and an engine that cannot count as 0', () => {
    expect(engineFraction(engine({ status: 'done' }))).toBe(1);
    expect(engineFraction(engine({ status: 'skipped' }))).toBe(1);
    expect(engineFraction(engine({ status: 'errored' }))).toBe(1);
    expect(engineFraction(engine({ status: 'cancelled' }))).toBe(1);
    expect(engineFraction(engine({ status: 'running', done: 10, total: 20 }))).toBe(0.5);
    expect(engineFraction(engine({ status: 'running', done: 30, total: 20 }))).toBe(1);
    expect(engineFraction(engine({ status: 'running', total: 0 }))).toBe(0);
    expect(engineFraction(engine({ status: 'pending' }))).toBe(0);
  });

  it('averages the engines while running and is complete once the scan is not', () => {
    const running = report({
      status: 'running',
      finishedAt: undefined,
      engines: [engine({ status: 'running', done: 10, total: 20 }), engine({ id: 'deps', status: 'running' }), engine({ id: 'sast', status: 'pending' })],
    });
    expect(overallPercent(running)).toBe(17);
    expect(overallPercent(report({ status: 'running', engines: [] }))).toBe(0);
    expect(overallPercent(report({ status: 'done', engines: [engine({ status: 'done' }), engine({ id: 'deps', status: 'pending' })] }))).toBe(100);
    expect(overallPercent(undefined)).toBe(0);
  });

  it('tones: done is ok without findings and warn with them; running accent, errored err, the rest muted', () => {
    expect(engineTone(engine({ status: 'done' }))).toBe('ok');
    expect(engineTone(engine({ status: 'done', findings: 2 }))).toBe('warn');
    expect(engineTone(engine({ status: 'running' }))).toBe('accent');
    expect(engineTone(engine({ status: 'errored' }))).toBe('err');
    expect(engineTone(engine({ status: 'pending' }))).toBe('muted');
    expect(engineTone(engine({ status: 'skipped' }))).toBe('muted');
    expect(engineTone(engine({ status: 'cancelled' }))).toBe('muted');
  });

  it('severity tones', () => {
    expect(severityTone('critical')).toBe('err');
    expect(severityTone('high')).toBe('err');
    expect(severityTone('medium')).toBe('warn');
    expect(severityTone('low')).toBe('info');
    expect(severityTone('info')).toBe('muted');
  });
});

describe('words', () => {
  it('counts in words, most severe first, or nothing found', () => {
    expect(countsText(counts({ high: 1, medium: 2, total: 3 }))).toBe('1 high, 2 medium');
    expect(countsText(counts({ critical: 1, info: 4, total: 5 }))).toBe('1 critical, 4 info');
    expect(countsText(counts())).toBe('nothing found');
    expect(countsText(undefined)).toBe('nothing found');
    expect(findingsText(3)).toBe('3 findings');
    expect(findingsText(1)).toBe('1 finding');
    expect(findingsText(0)).toBe('nothing found');
  });

  it('strips the human prefix from who started a scan', () => {
    expect(byName('human:kyle')).toBe('kyle');
    expect(byName('agent:a1')).toBe('agent:a1');
    expect(byName(undefined)).toBe('');
  });

  it('durations', () => {
    expect(durationText(420)).toBe('420 ms');
    expect(durationText(2300)).toBe('2.3 s');
    expect(durationText(65_000)).toBe('1 min 5 s');
    expect(durationText(120_000)).toBe('2 min');
    expect(durationText(undefined)).toBe('');
    expect(durationText(-1)).toBe('');
  });

  it('the status line: idle, running with the finished count, done with the summary and who started it, cancelled with the time', () => {
    const when = (ms: number) => `T${ms}`;
    expect(statusText(undefined, when)).toBe('No scan yet.');
    expect(statusText(null, when)).toBe('No scan yet.');
    const running = report({ status: 'running', engines: [engine({ status: 'done' }), engine({ id: 'deps', status: 'running' }), engine({ id: 'sast', status: 'skipped' })] });
    expect(statusText(running, when)).toBe('Scanning… 2 of 3 engines finished');
    expect(statusText(report({ status: 'running', engines: [] }), when)).toBe('Scanning… 0 of 3 engines finished');
    expect(statusText(report({ summary: '1 high finding in 2 files.' }), when)).toBe('1 high finding in 2 files. Started T1700000000000 by kyle.');
    expect(statusText(report({ counts: counts({ high: 1, medium: 2, total: 3 }) }), when)).toBe('Found 1 high, 2 medium. Started T1700000000000 by kyle.');
    expect(statusText(report({}), when)).toBe('Nothing found. Started T1700000000000 by kyle.');
    expect(statusText(report({ status: 'cancelled' }), when)).toBe('Cancelled after 2.3 s.');
    expect(statusText(report({ status: 'cancelled', finishedAt: undefined }), when)).toBe('Cancelled.');
    expect(statusText(report({ status: 'failed' }), when)).toBe('Failed.');
  });

  it('where a finding is: file and line, or package and version', () => {
    expect(whereText(finding({ file: 'notes.txt', line: 1 }))).toBe('notes.txt:1');
    expect(whereText(finding({ file: 'notes.txt' }))).toBe('notes.txt');
    expect(whereText(finding({ engine: 'deps', package: 'golang.org/x/text', version: '0.3.7' }))).toBe('golang.org/x/text@0.3.7');
    expect(whereText(finding({ engine: 'deps', package: 'left-pad' }))).toBe('left-pad');
    expect(whereText(finding({ engine: 'deps', package: 'golang.org/x/text', version: '0.3.7', file: 'go.mod' }))).toBe('golang.org/x/text@0.3.7 in go.mod');
    expect(whereText(finding({}))).toBe('');
  });
});

describe('findings lists', () => {
  const list: ScanFinding[] = [
    finding({ severity: 'medium', file: 'calc.go', line: 30, rule: 'b' }),
    finding({ engine: 'deps', severity: 'low', package: 'x', rule: 'c' }),
    finding({ engine: 'secrets', severity: 'high', file: 'notes.txt', line: 1, rule: 'd' }),
    finding({ severity: 'high', file: 'calc.go', line: 12, rule: 'e' }),
    finding({ severity: 'high', file: 'calc.go', line: 4, rule: 'f' }),
    finding({ severity: 'critical', file: 'z.go', line: 9, rule: 'a' }),
  ];

  it('sorts by severity, then file, then line, without changing the input', () => {
    const sorted = sortFindings(list);
    expect(sorted.map((f) => f.rule)).toEqual(['a', 'f', 'e', 'd', 'b', 'c']);
    expect(list[0]!.rule).toBe('b');
  });

});

describe('the graphite screen (P97)', () => {
  it('names severities and says what each engine counted', () => {
    expect(['critical', 'high', 'medium', 'low', 'info'].map(severityLabel)).toEqual(['Critical', 'High', 'Medium', 'Low', 'Info']);
    expect(severityLabel('weird')).toBe('Weird');
    expect(engineWords('secrets', 1)).toBe('secret, value never shown');
    expect(engineWords('secrets', 3)).toBe('secrets, values never shown');
    expect(engineWords('deps', 1)).toBe('vulnerable package');
    expect(engineWords('deps', 0)).toBe('vulnerable packages');
    expect(engineWords('sast', 2)).toBe('unsafe patterns');
    expect(engineWords('sast', 1)).toBe('unsafe pattern');
    expect(toolLabel('opengrep')).toBe('Opengrep');
    expect(toolLabel('gitleaks')).toBe('gitleaks');
  });

  it("an engine's state with its time, its files while it counts them, and why it stopped", () => {
    expect(engineStateText(engine({ status: 'done', startedAt: 1000, finishedAt: 1100 }))).toBe('done · 100 ms');
    expect(engineStateText(engine({ status: 'done', startedAt: 1000, finishedAt: 2700 }))).toBe('done · 1.7 s');
    expect(engineStateText(engine({ status: 'done' }))).toBe('done');
    expect(engineStateText(engine({ status: 'running', done: 3, total: 9 }))).toBe('running · 3 of 9 files');
    expect(engineStateText(engine({ status: 'running' }))).toBe('running');
    expect(engineStateText(engine({ status: 'pending' }))).toBe('waiting');
    expect(engineStateText(engine({ status: 'skipped' }))).toBe('skipped');
    expect(engineStateText(engine({ status: 'errored', startedAt: 0 + 1, finishedAt: 2001 }))).toBe('errored · 2.0 s');
  });

  it("a finding's place: file and line with the rule's last part, a dependency's manifest with its advisories", () => {
    expect(placeText(finding({ engine: 'secrets', file: 'config/dev.env', line: 4, rule: 'aws-access-token' }))).toBe('config/dev.env:4 · aws-access-token');
    expect(placeText(finding({ file: 'run.go', line: 5, rule: 'go.lang.security.audit.dangerous-exec-command' }))).toBe('run.go:5 · dangerous-exec-command');
    expect(placeText(finding({ file: 'run.go', rule: 'r' }))).toBe('run.go · r');
    expect(placeText(finding({ engine: 'deps', file: 'go.mod', rule: 'Go', ids: ['GO-2021-0113', 'CVE-2021-38561'] }))).toBe('go.mod · GO-2021-0113, CVE-2021-38561');
    expect(placeText(finding({ engine: 'deps', package: 'left-pad', version: '1.0.0', rule: 'npm', ids: [] }))).toBe('left-pad@1.0.0 · npm');
    expect(shortRule('a.b.c')).toBe('c');
    expect(shortRule('aws-access-token')).toBe('aws-access-token');
    // a dependency's title loses the ids its place already names
    expect(findingTitle(finding({ engine: 'deps', title: 'x 1.0 has known vulnerabilities: A-1, B-2', ids: ['A-1', 'B-2'] }))).toBe('x 1.0 has known vulnerabilities');
    expect(findingTitle(finding({ engine: 'deps', title: 'x 1.0 has known vulnerabilities: A-1', ids: ['B-2'] }))).toBe('x 1.0 has known vulnerabilities: A-1');
    expect(findingTitle(finding({ title: 'A command is built from input' }))).toBe('A command is built from input');
    expect(findingId(finding({ fingerprint: 'abc' }))).toBe('abc');
    expect(findingId(finding({ file: 'a.go', line: 3, rule: 'r' }))).toBe('sast:r:a.go:3:');
    expect(reportTarget({ projectId: 'p1', dir: 'C:\\x' })).toEqual({ projectId: 'p1' });
    expect(reportTarget({ projectId: '', dir: 'C:\\x' })).toEqual({ folder: 'C:\\x' });
  });

  it("splits a line at the core's masks, so a secret is drawn as a chip and never as text", () => {
    expect(lineParts('AWS_ACCESS_KEY_ID=[REDACTED:aws-access-token]')).toEqual([{ text: 'AWS_ACCESS_KEY_ID=' }, { mask: 'aws-access-token' }]);
    expect(lineParts('a [REDACTED] b [REDACTED:secret] c')).toEqual([{ text: 'a ' }, { mask: 'secret' }, { text: ' b ' }, { mask: 'secret' }, { text: ' c' }]);
    expect(lineParts('[REDACTED:secret]')).toEqual([{ mask: 'secret' }]);
    expect(lineParts('')).toEqual([{ text: '' }]);
    expect(lineParts('plain')).toEqual([{ text: 'plain' }]);
  });

  it('links an advisory to osv.dev and a weakness to cwe.mitre.org', () => {
    expect(refUrl('GO-2021-0113')).toBe('https://osv.dev/vulnerability/GO-2021-0113');
    expect(refUrl('CVE-2021-38561')).toBe('https://osv.dev/vulnerability/CVE-2021-38561');
    expect(refUrl('GHSA-abcd-efgh-ijkl')).toBe('https://osv.dev/vulnerability/GHSA-abcd-efgh-ijkl');
    expect(refUrl("CWE-78: Improper Neutralization of Special Elements used in an OS Command ('OS Command Injection')")).toBe('https://cwe.mitre.org/data/definitions/78.html');
    expect(refUrl('a note')).toBeUndefined();
    expect(refLabel('CWE-78: Improper Neutralization')).toBe('CWE-78');
    expect(refLabel('GO-2021-0113')).toBe('GO-2021-0113');
  });

  it('gives a task to a builder or backend agent first, a reviewer last, never the lead', () => {
    const a = (id: string, role: string) => ({ id, name: id, role }) as never;
    const list = taskAgents([a('lead', 'Team Lead'), a('rev', 'reviewer'), a('pm', 'PM'), a('be', 'Backend'), a('fe', 'frontend'), a('b', 'builder')]);
    expect(list.map((x: { id: string }) => x.id)).toEqual(['be', 'b', 'fe', 'pm', 'rev']);
    expect(taskAgents([a('m', 'manager')])).toEqual([]);
  });

  it('when, names and tones of the recent scans', () => {
    const now = 1_700_000_000_000;
    expect(agoText(now - 5_000, now)).toBe('now');
    expect(agoText(now - 5 * 60_000, now)).toBe('5 min ago');
    expect(agoText(now - 60 * 60_000, now)).toBe('1 h ago');
    expect(agoText(now - 30 * 3_600_000, now)).toBe('yesterday');
    expect(agoText(now - 5 * 86_400_000, now)).toBe(formatDate(now - 5 * 86_400_000));
    expect(folderName('C:\\code\\shop-api')).toBe('shop-api');
    expect(folderName('C:\\Users\\kyle\\Downloads\\left-pad-fork\\')).toBe('left-pad-fork');
    expect(folderName('/home/k/repo')).toBe('repo');
    expect(summaryTone({ status: 'done', counts: counts({ high: 1, total: 1 }) })).toBe('err');
    expect(summaryTone({ status: 'done', counts: counts({ critical: 1, total: 1 }) })).toBe('err');
    expect(summaryTone({ status: 'done', counts: counts({ medium: 1, total: 1 }) })).toBe('warn');
    expect(summaryTone({ status: 'done', counts: counts() })).toBe('ok');
    expect(summaryTone({ status: 'running', counts: counts() })).toBe('accent');
    expect(summaryTone({ status: 'cancelled', counts: counts() })).toBe('muted');
  });

  it('the line above the findings', () => {
    expect(lineText(undefined)).toBe('No scan yet');
    expect(lineText(report({ counts: counts({ high: 3, medium: 1, total: 4 }) }))).toBe('4 findings');
    expect(lineText(report({}))).toBe('Nothing found');
    expect(lineText(report({ status: 'running', engines: [engine({ status: 'done' })] }))).toBe('Scanning… 1 of 1 engines finished');
    expect(lineText(report({ status: 'cancelled' }))).toBe('Cancelled after 2.3 s');
  });
});

describe('emptyFindingsText', () => {
  const eng = (id: 'secrets' | 'deps' | 'sast', name: string, status: string) => ({ id, name, tool: '', status, findings: 0 }) as never;
  it('says nothing found only when every engine ran, and names what did not', () => {
    expect(emptyFindingsText({ status: 'done', engines: [eng('secrets', 'Secrets', 'done'), eng('deps', 'Dependencies', 'done'), eng('sast', 'Code', 'done')] })).toBe('Nothing found.');
    expect(emptyFindingsText({ status: 'done', engines: [eng('secrets', 'Secrets', 'done'), eng('deps', 'Dependencies', 'skipped'), eng('sast', 'Code', 'errored')] })).toBe(
      'Nothing found by Secrets; Dependencies and Code did not run (see above).',
    );
    expect(emptyFindingsText({ status: 'done', engines: [eng('secrets', 'Secrets', 'errored'), eng('deps', 'Dependencies', 'skipped')] })).toBe('No findings to show: no engine ran (see the reasons above).');
    expect(emptyFindingsText({ status: 'cancelled', engines: [eng('secrets', 'Secrets', 'cancelled')] })).toBe('No findings to show: the scan was cancelled before it finished.');
  });
});

describe('toolsUpdateText', () => {
  it('says, per tool, the newest it has, an install under way, a failure, a copy Drydock does not manage; Gitleaks comes with the app', () => {
    const gl = { tool: 'gitleaks', builtIn: true, found: true, version: '8.21.0' };
    expect(
      toolsUpdateText({
        gitleaks: gl,
        osv: { tool: 'osv-scanner', found: true, source: 'installed', tag: 'v2.3.0' },
        opengrep: { tool: 'opengrep', found: true, source: 'installed', tag: 'v1.10.0', job: { state: 'failed', error: 'GitHub answered 503', done: 0, total: 0, started: '' } },
      }),
    ).toBe('OSV-Scanner v2.3.0 is the newest. Opengrep could not be updated: GitHub answered 503. Gitleaks 8.21.0 is built into Drydock and updates with the app.');
    expect(
      toolsUpdateText({ gitleaks: { ...gl, version: '' }, osv: { tool: 'osv-scanner', found: false }, opengrep: { tool: 'opengrep', found: true, source: 'chosen', version: '1.9.0' } }),
    ).toBe('OSV-Scanner is not installed. Opengrep 1.9.0 is your own copy, which Drydock leaves alone. Gitleaks is built into Drydock and updates with the app.');
  });
});
