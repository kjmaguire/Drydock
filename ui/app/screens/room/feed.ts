// A room's messages as a small live model, newest first (the pattern of chats/chat-feed.ts): the first read is the newest
// page (room.messages `latest`), older pages are read when the reader scrolls up (loadOlder: room.messages `beforeSeq`),
// room.message_posted for the room reads what is new (`afterSeq`), and the stream reopening after a break reads forward
// again. Nothing here is authoritative: the list is what room.messages returned.
//
// A backward read asks for one message more than a page: getting it back means older messages exist, so the start of the
// room is known without a read that comes back empty.

import type { Message } from '../../api/types.ts';
import type { EventStore } from '../../state/eventStore.ts';
import { TypedEventTarget } from 'remix/component';

export interface FeedClient {
  /** seq > afterSeq, oldest first, at most limit. */
  roomMessages(roomId: string, afterSeq?: number, limit?: number): Promise<Message[]>;
  /** The newest limit messages, oldest first. */
  roomMessagesLatest(roomId: string, limit?: number): Promise<Message[]>;
  /** The limit messages just before beforeSeq, oldest first. */
  roomMessagesBefore(roomId: string, beforeSeq: number, limit?: number): Promise<Message[]>;
}

const bySeq = (a: Message, b: Message) => a.seq - b.seq;

/** Merge two seq-ordered lists by id (the later copy of a message wins), in seq order. */
export function mergeById(a: readonly Message[], b: readonly Message[]): Message[] {
  const m = new Map<string, Message>();
  for (const x of a) m.set(x.id, x);
  for (const x of b) m.set(x.id, x);
  return [...m.values()].sort(bySeq);
}

/** A backward read of up to size + 1 messages as a page of at most size (the newest of them, oldest first) and whether older ones exist. */
export function backPage(read: readonly Message[], size: number): { page: Message[]; hasMore: boolean } {
  const sorted = read.slice().sort(bySeq);
  return { page: sorted.slice(-size), hasMore: sorted.length > size };
}

export class MessageFeed extends TypedEventTarget<{ change: Event }> {
  messages: Message[] = [];
  error: Error | undefined;
  loading = true;
  loadingOlder = false;
  /** Older messages exist above the first one shown. */
  hasMore = false;

  #stopped = false;
  #off: (() => void)[] = [];
  #epoch: number;
  #loaded = false;
  #gen = 0;
  /** Raised by every load that lands: an older page asked for before it may no longer join the list. */
  #listGen = 0;
  #inFlight = false;
  #again = false;

  readonly roomId: string;
  private client: FeedClient;
  private store: EventStore;
  private pageSize: number;

  constructor(client: FeedClient, store: EventStore, roomId: string, pageSize = 100) {
    super();
    this.client = client;
    this.store = store;
    this.roomId = roomId;
    this.pageSize = pageSize;
    this.#epoch = store.getSnapshot().epoch;
  }

  /** Read the newest page and follow the stream until the signal aborts (or stop()). */
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
    void this.load();
    return this;
  }

  stop(): void {
    this.#stopped = true;
    this.#gen++;
    for (const off of this.#off.splice(0)) off();
  }

  /** The newest seq shown (0 before the first read). */
  get lastSeq(): number {
    return this.messages.at(-1)?.seq ?? 0;
  }

  /** Read the newest page. It joins what is shown when it reaches back to it; otherwise the list starts again from it. */
  load = async (): Promise<void> => {
    const my = ++this.#gen;
    const newest = this.lastSeq;
    try {
      const { page, hasMore } = backPage(await this.client.roomMessagesLatest(this.roomId, this.pageSize + 1), this.pageSize);
      if (my !== this.#gen || this.#stopped) return;
      this.#listGen++;
      const firstSeq = page[0]?.seq;
      if (this.#loaded && firstSeq !== undefined && firstSeq <= newest + 1) {
        const oldest = this.messages[0]?.seq ?? firstSeq;
        this.messages = mergeById(this.messages, page);
        if (firstSeq <= oldest) this.hasMore = hasMore;
      } else {
        this.messages = page;
        this.hasMore = hasMore;
      }
      this.#loaded = true;
      this.error = undefined;
    } catch (e) {
      if (my !== this.#gen || this.#stopped) return;
      this.error = e instanceof Error ? e : new Error(String(e));
    }
    this.loading = false;
    this.#emit();
  };

  /** Read what is new after the newest seq shown. More than a page of news restarts the list from the newest page. */
  pull = async (): Promise<void> => {
    if (this.#stopped) return;
    if (!this.#loaded) return this.load();
    if (this.#inFlight) {
      this.#again = true;
      return;
    }
    this.#inFlight = true;
    const gen = this.#listGen;
    try {
      for (;;) {
        this.#again = false;
        const page = await this.client.roomMessages(this.roomId, this.lastSeq, this.pageSize);
        if (this.#stopped) return;
        if (gen !== this.#listGen) break; // a load replaced the list meanwhile
        if (page.length >= this.pageSize) {
          this.#inFlight = false;
          return this.load();
        }
        if (page.length) this.messages = mergeById(this.messages, page);
        this.error = undefined;
        if (!this.#again) break;
      }
    } catch (e) {
      if (!this.#stopped) this.error = e instanceof Error ? e : new Error(String(e));
    } finally {
      this.#inFlight = false;
    }
    if (!this.#stopped) this.#emit();
  };

  /** Read the page before the oldest message shown; returns how many were added. */
  loadOlder = async (): Promise<number> => {
    if (this.loadingOlder || !this.hasMore || this.#stopped) return 0;
    const before = this.messages[0]?.seq ?? 0;
    if (before <= 1) {
      this.hasMore = false; // seqs start at 1: nothing is older
      return 0;
    }
    const gen = this.#listGen;
    this.loadingOlder = true;
    this.#emit();
    let added = 0;
    try {
      const read = await this.client.roomMessagesBefore(this.roomId, before, this.pageSize + 1);
      if (this.#stopped || gen !== this.#listGen) return 0;
      const { page, hasMore } = backPage(
        read.filter((m) => m.seq < before),
        this.pageSize,
      );
      const n = this.messages.length;
      this.messages = mergeById(page, this.messages);
      added = this.messages.length - n;
      this.hasMore = hasMore;
      this.error = undefined;
    } catch (e) {
      if (!this.#stopped) this.error = e instanceof Error ? e : new Error(String(e));
    } finally {
      this.loadingOlder = false;
      if (!this.#stopped) this.#emit();
    }
    return added;
  };

  #emit(): void {
    this.dispatchEvent(new Event('change'));
  }
}
