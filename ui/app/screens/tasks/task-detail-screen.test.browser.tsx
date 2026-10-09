import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { WorkflowTemplate } from '../../api/extTypes.ts';
import type { Evidence } from '../../api/types.ts';
import { expiredLicense, usage } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, renderPlain, type, waitFor } from '../../test/render.tsx';
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
  await waitFor(() => expect(byLabel<HTMLSelectElement>(r.container, 'Worker').value).toBe('a1'), r);
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
    expect(meta.textContent).toContain('Attempt 2 of 5');
    expect(r.$('[data-testid="task-cost"]')!.textContent).toBe('$0.50');
    await waitFor(() => expect(r.$('[data-testid="task-cost"]')!.parentElement!.textContent).toContain('estimated'), r);
    expect(byText(r.container, 'a', 'Open task channel').getAttribute('href')).toBe('/room/room_9');
    expect(byText(r.container, 'a', 'Workflow and explanation').getAttribute('href')).toBe('/task/t1/run');
    expect(r.$('strong')).toBeTruthy();
    expect(r.container.textContent).toContain('Given 3 and 4 When Multiply Then 12');
    expect(r.$('.md strong')!.textContent).toBe('Multiply');
    // while a run is active, only cancel is offered (no assignment)
    expect(r.$('select[aria-label="Worker"]')).toBeNull();
    expect(buttonNames(r.container)).toContain('Cancel task');
    await waitFor(() => expect(r.$$('[data-testid="usage-row"]').length).toBe(1), r);
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
    const taskDiff = recorder(() => 'diff --git a/calc.go b/calc.go\n@@ -1 +1 @@\n-return 0\n+return a - b\n');
    const d = detail({ evidence: [{ id: 1, taskId: 't1', attempt: 1, kind: 'diff', passed: null, summary: '1 file', created: 0 }] });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d, taskDiff }));
    await ready(r);
    await r.act(() => byText(r.container, 'button', 'Show the raw patch').click());
    await waitFor(() => expect(r.$('.diff-add')!.textContent).toBe('+return a - b'), r);
    expect(r.$('.diff-del')!.textContent).toBe('-return 0');
    expect(taskDiff.calls).toEqual([['t1']]);
  });

  it('says when no diff has been recorded', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail() }));
    await ready(r);
    expect(r.container.textContent).toContain('No diff has been recorded yet.');
    expect(buttonNames(r.container)).not.toContain('Show the raw patch');
  });
});

describe('Task view: assignment and control', () => {
  it('Assign sends the worker and a different reviewer; Start waits until the assignment is saved', async () => {
    const taskAssign = recorder(() => task());
    const d = detail({ task: task({ assignee: undefined, reviewer: undefined }) });
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => d, taskAssign }));
    await waitFor(() => expect(r.$('select[aria-label="Worker"] option[value="a1"]')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Assign').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Start').disabled).toBe(true);
    expect(byText(r.container, 'button', 'Start').getAttribute('title')).toBe('Assign a worker and a reviewer first');
    await r.act(() => type(byLabel<HTMLSelectElement>(r.container, 'Worker'), 'a1'));
    await r.act(() => type(byLabel<HTMLSelectElement>(r.container, 'Reviewer'), 'a1'));
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Assign').disabled).toBe(true); // same agent twice
    await r.act(() => type(byLabel<HTMLSelectElement>(r.container, 'Reviewer'), 'a2'));
    expect(r.$('select[aria-label="Worker"]')!.querySelector('option[value="a1"]')!.textContent).toBe('Dana (backend)');
    await r.act(() => byText(r.container, 'button', 'Assign').click());
    await waitFor(() => expect(taskAssign.calls).toEqual([['t1', 'a1', 'a2']]), r);
  });

  it('Start with a template calls task.startTemplate and shows what the template needs', async () => {
    const taskStartTemplate = recorder(() => ({ runId: 'run1', template: 'feature', workflow: 'feature', roles: { pm: 'a3' }, notes: ['pm: Priya'] }));
    const taskStart = recorder(() => 'run0');
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail(), templateList: async () => templates, taskStartTemplate, taskStart }));
    await ready(r);
    await waitFor(() => expect(r.$('select[aria-label="Template"] option[value="feature"]')).toBeTruthy(), r);
    await r.act(() => type(byLabel<HTMLSelectElement>(r.container, 'Template'), 'feature'));
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
    expect(r2.$$('.diff-line').length).toBe(3005);
  });
});
