// Pure helpers for the run view: formatting, the node states shown on the graph, and the geometry of our own SVG graph
// (node boxes from the layered layout in lib/graphLayout.ts, smooth edge paths, pan/zoom transforms, keyboard moves).
// Framework-free so the Node test runner covers them.

import type { CheckpointView, ExecView, GraphEdge, GraphNode, GraphView, RunExplanation, SubrunView } from '../../api/runClient.ts';
import { layout, nodeStates, type NodeState } from '../../lib/graphLayout.ts';
import type { Tone } from '../../ui/tokens.ts';

// ---------- formatting ----------

export function duration(from: number, to?: number, now: number = Date.now()): string {
  if (!from) return '';
  const ms = (to || now) - from;
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

export function formatBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
}

/** Event data as one line of JSON, cut at 400 characters. */
export function compact(data: unknown): string {
  try {
    const s = JSON.stringify(data);
    if (s === undefined) return '';
    return s.length > 400 ? `${s.slice(0, 400)}…` : s;
  } catch {
    return '';
  }
}

/** Cut a label to n characters with an ellipsis (SVG text does not ellipsize). */
export function clip(s: string, n: number): string {
  return s.length > n ? `${s.slice(0, Math.max(1, n - 1))}…` : s;
}

/** The tone of one turn's status. */
export const EXEC_TONE: Record<string, Tone> = { done: 'ok', failed: 'err', started: 'info', abandoned: 'warn' };

/** The tone of a run's status (the header chip). */
export function runTone(status: string): Tone {
  if (status === 'COMPLETED') return 'ok';
  if (status === 'FAILED') return 'err';
  if (status === 'WAITING_HUMAN') return 'warn';
  return 'info';
}

export const KIND_LABEL: Record<string, string> = { agent_task: 'agent', check: 'check', request: 'human', route: 'route', subworkflow: 'subworkflow', group: 'group discussion' };
export const STATUS_TEXT: Record<NodeState['status'], string> = {
  pending: 'not run',
  started: 'running',
  done: 'done',
  failed: 'failed',
  abandoned: 'abandoned',
  waiting: 'waiting for you',
};
export const NODE_TONE: Record<NodeState['status'], Tone> = { pending: 'muted', started: 'info', done: 'ok', failed: 'err', abandoned: 'warn', waiting: 'warn' };

export const execKey = (e: Pick<ExecView, 'nodeId' | 'step' | 'attempt'>): string => `${e.nodeId}|${e.step}|${e.attempt}`;

export const usageCost = (e: Pick<ExecView, 'usage'> | undefined): number => (e?.usage ?? []).reduce((s, u) => s + u.costUsd, 0);
export const usageTokens = (e: Pick<ExecView, 'usage'>): number => (e.usage ?? []).reduce((s, u) => s + u.inputTokens + u.outputTokens, 0);
export const runCost = (x: RunExplanation): number => x.executions.reduce((s, e) => s + usageCost(e), 0);

/** What an edge says on the canvas: loop bounds, its condition, or fan-out / fan-in. */
export function edgeLabel(e: GraphEdge): string | undefined {
  if (e.kind === 'loop') return `loop${e.maxIter ? ` ≤ ${e.maxIter}` : ''}${e.when ? ` when ${e.when}` : ''}`;
  if (e.when) return `when ${e.when}`;
  if (e.kind === 'fanout') return 'fan-out';
  if (e.kind === 'fanin') return 'fan-in';
  return undefined;
}

/** The status line of a node: what it did, its attempts and how often it ran. */
export function statusLine(s: NodeState): string {
  return `${STATUS_TEXT[s.status]}${s.attempts > 1 ? ` · attempt ${s.attempts}` : ''}${s.runs > 1 ? ` · ran ${s.runs}×` : ''}`;
}

export const PENDING: NodeState = { status: 'pending', attempts: 0, runs: 0 };

/** The node states as of a checkpoint (or the latest), with the nodes waiting on a human at that point. */
export function statesAsOf(x: RunExplanation, cp: CheckpointView | undefined): Map<string, NodeState> {
  const latest = x.checkpoints.find((c) => c.latest);
  return nodeStates(x.executions, cp?.step, (cp ?? latest)?.waiting ?? []);
}

// ---------- geometry ----------

export const NODE_W = 180;
export const NODE_H = 68;
const COL_GAP = 64;
const ROW_GAP = 36;

export interface NodeBox {
  id: string;
  def: GraphNode;
  column: number;
  row: number;
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface EdgeGeom {
  id: string;
  edge: GraphEdge;
  /** The SVG path (a cubic Bézier). */
  d: string;
  /** Where the label sits (the curve's midpoint). */
  lx: number;
  ly: number;
  label?: string;
  /** Points back (a loop edge or any edge into an earlier column): drawn under the nodes. */
  back: boolean;
}

export interface Bounds {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

/** The frame drawn around an expanded subworkflow: its node and the nodes of its nested graph. */
export interface ClusterBox {
  id: string;
  label: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface GraphGeom {
  nodes: NodeBox[];
  edges: EdgeGeom[];
  bounds: Bounds;
  clusters: ClusterBox[];
}

const r1 = (n: number) => Math.round(n * 10) / 10;

/**
 * Place every node of the graph (columns from the layered layout, each column centred against the tallest) and route
 * every edge: forward edges as horizontal S-curves from the right side of the source to the left side of the target,
 * edges that point back as arcs under the nodes (longer spans dip deeper so nested loops do not cross), self-loops as a
 * small arc above the node. Deterministic: the same graph always gets the same picture.
 */
export function graphGeometry(g: GraphView, clusters: readonly Cluster[] = []): GraphGeom {
  const placed = layout(g);
  const perCol = new Map<number, number>();
  for (const p of placed) perCol.set(p.column, (perCol.get(p.column) ?? 0) + 1);
  const tallest = Math.max(1, ...perCol.values());
  const defs = new Map(g.nodes.map((n) => [n.id, n]));
  const nodes: NodeBox[] = placed.map((p) => {
    const offset = ((tallest - (perCol.get(p.column) ?? 1)) * (NODE_H + ROW_GAP)) / 2;
    return { id: p.id, def: defs.get(p.id)!, column: p.column, row: p.row, x: p.column * (NODE_W + COL_GAP), y: offset + p.row * (NODE_H + ROW_GAP), w: NODE_W, h: NODE_H };
  });
  const byId = new Map(nodes.map((n) => [n.id, n]));
  let minY = 0;
  let maxY = Math.max(NODE_H, ...nodes.map((n) => n.y + n.h));
  let backIndex = 0;
  const edges: EdgeGeom[] = [];
  g.edges.forEach((e, i) => {
    const a = byId.get(e.from);
    const b = byId.get(e.to);
    if (!a || !b) return;
    const id = `${e.from}->${e.to}-${i}`;
    const label = edgeLabel(e);
    if (a === b) {
      const x1 = a.x + a.w * 0.68;
      const x2 = a.x + a.w * 0.32;
      const top = a.y - 46;
      minY = Math.min(minY, top - 4);
      edges.push({ id, edge: e, d: `M ${r1(x1)} ${r1(a.y)} C ${r1(x1)} ${r1(top)}, ${r1(x2)} ${r1(top)}, ${r1(x2)} ${r1(a.y)}`, lx: r1(a.x + a.w / 2), ly: r1(a.y + 0.75 * (top - a.y)), label, back: true });
      return;
    }
    if (b.column > a.column) {
      const x1 = a.x + a.w;
      const y1 = a.y + a.h / 2;
      const x2 = b.x;
      const y2 = b.y + b.h / 2;
      const dx = Math.max(36, (x2 - x1) / 2);
      edges.push({ id, edge: e, d: `M ${r1(x1)} ${r1(y1)} C ${r1(x1 + dx)} ${r1(y1)}, ${r1(x2 - dx)} ${r1(y2)}, ${r1(x2)} ${r1(y2)}`, lx: r1((x1 + x2) / 2), ly: r1((y1 + y2) / 2), label, back: false });
      return;
    }
    // back: bottom centre of the source to the bottom centre of the target, under both
    const x1 = a.x + a.w / 2;
    const y1 = a.y + a.h;
    const x2 = b.x + b.w / 2;
    const y2 = b.y + b.h;
    const depth = 34 + Math.abs(x1 - x2) * 0.16 + (backIndex++ % 3) * 6;
    const low = Math.max(y1, y2) + depth;
    maxY = Math.max(maxY, low);
    edges.push({ id, edge: e, d: `M ${r1(x1)} ${r1(y1)} C ${r1(x1)} ${r1(low)}, ${r1(x2)} ${r1(low)}, ${r1(x2)} ${r1(y2)}`, lx: r1((x1 + x2) / 2), ly: r1((y1 + y2) / 8 + 0.75 * low), label, back: true });
  });
  let maxX = Math.max(NODE_W, ...nodes.map((n) => n.x + n.w));
  let minX = 0;
  // a frame around each expanded subworkflow, a nested one inside its parent's (the deeper, the closer to its nodes)
  const deepest = Math.max(0, ...clusters.map((c) => depthOf(c.id)));
  const boxes: ClusterBox[] = [];
  for (const c of clusters) {
    const inside = c.members.map((m) => byId.get(m)).filter((b): b is NodeBox => !!b);
    if (!inside.length) continue;
    const pad = 10 + 8 * (deepest - depthOf(c.id));
    const x0 = Math.min(...inside.map((b) => b.x)) - pad;
    const y0 = Math.min(...inside.map((b) => b.y)) - pad - 16;
    const x1 = Math.max(...inside.map((b) => b.x + b.w)) + pad;
    const y1 = Math.max(...inside.map((b) => b.y + b.h)) + pad;
    boxes.push({ id: c.id, label: c.label, x: x0, y: y0, w: x1 - x0, h: y1 - y0 });
    minX = Math.min(minX, x0);
    minY = Math.min(minY, y0);
    maxX = Math.max(maxX, x1);
    maxY = Math.max(maxY, y1);
  }
  return { nodes, edges, bounds: { minX, minY, maxX, maxY }, clusters: boxes };
}

const depthOf = (id: string): number => id.split(SUB_SEP).length - 1;

// ---------- pan and zoom ----------

export interface View {
  x: number;
  y: number;
  k: number;
}

export const MIN_ZOOM = 0.2;
export const MAX_ZOOM = 2.5;
const clampK = (k: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));

/** The view that shows the whole graph centred in a w×h canvas, never enlarged past 1.1×. */
export function fitView(b: Bounds, w: number, h: number, pad = 40): View {
  const bw = Math.max(1, b.maxX - b.minX);
  const bh = Math.max(1, b.maxY - b.minY);
  if (w <= 0 || h <= 0) return { x: pad - b.minX, y: pad - b.minY, k: 1 };
  const k = clampK(Math.min((w - 2 * pad) / bw, (h - 2 * pad) / bh, 1.1));
  return { x: r1((w - bw * k) / 2 - b.minX * k), y: r1((h - bh * k) / 2 - b.minY * k), k };
}

/** The least zoom the graph opens at, where a node's text reads (UI review P1-7: a long graph fitted whole opened at 27 %). */
export const READABLE_ZOOM = 0.7;

/**
 * The view a graph opens at: all of it when that fits at READABLE_ZOOM or more, else READABLE_ZOOM with the focus point (the
 * current node's centre) in the middle of the canvas; pan, zoom or "Fit to view" see the rest.
 */
export function readableView(b: Bounds, w: number, h: number, focus: { x: number; y: number } | undefined): View {
  const fit = fitView(b, w, h);
  if (w <= 0 || h <= 0 || fit.k >= READABLE_ZOOM || !focus) return fit;
  const k = READABLE_ZOOM;
  return { x: r1(w / 2 - focus.x * k), y: r1(h / 2 - focus.y * k), k };
}

/** The node a run is at: one waiting for you, else one running, else one that failed, else the last that ran, else the entry. */
export function currentNode(g: GraphView, states: Map<string, NodeState>): string | undefined {
  for (const want of ['waiting', 'started', 'failed'] as const) {
    const n = g.nodes.find((x) => states.get(x.id)?.status === want);
    if (n) return n.id;
  }
  const ran = g.nodes.filter((x) => (states.get(x.id)?.status ?? 'pending') !== 'pending');
  return ran.at(-1)?.id ?? g.entry[0] ?? g.nodes[0]?.id;
}

/** Zoom by a factor keeping the canvas point (cx, cy) still. */
export function zoomAt(v: View, factor: number, cx: number, cy: number): View {
  const k = clampK(v.k * factor);
  const f = k / v.k;
  return { x: cx - (cx - v.x) * f, y: cy - (cy - v.y) * f, k };
}

export const viewTransform = (v: View): string => `translate(${r1(v.x)} ${r1(v.y)}) scale(${Math.round(v.k * 1000) / 1000})`;

// ---------- keyboard ----------

export type Arrow = 'ArrowLeft' | 'ArrowRight' | 'ArrowUp' | 'ArrowDown';

/** The nearest node in the direction of an arrow key (along the axis first, off-axis distance weighs double). */
export function neighbor(nodes: readonly NodeBox[], fromId: string, key: Arrow): string | undefined {
  const from = nodes.find((n) => n.id === fromId);
  if (!from) return nodes[0]?.id;
  const cx = from.x + from.w / 2;
  const cy = from.y + from.h / 2;
  let best: { id: string; score: number } | undefined;
  for (const n of nodes) {
    if (n === from) continue;
    const dx = n.x + n.w / 2 - cx;
    const dy = n.y + n.h / 2 - cy;
    const along = key === 'ArrowRight' ? dx : key === 'ArrowLeft' ? -dx : key === 'ArrowDown' ? dy : -dy;
    const across = key === 'ArrowRight' || key === 'ArrowLeft' ? Math.abs(dy) : Math.abs(dx);
    if (along <= 1) continue;
    const score = along + 2 * across;
    if (!best || score < best.score) best = { id: n.id, score };
  }
  return best?.id;
}

/** The edges into and out of a node, for the side panel. */
export function connections(g: GraphView, id: string): { incoming: GraphEdge[]; outgoing: GraphEdge[] } {
  return { incoming: g.edges.filter((e) => e.to === id && e.from !== id), outgoing: g.edges.filter((e) => e.from === id) };
}

// ---------- subworkflows: the nested view (docs/ui.md 6.2) ----------

/** Joins a subworkflow node's id and the id of a node of its nested graph ("build/draft"). */
export const SUB_SEP = '/';

/** The last part of a node id of the expanded view: the node's own name in its graph. */
export const localName = (id: string): string => id.slice(id.lastIndexOf(SUB_SEP) + 1);

/** An expanded subworkflow: its node (the frame's header) and every node of its nested graph, nested ones included. */
export interface Cluster {
  id: string;
  label: string;
  members: string[];
}

/** The nodes of a graph a run leaves from: none of their edges goes on (loop edges back aside). */
function exitsOf(g: GraphView): string[] {
  const out = g.nodes.filter((n) => !g.edges.some((e) => e.from === n.id && e.kind !== 'loop' && e.to !== n.id)).map((n) => n.id);
  return out.length ? out : g.nodes.slice(-1).map((n) => n.id);
}

/**
 * The graph with every subworkflow node named in `open` expanded in place: the node stays (it heads the frame), its nested
 * graph's nodes follow it as `<node>/<nested>` (and deeper, `<node>/<nested>/<inner>`), an edge leads from the node to each
 * nested entry, and the node's own out-edges leave from the nested graph's last nodes. Pure: the same graph and set give the
 * same picture, so the layout does not jump.
 */
export function expandSubworkflows(g: GraphView, open: ReadonlySet<string>): { graph: GraphView; clusters: Cluster[] } {
  const nodes: GraphNode[] = [];
  const edges: GraphEdge[] = [];
  const clusters: Cluster[] = [];
  const flat = (gv: GraphView, prefix: string): Map<string, { into: string[]; from: string[] }> => {
    const ports = new Map<string, { into: string[]; from: string[] }>();
    for (const n of gv.nodes) {
      const id = prefix + n.id;
      nodes.push({ ...n, id });
      if (n.kind === 'subworkflow' && n.sub && open.has(id)) {
        const first = nodes.length;
        const inner = flat(n.sub, id + SUB_SEP);
        const members = nodes.slice(first).map((m) => m.id);
        for (const e of n.sub.entry) for (const to of inner.get(e)?.into ?? []) edges.push({ from: id, to, kind: 'direct', nested: true });
        const exits = exitsOf(n.sub).flatMap((x) => inner.get(x)?.from ?? []);
        clusters.push({ id, label: n.id, members: [id, ...members] });
        ports.set(n.id, { into: [id], from: exits.length ? exits : [id] });
      } else {
        ports.set(n.id, { into: [id], from: [id] });
      }
    }
    for (const e of gv.edges) {
      const a = ports.get(e.from);
      const b = ports.get(e.to);
      if (!a || !b) continue;
      for (const from of a.from) for (const to of b.into) edges.push({ ...e, from, to });
    }
    return ports;
  };
  if (open.size === 0) return { graph: g, clusters };
  flat(g, '');
  return { graph: { ...g, nodes, edges }, clusters };
}

/**
 * The executions of the nested runs of the expanded subworkflows, as executions of the expanded view: each nested node's id
 * is `<node>/<nested>`, and its step is the step at which the parent's node ran, so "as of a checkpoint" shows a nested run
 * only from the step its subworkflow node ran at.
 */
export function subExecutions(x: RunExplanation, open: ReadonlySet<string>): ExecView[] {
  const out: ExecView[] = [];
  const walk = (subs: readonly SubrunView[], prefix: string, step: number | undefined) => {
    for (const s of subs) {
      const id = prefix + s.nodeId;
      if (!open.has(id)) continue;
      const at = step ?? s.step;
      for (const e of s.executions) out.push({ ...e, nodeId: id + SUB_SEP + e.nodeId, step: at });
      walk(s.subruns ?? [], id + SUB_SEP, at);
    }
  };
  walk(x.subruns ?? [], '', undefined);
  return out;
}

/** The nested run of a subworkflow node of the expanded view (its latest), if it has run. */
export function subrunOf(x: RunExplanation, id: string): SubrunView | undefined {
  const parts = id.split(SUB_SEP);
  let subs: readonly SubrunView[] = x.subruns ?? [];
  let found: SubrunView | undefined;
  for (const part of parts) {
    found = subs.filter((s) => s.nodeId === part).at(-1);
    if (!found) return undefined;
    subs = found.subruns ?? [];
  }
  return found;
}

// ---------- resume, time travel and replay (decision P123) ----------

export type RerunKind = 'resume' | 'timeTravel' | 'replay';

/** What a confirmation says before an action: its title, its button, and one line per thing that happens. */
export interface RerunPlan {
  kind: RerunKind;
  title: string;
  confirm: string;
  lines: string[];
  /** The steps that ask first in a replay (they change something outside the run). */
  asks: { node: string; effect: string }[];
}

/** The nodes of a graph that change something outside the run (meta.effect), in the graph's order. */
export function effectNodes(g: GraphView | undefined): { node: string; effect: string }[] {
  return (g?.nodes ?? []).filter((n) => n.meta?.effect).map((n) => ({ node: n.id, effect: n.meta!.effect! }));
}

/** The nodes that ran after a step in this run, in the order they first ran, and how many turns that was. */
export function ranAfter(x: RunExplanation, step: number): { nodes: string[]; turns: number } {
  const after = x.executions.filter((e) => e.step > step);
  return { nodes: [...new Set(after.map((e) => e.nodeId))], turns: after.length };
}

const list = (xs: readonly string[]): string => (xs.length <= 1 ? (xs[0] ?? '') : `${xs.slice(0, -1).join(', ')} and ${xs.at(-1)}`);

const runWords: Record<string, string> = { WAITING_HUMAN: 'waiting for you', PAUSED: 'paused' };

function supersedeLine(x: RunExplanation): string[] {
  const a = x.actions;
  if (!a?.supersedes) return [];
  const which = a.supersedes === x.run?.id ? 'This run' : "The task's current run";
  return [`${which} (${runWords[a.supersedesStatus ?? ''] ?? (a.supersedesStatus ?? '').toLowerCase()}) is stopped first; its history stays as it is.`];
}

const filesLine = "The worktree's files are not put back: the new run works on the files as they are now. The task's evidence, reviews and iterations used stay.";

/** What the confirmation of an action says will run again, from the run's own record. */
export function rerunPlan(x: RunExplanation, kind: RerunKind, cp?: CheckpointView): RerunPlan {
  if (kind === 'resume') {
    const latest = x.checkpoints.find((c) => c.latest) ?? x.checkpoints.at(-1);
    const step = latest?.step ?? 0;
    const lines = [`It goes on in this run from step ${step}, its latest checkpoint, in the same worktree.`];
    if (x.run?.failedReason) lines.push(`It stopped because: ${x.run.failedReason}`);
    if (latest?.next?.length) lines.push(`Runs again first: ${list(latest.next)}. What finished before it is not run again.`);
    lines.push('Answers already given stay as they were: a step you rejected is still rejected and stops the run again.');
    lines.push('Its budgets and loop counts carry on from where they were.');
    return { kind, title: 'Resume this run?', confirm: 'Resume the run', lines, asks: [] };
  }
  const from = cp ?? x.checkpoints[0];
  const step = from?.step ?? 0;
  const after = ranAfter(x, step);
  const again = after.nodes.length ? `${list(after.nodes)} (${after.turns} ${after.turns === 1 ? 'turn' : 'turns'} in this run)` : '';
  if (kind === 'timeTravel') {
    const lines = [`A new run starts from the checkpoint at step ${step} with this run's state as it was then. This run and its history are kept as they are.`];
    if (from?.next?.length) lines.push(`Runs first: ${list(from.next)}.`);
    lines.push(again ? `Ran after that point here and runs again as the workflow reaches it: ${again}.` : 'Nothing ran after that point in this run.');
    lines.push('Requests are asked afresh in the new run.', ...supersedeLine(x), filesLine);
    return { kind, title: `Time travel to step ${step}?`, confirm: 'Start the new run', lines, asks: [] };
  }
  const asks = effectNodes(x.graph);
  const lines = [`A new run goes through this run's steps again from step ${step}, with the inputs they had then. This run is kept as it is.`];
  lines.push(again ? `Runs again: ${again}. Agent turns are paid for again.` : 'Nothing ran after that point in this run.');
  lines.push(
    asks.length
      ? `Asks you first, every time, before a step that changes something outside the run: ${asks.map((a) => `${a.node} (${a.effect})`).join('; ')}.`
      : 'No step of this workflow changes anything outside the run without asking you.',
  );
  lines.push(...supersedeLine(x), filesLine);
  return { kind, title: `Replay this run from step ${step}?`, confirm: 'Start the replay', lines, asks };
}

/** Where a forked or replayed run came from, in words. */
export function originLine(x: RunExplanation): string | undefined {
  const o = x.origin;
  if (!o) return undefined;
  return o.kind === 'replay' ? `Replay of an earlier run from step ${o.step}` : `Time travel from step ${o.step} of an earlier run`;
}

/** Who drives the run, in words, for a run that has not ended. */
export function leaseWords(x: RunExplanation): string | undefined {
  const r = x.run;
  const l = x.actions?.lease;
  if (!r || !l || ['COMPLETED', 'FAILED', 'CANCELLED'].includes(r.status)) return undefined;
  if (l.driving || (l.held && l.mine)) return 'This Drydock';
  if (l.held) return 'Another Drydock';
  return r.status === 'RUNNING' || r.status === 'RECOVERING' ? 'Nobody: it stopped when Drydock did' : 'Nobody (nothing to run now)';
}
