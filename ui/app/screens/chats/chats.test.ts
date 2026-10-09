import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Chat, ChatMessage } from '../../api/chatClient.ts';
import { createChatClient } from '../../api/chatClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { isWorkspaceRoute, parseRoute } from '../../lib/route.ts';
import { routes } from '../../routes.ts';
import { buildItems, newChatHref } from '../../shell/palette-items.ts';
import { EventStore } from '../../state/eventStore.ts';
import { ev, FakeTransport } from '../../test/fakes.ts';
import { ChatFeed } from './chat-feed.ts';
import { adapterOptionText, adapterUnavailable, compactNumber, costText, isAbsolutePath, mergeMessages, relTime, shortPath, snippetParts, sortChatAdapters, startsGroup, toolInputText } from './chats.ts';

const adapter = (id: string, status: string, installed = status !== 'not_installed', version = ''): AdapterInfo => ({
  id,
  detection: { adapter: id, installed, status, version },
  capabilities: {} as AdapterInfo['capabilities'],
});
const msg = (seq: number, role = 'user', over: Partial<ChatMessage> = {}): ChatMessage => ({ id: `m${seq}`, chatId: 'c1', seq, role, body: `b${seq}`, createdAt: seq, ...over });
const chat = (over: Partial<Chat> = {}): Chat =>
  ({ id: 'c1', projectId: '', title: '', adapter: 'claude', status: 'idle', allowedTools: [], costUsd: null, costUnknownTurns: 0, ...over }) as Chat;

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

  it('accepts only absolute folders, as the core sees them on its platform', () => {
    // Windows (the product): a drive or a share; "/x" is not absolute there (filepath.IsAbs refuses it)
    for (const ok of ['C:\\work', 'd:/x', '\\\\server\\share\\x', '//server/share/x']) expect(isAbsolutePath(ok, true)).toBe(true);
    for (const bad of ['work', '.\\x', 'C:', 'notes/weekly', '', '/work/notes', '\\work', '\\\\server']) expect(isAbsolutePath(bad, true)).toBe(false);
    // the development host
    expect(isAbsolutePath('/home/me', false)).toBe(true);
    for (const bad of ['C:\\work', 'work', '']) expect(isAbsolutePath(bad, false)).toBe(false);
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
    expect(chats.map((i) => [i.label, i.href])).toEqual([
      ['New chat', '/chats?new'],
      ['Overflow', '/chats/c1'],
    ]);
    expect(items.some((i) => i.label === 'Chats' && i.href === '/chats')).toBe(true);
  });

  it('New chat from an open conversation keeps it behind the dialog', () => {
    expect(newChatHref('/chats/chat_a')).toBe('/chats/chat_a?new');
    expect(newChatHref('/chats')).toBe('/chats?new');
    expect(newChatHref('/tasks')).toBe('/chats?new');
    expect(buildItems([], [], [], [], '/chats/chat_a').find((i) => i.id === 'chat:new')?.href).toBe('/chats/chat_a?new');
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
    t.responses.set('chat.send', { turnId: 't1' });
    await c.chatSend('c1', 'hi', ['c2']);
    await c.chatMessages('c1', 50);
    await c.chatSearch('overflow');
    t.responses.set('chat.fork', chat());
    await c.chatFork('c1', 3);
    expect(t.calls.map((x) => [x.method, x.params])).toEqual([
      ['chat.list', { projectId: 'p', query: '', includeArchived: false }],
      ['chat.get', { id: 'c1' }],
      ['chat.send', { id: 'c1', text: 'hi', attach: ['c2'] }],
      ['chat.messages', { id: 'c1', beforeSeq: 50, limit: 100 }],
      ['chat.search', { query: 'overflow', projectId: '', limit: 50 }],
      ['chat.fork', { id: 'c1', uptoSeq: 3 }],
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

  it('a chat that is running when opened shows the turn as in progress', async () => {
    const { f } = feed(true);
    await settle();
    expect(f.running).toBe(true);
    expect(f.streaming).toEqual({ turnId: '', text: '' });
  });

  it('a chat.send answer that arrives after its turn already ended does not bring the turn back', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.turn_started', { chatId: 'c1', turnId: 't9', attach: [] });
    emit('chat.turn', { chatId: 'c1', turnId: 't9', status: 'done' });
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'idle', updatedAt: 5 }) });
    f.sent('t9'); // the HTTP answer lands last
    expect(f.running).toBe(false);
    expect(f.chat?.status).toBe('idle');
    f.sent('t10'); // a turn that has not ended yet does show as running
    expect(f.running).toBe(true);
  });

  it('an action answer older than the chat shown is dropped', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'idle', title: 'Fresh', updatedAt: 200 }) });
    f.setChat(chat({ status: 'running', title: 'Stale', updatedAt: 100 }));
    expect(f.chat?.title).toBe('Fresh');
    expect(f.running).toBe(false);
    f.setChat(chat({ status: 'idle', title: 'Newer', updatedAt: 300 }));
    expect(f.chat?.title).toBe('Newer');
  });

  it('a snapshot taken before events that arrived meanwhile does not undo them', async () => {
    let release: (v: { chat: Chat; messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    const { f, emit } = feed(false, () => new Promise((res) => (release = res)));
    emit('chat.turn_started', { chatId: 'c1', turnId: 't1', attach: [] });
    emit('chat.updated', { chatId: 'c1', chat: chat({ status: 'running', updatedAt: 50 }) });
    release({ chat: chat({ status: 'idle', updatedAt: 10 }), messages: [msg(1)], hasMore: false });
    await settle();
    expect(f.running).toBe(true);
    expect(f.chat?.status).toBe('running');
    expect(f.messages.map((m) => m.seq)).toEqual([1]);
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

  it('a deleted chat empties the feed', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.deleted', { chatId: 'c1' });
    expect(f.deleted).toBe(true);
    expect(f.chat).toBeUndefined();
    expect(f.messages).toEqual([]);
  });
});
