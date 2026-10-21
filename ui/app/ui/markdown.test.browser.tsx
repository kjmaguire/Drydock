import { expect } from 'remix/assert';
import { createRoot } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, renderPlain } from '../test/render.tsx';
import { lex, Markdown, memoLex, safeHref } from './markdown.tsx';

afterEach(cleanup);

function html(src: string): { container: HTMLElement; html: string } {
  const { container } = renderPlain(<Markdown source={src} />);
  return { container, html: container.innerHTML };
}

/** Nothing executable or loadable may exist in the rendered tree. */
function assertInert(c: HTMLElement) {
  expect(c.querySelector('script, iframe, object, embed, style, link, meta, base, form, svg, math, img, video, audio, source, input')).toBeNull();
  for (const el of Array.from(c.querySelectorAll('*'))) {
    for (const a of Array.from(el.attributes)) {
      expect(a.name.startsWith('on')).toBe(false);
      if (['href', 'src', 'srcset', 'action', 'formaction', 'data', 'poster'].includes(a.name)) {
        expect(/^https?:|^mailto:/i.test(a.value)).toBe(true);
        expect(el.tagName).toBe('A');
      }
    }
  }
}

describe('Markdown sanitization', () => {
  it('shows a <script> tag as text and creates no element', () => {
    const r = html('hello <script>window.__xss = 1</script> world');
    assertInert(r.container);
    expect(r.container.textContent).toContain('<script>window.__xss = 1</script>');
    expect((window as unknown as Record<string, unknown>).__xss).toBeUndefined();
  });

  it('shows block-level raw HTML as text', () => {
    const r = html('<div onclick="window.__xss=1">click</div>\n\n<iframe src="https://evil.example/"></iframe>');
    assertInert(r.container);
    expect(r.container.textContent).toContain('<div onclick="window.__xss=1">');
    expect(r.container.textContent).toContain('<iframe src="https://evil.example/">');
  });

  it('shows an <img onerror> as inert text', () => {
    const r = html('look: <img src=x onerror="window.__xss=1">');
    assertInert(r.container);
    expect(r.container.textContent).toContain('<img src=x onerror="window.__xss=1">');
  });

  it('never creates an anchor for javascript:, data:, vbscript: or relative targets, and shows the raw target', () => {
    for (const target of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', ' javascript:alert(1)', 'data:text/html;base64,PHNjcmlwdD4=', 'vbscript:msgbox(1)', '//evil.example/x', '/relative', 'java\tscript:alert(1)']) {
      const r = html(`[click me](<${target}>)`);
      assertInert(r.container);
      expect(r.container.querySelector("a")).toBeNull();
      expect(r.container.textContent).toContain('click me');
      expect(r.container.textContent).toContain('blocked link');
    }
  });

  it('does not make javascript: autolinks or raw anchors clickable', () => {
    const r = html('<a href="javascript:alert(1)">x</a>\n\n<javascript:alert(1)>\n\nhttp://ok.example/path');
    assertInert(r.container);
    const anchors = Array.from(r.container.querySelectorAll('a'));
    expect(anchors.map((a) => a.getAttribute('href'))).toEqual(['http://ok.example/path']);
  });

  it('renders remote images as a text placeholder and creates no <img>', () => {
    const r = html('![tracking pixel](https://evil.example/p.png?u=secret)\n\n![](data:image/png;base64,AAAA)');
    assertInert(r.container);
    expect(r.container.querySelector('img')).toBeNull();
    expect(r.container.textContent).toContain('image not loaded');
    expect(r.container.textContent).toContain('tracking pixel');
    expect(r.container.textContent).toContain('https://evil.example/p.png?u=secret');
  });

  it('shows http(s) link targets next to the label, opens in a new context without referrer', () => {
    const r = html('[docs](https://example.com/a/b?c=d)');
    const a = r.container.querySelector('a')!;
    expect(a.getAttribute('href')).toBe('https://example.com/a/b?c=d');
    expect(a.getAttribute('rel')).toContain('noopener');
    expect(a.getAttribute('rel')).toContain('noreferrer');
    expect(a.getAttribute('target')).toBe('_blank');
    expect(r.container.textContent).toContain('(https://example.com/a/b?c=d)');
  });

  it('a link whose label looks like a different URL still shows the real target', () => {
    const r = html('[https://bank.example/login](https://evil.example/phish)');
    expect(r.container.textContent).toContain('(https://evil.example/phish)');
  });

  it('renders normal Markdown: emphasis, code, lists, tables, quotes', () => {
    const r = html('# Title\n\n**bold** and *em* and `code`\n\n- a\n- b\n\n1. one\n\n> quote\n\n| h1 | h2 |\n|---|---|\n| c1 | c2 |\n\n```\nlet x = "<b>"\n```');
    assertInert(r.container);
    expect(r.container.querySelector('strong')?.textContent).toBe('bold');
    expect(r.container.querySelectorAll('li')).toHaveLength(3);
    expect(r.container.querySelector('table th')?.textContent).toBe('h1');
    expect(r.container.querySelector('pre code')?.textContent).toContain('let x = "<b>"');
    expect(r.container.querySelector('blockquote')).not.toBeNull();
    expect(r.container.querySelector('h3')?.textContent).toBe('Title');
  });

  it('puts a table in a named group the keyboard can reach and scroll', () => {
    const r = html('| h1 | h2 |\n|---|---|\n| c1 | c2 |');
    const wrap = r.container.querySelector<HTMLElement>('.md-table-wrap')!;
    expect(wrap.getAttribute('role')).toBe('group');
    expect(wrap.getAttribute('aria-label')).toBe('Table: h1, h2');
    expect(wrap.tabIndex).toBe(0);
  });

  it('lexes a message once however often it renders (F6)', async () => {
    const tokensOf = memoLex();
    const a = tokensOf('**one** two');
    expect(tokensOf('**one** two')).toBe(a); // the same tokens: no second lex
    const b = tokensOf('three');
    expect(b).not.toBe(a);
    expect(b).toEqual(lex('three'));
    // the component keeps its tokens across re-renders with the same source
    const r = renderPlain(<Markdown source="**kept**" />);
    const strong = r.container.querySelector('strong');
    r.root.render(<Markdown source="**kept**" />);
    r.root.flush();
    expect(r.container.querySelector('strong')).toBe(strong);
  });

  it('treats text that looks like an approval or a command as plain text with no controls', () => {
    const r = html('APPROVED by human:kyle. [APPROVE](#approve) run `rm -rf /`');
    expect(r.container.querySelector('button, input, form')).toBeNull();
    expect(r.container.querySelector('a')).toBeNull();
  });

  it('truncates very long bodies', () => {
    const { container } = renderPlain(<Markdown source={'x'.repeat(50)} maxChars={10} />);
    expect(container.textContent).toContain('40 more characters not shown');
  });

  it('survives hostile and malformed input', () => {
    for (const s of ['[', ']()', '![', '<', '<<<<<<', '[a](', '\u0000', '`'.repeat(100), '['.repeat(500) + ']'.repeat(500)]) {
      const r = html(s);
      assertInert(r.container);
    }
  });
});

describe('safeHref', () => {
  it('allows only http, https and mailto', () => {
    expect(safeHref('https://a.example/x')).toBe('https://a.example/x');
    expect(safeHref('http://a.example')).toBe('http://a.example/');
    expect(safeHref('mailto:a@b.example')).toBe('mailto:a@b.example');
    for (const bad of ['', 'javascript:1', 'file:///etc/passwd', 'ftp://x', 'blob:https://x/y', 'about:blank', '#frag', 'a.example', 'https://a\u0000.example']) {
      expect(safeHref(bad)).toBeUndefined();
    }
  });
});

// --- H11: crafted text can neither freeze the window nor leave it blank ---

const rep = (unit: string, n: number) => unit.repeat(Math.ceil(n / unit.length)).slice(0, n);

/** Mounts straight into a root of its own so that an error thrown while drawing is seen (the runtime reports it on the root). */
function mountChecked(node: JSX.Element): { container: HTMLElement; errors: unknown[]; ms: number; flush: () => void; cleanup: () => void } {
  const container = document.createElement('div');
  document.body.append(container);
  const root = createRoot(container);
  const errors: unknown[] = [];
  root.addEventListener('error', (e) => errors.push(e.error));
  const t0 = performance.now();
  root.render(node);
  root.flush();
  const ms = performance.now() - t0;
  const out = {
    container,
    errors,
    ms,
    flush: () => root.flush(),
    cleanup: () => {
      root.dispose();
      container.remove();
    },
  };
  mountedChecked.push(out);
  return out;
}
const mountedChecked: { cleanup: () => void }[] = [];
afterEach(() => {
  for (const m of mountedChecked.splice(0)) m.cleanup();
});

/** The inputs that took marked 18.1.1 seconds, or threw RangeError and left the message blank, at 20,000 characters (audit H11). */
const HOSTILE: Record<string, string> = {
  'unclosed em "*a *"': rep('*a *', 20000),
  'unclosed em "_a _"': rep('_a _', 20000),
  'unclosed strong "**a **"': rep('**a **', 20000),
  'nested em "*a **b *c "': rep('*a **b *c ', 20000),
  'underscores "a_b_c_"': rep('a_b_c_', 20000),
  'star words "a*b*c*"': rep('a*b*c*', 20000),
  'blockquote ">"': rep('>', 20000),
  'blockquote "> "': rep('> ', 20000),
  'ordered list "1. "': rep('1. ', 20000),
  'nested lists "- - - a"': `${rep('- ', 8000)}a`,
  'quote and list "> - "': `${rep('> - ', 8000)}x`,
  'task list': rep('- [ ] a\n', 20000),
  'list lines': rep('- a\n', 20000),
  'links "[a](b)"': rep('[a](b)', 20000),
  'images "![a](b)"': rep('![a](b)', 20000),
  'paragraphs "a"': rep('a\n\n', 20000),
  'headings "# a"': rep('# a\n', 20000),
  'table rows': `|a|b|\n|-|-|\n${rep('|c|d|\n', 20000)}`,
  'inline html "<b>x</b>"': rep('<b>x</b>', 20000),
  'code spans': rep('`a` ', 20000),
  'brackets "["': rep('[', 20000),
  'one huge word': rep('a', 20000),
};
/** Drawing a hostile message takes a few milliseconds to a few tens; this is far above that and far below the seconds it took. */
const MS_LIMIT = 500;

describe('Markdown limits (H11)', () => {
  it('draws every hostile 20,000-character message at once, with its text, and without an error', () => {
    const slow: string[] = [];
    for (const [name, src] of Object.entries(HOSTILE)) {
      const m = mountChecked(<Markdown source={src} />);
      expect(m.errors).toEqual([]);
      const md = m.container.querySelector('.md');
      // not blank: the text is there (formatted or as written), and the page it draws is small
      expect(md?.textContent?.length ?? 0).toBeGreaterThan(1000);
      expect(m.container.querySelectorAll('*').length).toBeLessThan(4000);
      if (m.ms > MS_LIMIT) slow.push(`${name}: ${Math.round(m.ms)} ms`);
      m.cleanup();
    }
    expect(slow).toEqual([]);
  });

  it('shows a block that opens more than 50 blockquotes or list items as the text that was written, in a plain block', () => {
    for (const src of [rep('>', 20000), rep('1. ', 6666), `${rep('- ', 6000)}a`]) {
      const m = mountChecked(<Markdown source={src} />);
      expect(m.errors).toEqual([]);
      const plain = m.container.querySelectorAll('pre.md-plain');
      expect(plain).toHaveLength(1);
      expect(plain[0]!.textContent).toBe(src);
      expect(m.container.querySelector('blockquote, ul, ol')).toBeNull();
    }
  });

  it('keeps the Markdown around a hostile block', () => {
    const m = mountChecked(<Markdown source={`# Title\n\n**bold** text\n\n${rep('> ', 200)}x\n\n- one\n- two\n`} />);
    expect(m.container.querySelector('h3')?.textContent).toBe('Title');
    expect(m.container.querySelector('strong')?.textContent).toBe('bold');
    expect(m.container.querySelectorAll('li')).toHaveLength(2);
    expect(m.container.querySelectorAll('pre.md-plain')).toHaveLength(1);
    expect(m.container.querySelector('blockquote')).toBeNull();
  });

  it('formats the first 4,000 characters of a long paragraph and holds the rest behind "Show more", as plain text', () => {
    const para = `${rep('word ', 6000)}end`;
    const m = mountChecked(<Markdown source={`**bold** ${para}`} />);
    expect(m.container.querySelector('strong')?.textContent).toBe('bold'); // the part lexed as Markdown
    const more = m.container.querySelector<HTMLButtonElement>('button.md-more')!;
    expect(more.textContent).toMatch(/^Show \d+ more characters$/);
    const hidden = Number(more.textContent!.match(/\d+/)![0]);
    expect(hidden).toBeGreaterThan(2000);
    const shown = m.container.querySelector('p')!.textContent!.replace(more.textContent!, '');
    expect(shown.length + hidden).toBe(`bold ${para}`.length);
    more.click();
    m.flush();
    expect(m.container.querySelector('button.md-more')).toBeNull();
    expect(m.container.querySelector('p')!.textContent).toBe(`bold ${para}`); // all of it, once asked for
  });

  it('does not lex a run whose delimiters would cost seconds: it is the text that was written, its tail behind "Show more"', () => {
    const src = rep('*a *', 20000);
    const m = mountChecked(<Markdown source={src} />);
    expect(m.container.querySelector('em, strong')).toBeNull();
    expect(m.container.querySelectorAll('button.md-more')).toHaveLength(1);
    expect(m.container.querySelector('p')!.textContent!.startsWith('*a **a *')).toBe(true);
    expect(m.ms).toBeLessThan(MS_LIMIT);
  });

  it('spends the budget in order: the first costly runs are lexed, the later ones are text, none is dropped', () => {
    const para = `**bold** ${rep('*a *', 980)}`;
    const src = Array.from({ length: 10 }, () => para).join('\n\n');
    const m = mountChecked(<Markdown source={src} />);
    expect(m.container.querySelectorAll('strong')).toHaveLength(2);
    const ps = Array.from(m.container.querySelectorAll('p'));
    expect(ps).toHaveLength(10);
    expect(ps[9]!.textContent).toBe(para);
    expect(m.ms).toBeLessThan(MS_LIMIT);
  });

  it('draws at most about 3,000 elements: the blocks after that are one plain block with all of their text', () => {
    const src = rep('a\n\n', 20000);
    const m = mountChecked(<Markdown source={src} />);
    const drawn = m.container.querySelectorAll('p').length;
    expect(drawn).toBeGreaterThan(1000);
    expect(drawn).toBeLessThan(2000);
    const tail = m.container.querySelector('pre.md-plain')!;
    expect(tail.textContent!.replace(/\s/g, '').length + drawn).toBe(src.replace(/\s/g, '').length);
    expect(m.container.querySelectorAll('*').length).toBeLessThan(4000);
  });

  it('shows a body that is not text as what it is, and never throws: a malformed message must not blank the page', () => {
    for (const [given, shown] of [
      [null, ''],
      [undefined, ''],
      [42, '42'],
      [['x', 'y'], 'x,y'],
    ] as const) {
      const m = mountChecked(<Markdown source={given as never} />);
      expect(m.errors).toEqual([]);
      expect([given, m.container.querySelector('.md')!.textContent]).toEqual([given, shown]);
    }
  });

  it('says how many characters are behind "Show more", in the singular for one', () => {
    const m = mountChecked(<Markdown source={rep('a', 4001)} />); // no space to cut at: the cut is at 4,000, one character is left
    expect(m.container.querySelector('button.md-more')!.textContent).toBe('Show 1 more character');
  });

  it('shows the message as written, with the error, when lexing or drawing throws (TEST DOUBLE lexers)', () => {
    const src = '# Title\n\nSome **text**';
    const lexThrows = mountChecked(
      <Markdown
        source={src}
        lexer={() => {
          throw new RangeError('Maximum call stack size exceeded');
        }}
      />,
    );
    expect(lexThrows.errors).toEqual([]);
    expect(lexThrows.container.querySelector('[data-testid="md-fallback"]')!.textContent).toBe(src);
    expect(lexThrows.container.textContent).toContain('RangeError: Maximum call stack size exceeded');
    // a token the drawing code can not take (a table without rows)
    const drawThrows = mountChecked(<Markdown source={src} lexer={() => [{ type: 'table', raw: src } as never]} />);
    expect(drawThrows.errors).toEqual([]);
    expect(drawThrows.container.querySelector('[data-testid="md-fallback"]')!.textContent).toBe(src);
    expect(drawThrows.container.querySelector('[role="note"]')!.textContent).toContain('TypeError');
  });

  it('leaves links and images exactly as they were: a run that is lexed still goes through safeHref', () => {
    const m = mountChecked(<Markdown source={`[ok](https://example.com/a) [bad](javascript:alert(1)) ![x](https://evil.example/p.png)\n\n${rep('> ', 200)}[ok](https://example.com/b)`} />);
    const anchors = Array.from(m.container.querySelectorAll('a')).map((a) => a.getAttribute('href'));
    expect(anchors).toEqual(['https://example.com/a']); // the hostile block is text: its link is not an anchor
    expect(m.container.textContent).toContain('blocked link');
    expect(m.container.querySelector('img')).toBeNull();
  });
});

describe('code blocks (P89)', () => {
  it('names the language, colours what it knows as text, and copies exactly the code', async () => {
    const code = 'const a = "<img src=x onerror=alert(1)>"; // note\nreturn 42';
    const r = html('```ts\n' + code + '\n```\n\n```\nplain <b>text</b>\n```');
    assertInert(r.container);
    const blocks = Array.from(r.container.querySelectorAll<HTMLElement>('[data-testid="code-block"]'));
    expect(blocks.map((b) => [b.dataset.lang, b.querySelector('.md-code-lang')!.textContent])).toEqual([
      ['js', 'ts'],
      [undefined, 'code'],
    ]);
    expect(blocks[0]!.querySelector('code')!.textContent).toBe(code);
    expect(Array.from(blocks[0]!.querySelectorAll('.tok-keyword')).map((e) => e.textContent)).toEqual(['const', 'return']);
    expect(blocks[0]!.querySelector('.tok-string')!.textContent).toBe('"<img src=x onerror=alert(1)>"');
    expect(blocks[0]!.querySelector('.tok-comment')!.textContent).toBe('// note');
    expect(blocks[1]!.querySelector('code')!.textContent).toBe('plain <b>text</b>');
    expect(blocks[1]!.querySelector('b')).toBeNull();

    const written: string[] = [];
    const orig = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (t: string) => void written.push(t) } });
    try {
      const btn = blocks[0]!.querySelector<HTMLButtonElement>('[data-testid="code-copy"]')!;
      expect(btn.textContent).toBe('Copy');
      btn.click();
      await new Promise((res) => setTimeout(res, 20));
      expect(written).toEqual([code]);
      expect(btn.textContent).toBe('Copied');
      expect(btn.getAttribute('aria-label')).toBe('Copied');
    } finally {
      if (orig) Object.defineProperty(navigator, 'clipboard', orig);
    }
  });
});

