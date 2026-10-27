import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AguiEvent, DrydockMeta } from '../../api/agui.gen.ts';
import { argsSummary, argsValue, planOf, planProgress, turnSummary, turnsOf, waitOutcomeText } from './activity.ts';

let seq = 0;
const at = (drydock: Partial<DrydockMeta> = {}) => {
  seq++;
  return { timestamp: seq * 1000, metadata: { drydock: { seq, ...drydock } } };
};

describe('a task agent activity as turns (P87)', () => {
  it('folds a thread into turns with messages, reasoning and tool calls in the order they happened', () => {
    const evs: AguiEvent[] = [
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:1', protocolVersion: '1.0' },
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
      { type: 'TOOL_CALL_RESULT', ...at({ isError: true }), messageId: 't', toolCallId: 'c1', role: 'tool', content: 'FAIL' },
      { type: 'TOOL_CALL_START', ...at(), toolCallId: 'c2', toolCallName: 'Read' },
      { type: 'RUN_FINISHED', ...at({ stopReason: 'end_turn' }), threadId: 'r', runId: 'r:1', outcome: { type: 'success' } },
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:2', protocolVersion: '1.0' },
      { type: 'RUN_ERROR', ...at({ threadId: 'r', runId: 'r:2' }), message: 'exit 2' },
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
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:1', protocolVersion: '1.0' },
      { type: 'TOOL_CALL_START', ...at(), toolCallId: 'c1', toolCallName: 'Bash' },
      { type: 'TOOL_CALL_END', ...at(), toolCallId: 'c1' },
      { type: 'RUN_ERROR', ...at({ threadId: 'r', runId: 'r:1' }), message: 'Reached maximum number of turns (3)', code: 'error_max_turns' },
      // interrupted by a person
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:2', protocolVersion: '1.0' },
      { type: 'CUSTOM', ...at(), name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: ls' } },
      { type: 'RUN_FINISHED', ...at({ stopReason: 'interrupted' }), threadId: 'r', runId: 'r:2', outcome: { type: 'cancelled' } },
      // its program ended during the turn
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:3', protocolVersion: '1.0' },
      { type: 'RUN_FINISHED', ...at(), threadId: 'r', runId: 'r:3', outcome: { type: 'cancelled' } },
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:4', protocolVersion: '1.0' },
      { type: 'RUN_FINISHED', ...at({ stopReason: 'end_turn' }), threadId: 'r', runId: 'r:4', outcome: { type: 'success' } },
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
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:1', protocolVersion: '1.0' },
      { type: 'CUSTOM', ...at(), name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: npm install', expiresAt: 5 } },
      { type: 'CUSTOM', ...at(), name: 'drydock.interrupt', value: { id: 'apr_2', title: 'Write: a.txt' } },
      { type: 'CUSTOM', ...at(), name: 'drydock.resume', value: { id: 'apr_1', outcome: 'approved_with_edits' } },
      { type: 'CUSTOM', ...at(), name: 'drydock.resume', value: { id: 'apr_9', outcome: 'rejected' } },
    ]);
    expect(turns[0]!.items.map((i) => ({ ...i, at: undefined }))).toEqual([
      { kind: 'wait', id: 'apr_1', title: 'Bash: npm install', expiresAt: 5, asked: true, outcome: 'approved_with_edits', at: undefined },
      { kind: 'wait', id: 'apr_2', title: 'Write: a.txt', expiresAt: undefined, asked: true, at: undefined },
    ]);
    // each wait says when it began (the event's time)
    expect(turns[0]!.items.map((i) => i.at > 0)).toEqual([true, true]);
    expect(['', 'approved', 'approved_with_edits', 'approved_for_task', 'rejected', 'timed_out', 'cancelled', 'denied', 'allowed_by_rule', 'refused'].map((o) => waitOutcomeText(o))).toEqual([
      'Waiting for you',
      'Allowed once by you',
      'Allowed by you with your changes',
      'Allowed by you for this task',
      'Refused by you',
      'Nobody answered in time, so it was refused',
      'Withdrawn before anyone answered',
      "Refused by Drydock's rules",
      "Allowed by Drydock's rules",
      'Refused without asking',
    ]);
    // a word the core may add later is not hidden, and not shown bare either
    expect(waitOutcomeText('some_new_outcome')).toBe('Decided (some_new_outcome)');
  });

  it('folds a decision nobody was asked for into its turn as a wait that was never open', () => {
    const turns = turnsOf([
      { type: 'RUN_STARTED', ...at(), threadId: 'r', runId: 'r:1', protocolVersion: '1.0' },
      // a rule refused the call, and one allowed by a task rule the person made: named after the event, there is no call id
      { type: 'CUSTOM', ...at(), name: 'drydock.decided', value: { tool: 'Bash', outcome: 'denied', rule: 'no git push' } },
      { type: 'CUSTOM', ...at(), name: 'drydock.decided', value: { tool: 'Bash', outcome: 'approved_for_task', rule: 'go test' } },
      // the agent refused a call itself: named after the call's card (the core names only the tool), with the reason the core gave
      { type: 'TOOL_CALL_START', ...at(), toolCallId: 'p1', toolCallName: 'Write' },
      { type: 'TOOL_CALL_ARGS', ...at(), toolCallId: 'p1', delta: '{"file_path":"config.json","content":"{}"}' },
      { type: 'CUSTOM', ...at(), name: 'drydock.decided', value: { tool: 'Write', outcome: 'refused', reason: 'nobody could be asked in this session, so Drydock refused the request', toolCallId: 'p1' } },
      // one the agent refused before its card was seen keeps the core's name
      { type: 'CUSTOM', ...at(), name: 'drydock.decided', value: { tool: 'WebFetch', outcome: 'refused', reason: 'the agent\'s own permission mode did not allow it', toolCallId: 'p2' } },
      // a resume names a wait, never a decision
      { type: 'CUSTOM', ...at(), name: 'drydock.resume', value: { id: 'p1', outcome: 'approved' } },
    ]);
    const seqs = turns[0]!.items.map((i) => i.at / 1000);
    expect(turns[0]!.items.map((i) => ({ ...i, at: undefined }))).toEqual([
      { kind: 'wait', id: `d${seqs[0]}`, title: 'Bash', asked: false, outcome: 'denied', reason: undefined, rule: 'no git push', at: undefined },
      { kind: 'wait', id: `d${seqs[1]}`, title: 'Bash', asked: false, outcome: 'approved_for_task', reason: undefined, rule: 'go test', at: undefined },
      { kind: 'tool', id: 'p1', name: 'Write', args: '{"file_path":"config.json","content":"{}"}', isError: false, at: undefined },
      { kind: 'wait', id: 'p1', title: 'Write: config.json', asked: false, outcome: 'refused', reason: 'nobody could be asked in this session, so Drydock refused the request', rule: undefined, at: undefined },
      { kind: 'wait', id: 'p2', title: 'WebFetch', asked: false, outcome: 'refused', reason: "the agent's own permission mode did not allow it", rule: undefined, at: undefined },
    ]);
    // the two decisions named after their events have different ids
    expect(new Set(turns[0]!.items.map((i) => `${i.kind}:${i.id}`)).size).toBe(5);
    expect(turnSummary(turns[0]!)).toBe('1 tool call · working');
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
