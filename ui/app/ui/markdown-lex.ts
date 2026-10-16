// The lexing half of the Markdown renderer, with the limits that keep text from outside (an agent's message, a pull request,
// a skill) from freezing the window or leaving it blank (audit 2026-10-16, H11). marked 18 is quick on ordinary text, but
// measured in Chromium with marked 18.1.1:
//   - a run of emphasis delimiters nobody closes ("*a *" repeated) makes the inline pass quadratic: about 15 ms at 1,000
//     characters, 60 ms at 2,000, 7 s at the 20,000 characters a message may hold;
//   - a line that opens thousands of blockquotes or list items ("> > > ..." or "1. 1. 1. ...") overflows the call stack
//     (RangeError), and the page behind it renders nothing at all.
// marked is used as a lexer only (see markdown.tsx). This file runs its two passes itself, so each can be limited:
//   - the block pass sees the text with every block cut out that opens more than `depth` markers on a line or runs over
//     `blockLines` lines (its task lists cost quadratic time), to be shown as plain text; only the first `lexChars` characters
//     are lexed at all;
//   - the inline pass runs once per paragraph, heading or table cell ("run"). A run is lexed as Markdown only up to
//     `runChars` characters (the rest is a `more` token: plain text behind a button), and only when `runCost` and the
//     message's `totalCost` allow it; otherwise it is plain text.
// Nothing is dropped: every character of the message is still shown, as Markdown or as the text that was written.

import { Lexer, type Token, type Tokens } from 'marked';

/** The default size of one Markdown instance (props.maxChars): the limits below are for this much text. */
export const DEFAULT_CHARS = 20000;

export interface MarkdownLimits {
  /** Longest run lexed as Markdown. The rest of a longer paragraph, heading or cell is a `more` token: plain text behind "Show more". */
  runChars: number;
  /** Most one run may cost (runCost); a run that would cost more is plain text. */
  runCost: number;
  /** Most all the runs of one message may cost together; the runs after that are plain text. */
  totalCost: number;
  /** Most blockquote or list markers a line may open (">>>" or "- - - 1.") before its block is plain text. */
  depth: number;
  /** Most lines one block (the text between blank lines, a code fence excepted) may have before it is plain text. */
  blockLines: number;
  /** Most characters of a message that are lexed at all; what follows (a caller may allow more to be shown) is plain text. */
  lexChars: number;
  /** Most elements one message draws (see weight); the blocks after that are one plain text block. */
  nodes: number;
}

/**
 * The limits for a Markdown instance that shows up to `maxChars` characters. The depth, the size of one run and of one block
 * do not grow with it (one block can never hang or overflow the stack); the budgets for the whole message grow up to twice
 * the default, for the callers that ask to show 30,000 or 40,000 characters. Beyond 40,000 characters a message is text.
 */
export function limitsFor(maxChars: number): MarkdownLimits {
  const scale = Math.min(2, Math.max(1, maxChars / DEFAULT_CHARS));
  return { runChars: 4000, runCost: 500_000, totalCost: 1_000_000 * scale, depth: 50, blockLines: 1000, lexChars: 2 * DEFAULT_CHARS, nodes: 3000 * scale };
}

/**
 * What lexing one inline run costs, in "characters scanned": every character is read once, and every `*` or `_` can make the
 * lexer read the rest of the run again to look for the delimiter that closes it. That second part is the quadratic one:
 * the measured time is about 30-60 ns per unit for the worst runs and far less for ordinary text.
 */
export function runCost(src: string): number {
  let delimiters = 0;
  for (let i = 0; i < src.length; i++) {
    const c = src.charCodeAt(i);
    if (c === 42 || c === 95) delimiters++; // * and _
  }
  return src.length * (1 + delimiters);
}

const isSpace = (c: string | undefined) => c === ' ' || c === '\t';
const isDigit = (c: string | undefined) => c !== undefined && c >= '0' && c <= '9';

/** How many blockquote or list markers open this line: "> > - 1. text" is 4. Counting stops once it passes `limit`. */
export function markerDepth(line: string, limit: number): number {
  let depth = 0;
  let i = 0;
  const n = line.length;
  while (i < n && depth <= limit) {
    while (i < n && isSpace(line[i])) i++;
    const c = line[i];
    if (c === '>') {
      depth++;
      i++;
    } else if ((c === '-' || c === '*' || c === '+') && isSpace(line[i + 1])) {
      depth++;
      i += 2;
    } else if (isDigit(c)) {
      let j = i;
      while (j < n && j - i < 10 && isDigit(line[j])) j++;
      if ((line[j] === '.' || line[j] === ')') && isSpace(line[j + 1])) {
        depth++;
        i = j + 2;
      } else break;
    } else break;
  }
  return depth;
}

/** A stretch of the source: Markdown to lex, or text to show as written. */
export interface Segment {
  plain: boolean;
  text: string;
}

const FENCE = /^ {0,3}(`{3,}|~{3,})/;

/** Whether `line` closes a code fence opened with `fence` (the same character, at least as many, nothing else). */
function closesFence(line: string, fence: string): boolean {
  const body = line.trim();
  if (body.length < fence.length) return false;
  for (let i = 0; i < body.length; i++) if (body[i] !== fence[0]) return false;
  return !line.startsWith('    ');
}

/**
 * Cuts the source into stretches at blank lines (a code fence keeps its blank lines), and marks the ones with a line that
 * opens more than `depth` markers, or with more than `blockLines` lines outside code fences, as plain text. Neighbours of the
 * same kind are joined again, so ordinary text comes back as one Markdown segment, lexed exactly as a whole (loose lists and
 * link definitions keep working across blank lines). Joining the segments gives the source back.
 */
export function segments(source: string, depth: number, blockLines: number): Segment[] {
  const out: Segment[] = [];
  const push = (text: string, plain: boolean) => {
    if (!text) return;
    const last = out[out.length - 1];
    if (last && last.plain === plain) last.text += text;
    else out.push({ plain, text });
  };
  const lines = source.split('\n');
  let chunk = '';
  let hostile = false;
  let count = 0;
  let fence: string | undefined;
  for (let li = 0; li < lines.length; li++) {
    const line = lines[li]!;
    chunk += li < lines.length - 1 ? `${line}\n` : line;
    if (fence !== undefined) {
      if (closesFence(line, fence)) fence = undefined;
    } else {
      const open = FENCE.exec(line);
      if (open) fence = open[1];
      else if (markerDepth(line, depth) > depth || ++count > blockLines) hostile = true;
    }
    if (fence === undefined && line.trim() === '') {
      push(chunk, hostile);
      chunk = '';
      hostile = false;
      count = 0;
    }
  }
  push(chunk, hostile);
  return out;
}

/** A block shown as the text that was written (marked has no such token). */
export interface PlainToken extends Tokens.Generic {
  type: 'plain';
}
/** The rest of a run that was lexed only up to `runChars`: plain text behind a button. */
export interface MoreToken extends Tokens.Generic {
  type: 'more';
}

const plainToken = (text: string): PlainToken => ({ type: 'plain', raw: text, text: text.replace(/\n+$/, '') });
const textToken = (text: string): Tokens.Text => ({ type: 'text', raw: text, text });

interface Budget {
  cost: number;
}

/** Lexes one run: as Markdown when it is short and cheap enough, else as the text that was written. */
function lexRun(lexer: Lexer, run: { src: string; tokens: Token[] }, limits: MarkdownLimits, budget: Budget): void {
  const { src } = run;
  let head = src;
  let rest = '';
  if (src.length > limits.runChars) {
    let cut = src.lastIndexOf(' ', limits.runChars);
    if (cut < limits.runChars / 2) cut = limits.runChars; // no space near the limit: cut in the middle of a word
    head = src.slice(0, cut);
    rest = src.slice(cut);
  }
  const cost = runCost(head);
  let asText = cost > limits.runCost || cost > budget.cost;
  if (!asText) {
    budget.cost -= cost;
    try {
      lexer.inlineTokens(head, run.tokens);
    } catch {
      // whatever marked could not take (a call stack that ran out): this run is text, and the lexer starts clean
      lexer.state.inLink = false;
      lexer.state.inRawBlock = false;
      run.tokens.length = 0;
      asText = true;
    }
  }
  if (asText) run.tokens.push(textToken(head));
  if (rest) run.tokens.push({ type: 'more', raw: rest, text: rest } satisfies MoreToken);
}

/** Lexes Markdown that passed the depth check: marked's block pass, then the limited inline pass. */
function lexMarkdown(text: string, limits: MarkdownLimits, budget: Budget): Token[] {
  const lexer = new Lexer({ gfm: true, breaks: false, async: false });
  try {
    lexer.blockTokens(text.replace(/\r\n|\r/g, '\n'), lexer.tokens);
  } catch {
    return [plainToken(text)];
  }
  const queue = lexer.inlineQueue;
  lexer.inlineQueue = [];
  for (const run of queue) lexRun(lexer, run, limits, budget);
  return Array.from(lexer.tokens);
}

/** The tokens of a message: Markdown as marked lexes it, within the limits; everything else plain (`plain`, `more` and `text` tokens). Never throws. */
export function lexSafe(source: string, limits: MarkdownLimits = limitsFor(DEFAULT_CHARS)): Token[] {
  const out: Token[] = [];
  const budget: Budget = { cost: limits.totalCost };
  let head = source;
  let tail = '';
  if (source.length > limits.lexChars) {
    const cut = source.lastIndexOf('\n', limits.lexChars);
    const at = cut > limits.lexChars / 2 ? cut + 1 : limits.lexChars; // between lines when there is a line end near the limit
    head = source.slice(0, at);
    tail = source.slice(at);
  }
  for (const seg of segments(head, limits.depth, limits.blockLines)) {
    if (seg.plain) out.push(plainToken(seg.text));
    else for (const tk of lexMarkdown(seg.text, limits, budget)) out.push(tk);
  }
  if (tail) out.push(plainToken(tail));
  return out;
}

const sum = (tokens: readonly Token[] | undefined): number => {
  let n = 0;
  if (tokens) for (const tk of tokens) n += weight(tk);
  return n;
};

/**
 * About how many elements and text nodes drawing this token creates. It is what the `nodes` budget counts: measured in
 * Chromium, drawing costs 5-25 microseconds per node, so 3,000 nodes are well under 100 ms.
 */
export function weight(tk: Token): number {
  switch (tk.type) {
    case 'space':
    case 'def':
      return 0;
    case 'text': {
      const t = tk as Tokens.Text;
      return t.tokens ? sum(t.tokens) : 1;
    }
    case 'escape':
    case 'br':
    case 'hr':
      return 1;
    case 'strong':
    case 'em':
    case 'del':
    case 'paragraph':
    case 'heading':
    case 'blockquote':
      return 1 + sum((tk as Tokens.Em).tokens);
    case 'codespan':
    case 'html':
    case 'plain':
    case 'more':
      return 2;
    case 'code':
      return 3;
    case 'image':
      return 6;
    case 'link':
      return 6 + sum((tk as Tokens.Link).tokens);
    case 'list': {
      let n = 1;
      for (const it of (tk as Tokens.List).items) n += 2 + sum(it.tokens);
      return n;
    }
    case 'table': {
      const tb = tk as Tokens.Table;
      let n = 8;
      for (const c of tb.header) n += 2 + sum(c.tokens);
      for (const r of tb.rows) {
        n += 1;
        for (const c of r) n += 2 + sum(c.tokens);
      }
      return n;
    }
    default:
      return 2;
  }
}
