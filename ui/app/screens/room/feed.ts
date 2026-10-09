// A room's messages in seq order (the legacy useMessages hook as a small model): read in pages from the last seq seen,
// extended when room.message_posted names the room, and pulled again after a reconnect. Nothing here is authoritative:
// the list is what room.messages returned.

import type { Message } from '../../api/types.ts';
import type { EventStore } from '../../state/eventStore.ts';
import { TypedEventTarget } from 'remix/component';

export interface FeedClient {
  roomMessages(roomId: string, afterSeq?: number, limit?: number): Promise<Message[]>;
}

export class MessageFeed extends TypedEventTarget<{ change: Event }> {
  messages: Message[] = [];
  error: Error | undefined;
  loading = true;

  #lastSeq = 0;
  #inFlight = false;
  #again = false;
  #stopped = false;
  #off: (() => void)[] = [];
  #epoch: number;

  readonly roomId: string;
  private client: FeedClient;
  private store: EventStore;
  private pageSize: number;

  constructor(client: FeedClient, store: EventStore, roomId: string, pageSize = 200) {
    super();
    this.client = client;
    this.store = store;
    this.roomId = roomId;
    this.pageSize = pageSize;
    this.#epoch = store.getSnapshot().epoch;
  }

  /** Read the first pages and follow the stream until the signal aborts (or stop()). */
  start(signal?: AbortSignal): this {
    this.#off.push(
      this.store.onEvent((e) => {
        if (e.type !== 'room.message_posted') return;
        const d = e.data as { room?: string } | undefined;
        if (d?.room === this.roomId) void this.pull();
      }),
      this.store.subscribe(() => {
        const epoch = this.store.getSnapshot().epoch;
        if (epoch === this.#epoch) return;
        this.#epoch = epoch;
        void this.pull();
      }),
    );
    signal?.addEventListener('abort', () => this.stop(), { once: true });
    void this.pull();
    return this;
  }

  stop(): void {
    this.#stopped = true;
    for (const off of this.#off.splice(0)) off();
  }

  get lastSeq(): number {
    return this.#lastSeq;
  }

  /** Read every page after the last seq seen. A pull asked for while one runs makes that one go round again. */
  pull = async (): Promise<void> => {
    if (this.#stopped) return;
    if (this.#inFlight) {
      this.#again = true;
      return;
    }
    this.#inFlight = true;
    try {
      for (;;) {
        this.#again = false;
        const page = await this.client.roomMessages(this.roomId, this.#lastSeq, this.pageSize);
        if (this.#stopped) return;
        if (page.length > 0) {
          this.#lastSeq = Math.max(this.#lastSeq, ...page.map((m) => m.seq));
          const have = new Set(this.messages.map((m) => m.id));
          const add = page.filter((m) => !have.has(m.id));
          if (add.length) this.messages = [...this.messages, ...add].sort((a, b) => a.seq - b.seq);
        }
        this.error = undefined;
        if (page.length < this.pageSize && !this.#again) break;
      }
    } catch (e) {
      if (!this.#stopped) this.error = e instanceof Error ? e : new Error(String(e));
    } finally {
      this.#inFlight = false;
      this.loading = false;
      if (!this.#stopped) this.dispatchEvent(new Event('change'));
    }
  };
}
