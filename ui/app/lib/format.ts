import type { Agent, Author, PresenceState, TaskStatus } from '../api/types.ts';

export function formatTime(iso: string | number | undefined): string {
  if (iso === undefined || iso === '' || iso === 0) return '';
  const d = typeof iso === 'number' ? new Date(iso) : new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function formatDateTime(iso: string | number | undefined): string {
  if (iso === undefined || iso === '' || iso === 0) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString();
}

/** A day, in the reader's locale ("15 Oct 2026"): for "verified on". */
export function formatDate(iso: string | number | undefined): string {
  if (iso === undefined || iso === '' || iso === 0) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' });
}

const STATUS_LABEL: Record<string, string> = {
  BACKLOG: 'Backlog',
  READY: 'Ready',
  CLAIMED: 'Claimed',
  IN_PROGRESS: 'In progress',
  BLOCKED: 'Blocked',
  WAITING_HUMAN: 'Waiting for you',
  REVIEW: 'In review',
  VERIFICATION: 'Awaiting your approval',
  COMPLETED: 'Completed',
  FAILED: 'Failed',
  CANCELLED: 'Cancelled',
};
const STATUS_ICON: Record<string, string> = {
  BACKLOG: '○',
  READY: '◌',
  CLAIMED: '◔',
  IN_PROGRESS: '▶',
  BLOCKED: '⛔',
  WAITING_HUMAN: '⏸',
  REVIEW: '◑',
  VERIFICATION: '◕',
  COMPLETED: '✔',
  FAILED: '✖',
  CANCELLED: '⊘',
};
export const statusLabel = (s: TaskStatus | string): string => STATUS_LABEL[s] ?? s;
export const statusIcon = (s: TaskStatus | string): string => STATUS_ICON[s] ?? '○';
export const statusTone = (s: string): 'ok' | 'err' | 'warn' | 'info' | 'muted' => {
  if (s === 'COMPLETED') return 'ok';
  if (s === 'FAILED' || s === 'CANCELLED') return 'err';
  if (s === 'WAITING_HUMAN' || s === 'VERIFICATION' || s === 'BLOCKED') return 'warn';
  if (s === 'BACKLOG') return 'muted';
  return 'info';
};

export function titleCase(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
}

/** Display name for a message author or agent id; the id is shown if the agent is not (yet) in the team list. */
export function authorName(a: Author, agents: readonly Agent[]): string {
  switch (a.kind) {
    case 'agent':
      return agents.find((x) => x.id === a.id)?.name ?? a.display ?? a.id;
    case 'tool':
      return titleCase(a.id);
    case 'system':
      return 'System';
    default:
      return a.display ?? a.id;
  }
}

/** DM rooms are named "dm:human:<you>+<agent id>"; show the agent's name instead. */
export function roomLabel(r: { kind: string; name: string }, agents: readonly Agent[]): string {
  if (r.kind !== 'dm') return r.name;
  const id = r.name.slice(r.name.lastIndexOf('+') + 1);
  return agents.find((a) => a.id === id)?.name ?? id;
}

export const agentName = (id: string | undefined, agents: readonly Agent[]): string => (id ? (agents.find((a) => a.id === id)?.name ?? id) : '');

export const presenceFor = (agentId: string, list: readonly PresenceState[] | undefined): PresenceState | undefined => list?.find((p) => p.agentId === agentId);

/**
 * Cost text. The core only has a summed `costUsd` per task; 0 means no cost was recorded, which is not the same as free,
 * so it is shown as "not reported" (docs/ui.md section 1).
 */
export function formatCost(usd: number | undefined | null): string {
  if (usd === undefined || usd === null || !(usd > 0)) return 'not reported';
  return formatUsd(usd);
}

/**
 * A dollar amount, the one rule for every screen: four decimals under a cent, else two. An amount above zero is never shown
 * as $0 (under $0.00005 it reads "<$0.0001").
 */
export function formatUsd(usd: number): string {
  if (usd > 0 && usd < 0.00005) return '<$0.0001';
  return `$${usd > 0 && usd < 0.01 ? usd.toFixed(4) : usd.toFixed(2)}`;
}

/** 1536 -> "1.5 KB". Sizes in the packaging screens come from the core as byte counts. */
export function formatBytes(n: number | undefined | null): string {
  if (n === undefined || n === null || !Number.isFinite(n)) return 'unknown';
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  // Round first, then pick the unit, so 1,048,500 bytes reads "1.0 MB" and never "1024 KB".
  const shown = (x: number) => Number(x.toFixed(x < 10 ? 1 : 0));
  while (shown(v) >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const r = shown(v);
  return `${r.toFixed(r < 10 ? 1 : 0)} ${units[i]}`;
}

/** An agent whose program is not running: the core says it as presence Paused with the label "stopped", and there is no presence at all before the first start. */
export const isStopped = (p: PresenceState | undefined): boolean => !p || (p.presence === 'Paused' && p.label === 'stopped');

/**
 * One status vocabulary for an agent, the same on every page (the UI review of 2026-10-21, P86). The core's presence has more
 * states than a person needs to tell apart; each maps to one of these, and what the agent is doing stays in its activity text.
 */
export type AgentStatus = 'working' | 'waiting-for-you' | 'waiting' | 'starting' | 'idle' | 'error' | 'stopped';

export const STATUS_WORDS: Record<AgentStatus, string> = {
  working: 'Working',
  'waiting-for-you': 'Waiting for you',
  waiting: 'Waiting',
  starting: 'Starting',
  idle: 'Idle',
  error: 'Error',
  stopped: 'Stopped',
};

/**
 * The status of an agent from its presence: on a turn (Working, Thinking) is working; held for a person (Needs Attention, Paused,
 * or waiting on a permission request) is waiting for you; waiting on anything else is waiting; a failed turn is an error; a
 * finished turn is idle; a program that is not running, or never ran, is stopped. Never "Unknown".
 */
export function agentStatus(p: PresenceState | undefined): AgentStatus {
  if (isStopped(p)) return 'stopped';
  switch (p!.presence) {
    case 'Working':
    case 'Thinking':
      return 'working';
    case 'Needs Attention':
    case 'Paused':
      return 'waiting-for-you';
    case 'Waiting':
      return /permission/i.test(p!.activity ?? '') ? 'waiting-for-you' : 'waiting';
    case 'Failed':
      return 'error';
    case 'Idle':
      return p!.label === 'starting' ? 'starting' : 'idle';
    default:
      return 'idle';
  }
}

/** The status as words (PresenceDot says them too): "Working", "Waiting for you", "Waiting", "Starting", "Idle", "Error" or "Stopped". */
export function presenceWords(p: PresenceState | undefined): string {
  return STATUS_WORDS[agentStatus(p)];
}

/** "1 file", "2 files": a count with its noun, so a page never writes "file(s)". `many` is for nouns that do not take an s. */
export const plural = (n: number, one: string, many = `${one}s`): string => `${n} ${n === 1 ? one : many}`;

/** "just now", "14 min ago", "3 h ago", "2 d ago": how long ago a time was; "" for no time. A time ahead reads as just now. */
export function agoWords(ms: number, now = Date.now()): string {
  if (!ms) return '';
  const s = Math.max(0, Math.floor((now - ms) / 1000));
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}
