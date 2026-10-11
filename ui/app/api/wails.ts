// WailsTransport: what the Wails v3 shell provides to the UI (app/bridge_windows.go: Bridge.Call, Bridge.Subscribe and the
// "drydock:event" event). Facts below were read from the Wails v3.0.0-beta.28 source in the Go module cache, not guessed:
//   - The asset server serves the runtime at /wails/runtime.js (pkg/application/application.go). It exports `Call`
//     (Call.ByName(fqn, ...args) -> Promise) and `Events` (Events.On(name, cb) -> unsubscribe).
//   - A bound method is addressed by its fully qualified name "<Go package path>.<Type>.<Method>"
//     (pkg/application/bindings.go: fqn = PkgPath + "." + Type + "." + Method). Bridge lives in package
//     github.com/kjmaguire/drydock/app, so its methods are "github.com/kjmaguire/drydock/app.Bridge.Call" and ".Subscribe".
//   - EventManager.Emit(name, data) with exactly one data argument delivers that value as `event.data` to the JS callback
//     (pkg/application/event_manager.go; the callback receives a WailsEvent {name, data, sender}).
//   - A failed binding call rejects with an Error whose name says what failed (internal/runtime/desktop/@wailsio/runtime/
//     src/runtime.ts and pkg/application/messageprocessor_call.go): "RuntimeError" when the Go method returned an error
//     (the message is the error text, here the core's own), "ReferenceError" for an unknown bound method, "TypeError" for
//     arguments that do not fit; a request that never reached the asset server rejects with fetch's TypeError ("Failed
//     to fetch"), and a non-JSON error answer with a plain Error carrying the response text.
// What has NOT been verified: none of this has run in WebView2 or on Windows. The runtime is imported from the asset server
// at run time instead of the @wailsio/runtime npm package, because that package's matching version (3.0.0-beta.28) was
// published less than two weeks ago.
//
// Stream health. Wails events carry no "the stream ended" signal, and Bridge.Subscribe returns before the Go side starts
// forwarding, so the transport watches the stream itself and reports a break through onError, as the SSE transport does:
//   - Subscribe rejecting;
//   - any call failing because the bridge cannot be reached (kind "network");
//   - a heartbeat (events.head every heartbeatMs) that fails with "network", "server" or "not_found", or that finds the
//     core holding events the stream has still not delivered one heartbeat later (the Go forwarder stopped).
// The event store then reopens the stream from the last seq it saw and, once Subscribe answers, re-reads (epoch).

import type { DrydockEvent } from './types.ts';
import { RpcError, type EventStream, type RpcErrorKind, type StreamHandlers, type Transport } from './transport.ts';

export const BRIDGE_FQN = 'github.com/kjmaguire/drydock/app.Bridge';
export const WAILS_EVENT_NAME = 'drydock:event';
export const WAILS_RUNTIME_URL = '/wails/runtime.js';

/** Mirrors app.Bridge. */
export interface WailsBridge {
  Call(method: string, params: string): Promise<string>;
  Subscribe(after: number): Promise<void>;
}

/** The subset of the Wails runtime module the UI uses. */
export interface WailsRuntimeModule {
  Call: { ByName(name: string, ...args: unknown[]): Promise<unknown> };
  Events: { On(name: string, cb: (ev: { data: unknown }) => void): () => void };
}

export interface WailsEvents {
  On(name: string, cb: (ev: { data: unknown }) => void): () => void;
}

/** Bridge methods called through the Wails runtime's binding-call mechanism. */
export function bridgeFromRuntime(rt: WailsRuntimeModule): WailsBridge {
  return {
    Call: async (method, params) => String(await rt.Call.ByName(`${BRIDGE_FQN}.Call`, method, params)),
    Subscribe: async (after) => {
      await rt.Call.ByName(`${BRIDGE_FQN}.Subscribe`, after);
    },
  };
}

/** True when the page is hosted by the Wails shell on Windows (WebView2) or any host that injected the Wails globals. */
export function isWailsHost(w: unknown = typeof window === 'undefined' ? undefined : window): boolean {
  const g = w as { chrome?: { webview?: unknown }; _wails?: unknown; wails?: unknown } | undefined;
  return !!g && (!!g.chrome?.webview || !!g._wails || !!g.wails);
}

/** Import /wails/runtime.js from the asset server. The specifier is a variable so the bundler leaves it alone. */
export async function loadWailsRuntime(importer: (url: string) => Promise<unknown> = (u) => import(/* @vite-ignore */ u)): Promise<WailsRuntimeModule> {
  const m = (await importer(WAILS_RUNTIME_URL)) as Partial<WailsRuntimeModule>;
  if (!m?.Call?.ByName || !m.Events?.On) throw new Error('the Wails runtime did not expose Call.ByName and Events.On');
  return m as WailsRuntimeModule;
}

/** Messages fetch uses for a request that never got an answer (Chromium/WebView2, Firefox, Safari). */
const UNREACHABLE = /failed to fetch|networkerror|load failed|network error|err_connection/i;

/**
 * What a rejected binding call means, in the HTTP transport's terms: the core refusing the call is "bad_request" (its own
 * text), an unknown core method "not_found" (as HTTP's 404), a panic in the core "server", a bridge the request never
 * reached "network". See the header for the Wails rejection shapes.
 */
export function wailsErrorKind(err: unknown): RpcErrorKind {
  const name = err instanceof Error ? err.name : '';
  const msg = err instanceof Error ? err.message : String(err);
  switch (name) {
    case 'RuntimeError':
      // the Go method returned an error: the core's text, exactly as the HTTP transport gets it
      if (msg.startsWith('unknown method')) return 'not_found';
      if (/: panic: /.test(msg)) return 'server';
      return 'bad_request';
    case 'ReferenceError':
      return 'not_found'; // the shell has no such bound method: this UI and the shell disagree about the bridge
    case 'TypeError':
      return UNREACHABLE.test(msg) ? 'network' : 'bad_request';
    default:
      return UNREACHABLE.test(msg) ? 'network' : 'server';
  }
}

/** Kinds that say the bridge or the core behind it is not answering (as opposed to refusing one call). */
const BROKEN: ReadonlySet<RpcErrorKind> = new Set(['network', 'server', 'not_found']);

export interface Timers {
  setInterval(fn: () => void, ms: number): unknown;
  clearInterval(h: unknown): void;
}

export interface WailsTransportOptions {
  /** How often an open stream checks the bridge and the core's newest seq (ms); 0 turns the heartbeat off. */
  heartbeatMs?: number;
  timers?: Timers;
}

const defaultTimers: Timers = {
  setInterval: (fn, ms) => {
    const h = setInterval(fn, ms);
    (h as { unref?: () => void }).unref?.(); // never keeps a Node process (tests) alive
    return h;
  },
  clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>),
};

export class WailsTransport implements Transport {
  readonly name = 'wails';
  readonly #heartbeatMs: number;
  readonly #timers: Timers;
  /** The open streams, told when a call finds the bridge unreachable. */
  readonly #streams = new Set<(err: unknown) => void>();

  constructor(
    private readonly bridge: WailsBridge,
    private readonly events: WailsEvents,
    opts: WailsTransportOptions = {},
  ) {
    this.#heartbeatMs = opts.heartbeatMs ?? 15_000;
    this.#timers = opts.timers ?? defaultTimers;
  }

  static fromRuntime(rt: WailsRuntimeModule, opts?: WailsTransportOptions): WailsTransport {
    return new WailsTransport(bridgeFromRuntime(rt), rt.Events, opts);
  }

  async call<T>(method: string, params?: unknown): Promise<T> {
    let out: string;
    try {
      out = await this.bridge.Call(method, JSON.stringify(params ?? {}));
    } catch (err) {
      const kind = wailsErrorKind(err);
      const text = err instanceof Error ? err.message : String(err);
      const e = new RpcError(method, kind, kind === 'network' ? `Could not reach the Drydock core: ${text}` : text);
      if (kind === 'network') for (const fail of [...this.#streams]) fail(e);
      throw e;
    }
    if (!out) return null as T;
    try {
      return JSON.parse(out) as T;
    } catch {
      throw new RpcError(method, 'invalid_response', 'The core returned a reply that is not JSON');
    }
  }

  openEventStream(after: number, h: StreamHandlers): EventStream {
    let closed = false;
    let open = false;
    /** The highest seq this stream delivered (or started after). */
    let seen = after;
    /** The core's head at the last heartbeat, when the stream had not delivered it yet. */
    let owed: number | undefined;
    let probing = false;
    let timer: unknown;

    const stop = () => {
      closed = true;
      off();
      if (timer !== undefined) this.#timers.clearInterval(timer);
      timer = undefined;
      this.#streams.delete(fail);
    };
    const fail = (err: unknown) => {
      if (closed) return;
      stop();
      h.onError(err);
    };
    const heartbeat = async () => {
      if (closed || probing) return;
      probing = true;
      try {
        const head = Number(await this.call<number | null>('events.head')) || 0;
        if (closed) return;
        if (owed !== undefined && seen < owed) {
          fail(new Error(`the event stream stalled: the core is at seq ${head}, the stream delivered up to ${seen}`));
          return;
        }
        owed = head > seen ? head : undefined;
      } catch (err) {
        if (err instanceof RpcError && BROKEN.has(err.kind)) fail(err);
      } finally {
        probing = false;
      }
    };

    const off = this.events.On(WAILS_EVENT_NAME, (ev) => {
      if (closed) return;
      const d = Array.isArray(ev.data) ? ev.data[0] : ev.data; // a single Emit argument arrives unwrapped
      const e = d as DrydockEvent;
      if (e && Number.isInteger(e.seq) && typeof e.type === 'string') {
        if (e.seq > seen) seen = e.seq;
        h.onEvent(e);
      }
    });
    this.#streams.add(fail);
    // Subscribe restarts the Go-side stream from `after`; the store dedupes by seq, so overlap is harmless.
    this.bridge.Subscribe(after).then(
      () => {
        if (closed || open) return;
        open = true;
        h.onOpen();
        if (this.#heartbeatMs > 0) timer = this.#timers.setInterval(() => void heartbeat(), this.#heartbeatMs);
      },
      (err) => fail(err),
    );
    return { close: stop };
  }
}
