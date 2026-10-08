import { useMemo, useState } from 'react';
import type { DrydockEvent } from '../api/types';
import { formatTime } from '../lib/format';
import { useStoreSnapshot } from '../state/context';
import { Empty, Page } from './common';

/** Prefix filter as in the core: "task.*" or "task." match by prefix, anything else exactly. Comma separated; empty matches all. */
export function matchesFilter(type: string, filter: string): boolean {
  const parts = filter
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  if (parts.length === 0) return true;
  return parts.some((p) => (p.endsWith('*') ? type.startsWith(p.slice(0, -1)) : p.endsWith('.') ? type.startsWith(p) : type === p));
}

export function ActivityView({ projectId }: { projectId: string | undefined }) {
  const snap = useStoreSnapshot();
  const [filter, setFilter] = useState('');
  const [thisProject, setThisProject] = useState(true);
  const rows = useMemo(() => {
    const out: DrydockEvent[] = [];
    for (let i = snap.events.length - 1; i >= 0 && out.length < 500; i--) {
      const e = snap.events[i]!;
      if (thisProject && projectId && e.projectId && e.projectId !== projectId) continue;
      if (matchesFilter(e.type, filter)) out.push(e);
    }
    return out;
  }, [snap.events, filter, thisProject, projectId]);
  return (
    <Page title="Activity">
      <p className="muted">
        The event log as the core persisted it (newest first). Stream: {snap.status}, last seq {snap.lastSeq}, {snap.events.length} events held.
      </p>
      <div className="form-actions">
        <label>
          Filter by type prefix
          <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="task.*, agent.*" spellCheck={false} />
        </label>
        <label className="inline">
          <input type="checkbox" checked={thisProject} onChange={(e) => setThisProject(e.target.checked)} /> This project only
        </label>
      </div>
      {rows.length === 0 ? <Empty title="No matching events" /> : null}
      <table className="table activity" aria-label="Events">
        <thead>
          <tr>
            <th scope="col">Seq</th>
            <th scope="col">Time</th>
            <th scope="col">Type</th>
            <th scope="col">Scope</th>
            <th scope="col">Data</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((e) => (
            <tr key={e.seq} data-testid="event-row" data-type={e.type}>
              <td>{e.seq}</td>
              <td>{formatTime(e.time)}</td>
              <td>
                <code>{e.type}</code>
              </td>
              <td className="muted">{[e.taskId, e.agentId, e.runId].filter(Boolean).join(' ')}</td>
              <td className="muted data-cell">{e.data === undefined ? '' : JSON.stringify(e.data).slice(0, 200)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Page>
  );
}
