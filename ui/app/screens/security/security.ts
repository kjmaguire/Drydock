// Pure helpers of the Security scan screen: progress arithmetic, tones, and the words for counts, states and durations; the
// words of the engine tiles, a finding's place, the masked lines of the code around it, and who a task from it goes to.

import type { ScanTarget } from '../../api/securityClient.ts';
import type { DismissReason, EngineID, EngineResult, ScanCounts, ScanFinding, ScanReport, ScanSummary, SecurityTools, Severity, ToolInfo } from '../../api/securityTypes.ts';
import type { Agent } from '../../api/types.ts';
import { formatDate, formatDateTime } from '../../lib/format.ts';
import type { Tone } from '../../ui/tokens.ts';

/** The engines in the order they are shown. The core sends name and tool with each result; the blurb is the UI's. */
export const ENGINES: readonly { id: EngineID; name: string; tool: string; blurb: string }[] = [
  { id: 'secrets', name: 'Secrets', tool: 'gitleaks', blurb: 'Keys, tokens and passwords left in files.' },
  { id: 'deps', name: 'Dependencies', tool: 'osv-scanner', blurb: 'Packages in the lockfiles and manifests with known vulnerabilities.' },
  { id: 'sast', name: 'Code', tool: 'opengrep', blurb: 'Code patterns the built-in rules mark as unsafe.' },
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

// ---- The graphite screen (decision P97): tiles, the findings list, the finding's pane, the recent scans.

/** The screen is wide enough for the list and the finding's pane side by side (the bases of the two columns, 560 + 380 px). */
export const SPLIT_QUERY = '@container (min-width: 940px)';

const SEVERITY_LABEL: Record<string, string> = { critical: 'Critical', high: 'High', medium: 'Medium', low: 'Low', info: 'Info' };

/** "High", "Critical": a severity as its badge says it. */
export const severityLabel = (s: Severity | string): string => SEVERITY_LABEL[s] ?? (s ? `${s.charAt(0).toUpperCase()}${s.slice(1)}` : '');

/** The words after an engine's count on its tile: "1 secret, value never shown", "2 unsafe patterns". */
export function engineWords(id: EngineID, n: number): string {
  const one = n === 1;
  switch (id) {
    case 'secrets':
      return one ? 'secret, value never shown' : 'secrets, values never shown';
    case 'deps':
      return one ? 'vulnerable package' : 'vulnerable packages';
    default:
      return one ? 'unsafe pattern' : 'unsafe patterns';
  }
}

/** A tool as its own project writes its name (Opengrep with a capital; gitleaks and osv-scanner in lower case). */
export const toolLabel = (tool: string): string => (tool === 'opengrep' ? 'Opengrep' : tool);

/**
 * What Update scanners did, one sentence per tool: OSV-Scanner and Opengrep are updated by Drydock (the newest release is
 * installed in the background), Gitleaks is built into Drydock and updates with the app.
 */
export function toolsUpdateText(tools: Pick<SecurityTools, 'gitleaks' | 'osv' | 'opengrep'>): string {
  const one = (name: string, x: ToolInfo): string => {
    const at = x.tag || x.version;
    if (x.job?.state === 'running') return `${name}: installing ${x.job.tag || x.latest || 'the newest release'}`;
    if (x.job?.state === 'failed') return `${name} could not be updated${x.job.error ? `: ${x.job.error}` : ''}`;
    if (!x.found) return `${name} is not installed`;
    if (x.source && x.source !== 'installed') return `${name}${at ? ` ${at}` : ''} is your own copy, which Drydock leaves alone`;
    return at ? `${name} ${at} is the newest` : `${name} is the newest`;
  };
  const gl = tools.gitleaks.version ? `Gitleaks ${tools.gitleaks.version}` : 'Gitleaks';
  return `${[one('OSV-Scanner', tools.osv), one('Opengrep', tools.opengrep), `${gl} is built into Drydock and updates with the app`].join('. ')}.`;
}

/** An engine's state on its tile: "done · 0.1 s", "running · 10 of 20 files", "waiting", "skipped", "errored · 2.0 s". */
export function engineStateText(e: EngineResult): string {
  const took = elapsed(e);
  switch (e.status) {
    case 'done':
      return took === undefined ? 'done' : `done · ${durationText(took)}`;
    case 'running':
      return engineDeterminate(e) ? `running · ${filesText(e)}` : 'running';
    case 'pending':
      return 'waiting';
    case 'errored':
    case 'cancelled':
      return took === undefined ? e.status : `${e.status} · ${durationText(took)}`;
    default:
      return e.status;
  }
}

/** The last part of a rule's id: "dangerous-exec-command" for "go.lang.security.audit.dangerous-exec-command". */
export const shortRule = (rule: string): string => rule.split('.').filter(Boolean).at(-1) ?? rule;

/** A finding's place, the list's mono line: "config/dev.env:4 · aws-access-token", "go.mod · GO-2021-0113, CVE-2021-38561". */
export function placeText(f: ScanFinding): string {
  if (f.engine === 'deps') {
    const ids = f.ids && f.ids.length > 0 ? f.ids.join(', ') : f.rule;
    return [f.file ?? (f.package ? `${f.package}${f.version ? `@${f.version}` : ''}` : ''), ids].filter(Boolean).join(' · ');
  }
  const where = f.file ? (f.line ? `${f.file}:${f.line}` : f.file) : '';
  return [where, shortRule(f.rule)].filter(Boolean).join(' · ');
}

/** A finding's title; a dependency's without the advisory ids its place already names. */
export function findingTitle(f: ScanFinding): string {
  if (f.engine === 'deps' && f.ids && f.ids.length > 0) {
    const tail = `: ${f.ids.join(', ')}`;
    if (f.title.endsWith(tail)) return f.title.slice(0, -tail.length);
  }
  return f.title;
}

/** Names a finding in the list: its fingerprint, else (a report from before fingerprints) its engine, rule and place. */
export const findingId = (f: ScanFinding): string => f.fingerprint || `${f.engine}:${f.rule}:${f.file ?? ''}:${f.line ?? 0}:${f.package ?? ''}`;

/** The project or folder a report's findings are dismissed under. */
export const reportTarget = (r: Pick<ScanReport, 'projectId' | 'dir'>): ScanTarget => (r.projectId ? { projectId: r.projectId } : { folder: r.dir });

/** A line of the code around a finding, as text and the masks the core put in place of each secret ("[REDACTED:<rule>]"). */
export type LinePart = { text: string } | { mask: string };

const MASK = /\[REDACTED(?::([^\]]*))?\]/g;

/** Splits a line at its masks: the page draws each as a "hidden" chip, never as the text the core wrote. */
export function lineParts(text: string): LinePart[] {
  const out: LinePart[] = [];
  let at = 0;
  for (const m of text.matchAll(MASK)) {
    if (m.index > at) out.push({ text: text.slice(at, m.index) });
    out.push({ mask: m[1] ?? 'secret' });
    at = m.index + m[0].length;
  }
  if (at < text.length || out.length === 0) out.push({ text: text.slice(at) });
  return out;
}

/** Where a reference of the advice is read: an advisory on osv.dev, a weakness on cwe.mitre.org; else nowhere. */
export function refUrl(ref: string): string | undefined {
  const cwe = /^CWE-(\d+)/i.exec(ref.trim());
  if (cwe) return `https://cwe.mitre.org/data/definitions/${cwe[1]}.html`;
  if (/^(GO|CVE|GHSA|PYSEC|RUSTSEC|OSV|GSD|MAL|RLSA|ALSA|DLA|DSA|USN)-[\w.-]+$/i.test(ref.trim())) return advisoryUrl(ref.trim());
  return undefined;
}

/** A reference as its link says it: "CWE-78" for "CWE-78: Improper Neutralization of ...". */
export const refLabel = (ref: string): string => /^(CWE-\d+)/i.exec(ref.trim())?.[1] ?? ref.trim();

const LEAD_ROLES = new Set(['lead', 'team lead', 'teamlead', 'manager', 'supervisor', 'engineering manager']);
const BUILD_ROLES = new Set(['builder', 'build', 'backend', 'back end', 'backend engineer', 'developer', 'dev', 'engineer', 'software engineer', 'coder', 'fullstack', 'full stack', 'implementer']);
const normRole = (r: string): string => r.trim().toLowerCase().replace(/[_-]/g, ' ').split(/\s+/).filter(Boolean).join(' ');
function roleRank(role: string): number {
  const r = normRole(role);
  if (BUILD_ROLES.has(r)) return 0;
  if (r === 'frontend' || r === 'front end' || r === 'frontend engineer') return 1;
  if (r === 'reviewer' || r === 'review') return 4;
  return r === 'security' || r === 'security engineer' ? 2 : 3;
}

/**
 * The agents a task from a finding can go to, the one it goes to first: a builder or backend agent before the others, a
 * reviewer last, never the team lead (the core gives the task another agent of the team to review).
 */
export function taskAgents(agents: readonly Agent[]): Agent[] {
  return agents
    .filter((a) => !LEAD_ROLES.has(normRole(a.role)))
    .map((a, i) => ({ a, i, r: roleRank(a.role) }))
    .sort((x, y) => x.r - y.r || x.i - y.i)
    .map((x) => x.a);
}

/** Why a finding was dismissed, in words. */
export const DISMISS_WORDS: Record<DismissReason, string> = {
  not_real: 'not real',
  false_positive: 'a false positive',
  accepted_risk: 'an accepted risk',
};

/** When a scan ran, as a chip says it: "now", "5 min ago", "2 h ago", "yesterday", else the date. */
export function agoText(ms: number, now: number = Date.now()): string {
  const d = now - ms;
  if (d < 60_000) return 'now';
  if (d < 3_600_000) return `${Math.floor(d / 60_000)} min ago`;
  if (d < 86_400_000) return `${Math.floor(d / 3_600_000)} h ago`;
  if (d < 2 * 86_400_000) return 'yesterday';
  return formatDate(ms);
}

/** The tone of a scan's count on its chip: red with something high or critical, amber with anything else, green with nothing. */
export function summaryTone(h: Pick<ScanSummary, 'status' | 'counts'>): Tone {
  if (h.status === 'running') return 'accent';
  if (h.status !== 'done') return 'muted';
  if (h.counts.critical + h.counts.high > 0) return 'err';
  return h.counts.total > 0 ? 'warn' : 'ok';
}

/** A folder by its last part ("shop-api" for C:\code\shop-api). */
export function folderName(path: string): string {
  return path.split(/[\\/]+/).filter(Boolean).at(-1) ?? path;
}

/** The words of the line above the findings: "4 findings", "Nothing found", "Scanning… 1 of 3 engines finished", "No scan yet". */
export function lineText(report: ScanReport | null | undefined): string {
  if (!report) return 'No scan yet';
  if (report.status === 'running') return statusText(report);
  if (report.status === 'done') {
    const n = report.counts.total;
    return n > 0 ? findingsText(n) : 'Nothing found';
  }
  return statusText(report).replace(/\.$/, '');
}
