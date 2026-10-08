// WailsTransport: what the Wails v3 shell provides to the UI (app/bridge_windows.go: Bridge.Call, Bridge.Subscribe and the
// "drydock:event" event). The class below is complete against those two shapes, but it has NEVER run inside WebView2:
//   - how the generated bindings are imported (docs/ui.md section 11) is not wired, so main.tsx does not select it yet;
//   - the exact payload shape of a Wails event callback is assumed (an object with `data`, which may be the event itself
//     or an array whose first element is the event) and must be checked on Windows.
// It is exercised in unit tests against fakes only.

import type { DrydockEvent } from './types';
import { RpcError, type EventStream, type StreamHandlers, type Transport } from './transport';

/** Mirrors app.Bridge. Wails binding functions return promises. */
export interface WailsBridge {
  Call(method: string, params: string): Promise<string>;
  Subscribe(after: number): Promise<void>;
}

/** Mirrors the subset of @wailsio/runtime Events.On the UI uses. Returns an unsubscribe function. */
export interface WailsEvents {
  On(name: string, cb: (ev: { data: unknown }) => void): () => void;
}

export const WAILS_EVENT_NAME = 'drydock:event';

export class WailsTransport implements Transport {
  readonly name = 'wails';
  constructor(
    private readonly bridge: WailsBridge,
    private readonly events: WailsEvents,
  ) {}

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
      const d = Array.isArray(ev.data) ? ev.data[0] : ev.data;
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
