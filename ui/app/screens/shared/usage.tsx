import type { Handle } from 'remix/component';
import type { UsageRow } from '../../api/types.ts';
import { formatCost, formatTime } from '../../lib/format.ts';
import { Badge, mono, muted } from '../../ui/data.tsx';
import { tableCard } from '../../ui/layout.tsx';

/** Where a usage figure came from. Anything but reported or estimated is shown as unknown, never guessed. */
export function SourceTag(handle: Handle<{ source: string }>) {
  return () => {
    const source = handle.props.source;
    if (source === 'reported')
      return (
        <Badge tone="ok" title="Reported by the agent CLI or the provider">
          reported
        </Badge>
      );
    if (source === 'estimated')
      return (
        <Badge tone="warn" title="Estimated by Drydock from a price table or a token count">
          estimated
        </Badge>
      );
    return <Badge title="The core did not say where this number came from">{source || 'source unknown'}</Badge>;
  };
}

export function distinctSources(rows: readonly UsageRow[]): string[] {
  return Array.from(new Set(rows.map((r) => r.source || 'unknown'))).sort();
}

export function UsageTable(handle: Handle<{ rows: readonly UsageRow[] }>) {
  return () => {
    const rows = handle.props.rows;
    if (rows.length === 0) return <p mix={muted}>No usage has been recorded.</p>;
    return (
      <div mix={tableCard}>
        <table aria-label="Usage">
          <thead>
            <tr>
              <th scope="col">Time</th>
              <th scope="col">Model</th>
              <th scope="col">Tokens in / out / cached</th>
              <th scope="col">Cost</th>
              <th scope="col">Source</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((u, i) => (
              <tr key={`${u.ts}-${i}`} data-testid="usage-row" data-source={u.source}>
                <td style={{ whiteSpace: 'nowrap' }}>{formatTime(u.ts)}</td>
                <td>
                  {u.provider}
                  {u.model ? (
                    <>
                      {' / '}
                      <code>{u.model}</code>
                    </>
                  ) : null}{' '}
                  <span mix={muted}>{u.tier}</span>
                </td>
                <td mix={mono}>
                  {u.inputTokens} / {u.outputTokens} / {u.cachedTokens}
                </td>
                <td>{formatCost(u.costUsd)}</td>
                <td>
                  <SourceTag source={u.source} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  };
}
