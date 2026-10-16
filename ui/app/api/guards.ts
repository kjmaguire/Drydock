// Small shape checks for what crosses into the UI from the core and from the browser (audit 2026-10-16, L43). The types in
// api/*.ts say what the core is meant to send; these say what is checked at the few places where a wrong shape would crash
// later, far from the cause. They check what the UI reads, not every field of the type.

import type { DrydockEvent } from './types.ts';

export const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);

/** An event as the core sends it: an object with a positive whole seq and a type. (parseEvent in transport.ts had this check alone.) */
export function isDrydockEvent(v: unknown): v is DrydockEvent {
  return isRecord(v) && Number.isInteger(v.seq) && (v.seq as number) > 0 && typeof v.type === 'string';
}

/**
 * What to say about a value that was meant to be an event but is not, for a warning in the console: its kind, and the seq and
 * type when they are what they should be. Never its data, which can be anything.
 */
export function describeBadEvent(v: unknown): string {
  if (!isRecord(v)) return v === null ? 'null' : Array.isArray(v) ? 'an array' : `a ${typeof v}`;
  const seq = Number.isInteger(v.seq) ? `seq ${String(v.seq)}` : `seq is ${typeof v.seq}`;
  const type = typeof v.type === 'string' ? `type ${JSON.stringify(v.type.slice(0, 60))}` : `type is ${typeof v.type}`;
  return `an object with ${seq} and ${type}`;
}

/** Console warnings, the first `limit` of them: one problem repeated for every event must not flood the console. */
export class LimitedWarn {
  #n = 0;
  constructor(private readonly limit = 5) {}
  warn(message: string): void {
    this.#n++;
    if (this.#n <= this.limit) console.warn(this.#n === this.limit ? `${message} (more of these are not reported)` : message);
  }
  /** How many warnings were asked for, shown or not. */
  get count(): number {
    return this.#n;
  }
}
