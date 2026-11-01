import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Task } from '../../api/types.ts';
import { budgetStatus } from '../../test/fixtures.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { closeAgentEditor } from '../team/agent-panel.tsx';
import { WorkScreen } from './work-screen.tsx';

afterEach(cleanup);

const now = Date.now();
const task = (over: Partial<Task>): Task => ({ id: 't1', projectId: 'prj_1', seq: 1, title: 'Task', description: '', status: 'IN_PROGRESS', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: now, updated: now, version: 1, ...over });
const never = () => new Promise<never>(() => {});

function svc(over: Partial<Client> = {}) {
  const calls: unknown[] = [];
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'calculator', hasRepo: true }] as never,
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    agentList: async () => [
      { id: 'agt1', projectId: 'prj_1', name: 'Team Lead', adapter: 'claude', role: 'lead', model: 'claude-opus-5-5', effort: 'high', providerId: '', permissionMode: '', status: '' },
      { id: 'agt2', projectId: 'prj_1', name: 'Backend Engineer', adapter: 'claude', role: 'backend', model: '', effort: 'medium', providerId: '', permissionMode: '', status: '' },
    ],
    roomList: async () => [],
    roomUnread: async () => [],
    presenceList: async () => [{ agentId: 'agt2', presence: 'Working', label: 'Working', activity: '' }] as never,
    needsYouList: async () => [{ key: 'n1', kind: 'review', phase: 'merge', severity: 1, title: 'Merge?', taskId: 't9', createdAt: '', context: { hasDiff: true, files: 2, insertions: 42, deletions: 3, checksPassed: 0, checksTotal: 0 } }] as never,
    taskList: async () => [
      task({ id: 't12', title: 'Currency converter page', assignee: 'agt2' }),
      task({ id: 't9', title: 'Add Multiply function', status: 'REVIEW' }),
      task({ id: 't8', title: 'Fix divide by zero', status: 'COMPLETED', completed: now - 60_000 }),
    ],
    chatList: async (p) => {
      calls.push(p);
      return [{ id: 'c1', projectId: 'prj_1', title: 'How is rounding done?', status: 'idle', updatedAt: now - 60_000, createdAt: now - 60_000 }] as never;
    },
    budgetStatus: async () => ({ statuses: [budgetStatus()], usage: { project: { rows: 3, reportedUsd: 0.84, estimatedUsd: 0, unknownCostRows: 0, inputTokens: 0, outputTokens: 0, cachedTokens: 0, turns: 3 } }, note: '' }),
    evalsReport: async () => ({ markdown: '', report: { quality: { firstCheckTestPass: { num: 7, den: 8, rate: 0.875 } } } }) as never,
    securityScanGet: async () => null as never,
    // the centre's item: its own reads never answer here (the task page and the chat are tested on their own)
    taskGet: never,
    taskSummaries: never,
    usageList: never,
    connectionList: never,
    adapterList: async () => [],
    chatGet: never,
    ...over,
  });
  s.workspace.start();
  s.project.start();
  return Object.assign(s, { calls });
}

const rows = (r: ReturnType<typeof mount>) =>
  Array.from(r.$$('[data-testid="work-item"]')).map((a) => [a.getAttribute('data-group'), a.querySelector('.title')!.textContent, a.querySelector('.line')!.textContent]);

describe('Work (decision P141)', () => {
  it("lists the project's tasks and chats as Needs you, Running and Earlier, opens the one that needs you, and shows the crew", async () => {
    const s = svc();
    const r = mount(<WorkScreen />, s);
    await waitFor(() => expect(r.$$('[data-testid="work-item"]')).toHaveLength(4), r);
    expect(s.calls).toEqual([{ projectId: 'prj_1' }]);
    expect(rows(r)).toEqual([
      ['needs', 'Add Multiply function', 'Ready to merge · +42 −3'],
      ['running', 'Currency converter page', 'Backend Engineer · now'],
      ['earlier', 'How is rounding done?', 'Chat · 1 min'],
      ['earlier', 'Fix divide by zero', 'Merged · 1 min'],
    ]);
    expect(Array.from(r.$$('[data-testid="work-list"] h2')).map((h) => h.textContent)).toEqual(['Needs you', 'Running', 'Earlier']);
    // nothing named in the URL: the first that needs you is open, and marked in the list
    expect(r.$('[data-testid="work-centre"]')!.getAttribute('data-item')).toBe('task:t9');
    expect(r.$('[data-item="task:t9"][data-testid="work-item"]')!.getAttribute('aria-current')).toBe('true');
    expect(r.$('[data-testid="work-item"]')!.getAttribute('href')).toBe('/?item=task%3At9');
    // the crew: name, model and effort, and a click opens the agent's editor; "+" adds one
    const crew = Array.from(r.$$('[data-testid="work-agent"]')).map((a) => [a.querySelector('.name')!.firstChild!.textContent, a.querySelector('.line')!.textContent, a.getAttribute('href')]);
    expect(crew).toEqual([
      ['Team Lead', 'Opus 5.5 · high', '/team?edit=agt1'],
      ['Backend Engineer', 'Default model · medium', '/team?edit=agt2'],
    ]);
    expect(r.$('[data-testid="work-add-agent"]')!.getAttribute('href')).toBe('/team?new=agent');
    await waitFor(() => expect(r.$('[data-testid="work-spend"] b')!.textContent).toBe('$0.84'), r);
    await waitFor(() => expect(r.$('[data-testid="work-checks"] b')!.textContent).toBe('88%'), r);
    expect(r.$('[data-testid="work-security"] b')!.textContent).toBe('not scanned');
    expect(r.$('[data-testid="work-merged"] b')!.textContent).toBe('1');
    // the old pages are a click away, with the old test ids
    expect(Array.from(r.$$('nav[aria-label="More of this project"] a')).map((a) => [a.getAttribute('data-testid'), a.getAttribute('href')])).toEqual([
      ['nav-tasks', '/tasks'],
      ['nav-pulls', '/pulls'],
      ['nav-rooms', '/rooms'],
    ]);
  });

  it('opens the item the URL names: a chat as its conversation, a task as its page', async () => {
    const r = mount(<WorkScreen item="chat:c1" />, svc());
    await waitFor(() => expect(r.$$('[data-testid="work-item"]')).toHaveLength(4), r);
    expect(r.$('[data-testid="work-centre"]')!.getAttribute('data-item')).toBe('chat:c1');
    expect(r.$('[data-item="chat:c1"][data-testid="work-item"]')!.getAttribute('aria-current')).toBe('true');
    expect(r.$$('[aria-current="true"]')).toHaveLength(1);
    cleanup();
    const t = mount(<WorkScreen item="task:t12" />, svc());
    await waitFor(() => expect(t.$('[data-testid="work-centre"]')!.textContent).toContain('Loading task'), t);
    expect(t.$('[data-testid="work-centre"]')!.getAttribute('data-item')).toBe('task:t12');
  });

  it('with nothing running or waiting: one line and Describe a task; the search narrows the list', async () => {
    const r = mount(<WorkScreen />, svc({ needsYouList: async () => [], taskList: async () => [task({ id: 't8', title: 'Fix divide by zero', status: 'COMPLETED' })] }));
    await waitFor(() => expect(r.$$('[data-testid="work-item"]')).toHaveLength(2), r);
    expect(r.$('[data-testid="work-empty"]')!.textContent).toBe('Describe a task and the crew takes it from there.Describe a task');
    expect(r.$('[data-testid="work-empty"] a')!.getAttribute('href')).toBe('/tasks/new');
    const box = r.$('[data-testid="work-search"]') as HTMLInputElement;
    expect(box.getAttribute('aria-label')).toBe('Search this project');
    await r.act(() => {
      box.value = 'divide';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(rows(r).map((x) => x[1])).toEqual(['Fix divide by zero']);
    await r.act(() => {
      box.value = 'qqq';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(r.$$('[data-testid="work-item"]')).toHaveLength(0);
    expect(r.$('[data-testid="work-list"]')!.textContent).toBe('Nothing matches "qqq".');
  });

  it('starts a chat in the project and opens it in the centre', async () => {
    const made: unknown[] = [];
    const went: string[] = [];
    const s = svc({
      chatCreate: async (p) => {
        made.push(p);
        return { id: 'c2' } as never;
      },
    });
    const r = mount(<WorkScreen onNavigate={(h) => went.push(h)} />, s);
    await waitFor(() => expect(r.$$('[data-testid="work-item"]')).toHaveLength(4), r);
    await r.act(() => (r.$('[data-testid="work-new-chat"]') as HTMLButtonElement).click());
    await waitFor(() => expect(went).toEqual(['/?item=chat%3Ac2']), r);
    expect(made).toEqual([{ projectId: 'prj_1' }]);
  });

  it("a click on a crew agent opens its editor in place (P137's panel), not the Crew page", async () => {
    const went: string[] = [];
    const r = mount(<WorkScreen onNavigate={(h) => went.push(h)} />, svc({ adapterList: never }));
    await waitFor(() => expect(r.$$('[data-testid="work-agent"]')).toHaveLength(2), r);
    expect(r.$('[data-testid="agent-editor"]')).toBeNull();
    await r.act(() => (r.$('[data-testid="work-agent"][data-name="Backend Engineer"]') as HTMLAnchorElement).click());
    await waitFor(() => expect(r.$('[data-testid="agent-editor"]')!.getAttribute('aria-label')).toBe('Edit Backend Engineer'), r);
    expect(went).toEqual([]);
    closeAgentEditor();
  });

  it('shows once, as one line, the note a start from Home left (P142)', async () => {
    const s = svc();
    s.ui.flash('calculator: The GitHub repository could not be made.', 'warn');
    const r = mount(<WorkScreen />, s);
    await waitFor(() => expect(r.$$('[data-testid="work-item"]')).toHaveLength(4), r);
    expect(r.$('[data-testid="work-flash"]')!.textContent).toBe('calculator: The GitHub repository could not be made.');
    expect(s.ui.takeFlash()).toBeUndefined();
  });
});
