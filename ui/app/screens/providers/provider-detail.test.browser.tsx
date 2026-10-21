import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { ProviderDetail, ProviderEdit } from '../../api/providerClient.ts';
import { byLabel, byText, cleanup, drain, errorOf, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { MAX_CONCURRENT_ERROR, ProviderDetailPanel } from './provider-detail.tsx';

afterEach(cleanup);

const detail = (): ProviderDetail => ({
  provider: { id: 'prov_1', name: 'Fixture', kind: 'direct', baseUrl: 'https://api.example.com/v1', keyRef: 'provider/prov_1/api-key', dataClass: 'private', tier: 'cheap', maxConcurrent: 0 } as never,
  models: [{ providerId: 'prov_1', id: 'model-a', inputPerM: 0.5, outputPerM: 1.5, priceSource: 'catalog' } as never],
  quota: [{ kind: 'rpd', model: 'model-a', limit: 250, used: 10, source: 'configured', updatedAt: 1 }],
});

const need = (r: { $: (sel: string) => Element | null }, sel: string) => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
};

describe('ProviderDetailPanel', () => {
  it('edits settings, a price and a quota limit through the core, and never shows the key', async () => {
    const updates: ProviderEdit[] = [];
    const prices: unknown[][] = [];
    const quotas: unknown[][] = [];
    let changed = 0;
    const s = testServices({
      providerDetail: async () => detail(),
      providerUpdate: async (e) => (updates.push(e), {}) as never,
      providerSetPrice: async (...a) => void prices.push(a),
      providerSetQuota: async (...a) => void quotas.push(a),
    });
    const r = mount(<ProviderDetailPanel id="prov_1" onChanged={() => changed++} />, s);
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    expect(r.container.textContent).not.toContain('provider/prov_1/api-key');
    expect(r.container.innerHTML).not.toContain('provider/prov_1/api-key');
    expect(need(r, '[data-testid="quota-window"]').textContent).toContain('10 of 250 used');
    expect(need(r, '[data-testid="provider-detail"]').getAttribute('aria-label')).toBe('Fixture details');

    type(byLabel(r.container, 'Name'), 'Renamed');
    const key = byLabel(r.container, /Replace the API key/);
    expect(key.type).toBe('password');
    type(key, 'sk-new');
    await r.act(() => byText(r.container, 'button', 'Save settings').click());
    await waitFor(() => expect(updates[0]).toMatchObject({ id: 'prov_1', name: 'Renamed', apiKey: 'sk-new', tier: 'cheap', dataClass: 'private', maxConcurrent: 0 }), r);
    expect(key.value).toBe('');
    expect(r.container.innerHTML).not.toContain('sk-new');
    await waitFor(() => expect(changed).toBe(1), r);

    await r.act(() => byText(r.container, 'button', 'Edit price').click());
    type(byLabel(r.container, 'Input price of model-a'), '2');
    type(byLabel(r.container, 'Output price of model-a'), '6');
    await r.act(() => byText(r.container, 'button', 'Save price').click());
    await waitFor(() => expect(prices[0]).toEqual(['prov_1', 'model-a', 2, 6, undefined, undefined]), r);

    type(byLabel<HTMLElement>(r.container, 'Quota window'), 'rpm');
    type(byLabel(r.container, 'Quota limit'), '30');
    await r.act(() => byText(r.container, 'button', 'Set limit').click());
    await waitFor(() => expect(quotas[0]).toEqual(['prov_1', '', 'rpm', 30]), r);
  });

  it('a price that is not a number is not sent; a non-positive quota limit is not sent', async () => {
    const prices: unknown[][] = [];
    const quotas: unknown[][] = [];
    const r = mount(
      <ProviderDetailPanel id="prov_1" onChanged={() => undefined} />,
      testServices({ providerDetail: async () => detail(), providerSetPrice: async (...a) => void prices.push(a), providerSetQuota: async (...a) => void quotas.push(a) }),
    );
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    await r.act(() => byText(r.container, 'button', 'Edit price').click());
    type(byLabel(r.container, 'Input price of model-a'), 'abc');
    await r.act(() => byText(r.container, 'button', 'Save price').click());
    type(byLabel(r.container, 'Quota limit'), '0');
    await r.act(() => byText(r.container, 'button', 'Set limit').click());
    // the values are checked in the click handlers; a call would reach the double from there or its promise chain
    await drain();
    expect(prices.length).toBe(0);
    expect(quotas.length).toBe(0);
    expect(byLabel(r.container, 'Input price of model-a')).toBeTruthy();
    // and a valid limit, the same way, is sent: the check above would have seen a call
    type(byLabel(r.container, 'Quota limit'), '5');
    await r.act(() => byText(r.container, 'button', 'Set limit').click());
    await drain();
    expect(quotas).toEqual([['prov_1', '', 'rpd', 5]]); // the form's default window
  });

  it('an OpenRouter provider shows the endpoints its calls skip and saves the uptime threshold', async () => {
    const updates: ProviderEdit[] = [];
    const orDetail = (): ProviderDetail => ({
      ...detail(),
      provider: { ...detail().provider, name: 'OpenRouter', flavor: 'openrouter', kind: 'aggregator', baseUrl: 'https://openrouter.ai/api/v1' } as never,
      endpointUptime: {
        minUptime: 98,
        defaultMinUptime: 98,
        avoiding: [
          {
            providerId: 'prov_1',
            model: 'acme/coder-7b',
            threshold: 98,
            keptBest: false,
            at: '2026-10-15T09:00:00Z',
            avoided: [{ tag: 'flaky', name: 'Upstream Flaky', uptimePct: 91.2, window: '30m' }],
          },
          {
            providerId: 'prov_1',
            model: 'acme/other',
            threshold: 98,
            keptBest: true,
            at: '2026-10-15T08:59:00Z',
            avoided: [{ tag: 'worst', uptimePct: 40, window: '5m' }],
          },
        ],
      },
    });
    const r = mount(
      <ProviderDetailPanel id="prov_1" onChanged={() => undefined} />,
      testServices({ providerDetail: async () => orDetail(), providerUpdate: async (e) => (updates.push(e), {}) as never }),
    );
    await waitFor(() => need(r, '[data-testid="endpoint-uptime"]'), r);
    const rows = Array.from(r.container.querySelectorAll('[data-testid="endpoint-avoidance"]'));
    expect(rows.length).toBe(2);
    const [first, second] = rows;
    expect(first?.getAttribute('data-model')).toBe('acme/coder-7b');
    expect(first?.textContent).toContain('flaky (Upstream Flaky): 91.2% over the last 30 minutes');
    expect(first?.textContent).not.toContain('best kept');
    expect(second?.textContent).toContain('all below 98%: best kept');
    expect(second?.textContent).toContain('worst: 40% over the last 5 minutes');

    const field = byLabel(r.container, /Skip endpoints below this recent uptime/);
    expect(field.value).toBe('98');
    type(field, '90');
    await r.act(() => byText(r.container, 'button', 'Save settings').click());
    await waitFor(() => expect(updates[0]).toMatchObject({ id: 'prov_1', minEndpointUptime: 90 }), r);
  });

  it('a concurrency limit that is not a whole number is told under the field and nothing is sent, the typed key stays; empty is unlimited (M25)', async () => {
    const updates: ProviderEdit[] = [];
    const r = mount(
      <ProviderDetailPanel id="prov_1" onChanged={() => undefined} />,
      testServices({ providerDetail: async () => detail(), providerUpdate: async (e) => (updates.push(e), {}) as never }),
    );
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    const limit = () => byLabel<HTMLInputElement>(r.container, /^Max concurrent agents/);
    const key = byLabel<HTMLInputElement>(r.container, /Replace the API key/);
    type(key, 'sk-typed-but-not-sent');
    for (const bad of ['many', '2.5', '1e3', '0', '-1', '4 agents']) {
      type(limit(), bad);
      await r.act(() => byText(r.container, 'button', 'Save settings').click());
      await waitFor(() => expect(errorOf(limit())).toBe(MAX_CONCURRENT_ERROR), r);
      await waitFor(() => expect(document.activeElement).toBe(limit()), r);
    }
    expect(updates).toEqual([]);
    // the replacement key was not read, sent or cleared by the refused form
    expect(key.value).toBe('sk-typed-but-not-sent');
    await r.act(() => type(limit(), '4'));
    expect(errorOf(limit())).toBe(null);
    await r.act(() => byText(r.container, 'button', 'Save settings').click());
    await waitFor(() => expect(updates[0]).toMatchObject({ id: 'prov_1', maxConcurrent: 4, apiKey: 'sk-typed-but-not-sent' }), r);
    // empty still means unlimited (0)
    type(limit(), '');
    await r.act(() => byText(r.container, 'button', 'Save settings').click());
    await waitFor(() => expect(updates[1]).toMatchObject({ id: 'prov_1', maxConcurrent: 0 }), r);
  });

  it('other providers have no endpoint uptime section or threshold, and send none', async () => {
    const updates: ProviderEdit[] = [];
    const r = mount(
      <ProviderDetailPanel id="prov_1" onChanged={() => undefined} />,
      testServices({ providerDetail: async () => detail(), providerUpdate: async (e) => (updates.push(e), {}) as never }),
    );
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    expect(r.$('[data-testid="endpoint-uptime"]')).toBeNull();
    expect(r.container.textContent).not.toContain('Skip endpoints below');
    await r.act(() => byText(r.container, 'button', 'Save settings').click());
    await waitFor(() => expect(updates.length).toBe(1), r);
    expect(updates[0]?.minEndpointUptime).toBe(undefined);
  });

  it('reads the key limits from the provider on a button, and shows a refusal verbatim', async () => {
    const asked: [string, boolean | undefined][] = [];
    const r = mount(
      <ProviderDetailPanel id="prov_1" onChanged={() => undefined} />,
      testServices({
        providerDetail: async (id, refresh) => {
          asked.push([id, refresh]);
          return refresh
            ? { ...detail(), openRouterKey: { label: 'main', limit: 10, limit_remaining: 8, limit_reset: '', usage: 2, usage_daily: 0, is_free_tier: true } }
            : detail();
        },
        providerUpdate: async () => {
          throw new Error('providers: base URL must use https');
        },
      }),
    );
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    expect(r.$('[data-testid="key-info"]')).toBeNull();
    await r.act(() => byText(r.container, 'button', "Read the key's limits from the provider").click());
    await waitFor(() => expect(need(r, '[data-testid="key-info"]').textContent).toBe('Key "main": used 2 of 10, free tier'), r);
    expect(asked.some(([, refresh]) => refresh === true)).toBe(true);

    await r.act(() => byText(r.container, 'button', 'Save settings').click());
    await waitFor(() => expect(need(r, '[role="alert"]').textContent).toContain('providers: base URL must use https'), r);
  });
});
