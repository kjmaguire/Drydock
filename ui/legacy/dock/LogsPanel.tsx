import { useCallback, useEffect, useRef, useState } from 'react';
import type { LogSource } from '../api/dockClient';
import { Badge, ErrorNote } from '../components/common';
import { formatTime } from '../lib/format';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';

/** How much is read at a time, and the most lines kept in memory (older ones are fetched again on request). */
const CHUNK = 64 << 10;
const MAX_LINES = 5000;

/** A task's check logs and agent streams (docs/ui.md 2, the Logs tab). Read by range: the tail first, earlier on request. */
export function LogsPanel({ taskId }: { taskId: string }) {
  const client = useClient();
  const list = useLive(() => client.logsList(taskId), [taskId], (e) => e.taskId === taskId, true, 500);
  const [selected, setSelected] = useState<string | undefined>();
  const logs = list.data ?? [];
  const current = logs.find((l) => l.id === selected) ?? logs[logs.length - 1];
  if (list.loading && !list.data) return <p className="muted">Loading logs...</p>;
  return (
    <div className="logs-panel" data-testid="logs-panel">
      <ErrorNote error={list.error} what="Could not list the logs" />
      {logs.length === 0 ? (
        <p className="muted">No logs yet. Check output and agent streams appear here once the task runs.</p>
      ) : (
        <div className="logs-body">
          <ul className="logs-list" aria-label="Logs">
            {logs.map((l) => (
              <li key={l.id}>
                <button className={l === current ? 'logs-item active' : 'logs-item'} aria-current={l === current ? 'true' : undefined} onClick={() => setSelected(l.id)}>
                  <LogLabel l={l} />
                </button>
              </li>
            ))}
          </ul>
          {current ? <LogView taskId={taskId} source={current} key={current.id} /> : null}
        </div>
      )}
    </div>
  );
}

function LogLabel({ l }: { l: LogSource }) {
  return (
    <>
      <span>{l.kind === 'agent' ? 'Agent: ' : 'Check: '}</span>
      <strong>{l.title}</strong>{' '}
      {l.passed === true ? <Badge tone="ok">passed</Badge> : l.passed === false ? <Badge tone="err">failed</Badge> : null}
      {l.running ? <Badge tone="info">running</Badge> : null}
      <span className="muted"> {formatTime(l.created)}</span>
    </>
  );
}

function LogView({ taskId, source }: { taskId: string; source: LogSource }) {
  const client = useClient();
  const [lines, setLines] = useState<string[]>([]);
  const [start, setStart] = useState(0);
  const [error, setError] = useState<Error | undefined>();
  const next = useRef(0);
  const box = useRef<HTMLPreElement>(null);
  const [follow, setFollow] = useState(true);

  // a read can end mid-line; that part waits for the rest instead of becoming a line of its own
  const partial = useRef('');
  const append = useCallback((text: string) => {
    if (!text) return;
    const all = partial.current + text;
    const cut = all.lastIndexOf('\n');
    partial.current = all.slice(cut + 1);
    if (cut < 0) return;
    const add = all.slice(0, cut).split('\n');
    setLines((prev) => {
      const out = prev.concat(add);
      return out.length > MAX_LINES ? out.slice(out.length - MAX_LINES) : out;
    });
  }, []);

  // first the tail, then whatever arrives while the source is still being written
  useEffect(() => {
    let stop = false;
    const tick = async (first: boolean) => {
      try {
        const r = await client.logsRead(taskId, source.id, first ? { tail: true, limit: CHUNK } : { from: next.current, limit: CHUNK });
        if (stop) return;
        if (first) setStart(r.start);
        next.current = r.next;
        append(r.text);
        setError(undefined);
        return r;
      } catch (e) {
        if (!stop) setError(e as Error);
        return undefined;
      }
    };
    void tick(true);
    const timer = setInterval(() => {
      void tick(false);
    }, source.running ? 1000 : 4000);
    return () => {
      stop = true;
      clearInterval(timer);
    };
  }, [client, taskId, source.id, source.running, append]);

  useEffect(() => {
    if (follow && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [lines, follow]);

  const earlier = async () => {
    try {
      const from = Math.max(0, start - CHUNK);
      const r = await client.logsRead(taskId, source.id, { from, limit: start - from });
      setStart(from);
      const add = r.text.replace(/\n$/, '').split('\n');
      setLines((prev) => add.concat(prev).slice(0, MAX_LINES));
    } catch (e) {
      setError(e as Error);
    }
  };

  return (
    <div className="log-view">
      <div className="diff-toolbar">
        {start > 0 ? <button onClick={earlier}>Show earlier output</button> : <span className="muted">From the beginning</span>}
        <label className="inline">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow
        </label>
      </div>
      <ErrorNote error={error} what="Could not read the log" />
      <pre
        ref={box}
        className="log-text"
        tabIndex={0}
        role="log"
        aria-label={`${source.title} log`}
        data-testid="log-text"
        onScroll={(e) => {
          const el = e.currentTarget;
          setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 8);
        }}
      >
        {lines.join('\n')}
        {partial.current ? (lines.length ? '\n' : '') + partial.current : ''}
      </pre>
    </div>
  );
}
