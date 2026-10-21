import { expect } from 'remix/assert';
import { describe, it, mock } from 'remix/test';
import type { DrydockEvent } from '../api/types.ts';
import { EVENT_PREVIEW_CHARS, EventStore, rowOf } from './eventStore.ts';
import { FakeTransport, ManualScheduler, ev } from '../test/fakes.ts';

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

  it('bumps epoch when the stream opens again after a break, never while it is down (F6)', () => {
    const { t, sched, store } = setup();
    store.start();
    t.current.h.onOpen();
    expect(store.getSnapshot().epoch).toBe(0); // the first open is not a reconnect
    t.current.h.onError(new Error('down'));
    expect(store.getSnapshot().epoch).toBe(0); // the core is unreachable: a re-read now would only fail
    sched.runAll();
    t.current.h.onError(new Error('still down'));
    sched.runAll();
    expect(store.getSnapshot().epoch).toBe(0);
    t.current.h.onOpen();
    expect(store.getSnapshot().epoch).toBe(1); // back: re-read once
    t.current.h.onOpen(); // a second open signal of the same stream is not another reconnect
    expect(store.getSnapshot().epoch).toBe(1);
  });

  it('bumps epoch when the first open failed and a retry succeeds', () => {
    const { t, sched, store } = setup();
    store.start();
    t.current.h.onError(new Error('core not up yet'));
    sched.runAll();
    t.current.h.onOpen();
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
    const real = console.error;
    console.error = () => {}; // reported, and checked below ("what the store does with bad input")
    try {
      store.ingest(ev(1));
      store.ingest(ev(2));
    } finally {
      console.error = real;
    }
    expect(seen).toEqual([1, 2]);
  });

  it('coalesces subscriber notifications when notifyEveryMs is set but never delays event callbacks', () => {
    const { store, sched } = setup({ notifyEveryMs: 50 });
    const listener = mock.fn();
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

  it('backfill adds history without telling event listeners and never moves lastSeq back', () => {
    const { store } = setup({ initialAfter: 100 });
    const seen: number[] = [];
    store.onEvent((e) => seen.push(e.seq));
    store.ingest(ev(101));
    expect(store.backfill([ev(50), ev(51), ev(101), ev(52)])).toBe(3);
    expect(store.backfill([ev(50)])).toBe(0);
    expect(seen).toEqual([101]);
    expect(store.getSnapshot().events.map((e) => e.seq)).toEqual([50, 51, 52, 101]);
    expect(store.getSnapshot().lastSeq).toBe(101);
  });
});

/** Runs `fn` with console.warn / console.error recorded instead of printed. */
function quiet<T>(fn: (calls: { warn: unknown[][]; error: unknown[][] }) => T): T {
  const calls = { warn: [] as unknown[][], error: [] as unknown[][] };
  const { warn, error } = console;
  console.warn = (...a: unknown[]) => void calls.warn.push(a);
  console.error = (...a: unknown[]) => void calls.error.push(a);
  try {
    return fn(calls);
  } finally {
    console.warn = warn;
    console.error = error;
  }
}

describe('the ring holds previews, not payloads (M22)', () => {
  const big = { diff: 'x'.repeat(5_000_000), files: Array.from({ length: 1000 }, (_, i) => `file${i}.go`) };

  it('keeps the envelope and at most about 2 KB of the payload, and tells the listeners the event whole', () => {
    const { store } = setup();
    const heard: DrydockEvent[] = [];
    store.onEvent((e) => void heard.push(e));
    const e = ev(1, 'task.diff', { projectId: 'p1', taskId: 't1', agentId: 'a1', runId: 'r1', data: big });
    store.ingest(e);
    const row = store.getSnapshot().events[0]!;
    expect(row).toMatchObject({ seq: 1, type: 'task.diff', projectId: 'p1', taskId: 't1', agentId: 'a1', runId: 'r1', cut: true });
    expect(row.preview.length).toBe(EVENT_PREVIEW_CHARS);
    expect(row.preview.startsWith('{"diff":"xxxx')).toBe(true);
    expect('data' in row).toBe(false); // the payload is not in the ring
    expect(JSON.stringify(row).length).toBeLessThan(EVENT_PREVIEW_CHARS + 500);
    expect(heard).toEqual([e]);
    expect(heard[0]!.data).toBe(big); // a listener that needs the payload gets the one that arrived
  });

  it('keeps a small payload whole, and says nothing was cut', () => {
    const { store } = setup();
    store.ingest(ev(1, 'agent.created', { data: { name: 'Pay' } }));
    store.ingest(ev(2, 'license.changed'));
    const [a, b] = store.getSnapshot().events;
    expect([a!.preview, a!.cut]).toEqual(['{"name":"Pay"}', false]);
    expect([b!.preview, b!.cut]).toEqual(['', false]);
    expect(rowOf(ev(3, 'x.y', { data: [1, 2] })).preview).toBe('[1,2]');
  });

  it('keeps previews of the events the Activity page backfills too', () => {
    const { store } = setup();
    store.backfill([ev(5, 'task.diff', { data: big }), ev(4, 'x.y')]);
    const rows = store.getSnapshot().events;
    expect(rows.map((r) => r.seq)).toEqual([4, 5]);
    expect(rows[1]!.preview.length).toBe(EVENT_PREVIEW_CHARS);
    expect('data' in rows[1]!).toBe(false);
  });

  it('holds a few KB per event however big the payloads, which is what bounds the ring by size and not only by count', () => {
    const { store } = setup({ maxEvents: 50 });
    for (let i = 1; i <= 200; i++) store.ingest(ev(i, 'task.diff', { data: { diff: 'y'.repeat(200_000) } }));
    const rows = store.getSnapshot().events;
    expect(rows).toHaveLength(50);
    expect(JSON.stringify(rows).length).toBeLessThan(50 * (EVENT_PREVIEW_CHARS + 500));
  });
});

describe('what the store does with bad input (L43)', () => {
  it('ignores a malformed event with a console warning that does not repeat its data, and stops warning after a few', () => {
    quiet((calls) => {
      const { store } = setup();
      const seen: number[] = [];
      store.onEvent((e) => void seen.push(e.seq));
      const bad = [null, undefined, 'event', 7, [], {}, { seq: 'x', type: 'a.b' }, { seq: 3 }, { seq: 0, type: 'a.b' }, { seq: 2.5, type: 'a.b' }, { seq: 4, type: 5, data: { secret: 'hunter2' } }];
      for (const b of bad) expect(store.ingest(b as unknown as DrydockEvent)).toBe(false);
      expect(store.ingest(ev(1))).toBe(true);
      expect(seen).toEqual([1]);
      expect(store.getSnapshot().events.map((e) => e.seq)).toEqual([1]);
      expect(store.malformed).toBe(bad.length);
      expect(calls.warn.length).toBe(5);
      expect(String(calls.warn[0]![0])).toContain('malformed event');
      expect(JSON.stringify(calls.warn)).not.toContain('hunter2');
      expect(String(calls.warn[4]![0])).toContain('more of these are not reported');
      // a duplicate is not malformed: it is the normal overlap after a reconnect
      expect(store.ingest(ev(1))).toBe(false);
      expect(store.malformed).toBe(bad.length);
    });
  });

  it('a listener that throws is reported once for the same failure, and the other listeners are still told', () => {
    quiet((calls) => {
      const { store } = setup();
      const seen: number[] = [];
      store.onEvent(() => {
        throw new Error('bad listener');
      });
      store.onEvent((e) => void seen.push(e.seq));
      store.ingest(ev(1));
      store.ingest(ev(2));
      store.ingest(ev(3));
      expect(seen).toEqual([1, 2, 3]);
      expect(store.listenerErrors).toBe(3);
      expect(calls.error).toHaveLength(1); // the same failure of the same listener is not repeated
      expect(String(calls.error[0]![0])).toContain('event listener');
      expect((calls.error[0]![1] as Error).message).toBe('bad listener');
    });
  });

  it('reports a listener again when it fails differently, and each failing listener on its own', () => {
    quiet((calls) => {
      const { store } = setup();
      let n = 0;
      store.onEvent(() => {
        throw new Error(`first ${n++ < 1 ? 'kind' : 'other kind'}`);
      });
      store.onEvent(() => {
        throw new Error('second');
      });
      store.ingest(ev(1));
      store.ingest(ev(2));
      expect(calls.error.map((c) => (c[1] as Error).message)).toEqual(['first kind', 'second', 'first other kind']);
    });
  });

  it('a subscriber that throws does not stop the others or the stream', () => {
    quiet((calls) => {
      const { store } = setup();
      const told: number[] = [];
      store.subscribe(() => {
        throw new Error('bad subscriber');
      });
      store.subscribe(() => void told.push(store.getSnapshot().events.length));
      expect(store.ingest(ev(1))).toBe(true);
      expect(store.ingest(ev(2))).toBe(true);
      expect(told).toEqual([1, 2]);
      expect(calls.error).toHaveLength(1);
      expect(String(calls.error[0]![0])).toContain('subscriber');
      store.stop();
      expect(store.getSnapshot().status).toBe('stopped');
    });
  });
});
