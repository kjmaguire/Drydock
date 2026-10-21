// Pure helpers for the Memory screen.

import type { Tone } from '../../ui/tokens.ts';

// The most a proposal may hold, as the core checks it (memory.MaxTitleChars and memory.MaxBodyChars, audit 2026-10-16, M26): a
// person accepts a proposal from what the page shows, and the entry then reaches every later agent as the project's own memory,
// so a proposal must be short enough to be read whole. An older entry may be longer; the page shows all of it and says so.
export const MEMORY_TITLE_LIMIT = 200;
export const MEMORY_BODY_LIMIT = 8000;

/** How many characters a text has, counted as the core counts them: a character is one, however many UTF-16 units it takes. */
export function charCount(text: string): number {
  let n = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i);
    if (c >= 0xd800 && c <= 0xdbff && i + 1 < text.length) {
      const d = text.charCodeAt(i + 1);
      if (d >= 0xdc00 && d <= 0xdfff) i++;
    }
    n++;
  }
  return n;
}

/** The line beside an entry's text box: its length, and that it is longer than a proposal may be now when it is. */
export function bodyNote(body: string): { count: number; over: boolean; text: string } {
  const count = charCount(body);
  const over = count > MEMORY_BODY_LIMIT;
  const length = `${count.toLocaleString()} character${count === 1 ? '' : 's'}`;
  return {
    count,
    over,
    text: over ? `${length}. This is longer than the ${MEMORY_BODY_LIMIT.toLocaleString()} characters a proposal may have now. All of it is shown above.` : length,
  };
}

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

/**
 * Where a proposal came from, in words (P92): an agent while it worked on a task ("agent:<id> task:<id>"), a person's change
 * request on a review ("review:<task>"), a person's edit of a generated instruction file ("edit:<file>"), or as stored.
 */
export function memorySource(source: string | undefined, nameOf: (agentId: string) => string = (x) => x, taskOf: (taskId: string) => string = (x) => x): string {
  const s = (source ?? '').trim();
  if (!s) return '';
  const agent = /^agent:(\S+)(?:\s+task:(\S+))?$/.exec(s);
  if (agent) return agent[2] ? `${nameOf(agent[1]!)}, working on ${taskOf(agent[2])}` : nameOf(agent[1]!);
  const review = /^review:(\S+)$/.exec(s);
  if (review) return `your change request on ${taskOf(review[1]!)}`;
  const edit = /^edit:(.+)$/.exec(s);
  if (edit) return `your edit of ${edit[1]}`;
  return s;
}

/** The line under an entry's title: who proposed it, when it changed, and whether agents can see it. */
export function memoryMeta(e: { source?: string; status: string }, updated: string, who = e.source ?? ''): string {
  return `${who ? `Proposed by ${who}. ` : ''}Updated ${updated}.${e.status === 'proposed' ? ' Agents do not see it until you accept it.' : ''}`;
}
