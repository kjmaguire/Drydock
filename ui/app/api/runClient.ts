// Typed client for the run explanation and the workflow graph (internal/core/ext_explain.go).

import type { Transport } from './transport.ts';
import type { AguiActivity } from './agui.gen.ts';
import type { UsageRow } from './types.ts';

export interface GraphNode {
  id: string;
  kind: 'agent_task' | 'check' | 'request' | 'route' | 'subworkflow' | string;
  maxAttempts?: number;
  fanIn?: string[];
  request?: string;
  meta?: Record<string, string>;
  sub?: GraphView;
}

export interface GraphEdge {
  from: string;
  to: string;
  kind: 'direct' | 'conditional' | 'fanout' | 'fanin' | 'loop' | string;
  when?: string;
  conditional?: boolean;
  maxIter?: number;
  onExhausted?: string;
}

export interface GraphView {
  name: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
  entry: string[];
}

export interface ManifestItem {
  source: string;
  included: boolean;
  bytes: number;
  reason: string;
  priority: number;
}

export interface RoutingDecisionView {
  id: string;
  decidedBy: string;
  agent?: string;
  providerId?: string;
  model?: string;
  effort?: string;
  level?: string;
  estCostUsd?: number;
  escalatedFrom?: string;
  reason?: string;
}

export interface ExecView {
  nodeId: string;
  kind?: string;
  step: number;
  attempt: number;
  status: 'started' | 'done' | 'failed' | 'abandoned' | string;
  error?: string;
  started: number;
  finished?: number;
  safePoint?: string;
  logId?: string;
  hasPrompt?: boolean;
  agentId?: string;
  manifest?: ManifestItem[];
  includedBytes?: number;
  usage?: UsageRow[];
  decision?: RoutingDecisionView;
}

export interface CheckpointView {
  id: string;
  step: number;
  kind: 'entry' | 'step' | 'response_entry' | string;
  parent?: string;
  created: number;
  status: string;
  lastNodes?: string[];
  waiting?: string[];
  latest?: boolean;
}

export interface EventView {
  seq: number;
  ts: number;
  type: string;
  agentId?: string;
  data?: unknown;
}

export interface RunInfoView {
  id: string;
  workflow: string;
  status: string;
  projectId?: string;
  taskId?: string;
  latestCheckpoint?: string;
  failedReason?: string;
  created: number;
  updated: number;
}

export interface RunExplanation {
  run?: RunInfoView;
  runs: { id: string; workflow: string; status: string; created: number }[];
  graph?: GraphView;
  executions: ExecView[];
  checkpoints: CheckpointView[];
  mcpCalls: EventView[];
  events: EventView[];
  eventsTruncated?: boolean;
}

export interface RoomExportView {
  id: string;
  roomId: string;
  kind: string;
  format: string;
  content: string;
  sha256: string;
  createdAt: string;
}

export interface RunClient {
  roomExport(roomId: string, kind: 'minutes' | 'adr_candidates' | 'pr_description', title?: string): Promise<RoomExportView>;
  runExplain(taskId: string, runId?: string): Promise<RunExplanation>;
  runPrompt(taskId: string, runId: string, nodeId: string, step: number, attempt: number): Promise<string>;
  /** What the task's agents did, turn by turn, in the typed vocabulary modelled on AG-UI (internal/agui, P87). */
  taskActivity(taskId: string): Promise<AguiActivity>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export function createRunClient(t: Transport): RunClient {
  return {
    taskActivity: async (taskId) => {
      const a = await t.call<AguiActivity | null>('task.activity', { taskId });
      return { ...a, threads: arr(a?.threads).map((th) => ({ ...th, events: arr(th.events) })) };
    },
    roomExport: (roomId, kind, title = '') => t.call('room.export', { roomId, kind, title }),
    runExplain: async (taskId, runId = '') => {
      const r = await t.call<RunExplanation>('run.explain', { taskId, runId });
      return {
        ...r,
        runs: arr(r.runs),
        executions: arr(r.executions).map((x) => ({ ...x, manifest: arr(x.manifest), usage: arr(x.usage) })),
        checkpoints: arr(r.checkpoints),
        mcpCalls: arr(r.mcpCalls),
        events: arr(r.events),
        graph: r.graph ? { ...r.graph, nodes: arr(r.graph.nodes), edges: arr(r.graph.edges), entry: arr(r.graph.entry) } : undefined,
      };
    },
    runPrompt: async (taskId, runId, nodeId, step, attempt) => (await t.call<string | null>('run.prompt', { taskId, runId, nodeId, step, attempt })) ?? '',
  };
}
