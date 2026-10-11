// The event store opens the core's ordered event stream and exposes it to React. It holds no domain state of its own: the
// screens read the core with RPC calls and re-read when a matching event arrives (docs/ui.md sections 1 and 11). The store's
// only jobs are: dedupe by seq, remember the highest seq seen, reopen the stream from that seq after a break, and tell
// listeners about each newly accepted event exactly once.

import type { DrydockEvent } from '../api/types.ts';
import type { EventStream, Transport } from '../api/transport.ts';

export type ConnectionState = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'stopped';

export interface StoreSnapshot {
  readonly status: ConnectionState;
  readonly lastSeq: number;
  /** Accepted events, ascending by seq, capped to maxEvents (the newest are kept). */
  readonly events: readonly DrydockEvent[];
  /** Number of times the stream has been (re)opened; useful to tests and the status line. */
  readonly connects: number;
  /** Bumped when the stream opens again after a break, so readers re-read the core once it is reachable again. */
  readonly epoch: number;
}

export interface Scheduler {
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(h: unknown): void;
}

export interface EventStoreOptions {
  maxEvents?: number;
  /** Delay before reopening: backoff(attempt) with attempt = 1, 2, 3... */
  backoff?: (attempt: number) => number;
  /** Coalesce listener notifications for at most this many ms (0 = synchronous). Event callbacks are never delayed. */
  notifyEveryMs?: number;
  scheduler?: Scheduler;
  initialAfter?: number;
}

const defaultScheduler: Scheduler = {
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

export class EventStore {
  private readonly transport: Transport;
  private readonly maxEvents: number;
  private readonly backoff: (n: number) => number;
  private readonly notifyEveryMs: number;
  private readonly sched: Scheduler;

  private events: DrydockEvent[] = [];
  private seen = new Set<number>();
  /** Everything at or below this has been trimmed from memory and counts as already seen. */
  private floor = 0;
  private lastSeq: number;
  private status: ConnectionState = 'idle';
  private connects = 0;
  private epoch = 0;
  private attempts = 0;
  private stream: EventStream | undefined;
  private gen = 0;
  private retry: unknown;
  private notifyTimer: unknown;
  private snap: StoreSnapshot;
  private listeners = new Set<() => void>();
  private eventListeners = new Set<(e: DrydockEvent) => void>();

  constructor(transport: Transport, opts: EventStoreOptions = {}) {
    this.transport = transport;
    this.maxEvents = opts.maxEvents ?? 5000;
    this.backoff = opts.backoff ?? ((n) => Math.min(250 * 2 ** (n - 1), 5000));
    this.notifyEveryMs = opts.notifyEveryMs ?? 0;
    this.sched = opts.scheduler ?? defaultScheduler;
    this.lastSeq = opts.initialAfter ?? 0;
    this.snap = this.makeSnapshot();
  }

  // --- subscribers (models and components re-read the snapshot when it changes) ---
  subscribe = (l: () => void): (() => void) => {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  };
  getSnapshot = (): StoreSnapshot => this.snap;

  /** Called once for each newly accepted event, in arrival order, never for a duplicate. */
  onEvent(cb: (e: DrydockEvent) => void): () => void {
    this.eventListeners.add(cb);
    return () => this.eventListeners.delete(cb);
  }

  start(): void {
    if (this.status !== 'idle' && this.status !== 'stopped') return;
    this.open('connecting');
  }

  stop(): void {
    this.sched.clearTimeout(this.retry);
    this.retry = undefined;
    this.gen++;
    this.stream?.close();
    this.stream = undefined;
    this.status = 'stopped';
    this.publish(true);
  }

  /** Accept one event. Returns false for a duplicate (same seq seen before, or older than what was trimmed). */
  ingest(e: DrydockEvent): boolean {
    if (!Number.isInteger(e.seq) || e.seq <= this.floor || this.seen.has(e.seq)) return false;
    this.seen.add(e.seq);
    // events normally arrive in order; a late older event is inserted in place
    if (this.events.length === 0 || e.seq > this.events[this.events.length - 1]!.seq) this.events.push(e);
    else {
      let i = this.events.length - 1;
      while (i >= 0 && this.events[i]!.seq > e.seq) i--;
      this.events.splice(i + 1, 0, e);
    }
    if (e.seq > this.lastSeq) this.lastSeq = e.seq;
    if (this.events.length > this.maxEvents) {
      const drop = this.events.splice(0, this.events.length - this.maxEvents);
      for (const d of drop) {
        this.seen.delete(d.seq);
        if (d.seq > this.floor) this.floor = d.seq;
      }
    }
    for (const cb of this.eventListeners) {
      try {
        cb(e);
      } catch {
        /* a listener must not break the stream */
      }
    }
    this.publish(false);
    return true;
  }

  /**
   * Add older events (for the Activity view) without telling event listeners: they are history, not news, and must not
   * trigger re-reads. Duplicates are ignored. Does not move lastSeq backwards.
   */
  backfill(older: readonly DrydockEvent[]): number {
    let added = 0;
    for (const e of older) {
      if (!Number.isInteger(e.seq) || e.seq <= this.floor || this.seen.has(e.seq)) continue;
      this.seen.add(e.seq);
      let i = this.events.length - 1;
      while (i >= 0 && this.events[i]!.seq > e.seq) i--;
      this.events.splice(i + 1, 0, e);
      added++;
    }
    if (added > 0) {
      if (this.events.length > this.maxEvents) {
        const drop = this.events.splice(0, this.events.length - this.maxEvents);
        for (const d of drop) {
          this.seen.delete(d.seq);
          if (d.seq > this.floor) this.floor = d.seq;
        }
      }
      this.publish(true);
    }
    return added;
  }

  // --- connection ---

  private open(status: ConnectionState): void {
    this.status = status;
    this.connects++;
    this.publish(true);
    const gen = ++this.gen;
    this.stream = this.transport.openEventStream(this.lastSeq, {
      onOpen: () => {
        if (gen !== this.gen) return;
        // A stream that opens after a break (or after a failed first open) may have missed what the core changed while
        // it was down: bump epoch now, when the core is reachable again, so live reads re-read once and succeed.
        if (this.attempts > 0) this.epoch++;
        this.attempts = 0;
        this.status = 'open';
        this.publish(true);
      },
      onEvent: (e) => {
        if (gen === this.gen) this.ingest(e);
      },
      onError: () => {
        if (gen !== this.gen) return;
        this.gen++; // late callbacks of the broken stream are ignored
        this.stream?.close();
        this.stream = undefined;
        this.status = 'reconnecting';
        this.attempts++;
        this.publish(true);
        this.retry = this.sched.setTimeout(() => {
          this.retry = undefined;
          if (this.status === 'reconnecting') this.open('reconnecting');
        }, this.backoff(this.attempts));
      },
    });
  }

  // --- snapshot publishing ---

  private makeSnapshot(): StoreSnapshot {
    return { status: this.status, lastSeq: this.lastSeq, events: this.events.slice(), connects: this.connects, epoch: this.epoch };
  }

  private publish(now: boolean): void {
    if (now || this.notifyEveryMs <= 0) {
      this.sched.clearTimeout(this.notifyTimer);
      this.notifyTimer = undefined;
      this.snap = this.makeSnapshot();
      for (const l of this.listeners) l();
      return;
    }
    if (this.notifyTimer !== undefined) return;
    this.notifyTimer = this.sched.setTimeout(() => {
      this.notifyTimer = undefined;
      this.snap = this.makeSnapshot();
      for (const l of this.listeners) l();
    }, this.notifyEveryMs);
  }
}
