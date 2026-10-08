import { useState } from 'react';
import type { Plan, ProposedTask } from '../api/extTypes';
import type { Agent } from '../api/types';
import { Markdown } from '../lib/Markdown';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { Badge, ErrorNote, Page } from '../components/common';

/** Everything a task depends on, transitively, plus the task itself. */
export function closure(keys: Iterable<string>, tasks: readonly ProposedTask[]): Set<string> {
  const by = new Map(tasks.map((t) => [t.key, t]));
  const out = new Set<string>();
  const visit = (k: string) => {
    if (out.has(k) || !by.has(k)) return;
    out.add(k);
    for (const d of by.get(k)!.dependsOn ?? []) visit(d);
  };
  for (const k of keys) visit(k);
  return out;
}

/** Remove a task and everything that depends on it, so the selection stays dependency-closed. */
export function deselect(selected: ReadonlySet<string>, key: string, tasks: readonly ProposedTask[]): Set<string> {
  const out = new Set(selected);
  out.delete(key);
  let changed = true;
  while (changed) {
    changed = false;
    for (const t of tasks) {
      if (out.has(t.key) && (t.dependsOn ?? []).some((d) => !out.has(d))) {
        out.delete(t.key);
        changed = true;
      }
    }
  }
  return out;
}

function TaskEditor({ t, onSave, onCancel }: { t: ProposedTask; onSave: (t: ProposedTask) => void; onCancel: () => void }) {
  const [title, setTitle] = useState(t.title);
  const [desc, setDesc] = useState(t.description);
  const [role, setRole] = useState(t.role);
  const [acc, setAcc] = useState((t.acceptance ?? []).join('\n'));
  return (
    <div className="form">
      <label>
        Title
        <input value={title} onChange={(e) => setTitle(e.target.value)} />
      </label>
      <label>
        Role
        <input value={role} onChange={(e) => setRole(e.target.value)} />
      </label>
      <label>
        Description
        <textarea value={desc} onChange={(e) => setDesc(e.target.value)} rows={3} />
      </label>
      <label>
        Acceptance criteria (one per line)
        <textarea value={acc} onChange={(e) => setAcc(e.target.value)} rows={3} />
      </label>
      <div className="form-actions">
        <button className="primary" onClick={() => onSave({ ...t, title, role, description: desc, acceptance: acc.split('\n').map((l) => l.trim()).filter(Boolean) })}>
          Keep my edits
        </button>
        <button onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

/** A proposed plan: the task graph, a dependency-closed selection, edits, and Accept / Reject. All text is the planner's (untrusted). */
export function PlanReview({ plan, onDone }: { plan: Plan; onDone: () => void }) {
  const client = useClient();
  const act = useAction();
  const tasks = plan.proposal?.tasks ?? [];
  const [selected, setSelected] = useState<Set<string>>(() => new Set(tasks.map((t) => t.key)));
  const [edits, setEdits] = useState<Record<string, ProposedTask>>({});
  const [editing, setEditing] = useState<string | undefined>();
  const toggle = (k: string, on: boolean) => setSelected(on ? closure([...selected, k], tasks) : deselect(selected, k, tasks));
  const proposed = plan.status === 'PROPOSED';
  const created = plan.createdTasks ?? {};
  return (
    <section className="card" data-testid="plan" data-status={plan.status}>
      <header className="card-head">
        <h3>{plan.goal}</h3>
        <Badge tone={plan.status === 'PROPOSED' ? 'warn' : plan.status === 'ACCEPTED' ? 'ok' : plan.status === 'FAILED' ? 'err' : 'muted'}>{plan.status.toLowerCase()}</Badge>
      </header>
      {plan.error ? <p className="error-note">{plan.error}</p> : null}
      {plan.proposal?.notes ? <Markdown source={plan.proposal.notes} /> : null}
      <ErrorNote error={act.error} what="Not sent" />
      <table className="table" aria-label="Proposed tasks">
        <thead>
          <tr>
            <th scope="col">Create</th>
            <th scope="col">Task</th>
            <th scope="col">Depends on</th>
            <th scope="col">Role</th>
          </tr>
        </thead>
        <tbody>
          {tasks.map((t0) => {
            const t = edits[t0.key] ?? t0;
            return (
              <tr key={t.key} data-testid="plan-task" data-key={t.key}>
                <td>
                  {proposed ? (
                    <input type="checkbox" aria-label={`Create ${t.title}`} checked={selected.has(t.key)} onChange={(e) => toggle(t.key, e.target.checked)} />
                  ) : created[t.key] ? (
                    <a href={`#/task/${encodeURIComponent(created[t.key]!)}`}>created</a>
                  ) : (
                    'no'
                  )}
                </td>
                <td>
                  <strong>{t.title}</strong> {edits[t.key] ? <Badge tone="info">edited</Badge> : null} <code>{t.key}</code>
                  <Markdown source={t.description} maxChars={3000} />
                  <ul>
                    {(t.acceptance ?? []).map((a, i) => (
                      <li key={i}>{a}</li>
                    ))}
                  </ul>
                  {proposed && editing !== t.key ? (
                    <button className="link-button" onClick={() => setEditing(t.key)}>
                      Edit {t.title}
                    </button>
                  ) : null}
                  {editing === t.key ? <TaskEditor t={t} onSave={(x) => { setEdits({ ...edits, [t.key]: x }); setEditing(undefined); }} onCancel={() => setEditing(undefined)} /> : null}
                </td>
                <td>{(t.dependsOn ?? []).join(', ') || 'none'}</td>
                <td>{t.role}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {proposed ? (
        <>
          <p className="muted">A task that depends on another cannot be created without it: selecting one selects what it needs, and clearing one clears what needs it.</p>
          <div className="form-actions">
            <button
              className="primary"
              disabled={act.busy !== '' || selected.size === 0}
              onClick={async () => {
                const used = Object.fromEntries(Object.entries(edits).filter(([k]) => selected.has(k)));
                const r = await act.run('accept', () => client.planAccept(plan.id, [...selected], used));
                if (r) onDone();
              }}
            >
              Accept {selected.size} task{selected.size === 1 ? '' : 's'}
            </button>
            <button disabled={act.busy !== ''} onClick={async () => { const r = await act.run('reject', () => client.planReject(plan.id)); if (r) onDone(); }}>
              Reject plan
            </button>
          </div>
        </>
      ) : null}
    </section>
  );
}

export default function PlanView({ projectId, agents }: { projectId: string; agents: readonly Agent[] }) {
  const client = useClient();
  const q = useLive(() => client.planList(projectId), [projectId], (e) => e.type.startsWith('workflow.') || e.type.startsWith('task.') || e.type.startsWith('plan.'), true, 300);
  const act = useAction();
  const [goal, setGoal] = useState('');
  const [reqs, setReqs] = useState('');
  const [planner, setPlanner] = useState('');
  const [sel, setSel] = useState<string | undefined>();
  const plans = [...(q.data ?? [])].sort((a, b) => b.createdAt - a.createdAt);
  const current = plans.find((p) => p.id === sel) ?? plans[0];
  return (
    <Page title="Plan">
      <p className="muted">
        Describe a goal and an agent with the pm or architect role proposes a graph of tasks. Nothing is created until you accept: choose the tasks you want, edit them if
        you like, and the rest are dropped. The planner&apos;s text is outside text and is shown inert.
      </p>
      <ErrorNote error={q.error ?? act.error} what="Plan" />
      <div className="form">
        <label>
          Goal
          <textarea value={goal} onChange={(e) => setGoal(e.target.value)} rows={3} />
        </label>
        <label>
          Requirements or constraints (optional)
          <textarea value={reqs} onChange={(e) => setReqs(e.target.value)} rows={3} />
        </label>
        <label>
          Planner agent
          <select aria-label="Planner agent" value={planner} onChange={(e) => setPlanner(e.target.value)}>
            <option value="">First agent with the pm or architect role</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.role})
              </option>
            ))}
          </select>
        </label>
        <div className="form-actions">
          <button className="primary" disabled={act.busy !== '' || !goal.trim()} onClick={async () => { const r = await act.run('propose', () => client.planPropose(projectId, goal.trim(), reqs, planner)); if (r) { setGoal(''); setReqs(''); setSel(r.planId); q.reload(); } }}>
            {act.busy === 'propose' ? 'Asking the planner...' : 'Propose a plan'}
          </button>
        </div>
      </div>
      {plans.length > 1 ? (
        <label>
          Plan
          <select aria-label="Plan" value={current?.id ?? ''} onChange={(e) => setSel(e.target.value)}>
            {plans.map((p) => (
              <option key={p.id} value={p.id}>
                {p.goal.slice(0, 60)} ({p.status.toLowerCase()})
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {current ? current.status === 'PLANNING' ? <p role="status">The planner is working on &quot;{current.goal}&quot;...</p> : <PlanReview key={current.id + current.status} plan={current} onDone={q.reload} /> : <p className="muted">No plans yet.</p>}
    </Page>
  );
}
