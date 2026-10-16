// The composer's typing aids (app/lib/composer.ts), ported from the legacy composer tests.
import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { activeToken, applySuggestion, mentionHandle, parseSlash, suggest } from '../../lib/composer.ts';

describe('composer typing aids', () => {
  it('finds the token at the caret', () => {
    expect(activeToken('hi @ba', 6)).toEqual({ sigil: '@', query: 'ba', start: 3, end: 6 });
    expect(activeToken('ping @role:rev', 14)).toMatchObject({ sigil: '@role:', query: 'rev' });
    expect(activeToken('see #gen', 8)).toMatchObject({ sigil: '#', query: 'gen' });
    expect(activeToken('/ta', 3)).toMatchObject({ sigil: '/', query: 'ta' });
    expect(activeToken('a /ta', 5)).toBeUndefined();
    expect(activeToken('mail a@b', 8)).toBeUndefined();
    expect(activeToken('hello', 5)).toBeUndefined();
  });

  it('inserts a handle the room resolves: the name when it is a clean unique handle, else the id', () => {
    expect(mentionHandle('Backend One', 'agt_1', ['Backend One', 'Reviewer #1'])).toBe('backend-one');
    expect(mentionHandle('Reviewer #1', 'agt_2', ['Backend One', 'Reviewer #1'])).toBe('agt_2');
    expect(mentionHandle('Sam', 'agt_3', ['Sam', 'sam'])).toBe('agt_3');
  });

  it('suggests members, roles, rooms and commands, best match first', () => {
    const ctx = {
      members: [
        { kind: 'agent' as const, id: 'agt_1', name: 'Backend One', role: 'backend' },
        { kind: 'tool' as const, id: 'tests', name: 'Tests' },
      ],
      rooms: [
        { name: 'general', kind: 'channel' },
        { name: 'task-3-subtract', kind: 'task' },
      ],
    };
    expect(suggest({ sigil: '@', query: 'back', start: 0, end: 5 }, ctx)[0]).toMatchObject({ label: 'Backend One', insert: '@backend-one ' });
    expect(suggest({ sigil: '@', query: 'tes', start: 0, end: 4 }, ctx)[0]?.insert).toBe('@tests ');
    expect(suggest({ sigil: '@role:', query: 'b', start: 0, end: 7 }, ctx)[0]?.insert).toBe('@role:backend ');
    expect(suggest({ sigil: '#', query: 'task', start: 0, end: 5 }, ctx).map((s) => s.insert)).toEqual(['#task-3-subtract ']);
    expect(suggest({ sigil: '/', query: 'de', start: 0, end: 3 }, ctx)[0]?.insert).toBe('/decision ');
    const r = applySuggestion('hi @ba there', { sigil: '@', query: 'ba', start: 3, end: 6 }, { label: '', insert: '@backend-one ' });
    expect(r).toEqual({ text: 'hi @backend-one there', caret: 16 });
  });

  it('parses slash commands into actions, and leaves plain text alone', () => {
    expect(parseSlash('hello')).toEqual({ kind: 'post', body: 'hello' });
    expect(parseSlash('/decision Use SQLite')).toEqual({ kind: 'post', body: '[DECISION] Use SQLite' });
    expect(parseSlash('/task Add Multiply')).toEqual({ kind: 'task', title: 'Add Multiply' });
    expect(parseSlash('/invite @backend-one')).toEqual({ kind: 'invite', handle: 'backend-one' });
    expect(parseSlash('/mode sequential')).toEqual({ kind: 'mode', mode: 'sequential' });
    expect(parseSlash('/help')).toEqual({ kind: 'help' });
    expect(parseSlash('/task').kind).toBe('error');
    expect(parseSlash('/mode moderator').kind).toBe('error');
    expect(parseSlash('/rm -rf').kind).toBe('error');
  });
});
