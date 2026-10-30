import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RouteLevel } from '../../api/extTypes.ts';
import { byLabel, cleanup, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { TaskRoutingLevel } from './task-level.tsx';

afterEach(cleanup);

const levels = ['cheapest', 'fast', 'balanced', 'quality', 'maximum_quality', 'custom'];

describe("A task's routing level (P127)", () => {
  it("follows its template's level until a person chooses the task's own, says which applies, and shows a refusal", async () => {
    const calls: string[] = [];
    const base: RouteLevel = {
      projectId: 'prj_1',
      workspace: 'balanced',
      project: 'cheapest',
      taskId: 'task_1',
      template: 'Security',
      templateLevel: 'quality',
      effective: 'quality',
      source: 'template',
      levels,
    };
    let current = base;
    let refuse = false;
    const svc = testServices({
      routeLevelTaskGet: async () => current,
      routeLevelTaskSet: async (id: string, level: string) => {
        calls.push(`${id}:${level}`);
        if (refuse) throw new Error('only a person may change the routing level');
        current = level ? { ...base, task: level, effective: level, source: 'task' } : base;
        return current;
      },
    } as Partial<Client>);
    const r = mount(<TaskRoutingLevel taskId="task_1" />, svc);
    const sel = await waitFor(() => {
      const s = byLabel<HTMLElement>(r.container, 'Routing level');
      if (optionsOf(s).length < 2) throw new Error('not loaded');
      return s;
    }, r);
    expect(optionsOf(sel)[0]!.label).toBe('Follow the Security template (Quality)');
    expect(valueOf(sel)).toBe('');
    const now = () => r.$('[data-testid="task-routing-level"]')!;
    expect(now().getAttribute('data-source')).toBe('template');
    expect(r.$('[data-testid="task-routing-level-now"]')!.textContent).toBe("This task's agent turns run at Quality, the level the Security template declares.");
    type(sel, 'cheapest');
    await waitFor(() => expect(now().getAttribute('data-source')).toBe('task'), r);
    expect(r.$('[data-testid="task-routing-level-now"]')!.textContent).toBe("This task's agent turns run at Cheapest, its own level.");
    expect(valueOf(byLabel(r.container, 'Routing level'))).toBe('cheapest');
    // back to the template's
    type(byLabel<HTMLElement>(r.container, 'Routing level'), '');
    await waitFor(() => expect(now().getAttribute('data-source')).toBe('template'), r);
    // a change made elsewhere (another window) is read again
    current = { ...base, task: 'fast', effective: 'fast', source: 'task' };
    svc.emit({ type: 'routing.level_changed' });
    await waitFor(() => expect(valueOf(byLabel(r.container, 'Routing level'))).toBe('fast'), r);
    refuse = true;
    type(byLabel<HTMLElement>(r.container, 'Routing level'), 'maximum_quality');
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('only a person'), r);
    expect(calls).toEqual(['task_1:cheapest', 'task_1:', 'task_1:maximum_quality']);
  });
});
