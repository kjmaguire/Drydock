import type { PresenceState } from '../api/types';
import { formatDateTime } from '../lib/format';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, PresenceDot } from './common';
import { UsageTable } from './Usage';
import { TaskDecisions } from './Explain';

const time = (ms: number) => (ms > 0 ? formatDateTime(ms) : '');

/** Agent Inspector (docs/ui.md section 10): only what the core can report today. Everything else is stated as absent. */
export function AgentInspector({ agentId, presence, onMessage }: { agentId: string | undefined; presence: readonly PresenceState[]; onMessage?: (agentId: string) => void }) {
  const client = useClient();
  const q = useLive(
    () => client.agentGet(agentId!),
    [agentId],
    (e) => e.agentId === agentId || (e.type.startsWith('task.') && !e.agentId) || e.type === 'room.message_posted',
    !!agentId,
    200,
  );
  if (!agentId) return <p className="muted">Choose an agent and press Inspect.</p>;
  const d = q.data;
  if (!d) {
    return (
      <div>
        <ErrorNote error={q.error} what="Could not load the agent" />
        {q.loading ? <p className="muted">Loading...</p> : null}
      </div>
    );
  }
  const a = d.agent;
  const runs = d.runs ?? [];
  const usage = d.usage ?? [];
  const p = presence.find((x) => x.agentId === a.id);
  return (
    <div className="inspector" data-testid="inspector" data-agent={a.id}>
      <h3>{a.name}</h3>
      <dl className="kv">
        <dt>Id</dt>
        <dd>
          <code>{a.id}</code>
        </dd>
        <dt>Adapter</dt>
        <dd>{a.adapter}</dd>
        <dt>Role</dt>
        <dd>{a.role}</dd>
        <dt>Purpose</dt>
        <dd data-testid="inspector-purpose" style={{ whiteSpace: 'pre-wrap' }}>{a.purpose || 'none set'}</dd>
        <dt>Model</dt>
        <dd>{a.model || 'adapter default (not set)'}</dd>
        <dt>Effort</dt>
        <dd>{a.effort || 'not set'}</dd>
        <dt>Permission mode</dt>
        <dd>{a.permissionMode}</dd>
        {d.isolation ? (
          <>
            <dt>Isolation</dt>
            <dd data-testid="inspector-isolation" data-level={d.isolation.level}>
              <Badge tone={d.isolation.level === 'guarded' ? 'ok' : 'warn'}>{d.isolation.level === 'guarded' ? 'guarded' : 'not held in'}</Badge> {d.isolation.summary}
              <ul>
                {(d.isolation.details ?? []).map((x, i) => (
                  <li key={i} className="muted">
                    {x}
                  </li>
                ))}
              </ul>
            </dd>
          </>
        ) : null}
        <dt>Presence</dt>
        <dd>
          <PresenceDot presence={p} />
          {p?.activity ? <div className="muted">{p.activity}</div> : null}
        </dd>
        <dt>Why this model?</dt>
        <dd className="muted">Not routed: the model was chosen in team setup (the model router is Slice 2).</dd>
      </dl>
      {onMessage ? <button onClick={() => onMessage(a.id)}>Message {a.name}</button> : null}
      <h4>Runs</h4>
      {runs.length === 0 ? <p className="muted">This agent has not run yet.</p> : null}
      {runs.map((r) => (
        <div key={r.id} className="run" data-testid="agent-run">
          <div>
            <Badge tone={r.status === 'failed' ? 'err' : r.status === 'running' ? 'info' : 'muted'}>{r.status}</Badge> <span className="muted">{time(r.startedAt)}</span>
          </div>
          <dl className="kv">
            <dt>PID</dt>
            <dd>{r.pid > 0 ? r.pid : 'not reported'}</dd>
            <dt>Working dir</dt>
            <dd>{r.cwd ? <code>{r.cwd}</code> : 'not reported'}</dd>
            <dt>Ended</dt>
            <dd>{r.endedAt > 0 ? time(r.endedAt) : 'not ended'}</dd>
            {r.taskId ? (
              <>
                <dt>Task</dt>
                <dd>
                  <a href={`#/task/${encodeURIComponent(r.taskId)}`}>{r.taskId}</a> ·{' '}
                  <a href={`#/task/${encodeURIComponent(r.taskId)}/run`}>what it was given and why</a>
                </dd>
              </>
            ) : null}
          </dl>
          {r.error ? <p className="error-note">{r.error}</p> : null}
        </div>
      ))}
      <h4>Usage</h4>
      <UsageTable rows={usage} />
      {runs.find((r) => r.taskId) ? (
        <>
          <h4>Why this model? (latest task)</h4>
          <TaskDecisions taskId={runs.find((r) => r.taskId)!.taskId} />
        </>
      ) : null}
    </div>
  );
}
