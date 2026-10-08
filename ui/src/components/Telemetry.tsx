import { useState } from 'react';
import type { TelemetryDocument, TelemetryExport } from '../api/extTypes';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote } from './common';

/** Telemetry settings (decision D12): off by default, local only, with a preview of exactly what an export would contain. */
export function TelemetryPanel() {
  const client = useClient();
  const q = useLive(() => client.telemetryStatus(), [], (e) => e.type.startsWith('telemetry.'));
  const act = useAction();
  const [preview, setPreview] = useState<TelemetryDocument | undefined>();
  const [exported, setExported] = useState<TelemetryExport | undefined>();
  const st = q.data;
  return (
    <section aria-label="Telemetry" data-testid="telemetry">
      <h2>Telemetry</h2>
      <p className="muted">
        Off by default. When on, Drydock records only allowlisted identifier-shaped attributes (no prompts, code, tool output, file contents or secrets) in a local database.
        Nothing is sent anywhere: there is no network exporter. You can preview exactly what an export contains, and export it to a local file.
      </p>
      <ErrorNote error={q.error ?? act.error} what="Telemetry" />
      {st ? (
        <>
          <p>
            Status: <Badge tone={st.enabled ? 'warn' : 'ok'} title="Only you can change this">{st.enabled ? 'on' : 'off'}</Badge>
            {st.enabled && st.enabledBy ? <span className="muted"> turned on by {st.enabledBy}</span> : null}
          </p>
          <dl className="kv">
            <dt>Destination</dt>
            <dd>{st.destination}</dd>
            <dt>Network exporter</dt>
            <dd>{st.networkExporter ? 'yes' : 'none'}</dd>
            <dt>Stored locally</dt>
            <dd data-testid="telemetry-counts">
              {st.spans} spans, {st.metrics} metrics, {st.pending} not yet exported
            </dd>
          </dl>
          {Object.keys(st.droppedAttributeKeys ?? {}).length > 0 ? (
            <p className="muted">Attribute keys dropped because they are not on the allowlist: {Object.entries(st.droppedAttributeKeys ?? {}).map(([k, n]) => `${k} (${n})`).join(', ')}</p>
          ) : null}
          <div className="form-actions">
            <button disabled={act.busy !== ''} onClick={async () => { await act.run('enable', () => client.telemetryEnable(!st.enabled)); q.reload(); }}>
              {st.enabled ? 'Turn telemetry off' : 'Turn telemetry on'}
            </button>
            <button disabled={act.busy !== ''} onClick={async () => setPreview(await act.run('preview', () => client.telemetryPreview(50)))}>
              Preview what an export contains
            </button>
            <button disabled={act.busy !== ''} onClick={async () => { const r = await act.run('export', () => client.telemetryExport()); if (r) { setExported(r); q.reload(); } }}>
              Export to a local file
            </button>
          </div>
        </>
      ) : null}
      {exported ? (
        <p role="status" data-testid="telemetry-exported">
          Wrote <code>{exported.path}</code> ({exported.spans} spans, {exported.metrics} metrics). It stays on this machine.
        </p>
      ) : null}
      {preview ? (
        <div data-testid="telemetry-preview">
          <p>
            Preview (first 50 rows; schema <code>{preview.schema}</code>): {(preview.spans ?? []).length} spans, {(preview.metrics ?? []).length} metrics.
          </p>
          {preview.note ? <p className="muted">{preview.note}</p> : null}
          {(preview.allowlist ?? []).length > 0 ? <p className="muted">Allowed attributes: {(preview.allowlist ?? []).join(', ')}</p> : null}
          {(preview.spans ?? []).length + (preview.metrics ?? []).length === 0 ? <p className="muted">Nothing has been recorded.</p> : null}
          <pre className="md-code" tabIndex={0} aria-label="Preview document">{JSON.stringify({ resource: preview.resource, spans: preview.spans, metrics: preview.metrics }, null, 2)}</pre>
        </div>
      ) : null}
    </section>
  );
}
