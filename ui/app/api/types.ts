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
  /** Empty while no repository is attached. */
  repoPath: string;
  /** False for a project made without a repository (attach one in Project settings). Absent from older cores: treat as true. */
  hasRepo?: boolean;
  defaultBranch: string;
  mergeStrategy: MergeStrategy | string;
  mode: ProjectMode | string;
  /** The human's description of the project, given to every agent as standing context (at most PROJECT_BRIEF_MAX). */
  brief?: string;
  created: number;
}

/** core.MaxBriefLen */
export const PROJECT_BRIEF_MAX = 8000;

/**
 * The repository configuration guard's view of a project's repository (core.GitConfig, docs/security.md s3): whether its own
 * git configuration, hooks and info/attributes are the state a person last trusted. `changed` names the items that differ
 * (config keys such as `filter.x.clean`, `hooks/<file>`, `info/attributes`), never their values; `fingerprint` is the state
 * trusting would accept.
 */
export interface GitConfigStatus {
  trusted: boolean;
  changed: string[];
  fingerprint: string;
}

/**
 * What an agent CLI would act on in one place (core.AgentConfigFinding, project.agentConfig, docs/security.md s3 item 2b): the
 * settings, hooks, plugins, MCP servers, commands or `.env` files its working folder brings. Drydock does not start that CLI
 * where they are not trusted. `where` is "the repository" or a task's worktree; `files` are names relative to that folder
 * (text from the repository: shown as text, never as markup); `fingerprint` is the state trusting accepts (names, kinds, sizes
 * and content digests of exactly those files).
 */
export interface AgentConfigFinding {
  adapter: string;
  where: string;
  /** Empty for the repository. */
  taskId: string;
  files: string[];
  fingerprint: string;
  trusted: boolean;
}

/** What deleting a project did (project.delete). */
export interface ProjectDeleted {
  name: string;
  /** Drydock's own worktrees of the project that were removed. */
  worktreesRemoved: number;
  /** What could not be cleaned up (a worktree folder in use, for example); the project itself is gone. */
  warnings: string[];
}

/** Whether a project has a repository (older cores do not send hasRepo; every project had one then). */
export const projectHasRepo = (p: Pick<Project, 'hasRepo'> | undefined): boolean => !!p && p.hasRepo !== false;

// ---- GitHub account (internal/core/github_account.go, internal/git/host/account.go) ----

export interface GitHubStatus {
  installed: boolean;
  loggedIn: boolean;
  /** GitHub (or the network) did not answer in time; gh may well be signed in. */
  unreachable?: boolean;
  login: string;
  error?: string;
}

/** A repository the signed-in user can reach. Name and description are outside text: shown, never acted on. */
export interface GitHubRepo {
  fullName: string;
  owner: string;
  name: string;
  description: string;
  private: boolean;
  archived: boolean;
  fork: boolean;
  defaultBranch: string;
  htmlUrl: string;
  pushedAt: string;
}

export interface CloneResult {
  path: string;
  defaultBranch: string;
}

// ---- project.setup (internal/core/project_setup.go) ----

export type SetupRepoKind = 'none' | 'local' | 'github';

export interface SetupRepo {
  kind: SetupRepoKind;
  /** local */
  path?: string;
  /** local: make a Git repository at path first when there is none (decision P58); an existing repository is used as it is. */
  init?: boolean;
  /** github: owner/name */
  github?: string;
  /** github: the parent folder (default github.cloneDir) */
  cloneDir?: string;
}

/** One agent of the wizard: a prebuilt catalog entry, a saved template, or a custom agent (name, role, adapter). */
export interface SetupAgent {
  prebuilt?: string;
  template?: string;
  name?: string;
  role?: string;
  adapter?: string;
  model?: string;
  effort?: string;
  permissionMode?: string;
  purpose?: string;
  practices?: boolean;
  /** Library or catalog server ids to grant (custom agents). */
  servers?: string[];
  /**
   * The person's confirmation of a permission mode that lets the agent act without asking (bypassPermissions, auto, dontAsk).
   * Sent only after they ticked the box in the form, and only for such a mode; without it the core does not add the agent
   * (core.NeedsConfirmationError becomes a warning of the result).
   */
  confirmSkipsApprovals?: boolean;
}

export interface SetupRequest {
  name: string;
  brief: string;
  repo: SetupRepo;
  mode: ProjectMode;
  libraryServers: string[];
  catalogServers: string[];
  agents: SetupAgent[];
  installSkills: boolean;
}

export interface SetupResult {
  project: Project;
  agents: Agent[];
  clone?: CloneResult;
  /** What did not work after the project was created (the project exists). */
  warnings: string[];
}

/** core.MaxSetupAgents */
export const SETUP_MAX_AGENTS = 20;

// ---- MCP catalog (internal/core/ext_mcpcatalog.go) ----

export interface McpCatalogEntry {
  id: string;
  display: string;
  url: string;
  description: string;
  auth: 'none' | 'bearer' | 'oauth' | string;
  authOptional: boolean;
  readOnlyTools: string[];
  recommendedRoles: string[];
  trust: string;
  inLibrary: boolean;
  libraryId?: string;
}

export interface McpCatalogAdded {
  server: { id: string; display: string };
  created: boolean;
  /** What the human still has to do (sign in, add a token). */
  next?: string;
}

// ---- Prebuilt agents (internal/core/ext_agentcatalog.go, internal/agentcatalog) ----

export type AgentCategory = 'management' | 'engineering' | 'design' | 'docs' | 'quality' | 'security';
export const AGENT_CATEGORIES: AgentCategory[] = ['management', 'engineering', 'design', 'docs', 'quality', 'security'];

export interface SkillRef {
  registry: string;
  name: string;
  ref: string;
}

export interface AdapterAvailability {
  adapter: string;
  preferred: boolean;
  installed: boolean;
  usable: boolean;
  status: string;
  reason?: string;
  /** The WSL distro where the CLI is ready when it is not usable on Windows (decision P58): available there. */
  wslDistro?: string;
}

export interface PrebuiltAgent {
  id: string;
  name: string;
  role: string;
  category: AgentCategory | string;
  summary: string;
  description: string;
  purpose: string;
  preferred: { adapter: string; model: string; effort: string; permissionMode: string; practices?: boolean | null };
  alternatives: { adapter: string; model: string; effort: string }[];
  /** MCP catalog server ids. */
  mcp: string[];
  skills: SkillRef[];
  tags: string[];
  availability: {
    /** The preferred CLI first, then the alternatives. */
    adapters: AdapterAvailability[];
    preferredUsable: boolean;
    /** Each MCP catalog server: its full name, publisher, logo key and summary (never only the id). */
    mcp: { id: string; display?: string; publisher?: string; brand?: string; summary?: string; auth?: string; inLibrary: boolean; libraryId?: string }[];
    /** Each skill: what its SKILL.md says at the pin, the source repository, a link at the pin and the license. */
    skills: (SkillRef & { description?: string; license?: string; repo?: string; sourceUrl?: string; imported: boolean; status?: string; samePin: boolean })[];
  };
}

export interface PrebuiltApplyReq {
  id: string;
  projectId: string;
  name?: string;
  adapter?: string;
  model?: string;
  effort?: string;
  permissionMode?: string;
  installSkills: boolean;
  connectMcp: boolean;
  /** The person's confirmation of a permission mode that lets the agent act without asking (see SetupAgent). */
  confirmSkipsApprovals?: boolean;
}

export interface SkippedItem {
  id?: string;
  name?: string;
  reason: string;
}

export interface PrebuiltApplied {
  id: string;
  agent: Agent;
  mcp: { granted: string[]; skipped: SkippedItem[] };
  skills: { assigned: string[]; shared: string[]; pending: SkippedItem[]; failed: SkippedItem[] };
  notes: string[];
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
  /** Whether the Claude best-practices text is added to this agent's system prompt (only for the claude adapter). */
  practices?: boolean;
  status: string;
  /** Models the model router may choose between for this agent's turns; empty: always `model`. */
  routeModels?: RouteModel[];
  /** Where the CLI runs: '' follows the project, 'native' on Windows, 'wsl' inside the distro `wslDistro`. */
  runMode?: string;
  wslDistro?: string;
}

export interface RouteModel {
  model: string;
  tier: 'cheap' | 'premium';
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
  practices?: boolean;
  /** Project skills the new agent is given. */
  skills?: string[];
  /** '' follows the project, 'native', or 'wsl' with `wslDistro`. */
  runMode?: string;
  wslDistro?: string;
  /** The person's confirmation of a permission mode that lets the agent act without asking (see SetupAgent). */
  confirmSkipsApprovals?: boolean;
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
  practices?: boolean;
  /** Replaces the routed models; an empty list turns routing off. */
  routeModels?: RouteModel[];
  /** Project skills to give the agent, and to take away from it. */
  addSkills?: string[];
  removeSkills?: string[];
  runMode?: string;
  wslDistro?: string;
  /** The person's confirmation of a change into a permission mode that lets the agent act without asking (see SetupAgent). */
  confirmSkipsApprovals?: boolean;
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
  /** Not installed on Windows but ready in this WSL distro (decision P58): available there. */
  wslDistro?: string;
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
  /**
   * "lead:N" for a follow-up the team lead split off (N is how many steps it is from a task a person made); absent for a task a
   * person made. Such a task waits in the backlog until a person moves it on, even under automatic scheduling. The screens read
   * it through leadStep (lib/task-origin.ts), which takes any value: this is whatever the core sent.
   */
  origin?: string;
}

export interface TaskCreateReq {
  projectId: string;
  title: string;
  description?: string;
  acceptance?: string[];
  priority?: number;
}

/**
 * core.TaskListDefault: task.list returns only this many of the newest tasks. A screen that shows the list as the whole says so
 * when the project has more (task.count), see lib/task-total.ts.
 */
export const TASK_LIST_LIMIT = 500;

/** task.count: how many tasks match (a project's tasks, or those in the given statuses). */
export interface TaskCount {
  total: number;
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
  /** Which gate a review request is (from the pipeline): accept the spec, review and merge, or review a plan. */
  phase?: ReviewPhase;
}

/** The gate a review request is (workflow.RequestSpec.Phase): "spec", "merge" or "plan". */
export type ReviewPhase = 'spec' | 'merge' | 'plan' | string;

/**
 * security.TaskAllowRule: a person's "approve for this task" of one call of an agent (decision P97). Later requests of the same
 * task, agent and tool that are the same call are approved without asking until the task ends: for a shell tool the same
 * programs with the same arguments (`commands`), for any other tool the same input (`inputDigest`).
 */
export interface TaskAllowRule {
  id: string;
  projectId: string;
  taskId: string;
  agentId: string;
  /** The approval's tool, "<cli>:<tool>" ("claude:Bash"). */
  tool: string;
  /** A shell command's first program ("go"). */
  program?: string;
  commands?: { program: string; args: string[] }[];
  inputDigest?: string;
  /** What it allows as a person reads it: the command, or where the call goes. */
  summary: string;
  /** The request it was approved on. */
  approvalId: string;
  createdBy: string;
  createdAt: number;
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
  /** The person's "approve for this task" rules in force for the task's agents (P97); they end with the task. taskGet always fills it. */
  allowRules?: TaskAllowRule[];
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

export type NeedsKind = 'approval' | 'review' | 'question' | 'failed_check' | 'watchdog_pause' | 'chat_permission';

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
  /** A chat whose CLI waits for the person to allow a tool call (kind chat_permission). */
  chatId?: string;
  createdAt: string;
  /** What a task's item is about (UI review P1-1): the assignee, the newest diff's size, the checks and the reviewer's verdict. */
  context?: NeedsContext;
  /** The task's number in its project (with the project, its key); absent for an item of no task. */
  taskSeq?: number;
  /** For kind review: which gate it is, from the request that made it. */
  phase?: ReviewPhase;
  /** The name of the agent the item is about (agentId): the one asking, or the task's assignee for a review. */
  agentName?: string;
  /** When an approval lapses unanswered (RFC 3339, like createdAt). */
  expiresAt?: string;
}

export interface NeedsContext {
  agent?: string;
  hasDiff: boolean;
  files: number;
  insertions: number;
  deletions: number;
  checksPassed: number;
  checksTotal: number;
  verdict?: string;
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
  /** A check run's name, or a commit status's context. */
  name: string;
  status: string; // queued | in_progress | completed (a commit status: pending | completed)
  conclusion: string; // success | failure | cancelled | skipped | timed_out | neutral | action_required (a commit status: success | failure | error)
  html_url: string;
  /** check_run, or status (the older commit status API that outside CI services use). Both count as CI. */
  kind?: 'check_run' | 'status' | string;
  description?: string;
}

/** host.CIState has no JSON tags, so the encoder emits the Go field names. */
export interface GitHubCIState {
  Runs: GitHubCheckRun[] | null;
  Pending: boolean;
  Failed: GitHubCheckRun[] | null;
  Passed: boolean;
  /** Required checks that had not reported when the CI gate stopped waiting (only from the gate, not the live view). */
  Missing?: string[] | null;
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
  /** The adapter id for the Windows install; "<adapter>@wsl:<distro>" for one inside a WSL distro (its own Connection). */
  key?: string;
  /** native | wsl */
  runMode?: string;
  /** The WSL distro (run mode wsl). */
  distro?: string;
  /** What the probe found in the distro (run mode wsl). */
  wsl?: WSLFacts;
  /** The CLI's own sign-in command as a person types it ("claude auth login"; the manifest's auth.login), which the card's
   * Sign in runs in a terminal (decision P50); absent when the CLI names none Drydock can start. */
  signIn?: string;
  /** The account the CLI's own status command says it is signed in as (its "email"); absent when it names none. */
  account?: string;
  /** The adapter of a custom manifest (a CLI you described, not a vendor's): listed after the vendors' CLIs. */
  custom?: boolean;
}

/** connections.WSLFacts: the distro behind a WSL Connection. */
export interface WSLFacts {
  distro: string;
  default?: boolean;
  /** 1 or 2 as wsl.exe -l -v reports it; 0 or missing when unknown. */
  wslVersion?: number;
  wsl2: boolean;
  kernel?: string;
  /** Windows programs can be started from inside: Drydock's tools need it. */
  interop: boolean;
  /** nat, mirrored, ... ("" when not reported). */
  netMode?: string;
  mountRoot?: string;
  home?: string;
  setsid: boolean;
  git?: string;
  notes?: string[];
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
  /** The Connection tested (Connection.key), its run mode and WSL distro. */
  key?: string;
  runMode?: string;
  distro?: string;
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

/** What keeps an agent inside its project, in plain words. "open" means its permission mode lets it act without asking. */
export interface Isolation {
  level: 'guarded' | 'open';
  summary: string;
  details: string[] | null;
}

/** The sum over ALL of an agent's usage rows (core.UsageTotals); `AgentDetail.usage` lists only the newest ones. */
export interface UsageTotals {
  /** How many usage rows the agent has in all. */
  rows: number;
  costUsd: number;
  inputTokens: number;
  outputTokens: number;
  /** How many of the rows have a cost that was estimated, not reported. */
  estimated: number;
}

/** core.AgentDetail.Usage holds this many of the newest usage rows (internal/core/ops.go). */
export const AGENT_USAGE_ROWS = 50;

export interface AgentDetail {
  agent: Agent;
  runs: AgentRun[] | null;
  /** The newest AGENT_USAGE_ROWS rows. Sum nothing from them: `usageTotals` covers every row. */
  usage: UsageRow[] | null;
  /** Sums over every usage row of the agent. A core that does not send them (older) shows as zero rows. */
  usageTotals: UsageTotals;
  isolation?: Isolation;
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
