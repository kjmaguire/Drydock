import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { hasHidden, visible } from './visible.ts';

describe('visible', () => {
  it('writes out the characters that reorder or hide text', () => {
    // a right-to-left override makes "gpj.exe" read as "exe.jpg"
    expect(visible('photo‮gpj.exe')).toBe('photo\\u{202e}gpj.exe');
    // isolates, zero-width space and joiners, the byte order mark, the soft hyphen, a tag character
    expect(visible('a⁦b⁩c')).toBe('a\\u{2066}b\\u{2069}c');
    expect(visible('x​y‍z﻿')).toBe('x\\u{200b}y\\u{200d}z\\u{feff}');
    expect(visible('ig­nore')).toBe('ig\\u{ad}nore');
    expect(visible('t\u{E0041}g')).toBe('t\\u{e0041}g');
    // line and paragraph separators, escape and backspace, NUL, DEL
    expect(visible('a b c')).toBe('a\\u{2028}b\\u{2029}c');
    expect(visible('rm\u001B[2K\u0008\u0000x\u007F')).toBe('rm\\u{1b}[2K\\u{8}\\u{0}x\\u{7f}');
    // a lone surrogate
    expect(visible('a\uD800b')).toBe('a\\u{d800}b');
  });

  it('leaves ordinary text alone: line breaks, tabs, accents, emoji, text in right-to-left scripts', () => {
    const plain = 'line one\nline two\r\n\tindented: café, 日本語, שלום, مرحبا, 🙂, ZWJ-free';
    expect(visible(plain)).toBe(plain);
    expect(hasHidden(plain)).toBe(false);
  });

  it('answers the same for the same text, however often it is asked', () => {
    // a global regular expression remembers where it stopped: the answer must not
    for (let i = 0; i < 4; i++) {
      expect(hasHidden('a‮b')).toBe(true);
      expect(hasHidden('ab')).toBe(false);
    }
  });

  it('accepts what a missing field gives it', () => {
    expect(visible(undefined)).toBe('');
    expect(visible(null)).toBe('');
    expect(hasHidden(undefined)).toBe(false);
  });
});
