// Pure helpers for the Backups screen.

import type { BackupInfo, BackupResult, BackupSettings, BackupVerify, RestoreStatus } from '../../api/pkgTypes.ts';
import { formatBytes } from '../../lib/format.ts';
import { numOrUndef } from '../settings/forms.ts';

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

export function settingsPayload(f: ScheduleForm): { enabled: boolean; dir: string; daily: number | undefined; weekly: number | undefined } {
  return { enabled: f.enabled, dir: f.dir.trim(), daily: numOrUndef(f.daily), weekly: numOrUndef(f.weekly) };
}
