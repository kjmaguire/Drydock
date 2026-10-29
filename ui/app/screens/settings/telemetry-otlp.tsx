// Sending telemetry to a collector over OTLP/HTTP (decision P113): off unless a person gives the endpoint and turns it on. What is
// sent is what the local sink records (the same allowlisted, content-free spans and metrics the preview shows), so the local rows
// stay the log of everything sent. Header values (an API key) go to the credential store and are never shown again; only their
// names come back. Plain http is accepted only for a collector on this computer. Since decision P121: the bodies' compression
// (automatic: gzip for https), and "Also send what was recorded before", chosen as sending is turned on (off unless chosen,
// bounded by a date and a number of rows).

import { css, on, type Handle } from 'remix/component';
import { KeyRound, Send, Trash2 } from 'lucide';
import type { TelemetryOtlpStatus, TelemetryOtlpTest, TelemetryStatus } from '../../api/extTypes.ts';
import { formatDateTime } from '../../lib/format.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { mono, muted } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Checkbox, errorAttrs, Field, formActions, Switch } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import {
  backfillProblem,
  backfillWords,
  COMPRESSION_OPTIONS,
  compressionWords,
  dateDaysAgo,
  DEFAULT_BACKFILL_ROWS,
  MAX_BACKFILL_ROWS,
  sinceFromDate,
} from './diagnostics.ts';
import { Done, panel, Row } from './settings-kit.tsx';

// the endpoint, then compression and encoding; one column when the page is narrow
const formCss = css({
  display: 'grid',
  gap: t.s3,
  gridTemplateColumns: 'minmax(0, 1fr) minmax(130px, 170px) minmax(130px, 170px)',
  alignItems: 'end',
  '@container (max-width: 560px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
});
const lineCss = css({ display: 'flex', gap: t.s2, alignItems: 'center', flexWrap: 'wrap', minWidth: '0' });
// name, value and the button on one row, so the button stays level with the fields at any width
const headerFormCss = css({ display: 'grid', gap: t.s2, gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr) auto', alignItems: 'center' });
const listCss = css({ margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: t.s2 });
const backfillCss = css({ display: 'grid', gap: t.s2, gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)', alignItems: 'end' });

/** The status line: whether spans and metrics are leaving, how many did, and the last success and error. */
export function otlpStatusWords(st: TelemetryOtlpStatus, telemetryOn: boolean): string {
  if (!st.enabled) return 'Not sending: sending is off.';
  if (!st.endpoint) return 'Not sending: there is no endpoint.';
  if (!telemetryOn) return 'Not sending: telemetry is off, so nothing is recorded or sent.';
  const parts = [`Sending: ${st.sentSpans} spans and ${st.sentMetrics} metrics sent since Drydock started`];
  if (st.queued > 0) parts.push(`${st.queued} waiting`);
  const dropped = st.droppedOverflow + st.droppedFailed + st.rejected;
  if (dropped > 0) parts.push(`${dropped} dropped (${st.droppedOverflow} queue full, ${st.droppedFailed} not accepted, ${st.rejected} rejected by the collector)`);
  return parts.join('; ') + '.';
}

export function OtlpExport(handle: Handle<{ status: TelemetryOtlpStatus; telemetryOn: boolean; onChanged: (st: TelemetryStatus) => void }>) {
  const s = services(handle);
  const act = action(handle);
  let endpoint = handle.props.status.endpoint;
  let encoding = handle.props.status.encoding;
  let seen = handle.props.status.endpoint + '|' + handle.props.status.encoding;
  let headerName = '';
  let headerValue = '';
  let test: TelemetryOtlpTest | undefined;
  // "Also send what was recorded before": off unless chosen, the last week and the default number of rows when it is
  let backfill = false;
  let sinceDate = dateDaysAgo(Date.now(), 7);
  let rows = String(DEFAULT_BACKFILL_ROWS);
  let backfillError: string | undefined;
  let backfillField: 'since' | 'rows' = 'since';

  const turnOn = () => {
    const change: Parameters<typeof s.client.telemetryOtlpSet>[0] = { enabled: true, endpoint: endpoint.trim(), encoding };
    if (backfill) {
      const since = sinceFromDate(sinceDate);
      const n = Number(rows);
      backfillError = backfillProblem(since, n, Date.now());
      backfillField = Number.isInteger(n) && n >= 1 && n <= MAX_BACKFILL_ROWS ? 'since' : 'rows';
      if (backfillError) {
        void handle.update();
        return;
      }
      change.backfill = { sinceMs: since!, maxRows: n };
    }
    backfillError = undefined;
    void set('enable', change).then((r) => {
      if (r) {
        backfill = false;
        void handle.update();
      }
    });
  };

  const set = async (what: string, change: Parameters<typeof s.client.telemetryOtlpSet>[0]) => {
    const r = await act.run(what, () => s.client.telemetryOtlpSet(change));
    if (r) handle.props.onChanged(r);
    return r;
  };

  return () => {
    const { status: st, telemetryOn } = handle.props;
    const stored = st.endpoint + '|' + st.encoding;
    if (stored !== seen) {
      seen = stored;
      endpoint = st.endpoint;
      encoding = st.encoding;
    }
    const busy = act.busy !== '';
    const headers = st.headers ?? [];
    const runTest = async () => {
      test = await act.run('test', () => s.client.telemetryOtlpTest());
      void handle.update();
    };
    return (
      <div mix={panel} data-testid="otlp">
        <Row
          label="Send to a collector (OTLP)"
          description={
            st.changedBy ? <span mix={muted}>last changed by {st.changedBy}</span> : 'Off unless you turn it on. Only what the preview shows is sent.'
          }
        >
          <Switch
            label="Send to a collector"
            checked={st.enabled}
            disabled={busy}
            onChange={(v) => (v ? turnOn() : void set('enable', { enabled: false }))}
          />
        </Row>
        {!st.enabled ? (
          <div data-testid="otlp-backfill">
            <Checkbox
              label="Also send what was recorded before"
              description="Off: only what is recorded from now on is sent. On: the rows recorded since the date that no collector has had, the newest first, up to the limit."
              checked={backfill}
              disabled={busy}
              onChange={(v) => ((backfill = v), (backfillError = undefined), void handle.update())}
            />
            {backfill ? (
              <div mix={backfillCss} style={{ marginTop: t.s2 }}>
                <Field label="Recorded since" error={backfillField === 'since' ? backfillError : undefined}>
                  {(id) => (
                    <input
                      id={id}
                      type="date"
                      value={sinceDate}
                      {...errorAttrs(id, backfillField === 'since' ? backfillError : undefined)}
                      mix={on('input', (e) => ((sinceDate = e.currentTarget.value), (backfillError = undefined), void handle.update()))}
                    />
                  )}
                </Field>
                <Field label={`At most (rows, up to ${MAX_BACKFILL_ROWS})`} error={backfillField === 'rows' ? backfillError : undefined}>
                  {(id) => (
                    <input
                      id={id}
                      {...errorAttrs(id, backfillField === 'rows' ? backfillError : undefined)}
                      type="number"
                      min="1"
                      max={String(MAX_BACKFILL_ROWS)}
                      value={rows}
                      mix={on('input', (e) => ((rows = e.currentTarget.value), (backfillError = undefined), void handle.update()))}
                    />
                  )}
                </Field>
              </div>
            ) : null}
          </div>
        ) : null}
        <div>
          <form
            mix={[formCss, on<HTMLFormElement, 'submit'>('submit', (e) => (e.preventDefault(), void set('endpoint', { endpoint: endpoint.trim(), encoding })))]}
            aria-label="Collector"
          >
            <Field label="Endpoint (OTLP/HTTP)" hint="Such as https://collector.example.com:4318. /v1/traces and /v1/metrics are added. Plain http only to this computer.">
              {(id) => (
                <input
                  id={id}
                  type="url"
                  spellcheck={false}
                  autocomplete="off"
                  placeholder="https://collector.example.com:4318"
                  value={endpoint}
                  mix={on('input', (e) => ((endpoint = e.currentTarget.value), void handle.update()))}
                />
              )}
            </Field>
            <Field label="Compression">
              {(id) => (
                <Select
                  id={id}
                  value={st.compression ?? ''}
                  disabled={busy}
                  testId="otlp-compression"
                  options={COMPRESSION_OPTIONS}
                  onChange={(v) => void set('compression', { compression: v === 'gzip' || v === 'none' ? v : '' })}
                />
              )}
            </Field>
            <Field label="Encoding">
              {(id) => (
                <select
                  id={id}
                  value={encoding}
                  mix={on('change', (e) => ((encoding = e.currentTarget.value === 'json' ? 'json' : 'protobuf'), void handle.update()))}
                >
                  <option value="protobuf">Protobuf</option>
                  <option value="json">JSON</option>
                </select>
              )}
            </Field>
            <div mix={formActions}>
              <Button type="submit" disabled={busy}>
                Save endpoint
              </Button>
              <Button icon={Send} disabled={busy || !st.endpoint} mix={[on('click', () => void runTest())]}>
                Send a test span
              </Button>
            </div>
          </form>
        </div>
        <div>
          <p style={{ margin: `0 0 ${t.s2}` }}>Headers sent with each request (an API key). Values are kept in the credential store and never shown.</p>
          {!st.canKeepHeaders ? <Notice tone="warn">This computer has no credential store, so no header can be kept.</Notice> : null}
          {headers.length > 0 ? (
            <ul mix={listCss} data-testid="otlp-headers">
              {headers.map((h) => (
                <li key={h} mix={lineCss}>
                  <code mix={mono}>{h}</code>
                  <span mix={muted}>value saved</span>
                  <Button size="sm" variant="ghost" icon={Trash2} disabled={busy} aria-label={`Remove the ${h} header`} mix={[on('click', () => void set('remove', { removeHeaders: [h] }))]}>
                    Remove
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
          {st.canKeepHeaders ? (
            <form
              mix={[
                headerFormCss,
                on<HTMLFormElement, 'submit'>('submit', async (e) => {
                  e.preventDefault();
                  const r = await set('header', { setHeaders: { [headerName.trim()]: headerValue } });
                  if (r) {
                    headerName = '';
                    headerValue = '';
                    void handle.update();
                  }
                }),
              ]}
              aria-label="Add a header"
              style={{ marginTop: t.s2 }}
            >
              <input
                aria-label="Header name"
                placeholder="Header name, such as x-api-key"
                spellcheck={false}
                autocomplete="off"
                value={headerName}
                mix={on('input', (e) => ((headerName = e.currentTarget.value), void handle.update()))}
              />
              <input
                type="password"
                aria-label="Header value"
                placeholder="Value"
                autocomplete="new-password"
                spellcheck={false}
                value={headerValue}
                mix={on('input', (e) => ((headerValue = e.currentTarget.value), void handle.update()))}
              />
              <Button type="submit" size="sm" icon={KeyRound} disabled={busy || !headerName.trim() || !headerValue}>
                Save header
              </Button>
            </form>
          ) : null}
        </div>
        <div data-testid="otlp-status" data-active={String(st.active)}>
          <p style={{ margin: '0' }}>{otlpStatusWords(st, telemetryOn)}</p>
          <p mix={muted} style={{ margin: '0' }} data-testid="otlp-compression-words">
            {compressionWords(st)}
          </p>
          {st.lastBackfill ? (
            <p mix={muted} style={{ margin: '0' }} data-testid="otlp-backfill-result">
              {backfillWords(st.lastBackfill, formatDateTime)}
            </p>
          ) : null}
          <p mix={muted} style={{ margin: '0' }}>
            Last success: {st.lastSuccessAt ? formatDateTime(st.lastSuccessAt) : 'none yet'}
            {st.lastError ? (
              <>
                {' '}
                · Last error{st.lastErrorAt ? ` (${formatDateTime(st.lastErrorAt)})` : ''}: <span data-testid="otlp-last-error">{st.lastError}</span>
              </>
            ) : null}
          </p>
        </div>
        <ErrorNote error={act.error} what="Sending" />
        {test ? (
          test.ok ? (
            <Done testId="otlp-test">
              <span>The collector accepted the test span. It is kept in the local telemetry too.</span>
            </Done>
          ) : (
            <Notice tone="warn" testId="otlp-test">
              The test span was not accepted: {test.error}
            </Notice>
          )
        ) : null}
      </div>
    );
  };
}
