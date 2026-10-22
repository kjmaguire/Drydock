// My tasks (graphite, decision P97): the tasks assigned to the person (app.info's human), in every project, newest first. The
// open ones come first; each row opens the task in its own project.

import { css, on, type Handle } from 'remix/component';
import { ListTodo } from 'lucide';
import type { Task } from '../../api/types.ts';
import { statusLabel, statusTone } from '../../lib/format.ts';
import { visible } from '../../lib/visible.ts';
import { routes } from '../../routes.ts';
import { starts } from '../../state/live.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Page } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { isTerminal } from '../tasks/tasks.ts';
import { agoWords, msOf, taskKey } from './needs-you.ts';

const listCss = css({ display: 'grid', margin: '0', padding: '0', listStyle: 'none', border: `1px solid ${t.border}`, borderRadius: t.rLg, background: t.bgSurface, overflow: 'hidden' });
const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  minHeight: '44px',
  padding: '10px 16px',
  color: t.text,
  textDecoration: 'none',
  borderTop: `1px solid ${t.border}`,
  ':is(li:first-child) > &': { borderTop: '0' },
  '&:hover': { background: t.bgHover, textDecoration: 'none', color: t.text },
  '&:focus-visible': { outline: 'none', boxShadow: `inset 0 0 0 2px ${t.accent}` },
  '& .text': { display: 'flex', flexDirection: 'column', gap: '2px', flex: '1', minWidth: '0' },
  '& .title': { fontWeight: '500', overflowWrap: 'anywhere' },
  '& .sub': { color: t.textFaint, fontSize: t.textXs },
  '& .key': { color: t.textFaint, fontFamily: t.mono, fontSize: t.textXs, marginRight: '6px' },
});
const dotCss = css({ display: 'inline-block', flex: 'none', width: '8px', height: '8px', borderRadius: '50%' });

/** The person's tasks, the open ones first (each group newest first, as the core lists them). */
export function myTaskOrder(list: readonly Task[]): Task[] {
  return [...list.filter((x) => !isTerminal(x.status)), ...list.filter((x) => isTerminal(x.status))];
}

export function MyTasksScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const human = () => s.workspace.info.data?.human ?? '';
  const tasks = query(handle, {
    load: () => s.client.taskList('', [], { assignee: human() }),
    enabled: () => !!human(),
    key: () => human(),
    when: (e) => starts(e, 'task.'),
  });
  return () => {
    const list = myTaskOrder(tasks.data ?? []);
    const open = list.filter((x) => !isTerminal(x.status)).length;
    return (
      <Page title="My tasks" icon={ListTodo} subtitle={tasks.data ? `${open} open · every project's tasks that are yours` : undefined}>
        <ErrorNote error={s.workspace.info.error ?? tasks.error} what="Could not load your tasks" />
        {(tasks.loading || s.workspace.info.loading) && !tasks.data ? <Loading /> : null}
        {tasks.data && list.length === 0 ? (
          <Empty title="No task is assigned to you" icon={ListTodo}>
            <p>A task you take on yourself, or one an agent hands to you, appears here.</p>
          </Empty>
        ) : null}
        {list.length ? (
          <ul mix={listCss} aria-label="Your tasks">
            {list.map((x) => {
              const p = s.workspace.projectList.find((q) => q.id === x.projectId);
              const key = taskKey(p?.key, x.seq);
              const done = msOf(x.completed);
              return (
                <li key={x.id} data-testid="my-task" data-status={x.status}>
                  <a
                    href={routes.task.href({ id: x.id })}
                    mix={[rowCss, on('click', () => {
                      if (x.projectId && x.projectId !== s.workspace.projectId) s.workspace.selectProject(x.projectId);
                    })]}
                  >
                    <span mix={dotCss} aria-hidden="true" style={{ background: tone[statusTone(x.status)].fg }} />
                    <span class="text">
                      <span class="title">
                        {key ? <span class="key">{key}</span> : null}
                        {visible(x.title)}
                      </span>
                      <span class="sub">{[p?.name, statusLabel(x.status), done ? `done ${agoWords(done)}` : ''].filter(Boolean).join(' · ')}</span>
                    </span>
                  </a>
                </li>
              );
            })}
          </ul>
        ) : null}
      </Page>
    );
  };
}
