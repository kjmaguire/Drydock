import { useState } from 'react';
import type { MemoryEntry } from '../api/extTypes';
import { Markdown } from '../lib/Markdown';
import { formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';

export const MEMORY_KINDS = ['decision', 'convention', 'api_contract', 'rejected', 'known_issue', 'preference'];

export function MemoryCard({ e, busy, onAccept, onReject, onDelete }: { e: MemoryEntry; busy: boolean; onAccept: () => void; onReject: () => void; onDelete: () => void }) {
  const [confirm, setConfirm] = useState(false);
  return (
    <article className="card" data-testid="memory" data-status={e.status} data-title={e.title}>
      <header className="card-head">
        <h3>{e.title}</h3>
        <Badge>{e.kind}</Badge>
        <Badge tone={e.status === 'accepted' ? 'ok' : e.status === 'proposed' ? 'warn' : 'muted'}>{e.status}</Badge>
      </header>
      <p className="muted">
        {e.source ? `Proposed by ${e.source}. ` : ''}Updated {formatDateTime(e.updated)}.
        {e.status === 'proposed' ? ' Agents do not see it until you accept it.' : ''}
      </p>
      <Markdown source={e.body} maxChars={6000} />
      <div className="form-actions">
        {e.status === 'proposed' ? (
          <>
            <button className="primary" disabled={busy} onClick={onAccept}>
              Accept
            </button>
            <button disabled={busy} onClick={onReject}>
              Reject
            </button>
          </>
        ) : null}
        {e.status === 'rejected' ? (
          <button disabled={busy} onClick={onAccept}>
            Accept after all
          </button>
        ) : null}
        {confirm ? (
          <>
            <button className="danger" disabled={busy} onClick={onDelete}>
              Yes, delete
            </button>
            <button onClick={() => setConfirm(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirm(true)}>Delete</button>
        )}
      </div>
    </article>
  );
}

export default function MemoryView({ projectId }: { projectId: string }) {
  const client = useClient();
  const [status, setStatus] = useState('');
  const [query, setQuery] = useState('');
  const [found, setFound] = useState<MemoryEntry[] | undefined>();
  const q = useLive(() => client.memoryList(projectId, status), [projectId, status], (e) => e.type.startsWith('memory.'));
  const act = useAction();
  const [kind, setKind] = useState('convention');
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const list = found ?? q.data ?? [];
  const decide = (fn: (id: string) => Promise<unknown>, id: string) => async () => {
    await act.run(id, () => fn(id));
    q.reload();
    setFound(undefined);
  };
  return (
    <Page title="Memory">
      <p className="muted">
        Project memory: decisions, conventions and known issues agents read. Agents (and you) can propose entries; only an entry you accept reaches agents. Secrets are
        redacted before anything is stored.
      </p>
      <div className="form-actions">
        <label>
          Show
          <select aria-label="Show" value={status} onChange={(e) => { setStatus(e.target.value); setFound(undefined); }}>
            <option value="">all</option>
            <option value="proposed">proposals</option>
            <option value="accepted">accepted</option>
            <option value="rejected">rejected</option>
          </select>
        </label>
        <label>
          Search
          <input aria-label="Search memory" value={query} onChange={(e) => setQuery(e.target.value)} />
        </label>
        <button disabled={!query.trim()} onClick={async () => setFound(await act.run('search', () => client.memorySearch(projectId, query.trim(), status)))}>
          Search
        </button>
        {found ? <button onClick={() => { setFound(undefined); setQuery(''); }}>Clear search</button> : null}
      </div>
      <ErrorNote error={q.error ?? act.error} what="Memory" />
      {(q.data || found) && list.length === 0 ? <p className="muted">{found ? 'Nothing matches.' : 'Nothing here.'}</p> : null}
      <div className="cards">
        {list.map((e) => (
          <MemoryCard key={e.id} e={e} busy={act.busy !== ''} onAccept={decide(client.memoryAccept, e.id)} onReject={decide(client.memoryReject, e.id)} onDelete={decide(client.memoryDelete, e.id)} />
        ))}
      </div>
      <h2>Propose an entry</h2>
      <div className="form">
        <label>
          Kind
          <select aria-label="Kind" value={kind} onChange={(e) => setKind(e.target.value)}>
            {MEMORY_KINDS.map((k) => (
              <option key={k}>{k}</option>
            ))}
          </select>
        </label>
        <label>
          Title
          <input value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label>
          Body
          <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={4} />
        </label>
        <div className="form-actions">
          <button className="primary" disabled={act.busy !== '' || !title.trim() || !body.trim()} onClick={async () => { const r = await act.run('propose', () => client.memoryPropose(projectId, kind, title.trim(), body)); if (r) { setTitle(''); setBody(''); q.reload(); } }}>
            Propose
          </button>
        </div>
      </div>
    </Page>
  );
}
