import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RouteRule } from '../../api/extTypes.ts';
import type { Provider } from '../../api/types.ts';
import { byLabel, byText, cleanup, errorOf, fromMenu, menuItems, mount, testServices, type, waitFor } from '../../test/render.tsx';
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
  return mount(
    <RoutingScreen />,
    testServices({ routeRulesList: async () => [], providerList: async () => [], routePreviews: async (reqs) => reqs.map(() => ({ blocked: true, reason: 'no providers' })), ...client }),
  );
}

/** Choose an action from a rule's "..." menu (Edit, Disable or Enable, Delete). */
const pick = (r: ReturnType<typeof mountWith>, row: Element, item: string) => fromMenu(r, row.querySelector('[data-testid="route-rule-more"]') as HTMLElement, item);

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
    // the priority is the number at the left, shown once; the action is shown as words, its exact JSON is the badge's tooltip
    expect(rows[1]!.querySelector('[title="priority 5"]')!.textContent).toBe('5');
    expect(rows[1]!.textContent).toContain('workspace, v3');
    expect(rows[1]!.textContent).not.toContain('priority 5,');
    expect(rows[1]!.textContent).not.toContain('{"prefer_tier"');
    expect(Array.from(rows[1]!.querySelectorAll('[title]')).some((n) => n.getAttribute('title') === '{"prefer_tier":"cheap"}')).toBe(true);
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
    await pick(r, row, 'Edit');
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
    await pick(r, row, 'Edit');
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
    await pick(r, row, 'Edit');
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
    await pick(r, row, 'Disable');
    await waitFor(() => expect(sets[1]).toEqual({ ...rule, enabled: false }), r);
    await pick(r, need(r, '[data-testid="route-rule"]'), 'Delete');
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
    // one "..." per rule, not three buttons (the UI audit of 2026-10-26)
    expect(Array.from(row('r1').querySelectorAll('button')).filter((b) => /^(Edit|Disable|Delete)$/.test(b.textContent!.trim())).length).toBe(0);
    expect(await menuItems(r, row('r1').querySelector('[data-testid="route-rule-more"]') as HTMLElement)).toEqual(['Edit', 'Disable', 'Delete']);
    await pick(r, row('r1'), 'Delete');
    // the question is asked on that rule only, with names that say which rule and what happens
    expect(deleted).toEqual([]);
    expect(row('r1').querySelector('[data-testid="route-rule-more"]')).toBeNull();
    expect(row('r2').querySelector('[data-testid="route-rule-more"]')).not.toBeNull();
    expect(row('r1').querySelector('[role="group"]')!.getAttribute('aria-label')).toBe('Delete the rule "r1"? It stops applying at once and cannot be brought back.');
    expect(byText(row('r1'), 'button', 'Keep the rule "r1"')).toBeTruthy();
    // Keep: nothing was sent and the rule is as before
    await r.act(() => byText(row('r1'), 'button', 'Keep the rule "r1"').click());
    await waitFor(() => expect(row('r1').querySelector('[data-testid="route-rule-more"]')).not.toBeNull(), r);
    expect(deleted).toEqual([]);
    // Yes: sent once, even when pressed twice, and only for that rule
    await pick(r, row('r1'), 'Delete');
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
    await pick(r, row, 'Edit');
    const ed = await waitFor(() => need(r, '[data-testid="rule-editor"]'), r);
    const field = byLabel<HTMLInputElement>(ed, /^Fallback chain/);
    expect(field.value).toBe('sonnet, haiku-b');
    expect(byLabel<HTMLTextAreaElement>(ed, /^Then/).value.includes('fallback')).toBe(false); // one place to edit it
    type(field, 'haiku-c, sonnet');
    await r.act(() => byText(ed, 'button', 'Save rule').click());
    await waitFor(() => expect(sets[0]).toEqual({ ...chained, then: { prefer_tier: 'cheap', fallback: ['haiku-c', 'sonnet'] } }), r);
    // emptied, the rule names no chain: its turns do not fall back
    await waitFor(() => expect(r.$('[data-testid="rule-editor"]')).toBeNull(), r);
    await pick(r, need(r, '[data-testid="route-rule"]'), 'Edit');
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

describe('the wiring diagram (decision P108)', () => {
  // TEST DOUBLE of route.previews: a model per level of agent work, the cheap one for the app's own small calls
  const byLevel: Record<string, string> = { cheapest: 'claude-haiku-5-5', fast: 'claude-sonnet-5-5', balanced: 'claude-opus-5-5', quality: 'claude-opus-5-5', maximum_quality: 'claude-fable-5-1' };
  const preview = async (req: Record<string, unknown>) => {
    if (req.RunKind === 'classify') return { blocked: true, reason: 'denied by rule r9' };
    if (req.RunKind === 'consult') return { error: 'the rules could not be read' };
    const model = req.RunKind === 'agent_task' ? byLevel[req.Level as string]! : req.RunKind === 'summarize' ? 'claude-haiku-5-5' : 'claude-opus-5-5';
    return { blocked: false, decision: { ID: 'd', Provider: 'anthropic', Model: model, Effort: req.Level === 'maximum_quality' ? 'max' : '', Reason: `${req.Level} for ${req.RunKind}`, Confidence: 1, EstCostUSD: null, DecidedBy: 'router' } };
  };

  it("names each level's and each purpose's model, wires every card to the router, and asks only dry runs", async () => {
    const asked: Record<string, unknown>[][] = [];
    const r = mountWith({ routePreviews: async (reqs) => (asked.push(reqs), Promise.all(reqs.map(preview))) as never });
    const cards = await waitFor(() => {
      const xs = Array.from(r.$$('[data-testid="wire-card"]')) as HTMLElement[];
      if (xs.length === 0 || xs[0]!.querySelector('[data-testid="wire-model"]')!.textContent === '…') throw new Error('not yet');
      return xs;
    }, r);
    const model = (key: string) => cards.find((c) => c.dataset.key === key)!.querySelector('[data-testid="wire-model"]')!.textContent;
    expect(['level-cheapest', 'level-fast', 'level-balanced', 'level-maximum_quality'].map(model)).toEqual(['Haiku 5.5', 'Sonnet 5.5', 'Opus 5.5', 'Fable 5.1']);
    expect(model('kind-summarize')).toBe('Haiku 5.5');
    expect(cards.find((c) => c.dataset.key === 'level-maximum_quality')!.textContent).toContain('anthropic · max effort');
    // a purpose a rule denies says so on its card, with the router's reason as its tooltip
    const denied = cards.find((c) => c.dataset.key === 'kind-classify')!;
    expect([denied.dataset.blocked, model('kind-classify'), denied.getAttribute('title')]).toEqual(['true', 'Blocked', 'denied by rule r9']);
    // one wire per card into the router node; the groups are headed
    expect(r.$$('[data-testid="wire-lines"] path').length).toBe(cards.length);
    expect(r.$('[data-testid="wire-node"]')!.textContent).toContain('Router');
    expect(Array.from(r.$$('[data-testid="wire-group"]')).map((g) => g.textContent)).toEqual(['Levelsagent work', 'Purposesbalanced']);
    // one call for every card (a page that asks fourteen times trips the smoke test's guard), with the Preview panel's defaults
    expect(asked).toHaveLength(1);
    expect(asked[0]).toHaveLength(cards.length);
    expect(asked[0]!.every((q) => q.Role === 'backend' && q.ContextCeiling === 'private')).toBe(true);
    // an answer the core could not give is that card's alone
    expect([model('kind-consult'), cards.find((c) => c.dataset.key === 'kind-consult')!.getAttribute('title')]).toEqual(['Not known', 'the rules could not be read']);
    // not one blocked reason for all: no notice
    expect(r.$('[data-testid="wire-blocked"]')).toBeNull();
  });

  it('says once, above the cards, that every turn is blocked and where to add a provider', async () => {
    const r = mountWith({});
    const note = await waitFor(() => need(r, '[data-testid="wire-blocked"]'), r);
    expect(note.textContent).toContain('Every turn is blocked: no providers.');
    expect(note.querySelector('a')!.getAttribute('href')).toBe('/models');
  });

  it('reads the answers again when a rule changes', async () => {
    let calls = 0;
    const svc = testServices({ routeRulesList: async () => [], providerList: async () => [], routePreviews: async (reqs) => (calls++, Promise.all(reqs.map(preview))) as never });
    const r = mount(<RoutingScreen />, svc);
    await waitFor(() => expect(calls).toBeGreaterThan(0), r);
    await waitFor(() => need(r, '[data-testid="wire-card"][data-key="level-cheapest"] [data-testid="wire-model"]').textContent === 'Haiku 5.5' || (() => { throw new Error('not yet'); })(), r);
    const before = calls;
    svc.emit({ type: 'routing.rule_changed' });
    await waitFor(() => expect(calls).toBeGreaterThan(before), r);
  });
});
