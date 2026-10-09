// The two questions a task asks when it cannot go on by itself, answered on the task page:
// - the limit question (pipeline "limit" node): a loop used its budget or the reviewer repeats the same feedback. Continue
//   (optionally with guidance for the team) gives a fresh loop budget; Stop cancels the task.
// - a watchdog pause (internal/watchdog): an agent's turn was stopped (spinning, a cap, an error streak) or the team lead
//   asked for a human. Guidance reaches the agent's next turn; Stop cancels the task.
// When the team lead looked first, its recommendation is shown prominently. Everything in the payload except the fixed
// labels is agent output or evidence and is shown inert.

import { css, on, type Handle } from 'remix/component';
import { Crown, Octagon, Play } from 'lucide';
import type { PendingRequest } from '../../api/types.ts';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';

/** pipeline.leadView: what the team lead said when it looked at the escalation. */
export interface LeadView {
  agent: string;
  name: string;
  action?: string;
  message?: string;
  reason?: string;
  target?: string;
  /** The lead already used its interventions on this task. */
  exhausted?: boolean;
  /** The lead's answer could not be applied as given. */
  invalid?: boolean;
}

/** pipeline.LimitPayload */
export interface LimitPayload {
  phase: 'limit';
  taskId: string;
  title: string;
  trigger: string;
  origin: string;
  evidence?: string;
  iteration: number;
  maxIterations: number;
  lead?: LeadView;
  note: string;
}

/** A watchdog pause (watchdog.Evidence + note): signal, stage, evidence, a handoff summary or the lead's message. */
export interface WatchdogPausePayload {
  signal: string;
  stage: number;
  evidence?: string;
  handoff?: string;
  /** The lead's message (a string), or its full view. */
  lead?: string | LeadView;
  note?: string;
}

export function limitPayload(p: PendingRequest): LimitPayload | undefined {
  const v = p.spec.payload as Partial<LimitPayload> | undefined;
  return v && v.phase === 'limit' ? (v as LimitPayload) : undefined;
}

export function watchdogPayload(p: PendingRequest): WatchdogPausePayload | undefined {
  const v = p.spec.payload as Partial<WatchdogPausePayload> | undefined;
  return v && typeof v.signal === 'string' && v.signal !== '' && typeof v.stage === 'number' ? (v as WatchdogPausePayload) : undefined;
}

export const LIMIT_TRIGGER: Record<string, string> = {
  iteration_limit: 'The task used up its loop budget.',
  repeated_revise: 'The reviewer keeps asking for the same change.',
};

export const SIGNAL_LABEL: Record<string, string> = {
  tool_spin: 'The agent kept repeating the same tool call.',
  error_echo: 'The agent kept running into the same error.',
  tool_call_cap: 'The agent made too many tool calls in one turn.',
  time_cap: 'The turn ran longer than allowed.',
  tool_error_streak: 'Several tool calls in a row failed.',
  stagnation: 'The agent made no progress.',
  lead_escalation: 'The team lead asks for a human decision.',
};

const leadCss = css({
  display: 'grid',
  gap: '4px',
  padding: `${t.s3} ${t.s3}`,
  borderRadius: t.rMd,
  border: `1px solid color-mix(in srgb, ${t.accent} 35%, ${t.border})`,
  background: t.accentSoft,
  '& .head': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textSm, color: t.textMuted },
  '& .head strong': { color: t.text, fontWeight: '600' },
  '& .head svg': { color: t.accentText },
  '& .msg': { margin: '0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textMd, color: t.text },
  '& .why': { margin: '0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm, color: t.textMuted },
});
const factsCss = css({
  display: 'grid',
  gap: t.s2,
  '& .what': { margin: '0', fontWeight: '500' },
  '& .meta': { display: 'flex', flexWrap: 'wrap', gap: t.s2, color: t.textMuted, fontSize: t.textSm },
  '& pre': { margin: '0', maxHeight: '200px', overflow: 'auto', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm },
});
const textareaCss = css({ width: '100%', resize: 'vertical' });

/** The team lead's recommendation, prominent. */
export function LeadRecommendation(handle: Handle<{ lead: LeadView | string }>) {
  return () => {
    const l = handle.props.lead;
    const v: LeadView = typeof l === 'string' ? { agent: '', name: 'The team lead', message: l } : l;
    return (
      <div mix={leadCss} role="note" aria-label="The team lead's recommendation" data-testid="lead-recommendation">
        <div class="head">
          <Icon icon={Crown} size={14} />
          <strong>{v.name || 'The team lead'}</strong>
          {v.action ? <Badge tone="accent">{`recommends: ${v.action}`}</Badge> : <span>says</span>}
          {v.target ? <span>for {v.target}</span> : null}
          {v.exhausted ? <Badge tone="warn" title="The lead already stepped in as often as it may on this task">out of interventions</Badge> : null}
          {v.invalid ? <Badge tone="err" title="The lead's answer could not be applied as given">not applicable</Badge> : null}
        </div>
        {v.message ? <p class="msg">{v.message}</p> : null}
        {v.reason ? <p class="why">Why: {v.reason}</p> : null}
      </div>
    );
  };
}

type Respond = (decision: string, data?: unknown) => Promise<void>;

/** The limit question: Continue (with optional guidance) or Stop the task. */
export function LimitPanel(handle: Handle<{ payload: LimitPayload; busy: boolean; readOnly: boolean; onRespond: Respond }>) {
  let text = '';
  return () => {
    const { payload: p, busy, readOnly, onRespond } = handle.props;
    return (
      <div style={{ display: 'grid', gap: 'var(--space-3)' }} data-testid="limit-question">
        <div mix={factsCss}>
          <p class="what">{LIMIT_TRIGGER[p.trigger] ?? p.trigger}</p>
          <div class="meta">
            <span>
              Iteration {p.iteration} of {p.maxIterations}
            </span>
            {p.origin ? (
              <span>
                at <code>{p.origin}</code>
              </span>
            ) : null}
          </div>
          {p.evidence ? <pre data-testid="limit-evidence">{p.evidence}</pre> : null}
        </div>
        {p.lead ? <LeadRecommendation lead={p.lead} /> : null}
        <Field label="Guidance for the team (optional)" hint="Goes to the agent's next turn with a fresh loop budget.">
          {(id) => <textarea id={id} rows={3} value={text} disabled={busy || readOnly} mix={[textareaCss, on('input', (e) => ((text = e.currentTarget.value), void handle.update()))]} />}
        </Field>
        <div mix={formActions}>
          <Button variant="primary" icon={Play} disabled={busy || readOnly} mix={on('click', () => void onRespond('approved', text.trim() ? { text: text.trim() } : undefined))}>
            {text.trim() ? 'Continue with guidance' : 'Continue'}
          </Button>
          <Button variant="danger" icon={Octagon} disabled={busy} mix={on('click', () => void onRespond('rejected', { action: 'stop' }))}>
            Stop the task
          </Button>
        </div>
      </div>
    );
  };
}

/** A watchdog pause: guidance and continue, or stop. */
export function WatchdogPanel(handle: Handle<{ payload: WatchdogPausePayload; busy: boolean; readOnly: boolean; onRespond: Respond }>) {
  let text = '';
  return () => {
    const { payload: p, busy, readOnly, onRespond } = handle.props;
    return (
      <div style={{ display: 'grid', gap: 'var(--space-3)' }} data-testid="watchdog-pause">
        <div mix={factsCss}>
          <p class="what">{SIGNAL_LABEL[p.signal] ?? p.signal}</p>
          <div class="meta">
            <span>
              Signal <code>{p.signal}</code>
            </span>
            <span>stage {p.stage}</span>
          </div>
          {p.evidence ? <pre data-testid="watchdog-evidence">{p.evidence}</pre> : null}
          {p.handoff ? (
            <details>
              <summary>Where the agent left off</summary>
              <pre>{p.handoff}</pre>
            </details>
          ) : null}
        </div>
        {p.lead ? <LeadRecommendation lead={p.lead} /> : null}
        <Field label="Guidance for the agent" hint="Reaches the agent's next turn; the watchdog's count starts again.">
          {(id) => <textarea id={id} rows={3} value={text} disabled={busy || readOnly} mix={[textareaCss, on('input', (e) => ((text = e.currentTarget.value), void handle.update()))]} />}
        </Field>
        <div mix={formActions}>
          <Button variant="primary" icon={Play} disabled={busy || readOnly} mix={on('click', () => void onRespond('approved', text.trim() ? { text: text.trim() } : undefined))}>
            {text.trim() ? 'Send guidance and continue' : 'Continue'}
          </Button>
          <Button variant="danger" icon={Octagon} disabled={busy} mix={on('click', () => void onRespond('rejected'))}>
            Stop
          </Button>
        </div>
      </div>
    );
  };
}
