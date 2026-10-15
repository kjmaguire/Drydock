import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { EventStore } from './eventStore.ts';
import { Live } from './live.ts';
import { FakeTransport, ManualScheduler } from '../test/fakes.ts';

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
});
