import { lazy, Suspense, useState } from 'react';
import type { Agent } from '../api/types';
import type { ExecView, RunExplanation } from '../api/runClient';
import { Badge, ErrorNote, Page } from '../components/common';
import { UsageTable } from '../components/Usage';
import { openDock } from '../dock/Dock';
import { agentName, formatCost, formatTime } from '../lib/format';
import { useClient } from '../state/context';
import { useLive, useTaskDetail } from '../state/hooks';

const WorkflowGraph = lazy(() => import('../components/WorkflowGraph'));

type Tab = 'graph' | 'turns' | 'checkpoints' | 'mcp' | 'events';

function duration(from: number, to?: number): string {
  if (!from) return '';
  const ms = (to || Date.now()) - from;
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

function formatBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
}

const STATUS_TONE: Record<string, 'ok' | 'err' | 'info' | 'warn' | 'muted'> = { done: 'ok', failed: 'err', started: 'info', abandoned: 'warn' };

/**
 * A run, explained (docs/ui.md S14 and S16): the workflow graph with its checkpoint timeline, and every turn with what it was
 * given (context manifest and exact prompt), what it cost, which model and why, plus the checkpoints, MCP calls and events.
 * Read-only.
 */
export function RunView({ taskId, runId, agents }: { taskId: string; runId?: string; agents: Agent[] }) {
  const client = useClient();
  const task = useTaskDetail(taskId).data?.task;
  const q = useLive(() => client.runExplain(taskId, runId), [taskId, runId], (e) => e.taskId === taskId || (!!e.runId && e.runId === runId), true, 400);
  const [tab, setTab] = useState<Tab>('graph');
  const x = q.data;
  const title = task ? `Run of #${task.seq} ${task.title}` : 'Run';
  return (
    <Page title={title} actions={<a href={`#/task/${encodeURIComponent(taskId)}`}>Back to the task</a>}>
      <ErrorNote error={q.error} what="Could not explain the run" />
      {!x ? (
        q.loading ? <p className="muted">Loading...</p> : null
      ) : !x.run ? (
        <p className="muted">This task has not run yet.</p>
      ) : (
        <>
          <RunHeader x={x} taskId={taskId} />
          <div role="tablist" aria-label="Run" className="tabs">
            {(
              [
                ['graph', 'Graph'],
                ['turns', `Turns (${x.executions.length})`],
                ['checkpoints', `Checkpoints (${x.checkpoints.length})`],
                ['mcp', `MCP calls (${x.mcpCalls.length})`],
                ['events', `Events (${x.events.length})`],
              ] as [Tab, string][]
            ).map(([id, label]) => (
              <button key={id} role="tab" aria-selected={tab === id} onClick={() => setTab(id)}>
                {label}
              </button>
            ))}
          </div>
          {tab === 'graph' ? (
            <Suspense fallback={<p className="muted">Loading the graph...</p>}>
              <WorkflowGraph x={x} agents={agents} onOpenTurn={() => setTab('turns')} />
            </Suspense>
          ) : null}
          {tab === 'turns' ? <Turns x={x} taskId={taskId} agents={agents} /> : null}
          {tab === 'checkpoints' ? <Checkpoints x={x} /> : null}
          {tab === 'mcp' ? <McpCalls x={x} agents={agents} /> : null}
          {tab === 'events' ? <Events x={x} agents={agents} /> : null}
        </>
      )}
    </Page>
  );
}

function RunHeader({ x, taskId }: { x: RunExplanation; taskId: string }) {
  const r = x.run!;
  const cost = x.executions.flatMap((e) => e.usage ?? []).reduce((s, u) => s + u.costUsd, 0);
  return (
    <div>
      <dl className="kv">
        <dt>Workflow</dt>
        <dd>
          <code>{r.workflow}</code>
        </dd>
        <dt>Status</dt>
        <dd>
          <Badge tone={r.status === 'COMPLETED' ? 'ok' : r.status === 'FAILED' ? 'err' : 'info'}>{r.status}</Badge>
          {r.failedReason ? <span className="muted"> {r.failedReason}</span> : null}
        </dd>
        <dt>Started</dt>
        <dd>{formatTime(r.created)}</dd>
        <dt>Cost of its turns</dt>
        <dd>{formatCost(cost)}</dd>
      </dl>
      {x.runs.length > 1 ? (
        <p>
          This task ran {x.runs.length} times:{' '}
          {x.runs.map((o, i) => (
            <span key={o.id}>
              {i > 0 ? ' · ' : ''}
              {o.id === r.id ? <strong>{formatTime(o.created)}</strong> : <a href={`#/task/${encodeURIComponent(taskId)}/run/${encodeURIComponent(o.id)}`}>{formatTime(o.created)}</a>} ({o.status})
            </span>
          ))}
        </p>
      ) : null}
    </div>
  );
}

export function Turns({ x, taskId, agents }: { x: RunExplanation; taskId: string; agents: Agent[] }) {
  const [open, setOpen] = useState<string | undefined>();
  if (x.executions.length === 0) return <p className="muted">No node has run yet.</p>;
  return (
    <ol className="turns" aria-label="Turns">
      {x.executions.map((e) => {
        const key = `${e.nodeId}|${e.step}|${e.attempt}`;
        const tokens = (e.usage ?? []).reduce((s, u) => s + u.inputTokens + u.outputTokens, 0);
        const cost = (e.usage ?? []).reduce((s, u) => s + u.costUsd, 0);
        return (
          <li key={key} className="turn" data-testid="turn" data-node={e.nodeId}>
            <button className="turn-head" aria-expanded={open === key} onClick={() => setOpen(open === key ? undefined : key)}>
              <span className="muted">step {e.step}</span> <strong>{e.nodeId}</strong> {e.attempt > 1 ? <span className="muted">attempt {e.attempt}</span> : null}{' '}
              <Badge tone={STATUS_TONE[e.status] ?? 'muted'}>{e.status}</Badge> <span className="muted">{duration(e.started, e.finished)}</span>
              {e.agentId ? <span> · {agentName(e.agentId, agents) || e.agentId}</span> : null}
              {e.decision?.model ? <span className="muted"> · {e.decision.model}</span> : null}
              {e.usage && e.usage.length ? (
                <span className="muted">
                  {' '}
                  · {tokens} tokens · {formatCost(cost)}
                </span>
              ) : null}
            </button>
            {open === key ? <TurnDetail e={e} taskId={taskId} runId={x.run!.id} /> : null}
          </li>
        );
      })}
    </ol>
  );
}

function TurnDetail({ e, taskId, runId }: { e: ExecView; taskId: string; runId: string }) {
  const client = useClient();
  const [prompt, setPrompt] = useState<string | undefined>();
  const [error, setError] = useState<Error | undefined>();
  return (
    <div className="turn-detail">
      {e.error ? (
        <p>
          <Badge tone="err">error</Badge> {e.error}
        </p>
      ) : null}
      {e.decision ? (
        <section aria-label="Why this model">
          <h4>Why this model</h4>
          <p>
            Decided by <strong>{e.decision.decidedBy}</strong>
            {e.decision.model ? (
              <>
                : <code>{e.decision.model}</code>
                {e.decision.effort ? ` (${e.decision.effort})` : ''}
              </>
            ) : null}
            {e.decision.escalatedFrom ? <> · escalated from {e.decision.escalatedFrom}</> : null}
            {e.decision.estCostUsd !== undefined && e.decision.estCostUsd !== null ? <> · estimated {formatCost(e.decision.estCostUsd)}</> : null}
          </p>
          {e.decision.reason ? <p className="muted">{e.decision.reason}</p> : null}
        </section>
      ) : null}
      {e.manifest && e.manifest.length ? (
        <section aria-label="What it was given">
          <h4>What it was given ({formatBytes(e.includedBytes ?? 0)} included)</h4>
          <table className="table" aria-label="Context manifest">
            <thead>
              <tr>
                <th scope="col">Source</th>
                <th scope="col">Included</th>
                <th scope="col">Size</th>
                <th scope="col">Priority</th>
                <th scope="col">Why</th>
              </tr>
            </thead>
            <tbody>
              {e.manifest.map((m, i) => (
                <tr key={`${m.source}-${i}`} data-testid="manifest-item" data-included={String(m.included)}>
                  <td>
                    <code>{m.source}</code>
                  </td>
                  <td>{m.included ? 'yes' : 'no'}</td>
                  <td>{m.bytes ? formatBytes(m.bytes) : ''}</td>
                  <td>{m.priority}</td>
                  <td>{m.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ) : null}
      <p className="turn-actions">
        {e.hasPrompt ? (
          <button
            onClick={async () => {
              setError(undefined);
              try {
                setPrompt(await client.runPrompt(taskId, runId, e.nodeId, e.step, e.attempt));
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Show the exact prompt
          </button>
        ) : null}
        {e.logId ? <button onClick={() => openDock('logs')}>Open its stream in the Logs tab</button> : null}
        {e.safePoint ? (
          <span className="muted">
            safe point <code>{e.safePoint.slice(0, 10)}</code>
          </span>
        ) : null}
      </p>
      <ErrorNote error={error} what="Could not read the prompt" />
      {prompt !== undefined ? (
        <pre className="log-text prompt-text" data-testid="prompt-text" tabIndex={0} aria-label="The exact prompt">
          {prompt}
        </pre>
      ) : null}
      {e.usage && e.usage.length ? (
        <section aria-label="Usage">
          <h4>Usage</h4>
          <UsageTable rows={e.usage} />
        </section>
      ) : null}
    </div>
  );
}

function Checkpoints({ x }: { x: RunExplanation }) {
  if (x.checkpoints.length === 0) return <p className="muted">No checkpoints.</p>;
  return (
    <table className="table" aria-label="Checkpoints">
      <thead>
        <tr>
          <th scope="col">Step</th>
          <th scope="col">Kind</th>
          <th scope="col">Time</th>
          <th scope="col">Run status</th>
          <th scope="col">After</th>
          <th scope="col">Waiting</th>
        </tr>
      </thead>
      <tbody>
        {x.checkpoints.map((c) => (
          <tr key={c.id} data-testid="checkpoint" data-latest={String(!!c.latest)}>
            <td>{c.step}</td>
            <td>
              {c.kind} {c.latest ? <Badge tone="info">latest</Badge> : null}
            </td>
            <td>{formatTime(c.created)}</td>
            <td>{c.status}</td>
            <td>{(c.lastNodes ?? []).join(', ')}</td>
            <td>{(c.waiting ?? []).join(', ')}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function compact(data: unknown): string {
  try {
    const s = JSON.stringify(data);
    return s.length > 400 ? `${s.slice(0, 400)}…` : s;
  } catch {
    return '';
  }
}

function McpCalls({ x, agents }: { x: RunExplanation; agents: Agent[] }) {
  if (x.mcpCalls.length === 0) return <p className="muted">No MCP calls in this run.</p>;
  return (
    <table className="table" aria-label="MCP calls">
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
          <tr key={e.seq}>
            <td>{formatTime(e.ts)}</td>
            <td>{e.type === 'mcp.call' ? 'call' : <Badge tone="warn">{e.type.replace('mcp.', '')}</Badge>}</td>
            <td>{agentName(e.agentId ?? '', agents) || e.agentId}</td>
            <td>
              <code className="wrap">{compact(e.data)}</code>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Events({ x, agents }: { x: RunExplanation; agents: Agent[] }) {
  return (
    <>
      {x.eventsTruncated ? <p className="muted">Only the latest {x.events.length} events are listed; the Activity page has all of them.</p> : null}
      <table className="table" aria-label="Events">
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
            <tr key={e.seq}>
              <td>{e.seq}</td>
              <td>{formatTime(e.ts)}</td>
              <td>
                <code>{e.type}</code>
              </td>
              <td>{e.agentId ? agentName(e.agentId, agents) || e.agentId : ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
