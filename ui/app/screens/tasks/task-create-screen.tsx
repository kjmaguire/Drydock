// New task: a title, a Markdown description and acceptance criteria (one per line). Creating opens the task page.

import { css, navigate, on, type Handle } from 'remix/component';
import { SquarePen } from 'lucide';
import type { Task } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { Empty, ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { parseAcceptance } from './tasks.ts';

const formCss = css({
  display: 'grid',
  gap: t.s4,
  maxWidth: '760px',
  padding: t.s5,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  boxShadow: t.shadowSm,
  '& input, & textarea': { width: '100%' },
  '& textarea': { resize: 'vertical' },
  '& .title-input': { fontSize: t.textLg, fontWeight: '500', height: '38px' },
});

/** `onCreated` replaces the navigation to the new task (tests). */
export function TaskCreateScreen(handle: Handle<{ onCreated?: (task: Task) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let title = '';
  let description = '';
  let acceptance = '';
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };

  const submit = async () => {
    const projectId = s.workspace.projectId;
    if (!projectId || !title.trim()) return;
    const created = await act.run('create', () => s.client.taskCreate({ projectId, title: title.trim(), description, acceptance: parseAcceptance(acceptance) }));
    if (!created) return;
    if (handle.props.onCreated) handle.props.onCreated(created);
    else void navigate(routes.task.href({ id: created.id }));
  };

  return () => {
    const projectId = s.workspace.projectId;
    return (
      <Page title="New task" icon={SquarePen} subtitle="A unit of work: assign a worker and a different reviewer, then start it">
        {!projectId && s.workspace.projects.data ? (
          <Empty title="No project yet" icon={SquarePen}>
            <p>Create or open a project first; tasks belong to a project.</p>
          </Empty>
        ) : (
          <form
            mix={[
              formCss,
              on('submit', (e) => {
                e.preventDefault();
                void submit();
              }),
            ]}
          >
            <ErrorNote error={act.error} what="Task not created" />
            <Field label="Task title">
              {(id) => (
                <input id={id} class="title-input" required autofocus placeholder="Add Multiply to the calculator" value={title} mix={on('input', (e) => set(() => (title = e.currentTarget.value)))} />
              )}
            </Field>
            <Field label="Description" hint="Markdown. What to change and why; the worker reads this.">
              {(id) => <textarea id={id} rows={7} value={description} mix={on('input', (e) => set(() => (description = e.currentTarget.value)))} />}
            </Field>
            <Field label="Acceptance criteria" hint="One per line, Given / When / Then.">
              {(id) => (
                <textarea
                  id={id}
                  rows={4}
                  placeholder="Given 3 and 4, when Multiply is called, then it returns 12"
                  value={acceptance}
                  mix={on('input', (e) => set(() => (acceptance = e.currentTarget.value)))}
                />
              )}
            </Field>
            <div mix={formActions}>
              <Button type="submit" variant="primary" disabled={act.busy !== '' || !title.trim() || !projectId}>
                Create task
              </Button>
              <a href={routes.tasks.href()} mix={buttonStyle('ghost')}>
                Cancel
              </a>
              <span mix={muted} style={{ marginLeft: 'auto', fontSize: 'var(--text-sm)' }}>
                {parseAcceptance(acceptance).length} criteria
              </span>
            </div>
          </form>
        )}
      </Page>
    );
  };
}
