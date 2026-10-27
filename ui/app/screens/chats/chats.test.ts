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
import { cliName } from '../shared/cli-name.ts';
import { ChatFeed } from './chat-feed.ts';
import { durationText, editLines, toolLabel, toolState, toolVerb } from './chat-tool.tsx';
import { sendsOnKey } from './chat-composer.tsx';
import { moveEffect, OUT_OF_PROJECT } from './chat-move.tsx';
import {
  adapterOptionText,
  adapterUnavailable,
  askCallText,
  askHead,
  chatPlaceWords,
  chatRowLine,
  folderText,
  inOwnFolder,
  listWhen,
  modeWords,
  ruleWords,
  chatChoices,
  chatCliText,
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

  it('says when a chat was last active as the list does: now, minutes, hours today, yesterday, a weekday, then the date', () => {
    const now = new Date(2026, 9, 22, 15, 0).getTime(); // a Thursday afternoon
    expect(listWhen(now - 10_000, now)).toBe('now');
    expect(listWhen(now - 5 * 60_000, now)).toBe('5 min');
    expect(listWhen(now - 2 * 3_600_000, now)).toBe('2 h');
    expect(listWhen(new Date(2026, 9, 21, 23, 0).getTime(), now)).toBe('yesterday');
    expect(listWhen(new Date(2026, 9, 19, 9, 0).getTime(), now)).toBe('Mon');
    expect(listWhen(new Date(2026, 8, 1).getTime(), now)).not.toMatch(/min|h$|yesterday/);
    expect(listWhen(0, now)).toBe('');
    expect(chatRowLine({ project: 'gocalc', cli: 'Codex', when: '2 h' })).toBe('gocalc · Codex · 2 h');
    expect(chatRowLine({ cli: 'Claude Code WSL', when: 'now' })).toBe('Claude Code WSL · now');
  });

  it('says the permission mode, where a chat runs and its folder in words from its fields', () => {
    expect(['', 'default', 'acceptEdits', 'plan', 'bypassPermissions', 'auto', 'dontAsk', 'read-only'].map(modeWords)).toEqual([
      'Asks before commands',
      'Asks before commands',
      'Edits files, asks before commands',
      'Plans only, changes nothing',
      'Never asks, skips approvals',
      'Decides itself, skips approvals',
      'Does not ask, skips approvals',
      'read-only mode',
    ]);
    const own = { id: 'chat_a', projectId: '', wslDistro: '', cwd: 'C:\\Users\\kyle\\.drydock\\chats\\chat_a' };
    expect(inOwnFolder(own)).toBe(true);
    expect(chatPlaceWords(own)).toBe('No project · runs in its own folder');
    expect(chatPlaceWords({ ...own, cwd: 'C:\\work\\notes' })).toBe('No project · runs in the folder you chose');
    expect(chatPlaceWords({ ...own, wslDistro: 'Ubuntu' })).toBe('No project · runs inside the distro');
    expect(chatPlaceWords({ ...own, projectId: 'p1' }, 'gocalc')).toBe("gocalc · runs in the project's folder");
    expect(chatPlaceWords({ ...own, projectId: 'p1', wslDistro: 'Ubuntu' }, 'gocalc')).toBe("gocalc · runs in the project's folder, inside the distro");
    expect(folderText({ cwd: '\\\\wsl.localhost\\Ubuntu\\home\\kyle\\.drydock\\chats\\chat_a', wslDistro: 'Ubuntu' })).toBe('~/.drydock/chats/chat_a');
    expect(folderText({ cwd: '\\\\wsl$\\Debian\\srv\\app', wslDistro: 'Debian' })).toBe('/srv/app');
    expect(folderText({ cwd: '/home/kyle/notes', wslDistro: 'Ubuntu' })).toBe('~/notes');
    expect(folderText({ cwd: 'C:\\repos\\gocalc', wslDistro: '' })).toBe('C:\\repos\\gocalc');
    expect(moveEffect({ projectId: '' }, 'p1', 'gocalc')).toContain('It joins gocalc and runs in the project\'s folder.');
    expect(moveEffect({ projectId: 'p1' }, 'p2', 'shop')).toContain('It moves to shop');
    expect(moveEffect({ projectId: 'p1' }, OUT_OF_PROJECT, '')).toBe('It leaves the project and keeps the folder it runs in, so its session continues.');
  });

  it('words a waiting request and its rule, a tool card and the keys that send', () => {
    const cmd = { tool: 'Bash', summary: 'Bash: git diff --stat', input: { command: 'git diff --stat' } };
    expect([askHead('Claude Code', cmd), askCallText(cmd)]).toEqual(['Claude Code wants to run a command', 'git diff --stat']);
    const fetch = { tool: 'Fetch', summary: 'Fetch: https://go.dev', input: { url: 'https://go.dev' } };
    expect([askHead('Gemini CLI', fetch), askCallText(fetch)]).toEqual(['Gemini CLI wants to use Fetch', 'https://go.dev']);
    expect(ruleWords('Bash(git diff --stat)')).toBe('git diff --stat');
    expect(ruleWords('WebFetch')).toBe('WebFetch');
    expect(ruleWords(`Bash(${'x'.repeat(60)})`)).toHaveLength(40);
    expect([toolVerb('Bash', true, 'ok'), toolVerb('Bash', true, 'running'), toolVerb('Read', false, 'ok'), toolVerb('mcp__drydock__chatmcp__echo', false, 'ok')]).toEqual(['Ran', 'Running', 'Read', 'chatmcp · echo']);
    const k = (key: string, o: Partial<KeyboardEvent> = {}) => sendsOnKey({ key, shiftKey: false, altKey: false, isComposing: false, ...o });
    expect([k('Enter'), k('Enter', { shiftKey: true }), k('Enter', { isComposing: true }), k('a')]).toEqual([true, false, false, false]);
  });

  it('never shows an unknown cost as $0 and says how many turns had none', () => {
    expect(costText({ costUsd: null, costUnknownTurns: 0 })).toBe('cost unknown');
    expect(costText({ costUsd: 0.0123, costUnknownTurns: 0 })).toBe('$0.01');
    expect(costText({ costUsd: 0.0002, costUnknownTurns: 0 })).toBe('$0.0002');
    expect(costText({ costUsd: 1.5, costUnknownTurns: 2 })).toBe("$1.50, and 2 turns' cost unknown");
    expect(costText({ costUsd: 1.5, costUnknownTurns: 1 })).toBe("$1.50, and 1 turn's cost unknown");
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

  it('offers each install of a CLI inside WSL as its own choice, "Claude Code WSL", with the distro only to tell two apart (decision P41)', () => {
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
      ['claude@wsl:Debian', 'Claude Code WSL (Debian) (not signed in)', 'Debian', 'not signed in'],
      ['claude@wsl:Kali', 'Claude Code WSL (Kali) 2.1.297', 'Kali', ''],
      ['claude@wsl:Ubuntu', 'Claude Code WSL (Ubuntu) 2.1.296', 'Ubuntu', ''],
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
    // one distro with the CLI: no distro in the name (the owner: "we don't need to see the Ubuntu")
    expect(noWindows.map((c) => c.text)).toEqual(['Claude Code on Windows (not installed)', 'Claude Code WSL 2.1.296']);
    expect(chatChoices([claude], [wslConn('Ubuntu'), wslConn('Alpine', { installed: false, status: 'not_installed' })]).map((c) => c.text)).toEqual(['Claude Code on Windows 2.1.295', 'Claude Code WSL 2.1.296']);
    expect(pickChatChoice(noWindows, '')?.value).toBe('claude@wsl:Ubuntu');
    expect(pickChatChoice([], '')).toBeUndefined();

    expect(wslUnavailable({ status: 'unsupported_version', version: '1.0' })).toBe('unsupported version 1.0');
    expect(wslUnavailable({ status: 'ready', gates: [{ id: 'g', text: 't', acked: false }] })).toBe('a notice to accept on the Connections page');
    // degraded (interop off, an untested version, a failed Test) is offered, as a degraded install on Windows is
    expect(wslUnavailable({ status: 'degraded' })).toBe('');
    expect(chatWhere({ wslDistro: 'Ubuntu' })).toBe('Runs inside the WSL distro Ubuntu');
    expect(chatCliText({ adapter: 'claude', wslDistro: 'Ubuntu' })).toBe('Claude Code WSL');
    expect(chatCliText({ adapter: 'claude', wslDistro: '' })).toBe('Claude Code');
    // the header names the install as the Connections page and the pickers do (cliName with the Connections list): the distro
    // only when the CLI is installed in another distro too; a distro without it does not count
    expect(chatCliText({ adapter: 'claude', wslDistro: 'Ubuntu' }, conns)).toBe('Claude Code WSL (Ubuntu)');
    expect(chatCliText({ adapter: 'claude', wslDistro: 'Ubuntu' }, conns)).toBe(cliName(conns[1]!, conns));
    expect(chatCliText({ adapter: 'claude', wslDistro: '' }, conns)).toBe('Claude Code');
    const one = [wslConn('Ubuntu'), wslConn('Alpine', { installed: false, status: 'not_installed' })];
    expect(chatCliText({ adapter: 'claude', wslDistro: 'Ubuntu' }, one)).toBe('Claude Code WSL');
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

  it('tells its view about text streaming into an answer already shown apart from changes, so only the answer redraws', async () => {
    const { f, emit } = feed();
    await settle();
    const seen: string[] = [];
    f.addEventListener('change', () => seen.push('change'));
    f.addEventListener('stream', () => seen.push('stream'));
    emit('chat.turn_started', { chatId: 'c1', turnId: 't2', attach: [] });
    emit('chat.thinking', { chatId: 'c1', turnId: 't2', text: 'Read ' }); // the fold appears: a change
    emit('chat.thinking', { chatId: 'c1', turnId: 't2', text: 'calc.go.' }); // it grows: a stream
    emit('chat.delta', { chatId: 'c1', turnId: 't2', text: 'Hel' }); // the answer appears: a change
    emit('chat.delta', { chatId: 'c1', turnId: 't2', text: 'lo ' }); // it grows: a stream
    emit('chat.delta', { chatId: 'c1', turnId: 't2', text: 'there' });
    emit('chat.delta', { chatId: 'c9', turnId: 'x', text: 'other chat' }); // not this chat: nothing
    emit('chat.message', { chatId: 'c1', message: msg(3, 'assistant', { body: 'Hello there' }) });
    emit('chat.turn', { chatId: 'c1', turnId: 't2', status: 'done' });
    expect(seen).toEqual(['change', 'change', 'stream', 'change', 'stream', 'stream', 'change', 'change']);
    expect(f.streaming).toBeUndefined();
  });

  it('streams the reasoning and lets the stored row it came before take it (P89)', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.turn_started', { chatId: 'c1', turnId: 't2', attach: [] });
    emit('chat.thinking', { chatId: 'c1', turnId: 't2', text: 'Read calc.go ' });
    emit('chat.thinking', { chatId: 'c1', turnId: 't2', text: 'first.' });
    emit('chat.thinking', { chatId: 'c9', turnId: 'x', text: 'other chat' });
    expect(f.streaming).toEqual({ turnId: 't2', text: '', thinking: 'Read calc.go first.' });
    emit('chat.delta', { chatId: 'c1', turnId: 't2', text: 'Looking' });
    expect(f.streaming).toEqual({ turnId: 't2', text: 'Looking', thinking: 'Read calc.go first.' });
    // the tool call stored next carries it: the live reasoning is cleared
    emit('chat.message', { chatId: 'c1', message: msg(3, 'tool', { toolName: 'Read', body: 'Read: calc.go', thinking: 'Read calc.go first.' }) });
    expect(f.streaming).toEqual({ turnId: 't2', text: 'Looking' });
    expect(f.messages.find((m) => m.seq === 3)?.thinking).toBe('Read calc.go first.');
    // a late chunk of a turn that already ended is dropped
    emit('chat.turn', { chatId: 'c1', turnId: 't2', status: 'done' });
    emit('chat.thinking', { chatId: 'c1', turnId: 't2', text: 'late' });
    expect(f.streaming).toBeUndefined();
  });

  it('drops the messages a resend replaced (P89)', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.message', { chatId: 'c1', message: msg(3, 'user') });
    emit('chat.message', { chatId: 'c1', message: msg(4, 'assistant') });
    emit('chat.rewound', { chatId: 'c1', fromSeq: 3 });
    expect(f.messages.map((m) => m.seq)).toEqual([1, 2]);
    emit('chat.rewound', { chatId: 'c1', fromSeq: 'x' }); // malformed: ignored
    emit('chat.rewound', { chatId: 'c9', fromSeq: 1 }); // another chat
    expect(f.messages.map((m) => m.seq)).toEqual([1, 2]);
  });

  it('a tool result lands on its call: status, result and duration, and a malformed one is ignored', async () => {
    const { f, emit } = feed();
    await settle();
    emit('chat.message', { chatId: 'c1', message: msg(3, 'tool', { id: 'm3', toolName: 'Bash', body: 'Bash: go test' }) });
    expect(f.messages.find((m) => m.id === 'm3')?.toolStatus).toBeUndefined();
    emit('chat.tool_result', { chatId: 'c1', messageId: 'm3', status: 'failed', result: 'FAIL', ms: 1200 });
    const m = f.messages.find((x) => x.id === 'm3')!;
    expect([m.toolStatus, m.toolResult, m.toolMs]).toEqual(['failed', 'FAIL', 1200]);
    emit('chat.tool_result', { chatId: 'c1', messageId: 'm3', status: 'ok' }); // no result or duration: not what the feed reads
    expect(f.messages.find((x) => x.id === 'm3')?.toolStatus).toBe('failed');
    emit('chat.tool_result', { chatId: 'c1', messageId: 'unknown', status: 'ok', result: '', ms: 1 }); // a call not loaded
    expect(f.messages).toHaveLength(3);
  });

  it('a result that comes before its call is in the list lands on the row when a stale read brings it (the view opened mid-turn)', async () => {
    let release: (v: { chat: Chat; messages: ChatMessage[]; hasMore: boolean }) => void = () => {};
    const store = new EventStore(new FakeTransport());
    const get = () => new Promise<{ chat: Chat; messages: ChatMessage[]; hasMore: boolean }>((res) => (release = res));
    const f = new ChatFeed({ chatGet: get, chatMessages: async () => ({ messages: [], hasMore: false }) }, store, 'c1').start();
    // the result is reported while the first read is out; the read was made before it was stored
    store.ingest(ev(2, 'chat.tool_result', { data: { chatId: 'c1', messageId: 'm3', status: 'ok', result: 'never guess flags', ms: 55 } }));
    release({ chat: chat(), messages: [msg(1), msg(3, 'tool', { id: 'm3', toolName: 'skills', body: 'skills' })], hasMore: false });
    await settle();
    const m = f.messages.find((x) => x.id === 'm3')!;
    expect([m.toolStatus, m.toolResult, m.toolMs]).toEqual(['ok', 'never guess flags', 55]);
    // a later read that already carries how the call ended keeps its own answer
    const page = { chat: chat(), messages: [msg(1), msg(3, 'tool', { id: 'm3', toolName: 'skills', body: 'skills', toolStatus: 'failed', toolResult: 'x', toolMs: 1 })], hasMore: false };
    f.stop();
    const g = new ChatFeed({ chatGet: async () => page, chatMessages: async () => ({ messages: [], hasMore: false }) }, store, 'c1').start();
    await settle();
    expect(g.messages.find((x) => x.id === 'm3')!.toolStatus).toBe('failed');
    g.stop();
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

describe('Tool cards', () => {
  it('names MCP tools as server and tool, Drydock\'s own as Drydock', () => {
    expect(toolLabel('mcp__drydock__skills')).toBe('Drydock · skills');
    expect(toolLabel('mcp__drydock__chatmcp__echo')).toBe('chatmcp · echo');
    expect(toolLabel('mcp__github__create_issue')).toBe('github · create_issue');
    expect(toolLabel('Bash')).toBe('Bash');
    expect(toolLabel(undefined)).toBe('Tool');
  });
  it('says how long a call took', () => {
    expect(durationText(undefined)).toBe('');
    expect(durationText(840)).toBe('840 ms');
    expect(durationText(1234)).toBe('1.2 s');
    expect(durationText(42_000)).toBe('42 s');
    expect(durationText(125_000)).toBe('2 min 5 s');
  });
  it('is running while its turn runs, then done, failed, or without a result', () => {
    expect(toolState({}, true)).toBe('running');
    expect(toolState({}, false)).toBe('unknown');
    expect(toolState({ toolStatus: 'ok' }, true)).toBe('ok');
    expect(toolState({ toolStatus: 'failed' }, false)).toBe('failed');
  });
  it('reads an edit as removed and added lines', () => {
    expect(editLines({ file_path: 'a.go', old_string: 'return 0', new_string: 'return a - b\n// done' })).toEqual([
      { kind: '-', text: 'return 0' },
      { kind: '+', text: 'return a - b' },
      { kind: '+', text: '// done' },
    ]);
    expect(editLines({ edits: [{ old_string: 'x', new_string: 'y' }, { old_string: '', new_string: 'z' }] })).toEqual([
      { kind: '-', text: 'x' },
      { kind: '+', text: 'y' },
      { kind: '+', text: 'z' },
    ]);
    expect(editLines({ command: 'ls' })).toBeUndefined();
    expect(editLines(undefined)).toBeUndefined();
  });
});
