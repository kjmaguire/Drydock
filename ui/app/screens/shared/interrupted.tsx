// An agent turn cut off by Drydock stopping while it ran (the app closed or crashed; gate "interrupted",
// internal/workflow/engine.go interruptedTurnGate). After one automatic re-run, the next cut-off waits for a person: run the step
// again, which is paid for again, or stop the run. The core adds the crash report kept on this computer from the time of the run
// (crashReport) when there is one; its summary is the redacted line that says why.

import { css, on, type Handle } from 'remix/component';
import type { PendingRequest } from '../../api/types.ts';
import { formatDateTime, plural } from '../../lib/format.ts';
import { visible } from '../../lib/visible.ts';
import { routes } from '../../routes.ts';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';

export const INTERRUPTED_GATE = 'interrupted';

export interface InterruptedPayload {
  gate: 'interrupted';
  /** The workflow node whose turn was cut off. */
  node: string;
  step: number;
  /** How many times the turn was cut off. */
  interruptions: number;
  crashReport?: { name: string; time: number; summary: string };
}

/** The payload of an interrupted-turn request, every field checked; undefined for any other request. */
export function interruptedPayload(p: PendingRequest): InterruptedPayload | undefined {
  const v = p.spec.payload as Record<string, unknown> | null | undefined;
  if (!v || v.gate !== INTERRUPTED_GATE) return undefined;
  const num = (x: unknown) => (typeof x === 'number' && Number.isFinite(x) ? x : 0);
  const c = v.crashReport as Record<string, unknown> | null | undefined;
  const crash = c && typeof c.name === 'string' && c.name ? { name: c.name, time: num(c.time), summary: typeof c.summary === 'string' ? c.summary : '' } : undefined;
  return { gate: INTERRUPTED_GATE, node: typeof v.node === 'string' ? v.node : '', step: num(v.step), interruptions: num(v.interruptions), crashReport: crash };
}

const panelCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& p': { margin: '0', overflowWrap: 'anywhere' },
  '& .crash': { display: 'grid', gap: '4px', padding: `${t.s2} ${t.s3}`, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp, fontSize: t.textSm },
  '& .crash code': { fontFamily: t.mono, overflowWrap: 'anywhere' },
});

/** What was cut off, the crash report of that moment, and the two answers. Read-only (license) turns off running again only. */
export function InterruptedPanel(handle: Handle<{ payload: InterruptedPayload; busy: boolean; readOnly: boolean; onRespond: (decision: string, data?: unknown) => Promise<void> }>) {
  return () => {
    const { payload: p, busy, readOnly, onRespond } = handle.props;
    const step = p.node ? `The ${p.node} step` : 'A step';
    return (
      <div mix={panelCss} data-testid="interrupted-gate">
        <p>
          {step} was cut off {p.interruptions > 0 ? plural(p.interruptions, 'time') : 'again'} because Drydock stopped while it ran. It was run again once by
          itself; now it waits for you.
        </p>
        {p.crashReport ? (
          <div class="crash" data-testid="interrupted-crash">
            <span>Drydock crashed at {formatDateTime(p.crashReport.time)}:</span>
            {p.crashReport.summary ? <code>{visible(p.crashReport.summary)}</code> : null}
            <a href={routes.support.href()}>See the crash report on the Support page</a>
          </div>
        ) : (
          <p mix={muted}>No crash report was kept for this run: Drydock may have been closed rather than crashed.</p>
        )}
        <div mix={formActions}>
          <Button variant="primary" disabled={busy || readOnly} mix={on('click', () => void onRespond('approved'))}>
            Run the step again (paid for again)
          </Button>
          <Button disabled={busy} mix={on('click', () => void onRespond('rejected'))}>
            Stop the run
          </Button>
        </div>
      </div>
    );
  };
}
