// The task page's run controls (the graphite mockup "Task", decision P97): the steer box with Pause and Resume, Hand to another
// agent, the labels, and Open the worktree (O). The client is a labelled test double (testing.ts); nothing imitates an agent.
import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { SteerResult } from '../../api/controlClient.ts';
import type { Task, TaskDetail } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, typeKeys, waitFor } from '../../test/render.tsx';
import { TaskDetailScreen } from './task-detail-screen.tsx';
import { detail, recorder, svc, task } from './testing.ts';

afterEach(cleanup);

const RUN = { id: 'run-1', workflow: 'iterate-until-verified', status: 'RUNNING', topology: '', created: 0, updated: 0, version: 1 };
const running = (over: Partial<TaskDetail> = {}, t: Partial<Task> = {}): TaskDetail =>
  detail({ task: task({ status: 'IN_PROGRESS', iteration: 1, worktree: '/w/t1', branch: 'drydock/t1-add-multiply', started: Date.now() - 5_000, ...t }), run: RUN, ...over });

function need<T>(v: T | null | undefined): T {
  if (!v) throw new Error('not yet');
  return v;
}

const steerInput = (root: ParentNode) => byLabel<HTMLInputElement>(root, 'Message the agent');
const key = (target: EventTarget, k: string, init: KeyboardEventInit = {}) => target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }));

describe('steering the running agent', () => {
  it('Ctrl+Enter sends the message to the agent and says when it reads it; the box empties', async () => {
    const taskSteer = recorder(async (_id: string, _m: string): Promise<SteerResult> => ({ delivered: 'now', messageId: 'msg_1' }));
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => running(), taskSteer }));
    const input = await waitFor(() => steerInput(r.container), r);
    expect(input.disabled).toBe(false);
    expect(input.placeholder).toBe('Steer Dana: it reads this before its next step');
    expect(Array.from(r.$$('[data-testid="steer"] kbd')).map((k) => k.textContent)).toEqual(['Ctrl', '↵']);
    await typeKeys(input, 'Keep the old parser for hex', r);
    await r.act(() => key(input, 'Enter', { ctrlKey: true }));
    await waitFor(() => expect(taskSteer.calls).toEqual([['t1', 'Keep the old parser for hex']]), r);
    await waitFor(() => expect(r.$('[data-testid="steer"] [role="status"]')!.textContent).toBe('Sent. Dana reads it before its next step.'), r);
    expect(steerInput(r.container).value).toBe('');
  });

  it('Enter sends too; a message for the next turn says so; an empty box sends nothing', async () => {
    const taskSteer = recorder(async (): Promise<SteerResult> => ({ delivered: 'next', messageId: 'msg_2' }));
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => running(), taskSteer }));
    const input = await waitFor(() => steerInput(r.container), r);
    await r.act(() => input.form!.requestSubmit());
    expect(taskSteer.calls.length).toBe(0);
    await typeKeys(input, 'use big.Float', r);
    await r.act(() => input.form!.requestSubmit());
    await waitFor(() => expect(taskSteer.calls.length).toBe(1), r);
    await waitFor(() => expect(r.$('[data-testid="steer"] [role="status"]')!.textContent).toBe('Sent. It goes with the next turn.'), r);
  });

  it("shows the core's refusal and keeps the message", async () => {
    const taskSteer = recorder(async (): Promise<SteerResult> => Promise.reject(new Error('nothing is running on task #1')));
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => running(), taskSteer }));
    const input = await waitFor(() => steerInput(r.container), r);
    await typeKeys(input, 'hello', r);
    await r.act(() => key(input, 'Enter', { metaKey: true }));
    await waitFor(() => expect(r.$('[data-testid="steer"] [role="alert"]')!.textContent).toContain('nothing is running on task #1'), r);
    expect(steerInput(r.container).value).toBe('hello');
  });

  it('is off, with the reason, while no run is going', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail() }));
    const input = await waitFor(() => steerInput(r.container), r);
    expect(input.disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.$('[data-testid="steer"]')!, 'button', 'Pause').disabled).toBe(true);
    expect(r.$('[data-testid="steer"]')!.textContent).toContain('Start the task to steer its agent.');
    cleanup();
    const done = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ status: 'COMPLETED' }) }) }));
    await waitFor(() => expect(done.$('[data-testid="steer"]')!.textContent).toContain('The task is closed: there is no run to steer.'), done);
  });
});

describe('pausing and resuming', () => {
  it('Pause stops the run and the page says who paused it; Resume goes on', async () => {
    const pausedDetail = running({ paused: { by: 'human:kyle', at: Date.now() }, run: { ...RUN, status: 'PAUSED' } });
    const taskPause = recorder(async (_id: string) => pausedDetail);
    const taskResume = recorder(async (_id: string) => running());
    let current = running();
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => current, taskPause, taskResume }));
    const steer = await waitFor(() => need(r.$('[data-testid="steer"]')), r);
    await waitFor(() => expect(r.$('[data-testid="task-status"]')!.textContent).toBe('Building · run 2'), r);
    current = pausedDetail;
    await r.act(() => byText(steer, 'button', 'Pause').click());
    await waitFor(() => expect(taskPause.calls).toEqual([['t1']]), r);
    await waitFor(() => expect(r.$('[data-testid="task-status"]')!.textContent).toBe('Paused by kyle · run 2'), r);
    expect(r.$('[data-testid="task-state"]')!.textContent).toBe('Paused by kyle');
    // paused, a message still goes (with the next turn), and the button resumes
    expect(steerInput(r.container).disabled).toBe(false);
    current = running();
    await r.act(() => byText(r.$('[data-testid="steer"]')!, 'button', 'Resume').click());
    await waitFor(() => expect(taskResume.calls).toEqual([['t1']]), r);
    await waitFor(() => expect(r.$('[data-testid="task-status"]')!.textContent).toBe('Building · run 2'), r);
    expect(byText(r.$('[data-testid="steer"]')!, 'button', 'Pause')).toBeTruthy();
  });

  it("shows the core's refusal to pause a run that waits for you", async () => {
    const taskPause = recorder(async (_id: string): Promise<TaskDetail> => Promise.reject(new Error('task #1 is waiting for you: answer it instead')));
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => running(), taskPause }));
    const steer = await waitFor(() => need(r.$('[data-testid="steer"]')), r);
    await r.act(() => byText(steer, 'button', 'Pause').click());
    await waitFor(() => expect(steer.querySelector('[role="alert"]')!.textContent).toContain('waiting for you'), r);
  });
});

describe('handing the task to another agent', () => {
  const openMenu = async (r: ReturnType<typeof mount>) => {
    const trigger = await waitFor(() => byText(r.container, 'button', 'Hand to another agent'), r);
    await r.act(() => trigger.click());
    await waitFor(() => expect(trigger.getAttribute('aria-expanded')).toBe('true'), r);
    return Array.from(r.$$('[role="menuitem"]')) as HTMLElement[];
  };

  it("offers the project's other agents and assigns without naming a reviewer (the core picks one)", async () => {
    const taskAssign = recorder(async (_id: string, _a: string, _rv?: string) => task({ assignee: 'a2' }));
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => running(), taskAssign }));
    const items = await openMenu(r);
    expect(items.map((i) => i.querySelector('.label')!.textContent)).toEqual(['Rex (reviewer)']);
    await r.act(() => items[0]!.click());
    await waitFor(() => expect(taskAssign.calls).toEqual([['t1', 'a2']]), r);
  });

  it("shows the core's refusal", async () => {
    const taskAssign = recorder(async (): Promise<Task> => Promise.reject(new Error('nobody else could review the work')));
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => running(), taskAssign }));
    const items = await openMenu(r);
    await r.act(() => items[0]!.click());
    await waitFor(() => expect(r.$('.task-meta [role="alert"]')!.textContent).toContain('nobody else could review the work'), r);
  });

  it('is not offered on a closed task', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ status: 'COMPLETED' }) }) }));
    await waitFor(() => expect(r.$('.task-meta')).toBeTruthy(), r);
    expect(Array.from(r.$$('button')).some((b) => b.textContent === 'Hand to another agent')).toBe(false);
  });
});

describe('the labels', () => {
  it('adds a label (trimmed, lower-cased) and removes one through task.labels.set', async () => {
    let labels = ['parser'];
    const taskLabelsSet = recorder(async (_id: string, next: string[]) => {
      labels = next;
      return task({ labels: next });
    });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ labels }) }), taskLabelsSet }));
    await waitFor(() => expect(Array.from(r.$$('[data-testid="task-label"]')).map((e) => e.textContent)).toEqual(['parser']), r);
    await r.act(() => byLabel(r.container, 'Edit labels').click());
    const input = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Add a label'), r);
    await typeKeys(input, ' Lexer ', r);
    await r.act(() => input.form!.requestSubmit());
    await waitFor(() => expect(taskLabelsSet.calls).toEqual([['t1', ['parser', 'lexer']]]), r);
    await waitFor(() => expect(byLabel(r.container, 'Remove label lexer')).toBeTruthy(), r);
    await r.act(() => byLabel(r.container, 'Remove label parser').click());
    await waitFor(() => expect(taskLabelsSet.calls[1]).toEqual(['t1', ['lexer']]), r);
    await r.act(() => byLabel(r.container, 'Done with labels').click());
    await waitFor(() => expect(Array.from(r.$$('[data-testid="task-label"]')).map((e) => e.textContent)).toEqual(['lexer']), r);
  });

  it('checks a label on the page and sends nothing when it cannot be kept', async () => {
    const taskLabelsSet = recorder(async (_id: string, next: string[]) => task({ labels: next }));
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ labels: ['parser'] }) }), taskLabelsSet }));
    await waitFor(() => byLabel(r.container, 'Edit labels'), r);
    await r.act(() => byLabel(r.container, 'Edit labels').click());
    const input = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Add a label'), r);
    await typeKeys(input, 'PARSER', r);
    await r.act(() => input.form!.requestSubmit());
    await waitFor(() => expect(r.$('[data-testid="labels-edit"] [role="alert"]')!.textContent).toBe('The task already has the label parser.'), r);
    expect(byLabel(r.container, 'Add a label').getAttribute('aria-invalid')).toBe('true');
    expect(taskLabelsSet.calls.length).toBe(0);
  });
});

describe('Open the worktree (O)', () => {
  it('opens a terminal in the dock in the task worktree, from the button and from O outside a text field', async () => {
    const s = svc({ taskGet: async () => running() });
    const r = mount(<TaskDetailScreen id="t1" />, s);
    const button = await waitFor(() => byText<HTMLButtonElement>(r.container, 'button', /^Open the worktree/), r);
    expect(button.querySelector('kbd')!.textContent).toBe('O');
    expect(button.disabled).toBe(false);
    await r.act(() => button.click());
    expect(s.ui.dock).toEqual({ ...s.ui.dock, open: true, tab: 'terminal' });
    expect(s.ui.takeTerminalAsk()).toEqual({ taskId: 't1' });
    // O on the page
    s.ui.setDock({ open: false, tab: 'logs' });
    await r.act(() => key(document.body, 'o'));
    expect(s.ui.dock.tab).toBe('terminal');
    expect(s.ui.takeTerminalAsk()).toEqual({ taskId: 't1' });
    // never while typing in the steer box, nor with a modifier
    s.ui.setDock({ open: false, tab: 'logs' });
    await r.act(() => key(steerInput(r.container), 'o'));
    await r.act(() => key(document.body, 'o', { ctrlKey: true }));
    expect(s.ui.dock.tab).toBe('logs');
    expect(s.ui.takeTerminalAsk()).toBe(undefined);
  });

  it('is off before the task has a worktree, and O does nothing', async () => {
    const s = svc({ taskGet: async () => detail() });
    const r = mount(<TaskDetailScreen id="t1" />, s);
    const button = await waitFor(() => byText<HTMLButtonElement>(r.container, 'button', /^Open the worktree/), r);
    expect(button.disabled).toBe(true);
    expect(button.getAttribute('title')).toBe('The task has no worktree yet: it gets one when it starts');
    await r.act(() => key(document.body, 'o'));
    expect(s.ui.takeTerminalAsk()).toBe(undefined);
  });
});
