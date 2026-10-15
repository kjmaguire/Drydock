// Typed client: one function per method in the core registry (internal/core/rpc.go). The client adds no behavior beyond
// turning Go's nil slices (JSON null) into empty arrays; errors are RpcError from the transport.

import type { Transport } from './transport.ts';
import { createExtClient, type ExtClient } from './extClient.ts';
import { createPkgClient, type PkgClient } from './pkgClient.ts';
import { createMcpClient, type McpClient } from './mcpClient.ts';
import { createLibraryClient, type LibraryClient } from './libraryClient.ts';
import { createDockClient, type DockClient, type TerminalInfo } from './dockClient.ts';
import { createRunClient, type RunClient } from './runClient.ts';
import { createAttachClient, type AttachClient } from './attachClient.ts';
import { createProviderClient, type ProviderClient } from './providerClient.ts';
import { createSetupClient, type SetupClient } from './setupClient.ts';
import { createLeadClient, type LeadClient } from './leadClient.ts';
import { createChatClient, type ChatClient } from './chatClient.ts';
import { createWSLClient, type WSLClient } from './wslClient.ts';
import { createRunnersClient, type RunnersClient } from './runnersClient.ts';
import { createSecurityClient, type SecurityClient } from './securityClient.ts';
import { createFolderClient, type FolderClient } from './folderClient.ts';
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
  GitConfigStatus,
  ProjectDeleted,
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
const gitConfig = (v: GitConfigStatus | null): GitConfigStatus => ({ trusted: v?.trusted === true, changed: arr(v?.changed), fingerprint: v?.fingerprint ?? '' });

export interface Client extends ExtClient, PkgClient, McpClient, LibraryClient, DockClient, RunClient, AttachClient, ProviderClient, SetupClient, LeadClient, ChatClient, WSLClient, SecurityClient, RunnersClient, FolderClient {
  appInfo(): Promise<AppInfo>;
  /** An empty repoPath makes a project without a repository (attach one later). */
  projectCreate(name: string, repoPath?: string): Promise<Project>;
  projectList(): Promise<Project[]>;
  projectGet(id: string): Promise<Project>;
  projectUpdate(id: string, mode?: ProjectMode, mergeStrategy?: MergeStrategy): Promise<Project>;
  /**
   * Deletes a project from Drydock (decision P51). `confirmName` must be the project's name: the core checks it too. Refused
   * while work runs in the project. The repository folder is never touched.
   */
  projectDelete(id: string, confirmName: string): Promise<ProjectDeleted>;
  /** Whether the repository's own git configuration is the one a person last trusted (errors for a project without one). */
  projectGitConfig(id: string): Promise<GitConfigStatus>;
  /** Trusts exactly the state with this fingerprint (the one shown); a stale fingerprint is an error. Returns the new status. */
  projectTrustGitConfig(id: string, fingerprint: string): Promise<GitConfigStatus>;
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
  /** With prompt, also one tiny prompt on the adapter's cheapest model (uses the person's account). */
  /** distro tests the install inside that WSL distro (its own Connection) instead of the Windows one. */
  connectionTest(adapter: string, prompt?: boolean, distro?: string): Promise<ConnectionReport>;
  /** Starts the CLI's own sign-in in a terminal of the dock (inside the WSL distro for that install) and returns the terminal. */
  connectionSignIn(adapter: string, distro?: string): Promise<TerminalInfo>;
  /** Checks one Connection again now, its sign-in included (the card's "Check again", and when its sign-in ends). */
  connectionCheck(adapter: string, distro?: string): Promise<Connection>;
  providerList(): Promise<Provider[]>;
  providerCreate(p: NewProvider): Promise<Provider>;
  providerDelete(id: string): Promise<void>;
  providerSetEnabled(id: string, enabled: boolean): Promise<void>;
  providerTest(id: string): Promise<ProbeResult>;
  providerModels(id: string): Promise<ProviderModel[]>;
  /** Forward: the messages after afterSeq, oldest first (catching up). */
  roomMessages(roomId: string, afterSeq?: number, limit?: number): Promise<Message[]>;
  /** The newest limit messages of a room, oldest first. */
  roomMessagesLatest(roomId: string, limit?: number): Promise<Message[]>;
  /** The limit messages just before beforeSeq (which must be at least 1), oldest first. */
  roomMessagesBefore(roomId: string, beforeSeq: number, limit?: number): Promise<Message[]>;
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
    ...createWSLClient(t),
    ...createSecurityClient(t),
    ...createRunnersClient(t),
    ...createFolderClient(t),
    appInfo: () => t.call('app.info'),
    projectCreate: (name, repoPath = '') => t.call('project.create', { name, repoPath }),
    projectList: async () => arr(await t.call<Project[] | null>('project.list')),
    projectGet: (id) => t.call('project.get', { id }),
    projectUpdate: (id, mode, mergeStrategy) => t.call('project.update', { id, mode: mode ?? '', mergeStrategy: mergeStrategy ?? '' }),
    projectDelete: async (id, confirmName) => {
      const r = await t.call<ProjectDeleted | null>('project.delete', { id, confirmName });
      return { name: r?.name ?? '', worktreesRemoved: r?.worktreesRemoved ?? 0, warnings: arr(r?.warnings) };
    },
    projectGitConfig: async (id) => gitConfig(await t.call<GitConfigStatus | null>('project.gitConfig', { id })),
    projectTrustGitConfig: async (id, fingerprint) => gitConfig(await t.call<GitConfigStatus | null>('project.trustGitConfig', { id, fingerprint })),
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
    connectionTest: async (adapter, prompt = false, distro = '') => {
      const r = await t.call<ConnectionReport>('connection.test', { adapter, prompt, distro });
      return { ...r, steps: arr(r.steps), matrix: r.matrix ?? {} };
    },
    connectionSignIn: (adapter, distro = '') => t.call<TerminalInfo>('connection.signIn', { adapter, distro }),
    connectionCheck: (adapter, distro = '') => t.call<Connection>('connection.check', { adapter, distro }),
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
    roomMessagesLatest: async (roomId, limit = 200) => arr(await t.call<Message[] | null>('room.messages', { roomId, latest: true, limit })),
    roomMessagesBefore: async (roomId, beforeSeq, limit = 200) => {
      // beforeSeq 0 would be a forward read from the start: there is nothing before seq 1
      if (!(beforeSeq >= 1)) throw new Error(`room.messages: beforeSeq must be at least 1 (got ${beforeSeq})`);
      return arr(await t.call<Message[] | null>('room.messages', { roomId, beforeSeq, limit }));
    },
    roomPost: (roomId, body, threadRoot = '') => t.call('room.post', { roomId, body, threadRoot }),
    roomMarkRead: async (roomId, seq) => Boolean(await t.call('room.markRead', { roomId, seq })),
    roomUnread: async (projectId) => arr(await t.call<Unread[] | null>('room.unread', { projectId })),
    needsYouList: async (projectId) => arr(await t.call<NeedsYouItem[] | null>('needsyou.list', { projectId })),
    presenceList: async () => arr(await t.call<PresenceState[] | null>('presence.list')),
    eventsReplay: async (after, opts = {}) =>
      arr(await t.call<DrydockEvent[] | null>('events.replay', { after, types: opts.types ?? [], taskId: opts.taskId ?? '', limit: opts.limit ?? 0 })),
  };
}
