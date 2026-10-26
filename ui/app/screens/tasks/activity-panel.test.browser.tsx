import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { AguiActivity, DrydockMeta, Metadata } from '../../api/agui.gen.ts';
import type { Connection } from '../../api/types.ts';
import { cleanup, mount, waitFor } from '../../test/render.tsx';
import { TaskActivity } from './activity-panel.tsx';
import { AGENTS, recorder, svc } from './testing.ts';

afterEach(cleanup);

// TEST DOUBLE data: a fixture task.activity in the core's AG-UI vocabulary, not an agent
const T0 = Date.UTC(2026, 9, 22, 9, 2, 0);
const at = (s: number) => T0 + s * 1000;
// what Drydock adds to an event, under metadata.drydock: the stored event's seq and, for some, its own fields
const md = (seq: number, drydock: Partial<DrydockMeta> = {}): Metadata => ({ drydock: { seq, ...drydock } });

const activity: AguiActivity = {
  threads: [
    {
      threadId: 'run_r',
      agentId: 'a2',
      adapter: 'claude',
      model: 'opus',
      events: [
        // a turn interrupted while it waited for a person, then one that failed
        { type: 'RUN_STARTED', timestamp: at(1), metadata: md(1), threadId: 'run_r', runId: 'run_r:1', protocolVersion: '1.0' },
        { type: 'CUSTOM', timestamp: at(2), metadata: md(2), name: 'drydock.interrupt', value: { id: 'apr_3', title: 'Bash: git push' } },
        { type: 'RUN_FINISHED', timestamp: at(3), metadata: md(3, { stopReason: 'interrupted' }), threadId: 'run_r', runId: 'run_r:1', outcome: { type: 'cancelled' } },
        { type: 'RUN_STARTED', timestamp: at(4), metadata: md(4), threadId: 'run_r', runId: 'run_r:2', protocolVersion: '1.0' },
        { type: 'RUN_ERROR', timestamp: at(5), metadata: md(5, { threadId: 'run_r', runId: 'run_r:2' }), message: 'rate limited' },
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
        { type: 'RUN_STARTED', timestamp: at(10), metadata: md(10), threadId: 'run_w', runId: 'run_w:1', protocolVersion: '1.0' },
        { type: 'TEXT_MESSAGE_START', timestamp: at(11), metadata: md(11), messageId: 'm1', role: 'assistant' },
        { type: 'TEXT_MESSAGE_CONTENT', timestamp: at(11), metadata: md(11), messageId: 'm1', delta: 'First turn.' },
        { type: 'TEXT_MESSAGE_END', timestamp: at(12), metadata: md(12), messageId: 'm1' },
        { type: 'RUN_FINISHED', timestamp: at(13), metadata: md(13), threadId: 'run_w', runId: 'run_w:1', outcome: { type: 'success' } },
        { type: 'RUN_STARTED', timestamp: at(20), metadata: md(14), threadId: 'run_w', runId: 'run_w:2', protocolVersion: '1.0' },
        {
          type: 'ACTIVITY_SNAPSHOT',
          timestamp: at(20),
          metadata: md(15),
          messageId: 'run_w:plan',
          activityType: 'PLAN',
          replace: true,
          content: { entries: [{ content: 'Read parse.go', status: 'completed' }, { content: 'Fix the exponent', status: 'in_progress' }, { content: 'Run the tests' }] },
        },
        { type: 'TOOL_CALL_START', timestamp: at(21), metadata: md(16), toolCallId: 'c1', toolCallName: 'Read' },
        { type: 'TOOL_CALL_ARGS', timestamp: at(21), metadata: md(16), toolCallId: 'c1', delta: '{"file_path":"calc/parse.go"}' },
        { type: 'TOOL_CALL_RESULT', timestamp: at(22), metadata: md(17), messageId: 'r1', toolCallId: 'c1', role: 'tool', content: 'package calc' },
        { type: 'TOOL_CALL_START', timestamp: at(22), metadata: md(18), toolCallId: 'c2', toolCallName: 'Read' },
        { type: 'TOOL_CALL_ARGS', timestamp: at(22), metadata: md(18), toolCallId: 'c2', delta: '{"file_path":"calc/parse_test.go"}' },
        { type: 'TOOL_CALL_RESULT', timestamp: at(23), metadata: md(19), messageId: 'r2', toolCallId: 'c2', role: 'tool', content: 'package calc' },
        { type: 'TOOL_CALL_START', timestamp: at(30), metadata: md(20), toolCallId: 'c3', toolCallName: 'Edit' },
        {
          type: 'TOOL_CALL_ARGS',
          timestamp: at(30),
          metadata: md(20),
          toolCallId: 'c3',
          delta: JSON.stringify({ file_path: 'calc/parse.go', old_string: 'exp, n := digits(s[i:])', new_string: 'sign := 1\nif s[i] == \'-\' {\n  sign = -1\n  i++\n}\nexp, n := digits(s[i:])\nexp *= sign' }),
        },
        { type: 'TOOL_CALL_RESULT', timestamp: at(31), metadata: md(21), messageId: 'r3', toolCallId: 'c3', role: 'tool', content: 'ok' },
        { type: 'TOOL_CALL_START', timestamp: at(40), metadata: md(22), toolCallId: 'c4', toolCallName: 'Bash' },
        { type: 'TOOL_CALL_ARGS', timestamp: at(40), metadata: md(22), toolCallId: 'c4', delta: '{"command":"go test ./..."}' },
        {
          type: 'TOOL_CALL_RESULT',
          timestamp: at(44),
          metadata: md(23, { isError: true }),
          messageId: 'r4',
          toolCallId: 'c4',
          role: 'tool',
          content: '--- FAIL: TestParse (0.00s)\n    --- FAIL: TestParse/-4e-2 (0.00s)\n        parse_test.go:31: got 0.04, want -0.04\n<img src=x onerror=alert(1)>\nFAIL',
        },
        { type: 'REASONING_MESSAGE_START', timestamp: at(45), metadata: md(24), messageId: 'th1', role: 'reasoning' },
        { type: 'REASONING_MESSAGE_CONTENT', timestamp: at(46), metadata: md(24), messageId: 'th1', delta: 'The leading minus is consumed twice.' },
        { type: 'REASONING_MESSAGE_END', timestamp: at(51), metadata: md(25), messageId: 'th1' },
        { type: 'CUSTOM', timestamp: at(52), metadata: md(26), name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: npm install left-pad' } },
        { type: 'CUSTOM', timestamp: at(53), metadata: md(27), name: 'drydock.resume', value: { id: 'apr_1', outcome: 'rejected' } },
        { type: 'CUSTOM', timestamp: at(54), metadata: md(28), name: 'drydock.interrupt', value: { id: 'apr_2', title: 'Bash: rm -rf build' } },
        { type: 'TOOL_CALL_START', timestamp: Date.now() - 14_000, metadata: md(29), toolCallId: 'c5', toolCallName: 'Bash' },
        { type: 'TOOL_CALL_ARGS', timestamp: Date.now() - 14_000, metadata: md(29), toolCallId: 'c5', delta: '{"command":"go test ./..."}' },
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
    const events: AguiActivity['threads'][number]['events'] = [{ type: 'RUN_STARTED', timestamp: at(1), metadata: md(1), threadId: 'big', runId: 'big:1', protocolVersion: '1.0' }];
    for (let i = 0; i < 600; i++) {
      events.push({ type: 'TOOL_CALL_START', timestamp: at(2 + i), metadata: md(2 + i * 3), toolCallId: `b${i}`, toolCallName: 'Bash' });
      events.push({ type: 'TOOL_CALL_ARGS', timestamp: at(2 + i), metadata: md(3 + i * 3), toolCallId: `b${i}`, delta: JSON.stringify({ command: `echo step ${i}` }) });
      events.push({ type: 'TOOL_CALL_RESULT', timestamp: at(3 + i), metadata: md(4 + i * 3), messageId: `m${i}`, toolCallId: `b${i}`, role: 'tool', content: `step ${i}` });
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

  it('draws a decision nobody was asked for like an answered wait, with the words of who decided and why', async () => {
    const decided: AguiActivity = {
      threads: [
        {
          threadId: 'run_d',
          agentId: 'a1',
          adapter: 'claude',
          events: [
            { type: 'RUN_STARTED', timestamp: at(1), metadata: md(1), threadId: 'run_d', runId: 'run_d:1', protocolVersion: '1.0' },
            // a rule of Drydock's refused the call; a task rule the person made allowed one; the agent refused one itself
            { type: 'CUSTOM', timestamp: at(2), metadata: md(2), name: 'drydock.decided', value: { tool: 'Bash', outcome: 'denied', rule: 'no git push' } },
            { type: 'CUSTOM', timestamp: at(3), metadata: md(3), name: 'drydock.decided', value: { tool: 'Bash', outcome: 'approved_for_task', rule: 'go test' } },
            { type: 'TOOL_CALL_START', timestamp: at(4), metadata: md(4), toolCallId: 'p1', toolCallName: 'Write' },
            { type: 'TOOL_CALL_ARGS', timestamp: at(4), metadata: md(4), toolCallId: 'p1', delta: '{"file_path":"config.json","content":"{}"}' },
            {
              type: 'CUSTOM',
              timestamp: at(5),
              metadata: md(5),
              name: 'drydock.decided',
              value: { tool: 'Write', outcome: 'refused', reason: 'nobody could be asked in this session, so Drydock refused the request', toolCallId: 'p1' },
            },
            { type: 'TOOL_CALL_RESULT', timestamp: at(6), metadata: md(6, { isError: true }), messageId: 'r1', toolCallId: 'p1', role: 'tool', content: 'Permission denied' },
            // and a wait a person answered, for comparison
            { type: 'CUSTOM', timestamp: at(7), metadata: md(7), name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: npm install' } },
            { type: 'CUSTOM', timestamp: at(8), metadata: md(8), name: 'drydock.resume', value: { id: 'apr_1', outcome: 'approved_for_task' } },
          ],
        },
      ],
    };
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => decided }));
    await waitFor(() => expect($$(r.container, '[data-testid=activity-wait]').length).toBe(4), r);
    const waits = $$(r.container, '[data-testid=activity-wait]');
    expect(waits.map((w) => [w.dataset.outcome ?? '', w.dataset.asked ?? '', w.dataset.open ?? '', w.textContent])).toEqual([
      ['denied', 'false', '', "Refused by Drydock's rules:Bash(rule: no git push)"],
      ['approved_for_task', 'false', '', 'Allowed by you for this task:Bash(rule: go test)'],
      ['refused', 'false', '', 'Refused without asking:Write: config.json(nobody could be asked in this session, so Drydock refused the request)'],
      ['approved_for_task', '', '', 'Allowed by you for this task:Bash: npm install'],
    ]);
    // the decisions are over, never open, and the refused call keeps its own card
    expect($$(r.container, '[data-testid=activity-step]').map((s) => [s.dataset.kind, s.dataset.state])).toEqual([
      ['wait', 'ok'],
      ['wait', 'ok'],
      ['edit', 'failed'],
      ['wait', 'ok'],
      ['wait', 'ok'],
    ]);
    expect(r.container.textContent).not.toContain('Needs your decision');
  });

  it('reads again when a permission request comes and when it is answered, not only on the agent events', async () => {
    const running: AguiActivity['threads'][number]['events'] = [
      { type: 'RUN_STARTED', timestamp: at(1), metadata: md(1), threadId: 'run_p', runId: 'run_p:1', protocolVersion: '1.0' },
      { type: 'TOOL_CALL_START', timestamp: at(2), metadata: md(2), toolCallId: 'c1', toolCallName: 'Bash' },
      { type: 'TOOL_CALL_ARGS', timestamp: at(2), metadata: md(2), toolCallId: 'c1', delta: '{"command":"npm install"}' },
    ];
    const asked: typeof running = [...running, { type: 'CUSTOM', timestamp: at(3), metadata: md(3), name: 'drydock.interrupt', value: { id: 'apr_1', title: 'Bash: npm install' } }];
    const answered: typeof running = [...asked, { type: 'CUSTOM', timestamp: at(4), metadata: md(4), name: 'drydock.resume', value: { id: 'apr_1', outcome: 'approved' } }];
    const thread = (events: typeof running): AguiActivity => ({ threads: [{ threadId: 'run_p', agentId: 'a1', adapter: 'claude', events }] });
    // the core's answer to each read (a labelled test double for the read): first the running command alone
    let answer = thread(running);
    const read = recorder(async (_id: string) => answer);
    const s = svc({ taskActivity: read });
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, s);
    await waitFor(() => expect(ofThread(r.container, 'run_p', '[data-testid=activity-step][data-kind=run][data-state=running]').length).toBe(1), r);
    expect(read.calls.length).toBe(1);
    expect(ofThread(r.container, 'run_p', '[data-testid=activity-wait]').length).toBe(0);
    // the request is the task's event, not the agent's: the feed reads again and shows the wait open
    answer = thread(asked);
    s.emit({ type: 'approval.requested', taskId: 't1', runId: 'run_p', agentId: 'a1', data: { request: 'apr_1', title: 'Permission request: Bash: npm install' } });
    await waitFor(() => expect(read.calls.length).toBe(2), r, 1100);
    await waitFor(() => expect(ofThread(r.container, 'run_p', '[data-testid=activity-wait]')[0]!.dataset.open).toBe('true'), r);
    // the answer too: the row stops saying "Waiting for you" while the allowed command runs
    answer = thread(answered);
    s.emit({ type: 'permission.decided', taskId: 't1', runId: 'run_p', agentId: 'a1', data: { approval: 'apr_1', outcome: 'approved' } });
    await waitFor(() => expect(read.calls.length).toBe(3), r, 1100);
    await waitFor(() => expect(ofThread(r.container, 'run_p', '[data-testid=activity-wait]')[0]!.dataset.outcome).toBe('approved'), r);
    expect(ofThread(r.container, 'run_p', '[data-testid=activity-wait]')[0]!.textContent).toBe('Allowed once by you:Bash: npm install');
    expect(read.calls.every((c) => c[0] === 't1')).toBe(true);
  });

  it('a process without a turn yet is starting when it is the newest and left out when it is not; a running read counts from the read that runs', async () => {
    const rows: AguiActivity = {
      threads: [
        { threadId: 'run_0', agentId: 'a2', adapter: 'claude', events: [] },
        {
          threadId: 'run_1',
          agentId: 'a1',
          adapter: 'claude',
          events: [
            { type: 'RUN_STARTED', timestamp: at(1), metadata: md(1), threadId: 'run_1', runId: 'run_1:1', protocolVersion: '1.0' },
            { type: 'TOOL_CALL_START', timestamp: at(2), metadata: md(2), toolCallId: 'c1', toolCallName: 'Read' },
            { type: 'TOOL_CALL_ARGS', timestamp: at(2), metadata: md(2), toolCallId: 'c1', delta: '{"file_path":"calc/parse.go"}' },
            { type: 'TOOL_CALL_RESULT', timestamp: at(3), metadata: md(3), messageId: 'r1', toolCallId: 'c1', role: 'tool', content: 'package calc' },
            { type: 'TOOL_CALL_START', timestamp: Date.now() - 14_000, metadata: md(4), toolCallId: 'c2', toolCallName: 'Read' },
            { type: 'TOOL_CALL_ARGS', timestamp: Date.now() - 14_000, metadata: md(4), toolCallId: 'c2', delta: '{"file_path":"calc/parse_test.go"}' },
          ],
        },
        { threadId: 'run_2', agentId: 'a2', adapter: 'claude', events: [] },
      ],
    };
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => rows }));
    await waitFor(() => expect($$(r.container, '[data-testid=activity-thread]').length).toBe(2), r);
    const threads = $$(r.container, '[data-testid=activity-thread]');
    expect(threads.map((t) => [t.dataset.thread, t.querySelector('.meta')!.textContent])).toEqual([
      ['run_1', '1 turn · working now'],
      ['run_2', 'starting'],
    ]);
    expect(r.container.textContent).not.toContain('0 turns');
    // Reading parse.go parse_test.go: the group's seconds are the second read's, which began 14 s ago, not the first's
    const read = ofThread(r.container, 'run_1', '[data-testid=activity-step][data-kind=read]')[0]!;
    expect(read.dataset.state).toBe('running');
    expect(Array.from(read.querySelectorAll('summary .what')).map((e) => e.textContent)).toEqual(['parse.go', 'parse_test.go']);
    expect(read.querySelector('summary .quiet')!.textContent).toMatch(/^1[45] s$/);
    expect(read.querySelector('summary .at')!.textContent).toBe('now');
  });

  it('a process stopped before its first turn says so, not that it is starting', async () => {
    const rows: AguiActivity = {
      threads: [
        {
          threadId: 'run_1',
          agentId: 'a1',
          adapter: 'claude',
          ended: true,
          events: [
            { type: 'RUN_STARTED', timestamp: at(1), metadata: md(1), threadId: 'run_1', runId: 'run_1:1', protocolVersion: '1.0' },
            { type: 'RUN_FINISHED', timestamp: at(2), metadata: md(2), threadId: 'run_1', runId: 'run_1:1', outcome: { type: 'success' } },
          ],
        },
        // killed by Cancel or Stop right after it spawned: the core publishes agent.started and agent.stopped, no turn
        { threadId: 'run_2', agentId: 'a2', adapter: 'claude', ended: true, events: [] },
      ],
    };
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => rows }));
    await waitFor(() => expect($$(r.container, '[data-testid=activity-thread]').length).toBe(2), r);
    expect($$(r.container, '[data-testid=activity-thread]').map((t) => [t.dataset.thread, t.querySelector('.meta')!.textContent])).toEqual([
      ['run_1', '1 turn'],
      ['run_2', 'stopped before its first turn'],
    ]);
    expect(r.container.textContent).not.toContain('starting');
  });

  it('shows the core error when the activity cannot be read', async () => {
    const r = mount(<TaskActivity taskId="t1" agents={AGENTS} />, svc({ taskActivity: async () => Promise.reject(new Error('activity unavailable')) }));
    await waitFor(() => expect(r.$('[role=alert]')!.textContent).toContain('activity unavailable'), r);
  });
});
