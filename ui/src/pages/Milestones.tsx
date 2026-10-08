import { useState } from 'react';
import type { Milestone } from '../api/extTypes';
import { formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive, useTasks } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';

function MilestoneCard({ m, tasks, onChanged }: { m: Milestone; tasks: { id: string; seq: number; title: string }[]; onChanged: () => void }) {
  const client = useClient();
  const act = useAction();
  const [add, setAdd] = useState('');
  const [confirm, setConfirm] = useState(false);
  const ids = m.taskIds ?? [];
  const p = m.progress;
  return (
    <article className="card" data-testid="milestone" data-title={m.title}>
      <header className="card-head">
        <h3>{m.title}</h3>
        <Badge tone={m.status === 'closed' ? 'ok' : 'info'}>{m.status}</Badge>
        {m.overdue ? <Badge tone="err">overdue</Badge> : null}
      </header>
      {m.description ? <p>{m.description}</p> : null}
      <p className="muted">{m.dueAt && !m.dueAt.startsWith('0001-') ? `Due ${formatDateTime(m.dueAt)}` : 'No due date'}</p>
      <div className="meter" role="img" aria-label={`${p.percent}% complete`}>
        <div className="meter-fill meter-ok" style={{ width: `${p.percent}%` }} />
      </div>
      <p>
        {p.completed} of {p.total} completed; {p.inProgress} in progress, {p.blocked} blocked, {p.open} not started, {p.failed} failed, {p.cancelled} cancelled.
      </p>
      <ul>
        {ids.map((id) => {
          const t = tasks.find((x) => x.id === id);
          return (
            <li key={id}>
              <a href={`#/task/${encodeURIComponent(id)}`}>{t ? `#${t.seq} ${t.title}` : id}</a>{' '}
              <button className="link-button" onClick={async () => { await act.run('remove', () => client.milestoneSetTasks(m.id, [], [id])); onChanged(); }}>
                Remove
              </button>
            </li>
          );
        })}
      </ul>
      <ErrorNote error={act.error} />
      <div className="form-actions">
        <select aria-label={`Add a task to ${m.title}`} value={add} onChange={(e) => setAdd(e.target.value)}>
          <option value="">Add a task...</option>
          {tasks.filter((t) => !ids.includes(t.id)).map((t) => (
            <option key={t.id} value={t.id}>
              #{t.seq} {t.title}
            </option>
          ))}
        </select>
        <button disabled={!add} onClick={async () => { await act.run('add', () => client.milestoneSetTasks(m.id, [add], [])); setAdd(''); onChanged(); }}>
          Add
        </button>
        <button onClick={async () => { await act.run('status', () => client.milestoneSetStatus(m.id, m.status === 'open' ? 'closed' : 'open', false)); onChanged(); }}>
          {m.status === 'open' ? 'Close milestone' : 'Reopen'}
        </button>
        {confirm ? (
          <>
            <button className="danger" onClick={async () => { await act.run('delete', () => client.milestoneDelete(m.id)); onChanged(); }}>Yes, delete</button>
            <button onClick={() => setConfirm(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirm(true)}>Delete</button>
        )}
      </div>
    </article>
  );
}

export default function MilestonesView({ projectId }: { projectId: string }) {
  const client = useClient();
  const q = useLive(() => client.milestoneList(projectId), [projectId], (e) => e.type.startsWith('milestone.') || e.type.startsWith('task.'));
  const tasks = useTasks(projectId);
  const act = useAction();
  const [title, setTitle] = useState('');
  const [desc, setDesc] = useState('');
  const [due, setDue] = useState('');
  const list = q.data ?? [];
  return (
    <Page title="Milestones">
      <ErrorNote error={q.error} what="Milestones" />
      {q.data && list.length === 0 ? <p className="muted">No milestones.</p> : null}
      <div className="cards">
        {list.map((m) => (
          <MilestoneCard key={m.id} m={m} tasks={tasks.data ?? []} onChanged={q.reload} />
        ))}
      </div>
      <h2>New milestone</h2>
      <ErrorNote error={act.error} what="Not created" />
      <div className="form">
        <label>
          Title
          <input value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label>
          Description
          <input value={desc} onChange={(e) => setDesc(e.target.value)} />
        </label>
        <label>
          Due date (optional)
          <input type="date" value={due} onChange={(e) => setDue(e.target.value)} />
        </label>
        <div className="form-actions">
          <button className="primary" disabled={!title.trim() || act.busy !== ''} onClick={async () => {
            const r = await act.run('create', () => client.milestoneCreate(projectId, title.trim(), desc || undefined, due ? new Date(`${due}T00:00:00Z`).toISOString() : undefined));
            if (r) { setTitle(''); setDesc(''); setDue(''); q.reload(); }
          }}>
            Create milestone
          </button>
        </div>
      </div>
    </Page>
  );
}
