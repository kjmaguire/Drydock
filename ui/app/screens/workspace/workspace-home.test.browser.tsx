import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ChatCreate, ChatListItem } from '../../api/chatClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { byLabel, byText, cleanup, drain, mount, testServices, type, typeKeys, valueOf, waitFor } from '../../test/render.tsx';
import { WorkspaceHomeScreen } from './workspace-home.tsx';

afterEach(cleanup);

// TEST DOUBLES only: a partial client with two projects, one of which has two items waiting for the human.
function show(client: Partial<Client> = {}) {
  const s = testServices({
    licenseStatus: async () => ({}) as never,
    appInfo: async () => ({}) as never,
    projectList: async () => [
      { id: 'p1', name: 'gocalc', repoPath: 'C:\\code\\gocalc', hasRepo: true },
      { id: 'p2', name: 'idea', repoPath: '', hasRepo: false },
    ] as never,
    needsYouList: async (pid: string) => (pid === 'p1' ? [{}, {}] : []) as never,
    chatList: async () => [],
    ...client,
  });
  s.workspace.start();
  const navigated: string[] = [];
  const r = mount(<WorkspaceHomeScreen onNavigate={(h) => navigated.push(h)} />, s);
  return { r, s, navigated };
}

const tiles = (r: { $$: (s: string) => NodeListOf<Element> }) => Array.from(r.$$('[data-testid="home-project"]')) as HTMLAnchorElement[];

describe('the workspace Home', () => {
  it('lists the projects with where they live and what needs you in each', async () => {
    const { r } = show();
    await waitFor(() => expect(tiles(r)).toHaveLength(2), r);
    const [gocalc, idea] = tiles(r);
    expect(gocalc!.textContent).toContain('gocalc');
    expect(gocalc!.textContent).toContain('C:\\code\\gocalc');
    expect(idea!.textContent).toContain('No repository yet');
    await waitFor(() => expect(gocalc!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('2 items need you'), r);
    expect(gocalc!.querySelector('[data-testid="home-project-needs"]')!.getAttribute('data-tone')).toBe('warn');
    expect(idea!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('Nothing needs you');
  });

  it("says on one tile that its list could not be read, and still counts the others", async () => {
    const { r } = show({
      needsYouList: async (pid: string) => {
        if (pid === 'p2') throw new Error('core unreachable');
        return [{}] as never;
      },
    });
    await waitFor(() => expect(tiles(r)).toHaveLength(2), r);
    const [gocalc, idea] = tiles(r);
    await waitFor(() => expect(gocalc!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('1 item needs you'), r);
    expect(idea!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('Could not check what needs you');
  });

  it('opens a project: selects it, then goes to its home', async () => {
    const { r, s, navigated } = show();
    await waitFor(() => expect(tiles(r)).toHaveLength(2), r);
    await r.act(() => tiles(r)[1]!.click());
    expect(s.workspace.projectId).toBe('p2');
    expect(navigated).toEqual(['/']);
  });

  it('links every workspace area: the skill library, agent templates, chats, connections and the app', async () => {
    const { r } = show();
    await waitFor(() => expect(r.$('[data-testid="home-skills"]')).not.toBeNull(), r);
    const hrefs = Array.from(r.$$('[data-testid^="home-"]'))
      .map((e) => e.getAttribute('href'))
      .filter(Boolean);
    for (const h of ['/library/skills', '/library', '/chats', '/integrations', '/connections', '/security-scan', '/models', '/routing', '/settings', '/backups', '/data', '/license', '/support', '/project/new']) {
      expect([h, hrefs.includes(h)]).toEqual([h, true]);
    }
    expect(r.$('[data-testid="home-skills"]')!.textContent).toContain('Skills');
    // a security scan of any folder, before it becomes a project (decision P61)
    const scan = r.$('[data-testid="home-security-scan"]')!;
    expect(scan.getAttribute('href')).toBe('/security-scan');
    expect(scan.textContent).toContain('Security scan');
    expect(scan.textContent).toContain('without running it');
  });

  it("holds a project only as a navigation tile: no project's page, agent or server is linked or shown", async () => {
    const { r } = show({ agentList: async () => [{ id: 'a1', name: 'Backend #1' }] as never });
    await waitFor(() => expect(tiles(r)).toHaveLength(2), r);
    await waitFor(() => expect(tiles(r)[0]!.querySelector('[data-testid="home-project-needs"]')!.textContent).toBe('2 items need you'), r);
    const hrefs = Array.from(r.$$('a')).map((a) => a.getAttribute('href'));
    for (const h of ['/project/integrations', '/team', '/tasks', '/skills', '/project', '/needs-you']) expect([h, hrefs.includes(h)]).toEqual([h, false]);
    expect(r.container.textContent).not.toContain('Backend #1');
    // the agent CLIs and the MCP gateway are on Connections
    expect(r.$('[data-testid="home-connections"]')!.textContent).toContain('MCP gateway');
  });

  it('offers the first project when there is none', async () => {
    const { r } = show({ projectList: async () => [] });
    await waitFor(() => expect(r.$('[data-testid="home-projects"]')!.textContent).toContain('No projects yet.'), r);
    expect(tiles(r)).toHaveLength(0);
    expect(r.$('[data-testid="home-projects"] a[href="/project/new"]')).not.toBeNull();
  });

  it("shows, once, the note the previous page left (a deleted project)", async () => {
    const s = testServices({ licenseStatus: async () => ({}) as never, appInfo: async () => ({}) as never, projectList: async () => [], needsYouList: async () => [] });
    s.ui.flash('gocalc was deleted.');
    s.workspace.start();
    const r = mount(<WorkspaceHomeScreen />, s);
    await waitFor(() => expect(r.$('[data-testid="home-flash"]')!.textContent).toContain('gocalc was deleted.'), r);
    expect(s.ui.takeFlash()).toBeUndefined();
  });
});

// Decision P60: a chat from Home, without a project. TEST DOUBLES: the chat methods of a partial client.
const homeChat = (over: Partial<ChatListItem> = {}): ChatListItem => ({
  id: 'chat_h',
  projectId: '',
  title: '',
  adapter: 'claude',
  model: '',
  effort: '',
  permissionMode: 'default',
  skipsApprovals: false,
  cwd: '/data/chats/chat_h',
  sessionId: '',
  allowedTools: [],
  readHistory: true,
  status: 'idle',
  lastError: '',
  pinned: false,
  archived: false,
  archivedAt: 0,
  forkedFrom: '',
  createdAt: 1,
  updatedAt: Date.now() - 60_000,
  tokensIn: 0,
  tokensOut: 0,
  costUsd: null,
  costUnknownTurns: 0,
  version: 1,
  preview: '',
  messageCount: 0,
  ...over,
});
const claudeCli: AdapterInfo = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready', version: '2.1.296' },
  capabilities: { permissionModes: ['default'] } as AdapterInfo['capabilities'],
};
const chatForm = (r: ReturnType<typeof mount>) => r.$('[data-testid="home-chat-form"]') as HTMLFormElement;
const dialogOpen = (testId: string) => Array.from(document.querySelectorAll<HTMLElement>('dialog[open]')).find((d) => d.querySelector(`[data-testid="${testId}"]`));

describe('Chat from Home', () => {
  it('starts a chat without a project: makes it, sends what was typed, and opens it', async () => {
    const created: ChatCreate[] = [];
    const sent: [string, string][] = [];
    const { r, navigated } = show({
      chatCreate: async (p) => (created.push(p), homeChat({ id: 'chat_new' })),
      chatSend: async (id, text) => (sent.push([id, text]), { turnId: 'cturn_1', version: 2 }),
    });
    await waitFor(() => expect(chatForm(r)).not.toBeNull(), r);
    // the chat comes first on Home
    expect(r.$('[data-testid="workspace-home"] section')!.getAttribute('data-testid')).toBe('home-chat');
    type(byLabel<HTMLTextAreaElement>(r.container, 'Message to Claude Code'), '  What is in this folder?  ');
    await r.act(() => byText(chatForm(r), 'button', 'Start chat').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/chat_new']), r);
    // no project, no folder: the core makes a private folder and runs Claude Code
    expect(created).toEqual([{}]);
    expect(sent).toEqual([['chat_new', 'What is in this folder?']]);
    expect(byLabel<HTMLTextAreaElement>(r.container, 'Message to Claude Code').value).toBe('');
  });

  // audit 2026-10-16, L40: sending the message is a paid turn. While it is being sent, a second click or Ctrl+Enter starts no
  // second chat, and the control is usable again when the send returns or fails.
  it('while the message is being sent a second click or Ctrl+Enter starts no second chat; the control is usable again when the send returns or fails', async () => {
    const calls: string[] = [];
    let finishSend: () => void = () => undefined;
    let failSend: (e: Error) => void = () => undefined;
    let n = 0;
    const { r, navigated } = show({
      chatCreate: async () => (calls.push('create'), homeChat({ id: `chat_${++n}` })),
      chatSend: () =>
        new Promise((res, rej) => {
          calls.push('send');
          finishSend = () => res({ turnId: 'cturn_1', version: 2 });
          failSend = rej;
        }),
    });
    const box = await waitFor(() => byLabel<HTMLTextAreaElement>(r.container, 'Message to Claude Code'), r);
    const startBtn = () => byText<HTMLButtonElement>(chatForm(r), 'button', /^(Start chat|Starting\.\.\.)$/);
    const ctrlEnter = () => r.act(() => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true, cancelable: true })));
    for (const fail of [false, true]) {
      calls.length = 0;
      type(box, 'What is in this folder?');
      await r.act(() => startBtn().click());
      await waitFor(() => expect(calls).toEqual(['create', 'send']), r); // chatSend is in flight, the chat exists
      // the control is off and says so; Options (which would make another chat) is off too
      expect(startBtn().disabled).toBe(true);
      expect(startBtn().textContent).toBe('Starting...');
      expect(byText<HTMLButtonElement>(r.container, 'button', 'Options...').disabled).toBe(true);
      await ctrlEnter();
      await r.act(() => startBtn().click());
      await r.act(() => chatForm(r).requestSubmit());
      await ctrlEnter();
      await drain();
      expect(calls).toEqual(['create', 'send']); // one chat, one message
      if (fail) failSend(new Error('Claude Code is not signed in'));
      else finishSend();
      await waitFor(() => expect(startBtn().disabled).toBe(false), r); // usable again
      expect(startBtn().textContent).toBe('Start chat');
      if (fail) {
        await waitFor(() => expect(r.$('[data-testid="home-chat-unsent"]')).not.toBeNull(), r);
        expect(box.value).toBe('What is in this folder?');
      } else {
        await waitFor(() => expect(navigated).toEqual(['/chats/chat_1']), r);
        expect(box.value).toBe('');
      }
      expect(calls).toEqual(['create', 'send']);
    }
  });

  it('a chat made from Options sends its message under the same guard: Start chat is off until the send ends', async () => {
    const calls: string[] = [];
    let finishSend: () => void = () => undefined;
    const { r, navigated } = show({
      adapterList: async () => [claudeCli],
      connectionList: async () => [],
      skillLibraryList: async () => [],
      mcpLibList: async () => [],
      chatCreate: async () => (calls.push('create'), homeChat({ id: 'chat_o2' })),
      chatSend: () =>
        new Promise((res) => {
          calls.push('send');
          finishSend = () => res({ turnId: 't', version: 1 });
        }),
    });
    const box = await waitFor(() => byLabel<HTMLTextAreaElement>(r.container, 'Message to Claude Code'), r);
    type(box, 'hello from options');
    await r.act(() => byText(r.container, 'button', 'Options...').click());
    const dlg = await waitFor(() => {
      const d = dialogOpen('new-chat-form');
      if (!d) throw new Error('no dialog');
      return d;
    }, r);
    await waitFor(() => expect(valueOf(byLabel(dlg, 'Project'))).toBe(''), r);
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(calls).toEqual(['create', 'send']), r);
    const startBtn = () => byText<HTMLButtonElement>(chatForm(r), 'button', /^(Start chat|Starting\.\.\.)$/);
    await waitFor(() => expect(startBtn().disabled).toBe(true), r);
    await r.act(() => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true, cancelable: true })));
    await drain();
    expect(calls).toEqual(['create', 'send']);
    finishSend();
    await waitFor(() => expect(navigated).toEqual(['/chats/chat_o2']), r);
    await waitFor(() => expect(startBtn().disabled).toBe(false), r);
  });

  it('keeps every key typed into the message box and sends the whole text (H12)', async () => {
    const sent: [string, string][] = [];
    const { r, navigated } = show({
      chatCreate: async () => homeChat({ id: 'chat_k' }),
      chatSend: async (id, text) => (sent.push([id, text]), { turnId: 'cturn_k', version: 2 }),
    });
    const box = await waitFor(() => byLabel<HTMLTextAreaElement>(r.container, 'Message to Claude Code'), r);
    await typeKeys(box, 'fix the failing test', r);
    expect(box.value).toBe('fix the failing test');
    await r.act(() => byText(chatForm(r), 'button', 'Start chat').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/chat_k']), r);
    expect(sent).toEqual([['chat_k', 'fix the failing test']]);
  });

  it('starts with Ctrl+Enter, and an empty message just opens the new chat', async () => {
    const sent: unknown[] = [];
    const { r, navigated } = show({ chatCreate: async () => homeChat({ id: 'chat_e' }), chatSend: async (...a) => (sent.push(a), { turnId: 't', version: 1 }) });
    const box = await waitFor(() => byLabel<HTMLTextAreaElement>(r.container, 'Message to Claude Code'), r);
    await r.act(() => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true, cancelable: true })));
    await waitFor(() => expect(navigated).toEqual(['/chats/chat_e']), r);
    expect(sent).toEqual([]);
  });

  it('lists the latest chats without a project to reopen, and links every chat', async () => {
    const { r } = show({
      chatList: async () => [
        homeChat({ id: 'c1', title: 'Shell help', preview: 'Use find with -newer' }),
        homeChat({ id: 'c2', title: 'In a project', projectId: 'p1' }),
        homeChat({ id: 'c3', title: 'Archived', archived: true, archivedAt: 5 }),
        ...[4, 5, 6, 7, 8].map((i) => homeChat({ id: `c${i}`, title: `Chat ${i}` })),
      ],
    });
    const rows = await waitFor(() => {
      const l = Array.from(r.$$('[data-testid="home-chat-recent"]')) as HTMLAnchorElement[];
      if (!l.length) throw new Error('no rows yet');
      return l;
    }, r);
    expect(rows.map((a) => a.getAttribute('href'))).toEqual(['/chats/c1', '/chats/c4', '/chats/c5', '/chats/c6', '/chats/c7']);
    expect(rows[0]!.textContent).toContain('Shell help');
    expect(rows[0]!.textContent).toContain('Use find with -newer');
    expect(r.$('[data-testid="home-chats-all"]')!.getAttribute('href')).toBe('/chats');
  });

  it('Options opens the new chat dialog without a project; the chosen folder, skill and server go with the chat, then the message', async () => {
    const created: ChatCreate[] = [];
    const sent: string[] = [];
    const { r, navigated } = show({
      adapterList: async () => [claudeCli],
      connectionList: async () => [],
      skillLibraryList: async () => [
        { id: 'l1', registryId: 'cloudflare-skills', repo: 'cloudflare/skills', name: 'wrangler', description: 'Wrangler rules.', license: 'MIT', globs: [], roles: [], sourcePath: '', pinnedCommit: '', contentHash: '', hasScripts: false, status: 'available', blockReason: '', risk: 0, flags: [], gone: false, updated: '' },
      ],
      mcpLibList: async () => [{ id: 'tst', display: 'Test server', transport: 'stdio', secretEnv: [], hasToken: false, guard: true, readOnlyTools: [], tools: [{ name: 'echo', readOnlyHint: true }], discoveredAt: 1, projects: [], createdAt: 1, updatedAt: 1 }],
      chatCreate: async (p) => (created.push(p), homeChat({ id: 'chat_o' })),
      chatSend: async (_id, text) => (sent.push(text), { turnId: 't', version: 1 }),
    });
    const box = await waitFor(() => byLabel<HTMLTextAreaElement>(r.container, 'Message to Claude Code'), r);
    type(box, 'hello from options');
    await r.act(() => byText(r.container, 'button', 'Options...').click());
    const dlg = await waitFor(() => {
      const d = dialogOpen('new-chat-form');
      if (!d) throw new Error('no dialog');
      return d;
    }, r);
    await waitFor(() => expect(valueOf(byLabel(dlg, 'Project'))).toBe(''), r);
    type(byLabel(dlg, 'Folder'), '/home/me/notes');
    const skill = await waitFor(() => dlg.querySelector<HTMLInputElement>('[data-testid="chat-skill-option"][data-name="wrangler"] input')!, r);
    await r.act(() => skill.click());
    const srv = await waitFor(() => dlg.querySelector<HTMLInputElement>('[data-testid="chat-mcp-option"][data-server="tst"] input')!, r);
    await r.act(() => srv.click());
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/chat_o']), r);
    expect(created).toEqual([
      {
        projectId: undefined,
        cwd: '/home/me/notes',
        adapter: 'claude',
        model: undefined,
        effort: undefined,
        permissionMode: undefined,
        readHistory: true,
        skills: [{ registryId: 'cloudflare-skills', name: 'wrangler' }],
        mcpServers: [{ serverId: 'tst', tools: [] }],
      },
    ]);
    expect(sent).toEqual(['hello from options']);
  });

  it('keeps a message that could not be sent, says why, and links the chat that was made', async () => {
    const { r, navigated } = show({
      chatCreate: async () => homeChat({ id: 'chat_x' }),
      chatSend: async () => {
        throw new Error('Claude Code is not signed in');
      },
    });
    const box = await waitFor(() => byLabel<HTMLTextAreaElement>(r.container, 'Message to Claude Code'), r);
    type(box, 'keep me');
    await r.act(() => byText(chatForm(r), 'button', 'Start chat').click());
    const note = await waitFor(() => {
      const n = r.$('[data-testid="home-chat-unsent"]');
      if (!n) throw new Error('no note yet');
      return n;
    }, r);
    expect(note.textContent).toContain('not signed in');
    expect(note.querySelector('a')!.getAttribute('href')).toBe('/chats/chat_x');
    expect(navigated).toEqual([]);
    expect(box.value).toBe('keep me');
  });
});
