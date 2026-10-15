import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Provider } from '../../api/types.ts';
import { actionChips, cheapProviders, newRule, parseChain, parseThen, sortRules, splitChain, withChain } from './routing.ts';

const rule = (id: string, priority: number) => ({ ...newRule(), id, priority });

describe('routing helpers', () => {
  it('orders rules by priority, highest first, then by id', () => {
    expect(sortRules([rule('b', 1), rule('a', 5), rule('c', 5)]).map((r) => r.id)).toEqual(['a', 'c', 'b']);
  });

  it('refuses an action that is not JSON with a reason', () => {
    expect(parseThen('{"prefer_tier":"cheap"}')).toEqual({ ok: true, value: { prefer_tier: 'cheap' } });
    const bad = parseThen('{nope');
    expect(bad.ok).toBe(false);
    if (!bad.ok) expect(bad.error).toMatch(/^The action is not valid JSON: /);
  });

  it('turns an action into readable chips', () => {
    expect(actionChips({ prefer_tier: 'cheap', deny: true, require_capability: ['tools', 'vision'], handoff: false, penalty: 2 })).toEqual([
      { key: 'prefer_tier', text: 'prefer tier: cheap', tone: 'accent' },
      { key: 'deny', text: 'deny', tone: 'err' },
      { key: 'require_capability', text: 'require capability: tools, vision', tone: 'muted' },
      { key: 'penalty', text: 'penalty: 2', tone: 'warn' },
    ]);
    expect(actionChips({})).toEqual([]);
    expect(actionChips(null)).toEqual([]);
  });

  it('reads a fallback chain in order, each model once, and keeps it apart from the other actions', () => {
    expect(parseChain(' sonnet, haiku-b  sonnet,,')).toEqual(['sonnet', 'haiku-b']);
    expect(parseChain('')).toEqual([]);
    expect(splitChain({ prefer_tier: 'cheap', fallback: ['a', 'b'] })).toEqual({ rest: { prefer_tier: 'cheap' }, chain: 'a, b' });
    expect(splitChain(null)).toEqual({ rest: {}, chain: '' });
    expect(withChain({ prefer_tier: 'cheap', fallback: ['old'] }, ['a', 'b'])).toEqual({ prefer_tier: 'cheap', fallback: ['a', 'b'] });
    expect(withChain({ fallback: ['old'] }, [])).toEqual({});
    expect(actionChips({ fallback: ['a', 'b'] })).toEqual([{ key: 'fallback', text: 'fallback: a → b', tone: 'muted' }]);
  });

  it('cheap selection covers cheap, private providers only', () => {
    const p = (id: string, tier: string, dataClass: string) => ({ id, tier, dataClass }) as Provider;
    expect(cheapProviders([p('a', 'cheap', 'private'), p('b', 'cheap', 'trainable'), p('c', 'premium', 'private')]).map((x) => x.id)).toEqual(['a']);
  });
});
