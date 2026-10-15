import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { BudgetStatus } from '../../api/extTypes.ts';
import { budgetStatus } from '../../test/fixtures.ts';
import { dayStart, doing, firstCheckText, greeting, mergedSince, ms, spendLine } from './overview.ts';

const DAY = 86_400_000;

describe('the day', () => {
  it('greets by the hour', () => {
    expect(greeting(6)).toBe('Good morning');
    expect(greeting(13)).toBe('Good afternoon');
    expect(greeting(21)).toBe('Good evening');
  });

  it('says what an agent is doing from its presence', () => {
    expect(doing(undefined)).toBe('Resting');
    expect(doing({ agentId: 'a', presence: 'working', label: 'Working', activity: 'Writing tests' } as never)).toBe('Writing tests');
    expect(doing({ agentId: 'a', presence: 'idle', label: 'Idle' } as never)).toBe('Idle');
    expect(doing({ agentId: 'a', presence: 'away' } as never)).toBe('away');
  });

  it('reads seconds and milliseconds alike', () => {
    expect(ms(undefined)).toBe(0);
    expect(ms(1_700_000_000)).toBe(1_700_000_000_000);
    expect(ms(1_700_000_000_000)).toBe(1_700_000_000_000);
  });

  it('counts the tasks completed since a moment, today or this week', () => {
    const now = Date.UTC(2026, 9, 15, 12);
    const tasks = [
      { status: 'COMPLETED', completed: now - 3 * DAY, updated: now },
      { status: 'COMPLETED', completed: now - 10 * DAY, updated: now },
      { status: 'COMPLETED', updated: now - DAY },
      { status: 'IN_PROGRESS', completed: now, updated: now },
    ];
    expect(mergedSince(tasks, now - 7 * DAY)).toBe(2);
    expect(mergedSince(tasks, now - DAY / 2)).toBe(0);
    expect(dayStart(now)).toBeLessThanOrEqual(now);
    expect(now - dayStart(now)).toBeLessThan(DAY);
  });
});

describe('the Insights numbers', () => {
  it('spend is the known cost against the project budget; unknown-cost calls are counted, never priced', () => {
    const st: BudgetStatus = budgetStatus({ pctUsd: 0.5 });
    expect(spendLine({ rows: 3, reportedUsd: 2.5, estimatedUsd: 0.25, unknownCostRows: 0, inputTokens: 0, outputTokens: 0, cachedTokens: 0, turns: 3 }, [st])).toEqual({ amount: '$2.75', note: 'of $5 budget', fraction: 0.5 });
    expect(spendLine({ rows: 2, reportedUsd: 0, estimatedUsd: 0, unknownCostRows: 2, inputTokens: 0, outputTokens: 0, cachedTokens: 0, turns: 2 }, [])).toEqual({ amount: 'n/a', note: 'no budget set · 2 calls not priced', fraction: undefined });
    expect(spendLine(undefined, [])).toEqual({ amount: '$0.00', note: 'no budget set', fraction: undefined });
  });

  it('a disabled budget, a budget without a dollar limit or a task budget is not the project line', () => {
    const base = budgetStatus();
    expect(spendLine(undefined, [{ ...base, budget: { ...base.budget, enabled: false } }]).note).toBe('no budget set');
    expect(spendLine(undefined, [{ ...base, budget: { ...base.budget, limitUsd: null, limitTokens: 1000 } }]).note).toBe('no budget set');
    expect(spendLine(undefined, [{ ...base, budget: { ...base.budget, scope: 'task', scopeId: '*' } }]).note).toBe('no budget set');
  });

  it('the first-check rate is a percentage, or n/a before any task was checked', () => {
    expect(firstCheckText(undefined)).toBe('n/a');
    expect(firstCheckText({ quality: { started: 0, accepted: 0, failed: 0, cancelled: 0, active: 0, completion: { num: 0, den: 0 }, firstCheckTestPass: { num: 0, den: 0, rate: null }, finalTestPass: { num: 0, den: 0 } } })).toBe('n/a');
    expect(firstCheckText({ quality: { started: 8, accepted: 7, failed: 1, cancelled: 0, active: 0, completion: { num: 7, den: 8, rate: 0.875 }, firstCheckTestPass: { num: 7, den: 8, rate: 0.875 }, finalTestPass: { num: 7, den: 8, rate: 0.875 } } })).toBe('88%');
  });
});
