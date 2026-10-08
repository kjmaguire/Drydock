import { useState, type FormEvent } from 'react';
import type { Budget, BudgetRow, BudgetSet, BudgetStatus } from '../api/extTypes';
import { useAction, numOrUndef } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';
import { pctText } from '../components/BudgetPause';

export function limitsText(b: Budget): string {
  const parts: string[] = [];
  if (b.limitUsd != null) parts.push(`$${b.limitUsd}`);
  if (b.limitTokens != null) parts.push(`${b.limitTokens.toLocaleString()} tokens`);
  if (b.limitTurns != null) parts.push(`${b.limitTurns} turns`);
  return parts.join(', ') || 'no limit';
}

/** State with text and a shape, never color alone: ok, warning at the warn percentage, paused at 100%. */
export function StateChip({ s }: { s: BudgetStatus }) {
  if (s.state === 'exceeded') return <Badge tone="err" title="The next agent turn does not start until a human resumes it">{'⏸'} paused at 100%</Badge>;
  if (s.state === 'warning') return <Badge tone="warn">{'⚠'} warning at {pctText(s.budget.warnPct)}</Badge>;
  return <Badge tone="ok">{'✔'} ok</Badge>;
}

/** Spend with its provenance. Unknown-cost calls are counted, never priced at zero. */
export function SpendLine({ s }: { s: BudgetStatus }) {
  const sp = s.spend;
  return (
    <div className="spend" data-testid="spend">
      <div>
        Known cost: <strong>${(sp.reportedUsd + sp.estimatedUsd).toFixed(4)}</strong> ({s.costLabel}
        {sp.reportedUsd > 0 ? `; reported $${sp.reportedUsd.toFixed(4)}` : ''}
        {sp.estimatedUsd > 0 ? `; estimated $${sp.estimatedUsd.toFixed(4)}` : ''})
      </div>
      {sp.unknownCostRows > 0 ? (
        <div className="warn-note" data-testid="unknown-cost">
          {sp.unknownCostRows} call{sp.unknownCostRows === 1 ? '' : 's'} with unknown cost are counted but not priced at zero
          {s.budget.limitUsd != null ? `, so the dollar percentage (${pctText(s.pctUsd)}) is a lower bound` : ''}.
        </div>
      ) : null}
      <div className="muted">
        {sp.turns} turn{sp.turns === 1 ? '' : 's'}, {(sp.inputTokens + sp.outputTokens).toLocaleString()} tokens ({sp.cachedTokens.toLocaleString()} cached)
      </div>
    </div>
  );
}

function BudgetForm({ projectId, initial, onDone }: { projectId: string; initial?: Budget; onDone: () => void }) {
  const client = useClient();
  const act = useAction();
  const [scope, setScope] = useState<string>(initial?.scope ?? 'project');
  const [scopeId, setScopeId] = useState(initial?.scopeId ?? projectId);
  const [usd, setUsd] = useState(initial?.limitUsd != null ? String(initial.limitUsd) : '');
  const [tokens, setTokens] = useState(initial?.limitTokens != null ? String(initial.limitTokens) : '');
  const [turns, setTurns] = useState(initial?.limitTurns != null ? String(initial.limitTurns) : '');
  const [warn, setWarn] = useState(initial ? String(Math.round(initial.warnPct * 100)) : '80');
  const [note, setNote] = useState(initial?.note ?? '');
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const body: BudgetSet = {
      id: initial?.id,
      version: initial?.version,
      scope,
      scopeId: scopeId.trim(),
      projectId: scopeId.trim() === '*' ? projectId : undefined,
      limitUsd: numOrUndef(usd) ?? null,
      limitTokens: numOrUndef(tokens) ?? null,
      limitTurns: numOrUndef(turns) ?? null,
      warnPct: (numOrUndef(warn) ?? 80) / 100,
      enabled: initial?.enabled ?? true,
      note,
    };
    if (await act.run('save', () => client.budgetSet(body))) onDone();
  };
  return (
    <form className="form form-grid" onSubmit={submit}>
      <ErrorNote error={act.error} what="Budget not saved" />
      <label>
        Scope
        <select aria-label="Scope" value={scope} onChange={(e) => setScope(e.target.value)} disabled={!!initial}>
          <option value="project">project</option>
          <option value="task">task</option>
          <option value="run">run</option>
          <option value="agent">agent</option>
        </select>
      </label>
      <label>
        Scope id (an id, or * for every task, run or agent)
        <input value={scopeId} onChange={(e) => setScopeId(e.target.value)} disabled={!!initial} required spellCheck={false} />
      </label>
      <label>
        Limit in USD
        <input inputMode="decimal" value={usd} onChange={(e) => setUsd(e.target.value)} />
      </label>
      <label>
        Limit in tokens
        <input inputMode="numeric" value={tokens} onChange={(e) => setTokens(e.target.value)} />
      </label>
      <label>
        Limit in turns
        <input inputMode="numeric" value={turns} onChange={(e) => setTurns(e.target.value)} />
      </label>
      <label>
        Warn at (percent)
        <input inputMode="numeric" value={warn} onChange={(e) => setWarn(e.target.value)} />
      </label>
      <label className="span-all">
        Note
        <input value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      <div className="form-actions">
        <button className="primary" type="submit" disabled={act.busy !== ''}>
          {initial ? 'Save changes' : 'Add budget'}
        </button>
        {initial ? <button type="button" onClick={onDone}>Cancel</button> : null}
      </div>
    </form>
  );
}

function BudgetCard({ row, onChanged }: { row: BudgetRow; onChanged: () => void }) {
  const client = useClient();
  const act = useAction();
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const b = row.budget;
  const s = row.status;
  return (
    <article className="card" data-testid="budget" data-scope={b.scope} data-state={s?.state ?? 'template'}>
      <header className="card-head">
        <h3>
          {b.scope} <code>{b.scopeId}</code>
        </h3>
        <Badge tone={b.enabled ? 'ok' : 'muted'}>{b.enabled ? 'enabled' : 'disabled'}</Badge>
        {s ? <StateChip s={s} /> : <Badge title="A ceiling for every instance; each task, run or agent is judged on its own">ceiling for every {b.scope}</Badge>}
      </header>
      <p>
        Limit: <strong>{limitsText(b)}</strong>, warns at {pctText(b.warnPct)}, pauses at 100%.
      </p>
      {b.note ? <p className="muted">{b.note}</p> : null}
      {s ? (
        <>
          <div className="meter" role="img" aria-label={`${pctText(s.pct)} of the limit used`}>
            <div className={`meter-fill meter-${s.state}`} style={{ width: `${Math.min(100, Math.round(s.pct * 100))}%` }} />
          </div>
          <p>
            Used {pctText(s.pct)} of the {s.binding ?? ''} limit.{' '}
            {s.pctUsd != null ? `USD ${pctText(s.pctUsd)}${s.costComplete ? '' : ' (lower bound)'}` : ''}
            {s.pctTokens != null ? ` tokens ${pctText(s.pctTokens)}` : ''}
            {s.pctTurns != null ? ` turns ${pctText(s.pctTurns)}` : ''}
          </p>
          <SpendLine s={s} />
        </>
      ) : null}
      <ErrorNote error={act.error} />
      {editing ? (
        <BudgetForm projectId={b.projectId ?? ''} initial={b} onDone={() => { setEditing(false); onChanged(); }} />
      ) : (
        <div className="form-actions">
          <button onClick={() => setEditing(true)}>Edit limits</button>
          <button disabled={act.busy !== ''} onClick={async () => { await act.run('toggle', () => client.budgetSet({ ...b, enabled: !b.enabled })); onChanged(); }}>
            {b.enabled ? 'Disable' : 'Enable'}
          </button>
          {confirm ? (
            <>
              <button className="danger" onClick={async () => { await act.run('delete', () => client.budgetDelete(b.id)); onChanged(); }}>Yes, delete</button>
              <button onClick={() => setConfirm(false)}>Keep</button>
            </>
          ) : (
            <button onClick={() => setConfirm(true)}>Delete</button>
          )}
        </div>
      )}
    </article>
  );
}

export default function BudgetsView({ projectId }: { projectId: string }) {
  const client = useClient();
  const q = useLive(() => client.budgetList(projectId), [projectId], (e) => e.type.startsWith('budget.') || e.type.startsWith('task.') || e.type === 'room.message_posted');
  const [adding, setAdding] = useState(false);
  const rows = q.data ?? [];
  return (
    <Page title="Budgets" actions={<button className="primary" onClick={() => setAdding(true)}>Add budget</button>}>
      <p className="muted">
        A budget warns at its warning percentage (80% by default). At 100% the next agent turn does not start: the run waits for you in Needs you, and you can raise the
        limit there or approve one more turn. Only you can change a budget. Costs are reported, estimated or unknown and are never merged: calls with an unknown cost are
        counted, not priced at zero.
      </p>
      <ErrorNote error={q.error} what="Could not list budgets" />
      {adding ? <BudgetForm projectId={projectId} onDone={() => { setAdding(false); q.reload(); }} /> : null}
      {q.data && rows.length === 0 && !adding ? <p className="muted">No budgets. Usage is shown, but nothing limits it.</p> : null}
      <div className="cards">
        {rows.map((r) => (
          <BudgetCard key={r.budget.id} row={r} onChanged={q.reload} />
        ))}
      </div>
    </Page>
  );
}
