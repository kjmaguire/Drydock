// The routing wiring diagram (decision P108): what the router would choose for each level of agent work and for each kind of
// turn, drawn as cards on the left wired to one router node on the right. Pure: the rows, what a preview says in words, and
// where each card and wire goes. The component (wiring-diagram.tsx) asks the core and draws.

import type { RoutePreview } from '../../api/extTypes.ts';
import { modelName } from '../../lib/model-name.ts';
import { LEVELS, RUN_KINDS, runKindLabel } from './routing.ts';

/** A colour for a level's card and wire; a purpose's wire is grey. */
export type WireTone = 'ok' | 'info' | 'accent' | 'warn' | 'err' | 'muted';

/** One card: what it is, the dry-run request that answers it, and its colour. */
export interface WireRow {
  key: string;
  label: string;
  request: Record<string, unknown>;
  tone: WireTone;
}

export interface WireGroup {
  key: string;
  title: string;
  note: string;
  rows: WireRow[];
}

const LEVEL_WORDS: Record<string, string> = {
  cheapest: 'Cheapest',
  fast: 'Fast',
  balanced: 'Balanced',
  quality: 'Quality',
  maximum_quality: 'Maximum quality',
};
const LEVEL_TONE: Record<string, WireTone> = { cheapest: 'ok', fast: 'info', balanced: 'accent', quality: 'warn', maximum_quality: 'err' };

/** The request of a card: the Preview panel's defaults (role backend, private context) with its run kind and level. */
const request = (runKind: string, level: string) => ({ RunKind: runKind, Level: level, Role: 'backend', ContextCeiling: 'private' });

/**
 * The diagram's groups: the levels of an agent's work (cheapest to maximum quality), then every other kind of turn and the
 * app's own model calls at the balanced level.
 */
export function wireGroups(): WireGroup[] {
  return [
    {
      key: 'levels',
      title: 'Levels',
      note: 'agent work',
      rows: LEVELS.map((l) => ({ key: `level-${l}`, label: LEVEL_WORDS[l] ?? l, request: request('agent_task', l), tone: LEVEL_TONE[l] ?? 'accent' })),
    },
    {
      key: 'purposes',
      title: 'Purposes',
      note: 'balanced',
      rows: RUN_KINDS.filter((k) => k !== 'agent_task').map((k) => {
        const words = runKindLabel(k).replace(/_/g, ' ');
        return { key: `kind-${k}`, label: words.charAt(0).toUpperCase() + words.slice(1), request: request(k, 'balanced'), tone: 'muted' as const };
      }),
    },
  ];
}

/** What a card says the router would do: the model by name and its effort, "Blocked" with why, or why it could not be asked. */
export interface WireAnswer {
  model: string;
  detail: string;
  blocked: boolean;
  title: string;
}

export function wireAnswer(r: RoutePreview | Error | undefined): WireAnswer {
  if (!r) return { model: '…', detail: '', blocked: false, title: '' };
  if (r instanceof Error) return { model: 'Not known', detail: '', blocked: true, title: r.message };
  if (r.blocked) return { model: 'Blocked', detail: '', blocked: true, title: r.reason ?? '' };
  const d = r.decision;
  const model = d?.Model ? modelName(d.Model) : 'default model';
  const detail = [d?.Provider ?? '', d?.Effort ? `${d.Effort} effort` : ''].filter(Boolean).join(' · ');
  return { model, detail, blocked: false, title: d?.Reason ?? '' };
}

/**
 * The one reason every card gives, when they all give the same (no provider set up yet): said once above the diagram rather
 * than only in fourteen tooltips. "" when the answers differ or one went through.
 */
export function sharedBlock(answers: readonly (RoutePreview | Error)[] | undefined): string {
  if (!answers || answers.length === 0) return '';
  const reasons = answers.map((r) => (r instanceof Error ? r.message : r.blocked ? (r.reason ?? '') : ''));
  const first = reasons[0]!;
  return first && reasons.every((x) => x === first) ? first : '';
}

/** Sizes of the diagram, in pixels. */
export const WIRE = { row: 52, gap: 8, head: 30, groupGap: 18 } as const;

/** Where each card sits (its top and the middle its wire leaves from), the node's middle and the diagram's height. */
export interface WireLayout {
  rows: { key: string; top: number; mid: number }[];
  heads: { key: string; top: number }[];
  node: number;
  height: number;
}

export function wireLayout(groups: readonly Pick<WireGroup, 'key' | 'rows'>[]): WireLayout {
  const rows: WireLayout['rows'] = [];
  const heads: WireLayout['heads'] = [];
  let y = 0;
  groups.forEach((g, gi) => {
    if (gi > 0) y += WIRE.groupGap;
    heads.push({ key: g.key, top: y });
    y += WIRE.head;
    g.rows.forEach((r, i) => {
      if (i > 0) y += WIRE.gap;
      rows.push({ key: r.key, top: y, mid: y + WIRE.row / 2 });
      y += WIRE.row;
    });
  });
  return { rows, heads, node: y / 2, height: y };
}

/**
 * A wire from a card's middle (x 0) to the node's middle (x 100), in the wires' own box (its width is stretched to the
 * column; the y values are pixels): a curve that leaves and arrives level.
 */
export const wirePath = (from: number, to: number): string => `M0 ${from} C50 ${from} 50 ${to} 100 ${to}`;
