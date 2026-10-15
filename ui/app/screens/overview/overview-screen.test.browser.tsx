import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Room, Task } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { budgetStatus } from '../../test/fixtures.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { OverviewScreen } from './overview-screen.tsx';

afterEach(cleanup);

const DAY = 86_400_000;
const now = Date.now();
const room = (over: Partial<Room>): Room => ({ id: 'r1', projectId: 'prj_1', kind: 'channel', name: 'general', mode: 'auto', archived: false, createdAt: '', version: 1, ...over });
const task = (over: Partial<Task>): Task => ({ id: 't1', projectId: 'prj_1', seq: 1, title: 'Task', description: '', status: 'IN_PROGRESS', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: now, updated: now, version: 1, ...over });
const report = (over: Record<string, unknown>) => ({ quality: { started: 8, accepted: 7, failed: 1, cancelled: 0, active: 0, completion: { num: 7, den: 8, rate: 0.875 }, firstCheckTestPass: { num: 7, den: 8, rate: 0.875 }, finalTestPass: { num: 7, den: 8, rate: 0.875 } }, ...over });

function svc(over: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc', hasRepo: true }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    agentList: async () => [
      { id: 'agt1', projectId: 'prj_1', name: 'Ada', adapter: 'claude', role: 'backend', model: '', effort: '', providerId: '', permissionMode: '', status: '' },
      { id: 'agt2', projectId: 'prj_1', name: 'Rex', adapter: 'claude', role: 'reviewer', model: '', effort: '', providerId: '', permissionMode: '', status: '' },
    ],
    roomList: async () => [room({}), room({ id: 'r2', kind: 'task', name: 'task-12-webhooks', taskId: 't12' })],
    roomUnread: async () => [],
    presenceList: async () => [{ agentId: 'agt1', presence: 'working', label: 'Working', activity: 'Writing the retry tests' }] as never,
    needsYouList: async () => [{ key: 'n1', kind: 'approval', severity: 1, title: 'Approve the merge of #9', detail: 'Rex found the tests green.', agentId: 'agt2', taskId: 't9' }] as never,
    taskList: async () => [
      task({ id: 't12', seq: 12, title: 'Retry webhooks', assignee: 'agt1' }),
      task({ id: 't9', seq: 9, title: 'Idempotent refunds', status: 'COMPLETED', completed: now - 60_000 }),
      task({ id: 't8', seq: 8, title: 'PDF invoices', status: 'COMPLETED', completed: now - 3 * DAY }),
      task({ id: 't2', seq: 2, title: 'Old one', status: 'COMPLETED', completed: now - 20 * DAY }),
      task({ id: 't1', seq: 1, title: 'Someday', status: 'BACKLOG' }),
    ],
    budgetStatus: async () => ({ statuses: [budgetStatus()], usage: { project: { rows: 3, reportedUsd: 2.5, estimatedUsd: 0, unknownCostRows: 0, inputTokens: 0, outputTokens: 0, cachedTokens: 0, turns: 3 } }, note: '' }),
    evalsReport: async () => ({ markdown: '', report: report({}) }),
    securityScanGet: async () => ({ id: 's1', projectId: 'prj_1', dir: '', status: 'done', by: 'human:kyle', startedAt: now, engines: [], findings: [], counts: { critical: 0, high: 1, medium: 1, low: 0, info: 0, total: 2 } }),
    ...over,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

const text = (r: { $: (s: string) => Element | null }, id: string) => r.$(`[data-testid="${id}"]`)!.textContent!.replace(/\s+/g, ' ').trim();

describe('the Overview', () => {
  it('greets with the numbers of the day, what needs you, the crew, what is in motion and the rooms', async () => {
    const r = mount(<OverviewScreen />, svc());
    await waitFor(() => expect(r.$$('[data-testid="overview-need"]')).toHaveLength(1), r);
    expect(r.$('[data-testid="overview"]')).not.toBeNull();
    expect(r.$('h1')!.textContent).toMatch(/^Good (morning|afternoon|evening)$/);
    expect(r.container.textContent).toContain('gocalc · 1 agent working · 1 merged today · 1 thing needs you');
    // needs you: the first items, each a link to where it is decided
    const need = r.$('[data-testid="overview-need"]')!;
    expect(need.getAttribute('data-kind')).toBe('approval');
    expect(need.textContent).toContain('Approve the merge of #9');
    expect(need.querySelector('a')!.textContent).toContain('Open and decide');
    // the crew as dots with what each is doing
    await waitFor(() => expect(r.$$('[data-testid="overview-agent"]')).toHaveLength(2), r);
    const ada = r.$('[data-testid="overview-agent"][data-name="Ada"]')!;
    expect(ada.querySelector('[data-dot="true"]')).not.toBeNull();
    expect(ada.textContent).toContain('Writing the retry tests');
    expect(r.$('[data-testid="overview-agent"][data-name="Rex"]')!.textContent).toContain('Resting');
    expect(r.$('[data-testid="overview-crew"] a[href="/team"]')).not.toBeNull();
    // in motion: the open tasks that are not in the backlog
    const moving = Array.from(r.$$('[data-testid="overview-task"]'));
    expect(moving.map((x) => x.getAttribute('href'))).toEqual([routes.task.href({ id: 't12' })]);
    expect(moving[0]!.textContent).toContain('Retry webhooks');
    // today
    expect(text(r, 'overview-merged')).toBe('1');
    // the rooms card is the e2e helpers' way to a room
    const rooms = r.$('[data-rooms]')!;
    expect(Array.from(rooms.querySelectorAll('a[data-testid="room-link"]')).map((a) => a.getAttribute('href'))).toEqual([routes.room.href({ id: 'r1' }), routes.room.href({ id: 'r2' })]);
    expect(rooms.querySelector('a[href="/rooms"]')).not.toBeNull();
  });

  it('Insights: the spend against the budget, what merged this week, the first-check rate and the last scan, each a link', async () => {
    const r = mount(<OverviewScreen />, svc());
    await waitFor(() => expect(text(r, 'overview-spend')).toContain('$2.50'), r);
    const spend = r.$('[data-testid="overview-spend"]')!;
    expect(spend.textContent).toContain('of $5 budget');
    expect(spend.getAttribute('href')).toBe(routes.budgets.href());
    expect(spend.querySelector('[role="progressbar"]')!.getAttribute('aria-valuenow')).toBe('50');
    const merged = r.$('[data-testid="overview-merged-week"]')!;
    expect(merged.querySelector('b')!.textContent).toBe('2');
    expect(merged.textContent).toContain('tasks completed this week');
    expect(merged.getAttribute('href')).toBe(routes.tasks.href());
    await waitFor(() => expect(r.$('[data-testid="overview-first-check"] b')!.textContent).toBe('88%'), r);
    expect(r.$('[data-testid="overview-first-check"]')!.getAttribute('href')).toBe(routes.evals.href());
    await waitFor(() => expect(r.$('[data-testid="overview-findings"] b')!.textContent).toBe('2'), r);
    const findings = r.$('[data-testid="overview-findings"]')!;
    expect(findings.textContent).toContain('2 findings in the last scan');
    expect(findings.getAttribute('href')).toBe(routes.security.href());
    expect(r.$('[data-testid="overview-insights"] [role="alert"]')).toBeNull();
  });

  it('Insights without history: no scan, no budget and no checked task read as such, never as zero', async () => {
    const r = mount(
      <OverviewScreen />,
      svc({
        budgetStatus: async () => ({ statuses: [], usage: {}, note: '' }),
        evalsReport: async () => ({ markdown: '', report: report({ quality: undefined }) }),
        securityScanGet: async () => null,
        taskList: async () => [],
        needsYouList: async () => [],
      }),
    );
    await waitFor(() => expect(text(r, 'overview-findings')).toContain('no scan yet'), r);
    expect(r.$('[data-testid="overview-findings"] b')!.textContent).toBe('–');
    await waitFor(() => expect(text(r, 'overview-spend')).toContain('no budget set'), r);
    expect(r.$('[data-testid="overview-spend"] b')!.textContent).toBe('$0.00');
    expect(r.$('[data-testid="overview-spend"] [role="progressbar"]')).toBeNull();
    await waitFor(() => expect(r.$('[data-testid="overview-first-check"] b')!.textContent).toBe('n/a'), r);
    expect(text(r, 'overview-first-check')).toContain('no task checked in 7 days');
    expect(r.$('[data-testid="overview-merged-week"] b')!.textContent).toBe('0');
    expect(r.$('[data-testid="overview-needs"]')!.textContent).toContain('Nothing needs you right now.');
    expect(r.$('[data-testid="overview-motion"]')!.textContent).toContain('Nothing is running.');
  });

  it('a read that fails is said in the Insights card, and the rest of the page stands', async () => {
    const r = mount(
      <OverviewScreen />,
      svc({
        securityScanGet: async () => {
          throw new Error('the scan store is locked');
        },
      }),
    );
    await waitFor(() => expect(r.$('[data-testid="overview-insights"] [role="alert"]')!.textContent).toContain('the scan store is locked'), r);
    await waitFor(() => expect(text(r, 'overview-spend')).toContain('$2.50'), r);
    expect(r.$$('[data-testid="overview-agent"]').length).toBe(2);
  });
});
