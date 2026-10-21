// Transport abstraction. The UI talks to the Go core through exactly two operations: call(method, params) and an ordered
// event stream with replay from a sequence number. HttpTransport (dev server, HTTP + Server-Sent Events) is implemented
// and tested here. WailsTransport (src/api/wails.ts) is the shape the Wails shell will provide; it has not run in WebView2.

import { isDrydockEvent, LimitedWarn } from './guards.ts';
import type { DrydockEvent } from './types.ts';

export type RpcErrorKind =
  | 'unauthorized' // 401: missing or wrong token
  | 'forbidden' // 403: host or origin refused
  | 'not_found' // 404: unknown method
  | 'bad_request' // 400: the core refused the call; message is the core's own error text
  | 'server' // 5xx
  | 'network' // the request never completed
  | 'invalid_response'; // the reply was not the expected JSON envelope

export class RpcError extends Error {
  readonly method: string;
  readonly kind: RpcErrorKind;
  readonly status: number | undefined;
  constructor(method: string, kind: RpcErrorKind, message: string, status?: number) {
    super(message);
    this.name = 'RpcError';
    this.method = method;
    this.kind = kind;
    this.status = status;
  }
}

export interface StreamHandlers {
  onOpen(): void;
  onEvent(e: DrydockEvent): void;
  /** The stream broke. The caller decides when to reopen (with the last sequence it saw). */
  onError(err?: unknown): void;
}

export interface EventStream {
  close(): void;
}

export interface Transport {
  readonly name: string;
  call<T>(method: string, params?: unknown): Promise<T>;
  /** Open an ordered stream of every persisted event with seq > after, then live events. */
  openEventStream(after: number, handlers: StreamHandlers): EventStream;
}

// --- HTTP + SSE (cmd/drydock-dev, cmd/drydock-e2e) ---

export interface EventSourceLike {
  onopen: ((ev: unknown) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  addEventListener(type: string, listener: (ev: { data: string }) => void): void;
  close(): void;
}
export type EventSourceFactory = (url: string) => EventSourceLike;

export interface HttpTransportOptions {
  token: string;
  /** Origin + path prefix of the Go server; '' means same origin. */
  baseUrl?: string;
  fetchImpl?: typeof fetch;
  eventSourceFactory?: EventSourceFactory;
}

export class HttpTransport implements Transport {
  readonly name = 'http';
  private readonly token: string;
  private readonly base: string;
  private readonly fetchImpl: typeof fetch;
  private readonly esFactory: EventSourceFactory;
  private readonly badEvents = new LimitedWarn();

  constructor(opts: HttpTransportOptions) {
    this.token = opts.token;
    this.base = (opts.baseUrl ?? '').replace(/\/+$/, '');
    this.fetchImpl = opts.fetchImpl ?? ((...a) => fetch(...a));
    this.esFactory = opts.eventSourceFactory ?? ((url) => new EventSource(url) as unknown as EventSourceLike);
  }

  async call<T>(method: string, params?: unknown): Promise<T> {
    let resp: Response;
    try {
      resp = await this.fetchImpl(`${this.base}/rpc/${encodeURIComponent(method)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Drydock-Token': this.token },
        body: JSON.stringify(params ?? {}),
        credentials: 'omit',
        referrerPolicy: 'no-referrer',
      });
    } catch (err) {
      throw new RpcError(method, 'network', `Could not reach the Drydock core: ${err instanceof Error ? err.message : String(err)}`);
    }
    const text = await resp.text().catch(() => '');
    let env: { result?: unknown; error?: string } | undefined;
    try {
      env = text ? (JSON.parse(text) as { result?: unknown; error?: string }) : undefined;
    } catch {
      env = undefined;
    }
    if (!resp.ok) {
      // The core answers {"error": "..."} for refused calls; its guards (token, host, origin) answer plain text.
      const msg = (env && typeof env.error === 'string' && env.error) || text.trim() || `HTTP ${resp.status}`;
      throw new RpcError(method, kindFor(resp.status), msg, resp.status);
    }
    if (!env || typeof env !== 'object') {
      throw new RpcError(method, 'invalid_response', 'The core returned a reply that is not JSON', resp.status);
    }
    if (typeof env.error === 'string' && env.error) throw new RpcError(method, 'bad_request', env.error, resp.status);
    return (env.result ?? null) as T;
  }

  openEventStream(after: number, h: StreamHandlers): EventStream {
    const url = `${this.base}/events?after=${encodeURIComponent(String(after))}&token=${encodeURIComponent(this.token)}`;
    const es = this.esFactory(url);
    let closed = false;
    es.onopen = () => {
      if (!closed) h.onOpen();
    };
    es.onerror = (err) => {
      if (!closed) h.onError(err);
    };
    es.addEventListener('drydock', (ev) => {
      if (closed) return;
      const parsed = parseEvent(ev.data);
      if (parsed) h.onEvent(parsed);
      else this.badEvents.warn('Ignored a malformed event from the core (not JSON, or without a seq and a type)');
    });
    return {
      close() {
        closed = true;
        es.close();
      },
    };
  }
}

function kindFor(status: number): RpcErrorKind {
  if (status === 401) return 'unauthorized';
  if (status === 403) return 'forbidden';
  if (status === 404) return 'not_found';
  if (status >= 500) return 'server';
  return 'bad_request';
}

/** Parse one SSE data payload. Returns undefined for anything that is not an event with a positive integer seq. */
export function parseEvent(data: string): DrydockEvent | undefined {
  try {
    const e: unknown = JSON.parse(data);
    if (isDrydockEvent(e)) return e;
  } catch {
    /* fall through */
  }
  return undefined;
}

// --- token handling for the dev transport ---

interface StorageLike {
  getItem(k: string): string | null;
  setItem(k: string, v: string): void;
}
const TOKEN_KEY = 'drydock.token';

/**
 * The token comes from the page URL (?token=) once and is then kept in sessionStorage, so a reload keeps working. Storage
 * can be missing or throw (private windows, blocked site data); the URL token still works then.
 */
export function resolveToken(search: string, storage: StorageLike | undefined): string {
  const fromUrl = new URLSearchParams(search).get('token') ?? '';
  if (fromUrl) {
    try {
      storage?.setItem(TOKEN_KEY, fromUrl);
    } catch {
      /* ignore */
    }
    return fromUrl;
  }
  try {
    return storage?.getItem(TOKEN_KEY) ?? '';
  } catch {
    return '';
  }
}
