// TypeScript mirror of the Go JSON the core returns (internal/core, internal/tasks, internal/room, internal/workflow,
// internal/git/host, internal/agents). Field names are copied from the Go struct tags. Where a Go struct has no tag the
// exported field name is what the encoder emits (CIState, CheckRun in the PR view). Keep this file in step with Go: the
// Wails binding generator (docs/ui.md section 11) will replace it later.

export interface DrydockEvent {
  seq: number;
  time: string;
  type: string;
  projectId?: string;
  runId?: string;
  taskId?: string;
  agentId?: string;
  data?: unknown;
}

export interface AppInfo {
  name: string;
  user: string;
  human: string;
  dataDir: string;
}

export type ProjectMode = 'local_merge' | 'pr_flow';
export type MergeStrategy = 'merge' | 'squash' | 'rebase';

export interface Project {
  id: string;
  name: string;
  repoPath: string;
  defaultBranch: string;
  mergeStrategy: MergeStrategy | string;
  mode: ProjectMode | string;
  created: number;
}

export interface Agent {
  id: string;
  projectId: string;
  name: string;
  adapter: string;
  role: string;
  model: string;
  effort: string;
  providerId: string;
  permissionMode: string;
  /** The human's standing instructions for this agent, sent with every run. */
  purpose?: string;
  status: string;
}

export interface AgentSpec {
  projectId: string;
  name: string;
  adapter: string;
  role: string;
  model?: string;
  effort?: string;
  providerId?: string;
  permissionMode?: string;
  purpose?: string;
}

/** Only the fields that are set change. The adapter cannot change: make a new agent for a different CLI. */
export interface AgentUpdate {
  id: string;
  name?: string;
  role?: string;
  purpose?: string;
  model?: string;
  effort?: string;
  permissionMode?: string;
}

/** agents.Capabilities. `false` means "not verified", never "impossible" (internal/agents/agents.go). */
export interface Capabilities {
  streaming: boolean;
  structuredOutput: boolean;
  modelSelection: boolean;
  effortControl: boolean;
  sessionResume: boolean;
  interrupt: boolean;
  mcp: boolean;
  tokenUsage: boolean;
  costReporting: boolean;
  headless: boolean;
  imageInput: boolean;
  toolCallVisibility: boolean;
  permissionModes?: string[];
  runModes?: string[];
  /** Values the adapter is known to accept; empty means the CLI reports its own choices when it starts. */
  efforts?: string[];
  models?: string[];
}

export type ConnectionStatus = 'not_installed' | 'unsupported_version' | 'unauthenticated' | 'ready' | 'degraded';

export interface Detection {
  adapter: string;
  installed: boolean;
  path?: string;
  version?: string;
  authenticated?: boolean | null;
  authDetail?: string;
  status: ConnectionStatus | string;
  problems?: string[];
  installHint?: string;
}

export interface AdapterInfo {
  id: string;
  detection: Detection;
  capabilities: Capabilities;
  error?: string;
}

export type TaskStatus =
  | 'BACKLOG'
  | 'READY'
  | 'CLAIMED'
  | 'IN_PROGRESS'
  | 'BLOCKED'
  | 'WAITING_HUMAN'
  | 'REVIEW'
  | 'VERIFICATION'
  | 'COMPLETED'
  | 'FAILED'
  | 'CANCELLED';

export interface Task {
  id: string;
  projectId: string;
  seq: number;
  title: string;
  description: string;
  status: TaskStatus | string;
  priority: number;
  assignee?: string;
  reviewer?: string;
  template?: string;
  files?: string[];
  acceptance?: string[];
  worktree?: string;
  branch?: string;
  baseBranch?: string;
  iteration: number;
  maxIterations: number;
  reviewFeedback?: string;
  verdict?: string;
  model?: string;
  effort?: string;
  costUsd: number;
  issueRef?: string;
  prRef?: string;
  runId?: string;
  blockedReason?: string;
  acceptedBy?: string;
  created: number;
  updated: number;
  started?: number;
  completed?: number;
  version: number;
  dependsOn?: string[];
}

export interface TaskCreateReq {
  projectId: string;
  title: string;
  description?: string;
  acceptance?: string[];
  priority?: number;
}

export interface Evidence {
  id: number;
  taskId: string;
  attempt: number;
  kind: string;
  passed?: boolean | null;
  summary: string;
  ref?: string;
  data?: unknown;
  created: number;
}

export interface ReviewRecord {
  id: number;
  taskId: string;
  attempt: number;
  reviewer: string;
  verdict: 'SHIP' | 'REVISE' | string;
  feedback: string;
  model?: string;
  created: number;
}

export interface RequestSpec {
  kind: 'approval' | 'question' | 'review' | string;
  title: string;
  payload?: unknown;
  actionHash?: string;
}

export interface PendingRequest {
  id: string;
  nodeId: string;
  step: number;
  spec: RequestSpec;
}

export interface RunInfo {
  id: string;
  workflow: string;
  status: string;
  projectId?: string;
  taskId?: string;
  topology: string;
  failedReason?: string;
  created: number;
  updated: number;
  version: number;
}

export interface TaskDetail {
  task: Task;
  evidence: Evidence[];
  reviews: ReviewRecord[];
  run?: RunInfo;
  pending: PendingRequest[];
  roomId?: string;
}

export type RoomKind = 'channel' | 'task' | 'dm' | 'group';

export interface Room {
  id: string;
  projectId: string;
  kind: RoomKind;
  name: string;
  topic?: string;
  mode: string;
  modeParams?: { order?: string[]; moderator?: string; max_rounds?: number; max_parallel?: number };
  taskId?: string;
  archived: boolean;
  createdBy?: string;
  createdAt: string;
  version: number;
}

export type AuthorKind = 'human' | 'agent' | 'tool' | 'system';

export interface Author {
  kind: AuthorKind;
  id: string;
  display?: string;
}

export type MsgType =
  | 'CHAT'
  | 'TASK'
  | 'DECISION'
  | 'STATUS'
  | 'RESULT'
  | 'QUESTION'
  | 'HANDOFF'
  | 'ERROR'
  | 'APPROVAL_REQUEST'
  | 'APPROVAL'
  | 'SYSTEM';

export type MarkerName = 'DECISION' | 'TODO' | 'STATUS' | 'RESULT';

export interface Marker {
  marker: MarkerName | string;
  line: number;
  text?: string;
}

export interface Message {
  id: string;
  room_id: string;
  seq: number;
  type: MsgType | string;
  author: Author;
  thread_root_id?: string;
  reply_to_id?: string;
  supersedes_id?: string;
  body: string;
  payload?: Record<string, unknown> | null;
  refs?: Record<string, unknown>;
  markers?: Marker[];
  mentions?: unknown[];
  trust: 'trusted' | 'untrusted' | string;
  run_id?: string;
  task_id?: string;
  event_seq?: number;
  created_at: string;
}

export interface Posted {
  Message?: Message;
  [k: string]: unknown;
}

export interface Unread {
  roomId: string;
  roomName: string;
  kind: RoomKind;
  unread: number;
  mentions: number;
  muted: boolean;
  lastReadSeq: number;
}

export type NeedsKind = 'approval' | 'review' | 'question' | 'failed_check' | 'watchdog_pause';

export interface NeedsYouItem {
  key: string;
  kind: NeedsKind | string;
  severity: number;
  title: string;
  detail?: string;
  projectId?: string;
  roomId?: string;
  messageId?: string;
  approvalId?: string;
  taskId?: string;
  agentId?: string;
  createdAt: string;
}

export type PresenceName =
  | 'Working'
  | 'Thinking'
  | 'Idle'
  | 'Waiting'
  | 'Completed'
  | 'Needs Attention'
  | 'Failed'
  | 'Paused';

export interface PresenceState {
  agentId: string;
  projectId?: string;
  taskId?: string;
  runId?: string;
  presence: PresenceName | string;
  label?: string;
  activity?: string;
  since: string;
  updated: string;
  seq: number;
}

// --- GitHub / pull requests (internal/git/host, internal/core/github.go) ---

export interface GitHubIssue {
  number: number;
  title: string;
  body: string;
  html_url: string;
  state: string;
  labels: string[] | null;
  author: string;
}

export interface GitHubPR {
  number: number;
  html_url: string;
  title: string;
  body: string;
  state: string;
  draft: boolean;
  merged: boolean;
  mergeable: boolean | null;
  mergeable_state: string;
  merge_commit_sha: string;
  head: { ref: string; sha: string };
  base: { ref: string };
}

export interface GitHubCheckRun {
  id: number;
  name: string;
  status: string; // queued | in_progress | completed
  conclusion: string; // success | failure | cancelled | skipped | timed_out | neutral | action_required
  html_url: string;
}

/** host.CIState has no JSON tags, so the encoder emits the Go field names. */
export interface GitHubCIState {
  Runs: GitHubCheckRun[] | null;
  Pending: boolean;
  Failed: GitHubCheckRun[] | null;
  Passed: boolean;
}

export interface GitHubReview {
  id: number;
  kind: 'review' | 'inline' | 'issue_comment' | string;
  author: string;
  association: string;
  state?: string;
  body: string;
  path?: string;
  line?: number;
  inReplyTo?: number;
  created: string;
  /** Only collaborator-level authors can trigger rework; everything else is for the human to read. */
  trusted: boolean;
}

export interface PRState {
  pr: GitHubPR;
  ci: GitHubCIState;
  reviews: GitHubReview[] | null;
  run?: RunInfo;
}

// --- connections (internal/connections, internal/core/connections.go) ---

export interface Prereq {
  tool: string;
  min?: string;
  found: boolean;
  path?: string;
  version?: string;
  ok: boolean;
  /** Plain-language guidance, never a command to run. */
  guide?: string;
}

export interface GateStatus {
  id: string;
  text: string;
  acked: boolean;
}

/** connections.Connection embeds agents.Detection, so the Detection fields are at the top level. */
export interface Connection extends Detection {
  reason?: string;
  warnings?: string[];
  gates?: GateStatus[];
  usable: boolean;
  prereqs?: Prereq[];
  transport?: string;
  compatClass?: string;
  platform: string;
}

export interface TestStep {
  id: string;
  name: string;
  status: string; // pass | fail | skipped
  reason?: string;
  detail?: string;
}

export interface MatrixCell {
  value: unknown;
  provenance: 'observed' | 'declared' | 'unknown' | string;
}

export interface ConnectionReport {
  adapter: string;
  cliVersion?: string;
  bridge?: string;
  platform: string;
  transport: string;
  form: string;
  steps: TestStep[] | null;
  capabilities: Capabilities;
  matrix: Record<string, MatrixCell> | null;
  agentMatrix?: string[];
  degraded?: string[];
  summary: string;
  /** Measured from the agent's own report; 0 when none was reported. */
  tokensUsed: number;
  ok: boolean;
  /** time.Duration in nanoseconds. */
  elapsed: number;
}

// --- providers (internal/providers) ---

export type ProviderKind = 'aggregator' | 'direct' | 'host';
export type DataClass = 'private' | 'trainable';
export type PriceTier = 'premium' | 'cheap' | 'free';

export interface Provider {
  id: string;
  name: string;
  kind: ProviderKind | string;
  baseUrl: string;
  keyRef: string;
  dataClass: DataClass | string;
  tier: PriceTier | string;
  flavor: string;
  enabled: boolean;
  maxConcurrent: number;
  hourlyCost: number;
  tlsMode: string;
  tlsFingerprint?: string;
  lanUnencrypted: boolean;
  attestations?: Record<string, boolean> | null;
  wireApis: string[] | null;
  status: string;
  lastSeen?: string;
  createdAt: string;
  updatedAt: string;
}

/** providers.NewProvider has no JSON tags: the core decodes the exported Go field names (case-insensitively). */
export interface NewProvider {
  Name: string;
  Kind: ProviderKind;
  BaseURL: string;
  APIKey?: string;
  DataClass: DataClass;
  Tier: PriceTier;
  Flavor?: string;
  MaxConcurrent?: number;
  HourlyCost?: number;
  TLSFingerprint?: string;
  Attestations?: Record<string, boolean>;
  WireAPIs?: string[];
}

export interface ProviderModel {
  providerId: string;
  id: string;
  contextLength: number;
  maxOutput: number;
  supportsTools: boolean;
  supportsReasoning: boolean;
  dataClass: string;
  tier: string;
  isFree: boolean;
  inputPerM: number;
  outputPerM: number;
  cacheReadPerM: number;
  cacheWritePerM: number;
  priceSource: 'catalog' | 'builtin' | 'user' | string;
  priceUpdatedAt?: string;
  source: string;
  probeStatus?: string;
}

/** providers.ProbeResult has no JSON tags either, so the keys are the Go names. */
export interface ProbeResult {
  ProviderID: string;
  OK: boolean;
  AuthFailed: boolean;
  /** time.Duration in nanoseconds. */
  Latency: number;
  Flavor: string;
  Models: ProviderModel[] | null;
  ServerInfo: Record<string, unknown> | null;
  Warnings: string[] | null;
  Error: string;
}

// --- usage and the agent inspector (internal/core/ops.go) ---

export interface UsageRow {
  ts: number;
  taskId: string;
  agentId: string;
  provider: string;
  model: string;
  tier: string;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  cacheWriteTokens: number;
  reasoningTokens: number;
  costUsd: number;
  source: 'reported' | 'estimated' | string;
  cacheHit: boolean;
}

export interface AgentRun {
  id: string;
  taskId: string;
  pid: number;
  cwd: string;
  status: string;
  startedAt: number;
  endedAt: number;
  error?: string;
}

export interface AgentDetail {
  agent: Agent;
  runs: AgentRun[] | null;
  usage: UsageRow[] | null;
}

export interface RoomMember {
  roomId: string;
  member: Author;
  role?: string;
  state: string;
  addedBy?: string;
  addedAt: string;
  lastReadSeq: number;
}
