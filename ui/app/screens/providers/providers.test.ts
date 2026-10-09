import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { ProbeResult, ProviderModel } from '../../api/types.ts';
import { emptyProviderForm, healthTone, isZeroTime, keyInfoText, newProviderRequest, priceText, probeDetail, probeLabel, quotaFraction, rawPrice } from './providers.ts';

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
