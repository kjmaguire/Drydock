// The workflow graph and checkpoint timeline (docs/ui.md 6.2, S14), drawn with React Flow. Loaded on first use. Read-only:
// choosing a checkpoint shows the run as it was then; nothing here changes a run.
import { Background, Controls, MarkerType, ReactFlow, type Edge, type Node } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { useMemo, useState } from 'react';
import type { Agent } from '../api/types';
import type { RunExplanation } from '../api/runClient';
import { layout, nodeStates, type NodeState } from '../lib/graphLayout';
import { agentName, formatCost, formatTime } from '../lib/format';
import { isDarkTheme } from '../lib/effectiveTheme';

const KIND_LABEL: Record<string, string> = { agent_task: 'agent', check: 'check', request: 'human', route: 'route', subworkflow: 'subworkflow' };
const STATUS_TEXT: Record<NodeState['status'], string> = { pending: 'not run', started: 'running', done: 'done', failed: 'failed', abandoned: 'abandoned', waiting: 'waiting for you' };

export default function WorkflowGraph({ x, agents, onOpenTurn }: { x: RunExplanation; agents: Agent[]; onOpenTurn: () => void }) {
  const [asOf, setAsOf] = useState<string | undefined>();
  const g = x.graph;
  const cp = x.checkpoints.find((c) => c.id === asOf);
  const states = useMemo(() => {
    const latest = x.checkpoints.find((c) => c.latest);
    return nodeStates(x.executions, cp?.step, (cp ?? latest)?.waiting ?? []);
  }, [x, cp]);

  const { nodes, edges } = useMemo(() => {
    if (!g) return { nodes: [] as Node[], edges: [] as Edge[] };
    const placed = layout(g);
    const ns: Node[] = placed.map((p) => {
      const def = g.nodes.find((n) => n.id === p.id)!;
      const s = states.get(p.id) ?? { status: 'pending' as const, attempts: 0, runs: 0 };
      const cost = (s.last?.usage ?? []).reduce((t, u) => t + u.costUsd, 0);
      return {
        id: p.id,
        position: { x: p.column * 230, y: p.row * 96 },
        className: `wf-node wf-${s.status} wf-kind-${def.kind}`,
        data: {
          label: (
            <div data-testid="wf-node" data-node={p.id} data-status={s.status}>
              <div className="wf-title">
                <strong>{p.id}</strong> <span className="wf-kind">{KIND_LABEL[def.kind] ?? def.kind}</span>
              </div>
              <div className="wf-status">
                {STATUS_TEXT[s.status]}
                {s.attempts > 1 ? ` · attempt ${s.attempts}` : ''}
                {s.runs > 1 ? ` · ran ${s.runs}×` : ''}
              </div>
              {s.last?.agentId ? <div className="wf-meta">{agentName(s.last.agentId, agents) || s.last.agentId}</div> : null}
              {s.last?.decision?.model ? <div className="wf-meta">{s.last.decision.model}</div> : null}
              {s.last?.usage?.length ? <div className="wf-meta">{formatCost(cost)}</div> : null}
            </div>
          ),
        },
        draggable: false,
        connectable: false,
      };
    });
    const es: Edge[] = g.edges.map((e, i) => {
      const label =
        e.kind === 'loop' ? `loop${e.maxIter ? ` ≤ ${e.maxIter}` : ''}${e.when ? ` when ${e.when}` : ''}` : e.when ? `when ${e.when}` : e.kind === 'fanout' ? 'fan-out' : e.kind === 'fanin' ? 'fan-in' : undefined;
      return {
        id: `${e.from}->${e.to}-${i}`,
        source: e.from,
        target: e.to,
        label,
        type: e.kind === 'loop' ? 'smoothstep' : 'default',
        className: `wf-edge wf-edge-${e.kind}`,
        markerEnd: { type: MarkerType.ArrowClosed },
        style: e.kind === 'loop' ? { strokeDasharray: '6 4' } : undefined,
      };
    });
    return { nodes: ns, edges: es };
  }, [g, states, agents]);

  if (!g) return <p className="muted">The workflow &quot;{x.run?.workflow}&quot; is not registered in this build, so its graph cannot be drawn. The Turns tab lists what ran.</p>;
  return (
    <div className="wf" data-testid="workflow-graph">
      <p className="muted">
        {cp ? (
          <>
            As of checkpoint at step {cp.step} ({cp.kind}, {formatTime(cp.created)}). <button className="link-button" onClick={() => setAsOf(undefined)}>Show the latest</button>
          </>
        ) : (
          'Latest state. Choose a checkpoint below to see the run as it was then.'
        )}{' '}
        <button className="link-button" onClick={onOpenTurn}>
          Every turn in detail
        </button>
      </p>
      <div className="wf-canvas" aria-label={`Workflow ${g.name}`}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          fitView
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          colorMode={isDarkTheme() ? 'dark' : 'light'}
          proOptions={{ hideAttribution: true }}
          minZoom={0.2}
        >
          <Background />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
      <div className="wf-timeline" role="group" aria-label="Checkpoint timeline">
        {x.checkpoints.map((c) => (
          <button
            key={c.id}
            className={`wf-cp wf-cp-${c.kind}`}
            aria-pressed={asOf === c.id}
            onClick={() => setAsOf(asOf === c.id ? undefined : c.id)}
            title={`${c.kind} at step ${c.step}, ${formatTime(c.created)}${c.lastNodes?.length ? `, after ${c.lastNodes.join(', ')}` : ''}`}
            data-testid="wf-checkpoint"
          >
            {c.step}
            {c.latest ? ' (latest)' : ''}
          </button>
        ))}
      </div>
    </div>
  );
}
