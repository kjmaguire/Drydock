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
      const list = (xs: unknown[]) => xs.join(k === 'fallback' ? ' → ' : ', ');
      const text = v === true ? label : `${label}: ${Array.isArray(v) ? list(v) : typeof v === 'object' ? JSON.stringify(v) : String(v)}`;
      const tone = k === 'deny' ? 'err' : k === 'escalate_to' || k === 'penalty' ? 'warn' : k.endsWith('tier') ? 'accent' : 'muted';
      return { key: k, text, tone } as const;
    });
}

/** The most models a fallback chain may name (router.MaxFallbackChain). */
export const MAX_CHAIN = 4;

/** The fallback chain typed in the editor: models separated by commas or spaces, in order, each once. */
export function parseChain(text: string): string[] {
  const out: string[] = [];
  for (const m of text.split(/[\s,]+/)) if (m && !out.includes(m)) out.push(m);
  return out;
}

/** The action without its chain (the editor shows the chain in its own field) and the chain as text. */
export function splitChain(then: RouteAction | null | undefined): { rest: RouteAction; chain: string } {
  const rest: RouteAction = { ...then };
  delete rest.fallback;
  return { rest, chain: (then?.fallback ?? []).join(', ') };
}

/** The action with the chain put back: none when the field is empty. */
export function withChain(then: RouteAction, chain: string[]): RouteAction {
  const rest: RouteAction = { ...then };
  delete rest.fallback;
  return chain.length > 0 ? { ...rest, fallback: chain } : rest;
}

export const LEVELS = ['cheapest', 'fast', 'balanced', 'quality', 'maximum_quality'];
/** The run kinds a decision is for, as a rule matches them with run.kind. The agent turn kinds come first (decision P63:
 * every turn of an agent is routed), then the app's own model calls. */
export const RUN_KINDS = ['agent_task', 'review', 'room_reply', 'spec', 'tests', 'design', 'security_review', 'consult', 'summarize', 'classify'];

const RUN_KIND_LABEL: Record<string, string> = {
  agent_task: 'work',
  review: 'review',
  room_reply: 'room reply',
  spec: 'spec (pm)',
  tests: 'tests first',
  design: 'design (architect)',
  security_review: 'security review',
};

/** The words for a run kind: the turn it was for ("room reply"), or the kind itself when it is not an agent turn. */
export const runKindLabel = (kind: string): string => RUN_KIND_LABEL[kind] ?? kind;

/** The run kinds as select options: the kind (what a rule matches) and its words. */
export const runKindOptions = (): [string, string][] => RUN_KINDS.map((k) => [k, RUN_KIND_LABEL[k] ? `${RUN_KIND_LABEL[k]} (${k})` : k]);

/** Providers the cheap model selection works on: cheap tier and private data class. */
export const cheapProviders = (list: readonly Provider[]): Provider[] => list.filter((p) => p.tier === 'cheap' && p.dataClass === 'private');
