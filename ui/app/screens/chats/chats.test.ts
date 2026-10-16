import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Chat, ChatMessage } from '../../api/chatClient.ts';
import { createChatClient } from '../../api/chatClient.ts';
import type { AdapterInfo, Connection } from '../../api/types.ts';
import { isWorkspaceRoute, parseRoute } from '../../lib/route.ts';
import { routes } from '../../routes.ts';
import { buildItems, isChatsPath } from '../../shell/palette-items.ts';
import { EventStore } from '../../state/eventStore.ts';
import { ev, FakeTransport } from '../../test/fakes.ts';
import { ChatFeed } from './chat-feed.ts';
import {
  adapterOptionText,
  adapterUnavailable,
  chatChoices,
  chatWhere,
  compactNumber,
  costText,
  mergeMessages,
  pickChatChoice,
  relTime,
  shortPath,
  snippetParts,
  sortChatAdapters,
  startsGroup,
  toolInputText,
  wslUnavailable,
} from './chats.ts';

const adapter = (id: string, status: string, installed = status !== 'not_installed', version = ''): AdapterInfo => ({
  id,
  detection: { adapter: id, installed, status, version },
  capabilities: {} as AdapterInfo['capabilities'],
});
const msg = (seq: number, role = 'user', over: Partial<ChatMessage> = {}): ChatMessage => ({ id: `m${seq}`, chatId: 'c1', seq, role, body: `b${seq}`, createdAt: seq, ...over });
const chat = (over: Partial<Chat> = {}): Chat =>
  ({ id: 'c1', projectId: '', title: '', adapter: 'claude', status: 'idle', allowedTools: [], costUsd: null, costUnknownTurns: 0, version: 1, ...over }) as Chat;

describe('chats helpers', () => {
  it('formats relative times for the list', () => {
    const now = new Date('2026-10-09T12:00:00Z').getTime();
    expect(relTime(now - 10_000, now)).toBe('now');
    expect(relTime(now - 5 * 60_000, now)).toBe('5m');
    expect(relTime(now - 3 * 3_600_000, now)).toBe('3h');
    expect(relTime(now - 2 * 86_400_000, now)).toBe('2d');
    expect(relTime(now - 30 * 86_400_000, now)).not.toBe('');
    expect(relTime(0, now)).toBe('');
  });

  it('never shows an unknown cost as $0 and says how many turns had none', () => {
    expect(costText({ costUsd: null, costUnknownTurns: 0 })).toBe('cost unknown');
    expect(costText({ costUsd: 0.0123, costUnknownTurns: 0 })).toBe('$0.01');
    expect(costText({ costUsd: 0.0002, costUnknownTurns: 0 })).toBe('$0.0002');
    expect(costText({ costUsd: 1.5, costUnknownTurns: 2 })).toBe('$1.50 + 2 turns unknown');
    expect(compactNumber(950)).toBe('950');
    expect(compactNumber(1234)).toBe('1.2k');
    expect(compactNumber(25_000)).toBe('25k');
  });

  it('splits search snippets into plain text and matches', () => {
    expect(snippetParts('an [overflow] in [Add]!')).toEqual([
      { text: 'an ', hit: false },
      { text: 'overflow', hit: true },
      { text: ' in ', hit: false },
      { text: 'Add', hit: true },
      { text: '!', hit: false },
    ]);
    expect(snippetParts('plain')).toEqual([{ text: 'plain', hit: false }]);
  });

  it('puts Claude Code first and marks CLIs that cannot start', () => {
    const list = [adapter('gemini', 'not_installed'), adapter('cline', 'ready', true, '3.0.70'), adapter('claude', 'ready', true, '2.1.295'), adapter('codex', 'unsupported_version', true, '0.1')];
    expect(sortChatAdapters(list).map((a) => a.id)).toEqual(['claude', 'cline', 'codex', 'gemini']);
    expect(adapterUnavailable(list[0]!)).toBe('not installed');
    expect(adapterUnavailable(list[3]!)).toBe('unsupported version 0.1');
    expect(adapterUnavailable(list[1]!)).toBe('');
    expect(adapterOptionText(list[2]!)).toBe('Claude Code 2.1.295');
    expect(adapterOptionText(adapter('codex', 'unauthenticated'))).toBe('Codex (not signed in)');
  });

  it('offers each install of a CLI inside WSL as its own choice, labelled with its distro (decision P41)', () => {
    const claude: AdapterInfo = { ...adapter('claude', 'ready', true, '2.1.295'), capabilities: { runModes: ['native', 'wsl'] } as AdapterInfo['capabilities'] };
    const cline = adapter('cline', 'ready', true, '3.0.70');
    const wslConn = (distro: string, over: Partial<Connection> = {}): Connection =>
      ({ adapter: 'claude', key: `claude@wsl:${distro}`, runMode: 'wsl', distro, installed: true, status: 'ready', usable: true, version: '2.1.296', platform: 'linux (WSL 2)', ...over }) as Connection;
    const conns = [
      { adapter: 'claude', key: 'claude', installed: true, status: 'ready', usable: true, platform: 'windows' } as Connection, // the Windows install: from adapter.list
      wslConn('Ubuntu'),
      wslConn('Debian', { status: 'unauthenticated', usable: false }),
      wslConn('Kali', { status: 'degraded', usable: false, version: '2.1.297' }),
      wslConn('Alpine', { installed: false, status: 'not_installed', usable: false }), // no Claude Code there: not offered
      wslConn('Fedora', { adapter: 'cline', key: 'cline@wsl:Fedora' }), // an adapter that cannot run in WSL here
    ];
    const choices = chatChoices([cline, claude], conns);
    expect(choices.map((c) => [c.value, c.text, c.wslDistro, c.why])).toEqual([
      ['claude', 'Claude Code on Windows 2.1.295', '', ''],
      ['claude@wsl:Debian', 'Claude Code in Debian (WSL) (not signed in)', 'Debian', 'not signed in'],
      ['claude@wsl:Kali', 'Claude Code in Kali (WSL) 2.1.297', 'Kali', ''],
      ['claude@wsl:Ubuntu', 'Claude Code in Ubuntu (WSL) 2.1.296', 'Ubuntu', ''],
      ['cline', 'Cline 3.0.70', '', ''],
    ]);
    expect(choices[2]!.adapter).toBe(claude);
    // without WSL installs nothing changes
    expect(chatChoices([claude], []).map((c) => c.text)).toEqual(['Claude Code 2.1.295']);

    // the picked choice when it can start, else Claude Code (Windows first), else the first that can
    expect(pickChatChoice(choices, 'claude@wsl:Ubuntu')?.value).toBe('claude@wsl:Ubuntu');
    expect(pickChatChoice(choices, 'claude@wsl:Debian')?.value).toBe('claude');
    expect(pickChatChoice(choices, '')?.value).toBe('claude');
    const noWindows = chatChoices([{ ...claude, detection: { adapter: 'claude', installed: false, status: 'not_installed' } }], [wslConn('Ubuntu')]);
    expect(noWindows.map((c) => c.text)).toEqual(['Claude Code on Windows (not installed)', 'Claude Code in Ubuntu (WSL) 2.1.296']);
    expect(pickChatChoice(noWindows, '')?.value).toBe('claude@wsl:Ubuntu');
    expect(pickChatChoice([], '')).toBeUndefined();

    expect(wslUnavailable({ status: 'unsupported_version', version: '1.0' })).toBe('unsupported version 1.0');
    expect(wslUnavailable({ status: 'ready', gates: [{ id: 'g', text: 't', acked: false }] })).toBe('a notice to accept on the Connections page');
    // degraded (interop off, an untested version, a failed Test) is offered, as a degraded install on Windows is
    expect(wslUnavailable({ status: 'degraded' })).toBe('');
    expect(chatWhere({ wslDistro: 'Ubuntu' })).toBe('in Ubuntu (WSL)');
    expect(chatWhere({ wslDistro: '' })).toBe('');
  });

  it('merges messages by id in seq order and groups by side', () => {
    const merged = mergeMessages([msg(3), msg(1)], [msg(2), { ...msg(3), body: 'new' }]);
    expect(merged.map((m) => [m.seq, m.body])).toEqual([
      [1, 'b1'],
      [2, 'b2'],
      [3, 'new'],
    ]);
    expect(startsGroup(undefined, msg(1))).toBe(true);
    expect(startsGroup(msg(1, 'assistant'), msg(2, 'tool'))).toBe(false);
    expect(startsGroup(msg(1, 'user'), msg(2, 'assistant'))).toBe(true);
    expect(startsGroup(msg(1, 'user'), msg(2, 'system'))).toBe(false);
  });

  it('shortens long folders in the middle and prints tool input', () => {
    expect(shortPath('C:\\a', 48)).toBe('C:\\a');
    const p = shortPath('C:\\Users\\kyle\\source\\repos\\a-very-long-project-name\\packages\\core', 30);
    expect(p.length).toBe(30);
    expect(p.startsWith('C:\\')).toBe(true);
    expect(p.endsWith('core')).toBe(true);
    expect(toolInputText({ command: 'npm test' })).toBe('{\n  "command": "npm test"\n}');
    expect(toolInputText({ truncated: true, preview: '{"a":' })).toContain('(truncated)');
  });
});

describe('chats routes and palette', () => {
  it('routes /chats and /chats/:id, both open without a project', () => {
    expect(routes.chats.href()).toBe('/chats');
    expect(routes.chat.href({ id: 'chat_1' })).toBe('/chats/chat_1');
    expect(parseRoute('/chats')).toEqual({ name: 'chats' });
    expect(parseRoute('/chats/chat_1')).toEqual({ name: 'chat', id: 'chat_1' });
    expect(parseRoute('/chats/a/b')).toEqual({ name: 'not-found' });
    expect(isWorkspaceRoute({ name: 'chats' })).toBe(true);
    expect(isWorkspaceRoute({ name: 'chat', id: 'x' })).toBe(true);
  });

  it('offers New chat and the recent chats in the palette', () => {
    const items = buildItems([], [], [], [{ ...chat({ id: 'c1', title: 'Overflow' }), preview: '', messageCount: 1 }, { ...chat({ id: 'c2', title: 'Old', archived: true }), preview: '', messageCount: 1 }]);
    const chats = items.filter((i) => i.group === 'Chats');
    // New chat is an action (the Chats screen opens its dialog over what is shown), not a URL
    expect(chats.map((i) => [i.label, i.href, i.action])).toEqual([
      ['New chat', undefined, 'chat:new'],
      ['Overflow', '/chats/c1', undefined],
    ]);
    expect(items.some((i) => i.label === 'Chats' && i.href === '/chats')).toBe(true);
  });

  it('knows the Chats screen from the routes, so New chat stays on an open conversation', () => {
    expect(isChatsPath(routes.chats.href())).toBe(true);
    expect(isChatsPath(routes.chat.href({ id: 'chat_a' }))).toBe(true);
    expect(isChatsPath(routes.tasks.href())).toBe(false);
    expect(isChatsPath('/chats/a/b')).toBe(false);
  });
});

describe('chat client', () => {
  it('sends the documented parameters and turns null lists into arrays', async () => {
    const t = new FakeTransport();
    const c = createChatClient(t);
    t.responses.set('chat.list', null);
    t.responses.set('chat.get', { chat: chat({ allowedTools: null as never }), messages: null, hasMore: true });
    expect(await c.chatList({ projectId: 'p' })).toEqual([]);
    const d = await c.chatGet('c1');
    expect(d.messages).toEqual([]);
    expect(d.chat.allowedTools).toEqual([]);
    expect(d.hasMore).toBe(true);
    t.responses.set('chat.send', { turnId: 't1', version: 4 });
    expect(await c.chatSend('c1', 'hi', ['c2'])).toEqual({ turnId: 't1', version: 4 });
    t.responses.set('chat.messages', { messages: [msg(1)], hasMore: true });
    expect(await c.chatMessages('c1', 50)).toEqual({ messages: [msg(1)], hasMore: true });
    t.responses.set('chat.messages', { messages: null, hasMore: false });
    expect(await c.chatMessages('c1', 2)).toEqual({ messages: [], hasMore: false });
    await c.chatSearch('overflow');
    t.responses.set('chat.fork', chat());
    await c.chatFork('c1', 3);
    await c.chatList({ query: 'notes', includeArchived: true, limit: 8 });
    expect(t.calls.map((x) => [x.method, x.params])).toEqual([
      ['chat.list', { projectId: 'p', query: '', includeArchived: false }],
      ['chat.get', { id: 'c1' }],
      ['chat.send', { id: 'c1', text: 'hi', attach: ['c2'] }],
      ['chat.messages', { id: 'c1', beforeSeq: 50, limit: 100 }],
      ['chat.messages', { id: 'c1', beforeSeq: 2, limit: 100 }],
      ['chat.search', { query: 'overflow', projectId: '', limit: 50 }],
      ['chat.fork', { id: 'c1', uptoSeq: 3 }],
      ['chat.list', { projectId: '', query: 'notes', includeArchived: true, limit: 8 }], // the limit is passed on
    ]);
  });
});

describe('ChatFeed', () => {
  function feed(running = false, get?: () => Promise<{ chat: Chat; messages: ChatMessage[]; hasMore: boolean }>) {
    const store = new EventStore(new FakeTransport());
    const client = {
      chatGet: get ?? (async () => ({ chat: chat({ status: running ? 'running' : 'idle' }), messages: [msg(1), msg(2, 'assistant')], hasMore: false })),
      chatMessages: async () => ({ messages: [], hasMore: false }),
    };
    const f = new ChatFeed(client, store, 'c1').start();
    let seq = 1;
    const emit = (type: string, data: unknown) => store.ingest(ev(++seq, type, { data }));
    return { f, emit };
  }
  const settle = () => new Promise((r) => setTimeout(r, 0));

  it('streams deltas, replaces them with the stored block and ends on chat.turn', async () => {
    const { f, emit } = feed();
    await settle();
    expect(f.messages).toHaveLength(2);
    expect(f.running).toBe(false);
    emit('chat.turn_started', { chatId: 'c1', turnId: 't2', attach: [] });
    expect(f.running).toBe(true);
    emit('chat.delta', { chatId: 'c1', turnId: 't2', text: 'Hel' });
    emit('chat.delta', { chatId: 'c1', turnId: 't2', text: 'lo ' });
    emit('chat.delta', { chatId: 'c9', turnId: 'x', text: 'other chat' });
    expect(f.streaming).toEqual({ turnId: 't2', text: 'Hello ' });
    emit('chat.message', { chatId: 'c1', message: msg(3, 'assistant', { body: 'Hello there' }) });
    expect(f.streaming?.text).toBe('');
    expect(f.messages.map((m) => m.seq)).toEqual([1, 2, 3]);
    emit('chat.message', { chatId: 'c1', message: msg(3, 'assistant', { body: 'Hello there' }) }); // a duplicate is merged
    expect(f.messages).toHaveLength(3);
    emit('chat.turn', { chatId: 'c1', turnId: 't2', status: 'done', fallback: true });
    expect(f.streaming).toBeUndefined();
    expect(f.running).toBe(false);
    expect(f.lastTurn?.fallback).toBe(true);
  });

  it('ignores a chat.* event whose data is not what the feed reads, with a console warning, and goes on with the next (L43)', async () => {
    const { f, emit } = feed();
    await settle();
    const warnings: unknown[][] = [];
    const warn = console.warn;
    console.warn = (...a: unknown[]) => void warnings.push(a);
    try {
      emit('chat.message', { chatId: 'c1', message: { id: 'x', chatId: 'c1', role: 'assistant' } }); // no seq, no body
      emit('chat.message', { chatId: 'c1', message: { id: 'y', chatId: 'c1', seq: 'nine', role: 'assistant', body: 'b' } });
      emit('chat.message', { chatId: 'c1', message: 'text' });
      emit('chat.message', { chatId: 'c1' });
      emit('chat.updated', { chatId: 'c1', chat: { id: 'c1', status: 'running' } }); // no title, no adapter
      emit('chat.updated', { chatId: 'c1', chat: 'x' });
      emit('chat.updated', { chatId: 'c1', chat: chat({ id: 'c2', status: 'running', version: 9 }) }); // another chat's snapshot
      emit('chat.turn', { chatId: 'c1', turnId: 7, status: 'done' });
      emit('chat.turn', { chatId: 'c1', turnId: 't', status: 'weird' });
      emit('chat.turn', { chatId: 'c1' });
    } finally {
      console.warn = warn;
    }
    expect(f.messages.map((m) => m.seq)).toEqual([1, 2]);
    expect(f.chat?.status).toBe('idle');
    expect(f.running).toBe(false);
    expect(f.lastTurn).toBeUndefined();
    expect(warnings.length).toBe(5); // the first few are reported, then it stops
    expect(String(warnings[0]![0])).toContain('chat.message');
    // and the feed still takes the events that are right
    emit('chat.message', { chatId: 'c1', message: msg(3, 'assistant') });
    emit('chat.turn', { chatId: 'c1', turnId: 't2', status: 'error', error: 'boom' });
    expect(f.messages.map((m) => m.seq)).toEqual([1, 2, 3]);
    expect(f.lastTurn?.error).toBe('boom');
  });

  it('a chat that is running when opened shows the turn as in progress', async () => {
    const { f } = feed(true);
    await settle();
    expect(f.running).toBe(true);
    expect(f.streaming).toEqual({ turnId: '', text: '' });
  });

  it('a chat.send answer that arrives after its turn already ended does not bring the turn back', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'running', version: 2 }) });
    emit('chat.turn_started', { chatId: 'c1', turnId: 't9', attach: [] });
    emit('chat.turn', { chatId: 'c1', turnId: 't9', status: 'done' });
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'idle', version: 5 }) });
    f.sent('t9', 2); // the HTTP answer lands last
    expect(f.running).toBe(false);
    expect(f.chat?.status).toBe('idle');
    f.sent('t10', 6); // a turn no snapshot shows yet does show as running
    expect(f.running).toBe(true);
  });

  it('orders snapshots by version: an older action answer or chat.updated never replaces a newer one', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'idle', title: 'Fresh', version: 7 }) });
    f.setChat(chat({ status: 'running', title: 'Stale', version: 6 })); // read before the turn ended
    expect(f.chat?.title).toBe('Fresh');
    expect(f.running).toBe(false);
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'running', title: 'Stale event', version: 5 }) });
    expect(f.chat?.title).toBe('Fresh');
    expect(f.running).toBe(false);
    // the clock does not count: a newer version wins even with an older updatedAt
    f.setChat(chat({ status: 'idle', title: 'Newer', version: 8, updatedAt: 1 }));
    expect(f.chat?.title).toBe('Newer');
    f.setChat(chat({ id: 'c2', title: 'Other chat', version: 99 }));
    expect(f.chat?.title).toBe('Newer');
  });

  it('drops a late delta of a turn that already ended', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.turn_started', { chatId: 'c1', turnId: 't2', attach: [] });
    emit('chat.delta', { chatId: 'c1', turnId: 't2', text: 'Hel' });
    emit('chat.turn', { chatId: 'c1', turnId: 't2', status: 'done' });
    emit('chat.delta', { chatId: 'c1', turnId: 't2', text: 'lo' });
    expect(f.streaming).toBeUndefined();
    expect(f.running).toBe(false);
  });

  it('a snapshot read before the turn started does not undo it', async () => {
    let release: (v: { chat: Chat; messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    const { f, emit } = feed(false, () => new Promise((res) => (release = res)));
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'running', version: 3 }) });
    emit('chat.turn_started', { chatId: 'c1', turnId: 't1', attach: [] });
    release({ chat: chat({ status: 'idle', version: 2 }), messages: [msg(1)], hasMore: false });
    await settle();
    expect(f.running).toBe(true);
    expect(f.chat?.status).toBe('running');
    expect(f.messages.map((m) => m.seq)).toEqual([1]);
  });

  it('a send while the chat is read keeps the turn running until a snapshot at its version shows it', async () => {
    let release: (v: { chat: Chat; messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    const { f, emit } = feed(false, () => new Promise((res) => (release = res)));
    f.sent('t1', 5);
    expect(f.running).toBe(true);
    release({ chat: chat({ status: 'idle', version: 4 }), messages: [msg(1)], hasMore: false }); // read before the send
    await settle();
    expect(f.running).toBe(true);
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'running', version: 5 }) });
    expect(f.running).toBe(true);
    emit('chat.turn', { chatId: 'c1', turnId: 't1', status: 'done' });
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'idle', version: 7 }) });
    expect(f.running).toBe(false);
  });

  it('a running chat opened while one of its tools reports still shows the turn in progress', async () => {
    let release: (v: { chat: Chat; messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    const { f, emit } = feed(false, () => new Promise((res) => (release = res)));
    emit('chat.tool', { chatId: 'c1', turnId: 't1', name: 'Bash', summary: 'Bash: npm test' });
    emit('chat.message', { chatId: 'c1', message: msg(301, 'tool') });
    release({ chat: chat({ status: 'running', version: 9 }), messages: [msg(299), msg(300, 'assistant')], hasMore: false });
    await settle();
    expect(f.running).toBe(true);
    expect(f.streaming).toEqual({ turnId: '', text: '' });
  });

  it('keeps the first page\'s hasMore when a message arrives while it is read', async () => {
    let release: (v: { chat: Chat; messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    const { f, emit } = feed(false, () => new Promise((res) => (release = res)));
    emit('chat.message', { chatId: 'c1', message: msg(301, 'tool') });
    release({ chat: chat({ status: 'running' }), messages: Array.from({ length: 200 }, (_, i) => msg(101 + i)), hasMore: true });
    await settle();
    expect(f.hasMore).toBe(true);
    expect(f.messages[0]?.seq).toBe(101);
    expect(f.messages.at(-1)?.seq).toBe(301);
  });

  it('after a reconnect a page that does not join the shown messages replaces them and keeps paging possible', async () => {
    let page = { chat: chat(), messages: [msg(1), msg(2, 'assistant')], hasMore: false };
    const store = new EventStore(new FakeTransport());
    const f = new ChatFeed({ chatGet: async () => page, chatMessages: async () => ({ messages: [], hasMore: false }) }, store, 'c1').start();
    await settle();
    expect(f.messages.map((m) => m.seq)).toEqual([1, 2]);
    page = { chat: chat(), messages: [msg(300), msg(301, 'assistant')], hasMore: true };
    await f.load();
    expect(f.messages.map((m) => m.seq)).toEqual([300, 301]);
    expect(f.hasMore).toBe(true);
    page = { chat: chat(), messages: [msg(301, 'assistant'), msg(302)], hasMore: true };
    await f.load(); // a page that overlaps is merged
    expect(f.messages.map((m) => m.seq)).toEqual([300, 301, 302]);
  });

  it('a message that arrives during a reconnect read does not make a gap look joined', async () => {
    let release: (v: { chat: Chat; messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    let first = true;
    const store = new EventStore(new FakeTransport());
    const get = () => (first ? ((first = false), Promise.resolve({ chat: chat(), messages: [msg(1), msg(2, 'assistant')], hasMore: false })) : new Promise<{ chat: Chat; messages: ChatMessage[]; hasMore: boolean }>((res) => (release = res)));
    const f = new ChatFeed({ chatGet: get, chatMessages: async () => ({ messages: [], hasMore: false }) }, store, 'c1').start();
    await settle();
    const reload = f.load();
    store.ingest(ev(2, 'chat.message', { data: { chatId: 'c1', message: msg(302) } }));
    release({ chat: chat(), messages: Array.from({ length: 200 }, (_, i) => msg(102 + i)), hasMore: true });
    await reload;
    expect(f.messages[0]?.seq).toBe(102); // not [1, 2, 102...]: 3..101 stay reachable through "Load older"
    expect(f.messages.at(-1)?.seq).toBe(302);
    expect(f.hasMore).toBe(true);
  });

  it('drops an older page asked for before a reload replaced the list', async () => {
    let page = { chat: chat(), messages: Array.from({ length: 200 }, (_, i) => msg(101 + i)), hasMore: true };
    let releaseOlder: (v: { messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    const store = new EventStore(new FakeTransport());
    const f = new ChatFeed({ chatGet: async () => page, chatMessages: () => new Promise((res) => (releaseOlder = res)) }, store, 'c1').start();
    await settle();
    const older = f.loadOlder();
    page = { chat: chat(), messages: Array.from({ length: 200 }, (_, i) => msg(401 + i)), hasMore: true };
    await f.load(); // the stream came back: 401..600 does not join 101..300
    releaseOlder({ messages: Array.from({ length: 100 }, (_, i) => msg(1 + i)), hasMore: false });
    expect(await older).toBe(0);
    expect(f.messages[0]?.seq).toBe(401);
    expect(f.hasMore).toBe(true);
  });

  it('a deleted chat empties the feed and nothing lands on it afterwards', async () => {
    let fail = false;
    let releaseOlder: (v: { messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    const store = new EventStore(new FakeTransport());
    const client = {
      chatGet: async () => {
        if (fail) throw new Error('chat not found');
        return { chat: chat(), messages: [msg(101), msg(102, 'assistant')], hasMore: true };
      },
      chatMessages: () => new Promise<{ messages: ChatMessage[]; hasMore: boolean }>((res) => (releaseOlder = res)),
    };
    const f = new ChatFeed(client, store, 'c1').start();
    await settle();
    const older = f.loadOlder();
    store.ingest(ev(2, 'chat.deleted', { data: { chatId: 'c1' } }));
    expect(f.deleted).toBe(true);
    expect(f.chat).toBeUndefined();
    expect(f.messages).toEqual([]);
    releaseOlder({ messages: [msg(1)], hasMore: false });
    await older;
    fail = true;
    await f.load(); // a reconnect's reload
    expect(f.messages).toEqual([]);
    expect(f.hasMore).toBe(false);
    expect(f.error).toBeUndefined();
  });
});
