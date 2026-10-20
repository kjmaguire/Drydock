// Pure helpers of the Security scan screen: progress arithmetic, tones, and the words for counts, states and durations.

import type { EngineID, EngineResult, ScanCounts, ScanFinding, ScanReport, Severity } from '../../api/securityTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import type { Tone } from '../../ui/tokens.ts';

/** The engines in the order they are shown. The core sends name and tool with each result; the blurb is the UI's. */
export const ENGINES: readonly { id: EngineID; name: string; tool: string; blurb: string }[] = [
  { id: 'secrets', name: 'Secrets', tool: 'gitleaks', blurb: 'Keys, tokens and passwords left in files.' },
  { id: 'deps', name: 'Dependencies', tool: 'osv-scanner', blurb: 'Packages in the lockfiles and manifests with known vulnerabilities.' },
  { id: 'sast', name: 'Code', tool: 'opengrep', blurb: 'Code patterns the rules folder marks as unsafe.' },
];

export const SEVERITIES: readonly Severity[] = ['critical', 'high', 'medium', 'low', 'info'];

const FINISHED = new Set(['done', 'skipped', 'errored', 'cancelled']);

/** True once an engine will not report anything more. */
export const engineFinished = (e: Pick<EngineResult, 'status'>): boolean => FINISHED.has(e.status);

/** True when a running engine counts its files (the secrets engine); the others cannot. */
export const engineDeterminate = (e: Pick<EngineResult, 'status' | 'total'>): boolean => e.status === 'running' && (e.total ?? 0) > 0;

/** 0..1: a finished engine counts 1, a running one counts done/total when it counts files and 0 when it cannot, pending is 0. */
export function engineFraction(e: Pick<EngineResult, 'status' | 'done' | 'total'>): number {
  if (engineFinished(e)) return 1;
  if (!engineDeterminate(e)) return 0;
  return Math.min(1, Math.max(0, (e.done ?? 0) / (e.total ?? 1)));
}

/** The whole scan, 0..100: the average of the engines' fractions; a report that is no longer running is complete. */
export function overallPercent(report: Pick<ScanReport, 'status' | 'engines'> | undefined): number {
  if (!report) return 0;
  if (report.status !== 'running') return 100;
  const list = report.engines ?? [];
  if (list.length === 0) return 0;
  const sum = list.reduce((acc, e) => acc + engineFraction(e), 0);
  return Math.round((sum / list.length) * 100);
}

export function engineTone(e: Pick<EngineResult, 'status' | 'findings'>): Tone {
  switch (e.status) {
    case 'done':
      return e.findings > 0 ? 'warn' : 'ok';
    case 'running':
      return 'accent';
    case 'errored':
      return 'err';
    default:
      return 'muted';
  }
}

export function severityTone(s: Severity | string): Tone {
  switch (s) {
    case 'critical':
    case 'high':
      return 'err';
    case 'medium':
      return 'warn';
    case 'low':
      return 'info';
    default:
      return 'muted';
  }
}

/** "1 high, 2 medium", or "nothing found". */
export function countsText(counts: ScanCounts | undefined): string {
  if (!counts) return 'nothing found';
  const parts = SEVERITIES.filter((s) => counts[s] > 0).map((s) => `${counts[s].toLocaleString()} ${s}`);
  return parts.length === 0 ? 'nothing found' : parts.join(', ');
}

/** "3 findings", "1 finding", "nothing found". */
export function findingsText(n: number): string {
  if (n <= 0) return 'nothing found';
  return `${n.toLocaleString()} ${n === 1 ? 'finding' : 'findings'}`;
}

/** Who started a scan, without the "human:" prefix the core records. */
export function byName(by: string | undefined): string {
  if (!by) return '';
  return by.startsWith('human:') ? by.slice('human:'.length) : by;
}

/** A duration in words: "420 ms", "2.3 s", "1 min 5 s". */
export function durationText(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return '';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms - m * 60_000) / 1000);
  return s > 0 ? `${m} min ${s} s` : `${m} min`;
}

/** How long an engine or a scan took, when it finished. */
export const elapsed = (x: { startedAt?: number; finishedAt?: number }): number | undefined =>
  x.startedAt && x.finishedAt && x.finishedAt >= x.startedAt ? x.finishedAt - x.startedAt : undefined;

/** The status line next to the Scan button. */
export function statusText(report: ScanReport | undefined | null, when: (ms: number) => string = formatDateTime): string {
  if (!report) return 'No scan yet.';
  const engines = report.engines ?? [];
  switch (report.status) {
    case 'running': {
      const n = engines.filter(engineFinished).length;
      return `Scanning… ${n} of ${engines.length || 3} engines finished`;
    }
    case 'done': {
      const what = report.summary?.trim() || (report.counts.total > 0 ? `Found ${countsText(report.counts)}.` : 'Nothing found.');
      return `${what} Started ${when(report.startedAt)} by ${byName(report.by)}.`;
    }
    case 'cancelled': {
      const d = elapsed(report);
      return d === undefined ? 'Cancelled.' : `Cancelled after ${durationText(d)}.`;
    }
    default:
      return report.status ? `${report.status.charAt(0).toUpperCase()}${report.status.slice(1)}.` : '';
  }
}

const RANK: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3, info: 4 };
const rank = (s: string): number => RANK[s] ?? 9;
const place = (f: ScanFinding): string => f.file ?? f.package ?? '';

/** Severity first (critical on top), then file, then line. Returns a new list. */
export function sortFindings(list: readonly ScanFinding[]): ScanFinding[] {
  return [...list].sort((a, b) => rank(a.severity) - rank(b.severity) || place(a).localeCompare(place(b)) || (a.line ?? 0) - (b.line ?? 0) || a.rule.localeCompare(b.rule));
}

export function filterFindings(list: readonly ScanFinding[], sev: Severity | 'all', engine: EngineID | 'all'): ScanFinding[] {
  return list.filter((f) => (sev === 'all' || f.severity === sev) && (engine === 'all' || f.engine === engine));
}

/** How many findings each engine has in a list. */
export function engineCounts(list: readonly ScanFinding[]): Record<EngineID, number> {
  const out: Record<EngineID, number> = { secrets: 0, deps: 0, sast: 0 };
  for (const f of list) if (f.engine in out) out[f.engine]++;
  return out;
}

/** "notes.txt:1" for a file finding, "pkg@1.2.3" for a dependency, else "". */
export function whereText(f: ScanFinding): string {
  if (f.package) {
    const pkg = f.version ? `${f.package}@${f.version}` : f.package;
    return f.file ? `${pkg} in ${f.file}` : pkg;
  }
  if (f.file) return f.line ? `${f.file}:${f.line}` : f.file;
  return '';
}

/** The osv.dev page of an advisory id. */
export const advisoryUrl = (id: string): string => `https://osv.dev/vulnerability/${encodeURIComponent(id)}`;

/** "1,204 of 5,330 files" while the secrets engine runs. */
export const filesText = (e: Pick<EngineResult, 'done' | 'total'>): string => `${(e.done ?? 0).toLocaleString()} of ${(e.total ?? 0).toLocaleString()} files`;

/**
 * What an empty findings list means. "Nothing found." only when the scan ran to the end and at least one engine looked;
 * a cancelled scan or one where every engine was skipped or broke checked nothing, and says so (never a pass).
 */
export function emptyFindingsText(report: Pick<ScanReport, 'status' | 'engines'>): string {
  if (report.status === 'cancelled') return 'No findings to show: the scan was cancelled before it finished.';
  const ran = report.engines.filter((e) => e.status === 'done');
  if (ran.length === 0) return 'No findings to show: no engine ran (see the reasons above).';
  if (ran.length < report.engines.length) {
    const idle = report.engines.filter((e) => e.status !== 'done').map((e) => e.name);
    return `Nothing found by ${ran.map((e) => e.name).join(' and ')}; ${idle.join(' and ')} did not run (see above).`;
  }
  return 'Nothing found.';
}

/** A rule set's language as people write it (Opengrep names them in lower case: csharp, cpp). */
export function languageName(id: string): string {
  const names: Record<string, string> = { javascript: 'JavaScript', typescript: 'TypeScript', python: 'Python', go: 'Go', java: 'Java', csharp: 'C#', c: 'C', cpp: 'C++' };
  return names[id] ?? id;
}
