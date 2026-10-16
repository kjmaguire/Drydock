// Pure helpers for the Backups screen.

import type { BackupInfo, BackupResult, BackupSettings, BackupVerify, RestoreStatus } from '../../api/pkgTypes.ts';
import { formatBytes } from '../../lib/format.ts';
import { parseWhole } from '../../lib/limits.ts';

/** A verification belongs to the file as it was when checked (name, time, size): a pruned or replaced file loses it. */
export function fingerprint(b: BackupInfo): string {
  return `${b.created}|${b.size}`;
}

export interface Checked {
  v: BackupVerify;
  fp: string;
}

/** The verifications that still match a listed file, by backup name. */
export function currentVerifications(backups: BackupInfo[], checked: Record<string, Checked>): Record<string, BackupVerify> {
  const out: Record<string, BackupVerify> = {};
  for (const b of backups) {
    const c = checked[b.name];
    if (c && c.fp === fingerprint(b)) out[b.name] = c.v;
  }
  return out;
}

/** Restore is offered only after a successful verify in this page that says this Drydock can restore it. */
export function canRestore(v: BackupVerify | undefined): boolean {
  return v?.ok === true && v.restorableHere === true;
}

export function restoreTitle(pending: RestoreStatus | undefined, ok: boolean): string {
  if (pending?.pending) return 'A restore is already staged; cancel it first';
  return ok ? 'Restore this backup (staged for the next start)' : 'Verify this backup first';
}

/** The line shown after "Back up now". */
export function madeMessage(r: BackupResult): string {
  let m = `Backup ${r.backup.name} written (${formatBytes(r.backup.size)}).`;
  if (r.pruned && r.pruned.length > 0) m += ` Removed ${r.pruned.length} old backup(s) beyond your retention.`;
  return m;
}

/** The form's starting values for a settings answer: the default folder shows as an empty field. */
export interface ScheduleForm {
  enabled: boolean;
  dir: string;
  daily: string;
  weekly: string;
}

export function formFrom(s: BackupSettings): ScheduleForm {
  return { enabled: s.enabled, dir: s.dir === s.defaultDir ? '' : s.dir, daily: String(s.daily), weekly: String(s.weekly) };
}

/** Changes when the core's settings change, so the form starts again from what the core holds. */
export function settingsKey(s: BackupSettings): string {
  return `${s.enabled}|${s.dir}|${s.daily}|${s.weekly}`;
}

// What a backup count says when its text is not a whole number (M25): a typo is never saved as "keep the old value".
export const DAILY_ERROR = 'Enter how many daily backups to keep as a whole number greater than zero, for example 7.';
export const WEEKLY_ERROR = 'Enter how many weekly backups to keep as a whole number, for example 4. Enter 0 to keep none.';

export interface ScheduleErrors {
  daily?: string;
  weekly?: string;
}

export interface SchedulePayload {
  enabled: boolean;
  dir: string;
  daily: number | undefined;
  weekly: number | undefined;
}

/**
 * The backup.settings.set request for the form, or what is wrong with it. An empty count is left as the core has it (undefined);
 * text that is not a whole number is an error under its field and nothing is sent. Daily is at least 1; weekly may be 0 (keep none).
 */
export function parseSchedule(f: ScheduleForm): { ok: true; payload: SchedulePayload } | { ok: false; errors: ScheduleErrors } {
  const daily = parseWhole(f.daily, 1, DAILY_ERROR);
  const weekly = parseWhole(f.weekly, 0, WEEKLY_ERROR);
  if (!daily.ok || !weekly.ok) return { ok: false, errors: { daily: daily.ok ? undefined : daily.error, weekly: weekly.ok ? undefined : weekly.error } };
  return { ok: true, payload: { enabled: f.enabled, dir: f.dir.trim(), daily: daily.value, weekly: weekly.value } };
}
