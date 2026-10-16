import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { marked, type Token, type Tokens } from 'marked';
import { DEFAULT_CHARS, lexSafe, limitsFor, markerDepth, runCost, segments, weight } from './markdown-lex.ts';

// The limits that keep crafted Markdown from freezing or blanking the window (audit 2026-10-16, H11). The corpus is the
// inputs that were measured to hang (7 s for 20,000 characters) or to throw RangeError with marked 18.1.1.

const rep = (s: string, n: number) => s.repeat(Math.ceil(n / s.length)).slice(0, n);
const L = limitsFor(DEFAULT_CHARS);

/** The text of a lexed message in reading order, with `more` tokens counted as the text they hide. */
function textOf(tokens: readonly Token[]): string {
  let out = '';
  const walk = (tk: Token) => {
    const t = tk as Tokens.Generic;
    if (t.type === 'plain' || t.type === 'more') out += t.text as string;
    else if (t.type === 'text' && !t.tokens) out += t.text as string;
    else if (t.type === 'code' || t.type === 'codespan') out += t.text as string;
    else if (t.type === 'list') for (const it of (t as Tokens.List).items) it.tokens.forEach(walk);
    else if (t.tokens) t.tokens.forEach(walk);
  };
  tokens.forEach(walk);
  return out;
}

/** The inline runs of paragraphs and headings, flattened. */
function runsOf(tokens: readonly Token[]): Token[][] {
  const out: Token[][] = [];
  for (const tk of tokens) {
    const t = tk as Tokens.Generic;
    if ((t.type === 'paragraph' || t.type === 'heading') && t.tokens) out.push(t.tokens);
  }
  return out;
}

describe('markerDepth', () => {
  it('counts the blockquote and list markers that open a line', () => {
    expect(markerDepth('plain text', 50)).toBe(0);
    expect(markerDepth('> quote', 50)).toBe(1);
    expect(markerDepth('> > > deep', 50)).toBe(3);
    expect(markerDepth('>>>>', 50)).toBe(4);
    expect(markerDepth('- item', 50)).toBe(1);
    expect(markerDepth('  * nested', 50)).toBe(1);
    expect(markerDepth('1. one', 50)).toBe(1);
    expect(markerDepth('12) twelve', 50)).toBe(1);
    expect(markerDepth('> - 1. mixed', 50)).toBe(3);
    expect(markerDepth('- - - a', 50)).toBe(3);
  });

  it('does not take ordinary text for a marker', () => {
    for (const line of ['2 > 1', 'a - b', '1.5 apples', '-5 degrees', '*bold* text', '**bold**', '1.', '-', '>=', '123456789012. x', 'a > b > c']) {
      expect(markerDepth(line, 50)).toBeLessThanOrEqual(line.startsWith('>') ? 1 : 0);
    }
    expect(markerDepth('*bold* text', 50)).toBe(0);
    expect(markerDepth('1.5 apples', 50)).toBe(0);
  });

  it('stops counting just past the limit, so a line of 20,000 markers costs next to nothing', () => {
    expect(markerDepth('>'.repeat(20000), 50)).toBe(51);
    expect(markerDepth('1. '.repeat(6000), 50)).toBe(51);
    expect(markerDepth('- '.repeat(6000), 50)).toBe(51);
  });
});

describe('runCost', () => {
  it('is the length for text without delimiters and grows with every * and _', () => {
    expect(runCost('')).toBe(0);
    expect(runCost('plain words')).toBe(11);
    expect(runCost('a *b* c')).toBe(7 * 3);
    expect(runCost('*a *'.repeat(250))).toBe(1000 * 501);
  });
});

describe('segments', () => {
  it('keeps ordinary text as one Markdown segment, however many blank lines it has', () => {
    const src = '# Title\n\nSome **text**.\n\n- a\n\n- b\n\n> quote\n\n```\ncode\n\nmore code\n```\n';
    expect(segments(src, 50, 1000)).toEqual([{ plain: false, text: src }]);
    expect(segments('', 50, 1000)).toEqual([]);
  });

  it('cuts out the block with a line that opens too many markers, and joins the source back', () => {
    const hostile = `${'> '.repeat(60)}x`;
    const src = `first\n\nsecond\n${hostile}\nstill the same block\n\nlast`;
    const segs = segments(src, 50, 1000);
    expect(segs.map((s) => s.plain)).toEqual([false, true, false]);
    expect(segs[1]!.text).toBe(`second\n${hostile}\nstill the same block\n\n`);
    expect(segs.map((s) => s.text).join('')).toBe(src);
  });

  it('cuts out a block with more lines than allowed, but never a code fence however long', () => {
    const long = rep('- [ ] a\n', 8 * 1500);
    const segs = segments(`intro\n\n${long}\ntail`, 50, 1000);
    expect(segs.map((s) => s.plain)).toEqual([false, true, false]);
    expect(segs.map((s) => s.text).join('')).toBe(`intro\n\n${long}\ntail`);
    const fenced = `\`\`\`\n${rep('line\n', 5000)}\`\`\`\n`;
    expect(segments(fenced, 50, 1000)).toEqual([{ plain: false, text: fenced }]);
  });

  it('does not look for markers inside a code fence', () => {
    const src = `\`\`\`\n${'>'.repeat(80)}\n\`\`\`\n`;
    expect(segments(src, 50, 1000).map((s) => s.plain)).toEqual([false]);
    // and a fence that is never closed keeps the rest of the text inside it
    expect(segments(`\`\`\`\n${'>'.repeat(80)}\nmore`, 50, 1000).map((s) => s.plain)).toEqual([false]);
  });
});

describe('lexSafe', () => {
  it('lexes ordinary Markdown exactly as marked does', () => {
    const docs = [
      '# Title\n\nSome **bold** and *em* and `code` and [a link](https://example.com/x "t") and ![img](https://e.example/p.png).\n\n- a\n- b\n  - nested\n\n1. one\n2. two\n\n> quote\n> more\n\n| h1 | h2 |\n|---|---|\n| c1 | c2 |\n\n```go\nfunc f() {}\n```\n\n---\n\n- [ ] todo\n- [x] done\n\n<div>html</div>\n\n[ref]: https://example.com\n\nSee [ref] and <https://auto.example>.\n',
      'one\ntwo\n\nthree  \nfour\\\nfive',
      'Setext\n======\n\nText with &amp; entities and \\* escapes',
      'a\r\nb\r\n\r\nc',
    ];
    for (const doc of docs) {
      const mine = JSON.parse(JSON.stringify(lexSafe(doc, L)));
      const theirs = JSON.parse(JSON.stringify(marked.lexer(doc, { gfm: true, breaks: false, async: false })));
      expect(mine).toEqual(theirs);
    }
  });

  it('shows a block with too many markers as the text that was written (marked 18.1.1 overflows the stack on these)', () => {
    for (const hostile of ['>'.repeat(20000), '> '.repeat(10000), '1. '.repeat(6666), `${'- '.repeat(6000)}a`, `${'> - '.repeat(5000)}x`]) {
      const tokens = lexSafe(hostile, L);
      expect(tokens).toHaveLength(1);
      expect(tokens[0]!.type).toBe('plain');
      expect(textOf(tokens)).toBe(hostile);
    }
  });

  it('keeps the Markdown around a hostile block', () => {
    const src = `# Heading\n\n**bold**\n\n${'> '.repeat(80)}x\n\n- item\n`;
    const types = lexSafe(src, L).map((t) => t.type);
    expect(types).toEqual(['heading', 'space', 'paragraph', 'space', 'plain', 'list']);
  });

  it('lexes a run only up to runChars: the rest is a `more` token with the text', () => {
    const para = `${rep('word ', 5000)}end`;
    const tokens = lexSafe(para, L);
    const run = runsOf(tokens)[0]!;
    expect(run.map((t) => t.type)).toEqual(['text', 'more']);
    expect((run[0] as Tokens.Text).text.length).toBeLessThanOrEqual(L.runChars);
    expect((run[0] as Tokens.Text).text + (run[1] as Tokens.Generic).text).toBe(para);
    // a run at the limit is not cut
    expect(runsOf(lexSafe(rep('w', L.runChars), L))[0]!.map((t) => t.type)).toEqual(['text']);
  });

  it('shows a run whose emphasis delimiters would cost too much as text (marked takes 7 s for 20,000 of them)', () => {
    for (const unit of ['*a *', '_a _', '**a **', '*a **b *c ']) {
      const src = rep(unit, 20000);
      const t0 = performance.now();
      const tokens = lexSafe(src, L);
      const ms = performance.now() - t0;
      expect(ms).toBeLessThan(400);
      const run = runsOf(tokens)[0]!;
      expect(run.map((t) => t.type)).toEqual(['text', 'more']);
      expect(textOf(tokens)).toBe(src);
    }
  });

  it('spends one budget on all the runs of a message: later runs are text', () => {
    // each run starts with real bold, so a run that was lexed shows a strong token; the filler after it is as dear as a run may be
    const para = `**bold** ${rep('*a *', 980)}`;
    expect(runCost(para)).toBeLessThanOrEqual(L.runCost);
    const src = Array.from({ length: 10 }, () => para).join('\n\n');
    const tokens = lexSafe(src, L);
    const lexed = runsOf(tokens).map((run) => run.some((t) => t.type === 'strong'));
    expect(lexed).toEqual([true, true, false, false, false, false, false, false, false, false]); // 1,000,000 / about 490,000
    expect(textOf(tokens)).toBe(`bold ${rep('*a *', 980)}`.repeat(2) + `**bold** ${rep('*a *', 980)}`.repeat(8));
  });

  it('lexes a run of 4,000 characters or less however long the message, and shows the rest of a longer one as text', () => {
    const inside = runsOf(lexSafe(`${rep('some ordinary words here ', 3900)}**bold**`, L))[0]!;
    expect(inside.some((t) => t.type === 'strong')).toBe(true);
    const beyond = runsOf(lexSafe(`${rep('some ordinary words here ', 4500)}**bold**`, L))[0]!;
    expect(beyond.some((t) => t.type === 'strong')).toBe(false); // it sits after the first 4,000 characters, in the `more` token
    expect(beyond.map((t) => t.type)).toEqual(['text', 'more']);
  });

  it('lexes at most lexChars of a message; the rest is text', () => {
    const src = rep('line of **text**\n\n', 50000);
    const tokens = lexSafe(src, L);
    const last = tokens[tokens.length - 1]!;
    expect(last.type).toBe('plain');
    expect(src.endsWith(last.raw)).toBe(true);
    expect(last.raw.length).toBeGreaterThanOrEqual(src.length - L.lexChars);
    expect(last.raw.length).toBeLessThan(src.length - L.lexChars / 2);
    expect(tokens.slice(0, -1).some((t) => t.type === 'paragraph')).toBe(true);
    expect(JSON.stringify(tokens.slice(0, -1))).toContain('"strong"'); // the first part is Markdown
  });

  it('never throws, whatever the text', () => {
    let seed = 7;
    const rnd = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
    const alphabet = ['*', '_', '>', '- ', '1. ', '`', '[', ']', '(', ')', '<', '!', '~', '|', '\n', '\n\n', ' ', 'a', '#', '\t', '\\', '&amp;', '```\n'];
    for (let i = 0; i < 200; i++) {
      let s = '';
      const n = 1 + Math.floor(rnd() * 3000);
      for (let j = 0; j < n; j++) s += alphabet[Math.floor(rnd() * alphabet.length)];
      expect(() => lexSafe(s, L)).not.toThrow();
    }
    expect(() => lexSafe('\u0000\ud800￾', L)).not.toThrow();
  });
});

describe('limitsFor', () => {
  it('grows the budgets for a caller that shows more, up to twice the default, and nothing else', () => {
    const big = limitsFor(200000);
    expect(big.totalCost).toBe(2 * L.totalCost);
    expect(big.nodes).toBe(2 * L.nodes);
    expect(limitsFor(600).totalCost).toBe(L.totalCost);
    for (const key of ['runChars', 'runCost', 'depth', 'blockLines', 'lexChars'] as const) expect(big[key]).toBe(L[key]);
  });
});

describe('weight', () => {
  it('counts about the elements a token draws, and nothing for a space', () => {
    const doc = lexSafe('# T\n\nplain **b** [l](https://e.example) `c`\n\n- a\n- b\n\n| h |\n|---|\n| c |\n', L);
    expect(weight({ type: 'space', raw: '\n\n' })).toBe(0);
    const total = doc.reduce((n, tk) => n + weight(tk), 0);
    expect(total).toBeGreaterThan(20);
    expect(total).toBeLessThan(80);
    expect(weight(lexSafe('a', L)[0]!)).toBe(2);
  });
});
