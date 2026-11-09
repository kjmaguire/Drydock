import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { RepoMapResult } from '../../api/repomapClient.ts';
import { budgetOf, doubled, languagesText, parseFocus, rankText, REPO_MAP_PREFS_MAX, repoMapPrefsOf, skippedText, statsText, taskFocusChips, toggleFocus, withRepoMapPrefs } from './repomap.ts';

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
    const none = { unsupported: 0, vendored: 0, generated: 0, tooLarge: 0, binary: 0, unreadable: 0, linked: 0, overLimit: 0 };
    expect(skippedText(none)).toBe('');
    expect(skippedText({ ...none, unsupported: 12, vendored: 3 })).toBe('12 in other languages or not code, 3 vendored');
    expect(skippedText({ ...none, linked: 2 })).toBe('2 links (not read)');
  });

  it('summarises a map', () => {
    expect(languagesText({ typescript: 12, go: 40, rust: 12 })).toBe('go 40, rust 12, typescript 12');
    const r = { shown: 9, files: 52, tokens: 1990, budget: 2048, elapsedMs: 340, cached: 50 } as RepoMapResult;
    expect(statsText(r)).toBe('9 of 52 source files in the map, about 1990 of 2048 tokens. Made in 340 ms, 50 from the cache.');
    expect(statsText({ ...r, files: 1, cached: 0 })).toBe('9 of 1 source file in the map, about 1990 of 2048 tokens. Made in 340 ms.');
    expect([rankText(0.2579), rankText(0.0169), rankText(0.0042)]).toEqual(['26%', '1.7%', '0.42%']);
  });
});

describe('the repo map as the person last made it (X412)', () => {
  it('reads a project\'s focus and budget, and anything malformed as the plain map', () => {
    expect(repoMapPrefsOf(undefined, 'p1')).toEqual({ focus: [], budget: 2048 });
    expect(repoMapPrefsOf({ p1: { focus: ['a', 3, ' b ', 'a'], budget: 100000 } }, 'p1')).toEqual({ focus: ['a', 'b'], budget: 8192 });
    expect(repoMapPrefsOf({ p1: 'nonsense' }, 'p1')).toEqual({ focus: [], budget: 2048 });
    const stored = withRepoMapPrefs({ p0: { focus: ['x'], budget: 1024 } }, 'p1', { focus: ['y'], budget: 4096 });
    expect(stored).toEqual({ p0: { focus: ['x'], budget: 1024 }, p1: { focus: ['y'], budget: 4096 } });
  });

  it('keeps at most REPO_MAP_PREFS_MAX projects, letting the oldest go', () => {
    const raw: Record<string, unknown> = {};
    for (let i = 0; i < REPO_MAP_PREFS_MAX; i++) raw[`p${i}`] = { focus: [], budget: 1024 };
    const out = withRepoMapPrefs(raw, 'new', { focus: [], budget: 2048 });
    expect(Object.keys(out).length).toBe(REPO_MAP_PREFS_MAX);
    expect('p0' in out).toBe(false);
    expect(out.new).toEqual({ focus: [], budget: 2048 });
  });

  it('doubles the budget up to the most, toggles a focus path, and offers open tasks that name files', () => {
    expect(doubled(1024)).toBe(2048);
    expect(doubled(6000)).toBe(8192);
    expect(doubled(8192)).toBe(undefined);
    expect(toggleFocus(['a'], 'b')).toEqual(['a', 'b']);
    expect(toggleFocus(['a', 'b'], 'a')).toEqual(['b']);
    const chips = taskFocusChips([
      { id: 't1', seq: 1, status: 'READY', updated: 1, files: ['a.go'] },
      { id: 't2', seq: 2, status: 'COMPLETED', updated: 5, files: ['b.go'] },
      { id: 't3', seq: 3, status: 'IN_PROGRESS', updated: 9, files: ['c.go', ' c.go '] },
      { id: 't4', seq: 4, status: 'READY', updated: 9, files: [] },
    ]);
    expect(chips).toEqual([
      { id: 't3', seq: 3, files: ['c.go'] },
      { id: 't1', seq: 1, files: ['a.go'] },
    ]);
  });
});
