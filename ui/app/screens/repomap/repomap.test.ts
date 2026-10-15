import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { RepoMapResult } from '../../api/repomapClient.ts';
import { budgetOf, languagesText, parseFocus, rankText, skippedText, statsText } from './repomap.ts';

describe('repo map page data', () => {
  it('reads focus paths separated by commas or new lines, without blanks or repeats', () => {
    expect(parseFocus(' internal/core, ui/app\n\ninternal/core ,')).toEqual(['internal/core', 'ui/app']);
    expect(parseFocus('')).toEqual([]);
  });

  it('keeps the budget inside 256..8192 and falls back to 2048', () => {
    expect(budgetOf('1024')).toBe(1024);
    expect(budgetOf('10')).toBe(256);
    expect(budgetOf('99999')).toBe(8192);
    expect(budgetOf('')).toBe(2048);
    expect(budgetOf('many')).toBe(2048);
  });

  it('names only the reasons that left files out', () => {
    const none = { unsupported: 0, vendored: 0, generated: 0, tooLarge: 0, binary: 0, unreadable: 0, overLimit: 0 };
    expect(skippedText(none)).toBe('');
    expect(skippedText({ ...none, unsupported: 12, vendored: 3 })).toBe('12 in other languages or not code, 3 vendored');
  });

  it('summarises a map', () => {
    expect(languagesText({ typescript: 12, go: 40, rust: 12 })).toBe('go 40, rust 12, typescript 12');
    const r = { shown: 9, files: 52, tokens: 1990, budget: 2048, elapsedMs: 340, cached: 50 } as RepoMapResult;
    expect(statsText(r)).toBe('9 of 52 source files in the map, about 1990 of 2048 tokens. Made in 340 ms, 50 from the cache.');
    expect(statsText({ ...r, files: 1, cached: 0 })).toBe('9 of 1 source file in the map, about 1990 of 2048 tokens. Made in 340 ms.');
    expect([rankText(0.2579), rankText(0.0169), rankText(0.0042)]).toEqual(['26%', '1.7%', '0.42%']);
  });
});
