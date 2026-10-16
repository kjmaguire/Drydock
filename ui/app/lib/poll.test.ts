import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { nextPollDelay, POLL_MAX_MS, POLL_MIN_MS, pollWhile } from './poll.ts';

/** Let every promise continuation that can run, run. */
const settle = () => new Promise<void>((res) => setTimeout(res, 0));

// TEST DOUBLES (labelled): a clock the test moves, a wait that records how long it was asked to wait and returns at once, and a page
// that is shown. Nothing here imitates the core: `read` is the test's own function.
function rig(over: { wanted?: () => boolean; visible?: () => Promise<void> } = {}) {
  const ac = new AbortController();
  const time = { ms: 0 };
  const waits: number[] = [];
  return {
    ac,
    time,
    waits,
    opts: {
      signal: ac.signal,
      wanted: over.wanted ?? (() => true),
      now: () => time.ms,
      wait: async (ms: number) => void waits.push(ms),
      visible: over.visible ?? (async () => undefined),
    },
  };
}

describe('how long to wait before the next read (L37)', () => {
  it('is the least interval, or twice the time the last read took when that is longer, and never more than the most', () => {
    expect(POLL_MIN_MS).toBe(500);
    expect(nextPollDelay(0)).toBe(500);
    expect(nextPollDelay(40)).toBe(500);
    expect(nextPollDelay(250)).toBe(500);
    expect(nextPollDelay(300)).toBe(600);
    expect(nextPollDelay(1000)).toBe(2000);
    expect(nextPollDelay(2400)).toBe(4800);
    expect(nextPollDelay(60_000)).toBe(POLL_MAX_MS);
    // a clock that went backwards or a value that is not a number is not a reason to wait less than the least or to stop
    expect(nextPollDelay(-5)).toBe(500);
    expect(nextPollDelay(Number.NaN)).toBe(500);
  });
});

describe('a poll that waits for each read to settle (L37)', () => {
  it('starts no read while the last one is pending, however long that takes', async () => {
    const t = rig();
    let reads = 0;
    let finish: () => void = () => undefined;
    const done = pollWhile({
      ...t.opts,
      read: () => {
        reads++;
        return new Promise<void>((res) => (finish = res));
      },
    });
    await settle();
    expect(reads).toBe(1);
    // the wait before a second read is only planned once the first has settled: a thousand turns of the event loop change nothing
    for (let i = 0; i < 1000; i++) await Promise.resolve();
    await settle();
    expect(reads).toBe(1);
    expect(t.waits).toEqual([500]);
    finish();
    await settle();
    expect(reads).toBe(2);
    expect(t.waits).toEqual([500, 500]);
    t.ac.abort();
    finish();
    await done;
  });

  it('waits longer after a slow read, twice as long as it took, and goes back to the least when reads are quick again', async () => {
    const t = rig();
    const took = [100, 1500, 3000, 10_000, 50];
    let i = 0;
    const done = pollWhile({
      ...t.opts,
      wanted: () => i < took.length,
      read: async () => {
        t.time.ms += took[i++]!;
      },
    });
    await done;
    // before each read: the first wait is the least; then twice the time the read before it took, within the least and the most
    expect(t.waits).toEqual([500, 500, 3000, 5000, 5000]);
  });

  it('stops when nothing more is wanted: no read is started after that, and the wait that was under way ends it', async () => {
    let wanted = true;
    let reads = 0;
    const t = rig({ wanted: () => wanted });
    let release: () => void = () => undefined;
    const opts = {
      ...t.opts,
      wait: async (ms: number) => {
        t.waits.push(ms);
        await new Promise<void>((res) => (release = res));
      },
      read: async () => void reads++,
    };
    const done = pollWhile(opts);
    await settle();
    expect(t.waits).toEqual([500]);
    wanted = false; // the scan finished while the poll was waiting
    release();
    await done;
    expect(reads).toBe(0);
  });

  it('ends when the signal aborts, at once if it is already aborted, and reads nothing more', async () => {
    const t = rig();
    let reads = 0;
    t.ac.abort();
    await pollWhile({ ...t.opts, read: async () => void reads++ });
    expect(reads).toBe(0);
    expect(t.waits).toEqual([]);

    const live = rig();
    let n = 0;
    const done = pollWhile({
      ...live.opts,
      read: async () => {
        if (++n === 3) live.ac.abort();
      },
    });
    await done;
    expect(n).toBe(3);
  });

  it('does not read when the signal aborted during the wait (the wait ends early, the read does not follow)', async () => {
    const t = rig();
    let reads = 0;
    let release: () => void = () => undefined;
    const done = pollWhile({
      ...t.opts,
      wait: async () => new Promise<void>((res) => (release = res)),
      read: async () => void reads++,
    });
    await settle();
    t.ac.abort();
    release();
    await done;
    expect(reads).toBe(0);
  });

  it('keeps going after a read that fails, and does not read while the page is hidden', async () => {
    let shown: () => void = () => undefined;
    let isShown = false;
    const t = rig({
      visible: () => (isShown ? Promise.resolve() : new Promise<void>((res) => (shown = res))),
    });
    let reads = 0;
    const done = pollWhile({
      ...t.opts,
      wanted: () => reads < 2,
      read: async () => {
        reads++;
        throw new Error('the core said no');
      },
    });
    await settle();
    expect(reads).toBe(0); // the page is hidden: the first wait is over and nothing is read
    isShown = true;
    shown();
    await done;
    expect(reads).toBe(2); // a failed read did not end the poll
  });
});
