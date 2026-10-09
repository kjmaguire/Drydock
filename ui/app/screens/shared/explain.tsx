import { css, on, type Handle } from 'remix/component';
import type { DecisionRow, RoutingDecision } from '../../api/extTypes.ts';
import { formatUsd } from '../../lib/format.ts';
import { query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { card, tableCard } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';

export const money = (v: number | null | undefined) => (v === null || v === undefined ? 'price unknown' : formatUsd(v));

const explainCss = css({ display: 'grid', gap: t.s2, '& summary': { fontSize: t.textSm }, '& ul': { margin: `${t.s2} 0 0`, paddingLeft: '20px' } });

/** "Why this model?" for one decision: the router's own explanation plus the facts. Decided-by static says it was not routed. */
export function ExplainText(handle: Handle<{ decisionId: string }>) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.routeExplain(handle.props.decisionId), key: () => handle.props.decisionId });
  return () => {
    if (q.error) return <ErrorNote error={q.error} what="Could not explain" />;
    if (!q.data) return <Loading />;
    const d: RoutingDecision = q.data.decision;
    const alts = d.Alternatives ?? [];
    return (
      <div mix={[card, explainCss]} data-testid="explain">
        <Markdown source={q.data.text} />
        {d.DecidedBy === 'static' ? <p mix={muted}>This was the agent&apos;s configured assignment. The router did not choose it.</p> : null}
        {alts.length > 0 ? (
          <details>
            <summary>{alts.length} alternative(s) not chosen</summary>
            <ul>
              {alts.map((a) => (
                <li>
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
  };
}

/** The decisions made for a task, each with an Explain button. Used in the task view and the agent inspector. */
export function TaskDecisions(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  const q = query(handle, {
    load: () => s.client.routeDecisions(handle.props.taskId),
    key: () => handle.props.taskId,
    when: (e) => e.type.startsWith('routing.') && (!e.taskId || e.taskId === handle.props.taskId),
  });
  let open: string | undefined;
  return () => {
    const rows: DecisionRow[] = q.data ?? [];
    return (
      <div data-testid="task-decisions" style={{ display: 'grid', gap: '12px' }}>
        <ErrorNote error={q.error} what="Could not load routing decisions" />
        {q.data && rows.length === 0 ? <p mix={muted}>No routing decision has been recorded for this task.</p> : null}
        {rows.length > 0 ? (
          <div mix={tableCard}>
            <table aria-label="Routing decisions">
              <thead>
                <tr>
                  <th scope="col">Run</th>
                  <th scope="col">Model</th>
                  <th scope="col">Decided by</th>
                  <th scope="col">Est. cost</th>
                  <th scope="col">Outcome</th>
                  <th scope="col">
                    <span class="sr-only">Explain</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.id} data-testid="decision-row" data-decided-by={r.decidedBy}>
                    <td>
                      {r.runKind} <span mix={muted}>attempt {r.attempt}</span>
                    </td>
                    <td>
                      <code>
                        {r.provider}/{r.model || 'default'}
                      </code>{' '}
                      <span mix={muted}>effort {r.effort || 'not set'}</span>
                    </td>
                    <td>
                      {r.decidedBy === 'static' ? <Badge title="Not routed: the agent's configured assignment">static</Badge> : <Badge tone="info">{r.decidedBy}</Badge>}
                    </td>
                    <td>{money(r.estCostUsd)}</td>
                    <td>{r.outcome ? `${r.outcome.outcome}${r.outcome.accepted ? ', accepted' : ''}${r.outcome.escalated ? ', escalated' : ''}` : 'pending'}</td>
                    <td style={{ textAlign: 'right' }}>
                      <Button
                        variant="link"
                        aria-expanded={open === r.id}
                        mix={on('click', () => {
                          open = open === r.id ? undefined : r.id;
                          void handle.update();
                        })}
                      >
                        Explain why this model
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {open ? <ExplainText key={open} decisionId={open} /> : null}
      </div>
    );
  };
}
