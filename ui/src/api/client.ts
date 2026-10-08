// Typed client: one function per method in the core registry (internal/core/rpc.go). The client adds no behavior beyond
// turning Go's nil slices (JSON null) into empty arrays; errors are RpcError from the transport.

import type { Transport } from './transport';
import type {
  AdapterInfo,
  Agent,
  AgentSpec,
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
} from './types';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export interface Client {
  appInfo(): Promise<AppInfo>;
  projectCreate(name: string, repoPath: string): Promise<Project>;
  projectList(): Promise<Project[]>;
  projectGet(id: string): Promise<Project>;
  projectUpdate(id: string, mode?: ProjectMode, mergeStrategy?: MergeStrategy): Promise<Project>;
  githubIssues(projectId: string, labels?: string): Promise<GitHubIssue[]>;
  githubImportIssue(projectId: string, number: number): Promise<Task>;
  githubPullRequest(taskId: string): Promise<PRState>;
  adapterList(): Promise<AdapterInfo[]>;
  agentCreate(spec: AgentSpec): Promise<Agent>;
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
  roomList(projectId: string): Promise<Room[]>;
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
    appInfo: () => t.call('app.info'),
    projectCreate: (name, repoPath) => t.call('project.create', { name, repoPath }),
    projectList: async () => arr(await t.call<Project[] | null>('project.list')),
    projectGet: (id) => t.call('project.get', { id }),
    projectUpdate: (id, mode, mergeStrategy) => t.call('project.update', { id, mode: mode ?? '', mergeStrategy: mergeStrategy ?? '' }),
    githubIssues: async (projectId, labels = '') => arr(await t.call<GitHubIssue[] | null>('github.issues', { projectId, labels })),
    githubImportIssue: (projectId, number) => t.call('github.importIssue', { projectId, number }),
    githubPullRequest: (taskId) => t.call('github.pullRequest', { taskId }),
    adapterList: async () => arr(await t.call<AdapterInfo[] | null>('adapter.list')),
    agentCreate: (spec) => t.call('agent.create', spec),
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
    roomList: async (projectId) => arr(await t.call<Room[] | null>('room.list', { projectId })),
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
