import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Agent } from '../../api/types.ts';
import { byLabel, byText, choose, cleanup, mount, optionsOf, valueOf, waitFor } from '../../test/render.tsx';
import { TaskControls } from '../tasks/task-controls.tsx';
import { agent, detail, recorder, svc, task } from '../tasks/testing.ts';
import { AddReviewerNote, AddSuggestedTeam, aloneInCrew } from './add-reviewer.tsx';

afterEach(cleanup);

const builder = { ...agent('a1', 'Backend Engineer', 'backend'), adapter: 'codex' } as Agent;
const applied = (id: string, name: string) => ({ id: 'code-reviewer', agent: agent(id, name, 'reviewer'), mcp: { granted: [], skipped: [] }, skills: { assigned: [], shared: [], pending: [], failed: [] }, notes: [] });

describe('a crew of one builder cannot run a task (E1)', () => {
  it('knows when a crew has no different reviewer', () => {
    expect(aloneInCrew([builder])?.id).toBe('a1');
    expect(aloneInCrew([builder, agent('a2', 'Rex', 'reviewer')])).toBe(undefined);
    expect(aloneInCrew([])).toBe(undefined);
    expect(aloneInCrew([agent('l1', 'Lead', 'team lead')])).toBe(undefined);
  });

  it('offers the missing Code Reviewer in one click, on the builder’s CLI', async () => {
    const agentCatalogApply = recorder(() => applied('a9', 'Code Reviewer'));
    const r = mount(<AddReviewerNote projectId="prj_1" agents={[builder]} />, svc({ agentCatalogApply }));
    expect(r.$('[data-testid="add-reviewer"]')!.textContent).toContain('Tasks need a reviewer besides Backend Engineer.');
    await r.act(() => byText(r.container, 'button', 'Add Code Reviewer').click());
    await waitFor(() => expect(agentCatalogApply.calls).toEqual([[{ id: 'code-reviewer', projectId: 'prj_1', adapter: 'codex', installSkills: true, connectMcp: true }]]), r);
    cleanup();
    // a crew that can run a task is not offered anything
    const two = mount(<AddReviewerNote projectId="prj_1" agents={[builder, agent('a2', 'Rex', 'reviewer')]} />, svc());
    expect(two.$('[data-testid="add-reviewer"]')).toBeNull();
  });

  it('a project with no agent is offered the suggested team in one call', async () => {
    const agentCatalogApplyMany = recorder(() => [{ id: 'team-lead', applied: applied('l1', 'Team Lead') }, { id: 'backend-engineer', error: 'no CLI' }]);
    const r = mount(<AddSuggestedTeam projectId="prj_1" />, svc({ agentCatalogApplyMany }));
    await r.act(() => byText(r.container, 'button', 'Add the suggested team').click());
    await waitFor(
      () =>
        expect(agentCatalogApplyMany.calls).toEqual([
          [{ projectId: 'prj_1', items: [{ id: 'team-lead' }, { id: 'backend-engineer' }, { id: 'code-reviewer' }], installSkills: true, connectMcp: true }],
        ]),
      r,
    );
    // what was not added is said, never hidden
    await waitFor(() => expect(r.$('[data-testid="add-team-failed"]')!.textContent).toBe('Not added: backend-engineer: no CLI'), r);
  });

  it("the task page's Reviewer offers Add Code Reviewer first and chooses the agent it added", async () => {
    const agentCatalogApply = recorder((_req: unknown) => applied('a9', 'Code Reviewer'));
    const d = detail({ task: task({ assignee: undefined, reviewer: undefined }) });
    const r = mount(<TaskControls detail={d} onChanged={() => undefined} />, svc({ agentCatalogApply }, { agents: [builder] }));
    const reviewer = await waitFor(() => {
      const el = byLabel<HTMLElement>(r.container, 'Reviewer');
      if (optionsOf(el).length < 3) throw new Error('agents not loaded');
      return el;
    }, r);
    expect(optionsOf(reviewer).map((o) => o.value).slice(0, 2)).toEqual(['', '__add-code-reviewer']);
    expect(optionsOf(reviewer)[1]!.label).toContain('Add Code Reviewer');
    await r.act(() => choose(reviewer, '__add-code-reviewer'));
    await waitFor(() => expect(agentCatalogApply.calls.length).toBe(1), r);
    expect(agentCatalogApply.calls[0]![0]).toMatchObject({ id: 'code-reviewer', projectId: 'prj_1' });
    await waitFor(() => expect(valueOf(byLabel<HTMLElement>(r.container, 'Reviewer'))).toBe('a9'), r);
  });
});
