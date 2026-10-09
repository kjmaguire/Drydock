// Backups: back up now, the schedule and folder, the list of backups with verify and a staged restore (behind a verify and a
// confirmation), and the databases kept from earlier restores. Works in every license state and with no project.

import { css, on, type Handle } from 'remix/component';
import { Archive, CalendarClock, DatabaseBackup, FolderClock, History, RotateCcw, ShieldCheck } from 'lucide';
import type { BackupInfo, BackupVerify, RestoreStaged } from '../../api/pkgTypes.ts';
import { formatBytes, formatDateTime } from '../../lib/format.ts';
import { starts } from '../../state/live.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, kv, mono, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions } from '../../ui/form.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { Callout, Done, Group, intro, panel, panelPad, plainList, settingsColumn, tableWrap, Warn } from '../settings/settings-kit.tsx';
import {
  canRestore,
  currentVerifications,
  fingerprint,
  formFrom,
  madeMessage,
  restoreTitle,
  settingsKey,
  settingsPayload,
  type Checked,
  type ScheduleForm,
} from './backups.ts';

const nowRow = css({
  display: 'flex',
  alignItems: 'flex-end',
  gap: t.s3,
  flexWrap: 'wrap',
  '& > :first-child': { flex: '1 1 260px' },
});

const scheduleGrid = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))',
  gap: t.s4,
  '& > .wide': { gridColumn: '1 / -1' },
});

const rowActions = css({ display: 'flex', gap: t.s1, justifyContent: 'flex-end', flexWrap: 'nowrap' });
const nameCell = css({ display: 'flex', alignItems: 'center', gap: t.s2, '& code': { whiteSpace: 'nowrap' } });

const verifyOk = css({
  margin: '0',
  padding: '8px 12px',
  borderRadius: t.rMd,
  background: t.successSoft,
  fontSize: t.textMd,
});
const verifyBad = css({
  margin: '0',
  padding: '8px 12px',
  borderRadius: t.rMd,
  background: t.dangerSoft,
  fontSize: t.textMd,
});

const confirmList = css({ margin: '0', paddingLeft: '18px', display: 'grid', gap: t.s2, fontSize: t.textMd, lineHeight: '1.5' });

function VerifyResult(handle: Handle<{ v: BackupVerify }>) {
  return () => {
    const v = handle.props.v;
    if (!v.ok) {
      return (
        <p class="error-note" mix={verifyBad} role="alert" data-testid="verify-result" data-ok="false">
          <strong>{v.name} is damaged:</strong> {v.error ?? 'verification failed'}
        </p>
      );
    }
    return (
      <p mix={verifyOk} role="status" data-testid="verify-result" data-ok="true" data-restorable={String(!!v.restorableHere)}>
        <strong>{v.name} verified.</strong> Schema version {v.schemaVersion}.{' '}
        {v.restorableHere ? 'It can be restored by this version of Drydock.' : 'It was made by a newer Drydock and cannot be restored here. Update Drydock first.'}
      </p>
    );
  };
}

/** The confirmation step of a restore: what it does, that it is staged, and that nothing is destroyed. */
function RestoreConfirm(handle: Handle<{ b: BackupInfo; onStaged: (r: RestoreStaged) => void; onCancel: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  return () => {
    const { b, onStaged, onCancel } = handle.props;
    return (
      <div data-testid="restore-confirm" style={{ display: 'grid', gap: t.s4 }}>
        <ul mix={confirmList}>
          <li>
            Restoring <strong>replaces everything</strong> in Drydock with the contents of this backup.
          </li>
          <li>
            It is <strong>staged, not applied now</strong>. Nothing changes until you quit and reopen Drydock; running agents stop when you quit.
          </li>
          <li>
            Nothing is destroyed: the database it replaces is kept as <code>replaced-&lt;time&gt;.db</code> in the backup folder.
          </li>
          <li>You can cancel the staged restore any time before you reopen Drydock.</li>
        </ul>
        <ErrorNote error={act.error} what="Not staged" />
        <div mix={formActions}>
          <Button
            variant="danger"
            icon={RotateCcw}
            disabled={act.busy !== ''}
            mix={on('click', async () => {
              const r = await act.run('restore', () => s.client.backupRestore(b.name, true));
              if (r) onStaged(r);
            })}
          >
            Stage this restore for the next start
          </Button>
          <Button mix={on('click', () => onCancel())}>Do not restore</Button>
        </div>
      </div>
    );
  };
}

export function BackupsScreen(handle: Handle) {
  const s = services(handle);
  const list = query(handle, { load: () => s.client.backupList(), when: (e) => starts(e, 'backup.') });
  // the settings on their own: when the folder cannot be read the list fails, and the form must still be there to fix it
  const settingsQ = query(handle, { load: () => s.client.backupSettingsGet(), when: (e) => starts(e, 'backup.') });
  const status = query(handle, { load: () => s.client.backupRestoreStatus(), when: (e) => starts(e, 'backup.') });
  const act = action(handle);
  const saveAct = action(handle);
  let label = '';
  let checked: Record<string, Checked> = {};
  let confirming = '';
  let staged: RestoreStaged | undefined;
  let made = '';
  let saved = false;
  // the schedule form: starts again from the core's answer whenever that answer changes
  let formKey = '';
  let form: ScheduleForm = { enabled: false, dir: '', daily: '', weekly: '' };

  const reloadAll = () => {
    list.reload();
    status.reload();
  };
  const edit = (p: Partial<ScheduleForm>) => {
    form = { ...form, ...p };
    saved = false;
    void handle.update();
  };

  const verify = async (b: BackupInfo) => {
    const r = await act.run(`verify:${b.name}`, () => s.client.backupVerify(b.name));
    if (r) {
      checked = { ...checked, [b.name]: { v: r, fp: fingerprint(b) } };
      void handle.update();
    }
  };

  const backUpNow = async () => {
    made = '';
    const r = await act.run('now', () => s.client.backupNow(label.trim()));
    if (r) {
      label = '';
      made = madeMessage(r);
      reloadAll();
    }
    void handle.update();
  };

  const saveSchedule = async () => {
    saved = false;
    const r = await saveAct.run('save', () => s.client.backupSettingsSet(settingsPayload(form)));
    if (r) {
      saved = true;
      reloadAll();
    }
    void handle.update();
  };

  const cancelStaged = async () => {
    await act.run('cancel', () => s.client.backupRestoreCancel());
    staged = undefined;
    reloadAll();
  };

  return () => {
    const l = list.data;
    const st = l?.settings ?? settingsQ.data;
    const pending = status.data;
    const backups = l?.backups ?? [];
    const verified = currentVerifications(backups, checked);
    const busy = act.busy !== '';
    if (st && settingsKey(st) !== formKey) {
      formKey = settingsKey(st);
      form = formFrom(st);
    }
    const confirmB = confirming ? backups.find((b) => b.name === confirming) : undefined;

    return (
      <Page title="Backups" icon={DatabaseBackup} subtitle={l ? `${backups.length} in the backup folder` : undefined}>
        <div mix={settingsColumn}>
          <p mix={intro}>
            Drydock keeps copies of its database in a backup folder. Backups, verifying and restoring work in every license state. A backup contains your project data
            and run history; keep the folder somewhere only you can read.
          </p>
          <ErrorNote error={list.error ?? status.error ?? act.error} what="Backups" />

          {pending?.pending ? (
            <Callout tone="warn" icon={History} role="status" testId="restore-pending" title="A restore is staged">
              <p>
                The backup <strong>{pending.backup}</strong>
                {pending.requested ? ` (requested ${formatDateTime(pending.requested)})` : ''} will replace the current database{' '}
                <strong>the next time Drydock starts</strong>. Nothing has changed yet. Quit and reopen Drydock to apply it; the current database is kept as{' '}
                <code>replaced-&lt;time&gt;.db</code> in the backup folder.
              </p>
              <div>
                <Button disabled={busy} mix={on('click', () => void cancelStaged())}>
                  Cancel the staged restore
                </Button>
              </div>
            </Callout>
          ) : null}
          {staged ? <Done testId="restore-staged">{staged.message}</Done> : null}
          {staged && staged.warnings && staged.warnings.length > 0 ? (
            <Warn testId="restore-warnings">
              <strong>Before you quit:</strong>
              <ul>
                {staged.warnings.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            </Warn>
          ) : null}

          <Group title="Back up now" icon={Archive} description="Write a backup of the whole database to the backup folder right away.">
            <div mix={panelPad}>
              <div mix={nowRow}>
                <Field label="Label (optional; becomes part of the file name)">
                  {(id) => <input id={id} value={label} placeholder="before-refactor" mix={on('input', (e) => ((label = e.currentTarget.value), void handle.update()))} />}
                </Field>
                <Button variant="primary" icon={Archive} disabled={busy} mix={on('click', () => void backUpNow())}>
                  {act.busy === 'now' ? 'Backing up...' : 'Back up now'}
                </Button>
              </div>
            </div>
            {made ? <Done testId="backup-made">{made}</Done> : null}
          </Group>

          <Group
            title="Schedule and location"
            icon={CalendarClock}
            description="Automatic backups run once a day while Drydock is open. Older backups beyond what you keep are removed."
          >
            {st ? (
              <>
                {l?.warning || st.warning ? (
                  <Warn testId="backup-location-warning">
                    <strong>Check this location.</strong> {l?.warning || st.warning}
                  </Warn>
                ) : null}
                <div mix={panel}>
                  <dl mix={kv}>
                    <dt>Folder</dt>
                    <dd>
                      <code data-testid="backup-dir">{st.dir}</code>
                    </dd>
                    <dt>Last successful backup</dt>
                    <dd data-testid="backup-last-success">{st.lastSuccess ? formatDateTime(st.lastSuccess) : 'none yet'}</dd>
                  </dl>
                  <form aria-label="Backup schedule" mix={on('submit', (e) => (e.preventDefault(), void saveSchedule()))}>
                    <div mix={scheduleGrid}>
                      <div class="wide">
                        <ErrorNote error={saveAct.error} what="Settings not saved" />
                      </div>
                      <div class="wide">
                        <Checkbox
                          label="Back up automatically (once a day while Drydock is open)"
                          checked={form.enabled}
                          onChange={(c) => edit({ enabled: c })}
                        />
                      </div>
                      <div class="wide">
                        <Field label="Backup folder (an absolute path; leave empty for the default)">
                          {(id) => (
                            <input
                              id={id}
                              mix={[mono, on('input', (e) => edit({ dir: e.currentTarget.value }))]}
                              value={form.dir}
                              placeholder={st.defaultDir}
                              spellcheck={false}
                            />
                          )}
                        </Field>
                      </div>
                      <Field label="Daily backups to keep">
                        {(id) => <input id={id} inputMode="numeric" value={form.daily} mix={on('input', (e) => edit({ daily: e.currentTarget.value }))} />}
                      </Field>
                      <Field label="Weekly backups to keep">
                        {(id) => <input id={id} inputMode="numeric" value={form.weekly} mix={on('input', (e) => edit({ weekly: e.currentTarget.value }))} />}
                      </Field>
                      <div mix={formActions}>
                        <Button type="submit" variant="primary" disabled={saveAct.busy !== ''}>
                          Save schedule
                        </Button>
                        {saved ? (
                          <span role="status" data-testid="settings-saved" mix={muted}>
                            Saved.
                          </span>
                        ) : null}
                      </div>
                    </div>
                  </form>
                </div>
                {st.lastError ? (
                  <p class="error-note" mix={verifyBad} role="alert" data-testid="backup-last-error">
                    <strong>Last backup error:</strong> {st.lastError}
                  </p>
                ) : null}
              </>
            ) : list.loading ? (
              <Loading />
            ) : null}
          </Group>

          <Group
            title="Backups"
            icon={ShieldCheck}
            stacked
            description="Every backup in the folder. Verify one before restoring it; a restore is staged and applied the next time Drydock starts."
          >
            {l && backups.length === 0 ? (
              <Empty title="No backups in this folder yet" icon={DatabaseBackup}>
                <p>Use Back up now, or turn on automatic backups.</p>
              </Empty>
            ) : null}
            {backups.length > 0 ? (
              <div mix={tableWrap}>
                <table aria-label="Backups">
                  <thead>
                    <tr>
                      <th scope="col">Name</th>
                      <th scope="col">Made</th>
                      <th scope="col">Size</th>
                      <th scope="col">Label</th>
                      <th scope="col" style={{ textAlign: 'right' }}>
                        Actions
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {backups.map((b) => {
                      const v = verified[b.name];
                      const ok = canRestore(v);
                      return (
                        <tr key={b.name} data-testid="backup-row" data-name={b.name}>
                          <td>
                            <span mix={nameCell}>
                              <code>{b.name}</code> {b.keep ? <Badge tone="info">kept</Badge> : null}
                              {v ? <Badge tone={ok ? 'ok' : 'err'}>{v.ok ? (ok ? 'verified' : 'newer version') : 'damaged'}</Badge> : null}
                            </span>
                          </td>
                          <td style={{ whiteSpace: 'nowrap' }}>{formatDateTime(b.created)}</td>
                          <td style={{ whiteSpace: 'nowrap' }}>{formatBytes(b.size)}</td>
                          <td>{b.label}</td>
                          <td>
                            <div mix={rowActions}>
                              <Button size="sm" disabled={busy} aria-label={`Verify ${b.name}`} mix={on('click', () => void verify(b))}>
                                {act.busy === `verify:${b.name}` ? 'Verifying...' : 'Verify'}
                              </Button>
                              <Button
                                size="sm"
                                disabled={busy || !ok || !!pending?.pending}
                                title={restoreTitle(pending, ok)}
                                aria-label={`Restore ${b.name}`}
                                mix={on('click', () => ((confirming = b.name), void handle.update()))}
                              >
                                Restore...
                              </Button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            ) : null}
            {Object.values(verified).map((v) => (
              <VerifyResult key={v.name} v={v} />
            ))}
            <p mix={faint}>Restore is enabled only after you verify the backup in this page.</p>
          </Group>

          {l && l.replaced && l.replaced.length > 0 ? (
            <Group
              title="Databases kept from earlier restores"
              icon={FolderClock}
              description="Each restore keeps the database it replaced here. Drydock never deletes these; remove them yourself when you are sure."
            >
              <div mix={panelPad}>
                <ul mix={plainList} data-testid="replaced-list">
                  {l.replaced.map((r) => (
                    <li key={r}>
                      <code>{r}</code>
                    </li>
                  ))}
                </ul>
              </div>
            </Group>
          ) : null}
        </div>

        <Dialog
          open={!!confirmB}
          title={confirmB ? `Restore ${confirmB.name}?` : 'Restore'}
          label={confirmB ? `Restore ${confirmB.name}?` : undefined}
          onClose={() => ((confirming = ''), void handle.update())}
        >
          {confirmB ? (
            <RestoreConfirm
              b={confirmB}
              onCancel={() => ((confirming = ''), void handle.update())}
              onStaged={(r) => {
                staged = r;
                confirming = '';
                reloadAll();
                void handle.update();
              }}
            />
          ) : null}
        </Dialog>
      </Page>
    );
  };
}
