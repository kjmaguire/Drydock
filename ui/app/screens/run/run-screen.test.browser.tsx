import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ExecView, GraphView, RunExplanation } from '../../api/runClient.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { RunScreen } from './run-screen.tsx';

afterEach(cleanup);

const g: GraphView = {
  name: 'iuv',
  entry: ['prepare'],
  nodes: ['prepare', 'work', 'verify', 'review', 'approve', 'done'].map((id) => ({ id, kind: id === 'work' || id === 'review' ? 'agent_task' : id === 'approve' ? 'request' : 'check' })),
  edges: [
    { from: 'prepare', to: 'work', kind: 'direct' },
    { from: 'work', to: 'verify', kind: 'direct' },
    { from: 'verify', to: 'work', kind: 'loop', when: 'fail', maxIter: 5 },
    { from: 'verify', to: 'review', kind: 'conditional', when: 'pass' },
    { from: 'review', to: 'approve', kind: 'direct' },
    { from: 'review', to: 'work', kind: 'loop', when: 'revise' },
    { from: 'approve', to: 'done', kind: 'direct' },
  ],
};

const ex = (nodeId: string, step: number, status = 'done', attempt = 1, extra: Partial<ExecView> = {}): ExecView => ({ nodeId, step, attempt, status, started: 1000 * step, finished: 1000 * step + 500, ...extra });

const explanation = (): RunExplanation => ({
  run: { id: 'run_1', workflow: 'iuv', status: 'WAITING_HUMAN', created: 1, updated: 2 },
  runs: [{ id: 'run_1', workflow: 'iuv', status: 'WAITING_HUMAN', created: 1 }],
  graph: g,
  executions: [
    ex('prepare', 1),
    ex('work', 2, 'done', 1, {
      agentId: 'agt_w',
      hasPrompt: true,
      logId: 'nx:run_1|work|2|1',
      includedBytes: 2048,
      manifest: [
        { source: 'prompt (work)', included: true, bytes: 2048, reason: 'task spec', priority: 1 },
        { source: 'repo map', included: false, bytes: 0, reason: 'not built', priority: 9 },
      ],
      decision: { id: 'dec_1', decidedBy: 'static', model: 'fake-1', reason: 'team setup' },
      usage: [{ ts: 2500, taskId: 't1', agentId: 'agt_w', provider: 'anthropic', model: 'fake-1', tier: 'premium', inputTokens: 10, outputTokens: 5, cachedTokens: 0, cacheWriteTokens: 0, reasoningTokens: 0, costUsd: 0.02, source: 'reported', cacheHit: false }],
    }),
  ],
  checkpoints: [
    { id: 'cp1', step: 0, kind: 'entry', created: 1, status: 'RUNNING' },
    { id: 'cp2', step: 2, kind: 'step', created: 2, status: 'WAITING_HUMAN', latest: true, waiting: ['approve'] },
  ],
  mcpCalls: [{ seq: 9, ts: 3, type: 'mcp.denied', agentId: 'agt_w', data: { tool: 'search_pack' } }],
  events: [{ seq: 1, ts: 1, type: 'workflow.started' }],
});

function setup(client: Partial<Client> = {}) {
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc' } as never],
    agentList: async () => [{ id: 'agt_w', name: 'Worker One', role: 'backend' } as never],
    roomList: async () => [],
    roomUnread: async () => [],
    needsYouList: async () => [],
    presenceList: async () => [],
    taskList: async () => [],
    runExplain: async () => explanation(),
    taskGet: async () => ({ task: { id: 't1', seq: 3, title: 'Subtract' } }) as never,
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

type R = ReturnType<typeof mount>;

const all = (r: R, sel: string): HTMLElement[] => Array.from(r.container.querySelectorAll<HTMLElement>(sel));

const node = (r: R, id: string) => r.$(`[data-testid="wf-node"][data-node="${id}"]`) as SVGGElement | null;
const tab = (r: R, name: RegExp) => byText(r.container, '[role="tab"]', name);
const key = (el: Element, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));

async function ready(r: R) {
  await waitFor(() => {
    const w = node(r, 'work');
    if (!w || !w.textContent?.includes('Worker One')) throw new Error('graph not drawn yet');
  }, r);
}

describe('RunScreen', () => {
  it('explains each turn: manifest, the exact prompt, why this model, usage; and lists checkpoints, MCP calls and events', async () => {
    const calls: unknown[][] = [];
    const s = setup({
      runPrompt: async (...a: unknown[]) => {
        calls.push(a);
        return 'Implement Subtract.\n<script>x</script>';
      },
    });
    const r = mount(<RunScreen taskId="t1" />, s);
    await ready(r);
    await waitFor(() => expect(r.$('h1')!.textContent).toContain('Run of #3 Subtract'), r);
    expect(r.$('[data-testid="run-id"]')!.textContent).toBe('run_1');
    expect(r.$('[data-testid="run-status"]')!.textContent).toBe('WAITING_HUMAN');
    expect(r.$('a[href="/task/t1"]')!.textContent).toContain('Back to the task');

    await r.act(() => tab(r, /Turns/).click());
    const work = all(r, '[data-testid="turn"]').find((li) => li.getAttribute('data-node') === 'work')!;
    expect(work.textContent).toContain('Worker One');
    expect(work.textContent).toContain('fake-1');
    expect(work.textContent).toContain('15 tokens');
    await r.act(() => work.querySelector('button')!.click());
    await waitFor(() => expect(all(r, '[data-testid="manifest-item"]').map((m) => m.getAttribute('data-included'))).toEqual(['true', 'false']), r);
    expect(work.textContent).toContain('team setup');
    expect(work.querySelectorAll('[data-testid="usage-row"]').length).toBe(1);
    await r.act(() => byText(work, 'button', 'Show the exact prompt').click());
    await waitFor(() => expect(work.querySelector('[data-testid="prompt-text"]')!.textContent).toContain('Implement Subtract.'), r);
    expect(calls).toEqual([['t1', 'run_1', 'work', 2, 1]]);
    expect(r.container.querySelector('script')).toBeNull();

    await r.act(() => tab(r, /Checkpoints/).click());
    await waitFor(() => expect(all(r, '[data-testid="checkpoint"]').map((c) => c.getAttribute('data-latest'))).toEqual(['false', 'true']), r);
    await r.act(() => tab(r, /MCP calls/).click());
    await waitFor(() => expect(r.$('[data-testid="mcp-call"]')!.textContent).toContain('denied'), r);
    await r.act(() => tab(r, /Events/).click());
    await waitFor(() => expect(r.$('[data-testid="run-event"]')!.textContent).toContain('workflow.started'), r);
  });

  it('says so when the task never ran', async () => {
    const s = setup({ runExplain: async () => ({ runs: [], executions: [], checkpoints: [], mcpCalls: [], events: [] }), taskGet: async () => ({ task: { id: 't1', seq: 1, title: 'x' } }) as never });
    const r = mount(<RunScreen taskId="t1" />, s);
    await waitFor(() => expect(r.container.textContent).toContain('This task has not run yet.'), r);
    expect(r.$('[data-testid="workflow-graph"]')).toBeNull();
  });

  it('shows the error when the run cannot be explained', async () => {
    const s = setup({
      runExplain: async () => {
        throw new Error('there is no run');
      },
    });
    const r = mount(<RunScreen taskId="t1" />, s);
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('Could not explain the run: there is no run'), r);
  });

  it('draws the graph as SVG: node states, edges with arrowheads, dashed loops and their labels', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup());
    await ready(r);
    const graph = r.$('[data-testid="workflow-graph"]')!;
    expect(graph.querySelector('svg')).toBeTruthy();
    expect(node(r, 'prepare')!.getAttribute('data-status')).toBe('done');
    expect(node(r, 'work')!.getAttribute('data-status')).toBe('done');
    expect(node(r, 'work')!.textContent).toContain('fake-1');
    expect(node(r, 'work')!.textContent).toContain('$0.02');
    expect(node(r, 'approve')!.getAttribute('data-status')).toBe('waiting');
    expect(node(r, 'approve')!.textContent).toContain('waiting for you');
    expect(node(r, 'done')!.getAttribute('data-status')).toBe('pending');
    const edges = all(r, '[data-testid="wf-edge"]');
    expect(edges.length).toBe(7);
    expect(graph.querySelectorAll('.react-flow__edge').length).toBe(7);
    expect(edges.every((e) => /^url\(#wf-arrow-/.test(e.querySelector('path')!.getAttribute('marker-end') ?? ''))).toBe(true);
    const loop = edges.find((e) => e.getAttribute('data-kind') === 'loop')!;
    expect(getComputedStyle(loop.querySelector('path')!).strokeDasharray).not.toBe('none');
    expect(graph.textContent).toContain('loop ≤ 5 when fail');
    expect(graph.textContent).toContain('when pass');
    // prepare -> work ran, approve -> done did not
    expect(edges.find((e) => e.getAttribute('data-from') === 'prepare')!.getAttribute('data-taken')).toBe('true');
    expect(edges.find((e) => e.getAttribute('data-from') === 'approve')!.getAttribute('data-taken')).toBe('false');
    expect(r.$('[data-testid="wf-canvas"]')!.getAttribute('aria-label')).toBe('Workflow iuv');
  });

  it('shows the run as of a checkpoint and back to the latest', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup());
    await ready(r);
    const cps = all(r, '[data-testid="wf-checkpoint"]');
    expect(cps.map((c) => c.textContent)).toEqual(['0', '2 (latest)']);
    expect(r.container.textContent).toContain('Latest state. Choose a checkpoint below');
    await r.act(() => (cps[0] as HTMLButtonElement).click());
    await waitFor(() => expect(node(r, 'work')!.getAttribute('data-status')).toBe('pending'), r);
    expect(r.$('[data-testid="workflow-graph"]')!.textContent).toMatch(/As of checkpoint at step 0 \(entry,/);
    expect(all(r, '[data-testid="wf-checkpoint"]')[0]!.getAttribute('aria-pressed')).toBe('true');
    await r.act(() => byText(r.$('[data-testid="workflow-graph"]')!, 'button', 'Show the latest').click());
    await waitFor(() => expect(node(r, 'work')!.getAttribute('data-status')).toBe('done'), r);

    // the Checkpoints tab can show a checkpoint on the graph too
    await r.act(() => tab(r, /Checkpoints/).click());
    await waitFor(() => r.$('[data-testid="checkpoint"]')!, r);
    await r.act(() => byText(all(r, '[data-testid="checkpoint"]')[0]!, 'button', 'Show on the graph').click());
    await waitFor(() => expect(node(r, 'work')!.getAttribute('data-status')).toBe('pending'), r);
    expect(all(r, '[data-testid="checkpoint"]')[0]!.getAttribute('aria-current')).toBe('true');
  });

  it('selects a node: the side panel shows what it did, why that model, its prompt and its turns', async () => {
    const explains: string[] = [];
    const prompts: unknown[][] = [];
    const s = setup({
      routeExplain: async (id: string) => {
        explains.push(id);
        return { text: 'The **static** assignment of the agent.', decision: { DecidedBy: 'static', Alternatives: [] } } as never;
      },
      runPrompt: async (...a: unknown[]) => {
        prompts.push(a);
        return 'Implement Subtract.';
      },
    });
    const r = mount(<RunScreen taskId="t1" />, s);
    await ready(r);
    expect(r.$('[data-testid="node-panel-empty"]')).toBeTruthy();
    await r.act(() => node(r, 'work')!.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    const panel = await waitFor(() => r.$('[data-testid="node-panel"][data-node="work"]')!, r);
    expect(node(r, 'work')!.getAttribute('aria-pressed')).toBe('true');
    expect(panel.textContent).toContain('Worker One');
    expect(panel.textContent).toContain('fake-1');
    expect(panel.textContent).toContain('team setup');
    expect(panel.textContent).toContain('$0.02');
    // edges touching the selection are highlighted
    expect(all(r, '[data-testid="wf-edge"][data-active="true"]').length).toBe(4);

    await r.act(() => byText(panel, 'button', 'Explain why this model').click());
    await waitFor(() => expect(panel.querySelector('[data-testid="explain"]')!.textContent).toContain('assignment'), r);
    expect(explains).toEqual(['dec_1']);

    await r.act(() => byText(panel, 'button', 'Show the exact prompt').click());
    await waitFor(() => expect(panel.querySelector('[data-testid="node-prompt-text"]')!.textContent).toBe('Implement Subtract.'), r);
    expect(prompts).toEqual([['t1', 'run_1', 'work', 2, 1]]);

    await r.act(() => byText(panel, 'button', 'Open its stream in the Logs tab').click());
    expect(s.ui.dock).toMatchObject({ open: true, tab: 'logs' });

    // its turn opens in the Turns tab
    await r.act(() => byText(panel, 'button', 'Open the turn at step 2').click());
    await waitFor(() => {
      const li = r.$('[data-testid="turn"][data-node="work"]')!;
      expect(li.querySelector('button')!.getAttribute('aria-expanded')).toBe('true');
      expect(li.getAttribute('data-selected')).toBe('true');
    }, r);

    // a connection selects the neighbour; Close clears the selection
    await r.act(() => byText(panel, 'button', 'Select prepare').click());
    await waitFor(() => r.$('[data-testid="node-panel"][data-node="prepare"]')!, r);
    await r.act(() => byText(r.$('[data-testid="node-panel"]')!, 'button', 'Close the node details').click());
    await waitFor(() => r.$('[data-testid="node-panel-empty"]')!, r);
    expect(all(r, '[data-testid="wf-node"][aria-pressed="true"]').length).toBe(0);
  });

  it('says what a node that has not run is waiting for', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup());
    await ready(r);
    await r.act(() => node(r, 'approve')!.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    const panel = await waitFor(() => r.$('[data-testid="node-panel"][data-node="approve"]')!, r);
    expect(panel.textContent).toContain('This node is waiting for you.');
    expect(panel.textContent).toContain('human');
  });

  it('moves between nodes with the arrow keys, selects with Enter and clears with Escape', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup());
    await ready(r);
    const first = node(r, 'prepare')!;
    expect(first.getAttribute('tabindex')).toBe('0');
    expect(node(r, 'work')!.getAttribute('tabindex')).toBe('-1');
    first.focus();
    expect(document.activeElement).toBe(first);
    await r.act(() => key(first, 'ArrowRight'));
    await waitFor(() => expect((document.activeElement as Element | null)?.getAttribute('data-node')).toBe('work'), r);
    expect(node(r, 'work')!.getAttribute('tabindex')).toBe('0');
    await r.act(() => key(node(r, 'work')!, 'Enter'));
    await waitFor(() => r.$('[data-testid="node-panel"][data-node="work"]')!, r);
    await r.act(() => key(node(r, 'work')!, 'Escape'));
    await waitFor(() => r.$('[data-testid="node-panel-empty"]')!, r);
    await r.act(() => key(node(r, 'work')!, ' '));
    await waitFor(() => r.$('[data-testid="node-panel"][data-node="work"]')!, r);
  });

  it('zooms with the buttons and the wheel, pans by dragging, and fits the graph back', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup());
    await ready(r);
    const vp = () => r.$('[data-testid="wf-viewport"]')!.getAttribute('transform')!;
    const zoom = () => r.$('[data-testid="wf-zoom"]')!.textContent!;
    const canvas = r.$('[data-testid="wf-canvas"]') as HTMLElement;
    const fitted = vp();
    const fittedZoom = zoom();
    await r.act(() => byText(r.container, 'button', 'Zoom in').click());
    await waitFor(() => expect(zoom()).not.toBe(fittedZoom), r);
    expect(parseInt(zoom(), 10)).toBeGreaterThan(parseInt(fittedZoom, 10));
    expect(byText(r.container, 'button', 'Fit to view').getAttribute('aria-pressed')).toBe('false');
    await r.act(() => byText(r.container, 'button', 'Zoom out').click());
    await r.act(() => byText(r.container, 'button', 'Zoom out').click());
    await waitFor(() => expect(parseInt(zoom(), 10)).toBeLessThan(parseInt(fittedZoom, 10)), r);

    await r.act(() => byText(r.container, 'button', 'Fit to view').click());
    await waitFor(() => expect(vp()).toBe(fitted), r);

    const box = canvas.getBoundingClientRect();
    await r.act(() => canvas.dispatchEvent(new WheelEvent('wheel', { deltaY: -200, clientX: box.left + 10, clientY: box.top + 10, bubbles: true, cancelable: true })));
    await waitFor(() => expect(parseInt(zoom(), 10)).toBeGreaterThan(parseInt(fittedZoom, 10)), r);

    await r.act(() => byText(r.container, 'button', 'Fit to view').click());
    await waitFor(() => expect(vp()).toBe(fitted), r);
    const at = (type: string, x: number, y: number) => canvas.dispatchEvent(new PointerEvent(type, { clientX: x, clientY: y, button: 0, pointerId: 1, bubbles: true }));
    await r.act(() => {
      at('pointerdown', box.left + 5, box.top + 5);
      at('pointermove', box.left + 65, box.top + 45);
      at('pointerup', box.left + 65, box.top + 45);
    });
    await waitFor(() => expect(vp()).not.toBe(fitted), r);
    const before = /translate\(([-\d.]+) ([-\d.]+)\)/.exec(fitted)!;
    const after = /translate\(([-\d.]+) ([-\d.]+)\)/.exec(vp())!;
    expect(Number(after[1]) - Number(before[1])).toBeCloseTo(60, 0);
    expect(Number(after[2]) - Number(before[2])).toBeCloseTo(40, 0);

    // keyboard zoom on a focused node
    const z = zoom();
    await r.act(() => key(node(r, 'prepare')!, '+'));
    await waitFor(() => expect(zoom()).not.toBe(z), r);
  });

  it('a click on the empty canvas clears the selection', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup());
    await ready(r);
    await r.act(() => node(r, 'work')!.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    await waitFor(() => r.$('[data-testid="node-panel"]')!, r);
    const canvas = r.$('[data-testid="wf-canvas"]') as HTMLElement;
    const box = canvas.getBoundingClientRect();
    await r.act(() => {
      canvas.dispatchEvent(new PointerEvent('pointerdown', { clientX: box.left + 3, clientY: box.top + 3, button: 0, pointerId: 1, bubbles: true }));
      canvas.dispatchEvent(new PointerEvent('pointerup', { clientX: box.left + 3, clientY: box.top + 3, button: 0, pointerId: 1, bubbles: true }));
    });
    await waitFor(() => r.$('[data-testid="node-panel-empty"]')!, r);
  });

  it('"Every turn in detail" opens the Turns tab', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup());
    await ready(r);
    await r.act(() => tab(r, /Events/).click());
    await waitFor(() => expect(tab(r, /Events/).getAttribute('aria-selected')).toBe('true'), r);
    await r.act(() => byText(r.container, 'button', 'Every turn in detail').click());
    await waitFor(() => expect(tab(r, /Turns/).getAttribute('aria-selected')).toBe('true'), r);
    expect(all(r, '[data-testid="turn"]').length).toBe(2);
  });

  it('says why there is no graph when the workflow is not registered, and still lists the turns', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup({ runExplain: async () => ({ ...explanation(), graph: undefined }) }));
    await waitFor(() => expect(r.container.textContent).toContain('The workflow "iuv" is not registered in this build'), r);
    expect(r.$('[data-testid="workflow-graph"]')).toBeNull();
    expect(all(r, '[data-testid="turn"]').length).toBe(2);
  });

  it('links the other runs of the task', async () => {
    const x = explanation();
    x.runs = [
      { id: 'run_0', workflow: 'iuv', status: 'FAILED', created: 0 },
      { id: 'run_1', workflow: 'iuv', status: 'WAITING_HUMAN', created: 1 },
    ];
    const asked: unknown[][] = [];
    const r = mount(
      <RunScreen taskId="t1" runId="run_1" />,
      setup({
        runExplain: async (...a: unknown[]) => {
          asked.push(a);
          return x;
        },
      }),
    );
    await waitFor(() => expect(r.$('[data-testid="run-history"]')!.textContent).toContain('This task ran 2 times'), r);
    expect(r.$('[data-testid="run-history"] a')!.getAttribute('href')).toBe('/task/t1/run/run_0');
    expect(r.$('[data-testid="run-history"] [aria-current="page"]')).toBeTruthy();
    expect(asked[0]).toEqual(['t1', 'run_1']);
  });

  it('re-reads the explanation when an event for the task arrives', async () => {
    let x = explanation();
    const s = setup({ runExplain: async () => x });
    const r = mount(<RunScreen taskId="t1" />, s);
    await ready(r);
    x = explanation();
    x.executions.push(ex('verify', 3, 'failed', 1, { error: 'tests failed' }));
    s.emit({ type: 'workflow.node_finished', taskId: 't1' });
    await waitFor(() => expect(node(r, 'verify')!.getAttribute('data-status')).toBe('failed'), r, 3000);
  });
});
