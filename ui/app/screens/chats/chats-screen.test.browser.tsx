import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Chat, ChatHit, ChatListItem, ChatMessage } from '../../api/chatClient.ts';
import type { AdapterInfo, Connection, Project } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, optionsOf, testServices, type, typeKeys, valueOf, waitFor } from '../../test/render.tsx';
import { lic } from '../../test/fixtures.ts';
import { CommandPalette } from '../../shell/palette.tsx';
import { ChatsScreen } from './chats-screen.tsx';
import { RETRY_TEXT } from './chats.ts';
import { OTHER_MODEL } from '../shared/model-field.tsx';
import { VirtualList } from '../../ui/virtual-list.tsx';

afterEach(cleanup);

// --- plain data fixtures (no behavior) ---

const chat = (over: Partial<Chat> = {}): Chat => ({
  id: 'chat_a',
  projectId: 'prj_1',
  title: 'Where does Multiply go?',
  adapter: 'claude',
  model: 'sonnet',
  effort: 'high',
  permissionMode: 'default',
  skipsApprovals: false,
  cwd: 'C:\\repos\\gocalc',
  sessionId: 'sess-1',
  allowedTools: [],
  readHistory: true,
  status: 'idle',
  lastError: '',
  pinned: false,
  archived: false,
  archivedAt: 0,
  forkedFrom: '',
  createdAt: Date.now() - 3_600_000,
  updatedAt: Date.now() - 120_000,
  tokensIn: 1200,
  tokensOut: 340,
  costUsd: 0.0123,
  costUnknownTurns: 0,
  version: 1,
  ...over,
});
const item = (over: Partial<ChatListItem> = {}): ChatListItem => ({ ...chat(over), preview: 'The Multiply function goes in calc.go', messageCount: 4, ...over });
const msg = (seq: number, role: string, body: string, over: Partial<ChatMessage> = {}): ChatMessage => ({ id: `m${seq}`, chatId: 'chat_a', seq, role, body, createdAt: Date.now() - 60_000 + seq, ...over });

const claude: AdapterInfo = {
  id: 'claude',
  detection: { adapter: 'claude', installed: true, status: 'ready', version: '2.1.295' },
  capabilities: { modelSelection: true, effortControl: true, efforts: ['low', 'medium', 'high'], models: ['opus', 'sonnet', 'haiku'], permissionModes: ['default', 'acceptEdits', 'plan', 'bypassPermissions'] } as AdapterInfo['capabilities'],
};
const gemini: AdapterInfo = { id: 'gemini', detection: { adapter: 'gemini', installed: false, status: 'not_installed' }, capabilities: {} as AdapterInfo['capabilities'] };
const cline: AdapterInfo = { id: 'cline', detection: { adapter: 'cline', installed: true, status: 'ready', version: '3.0.70' }, capabilities: { permissionModes: ['default'] } as AdapterInfo['capabilities'] };

type R = ReturnType<typeof mount>;
const $ = <T extends Element = HTMLElement>(r: R, sel: string) => r.container.querySelector<T>(sel);
const $$ = (r: R, sel: string) => Array.from(r.container.querySelectorAll<HTMLElement>(sel));
/** Elements in the top layer (dialogs and popovers render inside the container but may be found from the document). */
const doc$$ = (sel: string) => Array.from(document.querySelectorAll<HTMLElement>(sel));
/** The element, or throw (so waitFor retries until it exists). */
function must<T>(x: T | null | undefined, what: string): T {
  if (x === null || x === undefined) throw new Error(`missing ${what}`);
  return x;
}
const must$ = (r: R, sel: string): HTMLElement => must($<HTMLElement>(r, sel), sel);
/** The open dialog whose text includes `text`. */
const dialogWith = (text: string) => must(doc$$('dialog[open]').find((d) => d.textContent!.includes(text)), `dialog with ${text}`);
const newChatDialog = () => must(doc$$('dialog[open]').find((d) => d.querySelector('[data-testid="new-chat-form"]')), 'new chat dialog');

function setup(client: Partial<Client>, props: { id?: string; newChat?: boolean; palette?: boolean } = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project, { id: 'prj_2', name: 'payments-api' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    adapterList: async () => [claude, gemini, cline],
    chatList: async () => [],
    skillLibraryList: async () => [],
    mcpLibList: async () => [],
    mcpApprovalList: async () => [],
    chatAsks: async () => [],
    ...client,
  });
  s.workspace.start();
  const navigated: string[] = [];
  if (props.newChat) s.ui.requestNewChat(); // the palette's New chat, before the screen opened
  const r = mount(
    <>
      <ChatsScreen id={props.id} onNavigate={(h) => void navigated.push(h)} />
      {props.palette ? <CommandPalette onMessage={() => {}} onNavigate={(h) => void navigated.push(h)} /> : null}
    </>,
    s,
  );
  return { s, r, navigated };
}

const rows = (r: R) => $$(r, '[data-testid="chat-row"]');
const rowTitles = (r: R) => rows(r).map((e) => e.querySelector('.title > span')!.textContent);
const messages = (r: R) => $$(r, '[data-testid="chat-message"]');

async function pickMenu(r: R, rowTitle: string, label: string) {
  const trigger = await waitFor(() => byText(r.container, 'button', `Actions for ${rowTitle}`), r);
  await r.act(() => trigger.click());
  // a menu still finishing its last selection (flash, close, back to idle) ignores the click, as it does for a person:
  // click again until it opens (no fixed wait)
  const it = await waitFor(() => {
    const el = $$(r, '[popover]:popover-open [role^="menuitem"]').find((e) => e.textContent!.trim() === label);
    if (!el) {
      if (trigger.getAttribute('aria-expanded') !== 'true') trigger.click();
      throw new Error(`no menu item ${label}`);
    }
    return el;
  }, r);
  await r.act(() => it.click());
}

describe('Chats list', () => {
  it('shows the current project by default, pinned first, and every chat with All chats', async () => {
    const calls: unknown[] = [];
    const { r } = setup({
      chatList: async (p) => {
        calls.push(p);
        const all = [item({ id: 'c1', title: 'Older' }), item({ id: 'c2', title: 'Pinned one', pinned: true }), item({ id: 'c3', title: 'Running', status: 'running' })];
        return p?.projectId ? all : [...all, item({ id: 'c4', title: 'No project chat', projectId: '' })];
      },
    });
    await waitFor(() => expect(rowTitles(r)).toEqual(['Pinned one', 'Older', 'Running']), r);
    expect((calls[calls.length - 1] as { projectId: string }).projectId).toBe('prj_1');
    expect(rows(r)[0]!.getAttribute('data-pinned')).toBe('true');
    expect(rows(r)[2]!.querySelector('[data-testid="chat-running"]')).not.toBeNull();
    expect(rows(r)[2]!.querySelector('a')!.getAttribute('aria-label')).toBe('Running, running');
    await r.act(() => byText(r.container, 'button', 'All chats').click());
    await waitFor(() => expect(rowTitles(r)).toContain('No project chat'), r);
    expect((calls[calls.length - 1] as { projectId: string }).projectId).toBe('');
    expect(byText(r.container, 'button', 'All chats').getAttribute('aria-pressed')).toBe('true');
  });

  it('shows the open chat on top when the project filter would hide it (UI review P1-4)', async () => {
    const home = item({ id: 'chat_home', title: 'Started from Home', projectId: '' });
    const { r } = setup(
      {
        chatList: async (p) => (p?.projectId ? [item({ id: 'c1', title: 'Project chat' })] : [item({ id: 'c1', title: 'Project chat' }), home]),
        chatGet: async () => ({ chat: home, messages: [], hasMore: false }),
      },
      { id: 'chat_home' },
    );
    const open = await waitFor(() => must$(r, '[data-testid="chat-list-open"]'), r);
    expect(open.textContent).toContain('Started from Home');
    expect(open.previousElementSibling!.textContent).toBe('Open, not in a project');
    expect(rowTitles(r)).toEqual(['Started from Home', 'Project chat']);
    // with All chats it is in the list itself, so it is not shown twice
    await r.act(() => byText(r.container, 'button', 'All chats').click());
    await waitFor(() => expect(rowTitles(r)).toEqual(['Project chat', 'Started from Home']), r);
    expect($(r, '[data-testid="chat-list-open"]')).toBeNull();
  });

  it('searches with a debounced query and highlights the matches', async () => {
    const searched: string[] = [];
    const hits: ChatHit[] = [
      { chatId: 'c1', title: 'Overflow edge cases', seq: 3, role: 'assistant', snippet: 'an [overflow] happens when [Add] gets two large ints', archived: false },
      { chatId: 'c2', title: 'Old notes', seq: 0, snippet: '[overflow] notes', archived: true },
    ];
    const { r } = setup({
      chatList: async () => [item({ id: 'c1', title: 'Overflow edge cases' })],
      chatSearch: async (q) => (searched.push(q), hits),
    });
    await waitFor(() => expect(rows(r)).toHaveLength(1), r);
    const box = byLabel(r.container, 'Search chats');
    type(box, 'o');
    type(box, 'ov');
    type(box, 'overflow');
    await waitFor(() => expect($$(r, '[data-testid="chat-hit"]')).toHaveLength(2), r);
    expect(searched).toEqual(['overflow']); // one request for the settled query
    const marks = $$(r, '[data-testid="chat-hit"] mark').map((m) => m.textContent);
    expect(marks).toEqual(['overflow', 'Add', 'overflow']);
    expect($$(r, '[data-testid="chat-hit"]')[1]!.textContent).toContain('archived');
    expect($$(r, '[data-testid="chat-hit"] a')[0]!.getAttribute('href')).toBe('/chats/c1');
    type(box, '');
    await waitFor(() => expect(rows(r)).toHaveLength(1), r);
  });

  it('pins from the row menu and refreshes on chat events', async () => {
    let list = [item({ id: 'c1', title: 'First' }), item({ id: 'c2', title: 'Second' })];
    const updates: unknown[] = [];
    const { r, s } = setup({
      chatList: async () => list,
      chatUpdate: async (u) => {
        updates.push(u);
        list = list.map((c) => (c.id === u.id ? { ...c, pinned: !!u.pinned } : c)).sort((a, b) => Number(b.pinned) - Number(a.pinned));
        return list.find((c) => c.id === u.id)!;
      },
    });
    await waitFor(() => expect(rowTitles(r)).toEqual(['First', 'Second']), r);
    await pickMenu(r, 'Second', 'Pin');
    await waitFor(() => expect(updates).toEqual([{ id: 'c2', pinned: true }]), r);
    // the list is read again after the change, without waiting for the core's chat.updated (the stream may be down)
    await waitFor(() => expect(rowTitles(r)).toEqual(['Second', 'First']), r);
    expect($(r, '[aria-label="Pinned chats"]')).not.toBeNull();
    // another window created a chat: the list follows the event
    list = [...list, item({ id: 'c3', title: 'From elsewhere' })];
    s.emit({ type: 'chat.created', data: { chatId: 'c3' } });
    await waitFor(() => expect(rowTitles(r)).toContain('From elsewhere'), r);
  });

  it('deletes a chat only after the confirmation, and leaves the deleted chat', async () => {
    let list = [item({ id: 'chat_a', title: 'Doomed', messageCount: 7 })];
    const deleted: string[] = [];
    const { r, s, navigated } = setup(
      {
        chatList: async () => list,
        chatGet: async () => ({ chat: chat({ title: 'Doomed' }), messages: [], hasMore: false }),
        chatDelete: async (id) => {
          deleted.push(id);
          list = [];
        },
      },
      { id: 'chat_a' },
    );
    await waitFor(() => expect(rowTitles(r)).toEqual(['Doomed']), r);
    await pickMenu(r, 'Doomed', 'Delete…');
    const dlg = await waitFor(() => dialogWith('Delete chat'), r);
    expect(dlg.textContent).toContain('all 7 of its messages');
    expect(deleted).toEqual([]);
    await r.act(() => byText(dlg, 'button', 'Delete chat').click());
    await waitFor(() => expect(deleted).toEqual(['chat_a']), r);
    await waitFor(() => expect(navigated).toEqual(['/chats']), r);
    await waitFor(() => expect(rows(r)).toHaveLength(0), r); // read again, no chat.deleted needed
    s.emit({ type: 'chat.deleted', data: { chatId: 'chat_a' } }); // the core announces it later
    await waitFor(() => expect(r.container.textContent).toContain('This chat was deleted.'), r);
  });

  it('shows a failed delete inside the dialog, and nothing in the list once it is closed', async () => {
    const { r } = setup({ chatList: async () => [item({ id: 'c1', title: 'Keep' })], chatDelete: async () => Promise.reject(new Error('a turn is running in this chat: stop it first')) });
    await waitFor(() => expect(rowTitles(r)).toEqual(['Keep']), r);
    await pickMenu(r, 'Keep', 'Delete…');
    const dlg = await waitFor(() => dialogWith('Delete chat'), r);
    await r.act(() => byText(dlg, 'button', 'Delete chat').click());
    await waitFor(() => expect(must(dlg.querySelector('[role="alert"]'), 'alert in the dialog').textContent).toContain('stop it first'), r);
    expect(rowTitles(r)).toEqual(['Keep']);
    await r.act(() => byText(dlg, 'button', 'Cancel').click());
    await waitFor(() => expect(doc$$('dialog[open]')).toHaveLength(0), r);
    expect(must$(r, '[aria-label="Chats"]').querySelector('[role="alert"]')).toBeNull();
    // opened again, the dialog starts without the old error
    await pickMenu(r, 'Keep', 'Delete…');
    const again = await waitFor(() => dialogWith('Delete chat'), r);
    expect(again.querySelector('[role="alert"]')).toBeNull();
  });

  it('keeps a pin error in the list when a dialog opens and closes', async () => {
    const { r } = setup({ chatList: async () => [item({ id: 'c1', title: 'Draft' })], chatUpdate: async () => Promise.reject(new Error('Drydock is read-only: no valid license')) });
    await waitFor(() => expect(rowTitles(r)).toEqual(['Draft']), r);
    await pickMenu(r, 'Draft', 'Pin');
    const list = must$(r, '[aria-label="Chats"]');
    await waitFor(() => expect(must(list.querySelector('[role="alert"]'), 'list alert').textContent).toContain('no valid license'), r);
    await pickMenu(r, 'Draft', 'Rename');
    const dlg = await waitFor(() => dialogWith('Rename chat'), r);
    expect(dlg.querySelector('[role="alert"]')).toBeNull(); // the list's error is not the dialog's
    await r.act(() => byText(dlg, 'button', 'Cancel').click());
    await waitFor(() => expect(doc$$('dialog[open]')).toHaveLength(0), r);
    expect(must(list.querySelector('[role="alert"]'), 'list alert').textContent).toContain('no valid license');
  });

  it('keeps every key typed into the rename box, and renames to the whole title (H12)', async () => {
    const updates: unknown[] = [];
    const { r } = setup({ chatList: async () => [item({ id: 'c1', title: 'Draft' })], chatUpdate: async (u) => (updates.push(u), chat({ id: 'c1', title: u.title })) });
    await waitFor(() => expect(rowTitles(r)).toEqual(['Draft']), r);
    await pickMenu(r, 'Draft', 'Rename');
    const dlg = await waitFor(() => dialogWith('Rename chat'), r);
    const box = byLabel<HTMLInputElement>(dlg, 'Title');
    await waitFor(() => expect(box.selectionStart === 0 && box.selectionEnd === box.value.length).toBe(true), r); // opens with the old title selected
    await typeKeys(box, 'Release notes', r);
    expect(box.value).toBe('Release notes');
    await r.act(() => byText(dlg, 'button', 'Save').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'c1', title: 'Release notes' }]), r);
  });

  it('shows a failed rename inside the dialog, not behind it', async () => {
    const { r } = setup({ chatList: async () => [item({ id: 'c1', title: 'Draft' })], chatUpdate: async () => Promise.reject(new Error('Drydock is read-only: no valid license')) });
    await waitFor(() => expect(rowTitles(r)).toEqual(['Draft']), r);
    await pickMenu(r, 'Draft', 'Rename');
    const dlg = await waitFor(() => dialogWith('Rename chat'), r);
    type(byLabel(dlg, 'Title'), 'Notes');
    await r.act(() => byText(dlg, 'button', 'Save').click());
    await waitFor(() => expect(dlg.textContent).toContain('no valid license'), r);
    expect(must(dlg.querySelector('[role="alert"]'), 'alert in the dialog')).toBeTruthy();
    // closed, the error goes with the dialog: the list shows no bare alert
    await r.act(() => byText(dlg, 'button', 'Cancel').click());
    await waitFor(() => expect(doc$$('dialog[open]')).toHaveLength(0), r);
    expect(must$(r, '[aria-label="Chats"]').querySelector('[role="alert"]')).toBeNull();
  });

  it('renames from the row menu and forks into a new chat, reading the list again after each', async () => {
    let list = [item({ id: 'c1', title: 'Draft' })];
    const updates: unknown[] = [];
    const forks: unknown[] = [];
    const { r, navigated } = setup({
      chatList: async () => list,
      chatUpdate: async (u) => {
        updates.push(u);
        list = list.map((c) => (c.id === u.id ? { ...c, title: u.title ?? '' } : c));
        return chat({ id: u.id, title: u.title ?? '' });
      },
      chatFork: async (id, upto) => {
        forks.push([id, upto]);
        list = [item({ id: 'c9', title: 'Release notes (fork)' }), ...list];
        return chat({ id: 'c9', forkedFrom: id });
      },
    });
    await waitFor(() => expect(rowTitles(r)).toEqual(['Draft']), r);
    await pickMenu(r, 'Draft', 'Rename');
    const dlg = await waitFor(() => dialogWith('Rename chat'), r);
    type(byLabel(dlg, 'Title'), 'Release notes');
    await r.act(() => byText(dlg, 'button', 'Save').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'c1', title: 'Release notes' }]), r);
    await waitFor(() => expect(rowTitles(r)).toEqual(['Release notes']), r); // no chat.updated was sent
    await pickMenu(r, 'Release notes', 'Fork');
    await waitFor(() => expect(navigated).toEqual(['/chats/c9']), r);
    expect(forks).toEqual([['c1', undefined]]);
    await waitFor(() => expect(rowTitles(r)).toEqual(['Release notes (fork)', 'Release notes']), r);
  });
});

describe('New chat dialog', () => {
  it('defaults to the current project and Claude Code, lists missing CLIs disabled with the reason', async () => {
    const created: unknown[] = [];
    const { r, navigated } = setup({ chatCreate: async (p) => (created.push(p), chat({ id: 'c_new' })) }, { newChat: true });
    const dlg = await waitFor(newChatDialog, r);
    const project = byLabel<HTMLElement>(dlg, 'Project');
    await waitFor(() => expect(valueOf(project)).toBe('prj_1'), r);
    const agent = byLabel<HTMLElement>(dlg, 'Agent CLI');
    expect(valueOf(agent)).toBe('claude');
    const opts = optionsOf(agent).map((o) => [o.label, o.disabled]);
    expect(opts).toEqual([
      ['Claude Code 2.1.295', false],
      ['Cline 3.0.70', false],
      ['Gemini CLI (not installed)', true],
    ]);
    expect(byLabel<HTMLInputElement>(dlg, /^Let this chat read my other chats/).checked).toBe(true);
    // the model field offers the adapter's models
    expect(optionsOf(byLabel<HTMLElement>(dlg, 'Model')).map((o) => o.value)).toEqual(['', 'opus', 'sonnet', 'haiku', OTHER_MODEL]);
    type(byLabel(dlg, 'Model'), 'opus');
    type(byLabel<HTMLElement>(dlg, 'Effort'), 'low');
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(created).toEqual([{ projectId: 'prj_1', cwd: undefined, adapter: 'claude', model: 'opus', effort: 'low', permissionMode: undefined, readHistory: true }]), r);
    // one navigation, straight to the new chat (no extra trip to /chats on the way)
    await waitFor(() => expect(navigated).toEqual(['/chats/c_new']), r);
  });

  it("the palette's New chat over an open chat keeps it: cancelling leaves it as it was, with no navigation", async () => {
    const { r, s, navigated } = setup({ chatList: async () => [item(chat())], chatGet: async () => ({ chat: chat(), messages: [msg(1, 'user', 'still here')], hasMore: false }) }, { id: 'chat_a' });
    await waitFor(() => expect(messages(r)).toHaveLength(1), r);
    expect(doc$$('dialog[open]')).toHaveLength(0);
    await r.act(() => s.ui.requestNewChat()); // the screen is already open
    const dlg = await waitFor(newChatDialog, r);
    expect(messages(r)).toHaveLength(1); // the conversation stays mounted behind the dialog
    await r.act(() => byText(dlg, 'button', 'Cancel').click());
    await waitFor(() => expect(doc$$('dialog[open]')).toHaveLength(0), r);
    expect(navigated).toEqual([]);
    expect(messages(r)).toHaveLength(1);
    // asked once, opened once: a later render does not reopen it
    await r.act(() => s.ui.setTheme('dark'));
    expect(doc$$('dialog[open]')).toHaveLength(0);
  });

  it('without a project sends the folder to the core, which decides, and shows its refusal', async () => {
    const created: { cwd?: string }[] = [];
    const { r, navigated } = setup(
      {
        chatCreate: async (p) => {
          created.push(p);
          if (p.cwd === 'notes\\weekly') throw new Error('the folder "notes\\weekly" is not an absolute path');
          return chat({ id: 'c_np', projectId: '' });
        },
      },
      { newChat: true },
    );
    const dlg = await waitFor(newChatDialog, r);
    await waitFor(() => expect(valueOf(byLabel(dlg, 'Project'))).toBe('prj_1'), r);
    type(byLabel<HTMLElement>(dlg, 'Project'), '');
    const folder = await waitFor(() => byLabel(dlg, 'Folder'), r);
    type(folder, 'notes\\weekly');
    // the browser does not guess the core's path rules: Start stays enabled and the core answers
    await waitFor(() => expect(byText<HTMLButtonElement>(dlg, 'button', 'Start chat').disabled).toBe(false), r);
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(must(dlg.querySelector('[role="alert"]'), 'alert').textContent).toContain('is not an absolute path'), r);
    expect(navigated).toEqual([]);
    type(folder, 'C:\\work\\notes');
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/c_np']), r);
    expect(created.map((c) => c.cwd)).toEqual(['notes\\weekly', 'C:\\work\\notes']);
    expect(created[1]).toEqual({ projectId: undefined, cwd: 'C:\\work\\notes', adapter: 'claude', model: undefined, effort: undefined, permissionMode: undefined, readHistory: true });
  });

  it('warns clearly when a permission mode that skips approvals is picked', async () => {
    const { r } = setup({}, { newChat: true });
    const dlg = await waitFor(newChatDialog, r);
    const mode = await waitFor(() => byLabel<HTMLElement>(dlg, 'Permission mode'), r);
    await waitFor(() => expect(valueOf(mode)).toBe('default'), r);
    expect(dlg.querySelector('[data-testid="skip-warning"]')).toBeNull();
    expect(optionsOf(mode).find((o) => o.value === 'bypassPermissions')!.label).toBe('bypassPermissions (skips approvals)');
    type(mode, 'bypassPermissions');
    const warn = await waitFor(() => must(dlg.querySelector<HTMLElement>('[data-testid="skip-warning"]'), 'warning'), r);
    expect(warn.getAttribute('role')).toBe('alert');
    expect(warn.textContent).toContain('without asking you');
    type(mode, 'acceptEdits');
    await waitFor(() => expect(dlg.querySelector('[data-testid="skip-warning"]')).toBeNull(), r);
  });

  it('lists Claude Code installed inside a WSL distro as its own choice and sends the distro on create (decision P41)', async () => {
    const created: unknown[] = [];
    const claudeWSL: AdapterInfo = { ...claude, capabilities: { ...claude.capabilities, runModes: ['native', 'wsl'] } };
    const conn = (distro: string, over: Partial<Connection> = {}) =>
      ({ adapter: 'claude', key: `claude@wsl:${distro}`, runMode: 'wsl', distro, installed: true, status: 'ready', usable: true, version: '2.1.296', platform: 'linux (WSL 2)', ...over }) as Connection;
    const { r, navigated } = setup(
      {
        adapterList: async () => [claudeWSL, gemini, cline],
        connectionList: async () => [conn('Ubuntu'), conn('Debian', { status: 'unauthenticated', usable: false })],
        chatCreate: async (p) => (created.push(p), chat({ id: 'c_wsl', projectId: '', wslDistro: 'Ubuntu' })),
      },
      { newChat: true },
    );
    const dlg = await waitFor(newChatDialog, r);
    await waitFor(() => expect(valueOf(byLabel(dlg, 'Project'))).toBe('prj_1'), r);
    type(byLabel<HTMLElement>(dlg, 'Project'), ''); // the owner's case: a chat with no project
    const agent = byLabel<HTMLElement>(dlg, 'Agent CLI');
    await waitFor(
      () =>
        expect(optionsOf(agent).map((o) => [o.label, o.disabled])).toEqual([
          ['Claude Code on Windows 2.1.295', false],
          ['Claude Code WSL (Debian) (not signed in)', true], // the distro only because two offer it
          ['Claude Code WSL (Ubuntu) 2.1.296', false],
          ['Cline 3.0.70', false],
          ['Gemini CLI (not installed)', true],
        ]),
      r,
    );
    expect(valueOf(agent)).toBe('claude'); // Windows stays the default
    type(agent, 'claude@wsl:Ubuntu');
    await waitFor(() => expect(dlg.textContent).toContain('Runs inside the WSL distro Ubuntu'), r);
    expect(byLabel<HTMLInputElement>(dlg, 'Folder').placeholder).toBe('/home/you/notes');
    // the WSL install has Claude Code's models and modes
    expect(optionsOf(byLabel<HTMLElement>(dlg, 'Model')).map((o) => o.value)).toEqual(['', 'opus', 'sonnet', 'haiku', OTHER_MODEL]);
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/c_wsl']), r);
    expect(created).toEqual([{ projectId: undefined, cwd: undefined, adapter: 'claude', wslDistro: 'Ubuntu', model: undefined, effort: undefined, permissionMode: undefined, readHistory: true }]);
  });

  it('names a chat in WSL "Claude Code WSL" on its adapter chip, the distro in its tooltip', async () => {
    const c = chat({ wslDistro: 'Ubuntu', cwd: '\\\\wsl.localhost\\Ubuntu\\home\\kyle\\.drydock\\chats\\chat_a' });
    const { r } = setup({ chatList: async () => [item(c)], chatGet: async () => ({ chat: c, messages: [msg(1, 'user', 'hi')], hasMore: false }) }, { id: 'chat_a' });
    // "Claude Code WSL", the distro in the chip's tooltip only
    const chip = await waitFor(() => must$(r, '[data-testid="chip-adapter"]'), r);
    await waitFor(() => expect(chip.textContent).toContain('Claude Code WSL'), r);
    expect(chip.textContent).not.toContain('Ubuntu');
    expect(chip.getAttribute('title')).toBe('Runs inside the WSL distro Ubuntu');
  });

  it('names the distro on the adapter chip of a chat in WSL when the CLI is installed in two distros, as the Connections page does', async () => {
    const conn = (distro: string, over: Partial<Connection> = {}) =>
      ({ adapter: 'claude', key: `claude@wsl:${distro}`, runMode: 'wsl', distro, installed: true, status: 'ready', usable: true, version: '2.1.296', platform: 'linux (WSL 2)', ...over }) as Connection;
    let installs = [conn('Ubuntu'), conn('Debian')];
    let reads = 0;
    const c = chat({ wslDistro: 'Debian', cwd: '\\\\wsl.localhost\\Debian\\home\\kyle\\.drydock\\chats\\chat_a' });
    const { r, s } = setup(
      {
        chatList: async () => [item(c)],
        chatGet: async () => ({ chat: c, messages: [msg(1, 'user', 'hi')], hasMore: false }),
        connectionList: async () => (reads++, installs),
      },
      { id: 'chat_a' },
    );
    const chip = await waitFor(() => must$(r, '[data-testid="chip-adapter"]'), r);
    await waitFor(() => expect(chip.textContent).toBe('Claude Code WSL (Debian)'), r);
    expect(chip.getAttribute('title')).toBe('Runs inside the WSL distro Debian');
    // the CLI is gone from the other distro (a check on the Connections page): the name drops the distro again
    installs = [conn('Ubuntu', { installed: false, status: 'not_installed', usable: false }), conn('Debian')];
    const before = reads;
    s.emit({ type: 'connection.checked' });
    await waitFor(() => expect(reads).toBeGreaterThan(before), r);
    await waitFor(() => expect(must$(r, '[data-testid="chip-adapter"]').textContent).toBe('Claude Code WSL'), r);
  });

  it('does not read the Connections for a chat on Windows', async () => {
    let reads = 0;
    const c = chat();
    const { r } = setup(
      { chatList: async () => [item(c)], chatGet: async () => ({ chat: c, messages: [msg(1, 'user', 'hi')], hasMore: false }), connectionList: async () => (reads++, []) },
      { id: 'chat_a' },
    );
    const chip = await waitFor(() => must$(r, '[data-testid="chip-adapter"]'), r);
    expect(chip.textContent).toBe('Claude Code');
    await new Promise((ok) => setTimeout(ok, 50));
    expect(reads).toBe(0);
  });

  it('shows the error the core gave', async () => {
    const { r } = setup({ chatCreate: async () => Promise.reject(new Error('the adapter claude has unacknowledged gates')) }, { newChat: true });
    const dlg = await waitFor(newChatDialog, r);
    await waitFor(() => expect(valueOf(byLabel(dlg, 'Agent CLI'))).toBe('claude'), r);
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(dlg.querySelector('[role="alert"]')!.textContent).toContain('unacknowledged gates'), r);
  });
});

describe('Conversation', () => {
  function conv(c: Chat, msgs: ChatMessage[], client: Partial<Client> = {}, hasMore = false) {
    return setup({ chatList: async () => [item(c)], chatGet: async () => ({ chat: c, messages: msgs, hasMore }), ...client }, { id: c.id });
  }

  it('shows the header: title, chips, folder, session, tokens and a cost that is never $0 when unknown', async () => {
    const { r } = conv(chat({ costUsd: null, costUnknownTurns: 2, permissionMode: 'bypassPermissions', skipsApprovals: true }), [msg(1, 'user', 'hi'), msg(2, 'assistant', 'Hello **there**')]);
    await waitFor(() => expect(messages(r)).toHaveLength(2), r);
    expect($(r, '[data-testid="chat-title"]')!.textContent).toBe('Where does Multiply go?');
    expect($(r, '[data-testid="chip-adapter"]')!.textContent).toContain('Claude Code');
    expect($(r, '[data-testid="chip-model"]')!.textContent).toBe('sonnet');
    expect($(r, '[data-testid="chip-effort"]')!.textContent).toBe('high effort');
    expect($(r, '[data-testid="chip-mode"]')!.getAttribute('data-warn')).toBe('true');
    expect($(r, '[data-testid="chip-mode"]')!.textContent).toContain('skips approvals');
    expect($(r, '[data-testid="chat-status"]')!.textContent).toBe('Session kept');
    // the folder and the usage are in Properties, not in the header strip (UI review P1-2)
    const header = must$(r, '[data-testid="chat-status"]').parentElement!;
    expect([header.querySelector('[data-testid="chat-cwd"]'), header.querySelector('[data-testid="chat-usage"]')]).toEqual([null, null]);
    await r.act(() => must$(r, '[data-testid="chat-properties-open"]').click());
    const props = await waitFor(() => must(document.querySelector<HTMLElement>('[data-testid="chat-properties-form"]'), 'properties'), r);
    expect(props.querySelector('[data-testid="chat-cwd"]')!.getAttribute('title')).toBe('C:\\repos\\gocalc');
    const usage = props.querySelector('[data-testid="chat-usage"]')!.textContent!;
    expect(usage).toContain('Usage');
    expect(usage).toContain('1.2k in');
    expect(usage).toContain('cost unknown');
    expect(usage).not.toContain('$0');
    await r.act(() => byText(props, 'button', 'Cancel').click());
    // markdown is rendered as elements, never raw HTML
    expect(messages(r)[1]!.querySelector('.md strong')!.textContent).toBe('there');
    const log = $(r, '[role="log"]')!;
    expect(log.getAttribute('aria-live')).toBe('polite');
  });

  it('renames inline', async () => {
    const updates: unknown[] = [];
    const { r } = conv(chat(), [], { chatUpdate: async (u) => (updates.push(u), chat({ title: u.title })) });
    const btn = await waitFor(() => must$(r, '[data-testid="chat-title"]'), r);
    await r.act(() => btn.click());
    const input = await waitFor(() => byLabel(r.container, 'Chat title'), r);
    type(input, 'Multiply plan');
    await r.act(() => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })));
    await waitFor(() => expect(updates).toEqual([{ id: 'chat_a', title: 'Multiply plan' }]), r);
    await waitFor(() => expect($(r, '[data-testid="chat-title"]')!.textContent).toBe('Multiply plan'), r);
  });

  it('keeps every key typed into the inline title box (H12)', async () => {
    const updates: unknown[] = [];
    const { r } = conv(chat(), [], { chatUpdate: async (u) => (updates.push(u), chat({ title: u.title })) });
    const btn = await waitFor(() => must$(r, '[data-testid="chat-title"]'), r);
    await r.act(() => btn.click());
    const input = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Chat title'), r);
    await waitFor(() => expect(input.selectionStart === 0 && input.selectionEnd === input.value.length).toBe(true), r);
    await typeKeys(input, 'Multiply plan', r);
    expect(input.value).toBe('Multiply plan');
    await r.act(() => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })));
    await waitFor(() => expect(updates).toEqual([{ id: 'chat_a', title: 'Multiply plan' }]), r);
  });

  it('keeps every key typed into the allow-rule box, and allows the whole rule (H12)', async () => {
    const allowed: unknown[] = [];
    const { r } = conv(chat(), [], { chatAllowTool: async (id, tool) => (allowed.push([id, tool]), chat({ allowedTools: [tool] })) });
    const btn = await waitFor(() => byText(r.container, 'button', /^Allowed tools/), r);
    await r.act(() => btn.click());
    const box = await waitFor(() => byLabel<HTMLInputElement>(r.container, 'Rule to allow'), r);
    await typeKeys(box, 'Bash(npm test)', r);
    expect(box.value).toBe('Bash(npm test)');
    await r.act(() => (box.form as HTMLFormElement).requestSubmit());
    await waitFor(() => expect(allowed).toEqual([['chat_a', 'Bash(npm test)']]), r);
  });

  it('streams a turn: deltas fill the in-progress bubble outside the live region, the stored message replaces it, chat.turn ends it', async () => {
    const sent: unknown[] = [];
    const { r, s } = conv(chat(), [msg(1, 'user', 'earlier'), msg(2, 'assistant', 'earlier answer')], { chatSend: async (id, text, attach) => (sent.push([id, text, attach]), { turnId: 'turn_2', version: 2 }) });
    await waitFor(() => expect(messages(r)).toHaveLength(2), r);
    const box = byLabel<HTMLTextAreaElement>(r.container, 'Message');
    type(box, 'explain Multiply');
    await r.act(() => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true, cancelable: true })));
    await waitFor(() => expect(sent).toEqual([['chat_a', 'explain Multiply', []]]), r);
    // running: Send is disabled, Stop is offered, the textarea is cleared
    await waitFor(() => expect($(r, '[data-testid="chat-stop"]')).not.toBeNull(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Send').disabled).toBe(true);
    expect(box.value).toBe('');
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(3, 'user', 'explain Multiply', { turnId: 'turn_2' }) } });
    s.emit({ type: 'chat.turn_started', data: { chatId: 'chat_a', turnId: 'turn_2', attach: [] } });
    s.emit({ type: 'chat.delta', data: { chatId: 'chat_a', turnId: 'turn_2', text: 'Multiply goes ' } });
    s.emit({ type: 'chat.delta', data: { chatId: 'chat_a', turnId: 'turn_2', text: 'in calc.go ' } });
    s.emit({ type: 'chat.delta', data: { chatId: 'other', turnId: 'x', text: 'NOT MINE' } });
    const bubble = await waitFor(() => {
      const b = must$(r, '[data-testid="chat-streaming"]');
      if (!b.textContent!.includes('in calc.go')) throw new Error('not yet');
      return b;
    }, r);
    expect(bubble.textContent).toContain('Multiply goes in calc.go');
    expect(bubble.textContent).not.toContain('NOT MINE');
    expect(bubble.closest('[role="log"]')).toBeNull(); // deltas are not announced
    expect(bubble.getAttribute('aria-live')).toBe('off');
    // the reasoning streams open under the typing, then folds away on the row it came before (P89)
    s.emit({ type: 'chat.thinking', data: { chatId: 'chat_a', turnId: 'turn_2', text: 'Multiply is next to Add. ' } });
    const live = await waitFor(() => must$(r, '[data-testid="chat-thinking-live"]') as HTMLDetailsElement, r);
    expect([live.open, live.querySelector('summary')!.textContent, live.textContent!.includes('next to Add')]).toEqual([true, 'Thinking…', true]);
    // a tool call, then the stored block replaces the streamed text
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(4, 'tool', 'Read: calc.go', { toolName: 'Read', toolInput: { file_path: 'calc.go' }, turnId: 'turn_2', thinking: 'Multiply is next to Add.' }) } });
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(5, 'assistant', 'Multiply goes in calc.go next to Add.', { turnId: 'turn_2' }) } });
    await waitFor(() => expect(messages(r).map((m) => m.getAttribute('data-role'))).toEqual(['user', 'assistant', 'user', 'tool', 'assistant']), r);
    // the person's own messages are a bubble on the right; the agent's answer is text on the left, neither with an avatar (P97)
    expect(messages(r).map((m) => m.getAttribute('data-mine'))).toEqual(['true', null, 'true', null, null]);
    expect(messages(r).map((m) => m.querySelector('[data-kind]'))).toEqual([null, null, null, null, null]);
    await waitFor(() => expect($(r, '[data-testid="chat-streaming"]')?.textContent ?? '').not.toContain('in calc.go'), r);
    const tool = $(r, '[data-testid="chat-tool"]') as HTMLDetailsElement;
    expect(tool.querySelector('summary')!.textContent).toContain('Read');
    expect(tool.querySelector('summary')!.textContent).toContain('calc.go');
    expect(tool.open).toBe(false);
    await waitFor(() => expect($(r, '[data-testid="chat-thinking-live"]')).toBeNull(), r);
    const fold = must$(r, '[data-testid="chat-thinking"]') as HTMLDetailsElement;
    expect([fold.open, fold.querySelector('summary')!.textContent, fold.textContent!.includes('Multiply is next to Add.')]).toEqual([false, 'Thinking', true]);
    expect(fold.closest('[data-testid="chat-message"]')!.getAttribute('data-role')).toBe('tool');
    // an answer reads in a column, however wide the window
    expect(getComputedStyle(messages(r)[4]!.querySelector('.md, [class*="md"]')?.parentElement ?? messages(r)[4]!).maxWidth).toBe('720px');
    expect(tool.querySelector('pre')!.textContent).toContain('"file_path": "calc.go"');
    s.emit({ type: 'chat.updated', data: { chatId: 'chat_a', chat: chat({ status: 'idle', tokensIn: 2000 }) } });
    s.emit({ type: 'chat.turn', data: { chatId: 'chat_a', turnId: 'turn_2', status: 'done', usage: { inputTokens: 800, outputTokens: 40, costUsd: 0.01 } } });
    await waitFor(() => expect($(r, '[data-testid="chat-streaming"]')).toBeNull(), r);
    await waitFor(() => expect($(r, '[data-testid="chat-stop"]')).toBeNull(), r);
    await r.act(() => must$(r, '[data-testid="chat-properties-open"]').click());
    await waitFor(() => expect(must(document.querySelector('[data-testid="chat-properties-form"] [data-testid="chat-usage"]'), 'usage').textContent).toContain('2.0k in'), r);
    await r.act(() => byText(document.querySelector<HTMLElement>('[data-testid="chat-properties-form"]')!, 'button', 'Cancel').click());
    type(box, 'next');
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Send').disabled).toBe(false), r);
  });

  it('stops a running turn', async () => {
    const stopped: string[] = [];
    const { r } = conv(chat({ status: 'running' }), [msg(1, 'user', 'long job')], { chatStop: async (id) => (stopped.push(id), { stopped: true }) });
    const stop = await waitFor(() => must$(r, '[data-testid="chat-stop"]'), r);
    expect($(r, '[data-testid="chat-streaming"]')).not.toBeNull();
    await r.act(() => stop.click());
    await waitFor(() => expect(stopped).toEqual(['chat_a']), r);
  });

  it('a refusal offers the suggested rule, Allow for this chat, then Retry', async () => {
    let c = chat();
    const allowed: unknown[] = [];
    const sent: unknown[] = [];
    const msgs = [
      msg(1, 'user', 'touch a probe file'),
      msg(2, 'tool', 'Bash: touch probe.txt', { toolName: 'Bash', toolInput: { command: 'touch probe.txt' } }),
      msg(3, 'assistant', 'I was not allowed to run that.'),
      msg(4, 'denied', 'Not allowed in this chat: Bash: touch probe.txt', { toolName: 'Bash', toolInput: { command: 'touch probe.txt' }, suggestedRule: 'Bash(touch probe.txt)' }),
    ];
    const { r } = conv(c, msgs, {
      chatAllowTool: async (id, tool) => {
        allowed.push([id, tool]);
        c = chat({ allowedTools: [tool] });
        return c;
      },
      chatSend: async (id, text, attach) => (sent.push([id, text, attach]), { turnId: 't3', version: 3 }),
    });
    const callout = await waitFor(() => must$(r, '[data-testid="chat-denied"]'), r);
    expect(callout.textContent).toContain('Claude was not allowed to run Bash');
    expect(callout.textContent).toContain('Bash(touch probe.txt)');
    // the newest refusal leads with Allow and retry (UI review P1-3); Only allow keeps the two steps apart
    expect(Array.from(callout.querySelectorAll('button')).map((b) => b.textContent)).toEqual(['Allow and retry', 'Only allow']);
    expect(byText(r.container, 'button', /Allowed tools/).getAttribute('aria-label')).toBe('Allowed tools');
    await r.act(() => byText(callout, 'button', 'Only allow').click());
    await waitFor(() => expect(allowed).toEqual([['chat_a', 'Bash(touch probe.txt)']]), r);
    await waitFor(() => expect(callout.textContent).toContain('Allowed for this chat'), r);
    expect(byText(r.container, 'button', /Allowed tools/).getAttribute('aria-label')).toBe('Allowed tools (1)');
    await r.act(() => byText(callout, 'button', 'Retry').click());
    await waitFor(() => expect(sent).toEqual([['chat_a', RETRY_TEXT, []]]), r);
  });

  it('Allow and retry allows the rule and runs the turn again in one step; the canned retry shows as a note (P1-2, P1-3)', async () => {
    let c = chat();
    const order: string[] = [];
    const msgs = [
      msg(1, 'user', 'touch a probe file'),
      msg(2, 'denied', 'Not allowed in this chat: Bash: touch probe.txt', { toolName: 'Bash', toolInput: { command: 'touch probe.txt' }, suggestedRule: 'Bash(touch probe.txt)' }),
    ];
    const { r, s } = conv(c, msgs, {
      chatAllowTool: async (_id, tool) => (order.push(`allow ${tool}`), (c = chat({ allowedTools: [tool] }))),
      chatSend: async (_id, text) => (order.push(`send ${text}`), { turnId: 't3', version: 3 }),
    });
    const callout = await waitFor(() => must$(r, '[data-testid="chat-denied"]'), r);
    await r.act(() => byText(callout, 'button', 'Allow and retry').click());
    await waitFor(() => expect(order).toEqual(['allow Bash(touch probe.txt)', `send ${RETRY_TEXT}`]), r);
    // the retry Drydock sent is a note in the log, not a bubble in the person's name
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(3, 'user', RETRY_TEXT, { turnId: 't3' }) } });
    const note = await waitFor(() => must$(r, '[data-retry="true"]'), r);
    expect(note.textContent).toBe('You allowed the tool and asked to continue.');
    const row = note.closest('[data-testid="chat-message"]')!;
    expect([row.getAttribute('data-role'), row.getAttribute('data-mine'), row.querySelector('.chat-tools')]).toEqual(['system', null, null]);
  });

  it('tool calls are cards: an edit as a diff, a command with its output, a failure marked, one still running spinning', async () => {
    const msgs = [
      msg(1, 'user', 'fix it'),
      msg(2, 'tool', 'Edit: calc.go', { toolName: 'Edit', toolInput: { file_path: 'calc.go', old_string: 'return 0', new_string: 'return a - b' }, toolStatus: 'ok', toolResult: 'The file was updated.', toolMs: 40 }),
      msg(3, 'tool', 'Bash: go test ./...', { toolName: 'Bash', toolInput: { command: 'go test ./...' }, toolStatus: 'failed', toolResult: 'FAIL: TestSubtract', toolMs: 1234 }),
      msg(4, 'tool', 'echo: hello', { toolName: 'mcp__drydock__chatmcp__echo', toolInput: { text: 'hello' } }),
    ];
    const { r } = conv(chat({ status: 'running' }), msgs);
    const cards = await waitFor(() => {
      const c = $$(r, '[data-testid="chat-tool"]');
      if (c.length !== 3) throw new Error('three cards');
      return c;
    }, r);
    const [edit, bash, echo] = cards as [HTMLElement, HTMLElement, HTMLElement];
    expect(edit.getAttribute('data-state')).toBe('ok');
    expect(edit.querySelector('summary strong')!.textContent).toBe('Edit');
    expect(edit.querySelector('[data-testid="chat-tool-ms"]')!.textContent).toBe('40 ms');
    expect(Array.from(edit.querySelectorAll('[data-testid="chat-tool-diff"] > div')).map((d) => [d.getAttribute('data-kind'), d.textContent])).toEqual([
      ['-', '- return 0'],
      ['+', '+ return a - b'],
    ]);
    expect(bash.getAttribute('data-state')).toBe('failed');
    expect(bash.getAttribute('aria-label')).toBe('Bash: failed');
    expect(bash.querySelector('[data-testid="chat-tool-command"]')!.textContent).toBe('$ go test ./...');
    expect(bash.querySelector('[data-testid="chat-tool-result"]')!.textContent).toBe('FAIL: TestSubtract');
    expect(bash.querySelector('[data-testid="chat-tool-ms"]')!.textContent).toBe('1.2 s');
    expect(echo.getAttribute('data-state')).toBe('running'); // no result yet and its turn runs
    expect(echo.querySelector('summary strong')!.textContent).toBe('chatmcp · echo');
    expect(echo.querySelector('[role="status"]')).not.toBeNull();
  });

  it('a call that reported no result says so once its turn has ended', async () => {
    const { r } = conv(chat(), [msg(1, 'user', 'look'), msg(2, 'tool', 'Read: a.go', { toolName: 'Read', toolInput: { file_path: 'a.go' } })]);
    const card = await waitFor(() => must$(r, '[data-testid="chat-tool"]'), r);
    expect(card.getAttribute('data-state')).toBe('unknown');
    expect(card.textContent).toContain('No result was reported for this call.');
  });

  it('a request the agent waits on is an amber card where it was made, answered with Allow once or Deny (P97)', async () => {
    const answered: unknown[] = [];
    const remembered: unknown[] = [];
    let waiting = [
      { id: 'cask_1', chatId: 'chat_a', turnId: 't1', messageId: 'm2', tool: 'Run go test', summary: 'Run go test: go test ./...', input: { command: 'go test ./...' }, created: Date.now(), expiresAt: Date.now() + 30 * 60_000 },
      { id: 'cask_2', chatId: 'chat_a', turnId: 't1', messageId: 'm_older', tool: 'Fetch', summary: 'Fetch: https://go.dev', input: { url: 'https://go.dev' }, created: Date.now() + 1, expiresAt: Date.now() + 30 * 60_000 },
    ];
    const { r } = conv(chat({ adapter: 'gemini' }), [msg(1, 'user', 'run the tests'), msg(2, 'system', 'Asks to use Run go test: go test ./...')], {
      chatAsks: async () => waiting,
      chatAnswer: async (id, askId, allow) => {
        answered.push([id, askId, allow]);
        waiting = waiting.filter((a) => a.id !== askId);
      },
      chatAllowAndRemember: async (id, askId) => (remembered.push([id, askId]), { answered: true, rule: '' }),
    });
    const cards = await waitFor(() => {
      const c = $$(r, '[data-testid="chat-ask"]');
      if (c.length !== 2) throw new Error('two requests');
      return c;
    }, r);
    // the first is in the conversation, in the place of the note that records it; the other's note is not loaded: above the composer
    expect(cards[0]!.closest('[data-testid="chat-message"]')!.getAttribute('data-seq')).toBe('2');
    expect(cards[1]!.closest('[data-testid="chat-message"]')).toBeNull();
    expect(cards[1]!.closest('[data-testid="chat-asks"]')).not.toBeNull();
    expect(cards[0]!.getAttribute('aria-label')).toBe('Gemini CLI wants to run a command, waiting on you');
    expect(cards[0]!.textContent).toContain('waiting on you');
    expect(cards[0]!.querySelector('[data-testid="chat-ask-input"]')!.textContent).toBe('go test ./...');
    expect(cards[1]!.textContent).toContain('Gemini CLI wants to use Fetch');
    expect(cards[1]!.querySelector('[data-testid="chat-ask-input"]')!.textContent).toBe('https://go.dev');
    expect(cards[0]!.querySelector('[data-testid="chat-ask-expiry"]')!.textContent).toMatch(/^Refused at .+ if nobody answers\.$/);
    // the core names no rule for an agent without per-chat rules: no "Allow ... in this chat"
    expect(Array.from(cards[0]!.querySelectorAll('button')).map((b) => b.textContent)).toEqual(['Allow once', 'Deny']);
    await r.act(() => byText(cards[0]!, 'button', 'Allow once').click());
    await waitFor(() => expect(answered).toEqual([['chat_a', 'cask_1', true]]), r);
    await waitFor(() => expect($$(r, '[data-testid="chat-ask"]')).toHaveLength(1), r);
    // answered, the note is a quiet line again
    expect(messages(r)[1]!.textContent).toContain('Asks to use Run go test');
    await r.act(() => byText(must$(r, '[data-testid="chat-ask"]'), 'button', 'Deny').click());
    await waitFor(() => expect(answered).toEqual([['chat_a', 'cask_1', true], ['chat_a', 'cask_2', false]]), r);
    await waitFor(() => expect($(r, '[data-testid="chat-asks"]')).toBeNull(), r);
    expect(remembered).toEqual([]);
  });

  it('"Allow <tool> in this chat" remembers the call\'s rule (chat.answer remember), and shows the core\'s reason when it cannot (P97)', async () => {
    const remembered: unknown[] = [];
    let refuse = true;
    let waiting = [
      { id: 'cask_1', chatId: 'chat_a', turnId: 't1', messageId: 'm2', tool: 'Bash', summary: 'Bash: git diff --stat', input: { command: 'git diff --stat' }, rule: 'Bash(git diff --stat)', created: Date.now(), expiresAt: Date.now() + 30 * 60_000 },
    ];
    const { r } = conv(chat(), [msg(1, 'user', 'what changed?'), msg(2, 'system', 'Asks to use Bash: git diff --stat')], {
      chatAsks: async () => waiting,
      chatAllowAndRemember: async (id, askId) => {
        remembered.push([id, askId]);
        if (refuse) throw new Error('the call\'s input was shortened or hides a secret, so no rule can name it exactly: allow it once instead');
        waiting = [];
        return { answered: true, rule: 'Bash(git diff --stat)' };
      },
    });
    const card = await waitFor(() => must$(r, '[data-testid="chat-ask"]'), r);
    expect(card.getAttribute('aria-label')).toBe('Claude Code wants to run a command, waiting on you');
    expect(Array.from(card.querySelectorAll('button')).map((b) => b.textContent)).toEqual(['Allow once', 'Allow git diff --stat in this chat', 'Deny']);
    expect(must$(r, '[data-testid="chat-ask-remember"]').getAttribute('title')).toContain('Bash(git diff --stat)');
    await r.act(() => byText(card, 'button', 'Allow git diff --stat in this chat').click());
    await waitFor(() => expect(remembered).toEqual([['chat_a', 'cask_1']]), r);
    // refused: the reason is on the request, which still waits to be allowed once or denied
    await waitFor(() => expect(must(card.querySelector('[role="alert"]'), 'reason').textContent).toContain('allow it once instead'), r);
    expect(byText<HTMLButtonElement>(card, 'button', 'Allow once').disabled).toBe(false);
    refuse = false;
    await r.act(() => byText(card, 'button', 'Allow git diff --stat in this chat').click());
    await waitFor(() => expect(remembered).toHaveLength(2), r);
    await waitFor(() => expect($(r, '[data-testid="chat-ask"]')).toBeNull(), r);
  });

  it('lists the allowed tools and removes one', async () => {
    const removed: unknown[] = [];
    const { r } = conv(chat({ allowedTools: ['Bash(npm test)', 'Edit'] }), [], { chatDisallowTool: async (id, tool) => (removed.push([id, tool]), chat({ allowedTools: ['Edit'] })) });
    const btn = await waitFor(() => byText(r.container, 'button', 'Allowed tools (2)'), r);
    await r.act(() => btn.click());
    const pop = await waitFor(() => {
      const p = must$(r, '[data-testid="chat-allowed-tools"]');
      if (!p.textContent!.includes('Bash(npm test)')) throw new Error('closed');
      return p;
    }, r);
    await r.act(() => byText(pop, 'button', 'Remove Bash(npm test)').click());
    await waitFor(() => expect(removed).toEqual([['chat_a', 'Bash(npm test)']]), r);
    await waitFor(() => expect(byText(r.container, 'button', 'Allowed tools (1)')).toBeDefined(), r);
  });

  it('changes the settings from the model button, a mode that skips approvals only once confirmed', async () => {
    const updates: unknown[] = [];
    const { r } = conv(chat(), [], { chatUpdate: async (u) => (updates.push(u), chat({ ...u, skipsApprovals: u.permissionMode === 'bypassPermissions' } as Partial<Chat>)) });
    const btn = await waitFor(() => must$(r, '[data-testid="chat-settings-open"]'), r);
    expect(btn.closest('[data-testid="chat-composer"]')).not.toBeNull(); // in the composer's row
    expect(btn.getAttribute('aria-label')).toBe('Model sonnet: chat settings');
    expect(must$(r, '[data-testid="chip-model"]').textContent).toBe('sonnet');
    expect(must$(r, '[data-testid="chip-effort"]').textContent).toBe('high effort');
    await r.act(() => btn.click());
    const pop = await waitFor(() => {
      const p = must$(r, '[data-testid="chat-settings"]');
      if (!p.querySelector('form')) throw new Error('closed');
      return p;
    }, r);
    await waitFor(() => expect(valueOf(byLabel(pop, 'Permission mode'))).toBe('default'), r);
    type(byLabel(pop, 'Model'), 'opus');
    type(byLabel<HTMLElement>(pop, 'Permission mode'), 'bypassPermissions');
    await waitFor(() => expect(pop.querySelector('[data-testid="skip-approvals-warning"]')).not.toBeNull(), r);
    // Save waits for the confirmation (lib/permission.ts)
    expect(byText<HTMLButtonElement>(pop, 'button', 'Save').disabled).toBe(true);
    await r.act(() => byLabel(pop, 'I understand this agent will act without asking me first').click());
    await waitFor(() => expect(byText<HTMLButtonElement>(pop, 'button', 'Save').disabled).toBe(false), r);
    await r.act(() => byText(pop, 'button', 'Save').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'chat_a', model: 'opus', effort: 'high', permissionMode: 'bypassPermissions', readHistory: true }]), r);
    await waitFor(() => expect($(r, '[data-testid="chip-mode"]')!.getAttribute('data-warn')).toBe('true'), r);
  });

  it('the settings popover takes the focus on its model field when it opens and Escape closes it', async () => {
    const { r } = conv(chat(), []);
    const btn = await waitFor(() => must$(r, '[data-testid="chat-settings-open"]') as HTMLButtonElement, r);
    await r.act(() => btn.click());
    const model = await waitFor(() => {
      const el = must$(r, '[data-testid="chat-settings"]').querySelector<HTMLElement>('[role="combobox"]');
      if (!el || document.activeElement !== el) throw new Error('not focused yet');
      return el;
    }, r);
    await r.act(() => model.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })));
    await waitFor(() => expect(must$(r, '[data-testid="chat-settings"]').querySelector('form')).toBeNull(), r);
    expect(btn.getAttribute('aria-expanded')).toBe('false');
  });

  it('attaches other chats as removable chips and sends their ids', async () => {
    const sent: unknown[] = [];
    const others = [item({ id: 'chat_a' }), item({ id: 'c2', title: 'Overflow edge cases' }), item({ id: 'c3', title: 'Commit message' })];
    const { r } = setup(
      {
        chatList: async () => others,
        chatGet: async () => ({ chat: chat(), messages: [], hasMore: false }),
        chatSend: async (id, text, attach) => (sent.push([id, text, attach]), { turnId: 't', version: 2 }),
      },
      { id: 'chat_a' },
    );
    const open = await waitFor(() => byText(r.container, 'button', 'Attach chats'), r);
    await r.act(() => open.click());
    const picker = await waitFor(() => {
      const p = must$(r, '[data-testid="attach-picker"]');
      if (p.querySelectorAll('[data-testid="attach-option"]').length !== 2) throw new Error('not loaded');
      return p;
    }, r);
    // the chat itself is not offered
    expect(Array.from(picker.querySelectorAll('[data-testid="attach-option"] .t')).map((e) => e.textContent)).toEqual(['Overflow edge cases', 'Commit message']);
    expect(picker.textContent).toContain('for this message only');
    await r.act(() => (picker.querySelectorAll('[data-testid="attach-option"]')[0] as HTMLElement).click());
    await r.act(() => (picker.querySelectorAll('[data-testid="attach-option"]')[1] as HTMLElement).click());
    await waitFor(() => expect($$(r, '[data-testid="attach-chip"]').map((e) => e.textContent!.trim())).toEqual(['Overflow edge cases', 'Commit message']), r);
    expect($(r, '[aria-label="Attached chats"]')!.textContent).toContain('Their history is given to Claude for this message only.');
    await r.act(() => byText(r.container, 'button', 'Remove attached chat Commit message').click());
    await waitFor(() => expect($$(r, '[data-testid="attach-chip"]')).toHaveLength(1), r);
    type(byLabel<HTMLTextAreaElement>(r.container, 'Message'), 'compare with the overflow chat');
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Send').disabled).toBe(false), r);
    await r.act(() => byText(r.container, 'button', 'Send').click());
    await waitFor(() => expect(sent).toEqual([['chat_a', 'compare with the overflow chat', ['c2']]]), r);
    await waitFor(() => expect($$(r, '[data-testid="attach-chip"]')).toHaveLength(0), r);
  });

  it("searches every chat for the picker, and an attached chat's chip keeps its title when it leaves the list", async () => {
    const asked: unknown[] = [];
    const recent = [item({ id: 'chat_a' }), item({ id: 'c2', title: 'Overflow edge cases' })];
    const old = item({ id: 'c_old', title: 'Release notes from last year', archived: true });
    const { r } = setup(
      {
        chatList: async (p) => (asked.push(p), p?.query ? [old] : recent),
        chatGet: async () => ({ chat: chat(), messages: [], hasMore: false }),
      },
      { id: 'chat_a' },
    );
    const open = await waitFor(() => byText(r.container, 'button', 'Attach chats'), r);
    await r.act(() => open.click());
    const picker = must$(r, '[data-testid="attach-picker"]');
    const titles = () => Array.from(picker.querySelectorAll('[data-testid="attach-option"] .t')).map((e) => e.textContent);
    await waitFor(() => expect(titles()).toEqual(['Overflow edge cases']), r);
    type(byLabel(picker, 'Filter chats'), 'release');
    // not in the recent chats shown: the core's search finds it (archived chats included)
    await waitFor(() => expect(titles()).toEqual(['Release notes from last year']), r);
    expect(asked.find((a) => (a as { query?: string } | undefined)?.query)).toEqual({ query: 'release', includeArchived: true, limit: 100 });
    await r.act(() => (picker.querySelector('[data-testid="attach-option"]') as HTMLElement).click());
    type(byLabel(picker, 'Filter chats'), '');
    await waitFor(() => expect(titles()).toEqual(['Overflow edge cases']), r);
    expect($$(r, '[data-testid="attach-chip"]').map((e) => e.textContent!.trim())).toEqual(['Release notes from last year']);
    expect(byText(r.container, 'button', 'Remove attached chat Release notes from last year')).toBeDefined();
  });

  it('a send scrolls to the latest message even when its turn ended before chat.send answered', async () => {
    const latest = Array.from({ length: 40 }, (_, i) => msg(1 + i, i % 2 ? 'assistant' : 'user', `line ${1 + i}`));
    let emit: (e: { type: string; data: unknown }) => void = () => {};
    const { r, s } = conv(chat(), latest, {
      chatSend: async () => {
        // the CLI could not start: the turn is over before the answer arrives
        emit({ type: 'chat.turn_started', data: { chatId: 'chat_a', turnId: 't5', attach: [] } });
        emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(41, 'error', 'the CLI could not start') } });
        emit({ type: 'chat.turn', data: { chatId: 'chat_a', turnId: 't5', status: 'error' } });
        return { turnId: 't5', version: 3 };
      },
    });
    emit = (e) => s.emit(e);
    await waitFor(() => expect(messages(r)).toHaveLength(40), r);
    const scroller = must$(r, '[data-testid="chat-scroll"]');
    scroller.style.flex = 'none';
    scroller.style.height = '200px';
    scroller.style.overflowY = 'auto';
    await r.act(() => {
      scroller.scrollTop = 0;
      scroller.dispatchEvent(new Event('scroll'));
    });
    await waitFor(() => must$(r, '[data-testid="chat-jump"]'), r);
    type(byLabel<HTMLTextAreaElement>(r.container, 'Message'), 'go');
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Send').disabled).toBe(false), r);
    await r.act(() => byText(r.container, 'button', 'Send').click());
    // the list is windowed: the newest row is rendered at the bottom, the oldest are not in the DOM
    await waitFor(() => expect(messages(r).at(-1)!.getAttribute('data-seq')).toBe('41'), r);
    expect(messages(r).length).toBeLessThan(41);
    await waitFor(() => expect($(r, '[data-testid="chat-jump"]')).toBeNull(), r);
    await waitFor(() => expect(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight).toBeLessThan(40), r);
    expect($(r, '[data-testid="chat-stop"]')).toBeNull();
  });

  it('forks the whole chat from the header, or from one message', async () => {
    const forks: unknown[] = [];
    const { r, navigated } = conv(chat(), [msg(1, 'user', 'first'), msg(2, 'assistant', 'answer')], { chatFork: async (id, upto) => (forks.push([id, upto]), chat({ id: `fork${forks.length}` })) });
    await waitFor(() => expect(messages(r)).toHaveLength(2), r);
    await r.act(() => byText(r.container, 'button', 'Fork').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/fork1']), r);
    await r.act(() => byText(messages(r)[0]!, 'button', 'Fork from here').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/fork1', '/chats/fork2']), r);
    expect(forks).toEqual([
      ['chat_a', undefined],
      ['chat_a', 1],
    ]);
  });

  it('edits a message of the person and sends it again, and regenerates the newest answer (P89)', async () => {
    const resent: unknown[] = [];
    const { r, s } = conv(chat(), [msg(1, 'user', 'first'), msg(2, 'assistant', 'one answer'), msg(3, 'user', 'second'), msg(4, 'assistant', 'newest answer')], {
      chatResend: async (id, seq, text) => (resent.push([id, seq, text]), { turnId: `turn_r${resent.length}`, version: 9 }),
    });
    await waitFor(() => expect(messages(r)).toHaveLength(4), r);
    // Edit on the person's messages only; Regenerate on the newest answer only
    const labels = (i: number) => Array.from(messages(r)[i]!.querySelectorAll('.chat-tools button')).map((b) => b.getAttribute('aria-label'));
    expect([labels(0), labels(1), labels(2), labels(3)]).toEqual([
      ['Edit and send again', 'Fork from here'],
      ['Fork from here'],
      ['Edit and send again', 'Fork from here'],
      ['Regenerate', 'Fork from here'],
    ]);
    await r.act(() => byText(messages(r)[3]!, 'button', 'Regenerate').click());
    await waitFor(() => expect(resent).toEqual([['chat_a', 3, undefined]]), r);
    // while that turn runs nothing can be sent again; once it ends, Edit is back
    await waitFor(() => expect(labels(0)).toEqual(['Fork from here']), r);
    s.emit({ type: 'chat.turn', data: { chatId: 'chat_a', turnId: 'turn_r1', status: 'done', fallback: true } });
    await waitFor(() => expect(labels(0)).toEqual(['Edit and send again', 'Fork from here']), r);

    // Edit opens the message in place with what it says, and says what sending does
    await r.act(() => byText(messages(r)[0]!, 'button', 'Edit and send again').click());
    const box = byLabel<HTMLTextAreaElement>(r.container, 'Edit your message');
    expect(box.value).toBe('first');
    expect(must$(r, '[data-testid="chat-edit"]').textContent).toContain('replaces this message and everything after it');
    type(box, 'first, edited');
    await r.act(() => byText(r.container, 'button', 'Send again').click());
    await waitFor(() => expect(resent).toEqual([['chat_a', 3, undefined], ['chat_a', 1, 'first, edited']]), r);
    await waitFor(() => expect($(r, '[data-testid="chat-edit"]')).toBeNull(), r);
    // the core says the messages from seq 1 on were replaced: the feed drops them until the new ones arrive
    s.emit({ type: 'chat.rewound', data: { chatId: 'chat_a', fromSeq: 1 } });
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(1, 'user', 'first, edited', { id: 'm_new' }) } });
    await waitFor(() => expect(messages(r).map((m) => m.textContent!.includes('first, edited'))).toEqual([true]), r);
  });

  it('sends files with a message, shows them on it and reads them back on request (P90)', async () => {
    const sent: unknown[] = [];
    const read: string[] = [];
    const withFile = msg(3, 'user', 'see the notes', { attachments: [{ id: 'catt_1', name: 'notes.txt', mime: 'text/plain', size: 5, kind: 'text' }] });
    const { r } = conv(chat(), [msg(1, 'user', 'first'), msg(2, 'assistant', 'answer'), withFile], {
      chatSend: async (id, text, attach, files) => (sent.push([id, text, attach, (files ?? []).map((f) => [f.name, new TextDecoder().decode(f.data)])]), { turnId: 'turn_f', version: 4 }),
      chatAttachment: async (id) => (read.push(id), { id, name: 'notes.txt', mime: 'text/plain', size: 5, kind: 'text', data: new TextEncoder().encode('hello') }),
    });
    await waitFor(() => expect(messages(r)).toHaveLength(3), r);
    // the stored message lists its file as a card; Save reads it through the core
    const card = must$(r, '[data-testid="attachment"]');
    expect(card.textContent).toContain('notes.txt');
    await r.act(() => byText(card, 'button', 'Save').click());
    await waitFor(() => expect(read).toEqual(['catt_1']), r);
    await waitFor(() => expect(card.querySelector('a[download="notes.txt"]')).not.toBeNull(), r);

    // the composer takes files from its button, shows them as chips that can be removed, and sends them with the text
    const input = must$(r, '[data-testid="chat-file"]') as HTMLInputElement;
    const dt = new DataTransfer();
    dt.items.add(new File(['one'], 'a.txt', { type: 'text/plain' }));
    dt.items.add(new File(['two'], 'b.txt', { type: 'text/plain' }));
    input.files = dt.files;
    await r.act(() => input.dispatchEvent(new Event('change', { bubbles: true })));
    await waitFor(() => expect(Array.from(r.container.querySelectorAll('[data-testid="file-chip"]')).map((c) => c.querySelector('.name')!.textContent)).toEqual(['a.txt', 'b.txt']), r);
    await r.act(() => byText(r.container, 'button', 'Remove attached file b.txt').click());
    const box = byLabel<HTMLTextAreaElement>(r.container, 'Message');
    type(box, 'read a.txt');
    await r.act(() => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true, cancelable: true })));
    await waitFor(() => expect(sent).toEqual([['chat_a', 'read a.txt', [], [['a.txt', 'one']]]]), r);
    await waitFor(() => expect(r.container.querySelectorAll('[data-testid="file-chip"]')).toHaveLength(0), r);

    // more than four files are refused in the composer, before anything is read or sent
    const many = new DataTransfer();
    for (const n of ['1', '2', '3', '4', '5']) many.items.add(new File([n], `${n}.txt`));
    input.files = many.files;
    await r.act(() => input.dispatchEvent(new Event('change', { bubbles: true })));
    await waitFor(() => expect(must$(r, '[data-testid="chat-composer"]').textContent).toContain('At most 4 files go with one message.'), r);
    expect(r.container.querySelectorAll('[data-testid="file-chip"]')).toHaveLength(0);
  });

  it('loads older messages before the oldest one shown', async () => {
    const latest = Array.from({ length: 5 }, (_, i) => msg(201 + i, i % 2 ? 'assistant' : 'user', `recent ${201 + i}`));
    const asked: unknown[] = [];
    const { r } = conv(chat(), latest, {
      chatMessages: async (id, before, limit) => {
        asked.push([id, before, limit]);
        return before === 201
          ? { messages: Array.from({ length: 100 }, (_, i) => msg(101 + i, 'user', `old ${101 + i}`)), hasMore: true }
          : { messages: [msg(1, 'user', 'the very first')], hasMore: false };
      },
    }, true);
    await waitFor(() => expect(messages(r)).toHaveLength(5), r);
    const older = await waitFor(() => must$(r, '[data-testid="chat-older"]'), r);
    await r.act(() => older.click());
    await waitFor(() => expect(messages(r)).toHaveLength(105), r);
    expect(messages(r)[0]!.getAttribute('data-seq')).toBe('101');
    expect(asked).toEqual([['chat_a', 201, 100]]);
    await r.act(() => $(r, '[data-testid="chat-older"]')!.click());
    await waitFor(() => expect(messages(r)).toHaveLength(106), r);
    expect(asked[1]).toEqual(['chat_a', 101, 100]);
    // the core says when the start was reached
    await waitFor(() => expect($(r, '[data-testid="chat-older"]')).toBeNull(), r);
    expect(r.container.textContent).toContain('This is the start of');
  });

  it('counts only messages that arrive below as new, not older pages loaded above', async () => {
    const latest = Array.from({ length: 40 }, (_, i) => msg(201 + i, i % 2 ? 'assistant' : 'user', `recent ${201 + i}`));
    const { r, s } = conv(chat(), latest, { chatMessages: async () => ({ messages: Array.from({ length: 100 }, (_, i) => msg(101 + i, 'user', `old ${101 + i}`)), hasMore: true }) }, true);
    await waitFor(() => expect(messages(r)).toHaveLength(40), r);
    const scroller = must$(r, '[data-testid="chat-scroll"]');
    scroller.style.flex = 'none'; // a fixed, small viewport so the conversation scrolls
    scroller.style.height = '200px';
    scroller.style.overflowY = 'auto';
    // scroll to the top: the view leaves the bottom and the older page is read
    await r.act(() => {
      scroller.scrollTop = 0;
      scroller.dispatchEvent(new Event('scroll'));
    });
    // the older page lands above the view (rows of it are rendered in the overscan); the view stays where it was
    await waitFor(() => expect(messages(r).some((m) => Number(m.getAttribute('data-seq')) < 201)).toBe(true), r);
    const jump = await waitFor(() => must$(r, '[data-testid="chat-jump"]'), r);
    expect(jump.textContent).toContain('Jump to the latest');
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(241, 'assistant', 'fresh') } });
    await waitFor(() => expect(must$(r, '[data-testid="chat-jump"]').textContent).toContain('1 new message'), r);
    // the new row is below the window, so the log cannot announce it: a status says the count
    expect(must$(r, '[data-testid="chat-unseen"]').textContent).toBe('1 new message below');
  });

  it('renders only the rows near the view of a long chat, from the bottom, and keeps the DOM small while scrolling (F6)', async () => {
    // 2,000 stored messages of different heights (a long answer every ninth)
    const all = Array.from({ length: 2000 }, (_, i) => msg(1 + i, i % 2 ? 'assistant' : 'user', i % 9 ? `line ${1 + i}` : `long ${1 + i}\n\n${'an answer that wraps over lines '.repeat(30)}`));
    const { r } = conv(chat(), all);
    // the screen fills a window of 700 px, as in the app
    r.container.style.height = '700px';
    r.container.style.display = 'flex';
    const scroller = await waitFor(() => must$(r, '[data-testid="chat-scroll"]'), r);
    const rows = () => $$(r, '[data-vkey]');
    const atBottom = () => scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 1;
    await waitFor(() => expect(messages(r).at(-1)!.getAttribute('data-seq')).toBe('2000'), r);
    await waitFor(() => expect(atBottom()).toBe(true), r);
    const view = scroller.clientHeight;
    expect(view).toBeGreaterThan(200);
    expect(view).toBeLessThan(700);
    const bound = Math.ceil((4 * view + 600) / 20);
    expect(rows().length).toBeLessThanOrEqual(bound);
    // to the top, as a reader does (the browser's own scroll event)
    const moved = new Promise((res) => scroller.addEventListener('scroll', res, { once: true }));
    scroller.scrollTop = 0;
    await moved;
    await waitFor(() => expect(messages(r)[0]!.getAttribute('data-seq')).toBe('1'), r);
    expect(rows().length).toBeLessThanOrEqual(bound);
    await waitFor(() => expect(must$(r, '[data-testid="chat-jump"]').textContent).toContain('Jump to the latest'), r);
    await r.act(() => must$(r, '[data-testid="chat-jump"]').click());
    await waitFor(() => expect(messages(r).at(-1)!.getAttribute('data-seq')).toBe('2000'), r);
    await waitFor(() => expect(atBottom()).toBe(true), r);
    expect(rows().length).toBeLessThanOrEqual(bound);
  });

  it('streams a long answer over a long chat without drawing the conversation again, and stays at the bottom while the reader is there', async () => {
    // the review of 2026-10-27: every delta redrew and measured each row in the window, so a long chat made a streamed answer
    // stutter and keys typed meanwhile wait; now only the streamed answer redraws
    const all = Array.from({ length: 400 }, (_, i) => msg(1 + i, i % 2 ? 'assistant' : 'user', `line ${1 + i}`));
    const { r, s } = conv(chat({ status: 'running' }), all);
    r.container.style.height = '700px';
    r.container.style.display = 'flex';
    const scroller = await waitFor(() => must$(r, '[data-testid="chat-scroll"]'), r);
    const atBottom = () => scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 1;
    await waitFor(() => expect(messages(r).at(-1)!.getAttribute('data-seq')).toBe('400'), r);
    await waitFor(() => expect(atBottom()).toBe(true), r);
    s.emit({ type: 'chat.turn_started', data: { chatId: 'chat_a', turnId: 'turn_9', attach: [] } });
    s.emit({ type: 'chat.delta', data: { chatId: 'chat_a', turnId: 'turn_9', text: 'Part 0 of the answer.\n\n' } });
    await waitFor(() => expect(must$(r, '[data-testid="chat-streaming"]').textContent).toContain('Part 0'), r);
    // from here on the conversation is not drawn again: count the list's renders while 30 deltas stream
    const rows = VirtualList.prototype.rows;
    let drawn = 0;
    VirtualList.prototype.rows = function (this: VirtualList, ...a: Parameters<typeof rows>) {
      drawn++;
      return rows.apply(this, a);
    } as typeof rows;
    try {
      for (let i = 1; i <= 30; i++) s.emit({ type: 'chat.delta', data: { chatId: 'chat_a', turnId: 'turn_9', text: `Part ${i} of the answer, a paragraph that wraps over a line or two in the column.\n\n` } });
      await waitFor(() => expect(must$(r, '[data-testid="chat-streaming"]').textContent).toContain('Part 30 of the answer'), r);
      await waitFor(() => expect(atBottom()).toBe(true), r);
      expect(drawn).toBe(0);
    } finally {
      VirtualList.prototype.rows = rows;
    }
    // the reader scrolls up: more text below does not move what they read
    const moved = new Promise((res) => scroller.addEventListener('scroll', res, { once: true }));
    scroller.scrollTop = 100;
    await moved;
    await waitFor(() => must$(r, '[data-testid="chat-jump"]'), r);
    const top = scroller.scrollTop;
    s.emit({ type: 'chat.delta', data: { chatId: 'chat_a', turnId: 'turn_9', text: 'Part 31, read by nobody yet.\n\n' } });
    await waitFor(() => expect(must$(r, '[data-testid="chat-streaming"]').textContent).toContain('Part 31'), r);
    expect(Math.abs(scroller.scrollTop - top)).toBeLessThan(1);
  });

  it('shows that a chat deleted elsewhere is gone', async () => {
    const { r, s } = conv(chat(), [msg(1, 'user', 'hi')]);
    await waitFor(() => expect(messages(r)).toHaveLength(1), r);
    s.emit({ type: 'chat.deleted', data: { chatId: 'chat_a' } });
    await waitFor(() => expect(r.container.textContent).toContain('This chat was deleted.'), r);
    expect($(r, '[data-testid="chat-composer"]')).toBeNull();
  });

  it('renders system notes and errors quietly', async () => {
    const { r } = conv(chat(), [msg(1, 'user', 'hi'), msg(2, 'system', 'Stopped by you.'), msg(3, 'error', 'the CLI exited with code 1')]);
    await waitFor(() => expect(messages(r)).toHaveLength(3), r);
    expect(messages(r)[1]!.getAttribute('aria-label')).toBe('Note: Stopped by you.');
    expect(messages(r)[2]!.getAttribute('aria-label')).toBe('Error: the CLI exited with code 1');
    expect(messages(r)[1]!.querySelector('.md')).toBeNull();
  });

  it('says why an archived chat takes no messages', async () => {
    const { r } = conv(chat({ archived: true }), [msg(1, 'user', 'old')]);
    await waitFor(() => expect(r.container.textContent).toContain('This chat is archived.'), r);
    expect(byLabel<HTMLTextAreaElement>(r.container, 'Message').disabled).toBe(true);
  });
});

describe("The palette's New chat", () => {
  /** Run with the page at `path` (the palette reads where it is), then put the address back. */
  async function at(path: string, fn: () => Promise<void>) {
    const was = window.location.pathname + window.location.search + window.location.hash;
    history.replaceState(history.state, '', path);
    try {
      await fn();
    } finally {
      history.replaceState(history.state, '', was);
    }
  }
  async function choose(r: R, s: { ui: { setPalette(open: boolean): void } }, label: string) {
    await r.act(() => s.ui.setPalette(true));
    const input = byLabel<HTMLInputElement>(r.container, 'Command');
    await r.act(() => type(input, label));
    await r.act(() => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })));
  }

  it('opens the dialog over the open chat, without navigating', async () => {
    await at('/chats/chat_a', async () => {
      const { r, s, navigated } = setup({ chatList: async () => [item(chat())], chatGet: async () => ({ chat: chat(), messages: [msg(1, 'user', 'still here')], hasMore: false }) }, { id: 'chat_a', palette: true });
      await waitFor(() => expect(messages(r)).toHaveLength(1), r);
      await choose(r, s, 'New chat');
      await waitFor(newChatDialog, r);
      expect(navigated).toEqual([]);
      expect(messages(r)).toHaveLength(1);
      expect(s.ui.paletteOpen).toBe(false);
    });
  });

  it('from another screen goes to Chats, which opens the dialog', async () => {
    await at('/tasks', async () => {
      const s = testServices({
        projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project],
        licenseStatus: async () => lic(),
        appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
        adapterList: async () => [claude],
        chatList: async () => [],
      });
      s.workspace.start();
      const navigated: string[] = [];
      const pal = mount(<CommandPalette onMessage={() => {}} onNavigate={(h) => void navigated.push(h)} />, s);
      await choose(pal, s, 'New chat');
      await waitFor(() => expect(navigated).toEqual(['/chats']), pal);
      // the route renders the Chats screen, which takes the request
      const r = mount(<ChatsScreen onNavigate={(h) => void navigated.push(h)} />, s);
      await waitFor(newChatDialog, r);
    });
  });
});

describe('The graphite Chats screen (P97)', () => {
  function conv(c: Chat, msgs: ChatMessage[], client: Partial<Client> = {}) {
    return setup({ chatList: async () => [item(c)], chatGet: async () => ({ chat: c, messages: msgs, hasMore: false }), ...client }, { id: c.id });
  }
  const conn = (distro: string) =>
    ({ adapter: 'claude', key: `claude@wsl:${distro}`, runMode: 'wsl', distro, installed: true, status: 'ready', usable: true, version: '2.1.296', platform: 'linux (WSL 2)' }) as Connection;
  const press = (target: EventTarget, key: string, init: KeyboardEventInit = {}) => {
    const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init });
    target.dispatchEvent(e);
    return e;
  };

  it('lists each chat with its title and a faint line: the project, the CLI ("Claude Code WSL") and when; the open one highlighted', async () => {
    const now = Date.now();
    const list = [
      item({ id: 'chat_a', title: 'Why does WSL git see every file as changed?', projectId: '', wslDistro: 'Ubuntu', updatedAt: now - 10_000 }),
      item({ id: 'c2', title: 'Explain the routing rules', adapter: 'codex', updatedAt: now - 5 * 60_000 }),
      item({ id: 'c3', title: 'Long job', status: 'running' }),
    ];
    const { r } = setup({ chatList: async () => list, connectionList: async () => [conn('Ubuntu')], chatGet: async () => ({ chat: chat(list[0]), messages: [], hasMore: false }) }, { id: 'chat_a' });
    await waitFor(() => expect(rows(r).map((x) => x.querySelector('.line')!.textContent)).toEqual(['Claude Code WSL · now', 'gocalc · Codex · 5 min', 'gocalc · Claude Coderunning']), r);
    // the title is the row's first line, and the open chat is the current page
    expect(rows(r)[0]!.querySelector('.title > span')!.textContent).toBe('Why does WSL git see every file as changed?');
    expect(rows(r).map((x) => x.querySelector('a')!.getAttribute('aria-current'))).toEqual(['page', null, null]);
    expect(rows(r)[2]!.querySelector('[data-testid="chat-running"]')).not.toBeNull();
    // the column: "Chats" and New with its shortcut
    const col = must$(r, '[aria-label="Chats"]');
    expect(col.querySelector('h1')!.textContent).toBe('Chats');
    const btn = must$(r, '[data-testid="chat-new"]');
    expect([btn.textContent, btn.getAttribute('aria-label'), btn.getAttribute('aria-keyshortcuts'), btn.querySelector('kbd')!.textContent]).toEqual(['NewCtrl+N', 'New chat', 'Control+N', 'Ctrl+N']);
  });

  it('Ctrl+N opens the new chat dialog, but not while typing in a field', async () => {
    const { r } = setup({ chatList: async () => [item({ id: 'c1', title: 'One' })] });
    await waitFor(() => expect(rows(r)).toHaveLength(1), r);
    // typing in the search box: Ctrl+N is the field's (and the browser's), not the screen's
    const box = byLabel(r.container, 'Search chats');
    const typed = press(box, 'n', { ctrlKey: true });
    await new Promise((ok) => setTimeout(ok, 30));
    expect([typed.defaultPrevented, doc$$('dialog[open]').length]).toEqual([false, 0]);
    // a plain N does nothing
    press(document.body, 'n');
    await new Promise((ok) => setTimeout(ok, 30));
    expect(doc$$('dialog[open]')).toHaveLength(0);
    let e: KeyboardEvent | undefined;
    await r.act(() => void (e = press(document.body, 'n', { ctrlKey: true })));
    expect(e!.defaultPrevented).toBe(true);
    await waitFor(newChatDialog, r);
  });

  it('the header says the CLI with its state, the folder as the CLI sees it and where the chat runs, from its fields', async () => {
    const wsl = chat({ projectId: '', wslDistro: 'Ubuntu', cwd: '\\\\wsl.localhost\\Ubuntu\\home\\kyle\\.drydock\\chats\\chat_a' });
    const { r } = conv(wsl, [msg(1, 'user', 'hi')], { connectionList: async () => [conn('Ubuntu')] });
    await waitFor(() => expect(must$(r, '[data-testid="chip-adapter"]').textContent).toBe('Claude Code WSL'), r);
    const header = must$(r, '[data-testid="chat-view"] > header');
    expect(must$(r, '[data-testid="chat-folder"]').textContent).toBe('~/.drydock/chats/chat_a');
    expect(must$(r, '[data-testid="chat-folder"]').getAttribute('title')).toBe(wsl.cwd);
    expect(must$(r, '[data-testid="chat-place"]').textContent).toBe('No project · runs inside the distro');
    // the state is the dot (green: a session is kept); its words are for readers
    const status = must$(r, '[data-testid="chat-status"]');
    expect([status.textContent, status.className, status.parentElement!.getAttribute('data-state')]).toEqual(['Session kept', 'sr-only', 'session']);
    expect(header.contains(status)).toBe(true);
  });

  it("a project chat runs in the project's folder; a running turn shows on the chip", async () => {
    const { r } = conv(chat({ status: 'running' }), [msg(1, 'user', 'long job')]);
    await waitFor(() => expect(must$(r, '[data-testid="chat-place"]').textContent).toBe("gocalc · runs in the project's folder"), r);
    expect(must$(r, '[data-testid="chat-folder"]').textContent).toBe('C:\\repos\\gocalc');
    const status = must$(r, '[data-testid="chat-status"]');
    expect([status.textContent, status.className, status.parentElement!.getAttribute('data-state')]).toEqual(['Running', '', 'running']);
  });

  it('the approval mode button says the mode in words and changes it, a mode that skips approvals only once confirmed', async () => {
    const updates: unknown[] = [];
    const { r } = conv(chat(), [msg(1, 'user', 'hi')], { chatUpdate: async (u) => (updates.push(u), chat({ ...u, skipsApprovals: u.permissionMode === 'bypassPermissions' } as Partial<Chat>)) });
    const mode = await waitFor(() => must$(r, '[data-testid="chip-mode"]'), r);
    expect([mode.tagName, mode.textContent, mode.getAttribute('data-warn')]).toEqual(['BUTTON', 'Asks before commands', null]);
    await r.act(() => mode.click());
    const pop = await waitFor(() => {
      const p = must$(r, '[data-testid="chat-mode-settings"]');
      if (!p.querySelector('form')) throw new Error('closed');
      return p;
    }, r);
    type(byLabel<HTMLElement>(pop, 'Permission mode'), 'bypassPermissions');
    await waitFor(() => expect(pop.querySelector('[data-testid="skip-approvals-warning"]')).not.toBeNull(), r);
    expect(byText<HTMLButtonElement>(pop, 'button', 'Save').disabled).toBe(true);
    await r.act(() => byLabel(pop, 'I understand this agent will act without asking me first').click());
    await waitFor(() => expect(byText<HTMLButtonElement>(pop, 'button', 'Save').disabled).toBe(false), r);
    await r.act(() => byText(pop, 'button', 'Save').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'chat_a', permissionMode: 'bypassPermissions' }]), r);
    await waitFor(() => expect(must$(r, '[data-testid="chip-mode"]').textContent).toBe('Never asks, skips approvals'), r);
    expect(must$(r, '[data-testid="chip-mode"]').getAttribute('data-warn')).toBe('true');
  });

  it('Move to a project lists the projects, says what happens to the folder and moves the chat (chat.update projectId)', async () => {
    const updates: unknown[] = [];
    let fail = true;
    const home = chat({ projectId: '', cwd: 'C:\\Users\\kyle\\.drydock\\chats\\chat_a' });
    const { r } = conv(home, [msg(1, 'user', 'hi')], {
      chatUpdate: async (u) => {
        updates.push(u);
        if (fail) throw new Error('stop the running turn before moving the chat to another project');
        return chat({ projectId: u.projectId, cwd: 'C:\\repos\\payments-api', sessionId: '' });
      },
    });
    await waitFor(() => expect(must$(r, '[data-testid="chat-place"]').textContent).toBe('No project · runs in its own folder'), r);
    const move = must$(r, '[data-testid="chat-move"]');
    expect(move.textContent).toBe('Move to a project');
    expect(move.closest('[data-testid="chat-composer"]')).not.toBeNull();
    await r.act(() => move.click());
    const dlg = await waitFor(() => dialogWith('Move to a project'), r);
    const project = byLabel<HTMLElement>(dlg, 'Project');
    expect(optionsOf(project).map((o) => o.label)).toEqual(['gocalc', 'payments-api']);
    expect(valueOf(project)).toBe('prj_1');
    expect(must(dlg.querySelector('[data-testid="chat-move-effect"]'), 'effect').textContent).toBe(
      "It joins gocalc and runs in the project's folder. When that is another folder, the next message starts a new session, given the conversation so far. Its allow rules, skills and MCP servers stay.",
    );
    type(project, 'prj_2');
    await waitFor(() => expect(dlg.textContent).toContain('It joins payments-api'), r);
    // the core's refusal stays in the dialog
    await r.act(() => byText(dlg, 'button', 'Move').click());
    await waitFor(() => expect(must(dlg.querySelector('[role="alert"]'), 'refusal').textContent).toContain('stop the running turn'), r);
    fail = false;
    await r.act(() => byText(dlg, 'button', 'Move').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'chat_a', projectId: 'prj_2' }, { id: 'chat_a', projectId: 'prj_2' }]), r);
    await waitFor(() => expect(doc$$('dialog[open]')).toHaveLength(0), r);
    await waitFor(() => expect(must$(r, '[data-testid="chat-place"]').textContent).toBe("payments-api · runs in the project's folder"), r);
    expect(must$(r, '[data-testid="chat-move"]').textContent).toBe('Move to another project');
  });

  it('a chat in a project can be taken out of it, and nothing moves while a turn runs', async () => {
    const updates: unknown[] = [];
    const { r, s } = conv(chat(), [msg(1, 'user', 'hi')], { chatUpdate: async (u) => (updates.push(u), chat({ projectId: '' })) });
    const move = await waitFor(() => must$(r, '[data-testid="chat-move"]') as HTMLButtonElement, r);
    await r.act(() => move.click());
    const dlg = await waitFor(() => dialogWith('Move to another project'), r);
    const project = byLabel<HTMLElement>(dlg, 'Project');
    expect(optionsOf(project).map((o) => o.label)).toEqual(['payments-api', 'No project']);
    type(project, '__none');
    await waitFor(() => expect(dlg.textContent).toContain('It leaves the project and keeps the folder it runs in, so its session continues.'), r);
    await r.act(() => byText(dlg, 'button', 'Move').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'chat_a', projectId: '' }]), r);
    // a running turn: the button is off
    s.emit({ type: 'chat.updated', data: { chatId: 'chat_a', chat: chat({ projectId: '', status: 'running', version: 5 }) } });
    await waitFor(() => expect((must$(r, '[data-testid="chat-move"]') as HTMLButtonElement).disabled).toBe(true), r);
  });

  it('Enter sends, Shift+Enter is a new line; the send button reads "↵ send"', async () => {
    const sent: unknown[] = [];
    const { r } = conv(chat(), [msg(1, 'user', 'hi'), msg(2, 'assistant', 'hello')], { chatSend: async (id, text) => (sent.push([id, text]), { turnId: 't2', version: 2 }) });
    const box = await waitFor(() => byLabel<HTMLTextAreaElement>(r.container, 'Message'), r);
    expect(box.placeholder).toBe('Reply to Claude Code');
    const send = byText<HTMLButtonElement>(r.container, 'button', 'Send');
    expect([send.textContent, send.getAttribute('aria-keyshortcuts'), send.querySelector('kbd')!.textContent]).toEqual(['↵send', 'Enter', '↵']);
    type(box, 'first line');
    let shift: KeyboardEvent | undefined;
    await r.act(() => void (shift = press(box, 'Enter', { shiftKey: true })));
    expect(shift!.defaultPrevented).toBe(false);
    expect(sent).toEqual([]);
    await r.act(() => press(box, 'Enter'));
    await waitFor(() => expect(sent).toEqual([['chat_a', 'first line']]), r);
  });
});
