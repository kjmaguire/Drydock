// Pure helpers for the Support bundle screen.

import type { BundleFileReport, BundleReport } from '../../api/pkgTypes.ts';
import { parseWhole } from '../../lib/limits.ts';

export interface BundleOpts {
  eventLimit: number | undefined;
  includeRun: string | undefined;
}

/** What the events field says when its text is not a whole number (M25): a typo is never turned into the core's default. */
export const EVENTS_ERROR = 'Enter how many recent events to list as a whole number greater than zero, for example 500, or leave it empty for the default.';

/**
 * The options a preview and a create send: '' means the core's default, a run id is trimmed. Text in the events field that is not
 * a whole number is an error to show under the field, and nothing is sent.
 */
export function bundleOpts(events: string, run: string): { ok: true; opts: BundleOpts } | { ok: false; error: string } {
  const n = parseWhole(events, 1, EVENTS_ERROR);
  if (!n.ok) return { ok: false, error: n.error };
  return { ok: true, opts: { eventLimit: n.value, includeRun: run.trim() || undefined } };
}

/** A preview belongs to the choices it was made for. */
export function bundleKey(events: string, run: string): string {
  return `${events}|${run.trim()}`;
}

/** The files where something was redacted or replaced (the only ones worth a row in the report). */
export function scrubbedFiles(r: BundleReport): BundleFileReport[] {
  return (r.files ?? []).filter((f) => f.secretsRedacted > 0 || Object.keys(f.identityTokens ?? {}).length > 0);
}

/** "user: 3, host: 1", or "none". */
export function identityText(f: BundleFileReport): string {
  return (
    Object.entries(f.identityTokens ?? {})
      .map(([k, n]) => `${k}: ${n}`)
      .join(', ') || 'none'
  );
}
