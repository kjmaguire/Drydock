import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { detail, task } from '../screens/tasks/testing.ts';
import { Details } from './details.tsx';

afterEach(cleanup);

describe('the details panel: the Task tab', () => {
  // audit 2026-10-16, M27: the same badge as on the task's card and page, for a task the team lead split off
  it('marks a task the team lead split off, and says nothing for another', async () => {
    for (const [origin, badge] of [
      ['lead:3', true],
      [undefined, false],
      ['lead:oops', false],
    ] as const) {
      const s = testServices({ taskGet: async () => detail({ task: task({ origin }) }) });
      s.ui.setDetails(true);
      s.ui.setTab('task');
      const r = mount(<Details taskId="t1" page="conversation" />, s);
      await waitFor(() => expect(r.container.textContent).toContain('#1 Add Multiply'), r);
      expect(r.$('[data-testid="task-status"]')).toBeTruthy();
      const b = r.$('[data-testid="task-origin"]');
      if (badge) {
        expect(b!.textContent).toBe('Split off by the team lead');
        expect(b!.getAttribute('title')).toBe('Step 3 from a task you made');
      } else expect(b).toBeNull();
      cleanup();
    }
  });
});
