import { css, on, type Handle } from 'remix/component';
import type { BudgetPausePayload } from '../../api/extTypes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { errorAttrs, Field, formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { parseLimits, type LimitErrors } from '../budgets/budgets.ts';
import type { BudgetStatus } from '../../api/extTypes.ts';
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
const chipsCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2, fontSize: t.textSm, '& > span': { color: t.textMuted, minWidth: '0' } });

type LimitKind = 'usd' | 'tokens' | 'turns';
/** The raises offered beside each limit a paused budget has (C12): a quarter, a half, double. */
export const RAISES = [
  { label: '+25%', factor: 1.25 },
  { label: '+50%', factor: 1.5 },
  { label: 'x2', factor: 2 },
] as const;

/** A limit raised by factor, as the field holds it: dollars to the cent, tokens and turns whole and always above the old limit. */
export function raisedLimit(kind: LimitKind, limit: number, factor: number): string {
  if (kind === 'usd') return String(Math.round(limit * factor * 100) / 100);
  return String(Math.max(Math.floor(limit) + 1, Math.ceil(limit * factor)));
}

const KIND_WORD: Record<LimitKind, string> = { usd: 'USD', tokens: 'token', turns: 'turn' };
const limitOf = (b: BudgetStatus['budget'], k: LimitKind) => (k === 'usd' ? b.limitUsd : k === 'tokens' ? b.limitTokens : b.limitTurns);
const pctOf = (x: BudgetStatus, k: LimitKind) => (k === 'usd' ? x.pctUsd : k === 'tokens' ? x.pctTokens : x.pctTurns);
/** Whether this limit is one that paused the run: used up, or the budget's binding limit when it is over. */
const ranOut = (x: BudgetStatus, k: LimitKind) => (pctOf(x, k) ?? 0) >= 1 || (x.binding === k && x.pct >= 1);

export interface PausedLimit {
  key: string;
  kind: LimitKind;
  limit: number;
  what: string;
  /** How many paused budgets have a limit of this kind: budget.resume sets the new value on every one of them. */
  shared: number;
}

/**
 * The limits that paused the run, one per kind (USD, tokens, turns), each with the field it raises. A kind is listed only when a
 * paused budget ran out of it. budget.resume writes the one new value to every paused budget with a limit of that kind, so when more
 * than one has such a limit no raise is offered (a raise of one would change the others too): shared says how many.
 */
export function pausedLimits(exceeded: BudgetStatus[]): PausedLimit[] {
  const out: PausedLimit[] = [];
  for (const kind of ['usd', 'tokens', 'turns'] as const) {
    const hit = exceeded.filter((x) => limitOf(x.budget, kind) && ranOut(x, kind));
    if (hit.length === 0) continue;
    const shared = exceeded.filter((x) => limitOf(x.budget, kind)).length;
    const b = hit[0]!.budget;
    const limit = limitOf(b, kind)!;
    const name = `${b.scope}${b.scopeId && b.scopeId !== '*' ? '' : ' (each)'}`;
    const amount = kind === 'usd' ? `${dollars(limit)} limit` : kind === 'tokens' ? `${limit.toLocaleString('en-US')}-token limit` : `${limit}-turn limit`;
    out.push({ key: kind, kind, limit, what: `${name}: ${amount}`, shared });
  }
  return out;
}

/** What a shared limit's row says instead of chips: the one new value goes to every paused budget with that kind of limit. */
export const sharedNote = (l: PausedLimit) => `A new ${KIND_WORD[l.kind]} limit is set on all ${l.shared} paused budgets that have one.`;

const dollars = (v: number) => (Number.isInteger(v) ? `$${v}` : `$${v.toFixed(2)}`);

/** What Resume does, on the button: the new limit it sets, or one more turn when none is typed. */
export function resumeLabel(f: { usd: string; tokens: string; turns: string }): string {
  const p = parseLimits(f);
  if (!p.ok) return 'Resume';
  const set = [p.usd !== undefined, p.tokens !== undefined, p.turns !== undefined].filter(Boolean).length;
  if (set === 0) return 'Resume for one more turn';
  if (set > 1) return 'Resume with the new limits';
  if (p.usd !== undefined) return `Resume with a ${dollars(p.usd)} limit`;
  if (p.tokens !== undefined) return `Resume with a ${p.tokens.toLocaleString('en-US')}-token limit`;
  return `Resume with a ${p.turns}-turn limit`;
}

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
  // one limit paused the run: +50% of it is chosen to start with (the person still presses Resume)
  const only = pausedLimits(handle.props.payload.exceeded ?? []);
  if (only.length === 1 && only[0]!.shared === 1) {
    const v = raisedLimit(only[0]!.kind, only[0]!.limit, 1.5);
    if (only[0]!.kind === 'usd') usd = v;
    else if (only[0]!.kind === 'tokens') tokens = v;
    else turns = v;
  }
  const fieldOf = (k: LimitKind) => (k === 'usd' ? usd : k === 'tokens' ? tokens : turns);
  const setField = (k: LimitKind, v: string) => {
    if (k === 'usd') usd = v;
    else if (k === 'tokens') tokens = v;
    else turns = v;
    delete errors[k];
    void handle.update();
  };

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
    const raisable = pausedLimits(exceeded);
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
          {raisable.map((l) =>
            l.shared > 1 ? (
              <p key={l.key} mix={muted} data-testid="budget-raise-shared">
                {sharedNote(l)}
              </p>
            ) : (
              <div key={l.key} mix={chipsCss} role="group" aria-label={`Raise the ${l.what}`} data-testid="budget-raise">
                <span>{l.what}</span>
                {RAISES.map((x) => {
                  const v = raisedLimit(l.kind, l.limit, x.factor);
                  return (
                    <Button key={x.label} size="sm" variant={fieldOf(l.kind) === v ? 'secondary' : 'ghost'} aria-pressed={fieldOf(l.kind) === v ? 'true' : 'false'} mix={on('click', () => setField(l.kind, v))}>
                      {x.label}
                    </Button>
                  );
                })}
              </div>
            ),
          )}
          <div mix={limits}>
            {field('New USD limit', 'usd', () => usd, (v) => (usd = v), 'decimal')}
            {field('New token limit', 'tokens', () => tokens, (v) => (tokens = v), 'numeric')}
            {field('New turn limit', 'turns', () => turns, (v) => (turns = v), 'numeric')}
          </div>
        </fieldset>
        <div mix={formActions}>
          <Button variant="primary" disabled={act.busy !== '' || !runId || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} mix={on('click', (e) => void resume('approved', e.currentTarget))}>
            {resumeLabel({ usd, tokens, turns })}
          </Button>
          <Button disabled={act.busy !== '' || !runId} mix={on('click', (e) => void resume('rejected', e.currentTarget))}>
            Stop the run
          </Button>
        </div>
      </div>
    );
  };
}
