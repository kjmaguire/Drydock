import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { AguiActivity } from '../../api/agui.gen.ts';
import type { Connection } from '../../api/types.ts';
import { cleanup, mount, waitFor } from '../../test/render.tsx';
import { TaskActivity } from './activity-panel.tsx';
import { AGENTS, recorder, svc } from './testing.ts';

afterEach(cleanup);

// TEST DOUBLE data: a fixture task.activity in the core's AG-UI vocabulary, not an agent
const T0 = Date.UTC(2026, 9, 22, 9, 2, 0);
const at = (s: number) => T0 + s * 1000;

const activity: AguiActivity = {
  threads: [
    {
      threadId: 'run_r',
      agentId: 'a2',
      adapter: 'claude',
      model: 'opus',
      events: [
        // a turn interrupted while it waited for a person, then one that failed
        { type: 'RUN_STARTED', seq: 1, timestamp: at(1), threadId: 'run_r', runId: 'run_r:1' },
        { type: 'CUSTOM', seq: 2, timestamp: at(2), name: 'drydock.interrupt', value: { id: 'apr_3', title: 'Bash: git push' } },
        { type: 'RUN_FINISHED', seq: 3, timestamp: at(3), threadId: 'run_r', runId: 'run_r:1', outcome: { type: 'cancelled' }, stopReason: 'interrupted' },
        { type: 'RUN_STARTED', seq: 4, timestamp: at(4), threadId: 'run_r', runId: 'run_r:2' },
        { type: 'RUN_ERROR', seq: 5, timestamp: at(5), threadId: 'run_r', runId: 'run_r:2', message: 'rate limited' },
      ],
    },
    {
      threadId: 'run_w',
      agentId: 'a1',
      adapter: 'claude',
      model: 'sonnet',
      runMode: 'wsl',
      wslDistro: 'Ubuntu',
      events: [
        { type: 'RUN_STARTED', seq: 10, timestamp: at(10), threadId: 'run_w', runId: 'run_w:1' },
        { type: 'TEXT_MESSAGE_START', seq: 11, timestamp: at(11), messageId: 'm1', role: 'assistant' },
        { type: 'TEXT_MESSAGE_CONTENT', seq: 11, timestamp: at(11), messageId: 'm1', delta: 'First turn.' },
        { type: 'TEXT_MESSAGE_END', seq: 12, timestamp: at(12), messageId: 'm1' },
        { type: 'RUN_FINISHED', seq: 13, timestamp: at(13), threadId: 'run_w', runId: 'run_w:1', outcome: { type: 'success' } },
        { type: 'RUN_STARTED', seq: 14, timestamp: at(20), threadId: 'run_w', runId: 'run_w:2' },
        {
          type: 'ACTIVITY_SNAPSHOT',
          seq: 15,
          timestamp: at(20),
          messageId: 'run_w:plan',
          activityType: 'PLAN',
          replace: true,
          content: { entries: [{ content: 'Read parse.go', status: 'completed' }, { content: 'Fix the exponent', status: 'in_progress' }, { content: 'Run the tests' }] },
        },
        { type: 'TOOL_CALL_START', seq: 16, timestamp: at(21), toolCallId: 'c1', toolCallName: 'Read' },
        { type: 'TOOL_CALL_ARGS', seq: 16, timestamp: at(21), toolCallId: 'c1', delta: '{"file_path":"calc/parse.go"}' },
        { type: 'TOOL_CALL_RESULT', seq: 17, timestamp: at(22), messageId: 'r1', toolCallId: 'c1', role: 'tool', content: 'package calc' },
        { type: 'TOOL_CALL_START', seq: 18, timestamp: at(22), toolCallId: 'c2', toolCallName: 'Read' },
        { type: 'TOOL_CALL_ARGS', seq: 18, timestamp: at(22), toolCallId: 'c2', delta: '{"file_path":"calc/parse_test.go"}' },
        { type: 'TOOL_CALL_RESULT', seq: 19, timestamp: at(23), messageId: 'r2', toolCallId: 'c2', role: 'tool', content: 'package calc' },
        { type: 'TOOL_CALL_START', seq: 20, timestamp: at(30), toolCallId: 'c3', toolCallName: 'Edit' },
        {
          type: 'TOOL_CALL_ARGS',
          seq: 20,
          timestamp: at(30),
          toolCallId: 'c3',
          delta: JSON.stringify({ file_path: 'calc/parse.go', old_string: 'exp, n := digits(s[i:])', new_string: 'sign := 1\nif s[i] == \'-\' {\n  sign = -1\n  i++\n}\nexp, n := digits(s[i:])\nexp *= sign' }),
        },
        { type: 'TOOL_CALL_RESULT', seq: 21, timestamp: at(31), messageId: 'r3', toolCallId: 'c3', role: 'tool', content: 'ok' },
        { type: 'TOOL_CALL_START', seq: 22, timestamp: at(40), toolCallId: 'c4', toolCallName: 'Bash' },
        { type: 'TOOL_CALL_ARGS', seq: 22, timestamp: at(40), toolCallId: 'c4', delta: '{"command":"go test ./..."}' },
        {
          type: 'TOOL_CALL_RESULT',
          seq: 23,
          timestamp: at(44),
          messageId: 'r4',
          toolCallId: 'c4',
          role: 'tool',
          content: '--- FAIL: TestParse (0.00s)\n    --- FAIL: TestParse/-4e-2 (0.00s)\n        parse_test.go:31: got 0.04, want -0.04\n<img src=x onerror=alert(1)>\nFAIL',
          isError: true,
        },
        { type: 'REASONING_MESSAGE_START', seq: 24, timestamp: at(45), messageId: 'th1', role: 'reasoning' },
        { type: 'REASONING_MESSAGE_CONTENT', seq: 24, timestamp: at(46), messageId: 'th1', delta: 'The leading minus is consumed twice.' },
        { type: 'REASONING_MESSAGE_END', seq: 25, timestamp: at(51), messageId: 'th1' },
        { type: 'CUSTOM', seq: 26, timestamp: at(52), name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: npm install left-pad' } },
        { type: 'CUSTOM', seq: 27, timestamp: at(53), name: 'drydock.resume', value: { id: 'apr_1', outcome: 'rejected' } },
        { type: 'CUSTOM', seq: 28, timestamp: at(54), name: 'drydock.interrupt', value: { id: 'apr_2', title: 'Bash: rm -rf build' } },
        { type: 'TOOL_CALL_START', seq: 29, timestamp: Date.now() - 14_000, toolCallId: 'c5', toolCallName: 'Bash' },
        { type: 'TOOL_CALL_ARGS', seq: 29, timestamp: Date.now() - 14_000, toolCallId: 'c5', delta: '{"command":"go test ./..."}' },
      ],
    },
  ],
};

const conns: Connection[] = [];

const $$ = (root: ParentNode, sel: string) => Array.from(root.querySelectorAll<HTMLElement>(sel));
const ofThread = (root: ParentNode, id: string, sel: string) => $$(root, `[data-thread="${id}"] ${sel}, [data-thread="${id}"]${sel}`);

describe('the task page Activity as compact steps (P87, P97)', () => {
  it('heads the feed with the newest agent, its CLI and model, and lists each process in the order it started', async () => {
    const read = recorder(async (_id: string) => activity);
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: read, connectionList: async () => conns }));
    await waitFor(() => expect($$(r.container, '[data-testid=activity-thread]').length).toBe(2), r);
    expect(read.calls[0]).toEqual(['t1']);
    expect(r.$('h2')!.textContent).toBe('Activity');
    expect(r.$('[data-testid=activity-agent]')!.textContent).toBe('Dana · Claude Code WSL · sonnet');
    const threads = $$(r.container, '[data-testid=activity-thread]');
    expect(threads.map((t) => t.dataset.agent)).toEqual(['a2', 'a1']);
    // an earlier process names its CLI and model on its own line; the newest is in the head
    expect(threads[0]!.textContent).toContain('Rex · Claude Code · opus');
    expect(threads[0]!.textContent).toContain('2 turns');
    expect(threads[1]!.textContent).toContain('Dana');
    expect(threads[1]!.textContent).toContain('working now');
    expect(r.$('[role=log]')!.getAttribute('aria-label')).toBe('What the agents did');
  });

  it("draws the newest turn's steps: reads joined, the edit with its counts and hunk, the failed run with its failing line, the thought, the running step", async () => {
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => activity }));
    await waitFor(() => expect(ofThread(r.container, 'run_w', '[data-testid=activity-step]').length).toBe(7), r);
    const steps = ofThread(r.container, 'run_w', '[data-testid=activity-step]');
    expect(steps.map((s) => [s.dataset.kind, s.dataset.state])).toEqual([
      ['read', 'ok'],
      ['edit', 'ok'],
      ['run', 'failed'],
      ['thought', 'ok'],
      ['wait', 'ok'],
      ['wait', 'running'],
      ['run', 'running'],
    ]);
    const [read, edit, ran, thought, , , running] = steps as HTMLElement[];
    // Read parse.go parse_test.go, the full paths in the titles
    expect(Array.from(read!.querySelectorAll('summary .what')).map((e) => [e.textContent, e.getAttribute('title')])).toEqual([
      ['parse.go', 'calc/parse.go'],
      ['parse_test.go', 'calc/parse_test.go'],
    ]);
    expect(read!.querySelector('summary .verb')!.textContent).toBe('Read');
    expect(read!.querySelector('time')!.textContent).toBe(new Date(at(21)).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }));
    // Edited parse.go +7 −1 and the first six lines of the change
    expect(edit!.querySelector('summary')!.textContent).toContain('Editedparse.go+7−1');
    const hunk = edit!.querySelector('[data-testid=activity-hunk]')!;
    expect(Array.from(hunk.querySelectorAll('div[data-kind]')).map((d) => d.textContent)).toEqual(['− exp, n := digits(s[i:])', '+ sign := 1', "+ if s[i] == '-' {", '+   sign = -1', '+   i++', '+ }']);
    expect(hunk.querySelector('.rest')!.textContent).toBe('2 more lines');
    // Ran go test ./... · 1 failed, and the line that says why; the output is text, never markup
    expect(ran!.querySelector('summary .what')!.textContent).toBe('go test ./...');
    expect(ran!.querySelector('summary .bad')!.textContent).toBe('1 failed');
    expect(ran!.querySelector('[data-testid=activity-failing]')!.textContent).toBe('TestParse/-4e-2: got 0.04, want -0.04');
    expect(ran!.querySelector('[data-testid=activity-result]')!.textContent).toContain('<img src=x onerror=alert(1)>');
    expect(r.container.querySelector('img')).toBe(null);
    // the call opens to its whole input and result
    const details = ran!.querySelector<HTMLDetailsElement>('details.call')!;
    expect(details.open).toBe(false);
    await r.act(() => details.querySelector('summary')!.click());
    expect(details.open).toBe(true);
    expect(details.querySelector('pre')!.textContent).toBe('$ go test ./...');
    // Thought for 6 s, folded away with Show
    const th = thought!.querySelector<HTMLDetailsElement>('[data-testid=activity-reasoning]')!;
    expect(th.querySelector('summary')!.textContent).toContain('Thought for 6 s');
    expect(th.querySelector('summary .show')!.textContent).toBe('Show');
    expect(th.open).toBe(false);
    expect(th.textContent).toContain('The leading minus is consumed twice.');
    // the running step: a spinner, its seconds, now
    expect(running!.querySelector('.tile [role=status]')).toBeTruthy();
    expect(running!.querySelector('summary .verb')!.textContent).toBe('Running');
    expect(running!.querySelector('summary .quiet')!.textContent).toMatch(/^1[45] s$/);
    expect(running!.querySelector('summary .at')!.textContent).toBe('now');
    // the waits for a person, answered and open
    const waits = ofThread(r.container, 'run_w', '[data-testid=activity-wait]');
    expect(waits.map((w) => [w.dataset.outcome ?? '', w.dataset.open ?? '', w.textContent])).toEqual([
      ['rejected', '', 'Refused by you:Bash: npm install left-pad'],
      ['', 'true', 'Waiting for you:Bash: rm -rf build· answer it under Needs your decision'],
    ]);
    // the plan as a checklist with its progress
    const plan = r.$('[data-testid=activity-plan]')!;
    expect(plan.getAttribute('aria-label')).toBe('Plan, 1 of 3 done');
    expect($$(plan, 'li[data-status]').map((li) => li.getAttribute('data-status'))).toEqual(['completed', 'in_progress', 'pending']);
  });

  it('the running step counts its seconds live', async () => {
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => activity }));
    const q = () => ofThread(r.container, 'run_w', '[data-testid=activity-step][data-state=running][data-kind=run] summary .quiet')[0];
    const first = await waitFor(() => {
      const el = q();
      if (!el) throw new Error('not yet');
      return Number.parseInt(el.textContent!, 10);
    }, r);
    await waitFor(() => expect(Number.parseInt(q()!.textContent!, 10)).toBeGreaterThan(first), r, 3000);
  });

  it('earlier turns are one line each: opened, their steps and how they ended show; cancelled and failed turns say so', async () => {
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => activity }));
    await waitFor(() => expect($$(r.container, '[data-testid=activity-turn]').length).toBe(4), r);
    const turns = (id: string) => ofThread(r.container, id, '[data-testid=activity-turn]');
    expect(turns('run_r').map((b) => [b.dataset.status, b.getAttribute('aria-expanded'), b.textContent!.replace(/\d+:\d\d.*$/, '')])).toEqual([
      ['cancelled', 'false', 'Turn 1 · 0 tool calls · stopped'],
      ['error', 'true', 'Turn 2 · 0 tool calls · error'],
    ]);
    expect(turns('run_w').map((b) => [b.dataset.status, b.getAttribute('aria-expanded')])).toEqual([
      ['done', 'false'],
      ['running', 'true'],
    ]);
    // the newest failed turn says why
    expect(ofThread(r.container, 'run_r', '[data-testid=activity-error]')[0]!.textContent).toBe('The turn failed: rate limited');
    expect(r.container.textContent).not.toContain('First turn.');
    // opening the first turns shows what was in them
    await r.act(() => turns('run_r')[0]!.click());
    await r.act(() => turns('run_w')[0]!.click());
    await waitFor(() => expect(r.container.textContent).toContain('First turn.'), r);
    expect(ofThread(r.container, 'run_w', '[data-testid=activity-text]')[0]!.textContent).toBe('First turn.');
    expect(ofThread(r.container, 'run_r', '[data-testid=activity-cancelled]')[0]!.textContent).toBe('The turn was interrupted before it finished.');
    const over = ofThread(r.container, 'run_r', '[data-testid=activity-wait]')[0]!;
    expect([over.dataset.open ?? '', over.textContent]).toEqual(['', 'Not answered before the turn ended:Bash: git push']);
    expect(turns('run_r')[0]!.getAttribute('aria-expanded')).toBe('true');
    // and closing the newest one folds it
    await r.act(() => turns('run_w')[1]!.click());
    await waitFor(() => expect(ofThread(r.container, 'run_w', '[data-testid=activity-step][data-kind=edit]').length).toBe(0), r);
  });

  it('keeps a long feed windowed: only the rows near the view are in the page, at the bottom while live', async () => {
    const events: AguiActivity['threads'][number]['events'] = [{ type: 'RUN_STARTED', seq: 1, timestamp: at(1), threadId: 'big', runId: 'big:1' }];
    for (let i = 0; i < 600; i++) {
      events.push({ type: 'TOOL_CALL_START', seq: 2 + i * 3, timestamp: at(2 + i), toolCallId: `b${i}`, toolCallName: 'Bash' });
      events.push({ type: 'TOOL_CALL_ARGS', seq: 3 + i * 3, timestamp: at(2 + i), toolCallId: `b${i}`, delta: JSON.stringify({ command: `echo step ${i}` }) });
      events.push({ type: 'TOOL_CALL_RESULT', seq: 4 + i * 3, timestamp: at(3 + i), messageId: `m${i}`, toolCallId: `b${i}`, role: 'tool', content: `step ${i}` });
    }
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => ({ threads: [{ threadId: 'big', agentId: 'a1', events }] }) }));
    const feed = await waitFor(() => {
      const f = r.$('[data-testid=activity-feed]') as HTMLElement | null;
      if (!f) throw new Error('not yet');
      return f;
    }, r);
    await waitFor(() => expect($$(r.container, '[data-testid=activity-step]').length).toBeGreaterThan(0), r);
    await waitFor(() => expect(r.container.textContent).toContain('echo step 599'), r);
    expect($$(r.container, '[data-testid=activity-step]').length).toBeLessThan(200);
    expect(r.container.textContent).not.toContain('echo step 0 ');
    expect(getComputedStyle(feed).overflowY).toBe('auto');
  });

  it('says when no agent has worked on the task, and when only the newest activity is shown', async () => {
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => ({ threads: [] }) }));
    await waitFor(() => expect(r.container.textContent).toContain('No agent has worked on this task yet.'), r);
    expect(r.$('[data-testid=activity-agent]')).toBeNull();
    const r2 = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => ({ ...activity, truncated: true }) }));
    await waitFor(() => expect(r2.container.textContent).toContain('Only the newest activity is shown.'), r2);
  });

  it('shows the core error when the activity cannot be read', async () => {
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => Promise.reject(new Error('activity unavailable')) }));
    await waitFor(() => expect(r.$('[role=alert]')!.textContent).toContain('activity unavailable'), r);
  });
});
