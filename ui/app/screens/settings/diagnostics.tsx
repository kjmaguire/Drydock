// The diagnostics categories (docs/packaging.md section 9, decision P121), each a switch of its own, stored with who set it:
// crash reports (kept on this computer by default, as before; off keeps none and support bundles carry none) and usage counters
// (off by default; how often each part of Drydock is used, by name only, kept with the local telemetry). OpenTelemetry export is
// in the Telemetry group.

import { type Handle } from 'remix/component';
import { Stethoscope } from 'lucide';
import { starts } from '../../state/live.ts';
import { action, query, services } from '../../state/services.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Switch } from '../../ui/form.tsx';
import { crashWords } from './diagnostics.ts';
import { Group, panel, Row } from './settings-kit.tsx';

export function DiagnosticsPanel(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.diagnosticsStatus(), when: (e) => starts(e, 'diagnostics.', 'app.crashed', 'telemetry.') });
  const act = action(handle);

  const set = async (what: string, change: Parameters<typeof s.client.diagnosticsSet>[0]) => {
    const r = await act.run(what, () => s.client.diagnosticsSet(change));
    if (r) q.set(r);
  };

  return () => {
    const st = q.data;
    const busy = act.busy !== '';
    return (
      <Group
        title="Diagnostics"
        icon={Stethoscope}
        label="Diagnostics"
        testId="diagnostics"
        description="What Drydock keeps about how it runs, one switch each. Nothing here leaves this computer by itself."
      >
        <ErrorNote error={q.error ?? act.error} what="Diagnostics" />
        {!st && q.loading ? <Loading /> : null}
        {st ? (
          <div mix={panel}>
            <Row
              label="Crash reports"
              testId="diag-crash"
              description={
                <>
                  <span data-testid="diag-crash-words">{crashWords(st.crashReports.mode, st.crashReports.kept)}</span>
                  {st.crashReports.changedBy ? <span mix={muted}> Last changed by {st.crashReports.changedBy}.</span> : null}
                </>
              }
            >
              <Switch
                label="Keep crash reports"
                checked={st.crashReports.mode === 'local'}
                disabled={busy}
                onChange={(v) => void set('crash', { crashReports: v ? 'local' : 'off' })}
              />
            </Row>
            {st.usageCounters ? (
              <Row
                label="Usage counters"
                testId="diag-usage"
                description={
                  <>
                    <span>
                      {st.usageCounters.enabled
                        ? 'On: how often each part of Drydock is used is counted, by name only, and kept with the telemetry on this computer.'
                        : 'Off unless you turn it on. Counts how often each part of Drydock is used, by name only: never what you typed.'}
                    </span>
                    {st.usageCounters.enabled ? (
                      <span mix={muted}>
                        {' '}
                        {st.otlp ? 'Sent with the telemetry to your collector.' : 'Sent nowhere: no collector is sending.'}
                        {st.usageCounters.changedBy ? ` Turned on by ${st.usageCounters.changedBy}.` : ''}
                      </span>
                    ) : null}
                  </>
                }
              >
                <Switch
                  label="Count feature use"
                  checked={st.usageCounters.enabled}
                  disabled={busy}
                  onChange={(v) => void set('usage', { usageCounters: v })}
                />
              </Row>
            ) : null}
          </div>
        ) : null}
      </Group>
    );
  };
}
