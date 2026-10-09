// One chat as a small live model: chat.get for the chat and its latest messages, then the chat.* events. chat.message adds
// a stored message (merged by id, in seq order), chat.delta appends to the in-progress assistant text, chat.updated replaces
// the chat, chat.turn ends the streaming state. Older messages are read in pages (chat.messages beforeSeq). After a
// reconnect everything is read again. Nothing here is authoritative: the list is what the core returned.

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

export class ChatFeed extends TypedEventTarget<{ change: Event; completed: Event }> {
  chat: Chat | undefined;
  messages: ChatMessage[] = [];
  hasMore = false;
  loading = true;
  loadingOlder = false;
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

  /** Read the chat and its latest messages (keeps older pages already loaded). */
  load = async (): Promise<void> => {
    const my = ++this.#gen;
    try {
      const d = await this.#client.chatGet(this.id);
      if (my !== this.#gen || this.#stopped) return;
      const oldest = this.messages[0]?.seq;
      const firstLoad = oldest === undefined;
      this.chat = d.chat;
      this.messages = mergeMessages(this.messages, d.messages);
      if (firstLoad || (d.messages[0] && d.messages[0].seq <= (oldest ?? 0))) this.hasMore = d.hasMore;
      if (d.chat.status === 'running') this.streaming ??= { turnId: '', text: '' };
      else this.streaming = undefined;
      this.error = undefined;
    } catch (e) {
      if (my !== this.#gen || this.#stopped) return;
      this.error = e instanceof Error ? e : new Error(String(e));
    }
    this.loading = false;
    this.#emit();
  };

  /** Read the page before the oldest message shown. */
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
      this.messages = mergeMessages(this.messages, page);
      added = this.messages.length - n;
      this.hasMore = page.length >= OLDER_PAGE && page.length > 0;
      this.error = undefined;
    } catch (e) {
      this.error = e instanceof Error ? e : new Error(String(e));
    } finally {
      this.loadingOlder = false;
      this.#emit();
    }
    return added;
  };

  /** A send was accepted: show the turn as running before its first event arrives. */
  sent(turnId: string): void {
    this.streaming = { turnId, text: this.streaming?.turnId === turnId ? this.streaming.text : '' };
    if (this.chat) this.chat = { ...this.chat, status: 'running' };
    this.#emit();
  }

  /** The chat came back from an action (update, allow rule). */
  setChat(c: Chat): void {
    if (c.id !== this.id) return;
    this.chat = c;
    this.#emit();
  }

  #onEvent(e: DrydockEvent): void {
    const d = (e.data ?? {}) as Record<string, unknown>;
    if (!e.type.startsWith('chat.') || d.chatId !== this.id) return;
    switch (e.type) {
      case 'chat.turn_started':
        this.streaming = { turnId: String(d.turnId ?? ''), text: '' };
        if (this.chat) this.chat = { ...this.chat, status: 'running' };
        this.lastTurn = undefined;
        break;
      case 'chat.delta': {
        const turnId = String(d.turnId ?? '');
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
        if (m.role !== 'user') this.dispatchEvent(new Event('completed'));
        break;
      }
      case 'chat.updated': {
        const c = d.chat as Chat | undefined;
        if (c) {
          this.chat = normalizeChat(c);
          if (c.status !== 'running' && this.streaming && !this.streaming.text) this.streaming = undefined;
        }
        break;
      }
      case 'chat.turn':
        this.lastTurn = d as unknown as ChatTurnEnd;
        this.streaming = undefined;
        if (this.chat && this.chat.status === 'running') this.chat = { ...this.chat, status: d.status === 'error' ? 'error' : 'idle' };
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
