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
  /** Drawn by the run page, not in the workflow: into an expanded subworkflow's entry nodes and out of its last ones. */
  nested?: boolean;
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
  /** The nodes with input waiting at this checkpoint: what runs first when a run goes on from it. */
  next?: string[];
  /** Where a person resumed the run after it stopped (an entry whose parent is a checkpoint of the same run). */
  resumed?: boolean;
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

/** Where a forked (time travel) or replayed run started (P123). */
export interface OriginView {
  kind: 'fork' | 'replay' | string;
  run: string;
  checkpoint: string;
  step: number;
  by?: string;
}

/** Who drives a run (run_leases): held is a lease that has not run out; mine is this Drydock's; driving is a driver here now. */
export interface LeaseView {
  held: boolean;
  mine: boolean;
  driving: boolean;
  expires?: number;
}

/** The run page's Resume, Time travel and Replay: which the core allows now, and why one is off (internal/core/ext_runcontrol.go). */
export interface RunActions {
  resume: boolean;
  timeTravel: boolean;
  replay: boolean;
  resumeWhy?: string;
  timeTravelWhy?: string;
  replayWhy?: string;
  /** The task's run a fork or a replay stops first (it waits for a person or is paused). */
  supersedes?: string;
  supersedesStatus?: string;
  lease: LeaseView;
}

/** The run a subworkflow node started at one of its executions, with its own executions. */
export interface SubrunView {
  nodeId: string;
  step: number;
  attempt: number;
  runId: string;
  status: string;
  executions: ExecView[];
  subruns: SubrunView[];
}

export interface RunControlResult {
  runId: string;
  superseded?: string;
  taskStatus: string;
  /** What of the task's status could not be put back. */
  note?: string;
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
  origin?: OriginView;
  actions?: RunActions;
  /** The nested runs of the run's subworkflow nodes (absent from an older core). */
  subruns?: SubrunView[];
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
  /** Resume a run that failed or stopped when Drydock did, from its latest checkpoint (P123). */
  runResume(taskId: string, runId: string): Promise<RunControlResult>;
  /** Start a new run from a checkpoint of the run, with the state as of that checkpoint (P123). */
  runTimeTravel(taskId: string, runId: string, checkpointId: string): Promise<RunControlResult>;
  /** Run an ended run's steps again from a checkpoint (its entry when none); steps that change something outside ask first (P123). */
  runReplay(taskId: string, runId: string, checkpointId?: string): Promise<RunControlResult>;
  /** What the task's agents did, turn by turn, in the typed vocabulary modelled on AG-UI (internal/agui, P87). */
  taskActivity(taskId: string): Promise<AguiActivity>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

const subrun = (s: SubrunView): SubrunView => ({ ...s, executions: arr(s.executions), subruns: arr(s.subruns).map(subrun) });

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
        subruns: arr(r.subruns).map(subrun),
        graph: r.graph ? { ...r.graph, nodes: arr(r.graph.nodes), edges: arr(r.graph.edges), entry: arr(r.graph.entry) } : undefined,
      };
    },
    runPrompt: async (taskId, runId, nodeId, step, attempt) => (await t.call<string | null>('run.prompt', { taskId, runId, nodeId, step, attempt })) ?? '',
    runResume: (taskId, runId) => t.call('run.resume', { taskId, runId }),
    runTimeTravel: (taskId, runId, checkpointId) => t.call('run.timeTravel', { taskId, runId, checkpointId }),
    runReplay: (taskId, runId, checkpointId = '') => t.call('run.replay', { taskId, runId, checkpointId }),
  };
}
