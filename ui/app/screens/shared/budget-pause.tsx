import { css, on, type Handle } from 'remix/component';
import type { BudgetPausePayload } from '../../api/extTypes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';

export const pctText = (p: number | null | undefined) => (p === null || p === undefined ? 'n/a' : `${Math.round(p * 100)}%`);

/** Parse a number field: '' means unset. NaN is returned as undefined so the core never sees it. */
export function numOrUndef(s: string): number | undefined {
  const v = s.trim();
  if (v === '') return undefined;
  const n = Number(v);
  return Number.isFinite(n) ? n : undefined;
}

const panelCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid color-mix(in srgb, ${t.warning} 35%, transparent)`,
  borderRadius: t.rLg,
  background: t.warningSoft,
  '& ul': { margin: '0', paddingLeft: '20px' },
});
const limits = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: t.s3 });

/** The resume form for a budget pause: shown in the task view where the pending request is. A human action. */
export function BudgetPausePanel(handle: Handle<{ payload: BudgetPausePayload; runId: string | undefined; requestId: string; onDone: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let usd = '';
  let tokens = '';
  let turns = '';

  const resume = async (decision: 'approved' | 'rejected') => {
    const { runId, requestId, onDone } = handle.props;
    if (!runId) return;
    const r = await act.run(decision, () =>
      s.client.budgetResume({
        runId,
        requestId,
        decision,
        limitUsd: decision === 'approved' ? numOrUndef(usd) : undefined,
        limitTokens: decision === 'approved' ? numOrUndef(tokens) : undefined,
        limitTurns: decision === 'approved' ? numOrUndef(turns) : undefined,
      }),
    );
    if (r !== undefined) onDone();
  };

  const field = (label: string, value: () => string, set: (v: string) => void, mode: 'decimal' | 'numeric') => (
    <Field label={label}>
      {(id) => <input id={id} inputMode={mode} value={value()} mix={on('input', (e) => (set(e.currentTarget.value), void handle.update()))} />}
    </Field>
  );

  return () => {
    const { payload, runId } = handle.props;
    const ro = s.workspace.license;
    const exceeded = payload.exceeded ?? [];
    return (
      <div mix={panelCss} data-testid="budget-pause">
        <p>
          <strong>{payload.message}</strong>
        </p>
        <ul>
          {exceeded.map((x) => (
            <li key={x.budget.id + x.instanceId}>
              {x.message || `${x.budget.scope} ${x.budget.scopeId}: ${pctText(x.pct)} used`}
              {x.spend.unknownCostRows > 0 ? <span mix={muted}> ({x.spend.unknownCostRows} call(s) with unknown cost not counted as zero)</span> : null}
            </li>
          ))}
        </ul>
        <ErrorNote error={act.error} what="Not sent" />
        <fieldset>
          <legend>Raise the limit (optional; only a limit the budget already has can be raised)</legend>
          <div mix={limits}>
            {field('New USD limit', () => usd, (v) => (usd = v), 'decimal')}
            {field('New token limit', () => tokens, (v) => (tokens = v), 'numeric')}
            {field('New turn limit', () => turns, (v) => (turns = v), 'numeric')}
          </div>
        </fieldset>
        <div mix={formActions}>
          <Button variant="primary" disabled={act.busy !== '' || !runId || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} mix={on('click', () => void resume('approved'))}>
            Resume
          </Button>
          <Button disabled={act.busy !== '' || !runId} mix={on('click', () => void resume('rejected'))}>
            Stop the run
          </Button>
        </div>
        <p mix={muted}>Resume without a new limit lets exactly one more turn through.</p>
      </div>
    );
  };
}
