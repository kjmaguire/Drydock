// Pure helpers for the Support bundle screen.

import type { BundleFileReport, BundleReport } from '../../api/pkgTypes.ts';
import { numOrUndef } from '../settings/forms.ts';

export interface BundleOpts {
  eventLimit: number | undefined;
  includeRun: string | undefined;
}

/** The options a preview and a create send: '' means the core's default, a run id is trimmed. */
export function bundleOpts(events: string, run: string): BundleOpts {
  return { eventLimit: numOrUndef(events), includeRun: run.trim() || undefined };
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
