// Typed client: one function per method in the core registry (internal/core/rpc.go). The client adds no behavior beyond
// turning Go's nil slices (JSON null) into empty arrays; errors are RpcError from the transport.

import type { Transport } from './transport.ts';
import { createExtClient, type ExtClient } from './extClient.ts';
import { createPkgClient, type PkgClient } from './pkgClient.ts';
import { createMcpClient, type McpClient } from './mcpClient.ts';
import { createLibraryClient, type LibraryClient } from './libraryClient.ts';
import { createDockClient, type DockClient } from './dockClient.ts';
import { createRunClient, type RunClient } from './runClient.ts';
import { createAttachClient, type AttachClient } from './attachClient.ts';
import { createProviderClient, type ProviderClient } from './providerClient.ts';
import { createSetupClient, type SetupClient } from './setupClient.ts';
import { createLeadClient, type LeadClient } from './leadClient.ts';
import { createChatClient, type ChatClient } from './chatClient.ts';
import type {
  AdapterInfo,
  Agent,
  AgentDetail,
  Connection,
  ConnectionReport,
  NewProvider,
  ProbeResult,
  Provider,
  ProviderModel,
  RoomMember,
  UsageRow,
  AgentSpec,
  AgentUpdate,
  AppInfo,
  DrydockEvent,
  GitHubIssue,
  MergeStrategy,
  Message,
  NeedsYouItem,
  Posted,
  PRState,
  PresenceState,
  Project,
  ProjectMode,
  Room,
  Task,
  TaskCreateReq,
  TaskDetail,
  Unread,
} from './types.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export interface Client extends ExtClient, PkgClient, McpClient, LibraryClient, DockClient, RunClient, AttachClient, ProviderClient, SetupClient, LeadClient, ChatClient {
  appInfo(): Promise<AppInfo>;
  /** An empty repoPath makes a project without a repository (attach one later). */
  projectCreate(name: string, repoPath?: string): Promise<Project>;
  projectList(): Promise<Project[]>;
  projectGet(id: string): Promise<Project>;
  projectUpdate(id: string, mode?: ProjectMode, mergeStrategy?: MergeStrategy): Promise<Project>;
  githubIssues(projectId: string, labels?: string): Promise<GitHubIssue[]>;
  githubImportIssue(projectId: string, number: number): Promise<Task>;
  githubPullRequest(taskId: string): Promise<PRState>;
  adapterList(): Promise<AdapterInfo[]>;
  agentCreate(spec: AgentSpec): Promise<Agent>;
  agentUpdate(u: AgentUpdate): Promise<Agent>;
  /** Removes an agent from the team; its history stays. Refused while it owns unfinished tasks. */
  agentArchive(id: string): Promise<Agent>;
  agentList(projectId: string): Promise<Agent[]>;
  taskCreate(req: TaskCreateReq): Promise<Task>;
  taskList(projectId: string, statuses?: string[]): Promise<Task[]>;
  taskGet(id: string): Promise<TaskDetail>;
  taskDiff(id: string): Promise<string>;
  taskAssign(id: string, assignee: string, reviewer: string): Promise<Task>;
  /** Returns the run id. */
  taskStart(id: string): Promise<string>;
  taskCancel(id: string): Promise<void>;
  /** Answer a pending request of a run as the human. Bound to ids, never to message text. */
  runRespond(runId: string, requestId: string, decision: string, data?: unknown): Promise<void>;
  roomList(projectId: string, includeArchived?: boolean): Promise<Room[]>;
  roomGet(id: string): Promise<Room>;
  /** The thread root and its replies, in order. */
  roomThread(rootId: string): Promise<Message[]>;
  roomCreateDM(projectId: string, agentId: string): Promise<Room>;
  roomMembers(roomId: string): Promise<RoomMember[]>;
  /** The newest persisted event seq (0 if none). */
  eventsHead(): Promise<number>;
  usageList(filter: { taskId?: string; agentId?: string; limit?: number }): Promise<UsageRow[]>;
  agentGet(id: string): Promise<AgentDetail>;
  connectionList(): Promise<Connection[]>;
  connectionAcknowledge(gateId: string): Promise<void>;
  connectionTest(adapter: string): Promise<ConnectionReport>;
  providerList(): Promise<Provider[]>;
  providerCreate(p: NewProvider): Promise<Provider>;
  providerDelete(id: string): Promise<void>;
  providerSetEnabled(id: string, enabled: boolean): Promise<void>;
  providerTest(id: string): Promise<ProbeResult>;
  providerModels(id: string): Promise<ProviderModel[]>;
  roomMessages(roomId: string, afterSeq?: number, limit?: number): Promise<Message[]>;
  roomPost(roomId: string, body: string, threadRoot?: string): Promise<Posted>;
  roomMarkRead(roomId: string, seq: number): Promise<boolean>;
  roomUnread(projectId: string): Promise<Unread[]>;
  needsYouList(projectId: string): Promise<NeedsYouItem[]>;
  presenceList(): Promise<PresenceState[]>;
  eventsReplay(after: number, opts?: { types?: string[]; taskId?: string; limit?: number }): Promise<DrydockEvent[]>;
}

export function createClient(t: Transport): Client {
  return {
    ...createExtClient(t),
    ...createPkgClient(t),
    ...createMcpClient(t),
    ...createLibraryClient(t),
    ...createDockClient(t),
    ...createRunClient(t),
    ...createAttachClient(t),
    ...createProviderClient(t),
    ...createSetupClient(t),
    ...createLeadClient(t),
    ...createChatClient(t),
    appInfo: () => t.call('app.info'),
    projectCreate: (name, repoPath = '') => t.call('project.create', { name, repoPath }),
    projectList: async () => arr(await t.call<Project[] | null>('project.list')),
    projectGet: (id) => t.call('project.get', { id }),
    projectUpdate: (id, mode, mergeStrategy) => t.call('project.update', { id, mode: mode ?? '', mergeStrategy: mergeStrategy ?? '' }),
    githubIssues: async (projectId, labels = '') => arr(await t.call<GitHubIssue[] | null>('github.issues', { projectId, labels })),
    githubImportIssue: (projectId, number) => t.call('github.importIssue', { projectId, number }),
    githubPullRequest: (taskId) => t.call('github.pullRequest', { taskId }),
    adapterList: async () => arr(await t.call<AdapterInfo[] | null>('adapter.list')),
    agentCreate: (spec) => t.call('agent.create', spec),
    agentUpdate: (u) => t.call('agent.update', u),
    agentArchive: (id) => t.call('agent.archive', { id }),
    agentList: async (projectId) => arr(await t.call<Agent[] | null>('agent.list', { projectId })),
    taskCreate: (req) => t.call('task.create', req),
    taskList: async (projectId, statuses = []) => arr(await t.call<Task[] | null>('task.list', { projectId, statuses })),
    taskGet: async (id) => {
      const d = await t.call<TaskDetail>('task.get', { id });
      return { ...d, evidence: arr(d.evidence), reviews: arr(d.reviews), pending: arr(d.pending) };
    },
    taskDiff: async (id) => (await t.call<string | null>('task.diff', { id })) ?? '',
    taskAssign: (id, assignee, reviewer) => t.call('task.assign', { id, assignee, reviewer }),
    taskStart: (id) => t.call('task.start', { id }),
    taskCancel: async (id) => {
      await t.call('task.cancel', { id });
    },
    runRespond: async (runId, requestId, decision, data) => {
      await t.call('run.respond', { runId, requestId, decision, data: data ?? {} });
    },
    roomList: async (projectId, includeArchived = false) => arr(await t.call<Room[] | null>('room.list', { projectId, includeArchived })),
    roomGet: (id) => t.call('room.get', { id }),
    roomThread: async (rootId) => arr(await t.call<Message[] | null>('room.thread', { rootId })),
    roomCreateDM: (projectId, agentId) => t.call('room.createDM', { projectId, agentId }),
    roomMembers: async (roomId) => arr(await t.call<RoomMember[] | null>('room.members', { roomId })),
    eventsHead: async () => Number(await t.call<number | null>('events.head')) || 0,
    usageList: async ({ taskId = '', agentId = '', limit = 0 }) => arr(await t.call<UsageRow[] | null>('usage.list', { taskId, agentId, limit })),
    agentGet: async (id) => {
      const d = await t.call<AgentDetail>('agent.get', { id });
      return { ...d, runs: arr(d.runs), usage: arr(d.usage) };
    },
    connectionList: async () => arr(await t.call<Connection[] | null>('connection.list')),
    connectionAcknowledge: async (gateId) => {
      await t.call('connection.acknowledge', { gateId });
    },
    connectionTest: async (adapter) => {
      const r = await t.call<ConnectionReport>('connection.test', { adapter });
      return { ...r, steps: arr(r.steps), matrix: r.matrix ?? {} };
    },
    providerList: async () => arr(await t.call<Provider[] | null>('provider.list')),
    providerCreate: (p) => t.call('provider.create', p),
    providerDelete: async (id) => {
      await t.call('provider.delete', { id });
    },
    providerSetEnabled: async (id, enabled) => {
      await t.call('provider.setEnabled', { id, enabled });
    },
    providerTest: async (id) => {
      const r = await t.call<ProbeResult>('provider.test', { id });
      return { ...r, Models: arr(r.Models), Warnings: arr(r.Warnings) };
    },
    providerModels: async (id) => arr(await t.call<ProviderModel[] | null>('provider.models', { id })),
    roomMessages: async (roomId, afterSeq = 0, limit = 200) => arr(await t.call<Message[] | null>('room.messages', { roomId, afterSeq, limit })),
    roomPost: (roomId, body, threadRoot = '') => t.call('room.post', { roomId, body, threadRoot }),
    roomMarkRead: async (roomId, seq) => Boolean(await t.call('room.markRead', { roomId, seq })),
    roomUnread: async (projectId) => arr(await t.call<Unread[] | null>('room.unread', { projectId })),
    needsYouList: async (projectId) => arr(await t.call<NeedsYouItem[] | null>('needsyou.list', { projectId })),
    presenceList: async () => arr(await t.call<PresenceState[] | null>('presence.list')),
    eventsReplay: async (after, opts = {}) =>
      arr(await t.call<DrydockEvent[] | null>('events.replay', { after, types: opts.types ?? [], taskId: opts.taskId ?? '', limit: opts.limit ?? 0 })),
  };
}
