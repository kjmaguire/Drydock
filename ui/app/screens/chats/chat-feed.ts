// One chat as a small live model: chat.get for the chat and its latest messages, then the chat.* events. chat.message adds
// a stored message (merged by id, in seq order), chat.delta appends to the in-progress assistant text, chat.updated replaces
// the chat, chat.turn ends the streaming state, chat.deleted empties it. Older messages are read in pages (chat.messages
// beforeSeq). After a reconnect everything is read again. Nothing here is authoritative: the list is what the core returned.
//
// Three answers can arrive after the events that made them stale, and none of them may undo newer state: the chat.send
// response (its turn may already have ended over the event stream), an action's returned chat (compared by updatedAt), and
// a chat.get snapshot taken before events that arrived while it was in flight.

import { TypedEventTarget } from 'remix/component';
import type { Chat, ChatClient, ChatMessage, ChatTurnEnd } from '../../api/chatClient.ts';
import { normalizeChat } from '../../api/chatClient.ts';
import type { DrydockEvent } from '../../api/types.ts';
import type { EventStore } from '../../state/eventStore.ts';
import { mergeMessages } from './chats.ts';

export interface Streaming {
  turnId: string;
  /** Streamed assistant text not yet stored as a message. */
  text: string;
}

export const OLDER_PAGE = 100;

type FeedClient = Pick<ChatClient, 'chatGet' | 'chatMessages'>;

/** Whether chat a is at least as recent as b (updatedAt, ms; a missing value counts as 0). */
const notOlder = (a: Pick<Chat, 'updatedAt'>, b: Pick<Chat, 'updatedAt'> | undefined): boolean => !b || (a.updatedAt ?? 0) >= (b.updatedAt ?? 0);

/** Remembered ended turns (a late chat.send response is checked against them). */
const ENDED_KEEP = 20;

export class ChatFeed extends TypedEventTarget<{ change: Event }> {
  chat: Chat | undefined;
  messages: ChatMessage[] = [];
  hasMore = false;
  loading = true;
  loadingOlder = false;
  /** The chat was deleted (here or elsewhere). */
  deleted = false;
  error: Error | undefined;
  /** Set while a turn runs (from chat.turn_started, the first delta, or a running chat). */
  streaming: Streaming | undefined;
  /** The last turn's end, for a quiet note (fallback, error). */
  lastTurn: ChatTurnEnd | undefined;

  readonly id: string;
  #client: FeedClient;
  #store: EventStore;
  #off: (() => void)[] = [];
  #epoch: number;
  #gen = 0;
  /** Counts this chat's events, so a load can tell whether any arrived while it was in flight. */
  #events = 0;
  #ended: string[] = [];
  #stopped = false;

  constructor(client: FeedClient, store: EventStore, id: string) {
    super();
    this.#client = client;
    this.#store = store;
    this.id = id;
    this.#epoch = store.getSnapshot().epoch;
  }

  start(signal?: AbortSignal): this {
    this.#off.push(
      this.#store.onEvent((e) => this.#onEvent(e)),
      this.#store.subscribe(() => {
        const epoch = this.#store.getSnapshot().epoch;
        if (epoch === this.#epoch) return;
        this.#epoch = epoch;
        void this.load();
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

  get running(): boolean {
    return this.chat?.status === 'running' || !!this.streaming;
  }

  /** Read the chat and its latest messages (keeps older pages already loaded when they join up). */
  load = async (): Promise<void> => {
    const my = ++this.#gen;
    const eventsBefore = this.#events;
    try {
      const d = await this.#client.chatGet(this.id);
      if (my !== this.#gen || this.#stopped || this.deleted) return;
      const raced = this.#events !== eventsBefore; // events newer than this snapshot arrived meanwhile
      const newest = this.messages.at(-1)?.seq;
      const firstNew = d.messages[0]?.seq;
      if (newest === undefined || (firstNew !== undefined && firstNew > newest + 1)) {
        // first load, or the page does not join what is shown (a long turn while the stream was down): start from it
        this.messages = raced ? mergeMessages(d.messages, this.messages.filter((m) => firstNew === undefined || m.seq >= firstNew)) : d.messages;
        this.hasMore = d.hasMore;
      } else {
        this.messages = mergeMessages(this.messages, d.messages);
      }
      if (!raced || notOlder(d.chat, this.chat)) this.chat = d.chat;
      if (!raced) {
        if (d.chat.status === 'running') this.streaming ??= { turnId: '', text: '' };
        else this.streaming = undefined;
      }
      this.error = undefined;
    } catch (e) {
      if (my !== this.#gen || this.#stopped) return;
      this.error = e instanceof Error ? e : new Error(String(e));
    }
    this.loading = false;
    this.#emit();
  };

  /** Read the page before the oldest message shown; returns how many were added. */
  loadOlder = async (): Promise<number> => {
    if (this.loadingOlder || !this.hasMore || this.#stopped) return 0;
    const before = this.messages[0]?.seq ?? 0;
    this.loadingOlder = true;
    this.#emit();
    let added = 0;
    try {
      const page = await this.#client.chatMessages(this.id, before, OLDER_PAGE);
      if (this.#stopped) return 0;
      const n = this.messages.length;
      this.messages = mergeMessages(this.messages, page.messages);
      added = this.messages.length - n;
      this.hasMore = page.hasMore;
      this.error = undefined;
    } catch (e) {
      this.error = e instanceof Error ? e : new Error(String(e));
    } finally {
      this.loadingOlder = false;
      this.#emit();
    }
    return added;
  };

  /** A send was accepted: show the turn as running before its first event arrives, unless it already ended. */
  sent(turnId: string): void {
    if (this.#ended.includes(turnId)) return;
    this.streaming = { turnId, text: this.streaming?.turnId === turnId ? this.streaming.text : '' };
    if (this.chat) this.chat = { ...this.chat, status: 'running' };
    this.#emit();
  }

  /** The chat came back from an action (update, allow rule); an answer older than what is shown is dropped. */
  setChat(c: Chat): void {
    if (c.id !== this.id || this.deleted) return;
    if (!notOlder(c, this.chat)) return;
    this.chat = c;
    this.#emit();
  }

  #onEvent(e: DrydockEvent): void {
    const d = (e.data ?? {}) as Record<string, unknown>;
    if (!e.type.startsWith('chat.') || d.chatId !== this.id) return;
    this.#events++;
    switch (e.type) {
      case 'chat.turn_started':
        this.streaming = { turnId: String(d.turnId ?? ''), text: '' };
        if (this.chat) this.chat = { ...this.chat, status: 'running' };
        this.lastTurn = undefined;
        break;
      case 'chat.delta': {
        const turnId = String(d.turnId ?? '');
        if (this.#ended.includes(turnId)) return;
        const cur = this.streaming;
        this.streaming = { turnId, text: (cur && (cur.turnId === turnId || !cur.turnId) ? cur.text : '') + String(d.text ?? '') };
        break;
      }
      case 'chat.message': {
        const m = d.message as ChatMessage | undefined;
        if (!m || m.chatId !== this.id) return;
        this.messages = mergeMessages(this.messages, [m]);
        // the stored assistant block covers the text streamed so far (the core flushes deltas before it stores)
        if (m.role === 'assistant' && this.streaming) this.streaming = { ...this.streaming, text: '' };
        break;
      }
      case 'chat.updated': {
        const c = d.chat as Chat | undefined;
        if (c && notOlder(c, this.chat)) {
          this.chat = normalizeChat(c);
          if (c.status !== 'running' && this.streaming && !this.streaming.text) this.streaming = undefined;
        }
        break;
      }
      case 'chat.turn': {
        const turnId = String(d.turnId ?? '');
        this.#ended = [...this.#ended, turnId].slice(-ENDED_KEEP);
        this.lastTurn = d as unknown as ChatTurnEnd;
        this.streaming = undefined;
        if (this.chat && this.chat.status === 'running') this.chat = { ...this.chat, status: d.status === 'error' ? 'error' : 'idle' };
        break;
      }
      case 'chat.deleted':
        this.deleted = true;
        this.chat = undefined;
        this.streaming = undefined;
        this.messages = [];
        this.hasMore = false;
        this.loading = false;
        break;
      default:
        return;
    }
    this.#emit();
  }

  #emit(): void {
    this.dispatchEvent(new Event('change'));
  }
}
