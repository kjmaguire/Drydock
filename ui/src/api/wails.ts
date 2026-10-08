// WailsTransport: what the Wails v3 shell provides to the UI (app/bridge_windows.go: Bridge.Call, Bridge.Subscribe and the
// "drydock:event" event). Facts below were read from the Wails v3.0.0-beta.28 source in the Go module cache, not guessed:
//   - The asset server serves the runtime at /wails/runtime.js (pkg/application/application.go). It exports `Call`
//     (Call.ByName(fqn, ...args) -> Promise) and `Events` (Events.On(name, cb) -> unsubscribe).
//   - A bound method is addressed by its fully qualified name "<Go package path>.<Type>.<Method>"
//     (pkg/application/bindings.go: fqn = PkgPath + "." + Type + "." + Method). Bridge lives in package
//     github.com/kjmaguire/drydock/app, so its methods are "github.com/kjmaguire/drydock/app.Bridge.Call" and ".Subscribe".
//   - EventManager.Emit(name, data) with exactly one data argument delivers that value as `event.data` to the JS callback
//     (pkg/application/event_manager.go; the callback receives a WailsEvent {name, data, sender}).
// What has NOT been verified: none of this has run in WebView2 or on Windows. The runtime is imported from the asset server
// at run time instead of the @wailsio/runtime npm package, because that package's matching version (3.0.0-beta.28) was
// published less than two weeks ago.

import type { DrydockEvent } from './types';
import { RpcError, type EventStream, type StreamHandlers, type Transport } from './transport';

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

export class WailsTransport implements Transport {
  readonly name = 'wails';
  constructor(
    private readonly bridge: WailsBridge,
    private readonly events: WailsEvents,
  ) {}

  static fromRuntime(rt: WailsRuntimeModule): WailsTransport {
    return new WailsTransport(bridgeFromRuntime(rt), rt.Events);
  }

  async call<T>(method: string, params?: unknown): Promise<T> {
    try {
      const out = await this.bridge.Call(method, JSON.stringify(params ?? {}));
      return (out ? JSON.parse(out) : null) as T;
    } catch (err) {
      // Bridge.Call returns the core's error text; there is no status code on this path.
      throw new RpcError(method, 'bad_request', err instanceof Error ? err.message : String(err));
    }
  }

  openEventStream(after: number, h: StreamHandlers): EventStream {
    let closed = false;
    const off = this.events.On(WAILS_EVENT_NAME, (ev) => {
      if (closed) return;
      const d = Array.isArray(ev.data) ? ev.data[0] : ev.data; // a single Emit argument arrives unwrapped
      const e = d as DrydockEvent;
      if (e && Number.isInteger(e.seq) && typeof e.type === 'string') h.onEvent(e);
    });
    // Subscribe restarts the Go-side stream from `after`; the store dedupes by seq, so overlap is harmless.
    this.bridge.Subscribe(after).then(
      () => {
        if (!closed) h.onOpen();
      },
      (err) => {
        if (!closed) h.onError(err);
      },
    );
    return {
      close() {
        closed = true;
        off();
      },
    };
  }
}
