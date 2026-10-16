import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Agent, AgentDetail, Project } from '../../api/types.ts';
import { usage, usageTotals } from '../../test/fixtures.ts';
import { byText, cleanup, mount, renderPlain, testServices, waitFor } from '../../test/render.tsx';
import { CapabilityList } from './fields.tsx';
import { AgentInspector } from './inspector.tsx';

afterEach(cleanup);

const agent = (over: Partial<Agent> = {}): Agent => ({
  id: 'a1',
  projectId: 'p',
  name: 'Worker',
  adapter: 'claude',
  role: 'backend',
  model: '',
  effort: '',
  providerId: '',
  permissionMode: 'acceptEdits',
  status: '',
  ...over,
});

function setup(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'p', name: 'gocalc' } as Project],
    agentList: async () => [agent()],
    presenceList: async () => [],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    taskList: async () => [],
    routeDecisions: async () => [],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

async function inspector(r: ReturnType<typeof mount>) {
  return waitFor(() => {
    const el = r.container.querySelector<HTMLElement>('[data-testid="inspector"]');
    if (!el) throw new Error('no inspector yet');
    return el;
  }, r);
}

describe('Agent inspector', () => {
  it('shows runs with PID and cwd (not reported when absent) and usage with sources', async () => {
    const detail: AgentDetail = {
      agent: agent(),
      runs: [
        { id: 'r1', taskId: 't1', pid: 4242, cwd: '/w/t1', status: 'completed', startedAt: 1_700_000_000_000, endedAt: 1_700_000_100_000 },
        { id: 'r2', taskId: '', pid: 0, cwd: '', status: 'failed', startedAt: 1_700_000_200_000, endedAt: 0, error: 'exit 2' },
      ],
      usage: [usage({})],
      usageTotals: usageTotals([usage({})]),
    };
    const dms: [string, string][] = [];
    const navigated: string[] = [];
    const s = setup({
      agentGet: async () => detail,
      presenceList: async () => [{ agentId: 'a1', presence: 'Working', activity: 'tool: Write', since: '', updated: '', seq: 1 }],
      roomCreateDM: async (p, a) => (dms.push([p, a]), { id: 'dm1' } as never),
    });
    const r = mount(<AgentInspector agentId="a1" onNavigate={(h) => navigated.push(h)} />, s);
    const box = await inspector(r);
    const runs = Array.from(box.querySelectorAll('[data-testid="agent-run"]'));
    expect(runs.length).toBe(2);
    expect(runs[0]!.textContent).toContain('4242');
    expect(runs[0]!.textContent).toContain('/w/t1');
    expect(runs[0]!.querySelector('a[href="/task/t1"]')).toBeTruthy();
    expect(runs[0]!.querySelector('a[href="/task/t1/run"]')).toBeTruthy();
    expect(runs[1]!.textContent).toContain('not reported');
    expect(runs[1]!.textContent).toContain('not ended');
    expect(runs[1]!.textContent).toContain('exit 2');
    expect(box.textContent).toContain('adapter default (not set)');
    expect(box.textContent).toMatch(/Not routed/);
    expect(box.querySelector('[data-testid="usage-row"]')!.textContent).toContain('reported');
    await waitFor(() => expect(box.textContent).toContain('tool: Write'), r);
    expect(box.querySelector('[data-testid="task-decisions"]')).toBeTruthy();
    await r.act(() => byText(box, 'button', 'Message Worker').click());
    await waitFor(() => expect(navigated).toEqual(['/room/dm1']), r);
    expect(dms).toEqual([['p', 'a1']]);
  });

  it('marks a permission mode that skips approvals with the red badge next to the mode, and no other mode (M1)', async () => {
    const modeRow = (box: HTMLElement) => Array.from(box.querySelectorAll('dt')).find((d) => d.textContent === 'Permission mode')!.nextElementSibling as HTMLElement;
    for (const mode of ['bypassPermissions', 'auto', 'dontAsk']) {
      const r = mount(<AgentInspector agentId="a1" />, setup({ agentGet: async () => ({ agent: agent({ permissionMode: mode }), runs: [], usage: [], usageTotals: usageTotals() }) }));
      const dd = modeRow(await inspector(r));
      expect(dd.textContent).toContain(mode);
      expect(dd.querySelector('[data-testid="skips-approvals-badge"]')!.textContent).toBe('skips approvals');
      cleanup();
    }
    const r = mount(<AgentInspector agentId="a1" />, setup({ agentGet: async () => ({ agent: agent({ permissionMode: 'acceptEdits' }), runs: [], usage: [], usageTotals: usageTotals() }) }));
    expect(modeRow(await inspector(r)).querySelector('[data-testid="skips-approvals-badge"]')).toBeNull();
  });

  it('says the usage list holds only the newest rows when the agent has more (M21)', async () => {
    const newest = Array.from({ length: 50 }, () => usage({}));
    const r = mount(<AgentInspector agentId="a1" />, setup({ agentGet: async () => ({ agent: agent(), runs: [], usage: newest, usageTotals: usageTotals(newest, { rows: 1203 }) }) }));
    const box = await inspector(r);
    expect(box.querySelectorAll('[data-testid="usage-row"]').length).toBe(50);
    expect(box.querySelector('[data-testid="usage-cut"]')!.textContent).toBe('Showing the newest 50 of 1,203 usage rows.');
    cleanup();
    const few = mount(<AgentInspector agentId="a1" />, setup({ agentGet: async () => ({ agent: agent(), runs: [], usage: [usage({})], usageTotals: usageTotals([usage({})]) }) }));
    expect((await inspector(few)).querySelector('[data-testid="usage-cut"]')).toBeNull();
  });

  it('shows the purpose the agent is given, or none set', async () => {
    const r = mount(<AgentInspector agentId="a1" />, setup({ agentGet: async () => ({ agent: agent({ purpose: 'Billing only.' }), runs: [], usage: [], usageTotals: usageTotals() }) }));
    const box = await inspector(r);
    expect(box.querySelector('[data-testid="inspector-purpose"]')!.textContent).toBe('Billing only.');
    expect(box.textContent).toContain('This agent has not run yet.');
    cleanup();
    const r2 = mount(<AgentInspector agentId="a1" />, setup({ agentGet: async () => ({ agent: agent(), runs: null, usage: null, usageTotals: usageTotals() }) }));
    const box2 = await inspector(r2);
    expect(box2.querySelector('[data-testid="inspector-purpose"]')!.textContent).toBe('none set');
  });

  it('says whether the router chooses this agent\'s model, and between which models', async () => {
    const r = mount(
      <AgentInspector agentId="a1" />,
      setup({ agentGet: async () => ({ agent: agent({ routeModels: [{ model: 'claude-haiku-4-5', tier: 'cheap' }, { model: 'claude-opus-4-5', tier: 'premium' }] }), runs: [], usage: [], usageTotals: usageTotals() }) }),
    );
    const routing = (await inspector(r)).querySelector<HTMLElement>('[data-testid="inspector-routing"]')!;
    expect(routing.textContent).toContain('claude-haiku-4-5 (cheap), claude-opus-4-5 (premium)');
    expect(routing.textContent).not.toMatch(/Not routed|Slice/);
    // every turn is routed (P63): the room replies and the template steps too, not only task work
    expect(routing.textContent).toContain('room replies');
    expect(routing.textContent).not.toContain('use the configured model');
  });

  it('states the isolation in plain words, and warns when the permission mode lets the agent act freely', async () => {
    const detail = (level: 'guarded' | 'open'): AgentDetail => ({
      agent: agent(),
      runs: [],
      usage: [],
      usageTotals: usageTotals(),
      isolation: {
        level,
        summary: level === 'guarded' ? 'Claude Code checks keep it to its folder. This is not an operating-system sandbox.' : 'Its permission mode lets it act without asking.',
        details: ['Its own worktree.'],
      },
    });
    const r = mount(<AgentInspector agentId="a1" />, setup({ agentGet: async () => detail('guarded') }));
    const box = (await inspector(r)).querySelector<HTMLElement>('[data-testid="inspector-isolation"]')!;
    expect(box.getAttribute('data-level')).toBe('guarded');
    expect(box.textContent).toContain('not an operating-system sandbox');
    expect(box.textContent).toContain('Its own worktree.');
    cleanup();
    const r2 = mount(<AgentInspector agentId="a1" />, setup({ agentGet: async () => detail('open') }));
    const open = (await inspector(r2)).querySelector<HTMLElement>('[data-testid="inspector-isolation"]')!;
    expect(open.getAttribute('data-level')).toBe('open');
    expect(open.textContent).toContain('not held in');
  });

  it('says what to do when no agent is chosen', () => {
    const r = mount(<AgentInspector agentId={undefined} />, setup({}));
    expect(r.container.textContent).toMatch(/press Inspect/);
  });

  it('shows the core error when the agent cannot be read', async () => {
    const r = mount(
      <AgentInspector agentId="gone" />,
      setup({
        agentGet: async () => {
          throw new Error('agent gone not found');
        },
      }),
    );
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('agent gone not found'), r);
  });

  it('re-reads when an event names the agent', async () => {
    let purpose = 'first';
    const s = setup({ agentGet: async () => ({ agent: agent({ purpose }), runs: [], usage: [], usageTotals: usageTotals() }) });
    const r = mount(<AgentInspector agentId="a1" />, s);
    await waitFor(() => expect((r.container.querySelector('[data-testid="inspector-purpose"]') as HTMLElement | null)?.textContent).toBe('first'), r);
    purpose = 'second';
    s.emit({ type: 'agent.updated', agentId: 'a1', projectId: 'p' });
    await waitFor(() => expect(r.container.querySelector('[data-testid="inspector-purpose"]')!.textContent).toBe('second'), r);
  });
});

describe('CapabilityList', () => {
  it('capabilities that are false or absent read as not verified', () => {
    const r = renderPlain(<CapabilityList caps={undefined} />);
    expect(r.container.textContent).not.toContain('verified: ');
    expect(r.container.querySelectorAll('[data-verified="true"]').length).toBe(0);
    expect(r.container.textContent).toContain('Streaming: not verified');
  });

  it('a true capability reads as verified', () => {
    const r = renderPlain(<CapabilityList caps={{ streaming: true, mcp: false } as never} />);
    expect(r.container.querySelector('[data-cap="streaming"]')!.getAttribute('data-verified')).toBe('true');
    expect(r.container.querySelector('[data-cap="streaming"]')!.textContent).toContain('Streaming: verified');
    expect(r.container.querySelector('[data-cap="mcp"]')!.textContent).toContain('MCP: not verified');
  });
});
