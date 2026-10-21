import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { EventStore } from './eventStore.ts';
import { Live } from './live.ts';
import { FakeTransport, ManualScheduler, ev } from '../test/fakes.ts';

const tick = () => new Promise((r) => setTimeout(r, 0));

describe('Live', () => {
  it('set() shows an answer the caller already has and drops the read in flight', async () => {
    const t = new FakeTransport();
    const store = new EventStore(t, { scheduler: new ManualScheduler(), backoff: () => 100 });
    let reads = 0;
    let release: (v: string) => void = () => undefined;
    const live = new Live(store, {
      load: () => {
        reads++;
        return new Promise<string>((r) => (release = r));
      },
    }).start();
    store.start();
    live.reload();
    await tick();
    expect(reads).toBe(1);
    expect(live.loading).toBe(true);
    // the action's own result arrives first and is shown at once
    live.set('from the action');
    expect(live.data).toBe('from the action');
    expect(live.loading).toBe(false);
    // the older read lands later: it is stale and must not overwrite the newer value
    release('from the read');
    await tick();
    expect(live.data).toBe('from the action');
    expect(reads).toBe(1);
  });

  it('a read with no event matcher re-reads when the stream reopens, not when it breaks (F6)', async () => {
    const t = new FakeTransport();
    const sched = new ManualScheduler();
    const store = new EventStore(t, { scheduler: sched, backoff: () => 100 });
    let up = true;
    let reads = 0;
    let value = 'v1';
    const live = new Live(store, {
      load: async () => {
        reads++;
        if (!up) throw new Error('core is down');
        return value;
      },
    }).start();
    store.start();
    t.current.h.onOpen();
    live.reload();
    await tick();
    expect(reads).toBe(1);
    expect(live.data).toBe('v1');

    // the core goes away: nothing is read (it could only fail), and what was shown stays
    up = false;
    t.current.h.onError(new Error('down'));
    await tick();
    expect(reads).toBe(1);
    expect(live.error).toBe(undefined);
    expect(live.data).toBe('v1');

    // the core comes back with a change no event announced: the reopen re-reads it
    up = true;
    value = 'v2';
    sched.runAll();
    t.current.h.onOpen();
    await tick();
    expect(reads).toBe(2);
    expect(live.data).toBe('v2');
    live.stop();
    store.stop();
  });

  it('reload() returns a promise that settles when its read has, whether it answered, failed or was replaced, and never rejects (L37)', async () => {
    const store = new EventStore(new FakeTransport(), { scheduler: new ManualScheduler(), backoff: () => 100 });
    const releases: ((v: string) => void)[] = [];
    let fail = false;
    const live = new Live(store, {
      load: () => (fail ? Promise.reject(new Error('core is down')) : new Promise<string>((r) => releases.push(r))),
    });
    // it settles only when the read has answered
    let settled = 0;
    void live.reload().then(() => settled++);
    await tick();
    expect(settled).toBe(0);
    releases[0]!('one');
    await tick();
    expect(settled).toBe(1);
    expect(live.data).toBe('one');

    // a read that is replaced by a newer one still settles (its answer is dropped), and so does the newer one
    let first = 0;
    let second = 0;
    void live.reload().then(() => first++);
    await tick();
    void live.reload().then(() => second++);
    await tick();
    releases[2]!('newer');
    await tick();
    expect(second).toBe(1);
    expect(first).toBe(0); // the older read is still in flight: its promise settles with it
    releases[1]!('older');
    await tick();
    expect(first).toBe(1);
    expect(second).toBe(1);
    expect(live.data).toBe('newer');

    // a read that fails settles too, with the failure in `error`
    fail = true;
    await live.reload();
    expect(live.error?.message).toBe('core is down');
    expect(live.data).toBe('newer');
  });
});

describe('Live on streaming events (P87)', () => {
  const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
  it('ignores agent.output unless asked, and then re-reads at most once per interval while the stream goes on', async () => {
    const t = new FakeTransport();
    const store = new EventStore(t, { scheduler: new ManualScheduler(), backoff: () => 100 });
    let quiet = 0;
    let loud = 0;
    const when = (e: { taskId?: string }) => e.taskId === 't1';
    const a = new Live(store, { load: async () => ++quiet, when, debounceMs: 20 }).start();
    const b = new Live(store, { load: async () => ++loud, when, debounceMs: 20, noisy: true }).start();
    store.start();
    t.current.h.onOpen();
    let seq = 0;
    const out = (taskId = 't1') => t.current.h.onEvent({ ...ev(++seq), type: 'agent.output', taskId });
    // a steady stream for about 100 ms, one event every 5 ms: a debounce would wait for its end, the throttle reads during it
    for (let i = 0; i < 20; i++) {
      out();
      await wait(5);
    }
    const during = loud;
    await wait(40);
    expect(quiet).toBe(0);
    expect(during).toBeGreaterThanOrEqual(2);
    expect(loud).toBeLessThanOrEqual(8);
    // another task's stream is not this read's
    const before = loud;
    out('t2');
    await wait(40);
    expect(loud).toBe(before);
    // stop() leaves no read pending, and a later start would not be blocked by it
    out();
    b.stop();
    await wait(40);
    expect(loud).toBe(before);
    a.stop();
    store.stop();
  });
});
