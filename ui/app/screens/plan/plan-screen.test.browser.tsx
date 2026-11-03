import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { DevSettings, Milestone, Plan, ProposedTask, TickResult } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { AfterAccept, PlanReview, PlanScreen } from './plan-screen.tsx';

afterEach(cleanup);

const must = <T,>(x: T | null | undefined): T => {
  if (x === null || x === undefined) throw new Error('not yet');
  return x;
};

const pt = (key: string, deps: string[] = []): ProposedTask => ({
  key,
  title: `Task ${key}`,
  description: `Do ${key}`,
  role: 'backend',
  dependsOn: deps,
  acceptance: [`Given ${key}, when run, then ok`],
});
const tasks = [pt('a'), pt('b', ['a']), pt('c', ['b']), pt('d')];
const plan = (over: Partial<Plan> = {}): Plan => ({
  id: 'plan_1',
  projectId: 'prj_1',
  runId: 'run_1',
  goal: 'Add arithmetic',
  status: 'PROPOSED',
  plannerAgent: 'agt_pm',
  proposal: { tasks, notes: 'a first' },
  createdAt: 1,
  ...over,
});

function servicesWith(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

const box = (root: ParentNode, label: string) => byLabel<HTMLInputElement>(root, label);

// the planner the Plan page remembers per project lives in this browser's storage (X45)
const forgetPlanner = () => {
  try {
    localStorage.removeItem('drydock.planner.prj_1');
  } catch {
    // storage blocked: nothing remembered
  }
};

describe('PlanReview', () => {
  it('accepting sends exactly the selected, dependency-closed tasks and only the edits to them', async () => {
    const calls: [string, string[], Record<string, ProposedTask>][] = [];
    let done = 0;
    const s = testServices({
      planAccept: async (id, accept, edits) => (calls.push([id, accept ?? [], edits ?? {}]), plan({ status: 'ACCEPTED' })),
    });
    const p = plan({
      proposal: {
        tasks: [...tasks.slice(0, 2), { ...pt('x'), description: '<script>window.__xss=1</script>' }],
        notes: '',
      },
    });
    const r = mount(<PlanReview plan={p} onDone={() => done++} />, s);
    r.root.flush();
    expect(r.container.querySelector('script')).toBe(null);
    // clearing the dependency clears its dependent
    await r.act(() => box(r.container, 'Create Task a').click());
    expect(box(r.container, 'Create Task b').checked).toBe(false);
    await r.act(() => box(r.container, 'Create Task b').click()); // selecting b brings a back
    expect(box(r.container, 'Create Task a').checked).toBe(true);
    await r.act(() => box(r.container, 'Create Task x').click());
    // edit task b
    await r.act(() => byText(r.container, 'button', 'Edit Task b').click());
    type(byLabel(r.container, 'Title'), 'Task b (renamed)');
    await r.act(() => byText(r.container, 'button', 'Keep my edits').click());
    expect(r.container.textContent).toContain('Task b (renamed)');
    expect(r.$('[data-testid="plan-review"]')!.textContent).toContain('2 of 3 tasks selected');
    await r.act(() => byText(r.container, 'button', 'Accept 2 tasks').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    const [id, accept, edits] = calls[0]!;
    expect(id).toBe('plan_1');
    expect([...accept].sort()).toEqual(['a', 'b']);
    expect(Object.keys(edits)).toEqual(['b']);
    expect(edits.b!.title).toBe('Task b (renamed)');
    await waitFor(() => expect(done).toBe(1), r);
  });

  it('cancelling an edit keeps the planner text', async () => {
    const r = mount(<PlanReview plan={plan()} onDone={() => undefined} />, testServices());
    await r.act(() => byText(r.container, 'button', 'Edit Task a').click());
    type(byLabel(r.container, 'Title'), 'Other');
    await r.act(() => byText(r.container, 'button', 'Cancel').click());
    expect(r.$('[data-testid="plan-task-editor"]')).toBe(null);
    expect(r.container.textContent).not.toContain('Other');
    expect(r.container.textContent).not.toContain('edited');
  });

  it('rejecting calls plan.reject; an accepted plan links the created tasks and has no buttons', async () => {
    const rejected: string[] = [];
    const r = mount(<PlanReview plan={plan()} onDone={() => undefined} />, testServices({ planReject: async (id) => (rejected.push(id), {}) }));
    await r.act(() => byText(r.container, 'button', 'Reject plan').click());
    await waitFor(() => expect(rejected).toEqual(['plan_1']), r);
    cleanup();
    const r2 = mount(<PlanReview plan={plan({ status: 'ACCEPTED', createdTasks: { a: 'task_9' } })} onDone={() => undefined} />, testServices());
    r2.root.flush();
    expect(Array.from(r2.container.querySelectorAll('button')).some((b) => /Accept/.test(b.textContent ?? ''))).toBe(false);
    expect(r2.container.querySelector('input[type=checkbox]')).toBe(null);
    expect(byText(r2.container, 'a', 'created').getAttribute('href')).toBe('/task/task_9');
    expect(r2.$('[data-testid="plan"]')!.getAttribute('data-status')).toBe('ACCEPTED');
  });

  it('a planner failure is shown as the core wrote it', () => {
    const r = mount(
      <PlanReview
        plan={plan({
          status: 'FAILED',
          error: 'the planner did not produce a valid plan: not JSON',
          proposal: null,
        })}
        onDone={() => undefined}
      />,
      testServices(),
    );
    r.root.flush();
    expect(r.container.textContent).toContain('did not produce a valid plan');
  });

  it('a core refusal is shown and onDone is not called', async () => {
    let done = 0;
    const r = mount(
      <PlanReview plan={plan()} onDone={() => done++} />,
      testServices({
        planAccept: async () => {
          throw new Error('plan is not PROPOSED');
        },
      }),
    );
    await r.act(() => byText(r.container, 'button', 'Accept 4 tasks').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('plan is not PROPOSED'), r);
    expect(done).toBe(0);
  });

  it('read-only turns Accept off with the core reason', async () => {
    const s = servicesWith({ licenseStatus: async () => expiredLicense });
    const r = mount(<PlanReview plan={plan()} onDone={() => undefined} />, s);
    const accept = await waitFor(() => {
      const b = byText<HTMLButtonElement>(r.container, 'button', 'Accept 4 tasks');
      if (!b.disabled) throw new Error('not yet');
      return b;
    }, r);
    expect(accept.title).toContain('read-only');
    expect(r.$('[data-testid="readonly-note"]')).not.toBe(null);
  });
});

describe('PlanReview: who can take each task (X46)', () => {
  const qa = { ...pt('q'), title: 'Task q', role: 'qa' };
  const withCrew = (over: Partial<Plan> = {}) =>
    plan({ proposal: { tasks: [pt('a'), pt('b', ['a']), qa], notes: '' }, assignable: { a: true, b: true, q: false }, crewRoles: ['backend', 'reviewer'], ...over });

  it('names a task nobody on the crew can take, with Add one and Change role, and Accept counts them', async () => {
    const r = mount(<PlanReview plan={withCrew({ plannedBy: 'Priya' })} onDone={() => undefined} />, testServices());
    r.root.flush();
    expect(r.$('[data-testid="plan-by"]')!.textContent).toBe('Planned by Priya');
    const warn = r.$$('[data-testid="plan-task-nobody"]');
    expect(warn.length).toBe(1);
    expect(warn[0]!.closest('[data-testid="plan-task"]')!.getAttribute('data-key')).toBe('q');
    expect(warn[0]!.textContent).toContain('No qa agent on the crew');
    expect(byText(r.container, 'a', 'Add one').getAttribute('href')).toBe('/team?new=agent');
    byText(r.container, 'button', 'Accept 3 tasks · 1 has nobody');
    // clearing the task takes it out of the count
    await r.act(() => box(r.container, 'Create Task q').click());
    byText(r.container, 'button', 'Accept 2 tasks');
    await r.act(() => box(r.container, 'Create Task q').click());
    // Change role opens the editor with the crew's roles; choosing one someone has clears the warning
    await r.act(() => byText(r.container, 'button', 'Change the role of Task q').click());
    type(byLabel<HTMLElement>(r.container, 'Role'), 'backend');
    await r.act(() => byText(r.container, 'button', 'Keep my edits').click());
    expect(r.$$('[data-testid="plan-task-nobody"]').length).toBe(0);
    byText(r.container, 'button', 'Accept 3 tasks');
  });

  it('claims nothing when the core did not say who can take the tasks', () => {
    const r = mount(<PlanReview plan={plan()} onDone={() => undefined} />, testServices());
    r.root.flush();
    expect(r.$('[data-testid="plan-task-nobody"]')).toBe(null);
    byText(r.container, 'button', 'Accept 4 tasks');
  });
});

describe('Accept and start (X47)', () => {
  const dev = { maxParallel: 2, autoSchedule: false, respond: true } as DevSettings;
  const accepted = plan({ status: 'ACCEPTED', createdTasks: { a: 'task_a', b: 'task_b' } });

  it('Accept and start sends start and hands the scheduler pass back', async () => {
    const calls: (boolean | undefined)[] = [];
    let answer: Plan | undefined;
    const tick: TickResult = { promoted: [], started: ['task_a'], skipped: [], active: 1, capacity: 2, max: 2 };
    const s = testServices({ planAccept: async (_id, _a, _e, start) => (calls.push(start), { ...accepted, start: tick }) });
    const r = mount(<PlanReview plan={plan()} onDone={(a) => (answer = a)} />, s);
    await r.act(() => byText(r.container, 'button', 'Accept and start').click());
    await waitFor(() => expect(answer?.start?.started).toEqual(['task_a']), r);
    expect(calls).toEqual([true]);
  });

  it('the accepted card starts the ready tasks now, says what waits, and switches automatic starts only on a click', async () => {
    const ticks: string[] = [];
    const saved: DevSettings[] = [];
    const s = servicesWith({
      projectdevGet: async () => dev,
      projectdevSet: async (_p, d) => (saved.push(d), d),
      schedulerTick: async (p) => (ticks.push(p), { promoted: ['task_a'], started: ['task_a'], skipped: [{ taskId: 'task_b', reason: 'no worker or reviewer assigned' }], active: 1, capacity: 1, max: 2 }),
    });
    const r = mount(<PlanReview plan={accepted} onDone={() => undefined} />, s);
    const startBtn = await waitFor(() => byText<HTMLButtonElement>(r.container, 'button', 'Start now (up to 2 at once)'), r);
    expect(saved).toEqual([]);
    const sw = byLabel<HTMLElement>(r.container, 'Start the rest as each one finishes');
    expect(sw.getAttribute('aria-checked')).toBe('false');
    await r.act(() => startBtn.click());
    const st = await waitFor(() => must(r.$('[data-testid="plan-started"]')), r);
    expect(ticks).toEqual(['prj_1']);
    expect(st.textContent).toContain('Started 1 task. 1 task waits:');
    expect(st.textContent).toContain('Task b');
    expect(st.textContent).toContain('no worker or reviewer assigned');
    await r.act(() => sw.click());
    await waitFor(() => expect(saved.map((d) => d.autoSchedule)).toEqual([true]), r);
    expect(saved[0]!.maxParallel).toBe(2);
  });

  it('shows the pass of an Accept and start without another click', () => {
    const r = mount(<AfterAccept plan={accepted} started={{ promoted: [], started: ['task_a', 'task_b'], skipped: [], active: 2, capacity: 2, max: 2 }} />, testServices({ projectdevGet: async () => dev }));
    r.root.flush();
    expect(r.$('[data-testid="plan-started"]')!.textContent).toContain('Started 2 tasks.');
  });

  it("names the planned tasks that wait for their dependencies, not only the skipped ready ones", () => {
    const all = plan({ status: 'ACCEPTED', createdTasks: { a: 'task_a', b: 'task_b', c: 'task_c', d: 'task_d' } });
    const r = mount(
      <AfterAccept plan={all} started={{ promoted: [], started: ['task_a'], skipped: [{ taskId: 'task_d', reason: 'at the limit' }], active: 1, capacity: 1, max: 1 }} />,
      testServices({ projectdevGet: async () => dev }),
    );
    r.root.flush();
    const st = must(r.$('[data-testid="plan-started"]'));
    expect(st.textContent).toContain('Started 1 task. 3 tasks wait:');
    expect(st.textContent).toContain('at the limit');
    expect(Array.from(r.$$('[data-testid="plan-waits-for"]')).map((li) => li.textContent)).toEqual(['Task b waits for Task a', 'Task c waits for Task b']);
  });

  it('a failed pass after Accept and start says the tasks were created, and Start now tries again', async () => {
    const ticks: string[] = [];
    const s = testServices({
      projectdevGet: async () => dev,
      schedulerTick: async (p) => (ticks.push(p), { promoted: [], started: ['task_a'], skipped: [], active: 1, capacity: 2, max: 2 }),
    });
    const r = mount(<AfterAccept plan={accepted} startError="database is locked" />, s);
    r.root.flush();
    expect(must(r.$('[data-testid="plan-start-failed"]')).textContent).toContain('The tasks were created; starting them failed: database is locked');
    expect(r.$('[data-testid="plan-started"]')).toBe(null);
    await r.act(() => byText(r.container, 'button', 'Start now').click());
    await waitFor(() => must(r.$('[data-testid="plan-started"]')), r);
    expect(ticks).toEqual(['prj_1']);
    expect(r.$('[data-testid="plan-start-failed"]')).toBe(null);
  });

  it('creates a milestone from the plan with its created tasks (X49)', async () => {
    const made: unknown[][] = [];
    const s = testServices({
      projectdevGet: async () => dev,
      milestoneCreate: async (...args) => (made.push(args), { id: 'ms_1', title: 'Add arithmetic', taskIds: ['task_a', 'task_b'] } as Milestone),
    });
    const r = mount(<AfterAccept plan={accepted} />, s);
    await r.act(() => byText(r.container, 'button', 'Create a milestone from this plan').click());
    await waitFor(() => must(r.$('[data-testid="plan-milestone"]')), r);
    expect(made).toEqual([['prj_1', 'Add arithmetic', undefined, undefined, ['task_a', 'task_b']]]);
    expect(r.$('[data-testid="plan-milestone"]')!.textContent).toContain('created with 2 tasks');
  });
});

describe('PlanScreen', () => {
  const agent: Agent = {
    id: 'agt_pm',
    projectId: 'prj_1',
    name: 'Priya',
    adapter: 'claude',
    role: 'pm',
    model: '',
    effort: '',
    providerId: '',
    permissionMode: '',
    status: '',
  } as Agent;

  it('proposing asks the planner and shows it working, then the proposal', async () => {
    const proposed: unknown[][] = [];
    let status = 'NONE';
    const s = servicesWith({
      agentList: async () => [agent],
      planPropose: async (...args) => (proposed.push(args), (status = 'PLANNING'), { planId: 'plan_2', runId: 'run_2', plannerAgentId: 'agt_pm' }),
      planList: async () =>
        status === 'NONE'
          ? []
          : [
              plan({
                id: 'plan_2',
                status,
                proposal: status === 'PLANNING' ? null : plan().proposal,
              }),
            ],
    });
    const r = mount(<PlanScreen />, s);
    await waitFor(() => byText(r.container, 'h2', 'No plans yet.'), r);
    expect(r.$('[data-testid="plan-stepper"]')!.getAttribute('data-stage')).toBe('GOAL');
    // one agent on the crew: no choice to make, it plans
    await waitFor(() => expect(r.$('[data-testid="plan-planner"]')!.textContent).toBe('Priya plans.'), r);
    expect(r.$('[data-testid="plan-no-planner"]')).toBe(null);
    type(byLabel<HTMLTextAreaElement>(r.container, 'Goal'), 'Add arithmetic');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Propose a plan').click());
    await waitFor(() => expect(proposed).toEqual([['prj_1', 'Add arithmetic', '', '', []]]), r);
    await waitFor(() => expect(r.$('[role="status"]')!.textContent).toContain('planner is working'), r);
    expect(r.$('[data-testid="plan-stepper"]')!.getAttribute('data-stage')).toBe('PLANNING');
    expect(byLabel<HTMLTextAreaElement>(r.container, 'Goal').value).toBe('');
    status = 'PROPOSED';
    s.emit({ type: 'plan.proposed', projectId: 'prj_1' });
    await waitFor(() => expect(r.$$('[data-testid="plan-task"]').length).toBe(4), r, 3000);
    expect(r.$('[data-testid="plan"]')!.getAttribute('data-status')).toBe('PROPOSED');
  });

  it('sends the chosen planner agent and lets the human switch between plans', async () => {
    const proposed: unknown[][] = [];
    const s = servicesWith({
      agentList: async () => [agent, { ...agent, id: 'agt_be', name: 'Dana', role: 'backend' }],
      planPropose: async (...args) => (proposed.push(args), { planId: 'plan_1', runId: 'r', plannerAgentId: 'agt_pm' }),
      planList: async () => [plan({ id: 'old', goal: 'Old goal', status: 'REJECTED', createdAt: 1 }), plan({ id: 'new', goal: 'New goal', createdAt: 2 })],
    });
    const r = mount(<PlanScreen />, s);
    await waitFor(() => expect(r.$('[data-testid="plan"]')!.textContent).toContain('New goal'), r);
    expect(valueOf(byLabel(r.container, 'Plan'))).toBe('new');
    type(byLabel<HTMLElement>(r.container, 'Plan'), 'old');
    await waitFor(() => expect(r.$('[data-testid="plan"]')!.getAttribute('data-status')).toBe('REJECTED'), r);
    await waitFor(() => byText(r.container, '[role="option"]', 'Priya (pm)'), r);
    type(byLabel<HTMLTextAreaElement>(r.container, 'Goal'), 'More');
    type(byLabel<HTMLTextAreaElement>(r.container, 'Requirements or constraints (optional)'), 'no deps');
    type(byLabel<HTMLElement>(r.container, 'Planner agent'), 'agt_pm');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Propose a plan').click());
    await waitFor(() => expect(proposed).toEqual([['prj_1', 'More', 'no deps', 'agt_pm', []]]), r);
  });

  it('a proposal waiting for review leads; the goal form folds under it, and comes first again once the plan is decided (UI review P1-9)', async () => {
    let status = 'PROPOSED';
    const s = servicesWith({ agentList: async () => [agent], planList: async () => [plan({ id: 'p', goal: 'Add arithmetic', status })] });
    const r = mount(<PlanScreen />, s);
    const card = await waitFor(() => must(r.$('[data-testid="plan"]')), r);
    const goal = byLabel<HTMLTextAreaElement>(r.container, 'Goal');
    // the goal reads on the plan card, above the tasks and Accept; the form for a new one is folded below it
    expect(card.textContent).toContain('Add arithmetic');
    expect(card.compareDocumentPosition(goal) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const fold = goal.closest('details[data-testid="plan-new"]') as HTMLDetailsElement;
    expect([fold.open, fold.querySelector('summary')!.textContent]).toEqual([false, 'Plan something else']);
    status = 'ACCEPTED';
    s.emit({ type: 'plan.accepted', projectId: 'prj_1' });
    await waitFor(() => expect(r.$('[data-testid="plan"]')!.getAttribute('data-status')).toBe('ACCEPTED'), r, 3000);
    const goal2 = byLabel<HTMLTextAreaElement>(r.container, 'Goal');
    expect(goal2.closest('details')).toBeNull();
    expect(r.$('[data-testid="plan"]')!.compareDocumentPosition(goal2) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
  });

  it('Accept and start hands its pass to the accepted card the screen remounts, with no second click (X47)', async () => {
    let status = 'PROPOSED';
    const ticks: string[] = [];
    const tick: TickResult = { promoted: ['task_a'], started: ['task_a', 'task_d'], skipped: [], active: 2, capacity: 2, max: 2 };
    const createdTasks = { a: 'task_a', b: 'task_b', c: 'task_c', d: 'task_d' };
    const s = servicesWith({
      agentList: async () => [agent],
      projectdevGet: async () => ({ maxParallel: 2, autoSchedule: false, respond: true }) as DevSettings,
      schedulerTick: async (p) => (ticks.push(p), tick),
      planList: async () => [plan({ status, ...(status === 'ACCEPTED' ? { createdTasks } : {}) })],
      planAccept: async () => ((status = 'ACCEPTED'), plan({ status, createdTasks, start: tick })),
    });
    const r = mount(<PlanScreen />, s);
    await waitFor(() => expect(r.$('[data-testid="plan"]')!.getAttribute('data-status')).toBe('PROPOSED'), r);
    await r.act(() => byText(r.container, 'button', 'Accept and start').click());
    await waitFor(() => expect(r.$('[data-testid="plan"]')!.getAttribute('data-status')).toBe('ACCEPTED'), r, 3000);
    const st = await waitFor(() => must(r.$('[data-testid="plan-started"]')), r);
    expect(st.textContent).toContain('Started 2 tasks. 2 tasks wait:');
    expect(st.textContent).toContain('Task b');
    expect(ticks).toEqual([]);
  });

  it("an Accept and start whose pass failed shows the accepted card and the failure, not 'Not sent' (X47)", async () => {
    let status = 'PROPOSED';
    const createdTasks = { a: 'task_a', b: 'task_b', c: 'task_c', d: 'task_d' };
    const s = servicesWith({
      agentList: async () => [agent],
      projectdevGet: async () => ({ maxParallel: 2, autoSchedule: false, respond: true }) as DevSettings,
      planList: async () => [plan({ status, ...(status === 'ACCEPTED' ? { createdTasks } : {}) })],
      planAccept: async () => ((status = 'ACCEPTED'), plan({ status, createdTasks, startError: 'database is locked' })),
    });
    const r = mount(<PlanScreen />, s);
    await waitFor(() => expect(r.$('[data-testid="plan"]')!.getAttribute('data-status')).toBe('PROPOSED'), r);
    await r.act(() => byText(r.container, 'button', 'Accept and start').click());
    await waitFor(() => expect(r.$('[data-testid="plan"]')!.getAttribute('data-status')).toBe('ACCEPTED'), r, 3000);
    const failed = await waitFor(() => must(r.$('[data-testid="plan-start-failed"]')), r);
    expect(failed.textContent).toContain('The tasks were created; starting them failed: database is locked');
    expect(r.container.textContent).not.toContain('Not sent');
  });

  it('a crew without a planner says so with Add a planner, and the lead plans for now (X45)', async () => {
    forgetPlanner();
    const proposed: unknown[][] = [];
    const lead = { ...agent, id: 'agt_lead', name: 'Lena', role: 'team lead' };
    const s = servicesWith({
      agentList: async () => [{ ...agent, id: 'agt_be', name: 'Dana', role: 'backend' }, lead],
      planPropose: async (...args) => (proposed.push(args), { planId: 'p', runId: 'r', plannerAgentId: 'agt_lead', fallback: true }),
      planList: async () => [],
    });
    const r = mount(<PlanScreen />, s);
    const note = await waitFor(() => must(r.$('[data-testid="plan-no-planner"]')), r);
    expect(note.textContent).toContain('No planner on the crew');
    const add = r.$('[data-testid="plan-add-planner"]') as HTMLAnchorElement;
    expect(add.textContent).toBe('Add a planner');
    expect(new URL(add.href).searchParams.get('describe')).toBe('Plans each feature and writes the spec before anyone codes.');
    // automatic names the agent the core picks: the lead before anyone
    await waitFor(() => expect(byLabel<HTMLElement>(r.container, 'Planner agent').textContent).toContain('Automatic: Lena'), r);
    type(byLabel<HTMLTextAreaElement>(r.container, 'Goal'), 'Add arithmetic');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Propose a plan').click());
    await waitFor(() => expect(proposed).toEqual([['prj_1', 'Add arithmetic', '', '', []]]), r);
  });

  it('an empty crew cannot propose and is pointed at Add a planner (X45)', async () => {
    const r = mount(<PlanScreen />, servicesWith({ agentList: async () => [], planList: async () => [] }));
    const note = await waitFor(() => must(r.$('[data-testid="plan-no-planner"]')), r);
    expect(note.textContent).toContain('A plan needs one agent on the crew.');
    type(byLabel<HTMLTextAreaElement>(r.container, 'Goal'), 'x');
    r.root.flush();
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Propose a plan').disabled).toBe(true);
  });

  it('remembers the planner chosen for the project (X45)', async () => {
    forgetPlanner();
    const crew = [agent, { ...agent, id: 'agt_be', name: 'Dana', role: 'backend' }];
    const first = mount(<PlanScreen />, servicesWith({ agentList: async () => crew, planList: async () => [] }));
    await waitFor(() => byLabel<HTMLElement>(first.container, 'Planner agent'), first);
    type(byLabel<HTMLElement>(first.container, 'Planner agent'), 'agt_be');
    first.root.flush();
    cleanup();
    const proposed: unknown[][] = [];
    const r = mount(
      <PlanScreen />,
      servicesWith({ agentList: async () => crew, planList: async () => [], planPropose: async (...args) => (proposed.push(args), { planId: 'p', runId: 'r', plannerAgentId: 'agt_be' }) }),
    );
    await waitFor(() => expect(valueOf(byLabel(r.container, 'Planner agent'))).toBe('agt_be'), r);
    type(byLabel<HTMLTextAreaElement>(r.container, 'Goal'), 'x');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Propose a plan').click());
    await waitFor(() => expect(proposed).toEqual([['prj_1', 'x', '', 'agt_be', []]]), r);
    forgetPlanner();
  });

  it("plans from the project's requirement documents chosen as chips (X48)", async () => {
    const proposed: unknown[][] = [];
    const doc = (id: string, title: string, archived = false) => ({ id, projectId: 'prj_1', title, archived }) as never;
    const r = mount(
      <PlanScreen />,
      servicesWith({
        agentList: async () => [agent],
        planList: async () => [],
        requirementList: async () => [doc('req_a', 'Calculator PRD'), doc('req_b', 'Security notes'), doc('req_c', 'Old', true)],
        planPropose: async (...args) => (proposed.push(args), { planId: 'p', runId: 'r', plannerAgentId: 'agt_pm' }),
      }),
    );
    await waitFor(() => expect(r.$$('[data-testid="plan-req-chip"]').length).toBe(2), r);
    const chip = byText<HTMLButtonElement>(r.container, '[data-testid="plan-req-chip"]', 'Calculator PRD');
    expect(chip.getAttribute('aria-pressed')).toBe('false');
    await r.act(() => chip.click());
    expect(byText(r.container, '[data-testid="plan-req-chip"]', 'Calculator PRD').getAttribute('aria-pressed')).toBe('true');
    expect(r.$('[data-testid="plan-req-note"]')!.textContent).toBe('Accepted tasks are linked to it in the trace.');
    type(byLabel<HTMLTextAreaElement>(r.container, 'Goal'), 'Add arithmetic');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Propose a plan').click());
    await waitFor(() => expect(proposed).toEqual([['prj_1', 'Add arithmetic', '', '', ['req_a']]]), r);
  });

  it('read-only turns Propose off', async () => {
    const r = mount(
      <PlanScreen />,
      servicesWith({
        licenseStatus: async () => expiredLicense,
        planList: async () => [],
      }),
    );
    type(byLabel<HTMLTextAreaElement>(r.container, 'Goal'), 'x');
    await waitFor(() => {
      const b = byText<HTMLButtonElement>(r.container, 'button', 'Propose a plan');
      if (!b.disabled || !b.title.includes('read-only')) throw new Error('not yet');
    }, r);
  });
});
