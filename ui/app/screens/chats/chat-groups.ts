// The calm chat (decision P139): how the stored messages of a chat are read as a conversation. Pure and framework-free, so
// every rule here is unit-tested (chat-groups.test.ts).
//
//   - A burst: the tool calls one after another between two pieces of the agent's text are one quiet line ("Read 6 files, ran 3
//     commands · 4.2 s"), opened to today's tool cards. A failed call opens its burst by itself; a running burst names its
//     current step on the line. Reasoning before a call folds into the same line ("Thought for 6 s · read 4 files").
//   - Changes: the file edits of one turn of the agent (everything after one of the person's messages, up to the next) roll up
//     into one "Changed N files +A −D" card, placed after the turn's last row, with Review changes.
//   - Suggested replies: after the agent's final answer, when it ends with a question or an offer, up to three replies.
//   - Verbose (Ctrl+O): every burst open, remembered per viewer.

import type { ChatMessage } from '../../api/chatClient.ts';
import { durationText, editLines, toolLabel, type DiffLine } from './chat-tool.tsx';

/** One row of the conversation as it is drawn. */
export type FeedItem =
  | { kind: 'msg'; key: string; m: ChatMessage; /** its index in the messages */ index: number }
  | { kind: 'burst'; key: string; tools: ChatMessage[]; /** the index of its first call in the messages */ index: number }
  | { kind: 'changes'; key: string; changes: TurnChanges };

/** One file a turn changed: its path and the lines its edits removed and added (in the order they were made). */
export interface FileChange {
  path: string;
  added: number;
  removed: number;
  lines: DiffLine[];
  /** How many edits of the turn touched it. */
  edits: number;
}

export interface TurnChanges {
  files: FileChange[];
  added: number;
  removed: number;
}

type Field = Record<string, unknown>;
const fields = (input: unknown): Field => (input && typeof input === 'object' && !Array.isArray(input) ? (input as Field) : {});
const str = (f: Field, k: string): string => (typeof f[k] === 'string' ? (f[k] as string) : '');

const READS = new Set(['Read', 'NotebookRead', 'read_file', 'view']);
const EDITS = new Set(['Edit', 'MultiEdit', 'Write', 'NotebookEdit', 'edit', 'write_file', 'apply_patch']);
const SEARCHES = new Set(['Grep', 'Glob', 'LS', 'search', 'grep', 'glob']);
const WEB = new Set(['WebFetch', 'WebSearch', 'web_fetch', 'web_search']);

export type ToolKind = 'read' | 'edit' | 'command' | 'search' | 'web' | 'other';

/** What kind of work a tool call is, for the burst's line. */
export function toolKind(m: Pick<ChatMessage, 'toolName' | 'toolInput'>): ToolKind {
  const name = m.toolName ?? '';
  const f = fields(m.toolInput);
  if (name === 'Bash' || str(f, 'command')) return 'command';
  if (READS.has(name)) return 'read';
  if (EDITS.has(name) || editLines(m.toolInput)) return 'edit';
  if (SEARCHES.has(name)) return 'search';
  if (WEB.has(name)) return 'web';
  return 'other';
}

/** The file a call reads or edits, '' when it names none. */
export function toolPath(m: Pick<ChatMessage, 'toolInput'>): string {
  const f = fields(m.toolInput);
  return str(f, 'file_path') || str(f, 'path') || str(f, 'notebook_path') || str(f, 'filePath');
}

/**
 * The rows of a conversation: runs of tool calls become one burst, and after the last row of each turn of the agent that edited
 * files comes its changes card. Row keys stay put while a turn adds rows (a burst is keyed by its first call, a changes card by
 * the turn's first edit), so the virtual list keeps its place.
 */
export function groupFeed(msgs: readonly ChatMessage[]): FeedItem[] {
  const out: FeedItem[] = [];
  let burst: ChatMessage[] | undefined;
  let turnEdits: ChatMessage[] = [];
  const endTurn = () => {
    if (!turnEdits.length) return;
    const changes = turnChanges(turnEdits);
    if (changes.files.length) out.push({ kind: 'changes', key: `changes:${turnEdits[0]!.id}`, changes });
    turnEdits = [];
  };
  for (let i = 0; i < msgs.length; i++) {
    const m = msgs[i]!;
    if (m.role === 'tool') {
      if (!burst) {
        burst = [];
        out.push({ kind: 'burst', key: `burst:${m.id}`, tools: burst, index: i });
      }
      burst.push(m);
      if (toolKind(m) === 'edit' && m.toolStatus !== 'failed') turnEdits.push(m);
      continue;
    }
    burst = undefined;
    // the person's own message starts a new turn: the one before it ends here (a retry Drydock sent is the person's too)
    if (m.role === 'user') endTurn();
    out.push({ kind: 'msg', key: m.id, m, index: i });
  }
  endTurn();
  return out;
}

/** The files the edits of one turn changed, each once, in the order they were first edited, with the lines added and removed. */
export function turnChanges(edits: readonly ChatMessage[]): TurnChanges {
  const byPath = new Map<string, FileChange>();
  for (const m of edits) {
    const path = toolPath(m);
    if (!path) continue;
    let lines = editLines(m.toolInput);
    if (!lines) {
      // a whole file written: every line is added
      const content = str(fields(m.toolInput), 'content');
      lines = content ? content.replace(/\n$/, '').split('\n').map((text) => ({ kind: '+' as const, text })) : [];
    }
    const f = byPath.get(path) ?? { path, added: 0, removed: 0, lines: [], edits: 0 };
    for (const l of lines) l.kind === '+' ? f.added++ : f.removed++;
    f.lines = f.lines.concat(lines);
    f.edits++;
    byPath.set(path, f);
  }
  const files = [...byPath.values()];
  return { files, added: files.reduce((a, f) => a + f.added, 0), removed: files.reduce((a, f) => a + f.removed, 0) };
}

/** "Changed 3 files" (the card's title). */
export const changedText = (c: Pick<TurnChanges, 'files'>): string => `Changed ${c.files.length} ${c.files.length === 1 ? 'file' : 'files'}`;

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** The counts of a burst in words: "read 6 files, ran 3 commands" (lower case: the caller capitalises the line). */
export function burstCounts(tools: readonly ChatMessage[]): string {
  const n: Record<ToolKind, number> = { read: 0, edit: 0, command: 0, search: 0, web: 0, other: 0 };
  const read = new Set<string>();
  const edited = new Set<string>();
  for (const m of tools) {
    const k = toolKind(m);
    n[k]++;
    const p = toolPath(m);
    if (k === 'read') read.add(p || m.id);
    if (k === 'edit') edited.add(p || m.id);
  }
  const parts: string[] = [];
  if (n.read) parts.push(`read ${plural(read.size, 'file', 'files')}`);
  if (n.edit) parts.push(`edited ${plural(edited.size, 'file', 'files')}`);
  if (n.command) parts.push(`ran ${plural(n.command, 'command', 'commands')}`);
  if (n.search) parts.push(plural(n.search, 'search', 'searches'));
  if (n.web) parts.push(plural(n.web, 'web lookup', 'web lookups'));
  if (n.other) {
    const only = tools.filter((m) => toolKind(m) === 'other');
    const names = new Set(only.map((m) => toolLabel(m.toolName)));
    parts.push(names.size === 1 ? `used ${[...names][0]}${n.other > 1 ? ` ${n.other} times` : ''}` : `used ${plural(n.other, 'tool', 'tools')}`);
  }
  return parts.join(', ');
}

/**
 * How long the agent thought before a row: from the end of the row before it (a tool call's end is its start plus how long it
 * took) to this row. Unknown (0) without a row before, or when the gap is not a believable thinking time.
 */
export function thinkingMs(prev: Pick<ChatMessage, 'createdAt' | 'toolMs'> | undefined, m: Pick<ChatMessage, 'createdAt'>): number {
  if (!prev || !prev.createdAt || !m.createdAt) return 0;
  const d = m.createdAt - (prev.createdAt + (prev.toolMs ?? 0));
  return d > 0 && d < 30 * 60_000 ? d : 0;
}

/** "Thought for 6 s", or "Thought" when how long is not known. */
export function thoughtText(ms: number): string {
  // whole seconds under a minute ("6 s"): a tenth of a second says nothing about thinking
  // and nothing under a second: that is the gap between two rows, not thinking anyone would want timed
  const d = ms < 1000 ? '' : ms < 59_500 ? `${Math.round(ms / 1000)} s` : durationText(ms);
  return d ? `Thought for ${d}` : 'Thought';
}

/** The state of a burst as a whole: running while one of its calls runs, failed when one failed, else done. */
export function burstState(tools: readonly ChatMessage[], running: boolean): 'running' | 'failed' | 'ok' {
  if (tools.some((m) => m.toolStatus === 'failed')) return 'failed';
  if (running && tools.some((m) => !m.toolStatus)) return 'running';
  return 'ok';
}

/** The step a running burst is on, in words: "Running npm test", "Reading calc.go", "Editing calc.go". */
export function stepText(m: Pick<ChatMessage, 'toolName' | 'toolInput'>): string {
  const f = fields(m.toolInput);
  const p = toolPath(m);
  const base = (x: string) => x.split(/[\\/]/).pop() || x;
  switch (toolKind(m)) {
    case 'command':
      return `Running ${str(f, 'command') || 'a command'}`;
    case 'read':
      return p ? `Reading ${base(p)}` : 'Reading';
    case 'edit':
      return p ? `Editing ${base(p)}` : 'Editing';
    case 'search':
      return str(f, 'pattern') ? `Searching for ${str(f, 'pattern')}` : 'Searching';
    case 'web':
      return str(f, 'url') ? `Fetching ${str(f, 'url')}` : 'Searching the web';
    default:
      return `Using ${toolLabel(m.toolName)}`;
  }
}

/** The total time of a burst's calls (what they reported). */
export const burstMs = (tools: readonly ChatMessage[]): number => tools.reduce((a, m) => a + (m.toolMs ?? 0), 0);

/**
 * The quiet line of a burst: "Thought for 6 s · read 4 files", "Read 6 files, ran 3 commands". `thought` is how long the agent
 * thought before its first call (0: unknown), and whether it thought at all is `thinking`.
 */
export function burstLine(tools: readonly ChatMessage[], thinking: { any: boolean; ms: number }): string {
  const counts = burstCounts(tools);
  const line = thinking.any ? [thoughtText(thinking.ms), counts].filter(Boolean).join(' · ') : counts;
  return line ? line[0]!.toUpperCase() + line.slice(1) : '';
}

// --- suggested replies ---

/** A reply offered after the agent's answer: the chip's label and the text it sends. */
export interface Reply {
  label: string;
  text: string;
}

/** What the end of an answer asks: an offer to do something ("Want me to fix it?"), a yes-or-no question, another question. */
export type Ask = { kind: 'offer'; what: string } | { kind: 'yesno' } | { kind: 'open' } | undefined;

const OFFER = [
  /^(?:do you )?want me to (.+)$/i,
  /^would you like me to (.+)$/i,
  /^(?:shall|should|can|may) i (.+)$/i,
  /^do you want me to (.+)$/i,
  /^let me know if you(?:'d| would)? (?:like|want) me to (.+)$/i,
  /^i can (?:also )?(.+?)(?:,? if you(?:'d)? (?:like|want)(?: me to)?)$/i,
];
const YESNO = /^(?:is|are|does|do|did|should|can|could|would|will|shall|was|were|has|have|had|am)\b/i;

/** The last sentence of an answer, Markdown emphasis and a closing quote left out. */
export function lastSentence(body: string): string {
  const lines = body.trim().split('\n').map((l) => l.trim()).filter(Boolean);
  const last = (lines.at(-1) ?? '').replace(/^(?:[-*>]|#+|\d+\.)\s+/, '');
  const plain = last.replace(/[*_]{1,3}([^*_]+)[*_]{1,3}/g, '$1').replace(/["'”’)]+$/, '').trim();
  const parts = plain.split(/(?<=[.!?])\s+(?=[A-Z"'`*_(])/);
  return (parts.at(-1) ?? '').trim();
}

/** What the end of an answer asks the person, if anything. */
export function askOf(body: string): Ask {
  const s = lastSentence(body);
  if (!s) return undefined;
  const q = s.endsWith('?');
  const core = s.replace(/[?.!]+$/, '').trim();
  for (const re of OFFER) {
    const m = re.exec(core);
    // "I can ..." is an offer only with "if you like": a plain "I can't find it." is not one
    if (m && m[1] && (q || /^(?:let me know|i can)/i.test(core))) return { kind: 'offer', what: m[1].trim() };
  }
  if (!q) return undefined;
  return YESNO.test(core) ? { kind: 'yesno' } : { kind: 'open' };
}

const SWAP: Record<string, string> = { your: 'my', yours: 'mine', you: 'me' };
const GO_AHEAD = new Set(['go', 'proceed', 'continue', 'start', 'do', 'apply', 'make', 'run']);

/** The "yes" to an offer: "Yes, fix it" for "Want me to fix the rounding?", sending "Yes, please fix the rounding." */
export function acceptReply(what: string): Reply {
  const words = what.split(/\s+/);
  const verb = (words[0] ?? '').toLowerCase();
  const said = words.map((w) => SWAP[w.toLowerCase()] ?? w).join(' ');
  const label = /^[a-z]{2,12}$/.test(verb) && !GO_AHEAD.has(verb) && words.length > 1 ? `Yes, ${verb} it` : 'Yes, go ahead';
  return { label, text: `Yes, please ${said}.` };
}

export const EXPLAIN_MORE: Reply = { label: 'Explain more', text: 'Explain that in more detail.' };

/** Up to three replies for the end of an answer; none when it asks nothing. */
export function suggestReplies(body: string): Reply[] {
  const a = askOf(body);
  if (!a) return [];
  if (a.kind === 'offer') return [acceptReply(a.what), { label: 'Not now', text: 'Not now, thanks.' }, EXPLAIN_MORE];
  if (a.kind === 'yesno') return [{ label: 'Yes', text: 'Yes.' }, { label: 'No', text: 'No.' }, EXPLAIN_MORE];
  return [{ label: 'You decide', text: 'You decide: go with what you think is best.' }, EXPLAIN_MORE];
}

// --- verbose mode (Ctrl+O) ---

export const VERBOSE_KEY = 'drydock.chat.verbose';

/** Whether the viewer chose verbose mode (every burst open); storage may be missing or refuse (a private window). */
export function readVerbose(store: Pick<Storage, 'getItem'> | undefined = globalThis.localStorage): boolean {
  try {
    return store?.getItem(VERBOSE_KEY) === '1';
  } catch {
    return false;
  }
}

export function writeVerbose(on: boolean, store: Pick<Storage, 'setItem' | 'removeItem'> | undefined = globalThis.localStorage): void {
  try {
    if (on) store?.setItem(VERBOSE_KEY, '1');
    else store?.removeItem(VERBOSE_KEY);
  } catch {
    // kept for this view only
  }
}

/** Ctrl+O (not with Shift, Alt or Meta): the verbose toggle. */
export function isVerboseKey(e: Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'shiftKey' | 'altKey' | 'metaKey' | 'repeat'>): boolean {
  return e.ctrlKey && !e.shiftKey && !e.altKey && !e.metaKey && !e.repeat && (e.key === 'o' || e.key === 'O');
}

/**
 * groupFeed for a feed whose message list is replaced (never changed in place) on every change: the rows are worked out again
 * only when the list is a new one, so a render for anything else (a key typed, the stream, a popover) costs nothing here.
 */
export function memoGroups(): (msgs: readonly ChatMessage[]) => FeedItem[] {
  let last: readonly ChatMessage[] | undefined;
  let items: FeedItem[] = [];
  return (msgs) => {
    if (msgs !== last) {
      last = msgs;
      items = groupFeed(msgs);
    }
    return items;
  };
}
