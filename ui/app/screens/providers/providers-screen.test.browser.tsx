import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { NewProvider, Provider, ProviderModel } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, renderPlain, testServices, type, waitFor } from '../../test/render.tsx';
import { ModelTable, ProbeView } from './provider-parts.tsx';
import { ProvidersScreen } from './providers-screen.tsx';

afterEach(cleanup);

const provider = (over: Partial<Provider> = {}): Provider => ({
  id: 'prov_1',
  name: 'OpenRouter',
  kind: 'aggregator',
  baseUrl: 'https://openrouter.ai/api/v1',
  keyRef: 'ref',
  dataClass: 'private',
  tier: 'cheap',
  flavor: 'openrouter',
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

const need = (r: { $: (sel: string) => Element | null }, sel: string) => {
  const el = r.$(sel);
  if (!el) throw new Error(`no ${sel}`);
  return el as HTMLElement;
};
const has = (r: { container: Element }, s: string) => () => {
  if (!(r.container.textContent ?? '').includes(s)) throw new Error(`no text ${s}`);
};
const exactly = (root: ParentNode, s: string) => Array.from(root.querySelectorAll('*')).filter((e) => e.children.length === 0 && e.textContent === s);

function mountWith(client: Partial<Client>) {
  return mount(<ProvidersScreen />, testServices(client));
}

describe('ProvidersScreen', () => {
  it('lists providers; the key is never shown; a zero time is "never"; trainable carries the persistent notice', async () => {
    const r = mountWith({ providerList: async () => [provider(), provider({ id: 'p2', name: 'FreeTier', dataClass: 'trainable', keyRef: '', status: 'unknown' })] });
    const cards = await waitFor(() => {
      const c = Array.from(r.$$('[data-testid="provider"]'));
      if (c.length !== 2) throw new Error('not yet');
      return c;
    }, r);
    expect(cards[0]!.getAttribute('data-name')).toBe('OpenRouter');
    expect(exactly(r.container, 'never').length).toBe(2);
    expect(cards[0]!.textContent).toContain('stored in the credential store (never shown)');
    expect(cards[0]!.textContent).not.toContain('ref');
    expect(cards[1]!.textContent).toContain('none');
    expect(need(r, '[data-testid="trainable-note"]').textContent).toContain('Public documentation questions only; never receives repo content.');
    expect(cards[0]!.querySelector('[data-testid="trainable-note"]')).toBeNull();
    // health is a word as well as a color
    expect(cards[0]!.textContent).toContain('up');
    expect(cards[1]!.textContent).toContain('unknown');
  });

  it('create sends exported Go field names, the API key leaves the input immediately, and the key is not rendered anywhere', async () => {
    const calls: NewProvider[] = [];
    let list: Provider[] = [];
    const r = mountWith({
      providerList: async () => list,
      providerCreate: async (p) => {
        calls.push(p);
        list = [provider({ name: 'My Host' })];
        return list[0]!;
      },
    });
    await waitFor(has(r, 'No providers yet.'), r);
    const add = () => byText<HTMLButtonElement>(r.container, 'button', 'Add provider');
    expect(add().disabled).toBe(true);
    type(byLabel(r.container, 'Provider name'), 'My Host');
    type(byLabel<HTMLSelectElement>(r.container, 'Kind'), 'host');
    type(byLabel(r.container, 'Base URL'), 'http://192.168.1.5:8000/v1');
    const key = byLabel(r.container, /API key/);
    expect(key.type).toBe('password');
    type(key, 'sk-secret-123');
    type(byLabel<HTMLSelectElement>(r.container, 'Data class'), 'private');
    type(byLabel<HTMLSelectElement>(r.container, 'Tier'), 'free');
    await r.act(() => byLabel(r.container, /accept plain http/).click());
    await waitFor(() => expect(add().disabled).toBe(false), r);
    await r.act(() => add().click());
    await waitFor(() => expect(calls.length).toBe(1), r);
    expect(calls[0]).toEqual({
      Name: 'My Host',
      Kind: 'host',
      BaseURL: 'http://192.168.1.5:8000/v1',
      APIKey: 'sk-secret-123',
      DataClass: 'private',
      Tier: 'free',
      Attestations: { lan_attested: true },
    });
    expect(key.value).toBe('');
    await waitFor(() => need(r, '[data-testid="provider"]'), r);
    expect(r.container.innerHTML).not.toContain('sk-secret-123');
    // the form is reset for the next one
    expect(byLabel(r.container, 'Provider name').value).toBe('');
    expect(byLabel<HTMLInputElement>(r.container, /accept plain http/).checked).toBe(false);
  });

  it('shows the backend validation error verbatim and still does not keep the key', async () => {
    const msg = 'providers: plain http is only allowed on loopback, link-local or private addresses; use https';
    const r = mountWith({
      providerList: async () => [],
      providerCreate: async () => {
        throw new Error(msg);
      },
    });
    await waitFor(has(r, 'No providers yet.'), r);
    type(byLabel(r.container, 'Provider name'), 'x');
    type(byLabel(r.container, 'Base URL'), 'http://example.com/v1');
    const key = byLabel(r.container, /API key/);
    type(key, 'sk-secret-456');
    const add = byText<HTMLButtonElement>(r.container, 'button', 'Add provider');
    await waitFor(() => expect(add.disabled).toBe(false), r);
    await r.act(() => add.click());
    await waitFor(() => expect(r.$$('[role="alert"]').length).toBe(1), r);
    expect(need(r, '[role="alert"]').textContent).toContain(msg);
    expect(key.value).toBe('');
    expect(r.container.innerHTML).not.toContain('sk-secret-456');
    // what was typed apart from the key stays, so the user can correct it
    expect(byLabel(r.container, 'Provider name').value).toBe('x');
  });

  it('Test shows the probe, Models shows the catalog, enable/disable and delete (confirmed) call the core', async () => {
    const enabled: [string, boolean][] = [];
    const deleted: string[] = [];
    const r = mountWith({
      providerList: async () => [provider()],
      providerTest: async () => ({ ProviderID: 'prov_1', OK: false, AuthFailed: true, Latency: 42e6, Flavor: 'openrouter', Models: null, ServerInfo: null, Warnings: ['rate limited'], Error: 'HTTP 401' }),
      providerModels: async () => [model({ id: 'a/b' })],
      providerSetEnabled: async (id, on) => void enabled.push([id, on]),
      providerDelete: async (id) => void deleted.push(id),
    });
    const card = await waitFor(() => need(r, '[data-testid="provider"]'), r);
    await r.act(() => byText(card, 'button', 'Test').click());
    const p = await waitFor(() => need(r, '[data-testid="probe"]'), r);
    expect(p.getAttribute('data-ok')).toBe('false');
    expect(exactly(p, 'key rejected').length).toBe(1);
    expect(exactly(p, 'HTTP 401').length).toBe(1);
    expect(exactly(p, 'rate limited').length).toBe(1);
    expect(p.textContent).toContain('42 ms');
    await r.act(() => byText(card, 'button', 'Models').click());
    await waitFor(() => need(r, '[data-testid="model"]'), r);
    await r.act(() => byText(card, 'button', 'Disable').click());
    await waitFor(() => expect(enabled).toEqual([['prov_1', false]]), r);
    await r.act(() => byText(card, 'button', 'Delete').click());
    expect(deleted).toEqual([]);
    await r.act(() => byText(card, 'button', 'Yes, delete OpenRouter').click());
    await waitFor(() => expect(deleted).toEqual(['prov_1']), r);
  });

  it('Details and editing opens the provider detail in a dialog', async () => {
    const r = mountWith({
      providerList: async () => [provider()],
      providerDetail: async () => ({ provider: provider(), models: [], quota: [] }),
    });
    const card = await waitFor(() => need(r, '[data-testid="provider"]'), r);
    expect(r.$('[data-testid="provider-detail"]')).toBeNull();
    await r.act(() => byText(card, 'button', 'Details and editing').click());
    const panel = await waitFor(() => need(r, '[data-testid="provider-detail"]'), r);
    expect(panel.getAttribute('aria-label')).toBe('OpenRouter details');
    expect(need(r, 'dialog').hasAttribute('open')).toBe(true);
    await r.act(() => byText(need(r, 'dialog'), 'button', 'Close').click());
    await waitFor(() => expect(r.$('[data-testid="provider-detail"]')).toBeNull(), r);
  });

  it('read-only turns off adding, with the reason, but the fields stay usable', async () => {
    const s = testServices({ providerList: async () => [], licenseStatus: async () => expiredLicense, projectList: async () => [], appInfo: async () => ({}) as never });
    s.workspace.start();
    const r = mount(<ProvidersScreen />, s);
    await waitFor(() => need(r, '[data-testid="readonly-note"]'), r);
    type(byLabel(r.container, 'Provider name'), 'blocked');
    type(byLabel(r.container, 'Base URL'), 'https://x');
    const add = byText<HTMLButtonElement>(r.container, 'button', 'Add provider');
    await waitFor(() => expect(add.disabled).toBe(true), r);
    expect(add.title).toContain('read-only');
    expect(byLabel<HTMLInputElement>(r.container, 'Provider name').disabled).toBe(false);
  });
});

describe('ModelTable and ProbeView', () => {
  it('model prices: unknown is "not reported", never 0; free is free; source badges', () => {
    const rows = [
      model({ id: 'unknown-price' }),
      model({ id: 'free-one', isFree: true, priceSource: 'builtin' }),
      model({ id: 'paid', inputPerM: 3, outputPerM: 15, priceSource: 'catalog', contextLength: 200000, supportsTools: true }),
      model({ id: 'mine', inputPerM: 0.5, outputPerM: 1.5, priceSource: 'user' }),
    ];
    const r = renderPlain(<ModelTable models={rows} />);
    const tr = (id: string) => Array.from(r.$$('tr')).find((x) => x.querySelector('code')?.textContent === id)!;
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

  it('an empty catalog says how to fetch one', () => {
    const r = renderPlain(<ModelTable models={[]} />);
    expect(r.container.textContent).toContain('No models in the catalog');
  });

  it('probe view handles null lists', () => {
    const r = renderPlain(<ProbeView r={{ ProviderID: 'p', OK: true, AuthFailed: false, Latency: 0, Flavor: '', Models: null, ServerInfo: null, Warnings: null, Error: '' }} />);
    expect(r.container.textContent).toContain('reachable');
    expect(r.container.textContent).toContain('0 model(s)');
    expect(need(r, '[data-testid="probe"]').getAttribute('data-ok')).toBe('true');
  });
});
