// The workspace inbox, approvals and crew (internal/core/ext_inbox.go, ext_crew.go; decision P97). Like the rest of the client:
// one function per method in the core registry, and no behavior beyond turning Go's nil slices (JSON null) into empty arrays.
// Timestamps are unix milliseconds.

import type { Transport } from './transport.ts';
import type { Agent, ReviewPhase, TaskAllowRule, TaskStatus } from './types.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

/** core.ApprovalDetail (approval.get): one request as the inbox's right pane shows it. */
export interface ApprovalDetail {
  id: string;
  /** approval | question | review */
  kind: string;
  /** The security kind of a security-service approval: shell_command, agent_tool, permission_mode_escalation, ... */
  actionKind?: string;
  phase?: ReviewPhase;
  /** "permission_prompt" for an agent's tool permission request. */
  gate?: string;
  title: string;
  /** What is being decided, in plain words. */
  description: string;
  /** Why it was asked (the analyzer's words), for a security-service approval. */
  reason?: string;
  /** The tool and its input as the gate shows them: secrets redacted, a long value cut (`shortened`). */
  tool?: string;
  input?: string;
  shell: boolean;
  /** The paths and addresses the call names. */
  targets: string[];
  shortened: boolean;
  /** The person may change the input before approving (a Claude worker's whole, unredacted input). */
  editable: boolean;
  agent?: { id: string; name: string; adapter: string; model: string };
  projectId: string;
  projectName?: string;
  task?: { id: string; seq: number; title: string; status: TaskStatus | string };
  /** The run to answer it on: run.respond { runId, requestId: id }. Absent when it is not answered (a recorded mode change). */
  runId?: string;
  /** The agent process (arun_...) that asks. */
  agentRunId?: string;
  worktree?: string;
  cwd?: string;
  createdAt: number;
  expiresAt?: number;
  resolvedAt?: number;
  actionHash?: string;
  /** REQUESTED, APPROVED, REJECTED, EXPIRED, CANCELLED, CONSUMED */
  status: string;
  decidedBy?: string;
  /** For a permission_mode_escalation: from which mode to which ("" from: a new agent or template). */
  permissionMode?: { from: string; to: string };
  /** It can be approved for the rest of its task (runApproveForTask); `approveForTaskNote` says why not when it cannot. */
  approveForTask: boolean;
  approveForTaskNote?: string;
  /** The task's allow rules in force. */
  rules: TaskAllowRule[];
  /** A workflow request's own payload (a spec, a merge summary); absent for a security-service approval. */
  payload?: unknown;
}

export type RunOutcome = 'working' | 'finished' | 'failed' | 'cancelled';

/** core.RunSpan: one CLI run of an agent, clipped to the window; `end` is null while it runs and the window reaches now. */
export interface RunSpan {
  runId: string;
  start: number;
  end: number | null;
  /** The run's own times (endedAt 0 while it runs). */
  startedAt: number;
  endedAt: number;
  outcome: RunOutcome;
  taskId?: string;
  taskSeq?: number;
  taskTitle?: string;
}

/** core.WaitSpan: a time the agent waited on the person, from the request's creation until it was decided, clipped likewise. */
export interface WaitSpan {
  approvalId: string;
  kind: string;
  phase?: ReviewPhase;
  title: string;
  status: string;
  start: number;
  end: number | null;
  createdAt: number;
  resolvedAt: number;
  taskId?: string;
  taskSeq?: number;
}

/** core.AgentLane: one agent's runs and waits in the window, oldest first. */
export interface AgentLane {
  agentId: string;
  name: string;
  projectId: string;
  role: string;
  adapter: string;
  model: string;
  /** Removed from the team since; listed for its runs in the window. */
  archived?: boolean;
  runs: RunSpan[];
  waits: WaitSpan[];
}

/** core.CrewExtras (crew.extras): the accepted skills given to each agent, and each channel and group with its agents. */
export interface CrewExtras {
  skills: { agentId: string; name: string }[];
  rooms: { projectId: string; name: string; kind: string; agents: string[] }[];
}

/** core.CrewTimeline (agent.timeline). */
export interface CrewTimeline {
  since: number;
  until: number;
  agents: AgentLane[];
}

export interface InboxClient {
  /** One request's detail for the inbox's right pane. */
  approvalGet(id: string): Promise<ApprovalDetail>;
  /**
   * Approves an agent's tool permission request for the rest of its task (P97): the same call of the same agent is approved
   * without asking until the task ends. Refused for what is asked every time (deletes, pushes, merges, secrets, paths outside
   * the worktree...); approve it once with runRespond instead.
   */
  runApproveForTask(runId: string, requestId: string): Promise<void>;
  /** Ends an "approve for this task" rule: the next such call is asked again. */
  approvalRuleRemove(id: string): Promise<void>;
  /** Marks an inbox item done (a failed check, a watchdog pause, a question): it leaves the inbox. A request is answered instead. */
  needsYouAck(key: string): Promise<void>;
  /** Every project's agents, each with its projectId (the workspace's Crew page). */
  agentListAll(): Promise<Agent[]>;
  /** Each agent's runs and waits overlapping [since, until] (until omitted: now); projectId '' or omitted: every project. */
  agentTimeline(since: number, until?: number, projectId?: string): Promise<CrewTimeline>;
  /** Each agent's accepted skills and its channels and groups, in one read; projectId '': every project. */
  crewExtras(projectId: string): Promise<CrewExtras>;
}

export function createInboxClient(t: Transport): InboxClient {
  return {
    approvalGet: async (id) => {
      const d = await t.call<ApprovalDetail>('approval.get', { id });
      return { ...d, targets: arr(d.targets), rules: arr(d.rules) };
    },
    runApproveForTask: async (runId, requestId) => {
      await t.call('run.respond', { runId, requestId, decision: 'approved', scope: 'task', data: {} });
    },
    approvalRuleRemove: async (id) => {
      await t.call('approval.rule.remove', { id });
    },
    needsYouAck: async (key) => {
      await t.call('needsyou.ack', { key });
    },
    agentListAll: async () => arr(await t.call<Agent[] | null>('agent.listAll', {})),
    agentTimeline: async (since, until = 0, projectId = '') => {
      const tl = await t.call<CrewTimeline>('agent.timeline', { projectId, since, until });
      return { ...tl, agents: arr(tl?.agents).map((l) => ({ ...l, runs: arr(l.runs), waits: arr(l.waits) })) };
    },
    crewExtras: async (projectId) => {
      const x = await t.call<CrewExtras | null>('crew.extras', { projectId });
      return { skills: arr(x?.skills), rooms: arr(x?.rooms).map((r) => ({ ...r, agents: arr(r.agents) })) };
    },
  };
}
