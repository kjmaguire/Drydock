import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Budget } from '../../api/extTypes.ts';
import { budgetRequest, formValues, limitsText, plural, stateTone } from './budgets.ts';
import { drawFraction } from './meter.tsx';

const b = (over: Partial<Budget> = {}): Budget => ({
  id: 'bud_1',
  scope: 'project',
  scopeId: 'prj_1',
  limitUsd: 5,
  warnPct: 0.8,
  enabled: true,
  version: 4,
  createdAt: 0,
  updatedAt: 0,
  updatedBy: 'human:kyle',
  ...over,
});

describe('budget helpers', () => {
  it('limit text lists every limit and says when there is none', () => {
    expect(limitsText(b())).toBe('$5');
    expect(limitsText(b({ limitUsd: 7.5, limitTokens: 1000, limitTurns: 10 }))).toBe(`$7.5, ${(1000).toLocaleString()} tokens, 10 turns`);
    expect(limitsText(b({ limitUsd: null }))).toBe('no limit');
  });

  it('a new budget sends the warn percentage as a fraction and unset limits as null', () => {
    const v = { ...formValues('prj_1'), usd: '12.5' };
    expect(budgetRequest(v, 'prj_1')).toEqual({
      id: undefined,
      version: undefined,
      scope: 'project',
      scopeId: 'prj_1',
      projectId: undefined,
      limitUsd: 12.5,
      limitTokens: null,
      limitTurns: null,
      warnPct: 0.8,
      enabled: true,
      note: '',
    });
  });

  it('a "*" ceiling carries its project; an edit keeps id, version and enabled', () => {
    expect(budgetRequest({ ...formValues('prj_1'), scope: 'task', scopeId: ' * ', turns: '3', warn: '' }, 'prj_1')).toMatchObject({ scope: 'task', scopeId: '*', projectId: 'prj_1', limitTurns: 3, warnPct: 0.8 });
    const initial = b({ enabled: false, warnPct: 0.9, note: 'n' });
    const v = formValues('prj_1', initial);
    expect(v).toEqual({ scope: 'project', scopeId: 'prj_1', usd: '5', tokens: '', turns: '', warn: '90', note: 'n' });
    expect(budgetRequest({ ...v, usd: 'abc' }, 'prj_1', initial)).toMatchObject({ id: 'bud_1', version: 4, enabled: false, limitUsd: null, warnPct: 0.9 });
  });

  it('state tones and plurals; the bar is clamped but the value is not', () => {
    expect(stateTone('ok')).toBe('ok');
    expect(stateTone('warning')).toBe('warn');
    expect(stateTone('exceeded')).toBe('err');
    expect(plural(1, 'turn')).toBe('1 turn');
    expect(plural(2, 'turn')).toBe('2 turns');
    expect(drawFraction(1.2)).toBe(1);
    expect(drawFraction(-1)).toBe(0);
    expect(drawFraction(null)).toBe(0);
    expect(drawFraction(0.25)).toBe(0.25);
  });
});
