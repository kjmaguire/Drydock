// The Wails transport against a TEST DOUBLE of the Wails runtime bindings (Call.ByName, Events.On), shaped after the
// Wails v3.0.0-beta.28 runtime's rejections (see wails.ts). Never run in WebView2: what can be checked without Windows.
import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { EventStore } from '../state/eventStore.ts';
import { ManualScheduler } from '../test/fakes.ts';
import { RpcError } from './transport.ts';
import { BRIDGE_FQN, WailsTransport, wailsErrorKind, type Timers, type WailsRuntimeModule } from './wails.ts';

/** An Error as the Wails runtime builds it from a CallError of the given kind. */
const wailsError = (name: 'RuntimeError' | 'ReferenceError' | 'TypeError', message: string): Error => {
  const e = name === 'TypeError' ? new TypeError(message) : name === 'ReferenceError' ? new ReferenceError(message) : new Error(message);
  if (name === 'RuntimeError') e.name = 'RuntimeError';
  return e;
};

/** TEST DOUBLE: the Wails runtime module, with the bridge's methods answered by the test and events emitted by hand. */
class FakeRuntime implements WailsRuntimeModule {
  calls: { method: string; params: unknown }[] = [];
  subscribes: number[] = [];
  listeners = new Set<(ev: { data: unknown }) => void>();
  answer: (method: string, params: unknown) => unknown = () => null;
  subscribe: (after: number) => Promise<void> = async () => undefined;
  Call = {
    ByName: async (name: string, ...args: unknown[]): Promise<unknown> => {
      if (name === `${BRIDGE_FQN}.Subscribe`) {
        this.subscribes.push(args[0] as number);
        return this.subscribe(args[0] as number);
      }
      if (name !== `${BRIDGE_FQN}.Call`) throw wailsError('ReferenceError', `unknown bound method name '${name}'`);
      const method = args[0] as string;
      const params = JSON.parse(args[1] as string) as unknown;
      this.calls.push({ method, params });
      const r = this.answer(method, params);
      return JSON.stringify(r instanceof Promise ? await r : r);
    },
  };
  Events = {
    On: (_name: string, cb: (ev: { data: unknown }) => void) => {
      this.listeners.add(cb);
      return () => this.listeners.delete(cb);
    },
  };
  emit(seq: number, type = 'x.y'): void {
    for (const l of [...this.listeners]) l({ data: { seq, type, time: '' } });
  }
}

/** TEST DOUBLE: interval timers the test fires by hand. */
class ManualTimers implements Timers {
  next = 0;
  live = new Map<number, () => void>();
  setInterval(fn: () => void, _ms: number): unknown {
    this.live.set(++this.next, fn);
    return this.next;
  }
  clearInterval(h: unknown): void {
    this.live.delete(h as number);
  }
  fire(): void {
    for (const fn of [...this.live.values()]) fn();
  }
}

const tick = async () => {
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
};

function setup() {
  const rt = new FakeRuntime();
  const timers = new ManualTimers();
  const t = WailsTransport.fromRuntime(rt, { heartbeatMs: 1000, timers });
  const log: string[] = [];
  const handlers = {
    onOpen: () => void log.push('open'),
    onEvent: (e: { seq: number }) => void log.push(`event ${e.seq}`),
    onError: (err?: unknown) => void log.push(`error ${err instanceof Error ? err.message : String(err)}`),
  };
  return { rt, timers, t, log, handlers };
}

describe('Wails RPC errors keep their meaning (F21)', () => {
  it('maps the runtime rejections to the kinds the HTTP transport reports', () => {
    expect(wailsErrorKind(wailsError('RuntimeError', 'task not found: t9'))).toBe('bad_request');
    expect(wailsErrorKind(wailsError('RuntimeError', 'unknown method "nope.x"'))).toBe('not_found');
    expect(wailsErrorKind(wailsError('RuntimeError', 'github.com/kjmaguire/drydock/app.Bridge.Call: panic: nil map'))).toBe('server');
    expect(wailsErrorKind(wailsError('ReferenceError', "unknown bound method name 'x'"))).toBe('not_found');
    expect(wailsErrorKind(wailsError('TypeError', 'Failed to fetch'))).toBe('network');
    expect(wailsErrorKind(wailsError('TypeError', 'wrong number of arguments'))).toBe('bad_request');
    expect(wailsErrorKind(new Error('Internal Server Error'))).toBe('server');
    expect(wailsErrorKind(new Error('NetworkError when attempting to fetch resource.'))).toBe('network');
  });

  it('a call carries the kind, the method and the core text; a reply that is not JSON is invalid_response', async () => {
    const rt = new FakeRuntime();
    const t = new WailsTransport(
      {
        Call: async (method) => {
          if (method === 'task.get') throw wailsError('RuntimeError', 'task not found: t9');
          if (method === 'nope.x') throw wailsError('RuntimeError', 'unknown method "nope.x"');
          if (method === 'down') throw wailsError('TypeError', 'Failed to fetch');
          return '{not json';
        },
        Subscribe: async () => undefined,
      },
      rt.Events,
    );
    const err = (m: string) => t.call(m).catch((e: unknown) => e as RpcError);
    expect(await err('task.get')).toMatchObject({ name: 'RpcError', kind: 'bad_request', method: 'task.get', message: 'task not found: t9' });
    expect(await err('nope.x')).toMatchObject({ kind: 'not_found' });
    expect(await err('down')).toMatchObject({ kind: 'network', message: 'Could not reach the Drydock core: Failed to fetch' });
    expect(await err('garbled')).toMatchObject({ kind: 'invalid_response' });
  });
});

describe('Wails stream failure detection (F21)', () => {
  it('reports a Subscribe that fails as a broken stream', async () => {
    const { rt, t, log, handlers } = setup();
    rt.subscribe = async () => {
      throw wailsError('TypeError', 'Failed to fetch');
    };
    t.openEventStream(4, handlers);
    await tick();
    expect(log).toEqual(['error Failed to fetch']);
  });

  it('a call that cannot reach the bridge breaks the open stream once; later callbacks are ignored', async () => {
    const { rt, t, log, handlers } = setup();
    t.openEventStream(0, handlers);
    await tick();
    rt.answer = () => {
      throw wailsError('TypeError', 'Failed to fetch');
    };
    await t.call('project.list').catch(() => undefined);
    await t.call('project.list').catch(() => undefined);
    rt.emit(1);
    expect(log).toEqual(['open', 'error Could not reach the Drydock core: Failed to fetch']);
    expect(rt.listeners.size).toBe(0);
  });

  it('a refused call does not break the stream', async () => {
    const { rt, t, log, handlers } = setup();
    t.openEventStream(0, handlers);
    await tick();
    rt.answer = () => {
      throw wailsError('RuntimeError', 'name is required');
    };
    await t.call('project.create').catch(() => undefined);
    expect(log).toEqual(['open']);
  });

  it('the heartbeat breaks the stream when the bridge stops answering', async () => {
    const { rt, timers, t, log, handlers } = setup();
    let up = true;
    rt.answer = (m) => {
      if (!up) throw wailsError('TypeError', 'Failed to fetch');
      return m === 'events.head' ? 0 : null;
    };
    t.openEventStream(0, handlers);
    await tick();
    timers.fire();
    await tick();
    expect(log).toEqual(['open']);
    up = false;
    timers.fire();
    await tick();
    expect(log).toEqual(['open', 'error Could not reach the Drydock core: Failed to fetch']);
    expect(timers.live.size).toBe(0);
  });

  it('the heartbeat breaks a stream that stopped delivering: the core is ahead one heartbeat later', async () => {
    const { rt, timers, t, log, handlers } = setup();
    let head = 9;
    rt.answer = () => head;
    t.openEventStream(9, handlers);
    await tick();
    head = 11;
    timers.fire(); // the core has 10 and 11; they may be on their way
    await tick();
    rt.emit(10);
    rt.emit(11); // they arrived: all is well
    head = 12;
    timers.fire();
    await tick();
    expect(log).toEqual(['open', 'event 10', 'event 11']);
    timers.fire(); // 12 never arrived
    await tick();
    expect(log.at(-1)).toBe('error the event stream stalled: the core is at seq 12, the stream delivered up to 11');
  });

  it('close stops the heartbeat and the events', async () => {
    const { rt, timers, t, log, handlers } = setup();
    const s = t.openEventStream(0, handlers);
    await tick();
    expect(timers.live.size).toBe(1);
    s.close();
    expect(timers.live.size).toBe(0);
    rt.emit(1);
    expect(log).toEqual(['open']);
  });

  it('with the event store: a break reopens from the last seq, and the reopen re-reads (epoch)', async () => {
    const { rt, timers, t } = setup();
    const sched = new ManualScheduler();
    const store = new EventStore(t, { scheduler: sched, backoff: () => 100 });
    let up = true;
    rt.answer = () => {
      if (!up) throw wailsError('TypeError', 'Failed to fetch');
      return 2;
    };
    store.start();
    await tick();
    rt.emit(1);
    rt.emit(2);
    expect(store.getSnapshot().status).toBe('open');
    up = false;
    timers.fire();
    await tick();
    expect(store.getSnapshot().status).toBe('reconnecting');
    expect(store.getSnapshot().epoch).toBe(0);
    up = true;
    sched.runAll();
    await tick();
    expect(rt.subscribes).toEqual([0, 2]);
    expect(store.getSnapshot().status).toBe('open');
    expect(store.getSnapshot().epoch).toBe(1);
    store.stop();
  });
});

describe('Wails replies and events are checked, not cast (L43)', () => {
  const bridgeAnswering = (answer: (method: string) => unknown) =>
    new WailsTransport({ Call: (async (method: string) => answer(method)) as never, Subscribe: async () => undefined }, { On: () => () => undefined });

  it('a reply that is not JSON text is an invalid_response saying what came, not a value passed on', async () => {
    const t = bridgeAnswering((m) => ({ num: 42, obj: { a: 1 }, flag: false, text: '{"ok":1}' })[m]);
    for (const [method, kind] of [['num', 'number'], ['obj', 'object'], ['flag', 'boolean']] as const) {
      const err = await t.call(method).catch((e: unknown) => e as RpcError);
      expect(err).toMatchObject({ name: 'RpcError', kind: 'invalid_response', method });
      expect((err as Error).message).toBe(`The Drydock shell answered with ${kind}, not JSON text`);
    }
    expect(await t.call('text')).toEqual({ ok: 1 });
  });

  it('keeps taking no answer, null and empty text for a call without a result', async () => {
    const t = bridgeAnswering((m) => ({ none: undefined, nul: null, empty: '' })[m]);
    for (const m of ['none', 'nul', 'empty']) expect(await t.call(m)).toBeNull();
  });

  it('through the runtime module, a reply that is not text is not turned into text first ("42" used to pass for 42)', async () => {
    const rt = new FakeRuntime();
    rt.Call.ByName = async (name: string) => (name === `${BRIDGE_FQN}.Call` ? 42 : null);
    const t = WailsTransport.fromRuntime(rt);
    expect(await t.call('events.head').catch((e: unknown) => e)).toMatchObject({ kind: 'invalid_response' });
  });

  it('ignores an event that is not one, with a console warning that does not repeat its data, and goes on', async () => {
    const { rt, t, log, handlers } = setup();
    const warnings: unknown[][] = [];
    const real = console.warn;
    console.warn = (...a: unknown[]) => void warnings.push(a);
    try {
      t.openEventStream(0, handlers);
      await tick();
      for (const bad of ['junk', 7, null, [], {}, { seq: '3', type: 'a.b' }, { seq: 0, type: 'a.b' }, { seq: 4, type: 9, data: { token: 'secret-token' } }]) for (const l of [...rt.listeners]) l({ data: bad });
      rt.emit(5);
    } finally {
      console.warn = real;
    }
    expect(log).toEqual(['open', 'event 5']);
    expect(warnings.length).toBe(5); // the first few, then it stops
    expect(String(warnings[0]![0])).toContain('malformed event');
    expect(JSON.stringify(warnings)).not.toContain('secret-token');
  });
});
