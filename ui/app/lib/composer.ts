// The composer's typing aids (brief 21; docs/ui.md 14): @mentions, @role: mentions, #room links and /commands. Pure functions
// so they are tested without a DOM. The room core resolves what a mention means (internal/room/parse.go); these only help
// the human type a mention it will resolve.

export type Sigil = '@' | '@role:' | '#' | '/';

export interface Token {
  sigil: Sigil;
  query: string;
  /** byte range of the token in the text, sigil included */
  start: number;
  end: number;
}

export interface Suggestion {
  label: string;
  detail?: string;
  /** what replaces the token, sigil included */
  insert: string;
}

/** The mention or command being typed at the caret, if any. A slash command counts only at the start of the message. */
export function activeToken(text: string, caret: number): Token | undefined {
  const before = text.slice(0, caret);
  const m = /(^|[\s(])(@role:[A-Za-z0-9_-]*|@[A-Za-z0-9_.-]*|#[a-z0-9_-]*)$/.exec(before);
  if (m) {
    const tok = m[2]!;
    const start = caret - tok.length;
    const sigil: Sigil = tok.startsWith('@role:') ? '@role:' : tok[0] === '@' ? '@' : '#';
    return { sigil, query: tok.slice(sigil.length), start, end: caret };
  }
  const s = /^\/([a-z]*)$/.exec(before);
  if (s) return { sigil: '/', query: s[1]!, start: 0, end: caret };
  return undefined;
}

const HANDLE = /^[a-z0-9_][a-z0-9_.-]*[a-z0-9_]$|^[a-z0-9_]$/;

/** The text after "@" that the room resolves to this member: the name as a handle when that is unambiguous, else the id. */
export function mentionHandle(name: string, id: string, allNames: readonly string[]): string {
  const h = name.trim().toLowerCase().split(/\s+/).join('-');
  const clash = allNames.filter((n) => n.trim().toLowerCase().split(/\s+/).join('-') === h).length > 1;
  return HANDLE.test(h) && !clash ? h : id;
}

export interface MentionTarget {
  kind: 'human' | 'agent' | 'tool' | 'system';
  id: string;
  name: string;
  role?: string;
}

export interface SlashCommand {
  name: string;
  args: string;
  help: string;
}

/** The commands the composer understands. Each maps to an existing action; none bypasses an approval. */
export const SLASH_COMMANDS: SlashCommand[] = [
  { name: 'task', args: '<title>', help: 'Create a task in this project and open it' },
  { name: 'decision', args: '<text>', help: 'Post a [DECISION] (an ADR candidate)' },
  { name: 'todo', args: '<text>', help: 'Post a [TODO]' },
  { name: 'status', args: '<text>', help: 'Post a [STATUS]' },
  { name: 'result', args: '<text>', help: 'Post a [RESULT]' },
  { name: 'invite', args: '@agent', help: 'Add an agent of this project to the room' },
  { name: 'mode', args: 'auto | sequential', help: 'Change how agents take turns in this room' },
  { name: 'help', args: '', help: 'List these commands' },
];

/** Suggestions for a token, best first, at most limit. */
export function suggest(tok: Token, ctx: { members: MentionTarget[]; rooms: { name: string; kind: string }[] }, limit = 8): Suggestion[] {
  const q = tok.query.toLowerCase();
  const names = ctx.members.map((m) => m.name);
  const rank = (s: string) => (s.toLowerCase().startsWith(q) ? 0 : s.toLowerCase().includes(q) ? 1 : 2);
  let out: (Suggestion & { r: number })[] = [];
  switch (tok.sigil) {
    case '@':
      out = ctx.members
        .filter((m) => m.kind !== 'system')
        .map((m) => {
          const h = mentionHandle(m.name, m.id, names);
          return { label: m.name, detail: m.role ? `${m.kind}, ${m.role}` : m.kind, insert: `@${h} `, r: Math.min(rank(m.name), rank(h)) };
        });
      // role mentions are offered too, once a role is being typed
      for (const role of [...new Set(ctx.members.map((m) => m.role).filter((r): r is string => !!r))]) {
        out.push({ label: `role: ${role}`, detail: 'everyone in the room with this role', insert: `@role:${role} `, r: rank(role) + 0.5 });
      }
      break;
    case '@role:':
      for (const role of [...new Set(ctx.members.map((m) => m.role).filter((r): r is string => !!r))]) {
        out.push({ label: `role: ${role}`, detail: 'everyone in the room with this role', insert: `@role:${role} `, r: rank(role) });
      }
      break;
    case '#':
      out = ctx.rooms.map((r) => ({ label: `#${r.name}`, detail: r.kind === 'task' ? 'task channel' : 'channel', insert: `#${r.name} `, r: rank(r.name) }));
      break;
    case '/':
      out = SLASH_COMMANDS.map((c) => ({ label: `/${c.name}${c.args ? ' ' + c.args : ''}`, detail: c.help, insert: `/${c.name} `, r: rank(c.name) }));
      break;
  }
  return out
    .filter((s) => s.r < 2 || q === '')
    .sort((a, b) => a.r - b.r || a.label.localeCompare(b.label))
    .slice(0, limit)
    .map(({ r: _r, ...s }) => s);
}

/** Replace the token with the suggestion; returns the new text and where the caret goes. */
export function applySuggestion(text: string, tok: Token, s: Suggestion): { text: string; caret: number } {
  const next = text.slice(0, tok.start) + s.insert + text.slice(tok.end).replace(/^ /, '');
  return { text: next, caret: tok.start + s.insert.length };
}

export type Parsed =
  | { kind: 'post'; body: string }
  | { kind: 'task'; title: string }
  | { kind: 'invite'; handle: string }
  | { kind: 'mode'; mode: string }
  | { kind: 'help' }
  | { kind: 'error'; message: string };

/** What a message means when sent: a plain post, or a slash command. */
export function parseSlash(input: string): Parsed {
  const text = input.trim();
  if (!text.startsWith('/')) return { kind: 'post', body: text };
  const m = /^\/([a-z]+)\s*([\s\S]*)$/.exec(text);
  if (!m) return { kind: 'error', message: 'Type a command name after the slash, or /help.' };
  const [, name, rest] = m as unknown as [string, string, string];
  const arg = rest.trim();
  switch (name) {
    case 'task':
      return arg ? { kind: 'task', title: arg } : { kind: 'error', message: '/task needs a title.' };
    case 'decision':
    case 'todo':
    case 'status':
    case 'result':
      return arg ? { kind: 'post', body: `[${name.toUpperCase()}] ${arg}` } : { kind: 'error', message: `/${name} needs some text.` };
    case 'invite': {
      const h = /^@?(\S+)$/.exec(arg);
      return h ? { kind: 'invite', handle: h[1]! } : { kind: 'error', message: '/invite needs one @agent.' };
    }
    case 'mode':
      return arg === 'auto' || arg === 'sequential' ? { kind: 'mode', mode: arg } : { kind: 'error', message: '/mode takes auto or sequential (other modes are set in the room panel).' };
    case 'help':
      return { kind: 'help' };
    default:
      return { kind: 'error', message: `/${name} is not a command. Type /help for the list.` };
  }
}
