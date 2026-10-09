import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AdapterInfo, Agent } from '../../api/types.ts';
import { adapterOption, appliedNote, defaultRoutes, presetUntouched, purposeExcerpt, routesForUpdate, sortAdapters } from './team.ts';

const mk = (id: string, status: string, installed: boolean) => ({ id, detection: { adapter: id, installed, status }, capabilities: {} as never }) as AdapterInfo;

describe('team helpers', () => {
  it('sorts ready adapters first, then installed ones, then by id', () => {
    expect(sortAdapters([mk('gemini', 'not_installed', false), mk('claude', 'ready', true), mk('codex', 'not_installed', false)]).map((a) => a.id)).toEqual(['claude', 'codex', 'gemini']);
    expect(sortAdapters([mk('b', 'unauthenticated', true), mk('z', 'not_installed', false), mk('a', 'ready', true)]).map((a) => a.id)).toEqual(['a', 'b', 'z']);
  });

  it('names an adapter with its status, or says it is not installed', () => {
    expect(adapterOption(mk('claude', 'ready', true))).toBe('claude (ready)');
    expect(adapterOption(mk('codex', 'not_installed', false))).toBe('codex (not installed)');
  });

  it('shortens a long purpose to 160 characters', () => {
    expect(purposeExcerpt('short')).toBe('short');
    const long = 'x'.repeat(200);
    expect(purposeExcerpt(long)).toBe(`${'x'.repeat(160)}...`);
  });

  it('turns routing on with haiku and opus when the adapter offers both, else two empty rows', () => {
    expect(defaultRoutes(['opus', 'sonnet', 'haiku'])).toEqual([
      { model: 'haiku', tier: 'cheap' },
      { model: 'opus', tier: 'premium' },
    ]);
    expect(defaultRoutes([])).toEqual([
      { model: '', tier: 'cheap' },
      { model: '', tier: 'premium' },
    ]);
  });

  it('sends routed models only when they changed, without empty rows', () => {
    expect(routesForUpdate([], undefined)).toBe(undefined);
    expect(routesForUpdate([{ model: 'haiku', tier: 'cheap' }], [{ model: 'haiku', tier: 'cheap' }])).toBe(undefined);
    expect(routesForUpdate([{ model: 'haiku', tier: 'cheap' }, { model: ' ', tier: 'premium' }], [])).toEqual([{ model: 'haiku', tier: 'cheap' }]);
    expect(routesForUpdate([], [{ model: 'haiku', tier: 'cheap' }])).toEqual([]);
  });

  it('says who joined from a template, what was granted and what was not', () => {
    const agent = { name: 'Payments dev' } as Agent;
    expect(appliedNote({ agent, granted: ['docs'], skipped: [] })).toBe('Payments dev joined the team, with docs.');
    expect(appliedNote({ agent, granted: [], skipped: ['gh: not connected'] })).toBe('Payments dev joined the team. Not granted: gh: not connected.');
  });

  it('applies the Claude preset only while the fields are empty or still hold the last preset', () => {
    expect(presetUntouched('', '', undefined)).toBe(true);
    expect(presetUntouched('sonnet', 'medium', { model: 'sonnet', effort: 'medium' })).toBe(true);
    expect(presetUntouched('sonnet', 'low', { model: 'sonnet', effort: 'medium' })).toBe(false);
    expect(presetUntouched('opus', '', undefined)).toBe(false);
  });
});
