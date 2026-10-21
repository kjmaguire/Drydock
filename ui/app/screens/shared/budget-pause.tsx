import { css, on, type Handle } from 'remix/component';
import type { BudgetPausePayload } from '../../api/extTypes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { errorAttrs, Field, formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { parseLimits, type LimitErrors } from '../budgets/budgets.ts';
import { plural } from '../../lib/format.ts';

export const pctText = (p: number | null | undefined) => (p === null || p === undefined ? 'n/a' : `${Math.round(p * 100)}%`);

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
  // a new limit that is not a plain number is told under its field and nothing is sent (M25); an error goes when its field is edited
  let errors: LimitErrors = {};

  const resume = async (decision: 'approved' | 'rejected', button: HTMLElement) => {
    const { runId, requestId, onDone } = handle.props;
    if (!runId) return;
    // stopping the run asks nothing of the limit fields: it must always be possible
    const limits = decision === 'approved' ? parseLimits({ usd, tokens, turns }) : undefined;
    if (limits && !limits.ok) {
      errors = limits.errors;
      act.clear();
      handle.queueTask(() => button.closest('[data-testid="budget-pause"]')?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return;
    }
    errors = {};
    const r = await act.run(decision, () =>
      s.client.budgetResume({
        runId,
        requestId,
        decision,
        limitUsd: limits?.ok ? limits.usd : undefined,
        limitTokens: limits?.ok ? limits.tokens : undefined,
        limitTurns: limits?.ok ? limits.turns : undefined,
      }),
    );
    if (r !== undefined) onDone();
  };

  const field = (label: string, key: keyof LimitErrors, value: () => string, set: (v: string) => void, mode: 'decimal' | 'numeric') => (
    <Field label={label} error={errors[key]}>
      {(id) => (
        <input
          id={id}
          inputMode={mode}
          value={value()}
          {...errorAttrs(id, errors[key])}
          mix={on('input', (e) => (set(e.currentTarget.value), delete errors[key], void handle.update()))}
        />
      )}
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
              {x.spend.unknownCostRows > 0 ? <span mix={muted}> ({plural(x.spend.unknownCostRows, 'call')} with unknown cost not counted as zero)</span> : null}
            </li>
          ))}
        </ul>
        <ErrorNote error={act.error} what="Not sent" />
        <fieldset>
          <legend>Raise the limit (optional; only a limit the budget already has can be raised)</legend>
          <div mix={limits}>
            {field('New USD limit', 'usd', () => usd, (v) => (usd = v), 'decimal')}
            {field('New token limit', 'tokens', () => tokens, (v) => (tokens = v), 'numeric')}
            {field('New turn limit', 'turns', () => turns, (v) => (turns = v), 'numeric')}
          </div>
        </fieldset>
        <div mix={formActions}>
          <Button variant="primary" disabled={act.busy !== '' || !runId || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} mix={on('click', (e) => void resume('approved', e.currentTarget))}>
            Resume
          </Button>
          <Button disabled={act.busy !== '' || !runId} mix={on('click', (e) => void resume('rejected', e.currentTarget))}>
            Stop the run
          </Button>
        </div>
        <p mix={muted}>Resume without a new limit lets exactly one more turn through.</p>
      </div>
    );
  };
}
