// Typed client for provider detail and editing (internal/core/ext_providerdetail.go).

import type { Transport } from './transport';
import type { Provider, ProviderModel } from './types';

export interface QuotaWindowView {
  model?: string;
  kind: string;
  limit: number;
  used: number;
  resetAt?: number;
  source: 'configured' | 'headers' | 'api' | string;
  updatedAt: number;
}

export interface OpenRouterKey {
  label: string;
  limit: number | null;
  limit_remaining: number | null;
  limit_reset: string;
  usage: number;
  usage_daily: number;
  is_free_tier: boolean;
}

export interface ProviderDetail {
  provider: Provider;
  models: ProviderModel[];
  quota: QuotaWindowView[];
  openRouterKey?: OpenRouterKey;
  keyError?: string;
}

export interface ProviderEdit {
  id: string;
  name?: string;
  baseUrl?: string;
  tier?: string;
  dataClass?: string;
  maxConcurrent?: number;
  hourlyCost?: number;
  apiKey?: string;
}

export interface ProviderClient {
  providerDetail(id: string, refreshKey?: boolean): Promise<ProviderDetail>;
  providerUpdate(e: ProviderEdit): Promise<Provider>;
  providerSetPrice(providerId: string, model: string, inputPerM: number, outputPerM: number, cacheReadPerM?: number, cacheWritePerM?: number): Promise<void>;
  providerSetQuota(providerId: string, model: string, kind: string, limit: number): Promise<void>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export function createProviderClient(t: Transport): ProviderClient {
  return {
    providerDetail: async (id, refreshKey = false) => {
      const r = await t.call<ProviderDetail>('provider.detail', { id, refreshKey });
      return { ...r, models: arr(r.models), quota: arr(r.quota) };
    },
    providerUpdate: (e) => t.call('provider.update', e),
    providerSetPrice: async (providerId, model, inputPerM, outputPerM, cacheReadPerM = 0, cacheWritePerM = 0) => {
      await t.call('provider.setPrice', { providerId, model, inputPerM, outputPerM, cacheReadPerM, cacheWritePerM });
    },
    providerSetQuota: async (providerId, model, kind, limit) => {
      await t.call('provider.setQuota', { providerId, model, kind, limit });
    },
  };
}
