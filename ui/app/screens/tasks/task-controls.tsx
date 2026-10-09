// Assignment and control of an open task: choose a worker and a different reviewer, assign, pick a workflow template, start,
// or cancel (confirmed). While a run is in flight only cancel stays. A closed task shows nothing here.

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
import { t } from '../../ui/tokens.ts';
import { isRunActive, isTerminal } from './tasks.ts';

const boxCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '& .fields': { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(190px, 1fr))', gap: t.s3, alignItems: 'end' },
  '& select': { width: '100%' },
  '& .actions': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2 },
  '& .confirm': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, marginLeft: 'auto', fontSize: t.textMd },
  '& .info': { display: 'grid', gap: t.s1, padding: `${t.s2} ${t.s3}`, borderRadius: t.rMd, background: t.bgApp, fontSize: t.textSm },
  '& .info ul': { margin: '0', paddingLeft: '18px' },
  '& .running': { color: t.textMuted, fontSize: t.textMd },
});

const option = (a: Agent, selected: string) => (
  <option key={a.id} value={a.id} selected={a.id === selected}>
    {a.name} ({a.role})
  </option>
);

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
  let confirmCancel = false;
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
    if (isTerminal(task.status)) return null;
    const agents = s.project.agentList;
    const list = templates.data ?? [];
    const chosen: WorkflowTemplate | undefined = list.find((x) => x.name === template);
    const runActive = isRunActive(detail.run);
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
        {!runActive ? (
          <div class="fields">
            <Field label="Worker">
              {(id) => (
                <select id={id} aria-label="Worker" value={w} mix={on('change', (e) => set(() => (worker = e.currentTarget.value)))}>
                  <option value="" selected={w === ''}>
                    Choose a worker
                  </option>
                  {agents.map((a) => option(a, w))}
                </select>
              )}
            </Field>
            <Field label="Reviewer">
              {(id) => (
                <select id={id} aria-label="Reviewer" value={r} mix={on('change', (e) => set(() => (reviewer = e.currentTarget.value)))}>
                  <option value="" selected={r === ''}>
                    Choose a reviewer
                  </option>
                  {agents.map((a) => option(a, r))}
                </select>
              )}
            </Field>
            <Field label="Template">
              {(id) => (
                <select id={id} aria-label="Template" value={tpl} mix={on('change', (e) => set(() => (template = e.currentTarget.value)))}>
                  <option value="" selected={tpl === ''}>
                    Default (worker, checks, review, your approval)
                  </option>
                  {list.map((x) => (
                    <option key={x.name} value={x.name} selected={x.name === tpl}>
                      {x.title}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          </div>
        ) : (
          <p class="running">
            A run is in progress (<code>{detail.run?.status}</code>). Answer its requests above, or cancel the task.
          </p>
        )}
        {chosen && !runActive ? (
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
          {!runActive ? (
            <>
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
            </>
          ) : null}
          <div class="confirm">
            {confirmCancel ? (
              <>
                <span>Cancel this task and stop its run?</span>
                <Button
                  variant="danger"
                  disabled={busy}
                  mix={on(
                    'click',
                    () =>
                      void run('cancel', async () => {
                        await s.client.taskCancel(task.id);
                        confirmCancel = false;
                      }),
                  )}
                >
                  Yes, cancel task
                </Button>
                <Button mix={on('click', () => set(() => (confirmCancel = false)))}>Keep</Button>
              </>
            ) : (
              <Button variant="ghost" icon={Ban} mix={on('click', () => set(() => (confirmCancel = true)))}>
                Cancel task
              </Button>
            )}
          </div>
        </div>
      </div>
    );
  };
}
