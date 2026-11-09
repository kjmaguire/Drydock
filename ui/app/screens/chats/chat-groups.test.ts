import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { ChatMessage } from '../../api/chatClient.ts';
import {
  acceptReply,
  askOf,
  burstCounts,
  burstLine,
  burstMs,
  burstState,
  changedText,
  groupFeed,
  isVerboseKey,
  lastSentence,
  memoGroups,
  readVerbose,
  stepText,
  suggestReplies,
  thinkingMs,
  thoughtText,
  toolKind,
  turnChanges,
  VERBOSE_KEY,
  writeVerbose,
} from './chat-groups.ts';

let seq = 0;
const msg = (role: string, extra: Partial<ChatMessage> = {}): ChatMessage => {
  seq++;
  return { id: `m${seq}`, chatId: 'c1', seq, role, body: extra.body ?? `${role} ${seq}`, createdAt: seq * 1000, ...extra };
};
const read = (path: string, extra: Partial<ChatMessage> = {}) => msg('tool', { toolName: 'Read', toolInput: { file_path: path }, toolStatus: 'ok', toolMs: 100, ...extra });
const bash = (command: string, extra: Partial<ChatMessage> = {}) => msg('tool', { toolName: 'Bash', toolInput: { command }, toolStatus: 'ok', toolMs: 1000, ...extra });
const edit = (path: string, o: string, n: string, extra: Partial<ChatMessage> = {}) =>
  msg('tool', { toolName: 'Edit', toolInput: { file_path: path, old_string: o, new_string: n }, toolStatus: 'ok', toolMs: 50, ...extra });

describe('burst grouping (P139)', () => {
  it('folds the tool calls between two pieces of text into one burst, keyed by its first call', () => {
    const list = [msg('user'), read('a.go'), read('b.go'), bash('go test ./...'), msg('assistant'), bash('ls'), msg('assistant')];
    const items = groupFeed(list);
    expect(items.map((i) => i.kind)).toEqual(['msg', 'burst', 'msg', 'burst', 'msg']);
    const b = items[1]!;
    if (b.kind !== 'burst') throw new Error('not a burst');
    expect(b.tools.map((m) => m.id)).toEqual([list[1]!.id, list[2]!.id, list[3]!.id]);
    expect(b.key).toBe(`burst:${list[1]!.id}`);
    // a message row keeps its index in the list (the row before it decides its spacing)
    const m = items[2]!;
    expect(m.kind === 'msg' && m.index).toBe(4);
  });

  it('a refusal, a note or the person breaks a burst', () => {
    const items = groupFeed([read('a'), msg('denied'), read('b'), msg('system'), read('c'), msg('user'), read('d')]);
    expect(items.map((i) => i.kind)).toEqual(['burst', 'msg', 'burst', 'msg', 'burst', 'msg', 'burst']);
  });

  it('keeps the burst key as calls are added to it, and works the rows out again only for a new list', () => {
    const a = read('a');
    const first = groupFeed([msg('user'), a]);
    const second = groupFeed([msg('user'), a, bash('ls')]);
    expect(first[1]!.key).toBe(second[1]!.key);
    const memo = memoGroups();
    const list = [msg('user'), read('x')];
    const r1 = memo(list);
    expect(memo(list)).toBe(r1);
    expect(memo([...list])).not.toBe(r1);
  });

  it('says what a burst did in words', () => {
    expect(burstCounts([read('a'), read('b'), read('a'), bash('x'), bash('y'), bash('z')])).toBe('read 2 files, ran 3 commands');
    expect(burstCounts([edit('a', '1', '2')])).toBe('edited 1 file');
    expect(burstCounts([msg('tool', { toolName: 'Grep', toolInput: { pattern: 'x' } }), msg('tool', { toolName: 'WebFetch', toolInput: { url: 'u' } })])).toBe('1 search, 1 web lookup');
    expect(burstCounts([msg('tool', { toolName: 'mcp__drydock__skills' }), msg('tool', { toolName: 'mcp__drydock__skills' })])).toBe('used Drydock · skills 2 times');
    expect(burstCounts([msg('tool', { toolName: 'mcp__a__x' }), msg('tool', { toolName: 'mcp__b__y' })])).toBe('used 2 tools');
    // a command is a command whatever the tool is called (an ACP terminal)
    expect(toolKind({ toolName: 'execute', toolInput: { command: 'ls' } })).toBe('command');
    expect(toolKind({ toolName: 'whatever', toolInput: { oldText: 'a', newText: 'b', path: 'x' } })).toBe('edit');
  });

  it('puts the reasoning on the line, and the total time', () => {
    const tools = [read('a'), bash('b')];
    expect(burstLine(tools, { any: false, ms: 0 })).toBe('Read 1 file, ran 1 command');
    expect(burstLine(tools, { any: true, ms: 6200 })).toBe('Thought for 6 s · read 1 file, ran 1 command');
    expect(burstLine(tools, { any: true, ms: 0 })).toBe('Thought · read 1 file, ran 1 command');
    expect(burstMs(tools)).toBe(1100);
    expect(thoughtText(800)).toBe('Thought');
    expect(thoughtText(64_000)).toBe('Thought for 1 min 4 s');
  });

  it('measures thinking from the end of the row before', () => {
    expect(thinkingMs({ createdAt: 10_000, toolMs: 2000 }, { createdAt: 18_000 })).toBe(6000);
    expect(thinkingMs(undefined, { createdAt: 18_000 })).toBe(0);
    expect(thinkingMs({ createdAt: 20_000 }, { createdAt: 18_000 })).toBe(0);
    expect(thinkingMs({ createdAt: 1 }, { createdAt: 2 * 3600_000 })).toBe(0);
  });

  it('a failed call fails the burst; a running one names its step', () => {
    expect(burstState([read('a'), bash('x', { toolStatus: 'failed' })], false)).toBe('failed');
    expect(burstState([read('a'), bash('x', { toolStatus: undefined })], true)).toBe('running');
    expect(burstState([read('a'), bash('x', { toolStatus: undefined })], false)).toBe('ok');
    expect(stepText({ toolName: 'Bash', toolInput: { command: 'npm test' } })).toBe('Running npm test');
    expect(stepText({ toolName: 'Read', toolInput: { file_path: 'src/calc/calc.go' } })).toBe('Reading calc.go');
    expect(stepText({ toolName: 'Edit', toolInput: { file_path: 'C:\\x\\y.go' } })).toBe('Editing y.go');
    expect(stepText({ toolName: 'Grep', toolInput: { pattern: 'Round' } })).toBe('Searching for Round');
    expect(stepText({ toolName: 'mcp__drydock__skills' })).toBe('Using Drydock · skills');
  });
});

describe('the changes of a turn (P139)', () => {
  it('rolls up every edit of a turn into one card after its last row, per turn', () => {
    const list = [
      msg('user'),
      edit('calc.go', 'a\nb', 'c'),
      msg('assistant'),
      edit('calc.go', 'x', 'y\nz'),
      msg('tool', { toolName: 'Write', toolInput: { file_path: 'new.go', content: 'package x\n\nfunc A() {}\n' }, toolStatus: 'ok' }),
      edit('bad.go', 'p', 'q', { toolStatus: 'failed' }),
      msg('assistant'),
      msg('user'),
      read('calc.go'),
      msg('assistant'),
    ];
    const items = groupFeed(list);
    expect(items.map((i) => i.kind)).toEqual(['msg', 'burst', 'msg', 'burst', 'msg', 'changes', 'msg', 'burst', 'msg']);
    const card = items[5]!;
    if (card.kind !== 'changes') throw new Error('no card');
    expect(card.key).toBe(`changes:${list[1]!.id}`);
    expect(card.changes.files.map((f) => [f.path, f.added, f.removed, f.edits])).toEqual([
      ['calc.go', 3, 3, 2],
      ['new.go', 3, 0, 1],
    ]);
    expect([card.changes.added, card.changes.removed]).toEqual([6, 3]);
    expect(changedText(card.changes)).toBe('Changed 2 files');
    expect(card.changes.files[0]!.lines.map((l) => `${l.kind}${l.text}`)).toEqual(['-a', '-b', '+c', '-x', '+y', '+z']);
  });

  it('a turn still running has its card at the end; a turn without edits has none', () => {
    const items = groupFeed([msg('user'), edit('a.go', '', 'x')]);
    expect(items.map((i) => i.kind)).toEqual(['msg', 'burst', 'changes']);
    expect(groupFeed([msg('user'), read('a'), msg('assistant')]).some((i) => i.kind === 'changes')).toBe(false);
    // an edit that names no file changes nothing the card can list
    expect(turnChanges([msg('tool', { toolName: 'Edit', toolInput: { old_string: 'a', new_string: 'b' } })]).files).toEqual([]);
  });
});

describe('suggested replies (P139)', () => {
  it('reads the last sentence of an answer', () => {
    expect(lastSentence('Results are rounded.\n\n- one\n- two\n\nWant me to route both through `calc.Round` and add a test?')).toBe('Want me to route both through `calc.Round` and add a test?');
    expect(lastSentence('It works. **Shall I commit it?**')).toBe('Shall I commit it?');
    expect(lastSentence('')).toBe('');
  });

  it('tells an offer from a yes-or-no question and an open one, and nothing from a statement', () => {
    expect(askOf('Want me to fix the rounding?')).toEqual({ kind: 'offer', what: 'fix the rounding' });
    expect(askOf('Would you like me to add a test?')).toEqual({ kind: 'offer', what: 'add a test' });
    expect(askOf('Should I go ahead?')).toEqual({ kind: 'offer', what: 'go ahead' });
    expect(askOf('Let me know if you want me to update your docs.')).toEqual({ kind: 'offer', what: 'update your docs' });
    expect(askOf('I can also add a benchmark if you like.')).toEqual({ kind: 'offer', what: 'add a benchmark' });
    expect(askOf('Is that what you meant?')).toEqual({ kind: 'yesno' });
    expect(askOf('Which one do you prefer?')).toEqual({ kind: 'open' });
    expect(askOf('Done: the tests pass.')).toBe(undefined);
    expect(askOf("I can't find it.")).toBe(undefined);
  });

  it('offers up to three replies that name what they accept', () => {
    expect(acceptReply('fix the rounding')).toEqual({ label: 'Yes, fix it', text: 'Yes, please fix the rounding.' });
    expect(acceptReply('go ahead')).toEqual({ label: 'Yes, go ahead', text: 'Yes, please go ahead.' });
    expect(acceptReply('update your docs').text).toBe('Yes, please update my docs.');
    expect(suggestReplies('Want me to fix the rounding?').map((r) => r.label)).toEqual(['Yes, fix it', 'Not now', 'Explain more']);
    expect(suggestReplies('Is that right?').map((r) => r.label)).toEqual(['Yes', 'No', 'Explain more']);
    expect(suggestReplies('Which do you prefer?').map((r) => r.label)).toEqual(['You decide', 'Explain more']);
    expect(suggestReplies('All done.')).toEqual([]);
    for (const body of ['Want me to fix it?', 'Is it ok?', 'Which?']) expect(suggestReplies(body).length <= 3).toBe(true);
  });
});

describe('verbose mode (P139)', () => {
  const store = () => {
    const m = new Map<string, string>();
    return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k), m };
  };
  it('is remembered per viewer, and storage that refuses is not an error', () => {
    const s = store();
    expect(readVerbose(s)).toBe(false);
    writeVerbose(true, s);
    expect(s.m.get(VERBOSE_KEY)).toBe('1');
    expect(readVerbose(s)).toBe(true);
    writeVerbose(false, s);
    expect(readVerbose(s)).toBe(false);
    const broken = {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => {
        throw new Error('denied');
      },
      removeItem: () => {
        throw new Error('denied');
      },
    };
    expect(readVerbose(broken)).toBe(false);
    writeVerbose(true, broken);
    expect(readVerbose(undefined)).toBe(false);
  });
  it('is Ctrl+O alone', () => {
    const k = (key: string, o: Partial<KeyboardEvent> = {}) => ({ key, ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, repeat: false, ...o });
    expect(isVerboseKey(k('o'))).toBe(true);
    expect(isVerboseKey(k('O'))).toBe(true);
    expect(isVerboseKey(k('o', { shiftKey: true }))).toBe(false);
    expect(isVerboseKey(k('o', { ctrlKey: false }))).toBe(false);
    expect(isVerboseKey(k('o', { repeat: true }))).toBe(false);
    expect(isVerboseKey(k('p'))).toBe(false);
  });
});
