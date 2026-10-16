import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Plan, ProposedTask } from '../../api/extTypes.ts';
import type { Agent } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { PlanReview, PlanScreen } from './plan-screen.tsx';

afterEach(cleanup);

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

describe('PlanReview', () => {
  it('accepting sends exactly the selected, dependency-closed tasks and only the edits to them', async () => {
    const calls: [string, string[], Record<string, ProposedTask>][] = [];
    let done = 0;
    const s = testServices({
      planAccept: async (id, accept, edits) => (calls.push([id, accept ?? [], edits ?? {}]), {}),
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
    await waitFor(() => byText(r.container, '[role="option"]', 'Priya (pm)'), r);
    type(byLabel<HTMLTextAreaElement>(r.container, 'Goal'), 'Add arithmetic');
    r.root.flush();
    await r.act(() => byText(r.container, 'button', 'Propose a plan').click());
    await waitFor(() => expect(proposed).toEqual([['prj_1', 'Add arithmetic', '', '']]), r);
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
      agentList: async () => [agent],
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
    await waitFor(() => expect(proposed).toEqual([['prj_1', 'More', 'no deps', 'agt_pm']]), r);
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
