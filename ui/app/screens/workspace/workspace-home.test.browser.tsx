import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ChatCreate } from '../../api/chatClient.ts';
import type { AgentDraft } from '../../api/libraryClient.ts';
import type { Connection, Project, SetupRequest, TaskSummary } from '../../api/types.ts';
import { byText, cleanup, drain, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { adapterInfo, CATALOG } from '../project/wizard-fixtures.ts';
import { SETUP_NOTES_KEY } from '../../lib/setup-notes.ts';
import { DRAFT_KEY, START_KEY } from './start.ts';
import { WorkspaceHomeScreen } from './workspace-home.tsx';

afterEach(() => {
  cleanup();
  try {
    localStorage.removeItem(START_KEY);
    localStorage.removeItem(DRAFT_KEY);
    localStorage.removeItem(SETUP_NOTES_KEY);
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
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('Sonnet · medium'), r);
    await waitFor(() => expect(chipText(r, 'start-where')).toBe('New folder: new-project'), r);
    expect(chipText(r, 'start-who')).toBe('Solo · Claude Code');
    expect(chipText(r, 'start-permission')).toBe('Asks before edits');
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
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('Sonnet · medium'), r);
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
    expect(chats).toEqual([{ projectId: 'p9', adapter: 'claude', model: 'sonnet', effort: 'medium', permissionMode: 'default' }]);
    expect(sent).toEqual(['A small Go CLI that converts CSV to JSON']);
    expect(s.workspace.setupOpen).toBe(false);
    expect(chosen).toEqual(['p9']);
  });

  it("says under the chips that the builder's CLI is not signed in, refuses Enter, and offers another CLI that is ready (A1)", async () => {
    const setups: SetupRequest[] = [];
    const { r } = show({
      adapterList: async () => [{ ...adapterInfo('claude', true, ['sonnet']), detection: { adapter: 'claude', installed: true, status: 'unauthenticated' } }, adapterInfo('codex', true, ['gpt-5'])],
      projectSetup: async (req) => (setups.push(req), { project: project('p9', req.name), agents: [], warnings: [] }),
      chatCreate: async () => ({ id: 'c9' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    const line = await waitFor(() => {
      const el = r.$('[data-testid="start-cli"]');
      if (!el) throw new Error('no line yet');
      return el as HTMLElement;
    }, r);
    expect(line.dataset.state).toBe('sign-in');
    expect(line.textContent).toContain('Claude Code is not signed in.');
    expect(line.querySelector('[data-testid="start-cli-fix"]')!.textContent).toBe('Sign in');
    await r.act(() => type(box(r), 'A small Go CLI'));
    await r.act(() => enter(box(r)));
    expect(r.$('[data-testid="start-error"]')!.textContent).toBe('Claude Code is not signed in. Sign in first, or choose another CLI.');
    expect(setups).toHaveLength(0);
    await r.act(() => click(r, '[data-testid="start-cli-other"]'));
    expect(r.$('[data-testid="start-cli"]')).toBeNull();
    expect(chipText(r, 'start-who')).toBe('Solo · Codex');
    await r.act(() => enter(box(r)));
    await waitFor(() => expect(setups).toHaveLength(1), r);
    expect(setups[0]!.agents.map((a) => a.adapter)).toEqual(['codex']);
  });

  it('with a Small team the line covers the lead and reviewer, and Use Codex instead moves all three (A1)', async () => {
    const setups: SetupRequest[] = [];
    const { r } = show({
      adapterList: async () => [{ ...adapterInfo('claude', true, ['sonnet']), detection: { adapter: 'claude', installed: true, status: 'unauthenticated' } }, adapterInfo('codex', true, ['gpt-5'])],
      projectSetup: async (req) => (setups.push(req), { project: project('p9', req.name), agents: [], warnings: [] }),
      taskCreate: async () => ({ id: 't1' }) as never,
      taskAssign: async () => ({}) as never,
      taskStart: async () => 'run',
      chatCreate: async () => ({ id: 'c9' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    await waitFor(() => expect(r.$('[data-testid="start-cli"]')).not.toBeNull(), r);
    await r.act(() => click(r, '[data-testid="start-who"]'));
    await waitFor(() => expect(r.$('[data-testid="who-team"]')).not.toBeNull(), r);
    await r.act(() => click(r, '[data-testid="who-team"]'));
    expect((r.$('[data-testid="start-cli"]') as HTMLElement).dataset.adapter).toBe('claude');
    await r.act(() => click(r, '[data-testid="start-cli-other"]'));
    expect(r.$('[data-testid="start-cli"]')).toBeNull();
    await r.act(() => type(box(r), 'A small Go CLI'));
    await r.act(() => enter(box(r)));
    expect(r.$('[data-testid="start-error"]')).toBeNull();
    await waitFor(() => expect(setups).toHaveLength(1), r);
    expect(setups[0]!.agents.map((a) => a.adapter)).toEqual(['codex', 'codex', 'codex']);
  });

  it("follows Drydock's own CLI check from its kept answers, without starting the CLIs again (A2)", async () => {
    let listed = 0;
    let full = 0;
    let signedIn = false; // signed in elsewhere: the background check finds it
    const { r, s } = show({
      adapterList: async () => (listed++, [{ ...adapterInfo('claude', true, ['sonnet']), detection: { adapter: 'claude', installed: true, status: 'unauthenticated', checkSeq: 1 } }]),
      connectionList: async (c) => {
        if (!c) full++;
        return signedIn ? [{ adapter: 'claude', key: 'claude', runMode: 'native', installed: true, status: 'ready', usable: true, platform: 'windows', checkSeq: 2 } as Connection] : [];
      },
    });
    await waitFor(() => expect(r.$('[data-testid="start-cli"]')).not.toBeNull(), r);
    expect(listed).toBe(1);
    await new Promise((res) => setTimeout(res, 5));
    signedIn = true;
    s.emit({ type: 'connection.checked' });
    await waitFor(() => expect(r.$('[data-testid="start-cli"]')).toBeNull(), r);
    expect([listed, full]).toEqual([1, 0]);
  });

  it('keeps what failed after the project was made as lines for its Work page, not one flash (B2)', async () => {
    const cli = { kind: 'cli', text: 'Backend Engineer cannot work yet', adapter: 'claude', status: 'unauthenticated' };
    const { r, s, navigated } = show({
      projectSetup: async (req) => ({ project: project('p9', req.name), agents: [], warnings: [cli.text], problems: [cli] }),
      chatCreate: async () => ({ id: 'c9' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('Sonnet · medium'), r);
    await r.act(() => type(box(r), 'A small Go CLI'));
    await r.act(() => enter(box(r)));
    await waitFor(() => expect(navigated).toEqual(['/?item=chat:c9']), r);
    expect(JSON.parse(localStorage.getItem(SETUP_NOTES_KEY)!)).toEqual({ p9: [cli] });
    expect(s.ui.toasts).toEqual([]);
  });

  it('says what is missing, and an error under the box keeps the text', async () => {
    const { r } = show({
      projectSetup: async () => {
        throw new Error('the folder is inside another repository');
      },
    });
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('Sonnet · medium'), r);
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
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('Sonnet · medium'), r);
    await r.act(() => type(box(r), 'What is a monad?'));
    await r.act(() => byText(r.container, 'button', 'Just chat').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/c1']), r);
    expect(chats).toEqual([{ adapter: 'claude', model: 'sonnet', effort: 'medium', permissionMode: 'default' }]);
    expect(sent).toEqual(['What is a monad?']);
  });

  it("with nothing remembered here, the chips start from the person's last chat choice (chat.lastChoice, D2)", async () => {
    const asked: unknown[][] = [];
    const chats: ChatCreate[] = [];
    const { r } = show({
      chatLastChoice: async (...a: unknown[]) => (asked.push(a), { model: 'opus', effort: 'high', permissionMode: 'plan' }),
      chatCreate: async (p) => (chats.push(p), { id: 'c1' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('Opus · high'), r);
    expect(chipText(r, 'start-permission')).toBe('Read only');
    expect(asked[0]).toEqual(['', 'claude']);
    await r.act(() => type(box(r), 'What is a monad?'));
    await r.act(() => byText(r.container, 'button', 'Just chat').click());
    await waitFor(() => expect(chats).toEqual([{ adapter: 'claude', model: 'opus', effort: 'high', permissionMode: 'plan' }]), r);
  });

  it('the Where menu lists Recent as Pick up does: the most recently active first, with the same pill (B9)', async () => {
    // billing-api was made last, but calculator and marketing-site were active since
    const { r } = show({
      projectList: async () => [project('p1', 'calculator'), project('p2', 'marketing-site'), project('p3', 'billing-api', { created: now - 3 * DAY }), project('p4', 'old-thing', { created: now - 90 * DAY })],
    });
    await waitFor(() => expect(cards(r)).toHaveLength(3), r);
    await waitFor(() => expect(cards(r)[1]!.querySelector('[data-testid="home-project-state"]')!.textContent).toBe('needs you'), r);
    await r.act(() => click(r, '[data-testid="start-where"]'));
    await waitFor(() => expect(r.$$('[data-testid="where-recent"]').length).toBe(4), r);
    const rows = Array.from(r.$$('[data-testid="where-recent"]')) as HTMLElement[];
    expect(rows.map((b) => b.dataset.project)).toEqual(cards(r).map((c) => c.dataset.project).concat(['p4']));
    expect(rows.slice(0, 3).map((b) => b.querySelector('[data-testid="where-recent-state"]')!.textContent)).toEqual(['2 running', 'needs you', '2d']);
  });

  it('keeps an unsent description when Home is left, and forgets it once sent (B10)', async () => {
    const first = show({
      chatCreate: async () => ({ id: 'c1' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    await waitFor(() => expect(box(first.r)).not.toBeNull(), first.r);
    await first.r.act(() => type(box(first.r), 'A pricing page with three tiers'));
    cleanup(); // Home is left (a card, Settings)
    const again = show({
      chatCreate: async () => ({ id: 'c1' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    await waitFor(() => expect(box(again.r)?.value).toBe('A pricing page with three tiers'), again.r);
    await waitFor(() => expect(chipText(again.r, 'start-model')).toBe('Sonnet · medium'), again.r);
    await again.r.act(() => byText(again.r.container, 'button', 'Just chat').click());
    await waitFor(() => expect(again.navigated).toEqual(['/chats/c1']), again.r);
    expect(localStorage.getItem(DRAFT_KEY)).toBeNull();
    cleanup();
    const third = show();
    await waitFor(() => expect(box(third.r)).not.toBeNull(), third.r);
    expect(box(third.r).value).toBe('');
  });

  it('Open a folder keeps an untick when the dialog is opened again on the same folder (B6)', async () => {
    let checks = 0;
    const { r } = show({
      repoCheck: async (path: string) => (checks++, { path, state: 'missing', entries: 0, canInit: true, message: 'This folder does not exist yet.' }) as never,
    });
    await waitFor(() => expect(r.$('[data-testid="or-folder"]')).not.toBeNull(), r);
    await r.act(() => click(r, '[data-testid="or-folder"]'));
    const field = await waitFor(() => r.$('[data-testid="start-folder-dialog"] [data-testid="folder-field"] input') as HTMLInputElement, r);
    await r.act(() => type(field, '/src/new'));
    const tick = () => r.$('[data-testid="folder-init"] input') as HTMLInputElement;
    await waitFor(() => expect(tick().checked).toBe(true), r, 3000);
    await r.act(() => tick().click());
    expect(tick().checked).toBe(false);
    await r.act(() => byText(r.$('[data-testid="start-folder-dialog"]')!, 'button', 'Cancel').click());
    await r.act(() => click(r, '[data-testid="or-folder"]'));
    // the folder is checked again; the person's untick stands
    await waitFor(() => expect(checks).toBe(2), r, 3000);
    await drain();
    r.root.flush();
    expect(tick().checked).toBe(false);
    expect((r.$('[data-testid="folder-use"]') as HTMLButtonElement).disabled).toBe(true);
  });

  it('remembers the chips per place and chooses them again', async () => {
    const setups: SetupRequest[] = [];
    const first = show({
      projectSetup: async (req) => (setups.push(req), { project: project('p9', req.name), agents: [], warnings: [] }),
      chatCreate: async () => ({ id: 'c1' }) as never,
      chatSend: async () => ({ turnId: 't', version: 1 }),
    });
    const { r } = first;
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('Sonnet · medium'), r);
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
    await waitFor(() => expect(chipText(r, 'start-model')).toBe('Sonnet · medium'), r);
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
