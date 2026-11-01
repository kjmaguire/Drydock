// One task: the agent's live run (the graphite mockup "Task", decision P97). A bar with the breadcrumb (project › GOC-43) and the
// status pill ("Building · run 3", "Paused by kyle"); the main column with the title, the description, the acceptance criteria as
// a checklist with each one's state, what needs your decision (the review, spec and merge gates) while a request waits, the
// assignment while the task is not running, the Activity (what the agents did, step by step), then the evidence, the reviewer
// verdicts, the team lead, the diff, preview, usage and routing decisions; the steer box at its foot. Beside it the properties
// panel (task-properties.tsx): status, agent, where it runs, branch, template, labels, steps, checks, this run, Open the worktree
// (O) and Hand to another agent.

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { ClipboardList, Columns2, MessagesSquare } from 'lucide';
import type { AssignPreview } from '../../api/controlClient.ts';
import type { Connection, SummaryCheck, Task, TaskDetail } from '../../api/types.ts';
import { agentName } from '../../lib/format.ts';
import { LEAD_ORIGIN_WAITS, leadStep } from '../../lib/task-origin.ts';
import { routes } from '../../routes.ts';
import { taskKey } from '../../shell/palette-items.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { OriginBadge, muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { DiffViewer } from '../dock/diff-viewer.tsx';
import { TaskDecisions } from '../shared/explain.tsx';
import { UsageTable } from '../shared/usage.tsx';
import { Interventions, leadOf } from '../team/lead.tsx';
import { TaskActivity } from './activity-panel.tsx';
import { CriteriaList } from './criteria.tsx';
import { EvidenceList, ReviewList } from './evidence.tsx';
import { PreviewControls } from './preview-controls.tsx';
import { SandboxApp } from './sandbox-app.tsx';
import { DiffView } from './raw-diff.tsx';
import { ReviewPanel } from './review-panel.tsx';
import { SteerBox } from './steer-box.tsx';
import { TaskControls } from './task-controls.tsx';
import { criteriaRows, statusDot, statusPill } from './task-page.ts';
import { TaskProperties } from './task-properties.tsx';
import { TaskTools } from './task-tools.tsx';
import { isWaitingForTask, newestDiffId } from './tasks.ts';

// the page: the work and the properties side by side, each column scrolling on its own (the bar stays on top of the work and the
// steer box at its foot); on a narrower window the properties go under the work and the page scrolls as one.
const pageCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'stretch',
  flex: '1',
  minHeight: '0',
  overflowY: 'auto',
  overflowX: 'hidden',
  background: t.bgApp,
  '@media (min-width: 1200px)': { flexWrap: 'nowrap', overflowY: 'hidden', '& > article, & > aside': { overflowY: 'auto', minHeight: '0' } },
});

// inside a project's Work view (decision P141) the page is a column between the list and the crew: the properties go under the
// work at any window width, and the page scrolls as one
const embeddedCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'stretch', flex: '1', minHeight: '0', overflowY: 'auto', overflowX: 'hidden', background: t.bgApp });

const workCss = css({
  flex: '3 1 560px',
  minWidth: '0',
  display: 'flex',
  flexDirection: 'column',
  borderRight: `1px solid ${t.border}`,
});

const barCss = css({
  position: 'sticky',
  top: '0',
  zIndex: '2',
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: t.s2,
  minHeight: 'var(--header-height)',
  padding: `${t.s2} 28px`,
  borderBottom: `1px solid ${t.border}`,
  background: t.bgApp,
  color: t.textFaint,
  '& > a.project': { color: t.textMuted, textDecoration: 'none', minHeight: '28px', display: 'inline-flex', alignItems: 'center' },
  '& > a.project:hover': { color: t.text },
  '& > .key': { fontFamily: t.mono, fontSize: t.textXs, color: t.textMuted },
  '& > .end': { marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap' },
});

const pillCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: t.s2,
  height: '24px',
  padding: '0 10px',
  borderRadius: t.rFull,
  background: t.accentSoft,
  color: t.accentText,
  fontSize: t.textXs,
  fontWeight: '500',
  whiteSpace: 'nowrap',
  '&[data-tone="warn"]': { background: t.warningSoft, color: t.warning },
  '&[data-tone="ok"]': { background: t.successSoft, color: t.success },
  '&[data-tone="err"]': { background: t.dangerSoft, color: t.danger },
  '&[data-tone="muted"]': { background: t.bgActive, color: t.textMuted },
  '& > .d': { width: '7px', height: '7px', borderRadius: '50%', background: 'currentColor', flex: 'none' },
  '&[data-tone="accent"] > .d': { background: t.accent },
});

const bodyCss = css({
  flex: '1',
  display: 'flex',
  flexDirection: 'column',
  gap: t.s5,
  padding: `22px 28px ${t.s5}`,
  minWidth: '0',
  '& > h1': { margin: '0', fontSize: t.text2xl, fontWeight: '600', letterSpacing: '-0.01em', overflowWrap: 'anywhere' },
  '& > .lead': { display: 'grid', gap: t.s3, minWidth: '0' },
});

const descCss = css({ color: t.textMuted, lineHeight: '1.6', maxWidth: '720px', overflowWrap: 'anywhere', '& p': { margin: '0' }, '& p + p': { marginTop: t.s2 } });

// under the title: the badge of a task the team lead split off, and the one sentence under it
const originCss = css({ display: 'grid', justifyItems: 'start', gap: '4px', color: t.textMuted, fontSize: t.textMd });

// the sections after the Activity, each headed like it: a quiet name and a hairline
const sectionCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& > .head': { display: 'flex', alignItems: 'center', gap: '10px', minWidth: '0', minHeight: '28px' },
  '& > .head > h2': { margin: '0', fontSize: t.textXs, fontWeight: '500', color: t.textFaint, flex: 'none' },
  '& > .head > .rule': { flex: '1', minWidth: '16px', height: '1px', background: t.border },
});

const sideCss = css({
  flex: '1 1 300px',
  minWidth: '0',
  maxWidth: '380px',
  background: t.bgSidebar,
  '& > .in': { display: 'flex', flexDirection: 'column', minHeight: '100%', padding: '18px 22px', boxSizing: 'border-box' },
  '@media (max-width: 899px)': { maxWidth: 'none' },
});

const blockedCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  borderRadius: t.rLg,
  background: t.dangerSoft,
  border: `1px solid color-mix(in srgb, ${t.danger} 30%, transparent)`,
  '& > p': { margin: '0' },
  '& > p:first-child': { fontWeight: '600' },
  '& button': { justifySelf: 'start' },
});

/** A section after the Activity, headed like it. */
function Part(handle: Handle<{ title: string; label?: string; testId?: string; actions?: RemixNode; children?: RemixNode }>) {
  return () => {
    const { title, label, testId, actions, children } = handle.props;
    return (
      <section mix={sectionCss} aria-label={label ?? title} data-testid={testId}>
        <div class="head">
          <h2>{title}</h2>
          <span class="rule" aria-hidden="true" />
          {actions}
        </div>
        {children}
      </section>
    );
  };
}

/** `embedded`: drawn in the centre of a project's Work view (decision P141) rather than as a page of its own. */
export function TaskDetailScreen(handle: Handle<{ id: string; embedded?: boolean }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
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
  // the Checks grid: the task's own row of task.summaries (an older core has none: the grid says no checks ran)
  const summary = query(handle, {
    load: async (): Promise<SummaryCheck[]> => {
      const d = detail.data;
      if (!d) return [];
      const r = await s.client.taskSummaries({ projectId: d.task.projectId, ids: [handle.props.id] }).catch(() => undefined);
      return r?.summaries.find((x) => x.taskId === handle.props.id)?.checks ?? [];
    },
    key: () => `${handle.props.id}:${detail.data ? 'y' : 'n'}`,
    when: (e) => e.taskId === handle.props.id,
    debounceMs: 300,
  });
  // where the assignee's CLI runs, as the core resolves it (the project's default applied)
  const preview = query(handle, {
    load: async (): Promise<AssignPreview | undefined> => {
      const a = detail.data?.task.assignee;
      if (!a || a.startsWith('human:')) return undefined;
      return s.client.taskAssignPreview(handle.props.id, a).catch(() => undefined);
    },
    key: () => `${handle.props.id}:${detail.data?.task.assignee ?? ''}`,
    when: (e) => e.type.startsWith('agent.') && !e.taskId,
    debounceMs: 300,
  });
  const installs = query(handle, { load: (): Promise<Connection[]> => s.client.connectionList().catch(() => []), when: (e) => e.type.startsWith('connection.') });

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
    const presence = s.project.presence.data;
    const project = s.workspace.projectList.find((p) => p.id === task.projectId);
    const checks = summary.data ?? [];
    const reload = () => detail.reload();
    const setDetail = (next?: TaskDetail) => (next ? detail.set(next) : void detail.reload());
    const setTask = (tk: Task) => detail.set({ ...d, task: { ...d.task, ...tk } });
    const name = task.assignee ? (task.assignee.startsWith('human:') ? task.assignee.slice(6) : agentName(task.assignee, agents)) : '';
    const criteria = criteriaRows(task.acceptance, d.criteria, d.acceptanceState, (id) => agentName(id, agents), d.evidence);
    const tone = statusDot(task, d.paused);
    return (
      <Page
        flush
        title={task.title}
        head={(titleId) => (
          <div mix={handle.props.embedded ? embeddedCss : pageCss}>
            <article mix={workCss} aria-labelledby={titleId}>
              <header mix={barCss}>
                <a class="project" href={routes.tasks.href()}>
                  {project?.name ?? 'Tasks'}
                </a>
                <span aria-hidden="true">›</span>
                <span class="key" data-testid="task-key">
                  {taskKey(task.seq, project?.key)}
                </span>
                <span class="end">
                  {d.roomId ? (
                    <a href={routes.room.href({ id: d.roomId })} mix={buttonStyle('ghost', 'sm')}>
                      <Icon icon={MessagesSquare} size={14} />
                      Open task channel
                    </a>
                  ) : null}
                  <span mix={pillCss} data-testid="task-status" data-tone={tone}>
                    <span class="d" aria-hidden="true" />
                    {statusPill(task, d.paused, checks)}
                  </span>
                </span>
              </header>

              <div mix={bodyCss}>
                <ErrorNote error={detail.error} what="Refresh failed" />
                <div class="lead">
                  <h1 id={titleId}>{task.title}</h1>
                  {leadStep(task.origin) === undefined ? null : (
                    <span mix={originCss} data-testid="task-origin-note">
                      <OriginBadge origin={task.origin} />
                      {task.status === 'BACKLOG' ? <span>{LEAD_ORIGIN_WAITS}</span> : null}
                    </span>
                  )}
                  <section aria-label="Description" mix={descCss}>
                    {task.description ? <Markdown source={task.description} /> : <p mix={muted}>No description.</p>}
                  </section>
                  {criteria.length ? <CriteriaList taskId={task.id} rows={criteria} onChanged={(cs) => detail.set({ ...d, criteria: cs })} /> : null}
                </div>
                {task.blockedReason ? <BlockedNote task={task} onChanged={reload} /> : null}
                <ReviewPanel detail={d} onChanged={reload} />
                <TaskControls key={task.id} detail={d} onChanged={reload} />
                <TaskActivity taskId={task.id} agents={agents} />
                <Part title="Evidence">
                  <EvidenceList evidence={d.evidence} />
                </Part>
                <Part title="Reviews">
                  <ReviewList reviews={d.reviews} agents={agents} />
                </Part>
                {leadOf(agents) ? (
                  <Part title="Team lead" label="Team lead on this task">
                    <Interventions projectId={task.projectId} taskId={task.id} />
                  </Part>
                ) : null}
                <Part
                  title="Diff"
                  testId="task-diff"
                  actions={
                    <Button size="sm" variant="ghost" icon={Columns2} mix={on('click', () => s.ui.openDock('diff'))}>
                      Open in the dock
                    </Button>
                  }
                >
                  <DiffViewer taskId={task.id} />
                  <DiffView key={task.id} taskId={task.id} diffId={newestDiffId(d.evidence)} />
                </Part>
                <PreviewControls taskId={task.id} hasWorktree={!!task.worktree} />
                <SandboxApp key={`sandbox-app-${task.id}`} taskId={task.id} />
                <Part title="Usage">
                  <ErrorNote error={usage.error} what="Could not load usage" />
                  <UsageTable rows={usage.data ?? []} />
                </Part>
                <Part title="Why these models?">
                  <TaskDecisions taskId={task.id} />
                </Part>
                <Part title="Agent tools">
                  <TaskTools key={task.id} taskId={task.id} />
                </Part>
              </div>

              <SteerBox key={task.id} detail={d} agentName={name} onChanged={setDetail} />
            </article>

            <aside mix={sideCss} aria-label="Properties">
              <div class="in">
                <TaskProperties
                  detail={d}
                  agents={agents}
                  presence={presence}
                  usage={usage.data ?? []}
                  tasks={s.project.taskList}
                  checks={checks}
                  preview={preview.data}
                  installs={installs.data ?? []}
                  you={s.workspace.info.data?.human || undefined}
                  onChanged={reload}
                  onTask={setTask}
                />
              </div>
            </aside>
          </div>
        )}
      />
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
