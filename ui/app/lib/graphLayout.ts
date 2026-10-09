import type { ExecView, GraphView } from '../api/runClient.ts';

export interface Placed {
  id: string;
  column: number;
  row: number;
}

/**
 * A deterministic left-to-right layered layout (docs/ui.md 6.2): a node's column is the longest path to it from an entry
 * node over the forward edges; rows follow declaration order within a column. Loop edges point back, and so does any other
 * edge that closes a cycle (an escalation that resumes earlier work is a conditional edge, not a loop): a depth-first walk
 * from the entries finds those and leaves them out, so a cycle never stretches the graph. The same graph always gets the
 * same positions, so a re-render never moves a node.
 */
export function layout(g: GraphView): Placed[] {
  const ids = g.nodes.map((n) => n.id);
  const known = new Set(ids);
  const next = new Map<string, string[]>();
  for (const e of g.edges) {
    if (e.kind === 'loop' || e.from === e.to || !known.has(e.from) || !known.has(e.to)) continue;
    const list = next.get(e.from) ?? [];
    if (!list.includes(e.to)) list.push(e.to);
    next.set(e.from, list);
  }
  const roots = g.entry.filter((e) => known.has(e));
  if (roots.length === 0 && ids.length) roots.push(ids[0]!);
  // depth-first from the entries, in post-order: a forward edge goes to a node that finished earlier; an edge to a node that
  // finished later (still on the walk when it was met) points back and is dropped
  const seen = new Set<string>();
  const order: string[] = [];
  const walk = (id: string) => {
    seen.add(id);
    for (const to of next.get(id) ?? []) if (!seen.has(to)) walk(to);
    order.push(id);
  };
  for (const r of roots) if (!seen.has(r)) walk(r);
  const finished = new Map(order.map((id, i) => [id, i]));
  const forward = (from: string) => (next.get(from) ?? []).filter((to) => finished.get(to)! < finished.get(from)!);
  const depth = new Map<string, number>();
  for (const r of roots) depth.set(r, 0);
  for (let i = order.length - 1; i >= 0; i--) {
    const from = order[i]!;
    const d = depth.get(from);
    if (d === undefined) continue;
    for (const to of forward(from)) depth.set(to, Math.max(depth.get(to) ?? 0, d + 1));
  }
  const maxDepth = Math.max(0, ...depth.values());
  const rows = new Map<number, number>();
  return ids.map((id) => {
    const column = depth.get(id) ?? maxDepth + 1;
    const row = rows.get(column) ?? 0;
    rows.set(column, row + 1);
    return { id, column, row };
  });
}

export interface NodeState {
  status: 'pending' | 'started' | 'done' | 'failed' | 'abandoned' | 'waiting';
  attempts: number;
  runs: number;
  last?: ExecView;
}

/** What each node did, from its executions up to a step (the latest by default), and the nodes waiting on a human. */
export function nodeStates(execs: ExecView[], uptoStep?: number, waiting: string[] = []): Map<string, NodeState> {
  const out = new Map<string, NodeState>();
  for (const x of execs) {
    if (uptoStep !== undefined && x.step > uptoStep) continue;
    const s = out.get(x.nodeId) ?? { status: 'pending', attempts: 0, runs: 0 };
    s.runs++;
    s.attempts = Math.max(s.attempts, x.attempt);
    s.status = x.status as NodeState['status'];
    s.last = x;
    out.set(x.nodeId, s);
  }
  for (const w of waiting) {
    const s = out.get(w) ?? { status: 'pending', attempts: 0, runs: 0 };
    s.status = 'waiting';
    out.set(w, s);
  }
  return out;
}
