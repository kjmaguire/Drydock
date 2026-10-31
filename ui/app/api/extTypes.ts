// Types for the Slice 2-5 methods (internal/core/ext_*.go). Field names come from the Go struct tags. Structs without JSON
// tags in Go (router.RoutingDecision, probe results) arrive with their Go field names and are typed that way here.

// --- budgets (internal/budget) ---
export type BudgetScope = 'project' | 'workflow' | 'task' | 'run' | 'agent';
export interface Budget {
  id: string;
  scope: BudgetScope | string;
  /**
   * A concrete id, or "*" for a ceiling that applies to every task, run or agent (each judged on its own). For the workflow
   * scope (P118), the workflow's name; its projectId keeps the count to that project's runs.
   */
  scopeId: string;
  projectId?: string;
  limitUsd?: number | null;
  limitTokens?: number | null;
  limitTurns?: number | null;
  warnPct: number;
  enabled: boolean;
  note?: string;
  version: number;
  createdAt: number;
  updatedAt: number;
  updatedBy: string;
}
export interface BudgetSpend {
  rows: number;
  reportedUsd: number;
  estimatedUsd: number;
  /** Rows with no reported cost and no price: counted, never priced at zero. */
  unknownCostRows: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  turns: number;
}
export interface BudgetStatus {
  budget: Budget;
  instanceId: string;
  spend: BudgetSpend;
  pctUsd?: number | null;
  pctTokens?: number | null;
  pctTurns?: number | null;
  pct: number;
  binding?: string;
  state: 'ok' | 'warning' | 'exceeded' | string;
  /** False when a dollar limit is set and some rows have unknown cost: the dollar percentage is a lower bound. */
  costComplete: boolean;
  costLabel: string;
  message: string;
}
export interface BudgetRow {
  budget: Budget;
  status?: BudgetStatus;
}
export interface BudgetSet {
  id?: string;
  scope: BudgetScope | string;
  scopeId: string;
  projectId?: string;
  limitUsd?: number | null;
  limitTokens?: number | null;
  limitTurns?: number | null;
  warnPct?: number;
  enabled?: boolean;
  note?: string;
  version?: number;
}
export interface BudgetResumeReq {
  runId: string;
  requestId?: string;
  decision?: 'approved' | 'rejected';
  limitUsd?: number;
  limitTokens?: number;
  limitTurns?: number;
}
export interface BudgetResumed {
  runId: string;
  requestId: string;
  decision: string;
  raised: string[];
  by: string;
}
/** The payload of the approval request a budget pause raises (internal/budget/gate.go). */
export interface BudgetPausePayload {
  gate: 'budget';
  message: string;
  exceeded: BudgetStatus[];
  resume?: string;
  node?: string;
  taskId?: string;
}

// --- evals (internal/evals) ---
export interface EvalsOptions {
  projectId?: string;
  sinceMs?: number;
  untilMs?: number;
  minN?: number;
}
export interface Ratio {
  num: number;
  den: number;
  rate?: number | null;
}
export interface EvalsReport {
  generatedAt?: number;
  quality?: {
    started: number;
    accepted: number;
    failed: number;
    cancelled: number;
    active: number;
    completion: Ratio;
    firstCheckTestPass: Ratio;
    finalTestPass: Ratio;
  };
  cheap?: { cheapWorker: Ratio; strictCheapOnly: Ratio; unclassified: number };
  revise?: { premiumReviseVerdicts: Ratio; tasksWithPremiumRevise: Ratio; escalated: Ratio };
  resume?: { reliability: Ratio };
  watchdog?: { trips: number; falseRate: Ratio };
  routing?: { decisions: number; withOutcome: number; good: Ratio };
  rows?: unknown[];
  [k: string]: unknown;
}
export interface EvalsReportResult {
  markdown: string;
  report: EvalsReport;
}
export interface EvalsExportResult {
  path: string;
  bytes: number;
  sha256: string;
  tasks: number;
  accepted: number;
}

// --- routing (internal/router, internal/llm/probe) ---
export interface RouteAction {
  prefer_tier?: string;
  require_tier?: string;
  require_capability?: string[];
  deny?: boolean;
  escalate_to?: string;
  handoff?: boolean;
  set_effort?: string;
  set_team_size?: number;
  penalty?: number;
  /** Models an agent turn moves to, in order, when the chosen one is unavailable (at most 4; same tier or cheaper, at most 2 hops). */
  fallback?: string[];
}
export interface RouteRule {
  id: string;
  scope: string;
  priority: number;
  when: string;
  then: RouteAction;
  enabled: boolean;
  version: number;
}
export interface RoutingOutcome {
  outcome: string;
  accepted: boolean;
  escalated: boolean;
  iterations: number;
  costUsd: number;
  costSource: string;
}
export interface DecisionRow {
  id: string;
  ts: number;
  runId?: string;
  nodeId?: string;
  attempt: number;
  runKind: string;
  level: string;
  agent: string;
  provider: string;
  model: string;
  effort: string;
  decidedBy: string;
  /** null = price unknown, never zero */
  estCostUsd: number | null;
  escalatedFrom?: string;
  /** why Drydock stopped the turn (a wall-time ceiling, P112) */
  stopReason?: string;
  /** what an LLM tiebreak did between tied candidates (P112) */
  tiebreak?: string;
  outcome?: RoutingOutcome;
}
export interface RouteAlternative {
  Provider: string;
  Model: string;
  Tier: string;
  Rejected: string;
  Detail: string;
}
/** router.RoutingDecision has no JSON tags. */
export interface RoutingDecision {
  ID: string;
  Agent?: { Adapter: string; Instance: string };
  Provider: string;
  Model: string;
  Effort: string;
  EffortApplied?: boolean;
  TeamSize?: number;
  Reason: string;
  Confidence: number;
  EstCostUSD: number | null;
  PoliciesApplied?: string[] | null;
  Alternatives?: RouteAlternative[] | null;
  DecidedBy: string;
  Facts?: {
    Tier?: string;
    DataClass?: string;
    QuotaRemainingPct?: number;
    BudgetPctUsed?: number;
    BudgetDownshift?: boolean;
    NextEscalation?: string;
    Tiebreak?: RouteTiebreak | null;
    Stopped?: string;
  };
}
/** The LLM tiebreak of a decision (router.TiebreakRecord, P112). */
export interface RouteTiebreak {
  between: string[];
  ranked: string;
  chose: string;
  by?: string;
  why?: string;
  failed?: string;
  /** a dry run: a real decision would ask */
  dryRun?: boolean;
}
export interface RouteExplain {
  text: string;
  decision: RoutingDecision;
}
export interface RoutePreview {
  blocked: boolean;
  reason?: string;
  alternatives?: RouteAlternative[] | null;
  decision?: RoutingDecision;
  explain?: string;
  budget?: unknown;
  recorded?: boolean;
  /** the wall-time ceiling a turn of this level runs under, when one is set (P112) */
  ceilingSeconds?: number;
}
/** route.settings.get (P112): the LLM tiebreak and the wall-time ceilings of a turn. */
export interface RouteSettings {
  tiebreak: {
    enabled: boolean;
    budgetUsd: number;
    timeoutMs: number;
    margin?: number;
    /** provider/model a tiebreak calls now; absent when no cheap model is selected (modelNote says why) */
    model?: string;
    modelNote?: string;
    spentTodayUsd: number;
    callsToday: number;
  };
  /** seconds; a level or agent left out has no ceiling */
  ceilings: { levels: Record<string, number>; agents: Record<string, number> };
  /** the workspace's level: the level of every project that sets none of its own (P118) */
  level: string;
  agents: { id: string; name: string; project: string }[];
}
/** route.level.get (P118): the workspace's routing level, a project's own ("" when it follows the workspace) and the result. */
export interface RouteLevel {
  projectId?: string;
  workspace: string;
  project: string;
  /** Asked for a task (P127): its id, its own level ("" or absent when it follows), its template's title and declared level. */
  taskId?: string;
  task?: string;
  template?: string;
  templateLevel?: string;
  effective: string;
  source: 'task' | 'template' | 'project' | 'workspace' | string;
  levels: string[];
}
export interface RouteSettingsPatch {
  tiebreak?: { enabled?: boolean; budgetUsd?: number; timeoutMs?: number };
  ceilings?: { levels?: Record<string, number>; agents?: Record<string, number> };
}
/** A model a provider refused for want of data retention (P112). */
export interface RetentionBlock {
  providerId: string;
  model: string;
  source: string;
  message: string;
  seenAt: number;
}
export interface CheapSelection {
  providerId: string;
  default?: string;
  fallback?: string;
  probed?: { Model?: string; model?: string; [k: string]: unknown }[] | null;
  skipped?: unknown[] | null;
  at?: string;
  message: string;
}

// --- checks, project development settings, telemetry ---
export interface CheckSettings {
  secrets: boolean;
  deps: boolean;
  depsOffline: boolean;
  /** the security scan's code and dependency engines on every task's change (decision P131) */
  scan: boolean;
}
/** The skill delivery a project has when it never chose one (the core's skills.DefaultDelivery). */
export const DEFAULT_SKILL_DELIVERY: 'all' | 'relevant' = 'relevant';
/** The context package ceiling's default and bounds in tokens (the core's ctxeng.DefaultCeiling, MinCeiling, MaxCeiling). */
export const CONTEXT_CEILING = { default: 20000, min: 4000, max: 200000 } as const;

export interface DevSettings {
  maxParallel: number;
  autoSchedule: boolean;
  respond: boolean;
  /** A task waits while an earlier task changes the same code. Absent means on. */
  collisionGuard?: boolean;
  /** Claude workers may run the project's detected check commands (exact lines) during their turn. Absent means off. */
  workerChecks?: boolean;
  /** Minutes a RUNNING task may go without progress before the team lead is asked about it (0 or absent: 15; at most 1440). */
  leadStallMinutes?: number;
  /**
   * The CI gate before a pull request is merged: `required` (the default; absent means it) or `none`, a human's statement
   * that the repository has no CI. Only the human changes it; an import sets it back to required.
   */
  ciMode?: 'required' | 'none';
  /** Who declared that the repository has no CI, and when (Unix ms). Set by the core when it saves `none`, never by the UI. */
  ciNoneBy?: string;
  ciNoneAt?: number;
  /**
   * How a Claude agent gets the project's skills: `all` puts every skill that reaches it into every turn; `relevant` puts in
   * the repository's own skills and the imported ones whose paths match the task's files, and lists the other imported ones
   * for the agent to open when the work needs them.
   * Absent means DEFAULT_SKILL_DELIVERY.
   */
  skillDelivery?: 'all' | 'relevant';
  /**
   * The ceiling, in estimated tokens, on what each agent turn starts with (instruction files plus the turn's prompt). Over it,
   * file excerpts, the repository map, memory, design notes, the spec, the handoff and check output are cut in that order; the
   * task and the instructions never are. 0 or absent: CONTEXT_CEILING.default; otherwise CONTEXT_CEILING.min to max.
   */
  contextCeiling?: number;
  e2e: { enabled: boolean; command?: string[]; timeoutSec?: number };
  preview: { enabled: boolean; command?: string[]; url?: string; timeoutSec?: number };
}
export interface TelemetryStatus {
  enabled: boolean;
  enabledBy?: string;
  enabledAt?: number;
  spans: number;
  metrics: number;
  pending: number;
  droppedAttributeKeys: Record<string, number> | null;
  destination: string;
  networkExporter: boolean;
  /** The OTLP exporter (decision P113); absent when it did not start. */
  otlp?: TelemetryOtlpStatus;
  /** Usage counters (decision P121): how often each feature was used. Off by default. */
  usageCounters?: UsageCounters;
}
/** A switch a person turned on or off, and who did it when. */
export interface Consent {
  enabled: boolean;
  changedBy?: string;
  changedAt?: number;
}
export interface UsageCounters extends Consent {
  /** Uses counted and not yet written to the local telemetry. */
  waiting: number;
}
/** "Also send what was recorded before" (decision P121): rows since sinceMs that no collector had, the newest maxRows. */
export interface TelemetryBackfill {
  sinceMs: number;
  maxRows?: number;
}
export interface TelemetryBackfillResult {
  at: number;
  sinceMs: number;
  maxRows: number;
  queued: number;
  /** Rows in the window the cap left out. */
  left: number;
  by?: string;
}
/** Where telemetry is sent over OTLP/HTTP. Header values are in the credential store and never come back. */
export interface TelemetryOtlpStatus {
  enabled: boolean;
  endpoint: string;
  encoding: 'protobuf' | 'json';
  headers: string[] | null;
  changedBy?: string;
  /** On, with an endpoint, and telemetry records: spans and metrics are being sent. */
  active: boolean;
  queued: number;
  queueSize: number;
  sentSpans: number;
  sentMetrics: number;
  droppedOverflow: number;
  droppedFailed: number;
  rejected: number;
  lastSuccessAt?: number;
  lastError?: string;
  lastErrorAt?: number;
  canKeepHeaders: boolean;
  /** What the person chose: gzip, none, or '' for automatic (gzip for https). Decision P121. */
  compression?: TelemetryCompression;
  /** Whether bodies are compressed now. */
  gzip?: boolean;
  lastBackfill?: TelemetryBackfillResult;
}
export type TelemetryCompression = '' | 'gzip' | 'none';
export interface TelemetryOtlpChange {
  enabled?: boolean;
  endpoint?: string;
  encoding?: 'protobuf' | 'json';
  setHeaders?: Record<string, string>;
  removeHeaders?: string[];
  compression?: TelemetryCompression;
  /** With a change that turns sending on: also send what was recorded before. */
  backfill?: TelemetryBackfill;
}
/** The diagnostics categories (docs/packaging.md section 9, decision P121). */
export interface DiagnosticsStatus {
  crashReports: { mode: 'local' | 'off'; changedBy?: string; changedAt?: number; kept: number };
  /** Absent when telemetry did not start. */
  usageCounters?: UsageCounters;
  telemetry: boolean;
  otlp: boolean;
}
export interface DiagnosticsChange {
  crashReports?: 'local' | 'off';
  usageCounters?: boolean;
}
export interface TelemetryOtlpTest {
  ok: boolean;
  error?: string;
  status: TelemetryOtlpStatus;
}
export interface TelemetryDocument {
  schema: string;
  exportedAt: number;
  resource: Record<string, unknown>;
  spans: unknown[] | null;
  metrics: unknown[] | null;
  allowlist?: string[] | null;
  note?: string;
}
export interface TelemetryExport {
  path: string;
  spans: number;
  metrics: number;
  document: TelemetryDocument;
}

// --- expert packs (internal/packs) ---
export interface Pack {
  id: string;
  title: string;
  kind: string;
  visibility: string;
  version: string;
  status: string;
  lastRefreshAt?: string;
  lastSuccessAt?: string;
  lastError?: string;
  pages: number;
  chunks: number;
  quarantined: number;
  ageDays: number;
  freshness?: string;
  offline: boolean;
  manifestDrift?: boolean;
  nextRefreshAt?: string;
  attribution?: string;
  manifest?: { license?: { content?: string; attribution?: string }; sources?: { type: string; url?: string; path?: string; ref?: string }[] } | null;
}
/** One of a pack's own files (a pack made from files keeps them in packs/<id>/files). */
export interface PackFile {
  name: string;
  size: number;
  modified: string;
}
/** A file a person gives a pack. */
export interface PackUpload {
  name: string;
  data: Uint8Array;
}
export interface PackCitation {
  pack: string;
  version: string;
  url: string;
  headingPath: string;
  /** The PDF page the passage is on; absent for sources without pages. */
  page?: number;
  chunkId?: number;
  fetchedAt?: string;
  contentHash: string;
  origin: string;
}
export interface PackHit {
  citation: PackCitation;
  text: string;
  snippet?: string;
  score: number;
  allTerms: boolean;
  origin: string;
}
export interface PackSearchResult {
  found: boolean;
  hits: PackHit[] | null;
  searched: string[] | null;
  unavailable?: Record<string, string>;
  warn?: string[];
  attributions?: string[];
  suspiciousDropped?: number;
  confidence: string;
  sourceAgeDays: number;
}
export interface PackConsult {
  found: boolean;
  answer: string;
  citations: PackCitation[] | null;
  /** used: the model's answer drew on this source (none is marked when no model wrote the answer). */
  sources: { n: number; citation: PackCitation; excerpt: string; used?: boolean }[] | null;
  confidence: string;
  sourceAgeDays: number;
  route: string;
  model?: string;
  usage: { inputTokens: number; outputTokens: number; cachedTokens?: number; costUsd: number };
  degraded?: string;
  warn?: string[];
  unavailable?: Record<string, string>;
}
export interface PackRefreshResult {
  packId: string;
  version: string;
  newVersion: boolean;
  pages: number;
  added: number;
  changed: number;
  removed: number;
  unchanged: number;
  skipped: number;
  failed: number;
  reasons?: Record<string, number>;
  quarantined: number;
  redactions: number;
  /** The commit each git_docs source was read at ("sources[0]"). */
  sources?: Record<string, string>;
}
export interface QuarantinedChunk {
  chunkId: number;
  url?: string;
  version?: number;
  headingPath: string;
  body: string;
  risk: number;
  flags: string[] | null;
}
export interface PackAttachment {
  projectId: string;
  packId: string;
  agentId?: string;
  role?: string;
  createdAt: string;
  createdBy?: string;
}

// --- requirements and milestones (internal/requirements) ---
export interface Requirement {
  id: string;
  projectId: string;
  title: string;
  source: string;
  sourceRef?: string;
  version: number;
  contentHash: string;
  archived: boolean;
  createdBy?: string;
  created: string;
  updated: string;
  chunks: number;
  quarantined: number;
  links: number;
  staleLinks: number;
}
export interface RequirementVersion {
  version: number;
  contentHash: string;
  chunks: number;
  redactions: number;
  createdBy?: string;
  created: string;
}
export interface RequirementHit {
  citation: string;
  docId: string;
  title: string;
  version: number;
  headingPath: string;
  chunkId: number;
  text: string;
  score: number;
  allTerms: boolean;
}
export interface RequirementLink {
  id: number;
  requirementId: string;
  version: number;
  taskId: string;
  criterion: string;
  createdBy?: string;
  created: string;
  stale: boolean;
}
export interface TraceEvidence {
  id: number;
  kind: string;
  passed?: boolean | null;
  summary: string;
  ref?: string;
  attempt: number;
  created: string;
}
export interface TraceTask {
  taskId: string;
  title: string;
  status: string;
  linkedVersion: number;
  stale: boolean;
  criteria: string[] | null;
  evidence: TraceEvidence[] | null;
  testsPassing: boolean;
  hasTests: boolean;
}
export interface RequirementTrace {
  requirement: Requirement;
  tasks: TraceTask[] | null;
  criteria: number;
  tasksCompleted: number;
  tasksStale: number;
  verified: boolean;
}
export interface Milestone {
  id: string;
  projectId: string;
  title: string;
  description?: string;
  dueAt?: string;
  status: string;
  created: string;
  updated: string;
  closed?: string;
  taskIds: string[] | null;
  progress: { total: number; completed: number; inProgress: number; blocked: number; open: number; cancelled: number; failed: number; percent: number };
  overdue: boolean;
}

// --- skills (internal/skills) ---
export interface Skill {
  id: string;
  projectId: string;
  name: string;
  description: string;
  license?: string;
  globs: string[] | null;
  roles: string[] | null;
  origin: string;
  registryId?: string;
  sourcePath?: string;
  pinnedCommit?: string;
  contentHash: string;
  hasScripts?: boolean;
  allowedToolsIgnored?: boolean;
  status: 'offered' | 'accepted' | 'rejected' | 'blocked' | string;
  /** Who receives it once accepted: 'team' (every agent its roles take in, plus `agents`) or 'agents' (only `agents`). */
  audience: 'team' | 'agents' | string;
  /** The agents it is given to. */
  agents: string[] | null;
  risk?: number;
  flags?: string[] | null;
  blockReason?: string;
  decidedBy?: string;
  decidedAt?: string;
  created: string;
  updated: string;
  body?: string;
}
export interface SkillScan {
  found: number;
  accepted: number;
  offered: number;
  blocked: number;
  removed: number;
  errors: string[] | null;
}
export interface SkillRegistry {
  id: string;
  repo: string;
  root?: string;
  license?: string;
  licenseScope?: string;
  trust: string;
  importMode?: string;
  notes?: string;
  also?: string;
  enabled: boolean;
  curated: boolean;
  addedBy?: string;
}
export interface SkillDelivery {
  ts: number;
  taskId: string;
  agentId: string;
  runId?: string;
  skill: string;
  contentHash?: string;
  method: string;
  fidelity: string;
  path?: string;
}
export interface SkillCoverage {
  files: number;
  tool: number;
}
/** What skill.importAll did with every valid skill in a registry: each name lands in exactly one list. */
export interface SkillImportAll {
  commit: string;
  imported: string[];
  updated: string[];
  unchanged: string[];
  blocked: { name: string; reason: string }[];
  skipped: { name: string; reason: string }[];
}
/** A skill in the workspace skill library, which the core fills from every enabled registry by itself (decision P48). */
export interface LibrarySkill {
  id: string;
  registryId: string;
  repo: string;
  name: string;
  description: string;
  license: string;
  globs: string[] | null;
  roles: string[] | null;
  sourcePath: string;
  pinnedCommit: string;
  contentHash: string;
  hasScripts: boolean;
  /** A `blocked` skill stays in the library with its reason but cannot be added to a project. */
  status: 'available' | 'blocked' | string;
  blockReason: string;
  risk: number;
  flags: string[] | null;
  /** The skill left the registry upstream; the library keeps its last text. */
  gone: boolean;
  updated: string;
}
/** A library skill the person picked to add to a project (Library > Skills hands them to the project's Skills page). */
export interface SkillPick {
  registryId: string;
  name: string;
}
/** One recommendation (decision P52): give, add or stop using a skill, give an MCP server, or assign an integration. */
export interface RecommendItem {
  id: string;
  kind: 'skill' | 'mcp' | 'integration' | string;
  action: 'add' | 'give' | 'remove' | string;
  /** A skill's name, a library MCP server's id, an integration's kind. */
  name: string;
  /** What a person reads: the skill's name, the server's display name, the vendor's name. */
  label: string;
  registryId?: string;
  /** Agent ids; empty is the whole team (a skill) or the project (an integration). */
  agents: string[];
  reason: string;
  status: 'open' | 'applied' | 'dismissed' | string;
  note?: string;
}
/** One recommendation turn: who answered, why it ran, its items and what the core dropped from the answer. */
export interface Recommendation {
  id: string;
  projectId: string;
  agentId: string;
  trigger: 'manual' | 'skills_added' | 'evolution' | string;
  status: 'running' | 'done' | 'failed' | string;
  candidates: SkillPick[];
  summary: string;
  items: RecommendItem[];
  dropped: string[];
  error?: string;
  costUsd: number;
  createdAt: number;
  finishedAt?: number;
}
/** A project's recent recommendation turns, newest first, and whether it asks again by itself as it evolves. */
export interface Recommendations {
  auto: boolean;
  runs: Recommendation[];
}
/** What skill.libraryAddAll did with every library skill of a registry: each name lands in exactly one list. */
export interface SkillLibraryAddAll {
  added: string[];
  unchanged: string[];
  blocked: { name: string; reason: string }[];
  skipped: { name: string; reason: string }[];
}
/** A skill a library sync brought in or changed, or a project skill whose library text is newer. */
export interface SkillRegistryChange {
  name: string;
  description: string;
}
/** A registry's place in the library: what the last sync brought, and per project the skills whose copy is behind the library. */
export interface SkillRegistryStatus {
  registryId: string;
  repo: string;
  enabled: boolean;
  lastCommit: string;
  checkedAt?: string;
  lastError?: string;
  /** Skills in the library from this registry, and how many of them are blocked. */
  skills: number;
  blocked: number;
  added: SkillRegistryChange[] | null;
  changed: SkillRegistryChange[] | null;
  projects: Record<string, { updates: SkillRegistryChange[] | null }> | null;
}
/** How one project skill is used by its runs (skill.usage), with the core's recommendation (decision P49). */
export interface SkillUsage {
  name: string;
  registryId: string;
  origin: 'repo' | 'registry' | string;
  status: string;
  /** Times the full text went into a run. */
  loaded: number;
  /** Times it was only listed for on-demand reading. */
  listed: number;
  /** Times an agent read it on demand or fetched it through the skills tool. */
  opened: number;
  tasks: number;
  tokensLoaded: number;
  tokensListed: number;
  lastUsed?: string;
  recommendation: '' | 'remove' | 'narrow' | 'keep' | string;
  reason: string;
}

// --- memory (internal/memory) ---
export interface MemoryEntry {
  id: string;
  projectId: string;
  kind: string;
  title: string;
  body: string;
  status: 'proposed' | 'accepted' | 'rejected' | string;
  source?: string;
  created: number;
  updated: number;
}

// --- templates, PlanTeam, scheduler, room agents (internal/pipeline, internal/agents/responder) ---
export interface WorkflowTemplate {
  name: string;
  title: string;
  description: string;
  workflow: string;
  specGate: boolean;
  testFirst: boolean;
  steps: string[] | null;
  roles: { role: string; required: boolean; purpose: string }[] | null;
}
export interface StartTemplateReq {
  taskId: string;
  template: string;
  agents?: Record<string, string>;
  scans?: string[];
}
export interface StartTemplateResult {
  runId: string;
  template: string;
  workflow: string;
  roles: Record<string, string> | null;
  notes?: string[] | null;
}
export interface Criterion {
  id: string;
  given: string;
  when: string;
  then: string;
}
/** The payload of the pending request at the spec gate (internal/pipeline/tdnodes.go). Untrusted: the spec is agent output. */
export interface SpecReviewPayload {
  phase: 'spec';
  taskId: string;
  title: string;
  spec: string;
  criteria: Criterion[] | null;
  sha?: string;
  ref?: string;
  note?: string;
}
export interface ProposedTask {
  key: string;
  title: string;
  description: string;
  role: string;
  dependsOn: string[] | null;
  acceptance: string[] | null;
  template?: string;
  priority?: number;
}
export interface Plan {
  id: string;
  projectId: string;
  runId: string;
  goal: string;
  status: string;
  plannerAgent: string;
  proposal?: { tasks: ProposedTask[] | null; notes?: string } | null;
  createdTasks?: Record<string, string> | null;
  error?: string;
  createdAt: number;
  decidedAt?: number;
  decidedBy?: string;
}
export interface SchedulerStatus {
  settings: DevSettings;
  activeRuns: number;
  agentSlotsInUse: number;
  peakAgentSlots: number;
}
export interface TickResult {
  promoted: string[] | null;
  started: string[] | null;
  skipped: { taskId: string; reason: string }[] | null;
  active: number;
  capacity: number;
  max: number;
}
export interface RoomTurn {
  turnId: string;
  roomId: string;
  triggerId: string;
  agentId: string;
  round: number;
  reason: string;
  mode: string;
  status: string;
  messageId?: string;
  contextMessages: number;
  contextTokens: number;
  inputTokens: number;
  outputTokens: number;
  costUsd: number;
  error?: string;
  startedAt: number;
  endedAt: number;
  /** The model the reply ran on and its routing decision (after any fallback hop); absent for a turn from before replies were routed. */
  model?: string;
  decisionId?: string;
}
export interface PreviewInfo {
  taskId: string;
  url: string;
  pid: number;
}

// --- integrations (internal/integrations) ---
export interface OpInfo {
  op: string;
  label: string;
  params: string[] | null;
  neverAuto: boolean;
  description: string;
}
export interface SyncStatus {
  state: string;
  lastSuccessAt: number;
  lastAttemptAt: number;
  lastError?: string;
  lastErrorCode?: string;
  itemsSeen: number;
  itemsLastSync: number;
  consecutiveFailures: number;
  nextAttemptAt?: number;
  watermark?: number;
  resuming: boolean;
}
/** What the vendor's API says a connection reaches (read on Test; never typed by a person). */
export interface IntegrationIdentity {
  account?: string;
  workspace?: string;
  workspaceUrl?: string;
  scopes?: string[] | null;
  method?: string;
}
/** A connection assigned to one project, with that project's scope (decision P42). */
export interface IntegrationAssignment {
  id: string;
  integrationId: string;
  projectId: string;
  config: Record<string, string> | null;
  enabled: boolean;
  createdBy: string;
  created: number;
  updated: number;
  version: number;
  sync: SyncStatus;
}
export interface IntegrationView {
  id: string;
  kind: string;
  /** The vendor's name: fixed, not editable. */
  name: string;
  /** Only on a connection made before P42. */
  projectId: string;
  status: string;
  statusDetail: string;
  authKind: string;
  baseUrl?: string;
  config: Record<string, string> | null;
  pollIntervalS: number;
  enabled: boolean;
  created: number;
  updated: number;
  version: number;
  hasCredential: boolean;
  sync: SyncStatus;
  tools: { syncTools: string[] | null; ops: OpInfo[] | null };
  identity?: IntegrationIdentity;
  checkedAt?: number;
  assignments?: IntegrationAssignment[] | null;
  legacyItems?: number;
  /** The last check against the person's account (integration.verify, decision P59); absent: never, or not since a reconnect. */
  verified?: IntegrationVerifyRecord;
}
/** What is kept of the last check against the account: no content, no secret. */
export interface IntegrationVerifyRecord {
  at: number;
  ok: boolean;
  summary: string;
  calls: number;
  failedCalls: number;
  items: number;
  mismatches: number;
}
/** One HTTP call the check made, with the status it answered (0: no answer arrived). */
export interface IntegrationVerifyCall {
  endpoint: string;
  status: number;
  ok: boolean;
}
/** The outcome of Check against your account: the steps, the calls, what was read (counts) and what did not match. */
export interface IntegrationVerifyReport {
  integrationId: string;
  kind: string;
  ok: boolean;
  at: number;
  summary: string;
  steps: { id: string; name: string; ok: boolean; skipped?: boolean; detail: string }[] | null;
  calls: IntegrationVerifyCall[] | null;
  read: { accountNamed: boolean; workspaceNamed: boolean; scopes: number; choices: number; assignments: number; items: number; morePages: boolean; detailRead: boolean };
  mismatches: string[] | null;
  notes: string[] | null;
}
export interface ConnectIntegrationReq {
  /** Set to reconnect (a new credential for the same connection). */
  id?: string;
  kind: string;
  /** A shortcut: connect and assign in one call. Connecting needs no project. */
  projectId?: string;
  /** Sent once and never kept: the page reads it from an uncontrolled password input. */
  token?: string;
  /** Aikido's REST API client credentials and region. */
  clientId?: string;
  clientSecret?: string;
  region?: string;
  /** A finished provider sign-in (integration.oauthStart). */
  oauthSession?: string;
  baseUrl?: string;
  config?: Record<string, string>;
  pollIntervalS?: number;
  skipTest?: boolean;
  /** A sign-in made before P42 through the vendor's MCP server: "oauth/mcplib/<library id>". */
  oauthRef?: string;
}
export interface IntegrationChoice {
  value: string;
  label: string;
}
export interface IntegrationAuthField {
  key: string;
  label: string;
  secret?: boolean;
  hint?: string;
  options?: IntegrationChoice[] | null;
  advanced?: boolean;
}
/** The OAuth app a sign-in uses, or where to register one (integrations.OAuthAppInfo). The client id is public, not a secret. */
export interface IntegrationOAuthApp {
  kind: string;
  clientId?: string;
  baseUrl?: string;
  registerUrl: string;
  /** setting: pasted on the Integrations page; build: the release or the environment; absent: none, the sign-in is off. */
  source?: string;
  /** The vendor can be self-hosted (Sentry): a base URL goes with the client id. */
  baseUrlSetting?: boolean;
  /** The loopback redirect the registered app must allow (the code flow). */
  redirectUri?: string;
  scopes?: string[] | null;
}
export interface IntegrationAuthMethod {
  id: string;
  label: string;
  fields?: IntegrationAuthField[] | null;
  available: boolean;
  note?: string;
  flow?: string;
  /** oauth: the app this sign-in uses, or where to register one. */
  oauthApp?: IntegrationOAuthApp | null;
}
export interface IntegrationSetting {
  key: string;
  label: string;
  hint?: string;
  required?: boolean;
  choices?: boolean;
}
/** One integration as the core describes it (integration.kinds): fixed name, auth, settings, API and docs. */
export interface IntegrationKindInfo {
  kind: string;
  label: string;
  blurb: string;
  items: string;
  api: string;
  apiVersion: string;
  sdk: string;
  rateLimit: string;
  docs: { title: string; url: string }[] | null;
  auth: IntegrationAuthMethod[] | null;
  settings: IntegrationSetting[] | null;
  ops: OpInfo[] | null;
  /** One sentence on why there is no Sign in button (Notion, Aikido); absent when the kind has one. */
  noSignIn?: string;
}
export interface IntegrationAssignReq {
  integrationId: string;
  projectId: string;
  config?: Record<string, string>;
  enabled?: boolean;
  version?: number;
}
export interface IntegrationOAuthSession {
  id: string;
  kind: string;
  method: string;
  state: string;
  url: string;
  userCode?: string;
  error?: string;
  expiresAt: number;
}
export interface IntegrationTestStep {
  id: string;
  name: string;
  ok: boolean;
  detail: string;
}
export interface IntegrationTestReport {
  integrationId: string;
  ok: boolean;
  steps: IntegrationTestStep[] | null;
  identity?: IntegrationIdentity;
  summary: string;
  at: number;
}
export interface ExternalItem {
  id: string;
  integrationId: string;
  assignmentId?: string;
  source: string;
  projectId: string;
  externalId: string;
  kind: string;
  issueRef: string;
  url: string;
  title: string;
  excerpt: string;
  state: string;
  labels: string[] | null;
  assignee: string;
  priority: string;
  meta: Record<string, unknown> | null;
  externalUpdatedAt: number;
  fetchedAt: number;
  risk: number;
  riskFlags: string[] | null;
  redactions: number;
  truncated: boolean;
  untrusted: boolean;
  taskId?: string;
  importedBy?: string;
  importedAt?: number;
  gone: boolean;
  warnings?: string[] | null;
  /** A Notion data source row's typed properties (decision P114), by name. */
  properties?: Record<string, { type: string; text: string }> | null;
  /** The vendor's state of a linked item maps to a task status the task is not in: offered to the person, never applied by itself. */
  statusSuggestion?: { upstream: string; to: string; taskStatus: string } | null;
}
export interface ExternalItemDetail extends ExternalItem {
  body: string;
  /** The same text as it appears inside a task description (a quoted, labelled block). */
  quoted: string;
}
export interface IntegrationRule {
  id: string;
  integrationId: string;
  projectId: string;
  name: string;
  when: string;
  action: string;
  params: Record<string, unknown> | null;
  maxPerDay: number;
  dryRun: boolean;
  enabled: boolean;
  createdBy: string;
  updatedBy: string;
  created: number;
  updated: number;
  version: number;
  matches?: { [k: string]: unknown }[] | null;
  createdLast24h?: number;
}
export interface RuleReq {
  id?: string;
  version?: number;
  integrationId: string;
  projectId: string;
  name: string;
  when: string;
  action: string;
  params?: Record<string, unknown>;
  maxPerDay?: number;
  dryRun?: boolean;
  enabled?: boolean;
  delete?: boolean;
}
export interface Outbox {
  id: string;
  integrationId: string;
  projectId: string;
  taskId?: string;
  op: string;
  target: string;
  tool: string;
  payload: Record<string, unknown> | null;
  payloadHash: string;
  /** The exact text that would be sent, for the human to read before approving. */
  preview: string;
  approvalId: string;
  status: string;
  idempotencyKey: string;
  createdBy: string;
  created: number;
  decidedBy?: string;
  sentAt?: number;
  receipt?: string;
  error?: string;
  version: number;
  /** The auto-approve class it went out under (decision P114); absent: a person approved it, or it waits for one. */
  autoClass?: string;
  /** The reference of a comment, so a retry looks for it on the vendor first. */
  marker?: string;
}
// --- integrations, decision P114: webhooks, mappings, auto-approve classes, the weekly sweep, attachments ---
export interface WebhookEndpoint {
  kind: string;
  label: string;
  path: string;
  /** http://127.0.0.1:<port><path>: what the person's own tunnel forwards to. */
  url?: string;
  integrationId?: string;
  hasSecret: boolean;
  /** What the secret is called at the vendor (never its value). */
  secret: string;
  header: string;
  where: string;
  /** Commands the person can run here to bring deliveries without a tunnel (GitHub's forwarder, decision P122); never run by Drydock. */
  commands?: string[];
}
export interface WebhookStatus {
  enabled: boolean;
  port: number;
  listening: boolean;
  error?: string;
  address?: string;
  endpoints: WebhookEndpoint[] | null;
  queued: number;
  recent: { kind: string; receivedAt: number; outcome: string; externalId?: string }[] | null;
  /** The token Notion sent when a subscription was created: the person pastes it into Notion and chooses Use this token. */
  notionVerification?: { token: string; receivedAt: number } | null;
  exposeNote: string;
}
export interface IntegrationMapping {
  assignmentId?: string;
  scope?: string;
  /** The vendor's state (status) or the Drydock field (property: state, priority, assignee, labels). */
  remote: string;
  remoteName?: string;
  /** The task status (status) or the Notion property name (property). */
  local: string;
  direction?: string;
  createdBy?: string;
  created?: number;
}
export interface MappingField {
  name: string;
  type?: string;
  options?: string[] | null;
  /** The Notion property a status option belongs to. */
  property?: string;
}
export interface AutoApproveClass {
  integrationId: string;
  projectId: string;
  class: string;
  label: string;
  description: string;
  enabled: boolean;
  createdBy?: string;
  updatedBy?: string;
  updated?: number;
  /** 0: never set. */
  version: number;
}
export interface SweepSettings {
  enabled: boolean;
  everyMs: number;
}
export interface IntegrationAttachment {
  id: string;
  itemId: string;
  integrationId: string;
  externalId: string;
  name: string;
  mime: string;
  size: number;
  url?: string;
  downloadable: boolean;
  sha256?: string;
  textKept: boolean;
  redactions: number;
  risk: number;
  listedAt: number;
  downloadedAt?: number;
  downloadedBy?: string;
  /** Where on the item it was found (decision P122): issue, event, property, page body, issue body, finding. */
  source?: string;
  untrusted: boolean;
}
export interface AttachmentText {
  id: string;
  itemId: string;
  name: string;
  text: string;
  redactions: number;
  risk: number;
  untrusted: boolean;
}
export interface ProposeReq {
  integrationId: string;
  op: string;
  target: string;
  taskId?: string;
  projectId: string;
  params: Record<string, string>;
}
export interface ImportedItem {
  itemId: string;
  taskId: string;
  issueRef: string;
  existing: boolean;
  requirements?: boolean;
  warnings?: string[] | null;
}
export interface NotionPageText {
  itemId: string;
  projectId: string;
  externalId: string;
  sourceRef: string;
  title: string;
  url: string;
  text: string;
  contentHash: string;
  untrusted: boolean;
  risk: number;
  riskFlags: string[] | null;
  truncated: boolean;
}
export interface IntegrationSyncResult {
  integrationId: string;
  pages: number;
  seen: number;
  new: number;
  changed: number;
  unchanged: number;
  gone: number;
  complete: boolean;
  skipped?: string;
  ruleResults?: { ruleId: string; itemId: string; outcome: string; taskId?: string }[] | null;
}

// --- the per-task sandbox (internal/core/ext_sandbox.go, docs/sandbox.md) ---
export interface SandboxSwitch {
  enabled: boolean;
  by?: string;
  at?: number;
}
export interface SandboxEngine {
  client: string;
  version: string;
  os: string;
  arch: string;
}
export interface SandboxServiceDef {
  image: string;
  env?: Record<string, string>;
  command?: string[];
}
export interface SandboxConfig {
  image: string;
  setup?: string[];
  env?: Record<string, string>;
  services?: Record<string, SandboxServiceDef>;
  network?: 'allowlist' | 'open' | 'none';
  allowHosts?: string[];
  noDefaultHosts?: boolean;
  volumes?: string[];
  user?: string;
  cpus?: number;
  memory?: string;
  pids?: number;
}
export interface SandboxStatus {
  switch: SandboxSwitch;
  engine?: SandboxEngine;
  engineError?: string;
  found: boolean;
  branch?: string;
  config?: SandboxConfig;
  configError?: string;
  hosts?: string[];
  /** A definition to start from, when the project has none. */
  starter?: string;
  egressError?: string;
  configPath: string;
  /**
   * The digest of the definition shown (the one on the default branch); sandbox.trust confirms exactly the definition with the
   * digest it is given. Absent without a valid definition.
   */
  fingerprint?: string;
  /**
   * True only when the sandbox is on and this is the definition the person confirmed. A definition that differs does not run:
   * the project's tasks wait (in Needs you) until it is confirmed. Absent from an older core: treat as true.
   */
  trusted?: boolean;
  /**
   * What differs from the definition the person confirmed, one plain line each, or a description of the definition when none
   * was ever confirmed. Text from the repository's definition: shown as text.
   */
  changes?: string[];
}
/** A port of a task's sandbox forwarded to this computer's loopback address (decision P134). */
export interface SandboxForward {
  taskId: string;
  /** The port of the app in the sandbox. */
  port: number;
  /** The port on 127.0.0.1 that reaches it. */
  hostPort: number;
  address: string;
}
export interface SandboxForwards {
  /** False when the task has no running sandbox (it has not run a sandboxed turn yet, or it ended). */
  running: boolean;
  forwards: SandboxForward[];
  /** False in a window that cannot open Drydock's viewer (the browser harness). */
  canView: boolean;
  max: number;
  /** Why the sandbox could not be asked (no container engine answers). */
  error?: string;
}
export interface SandboxTestStep {
  name: string;
  ok: boolean;
  detail?: string;
  durationMs: number;
}
