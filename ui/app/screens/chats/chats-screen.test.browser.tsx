import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Chat, ChatHit, ChatListItem, ChatMessage } from '../../api/chatClient.ts';
import type { AdapterInfo, Project } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { lic } from '../../test/fixtures.ts';
import { ChatsScreen } from './chats-screen.tsx';
import { onWindows, RETRY_TEXT } from './chats.ts';

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

function setup(client: Partial<Client>, props: { id?: string; newChat?: boolean } = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project, { id: 'prj_2', name: 'payments-api' } as Project],
    licenseStatus: async () => lic(),
    appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
    adapterList: async () => [claude, gemini, cline],
    chatList: async () => [],
    ...client,
  });
  s.workspace.start();
  const navigated: string[] = [];
  const r = mount(<ChatsScreen id={props.id} newChat={props.newChat} onNavigate={(h) => void navigated.push(h)} />, s);
  return { s, r, navigated };
}

const rows = (r: R) => $$(r, '[data-testid="chat-row"]');
const rowTitles = (r: R) => rows(r).map((e) => e.querySelector('.title > span')!.textContent);
const messages = (r: R) => $$(r, '[data-testid="chat-message"]');

async function pickMenu(r: R, rowTitle: string, label: string) {
  const trigger = await waitFor(() => byText(r.container, 'button', `Actions for ${rowTitle}`), r);
  await r.act(() => trigger.click());
  const it = await waitFor(() => {
    const el = $$(r, '[popover]:popover-open [role^="menuitem"]').find((e) => e.textContent!.trim() === label);
    if (!el) throw new Error(`no menu item ${label}`);
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
    s.emit({ type: 'chat.updated', data: { chatId: 'c2' } }); // the core announces the change; the list follows it
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
    s.emit({ type: 'chat.deleted', data: { chatId: 'chat_a' } }); // the core announces it
    await waitFor(() => expect(navigated).toEqual(['/chats']), r);
    await waitFor(() => expect(rows(r)).toHaveLength(0), r);
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
  });

  it('renames from the row menu and forks into a new chat', async () => {
    const list = [item({ id: 'c1', title: 'Draft' })];
    const updates: unknown[] = [];
    const forks: unknown[] = [];
    const { r, navigated } = setup({
      chatList: async () => list,
      chatUpdate: async (u) => (updates.push(u), chat({ id: u.id, title: u.title ?? '' })),
      chatFork: async (id, upto) => (forks.push([id, upto]), chat({ id: 'c9', forkedFrom: id })),
    });
    await waitFor(() => expect(rowTitles(r)).toEqual(['Draft']), r);
    await pickMenu(r, 'Draft', 'Rename');
    const dlg = await waitFor(() => dialogWith('Rename chat'), r);
    type(byLabel(dlg, 'Title'), 'Release notes');
    await r.act(() => byText(dlg, 'button', 'Save').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'c1', title: 'Release notes' }]), r);
    await new Promise((res) => setTimeout(res, 300)); // the menu finishes closing after a selection
    await pickMenu(r, 'Draft', 'Fork');
    await waitFor(() => expect(navigated).toEqual(['/chats/c9']), r);
    expect(forks).toEqual([['c1', undefined]]);
  });
});

describe('New chat dialog', () => {
  it('defaults to the current project and Claude Code, lists missing CLIs disabled with the reason', async () => {
    const created: unknown[] = [];
    const { r, navigated } = setup({ chatCreate: async (p) => (created.push(p), chat({ id: 'c_new' })) }, { newChat: true });
    const dlg = await waitFor(newChatDialog, r);
    const project = byLabel<HTMLSelectElement>(dlg, 'Project');
    await waitFor(() => expect(project.value).toBe('prj_1'), r);
    const agent = byLabel<HTMLSelectElement>(dlg, 'Agent CLI');
    expect(agent.value).toBe('claude');
    const opts = Array.from(agent.options).map((o) => [o.textContent, o.disabled]);
    expect(opts).toEqual([
      ['Claude Code 2.1.295', false],
      ['Cline 3.0.70', false],
      ['Gemini CLI (not installed)', true],
    ]);
    expect(byLabel<HTMLInputElement>(dlg, /^Let this chat read my other chats/).checked).toBe(true);
    // the model field offers the adapter's models
    expect(Array.from(dlg.querySelectorAll('datalist option')).map((o) => o.getAttribute('value'))).toEqual(['opus', 'sonnet', 'haiku']);
    type(byLabel(dlg, 'Model'), 'opus');
    type(byLabel<HTMLSelectElement>(dlg, 'Effort'), 'low');
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(created).toEqual([{ projectId: 'prj_1', cwd: undefined, adapter: 'claude', model: 'opus', effort: 'low', permissionMode: undefined, readHistory: true }]), r);
    // one navigation, straight to the new chat (no extra trip to /chats on the way)
    await waitFor(() => expect(navigated).toEqual(['/chats/c_new']), r);
  });

  it('New chat over an open chat keeps it: cancelling goes back to it without "?new"', async () => {
    const { r, navigated } = setup({ chatList: async () => [item(chat())], chatGet: async () => ({ chat: chat(), messages: [msg(1, 'user', 'still here')], hasMore: false }) }, { id: 'chat_a', newChat: true });
    const dlg = await waitFor(newChatDialog, r);
    await waitFor(() => expect(messages(r)).toHaveLength(1), r); // the conversation stays mounted behind the dialog
    await r.act(() => byText(dlg, 'button', 'Cancel').click());
    await waitFor(() => expect(navigated).toEqual(['/chats/chat_a']), r);
    expect(messages(r)).toHaveLength(1);
  });

  it('without a project takes an absolute folder (or none) and refuses a relative one', async () => {
    const created: unknown[] = [];
    const { r } = setup({ chatCreate: async (p) => (created.push(p), chat({ id: 'c_np', projectId: '' })) }, { newChat: true });
    const dlg = await waitFor(newChatDialog, r);
    await waitFor(() => expect(byLabel<HTMLSelectElement>(dlg, 'Project').value).toBe('prj_1'), r);
    type(byLabel<HTMLSelectElement>(dlg, 'Project'), '');
    const folder = await waitFor(() => byLabel(dlg, 'Folder'), r);
    type(folder, 'notes\\weekly');
    await waitFor(() => expect(dlg.textContent).toContain('Enter an absolute folder path'), r);
    expect(byText<HTMLButtonElement>(dlg, 'button', 'Start chat').disabled).toBe(true);
    const abs = onWindows() ? 'C:\\work\\notes' : '/work/notes'; // what the core's filepath.IsAbs accepts here
    type(folder, abs);
    await waitFor(() => expect(byText<HTMLButtonElement>(dlg, 'button', 'Start chat').disabled).toBe(false), r);
    expect(dlg.textContent).not.toContain('Enter an absolute folder path');
    await r.act(() => byText(dlg, 'button', 'Start chat').click());
    await waitFor(() => expect(created).toHaveLength(1), r);
    expect(created[0]).toEqual({ projectId: undefined, cwd: abs, adapter: 'claude', model: undefined, effort: undefined, permissionMode: undefined, readHistory: true });
  });

  it('warns clearly when a permission mode that skips approvals is picked', async () => {
    const { r } = setup({}, { newChat: true });
    const dlg = await waitFor(newChatDialog, r);
    const mode = await waitFor(() => byLabel<HTMLSelectElement>(dlg, 'Permission mode'), r);
    await waitFor(() => expect(mode.value).toBe('default'), r);
    expect(dlg.querySelector('[data-testid="skip-warning"]')).toBeNull();
    expect(Array.from(mode.options).find((o) => o.value === 'bypassPermissions')!.textContent).toBe('bypassPermissions (skips approvals)');
    type(mode, 'bypassPermissions');
    const warn = await waitFor(() => must(dlg.querySelector<HTMLElement>('[data-testid="skip-warning"]'), 'warning'), r);
    expect(warn.getAttribute('role')).toBe('alert');
    expect(warn.textContent).toContain('without asking you');
    type(mode, 'acceptEdits');
    await waitFor(() => expect(dlg.querySelector('[data-testid="skip-warning"]')).toBeNull(), r);
  });

  it('shows the error the core gave', async () => {
    const { r } = setup({ chatCreate: async () => Promise.reject(new Error('the adapter claude has unacknowledged gates')) }, { newChat: true });
    const dlg = await waitFor(newChatDialog, r);
    await waitFor(() => expect(byLabel<HTMLSelectElement>(dlg, 'Agent CLI').value).toBe('claude'), r);
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
    expect($(r, '[data-testid="chat-cwd"]')!.getAttribute('title')).toBe('C:\\repos\\gocalc');
    expect($(r, '[data-testid="chat-status"]')!.textContent).toBe('Session kept');
    const usage = $(r, '[data-testid="chat-usage"]')!.textContent!;
    expect(usage).toContain('1.2k in');
    expect(usage).toContain('cost unknown');
    expect(usage).not.toContain('$0');
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

  it('streams a turn: deltas fill the in-progress bubble outside the live region, the stored message replaces it, chat.turn ends it', async () => {
    const sent: unknown[] = [];
    const { r, s } = conv(chat(), [msg(1, 'user', 'earlier'), msg(2, 'assistant', 'earlier answer')], { chatSend: async (id, text, attach) => (sent.push([id, text, attach]), { turnId: 'turn_2' }) });
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
    // a tool call, then the stored block replaces the streamed text
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(4, 'tool', 'Read: calc.go', { toolName: 'Read', toolInput: { file_path: 'calc.go' }, turnId: 'turn_2' }) } });
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(5, 'assistant', 'Multiply goes in calc.go next to Add.', { turnId: 'turn_2' }) } });
    await waitFor(() => expect(messages(r).map((m) => m.getAttribute('data-role'))).toEqual(['user', 'assistant', 'user', 'tool', 'assistant']), r);
    await waitFor(() => expect($(r, '[data-testid="chat-streaming"]')?.textContent ?? '').not.toContain('in calc.go'), r);
    const tool = $(r, '[data-testid="chat-tool"]') as HTMLDetailsElement;
    expect(tool.querySelector('summary')!.textContent).toContain('Read');
    expect(tool.querySelector('summary')!.textContent).toContain('calc.go');
    expect(tool.open).toBe(false);
    expect(tool.querySelector('pre')!.textContent).toContain('"file_path": "calc.go"');
    s.emit({ type: 'chat.updated', data: { chatId: 'chat_a', chat: chat({ status: 'idle', tokensIn: 2000 }) } });
    s.emit({ type: 'chat.turn', data: { chatId: 'chat_a', turnId: 'turn_2', status: 'done', usage: { inputTokens: 800, outputTokens: 40, costUsd: 0.01 } } });
    await waitFor(() => expect($(r, '[data-testid="chat-streaming"]')).toBeNull(), r);
    await waitFor(() => expect($(r, '[data-testid="chat-stop"]')).toBeNull(), r);
    expect($(r, '[data-testid="chat-usage"]')!.textContent).toContain('2.0k in');
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
      chatSend: async (id, text, attach) => (sent.push([id, text, attach]), { turnId: 't3' }),
    });
    const callout = await waitFor(() => must$(r, '[data-testid="chat-denied"]'), r);
    expect(callout.textContent).toContain('Claude was not allowed to run Bash');
    expect(callout.textContent).toContain('Bash(touch probe.txt)');
    expect(callout.querySelector('button')!.textContent).toBe('Allow for this chat');
    expect(byText(r.container, 'button', /Allowed tools/).textContent).toBe('Allowed tools');
    await r.act(() => byText(callout, 'button', 'Allow for this chat').click());
    await waitFor(() => expect(allowed).toEqual([['chat_a', 'Bash(touch probe.txt)']]), r);
    await waitFor(() => expect(callout.textContent).toContain('Allowed for this chat'), r);
    expect(byText(r.container, 'button', /Allowed tools/).textContent).toBe('Allowed tools (1)');
    await r.act(() => byText(callout, 'button', 'Retry').click());
    await waitFor(() => expect(sent).toEqual([['chat_a', RETRY_TEXT, []]]), r);
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

  it('changes the settings through the chips popover, with the skip-approvals warning', async () => {
    const updates: unknown[] = [];
    const { r } = conv(chat(), [], { chatUpdate: async (u) => (updates.push(u), chat({ ...u, skipsApprovals: u.permissionMode === 'bypassPermissions' } as Partial<Chat>)) });
    const btn = await waitFor(() => byText(r.container, 'button', 'Chat settings'), r);
    await r.act(() => btn.click());
    const pop = await waitFor(() => {
      const p = must$(r, '[data-testid="chat-settings"]');
      if (!p.querySelector('form')) throw new Error('closed');
      return p;
    }, r);
    await waitFor(() => expect(byLabel<HTMLSelectElement>(pop, 'Permission mode').value).toBe('default'), r);
    type(byLabel(pop, 'Model'), 'opus');
    type(byLabel<HTMLSelectElement>(pop, 'Permission mode'), 'bypassPermissions');
    await waitFor(() => expect(pop.querySelector('[data-testid="skip-warning"]')).not.toBeNull(), r);
    await r.act(() => byText(pop, 'button', 'Save').click());
    await waitFor(() => expect(updates).toEqual([{ id: 'chat_a', model: 'opus', effort: 'high', permissionMode: 'bypassPermissions', readHistory: true }]), r);
    await waitFor(() => expect($(r, '[data-testid="chip-mode"]')!.getAttribute('data-warn')).toBe('true'), r);
  });

  it('a header popover takes the focus when it opens and Escape closes it', async () => {
    const { r } = conv(chat(), []);
    const btn = await waitFor(() => byText<HTMLButtonElement>(r.container, 'button', 'Chat settings'), r);
    await r.act(() => btn.click());
    const model = await waitFor(() => {
      const el = must$(r, '[data-testid="chat-settings"]').querySelector('input');
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
        chatSend: async (id, text, attach) => (sent.push([id, text, attach]), { turnId: 't' }),
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
    await waitFor(() => expect(messages(r)).toHaveLength(140), r);
    const jump = await waitFor(() => must$(r, '[data-testid="chat-jump"]'), r);
    expect(jump.textContent).toContain('Jump to the latest');
    s.emit({ type: 'chat.message', data: { chatId: 'chat_a', message: msg(241, 'assistant', 'fresh') } });
    await waitFor(() => expect(must$(r, '[data-testid="chat-jump"]').textContent).toContain('1 new message'), r);
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
