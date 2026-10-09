import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { ExecView, GraphView, RunExplanation } from '../../api/runClient.ts';
import { layout, nodeStates } from '../../lib/graphLayout.ts';
import { routes } from '../../routes.ts';
import {
  clip,
  compact,
  connections,
  duration,
  edgeLabel,
  fitView,
  formatBytes,
  graphGeometry,
  MAX_ZOOM,
  MIN_ZOOM,
  neighbor,
  NODE_H,
  NODE_W,
  runCost,
  statesAsOf,
  statusLine,
  viewTransform,
  zoomAt,
} from './run-model.ts';

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

describe('the workflow layout', () => {
  it('puts each node in the column of its longest path from the entry, ignoring loops, and is stable', () => {
    const p = layout(g);
    const col = Object.fromEntries(p.map((n) => [n.id, n.column]));
    expect(col).toEqual({ prepare: 0, work: 1, verify: 2, review: 3, approve: 4, done: 5 });
    expect(layout(g)).toEqual(p);
    // a node nothing reaches goes after the rest
    const lone = layout({ ...g, nodes: [...g.nodes, { id: 'orphan', kind: 'check' }] }).find((n) => n.id === 'orphan');
    expect(lone?.column).toBe(6);
  });

  it('derives node states from executions, as of a step, and marks the nodes waiting on a human', () => {
    const execs = [ex('prepare', 1), ex('work', 2), ex('verify', 3, 'failed'), ex('work', 4, 'done', 2), ex('verify', 5)];
    const s = nodeStates(execs);
    expect(s.get('work')).toMatchObject({ status: 'done', runs: 2, attempts: 2 });
    expect(s.get('verify')?.status).toBe('done');
    const early = nodeStates(execs, 3, ['approve']);
    expect(early.get('verify')?.status).toBe('failed');
    expect(early.get('work')?.runs).toBe(1);
    expect(early.get('approve')?.status).toBe('waiting');
  });

  it('uses the latest checkpoint for waiting nodes unless a checkpoint is chosen', () => {
    const x = {
      executions: [ex('prepare', 1), ex('work', 2)],
      checkpoints: [
        { id: 'cp1', step: 0, kind: 'entry', created: 1, status: 'RUNNING' },
        { id: 'cp2', step: 2, kind: 'step', created: 2, status: 'WAITING_HUMAN', latest: true, waiting: ['approve'] },
      ],
    } as unknown as RunExplanation;
    expect(statesAsOf(x, undefined).get('approve')?.status).toBe('waiting');
    expect(statesAsOf(x, undefined).get('work')?.status).toBe('done');
    const first = statesAsOf(x, x.checkpoints[0]);
    expect(first.get('work')).toBeUndefined();
    expect(first.get('approve')).toBeUndefined();
  });
});

describe('the run route', () => {
  it('links a task run, with and without a run id', () => {
    expect(routes.run.href({ id: 't1' })).toBe('/task/t1/run');
    expect(routes.run.href({ id: 't1', runId: 'run_2' })).toBe('/task/t1/run/run_2');
  });
});

describe('the graph geometry', () => {
  it('places nodes by column and row, centring short columns, and is deterministic', () => {
    const geo = graphGeometry({ ...g, nodes: [...g.nodes, { id: 'side', kind: 'check' }], edges: [...g.edges, { from: 'prepare', to: 'side', kind: 'direct' }] });
    const at = Object.fromEntries(geo.nodes.map((n) => [n.id, n]));
    expect(at.prepare!.x).toBe(0);
    expect(at.work!.x).toBeGreaterThan(at.prepare!.x + NODE_W);
    expect(at.side!.x).toBe(at.work!.x);
    expect(at.side!.y).toBeGreaterThan(at.work!.y + NODE_H);
    // prepare is alone in its column: centred between the two rows of column 1
    expect(at.prepare!.y).toBe((at.work!.y + at.side!.y) / 2);
    expect(graphGeometry(g)).toEqual(graphGeometry(g));
  });

  it('routes forward edges as curves between node sides and back edges as arcs under the nodes', () => {
    const geo = graphGeometry(g);
    const at = Object.fromEntries(geo.nodes.map((n) => [n.id, n]));
    const fwd = geo.edges.find((e) => e.edge.from === 'prepare')!;
    expect(fwd.back).toBe(false);
    expect(fwd.d.startsWith(`M ${at.prepare!.x + NODE_W} ${at.prepare!.y + NODE_H / 2} C`)).toBe(true);
    expect(fwd.d.endsWith(`${at.work!.x} ${at.work!.y + NODE_H / 2}`)).toBe(true);
    const loops = geo.edges.filter((e) => e.back);
    expect(loops.map((e) => e.label)).toEqual(['loop ≤ 5 when fail', 'loop when revise']);
    // both loops dip below the nodes; the longer one deeper, and the bounds include it
    expect(loops[1]!.ly).toBeGreaterThan(loops[0]!.ly);
    expect(geo.bounds.maxY).toBeGreaterThan(NODE_H + 30);
    expect(geo.edges.length).toBe(g.edges.length);
  });

  it('draws a self-loop above its node and skips edges to unknown nodes', () => {
    const geo = graphGeometry({ name: 's', entry: ['a'], nodes: [{ id: 'a', kind: 'check' }], edges: [{ from: 'a', to: 'a', kind: 'loop' }, { from: 'a', to: 'ghost', kind: 'direct' }] });
    expect(geo.edges.length).toBe(1);
    expect(geo.edges[0]!.ly).toBeLessThan(0);
    expect(geo.bounds.minY).toBeLessThan(0);
  });

  it('labels edges with loop bounds, conditions and fan-out / fan-in', () => {
    expect(edgeLabel({ from: 'a', to: 'b', kind: 'loop', maxIter: 3, when: 'fail' })).toBe('loop ≤ 3 when fail');
    expect(edgeLabel({ from: 'a', to: 'b', kind: 'loop' })).toBe('loop');
    expect(edgeLabel({ from: 'a', to: 'b', kind: 'conditional', when: 'pass' })).toBe('when pass');
    expect(edgeLabel({ from: 'a', to: 'b', kind: 'fanout' })).toBe('fan-out');
    expect(edgeLabel({ from: 'a', to: 'b', kind: 'fanin' })).toBe('fan-in');
    expect(edgeLabel({ from: 'a', to: 'b', kind: 'direct' })).toBeUndefined();
  });
});

describe('pan, zoom and keyboard moves', () => {
  it('fits the whole graph in the canvas, centred, never past 1.1x', () => {
    const b = { minX: 0, minY: 0, maxX: 1000, maxY: 200 };
    const v = fitView(b, 540, 400, 20);
    expect(v.k).toBe(0.5);
    expect(v.x).toBe(20);
    expect(v.y).toBe(150);
    expect(fitView({ minX: 0, minY: 0, maxX: 100, maxY: 50 }, 2000, 2000).k).toBe(1.1);
    expect(fitView(b, 0, 0).k).toBe(1);
  });

  it('zooms about a point and clamps the zoom', () => {
    const v = zoomAt({ x: 0, y: 0, k: 1 }, 2, 100, 100);
    expect(v).toEqual({ x: -100, y: -100, k: 2 });
    expect(zoomAt(v, 100, 0, 0).k).toBe(MAX_ZOOM);
    expect(zoomAt(v, 0.001, 0, 0).k).toBe(MIN_ZOOM);
    expect(viewTransform({ x: 1.234, y: 5, k: 0.5 })).toBe('translate(1.2 5) scale(0.5)');
  });

  it('moves to the nearest node in the direction of an arrow key', () => {
    const nodes = graphGeometry({ ...g, nodes: [...g.nodes, { id: 'side', kind: 'check' }], edges: [...g.edges, { from: 'prepare', to: 'side', kind: 'direct' }] }).nodes;
    expect(neighbor(nodes, 'prepare', 'ArrowRight')).toBe('work');
    expect(neighbor(nodes, 'work', 'ArrowDown')).toBe('side');
    expect(neighbor(nodes, 'side', 'ArrowUp')).toBe('work');
    expect(neighbor(nodes, 'work', 'ArrowLeft')).toBe('prepare');
    expect(neighbor(nodes, 'prepare', 'ArrowLeft')).toBeUndefined();
    expect(neighbor(nodes, 'missing', 'ArrowLeft')).toBe('prepare');
  });

  it('lists the edges into and out of a node', () => {
    const c = connections(g, 'work');
    expect(c.incoming.map((e) => e.from)).toEqual(['prepare', 'verify', 'review']);
    expect(c.outgoing.map((e) => e.to)).toEqual(['verify']);
  });
});

describe('formatting', () => {
  it('formats durations, sizes, event data and labels', () => {
    expect(duration(0)).toBe('');
    expect(duration(1000, 1500)).toBe('500 ms');
    expect(duration(1000, 3500)).toBe('2.5 s');
    expect(duration(1000, 1000 + 125_000)).toBe('2 min 5 s');
    expect(duration(1000, undefined, 1200)).toBe('200 ms');
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(2048)).toBe('2.0 KB');
    expect(formatBytes(3 << 20)).toBe('3.0 MB');
    expect(compact({ tool: 'x' })).toBe('{"tool":"x"}');
    expect(compact('a'.repeat(500)).length).toBe(401);
    expect(compact(undefined)).toBe('');
    expect(clip('abcdef', 4)).toBe('abc…');
    expect(clip('abc', 4)).toBe('abc');
    expect(statusLine({ status: 'done', attempts: 2, runs: 3 })).toBe('done · attempt 2 · ran 3×');
    expect(statusLine({ status: 'waiting', attempts: 0, runs: 0 })).toBe('waiting for you');
  });

  it('sums the cost of every turn', () => {
    const u = (costUsd: number) => ({ costUsd, inputTokens: 1, outputTokens: 1 }) as never;
    expect(runCost({ executions: [ex('a', 1, 'done', 1, { usage: [u(0.01), u(0.02)] }), ex('b', 2)] } as unknown as RunExplanation)).toBeCloseTo(0.03, 6);
  });
});
