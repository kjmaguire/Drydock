// Pure helpers for the Routing screen: rule order, the action JSON, readable action chips and the preview choices.

import type { RouteAction, RouteRule } from '../../api/extTypes.ts';
import type { Provider } from '../../api/types.ts';

/** Rules in evaluation order: highest priority first, then by id. */
export function sortRules(rules: readonly RouteRule[]): RouteRule[] {
  return [...rules].sort((a, b) => b.priority - a.priority || a.id.localeCompare(b.id));
}

/** A blank rule for the editor; the core assigns the id. */
export const newRule = (): RouteRule => ({ id: '', scope: 'workspace', priority: 0, when: 'true', then: {}, enabled: true, version: 0 });

/** Parse the action text, or say why it is not JSON (checked before the core is called). */
export function parseThen(text: string): { ok: true; value: RouteAction } | { ok: false; error: string } {
  try {
    return { ok: true, value: JSON.parse(text) as RouteAction };
  } catch (e) {
    return { ok: false, error: `The action is not valid JSON: ${(e as Error).message}` };
  }
}

/** One readable chip per action key, e.g. "prefer tier: cheap", "deny". The raw JSON stays available next to them. */
export function actionChips(then: RouteAction | null | undefined): { key: string; text: string; tone: 'accent' | 'warn' | 'err' | 'muted' }[] {
  if (!then) return [];
  return Object.entries(then)
    .filter(([, v]) => v !== undefined && v !== null && v !== false)
    .map(([k, v]) => {
      const label = k.replace(/_/g, ' ');
      const text = v === true ? label : `${label}: ${Array.isArray(v) ? v.join(', ') : typeof v === 'object' ? JSON.stringify(v) : String(v)}`;
      const tone = k === 'deny' ? 'err' : k === 'escalate_to' || k === 'penalty' ? 'warn' : k.endsWith('tier') ? 'accent' : 'muted';
      return { key: k, text, tone } as const;
    });
}

export const LEVELS = ['cheapest', 'fast', 'balanced', 'quality', 'maximum_quality'];
export const RUN_KINDS = ['agent_task', 'review', 'consult', 'summarize', 'classify'];

/** Providers the cheap model selection works on: cheap tier and private data class. */
export const cheapProviders = (list: readonly Provider[]): Provider[] => list.filter((p) => p.tier === 'cheap' && p.dataClass === 'private');
