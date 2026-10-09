import { useState, type ChangeEvent } from 'react';
import type { Requirement, RequirementHit, RequirementTrace } from '../api/extTypes';
import type { Task } from '../api/types';
import { Markdown } from '../lib/Markdown';
import { formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive, useTasks } from '../state/hooks';
import { Badge, ErrorNote, Page, StatusChip } from '../components/common';

/** requirement -> tasks -> criteria -> evidence. "Verified" is the core's judgement; stale links are shown as stale. */
export function TraceView({ t }: { t: RequirementTrace }) {
  const tasks = t.tasks ?? [];
  return (
    <div data-testid="trace" data-verified={t.verified}>
      <p>
        <Badge tone={t.verified ? 'ok' : 'warn'}>{t.verified ? 'verified' : 'not verified'}</Badge> {t.criteria} criteria linked, {t.tasksCompleted} of {tasks.length} task(s) completed
        {t.tasksStale > 0 ? <span className="warn-note"> {t.tasksStale} task(s) were linked to an older version of this requirement.</span> : null}
      </p>
      {tasks.length === 0 ? <p className="muted">No task is linked to this requirement.</p> : null}
      {tasks.map((tt) => (
        <div key={tt.taskId} className="gh-review" data-testid="trace-task">
          <div>
            <a href={`#/task/${encodeURIComponent(tt.taskId)}`}>{tt.title}</a> <StatusChip status={tt.status} /> <span className="muted">linked to v{tt.linkedVersion}</span>
            {tt.stale ? <Badge tone="warn">stale link</Badge> : null}{' '}
            {tt.hasTests ? <Badge tone={tt.testsPassing ? 'ok' : 'err'}>{tt.testsPassing ? 'tests passing' : 'tests failing'}</Badge> : <Badge>no test evidence</Badge>}
          </div>
          <ul>
            {(tt.criteria ?? []).map((c, i) => (
              <li key={i}>{c}</li>
            ))}
          </ul>
          <ul className="evidence">
            {(tt.evidence ?? []).map((e) => (
              <li key={e.id}>
                <Badge tone={e.passed === true ? 'ok' : e.passed === false ? 'err' : 'muted'}>{e.passed === true ? 'PASS' : e.passed === false ? 'FAIL' : 'INFO'}</Badge> <strong>{e.kind}</strong> {e.summary}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

function RequirementDetail({ r, tasks, onChanged }: { r: Requirement; tasks: Task[]; onChanged: () => void }) {
  const client = useClient();
  const act = useAction();
  const text = useLive(() => client.requirementText(r.id), [r.id, r.version], () => false);
  const versions = useLive(() => client.requirementVersions(r.id), [r.id, r.version], () => false);
  const trace = useLive(() => client.requirementTrace(r.id), [r.id, r.version], (e) => e.type.startsWith('task.') || e.type.startsWith('requirement.'));
  const quarantined = useLive(() => client.requirementQuarantined(r.id), [r.id, r.version], () => false);
  const [newText, setNewText] = useState('');
  const [taskId, setTaskId] = useState('');
  const [criteria, setCriteria] = useState('');
  const links = useLive(() => client.requirementLinks({ requirementId: r.id }), [r.id], (e) => e.type.startsWith('requirement.'));
  const done = () => { onChanged(); trace.reload(); links.reload(); versions.reload(); text.reload(); };
  return (
    <section className="card" aria-label={`Requirement ${r.title}`} data-testid="requirement-detail">
      <h3>
        {r.title} <span className="muted">v{r.version}, {r.source}, {r.chunks} passages</span>
      </h3>
      <ErrorNote error={act.error} />
      {r.quarantined > 0 ? (
        <div className="warn-note" data-testid="req-quarantine">
          {r.quarantined} passage(s) look like instructions to an agent and are kept out of search.
          {(quarantined.data ?? []).map((c) => (
            <div key={c.chunkId}>
              <Markdown source={c.body} maxChars={1500} />
              <button onClick={async () => { await act.run('release', () => client.requirementReleaseChunk(c.chunkId)); done(); quarantined.reload(); }}>Release this passage</button>
            </div>
          ))}
        </div>
      ) : null}
      <details open>
        <summary>Text (outside text, shown inert)</summary>
        <Markdown source={text.data ?? ''} maxChars={30000} />
      </details>
      <h4>Versions</h4>
      <ul>
        {(versions.data ?? []).map((v) => (
          <li key={v.version}>
            v{v.version} <span className="muted">{formatDateTime(v.created)}, {v.chunks} passages, by {v.createdBy || 'unknown'}</span>
          </li>
        ))}
      </ul>
      <h4>New version</h4>
      <div className="form">
        <label>
          Replacement text
          <textarea value={newText} onChange={(e) => setNewText(e.target.value)} rows={4} />
        </label>
        <div className="form-actions">
          <button disabled={act.busy !== '' || !newText.trim()} onClick={async () => { const x = await act.run('update', () => client.requirementUpdate(r.id, newText)); if (x) { setNewText(''); done(); } }}>
            Save as a new version
          </button>
          <button onClick={async () => { await act.run('archive', () => client.requirementArchive(r.id, !r.archived)); done(); }}>{r.archived ? 'Unarchive' : 'Archive'}</button>
        </div>
      </div>
      <h4>Trace</h4>
      {trace.data ? <TraceView t={trace.data} /> : <ErrorNote error={trace.error} />}
      <h4>Link criteria to a task</h4>
      <div className="form">
        <label>
          Task
          <select aria-label="Task to link" value={taskId} onChange={(e) => setTaskId(e.target.value)}>
            <option value="">Choose a task</option>
            {tasks.map((t) => (
              <option key={t.id} value={t.id}>
                #{t.seq} {t.title}
              </option>
            ))}
          </select>
        </label>
        <label>
          Criteria (one per line; they must appear in this requirement)
          <textarea value={criteria} onChange={(e) => setCriteria(e.target.value)} rows={3} />
        </label>
        <div className="form-actions">
          <button disabled={act.busy !== '' || !taskId || !criteria.trim()} onClick={async () => { const x = await act.run('link', () => client.requirementLink(r.id, taskId, criteria.split('\n').map((l) => l.trim()).filter(Boolean))); if (x) { setCriteria(''); done(); } }}>
            Link
          </button>
        </div>
      </div>
      <ul>
        {(links.data ?? []).map((l) => (
          <li key={l.id} data-testid="req-link">
            <a href={`#/task/${encodeURIComponent(l.taskId)}`}>{l.taskId}</a>: {l.criterion} <span className="muted">v{l.version}</span> {l.stale ? <Badge tone="warn">stale</Badge> : null}
          </li>
        ))}
      </ul>
      {(links.data ?? []).length > 0 ? (
        <button onClick={async () => { const ids = Array.from(new Set((links.data ?? []).map((l) => l.taskId))); for (const id of ids) await act.run('unlink', () => client.requirementUnlink(r.id, id)); done(); }}>Remove all links</button>
      ) : null}
    </section>
  );
}

export default function RequirementsView({ projectId }: { projectId: string }) {
  const client = useClient();
  const q = useLive(() => client.requirementList(projectId, true), [projectId], (e) => e.type.startsWith('requirement.'));
  const tasks = useTasks(projectId);
  const act = useAction();
  const [title, setTitle] = useState('');
  const [text, setText] = useState('');
  const [fileErr, setFileErr] = useState('');
  const [open, setOpen] = useState<string | undefined>();
  const [query, setQuery] = useState('');
  const [hits, setHits] = useState<RequirementHit[] | undefined>();
  const list = q.data ?? [];
  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;
    setFileErr('');
    if (f.size > 1_000_000) {
      setFileErr('That file is larger than 1 MB.');
      return;
    }
    setText(await f.text());
    if (!title.trim()) setTitle(f.name.replace(/\.[^.]+$/, ''));
  };
  const sel = list.find((r) => r.id === open);
  return (
    <Page title="Requirements">
      <p className="muted">
        Upload or paste requirements text, link its criteria to tasks, and see which tasks have passing test evidence. The text is outside text: it is stored with secrets
        redacted, shown inert, and passages that look like instructions to an agent are quarantined.
      </p>
      <ErrorNote error={q.error ?? act.error} what="Requirements" />
      {q.data && list.length === 0 ? <p className="muted">No requirements yet.</p> : null}
      <table className="table" aria-label="Requirements">
        <thead>
          <tr>
            <th scope="col">Title</th>
            <th scope="col">Version</th>
            <th scope="col">Links</th>
            <th scope="col" />
          </tr>
        </thead>
        <tbody>
          {list.map((r) => (
            <tr key={r.id} data-testid="requirement" data-title={r.title}>
              <td>
                {r.title} {r.archived ? <Badge>archived</Badge> : null} {r.quarantined > 0 ? <Badge tone="warn">{r.quarantined} quarantined</Badge> : null}
              </td>
              <td>v{r.version}</td>
              <td>
                {r.links}
                {r.staleLinks > 0 ? <Badge tone="warn">{r.staleLinks} stale</Badge> : null}
              </td>
              <td>
                <button className="link-button" onClick={() => setOpen(open === r.id ? undefined : r.id)} aria-expanded={open === r.id}>
                  {open === r.id ? 'Close' : 'Open'}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {sel ? <RequirementDetail r={sel} tasks={tasks.data ?? []} onChanged={q.reload} /> : null}
      <h2>Add requirements</h2>
      <div className="form">
        <label>
          Title
          <input value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label>
          Upload a text or Markdown file (read in this browser, then sent to the core)
          <input type="file" accept=".md,.markdown,.txt,text/plain,text/markdown" onChange={onFile} />
        </label>
        {fileErr ? <p className="error-note" role="alert">{fileErr}</p> : null}
        <label>
          Text
          <textarea value={text} onChange={(e) => setText(e.target.value)} rows={8} />
        </label>
        <div className="form-actions">
          <button className="primary" disabled={act.busy !== '' || !title.trim() || !text.trim()} onClick={async () => { const r = await act.run('add', () => client.requirementAdd(projectId, title.trim(), text)); if (r) { setTitle(''); setText(''); setOpen(r.id); q.reload(); } }}>
            Add requirement
          </button>
        </div>
      </div>
      <h2>Search requirements</h2>
      <div className="form-actions">
        <label>
          Search
          <input aria-label="Search requirements" value={query} onChange={(e) => setQuery(e.target.value)} />
        </label>
        <button disabled={!query.trim()} onClick={async () => setHits(await act.run('search', () => client.requirementSearch(projectId, query.trim(), '', 8)))}>
          Search
        </button>
      </div>
      {hits ? (
        <ol data-testid="req-hits">
          {hits.length === 0 ? <li className="muted">Nothing found.</li> : null}
          {hits.map((h) => (
            <li key={h.chunkId}>
              <code>{h.citation}</code> {h.title} <span className="muted">v{h.version}, {h.headingPath}</span>
              <Markdown source={h.text} maxChars={2000} />
            </li>
          ))}
        </ol>
      ) : null}
    </Page>
  );
}
