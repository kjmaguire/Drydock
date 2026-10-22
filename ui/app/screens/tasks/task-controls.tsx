// Assignment and control of an open task that is not running: choose a worker and a different reviewer, assign, pick a workflow
// template, start, or cancel (confirmed). While a run is in flight the task page offers only CancelTask (beside the run's
// properties), and the steer box. A closed task shows nothing here.

import { css, on, type Handle } from 'remix/component';
import { Ban, Play, UserCheck } from 'lucide';
import type { WorkflowTemplate } from '../../api/extTypes.ts';
import type { Agent, TaskDetail } from '../../api/types.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Select, type SelectOption } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { isRunActive, isTerminal } from './tasks.ts';

const boxCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& .fields': { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(190px, 1fr))', gap: t.s3, alignItems: 'end' },
  '& select': { width: '100%' },
  '& .actions': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2 },
  '& .info': { display: 'grid', gap: t.s1, padding: `${t.s2} ${t.s3}`, borderRadius: t.rMd, background: t.bgApp, fontSize: t.textSm },
  '& .info ul': { margin: '0', paddingLeft: '18px' },
});

const option = (a: Agent): SelectOption => ({ value: a.id, label: `${a.name} (${a.role})` });

export function TaskControls(handle: Handle<{ detail: TaskDetail; onChanged: () => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
  const templates = query(handle, { load: () => s.client.templateList() });
  const act = action(handle);
  let worker: string | undefined;
  let reviewer: string | undefined;
  let template: string | undefined;
  let notes: string[] = [];
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };

  const run = async (what: string, fn: () => Promise<unknown>) => {
    const r = await act.run(what, async () => {
      await fn();
      return true;
    });
    if (r) handle.props.onChanged();
  };

  return () => {
    const { detail } = handle.props;
    const task = detail.task;
    worker ??= task.assignee ?? '';
    reviewer ??= task.reviewer ?? '';
    template ??= task.template ?? '';
    if (isTerminal(task.status) || isRunActive(detail.run)) return null;
    const agents = s.project.agentList;
    const list = templates.data ?? [];
    const chosen: WorkflowTemplate | undefined = list.find((x) => x.name === template);
    const ro = s.workspace.license;
    const busy = act.busy !== '';
    const w = worker;
    const r = reviewer;
    const tpl = template;
    return (
      <div mix={boxCss} role="group" aria-label="Assignment and control">
        <ErrorNote error={act.error} />
        <ReadOnlyNote />
        {notes.length > 0 ? (
          <div role="status" mix={muted}>
            {notes.join(' ')}
          </div>
        ) : null}
        <div class="fields">
            <Field label="Worker">
              {(id) => (
                <Select id={id} label="Worker" value={w} options={[{ value: '', label: 'Choose a worker' }, ...agents.map(option)]} onChange={(v) => set(() => (worker = v))} />
              )}
            </Field>
            <Field label="Reviewer">
              {(id) => (
                <Select id={id} label="Reviewer" value={r} options={[{ value: '', label: 'Choose a reviewer' }, ...agents.map(option)]} onChange={(v) => set(() => (reviewer = v))} />
              )}
            </Field>
            <Field label="Template">
              {(id) => (
                <Select
                  id={id}
                  label="Template"
                  value={tpl}
                  options={[{ value: '', label: 'Default (worker, checks, review, your approval)' }, ...list.map((x) => ({ value: x.name, label: x.title }))]}
                  onChange={(v) => set(() => (template = v))}
                />
              )}
            </Field>
        </div>
        {chosen ? (
          <div class="info" data-testid="template-info">
            <p>{chosen.description}</p>
            <p mix={muted}>Steps: {(chosen.steps ?? []).join(' > ')}</p>
            {(chosen.roles ?? []).length > 0 ? (
              <ul>
                {(chosen.roles ?? []).map((role) => (
                  <li key={role.role}>
                    Needs an agent with role <code>{role.role}</code> ({role.required ? 'required' : 'optional'}): {role.purpose}
                  </li>
                ))}
              </ul>
            ) : null}
            {chosen.specGate ? <p mix={muted}>You will be asked to accept the spec before anything is built.</p> : null}
          </div>
        ) : null}
        <div class="actions">
          <Button icon={UserCheck} disabled={!w || !r || w === r || busy} mix={on('click', () => void run('assign', () => s.client.taskAssign(task.id, w, r)))}>
            Assign
          </Button>
          <Button
            variant="primary"
            icon={Play}
            disabled={ro.readOnly || !task.assignee || !task.reviewer || busy || w !== (task.assignee ?? '') || r !== (task.reviewer ?? '')}
            title={ro.readOnly ? ro.reason : !task.assignee || !task.reviewer ? 'Assign a worker and a reviewer first' : 'Start the task'}
            mix={on(
              'click',
              () =>
                void run('start', async () => {
                  if (!tpl) return s.client.taskStart(task.id);
                  const res = await s.client.taskStartTemplate({ taskId: task.id, template: tpl });
                  notes = res.notes ?? [];
                  return res;
                }),
            )}
          >
            Start
          </Button>
          {w && r && w === r ? <span mix={muted}>The reviewer must be a different agent from the worker.</span> : null}
          <CancelTask taskId={task.id} onChanged={handle.props.onChanged} />
        </div>
      </div>
    );
  };
}

const cancelCss = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, marginLeft: 'auto', fontSize: t.textMd });

/** Cancel the task and stop its run, asked twice ("Cancel this task and stop its run?"). */
export function CancelTask(handle: Handle<{ taskId: string; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let confirm = false;
  const set = (v: boolean) => {
    confirm = v;
    void handle.update();
  };
  return () => {
    const { taskId, onChanged } = handle.props;
    const busy = act.busy !== '';
    return (
      <div mix={cancelCss} role="group" aria-label="Cancel the task">
        <ErrorNote error={act.error} what="Could not cancel the task" />
        {confirm ? (
          <>
            <span>Cancel this task and stop its run?</span>
            <Button
              variant="danger"
              disabled={busy}
              mix={on('click', async () => {
                if ((await act.run('cancel', async () => (await s.client.taskCancel(taskId), true))) === true) {
                  confirm = false;
                  onChanged();
                }
              })}
            >
              Yes, cancel task
            </Button>
            <Button mix={on('click', () => set(false))}>Keep</Button>
          </>
        ) : (
          <Button variant="ghost" icon={Ban} mix={on('click', () => set(true))}>
            Cancel task
          </Button>
        )}
      </div>
    );
  };
}
