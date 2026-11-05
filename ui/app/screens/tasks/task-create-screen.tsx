// New task: a title, a Markdown description and acceptance criteria (one per line). Creating opens the task page. The crew pair
// is chosen for you as Home chooses it (pickPair: a builder and a different reviewer, never the lead), shown as a chip with Change:
// "Create and start" makes, assigns and starts the task in one core call (task.create with assignee, reviewer and start; the
// person's click is the start), "Create only" makes it unassigned and waiting (its page preselects the same pair). Enter in the
// form is "Create only": paid work starts only on the named button.

import { css, navigate, on, type Handle } from 'remix/component';
import { SquarePen } from 'lucide';
import { projectHasRepo, type Agent, type Task, type TaskCreateResult } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { Empty, ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Page } from '../../ui/layout.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { pickPair } from '../workspace/start.ts';
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
  '& .crew': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textMd },
  '& .crew-chip': { display: 'inline-flex', alignItems: 'center', gap: t.s2, padding: `4px ${t.s2}`, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp },
  '& .crew-fields': { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(190px, 1fr))', gap: t.s3 },
});

/** The pair a new task starts with: the person's choice when they changed it, else the one chosen for them (pickPair). */
export function crewPair(agents: readonly Agent[], worker: string | undefined, reviewer: string | undefined): { worker: string; reviewer: string } {
  const pair = pickPair(agents);
  return { worker: worker ?? pair?.worker ?? '', reviewer: reviewer ?? pair?.reviewer ?? '' };
}

/** What keeps "Create and start" from starting, in words; undefined when it can. */
export function startBlock(o: { worker: string; reviewer: string; hasRepo: boolean; readOnly: boolean; reason?: string }): string | undefined {
  if (o.readOnly) return o.reason || 'Read-only: nothing can be started.';
  if (!o.hasRepo) return 'This project has no repository yet: attach one in its settings to start tasks.';
  if (!o.worker || !o.reviewer) return 'Choose a worker and a reviewer.';
  if (o.worker === o.reviewer) return 'The reviewer must be a different agent from the worker.';
  return undefined;
}

/** `onCreated` replaces the navigation to the new task (tests). */
export function TaskCreateScreen(handle: Handle<{ onCreated?: (task: Task) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const act = action(handle);
  let title = '';
  let description = '';
  let acceptance = '';
  // the person's own choice of the pair (Change); undefined keeps the one chosen for them
  let worker: string | undefined;
  let reviewer: string | undefined;
  let changing = false;
  // a task made whose start then failed (task.create's startError)
  let made: TaskCreateResult | undefined;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };

  const submit = async (start: boolean) => {
    const projectId = s.workspace.projectId;
    if (!projectId || !title.trim()) return;
    made = undefined;
    const pair = crewPair(s.project.agentList, worker, reviewer);
    // Create only makes the task as it always did, unassigned: its page then has the same pair preselected for one Start
    const crew = start && pair.worker && pair.reviewer && pair.worker !== pair.reviewer ? { assignee: pair.worker, reviewer: pair.reviewer, start: true } : {};
    const created = await act.run(start ? 'start' : 'create', () =>
      s.client.taskCreate({ projectId, title: title.trim(), description, acceptance: parseAcceptance(acceptance), ...crew }),
    );
    if (!created) return;
    if (created.startError) {
      // the task exists but did not start: say so with a link to it, and clear the form so it cannot be made a second time
      made = created;
      title = description = acceptance = '';
      void handle.update();
      return;
    }
    if (handle.props.onCreated) handle.props.onCreated(created);
    else void navigate(routes.task.href({ id: created.id }));
  };

  return () => {
    const projectId = s.workspace.projectId;
    const agents = s.project.agentList;
    const pair = crewPair(agents, worker, reviewer);
    const nameOf = (id: string) => agents.find((a) => a.id === id)?.name ?? id;
    const hasPair = !!pair.worker && !!pair.reviewer;
    const lic = s.workspace.license;
    const blocked = startBlock({ ...pair, hasRepo: projectHasRepo(s.workspace.project), readOnly: lic.readOnly, reason: lic.reason });
    const off = act.busy !== '' || !title.trim() || !projectId;
    const options = agents.filter((a) => !(a as { archived?: boolean }).archived).map((a) => ({ value: a.id, label: `${a.name} (${a.role})` }));
    return (
      <Page title="New task" icon={SquarePen} subtitle="Describe the work; one agent builds it and another reviews it">
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
                // Enter (and "Create task") only creates: paid work starts only on a click on "Create and start"
                void submit(false);
              }),
            ]}
          >
            <ErrorNote error={act.error} what="Task not created" />
            {made ? (
              <p role="status" data-testid="task-made-not-started">
                {made.startError}.{' '}
                <a href={routes.task.href({ id: made.id })}>Open task #{made.seq}</a> to start it from its page.
              </p>
            ) : null}
            <Field label="Task title">
              {(id) => (
                <input id={id} class="title-input" required autofocus placeholder="Add Multiply to the calculator" value={title} mix={on('input', (e) => set(() => (title = e.currentTarget.value)))} />
              )}
            </Field>
            <Field label="Description" hint="Markdown. What to change and why; the worker reads this.">
              {(id) => <textarea id={id} rows={7} value={description} mix={on('input', (e) => set(() => (description = e.currentTarget.value)))} />}
            </Field>
            <Field label="Acceptance criteria" hint="One per line, Given / When / Then. Numbered for you (AC1, AC2...).">
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
            {hasPair || changing ? (
              <div class="crew" role="group" aria-label="Crew" data-testid="task-crew">
                {changing ? (
                  <div class="crew-fields">
                    <Field label="Worker">
                      {(id) => <Select id={id} label="Worker" value={pair.worker} options={[{ value: '', label: 'Choose a worker' }, ...options]} onChange={(v) => set(() => (worker = v))} />}
                    </Field>
                    <Field label="Reviewer">
                      {(id) => <Select id={id} label="Reviewer" value={pair.reviewer} options={[{ value: '', label: 'Choose a reviewer' }, ...options]} onChange={(v) => set(() => (reviewer = v))} />}
                    </Field>
                  </div>
                ) : (
                  <span class="crew-chip" data-testid="task-crew-pair">
                    {nameOf(pair.worker)} builds, {nameOf(pair.reviewer)} reviews
                    <span aria-hidden="true">·</span>
                    <Button size="sm" variant="ghost" mix={on('click', () => set(() => (changing = true)))}>
                      Change
                    </Button>
                  </span>
                )}
              </div>
            ) : agents.length > 0 ? (
              <p mix={muted} data-testid="task-crew-none">
                Add a second agent to the crew to start tasks from here: one builds, another reviews.
              </p>
            ) : null}
            <div mix={formActions}>
              {hasPair ? (
                <>
                  <Button variant="primary" disabled={off || !!blocked} title={blocked} mix={on('click', () => void submit(true))}>
                    {act.busy === 'start' ? 'Starting...' : 'Create and start'}
                  </Button>
                  <Button type="submit" disabled={off} title="Enter does this too">
                    Create only
                  </Button>
                </>
              ) : (
                <Button type="submit" variant="primary" disabled={off}>
                  Create task
                </Button>
              )}
              <a href={routes.tasks.href()} mix={buttonStyle('ghost')}>
                Cancel
              </a>
              <span mix={muted} style={{ marginLeft: 'auto', fontSize: 'var(--text-sm)' }}>
                {parseAcceptance(acceptance).length} criteria
              </span>
            </div>
            {hasPair && blocked && title.trim() ? (
              <p mix={muted} data-testid="task-start-blocked">
                {blocked}
              </p>
            ) : null}
          </form>
        )}
      </Page>
    );
  };
}
