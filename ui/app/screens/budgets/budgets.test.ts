import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Budget, BudgetSet } from '../../api/extTypes.ts';
import {
  BUDGET_SCOPES,
  formValues,
  limitsText,
  parseBudgetForm,
  parseLimits,
  plural,
  scopeIdAfter,
  stateTone,
  TOKENS_ERROR,
  TURNS_ERROR,
  USD_ERROR,
  WARN_ERROR,
  type BudgetFormValues,
} from './budgets.ts';
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

  /** The request for a form that must be valid. */
  const request = (v: BudgetFormValues, projectId: string, initial?: Budget): BudgetSet => {
    const r = parseBudgetForm(v, projectId, initial);
    if (!r.ok) throw new Error(`the form was refused: ${JSON.stringify(r.errors)}`);
    return r.request;
  };

  it('a new budget sends the warn percentage as a fraction and unset limits as null', () => {
    const v = { ...formValues('prj_1'), usd: '12.5' };
    expect(request(v, 'prj_1')).toEqual({
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
    expect(request({ ...formValues('prj_1'), scope: 'task', scopeId: ' * ', turns: '3', warn: '' }, 'prj_1')).toMatchObject({ scope: 'task', scopeId: '*', projectId: 'prj_1', limitTurns: 3, warnPct: 0.8 });
    const initial = b({ enabled: false, warnPct: 0.9, note: 'n' });
    const v = formValues('prj_1', initial);
    expect(v).toEqual({ scope: 'project', scopeId: 'prj_1', usd: '5', tokens: '', turns: '', warn: '90', note: 'n' });
    expect(request({ ...v, usd: '6' }, 'prj_1', initial)).toMatchObject({ id: 'bud_1', version: 4, enabled: false, limitUsd: 6, warnPct: 0.9 });
  });

  it('a mistyped limit is an error under its own field and no request is made, never a limit that quietly goes missing (M25)', () => {
    const v = formValues('prj_1');
    for (const bad of ['5 dollars', '$5', '1,000', '1e999', 'abc', '0', '-5', '5..5']) {
      expect(parseBudgetForm({ ...v, usd: bad }, 'prj_1')).toEqual({ ok: false, errors: { usd: USD_ERROR, tokens: undefined, turns: undefined, warn: undefined } });
    }
    for (const bad of ['200,000', '2e5', '1.5', 'many', '0', '-1', '5 tokens']) {
      expect(parseBudgetForm({ ...v, tokens: bad }, 'prj_1')).toEqual({ ok: false, errors: { usd: undefined, tokens: TOKENS_ERROR, turns: undefined, warn: undefined } });
    }
    for (const bad of ['forty', '4e1', '2.5', '0', '-3']) {
      expect(parseBudgetForm({ ...v, turns: bad }, 'prj_1')).toEqual({ ok: false, errors: { usd: undefined, tokens: undefined, turns: TURNS_ERROR, warn: undefined } });
    }
    for (const bad of ['0', '100', '80%', '8.5', 'high']) {
      expect(parseBudgetForm({ ...v, usd: '5', warn: bad }, 'prj_1')).toEqual({ ok: false, errors: { warn: WARN_ERROR } });
    }
    // a bad limit next to a good one: only the bad field is named
    expect(parseBudgetForm({ ...v, usd: '12.50', tokens: 'lots', turns: '10' }, 'prj_1')).toEqual({ ok: false, errors: { usd: undefined, tokens: TOKENS_ERROR, turns: undefined, warn: undefined } });
    // an edit of a budget is refused the same way (it used to save the old limit away as null)
    const initial = b();
    expect(parseBudgetForm({ ...formValues('prj_1', initial), usd: 'abc' }, 'prj_1', initial).ok).toBe(false);
  });

  it('a blank limit is still "no limit on that one", and a budget with none is left for the core to refuse in its own words', () => {
    const v = formValues('prj_1');
    expect(request({ ...v, usd: '  ', tokens: '1000' }, 'prj_1')).toMatchObject({ limitUsd: null, limitTokens: 1000, limitTurns: null });
    expect(request(v, 'prj_1')).toMatchObject({ limitUsd: null, limitTokens: null, limitTurns: null, warnPct: 0.8 });
    expect(parseLimits({ usd: '', tokens: '', turns: '' })).toEqual({ ok: true, usd: undefined, tokens: undefined, turns: undefined });
    expect(parseLimits({ usd: '7.25', tokens: ' 1000 ', turns: '3' })).toEqual({ ok: true, usd: 7.25, tokens: 1000, turns: 3 });
  });

  it('a stored limit comes back into its field as plain numbers and is accepted as the same number', () => {
    const tiny = b({ limitUsd: 1e-7, limitTokens: 5_000_000, limitTurns: 12 });
    const v = formValues('prj_1', tiny);
    expect(v).toMatchObject({ usd: '0.0000001', tokens: '5000000', turns: '12' });
    expect(request(v, 'prj_1', tiny)).toMatchObject({ limitUsd: 1e-7, limitTokens: 5_000_000, limitTurns: 12 });
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

describe('workflow budgets (P118)', () => {
  it('a workflow budget names a workflow and belongs to the project it is made in', () => {
    const r = parseBudgetForm({ ...formValues('prj_1'), scope: 'workflow', scopeId: 'iterate-until-verified', usd: '5' }, 'prj_1');
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.request.scope).toBe('workflow');
      expect(r.request.scopeId).toBe('iterate-until-verified');
      expect(r.request.projectId).toBe('prj_1');
    }
    // a budget on an explicit task still names no project
    const t = parseBudgetForm({ ...formValues('prj_1'), scope: 'task', scopeId: 'task_1', usd: '5' }, 'prj_1');
    expect(t.ok && t.request.projectId).toBe(undefined);
  });

  it('changing the scope to or from workflow changes the scope id with it', () => {
    const wfs = ['bug', 'iterate-until-verified'];
    expect(BUDGET_SCOPES).toContain('workflow');
    expect(scopeIdAfter('project', 'workflow', 'prj_1', 'prj_1', wfs)).toBe('bug');
    expect(scopeIdAfter('project', 'workflow', 'prj_1', 'prj_1', [])).toBe('');
    expect(scopeIdAfter('workflow', 'project', 'bug', 'prj_1', wfs)).toBe('prj_1');
    expect(scopeIdAfter('workflow', 'task', 'bug', 'prj_1', wfs)).toBe('');
    expect(scopeIdAfter('task', 'run', 'task_1', 'prj_1', wfs)).toBe('task_1');
    expect(scopeIdAfter('workflow', 'workflow', 'bug', 'prj_1', wfs)).toBe('bug');
  });
});
