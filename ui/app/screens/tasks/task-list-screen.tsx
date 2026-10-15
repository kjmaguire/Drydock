// Tasks (Crew HQ, decision P64): a board of four columns, Up next, Crew working, Needs you and Merged / Done, each card a
// task with its writer and reviewer as dots, its cost and age and its status; or the dense list grouped by status with the
// status filters (Board | List, remembered). Both views share the text filter and the scheduler. Nothing is approved from
// here: a Needs-you card opens the task's page, where the decision is made. The list is the project's shared task read
// (s.project.tasks), re-read on task events.

import { css, on, type Handle } from 'remix/component';
import { CircleDashed, ClipboardList, Kanban, List, Plus, Search } from 'lucide';
import type { Agent, PresenceState, Task } from '../../api/types.ts';
import { agentName, formatCost, formatDateTime, presenceFor, statusLabel, statusTone } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Avatar, StatusChip, presenceColor } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { SchedulerPanel } from './scheduler-panel.tsx';
import { ago, BOARD_COLUMNS, boardColumns, FILTERS, filterCounts, groupByStatus, inFilter, matchesText, type BoardColumn, type TaskFilter } from './tasks.ts';

const FILTER_KEY = 'drydock.tasks.filter';
const VIEW_KEY = 'drydock.tasks.view';
type View = 'board' | 'list';

function readFilter(): TaskFilter {
  try {
    const v = localStorage.getItem(FILTER_KEY);
    return FILTERS.some((f) => f.name === v) ? (v as TaskFilter) : 'all';
  } catch {
    return 'all';
  }
}

function writeFilter(f: TaskFilter): void {
  try {
    localStorage.setItem(FILTER_KEY, f);
  } catch {
    /* a convenience only */
  }
}

function readView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === 'list' ? 'list' : 'board';
  } catch {
    return 'board';
  }
}

function writeView(v: View): void {
  try {
    localStorage.setItem(VIEW_KEY, v);
  } catch {
    /* a convenience only */
  }
}

const toolbarCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flexWrap: 'wrap',
  minWidth: '0',
});

// Board | List: a segmented control, the pressed segment a white pill on the warm grey track.
const segCss = css({
  display: 'inline-flex',
  gap: '2px',
  padding: '3px',
  borderRadius: t.rFull,
  background: t.bgSidebar,
  '& > button': {
    minHeight: '30px',
    padding: '0 13px',
    border: '0',
    borderRadius: t.rFull,
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
    fontWeight: '550',
    gap: '6px',
  },
  '& > button:hover': { background: 'transparent', color: t.text },
  '& > button[aria-pressed="true"]': { background: t.bgSurface, color: t.text, boxShadow: t.shadowSm },
  '& > button:focus-visible': { boxShadow: t.focus },
});

const searchCss = css({
  position: 'relative',
  display: 'flex',
  alignItems: 'center',
  marginLeft: 'auto',
  '& > svg': { position: 'absolute', left: '11px', color: t.textFaint, pointerEvents: 'none' },
  '& > input': { width: '240px', height: '32px', paddingLeft: '32px', fontSize: t.textSm, borderRadius: t.rFull },
  '@media (max-width: 720px)': { marginLeft: '0', width: '100%', '& > input': { width: '100%' } },
});

// --- the board ---

const boardCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(4, minmax(0, 1fr))',
  gap: t.s4,
  alignItems: 'start',
  '@media (max-width: 1100px)': { gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' },
  '@media (max-width: 640px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
});

const columnCss = css({
  display: 'grid',
  gap: '10px',
  alignContent: 'start',
  minHeight: '360px',
  padding: t.s3,
  borderRadius: t.rXl,
  background: t.bgSidebar,
  minWidth: '0',
  '& > h3': { display: 'flex', alignItems: 'center', gap: t.s2, padding: '4px 6px 2px', fontSize: t.textMd, fontWeight: '650' },
  '& > h3 > i': { width: '9px', height: '9px', borderRadius: '50%', display: 'inline-block', flex: 'none' },
  '& > h3 > em': { marginLeft: 'auto', fontStyle: 'normal', fontFamily: t.mono, fontSize: t.textXs, fontWeight: '500', color: t.textFaint },
  '& > ul': { display: 'grid', gap: '10px', margin: '0', padding: '0', listStyle: 'none' },
  '& > .empty': { margin: '0', padding: `${t.s2} 6px`, color: t.textFaint, fontSize: t.textSm },
  '&[data-column="needs"] > h3': { color: t.warning },
});

const cardCss = css({
  position: 'relative',
  display: 'grid',
  gap: '10px',
  padding: '14px',
  borderRadius: '18px',
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  minWidth: '0',
  transition: `border-color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}`,
  '&:hover, &:focus-within': { borderColor: t.borderStrong, boxShadow: t.shadowSm },
  '&[data-needs="true"]': { borderColor: t.mentionBorder, boxShadow: `0 10px 24px -14px color-mix(in srgb, ${t.mentionBorder} 60%, transparent)` },
  '& .top': { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: t.s2, fontFamily: t.mono, fontSize: t.textXs, color: t.textFaint, whiteSpace: 'nowrap' },
  '& .top .seq': { fontVariantNumeric: 'tabular-nums' },
  '& .top .when': { overflow: 'hidden', textOverflow: 'ellipsis' },
  '& h4': { fontSize: t.textMd, fontWeight: '600', lineHeight: '1.35', letterSpacing: '-0.005em', overflowWrap: 'anywhere' },
  '& h4 a': { color: t.text, textDecoration: 'none', outline: 'none' },
  // the whole card is the link's hit area; its name is only the title. The Open link stacks above it.
  '& h4 a::after': { content: '""', position: 'absolute', inset: '0', borderRadius: '18px' },
  '& h4 a:focus-visible::after': { boxShadow: t.focus },
  '& .sub': { fontFamily: t.mono, fontSize: t.textXs, color: t.textFaint, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .who': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textSm, color: t.textMuted, minWidth: '0' },
  '& .who .dots': { display: 'inline-flex', alignItems: 'center', flex: 'none' },
  '& .who .dots > span + span': { marginLeft: '-7px' },
  '& .who .none': { color: t.textFaint, display: 'inline-flex' },
  '& .who .names': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .foot': { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: t.s2, flexWrap: 'wrap' },
  '& .foot a': { position: 'relative', zIndex: '1' },
  '&[data-column="done"] h4': { color: t.textMuted, fontWeight: '550' },
});

const addCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: '13px 14px',
  borderRadius: '18px',
  border: `1.5px dashed ${t.borderStrong}`,
  color: t.textMuted,
  fontSize: t.textSm,
  fontWeight: '550',
  textDecoration: 'none',
  '&:hover': { color: t.text, borderColor: t.text, textDecoration: 'none' },
});

const ringCss = css({ display: 'inline-flex', borderRadius: '50%', boxShadow: `0 0 0 2px ${t.bgSurface}` });

/** The writer and the reviewer as dots with their presence, named in words beside them. */
function Crew(handle: Handle<{ task: Task; agents: readonly Agent[]; presence: readonly PresenceState[] | undefined }>) {
  return () => {
    const { task, agents, presence } = handle.props;
    const worker = agentName(task.assignee, agents);
    const reviewer = agentName(task.reviewer, agents);
    const label = `Worker: ${worker || 'unassigned'}. Reviewer: ${reviewer || 'unassigned'}.`;
    const words = [worker ? `${worker} writes` : '', reviewer ? `${reviewer} reviews` : ''].filter(Boolean).join(' · ') || 'unassigned';
    return (
      <span class="who" title={label}>
        <span class="sr-only">{label}</span>
        <span class="dots" aria-hidden="true">
          {task.assignee ? (
            <span mix={ringCss}>
              <Avatar id={task.assignee} name={worker} size={22} kind="agent" status={presenceColor(presenceFor(task.assignee, presence))} />
            </span>
          ) : (
            <span class="none">
              <Icon icon={CircleDashed} size={20} />
            </span>
          )}
          {task.reviewer ? (
            <span mix={ringCss}>
              <Avatar id={task.reviewer} name={reviewer} size={22} kind="agent" status={presenceColor(presenceFor(task.reviewer, presence))} />
            </span>
          ) : null}
        </span>
        <span class="names" aria-hidden="true">
          {words}
        </span>
      </span>
    );
  };
}

function Card(handle: Handle<{ task: Task; column: BoardColumn; agents: readonly Agent[]; presence: readonly PresenceState[] | undefined; now: number }>) {
  return () => {
    const { task, column, agents, presence, now } = handle.props;
    const needs = column === 'needs';
    const when = ago(task.updated, now);
    return (
      <li mix={cardCss} data-testid="task-row" data-status={task.status} data-column={column} data-needs={needs ? 'true' : undefined}>
        <div class="top">
          <span class="seq">#{task.seq}</span>
          <span class="when" title={formatDateTime(task.updated)}>
            {task.costUsd > 0 ? (
              <>
                <span title="Cost">{formatCost(task.costUsd)}</span>
                {when ? ' · ' : ''}
              </>
            ) : (
              <span class="sr-only">cost not reported</span>
            )}
            {when ? <time>{when}</time> : null}
          </span>
        </div>
        <h4>
          <a href={routes.task.href({ id: task.id })}>{task.title}</a>
        </h4>
        {task.blockedReason ? <span class="sub" title={task.blockedReason}>{task.blockedReason}</span> : task.branch ? <span class="sub" title={task.branch}>{task.branch}</span> : null}
        <Crew task={task} agents={agents} presence={presence} />
        <div class="foot">
          <StatusChip status={task.status} />
          {needs ? (
            <a href={routes.task.href({ id: task.id })} mix={buttonStyle('primary', 'sm')} aria-label={`Open #${task.seq} ${task.title}`}>
              Open
            </a>
          ) : null}
        </div>
      </li>
    );
  };
}

// --- the list ---

const listCss = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
});

const groupCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '32px',
  padding: `0 ${t.s4}`,
  background: t.bgApp,
  borderTop: `1px solid ${t.border}`,
  fontSize: t.textSm,
  fontWeight: '600',
  color: t.text,
  '&:first-child': { borderTop: '0' },
  '& .dot': { width: '8px', height: '8px', borderRadius: '50%' },
  '& .count': { color: t.textFaint, fontWeight: '500' },
});

const rowCss = css({
  position: 'relative',
  display: 'grid',
  gridTemplateColumns: '48px minmax(0, 1fr) auto auto 84px 44px',
  alignItems: 'center',
  gap: t.s3,
  minHeight: '44px',
  padding: `0 ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  fontSize: t.textMd,
  transition: `background-color ${t.fast} ${t.ease}`,
  '&:hover': { background: t.bgHover },
  '&:focus-within': { background: t.bgHover },
  '& .seq': { color: t.textFaint, fontVariantNumeric: 'tabular-nums', fontSize: t.textSm },
  '& .title': { display: 'flex', alignItems: 'baseline', gap: t.s2, minWidth: '0' },
  '& .title a': { color: t.text, fontWeight: '500', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', textDecoration: 'none', outline: 'none' },
  // the whole row is the link's hit area; the link's name is only the title
  '& .title a::after': { content: '""', position: 'absolute', inset: '0' },
  '& .title a:focus-visible::after': { boxShadow: `inset ${t.focus}`, borderRadius: t.rSm },
  '& .title .sub': { color: t.textFaint, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', minWidth: '0' },
  '& .who .names': { display: 'none' },
  '& .who .dots > span + span': { marginLeft: '-7px' },
  '& .who .none': { color: t.textFaint, display: 'inline-flex' },
  '& .cost, & .time': { color: t.textMuted, fontSize: t.textSm, textAlign: 'right', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' },
  '& .cost.none': { color: t.textFaint },
  '@media (max-width: 720px)': { gridTemplateColumns: '40px minmax(0, 1fr) auto auto', '& .cost, & .time': { display: 'none' } },
});

export function TaskListScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.project);
  let filter: TaskFilter = readFilter();
  let view: View = readView();
  let text = '';

  const setView = (v: View) => {
    view = v;
    writeView(v);
    void handle.update();
  };

  return () => {
    const q = s.project.tasks;
    const all = q.data ?? [];
    const agents = s.project.agentList;
    const presence = s.project.presence.data;
    // tasks whose run waits for the person (an item in Needs you), whatever their status says
    const waiting = new Set((s.project.needs.data ?? []).map((i) => i.taskId).filter((id): id is string => !!id));
    const counts = filterCounts(all, waiting);
    const typed = all.filter((x) => matchesText(x, text));
    const shown = view === 'list' ? typed.filter((x) => inFilter(x, filter, waiting)) : typed;
    const now = Date.now();
    const needing = counts.waiting;
    const subtitle = q.data ? `${all.length} ${all.length === 1 ? 'task' : 'tasks'}${needing ? ` · ${needing} ${needing === 1 ? 'needs' : 'need'} you` : ''}` : undefined;
    const noMatch = (
      <Empty title="No tasks match" icon={Search}>
        <p>Nothing in this view{text.trim() ? ` matches "${text.trim()}"` : ''}.</p>
      </Empty>
    );
    return (
      <Page
        title="Tasks"
        subtitle={subtitle}
        wide
        actions={
          <a href={routes.taskNew.href()} mix={buttonStyle('primary')}>
            <Icon icon={Plus} size={15} strokeWidth={2.4} />
            New task
          </a>
        }
      >
        <ErrorNote error={q.error} what="Could not load tasks" />
        {q.loading && !q.data ? <Loading label="Loading tasks..." /> : null}
        {q.data && all.length === 0 ? (
          <Empty title="No tasks yet" icon={ClipboardList}>
            <p>A task is the unit of work. Create one, assign a worker and a different reviewer, then start it.</p>
          </Empty>
        ) : null}
        {all.length > 0 ? (
          <>
            <div mix={toolbarCss}>
              <div role="group" aria-label="View" mix={segCss}>
                <button type="button" aria-pressed={view === 'board'} data-testid="task-view-board" mix={on('click', () => setView('board'))}>
                  <Icon icon={Kanban} size={14} />
                  Board
                </button>
                <button type="button" aria-pressed={view === 'list'} data-testid="task-view-list" mix={on('click', () => setView('list'))}>
                  <Icon icon={List} size={14} />
                  List
                </button>
              </div>
              <label mix={searchCss}>
                <Icon icon={Search} size={14} />
                <span class="sr-only">Filter tasks</span>
                <input type="search" placeholder="Filter by title, #, branch" value={text} mix={on('input', (e) => ((text = e.currentTarget.value), void handle.update()))} />
              </label>
            </div>
            {view === 'board' ? (
              shown.length === 0 ? (
                noMatch
              ) : (
                <div mix={boardCss} data-testid="task-board">
                  {boardColumns(shown, waiting).map((col) => {
                    const def = BOARD_COLUMNS.find((c) => c.name === col.column)!;
                    return (
                      <section key={col.column} mix={columnCss} aria-label={def.label} data-testid="board-column" data-column={col.column}>
                        <h3>
                          <i style={{ background: tone[def.tone].fg }} aria-hidden="true" />
                          {def.label}
                          <em>{col.tasks.length}</em>
                        </h3>
                        {col.tasks.length > 0 ? (
                          <ul>
                            {col.tasks.map((task) => (
                              <Card key={task.id} task={task} column={col.column} agents={agents} presence={presence} now={now} />
                            ))}
                          </ul>
                        ) : col.column === 'needs' ? (
                          <p class="empty">Nothing needs you.</p>
                        ) : col.column !== 'next' ? (
                          <p class="empty">Nothing here yet.</p>
                        ) : null}
                        {col.column === 'next' ? (
                          <a href={routes.taskNew.href()} mix={addCss}>
                            <Icon icon={Plus} size={15} />
                            Describe a task…
                          </a>
                        ) : null}
                      </section>
                    );
                  })}
                </div>
              )
            ) : (
              <Tabs
                label="Filter tasks by status"
                items={FILTERS.map((f) => ({ name: f.name, label: f.label, count: f.name === 'all' ? undefined : counts[f.name], testId: `task-filter-${f.name}` }))}
                active={filter}
                onChange={(name) => {
                  filter = name as TaskFilter;
                  writeFilter(filter);
                  void handle.update();
                }}
              >
                {() =>
                  shown.length === 0 ? (
                    noMatch
                  ) : (
                    <ul mix={listCss} aria-label="Tasks" style={{ marginTop: '12px' }}>
                      {groupByStatus(shown).map((g) => [
                        <li key={`g-${g.status}`} mix={groupCss} aria-hidden="true">
                          <span class="dot" style={{ background: tone[statusTone(g.status)].fg }} />
                          {statusLabel(g.status)}
                          <span class="count">{g.tasks.length}</span>
                        </li>,
                        ...g.tasks.map((task) => (
                          <li key={task.id} mix={rowCss} data-testid="task-row" data-status={task.status}>
                            <span class="seq">#{task.seq}</span>
                            <span class="title">
                              <a href={routes.task.href({ id: task.id })}>{task.title}</a>
                              {task.blockedReason ? <span class="sub">{task.blockedReason}</span> : task.branch ? <span class="sub">{task.branch}</span> : null}
                            </span>
                            <StatusChip status={task.status} />
                            <Crew task={task} agents={agents} presence={presence} />
                            <span class={task.costUsd > 0 ? 'cost' : 'cost none'} title="Cost">
                              {task.costUsd > 0 ? formatCost(task.costUsd) : '–'}
                              {task.costUsd > 0 ? null : <span class="sr-only">cost not reported</span>}
                            </span>
                            <time class="time" title={formatDateTime(task.updated)}>
                              {ago(task.updated, now)}
                            </time>
                          </li>
                        )),
                      ])}
                    </ul>
                  )
                }
              </Tabs>
            )}
          </>
        ) : null}
        {s.project.id ? <SchedulerPanel projectId={s.project.id} /> : null}
      </Page>
    );
  };
}
