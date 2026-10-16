// Pure helpers for the Expert packs screen.

import type { Pack, PackAttachment, PackRefreshResult, PackSearchResult } from '../../api/extTypes.ts';

export const zeroTime = (s?: string): boolean => !s || s.startsWith('0001-');

/** What a refresh did, as the core counted it. */
export const refreshText = (r: PackRefreshResult): string =>
  `Version ${r.version}: ${r.pages} pages, ${r.added} added, ${r.changed} changed, ${r.unchanged} unchanged, ${r.skipped} skipped, ${r.failed} failed, ${r.quarantined} quarantined.`;

/** Who in this project the pack is attached to ('no' when nobody). */
export function attachedText(mine: readonly PackAttachment[]): string {
  return mine.length === 0 ? 'no' : mine.map((a) => `${a.role || 'all roles'}${a.agentId ? ` (agent ${a.agentId})` : ''}`).join(', ');
}

export function freshnessText(p: Pack): string {
  return `${p.freshness || 'unknown'}${p.version ? `, ${p.ageDays} day(s) old` : ''}${p.offline ? ', offline' : ''}`;
}

/** The packs a search or question goes to: the chosen ones, or all when none is chosen. */
export const packIds = (selected: readonly string[], all: readonly Pack[]): string[] => (selected.length ? [...selected] : all.map((p) => p.id));

export function searchHeadline(r: PackSearchResult): string {
  const hits = r.hits ?? [];
  return `${r.found ? `${hits.length} hit(s)` : 'Nothing found'} in ${(r.searched ?? []).join(', ') || 'no pack'}`;
}
