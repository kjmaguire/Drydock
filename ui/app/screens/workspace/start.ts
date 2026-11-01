// The start screen's rules (decision P142), kept apart from the component so they run in Node: what the chips mean, the names
// and the folder a description gives, the project.setup request (the wizard's own defaults, through its buildSetup), and what
// Enter does: make the project when the place is new, then the first piece of work, a task when the crew has a worker and a
// different reviewer and the project has a repository, else a chat in the project.

import { PROJECT_BRIEF_MAX, projectHasRepo, type Agent, type PrebuiltAgent, type Project, type SetupRequest, type SetupResult, type Task } from '../../api/types.ts';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import type { Chat, ChatCreate } from '../../api/chatClient.ts';
import type { WorkItem } from '../../lib/project-work.ts';
import { PERMISSION_SEGMENTS } from '../team/agent-edit.ts';
import { adapterName } from '../team/team.ts';
import { buildSetup, cloneTarget, emptyWizard, memberFromPrebuilt, stepProblem, suggestTeam, uniqueName, type TeamMember, type WizardState } from '../project/wizard.ts';

/** Where the work happens: a new folder, a folder already on this PC, a GitHub repository to clone, or a project. */
export type Where =
  | { kind: 'new'; path: string }
  | { kind: 'folder'; path: string; init: boolean }
  | { kind: 'clone'; repo: string; cloneDir: string }
  | { kind: 'project'; projectId: string };

/** Who works on it: one builder, the suggested team, saved agents (templates), or an existing project's own crew. */
export type CrewKind = 'solo' | 'team' | 'mine' | 'project';

/** An agent written with Describe (decision P138) for a project that does not exist yet: it joins the setup's agents. */
export interface DraftedMember {
  name: string;
  role: string;
  adapter: string;
  model: string;
  effort: string;
  permissionMode: string;
  purpose: string;
  /** Library MCP server ids. */
  tools: string[];
  /** The mode the skip-approvals box was ticked for ('' when not). */
  confirmed: string;
}

export interface Crew {
  kind: CrewKind;
  /** mine: the template ids. */
  templates: string[];
  drafted: DraftedMember[];
}

/** The model, effort and permission chips: they set the builder's (and a chat's) settings. */
export interface Tuning {
  model: string;
  effort: string;
  permission: string;
}

/** The catalog's builder: Solo is this agent, and the suggested team's worker. */
export const BUILDER = 'backend-engineer';

/** tasks.MaxTitleLen, in bytes. */
export const TASK_TITLE_MAX = 300;
/** A title is the first sentence, cut at a word near this many characters. */
const TITLE_SHOWN = 90;

const STOP = new Set(['a', 'an', 'the', 'to', 'for', 'of', 'and', 'with', 'that', 'which', 'in', 'on', 'my', 'me', 'i', 'we', 'please', 'some', 'small', 'simple', 'new', 'build', 'make', 'create', 'write', 'add', 'it', 'is', 'are', 'can', 'you', 'let', 'lets', 'want', 'would', 'like']);

/** The words of a text, lower-case ASCII letters and digits ("Café CLI" -> cafe, cli). */
function words(text: string): string[] {
  return text
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
}

/** A folder name from a description: its first three words that say something, joined with "-" ("new-project" when none). */
export function slugify(text: string): string {
  const all = words(text);
  const picked = all.filter((w) => !STOP.has(w)).slice(0, 3);
  const slug = (picked.length ? picked : all.slice(0, 3)).join('-').slice(0, 40).replace(/-+$/, '');
  return slug || 'new-project';
}

/** The new folder for a description: the usual code folder (github.cloneDir) and the description's slug. */
export function newFolderPath(baseDir: string, text: string): string {
  return cloneTarget(baseDir, { name: slugify(text) });
}

/** The last part of a path ("C:\\code\\csv2json" -> "csv2json"). */
export function folderName(path: string): string {
  return path.trim().replace(/[\\/]+$/, '').split(/[\\/]/).pop() ?? '';
}

/** The project's name: the folder's or the repository's name, else the description's first words ("Csv to json"). */
export function projectName(where: Where, text: string): string {
  if (where.kind === 'new' || where.kind === 'folder') {
    const n = folderName(where.path);
    if (n) return n;
  }
  if (where.kind === 'clone') return where.repo.split('/').pop() ?? where.repo;
  const w = text.trim().split(/\s+/).slice(0, 4).join(' ');
  return w.length > 40 ? w.slice(0, 40).trimEnd() : w;
}

const bytes = (s: string) => new TextEncoder().encode(s).length;

/** The task's title: the description's first sentence on one line, cut at a word, within the core's limit. */
export function taskTitle(text: string): string {
  const line = text.trim().split(/\n/)[0] ?? '';
  let first = (line.split(/(?<=[.!?])\s/)[0] ?? '').replace(/\s+/g, ' ').trim().replace(/[.]+$/, '');
  if (Array.from(first).length > TITLE_SHOWN) {
    const cut = Array.from(first).slice(0, TITLE_SHOWN).join('');
    const sp = cut.lastIndexOf(' ');
    first = `${(sp > TITLE_SHOWN / 2 ? cut.slice(0, sp) : cut).trimEnd()}…`;
  }
  while (bytes(first) > TASK_TITLE_MAX) first = `${Array.from(first).slice(0, -2).join('')}…`;
  return first;
}

/** The crews a place offers: a new place gets a crew made for it, a project keeps its own (or one agent in a chat). */
export function crewKinds(where: Where): CrewKind[] {
  return where.kind === 'project' ? ['project', 'solo'] : ['solo', 'team', 'mine'];
}

/** The crew a place starts with, or the remembered one when the place offers it. */
export function crewFor(where: Where, remembered?: CrewKind): CrewKind {
  const kinds = crewKinds(where);
  return remembered && kinds.includes(remembered) ? remembered : kinds[0]!;
}

/** Whether the model and permission chips apply: they set the builder Drydock adds (Solo, Small team) or the chat's settings. */
export function tuningApplies(crew: Pick<Crew, 'kind'>): boolean {
  return crew.kind === 'solo' || crew.kind === 'team';
}

/** The builder's catalog entry. */
export const builderEntry = (catalog: readonly PrebuiltAgent[]): PrebuiltAgent | undefined => catalog.find((e) => e.id === BUILDER);

/** The chips' first values: the builder's model and effort, and its permission mode (a new agent edits freely). */
export function defaultTuning(catalog: readonly PrebuiltAgent[]): Tuning {
  const e = builderEntry(catalog);
  const m = e ? memberFromPrebuilt(e, []) : undefined;
  return { model: m?.model ?? '', effort: m?.effort ?? '', permission: m?.permissionMode || 'acceptEdits' };
}

/** The builder's agent CLI: the catalog's recommendation (or the first usable alternative), else the first CLI offered. */
export function builderAdapter(catalog: readonly PrebuiltAgent[], fallback = 'claude'): string {
  const e = builderEntry(catalog);
  return e ? memberFromPrebuilt(e, []).adapter : fallback;
}

/** The setup's agents for a crew, with the chips' model, effort and permission on the builder. */
export function crewMembers(crew: Crew, catalog: readonly PrebuiltAgent[], templates: readonly AgentTemplate[], tuning: Tuning, fallbackAdapter = 'claude'): TeamMember[] {
  const tune = (m: TeamMember): TeamMember => ({ ...m, model: tuning.model, effort: tuning.effort, permissionMode: tuning.permission });
  let team: TeamMember[] = [];
  if (crew.kind === 'solo' || crew.kind === 'team') {
    const e = builderEntry(catalog);
    if (crew.kind === 'team' && catalog.length) {
      team = suggestTeam(catalog).map((m) => (m.ref === BUILDER ? tune(m) : m));
    } else if (e) {
      team = [tune(memberFromPrebuilt(e, []))];
    } else {
      team = [tune({ key: 'builder', source: 'custom', name: 'Builder', role: 'backend', adapter: fallbackAdapter, model: '', effort: '', permissionMode: '', purpose: '' })];
    }
  } else if (crew.kind === 'mine') {
    for (const id of crew.templates) {
      const tpl = templates.find((x) => x.id === id);
      if (!tpl) continue;
      team.push({
        key: `t:${id}`,
        source: 'template',
        ref: id,
        name: uniqueName(tpl.name, team),
        role: tpl.role,
        adapter: tpl.adapter,
        model: tpl.model,
        effort: tpl.effort,
        permissionMode: tpl.permissionMode,
        purpose: tpl.purpose,
      });
    }
  }
  for (const d of crew.drafted) {
    team.push({
      key: `d:${d.name}`,
      source: 'custom',
      name: uniqueName(d.name.trim(), team),
      role: d.role,
      adapter: d.adapter,
      model: d.model,
      effort: d.effort,
      permissionMode: d.permissionMode,
      purpose: d.purpose,
      confirmed: d.confirmed,
    });
  }
  return team;
}

/** The wizard's state for a start: its defaults (no connectors, local merge, the agents' skills installed), this place and crew. */
export function startWizard(where: Where, text: string, team: TeamMember[]): WizardState {
  const st: WizardState = { ...emptyWizard(), name: projectName(where, text), brief: text.trim(), team, suggested: true };
  if (where.kind === 'new') Object.assign(st, { repo: 'local', localPath: where.path.trim(), initRepo: true });
  else if (where.kind === 'folder') Object.assign(st, { repo: 'local', localPath: where.path.trim(), initRepo: where.init });
  else if (where.kind === 'clone') {
    const [owner = '', name = ''] = where.repo.split('/');
    Object.assign(st, { repo: 'github', cloneDir: where.cloneDir.trim(), github: { fullName: where.repo, name, owner, description: '', private: false, fork: false, archived: false, pushedAt: '', defaultBranch: '', htmlUrl: '' } });
  }
  return st;
}

/** The project.setup request of a start (the drafted agents' tools are granted with it). */
export function startSetup(where: Where, text: string, team: TeamMember[], drafted: readonly DraftedMember[] = []): SetupRequest {
  const req = buildSetup(startWizard(where, text, team));
  req.agents = req.agents.map((a) => {
    const d = drafted.find((x) => a.name === x.name.trim() && !a.prebuilt && !a.template);
    return d && d.tools.length ? { ...a, servers: [...d.tools] } : a;
  });
  return req;
}

// ---- New project (decision P143): a project with no work started ----

/** A folder name from a project's name: its words, lower-case, joined with "-" ("Pay Portal" -> pay-portal). */
export function nameSlug(name: string): string {
  return words(name).join('-').slice(0, 40).replace(/-+$/, '') || 'new-project';
}

/** The new folder for a project's name: the usual code folder (github.cloneDir) and the name's slug. */
export function newProjectFolder(baseDir: string, name: string): string {
  return baseDir.trim() ? cloneTarget(baseDir, { name: nameSlug(name) }) : '';
}

/** The name New project offers until the person types one: the folder's or the repository's ("" for a new folder). */
export function defaultProjectName(where: Where): string {
  if (where.kind === 'new') return '';
  return projectName(where, '');
}

/** What keeps New project's Create off, in words; undefined when it can create. */
export function newProjectProblem(where: Where, name: string): string | undefined {
  if (!name.trim()) return 'Name the project.';
  if (where.kind === 'new' && !where.path.trim()) return 'Choose the new folder.';
  if (where.kind === 'folder' && !where.path.trim()) return 'Choose a folder.';
  if (where.kind === 'clone' && (!where.repo || !where.cloneDir.trim())) return 'Choose a repository to clone.';
  return undefined;
}

/**
 * New project's project.setup: the start box's defaults (P142: the wizard's defaults through buildSetup, Solo's builder on its
 * recommended CLI with its model and effort, no brief, no connectors, local merge, the agents' skills installed) with the name
 * the person gave, and nothing started.
 */
export function newProjectSetup(o: { where: Where; name: string; catalog: readonly PrebuiltAgent[]; fallbackAdapter?: string }): SetupRequest | { problem: string } {
  const problem = newProjectProblem(o.where, o.name);
  if (problem) return { problem };
  const solo: Crew = { kind: 'solo', templates: [], drafted: [] };
  const team = crewMembers(solo, o.catalog, [], defaultTuning(o.catalog), builderAdapter(o.catalog, o.fallbackAdapter));
  const st = { ...startWizard(o.where, '', team), name: o.name.trim() };
  const p = stepProblem(st, 'Team');
  if (p) return { problem: p };
  return buildSetup(st);
}

/** What keeps Enter from starting, in words; undefined when it can start. */
export function startProblem(where: Where, text: string, team: readonly TeamMember[]): string | undefined {
  if (!text.trim()) return 'Describe what you want to build.';
  if (text.trim().length > PROJECT_BRIEF_MAX) return `Keep it under ${PROJECT_BRIEF_MAX} characters.`;
  if (where.kind === 'new' && !where.path.trim()) return 'Choose the new folder.';
  if (where.kind === 'folder' && !where.path.trim()) return 'Choose a folder.';
  if (where.kind === 'clone' && (!where.repo || !where.cloneDir.trim())) return 'Choose a repository to clone.';
  if (where.kind !== 'project') {
    const p = stepProblem(startWizard(where, text, [...team]), 'Team');
    if (p) return p;
  }
  return undefined;
}

/** An agent as the crew rules read it (an archived one does not work). */
type AgentLike = Pick<Agent, 'id' | 'role'> & { archived?: boolean };

const isLead = (a: Pick<Agent, 'role'>) => /lead/i.test(a.role);
const isReviewer = (a: Pick<Agent, 'role'>) => /review/i.test(a.role);

/** A worker and a different reviewer from a crew (never the lead): the builder works, the reviewer-role agent reviews. */
export function pickPair(agents: readonly AgentLike[]): { worker: string; reviewer: string } | undefined {
  const usable = agents.filter((a) => !a.archived && !isLead(a));
  const worker = usable.find((a) => !isReviewer(a)) ?? usable[0];
  if (!worker) return undefined;
  const reviewer = usable.find((a) => a.id !== worker.id && isReviewer(a)) ?? usable.find((a) => a.id !== worker.id);
  return reviewer ? { worker: worker.id, reviewer: reviewer.id } : undefined;
}

export type FirstWork = { kind: 'task'; worker: string; reviewer: string } | { kind: 'chat' };

/**
 * The first piece of work: a task when the project has a repository and the crew a worker and a different reviewer (a task
 * cannot start otherwise), and Solo is not chosen; else a chat in the project (Solo is one agent: it works in a chat).
 */
export function firstWork(crew: CrewKind, hasRepo: boolean, agents: readonly AgentLike[]): FirstWork {
  if (crew === 'solo' || !hasRepo) return { kind: 'chat' };
  const pair = pickPair(agents);
  return pair ? { kind: 'task', ...pair } : { kind: 'chat' };
}

/** A chat's CLI and settings: the chips' when they apply (on the builder's CLI), else those of the crew's worker. */
export function chatSettings(tuning: Tuning | undefined, adapter: string, agents: readonly (AgentLike & Pick<Agent, 'adapter' | 'model' | 'effort' | 'permissionMode'>)[]): Pick<ChatCreate, 'adapter' | 'model' | 'effort' | 'permissionMode'> {
  const clean = (o: Record<string, string | undefined>) => Object.fromEntries(Object.entries(o).filter(([, v]) => !!v)) as Pick<ChatCreate, 'adapter' | 'model' | 'effort' | 'permissionMode'>;
  if (tuning) return clean({ adapter, model: tuning.model, effort: tuning.effort, permissionMode: tuning.permission });
  const pair = pickPair(agents);
  const a = agents.find((x) => x.id === pair?.worker) ?? agents.find((x) => !x.archived && !isLead(x)) ?? agents[0];
  return a ? clean({ adapter: a.adapter, model: a.model, effort: a.effort, permissionMode: a.permissionMode }) : {};
}

/** What Enter does, worked out before anything is sent. */
export interface StartPlan {
  text: string;
  crew: CrewKind;
  /** A new place: the project to make first. */
  setup?: SetupRequest;
  /** An existing project. */
  project?: Pick<Project, 'id' | 'hasRepo'>;
  /** The chips' settings when they apply (Solo, Small team), on the builder's CLI. */
  tuning?: Tuning;
  adapter: string;
}

export function planStart(o: {
  where: Where;
  text: string;
  crew: Crew;
  tuning: Tuning;
  catalog: readonly PrebuiltAgent[];
  templates: readonly AgentTemplate[];
  projects: readonly Project[];
  fallbackAdapter?: string;
}): StartPlan | { problem: string } {
  const adapter = builderAdapter(o.catalog, o.fallbackAdapter);
  const tuning = tuningApplies(o.crew) ? o.tuning : undefined;
  if (o.where.kind === 'project') {
    const id = o.where.projectId;
    const project = o.projects.find((p) => p.id === id);
    if (!project) return { problem: 'That project is gone: choose another place.' };
    if (!o.text.trim()) return { problem: 'Describe what you want to build.' };
    return { text: o.text.trim(), crew: o.crew.kind, project, tuning, adapter };
  }
  const team = crewMembers(o.crew, o.catalog, o.templates, o.tuning, adapter);
  const problem = startProblem(o.where, o.text, team);
  if (problem) return { problem };
  return { text: o.text.trim(), crew: o.crew.kind, setup: startSetup(o.where, o.text, team, o.crew.drafted), tuning, adapter };
}

/** The calls a start makes (the app's client; a test's labelled double). */
export interface StartClient {
  projectSetup(req: SetupRequest): Promise<SetupResult>;
  agentList(projectId: string): Promise<Agent[]>;
  taskCreate(req: { projectId: string; title: string; description?: string }): Promise<Task>;
  taskAssign(id: string, assignee: string, reviewer?: string): Promise<Task>;
  taskStart(id: string): Promise<string>;
  chatCreate(p: ChatCreate): Promise<Chat>;
  chatSend(id: string, text: string): Promise<unknown>;
}

export interface StartOutcome {
  project: Pick<Project, 'id' | 'name'>;
  item: WorkItem;
  warnings: string[];
}

/** A start that failed after the project was made: the next try works in that project instead of making another. */
export class StartError extends Error {
  constructor(
    message: string,
    readonly projectId: string | undefined,
  ) {
    super(message);
  }
}

/**
 * Enter: the project (when the place is new), then the first piece of work, a task (made, assigned and started) or a chat (made
 * and sent the description). `progress` names each step as it begins; `created` hears of the project as soon as it exists.
 */
export async function runStart(c: StartClient, plan: StartPlan, progress: (step: string) => void, created?: (p: Project) => void): Promise<StartOutcome> {
  let projectId = plan.project?.id;
  let project: Pick<Project, 'id' | 'name'> | undefined = plan.project ? { id: plan.project.id, name: (plan.project as Project).name ?? '' } : undefined;
  let hasRepo = projectHasRepo(plan.project);
  let agents: Agent[] = [];
  let warnings: string[] = [];
  try {
    if (plan.setup) {
      progress('Creating project…');
      const r = await c.projectSetup(plan.setup);
      projectId = r.project.id;
      project = r.project;
      hasRepo = projectHasRepo(r.project);
      agents = r.agents;
      warnings = r.warnings;
      created?.(r.project);
    } else if (projectId) {
      agents = await c.agentList(projectId);
    }
    if (!projectId || !project) throw new Error('No project to start in.');
    const work = firstWork(plan.crew, hasRepo, agents);
    if (work.kind === 'task') {
      progress('Starting task…');
      const task = await c.taskCreate({ projectId, title: taskTitle(plan.text), description: plan.text });
      await c.taskAssign(task.id, work.worker, work.reviewer);
      await c.taskStart(task.id);
      return { project, item: { kind: 'task', id: task.id }, warnings };
    }
    progress('Starting chat…');
    const chat = await c.chatCreate({ projectId, ...chatSettings(plan.tuning, plan.adapter, agents) });
    await c.chatSend(chat.id, plan.text);
    return { project, item: { kind: 'chat', id: chat.id }, warnings };
  } catch (e) {
    throw new StartError(e instanceof Error ? e.message : String(e), projectId);
  }
}

/** Just chat: a chat without a project (its private folder), sent the description when there is one. */
export async function runJustChat(c: Pick<StartClient, 'chatCreate' | 'chatSend'>, text: string, settings: Pick<ChatCreate, 'adapter' | 'model' | 'effort' | 'permissionMode'>): Promise<string> {
  const chat = await c.chatCreate({ ...settings });
  if (text.trim()) await c.chatSend(chat.id, text.trim());
  return chat.id;
}

// ---- the chips' words ----

export function whereLabel(where: Where, projects: readonly Pick<Project, 'id' | 'name'>[]): string {
  switch (where.kind) {
    case 'new':
      return `New folder: ${folderName(where.path) || '…'}`;
    case 'folder':
      return folderName(where.path) || 'A folder';
    case 'clone':
      return where.repo;
    case 'project':
      return projects.find((p) => p.id === where.projectId)?.name ?? 'A project';
  }
}

const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;

export function crewLabel(crew: Crew, o: { adapter: string; catalog: readonly PrebuiltAgent[]; projectAgents?: number }): { name: string; detail: string } {
  const extra = crew.drafted.map((d) => d.name.trim()).filter(Boolean);
  const plus = extra.length ? ` + ${extra.join(', ')}` : '';
  switch (crew.kind) {
    case 'solo':
      return { name: 'Solo', detail: `${adapterName(o.adapter)}${plus}` };
    case 'team':
      return { name: 'Small team', detail: `${plural(suggestTeam(o.catalog).length || 1, 'agent')}${plus}` };
    case 'mine':
      return { name: 'My agents', detail: `${plural(crew.templates.length, 'agent')}${plus}` };
    case 'project':
      return { name: 'Project crew', detail: o.projectAgents === undefined ? '' : plural(o.projectAgents, 'agent') };
  }
}

export function tuningLabel(t: Tuning): string {
  return `${t.model || 'default model'} · ${t.effort || 'default effort'}`;
}

export function permissionLabel(mode: string): string {
  return PERMISSION_SEGMENTS.find((x) => x.value === mode)?.label ?? (mode || 'CLI default');
}

// ---- remembered choices (per place, in this browser) ----

export interface Remembered {
  crew: CrewKind;
  templates: string[];
  model: string;
  effort: string;
  permission: string;
}

export const START_KEY = 'drydock.start';
const KEEP = 40;

/** The place a choice is remembered for: a project, a folder, a repository, or "a new folder". */
export function whereKey(w: Where): string {
  switch (w.kind) {
    case 'new':
      return 'new';
    case 'folder':
      return `folder:${w.path.trim()}`;
    case 'clone':
      return `clone:${w.repo}`;
    case 'project':
      return `project:${w.projectId}`;
  }
}

type Store = Pick<Storage, 'getItem' | 'setItem'>;

function readAll(store: Store | undefined): Record<string, Remembered> {
  try {
    const v = JSON.parse(store?.getItem(START_KEY) ?? '{}') as unknown;
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, Remembered>) : {};
  } catch {
    return {};
  }
}

const valid = (r: unknown): r is Remembered =>
  !!r && typeof r === 'object' && ['solo', 'team', 'mine', 'project'].includes((r as Remembered).crew) && Array.isArray((r as Remembered).templates) && typeof (r as Remembered).model === 'string' && typeof (r as Remembered).effort === 'string' && typeof (r as Remembered).permission === 'string';

/** The choices last made for a place; undefined when none (or the browser keeps nothing). */
export function recall(store: Store | undefined, key: string): Remembered | undefined {
  const r = readAll(store)[key];
  return valid(r) ? r : undefined;
}

/** Keeps the choices for each of the keys (the newest first, at most KEEP places). */
export function remember(store: Store | undefined, keys: readonly string[], r: Remembered): void {
  try {
    const all = readAll(store);
    for (const k of keys) delete all[k];
    const next: Record<string, Remembered> = {};
    for (const k of keys) next[k] = r;
    for (const [k, v] of Object.entries(all).slice(0, KEEP - keys.length)) next[k] = v;
    store?.setItem(START_KEY, JSON.stringify(next));
  } catch {
    /* a convenience only */
  }
}

/** This browser's storage, or nothing where it cannot be reached. */
export function browserStore(): Store | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}
