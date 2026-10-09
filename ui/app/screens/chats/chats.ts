// Pure helpers for the Chats section (no DOM, no services): times, cost, snippets, adapter choices, path checks.

import type { AdapterInfo } from '../../api/types.ts';
import type { Chat, ChatMessage } from '../../api/chatClient.ts';
import { formatUsd } from '../../lib/format.ts';

/** Permission modes that skip approvals (docs/chats.md: skipsApprovals). */
export const SKIP_MODES = new Set(['bypassPermissions', 'auto', 'dontAsk']);

export const skipsApprovals = (mode: string): boolean => SKIP_MODES.has(mode);

/** The message a Retry after "Allow for this chat" sends. */
export const RETRY_TEXT = 'Continue — the tool is now allowed.';

/** At most this many chats can be attached to one message (chat.send). */
export const MAX_ATTACH = 5;

/** A compact relative time for a list row: "now", "5m", "3h", "2d", then the date. */
export function relTime(ms: number, now = Date.now()): string {
  if (!ms) return '';
  const d = Math.max(0, now - ms);
  const min = Math.floor(d / 60_000);
  if (min < 1) return 'now';
  if (min < 60) return `${min}m`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h`;
  const days = Math.floor(h / 24);
  if (days < 7) return `${days}d`;
  const date = new Date(ms);
  const sameYear = date.getFullYear() === new Date(now).getFullYear();
  return date.toLocaleDateString([], sameYear ? { month: 'short', day: 'numeric' } : { year: 'numeric', month: 'short', day: 'numeric' });
}

/** 1234 -> "1.2k". */
export function compactNumber(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0';
  if (n < 1000) return String(Math.round(n));
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}

/** The cost of a chat. null is unknown, never $0; turns without a reported cost are said. */
export function costText(c: Pick<Chat, 'costUsd' | 'costUnknownTurns'>): string {
  if (c.costUsd === null || c.costUsd === undefined) return 'cost unknown';
  const money = formatUsd(c.costUsd);
  return c.costUnknownTurns > 0 ? `${money} + ${c.costUnknownTurns} ${c.costUnknownTurns === 1 ? 'turn' : 'turns'} unknown` : money;
}

/** "Claude Code" for the claude adapter, else the adapter id. */
export function adapterLabel(id: string): string {
  const names: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', gemini: 'Gemini CLI', cline: 'Cline', opencode: 'OpenCode', goose: 'Goose' };
  return names[id] ?? id;
}

/** "Claude" for the claude adapter (how the callouts speak about the agent), else its label. */
export function agentWord(id: string): string {
  return id === 'claude' ? 'Claude' : adapterLabel(id);
}

/** Why an adapter cannot start a chat, or '' when it can. */
export function adapterUnavailable(a: AdapterInfo): string {
  if (a.error) return a.error;
  const d = a.detection;
  if (!d.installed || d.status === 'not_installed') return 'not installed';
  if (d.status === 'unsupported_version') return `unsupported version${d.version ? ` ${d.version}` : ''}`;
  return '';
}

/** Claude first, then the usable adapters, then the rest, each by id. */
export function sortChatAdapters(list: readonly AdapterInfo[]): AdapterInfo[] {
  const rank = (a: AdapterInfo) => (a.id === 'claude' ? 0 : adapterUnavailable(a) ? 2 : 1);
  return [...list].sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id));
}

/** The option text of an adapter in the new chat dialog. */
export function adapterOptionText(a: AdapterInfo): string {
  const why = adapterUnavailable(a);
  if (why) return `${adapterLabel(a.id)} (${why})`;
  if (a.detection.status === 'unauthenticated') return `${adapterLabel(a.id)} (not signed in)`;
  return a.detection.version ? `${adapterLabel(a.id)} ${a.detection.version}` : adapterLabel(a.id);
}

/** Whether the app runs on Windows (the product target; Linux is only the development host). */
export const onWindows = (): boolean => typeof navigator !== 'undefined' && /Windows/i.test(navigator.userAgent);

/**
 * An absolute folder path as the core's filepath.IsAbs sees it: on Windows a drive (C:\x, C:/x) or a share (\\server\share,
 * //server/share) — "/x" is not absolute there; elsewhere a path starting with "/".
 */
export function isAbsolutePath(p: string, windows = onWindows()): boolean {
  const s = p.trim();
  if (!windows) return s.startsWith('/');
  return /^[a-zA-Z]:[\\/]/.test(s) || /^[\\/]{2}[^\\/]+[\\/][^\\/]+/.test(s);
}

/** Split a search snippet into plain and matched parts ([brackets] mark the matches). */
export function snippetParts(snippet: string): { text: string; hit: boolean }[] {
  const out: { text: string; hit: boolean }[] = [];
  const re = /\[([^\]]*)\]/g;
  let last = 0;
  for (let m = re.exec(snippet); m; m = re.exec(snippet)) {
    if (m.index > last) out.push({ text: snippet.slice(last, m.index), hit: false });
    if (m[1]) out.push({ text: m[1], hit: true });
    last = m.index + m[0].length;
  }
  if (last < snippet.length) out.push({ text: snippet.slice(last), hit: false });
  return out;
}

/** The title shown for a chat: its own, else "New chat". */
export const chatTitle = (c: Pick<Chat, 'title'>): string => c.title.trim() || 'New chat';

/** The middle of a long path is cut so the start (drive) and the end (folder) stay visible. */
export function shortPath(p: string, max = 48): string {
  if (p.length <= max) return p;
  const keep = max - 1;
  const head = Math.ceil(keep * 0.35);
  return `${p.slice(0, head)}…${p.slice(p.length - (keep - head))}`;
}

/** The tool input as pretty JSON for the collapsible block. */
export function toolInputText(input: unknown): string {
  if (input === undefined || input === null) return '';
  if (typeof input === 'object' && (input as { truncated?: boolean }).truncated) {
    return `${String((input as { preview?: string }).preview ?? '')}\n… (truncated)`;
  }
  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

/** Merge messages by id and keep them in seq order. */
export function mergeMessages(have: readonly ChatMessage[], add: readonly ChatMessage[]): ChatMessage[] {
  const byId = new Map(have.map((m) => [m.id, m] as const));
  for (const m of add) byId.set(m.id, m);
  return [...byId.values()].sort((a, b) => a.seq - b.seq);
}

/** Group rows: a message starts a group when its author side (you or the agent) changes or a quiet row sits between. */
export function startsGroup(prev: ChatMessage | undefined, m: ChatMessage): boolean {
  if (!prev) return true;
  const side = (x: ChatMessage) => (x.role === 'user' ? 'user' : x.role === 'assistant' || x.role === 'tool' ? 'agent' : 'note');
  if (side(m) === 'note') return false;
  return side(prev) !== side(m);
}
