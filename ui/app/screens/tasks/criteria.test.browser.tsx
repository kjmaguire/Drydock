import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { CriterionStatus } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, fromMenu, mount, type, waitFor } from '../../test/render.tsx';
import { TaskDetailScreen } from './task-detail-screen.tsx';
import { detail, recorder, svc, task } from './testing.ts';

afterEach(cleanup);

// The acceptance criteria with a status each (decision P125), with a test double of the client (testing.ts): the status and
// what showed it, an agent's unconfirmed claim, and the person's word from each criterion's menu.

const acceptance = ['AC1: Given 3 and 4, when Multiply, then 12', 'AC2: Given 0, when Multiply, then 0', 'AC3: Given -1, when Multiply, then -4'];

const statuses: CriterionStatus[] = [
  { index: 0, id: 'AC1', status: 'met', source: 'check', by: 'test', evidence: 'the newest test run passed, and its tests carry AC1', at: 5 },
  { index: 1, id: 'AC2', status: 'not_met', source: 'review', by: 'a2', evidence: 'AC2 is not met: Multiply(0, 0) panics', at: 6, claim: { by: 'a1', evidence: 'TestMultiply_AC2 passes', at: 7 } },
  { index: 2, id: 'AC3', status: 'waived', source: 'person', by: 'human:kyle', evidence: 'negative numbers are out of scope', at: 8 },
];

const rows = (r: ReturnType<typeof mount>) => Array.from(r.$$('[data-testid="task-criteria"] li')) as HTMLElement[];

function need<T>(v: T | null | undefined): T {
  if (!v) throw new Error('not yet');
  return v;
}

async function menuState(r: ReturnType<typeof mount>, trigger: HTMLElement): Promise<[string, boolean][]> {
  await r.act(() => trigger.click());
  const items = await waitFor(() => {
    const els = Array.from(r.$$('[popover]:popover-open [role="menuitem"]')) as HTMLElement[];
    if (!els.length) throw new Error('the menu did not open');
    return els;
  }, r);
  const out = items.map((m): [string, boolean] => [m.textContent!.trim(), m.getAttribute('aria-disabled') === 'true']);
  await r.act(() => document.activeElement?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })));
  return out;
}

describe('acceptance criteria with a status each', () => {
  it('says each status in words with what showed it, and an agent claim apart from the status', async () => {
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ acceptance }), criteria: statuses }) }));
    await waitFor(() => expect(rows(r).length).toBe(3), r);
    expect(rows(r).map((li) => [li.getAttribute('data-state'), li.getAttribute('title'), li.querySelector('svg')!.getAttribute('aria-label')])).toEqual([
      ['met', 'AC1: met', 'met'],
      ['not_met', 'AC2: not met', 'not met'],
      ['waived', 'AC3: waived', 'waived'],
    ]);
    await waitFor(() => expect(rows(r)[1]!.querySelector('.why')!.textContent).toBe("Rex's review: AC2 is not met: Multiply(0, 0) panics."), r);
    expect(rows(r).map((li) => li.querySelector('.why')!.textContent)).toEqual([
      'By the test check: the newest test run passed, and its tests carry AC1.',
      "Rex's review: AC2 is not met: Multiply(0, 0) panics.",
      'Waived by kyle: negative numbers are out of scope.',
    ]);
    expect(rows(r)[1]!.querySelector('.claim')!.textContent).toBe('Dana says it is met: TestMultiply_AC2 passes. Not confirmed: a check or a person confirms it.');
    expect(rows(r)[0]!.querySelector('.claim')).toBeNull();
  });

  it("records the person's word from the menu and shows the core's answer", async () => {
    const mark = recorder(async (_t: string, i: number, m: string): Promise<CriterionStatus[]> =>
      statuses.map((c) => (c.index === i ? { index: i, id: c.id, status: m, source: 'person', by: 'human:kyle', at: 9 } : c)),
    );
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ acceptance }), criteria: statuses }), taskCriterionMark: mark }));
    const more = await waitFor(() => need(r.$('[data-testid="criterion-1"]')) as HTMLElement, r);
    // Reopen takes back a person's word only: AC2's status is a review's
    expect(await menuState(r, more)).toEqual([
      ['Mark as met', false],
      ['Mark as not met', false],
      ['Waive...', false],
      ['Reopen', true],
    ]);
    await fromMenu(r, more, 'Mark as met');
    await waitFor(() => expect(mark.calls).toEqual([['t1', 1, 'met', '']]), r);
    await waitFor(() => expect(rows(r)[1]!.getAttribute('data-state')).toBe('met'), r);
    expect(rows(r)[1]!.querySelector('.why')!.textContent).toBe('Confirmed by kyle.');
    // AC3 is the person's waiver: Reopen is offered, Waive is not
    expect(await menuState(r, r.$('[data-testid="criterion-2"]') as HTMLElement)).toEqual([
      ['Mark as met', false],
      ['Mark as not met', false],
      ['Waive...', true],
      ['Reopen', false],
    ]);
    await fromMenu(r, r.$('[data-testid="criterion-2"]') as HTMLElement, 'Reopen');
    await waitFor(() => expect(mark.calls[1]).toEqual(['t1', 2, 'open', '']), r);
  });

  it('asks why before it waives, and sends nothing without a reason', async () => {
    const mark = recorder(async (): Promise<CriterionStatus[]> => [{ ...statuses[0]!, status: 'waived', source: 'person', by: 'human:kyle', evidence: 'covered elsewhere' }, statuses[1]!, statuses[2]!]);
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ acceptance }), criteria: statuses }), taskCriterionMark: mark }));
    const more = await waitFor(() => need(r.$('[data-testid="criterion-0"]')) as HTMLElement, r);
    await fromMenu(r, more, 'Waive...');
    const form = await waitFor(() => need(r.$('form[aria-label="Waive AC1"]')) as HTMLElement, r);
    const waive = byText(form, 'button', 'Waive') as HTMLButtonElement;
    expect(waive.disabled).toBe(true);
    await r.act(() => type(byLabel(form, 'Why is it waived?'), 'covered elsewhere'));
    await waitFor(() => expect(waive.disabled).toBe(false), r);
    await r.act(() => waive.click());
    await waitFor(() => expect(mark.calls).toEqual([['t1', 0, 'waived', 'covered elsewhere']]), r);
    await waitFor(() => expect(r.$('form[aria-label="Waive AC1"]')).toBeNull(), r);
    expect(rows(r)[0]!.getAttribute('data-state')).toBe('waived');
  });

  it("shows the core's refusal and keeps the criterion as it was", async () => {
    const mark = recorder(async (): Promise<CriterionStatus[]> => Promise.reject(new Error('only a person can confirm, waive or reopen an acceptance criterion')));
    const r = mount(<TaskDetailScreen id="t1" />, svc({ taskGet: async () => detail({ task: task({ acceptance }), criteria: statuses }), taskCriterionMark: mark }));
    const more = await waitFor(() => need(r.$('[data-testid="criterion-1"]')) as HTMLElement, r);
    await fromMenu(r, more, 'Mark as not met');
    await waitFor(() => expect(r.container.textContent).toContain('only a person can confirm'), r);
    expect(rows(r)[1]!.getAttribute('data-state')).toBe('not_met');
  });

  it('read-only: confirming and waiving are off, saying not met and reopening stay', async () => {
    const s = svc({ taskGet: async () => detail({ task: task({ acceptance }), criteria: statuses }) }, { license: expiredLicense });
    const r = mount(<TaskDetailScreen id="t1" />, s);
    const more = await waitFor(() => need(r.$('[data-testid="criterion-2"]')) as HTMLElement, r);
    await waitFor(() => expect(s.workspace.license.readOnly).toBe(true), r);
    expect(await menuState(r, more)).toEqual([
      ['Mark as met', true],
      ['Mark as not met', false],
      ['Waive...', true],
      ['Reopen', false],
    ]);
  });
});
