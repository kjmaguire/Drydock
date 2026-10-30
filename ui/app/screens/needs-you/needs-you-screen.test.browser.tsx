import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ApprovalDetail } from '../../api/inboxClient.ts';
import type { RunningScan } from '../../api/securityTypes.ts';
import type { NeedsYouItem, Task, TaskSummary } from '../../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { NeedsYouScreen } from './needs-you-screen.tsx';
import { startOfToday } from './needs-you.ts';

afterEach(cleanup);

// TEST DOUBLES of the core's answers: two projects, a worker's permission request and a spec gate in one, a failed check in the other
const NOW = Date.now();
const iso = (ms: number) => new Date(ms).toISOString();
const permission: NeedsYouItem = {
  key: 'approval:apr1', kind: 'approval', severity: 0, title: 'Permission request: Bash: curl https://example.com', projectId: 'p1', taskId: 't1', taskSeq: 41,
  agentId: 'a1', agentName: 'Free hand', approvalId: 'apr1', createdAt: iso(NOW - 2 * 60_000), expiresAt: iso(NOW + 58 * 60_000 + 30_000),
};
const specGate: NeedsYouItem = { key: 'approval:apr2', kind: 'review', phase: 'spec', severity: 2, title: 'Accept the spec: Add a changelog entry', projectId: 'p1', taskId: 't2', taskSeq: 42, agentName: 'Planner', approvalId: 'apr2', createdAt: iso(NOW - 6 * 60_000) };
const failed: NeedsYouItem = { key: 'check:m1', kind: 'failed_check', severity: 1, title: 'Tests failed: 2 of 9', projectId: 'p2', roomId: 'r1', taskId: 't3', taskSeq: 7, createdAt: iso(NOW - 60 * 60_000) };

const summary = (over: Partial<TaskSummary>): TaskSummary =>
  ({ taskId: 't', projectId: 'p1', projectKey: 'GOC', seq: 1, title: 't', status: 'IN_PROGRESS', labels: [], criteria: 0, step: 'code', stepIndex: 1, stepCount: 5, attempt: 1, maxAttempts: 3, checks: [], pendingCount: 0, updatedAt: NOW, ...over }) as TaskSummary;
const summaries = [
  summary({ taskId: 't2', seq: 42, title: 'Add a changelog entry', status: 'WAITING_HUMAN', template: 'feature', criteria: 4 }),
  summary({ taskId: 't4', projectId: 'p2', projectKey: 'SHP', seq: 12, title: 'Rate limit the checkout API', doing: 'Editing limiter.go', stepIndex: 3, stepCount: 5, startedAt: NOW - 252_000 }),
];
const scan: RunningScan = {
  id: 'scan1', projectId: 'p2', name: 'shop-api', by: 'human:kyle', startedAt: NOW - 3000, counts: { critical: 0, high: 0, medium: 0, low: 0, info: 0, total: 0 },
  engines: [
    { id: 'secrets', name: 'Secrets', tool: 'gitleaks', status: 'done', findings: 0 },
    { id: 'sast', name: 'Code', tool: 'opengrep', status: 'running', findings: 0 },
  ],
} as RunningScan;
const doneTask = { id: 't9', projectId: 'p1', seq: 38, title: 'Cover the empty case', status: 'COMPLETED', completed: NOW - 3_600_000 } as Task;

const permDetail = (over: Partial<ApprovalDetail> = {}): ApprovalDetail => ({
  id: 'apr1', kind: 'approval', actionKind: 'shell_command', gate: 'permission_prompt', title: permission.title,
  description: 'Free hand wants to run curl https://example.com for task 41 (Fetch). Its own rules do not allow this command, so it waits for your answer.',
  tool: 'Bash', input: 'curl https://example.com', shell: true, targets: [], shortened: false, editable: true,
  agent: { id: 'a1', name: 'Free hand', adapter: 'claude', model: 'sonnet' }, projectId: 'p1', projectName: 'gocalc', task: { id: 't1', seq: 41, title: 'Fetch', status: 'IN_PROGRESS' },
  runId: 'run1', worktree: 'gocalc/.drydock/wt/free-hand', createdAt: NOW - 120_000, expiresAt: NOW + 58 * 60_000 + 30_000,
  actionHash: 'f041205c9d0e1b2a3c4d5e6f7a8b9c0da247a', status: 'REQUESTED', approveForTask: true, rules: [], ...over,
});
const specDetail: ApprovalDetail = {
  id: 'apr2', kind: 'review', phase: 'spec', title: specGate.title, description: 'The spec of task 42 (Add a changelog entry) is written. Accept it to start building, or send it back with what to change.',
  shell: false, targets: [], shortened: false, editable: false, projectId: 'p1', runId: 'run2', createdAt: NOW, status: 'REQUESTED', approveForTask: false, rules: [],
  payload: { phase: 'spec', taskId: 't2', title: 'Add a changelog entry', spec: 'words', criteria: [{ id: 'AC1', given: 'a release', when: 'it is cut', then: 'the changelog names it' }] },
};

interface Calls {
  forTask: unknown[][];
  respond: unknown[][];
  ack: string[];
  approvalGet: string[];
  taskList: unknown[][];
}

function servicesWith(over: Partial<Client> = {}, items: NeedsYouItem[] = [permission, specGate, failed]) {
  const calls: Calls = { forTask: [], respond: [], ack: [], approvalGet: [], taskList: [] };
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc', key: 'GOC' }, { id: 'p2', name: 'shop-api', key: 'SHP' }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({ name: 'Drydock', user: 'kyle', human: 'human:kyle', dataDir: '' }),
    needsYouList: async (pid) => (pid === '' ? items : []),
    taskSummaries: async () => ({ summaries, total: summaries.length, partial: false }),
    securityScanRunning: async () => [scan],
    taskList: async (...a) => {
      calls.taskList.push(a);
      return [doneTask];
    },
    approvalGet: async (id) => {
      calls.approvalGet.push(id);
      return id === 'apr2' ? specDetail : permDetail();
    },
    runApproveForTask: async (...a) => void calls.forTask.push(a),
    runRespond: async (...a) => void calls.respond.push(a),
    needsYouAck: async (key) => void calls.ack.push(key),
    ...over,
  });
  s.workspace.start();
  return { s, calls };
}

const press = (key: string, target: EventTarget = document.body) => target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));

async function open(over: Partial<Client> = {}, items?: NeedsYouItem[], props: { tab?: string } = {}) {
  const { s, calls } = servicesWith(over, items);
  const navigated: string[] = [];
  const r = mount(<NeedsYouScreen {...props} onNavigate={(h) => navigated.push(h)} />, s);
  await waitFor(() => {
    if (r.$$('[data-testid="needs-item"]').length !== (items ?? [1, 2, 3]).length) throw new Error('not yet');
    if (!r.$('[data-testid="running-item"]')) throw new Error('no running rows yet');
  }, r);
  return { s, calls, r, navigated };
}

const selectedKey = (r: { $: (s: string) => Element | null }) => r.$('[data-row-key][aria-current="true"]')?.getAttribute('data-row-key');

describe('the Inbox', () => {
  it("lists every project's items with the task's key, a title and a subtitle; the tabs count what is real", async () => {
    const { r, s } = await open();
    expect(r.$('h1')!.textContent).toBe('Inbox');
    expect(r.$('[role="tablist"]')!.getAttribute('aria-label')).toBe('Filter');
    const tabs = Array.from(r.$$('[role="tab"]')).map((x) => x.textContent);
    await waitFor(() => expect(Array.from(r.$$('[role="tab"]')).map((x) => x.textContent)).toEqual(['Needs you 3', 'Running 2', 'Done today 1']), r);
    expect(tabs[0]).toContain('Needs you');
    const rows = Array.from(r.$$('[data-testid="needs-item"]'));
    expect(rows.map((x) => x.getAttribute('data-look'))).toEqual(['approval', 'spec', 'failed_check']);
    expect(rows.map((x) => x.querySelector('[data-testid="task-key"]')?.textContent)).toEqual(['GOC-41', 'GOC-42', 'SHP-7']);
    expect(rows[0]!.querySelector('.title')!.textContent).toBe('GOC-41Free hand asks to use Bash: curl https://example.com');
    expect(rows[0]!.querySelector('[data-testid="needs-sub"]')!.textContent).toBe('gocalc · approval · asked 2 min ago');
    expect(rows[1]!.querySelector('.title')!.textContent).toBe('GOC-42Add a changelog entry: the spec is ready');
    expect(rows[1]!.querySelector('[data-testid="needs-sub"]')!.textContent).toBe('gocalc · spec gate · Feature template · 6 min ago');
    expect(rows[2]!.querySelector('[data-testid="needs-sub"]')!.textContent).toBe('shop-api · failed check · 1 h ago');
    // what runs, under them: the agent's step, a real progress bar and a clock
    const run = r.$('[data-testid="running-item"]')!;
    expect(run.querySelector('.line')!.textContent).toBe('SHP-12Rate limit the checkout API · editing limiter.go');
    expect(run.querySelector('[role="progressbar"]')!.getAttribute('aria-valuenow')).toBe('60');
    expect(run.querySelector('.time')!.textContent).toMatch(/^4:1\d$/);
    const sc = r.$('[data-testid="running-scan"]')!;
    expect(sc.querySelector('.line')!.textContent).toBe('Security scan of shop-api · Code running');
    expect(sc.querySelector('[role="progressbar"]')!.getAttribute('aria-valuenow')).toBe('50');
    // the workspace counts the same lists the sidebar shows
    expect(s.workspace.needsCount).toBe(3);
    expect(s.workspace.runningCount).toBe(2);
    expect(r.$('footer')!.textContent).toBe('A approveD deny↵ openE done');
  });

  it("selects the first item and shows the request as the core describes it: the agent, what it applies to, the worktree, when it lapses, the hash", async () => {
    const { r, calls } = await open();
    expect(selectedKey(r)).toBe('needs:approval:apr1');
    const pane = r.$('[data-testid="inbox-detail"]')!;
    await waitFor(() => expect(pane.querySelector('[data-testid="detail-facts"]')!.textContent).toContain('Free hand · Claude Code · sonnet'), r);
    expect(calls.approvalGet).toEqual(['apr1']);
    expect(pane.querySelector('[data-testid="detail-kind"]')!.textContent).toBe('Approval · gocalc');
    expect(pane.querySelector('h2')!.textContent).toBe('GOC-41Free hand asks to use Bash: curl https://example.com');
    expect(pane.querySelector('[data-testid="detail-description"]')!.textContent).toContain('wants to run curl https://example.com');
    const facts = pane.querySelector('[data-testid="detail-facts"]')!.textContent!;
    expect(facts).toContain('Commandcurl https://example.com');
    expect(facts).toContain('Applies toThis exact call, until GOC-41 ends');
    expect(facts).toContain('Worktreegocalc/.drydock/wt/free-hand');
    expect(facts).toContain('Expiresin 58 min');
    expect(facts).toContain('Action hashf041205c…a247a');
    expect(pane.querySelector('[data-testid="detail-stays"]')!.textContent).toContain('Pushes, merges, paths outside the worktree and secrets are asked every time.');
    expect(pane.querySelector('[data-testid="detail-approve"]')!.textContent).toBe('Approve A');
    expect(pane.querySelector('[data-testid="detail-deny"]')!.textContent).toBe('Deny D');
    expect(pane.querySelector('[data-testid="detail-once"]')!.textContent).toBe('Approve once');
    // the selected row says A answers it
    await waitFor(() => expect(r.$('[data-row-key="needs:approval:apr1"] kbd')?.textContent).toBe('A'), r);
    expect(r.$('[data-row-key="needs:approval:apr2"] kbd')).toBeNull();
  });

  it('A approves the permission request for the rest of its task, Approve once only this request, D denies it', async () => {
    const { r, calls } = await open();
    await waitFor(() => expect(r.$('[data-testid="detail-approve"]')).not.toBeNull(), r);
    await r.act(() => press('a'));
    await waitFor(() => expect(calls.forTask).toEqual([['run1', 'apr1']]), r);
    expect(calls.respond).toEqual([]);
    await waitFor(() => expect(r.$('[role="status"]')!.textContent).toContain('Answer sent'), r);
    // the item stays until the core confirms: another key does not answer it again (audit 2026-10-27)
    await r.act(() => press('d'));
    await r.act(() => press('a'));
    await new Promise((res) => setTimeout(res, 50));
    expect([calls.forTask, calls.respond]).toEqual([[['run1', 'apr1']], []]);
    cleanup();

    const two = await open();
    await waitFor(() => expect(two.r.$('[data-testid="detail-once"]')).not.toBeNull(), two.r);
    await two.r.act(() => (two.r.$('[data-testid="detail-once"]') as HTMLButtonElement).click());
    await waitFor(() => expect(two.calls.respond).toEqual([['run1', 'apr1', 'approved']]), two.r);
    expect(two.calls.forTask).toEqual([]);
    cleanup();

    const three = await open();
    await waitFor(() => expect(three.r.$('[data-testid="detail-deny"]')).not.toBeNull(), three.r);
    await three.r.act(() => press('D'));
    await waitFor(() => expect(three.calls.respond).toEqual([['run1', 'apr1', 'rejected']]), three.r);
  });

  it("a call the core approves only once has Approve (once) and Deny, and says why it cannot be approved for the task", async () => {
    const { r, calls } = await open({ approvalGet: async () => permDetail({ approveForTask: false, approveForTaskNote: 'it pushes or merges, which is asked every time' }) });
    await waitFor(() => expect(r.$('[data-testid="detail-once-note"]')).not.toBeNull(), r);
    expect(r.$('[data-testid="detail-once-note"]')!.textContent).toBe('This call can only be approved once: it pushes or merges, which is asked every time.');
    expect(r.$('[data-testid="detail-once"]')).toBeNull();
    expect(r.$('[data-testid="detail-applies"]')!.textContent).toBe('This request only');
    expect(r.$('[data-testid="detail-stays"]')!.textContent).toContain('The next call asks you again.');
    await r.act(() => press('a'));
    await waitFor(() => expect(calls.respond).toEqual([['run1', 'apr1', 'approved']]), r);
    expect(calls.forTask).toEqual([]);
  });

  it('J and K move the selection; the spec gate shows its criteria and A accepts the spec as written', async () => {
    const { r, calls } = await open();
    await r.act(() => press('j'));
    expect(selectedKey(r)).toBe('needs:approval:apr2');
    await waitFor(() => expect(r.$('[data-testid="detail-criteria"]')!.textContent).toBe('Given a release, when it is cut, then the changelog names it'), r);
    expect(r.$('[data-testid="detail-kind"]')!.textContent).toBe('Spec gate · gocalc');
    expect(r.$('[data-testid="detail-approve"]')!.textContent).toBe('Accept spec A');
    expect(r.$('[data-testid="detail-deny"]')).toBeNull();
    expect(r.$('[data-testid="detail-open"]')!.textContent).toBe('Open to review');
    // D does nothing to a review: sending a spec back needs the feedback, on the task's page
    await r.act(() => press('d'));
    await r.act(() => press('a'));
    await waitFor(() => expect(calls.respond).toEqual([['run2', 'apr2', 'approved']]), r);
    await r.act(() => press('j'));
    await r.act(() => press('j'));
    expect(selectedKey(r)).toBe('task:t4');
    await r.act(() => press('k'));
    expect(selectedKey(r)).toBe('needs:check:m1');
    // a key typed in a field is the field's
    const input = document.createElement('input');
    document.body.append(input);
    await r.act(() => press('k', input));
    input.remove();
    expect(selectedKey(r)).toBe('needs:check:m1');
  });

  it("says the reviewer's verdict in words in the detail, never SHIP (UI audit 2026-10-26)", async () => {
    const reviewed: NeedsYouItem = {
      ...specGate,
      context: { agent: '', hasDiff: true, files: 1, insertions: 1, deletions: 4, checksPassed: 4, checksTotal: 4, verdict: 'SHIP' },
    };
    const { r } = await open({}, [reviewed]);
    const verdict = await waitFor(() => r.$('[data-testid="detail-verdict"]') ?? (() => { throw new Error('no verdict'); })(), r);
    expect(verdict.textContent).toBe('approved');
    expect(r.$('[data-testid="inbox-detail"]')!.textContent!.includes('SHIP')).toBe(false);
  });

  it('E marks a failed check done; a request cannot be (it is answered instead); Enter opens the selected item in its project', async () => {
    const { r, calls, s, navigated } = await open();
    await r.act(() => press('e'));
    expect(calls.ack).toEqual([]);
    expect(r.$('[data-testid="detail-done"]')).toBeNull();
    await r.act(() => (r.$('[data-row-key="needs:check:m1"]') as HTMLButtonElement).click());
    expect(r.$('[data-testid="detail-done"]')!.textContent).toBe('Mark done E');
    await r.act(() => press('e'));
    await waitFor(() => expect(calls.ack).toEqual(['check:m1']), r);
    await r.act(() => press('Enter'));
    expect(navigated).toEqual(['/task/t3']);
    expect(s.workspace.projectId).toBe('p2');
  });

  it("a budget's warning is its own item: it says what to do, opens the Budgets page, and can be marked done (P118)", async () => {
    const warning: NeedsYouItem = {
      key: 'budget:bud1:p2/iterate-until-verified:warning:5', kind: 'budget', severity: 2, title: 'Budget warning: the budget of the workflow iterate-until-verified passed 80%',
      detail: 'Agents keep working. At the limit, the next agent turn waits for you: raise the limit on the Budgets page if this work should go on, or dismiss this.',
      projectId: 'p2', budgetId: 'bud1', budgetState: 'warning', createdAt: iso(NOW - 5 * 60_000),
    };
    const { r, calls, s, navigated } = await open({}, [warning]);
    const row = r.$('[data-testid="needs-item"]')!;
    expect(row.getAttribute('data-kind')).toBe('budget');
    expect(row.getAttribute('data-look')).toBe('budget');
    expect(row.textContent).toContain('shop-api · budget');
    await r.act(() => (r.$('[data-row-key^="needs:budget:"]') as HTMLButtonElement).click());
    // what to do, once (the detail is the hint, not repeated as a description), and no Approve: nothing is answered here
    expect(r.$('[data-testid="needs-hint"]')!.textContent).toContain('raise the limit on the Budgets page');
    expect(r.$('[data-testid="detail-description"]')).toBeNull();
    expect(r.$('[data-testid="detail-approve"]')).toBeNull();
    expect(r.$('[data-testid="detail-open"]')!.textContent).toBe('Open Budgets');
    await r.act(() => press('e'));
    await waitFor(() => expect(calls.ack).toEqual([warning.key]), r);
    await r.act(() => press('Enter'));
    expect(navigated).toEqual(['/budgets']);
    expect(s.workspace.projectId).toBe('p2');
  });

  it('the Running tab lists what runs, the Done today tab the tasks completed since midnight', async () => {
    const { r, calls, s } = await open();
    await r.act(() => (r.$('[data-testid="inbox-tab-running"]') as HTMLButtonElement).click());
    expect(s.ui.inboxTab).toBe('running');
    expect(r.$$('[data-testid="needs-item"]').length).toBe(0);
    expect(r.$$('[data-testid="running-item"]').length).toBe(1);
    expect(r.$$('[data-testid="running-scan"]').length).toBe(1);
    expect(selectedKey(r)).toBe('task:t4');
    expect(r.$('[data-testid="inbox-detail"]')!.textContent).toContain('3 of 5');
    await r.act(() => (r.$('[data-testid="inbox-tab-done"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$$('[data-testid="done-item"]').length).toBe(1), r);
    expect(r.$('[data-testid="done-item"] .title')!.textContent).toBe('GOC-38Cover the empty case');
    expect(r.$('[data-testid="done-item"] .sub')!.textContent).toBe('gocalc · done 1 h ago');
    expect(calls.taskList[0]).toEqual(['', ['COMPLETED'], { completedSince: startOfToday() }]);
  });

  it('opens on the tab its address names', async () => {
    const { s } = servicesWith();
    const r = mount(<NeedsYouScreen tab="running" />, s);
    await waitFor(() => expect(s.ui.inboxTab).toBe('running'), r);
    await waitFor(() => expect(r.$$('[data-testid="running-item"]').length).toBe(1), r);
    expect(r.$$('[data-testid="needs-item"]').length).toBe(0);
    expect(r.$('[data-testid="inbox-tab-running"]')!.getAttribute('aria-selected')).toBe('true');
    cleanup();
    // the address without a tab is Needs you, whatever tab was open last
    const again = mount(<NeedsYouScreen />, s);
    await waitFor(() => expect(s.ui.inboxTab).toBe('needs'), again);
    await waitFor(() => expect(again.$$('[data-testid="needs-item"]').length).toBe(3), again);
  });

  it('says plainly when nothing waits and nothing runs, with or without a project', async () => {
    const { s } = servicesWith({ projectList: async () => [], taskSummaries: async () => ({ summaries: [], total: 0, partial: false }), securityScanRunning: async () => [], taskList: async () => [] }, []);
    const r = mount(<NeedsYouScreen />, s);
    await waitFor(() => expect(r.container.textContent).toContain('Nothing is waiting for you.'), r);
    await waitFor(() => expect(r.container.textContent).toContain('No agent is working on a task and no scan runs.'), r);
    expect(r.$('h1')!.textContent).toBe('Inbox');
    expect(r.$('[data-testid="inbox-detail"]')!.textContent).toBe('Nothing to show here yet.');
  });

  it('reads again when an item is answered elsewhere', async () => {
    let items = [permission, specGate, failed];
    const { r, s } = await open({ needsYouList: async () => items });
    items = [specGate, failed];
    s.emit({ type: 'needsyou.acked', projectId: 'p1' });
    await waitFor(() => expect(r.$$('[data-testid="needs-item"]').length).toBe(2), r);
    // the selection moves to the item now in its place
    expect(selectedKey(r)).toBe('needs:approval:apr2');
    items = [failed];
    s.emit({ type: 'approval.decided', projectId: 'p1' });
    await waitFor(() => expect(r.$$('[data-testid="needs-item"]').length).toBe(1), r);
  });

  it('an agent asking to join a channel is let in or declined from the detail (P130)', async () => {
    const join: NeedsYouItem = {
      key: 'join:jr1', kind: 'join_request', severity: 3, title: 'Asks to join #architecture', detail: 'Why: I record security decisions', projectId: 'p1', roomId: 'r9',
      agentId: 'a3', agentName: 'Sec', joinRequestId: 'jr1', createdAt: iso(NOW - 60_000),
    };
    const answers: unknown[][] = [];
    const { r, calls } = await open({ roomAnswerJoin: async (...a) => (answers.push(a), { id: 'jr1' }) as never }, [join]);
    await r.act(() => (r.$('[data-row-key="needs:join:jr1"]') as HTMLButtonElement).click());
    expect(r.$('[data-testid="detail-description"]')!.textContent).toBe('Why: I record security decisions');
    expect(r.$('[data-testid="needs-hint"]')!.textContent).toContain('Let it in as an observer');
    expect(r.$('[data-testid="detail-open"]')!.textContent).toBe('Open the channel');
    expect(r.$('[data-testid="detail-done"]')).toBeNull();
    await r.act(() => press('e'));
    expect(calls.ack).toEqual([]);
    await r.act(() => (r.$('[data-testid="detail-let-in"]') as HTMLButtonElement).click());
    await waitFor(() => expect(answers).toEqual([['jr1', true]]), r);
  });
});
