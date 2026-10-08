import type { Agent, Author, PresenceState, TaskStatus } from '../api/types';

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

export const agentName = (id: string | undefined, agents: readonly Agent[]): string => (id ? (agents.find((a) => a.id === id)?.name ?? id) : '');

export const presenceFor = (agentId: string, list: readonly PresenceState[] | undefined): PresenceState | undefined => list?.find((p) => p.agentId === agentId);

/**
 * Cost text. The core only has a summed `costUsd` per task; 0 means no cost was recorded, which is not the same as free,
 * so it is shown as "not reported" (docs/ui.md section 1).
 */
export function formatCost(usd: number | undefined | null): string {
  if (usd === undefined || usd === null || !(usd > 0)) return 'not reported';
  return `$${usd < 0.01 ? usd.toFixed(4) : usd.toFixed(2)}`;
}
