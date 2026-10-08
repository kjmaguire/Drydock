import { useState, type FormEvent } from 'react';
import type { Agent, Evidence, PendingRequest, ReviewRecord, Task, TaskDetail } from '../api/types';
import { Markdown } from '../lib/Markdown';
import { agentName, formatCost, formatDateTime } from '../lib/format';
import { useClient } from '../state/context';
import { useAgents, useLive, useTaskDetail, useTasks } from '../state/hooks';
import { distinctSources, SourceTag, UsageTable } from './Usage';
import { BudgetPausePanel } from './BudgetPause';
import { TaskDecisions } from './Explain';
import type { BudgetPausePayload, Criterion, SpecReviewPayload, WorkflowTemplate } from '../api/extTypes';
import { safeHref } from '../lib/Markdown';
import { useAction } from '../lib/useAction';
import { SchedulerPanel } from './Scheduler';
import { Badge, Empty, ErrorNote, Page, SourceBadge, StatusChip } from './common';
import { ReadOnlyNote, useReadOnly } from '../state/license';

// --- list + create ---

export function TaskList({ projectId, agents }: { projectId: string; agents: readonly Agent[] }) {
  const tasks = useTasks(projectId);
  const list = tasks.data ?? [];
  return (
    <Page title="Tasks" actions={<a className="btn primary" href="#/tasks/new">New task</a>}>
      <ErrorNote error={tasks.error} what="Could not load tasks" />
      {tasks.loading && !tasks.data ? <p className="muted">Loading tasks...</p> : null}
      {tasks.data && list.length === 0 ? (
        <Empty title="No tasks yet">
          <p>A task is the unit of work. Create one, assign a worker and a different reviewer, then start it.</p>
        </Empty>
      ) : null}
      {list.length > 0 ? (
        <table className="table" aria-label="Tasks">
          <thead>
            <tr>
              <th scope="col">#</th>
              <th scope="col">Title</th>
              <th scope="col">Status</th>
              <th scope="col">Worker</th>
              <th scope="col">Reviewer</th>
            </tr>
          </thead>
          <tbody>
            {list.map((t) => (
              <tr key={t.id} data-testid="task-row">
                <td>{t.seq}</td>
                <td>
                  <a href={`#/task/${encodeURIComponent(t.id)}`}>{t.title}</a>
                </td>
                <td>
                  <StatusChip status={t.status} />
                </td>
                <td>{agentName(t.assignee, agents) || <span className="muted">unassigned</span>}</td>
                <td>{agentName(t.reviewer, agents) || <span className="muted">unassigned</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      <SchedulerPanel projectId={projectId} />
    </Page>
  );
}

export function TaskCreate({ projectId }: { projectId: string }) {
  const client = useClient();
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [acceptance, setAcceptance] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const t = await client.taskCreate({
        projectId,
        title: title.trim(),
        description,
        acceptance: acceptance
          .split('\n')
          .map((s) => s.trim())
          .filter(Boolean),
      });
      window.location.hash = `#/task/${encodeURIComponent(t.id)}`;
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Page title="New task">
      <form className="form" onSubmit={submit}>
        <ErrorNote error={error} what="Task not created" />
        <label>
          Task title
          <input value={title} onChange={(e) => setTitle(e.target.value)} required />
        </label>
        <label>
          Description
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={5} />
        </label>
        <label>
          Acceptance criteria (one per line, Given / When / Then)
          <textarea value={acceptance} onChange={(e) => setAcceptance(e.target.value)} rows={3} />
        </label>
        <div className="form-actions">
          <button type="submit" className="primary" disabled={busy || !title.trim()}>
            Create task
          </button>
          <a href="#/tasks">Cancel</a>
        </div>
      </form>
    </Page>
  );
}

// --- detail ---

export function TaskDetailView({ taskId, agents }: { taskId: string; agents: readonly Agent[] }) {
  const detail = useTaskDetail(taskId);
  const d = detail.data;
  if (!d) {
    return (
      <Page title="Task">
        <ErrorNote error={detail.error} what="Could not load the task" />
        {detail.loading ? <p className="muted">Loading task...</p> : null}
      </Page>
    );
  }
  const t = d.task;
  return (
    <Page title={`#${t.seq} ${t.title}`} actions={<StatusChip status={t.status} />}>
      <ErrorNote error={detail.error} what="Refresh failed" />
      <div className="task-meta">
        <span>
          Worker: <strong>{agentName(t.assignee, agents) || 'unassigned'}</strong>
        </span>
        <span>
          Reviewer: <strong>{agentName(t.reviewer, agents) || 'unassigned'}</strong>
        </span>
        {t.branch ? (
          <span>
            Branch: <code>{t.branch}</code>
          </span>
        ) : null}
        <span>
          Attempt {t.iteration} of {t.maxIterations}
        </span>
        <span>
          Cost: <strong data-testid="task-cost">{formatCost(t.costUsd)}</strong> <CostSources taskId={t.id} costUsd={t.costUsd} />
        </span>
        {t.issueRef ? <span>Issue: {t.issueRef}</span> : null}
        {t.prRef ? <span>PR: {t.prRef}</span> : null}
        {t.acceptedBy ? <span>Accepted by {t.acceptedBy}</span> : null}
        {d.run ? (
          <span>
            Run: <code>{d.run.status}</code>
          </span>
        ) : null}
        {d.roomId ? <a href={`#/room/${encodeURIComponent(d.roomId)}`}>Open task channel</a> : null}
      </div>
      {t.blockedReason ? <p className="error-note">Blocked: {t.blockedReason}</p> : null}

      <ReviewPanel detail={d} onChanged={detail.reload} />
      <TeamBar detail={d} projectId={t.projectId} onChanged={detail.reload} />

      <h2>Description</h2>
      {t.description ? <Markdown source={t.description} /> : <p className="muted">No description.</p>}
      {t.acceptance && t.acceptance.length > 0 ? (
        <>
          <h3>Acceptance criteria</h3>
          <ul>
            {t.acceptance.map((a, i) => (
              <li key={i}>{a}</li>
            ))}
          </ul>
        </>
      ) : null}

      <h2>Evidence</h2>
      <EvidenceList evidence={d.evidence} />
      <h2>Reviews</h2>
      <ReviewList reviews={d.reviews} agents={agents} />
      <h2>Usage</h2>
      <TaskUsage taskId={t.id} />
      <h2>Why these models?</h2>
      <TaskDecisions taskId={t.id} />
      <PreviewControls taskId={t.id} hasWorktree={!!t.worktree} />
      <h2>Diff</h2>
      <DiffView taskId={t.id} hasDiff={d.evidence.some((e) => e.kind === 'diff')} />
    </Page>
  );
}

function TeamBar({ detail, projectId, onChanged }: { detail: TaskDetail; projectId: string; onChanged: () => void }) {
  const client = useClient();
  const agents = useAgents(projectId);
  const list = agents.data ?? [];
  const t = detail.task;
  const [worker, setWorker] = useState(t.assignee ?? '');
  const [reviewer, setReviewer] = useState(t.reviewer ?? '');
  const [busy, setBusy] = useState<'assign' | 'start' | 'cancel' | ''>('');
  const templates = useLive(() => client.templateList(), [], () => false);
  const [template, setTemplate] = useState(t.template ?? '');
  const [notes, setNotes] = useState<string[]>([]);
  const chosen: WorkflowTemplate | undefined = (templates.data ?? []).find((x) => x.name === template);
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const terminal = ['COMPLETED', 'FAILED', 'CANCELLED'].includes(t.status);
  const runActive = !!detail.run && !['COMPLETED', 'FAILED', 'CANCELLED'].includes(detail.run.status);

  const ro = useReadOnly();
  const act = async (what: 'assign' | 'start' | 'cancel', fn: () => Promise<unknown>) => {
    setBusy(what);
    setError(undefined);
    try {
      await fn();
      onChanged();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy('');
    }
  };
  if (terminal) return null;
  return (
    <div className="team-bar" role="group" aria-label="Assignment and control">
      <ErrorNote error={error} />
      <ReadOnlyNote />
      {notes.length > 0 ? (
        <div role="status" className="muted">
          {notes.join(' ')}
        </div>
      ) : null}
      {!runActive ? (
        <>
          <label>
            Worker
            <select aria-label="Worker" value={worker} onChange={(e) => setWorker(e.target.value)}>
              <option value="">Choose a worker</option>
              {list.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name} ({a.role})
                </option>
              ))}
            </select>
          </label>
          <label>
            Reviewer
            <select aria-label="Reviewer" value={reviewer} onChange={(e) => setReviewer(e.target.value)}>
              <option value="">Choose a reviewer</option>
              {list.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name} ({a.role})
                </option>
              ))}
            </select>
          </label>
          <button disabled={!worker || !reviewer || worker === reviewer || busy !== ''} onClick={() => act('assign', () => client.taskAssign(t.id, worker, reviewer))}>
            Assign
          </button>
          <button
            className="primary"
            disabled={ro.readOnly || !t.assignee || !t.reviewer || busy !== '' || worker !== (t.assignee ?? '') || reviewer !== (t.reviewer ?? '')}
            title={ro.readOnly ? ro.reason : !t.assignee || !t.reviewer ? 'Assign a worker and a reviewer first' : 'Start the task'}
            onClick={() =>
              act('start', async () => {
                if (!template) return client.taskStart(t.id);
                const r = await client.taskStartTemplate({ taskId: t.id, template });
                setNotes(r.notes ?? []);
                return r;
              })
            }
          >
            Start
          </button>
          <label>
            Template
            <select aria-label="Template" value={template} onChange={(e) => setTemplate(e.target.value)}>
              <option value="">Default (worker, checks, review, your approval)</option>
              {(templates.data ?? []).map((x) => (
                <option key={x.name} value={x.name}>
                  {x.title}
                </option>
              ))}
            </select>
          </label>
        </>
      ) : null}
      {chosen && !runActive ? (
        <div className="template-info span-all" data-testid="template-info">
          <p>{chosen.description}</p>
          <p className="muted">Steps: {(chosen.steps ?? []).join(' > ')}</p>
          {(chosen.roles ?? []).length > 0 ? (
            <ul>
              {(chosen.roles ?? []).map((r) => (
                <li key={r.role}>
                  Needs an agent with role <code>{r.role}</code> ({r.required ? 'required' : 'optional'}): {r.purpose}
                </li>
              ))}
            </ul>
          ) : null}
          {chosen.specGate ? <p className="muted">You will be asked to accept the spec before anything is built.</p> : null}
        </div>
      ) : null}
      {confirmCancel ? (
        <>
          <span>Cancel this task and stop its run?</span>
          <button className="danger" disabled={busy !== ''} onClick={() => act('cancel', async () => { await client.taskCancel(t.id); setConfirmCancel(false); })}>
            Yes, cancel task
          </button>
          <button onClick={() => setConfirmCancel(false)}>Keep</button>
        </>
      ) : (
        <button onClick={() => setConfirmCancel(true)}>Cancel task</button>
      )}
    </div>
  );
}

function useTaskUsage(taskId: string) {
  const client = useClient();
  return useLive(() => client.usageList({ taskId }), [taskId], (e) => e.taskId === taskId || e.type.startsWith('llm.'));
}

/** Source badges for the task's cost, taken from the usage rows the core returns (reported, estimated, or both). */
function CostSources({ taskId, costUsd }: { taskId: string; costUsd: number }) {
  const rows = useTaskUsage(taskId).data ?? [];
  if (rows.length === 0) return costUsd > 0 ? <SourceBadge source="unknown" /> : null;
  return (
    <>
      {distinctSources(rows).map((s) => (
        <SourceTag key={s} source={s} />
      ))}
    </>
  );
}

function TaskUsage({ taskId }: { taskId: string }) {
  const q = useTaskUsage(taskId);
  return (
    <>
      <ErrorNote error={q.error} what="Could not load usage" />
      <UsageTable rows={q.data ?? []} />
    </>
  );
}

// --- the human review panel: buttons bound to the request id, never to message text ---

function payloadOf(p: PendingRequest): { files: string[]; branch?: string; base?: string } {
  const v = (p.spec.payload ?? {}) as { files?: unknown; branch?: unknown; base?: unknown };
  return {
    files: Array.isArray(v.files) ? v.files.filter((f): f is string => typeof f === 'string') : [],
    branch: typeof v.branch === 'string' ? v.branch : undefined,
    base: typeof v.base === 'string' ? v.base : undefined,
  };
}

export function ReviewPanel({ detail, onChanged }: { detail: TaskDetail; onChanged: () => void }) {
  if (detail.pending.length === 0) return null;
  return (
    <section className="review-panel" aria-label="Needs your decision" data-testid="review-panel">
      <h2>Needs your decision</h2>
      {detail.pending.map((p) => (
        <PendingCard key={p.id} p={p} runId={detail.run?.id} onChanged={onChanged} />
      ))}
    </section>
  );
}

function specPayload(p: PendingRequest): SpecReviewPayload | undefined {
  const v = p.spec.payload as Partial<SpecReviewPayload> | undefined;
  return v && v.phase === 'spec' && p.spec.kind === 'review' ? (v as SpecReviewPayload) : undefined;
}

function budgetPayload(p: PendingRequest): BudgetPausePayload | undefined {
  const v = p.spec.payload as Partial<BudgetPausePayload> | undefined;
  return v && v.gate === 'budget' ? (v as BudgetPausePayload) : undefined;
}

export const criterionLine = (c: Criterion) => `Given ${c.given}, when ${c.when}, then ${c.then}`;

/**
 * The spec gate: the PM agent's spec and its Given/When/Then criteria. Nothing is built until you accept. The spec is agent
 * output (untrusted) and is shown inert. You can edit the criteria before accepting (sent as data.criteria) or send the spec
 * back with feedback. The buttons answer this request id; no text in the spec can answer it.
 */
/** `busy` is a request in flight; `readOnly` (license) turns off approving only: sending a spec back stays available. */
export function SpecGate({ payload, busy, readOnly = false, onRespond }: { payload: SpecReviewPayload; busy: boolean; readOnly?: boolean; onRespond: (decision: string, data?: unknown) => Promise<void> }) {
  const original = (payload.criteria ?? []).map(criterionLine);
  const [lines, setLines] = useState(original.join('\n'));
  const [rejecting, setRejecting] = useState(false);
  const [feedback, setFeedback] = useState('');
  const edited = lines.trim() !== original.join('\n').trim();
  return (
    <div className="spec-gate" data-testid="spec-gate">
      <h4>Spec: {payload.title}</h4>
      <p className="muted">Written by the PM agent, so treat it as a proposal. Accepting it fixes the acceptance criteria that tests are written against.</p>
      <details open>
        <summary>Spec text</summary>
        <Markdown source={payload.spec} maxChars={40000} />
      </details>
      <h4>Acceptance criteria (Given / When / Then)</h4>
      <ol data-testid="spec-criteria">
        {(payload.criteria ?? []).map((c) => (
          <li key={c.id}>
            <strong>{c.id}</strong> Given {c.given}; when {c.when}; then {c.then}
          </li>
        ))}
      </ol>
      <label>
        Edit the criteria before accepting (one per line)
        <textarea aria-label="Edit criteria" value={lines} onChange={(e) => setLines(e.target.value)} rows={Math.max(3, original.length + 1)} spellCheck={false} />
      </label>
      {rejecting ? (
        <div className="form">
          <label>
            Feedback for the PM agent
            <textarea value={feedback} onChange={(e) => setFeedback(e.target.value)} rows={3} />
          </label>
          <div className="form-actions">
            <button className="primary" disabled={busy || !feedback.trim()} onClick={() => onRespond('rejected', { feedback: feedback.trim() })}>
              Send spec back
            </button>
            <button onClick={() => setRejecting(false)}>Back</button>
          </div>
        </div>
      ) : (
        <div className="form-actions">
          <button className="primary" disabled={busy || readOnly} onClick={() => onRespond('approved', edited ? { criteria: lines.split('\n').map((l) => l.trim()).filter(Boolean) } : undefined)}>
            {edited ? 'Accept spec with my edited criteria' : 'Accept spec'}
          </button>
          <button disabled={busy} onClick={() => setRejecting(true)}>
            Reject with feedback
          </button>
        </div>
      )}
    </div>
  );
}

function PendingCard({ p, runId, onChanged }: { p: PendingRequest; runId: string | undefined; onChanged: () => void }) {
  const client = useClient();
  const [mode, setMode] = useState<'idle' | 'changes'>('idle');
  const [text, setText] = useState('');
  const [state, setState] = useState<'idle' | 'sending' | 'sent'>('idle');
  const [error, setError] = useState<Error | undefined>();
  const info = payloadOf(p);
  const spec = specPayload(p);
  const budgetPause = budgetPayload(p);
  const respond = async (decision: string, data?: unknown) => {
    if (!runId) {
      setError(new Error('This task has no run to answer.'));
      return;
    }
    setState('sending');
    setError(undefined);
    try {
      await client.runRespond(runId, p.id, decision, data);
      // The answer is accepted; the request disappears when the core's events say so (it is not assumed here).
      setState('sent');
      onChanged();
    } catch (err) {
      setError(err as Error);
      setState('idle');
    }
  };
  const ro = useReadOnly();
  // Refusing work (reject, request changes) stays available in read-only mode; approving and answering do not.
  const sending = state !== 'idle';
  const busy = sending || ro.readOnly;
  return (
    <div className="pending" data-testid="pending-request" data-request-id={p.id} data-kind={p.spec.kind}>
      <h3>{p.spec.title}</h3>
      <p className="muted">
        Request kind: <code>{p.spec.kind}</code>
      </p>
      {info.branch ? (
        <p>
          Branch <code>{info.branch}</code>
          {info.base ? (
            <>
              {' '}
              into <code>{info.base}</code>
            </>
          ) : null}
        </p>
      ) : null}
      {info.files.length > 0 ? (
        <details open>
          <summary>{info.files.length} file(s) changed</summary>
          <ul>
            {info.files.map((f) => (
              <li key={f}>
                <code>{f}</code>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      <ErrorNote error={error} what="Not sent" />
      <ReadOnlyNote />
      {state === 'sent' ? <p role="status">Answer sent. Waiting for the core to confirm...</p> : null}

      {budgetPause ? (
        <BudgetPausePanel payload={budgetPause} runId={runId} requestId={p.id} onDone={onChanged} />
      ) : spec ? (
        <SpecGate payload={spec} busy={sending} readOnly={ro.readOnly} onRespond={respond} />
      ) : p.spec.kind === 'question' ? (
        <div className="form">
          <label>
            Your answer
            <textarea value={text} onChange={(e) => setText(e.target.value)} rows={3} disabled={busy} />
          </label>
          <div className="form-actions">
            <button className="primary" disabled={busy || !text.trim()} onClick={() => respond('answered', { text })}>
              Send answer
            </button>
          </div>
        </div>
      ) : p.spec.kind === 'review' ? (
        mode === 'idle' ? (
          <div className="form-actions">
            <button className="primary" disabled={busy} onClick={() => respond('approved')}>
              Approve
            </button>
            <button disabled={sending} onClick={() => setMode('changes')}>
              Request changes
            </button>
          </div>
        ) : (
          <div className="form">
            <label>
              Feedback for the worker
              <textarea value={text} onChange={(e) => setText(e.target.value)} rows={3} disabled={sending} />
            </label>
            <div className="form-actions">
              <button className="primary" disabled={sending || !text.trim()} onClick={() => respond('rejected', { feedback: text.trim() })}>
                Send feedback
              </button>
              <button disabled={sending} onClick={() => setMode('idle')}>
                Back
              </button>
            </div>
          </div>
        )
      ) : (
        <div className="form-actions">
          <button className="primary" disabled={busy} onClick={() => respond('approved')}>
            Approve
          </button>
          <button disabled={sending} onClick={() => respond('rejected')}>
            Reject
          </button>
        </div>
      )}
    </div>
  );
}

// --- evidence, reviews, diff ---

export const EVIDENCE_LABEL: Record<string, string> = {
  spec: 'Spec written',
  spec_accept: 'Spec accepted',
  red_test: 'Test written first (red)',
  green_test: 'Tests now pass (green)',
  design: 'Design notes',
  e2e: 'End-to-end check',
  screenshot: 'Screenshot',
  preview: 'Preview',
  test: 'Tests',
  diff: 'Diff',
  commit: 'Commit',
};

export function EvidenceList({ evidence }: { evidence: Evidence[] }) {
  if (evidence.length === 0) return <p className="muted">No evidence yet. A task enters review only with evidence.</p>;
  return (
    <ul className="evidence" aria-label="Evidence">
      {evidence.map((e) => {
        const skippable = e.kind === 'e2e' || e.kind === 'preview';
        const res = e.passed === true ? 'pass' : e.passed === false ? 'fail' : skippable ? 'skipped' : 'info';
        const link = e.kind === 'preview' && e.ref ? safeHref(e.ref) : undefined;
        return (
          <li key={e.id} data-testid="evidence" data-kind={e.kind} data-result={res}>
            <span className={`chip chip-${res === 'pass' ? 'ok' : res === 'fail' ? 'err' : 'muted'}`}>
              <span aria-hidden="true">{res === 'pass' ? '✔' : res === 'fail' ? '✖' : '•'}</span> {res === 'pass' ? 'PASS' : res === 'fail' ? 'FAIL' : res === 'skipped' ? 'SKIPPED' : 'INFO'}
            </span>{' '}
            <strong>{EVIDENCE_LABEL[e.kind] ?? e.kind}</strong> <span className="muted">{e.kind} attempt {e.attempt}</span>
            <div>{e.summary}</div>
            {link ? (
              <a href={link} target="_blank" rel="noopener noreferrer" title={link}>
                {link}
              </a>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

export function ReviewList({ reviews, agents }: { reviews: ReviewRecord[]; agents: readonly Agent[] }) {
  if (reviews.length === 0) return <p className="muted">No reviewer verdict yet.</p>;
  return (
    <ul className="reviews" aria-label="Reviews">
      {reviews.map((r) => (
        <li key={r.id} data-testid="review" data-verdict={r.verdict}>
          <span className={`chip chip-${r.verdict === 'SHIP' ? 'ok' : 'warn'}`}>{r.verdict}</span> by <strong>{agentName(r.reviewer, agents)}</strong>{' '}
          <span className="muted">attempt {r.attempt}</span>
          {r.feedback ? <Markdown source={r.feedback} /> : null}
        </li>
      ))}
    </ul>
  );
}

const DIFF_LINE_CAP = 3000;

export function classifyDiffLine(l: string): 'add' | 'del' | 'hunk' | 'meta' | 'ctx' {
  if (l.startsWith('+++') || l.startsWith('---') || l.startsWith('diff ') || l.startsWith('index ') || l.startsWith('new file') || l.startsWith('deleted file')) return 'meta';
  if (l.startsWith('@@')) return 'hunk';
  if (l.startsWith('+')) return 'add';
  if (l.startsWith('-')) return 'del';
  return 'ctx';
}

export function UnifiedDiff({ text }: { text: string }) {
  const [all, setAll] = useState(false);
  const lines = text.replace(/\n$/, '').split('\n');
  const shown = all ? lines : lines.slice(0, DIFF_LINE_CAP);
  return (
    <div className="diff" role="region" aria-label="Unified diff" tabIndex={0} data-testid="diff">
      {shown.map((l, i) => (
        <div key={i} className={`diff-line diff-${classifyDiffLine(l)}`}>
          {l || ' '}
        </div>
      ))}
      {lines.length > shown.length ? (
        <button onClick={() => setAll(true)}>Show all {lines.length} lines</button>
      ) : null}
    </div>
  );
}

function DiffView({ taskId, hasDiff }: { taskId: string; hasDiff: boolean }) {
  const client = useClient();
  const [text, setText] = useState<string | undefined>();
  const [error, setError] = useState<Error | undefined>();
  const [busy, setBusy] = useState(false);
  if (!hasDiff && text === undefined) return <p className="muted">No diff has been recorded yet.</p>;
  return (
    <div>
      {text === undefined ? (
        <button
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            setError(undefined);
            try {
              setText(await client.taskDiff(taskId));
            } catch (e) {
              setError(e as Error);
            } finally {
              setBusy(false);
            }
          }}
        >
          Show diff
        </button>
      ) : text === '' ? (
        <p className="muted">The recorded diff is empty.</p>
      ) : (
        <UnifiedDiff text={text} />
      )}
      <ErrorNote error={error} what="Could not load the diff" />
    </div>
  );
}

export function TaskSummaryCard({ task }: { task: Task }) {
  return (
    <div className="task-card">
      <div>
        <a href={`#/task/${encodeURIComponent(task.id)}`}>
          #{task.seq} {task.title}
        </a>
      </div>
      <StatusChip status={task.status} />
      <div className="muted">updated {formatDateTime(task.updated)}</div>
      {task.acceptedBy ? <Badge tone="ok">accepted</Badge> : null}
    </div>
  );
}


/** Start or stop the project's preview of the task result. Starting runs the project's preview command in the task worktree. */
export function PreviewControls({ taskId, hasWorktree }: { taskId: string; hasWorktree: boolean }) {
  const client = useClient();
  const act = useAction();
  const q = useLive(() => client.previewList(), [taskId], (e) => e.type.startsWith('preview.') || e.type.startsWith('task.'));
  const ro = useReadOnly();
  const mine = (q.data ?? []).find((p) => p.taskId === taskId);
  const href = mine ? safeHref(mine.url) : undefined;
  if (!hasWorktree && !mine) return null;
  return (
    <section aria-label="Preview">
      <h2>Preview</h2>
      <ErrorNote error={act.error ?? q.error} what="Preview" />
      {mine ? (
        <p data-testid="preview-running">
          Running (pid {mine.pid}):{' '}
          {href ? (
            <a href={href} target="_blank" rel="noopener noreferrer" title={href}>
              {href}
            </a>
          ) : (
            mine.url
          )}
        </p>
      ) : (
        <p className="muted">No preview is running. Starting one runs the project&apos;s preview command in this task&apos;s worktree (set it under Project settings).</p>
      )}
      <div className="form-actions">
        {mine ? (
          <button disabled={act.busy !== ''} onClick={async () => { await act.run('stop', () => client.previewStop(taskId)); q.reload(); }}>
            Stop preview
          </button>
        ) : (
          <button disabled={act.busy !== '' || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} onClick={async () => { await act.run('start', () => client.previewStart(taskId)); q.reload(); }}>
            Start preview
          </button>
        )}
      </div>
    </section>
  );
}
