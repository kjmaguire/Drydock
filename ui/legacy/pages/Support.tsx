import { useState } from 'react';
import type { BundleCreated, BundlePreview, BundleReport } from '../api/pkgTypes';
import { formatBytes } from '../lib/format';
import { numOrUndef, useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { ErrorNote, Page } from '../components/common';
import { RuleCounts } from './Data';

/** The scrub report as the core gives it: names and counts only. No scanned text is ever shown here. */
export function ScrubReport({ r }: { r: BundleReport }) {
  const files = r.files ?? [];
  return (
    <div data-testid="scrub-report">
      <h3>Scrub report</h3>
      <p>
        <strong data-testid="scrub-total">{r.totalSecretsRedacted}</strong> secret-like value(s) redacted across {files.length} file(s).
      </p>
      <RuleCounts by={r.byRule} />
      {files.some((f) => f.secretsRedacted > 0 || Object.keys(f.identityTokens ?? {}).length > 0) ? (
        <table className="table" aria-label="Per-file scrub results">
          <thead>
            <tr>
              <th scope="col">File</th>
              <th scope="col">Secrets redacted</th>
              <th scope="col">Identity replaced</th>
            </tr>
          </thead>
          <tbody>
            {files
              .filter((f) => f.secretsRedacted > 0 || Object.keys(f.identityTokens ?? {}).length > 0)
              .map((f) => (
                <tr key={f.name}>
                  <td>
                    <code>{f.name}</code>
                  </td>
                  <td>{f.secretsRedacted}</td>
                  <td>{Object.entries(f.identityTokens ?? {}).map(([k, n]) => `${k}: ${n}`).join(', ') || 'none'}</td>
                </tr>
              ))}
          </tbody>
        </table>
      ) : null}
      {(r.refused ?? []).length > 0 ? (
        <>
          <p>Left out of the bundle:</p>
          <ul>
            {(r.refused ?? []).map((x) => (
              <li key={x.name}>
                <code>{x.name}</code>: {x.reason}
              </li>
            ))}
          </ul>
        </>
      ) : null}
      <p className="muted">{r.notice}</p>
    </div>
  );
}

function FileTable({ files }: { files: { name: string; size: number }[] | null }) {
  return (
    <table className="table" aria-label="Files in the support bundle">
      <thead>
        <tr>
          <th scope="col">File</th>
          <th scope="col">Size</th>
        </tr>
      </thead>
      <tbody>
        {(files ?? []).map((f) => (
          <tr key={f.name} data-testid="bundle-file">
            <td>
              <code>{f.name}</code>
            </td>
            <td>{formatBytes(f.size)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export default function SupportPage() {
  const client = useClient();
  const act = useAction();
  const [events, setEvents] = useState('');
  const [run, setRun] = useState('');
  const [ack, setAck] = useState(false);
  const [dest, setDest] = useState('');
  const [preview, setPreview] = useState<(BundlePreview & { key: string }) | undefined>();
  const [created, setCreated] = useState<BundleCreated | undefined>();
  const key = `${events}|${run.trim()}`;
  const fresh = preview?.key === key;
  const opts = { eventLimit: numOrUndef(events), includeRun: run.trim() || undefined };

  return (
    <Page title="Support bundle">
      <p className="muted">
        A support bundle is a zip of diagnostics for a bug report: versions, connection states, settings, recent logs, event types and a scrub report. Secrets are redacted
        and your name, computer name and folders are replaced. Event payloads are dropped (only event types and ids are listed). Scrubbing reduces but does not eliminate leakage, so check the file list and report before you send it.
      </p>
      <p data-testid="bundle-never-uploaded">
        <strong>Drydock never uploads a support bundle.</strong> Creating one writes a file on this computer; you decide whether and where to send it. Support bundles work in every license
        state.
      </p>
      <div className="form">
        <label>
          Recent events to list (empty for the default of 500, at most 5000)
          <input inputMode="numeric" value={events} onChange={(e) => { setEvents(e.target.value); setAck(false); }} />
        </label>
        <label>
          Include one run (optional; a run id). Adds that run&apos;s events with payloads and its raw output.
          <input value={run} onChange={(e) => { setRun(e.target.value); setAck(false); }} spellCheck={false} />
        </label>
        <div className="form-actions">
          <button
            className="primary"
            disabled={act.busy !== ''}
            onClick={async () => {
              setCreated(undefined);
              setAck(false);
              const r = await act.run('preview', () => client.supportbundlePreview(opts));
              if (r) setPreview({ ...r, key });
            }}
          >
            {act.busy === 'preview' ? 'Building the preview...' : 'Preview the bundle'}
          </button>
        </div>
      </div>
      <ErrorNote error={act.error} what="Support bundle" />
      {preview ? (
        <div data-testid="bundle-preview" data-fresh={String(fresh)}>
          <p className="muted">Nothing has been written yet. This is what the bundle would contain.</p>
          {!fresh ? <p className="warn-note">This preview is for different choices. Preview again before creating.</p> : null}
          {preview.warning ? (
            <p className="warn-note" role="note" data-testid="bundle-run-warning">
              {preview.warning}
            </p>
          ) : null}
          <h3>Files</h3>
          <FileTable files={preview.files} />
          <ScrubReport r={preview.report} />
        </div>
      ) : null}
      {preview && fresh ? (
        <div className="form">
          {opts.includeRun ? (
            <label className="inline">
              <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} /> I have read the scrub report above for run {opts.includeRun} and I understand its payloads and raw
              output can contain source code.
            </label>
          ) : null}
          <label>
            Save to (optional full path; empty uses the support folder in the Drydock data folder)
            <input value={dest} onChange={(e) => setDest(e.target.value)} spellCheck={false} />
          </label>
          <div className="form-actions">
            <button
              disabled={act.busy !== '' || (!!opts.includeRun && !ack)}
              onClick={async () => {
                const r = await act.run('create', () => client.supportbundleCreate({ ...opts, dest: dest.trim() || undefined, acknowledgeRun: !!opts.includeRun && ack }));
                if (r) setCreated(r);
              }}
            >
              {act.busy === 'create' ? 'Creating...' : 'Create the bundle'}
            </button>
          </div>
        </div>
      ) : null}
      {created ? (
        <p role="status" data-testid="bundle-created">
          Created <code data-testid="bundle-path">{created.path}</code> ({formatBytes(created.size)}, {(created.files ?? []).length} file(s)). It has not been uploaded anywhere.
        </p>
      ) : null}
    </Page>
  );
}
