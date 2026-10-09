// Telemetry settings (decision D12): off by default, local only, with a preview of exactly what an export would contain.

import { css, on, type Handle } from 'remix/component';
import { Activity, Eye, FileDown } from 'lucide';
import type { TelemetryDocument, TelemetryExport } from '../../api/extTypes.ts';
import { starts } from '../../state/live.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, faint, kv, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { Done, Group, panel, Row } from './settings-kit.tsx';

const actionsRow = css({ display: 'flex', gap: t.s2, flexWrap: 'wrap' });
const previewCss = css({
  display: 'grid',
  gap: t.s2,
  '& > p': { margin: '0' },
  '& pre': { maxHeight: '320px', overflow: 'auto', margin: '0' },
});

export function TelemetryPanel(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.telemetryStatus(), when: (e) => starts(e, 'telemetry.') });
  const act = action(handle);
  let preview: TelemetryDocument | undefined;
  let exported: TelemetryExport | undefined;

  const toggle = async (enabled: boolean) => {
    await act.run('enable', () => s.client.telemetryEnable(enabled));
    q.reload();
  };
  const runPreview = async () => {
    preview = await act.run('preview', () => s.client.telemetryPreview(50));
    void handle.update();
  };
  const runExport = async () => {
    const r = await act.run('export', () => s.client.telemetryExport());
    if (r) {
      exported = r;
      q.reload();
    }
    void handle.update();
  };

  return () => {
    const st = q.data;
    const busy = act.busy !== '';
    const dropped = Object.entries(st?.droppedAttributeKeys ?? {});
    return (
      <Group
        title="Telemetry"
        icon={Activity}
        label="Telemetry"
        testId="telemetry"
        description="Off by default. When on, Drydock records only allowlisted identifier-shaped attributes (no prompts, code, tool output, file contents or secrets) in a local database. Nothing is sent anywhere: there is no network exporter. You can preview exactly what an export contains, and export it to a local file."
      >
        <ErrorNote error={q.error ?? act.error} what="Telemetry" />
        {!st && q.loading ? <Loading /> : null}
        {st ? (
          <div mix={panel}>
            <Row
              label={
                <>
                  Status:{' '}
                  <Badge tone={st.enabled ? 'warn' : 'ok'} title="Only you can change this">
                    {st.enabled ? 'on' : 'off'}
                  </Badge>
                </>
              }
              description={st.enabled && st.enabledBy ? <span mix={muted}>turned on by {st.enabledBy}</span> : 'Only you can turn it on, here.'}
            >
              <Button variant={st.enabled ? 'secondary' : 'primary'} disabled={busy} mix={on('click', () => void toggle(!st.enabled))}>
                {st.enabled ? 'Turn telemetry off' : 'Turn telemetry on'}
              </Button>
            </Row>
            <dl mix={kv}>
              <dt>Destination</dt>
              <dd>{st.destination}</dd>
              <dt>Network exporter</dt>
              <dd>{st.networkExporter ? 'yes' : 'none'}</dd>
              <dt>Stored locally</dt>
              <dd data-testid="telemetry-counts">
                {st.spans} spans, {st.metrics} metrics, {st.pending} not yet exported
              </dd>
            </dl>
            {dropped.length > 0 ? (
              <p mix={faint} style={{ margin: '0' }}>
                Attribute keys dropped because they are not on the allowlist: {dropped.map(([k, n]) => `${k} (${n})`).join(', ')}
              </p>
            ) : null}
            <div mix={actionsRow}>
              <Button icon={Eye} disabled={busy} mix={on('click', () => void runPreview())}>
                Preview what an export contains
              </Button>
              <Button icon={FileDown} disabled={busy} mix={on('click', () => void runExport())}>
                Export to a local file
              </Button>
            </div>
          </div>
        ) : null}
        {exported ? (
          <Done testId="telemetry-exported">
            <span>
              Wrote <code>{exported.path}</code> ({exported.spans} spans, {exported.metrics} metrics). It stays on this machine.
            </span>
          </Done>
        ) : null}
        {preview ? (
          <div mix={previewCss} data-testid="telemetry-preview">
            <p>
              Preview (first 50 rows; schema <code>{preview.schema}</code>): {(preview.spans ?? []).length} spans, {(preview.metrics ?? []).length} metrics.
            </p>
            {preview.note ? <p mix={muted}>{preview.note}</p> : null}
            {(preview.allowlist ?? []).length > 0 ? <p mix={muted}>Allowed attributes: {(preview.allowlist ?? []).join(', ')}</p> : null}
            {(preview.spans ?? []).length + (preview.metrics ?? []).length === 0 ? <p mix={muted}>Nothing has been recorded.</p> : null}
            <pre tabIndex={0} aria-label="Preview document">
              {JSON.stringify({ resource: preview.resource, spans: preview.spans, metrics: preview.metrics }, null, 2)}
            </pre>
          </div>
        ) : null}
      </Group>
    );
  };
}
