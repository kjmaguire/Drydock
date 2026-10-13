// The workflow graph and its checkpoint timeline (docs/ui.md 6.2, S14), drawn as our own inline SVG: nodes are rounded
// boxes colored by what they did, edges are smooth paths with arrowheads (loops dashed, under the nodes). Drag the
// background to pan, use the wheel or the buttons to zoom, "Fit to view" to see it all. Nodes are focusable (Tab reaches
// the graph, the arrow keys move between nodes, Enter or Space selects, Escape clears) and the selection drives the side
// panel. Read-only: choosing a checkpoint shows the run as it was then; nothing here changes a run.

import { css, on, ref, type Handle, type RemixNode } from 'remix/component';
import { Bot, GitBranch, ListChecks, Maximize, UserRound, Workflow, ZoomIn, ZoomOut, type IconNode } from 'lucide';
import type { Agent } from '../../api/types.ts';
import type { GraphView, RunExplanation } from '../../api/runClient.ts';
import type { NodeState } from '../../lib/graphLayout.ts';
import { agentName, formatCost, formatTime } from '../../lib/format.ts';
import { Button, IconButton } from '../../ui/button.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t, tone } from '../../ui/tokens.ts';
import {
  clip,
  fitView,
  graphGeometry,
  KIND_LABEL,
  neighbor,
  NODE_TONE,
  PENDING,
  statusLine,
  usageCost,
  viewTransform,
  zoomAt,
  type Arrow,
  type GraphGeom,
  type View,
} from './run-model.ts';

export const KIND_ICON: Record<string, IconNode> = { agent_task: Bot, check: ListChecks, request: UserRound, route: GitBranch, subworkflow: Workflow };

const GRID = 20;

/** A canvas tall enough for the graph at the zoom that fits its width (wide, short graphs get a shorter canvas). */
function canvasHeight(gm: GraphGeom, width: number): number {
  if (width <= 0) return 420;
  const bw = Math.max(1, gm.bounds.maxX - gm.bounds.minX);
  const bh = Math.max(1, gm.bounds.maxY - gm.bounds.minY);
  const k = Math.max(0.2, Math.min(1.1, (width - 80) / bw));
  return Math.round(Math.min(600, Math.max(420, bh * k + 80 + 56)));
}

const benchCss = css({
  display: 'grid',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  minWidth: '0',
});

const barCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: `${t.s1} ${t.s3}`,
  padding: `${t.s2} ${t.s3}`,
  borderBottom: `1px solid ${t.border}`,
  fontSize: t.textSm,
  color: t.textMuted,
  '& > p': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: `2px ${t.s2}`, margin: '0', minWidth: '0' },
});

const legendCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  marginLeft: 'auto',
  fontSize: t.textXs,
  color: t.textFaint,
  '& span': { display: 'inline-flex', alignItems: 'center', gap: '5px' },
  '& i': { width: '7px', height: '7px', borderRadius: '50%', display: 'inline-block' },
});

const splitCss = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1fr) 340px',
  minHeight: '0',
  '@media (max-width: 1100px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
});

const canvasCss = css({
  position: 'relative',
  height: 'var(--wf-h, 420px)',
  minWidth: '0',
  overflow: 'hidden',
  background: t.bgApp,
  backgroundImage: `radial-gradient(circle, color-mix(in srgb, ${t.textFaint} 45%, transparent) 1px, transparent 1.3px)`,
  cursor: 'grab',
  touchAction: 'none',
  userSelect: 'none',
  '&[data-dragging="true"]': { cursor: 'grabbing' },
  '& > svg': { display: 'block', width: '100%', height: '100%' },
});

const sideCss = css({
  minWidth: '0',
  borderLeft: `1px solid ${t.border}`,
  background: t.bgSurface,
  overflow: 'auto',
  height: 'var(--wf-h, 420px)',
  '@media (max-width: 1100px)': { borderLeft: '0', borderTop: `1px solid ${t.border}`, height: 'auto' },
});

const controlsCss = css({
  position: 'absolute',
  left: t.s3,
  bottom: t.s3,
  display: 'flex',
  alignItems: 'center',
  gap: '2px',
  padding: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgRaised,
  boxShadow: t.shadowSm,
  cursor: 'default',
  '& output': { minWidth: '42px', textAlign: 'center', fontSize: t.textXs, color: t.textMuted, fontVariantNumeric: 'tabular-nums' },
});

const hintCss = css({ position: 'absolute', right: t.s3, bottom: t.s3, fontSize: t.textXs, color: t.textFaint, pointerEvents: 'none' });

const svgCss = css({
  '& .wf-edge path': { fill: 'none', stroke: t.borderStrong, strokeWidth: '1.5px', transition: `stroke ${t.fast} ${t.ease}` },
  '& .wf-edge[data-taken="true"] path': { stroke: t.textFaint },
  '& .wf-edge[data-active="true"] path': { stroke: t.accent, strokeWidth: '2px' },
  '& .wf-edge[data-kind="loop"] path': { strokeDasharray: '6 4' },
  '& .wf-arrow': { fill: t.borderStrong },
  '& .wf-arrow-taken': { fill: t.textFaint },
  '& .wf-arrow-active': { fill: t.accent },
  '& .wf-label rect': { fill: t.bgSurface, stroke: t.border, strokeWidth: '1px' },
  '& .wf-label text': { fill: t.textMuted, fontSize: '12px', fontWeight: '500' },
  '& .wf-label[data-active="true"] rect': { stroke: t.accent },
  '& .wf-label[data-active="true"] text': { fill: t.accentText },
  '& .wf-node': { cursor: 'pointer', outline: 'none' },
  '& .wf-box': { fill: t.bgSurface, stroke: t.border, strokeWidth: '1px', filter: 'drop-shadow(0 1px 1.5px rgb(0 0 0 / 0.08))', transition: `stroke ${t.fast} ${t.ease}` },
  '& .wf-node:hover .wf-box': { stroke: t.borderStrong },
  '& .wf-ring': { fill: 'none', stroke: 'transparent', strokeWidth: '2px' },
  '& .wf-node[aria-pressed="true"] .wf-box': { stroke: t.accent, strokeWidth: '1.5px' },
  '& .wf-node[aria-pressed="true"] .wf-ring': { stroke: `color-mix(in srgb, ${t.accent} 35%, transparent)`, strokeWidth: '4px' },
  '& .wf-node:focus-visible .wf-ring': { stroke: t.accent, strokeWidth: '2px' },
  '& .wf-title': { fill: t.text, fontSize: '13.5px', fontWeight: '600' },
  '& .wf-kind': { fill: t.textFaint, fontSize: '11px', fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' },
  '& .wf-status': { fontSize: '12px', fontWeight: '500' },
  '& .wf-meta': { fill: t.textMuted, fontSize: '12px' },
  '& .wf-cost': { fill: t.textMuted, fontSize: '12px', fontFamily: t.mono },
  '& .wf-icon': { color: t.textMuted },
  '& .wf-node[data-status="pending"] .wf-title': { fill: t.textMuted },
  '& .wf-node[data-status="pending"] .wf-box': { strokeDasharray: '4 3' },
  '& .wf-pulse': { transformBox: 'fill-box', transformOrigin: 'center', animation: 'wf-pulse 1.6s ease-out infinite' },
  '@keyframes wf-pulse': { from: { opacity: '0.7', transform: 'scale(1)' }, to: { opacity: '0', transform: 'scale(2.6)' } },
  '@media (prefers-reduced-motion: reduce)': { '& .wf-pulse': { animation: 'none', opacity: '0' } },
  '& text': { userSelect: 'none' },
});

const timelineCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: `${t.s2} ${t.s3}`,
  borderTop: `1px solid ${t.border}`,
  overflowX: 'auto',
  '& > .label': { flex: 'none', fontSize: t.textXs, fontWeight: '600', color: t.textFaint, textTransform: 'uppercase', letterSpacing: '0.04em', marginRight: t.s1 },
  '& > .track': { position: 'relative', display: 'flex', alignItems: 'center', gap: t.s2 },
  '& > .track::before': { content: '""', position: 'absolute', left: '8px', right: '8px', top: '50%', height: '1px', background: t.border },
});

const cpCss = css({
  position: 'relative',
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  height: '24px',
  padding: '0 9px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  background: t.bgRaised,
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '500',
  fontVariantNumeric: 'tabular-nums',
  whiteSpace: 'nowrap',
  '&:hover': { color: t.text, borderColor: t.borderStrong, background: t.bgRaised },
  '&[aria-pressed="true"]': { color: t.accentText, borderColor: t.accent, background: t.accentSoft },
  '&[data-latest="true"]': { color: t.text },
  '& i': { width: '6px', height: '6px', borderRadius: '50%', display: 'inline-block' },
});

const CP_TONE: Record<string, string> = { entry: tone.info.fg, step: t.textFaint, response_entry: tone.warn.fg };

const LEGEND: [NodeState['status'], string][] = [
  ['done', 'done'],
  ['started', 'running'],
  ['waiting', 'waiting'],
  ['failed', 'failed'],
  ['pending', 'not run'],
];

export function WorkflowGraph(
  handle: Handle<{
    x: RunExplanation;
    agents: readonly Agent[];
    states: Map<string, NodeState>;
    asOf?: string;
    selected?: string;
    onAsOf: (id: string | undefined) => void;
    onSelect: (id: string | undefined) => void;
    onOpenTurns: () => void;
    /** The side panel (node details). */
    children?: RemixNode;
  }>,
) {
  const markerBase = `wf-arrow-${handle.id}`;
  let geoFor: GraphView | undefined;
  let geo: GraphGeom = { nodes: [], edges: [], bounds: { minX: 0, minY: 0, maxX: 1, maxY: 1 } };
  let canvas: HTMLDivElement | undefined;
  let size = { w: 0, h: 0 };
  let view: View | undefined; // undefined: fit to the canvas (follows resizes)
  let focusId: string | undefined;
  let drag: { x: number; y: number; vx: number; vy: number; moved: boolean } | undefined;

  const geometry = (g: GraphView): GraphGeom => {
    if (g !== geoFor) {
      geoFor = g;
      geo = graphGeometry(g);
    }
    return geo;
  };
  const current = (): View => view ?? fitView(geo.bounds, size.w, size.h);
  const setView = (v: View | undefined) => {
    view = v;
    void handle.update();
  };
  const zoomBy = (f: number) => setView(zoomAt(current(), f, size.w / 2, size.h / 2));

  /** Pan just enough that a node is inside the canvas. */
  const ensureVisible = (id: string) => {
    const n = geo.nodes.find((b) => b.id === id);
    if (!n || size.w <= 0) return;
    const v = current();
    const m = 24;
    const left = n.x * v.k + v.x;
    const top = n.y * v.k + v.y;
    const right = left + n.w * v.k;
    const bottom = top + n.h * v.k;
    let dx = 0;
    let dy = 0;
    if (left < m) dx = m - left;
    else if (right > size.w - m) dx = size.w - m - right;
    if (top < m) dy = m - top;
    else if (bottom > size.h - m) dy = size.h - m - bottom;
    if (dx || dy) setView({ ...v, x: v.x + dx, y: v.y + dy });
  };

  const focusNode = (id: string) => {
    focusId = id;
    ensureVisible(id);
    void handle.update();
    handle.queueTask(() => canvas?.querySelector<SVGGElement>(`[data-testid="wf-node"][data-node="${CSS.escape(id)}"]`)?.focus());
  };

  const attach = (node: Element, signal: AbortSignal) => {
    canvas = node as HTMLDivElement;
    const measure = () => {
      const w = canvas?.clientWidth ?? 0;
      const h = canvas?.clientHeight ?? 0;
      if (w === size.w && h === size.h) return;
      size = { w, h };
      void handle.update();
    };
    measure();
    if (typeof ResizeObserver !== 'undefined') {
      const ro = new ResizeObserver(measure);
      ro.observe(canvas);
      signal.addEventListener('abort', () => ro.disconnect());
    }
    canvas.addEventListener(
      'wheel',
      (e) => {
        e.preventDefault();
        const r = canvas!.getBoundingClientRect();
        setView(zoomAt(current(), Math.exp(-e.deltaY * 0.0015), e.clientX - r.left, e.clientY - r.top));
      },
      { passive: false, signal },
    );
  };

  return () => {
    const { x, agents, states, asOf, selected, onAsOf, onSelect, onOpenTurns, children } = handle.props;
    const g = x.graph;
    if (!g) {
      return (
        <p data-testid="workflow-missing" style={{ color: t.textMuted }}>
          The workflow &quot;{x.run?.workflow}&quot; is not registered in this build, so its graph cannot be drawn. The Turns tab lists what ran.
        </p>
      );
    }
    const gm = geometry(g);
    const v = current();
    const cp = x.checkpoints.find((c) => c.id === asOf);
    const state = (id: string) => states.get(id) ?? PENDING;
    const tabStop = focusId && gm.nodes.some((n) => n.id === focusId) ? focusId : selected && gm.nodes.some((n) => n.id === selected) ? selected : gm.nodes[0]?.id;
    const edges = gm.edges.map((e) => {
      const from = state(e.edge.from).status;
      const to = state(e.edge.to).status;
      const active = !!selected && (e.edge.from === selected || e.edge.to === selected);
      const taken = from !== 'pending' && to !== 'pending';
      return { ...e, active, taken, marker: `${markerBase}-${active ? 'active' : taken ? 'taken' : 'idle'}` };
    });
    const ordered = [...edges.filter((e) => e.back), ...edges.filter((e) => !e.back)];

    return (
      <div mix={benchCss} data-testid="workflow-graph">
        <div mix={barCss}>
          <p>
            {cp ? (
              <>
                <span>
                  As of checkpoint at step {cp.step} ({cp.kind}, {formatTime(cp.created)}).
                </span>
                <Button variant="link" size="sm" mix={[on('click', () => onAsOf(undefined))]}>
                  Show the latest
                </Button>
              </>
            ) : (
              <span>Latest state. Choose a checkpoint below to see the run as it was then.</span>
            )}
            <Button variant="link" size="sm" mix={[on('click', () => onOpenTurns())]}>
              Every turn in detail
            </Button>
          </p>
          <div mix={legendCss} aria-hidden="true">
            {LEGEND.map(([st, label]) => (
              <span key={st}>
                <i style={{ background: st === 'pending' ? t.borderStrong : tone[NODE_TONE[st]].fg }} />
                {label}
              </span>
            ))}
          </div>
        </div>
        <div mix={splitCss} style={{ '--wf-h': `${canvasHeight(gm, size.w)}px` }}>
          <div
            mix={[
              canvasCss,
              ref(attach),
              on('pointerdown', (e) => {
                if (e.button !== 0) return;
                const target = e.target as Element;
                if (target.closest('[data-testid="wf-node"], button')) return;
                const cv = current();
                drag = { x: e.clientX, y: e.clientY, vx: cv.x, vy: cv.y, moved: false };
                try {
                  e.currentTarget.setPointerCapture(e.pointerId);
                } catch {
                  /* not an active pointer (a synthetic event): panning still follows the moves */
                }
              }),
              on('pointermove', (e) => {
                if (!drag) return;
                const dx = e.clientX - drag.x;
                const dy = e.clientY - drag.y;
                if (!drag.moved && Math.abs(dx) + Math.abs(dy) < 3) return;
                if (!drag.moved) e.currentTarget.setAttribute('data-dragging', 'true');
                drag.moved = true;
                setView({ ...current(), x: drag.vx + dx, y: drag.vy + dy });
              }),
              on('pointerup', (e) => {
                const d = drag;
                drag = undefined;
                e.currentTarget.removeAttribute('data-dragging');
                if (d && !d.moved && selected) onSelect(undefined);
              }),
              on('pointercancel', (e) => {
                drag = undefined;
                e.currentTarget.removeAttribute('data-dragging');
              }),
              on('keydown', (e) => {
                if ((e.target as Element).closest('button')) return;
                if (e.key === '+' || e.key === '=') zoomBy(1.2);
                else if (e.key === '-' || e.key === '_') zoomBy(1 / 1.2);
                else if (e.key === '0') setView(undefined);
                else return;
                e.preventDefault();
              }),
            ]}
            role="group"
            aria-label={`Workflow ${g.name}`}
            aria-roledescription="workflow graph"
            data-testid="wf-canvas"
            style={{ backgroundSize: `${GRID * v.k}px ${GRID * v.k}px`, backgroundPosition: `${v.x}px ${v.y}px` }}
          >
            <svg xmlns="http://www.w3.org/2000/svg" mix={svgCss}>
              <defs>
                {(['idle', 'taken', 'active'] as const).map((kind) => (
                  <marker key={kind} id={`${markerBase}-${kind}`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="9" markerHeight="9" markerUnits="userSpaceOnUse" orient="auto-start-reverse">
                    <path d="M 0 0.8 L 10 5 L 0 9.2 z" class={kind === 'idle' ? 'wf-arrow' : `wf-arrow-${kind}`} />
                  </marker>
                ))}
              </defs>
              <g transform={viewTransform(v)} data-testid="wf-viewport">
                <g aria-hidden="true">
                  {ordered.map((e) => (
                    <g key={e.id} class="wf-edge" data-testid="wf-edge" data-from={e.edge.from} data-to={e.edge.to} data-kind={e.edge.kind} data-taken={String(e.taken)} data-active={String(e.active)}>
                      <path d={e.d} marker-end={`url(#${e.marker})`} />
                    </g>
                  ))}
                  {ordered
                    .filter((e) => e.label)
                    .map((e) => {
                      const w = Math.round(e.label!.length * 5.9 + 14);
                      return (
                        <g key={`l-${e.id}`} class="wf-label" data-active={String(e.active)} transform={`translate(${e.lx} ${e.ly})`}>
                          <rect x={-w / 2} y={-9} width={w} height={18} rx={9} />
                          <text text-anchor="middle" dominant-baseline="central">
                            {e.label}
                          </text>
                        </g>
                      );
                    })}
                </g>
                {gm.nodes.map((n) => {
                  const s = state(n.id);
                  const color = s.status === 'pending' ? t.textFaint : tone[NODE_TONE[s.status]].fg;
                  const agent = s.last?.agentId ? agentName(s.last.agentId, agents) || s.last.agentId : '';
                  const model = s.last?.decision?.model ?? '';
                  const meta = [agent, model].filter(Boolean).join(' · ');
                  const cost = s.last?.usage?.length ? formatCost(usageCost(s.last)) : '';
                  const kind = KIND_LABEL[n.def.kind] ?? n.def.kind;
                  const line = statusLine(s);
                  const isSel = selected === n.id;
                  return (
                    <g
                      key={n.id}
                      class="wf-node"
                      data-testid="wf-node"
                      data-node={n.id}
                      data-status={s.status}
                      data-kind={n.def.kind}
                      role="button"
                      aria-pressed={isSel ? 'true' : 'false'}
                      aria-label={`${n.id} (${kind}): ${line}${meta ? `, ${meta}` : ''}${cost ? `, ${cost}` : ''}`}
                      tabindex={n.id === tabStop ? 0 : -1}
                      transform={`translate(${n.x} ${n.y})`}
                      mix={[
                        on('click', () => {
                          focusId = n.id;
                          onSelect(isSel ? undefined : n.id);
                        }),
                        on('focus', () => {
                          if (focusId !== n.id) {
                            focusId = n.id;
                            void handle.update();
                          }
                        }),
                        on('keydown', (e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            onSelect(isSel ? undefined : n.id);
                          } else if (e.key === 'Escape') {
                            if (selected) {
                              e.preventDefault();
                              onSelect(undefined);
                            }
                          } else if (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'ArrowUp' || e.key === 'ArrowDown') {
                            e.preventDefault();
                            const next = neighbor(gm.nodes, n.id, e.key as Arrow);
                            if (next) focusNode(next);
                          }
                        }),
                      ]}
                    >
                      <title>{`${n.id} (${kind}): ${line}${meta ? `\n${meta}` : ''}${cost ? `\n${cost}` : ''}`}</title>
                      <rect class="wf-ring" x={-3} y={-3} width={n.w + 6} height={n.h + 6} rx={11} />
                      <rect class="wf-box" width={n.w} height={n.h} rx={8} />
                      <g class="wf-icon" transform="translate(11 10)">
                        <Icon icon={KIND_ICON[n.def.kind] ?? Workflow} size={15} />
                      </g>
                      <text class="wf-title" x={32} y={22}>
                        {clip(n.id, 16)}
                      </text>
                      <text class="wf-kind" x={n.w - 12} y={22} text-anchor="end">
                        {kind}
                      </text>
                      {s.status === 'started' ? <circle class="wf-pulse" cx={16} cy={41} r={3.5} style={{ fill: color }} /> : null}
                      <circle cx={16} cy={41} r={3.5} style={{ fill: color }} />
                      <text class="wf-status" x={26} y={45} style={{ fill: color }}>
                        {clip(line, cost ? 18 : 26)}
                      </text>
                      {cost ? (
                        <text class="wf-cost" x={n.w - 12} y={45} text-anchor="end">
                          {cost}
                        </text>
                      ) : null}
                      {meta ? (
                        <text class="wf-meta" x={12} y={60}>
                          {clip(meta, 28)}
                        </text>
                      ) : null}
                    </g>
                  );
                })}
              </g>
            </svg>
            <div mix={controlsCss} role="toolbar" aria-label="Graph view">
              <IconButton icon={ZoomOut} label="Zoom out" size={15} mix={[on('click', () => zoomBy(1 / 1.2))]} />
              <output aria-label="Zoom level" data-testid="wf-zoom">
                {Math.round(v.k * 100)}%
              </output>
              <IconButton icon={ZoomIn} label="Zoom in" size={15} mix={[on('click', () => zoomBy(1.2))]} />
              <IconButton icon={Maximize} label="Fit to view" size={15} aria-pressed={view === undefined ? 'true' : 'false'} mix={[on('click', () => setView(undefined))]} />
            </div>
            <span mix={hintCss} aria-hidden="true">
              Drag to pan · scroll to zoom
            </span>
          </div>
          <aside mix={sideCss} aria-label="Node details">
            {children}
          </aside>
        </div>
        <div mix={timelineCss} role="group" aria-label="Checkpoint timeline">
          <span class="label">Checkpoints</span>
          {x.checkpoints.length === 0 ? <span style={{ color: t.textFaint, fontSize: t.textSm }}>No checkpoints yet.</span> : null}
          <div class="track">
            {x.checkpoints.map((c) => (
              <button
                key={c.id}
                type="button"
                mix={[cpCss, on('click', () => onAsOf(asOf === c.id ? undefined : c.id))]}
                aria-pressed={asOf === c.id ? 'true' : 'false'}
                data-latest={String(!!c.latest)}
                title={`${c.kind} at step ${c.step}, ${formatTime(c.created)}${c.lastNodes?.length ? `, after ${c.lastNodes.join(', ')}` : ''}`}
                data-testid="wf-checkpoint"
              >
                <i aria-hidden="true" style={{ background: CP_TONE[c.kind] ?? t.textFaint }} />
                {c.step}
                {c.latest ? ' (latest)' : ''}
              </button>
            ))}
          </div>
        </div>
      </div>
    );
  };
}
