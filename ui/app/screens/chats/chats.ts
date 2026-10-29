// Pure helpers for the Chats section (no DOM, no services): times, cost, snippets, adapter choices, path checks.

import type { AdapterInfo, Connection } from '../../api/types.ts';
import type { Chat, ChatMcpGrant, ChatMessage, ChatSkillRef } from '../../api/chatClient.ts';
import type { LibrarySkill } from '../../api/extTypes.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import { formatUsd } from '../../lib/format.ts';
import { adapterLabel, chatInstall, cliName, type CliInstall } from '../shared/cli-name.ts';

// --- a chat's properties (decision P60): library skills and MCP servers ---

/** The key of a library skill: registry and name. */
export const skillKey = (k: Pick<ChatSkillRef, 'registryId' | 'name'>): string => `${k.registryId}/${k.name}`;

/** A library skill a chat can be given: available, and still in its registry. */
export const skillUsable = (k: Pick<LibrarySkill, 'status' | 'gone'>): boolean => k.status === 'available' && !k.gone;

/** The first sentence of a description (at most max characters). */
export function firstSentence(text: string, max = 140): string {
  const t = text.replace(/\s+/g, ' ').trim();
  const m = /^(.+?[.!?])(\s|$)/.exec(t);
  const s = m ? m[1]! : t;
  return s.length > max ? `${s.slice(0, max - 1)}…` : s;
}

/**
 * The library skills the picker shows: the chosen ones first (also when they cannot be used any more, so they can be taken
 * away), then the usable ones matching the filter (name, description or registry), at most `max` in all.
 */
export function pickableSkills(library: readonly LibrarySkill[], chosen: readonly ChatSkillRef[], query: string, max = 60): LibrarySkill[] {
  const picked = new Set(chosen.map(skillKey));
  const q = query.trim().toLowerCase();
  const matches = (k: LibrarySkill) => !q || k.name.toLowerCase().includes(q) || k.description.toLowerCase().includes(q) || k.registryId.toLowerCase().includes(q);
  const first = library.filter((k) => picked.has(skillKey(k)));
  const rest = library.filter((k) => !picked.has(skillKey(k)) && skillUsable(k) && matches(k));
  return [...first, ...rest].slice(0, Math.max(max, first.length));
}

/** Add or take away one skill, keeping the order chosen. */
export function toggleSkill(chosen: readonly ChatSkillRef[], k: ChatSkillRef, on: boolean): ChatSkillRef[] {
  const rest = chosen.filter((c) => skillKey(c) !== skillKey(k));
  return on ? [...rest, { registryId: k.registryId, name: k.name }] : rest;
}

/** Add or take away one server. A server the chat already had keeps its tools; a new one asks the core for its tools now (empty). */
export function toggleServer(chosen: readonly ChatMcpGrant[], serverId: string, on: boolean, had: readonly ChatMcpGrant[] = []): ChatMcpGrant[] {
  const rest = chosen.filter((g) => g.serverId !== serverId);
  if (!on) return rest;
  const before = had.find((g) => g.serverId === serverId);
  return [...rest, { serverId, tools: before ? [...before.tools] : [] }];
}

/** The tools a server offers a chat: listed and not flagged as reading like instructions. */
export const grantableTools = (s: Pick<McpLibraryServer, 'tools'>): string[] => s.tools.filter((t) => !(t.flags ?? []).length).map((t) => t.name);

/** Two lists of skills are the same choice. */
export const sameSkills = (a: readonly ChatSkillRef[], b: readonly ChatSkillRef[]): boolean => a.length === b.length && a.every((k, i) => skillKey(k) === skillKey(b[i]!));

/** Two lists of grants are the same choice. */
export const sameServers = (a: readonly ChatMcpGrant[], b: readonly ChatMcpGrant[]): boolean =>
  a.length === b.length && a.every((g, i) => g.serverId === b[i]!.serverId && g.tools.join(',') === b[i]!.tools.join(','));

/** "2 skills", "1 MCP server": what a chat was given, for its header chips (empty parts left out). */
export function propsChips(c: Pick<Chat, 'skills' | 'mcpServers'>): string[] {
  const out: string[] = [];
  const k = c.skills?.length ?? 0;
  const m = c.mcpServers?.length ?? 0;
  if (k) out.push(`${k} ${k === 1 ? 'skill' : 'skills'}`);
  if (m) out.push(`${m} MCP ${m === 1 ? 'server' : 'servers'}`);
  return out;
}

/** Permission modes that skip approvals (docs/chats.md: skipsApprovals). */
export const SKIP_MODES = new Set(['bypassPermissions', 'auto', 'dontAsk']);

export const skipsApprovals = (mode: string): boolean => SKIP_MODES.has(mode);

/** The message a Retry after "Allow for this chat" sends. */
export const RETRY_TEXT = 'Continue — the tool is now allowed.';

/** Whether a message is the canned retry Drydock sends for the person after an allow (shown as a note, not as their words). */
export const isRetryMessage = (m: { role: string; body: string }): boolean => m.role === 'user' && m.body === RETRY_TEXT;

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
  // "+ 2 turns unknown" read as if turns were missing (UI review P1-2): it is the cost of those turns that is not known
  return c.costUnknownTurns > 0 ? `${money}, and ${c.costUnknownTurns} ${c.costUnknownTurns === 1 ? "turn's" : "turns'"} cost unknown` : money;
}

export { adapterLabel };

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

/** The option text of an adapter in the new chat dialog; `where` ("on Windows") tells it apart from its installs in WSL. */
export function adapterOptionText(a: AdapterInfo, where = ''): string {
  const name = where ? `${adapterLabel(a.id)} ${where}` : adapterLabel(a.id);
  const why = adapterUnavailable(a);
  if (why) return `${name} (${why})`;
  if (a.detection.status === 'unauthenticated') return `${name} (not signed in)`;
  return a.detection.version ? `${name} ${a.detection.version}` : name;
}

/**
 * Why a CLI install inside a WSL distro cannot start a chat, or '' when it can: as for one on Windows (adapterUnavailable), a
 * degraded install (an untested version, interop off, a failed Test) is offered, and the Connections page says what it lacks.
 */
export function wslUnavailable(c: Pick<Connection, 'status' | 'version' | 'gates'>): string {
  if (c.status === 'unauthenticated') return 'not signed in';
  if (c.status === 'unsupported_version') return `unsupported version${c.version ? ` ${c.version}` : ''}`;
  if ((c.gates ?? []).some((g) => !g.acked)) return 'a notice to accept on the Connections page';
  return '';
}

/** One entry of the new chat dialog's Agent CLI picker: an adapter on Windows, or its install inside a WSL distro. */
export interface ChatChoice {
  /** The select value: the adapter id, or the WSL Connection's key ("claude@wsl:Ubuntu"). */
  value: string;
  adapter: AdapterInfo;
  /** '' on Windows. */
  wslDistro: string;
  text: string;
  /** Why it cannot start a chat ('' when it can). */
  why: string;
}

/**
 * The Agent CLI choices of the new chat dialog: every adapter on Windows (sortChatAdapters order), each followed by its
 * installs inside WSL distros (the WSL Connections the Connections page shows, by distro). A distro without the CLI is not
 * offered; one that cannot start a chat (wslUnavailable) is offered disabled with the reason. With an install in WSL, the
 * Windows one says "on Windows". A WSL choice is named as the Connections page names it (cliName): "Claude Code WSL", with
 * the distro only when the CLI is installed in another distro too.
 */
export function chatChoices(adapters: readonly AdapterInfo[], connections: readonly Connection[]): ChatChoice[] {
  const inWSL = connections.filter((c) => c.runMode === 'wsl' && !!c.distro && c.installed && c.status !== 'not_installed');
  const out: ChatChoice[] = [];
  for (const a of sortChatAdapters(adapters)) {
    const mine = (a.capabilities?.runModes ?? []).includes('wsl') ? inWSL.filter((c) => c.adapter === a.id).sort((x, y) => x.distro!.localeCompare(y.distro!)) : [];
    out.push({ value: a.id, adapter: a, wslDistro: '', text: adapterOptionText(a, mine.length ? 'on Windows' : ''), why: adapterUnavailable(a) });
    for (const c of mine) {
      const why = wslUnavailable(c);
      const label = cliName(c, connections);
      out.push({ value: c.key || `${a.id}@wsl:${c.distro}`, adapter: a, wslDistro: c.distro!, text: why ? `${label} (${why})` : c.version ? `${label} ${c.version}` : label, why });
    }
  }
  return out;
}

/** The choice the dialog starts with (or keeps): the one picked if it can start, else Claude Code (Windows first), else the first that can. */
export function pickChatChoice(choices: readonly ChatChoice[], picked: string): ChatChoice | undefined {
  const ok = choices.filter((c) => !c.why);
  return ok.find((c) => c.value === picked) ?? ok.find((c) => c.adapter.id === 'claude') ?? ok[0];
}

/**
 * The chat's CLI for its header: "Claude Code", or "Claude Code WSL" inside a distro, named as the Connections page names that
 * install (cliName with `installs`, the Connections list): the distro only when the CLI is installed in another distro too
 * (which distro otherwise: chatWhere, its tooltip).
 */
export const chatCliText = (c: Pick<Chat, 'adapter' | 'wslDistro'>, installs: readonly CliInstall[] = []): string => cliName(chatInstall(c), installs);

/** Where a chat's CLI runs, in words for a tooltip: "Runs inside the WSL distro Ubuntu", or '' on Windows. */
export const chatWhere = (c: Pick<Chat, 'wslDistro'>): string => (c.wslDistro ? `Runs inside the WSL distro ${c.wslDistro}` : '');

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

export { chatTitle } from '../../api/chatClient.ts';

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

// --- the graphite Chats screen (decision P97) ---

/**
 * When a chat was last active, for its row in the list: "now", "5 min", "3 h" (today), "yesterday", the weekday within a week
 * ("Mon"), then the date.
 */
export function listWhen(ms: number, now = Date.now()): string {
  if (!ms) return '';
  const d = Math.max(0, now - ms);
  const min = Math.floor(d / 60_000);
  if (min < 1) return 'now';
  if (min < 60) return `${min} min`;
  const then = new Date(ms);
  const today = new Date(now);
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((day(today) - day(then)) / 86_400_000);
  if (days <= 0) return `${Math.floor(min / 60)} h`;
  if (days === 1) return 'yesterday';
  if (days < 7) return then.toLocaleDateString('en-US', { weekday: 'short' });
  return then.toLocaleDateString([], then.getFullYear() === today.getFullYear() ? { month: 'short', day: 'numeric' } : { year: 'numeric', month: 'short', day: 'numeric' });
}

/** The faint line of a chat's row: its project (when it is in one), its CLI and when it was last active, joined by " · ". */
export function chatRowLine(parts: { project?: string; cli: string; when: string }): string {
  return [parts.project, parts.cli, parts.when].filter(Boolean).join(' · ');
}

/** A permission mode in plain words, for the composer's approval button. A mode Drydock does not know is named as it is. */
export function modeWords(mode: string): string {
  switch (mode) {
    case '':
    case 'default':
      return 'Asks before commands';
    case 'acceptEdits':
      return 'Edits files, asks before commands';
    case 'plan':
      return 'Plans only, changes nothing';
    case 'bypassPermissions':
      return 'Never asks, skips approvals';
    case 'auto':
      return 'Decides itself, skips approvals';
    case 'dontAsk':
      return 'Does not ask, skips approvals';
    default:
      return `${mode} mode`;
  }
}

/**
 * A model id as a person reads it: "claude-opus-5-5" and "opus-5-5-20261001" are "Opus 5.5", the alias "sonnet" is "Sonnet";
 * an id of another vendor stays as it is.
 */
export function modelName(id: string): string {
  const m = id.trim().replace(/\[[^\]]*\]$/, '');
  const x = /^(?:claude-)?(opus|sonnet|haiku|fable)(?:-(\d+)(?:-(\d{1,2}))?)?(?:-\d{8})?$/i.exec(m);
  if (!x) return m;
  const family = x[1]!.charAt(0).toUpperCase() + x[1]!.slice(1).toLowerCase();
  const version = x[2] ? (x[3] ? `${x[2]}.${x[3]}` : x[2]) : '';
  return version ? `${family} ${version}` : family;
}

/**
 * The effort Claude Code uses when none is chosen, by model (its documentation, 2026-10: medium for Opus, Sonnet and Haiku 5.5,
 * xhigh for Opus 4.7, high for the others); "" for another CLI or a model not known yet. The person's own Claude Code settings
 * (effortLevel) can change it, which Drydock does not read.
 */
export function defaultEffort(adapter: string, model: string): string {
  if (adapter !== 'claude' || !model.trim()) return '';
  const name = modelName(model);
  if (!/\d/.test(name)) return ''; // an alias ("opus"): which model it means is the CLI's to say
  if (/^(Opus|Sonnet|Haiku) 5\.5$/.test(name)) return 'medium';
  if (name === 'Opus 4.7') return 'xhigh';
  return 'high';
}

/** The chat's model in words: the one chosen, else the one its CLI reported as its default, else "default model". */
export function chatModelText(c: Pick<Chat, 'model' | 'cliModel'>): string {
  const m = c.model.trim() || (c.cliModel ?? '').trim();
  return m ? modelName(m) : 'default model';
}

/** The chat's effort: the one chosen, else its CLI's default for the model ("" when not known); `chosen` says which. */
export function chatEffort(c: Pick<Chat, 'adapter' | 'model' | 'effort' | 'cliModel'>): { text: string; chosen: boolean } {
  if (c.effort.trim()) return { text: c.effort.trim(), chosen: true };
  return { text: defaultEffort(c.adapter, c.model.trim() || (c.cliModel ?? '')), chosen: false };
}

/** The chat runs in its own private folder (the core's chats/<id> folder, on Windows or in a distro's home). */
export function inOwnFolder(c: Pick<Chat, 'id' | 'cwd'>): boolean {
  const p = c.cwd.replace(/\\/g, '/').replace(/\/+$/, '');
  return p.endsWith(`/chats/${c.id}`);
}

/**
 * Where the chat runs, in words for the right of its header, from its fields: in a project, the project's folder; without
 * one, inside its WSL distro, in its own folder, or in the folder chosen for it.
 */
export function chatPlaceWords(c: Pick<Chat, 'id' | 'projectId' | 'wslDistro' | 'cwd'>, projectName?: string): string {
  if (c.projectId) return `${projectName || 'Project'} · runs in the project's folder${c.wslDistro ? ', inside the distro' : ''}`;
  if (c.wslDistro) return 'No project · runs inside the distro';
  return inOwnFolder(c) ? 'No project · runs in its own folder' : 'No project · runs in the folder you chose';
}

/**
 * The chat's folder for its header, as its CLI sees it: a folder inside a WSL distro as the distro names it
 * (\\wsl.localhost\Ubuntu\home\kyle\x is /home/kyle/x), the home folder there as ~; then shortened in the middle.
 */
export function folderText(c: Pick<Chat, 'cwd' | 'wslDistro'>, max = 48): string {
  let p = c.cwd;
  const m = /^\\\\(?:wsl\.localhost|wsl\$)\\[^\\]+(\\.*)?$/i.exec(p);
  if (m) p = (m[1] ?? '\\').replace(/\\/g, '/');
  if (c.wslDistro || m) p = p.replace(/^\/home\/[^/]+(?=\/|$)/, '~').replace(/^\/root(?=\/|$)/, '~');
  return shortPath(p, max);
}

/** The command a waiting request would run, or '' when it is not a shell command. */
export function askCommandOf(a: { input?: unknown }): string {
  const f = a.input && typeof a.input === 'object' && !Array.isArray(a.input) ? (a.input as Record<string, unknown>) : {};
  return typeof f.command === 'string' ? f.command : '';
}

/** What a waiting request shows in its box: the command, else the call in one line without its tool's name. */
export function askCallText(a: { tool: string; summary: string; input?: unknown }): string {
  const cmd = askCommandOf(a);
  if (cmd) return cmd;
  return a.tool && a.summary.startsWith(`${a.tool}: `) ? a.summary.slice(a.tool.length + 2) : a.summary;
}

/** The head of a waiting request: "Claude Code wants to run a command", or "... wants to use Fetch". */
export function askHead(cli: string, a: { tool: string; input?: unknown }): string {
  return askCommandOf(a) ? `${cli} wants to run a command` : `${cli} wants to use ${a.tool || 'a tool'}`;
}

/** An allow rule in the words of its button: Bash(git diff --stat) is "git diff --stat" (shortened past 40 characters). */
export function ruleWords(rule: string): string {
  const m = /^Bash\((.*)\)$/s.exec(rule);
  const w = m ? m[1]!.trim() : rule;
  return w.length > 40 ? `${w.slice(0, 39)}…` : w;
}
