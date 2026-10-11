// A room's messages as a small live model, newest first (the pattern of chats/chat-feed.ts): the first read finds the newest
// page, older pages are read when the reader scrolls up (loadOlder), room.message_posted for the room reads what is new, and
// the stream reopening after a break reads again. Nothing here is authoritative: the list is what room.messages returned.
//
// room.messages only answers "seq > afterSeq, oldest first, at most limit". Seqs in a room count up from 1 (the core assigns
// MAX(seq)+1), so the newest page is found by probing with limit 1 ("is there anything after x?"): a page read from a hint
// (the read cursor plus the unread count) usually lands on the tail in one call; otherwise the probes gallop forward and
// bisect, a few small calls instead of reading the whole history.

import type { Message } from '../../api/types.ts';
import type { EventStore } from '../../state/eventStore.ts';
import { TypedEventTarget } from 'remix/component';

export interface FeedClient {
  roomMessages(roomId: string, afterSeq?: number, limit?: number): Promise<Message[]>;
}

export type PageReader = (afterSeq: number, limit: number) => Promise<Message[]>;

const bySeq = (a: Message, b: Message) => a.seq - b.seq;

/** Merge two seq-ordered lists by id (the later copy of a message wins), in seq order. */
export function mergeById(a: readonly Message[], b: readonly Message[]): Message[] {
  const m = new Map<string, Message>();
  for (const x of a) m.set(x.id, x);
  for (const x of b) m.set(x.id, x);
  return [...m.values()].sort(bySeq);
}

/**
 * The newest `size` messages of a room (oldest first) and whether older ones exist. `hint` is a seq near the newest (0 when
 * unknown); a wrong hint costs a few probes, never a wrong answer.
 */
export async function latestPage(get: PageReader, size: number, hint = 0): Promise<{ page: Message[]; hasMore: boolean }> {
  const after = Math.max(0, Math.floor(hint) - size);
  const first = await get(after, size + 1); // one more than a page: fewer back means this is the tail
  if (first.length > 0 && first.length <= size) return { page: first.slice().sort(bySeq), hasMore: after > 0 };
  if (first.length === 0 && after === 0) return { page: [], hasMore: false };
  const beyond = async (x: number) => (await get(x, 1)).length > 0; // is there a message with seq > x?
  // bracket the newest seq N: lo < N <= hi (lo = 0 also allows an empty room)
  let lo: number;
  let hi: number;
  if (first.length === 0) {
    lo = 0; // the hint was past the end
    hi = after;
  } else {
    lo = Math.max(...first.map((m) => m.seq)) - 1; // N >= the last seq read
    let step = size;
    while (await beyond(lo + step)) {
      lo += step;
      step *= 2;
    }
    hi = lo + step;
  }
  while (hi - lo > size) {
    const mid = Math.floor((lo + hi) / 2);
    if (await beyond(mid)) lo = mid;
    else hi = mid;
  }
  // N is in (lo, hi] and hi - lo <= size: one read from size below lo covers it
  const from = Math.max(0, lo - size);
  const tail = (await get(from, 2 * size)).slice().sort(bySeq);
  const page = tail.slice(-size);
  return { page, hasMore: from > 0 || tail.length > page.length };
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
  private hint: () => number;

  constructor(client: FeedClient, store: EventStore, roomId: string, pageSize = 100, hint: () => number = () => 0) {
    super();
    this.client = client;
    this.store = store;
    this.roomId = roomId;
    this.pageSize = pageSize;
    this.hint = hint;
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

  #get: PageReader = (after, limit) => this.client.roomMessages(this.roomId, after, limit);

  /** Read the newest page. It joins what is shown when it reaches back to it; otherwise the list starts again from it. */
  load = async (): Promise<void> => {
    const my = ++this.#gen;
    const newest = this.lastSeq;
    try {
      const { page, hasMore } = await latestPage(this.#get, this.pageSize, Math.max(this.hint(), newest));
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
        const page = await this.#get(this.lastSeq, this.pageSize);
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
      this.hasMore = false;
      return 0;
    }
    const after = Math.max(0, before - 1 - this.pageSize);
    const gen = this.#listGen;
    this.loadingOlder = true;
    this.#emit();
    let added = 0;
    try {
      const page = (await this.#get(after, before - 1 - after)).filter((m) => m.seq < before);
      if (this.#stopped || gen !== this.#listGen) return 0;
      const n = this.messages.length;
      this.messages = mergeById(page, this.messages);
      added = this.messages.length - n;
      this.hasMore = after > 0;
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
