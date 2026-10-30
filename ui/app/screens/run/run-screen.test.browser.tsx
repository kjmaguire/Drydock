import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ExecView, GraphView, RunExplanation } from '../../api/runClient.ts';
import { byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { CHAT_TEXT_MAX, tryInChat } from './turns.tsx';
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
  events: [
    { seq: 1, ts: 1, type: 'workflow.started', data: { workflow: 'iterate-until-verified', note: '<img src=x onerror=alert(1)>' } },
    { seq: 2, ts: 2, type: 'workflow.node_started', agentId: 'agt_w', data: { node: 'work' } },
    { seq: 3, ts: 3, type: 'workflow.request_raised', data: { node: 'approve' } },
  ],
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
    // the status is said once, in words, in the header
    expect(r.$('[data-testid="run-status"]')!.textContent).toBe('Waiting for you');
    expect(r.container.textContent!.match(/Waiting for you/g)!.length).toBeGreaterThanOrEqual(1);
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
    // the inspector (P91): words filter by type, agent or payload; a row opens to its payload, as text
    const filter = r.$('[aria-label="Filter events"]') as HTMLInputElement;
    const types = () => all(r, '[data-testid="run-event"]').map((e) => e.getAttribute('data-type'));
    await r.act(() => type(filter, 'worker one'));
    await waitFor(() => expect(types()).toEqual(['workflow.node_started']), r);
    await r.act(() => type(filter, 'approve'));
    await waitFor(() => expect(types()).toEqual(['workflow.request_raised']), r);
    expect(r.$('[data-testid="run-events-count"]')!.textContent).toBe('1 of 3 events');
    await r.act(() => type(filter, ''));
    await waitFor(() => expect(types()).toHaveLength(3), r);
    const first = all(r, '[data-testid="run-event"]')[0]!;
    await r.act(() => byText(first, 'button', 'Payload').click());
    const payload = await waitFor(() => r.$('[data-testid="run-event-payload"]')!, r);
    expect(JSON.parse(payload.querySelector('pre')!.textContent!)).toEqual({ workflow: 'iterate-until-verified', note: '<img src=x onerror=alert(1)>' });
    expect(r.container.querySelector('img')).toBeNull();
    await r.act(() => byText(first, 'button', 'Hide').click());
    await waitFor(() => expect(r.$('[data-testid="run-event-payload"]')).toBeNull(), r);
  });

  it('offers Try in a chat on an agent turn, which sends its exact prompt to a new chat of the same agent (P91)', async () => {
    const created: unknown[] = [];
    const sent: unknown[] = [];
    const client = {
      runPrompt: async () => 'Implement Subtract.',
      chatCreate: async (p: unknown) => (created.push(p), { id: 'chat_9' }),
      chatSend: async (id: string, text: string) => void sent.push([id, text]),
    };
    const exec = { nodeId: 'work', step: 2, attempt: 1, status: 'done', started: 1 } as ExecView;
    const id = await tryInChat(client, { taskId: 't1', runId: 'run_1', exec, projectId: 'p1', agent: { adapter: 'claude', model: 'sonnet', name: 'Worker One' } });
    expect(id).toBe('chat_9');
    expect(created).toEqual([{ projectId: 'p1', adapter: 'claude', model: 'sonnet', title: 'Try: work turn of Worker One' }]);
    expect(sent).toEqual([['chat_9', 'Implement Subtract.']]);
    // a prompt longer than a chat message takes is refused before any chat is made
    const big = { ...client, runPrompt: async () => 'x'.repeat(CHAT_TEXT_MAX + 1) };
    let err = '';
    try {
      await tryInChat(big, { taskId: 't1', runId: 'run_1', exec, projectId: 'p1', agent: { adapter: 'claude', model: '', name: 'W' } });
    } catch (e) {
      err = String(e);
    }
    expect(err).toContain('cannot be tried in a chat');
    expect(created).toHaveLength(1);

    // the button is on an agent's turn of a known agent
    const r = mount(<RunScreen taskId="t1" />, setup({ agentList: async () => [{ id: 'agt_w', name: 'Worker One', role: 'backend', adapter: 'claude', model: 'sonnet' } as never] }));
    await ready(r);
    await r.act(() => tab(r, /Turns/).click());
    const work = await waitFor(() => all(r, '[data-testid="turn"]').find((li) => li.getAttribute('data-node') === 'work')!, r);
    await r.act(() => work.querySelector('button')!.click());
    await waitFor(() => expect(byText(work, 'button', 'Try in a chat')).toBeTruthy(), r);
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
    expect(graph.querySelectorAll('[data-testid="wf-edge"]').length).toBe(7);
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
    // it opens on the node the run is at (UI review P1-7): here the approval waiting for you
    await waitFor(() => r.$('[data-testid="node-panel"][data-node="approve"]')!, r);
    await r.act(() => node(r, 'work')!.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    const panel = await waitFor(() => r.$('[data-testid="node-panel"][data-node="work"]')!, r);
    expect(node(r, 'work')!.getAttribute('aria-pressed')).toBe('true');
    expect(panel.textContent).toContain('Worker One');
    expect(panel.textContent).toContain('fake-1');
    expect(panel.textContent).toContain('team setup');
    expect(panel.textContent).toContain('$0.02');
    // edges touching the selection are highlighted, and only theirs show their conditions as pills, over the nodes
    expect(all(r, '[data-testid="wf-edge"][data-active="true"]').length).toBe(4);
    const pills = all(r, '[data-testid="wf-label"]').map((l) => l.textContent);
    const labelled = all(r, '[data-testid="wf-edge"][data-active="true"]').map((e) => e.querySelector('title')?.textContent).filter(Boolean);
    expect(pills.sort()).toEqual([...labelled].sort() as string[]);
    expect(all(r, '[data-testid="wf-edge"]').filter((e) => e.getAttribute('data-active') !== 'true' && e.querySelector('title')).length > 0).toBe(true);
    const lastNode = all(r, '[data-testid="wf-node"]').pop()!;
    for (const l of all(r, '[data-testid="wf-label"]')) expect(lastNode.compareDocumentPosition(l) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

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
    // the run waits there, so it is the node shown first
    const panel = await waitFor(() => r.$('[data-testid="node-panel"][data-node="approve"]')!, r);
    expect(node(r, 'approve')!.getAttribute('aria-pressed')).toBe('true');
    expect(panel.textContent).toContain('This node is waiting for you.');
    expect(panel.textContent).toContain('human');
  });

  it('moves between nodes with the arrow keys, selects with Enter and clears with Escape', async () => {
    const r = mount(<RunScreen taskId="t1" />, setup());
    await ready(r);
    // the node the run is at is selected, so Tab reaches it; Escape closes it and the first node takes the tab stop
    expect(node(r, 'approve')!.getAttribute('tabindex')).toBe('0');
    await r.act(() => key(node(r, 'approve')!, 'Escape'));
    await waitFor(() => r.$('[data-testid="node-panel-empty"]')!, r);
    const first = node(r, 'prepare')!;
    await waitFor(() => expect(first.getAttribute('tabindex')).toBe('0'), r);
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
    // it opens readable (UI review P1-7): at 70 % or more, not the whole graph squeezed small; Fit to view shows it all
    expect(parseInt(zoom(), 10)).toBeGreaterThan(69);
    expect(byText(r.container, 'button', 'Fit to view').getAttribute('aria-pressed')).toBe('false');
    await r.act(() => byText(r.container, 'button', 'Fit to view').click());
    expect(byText(r.container, 'button', 'Fit to view').getAttribute('aria-pressed')).toBe('true');
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

describe('RunScreen actions and the nested view (P123)', () => {
  const withActions = (over: Partial<RunExplanation> = {}): RunExplanation => {
    const x = explanation();
    x.checkpoints[0]!.next = ['prepare'];
    x.actions = {
      resume: false,
      timeTravel: true,
      replay: false,
      resumeWhy: 'The run is waiting for you: answer it on the task page.',
      replayWhy: 'Only a run that has ended is replayed; this one is waiting for you.',
      supersedes: 'run_1',
      supersedesStatus: 'WAITING_HUMAN',
      lease: { held: false, mine: false, driving: false },
    };
    return { ...x, ...over };
  };

  it('time travels from the chosen checkpoint after a confirmation that says what runs again, then opens the new run', async () => {
    const calls: unknown[][] = [];
    const went: string[] = [];
    const s = setup({
      runExplain: async () => withActions(),
      runTimeTravel: async (...a: unknown[]) => (calls.push(a), { runId: 'run_2', superseded: 'run_1', taskStatus: 'IN_PROGRESS' }),
    });
    const r = mount(<RunScreen taskId="t1" runId="run_1" onNavigate={(h) => went.push(h)} />, s);
    await ready(r);
    const bar = r.$('[data-testid="run-actions"]')!;
    expect((byText(bar, 'button', 'Resume') as HTMLButtonElement).disabled).toBe(true);
    expect((byText(bar, 'button', 'Replay from the start') as HTMLButtonElement).disabled).toBe(true);
    expect(r.$('[data-testid="run-actions-why"]')!.textContent).toContain('answer it on the task page');
    expect(r.$('[data-testid="run-actions-why"]')!.textContent).toContain('Only a run that has ended is replayed');
    expect(bar.textContent).toContain('To time travel, choose a checkpoint under the graph.');
    const graph = r.$('[data-testid="workflow-graph"]')!;
    expect(graph.textContent).not.toContain('Time travel from here');
    await r.act(() => (all(r, '[data-testid="wf-checkpoint"]')[0] as HTMLButtonElement).click());
    const travel = await waitFor(() => byText(graph, 'button', 'Time travel from here') as HTMLButtonElement, r);
    expect(travel.disabled).toBe(false);
    expect((byText(graph, 'button', 'Replay from here') as HTMLButtonElement).disabled).toBe(true);
    await r.act(() => travel.click());
    const dlg = await waitFor(() => r.$('[data-testid="rerun-confirm"]')!, r);
    expect(dlg.getAttribute('data-kind')).toBe('timeTravel');
    expect(r.$('dialog h2')!.textContent).toBe('Time travel to step 0?');
    const text = r.$('[data-testid="rerun-plan"]')!.textContent!;
    expect(text).toContain('Runs first: prepare.');
    expect(text).toContain('prepare and work (2 turns in this run)');
    expect(text).toContain('This run (waiting for you) is stopped first');
    expect(calls).toEqual([]);
    await r.act(() => byText(dlg, 'button', 'Start the new run').click());
    await waitFor(() => expect(went).toEqual(['/task/t1/run/run_2']), r);
    expect(calls).toEqual([['t1', 'run_1', 'cp1', false]]);
  });

  it('resumes a failed run after its confirmation and reads it again; a refusal is shown in the dialog', async () => {
    const x = withActions({
      run: { id: 'run_1', workflow: 'iuv', status: 'FAILED', created: 1, updated: 2, failedReason: 'node work: the provider was overloaded' },
      actions: { resume: true, timeTravel: true, replay: true, lease: { held: false, mine: false, driving: false } },
    });
    let reads = 0;
    let refuse = true;
    const resumed: unknown[][] = [];
    const s = setup({
      runExplain: async () => (reads++, x),
      runResume: async (...a: unknown[]) => {
        if (refuse) throw new Error('another Drydock is driving this run right now');
        resumed.push(a);
        return { runId: 'run_1', taskStatus: 'IN_PROGRESS' };
      },
    });
    const went: string[] = [];
    const r = mount(<RunScreen taskId="t1" runId="run_1" onNavigate={(h) => went.push(h)} />, s);
    await ready(r);
    expect(r.$('[data-testid="run-actions-why"]')).toBeNull();
    expect(r.$('[data-testid="run-lease"]')).toBeNull();
    await r.act(() => byText(r.$('[data-testid="run-actions"]')!, 'button', 'Resume').click());
    const dlg = await waitFor(() => r.$('[data-testid="rerun-confirm"]')!, r);
    expect(r.$('dialog h2')!.textContent).toBe('Resume this run?');
    expect(dlg.textContent).toContain('It stopped because: node work: the provider was overloaded');
    expect(dlg.textContent).toContain('a step you rejected is still rejected');
    await r.act(() => byText(dlg, 'button', 'Resume the run').click());
    await waitFor(() => expect(dlg.querySelector('[role="alert"]')!.textContent).toContain('It did not start: another Drydock is driving this run right now'), r);
    refuse = false;
    const before = reads;
    await r.act(() => byText(dlg, 'button', 'Resume the run').click());
    await waitFor(() => expect(r.$('[data-testid="rerun-confirm"]')).toBeNull(), r);
    expect(resumed).toEqual([['t1', 'run_1']]);
    await waitFor(() => expect(reads).toBeGreaterThan(before), r);
    expect(went).toEqual([]);
  });

  it('replays an ended run from the start, naming the steps that ask first, and says where a run came from', async () => {
    const x = withActions({
      run: { id: 'run_1', workflow: 'iuv', status: 'CANCELLED', created: 1, updated: 2 },
      graph: { ...g, nodes: g.nodes.map((n) => (n.id === 'done' ? { ...n, meta: { effect: 'merges the branch' } } : n)) },
      actions: { resume: false, resumeWhy: 'The run was cancelled.', timeTravel: true, replay: true, supersedes: 'run_9', supersedesStatus: 'PAUSED', lease: { held: false, mine: false, driving: false } },
      origin: { kind: 'fork', run: 'run_0', checkpoint: 'cpX', step: 1, by: 'human:kyle' },
    });
    const calls: unknown[][] = [];
    const went: string[] = [];
    const s = setup({ runExplain: async () => x, runReplay: async (...a: unknown[]) => (calls.push(a), { runId: 'run_3', superseded: 'run_9', taskStatus: 'IN_PROGRESS' }) });
    const r = mount(<RunScreen taskId="t1" runId="run_1" onNavigate={(h) => went.push(h)} />, s);
    await ready(r);
    const origin = r.$('[data-testid="run-origin"]')!;
    expect(origin.textContent).toContain('Time travel from step 1 of an earlier run');
    expect(origin.querySelector('a')!.getAttribute('href')).toBe('/task/t1/run/run_0');
    await r.act(() => byText(r.$('[data-testid="run-actions"]')!, 'button', 'Replay from the start').click());
    const dlg = await waitFor(() => r.$('[data-testid="rerun-confirm"]')!, r);
    expect(r.$('dialog h2')!.textContent).toBe('Replay this run from step 0?');
    expect(dlg.textContent).toContain('done (merges the branch)');
    expect(dlg.textContent).toContain("The task's current run (paused) is stopped first");
    await r.act(() => byText(dlg, 'button', 'Cancel').click());
    await waitFor(() => expect(r.$('[data-testid="rerun-confirm"]')).toBeNull(), r);
    expect(calls).toEqual([]);
    await r.act(() => byText(r.$('[data-testid="run-actions"]')!, 'button', 'Replay from the start').click());
    await r.act(() => byText(r.$('[data-testid="rerun-confirm"]')!, 'button', 'Start the replay').click());
    await waitFor(() => expect(went).toEqual(['/task/t1/run/run_3']), r);
    expect(calls).toEqual([['t1', 'run_1', undefined]]);
  });

  it("opens a subworkflow node into its own nodes, with their states from the nested run, and closes it again", async () => {
    const sub: GraphView = { name: 'inner', entry: ['draft'], nodes: [{ id: 'draft', kind: 'agent_task' }, { id: 'lint', kind: 'check' }], edges: [{ from: 'draft', to: 'lint', kind: 'direct' }] };
    const x = withActions({
      graph: {
        name: 'outer',
        entry: ['prepare'],
        nodes: [
          { id: 'prepare', kind: 'route' },
          { id: 'build', kind: 'subworkflow', sub },
          { id: 'work', kind: 'agent_task' },
        ],
        edges: [
          { from: 'prepare', to: 'build', kind: 'direct' },
          { from: 'build', to: 'work', kind: 'direct' },
        ],
      },
      executions: [ex('prepare', 1), ex('build', 2, 'failed'), ex('work', 3, 'done', 1, { agentId: 'agt_w' })],
      subruns: [{ nodeId: 'build', step: 2, attempt: 1, runId: 'run_1-build-2-1', status: 'FAILED', executions: [ex('draft', 1), ex('lint', 2, 'failed', 1, { error: 'lint failed' })], subruns: [] }],
    });
    const r = mount(<RunScreen taskId="t1" runId="run_1" />, setup({ runExplain: async () => x }));
    await ready(r);
    expect(node(r, 'build/draft')).toBeNull();
    // the panel opens on the node the run is at: the subworkflow that failed
    const panel = await waitFor(() => r.$('[data-testid="node-panel"][data-node="build"]')!, r);
    const section = panel.querySelector('[data-testid="node-subworkflow"]')!;
    expect(section.textContent).toContain('inner');
    expect(section.textContent).toContain('run_1-build-2-1');
    expect(section.textContent).toContain('failed');
    await r.act(() => byText(section as HTMLElement, 'button', 'Show its nodes in the graph').click());
    await waitFor(() => expect(node(r, 'build/lint')!.getAttribute('data-status')).toBe('failed'), r);
    expect(node(r, 'build/draft')!.getAttribute('data-status')).toBe('done');
    expect(node(r, 'build/draft')!.querySelector('.wf-title')!.textContent).toBe('draft');
    expect(r.$('[data-testid="wf-cluster"][data-node="build"]')!.textContent).toContain('build');
    expect(all(r, '[data-testid="wf-edge"]').some((e) => e.getAttribute('data-from') === 'build/lint' && e.getAttribute('data-to') === 'work')).toBe(true);
    // a nested node has its own panel
    await r.act(() => node(r, 'build/lint')!.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    const lint = await waitFor(() => r.$('[data-testid="node-panel"][data-node="build/lint"]')!, r);
    expect(lint.textContent).toContain('lint failed');
    // as of the checkpoint before build ran, the nested nodes had not run
    await r.act(() => (all(r, '[data-testid="wf-checkpoint"]')[0] as HTMLButtonElement).click());
    await waitFor(() => expect(node(r, 'build/lint')!.getAttribute('data-status')).toBe('pending'), r);
    await r.act(() => byText(r.$('[data-testid="workflow-graph"]')!, 'button', 'Show the latest').click());
    await r.act(() => node(r, 'build')!.dispatchEvent(new MouseEvent('click', { bubbles: true })));
    const again = await waitFor(() => r.$('[data-testid="node-panel"][data-node="build"]')!, r);
    await r.act(() => byText(again, 'button', 'Hide its nodes').click());
    await waitFor(() => expect(node(r, 'build/draft')).toBeNull(), r);
    expect(r.$('[data-testid="wf-cluster"]')).toBeNull();
  });

  it('marks a resumed checkpoint on the timeline', async () => {
    const x = withActions();
    x.checkpoints = [x.checkpoints[0]!, { id: 'cp3', step: 2, kind: 'entry', parent: 'cp2', created: 3, status: 'RUNNING', resumed: true, latest: true }];
    const r = mount(<RunScreen taskId="t1" runId="run_1" />, setup({ runExplain: async () => x }));
    await ready(r);
    const cps = all(r, '[data-testid="wf-checkpoint"]');
    expect(cps.map((c) => c.textContent)).toEqual(['0', '2 (resumed) (latest)']);
    expect(cps[1]!.getAttribute('data-kind')).toBe('resumed');
  });
});

describe("RunScreen: the worktree's files back, and an ended task run again (P124)", () => {
  const ref = 'refs/drydock/safe/t1/run_1/201';
  const withFiles = (over: Partial<RunExplanation> = {}): RunExplanation => {
    const x = explanation();
    x.checkpoints[0]!.safePoint = ref;
    x.executions[1]!.safePoint = ref;
    x.actions = { resume: false, timeTravel: true, replay: false, restore: true, runAgain: false, supersedes: 'run_1', supersedesStatus: 'WAITING_HUMAN', lease: { held: false, mine: false, driving: false } };
    return { ...x, ...over };
  };
  const preview = { ref, allowed: true, changes: [{ path: 'calc.go', change: 'changed' }, { path: 'junk.txt', change: 'removed' }] };

  it('time travels with the files put back when the box is ticked, listing the files that change first', async () => {
    const calls: unknown[][] = [];
    const asked: unknown[][] = [];
    const s = setup({
      runExplain: async () => withFiles(),
      runRestorePreview: async (...a: unknown[]) => (asked.push(a), preview),
      runTimeTravel: async (...a: unknown[]) => (calls.push(a), { runId: 'run_2', superseded: 'run_1', taskStatus: 'IN_PROGRESS' }),
    });
    const went: string[] = [];
    const r = mount(<RunScreen taskId="t1" runId="run_1" onNavigate={(h) => went.push(h)} />, s);
    await ready(r);
    await r.act(() => (all(r, '[data-testid="wf-checkpoint"]')[0] as HTMLButtonElement).click());
    await r.act(() => (byText(r.$('[data-testid="workflow-graph"]')!, 'button', 'Time travel from here') as HTMLButtonElement).click());
    const dlg = await waitFor(() => r.$('[data-testid="rerun-confirm"]')!, r);
    expect(r.$('[data-testid="rerun-plan"]')!.textContent).toContain("The worktree's files are not put back");
    const box = dlg.querySelector<HTMLInputElement>('[data-testid="rerun-files"] input[type="checkbox"]')!;
    expect(box.closest('label')!.textContent).toContain("Also put the worktree's files back to this point");
    await r.act(() => box.click());
    const files = await waitFor(() => r.$('[data-testid="restore-files"]')!, r);
    expect(asked).toEqual([['t1', 'run_1', ref]]);
    expect(files.textContent).toContain('calc.go');
    expect(files.textContent).toContain('is removed (it did not exist then)');
    expect(r.$('[data-testid="rerun-plan"]')!.textContent).toContain('go back to how they were at step 0 first (2 files change, listed below)');
    await r.act(() => byText(dlg, 'button', 'Start the new run').click());
    await waitFor(() => expect(went).toEqual(['/task/t1/run/run_2']), r);
    expect(calls).toEqual([['t1', 'run_1', 'cp1', true]]);
  });

  it('says when a checkpoint has no safe point, and keeps the start off while the files cannot be put back', async () => {
    const x = withFiles();
    x.checkpoints[1]!.safePoint = undefined;
    const s = setup({ runExplain: async () => x, runRestorePreview: async () => ({ ref, allowed: false, why: 'An agent turn is running in the task\'s worktree: wait for it to end.', changes: [] }) });
    const r = mount(<RunScreen taskId="t1" runId="run_1" />, s);
    await ready(r);
    await r.act(() => (all(r, '[data-testid="wf-checkpoint"]')[1] as HTMLButtonElement).click());
    await r.act(() => (byText(r.$('[data-testid="workflow-graph"]')!, 'button', 'Time travel from here') as HTMLButtonElement).click());
    let dlg = await waitFor(() => r.$('[data-testid="rerun-files"]')!, r);
    expect(dlg.textContent).toContain('no agent turn ran after it, so no safe point holds them');
    expect(dlg.querySelector('input[type="checkbox"]')).toBeNull();
    await r.act(() => byText(r.$('[data-testid="rerun-confirm"]')!, 'button', 'Cancel').click());
    await r.act(() => (all(r, '[data-testid="wf-checkpoint"]')[0] as HTMLButtonElement).click());
    await r.act(() => (byText(r.$('[data-testid="workflow-graph"]')!, 'button', 'Time travel from here') as HTMLButtonElement).click());
    dlg = await waitFor(() => r.$('[data-testid="rerun-files"]')!, r);
    await r.act(() => dlg.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
    await waitFor(() => expect(r.$('[data-testid="restore-why"]')!.textContent).toContain('An agent turn is running'), r);
    expect((byText(r.$('[data-testid="rerun-confirm"]')!, 'button', 'Start the new run') as HTMLButtonElement).disabled).toBe(true);
  });

  it('puts the files back to a chosen safe point after listing them, and undoes it', async () => {
    const restored: unknown[][] = [];
    const s = setup({
      runExplain: async () => withFiles(),
      runRestorePreview: async () => preview,
      runRestore: async (...a: unknown[]) => (restored.push(a), { ref: a[2] as string, undoRef: 'refs/drydock/safe/t1/undo/17', changed: 2 }),
    });
    const r = mount(<RunScreen taskId="t1" runId="run_1" />, s);
    await ready(r);
    await r.act(() => byText(r.$('[data-testid="run-actions"]')!, 'button', 'Put files back...').click());
    const dlg = await waitFor(() => r.$('[data-testid="restore-dialog"]')!, r);
    expect((byText(dlg, 'button', 'Put the files back') as HTMLButtonElement).disabled).toBe(true);
    const radio = dlg.querySelector<HTMLInputElement>('input[type="radio"]')!;
    expect(radio.closest('label')!.textContent).toBe('Before work at step 2');
    await r.act(() => radio.click());
    await waitFor(() => expect(r.$('[data-testid="restore-files"]')!.textContent).toContain('calc.go'), r);
    await r.act(() => byText(dlg, 'button', 'Put the files back').click());
    const done = await waitFor(() => r.$('[data-testid="restore-done"]')!, r);
    expect(done.textContent).toContain('Put back: 2 files changed. What was there before is kept as refs/drydock/safe/t1/undo/17.');
    expect(restored).toEqual([['t1', 'run_1', ref]]);
    await r.act(() => byText(r.$('[data-testid="restore-dialog"]')!, 'button', 'Undo the restore').click());
    await waitFor(() => expect(r.$('[data-testid="restore-done"]')!.textContent).toContain('Undone: the files are back as they were before the restore'), r);
    expect(restored).toEqual([
      ['t1', 'run_1', ref],
      ['t1', 'run_1', 'refs/drydock/safe/t1/undo/17'],
    ]);
  });

  it('lists the undo points earlier restores kept and restores one with the same preview, after the dialog that made it closed (P127)', async () => {
    const undo = 'refs/drydock/safe/t1/undo/1700000000000';
    const asked: unknown[][] = [];
    const restored: unknown[][] = [];
    const s = setup({
      // a run with no safe point of its own still offers the undo points of the task
      runExplain: async () => withFiles({ executions: explanation().executions, undoPoints: [{ ref: undo, at: 1700000000000, restored: 'refs/drydock/safe/t1/run_0/101' }] }),
      runRestorePreview: async (...a: unknown[]) => (asked.push(a), { ref: undo, allowed: true, changes: [{ path: 'calc.go', change: 'changed' }] }),
      runRestore: async (...a: unknown[]) => (restored.push(a), { ref: undo, undoRef: 'refs/drydock/safe/t1/undo/1700000000500', changed: 1 }),
    });
    const r = mount(<RunScreen taskId="t1" runId="run_1" />, s);
    await ready(r);
    await r.act(() => byText(r.$('[data-testid="run-actions"]')!, 'button', 'Put files back...').click());
    const dlg = await waitFor(() => r.$('[data-testid="restore-dialog"]')!, r);
    const list = dlg.querySelector('[data-testid="undo-points"]')!;
    expect(list.querySelector('legend')!.textContent).toBe('Undo an earlier restore');
    const radio = list.querySelector<HTMLInputElement>('input[type="radio"]')!;
    expect(radio.closest('label')!.textContent).toContain('As the files were before the restore of');
    expect(radio.closest('label')!.textContent).toContain('(to a safe point of another run)');
    await r.act(() => radio.click());
    await waitFor(() => expect(r.$('[data-testid="restore-files"]')!.textContent).toContain('calc.go'), r);
    expect(asked).toEqual([['t1', 'run_1', undo]]);
    await r.act(() => byText(dlg, 'button', 'Put the files back').click());
    await waitFor(() => expect(r.$('[data-testid="restore-done"]')!.textContent).toContain('refs/drydock/safe/t1/undo/1700000000500'), r);
    expect(restored).toEqual([['t1', 'run_1', undo]]);
  });

  it('runs a cancelled task again as a new task and opens it; a completed one says why not', async () => {
    const asked: unknown[][] = [];
    const went: string[] = [];
    const ended = (status: string, runAgain: boolean, runAgainWhy?: string) =>
      withFiles({
        run: { id: 'run_1', workflow: 'iuv', status, created: 1, updated: 2 },
        actions: { resume: false, timeTravel: false, replay: false, restore: false, restoreWhy: "The task's worktree is gone, so there is nothing to put back.", runAgain, runAgainWhy, lease: { held: false, mine: false, driving: false } },
      });
    const s = setup({
      runExplain: async () => ended('CANCELLED', true),
      taskGet: async () => ({ task: { id: 't1', seq: 3, title: 'Subtract', status: 'CANCELLED' } }) as never,
      taskRunAgain: async (...a: unknown[]) => (asked.push(a), { taskId: 't9', seq: 9, runId: 'run_9', from: 't1' }),
    });
    const r = mount(<RunScreen taskId="t1" runId="run_1" onNavigate={(h) => went.push(h)} />, s);
    await ready(r);
    const again = await waitFor(() => byText(r.$('[data-testid="run-actions"]')!, 'button', 'Run again as a new task') as HTMLButtonElement, r);
    expect(again.disabled).toBe(false);
    expect((byText(r.$('[data-testid="run-actions"]')!, 'button', 'Put files back...') as HTMLButtonElement).disabled).toBe(true);
    await r.act(() => again.click());
    const dlg = await waitFor(() => r.$('[data-testid="run-again-confirm"]')!, r);
    expect(r.$('dialog[open] h2')!.textContent).toBe('Run task #3 again?');
    expect(dlg.textContent).toContain('Task #3, which was cancelled, stays as it is');
    await r.act(() => byText(dlg, 'button', 'Run it again').click());
    await waitFor(() => expect(went).toEqual(['/task/t9']), r);
    expect(asked).toEqual([['t1']]);

    cleanup();
    const done = setup({
      runExplain: async () => ended('COMPLETED', false, 'Task #3 completed and its work was merged: more work on it is a new task.'),
      taskGet: async () => ({ task: { id: 't1', seq: 3, title: 'Subtract', status: 'COMPLETED' } }) as never,
    });
    const r2 = mount(<RunScreen taskId="t1" runId="run_1" />, done);
    await ready(r2);
    const off = await waitFor(() => byText(r2.$('[data-testid="run-actions"]')!, 'button', 'Run again as a new task') as HTMLButtonElement, r2);
    expect(off.disabled).toBe(true);
    expect(r2.$('[data-testid="run-actions-why"]')!.textContent).toContain('its work was merged');
  });
});
