import { useState } from 'react';
import type { DecisionRow, RoutingDecision } from '../api/extTypes';
import { Markdown } from '../lib/Markdown';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote } from './common';

export const money = (v: number | null | undefined) => (v === null || v === undefined ? 'price unknown' : `$${v < 0.01 ? v.toFixed(4) : v.toFixed(2)}`);

/** "Why this model?" for one decision: the router's own explanation plus the facts. Decided-by static says it was not routed. */
export function ExplainText({ decisionId }: { decisionId: string }) {
  const client = useClient();
  const q = useLive(() => client.routeExplain(decisionId), [decisionId], () => false);
  if (q.error) return <ErrorNote error={q.error} what="Could not explain" />;
  if (!q.data) return <p className="muted">Loading...</p>;
  const d: RoutingDecision = q.data.decision;
  return (
    <div className="explain" data-testid="explain">
      <Markdown source={q.data.text} />
      {d.DecidedBy === 'static' ? <p className="muted">This was the agent&apos;s configured assignment. The router did not choose it.</p> : null}
      {(d.Alternatives ?? []).length > 0 ? (
        <details>
          <summary>{(d.Alternatives ?? []).length} alternative(s) not chosen</summary>
          <ul>
            {(d.Alternatives ?? []).map((a, i) => (
              <li key={i}>
                <code>
                  {a.Provider}/{a.Model}
                </code>{' '}
                ({a.Tier}): {a.Rejected} {a.Detail}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  );
}

/** The decisions made for a task, each with an Explain button. Used in the task view and the agent inspector. */
export function TaskDecisions({ taskId }: { taskId: string }) {
  const client = useClient();
  const q = useLive(() => client.routeDecisions(taskId), [taskId], (e) => e.type.startsWith('routing.') && (!e.taskId || e.taskId === taskId));
  const [open, setOpen] = useState<string | undefined>();
  const rows: DecisionRow[] = q.data ?? [];
  return (
    <div data-testid="task-decisions">
      <ErrorNote error={q.error} what="Could not load routing decisions" />
      {q.data && rows.length === 0 ? <p className="muted">No routing decision has been recorded for this task.</p> : null}
      {rows.length > 0 ? (
        <table className="table" aria-label="Routing decisions">
          <thead>
            <tr>
              <th scope="col">Run</th>
              <th scope="col">Model</th>
              <th scope="col">Decided by</th>
              <th scope="col">Est. cost</th>
              <th scope="col">Outcome</th>
              <th scope="col" />
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} data-testid="decision-row" data-decided-by={r.decidedBy}>
                <td>
                  {r.runKind} <span className="muted">attempt {r.attempt}</span>
                </td>
                <td>
                  <code>
                    {r.provider}/{r.model || 'default'}
                  </code>{' '}
                  <span className="muted">effort {r.effort || 'not set'}</span>
                </td>
                <td>{r.decidedBy === 'static' ? <Badge title="Not routed: the agent's configured assignment">static</Badge> : <Badge tone="info">{r.decidedBy}</Badge>}</td>
                <td>{money(r.estCostUsd)}</td>
                <td>{r.outcome ? `${r.outcome.outcome}${r.outcome.accepted ? ', accepted' : ''}${r.outcome.escalated ? ', escalated' : ''}` : 'pending'}</td>
                <td>
                  <button className="link-button" onClick={() => setOpen(open === r.id ? undefined : r.id)} aria-expanded={open === r.id}>
                    Explain why this model
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      {open ? <ExplainText decisionId={open} /> : null}
    </div>
  );
}

