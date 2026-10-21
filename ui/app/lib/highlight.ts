// Light syntax colouring for code blocks in messages (decision P89): comments, strings, numbers and keywords, for the
// languages agents write most. It is a scanner, not a parser, and it only ever splits the text into runs: the runs joined are
// exactly the code, and each is drawn as text (never HTML). Nothing is installed for it. Long code is left plain.

export type TokenKind = 'plain' | 'keyword' | 'string' | 'comment' | 'number';

export interface CodeToken {
  kind: TokenKind;
  text: string;
}

/** Code longer than this is drawn plain: colouring is a convenience, never a cost. */
export const HIGHLIGHT_MAX = 20_000;

interface Family {
  keywords: ReadonlySet<string>;
  /** Line comment starts. */
  line: readonly string[];
  /** Block comments as [open, close]. */
  block: readonly (readonly [string, string])[];
  /** String quotes; a backtick string may span lines. */
  quotes: readonly string[];
}

const words = (s: string) => new Set(s.split(/\s+/).filter(Boolean));

const C_LIKE = {
  line: ['//'],
  block: [['/*', '*/']] as const,
  quotes: ['"', "'", '`'],
};

const FAMILIES: Record<string, Family> = {
  js: {
    ...C_LIKE,
    keywords: words(
      'as async await break case catch class const continue debugger default delete do else enum export extends false finally for from function if implements import in instanceof interface let new null of private protected public readonly return static super switch this throw true try type typeof undefined var void while with yield',
    ),
  },
  go: {
    ...C_LIKE,
    keywords: words('break case chan const continue default defer else fallthrough false for func go goto if import interface iota map nil package range return select struct switch true type var'),
  },
  rust: {
    ...C_LIKE,
    quotes: ['"'],
    keywords: words('as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while'),
  },
  c: {
    ...C_LIKE,
    quotes: ['"', "'"],
    keywords: words(
      'abstract auto bool break case catch char class const continue default delete do double else enum extern false final float for foreach goto if implements import int interface long namespace new null nullptr package private protected public return short signed sizeof static string struct super switch this throw true try typedef union unsigned using var virtual void volatile while',
    ),
  },
  python: {
    line: ['#'],
    block: [],
    quotes: ['"', "'"],
    keywords: words('and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return self True try while with yield'),
  },
  shell: {
    line: ['#'],
    block: [],
    quotes: ['"', "'"],
    keywords: words('case do done elif else esac exit export fi for function if in local return set then until while'),
  },
  powershell: {
    line: ['#'],
    block: [['<#', '#>']],
    quotes: ['"', "'"],
    keywords: words('begin break catch class continue do else elseif end exit filter finally for foreach function if in param process return switch throw trap try until while'),
  },
  sql: {
    line: ['--'],
    block: [['/*', '*/']],
    quotes: ["'", '"'],
    keywords: words(
      'add all alter and as asc between by case create delete desc distinct drop else end exists from group having in index inner insert into is join key left like limit not null on or order outer primary references right select set table then union unique update values when where with',
    ),
  },
  json: { line: [], block: [], quotes: ['"'], keywords: words('true false null') },
  yaml: { line: ['#'], block: [], quotes: ['"', "'"], keywords: words('true false null yes no on off') },
};

const ALIASES: Record<string, string> = {
  javascript: 'js',
  jsx: 'js',
  ts: 'js',
  typescript: 'js',
  tsx: 'js',
  mjs: 'js',
  cjs: 'js',
  golang: 'go',
  rs: 'rust',
  'c++': 'c',
  cpp: 'c',
  cc: 'c',
  h: 'c',
  cs: 'c',
  csharp: 'c',
  java: 'c',
  kotlin: 'c',
  kt: 'c',
  swift: 'c',
  py: 'python',
  sh: 'shell',
  bash: 'shell',
  zsh: 'shell',
  console: 'shell',
  ps1: 'powershell',
  pwsh: 'powershell',
  yml: 'yaml',
  jsonc: 'json',
};

/** The family a fence's language names, or undefined when it is not one the colouring knows. */
export function familyOf(lang: string | undefined): string | undefined {
  const l = (lang ?? '').trim().toLowerCase().split(/\s+/)[0] ?? '';
  const key = ALIASES[l] ?? l;
  return FAMILIES[key] ? key : undefined;
}

const isWordStart = (c: string) => /[A-Za-z_$]/.test(c);
const isWord = (c: string) => /[A-Za-z0-9_$]/.test(c);
const isDigit = (c: string) => c >= '0' && c <= '9';

/** Splits code into coloured runs. Unknown languages and long code come back as one plain run. */
export function highlight(code: string, lang: string | undefined): CodeToken[] {
  const fam = familyOf(lang);
  if (!fam || code.length > HIGHLIGHT_MAX) return code ? [{ kind: 'plain', text: code }] : [];
  const f = FAMILIES[fam]!;
  const out: CodeToken[] = [];
  const push = (kind: TokenKind, text: string) => {
    if (!text) return;
    const last = out[out.length - 1];
    if (last && last.kind === kind) last.text += text;
    else out.push({ kind, text });
  };
  let i = 0;
  const n = code.length;
  outer: while (i < n) {
    for (const [open, close] of f.block) {
      if (code.startsWith(open, i)) {
        const end = code.indexOf(close, i + open.length);
        const stop = end < 0 ? n : end + close.length;
        push('comment', code.slice(i, stop));
        i = stop;
        continue outer;
      }
    }
    for (const lc of f.line) {
      // "#" starts a comment only at a word boundary (so "a#b" or "$#" stay code)
      if (code.startsWith(lc, i) && (lc !== '#' || i === 0 || /\s/.test(code[i - 1]!))) {
        const end = code.indexOf('\n', i);
        const stop = end < 0 ? n : end;
        push('comment', code.slice(i, stop));
        i = stop;
        continue outer;
      }
    }
    const c = code[i]!;
    if (f.quotes.includes(c)) {
      let j = i + 1;
      let closed = false;
      while (j < n) {
        if (code[j] === c) {
          closed = true;
          break;
        }
        if (code[j] === '\\') j++;
        else if (code[j] === '\n' && c !== '`') break; // an unclosed quote ends with its line, the newline left out
        j++;
      }
      const stop = closed ? j + 1 : Math.min(n, j);
      push('string', code.slice(i, stop));
      i = stop;
      continue;
    }
    if (isDigit(c) && (i === 0 || !isWord(code[i - 1]!))) {
      let j = i + 1;
      while (j < n && /[0-9a-fA-FxXoObB_.]/.test(code[j]!)) j++;
      push('number', code.slice(i, j));
      i = j;
      continue;
    }
    if (isWordStart(c)) {
      let j = i + 1;
      while (j < n && isWord(code[j]!)) j++;
      const w = code.slice(i, j);
      push(f.keywords.has(fam === 'sql' ? w.toLowerCase() : w) ? 'keyword' : 'plain', w);
      i = j;
      continue;
    }
    push('plain', c);
    i++;
  }
  return out;
}
