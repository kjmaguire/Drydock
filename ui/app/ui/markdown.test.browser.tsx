import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, renderPlain } from '../test/render.tsx';
import { Markdown, safeHref } from './markdown.tsx';

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
