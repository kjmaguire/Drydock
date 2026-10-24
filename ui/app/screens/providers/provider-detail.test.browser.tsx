import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { ProviderDetail, ProviderEdit } from '../../api/providerClient.ts';
import type { Provider, ProviderModel } from '../../api/types.ts';
import { byLabel, byText, cleanup, drain, errorOf, mount, renderPlain, testServices, type, waitFor } from '../../test/render.tsx';
import { MAX_CONCURRENT_ERROR, ProviderPane } from './provider-detail.tsx';
import { ProbeView } from './provider-parts.tsx';

afterEach(cleanup);

const provider = (over: Partial<Provider> = {}): Provider => ({
  id: 'prov_1',
  name: 'Fixture',
  kind: 'direct',
  baseUrl: 'https://api.example.com/v1',
  keyRef: 'provider/prov_1/api-key',
  dataClass: 'private',
  tier: 'cheap',
  flavor: '',
  enabled: true,
  maxConcurrent: 0,
  hourlyCost: 0,
  tlsMode: 'system',
  lanUnencrypted: false,
  wireApis: null,
  status: 'up',
  lastSeen: '0001-01-01T00:00:00Z',
  createdAt: '',
  updatedAt: '',
  ...over,
});

const model = (over: Partial<ProviderModel>): ProviderModel => ({
  providerId: 'prov_1',
  id: 'm1',
  contextLength: 0,
  maxOutput: 0,
  supportsTools: false,
  supportsReasoning: false,
  dataClass: 'private',
  tier: 'cheap',
  isFree: false,
  inputPerM: 0,
  outputPerM: 0,
  cacheReadPerM: 0,
  cacheWritePerM: 0,
  priceSource: 'catalog',
  source: 'listing',
  ...over,
});

// a labelled test double of the core's provider.detail answer
const detail = (p: Provider = provider()): ProviderDetail => ({
  provider: p,
  models: [model({ id: 'model-a', inputPerM: 0.5, outputPerM: 1.5, contextLength: 200000 })],
  quota: [{ kind: 'rpd', model: 'model-a', limit: 250, used: 10, source: 'configured', updatedAt: 1 }],
});

const need = (r: { $: (sel: string) => Element | null }, sel: string) => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
};
const exactly = (root: ParentNode, s: string) => Array.from(root.querySelectorAll('*')).filter((e) => e.children.length === 0 && e.textContent === s);
/** The value cell of a settings row, by its label. */
const rowValue = (root: ParentNode, label: string) => {
  const row = Array.from(root.querySelectorAll('[data-part="label"]')).find((l) => l.textContent === label);
  if (!row) throw new Error(`no row ${label}`);
  return row.nextElementSibling as HTMLElement;
};

function show(client: Partial<Client>, p: Provider = provider(), hooks: { changed?: () => void; removed?: () => void } = {}) {
  return mount(<ProviderPane p={p} onChanged={hooks.changed ?? (() => undefined)} onRemoved={hooks.removed ?? (() => undefined)} />, testServices(client));
}
/** Opens the settings form (Edit) once the detail is read. */
async function edit(r: ReturnType<typeof show>) {
  const b = await waitFor(() => {
    const el = byText<HTMLButtonElement>(r.container, 'button', 'Edit');
    if (el.disabled) throw new Error('not yet');
    return el;
  }, r);
  await r.act(() => b.click());
  return waitFor(() => need(r, 'form[aria-label="Provider settings"]'), r);
}

describe('ProviderPane', () => {
  it('the head and the settings rows; the key is never shown; a zero time is "never"', async () => {
    const r = show({ providerDetail: async () => detail() });
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    const head = need(r, '[data-testid="provider-head"]');
    expect(head.querySelector('h2')!.textContent).toBe('Fixture');
    expect(need(r, '[data-testid="detail-status"]').textContent).toBe('Up');
    expect(need(r, '[data-testid="detail-status"]').getAttribute('data-tone')).toBe('ok');
    expect(head.textContent).toContain('Vendor API · api.example.com');
    const settings = need(r, '[data-testid="provider-settings"]');
    expect(rowValue(settings, 'Kind').textContent).toBe('Vendor API');
    expect(rowValue(settings, 'Base URL').textContent).toBe('https://api.example.com/v1');
    expect(rowValue(settings, 'Data class').textContent).toContain('private');
    expect(rowValue(settings, 'Tier').textContent).toBe('cheap');
    expect(rowValue(settings, 'API key').textContent).toBe('Stored in the credential store (never shown)');
    expect(rowValue(settings, 'Attestations').textContent).toBe('None');
    expect(rowValue(settings, 'Flavor').textContent).toBe('not detected');
    expect(r.$('[data-testid="trainable-note"]')).toBeNull();
    expect(r.container.innerHTML).not.toContain('provider/prov_1/api-key');
    // health in words, and the last time it was seen
    expect(need(r, '[data-testid="provider-health-word"]').textContent).toBe('Up');
    expect(exactly(need(r, '[data-testid="provider-health"]'), 'never').length).toBe(1);
    // the models it offers: a known price in dollars, the context
    const row = need(r, '[data-testid="price-row"]');
    expect(row.textContent).toContain('$0.50');
    expect(row.textContent).toContain('200,000');
    expect(need(r, '[data-testid="quota-window"]').textContent).toContain('10 of 250 used');
    expect(need(r, '[data-testid="provider-detail"]').getAttribute('aria-label')).toBe('Fixture details');
    // a provider not on OpenRouter has no key limits to read
    expect(r.container.textContent).not.toContain('Key limits');
  });

  it('a trainable provider without a key: the persistent notice, "None", the attestations in words', async () => {
    const p = provider({ name: 'FreeTier', kind: 'host', baseUrl: 'http://127.0.0.1:9/v1', dataClass: 'trainable', keyRef: '', status: 'unknown', attestations: { lan_attested: true } });
    const r = show({ providerDetail: async () => detail(p) }, p);
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    expect(need(r, '[data-testid="trainable-note"]').textContent).toContain('Public documentation questions only; never receives repo content.');
    const settings = need(r, '[data-testid="provider-settings"]');
    expect(rowValue(settings, 'API key').textContent).toBe('None');
    expect(byText(settings, 'button', 'Add a key')).toBeTruthy();
    expect(rowValue(settings, 'Attestations').textContent).toBe('Plain http on my private network: attested');
    expect(need(r, '[data-testid="detail-status"]').textContent).toBe('Not checked');
    expect(need(r, '[data-testid="provider-head"]').textContent).toContain('Your server · 127.0.0.1:9');
  });

  it('Check now shows the probe and reads the list again; Disable calls the core; Remove asks first, then removes it once', async () => {
    const enabled: [string, boolean][] = [];
    const deleted: string[] = [];
    let changed = 0;
    let removed = 0;
    const r = show(
      {
        providerDetail: async () => detail(),
        providerTest: async () => ({ ProviderID: 'prov_1', OK: false, AuthFailed: true, Latency: 42e6, Flavor: 'openrouter', Models: null, ServerInfo: null, Warnings: ['rate limited'], Error: 'HTTP 401' }),
        providerSetEnabled: async (id, on) => void enabled.push([id, on]),
        providerDelete: async (id) => void deleted.push(id),
      },
      provider(),
      { changed: () => changed++, removed: () => removed++ },
    );
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    await r.act(() => need(r, '[data-testid="provider-check"]').click());
    const p = await waitFor(() => need(r, '[data-testid="probe"]'), r);
    expect(p.getAttribute('data-ok')).toBe('false');
    expect(exactly(p, 'key rejected').length).toBe(1);
    expect(exactly(p, 'HTTP 401').length).toBe(1);
    expect(exactly(p, 'rate limited').length).toBe(1);
    expect(p.textContent).toContain('42 ms');
    expect(changed).toBe(1);
    await r.act(() => byText(r.container, 'button', 'Disable').click());
    await waitFor(() => expect(enabled).toEqual([['prov_1', false]]), r);
    await r.act(() => need(r, '[data-testid="provider-remove"]').click());
    expect(deleted).toEqual([]);
    const yes = need(r, '[data-testid="provider-remove-confirm"]');
    expect(yes.getAttribute('aria-label')).toBe('Yes, remove Fixture');
    expect(yes.closest('[role="group"]')!.getAttribute('aria-label')).toBe('Remove Fixture? Its API key is deleted from the credential store.');
    await r.act(() => {
      yes.click();
      yes.click();
    });
    await waitFor(() => expect(deleted).toEqual(['prov_1']), r);
    await waitFor(() => expect(removed).toBe(1), r);
  });

  it('a disabled provider says so in its pill, offers Enable, and keeps its health in words', async () => {
    const p = provider({ enabled: false, status: 'down' });
    const r = show({ providerDetail: async () => detail(p) }, p);
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    expect(need(r, '[data-testid="detail-status"]').textContent).toBe('Disabled');
    expect(byText(r.container, 'button', 'Enable')).toBeTruthy();
    expect(need(r, '[data-testid="provider-health-word"]').textContent).toBe('Down');
  });

  it('Edit saves the settings through the core, the replaced key leaves its field and is never shown; Cancel closes the form', async () => {
    const updates: ProviderEdit[] = [];
    let changed = 0;
    const r = show({ providerDetail: async () => detail(), providerUpdate: async (e) => (updates.push(e), {}) as never }, provider(), { changed: () => changed++ });
    const form = await edit(r);
    expect(r.$('[data-testid="provider-settings"]')).toBeNull(); // the form takes the rows' place
    type(byLabel(form, 'Name'), 'Renamed');
    const key = byLabel(form, /Replace the API key/);
    expect(key.type).toBe('password');
    type(key, 'sk-new');
    await r.act(() => byText(form, 'button', 'Save settings').click());
    await waitFor(() => expect(updates[0]).toMatchObject({ id: 'prov_1', name: 'Renamed', apiKey: 'sk-new', tier: 'cheap', dataClass: 'private', maxConcurrent: 0 }), r);
    expect(key.value).toBe('');
    expect(r.container.innerHTML).not.toContain('sk-new');
    await waitFor(() => expect(changed).toBe(1), r);
    // saved: the rows are back
    await waitFor(() => need(r, '[data-testid="provider-settings"]'), r);
    const again = await edit(r);
    await r.act(() => byText(again, 'button', 'Cancel').click());
    await waitFor(() => need(r, '[data-testid="provider-settings"]'), r);
    expect(updates.length).toBe(1);
  });

  it('Replace opens the form at the key field', async () => {
    const r = show({ providerDetail: async () => detail() });
    const replace = await waitFor(() => {
      const b = byText<HTMLButtonElement>(need(r, '[data-testid="provider-key"]'), 'button', 'Replace');
      if (b.disabled) throw new Error('not yet');
      return b;
    }, r);
    await r.act(() => replace.click());
    await waitFor(() => expect(document.activeElement).toBe(byLabel(r.container, /Replace the API key/)), r);
  });

  it('edits a price and a quota limit through the core; a price that is not a number or a non-positive limit is not sent', async () => {
    const prices: unknown[][] = [];
    const quotas: unknown[][] = [];
    const r = show({ providerDetail: async () => detail(), providerSetPrice: async (...a) => void prices.push(a), providerSetQuota: async (...a) => void quotas.push(a) });
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
    type(byLabel(r.container, 'Input price of model-a'), '2');
    type(byLabel(r.container, 'Output price of model-a'), '6');
    await r.act(() => byText(r.container, 'button', 'Save price').click());
    await waitFor(() => expect(prices[0]).toEqual(['prov_1', 'model-a', 2, 6, 0, 0]), r);
    type(byLabel(r.container, 'Quota limit'), '5');
    await r.act(() => byText(r.container, 'button', 'Set limit').click());
    await drain();
    expect(quotas).toEqual([['prov_1', '', 'rpd', 5]]); // the form's default window
    type(byLabel<HTMLElement>(r.container, 'Quota window'), 'rpm');
    type(byLabel(r.container, 'Quota limit'), '30');
    await r.act(() => byText(r.container, 'button', 'Set limit').click());
    await waitFor(() => expect(quotas[1]).toEqual(['prov_1', '', 'rpm', 30]), r);
  });

  it('prices: unknown is "not reported" with "no price", never 0; free is free; the source of each', async () => {
    const d = { ...detail(), models: [model({ id: 'unknown-price' }), model({ id: 'free-one', isFree: true, priceSource: 'builtin' }), model({ id: 'paid', inputPerM: 3, outputPerM: 15 }), model({ id: 'mine', inputPerM: 0.5, outputPerM: 1.5, priceSource: 'user' })] };
    const r = show({ providerDetail: async () => d });
    await waitFor(() => expect(r.$$('[data-testid="price-row"]').length).toBe(4), r);
    const tr = (id: string) => need(r, `[data-testid="price-row"][data-model="${id}"]`);
    expect(tr('unknown-price').textContent).toContain('not reported');
    expect(tr('unknown-price').textContent).toContain('no price');
    expect(tr('unknown-price').textContent).not.toContain('$0');
    expect(tr('free-one').textContent).toContain('free');
    expect(tr('free-one').textContent).toContain('builtin');
    expect(tr('paid').textContent).toContain('$3.00');
    expect(tr('paid').textContent).toContain('$15.00');
    expect(tr('paid').textContent).toContain('catalog');
    expect(tr('mine').textContent).toContain('user');
  });

  it('no models listed yet says how to get them', async () => {
    const r = show({ providerDetail: async () => ({ ...detail(), models: [] }) });
    await waitFor(() => need(r, '[data-testid="provider-models"]'), r);
    expect(need(r, '[data-testid="provider-models"]').textContent).toContain('Check now reads the provider');
  });

  it('a concurrency limit that is not a whole number is told under the field and nothing is sent, the typed key stays; empty is unlimited (M25)', async () => {
    const updates: ProviderEdit[] = [];
    const r = show({ providerDetail: async () => detail(), providerUpdate: async (e) => (updates.push(e), {}) as never });
    const form = await edit(r);
    const limit = () => byLabel<HTMLInputElement>(r.container, /^Max concurrent agents/);
    const key = byLabel<HTMLInputElement>(form, /Replace the API key/);
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
    const again = await edit(r);
    type(byLabel<HTMLInputElement>(again, /^Max concurrent agents/), '');
    await r.act(() => byText(again, 'button', 'Save settings').click());
    await waitFor(() => expect(updates[1]).toMatchObject({ id: 'prov_1', maxConcurrent: 0 }), r);
  });

  const openRouter = (): Provider => provider({ name: 'OpenRouter', flavor: 'openrouter', kind: 'aggregator', baseUrl: 'https://openrouter.ai/api/v1' });

  it('an OpenRouter provider shows the endpoints its calls skip and saves the uptime threshold', async () => {
    const updates: ProviderEdit[] = [];
    const orDetail = (): ProviderDetail => ({
      ...detail(openRouter()),
      endpointUptime: {
        minUptime: 98,
        defaultMinUptime: 98,
        avoiding: [
          { providerId: 'prov_1', model: 'acme/coder-7b', threshold: 98, keptBest: false, at: '2026-10-15T09:00:00Z', avoided: [{ tag: 'flaky', name: 'Upstream Flaky', uptimePct: 91.2, window: '30m' }] },
          { providerId: 'prov_1', model: 'acme/other', threshold: 98, keptBest: true, at: '2026-10-15T08:59:00Z', avoided: [{ tag: 'worst', uptimePct: 40, window: '5m' }] },
        ],
      },
    });
    const r = show({ providerDetail: async () => orDetail(), providerUpdate: async (e) => (updates.push(e), {}) as never }, openRouter());
    await waitFor(() => need(r, '[data-testid="endpoint-uptime"]'), r);
    expect(r.$('[data-testid="provider-head"] [data-brand="openrouter"]')).not.toBeNull();
    const rows = Array.from(r.container.querySelectorAll('[data-testid="endpoint-avoidance"]'));
    expect(rows.length).toBe(2);
    const [first, second] = rows;
    expect(first?.getAttribute('data-model')).toBe('acme/coder-7b');
    expect(first?.textContent).toContain('flaky (Upstream Flaky): 91.2% over the last 30 minutes');
    expect(first?.textContent).not.toContain('best kept');
    expect(second?.textContent).toContain('all below 98%: best kept');
    expect(second?.textContent).toContain('worst: 40% over the last 5 minutes');
    // the long explanation is in the Guide
    expect(need(r, '[data-testid="endpoint-uptime"] [data-testid="learn-more"]').getAttribute('data-topic')).toBe('providers');

    const form = await edit(r);
    const field = byLabel(form, /Skip endpoints below this recent uptime/);
    expect(field.value).toBe('98');
    type(field, '90');
    await r.act(() => byText(form, 'button', 'Save settings').click());
    await waitFor(() => expect(updates[0]).toMatchObject({ id: 'prov_1', minEndpointUptime: 90 }), r);
  });

  it('other providers have no endpoint uptime section or threshold, and send none', async () => {
    const updates: ProviderEdit[] = [];
    const r = show({ providerDetail: async () => detail(), providerUpdate: async (e) => (updates.push(e), {}) as never });
    const form = await edit(r);
    expect(r.$('[data-testid="endpoint-uptime"]')).toBeNull();
    expect(r.container.textContent).not.toContain('Skip endpoints below');
    await r.act(() => byText(form, 'button', 'Save settings').click());
    await waitFor(() => expect(updates.length).toBe(1), r);
    expect(updates[0]?.minEndpointUptime).toBe(undefined);
  });

  it("reads OpenRouter's key limits on a button, and shows a refusal of the core verbatim", async () => {
    const asked: [string, boolean | undefined][] = [];
    const r = show(
      {
        providerDetail: async (id, refresh) => {
          asked.push([id, refresh]);
          return refresh ? { ...detail(openRouter()), openRouterKey: { label: 'main', limit: 10, limit_remaining: 8, limit_reset: '', usage: 2, usage_daily: 0, is_free_tier: true } } : detail(openRouter());
        },
        providerUpdate: async () => {
          throw new Error('providers: base URL must use https');
        },
      },
      openRouter(),
    );
    await waitFor(() => need(r, '[data-testid="price-row"]'), r);
    expect(r.$('[data-testid="key-info"]')).toBeNull();
    await r.act(() => byText(r.container, 'button', 'Read from OpenRouter').click());
    await waitFor(() => expect(need(r, '[data-testid="key-info"]').textContent).toBe('Key "main": used 2 of 10, free tier'), r);
    expect(asked.some(([, refresh]) => refresh === true)).toBe(true);

    const form = await edit(r);
    await r.act(() => byText(form, 'button', 'Save settings').click());
    await waitFor(() => expect(need(r, '[role="alert"]').textContent).toContain('providers: base URL must use https'), r);
  });

  it('a detail the core could not read says so; the rows from the list stay', async () => {
    const r = show({
      providerDetail: async () => {
        throw new Error('no such provider');
      },
    });
    await waitFor(() => expect(need(r, '[role="alert"]').textContent).toContain('no such provider'), r);
    expect(need(r, '[data-testid="provider-settings"]')).toBeTruthy();
    expect(r.$('[data-testid="provider-detail"]')).toBeNull();
  });
});

describe('ProbeView', () => {
  it('handles null lists', () => {
    const r = renderPlain(<ProbeView r={{ ProviderID: 'p', OK: true, AuthFailed: false, Latency: 0, Flavor: '', Models: null, ServerInfo: null, Warnings: null, Error: '' }} />);
    expect(r.container.textContent).toContain('reachable');
    expect(r.container.textContent).toContain('0 models');
    expect(need(r, '[data-testid="probe"]').getAttribute('data-ok')).toBe('true');
  });
});
