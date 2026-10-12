import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { EngineResult, ScanCounts, ScanFinding, ScanReport } from '../../api/securityTypes.ts';
import {
  byName,
  countsText,
  durationText,
  engineFraction,
  engineTone,
  filterFindings,
  findingsText,
  overallPercent,
  severityTone,
  sortFindings,
  statusText,
  whereText,
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

  it('filters by severity and engine', () => {
    expect(filterFindings(list, 'all', 'all')).toHaveLength(6);
    expect(filterFindings(list, 'high', 'all').map((f) => f.rule)).toEqual(['d', 'e', 'f']);
    expect(filterFindings(list, 'all', 'sast').map((f) => f.rule)).toEqual(['b', 'e', 'f', 'a']);
    expect(filterFindings(list, 'high', 'secrets').map((f) => f.rule)).toEqual(['d']);
    expect(filterFindings(list, 'info', 'all')).toEqual([]);
  });
});
