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

/** An event's payload as a person reads it: indented JSON, or the text as stored. */
export function payloadText(data: unknown): string {
  if (data === undefined || data === null) return '';
  try {
    return JSON.stringify(data, null, 2);
  } catch {
    return String(data);
  }
}

/** Whether an event matches the inspector's filter: words that each appear in its type, its agent or its payload. */
export function eventMatches(e: { type: string; agentId?: string; data?: unknown }, agent: string, filter: string): boolean {
  const words = filter.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const hay = `${e.type} ${agent} ${e.agentId ?? ''} ${payloadText(e.data)}`.toLowerCase();
  return words.every((w) => hay.includes(w));
}

const payloadCss = css({
  margin: '0',
  maxHeight: '360px',
  overflow: 'auto',
  padding: `${t.s2} ${t.s3}`,
  fontSize: t.textXs,
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
  background: t.bgApp,
  borderRadius: t.rSm,
});

/**
 * The run's events as an inspector (P91): filtered by any words of the type, the agent or the payload, each row opening to its
 * payload as stored (redacted by the core, cut past 16 KB), with a Copy of the event as JSON.
 */
export function Events(handle: Handle<{ x: RunExplanation; agents: readonly Agent[] }>) {
  let filter = '';
  const open = new Set<number>();
  let copied = -1;
  const toggle = (seq: number) => {
    if (open.has(seq)) open.delete(seq);
    else open.add(seq);
    void handle.update();
  };
  const copy = async (e: RunExplanation['events'][number]) => {
    try {
      await navigator.clipboard.writeText(JSON.stringify(e, null, 2));
      copied = e.seq;
      void handle.update();
    } catch {
      /* the clipboard can be refused; the payload is on the page to select */
    }
  };
  return () => {
    const { x, agents } = handle.props;
    const shown = x.events.filter((e) => eventMatches(e, e.agentId ? agentName(e.agentId, agents) : '', filter));
    return (
      <div style={{ display: 'grid', gap: '8px' }}>
        {x.eventsTruncated ? <p mix={muted}>Only the latest {x.events.length} events are listed; the Activity page has all of them.</p> : null}
        <input
          type="search"
          aria-label="Filter events"
          placeholder="Filter by type, agent or anything in the payload"
          value={filter}
          style={{ maxWidth: '420px' }}
          mix={on('input', (e) => {
            filter = (e.currentTarget as HTMLInputElement).value;
            void handle.update();
          })}
        />
        {filter ? (
          <p mix={muted} data-testid="run-events-count">
            {shown.length} of {x.events.length} events
          </p>
        ) : null}
        <div mix={tableCard}>
          <table aria-label="Events">
            <thead>
              <tr>
                <th scope="col">#</th>
                <th scope="col">Time</th>
                <th scope="col">Type</th>
                <th scope="col">Agent</th>
                <th scope="col">
                  <span class="sr-only">Payload</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {shown.flatMap((e) => {
                const isOpen = open.has(e.seq);
                const rows = [
                  <tr key={e.seq} data-testid="run-event" data-type={e.type}>
                    <td mix={mono}>{e.seq}</td>
                    <td style={{ whiteSpace: 'nowrap' }}>{formatTime(e.ts)}</td>
                    <td>
                      <code>{e.type}</code>
                    </td>
                    <td>{e.agentId ? agentName(e.agentId, agents) || e.agentId : ''}</td>
                    <td style={{ textAlign: 'right' }}>
                      {e.data !== undefined ? (
                        <Button size="sm" variant="ghost" aria-expanded={isOpen ? 'true' : 'false'} mix={on('click', () => toggle(e.seq))}>
                          {isOpen ? 'Hide' : 'Payload'}
                        </Button>
                      ) : null}
                    </td>
                  </tr>,
                ];
                if (isOpen)
                  rows.push(
                    <tr key={`${e.seq}:data`} data-testid="run-event-payload">
                      <td colSpan={5}>
                        <div style={{ display: 'grid', gap: '4px' }}>
                          <pre mix={payloadCss} tabIndex={0} aria-label={`Payload of event ${e.seq}`}>
                            {payloadText(e.data)}
                          </pre>
                          <span>
                            <Button size="sm" variant="secondary" mix={on('click', () => void copy(e))}>
                              {copied === e.seq ? 'Copied' : 'Copy as JSON'}
                            </Button>
                          </span>
                        </div>
                      </td>
                    </tr>,
                  );
                return rows;
              })}
            </tbody>
          </table>
        </div>
      </div>
    );
  };
}
