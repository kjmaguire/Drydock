import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { CUT_MARKER, CUT_MIDDLE_MARKER, type AguiEvent, type AguiThread, type DrydockMeta } from '../../api/agui.gen.ts';
import type { ToolItem, Turn } from './activity.ts';
import {
  activityRows,
  baseName,
  changeLines,
  clockText,
  commandOf,
  editStat,
  HUNK_LINES,
  lastLine,
  secondsText,
  stepsOf,
  stepVerb,
  testFailures,
  thoughtText,
  toolKind,
  withoutCutMarkers,
  type EditStep,
  type ReadStep,
  type RunStep,
} from './activity-steps.ts';

const tool = (id: string, name: string, args: unknown, over: Partial<ToolItem> = {}): ToolItem => ({
  kind: 'tool',
  id,
  name,
  args: typeof args === 'string' ? args : JSON.stringify(args),
  isError: false,
  at: 1000,
  ...over,
});

const turn = (items: Turn['items'], status: Turn['status'] = 'done'): Turn => ({ runId: 'r:1', status, startedAt: 1000, items });

describe('the edit tool arguments as a change (diff stat and hunk)', () => {
  it('counts the lines an Edit removes and adds from its old and new strings', () => {
    const s = editStat(JSON.stringify({ file_path: 'parse.go', old_string: 'exp, n := digits(s[i:])', new_string: 'sign := 1\nif s[i] == \'-\' {\n  sign = -1\n}' }));
    expect([s.added, s.removed, s.more]).toEqual([4, 1, 0]);
    expect(s.hunk.map((l) => `${l.kind}${l.text}`)).toEqual(['-exp, n := digits(s[i:])', '+sign := 1', "+if s[i] == '-' {", '+  sign = -1', '+}']);
  });

  it('adds up every edit of a MultiEdit and shows at most the first lines of the change', () => {
    const edits = [
      { old_string: 'a\nb', new_string: 'c' },
      { old_string: 'd', new_string: 'e\nf\ng\nh' },
    ];
    const s = editStat(JSON.stringify({ file_path: 'x.go', edits }));
    expect([s.added, s.removed]).toEqual([5, 3]);
    expect(s.hunk.length).toBe(HUNK_LINES);
    expect(s.more).toBe(8 - HUNK_LINES);
  });

  it('reads a Write as every line added, and an ACP edit by its oldText and newText', () => {
    expect(editStat(JSON.stringify({ file_path: 'n.go', content: 'package n\n\nfunc N() {}\n' }))).toEqual({
      added: 3,
      removed: 0,
      hunk: [
        { kind: '+', text: 'package n' },
        { kind: '+', text: '' },
        { kind: '+', text: 'func N() {}' },
      ],
      more: 0,
    });
    expect(changeLines(JSON.stringify({ path: 'a', oldText: 'x', newText: 'y' }))).toEqual([
      { kind: '-', text: 'x' },
      { kind: '+', text: 'y' },
    ]);
  });

  it('has no change for arguments that carry none, or that are not JSON', () => {
    expect(changeLines(JSON.stringify({ file_path: 'a.go' }))).toBe(undefined);
    expect(editStat('not json')).toEqual({ added: 0, removed: 0, hunk: [], more: 0 });
    expect(editStat('')).toEqual({ added: 0, removed: 0, hunk: [], more: 0 });
  });
});

describe('a test run output: how many failed and why', () => {
  it("reads Go's failures without counting a parent test twice, joining the message under the failing subtest", () => {
    const out = [
      '--- FAIL: TestParse (0.00s)',
      '    --- FAIL: TestParse/-4e-2 (0.00s)',
      '        parse_test.go:31: got 0.04, want -0.04',
      'FAIL',
      'FAIL\tgocalc\t0.004s',
    ].join('\n');
    expect(testFailures(out)).toEqual({ failed: 1, line: 'TestParse/-4e-2: got 0.04, want -0.04' });
    expect(testFailures('--- FAIL: TestA (0.00s)\n--- FAIL: TestB (0.00s)\n    b_test.go:3: no\nFAIL').failed).toBe(2);
  });

  it('reads pytest, Jest and Vitest, and finds nothing in a run that passed', () => {
    expect(testFailures('FAILED tests/test_calc.py::test_sub - assert 8 == 2\n=== 1 failed, 3 passed ===')).toEqual({ failed: 1, line: 'tests/test_calc.py::test_sub: assert 8 == 2' });
    expect(testFailures('  ✕ subtracts (3 ms)\n  ✓ adds\nTests:       1 failed, 1 passed, 2 total')).toEqual({ failed: 1, line: 'subtracts' });
    expect(testFailures('ok  \tgocalc\t0.003s')).toEqual({ failed: 0, line: '' });
  });

  it('a failed command without test lines leaves its last line as the reason', () => {
    expect(lastLine('building...\n\n./calc.go:4:2: undefined: x\n\n')).toBe('./calc.go:4:2: undefined: x');
  });

  it("reads a cut output as the command's own lines: the lines the core put in where it cut are never the reason", () => {
    // the core keeps the head and the tail of a long result around a marker line; a test runner's summary is in the tail
    const cut = `${'=== RUN TestA\n'.repeat(3)}${CUT_MIDDLE_MARKER}\n--- FAIL: TestX\nFAIL\tpkg 1.2s`;
    expect(testFailures(cut)).toEqual({ failed: 1, line: 'TestX' });
    expect(lastLine(cut)).toBe('FAIL\tpkg 1.2s');
    // a text cut at its end carries the end marker as its last line: the reason is the command's last line
    const tail = `building...\n./a.go:1: oops\n${CUT_MARKER}`;
    expect(lastLine(tail)).toBe('./a.go:1: oops');
    expect(testFailures(tail)).toEqual({ failed: 0, line: '' });
    expect(withoutCutMarkers(`a\n${CUT_MARKER}\nb`)).toBe('a\nb');
    // a line that only mentions the marker is the command's own
    expect(withoutCutMarkers(`x ${CUT_MARKER}`)).toBe(`x ${CUT_MARKER}`);
    const steps = stepsOf(turn([tool('c1', 'Bash', { command: 'go test ./...' }, { result: cut, isError: true })]));
    expect([(steps[0] as RunStep).failed, (steps[0] as RunStep).failing]).toEqual([1, 'TestX']);
  });
});

describe('durations and clocks', () => {
  it('says how long something took in words', () => {
    expect([secondsText(400), secondsText(6000), secondsText(6400), secondsText(60_000), secondsText(125_000), secondsText(3_840_000), secondsText(7_200_000)]).toEqual([
      'under 1 s',
      '6 s',
      '6 s',
      '1 min',
      '2 min 5 s',
      '1 h 4 min',
      '2 h',
    ]);
    expect([secondsText(undefined), secondsText(-1), secondsText(Number.NaN)]).toEqual(['', '', '']);
  });

  it('a running time is a clock of minutes and seconds, with the hours past an hour', () => {
    expect([clockText(0), clockText(587_000), clockText(3_725_000), clockText(-5)]).toEqual(['0:00', '9:47', '1:02:05', '0:00']);
  });

  it('a thought says how long it took from its events, or only that it happened', () => {
    expect(thoughtText({ ms: 6000 })).toBe('Thought for 6 s');
    expect(thoughtText({ ms: 0 })).toBe('Thought');
  });
});

describe('a turn as compact steps', () => {
  it('names the kind of each tool, joins reads in a row, and reads edits, commands and searches', () => {
    expect(['Read', 'mcp__fs__read_file', 'Edit', 'Write', 'Bash', 'shell', 'Grep', 'Glob', 'WebFetch'].map(toolKind)).toEqual(['read', 'read', 'edit', 'edit', 'run', 'run', 'search', 'search', 'tool']);
    const steps = stepsOf(
      turn([
        tool('c1', 'Read', { file_path: 'internal/calc/parse.go' }, { result: 'package calc', endAt: 1100 }),
        tool('c2', 'Read', { file_path: 'internal/calc/parse_test.go' }, { result: 'package calc' }),
        tool('c3', 'Edit', { file_path: 'internal/calc/parse.go', old_string: 'a', new_string: 'b\nc' }, { result: 'ok' }),
        tool('c4', 'Bash', { command: 'go test ./...' }, { result: '--- FAIL: TestParse (0.00s)\n    parse_test.go:9: got 1, want 2\nFAIL', isError: true }),
        tool('c5', 'Grep', { pattern: 'digits\\(' }, { result: 'parse.go:41' }),
        tool('c6', 'WebFetch', { url: 'https://go.dev/ref/spec' }, { result: 'spec' }),
      ]),
    );
    expect(steps.map((s) => [s.kind, s.state, stepVerb(s)])).toEqual([
      ['read', 'ok', 'Read'],
      ['edit', 'ok', 'Edited'],
      ['run', 'failed', 'Ran'],
      ['search', 'ok', 'Searched'],
      ['tool', 'ok', 'WebFetch'],
    ]);
    const read = steps[0] as ReadStep;
    expect(read.files).toEqual(['internal/calc/parse.go', 'internal/calc/parse_test.go']);
    expect(read.files.map(baseName)).toEqual(['parse.go', 'parse_test.go']);
    expect(read.tools.map((x) => x.id)).toEqual(['c1', 'c2']);
    const edit = steps[1] as EditStep;
    expect([edit.file, edit.added, edit.removed]).toEqual(['internal/calc/parse.go', 2, 1]);
    const run = steps[2] as RunStep;
    expect([run.command, run.failed, run.failing]).toEqual(['go test ./...', 1, 'TestParse: got 1, want 2']);
  });

  it('a call without a result runs while its turn runs and has no result once it ended; a running read keeps its group running', () => {
    const items = [tool('c1', 'Read', { file_path: 'a.go' }, { result: 'x' }), tool('c2', 'Read', { file_path: 'b.go' }), tool('c3', 'Bash', { command: 'go test ./...' })];
    const live = stepsOf(turn(items, 'running'));
    expect(live.map((s) => [s.kind, s.state, stepVerb(s)])).toEqual([
      ['read', 'running', 'Reading'],
      ['run', 'running', 'Running'],
    ]);
    const over = stepsOf(turn(items, 'cancelled'));
    expect(over.map((s) => s.state)).toEqual(['unknown', 'unknown']);
  });

  it('a running read grouped with earlier reads counts its seconds from the read that runs, not from the first', () => {
    const live = stepsOf(turn([tool('c1', 'Read', { file_path: 'a.go' }, { at: 1000, result: 'x', endAt: 1100 }), tool('c2', 'Read', { file_path: 'b.go' }, { at: 2000, result: 'y', endAt: 2100 }), tool('c3', 'Read', { file_path: 'c.go' }, { at: 9000 })], 'running'));
    expect(live.map((s) => [s.kind, s.state, s.at, s.runningSince])).toEqual([['read', 'running', 1000, 9000]]);
    // a step that ended, or whose call has a result, counts nothing
    const done = stepsOf(turn([tool('c1', 'Read', { file_path: 'a.go' }, { at: 1000, result: 'x' }), tool('c2', 'Read', { file_path: 'b.go' }, { at: 2000, result: 'y' })], 'running'));
    expect(done.map((s) => [s.state, s.runningSince])).toEqual([['ok', undefined]]);
    const over = stepsOf(turn([tool('c1', 'Read', { file_path: 'a.go' }, { at: 1000 })], 'done'));
    expect(over.map((s) => [s.state, s.runningSince])).toEqual([['unknown', undefined]]);
  });

  it("a failed command that is not a test run gives its output's last line as the reason; a write says Wrote", () => {
    const steps = stepsOf(turn([tool('c1', 'Bash', { command: 'go build' }, { result: 'x\n./a.go:1: oops', isError: true }), tool('c2', 'Write', { file_path: 'n.go', content: 'a\nb' }, { result: 'ok' })]));
    expect((steps[0] as RunStep).failing).toBe('./a.go:1: oops');
    expect(stepVerb(steps[1]!)).toBe('Wrote');
  });

  it('reads a command given as a list of words, the shell running the last one', () => {
    expect(commandOf(JSON.stringify({ command: ['bash', '-lc', 'go test ./...'] }))).toBe('go test ./...');
    expect(commandOf(JSON.stringify({ command: ['go', 'vet', './...'] }))).toBe('go vet ./...');
    expect(commandOf('"npm test"')).toBe('npm test');
  });

  it('messages, thoughts with their time, and waits are steps; a message without text yet is left out', () => {
    const steps = stepsOf(
      turn([
        { kind: 'reasoning', id: 'r1', text: 'the leading minus is consumed twice', at: 2000, endAt: 8000 },
        { kind: 'text', id: 'm0', text: '', at: 8000 },
        { kind: 'text', id: 'm1', text: 'Fixed it.', at: 9000, endAt: 9000 },
        { kind: 'wait', id: 'apr_1', title: 'Bash: git push', asked: true, at: 9500, outcome: 'approved' },
        // a decision nobody was asked for is over from the start
        { kind: 'wait', id: 'p1', title: 'Write: config.json', asked: false, at: 9600, outcome: 'refused', reason: 'nobody could be asked in this session, so Drydock refused the request' },
      ]),
    );
    expect(steps.map((s) => [s.kind, s.state])).toEqual([
      ['thought', 'ok'],
      ['text', 'ok'],
      ['wait', 'ok'],
      ['wait', 'ok'],
    ]);
    expect(stepVerb(steps[0]!)).toBe('Thought for 6 s');
  });
});

describe('the rows of the feed', () => {
  let seq = 0;
  const at = (drydock: Partial<DrydockMeta> = {}) => {
    seq++;
    return { timestamp: seq * 1000, metadata: { drydock: { seq, ...drydock } } };
  };
  const thread = (id: string, events: AguiEvent[]): AguiThread => ({ threadId: id, agentId: `agent-${id}`, adapter: 'claude', model: 'sonnet', events });
  const twoTurns = (id: string): AguiThread =>
    thread(id, [
      { type: 'RUN_STARTED', ...at(), threadId: id, runId: `${id}:1`, protocolVersion: '1.0' },
      { type: 'TOOL_CALL_START', ...at(), toolCallId: `${id}-c1`, toolCallName: 'Read' },
      { type: 'TOOL_CALL_ARGS', ...at(), toolCallId: `${id}-c1`, delta: '{"file_path":"calc.go"}' },
      { type: 'TOOL_CALL_RESULT', ...at(), messageId: 'x', toolCallId: `${id}-c1`, role: 'tool', content: 'package calc' },
      { type: 'RUN_ERROR', ...at({ threadId: id, runId: `${id}:1` }), message: 'rate limited' },
      { type: 'RUN_STARTED', ...at(), threadId: id, runId: `${id}:2`, protocolVersion: '1.0' },
      { type: 'ACTIVITY_SNAPSHOT', ...at(), messageId: 'p', activityType: 'PLAN', replace: true, content: { entries: [{ content: 'Fix it', status: 'in_progress' }] } },
      { type: 'TOOL_CALL_START', ...at(), toolCallId: `${id}-c2`, toolCallName: 'Bash' },
      { type: 'TOOL_CALL_ARGS', ...at(), toolCallId: `${id}-c2`, delta: '{"command":"go test ./..."}' },
    ]);

  it('each process, its plan and its turns; the newest turn open, an earlier one a line until opened', () => {
    const rows = activityRows([twoTurns('w')]);
    expect(rows.map((r) => r.type)).toEqual(['thread', 'plan', 'turn', 'turn', 'step']);
    expect(rows.filter((r) => r.type === 'turn').map((r) => (r.type === 'turn' ? [r.n, r.open] : null))).toEqual([
      [1, false],
      [2, true],
    ]);
    const head = rows[0]!;
    expect(head.type === 'thread' ? [head.turns, head.working] : null).toEqual([2, true]);
    // opening the first turn shows its steps and how it ended
    const opened = activityRows([twoTurns('w2')], new Set(['turn:w2:0']));
    expect(opened.map((r) => r.type)).toEqual(['thread', 'plan', 'turn', 'step', 'end', 'turn', 'step']);
    // every key names one place
    const keys = opened.map((r) => r.key);
    expect(new Set(keys).size).toBe(keys.length);
  });

  it('a process with no turns yet is starting when it is the newest, and left out when an earlier one never turned', () => {
    const empty = (id: string): AguiThread => thread(id, []);
    const rows = activityRows([empty('a'), twoTurns('b'), empty('c')]);
    expect(rows.filter((r) => r.type === 'thread').map((r) => (r.type === 'thread' ? [r.thread.threadId, r.turns, r.working] : null))).toEqual([
      ['b', 2, true],
      ['c', 0, false],
    ]);
    expect(rows[rows.length - 1]!.type).toBe('thread');
    // every key names one place
    const keys = rows.map((r) => r.key);
    expect(new Set(keys).size).toBe(keys.length);
  });

  it('a process with one turn has no turn line; processes follow in the order they started', () => {
    const one = thread('a', [
      { type: 'RUN_STARTED', ...at(), threadId: 'a', runId: 'a:1', protocolVersion: '1.0' },
      { type: 'TEXT_MESSAGE_START', ...at(), messageId: 'm', role: 'assistant' },
      { type: 'TEXT_MESSAGE_CONTENT', ...at(), messageId: 'm', delta: 'SHIP' },
      { type: 'RUN_FINISHED', ...at(), threadId: 'a', runId: 'a:1', outcome: { type: 'success' } },
    ]);
    const rows = activityRows([one, twoTurns('b')]);
    expect(rows.slice(0, 2).map((r) => r.type)).toEqual(['thread', 'step']);
    expect(rows.filter((r) => r.type === 'thread').map((r) => r.thread.threadId)).toEqual(['a', 'b']);
  });
});
