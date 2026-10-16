// Typed client for provider detail and editing (internal/core/ext_providerdetail.go).

import type { Transport } from './transport.ts';
import type { Provider, ProviderModel } from './types.ts';

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

/** One OpenRouter endpoint the app's own calls skip for its poor recent uptime (decision P53 c). */
export interface AvoidedEndpoint {
  tag: string;
  name?: string;
  uptimePct: number;
  /** Which of OpenRouter's uptime windows the figure is: "5m" or "30m". */
  window: string;
}

export interface EndpointAvoidance {
  providerId: string;
  model: string;
  threshold: number;
  avoided: AvoidedEndpoint[];
  /** Every endpoint was below the threshold: the best were kept, not all excluded. */
  keptBest: boolean;
  at: string;
}

/** OpenRouter providers only: the threshold (0 is off) and what is being skipped now. */
export interface EndpointUptimeView {
  minUptime: number;
  defaultMinUptime: number;
  avoiding: EndpointAvoidance[];
}

export interface ProviderDetail {
  provider: Provider;
  models: ProviderModel[];
  quota: QuotaWindowView[];
  openRouterKey?: OpenRouterKey;
  keyError?: string;
  endpointUptime?: EndpointUptimeView;
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
  /** OpenRouter only: skip endpoints whose recent uptime is below this percentage (0 turns it off). */
  minEndpointUptime?: number;
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
      const out: ProviderDetail = { ...r, models: arr(r.models), quota: arr(r.quota) };
      if (r.endpointUptime) {
        out.endpointUptime = {
          ...r.endpointUptime,
          avoiding: arr(r.endpointUptime.avoiding).map((a) => ({ ...a, avoided: arr(a.avoided) })),
        };
      }
      return out;
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
