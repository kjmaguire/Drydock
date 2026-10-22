import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AguiEvent } from '../../api/agui.gen.ts';
import { argsSummary, argsValue, planOf, planProgress, turnSummary, turnsOf, waitOutcomeText } from './activity.ts';

let seq = 0;
const at = () => ({ seq: ++seq, timestamp: seq * 1000 });

describe('a task agent activity as turns (P87)', () => {
  it('folds a thread into turns with messages, reasoning and tool calls in the order they happened', () => {
    const evs: AguiEvent[] = [
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:1' },
      { type: 'REASONING_MESSAGE_START', ...at(), messageId: 'x', role: 'reasoning' },
      { type: 'REASONING_MESSAGE_CONTENT', ...at(), messageId: 'x', delta: 'look first' },
      { type: 'REASONING_MESSAGE_END', ...at(), messageId: 'x' },
      { type: 'TEXT_MESSAGE_START', ...at(), messageId: 'm', role: 'assistant' },
      { type: 'TEXT_MESSAGE_CONTENT', ...at(), messageId: 'm', delta: 'Reading ' },
      { type: 'TEXT_MESSAGE_CONTENT', ...at(), messageId: 'm', delta: 'it.' },
      { type: 'TEXT_MESSAGE_END', ...at(), messageId: 'm' },
      { type: 'TOOL_CALL_START', ...at(), toolCallId: 'c1', toolCallName: 'Bash' },
      { type: 'TOOL_CALL_ARGS', ...at(), toolCallId: 'c1', delta: '{"command":"go test ./..."}' },
      { type: 'TOOL_CALL_END', ...at(), toolCallId: 'c1' },
      { type: 'TOOL_CALL_RESULT', ...at(), messageId: 't', toolCallId: 'c1', role: 'tool', content: 'FAIL', isError: true },
      { type: 'TOOL_CALL_START', ...at(), toolCallId: 'c2', toolCallName: 'Read' },
      { type: 'RUN_FINISHED', ...at(), threadId: 'r', runId: 'r:1', outcome: { type: 'success' }, stopReason: 'end_turn' },
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:2' },
      { type: 'RUN_ERROR', ...at(), threadId: 'r', runId: 'r:2', message: 'exit 2' },
      // output read from the middle of a turn opens one
      { type: 'TEXT_MESSAGE_CONTENT', ...at(), messageId: 'late', delta: 'tail' },
      { type: 'TOOL_CALL_RESULT', ...at(), messageId: 't2', toolCallId: 'c9', role: 'tool', content: 'ok' },
    ];
    const turns = turnsOf(evs);
    expect(turns.map((t) => [t.runId, t.status])).toEqual([
      ['r:1', 'done'],
      ['r:2', 'error'],
      ['', 'running'],
    ]);
    const [one, two, three] = turns;
    expect(one!.items.map((i) => (i.kind === 'tool' ? `tool ${i.name} ${i.args} -> ${i.result ?? '(running)'}${i.isError ? ' !' : ''}` : i.kind === 'wait' ? `wait ${i.title}` : `${i.kind} ${i.text}`))).toEqual([
      'reasoning look first',
      'text Reading it.',
      'tool Bash {"command":"go test ./..."} -> FAIL !',
      'tool Read  -> (running)',
    ]);
    expect(one!.stopReason).toBe('end_turn');
    expect(turnSummary(one!)).toBe('2 tool calls, 1 failed · done');
    expect(two!.error).toBe('exit 2');
    expect(turnSummary(two!)).toBe('0 tool calls · error');
    expect(three!.items.map((i) => i.kind)).toEqual(['text', 'tool']);
    expect(turnSummary(three!)).toBe('1 tool call · working');
  });

  it('reads a failed turn as an error and a cancelled one as stopped, never as done', () => {
    const turns = turnsOf([
      // a turn the agent reported as failed (a Claude result with is_error, an ACP prompt that failed): the core sends RUN_ERROR
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:1' },
      { type: 'TOOL_CALL_START', ...at(), toolCallId: 'c1', toolCallName: 'Bash' },
      { type: 'TOOL_CALL_END', ...at(), toolCallId: 'c1' },
      { type: 'RUN_ERROR', ...at(), threadId: 'r', runId: 'r:1', message: 'Reached maximum number of turns (3)', code: 'error_max_turns' },
      // interrupted by a person
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:2' },
      { type: 'CUSTOM', ...at(), name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: ls' } },
      { type: 'RUN_FINISHED', ...at(), threadId: 'r', runId: 'r:2', outcome: { type: 'cancelled' }, stopReason: 'interrupted' },
      // its program ended during the turn
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:3' },
      { type: 'RUN_FINISHED', ...at(), threadId: 'r', runId: 'r:3', outcome: { type: 'cancelled' } },
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:4' },
      { type: 'RUN_FINISHED', ...at(), threadId: 'r', runId: 'r:4', outcome: { type: 'success' }, stopReason: 'end_turn' },
    ]);
    expect(turns.map((t) => [t.runId, t.status, t.stopReason ?? '', t.error ?? ''])).toEqual([
      ['r:1', 'error', '', 'Reached maximum number of turns (3)'],
      ['r:2', 'cancelled', 'interrupted', ''],
      ['r:3', 'cancelled', '', ''],
      ['r:4', 'done', 'end_turn', ''],
    ]);
    expect(turns.map(turnSummary)).toEqual(['1 tool call · error', '0 tool calls · stopped', '0 tool calls · stopped', '0 tool calls · done']);
    // the wait of the interrupted turn is over without an answer
    expect(waitOutcomeText(undefined, false)).toBe('Not answered before the turn ended');
  });

  it('shows each wait for a person in its turn and how it was answered (P88)', () => {
    const turns = turnsOf([
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:1' },
      { type: 'CUSTOM', ...at(), name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: npm install', expiresAt: 5 } },
      { type: 'CUSTOM', ...at(), name: 'drydock.interrupt', value: { id: 'apr_2', title: 'Write: a.txt' } },
      { type: 'CUSTOM', ...at(), name: 'drydock.resume', value: { id: 'apr_1', outcome: 'approved_with_edits' } },
      { type: 'CUSTOM', ...at(), name: 'drydock.resume', value: { id: 'apr_9', outcome: 'rejected' } },
    ]);
    expect(turns[0]!.items).toEqual([
      { kind: 'wait', id: 'apr_1', title: 'Bash: npm install', expiresAt: 5, outcome: 'approved_with_edits' },
      { kind: 'wait', id: 'apr_2', title: 'Write: a.txt', expiresAt: undefined },
    ]);
    expect(['', 'approved', 'approved_with_edits', 'rejected', 'timed_out', 'cancelled', 'denied'].map((o) => waitOutcomeText(o))).toEqual([
      'Waiting for you',
      'Allowed once by you',
      'Allowed by you with your changes',
      'Refused by you',
      'Nobody answered in time, so it was refused',
      'Withdrawn before anyone answered',
      "Refused by Drydock's rules",
    ]);
  });

  it('keeps the newest plan and says how far it is', () => {
    const plan = (entries: { content: string; status?: string }[]): AguiEvent => ({ type: 'ACTIVITY_SNAPSHOT', ...at(), messageId: 'p', activityType: 'PLAN', content: { entries }, replace: true });
    expect(planOf([])).toBe(undefined);
    const p = planOf([plan([{ content: 'a', status: 'in_progress' }]), plan([{ content: 'a', status: 'completed' }, { content: 'b', status: 'in_progress' }, { content: 'c' }])]);
    expect(p!.map((e) => e.content)).toEqual(['a', 'b', 'c']);
    expect(planProgress(p!)).toBe('1 of 3 done');
  });

  it('says a call in one line from its most telling argument', () => {
    expect(argsSummary('{"command":"go   test\\n ./..."}')).toBe('go test ./...');
    expect(argsSummary('{"file_path":"C:\\\\src\\\\a.go","content":"x"}')).toBe('C:\\src\\a.go');
    expect(argsSummary('{"n":1}')).toBe('{"n":1}');
    expect(argsSummary('not json')).toBe('not json');
    expect(argsSummary(`{"command":"${'x'.repeat(300)}"}`).length).toBe(160);
    expect(argsValue('')).toBe(undefined);
    expect(argsValue('{"a":1}')).toEqual({ a: 1 });
  });
});
