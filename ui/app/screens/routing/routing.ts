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

/** A level as the pages name it. */
export const LEVEL_WORDS: Record<string, string> = {
  cheapest: 'Cheapest',
  fast: 'Fast',
  balanced: 'Balanced',
  quality: 'Quality',
  maximum_quality: 'Maximum quality',
  custom: 'Custom',
};
export const levelWords = (l: string): string => LEVEL_WORDS[l] ?? l;

/** What each level does, in one line (docs/model-routing.md s3). */
export const LEVEL_INTENT: Record<string, string> = {
  cheapest: 'cheap models first, escalating after 2 cheap tries',
  fast: 'cheap, fast models first',
  balanced: 'cheap for cheap-first work, premium where it matters, one premium review',
  quality: 'premium work and review; cheap only for exploration, docs and test fixes',
  maximum_quality: 'the best models at top effort',
  custom: 'only the stored rules decide',
};

/**
 * The choices of a project's own level (P118): following the workspace (the empty value, naming the workspace's level), then
 * each level. A stored level the list does not know (custom) stays choosable.
 */
export function projectLevelOptions(workspace: string, current = ''): { value: string; label: string; hint?: string }[] {
  const levels = current && !LEVELS.includes(current) ? [...LEVELS, current] : LEVELS;
  return [
    { value: '', label: `Follow the workspace (${levelWords(workspace)})`, hint: 'set on the Routing page' },
    ...levels.map((l) => ({ value: l, label: levelWords(l), hint: LEVEL_INTENT[l] })),
  ];
}

/** Where a project's turns get their level, in a sentence. */
export function levelSentence(rl: { effective: string; source: string }): string {
  return rl.source === 'project'
    ? `This project's agent turns run at ${levelWords(rl.effective)}, its own level.`
    : `This project's agent turns run at ${levelWords(rl.effective)}, the workspace's level.`;
}
/** The run kinds a decision is for, as a rule matches them with run.kind. The agent turn kinds come first (decision P63:
 * every turn of an agent is routed), then the app's own model calls. */
export const RUN_KINDS = ['agent_task', 'review', 'room_reply', 'spec', 'tests', 'design', 'security_review', 'lead', 'recommend', 'consult', 'summarize', 'classify'];

const RUN_KIND_LABEL: Record<string, string> = {
  agent_task: 'work',
  review: 'review',
  room_reply: 'room reply',
  spec: 'spec (pm)',
  tests: 'tests first',
  design: 'design (architect)',
  security_review: 'security review',
  lead: 'team lead',
  recommend: 'recommendation',
};

/** The words for a run kind: the turn it was for ("room reply"), or the kind itself when it is not an agent turn. */
export const runKindLabel = (kind: string): string => RUN_KIND_LABEL[kind] ?? kind;

/** The run kinds as select options: the kind (what a rule matches) and its words. */
export const runKindOptions = (): [string, string][] => RUN_KINDS.map((k) => [k, RUN_KIND_LABEL[k] ? `${RUN_KIND_LABEL[k]} (${k})` : k]);

/** Providers the cheap model selection works on: cheap tier and private data class. */
export const cheapProviders = (list: readonly Provider[]): Provider[] => list.filter((p) => p.tier === 'cheap' && p.dataClass === 'private');

/** A wall-time ceiling in minutes as typed (P112): empty is none; otherwise a whole number of minutes from 1 to 1440. */
export function parseMinutes(text: string): { ok: true; seconds: number } | { ok: false; error: string } {
  const t = text.trim();
  if (t === '') return { ok: true, seconds: 0 };
  if (!/^\d+$/.test(t) || Number(t) < 1 || Number(t) > 1440) return { ok: false, error: 'A whole number of minutes from 1 to 1440, or empty for no limit.' };
  return { ok: true, seconds: Number(t) * 60 };
}

/** The minutes of a stored ceiling, for its field ("" when there is none). */
export const minutesOf = (seconds: number | undefined): string => (seconds && seconds > 0 ? String(Math.max(1, Math.round(seconds / 60))) : '');

/** A ceiling in words: "45 min", "2 h", "1 h 30 min" (seconds under a minute as seconds). */
export function ceilingWords(seconds: number): string {
  if (seconds < 60) return `${seconds} s`;
  const m = Math.round(seconds / 60);
  const h = Math.floor(m / 60);
  if (h === 0) return `${m} min`;
  return m % 60 === 0 ? `${h} h` : `${h} h ${m % 60} min`;
}
