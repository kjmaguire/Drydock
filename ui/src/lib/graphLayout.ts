import type { ExecView, GraphView } from '../api/runClient';

export interface Placed {
  id: string;
  column: number;
  row: number;
}

/**
 * A deterministic left-to-right layered layout (docs/ui.md 6.2): a node's column is the longest path to it from an entry
 * node, ignoring loop edges (which point back); rows follow declaration order within a column. The same graph always gets
 * the same positions, so a re-render never moves a node.
 */
export function layout(g: GraphView): Placed[] {
  const ids = g.nodes.map((n) => n.id);
  const depth = new Map<string, number>();
  for (const e of g.entry) depth.set(e, 0);
  if (depth.size === 0 && ids.length) depth.set(ids[0]!, 0);
  const forward = g.edges.filter((e) => e.kind !== 'loop' && e.from !== e.to);
  for (let i = 0; i < ids.length; i++) {
    let changed = false;
    for (const e of forward) {
      const d = depth.get(e.from);
      if (d === undefined) continue;
      if ((depth.get(e.to) ?? -1) < d + 1 && d + 1 < ids.length) {
        depth.set(e.to, d + 1);
        changed = true;
      }
    }
    if (!changed) break;
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
