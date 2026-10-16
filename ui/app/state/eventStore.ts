// The event store opens the core's ordered event stream and exposes it to React. It holds no domain state of its own: the
// screens read the core with RPC calls and re-read when a matching event arrives (docs/ui.md sections 1 and 11). The store's
// only jobs are: dedupe by seq, remember the highest seq seen, reopen the stream from that seq after a break, and tell
// listeners about each newly accepted event exactly once.

import { describeBadEvent, isDrydockEvent, LimitedWarn } from '../api/guards.ts';
import type { DrydockEvent } from '../api/types.ts';
import type { EventStream, Transport } from '../api/transport.ts';
import { previewOf } from '../lib/preview.ts';

export type ConnectionState = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'stopped';

/** The most characters of an event's payload the ring keeps, as JSON text. */
export const EVENT_PREVIEW_CHARS = 2048;

/**
 * What the ring keeps of an event (audit 2026-10-16, M22): its envelope and a preview of its payload, never the payload. A
 * payload can be megabytes (a diff, a tool result) and the ring holds thousands of events, so it was bounded by count and
 * not by size. The listeners of onEvent still get the event whole, as it arrived; a row that needs more than the preview asks
 * the core (events.replay).
 */
export interface EventRow {
  seq: number;
  time: string;
  type: string;
  projectId?: string;
  runId?: string;
  taskId?: string;
  agentId?: string;
  /** The payload as JSON text, cut at EVENT_PREVIEW_CHARS; empty when the event carried none. */
  preview: string;
  /** The payload was longer than the preview. */
  cut: boolean;
}

/** The row for an event: no reference to its payload is kept. */
export function rowOf(e: DrydockEvent): EventRow {
  const p = previewOf(e.data, EVENT_PREVIEW_CHARS);
  const row: EventRow = { seq: e.seq, time: e.time, type: e.type, preview: p.text, cut: p.cut };
  if (e.projectId !== undefined) row.projectId = e.projectId;
  if (e.runId !== undefined) row.runId = e.runId;
  if (e.taskId !== undefined) row.taskId = e.taskId;
  if (e.agentId !== undefined) row.agentId = e.agentId;
  return row;
}

export interface StoreSnapshot {
  readonly status: ConnectionState;
  readonly lastSeq: number;
  /** Accepted events as rows (envelope and payload preview), ascending by seq, capped to maxEvents (the newest are kept). */
  readonly events: readonly EventRow[];
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

  private events: EventRow[] = [];
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
  private readonly badEvents = new LimitedWarn();
  /** The message each listener last failed with, so one that fails on every event is reported when it changes, not every time. */
  private readonly lastFailure = new WeakMap<object, string>();
  /** Listener failures seen (reported or not). */
  listenerErrors = 0;

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

  /** Events refused for not being events at all (a missing seq or type); each is warned about in the console, the first few. */
  get malformed(): number {
    return this.badEvents.count;
  }

  /** Accept one event. Returns false for a duplicate (same seq seen before, or older than what was trimmed) or a malformed one. */
  ingest(e: DrydockEvent): boolean {
    if (!isDrydockEvent(e)) {
      this.badEvents.warn(`Ignored a malformed event: ${describeBadEvent(e)}`);
      return false;
    }
    if (e.seq <= this.floor || this.seen.has(e.seq)) return false;
    this.seen.add(e.seq);
    const row = rowOf(e);
    // events normally arrive in order; a late older event is inserted in place
    if (this.events.length === 0 || e.seq > this.events[this.events.length - 1]!.seq) this.events.push(row);
    else {
      let i = this.events.length - 1;
      while (i >= 0 && this.events[i]!.seq > e.seq) i--;
      this.events.splice(i + 1, 0, row);
    }
    if (e.seq > this.lastSeq) this.lastSeq = e.seq;
    this.trim();
    for (const cb of this.eventListeners) {
      try {
        cb(e);
      } catch (err) {
        this.listenerFailed(cb, err, 'event listener'); // reported, and the others are still told
      }
    }
    this.publish(false);
    return true;
  }

  private trim(): void {
    if (this.events.length <= this.maxEvents) return;
    const drop = this.events.splice(0, this.events.length - this.maxEvents);
    for (const d of drop) {
      this.seen.delete(d.seq);
      if (d.seq > this.floor) this.floor = d.seq;
    }
  }

  /** A listener threw: say so in the console (once for the same message), count it, and go on. */
  private listenerFailed(listener: object, err: unknown, what: string): void {
    this.listenerErrors++;
    const message = err instanceof Error ? err.message : String(err);
    if (this.lastFailure.get(listener) === message) return;
    this.lastFailure.set(listener, message);
    console.error(`Event store ${what} failed; the stream goes on.`, err);
  }

  /**
   * Add older events (for the Activity view) without telling event listeners: they are history, not news, and must not
   * trigger re-reads. Duplicates are ignored. Does not move lastSeq backwards.
   */
  backfill(older: readonly DrydockEvent[]): number {
    let added = 0;
    for (const e of older) {
      if (!isDrydockEvent(e)) {
        this.badEvents.warn(`Ignored a malformed event in the history: ${describeBadEvent(e)}`);
        continue;
      }
      if (e.seq <= this.floor || this.seen.has(e.seq)) continue;
      this.seen.add(e.seq);
      let i = this.events.length - 1;
      while (i >= 0 && this.events[i]!.seq > e.seq) i--;
      this.events.splice(i + 1, 0, rowOf(e));
      added++;
    }
    if (added > 0) {
      this.trim();
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
      this.notify();
      return;
    }
    if (this.notifyTimer !== undefined) return;
    this.notifyTimer = this.sched.setTimeout(() => {
      this.notifyTimer = undefined;
      this.snap = this.makeSnapshot();
      this.notify();
    }, this.notifyEveryMs);
  }

  /** Tell every subscriber; one that throws is reported and does not stop the others (or the stream). */
  private notify(): void {
    for (const l of [...this.listeners]) {
      try {
        l();
      } catch (err) {
        this.listenerFailed(l, err, 'subscriber');
      }
    }
  }
}
