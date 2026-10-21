import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { WorkflowTemplate } from '../../api/extTypes.ts';
import type { Evidence } from '../../api/types.ts';
import { expiredLicense, usage } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, optionsOf, renderPlain, type, valueOf, waitFor } from '../../test/render.tsx';
import { EvidenceList } from './evidence.tsx';
import { UnifiedDiff } from './raw-diff.tsx';
import { TaskDetailScreen } from './task-detail-screen.tsx';
import { detail, recorder, svc, task } from './testing.ts';

afterEach(cleanup);

const templates: WorkflowTemplate[] = [
  { name: 'iterate-until-verified', title: 'Iterate Until Verified', description: 'Work, review, approve.', workflow: 'iterate-until-verified', specGate: false, testFirst: false, steps: ['work'], roles: [] },
  {
    name: 'feature',
    title: 'Feature',
    description: 'A PM writes a spec.',
    workflow: 'feature',
    specGate: true,
    testFirst: true,
    steps: ['spec (PM)', 'tests first'],
    roles: [{ role: 'pm', required: true, purpose: 'writes the spec' }],
  },
];

const buttonNames = (root: ParentNode) => Array.from(root.querySelectorAll('button')).map((b) => (b.getAttribute('aria-label') ?? b.textContent ?? '').trim());

function need<T>(v: T | null | undefined): T {
  if (!v) throw new Error('not yet');
  return v;
}

async function ready(r: ReturnType<typeof mount>) {
  await waitFor(() => expect(valueOf(byLabel(r.container, 'Worker'))).toBe('a1'), r);
}

describe('Task view: header, properties and content', () => {
  it('shows the number and title as the heading, the status, the properties and the description', async () => {
    const d = detail({
      task: task({ status: 'IN_PROGRESS', description: 'Make **Multiply** work.', acceptance: ['Given 3 and 4 When Multiply Then 12'], branch: 'drydock/t1', iteration: 2, costUsd: 0.5 }),
      roomId: 'room_9',
      run: { id: 'run-1', workflow: 'iterate-until-verified', status: 'RUNNING', topology: '', created: 0, updated: 0, version: 1 },
    });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d, usageList: async () => [usage({ source: 'estimated' })] }));
    await waitFor(() => expect(r.$('h1')!.textContent).toContain('#1 Add Multiply'), r);
    expect(r.$('[data-testid="task-status"]')!.textContent).toContain('In progress');
    const meta = r.$('.task-meta')!;
    await waitFor(() => expect(meta.textContent).toContain('Dana'), r);
    expect(meta.textContent).toContain('Rex');
    expect(meta.textContent).toContain('drydock/t1');
    expect(meta.textContent).toContain('2 of 5');
    expect(meta.querySelectorAll('[data-dot="true"]').length).toBe(2);
    expect(r.$('[data-testid="task-cost"]')!.textContent).toBe('$0.50');
    await waitFor(() => expect(r.$('[data-testid="task-cost"]')!.parentElement!.textContent).toContain('estimated'), r);
    expect(byText(r.container, 'a', 'Open task channel').getAttribute('href')).toBe('/room/room_9');
    expect(byText(r.container, 'a', 'Workflow and explanation').getAttribute('href')).toBe('/task/t1/run');
    expect(r.$('strong')).toBeTruthy();
    expect(r.container.textContent).toContain('Given 3 and 4 When Multiply Then 12');
    expect(r.$('.md strong')!.textContent).toBe('Multiply');
    // while a run is active, only cancel is offered (no assignment)
    expect(r.$('[role="combobox"][aria-label="Worker"]')).toBeNull();
    expect(buttonNames(r.container)).toContain('Cancel task');
    await waitFor(() => expect(r.$$('[data-testid="usage-row"]').length).toBe(1), r);
    // the step bar: the spec is done (one criterion), the code is being written on attempt 2
    const steps = Array.from(r.$$('[data-testid="task-steps"] li[data-step]'));
    expect(steps.map((x) => x.getAttribute('data-step'))).toEqual(['spec', 'code', 'checks', 'review', 'you']);
    expect(steps.map((x) => x.getAttribute('data-state'))).toEqual(['done', 'current', 'todo', 'todo', 'todo']);
    expect(steps[0]!.textContent).toContain('1 criteria');
    expect(steps[1]!.textContent).toContain('attempt 2');
    expect(steps[1]!.getAttribute('aria-current')).toBe('step');
    // the task's model and time are properties
    expect(meta.textContent).toContain('Model');
    expect(meta.textContent).toContain('Time');
  });

  // audit 2026-10-16, M27: the team lead's follow-up waits in the backlog for a person; the page says where it came from and why
  it('a task the team lead split off shows the badge under the title and the sentence that it waits for you; others show neither', async () => {
    const SENTENCE = 'It waits in the backlog until you move it on; the lead cannot start it by itself.';
    const open = async (over: Parameters<typeof task>[0]) => {
      const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task(over) }) }));
      await waitFor(() => expect(r.$('h1')!.textContent).toContain('#1 Add Multiply'), r);
      return r;
    };
    const split = await open({ status: 'BACKLOG', origin: 'lead:2' });
    const note = split.$('[data-testid="task-origin-note"]')!;
    expect(note.querySelector('[data-testid="task-origin"]')!.textContent).toBe('Split off by the team lead');
    expect(note.querySelector('[data-testid="task-origin"]')!.getAttribute('title')).toBe('Step 2 from a task you made');
    expect(note.textContent).toBe(`Split off by the team lead${SENTENCE}`);
    // the sentence is under the badge, in the header beside the title, and not part of the heading's name
    expect(split.$('h1')!.textContent).not.toContain('Split off');
    expect(note.firstElementChild!.getAttribute('data-testid')).toBe('task-origin');
    cleanup();
    // once a person has moved it on, it no longer waits: the badge stays (where it came from), the sentence goes
    const moved = await open({ status: 'IN_PROGRESS', origin: 'lead:1' });
    expect(moved.$('[data-testid="task-origin"]')!.getAttribute('title')).toBe('Step 1 from a task you made');
    expect(moved.container.textContent).not.toContain(SENTENCE);
    cleanup();
    // a task a person made, or an origin that is not "lead:N": nothing, and the page is as before
    for (const origin of [undefined, '', 7, { n: 2 }, ['lead:2'], 'lead:', 'lead:x', 'lead:0', 'user:1', 'LEAD:1']) {
      const r = await open({ status: 'BACKLOG', origin: origin as never });
      expect(r.$('[data-testid="task-origin"]')).toBeNull();
      expect(r.$('[data-testid="task-origin-note"]')).toBeNull();
      expect(r.container.textContent).not.toContain(SENTENCE);
      expect(r.$('.task-meta')).toBeTruthy();
      cleanup();
    }
  });

  it('a task waiting for you shows the decide card beside the work, with You lit on the step bar', async () => {
    const d = detail({
      task: task({ status: 'WAITING_HUMAN', iteration: 1, started: 1_700_000_000_000, model: 'model-x' }),
      evidence: [{ id: 1, taskId: 't1', attempt: 1, kind: 'test', passed: true, summary: 'ok', created: 0 }],
      reviews: [{ id: 1, taskId: 't1', attempt: 1, reviewer: 'a2', verdict: 'SHIP', feedback: 'Ship it.', created: 0 }],
      pending: [{ id: 'req-1', nodeId: 'approve', step: 3, spec: { kind: 'review', title: 'Review and merge: Add Multiply' } }],
      run: { id: 'run-1', workflow: 'iterate-until-verified', status: 'WAITING_HUMAN', topology: '', created: 0, updated: 0, version: 1 },
    });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d }));
    await waitFor(() => expect(r.$('[data-testid="review-panel"]')).toBeTruthy(), r);
    expect(r.$('[data-testid="task-status"]')!.textContent).toContain('Waiting for you');
    // the decide card is in the side column, above the properties
    const side = r.$('aside')!;
    const panel = side.querySelector('[data-testid="review-panel"]')!;
    const meta = side.querySelector('.task-meta')!;
    expect(panel.compareDocumentPosition(meta) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(buttonNames(side)).toContain('Approve');
    expect(buttonNames(side)).toContain('Request changes');
    const steps = Array.from(r.$$('[data-testid="task-steps"] li[data-step]'));
    expect(steps.map((x) => x.getAttribute('data-state'))).toEqual(['done', 'done', 'done', 'done', 'current']);
    expect(steps[2]!.textContent).toContain('1 of 1');
    expect(steps[3]!.textContent).toContain('ship it');
    expect(steps[4]!.textContent).toContain('approve');
    expect(meta.textContent).toContain('model-x');
    expect(meta.textContent).not.toContain('not started');
  });

  it('a cost with no usage rows is marked as of unknown source; no cost says not reported', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ costUsd: 0.2 }) }) }));
    await waitFor(() => expect(r.$('[data-testid="task-cost"]')!.parentElement!.textContent).toContain('source not exposed'), r);
    const r2 = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail() }));
    await waitFor(() => expect(r2.$('[data-testid="task-cost"]')!.textContent).toBe('not reported'), r2);
  });

  it('shows the core error when the task cannot be read', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => Promise.reject(new Error('task not found')) }));
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('task not found'), r);
  });

  it('the dock buttons open the dock on the right tab', async () => {
    const s = svc({ taskGet: async () => detail() });
    const r = mount(<TaskDetailScreen id="t1" />, s);
    await ready(r);
    await r.act(() => byText(r.container, 'button', 'Logs').click());
    expect(s.ui.dock).toEqual({ ...s.ui.dock, open: true, tab: 'logs' });
    await r.act(() => byText(r.container, 'button', 'Open in the dock').click());
    expect(s.ui.dock.tab).toBe('diff');
    await r.act(() => byText(r.container, 'button', 'Terminal').click());
    expect(s.ui.dock.tab).toBe('terminal');
  });

  it('shows the raw patch on request, as text lines classed by kind', async () => {
    const taskDiff = recorder((_id: string, _evidenceId?: number) => 'diff --git a/calc.go b/calc.go\n@@ -1 +1 @@\n-return 0\n+return a - b\n');
    const d = detail({ evidence: [{ id: 1, taskId: 't1', attempt: 1, kind: 'diff', passed: null, summary: '1 file', created: 0 }] });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d, taskDiff }));
    await ready(r);
    await r.act(() => byText(r.container, 'button', 'Show the raw patch').click());
    await waitFor(() => expect(r.$('.diff-add')!.textContent).toBe('+return a - b'), r);
    expect(r.$('.diff-del')!.textContent).toBe('-return 0');
    // the request names the evidence the card is keyed on (L36)
    expect(taskDiff.calls).toEqual([['t1', 1]]);
  });

  it("has one patch viewer: the review card's See the patch brings the Diff section into view (UI review P1-1)", async () => {
    const ev: Evidence = { id: 3, taskId: 't1', attempt: 1, kind: 'diff', passed: null, summary: '1 file', data: { Files: 1, Insertions: 2, Deletions: 0 }, created: 0 };
    const d = detail({
      evidence: [ev],
      pending: [{ id: 'req-1', nodeId: 'approve', step: 3, spec: { kind: 'review', title: 'Review and merge', payload: { files: ['a.go'], branch: 'drydock/t1', base: 'main', evidence: [ev] } } }],
      run: { id: 'run-1', workflow: 'iterate-until-verified', status: 'WAITING_HUMAN', topology: 'x', created: 0, updated: 0, version: 1 },
    });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d }));
    await waitFor(() => need(byText(r.container, 'button', 'See the patch')), r);
    expect(buttonNames(r.container).filter((n) => n === 'Show the raw patch').length).toBe(1);
    const section = r.$('[data-testid="task-diff"]')!;
    let scrolled = 0;
    section.scrollIntoView = () => void scrolled++;
    await r.act(() => byText(r.container, 'button', 'See the patch').click());
    expect(scrolled).toBe(1);
  });

  describe('the raw patch is the one the card is about (L36)', () => {
    const ev = (id: number, kind: string): Evidence => ({ id, taskId: 't1', attempt: 1, kind, passed: null, summary: kind, created: id });
    const PATCHES: Record<number, string> = {
      3: 'diff --git a/a.go b/a.go\n-old\n+FIRST PATCH\n',
      9: 'diff --git a/a.go b/a.go\n-old\n+SECOND PATCH\n',
    };

    it('asks for the newest diff evidence by id, drops the text when a newer patch is recorded, says so, and shows the new one only when asked', async () => {
      let d = detail({ evidence: [ev(2, 'test'), ev(3, 'diff'), ev(4, 'test')] });
      const taskDiff = recorder((_id: string, evidenceId?: number) => PATCHES[evidenceId ?? 0] ?? 'the core would refuse this id');
      const s = svc({ taskGet: async () => d, taskDiff });
      const r = mount(<TaskDetailScreen id="t1" />, s);
      await ready(r);
      await r.act(() => byText(r.container, 'button', 'Show the raw patch').click());
      await waitFor(() => expect(r.$('.diff-add')!.textContent).toBe('+FIRST PATCH'), r);
      // the newest evidence of kind diff is asked for, not the newest evidence
      expect(taskDiff.calls).toEqual([['t1', 3]]);
      expect(r.$('[data-testid="raw-diff-changed"]')).toBeNull();

      // the task goes round again and a second patch is recorded: the first is no longer on the page, the page says why
      d = detail({ evidence: [ev(2, 'test'), ev(3, 'diff'), ev(4, 'test'), ev(8, 'test'), ev(9, 'diff')] });
      s.emit({ type: 'task.evidence', taskId: 't1' });
      const note = await waitFor(() => need(r.$('[data-testid="raw-diff-changed"]')), r);
      expect(note.textContent).toBe('The patch changed since you opened it. Show it again.');
      expect(note.getAttribute('role')).toBe('status');
      expect(r.container.textContent).not.toContain('FIRST PATCH');
      expect(r.$('.diff-add')).toBeNull();
      // nothing was fetched or swapped in on its own
      expect(taskDiff.calls).toEqual([['t1', 3]]);
      expect(r.container.textContent).not.toContain('SECOND PATCH');

      // asked again, it is the new patch, by its id, and the note goes
      await r.act(() => byText(r.container, 'button', 'Show the raw patch').click());
      await waitFor(() => expect(r.$('.diff-add')!.textContent).toBe('+SECOND PATCH'), r);
      expect(taskDiff.calls).toEqual([['t1', 3], ['t1', 9]]);
      expect(r.$('[data-testid="raw-diff-changed"]')).toBeNull();
    });

    it('does not show a patch that was read while a newer one was recorded', async () => {
      let d = detail({ evidence: [ev(3, 'diff')] });
      let release: (text: string) => void = () => undefined;
      const calls: [string, number | undefined][] = [];
      const taskDiff = async (id: string, evidenceId?: number) => {
        calls.push([id, evidenceId]);
        return evidenceId === 3 ? new Promise<string>((res) => (release = res)) : (PATCHES[evidenceId ?? 0] ?? '');
      };
      const s = svc({ taskGet: async () => d, taskDiff });
      const r = mount(<TaskDetailScreen id="t1" />, s);
      await ready(r);
      await r.act(() => byText(r.container, 'button', 'Show the raw patch').click());
      await waitFor(() => expect(calls).toEqual([['t1', 3]]), r);
      // the first patch is still being read when the second is recorded
      d = detail({ evidence: [ev(3, 'diff'), ev(9, 'diff')] });
      s.emit({ type: 'task.evidence', taskId: 't1' });
      await waitFor(() => expect(r.$$('[data-testid="evidence"]').length).toBe(2), r);
      release(PATCHES[3]!);
      await waitFor(() => need(r.$('[data-testid="raw-diff-changed"]')), r);
      expect(r.container.textContent).not.toContain('FIRST PATCH');
      expect(r.$('.diff-add')).toBeNull();
      await r.act(() => byText(r.container, 'button', 'Show the raw patch').click());
      await waitFor(() => expect(r.$('.diff-add')!.textContent).toBe('+SECOND PATCH'), r);
      expect(calls).toEqual([['t1', 3], ['t1', 9]]);
    });

    it('keeps its text through changes that are not a new patch', async () => {
      let d = detail({ evidence: [ev(3, 'diff')] });
      const taskDiff = recorder((_id: string, evidenceId?: number) => PATCHES[evidenceId ?? 0] ?? '');
      const s = svc({ taskGet: async () => d, taskDiff });
      const r = mount(<TaskDetailScreen id="t1" />, s);
      await ready(r);
      await r.act(() => byText(r.container, 'button', 'Show the raw patch').click());
      await waitFor(() => expect(r.$('.diff-add')!.textContent).toBe('+FIRST PATCH'), r);
      d = detail({ evidence: [ev(3, 'diff'), ev(10, 'test')] }); // evidence of another kind is not a new patch
      s.emit({ type: 'task.evidence', taskId: 't1' });
      await waitFor(() => expect(r.$$('[data-testid="evidence"]').length).toBe(2), r);
      expect(r.$('.diff-add')!.textContent).toBe('+FIRST PATCH');
      expect(r.$('[data-testid="raw-diff-changed"]')).toBeNull();
      expect(taskDiff.calls).toEqual([['t1', 3]]);
    });
  });

  it('offers no raw patch when no diff has been recorded', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail() }));
    await ready(r);
    expect(buttonNames(r.container)).not.toContain('Show the raw patch');
  });
});

describe('Task view: assignment and control', () => {
  it('Assign sends the worker and a different reviewer; Start waits until the assignment is saved', async () => {
    const taskAssign = recorder(() => task());
    const d = detail({ task: task({ assignee: undefined, reviewer: undefined }) });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d, taskAssign }));
    await waitFor(() => expect(optionsOf(byLabel<HTMLElement>(r.container, 'Worker')).some((o) => o.value === 'a1')).toBe(true), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Assign').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Start').disabled).toBe(true);
    expect(byText(r.container, 'button', 'Start').getAttribute('title')).toBe('Assign a worker and a reviewer first');
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Worker'), 'a1'));
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Reviewer'), 'a1'));
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Assign').disabled).toBe(true); // same agent twice
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Reviewer'), 'a2'));
    expect(optionsOf(byLabel<HTMLElement>(r.container, 'Worker')).find((o) => o.value === 'a1')!.label).toBe('Dana (backend)');
    await r.act(() => byText(r.container, 'button', 'Assign').click());
    await waitFor(() => expect(taskAssign.calls).toEqual([['t1', 'a1', 'a2']]), r);
  });

  it('Start with a template calls task.startTemplate and shows what the template needs', async () => {
    const taskStartTemplate = recorder(() => ({ runId: 'run1', template: 'feature', workflow: 'feature', roles: { pm: 'a3' }, notes: ['pm: Priya'] }));
    const taskStart = recorder(() => 'run0');
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail(), templateList: async () => templates, taskStartTemplate, taskStart }));
    await ready(r);
    await waitFor(() => expect(optionsOf(byLabel<HTMLElement>(r.container, 'Template')).some((o) => o.value === 'feature')).toBe(true), r);
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Template'), 'feature'));
    const info = r.$('[data-testid="template-info"]')!;
    expect(info.textContent).toContain('role pm');
    expect(info.textContent).toContain('required');
    expect(info.textContent).toContain('accept the spec before anything is built');
    await r.act(() => byText(r.container, 'button', 'Start').click());
    await waitFor(() => expect(taskStartTemplate.calls).toEqual([[{ taskId: 't1', template: 'feature' }]]), r);
    expect(taskStart.calls.length).toBe(0);
    await waitFor(() => expect(r.container.textContent).toContain('pm: Priya'), r);
  });

  it('Start without a template uses the default workflow', async () => {
    const taskStart = recorder(() => 'run0');
    const taskStartTemplate = recorder(() => ({}) as never);
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail(), taskStart, taskStartTemplate, templateList: async () => templates }));
    await ready(r);
    await r.act(() => byText(r.container, 'button', 'Start').click());
    await waitFor(() => expect(taskStart.calls).toEqual([['t1']]), r);
    expect(taskStartTemplate.calls.length).toBe(0);
  });

  it('read-only turns off Start with the core reason', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail() }, { license: expiredLicense }));
    await ready(r);
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Start').disabled).toBe(true), r);
    expect(byText(r.container, 'button', 'Start').getAttribute('title')).toBe(expiredLicense.banner);
  });

  it('cancelling asks first and calls task.cancel only after Yes', async () => {
    const taskCancel = recorder(() => undefined);
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail(), taskCancel }));
    await ready(r);
    await r.act(() => byText(r.container, 'button', 'Cancel task').click());
    expect(r.container.textContent).toContain('Cancel this task and stop its run?');
    await r.act(() => byText(r.container, 'button', 'Keep').click());
    expect(taskCancel.calls.length).toBe(0);
    await r.act(() => byText(r.container, 'button', 'Cancel task').click());
    await r.act(() => byText(r.container, 'button', 'Yes, cancel task').click());
    await waitFor(() => expect(taskCancel.calls).toEqual([['t1']]), r);
  });

  it('a closed task has no assignment or controls', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ status: 'COMPLETED', acceptedBy: 'human:kyle' }) }) }));
    await waitFor(() => expect(r.$('[data-testid="task-status"]')!.textContent).toContain('Completed'), r);
    expect(r.$('[role="group"][aria-label="Assignment and control"]')).toBeNull();
    expect(buttonNames(r.container)).not.toContain('Cancel task');
    expect(r.$('.task-meta')!.textContent).toContain('Accepted by human:kyle');
  });

  it('a task waiting for an earlier one says why and can be let go on', async () => {
    const taskGuardRelease = recorder(() => ({ released: true }));
    const d = detail({ task: task({ status: 'BLOCKED', blockedReason: 'waiting for "Variant A" (both change calc.go) to finish' }) });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d, taskGuardRelease }));
    const note = await waitFor(() => need(r.$('[data-testid="blocked-note"]')), r);
    expect(note.textContent).toContain('Variant A');
    expect(note.textContent).toContain('calc.go');
    await r.act(() => byText(note, 'button', 'Let it go on now').click());
    await waitFor(() => expect(taskGuardRelease.calls).toEqual([['t1']]), r);
  });

  it('a blocked task that is not waiting for another task has no release button', async () => {
    const d = detail({ task: task({ status: 'BLOCKED', blockedReason: 'the build server is down' }) });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d }));
    const note = await waitFor(() => need(r.$('[data-testid="blocked-note"]')), r);
    expect(note.textContent).toContain('build server');
    expect(buttonNames(r.container)).not.toContain('Let it go on now');
  });

  it('a pending spec review shows the spec gate bound to the request id; a budget pause shows the resume form', async () => {
    const runRespond = recorder(() => undefined);
    const specPending = {
      id: 'req-spec',
      nodeId: 'specreview',
      step: 2,
      spec: { kind: 'review', title: 'Accept the spec: Add Multiply', payload: { phase: 'spec', taskId: 't1', title: 'Multiply', spec: 'spec text', criteria: [{ id: 'AC1', given: 'a', when: 'b', then: 'c' }] } },
    };
    const budgetPending = { id: 'req-bud', nodeId: 'work', step: 4, spec: { kind: 'approval', title: 'Budget limit reached', payload: { gate: 'budget', message: 'Budget limit reached: project at 100%.', exceeded: [] } } };
    const run = { id: 'run-1', workflow: 'feature', status: 'WAITING_HUMAN', topology: '', created: 0, updated: 0, version: 1 };
    const d = detail({ task: task({ status: 'WAITING_HUMAN' }), pending: [specPending, budgetPending], run });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d, runRespond }));
    await waitFor(() => expect(r.$('[data-testid="spec-gate"]')).toBeTruthy(), r);
    expect(r.$('[data-testid="review-panel"]')).toBeTruthy();
    expect(r.$('[data-testid="budget-pause"]')!.textContent).toContain('Budget limit reached: project at 100%.');
    expect(buttonNames(r.container)).not.toContain('Approve'); // no generic approve on these kinds
    await r.act(() => byText(r.container, 'button', 'Accept spec').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-1', 'req-spec', 'approved', undefined]]), r);
  });

  it('re-reads the task when one of its events arrives', async () => {
    let status = 'IN_PROGRESS';
    const s = svc({ taskGet: async () => detail({ task: task({ status }) }) });
    const r = mount(<TaskDetailScreen id="t1" />, s);
    await waitFor(() => expect(r.$('[data-testid="task-status"]')!.textContent).toContain('In progress'), r);
    status = 'VERIFICATION';
    s.emit({ type: 'task.status_changed', taskId: 't1' });
    await waitFor(() => expect(r.$('[data-testid="task-status"]')!.textContent).toContain('Awaiting your approval'), r);
  });
});

describe('Evidence and the raw diff', () => {
  const ev = (id: number, kind: string, passed: boolean | null, summary: string): Evidence => ({ id, taskId: 't1', attempt: 1, kind, passed, summary, created: 0 });

  it('evidence shows the test-first kinds with plain labels; skipped e2e is SKIPPED, not a pass', () => {
    const r = renderPlain(
      <EvidenceList
        evidence={[
          ev(1, 'spec', null, 'spec with 2 criteria'),
          ev(2, 'spec_accept', true, 'accepted'),
          ev(3, 'red_test', true, 'RED: 2 failing'),
          ev(4, 'green_test', true, 'GREEN'),
          ev(5, 'e2e', null, 'e2e skipped: no command'),
          ev(6, 'preview', true, 'preview ready at http://127.0.0.1:5173/'),
          { ...ev(7, 'preview', true, 'x'), ref: 'javascript:alert(1)' },
        ]}
      />,
    );
    const rows = r.$$('[data-testid="evidence"]');
    expect(rows[2]!.textContent).toContain('Test written first (red)');
    expect(rows[3]!.textContent).toContain('Tests now pass (green)');
    expect(rows[4]!.getAttribute('data-result')).toBe('skipped');
    expect(rows[4]!.textContent).toContain('SKIPPED');
    expect(rows[1]!.getAttribute('data-result')).toBe('pass');
    expect(Array.from(r.$$('a')).every((a) => !/^javascript/i.test(a.getAttribute('href') ?? ''))).toBe(true);
  });

  it('says plainly when there is no evidence', () => {
    const r = renderPlain(<EvidenceList evidence={[]} />);
    expect(r.container.textContent).toContain('No evidence yet');
  });

  it('renders diff text without interpreting it, and caps very long diffs', async () => {
    const r = renderPlain(<UnifiedDiff text={'+<script>x</script>\n-old\n'} />);
    expect(r.$('script')).toBeNull();
    expect(r.$('.diff-add')!.textContent).toBe('+<script>x</script>');
    const long = Array.from({ length: 3005 }, (_, i) => `+line ${i}`).join('\n');
    const r2 = renderPlain(<UnifiedDiff text={long} />);
    expect(r2.$$('.diff-line').length).toBe(3000);
    await r2.act(() => byText(r2.container, 'button', 'Show all 3005 lines').click());
    // all 3,005 lines are in the view, but only the ones near it are in the page (L37)
    await waitFor(() => expect(r2.$('[data-testid="diff"]')!.getAttribute('data-windowed')).toBe('true'), r2);
    expect(r2.$$('.diff-line').length).toBeLessThan(400);
    expect(r2.$('.diff-line')!.textContent).toBe('+line 0');
    expect(r2.$('[data-testid="diff"]')!.getAttribute('data-rows')).toBe('3005');
  });

  describe('a very long patch (L37)', () => {
    const LINES = 50_000;
    const lineAt = (i: number) => (i % 500 === 0 ? `@@ -${i},3 +${i},4 @@` : i % 3 === 0 ? `-removed ${i}` : i % 3 === 1 ? `+added ${i}` : ` context ${i}`);
    const patch = Array.from({ length: LINES }, (_, i) => lineAt(i)).join('\n');
    const lastLine = lineAt(LINES - 1);
    const frames = () => new Promise<void>((res) => requestAnimationFrame(() => requestAnimationFrame(() => res())));
    const rowTexts = (r: ReturnType<typeof renderPlain>) => Array.from(r.$$('.diff-line')).map((e) => e.textContent!);

    it('draws the first lines as it always did, and only a window of the lines once all of them are asked for', async () => {
      const r = renderPlain(<UnifiedDiff text={patch} />);
      // before "Show all": the first 3,000 lines, the button, and every line selectable as before
      expect(r.$$('.diff-line').length).toBe(3000);
      expect(r.$('[data-testid="diff"]')!.getAttribute('data-windowed')).toBe('false');
      await r.act(() => byText(r.container, 'button', `Show all ${LINES} lines`).click());
      const sc = await waitFor(() => need(r.$('[data-testid="diff"][data-windowed="true"]')) as HTMLElement, r);
      // the keyboard is not lost with the button that was pressed: it is on the view of the patch
      await waitFor(() => expect(document.activeElement).toBe(sc), r);
      await frames();
      // a window: far fewer rows than lines, the first lines among them, a scroller of a most height that is named
      const drawn = rowTexts(r);
      expect(drawn.length).toBeGreaterThan(10);
      expect(drawn.length).toBeLessThan(300);
      expect(drawn[0]).toBe('@@ -0,3 +0,4 @@');
      expect(drawn).not.toContain(lastLine);
      expect(getComputedStyle(sc).overflowY).toBe('auto');
      expect(sc.clientHeight).toBeLessThanOrEqual(560);
      expect(sc.scrollHeight).toBeGreaterThan(LINES * 10);
      expect(sc.getAttribute('aria-label')).toBe('Unified diff');
      expect(sc.getAttribute('role')).toBe('region');
      expect(sc.getAttribute('tabindex')).toBe('0');
      // lines keep their kind
      expect(r.$('.diff-hunk')!.textContent).toBe('@@ -0,3 +0,4 @@');
      expect(r.$('.diff-add')).not.toBeNull();
      expect(r.$('.diff-del')).not.toBeNull();
      expect(r.$('.diff-ctx')).not.toBeNull();
    });

    it('still shows the first and the last line when scrolled to them', async () => {
      const r = renderPlain(<UnifiedDiff text={patch} />);
      await r.act(() => byText(r.container, 'button', `Show all ${LINES} lines`).click());
      const sc = await waitFor(() => need(r.$('[data-testid="diff"][data-windowed="true"]')) as HTMLElement, r);
      await frames();
      const scrollTo = async (top: number) => {
        const before = sc.scrollTop;
        const moved = new Promise((res) => sc.addEventListener('scroll', res, { once: true }));
        sc.scrollTop = top;
        if (sc.scrollTop !== before) await moved;
        r.root.flush();
      };
      await scrollTo(sc.scrollHeight);
      await waitFor(() => expect(rowTexts(r)).toContain(lastLine), r);
      expect(rowTexts(r).length).toBeLessThan(300);
      expect(rowTexts(r)).not.toContain('@@ -0,3 +0,4 @@');
      await scrollTo(0);
      await waitFor(() => expect(rowTexts(r)[0]).toBe('@@ -0,3 +0,4 @@'), r);
      expect(rowTexts(r).length).toBeLessThan(300);
    });

    it('a patch within the cap is drawn whole and offers nothing to expand', () => {
      const r = renderPlain(<UnifiedDiff text={Array.from({ length: 3000 }, (_, i) => `+l${i}`).join('\n')} />);
      expect(r.$$('.diff-line').length).toBe(3000);
      expect(r.$$('button').length).toBe(0);
    });
  });
});
