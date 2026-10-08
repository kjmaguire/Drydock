import { useState } from 'react';
import type { BudgetPausePayload } from '../api/extTypes';
import { useAction, numOrUndef } from '../lib/useAction';
import { useClient } from '../state/context';
import { ErrorNote } from './common';
import { useReadOnly } from '../state/license';

export const pctText = (p: number | null | undefined) => (p === null || p === undefined ? 'n/a' : `${Math.round(p * 100)}%`);

/** The resume form for a budget pause: shown in the task view where the pending request is. A human action. */
export function BudgetPausePanel({ payload, runId, requestId, onDone }: { payload: BudgetPausePayload; runId: string | undefined; requestId: string; onDone: () => void }) {
  const client = useClient();
  const act = useAction();
  const ro = useReadOnly();
  const [usd, setUsd] = useState('');
  const [tokens, setTokens] = useState('');
  const [turns, setTurns] = useState('');
  const exceeded = payload.exceeded ?? [];
  const resume = async (decision: 'approved' | 'rejected') => {
    if (!runId) return;
    const r = await act.run(decision, () =>
      client.budgetResume({
        runId,
        requestId,
        decision,
        limitUsd: decision === 'approved' ? numOrUndef(usd) : undefined,
        limitTokens: decision === 'approved' ? numOrUndef(tokens) : undefined,
        limitTurns: decision === 'approved' ? numOrUndef(turns) : undefined,
      }),
    );
    if (r) onDone();
  };
  return (
    <div className="budget-pause" data-testid="budget-pause">
      <p>{payload.message}</p>
      <ul>
        {exceeded.map((s) => (
          <li key={s.budget.id + s.instanceId}>
            {s.message || `${s.budget.scope} ${s.budget.scopeId}: ${pctText(s.pct)} used`}
            {s.spend.unknownCostRows > 0 ? <span className="muted"> ({s.spend.unknownCostRows} call(s) with unknown cost not counted as zero)</span> : null}
          </li>
        ))}
      </ul>
      <ErrorNote error={act.error} what="Not sent" />
      <fieldset>
        <legend>Raise the limit (optional; only a limit the budget already has can be raised)</legend>
        <div className="form-actions">
          <label>
            New USD limit
            <input inputMode="decimal" value={usd} onChange={(e) => setUsd(e.target.value)} />
          </label>
          <label>
            New token limit
            <input inputMode="numeric" value={tokens} onChange={(e) => setTokens(e.target.value)} />
          </label>
          <label>
            New turn limit
            <input inputMode="numeric" value={turns} onChange={(e) => setTurns(e.target.value)} />
          </label>
        </div>
      </fieldset>
      <div className="form-actions">
        <button className="primary" disabled={act.busy !== '' || !runId || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} onClick={() => resume('approved')}>
          Resume
        </button>
        <button disabled={act.busy !== '' || !runId || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} onClick={() => resume('rejected')}>
          Stop the run
        </button>
      </div>
      <p className="muted">Resume without a new limit lets exactly one more turn through.</p>
    </div>
  );
}
