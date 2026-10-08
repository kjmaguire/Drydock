import { useState, type FormEvent } from 'react';
import type { BackupInfo, BackupSettings, BackupVerify, RestoreStaged } from '../api/pkgTypes';
import { formatBytes, formatDateTime } from '../lib/format';
import { numOrUndef, useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';

function SettingsForm({ s, onSaved }: { s: BackupSettings; onSaved: () => void }) {
  const client = useClient();
  const act = useAction();
  const [enabled, setEnabled] = useState(s.enabled);
  const [dir, setDir] = useState(s.dir === s.defaultDir ? '' : s.dir);
  const [daily, setDaily] = useState(String(s.daily));
  const [weekly, setWeekly] = useState(String(s.weekly));
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const r = await act.run('save', () => client.backupSettingsSet({ enabled, dir: dir.trim(), daily: numOrUndef(daily), weekly: numOrUndef(weekly) }));
    if (r) onSaved();
  };
  return (
    <form className="form form-grid" onSubmit={submit} aria-label="Backup schedule">
      <ErrorNote error={act.error} what="Settings not saved" />
      <label className="inline">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> Back up automatically (once a day while Drydock is open)
      </label>
      <label>
        Backup folder (an absolute path; leave empty for the default)
        <input value={dir} onChange={(e) => setDir(e.target.value)} placeholder={s.defaultDir} spellCheck={false} />
      </label>
      <label>
        Daily backups to keep
        <input inputMode="numeric" value={daily} onChange={(e) => setDaily(e.target.value)} />
      </label>
      <label>
        Weekly backups to keep
        <input inputMode="numeric" value={weekly} onChange={(e) => setWeekly(e.target.value)} />
      </label>
      <div className="form-actions">
        <button type="submit" className="primary" disabled={act.busy !== ''}>
          Save schedule
        </button>
      </div>
    </form>
  );
}

function RestoreConfirm({ b, onStaged, onCancel }: { b: BackupInfo; onStaged: (r: RestoreStaged) => void; onCancel: () => void }) {
  const client = useClient();
  const act = useAction();
  return (
    <div className="staged" data-testid="restore-confirm">
      <h3>Restore {b.name}?</h3>
      <ul>
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
      <div className="form-actions">
        <button
          className="danger"
          disabled={act.busy !== ''}
          onClick={async () => {
            const r = await act.run('restore', () => client.backupRestore(b.name, true));
            if (r) onStaged(r);
          }}
        >
          Stage this restore for the next start
        </button>
        <button onClick={onCancel}>Do not restore</button>
      </div>
    </div>
  );
}

function VerifyResult({ v }: { v: BackupVerify }) {
  if (!v.ok) {
    return (
      <p className="error-note" role="alert" data-testid="verify-result" data-ok="false">
        <strong>{v.name} is damaged:</strong> {v.error ?? 'verification failed'}
      </p>
    );
  }
  return (
    <p role="status" data-testid="verify-result" data-ok="true" data-restorable={String(!!v.restorableHere)}>
      <strong>{v.name} verified.</strong> Schema version {v.schemaVersion}.{' '}
      {v.restorableHere ? 'It can be restored by this version of Drydock.' : 'It was made by a newer Drydock and cannot be restored here. Update Drydock first.'}
    </p>
  );
}

export default function BackupsPage() {
  const client = useClient();
  const list = useLive(() => client.backupList(), [], (e) => e.type.startsWith('backup.'));
  const status = useLive(() => client.backupRestoreStatus(), [], (e) => e.type.startsWith('backup.'));
  const act = useAction();
  const [label, setLabel] = useState('');
  const [verified, setVerified] = useState<Record<string, BackupVerify>>({});
  const [confirming, setConfirming] = useState('');
  const [staged, setStaged] = useState<RestoreStaged | undefined>();
  const [made, setMade] = useState('');
  const [saved, setSaved] = useState(false);
  const l = list.data;
  const s = l?.settings;
  const pending = status.data;

  const reloadAll = () => {
    list.reload();
    status.reload();
  };
  const verify = async (name: string) => {
    const r = await act.run(`verify:${name}`, () => client.backupVerify(name));
    if (r) setVerified((m) => ({ ...m, [name]: r }));
  };
  return (
    <Page title="Backups">
      <p className="muted">
        Drydock keeps copies of its database in a backup folder. Backups, verifying and restoring work in every license state. A backup contains your project data and
        run history; keep the folder somewhere only you can read.
      </p>
      <ErrorNote error={list.error ?? status.error ?? act.error} what="Backups" />

      {pending?.pending ? (
        <div className="staged" role="status" data-testid="restore-pending">
          <h2>A restore is staged</h2>
          <p>
            The backup <strong>{pending.backup}</strong>
            {pending.requested ? ` (requested ${formatDateTime(pending.requested)})` : ''} will replace the current database <strong>the next time Drydock starts</strong>.
            Nothing has changed yet. Quit and reopen Drydock to apply it; the current database is kept as <code>replaced-&lt;time&gt;.db</code> in the backup folder.
          </p>
          <button
            disabled={act.busy !== ''}
            onClick={async () => {
              await act.run('cancel', () => client.backupRestoreCancel());
              setStaged(undefined);
              reloadAll();
            }}
          >
            Cancel the staged restore
          </button>
        </div>
      ) : null}
      {staged ? (
        <p role="status" data-testid="restore-staged">
          {staged.message}
        </p>
      ) : null}
      {staged && staged.warnings && staged.warnings.length > 0 ? (
        <div className="warn-note" role="note" data-testid="restore-warnings">
          <strong>Before you quit:</strong>
          <ul>
            {staged.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </div>
      ) : null}

      <h2>Back up now</h2>
      <div className="form-actions">
        <label>
          Label (optional; becomes part of the file name)
          <input value={label} onChange={(e) => setLabel(e.target.value)} />
        </label>
        <button
          className="primary"
          disabled={act.busy !== ''}
          onClick={async () => {
            setMade('');
            const r = await act.run('now', () => client.backupNow(label.trim()));
            if (r) {
              setLabel('');
              setMade(`Backup ${r.backup.name} written (${formatBytes(r.backup.size)}).`);
              if (r.pruned && r.pruned.length > 0) setMade((m) => `${m} Removed ${r.pruned!.length} old backup(s) beyond your retention.`);
              reloadAll();
            }
          }}
        >
          {act.busy === 'now' ? 'Backing up...' : 'Back up now'}
        </button>
      </div>
      {made ? (
        <p role="status" data-testid="backup-made">
          {made}
        </p>
      ) : null}

      <h2>Schedule and location</h2>
      {s ? (
        <>
          {l?.warning || s.warning ? (
            <div className="warn-note" role="note" data-testid="backup-location-warning">
              <strong>Check this location.</strong> {l?.warning || s.warning}
            </div>
          ) : null}
          <dl className="kv">
            <dt>Folder</dt>
            <dd>
              <code data-testid="backup-dir">{s.dir}</code>
            </dd>
            <dt>Last successful backup</dt>
            <dd data-testid="backup-last-success">{s.lastSuccess ? formatDateTime(s.lastSuccess) : 'none yet'}</dd>
          </dl>
          {s.lastError ? (
            <p className="error-note" role="alert" data-testid="backup-last-error">
              <strong>Last backup error:</strong> {s.lastError}
            </p>
          ) : null}
          <SettingsForm
            s={s}
            onSaved={() => {
              setSaved(true);
              reloadAll();
            }}
            key={`${s.enabled}|${s.dir}|${s.daily}|${s.weekly}`}
          />
          {saved ? (
            <p role="status" data-testid="settings-saved">
              Saved.
            </p>
          ) : null}
        </>
      ) : list.loading ? (
        <p className="muted">Loading...</p>
      ) : null}

      <h2>Backups</h2>
      {l && l.backups && l.backups.length === 0 ? <p className="muted">No backups in this folder yet.</p> : null}
      {l && l.backups && l.backups.length > 0 ? (
        <table className="table" aria-label="Backups">
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Made</th>
              <th scope="col">Size</th>
              <th scope="col">Label</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {l.backups.map((b) => {
              const v = verified[b.name];
              const canRestore = v?.ok === true && v.restorableHere === true;
              return (
                <tr key={b.name} data-testid="backup-row" data-name={b.name}>
                  <td>
                    <code>{b.name}</code> {b.keep ? <Badge tone="info">kept</Badge> : null}
                  </td>
                  <td>{formatDateTime(b.created)}</td>
                  <td>{formatBytes(b.size)}</td>
                  <td>{b.label}</td>
                  <td>
                    <div className="form-actions">
                      <button disabled={act.busy !== ''} onClick={() => verify(b.name)} aria-label={`Verify ${b.name}`}>
                        Verify
                      </button>
                      <button
                        disabled={act.busy !== '' || !canRestore || !!pending?.pending}
                        title={pending?.pending ? 'A restore is already staged; cancel it first' : canRestore ? 'Restore this backup (staged for the next start)' : 'Verify this backup first'}
                        onClick={() => setConfirming(b.name)}
                        aria-label={`Restore ${b.name}`}
                      >
                        Restore...
                      </button>
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}
      {Object.values(verified).map((v) => (
        <VerifyResult key={v.name} v={v} />
      ))}
      {confirming && l?.backups?.find((b) => b.name === confirming) ? (
        <RestoreConfirm
          b={l.backups.find((b) => b.name === confirming)!}
          onCancel={() => setConfirming('')}
          onStaged={(r) => {
            setStaged(r);
            setConfirming('');
            reloadAll();
          }}
        />
      ) : null}
      <p className="muted">Restore is enabled only after you verify the backup in this page.</p>

      {l && l.replaced && l.replaced.length > 0 ? (
        <>
          <h2>Databases kept from earlier restores</h2>
          <p className="muted">Each restore keeps the database it replaced here. Drydock never deletes these; remove them yourself when you are sure.</p>
          <ul data-testid="replaced-list">
            {l.replaced.map((r) => (
              <li key={r}>
                <code>{r}</code>
              </li>
            ))}
          </ul>
        </>
      ) : null}
    </Page>
  );
}
