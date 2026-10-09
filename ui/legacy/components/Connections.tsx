import { useState } from 'react';
import type { Connection, ConnectionReport, GateStatus } from '../api/types';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from './common';

const provTone = (p: string) => (p === 'observed' ? 'ok' : p === 'declared' ? 'warn' : 'muted');

/** Plain-language result of Test connection: a verdict, the steps, what is degraded, and the capability matrix with provenance. */
export function ReportView({ r }: { r: ConnectionReport }) {
  const steps = r.steps ?? [];
  const matrix = Object.entries(r.matrix ?? {}).sort(([a], [b]) => a.localeCompare(b));
  return (
    <div className="report" data-testid="connection-report" data-ok={r.ok}>
      <p>
        <Badge tone={r.ok ? 'ok' : 'err'}>{r.ok ? 'passed' : 'did not pass'}</Badge> {r.summary}
      </p>
      <p className="muted">
        {r.adapter}
        {r.cliVersion ? ` ${r.cliVersion}` : ''} via {r.transport || 'unknown transport'} on {r.platform}; {r.form} form, took {(r.elapsed / 1e9).toFixed(1)} s. Tokens used by the test:{' '}
        {r.tokensUsed > 0 ? r.tokensUsed : 'not reported'}.
      </p>
      <h4>Steps</h4>
      <ol className="steps">
        {steps.map((s) => (
          <li key={s.id} data-status={s.status}>
            <span aria-hidden="true">{s.status === 'pass' ? '✔' : s.status === 'fail' ? '✖' : '–'}</span> <strong>{s.id}</strong> {s.name}:{' '}
            <span className={s.status === 'pass' ? 'ok' : s.status === 'fail' ? 'err-text' : 'muted'}>{s.status}</span>
            {s.reason ? <span className="muted"> ({s.reason})</span> : null}
            {s.detail ? <div className="muted">{s.detail}</div> : null}
          </li>
        ))}
      </ol>
      {(r.degraded ?? []).length > 0 ? (
        <>
          <h4>What is degraded</h4>
          <ul>
            {(r.degraded ?? []).map((d, i) => (
              <li key={i}>{d}</li>
            ))}
          </ul>
        </>
      ) : null}
      {matrix.length > 0 ? (
        <details>
          <summary>Capability matrix ({matrix.length}) with where each value comes from</summary>
          <table className="table" aria-label="Capability matrix">
            <thead>
              <tr>
                <th scope="col">Capability</th>
                <th scope="col">Value</th>
                <th scope="col">Evidence</th>
              </tr>
            </thead>
            <tbody>
              {matrix.map(([k, c]) => (
                <tr key={k}>
                  <td>{k}</td>
                  <td>{typeof c.value === 'string' ? c.value : JSON.stringify(c.value)}</td>
                  <td>
                    <Badge tone={provTone(c.provenance)} title="observed: seen during this test. declared: the CLI says so. unknown: not verified.">
                      {c.provenance}
                    </Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      ) : null}
    </div>
  );
}

function Gate({ g, onAck }: { g: GateStatus; onAck: (id: string) => Promise<void> }) {
  const [checked, setChecked] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <div className="gate" data-testid="gate" data-gate={g.id} data-acked={g.acked}>
      <p>{g.text}</p>
      {g.acked ? (
        <Badge tone="ok">accepted</Badge>
      ) : (
        <div className="form-actions">
          <label className="inline">
            <input type="checkbox" checked={checked} onChange={(e) => setChecked(e.target.checked)} /> I have read this and accept it
          </label>
          <button
            className="primary"
            disabled={!checked || busy}
            onClick={async () => {
              setBusy(true);
              try {
                await onAck(g.id);
              } finally {
                setBusy(false);
              }
            }}
          >
            Accept
          </button>
        </div>
      )}
    </div>
  );
}

function ConnectionCard({ c, onChanged }: { c: Connection; onChanged: () => void }) {
  const client = useClient();
  const [confirming, setConfirming] = useState(false);
  const [testing, setTesting] = useState(false);
  const [report, setReport] = useState<ConnectionReport | undefined>();
  const [error, setError] = useState<Error | undefined>();
  const ack = async (id: string) => {
    setError(undefined);
    try {
      await client.connectionAcknowledge(id);
      onChanged();
    } catch (e) {
      setError(e as Error);
    }
  };
  const runTest = async () => {
    setTesting(true);
    setError(undefined);
    setConfirming(false);
    try {
      setReport(await client.connectionTest(c.adapter));
      onChanged();
    } catch (e) {
      setError(e as Error);
    } finally {
      setTesting(false);
    }
  };
  return (
    <article className="card" data-testid="connection" data-adapter={c.adapter} data-usable={c.usable}>
      <header className="card-head">
        <h3>{c.adapter}</h3>
        <Badge tone={c.status === 'ready' ? 'ok' : c.status === 'degraded' ? 'warn' : 'muted'}>{c.status}</Badge>
        <Badge tone={c.usable ? 'ok' : 'warn'} title="Usable means the CLI is ready and every notice below is accepted">
          {c.usable ? 'usable' : 'not usable yet'}
        </Badge>
      </header>
      <dl className="kv">
        <dt>Installed</dt>
        <dd>{c.installed ? 'yes' : 'no'}</dd>
        {c.version ? (
          <>
            <dt>Version</dt>
            <dd>
              {c.version}
              {c.compatClass ? <span className="muted"> (evidence: {c.compatClass})</span> : null}
            </dd>
          </>
        ) : null}
        {c.path ? (
          <>
            <dt>Path</dt>
            <dd>
              <code>{c.path}</code>
            </dd>
          </>
        ) : null}
        <dt>Signed in</dt>
        <dd>{c.authenticated === true ? 'yes' : c.authenticated === false ? 'no' : 'not reported'}</dd>
        {c.transport ? (
          <>
            <dt>Transport</dt>
            <dd>{c.transport}</dd>
          </>
        ) : null}
        <dt>Platform</dt>
        <dd>{c.platform}</dd>
        {c.reason ? (
          <>
            <dt>Reason</dt>
            <dd>
              <code>{c.reason}</code>
            </dd>
          </>
        ) : null}
      </dl>
      {c.authDetail ? <p className="muted">{c.authDetail}</p> : null}
      {(c.problems ?? []).map((p, i) => (
        <p key={i} className="error-note">
          {p}
        </p>
      ))}
      {(c.warnings ?? []).map((w, i) => (
        <p key={i} className="warn-note">
          {w}
        </p>
      ))}
      {c.installHint ? (
        <p className="muted">
          Install guidance (Drydock never runs this for you): <code>{c.installHint}</code>
        </p>
      ) : null}
      {(c.prereqs ?? []).length > 0 ? (
        <details>
          <summary>Prerequisites</summary>
          <ul>
            {(c.prereqs ?? []).map((p) => (
              <li key={p.tool} data-ok={p.ok}>
                <span aria-hidden="true">{p.ok ? '✔' : '✖'}</span> {p.tool}
                {p.min ? ` (needs ${p.min} or newer)` : ''}: {p.found ? `found${p.version ? ` ${p.version}` : ''}` : 'not found'}
                {p.guide ? <div className="muted">{p.guide}</div> : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {(c.gates ?? []).map((g) => (
        <Gate key={g.id} g={g} onAck={ack} />
      ))}
      <ErrorNote error={error} what="Failed" />
      <div className="form-actions">
        {!confirming ? (
          <button disabled={testing} onClick={() => setConfirming(true)}>
            {testing ? 'Testing...' : 'Test connection...'}
          </button>
        ) : (
          <>
            <span className="warn-note">
              Test can start the real {c.adapter} CLI and send it a short prompt. That can use your account and cost a few cents.
            </span>
            <button className="primary" onClick={runTest}>
              Run the test
            </button>
            <button onClick={() => setConfirming(false)}>Cancel</button>
          </>
        )}
      </div>
      {report ? <ReportView r={report} /> : null}
    </article>
  );
}

export function ConnectionsView() {
  const client = useClient();
  const q = useLive(() => client.connectionList(), [], (e) => e.type.startsWith('connection.'));
  const list = q.data ?? [];
  return (
    <Page title="Connections">
      <p className="muted">
        Agent CLIs found on this machine. Drydock uses your own installed CLI and its own sign-in; it never reads or transmits your credentials. A CLI is usable
        only when it is ready and every notice below has been accepted by you.
      </p>
      <ErrorNote error={q.error} what="Could not list connections" />
      {q.loading && !q.data ? <p className="muted">Detecting...</p> : null}
      <div className="cards">
        {list.map((c) => (
          <ConnectionCard key={c.adapter} c={c} onChanged={q.reload} />
        ))}
      </div>
    </Page>
  );
}
