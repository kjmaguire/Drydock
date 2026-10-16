// A poll that never piles up reads (audit 2026-10-16, L37). A timer that starts a read every 500 ms starts the next one whether
// or not the last has answered: when the core is busy (a scan is what keeps it busy) the reads queue up behind each other, every
// one making it busier and the answers arriving late and out of step. This poll waits for a read to settle, then waits again
// before the next, and the wait grows with how long the read took, so a slow core is asked less often, not more.
//
// Pure apart from the clock and the wait it is given; ends with the signal or when nothing more is wanted.

import { sleep, whenVisible } from './wait.ts';

/** The least time between the end of one read and the start of the next. */
export const POLL_MIN_MS = 500;
/** The most: a core that answers slowly is still asked now and then, so what is shown does not freeze. */
export const POLL_MAX_MS = 5000;

/**
 * How long to wait before the next read, given how long the last one took: the least interval, or twice the time the read took
 * when that is longer, and never more than the most.
 */
export function nextPollDelay(tookMs: number, minMs = POLL_MIN_MS, maxMs = POLL_MAX_MS): number {
  const wanted = Number.isFinite(tookMs) && tookMs > 0 ? 2 * tookMs : 0;
  return Math.min(maxMs, Math.max(minMs, wanted));
}

export interface PollOptions {
  /** One read. The poll waits for it to settle before it plans the next; a read that fails is the reader's to report. */
  read: () => Promise<unknown>;
  /** Whether another read is wanted, asked before every read. */
  wanted: () => boolean;
  /** Ends the poll (the component is gone). */
  signal: AbortSignal;
  minMs?: number;
  maxMs?: number;
  /** The clock in ms; replaceable in a test. */
  now?: () => number;
  /** Waits `ms`, or less when the signal aborts; replaceable in a test. */
  wait?: (ms: number, signal: AbortSignal) => Promise<void>;
  /** Resolves when the page is shown (a window nobody looks at is not polled); replaceable in a test. */
  visible?: (signal: AbortSignal) => Promise<void>;
}

/**
 * Waits, reads, waits again, until `wanted()` says no or the signal aborts, and then resolves. At most one read is under way at any
 * time. The first read comes after the first wait: the caller has just read.
 */
export async function pollWhile(o: PollOptions): Promise<void> {
  const { read, wanted, signal, minMs = POLL_MIN_MS, maxMs = POLL_MAX_MS, now = () => performance.now(), wait = sleep, visible = (s) => whenVisible(s) } = o;
  let delay = minMs;
  while (!signal.aborted && wanted()) {
    await wait(delay, signal);
    await visible(signal);
    if (signal.aborted || !wanted()) return;
    const began = now();
    try {
      await read();
    } catch {
      // a read reports its own failure where it is shown; the poll goes on
    }
    delay = nextPollDelay(now() - began, minMs, maxMs);
  }
}
