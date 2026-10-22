import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { AguiActivity } from '../../api/agui.gen.ts';
import { cleanup, mount, waitFor } from '../../test/render.tsx';
import { TaskActivity } from './activity-panel.tsx';
import { AGENTS, recorder, svc } from './testing.ts';

afterEach(cleanup);

const activity: AguiActivity = {
  threads: [
    {
      threadId: 'run_w',
      agentId: 'a1',
      events: [
        { type: 'RUN_STARTED', seq: 1, timestamp: 1, threadId: 'run_w', runId: 'run_w:1' },
        { type: 'TEXT_MESSAGE_START', seq: 2, timestamp: 2, messageId: 'm1', role: 'assistant' },
        { type: 'TEXT_MESSAGE_CONTENT', seq: 2, timestamp: 2, messageId: 'm1', delta: 'First turn.' },
        { type: 'TEXT_MESSAGE_END', seq: 3, timestamp: 3, messageId: 'm1' },
        { type: 'RUN_FINISHED', seq: 4, timestamp: 4, threadId: 'run_w', runId: 'run_w:1', outcome: { type: 'success' } },
        { type: 'RUN_STARTED', seq: 5, timestamp: 5, threadId: 'run_w', runId: 'run_w:2' },
        {
          type: 'ACTIVITY_SNAPSHOT',
          seq: 6,
          timestamp: 6,
          messageId: 'run_w:plan',
          activityType: 'PLAN',
          replace: true,
          content: { entries: [{ content: 'Read calc.go', status: 'completed' }, { content: 'Fix Subtract', status: 'in_progress' }, { content: 'Run the tests' }] },
        },
        { type: 'REASONING_MESSAGE_START', seq: 7, timestamp: 7, messageId: 'r1', role: 'reasoning' },
        { type: 'REASONING_MESSAGE_CONTENT', seq: 7, timestamp: 7, messageId: 'r1', delta: 'Subtract adds; it should subtract.' },
        { type: 'REASONING_MESSAGE_END', seq: 8, timestamp: 8, messageId: 'r1' },
        { type: 'TOOL_CALL_START', seq: 9, timestamp: 9, toolCallId: 'c1', toolCallName: 'Bash' },
        { type: 'TOOL_CALL_ARGS', seq: 9, timestamp: 9, toolCallId: 'c1', delta: '{"command":"go test ./..."}' },
        { type: 'TOOL_CALL_END', seq: 9, timestamp: 9, toolCallId: 'c1' },
        { type: 'TOOL_CALL_RESULT', seq: 10, timestamp: 10, messageId: 't1', toolCallId: 'c1', role: 'tool', content: '<img src=x onerror=alert(1)> FAIL', isError: true },
        { type: 'TOOL_CALL_START', seq: 11, timestamp: 11, toolCallId: 'c2', toolCallName: 'mcp__drydock__search_memory' },
        { type: 'CUSTOM', seq: 12, timestamp: 12, name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: npm install left-pad' } },
        { type: 'CUSTOM', seq: 13, timestamp: 13, name: 'drydock.resume', value: { id: 'apr_1', outcome: 'rejected' } },
        { type: 'CUSTOM', seq: 14, timestamp: 14, name: 'drydock.interrupt', value: { id: 'apr_2', title: 'Bash: rm -rf build' } },
      ],
    },
    {
      threadId: 'run_r',
      agentId: 'a2',
      events: [
        // a turn interrupted while it waited for a person, then one that failed
        { type: 'RUN_STARTED', seq: 18, timestamp: 18, threadId: 'run_r', runId: 'run_r:1' },
        { type: 'CUSTOM', seq: 18, timestamp: 18, name: 'drydock.interrupt', value: { id: 'apr_3', title: 'Bash: git push' } },
        { type: 'RUN_FINISHED', seq: 19, timestamp: 19, threadId: 'run_r', runId: 'run_r:1', outcome: { type: 'cancelled' }, stopReason: 'interrupted' },
        { type: 'RUN_STARTED', seq: 20, timestamp: 20, threadId: 'run_r', runId: 'run_r:2' },
        { type: 'RUN_ERROR', seq: 20, timestamp: 20, threadId: 'run_r', runId: 'run_r:2', message: 'rate limited' },
      ],
    },
  ],
};

describe('the task page Activity (P87)', () => {
  it('shows each agent process, newest first, with its plan, its turns and its tool calls as cards', async () => {
    const read = recorder(async (_id: string) => activity);
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: read }));
    await waitFor(() => expect(r.container.querySelectorAll('[data-testid=activity-thread]').length).toBe(2), r);
    expect(read.calls[0]).toEqual(['t1']);
    const threads = Array.from(r.container.querySelectorAll<HTMLElement>('[data-testid=activity-thread]'));
    expect(threads.map((t) => t.dataset.agent)).toEqual(['a2', 'a1']);
    const [rex, dana] = threads as [HTMLElement, HTMLElement];
    expect(rex.textContent).toContain('Rex');
    expect(rex.querySelector('[data-testid=activity-error]')!.textContent).toContain('rate limited');
    // a cancelled turn is stopped, not done, and its unanswered wait is over
    const rexTurns = Array.from(rex.querySelectorAll<HTMLDetailsElement>('[data-testid=activity-turn]'));
    expect(rexTurns.map((t) => [t.dataset.status, t.querySelector('summary')!.textContent])).toEqual([
      ['cancelled', 'Turn 1 · 0 tool calls · stopped'],
      ['error', 'Turn 2 · 0 tool calls · error'],
    ]);
    expect(rexTurns[0]!.querySelector('[data-testid=activity-cancelled]')!.textContent).toBe('The turn was interrupted before it finished.');
    const over = rexTurns[0]!.querySelector<HTMLElement>('[data-testid=activity-wait]')!;
    expect([over.dataset.open ?? '', over.textContent]).toEqual(['', 'Not answered before the turn ended:Bash: git push']);
    expect(rex.textContent).not.toContain('working now');

    // the plan as a checklist with its progress
    const plan = dana.querySelector('[data-testid=activity-plan]')!;
    expect(plan.getAttribute('aria-label')).toBe('Plan, 1 of 3 done');
    expect(Array.from(plan.querySelectorAll('li[data-status]')).map((li) => li.getAttribute('data-status'))).toEqual(['completed', 'in_progress', 'pending']);

    // the newest turn is open and working, the earlier one folded to one line
    const turns = Array.from(dana.querySelectorAll<HTMLDetailsElement>('[data-testid=activity-turn]'));
    expect(turns.map((t) => [t.dataset.status, t.open])).toEqual([
      ['done', false],
      ['running', true],
    ]);
    expect(turns[0]!.querySelector('summary')!.textContent).toBe('Turn 1 · 0 tool calls · done');
    expect(dana.textContent).toContain('working now');
    expect(turns[1]!.querySelector('[data-testid=activity-reasoning]')!.textContent).toContain('Subtract adds');

    // tool calls are the chat's cards: the failed one with its output as text, the one without a result running
    const cards = Array.from(turns[1]!.querySelectorAll<HTMLElement>('[data-testid=chat-tool]'));
    expect(cards.map((c) => c.dataset.state)).toEqual(['failed', 'running']);
    expect(cards[0]!.textContent).toContain('$ go test ./...');
    expect(cards[0]!.querySelector('[data-testid=chat-tool-result]')!.textContent).toContain('<img src=x onerror=alert(1)> FAIL');
    expect(r.container.querySelector('img')).toBe(null);
    expect(cards[1]!.textContent).toContain('Drydock · search_memory');
    // the waits for a person, answered and open
    const waits = Array.from(turns[1]!.querySelectorAll<HTMLElement>('[data-testid=activity-wait]'));
    expect(waits.map((w) => [w.dataset.outcome ?? '', w.dataset.open ?? '', w.textContent])).toEqual([
      ['rejected', '', 'Refused by you:Bash: npm install left-pad'],
      ['', 'true', 'Waiting for you:Bash: rm -rf build· answer it under Needs your decision'],
    ]);
  });

  it('says when no agent has worked on the task, and when only the newest activity is shown', async () => {
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => ({ threads: [] }) }));
    await waitFor(() => expect(r.container.textContent).toContain('No agent has worked on this task yet.'), r);
    const r2 = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => ({ ...activity, truncated: true }) }));
    await waitFor(() => expect(r2.container.textContent).toContain('Only the newest activity is shown.'), r2);
  });
});
