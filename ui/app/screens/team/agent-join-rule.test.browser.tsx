import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { JoinRule } from '../../api/roomClient.ts';
import { byLabel, cleanup, choose, mount, testServices, waitFor } from '../../test/render.tsx';
import { AgentJoinRule } from './agent-join-rule.tsx';

afterEach(cleanup);

describe('Joins task rooms on the agent editor (X17)', () => {
  it("sets the agent's rule: every task at once, then a folder chosen from the repository's folders with Set, then never", async () => {
    let rules: JoinRule[] = [{ id: 'jr0', projectId: 'p1', agentId: 'a2', pathPrefix: 'web/', state: 'observer', createdBy: 'human:kyle', createdAt: 1 }];
    const added: unknown[] = [];
    const removed: string[] = [];
    let n = 0;
    const s = testServices({
      roomJoinRules: async () => rules,
      repoFolders: async () => ['auth/', 'auth/login/', 'cmd/'],
      roomJoinRuleAdd: async (r) => {
        added.push(r);
        const x = { id: `jr${++n}`, projectId: r.projectId, agentId: r.agentId, pathPrefix: r.pathPrefix, state: r.state ?? 'observer', createdBy: 'human:kyle', createdAt: 2 };
        rules = [...rules, x];
        return x;
      },
      roomJoinRuleRemove: async (id) => {
        removed.push(id);
        rules = rules.filter((r) => r.id !== id);
      },
    });
    const r = mount(<AgentJoinRule projectId="p1" agentId="a1" agentName="Sec" />, s);
    const mode = await waitFor(() => {
      const el = byLabel<HTMLButtonElement>(r.container, 'Joins task rooms');
      if (el.disabled) throw new Error('loading');
      return el;
    }, r);
    expect(mode.getAttribute('data-value')).toBe('never');
    await r.act(() => choose(mode, 'every'));
    await waitFor(() => expect(added).toEqual([{ projectId: 'p1', agentId: 'a1', pathPrefix: '', state: 'observer' }]), r);
    await waitFor(() => expect(byLabel(r.container, 'Joins task rooms').getAttribute('data-value')).toBe('every'), r);
    // a folder: the repository's folders are offered, and Set applies it
    await r.act(() => choose(byLabel(r.container, 'Joins task rooms'), 'folder'));
    const input = r.container.querySelector<HTMLInputElement>('[data-testid="agent-join-folder"]')!;
    await waitFor(() => expect(Array.from(r.container.querySelectorAll(`#${input.getAttribute('list')} option`)).map((o) => o.getAttribute('value'))).toEqual(['auth/', 'auth/login/', 'cmd/']), r);
    await r.act(() => {
      input.value = 'auth/login/';
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await r.act(() => r.container.querySelector<HTMLElement>('[data-testid="agent-join-set"]')!.click());
    await waitFor(() => expect(added[1]).toEqual({ projectId: 'p1', agentId: 'a1', pathPrefix: 'auth/login/', state: 'observer' }), r);
    expect(removed).toEqual(['jr1']);
    // never removes the agent's rules and no one else's (once the rules are read again)
    await waitFor(() => expect(byLabel<HTMLButtonElement>(r.container, 'Joins task rooms').disabled).toBe(false), r);
    expect(byLabel(r.container, 'Joins task rooms').getAttribute('data-value')).toBe('folder');
    await r.act(() => choose(byLabel(r.container, 'Joins task rooms'), 'never'));
    await waitFor(() => expect(removed).toEqual(['jr1', 'jr2']), r);
    expect(rules.map((x) => x.id)).toEqual(['jr0']);
  });
});
