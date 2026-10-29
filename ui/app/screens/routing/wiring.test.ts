import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { RoutePreview } from '../../api/extTypes.ts';
import { sharedBlock, WIRE, wireAnswer, wireGroups, wireLayout, wirePath } from './wiring.ts';

const decided = (Model: string, Provider = 'anthropic', Effort = ''): RoutePreview => ({
  blocked: false,
  decision: { ID: 'd', Provider, Model, Effort, Reason: 'the balanced tier', Confidence: 1, EstCostUSD: null, DecidedBy: 'router' },
});

describe('the routing wiring (decision P108)', () => {
  it('has a card per level of agent work, cheapest first, and one per other kind of turn at the balanced level', () => {
    const [levels, purposes] = wireGroups();
    expect(levels!.rows.map((r) => r.label)).toEqual(['Cheapest', 'Fast', 'Balanced', 'Quality', 'Maximum quality']);
    expect(levels!.rows.every((r) => r.request.RunKind === 'agent_task')).toBe(true);
    expect(levels!.rows.map((r) => r.request.Level)).toEqual(['cheapest', 'fast', 'balanced', 'quality', 'maximum_quality']);
    // each level has a colour of its own; the purposes are grey
    expect(new Set(levels!.rows.map((r) => r.tone)).size).toBe(5);
    expect(purposes!.rows.map((r) => r.label).slice(0, 3)).toEqual(['Review', 'Room reply', 'Spec (pm)']);
    expect(purposes!.rows.some((r) => r.request.RunKind === 'agent_task')).toBe(false);
    expect(purposes!.rows.every((r) => r.request.Level === 'balanced' && r.tone === 'muted')).toBe(true);
  });

  it('says the model by name with its provider and effort, "Blocked" with why, and a failed call as not known', () => {
    expect(wireAnswer(decided('claude-opus-5-5', 'anthropic', 'high'))).toEqual({ model: 'Opus 5.5', detail: 'anthropic · high effort', blocked: false, title: 'the balanced tier' });
    expect(wireAnswer(decided('', 'local')).model).toBe('default model');
    expect(wireAnswer({ blocked: true, reason: 'no candidate providers or models are registered' })).toEqual({
      model: 'Blocked',
      detail: '',
      blocked: true,
      title: 'no candidate providers or models are registered',
    });
    expect(wireAnswer(new Error('core not answering'))).toEqual({ model: 'Not known', detail: '', blocked: true, title: 'core not answering' });
    expect(wireAnswer(undefined).model).toBe('…');
  });

  it('says once why every card is blocked when they all give the same reason, and nothing when one went through', () => {
    const no: RoutePreview = { blocked: true, reason: 'no providers' };
    expect(sharedBlock([no, no, no])).toBe('no providers');
    expect(sharedBlock([no, decided('x')])).toBe('');
    expect(sharedBlock([no, { blocked: true, reason: 'denied by a rule' }])).toBe('');
    expect(sharedBlock(undefined)).toBe('');
  });

  it('lays the cards out under their headings and puts the node in the middle; a wire leaves and arrives level', () => {
    const l = wireLayout([
      { key: 'a', rows: [{ key: 'a1' }, { key: 'a2' }] as never },
      { key: 'b', rows: [{ key: 'b1' }] as never },
    ]);
    expect(l.heads).toEqual([
      { key: 'a', top: 0 },
      { key: 'b', top: WIRE.head + 2 * WIRE.row + WIRE.gap + WIRE.groupGap },
    ]);
    expect(l.rows[0]).toEqual({ key: 'a1', top: WIRE.head, mid: WIRE.head + WIRE.row / 2 });
    expect(l.rows[1]!.top).toBe(WIRE.head + WIRE.row + WIRE.gap);
    expect(l.height).toBe(l.rows[2]!.top + WIRE.row);
    expect(l.node).toBe(l.height / 2);
    expect(wirePath(10, 40)).toBe('M0 10 C50 10 50 40 100 40');
  });
});
