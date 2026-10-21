// One chat as a small live model: chat.get for the chat and its latest messages, then the chat.* events. chat.message adds
// a stored message (merged by id, in seq order), chat.delta appends to the in-progress assistant text, chat.turn ends the
// streaming state, chat.deleted stops the feed. Older messages are read in pages (chat.messages beforeSeq). After a reconnect
// everything is read again. Nothing here is authoritative: what is shown is what the core returned.
//
// The chat itself is only ever a snapshot from the core (chat.get, chat.updated, an action's reply), and of two snapshots the
// one with the higher version wins: the core raises it with every write, so a reply or a reload read before a newer write
// never replaces it. What only this view knows is kept apart from the snapshot: the turn a send started (until a snapshot at
// or after the send's version, or the turn's own events, show it) and the streamed text.

import { TypedEventTarget } from 'remix/component';
import type { Chat, ChatClient, ChatMessage, ChatTurnEnd } from '../../api/chatClient.ts';
import { isChat, isChatMessage, isChatToolResult, isChatTurnEnd, normalizeChat } from '../../api/chatClient.ts';
import { LimitedWarn } from '../../api/guards.ts';
import type { DrydockEvent } from '../../api/types.ts';
import type { EventStore } from '../../state/eventStore.ts';
import { mergeMessages } from './chats.ts';

export interface Streaming {
  turnId: string;
  /** Streamed assistant text not yet stored as a message. */
  text: string;
  /** Streamed reasoning not yet kept on a stored row (chat.thinking, P89). */
  thinking?: string;
}

export const OLDER_PAGE = 100;

type FeedClient = Pick<ChatClient, 'chatGet' | 'chatMessages'>;

export class ChatFeed extends TypedEventTarget<{ change: Event }> {
  chat: Chat | undefined;
  messages: ChatMessage[] = [];
  hasMore = false;
  loading = true;
  loadingOlder = false;
  /** The chat was deleted (here or elsewhere); the feed is stopped. */
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
  /** Raised by every load that lands: an older page asked for before it may no longer join the list. */
  #listGen = 0;
  /** A load has landed; until then a load starts the list from its own page. */
  #loaded = false;
  /** A turn chat.send accepted that no snapshot shows yet, and the chat version that will show it. */
  #pending: { turnId: string; version: number } | undefined;
  #stopped = false;
  readonly #badEvents = new LimitedWarn();

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
    return this.chat?.status === 'running' || !!this.streaming || !!this.#pending;
  }

  /** Read the chat and its latest messages (keeps what is shown when the page joins it). */
  load = async (): Promise<void> => {
    const my = ++this.#gen;
    const first = !this.#loaded;
    // what was shown before this read; messages events add meanwhile are newer and must not make a gap look joined
    const baseNewest = this.messages.at(-1)?.seq;
    try {
      const d = await this.#client.chatGet(this.id);
      if (my !== this.#gen || this.#stopped) return;
      this.#listGen++;
      const firstSeq = d.messages[0]?.seq;
      if (!first && baseNewest !== undefined && firstSeq !== undefined && firstSeq <= baseNewest + 1) {
        // the page joins what was shown: merge; its hasMore counts when it reaches back to the oldest message shown
        const oldest = this.messages[0]?.seq ?? firstSeq;
        this.messages = mergeMessages(this.messages, d.messages);
        if (firstSeq <= oldest) this.hasMore = d.hasMore;
      } else {
        // the first load, or a page that does not join (a long turn stored messages while the stream was down): start the
        // list from the page, keeping only what events added from the page on
        this.messages = mergeMessages(d.messages, firstSeq === undefined ? this.messages : this.messages.filter((m) => m.seq >= firstSeq));
        this.hasMore = d.hasMore;
      }
      this.#loaded = true;
      this.#take(d.chat);
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
    const gen = this.#listGen;
    this.loadingOlder = true;
    this.#emit();
    let added = 0;
    try {
      const page = await this.#client.chatMessages(this.id, before, OLDER_PAGE);
      // stopped, or a load landed meanwhile: the page was asked for against a list that may be gone
      if (this.#stopped || gen !== this.#listGen) return 0;
      const n = this.messages.length;
      this.messages = mergeMessages(this.messages, page.messages);
      added = this.messages.length - n;
      this.hasMore = page.hasMore;
      this.error = undefined;
    } catch (e) {
      if (!this.#stopped) this.error = e instanceof Error ? e : new Error(String(e));
    } finally {
      this.loadingOlder = false;
      this.#emit();
    }
    return added;
  };

  /**
   * chat.send accepted a turn: show it as running until a snapshot at or after `version`, or the turn's own events, show it.
   * A turn that already ended (its chat.turn came first) or that a snapshot already shows is not pending.
   */
  sent(turnId: string, version = 0): void {
    if (this.#stopped || this.lastTurn?.turnId === turnId) return;
    if (version > 0 && this.chat && this.chat.version >= version) return;
    this.#pending = { turnId, version };
    this.#emit();
  }

  /** The chat came back from an action (update, allow rule): kept only when it is not older than the one shown. */
  setChat(c: Chat): void {
    if (c.id !== this.id || this.#stopped) return;
    if (this.#take(c)) this.#emit();
  }

  /** Apply a snapshot of the chat unless it is older (by version) than the one shown. */
  #take(c: Chat): boolean {
    if (this.chat && (c.version ?? 0) < (this.chat.version ?? 0)) return false;
    this.chat = c;
    if (this.#pending && (c.version ?? 0) >= this.#pending.version) this.#pending = undefined; // it shows the send's turn
    if (c.status === 'running') this.streaming ??= { turnId: '', text: '' };
    else if (!this.#pending) this.streaming = undefined;
    return true;
  }

  #onEvent(e: DrydockEvent): void {
    const d = (e.data ?? {}) as Record<string, unknown>;
    if (!e.type.startsWith('chat.') || d.chatId !== this.id) return;
    switch (e.type) {
      case 'chat.turn_started': {
        const turnId = String(d.turnId ?? '');
        this.streaming = { turnId, text: '' };
        if (this.#pending?.turnId === turnId) this.#pending = undefined;
        this.lastTurn = undefined;
        break;
      }
      case 'chat.thinking': {
        const turnId = String(d.turnId ?? '');
        if (this.lastTurn?.turnId === turnId) return;
        const cur = this.streaming;
        const same = cur && (cur.turnId === turnId || !cur.turnId);
        this.streaming = { turnId, text: same ? cur.text : '', thinking: (same ? (cur.thinking ?? '') : '') + String(d.text ?? '') };
        break;
      }
      case 'chat.delta': {
        const turnId = String(d.turnId ?? '');
        if (this.lastTurn?.turnId === turnId) return; // a late delta of a turn that already ended
        const cur = this.streaming;
        const same = cur && (cur.turnId === turnId || !cur.turnId);
        const thinking = same ? cur.thinking : undefined;
        this.streaming = { turnId, text: (same ? cur.text : '') + String(d.text ?? ''), ...(thinking ? { thinking } : {}) };
        break;
      }
      case 'chat.message': {
        const m = d.message;
        if (!isChatMessage(m)) return this.#ignore(e, 'message');
        if (m.chatId !== this.id) return;
        this.messages = mergeMessages(this.messages, [m]);
        // the stored assistant block covers the text streamed so far (the core flushes deltas before it stores)
        if (m.role === 'assistant' && this.streaming) this.streaming = { ...this.streaming, text: '' };
        // the stored tool call or answer keeps the reasoning streamed before it (P89)
        if ((m.role === 'assistant' || m.role === 'tool') && this.streaming?.thinking) this.streaming = { turnId: this.streaming.turnId, text: this.streaming.text };
        break;
      }
      case 'chat.rewound': {
        // a resend replaced the person's message at fromSeq and everything after it (P89)
        const from = Number(d.fromSeq);
        if (!Number.isInteger(from) || from < 1) return this.#ignore(e, 'rewound');
        this.messages = this.messages.filter((m) => m.seq < from);
        break;
      }
      case 'chat.tool_result': {
        if (!isChatToolResult(d)) return this.#ignore(e, 'result');
        const i = this.messages.findIndex((m) => m.id === d.messageId);
        if (i < 0) return; // a page not loaded: the stored row carries the result when it is read
        const next = this.messages.slice();
        next[i] = { ...next[i]!, toolStatus: d.status, toolResult: d.result, toolMs: d.ms };
        this.messages = next;
        break;
      }
      case 'chat.updated': {
        const c = d.chat;
        if (!isChat(c) || c.id !== this.id) return this.#ignore(e, 'chat');
        if (!this.#take(normalizeChat(c))) return;
        break;
      }
      case 'chat.turn':
        if (!isChatTurnEnd(d)) return this.#ignore(e, 'turn');
        this.lastTurn = d;
        this.streaming = undefined;
        if (this.#pending?.turnId === d.turnId) this.#pending = undefined;
        break;
      case 'chat.deleted':
        this.stop(); // no reload or older page lands on a deleted chat
        this.deleted = true;
        this.chat = undefined;
        this.streaming = undefined;
        this.#pending = undefined;
        this.messages = [];
        this.hasMore = false;
        this.loading = false;
        this.error = undefined;
        break;
      default:
        return;
    }
    this.#emit();
  }

  /** An event for this chat whose data is not the shape the feed reads: it is left out, and the console says which event. */
  #ignore(e: DrydockEvent, field: string): void {
    this.#badEvents.warn(`Ignored a ${e.type} event (seq ${e.seq}) whose ${field} is not what the chat feed reads`);
  }

  #emit(): void {
    this.dispatchEvent(new Event('change'));
  }
}
