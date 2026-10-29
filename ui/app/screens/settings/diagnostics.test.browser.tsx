import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { DiagnosticsChange, DiagnosticsStatus, TelemetryOtlpChange, TelemetryOtlpStatus, TelemetryStatus } from '../../api/extTypes.ts';
import { byLabel, byText, choose, cleanup, errorOf, mount, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { DiagnosticsPanel } from './diagnostics.tsx';
import { dateDaysAgo, sinceFromDate } from './diagnostics.ts';
import { TelemetryPanel } from './telemetry.tsx';

// Decision P121 on the Settings page: the OTLP bodies' compression, "Also send what was recorded before", content export and the
// diagnostics categories, against a stand-in client that records what the page asks the core.

afterEach(() => cleanup());

const tid = (root: ParentNode, id: string) => {
  const el = root.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  if (!el) throw new Error(`no ${id}`);
  return el;
};
const btn = (root: ParentNode, name: string) => byText<HTMLButtonElement>(root, 'button', name);

const otlp: TelemetryOtlpStatus = {
  enabled: false,
  endpoint: 'https://collector.example.com:4318',
  encoding: 'protobuf',
  headers: [],
  active: false,
  queued: 0,
  queueSize: 2048,
  sentSpans: 0,
  sentMetrics: 0,
  droppedOverflow: 0,
  droppedFailed: 0,
  rejected: 0,
  canKeepHeaders: true,
  compression: '',
  gzip: true,
};
const tel: TelemetryStatus = {
  enabled: true,
  spans: 4,
  metrics: 9,
  pending: 13,
  droppedAttributeKeys: null,
  destination: 'this device only (local SQLite); export on demand',
  networkExporter: false,
  otlp,
  content: { enabled: false },
  usageCounters: { enabled: false, waiting: 0 },
};

function telemetry(st0: TelemetryStatus = tel) {
  let st = st0;
  const changes: TelemetryOtlpChange[] = [];
  const content: [boolean, boolean][] = [];
  const r = mount(
    <TelemetryPanel />,
    testServices({
      telemetryStatus: async () => st,
      telemetryOtlpSet: async (ch) => {
        changes.push(ch);
        const o = { ...st.otlp! };
        if (ch.compression !== undefined) {
          o.compression = ch.compression;
          o.gzip = ch.compression === 'gzip' || (ch.compression === '' && o.endpoint.startsWith('https://'));
        }
        if (ch.enabled !== undefined) o.enabled = o.active = ch.enabled;
        if (ch.backfill) o.lastBackfill = { at: 1, sinceMs: ch.backfill.sinceMs, maxRows: ch.backfill.maxRows ?? 500, queued: 6, left: 2, by: 'human:kyle' };
        st = { ...st, otlp: o };
        return st;
      },
      telemetryContentSet: async (enabled, acknowledged) => {
        content.push([enabled, acknowledged]);
        st = { ...st, content: { enabled, changedBy: 'human:kyle', changedAt: 1 } };
        return st;
      },
    }),
  );
  return { r, changes, content };
}

describe('OTLP export: compression and what was recorded before (decision P121)', () => {
  it('chooses the compression from the kit dropdown and says what it does', async () => {
    const { r, changes } = telemetry();
    const box = await waitFor(() => tid(r.container, 'otlp'), r);
    expect(tid(r.container, 'otlp-compression-words').textContent).toBe('Bodies are compressed with gzip (automatic).');
    const sel = tid(box, 'otlp-compression');
    expect(valueOf(sel)).toBe('');
    await r.act(() => choose(sel, 'none'));
    await waitFor(() => expect(changes).toEqual([{ compression: 'none' }]), r);
    await waitFor(() => expect(tid(r.container, 'otlp-compression-words').textContent).toBe('Bodies are not compressed (you chose none).'), r);
  });

  it('sends only what is recorded from now on unless chosen, and sends the bounded rows recorded before when it is', async () => {
    const { r, changes } = telemetry();
    const box = await waitFor(() => tid(r.container, 'otlp'), r);
    const choice = tid(box, 'otlp-backfill');
    const check = byLabel<HTMLInputElement>(choice, /Also send what was recorded before/);
    expect(check.checked).toBe(false);
    expect(choice.querySelector('input[type="date"]')).toBe(null);
    await r.act(() => check.click());
    const since = byLabel<HTMLInputElement>(choice, 'Recorded since');
    const rows = byLabel<HTMLInputElement>(choice, /At most \(rows, up to 1000\)/);
    expect(since.value).toBe(dateDaysAgo(Date.now(), 7));
    expect(rows.value).toBe('500');
    // a limit past the bound is refused here, before anything is sent
    await r.act(() => type(rows, '5000'));
    await r.act(() => byLabel<HTMLButtonElement>(box, 'Send to a collector').click());
    await waitFor(() => expect(errorOf(rows)).toBe('Send between 1 and 1000 rows.'), r);
    expect(errorOf(since)).toBe(null);
    expect(changes).toEqual([]);
    await r.act(() => type(rows, '6'));
    await r.act(() => byLabel<HTMLButtonElement>(box, 'Send to a collector').click());
    await waitFor(
      () =>
        expect(changes).toEqual([
          { enabled: true, endpoint: 'https://collector.example.com:4318', encoding: 'protobuf', backfill: { sinceMs: sinceFromDate(dateDaysAgo(Date.now(), 7))!, maxRows: 6 } },
        ]),
      r,
    );
    await waitFor(() => expect(tid(r.container, 'otlp-backfill-result').textContent).toContain('6 rows since'), r);
    expect(tid(r.container, 'otlp-backfill-result').textContent).toContain('2 more rows in that time were left out by the limit of 6.');
    // once sending is on, the choice is gone (it is made when sending is turned on)
    expect(r.container.querySelector('[data-testid="otlp-backfill"]')).toBe(null);
  });

  it('turns sending on with nothing recorded before by default', async () => {
    const { r, changes } = telemetry();
    const box = await waitFor(() => tid(r.container, 'otlp'), r);
    await r.act(() => byLabel<HTMLButtonElement>(box, 'Send to a collector').click());
    await waitFor(() => expect(changes).toEqual([{ enabled: true, endpoint: 'https://collector.example.com:4318', encoding: 'protobuf' }]), r);
  });
});

describe('Content export (decision P121)', () => {
  it('is off, shows what it sends before it turns on, and Cancel sends nothing', async () => {
    const { r, content } = telemetry();
    const box = await waitFor(() => tid(r.container, 'content-export'), r);
    expect(box.getAttribute('data-enabled')).toBe('false');
    expect(box.textContent).toContain('Off unless you turn it on.');
    await r.act(() => byLabel<HTMLButtonElement>(box, 'Content export').click());
    const dlg = await waitFor(() => r.container.querySelector<HTMLDialogElement>('dialog[open]')!, r);
    expect(dlg.textContent).toContain('can hold code');
    expect(dlg.textContent).toContain('Secrets are masked');
    await r.act(() => btn(dlg, 'Cancel').click());
    expect(content).toEqual([]);
    expect(tid(r.container, 'content-export').getAttribute('data-enabled')).toBe('false');

    await r.act(() => byLabel<HTMLButtonElement>(tid(r.container, 'content-export'), 'Content export').click());
    const again = await waitFor(() => r.container.querySelector<HTMLDialogElement>('dialog[open]')!, r);
    await r.act(() => btn(again, 'Turn on content export').click());
    await waitFor(() => expect(content).toEqual([[true, true]]), r);
    await waitFor(() => expect(tid(r.container, 'content-export').getAttribute('data-enabled')).toBe('true'), r);
    expect(tid(r.container, 'content-export').textContent).toContain('turned on by human:kyle');
    // off at once, with no question
    await r.act(() => byLabel<HTMLButtonElement>(tid(r.container, 'content-export'), 'Content export').click());
    await waitFor(() => expect(content).toEqual([[true, true], [false, false]]), r);
  });
});

describe('DiagnosticsPanel (decision P121)', () => {
  const diag: DiagnosticsStatus = {
    crashReports: { mode: 'local', kept: 1 },
    usageCounters: { enabled: false, waiting: 0 },
    content: { enabled: false },
    telemetry: false,
    otlp: false,
  };

  it('keeps crash reports by default with usage counters off, and each switch is its own change', async () => {
    let st = diag;
    const changes: DiagnosticsChange[] = [];
    const r = mount(
      <DiagnosticsPanel />,
      testServices({
        diagnosticsStatus: async () => st,
        diagnosticsSet: async (ch) => {
          changes.push(ch);
          st = {
            ...st,
            crashReports: ch.crashReports ? { ...st.crashReports, mode: ch.crashReports, changedBy: 'human:kyle' } : st.crashReports,
            usageCounters: ch.usageCounters !== undefined ? { enabled: ch.usageCounters, waiting: 0, changedBy: 'human:kyle' } : st.usageCounters,
          };
          return st;
        },
      }),
    );
    const box = tid(r.container, 'diagnostics');
    const crash = await waitFor(() => byLabel<HTMLButtonElement>(box, 'Keep crash reports'), r);
    const usage = byLabel<HTMLButtonElement>(box, 'Count feature use');
    expect(crash.getAttribute('aria-checked')).toBe('true');
    expect(usage.getAttribute('aria-checked')).toBe('false');
    expect(tid(box, 'diag-crash-words').textContent).toContain('a report stays on this computer');
    expect(tid(box, 'diag-crash-words').textContent).toContain('1 report kept now');
    expect(tid(box, 'diag-usage').textContent).toContain('never what you typed');

    await r.act(() => crash.click());
    await waitFor(() => expect(changes).toEqual([{ crashReports: 'off' }]), r);
    await waitFor(() => expect(tid(r.container, 'diag-crash-words').textContent).toContain('Off: a crash leaves no report'), r);
    expect(tid(r.container, 'diag-crash').textContent).toContain('Last changed by human:kyle.');

    await r.act(() => byLabel<HTMLButtonElement>(tid(r.container, 'diagnostics'), 'Count feature use').click());
    await waitFor(() => expect(changes).toEqual([{ crashReports: 'off' }, { usageCounters: true }]), r);
    await waitFor(() => expect(tid(r.container, 'diag-usage').textContent).toContain('Sent nowhere: no collector is sending.'), r);
  });
});
