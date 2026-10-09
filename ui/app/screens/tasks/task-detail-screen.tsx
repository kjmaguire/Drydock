// One task, in the spirit of Linear's issue view: a header (number, title, status, channel), then a main column (what needs
// your decision, assignment and control, description, acceptance criteria, evidence, reviews, the diff, preview, usage and
// routing decisions) beside a properties column (worker, reviewer, template, branch, attempt, cost, links, run).

import { css, on, type Handle } from 'remix/component';
import { ClipboardList, Columns2, FileDiff, ListChecks, MessagesSquare, ScrollText, SquareTerminal, Workflow } from 'lucide';
import type { Agent, Task, TaskDetail, UsageRow } from '../../api/types.ts';
import { agentName, formatCost, formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge, SourceBadge, StatusChip, faint, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { DiffViewer } from '../dock/diff-viewer.tsx';
import { TaskDecisions } from '../shared/explain.tsx';
import { SourceTag, UsageTable, distinctSources } from '../shared/usage.tsx';
import { EvidenceList, ReviewList } from './evidence.tsx';
import { PreviewControls } from './preview-controls.tsx';
import { DiffView } from './raw-diff.tsx';
import { ReviewPanel } from './review-panel.tsx';
import { TaskControls } from './task-controls.tsx';
import { isWaitingForTask } from './tasks.ts';

const layoutCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'flex-start',
  gap: t.s6,
  '& > .main': { flex: '1 1 520px', minWidth: '0', display: 'grid', gap: t.s6 },
  '& > .side': { flex: '0 1 280px', minWidth: '240px', position: 'sticky', top: '0' },
});

const seqCss = css({ color: t.textFaint, fontWeight: '500', fontVariantNumeric: 'tabular-nums' });

const proseCss = css({ padding: `${t.s3} ${t.s4}`, border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface });

const criteriaCss = css({
  display: 'grid',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > li': { display: 'flex', alignItems: 'flex-start', gap: t.s3, padding: `10px ${t.s4}`, borderTop: `1px solid ${t.border}`, overflowWrap: 'anywhere' },
  '& > li:first-child': { borderTop: '0' },
  '& > li svg': { color: t.textFaint, marginTop: '2px' },
});

const sideCss = css({
  display: 'grid',
  gap: t.s4,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  fontSize: t.textMd,
  '& h2': { fontSize: t.textXs, fontWeight: '600', color: t.textMuted, textTransform: 'uppercase', letterSpacing: '0.04em' },
  '& dl': { display: 'grid', gridTemplateColumns: '84px 1fr', gap: `10px ${t.s3}`, margin: '0', alignItems: 'center' },
  '& dt': { color: t.textMuted, fontSize: t.textSm },
  '& dd': { margin: '0', minWidth: '0', display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '6px', overflowWrap: 'anywhere' },
  '& hr': { border: '0', borderTop: `1px solid ${t.border}`, margin: '0' },
  '& dd code': { minWidth: '0', padding: '0', background: 'transparent', fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
});

const blockedCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  borderRadius: t.rLg,
  background: t.dangerSoft,
  border: `1px solid color-mix(in srgb, ${t.danger} 30%, transparent)`,
  '& > p:first-child': { fontWeight: '600' },
  '& button': { justifySelf: 'start' },
});

const dockLinks = css({ display: 'flex', alignItems: 'center', gap: t.s1, flexWrap: 'wrap' });

export function TaskDetailScreen(handle: Handle<{ id: string }>) {
  const s = services(handle);
  watch(handle, s.project);
  const detail = query(handle, {
    load: () => s.client.taskGet(handle.props.id),
    key: () => handle.props.id,
    when: (e) => e.taskId === handle.props.id || (e.type.startsWith('approval.') && !e.taskId),
    debounceMs: 80,
  });
  const usage = query(handle, {
    load: () => s.client.usageList({ taskId: handle.props.id }),
    key: () => handle.props.id,
    when: (e) => e.taskId === handle.props.id || e.type.startsWith('llm.'),
  });

  return () => {
    const d = detail.data;
    if (!d || d.task.id !== handle.props.id) {
      return (
        <Page title="Task" icon={ClipboardList}>
          <ErrorNote error={detail.error} what="Could not load the task" />
          {detail.loading || !detail.error ? <Loading label="Loading task..." /> : null}
        </Page>
      );
    }
    const task = d.task;
    const agents = s.project.agentList;
    const reload = () => detail.reload();
    return (
      <Page
        title={
          <>
            <span mix={seqCss}>#{task.seq}</span> {task.title}
          </>
        }
        icon={ClipboardList}
        actions={
          <>
            <StatusChip status={task.status} />
            {d.roomId ? (
              <a href={routes.room.href({ id: d.roomId })} mix={buttonStyle('secondary', 'sm')}>
                <Icon icon={MessagesSquare} size={14} />
                Open task channel
              </a>
            ) : null}
          </>
        }
      >
        <ErrorNote error={detail.error} what="Refresh failed" />
        <div mix={layoutCss}>
          <div class="main">
            {task.blockedReason ? <BlockedNote task={task} onChanged={reload} /> : null}
            <ReviewPanel detail={d} onChanged={reload} />
            <TaskControls key={task.id} detail={d} onChanged={reload} />

            <Section title="Description">
              {task.description ? (
                <div mix={proseCss}>
                  <Markdown source={task.description} />
                </div>
              ) : (
                <p mix={muted}>No description.</p>
              )}
            </Section>
            {task.acceptance && task.acceptance.length > 0 ? (
              <section style={{ display: 'grid', gap: '12px' }} aria-label="Acceptance criteria">
                <h3 style={{ fontSize: 'var(--text-md)', fontWeight: '600' }}>Acceptance criteria</h3>
                <ul mix={criteriaCss}>
                  {task.acceptance.map((a, i) => (
                    <li key={i}>
                      <Icon icon={ListChecks} size={15} />
                      <span>{a}</span>
                    </li>
                  ))}
                </ul>
              </section>
            ) : null}

            <Section title="Evidence">
              <EvidenceList evidence={d.evidence} />
            </Section>
            <Section title="Reviews">
              <ReviewList reviews={d.reviews} agents={agents} />
            </Section>
            <Section
              title="Diff"
              actions={
                <div mix={dockLinks}>
                  <Button size="sm" variant="ghost" icon={Columns2} mix={on('click', () => s.ui.openDock('diff'))}>
                    Open in the dock
                  </Button>
                  <Button size="sm" variant="ghost" icon={ScrollText} mix={on('click', () => s.ui.openDock('logs'))}>
                    Logs
                  </Button>
                  <Button size="sm" variant="ghost" icon={SquareTerminal} mix={on('click', () => s.ui.openDock('terminal'))}>
                    Terminal
                  </Button>
                </div>
              }
            >
              <DiffViewer taskId={task.id} />
              <DiffView key={task.id} taskId={task.id} hasDiff={d.evidence.some((e) => e.kind === 'diff')} />
            </Section>
            <PreviewControls taskId={task.id} hasWorktree={!!task.worktree} />
            <Section title="Usage">
              <ErrorNote error={usage.error} what="Could not load usage" />
              <UsageTable rows={usage.data ?? []} />
            </Section>
            <Section title="Why these models?">
              <TaskDecisions taskId={task.id} />
            </Section>
          </div>
          <aside class="side" aria-label="Task properties">
            <Properties detail={d} agents={agents} usage={usage.data ?? []} tasks={s.project.taskList} />
          </aside>
        </div>
      </Page>
    );
  };
}

function Person(handle: Handle<{ id: string | undefined; agents: readonly Agent[] }>) {
  return () => {
    const { id, agents } = handle.props;
    if (!id) return <span mix={faint}>unassigned</span>;
    const name = agentName(id, agents);
    return (
      <>
        <Avatar id={id} name={name} size={20} />
        <strong style={{ fontWeight: '500' }}>{name}</strong>
      </>
    );
  };
}

/** The properties column. Its class task-meta is what the end-to-end suite compares before and after a reload. */
function Properties(handle: Handle<{ detail: TaskDetail; agents: readonly Agent[]; usage: readonly UsageRow[]; tasks: readonly Task[] }>) {
  return () => {
    const { detail: d, agents, usage, tasks } = handle.props;
    const task = d.task;
    const deps = (task.dependsOn ?? []).map((id) => tasks.find((x) => x.id === id) ?? { id, seq: 0, title: id });
    return (
      <div mix={sideCss} class="task-meta">
        <h2>Properties</h2>
        <dl>
          <dt>Worker</dt>
          <dd>
            <Person id={task.assignee} agents={agents} />
          </dd>
          <dt>Reviewer</dt>
          <dd>
            <Person id={task.reviewer} agents={agents} />
          </dd>
          <dt>Template</dt>
          <dd>{task.template ? <code title={task.template}>{task.template}</code> : <span mix={faint}>default</span>}</dd>
          {task.branch ? (
            <>
              <dt>Branch</dt>
              <dd>
                <code title={task.branch}>{task.branch}</code>
              </dd>
            </>
          ) : null}
          <dt>Attempt</dt>
          <dd>
            Attempt {task.iteration} of {task.maxIterations}
          </dd>
          <dt>Cost</dt>
          <dd>
            <strong data-testid="task-cost">{formatCost(task.costUsd)}</strong>
            <CostSources rows={usage} costUsd={task.costUsd} />
          </dd>
          {task.acceptedBy ? (
            <>
              <dt>Accepted</dt>
              <dd>
                <Badge tone="ok">Accepted by {task.acceptedBy}</Badge>
              </dd>
            </>
          ) : null}
        </dl>
        {task.issueRef || task.prRef || deps.length > 0 ? (
          <>
            <hr />
            <h2>Links</h2>
            <dl>
              {task.issueRef ? (
                <>
                  <dt>Issue</dt>
                  <dd>{task.issueRef}</dd>
                </>
              ) : null}
              {task.prRef ? (
                <>
                  <dt>PR</dt>
                  <dd>
                    <a href={routes.pulls.href()}>{task.prRef}</a>
                  </dd>
                </>
              ) : null}
              {deps.length > 0 ? (
                <>
                  <dt>Depends on</dt>
                  <dd>
                    {deps.map((x) => (
                      <a key={x.id} href={routes.task.href({ id: x.id })}>
                        {x.seq ? `#${x.seq} ${x.title}` : x.title}
                      </a>
                    ))}
                  </dd>
                </>
              ) : null}
            </dl>
          </>
        ) : null}
        {d.run ? (
          <>
            <hr />
            <h2>Run</h2>
            <dl>
              <dt>Run</dt>
              <dd>
                <code>{d.run.status}</code>
              </dd>
              <dt>Workflow</dt>
              <dd>
                <code>{d.run.workflow}</code>
              </dd>
            </dl>
            <a href={routes.run.href({ id: task.id })} mix={buttonStyle('secondary', 'sm')}>
              <Icon icon={Workflow} size={14} />
              Workflow and explanation
            </a>
          </>
        ) : null}
        <hr />
        <dl>
          <dt>Created</dt>
          <dd mix={faint}>{formatDateTime(task.created) || 'not recorded'}</dd>
          <dt>Updated</dt>
          <dd mix={faint}>{formatDateTime(task.updated) || 'not recorded'}</dd>
        </dl>
        <p mix={faint} style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
          <Icon icon={FileDiff} size={13} />
          {d.evidence.length} evidence item(s), {d.reviews.length} review(s)
        </p>
      </div>
    );
  };
}

/** Source badges for the task's cost, taken from the usage rows the core returns (reported, estimated, or both). */
function CostSources(handle: Handle<{ rows: readonly UsageRow[]; costUsd: number }>) {
  return () => {
    const { rows, costUsd } = handle.props;
    if (rows.length === 0) return costUsd > 0 ? <SourceBadge source="unknown" /> : null;
    return (
      <>
        {distinctSources(rows).map((src) => (
          <SourceTag key={src} source={src} />
        ))}
      </>
    );
  };
}

/** A BLOCKED task says why. When it is waiting for an earlier task, the human can let it go on (the merge is still checked and approved). */
function BlockedNote(handle: Handle<{ task: Task; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  return () => {
    const { task, onChanged } = handle.props;
    const waiting = isWaitingForTask(task);
    return (
      <div mix={blockedCss} data-testid="blocked-note">
        <p>Blocked: {task.blockedReason}</p>
        {waiting ? (
          <>
            <p mix={muted}>It continues by itself when that task is merged. Its changes are then brought up to date and checked again before anything reaches the main branch.</p>
            <ErrorNote error={act.error} what="Could not release the task" />
            <Button
              disabled={act.busy !== ''}
              mix={on('click', async () => {
                if (await act.run('release', () => s.client.taskGuardRelease(task.id))) onChanged();
              })}
            >
              Let it go on now
            </Button>
          </>
        ) : null}
      </div>
    );
  };
}
