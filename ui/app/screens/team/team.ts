// Pure helpers for the team page, the agent editor and the template form (no DOM, no services).

import type { AppliedTemplate, SkillsInstalled } from '../../api/libraryClient.ts';
import { agentStatus, isStopped, presenceWords } from '../../lib/format.ts';
import type { AdapterInfo, Agent, AgentRun, Capabilities, PresenceState, RouteModel, Task, UsageTotals } from '../../api/types.ts';

export const CAP_LABEL: [keyof Capabilities, string][] = [
  ['streaming', 'Streaming'],
  ['structuredOutput', 'Structured output'],
  ['modelSelection', 'Model selection'],
  ['effortControl', 'Effort control'],
  ['sessionResume', 'Session resume'],
  ['interrupt', 'Interrupt'],
  ['mcp', 'MCP'],
  ['tokenUsage', 'Token usage'],
  ['costReporting', 'Cost reporting'],
  ['headless', 'Headless'],
  ['imageInput', 'Image input'],
  ['toolCallVisibility', 'Tool-call visibility'],
];

export const ROLE_SUGGESTIONS = ['backend', 'frontend', 'reviewer', 'tester', 'architect'];

export const PURPOSE_HELP =
  'Standing instructions for this agent, sent with every run: what it owns, what it must not touch, how it should work. It never overrides the task, the independent review or your approval.';

/** adapter.list comes back in map order; show ready adapters first, then installed ones, then by id. */
export function sortAdapters(list: readonly AdapterInfo[]): AdapterInfo[] {
  const rank = (a: AdapterInfo) => (a.detection.status === 'ready' ? 0 : a.detection.installed ? 1 : 2);
  return [...list].sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id));
}

/** The purpose as shown on a team card: at most 160 characters. */
export function purposeExcerpt(purpose: string, max = 160): string {
  return purpose.length > max ? `${purpose.slice(0, max)}...` : purpose;
}

/** The option text of an adapter in the add-agent form. */
export function adapterOption(a: AdapterInfo): string {
  return `${a.id} (${a.detection.installed ? a.detection.status : 'not installed'})`;
}

/** The routed models when the human turns routing on: haiku then opus when the adapter offers both, else two empty rows. */
export function defaultRoutes(suggestions: readonly string[]): RouteModel[] {
  return suggestions.includes('haiku') && suggestions.includes('opus')
    ? [
        { model: 'haiku', tier: 'cheap' },
        { model: 'opus', tier: 'premium' },
      ]
    : [
        { model: '', tier: 'cheap' },
        { model: '', tier: 'premium' },
      ];
}

/** routeModels for agent.update: undefined when unchanged (the core keeps them), else the rows that name a model. */
export function routesForUpdate(routes: readonly RouteModel[], original: readonly RouteModel[] | undefined): RouteModel[] | undefined {
  return JSON.stringify(routes) === JSON.stringify(original ?? []) ? undefined : routes.filter((r) => r.model.trim());
}

/** What installing an agent's skills did, in sentences (prebuilt agents and templates). */
export function skillLines(r: SkillsInstalled): string[] {
  const out: string[] = [];
  if (r.assigned.length) out.push(`Skills: ${r.assigned.join(', ')}.`);
  if (r.shared.length) out.push(`Already with the whole team: ${r.shared.join(', ')}.`);
  for (const x of r.pending) out.push(`Skill ${x.name ?? x.id} waits for your decision: ${x.reason}`);
  for (const x of r.failed) out.push(`Skill ${x.name ?? x.id} not installed: ${x.reason}`);
  return out;
}

/** What a template apply says: who joined, what was granted and what was not, and what its skills did. */
export function appliedNote(r: AppliedTemplate): string {
  const head = `${r.agent.name} joined the team${r.granted.length ? `, with ${r.granted.join(', ')}` : ''}${r.skipped.length ? `. Not granted: ${r.skipped.join('; ')}` : ''}.`;
  return [head, ...skillLines(r.skills)].join(' ');
}

/**
 * Whether the Claude preset may overwrite the model and effort fields: only while they are empty or still hold the preset
 * applied last (the human has not changed them).
 */
export function presetUntouched(model: string, effort: string, last: { model: string; effort: string } | undefined): boolean {
  return (model === '' && effort === '') || (last !== undefined && model === last.model && effort === last.effort);
}

/**
 * What an agent is doing, in one line: its activity, else its presence label, else the presence name. One word for an agent whose
 * program is not running: "Stopped" (the pages used to say "Resting", "stopped" and "Paused (stopped)" for it, the UI audit's B5).
 */
export function doing(p: PresenceState | undefined): string {
  if (isStopped(p)) return 'Stopped';
  return p!.activity || p!.label || presenceWords(p);
}

export { presenceWords };

/** "Claude Code" for the claude adapter, else the adapter id (the chips of an agent's page). */
export function adapterName(id: string): string {
  const names: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', gemini: 'Gemini CLI', cline: 'Cline', opencode: 'OpenCode', goose: 'Goose' };
  return names[id] ?? id;
}

/** Where the agent's CLI runs, in words ("in WSL", without the distro: runsWhereTitle names it), or '' when it follows the project. */
export function runsWhere(a: Pick<Agent, 'runMode' | 'wslDistro'>): string {
  if (a.runMode === 'wsl') return 'in WSL';
  if (a.runMode === 'native') return 'on Windows';
  return '';
}

/** The tooltip of where the agent runs: which WSL distro ("Runs inside the WSL distro Ubuntu"), or undefined. */
export const runsWhereTitle = (a: Pick<Agent, 'runMode' | 'wslDistro'>): string | undefined =>
  a.runMode === 'wsl' && a.wslDistro ? `Runs inside the WSL distro ${a.wslDistro}` : undefined;

const DONE = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);

/** A core timestamp in milliseconds: the core writes seconds in some fields and milliseconds in others. */
export function toMs(n: number | undefined): number {
  if (!n) return 0;
  return n < 1e11 ? n * 1000 : n;
}

/**
 * The task an agent is on right now: the one its presence names, else the one of its latest run still running, else its
 * first open task as worker. Undefined when it has none.
 */
export function currentTask(agentId: string, presence: PresenceState | undefined, runs: readonly AgentRun[], tasks: readonly Task[]): Task | undefined {
  const byId = (id: string | undefined) => (id ? tasks.find((x) => x.id === id) : undefined);
  const fromPresence = byId(presence?.taskId);
  if (fromPresence) return fromPresence;
  const running = runs.find((r) => r.status === 'running' && r.taskId);
  const fromRun = byId(running?.taskId);
  if (fromRun) return fromRun;
  return tasks.find((x) => x.assignee === agentId && !DONE.has(x.status) && x.status !== 'BACKLOG');
}

/** The steps a task goes through, as the progress strip shows them (the task's status says which one it is on). */
export const TASK_STEPS: { key: string; label: string; statuses: string[] }[] = [
  { key: 'work', label: 'work', statuses: ['CLAIMED', 'IN_PROGRESS', 'BLOCKED'] },
  { key: 'review', label: 'review', statuses: ['REVIEW'] },
  { key: 'you', label: 'you', statuses: ['VERIFICATION', 'WAITING_HUMAN'] },
  { key: 'done', label: 'done', statuses: ['COMPLETED'] },
];

/** How far a task status is along the steps; -1 before the first (ready, backlog) and for a task that failed or was cancelled. */
export function stepIndex(status: string): number {
  return TASK_STEPS.findIndex((s) => s.statuses.includes(status));
}

export interface AgentStats {
  /** Tasks this agent worked on that completed. */
  merged: number;
  /** Tasks this agent reviewed that completed. */
  reviewed: number;
  /** Of the completed tasks it worked on, the share that needed no rework round (iteration 0); undefined with none completed. */
  firstTime: number | undefined;
  /** Mean reported cost of a completed task it worked on; undefined when no task reports a cost. */
  avgCostUsd: number | undefined;
  /** Mean minutes from start to completion of the tasks it worked on; undefined when none has both times. */
  avgMinutes: number | undefined;
}

/** What the project's task list says about an agent. Every number here is counted from tasks; nothing is estimated. */
export function agentStats(agentId: string, tasks: readonly Task[]): AgentStats {
  const worked = tasks.filter((x) => x.assignee === agentId && x.status === 'COMPLETED');
  const reviewed = tasks.filter((x) => x.reviewer === agentId && x.status === 'COMPLETED').length;
  const costs = worked.map((x) => x.costUsd).filter((c) => c > 0);
  const times = worked
    .filter((x) => x.started && x.completed)
    .map((x) => toMs(x.completed) - toMs(x.started))
    .filter((d) => d > 0);
  const mean = (xs: number[]) => xs.reduce((a, b) => a + b, 0) / xs.length;
  return {
    merged: worked.length,
    reviewed,
    firstTime: worked.length ? worked.filter((x) => (x.iteration ?? 0) === 0).length / worked.length : undefined,
    avgCostUsd: costs.length ? mean(costs) : undefined,
    avgMinutes: times.length ? mean(times) / 60_000 : undefined,
  };
}

/**
 * The caption under an agent's spend figure: it is the sum over every usage row, so it says how many (the page lists only the
 * newest 50) and how many of those costs were estimated, not reported (audit 2026-10-16, M21).
 */
export function spentLabel(t: Pick<UsageTotals, 'rows' | 'estimated'>): string {
  const n = (x: number) => x.toLocaleString('en-US');
  return `spent in all ${n(t.rows)} usage ${t.rows === 1 ? 'row' : 'rows'}${t.estimated > 0 ? `, ${n(t.estimated)} estimated` : ''}`;
}

/** "14 m", "1 h 05 m", "2.1 d" for an average duration in minutes. */
export function minutesText(m: number): string {
  if (m < 1) return 'under 1 m';
  if (m < 60) return `${Math.round(m)} m`;
  if (m < 60 * 24) return `${Math.floor(m / 60)} h ${String(Math.round(m % 60)).padStart(2, '0')} m`;
  return `${(m / (60 * 24)).toFixed(1)} d`;
}

/** The figures in the Crew page's header: how many agents, how many at work, how many wait on the person. */
export interface CrewFigures {
  agents: number;
  working: number;
  /** Agents waiting on the person: their presence says so, or one of their requests is open (`waitingOn`). */
  waiting: number;
  stopped: number;
}

const AT_WORK = new Set(['Working', 'Thinking']);

/** Counts the Crew page's figures from the team, its presence and the agents with a request open for the person. */
export function crewFigures(agents: readonly { id: string }[], presence: readonly PresenceState[], waitingOn: ReadonlySet<string> = new Set()): CrewFigures {
  const at = (id: string) => presence.find((p) => p.agentId === id);
  return {
    agents: agents.length,
    working: agents.filter((a) => !waitingOn.has(a.id) && AT_WORK.has(at(a.id)?.presence ?? '')).length,
    waiting: agents.filter((a) => waitingOn.has(a.id) || agentStatus(at(a.id)) === 'waiting-for-you').length,
    stopped: agents.filter((a) => !waitingOn.has(a.id) && isStopped(at(a.id))).length,
  };
}

/** How many accepted skills are given to one agent. */
export const skillCount = (skills: readonly { status: string; agents?: string[] | null }[], agentId: string): number =>
  skills.filter((k) => k.status === 'accepted' && (k.agents ?? []).includes(agentId)).length;
