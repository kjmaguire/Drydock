// Typed client for the Slice 2-5 methods (internal/core/ext_*.go). Same rules as client.ts: one function per method, Go's nil
// slices (JSON null) become empty arrays, errors are RpcError from the transport.

import type { Transport } from './transport.ts';
import type {
  Budget,
  BudgetResumeReq,
  BudgetResumed,
  BudgetRow,
  BudgetSet,
  BudgetStatus,
  BudgetSpend,
  CheapSelection,
  CheckSettings,
  DecisionRow,
  DevSettings,
  SandboxStatus,
  SandboxSwitch,
  SandboxTestStep,
  EvalsExportResult,
  EvalsOptions,
  EvalsReportResult,
  ExternalItem,
  ExternalItemDetail,
  ImportedItem,
  IntegrationRule,
  IntegrationSyncResult,
  IntegrationTestReport,
  IntegrationView,
  ConnectIntegrationReq,
  IntegrationAssignment,
  IntegrationAssignReq,
  IntegrationChoice,
  IntegrationKindInfo,
  IntegrationOAuthSession,
  MemoryEntry,
  Milestone,
  NotionPageText,
  Outbox,
  Pack,
  PackAttachment,
  PackConsult,
  PackRefreshResult,
  PackSearchResult,
  Plan,
  PreviewInfo,
  ProposeReq,
  QuarantinedChunk,
  Requirement,
  RequirementHit,
  RequirementLink,
  RequirementTrace,
  RequirementVersion,
  RoomTurn,
  RouteExplain,
  RoutePreview,
  RouteRule,
  RuleReq,
  SchedulerStatus,
  Skill,
  SkillCandidate,
  SkillCoverage,
  SkillDelivery,
  SkillRegistry,
  SkillScan,
  StartTemplateReq,
  StartTemplateResult,
  TelemetryDocument,
  TelemetryExport,
  TelemetryStatus,
  TickResult,
  ProposedTask,
  WorkflowTemplate,
} from './extTypes.ts';
import type { Room, RoomMember } from './types.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export interface ExtClient {
  // budgets
  budgetList(projectId: string, scope?: string): Promise<BudgetRow[]>;
  budgetSet(b: BudgetSet): Promise<Budget>;
  budgetDelete(id: string): Promise<void>;
  budgetStatus(ids: { projectId?: string; taskId?: string; runId?: string; agentId?: string }): Promise<{ statuses: BudgetStatus[]; usage: Record<string, BudgetSpend>; note: string }>;
  budgetResume(req: BudgetResumeReq): Promise<BudgetResumed>;
  // evals
  evalsReport(o?: EvalsOptions): Promise<EvalsReportResult>;
  evalsExport(o?: EvalsOptions): Promise<EvalsExportResult>;
  // routing
  routeDecisions(taskId: string): Promise<DecisionRow[]>;
  routeExplain(decisionId: string): Promise<RouteExplain>;
  routeRulesList(): Promise<RouteRule[]>;
  routeRulesSet(rule: RouteRule): Promise<RouteRule>;
  routeRulesDelete(id: string): Promise<void>;
  routePreview(request: Record<string, unknown>): Promise<RoutePreview>;
  routeCheapGet(providerId: string): Promise<{ selection: CheapSelection | null; selected: boolean }>;
  routeCheapSelect(providerId: string, force?: boolean): Promise<CheapSelection>;
  routeProbeStartup(enabled: boolean): Promise<{ enabled: boolean; by: string }>;
  // checks, dev settings, telemetry
  checksSettingsGet(projectId: string): Promise<CheckSettings>;
  checksSettingsSet(projectId: string, s: CheckSettings): Promise<CheckSettings>;
  projectdevGet(projectId: string): Promise<DevSettings>;
  projectdevSet(projectId: string, s: DevSettings): Promise<DevSettings>;
  // the per-task sandbox
  sandboxStatus(projectId: string): Promise<SandboxStatus>;
  sandboxSet(projectId: string, enabled: boolean): Promise<SandboxSwitch>;
  sandboxTest(projectId: string): Promise<{ steps: SandboxTestStep[] }>;
  telemetryStatus(): Promise<TelemetryStatus>;
  telemetryEnable(enabled: boolean): Promise<TelemetryStatus>;
  telemetryPreview(limit?: number): Promise<TelemetryDocument>;
  telemetryExport(): Promise<TelemetryExport>;
  // expert packs
  packList(): Promise<Pack[]>;
  packGet(id: string): Promise<Pack>;
  packInstall(yaml: string): Promise<Pack>;
  packRemove(id: string): Promise<void>;
  packRefresh(id: string, force?: boolean): Promise<PackRefreshResult>;
  packSearch(packIds: string[], query: string, k?: number): Promise<PackSearchResult>;
  packConsult(packIds: string[], question: string): Promise<PackConsult>;
  packQuarantined(id: string): Promise<QuarantinedChunk[]>;
  packReleaseChunk(chunkId: number): Promise<void>;
  packAttach(projectId: string, packId: string, agentId?: string, role?: string): Promise<void>;
  packDetach(projectId: string, packId: string, agentId?: string, role?: string): Promise<void>;
  packAttachments(projectId: string): Promise<PackAttachment[]>;
  // requirements and milestones
  requirementList(projectId: string, includeArchived?: boolean): Promise<Requirement[]>;
  requirementAdd(projectId: string, title: string, text: string, format?: string): Promise<Requirement>;
  requirementUpdate(id: string, text: string, format?: string): Promise<{ requirement: Requirement; changed: boolean }>;
  requirementArchive(id: string, archived: boolean): Promise<void>;
  requirementVersions(id: string): Promise<RequirementVersion[]>;
  requirementText(id: string, version?: number): Promise<string>;
  requirementSearch(projectId: string, query: string, docId?: string, k?: number): Promise<RequirementHit[]>;
  requirementLink(requirementId: string, taskId: string, criteria: string[]): Promise<RequirementLink[]>;
  requirementUnlink(requirementId: string, taskId: string): Promise<void>;
  requirementLinks(filter: { requirementId?: string; taskId?: string }): Promise<RequirementLink[]>;
  requirementTrace(id: string): Promise<RequirementTrace>;
  requirementCoverage(projectId: string): Promise<RequirementTrace[]>;
  requirementQuarantined(id: string): Promise<QuarantinedChunk[]>;
  requirementReleaseChunk(chunkId: number): Promise<void>;
  milestoneList(projectId: string): Promise<Milestone[]>;
  milestoneCreate(projectId: string, title: string, description?: string, dueAt?: string, taskIds?: string[]): Promise<Milestone>;
  milestoneSetStatus(id: string, status: 'open' | 'closed', force?: boolean): Promise<Milestone>;
  milestoneSetTasks(id: string, add: string[], remove: string[]): Promise<Milestone>;
  milestoneDelete(id: string): Promise<void>;
  // skills
  skillList(projectId: string, statuses?: string[]): Promise<Skill[]>;
  skillScan(projectId: string): Promise<SkillScan>;
  skillAccept(projectId: string, name: string): Promise<Skill>;
  skillReject(projectId: string, name: string): Promise<Skill>;
  skillRelease(projectId: string, name: string): Promise<Skill>;
  skillRegistries(): Promise<SkillRegistry[]>;
  skillRegistryAdd(registry: Partial<SkillRegistry> & { id: string; repo: string }, acknowledged: boolean): Promise<SkillRegistry>;
  skillRegistryEnable(id: string, enabled: boolean): Promise<void>;
  skillRegistryRemove(id: string): Promise<void>;
  skillCandidates(registryId: string, ref?: string): Promise<{ commit: string; skills: SkillCandidate[] }>;
  skillImport(projectId: string, registryId: string, name: string, ref?: string): Promise<Skill>;
  /** Who receives a skill: the whole team (its roles, plus `agents`) or only the chosen `agents`. */
  skillAudience(projectId: string, name: string, audience: 'team' | 'agents', agents: string[]): Promise<Skill>;
  skillDeliveries(projectId: string, taskId?: string): Promise<SkillDelivery[]>;
  skillCoverage(projectId: string, taskId?: string): Promise<SkillCoverage>;
  // memory
  memoryList(projectId: string, status?: string, kind?: string): Promise<MemoryEntry[]>;
  memorySearch(projectId: string, query: string, status?: string): Promise<MemoryEntry[]>;
  memoryPropose(projectId: string, kind: string, title: string, body: string): Promise<MemoryEntry>;
  memoryAccept(id: string): Promise<MemoryEntry>;
  memoryReject(id: string): Promise<MemoryEntry>;
  memoryDelete(id: string): Promise<void>;
  // development flow
  templateList(): Promise<WorkflowTemplate[]>;
  taskStartTemplate(req: StartTemplateReq): Promise<StartTemplateResult>;
  roomSetMode(req: { roomId: string; mode: string; order?: string[]; moderator?: string; maxRounds?: number; maxParallel?: number }): Promise<Room>;
  roomInvite(roomId: string, agentId: string, role?: string, state?: string): Promise<RoomMember[]>;
  roomRemoveMember(roomId: string, agentId: string): Promise<RoomMember[]>;
  roomTurns(roomId: string, limit?: number): Promise<RoomTurn[]>;
  roomReply(messageId: string): Promise<unknown>;
  planPropose(projectId: string, goal: string, requirements?: string, plannerAgentId?: string): Promise<{ planId: string; runId: string; plannerAgentId: string }>;
  planGet(planId: string): Promise<Plan>;
  planList(projectId: string): Promise<Plan[]>;
  planAccept(planId: string, accept?: string[], edits?: Record<string, ProposedTask>): Promise<unknown>;
  planReject(planId: string): Promise<unknown>;
  schedulerStatus(projectId: string): Promise<SchedulerStatus>;
  schedulerTick(projectId: string): Promise<TickResult>;
  /** Let a task that waits for an earlier one go on anyway (task.guard.release). */
  taskGuardRelease(taskId: string): Promise<{ released: boolean }>;
  previewStart(taskId: string): Promise<unknown>;
  previewStop(taskId: string): Promise<{ stopped: boolean }>;
  previewList(): Promise<PreviewInfo[]>;
  // integrations
  integrationList(): Promise<IntegrationView[]>;
  integrationKinds(): Promise<IntegrationKindInfo[]>;
  integrationAssign(req: IntegrationAssignReq): Promise<IntegrationAssignment>;
  integrationUnassign(assignmentId: string): Promise<void>;
  integrationChoices(integrationId: string, key: string, config?: Record<string, string>): Promise<IntegrationChoice[]>;
  integrationOAuthStart(kind: string, baseUrl?: string): Promise<IntegrationOAuthSession>;
  integrationOAuthStatus(id: string): Promise<IntegrationOAuthSession>;
  integrationSyncAssignment(assignmentId: string, full?: boolean): Promise<IntegrationSyncResult>;
  integrationConnect(req: ConnectIntegrationReq): Promise<{ connection: IntegrationView; test?: IntegrationTestReport }>;
  integrationOAuthBegin(kind: string, baseUrl?: string): Promise<{ libraryId: string; oauthRef: string; status: { state: string; url?: string; error?: string; signedIn?: boolean } }>;
  integrationDisconnect(id: string): Promise<void>;
  integrationTest(id: string): Promise<IntegrationTestReport>;
  integrationSync(id: string, full?: boolean): Promise<IntegrationSyncResult>;
  integrationItems(filter: { integrationId?: string; assignmentId?: string; projectId?: string; kind?: string; state?: string; imported?: boolean; limit?: number; offset?: number }): Promise<ExternalItem[]>;
  integrationItem(id: string): Promise<ExternalItemDetail>;
  integrationRulesList(integrationId: string): Promise<IntegrationRule[]>;
  integrationRulesSet(r: RuleReq): Promise<IntegrationRule>;
  integrationImport(itemId: string, projectId?: string): Promise<ImportedItem>;
  integrationNotionPage(itemId: string): Promise<NotionPageText>;
  integrationOutboundPropose(p: ProposeReq): Promise<Outbox>;
  integrationOutboundDecide(req: { id?: string; approvalId?: string; approve: boolean }): Promise<Outbox>;
  integrationOutboundExecute(id: string): Promise<Outbox>;
  integrationOutboundList(filter: { integrationId?: string; status?: string; limit?: number }): Promise<Outbox[]>;
}

export function createExtClient(t: Transport): ExtClient {
  const call = <T>(m: string, p?: unknown) => t.call<T>(m, p);
  const voidCall = async (m: string, p?: unknown) => {
    await t.call(m, p);
  };
  return {
    budgetList: async (projectId, scope = '') => arr(await call<BudgetRow[] | null>('budget.list', { projectId, scope })),
    budgetSet: (b) => call('budget.set', b),
    budgetDelete: (id) => voidCall('budget.delete', { id }),
    budgetStatus: async (ids) => {
      const r = await call<{ statuses: BudgetStatus[] | null; usage: Record<string, BudgetSpend> | null; note: string }>('budget.status', ids);
      return { statuses: arr(r.statuses), usage: r.usage ?? {}, note: r.note };
    },
    budgetResume: (req) => call('budget.resume', req),

    evalsReport: (o = {}) => call('evals.report', o),
    evalsExport: (o = {}) => call('evals.export', o),

    routeDecisions: async (taskId) => arr(await call<DecisionRow[] | null>('route.decisions', { taskId })),
    routeExplain: (decisionId) => call('route.explain', { decisionId }),
    routeRulesList: async () => arr((await call<{ rules: RouteRule[] | null }>('route.rules.list')).rules),
    routeRulesSet: (rule) => call('route.rules.set', { rule }),
    routeRulesDelete: (id) => voidCall('route.rules.delete', { id }),
    routePreview: (request) => call('route.preview', { request }),
    routeCheapGet: (providerId) => call('route.cheap.get', { providerId }),
    routeCheapSelect: (providerId, force = false) => call('route.cheap.select', { providerId, force }),
    routeProbeStartup: (enabled) => call('route.probe.startup', { enabled }),

    checksSettingsGet: (projectId) => call('checks.settings.get', { projectId }),
    checksSettingsSet: (projectId, s) => call('checks.settings.set', { projectId, ...s }),
    projectdevGet: (projectId) => call('projectdev.get', { projectId }),
    projectdevSet: (projectId, settings) => call('projectdev.set', { projectId, settings }),
    sandboxStatus: (projectId) => call('sandbox.status', { projectId }),
    sandboxSet: (projectId, enabled) => call('sandbox.set', { projectId, enabled }),
    sandboxTest: async (projectId) => {
      const r = await call<{ steps: SandboxTestStep[] | null }>('sandbox.test', { projectId });
      return { steps: r.steps ?? [] };
    },
    telemetryStatus: () => call('telemetry.status'),
    telemetryEnable: (enabled) => call('telemetry.enable', { enabled }),
    telemetryPreview: async (limit = 0) => {
      const d = await call<TelemetryDocument>('telemetry.preview', { limit });
      return { ...d, spans: arr(d.spans), metrics: arr(d.metrics), allowlist: arr(d.allowlist) };
    },
    telemetryExport: () => call('telemetry.export'),

    packList: async () => arr(await call<Pack[] | null>('pack.list')),
    packGet: (id) => call('pack.get', { id }),
    packInstall: (yaml) => call('pack.install', { yaml }),
    packRemove: (id) => voidCall('pack.remove', { id }),
    packRefresh: (id, force = false) => call('pack.refresh', { id, force }),
    packSearch: async (packIds, query, k = 0) => {
      const r = await call<PackSearchResult>('pack.search', { packIds, query, k });
      return { ...r, hits: arr(r.hits), searched: arr(r.searched) };
    },
    packConsult: async (packIds, question) => {
      const r = await call<PackConsult>('pack.consult', { packIds, question });
      return { ...r, citations: arr(r.citations), sources: arr(r.sources) };
    },
    packQuarantined: async (id) => arr(await call<QuarantinedChunk[] | null>('pack.quarantined', { id })),
    packReleaseChunk: (chunkId) => voidCall('pack.releaseChunk', { chunkId }),
    packAttach: (projectId, packId, agentId = '', role = '') => voidCall('pack.attach', { projectId, packId, agentId, role }),
    packDetach: (projectId, packId, agentId = '', role = '') => voidCall('pack.detach', { projectId, packId, agentId, role }),
    packAttachments: async (projectId) => arr(await call<PackAttachment[] | null>('pack.attachments', { projectId })),

    requirementList: async (projectId, includeArchived = false) => arr(await call<Requirement[] | null>('requirement.list', { projectId, includeArchived })),
    requirementAdd: (projectId, title, text, format = 'markdown') => call('requirement.add', { projectId, title, text, format, source: 'upload' }),
    requirementUpdate: (id, text, format = 'markdown') => call('requirement.update', { id, text, format }),
    requirementArchive: (id, archived) => voidCall('requirement.archive', { id, archived }),
    requirementVersions: async (id) => arr(await call<RequirementVersion[] | null>('requirement.versions', { id })),
    requirementText: async (id, version = 0) => String((await call<string | null>('requirement.text', { id, version })) ?? ''),
    requirementSearch: async (projectId, query, docId = '', k = 0) => arr(await call<RequirementHit[] | null>('requirement.search', { projectId, query, docId, k })),
    requirementLink: async (requirementId, taskId, criteria) => arr(await call<RequirementLink[] | null>('requirement.link', { requirementId, taskId, criteria })),
    requirementUnlink: (requirementId, taskId) => voidCall('requirement.unlink', { requirementId, taskId }),
    requirementLinks: async ({ requirementId = '', taskId = '' }) => arr(await call<RequirementLink[] | null>('requirement.links', { requirementId, taskId })),
    requirementTrace: async (id) => {
      const r = await call<RequirementTrace>('requirement.trace', { id });
      return { ...r, tasks: arr(r.tasks).map((t) => ({ ...t, criteria: arr(t.criteria), evidence: arr(t.evidence) })) };
    },
    requirementCoverage: async (projectId) => arr(await call<RequirementTrace[] | null>('requirement.coverage', { projectId })),
    requirementQuarantined: async (id) => arr(await call<QuarantinedChunk[] | null>('requirement.quarantined', { id })),
    requirementReleaseChunk: (chunkId) => voidCall('requirement.releaseChunk', { chunkId }),
    milestoneList: async (projectId) => arr(await call<Milestone[] | null>('milestone.list', { projectId })),
    milestoneCreate: (projectId, title, description, dueAt, taskIds) => call('milestone.create', { projectId, title, description, dueAt, taskIds }),
    milestoneSetStatus: (id, status, force = false) => call('milestone.setStatus', { id, status, force }),
    milestoneSetTasks: (id, add, remove) => call('milestone.setTasks', { id, add, remove }),
    milestoneDelete: (id) => voidCall('milestone.delete', { id }),

    skillList: async (projectId, statuses = []) => arr(await call<Skill[] | null>('skill.list', { projectId, statuses })),
    skillScan: (projectId) => call('skill.scan', { projectId }),
    skillAccept: (projectId, name) => call('skill.accept', { projectId, name }),
    skillReject: (projectId, name) => call('skill.reject', { projectId, name }),
    skillRelease: (projectId, name) => call('skill.release', { projectId, name }),
    skillRegistries: async () => arr(await call<SkillRegistry[] | null>('skill.registries')),
    skillRegistryAdd: (registry, acknowledged) => call('skill.registryAdd', { registry, acknowledged }),
    skillRegistryEnable: (id, enabled) => voidCall('skill.registryEnable', { id, enabled }),
    skillRegistryRemove: (id) => voidCall('skill.registryRemove', { id }),
    skillCandidates: async (registryId, ref = '') => {
      const r = await call<{ commit: string; skills: SkillCandidate[] | null }>('skill.candidates', { registryId, ref });
      return { commit: r.commit, skills: arr(r.skills) };
    },
    skillImport: (projectId, registryId, name, ref = '') => call('skill.import', { projectId, registryId, name, ref }),
    skillAudience: (projectId, name, audience, agents) => call('skill.audience', { projectId, name, audience, agents }),
    skillDeliveries: async (projectId, taskId = '') => arr(await call<SkillDelivery[] | null>('skill.deliveries', { projectId, taskId })),
    skillCoverage: (projectId, taskId = '') => call('skill.coverage', { projectId, taskId }),

    memoryList: async (projectId, status = '', kind = '') => arr(await call<MemoryEntry[] | null>('memory.list', { projectId, status, kind })),
    memorySearch: async (projectId, query, status = '') => arr(await call<MemoryEntry[] | null>('memory.search', { projectId, query, status })),
    memoryPropose: (projectId, kind, title, body) => call('memory.propose', { projectId, kind, title, body }),
    memoryAccept: (id) => call('memory.accept', { id }),
    memoryReject: (id) => call('memory.reject', { id }),
    memoryDelete: (id) => voidCall('memory.delete', { id }),

    templateList: async () => arr(await call<WorkflowTemplate[] | null>('template.list')),
    taskStartTemplate: (req) => call('task.startTemplate', req),
    roomSetMode: (req) => call('room.setMode', req),
    roomInvite: async (roomId, agentId, role = '', state = '') => arr(await call<RoomMember[] | null>('room.invite', { roomId, agentId, role, state })),
    roomRemoveMember: async (roomId, agentId) => arr(await call<RoomMember[] | null>('room.removeMember', { roomId, agentId })),
    roomTurns: async (roomId, limit = 0) => arr(await call<RoomTurn[] | null>('room.turns', { roomId, limit })),
    roomReply: (messageId) => call('room.reply', { messageId }),
    planPropose: (projectId, goal, requirements = '', plannerAgentId = '') => call('plan.propose', { projectId, goal, requirements, plannerAgentId }),
    planGet: (planId) => call('plan.get', { planId }),
    planList: async (projectId) => arr(await call<Plan[] | null>('plan.list', { projectId })),
    planAccept: (planId, accept, edits) => call('plan.accept', { planId, accept, edits }),
    planReject: (planId) => call('plan.reject', { planId }),
    schedulerStatus: (projectId) => call('scheduler.status', { projectId }),
    schedulerTick: (projectId) => call('scheduler.tick', { projectId }),
    taskGuardRelease: (taskId) => call('task.guard.release', { id: taskId }),
    previewStart: (taskId) => call('preview.start', { id: taskId }),
    previewStop: (taskId) => call('preview.stop', { id: taskId }),
    previewList: async () => arr(await call<PreviewInfo[] | null>('preview.list')),

    integrationList: async () => arr(await call<IntegrationView[] | null>('integration.list')),
    integrationKinds: async () => arr(await call<IntegrationKindInfo[] | null>('integration.kinds')),
    integrationAssign: (req) => call('integration.assign', req),
    integrationUnassign: (id) => voidCall('integration.unassign', { id }),
    integrationChoices: async (integrationId, key, config = {}) => arr(await call<IntegrationChoice[] | null>('integration.choices', { integrationId, key, config })),
    integrationOAuthStart: (kind, baseUrl = '') => call('integration.oauthStart', { kind, baseUrl }),
    integrationOAuthStatus: (id) => call('integration.oauthStatus', { id }),
    integrationSyncAssignment: (assignmentId, full = false) => call('integration.sync', { assignmentId, full }),
    integrationConnect: (req) => call('integration.connect', req),
    integrationOAuthBegin: (kind, baseUrl = '') => call('integration.oauthBegin', { kind, baseUrl }),
    integrationDisconnect: (id) => voidCall('integration.disconnect', { id }),
    integrationTest: (id) => call('integration.test', { id }),
    integrationSync: (id, full = false) => call('integration.sync', { id, full }),
    integrationItems: async (f) => arr(await call<ExternalItem[] | null>('integration.items', f)),
    integrationItem: (id) => call('integration.item', { id }),
    integrationRulesList: async (integrationId) => arr(await call<IntegrationRule[] | null>('integration.rules.list', { integrationId })),
    integrationRulesSet: (r) => call('integration.rules.set', r),
    integrationImport: (itemId, projectId = '') => call('integration.import', { itemId, projectId }),
    integrationNotionPage: (itemId) => call('integration.notion.page', { itemId }),
    integrationOutboundPropose: (p) => call('integration.outbound.propose', p),
    integrationOutboundDecide: (req) => call('integration.outbound.decide', req),
    integrationOutboundExecute: (id) => call('integration.outbound.execute', { id }),
    integrationOutboundList: async (f) => arr(await call<Outbox[] | null>('integration.outbound.list', f)),
  };
}
