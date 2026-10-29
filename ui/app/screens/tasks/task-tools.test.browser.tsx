import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { TaskToolsView } from '../../api/mcpClient.ts';
import { byLabel, byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { TaskTools } from './task-tools.tsx';

afterEach(cleanup);

describe("A task's agent tools (P115)", () => {
  it("shows what the agent gets, narrows it to the ticked tools and stops narrowing", async () => {
    const all = ['propose_memory', 'read_range', 'search_memory', 'docs__search', 'fetch_range'];
    let narrowed: string[] | null = null;
    const sent: (string[] | null)[] = [];
    const view = (): TaskToolsView => ({
      taskId: 't1',
      agentId: 'agt_1',
      choices: [
        { name: 'propose_memory', label: 'propose_memory', group: 'workspace' },
        { name: 'read_range', label: 'read_range', group: 'workspace' },
        { name: 'search_memory', label: 'search_memory', group: 'workspace' },
        { name: 'docs__*', label: 'Docs double', group: 'server' },
      ],
      narrowed,
      // the labelled test double narrows the way the core does: exposed names that match a kept name or glob
      agentTools: narrowed === null ? all : all.filter((x) => x === 'fetch_range' || narrowed!.some((g) => (g.endsWith('*') ? x.startsWith(g.slice(0, -1)) : x === g))),
    });
    const client: Partial<Client> = {
      taskMcpToolsGet: async () => view(),
      taskMcpToolsSet: async (_t, tools) => {
        sent.push(tools);
        narrowed = tools;
        return view();
      },
    };
    const r = mount(<TaskTools taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="task-agent-tools"]')!.textContent).toContain('search_memory'), r);
    expect(r.$('[data-testid="task-tools"]')!.getAttribute('data-narrowed')).toBe('false');
    await r.act(() => (r.$('[data-testid="task-tools-edit"]') as HTMLButtonElement).click());
    // every tool starts ticked; untick memory and the proposal, keep files and the docs server
    await r.act(() => byLabel<HTMLInputElement>(r.container, 'search_memory').click());
    await r.act(() => byLabel<HTMLInputElement>(r.container, 'propose_memory').click());
    await r.act(() => byText(r.container, 'button', 'Keep only these').click());
    await waitFor(() => expect(sent).toEqual([['read_range', 'docs__*']]), r);
    await waitFor(() => expect(r.$('[data-testid="task-tools"]')!.getAttribute('data-narrowed')).toBe('true'), r);
    expect(r.$('[data-testid="task-agent-tools"]')!.textContent).not.toContain('search_memory');
    await r.act(() => (r.$('[data-testid="task-tools-clear"]') as HTMLButtonElement).click());
    await waitFor(() => expect(sent[1]).toBe(null), r);
    await waitFor(() => expect(r.$('[data-testid="task-tools"]')!.getAttribute('data-narrowed')).toBe('false'), r);
  });
});
