import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { BackupInfo, BackupSettings } from '../../api/pkgTypes.ts';
import { canRestore, currentVerifications, DAILY_ERROR, fingerprint, formFrom, madeMessage, parseSchedule, restoreTitle, settingsKey, WEEKLY_ERROR } from './backups.ts';

const b = (over: Partial<BackupInfo> = {}): BackupInfo => ({ path: 'p', name: 'b1.db', size: 2048, sha256: 'x', created: '2026-01-02T03:04:05Z', label: '', keep: false, ...over });
const st = (over: Partial<BackupSettings> = {}): BackupSettings => ({ enabled: true, dir: 'C:\\b', defaultDir: 'C:\\b', daily: 7, weekly: 4, ...over });

describe('backups helpers', () => {
  it('keeps a verification only while the file is the one that was checked', () => {
    const v = { name: 'b1.db', size: 2048, created: '', ok: true, restorableHere: true };
    const checked = { 'b1.db': { v, fp: fingerprint(b()) } };
    expect(currentVerifications([b()], checked)).toEqual({ 'b1.db': v });
    expect(currentVerifications([b({ size: 4096 })], checked)).toEqual({});
    expect(currentVerifications([], checked)).toEqual({});
  });

  it('allows restore only for an ok verify this Drydock can restore', () => {
    expect(canRestore(undefined)).toBe(false);
    expect(canRestore({ name: 'x', size: 1, created: '', ok: false })).toBe(false);
    expect(canRestore({ name: 'x', size: 1, created: '', ok: true, restorableHere: false })).toBe(false);
    expect(canRestore({ name: 'x', size: 1, created: '', ok: true, restorableHere: true })).toBe(true);
    expect(restoreTitle({ pending: true }, true)).toContain('already staged');
    expect(restoreTitle({ pending: false }, false)).toBe('Verify this backup first');
  });

  it('words a new backup and the pruned ones', () => {
    expect(madeMessage({ backup: b(), pruned: null })).toBe('Backup b1.db written (2.0 KB).');
    expect(madeMessage({ backup: b(), pruned: ['a', 'b'] })).toContain('Removed 2 old backup(s)');
  });

  it('the form shows the default folder as empty and sends numbers or nothing', () => {
    expect(formFrom(st())).toEqual({ enabled: true, dir: '', daily: '7', weekly: '4' });
    expect(formFrom(st({ dir: 'D:\\x' })).dir).toBe('D:\\x');
    expect(parseSchedule({ enabled: false, dir: ' D:\\bk ', daily: '10', weekly: '' })).toEqual({ ok: true, payload: { enabled: false, dir: 'D:\\bk', daily: 10, weekly: undefined } });
    expect(settingsKey(st())).not.toBe(settingsKey(st({ daily: 8 })));
  });

  it('a count that is not a whole number is an error under its field, never "keep what the core has" (M25)', () => {
    for (const bad of ['abc', '1e3', '7.5', '1,000', '-1', '0', '7 days']) {
      expect(parseSchedule({ enabled: true, dir: '', daily: bad, weekly: '4' })).toEqual({ ok: false, errors: { daily: DAILY_ERROR, weekly: undefined } });
    }
    for (const bad of ['four', '1e3', '4.5', '1,000', '-1']) {
      expect(parseSchedule({ enabled: true, dir: '', daily: '7', weekly: bad })).toEqual({ ok: false, errors: { daily: undefined, weekly: WEEKLY_ERROR } });
    }
    expect(parseSchedule({ enabled: true, dir: '', daily: 'x', weekly: 'y' })).toEqual({ ok: false, errors: { daily: DAILY_ERROR, weekly: WEEKLY_ERROR } });
  });

  it('weekly may be 0 (keep none); daily may not; an empty count is left as the core has it', () => {
    expect(parseSchedule({ enabled: true, dir: '', daily: '1', weekly: '0' })).toEqual({ ok: true, payload: { enabled: true, dir: '', daily: 1, weekly: 0 } });
    expect(parseSchedule({ enabled: true, dir: '', daily: '', weekly: '' })).toEqual({ ok: true, payload: { enabled: true, dir: '', daily: undefined, weekly: undefined } });
    expect(DAILY_ERROR).toContain('greater than zero');
  });
});
