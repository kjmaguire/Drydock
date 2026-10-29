// Pure words and values for the diagnostics settings (decision P121): compression of the OTLP bodies, "Also send what was
// recorded before" and the crash report and usage counter categories.

import type { TelemetryBackfillResult, TelemetryCompression, TelemetryOtlpStatus } from '../../api/extTypes.ts';

/** The most rows "Also send what was recorded before" may send, and the default (internal/telemetry). */
export const MAX_BACKFILL_ROWS = 1000;
export const DEFAULT_BACKFILL_ROWS = 500;
/** How far back telemetry is kept, so how far back it can be sent. */
export const RETENTION_DAYS = 30;

export const COMPRESSION_OPTIONS: { value: TelemetryCompression; label: string; hint: string }[] = [
  { value: '', label: 'Automatic', hint: 'gzip for https, none for a collector on this computer' },
  { value: 'gzip', label: 'gzip', hint: 'Always compress' },
  { value: 'none', label: 'None', hint: 'Never compress' },
];

/** What the status says about compression. */
export function compressionWords(st: Pick<TelemetryOtlpStatus, 'compression' | 'gzip'>): string {
  const how = st.gzip ? 'Bodies are compressed with gzip' : 'Bodies are not compressed';
  return st.compression ? `${how} (you chose ${st.compression}).` : `${how} (automatic).`;
}

const pad = (n: number) => String(n).padStart(2, '0');

/** The local date `days` before now, as a date field holds it (YYYY-MM-DD). */
export function dateDaysAgo(now: number, days: number): string {
  const d = new Date(now - days * 86_400_000);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** The start of a date field's day in local time, in ms; undefined for anything that is not a date. */
export function sinceFromDate(value: string): number | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value.trim());
  if (!m) return undefined;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return Number.isNaN(d.getTime()) || d.getDate() !== Number(m[3]) ? undefined : d.getTime();
}

/** Why a backfill choice cannot be sent, or undefined. The core checks the same bounds. */
export function backfillProblem(since: number | undefined, rows: number, now: number): string | undefined {
  if (since === undefined) return 'Give the date to send from.';
  if (since > now) return 'The date to send from is in the future.';
  if (since < now - RETENTION_DAYS * 86_400_000 - 86_400_000) return `Telemetry keeps ${RETENTION_DAYS} days: choose a date within them.`;
  if (!Number.isInteger(rows) || rows < 1 || rows > MAX_BACKFILL_ROWS) return `Send between 1 and ${MAX_BACKFILL_ROWS} rows.`;
  return undefined;
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`;

/** The line about the last "Also send what was recorded before". */
export function backfillWords(b: TelemetryBackfillResult, date: (ms: number) => string): string {
  const head = `Also sent what was recorded before: ${plural(b.queued, 'row')} since ${date(b.sinceMs)} queued`;
  return b.left > 0 ? `${head}; ${plural(b.left, 'more row')} in that time were left out by the limit of ${b.maxRows}.` : `${head}.`;
}

/** What the crash reports' row says. */
export function crashWords(mode: 'local' | 'off', kept: number): string {
  const keptWords = kept === 0 ? 'None is kept now.' : `${plural(kept, 'report')} kept now (Support page).`;
  return mode === 'local'
    ? `When Drydock stops unexpectedly, a report stays on this computer. It leaves only in a support bundle you send. ${keptWords}`
    : `Off: a crash leaves no report, and support bundles carry none. ${keptWords}`;
}
