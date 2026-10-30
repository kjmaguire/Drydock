import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RouteLevel } from '../../api/extTypes.ts';
import { byLabel, cleanup, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { RoutingLevelPanel } from './project-routing-level.tsx';

afterEach(cleanup);

const levels = ['cheapest', 'fast', 'balanced', 'quality', 'maximum_quality', 'custom'];

describe("The project's routing level (P118)", () => {
  it('follows the workspace until the project chooses its own, says which applies, and shows a refusal', async () => {
    const calls: string[] = [];
    let current: RouteLevel = { projectId: 'prj_1', workspace: 'cheapest', project: '', effective: 'cheapest', source: 'workspace', levels };
    let refuse = false;
    const r = mount(
      <RoutingLevelPanel projectId="prj_1" />,
      testServices({
        routeLevelGet: async () => current,
        routeLevelSet: async (id: string, level: string) => {
          calls.push(`${id}:${level}`);
          if (refuse) throw new Error('only a person may change the routing level');
          current = { ...current, project: level, effective: level || current.workspace, source: level ? 'project' : 'workspace' };
          return current;
        },
      } as Partial<Client>),
    );
    const sel = await waitFor(() => {
      const s = byLabel<HTMLElement>(r.container, 'Level');
      if (optionsOf(s).length < 2) throw new Error('not loaded');
      return s;
    }, r);
    // the first choice follows the workspace and names its level; then every level with what it does
    expect(optionsOf(sel)[0]!.label).toContain('Follow the workspace (Cheapest)');
    expect(optionsOf(sel).map((o) => o.value)).toEqual(['', 'cheapest', 'fast', 'balanced', 'quality', 'maximum_quality']);
    expect(valueOf(sel)).toBe('');
    expect(r.$('[data-testid="routing-level-now"]')!.textContent).toContain("run at Cheapest, the workspace's level");
    type(sel, 'quality');
    await waitFor(() => expect(calls).toEqual(['prj_1:quality']), r);
    await waitFor(() => expect(r.$('[data-testid="routing-level-now"]')!.getAttribute('data-source')).toBe('project'), r);
    expect(r.$('[data-testid="routing-level-now"]')!.textContent).toContain('run at Quality, its own level');
    expect(valueOf(byLabel(r.container, 'Level'))).toBe('quality');
    // back to following the workspace
    type(byLabel<HTMLElement>(r.container, 'Level'), '');
    await waitFor(() => expect(r.$('[data-testid="routing-level-now"]')!.getAttribute('data-source')).toBe('workspace'), r);
    refuse = true;
    type(byLabel<HTMLElement>(r.container, 'Level'), 'fast');
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('only a person'), r);
    expect(calls).toEqual(['prj_1:quality', 'prj_1:', 'prj_1:fast']);
  });
});
