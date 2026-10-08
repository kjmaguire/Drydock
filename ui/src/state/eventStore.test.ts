import { describe, expect, it, vi } from 'vitest';
import { EventStore } from './eventStore';
import { FakeTransport, ManualScheduler, ev } from '../test/fakes';

function setup(opts: ConstructorParameters<typeof EventStore>[1] = {}) {
  const t = new FakeTransport();
  const sched = new ManualScheduler();
  const store = new EventStore(t, { scheduler: sched, backoff: (n) => n * 100, ...opts });
  return { t, sched, store };
}

describe('EventStore', () => {
  it('opens the stream from the start and tracks status', () => {
    const { t, store } = setup();
    expect(store.getSnapshot().status).toBe('idle');
    store.start();
    expect(t.opens).toHaveLength(1);
    expect(t.current.after).toBe(0);
    expect(store.getSnapshot().status).toBe('connecting');
    t.current.h.onOpen();
    expect(store.getSnapshot().status).toBe('open');
  });

  it('dedupes by seq and reports each event once', () => {
    const { t, store } = setup();
    const seen: number[] = [];
    store.onEvent((e) => seen.push(e.seq));
    store.start();
    const h = t.current.h;
    h.onEvent(ev(1));
    h.onEvent(ev(2));
    h.onEvent(ev(2));
    h.onEvent(ev(1));
    h.onEvent(ev(3));
    expect(seen).toEqual([1, 2, 3]);
    expect(store.getSnapshot().events.map((e) => e.seq)).toEqual([1, 2, 3]);
    expect(store.getSnapshot().lastSeq).toBe(3);
  });

  it('keeps events ordered when an older one arrives late, and still dedupes it', () => {
    const { store } = setup();
    store.ingest(ev(1));
    store.ingest(ev(5));
    expect(store.ingest(ev(3))).toBe(true);
    expect(store.ingest(ev(3))).toBe(false);
    expect(store.getSnapshot().events.map((e) => e.seq)).toEqual([1, 3, 5]);
    expect(store.getSnapshot().lastSeq).toBe(5);
  });

  it('ignores events with a bad seq', () => {
    const { store } = setup();
    expect(store.ingest(ev(0))).toBe(false);
    expect(store.ingest({ ...ev(1), seq: 1.5 })).toBe(false);
    expect(store.getSnapshot().events).toHaveLength(0);
  });

  it('reconnects after an error from the last seq seen, with backoff, and drops replayed duplicates', () => {
    const { t, sched, store } = setup();
    const seen: number[] = [];
    store.onEvent((e) => seen.push(e.seq));
    store.start();
    t.current.h.onOpen();
    t.current.h.onEvent(ev(1));
    t.current.h.onEvent(ev(2));
    const first = t.current;
    first.h.onError(new Error('boom'));
    expect(first.closed).toBe(true);
    expect(store.getSnapshot().status).toBe('reconnecting');
    expect(t.opens).toHaveLength(1);
    expect(sched.runAll()).toEqual([100]);
    expect(t.opens).toHaveLength(2);
    expect(t.current.after).toBe(2);
    // the server replays from 2 (inclusive of nothing older) and may resend 2; only 3 and 4 are new
    t.current.h.onEvent(ev(2));
    t.current.h.onEvent(ev(3));
    t.current.h.onEvent(ev(4));
    expect(seen).toEqual([1, 2, 3, 4]);
    t.current.h.onOpen();
    expect(store.getSnapshot().status).toBe('open');
    expect(store.getSnapshot().epoch).toBe(1);
  });

  it('backs off further on repeated failures and resets after a successful open', () => {
    const { t, sched, store } = setup();
    store.start();
    t.current.h.onError();
    expect(sched.runAll()).toEqual([100]);
    t.current.h.onError();
    expect(sched.runAll()).toEqual([200]);
    t.current.h.onOpen();
    t.current.h.onError();
    expect(sched.runAll()).toEqual([100]);
  });

  it('ignores callbacks from a stream that was already replaced or closed', () => {
    const { t, sched, store } = setup();
    store.start();
    const old = t.current;
    old.h.onError();
    sched.runAll();
    old.h.onEvent(ev(9)); // late delivery from the broken stream
    old.h.onError(); // must not schedule another reconnect
    expect(store.getSnapshot().events).toHaveLength(0);
    expect(sched.tasks.size).toBe(0);
    store.stop();
    t.current.h.onEvent(ev(10));
    expect(store.getSnapshot().events).toHaveLength(0);
    expect(store.getSnapshot().status).toBe('stopped');
  });

  it('can resume from a known seq', () => {
    const t = new FakeTransport();
    const store = new EventStore(t, { initialAfter: 41 });
    store.start();
    expect(t.current.after).toBe(41);
  });

  it('caps memory and still treats trimmed seqs as seen', () => {
    const { store } = setup({ maxEvents: 3 });
    for (let i = 1; i <= 6; i++) store.ingest(ev(i));
    expect(store.getSnapshot().events.map((e) => e.seq)).toEqual([4, 5, 6]);
    expect(store.ingest(ev(2))).toBe(false);
    expect(store.ingest(ev(7))).toBe(true);
  });

  it('a throwing listener does not break the stream', () => {
    const { store } = setup();
    const seen: number[] = [];
    store.onEvent(() => {
      throw new Error('bad listener');
    });
    store.onEvent((e) => seen.push(e.seq));
    store.ingest(ev(1));
    store.ingest(ev(2));
    expect(seen).toEqual([1, 2]);
  });

  it('coalesces subscriber notifications when notifyEveryMs is set but never delays event callbacks', () => {
    const { store, sched } = setup({ notifyEveryMs: 50 });
    const listener = vi.fn();
    store.subscribe(listener);
    const seen: number[] = [];
    store.onEvent((e) => seen.push(e.seq));
    store.ingest(ev(1));
    store.ingest(ev(2));
    store.ingest(ev(3));
    expect(seen).toEqual([1, 2, 3]);
    expect(listener).not.toHaveBeenCalled();
    expect(sched.runAll()).toEqual([50]);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot().events).toHaveLength(3);
  });

  it('start is idempotent while running', () => {
    const { t, store } = setup();
    store.start();
    store.start();
    expect(t.opens).toHaveLength(1);
  });
});
