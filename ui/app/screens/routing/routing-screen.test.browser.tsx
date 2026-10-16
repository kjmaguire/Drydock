import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RouteRule } from '../../api/extTypes.ts';
import type { Provider } from '../../api/types.ts';
import { byLabel, byText, cleanup, errorOf, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { PRIORITY_ERROR } from './rules-panel.tsx';
import { RoutingScreen } from './routing-screen.tsx';

afterEach(cleanup);

const rule: RouteRule = { id: 'r1', scope: 'workspace', priority: 5, when: 'true', then: { prefer_tier: 'cheap' }, enabled: true, version: 3 };

const need = (r: { $: (sel: string) => Element | null }, sel: string) => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
};
const has = (r: { container: Element }, s: string | RegExp) => () => {
  const txt = r.container.textContent ?? '';
  if (typeof s === 'string' ? !txt.includes(s) : !s.test(txt)) throw new Error(`no text ${String(s)}`);
};

function mountWith(client: Partial<Client>) {
  return mount(<RoutingScreen />, testServices({ routeRulesList: async () => [], providerList: async () => [], ...client }));
}

describe('RoutingScreen', () => {
  it('lists rules in priority order with readable actions', async () => {
    const r = mountWith({ routeRulesList: async () => [{ ...rule, id: 'low', priority: 1 }, rule, { ...rule, id: 'r0', priority: 5, enabled: false }] });
    const rows = await waitFor(() => {
      const x = Array.from(r.$$('[data-testid="route-rule"]'));
      if (x.length !== 3) throw new Error('not yet');
      return x;
    }, r);
    expect(rows.map((x) => x.getAttribute('data-rule'))).toEqual(['r0', 'r1', 'low']);
    expect(rows[1]!.textContent).toContain('prefer tier: cheap');
    expect(rows[1]!.textContent).toContain('{"prefer_tier":"cheap"}');
    expect(rows[1]!.textContent).toContain('workspace, priority 5, v3');
    expect(rows[0]!.textContent).toContain('disabled');
  });

  it('saves a rule against its version and shows a stale-version refusal verbatim', async () => {
    const calls: RouteRule[] = [];
    const r = mountWith({
      routeRulesList: async () => [rule],
      routeRulesSet: async (x) => {
        calls.push(x);
        throw new Error('rule r1 changed since you read it (version 3 is stale): reload and retry');
      },
    });
    const row = await waitFor(() => need(r, '[data-testid="route-rule"]'), r);
    await r.act(() => byText(row, 'button', 'Edit').click());
    const ed = await waitFor(() => need(r, '[data-testid="rule-editor"]'), r);
    type(byLabel(ed, /^Priority/), '9');
    await r.act(() => byText(ed, 'button', 'Save rule').click());
    await waitFor(() => expect(calls[0]).toEqual({ ...rule, priority: 9 }), r);
    await waitFor(has(r, /version 3 is stale/), r);
  });

  it('a priority that is not a whole number is told under the field and nothing is sent; it is never saved as 0 (M25)', async () => {
    const calls: RouteRule[] = [];
    const r = mountWith({ routeRulesList: async () => [rule], routeRulesSet: async (x) => (calls.push(x), x) });
    const row = await waitFor(() => need(r, '[data-testid="route-rule"]'), r);
    await r.act(() => byText(row, 'button', 'Edit').click());
    const ed = await waitFor(() => need(r, '[data-testid="rule-editor"]'), r);
    const priority = () => byLabel<HTMLInputElement>(ed, /^Priority/);
    for (const bad of ['high', '1e3', '2.5', '--3', '10 first']) {
      type(priority(), bad);
      await r.act(() => byText(ed, 'button', 'Save rule').click());
      await waitFor(() => expect(errorOf(priority())).toBe(PRIORITY_ERROR), r);
      await waitFor(() => expect(document.activeElement).toBe(priority()), r);
    }
    expect(calls).toEqual([]);
    // a negative whole number is a priority; so is an empty field (0)
    await r.act(() => type(priority(), '-3'));
    expect(errorOf(priority())).toBe(null);
    await r.act(() => byText(ed, 'button', 'Save rule').click());
    await waitFor(() => expect(calls[0]).toEqual({ ...rule, priority: -3 }), r);
  });

  it('refuses an action that is not JSON before calling the core', async () => {
    const calls: RouteRule[] = [];
    const r = mountWith({ routeRulesList: async () => [rule], routeRulesSet: async (x) => (calls.push(x), x) });
    const row = await waitFor(() => need(r, '[data-testid="route-rule"]'), r);
    await r.act(() => byText(row, 'button', 'Edit').click());
    const ed = await waitFor(() => need(r, '[data-testid="rule-editor"]'), r);
    type(byLabel<HTMLTextAreaElement>(ed, /^Then/), '{nope');
    await r.act(() => byText(ed, 'button', 'Save rule').click());
    await waitFor(has(r, /not valid JSON/), r);
    expect(calls.length).toBe(0);
  });

  it('adds a rule, toggles and deletes through the core', async () => {
    const sets: RouteRule[] = [];
    const deleted: string[] = [];
    const r = mountWith({ routeRulesList: async () => [rule], routeRulesSet: async (x) => (sets.push(x), x), routeRulesDelete: async (id) => void deleted.push(id) });
    await waitFor(() => need(r, '[data-testid="route-rule"]'), r);
    await r.act(() => byText(r.container, 'button', 'Add a rule').click());
    const ed = await waitFor(() => need(r, '[data-testid="rule-editor"]'), r);
    // the editor names the turn kinds a rule can match, the routed room replies and template steps among them (P63)
    const hint = ed.querySelector('[data-testid="run-kinds-hint"]')!.textContent!;
    for (const k of ['room_reply', 'spec', 'tests', 'design', 'security_review']) expect(hint).toContain(`'${k}'`);
    type(byLabel<HTMLTextAreaElement>(ed, /^When/), 'task.kind == "docs"');
    type(byLabel<HTMLTextAreaElement>(ed, /^Then/), '{"require_tier":"free"}');
    expect(byLabel<HTMLInputElement>(ed, 'Enabled').checked).toBe(true); // a rule you add is meant to apply
    await r.act(() => byText(ed, 'button', 'Save rule').click());
    await waitFor(() => expect(sets[0]).toEqual({ id: '', scope: 'workspace', priority: 0, when: 'task.kind == "docs"', then: { require_tier: 'free' }, enabled: true, version: 0 }), r);
    await waitFor(() => expect(r.$('[data-testid="rule-editor"]')).toBeNull(), r);
    const row = need(r, '[data-testid="route-rule"]');
    await r.act(() => byText(row, 'button', 'Disable').click());
    await waitFor(() => expect(sets[1]).toEqual({ ...rule, enabled: false }), r);
    await r.act(() => byText(need(r, '[data-testid="route-rule"]'), 'button', 'Delete').click());
    await r.act(() => byText(need(r, '[data-testid="route-rule"]'), 'button', 'Yes, delete the rule "r1"').click());
    await waitFor(() => expect(deleted).toEqual(['r1']), r);
  });

  // audit 2026-10-16, L40: deleting a rule cannot be undone, so it asks first
  it('deleting a rule asks first: one click sends nothing, Keep cancels, Yes deletes it once', async () => {
    const deleted: string[] = [];
    let listed = [rule, { ...rule, id: 'r2', priority: 4 }];
    const r = mountWith({
      routeRulesList: async () => listed,
      routeRulesDelete: async (id) => {
        deleted.push(id);
        listed = listed.filter((x) => x.id !== id);
      },
    });
    const row = (id: string) => need(r, `[data-testid="route-rule"][data-rule="${id}"]`);
    await waitFor(() => row('r1'), r);
    await r.act(() => byText(row('r1'), 'button', 'Delete').click());
    // the question is asked on that rule only, with names that say which rule and what happens
    expect(deleted).toEqual([]);
    expect(row('r1').textContent).not.toContain('Delete');
    expect(row('r2').textContent).toContain('Delete');
    expect(row('r1').querySelector('[role="group"]')!.getAttribute('aria-label')).toBe('Delete the rule "r1"? It stops applying at once and cannot be brought back.');
    expect(byText(row('r1'), 'button', 'Keep the rule "r1"')).toBeTruthy();
    // Keep: nothing was sent and the rule is as before
    await r.act(() => byText(row('r1'), 'button', 'Keep the rule "r1"').click());
    await waitFor(() => byText(row('r1'), 'button', 'Delete'), r);
    expect(deleted).toEqual([]);
    // Yes: sent once, even when pressed twice, and only for that rule
    await r.act(() => byText(row('r1'), 'button', 'Delete').click());
    const yes = byText<HTMLButtonElement>(row('r1'), 'button', 'Yes, delete the rule "r1"');
    await r.act(() => {
      yes.click();
      yes.click();
    });
    await waitFor(() => expect(deleted).toEqual(['r1']), r);
    await waitFor(() => expect(r.$$('[data-testid="route-rule"]').length).toBe(1), r);
    expect(row('r2')).toBeTruthy();
  });

  it('edits the fallback chain of a rule in its own field and shows it on the rule', async () => {
    const sets: RouteRule[] = [];
    const chained: RouteRule = { ...rule, when: "candidate.tier == 'cheap'", then: { prefer_tier: 'cheap', fallback: ['sonnet', 'haiku-b'] } };
    const r = mountWith({ routeRulesList: async () => [chained], routeRulesSet: async (x) => (sets.push(x), x) });
    const row = await waitFor(() => need(r, '[data-testid="route-rule"]'), r);
    expect(row.textContent).toContain('fallback: sonnet → haiku-b');
    await r.act(() => byText(row, 'button', 'Edit').click());
    const ed = await waitFor(() => need(r, '[data-testid="rule-editor"]'), r);
    const field = byLabel<HTMLInputElement>(ed, /^Fallback chain/);
    expect(field.value).toBe('sonnet, haiku-b');
    expect(byLabel<HTMLTextAreaElement>(ed, /^Then/).value.includes('fallback')).toBe(false); // one place to edit it
    type(field, 'haiku-c, sonnet');
    await r.act(() => byText(ed, 'button', 'Save rule').click());
    await waitFor(() => expect(sets[0]).toEqual({ ...chained, then: { prefer_tier: 'cheap', fallback: ['haiku-c', 'sonnet'] } }), r);
    // emptied, the rule names no chain: its turns do not fall back
    await waitFor(() => expect(r.$('[data-testid="rule-editor"]')).toBeNull(), r);
    await r.act(() => byText(need(r, '[data-testid="route-rule"]'), 'button', 'Edit').click());
    const ed2 = await waitFor(() => need(r, '[data-testid="rule-editor"]'), r);
    type(byLabel<HTMLInputElement>(ed2, /^Fallback chain/), '');
    await r.act(() => byText(ed2, 'button', 'Save rule').click());
    await waitFor(() => expect(sets[1]).toEqual({ ...chained, then: { prefer_tier: 'cheap' } }), r);
  });

  it('a preview that is blocked says why; a dry run says nothing was recorded', async () => {
    const asked: unknown[] = [];
    const r = mountWith({
      routePreview: async (req) => (
        asked.push(req),
        { blocked: true, reason: 'no private provider has quota', alternatives: [{ Provider: 'p', Model: 'm', Tier: 'cheap', Rejected: 'quota', Detail: 'exhausted' }] }
      ),
    });
    await r.act(() => byText(r.container, 'button', 'Preview').click());
    const out = await waitFor(() => need(r, '[data-testid="route-preview"]'), r);
    expect(out.getAttribute('data-blocked')).toBe('true');
    expect(out.textContent).toContain('no private provider has quota');
    expect(out.textContent).toContain('p/m rejected: quota exhausted');
    expect(out.textContent).toContain('Recorded: no');
    expect(asked[0]).toEqual({ RunKind: 'agent_task', Level: 'balanced', Role: 'backend', ContextCeiling: 'private' });
  });

  it('a preview sends the chosen inputs and shows the decision with its explanation', async () => {
    const asked: unknown[] = [];
    const r = mountWith({
      routePreview: async (req) => (
        asked.push(req),
        { blocked: false, decision: { ID: '', Provider: 'or', Model: 'x/y', Effort: '', Reason: '', Confidence: 1, EstCostUSD: null, DecidedBy: 'rules' }, explain: '- Rule r1 applied' }
      ),
    });
    type(byLabel<HTMLElement>(r.container, 'Run kind'), 'review');
    type(byLabel<HTMLElement>(r.container, 'Level'), 'quality');
    type(byLabel(r.container, 'Role'), 'reviewer');
    type(byLabel<HTMLElement>(r.container, 'Context'), 'public_docs');
    await r.act(() => byText(r.container, 'button', 'Preview').click());
    const out = await waitFor(() => need(r, '[data-testid="route-preview"]'), r);
    expect(asked[0]).toEqual({ RunKind: 'review', Level: 'quality', Role: 'reviewer', ContextCeiling: 'public_docs' });
    expect(out.getAttribute('data-blocked')).toBe('false');
    expect(out.textContent).toContain('Would use or/x/y (effort not set), estimated cost price unknown.');
    expect(out.textContent).toContain('Rule r1 applied');
  });

  it('the startup probe warns about cost and is only changed by a button', async () => {
    const calls: boolean[] = [];
    const r = mountWith({ routeProbeStartup: async (enabled) => (calls.push(enabled), { enabled, by: 'human:kyle' }) });
    await waitFor(() => byText(r.container, 'button', 'Change the startup probe...'), r);
    await r.act(() => byText(r.container, 'button', 'Change the startup probe...').click());
    await waitFor(has(r, 'every time Drydock starts. That can cost a little.'), r);
    expect(calls.length).toBe(0);
    await r.act(() => byText(r.container, 'button', 'Turn on').click());
    await waitFor(() => expect(calls).toEqual([true]), r);
    await waitFor(has(r, 'Startup probe: on (set in this session).'), r);
  });

  it('cheap selection lists cheap private providers, shows the stored choice and probes on a button', async () => {
    const prov = (over: Partial<Provider>) => ({ id: 'p1', name: 'Cheap one', tier: 'cheap', dataClass: 'private', enabled: true, ...over }) as Provider;
    const selects: string[] = [];
    const r = mountWith({
      providerList: async () => [prov({}), prov({ id: 'p2', name: 'Trainable', dataClass: 'trainable' })],
      routeCheapGet: async () => ({ selection: { providerId: 'p1', default: 'a/b', fallback: '', message: 'picked last week' }, selected: true }),
      routeCheapSelect: async (id) => (selects.push(id), { providerId: id, default: 'c/d', fallback: 'e/f', message: 'fresh pick' }),
    });
    const li = await waitFor(() => {
      const x = Array.from(r.$$('[data-testid="cheap-provider"]'));
      if (x.length !== 1 || !x[0]!.querySelector('[data-testid="cheap-selection"]')) throw new Error('not yet');
      return x[0]!;
    }, r);
    expect(li.textContent).toContain('Cheap one');
    expect(li.textContent).toContain('default a/b, fallback none');
    await r.act(() => byText(li, 'button', 'Probe and select now').click());
    await waitFor(() => expect(need(r, '[data-testid="cheap-selection"]').textContent).toContain('default c/d, fallback e/f'), r);
    expect(selects).toEqual(['p1']);
  });

  it('says when there is no cheap private provider', async () => {
    const r = mountWith({});
    await waitFor(has(r, 'No enabled cheap private provider.'), r);
  });
});
