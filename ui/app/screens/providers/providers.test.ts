import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { ProbeResult, ProviderModel } from '../../api/types.ts';
import {
  ADD_PROVIDER,
  defaultProvider,
  emptyProviderForm,
  healthTone,
  isZeroTime,
  keyInfoText,
  newProviderRequest,
  priceText,
  probeDetail,
  probeLabel,
  providerBlurb,
  providerBrand,
  providerHost,
  providerState,
  quotaFraction,
  rawPrice,
} from './providers.ts';

const model = (over: Partial<ProviderModel> = {}): ProviderModel => ({
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

const probe = (over: Partial<ProbeResult> = {}): ProbeResult => ({ ProviderID: 'p', OK: true, AuthFailed: false, Latency: 0, Flavor: '', Models: null, ServerInfo: null, Warnings: null, Error: '', ...over });

describe('provider helpers', () => {
  it('an unknown price is "not reported", never 0; free is free', () => {
    expect(priceText(model(), 'out')).toBe('not reported');
    expect(priceText(model({ isFree: true }), 'in')).toBe('free');
    expect(priceText(model({ inputPerM: 3, outputPerM: 15 }), 'in')).toBe('$3.00');
    expect(priceText(model({ inputPerM: 0.001, outputPerM: 0 }), 'in')).toBe('$0.0010');
    expect(priceText(model({ inputPerM: 0.001, outputPerM: 0 }), 'out')).toBe('free');
    expect(rawPrice(model(), 'in')).toBe('unknown');
    expect(rawPrice(model({ inputPerM: 0.5, outputPerM: 1.5 }), 'out')).toBe('1.5');
  });

  it('probe words; null lists count as empty', () => {
    expect(probeLabel(probe())).toBe('reachable');
    expect(probeLabel(probe({ OK: false, AuthFailed: true }))).toBe('key rejected');
    expect(probeLabel(probe({ OK: false }))).toBe('failed');
    expect(probeDetail(probe())).toBe('0 model(s) listed');
    expect(probeDetail(probe({ Flavor: 'openrouter', Latency: 42e6, Models: [model()] }))).toBe('flavor openrouter, 42 ms, 1 model(s) listed');
  });

  it('create sends exported Go field names, only true attestations, and the key only when typed', () => {
    const v = { ...emptyProviderForm(), name: ' My Host ', kind: 'host' as const, baseUrl: 'http://192.168.1.5:8000/v1 ', tier: 'free' as const, att: { lan_attested: true, billing_enabled: false } };
    expect(newProviderRequest(v, 'sk-secret-123')).toEqual({
      Name: 'My Host',
      Kind: 'host',
      BaseURL: 'http://192.168.1.5:8000/v1',
      APIKey: 'sk-secret-123',
      DataClass: 'private',
      Tier: 'free',
      Attestations: { lan_attested: true },
    });
    const adv = newProviderRequest({ ...emptyProviderForm(), name: 'x', baseUrl: 'https://x', flavor: 'vllm', maxConcurrent: '4', hourlyCost: '1.5', fingerprint: ' ab:cd ' }, '');
    expect(adv).toEqual({ Name: 'x', Kind: 'direct', BaseURL: 'https://x', DataClass: 'private', Tier: 'cheap', Attestations: {}, Flavor: 'vllm', MaxConcurrent: 4, HourlyCost: 1.5, TLSFingerprint: 'ab:cd' });
  });

  it('key limits in words, zero times, health and quota fractions', () => {
    expect(keyInfoText({ openRouterKey: { label: 'main', limit: null, limit_remaining: null, limit_reset: '', usage: 1.5, usage_daily: 0, is_free_tier: true } })).toBe('Key "main": used 1.5 (no limit), free tier');
    expect(keyInfoText({ openRouterKey: { label: 'k', limit: 10, limit_remaining: 8, limit_reset: '', usage: 2, usage_daily: 0, is_free_tier: false } })).toBe('Key "k": used 2 of 10');
    expect(keyInfoText({ keyError: 'not an OpenRouter provider' })).toBe('not an OpenRouter provider');
    expect(isZeroTime('0001-01-01T00:00:00Z')).toBe(true);
    expect(isZeroTime(undefined)).toBe(true);
    expect(isZeroTime('2026-01-01T00:00:00Z')).toBe(false);
    expect(healthTone('up')).toBe('ok');
    expect(healthTone('down')).toBe('err');
    expect(healthTone('')).toBe('muted');
    expect(quotaFraction(10, 250)).toBe(0.04);
    expect(quotaFraction(10, 0)).toBe(undefined);
  });
});

describe('provider tiles', () => {
  it('the logo is looked up by the API host, else the detected flavor', () => {
    expect(providerBrand({ baseUrl: 'https://openrouter.ai/api/v1', flavor: 'openai' })).toBe('openrouter');
    expect(providerBrand({ baseUrl: 'https://api.anthropic.com/v1', flavor: '' })).toBe('anthropic');
    expect(providerBrand({ baseUrl: 'https://generativelanguage.googleapis.com/v1beta/openai', flavor: '' })).toBe('gemini');
    expect(providerBrand({ baseUrl: 'https://api.mistral.ai/v1', flavor: 'openai' })).toBe('mistral');
    expect(providerBrand({ baseUrl: 'https://api.deepseek.com', flavor: '' })).toBe('deepseek');
    expect(providerBrand({ baseUrl: 'http://127.0.0.1:11434/v1', flavor: 'Ollama' })).toBe('ollama');
    expect(providerBrand({ baseUrl: 'http://10.0.0.5:8000/v1', flavor: 'vllm' })).toBe('vllm');
    // a look-alike host is not the brand
    expect(providerBrand({ baseUrl: 'https://notopenrouter.ai/v1', flavor: '' })).toBe('');
    expect(providerBrand({ baseUrl: 'not a url', flavor: '' })).toBe('');
  });

  it('what it is and its state, with a tone', () => {
    expect(providerHost('http://127.0.0.1:9/v1')).toBe('127.0.0.1:9');
    expect(providerHost('nonsense')).toBe('nonsense');
    expect(providerBlurb({ kind: 'aggregator', baseUrl: 'https://openrouter.ai/api/v1' })).toBe('Aggregator, openrouter.ai');
    expect(providerBlurb({ kind: 'direct', baseUrl: 'https://api.anthropic.com' })).toBe('Vendor API, api.anthropic.com');
    expect(providerBlurb({ kind: 'host', baseUrl: 'http://127.0.0.1:9/v1' })).toBe('Your server, 127.0.0.1:9');
    expect(providerState({ enabled: true, status: 'up' })).toEqual({ text: 'Up', tone: 'ok' });
    expect(providerState({ enabled: true, status: 'down' })).toEqual({ text: 'Down', tone: 'bad' });
    expect(providerState({ enabled: true, status: 'unknown' })).toEqual({ text: 'Not checked yet', tone: 'warn' });
    expect(providerState({ enabled: false, status: 'down' })).toEqual({ text: 'Disabled', tone: '' });
  });

  it('opens the first enabled provider that is down, else the first, else the add form', () => {
    const p = (id: string, status: string, enabled = true) => ({ id, status, enabled });
    expect(defaultProvider([p('a', 'up'), p('b', 'down', false), p('c', 'down')])).toBe('c');
    expect(defaultProvider([p('a', 'unknown'), p('b', 'up')])).toBe('a');
    expect(defaultProvider([])).toBe(ADD_PROVIDER);
  });
});
