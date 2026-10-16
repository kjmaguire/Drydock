import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { previewOf } from './preview.ts';

describe('previewOf', () => {
  it('writes plain JSON data exactly as JSON.stringify does', () => {
    for (const v of [{ a: 1 }, { name: 'Pay', list: [1, 2, { x: null }], t: true, f: false, s: 'a "quoted" \n line  ' }, [], {}, [[]], 'text', 0, -1.5, 1e21, true, null, { a: { b: { c: [1, [2, [3]]] } } }, { 'key with "quotes"': 1 }, '\ud800 lone surrogate']) {
      expect(previewOf(v, 100000)).toEqual({ text: JSON.stringify(v), cut: false });
    }
  });

  it('leaves out what JSON can not hold, as JSON.stringify does', () => {
    const v = { a: undefined, b: () => 1, c: Symbol('s'), d: 1, e: [undefined, () => 1, 2], f: NaN, g: Infinity };
    expect(previewOf(v, 1000).text).toBe(JSON.stringify(v));
    expect(previewOf(undefined, 10)).toEqual({ text: '', cut: false });
    expect(previewOf(() => 1, 10).text).toBe('null');
    expect(previewOf(10n, 10).text).toBe('"10"');
  });

  it('stops at the limit and says it cut', () => {
    expect(previewOf({ a: 'x'.repeat(100) }, 20)).toEqual({ text: '{"a":"xxxxxxxxxxxxxx', cut: true });
    expect(previewOf('x'.repeat(500), 200).text.length).toBe(200);
    expect(previewOf([1, 2, 3, 4, 5, 6, 7, 8, 9], 9)).toEqual({ text: '[1,2,3,4,', cut: true });
    // exactly the limit is not cut
    expect(previewOf({ a: 1 }, 7)).toEqual({ text: '{"a":1}', cut: false });
    expect(previewOf({ a: 1 }, 6)).toEqual({ text: '{"a":1', cut: true });
  });

  it('reads only as much of a huge payload as it writes', () => {
    const t0 = performance.now();
    const hugeString = previewOf({ diff: 'x'.repeat(50_000_000) }, 2048);
    const hugeArray = previewOf(Array.from({ length: 2_000_000 }, (_, i) => i), 2048);
    const manyKeys = previewOf(Object.fromEntries(Array.from({ length: 300_000 }, (_, i) => [`k${i}`, i])), 2048);
    const ms = performance.now() - t0;
    for (const p of [hugeString, hugeArray, manyKeys]) {
      expect(p.cut).toBe(true);
      expect(p.text.length).toBe(2048);
    }
    expect(ms).toBeLessThan(2000); // building the three payloads takes most of it
  });

  it('survives nesting, cycles and values that throw', () => {
    let deep: unknown = 'bottom';
    for (let i = 0; i < 100_000; i++) deep = { d: deep };
    expect(previewOf(deep, 2048).text.startsWith('{"d":{"d":')).toBe(true);
    const cyclic: Record<string, unknown> = { a: 1 };
    cyclic.self = cyclic;
    expect(previewOf(cyclic, 2048).text).toContain('"…"');
    const bad = {
      ok: 1,
      get boom(): number {
        throw new Error('getter');
      },
    };
    expect(previewOf(bad, 2048)).toEqual({ text: '{"ok":1', cut: true });
  });
});
