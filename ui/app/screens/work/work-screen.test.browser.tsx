import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Connection, Task } from '../../api/types.ts';
import { budgetStatus } from '../../test/fixtures.ts';
import { cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { SETUP_NOTES_KEY } from '../../lib/setup-notes.ts';
import { adapterInfo, CATALOG } from '../project/wizard-fixtures.ts';
import { closeAgentEditor } from '../team/agent-panel.tsx';
import { WorkScreen } from './work-screen.tsx';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem(SETUP_NOTES_KEY);
  } catch {
    /* nothing kept */
  }
});

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
    // the start box of the empty centre (B11)
    agentCatalogList: async () => CATALOG,
    githubCloneDir: async () => '',
    chatLastChoice: async () => undefined as never,
    ...over,
  });
  s.workspace.start();
  s.project.start();
  return Object.assign(s, { calls });
}

const rows = (r: ReturnType<typeof mount>) =>
  Array.from(r.$$('[data-testid="work-item"]')).map((a) => [a.getAttribute('data-group'), a.querySelector('.title')!.textContent, a.querySelector('.line')!.textContent]);

describe('Work (decision P141)', () => {
  it('says when the lead has recommendations waiting, with Review to the Skills page; nothing when none wait', async () => {
    const item = (id: string, status = 'open') => ({ id, kind: 'skill', action: 'give', name: 'wrangler', label: 'wrangler', agents: ['agt2'], reason: 'why', status });
    const run = { id: 'rec1', projectId: 'prj_1', agentId: 'agt1', trigger: 'evolution', status: 'done', candidates: [], summary: '', items: [item('a'), item('b'), item('c'), item('d', 'applied')], dropped: [], costUsd: 0, createdAt: 1 };
    const r = mount(<WorkScreen />, svc({ recommendList: async () => ({ auto: true, runs: [run] }) }));
    const line = await waitFor(() => r.$('[data-testid="recommend-line"]') ?? (() => { throw new Error('no line yet'); })(), r);
    expect(line.textContent).toContain('Team Lead recommends 3 changes');
    expect(line.querySelector('[data-testid="recommend-line-review"]')!.getAttribute('href')).toBe('/skills');
    cleanup();
    const none = mount(<WorkScreen />, svc({ recommendList: async () => ({ auto: true, runs: [{ ...run, items: [item('a', 'dismissed')] }] }) }));
    await waitFor(() => expect(none.$$('[data-testid="work-item"]').length).toBeGreaterThan(0), none);
    expect(none.$('[data-testid="recommend-line"]')).toBeNull();
  });

  it('says when memory proposals wait, with Review to the Memory page; nothing when none wait (E10)', async () => {
    const entry = (id: string, status: string) => ({ id, projectId: 'prj_1', kind: 'decision', title: id, body: 'x', status, source: 'agent:agt1', created: 1, updated: 1 });
    const r = mount(<WorkScreen />, svc({ memoryList: async (_p: string, status?: string) => [entry('m1', 'proposed'), entry('m2', 'proposed'), entry('m3', 'accepted')].filter((e) => e.status === status) }));
    const line = await waitFor(() => r.$('[data-testid="memory-line"]') ?? (() => { throw new Error('no line yet'); })(), r);
    expect(line.textContent).toContain('2 memory proposals');
    expect(line.querySelector('[data-testid="memory-line-review"]')!.getAttribute('href')).toBe('/memory');
    cleanup();
    const none = mount(<WorkScreen />, svc({ memoryList: async () => [] }));
    await waitFor(() => expect(none.$$('[data-testid="work-item"]').length).toBeGreaterThan(0), none);
    expect(none.$('[data-testid="memory-line"]')).toBeNull();
  });

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

  it("puts the rooms' unread count on the Rooms link, so an answer is seen from Work (X18)", async () => {
    const r = mount(
      <WorkScreen />,
      svc({
        roomUnread: async () => [
          { roomId: 'r1', roomName: 'general', kind: 'channel', unread: 2, mentions: 0, muted: false, lastReadSeq: 0 },
          { roomId: 'r2', roomName: 'dm', kind: 'dm', unread: 1, mentions: 0, muted: false, lastReadSeq: 0 },
        ],
      }),
    );
    await waitFor(() => expect(r.$('[data-testid="nav-rooms-unread"]')?.textContent).toBe('3, 3 unread'), r);
    expect(r.$('[data-testid="nav-rooms"]')!.textContent).toBe('Rooms3, 3 unread');
    cleanup();
    const none = mount(<WorkScreen />, svc());
    await waitFor(() => expect(none.$$('[data-testid="work-item"]').length).toBeGreaterThan(0), none);
    expect(none.$('[data-testid="nav-rooms-unread"]')).toBeNull();
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

  it("with nothing running or waiting: Home's start box for this project, the task form a link; the search narrows the list", async () => {
    const r = mount(<WorkScreen />, svc({ needsYouList: async () => [], taskList: async () => [task({ id: 't8', title: 'Fix divide by zero', status: 'COMPLETED' })] }));
    await waitFor(() => expect(r.$$('[data-testid="work-item"]')).toHaveLength(2), r);
    const empty = r.$('[data-testid="work-empty"]')!;
    expect(empty.querySelector('p')!.textContent).toBe('Describe what the crew should do first.');
    expect(empty.querySelector('[data-testid="start-box"]')).not.toBeNull();
    // Where is this project: no Where chip and no Or row
    expect(empty.querySelector('[data-testid="start-where"]')).toBeNull();
    expect(empty.querySelector('[data-testid="start-or"]')).toBeNull();
    expect(r.$('[data-testid="work-task-form"]')!.getAttribute('href')).toBe('/tasks/new');
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

  it('the empty centre takes one description and Enter: a crew that cannot run a task gets a chat in this project (B11)', async () => {
    const made: unknown[] = [];
    const sent: string[] = [];
    const went: string[] = [];
    const s = svc({
      needsYouList: async () => [],
      taskList: async () => [],
      chatList: async () => [],
      chatCreate: async (p) => (made.push(p), { id: 'c7' }) as never,
      chatSend: async (_id, text) => (sent.push(text), { turnId: 't', version: 1 }),
    });
    const r = mount(<WorkScreen onNavigate={(h) => went.push(h)} />, s);
    const box = await waitFor(() => {
      const el = r.$('[data-testid="work-empty"] [data-testid="start-text"]');
      if (!el) throw new Error('no box yet');
      return el as HTMLTextAreaElement;
    }, r);
    await waitFor(() => expect(r.$('[data-testid="start-who"]')!.textContent).toContain('2 agents'), r);
    await r.act(() => type(box, 'Add a Divide function'));
    await r.act(() => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })));
    await waitFor(() => expect(went).toEqual(['/?item=chat:c7']), r);
    expect((made[0] as { projectId: string }).projectId).toBe('prj_1');
    expect(sent).toEqual(['Add a Divide function']);
  });

  it('shows what failed when the project was made as a line each with its fix, until dismissed or resolved (B2)', async () => {
    localStorage.setItem(
      SETUP_NOTES_KEY,
      JSON.stringify({
        prj_1: [
          { kind: 'cli', text: 'Backend Engineer cannot work yet: the claude CLI is not usable on this machine (unauthenticated).', adapter: 'claude', status: 'unauthenticated' },
          { kind: 'decision', text: 'Backend Engineer: skill wrangler waits for your decision: new source' },
          { kind: 'connector', text: 'connector docs not added: offline' },
        ],
      }),
    );
    // TEST DOUBLE of the kept checks (connection.list {cached}): the CLI line reads these, which start no CLI
    let ready = false;
    let cachedReads = 0;
    const s = svc({
      connectionList: async (cached) => {
        if (cached) cachedReads++;
        const c = { adapter: 'claude', key: 'claude', runMode: 'native', installed: true, platform: 'windows' };
        return [(ready ? { ...c, status: 'ready', usable: true } : { ...c, status: 'unauthenticated', usable: false }) as Connection];
      },
    });
    const r = mount(<WorkScreen />, s);
    const lines = () => Array.from(r.$$('[data-testid="setup-note"]')) as HTMLElement[];
    await waitFor(() => expect(lines()).toHaveLength(3), r);
    expect(lines().map((l) => l.dataset.kind)).toEqual(['cli', 'decision', 'connector']);
    expect(lines()[0]!.querySelector('[data-testid="setup-note-fix"]')!.textContent).toBe('Sign in');
    expect(lines()[1]!.querySelector('a')!.getAttribute('href')).toBe('/needs-you');
    expect(lines()[1]!.querySelector('a')!.textContent).toBe('Decide');
    expect(lines()[2]!.querySelector('a')!.getAttribute('href')).toBe('/project/integrations');
    await r.act(() => (lines()[2]!.querySelector('[data-testid="setup-note-dismiss"]') as HTMLButtonElement).click());
    expect(lines()).toHaveLength(2);
    expect(JSON.parse(localStorage.getItem(SETUP_NOTES_KEY)!).prj_1).toHaveLength(2);
    // signed in elsewhere: the CLI line goes by itself
    ready = true;
    s.emit({ type: 'connection.sign_in_finished' });
    await waitFor(() => expect(lines().map((l) => l.dataset.kind)).toEqual(['decision']), r);
    expect(JSON.parse(localStorage.getItem(SETUP_NOTES_KEY)!).prj_1).toHaveLength(1);
    expect(cachedReads).toBeGreaterThan(0);
  });

  it('keeps a CLI line while the CLI is ready but its notice is not accepted (B2)', async () => {
    localStorage.setItem(SETUP_NOTES_KEY, JSON.stringify({ prj_1: [{ kind: 'cli', text: 'Ada cannot work yet: accept the notice of mycli', adapter: 'mycli', status: 'ready' }] }));
    const gated = { adapter: 'mycli', key: 'mycli', runMode: 'native', installed: true, platform: 'windows', status: 'ready', usable: false, gates: [{ id: 'g', text: 'Terms', acked: false }] } as Connection;
    const s = svc({ connectionList: async () => [gated], adapterList: async () => [adapterInfo('mycli')] });
    const r = mount(<WorkScreen />, s);
    await waitFor(() => expect(r.$$('[data-testid="setup-note"]')).toHaveLength(1), r);
    s.emit({ type: 'connection.checked' });
    await new Promise((res) => setTimeout(res, 50));
    await r.act(() => {});
    expect(r.$$('[data-testid="setup-note"]')).toHaveLength(1);
    expect(JSON.parse(localStorage.getItem(SETUP_NOTES_KEY)!).prj_1).toHaveLength(1);
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

  it('says where the folder stands against GitHub in one line, and Sync now updates it (P143)', async () => {
    const synced: string[] = [];
    const state = { projectId: 'prj_1', remote: 'origin', github: true, branch: 'main', current: 'main', tracked: true, ahead: 0, behind: 2, checkedAt: now, checking: false };
    const s = svc({
      repoSyncState: async () => state,
      repoSync: async (id) => (synced.push(id), { ...state, behind: 1, outcome: 'local_changes' }),
    });
    const r = mount(<WorkScreen />, s);
    const line = await waitFor(() => {
      const el = r.$('[data-testid="repo-sync-state"]');
      if (!el) throw new Error('no sync line');
      return el;
    }, r);
    expect(line.textContent).toBe('2 behind');
    // under the list, which every width keeps: not in the crew rail, which leaves under 1180 px
    expect(r.$('[data-testid="repo-sync"]')!.closest('aside[data-crew]')).toBeNull();
    expect(r.$('[data-testid="repo-sync"]')!.closest('section')!.querySelector('[data-testid="work-list"]')).not.toBeNull();
    await r.act(() => (r.$('[data-testid="repo-sync-now"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('[data-testid="repo-sync-why"]')!.textContent).toBe('Not updated: the folder has local changes.'), r);
    expect(synced).toEqual(['prj_1']);
    expect(r.$('[data-testid="repo-sync-state"]')!.textContent).toBe('1 behind');
    // a repo.synced event reads it again
    state.behind = 0;
    await r.act(() => s.emit({ type: 'repo.synced', projectId: 'prj_1' }));
    await waitFor(() => expect(r.$('[data-testid="repo-sync-state"]')!.textContent).toBe('Up to date with GitHub'), r);
  });

  it('shows no sync line for a repository without a remote', async () => {
    const s = svc({ repoSyncState: async () => ({ projectId: 'prj_1', remote: '', github: false, branch: '', current: '', tracked: false, ahead: 0, behind: 0, checkedAt: 0, checking: false }) });
    const r = mount(<WorkScreen />, s);
    await waitFor(() => expect(r.$$('[data-testid="work-item"]')).toHaveLength(4), r);
    await new Promise((res) => setTimeout(res, 50));
    expect(r.$('[data-testid="repo-sync"]')).toBeNull();
  });
});
