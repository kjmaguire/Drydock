// Pure helpers for the Providers screens: price text (an unknown price is never shown as 0), probe summaries, the create
// request (exported Go field names), the key-limit summary and the list rows of the Models page (logo, what it is, state).

import type { AvoidedEndpoint, ProviderDetail } from '../../api/providerClient.ts';
import type { DataClass, NewProvider, PriceTier, ProbeResult, Provider, ProviderKind, ProviderModel } from '../../api/types.ts';
import { formatUsd, plural } from '../../lib/format.ts';
import type { Tone } from '../../ui/tokens.ts';

export const isZeroTime = (s: string | undefined) => !s || s.startsWith('0001-');

/** USD per million tokens. A model with no known price says so; it is never shown as 0. */
export function priceText(m: ProviderModel, which: 'in' | 'out'): string {
  if (m.isFree) return 'free';
  const v = which === 'in' ? m.inputPerM : m.outputPerM;
  if (!(m.inputPerM > 0 || m.outputPerM > 0)) return 'not reported';
  return v > 0 ? formatUsd(v) : 'free';
}

export const priceKnown = (m: ProviderModel) => m.isFree || m.inputPerM > 0 || m.outputPerM > 0;

/** The raw price cell of the editable price table: free, the number, or unknown (never 0 for an unknown price). */
export function rawPrice(m: ProviderModel, which: 'in' | 'out'): string {
  if (m.isFree) return 'free';
  if (!(m.inputPerM || m.outputPerM)) return 'unknown';
  return String(which === 'in' ? m.inputPerM : m.outputPerM);
}

export const PRICE_HINT: Record<string, string> = {
  catalog: 'Price from the provider catalog',
  builtin: 'Price from Drydock’s built-in table',
  user: 'Price entered by you',
};

export const priceTone = (src: string): Tone => (src === 'catalog' ? 'ok' : src === 'user' ? 'info' : 'warn');

/** The probe outcome in words. */
export const probeLabel = (r: ProbeResult) => (r.OK ? 'reachable' : r.AuthFailed ? 'key rejected' : 'failed');

/** "flavor x, 42 ms, 3 models listed". Null lists count as empty. */
export function probeDetail(r: ProbeResult): string {
  return `${r.Flavor ? `flavor ${r.Flavor}, ` : ''}${r.Latency > 0 ? `${Math.round(r.Latency / 1e6)} ms, ` : ''}${plural((r.Models ?? []).length, 'model')} listed`;
}

/** Health from the last probe; anything but up or down is unknown. */
export function healthTone(status: string): Tone {
  return status === 'up' ? 'ok' : status === 'down' ? 'err' : 'muted';
}

// Well-known API hosts and the brand their logo is; a host not listed falls back to the detected flavor.
const BRAND_HOSTS: [suffix: string, brand: string][] = [
  ['openrouter.ai', 'openrouter'],
  ['anthropic.com', 'anthropic'],
  ['generativelanguage.googleapis.com', 'gemini'],
  ['mistral.ai', 'mistral'],
  ['deepseek.com', 'deepseek'],
  ['openai.com', 'openai'],
  ['groq.com', 'groq'],
];

/** The host (and port) of a provider's base URL, or the URL itself when it does not parse. */
export function providerHost(baseUrl: string): string {
  try {
    return new URL(baseUrl).host;
  } catch {
    return baseUrl;
  }
}

/** The brand id a provider's logo is looked up by (BrandLogo decides whether it has one): its API host, else its flavor. */
export function providerBrand(p: Pick<Provider, 'baseUrl' | 'flavor'>): string {
  let host = '';
  try {
    host = new URL(p.baseUrl).hostname.toLowerCase();
  } catch {
    host = '';
  }
  const hit = BRAND_HOSTS.find(([suffix]) => host === suffix || host.endsWith(`.${suffix}`));
  return hit ? hit[1] : (p.flavor ?? '').toLowerCase();
}

const KIND_WORDS: Record<string, string> = { aggregator: 'Aggregator', direct: 'Vendor API', host: 'Your server' };

/** A provider's kind in words ("Aggregator", "Vendor API", "Your server"). */
export const kindWords = (kind: string): string => KIND_WORDS[kind] ?? kind;

/** The list row's line under the name: the kind in words and the tier ("Aggregator · cheap"). */
export const providerRowSub = (p: Pick<Provider, 'kind' | 'tier'>): string => `${kindWords(p.kind)} · ${p.tier}`;

/** The detail's line under the name: the kind in words and the host it calls ("Aggregator · openrouter.ai"). */
export const providerHeadSub = (p: Pick<Provider, 'kind' | 'baseUrl'>): string => `${kindWords(p.kind)} · ${providerHost(p.baseUrl)}`;

/** The state at the end of a provider's row and on its detail: disabled, or the health from the last check. */
export function providerStatus(p: Pick<Provider, 'enabled' | 'status'>): { text: string; tone: Tone } {
  if (!p.enabled) return { text: 'Disabled', tone: 'muted' };
  if (p.status === 'up') return { text: 'Up', tone: 'ok' };
  if (p.status === 'down') return { text: 'Down', tone: 'err' };
  return { text: 'Not checked', tone: 'muted' };
}

/**
 * The providers the Providers group lists: every one but the server of Models on this PC, which the core registers as a provider
 * (local.get's settings.providerId) and which is the page's own "On this PC" row.
 */
export function listedProviders<P extends Pick<Provider, 'id'>>(list: readonly P[], localProviderId: string | undefined): P[] {
  return localProviderId ? list.filter((p) => p.id !== localProviderId) : [...list];
}

/** Where the selection goes when a provider is removed: the one after it, else the one before, else `fallback`. */
export function nextAfterRemoval(list: readonly Pick<Provider, 'id'>[], removed: string, fallback: string): string {
  const i = list.findIndex((p) => p.id === removed);
  const next = list[i + 1] ?? list[i - 1];
  return i >= 0 && next ? next.id : fallback;
}

export const ATTESTATIONS: [string, string][] = [
  ['billing_enabled', 'The Google project behind this key has billing enabled (needed for a private Gemini route)'],
  ['lan_attested', 'I accept plain http to this host on my private network'],
];

export const FLAVORS = ['openai', 'openrouter', 'gemini', 'anthropic', 'vllm', 'sglang', 'ollama', 'llamacpp'];

export const QUOTA_KINDS: [string, string][] = [
  ['rpm', 'requests per minute'],
  ['rpd', 'requests per day'],
  ['tpm', 'tokens per minute'],
  ['tpd', 'tokens per day'],
  ['itpm', 'input tokens per minute'],
  ['otpm', 'output tokens per minute'],
  ['credits', 'credits (USD)'],
];

export const quotaKindLabel = (k: string) => QUOTA_KINDS.find(([v]) => v === k)?.[1] ?? k;

export interface ProviderFormValues {
  name: string;
  kind: ProviderKind;
  baseUrl: string;
  dataClass: DataClass;
  tier: PriceTier;
  flavor: string;
  maxConcurrent: string;
  hourlyCost: string;
  fingerprint: string;
  att: Record<string, boolean>;
}

export const emptyProviderForm = (): ProviderFormValues => ({
  name: '',
  kind: 'direct',
  baseUrl: '',
  dataClass: 'private',
  tier: 'cheap',
  flavor: '',
  maxConcurrent: '',
  hourlyCost: '',
  fingerprint: '',
  att: {},
});

/** OpenRouter's API address: the host the core's flavor detection, key limits and endpoint uptime read (internal/providers). */
export const OPENROUTER_BASE = 'https://openrouter.ai/api/v1';

/**
 * The provider form filled in for OpenRouter (the Integrations page sets it up in place, decision P103): its name, an aggregator,
 * its address; the key is typed, and the data class and tier keep the form's defaults (private, cheap).
 */
export const openRouterForm = (): ProviderFormValues => ({ ...emptyProviderForm(), name: 'OpenRouter', kind: 'aggregator', baseUrl: OPENROUTER_BASE });

/** The configured providers that are OpenRouter: their address is on openrouter.ai, whatever they were named. */
export const openRouterProviders = <P extends Pick<Provider, 'baseUrl' | 'flavor'>>(list: readonly P[]): P[] => list.filter((p) => providerBrand(p) === 'openrouter');

/**
 * provider.create takes the exported Go field names. Only attestations that are true are sent; the key is sent only when
 * one was typed. The key is passed in (read from the input at submit time), never kept in the form values.
 */
export function newProviderRequest(v: ProviderFormValues, apiKey: string): NewProvider {
  const body: NewProvider = {
    Name: v.name.trim(),
    Kind: v.kind,
    BaseURL: v.baseUrl.trim(),
    DataClass: v.dataClass,
    Tier: v.tier,
    Attestations: Object.fromEntries(Object.entries(v.att).filter(([, on]) => on)),
  };
  if (apiKey) body.APIKey = apiKey;
  if (v.flavor) body.Flavor = v.flavor;
  if (v.maxConcurrent.trim()) body.MaxConcurrent = Number(v.maxConcurrent);
  if (v.hourlyCost.trim()) body.HourlyCost = Number(v.hourlyCost);
  if (v.fingerprint.trim()) body.TLSFingerprint = v.fingerprint.trim();
  return body;
}

/** The OpenRouter key limits in words, or the provider's error. */
export function keyInfoText(d: Pick<ProviderDetail, 'openRouterKey' | 'keyError'>): string {
  const k = d.openRouterKey;
  if (!k) return d.keyError ?? '';
  return `Key "${k.label}": used ${k.usage}${k.limit !== null ? ` of ${k.limit}` : ' (no limit)'}${k.is_free_tier ? ', free tier' : ''}`;
}

/** Used fraction of a quota window (undefined without a positive limit). */
export const quotaFraction = (used: number, limit: number): number | undefined => (limit > 0 ? used / limit : undefined);

/** "flaky (Upstream Flaky): 91.2% over the last 30 minutes": one skipped OpenRouter endpoint in words. */
export function avoidedEndpointText(e: AvoidedEndpoint): string {
  const span = e.window === '5m' ? '5 minutes' : e.window === '30m' ? '30 minutes' : e.window;
  const name = e.name && e.name !== e.tag ? ` (${e.name})` : '';
  return `${e.tag}${name}: ${Math.round(e.uptimePct * 10) / 10}% over the last ${span}`;
}

/** The threshold field's value as a number between 0 and 100, or undefined when it is not one (nothing is sent). */
export function uptimeThreshold(s: string): number | undefined {
  const v = s.trim();
  if (v === '') return undefined;
  const n = Number(v);
  return Number.isFinite(n) && n >= 0 && n <= 100 ? n : undefined;
}
