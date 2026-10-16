// One task (Crew HQ, decision P64): the title with its status pill and the description, then the step bar (Spec → Code →
// Checks → Review → You) and, in the main column, assignment and control, the acceptance criteria, evidence, the reviewer
// verdicts, the diff, preview, usage and routing decisions. Beside it the decide card (what needs your decision, with its
// buttons), the properties (writer, reviewer, branch, cost, time, model, attempt, links, run) and the More links.

import { css, on, type Handle } from 'remix/component';
import { Check, ClipboardList, Columns2, FileDiff, Flag, ListChecks, MessagesSquare, ScrollText, SquareTerminal, Workflow } from 'lucide';
import type { Agent, PresenceState, Task, TaskDetail, UsageRow } from '../../api/types.ts';
import { agentName, formatCost, formatDateTime, presenceFor } from '../../lib/format.ts';
import { LEAD_ORIGIN_WAITS, leadStep } from '../../lib/task-origin.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge, OriginBadge, SourceBadge, StatusChip, faint, muted, presenceColor } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { DiffViewer } from '../dock/diff-viewer.tsx';
import { TaskDecisions } from '../shared/explain.tsx';
import { SourceTag, UsageTable, distinctSources } from '../shared/usage.tsx';
import { EvidenceList, ReviewList } from './evidence.tsx';
import { PreviewControls } from './preview-controls.tsx';
import { DiffView } from './raw-diff.tsx';
import { ReviewPanel } from './review-panel.tsx';
import { Interventions, leadOf } from '../team/lead.tsx';
import { stepsFor, type Step } from './steps.ts';
import { TaskControls } from './task-controls.tsx';
import { elapsed, isWaitingForTask, newestDiffId } from './tasks.ts';

const layoutCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1fr) 340px',
  gap: t.s6,
  alignItems: 'start',
  '& > .main': { minWidth: '0', display: 'grid', gap: t.s6 },
  '& > .side': { minWidth: '0', display: 'grid', gap: t.s4, position: 'sticky', top: '0' },
  // on a narrow window the decision comes first, then the work
  '@media (max-width: 1100px)': { gridTemplateColumns: 'minmax(0, 1fr)', '& > .side': { order: '-1', position: 'static' } },
});

const seqCss = css({ color: t.textFaint, fontWeight: '500', fontVariantNumeric: 'tabular-nums' });
const chipCss = css({ display: 'inline-flex', alignItems: 'center', marginLeft: '2px', '& > span': { height: '24px', padding: '0 10px', fontSize: t.textSm } });

const descCss = css({ color: t.textMuted, fontSize: t.textLg, lineHeight: '1.5', maxWidth: '80ch', '& p': { margin: '0' } });

// under the title: the badge of a task the team lead split off, and the one sentence under it
const originCss = css({ display: 'grid', justifyItems: 'start', gap: '4px', color: t.textMuted, fontSize: t.textMd });

// The step bar: five circles joined by lines. Done is a soft green tick, the current step a black disc with a soft yellow halo,
// the rest grey; the line before a current step is yellow.
const stepsCss = css({
  display: 'flex',
  alignItems: 'flex-start',
  margin: '0',
  padding: `${t.s4} ${t.s5}`,
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rXl,
  background: t.bgSurface,
  overflowX: 'auto',
  '& > li': { display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '6px', flex: 'none', width: '92px', textAlign: 'center' },
  '& > li > .c': { display: 'grid', placeItems: 'center', width: '34px', height: '34px', borderRadius: '50%', background: t.bgActive, color: t.textFaint, fontSize: t.textSm, fontWeight: '600' },
  '& > li[data-state="done"] > .c': { background: t.successSoft, color: t.success },
  '& > li[data-state="current"] > .c': { background: t.accent, color: t.onAccent, boxShadow: `0 0 0 5px ${t.warningSoft}` },
  '& > li[data-state="failed"] > .c': { background: t.dangerSoft, color: t.danger },
  '& > li > b': { fontSize: t.textSm, fontWeight: '600' },
  '& > li[data-state="todo"] > b': { color: t.textMuted, fontWeight: '500' },
  '& > li > small': { fontSize: t.textXs, color: t.textFaint, overflowWrap: 'anywhere' },
  '& > .ln': { flex: '1', minWidth: '12px', height: '3px', marginTop: '16px', borderRadius: '2px', background: t.bgActive },
  '& > .ln[data-state="done"]': { background: t.successSoft },
  '& > .ln[data-state="current"]': { background: t.warning },
});

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

const propsCss = css({
  display: 'grid',
  gap: t.s4,
  fontSize: t.textMd,
  '& h2': { fontSize: t.textXs, fontWeight: '650', color: t.textFaint, textTransform: 'uppercase', letterSpacing: '0.06em', fontFamily: t.mono },
  '& dl': { display: 'grid', gridTemplateColumns: '78px 1fr', gap: `10px ${t.s3}`, margin: '0', alignItems: 'center' },
  '& dt': { color: t.textFaint, fontSize: t.textSm },
  '& dd': { margin: '0', minWidth: '0', display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '6px', overflowWrap: 'anywhere' },
  '& hr': { border: '0', borderTop: `1px solid ${t.border}`, margin: '0' },
  '& dd code': { minWidth: '0', padding: '0', background: 'transparent', fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
});

const moreCss = css({
  display: 'grid',
  gap: t.s2,
  '& h2': { fontSize: t.textXs, fontWeight: '650', color: t.textFaint, textTransform: 'uppercase', letterSpacing: '0.06em', fontFamily: t.mono },
  '& > a, & > button': {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'flex-start',
    gap: '10px',
    width: '100%',
    minHeight: '40px',
    padding: `0 ${t.s3}`,
    border: '0',
    borderRadius: t.rLg,
    background: t.bgSidebar,
    color: t.text,
    fontSize: t.textMd,
    fontWeight: '500',
    textDecoration: 'none',
    textAlign: 'left',
  },
  '& > a:hover, & > button:hover': { background: t.bgActive, textDecoration: 'none' },
  '& svg': { color: t.textMuted },
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
    const presence = s.project.presence.data;
    const reload = () => detail.reload();
    return (
      <Page
        title={
          <>
            <span mix={seqCss}>#{task.seq}</span> {task.title}
            <span mix={chipCss}>
              <StatusChip status={task.status} />
            </span>
          </>
        }
        subtitle={
          leadStep(task.origin) === undefined ? undefined : (
            <span mix={originCss} data-testid="task-origin-note">
              <OriginBadge origin={task.origin} />
              {task.status === 'BACKLOG' ? <span>{LEAD_ORIGIN_WAITS}</span> : null}
            </span>
          )
        }
        wide
        actions={
          d.roomId ? (
            <a href={routes.room.href({ id: d.roomId })} mix={buttonStyle('secondary')}>
              <Icon icon={MessagesSquare} size={15} />
              Open task channel
            </a>
          ) : null
        }
      >
        <ErrorNote error={detail.error} what="Refresh failed" />
        <div mix={layoutCss}>
          <div class="main">
            <section aria-label="Description" mix={descCss}>
              {task.description ? <Markdown source={task.description} /> : <p mix={muted}>No description.</p>}
            </section>
            <StepBar steps={stepsFor(task, d.evidence, d.reviews, d.pending)} />
            {task.blockedReason ? <BlockedNote task={task} onChanged={reload} /> : null}
            <TaskControls key={task.id} detail={d} onChanged={reload} />
            {task.acceptance && task.acceptance.length > 0 ? (
              <Section title="Acceptance criteria" label="Acceptance criteria">
                <ul mix={criteriaCss}>
                  {task.acceptance.map((a, i) => (
                    <li key={i}>
                      <Icon icon={ListChecks} size={15} />
                      <span>{a}</span>
                    </li>
                  ))}
                </ul>
              </Section>
            ) : null}
            <Section title="Evidence">
              <EvidenceList evidence={d.evidence} />
            </Section>
            <Section title="Reviews">
              <ReviewList reviews={d.reviews} agents={agents} />
            </Section>
            {leadOf(agents) ? (
              <Section title="Team lead" label="Team lead on this task">
                <Interventions projectId={task.projectId} taskId={task.id} />
              </Section>
            ) : null}
            <Section
              title="Diff"
              actions={
                <Button size="sm" variant="ghost" icon={Columns2} mix={on('click', () => s.ui.openDock('diff'))}>
                  Open in the dock
                </Button>
              }
            >
              <DiffViewer taskId={task.id} />
              <DiffView key={task.id} taskId={task.id} diffId={newestDiffId(d.evidence)} />
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
          <aside class="side" aria-label="Decision and properties">
            <ReviewPanel detail={d} onChanged={reload} />
            <Properties detail={d} agents={agents} presence={presence} usage={usage.data ?? []} tasks={s.project.taskList} />
            <div mix={[card, moreCss]} role="group" aria-label="More">
              <h2>More</h2>
              <button type="button" mix={on('click', () => s.ui.openDock('terminal'))}>
                <Icon icon={SquareTerminal} size={16} />
                Terminal
              </button>
              <button type="button" mix={on('click', () => s.ui.openDock('logs'))}>
                <Icon icon={ScrollText} size={16} />
                Logs
              </button>
              {d.run ? (
                <a href={routes.run.href({ id: task.id })}>
                  <Icon icon={Workflow} size={16} />
                  Workflow and explanation
                </a>
              ) : null}
            </div>
          </aside>
        </div>
      </Page>
    );
  };
}

/** The step bar: where the task is on its way from the spec to your decision. The words carry the state; colour repeats it. */
function StepBar(handle: Handle<{ steps: Step[] }>) {
  return () => {
    const steps = handle.props.steps;
    const current = steps.findIndex((x) => x.state === 'current');
    return (
      <ol mix={stepsCss} aria-label="Progress" data-testid="task-steps">
        {steps.flatMap((st, i) => [
          i > 0 ? <li key={`ln-${st.name}`} class="ln" aria-hidden="true" data-state={i === current ? 'current' : steps[i - 1]!.state === 'done' ? 'done' : 'todo'} /> : null,
          <li key={st.name} data-step={st.name} data-state={st.state} aria-current={st.state === 'current' ? 'step' : undefined}>
            <span class="c" aria-hidden="true">
              {st.state === 'done' ? <Icon icon={Check} size={16} strokeWidth={2.6} /> : st.name === 'you' ? <Icon icon={Flag} size={15} strokeWidth={2.2} /> : i + 1}
            </span>
            <b>{st.label}</b>
            <small>
              {st.note || (st.state === 'done' ? 'done' : st.state === 'current' ? 'now' : st.state === 'failed' ? 'failed' : 'to do')}
              <span class="sr-only">{st.note ? `, ${st.state === 'todo' ? 'to do' : st.state}` : ''}</span>
            </small>
          </li>,
        ])}
      </ol>
    );
  };
}

function Person(handle: Handle<{ id: string | undefined; agents: readonly Agent[]; presence: readonly PresenceState[] | undefined }>) {
  return () => {
    const { id, agents, presence } = handle.props;
    if (!id) return <span mix={faint}>unassigned</span>;
    const name = agentName(id, agents);
    return (
      <>
        <Avatar id={id} name={name} size={22} kind="agent" status={presenceColor(presenceFor(id, presence))} />
        <strong style={{ fontWeight: '500' }}>{name}</strong>
      </>
    );
  };
}

/** The properties card. Its class task-meta is what the end-to-end suite compares before and after a reload. */
function Properties(handle: Handle<{ detail: TaskDetail; agents: readonly Agent[]; presence: readonly PresenceState[] | undefined; usage: readonly UsageRow[]; tasks: readonly Task[] }>) {
  return () => {
    const { detail: d, agents, presence, usage, tasks } = handle.props;
    const task = d.task;
    const deps = (task.dependsOn ?? []).map((id) => tasks.find((x) => x.id === id) ?? { id, seq: 0, title: id });
    const writer = agents.find((a) => a.id === task.assignee);
    const model = task.model || writer?.model || '';
    const time = elapsed(task);
    return (
      <div mix={[card, propsCss]} class="task-meta" role="group" aria-label="Task properties">
        <dl>
          <dt>Writer</dt>
          <dd>
            <Person id={task.assignee} agents={agents} presence={presence} />
          </dd>
          <dt>Reviewer</dt>
          <dd>
            <Person id={task.reviewer} agents={agents} presence={presence} />
          </dd>
          {task.branch ? (
            <>
              <dt>Branch</dt>
              <dd>
                <code title={task.branch}>{task.branch}</code>
              </dd>
            </>
          ) : null}
          <dt>Cost</dt>
          <dd>
            <strong data-testid="task-cost">{formatCost(task.costUsd)}</strong>
            <CostSources rows={usage} costUsd={task.costUsd} />
          </dd>
          <dt>Time</dt>
          <dd>{time || <span mix={faint}>not started</span>}</dd>
          <dt>Model</dt>
          <dd>{model ? <span>{task.effort ? `${model} · ${task.effort}` : model}</span> : <span mix={faint}>default</span>}</dd>
          <dt>Attempt</dt>
          <dd>
            {task.iteration} of {task.maxIterations}
          </dd>
          <dt>Template</dt>
          <dd>{task.template ? <code title={task.template}>{task.template}</code> : <span mix={faint}>default</span>}</dd>
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
