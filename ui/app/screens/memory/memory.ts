// Pure helpers for the Memory screen.

import type { Tone } from '../../ui/tokens.ts';

export const MEMORY_KINDS = ['decision', 'convention', 'api_contract', 'rejected', 'known_issue', 'preference'];

/** The status filter: '' is everything. The words are what the legacy "Show" select offered. */
export const MEMORY_FILTERS: { value: string; label: string }[] = [
  { value: '', label: 'all' },
  { value: 'proposed', label: 'proposals' },
  { value: 'accepted', label: 'accepted' },
  { value: 'rejected', label: 'rejected' },
];

export const memoryTone = (status: string): Tone => (status === 'accepted' ? 'ok' : status === 'proposed' ? 'warn' : 'muted');

/** A kind as words: api_contract -> api contract. */
export const kindLabel = (k: string): string => k.replace(/_/g, ' ');

/** The line under an entry's title: who proposed it, when it changed, and whether agents can see it. */
export function memoryMeta(e: { source?: string; status: string }, updated: string): string {
  return `${e.source ? `Proposed by ${e.source}. ` : ''}Updated ${updated}.${e.status === 'proposed' ? ' Agents do not see it until you accept it.' : ''}`;
}
