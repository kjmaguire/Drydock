import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { BudgetRow, BudgetSet } from '../../api/extTypes.ts';
import type { Client } from '../../api/client.ts';
import { budget, budgetStatus } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { BudgetsScreen } from './budgets-screen.tsx';

afterEach(cleanup);

function servicesWith(client: Partial<Client>) {
  const s = testServices({
    projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as never],
    licenseStatus: async () => undefined as never,
    appInfo: async () => ({}) as never,
    ...client,
  });
  s.workspace.start();
  return s;
}

const text = (el: Element) => el.textContent ?? '';
/** The element matching the selector; throws (so waitFor retries) until there is one. */
const need = (r: { $: (sel: string) => Element | null }, sel: string) => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
};
const until = (check: () => boolean, msg = 'not yet') => () => {
  if (!check()) throw new Error(msg);
};

describe('BudgetsScreen', () => {
  it('shows ok, warning and paused with text, and never prices unknown calls at zero', async () => {
    const rows: BudgetRow[] = [
      { budget: budget(), status: budgetStatus() },
      { budget: budget({ id: 'b2', scope: 'task', scopeId: 't1' }), status: budgetStatus({ state: 'warning', pct: 0.85, pctUsd: 0.85, budget: budget({ id: 'b2' }) }) },
      {
        budget: budget({ id: 'b3', scopeId: 'prj_2' }),
        status: budgetStatus({
          state: 'exceeded',
          pct: 1.2,
          pctUsd: 1.2,
          costComplete: false,
          spend: { rows: 4, reportedUsd: 6, estimatedUsd: 0, unknownCostRows: 2, inputTokens: 1, outputTokens: 1, cachedTokens: 0, turns: 4 },
        }),
      },
      { budget: budget({ id: 'b4', scope: 'task', scopeId: '*', projectId: 'prj_1' }) },
    ];
    const asked: string[] = [];
    const r = mount(<BudgetsScreen />, servicesWith({ budgetList: async (pid) => (asked.push(pid), rows) }));
    const cards = await waitFor(() => {
      const c = Array.from(r.$$('[data-testid="budget"]'));
      if (c.length !== 4) throw new Error(`have ${c.length}`);
      return c;
    }, r);
    expect(asked[0]).toBe('prj_1');
    expect(cards.map((c) => c.getAttribute('data-state'))).toEqual(['ok', 'warning', 'exceeded', 'template']);
    expect(text(cards[0]!)).toContain('$5');
    expect(text(cards[0]!)).toContain('warns at 80%');
    expect(text(cards[1]!)).toContain('warning at 80%');
    expect(text(cards[2]!)).toContain('paused at 100%');
    expect(text(cards[3]!)).toContain('ceiling for every task');
    const unknown = cards[2]!.querySelector('[data-testid="unknown-cost"]')!;
    expect(text(unknown)).toContain('2 calls with unknown cost are counted but not priced at zero');
    expect(text(unknown)).toContain('lower bound');
    // the bar repeats the number in words, and over 100% the text keeps the real value
    const bar = cards[2]!.querySelector('[role="progressbar"]')!;
    expect(bar.getAttribute('aria-label')).toBe('120% of the limit used');
    expect(bar.getAttribute('aria-valuenow')).toBe('100');
    expect(cards[3]!.querySelector('[role="progressbar"]')).toBeNull();
  });

  it('adds a budget with the warn percentage as a fraction and unset limits as null', async () => {
    const calls: BudgetSet[] = [];
    let list: BudgetRow[] = [];
    const s = servicesWith({
      budgetList: async () => list,
      budgetSet: async (b) => {
        calls.push(b);
        list = [{ budget: budget({ ...b, id: 'bud_9', limitUsd: b.limitUsd }) }];
        return list[0]!.budget;
      },
    });
    const r = mount(<BudgetsScreen />, s);
    await waitFor(until(() => text(r.container).includes('No budgets')), r);
    await r.act(() => byText(r.container, 'button', 'Add budget').click());
    const form = await waitFor(() => need(r, 'form[aria-label="New budget"]'), r);
    type(byLabel(form, 'Limit in USD'), '12.5');
    await r.act(() => byText(form, 'button', 'Add budget').click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toMatchObject({ scope: 'project', scopeId: 'prj_1', limitUsd: 12.5, limitTokens: null, limitTurns: null, warnPct: 0.8, enabled: true });
    await waitFor(() => expect(r.$$('[data-testid="budget"]').length).toBe(1), r);
    expect(r.$('form[aria-label="New budget"]')).toBeNull();
  });

  it('shows the core error when a budget is refused', async () => {
    const r = mount(
      <BudgetsScreen />,
      servicesWith({
        budgetList: async () => [],
        budgetSet: async () => {
          throw new Error('budget: set at least one of limitUsd, limitTokens, limitTurns');
        },
      }),
    );
    await waitFor(until(() => text(r.container).includes('No budgets')), r);
    await r.act(() => byText(r.container, 'button', 'Add budget').click());
    const form = await waitFor(() => need(r, 'form[aria-label="New budget"]'), r);
    await r.act(() => byText(form, 'button', 'Add budget').click());
    await waitFor(() => expect(text(r.$('[role="alert"]')!)).toContain('set at least one of limitUsd'), r);
  });

  it('edits limits against the version, toggles, and deletes only after a confirm', async () => {
    const sets: BudgetSet[] = [];
    const deleted: string[] = [];
    const s = servicesWith({
      budgetList: async () => [{ budget: budget({ version: 3 }), status: budgetStatus() }],
      budgetSet: async (b) => (sets.push(b), budget()),
      budgetDelete: async (id) => void deleted.push(id),
    });
    const r = mount(<BudgetsScreen />, s);
    const card = await waitFor(() => need(r, '[data-testid="budget"]'), r);
    await r.act(() => byText(card, 'button', 'Edit limits').click());
    const form = await waitFor(() => need(r, 'form[aria-label="Edit budget"]'), r);
    expect(byLabel<HTMLButtonElement>(form, 'Scope').disabled).toBe(true);
    expect(byLabel(form, 'Limit in USD').value).toBe('5');
    type(byLabel(form, 'Limit in USD'), '7.5');
    await r.act(() => byText(form, 'button', 'Save changes').click());
    await waitFor(() => expect(sets[0]).toMatchObject({ id: 'bud_1', version: 3, limitUsd: 7.5, scope: 'project', scopeId: 'prj_1' }), r);

    await r.act(() => byText(r.$('[data-testid="budget"]')!, 'button', 'Disable').click());
    await waitFor(() => expect(sets[1]).toMatchObject({ id: 'bud_1', enabled: false }), r);

    await r.act(() => byText(r.$('[data-testid="budget"]')!, 'button', 'Delete').click());
    expect(deleted).toEqual([]);
    await r.act(() => byText(r.$('[data-testid="budget"]')!, 'button', 'Yes, delete').click());
    await waitFor(() => expect(deleted).toEqual(['bud_1']), r);
  });

  it('asks for a project when none is selected', async () => {
    const r = mount(<BudgetsScreen />, servicesWith({ projectList: async () => [] }));
    await waitFor(until(() => text(r.container).includes('No project selected')), r);
    expect(Array.from(r.$$('button')).some((b) => text(b) === 'Add budget')).toBe(false);
  });
});
