import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, renderPlain } from '../test/render.tsx';
import { CLIP_CHARS, CLIP_LINES, ClippedText, clipText } from './clipped-text.tsx';

afterEach(cleanup);

const rep = (s: string, n: number) => s.repeat(Math.ceil(n / s.length)).slice(0, n);

describe('clipText', () => {
  it('leaves a short text alone', () => {
    expect(clipText('Searches the docs.')).toEqual({ head: 'Searches the docs.', clipped: false });
    expect(clipText('x'.repeat(CLIP_CHARS))).toEqual({ head: 'x'.repeat(CLIP_CHARS), clipped: false });
    expect(clipText('1\n2\n3\n4\n5\n6')).toEqual({ head: '1\n2\n3\n4\n5\n6', clipped: false });
  });

  it('cuts a long text at a space near the limit and says it is cut', () => {
    const { head, clipped } = clipText(rep('word ', 2000));
    expect(clipped).toBe(true);
    expect(head.length).toBeLessThanOrEqual(CLIP_CHARS + 1);
    expect(head.endsWith('word…')).toBe(true);
  });

  it('cuts in the middle of a word when there is no space to cut at, but never between the halves of a surrogate pair', () => {
    expect(clipText('x'.repeat(1000)).head).toBe(`${'x'.repeat(CLIP_CHARS)}…`);
    const emoji = '😀'.repeat(400); // two UTF-16 units each
    const { head } = clipText(emoji);
    expect(head.endsWith('😀…')).toBe(true);
    expect(head.length % 2).toBe(1); // whole emoji and one ellipsis
  });

  it('keeps a few lines of a text that is short but tall', () => {
    const { head, clipped } = clipText(Array.from({ length: 50 }, (_, i) => `line ${i}`).join('\n'));
    expect(clipped).toBe(true);
    expect(head.split('\n')).toHaveLength(CLIP_LINES);
    expect(head.endsWith('…')).toBe(true);
  });
});

describe('ClippedText (L48)', () => {
  it('shows a short text as it is, with nothing to open', () => {
    const r = renderPlain(<ClippedText text="Searches the docs." data-testid="d" />);
    const box = r.$('[data-testid="d"]')!;
    expect(box.textContent).toBe('Searches the docs.');
    expect(box.querySelector('button')).toBeNull();
    expect(box.hasAttribute('title')).toBe(false);
  });

  it('shows the start of a long text, the tooltip has more, and Show more opens all of it in a box that scrolls', () => {
    const long = `${rep('This tool does a great many things. ', 10000)}END`;
    const r = renderPlain(<ClippedText text={long} data-testid="d" />);
    const box = r.$('[data-testid="d"]')!;
    expect(box.textContent!.length).toBeLessThan(CLIP_CHARS + 30); // the start, the ellipsis and the button
    expect(box.textContent).toContain('This tool does a great many things.');
    expect(box.textContent).not.toContain('END');
    expect(box.getAttribute('title')!.length).toBe(2000); // a part of it, not 370 KB in an attribute
    expect(box.getAttribute('title')!.startsWith('This tool does')).toBe(true);
    const more = box.querySelector('button')!;
    expect(more.textContent).toBe('Show more');
    expect(more.getAttribute('aria-expanded')).toBe('false');
    more.click();
    r.root.flush();
    const full = box.querySelector<HTMLElement>('.clip-full')!;
    expect(full.textContent).toBe(long);
    expect(getComputedStyle(full).maxHeight).toBe('240px');
    expect(getComputedStyle(full).overflowY).toBe('auto');
    expect(box.querySelector('button')!.textContent).toBe('Show less');
    expect(box.querySelector('button')!.getAttribute('aria-expanded')).toBe('true');
    box.querySelector('button')!.click();
    r.root.flush();
    expect(box.querySelector('.clip-full')).toBeNull();
    expect(box.textContent!.length).toBeLessThan(CLIP_CHARS + 30);
  });

  it('writes out characters that are not drawn, which would change what the text says', () => {
    const r = renderPlain(<ClippedText text={'Reads files ‮.exe'} data-testid="d" />);
    expect(r.$('[data-testid="d"]')!.textContent).toBe('Reads files \\u{202e}.exe');
  });

  it('shows a description that is not text as what it is, instead of throwing', () => {
    for (const [given, shown] of [
      [null, ''],
      [42, '42'],
    ] as const) {
      const r = renderPlain(<ClippedText text={given as never} data-testid="d" />);
      expect(r.$('[data-testid="d"]')!.textContent).toBe(shown);
    }
  });

  it('keeps the style it is given', () => {
    const r = renderPlain(<ClippedText text="x" data-testid="d" class="mine" />);
    expect(r.$('[data-testid="d"]')!.classList.contains('mine')).toBe(true);
  });
});
