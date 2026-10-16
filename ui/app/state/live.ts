// A live read: an RPC read plus the events that invalidate it. The screen state is a projection of what the core holds,
// re-read when the event log says something relevant changed or when the stream reconnects. Nothing here is authoritative.

import { TypedEventTarget } from 'remix/component';
import type { DrydockEvent } from '../api/types.ts';
import type { EventStore } from './eventStore.ts';

/** Events that arrive in bulk while an agent streams; they never invalidate a read by themselves. */
export const isNoisy = (e: DrydockEvent): boolean => e.type === 'agent.output' || e.type === 'workflow.node_chunk';

/** True when the event type starts with any of the prefixes. */
export const starts = (e: DrydockEvent, ...prefixes: string[]): boolean => prefixes.some((p) => e.type.startsWith(p));

export interface LiveOptions<T> {
  load: () => Promise<T>;
  /** Accepted events for which this returns true re-read (debounced). */
  when?: (e: DrydockEvent) => boolean;
  debounceMs?: number;
}

/**
 * One live read. `change` fires whenever data, error or loading change. Responses that arrive out of order are dropped, so
 * a slow answer for an old key never replaces a newer one.
 */
export class Live<T> extends TypedEventTarget<{ change: Event }> {
  data: T | undefined = undefined;
  error: Error | undefined = undefined;
  loading = false;

  #store: EventStore;
  #opts: LiveOptions<T>;
  #gen = 0;
  #timer: ReturnType<typeof setTimeout> | undefined;
  #off: (() => void)[] = [];
  #epoch: number;
  #enabled = true;

  constructor(store: EventStore, opts: LiveOptions<T>) {
    super();
    this.#store = store;
    this.#opts = opts;
    this.#epoch = store.getSnapshot().epoch;
  }

  /** Subscribe to the event stream. Call stop() (or abort the signal) to release it. */
  start(signal?: AbortSignal): this {
    const when = this.#opts.when;
    if (when) {
      this.#off.push(
        this.#store.onEvent((e) => {
          if (!this.#enabled || isNoisy(e) || !when(e)) return;
          clearTimeout(this.#timer);
          this.#timer = setTimeout(() => this.reload(), this.#opts.debounceMs ?? 100);
        }),
      );
    }
    this.#off.push(
      this.#store.subscribe(() => {
        const epoch = this.#store.getSnapshot().epoch;
        if (epoch === this.#epoch) return;
        this.#epoch = epoch;
        if (this.#enabled) this.reload();
      }),
    );
    signal?.addEventListener('abort', () => this.stop(), { once: true });
    return this;
  }

  stop(): void {
    clearTimeout(this.#timer);
    for (const off of this.#off.splice(0)) off();
    this.#gen++;
  }

  /**
   * Read again, keeping the current data on screen until the answer arrives. The promise it returns settles when this read has: it
   * answered, failed (the failure is in `error`), or a newer read took its place. It never rejects, so a poll can wait for it.
   */
  reload = (): Promise<void> => {
    if (!this.#enabled) return Promise.resolve();
    const my = ++this.#gen;
    this.loading = true;
    return Promise.resolve()
      .then(() => this.#opts.load())
      .then(
        (data) => {
          if (my !== this.#gen) return;
          this.data = data;
          this.error = undefined;
          this.loading = false;
          this.#emit();
        },
        (err: unknown) => {
          if (my !== this.#gen) return;
          this.error = err instanceof Error ? err : new Error(String(err));
          this.loading = false;
          this.#emit();
        },
      );
  };

  /**
   * Show an answer the caller already has (the result of the action that changed it) instead of reading again; a read in
   * flight is dropped, so it cannot overwrite the newer value when it lands.
   */
  set(data: T): void {
    this.#gen++;
    this.data = data;
    this.error = undefined;
    this.loading = false;
    this.#emit();
  }

  /** Forget the data (the key changed) and, when enabled, read again. Does not emit synchronously, so it is safe in render. */
  reset(enabled = true): void {
    this.#enabled = enabled;
    this.#gen++;
    this.data = undefined;
    this.error = undefined;
    this.loading = enabled;
    clearTimeout(this.#timer);
  }

  #emit(): void {
    this.dispatchEvent(new Event('change'));
  }
}
