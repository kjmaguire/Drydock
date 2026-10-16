// Milestones: groups of tasks with a due date and progress the core computes from the tasks' states.

import { css, on, type Handle } from 'remix/component';
import { CalendarDays, Flag, Plus, Trash2 } from 'lucide';
import type { Milestone } from '../../api/extTypes.ts';
import type { Task } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, StatusChip } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, spacer } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { dueISO, progressText, segments, zeroTime } from './milestones.ts';

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
const addRow = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  '& select': { flex: '1', minWidth: '0' },
});
const footer = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  paddingTop: t.s3,
  borderTop: `1px solid ${t.border}`,
});

function MilestoneCard(handle: Handle<{ m: Milestone; tasks: Task[]; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let add = '';
  let confirm = false;
  const run = async (label: string, fn: () => Promise<unknown>) => {
    await act.run(label, fn);
    handle.props.onChanged();
  };
  return () => {
    const { m, tasks } = handle.props;
    const ids = m.taskIds ?? [];
    const p = m.progress;
    const free = tasks.filter((x) => !ids.includes(x.id));
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
                  <a href={routes.task.href({ id })}>{tk ? `#${tk.seq} ${tk.title}` : id}</a>
                  {tk ? <StatusChip status={tk.status} /> : null}
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
        <div mix={addRow}>
          <Select
            label={`Add a task to ${m.title}`}
            value={add}
            options={[{ value: '', label: 'Add a task...' }, ...free.map((x) => ({ value: x.id, label: `#${x.seq} ${x.title}` }))]}
            onChange={(v) => {
              add = v;
              void handle.update();
            }}
          />
          <Button
            icon={Plus}
            disabled={!add || act.busy !== ''}
            mix={on('click', async () => {
              const id = add;
              await act.run('add', () => s.client.milestoneSetTasks(m.id, [id], []));
              add = '';
              handle.props.onChanged();
            })}
          >
            Add
          </Button>
        </div>
        <div mix={footer}>
          <Button
            size="sm"
            disabled={act.busy !== ''}
            mix={on('click', () => void run('status', () => s.client.milestoneSetStatus(m.id, m.status === 'open' ? 'closed' : 'open', false)))}
          >
            {m.status === 'open' ? 'Close milestone' : 'Reopen'}
          </Button>
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
        <ErrorNote error={q.error} what="Milestones" />
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
              <MilestoneCard key={m.id} m={m} tasks={tasks} onChanged={() => q.reload()} />
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
