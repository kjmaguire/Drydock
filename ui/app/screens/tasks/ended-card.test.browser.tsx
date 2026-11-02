import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { expiredLicense } from '../../test/fixtures.ts';
import { byText, cleanup, mount, waitFor } from '../../test/render.tsx';
import { EndedCard } from './ended-card.tsx';
import { detail, recorder, svc, task } from './testing.ts';

afterEach(cleanup);

const run = (failedReason?: string) => ({ id: 'run-7', workflow: 'iterate-until-verified', status: 'FAILED', topology: 'x', failedReason, created: 0, updated: 0, version: 1 });

describe('A failed or cancelled task on its page', () => {
  it('says why it stopped and runs it again as a new task after the confirmation, then opens the new task', async () => {
    const taskRunAgain = recorder(() => ({ taskId: 't9', seq: 9, runId: 'run-9', from: 't1' }));
    const went: string[] = [];
    const d = detail({ task: task({ seq: 4, status: 'FAILED' }), run: run('the worker gave up after 5 attempts'), runAgain: true });
    const r = mount(<EndedCard detail={d} onNavigate={(h) => went.push(h)} />, svc({ taskRunAgain }));
    expect(r.$('[data-testid="task-ended"]')!.textContent).toContain('This task failed.');
    expect(r.$('[data-testid="task-ended-reason"]')!.textContent).toBe('Why it stopped: the worker gave up after 5 attempts');
    await r.act(() => byText(r.container, 'button', 'Run again as a new task').click());
    // paid work starts again only after the confirmation, which says what happens
    expect(taskRunAgain.calls).toEqual([]);
    const confirm = r.$('[data-testid="run-again-confirm"]')!;
    expect(confirm.textContent).toContain('A new task with the same title');
    expect(confirm.textContent).toContain('Task #4, which failed, stays as it is');
    await r.act(() => byText(r.container, 'button', 'Run it again').click());
    await waitFor(() => expect(taskRunAgain.calls).toEqual([['t1']]), r);
    await waitFor(() => expect(went).toEqual(['/task/t9']), r);
  });

  it('a read-only licence turns Run again off and says why, before any confirmation', async () => {
    const taskRunAgain = recorder(() => ({ taskId: 't9', seq: 9, runId: 'run-9', from: 't1' }));
    const d = detail({ task: task({ seq: 4, status: 'FAILED' }), run: run('gave up'), runAgain: true });
    const r = mount(<EndedCard detail={d} />, svc({ taskRunAgain }, { license: expiredLicense }));
    const button = () => byText<HTMLButtonElement>(r.container, 'button', 'Run again as a new task');
    await waitFor(() => expect(button().disabled).toBe(true), r);
    expect(button().title).toContain('read-only');
    await r.act(() => button().click());
    expect(r.$('[data-testid="run-again-confirm"]')).toBeNull();
    expect(taskRunAgain.calls).toEqual([]);
  });

  it('says why it cannot run again instead of offering the button', () => {
    const d = detail({ task: task({ status: 'CANCELLED' }), runAgainWhy: 'A person took task #1 on, so no agent runs it.' });
    const r = mount(<EndedCard detail={d} />, svc());
    expect(r.$('[data-testid="task-ended"]')!.textContent).toContain('This task was cancelled.');
    expect(r.$('[data-testid="run-again"]')).toBeNull();
    expect(r.$('[data-testid="task-ended-why"]')!.textContent).toBe('A person took task #1 on, so no agent runs it.');
  });

  it('renders nothing for a task that has not failed or been cancelled', () => {
    for (const status of ['RUNNING', 'COMPLETED', 'BACKLOG']) {
      const r = mount(<EndedCard detail={detail({ task: task({ status }) })} />, svc());
      expect(r.$('[data-testid="task-ended"]')).toBeNull();
      cleanup();
    }
  });
});
