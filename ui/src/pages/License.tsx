import { useRef, useState, type FormEvent } from 'react';
import type { LicenseView } from '../api/pkgTypes';
import { formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { useLicense } from '../state/license';
import { Badge, ErrorNote, Page } from '../components/common';

const STATE_TEXT: Record<string, string> = {
  none: 'No license',
  trial: 'Trial',
  active: 'Active',
  grace: 'Grace period',
  expired: 'Expired',
};

/** What stays usable when the app is read-only, and what is turned off. Mirrors the core's own refusal text. */
export const READONLY_USABLE = ['Reading everything', 'Export', 'Backups and restore', 'Support bundle', 'Importing a license'];
export const READONLY_OFF = ['Starting tasks and workflows', 'Approving agent actions', 'Creating projects and agents', 'Adding providers and connections', 'Agent replies in rooms', 'Merging and pull requests'];

function Unconfigured({ view }: { view: LicenseView }) {
  return (
    <div className="staged" role="status" data-testid="license-unconfigured">
      <h2>Licensing is not configured in this build</h2>
      <p>
        <strong>This is a development or unlicensed build. Every feature is allowed and nothing is restricted.</strong> There is no license to import here: a release
        build that carries license verification keys shows your license on this page instead.
      </p>
      <p className="muted">{view.message}</p>
    </div>
  );
}

function Details({ v }: { v: LicenseView }) {
  const expires = v.expiresAt ? formatDateTime(v.expiresAt * 1000) : '';
  return (
    <dl className="kv" data-testid="license-details">
      <dt>State</dt>
      <dd>
        <Badge tone={v.state === 'active' || v.state === 'trial' ? 'ok' : v.state === 'grace' ? 'warn' : 'err'}>
          <span data-testid="license-state">{STATE_TEXT[v.state] ?? v.state}</span>
        </Badge>{' '}
        {v.readOnly ? <Badge tone="err">read-only</Badge> : null}
      </dd>
      {v.state !== 'none' ? (
        <>
          <dt>{v.state === 'expired' ? 'Expired' : v.state === 'grace' ? 'Grace days left' : 'Days left'}</dt>
          <dd data-testid="license-days">
            {v.state === 'expired' ? expires || 'expired' : `${v.daysLeft} day(s)${expires && v.state !== 'grace' ? `, until ${expires}` : ''}`}
          </dd>
        </>
      ) : null}
      {v.kind ? (
        <>
          <dt>Kind</dt>
          <dd>{v.kind}</dd>
        </>
      ) : null}
      {v.plan ? (
        <>
          <dt>Plan</dt>
          <dd>{v.plan}</dd>
        </>
      ) : null}
      {v.name ? (
        <>
          <dt>Licensed to</dt>
          <dd>{v.name}</dd>
        </>
      ) : null}
      {v.subject ? (
        <>
          <dt>Subject</dt>
          <dd>{v.subject}</dd>
        </>
      ) : null}
      {v.licenseId ? (
        <>
          <dt>License id</dt>
          <dd>
            <code>{v.licenseId}</code>
          </dd>
        </>
      ) : null}
      <dt>Update channels</dt>
      <dd>{(v.channels ?? []).join(', ') || 'stable'}</dd>
      {(v.features ?? []).length > 0 ? (
        <>
          <dt>Features</dt>
          <dd>{(v.features ?? []).join(', ')}</dd>
        </>
      ) : null}
    </dl>
  );
}

export default function LicensePage() {
  const client = useClient();
  const lic = useLicense();
  const q = useLive(() => client.licenseStatus(), [], (e) => e.type.startsWith('license.'));
  const act = useAction();
  const tokenRef = useRef<HTMLInputElement>(null);
  const [path, setPath] = useState('');
  const [confirmClear, setConfirmClear] = useState(false);
  const [done, setDone] = useState('');
  const v = q.data;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    // The token lives only in the uncontrolled input: read once and cleared before the request leaves. It is never shown back.
    const token = tokenRef.current?.value.trim() ?? '';
    if (tokenRef.current) tokenRef.current.value = '';
    if (!token && !path.trim()) return;
    setDone('');
    const r = await act.run('import', () => client.licenseImport(token ? { token } : { path: path.trim() }));
    if (r) {
      setPath('');
      setDone(r.message);
      q.reload();
      lic.reload();
    }
  };
  const clear = async () => {
    setConfirmClear(false);
    setDone('');
    const r = await act.run('clear', () => client.licenseClear());
    if (r) {
      setDone(r.message);
      q.reload();
      lic.reload();
    }
  };

  return (
    <Page title="License">
      <ErrorNote error={q.error ?? act.error} what="License" />
      {!v && q.loading ? <p className="muted">Loading...</p> : null}
      {v && !v.configured ? <Unconfigured view={v} /> : null}
      {v && v.configured ? (
        <>
          <p data-testid="license-message">{v.message}</p>
          {v.clockSuspect ? (
            <p className="error-note" role="alert">
              The system clock appears to have been set back. The license is judged by the latest time Drydock has seen
              {v.lastSeen ? ` (${formatDateTime(v.lastSeen * 1000)})` : ''}.
            </p>
          ) : null}
          <Details v={v} />
          {v.readOnly ? (
            <div className="staged" data-testid="license-readonly">
              <h2>Drydock is read-only</h2>
              <p>Nothing has been deleted. Importing a valid license turns work back on; workflows that were parked resume from their checkpoints.</p>
              <div className="cols-2">
                <div>
                  <h3>Still available</h3>
                  <ul>
                    {READONLY_USABLE.map((x) => (
                      <li key={x}>{x}</li>
                    ))}
                  </ul>
                </div>
                <div>
                  <h3>Turned off</h3>
                  <ul>
                    {READONLY_OFF.map((x) => (
                      <li key={x}>{x}</li>
                    ))}
                  </ul>
                </div>
              </div>
            </div>
          ) : null}
        </>
      ) : null}

      {v && v.configured ? (
        <>
          <h2>Import a license</h2>
          <p className="muted">
            Paste the license token, or give the path of a license file on this computer. The token is sent once to your local core and is never shown again or kept in
            this page. Importing is always allowed, including when Drydock is read-only.
          </p>
          <form className="form" onSubmit={submit} autoComplete="off">
            <label>
              License token
              <input ref={tokenRef} type="password" autoComplete="off" spellCheck={false} name="license-token" />
            </label>
            <label>
              Or a license file path
              <input value={path} onChange={(e) => setPath(e.target.value)} placeholder="C:\\Users\\you\\Downloads\\drydock.drydock-license" spellCheck={false} />
            </label>
            <div className="form-actions">
              <button type="submit" className="primary" disabled={act.busy !== ''}>
                {act.busy === 'import' ? 'Checking...' : 'Import license'}
              </button>
            </div>
          </form>
          {done ? (
            <p role="status" data-testid="license-done">
              {done}
            </p>
          ) : null}
          {v.state !== 'none' ? (
            <div className="form-actions">
              {confirmClear ? (
                <>
                  <span>Remove the installed license? Drydock becomes read-only until you import one. Nothing is deleted.</span>
                  <button className="danger" disabled={act.busy !== ''} onClick={clear}>
                    Yes, remove it
                  </button>
                  <button onClick={() => setConfirmClear(false)}>Keep it</button>
                </>
              ) : (
                <button onClick={() => setConfirmClear(true)}>Remove the installed license</button>
              )}
            </div>
          ) : null}
        </>
      ) : null}
      <p className="muted">
        Licenses are checked offline on this computer; Drydock never contacts a license server. The check protects against casual sharing and gives honest expiry
        behaviour. It is not tamper-proof.
      </p>
    </Page>
  );
}
