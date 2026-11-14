// Milestones: groups of tasks with a due date and progress the core computes from the tasks' states.

import { css, on, type Handle } from 'remix/component';
import { CalendarDays, Check, Flag, Plus, Trash2 } from 'lucide';
import type { Milestone, Plan } from '../../api/extTypes.ts';
import type { Task } from '../../api/types.ts';
import { formatDateTime, plural } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, OriginBadge, StatusChip } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, spacer } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { ConfirmAction } from '../shared/confirm-action.tsx';
import { TaskTotalNotice } from '../shared/task-total.tsx';
import { allDoneOf, dueISO, matchesFilter, progressText, segments, taskGroups, unfinishedOf, zeroTime } from './milestones.ts';

const gridCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(360px, 1fr))',
  gap: t.s3,
});
const cardCss = css({
  display: 'grid',
  gap: t.s3,
  alignContent: 'start',
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& header': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    minWidth: '0',
  },
  '& h3': {
    fontSize: t.textLg,
    fontWeight: '600',
    minWidth: '0',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  '& .pct': {
    marginLeft: 'auto',
    fontSize: t.textLg,
    fontWeight: '600',
    fontVariantNumeric: 'tabular-nums',
  },
  '& .desc': { color: t.text, fontSize: t.textMd, overflowWrap: 'anywhere' },
  '& .due': {
    display: 'flex',
    alignItems: 'center',
    gap: '6px',
    color: t.textMuted,
    fontSize: t.textSm,
  },
  '& .summary': { color: t.textMuted, fontSize: t.textSm },
  '&[data-status="closed"]': { background: t.bgApp },
});
const barCss = css({
  display: 'flex',
  height: '6px',
  borderRadius: t.rFull,
  background: t.bgActive,
  overflow: 'hidden',
  '& span': { height: '100%', transition: `width ${t.med} ${t.ease}` },
});
const SEGMENT_COLOR = {
  completed: t.success,
  inProgress: t.accent,
  blocked: t.warning,
  failed: t.danger,
};
const taskList = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  overflow: 'hidden',
  '& li': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    padding: `6px ${t.s3}`,
    borderTop: `1px solid ${t.border}`,
    fontSize: t.textMd,
    minWidth: '0',
  },
  '& li:first-child': { borderTop: '0' },
  '& li a': {
    flex: '1',
    minWidth: '0',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    color: t.text,
  },
});
const addPanel = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& .groups': { display: 'flex', gap: t.s2, flexWrap: 'wrap' },
  '& ul': { margin: '0', padding: '0', listStyle: 'none', maxHeight: '240px', overflowY: 'auto', display: 'grid', gap: '2px' },
  '& li label': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, minWidth: '0' },
  '& li span': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .actions': { display: 'flex', gap: t.s2, alignItems: 'center' },
});
const footer = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  paddingTop: t.s3,
  borderTop: `1px solid ${t.border}`,
});

function MilestoneCard(handle: Handle<{ m: Milestone; tasks: Task[]; plans: Plan[]; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  // Add tasks: the open list, its filter, and the tasks ticked in it (sent in one milestone.setTasks)
  let adding = false;
  let filter = '';
  let picked = new Set<string>();
  let confirm = false;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  const run = async (label: string, fn: () => Promise<unknown>) => {
    await act.run(label, fn);
    handle.props.onChanged();
  };
  return () => {
    const { m, tasks } = handle.props;
    const ids = m.taskIds ?? [];
    const p = m.progress;
    const free = tasks.filter((x) => !ids.includes(x.id));
    const shown = free.filter((x) => matchesFilter(x, filter));
    const groups = taskGroups(free, handle.props.plans);
    const chosen = [...picked].filter((id) => free.some((x) => x.id === id));
    const unfinished = unfinishedOf(p);
    // "All done" only when nothing failed; failed tasks still close without force, under a plain "Close milestone"
    const allDone = allDoneOf(p);
    return (
      <article mix={cardCss} data-testid="milestone" data-title={m.title} data-status={m.status}>
        <header>
          <span
            style={{
              display: 'inline-flex',
              color: m.overdue ? 'var(--danger)' : 'var(--text-muted)',
            }}
          >
            <Icon icon={Flag} size={16} />
          </span>
          <h3 title={m.title}>{m.title}</h3>
          <Badge tone={m.status === 'closed' ? 'ok' : 'info'}>{m.status}</Badge>
          {m.overdue ? <Badge tone="err">overdue</Badge> : null}
          <span class="pct" aria-hidden="true">
            {p.percent}%
          </span>
        </header>
        {m.description ? <p class="desc">{m.description}</p> : null}
        <p class="due">
          <Icon icon={CalendarDays} size={13} />
          {m.dueAt && !zeroTime(m.dueAt) ? `Due ${formatDateTime(m.dueAt)}` : 'No due date'}
        </p>
        <div mix={barCss} role="img" aria-label={`${p.percent}% complete`}>
          {segments(p).map((sg) => (
            <span key={sg.key} style={{ width: `${sg.pct}%`, background: SEGMENT_COLOR[sg.key] }} />
          ))}
        </div>
        <p class="summary">{progressText(p)}</p>
        {ids.length > 0 ? (
          <ul mix={taskList} aria-label={`Tasks in ${m.title}`}>
            {ids.map((id) => {
              const tk = tasks.find((x) => x.id === id);
              return (
                <li key={id}>
                  <a href={routes.task.href({ id })}>{tk ? `${s.workspace.keyOf(tk)} ${tk.title}` : id}</a>
                  {tk ? <StatusChip status={tk.status} /> : null}
                  {tk ? <OriginBadge origin={tk.origin} /> : null}
                  <Button
                    variant="link"
                    size="sm"
                    disabled={act.busy !== ''}
                    mix={on('click', () => void run('remove', () => s.client.milestoneSetTasks(m.id, [], [id])))}
                  >
                    Remove
                  </Button>
                </li>
              );
            })}
          </ul>
        ) : null}
        <ErrorNote error={act.error} />
        {adding ? (
          <div mix={addPanel} role="group" aria-label={`Add tasks to ${m.title}`} data-testid="milestone-add">
            <input
              type="search"
              aria-label={`Filter tasks for ${m.title}`}
              placeholder="Filter by number, title or label"
              value={filter}
              mix={on('input', (e) => set(() => (filter = e.currentTarget.value)))}
            />
            {groups.length > 0 ? (
              <div class="groups">
                {groups.map((g) => (
                  <Button
                    key={g.key}
                    size="sm"
                    variant="ghost"
                    mix={on('click', () => set(() => (picked = new Set([...picked, ...g.ids]))))}
                  >
                    {g.label} ({g.ids.length})
                  </Button>
                ))}
              </div>
            ) : null}
            {free.length === 0 ? <p class="summary">Every task is in this milestone already.</p> : null}
            <ul aria-label={`Tasks to add to ${m.title}`}>
              {shown.map((x) => (
                <li key={x.id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={picked.has(x.id)}
                      mix={on('change', (e) =>
                        set(() => {
                          picked = new Set(picked);
                          if (e.currentTarget.checked) picked.add(x.id);
                          else picked.delete(x.id);
                        }),
                      )}
                    />
                    <span>{`${s.workspace.keyOf(x)} ${x.title}`}</span>
                  </label>
                </li>
              ))}
            </ul>
            <div class="actions">
              <Button
                variant="primary"
                size="sm"
                icon={Plus}
                disabled={chosen.length === 0 || act.busy !== ''}
                mix={on('click', async () => {
                  const r = await act.run('add', () => s.client.milestoneSetTasks(m.id, chosen, []));
                  if (r) set(() => ((adding = false), (picked = new Set()), (filter = '')));
                  handle.props.onChanged();
                })}
              >
                {chosen.length === 0 ? 'Add tasks' : `Add ${plural(chosen.length, 'task')}`}
              </Button>
              <Button size="sm" variant="ghost" mix={on('click', () => set(() => ((adding = false), (picked = new Set()), (filter = ''))))}>
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <div>
            <Button icon={Plus} size="sm" disabled={act.busy !== ''} mix={on('click', () => set(() => (adding = true)))}>
              Add tasks…
            </Button>
          </div>
        )}
        <div mix={footer}>
          {m.status !== 'open' ? (
            <Button size="sm" disabled={act.busy !== ''} mix={on('click', () => void run('status', () => s.client.milestoneSetStatus(m.id, 'open', false)))}>
              Reopen
            </Button>
          ) : unfinished > 0 ? (
            // the core refuses to close with unfinished tasks unless told to: the person is asked, and Yes sends force
            <ConfirmAction
              label="Close milestone"
              variant="secondary"
              yes="Close anyway"
              yesName={`Close "${m.title}" anyway`}
              keepName={`Keep "${m.title}" open`}
              question={`${plural(unfinished, 'task')} in "${m.title}" ${unfinished === 1 ? 'is' : 'are'} not finished. Close anyway? Reopen undoes it.`}
              disabled={act.busy !== ''}
              testId="milestone-close"
              onConfirm={() => run('status', () => s.client.milestoneSetStatus(m.id, 'closed', true))}
            />
          ) : (
            <Button
              size="sm"
              variant={allDone ? 'primary' : 'secondary'}
              icon={allDone ? Check : undefined}
              disabled={act.busy !== ''}
              data-testid="milestone-close"
              mix={on('click', () => void run('status', () => s.client.milestoneSetStatus(m.id, 'closed', false)))}
            >
              {allDone ? 'All done · Close milestone' : 'Close milestone'}
            </Button>
          )}
          <span mix={spacer} />
          {confirm ? (
            <>
              <Button variant="danger" size="sm" disabled={act.busy !== ''} mix={on('click', () => void run('delete', () => s.client.milestoneDelete(m.id)))}>
                Yes, delete
              </Button>
              <Button
                variant="ghost"
                size="sm"
                mix={on('click', () => {
                  confirm = false;
                  void handle.update();
                })}
              >
                Keep
              </Button>
            </>
          ) : (
            <Button
              variant="ghost"
              size="sm"
              icon={Trash2}
              mix={on('click', () => {
                confirm = true;
                void handle.update();
              })}
            >
              Delete
            </Button>
          )}
        </div>
      </article>
    );
  };
}

const formCss = css({ display: 'grid', gap: t.s4 });

export function MilestonesScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const act = action(handle);
  const pid = () => s.workspace.projectId;
  const q = query(handle, {
    load: () => s.client.milestoneList(pid()!),
    when: (e) => e.type.startsWith('milestone.') || e.type.startsWith('task.'),
    key: pid,
    enabled: () => !!pid(),
  });
  // the accepted plans, to add all tasks of one at once; a failed read only leaves that choice out
  const plans = query(handle, {
    load: () => s.client.planList(pid()!),
    when: (e) => e.type.startsWith('plan.'),
    key: pid,
    enabled: () => !!pid(),
  });
  let title = '';
  let desc = '';
  let due = '';

  const create = async () => {
    const projectId = pid();
    if (!projectId) return;
    const r = await act.run('create', () => s.client.milestoneCreate(projectId, title.trim(), desc || undefined, dueISO(due)));
    if (r) {
      title = '';
      desc = '';
      due = '';
      q.reload();
      void handle.update();
    }
  };

  return () => {
    const list = q.data ?? [];
    const tasks = s.project.taskList;
    const open = list.filter((m) => m.status === 'open').length;
    return (
      <Page title="Milestones" icon={Flag} subtitle={q.data ? `${open} open, ${list.length - open} closed` : undefined}>
        <ErrorNote error={q.error} retry={q.reload} what="Milestones" />
        <TaskTotalNotice>Older tasks cannot be added to a milestone from here, and one that is already in a milestone shows by its id.</TaskTotalNotice>
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && list.length === 0 ? (
          <div mix={card}>
            <Empty title="No milestones." icon={Flag}>
              <p>Group tasks under a milestone to follow their progress toward a date. Create one below.</p>
            </Empty>
          </div>
        ) : null}
        {list.length > 0 ? (
          <div mix={gridCss}>
            {list.map((m) => (
              <MilestoneCard key={m.id} m={m} tasks={tasks} plans={plans.data ?? []} onChanged={() => q.reload()} />
            ))}
          </div>
        ) : null}
        <Section title="New milestone">
          <ErrorNote error={act.error} what="Not created" />
          <form
            mix={[
              card,
              formCss,
              on('submit', (e) => {
                e.preventDefault();
                if (title.trim() && !act.busy) void create();
              }),
            ]}
          >
            <div mix={formGrid}>
              <Field label="Title">
                {(id) => <input id={id} value={title} mix={on('input', (e) => ((title = e.currentTarget.value), void handle.update()))} />}
              </Field>
              <Field label="Description">
                {(id) => <input id={id} value={desc} mix={on('input', (e) => ((desc = e.currentTarget.value), void handle.update()))} />}
              </Field>
              <Field label="Due date (optional)">
                {(id) => <input id={id} type="date" value={due} mix={on('input', (e) => ((due = e.currentTarget.value), void handle.update()))} />}
              </Field>
            </div>
            <div mix={formActions}>
              <Button type="submit" variant="primary" disabled={!title.trim() || act.busy !== ''}>
                Create milestone
              </Button>
            </div>
          </form>
        </Section>
      </Page>
    );
  };
}
