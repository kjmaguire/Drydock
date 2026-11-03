// The task page's Milestone row (X411): the milestones the task is in, each with where it stands against its date (red when
// overdue), a remove button, and "Add to a milestone" listing the project's open ones. milestone.setTasks is the change; the
// list is the project's milestone.list, read again on milestone events.

import { css, on, type Handle } from 'remix/component';
import { X } from 'lucide';
import { routes } from '../../routes.ts';
import { action, query, services } from '../../state/services.tsx';
import { IconButton } from '../../ui/button.tsx';
import { faint } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { dueText, milestonesOfTask } from '../milestones/milestones.ts';

const rowCss = css({
  display: 'grid',
  gap: '6px',
  width: '100%',
  minWidth: '0',
  '& ul': { display: 'flex', flexWrap: 'wrap', gap: '6px', margin: '0', padding: '0', listStyle: 'none' },
  '& li': { display: 'inline-flex', alignItems: 'center', gap: '4px', minWidth: '0', paddingLeft: '8px', borderRadius: t.rSm, background: t.bgActive, color: t.textMuted, fontSize: t.textXs },
  '& li a': { color: 'inherit', textDecoration: 'none', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& li[data-overdue="true"]': { color: t.danger },
  '& li button': { width: '28px', minHeight: '28px', height: '28px' },
});

export function TaskMilestones(handle: Handle<{ taskId: string; projectId: string; now?: () => number }>) {
  const s = services(handle);
  const act = action(handle);
  const list = query(handle, {
    load: () => s.client.milestoneList(handle.props.projectId),
    key: () => handle.props.projectId,
    when: (e) => e.type.startsWith('milestone.') || e.type.startsWith('task.'),
    debounceMs: 300,
  });
  const change = async (id: string, add: string[], remove: string[]) => {
    const r = await act.run('milestone', () => s.client.milestoneSetTasks(id, add, remove));
    if (r) list.reload();
  };
  return () => {
    const { taskId } = handle.props;
    const now = handle.props.now ? handle.props.now() : Date.now();
    const { in: mine, open } = milestonesOfTask(list.data ?? [], taskId);
    const busy = act.busy !== '';
    return (
      <div mix={rowCss} data-testid="task-milestones">
        {mine.length > 0 ? (
          <ul aria-label="Milestones of this task">
            {mine.map((m) => {
              const due = dueText(m, now);
              return (
                <li key={m.id} data-testid="task-milestone" data-overdue={m.overdue ? 'true' : undefined}>
                  <a href={routes.milestones.href()} title={m.title}>
                    {m.title}
                    {due ? ` · ${due}` : ''} · {m.progress.percent}%
                  </a>
                  <IconButton icon={X} size={12} label={`Take this task out of ${m.title}`} disabled={busy} mix={on('click', () => void change(m.id, [], [taskId]))} />
                </li>
              );
            })}
          </ul>
        ) : list.data ? (
          <span mix={faint}>none</span>
        ) : list.error ? (
          // a quiet line, not an alert: the panel's alerts are about the task itself
          <span mix={faint} data-testid="task-milestones-unread">
            Milestones could not be read.
          </span>
        ) : null}
        {open.length > 0 ? (
          <Select
            size="sm"
            label="Add to a milestone"
            value=""
            disabled={busy}
            options={[{ value: '', label: 'Add to a milestone' }, ...open.map((m) => ({ value: m.id, label: m.title, hint: dueText(m, now) || undefined }))]}
            onChange={(v) => {
              if (v) void change(v, [taskId], []);
            }}
          />
        ) : null}
        <ErrorNote error={act.error} />
      </div>
    );
  };
}
