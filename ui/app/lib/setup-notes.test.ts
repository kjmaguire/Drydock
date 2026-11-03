import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { dropSetupNote, keepSetupNotes, SETUP_NOTES_KEY, setupLines, setupNotes } from './setup-notes.ts';

/** A LABELLED TEST DOUBLE of the browser's storage. */
const memory = () => {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), m };
};

describe('setup notes (B2)', () => {
  it('takes the lines of a setup, or each warning of an older core as a line', () => {
    const cli = { kind: 'cli', text: 'Wren cannot work yet', adapter: 'claude', status: 'unauthenticated' };
    expect(setupLines({ warnings: ['Wren cannot work yet'], problems: [cli] })).toEqual([cli]);
    expect(setupLines({ warnings: ['brief not saved: x'] })).toEqual([{ kind: 'other', text: 'brief not saved: x' }]);
  });

  it('keeps them per project until each is dropped, and survives what it cannot read', () => {
    const s = memory();
    const a = { kind: 'other', text: 'brief not saved' };
    const b = { kind: 'connector', text: 'connector docs not added' };
    keepSetupNotes(s, 'p1', [a, b]);
    keepSetupNotes(s, 'p2', []);
    expect(setupNotes(s, 'p1')).toEqual([a, b]);
    expect(setupNotes(s, 'p2')).toEqual([]);
    expect(dropSetupNote(s, 'p1', a)).toEqual([b]);
    dropSetupNote(s, 'p1', b);
    expect(s.m.get(SETUP_NOTES_KEY)).toBe('{}');
    s.setItem(SETUP_NOTES_KEY, 'not json');
    expect(setupNotes(s, 'p1')).toEqual([]);
    expect(setupNotes(undefined, 'p1')).toEqual([]);
  });
});
