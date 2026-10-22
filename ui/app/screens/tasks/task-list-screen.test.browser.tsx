import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Task, TaskSummary } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, type, waitFor } from '../../test/render.tsx';
import { SchedulerPanel } from './scheduler-panel.tsx';
import { TaskCreateScreen } from './task-create-screen.tsx';
import { TaskListScreen } from './task-list-screen.tsx';
import { recorder, svc, task } from './testing.ts';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem('drydock.tasks.view');
  } catch {
    /* ignore */
  }
});

const status = { settings: { maxParallel: 2, autoSchedule: false, respond: true, e2e: { enabled: false }, preview: { enabled: false } }, activeRuns: 0, agentSlotsInUse: 0, peakAgentSlots: 1 } as never;

const NOW = Date.now();

// TEST DOUBLES (labelled): task.summaries rows as the core writes them, one per step of the board
const sum = (over: Partial<TaskSummary>): TaskSummary => ({
  taskId: 't',
  projectId: 'prj_1',
  projectKey: 'GOC',
  seq: 1,
  title: 'T',
  status: 'IN_PROGRESS',
  labels: [],
  criteria: 0,
  step: 'code',
  stepIndex: 1,
  stepCount: 5,
  attempt: 1,
  maxAttempts: 5,
  checks: [],
  pendingCount: 0,
  updatedAt: NOW - 60_000,
  ...over,
});

const pending = (phase: string, requestId = 'req_1', runId = 'run_1') => ({ kind: phase === 'spec' || phase === 'merge' ? 'review' : phase, phase, title: `Request ${phase}`, requestId, runId, createdAt: NOW - 60_000 });

const allPassed = ['build', 'test', 'lint', 'typecheck', 'secrets', 'deps', 'ci', 'preview'].map((kind) => ({ kind, status: 'passed' }));

const SUMMARIES: TaskSummary[] = [
  sum({ taskId: 'gate', seq: 41, title: 'Add a changelog entry', status: 'WAITING_HUMAN', step: 'spec', stepIndex: 0, criteria: 4, template: 'Feature', labels: ['docs'], assignee: 'a1', startedAt: NOW - 120_000, pending: pending('spec', 'req_spec'), pendingCount: 1 }),
  sum({ taskId: 'draft', seq: 44, title: 'Support hex input', status: 'IN_PROGRESS', step: 'spec', stepIndex: 0, assignee: 'a1', presence: 'Working', startedAt: NOW - 30_000 }),
  sum({ taskId: 'next', seq: 46, title: 'Write the README', status: 'BACKLOG', step: 'spec', stepIndex: 0 }),
  sum({ taskId: 'build', seq: 43, title: 'Parse negative exponents', step: 'code', stepIndex: 1, attempt: 3, assignee: 'a1', presence: 'Working', doing: 'Running go test ./...', startedAt: NOW - 587_000, diff: { files: 2, insertions: 48, deletions: 6 }, labels: ['parser'] }),
  sum({
    taskId: 'check',
    seq: 40,
    title: 'Divide by zero says so',
    step: 'code',
    stepIndex: 1,
    assignee: 'a1',
    presence: 'Working',
    startedAt: NOW - 100_000,
    checks: [
      { kind: 'test', status: 'passed' },
      { kind: 'lint', status: 'passed' },
      { kind: 'typecheck', status: 'passed' },
      { kind: 'secrets', status: 'passed' },
      { kind: 'deps', status: 'passed' },
      { kind: 'ci', status: 'running' },
      { kind: 'preview', status: 'failed', reason: 'the page did not start on port 5173' },
    ],
  }),
  sum({ taskId: 'merge', seq: 38, title: 'Cover the empty case', status: 'VERIFICATION', step: 'you', stepIndex: 4, assignee: 'a1', reviewer: 'a2', verdict: 'SHIP', checks: allPassed, diff: { files: 2, insertions: 31, deletions: 2 }, pending: pending('merge', 'req_merge', 'run_38'), pendingCount: 1, startedAt: NOW - 900_000 }),
  sum({ taskId: 'done', seq: 37, title: 'Trim spaces in input', status: 'COMPLETED', step: 'done', stepIndex: 5, completedAt: NOW - 60_000, startedAt: NOW - 120_000, assignee: 'a1' }),
  sum({ taskId: 'gone', seq: 36, title: 'Try a faster parser', status: 'FAILED', step: 'code', stepIndex: 1, completedAt: NOW - 7_200_000 }),
];

const LEAD_TASKS: Task[] = [task({ id: 'next', seq: 46, title: 'Write the README', origin: 'lead:2' })];

type Opts = Parameters<typeof svc>[0];

function board(client: Opts = {}, opts: Parameters<typeof svc>[1] = {}, list: TaskSummary[] = SUMMARIES, extra: { partial?: boolean; total?: number } = {}) {
  const taskSummaries = recorder(async () => ({ summaries: list, total: extra.total ?? list.length, partial: extra.partial ?? false }));
  const taskCount = recorder(async (_p: string, statuses?: string[]) => ({ total: statuses?.length ? 6 : list.length }));
  const navigated: string[] = [];
  const s = svc(
    {
      schedulerStatus: async () => status,
      taskSummaries,
      taskCount: taskCount as never,
      presenceList: async () => [{ agentId: 'a1', presence: 'Working', taskId: 'build' } as never],
      ...client,
    },
    { tasks: LEAD_TASKS, ...opts },
  );
  const r = mount(<TaskListScreen onNavigate={(h) => void navigated.push(h)} />, s);
  return { s, r, taskSummaries, taskCount, navigated };
}

const cards = (r: ReturnType<typeof mount>) => Array.from(r.$$('[data-testid="task-row"]')) as HTMLElement[];
const card = (r: ReturnType<typeof mount>, id: string) => r.$(`[data-testid="task-row"][data-task-id="${id}"]`) as HTMLElement;
const column = (r: ReturnType<typeof mount>, name: string) => r.$(`[data-testid="board-column"][data-column="${name}"]`) as HTMLElement;
const idsIn = (el: Element) => Array.from(el.querySelectorAll('[data-testid="task-row"]')).map((c) => c.getAttribute('data-task-id'));

async function shown(r: ReturnType<typeof mount>, n: number) {
  await waitFor(() => {
    if (cards(r).length !== n) throw new Error(`have ${cards(r).length}`);
  }, r);
}

/** The menu's popover surface is showing (role="menu" is the list inside it). */
const menuOpen = (r: ReturnType<typeof mount>) => Array.from(r.$$('[role="menu"]')).some((m) => m.closest('[popover]')?.matches(':popover-open'));

/** Open a menu by its trigger (a menu still finishing its last selection ignores a click, so it is repeated until it shows). */
async function openMenu(r: ReturnType<typeof mount>, trigger: HTMLElement) {
  await r.act(() => trigger.click());
  await waitFor(() => {
    if (!menuOpen(r) && trigger.getAttribute('aria-expanded') !== 'true') trigger.click();
    expect(menuOpen(r)).toBe(true);
  }, r);
}

async function pick(r: ReturnType<typeof mount>, trigger: HTMLElement, selector: string) {
  await openMenu(r, trigger);
  const item = r.$(selector) as HTMLElement;
  await r.act(() => item.click());
  await waitFor(() => expect(menuOpen(r)).toBe(false), r);
}

const key = (k: string, target: EventTarget = document.body, init: KeyboardEventInit = {}) =>
  target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }));

describe('TaskListScreen: the board', () => {
  it('has the bar of the mockup: the project, Board / List / Timeline, the filters, the crew line and New task (C)', async () => {
    const { r, taskSummaries } = board();
    await shown(r, 8);
    expect(r.$('h1')!.textContent).toContain('gocalc');
    const tabs = Array.from(r.$$('[role="tab"]')).map((x) => [x.textContent, x.getAttribute('data-testid'), x.getAttribute('aria-selected')]);
    expect(tabs).toEqual([
      ['Board', 'task-view-board', 'true'],
      ['List', 'task-view-list', 'false'],
      ['Timeline', 'task-view-timeline', 'false'],
    ]);
    expect(r.$('[data-testid="filter-agent"]')!.textContent).toBe('Agent');
    expect(r.$('[data-testid="filter-label"]')!.textContent).toBe('Label');
    expect(r.$('[data-testid="filter-needs-me"]')!.getAttribute('aria-pressed')).toBe('false');
    // one agent works (its presence), two tasks wait for the person (the spec gate and the merge)
    await waitFor(() => expect(r.$('[data-testid="crew-line"]')!.textContent).toBe('1 agent working · 2 waiting on you'), r);
    const add = byText(r.container, 'a', /New task/);
    expect(add.getAttribute('href')).toBe('/tasks/new');
    expect(add.getAttribute('aria-keyshortcuts')).toBe('C');
    expect(add.querySelector('kbd')!.textContent).toBe('C');
    expect(taskSummaries.calls[0]).toEqual([{ projectId: 'prj_1' }]);
  });

  it('five columns from the steps, each with its glyph, name and count; Merged counts this week; failed tasks under Stopped', async () => {
    const { r, taskCount } = board();
    await shown(r, 8);
    const cols = Array.from(r.$$('[data-testid="board-column"]'));
    expect(cols.map((c) => c.getAttribute('aria-label'))).toEqual(['Spec', 'Building', 'Checks', 'Review', 'Merged']);
    expect(cols.map((c) => c.querySelector('h2 svg')?.getAttribute('aria-hidden'))).toEqual(['true', 'true', 'true', 'true', 'true']);
    expect(idsIn(column(r, 'spec'))).toEqual(['gate', 'draft', 'next']);
    expect(idsIn(column(r, 'building'))).toEqual(['build']);
    // a pass under way: Checks
    expect(idsIn(column(r, 'checks'))).toEqual(['check']);
    expect(idsIn(column(r, 'review'))).toEqual(['merge']);
    expect(idsIn(column(r, 'merged'))).toEqual(['done', 'gone']);
    expect(Array.from(column(r, 'merged').querySelectorAll('[data-testid="stopped"] [data-testid="task-row"]')).map((c) => c.getAttribute('data-task-id'))).toEqual(['gone']);
    expect(card(r, 'gone').getAttribute('data-column')).toBe('stopped');
    expect(card(r, 'gone').textContent).toContain('Failed');
    expect(r.$('[data-testid="stopped-head"]')!.textContent).toContain('Stopped1');
    const counts = cols.map((c) => c.querySelector('[data-testid="column-count"]')!.textContent);
    await waitFor(() => expect(column(r, 'merged').querySelector('[data-testid="column-count"]')!.textContent).toBe('6 this week'), r);
    expect(counts.slice(0, 4)).toEqual(['3', '1', '1', '1']);
    // the week's count is the core's: completed tasks since seven days ago
    const week = taskCount.calls.find((c) => (c[1] as string[] | undefined)?.length);
    expect(week![1]).toEqual(['COMPLETED']);
    const since = (week as unknown as [string, string[], { completedSince: number }])[2].completedSince;
    expect(Math.abs(Date.now() - 7 * 86_400_000 - since)).toBeLessThan(60_000);
  });

  it('a card is a link to its task, with its key, its tag and its title', async () => {
    const { r } = board();
    await shown(r, 8);
    const c = card(r, 'gate');
    expect(c.querySelector('h3 a')!.getAttribute('href')).toBe('/task/gate');
    expect(c.querySelector('h3 a')!.textContent).toBe('Add a changelog entry');
    expect(c.querySelector('.key')!.textContent).toBe('GOC-41');
    expect(c.querySelector('[data-testid="card-tag"]')!.textContent).toBe('Waiting on you');
    expect(c.getAttribute('data-waiting')).toBe('true');
    expect(card(r, 'merge').querySelector('[data-testid="card-tag"]')!.textContent).toBe('Ready to merge');
    expect(card(r, 'merge').getAttribute('data-waiting')).toBe('true');
    expect(card(r, 'build').querySelector('[data-testid="card-tag"]')!.getAttribute('data-kind')).toBe('elapsed');
    expect(card(r, 'build').querySelector('[data-testid="card-tag"]')!.textContent).toMatch(/^9:4[78]$/);
    expect(card(r, 'build').getAttribute('data-waiting')).toBeNull();
    expect(card(r, 'next').querySelector('[data-testid="card-tag"]')!.textContent).toBe('Backlog');
    expect(card(r, 'done').getAttribute('data-column')).toBe('merged');
    expect(card(r, 'done').textContent).toContain('Today');
    expect(card(r, 'done').querySelector('[data-testid="card-tag"]')).toBeNull();
  });

  it('says what each step has to say: the spec gate, the drafting agent, the doing line on its try with progress, the checks and their failure, the verdict', async () => {
    const { r } = board();
    await shown(r, 8);
    // Spec: the gate, the template and the labels; the planner drafting; an unassigned task in the backlog
    expect(card(r, 'gate').querySelector('[data-testid="spec-gate"]')!.textContent).toBe('Spec gate: 4 acceptance criteria written. Accept, edit or send back with feedback.');
    expect(Array.from(card(r, 'gate').querySelectorAll('.chips > span')).map((x) => x.textContent)).toEqual(['Feature', 'docs']);
    expect(card(r, 'gate').querySelector('.who')).toBeNull();
    expect(card(r, 'draft').querySelector('.who')!.textContent).toBe('Dana is drafting');
    expect(card(r, 'draft').querySelector('.who [data-dot="true"]')!.getAttribute('data-letters')).toBe('D');
    expect(card(r, 'next').querySelector('.who')!.textContent).toBe('Not assigned yet');
    // Building: what the agent does on which try, how far along, who it is, the diff
    const b = card(r, 'build');
    expect(b.querySelector('[data-testid="doing"]')!.textContent).toBe('Running go test ./... · 3rd try');
    const bar = b.querySelector('[role="progressbar"]')!;
    expect(bar.getAttribute('aria-valuenow')).toBe('20');
    expect(bar.getAttribute('aria-label')).toBe('GOC-43 progress');
    expect(b.querySelector('.who .name')!.textContent).toBe('Dana · Claude Code');
    expect(b.querySelector('.who .chip')!.textContent).toBe('+48\u00a0−6');
    expect(b.querySelector('[data-testid="checks-bar"]')).toBeNull();
    // Checks: the bar of the kinds the project runs, the failing check's reason, the agent fixing it
    const c = card(r, 'check');
    const checks = c.querySelector('[data-testid="checks-bar"]')!;
    expect(checks.getAttribute('role')).toBe('img');
    expect(checks.getAttribute('aria-label')).toBe('Checks: 5 passed, 1 running, 1 failed');
    expect(Array.from(checks.querySelectorAll('span')).map((x) => [x.getAttribute('data-segment'), x.getAttribute('data-state')])).toEqual([
      ['tests', 'passed'],
      ['lint', 'passed'],
      ['types', 'passed'],
      ['secrets', 'passed'],
      ['deps', 'passed'],
      ['ci', 'running'],
      ['preview', 'failed'],
    ]);
    expect(c.querySelector('[data-testid="failing-check"]')!.textContent).toBe('preview: the page did not start on port 5173');
    expect(c.querySelector('.who .name')!.textContent).toBe('Dana is fixing it');
    // Review: every check passed, the verdict with the files and the diff, Merge and View diff
    const m = card(r, 'merge');
    expect(m.querySelector('[data-testid="checks-bar"]')!.getAttribute('aria-label')).toBe('Checks: 7 passed');
    expect(m.querySelector('[data-testid="verdict"]')!.textContent).toBe('Rex approved · 2 files · +31\u00a0−2');
    expect(byText(m, 'button', 'Merge GOC-38').textContent).toBe('Merge');
    expect(byText(m, 'a', 'View diff of GOC-38').getAttribute('href')).toBe('/task/merge');
    expect(byText(m, 'a', 'View diff of GOC-38').textContent).toBe('View diff');
    // only the ready-to-merge card has buttons to act on; nothing else is decided from the board
    expect(Array.from(r.$$('[data-testid="task-board"] button')).map((x) => x.textContent?.trim()).filter(Boolean)).toEqual(['Merge']);
  });

  it('Merge answers the pending merge request with the run and request ids, as the review panel does, then reads again', async () => {
    const runRespond = recorder(async () => undefined);
    const { r, taskSummaries } = board({ runRespond });
    await shown(r, 8);
    const before = taskSummaries.calls.length;
    await r.act(() => byText(card(r, 'merge'), 'button', 'Merge GOC-38').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run_38', 'req_merge', 'approved']]), r);
    await waitFor(() => expect(taskSummaries.calls.length).toBeGreaterThan(before), r);
  });

  it('shows why a merge failed, and keeps Merge off while read-only', async () => {
    const { r } = board({ runRespond: async () => Promise.reject(new Error('the request was already answered')) });
    await shown(r, 8);
    await r.act(() => byText(card(r, 'merge'), 'button', 'Merge GOC-38').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('the request was already answered'), r);
    cleanup();
    const ro = board({}, { license: expiredLicense });
    await shown(ro.r, 8);
    await waitFor(() => expect(byText<HTMLButtonElement>(card(ro.r, 'merge'), 'button', 'Merge GOC-38').disabled).toBe(true), ro.r);
  });

  it('View diff opens the task with the diff in the dock', async () => {
    const { r, s } = board();
    await shown(r, 8);
    const link = byText(card(r, 'merge'), 'a', 'View diff of GOC-38');
    link.addEventListener('click', (e) => e.preventDefault());
    await r.act(() => link.click());
    expect(s.ui.dock.open).toBe(true);
    expect(s.ui.dock.tab).toBe('diff');
  });

  it('filters by agent, by label and to what needs the person', async () => {
    const list = SUMMARIES.map((x) => (x.taskId === 'check' ? { ...x, assignee: 'a2' } : x));
    const { r } = board({}, {}, list);
    await shown(r, 8);
    await pick(r, r.$('[data-testid="filter-agent"]') as HTMLElement, '[role="menu"][aria-label="Agent"] [role="menuitemradio"][data-value="a2"]');
    // Rex works on one and reviews another
    await waitFor(() => expect(cards(r).map((c) => c.getAttribute('data-task-id'))).toEqual(['check', 'merge']), r);
    expect(r.$('[data-testid="filter-agent"]')!.textContent).toContain('Agent: Rex');
    expect(r.$('[data-testid="filter-agent"]')!.getAttribute('data-on')).toBe('true');
    await pick(r, r.$('[data-testid="filter-agent"]') as HTMLElement, '[role="menu"][aria-label="Agent"] [role="menuitemradio"][data-value=""]');
    await shown(r, 8);
    // the labels the tasks carry
    await openMenu(r, r.$('[data-testid="filter-label"]') as HTMLElement);
    expect(Array.from(r.$$('[role="menu"][aria-label="Label"] [role="menuitemradio"]')).map((x) => x.textContent!.trim())).toEqual(['Any label', 'docs', 'parser']);
    await r.act(() => (r.$('[role="menu"][aria-label="Label"] [role="menuitemradio"][data-value="parser"]') as HTMLElement).click());
    await waitFor(() => expect(cards(r).map((c) => c.getAttribute('data-task-id'))).toEqual(['build']), r);
    await pick(r, r.$('[data-testid="filter-label"]') as HTMLElement, '[role="menu"][aria-label="Label"] [role="menuitemradio"][data-value=""]');
    await shown(r, 8);
    // Needs me: the spec gate and the merge
    await r.act(() => (r.$('[data-testid="filter-needs-me"]') as HTMLElement).click());
    await waitFor(() => expect(cards(r).map((c) => c.getAttribute('data-task-id'))).toEqual(['gate', 'merge']), r);
    expect(r.$('[data-testid="filter-needs-me"]')!.getAttribute('aria-pressed')).toBe('true');
    await r.act(() => (r.$('[data-testid="filter-needs-me"]') as HTMLElement).click());
    await shown(r, 8);
  });

  it('Needs me also takes a task whose run asks through Needs you, without a request of its own', async () => {
    const needsYouList = async () => [{ key: 'k1', kind: 'question', severity: 2, title: 'Which API?', taskId: 'build', createdAt: '' }] as never;
    const { r } = board({ needsYouList });
    await shown(r, 8);
    await waitFor(() => expect(card(r, 'build').querySelector('[data-testid="card-tag"]')!.textContent).toBe('Waiting on you'), r);
    await r.act(() => (r.$('[data-testid="filter-needs-me"]') as HTMLElement).click());
    await waitFor(() => expect(cards(r).map((c) => c.getAttribute('data-task-id')).sort()).toEqual(['build', 'gate', 'merge']), r);
    expect(r.$('[data-testid="crew-line"]')!.textContent).toBe('1 agent working · 3 waiting on you');
  });

  it('C opens a new task, but not while typing, with a modifier, or in a menu', async () => {
    const { r, navigated } = board();
    await shown(r, 8);
    key('c');
    expect(navigated).toEqual(['/tasks/new']);
    key('c', document.body, { ctrlKey: true });
    key('c', document.body, { metaKey: true });
    const input = document.createElement('input');
    r.container.append(input);
    key('c', input);
    expect(navigated).toEqual(['/tasks/new']);
    key('C');
    expect(navigated).toEqual(['/tasks/new', '/tasks/new']);
  });

  it('marks the tasks the team lead split off, on the board and in the list', async () => {
    const { r } = board();
    await shown(r, 8);
    await waitFor(() => expect(card(r, 'next').querySelector('[data-testid="task-origin"]')!.textContent).toBe('Split off by the team lead'), r);
    expect(card(r, 'next').querySelector('[data-testid="task-origin"]')!.getAttribute('title')).toBe('Step 2 from a task you made');
    expect(r.$$('[data-testid="task-origin"]').length).toBe(1);
    await r.act(() => (r.$('[data-testid="task-view-list"]') as HTMLElement).click());
    await waitFor(() => expect(r.$('[data-testid="task-list"]')).toBeTruthy(), r);
    expect(card(r, 'next').querySelector('[data-testid="task-origin"]')!.textContent).toBe('Split off by the team lead');
    expect(r.$$('[data-testid="task-origin"]').length).toBe(1);
  });

  it('edits a card\'s labels from its menu, checking them before anything is sent', async () => {
    const taskLabelsSet = recorder(async () => task({ id: 'build' }));
    const { r, taskSummaries } = board({ taskLabelsSet });
    await shown(r, 8);
    const more = card(r, 'build').querySelector('button[aria-label="More for GOC-43"]') as HTMLElement;
    await pick(r, more, '[role="menu"][aria-label="More for GOC-43"] [role="menuitem"]');
    await waitFor(() => expect(r.$('dialog[open]')).toBeTruthy(), r);
    expect(r.$('dialog h2')!.textContent).toBe('Labels of GOC-43');
    const field = byLabel(r.$('dialog')!, 'Labels');
    expect(field.value).toBe('parser');
    // the project's other labels are one click away
    await r.act(() => byText(r.$('dialog')!, 'button', 'Add docs').click());
    expect(byLabel(r.$('dialog')!, 'Labels').value).toBe('parser, docs');
    await r.act(() => type(byLabel(r.$('dialog')!, 'Labels'), 'a,b,c,d,e,f,g,h,i'));
    await r.act(() => byText(r.$('dialog')!, 'button', 'Save labels').click());
    await waitFor(() => expect(r.$('dialog')!.textContent).toContain('A task takes up to 8 labels; this is 9.'), r);
    expect(byLabel(r.$('dialog')!, 'Labels').getAttribute('aria-invalid')).toBe('true');
    expect(taskLabelsSet.calls).toEqual([]);
    await r.act(() => type(byLabel(r.$('dialog')!, 'Labels'), 'Parser, API'));
    const before = taskSummaries.calls.length;
    await r.act(() => byText(r.$('dialog')!, 'button', 'Save labels').click());
    await waitFor(() => expect(taskLabelsSet.calls).toEqual([['build', ['parser', 'api']]]), r);
    await waitFor(() => expect(r.$('dialog[open]')).toBeNull(), r);
    await waitFor(() => expect(taskSummaries.calls.length).toBeGreaterThan(before), r);
  });

  it('reads again on the task, run, presence and Needs you events of the project, not on another project\'s', async () => {
    const { r, s, taskSummaries } = board();
    await shown(r, 8);
    for (const type of ['task.updated', 'workflow.request_raised', 'room.presence_changed', 'needsyou.ack']) {
      const before = taskSummaries.calls.length;
      s.emit({ type, projectId: 'prj_1', taskId: 'build' });
      await waitFor(() => expect(taskSummaries.calls.length).toBeGreaterThan(before), r, 3000);
    }
    const before = taskSummaries.calls.length;
    s.emit({ type: 'task.updated', projectId: 'prj_other' });
    await new Promise((res) => setTimeout(res, 400));
    expect(taskSummaries.calls.length).toBe(before);
  });

  it('says when the list is cut, as the core says (summaries.partial)', async () => {
    const { r } = board({}, {}, SUMMARIES, { partial: true, total: 612 });
    await shown(r, 8);
    const notice = await waitFor(() => r.$('[data-testid="task-total-notice"]')!, r);
    expect(notice.textContent).toContain('Showing the newest 8 of 612 tasks.');
    expect(notice.textContent).toContain('Older tasks are not on this board');
    cleanup();
    const whole = board();
    await shown(whole.r, 8);
    expect(whole.r.$('[data-testid="task-total-notice"]')).toBeNull();
  });

  it('explains what a task is when there are none, and still shows the scheduler', async () => {
    const { r } = board({}, {}, []);
    await waitFor(() => expect(r.container.textContent).toContain('No tasks yet'), r);
    await waitFor(() => expect(r.$('[data-testid="scheduler"]')).toBeTruthy(), r);
    expect(r.$('[data-testid="task-board"]')).toBeNull();
  });

  it('shows the core error when tasks cannot be read', async () => {
    const { r } = board({ taskSummaries: async () => Promise.reject(new Error('database is locked')) });
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('database is locked'), r);
  });
});

describe('TaskListScreen: the list and the timeline', () => {
  it('the list has the same tasks as dense rows in the board\'s order, each a link with its key, stage, tag, agent and labels; it is remembered', async () => {
    const { r } = board();
    await shown(r, 8);
    await r.act(() => (r.$('[data-testid="task-view-list"]') as HTMLElement).click());
    await waitFor(() => expect(r.$('[data-testid="task-list"]')).toBeTruthy(), r);
    expect(r.$('[data-testid="task-board"]')).toBeNull();
    expect(r.$('[data-testid="task-view-list"]')!.getAttribute('aria-selected')).toBe('true');
    expect(cards(r).map((c) => c.getAttribute('data-task-id'))).toEqual(['gate', 'draft', 'next', 'build', 'check', 'merge', 'done', 'gone']);
    const row = card(r, 'build');
    expect(row.querySelector('a')!.getAttribute('href')).toBe('/task/build');
    expect(row.querySelector('.key')!.textContent).toBe('GOC-43');
    expect(row.querySelector('.stage')!.textContent).toBe('Building');
    expect(row.querySelector('.who')!.textContent).toBe('Dana');
    expect(row.querySelector('[data-testid="card-label"]')!.textContent).toBe('parser');
    expect(card(r, 'merge').querySelector('.tag')!.textContent).toBe('Ready to merge');
    expect(card(r, 'gone').querySelector('.stage')!.textContent).toBe('Stopped');
    expect(card(r, 'gone').querySelector('.tag')!.textContent).toBe('Failed');
    expect(localStorage.getItem('drydock.tasks.view')).toBe('list');
    cleanup();
    const again = board();
    await shown(again.r, 8);
    expect(again.r.$('[data-testid="task-view-list"]')!.getAttribute('aria-selected')).toBe('true');
  });

  it('the timeline lays the started tasks along today or this week, one lane per agent, each block a link', async () => {
    const { r } = board();
    await shown(r, 8);
    await r.act(() => (r.$('[data-testid="task-view-timeline"]') as HTMLElement).click());
    const tl = await waitFor(() => r.$('[data-testid="task-timeline"]')!, r);
    expect(r.$('[data-testid="timeline-today"]')!.getAttribute('aria-pressed')).toBe('true');
    // the never-started backlog task is not on it, and that is said
    expect(tl.textContent).toContain('1 task not started yet, so not on the timeline');
    await r.act(() => (r.$('[data-testid="timeline-week"]') as HTMLElement).click());
    await waitFor(() => expect(r.$('[data-testid="timeline-week"]')!.getAttribute('aria-pressed')).toBe('true'), r);
    const lanes = Array.from(r.$$('[data-testid="timeline-lane"]'));
    expect(lanes.map((l) => l.getAttribute('data-agent'))).toEqual(['a1']);
    expect(lanes[0]!.textContent).toContain('Dana');
    const blocks = Array.from(r.$$('[data-testid="timeline-block"]')) as HTMLAnchorElement[];
    expect(blocks.map((b) => b.textContent).sort()).toEqual(['GOC-37', 'GOC-38', 'GOC-40', 'GOC-41', 'GOC-43', 'GOC-44']);
    const merged = blocks.find((b) => b.textContent === 'GOC-37')!;
    expect(merged.getAttribute('href')).toBe('/task/done');
    expect(merged.getAttribute('data-state')).toBe('done');
    expect(merged.getAttribute('aria-label')).toBe('GOC-37 Trim spaces in input, merged');
    expect(blocks.find((b) => b.textContent === 'GOC-38')!.getAttribute('data-state')).toBe('waiting');
  });
});

describe('Scheduler', () => {
  it('shows the scheduler and what one tick did, including why tasks were skipped', async () => {
    const schedulerTick = recorder(() => ({ promoted: ['t1'], started: ['t1'], skipped: [{ taskId: 't2', reason: 'the worker is busy' }], active: 1, capacity: 1, max: 2 }));
    const r = mount(<SchedulerPanel projectId="prj_1" />, svc({ schedulerStatus: async () => status, schedulerTick }));
    await waitFor(() => expect(r.$('[data-testid="scheduler"]')!.textContent).toContain('manual'), r);
    expect(r.$('[data-testid="scheduler"]')!.textContent).toContain('up to 2 tasks in parallel');
    await r.act(() => byText(r.container, 'button', 'Start ready tasks now').click());
    await waitFor(() => expect(r.$('[data-testid="tick-result"]')).toBeTruthy(), r);
    const out = r.$('[data-testid="tick-result"]')!;
    expect(out.textContent).toContain('Started 1');
    expect(out.textContent).toContain('the worker is busy');
    expect(out.querySelector('a')!.getAttribute('href')).toBe('/task/t2');
    expect(schedulerTick.calls).toEqual([['prj_1']]);
  });

  it('disables the scheduler tick when read-only and leaves reading alone', async () => {
    const r = mount(<SchedulerPanel projectId="prj_1" />, svc({ schedulerStatus: async () => ({ settings: { autoSchedule: false, maxParallel: 0 }, activeRuns: 0, agentSlotsInUse: 0, peakAgentSlots: 0 }) as never }, { license: expiredLicense }));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Start ready tasks now').disabled).toBe(true);
    await waitFor(() => expect(r.$('[data-testid="scheduler"]')!.textContent).toContain('the default number of'), r);
  });
});

describe('TaskCreateScreen', () => {
  it('creates the task with trimmed title and one criterion per line, then hands over the new task', async () => {
    const taskCreate = recorder(() => task({ id: 't9' }));
    const created: Task[] = [];
    const r = mount(<TaskCreateScreen onCreated={(x) => created.push(x)} />, svc({ taskCreate }));
    await waitFor(() => expect(byLabel(r.container, 'Task title')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Create task').disabled).toBe(true);
    await r.act(() => type(byLabel(r.container, 'Task title'), '  Add Multiply  '));
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Description'), 'Add a Multiply function.'));
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Acceptance criteria'), 'Given 3 and 4 When Multiply Then 12\n\n  Given 0 When Multiply Then 0 '));
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Create task').disabled).toBe(false), r);
    await r.act(() => byText(r.container, 'button', 'Create task').click());
    await waitFor(() => expect(created.map((x) => x.id)).toEqual(['t9']), r);
    expect(taskCreate.calls).toEqual([
      [{ projectId: 'prj_1', title: 'Add Multiply', description: 'Add a Multiply function.', acceptance: ['Given 3 and 4 When Multiply Then 12', 'Given 0 When Multiply Then 0'] }],
    ]);
    expect(byText(r.container, 'a', 'Cancel').getAttribute('href')).toBe('/tasks');
  });

  it('shows the core refusal and stays on the form', async () => {
    const r = mount(<TaskCreateScreen onCreated={() => undefined} />, svc({ taskCreate: async () => Promise.reject(new Error('title is required')) }));
    await waitFor(() => expect(byLabel(r.container, 'Task title')).toBeTruthy(), r);
    await r.act(() => type(byLabel(r.container, 'Task title'), 'x'));
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Create task').disabled).toBe(false), r);
    await r.act(() => byText(r.container, 'button', 'Create task').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('Task not created: title is required'), r);
    expect(byLabel<HTMLInputElement>(r.container, 'Task title').value).toBe('x');
  });
});
