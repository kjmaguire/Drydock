import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { BackupList, BackupSettings, BackupVerify } from '../../api/pkgTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { BackupsScreen } from './backups-screen.tsx';

afterEach(cleanup);

const settings = (over: Partial<BackupSettings> = {}): BackupSettings => ({
  enabled: true,
  dir: 'C:\\data\\backups',
  defaultDir: 'C:\\data\\backups',
  daily: 7,
  weekly: 4,
  lastSuccess: 1760000000000,
  ...over,
});
const backups = (over: Partial<BackupList> = {}): BackupList => ({
  dir: 'C:\\data\\backups',
  backups: [{ path: 'C:\\data\\backups\\b1.db', name: 'b1.db', size: 2048, sha256: 'abc', created: '2026-01-02T03:04:05Z', label: 'manual', keep: false }],
  replaced: [],
  settings: settings(),
  ...over,
});
const notPending = async () => ({ pending: false });

const tid = (root: ParentNode, id: string) => {
  const el = root.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  if (!el) throw new Error(`no ${id}`);
  return el;
};
const none = (root: ParentNode, id: string) => root.querySelector(`[data-testid="${id}"]`) === null;
const btn = (root: ParentNode, name: string) => byText<HTMLButtonElement>(root, 'button', name);
const show = (client: Partial<Client>) => {
  const s = testServices({ backupRestoreStatus: notPending, ...client });
  return Object.assign(mount(<BackupsScreen />, s), { s });
};
const ok = (name: string): BackupVerify => ({ name, size: 2048, created: '', ok: true, schemaVersion: 30, restorableHere: true });

describe('BackupsScreen', () => {
  it('lists backups, the folder and last success', async () => {
    const r = show({ backupList: async () => backups() });
    const row = await waitFor(() => tid(r.container, 'backup-row'), r);
    expect(row.getAttribute('data-name')).toBe('b1.db');
    expect(row.textContent).toContain('2.0 KB');
    expect(tid(r.container, 'backup-dir').textContent).toBe('C:\\data\\backups');
    expect(none(r.container, 'backup-last-error')).toBe(true);
    expect(none(r.container, 'restore-pending')).toBe(true);
    expect(r.$('h1')!.textContent).toContain('Backups');
  });

  it('shows the location warning and the last error', async () => {
    const st = settings({ dir: 'C:\\Users\\a\\OneDrive\\b', warning: 'This folder is synced by OneDrive.', lastError: 'disk full' });
    const r = show({ backupList: async () => backups({ settings: st, warning: st.warning }) });
    expect((await waitFor(() => tid(r.container, 'backup-location-warning'), r)).textContent).toContain('synced by OneDrive');
    expect(tid(r.container, 'backup-last-error').textContent).toContain('disk full');
  });

  it('says so when the folder has no backups', async () => {
    const r = show({ backupList: async () => backups({ backups: [] }) });
    await waitFor(() => expect(r.container.textContent).toContain('No backups in this folder yet'), r);
  });

  it('backs up now with the label and reloads the list', async () => {
    const labels: (string | undefined)[] = [];
    let reads = 0;
    const r = show({
      backupList: async () => (reads++, backups()),
      backupNow: async (l) => (labels.push(l), { backup: backups().backups![0]!, pruned: ['old.db'] }),
    });
    await waitFor(() => tid(r.container, 'backup-row'), r);
    type(byLabel(r.container, /^Label \(optional/), 'before refactor');
    await r.act(() => btn(r.container, 'Back up now').click());
    const made = await waitFor(() => tid(r.container, 'backup-made'), r);
    expect(made.textContent).toContain('Backup b1.db written (2.0 KB).');
    expect(made.textContent).toContain('Removed 1 old backup(s)');
    expect(labels).toEqual(['before refactor']);
    await waitFor(() => expect(reads).toBeGreaterThan(1), r);
    expect(byLabel(r.container, /^Label \(optional/).value).toBe('');
  });

  it('requires a successful verify before restore, then explains the staged restore and stages it with confirm', async () => {
    const restores: [string, boolean][] = [];
    let pending = false;
    const r = show({
      backupList: async () => backups(),
      backupVerify: async (name) => ok(name),
      backupRestore: async (n, c) => {
        restores.push([n, c]);
        pending = true;
        return { staged: true, restartRequired: true, backup: n, schemaVersion: 30, message: 'The restore is staged. Quit and reopen Drydock to apply it.', warnings: ['1 workflow run(s) are running and will be interrupted'] };
      },
      backupRestoreStatus: async () => (pending ? { pending: true, backup: 'b1.db', requested: 1760000000000 } : { pending: false }),
    });
    const restore = await waitFor(() => btn(r.container, 'Restore b1.db'), r);
    expect(restore.disabled).toBe(true);
    expect(restore.title).toBe('Verify this backup first');
    await r.act(() => btn(r.container, 'Verify b1.db').click());
    expect((await waitFor(() => tid(r.container, 'verify-result'), r)).getAttribute('data-ok')).toBe('true');
    await waitFor(() => expect(btn(r.container, 'Restore b1.db').disabled).toBe(false), r);
    await r.act(() => btn(r.container, 'Restore b1.db').click());
    const c = await waitFor(() => tid(r.container, 'restore-confirm'), r);
    expect(c.textContent).toContain('staged, not applied now');
    expect(c.textContent).toContain('Nothing is destroyed');
    expect(r.container.querySelector('dialog')!.open).toBe(true);
    expect(restores.length).toBe(0);
    await r.act(() => btn(c, 'Stage this restore for the next start').click());
    await waitFor(() => expect(restores).toEqual([['b1.db', true]]), r);
    const p = await waitFor(() => tid(r.container, 'restore-pending'), r);
    expect(p.textContent).toContain('b1.db');
    expect(p.textContent).toContain('next time Drydock starts');
    expect(p.textContent).toContain('Nothing has changed yet');
    expect(tid(r.container, 'restore-staged').textContent).toContain('staged');
    expect(tid(r.container, 'restore-warnings').textContent).toContain('will be interrupted');
    expect(none(r.container, 'restore-confirm')).toBe(true);
    // a second restore cannot be staged on top
    expect(btn(r.container, 'Restore b1.db').disabled).toBe(true);
    expect(btn(r.container, 'Restore b1.db').title).toBe('A restore is already staged; cancel it first');
  });

  it('declining the confirmation stages nothing', async () => {
    let restores = 0;
    const r = show({ backupList: async () => backups(), backupVerify: async (n) => ok(n), backupRestore: async () => (restores++, null as never) });
    await r.act(async () => (await waitFor(() => btn(r.container, 'Verify b1.db'), r)).click());
    await waitFor(() => expect(btn(r.container, 'Restore b1.db').disabled).toBe(false), r);
    await r.act(() => btn(r.container, 'Restore b1.db').click());
    const c = await waitFor(() => tid(r.container, 'restore-confirm'), r);
    await r.act(() => btn(c, 'Do not restore').click());
    await waitFor(() => expect(none(r.container, 'restore-confirm')).toBe(true), r);
    expect(restores).toBe(0);
    expect(none(r.container, 'restore-pending')).toBe(true);
  });

  it('cancels a staged restore', async () => {
    let pending = true;
    let cancels = 0;
    const r = show({
      backupList: async () => backups(),
      backupRestoreCancel: async () => {
        cancels++;
        pending = false;
        return { cancelled: true };
      },
      backupRestoreStatus: async () => (pending ? { pending: true, backup: 'b1.db', requested: 1760000000000 } : { pending: false }),
    });
    await waitFor(() => tid(r.container, 'restore-pending'), r);
    await r.act(() => btn(r.container, 'Cancel the staged restore').click());
    await waitFor(() => expect(cancels).toBe(1), r);
    await waitFor(() => expect(none(r.container, 'restore-pending')).toBe(true), r);
  });

  it('keeps restore off for a damaged backup and shows why', async () => {
    const r = show({ backupList: async () => backups(), backupVerify: async (name) => ({ name, size: 1, created: '', ok: false, error: 'checksum mismatch' }) });
    await r.act(async () => (await waitFor(() => btn(r.container, 'Verify b1.db'), r)).click());
    const v = await waitFor(() => tid(r.container, 'verify-result'), r);
    expect(v.textContent).toContain('checksum mismatch');
    expect(v.getAttribute('data-ok')).toBe('false');
    expect(btn(r.container, 'Restore b1.db').disabled).toBe(true);
  });

  it('keeps restore off for a backup from a newer Drydock', async () => {
    const r = show({ backupList: async () => backups(), backupVerify: async (name) => ({ name, size: 1, created: '', ok: true, schemaVersion: 99, restorableHere: false }) });
    await r.act(async () => (await waitFor(() => btn(r.container, 'Verify b1.db'), r)).click());
    const v = await waitFor(() => tid(r.container, 'verify-result'), r);
    expect(v.textContent).toContain('newer Drydock');
    expect(v.getAttribute('data-restorable')).toBe('false');
    expect(btn(r.container, 'Restore b1.db').disabled).toBe(true);
  });

  it('saves the schedule', async () => {
    const sets: object[] = [];
    const r = show({ backupList: async () => backups(), backupSettingsSet: async (x) => (sets.push(x), settings()) });
    type(await waitFor(() => byLabel(r.container, 'Daily backups to keep'), r), '10');
    type(byLabel(r.container, /Backup folder/), 'D:\\bk');
    await r.act(() => byLabel(r.container, /Back up automatically/).click());
    await r.act(() => btn(r.container, 'Save schedule').click());
    await waitFor(() => expect(sets).toEqual([{ enabled: false, dir: 'D:\\bk', daily: 10, weekly: 4 }]), r);
    await waitFor(() => tid(r.container, 'settings-saved'), r);
  });

  it('does not leave a stale "Saved." after a later edit or a refused save', async () => {
    let fail = false;
    const r = show({
      backupList: async () => backups(),
      backupSettingsSet: async () => {
        if (fail) throw new Error('daily must be between 1 and 365');
        return settings();
      },
    });
    await r.act(async () => (await waitFor(() => btn(r.container, 'Save schedule'), r)).click());
    await waitFor(() => tid(r.container, 'settings-saved'), r);
    await r.act(() => type(byLabel(r.container, 'Daily backups to keep'), '0'));
    expect(none(r.container, 'settings-saved')).toBe(true);
    fail = true;
    await r.act(() => btn(r.container, 'Save schedule').click());
    const alert = await waitFor(() => {
      const a = r.container.querySelector('[role="alert"]');
      if (!a) throw new Error('no alert');
      return a;
    }, r);
    expect(alert.textContent).toContain('between 1 and 365');
    expect(none(r.container, 'settings-saved')).toBe(true);
  });

  it('shows the core message when settings are refused', async () => {
    const r = show({
      backupList: async () => backups(),
      backupSettingsSet: async () => {
        throw new Error('the backup folder must be an absolute path');
      },
    });
    type(await waitFor(() => byLabel(r.container, /Backup folder/), r), 'rel');
    await r.act(() => btn(r.container, 'Save schedule').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('must be an absolute path'), r);
  });

  it('starts the form again from the core when the settings change; the default folder shows as empty', async () => {
    let list = backups();
    const r = show({ backupList: async () => list });
    await waitFor(() => expect(byLabel(r.container, 'Daily backups to keep').value).toBe('7'), r);
    expect(byLabel(r.container, /Backup folder/).value).toBe('');
    list = backups({ settings: settings({ daily: 9, dir: 'D:\\elsewhere' }) });
    r.s.emit({ type: 'backup.settings_changed' });
    await waitFor(() => expect(byLabel(r.container, 'Daily backups to keep').value).toBe('9'), r);
    expect(byLabel(r.container, /Backup folder/).value).toBe('D:\\elsewhere');
  });

  it('forgets a verification when the backup file is pruned or replaced', async () => {
    let list = backups();
    const r = show({ backupList: async () => list, backupVerify: async (n) => ok(n) });
    await r.act(async () => (await waitFor(() => btn(r.container, 'Verify b1.db'), r)).click());
    await waitFor(() => expect(btn(r.container, 'Restore b1.db').disabled).toBe(false), r);
    // same name, different file on disk: it must be verified again
    list = backups({ backups: [{ ...backups().backups![0]!, size: 4096, created: '2026-02-02T00:00:00Z' }] });
    r.s.emit({ type: 'backup.created' });
    await waitFor(() => expect(btn(r.container, 'Restore b1.db').disabled).toBe(true), r);
    expect(none(r.container, 'verify-result')).toBe(true);
  });

  it('lists the databases kept from earlier restores', async () => {
    const r = show({ backupList: async () => backups({ replaced: ['replaced-20260101.db'] }) });
    expect((await waitFor(() => tid(r.container, 'replaced-list'), r)).textContent).toContain('replaced-20260101.db');
  });
});
