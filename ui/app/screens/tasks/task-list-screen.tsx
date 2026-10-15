// Tasks: a dense, Linear-style issue list grouped by status, with status filters and a quick text filter, then the scheduler.
// The list is the project's shared task read (s.project.tasks), re-read on task events.

import { css, on, type Handle } from 'remix/component';
import { CircleDashed, ClipboardList, Plus, Search } from 'lucide';
import type { Agent, Task } from '../../api/types.ts';
import { agentName, formatCost, formatDateTime, statusLabel, statusTone } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Avatar, StatusChip } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { SchedulerPanel } from './scheduler-panel.tsx';
import { ago, FILTERS, filterCounts, groupByStatus, inFilter, matchesText, type TaskFilter } from './tasks.ts';

const FILTER_KEY = 'drydock.tasks.filter';

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

const toolbarCss = css({
  position: 'relative',
  minWidth: '0',
  '& [role="tablist"]': { paddingRight: '240px' },
  '@media (max-width: 720px)': { '& [role="tablist"]': { paddingRight: '0' } },
});

const searchCss = css({
  position: 'absolute',
  top: '4px',
  right: '0',
  display: 'flex',
  alignItems: 'center',
  '& > svg': { position: 'absolute', left: '8px', color: t.textFaint, pointerEvents: 'none' },
  '& > input': { width: '220px', height: '28px', paddingLeft: '28px', fontSize: t.textSm },
  '@media (max-width: 720px)': { position: 'static', marginTop: t.s2, '& > input': { width: '100%' } },
});

const listCss = css({
  margin: `${t.s3} 0 0`,
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
  gridTemplateColumns: '48px minmax(0, 1fr) auto 52px 84px 44px',
  alignItems: 'center',
  gap: t.s3,
  minHeight: '40px',
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
  '& .people': { display: 'flex', alignItems: 'center', justifyContent: 'flex-end' },
  '& .people > span + span': { marginLeft: '-6px' },
  '& .people .none': { color: t.textFaint, display: 'inline-flex' },
  '& .cost, & .time': { color: t.textMuted, fontSize: t.textSm, textAlign: 'right', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' },
  '& .cost.none': { color: t.textFaint },
  '@media (max-width: 720px)': { gridTemplateColumns: '40px minmax(0, 1fr) auto 52px', '& .cost, & .time': { display: 'none' } },
});

const ringCss = css({ display: 'inline-flex', borderRadius: '50%', boxShadow: `0 0 0 2px ${t.bgSurface}` });

function People(handle: Handle<{ task: Task; agents: readonly Agent[] }>) {
  return () => {
    const { task, agents } = handle.props;
    const worker = agentName(task.assignee, agents);
    const reviewer = agentName(task.reviewer, agents);
    const label = `Worker: ${worker || 'unassigned'}. Reviewer: ${reviewer || 'unassigned'}.`;
    return (
      <span class="people" title={label}>
        <span class="sr-only">{label}</span>
        {task.assignee ? (
          <span mix={ringCss}>
            <Avatar id={task.assignee} name={worker} size={22} kind="agent" />
          </span>
        ) : (
          <span class="none">
            <Icon icon={CircleDashed} size={20} />
          </span>
        )}
        {task.reviewer ? (
          <span mix={ringCss}>
            <Avatar id={task.reviewer} name={reviewer} size={22} kind="agent" />
          </span>
        ) : null}
      </span>
    );
  };
}

export function TaskListScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.project);
  let filter: TaskFilter = readFilter();
  let text = '';

  return () => {
    const q = s.project.tasks;
    const all = q.data ?? [];
    const agents = s.project.agentList;
    // tasks whose run waits for the person (an item in Needs you), whatever their status says
    const waiting = new Set((s.project.needs.data ?? []).map((i) => i.taskId).filter((id): id is string => !!id));
    const counts = filterCounts(all, waiting);
    const shown = all.filter((x) => inFilter(x, filter, waiting) && matchesText(x, text));
    const groups = groupByStatus(shown);
    const now = Date.now();
    return (
      <Page
        title="Tasks"
        icon={ClipboardList}
        subtitle={q.data ? `${all.length} task${all.length === 1 ? '' : 's'}` : undefined}
        actions={
          <a href={routes.taskNew.href()} mix={buttonStyle('primary', 'sm')}>
            <Icon icon={Plus} size={14} />
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
          <div mix={toolbarCss}>
            <label mix={searchCss}>
              <Icon icon={Search} size={14} />
              <span class="sr-only">Filter tasks</span>
              <input type="search" placeholder="Filter by title, #, branch" value={text} mix={on('input', (e) => ((text = e.currentTarget.value), void handle.update()))} />
            </label>
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
                      <Empty title="No tasks match" icon={Search}>
                        <p>Nothing in this view{text.trim() ? ` matches "${text.trim()}"` : ''}.</p>
                      </Empty>
                    ) : (
                      <ul mix={listCss} aria-label="Tasks">
                        {groups.map((g) => [
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
                              <People task={task} agents={agents} />
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
          </div>
        ) : null}
        {s.project.id ? <SchedulerPanel projectId={s.project.id} /> : null}
      </Page>
    );
  };
}
