// The checkpoints, MCP calls and events of a run, as tables. Read-only.

import { css, on, type Handle } from 'remix/component';
import type { Agent } from '../../api/types.ts';
import type { RunExplanation } from '../../api/runClient.ts';
import { agentName, formatTime } from '../../lib/format.ts';
import { Button } from '../../ui/button.tsx';
import { Badge, mono, muted } from '../../ui/data.tsx';
import { tableCard } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { compact } from './run-model.ts';

const rowSel = css({ '& tr[aria-current="true"] td': { background: t.accentSoft } });
const wrapCode = css({ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm });

export function Checkpoints(handle: Handle<{ x: RunExplanation; asOf?: string; onShow?: (id: string) => void }>) {
  return () => {
    const { x, asOf, onShow } = handle.props;
    if (x.checkpoints.length === 0) return <p mix={muted}>No checkpoints.</p>;
    return (
      <div mix={[tableCard, rowSel]}>
        <table aria-label="Checkpoints">
          <thead>
            <tr>
              <th scope="col">Step</th>
              <th scope="col">Kind</th>
              <th scope="col">Time</th>
              <th scope="col">Run status</th>
              <th scope="col">After</th>
              <th scope="col">Waiting</th>
              {onShow ? (
                <th scope="col">
                  <span class="sr-only">Graph</span>
                </th>
              ) : null}
            </tr>
          </thead>
          <tbody>
            {x.checkpoints.map((c) => (
              <tr key={c.id} data-testid="checkpoint" data-latest={String(!!c.latest)} aria-current={asOf === c.id ? 'true' : undefined}>
                <td mix={mono}>{c.step}</td>
                <td>
                  {c.kind} {c.latest ? <Badge tone="info">latest</Badge> : null}
                </td>
                <td style={{ whiteSpace: 'nowrap' }}>{formatTime(c.created)}</td>
                <td>{c.status}</td>
                <td>{(c.lastNodes ?? []).join(', ')}</td>
                <td>{(c.waiting ?? []).join(', ')}</td>
                {onShow ? (
                  <td style={{ textAlign: 'right' }}>
                    <Button variant="link" size="sm" mix={[on('click', () => onShow(c.id))]}>
                      Show on the graph
                    </Button>
                  </td>
                ) : null}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  };
}

export function McpCalls(handle: Handle<{ x: RunExplanation; agents: readonly Agent[] }>) {
  return () => {
    const { x, agents } = handle.props;
    if (x.mcpCalls.length === 0) return <p mix={muted}>No MCP calls in this run.</p>;
    return (
      <div mix={tableCard}>
        <table aria-label="MCP calls">
          <thead>
            <tr>
              <th scope="col">Time</th>
              <th scope="col">Event</th>
              <th scope="col">Agent</th>
              <th scope="col">Details</th>
            </tr>
          </thead>
          <tbody>
            {x.mcpCalls.map((e) => (
              <tr key={e.seq} data-testid="mcp-call">
                <td style={{ whiteSpace: 'nowrap' }}>{formatTime(e.ts)}</td>
                <td>{e.type === 'mcp.call' ? 'call' : <Badge tone="warn">{e.type.replace('mcp.', '')}</Badge>}</td>
                <td>{agentName(e.agentId ?? '', agents) || e.agentId}</td>
                <td>
                  <code mix={wrapCode}>{compact(e.data)}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  };
}

export function Events(handle: Handle<{ x: RunExplanation; agents: readonly Agent[] }>) {
  return () => {
    const { x, agents } = handle.props;
    return (
      <div style={{ display: 'grid', gap: '8px' }}>
        {x.eventsTruncated ? <p mix={muted}>Only the latest {x.events.length} events are listed; the Activity page has all of them.</p> : null}
        <div mix={tableCard}>
          <table aria-label="Events">
            <thead>
              <tr>
                <th scope="col">#</th>
                <th scope="col">Time</th>
                <th scope="col">Type</th>
                <th scope="col">Agent</th>
              </tr>
            </thead>
            <tbody>
              {x.events.map((e) => (
                <tr key={e.seq} data-testid="run-event">
                  <td mix={mono}>{e.seq}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>{formatTime(e.ts)}</td>
                  <td>
                    <code>{e.type}</code>
                  </td>
                  <td>{e.agentId ? agentName(e.agentId, agents) || e.agentId : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    );
  };
}
