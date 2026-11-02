// Plan with PlanTeam: describe a goal, an agent with the pm or architect role proposes a graph of tasks, and nothing is
// created until the human accepts a dependency-closed selection of them (edited if they like). The planner's text is
// outside text: it is rendered inert through the Markdown component and decides nothing.

import { css, on, type Handle } from 'remix/component';
import { Check, CircleCheck, CircleX, Flag, GitBranch, ListChecks, Pencil, Play, Sparkles, Target, UserX, X } from 'lucide';
import type { DevSettings, Milestone, Plan, ProposedTask, TickResult } from '../../api/extTypes.ts';
import { routes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote } from '../../ui/feedback.tsx';
import { Field, Switch, formActions, formGrid } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card, row } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { plural } from '../../lib/format.ts';
import { newAgentHref } from '../work/edit-agent.ts';
import { acceptLabel, assignable, editsFor, lines, milestoneTitle, newestFirst, nobodyCount, planTone, stageText, startedText, STEPS, stepStates, toggle, type StepState } from './plan.ts';

// --- stepper ---

const stepperCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  flexWrap: 'wrap',
  '& li': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    fontSize: t.textMd,
    color: t.textMuted,
    fontWeight: '500',
  },
  '& li[aria-current="step"]': { color: t.text },
  '& li + li::before': {
    content: '""',
    width: '28px',
    height: '1px',
    background: t.borderStrong,
  },
});
const stepDot = css({
  display: 'inline-grid',
  placeItems: 'center',
  width: '20px',
  height: '20px',
  borderRadius: '50%',
  fontSize: t.textXs,
  fontWeight: '600',
  border: `1px solid ${t.borderStrong}`,
  color: t.textMuted,
  background: t.bgSurface,
});
const stepPulse = css({
  animation: 'dd-plan-pulse 1.2s ease-in-out infinite',
  '@keyframes dd-plan-pulse': { '50%': { opacity: '0.45' } },
});
const stepperWrap = css({
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: t.s3,
  flexWrap: 'wrap',
  padding: `${t.s3} ${t.s4}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
});

const STEP_STYLE: Record<StepState, { fg: string; bg: string; border: string }> = {
  done: { fg: t.onAccent, bg: t.successSolid, border: t.successSolid },
  current: { fg: t.onAccent, bg: t.accent, border: t.accent },
  working: { fg: t.accentText, bg: t.accentSoft, border: t.accent },
  failed: { fg: t.onAccent, bg: t.dangerSolid, border: t.dangerSolid },
  todo: { fg: t.textMuted, bg: t.bgSurface, border: t.borderStrong },
};

const STEP_WORD: Record<StepState, string> = {
  done: 'done',
  current: 'current',
  working: 'in progress',
  failed: 'stopped',
  todo: 'not yet',
};

/** Where the plan on screen is: goal -> proposal -> review -> create. */
function Stepper(handle: Handle<{ plan: Plan | undefined }>) {
  return () => {
    const plan = handle.props.plan;
    const states = stepStates(plan);
    return (
      <div mix={stepperWrap} data-testid="plan-stepper" data-stage={plan?.status ?? 'GOAL'}>
        <ol mix={stepperCss} aria-label="Plan progress">
          {STEPS.map((name, i) => {
            const st = states[i]!;
            const c = STEP_STYLE[st];
            const active = st === 'current' || st === 'working' || st === 'failed';
            return (
              <li key={name} aria-current={active ? 'step' : undefined} data-state={st}>
                <span
                  mix={[stepDot, st === 'working' ? stepPulse : undefined]}
                  style={{
                    color: c.fg,
                    background: c.bg,
                    borderColor: c.border,
                  }}
                  aria-hidden="true"
                >
                  {st === 'done' ? (
                    <Icon icon={Check} size={12} strokeWidth={2.5} />
                  ) : st === 'failed' ? (
                    <Icon icon={X} size={12} strokeWidth={2.5} />
                  ) : (
                    String(i + 1)
                  )}
                </span>
                <span>{name}</span>
                <span class="sr-only">({STEP_WORD[st]})</span>
              </li>
            );
          })}
        </ol>
        <span mix={muted} style={{ fontSize: 'var(--text-sm)' }}>
          {stageText(plan)}
        </span>
      </div>
    );
  };
}

// --- one proposed task's editor ---

function TaskEditor(
  handle: Handle<{
    task: ProposedTask;
    /** The plan roles the crew has an agent for (from the core); without them the role is typed. */
    crewRoles?: string[] | null;
    onSave: (t: ProposedTask) => void;
    onCancel: () => void;
  }>,
) {
  const t0 = handle.props.task;
  let title = t0.title;
  let desc = t0.description;
  let role = t0.role;
  let acc = (t0.acceptance ?? []).join('\n');
  const input = (set: (v: string) => void) =>
    on<HTMLInputElement | HTMLTextAreaElement, 'input'>('input', (e) => (set(e.currentTarget.value), void handle.update()));
  return () => (
    <div mix={editorCss} data-testid="plan-task-editor">
      <div mix={formGrid}>
        <Field label="Title">{(id) => <input id={id} value={title} mix={input((v) => (title = v))} />}</Field>
        <Field label="Role">
          {(id) => {
            const roles = handle.props.crewRoles;
            if (!roles) return <input id={id} value={role} mix={input((v) => (role = v))} />;
            const options: { value: string; label: string; hint?: string }[] = roles.map((r) => ({ value: r, label: r }));
            if (!roles.includes(t0.role)) options.push({ value: t0.role, label: t0.role, hint: 'nobody on the crew' });
            return <Select id={id} label="Role" value={role} options={options} onChange={(v) => ((role = v), void handle.update())} />;
          }}
        </Field>
      </div>
      <Field label="Description">{(id) => <textarea id={id} rows={3} value={desc} mix={input((v) => (desc = v))} />}</Field>
      <Field label="Acceptance criteria (one per line)">{(id) => <textarea id={id} rows={3} value={acc} mix={input((v) => (acc = v))} />}</Field>
      <div mix={formActions}>
        <Button
          variant="primary"
          size="sm"
          mix={on('click', () =>
            handle.props.onSave({
              ...handle.props.task,
              title,
              role,
              description: desc,
              acceptance: lines(acc),
            }),
          )}
        >
          Keep my edits
        </Button>
        <Button size="sm" variant="ghost" mix={on('click', () => handle.props.onCancel())}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

const editorCss = css({
  display: 'grid',
  gap: t.s3,
  marginTop: t.s2,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
});

// --- the proposal under review ---

const planHead = css({
  display: 'flex',
  alignItems: 'flex-start',
  gap: t.s3,
  padding: `${t.s4} ${t.s4} ${t.s3}`,
  '& h3': {
    flex: '1',
    fontSize: t.textLg,
    fontWeight: '600',
    minWidth: '0',
    overflowWrap: 'anywhere',
  },
});
const planCard = css({
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  minWidth: '0',
});
const planBody = css({
  display: 'grid',
  gap: t.s3,
  padding: `0 ${t.s4} ${t.s4}`,
});
const notesCss = css({
  padding: `${t.s2} ${t.s3}`,
  borderLeft: `2px solid ${t.borderStrong}`,
  color: t.textMuted,
  fontSize: t.textMd,
});
const tasksCss = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  borderTop: `1px solid ${t.border}`,
});
const taskCss = css({
  display: 'grid',
  gridTemplateColumns: '24px 1fr auto',
  gap: `${t.s1} ${t.s3}`,
  alignItems: 'start',
  padding: `${t.s3} ${t.s4}`,
  borderBottom: `1px solid ${t.border}`,
  transition: `opacity ${t.fast} ${t.ease}, background-color ${t.fast} ${t.ease}`,
  '&[data-selected="false"]': { opacity: '0.55' },
  '&:hover': { background: t.bgHover },
  '& .pick': { paddingTop: '2px' },
  '& .title': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
    minWidth: '0',
  },
  '& .title strong': { fontWeight: '600', overflowWrap: 'anywhere' },
  '& .desc': { color: t.textMuted, fontSize: t.textMd },
  '& .deps': {
    display: 'flex',
    alignItems: 'center',
    gap: '6px',
    flexWrap: 'wrap',
    color: t.textMuted,
    fontSize: t.textSm,
  },
  '& .meta': { display: 'flex', alignItems: 'center', gap: t.s2 },
  '& .nobody': { display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap', color: t.warning, fontSize: t.textSm, margin: '0' },
  '& .body': { display: 'grid', gap: t.s2, minWidth: '0' },
});
const criteriaCss = css({
  display: 'grid',
  gap: '4px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  fontSize: t.textMd,
  '& li': { display: 'flex', alignItems: 'flex-start', gap: t.s2 },
  '& li svg': { marginTop: '3px', color: t.success },
});
const reviewBar = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  background: t.bgApp,
  '& .summary': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    flexWrap: 'wrap',
  },
});
const createdLink = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '4px',
  fontSize: t.textSm,
  fontWeight: '500',
});

/** A proposed plan: the task graph, a dependency-closed selection, edits, and Accept / Reject. All text is the planner's (untrusted). */
export function PlanReview(handle: Handle<{ plan: Plan; onDone: (answer?: Plan) => void; started?: TickResult }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let selected = new Set((handle.props.plan.proposal?.tasks ?? []).map((x) => x.key));
  let edits: Record<string, ProposedTask> = {};
  let editing: string | undefined;

  const decide = async (what: 'accept' | 'start' | 'reject') => {
    const { plan, onDone } = handle.props;
    let answer: Plan | undefined;
    if (what === 'reject') await act.run('reject', () => s.client.planReject(plan.id));
    else answer = await act.run(what, () => s.client.planAccept(plan.id, [...selected], editsFor(selected, edits), what === 'start'));
    if (!act.error) onDone(answer);
  };

  return () => {
    const { plan } = handle.props;
    const ro = s.workspace.license;
    const tasks = plan.proposal?.tasks ?? [];
    const proposed = plan.status === 'PROPOSED';
    const created = plan.createdTasks ?? {};
    const editedCount = Object.keys(editsFor(selected, edits)).length;
    const nobody = nobodyCount(plan, selected, edits);
    return (
      <article mix={planCard} data-testid="plan" data-status={plan.status} aria-label={`Plan: ${plan.goal}`}>
        <header mix={planHead}>
          <span
            style={{
              display: 'inline-flex',
              color: 'var(--text-muted)',
              marginTop: '3px',
            }}
          >
            <Icon icon={Target} size={16} />
          </span>
          <h3>{plan.goal}</h3>
          <Badge tone={planTone(plan.status)}>{plan.status.toLowerCase()}</Badge>
        </header>
        <div mix={planBody}>
          {plan.plannedBy ? (
            <p mix={muted} style={{ fontSize: 'var(--text-sm)' }} data-testid="plan-by">
              Planned by {plan.plannedBy}
            </p>
          ) : null}
          {plan.error ? <ErrorNote error={plan.error} what="The planner failed" /> : null}
          {plan.proposal?.notes ? (
            <div mix={notesCss}>
              <Markdown source={plan.proposal.notes} />
            </div>
          ) : null}
          <ErrorNote error={act.error} what="Not sent" />
        </div>
        {tasks.length > 0 ? (
          <ul mix={tasksCss} aria-label="Proposed tasks">
            {tasks.map((t0) => {
              const tk = edits[t0.key] ?? t0;
              const on_ = selected.has(tk.key);
              const deps = tk.dependsOn ?? [];
              const acceptance = tk.acceptance ?? [];
              const createdId = created[tk.key];
              return (
                <li key={tk.key} mix={taskCss} data-testid="plan-task" data-key={tk.key} data-selected={proposed ? String(on_) : undefined}>
                  <span class="pick">
                    {proposed ? (
                      <input
                        type="checkbox"
                        aria-label={`Create ${tk.title}`}
                        checked={on_}
                        mix={on('change', (e) => {
                          selected = toggle(selected, tk.key, e.currentTarget.checked, tasks);
                          void handle.update();
                        })}
                      />
                    ) : createdId ? (
                      <Icon icon={CircleCheck} size={16} label="Created" />
                    ) : (
                      <Icon icon={CircleX} size={16} label="Not created" />
                    )}
                  </span>
                  <div class="body">
                    <div class="title">
                      <strong>{tk.title}</strong>
                      {edits[tk.key] ? <Badge tone="info">edited</Badge> : null}
                      <code>{tk.key}</code>
                    </div>
                    {tk.description ? (
                      <div class="desc">
                        <Markdown source={tk.description} maxChars={3000} />
                      </div>
                    ) : null}
                    {acceptance.length > 0 ? (
                      <ul mix={criteriaCss} aria-label={`Acceptance criteria for ${tk.title}`}>
                        {acceptance.map((a, i) => (
                          <li key={i}>
                            <Icon icon={Check} size={13} />
                            <span>{a}</span>
                          </li>
                        ))}
                      </ul>
                    ) : null}
                    <div class="deps">
                      <Icon icon={GitBranch} size={13} />
                      {deps.length > 0 ? (
                        <>
                          <span>Depends on</span>
                          {deps.map((d) => (
                            <code key={d}>{d}</code>
                          ))}
                        </>
                      ) : (
                        <span>Depends on none</span>
                      )}
                    </div>
                    {proposed && assignable(plan, tk) === false ? (
                      <p class="nobody" data-testid="plan-task-nobody">
                        <Icon icon={UserX} size={13} />
                        <span>No {tk.role} agent on the crew</span> ·{' '}
                        <a href={newAgentHref()} data-testid="plan-add-agent">
                          Add one
                        </a>{' '}
                        ·{' '}
                        <Button
                          variant="link"
                          size="sm"
                          aria-label={`Change the role of ${tk.title}`}
                          mix={on('click', () => {
                            editing = tk.key;
                            void handle.update();
                          })}
                        >
                          Change role
                        </Button>
                      </p>
                    ) : null}
                    {proposed && editing !== tk.key ? (
                      <div>
                        <Button
                          variant="ghost"
                          size="sm"
                          icon={Pencil}
                          mix={on('click', () => {
                            editing = tk.key;
                            void handle.update();
                          })}
                        >
                          Edit {tk.title}
                        </Button>
                      </div>
                    ) : null}
                    {editing === tk.key ? (
                      <TaskEditor
                        key={`edit-${tk.key}`}
                        task={tk}
                        crewRoles={plan.crewRoles}
                        onSave={(x) => {
                          edits = { ...edits, [tk.key]: x };
                          editing = undefined;
                          void handle.update();
                        }}
                        onCancel={() => {
                          editing = undefined;
                          void handle.update();
                        }}
                      />
                    ) : null}
                  </div>
                  <div class="meta">
                    {tk.role ? <Badge tone="accent">{tk.role}</Badge> : null}
                    {!proposed && createdId ? (
                      <a mix={createdLink} href={routes.task.href({ id: createdId })}>
                        created
                      </a>
                    ) : null}
                    {!proposed && !createdId && plan.status === 'ACCEPTED' ? <Badge>not created</Badge> : null}
                  </div>
                </li>
              );
            })}
          </ul>
        ) : null}
        {proposed ? (
          <div mix={reviewBar} data-testid="plan-review">
            <div class="summary">
              <Icon icon={ListChecks} size={16} />
              <strong>
                {selected.size} of {tasks.length} task
                {tasks.length === 1 ? '' : 's'} selected
              </strong>
              {editedCount > 0 ? <Badge tone="info">{editedCount} edited</Badge> : null}
            </div>
            <p mix={muted} style={{ fontSize: 'var(--text-sm)' }}>
              A task that depends on another cannot be created without it: selecting one selects what it needs, and clearing one clears what needs it.
            </p>
            <ReadOnlyNote />
            <div mix={row}>
              <Button
                variant="primary"
                icon={Check}
                disabled={act.busy !== '' || ro.readOnly || selected.size === 0}
                title={ro.readOnly ? ro.reason : undefined}
                mix={on('click', () => void decide('accept'))}
              >
                {acceptLabel(selected.size, nobody)}
              </Button>
              <Button
                icon={Play}
                disabled={act.busy !== '' || ro.readOnly || selected.size === 0}
                title={ro.readOnly ? ro.reason : 'Create the tasks, then start the ready ones within the project limit'}
                mix={on('click', () => void decide('start'))}
              >
                {act.busy === 'start' ? 'Starting...' : 'Accept and start'}
              </Button>
              <Button disabled={act.busy !== ''} mix={on('click', () => void decide('reject'))}>
                Reject plan
              </Button>
            </div>
          </div>
        ) : null}
        {plan.status === 'ACCEPTED' && Object.keys(created).length > 0 ? <AfterAccept plan={plan} started={handle.props.started} /> : null}
      </article>
    );
  };
}

// --- after an accept: start the work, follow it in a milestone ---

const afterCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  background: t.bgApp,
  borderTop: `1px solid ${t.border}`,
  '& .tick': { display: 'grid', gap: t.s1, fontSize: t.textSm },
  '& .tick ul': { margin: '0', paddingLeft: '18px' },
});

/**
 * The accepted card's next steps: start the ready tasks now (one scheduler pass, the person's click), let the scheduler start the
 * rest as each one finishes (the project's own automatic scheduling switch, shown as it is and changed only here by the person),
 * and gather the plan's tasks in a milestone.
 */
export function AfterAccept(handle: Handle<{ plan: Plan; started?: TickResult }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const dev = query(handle, { load: () => s.client.projectdevGet(handle.props.plan.projectId), key: () => handle.props.plan.projectId });
  let tick: TickResult | undefined = handle.props.started;
  let milestone: Milestone | undefined;

  const start = async () => {
    const r = await act.run('tick', () => s.client.schedulerTick(handle.props.plan.projectId));
    if (r) (tick = r), void handle.update();
  };
  const setAuto = async (cur: DevSettings, on_: boolean) => {
    const r = await act.run('auto', () => s.client.projectdevSet(handle.props.plan.projectId, { ...cur, autoSchedule: on_ }));
    if (r) dev.set(r);
  };
  const toMilestone = async () => {
    const { plan } = handle.props;
    const r = await act.run('milestone', () => s.client.milestoneCreate(plan.projectId, milestoneTitle(plan.goal), undefined, undefined, Object.values(plan.createdTasks ?? {})));
    if (r) (milestone = r), void handle.update();
  };

  return () => {
    const ro = s.workspace.license;
    const d = dev.data;
    const busy = act.busy !== '';
    const max = d?.maxParallel ?? 0;
    return (
      <div mix={afterCss} data-testid="plan-after">
        <ErrorNote error={act.error ?? dev.error} />
        <div mix={row}>
          <Button
            variant="primary"
            icon={Play}
            disabled={busy || ro.readOnly}
            title={ro.readOnly ? ro.reason : undefined}
            mix={on('click', () => void start())}
          >
            {max > 0 ? `Start now (up to ${max} at once)` : 'Start now'}
          </Button>
          <Button icon={Flag} disabled={busy || ro.readOnly || !!milestone} mix={on('click', () => void toMilestone())}>
            Create a milestone from this plan
          </Button>
        </div>
        {d ? (
          <Switch
            label="Start the rest as each one finishes"
            checked={d.autoSchedule}
            disabled={busy || ro.readOnly}
            onChange={(v) => void setAuto(d, v)}
          />
        ) : null}
        {tick ? (
          <div class="tick" role="status" data-testid="plan-started">
            <p>{startedText(tick)}</p>
            {(tick.skipped ?? []).length > 0 ? (
              <ul>
                {(tick.skipped ?? []).map((sk) => (
                  <li key={sk.taskId}>
                    <a href={routes.task.href({ id: sk.taskId })}>{taskName(handle.props.plan, sk.taskId)}</a> <span mix={muted}>{sk.reason}</span>
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}
        {milestone ? (
          <p role="status" data-testid="plan-milestone">
            Milestone &quot;{milestone.title}&quot; created with {plural((milestone.taskIds ?? []).length, 'task')}. <a href={routes.milestones.href()}>Open milestones</a>
          </p>
        ) : null}
      </div>
    );
  };
}

/** The planned task's title for a created task id, else the id. */
function taskName(plan: Plan, id: string): string {
  const key = Object.entries(plan.createdTasks ?? {}).find(([, v]) => v === id)?.[0];
  return (plan.proposal?.tasks ?? []).find((x) => x.key === key)?.title ?? id;
}

// --- the screen ---

const workingCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  padding: t.s4,
  '& .spin': {
    width: '16px',
    height: '16px',
    flex: 'none',
    borderRadius: '50%',
    border: `2px solid ${t.borderStrong}`,
    borderTopColor: t.accent,
    animation: 'dd-plan-spin 700ms linear infinite',
  },
  '@keyframes dd-plan-spin': { to: { transform: 'rotate(360deg)' } },
});
const composeCss = css({ display: 'grid', gap: t.s4 });
// the requirements take the width; the planner picker sits beside them (they were a 230 px box next to a full-width goal)
const reqGrid = css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(200px, 280px)', gap: t.s4, alignItems: 'start', '@media (max-width: 719px)': { gridTemplateColumns: '1fr' } });
const newPlanCss = css({
  display: 'grid',
  gap: t.s3,
  '& > summary': { cursor: 'pointer', width: 'fit-content', color: t.textMuted, fontSize: t.textSm, fontWeight: '500' },
  '& > summary:hover': { color: t.text },
});
const planSelect = css({ width: '280px', maxWidth: '40vw' });

export function PlanScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const act = action(handle);
  const pid = () => s.workspace.projectId;
  const q = query(handle, {
    load: () => s.client.planList(pid()!),
    when: (e) => e.type.startsWith('workflow.') || e.type.startsWith('task.') || e.type.startsWith('plan.'),
    key: pid,
    enabled: () => !!pid(),
    debounceMs: 300,
  });
  let goal = '';
  let reqs = '';
  let planner = '';
  let sel: string | undefined;
  // the scheduler pass of the last "Accept and start", shown on that plan's accepted card
  let started: { planId: string; tick: TickResult } | undefined;
  const onDone = (answer?: Plan) => {
    if (answer?.start) started = { planId: answer.id, tick: answer.start };
    q.reload();
  };

  const propose = async () => {
    const projectId = pid();
    if (!projectId) return;
    const r = await act.run('propose', () => s.client.planPropose(projectId, goal.trim(), reqs, planner));
    if (r) {
      goal = '';
      reqs = '';
      sel = r.planId;
      q.reload();
    }
  };

  return () => {
    const ro = s.workspace.license;
    const agents = s.project.agentList;
    const plans = newestFirst(q.data ?? []);
    const current = plans.find((p) => p.id === sel) ?? plans[0];
    // while a plan is drafted or waits for review it leads, and the form for a new goal folds under it (UI review P1-9)
    const lead = current?.status === 'PLANNING' || current?.status === 'PROPOSED';
    return (
      <Page
        title="Plan"
        icon={Sparkles}
        subtitle="A pm or architect agent proposes tasks; nothing is created until you accept" guide="tasks"
        actions={
          plans.length > 1 ? (
            <Select
              label="Plan"
              value={current?.id ?? ''}
              mix={[planSelect]}
              options={plans.map((p) => ({ value: p.id, label: p.goal.slice(0, 60), hint: p.status.toLowerCase() }))}
              onChange={(v) => {
                sel = v;
                void handle.update();
              }}
            />
          ) : undefined
        }
      >
        <ErrorNote error={q.error ?? act.error} what="Plan" />
        <Stepper plan={current} />
        {lead ? (
          <>
            <Section title="Proposal">
              {current ? (
                current.status === 'PLANNING' ? (
                  <div mix={[planCard, workingCss]} data-testid="plan-working">
                    <span class="spin" aria-hidden="true" />
                    <p role="status">The planner is working on &quot;{current.goal}&quot;...</p>
                  </div>
                ) : (
                  <PlanReview key={current.id + current.status} plan={current} onDone={onDone} started={started?.planId === current.id ? started.tick : undefined} />
                )
              ) : q.data ? (
                <div mix={card}>
                  <Empty title="No plans yet." icon={Sparkles}>
                    <p>Describe a goal above and the planner proposes tasks for you to review.</p>
                  </Empty>
                </div>
              ) : null}
            </Section>
            <details mix={newPlanCss} data-testid="plan-new">
              <summary>Plan something else</summary>
              <form
                mix={[
                  card,
                  composeCss,
                  on('submit', (e) => {
                    e.preventDefault();
                    if (!act.busy && !ro.readOnly && goal.trim()) void propose();
                  }),
                ]}
              >
                <Field label="Goal">
                  {(id) => (
                    <textarea
                      id={id}
                      rows={3}
                      value={goal}
                      placeholder="Add arithmetic helpers to the calculator"
                      mix={on('input', (e) => ((goal = e.currentTarget.value), void handle.update()))}
                    />
                  )}
                </Field>
                <div mix={reqGrid}>
                  <Field label="Requirements or constraints (optional)">
                    {(id) => <textarea id={id} rows={2} value={reqs} mix={on('input', (e) => ((reqs = e.currentTarget.value), void handle.update()))} />}
                  </Field>
                  <Field label="Planner agent">
                    {(id) => (
                      <Select
                        id={id}
                        label="Planner agent"
                        value={planner}
                        options={[{ value: '', label: 'First agent with the pm or architect role' }, ...agents.map((a) => ({ value: a.id, label: `${a.name} (${a.role})` }))]}
                        onChange={(v) => ((planner = v), void handle.update())}
                      />
                    )}
                  </Field>
                </div>
                <div mix={formActions}>
                  <Button
                    type="submit"
                    variant="primary"
                    icon={Sparkles}
                    disabled={act.busy !== '' || ro.readOnly || !goal.trim()}
                    title={ro.readOnly ? ro.reason : undefined}
                  >
                    {act.busy === 'propose' ? 'Asking the planner...' : 'Propose a plan'}
                  </Button>
                </div>
              </form>
            </details>
          </>
        ) : (
          <>
            <Section title="What should the team achieve?" description="The planner breaks it into tasks with acceptance criteria and dependencies.">
              <form
                mix={[
                  card,
                  composeCss,
                  on('submit', (e) => {
                    e.preventDefault();
                    if (!act.busy && !ro.readOnly && goal.trim()) void propose();
                  }),
                ]}
              >
                <Field label="Goal">
                  {(id) => (
                    <textarea
                      id={id}
                      rows={3}
                      value={goal}
                      placeholder="Add arithmetic helpers to the calculator"
                      mix={on('input', (e) => ((goal = e.currentTarget.value), void handle.update()))}
                    />
                  )}
                </Field>
                <div mix={reqGrid}>
                  <Field label="Requirements or constraints (optional)">
                    {(id) => <textarea id={id} rows={2} value={reqs} mix={on('input', (e) => ((reqs = e.currentTarget.value), void handle.update()))} />}
                  </Field>
                  <Field label="Planner agent">
                    {(id) => (
                      <Select
                        id={id}
                        label="Planner agent"
                        value={planner}
                        options={[{ value: '', label: 'First agent with the pm or architect role' }, ...agents.map((a) => ({ value: a.id, label: `${a.name} (${a.role})` }))]}
                        onChange={(v) => ((planner = v), void handle.update())}
                      />
                    )}
                  </Field>
                </div>
                <div mix={formActions}>
                  <Button
                    type="submit"
                    variant="primary"
                    icon={Sparkles}
                    disabled={act.busy !== '' || ro.readOnly || !goal.trim()}
                    title={ro.readOnly ? ro.reason : undefined}
                  >
                    {act.busy === 'propose' ? 'Asking the planner...' : 'Propose a plan'}
                  </Button>
                </div>
              </form>
            </Section>
            <Section title="Proposal">
              {current ? (
                current.status === 'PLANNING' ? (
                  <div mix={[planCard, workingCss]} data-testid="plan-working">
                    <span class="spin" aria-hidden="true" />
                    <p role="status">The planner is working on &quot;{current.goal}&quot;...</p>
                  </div>
                ) : (
                  <PlanReview key={current.id + current.status} plan={current} onDone={onDone} started={started?.planId === current.id ? started.tick : undefined} />
                )
              ) : q.data ? (
                <div mix={card}>
                  <Empty title="No plans yet." icon={Sparkles}>
                    <p>Describe a goal above and the planner proposes tasks for you to review.</p>
                  </Empty>
                </div>
              ) : null}
            </Section>
          </>
        )}
      </Page>
    );
  };
}
