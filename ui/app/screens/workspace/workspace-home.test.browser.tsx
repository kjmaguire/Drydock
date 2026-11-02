import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ChatCreate } from '../../api/chatClient.ts';
import type { AgentDraft } from '../../api/libraryClient.ts';
import type { Project, SetupRequest, TaskSummary } from '../../api/types.ts';
import { byText, cleanup, drain, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { adapterInfo, CATALOG } from '../project/wizard-fixtures.ts';
import { START_KEY } from './start.ts';
import { WorkspaceHomeScreen } from './workspace-home.tsx';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem(START_KEY);
  } catch {
    /* nothing kept */
  }
});

const DAY = 86_400_000;
const now = Date.now();
const project = (id: string, name: string, over: Partial<Project> = {}): Project =>
  ({ id, name, repoPath: `/src/${name}`, hasRepo: true, defaultBranch: 'main', mergeStrategy: 'merge', mode: 'local_merge', created: now - 30 * DAY, ...over }) as Project;
const summary = (projectId: string, title: string, status: string, updatedAt: number, doing = ''): TaskSummary => ({ projectId, title, status, updatedAt, doing }) as TaskSummary;

// TEST DOUBLES only: a partial client with four projects, their tasks and what needs the person, the catalog and one CLI.
function show(client: Partial<Client> = {}) {
  const s = testServices({
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    projectList: async () => [project('p1', 'calculator'), project('p2', 'marketing-site'), project('p3', 'billing-api'), project('p4', 'old-thing', { created: now - 90 * DAY })],
    needsYouList: async () => [{ projectId: 'p2', title: 'Pricing page ready to merge' }] as never,
    taskSummaries: async () => ({
      summaries: [
        summary('p1', 'Add Multiply', 'IN_PROGRESS', now - 60_000, 'Editing calc.go'),
        summary('p1', 'Add Divide', 'REVIEW', now - 120_000),
        summary('p2', 'Pricing page', 'REVIEW', now - 5 * 60_000),
        summary('p3', 'Retry failed webhooks', 'COMPLETED', now - 2 * DAY),
      ],
      total: 4,
      partial: false,
    }),
    securityScanRunning: async () => [],
    agentCatalogList: async () => CATALOG,
    adapterList: async () => [adapterInfo('claude', true, ['opus', 'sonnet', 'haiku'])],
    githubCloneDir: async () => '/home/me/source/repos',
    agentTemplateList: async () => [],
    agentList: async () => [],
    ...client,
  });
  s.workspace.start();
  const navigated: string[] = [];
  const r = mount(<WorkspaceHomeScreen onNavigate={(h) => navigated.push(h)} />, s);
  return { r, s, navigated };
}

const cards = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('[data-testid="home-project"]')) as HTMLAnchorElement[];
const box = (r: { $: (s: string) => Element | null }) => r.$('[data-testid="start-text"]') as HTMLTextAreaElement;
const enter = (el: HTMLElement, shift = false) => el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', shiftKey: shift, bubbles: true, cancelable: true }));
const chipText = (r: { $: (s: string) => Element | null }, id: string) => (r.$(`[data-testid="${id}"]`)?.textContent ?? '').replace(/\s+/g, ' ').replace(/\s*·\s*/g, ' · ').trim();
const click = (r: { $: (s: string) => Element | null }, sel: string) => (r.$(sel) as HTMLElement).click();

describe('Home is the start screen (P142)', () => {
  it('asks what to build: one box, its chips, the Or row; no paragraphs', async () => {
    const { r } = show();
    await waitFor(() => expect(box(r)).not.toBeNull(), r);
    expect(r.$('h1')!.textContent).toBe('What do you want to build?');
    expect(r.$$('[data-testid="workspace-home"] p').length).toBe(1);
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('sonnet · medium'), r);
    await waitFor(() => expect(chipText(r, 'start-where')).toBe('New folder: new-project'), r);
    expect(chipText(r, 'start-who')).toBe('Solo · Claude Code');
    expect(chipText(r, 'start-permission')).toBe('Edits freely');
    await r.act(() => type(box(r), 'A small Go CLI that converts CSV to JSON'));
    await waitFor(() => expect(chipText(r, 'start-where')).toBe('New folder: go-cli-converts'), r);
    expect(Array.from(r.$$('[data-testid="start-or"] button')).map((b) => b.textContent!.trim())).toEqual(['New folder', 'Clone from GitHub', 'Open a folder…', 'Just chat']);
    // no way to the old wizard any more: a project without work is New project (P143)
    expect(r.$('[data-testid="start-more"]')).toBe(null);
  });

  it('picks up the three most recently active projects, with what runs or needs you; All projects shows the rest', async () => {
    const { r } = show();
    await waitFor(() => expect(cards(r)).toHaveLength(3), r);
    await waitFor(() => expect(cards(r).map((c) => c.querySelector('strong')!.textContent)).toEqual(['calculator', 'marketing-site', 'billing-api']), r);
    const state = (i: number) => cards(r)[i]!.querySelector('[data-testid="home-project-state"]')!;
    const line = (i: number) => cards(r)[i]!.querySelector('[data-testid="home-project-line"]')!.textContent;
    await waitFor(() => expect(state(1).textContent).toBe('needs you'), r);
    expect(state(0).textContent).toBe('2 running');
    expect(state(0).getAttribute('data-tone')).toBe('run');
    expect(state(1).getAttribute('data-tone')).toBe('warn');
    expect(state(2).textContent).toBe('2d');
    expect(line(0)).toBe('Add Multiply: Editing calc.go');
    expect(line(1)).toBe('Pricing page ready to merge');
    expect(line(2)).toBe('Last: Retry failed webhooks (completed)');
    await r.act(() => click(r, '[data-testid="home-all-projects"]'));
    expect(cards(r)).toHaveLength(4);
    expect(cards(r)[3]!.textContent).toContain('Nothing started yet.');
  });

  it('opening a card selects the project and opens it', async () => {
    const { r, s, navigated } = show();
    await waitFor(() => expect(cards(r)).toHaveLength(3), r);
    await r.act(() => cards(r).find((c) => c.dataset.project === 'p3')!.click());
    expect(s.workspace.projectId).toBe('p3');
    expect(navigated).toEqual(['/']);
  });

  it('Enter in a new folder with Solo: one project.setup, then a chat in it sent the description, and lands on it', async () => {
    const setups: SetupRequest[] = [];
    const chats: ChatCreate[] = [];
    const sent: string[] = [];
    let release: () => void = () => undefined;
    const { r, s, navigated } = show({
      projectSetup: (req) =>
        new Promise((res) => {
          setups.push(req);
          release = () => res({ project: project('p9', req.name), agents: [{ id: 'a1', name: 'Backend Engineer', role: 'backend' }] as never, warnings: [] });
        }),
      chatCreate: async (p) => (chats.push(p), { id: 'c9' }) as never,
      chatSend: async (_id, text) => (sent.push(text), { turnId: 't', version: 1 }),
    });
    const chosen: string[] = [];
    s.workspace.selectProject = (id: string) => void chosen.push(id);
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('sonnet · medium'), r);
    await r.act(() => type(box(r), 'A small Go CLI that converts CSV to JSON'));
    await r.act(() => enter(box(r), true)); // Shift+Enter is a new line
    expect(setups).toHaveLength(0);
    await r.act(() => enter(box(r)));
    await waitFor(() => expect(r.$('[data-testid="start-progress"]')!.textContent).toBe('Creating project…'), r);
    expect(s.workspace.setupOpen).toBe(true);
    // a second Enter meanwhile starts nothing
    await r.act(() => enter(box(r)));
    expect(setups).toHaveLength(1);
    expect(setups[0]!.repo).toEqual({ kind: 'local', path: '/home/me/source/repos/go-cli-converts', init: true });
    expect(setups[0]!.name).toBe('go-cli-converts');
    expect(setups[0]!.agents.map((a) => a.prebuilt)).toEqual(['backend-engineer']);
    release();
    await waitFor(() => expect(navigated).toEqual(['/?item=chat:c9']), r);
    expect(chats).toEqual([{ projectId: 'p9', adapter: 'claude', model: 'sonnet', effort: 'medium', permissionMode: 'acceptEdits' }]);
    expect(sent).toEqual(['A small Go CLI that converts CSV to JSON']);
    expect(s.workspace.setupOpen).toBe(false);
    expect(chosen).toEqual(['p9']);
  });

  it('says what is missing, and an error under the box keeps the text', async () => {
    const { r } = show({
      projectSetup: async () => {
        throw new Error('the folder is inside another repository');
      },
    });
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('sonnet · medium'), r);
    await r.act(() => enter(box(r)));
    expect(r.$('[data-testid="start-error"]')!.textContent).toBe('Describe what you want to build.');
    await r.act(() => type(box(r), 'Build a thing'));
    expect(r.$('[data-testid="start-error"]')).toBeNull();
    await r.act(() => enter(box(r)));
    await waitFor(() => expect(r.$('[data-testid="start-error"]')!.textContent).toBe('the folder is inside another repository'), r);
    expect(box(r).value).toBe('Build a thing');
  });

  it('a Recent project: its crew, a task assigned to its builder and reviewer, started, and lands on the task', async () => {
    const calls: string[] = [];
    const { r, s, navigated } = show({
      agentList: async () =>
        [
          { id: 'l', name: 'Lead', role: 'lead' },
          { id: 'b', name: 'Builder', role: 'backend' },
          { id: 'v', name: 'Reviewer', role: 'reviewer' },
        ] as never,
      taskCreate: async (req) => (calls.push(`create ${req.projectId} ${req.title}`), { id: 't5' }) as never,
      taskAssign: async (id, w, rv) => (calls.push(`assign ${id} ${w} ${rv}`), {}) as never,
      taskStart: async (id) => (calls.push(`start ${id}`), 'run'),
    });
    await waitFor(() => expect(cards(r)).toHaveLength(3), r);
    await r.act(() => click(r, '[data-testid="start-where"]'));
    await waitFor(() => expect(r.$('[data-testid="where-recent"][data-project="p1"]')).not.toBeNull(), r);
    await r.act(() => click(r, '[data-testid="where-recent"][data-project="p1"]'));
    expect(chipText(r, 'start-where')).toBe('calculator');
    await waitFor(() => expect(chipText(r, 'start-who')).toBe('Project crew · 3 agents'), r);
    // the project's agents keep their own model and permissions
    expect(r.$('[data-testid="start-model"]')).toBeNull();
    await r.act(() => type(box(r), 'Add a currency converter page. With tests.'));
    await r.act(() => enter(box(r)));
    await waitFor(() => expect(navigated).toEqual(['/?item=task:t5']), r);
    expect(calls).toEqual(['create p1 Add a currency converter page', 'assign t5 b v', 'start t5']);
    expect(s.workspace.projectId).toBe('p1');
  });

  it('Just chat: a chat without a project, sent what was typed', async () => {
    const chats: ChatCreate[] = [];
    const sent: string[] = [];
    const { r, navigated } = show({
      chatCreate: async (p) => (chats.push(p), { id: 'c1' }) as never,
      chatSend: async (_id, text) => (sent.push(text), { turnId: 't', version: 1 }),
    });
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('sonnet · medium'), r);
    await r.act(() => type(box(r), 'What is a monad?'));
    await r.act(() => byText(r.container, 'button', 'Just chat').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/c1']), r);
    expect(chats).toEqual([{ adapter: 'claude', model: 'sonnet', effort: 'medium', permissionMode: 'acceptEdits' }]);
    expect(sent).toEqual(['What is a monad?']);
  });

  it('remembers the chips per place and chooses them again', async () => {
    const setups: SetupRequest[] = [];
    const first = show({
      projectSetup: async (req) => (setups.push(req), { project: project('p9', req.name), agents: [], warnings: [] }),
      chatCreate: async () => ({ id: 'c1' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    const { r } = first;
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('sonnet · medium'), r);
    await r.act(() => click(r, '[data-testid="start-who"]'));
    await waitFor(() => expect(r.$('[data-testid="who-team"]')).not.toBeNull(), r);
    await r.act(() => click(r, '[data-testid="who-team"]'));
    expect(chipText(r, 'start-who')).toBe('Small team · 3 agents');
    await r.act(() => click(r, '[data-testid="start-permission"]'));
    await waitFor(() => expect(r.$('[data-testid="perm-option"][data-value="plan"]')).not.toBeNull(), r);
    await r.act(() => click(r, '[data-testid="perm-option"][data-value="plan"]'));
    expect(chipText(r, 'start-permission')).toBe('Read only');
    await r.act(() => type(box(r), 'Write docs'));
    await r.act(() => enter(box(r)));
    await waitFor(() => expect(first.navigated).toHaveLength(1), r);
    expect(setups[0]!.agents.find((a) => a.prebuilt === 'backend-engineer')!.permissionMode).toBe('plan');
    cleanup();
    const again = show();
    await waitFor(() => expect(chipText(again.r, 'start-who')).toBe('Small team · 3 agents'), again.r);
    expect(chipText(again.r, 'start-permission')).toBe('Read only');
  });

  it('Describe a new agent: the drafted agent joins the crew of the new project', async () => {
    const draft: AgentDraft = { name: 'Docs Writer', role: 'docs', adapter: 'claude', model: 'haiku', effort: 'low', permissionMode: 'plan', purpose: 'Writes docs.', skills: [], tools: [], why: 'You asked for docs.', source: 'rules' };
    const setups: SetupRequest[] = [];
    const { r } = show({
      agentDraft: async () => draft,
      projectSetup: async (req) => (setups.push(req), { project: project('p9', req.name), agents: [], warnings: [] }),
      chatCreate: async () => ({ id: 'c1' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('sonnet · medium'), r);
    await r.act(() => click(r, '[data-testid="start-who"]'));
    await waitFor(() => expect(r.$('[data-testid="who-describe"]')).not.toBeNull(), r);
    await r.act(() => click(r, '[data-testid="who-describe"]'));
    const dlg = await waitFor(() => document.querySelector<HTMLElement>('dialog[open] [data-testid="describe-agent"]')!, r);
    await r.act(() => type(dlg.querySelector('textarea')!, 'someone who writes docs'));
    await r.act(() => byText(dlg, 'button', /^Draft/).click());
    await waitFor(() => byText(dlg, 'button', 'Add to crew'), r);
    await r.act(() => byText(dlg, 'button', 'Add to crew').click());
    await drain();
    expect(chipText(r, 'start-who')).toBe('Solo · Claude Code + Docs Writer');
    await r.act(() => type(box(r), 'Document the API'));
    await r.act(() => enter(box(r)));
    await waitFor(() => expect(setups).toHaveLength(1), r);
    expect(setups[0]!.agents.map((a) => a.prebuilt ?? a.name)).toEqual(['backend-engineer', 'Docs Writer']);
  });
});
