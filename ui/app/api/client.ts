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
import { createRepoMapClient, type RepoMapClient } from './repomapClient.ts';
import { createSecurityClient, type SecurityClient } from './securityClient.ts';
import { createFolderClient, type FolderClient } from './folderClient.ts';
import { createLocalClient, type LocalClient } from './localClient.ts';
import { createShellClient, type ShellClient } from './shellClient.ts';
import { createKeysClient, type KeysClient } from './keysClient.ts';
import { createInboxClient, type InboxClient } from './inboxClient.ts';
import { createTaskDataClient, type TaskDataClient } from './taskDataClient.ts';
import { createControlClient, type ControlClient } from './controlClient.ts';
import { createRoomClient, type RoomClient } from './roomClient.ts';
import type {
  AdapterInfo,
  Agent,
  AgentConfigFinding,
  AgentDetail,
  Connection,
  ConnectionHistory,
  ConnectionReport,
  CustomCLIs,
  NewProvider,
  ProbeResult,
  Provider,
  ProviderModel,
  RoomMember,
  UsageRow,
  AgentSpec,
  AgentUpdate,
  UpdatedAgent,
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
  TaskCount,
  TaskCreateReq,
  TaskDetail,
  TaskFilter,
  Unread,
  UsageTotals,
} from './types.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const gitConfig = (v: GitConfigStatus | null): GitConfigStatus => ({ trusted: v?.trusted === true, changed: arr(v?.changed), fingerprint: v?.fingerprint ?? '' });
const customs = (v: CustomCLIs | null): CustomCLIs => ({ dir: v?.dir ?? '', files: arr(v?.files) });
const agentConfig = (v: AgentConfigFinding[] | null | undefined): AgentConfigFinding[] => arr(v).map((f) => ({ ...f, files: arr(f.files), taskId: f.taskId ?? '', trusted: f.trusted === true }));
const usageTotals = (v: UsageTotals | null | undefined): UsageTotals => ({
  rows: Number(v?.rows) || 0,
  costUsd: Number(v?.costUsd) || 0,
  inputTokens: Number(v?.inputTokens) || 0,
  outputTokens: Number(v?.outputTokens) || 0,
  estimated: Number(v?.estimated) || 0,
});

export interface Client extends ExtClient, PkgClient, McpClient, LibraryClient, DockClient, RunClient, AttachClient, ProviderClient, SetupClient, LeadClient, ChatClient, WSLClient, SecurityClient, RunnersClient, FolderClient, RepoMapClient, LocalClient, ShellClient, KeysClient, InboxClient, TaskDataClient, ControlClient, RoomClient {
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
  /**
   * What each agent CLI that acts on files in its working folder would find in the project's repository and in the worktrees of
   * its unfinished tasks (settings, hooks, plugins, MCP servers, `.env` files). Only places with something in them; empty when
   * nothing is found. The core does not start such a CLI where this is not trusted.
   */
  projectAgentConfig(id: string): Promise<AgentConfigFinding[]>;
  /** Trusts the configuration with this fingerprint for this adapter (a human only), the one that was shown. Returns the findings read again. */
  projectTrustAgentConfig(id: string, adapter: string, fingerprint: string): Promise<AgentConfigFinding[]>;
  githubIssues(projectId: string, labels?: string): Promise<GitHubIssue[]>;
  githubImportIssue(projectId: string, number: number): Promise<Task>;
  githubPullRequest(taskId: string): Promise<PRState>;
  adapterList(): Promise<AdapterInfo[]>;
  agentCreate(spec: AgentSpec): Promise<Agent>;
  agentUpdate(u: AgentUpdate): Promise<UpdatedAgent>;
  /** Removes an agent from the team; its history stays. Refused while it owns unfinished tasks. */
  agentArchive(id: string): Promise<Agent>;
  agentList(projectId: string): Promise<Agent[]>;
  taskCreate(req: TaskCreateReq): Promise<Task>;
  /**
   * The newest TASK_LIST_LIMIT tasks (newest first), whatever the project has in all: ask taskCount how many there are. An empty
   * projectId is every project; `filter` narrows to an agent's tasks, a label, or the tasks that ended since a time.
   */
  taskList(projectId: string, statuses?: string[], filter?: TaskFilter): Promise<Task[]>;
  /** How many tasks the project has (those in the given statuses, and matching `filter`, when any are named). */
  taskCount(projectId: string, statuses?: string[], filter?: TaskFilter): Promise<TaskCount>;
  taskGet(id: string): Promise<TaskDetail>;
  /**
   * The task's diff evidence as text: the one with `evidenceId` (the core refuses an id that is not a diff of the task), or the
   * newest when none is given. A screen that shows a patch beside an Approve button asks for the evidence its card is keyed on.
   */
  taskDiff(id: string, evidenceId?: number): Promise<string>;
  /**
   * Hands the task to an agent, also while its run is active (its current turn stops and the new agent goes on in the same
   * worktree), or to the person themselves (assignee = app.info's human: no agent works on it). Without a reviewer the task
   * keeps its own unless that is the new worker; then the project's reviewer-role agent reviews, else the first other agent.
   */
  taskAssign(id: string, assignee: string, reviewer?: string): Promise<Task>;
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
  /** An install's kept Test connection runs, newest first, and its capability matrix (decision P132). */
  connectionHistory(adapter: string, distro?: string): Promise<ConnectionHistory>;
  /** A person's own CLIs (decision P115): the folder they are kept in and every file, with why one did not load. */
  connectionCustomList(): Promise<CustomCLIs>;
  /** The template of a custom manifest, made ready for an id. */
  connectionCustomTemplate(id: string): Promise<string>;
  /** Adds a person's own CLI from its manifest (checked strictly by the core); its Connection is returned. */
  connectionCustomAdd(yaml: string): Promise<Connection>;
  /** Removes a person's own CLI (refused while an agent runs on it). */
  connectionCustomRemove(id: string): Promise<CustomCLIs>;
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
    ...createRepoMapClient(t),
    ...createLocalClient(t),
    ...createShellClient(t),
    ...createKeysClient(t),
    ...createInboxClient(t),
    ...createTaskDataClient(t),
    ...createControlClient(t),
    ...createRoomClient(t),
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
    projectAgentConfig: async (id) => agentConfig(await t.call<AgentConfigFinding[] | null>('project.agentConfig', { id })),
    projectTrustAgentConfig: async (id, adapter, fingerprint) => agentConfig(await t.call<AgentConfigFinding[] | null>('project.trustAgentConfig', { id, adapter, fingerprint })),
    githubIssues: async (projectId, labels = '') => arr(await t.call<GitHubIssue[] | null>('github.issues', { projectId, labels })),
    githubImportIssue: (projectId, number) => t.call('github.importIssue', { projectId, number }),
    githubPullRequest: (taskId) => t.call('github.pullRequest', { taskId }),
    adapterList: async () => arr(await t.call<AdapterInfo[] | null>('adapter.list')),
    agentCreate: (spec) => t.call('agent.create', spec),
    agentUpdate: (u) => t.call('agent.update', u),
    agentArchive: (id) => t.call('agent.archive', { id }),
    agentList: async (projectId) => arr(await t.call<Agent[] | null>('agent.list', { projectId })),
    taskCreate: (req) => t.call('task.create', req),
    taskList: async (projectId, statuses = [], filter) => arr(await t.call<Task[] | null>('task.list', { projectId, statuses, ...filter })),
    taskCount: async (projectId, statuses = [], filter) => ({ total: Number((await t.call<TaskCount | null>('task.count', { projectId, statuses, ...filter }))?.total) || 0 }),
    taskGet: async (id) => {
      const d = await t.call<TaskDetail>('task.get', { id });
      return { ...d, evidence: arr(d.evidence), reviews: arr(d.reviews), pending: arr(d.pending), allowRules: arr(d.allowRules) };
    },
    taskDiff: async (id, evidenceId) => (await t.call<string | null>('task.diff', evidenceId === undefined ? { id } : { id, evidenceId })) ?? '',
    taskAssign: (id, assignee, reviewer = '') => t.call('task.assign', { id, assignee, reviewer }),
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
      return { ...d, runs: arr(d.runs), usage: arr(d.usage), usageTotals: usageTotals(d.usageTotals) };
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
    connectionHistory: async (adapter, distro = '') => {
      const h = await t.call<ConnectionHistory | null>('connection.history', { adapter, distro });
      return { key: h?.key ?? adapter, runs: arr(h?.runs), matrix: h?.matrix ?? {}, matrixAt: h?.matrixAt ?? 0 };
    },
    connectionCustomList: async () => customs(await t.call<CustomCLIs>('connection.custom.list', {})),
    connectionCustomTemplate: async (id) => (await t.call<{ yaml: string }>('connection.custom.template', { id })).yaml,
    connectionCustomAdd: (yaml) => t.call<Connection>('connection.custom.add', { yaml }),
    connectionCustomRemove: async (id) => customs(await t.call<CustomCLIs>('connection.custom.remove', { id })),
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
