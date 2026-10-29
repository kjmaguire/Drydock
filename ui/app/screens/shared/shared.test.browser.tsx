import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { BudgetPausePayload } from '../../api/extTypes.ts';
import { budgetStatus, expiredLicense, usage } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, errorOf, mount, renderPlain, testServices, type, waitFor } from '../../test/render.tsx';
import { TOKENS_ERROR, TURNS_ERROR, USD_ERROR } from '../budgets/budgets.ts';
import { BudgetPausePanel } from './budget-pause.tsx';
import { ExplainText, TaskDecisions } from './explain.tsx';
import { UsageTable, distinctSources } from './usage.tsx';

afterEach(cleanup);

describe('UsageTable', () => {
  it('usage rows carry a source badge and an unreported cost is never 0', () => {
    const r = renderPlain(<UsageTable rows={[usage({}), usage({ ts: 2, costUsd: 0, source: 'estimated' }), usage({ ts: 3, source: 'weird' })]} />);
    const rows = r.$$('[data-testid="usage-row"]');
    expect(rows[0]!.textContent).toContain('$0.01');
    expect(rows[0]!.textContent).toContain('reported');
    expect(rows[1]!.textContent).toContain('not reported');
    expect(rows[1]!.textContent).toContain('estimated');
    expect(rows[1]!.textContent).not.toContain('$0');
    expect(rows[2]!.textContent).toContain('weird');
    expect(distinctSources([usage({}), usage({ source: 'estimated' }), usage({})])).toEqual(['estimated', 'reported']);
  });
});

describe('BudgetPausePanel', () => {
  it('resumes the exact request with raised limits, or stops the run', async () => {
    const calls: unknown[] = [];
    let done = 0;
    const s = testServices({ budgetResume: async (p) => (calls.push(p), { runId: 'r', requestId: 'q', decision: 'approved', raised: ['bud_1'], by: 'human:kyle' }) as never });
    const payload: BudgetPausePayload = { gate: 'budget', message: 'Budget limit reached: project prj_1 at 100%.', exceeded: [budgetStatus({ state: 'exceeded', pct: 1.1 })] };
    const r = mount(<BudgetPausePanel payload={payload} runId="run-9" requestId="req-3" onDone={() => done++} />, s);
    expect(r.container.textContent).toContain('Budget limit reached');
    type(byLabel(r.container, 'New USD limit'), '10');
    await r.act(() => byText(r.container, 'button', 'Resume').click());
    await waitFor(() => expect(calls[0]).toEqual({ runId: 'run-9', requestId: 'req-3', decision: 'approved', limitUsd: 10, limitTokens: undefined, limitTurns: undefined }), r);
    await waitFor(() => expect(done).toBe(1), r);
    await r.act(() => byText(r.container, 'button', 'Stop the run').click());
    await waitFor(() => expect(calls[1]).toEqual({ runId: 'run-9', requestId: 'req-3', decision: 'rejected', limitUsd: undefined, limitTokens: undefined, limitTurns: undefined }), r);
  });

  it('a new limit that is not a plain number is told under its field and the run is not resumed; blank fields still resume with one more turn (M25)', async () => {
    const calls: unknown[] = [];
    const s = testServices({ budgetResume: async (p) => (calls.push(p), { runId: 'r', requestId: 'q', decision: 'approved', raised: [], by: 'human:kyle' }) as never });
    const payload: BudgetPausePayload = { gate: 'budget', message: 'Budget limit reached: project prj_1 at 100%.', exceeded: [budgetStatus({ state: 'exceeded', pct: 1.1 })] };
    let done = 0;
    const r = mount(<BudgetPausePanel payload={payload} runId="run-9" requestId="req-3" onDone={() => done++} />, s);
    const usd = () => byLabel<HTMLInputElement>(r.container, 'New USD limit');
    const tokens = () => byLabel<HTMLInputElement>(r.container, 'New token limit');
    const turns = () => byLabel<HTMLInputElement>(r.container, 'New turn limit');
    for (const [field, bad, message] of [
      [usd, '5 dollars', USD_ERROR],
      [usd, '$5', USD_ERROR],
      [usd, '1,000', USD_ERROR],
      [usd, '1e999', USD_ERROR],
      [usd, 'abc', USD_ERROR],
      [tokens, '1e6', TOKENS_ERROR],
      [turns, '2.5', TURNS_ERROR],
    ] as const) {
      for (const f of [usd, tokens, turns]) type(f(), '');
      type(field(), bad);
      await r.act(() => byText(r.container, 'button', 'Resume').click());
      await waitFor(() => expect(errorOf(field())).toBe(message), r);
      await waitFor(() => expect(document.activeElement).toBe(field()), r);
      for (const other of [usd, tokens, turns].filter((f) => f !== field)) expect(errorOf(other())).toBe(null);
    }
    expect(calls).toEqual([]);
    expect(done).toBe(0);
    // stopping the run asks nothing of the fields: it is always possible, and sends no limit
    await r.act(() => byText(r.container, 'button', 'Stop the run').click());
    await waitFor(() => expect(calls).toEqual([{ runId: 'run-9', requestId: 'req-3', decision: 'rejected', limitUsd: undefined, limitTokens: undefined, limitTurns: undefined }]), r);
    // blank fields still resume without a new limit (exactly one more turn); a plain number is sent as it is
    for (const f of [usd, tokens, turns]) type(f(), '');
    await r.act(() => byText(r.container, 'button', 'Resume').click());
    await waitFor(() => expect(calls[1]).toEqual({ runId: 'run-9', requestId: 'req-3', decision: 'approved', limitUsd: undefined, limitTokens: undefined, limitTurns: undefined }), r);
    type(usd(), '12.50');
    type(tokens(), ' 500000 ');
    await r.act(() => byText(r.container, 'button', 'Resume').click());
    await waitFor(() => expect(calls[2]).toEqual({ runId: 'run-9', requestId: 'req-3', decision: 'approved', limitUsd: 12.5, limitTokens: 500000, limitTurns: undefined }), r);
    expect(errorOf(usd())).toBe(null);
  });

  it('read-only turns off approving but never refusing: Stop the run stays available', async () => {
    const calls: { decision?: string }[] = [];
    const s = testServices({ licenseStatus: async () => expiredLicense, projectList: async () => [], appInfo: async () => ({}) as never, budgetResume: async (p) => (calls.push(p), {}) as never });
    s.workspace.start();
    const r = mount(<BudgetPausePanel payload={{ gate: 'budget', message: 'Budget limit reached.', exceeded: [] }} runId="run-1" requestId="req-1" onDone={() => undefined} />, s);
    await waitFor(() => expect(byText<HTMLButtonElement>(r.container, 'button', 'Resume').disabled).toBe(true), r);
    const stop = byText<HTMLButtonElement>(r.container, 'button', 'Stop the run');
    expect(stop.disabled).toBe(false);
    await r.act(() => stop.click());
    await waitFor(() => expect(calls[0]!.decision).toBe('rejected'), r);
  });
});

describe('routing decisions', () => {
  it('unknown price is "price unknown", static is labelled, Explain loads the router text', async () => {
    const asked: string[] = [];
    const s = testServices({
      routeDecisions: async () => [
        { id: 'dec_1', ts: 1, attempt: 1, runKind: 'agent_task', level: 'balanced', agent: 'a', provider: 'claude', model: '', effort: '', decidedBy: 'static', estCostUsd: null },
        { id: 'dec_2', ts: 2, attempt: 1, runKind: 'review', level: 'balanced', agent: 'a', provider: 'or', model: 'x/y', effort: 'low', decidedBy: 'rules', estCostUsd: 0.0012, outcome: { outcome: 'completed', accepted: true, escalated: false, iterations: 1, costUsd: 0.001, costSource: 'reported' } },
      ] as never,
      routeExplain: async (id: string) => (
        asked.push(id),
        {
          text: '- Chosen: or/x/y\n- Rule r1 applied\n<img src=x onerror=alert(1)>',
          decision: { ID: 'dec_2', Provider: 'or', Model: 'x/y', Effort: 'low', Reason: 'cheap first', Confidence: 0.8, EstCostUSD: 0.0012, DecidedBy: 'rules', Alternatives: [{ Provider: 'q', Model: 'z', Tier: 'premium', Rejected: 'r2', Detail: 'costs more' }] },
        } as never
      ),
    });
    const r = mount(<TaskDecisions taskId="t1" />, s);
    const rows = await waitFor(() => {
      const x = r.$$('[data-testid="decision-row"]');
      if (x.length !== 2) throw new Error('not yet');
      return x;
    }, r);
    expect(rows[0]!.textContent).toContain('price unknown');
    expect(rows[0]!.textContent).toContain('static');
    expect(rows[1]!.textContent).toContain('$0.0012');
    await r.act(() => byText(rows[1]!, 'button', 'Explain why this model').click());
    await waitFor(() => r.$('[data-testid="explain"]') ?? (() => { throw new Error('no explain'); })(), r);
    expect(asked).toEqual(['dec_2']);
    expect(r.container.querySelector('img')).toBeNull();
    expect(r.container.textContent).toContain('alternative not chosen');
  });

  it('names the kind of turn of each decision: work, review, room reply and the template steps', async () => {
    const row = (id: string, runKind: string) => ({ id, ts: 1, attempt: 1, runKind, level: 'balanced', agent: 'a', provider: 'agent:claude', model: 'haiku', effort: '', decidedBy: 'rules', estCostUsd: null });
    const kinds = ['agent_task', 'review', 'room_reply', 'spec', 'tests', 'design', 'security_review', 'lead', 'recommend'];
    const s = testServices({ routeDecisions: async () => kinds.map((k, i) => row('dec_' + i, k)) as never });
    const r = mount(<TaskDecisions taskId="t1" />, s);
    const rows = await waitFor(() => {
      const x = r.$$('[data-testid="decision-row"]');
      if (x.length !== kinds.length) throw new Error('not yet');
      return x;
    }, r);
    expect([...rows].map((x) => x.querySelector('td')!.textContent!.replace(/attempt \d+/, '').trim())).toEqual([
      'work',
      'review',
      'room reply',
      'spec (pm)',
      'tests first',
      'design (architect)',
      'security review',
      'team lead',
      'recommendation',
    ]);
    expect([...rows].map((x) => x.getAttribute('data-run-kind'))).toEqual(kinds);
  });

  it('says when a tiebreak chose the model and when Drydock stopped the turn (P112)', async () => {
    const row = { ts: 1, attempt: 1, runKind: 'agent_task', level: 'balanced', agent: 'a', provider: 'agent:claude', model: 'sonnet', effort: '', estCostUsd: null };
    const s = testServices({
      routeDecisions: async () =>
        [
          { ...row, id: 'd1', decidedBy: 'llm', tiebreak: 'tiebreak by or/tiny chose agent:claude/sonnet over agent:claude/haiku' },
          { ...row, id: 'd2', decidedBy: 'rules', stopReason: 'the turn ran past the 30m wall-time ceiling of the balanced level' },
        ] as never,
    });
    const r = mount(<TaskDecisions taskId="t1" />, s);
    const rows = await waitFor(() => {
      const x = r.$$('[data-testid="decision-row"]');
      if (x.length !== 2) throw new Error('not yet');
      return x;
    }, r);
    expect(rows[0]!.textContent).toContain('llm tiebreak');
    expect(rows[0]!.querySelector('[data-testid="decision-tiebreak"]')!.textContent).toContain('chose agent:claude/sonnet over agent:claude/haiku');
    expect(rows[0]!.querySelector('[data-testid="decision-stopped"]')).toBeNull();
    expect(rows[1]!.querySelector('[data-testid="decision-stopped"]')!.textContent).toContain('stopped the turn ran past the 30m wall-time ceiling');
  });

  it('a static explanation says the router did not choose', async () => {
    const s = testServices({ routeExplain: async () => ({ text: '- Decided by: static assignment.', decision: { ID: 'd', Provider: 'claude', Model: '', Effort: '', Reason: '', Confidence: 1, EstCostUSD: null, DecidedBy: 'static' } }) as never });
    const r = mount(<ExplainText decisionId="d" />, s);
    await waitFor(() => expect(r.container.textContent).toContain('did not choose it'), r);
  });
});
