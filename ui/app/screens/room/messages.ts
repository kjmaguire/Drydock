// Pure helpers for the room screens: how messages are grouped, divided by day, marked and counted. No DOM, so they are
// tested in Node (messages.test.ts).

import type { AttachmentInfo } from '../../api/attachClient.ts';
import type { Message, RoomMember } from '../../api/types.ts';

/** The markers shown as chips (room-protocol: [DECISION] [TODO] [STATUS] [RESULT] at the start of a line). */
export const MARKERS = new Set(['DECISION', 'TODO', 'STATUS', 'RESULT']);

export const markersOf = (m: Message) => (m.markers ?? []).filter((k) => MARKERS.has(String(k.marker)));

/** Consecutive messages from the same author closer than this are shown as one group (Discord, Slack). */
export const GROUP_MS = 5 * 60 * 1000;

export interface CheckPayload {
  check_kind?: string;
  check_status?: string;
  duration_ms?: number;
  failure_signature?: string;
  summary?: string;
  summary_truncated?: boolean;
  log_ref?: string;
}

export const checkOf = (m: Message): CheckPayload | undefined => {
  const p = m.payload as CheckPayload | null | undefined;
  return m.author.kind === 'tool' && p?.check_kind ? p : undefined;
};

/** SYSTEM and STATUS messages are quiet: they never start a conversation group and are drawn smaller. */
export const isQuiet = (m: Message): boolean => m.type === 'SYSTEM' || m.type === 'STATUS';

export const isRecord = (m: Message): boolean => m.type === 'APPROVAL_REQUEST' || m.type === 'APPROVAL';

export function attachmentsOf(m: Message): AttachmentInfo[] {
  const a = (m.payload as { attachments?: AttachmentInfo[] } | null | undefined)?.attachments;
  return Array.isArray(a) ? a : [];
}

export const authorKey = (m: Message): string => `${m.author.kind}:${m.author.id}`;

function time(iso: string): number {
  const n = new Date(iso).getTime();
  return Number.isNaN(n) ? 0 : n;
}

/** Local calendar day of a timestamp ("2026-10-09"), or '' when the time is unknown. */
export function dayKey(iso: string): string {
  const n = time(iso);
  if (!n) return '';
  const d = new Date(n);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

/** "Today", "Yesterday", or the date in words. */
export function dayLabel(iso: string, now: Date = new Date()): string {
  const n = time(iso);
  if (!n) return '';
  const d = new Date(n);
  const today = dayKey(now.toISOString());
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  const k = dayKey(iso);
  if (k === today) return 'Today';
  if (k === dayKey(y.toISOString())) return 'Yesterday';
  return d.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric', ...(d.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}) });
}

/** "14:05" for the message header; the full time is in the title. */
export function shortTime(iso: string): string {
  const n = time(iso);
  return n ? new Date(n).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '';
}

/** Whether `m` continues the group of `prev` (same author, close in time, same day, both plain conversation). */
export function continues(prev: Message | undefined, m: Message): boolean {
  if (!prev) return false;
  if (authorKey(prev) !== authorKey(m) || prev.trust !== m.trust) return false;
  if (m.type !== 'CHAT' || isQuiet(prev) || isRecord(prev) || checkOf(prev) || checkOf(m)) return false;
  if (m.thread_root_id || prev.thread_root_id) return false;
  if (dayKey(prev.created_at) !== dayKey(m.created_at)) return false;
  const gap = time(m.created_at) - time(prev.created_at);
  return gap >= 0 && gap < GROUP_MS;
}

export type Row =
  | { kind: 'day'; key: string; label: string }
  | { kind: 'new'; key: string }
  | { kind: 'message'; key: string; m: Message; head: boolean };

/**
 * The rows of the message list: a day divider before the first message of each day, a "new messages" divider before the
 * first message after `newAfterSeq` (when given), and each message marked as a group head or a continuation.
 */
export function buildRows(messages: readonly Message[], opts: { newAfterSeq?: number; now?: Date } = {}): Row[] {
  const rows: Row[] = [];
  let day = '';
  let prev: Message | undefined;
  let newShown = opts.newAfterSeq === undefined;
  for (const m of messages) {
    const k = dayKey(m.created_at);
    let breakGroup = false;
    if (k && k !== day) {
      day = k;
      // keyed by the message it stands before: a key names one place in the list (the windowed list anchors on keys), and
      // when an older page of the same day comes the divider moves up under a new key
      rows.push({ kind: 'day', key: `day-${m.id}`, label: dayLabel(m.created_at, opts.now) });
      breakGroup = true;
    }
    if (!newShown && m.seq > opts.newAfterSeq!) {
      newShown = true;
      rows.push({ kind: 'new', key: 'new' });
      breakGroup = true;
    }
    rows.push({ kind: 'message', key: m.id, m, head: breakGroup || !continues(prev, m) });
    prev = m;
  }
  return rows;
}

/** Replies per thread root, from the messages that carry thread_root_id. */
export function replyCounts(messages: readonly Message[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const m of messages) if (m.thread_root_id) out.set(m.thread_root_id, (out.get(m.thread_root_id) ?? 0) + 1);
  return out;
}

export const repliesText = (n: number): string => `${n} ${n === 1 ? 'reply' : 'replies'}`;

interface MentionLike {
  kind?: string;
  id?: string;
  members?: string[];
}

/** True when the core resolved a mention in this message to a human (directly or through a role). */
export function mentionsHuman(m: Message): boolean {
  if (m.author.kind === 'human') return false;
  return (m.mentions ?? []).some((x) => {
    const mm = x as MentionLike | null;
    if (!mm) return false;
    if (mm.kind === 'human') return true;
    return mm.kind === 'role' && (mm.members ?? []).some((k) => k.startsWith('human:'));
  });
}

/** The newest message written by a human, for "Ask agents to reply to my last message". */
export const lastHumanMessageId = (messages: readonly Message[]): string | undefined => {
  for (let i = messages.length - 1; i >= 0; i--) if (messages[i]!.author.kind === 'human') return messages[i]!.id;
  return undefined;
};

export function formatSize(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} B`;
}

/** A room member's key as the turn-taking settings name it (agents by id, humans and tools with a prefix). */
export const memberKey = (m: RoomMember): string =>
  m.member.kind === 'human' ? `human:${m.member.id}` : m.member.kind === 'tool' ? `tool:${m.member.id}` : m.member.kind === 'system' ? 'system' : m.member.id;

export const MODE_TEXT: Record<string, string> = {
  auto: 'Auto: simple rules decide who answers a message (an explicit mention first, then the task assignee, then a role match). No model chooses the speaker, and agents do not talk to each other without a bound.',
  sequential: 'Sequential: the members you list speak in that order, one turn each per round, and the debate stops at the round limit.',
  moderator: 'Moderator: the moderator you pick (you or an agent) names who speaks next. If the moderator is an agent, its choices are ordinary agent turns and are billed.',
};

/** The file name a room export is offered under. */
export const exportFileName = (roomName: string, kind: string, format: string): string =>
  `${roomName}-${kind === 'minutes' ? 'minutes' : 'adr-candidates'}.${format === 'json' ? 'json' : 'md'}`;

/** "a.txt is over 10 MB." and friends: the checks done before files are read. */
export const MAX_FILES = 4;
export const MAX_SIZE = 10 << 20;

export function checkFiles(have: number, incoming: readonly { name: string; size: number }[]): string | undefined {
  if (have + incoming.length > MAX_FILES) return `At most ${MAX_FILES} files go with one message.`;
  const big = incoming.find((f) => f.size > MAX_SIZE);
  return big ? `${big.name} is over ${MAX_SIZE >> 20} MB.` : undefined;
}
